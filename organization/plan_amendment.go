package organization

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path"
	"reflect"
	"strings"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

const PlanAmendmentContractVersion = "0.15.0"

var ErrPlanAmendment = errors.New("invalid bounded plan amendment")

// The PM proposes an execution repair. None of these fields is authority to
// mutate a task, specification, budget, or repository.
type PlanAmendmentProposal struct {
	SchemaVersion       string                `json:"schema_version"`
	FeatureID           kernel.UUIDv7         `json:"feature_id"`
	FeatureRevision     uint64                `json:"feature_revision"`
	PlanVersion         uint64                `json:"plan_version"`
	PlanDigest          kernel.Digest         `json:"plan_digest"`
	SpecificationDigest kernel.Digest         `json:"specification_digest"`
	BlockedTaskID       kernel.UUIDv7         `json:"blocked_task_id"`
	BlockedTaskRevision uint64                `json:"blocked_task_revision"`
	BlockedInvocationID kernel.UUIDv7         `json:"blocked_invocation_id"`
	ConsultationID      kernel.UUIDv7         `json:"consultation_id"`
	AnswerDigest        kernel.Digest         `json:"answer_digest"`
	Reason              string                `json:"reason"`
	SuccessorPlan       FeaturePlan           `json:"successor_plan"`
	Replacements        []PlanTaskReplacement `json:"replacements"`
	Impacts             []PlanAmendmentImpact `json:"impacts"`
}

type PlanTaskReplacement struct {
	PriorTaskID     kernel.UUIDv7 `json:"prior_task_id"`
	SuccessorTaskID kernel.UUIDv7 `json:"successor_task_id"`
}

type PlanAmendmentImpact struct {
	Kind         string        `json:"kind"`
	Path         string        `json:"path"`
	OwnerTaskID  kernel.UUIDv7 `json:"owner_task_id"`
	Verification string        `json:"verification"`
}

// These inputs are loaded by the harness, never decoded from a model result.
// The caller must fence them transactionally when recording a decision.
type PlanAmendmentWitness struct {
	Tasks               map[kernel.UUIDv7]kernel.AggregateState
	Invocations         map[kernel.AggregateRef]kernel.WorkInvocation
	Consultation        kernel.WorkInvocation
	Evidence            map[kernel.UUIDv7]kernel.EvidenceMetadata
	Budget              kernel.WorkBudgetAccount
	PM                  kernel.ActorFQN
	PMExecution         kernel.ExecutionTuple
	AllowedRoots        []string
	Now                 time.Time
	ScopeChangeRequired bool
}

type PlanAmendmentDecision struct {
	Accepted bool   `json:"accepted"`
	Code     string `json:"code"`
}

func amendmentReject(code string) PlanAmendmentDecision { return PlanAmendmentDecision{Code: code} }

// RemainingPlanTaskAllowances is shared by the model input and the decision
// validator. Highest ordinals include failed attempts; a new ID cannot refill
// the predecessor's ordinary or repair allowance.
func RemainingPlanTaskAllowances(task PlannedTask, invocations map[kernel.AggregateRef]kernel.WorkInvocation) (ordinary, repair uint64) {
	var attempts, repairs uint64
	for _, invocation := range invocations {
		if invocation.TaskID != task.ID {
			continue
		}
		if invocation.Purpose == task.Purpose && invocation.AttemptOrdinal > attempts {
			attempts = invocation.AttemptOrdinal
		}
		if invocation.Purpose == kernel.PurposeRepair && invocation.AttemptOrdinal > repairs {
			repairs = invocation.AttemptOrdinal
		}
	}
	if uint64(task.AttemptLimit) > attempts {
		ordinary = uint64(task.AttemptLimit) - attempts
	}
	if uint64(task.ReviewRoundLimit) > repairs {
		repair = uint64(task.ReviewRoundLimit) - repairs
	}
	return ordinary, repair
}

