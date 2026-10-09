package nativeagent

import (
	"context"
	"strings"
	"testing"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

func prestartRetryFixture(t *testing.T, returnedDecision bool) (*contextJournal, application.ExecutionBrief, kernel.WorkInvocation, *ContextReturnBinding) {
	t.Helper()
	journal, child, source := returnedContextFixture(t)
	child.Purpose = source.Purpose
	prior := source.Clone()
	prior.ID = "019ff000-0000-7000-8000-000000000099"
	prior.State = kernel.InvocationStartFailed
	prior.StartedAt, prior.ConversationID, prior.RequestDigest, prior.OutputDigest = nil, nil, nil, nil
	prior.AttemptOrdinal, prior.RetryOrdinal = source.AttemptOrdinal+1, source.RetryOrdinal+1
	prior.RetryOfInvocationID = &source.ID
	prior.ConditionDigest = kernel.Digest(strings.Repeat("c", 64))
	prior.TerminalEvidenceIDs = []kernel.UUIDv7{prior.ID}
	child.AttemptOrdinal, child.RetryOrdinal = prior.AttemptOrdinal+1, prior.RetryOrdinal+1
	child.RetryOfInvocationID = &prior.ID
	child.Evidence = append(child.Evidence, kernel.EvidenceRef{EvidenceID: prior.ID, SHA256: kernel.Digest(strings.Repeat("d", 64))})
	if returnedDecision {
		// The first returned attempt never began. Its original decision owner's
		// task remains the authoritative dependency, not a retry of that task.
		child.Task.TaskID = child.InvocationID
		child.Task.DependsOn = []kernel.UUIDv7{source.TaskID}
		prior.TaskID = child.Task.TaskID
		prior.AttemptOrdinal, prior.RetryOrdinal, prior.RetryOfInvocationID = 1, 0, nil
		child.AttemptOrdinal, child.RetryOrdinal = 2, 1
	}
	grant := &ContextReturnBinding{SourceInvocationID: source.ID, SourceTaskID: source.TaskID, ChildInvocationID: child.InvocationID, ChildTaskID: child.Task.TaskID, PreStartRetries: []kernel.WorkInvocation{prior}}
	return journal, child, source, grant
}

func TestPrestartRetriesPreserveLastStartedOrOriginalDecisionHistory(t *testing.T) {
	for _, returnedDecision := range []bool{false, true} {
		journal, child, source, grant := prestartRetryFixture(t, returnedDecision)
		if _, err := BindContextFork(t.Context(), journal, child, source); err == nil {
			t.Fatal("retry skipped a predecessor without lineage authority")
		}
		ref, err := BindContextFork(t.Context(), journal, child, source, grant)
		if err != nil {
			t.Fatal(err)
		}
		fork, err := agentruntime.PrepareContextFork(t.Context(), journal, string(child.InvocationID), *ref)
		if err != nil || len(fork.WorkingHistory) == 0 {
			t.Fatal("original history missing", err)
		}
	}
}

func TestPrestartRetryCannotDiscardStartedOrUnrelatedHistory(t *testing.T) {
	for _, mutate := range []func(*contextJournal, *application.ExecutionBrief, *ContextReturnBinding){
		func(_ *contextJournal, _ *application.ExecutionBrief, grant *ContextReturnBinding) {
			grant.PreStartRetries[0].RequestDigest = &grant.PreStartRetries[0].ConditionDigest
		},
		func(_ *contextJournal, _ *application.ExecutionBrief, grant *ContextReturnBinding) {
			grant.PreStartRetries[0].StartedAt = grant.PreStartRetries[0].FinishedAt
		},
		func(_ *contextJournal, _ *application.ExecutionBrief, grant *ContextReturnBinding) {
			grant.PreStartRetries[0].ActorFQN = "teams::coder-2"
		},
		func(_ *contextJournal, _ *application.ExecutionBrief, grant *ContextReturnBinding) {
			grant.PreStartRetries[0].RetryOfInvocationID = nil
		},
		func(_ *contextJournal, child *application.ExecutionBrief, _ *ContextReturnBinding) {
			child.Evidence = nil
		},
		func(j *contextJournal, _ *application.ExecutionBrief, grant *ContextReturnBinding) {
			j.streams[string(grant.PreStartRetries[0].ID)] = []agentruntime.Entry{{Kind: agentruntime.Started}}
		},
	} {
		journal, child, source, grant := prestartRetryFixture(t, false)
		mutate(journal, &child, grant)
		if _, err := BindContextFork(context.Background(), journal, child, source, grant); err == nil {
			t.Fatal("invalid pre-start history bridge accepted")
		}
	}
}
