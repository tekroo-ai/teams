package agentruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// AdmissionReceipt binds counting to the exact request subsequently dispatched.
// Counting renders the provider's own template and never generates tokens.
type AdmissionReceipt struct {
	RequestSHA256 string `json:"request_sha256"`
	RequestBytes  int    `json:"request_bytes"`
	PromptTokens  int    `json:"prompt_tokens"`
	ContextTokens int    `json:"context_tokens"`
	OutputReserve int    `json:"output_reserve_tokens"`
	RepairReserve int    `json:"repair_reserve_tokens"`
}

func (model OpenAIModel) admitRendered(ctx context.Context, body []byte) (*AdmissionReceipt, error) {
	p := model.ContextPolicy
	if !p.Valid() || len(body) > p.RequestBytes {
		if p.Valid() && p.Representation == CatalogHistoryV1 && len(body) > p.RequestBytes {
			return nil, &CapacityLimitError{Layer: "transport_bytes", Actual: len(body), Limit: p.RequestBytes}
		}
		return nil, fmt.Errorf("%w: request byte limit exceeded; journal retained", ErrContextBudget)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(model.BaseURL, "/")+"/chat/token-count", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if model.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+model.APIKey)
	}
	response, err := model.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rendered request admission unavailable: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil {
		return nil, err
	}
	var count struct {
		Model   string `json:"model"`
		Digest  string `json:"request_sha256"`
		Prompt  *int   `json:"prompt_tokens"`
		Context *int   `json:"context_tokens"`
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(body))
	if response.StatusCode != http.StatusOK || len(raw) > 4096 || json.Unmarshal(raw, &count) != nil || count.Model != model.Model || count.Digest != digest || count.Prompt == nil || count.Context == nil {
		return nil, fmt.Errorf("%w: rendered request admission unavailable or count binding invalid (HTTP %d); no generation dispatched", ErrContextBudget, response.StatusCode)
	}
	if err := p.AdmitTokens(len(body), *count.Prompt, *count.Context, model.MaxTokens); err != nil {
		return nil, err
	}
	return &AdmissionReceipt{digest, len(body), *count.Prompt, *count.Context, model.MaxTokens, p.RepairReserveTokens}, nil
}