// EvaluatePlanAmendment is deliberately pure: acceptance authorizes no writes.
// A durable assessment consumes the single-use allowance before this function
// runs, including when the proposal is invalid or the PM fails.
func EvaluatePlanAmendment(feature FeatureRequest, proposal PlanAmendmentProposal, witness PlanAmendmentWitness) PlanAmendmentDecision {
	encoded, marshalErr := json.Marshal(proposal)
	if marshalErr != nil || ValidatePlanAmendmentProposalJSON(encoded) != nil {
		return amendmentReject("INVALID_PROPOSAL_SCHEMA")
	}
	if feature.Validate() != nil || feature.Plan == nil || feature.Specification == nil || feature.Design == nil || feature.PlanSupersession != nil || feature.Status != FeaturePlanned {
		return amendmentReject("FEATURE_NOT_AMENDABLE")
	}
	if feature.PlanAmendment != nil {
		if feature.PlanAmendment.State != AmendmentAssessing || feature.PlanAmendment.FeatureRevision != proposal.FeatureRevision || feature.Revision != proposal.FeatureRevision+1 || feature.PlanAmendment.BlockedTaskID != proposal.BlockedTaskID || feature.PlanAmendment.BlockedTaskRevision != proposal.BlockedTaskRevision || feature.PlanAmendment.BlockedInvocationID != proposal.BlockedInvocationID || feature.PlanAmendment.AnswerDigest != proposal.AnswerDigest || feature.PlanAmendment.ConsultationID != proposal.ConsultationID {
			return amendmentReject("ALLOWANCE_USED")
		}
	} else if feature.Revision != proposal.FeatureRevision {
		return amendmentReject("STALE_FEATURE")
	}
	if proposal.SchemaVersion != PlanAmendmentContractVersion || proposal.FeatureID != feature.ID || proposal.PlanVersion != feature.Plan.Version || proposal.PlanDigest != PlanAmendmentDigest(*feature.Plan) || proposal.SpecificationDigest != PlanAmendmentDigest(*feature.Specification) {
		return amendmentReject("STALE_SOURCE")
	}
	if strings.TrimSpace(proposal.Reason) == "" || len(proposal.Reason) > 4096 || !proposal.BlockedTaskID.Valid() || !proposal.BlockedInvocationID.Valid() || !proposal.ConsultationID.Valid() || !proposal.AnswerDigest.Valid() {
		return amendmentReject("INVALID_PROPOSAL")
	}
	if !witness.Budget.Valid() || witness.Budget.ID != feature.BudgetAccountID || witness.Budget.LifecycleEpoch != feature.LifecycleEpoch || witness.Now.IsZero() || !witness.Now.Before(witness.Budget.DeadlineAt) || feature.PlanAmendment == nil && feature.LastHop > feature.Input.MaximumHops-2 {
		return amendmentReject("BUDGET_EXHAUSTED")
	}
	prior := make(map[kernel.UUIDv7]PlannedTask, len(feature.Plan.Tasks))
	for _, task := range feature.Plan.Tasks {
		prior[task.ID] = task
		state, found := witness.Tasks[task.ID]
		if !found || state.ID != task.ID || state.Kind != kernel.AggregateTask || state.Revision == 0 {
			return amendmentReject("STATE_UNAVAILABLE")
		}
	}
	blocked, found := witness.Tasks[proposal.BlockedTaskID]
	inv, invocationFound := witness.Invocations[kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: proposal.BlockedInvocationID}]
	consult := witness.Consultation
	planned, plannedFound := prior[proposal.BlockedTaskID]
	if !plannedFound || !amendmentSourceAttemptAllowed(planned, inv) || proposal.ConsultationID == proposal.BlockedTaskID {
		return amendmentReject("UNBOUND_QUESTION")
	}
	if !witness.ScopeChangeRequired || !found || blocked.Revision != proposal.BlockedTaskRevision || blocked.Phase != kernel.PhaseActive || blocked.Condition != kernel.ConditionBlocked || !invocationFound || inv.TaskID != blocked.ID || inv.BudgetAccountID != feature.BudgetAccountID || inv.State != kernel.InvocationSucceeded || inv.OutputDigest == nil || consult.TaskID != proposal.ConsultationID || consult.State != kernel.InvocationSucceeded || consult.OutputDigest == nil || *consult.OutputDigest != proposal.AnswerDigest || consult.BudgetAccountID != feature.BudgetAccountID || consult.ActorFQN != feature.Design.PreparedBy || consult.Purpose != kernel.PurposeHandoff {
		return amendmentReject("UNBOUND_QUESTION")
	}
	for _, invocation := range []kernel.WorkInvocation{inv, consult} {
		if len(invocation.TerminalEvidenceIDs) == 0 {
			return amendmentReject("EVIDENCE_UNAVAILABLE")
		}
		outputBound := false
		for _, id := range invocation.TerminalEvidenceIDs {
			evidence, present := witness.Evidence[id]
			if !present || !evidence.Available || !evidence.SHA256.Valid() {
				return amendmentReject("EVIDENCE_UNAVAILABLE")
			}
			outputBound = outputBound || evidence.SHA256 == *invocation.OutputDigest
		}
		if !outputBound {
			return amendmentReject("EVIDENCE_UNAVAILABLE")
		}
	}
	plan := proposal.SuccessorPlan
	if plan.Validate(feature) != nil || plan.Version != feature.Plan.Version+1 || plan.PreparedBy != witness.PM || witness.PM != feature.Plan.PreparedBy || !featureActorHasRole(witness.PM, "project-manager") || plan.PreparedExecution != witness.PMExecution || plan.CreatedAt.After(witness.Now) || plan.CreatedAt.Before(feature.UpdatedAt) {
		return amendmentReject("INVALID_SUCCESSOR_PLAN")
	}
	if plan.Architecture != feature.Plan.Architecture || !reflect.DeepEqual(plan.Stories, feature.Plan.Stories) || !reflect.DeepEqual(plan.DesignDecisions, feature.Plan.DesignDecisions) || !reflect.DeepEqual(plan.Assumptions, feature.Plan.Assumptions) || plan.SourceDesignDigest != feature.Plan.SourceDesignDigest {
		return amendmentReject("PRODUCT_OR_DESIGN_CHANGED")
	}
	next := make(map[kernel.UUIDv7]PlannedTask, len(plan.Tasks))
	newCount := uint64(0)
	needed := make(kernel.PurposeCounters)
	for _, task := range plan.Tasks {
		next[task.ID] = task
		if old, exists := prior[task.ID]; exists {
			if !reflect.DeepEqual(old, task) {
				return amendmentReject("TASK_REWRITTEN")
			}
			continue
		}
		newCount++
		if !strings.HasPrefix(string(task.Owner), feature.Input.Team+"::") {
			return amendmentReject("OWNER_OUTSIDE_TEAM")
		}
		needed[task.Purpose]++
		if task.Purpose != kernel.PurposeImplementation && task.Purpose != kernel.PurposeValidation && task.Purpose != kernel.PurposeReview && task.Purpose != kernel.PurposePromotion {
			return amendmentReject("PURPOSE_NOT_ALLOWED")
		}
		if len(task.Title) > 256 || len(task.Description) > 65536 || len(task.AcceptanceCriteria) > 64 || validBoundedStrings(task.AcceptanceCriteria, 4096) != nil || task.AttemptLimit > 16 || task.ReviewRoundLimit > 8 {
			return amendmentReject("INVALID_SUCCESSOR_PLAN")
		}
		for _, scope := range task.WriteScope {
			if !AmendmentRepositoryPath(scope) || !coveredAmendmentPath(scope, witness.AllowedRoots) {
				return amendmentReject("WRITE_SCOPE_DENIED")
			}
		}
	}
	// Count retired nodes too: swapping IDs must not mint planning capacity.
	if newCount == 0 || uint64(len(prior))+newCount > uint64(feature.Input.MaximumTasks) || newCount > witness.Budget.MaximumAdditionalInvocations() {
		return amendmentReject("BUDGET_EXHAUSTED")
	}
	for purpose, count := range needed {
		if count > witness.Budget.PurposeLimits[purpose]-witness.Budget.PurposeUsed[purpose] {
			return amendmentReject("BUDGET_EXHAUSTED")
		}
	}
	removed := make(map[kernel.UUIDv7]kernel.UUIDv7)
	usedSuccessors := make(map[kernel.UUIDv7]bool)
	for _, replacement := range proposal.Replacements {
		old, exists := prior[replacement.PriorTaskID]
		fresh, freshExists := next[replacement.SuccessorTaskID]
		_, oldKept := next[replacement.PriorTaskID]
		_, successorOld := prior[replacement.SuccessorTaskID]
		if !exists || !freshExists || oldKept || successorOld || old.StoryID != fresh.StoryID || old.Purpose != fresh.Purpose || removed[old.ID] != "" || usedSuccessors[fresh.ID] {
			return amendmentReject("INVALID_REPLACEMENT")
		}
		// A successor ID cannot reset an exhausted task family. New unrelated
		// compatibility nodes have their own finite limits, but a replacement
		// inherits only the predecessor's unspent ordinary/repair allowance.
		ordinary, repair := RemainingPlanTaskAllowances(old, witness.Invocations)
		if ordinary == 0 || repair == 0 || uint64(fresh.AttemptLimit) > ordinary || uint64(fresh.ReviewRoundLimit) > repair {
			return amendmentReject("ATTEMPT_ALLOWANCE_EXHAUSTED")
		}
		removed[old.ID], usedSuccessors[fresh.ID] = fresh.ID, true
	}
	if removed[proposal.BlockedTaskID] == "" {
		return amendmentReject("BLOCKED_TASK_NOT_REPLACED")
	}
	for id := range prior {
		_, kept := next[id]
		if kept == (removed[id] != "") {
			return amendmentReject("UNACCOUNTED_TASK")
		}
		if kept {
			continue
		}
		state := witness.Tasks[id]
		if state.Phase == kernel.PhaseCompleted || state.Phase == kernel.PhaseClosed {
			return amendmentReject("COMPLETED_WORK_CHANGED")
		}
		for _, invocation := range witness.Invocations {
			if invocation.TaskID != id {
				continue
			}
			if !invocation.State.Terminal() {
				return amendmentReject("ACTIVE_WORK_CHANGED")
			}
			if id != proposal.BlockedTaskID {
				return amendmentReject("EXECUTED_WORK_CHANGED")
			}
		}
	}
	// Only the blocked node and its downstream closure may be replaced. Unrelated
	// untouched work cannot be smuggled into an amendment.
	affected := map[kernel.UUIDv7]bool{proposal.BlockedTaskID: true}
	for changed := true; changed; {
		changed = false
		for _, task := range feature.Plan.Tasks {
			for _, dependency := range task.DependsOn {
				if affected[dependency] && !affected[task.ID] {
					affected[task.ID] = true
					changed = true
				}
			}
		}
	}
	for id := range removed {
		if !affected[id] {
			return amendmentReject("UNRELATED_TASK_REPLACED")
		}
	}
	for id, successor := range removed {
		for _, dependency := range prior[id].DependsOn {
			if replacement := removed[dependency]; replacement != "" {
				dependency = replacement
			}
			found := false
			for _, candidate := range next[successor].DependsOn {
				found = found || candidate == dependency
			}
			if !found {
				return amendmentReject("DEPENDENCY_DROPPED")
			}
		}
	}
	// Every fresh node must be an ancestor or descendant of a replacement;
	// adding unrelated feature work is not a compatibility repair.
	related := make(map[kernel.UUIDv7]bool)
	for _, id := range removed {
		related[id] = true
	}
	for changed := true; changed; {
		changed = false
		for _, task := range plan.Tasks {
			if prior[task.ID].ID != "" {
				continue
			}
			for _, dependency := range task.DependsOn {
				if prior[dependency].ID != "" {
					continue
				}
				if related[task.ID] && !related[dependency] {
					related[dependency] = true
					changed = true
				}
				if related[dependency] && !related[task.ID] {
					related[task.ID] = true
					changed = true
				}
			}
		}
	}
	for _, task := range plan.Tasks {
		if prior[task.ID].ID == "" && !related[task.ID] {
			return amendmentReject("UNRELATED_TASK_ADDED")
		}
	}
	// Retained consumer handoffs are byte-for-byte stable.
	for _, handoff := range feature.Plan.Handoffs {
		if id := removed[handoff.ConsumerTaskID]; id != "" {
			handoff.ConsumerTaskID = id
		}
		if id := removed[handoff.ProviderTaskID]; id != "" {
			handoff.ProviderTaskID = id
		}
		found := false
		for _, candidate := range plan.Handoffs {
			if reflect.DeepEqual(candidate, handoff) {
				found = true
			}
		}
		if !found {
			return amendmentReject("RETAINED_HANDOFF_CHANGED")
		}
	}
	if len(proposal.Impacts) == 0 || len(proposal.Impacts) > 256 {
		return amendmentReject("IMPACT_COVERAGE_MISSING")
	}
	covered := make(map[string]bool)
	for _, impact := range proposal.Impacts {
		switch impact.Kind {
		case "PRODUCTION_IMPLEMENTER", "WIRING", "CALLER", "TEST_DOUBLE":
		default:
			return amendmentReject("INVALID_IMPACT")
		}
		task, exists := next[impact.OwnerTaskID]
		if !exists || prior[impact.OwnerTaskID].ID != "" || !AmendmentRepositoryPath(impact.Path) || strings.TrimSpace(impact.Verification) == "" || len(impact.Verification) > 4096 || !coveredAmendmentPath(impact.Path, task.WriteScope) {
			return amendmentReject("IMPACT_COVERAGE_MISSING")
		}
		key := string(task.ID) + "\x00" + impact.Path
		if covered[key] {
			return amendmentReject("DUPLICATE_IMPACT")
		}
		covered[key] = true
	}
	for _, task := range plan.Tasks {
		if prior[task.ID].ID != "" {
			continue
		}
		for _, invocation := range witness.Invocations {
			if invocation.State.Terminal() {
				continue
			}
			// During draft validation the exact read-only PM assessment is still
			// active. It owns no implementation write scope. Do not exempt any
			// other task, actor, purpose, execution or additional attempt.
			if a := feature.PlanAmendment; a != nil && a.State == AmendmentAssessing && invocation.TaskID == a.AssessmentID && invocation.ActorFQN == witness.PM && invocation.Execution == witness.PMExecution && invocation.Purpose == kernel.PurposeHandoff && invocation.AttemptOrdinal == 1 {
				continue
			}
			active, exists := prior[invocation.TaskID]
			if !exists {
				return amendmentReject("STATE_UNAVAILABLE")
			}
			for _, scope := range task.WriteScope {
				for _, activeScope := range active.WriteScope {
					if scope == activeScope || strings.HasPrefix(scope, activeScope+"/") || strings.HasPrefix(activeScope, scope+"/") {
						return amendmentReject("ACTIVE_WRITER_OVERLAP")
					}
				}
			}
		}
		for _, scope := range task.WriteScope {
			// Exact coverage prevents a model declaring one inspected leaf while
			// requesting its entire parent directory as writable authority.
			if !covered[string(task.ID)+"\x00"+scope] {
				return amendmentReject("IMPACT_COVERAGE_MISSING")
			}
		}
	}
	return PlanAmendmentDecision{Accepted: true, Code: "APPROVED"}
}

