package kernel_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

func TestValidationReviewPlanIsInvariantToBranchAndParentOrder(t *testing.T) {
	input := validationReviewInput()
	first := kernel.PlanValidationReview(input)
	input.RequiredBranchIDs[0], input.RequiredBranchIDs[1] = input.RequiredBranchIDs[1], input.RequiredBranchIDs[0]
	input.Branches[0], input.Branches[1] = input.Branches[1], input.Branches[0]
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

func TestBoundedValidationEnforcesExactAuthorityDeadlineRoundAndSupersession(t *testing.T) {
	deadline := time.Date(2026, time.August, 12, 0, 0, 0, 0, time.UTC)
	validator := kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "test-validator"}
	adjudicator := kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "review-chair"}
	branch := kernel.ReviewBranchSpec{BranchID: "tests", Validator: validator, DeadlineAt: deadline, RoundLimit: 2}
	snapshot := kernel.CompletionReviewSnapshot{
		RequiredBranchIDs: []string{"tests"}, Branches: map[string]kernel.ReviewBranchSpec{"tests": branch},
		Adjudication:        kernel.ReviewAdjudication{Adjudicator: adjudicator, DeadlineAt: deadline.Add(24 * time.Hour), RoundLimit: 1},
		PartialResultPolicy: "WAIT_ALL", Results: map[string]string{}, ResultRecords: map[string]kernel.ReviewBranchResult{}, KnownResultEvents: map[kernel.UUIDv7]kernel.ReviewBranchResult{}, ReviewRevision: 1,
	}
	firstID := kernel.UUIDv7("00000000-0000-7000-8000-000000000601")
	evidenceID := kernel.UUIDv7("00000000-0000-7000-8000-000000000602")
	first := kernel.ReviewBranchResult{BranchID: "tests", SourceRole: "VALIDATOR", Authority: validator, Round: 1, Result: "FAIL", EvidenceIDs: []kernel.UUIDv7{evidenceID}, EventID: firstID, DecidedAt: deadline.Add(-time.Hour)}
	accepted, ok := kernel.ApplyBoundedReviewBranchResult(snapshot, first)
	if !ok || accepted.Join.Status != "FAIL" || accepted.ReviewRevision != 2 {
		t.Fatalf("bounded first result = %#v ok=%t", accepted, ok)
	}
	wrong := first
	wrong.EventID = kernel.UUIDv7("00000000-0000-7000-8000-000000000603")
	wrong.Authority = kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "other-validator"}
	if reason := kernel.BoundedReviewResultReason(snapshot, wrong); reason != "VALIDATOR_MISMATCH" {
		t.Fatalf("wrong-validator reason = %q", reason)
	}
	late := first
	late.EventID = kernel.UUIDv7("00000000-0000-7000-8000-000000000604")
	late.DecidedAt = deadline.Add(time.Nanosecond)
	if reason := kernel.BoundedReviewResultReason(snapshot, late); reason != "BRANCH_DEADLINE_EXCEEDED" {
		t.Fatalf("late reason = %q", reason)
	}
	exhausted := first
	exhausted.EventID = kernel.UUIDv7("00000000-0000-7000-8000-000000000605")
	exhausted.Round = 3
	if reason := kernel.BoundedReviewResultReason(snapshot, exhausted); reason != "BRANCH_ROUND_EXHAUSTED" {
		t.Fatalf("round reason = %q", reason)
	}
	second := kernel.ReviewBranchResult{BranchID: "tests", SourceRole: "ADJUDICATOR", Authority: adjudicator, Round: 2, Result: "PASS", EvidenceIDs: []kernel.UUIDv7{evidenceID}, SupersedesResultEventIDs: []kernel.UUIDv7{firstID}, EventID: kernel.UUIDv7("00000000-0000-7000-8000-000000000606"), DecidedAt: deadline.Add(time.Hour)}
	if reason := kernel.BoundedReviewResultReason(accepted, second); reason != "ADJUDICATION_EXHAUSTED" {
		t.Fatalf("adjudication round reason = %q", reason)
	}
	second.Round = 1
	if reason := kernel.BoundedReviewResultReason(accepted, second); reason != "CHANGED_CONDITION_REQUIRED" {
		t.Fatalf("changed-condition reason = %q", reason)
	}
	second.ChangedConditionEvidenceIDs = []kernel.UUIDv7{evidenceID}
	resolved, ok := kernel.ApplyBoundedReviewBranchResult(accepted, second)
	if !ok || resolved.Join.Status != "PASS" || resolved.ResultRecords["tests"].EventID != second.EventID {
		t.Fatalf("adjudicated result = %#v ok=%t", resolved, ok)
	}
	resolved.Finalization = &kernel.ReviewFinalization{EventID: kernel.UUIDv7("00000000-0000-7000-8000-000000000607"), ReviewRevision: resolved.ReviewRevision, TerminalStatus: "PASS"}
	postFinalization := second
	postFinalization.EventID = kernel.UUIDv7("00000000-0000-7000-8000-000000000608")
	if reason := kernel.BoundedReviewResultReason(resolved, postFinalization); reason != "REVIEW_ALREADY_FINALIZED" {
		t.Fatalf("post-finalization reason = %q", reason)
	}
}

