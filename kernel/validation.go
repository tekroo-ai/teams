package kernel

import "sort"

type ValidationOpeningStatus string

const (
	ValidationOpeningReady   ValidationOpeningStatus = "READY_TO_OPEN"
	ValidationOpeningInvalid ValidationOpeningStatus = "INVALID_INPUT"
)

type ValidationReviewInput struct {
	ReviewID             UUIDv7
	Subject              AggregateState
	CriteriaRevision     uint64
	EvidenceSetDigest    Digest
	BranchPolicyRevision uint64
	RequiredBranchIDs    []string
	PartialResultPolicy  string
	Parents              []DagParent
}

type ValidationReviewDecision struct {
	Status               ValidationOpeningStatus
	Reason               string
	ReviewID             UUIDv7
	Subject              AggregateState
	CriteriaRevision     uint64
	EvidenceSetDigest    Digest
	BranchPolicyRevision uint64
	RequiredBranchIDs    []string
	PartialResultPolicy  string
	Parents              []DagParent
}

type ValidationJoinDecision struct {
	Complete          bool
	Status            string
	RequiredBranchIDs []string
	MissingBranchIDs  []string
	Results           []ReviewBranchResult
}

func PlanValidationReview(input ValidationReviewInput) ValidationReviewDecision {
	decision := ValidationReviewDecision{Status: ValidationOpeningInvalid, Reason: "INVALID_VALIDATION_REVIEW_INPUT", ReviewID: input.ReviewID, Subject: input.Subject.Clone()}
	if !input.ReviewID.Valid() || (input.Subject.Kind != AggregateTask && input.Subject.Kind != AggregateStory) || !input.Subject.ID.Valid() || input.Subject.Revision == 0 || input.Subject.LifecycleEpoch == 0 || input.Subject.Phase != PhaseActive || input.Subject.Condition != ConditionRunnable || input.CriteriaRevision == 0 || !input.EvidenceSetDigest.Valid() || input.BranchPolicyRevision == 0 || (input.PartialResultPolicy != "WAIT_ALL" && input.PartialResultPolicy != "FAIL_FAST") {
		return decision
	}
	branches, ok := canonicalValidationBranches(input.RequiredBranchIDs)
	if !ok {
		return decision
	}
	parents, ok := canonicalValidationParents(input.Parents)
	if !ok {
		return decision
	}
	decision.CriteriaRevision = input.CriteriaRevision
	decision.EvidenceSetDigest = input.EvidenceSetDigest
	decision.BranchPolicyRevision = input.BranchPolicyRevision
	decision.RequiredBranchIDs = branches
	decision.PartialResultPolicy = input.PartialResultPolicy
	decision.Parents = parents
	decision.Status, decision.Reason = ValidationOpeningReady, "READY_TO_OPEN"
	return decision
}

func PlanValidationJoin(snapshot CompletionReviewSnapshot) ValidationJoinDecision {
	branches, ok := canonicalValidationBranches(snapshot.RequiredBranchIDs)
	if !ok || snapshot.BranchPolicyRevision == 0 || (snapshot.PartialResultPolicy != "WAIT_ALL" && snapshot.PartialResultPolicy != "FAIL_FAST") || len(snapshot.Results) > 32 {
		return ValidationJoinDecision{Status: "CONFLICT"}
	}
	results := make([]ReviewBranchResult, 0, len(snapshot.Results))
	for branch, result := range snapshot.Results {
		results = append(results, ReviewBranchResult{BranchID: branch, Result: result})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].BranchID < results[j].BranchID })
	join := EvaluateReviewJoin(branches, snapshot.PartialResultPolicy, results)
	if join.Status == "CONFLICT" {
		return ValidationJoinDecision{Status: "CONFLICT"}
	}
	observed := make(map[string]struct{}, len(results))
	for _, result := range results {
		observed[result.BranchID] = struct{}{}
	}
	missing := make([]string, 0, len(branches)-len(results))
	for _, branch := range branches {
		if _, found := observed[branch]; !found {
			missing = append(missing, branch)
		}
	}
	return ValidationJoinDecision{Complete: join.Complete, Status: join.Status, RequiredBranchIDs: branches, MissingBranchIDs: missing, Results: results}
}

func canonicalValidationBranches(values []string) ([]string, bool) {
	if len(values) == 0 || len(values) > 32 {
		return nil, false
	}
	branches := append([]string(nil), values...)
	for _, branch := range branches {
		if branch == "" || len(branch) > 4096 {
			return nil, false
		}
	}
	sort.Strings(branches)
	for index := 1; index < len(branches); index++ {
		if branches[index-1] == branches[index] {
			return nil, false
		}
	}
	return branches, true
}

func canonicalValidationParents(values []DagParent) ([]DagParent, bool) {
	if len(values) == 0 || len(values) > 64 {
		return nil, false
	}
	parents := append([]DagParent(nil), values...)
	for _, parent := range parents {
		if !parent.ParentEventID.Valid() || !parent.EdgeKind.Valid() {
			return nil, false
		}
	}
	sort.Slice(parents, func(i, j int) bool {
		if parents[i].EdgeKind != parents[j].EdgeKind {
			return parents[i].EdgeKind < parents[j].EdgeKind
		}
		return parents[i].ParentEventID < parents[j].ParentEventID
	})
	for index := 1; index < len(parents); index++ {
		if parents[index-1] == parents[index] {
			return nil, false
		}
	}
	return parents, true
}
