package operationalruntime

import (
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

func TestPlanningRecoveryRequestValidationAndDigestAreDeterministic(t *testing.T) {
	invocationID := kernel.UUIDv7("00000000-0000-7000-8000-000000000301")
	first := kernel.EvidenceRef{EvidenceID: "00000000-0000-7000-8000-000000000302", SHA256: repeatedDigest('a')}
	second := kernel.EvidenceRef{EvidenceID: "00000000-0000-7000-8000-000000000303", SHA256: repeatedDigest('b')}
	deadline := time.Date(2026, 9, 1, 15, 0, 0, 0, time.UTC)
	request := PlanningRecoveryRequest{ExpectedRevision: 5, Reason: "correct observed planning drift", EvidenceRefs: []kernel.EvidenceRef{second, first}, DeadlineAt: deadline, IdempotencyKey: "planning-recovery-1"}
	if !request.Valid() {
		t.Fatal("valid recovery request was rejected")
	}
	digest, err := planningRecoveryConditionDigest(invocationID, request)
	if err != nil {
		t.Fatal(err)
	}
	reordered := request
	reordered.EvidenceRefs = []kernel.EvidenceRef{first, second}
	reorderedDigest, err := planningRecoveryConditionDigest(invocationID, reordered)
	if err != nil {
		t.Fatal(err)
	}
	if digest != reorderedDigest {
		t.Fatalf("evidence order changed recovery digest: %s != %s", digest, reorderedDigest)
	}
	duplicate := request
	duplicate.EvidenceRefs = []kernel.EvidenceRef{first, first}
	if duplicate.Valid() {
		t.Fatal("duplicate recovery evidence was accepted")
	}
	withoutDeadline := request
	withoutDeadline.DeadlineAt = time.Time{}
	if withoutDeadline.Valid() {
		t.Fatal("recovery without a successor deadline was accepted")
	}
	changedDeadline := request
	changedDeadline.DeadlineAt = deadline.Add(time.Minute)
	changedDigest, err := planningRecoveryConditionDigest(invocationID, changedDeadline)
	if err != nil {
		t.Fatal(err)
	}
	if changedDigest == digest {
		t.Fatal("deadline change did not change the recovery condition")
	}
}

func TestPlanningRecoveryProfileSupersedesDeadlineAndIsIdempotent(t *testing.T) {
	tracked, _, _, _ := taskExecutionRefreshFixture(t)
	prior := tracked.profile.Binding()
	deadline := tracked.profile.Budgets.DeadlineAt.Add(time.Hour)
	evidenceID := kernel.UUIDv7("00000000-0000-7000-8000-000000000304")
	condition := repeatedDigest('c')
	planning := ProductionPlanning{PolicyRevision: 2, ClassificationPolicyDigest: repeatedDigest('d'), PromotionPolicyDigest: repeatedDigest('e'), VerificationTopologyDigest: repeatedDigest('f')}

	successor, alreadyBound, err := planningRecoveryProfile(tracked.profile, prior, planning, condition, deadline, []kernel.UUIDv7{evidenceID})
	if err != nil {
		t.Fatal(err)
	}
	if alreadyBound || successor.ProfileRevision != prior.ProfileRevision+1 || successor.ProfileID == prior.ProfileID || successor.SupersedesProfileID == nil || *successor.SupersedesProfileID != prior.ProfileID || !successor.Budgets.DeadlineAt.Equal(deadline) || !containsEveryUUID(successor.ClassificationEvidenceIDs, []kernel.UUIDv7{evidenceID}) {
		t.Fatalf("successor = %#v alreadyBound=%t", successor, alreadyBound)
	}
	reloaded, alreadyBound, err := planningRecoveryProfile(successor, prior, planning, condition, deadline, []kernel.UUIDv7{evidenceID})
	if err != nil || !alreadyBound || reloaded.ProfileDigest != successor.ProfileDigest {
		t.Fatalf("idempotent successor = %#v alreadyBound=%t err=%v", reloaded, alreadyBound, err)
	}
}

func TestPlanningRecoveryProfileReusesCompatibleCommittedSuccessor(t *testing.T) {
	tracked, _, _, _ := taskExecutionRefreshFixture(t)
	prior := tracked.profile.Binding()
	deadline := tracked.profile.Budgets.DeadlineAt.Add(time.Hour)
	evidenceID := kernel.UUIDv7("00000000-0000-7000-8000-000000000304")
	planning := ProductionPlanning{PolicyRevision: 2, ClassificationPolicyDigest: repeatedDigest('d'), PromotionPolicyDigest: repeatedDigest('e'), VerificationTopologyDigest: repeatedDigest('f')}

	committed, _, err := planningRecoveryProfile(tracked.profile, prior, planning, repeatedDigest('a'), deadline, []kernel.UUIDv7{evidenceID})
	if err != nil {
		t.Fatal(err)
	}
	reused, alreadyBound, err := planningRecoveryProfile(committed, prior, planning, repeatedDigest('b'), deadline, []kernel.UUIDv7{evidenceID})
	if err != nil || !alreadyBound || reused.ProfileDigest != committed.ProfileDigest {
		t.Fatalf("reused=%#v alreadyBound=%t err=%v", reused, alreadyBound, err)
	}
}

func TestPlanningRecoveryProfileCompletesCompatibleSuccessorWithMissingEvidence(t *testing.T) {
	tracked, _, _, _ := taskExecutionRefreshFixture(t)
	prior := tracked.profile.Binding()
	deadline := tracked.profile.Budgets.DeadlineAt.Add(time.Hour)
	firstEvidence := kernel.UUIDv7("00000000-0000-7000-8000-000000000304")
	recoveryEvidence := kernel.UUIDv7("00000000-0000-7000-8000-000000000305")
	planning := ProductionPlanning{PolicyRevision: 2, ClassificationPolicyDigest: repeatedDigest('d'), PromotionPolicyDigest: repeatedDigest('e'), VerificationTopologyDigest: repeatedDigest('f')}

	committed, _, err := planningRecoveryProfile(tracked.profile, prior, planning, repeatedDigest('a'), deadline, []kernel.UUIDv7{firstEvidence})
	if err != nil {
		t.Fatal(err)
	}
	completed, alreadyBound, err := planningRecoveryProfile(committed, prior, planning, repeatedDigest('b'), deadline, []kernel.UUIDv7{recoveryEvidence})
	if err != nil || alreadyBound || completed.ProfileRevision != committed.ProfileRevision+1 || completed.SupersedesProfileID == nil || *completed.SupersedesProfileID != committed.ProfileID || !containsEveryUUID(completed.ClassificationEvidenceIDs, []kernel.UUIDv7{firstEvidence, recoveryEvidence}) {
		t.Fatalf("completed=%#v alreadyBound=%t err=%v", completed, alreadyBound, err)
	}
	reloaded, alreadyBound, err := planningRecoveryProfile(completed, prior, planning, repeatedDigest('b'), deadline, []kernel.UUIDv7{recoveryEvidence})
	if err != nil || !alreadyBound || reloaded.ProfileDigest != completed.ProfileDigest {
		t.Fatalf("reloaded=%#v alreadyBound=%t err=%v", reloaded, alreadyBound, err)
	}
}

func TestRecoveryBudgetReusesSharedAccountThatAlreadyCoversDeadline(t *testing.T) {
	deadline := time.Date(2026, 9, 2, 10, 30, 0, 0, time.UTC)
	terminal := kernel.WorkInvocation{AdmissionPolicyRevision: 25, AdmissionPolicyDigest: repeatedDigest('a')}
	account := kernel.WorkBudgetAccount{PolicyRevision: 25, PolicyDigest: terminal.AdmissionPolicyDigest, DeadlineAt: deadline}
	if recoveryBudgetAlreadyCovers(account, terminal, deadline) {
		t.Fatal("terminal admission policy was reused without the revision required for a changed-condition retry")
	}
	account.PolicyRevision++
	account.PolicyDigest = repeatedDigest('b')
	if !recoveryBudgetAlreadyCovers(account, terminal, deadline.Add(-time.Minute)) {
		t.Fatal("newer shared budget policy was not reused")
	}
	if recoveryBudgetAlreadyCovers(account, terminal, deadline.Add(time.Minute)) {
		t.Fatal("insufficient shared deadline was reused")
	}
}

func TestPlanningRecoveryProfileCompletesDurablePartialBinding(t *testing.T) {
	tracked, _, _, _ := taskExecutionRefreshFixture(t)
	prior := tracked.profile.Binding()
	deadline := tracked.profile.Budgets.DeadlineAt.Add(time.Hour)
	firstEvidence := kernel.UUIDv7("00000000-0000-7000-8000-000000000304")
	terminalEvidence := kernel.UUIDv7("00000000-0000-7000-8000-000000000305")
	condition := repeatedDigest('c')
	planning := ProductionPlanning{PolicyRevision: 2, ClassificationPolicyDigest: repeatedDigest('d'), PromotionPolicyDigest: repeatedDigest('e'), VerificationTopologyDigest: repeatedDigest('f')}

	partial, _, err := planningRecoveryProfile(tracked.profile, prior, planning, condition, deadline, []kernel.UUIDv7{firstEvidence})
	if err != nil {
		t.Fatal(err)
	}
	completed, alreadyBound, err := planningRecoveryProfile(partial, prior, planning, condition, deadline, []kernel.UUIDv7{firstEvidence, terminalEvidence})
	if err != nil {
		t.Fatal(err)
	}
	if alreadyBound || completed.ProfileRevision != partial.ProfileRevision+1 || completed.SupersedesProfileID == nil || *completed.SupersedesProfileID != partial.ProfileID || !containsEveryUUID(completed.ClassificationEvidenceIDs, []kernel.UUIDv7{firstEvidence, terminalEvidence}) {
		t.Fatalf("completed = %#v alreadyBound=%t", completed, alreadyBound)
	}
	reloaded, alreadyBound, err := planningRecoveryProfile(completed, prior, planning, condition, deadline, []kernel.UUIDv7{firstEvidence, terminalEvidence})
	if err != nil || !alreadyBound || reloaded.ProfileDigest != completed.ProfileDigest {
		t.Fatalf("reloaded = %#v alreadyBound=%t err=%v", reloaded, alreadyBound, err)
	}
}

func TestRecoveryProfileEvidenceIncludesEveryTerminalReceipt(t *testing.T) {
	terminal := recoveryTerminalFixture(kernel.InvocationFailed, boolPointer(true), nil)
	terminal.TerminalEvidenceIDs = []kernel.UUIDv7{
		"00000000-0000-7000-8000-000000000306",
		"00000000-0000-7000-8000-000000000307",
	}
	result := recoveryProfileEvidenceIDs(terminal, []kernel.UUIDv7{
		"00000000-0000-7000-8000-000000000305",
		"00000000-0000-7000-8000-000000000306",
	})
	if len(result) != 3 || result[0] != "00000000-0000-7000-8000-000000000305" || result[2] != "00000000-0000-7000-8000-000000000307" {
		t.Fatalf("evidence ids = %v", result)
	}
}

func boolPointer(value bool) *bool { return &value }

func TestFeatureBudgetPolicyAcceptsOnlyExpiredPredecessorForRecovery(t *testing.T) {
	now := time.Date(2026, 9, 1, 16, 0, 0, 0, time.UTC)
	planning := ProductionPlanning{PolicyRevision: 2, BudgetPolicyDigest: repeatedDigest('b')}
	account := kernel.WorkBudgetAccount{PolicyRevision: 1, PolicyDigest: repeatedDigest('a'), DeadlineAt: now.Add(time.Minute)}
	if featureBudgetPolicyAccepted(account, planning, now) {
		t.Fatal("unexpired predecessor policy was accepted")
	}
	account.DeadlineAt = now.Add(-time.Minute)
	if !featureBudgetPolicyAccepted(account, planning, now) {
		t.Fatal("expired predecessor policy was not accepted for recovery")
	}
	account.PolicyRevision = planning.PolicyRevision
	if featureBudgetPolicyAccepted(account, planning, now) {
		t.Fatal("current revision with the wrong digest was accepted")
	}
	account.PolicyDigest = planning.BudgetPolicyDigest
	if !featureBudgetPolicyAccepted(account, planning, now) {
		t.Fatal("current policy was rejected")
	}
}

func TestRecoverablePlanningTerminalAcceptsOnlyExplicitlyRecoverableTerminals(t *testing.T) {
	now := time.Date(2026, 9, 1, 16, 0, 0, 0, time.UTC)
	retryable := true
	notRetryable := false
	tests := []struct {
		name       string
		invocation kernel.WorkInvocation
		want       bool
	}{
		{name: "requested cancellation", invocation: recoveryTerminalFixture(kernel.InvocationCancelled, nil, &now), want: true},
		{name: "unrequested cancellation", invocation: recoveryTerminalFixture(kernel.InvocationCancelled, nil, nil)},
		{name: "timed out", invocation: recoveryTerminalFixture(kernel.InvocationTimedOut, nil, nil), want: true},
		{name: "retryable failure", invocation: recoveryTerminalFixture(kernel.InvocationFailed, &retryable, nil), want: true},
		{name: "non-retryable failure", invocation: recoveryTerminalFixture(kernel.InvocationFailed, &notRetryable, nil)},
		{name: "failure without classification", invocation: recoveryTerminalFixture(kernel.InvocationFailed, nil, nil)},
		{name: "retryable start failure", invocation: recoveryTerminalFixture(kernel.InvocationStartFailed, &retryable, nil), want: true},
		{name: "successful invocation", invocation: recoveryTerminalFixture(kernel.InvocationSucceeded, nil, nil)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := recoverablePlanningTerminal(test.invocation); got != test.want {
				t.Fatalf("recoverablePlanningTerminal() = %t, want %t", got, test.want)
			}
		})
	}
}

