package operationalruntime

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestBoundedWorkBlockReasonPreservesShortReason(t *testing.T) {
	reason := "validation did not pass"
	if got := boundedWorkBlockReason(reason, kernel.UUIDv7("00000000-0000-7000-8000-000000000001")); got != reason {
		t.Fatalf("reason=%q, want %q", got, reason)
	}
}

func TestBoundedWorkBlockReasonRetainsEvidencePointerWithinContractLimit(t *testing.T) {
	invocationID := kernel.UUIDv7("00000000-0000-7000-8000-000000000001")
	reason := strings.Repeat("界", maximumWorkBlockReasonRunes+100)
	got := boundedWorkBlockReason(reason, invocationID)
	if count := utf8.RuneCountInString(got); count != maximumWorkBlockReasonRunes {
		t.Fatalf("rune count=%d, want %d", count, maximumWorkBlockReasonRunes)
	}
	if !strings.HasPrefix(got, strings.Repeat("界", 100)) {
		t.Fatal("bounded reason did not preserve the beginning of the validator result")
	}
	if !strings.HasSuffix(got, "teams://work-invocation/"+string(invocationID)) {
		t.Fatal("bounded reason did not retain the full-result evidence pointer")
	}
}

func TestReviewFindingsKeepEachBoundedReasonInItsOwnFinding(t *testing.T) {
	feature := organization.FeatureRequest{ID: "00000000-0000-7000-8000-000000000001"}
	target := kernel.UUIDv7("00000000-0000-7000-8000-000000000002")
	evidence := []kernel.UUIDv7{"00000000-0000-7000-8000-000000000003"}
	first := strings.Repeat("a", 4096)
	second := strings.Repeat("b", 4096)

	findings := reviewFindings(feature, target, "validator-00000000-0000-7000-8000-000000000004", structuredValidationResult{Outcome: "FAIL", Reasons: []string{first, second}}, evidence)
	if len(findings) != 2 {
		t.Fatalf("finding count=%d, want 2", len(findings))
	}
	if findings[0].Summary != first || findings[1].Summary != second {
		t.Fatal("findings did not preserve the separately bounded reasons")
	}
	if len(findings[0].Summary) > 4096 || len(findings[1].Summary) > 4096 {
		t.Fatal("finding summary exceeds catalogue maximum")
	}
}

func TestValidatorsBoundToCompletionReviewKeepsFrozenBranchSet(t *testing.T) {
	securityID := kernel.UUIDv7("00000000-0000-7000-8000-000000000004")
	testerID := kernel.UUIDv7("00000000-0000-7000-8000-000000000005")
	security := taskValidatorResult{Task: organization.PlannedTask{ID: securityID}, Invocation: kernel.WorkInvocation{ActorFQN: "teams::security-1"}}
	tester := taskValidatorResult{Task: organization.PlannedTask{ID: testerID}, Invocation: kernel.WorkInvocation{ActorFQN: "teams::tester-1"}}
	securityBranch := "validator-" + string(securityID)
	review := kernel.CompletionReviewSnapshot{
		RequiredBranchIDs: []string{securityBranch},
		Branches: map[string]kernel.ReviewBranchSpec{
			securityBranch: {Validator: kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: "teams::security-1"}},
		},
	}

	selected, err := validatorsBoundToCompletionReview(review, []taskValidatorResult{security, tester})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0].Task.ID != securityID {
		t.Fatalf("selected=%#v, want only security validator", selected)
	}
}

func TestFinalizedFailedTaskReviewPreventsDuplicateReview(t *testing.T) {
	taskID := kernel.UUIDv7("00000000-0000-7000-8000-000000000006")
	validatorID := kernel.UUIDv7("00000000-0000-7000-8000-000000000007")
	branchID := "validator-" + string(validatorID)
	candidate := kernel.Digest(strings.Repeat("a", 64))
	profile := kernel.WorkRiskProfile{LifecycleEpoch: 1, ScopeRevision: 1, VerificationTopologyDigest: kernel.Digest(strings.Repeat("b", 64))}
	validator := taskValidatorResult{Task: organization.PlannedTask{ID: validatorID}, Invocation: kernel.WorkInvocation{ActorFQN: "teams::security-1"}, Result: structuredValidationResult{Outcome: "FAIL"}}
	review := kernel.CompletionReviewSnapshot{
		ReviewID: "00000000-0000-7000-8000-000000000008", Subject: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: taskID}, LifecycleEpoch: 1, ScopeRevision: 1, BranchPolicyRevision: 3, CandidateArtifactDigest: candidate, VerificationTopologyDigest: profile.VerificationTopologyDigest,
		RequiredBranchIDs: []string{branchID}, Branches: map[string]kernel.ReviewBranchSpec{branchID: {Validator: kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: "teams::security-1"}}}, ResultRecords: map[string]kernel.ReviewBranchResult{branchID: {Result: "FAIL"}}, ReviewRevision: 2, Finalization: &kernel.ReviewFinalization{ReviewRevision: 2, TerminalStatus: "FAIL"},
	}
	snapshot := kernel.Snapshot{Reviews: map[kernel.AggregateRef]kernel.CompletionReviewSnapshot{{Kind: kernel.AggregateCompletionReview, ID: review.ReviewID}: review}}
	found, ok := finalizedFailedTaskReview(snapshot, organization.PlannedTask{ID: taskID}, profile, candidate, []taskValidatorResult{validator}, 3)
	if !ok || found.ReviewID != review.ReviewID {
		t.Fatalf("failed review=%#v found=%t", found, ok)
	}
}
