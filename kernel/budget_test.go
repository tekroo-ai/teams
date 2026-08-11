package kernel_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/tekroo-ai/teams/kernel"
)

func TestAttemptBudgetIsFiniteIdempotentAndRestartStable(t *testing.T) {
	budget := kernel.NewAttemptBudget(3)
	digests := []kernel.Digest{
		kernel.Digest("1111111111111111111111111111111111111111111111111111111111111111"),
		kernel.Digest("2222222222222222222222222222222222222222222222222222222222222222"),
		kernel.Digest("3333333333333333333333333333333333333333333333333333333333333333"),
		kernel.Digest("4444444444444444444444444444444444444444444444444444444444444444"),
	}
	if got := budget.Consume("attempt-1", digests[0], false, false); got != kernel.BudgetRejected {
		t.Fatalf("unchanged retry = %s, want REJECTED_UNCHANGED", got)
	}
	if got := budget.Consume("attempt-1", digests[0], true, false); got != kernel.BudgetConsumed {
		t.Fatalf("first attempt = %s, want CONSUMED", got)
	}
	if got := budget.Consume("attempt-1", digests[0], true, false); got != kernel.BudgetDuplicate || budget.Used != 1 {
		t.Fatalf("duplicate = %s, used = %d", got, budget.Used)
	}
	restarted := budget.Clone()
	if got := restarted.Consume("attempt-2", digests[1], true, false); got != kernel.BudgetConsumed {
		t.Fatalf("post-restart attempt = %s", got)
	}
	if got := restarted.Consume("attempt-3", digests[2], false, true); got != kernel.BudgetConsumed {
		t.Fatalf("authorized override = %s", got)
	}
	if got := restarted.Consume("attempt-4", digests[3], true, false); got != kernel.BudgetExhausted || restarted.Used != 3 {
		t.Fatalf("exhausted attempt = %s, used = %d", got, restarted.Used)
	}
	if budget.Used != 1 {
		t.Fatalf("clone mutated pre-restart state: used = %d", budget.Used)
	}
}

func TestEvaluatorConsumesConfiguredDurableAttemptBudget(t *testing.T) {
	evaluator := kernel.Evaluator{Catalogue: loadCatalogue(t)}
	command := validStoryCreateCommand(t)
	context := validDecisionContext(t)
	grant := grantFor(command.Authority, command, context.Provenance.GrantDigests[0])
	snapshot := kernel.Snapshot{Authorization: authorizationPolicy(grant)}
	snapshot.Authorization.Requirements.AttemptLimits = map[string]uint32{command.CommandType: 2}

	first := evaluate(t, evaluator, command, snapshot, context)
	if first.Receipt.OutcomeCode != kernel.OutcomeApplied || first.AttemptBudget == nil || first.AttemptBudget.ExpectedUsed != 0 {
		t.Fatalf("first governed attempt = %#v", first)
	}
	key := first.AttemptBudget.Key
	snapshot.AttemptBudgets = map[kernel.AttemptBudgetKey]kernel.AttemptBudgetSnapshot{key: {
		Limit: 2, Used: 1, Attempts: map[string]kernel.Digest{first.AttemptBudget.AttemptKey: first.AttemptBudget.ConditionDigest},
	}}
	unchangedCommand := command
	unchangedCommand.CommandID = mustUUID(t, "00000000-0000-7000-8000-000000000010")
	unchanged := evaluate(t, evaluator, unchangedCommand, snapshot, context)
	if unchanged.Receipt.OutcomeCode != kernel.OutcomeRejectedPolicy || unchanged.Receipt.ReasonCode != "RETRY_CONDITION_UNCHANGED" {
		t.Fatalf("unchanged governed attempt = %#v", unchanged)
	}
	secondCommand := command
	secondCommand.CommandID = mustUUID(t, "00000000-0000-7000-8000-000000000011")
	secondCommand.Payload = json.RawMessage(`{"acceptance_criteria":["one owner wins"],"description":"Exact ownership.","title":"Ownership revised"}`)
	second := evaluate(t, evaluator, secondCommand, snapshot, context)
	if second.Receipt.OutcomeCode != kernel.OutcomeApplied || second.AttemptBudget == nil || second.AttemptBudget.ExpectedUsed != 1 {
		t.Fatalf("second governed attempt = %#v", second)
	}
	snapshot.AttemptBudgets[key] = kernel.AttemptBudgetSnapshot{
		Limit: 2, Used: 2,
		Attempts: map[string]kernel.Digest{
			first.AttemptBudget.AttemptKey:  first.AttemptBudget.ConditionDigest,
			second.AttemptBudget.AttemptKey: second.AttemptBudget.ConditionDigest,
		},
	}
	thirdCommand := secondCommand
	thirdCommand.CommandID = mustUUID(t, "00000000-0000-7000-8000-000000000012")
	thirdCommand.Payload = json.RawMessage(`{"acceptance_criteria":["one owner wins"],"description":"A third condition.","title":"Ownership revised"}`)
	third := evaluate(t, evaluator, thirdCommand, snapshot, context)
	if third.Receipt.OutcomeCode != kernel.OutcomeRejectedPolicy || third.Receipt.ReasonCode != "RETRY_BUDGET_EXHAUSTED" || third.AttemptBudget != nil {
		t.Fatalf("exhausted governed attempt = %#v", third)
	}
}

func TestGeneratedAttemptBudgetsNeverExceedLimit(t *testing.T) {
	const seed uint64 = 0x71e5b00d
	generator := seed
	for history := 0; history < 4096; history++ {
		limit := uint32(nextRandom(&generator)%16) + 1
		budget := kernel.NewAttemptBudget(limit)
		for step := 0; step < 128; step++ {
			ordinal := nextRandom(&generator) % 32
			character := "0123456789abcdef"[ordinal%16]
			digest := kernel.Digest(strings.Repeat(string(character), 64))
			budget.Consume(fmt.Sprintf("attempt-%d", ordinal), digest, nextRandom(&generator)&1 == 1, nextRandom(&generator)&7 == 0)
			if budget.Used > limit {
				t.Fatalf("seed=%x history=%d step=%d used=%d limit=%d", seed, history, step, budget.Used, limit)
			}
		}
	}
}