func recoveryTerminalFixture(state kernel.WorkInvocationState, retryable *bool, cancelledAt *time.Time) kernel.WorkInvocation {
	invocation := kernel.WorkInvocation{
		ID: "00000000-0000-7000-8000-000000000321", Revision: 4, State: state,
		AuthorizationEventID: "00000000-0000-7000-8000-000000000322", ParentEventID: "00000000-0000-7000-8000-000000000323",
		TaskID: "00000000-0000-7000-8000-000000000324", BudgetAccountID: "00000000-0000-7000-8000-000000000325",
		LifecycleEpoch: 1, ScopeRevision: 1, TaskRevision: 1,
		WorkProfile:           kernel.WorkProfileBinding{ProfileID: "00000000-0000-7000-8000-000000000326", ProfileRevision: 1, ProfileDigest: repeatedDigest('1'), LifecycleEpoch: 1, ScopeRevision: 1},
		QualifiedAssignmentID: "00000000-0000-7000-8000-000000000327", Purpose: kernel.PurposeReplan,
		AttemptFamily: "replan", AttemptOrdinal: 10, ConditionDigest: repeatedDigest('2'), OutputPredicateDigest: repeatedDigest('3'),
		AllowedTerminalOutcomes: []kernel.WorkInvocationState{kernel.InvocationSucceeded, kernel.InvocationFailed, kernel.InvocationTimedOut, kernel.InvocationCancelled, kernel.InvocationStartFailed},
		ToolPolicyDigest:        repeatedDigest('4'), EffectPolicyDigest: repeatedDigest('5'), ActorFQN: "teams::architect-1",
		Execution:          kernel.ExecutionTuple{ExecutionID: "00000000-0000-7000-8000-000000000328", FencingEpoch: 1},
		ModelProfileDigest: repeatedDigest('6'), RuntimeIdentityDigest: repeatedDigest('7'), WorkspaceID: "architect-1",
		DeadlineAt: time.Date(2026, 9, 1, 17, 0, 0, 0, time.UTC), IdempotencyKey: "recoverable-terminal-fixture",
		AdmissionPolicyRevision: 1, AdmissionPolicyDigest: repeatedDigest('8'), GlobalDebitOrdinal: 1, PurposeDebitOrdinal: 1,
		Retryable: retryable, CancellationRequestedAt: cancelledAt,
		LastEventID: "00000000-0000-7000-8000-000000000329",
	}
	return invocation
}

