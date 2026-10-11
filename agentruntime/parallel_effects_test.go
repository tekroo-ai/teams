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

type concurrentEffects struct {
	reads     concurrentReads
	mu        sync.Mutex
	receipts  map[string]json.RawMessage
	uncertain string
}

func (*concurrentEffects) Handles(name string) bool {
	return name == "run_go_tests" || name == "edit_file"
}
func (*concurrentEffects) ParallelEffectSafe(name string) bool { return name == "run_go_tests" }
func (effects *concurrentEffects) ReconcileEffect(_ context.Context, call ToolCall) (json.RawMessage, bool, error) {
	effects.mu.Lock()
	defer effects.mu.Unlock()
	result, found := effects.receipts[call.ID]
	return result, found, nil
}
func (effects *concurrentEffects) ExecuteEffect(ctx context.Context, call ToolCall) (json.RawMessage, error) {
	if output, found, _ := effects.ReconcileEffect(ctx, call); found {
		return output, nil
	}
	output, err := effects.reads.ExecuteReadOnly(ctx, call)
	if err != nil {
		return nil, err
	}
	effects.mu.Lock()
	defer effects.mu.Unlock()
	if effects.receipts == nil {
		effects.receipts = map[string]json.RawMessage{}
	}
	effects.receipts[call.ID] = output
	if call.ID == effects.uncertain {
		return nil, ErrEffectUncertain
	}
	return output, nil
}

func TestParallelEffectsBoundedOverlapRestartAndContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	calls := []ToolCall{{ID: "one", Name: "run_go_tests", Arguments: json.RawMessage(`{}`)}, {ID: "two", Name: "run_go_tests", Arguments: json.RawMessage(`{}`)}, {ID: "three", Name: "run_go_tests", Arguments: json.RawMessage(`{}`)}}
	effects := &concurrentEffects{reads: concurrentReads{entered: make(chan string, 3), release: make(chan struct{})}}
	journal := &interruptedReadJournal{fail: true}
	model := &scriptedModel{responses: []Completion{{ToolCalls: calls}, {Text: "done"}}}
	runner := Runner{Journal: journal, Tools: &concurrentReads{}, Effects: effects, Model: model, MaxParallelEffects: 2, SystemPrompt: "pinned instructions"}
	done := make(chan error, 1)
	go func() {
		_, err := runner.Run(ctx, "parallel-effects", testRequestDigest, "original context")
		done <- err
	}()
	for range 2 {
		select {
		case <-effects.reads.entered:
		case err := <-done:
			t.Fatalf("runner stopped before overlap: %v", err)
		case <-ctx.Done():
			t.Fatal("effects did not overlap")
		}
	}
	close(effects.reads.release)
	if err := <-done; err == nil {
		t.Fatal("journal interruption hidden")
	}
	journal.fail = false
	if result, err := runner.Run(ctx, "parallel-effects", testRequestDigest, "original context"); err != nil || result != "done" {
		t.Fatalf("%q %v", result, err)
	}
	if effects.reads.maximum != 2 || effects.reads.active != 0 || len(effects.reads.calls) != 3 {
		t.Fatalf("physical calls=%v maximum=%d active=%d", effects.reads.calls, effects.reads.maximum, effects.reads.active)
	}
	seen := model.seen[1]
	if seen[0].Content != "pinned instructions" || seen[1].Content != "original context" {
		t.Fatal("context lost")
	}
	var ids []string
	for _, message := range seen {
		if message.Role == "tool" {
			ids = append(ids, message.CallID)
		}
	}
	if !reflect.DeepEqual(ids, []string{"one", "two", "three"}) {
		t.Fatal(ids)
	}
}

func TestParallelEffectsOnlyQualifiedPrefixAndExplicitOptIn(t *testing.T) {
	runner := Runner{Effects: &concurrentEffects{}, MaxParallelEffects: 2, FinalTool: "submit_result"}
	for _, test := range []struct {
		names []string
		want  int
	}{
		{[]string{"run_go_tests", "run_go_tests", "edit_file", "run_go_tests"}, 2},
		{[]string{"run_go_tests", "submit_result", "run_go_tests"}, 1},
		{[]string{"read_file", "run_go_tests", "run_go_tests"}, 0},
		{[]string{"run_go_tests_worktree", "run_go_tests"}, 0},
	} {
		var calls []ToolCall
		for _, name := range test.names {
			calls = append(calls, ToolCall{Name: name})
		}
		if got := runner.parallelEffectPrefix(calls); got != test.want {
			t.Fatalf("%v got=%d", test.names, got)
		}
	}
	runner.MaxParallelEffects = 0
	if runner.parallelEffectPrefix([]ToolCall{{Name: "run_go_tests"}, {Name: "run_go_tests"}}) != 0 {
		t.Fatal("implicit opt-in")
	}
}

func TestParallelEffectsUncertainStopsAndCancellationJoinsWorkers(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	effects := &concurrentEffects{uncertain: "one"}
	runner := Runner{Effects: effects, MaxParallelEffects: 2}
	if _, err := runner.executeParallelTools(ctx, []ToolCall{{ID: "one", Name: "run_go_tests"}, {ID: "two", Name: "run_go_tests"}}, 2, effects.ExecuteEffect); !errors.Is(err, ErrEffectUncertain) {
		t.Fatal(err)
	}
	effects = &concurrentEffects{reads: concurrentReads{entered: make(chan string, 2), release: make(chan struct{})}}
	work, cancelWork := context.WithCancel(ctx)
	done := make(chan []ToolResult, 1)
	go func() {
		results, _ := runner.executeParallelTools(work, []ToolCall{{ID: "one", Name: "run_go_tests"}, {ID: "two", Name: "run_go_tests"}}, 2, effects.ExecuteEffect)
		done <- results
	}()
	for range 2 {
		select {
		case <-effects.reads.entered:
		case <-ctx.Done():
			t.Fatal("workers never entered")
		}
	}
	cancelWork()
	for _, result := range <-done {
		if result.Error != context.Canceled.Error() {
			t.Fatal(result)
		}
	}
	if effects.reads.active != 0 {
		t.Fatal("worker survived cancellation")
	}
}

func TestParallelEffectsOwnershipLossStartsNoPhysicalWork(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	effects := &concurrentEffects{}
	lost := errors.New("ownership lost")
	runner := Runner{Effects: effects, CheckOwnership: func(context.Context) error { return lost }}
	_, err := runner.executeParallelTools(ctx, []ToolCall{{ID: "one", Name: "run_go_tests"}, {ID: "two", Name: "run_go_tests"}}, 2, effects.ExecuteEffect)
	if !errors.Is(err, lost) || errors.Is(err, ErrEffectUncertain) || len(effects.reads.calls) != 0 {
		t.Fatalf("physical calls=%v err=%v", effects.reads.calls, err)
	}
}
