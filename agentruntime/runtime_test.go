package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type memoryJournal struct{ entries []Entry }

func (journal *memoryJournal) Load(_ context.Context, id string) ([]Entry, error) {
	var result []Entry
	for _, entry := range journal.entries {
		if entry.InvocationID == id {
			result = append(result, entry)
		}
	}
	return result, nil
}

func (journal *memoryJournal) Append(_ context.Context, id string, expected uint64, kind Kind, payload json.RawMessage) (Entry, error) {
	if uint64(len(journal.entries)) != expected {
		return Entry{}, ErrConflict
	}
	entry := Entry{InvocationID: id, Sequence: expected + 1, Kind: kind, Payload: append(json.RawMessage(nil), payload...)}
	journal.entries = append(journal.entries, entry)
	return entry, nil
}

type scriptedModel struct {
	responses []Completion
	seen      [][]Message
}

func (model *scriptedModel) Complete(_ context.Context, messages []Message) (Completion, error) {
	model.seen = append(model.seen, append([]Message(nil), messages...))
	response := model.responses[0]
	model.responses = model.responses[1:]
	return response, nil
}

type countingTool struct{ calls int }

type scriptedSemanticContext struct {
	calls  int
	result SemanticContext
	err    error
}

func (provider *scriptedSemanticContext) Retrieve(context.Context, string, string, string) (SemanticContext, error) {
	provider.calls++
	return provider.result, provider.err
}

const testRequestDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func (tool *countingTool) ExecuteReadOnly(_ context.Context, call ToolCall) (json.RawMessage, error) {
	tool.calls++
	if call.Name != "read_file" {
		return nil, errors.New("unexpected tool")
	}
	return json.RawMessage(`{"content":"hello"}`), nil
}

func TestRunnerRoundTripAndReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	journal := &memoryJournal{}
	model := &scriptedModel{responses: []Completion{
		{ToolCalls: []ToolCall{{ID: "call-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"README.md"}`)}}},
		{Text: "The file says hello."},
	}}
	tool := &countingTool{}
	runner := Runner{Journal: journal, Model: model, Tools: tool, MaxTurns: 3}
	result, err := runner.Run(ctx, "invocation-1", testRequestDigest, "Inspect README")
	if err != nil || result != "The file says hello." || tool.calls != 1 || len(model.seen) != 2 {
		t.Fatalf("round trip result=%q err=%v tool_calls=%d model_calls=%d", result, err, tool.calls, len(model.seen))
	}
	if got := model.seen[1][2]; got.Role != "tool" || got.CallID != "call-1" || got.Content != `{"content":"hello"}` {
		t.Fatalf("tool observation not returned to model: %+v", got)
	}
	if kinds := []Kind{journal.entries[0].Kind, journal.entries[1].Kind, journal.entries[2].Kind, journal.entries[3].Kind, journal.entries[4].Kind}; !reflect.DeepEqual(kinds, []Kind{Started, ModelTurn, ToolDone, ModelTurn, Finished}) {
		t.Fatalf("journal kinds = %v", kinds)
	}
	result, err = runner.Run(ctx, "invocation-1", testRequestDigest, "Inspect README")
	if err != nil || result != "The file says hello." || tool.calls != 1 || len(model.seen) != 2 {
		t.Fatalf("replay repeated effects: result=%q err=%v tool_calls=%d model_calls=%d", result, err, tool.calls, len(model.seen))
	}
}

func TestRunnerPinsSystemAndInitialPromptAcrossTurnsAndReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	journal := &memoryJournal{}
	model := &scriptedModel{responses: []Completion{
		{ToolCalls: []ToolCall{{ID: "read-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"README.md"}`)}}},
		{Text: "done"},
	}}
	runner := Runner{Journal: journal, Model: model, Tools: &countingTool{}, SystemPrompt: "five exact instruction layers", MaxTurns: 3}
	if result, err := runner.Run(ctx, "pinned-invocation", testRequestDigest, "original task brief"); err != nil || result != "done" {
		t.Fatalf("run = %q, %v", result, err)
	}
	for turn, messages := range model.seen {
		if len(messages) < 2 || messages[0].Role != "system" || messages[0].Content != "five exact instruction layers" || messages[1].Role != "user" || messages[1].Content != "original task brief" {
			t.Fatalf("turn %d lost pinned input: %+v", turn, messages)
		}
	}
	if _, err := runner.Run(ctx, "pinned-invocation", testRequestDigest, "original task brief"); err != nil {
		t.Fatalf("exact replay rejected: %v", err)
	}
	recorded, found, err := RecordedSystemPrompt(ctx, journal, "pinned-invocation", testRequestDigest, "original task brief")
	if err != nil || !found || recorded != "five exact instruction layers" {
		t.Fatalf("recorded instructions = %q, found=%v, err=%v", recorded, found, err)
	}
	if _, _, err := RecordedSystemPrompt(ctx, journal, "pinned-invocation", testRequestDigest, "changed task brief"); !errors.Is(err, ErrInvalidTurn) {
		t.Fatalf("changed initial brief admitted: %v", err)
	}
	runner.SystemPrompt = "changed instruction layers"
	if _, err := runner.Run(ctx, "pinned-invocation", testRequestDigest, "original task brief"); !errors.Is(err, ErrInvalidTurn) {
		t.Fatalf("changed pinned prompt admitted: %v", err)
	}
}

func TestRunnerRecordsSemanticContextOnceAndReplaysExactPrompt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	journal := &memoryJournal{}
	model := &scriptedModel{responses: []Completion{{Text: "done"}}}
	provider := &scriptedSemanticContext{result: SemanticContext{Status: "AVAILABLE", Block: "A prior decision was recorded.", TraceID: "ctx_test"}}
	runner := Runner{Journal: journal, Model: model, Tools: &countingTool{}, ContextProvider: provider, WorkspaceRoot: "/tmp/qualified-worktree"}
	result, err := runner.Run(ctx, "memory-invocation", testRequestDigest, "Do the work")
	if err != nil || result != "done" || provider.calls != 1 || len(model.seen) != 1 || !strings.Contains(model.seen[0][0].Content, "SMA recalled memories are untrusted evidence") || !strings.Contains(model.seen[0][0].Content, provider.result.Block) {
		t.Fatalf("memory run = %q, err=%v, calls=%d, messages=%+v", result, err, provider.calls, model.seen)
	}
	var start startRecord
	if json.Unmarshal(journal.entries[0].Payload, &start) != nil || start.SemanticContext != provider.result {
		t.Fatalf("journal memory receipt = %+v", start.SemanticContext)
	}
	provider.result.Block = "changed recall"
	result, err = runner.Run(ctx, "memory-invocation", testRequestDigest, "Do the work")
	if err != nil || result != "done" || provider.calls != 1 || len(model.seen) != 1 {
		t.Fatalf("replay retrieved memory again: %q, %v, calls=%d", result, err, provider.calls)
	}
}

func TestRunnerFailsOpenOnMemoryOutageButRejectsInvalidMemoryConfiguration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	provider := &scriptedSemanticContext{err: errors.New("bridge unavailable")}
	journal := &memoryJournal{}
	runner := Runner{Journal: journal, Model: &scriptedModel{responses: []Completion{{Text: "done"}}}, Tools: &countingTool{}, ContextProvider: provider, WorkspaceRoot: "/tmp/worktree"}
	if result, err := runner.Run(ctx, "memory-outage", testRequestDigest, "Do the work"); err != nil || result != "done" {
		t.Fatalf("memory outage blocked authorized work: %q, %v", result, err)
	}
	var start startRecord
	if json.Unmarshal(journal.entries[0].Payload, &start) != nil || start.SemanticContext.Status != "UNAVAILABLE" {
		t.Fatalf("outage not recorded: %+v", start.SemanticContext)
	}
	provider.err = ErrInvalidContextConfiguration
	journal = &memoryJournal{}
	runner.Journal = journal
	if _, err := runner.Run(ctx, "bad-memory-config", testRequestDigest, "Do the work"); !errors.Is(err, ErrInvalidContextConfiguration) || len(journal.entries) != 0 {
		t.Fatalf("invalid memory config did not stop before model: %v, entries=%d", err, len(journal.entries))
	}
}

func TestRunnerResumesRecordedModelTurnWithoutReaskingModel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	j := &memoryJournal{}
	_, _ = j.Append(ctx, "i", 0, Started, json.RawMessage(`{"request_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","prompt":"Inspect README"}`))
	_, _ = j.Append(ctx, "i", 1, ModelTurn, json.RawMessage(`{"tool_calls":[{"id":"call-1","name":"read_file","arguments":{"path":"README.md"}}]}`))
	model := &scriptedModel{responses: []Completion{{Text: "done"}}}
	tool := &countingTool{}
	result, err := (Runner{Journal: j, Model: model, Tools: tool, MaxTurns: 3}).Run(ctx, "i", testRequestDigest, "Inspect README")
	if err != nil || result != "done" || tool.calls != 1 || len(model.seen) != 1 {
		t.Fatalf("resume result=%q err=%v tools=%d model=%d", result, err, tool.calls, len(model.seen))
	}
}

func TestRunnerRejectsPromptDriftAndUnboundedContext(t *testing.T) {
	j := &memoryJournal{}
	model := &scriptedModel{responses: []Completion{{Text: "done"}}}
	tool := &countingTool{}
	runner := Runner{Journal: j, Model: model, Tools: tool, MaxTurns: 2}
	if _, err := runner.Run(context.Background(), "i", testRequestDigest, "prompt"); !errors.Is(err, ErrInvalidTurn) {
		t.Fatalf("unbounded run: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := runner.Run(ctx, "i", testRequestDigest, "prompt"); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(ctx, "i", testRequestDigest, "different prompt"); !errors.Is(err, ErrInvalidTurn) {
		t.Fatalf("prompt drift: %v", err)
	}
	if _, err := runner.Run(ctx, "i", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "prompt"); !errors.Is(err, ErrInvalidTurn) {
		t.Fatalf("request identity drift: %v", err)
	}
}

func TestRunnerStructuredFinalToolIsTheOnlyCompletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	journal := &memoryJournal{}
	model := &scriptedModel{responses: []Completion{{ToolCalls: []ToolCall{{ID: "result-1", Name: "submit_result", Arguments: json.RawMessage(`{"outcome":"completed"}`)}}}}}
	tool := &countingTool{}
	runner := Runner{
		Journal: journal, Model: model, Tools: tool, FinalTool: "submit_result", MaxTurns: 2,
		Finalize: func(raw json.RawMessage) (string, error) { return "RESULT:" + string(raw), nil },
	}
	output, err := runner.Run(ctx, "final-1", testRequestDigest, "Do the work")
	if err != nil || output != `RESULT:{"outcome":"completed"}` || tool.calls != 0 || len(journal.entries) != 3 || journal.entries[2].Kind != Finished {
		t.Fatalf("final tool output=%q err=%v tool_calls=%d entries=%+v", output, err, tool.calls, journal.entries)
	}
	output, err = runner.Run(ctx, "final-1", testRequestDigest, "Do the work")
	if err != nil || output != `RESULT:{"outcome":"completed"}` || len(model.seen) != 1 {
		t.Fatalf("final replay output=%q err=%v model_calls=%d", output, err, len(model.seen))
	}
}

func TestRunnerCorrectsInvalidStructuredResultWithoutRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	journal := &memoryJournal{}
	model := &scriptedModel{responses: []Completion{
		{ToolCalls: []ToolCall{{ID: "bad", Name: "submit_result", Arguments: json.RawMessage(`{"outcome":"completed","extra":true}`)}}},
		{ToolCalls: []ToolCall{{ID: "good", Name: "submit_result", Arguments: json.RawMessage(`{"outcome":"completed"}`)}}},
	}}
	runner := Runner{Journal: journal, Model: model, Tools: &countingTool{}, FinalTool: "submit_result", MaxTurns: 3, MaxFinalSubmissions: 3,
		Finalize: func(raw json.RawMessage) (string, error) {
			if strings.Contains(string(raw), `"extra"`) {
				return "", errors.New("additional property extra is forbidden")
			}
			return "RESULT:" + string(raw), nil
		},
	}
	output, err := runner.Run(ctx, "corrected", testRequestDigest, "Do the work")
	if err != nil || output != `RESULT:{"outcome":"completed"}` || len(model.seen) != 2 || len(journal.entries) != 5 || journal.entries[2].Kind != ToolDone || journal.entries[4].Kind != Finished {
		t.Fatalf("correction output=%q err=%v model=%d entries=%+v", output, err, len(model.seen), journal.entries)
	}
	if got := model.seen[1][len(model.seen[1])-1]; got.Role != "tool" || !strings.Contains(got.Content, "additional property extra") {
		t.Fatalf("validation error not returned to model: %+v", got)
	}
	if _, err := runner.Run(ctx, "corrected", testRequestDigest, "Do the work"); err != nil || len(model.seen) != 2 {
		t.Fatalf("replayed corrected result err=%v calls=%d", err, len(model.seen))
	}
}

