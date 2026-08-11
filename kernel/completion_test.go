package kernel_test

import (
	"reflect"
	"testing"

	"github.com/tekroo-ai/teams/kernel"
)

func TestCompletionPlanIsCanonicalAndBindsCurrentFinalizedReview(t *testing.T) {
	input := completionInput(kernel.AggregateStory)
	first := kernel.PlanCompletion(input)
	input.EvidenceRefs[0], input.EvidenceRefs[1] = input.EvidenceRefs[1], input.EvidenceRefs[0]
	input.ArtifactDigests[0], input.ArtifactDigests[1] = input.ArtifactDigests[1], input.ArtifactDigests[0]
	input.DependencyTasks[0], input.DependencyTasks[1] = input.DependencyTasks[1], input.DependencyTasks[0]
	input.Parents[0], input.Parents[1] = input.Parents[1], input.Parents[0]
	second := kernel.PlanCompletion(input)
	if !reflect.DeepEqual(first, second) || first.Status != kernel.CompletionReady || first.FinalizedEventID != input.Review.Finalization.EventID || first.ReviewRevision != input.Review.ReviewRevision {
		t.Fatalf("completion plans = %#v %#v", first, second)
	}
	if len(first.Parents) != 3 || first.Parents[0].ParentEventID != input.Review.Finalization.EventID {
		t.Fatalf("completion parents = %#v", first.Parents)
	}
}

func TestCompletionPlanHasNoEffectForNonPassStaleOrClosedWork(t *testing.T) {
	input := completionInput(kernel.AggregateTask)
	input.Review.Join = kernel.ReviewJoinResult{Complete: true, Status: "FAIL"}
	input.Review.Finalization.TerminalStatus = "FAIL"
	if decision := kernel.PlanCompletion(input); decision.Status != kernel.CompletionNotReady || decision.Reason != "REVIEW_NOT_PASS" {
		t.Fatalf("failed review decision = %#v", decision)
	}
	input = completionInput(kernel.AggregateTask)
	input.Review.ReviewRevision++
	if decision := kernel.PlanCompletion(input); decision.Status != kernel.CompletionNotReady || decision.Reason != "REVIEW_NOT_FINALIZED" {
		t.Fatalf("stale finalization decision = %#v", decision)
	}
	input = completionInput(kernel.AggregateTask)
	input.Work.LifecycleEpoch++
	if decision := kernel.PlanCompletion(input); decision.Status != kernel.CompletionNotReady || decision.Reason != "STALE_COMPLETION_REVIEW" {
		t.Fatalf("old-epoch review decision = %#v", decision)
	}
	input = completionInput(kernel.AggregateTask)
	input.ReviewID = kernel.UUIDv7("00000000-0000-7000-8000-000000000799")
	if decision := kernel.PlanCompletion(input); decision.Status != kernel.CompletionNotReady || decision.Reason != "STALE_COMPLETION_REVIEW" {
		t.Fatalf("mismatched review identity decision = %#v", decision)
	}
	input = completionInput(kernel.AggregateTask)
	input.Work.Phase = kernel.PhaseCompleted
	if decision := kernel.PlanCompletion(input); decision.Status != kernel.CompletionAlreadyComplete {
		t.Fatalf("already-complete decision = %#v", decision)
	}
	input = completionInput(kernel.AggregateTask)
	input.UnresolvedExceptions = []string{"exception"}
	if decision := kernel.PlanCompletion(input); decision.Status != kernel.CompletionNotReady || decision.Reason != "UNRESOLVED_EXCEPTIONS" {
		t.Fatalf("exception decision = %#v", decision)
	}
}

func TestReopeningPlanIsExplicitCanonicalAndTerminalOnly(t *testing.T) {
	input := reopeningInput()
	first := kernel.PlanReopening(input)
	input.EvidenceRefs[0], input.EvidenceRefs[1] = input.EvidenceRefs[1], input.EvidenceRefs[0]
	input.Parents[0], input.Parents[1] = input.Parents[1], input.Parents[0]
	second := kernel.PlanReopening(input)
	if !reflect.DeepEqual(first, second) || first.Status != kernel.ReopeningReady || first.NewScopeRevision != 4 || !first.OwnerCarryForward {
		t.Fatalf("reopening plans = %#v %#v", first, second)
	}
	input.Work.Phase = kernel.PhaseActive
	if decision := kernel.PlanReopening(input); decision.Status != kernel.ReopeningNotEligible || decision.Reason != "WORK_NOT_REOPENABLE" {
		t.Fatalf("active reopening decision = %#v", decision)
	}
	input = reopeningInput()
	input.Work.Ownership.OwnerFQN = nil
	if decision := kernel.PlanReopening(input); decision.Status != kernel.ReopeningInvalid {
		t.Fatalf("ownerless carry-forward decision = %#v", decision)
	}
	input = reopeningInput()
	input.Work.Phase = kernel.PhaseAccepted
	if decision := kernel.PlanReopening(input); decision.Status != kernel.ReopeningNotEligible {
		t.Fatalf("accepted task reopening decision = %#v", decision)
	}
}

