package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
)

func (runner Runner) parallelReadBatch(calls []ToolCall) bool {
	tools, ok := runner.Tools.(ParallelReadOnlyTools)
	if !ok || runner.MaxParallelReads < 2 || len(calls) < 2 {
		return false
	}
	for _, call := range calls {
		if call.Name == runner.FinalTool || runner.Effects != nil && runner.Effects.Handles(call.Name) || !tools.ParallelSafe(call.Name) {
			return false
		}
	}
	return true
}

// Workers write disjoint result slots. Journal writes remain serial and in call
// order, so existing replay, retained-context and compaction contracts hold.
// Await every worker before returning; deadlines are inherited, not detached.
func (runner Runner) executeParallelReads(ctx context.Context, calls []ToolCall) ([]ToolResult, error) {
	return runner.executeParallelTools(ctx, calls, min(runner.MaxParallelReads, 4), runner.Tools.ExecuteReadOnly)
}

func (runner Runner) parallelEffectPrefix(calls []ToolCall) int {
	effects, ok := runner.Effects.(ParallelEffects)
	if !ok || runner.MaxParallelEffects < 2 {
		return 0
	}
	for i, call := range calls {
		if call.Name == runner.FinalTool || !effects.Handles(call.Name) || !effects.ParallelEffectSafe(call.Name) {
			return i
		}
	}
	return len(calls)
}

// Physical effects retain their own reservation/completion ledger. If a batch
// loses an acknowledgement, join every worker and stop; replay consults those
// receipts rather than initiating a second physical execution.
func (runner Runner) executeParallelTools(ctx context.Context, calls []ToolCall, limit int, execute func(context.Context, ToolCall) (json.RawMessage, error)) ([]ToolResult, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make([]ToolResult, len(calls))
	jobs := make(chan int)
	var workers sync.WaitGroup
	var fatal error
	var fatalOnce sync.Once
	for range min(limit, len(calls)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				call := calls[index]
				result := ToolResult{CallID: call.ID, Name: call.Name, Output: json.RawMessage("null")}
				if err := ctx.Err(); err != nil {
					result.Error = err.Error()
				} else if err := runner.checkOwnership(ctx); err != nil {
					fatalOnce.Do(func() { fatal = err; cancel() })
					result.Error = err.Error()
				} else {
					output, err := execute(ctx, call)
					if errors.Is(err, ErrEffectUncertain) {
						fatalOnce.Do(func() { fatal = err; cancel() })
					}
					if err != nil {
						result.Error = err.Error()
					} else {
						result.Output = output
					}
				}
				results[index] = result
			}
		}()
	}
	for index := range calls {
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	return results, fatal
}
