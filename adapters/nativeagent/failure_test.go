package nativeagent

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/application"
)

func TestFailedJournalRetryabilityUsesRetainedFailureClassification(t *testing.T) {
	brief, digest, _ := testBriefAndProfile()
	start, _ := json.Marshal(map[string]any{"request_digest": string(digest), "prompt": "fixture"})
	for _, cause := range []error{agentruntime.ErrReasoningBudgetExhausted, agentruntime.ErrInvalidTurn, agentruntime.ErrTransientModel, errors.New("unknown failure")} {
		f := agentruntime.ClassifyFailure(cause)
		raw, _ := json.Marshal(f)
		entries := []agentruntime.Entry{{InvocationID: string(brief.InvocationID), Sequence: 1, Kind: agentruntime.Started, Payload: start}, {InvocationID: string(brief.InvocationID), Sequence: 2, Kind: agentruntime.Failed, Payload: raw}}
		observation, terminal, err := observeJournalWithPrompt(brief, digest, entries, false)
		if err != nil || !terminal || observation.State != application.ExternalFailed || observation.Retryable != f.Retryable {
			t.Fatalf("%v: %+v %v", cause, observation, err)
		}
	}
	entries := []agentruntime.Entry{{InvocationID: string(brief.InvocationID), Sequence: 1, Kind: agentruntime.Started, Payload: start}, {InvocationID: string(brief.InvocationID), Sequence: 2, Kind: agentruntime.Failed, Payload: json.RawMessage(`"legacy unknown cause"`)}}
	observation, terminal, err := observeJournalWithPrompt(brief, digest, entries, false)
	if err != nil || !terminal || observation.Retryable {
		t.Fatalf("legacy classified retryable: %+v %v", observation, err)
	}
}