func PlanAmendmentDigest(value any) kernel.Digest {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return kernel.Digest(hex.EncodeToString(digest[:]))
}

func AmendmentRepositoryPath(value string) bool {
	if value == "" || len(value) > 1024 || value == "." || path.IsAbs(value) || path.Clean(value) != value || strings.ContainsAny(value, "\\\x00\r\n*?[]") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." || part == ".git" || part == ".openhands" {
			return false
		}
	}
	return true
}

func coveredAmendmentPath(value string, scopes []string) bool {
	for _, scope := range scopes {
		if AmendmentRepositoryPath(scope) && (value == scope || strings.HasPrefix(value, scope+"/")) {
			return true
		}
	}
	return false
}

type PlanAmendmentState string

const (
	AmendmentAssessing PlanAmendmentState = "ASSESSING"
	AmendmentApproved  PlanAmendmentState = "APPROVED"
	AmendmentRejected  PlanAmendmentState = "REJECTED"
	AmendmentApplied   PlanAmendmentState = "APPLIED"
)

// The original plan and reservation survive rejection, publication and restart.
// The single record is an allowance ledger, not a retry counter.
type FeaturePlanAmendment struct {
	ContractVersion          string                 `json:"contract_version"`
	AssessmentID             kernel.UUIDv7          `json:"assessment_id"`
	FeatureRevision          uint64                 `json:"feature_revision"`
	BlockedTaskID            kernel.UUIDv7          `json:"blocked_task_id"`
	BlockedTaskRevision      uint64                 `json:"blocked_task_revision"`
	BlockedInvocationID      kernel.UUIDv7          `json:"blocked_invocation_id"`
	ConsultationID           kernel.UUIDv7          `json:"consultation_id"`
	ConsultationInvocationID kernel.UUIDv7          `json:"consultation_invocation_id"`
	AnswerDigest             kernel.Digest          `json:"answer_digest"`
	PriorPlan                FeaturePlan            `json:"prior_plan"`
	PriorDigest              kernel.Digest          `json:"prior_digest"`
	SpecificationDigest      kernel.Digest          `json:"specification_digest"`
	State                    PlanAmendmentState     `json:"state"`
	RequestedAt              time.Time              `json:"requested_at"`
	AssessmentInput          *kernel.EvidenceRef    `json:"assessment_input,omitempty"`
	DeadlineAt               time.Time              `json:"deadline_at"`
	Proposal                 *PlanAmendmentProposal `json:"proposal,omitempty"`
	ProposalDigest           kernel.Digest          `json:"proposal_digest,omitempty"`
	AssessmentResultDigest   kernel.Digest          `json:"assessment_result_digest,omitempty"`
	AssessmentInvocationID   kernel.UUIDv7          `json:"assessment_invocation_id,omitempty"`
	AssessmentOutputDigest   kernel.Digest          `json:"assessment_output_digest,omitempty"`
	PolicyDigest             kernel.Digest          `json:"policy_digest,omitempty"`
	Decision                 *PlanAmendmentDecision `json:"decision,omitempty"`
	DecidedBy                *kernel.PrincipalRef   `json:"decided_by,omitempty"`
	DecidedAt                *time.Time             `json:"decided_at,omitempty"`
}

