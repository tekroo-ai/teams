package kernel

import "sort"

type CompletionPlanningStatus string

const (
	CompletionReady           CompletionPlanningStatus = "READY_TO_COMPLETE"
	CompletionNotReady        CompletionPlanningStatus = "NOT_READY"
	CompletionAlreadyComplete CompletionPlanningStatus = "ALREADY_COMPLETE"
	CompletionInvalid         CompletionPlanningStatus = "INVALID_INPUT"
)

type CompletionInput struct {
	Work                 AggregateState
	ReviewID             UUIDv7
	Review               CompletionReviewSnapshot
	EvidenceRefs         []EvidenceRef
	ArtifactDigests      []Digest
	UnresolvedExceptions []string
	DependencyTasks      []AggregateState
	Parents              []DagParent
}

type CompletionDecision struct {
	Status               CompletionPlanningStatus
	Reason               string
	Work                 AggregateState
	ReviewID             UUIDv7
	ReviewRevision       uint64
	BranchPolicyRevision uint64
	CriteriaRevision     uint64
	FinalizedEventID     UUIDv7
	EvidenceRefs         []EvidenceRef
	ArtifactDigests      []Digest
	DependencyTasks      []AggregateState
	Parents              []DagParent
}

func PlanCompletion(input CompletionInput) CompletionDecision {
	decision := CompletionDecision{Status: CompletionInvalid, Reason: "INVALID_COMPLETION_INPUT", Work: input.Work.Clone(), ReviewID: input.ReviewID}
	if (input.Work.Kind != AggregateTask && input.Work.Kind != AggregateStory) || !input.Work.ID.Valid() || input.Work.Revision == 0 || input.Work.LifecycleEpoch == 0 || !input.ReviewID.Valid() {
		return decision
	}
	if input.Work.Phase == PhaseCompleted || input.Work.Phase == PhaseAccepted {
		decision.Status, decision.Reason = CompletionAlreadyComplete, "WORK_ALREADY_COMPLETE"
		return decision
	}
	if input.Work.Phase != PhaseActive || input.Work.Condition != ConditionRunnable {
		decision.Status, decision.Reason = CompletionNotReady, "WORK_NOT_ACTIVE_RUNNABLE"
		return decision
	}
	if len(input.UnresolvedExceptions) > 0 {
		decision.Status, decision.Reason = CompletionNotReady, "UNRESOLVED_EXCEPTIONS"
		return decision
	}
	workRef := AggregateRef{Kind: input.Work.Kind, ID: input.Work.ID}
	review := input.Review
	if review.ReviewID != input.ReviewID || review.Subject != workRef || review.LifecycleEpoch != input.Work.LifecycleEpoch || review.CriteriaRevision == 0 || review.BranchPolicyRevision == 0 || review.ReviewRevision == 0 {
		decision.Status, decision.Reason = CompletionNotReady, "STALE_COMPLETION_REVIEW"
		return decision
	}
	if review.Finalization == nil || !review.Finalization.EventID.Valid() || review.Finalization.ReviewRevision != review.ReviewRevision {
		decision.Status, decision.Reason = CompletionNotReady, "REVIEW_NOT_FINALIZED"
		return decision
	}
	if review.Finalization.TerminalStatus != "PASS" || !review.Join.Complete || review.Join.Status != "PASS" {
		decision.Status, decision.Reason = CompletionNotReady, "REVIEW_NOT_PASS"
		return decision
	}
	evidence, ok := canonicalCompletionEvidence(input.EvidenceRefs)
	if !ok {
		return decision
	}
	artifacts, ok := canonicalCompletionDigests(input.ArtifactDigests)
	if !ok {
		return decision
	}
	dependencies, ok := canonicalCompletionDependencies(input.Work.Kind, input.DependencyTasks)
	if !ok {
		decision.Status, decision.Reason = CompletionNotReady, "DEPENDENCY_NOT_READY"
		return decision
	}
	parents := append([]DagParent(nil), input.Parents...)
	parents = append(parents, DagParent{ParentEventID: review.Finalization.EventID, EdgeKind: EdgeCausal})
	parents, ok = canonicalValidationParents(parents)
	if !ok {
		return decision
	}
	decision.Status, decision.Reason = CompletionReady, "READY_TO_COMPLETE"
	decision.ReviewRevision = review.ReviewRevision
	decision.BranchPolicyRevision = review.BranchPolicyRevision
	decision.CriteriaRevision = review.CriteriaRevision
	decision.FinalizedEventID = review.Finalization.EventID
	decision.EvidenceRefs = evidence
	decision.ArtifactDigests = artifacts
	decision.DependencyTasks = dependencies
	decision.Parents = parents
	return decision
}

