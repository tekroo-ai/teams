package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
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
	SourceDigest  string        `json:"source_digest"`
	History       []Message     `json:"history,omitempty"`
	Delta         *historyDelta `json:"delta,omitempty"`
	RequestSHA256 string        `json:"request_sha256,omitempty"`
}

type historyReplacement struct {
	Index   int                `json:"index"`
	Content string             `json:"content"`
	Receipt *RecordedResultRef `json:"recorded_result,omitempty"`
}

type historyDelta struct {
	Length       int                  `json:"length"`
	Replacements []historyReplacement `json:"replacements"`
	ResultDigest string               `json:"result_digest"`
}

func makeHistoryDelta(before, after []Message) (*historyDelta, error) {
	if !equalProtectedContext(before, after) {
		return nil, ErrInvalidTurn
	}
	d := &historyDelta{Length: len(before), ResultDigest: deltaHistoryDigest(after)}
	for i, m := range before {
		if reflect.DeepEqual(m, after[i]) {
			continue
		}
		if m.Role != "tool" || m.Receipt != nil && !reflect.DeepEqual(m.Receipt, after[i].Receipt) {
			return nil, ErrInvalidTurn
		}
		change := historyReplacement{Index: i, Content: after[i].Content}
		if m.Receipt == nil {
			change.Receipt = after[i].Receipt
		}
		d.Replacements = append(d.Replacements, change)
	}
	return d, nil
}

func applyHistoryDelta(before []Message, d *historyDelta) ([]Message, error) {
	if d == nil || d.Length != len(before) || !validForkDigest(d.ResultDigest) {
		return nil, ErrInvalidTurn
	}
	after := append([]Message(nil), before...)
	previous := -1
	for _, change := range d.Replacements {
		if change.Index <= previous || change.Index < 0 || change.Index >= len(after) || after[change.Index].Role != "tool" || change.Receipt != nil && after[change.Index].Receipt != nil {
			return nil, ErrInvalidTurn
		}
		previous = change.Index
		after[change.Index].Content = change.Content
		if change.Receipt != nil {
			ref := *change.Receipt
			after[change.Index].Receipt = &ref
		}
	}
	if deltaHistoryDigest(after) != d.ResultDigest || !validWorkingHistory(after) {
		return nil, ErrConflict
	}
	return after, nil
}

// An empty slice has no visible messages, whether its Go backing slice is nil
// or allocated. Normalize only the NEW delta format; legacy hashes are frozen.
func deltaHistoryDigest(history []Message) string {
	if len(history) == 0 {
		return historyDigest([]Message{})
	}
	return historyDigest(history)
}

type capacityCondenser interface {
	CondenseForCapacity(context.Context, []Message) ([]Message, error)
	BindingDigest() string
}

func deliveryDigest(condenser HistoryCondenser) string {
	if c, ok := condenser.(capacityCondenser); ok {
		return c.BindingDigest()
	}
	return ""
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
