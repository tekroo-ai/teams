package agentruntime

import (
	"context"
	"encoding/json"
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
func (runner Runner) executeParallelReads(ctx context.Context, calls []ToolCall) []ToolResult {
	results := make([]ToolResult, len(calls))
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range min(runner.MaxParallelReads, 4, len(calls)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				call := calls[index]
				result := ToolResult{CallID: call.ID, Name: call.Name, Output: json.RawMessage("null")}
				if err := ctx.Err(); err != nil {
					result.Error = err.Error()
				} else {
					output, err := runner.Tools.ExecuteReadOnly(ctx, call)
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
	return results
}
