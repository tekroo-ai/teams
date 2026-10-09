package nativeagent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

func returnedContextFixture(t *testing.T) (*contextJournal, application.ExecutionBrief, kernel.WorkInvocation) {
	j, child, source, _ := contextFixture(t)
	source.TerminalEvidenceIDs = []kernel.UUIDv7{source.ID}
	child.Task.TaskID = source.TaskID
	child.Task.DependsOn = nil // a retry must not introduce a self-dependency
	child.RetryOfInvocationID = &source.ID
	child.AttemptOrdinal = source.AttemptOrdinal + 1
	child.RetryOrdinal = source.RetryOrdinal + 1
	child.ConditionDigest = kernel.Digest(strings.Repeat("b", 64))
	return j, child, source
}

func TestReturnedContextBindsExactRetryAndRepairLineage(t *testing.T) {
	for _, kind := range []string{"retry", "repair"} {
		t.Run(kind, func(t *testing.T) {
			j, child, source := returnedContextFixture(t)
			if kind == "repair" {
				child.RetryOfInvocationID = nil
				child.Purpose = kernel.PurposeRepair
				child.RecoveryDirective = &application.ExecutionRecoveryDirective{SourceEventID: source.ID, Reason: "Apply accepted clarification", Evidence: child.Evidence}
			}
			ref, err := BindContextFork(context.Background(), j, child, source)
			if err != nil {
				t.Fatal(err)
			}
			fork, err := agentruntime.PrepareContextFork(context.Background(), j, string(child.InvocationID), *ref)
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(fork)
			if !strings.Contains(string(encoded), "Original design rationale") {
				t.Fatal("context missing")
			}
			child.Evidence = nil
			if _, err := BindContextFork(context.Background(), j, child, source); err == nil {
				t.Fatal("missing terminal evidence accepted")
			}
		})
	}
	for _, mutate := range []func(*application.ExecutionBrief){
		func(c *application.ExecutionBrief) { c.RetryOrdinal++ },
		func(c *application.ExecutionBrief) { c.AttemptOrdinal++ },
		func(c *application.ExecutionBrief) { c.Task.TaskID = c.InvocationID },
		func(c *application.ExecutionBrief) { c.ConditionDigest = kernel.Digest(strings.Repeat("a", 64)) },
		func(c *application.ExecutionBrief) { c.ActorFQN = "teams::coder-2" },
	} {
		j, child, source := returnedContextFixture(t)
		mutate(&child)
		if _, err := BindContextFork(context.Background(), j, child, source); err == nil {
			t.Fatal("invalid returned context admitted")
		}
	}
}

func TestReturnedContextAcceptsOnlyMatchingTerminalJournal(t *testing.T) {
	for _, terminal := range []struct {
		state kernel.WorkInvocationState
		kind  agentruntime.Kind
	}{
		{kernel.InvocationFailed, agentruntime.Failed}, {kernel.InvocationCancelled, agentruntime.Cancelled}, {kernel.InvocationTimedOut, agentruntime.TimedOut},
	} {
		t.Run(string(terminal.state), func(t *testing.T) {
			j, child, source := returnedContextFixture(t)
			source.State = terminal.state
			source.OutputDigest = nil
			last := &j.streams[string(source.ID)][2]
			last.Kind = terminal.kind
			last.Payload = json.RawMessage(`"original terminal reason"`)
			ref, err := BindContextFork(context.Background(), j, child, source)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := agentruntime.PrepareContextFork(context.Background(), j, string(child.InvocationID), *ref); err != nil {
				t.Fatal(err)
			}
			last.Kind = agentruntime.Finished
			if _, err := BindContextFork(context.Background(), j, child, source); err == nil {
				t.Fatal("terminal/journal mismatch accepted")
			}
		})
	}
}

func TestCorrectedSuccessorContextRequiresExactCoordinatorBinding(t *testing.T) {
	j, child, source, _ := contextFixture(t)
	child.Task.TaskID = child.InvocationID
	child.Task.DependsOn = nil
	grant := &ContextReturnBinding{SourceInvocationID: source.ID, SourceTaskID: source.TaskID, ChildInvocationID: child.InvocationID, ChildTaskID: child.Task.TaskID, CorrectionEvidence: child.Evidence}
	if _, err := BindContextFork(context.Background(), j, child, source); err == nil {
		t.Fatal("blocked successor gained implicit access")
	}
	if _, err := BindContextFork(context.Background(), j, child, source, grant); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*ContextReturnBinding){
		func(g *ContextReturnBinding) { g.ChildInvocationID = source.ID },
		func(g *ContextReturnBinding) { g.SourceInvocationID = child.InvocationID },
		func(g *ContextReturnBinding) { g.ChildTaskID = source.TaskID },
		func(g *ContextReturnBinding) { g.CorrectionEvidence = nil },
	} {
		bad := *grant
		mutate(&bad)
		if _, err := BindContextFork(context.Background(), j, child, source, &bad); err == nil {
			t.Fatal("mismatched return binding accepted")
		}
	}
	child.Evidence = nil
	if _, err := BindContextFork(context.Background(), j, child, source, grant); err == nil {
		t.Fatal("unadmitted correction evidence accepted")
	}
}