func completionInput(kind kernel.AggregateKind) kernel.CompletionInput {
	workID := kernel.UUIDv7("00000000-0000-7000-8000-000000000701")
	owner := kernel.ActorFQN("teams::coder-1")
	work := kernel.AggregateState{Kind: kind, ID: workID, Revision: 5, LifecycleEpoch: 2, Phase: kernel.PhaseActive, Condition: kernel.ConditionRunnable, Ownership: kernel.Ownership{OwnerFQN: &owner, OwnershipVersion: 3}}
	finalizedID := kernel.UUIDv7("00000000-0000-7000-8000-000000000702")
	reviewID := kernel.UUIDv7("00000000-0000-7000-8000-000000000703")
	review := kernel.CompletionReviewSnapshot{
		ReviewID: reviewID, Subject: kernel.AggregateRef{Kind: kind, ID: workID}, LifecycleEpoch: 2, CriteriaRevision: 6, BranchPolicyRevision: 4, ReviewRevision: 3,
		Join: kernel.ReviewJoinResult{Complete: true, Status: "PASS"}, Finalization: &kernel.ReviewFinalization{EventID: finalizedID, ReviewRevision: 3, TerminalStatus: "PASS"},
	}
	input := kernel.CompletionInput{
		Work: work, ReviewID: reviewID, Review: review,
		EvidenceRefs: []kernel.EvidenceRef{
			{EvidenceID: kernel.UUIDv7("00000000-0000-7000-8000-000000000705"), SHA256: kernel.Digest("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")},
			{EvidenceID: kernel.UUIDv7("00000000-0000-7000-8000-000000000704"), SHA256: kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")},
		},
		ArtifactDigests: []kernel.Digest{kernel.Digest("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"), kernel.Digest("cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")},
		Parents: []kernel.DagParent{
			{ParentEventID: kernel.UUIDv7("00000000-0000-7000-8000-000000000707"), EdgeKind: kernel.EdgeDerivation},
			{ParentEventID: kernel.UUIDv7("00000000-0000-7000-8000-000000000706"), EdgeKind: kernel.EdgeCausal},
		},
	}
	if kind == kernel.AggregateStory {
		input.DependencyTasks = []kernel.AggregateState{
			{Kind: kernel.AggregateTask, ID: kernel.UUIDv7("00000000-0000-7000-8000-000000000709"), Revision: 2, LifecycleEpoch: 1, Phase: kernel.PhaseCompleted},
			{Kind: kernel.AggregateTask, ID: kernel.UUIDv7("00000000-0000-7000-8000-000000000708"), Revision: 3, LifecycleEpoch: 1, Phase: kernel.PhaseCompleted},
		}
	}
	return input
}

func reopeningInput() kernel.ReopeningInput {
	owner := kernel.ActorFQN("teams::coder-1")
	return kernel.ReopeningInput{
		Work:             kernel.AggregateState{Kind: kernel.AggregateTask, ID: kernel.UUIDv7("00000000-0000-7000-8000-000000000711"), Revision: 7, LifecycleEpoch: 2, Phase: kernel.PhaseCompleted, Condition: kernel.ConditionRunnable, Ownership: kernel.Ownership{OwnerFQN: &owner, OwnershipVersion: 3}},
		NewScopeRevision: 4, Reason: "new evidence requires remediation", OwnerCarryForward: true,
		EvidenceRefs: []kernel.EvidenceRef{
			{EvidenceID: kernel.UUIDv7("00000000-0000-7000-8000-000000000713"), SHA256: kernel.Digest("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")},
			{EvidenceID: kernel.UUIDv7("00000000-0000-7000-8000-000000000712"), SHA256: kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")},
		},
		Parents: []kernel.DagParent{
			{ParentEventID: kernel.UUIDv7("00000000-0000-7000-8000-000000000715"), EdgeKind: kernel.EdgeSupersession},
			{ParentEventID: kernel.UUIDv7("00000000-0000-7000-8000-000000000714"), EdgeKind: kernel.EdgeCausal},
		},
	}
}
