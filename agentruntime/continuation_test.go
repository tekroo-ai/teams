package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

type continuationCondenser struct{ calls int }

func (c *continuationCondenser) Condense(_ context.Context, history []Message) ([]Message, error) {
	c.calls++
	return history, nil
}

func TestStructuredContinuationPreservesExactPrefixAndCheckpoints(t *testing.T) {
	for _, failTransport := range []bool{false, true} {
		t.Run(fmt.Sprint(failTransport), func(t *testing.T) {
			contract := structuredTestContract(t)
			parts := []string{`{"submit_result": {"work_product":`, ` {"ok":true}, "evidence":["checked`, `"]}}`}
			requests, part := 0, 0
			var original []Message
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				var body struct {
					Messages []Message       `json:"messages"`
					Continue bool            `json:"continue_final_message"`
					Tools    json.RawMessage `json:"tools"`
					Thinking bool            `json:"enable_thinking"`
					Template map[string]bool `json:"chat_template_kwargs"`
					Format   struct {
						Schema struct {
							Schema json.RawMessage `json:"schema"`
						} `json:"json_schema"`
					} `json:"response_format"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if len(body.Tools) != 0 || body.Thinking || body.Template["enable_thinking"] || body.Template == nil || string(body.Format.Schema.Schema) != string(contract.schema) {
					t.Errorf("continuation changed grammar/tools/thinking: %+v", body)
				}
				if part == 0 {
					original = body.Messages
					if body.Continue {
						t.Error("initial request marked as continuation")
					}
				} else {
					last := body.Messages[len(body.Messages)-1]
					if !body.Continue || last.Role != "assistant" || last.Content != strings.Join(parts[:part], "") || !reflect.DeepEqual(body.Messages[:len(body.Messages)-1], original) {
						t.Errorf("partial prefix or original context changed: %+v", body.Messages)
					}
				}
				if failTransport && requests == 2 {
					http.Error(w, "transport unavailable", 503)
					return
				}
				reason := "length"
				if part == 2 {
					reason = "stop"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"id": fmt.Sprintf("part-%d", part), "choices": []any{map[string]any{"finish_reason": reason, "message": map[string]any{"content": parts[part]}}}})
				part++
			}))
			defer server.Close()
			journal, tools := &memoryJournal{}, &countingTool{}
			condenser := &continuationCondenser{}
			runner := Runner{Journal: journal, Tools: tools, FinalTool: "submit_result", MaxTurns: 1, Condenser: condenser,
				Model:    OpenAIModel{Client: server.Client(), BaseURL: server.URL + "/v1", Model: "local", MaxTokens: 8, TurnContract: contract},
				Finalize: func(raw json.RawMessage) (string, error) { return string(raw), nil }}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result, err := runner.Run(ctx, "partial", testRequestDigest, "Finish this task")
			if failTransport {
				if err == nil || len(journal.entries) != 2 || journal.entries[1].Kind != "model_partial" {
					t.Fatalf("partial lost on failure: %v, %+v", err, journal.entries)
				}
				result, err = runner.Run(ctx, "partial", testRequestDigest, "Finish this task")
			}
			if err != nil || !json.Valid([]byte(result)) || part != 3 || tools.calls != 0 {
				t.Fatalf("result=%s err=%v parts=%d tools=%d", result, err, part, tools.calls)
			}
			if len(journal.entries) != 5 || journal.entries[1].Kind != "model_partial" || journal.entries[2].Kind != "model_partial" {
				t.Fatalf("missing partial checkpoints: %+v", journal.entries)
			}
			if condenser.calls != 1 {
				t.Fatalf("continuation re-condensed original context %d times", condenser.calls)
			}
			before := requests
			if _, err := runner.Run(ctx, "partial", testRequestDigest, "Finish this task"); err != nil || requests != before {
				t.Fatalf("replay repeated generation: %v", err)
			}
		})
	}
}

func TestStructuredContinuationFailsClosed(t *testing.T) {
	for _, failure := range []string{"no-progress", "overflow", "native-tools", "model-drift", "schema-drift", "cancelled"} {
		t.Run(failure, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				content, reason := `{"submit_result":`, "length"
				message := map[string]any{"content": content}
				if requests > 1 {
					switch failure {
					case "no-progress":
						message["content"] = ""
					case "overflow":
						message["content"] = strings.Repeat("x", 1<<20)
					case "native-tools":
						message["tool_calls"] = []any{map[string]any{"id": "c", "function": map[string]any{"name": "read_file", "arguments": "{}"}}}
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "r", "choices": []any{map[string]any{"finish_reason": reason, "message": message}}})
			}))
			defer server.Close()
			model := OpenAIModel{Client: server.Client(), BaseURL: server.URL + "/v1", Model: "local", MaxTokens: 8, TurnContract: structuredTestContract(t)}
			_, err := model.Complete(context.Background(), []Message{{Role: "user", Content: "finish"}})
			var partial *PartialTurnError
			if !errors.As(err, &partial) {
				t.Fatalf("expected resumable partial: %v", err)
			}
			ctx := context.Background()
			if failure == "model-drift" {
				model.Model = "changed"
			}
			if failure == "schema-drift" {
				model.TurnContract.schema = json.RawMessage(`{"type":"object"}`)
			}
			if failure == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, err = model.Continue(ctx, *partial)
			if err == nil {
				t.Fatal("invalid continuation accepted")
			}
			if (failure == "schema-drift" || failure == "model-drift") && requests != 1 {
				t.Fatal("changed binding reached server")
			}
		})
	}
}

func TestStructuredContinuationDispatchesOnlyWholeValidatedCalls(t *testing.T) {
	requests := 0
	tools := &countingTool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests <= 2 && tools.calls != 0 {
			t.Error("partial document dispatched a tool")
		}
		parts := []string{`{"calls":[{"read_file":{"path":"README.md"}}`, `]}`, `{"submit_result":{"work_product":{"ok":true},"evidence":[]}}`}
		reason := "stop"
		if requests == 1 {
			reason = "length"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": fmt.Sprintf("r%d", requests), "choices": []any{map[string]any{"finish_reason": reason, "message": map[string]any{"content": parts[requests-1]}}}})
	}))
	defer server.Close()
	journal := &memoryJournal{}
	runner := Runner{Journal: journal, Tools: tools, FinalTool: "submit_result", MaxTurns: 2,
		Model:    OpenAIModel{Client: server.Client(), BaseURL: server.URL + "/v1", Model: "local", MaxTokens: 8, TurnContract: structuredTestContract(t)},
		Finalize: func(raw json.RawMessage) (string, error) { return string(raw), nil }}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := runner.Run(ctx, "calls", testRequestDigest, "Read and finish"); err != nil || tools.calls != 1 || requests != 3 {
		t.Fatalf("err=%v tools=%d requests=%d", err, tools.calls, requests)
	}
	if _, err := runner.Run(ctx, "calls", testRequestDigest, "Read and finish"); err != nil || tools.calls != 1 || requests != 3 {
		t.Fatalf("replay err=%v tools=%d requests=%d", err, tools.calls, requests)
	}
}

// Explicitly opt in to the deployed endpoint. No repository or organizational
// work is performed; the schema fixes the entire disposable receipt value.
func TestLiveStructuredContinuation(t *testing.T) {
	base, model := os.Getenv("TEKROO_CONTINUATION_BASE_URL"), os.Getenv("TEKROO_CONTINUATION_MODEL")
	if base == "" || model == "" {
		t.Skip("requires explicit continuation endpoint and model")
	}
	value := "prefix-" + strings.Repeat("abcdef", 32)
	schema, _ := json.Marshal(map[string]any{"type": "object", "additionalProperties": false, "required": []string{"receipt"}, "properties": map[string]any{"receipt": map[string]any{"type": "string", "const": value}}})
	contract, err := NewStructuredTurnContract([]ToolDefinition{{Name: "submit_result", Parameters: schema}}, "submit_result")
	if err != nil {
		t.Fatal(err)
	}
	journal, tools := &memoryJournal{}, &countingTool{}
	runner := Runner{Journal: journal, Tools: tools, FinalTool: "submit_result", MaxTurns: 1,
		Model:    OpenAIModel{Client: &http.Client{Timeout: time.Minute}, BaseURL: base, Model: model, MaxTokens: 8, TurnContract: contract},
		Finalize: func(raw json.RawMessage) (string, error) { return string(raw), nil }}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	result, err := runner.Run(ctx, "live-continuation", testRequestDigest, "Return the required receipt object.")
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		Receipt string `json:"receipt"`
	}
	if json.Unmarshal([]byte(result), &receipt) != nil || receipt.Receipt != value || tools.calls != 0 {
		t.Fatalf("incorrect assembled receipt: %s", result)
	}
	partials := 0
	for _, entry := range journal.entries {
		if entry.Kind == ModelPartial {
			partials++
		}
		if entry.Kind == ModelRejected || entry.Kind == ToolDone {
			t.Fatalf("unexpected rejection or tool correction: %+v", entry)
		}
	}
	if partials < 2 {
		t.Fatalf("did not exercise repeated caps: %d", partials)
	}
	before := len(journal.entries)
	if _, err := runner.Run(ctx, "live-continuation", testRequestDigest, "Return the required receipt object."); err != nil || len(journal.entries) != before {
		t.Fatalf("completed replay: %v", err)
	}
	t.Logf("PASS: %d durable partial checkpoints, exact assembled receipt, no rejected turns/tools/corrections", partials)
}
