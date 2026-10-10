package organization

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

// Opt-in authority must come from an accepted, content-addressed policy, not a
// PM output. A zero value is disabled. Production configuration is unchanged.
type PlanAmendmentPolicy struct {
	ContractVersion string              `json:"contract_version"`
	ManifestDigest  kernel.Digest       `json:"manifest_digest"`
	Authority       kernel.PrincipalRef `json:"authority"`
	AllowedRoots    []string            `json:"allowed_roots"`
}

func (policy PlanAmendmentPolicy) Valid() bool {
	if policy.ContractVersion != PlanAmendmentContractVersion || !policy.ManifestDigest.Valid() || policy.Authority.Kind != kernel.PrincipalPolicy || !policy.Authority.Valid() || len(policy.AllowedRoots) == 0 || len(policy.AllowedRoots) > 32 {
		return false
	}
	seen := make(map[string]bool)
	for _, root := range policy.AllowedRoots {
		if !AmendmentRepositoryPath(root) || seen[root] {
			return false
		}
		seen[root] = true
	}
	return true
}

type PlanAmendmentReservation struct {
	AssessmentID        kernel.UUIDv7
	BlockedTaskID       kernel.UUIDv7
	BlockedTaskRevision uint64
	BlockedInvocationID kernel.UUIDv7
	ConsultationID      kernel.UUIDv7
	AnswerDigest        kernel.Digest
}

// ReservePlanAmendment consumes the allowance before any PM model invocation.
// Its CAS must also fence authoritative task/invocation/budget revisions.
func ReservePlanAmendment(feature FeatureRequest, request PlanAmendmentReservation, witness PlanAmendmentWitness) (FeatureRequest, error) {
	if feature.Validate() != nil || feature.Plan == nil || feature.Design == nil || feature.Specification == nil || feature.Status != FeaturePlanned || feature.PlanSupersession != nil || !request.AssessmentID.Valid() {
		return FeatureRequest{}, ErrPlanAmendment
	}
	if prior := feature.PlanAmendment; prior != nil {
		if prior.AssessmentID == request.AssessmentID && prior.BlockedTaskID == request.BlockedTaskID && prior.BlockedTaskRevision == request.BlockedTaskRevision && prior.BlockedInvocationID == request.BlockedInvocationID && prior.ConsultationID == request.ConsultationID && prior.AnswerDigest == request.AnswerDigest {
			return feature, nil
		}
		return FeatureRequest{}, ErrPlanAmendment
	}
	state, found := witness.Tasks[request.BlockedTaskID]
	blocked, blockedFound := witness.Invocations[kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: request.BlockedInvocationID}]
	consult := witness.Consultation
	var planned PlannedTask
	plannedFound := false
	for _, task := range feature.Plan.Tasks {
		if task.ID == request.BlockedTaskID {
			planned, plannedFound = task, true
			break
		}
	}
	if !plannedFound || !amendmentSourceAttemptAllowed(planned, blocked) || request.ConsultationID == request.BlockedTaskID {
		return FeatureRequest{}, ErrPlanAmendment
	}
	if !witness.ScopeChangeRequired || !found || state.ID != request.BlockedTaskID || state.Revision != request.BlockedTaskRevision || state.Phase != kernel.PhaseActive || state.Condition != kernel.ConditionBlocked || !blockedFound || blocked.TaskID != request.BlockedTaskID || blocked.State != kernel.InvocationSucceeded || blocked.OutputDigest == nil || blocked.BudgetAccountID != feature.BudgetAccountID || consult.TaskID != request.ConsultationID || consult.State != kernel.InvocationSucceeded || consult.OutputDigest == nil || *consult.OutputDigest != request.AnswerDigest || consult.ActorFQN != feature.Design.PreparedBy || consult.BudgetAccountID != feature.BudgetAccountID || consult.Purpose != kernel.PurposeHandoff {
		return FeatureRequest{}, ErrPlanAmendment
	}
	for _, invocation := range []kernel.WorkInvocation{blocked, consult} {
		outputBound := false
		for _, id := range invocation.TerminalEvidenceIDs {
			evidence, found := witness.Evidence[id]
			if !found || !evidence.Available || !evidence.SHA256.Valid() {
				return FeatureRequest{}, ErrPlanAmendment
			}
			outputBound = outputBound || evidence.SHA256 == *invocation.OutputDigest
		}
		if !outputBound {
			return FeatureRequest{}, ErrPlanAmendment
		}
	}
	if !witness.Budget.Valid() || witness.Budget.ID != feature.BudgetAccountID || witness.Budget.LifecycleEpoch != feature.LifecycleEpoch || witness.Now.Before(feature.UpdatedAt) || !witness.Now.Before(witness.Budget.DeadlineAt) || witness.Budget.MaximumAdditionalInvocations() == 0 || witness.Budget.PurposeUsed[kernel.PurposeHandoff] >= witness.Budget.PurposeLimits[kernel.PurposeHandoff] || feature.LastHop > feature.Input.MaximumHops-2 {
		return FeatureRequest{}, ErrPlanAmendment
	}
	// No caller may reserve against a partial graph witness.
	for _, task := range feature.Plan.Tasks {
		if state, found := witness.Tasks[task.ID]; !found || state.ID != task.ID || state.Kind != kernel.AggregateTask || state.Revision == 0 {
			return FeatureRequest{}, ErrPlanAmendment
		}
	}
	next := feature
	next.Revision++
	next.LastHop += 2 // Forward PM assessment and its answer; never a backwards hop.
	next.UpdatedAt = witness.Now.UTC()
	next.PlanAmendment = &FeaturePlanAmendment{
		ContractVersion: PlanAmendmentContractVersion, AssessmentID: request.AssessmentID, FeatureRevision: feature.Revision,
		BlockedTaskID: request.BlockedTaskID, BlockedTaskRevision: request.BlockedTaskRevision, BlockedInvocationID: request.BlockedInvocationID,
		ConsultationID: request.ConsultationID, AnswerDigest: request.AnswerDigest, ConsultationInvocationID: witness.Consultation.ID,
		PriorPlan: *feature.Plan, PriorDigest: PlanAmendmentDigest(*feature.Plan), SpecificationDigest: PlanAmendmentDigest(*feature.Specification),
		State: AmendmentAssessing, RequestedAt: witness.Now.UTC(), DeadlineAt: witness.Budget.DeadlineAt.UTC(),
	}
	if next.Validate() != nil {
		return FeatureRequest{}, ErrPlanAmendment
	}
	return next, nil
}

