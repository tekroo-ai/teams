package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPinnedProjectReferenceVerifiesDigestBytesAndVisibleSource(t *testing.T) {
	project := strings.Repeat("instruction line\n", 100)
	sum := sha256.Sum256([]byte(project))
	hash := hex.EncodeToString(sum[:])
	system := Message{Role: "system", Content: "[PROJECT | source=workspace AGENTS.md | sha256=" + hash + "]\n" + project + "\n[/ PROJECT]\n"}
	excerpt := readMessage("read", "AGENTS.md", hash, 1, 50, strings.TrimSuffix(strings.Repeat("instruction line\n", 50), "\n"))
	messages := []Message{system, excerpt}
	delivered := referenceRepeatedReads(messages)
	if !reflect.DeepEqual(delivered[0], system) || !reflect.DeepEqual(messages[1], excerpt) || !strings.Contains(delivered[1].Content, `"source":"PINNED_PROJECT_LAYER"`) || strings.Contains(delivered[1].Content, `"output"`) {
		t.Fatalf("lost pin or raw read: %+v", delivered)
	}
	for _, candidate := range [][]Message{
		{excerpt},
		{{Role: "system", Content: strings.Replace(system.Content, "instruction line", "changed", 1)}, excerpt},
		{system, readMessage("changed", "AGENTS.md", strings.Repeat("b", 64), 1, 50, strings.TrimSuffix(strings.Repeat("instruction line\n", 50), "\n"))},
		{system, readMessage("bytes", "AGENTS.md", hash, 1, 50, "not the pinned bytes")},
		{system, readMessage("range", "AGENTS.md", hash, 1, 1000, excerpt.Content)},
	} {
		if got := referenceRepeatedReads(candidate); !reflect.DeepEqual(got, candidate) {
			t.Fatalf("unverified reference: %+v", got)
		}
	}
	if got := referenceRepeatedReads(messages); !reflect.DeepEqual(got, delivered) {
		t.Fatal("restart reference differs")
	}
}

func readMessage(id, path, hash string, start, end int, content string) Message {
	payload, _ := json.Marshal(map[string]any{"name": "read_file", "path": path, "sha256": hash, "start_line": start, "end_line": end, "output": content, "line_count": 20, "truncated": true})
	return Message{Role: "tool", Name: "read_file", CallID: id, Content: string(payload)}
}

func TestReadReferenceOnlyForExactVisibleUnchangedExcerpt(t *testing.T) {
	hash := strings.Repeat("a", 64)
	content := strings.Repeat("some source line\n", 100)
	first := readMessage("first", "file.go", hash, 1, 10, content)
	duplicate := readMessage("again", "file.go", hash, 1, 10, content)
	messages := []Message{{Role: "system", Content: "pinned"}, first, duplicate,
		readMessage("section", "file.go", hash, 11, 20, content),
		readMessage("changed", "file.go", strings.Repeat("b", 64), 1, 10, content),
		readMessage("different", "other.go", hash, 1, 10, content),
		readMessage("bytes", "file.go", hash, 1, 10, content+"new"),
		{Role: "tool", Name: "read_file", CallID: "denied", Content: `"agent tool is not authorized"`},
		{Role: "tool", Name: "read_file", CallID: "missing", Content: `{"path":"file.go","status":"NOT_FOUND"}`},
	}
	original := append([]Message(nil), messages...)
	delivered := referenceRepeatedReads(messages)
	if !reflect.DeepEqual(messages, original) {
		t.Fatal("raw history mutated")
	}
	for i := range delivered {
		if i != 2 && !reflect.DeepEqual(delivered[i], original[i]) {
			t.Fatalf("nonduplicate %d changed", i)
		}
	}
	var fields struct {
		Output    string        `json:"output"`
		Delivery  string        `json:"content_delivery"`
		Reference readReference `json:"content_reference"`
		LineCount int           `json:"line_count"`
	}
	if err := json.Unmarshal([]byte(delivered[2].Content), &fields); err != nil {
		t.Fatal(err)
	}
	if fields.Output != "" || fields.Delivery != "IN_CONTEXT_REFERENCE" || fields.Reference.CallID != "first" || fields.Reference.SHA256 != hash || fields.LineCount != 20 {
		t.Fatalf("reference %+v", fields)
	}
	// History removal/condensation and a fresh invocation cannot use an old
	// index. The remaining read must deliver full contents again.
	if got := referenceRepeatedReads([]Message{duplicate}); !reflect.DeepEqual(got, []Message{duplicate}) {
		t.Fatal("dangling reference after history removal")
	}
	if got := referenceRepeatedReads(messages); !reflect.DeepEqual(got, delivered) {
		t.Fatal("restart presentation is not deterministic")
	}
}

type repeatedReadTool struct {
	calls   int
	message Message
}

func (tool *repeatedReadTool) ExecuteReadOnly(context.Context, ToolCall) (json.RawMessage, error) {
	tool.calls++
	return json.RawMessage(tool.message.Content), nil
}

type pauseAfterReads struct{ seen []Message }

func (model *pauseAfterReads) Complete(_ context.Context, messages []Message) (Completion, error) {
	model.seen = append([]Message(nil), messages...)
	return Completion{}, errors.New("recorded checkpoint")
}

func TestReadReferencesPreserveRawJournalAndRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	j := &memoryJournal{}
	tool := &repeatedReadTool{message: readMessage("", "file.go", strings.Repeat("a", 64), 1, 10, strings.Repeat("source\n", 100))}
	model := &scriptedModel{responses: []Completion{
		{ToolCalls: []ToolCall{{ID: "first", Name: "read_file", Arguments: json.RawMessage(`{"path":"file.go"}`)}}},
		{ToolCalls: []ToolCall{{ID: "second", Name: "read_file", Arguments: json.RawMessage(`{"path":"file.go"}`)}}},
	}}
	// Stop exactly after both reads, then reconstruct from the raw journal.
	runner := Runner{Journal: j, Model: model, Tools: tool, MaxTurns: 2}
	if _, err := runner.Run(ctx, "reads", testRequestDigest, "inspect"); !errors.Is(err, ErrTurnLimit) {
		t.Fatalf("checkpoint %v", err)
	}
	var outputs []json.RawMessage
	for _, entry := range j.entries {
		if entry.Kind == ToolDone {
			var r ToolResult
			if err := json.Unmarshal(entry.Payload, &r); err != nil {
				t.Fatal(err)
			}
			outputs = append(outputs, r.Output)
		}
	}
	if len(outputs) != 2 || string(outputs[0]) != tool.message.Content || string(outputs[1]) != tool.message.Content || tool.calls != 2 {
		t.Fatal("raw receipts or live reads lost")
	}
	paused := &pauseAfterReads{}
	runner.Model = paused
	runner.MaxTurns = 0
	if _, err := runner.Run(ctx, "reads", testRequestDigest, "inspect"); err == nil {
		t.Fatal("expected model stop")
	}
	if tool.calls != 2 || !strings.Contains(paused.seen[len(paused.seen)-1].Content, "IN_CONTEXT_REFERENCE") {
		t.Fatal("restart repeated reads or failed reference")
	}
}