func TestExplicitPlanningRecoveryPreservesLineageWithoutReusingCondition(t *testing.T) {
	tracked, profile, workspace, _ := taskExecutionRefreshFixture(t)
	now := time.Date(2026, 9, 1, 16, 0, 0, 0, time.UTC)
	prior := kernel.WorkInvocation{
		ID: "00000000-0000-7000-8000-000000000311", Revision: 5, State: kernel.InvocationCancelled,
		AuthorizationEventID: "00000000-0000-7000-8000-000000000312", ParentEventID: "00000000-0000-7000-8000-000000000313",
		TaskID: tracked.plan.ID, BudgetAccountID: "00000000-0000-7000-8000-000000000314",
		LifecycleEpoch: 1, ScopeRevision: 1, TaskRevision: 5, WorkProfile: tracked.profile.Binding(),
		QualifiedAssignmentID: "00000000-0000-7000-8000-000000000315", Purpose: kernel.PurposeReplan,
		AttemptFamily: "replan", AttemptOrdinal: 7, ConditionDigest: repeatedDigest('1'), OutputPredicateDigest: repeatedDigest('2'),
		AllowedTerminalOutcomes: []kernel.WorkInvocationState{kernel.InvocationSucceeded, kernel.InvocationFailed, kernel.InvocationTimedOut, kernel.InvocationCancelled, kernel.InvocationStartFailed},
		ToolPolicyDigest:        repeatedDigest('3'), EffectPolicyDigest: repeatedDigest('4'), ActorFQN: tracked.owner.ActorFQN,
		Execution: tracked.owner.Execution, ModelProfileDigest: profile.ModelProfileDigest, RuntimeIdentityDigest: profile.RuntimeIdentityDigest,
		WorkspaceID: workspace.WorkspaceID, DeadlineAt: now.Add(time.Hour), IdempotencyKey: "prior-planning-attempt",
		AdmissionPolicyRevision: 2, AdmissionPolicyDigest: repeatedDigest('5'), GlobalDebitOrdinal: 1, PurposeDebitOrdinal: 1,
		LastEventID: "00000000-0000-7000-8000-000000000316", CancellationRequestedAt: &now,
	}
	if !validInvocationContinuation(prior, kernel.PurposeReplan, 8, true, false) {
		t.Fatal("explicit cancelled recovery did not preserve prior invocation lineage")
	}
	if validInvocationContinuation(prior, kernel.PurposeReplan, 8, true, true) {
		t.Fatal("cancelled recovery was allowed to reuse the cancelled condition")
	}
	prior.CancellationRequestedAt = nil
	if validInvocationContinuation(prior, kernel.PurposeReplan, 8, true, false) {
		t.Fatal("unrequested cancellation was accepted for explicit recovery")
	}
	prior.State = kernel.InvocationTimedOut
	if !validInvocationContinuation(prior, kernel.PurposeReplan, 8, true, false) {
		t.Fatal("timed-out recovery did not preserve prior invocation lineage")
	}
}
