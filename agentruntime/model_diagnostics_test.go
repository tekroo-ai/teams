package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEmptyLengthRetainsRawUsageAndReasoningSizeNotText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"length-1","usage":{"prompt_tokens":42,"completion_tokens":512,"completion_tokens_details":{"reasoning_tokens":500}},"choices":[{"finish_reason":"length","message":{"content":"","reasoning_content":"private reasoning"}}]}`))
	}))
	defer server.Close()
	model := OpenAIModel{Client: server.Client(), BaseURL: server.URL + "/v1", Model: "fixture", MaxTokens: 512}
	journal := &memoryJournal{}
	runner := Runner{Journal: journal, Model: model, Tools: &countingTool{}, MaxTurns: 3}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	_, err := runner.Run(ctx, "diagnostics", testRequestDigest, "do work")
	var observed *ModelCompletionError
	if !errors.As(err, &observed) || !errors.Is(err, ErrInvalidTurn) {
		t.Fatalf("error chain: %v", err)
	}
	d := observed.Diagnostics
	if d.FinishReason != "length" || d.ContentBytes != 0 || d.ReasoningBytes != len("private reasoning") || d.MaxOutputTokens != 512 || len(d.ResponseSHA256) != 64 {
		t.Fatalf("diagnostics: %+v", d)
	}
	var usage map[string]any
	if json.Unmarshal(d.Usage, &usage) != nil || usage["completion_tokens"] != float64(512) {
		t.Fatalf("usage: %s", d.Usage)
	}
	if len(journal.entries) != 2 || journal.entries[1].Kind != ModelResponse || strings.Contains(string(journal.entries[1].Payload), "private reasoning") {
		t.Fatalf("journal lost metadata or retained text: %+v", journal.entries)
	}
}

func TestSuccessfulModelDiagnosticsSurviveJournalReplay(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"id":"success-1","usage":{"prompt_tokens":8,"completion_tokens":2},"choices":[{"finish_reason":"stop","message":{"content":"done","reasoning_content":"private"}}]}`))
	}))
	defer server.Close()
	model := OpenAIModel{Client: server.Client(), BaseURL: server.URL + "/v1", Model: "fixture", MaxTokens: 32}
	journal := &memoryJournal{}
	runner := Runner{Journal: journal, Model: model, Tools: &countingTool{}, MaxTurns: 1}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	result, err := runner.Run(ctx, "success", testRequestDigest, "do work")
	if err != nil || result != "done" {
		t.Fatalf("%q %v", result, err)
	}
	var turn Completion
	if len(journal.entries) < 2 || json.Unmarshal(journal.entries[1].Payload, &turn) != nil || turn.Diagnostics == nil || turn.Diagnostics.ResponseID != "success-1" || turn.Diagnostics.ContentBytes != 4 || turn.Diagnostics.ReasoningBytes != 7 || turn.Diagnostics.FinishReason != "stop" {
		t.Fatalf("metadata missing: %+v", journal.entries)
	}
	if strings.Contains(string(journal.entries[1].Payload), "private") {
		t.Fatal("reasoning text retained")
	}
	if result, err := runner.Run(ctx, "success", testRequestDigest, "do work"); err != nil || result != "done" || requests != 1 {
		t.Fatalf("replay regenerated: %q %v calls=%d", result, err, requests)
	}
}
