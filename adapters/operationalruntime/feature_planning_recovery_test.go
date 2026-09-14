package operationalruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
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

func TestPlanningRecoveryProfileCompactsFullHistoricalEvidence(t *testing.T) {
	tracked, _, _, _ := taskExecutionRefreshFixture(t)
	current := tracked.profile
	current.ClassificationEvidenceIDs = make([]kernel.UUIDv7, 64)
	for index := range current.ClassificationEvidenceIDs {
		current.ClassificationEvidenceIDs[index] = kernel.UUIDv7(fmt.Sprintf("00000000-0000-7000-8000-%012d", index+1))
	}
	current.ProfileDigest = ""
	encoded, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	current.ProfileDigest = digestBytes(encoded)
	prior := current.Binding()
	recoveryEvidence := []kernel.UUIDv7{
		"00000000-0000-7000-8001-000000000005",
		"00000000-0000-7000-8001-000000000006",
	}
	planning := ProductionPlanning{PolicyRevision: 2, ClassificationPolicyDigest: repeatedDigest('d'), PromotionPolicyDigest: repeatedDigest('e'), VerificationTopologyDigest: repeatedDigest('f')}

	next, bound, err := planningRecoveryProfile(current, prior, planning, repeatedDigest('c'), current.Budgets.DeadlineAt.Add(time.Hour), recoveryEvidence)
	if err != nil || bound || !next.Valid() || len(next.ClassificationEvidenceIDs) != len(recoveryEvidence) || !containsEveryUUID(next.ClassificationEvidenceIDs, recoveryEvidence) || next.SupersedesProfileID == nil || *next.SupersedesProfileID != current.ProfileID {
		t.Fatalf("compacted planning-recovery profile=%#v bound=%t err=%v", next, bound, err)
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

func TestPlanningRecoveryPreludeAllowsOnlyDurableRecoveryCheckpoints(t *testing.T) {
	for _, eventType := range []string{
		"tekroo.event.task.work-profile-bound",
		"tekroo.event.task.qualified-assignment-authorized",
		"tekroo.event.task.operational-scope-bound",
		"tekroo.event.task.work-budget-bound",
		"tekroo.event.work.unblocked",
	} {
		if !planningRecoveryPreludeEvent(eventType) {
			t.Fatalf("recovery checkpoint %q was rejected", eventType)
		}
	}
	for _, eventType := range []string{"tekroo.event.work.blocked", "tekroo.event.task.completed", "tekroo.event.task.accepted"} {
		if planningRecoveryPreludeEvent(eventType) {
			t.Fatalf("unrelated task event %q was accepted as a recovery checkpoint", eventType)
		}
	}
}

func TestRecoverableTaskTerminalAllowsExplicitRepairAfterNonRetryableFailure(t *testing.T) {
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
		{name: "non-retryable failure", invocation: recoveryTerminalFixture(kernel.InvocationFailed, &notRetryable, nil), want: true},
		{name: "unclassified failure", invocation: recoveryTerminalFixture(kernel.InvocationFailed, nil, nil), want: true},
		{name: "non-retryable start failure", invocation: recoveryTerminalFixture(kernel.InvocationStartFailed, &notRetryable, nil), want: true},
		{name: "successful invocation", invocation: recoveryTerminalFixture(kernel.InvocationSucceeded, nil, nil)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := recoverableTaskTerminal(test.invocation); got != test.want {
				t.Fatalf("recoverableTaskTerminal() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestClassifyTaskRecoveryAllowsTerminalValidatorAfterRuntimeRepair(t *testing.T) {
	targetID := kernel.UUIDv7("00000000-0000-7000-8000-000000000330")
	task := organization.PlannedTask{Purpose: kernel.PurposeValidation, Validates: []kernel.UUIDv7{targetID}}
	state := kernel.AggregateState{Phase: kernel.PhaseActive, Condition: kernel.ConditionRunnable}
	nonRetryable := false
	invocation := recoveryTerminalFixture(kernel.InvocationFailed, &nonRetryable, nil)

	kind, err := (&ProductionService{}).classifyTaskRecovery(context.Background(), task, state, "", invocation)
	if err != nil || kind != taskRecoveryTerminalInvocation {
		t.Fatalf("terminal validator recovery kind=%v err=%v", kind, err)
	}
}

func TestClassifyTaskRecoveryAllowsOperatorRevalidationBeforeReviewConsumesResult(t *testing.T) {
	targetID := kernel.UUIDv7("00000000-0000-7000-8000-000000000333")
	task := organization.PlannedTask{Purpose: kernel.PurposeValidation, Validates: []kernel.UUIDv7{targetID}}
	state := kernel.AggregateState{Phase: kernel.PhaseActive, Condition: kernel.ConditionRunnable}
	invocation := recoveryTerminalFixture(kernel.InvocationSucceeded, nil, nil)
	invocation.Purpose = kernel.PurposeValidation
	invocation.AttemptFamily = "validation"
	output := repeatedDigest('9')
	invocation.OutputDigest = &output

	kind, err := (&ProductionService{}).classifyTaskRecovery(context.Background(), task, state, "", invocation)
	if err != nil || kind != taskRecoveryTerminalInvocation {
		t.Fatalf("successful validator revalidation kind=%v err=%v", kind, err)
	}
	state.Phase = kernel.PhaseCompleted
	kind, err = (&ProductionService{}).classifyTaskRecovery(context.Background(), task, state, "", invocation)
	if err != nil || kind != taskRecoveryTerminalInvocation {
		t.Fatalf("completed validator revalidation kind=%v err=%v", kind, err)
	}

	state.Phase = kernel.PhaseActive
	state.Condition = kernel.ConditionBlocked
	if _, err := (&ProductionService{}).classifyTaskRecovery(context.Background(), task, state, "", invocation); err == nil {
		t.Fatal("blocked validation task accepted operator revalidation")
	}
}

func TestClassifyTaskRecoveryAllowsOperatorRepairOfSucceededImplementation(t *testing.T) {
	taskID := kernel.UUIDv7("00000000-0000-7000-8000-000000000324")
	task := organization.PlannedTask{ID: taskID, Purpose: kernel.PurposeImplementation}
	state := kernel.AggregateState{Phase: kernel.PhaseActive, Condition: kernel.ConditionRunnable}
	invocation := recoveryTerminalFixture(kernel.InvocationSucceeded, nil, nil)
	invocation.Purpose = kernel.PurposeImplementation
	invocation.AttemptFamily = "implementation"
	output := repeatedDigest('9')
	invocation.OutputDigest = &output

	kind, err := (&ProductionService{}).classifyTaskRecovery(context.Background(), task, state, "", invocation)
	if err != nil || kind != taskRecoveryOperatorRepair {
		t.Fatalf("successful implementation repair kind=%v err=%v", kind, err)
	}
	state.Phase = kernel.PhaseCompleted
	kind, err = (&ProductionService{}).classifyTaskRecovery(context.Background(), task, state, "", invocation)
	if err != nil || kind != taskRecoveryOperatorRepair {
		t.Fatalf("completed implementation repair kind=%v err=%v", kind, err)
	}

	state.Condition = kernel.ConditionBlocked
	if _, err := (&ProductionService{}).classifyTaskRecovery(context.Background(), task, state, "", invocation); err == nil {
		t.Fatal("blocked implementation task accepted operator repair")
	}
}

func TestClassifyTaskRecoveryAllowsOperatorRepairOfSucceededRepair(t *testing.T) {
	taskID := kernel.UUIDv7("00000000-0000-7000-8000-000000000324")
	task := organization.PlannedTask{ID: taskID, Purpose: kernel.PurposeImplementation}
	state := kernel.AggregateState{Phase: kernel.PhaseActive, Condition: kernel.ConditionRunnable}
	invocation := recoveryTerminalFixture(kernel.InvocationSucceeded, nil, nil)
	invocation.Purpose = kernel.PurposeRepair
	invocation.AttemptFamily = "repair"
	invocation.AttemptOrdinal = 1
	output := repeatedDigest('9')
	invocation.OutputDigest = &output

	kind, err := (&ProductionService{}).classifyTaskRecovery(context.Background(), task, state, "", invocation)
	if err != nil || kind != taskRecoveryOperatorRepair {
		t.Fatalf("successful repair successor kind=%v err=%v", kind, err)
	}
}

func TestFailedRepairContinuationPreservesRepairPurpose(t *testing.T) {
	task := organization.PlannedTask{Purpose: kernel.PurposeImplementation}
	terminal := kernel.WorkInvocation{Purpose: kernel.PurposeRepair, AttemptOrdinal: 2}
	if nextPurpose := taskRecoverySuccessorPurpose(task, terminal); nextPurpose != kernel.PurposeRepair {
		t.Fatalf("successor purpose=%s", nextPurpose)
	}
	terminal.Purpose = kernel.PurposeImplementation
	if nextPurpose := taskRecoverySuccessorPurpose(task, terminal); nextPurpose != kernel.PurposeImplementation {
		t.Fatalf("implementation successor purpose=%s", nextPurpose)
	}
}

func TestSucceededRepairCanContinueWithChangedCondition(t *testing.T) {
	prior := recoveryTerminalFixture(kernel.InvocationSucceeded, nil, nil)
	prior.Purpose = kernel.PurposeRepair
	prior.AttemptFamily = "repair"
	prior.AttemptOrdinal = 1
	output := repeatedDigest('9')
	prior.OutputDigest = &output
	if !validInvocationContinuation(prior, kernel.PurposeRepair, 2, true, false) {
		t.Fatal("succeeded repair could not be superseded by an evidence-bound correction")
	}
}

func TestNextTaskPurposeAttemptIsIndependentPerPurpose(t *testing.T) {
	taskID := kernel.UUIDv7("00000000-0000-7000-8000-000000000334")
	invocations := map[kernel.AggregateRef]kernel.WorkInvocation{
		{Kind: kernel.AggregateWorkInvocation, ID: "00000000-0000-7000-8000-000000000335"}: {TaskID: taskID, Purpose: kernel.PurposeImplementation, AttemptOrdinal: 3},
		{Kind: kernel.AggregateWorkInvocation, ID: "00000000-0000-7000-8000-000000000336"}: {TaskID: taskID, Purpose: kernel.PurposeRepair, AttemptOrdinal: 1},
	}
	if got := nextTaskPurposeAttempt(invocations, taskID, kernel.PurposeRepair); got != 2 {
		t.Fatalf("next repair attempt = %d, want 2", got)
	}
}

func TestEffectiveTaskRecoveryDeadlinePreservesAcceptedSuspensionExtension(t *testing.T) {
	requested := time.Date(2026, 9, 8, 7, 0, 0, 0, time.UTC)
	profile := requested.Add(20 * time.Second)
	account := requested.Add(31 * time.Second)
	if got := effectiveTaskRecoveryDeadline(requested, profile, account); !got.Equal(account) {
		t.Fatalf("effective deadline = %s, want %s", got, account)
	}
	if got := effectiveTaskRecoveryDeadline(account, requested, profile); !got.Equal(account) {
		t.Fatalf("requested later deadline = %s, want %s", got, account)
	}
}

func TestReopenedTaskRecoveryProfileAdvancesLifecycleAndPreservesClassification(t *testing.T) {
	tracked, _, _, _ := taskExecutionRefreshFixture(t)
	current := tracked.profile
	planning := ProductionPlanning{PolicyRevision: 2, ClassificationPolicyDigest: repeatedDigest('d'), PromotionPolicyDigest: repeatedDigest('e'), VerificationTopologyDigest: repeatedDigest('f')}
	evidenceID := kernel.UUIDv7("00000000-0000-7000-8000-000000000337")
	deadline := current.Budgets.DeadlineAt.Add(time.Hour)
	next, err := reopenedTaskRecoveryProfile(current, planning, repeatedDigest('9'), deadline, []kernel.UUIDv7{evidenceID}, current.LifecycleEpoch+1, current.ScopeRevision+1)
	if err != nil || !next.Valid() || next.LifecycleEpoch != current.LifecycleEpoch+1 || next.ScopeRevision != current.ScopeRevision+1 || next.WorkKind != current.WorkKind || next.SupersedesProfileID == nil || *next.SupersedesProfileID != current.ProfileID || !containsEveryUUID(next.ClassificationEvidenceIDs, []kernel.UUIDv7{evidenceID}) {
		t.Fatalf("reopened profile=%#v err=%v", next, err)
	}
}

func TestReopenedTaskRecoveryProfileCompactsFullHistoricalEvidenceAtLifecycleBoundary(t *testing.T) {
	tracked, _, _, _ := taskExecutionRefreshFixture(t)
	current := tracked.profile
	current.ClassificationEvidenceIDs = make([]kernel.UUIDv7, 64)
	for index := range current.ClassificationEvidenceIDs {
		current.ClassificationEvidenceIDs[index] = kernel.UUIDv7(fmt.Sprintf("00000000-0000-7000-8000-%012d", index+1))
	}
	current.ProfileDigest = ""
	encoded, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	current.ProfileDigest = digestBytes(encoded)
	planning := ProductionPlanning{PolicyRevision: 2, ClassificationPolicyDigest: repeatedDigest('d'), PromotionPolicyDigest: repeatedDigest('e'), VerificationTopologyDigest: repeatedDigest('f')}
	recoveryEvidence := []kernel.UUIDv7{
		"00000000-0000-7000-8001-000000000001",
		"00000000-0000-7000-8001-000000000002",
	}
	next, err := reopenedTaskRecoveryProfile(current, planning, repeatedDigest('9'), current.Budgets.DeadlineAt.Add(time.Hour), recoveryEvidence, current.LifecycleEpoch+1, current.ScopeRevision+1)
	if err != nil || !next.Valid() || len(next.ClassificationEvidenceIDs) != len(recoveryEvidence) || !containsEveryUUID(next.ClassificationEvidenceIDs, recoveryEvidence) || next.SupersedesProfileID == nil || *next.SupersedesProfileID != current.ProfileID {
		t.Fatalf("compacted reopened profile=%#v err=%v", next, err)
	}
}

func TestContinuedOperatorRepairProfileResumesOrAdvancesCommittedRecovery(t *testing.T) {
	tracked, _, _, _ := taskExecutionRefreshFixture(t)
	current := tracked.profile
	planning := ProductionPlanning{PolicyRevision: 2, ClassificationPolicyDigest: repeatedDigest('d'), PromotionPolicyDigest: repeatedDigest('e'), VerificationTopologyDigest: repeatedDigest('f')}
	evidenceID := kernel.UUIDv7("00000000-0000-7000-8000-000000000338")
	current.ClassificationPolicyRevision = planning.PolicyRevision
	current.ClassificationPolicyDigest = planning.ClassificationPolicyDigest
	current.PromotionPolicyRevision = planning.PolicyRevision
	current.PromotionPolicyDigest = planning.PromotionPolicyDigest
	current.VerificationTopologyDigest = planning.VerificationTopologyDigest
	current.ClassificationEvidenceIDs = append(current.ClassificationEvidenceIDs, evidenceID)
	current.ProfileDigest = ""
	encoded, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	current.ProfileDigest = digestBytes(encoded)

	resumed, bound, err := continuedTaskRecoveryProfile(current, planning, repeatedDigest('9'), current.Budgets.DeadlineAt, []kernel.UUIDv7{evidenceID}, current.LifecycleEpoch, current.ScopeRevision)
	if err != nil || !bound || resumed.Binding() != current.Binding() {
		t.Fatalf("resumed profile=%#v bound=%t err=%v", resumed, bound, err)
	}

	deadline := current.Budgets.DeadlineAt.Add(time.Hour)
	next, bound, err := continuedTaskRecoveryProfile(current, planning, repeatedDigest('8'), deadline, []kernel.UUIDv7{evidenceID}, current.LifecycleEpoch, current.ScopeRevision)
	if err != nil || bound || !next.Valid() || next.ProfileRevision != current.ProfileRevision+1 || next.SupersedesProfileID == nil || *next.SupersedesProfileID != current.ProfileID || !next.Budgets.DeadlineAt.Equal(deadline) {
		t.Fatalf("continued profile=%#v bound=%t err=%v", next, bound, err)
	}
}

func TestContinuedTaskRecoveryProfileCompactsFullHistoricalEvidence(t *testing.T) {
	tracked, _, _, _ := taskExecutionRefreshFixture(t)
	current := tracked.profile
	current.ClassificationEvidenceIDs = make([]kernel.UUIDv7, 64)
	for index := range current.ClassificationEvidenceIDs {
		current.ClassificationEvidenceIDs[index] = kernel.UUIDv7(fmt.Sprintf("00000000-0000-7000-8000-%012d", index+1))
	}
	current.ProfileDigest = ""
	encoded, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	current.ProfileDigest = digestBytes(encoded)
	planning := ProductionPlanning{PolicyRevision: 2, ClassificationPolicyDigest: repeatedDigest('d'), PromotionPolicyDigest: repeatedDigest('e'), VerificationTopologyDigest: repeatedDigest('f')}
	recoveryEvidence := []kernel.UUIDv7{
		"00000000-0000-7000-8001-000000000003",
		"00000000-0000-7000-8001-000000000004",
	}
	next, bound, err := continuedTaskRecoveryProfile(current, planning, repeatedDigest('7'), current.Budgets.DeadlineAt.Add(time.Hour), recoveryEvidence, current.LifecycleEpoch, current.ScopeRevision)
	if err != nil || bound || !next.Valid() || len(next.ClassificationEvidenceIDs) != len(recoveryEvidence) || !containsEveryUUID(next.ClassificationEvidenceIDs, recoveryEvidence) || next.SupersedesProfileID == nil || *next.SupersedesProfileID != current.ProfileID {
		t.Fatalf("compacted continued profile=%#v bound=%t err=%v", next, bound, err)
	}
}

func TestTerminalValidationRecoveryConditionsPreserveCandidateIdentity(t *testing.T) {
	targetID := kernel.UUIDv7("00000000-0000-7000-8000-000000000331")
	candidateDigest := repeatedDigest('a')
	recoveryDigest := repeatedDigest('b')
	task := organization.PlannedTask{
		Purpose:   kernel.PurposeValidation,
		DependsOn: []kernel.UUIDv7{targetID},
		Validates: []kernel.UUIDv7{targetID},
	}
	invocations := map[kernel.AggregateRef]kernel.WorkInvocation{
		{Kind: kernel.AggregateTask, ID: targetID}: {
			ID: "00000000-0000-7000-8000-000000000332", TaskID: targetID, State: kernel.InvocationSucceeded, OutputDigest: &candidateDigest,
		},
	}

	conditions, err := terminalValidationRecoveryConditionDigests(task, invocations, recoveryDigest)
	if err != nil || len(conditions) != 2 || conditions[0] != candidateDigest || conditions[1] != recoveryDigest {
		t.Fatalf("terminal validation recovery conditions=%v err=%v", conditions, err)
	}
}

func TestValidatorConditionMatchesExactTerminalRecoveryLineage(t *testing.T) {
	taskID := kernel.UUIDv7("00000000-0000-7000-8000-000000000331")
	profileDigest := repeatedDigest('1')
	criteriaDigest := repeatedDigest('2')
	candidateDigest := repeatedDigest('3')
	recoveryDigest := repeatedDigest('4')
	baseConditions := []kernel.Digest{candidateDigest}
	baseDigest, err := taskInvocationConditionDigest(profileDigest, criteriaDigest, baseConditions)
	if err != nil {
		t.Fatal(err)
	}
	recoveryCondition, err := taskInvocationConditionDigest(profileDigest, criteriaDigest, []kernel.Digest{candidateDigest, recoveryDigest})
	if err != nil {
		t.Fatal(err)
	}
	prior := recoveryTerminalFixture(kernel.InvocationFailed, nil, nil)
	prior.TaskID = taskID
	prior.Purpose = kernel.PurposeValidation
	prior.AttemptFamily = "validation"
	prior.AttemptOrdinal = 1
	prior.RetryOrdinal = 0
	current := prior.Clone()
	current.ID = "00000000-0000-7000-8000-000000000332"
	current.State = kernel.InvocationSucceeded
	current.AttemptOrdinal = 2
	current.RetryOrdinal = 1
	current.RetryOfInvocationID = &prior.ID
	current.WorkProfile.ProfileDigest = profileDigest
	current.ConditionDigest = recoveryCondition
	output := repeatedDigest('5')
	current.OutputDigest = &output
	snapshot := kernel.Snapshot{WorkInvocations: map[kernel.AggregateRef]kernel.WorkInvocation{prior.Ref(): prior, current.Ref(): current}}
	if !validatorConditionMatches(snapshot, taskID, current, profileDigest, criteriaDigest, baseConditions, baseDigest) {
		t.Fatal("exact terminal validation recovery was not recognized")
	}

	mutated := current.Clone()
	mutated.ConditionDigest = baseDigest
	if !validatorConditionMatches(snapshot, taskID, mutated, profileDigest, criteriaDigest, baseConditions, baseDigest) {
		t.Fatal("ordinary base condition was not recognized")
	}
	mutated.ConditionDigest = recoveryCondition
	mutated.RetryOrdinal++
	if validatorConditionMatches(snapshot, taskID, mutated, profileDigest, criteriaDigest, baseConditions, baseDigest) {
		t.Fatal("mutated retry ordinal matched terminal recovery")
	}
	wrongPrior := kernel.UUIDv7("00000000-0000-7000-8000-000000000333")
	mutated = current.Clone()
	mutated.RetryOfInvocationID = &wrongPrior
	if validatorConditionMatches(snapshot, taskID, mutated, profileDigest, criteriaDigest, baseConditions, baseDigest) {
		t.Fatal("unknown predecessor matched terminal recovery")
	}
	mutated = current.Clone()
	mutated.WorkProfile.ProfileDigest = repeatedDigest('6')
	if validatorConditionMatches(snapshot, taskID, mutated, profileDigest, criteriaDigest, baseConditions, baseDigest) {
		t.Fatal("wrong successor profile matched terminal recovery")
	}
}

func TestValidatorConditionMatchesAcrossMaintenanceOnlyProfileSuccessors(t *testing.T) {
	taskID := kernel.UUIDv7("00000000-0000-7000-8000-000000000351")
	oldProfileID := kernel.UUIDv7("00000000-0000-7000-8000-000000000352")
	newProfileID := kernel.UUIDv7("00000000-0000-7000-8000-000000000353")
	oldProfileDigest := repeatedDigest('1')
	newProfileDigest := repeatedDigest('6')
	criteriaDigest := repeatedDigest('2')
	candidateDigest := repeatedDigest('3')
	recoveryDigest := repeatedDigest('4')
	oldEvidenceID := kernel.UUIDv7("00000000-0000-7000-8000-000000000354")
	newEvidenceID := kernel.UUIDv7("00000000-0000-7000-8000-000000000355")
	oldProfile := validatorMaintenanceProfile(taskID, oldProfileID, 2, oldProfileDigest, time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC), []kernel.UUIDv7{oldEvidenceID})
	newProfile := oldProfile.Clone()
	newProfile.ProfileID = newProfileID
	newProfile.ProfileRevision = 3
	newProfile.ProfileDigest = newProfileDigest
	newProfile.Budgets.DeadlineAt = oldProfile.Budgets.DeadlineAt.Add(time.Hour)
	newProfile.ClassificationEvidenceIDs = []kernel.UUIDv7{oldEvidenceID, newEvidenceID}
	newProfile.SupersedesProfileID = &oldProfileID

	baseConditions := []kernel.Digest{candidateDigest}
	baseDigest, err := taskInvocationConditionDigest(newProfileDigest, criteriaDigest, baseConditions)
	if err != nil {
		t.Fatal(err)
	}
	recoveryCondition, err := taskInvocationConditionDigest(oldProfileDigest, criteriaDigest, []kernel.Digest{candidateDigest, recoveryDigest})
	if err != nil {
		t.Fatal(err)
	}
	prior := recoveryTerminalFixture(kernel.InvocationFailed, nil, nil)
	prior.TaskID = taskID
	prior.Purpose = kernel.PurposeValidation
	prior.AttemptFamily = "validation"
	prior.AttemptOrdinal = 1
	prior.RetryOrdinal = 0
	prior.WorkProfile = oldProfile.Binding()
	current := prior.Clone()
	current.ID = "00000000-0000-7000-8000-000000000356"
	current.State = kernel.InvocationSucceeded
	current.AttemptOrdinal = 2
	current.RetryOrdinal = 1
	current.RetryOfInvocationID = &prior.ID
	current.ConditionDigest = recoveryCondition
	output := repeatedDigest('5')
	current.OutputDigest = &output
	snapshot := kernel.Snapshot{
		WorkInvocations: map[kernel.AggregateRef]kernel.WorkInvocation{prior.Ref(): prior, current.Ref(): current},
		WorkProfiles: map[kernel.AggregateRef]kernel.WorkProfileSnapshot{
			{Kind: kernel.AggregateTask, ID: taskID}: {Profile: newProfile, BoundEventID: "00000000-0000-7000-8000-000000000357", TaskRevision: 2},
		},
		WorkProfileHistory: map[kernel.UUIDv7]kernel.WorkRiskProfile{oldProfileID: oldProfile, newProfileID: newProfile},
	}
	if !validatorConditionMatches(snapshot, taskID, current, newProfileDigest, criteriaDigest, baseConditions, baseDigest) {
		t.Fatal("maintenance-only profile successor invalidated a completed validator")
	}

	priorProfileID := kernel.UUIDv7("00000000-0000-7000-8000-000000000358")
	oldProfile.SupersedesProfileID = &priorProfileID
	newProfile.SupersedesProfileID = &oldProfileID
	prior.State = kernel.InvocationSucceeded
	prior.WorkProfile.ProfileID = priorProfileID
	prior.WorkProfile.ProfileRevision = 1
	prior.WorkProfile.ProfileDigest = repeatedDigest('b')
	prior.ConditionDigest, err = taskInvocationConditionDigest(prior.WorkProfile.ProfileDigest, criteriaDigest, baseConditions)
	if err != nil {
		t.Fatal(err)
	}
	priorOutput := repeatedDigest('c')
	prior.OutputDigest = &priorOutput
	current.RetryOfInvocationID = &prior.ID
	snapshot.WorkInvocations = map[kernel.AggregateRef]kernel.WorkInvocation{prior.Ref(): prior, current.Ref(): current}
	snapshot.WorkProfiles[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: taskID}] = kernel.WorkProfileSnapshot{Profile: newProfile, BoundEventID: "00000000-0000-7000-8000-000000000357", TaskRevision: 2}
	snapshot.WorkProfileHistory = map[kernel.UUIDv7]kernel.WorkRiskProfile{oldProfileID: oldProfile, newProfileID: newProfile}
	if !validatorConditionMatches(snapshot, taskID, current, newProfileDigest, criteriaDigest, baseConditions, baseDigest) {
		t.Fatal("maintenance-only profile successor obscured authorized revalidation lineage")
	}

	semanticChange := newProfile.Clone()
	semanticChange.WorkKind = kernel.WorkSecurityReview
	snapshot.WorkProfiles[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: taskID}] = kernel.WorkProfileSnapshot{Profile: semanticChange, BoundEventID: "00000000-0000-7000-8000-000000000357", TaskRevision: 2}
	snapshot.WorkProfileHistory[newProfileID] = semanticChange
	if validatorConditionMatches(snapshot, taskID, current, newProfileDigest, criteriaDigest, baseConditions, baseDigest) {
		t.Fatal("semantic profile change preserved a stale validator result")
	}
}

func validatorMaintenanceProfile(taskID, profileID kernel.UUIDv7, revision uint64, profileDigest kernel.Digest, deadline time.Time, evidence []kernel.UUIDv7) kernel.WorkRiskProfile {
	return kernel.WorkRiskProfile{
		TaskID: taskID, ProfileID: profileID, ProfileRevision: revision, ProfileDigest: profileDigest,
		LifecycleEpoch: 1, ScopeRevision: 1, WorkKind: kernel.WorkValidation,
		Ambiguity: kernel.AmbiguityLow, Novelty: kernel.NoveltyRoutine, BlastRadius: kernel.BlastLocal, SecuritySensitivity: kernel.SecurityOrdinary,
		MinimumDecisionRoute: kernel.RouteBoundedExecution, AcceptanceCriteriaDigest: repeatedDigest('7'),
		RequiredDeterministicGateIDs: []string{"go-test"}, RequiredValidationBranches: 1,
		RequiredIndependenceDimensions: []kernel.IndependenceDimension{kernel.IndependenceActor},
		ImplementationVariantCount:     1, ValidCandidateQuorum: 1, VerificationTopologyDigest: repeatedDigest('8'),
		ClassificationPolicyRevision: 1, ClassificationPolicyDigest: repeatedDigest('9'),
		PromotionPolicyRevision: 1, PromotionPolicyDigest: repeatedDigest('a'),
		Budgets:                 kernel.FiniteWorkBudgets{AttemptLimit: 2, ReviewRoundLimit: 1, PromotionLimit: 1, EscalationLimit: 1, DeadlineAt: deadline},
		ClassificationAuthority: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"}, ClassificationEvidenceIDs: append([]kernel.UUIDv7(nil), evidence...),
	}
}

func TestValidatorConditionMatchesSuccessfulOperatorRevalidationLineage(t *testing.T) {
	taskID := kernel.UUIDv7("00000000-0000-7000-8000-000000000341")
	priorProfileID := kernel.UUIDv7("00000000-0000-7000-8000-000000000342")
	currentProfileID := kernel.UUIDv7("00000000-0000-7000-8000-000000000343")
	profileDigest := repeatedDigest('1')
	criteriaDigest := repeatedDigest('2')
	candidateDigest := repeatedDigest('3')
	recoveryDigest := repeatedDigest('4')
	priorOutput := repeatedDigest('5')
	baseConditions := []kernel.Digest{candidateDigest}
	baseDigest, err := taskInvocationConditionDigest(profileDigest, criteriaDigest, baseConditions)
	if err != nil {
		t.Fatal(err)
	}
	recoveryCondition, err := taskInvocationConditionDigest(profileDigest, criteriaDigest, []kernel.Digest{candidateDigest, recoveryDigest})
	if err != nil {
		t.Fatal(err)
	}
	prior := recoveryTerminalFixture(kernel.InvocationSucceeded, nil, nil)
	prior.TaskID = taskID
	prior.Purpose = kernel.PurposeValidation
	prior.AttemptFamily = "validation"
	prior.AttemptOrdinal = 1
	prior.RetryOrdinal = 0
	prior.WorkProfile.ProfileID = priorProfileID
	prior.WorkProfile.ProfileDigest = profileDigest
	prior.ConditionDigest = baseDigest
	prior.OutputDigest = &priorOutput
	current := prior.Clone()
	current.ID = "00000000-0000-7000-8000-000000000344"
	current.AttemptOrdinal = 2
	current.RetryOrdinal = 1
	current.RetryOfInvocationID = &prior.ID
	current.WorkProfile.ProfileID = currentProfileID
	current.WorkProfile.ProfileRevision = 2
	current.WorkProfile.ProfileDigest = profileDigest
	current.ConditionDigest = recoveryCondition
	currentOutput := repeatedDigest('6')
	current.OutputDigest = &currentOutput
	snapshot := kernel.Snapshot{
		WorkInvocations: map[kernel.AggregateRef]kernel.WorkInvocation{prior.Ref(): prior, current.Ref(): current},
		WorkProfiles: map[kernel.AggregateRef]kernel.WorkProfileSnapshot{
			{Kind: kernel.AggregateTask, ID: taskID}: {
				Profile: kernel.WorkRiskProfile{
					TaskID: taskID, ProfileID: currentProfileID, ProfileRevision: 2, ProfileDigest: profileDigest,
					LifecycleEpoch: current.WorkProfile.LifecycleEpoch, ScopeRevision: current.WorkProfile.ScopeRevision,
					SupersedesProfileID: &priorProfileID,
				},
			},
		},
	}
	if !validatorConditionMatches(snapshot, taskID, current, profileDigest, criteriaDigest, baseConditions, baseDigest) {
		t.Fatal("successful operator revalidation lineage was not recognized")
	}
	changedConditions := []kernel.Digest{repeatedDigest('7')}
	changedBaseDigest, err := taskInvocationConditionDigest(profileDigest, criteriaDigest, changedConditions)
	if err != nil {
		t.Fatal(err)
	}
	if validatorConditionMatches(snapshot, taskID, current, profileDigest, criteriaDigest, changedConditions, changedBaseDigest) {
		t.Fatal("operator revalidation masked a changed candidate")
	}

	mutatedPrior := prior.Clone()
	mutatedPrior.ConditionDigest = repeatedDigest('8')
	mutated := current.Clone()
	mutated.ConditionDigest = mutatedPrior.ConditionDigest
	snapshot.WorkInvocations = map[kernel.AggregateRef]kernel.WorkInvocation{mutatedPrior.Ref(): mutatedPrior, mutated.Ref(): mutated}
	if validatorConditionMatches(snapshot, taskID, mutated, profileDigest, criteriaDigest, baseConditions, baseDigest) {
		t.Fatal("unchanged predecessor condition matched operator revalidation")
	}
	snapshot.WorkInvocations = map[kernel.AggregateRef]kernel.WorkInvocation{prior.Ref(): prior, current.Ref(): current}
	wrongProfileID := kernel.UUIDv7("00000000-0000-7000-8000-000000000345")
	snapshot.WorkProfiles[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: taskID}] = kernel.WorkProfileSnapshot{Profile: kernel.WorkRiskProfile{
		TaskID: taskID, ProfileID: currentProfileID, ProfileRevision: 2, ProfileDigest: profileDigest,
		LifecycleEpoch: current.WorkProfile.LifecycleEpoch, ScopeRevision: current.WorkProfile.ScopeRevision,
		SupersedesProfileID: &wrongProfileID,
	}}
	if validatorConditionMatches(snapshot, taskID, current, profileDigest, criteriaDigest, baseConditions, baseDigest) {
		t.Fatal("non-successor profile matched operator revalidation")
	}
}

func TestPostExecutionReviewDeadlineUsesDurableTerminalWindow(t *testing.T) {
	profileDeadline := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	firstFinished := time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC)
	lateIngestion := time.Date(2026, 9, 5, 15, 0, 0, 0, time.UTC)
	first := recoveryTerminalFixture(kernel.InvocationSucceeded, nil, nil)
	first.FinishedAt = &firstFinished
	second := recoveryTerminalFixture(kernel.InvocationSucceeded, nil, nil)
	second.FinishedAt = &lateIngestion
	deadline, err := postExecutionReviewDeadline(profileDeadline, 2*time.Hour, first, second)
	if err != nil || !deadline.Equal(time.Date(2026, 9, 5, 17, 0, 0, 0, time.UTC)) {
		t.Fatalf("post-execution review deadline=%s err=%v", deadline, err)
	}
	second.FinishedAt = nil
	if _, err := postExecutionReviewDeadline(profileDeadline, 2*time.Hour, first, second); err == nil {
		t.Fatal("missing terminal timestamp was accepted")
	}
}

