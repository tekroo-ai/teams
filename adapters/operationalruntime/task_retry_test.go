package operationalruntime

import (
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestOrdinaryRetryableTerminalEligibilityIsBounded(t *testing.T) {
	yes := true
	task := organization.PlannedTask{Purpose: kernel.PurposeImplementation, AttemptLimit: 3, ReviewRoundLimit: 2}
	state := kernel.AggregateState{Phase: kernel.PhaseActive, Condition: kernel.ConditionRunnable}
	invocation := kernel.WorkInvocation{Purpose: kernel.PurposeImplementation, State: kernel.InvocationFailed, AttemptOrdinal: 1, Retryable: &yes}
	if !ordinaryRetryableTerminalEligible(task, state, invocation) {
		t.Fatal("first retryable implementation failure must admit its bounded successor")
	}
	invocation.AttemptOrdinal = 3
	if ordinaryRetryableTerminalEligible(task, state, invocation) {
		t.Fatal("attempt ceiling must stop ordinary retry")
	}
	invocation.AttemptOrdinal = 1
	invocation.Retryable = nil
	if ordinaryRetryableTerminalEligible(task, state, invocation) {
		t.Fatal("an unclassified failure must remain an operator decision")
	}
	invocation.Retryable = &yes
	invocation.State = kernel.InvocationSucceeded
	if ordinaryRetryableTerminalEligible(task, state, invocation) {
		t.Fatal("a successful invocation is not an ordinary terminal retry")
	}
}

func TestAutomaticRetryableRecoveryDeadlineIsInvocationBound(t *testing.T) {
	deadline := time.Date(2026, 9, 14, 9, 59, 0, 0, time.FixedZone("local", -6*60*60))
	want := deadline.UTC().Add(time.Nanosecond)
	if got := automaticRetryableRecoveryDeadline(deadline); !got.Equal(want) {
		t.Fatalf("deadline = %s, want %s", got, want)
	}
}

func TestAutomaticRetryableIdempotencyKeyLeavesEnvelopeRunway(t *testing.T) {
	invocationID := kernel.UUIDv7("00000000-0000-7000-8000-000000000001")
	key := automaticRetryableIdempotencyKey(invocationID)
	if len(key) != len("auto-r3-")+36 {
		t.Fatalf("key length = %d", len(key))
	}
	// RetryFailedTask composes this value with the feature, task, terminal,
	// and successor-profile identities. Preserve ample room under the kernel's
	// 256-byte idempotency-envelope bound.
	if len(key) > 64 {
		t.Fatalf("automatic recovery key is not compact: %q", key)
	}
}
