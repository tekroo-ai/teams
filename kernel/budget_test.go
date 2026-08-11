package kernel_test

import (
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