func TestReusableFinalizedTaskReviewSurvivesMaintenanceProfileSuccessor(t *testing.T) {
	taskID := kernel.UUIDv7("00000000-0000-7000-8000-000000000661")
	validatorTaskID := kernel.UUIDv7("00000000-0000-7000-8000-000000000662")
	oldProfileID := kernel.UUIDv7("00000000-0000-7000-8000-000000000663")
	newProfileID := kernel.UUIDv7("00000000-0000-7000-8000-000000000664")
	reviewID := kernel.UUIDv7("00000000-0000-7000-8000-000000000665")
	resultEventID := kernel.UUIDv7("00000000-0000-7000-8000-000000000666")
	finalizedEventID := kernel.UUIDv7("00000000-0000-7000-8000-000000000667")
	evidenceID := kernel.UUIDv7("00000000-0000-7000-8000-000000000668")
	additionalEvidenceID := kernel.UUIDv7("00000000-0000-7000-8000-000000000669")
	candidate := repeatedDigest('d')
	oldProfile := validatorMaintenanceProfile(taskID, oldProfileID, 1, repeatedDigest('e'), time.Date(2026, 9, 8, 8, 0, 0, 0, time.UTC), []kernel.UUIDv7{evidenceID})
	newProfile := validatorMaintenanceProfile(taskID, newProfileID, 2, repeatedDigest('f'), time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC), []kernel.UUIDv7{evidenceID, additionalEvidenceID})
	newProfile.SupersedesProfileID = &oldProfileID
	validatorActor := kernel.ActorFQN("example::tester-1")
	branchID := "validator-" + string(validatorTaskID)
	result := structuredValidationResult{Outcome: "PASS", Reasons: []string{"candidate satisfies the acceptance criteria"}}
	review := kernel.CompletionReviewSnapshot{
		ReviewID: reviewID, Subject: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: taskID}, LifecycleEpoch: 1, CriteriaRevision: 1,
		BranchPolicyRevision: 3, RequiredBranchIDs: []string{branchID},
		Branches:       map[string]kernel.ReviewBranchSpec{branchID: {BranchID: branchID, Validator: kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: string(validatorActor)}}},
		ResultRecords:  map[string]kernel.ReviewBranchResult{branchID: {BranchID: branchID, SourceRole: "VALIDATOR", Authority: kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: string(validatorActor)}, Result: "PASS", Reasons: append([]string(nil), result.Reasons...), EventID: resultEventID, CandidateArtifactDigest: candidate}},
		ReviewRevision: 2, Join: kernel.ReviewJoinResult{Complete: true, Status: "PASS"}, ScopeRevision: 1,
		WorkProfile: oldProfile.Binding(), CandidateArtifactDigest: candidate, VerificationTopologyDigest: oldProfile.VerificationTopologyDigest,
		Finalization: &kernel.ReviewFinalization{EventID: finalizedEventID, ReviewRevision: 2, TerminalStatus: "PASS", ResultEventIDs: []kernel.UUIDv7{resultEventID}, ScopeRevision: 1, WorkProfile: oldProfile.Binding(), CandidateArtifactDigest: candidate, VerificationTopologyDigest: oldProfile.VerificationTopologyDigest},
	}
	snapshot := kernel.Snapshot{
		WorkProfiles:       map[kernel.AggregateRef]kernel.WorkProfileSnapshot{{Kind: kernel.AggregateTask, ID: taskID}: {Profile: newProfile, BoundEventID: "00000000-0000-7000-8000-000000000670", TaskRevision: 2}},
		WorkProfileHistory: map[kernel.UUIDv7]kernel.WorkRiskProfile{oldProfileID: oldProfile, newProfileID: newProfile},
		Reviews:            map[kernel.AggregateRef]kernel.CompletionReviewSnapshot{{Kind: kernel.AggregateCompletionReview, ID: reviewID}: review},
		AcceptedEvents:     map[kernel.UUIDv7]kernel.AcceptedEvent{finalizedEventID: {EventType: "tekroo.event.completion-review.finalized"}},
	}
	validators := []taskValidatorResult{{Task: organization.PlannedTask{ID: validatorTaskID}, Invocation: kernel.WorkInvocation{ActorFQN: validatorActor}, Result: result}}
	got, found := reusableFinalizedTaskReview(snapshot, organization.PlannedTask{ID: taskID}, newProfile, candidate, validators, 3)
	if !found || got.ReviewID != reviewID {
		t.Fatalf("durable PASS review was not reused: found=%t review=%s", found, got.ReviewID)
	}

	mutated := review
	mutated.ResultRecords = map[string]kernel.ReviewBranchResult{branchID: review.ResultRecords[branchID]}
	changed := mutated.ResultRecords[branchID]
	changed.Reasons = []string{"different result"}
	mutated.ResultRecords[branchID] = changed
	snapshot.Reviews[kernel.AggregateRef{Kind: kernel.AggregateCompletionReview, ID: reviewID}] = mutated
	if _, found := reusableFinalizedTaskReview(snapshot, organization.PlannedTask{ID: taskID}, newProfile, candidate, validators, 3); found {
		t.Fatal("review with a changed validator result was reused")
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

func TestExplicitInvalidValidatorRecoveryPreservesLineageUnderChangedCondition(t *testing.T) {
	prior := recoveryTerminalFixture(kernel.InvocationSucceeded, nil, nil)
	prior.Purpose = kernel.PurposeHandoff
	prior.AttemptFamily = "handoff"
	if !validInvocationContinuation(prior, kernel.PurposeHandoff, prior.AttemptOrdinal+1, true, false) {
		t.Fatal("configured workflow handoff recovery did not preserve prior invocation lineage")
	}
	if validInvocationContinuation(prior, kernel.PurposeHandoff, prior.AttemptOrdinal+1, false, false) {
		t.Fatal("ordinary handoff continuation accepted a succeeded invocation")
	}

	prior.Purpose = kernel.PurposeReplan
	prior.AttemptFamily = "replan"
	if !validInvocationContinuation(prior, kernel.PurposeReplan, prior.AttemptOrdinal+1, true, false) {
		t.Fatal("explicit invalid-planning-output recovery did not preserve prior invocation lineage")
	}
	if validInvocationContinuation(prior, kernel.PurposeReplan, prior.AttemptOrdinal+1, false, false) {
		t.Fatal("ordinary planning continuation accepted a succeeded invocation")
	}

	prior.Purpose = kernel.PurposeValidation
	prior.AttemptFamily = "validation"
	output := repeatedDigest('9')
	prior.OutputDigest = &output
	if !validInvocationContinuation(prior, kernel.PurposeValidation, prior.AttemptOrdinal+1, true, false) {
		t.Fatal("explicit invalid-validator recovery did not preserve prior invocation lineage")
	}
	if validInvocationContinuation(prior, kernel.PurposeValidation, prior.AttemptOrdinal+1, true, true) {
		t.Fatal("invalid-validator recovery was allowed to reuse the rejected condition")
	}
	if validInvocationContinuation(prior, kernel.PurposeImplementation, prior.AttemptOrdinal+1, true, false) {
		t.Fatal("successful validator was accepted as an implementation recovery")
	}

	targetID := kernel.UUIDv7("00000000-0000-7000-8000-000000000331")
	task := organization.PlannedTask{
		DependsOn: []kernel.UUIDv7{targetID},
		Validates: []kernel.UUIDv7{targetID},
	}
	targetOutput := repeatedDigest('8')
	target := recoveryTerminalFixture(kernel.InvocationSucceeded, nil, nil)
	target.TaskID = targetID
	target.OutputDigest = &targetOutput
	conditions, err := validationRecoveryConditionDigests(task, map[kernel.AggregateRef]kernel.WorkInvocation{target.Ref(): target}, prior)
	if err != nil {
		t.Fatal(err)
	}
	if len(conditions) != 2 || conditions[0] != targetOutput || conditions[1] != output {
		t.Fatalf("validator recovery conditions = %v", conditions)
	}
}
