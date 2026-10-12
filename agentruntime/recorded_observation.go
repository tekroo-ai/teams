package agentruntime

import "encoding/json"

// toolObservation and completedSubmissionObservation define the exact bytes
// used by replay, fork restoration and recorded-result reads. A Finished-backed
// observation is protocol closure, not a new ToolDone or an effect execution.
func toolObservation(entry Entry) (Message, error) {
	var result ToolResult
	if entry.Kind != ToolDone || entry.InvocationID == "" || entry.Sequence == 0 || json.Unmarshal(entry.Payload, &result) != nil || !json.Valid(result.Output) {
		return Message{}, ErrInvalidTurn
	}
	content := string(result.Output)
	if result.Error != "" {
		content = strJSON(result.Error)
	}
	return Message{Role: "tool", Name: result.Name, CallID: result.CallID, Content: content,
		Receipt: &RecordedResultRef{InvocationID: entry.InvocationID, Sequence: entry.Sequence, SHA256: resultDigest(content)}}, nil
}

func completedSubmissionObservation(terminal Entry, call ToolCall) (Message, error) {
	if terminal.Kind != Finished || terminal.InvocationID == "" || terminal.Sequence == 0 || call.ID == "" || call.Name == "" || !json.Valid(call.Arguments) {
		return Message{}, ErrInvalidTurn
	}
	content, err := json.Marshal(map[string]any{"historical_result": call.Arguments, "accepted": true, "executed_in_child": false})
	if err != nil {
		return Message{}, err
	}
	return Message{Role: "tool", Name: call.Name, CallID: call.ID, Content: string(content),
		Receipt: &RecordedResultRef{InvocationID: terminal.InvocationID, Sequence: terminal.Sequence, SHA256: resultDigest(string(content))}}, nil
}

// recordedObservation reads only supported source kinds. Finished is permitted
// only at the exact terminal of an already verified frozen ancestor; its entire
// recorded call/result chain is replayed without executing anything. A caller
// cannot reinterpret an arbitrary entry or the current invocation as completion.
func recordedObservation(entries []Entry, frozen *ForkSource, sequence uint64) (Message, error) {
	if sequence == 0 || sequence > uint64(len(entries)) || entries[sequence-1].Sequence != sequence {
		return Message{}, ErrInvalidTurn
	}
	entry := entries[sequence-1]
	if entry.Kind == ToolDone {
		return toolObservation(entry)
	}
	if entry.Kind != Finished || frozen == nil || frozen.terminal() != Finished || frozen.InvocationID != entry.InvocationID || frozen.ThroughSequence != sequence || sequence != uint64(len(entries)) {
		return Message{}, ErrInvalidTurn
	}
	var start startRecord
	var result string
	if entries[0].Kind != Started || json.Unmarshal(entries[0].Payload, &start) != nil || start.FinalTool == "" || json.Unmarshal(entry.Payload, &result) != nil {
		return Message{}, ErrInvalidTurn
	}
	messages, pending, complete, _, err := replay(entry.InvocationID, frozen.RequestDigest, start.Prompt, start.SystemPrompt, start.FinalTool,
		func(json.RawMessage) (string, error) { return result, nil }, start.EffectAuthority, entries)
	if err != nil || complete == nil || len(pending) != 0 || len(messages) == 0 {
		return Message{}, ErrInvalidTurn
	}
	last := messages[len(messages)-1]
	if last.Role != "assistant" || len(last.ToolCalls) != 1 || last.ToolCalls[0].Name != start.FinalTool {
		return Message{}, ErrInvalidTurn
	}
	return completedSubmissionObservation(entry, last.ToolCalls[0])
}
