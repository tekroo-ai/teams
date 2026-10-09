package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// ForkSource identifies an immutable, completed invocation. It is selected by
// Teams, never by a model-supplied conversation ID. The full original journal
// remains authoritative even when WorkingHistory contains a compaction.
type ForkSource struct {
	InvocationID    string `json:"invocation_id"`
	RequestDigest   string `json:"request_digest"`
	ThroughSequence uint64 `json:"through_sequence"`
	JournalSHA256   string `json:"journal_sha256"`
	// Empty means Finished for existing checkpoints. Other terminals are
	// admitted only through a coordinator-authorized retry, never consultation.
	TerminalKind Kind `json:"terminal_kind,omitempty"`
}

type ContextFork struct {
	Source         ForkSource `json:"source"`
	WorkingHistory []Message  `json:"working_history"`
	HistorySHA256  string     `json:"history_sha256"`
}

func journalDigest(entries []Entry) string {
	hash := sha256.New()
	for _, entry := range entries {
		// Frame metadata and retain payload bytes exactly. Marshaling a slice
		// of RawMessages would normalize whitespace inside the raw evidence.
		metadata, _ := json.Marshal(struct {
			InvocationID string
			Sequence     uint64
			Kind         Kind
			PayloadBytes int
		}{entry.InvocationID, entry.Sequence, entry.Kind, len(entry.Payload)})
		hash.Write(metadata)
		hash.Write([]byte{0})
		hash.Write(entry.Payload)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func validForkDigest(digest string) bool {
	decoded, err := hex.DecodeString(digest)
	return err == nil && len(decoded) == sha256.Size && digest == strings.ToLower(digest)
}

func (source ForkSource) Valid() bool {
	return strings.TrimSpace(source.InvocationID) != "" && source.ThroughSequence >= 2 && validForkDigest(source.RequestDigest) && validForkDigest(source.JournalSHA256) && validForkTerminal(source.terminal())
}

func (source ForkSource) terminal() Kind {
	if source.TerminalKind == "" {
		return Finished
	}
	return source.TerminalKind
}

func validForkTerminal(kind Kind) bool {
	return kind == Finished || kind == Failed || kind == Cancelled || kind == TimedOut
}

// CaptureForkSource freezes the complete recorded history, not a generated
// summary. A terminal success is required: pending effects cannot be forked.
func CaptureForkSource(ctx context.Context, journal Journal, id, requestDigest string) (ForkSource, error) {
	return captureForkSource(ctx, journal, id, requestDigest, Finished)
}

// CaptureRetryForkSource retains a terminal attempt for an already admitted
// successor. Callers must bind the exact retry lineage and terminal evidence.
// It does not authorize a retry or execute any historical tool.
func CaptureRetryForkSource(ctx context.Context, journal Journal, id, requestDigest string, terminal Kind) (ForkSource, error) {
	if !validForkTerminal(terminal) {
		return ForkSource{}, ErrInvalidTurn
	}
	return captureForkSource(ctx, journal, id, requestDigest, terminal)
}

func captureForkSource(ctx context.Context, journal Journal, id, requestDigest string, terminal Kind) (ForkSource, error) {
	if journal == nil || strings.TrimSpace(id) == "" || !validForkDigest(requestDigest) {
		return ForkSource{}, ErrInvalidTurn
	}
	entries, err := journal.Load(ctx, id)
	if err != nil {
		return ForkSource{}, err
	}
	if len(entries) < 2 || entries[len(entries)-1].Kind != terminal {
		return ForkSource{}, ErrInvalidTurn
	}
	var start startRecord
	if json.Unmarshal(entries[0].Payload, &start) != nil || start.RequestDigest != requestDigest {
		return ForkSource{}, ErrInvalidTurn
	}
	ref := ForkSource{InvocationID: id, RequestDigest: requestDigest, ThroughSequence: uint64(len(entries)), JournalSHA256: journalDigest(entries)}
	if terminal != Finished {
		ref.TerminalKind = terminal
	}
	if _, err := LoadForkEntries(ctx, journal, ref); err != nil {
		return ForkSource{}, err
	}
	return ref, nil
}

func LoadForkEntries(ctx context.Context, journal Journal, source ForkSource) ([]Entry, error) {
	if journal == nil || !source.Valid() {
		return nil, ErrInvalidTurn
	}
	entries, err := journal.Load(ctx, source.InvocationID)
	if err != nil {
		return nil, err
	}
	if uint64(len(entries)) != source.ThroughSequence || journalDigest(entries) != source.JournalSHA256 || entries[len(entries)-1].Kind != source.terminal() {
		return nil, ErrConflict
	}
	for i, entry := range entries {
		if entry.InvocationID != source.InvocationID || entry.Sequence != uint64(i+1) || !json.Valid(entry.Payload) {
			return nil, ErrInvalidTurn
		}
	}
	var start startRecord
	if entries[0].Kind != Started || json.Unmarshal(entries[0].Payload, &start) != nil || start.RequestDigest != source.RequestDigest {
		return nil, ErrInvalidTurn
	}
	return entries, nil
}

// PrepareContextFork reconstructs the parent's actual working context. Replay
// does not execute tools. The final historical submission receives a synthetic
// observation so it cannot become a pending tool in the child invocation.
func PrepareContextFork(ctx context.Context, journal Journal, childID string, source ForkSource) (*ContextFork, error) {
	if childID == "" || childID == source.InvocationID {
		return nil, ErrInvalidTurn
	}
	entries, err := LoadForkEntries(ctx, journal, source)
	if err != nil {
		return nil, err
	}
	var start startRecord
	var result string
	if json.Unmarshal(entries[0].Payload, &start) != nil {
		return nil, ErrInvalidTurn
	}
	terminal := source.terminal()
	replayEntries := entries
	if terminal == Finished {
		if json.Unmarshal(entries[len(entries)-1].Payload, &result) != nil {
			return nil, ErrInvalidTurn
		}
	} else {
		replayEntries = entries[:len(entries)-1]
	}
	messages, pending, completed, _, err := replay(source.InvocationID, source.RequestDigest, start.Prompt, start.SystemPrompt, start.FinalTool, func(json.RawMessage) (string, error) { return result, nil }, start.EffectAuthority, replayEntries)
	if err != nil || terminal == Finished && (completed == nil || len(pending) != 0) {
		return nil, ErrInvalidTurn
	}
	if len(messages) > 0 && messages[0].Role == "system" {
		// Preserve prior instructions as historical evidence, not as current
		// authority: the child's own authenticated instruction layers govern it.
		messages[0] = Message{Role: "user", Content: "Historical source instructions (not current authority):\n" + messages[0].Content}
	}
	if terminal == Finished && start.FinalTool != "" {
		last := messages[len(messages)-1]
		if len(last.ToolCalls) != 1 || last.ToolCalls[0].Name != start.FinalTool {
			return nil, ErrInvalidTurn
		}
		call := last.ToolCalls[0]
		observation, _ := json.Marshal(map[string]any{"historical_result": json.RawMessage(call.Arguments), "accepted": true, "executed_in_child": false})
		messages = append(messages, Message{Role: "tool", Name: call.Name, CallID: call.ID, Content: string(observation)})
	}
	if terminal != Finished {
		// A missing journal observation is not proof that an effect did or did
		// not run. Close historical tool syntax without executing or certifying it.
		for _, call := range pending {
			observation, _ := json.Marshal(map[string]any{"historical_call": true, "result_recorded": false, "executed_in_child": false, "instruction": "Consult original effect receipts before taking any new action; do not replay this call."})
			messages = append(messages, Message{Role: "tool", Name: call.Name, CallID: call.ID, Content: string(observation)})
		}
		messages = append(messages, Message{Role: "user", Content: "Historical invocation terminated with " + string(terminal) + "; this is not a successful result. Exact terminal receipt: " + string(entries[len(entries)-1].Payload)})
	}
	if !validWorkingHistory(messages) {
		return nil, ErrInvalidTurn
	}
	fork := &ContextFork{Source: source, WorkingHistory: messages, HistorySHA256: historyDigest(messages)}
	encoded, err := json.Marshal(fork)
	if err != nil || len(encoded) > MaxJournalPayloadBytes/2 {
		return nil, fmt.Errorf("%w: inherited working context is too large; original journal remains retained", ErrInvalidTurn)
	}
	return fork, nil
}

func validContextFork(fork *ContextFork, childID string) bool {
	return fork == nil || (fork.Source.Valid() && fork.Source.InvocationID != childID && len(fork.WorkingHistory) > 0 && historyDigest(fork.WorkingHistory) == fork.HistorySHA256 && validWorkingHistory(fork.WorkingHistory))
}
