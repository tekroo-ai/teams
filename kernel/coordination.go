package kernel

import (
	"encoding/json"
	"errors"
	"sort"
)

type ReviewBranchResult struct {
	BranchID string `json:"branchId"`
	Result   string `json:"result"`
}

type ReviewJoinResult struct {
	Complete bool   `json:"complete"`
	Status   string `json:"status"`
}

type CompletionReviewSnapshot struct {
	BranchPolicyRevision uint64
	RequiredBranchIDs    []string
	PartialResultPolicy  string
	Results              map[string]string
	Join                 ReviewJoinResult
}

func (s CompletionReviewSnapshot) Clone() CompletionReviewSnapshot {
	copy := s
	copy.RequiredBranchIDs = append([]string(nil), s.RequiredBranchIDs...)
	copy.Results = make(map[string]string, len(s.Results))
	for branch, result := range s.Results {
		copy.Results[branch] = result
	}
	return copy
}

// EvaluateAllPassJoin is order-independent. A repeated branch is accepted only
// when its result is identical; contradictory repeats make the join a conflict.
func EvaluateAllPassJoin(required []string, results []ReviewBranchResult) ReviewJoinResult {
	return EvaluateReviewJoin(required, "WAIT_ALL", results)
}

func EvaluateReviewJoin(required []string, partialResultPolicy string, results []ReviewBranchResult) ReviewJoinResult {
	requiredSet := make(map[string]struct{}, len(required))
	for _, branch := range required {
		if branch == "" {
			return ReviewJoinResult{Status: "CONFLICT"}
		}
		if _, duplicate := requiredSet[branch]; duplicate {
			return ReviewJoinResult{Status: "CONFLICT"}
		}
		requiredSet[branch] = struct{}{}
	}
	observed := make(map[string]string, len(results))
	for _, result := range results {
		if _, requiredBranch := requiredSet[result.BranchID]; !requiredBranch {
			return ReviewJoinResult{Status: "CONFLICT"}
		}
		if prior, duplicate := observed[result.BranchID]; duplicate && prior != result.Result {
			return ReviewJoinResult{Status: "CONFLICT"}
		}
		observed[result.BranchID] = result.Result
	}
	for _, result := range observed {
		if result != "PASS" && partialResultPolicy == "FAIL_FAST" {
			return ReviewJoinResult{Complete: true, Status: result}
		}
	}
	if len(observed) != len(requiredSet) {
		return ReviewJoinResult{Status: "PENDING"}
	}
	for _, result := range observed {
		if result != "PASS" {
			return ReviewJoinResult{Complete: true, Status: result}
		}
	}
	return ReviewJoinResult{Complete: true, Status: "PASS"}
}

func CompletionReviewFromPayload(payload json.RawMessage) (CompletionReviewSnapshot, error) {
	var value struct {
		BranchPolicyRevision uint64   `json:"branch_policy_revision"`
		RequiredBranchIDs    []string `json:"required_branch_ids"`
		JoinRule             string   `json:"join_rule"`
		PartialResultPolicy  string   `json:"partial_result_policy"`
	}
	if json.Unmarshal(payload, &value) != nil || value.BranchPolicyRevision == 0 || value.JoinRule != "ALL_PASS" || (value.PartialResultPolicy != "WAIT_ALL" && value.PartialResultPolicy != "FAIL_FAST") {
		return CompletionReviewSnapshot{}, errors.New("invalid completion review policy")
	}
	join := EvaluateReviewJoin(value.RequiredBranchIDs, value.PartialResultPolicy, nil)
	if join.Status == "CONFLICT" {
		return CompletionReviewSnapshot{}, errors.New("invalid completion review branches")
	}
	return CompletionReviewSnapshot{
		BranchPolicyRevision: value.BranchPolicyRevision,
		RequiredBranchIDs:    append([]string(nil), value.RequiredBranchIDs...),
		PartialResultPolicy:  value.PartialResultPolicy,
		Results:              make(map[string]string),
		Join:                 join,
	}, nil
}

func ReviewBranchResultFromPayload(payload json.RawMessage) (UUIDv7, uint64, ReviewBranchResult, error) {
	var value struct {
		ReviewID             UUIDv7 `json:"review_id"`
		BranchID             string `json:"branch_id"`
		BranchPolicyRevision uint64 `json:"branch_policy_revision"`
		Result               string `json:"result"`
	}
	if json.Unmarshal(payload, &value) != nil || !value.ReviewID.Valid() || value.BranchID == "" || value.BranchPolicyRevision == 0 || (value.Result != "PASS" && value.Result != "FAIL" && value.Result != "INCONCLUSIVE") {
		return "", 0, ReviewBranchResult{}, errors.New("invalid review branch result")
	}
	return value.ReviewID, value.BranchPolicyRevision, ReviewBranchResult{BranchID: value.BranchID, Result: value.Result}, nil
}

func ApplyReviewBranchResult(snapshot CompletionReviewSnapshot, result ReviewBranchResult) (CompletionReviewSnapshot, bool) {
	next := snapshot.Clone()
	for _, required := range next.RequiredBranchIDs {
		if required != result.BranchID {
			continue
		}
		if prior, exists := next.Results[result.BranchID]; exists && prior != result.Result {
			return snapshot, false
		}
		next.Results[result.BranchID] = result.Result
		values := make([]ReviewBranchResult, 0, len(next.Results))
		for branch, branchResult := range next.Results {
			values = append(values, ReviewBranchResult{BranchID: branch, Result: branchResult})
		}
		next.Join = EvaluateReviewJoin(next.RequiredBranchIDs, next.PartialResultPolicy, values)
		return next, next.Join.Status != "CONFLICT"
	}
	return snapshot, false
}

func CanonicalSuccessorIDs(ids []UUIDv7) bool {
	if len(ids) == 0 {
		return false
	}
	for index, id := range ids {
		if !id.Valid() {
			return false
		}
		if index > 0 && ids[index-1] >= id {
			return false
		}
	}
	return sort.SliceIsSorted(ids, func(i, j int) bool { return ids[i] < ids[j] })
}

func CompatibilityOutcome(sourceVersion string, exactContextAvailable bool) string {
	if sourceVersion == SchemaVersion && exactContextAvailable {
		return "ACCEPTED"
	}
	return "MIGRATION_REQUIRED"
}
