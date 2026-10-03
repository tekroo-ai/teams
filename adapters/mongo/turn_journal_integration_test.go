//go:build mongo_integration

package mongo

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/agentruntime"
)

func TestTurnJournalPersistsAndRejectsStaleAppend(t *testing.T) {
	store := openTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := store.Append(ctx, "invocation-1", 0, agentruntime.Started, json.RawMessage(`"prompt"`)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(ctx, "invocation-1", 0, agentruntime.Started, json.RawMessage(`"prompt"`)); !errors.Is(err, agentruntime.ErrConflict) {
		t.Fatalf("duplicate append = %v", err)
	}
	if _, err := store.Append(ctx, "invocation-1", 1, agentruntime.ModelTurn, json.RawMessage(`{"text":"done"}`)); err != nil {
		t.Fatal(err)
	}
	entries, err := store.Load(ctx, "invocation-1")
	if err != nil || len(entries) != 2 || entries[0].Sequence != 1 || entries[1].Sequence != 2 || entries[1].Kind != agentruntime.ModelTurn {
		t.Fatalf("persisted entries=%+v err=%v", entries, err)
	}
}

type turnTestModel struct{ calls int }

func (model *turnTestModel) Complete(_ context.Context, messages []agentruntime.Message) (agentruntime.Completion, error) {
	model.calls++
	if model.calls == 1 {
		return agentruntime.Completion{ToolCalls: []agentruntime.ToolCall{{ID: "read-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"README.md"}`)}}}, nil
	}
	if len(messages) != 3 || messages[2].Role != "tool" {
		return agentruntime.Completion{}, agentruntime.ErrInvalidTurn
	}
	return agentruntime.Completion{Text: "done"}, nil
}

type turnTestTool struct{ calls int }

func (tool *turnTestTool) ExecuteReadOnly(_ context.Context, _ agentruntime.ToolCall) (json.RawMessage, error) {
	tool.calls++
	return json.RawMessage(`{"content":"hello"}`), nil
}

func TestTurnRunnerMongoRoundTripAndRestart(t *testing.T) {
	store := openTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	model := &turnTestModel{}
	tool := &turnTestTool{}
	runner := agentruntime.Runner{Journal: store, Model: model, Tools: tool, MaxTurns: 3}
	const digest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	output, err := runner.Run(ctx, "invocation-mongo", digest, "Read README")
	if err != nil || output != "done" || model.calls != 2 || tool.calls != 1 {
		t.Fatalf("first run: output=%q err=%v model=%d tool=%d", output, err, model.calls, tool.calls)
	}
	// Reopen the MongoDB connection and reconstruct the runner. Neither the
	// model request nor the completed tool call may be repeated.
	reopened, err := Open(ctx, testConfig(testMongoURI, store.db.Name()))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(ctx)
	restarted := agentruntime.Runner{Journal: reopened, Model: model, Tools: tool, MaxTurns: 3}
	output, err = restarted.Run(ctx, "invocation-mongo", digest, "Read README")
	if err != nil || output != "done" || model.calls != 2 || tool.calls != 1 {
		t.Fatalf("restart: output=%q err=%v model=%d tool=%d", output, err, model.calls, tool.calls)
	}
}