func TestCompletionReviewFinalizationBindsCurrentResultSet(t *testing.T) {
	subject := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: kernel.UUIDv7("00000000-0000-7000-8000-000000000611")}
	resultID := kernel.UUIDv7("00000000-0000-7000-8000-000000000612")
	finalizedID := kernel.UUIDv7("00000000-0000-7000-8000-000000000614")
	record := kernel.ReviewBranchResult{BranchID: "tests", Result: "PASS", EventID: resultID}
	snapshot := kernel.CompletionReviewSnapshot{
		Subject: subject, LifecycleEpoch: 2, BranchPolicyRevision: 4, RequiredBranchIDs: []string{"tests"},
		Results: map[string]string{"tests": "PASS"}, ResultRecords: map[string]kernel.ReviewBranchResult{"tests": record}, KnownResultEvents: map[kernel.UUIDv7]kernel.ReviewBranchResult{resultID: record}, ReviewRevision: 2, Join: kernel.ReviewJoinResult{Complete: true, Status: "PASS"},
	}
	payload := json.RawMessage(`{"review_id":"00000000-0000-7000-8000-000000000615","subject_kind":"task","subject_id":"00000000-0000-7000-8000-000000000611","lifecycle_epoch":2,"branch_policy_revision":4,"expected_review_revision":2,"terminal_status":"PASS","result_event_ids":["00000000-0000-7000-8000-000000000612"],"evidence_ids":["00000000-0000-7000-8000-000000000613"]}`)
	finalized, ok := kernel.ApplyReviewFinalization(snapshot, finalizedID, payload)
	if !ok || finalized.Finalization == nil || finalized.Finalization.EventID != finalizedID {
		t.Fatalf("finalization = %#v ok=%t", finalized, ok)
	}
	stale := json.RawMessage(`{"review_id":"00000000-0000-7000-8000-000000000615","subject_kind":"task","subject_id":"00000000-0000-7000-8000-000000000611","lifecycle_epoch":2,"branch_policy_revision":4,"expected_review_revision":1,"terminal_status":"PASS","result_event_ids":["00000000-0000-7000-8000-000000000612"],"evidence_ids":["00000000-0000-7000-8000-000000000613"]}`)
	if _, ok := kernel.ApplyReviewFinalization(snapshot, finalizedID, stale); ok {
		t.Fatal("stale review revision finalized")
	}
}