func (record FeaturePlanAmendment) Validate(feature FeatureRequest) error {
	if record.AssessmentInput != nil && (!record.AssessmentInput.EvidenceID.Valid() || !record.AssessmentInput.SHA256.Valid()) {
		return ErrPlanAmendment
	}
	if record.ContractVersion != PlanAmendmentContractVersion || !record.AssessmentID.Valid() || record.FeatureRevision == 0 || record.FeatureRevision >= feature.Revision || !record.BlockedTaskID.Valid() || record.BlockedTaskRevision == 0 || !record.BlockedInvocationID.Valid() || !record.ConsultationID.Valid() || !record.ConsultationInvocationID.Valid() || !record.AnswerDigest.Valid() || record.PriorPlan.Validate(feature) != nil || record.PriorDigest != PlanAmendmentDigest(record.PriorPlan) || feature.Specification == nil || record.SpecificationDigest != PlanAmendmentDigest(*feature.Specification) || record.RequestedAt.Before(feature.CreatedAt) || !record.DeadlineAt.After(record.RequestedAt) || feature.Plan == nil {
		return ErrPlanAmendment
	}
	if record.State == AmendmentAssessing {
		if record.Proposal != nil || record.Decision != nil || record.DecidedAt != nil || record.DecidedBy != nil || record.ProposalDigest != "" || record.AssessmentResultDigest != "" || record.PolicyDigest != "" || record.AssessmentInvocationID != "" || record.AssessmentOutputDigest != "" || PlanAmendmentDigest(*feature.Plan) != record.PriorDigest {
			return ErrPlanAmendment
		}
		return nil
	}
	if record.State != AmendmentRejected && record.State != AmendmentApproved && record.State != AmendmentApplied || !record.AssessmentResultDigest.Valid() || !record.PolicyDigest.Valid() || record.Decision == nil || record.DecidedBy == nil || record.DecidedBy.Kind != kernel.PrincipalPolicy || !record.DecidedBy.Valid() || record.DecidedAt == nil || record.DecidedAt.Before(record.RequestedAt) || record.DecidedAt.After(feature.UpdatedAt) || record.Decision.Code == "" {
		return ErrPlanAmendment
	}
	if record.State == AmendmentRejected {
		if record.Decision.Accepted || PlanAmendmentDigest(*feature.Plan) != record.PriorDigest {
			return ErrPlanAmendment
		}
	} else if !record.Decision.Accepted || record.Decision.Code != "APPROVED" || record.Proposal == nil || !record.DecidedAt.Before(record.DeadlineAt) {
		return ErrPlanAmendment
	}
	if record.Proposal != nil && record.ProposalDigest != PlanAmendmentDigest(*record.Proposal) || record.Proposal == nil && record.ProposalDigest != "" {
		return ErrPlanAmendment
	}
	if record.AssessmentOutputDigest != "" && !record.AssessmentOutputDigest.Valid() || record.AssessmentInvocationID == "" && record.AssessmentOutputDigest != "" || record.AssessmentInvocationID != "" && (!record.AssessmentInvocationID.Valid() || record.AssessmentOutputDigest == "" && record.State != AmendmentRejected) {
		return ErrPlanAmendment
	}
	if record.State == AmendmentApplied {
		if PlanAmendmentDigest(*feature.Plan) != PlanAmendmentDigest(record.Proposal.SuccessorPlan) {
			return ErrPlanAmendment
		}
	} else if PlanAmendmentDigest(*feature.Plan) != record.PriorDigest {
		return ErrPlanAmendment
	}
	return nil
}

// Decode rejects encoded JSON, unknown fields and trailing values. Formal
// Draft 2020-12 validation is performed first by the schema-bound result path.
func DecodePlanAmendmentProposal(raw []byte) (PlanAmendmentProposal, error) {
	if err := ValidatePlanAmendmentProposalJSON(raw); err != nil {
		return PlanAmendmentProposal{}, errors.Join(ErrPlanAmendment, err)
	}
	var proposal PlanAmendmentProposal
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&proposal) != nil || !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) || decoder.Decode(new(any)) != io.EOF {
		return PlanAmendmentProposal{}, ErrPlanAmendment
	}
	return proposal, nil
}
