package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFailureClassificationIsNotRetryableByDefault(t *testing.T) {
	for _, tc := range []struct {
		err   error
		class string
		retry bool
	}{
		{errors.New("unknown cause"), "EXECUTION_FAILURE", false},
		{errors.Join(ErrInvalidTurn, ErrReasoningBudgetExhausted), "REASONING_BUDGET_EXHAUSTED", false},
		{ErrInvalidTurn, "INVALID_MODEL_TURN", false},
		{ErrTurnLimit, "INVALID_MODEL_TURN", false},
		{ErrTransientModel, "TRANSIENT_MODEL_TRANSPORT", true},
		{context.Canceled, "EXECUTION_FAILURE", false},
	} {
		f := ClassifyFailure(tc.err)
		if f.Class != tc.class || f.Retryable != tc.retry {
			t.Fatalf("%v: %+v", tc.err, f)
		}
		raw, _ := json.Marshal(f)
		got, err := DecodeFailure(raw)
		if err != nil || got != f {
			t.Fatalf("failed round trip: %s %v", raw, err)
		}
	}
	legacy, err := DecodeFailure(json.RawMessage(`"structured continuation made no progress"`))
	if err != nil || legacy.Retryable || legacy.Class != "LEGACY_UNCLASSIFIED" {
		t.Fatalf("legacy classification: %+v %v", legacy, err)
	}
	for _, raw := range []string{`null`, `{}`, `{"reason":"x","class":"REASONING_BUDGET_EXHAUSTED","retryable":true}`, `{"reason":"x","class":"invented","retryable":true}`, `{"reason":"x","class":"EXECUTION_FAILURE","extra":1}`} {
		if _, err := DecodeFailure(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted invalid failure: %s", raw)
		}
	}
}

func TestReasoningOnlyUsageFailurePreservesRawReceiptAndHasNoContinuation(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"id":"r51-reasoning-only","usage":{"completion_tokens":8192,"completion_tokens_details":{"reasoning_tokens":8192}},"choices":[{"finish_reason":"length","message":{"content":""}}]}`))
	}))
	defer server.Close()
	model := OpenAIModel{Client: server.Client(), BaseURL: server.URL, Model: "fixture", MaxTokens: 8192, TurnContract: structuredTestContract(t)}
	_, err := model.Complete(context.Background(), []Message{{Role: "user", Content: "Report."}})
	var partial *PartialTurnError
	var receipt *ModelCompletionError
	if !errors.Is(err, ErrReasoningBudgetExhausted) || errors.As(err, &partial) || !errors.As(err, &receipt) || requests != 1 {
		t.Fatalf("classification/continuation: %v requests=%d", err, requests)
	}
	var usage struct {
		Completion int `json:"completion_tokens"`
	}
	if json.Unmarshal(receipt.Diagnostics.Usage, &usage) != nil || usage.Completion != 8192 || receipt.Diagnostics.Continuation || receipt.Diagnostics.ContentBytes != 0 || receipt.Diagnostics.ResponseID != "r51-reasoning-only" {
		t.Fatalf("receipt: %+v", receipt.Diagnostics)
	}
}

func TestHTTPFailuresOnlyRetryProvenTransientStatuses(t *testing.T) {
	for _, status := range []int{400, 401, 404, 429, 500, 501, 502, 503, 504} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
			defer server.Close()
			model := OpenAIModel{Client: server.Client(), BaseURL: server.URL, Model: "fixture", MaxTokens: 32}
			_, err := model.Complete(context.Background(), []Message{{Role: "user", Content: "Report."}})
			if ClassifyFailure(err).Retryable != (status == 429 || status == 502 || status == 503 || status == 504) {
				t.Fatalf("HTTP %d: %v", status, err)
			}
		})
	}
}