// Repair is an existing continuation of an implementation task, not a new
// purpose or allowance. Bind its ordinal to the same finite repair account.
func amendmentSourceAttemptAllowed(task PlannedTask, invocation kernel.WorkInvocation) bool {
	if task.Purpose != kernel.PurposeImplementation || invocation.AttemptOrdinal == 0 {
		return false
	}
	switch invocation.Purpose {
	case kernel.PurposeImplementation:
		return invocation.AttemptOrdinal <= uint64(task.AttemptLimit)
	case kernel.PurposeRepair:
		return invocation.AttemptOrdinal <= uint64(task.ReviewRoundLimit)
	default:
		return false
	}
}

// CheckPlanAmendmentDraft is a read-only preview of the authoritative decision.
// It consumes no allowance and authorizes no publication. Callers must reload
// state and fence it again when recording the terminal decision.
func CheckPlanAmendmentDraft(feature FeatureRequest, raw []byte, witness PlanAmendmentWitness, policy PlanAmendmentPolicy) PlanAmendmentDecision {
	if !policy.Valid() || feature.PlanAmendment == nil {
		return amendmentReject("FEATURE_NOT_AMENDABLE")
	}
	proposal, err := DecodePlanAmendmentProposal(raw)
	witness.AllowedRoots = append([]string(nil), policy.AllowedRoots...)
	decision := amendmentReject("INVALID_PROPOSAL_SCHEMA")
	if err == nil {
		decision = EvaluatePlanAmendment(feature, proposal, witness)
	}
	if !witness.Now.Before(feature.PlanAmendment.DeadlineAt) {
		decision = amendmentReject("DEADLINE_EXPIRED")
	}
	return decision
}

// RecordPlanAmendmentDecision retains rejection as a terminal decision. It
// does not publish a plan or materialize/activate any work.
func RecordPlanAmendmentDecision(feature FeatureRequest, raw []byte, witness PlanAmendmentWitness, policy PlanAmendmentPolicy, authority kernel.PrincipalRef) (FeatureRequest, error) {
	if !policy.Valid() || authority != policy.Authority || feature.Validate() != nil || feature.PlanAmendment == nil {
		return FeatureRequest{}, ErrPlanAmendment
	}
	old := feature.PlanAmendment
	resultSum := sha256.Sum256(raw)
	resultDigest := kernel.Digest(hex.EncodeToString(resultSum[:]))
	proposal, decodeErr := DecodePlanAmendmentProposal(raw)
	digest := kernel.Digest("")
	if decodeErr == nil {
		digest = PlanAmendmentDigest(proposal)
	}
	if old.State != AmendmentAssessing {
		if old.DecidedBy != nil && *old.DecidedBy == authority && old.PolicyDigest == PlanAmendmentDigest(policy) && old.AssessmentResultDigest == resultDigest && old.ProposalDigest == digest {
			return feature, nil
		}
		return FeatureRequest{}, ErrPlanAmendment
	}
	if witness.Now.Before(feature.UpdatedAt) {
		return FeatureRequest{}, ErrPlanAmendment
	}
	decision := CheckPlanAmendmentDraft(feature, raw, witness, policy)
	next := feature
	next.Revision++
	next.UpdatedAt = witness.Now.UTC()
	record := *old
	record.AssessmentResultDigest, record.PolicyDigest = resultDigest, PlanAmendmentDigest(policy)
	record.Decision, record.DecidedBy, record.DecidedAt = &decision, &authority, &next.UpdatedAt
	if decodeErr == nil {
		record.Proposal, record.ProposalDigest = &proposal, digest
	}
	if decision.Accepted {
		record.State = AmendmentApproved
	} else {
		record.State = AmendmentRejected
	}
	next.PlanAmendment = &record
	if next.Validate() != nil {
		return FeatureRequest{}, ErrPlanAmendment
	}
	return next, nil
}