func TestBoundedValidationDeduplicatesFindingKeysAndRejectsConflicts(t *testing.T) {
	deadline := time.Date(2026, time.August, 12, 0, 0, 0, 0, time.UTC)
	validator := kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "test-validator"}
	evidenceID := kernel.UUIDv7("00000000-0000-7000-8000-000000000621")
	finding := kernel.ReviewFinding{FindingID: kernel.UUIDv7("00000000-0000-7000-8000-000000000622"), FindingKey: kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), Classification: "DEFECT", Summary: "same logical defect", EvidenceIDs: []kernel.UUIDv7{evidenceID}}
	prior := kernel.ReviewBranchResult{BranchID: "review", Result: "FAIL", Findings: []kernel.ReviewFinding{finding}, EventID: kernel.UUIDv7("00000000-0000-7000-8000-000000000623")}
	snapshot := kernel.CompletionReviewSnapshot{
		Branches: map[string]kernel.ReviewBranchSpec{
			"tests": {BranchID: "tests", Validator: validator, DeadlineAt: deadline, RoundLimit: 1},
		},
		ResultRecords: map[string]kernel.ReviewBranchResult{"review": prior}, KnownResultEvents: map[kernel.UUIDv7]kernel.ReviewBranchResult{prior.EventID: prior},
	}
	candidate := kernel.ReviewBranchResult{BranchID: "tests", SourceRole: "VALIDATOR", Authority: validator, Round: 1, Result: "FAIL", EvidenceIDs: []kernel.UUIDv7{evidenceID}, Findings: []kernel.ReviewFinding{finding}, EventID: kernel.UUIDv7("00000000-0000-7000-8000-000000000624"), DecidedAt: deadline.Add(-time.Hour)}
	if reason := kernel.BoundedReviewResultReason(snapshot, candidate); reason != "" {
		t.Fatalf("identical finding key did not deduplicate: %q", reason)
	}
	candidate.Findings[0].Summary = "conflicting definition"
	if reason := kernel.BoundedReviewResultReason(snapshot, candidate); reason != "FINDING_KEY_CONFLICT" {
		t.Fatalf("finding conflict reason = %q", reason)
	}
	duplicatePayload := json.RawMessage(`{"review_id":"00000000-0000-7000-8000-000000000625","branch_id":"tests","branch_policy_revision":1,"source_role":"VALIDATOR","round":1,"result":"FAIL","reasons":[],"evidence_ids":["00000000-0000-7000-8000-000000000621"],"findings":[{"finding_id":"00000000-0000-7000-8000-000000000622","finding_key":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","classification":"DEFECT","summary":"first","evidence_ids":["00000000-0000-7000-8000-000000000621"]},{"finding_id":"00000000-0000-7000-8000-000000000626","finding_key":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","classification":"DEFECT","summary":"second","evidence_ids":["00000000-0000-7000-8000-000000000621"]}],"supersedes_result_event_ids":[],"changed_condition_evidence_ids":[]}`)
	if _, _, _, err := kernel.ReviewBranchResultFromPayload(duplicatePayload); err == nil {
		t.Fatal("duplicate finding key accepted")
	}
}

func validationReviewInput() kernel.ValidationReviewInput {
	deadline := time.Date(2026, time.August, 12, 0, 0, 0, 0, time.UTC)
	evidenceID := kernel.UUIDv7("00000000-0000-7000-8000-000000000203")
	return kernel.ValidationReviewInput{
		ReviewID:         kernel.UUIDv7("00000000-0000-7000-8000-000000000501"),
		Subject:          kernel.AggregateState{Kind: kernel.AggregateTask, ID: kernel.UUIDv7("00000000-0000-7000-8000-000000000101"), Revision: 3, LifecycleEpoch: 2, Phase: kernel.PhaseActive, Condition: kernel.ConditionRunnable},
		CriteriaRevision: 5, EvidenceSetDigest: kernel.Digest("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"), BranchPolicyRevision: 4,
		RequiredBranchIDs: []string{"tests", "review"}, PartialResultPolicy: "WAIT_ALL",
		Branches: []kernel.ReviewBranchSpec{
			{BranchID: "tests", Validator: kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "test-validator"}, ResolutionOwnerFQN: kernel.ActorFQN("teams::coder-1"), AcceptanceCriteria: []string{"tests pass"}, InputEvidenceIDs: []kernel.UUIDv7{evidenceID}, DeadlineAt: deadline, RoundLimit: 2},
			{BranchID: "review", Validator: kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: "teams::reviewer-1"}, ResolutionOwnerFQN: kernel.ActorFQN("teams::coder-1"), AcceptanceCriteria: []string{"review passes"}, InputEvidenceIDs: []kernel.UUIDv7{evidenceID}, DeadlineAt: deadline, RoundLimit: 2},
		},
		Adjudication: kernel.ReviewAdjudication{Adjudicator: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "review-chair"}, DeadlineAt: deadline.Add(24 * time.Hour), RoundLimit: 1},
		Parents:      []kernel.DagParent{{ParentEventID: kernel.UUIDv7("00000000-0000-7000-8000-000000000201"), EdgeKind: kernel.EdgeCausal}, {ParentEventID: kernel.UUIDv7("00000000-0000-7000-8000-000000000202"), EdgeKind: kernel.EdgeDerivation}},
	}
}
