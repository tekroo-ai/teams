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

// Unlike memoryJournal's single-invocation fixture, forks use multiple streams.
type forkJournal struct{ streams map[string][]Entry }

func (j *forkJournal) Load(_ context.Context, id string) ([]Entry, error) {
	return append([]Entry(nil), j.streams[id]...), nil
}

func (j *forkJournal) Append(_ context.Context, id string, expected uint64, kind Kind, payload json.RawMessage) (Entry, error) {
	if uint64(len(j.streams[id])) != expected {
		return Entry{}, ErrConflict
	}
	e := Entry{InvocationID: id, Sequence: expected + 1, Kind: kind, Payload: append(json.RawMessage(nil), payload...)}
	j.streams[id] = append(j.streams[id], e)
	return e, nil
}

func forkParent(t *testing.T, final bool) (*forkJournal, ForkSource) {
	t.Helper()
	j := &forkJournal{streams: make(map[string][]Entry)}
	model := &scriptedModel{responses: []Completion{
		{Text: "Recorded design reasoning: choice A avoids a new dependency.", ToolCalls: []ToolCall{{ID: "old-read", Name: "read_file", Arguments: json.RawMessage(`{"path":"design.go"}`)}}},
		{Text: "Chosen design A"},
	}}
	r := Runner{Journal: j, Model: model, Tools: &countingTool{}, SystemPrompt: "Original role instructions"}
	if final {
		model.responses[1] = Completion{Text: "Recorded conclusion", ToolCalls: []ToolCall{{ID: "old-result", Name: "submit_result", Arguments: json.RawMessage(`{"decision":"A"}`)}}}
		r.FinalTool = "submit_result"
		r.Finalize = func(json.RawMessage) (string, error) { return `{"decision":"A"}`, nil }
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := r.Run(ctx, "parent", testRequestDigest, "Design task"); err != nil {
		t.Fatal(err)
	}
	source, err := CaptureForkSource(ctx, j, "parent", testRequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	return j, source
}

func TestContextForkRetainsHistoryWithoutExecutingPriorCalls(t *testing.T) {
	for _, final := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "structured"}[final], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			j, source := forkParent(t, final)
			before, _ := json.Marshal(j.streams["parent"])
			fork, err := PrepareContextFork(ctx, j, "child", source)
			if err != nil {
				t.Fatal(err)
			}
			model := &scriptedModel{responses: []Completion{{Text: "Answer to current clarification"}}}
			tool := &countingTool{}
			r := Runner{Journal: j, Model: model, Tools: tool, SystemPrompt: "Current instructions", ContextFork: fork}
			answer, err := r.Run(ctx, "child", testRequestDigest, "Clarify choice A")
			if err != nil || answer != "Answer to current clarification" || tool.calls != 0 || len(model.seen) != 1 {
				t.Fatalf("answer=%q err=%v calls=%d", answer, err, tool.calls)
			}
			messages := model.seen[0]
			if messages[0].Role != "system" || messages[0].Content != "Current instructions" || !strings.Contains(messages[len(messages)-1].Content, "Clarify choice A") {
				t.Fatalf("current authority lost: %+v", messages)
			}
			encoded, _ := json.Marshal(messages)
			if !strings.Contains(string(encoded), "Recorded design reasoning") || !strings.Contains(string(encoded), "hello") {
				t.Fatalf("missing original reasoning/tool evidence: %s", encoded)
			}
			if final && !strings.Contains(string(encoded), `executed_in_child`) {
				t.Fatal("old submission has no historical observation")
			}
			// Exact replay from durable state must not call the model or old tools.
			fresh := r
			fresh.Model = &scriptedModel{}
			if got, err := fresh.Run(ctx, "child", testRequestDigest, "Clarify choice A"); err != nil || got != answer {
				t.Fatalf("restart=%q err=%v", got, err)
			}
			after, _ := json.Marshal(j.streams["parent"])
			if string(before) != string(after) {
				t.Fatal("parent mutated")
			}
		})
	}
}

