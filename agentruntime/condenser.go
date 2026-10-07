package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

// HistoryCondenser may replace old working turns with a checkpoint. It never
// receives the pinned system instructions or original admitted task brief.
// The full uncondensed transcript remains in the durable turn journal.
type HistoryCondenser interface {
	Condense(context.Context, []Message) ([]Message, error)
}

// ContextCondenser measures the complete request but may replace only history.
type ContextCondenser interface {
	CondenseContext(context.Context, []Message, []Message) ([]Message, error)
}

type historyCheckpoint struct {
	SourceDigest string    `json:"source_digest"`
	History      []Message `json:"history"`
}

func historyDigest(messages []Message) string {
	encoded, _ := json.Marshal(messages)
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}

func validWorkingHistory(history []Message) bool {
	var pending []ToolCall
	for _, message := range history {
		switch message.Role {
		case "user":
			if len(pending) != 0 || len(message.ToolCalls) != 0 {
				return false
			}
		case "assistant":
			if len(pending) != 0 {
				return false
			}
			pending = append(pending, message.ToolCalls...)
		case "tool":
			if len(pending) == 0 || message.CallID != pending[0].ID || message.Name != pending[0].Name {
				return false
			}
			pending = pending[1:]
		default:
			return false
		}
	}
	return len(pending) == 0
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
	var condensed []Message
	var err error
	if aware, ok := condenser.(ContextCondenser); ok {
		condensed, err = aware.CondenseContext(ctx, append([]Message(nil), messages[:pinned]...), history)
	} else {
		condensed, err = condenser.Condense(ctx, history)
	}
	if err != nil {
		return nil, err
	}
	if !validWorkingHistory(condensed) {
		return nil, ErrInvalidTurn
	}
	result := make([]Message, 0, pinned+len(condensed))
	result = append(result, messages[:pinned]...)
	result = append(result, condensed...)
	return result, nil
}
