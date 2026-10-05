package agentruntime

import "context"

// HistoryCondenser may replace old working turns with a checkpoint. It never
// receives the pinned system instructions or original admitted task brief.
// The full uncondensed transcript remains in the durable turn journal.
type HistoryCondenser interface {
	Condense(context.Context, []Message) ([]Message, error)
}

func condenseHistory(ctx context.Context, messages []Message, condenser HistoryCondenser) ([]Message, error) {
	if condenser == nil {
		return messages, nil
	}
	if len(messages) == 0 || messages[0].Role != "system" && messages[0].Role != "user" {
		return nil, ErrInvalidTurn
	}
	pinned := 1
	if messages[0].Role == "system" {
		if len(messages) < 2 || messages[1].Role != "user" {
			return nil, ErrInvalidTurn
		}
		pinned = 2
	}
	history := make([]Message, len(messages)-pinned)
	for index, message := range messages[pinned:] {
		history[index] = message
		history[index].ToolCalls = append([]ToolCall(nil), message.ToolCalls...)
	}
	condensed, err := condenser.Condense(ctx, history)
	if err != nil {
		return nil, err
	}
	for _, message := range condensed {
		if message.Role == "system" || message.Role == "" {
			return nil, ErrInvalidTurn
		}
	}
	result := make([]Message, 0, pinned+len(condensed))
	result = append(result, messages[:pinned]...)
	result = append(result, condensed...)
	return result, nil
}