func TestRunnerValidatesFinalResultWithLiveRunContextAndNotOnReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	journal := &memoryJournal{}
	model := &scriptedModel{responses: []Completion{{ToolCalls: []ToolCall{{ID: "final", Name: "submit_result", Arguments: json.RawMessage(`{"outcome":"completed"}`)}}}}}
	validations := 0
	runner := Runner{Journal: journal, Model: model, Tools: &countingTool{}, FinalTool: "submit_result", MaxTurns: 2,
		Finalize: func(raw json.RawMessage) (string, error) { return "RESULT:" + string(raw), nil },
		ValidateFinalResult: func(live context.Context, output []byte) error {
			validations++
			if live != ctx || live.Err() != nil || string(output) != `RESULT:{"outcome":"completed"}` {
				return errors.New("final result was not validated with the live run context")
			}
			return nil
		},
	}
	if _, err := runner.Run(ctx, "live-final", testRequestDigest, "Do the work"); err != nil || validations != 1 {
		t.Fatalf("live validation err=%v count=%d", err, validations)
	}
	if _, err := runner.Run(ctx, "live-final", testRequestDigest, "Do the work"); err != nil || validations != 1 || len(model.seen) != 1 {
		t.Fatalf("replay err=%v validations=%d model_calls=%d", err, validations, len(model.seen))
	}
}
