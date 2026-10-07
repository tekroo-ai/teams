package agentruntime

import (
	"context"
	"encoding/hex"
	"fmt"
	"unicode/utf8"
)

// Bounds a single structured document, not an agent's lifetime or working turns.
const MaxStructuredResponseBytes = 1 << 20

// JSON escaping plus the pinned request must fit below MongoDB's document cap.
const MaxJournalPayloadBytes = 8 << 20

// PartialTurnError is a transport checkpoint, never an executable tool proposal.
// Messages retain the exact pre-generation context, including any condensation.
// Prefix is raw assistant content; it must not be parsed and reserialized.
type PartialTurnError struct {
	Prefix      string            `json:"prefix"`
	Binding     string            `json:"binding"`
	ResponseID  string            `json:"response_id"`
	Messages    []Message         `json:"messages"`
	Diagnostics *ModelDiagnostics `json:"diagnostics,omitempty"`
}

func (partial *PartialTurnError) Error() string {
	return fmt.Sprintf("structured response reached token cap; retained %d bytes", len(partial.Prefix))
}

func (partial *PartialTurnError) Unwrap() error { return ErrInvalidTurn }

func (partial *PartialTurnError) valid() bool {
	if partial == nil || partial.ResponseID == "" || len(partial.Prefix) == 0 || len(partial.Prefix) > MaxStructuredResponseBytes || !utf8.ValidString(partial.Prefix) || len(partial.Messages) == 0 {
		return false
	}
	digest, err := hex.DecodeString(partial.Binding)
	return err == nil && len(digest) == 32 && hex.EncodeToString(digest) == partial.Binding
}

// ContinuingModel resumes only a grammar-constrained document. The runner owns
// checkpoint persistence and will never dispatch partial calls.
type ContinuingModel interface {
	Continue(context.Context, PartialTurnError) (Completion, error)
}