// PlanAmendmentStore is an explicit transaction boundary. AdvanceFeature alone
// is insufficient: a task may acquire an invocation after the policy read.
type PlanAmendmentStore interface {
	CompareAndSwapPlanAmendment(context.Context, FeatureRequest, uint64, PlanAmendmentFence) (FeatureRequest, error)
}

type PlanAmendmentFence struct {
	Preconditions []kernel.AggregatePrecondition
	Budget        kernel.WorkBudgetAccount
}

// ValidPlanAmendmentTransition prevents the persistence port from being used
// as a broad replan/specification/budget mutation operation.
func ValidPlanAmendmentTransition(prior, next FeatureRequest) bool {
	if prior.Validate() != nil || next.Validate() != nil || prior.Plan == nil || prior.Status != FeaturePlanned || next.Status != FeaturePlanned || next.Revision != prior.Revision+1 || next.UpdatedAt.Before(prior.UpdatedAt) || next.PlanAmendment == nil {
		return false
	}
	a, b := prior, next
	a.Revision, b.Revision = 0, 0
	a.UpdatedAt, b.UpdatedAt = time.Time{}, time.Time{}
	a.PlanAmendment, b.PlanAmendment = nil, nil
	if prior.PlanAmendment == nil {
		b.LastHop = a.LastHop
		if next.LastHop != prior.LastHop+2 || next.PlanAmendment.State != AmendmentAssessing || next.PlanAmendment.FeatureRevision != prior.Revision || next.PlanAmendment.PriorDigest != PlanAmendmentDigest(*prior.Plan) {
			return false
		}
	} else {
		old, record := *prior.PlanAmendment, *next.PlanAmendment
		if old.State == AmendmentAssessing && (record.State == AmendmentApproved || record.State == AmendmentRejected) {
			record.State, record.Proposal, record.ProposalDigest, record.Decision, record.DecidedBy, record.DecidedAt = old.State, old.Proposal, old.ProposalDigest, old.Decision, old.DecidedBy, old.DecidedAt
			record.AssessmentResultDigest, record.PolicyDigest = old.AssessmentResultDigest, old.PolicyDigest
			record.AssessmentInvocationID, record.AssessmentOutputDigest = old.AssessmentInvocationID, old.AssessmentOutputDigest
		} else if old.State == AmendmentApproved && record.State == AmendmentApplied {
			record.State = old.State
			b.Plan = a.Plan
		} else {
			return false
		}
		if !reflect.DeepEqual(old, record) {
			return false
		}
	}
	return reflect.DeepEqual(a, b)
}

// Revalidate immediately before publication against fresh authoritative state.
// The model's assessment remains one immutable result; rechecking does not
// create another turn or replace the recorded approval.
func RevalidatePlanAmendment(feature FeatureRequest, witness PlanAmendmentWitness, policy PlanAmendmentPolicy) PlanAmendmentDecision {
	if feature.Validate() != nil || feature.PlanAmendment == nil || feature.PlanAmendment.State != AmendmentApproved || !policy.Valid() || feature.PlanAmendment.PolicyDigest != PlanAmendmentDigest(policy) || feature.PlanAmendment.DecidedBy == nil || *feature.PlanAmendment.DecidedBy != policy.Authority {
		return amendmentReject("APPROVAL_UNAVAILABLE")
	}
	record := *feature.PlanAmendment
	proposal := record.Proposal
	record.State = AmendmentAssessing
	record.Proposal, record.Decision, record.DecidedAt, record.DecidedBy = nil, nil, nil, nil
	record.ProposalDigest, record.AssessmentResultDigest, record.PolicyDigest = "", "", ""
	record.AssessmentInvocationID, record.AssessmentOutputDigest = "", ""
	// Validate the original proposal against its reservation, not the later
	// decision timestamp. Every external revision/budget is supplied afresh.
	feature.Revision, feature.UpdatedAt = record.FeatureRevision+1, record.RequestedAt
	feature.PlanAmendment = &record
	witness.AllowedRoots = append([]string(nil), policy.AllowedRoots...)
	return EvaluatePlanAmendment(feature, *proposal, witness)
}

func PublishPlanAmendment(feature FeatureRequest, witness PlanAmendmentWitness, policy PlanAmendmentPolicy) (FeatureRequest, error) {
	if !RevalidatePlanAmendment(feature, witness, policy).Accepted || witness.Now.Before(feature.UpdatedAt) {
		return FeatureRequest{}, ErrPlanAmendment
	}
	next := feature
	record := *feature.PlanAmendment
	record.State = AmendmentApplied
	next.PlanAmendment = &record
	plan := record.Proposal.SuccessorPlan
	next.Plan = &plan
	next.Revision++
	next.UpdatedAt = witness.Now.UTC()
	if !ValidPlanAmendmentTransition(feature, next) {
		return FeatureRequest{}, ErrPlanAmendment
	}
	return next, nil
}
