package kernel

import (
	"encoding/json"
	"testing"
	"time"
)

func TestTaskBudgetBindingUsesTaskLifecycleIndependentOfSharedAccountLifecycle(t *testing.T) {
	taskID := UUIDv7("00000000-0000-7000-8000-000000000951")
	storyID := UUIDv7("00000000-0000-7000-8000-000000000952")
	budgetID := UUIDv7("00000000-0000-7000-8000-000000000953")
	budgetRef := AggregateRef{Kind: AggregateWorkBudget, ID: budgetID}
	limits := zeroPurposeCounters()
	used := zeroPurposeCounters()
	for _, purpose := range AllWorkPurposes {
		limits[purpose] = 8
	}
	account := WorkBudgetAccount{
		ID: budgetID, Revision: 3, RootWork: AggregateRef{Kind: AggregateStory, ID: storyID},
		LifecycleEpoch: 1, PolicyRevision: 1, PolicyDigest: Digest("1111111111111111111111111111111111111111111111111111111111111111"),
		ModelInvocationLimit: 8, PurposeLimits: limits, PurposeUsed: used,
		DeadlineAt: time.Date(2026, 9, 8, 8, 0, 0, 0, time.UTC), LastEventID: UUIDv7("00000000-0000-7000-8000-000000000954"),
	}
	payload, err := json.Marshal(map[string]any{
		"task_id": taskID, "budget_account_id": budgetID, "expected_task_revision": uint64(7),
		"lifecycle_epoch": uint64(2), "scope_revision": uint64(2),
		"task_model_invocation_limit": uint64(8), "purpose_limits": limits,
		"evidence_ids": []UUIDv7{},
	})
	if err != nil {
		t.Fatal(err)
	}
	command := KernelCommand{
		CommandType: "tekroo.command.task.bind-work-budget", Target: AggregateRef{Kind: AggregateTask, ID: taskID},
		Preconditions: []AggregatePrecondition{{Aggregate: budgetRef, Expected: NewExpectedRevision(account.Revision)}}, Payload: payload,
	}
	snapshot := Snapshot{
		Revision:           7,
		State:              &AggregateState{Kind: AggregateTask, ID: taskID, Revision: 7, LifecycleEpoch: 2, ScopeRevision: 2},
		WorkBudgetAccounts: map[AggregateRef]WorkBudgetAccount{budgetRef: account},
		TaskWorkBudgets:    map[AggregateRef]TaskWorkBudgetBinding{},
	}
	decision, outcome, reason, handled := evaluatePhase4Command(command, snapshot, DecisionContext{EventID: UUIDv7("00000000-0000-7000-8000-000000000955")})
	if !handled || outcome != OutcomeApplied || reason != reasonApplied || decision.LifecycleEpoch != 2 {
		t.Fatalf("decision=%#v outcome=%s reason=%s handled=%t", decision, outcome, reason, handled)
	}
}
