package agentruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRenderedRequestAdmissionCountsExactWireAndRetainsLargeDraft(t *testing.T) {
	for _, mode := range []string{"fit", "overflow", "missing", "wrong-digest", "wrong-model"} {
		t.Run(mode, func(t *testing.T) {
			posts, counts := 0, 0
			var counted []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				if r.URL.Path == "/v1/chat/token-count" {
					counts++
					counted = raw
					if mode == "missing" {
						http.NotFound(w, r)
						return
					}
					n := 60000
					if mode == "overflow" {
						n = 240000
					}
					digest := fmt.Sprintf("%x", sha256.Sum256(raw))
					if mode == "wrong-digest" {
						digest = strings.Repeat("0", 64)
					}
					name := "qualified"
					if mode == "wrong-model" {
						name = "other"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"model": name, "request_sha256": digest, "prompt_tokens": n, "context_tokens": 262144})
					return
				}
				posts++
				if string(raw) != string(counted) {
					t.Error("count and generation requests differ")
				}
				_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"done"}}]}`))
			}))
			defer server.Close()
			policy := DefaultContextDeliveryPolicy()
			policy.Admission = "rendered_tokens"
			policy.RequestBytes = 2 << 20
			policy.RepairReserveTokens = 32768
			model := OpenAIModel{Client: server.Client(), BaseURL: server.URL + "/v1", Model: "qualified", MaxTokens: 32768, ContextPolicy: &policy}
			transcript := []Message{{Role: "system", Content: strings.Repeat("authority ", 23000)}, {Role: "user", Content: "original task"}, {Role: "assistant", Content: "retained draft"}}
			result, err := model.Complete(context.Background(), transcript)
			if counts != 1 {
				t.Fatal("count not requested")
			}
			if mode == "fit" {
				if err != nil || posts != 1 || result.Diagnostics.Admission.PromptTokens != 60000 {
					t.Fatalf("fit rejected: %v %+v", err, result)
				}
			} else if err == nil || posts != 0 {
				t.Fatalf("failed count sent generation: %v posts=%d", err, posts)
			}
			if mode == "overflow" && !errors.Is(err, ErrContextBudget) {
				t.Fatal(err)
			}
		})
	}
}

// Deterministic qualification of the combined path. Token counts here are
// explicit provider fixtures; real-tokenizer qualification is a separate gate.
func TestCombinedLargeDraftLinkRejectionAndPointerCorrectionRetainsContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	policy := DefaultContextDeliveryPolicy()
	policy.Admission = "rendered_tokens"
	policy.RequestBytes = 2 << 20
	policy.RepairReserveTokens = 32768
	journal := &memoryJournal{}
	raw := largeReceipt(t)
	reader := RecordedResultReader{Journal: journal, InvocationID: "combined", Policy: policy}
	tools := &combinedReceiptTools{raw: raw, reader: reader}
	draft := "Original decision: preserve the original requirement. " + strings.Repeat("retained draft detail ", 10500)
	invalid, _ := json.Marshal(map[string]any{"validates": []int{0}, "skipped": "SKIP", "excluded": "NOT_RUN", "draft": draft})
	seed := &scriptedModel{responses: []Completion{
		{ToolCalls: []ToolCall{{ID: "source", Name: "read_file", Arguments: json.RawMessage(`{"path":"fixture"}`)}}},
		{Text: draft, ToolCalls: []ToolCall{{ID: "bad", Name: "submit_result", Arguments: invalid}}},
	}}
	finalize := func(raw json.RawMessage) (string, error) {
		var result struct {
			Validates         []int
			Skipped, Excluded string
		}
		if json.Unmarshal(raw, &result) != nil || len(result.Validates) != 0 {
			return "", errors.New("validates must be empty: host-owned validation links cannot be authored")
		}
		if result.Skipped != "SKIP" || result.Excluded != "NOT_RUN" {
			return "", errors.New("named validation evidence must not be promoted to PASS")
		}
		return string(raw), nil
	}
	runner := Runner{Journal: journal, Tools: tools, Model: seed, FinalTool: "submit_result", Finalize: finalize, MaxFinalSubmissions: 3, MaxTurns: 2, Condenser: ReceiptCondenser{Policy: policy}, SystemPrompt: "Original authority: retain decisions and distinguish unavailable evidence."}
	if _, err := runner.Run(ctx, "combined", testRequestDigest, "Original task"); !errors.Is(err, ErrTurnLimit) {
		t.Fatal(err)
	}
	before, _ := json.Marshal(journal.entries)
	seedEntries := len(journal.entries)
	var source ToolResult
	_ = json.Unmarshal(journal.entries[2].Payload, &source)
	ref := RecordedResultRef{"combined", 3, resultDigest(string(source.Output))}
	args := func(pointer, name string) json.RawMessage {
		m := map[string]any{"invocation_id": ref.InvocationID, "sequence": ref.Sequence, "sha256": ref.SHA256, "json_pointer": pointer}
		if name != "" {
			m["match_field"] = "name"
			m["match_value"] = name
		}
		b, _ := json.Marshal(m)
		return b
	}
	defs := []ToolDefinition{reader.Definition(), {Name: "submit_result", Parameters: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["validates","skipped","excluded"],"properties":{"validates":{"type":"array","maxItems":0,"items":{"type":"integer"}},"skipped":{"const":"SKIP"},"excluded":{"const":"NOT_RUN"}}}`)}}
	contract, err := NewStructuredTurnContract(defs, "submit_result")
	if err != nil {
		t.Fatal(err)
	}
	posts, counts := 0, 0
	var counted []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte(draft)) || !bytes.Contains(body, []byte("Original authority")) || !bytes.Contains(body, []byte("validates must be empty")) || bytes.Contains(body, []byte("TestCase2499")) {
			t.Error("retained decision/authority lost or full evidence re-inlined")
		}
		if len(body) <= 196608 {
			t.Error("fixture did not exceed historical transport cap")
		}
		if r.URL.Path == "/v1/chat/token-count" {
			counts++
			counted = body
			_ = json.NewEncoder(w).Encode(map[string]any{"model": "qualified", "request_sha256": fmt.Sprintf("%x", sha256.Sum256(body)), "prompt_tokens": 65000, "context_tokens": 262144})
			return
		}
		if !bytes.Equal(body, counted) {
			t.Error("generation differs from counted body")
		}
		posts++
		var content []byte
		switch posts {
		case 1:
			content, _ = json.Marshal(map[string]any{"calls": []any{map[string]any{"read_recorded_result": json.RawMessage(args("/read", ""))}}})
		case 2:
			if !bytes.Contains(body, []byte("not found")) || !bytes.Contains(body, []byte("output")) {
				t.Error("pointer correction evidence absent")
			}
			content, _ = json.Marshal(map[string]any{"calls": []any{map[string]any{"read_recorded_result": json.RawMessage(args("/output", ""))}}})
		case 3:
			content, _ = json.Marshal(map[string]any{"calls": []any{map[string]any{"read_recorded_result": json.RawMessage(args("/test_evidence/tests", "TestCase3"))}, map[string]any{"read_recorded_result": json.RawMessage(args("/test_evidence/excluded", "TestUnavailable"))}}})
		case 4:
			if !bytes.Contains(body, []byte("TestCase3")) || !bytes.Contains(body, []byte("TestUnavailable")) {
				t.Error("exact named evidence not read")
			}
			content, _ = json.Marshal(map[string]any{"submit_result": json.RawMessage(`{"validates":[],"skipped":"SKIP","excluded":"NOT_RUN"}`)})
		default:
			t.Error("unbounded recovery")
			http.Error(w, "too many", 500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": fmt.Sprintf("combined-%d", posts), "choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": string(content)}}}})
	}))
	defer server.Close()
	runner.Model = OpenAIModel{Client: server.Client(), BaseURL: server.URL + "/v1", Model: "qualified", MaxTokens: 32768, ContextPolicy: &policy, FinalTool: "submit_result", TurnContract: contract}
	runner.MaxTurns = 6
	result, err := runner.Run(ctx, "combined", testRequestDigest, "Original task")
	if err != nil || !strings.Contains(result, `"validates":[]`) || posts != 4 || counts != 4 || tools.sourceCalls != 1 {
		t.Fatalf("combined correction: result=%s err=%v posts=%d counts=%d source=%d", result, err, posts, counts, tools.sourceCalls)
	}
	after, _ := json.Marshal(journal.entries[:seedEntries])
	if !bytes.Equal(before, after) {
		t.Fatal("seeded raw draft or rejected result changed")
	}
	failures := 0
	for _, e := range journal.entries {
		if e.Kind == ToolDone {
			var done ToolResult
			_ = json.Unmarshal(e.Payload, &done)
			if done.Error != "" {
				failures++
			}
		}
	}
	if failures != 2 {
		t.Fatalf("missing durable rejection/error receipts: %d", failures)
	}
	if _, err := runner.Run(ctx, "combined", testRequestDigest, "Original task"); err != nil || posts != 4 || tools.sourceCalls != 1 {
		t.Fatal("completed replay re-executed work", err)
	}
}

type combinedReceiptTools struct {
	raw         json.RawMessage
	reader      RecordedResultReader
	sourceCalls int
}

func (tools *combinedReceiptTools) ExecuteReadOnly(ctx context.Context, call ToolCall) (json.RawMessage, error) {
	if call.Name == "read_recorded_result" {
		return tools.reader.Read(ctx, call.Arguments)
	}
	if call.Name != "read_file" {
		return nil, ErrInvalidTurn
	}
	tools.sourceCalls++
	return tools.raw, nil
}
