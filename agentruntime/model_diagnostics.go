package agentruntime

import "encoding/json"

// ModelDiagnostics retains response metadata, not prompts, credentials or
// reasoning text. Absent usage stays absent rather than being reported as zero.
type ModelDiagnostics struct {
	ResponseID      string          `json:"response_id,omitempty"`
	ResponseSHA256  string          `json:"response_sha256"`
	FinishReason    string          `json:"finish_reason"`
	ContentBytes    int             `json:"content_bytes"`
	ReasoningBytes  int             `json:"reasoning_bytes"`
	Usage           json.RawMessage `json:"usage,omitempty"`
	Continuation    bool            `json:"continuation"`
	MaxOutputTokens int             `json:"max_output_tokens"`
}

type ModelCompletionError struct {
	Diagnostics ModelDiagnostics
	Cause       error
}

func (err *ModelCompletionError) Error() string { return err.Cause.Error() }
func (err *ModelCompletionError) Unwrap() error { return err.Cause }
