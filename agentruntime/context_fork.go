package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	WorkingHistory []Message  `json:"working_history,omitempty"`
	HistorySHA256  string     `json:"history_sha256"`
	Representation string     `json:"representation,omitempty"`
}

const frozenHistoryV1 = "frozen_history_v1"

func storedContextFork(fork *ContextFork, compact bool) *ContextFork {
	if fork == nil || !compact {
		return fork
	}
	return &ContextFork{Source: fork.Source, HistorySHA256: fork.HistorySHA256, Representation: frozenHistoryV1}
}

func validStoredContextFork(fork *ContextFork, childID, delivery string) bool {
	if fork != nil && fork.Representation == frozenHistoryV1 {
		return delivery != "" && fork.Source.Valid() && fork.Source.InvocationID != childID && len(fork.WorkingHistory) == 0 && validForkDigest(fork.HistorySHA256)
	}
	return validContextFork(fork, childID)
}

func matchesStoredFork(stored, full *ContextFork) bool {
	return stored != nil && full != nil && stored.Representation == frozenHistoryV1 && len(stored.WorkingHistory) == 0 && stored.Source == full.Source && stored.HistorySHA256 == full.HistorySHA256 && full.Representation == "" && historyDigest(full.WorkingHistory) == stored.HistorySHA256
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
	return prepareContextFork(ctx, journal, childID, source, false, 0)
}

// Catalog delivery persists frozen roots rather than copying their transcript.
// Legacy callers retain their historical storage-size guard.
func PrepareContextForkWithPolicy(ctx context.Context, journal Journal, childID string, source ForkSource, policy ContextDeliveryPolicy) (*ContextFork, error) {
	if !policy.Valid() {
		return nil, ErrInvalidTurn
	}
	return prepareContextFork(ctx, journal, childID, source, policy.Representation == CatalogHistoryV1, 0)
}

func prepareContextFork(ctx context.Context, journal Journal, childID string, source ForkSource, compact bool, depth int) (*ContextFork, error) {
	if depth >= 64 {
		return nil, ErrInvalidTurn
	}
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
	// Validate the full bounded chain before recursive restoration. Source
	// hashes remain over the ORIGINAL stored bytes, never an expanded copy.
	if _, err := ReceiptSources(ctx, journal, &source); err != nil {
		return nil, err
	}
	var restored *ContextFork
	if start.ContextFork != nil && start.ContextFork.Representation == frozenHistoryV1 {
		restored, err = prepareContextFork(ctx, journal, source.InvocationID, start.ContextFork.Source, true, depth+1)
		if err != nil || !matchesStoredFork(start.ContextFork, restored) {
			return nil, errors.Join(ErrConflict, err)
		}
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
	messages, pending, completed, _, err := replay(source.InvocationID, source.RequestDigest, start.Prompt, start.SystemPrompt, start.FinalTool, func(json.RawMessage) (string, error) { return result, nil }, start.EffectAuthority, replayEntries, restored)
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
		observation, err := completedSubmissionObservation(entries[len(entries)-1], last.ToolCalls[0])
		if err != nil {
			return nil, err
		}
		messages = append(messages, observation)
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
	// Legacy nested checkpoints predate receipt references. Bind their exact
	// unchanged tool bytes to verified ancestry; do not rewrite decisions or
	// manufacture references for unrecorded observations. Successful protocol
	// closure is derivable from its frozen Finished entry, not a fake ToolDone.
	sources, err := ReceiptSources(ctx, journal, &source)
	if err != nil {
		return nil, err
	}
	missing := false
	for _, m := range messages {
		missing = missing || m.Role == "tool" && m.Receipt == nil
	}
	var originals []Message
	for _, ancestor := range sources {
		if !missing {
			break
		}
		original, err := LoadForkEntries(ctx, journal, ancestor)
		if err != nil {
			return nil, err
		}
		for _, entry := range original {
			if entry.Kind != ToolDone && entry.Kind != Finished {
				continue
			}
			if entry.Kind == Finished {
				var start startRecord
				if json.Unmarshal(original[0].Payload, &start) != nil {
					return nil, ErrInvalidTurn
				}
				if start.FinalTool == "" { // Text completion needs no tool closure.
					continue
				}
			}
			observation, err := recordedObservation(original, &ancestor, entry.Sequence)
			if err != nil {
				return nil, err
			}
			originals = append(originals, observation)
		}
	}
	if missing {
		messages = restoreRecordedReferences(messages, originals)
	}
	fork := &ContextFork{Source: source, WorkingHistory: messages, HistorySHA256: historyDigest(messages)}
	encoded, err := json.Marshal(storedContextFork(fork, compact))
	if err != nil || len(encoded) > MaxJournalPayloadBytes/2 {
		return nil, fmt.Errorf("%w: inherited working context is too large; original journal remains retained", ErrInvalidTurn)
	}
	return fork, nil
}

func validContextFork(fork *ContextFork, childID string) bool {
	return fork == nil || (fork.Representation == "" && fork.Source.Valid() && fork.Source.InvocationID != childID && len(fork.WorkingHistory) > 0 && historyDigest(fork.WorkingHistory) == fork.HistorySHA256 && validWorkingHistory(fork.WorkingHistory))
}
