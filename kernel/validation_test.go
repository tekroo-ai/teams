package kernel_test

import (
	"reflect"
	"testing"

	"github.com/tekroo-ai/teams/kernel"
)

func TestValidationReviewPlanIsInvariantToBranchAndParentOrder(t *testing.T) {
	input := validationReviewInput()
	first := kernel.PlanValidationReview(input)
	input.RequiredBranchIDs[0], input.RequiredBranchIDs[1] = input.RequiredBranchIDs[1], input.RequiredBranchIDs[0]
	input.Parents[0], input.Parents[1] = input.Parents[1], input.Parents[0]
	second := kernel.PlanValidationReview(input)
	if !reflect.DeepEqual(first, second) || first.Status != kernel.ValidationOpeningReady || !reflect.DeepEqual(first.RequiredBranchIDs, []string{"review", "tests"}) {
		t.Fatalf("validation review plans = %#v %#v", first, second)
	}
}

func TestValidationJoinProjectionIsCanonicalAndOrderIndependent(t *testing.T) {
	first := kernel.PlanValidationJoin(kernel.CompletionReviewSnapshot{
		BranchPolicyRevision: 4, RequiredBranchIDs: []string{"tests", "review"}, PartialResultPolicy: "WAIT_ALL",
		Results: map[string]string{"tests": "PASS", "review": "PASS"},
	})
	second := kernel.PlanValidationJoin(kernel.CompletionReviewSnapshot{
		BranchPolicyRevision: 4, RequiredBranchIDs: []string{"review", "tests"}, PartialResultPolicy: "WAIT_ALL",
		Results: map[string]string{"review": "PASS", "tests": "PASS"},
	})
	if !reflect.DeepEqual(first, second) || !first.Complete || first.Status != "PASS" || len(first.MissingBranchIDs) != 0 || !reflect.DeepEqual(first.Results, []kernel.ReviewBranchResult{{BranchID: "review", Result: "PASS"}, {BranchID: "tests", Result: "PASS"}}) {
		t.Fatalf("validation join plans = %#v %#v", first, second)
	}
}

func TestValidationJoinPartialPoliciesHaveDeterministicFailurePrecedence(t *testing.T) {
	results := []kernel.ReviewBranchResult{{BranchID: "review", Result: "INCONCLUSIVE"}, {BranchID: "lint", Result: "FAIL"}}
	for index := 0; index < 20; index++ {
		if result := kernel.EvaluateReviewJoin([]string{"tests", "lint", "review"}, "FAIL_FAST", results); !result.Complete || result.Status != "FAIL" {
			t.Fatalf("fail-fast result = %#v", result)
		}
		results[0], results[1] = results[1], results[0]
	}
	wait := kernel.EvaluateReviewJoin([]string{"tests", "lint", "review"}, "WAIT_ALL", results)
	if wait.Complete || wait.Status != "PENDING" {
		t.Fatalf("wait-all partial result = %#v", wait)
	}
	results = append(results, kernel.ReviewBranchResult{BranchID: "tests", Result: "PASS"})
	if joined := kernel.EvaluateReviewJoin([]string{"tests", "lint", "review"}, "WAIT_ALL", results); !joined.Complete || joined.Status != "FAIL" {
		t.Fatalf("wait-all terminal result = %#v", joined)
	}
}

func TestValidationJoinRejectsInvalidAndUnboundedInputs(t *testing.T) {
	tests := []struct {
		name     string
		required []string
		policy   string
		results  []kernel.ReviewBranchResult
	}{
		{"empty", nil, "WAIT_ALL", nil},
		{"duplicate", []string{"tests", "tests"}, "WAIT_ALL", nil},
		{"unknown-result", []string{"tests"}, "WAIT_ALL", []kernel.ReviewBranchResult{{BranchID: "tests", Result: "MAYBE"}}},
		{"unknown-branch", []string{"tests"}, "WAIT_ALL", []kernel.ReviewBranchResult{{BranchID: "review", Result: "PASS"}}},
		{"unknown-policy", []string{"tests"}, "FIRST", nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if result := kernel.EvaluateReviewJoin(test.required, test.policy, test.results); result.Status != "CONFLICT" {
				t.Fatalf("result = %#v", result)
			}
		})
	}
	input := validationReviewInput()
	input.RequiredBranchIDs = make([]string, 33)
	if decision := kernel.PlanValidationReview(input); decision.Status != kernel.ValidationOpeningInvalid {
		t.Fatalf("unbounded opening = %#v", decision)
	}
}

func TestValidationBranchReplayIsIdempotentAndContradictionConflicts(t *testing.T) {
	snapshot := kernel.CompletionReviewSnapshot{BranchPolicyRevision: 1, RequiredBranchIDs: []string{"tests"}, PartialResultPolicy: "WAIT_ALL", Results: map[string]string{}}
	first, ok := kernel.ApplyReviewBranchResult(snapshot, kernel.ReviewBranchResult{BranchID: "tests", Result: "PASS"})
	if !ok || !first.Join.Complete || first.Join.Status != "PASS" {
		t.Fatalf("first result = %#v ok=%t", first, ok)
	}
	replay, ok := kernel.ApplyReviewBranchResult(first, kernel.ReviewBranchResult{BranchID: "tests", Result: "PASS"})
	if !ok || !reflect.DeepEqual(first, replay) {
		t.Fatalf("replay = %#v ok=%t", replay, ok)
	}
	contradiction, ok := kernel.ApplyReviewBranchResult(first, kernel.ReviewBranchResult{BranchID: "tests", Result: "FAIL"})
	if ok || !reflect.DeepEqual(first, contradiction) {
		t.Fatalf("contradiction = %#v ok=%t", contradiction, ok)
	}
}

func validationReviewInput() kernel.ValidationReviewInput {
	return kernel.ValidationReviewInput{
		ReviewID:         kernel.UUIDv7("00000000-0000-7000-8000-000000000501"),
		Subject:          kernel.AggregateState{Kind: kernel.AggregateTask, ID: kernel.UUIDv7("00000000-0000-7000-8000-000000000101"), Revision: 3, LifecycleEpoch: 2, Phase: kernel.PhaseActive, Condition: kernel.ConditionRunnable},
		CriteriaRevision: 5, EvidenceSetDigest: kernel.Digest("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"), BranchPolicyRevision: 4,
		RequiredBranchIDs: []string{"tests", "review"}, PartialResultPolicy: "WAIT_ALL",
		Parents: []kernel.DagParent{{ParentEventID: kernel.UUIDv7("00000000-0000-7000-8000-000000000201"), EdgeKind: kernel.EdgeCausal}, {ParentEventID: kernel.UUIDv7("00000000-0000-7000-8000-000000000202"), EdgeKind: kernel.EdgeDerivation}},
	}
}
