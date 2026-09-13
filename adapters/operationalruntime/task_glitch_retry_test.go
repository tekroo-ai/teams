package operationalruntime

import (
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestTerminationReasonGlitchClassification(t *testing.T) {
	validator := organization.PlannedTask{Purpose: kernel.PurposeValidation, AttemptLimit: 3, Validates: []kernel.UUIDv7{kernel.UUIDv7("00000000-0000-7000-8000-0000000000a1")}}
	failed := kernel.WorkInvocation{State: kernel.InvocationFailed, AttemptOrdinal: 1}
	digest := kernel.Digest(kernel.UUIDv7("00000000-0000-7000-8000-0000000000b1"))
	failed.OutputDigest = &digest

	if !terminationReasonIsAutoRetryableGlitch([]byte(`{"reason":"WORK_PURPOSE_REPOSITORY_MUTATION_NOT_AUTHORIZED","command":"mkdir -p /tmp/tekroo-fold-probe"}`)) {
		t.Error("an allow-listed enforcement trip must classify as a retryable glitch")
	}
	if !terminationReasonIsAutoRetryableGlitch([]byte(`{"reason":"ROLE_REPOSITORY_MUTATION_NOT_AUTHORIZED","command":"git diff base head -- file > /tmp/file.diff"}`)) {
		t.Error("a role-level repository enforcement trip must classify as a retryable glitch")
	}
	if terminationReasonIsAutoRetryableGlitch([]byte("I finished the validation, here is a summary in prose")) {
		t.Error("unparseable terminal output must stay with the operator")
	}
	if terminationReasonIsAutoRetryableGlitch([]byte(`{"reason":"WORKSPACE_WRITE_OUTSIDE_AUTHORIZED_SCOPE","command":"rm -rf /"}`)) {
		t.Error("a non-allow-listed enforcement trip must stay with the operator")
	}
	if !glitchTerminatedTaskEligible(validator, kernel.AggregateState{Phase: kernel.PhaseActive}, failed) {
		t.Error("a failed validator attempt below the limit must be eligible")
	}
	if glitchTerminatedTaskEligible(validator, kernel.AggregateState{Phase: kernel.PhaseCompleted}, failed) {
		t.Error("a completed task must not be retried")
	}
	exhausted := failed
	exhausted.AttemptOrdinal = 3
	if glitchTerminatedTaskEligible(validator, kernel.AggregateState{Phase: kernel.PhaseActive}, exhausted) {
		t.Error("eligibility must stop at the planned attempt limit")
	}
	retryable := failed
	yes := true
	retryable.Retryable = &yes
	if glitchTerminatedTaskEligible(validator, kernel.AggregateState{Phase: kernel.PhaseActive}, retryable) {
		t.Error("kernel-flagged retryable terminations belong to ordinary admission")
	}
	if !glitchTerminatedTaskEligible(organization.PlannedTask{Purpose: kernel.PurposeImplementation, AttemptLimit: 3}, kernel.AggregateState{Phase: kernel.PhaseActive}, failed) {
		t.Error("eligibility is set by the verdict-less reason class and attempt bound, not by purpose")
	}
}

func TestAutomaticGlitchRecoveryDeadlineIsDeterministicStrictSuccessor(t *testing.T) {
	budget := time.Date(2026, time.September, 13, 5, 47, 36, 0, time.UTC)

	if got, want := automaticGlitchRecoveryDeadline(budget, budget), budget.Add(time.Nanosecond); !got.Equal(want) {
		t.Fatalf("equal deadlines: got %s want %s", got, want)
	}
	invocation := budget.Add(time.Second)
	if got, want := automaticGlitchRecoveryDeadline(budget, invocation), invocation.Add(time.Nanosecond); !got.Equal(want) {
		t.Fatalf("later invocation deadline: got %s want %s", got, want)
	}
}

func TestRepeatedTerminalOutputDoesNotConsumeAnotherAutomaticAttempt(t *testing.T) {
	digest := kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	current := kernel.WorkInvocation{
		ID: "00000000-0000-7000-8000-0000000000a3", TaskID: "00000000-0000-7000-8000-0000000000a1",
		State: kernel.InvocationFailed, OutputDigest: &digest,
	}
	prior := current
	prior.ID = "00000000-0000-7000-8000-0000000000a2"
	snapshot := kernel.Snapshot{WorkInvocations: map[kernel.AggregateRef]kernel.WorkInvocation{
		{Kind: kernel.AggregateWorkInvocation, ID: prior.ID}:   prior,
		{Kind: kernel.AggregateWorkInvocation, ID: current.ID}: current,
	}}
	if !repeatedTerminalOutput(snapshot, current) {
		t.Fatal("identical prior terminal output was not detected")
	}
	prior.State = kernel.InvocationSucceeded
	snapshot.WorkInvocations[kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: prior.ID}] = prior
	if !repeatedTerminalOutput(snapshot, current) {
		t.Fatal("identical successful structured output was not detected")
	}
	otherDigest := kernel.Digest("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	prior.OutputDigest = &otherDigest
	snapshot.WorkInvocations[kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: prior.ID}] = prior
	if repeatedTerminalOutput(snapshot, current) {
		t.Fatal("changed terminal output was incorrectly suppressed")
	}
}
