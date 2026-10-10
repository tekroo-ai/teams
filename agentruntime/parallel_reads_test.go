package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

type concurrentReads struct {
	mu              sync.Mutex
	active, maximum int
	entered         chan string
	release         chan struct{}
	fail            string
	calls           []string
}

func (*concurrentReads) ParallelSafe(name string) bool { return name == "read_file" }

func (tools *concurrentReads) ExecuteReadOnly(ctx context.Context, call ToolCall) (json.RawMessage, error) {
	tools.mu.Lock()
	tools.active++
	tools.maximum = max(tools.maximum, tools.active)
	tools.calls = append(tools.calls, call.ID)
	tools.mu.Unlock()
	defer func() { tools.mu.Lock(); tools.active--; tools.mu.Unlock() }()
	if tools.entered != nil {
		tools.entered <- call.ID
	}
	if tools.release != nil {
		select {
		case <-tools.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if call.ID == tools.fail {
		return nil, errors.New("read failed")
	}
	return json.RawMessage(`{"value":"read"}`), nil
}

func TestParallelReadsOverlapBoundedAndJournalInCallOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	calls := []ToolCall{}
	for _, id := range []string{"one", "two", "three", "four", "five"} {
		calls = append(calls, ToolCall{ID: id, Name: "read_file", Arguments: json.RawMessage(`{"path":"source.go"}`)})
	}
	tools := &concurrentReads{entered: make(chan string, 5), release: make(chan struct{}), fail: "two"}
	model := &scriptedModel{responses: []Completion{{ToolCalls: calls}, {Text: "done"}}}
	journal := &memoryJournal{}
	runner := Runner{Journal: journal, Tools: tools, Model: model, MaxParallelReads: 2}
	done := make(chan error, 1)
	go func() { _, err := runner.Run(ctx, "parallel", testRequestDigest, "inspect"); done <- err }()
	// Both readers must start before either may return. This proves overlap
	// without inferring concurrency from short elapsed times.
	for range 2 {
		select {
		case <-tools.entered:
		case <-ctx.Done():
			t.Fatal("second reader did not overlap")
		}
	}
	close(tools.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if tools.maximum != 2 || tools.active != 0 {
		t.Fatalf("active=%d maximum=%d", tools.active, tools.maximum)
	}
	var ids []string
	for _, entry := range journal.entries {
		if entry.Kind != ToolDone {
			continue
		}
		var result ToolResult
		if err := json.Unmarshal(entry.Payload, &result); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, result.CallID)
		if result.CallID == "two" && (result.Error != "read failed" || string(result.Output) != "null") {
			t.Fatalf("error lost: %+v", result)
		}
	}
	if !reflect.DeepEqual(ids, []string{"one", "two", "three", "four", "five"}) {
		t.Fatalf("journal order=%v", ids)
	}
	if _, err := runner.Run(ctx, "parallel", testRequestDigest, "inspect"); err != nil || len(tools.calls) != 5 {
		t.Fatalf("replay repeated calls: %v %v", tools.calls, err)
	}
}

func TestParallelReadsRejectMixedBatchesAndDefaultRemainsSerial(t *testing.T) {
	tools := &concurrentReads{}
	runner := Runner{Tools: tools, MaxParallelReads: 4, FinalTool: "submit_result"}
	for _, calls := range [][]ToolCall{
		{{Name: "read_file"}, {Name: "edit_file"}},
		{{Name: "read_file"}, {Name: "run_go_tests"}},
		{{Name: "read_file"}, {Name: "git_status"}},
		{{Name: "read_file"}, {Name: "submit_result"}},
	} {
		if runner.parallelReadBatch(calls) {
			t.Fatalf("unsafe batch qualified: %+v", calls)
		}
	}
	runner.MaxParallelReads = 0
	if runner.parallelReadBatch([]ToolCall{{Name: "read_file"}, {Name: "read_file"}}) {
		t.Fatal("legacy caller opted in implicitly")
	}
}

func TestParallelReadsCancellationWaitsForEveryReader(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	tools := &concurrentReads{entered: make(chan string, 2), release: make(chan struct{})}
	runner := Runner{Tools: tools, MaxParallelReads: 2}
	done := make(chan []ToolResult, 1)
	go func() {
		done <- runner.executeParallelReads(ctx, []ToolCall{{ID: "one", Name: "read_file"}, {ID: "two", Name: "read_file"}})
	}()
	for range 2 {
		<-tools.entered
	}
	cancel()
	for _, result := range <-done {
		if result.Error != context.Canceled.Error() {
			t.Fatalf("cancellation lost: %+v", result)
		}
	}
	if tools.active != 0 {
		t.Fatal("returned with outstanding readers")
	}
}

type interruptedReadJournal struct {
	memoryJournal
	doneCount int
	fail      bool
}

func (journal *interruptedReadJournal) Append(ctx context.Context, id string, expected uint64, kind Kind, payload json.RawMessage) (Entry, error) {
	if kind == ToolDone {
		journal.doneCount++
		if journal.fail && journal.doneCount == 2 {
			return Entry{}, errors.New("interrupted journal append")
		}
	}
	return journal.memoryJournal.Append(ctx, id, expected, kind, payload)
}

func TestParallelReadsRestartPreservesRecordedResultsAndContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	calls := []ToolCall{
		{ID: "one", Name: "read_file", Arguments: json.RawMessage(`{"path":"one.go"}`)},
		{ID: "two", Name: "read_file", Arguments: json.RawMessage(`{"path":"two.go"}`)},
		{ID: "three", Name: "read_file", Arguments: json.RawMessage(`{"path":"three.go"}`)},
	}
	tools := &concurrentReads{}
	journal := &interruptedReadJournal{fail: true}
	model := &scriptedModel{responses: []Completion{{ToolCalls: calls}, {Text: "done"}}}
	runner := Runner{Journal: journal, Tools: tools, Model: model, MaxParallelReads: 4, SystemPrompt: "pinned instructions"}
	if _, err := runner.Run(ctx, "interrupted", testRequestDigest, "original decision context"); err == nil {
		t.Fatal("journal interruption hidden")
	}
	journal.fail = false
	if result, err := runner.Run(ctx, "interrupted", testRequestDigest, "original decision context"); err != nil || result != "done" {
		t.Fatalf("restart: %q %v", result, err)
	}
	counts := map[string]int{}
	for _, id := range tools.calls {
		counts[id]++
	}
	// Unrecorded reads may repeat after interruption; recorded reads must not.
	if !reflect.DeepEqual(counts, map[string]int{"one": 1, "two": 2, "three": 2}) {
		t.Fatalf("restart calls=%v", counts)
	}
	seen := model.seen[1]
	if seen[0].Content != "pinned instructions" || seen[1].Content != "original decision context" {
		t.Fatalf("restart lost context: %+v", seen)
	}
	var observed []string
	for _, message := range seen {
		if message.Role == "tool" {
			observed = append(observed, message.CallID)
		}
	}
	if !reflect.DeepEqual(observed, []string{"one", "two", "three"}) {
		t.Fatalf("restart duplicated or reordered observations: %v", observed)
	}
}