type ReopeningPlanningStatus string

const (
	ReopeningReady       ReopeningPlanningStatus = "READY_TO_REOPEN"
	ReopeningNotEligible ReopeningPlanningStatus = "NOT_ELIGIBLE"
	ReopeningInvalid     ReopeningPlanningStatus = "INVALID_INPUT"
)

type ReopeningInput struct {
	Work              AggregateState
	NewScopeRevision  uint64
	Reason            string
	OwnerCarryForward bool
	EvidenceRefs      []EvidenceRef
	Parents           []DagParent
}

type ReopeningDecision struct {
	Status            ReopeningPlanningStatus
	Reason            string
	Work              AggregateState
	NewScopeRevision  uint64
	ReopeningReason   string
	OwnerCarryForward bool
	EvidenceRefs      []EvidenceRef
	Parents           []DagParent
}

func PlanReopening(input ReopeningInput) ReopeningDecision {
	decision := ReopeningDecision{Status: ReopeningInvalid, Reason: "INVALID_REOPENING_INPUT", Work: input.Work.Clone()}
	if (input.Work.Kind != AggregateTask && input.Work.Kind != AggregateStory) || !input.Work.ID.Valid() || input.Work.Revision == 0 || input.Work.LifecycleEpoch == 0 || input.NewScopeRevision == 0 || input.Reason == "" || len(input.Reason) > 4096 {
		return decision
	}
	eligible := input.Work.Phase == PhaseCompleted || (input.Work.Kind == AggregateStory && input.Work.Phase == PhaseAccepted)
	if !eligible {
		decision.Status, decision.Reason = ReopeningNotEligible, "WORK_NOT_REOPENABLE"
		return decision
	}
	if input.OwnerCarryForward && input.Work.Ownership.OwnerFQN == nil {
		return decision
	}
	evidence, ok := canonicalCompletionEvidence(input.EvidenceRefs)
	if !ok {
		return decision
	}
	parents, ok := canonicalValidationParents(input.Parents)
	if !ok {
		return decision
	}
	decision.Status, decision.Reason = ReopeningReady, "READY_TO_REOPEN"
	decision.NewScopeRevision = input.NewScopeRevision
	decision.ReopeningReason = input.Reason
	decision.OwnerCarryForward = input.OwnerCarryForward
	decision.EvidenceRefs = evidence
	decision.Parents = parents
	return decision
}

func canonicalCompletionEvidence(values []EvidenceRef) ([]EvidenceRef, bool) {
	if len(values) == 0 || len(values) > 64 {
		return nil, false
	}
	result := append([]EvidenceRef(nil), values...)
	for _, value := range result {
		if !value.EvidenceID.Valid() || !value.SHA256.Valid() {
			return nil, false
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].EvidenceID != result[j].EvidenceID {
			return result[i].EvidenceID < result[j].EvidenceID
		}
		return result[i].SHA256 < result[j].SHA256
	})
	for index := 1; index < len(result); index++ {
		if result[index-1].EvidenceID == result[index].EvidenceID {
			return nil, false
		}
	}
	return result, true
}

func canonicalCompletionDigests(values []Digest) ([]Digest, bool) {
	if len(values) > 64 {
		return nil, false
	}
	result := append([]Digest(nil), values...)
	for _, value := range result {
		if !value.Valid() {
			return nil, false
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	for index := 1; index < len(result); index++ {
		if result[index-1] == result[index] {
			return nil, false
		}
	}
	return result, true
}

func canonicalCompletionDependencies(kind AggregateKind, values []AggregateState) ([]AggregateState, bool) {
	if kind == AggregateTask {
		return nil, len(values) == 0
	}
	if len(values) > 64 {
		return nil, false
	}
	result := make([]AggregateState, len(values))
	for index, value := range values {
		if value.Kind != AggregateTask || !value.ID.Valid() || value.Revision == 0 || value.Phase != PhaseCompleted {
			return nil, false
		}
		result[index] = value.Clone()
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	for index := 1; index < len(result); index++ {
		if result[index-1].ID == result[index].ID {
			return nil, false
		}
	}
	return result, true
}