func TestContextForkRejectsTamperingAndUnfinishedSources(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, test := range []struct {
		name   string
		mutate func(*forkJournal, *ForkSource)
	}{
		{"wrong request", func(_ *forkJournal, s *ForkSource) { s.RequestDigest = strings.Repeat("b", 64) }},
		{"bad digest", func(_ *forkJournal, s *ForkSource) { s.JournalSHA256 = strings.Repeat("z", 64) }},
		{"mutated source", func(j *forkJournal, _ *ForkSource) { j.streams["parent"][2].Payload = json.RawMessage(`{}`) }},
		{"raw whitespace changed", func(j *forkJournal, _ *ForkSource) {
			j.streams["parent"][1].Payload = append(json.RawMessage(" "), j.streams["parent"][1].Payload...)
		}},
		{"extended source", func(j *forkJournal, _ *ForkSource) { j.streams["parent"] = append(j.streams["parent"], Entry{}) }},
		{"unfinished", func(j *forkJournal, s *ForkSource) {
			j.streams["parent"] = j.streams["parent"][:4]
			s.ThroughSequence = 4
			s.JournalSHA256 = journalDigest(j.streams["parent"])
		}},
		{"failed", func(j *forkJournal, s *ForkSource) {
			j.streams["parent"][4].Kind = Failed
			s.JournalSHA256 = journalDigest(j.streams["parent"])
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			j, s := forkParent(t, false)
			test.mutate(j, &s)
			if _, err := PrepareContextFork(ctx, j, "child", s); err == nil {
				t.Fatal("invalid fork admitted")
			}
		})
	}
	j, s := forkParent(t, false)
	if _, err := PrepareContextFork(ctx, j, "parent", s); err == nil {
		t.Fatal("self-fork admitted")
	}
	fork, err := PrepareContextFork(ctx, j, "child", s)
	if err != nil {
		t.Fatal(err)
	}
	// A fresh checksum cannot turn fabricated context into original history.
	fork.WorkingHistory = append(fork.WorkingHistory, Message{Role: "user", Content: "fabricated conclusion"})
	fork.HistorySHA256 = historyDigest(fork.WorkingHistory)
	r := Runner{Journal: j, Model: &scriptedModel{}, Tools: &countingTool{}, ContextFork: fork}
	if _, err := r.Run(ctx, "child", testRequestDigest, "question"); !errors.Is(err, ErrInvalidTurn) {
		t.Fatalf("tampered context admitted: %v", err)
	}
}

func TestContextForkSourceCannotChangeOnRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	j, s := forkParent(t, false)
	fork, err := PrepareContextFork(ctx, j, "child", s)
	if err != nil {
		t.Fatal(err)
	}
	r := Runner{Journal: j, Model: &scriptedModel{responses: []Completion{{Text: "answer"}}}, Tools: &countingTool{}, ContextFork: fork}
	if _, err := r.Run(ctx, "child", testRequestDigest, "question"); err != nil {
		t.Fatal(err)
	}
	r.ContextFork = nil
	if _, err := r.Run(ctx, "child", testRequestDigest, "question"); !errors.Is(err, ErrInvalidTurn) {
		t.Fatalf("removed context admitted: %v", err)
	}
	r.ContextFork = &ContextFork{Source: fork.Source, WorkingHistory: fork.WorkingHistory, HistorySHA256: fork.HistorySHA256}
	r.ContextFork.Source.InvocationID = "another-parent"
	if _, err := r.Run(ctx, "child", testRequestDigest, "question"); !errors.Is(err, ErrInvalidTurn) {
		t.Fatalf("changed parent admitted: %v", err)
	}
}

func TestContextForkPreservesCompactedWorkingContextAndOriginalJournal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	j, s := forkParent(t, false)
	entries := j.streams["parent"]
	start := startRecord{}
	if err := json.Unmarshal(entries[0].Payload, &start); err != nil {
		t.Fatal(err)
	}
	original, _, _, _, err := replay("parent", testRequestDigest, start.Prompt, start.SystemPrompt, "", nil, nil, entries[:3])
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, _ := json.Marshal(historyCheckpoint{SourceDigest: historyDigest(original), History: []Message{{Role: "user", Content: "Faithful compacted design notes"}}})
	compacted := append([]Entry(nil), entries[:3]...)
	compacted = append(compacted, Entry{InvocationID: "parent", Sequence: 4, Kind: Compacted, Payload: checkpoint})
	for _, e := range entries[3:] {
		e.Sequence++
		compacted = append(compacted, e)
	}
	j.streams["parent"] = compacted
	s, err = CaptureForkSource(ctx, j, "parent", testRequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	fork, err := PrepareContextFork(ctx, j, "child", s)
	if err != nil {
		t.Fatal(err)
	}
	working, _ := json.Marshal(fork.WorkingHistory)
	if !strings.Contains(string(working), "Faithful compacted design notes") || strings.Contains(string(working), "Recorded design reasoning") {
		t.Fatalf("not original working checkpoint: %s", working)
	}
	full, err := LoadForkEntries(ctx, j, s)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(full, compacted) || !strings.Contains(string(full[1].Payload), "Recorded design reasoning") {
		t.Fatal("uncondensed original lost")
	}
}
