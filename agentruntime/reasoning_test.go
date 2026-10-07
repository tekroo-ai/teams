package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func reasoningBudget(value int) *int { return &value }

func TestReasoningConfigurationValidation(t *testing.T) {
	for _, config := range []ReasoningConfig{{}, {EnableThinking: true, Effort: "low"}, {EnableThinking: true, Effort: "medium", BudgetTokens: reasoningBudget(-1)}, {EnableThinking: true, Effort: "high", BudgetTokens: reasoningBudget(1024)}, {EnableThinking: true, Effort: "xhigh"}} {
		if !config.Valid() {
			t.Fatalf("rejected valid configuration: %+v", config)
		}
	}
	for _, config := range []ReasoningConfig{{Effort: "medium"}, {BudgetTokens: reasoningBudget(-1)}, {EnableThinking: true}, {EnableThinking: true, Effort: "unknown"}, {EnableThinking: true, Effort: "medium", BudgetTokens: reasoningBudget(0)}, {EnableThinking: true, Effort: "medium", BudgetTokens: reasoningBudget(-2)}} {
		if config.Valid() {
			t.Fatalf("accepted contradictory/invalid configuration: %+v", config)
		}
	}
}

func TestOrdinaryStructuredTurnsHonorReasoning(t *testing.T) {
	for _, effort := range []string{"low", "medium"} {
		for _, mode := range []string{"actions", "final-repair", "checkpoint"} {
			t.Run(effort+"/"+mode, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						return
					}
					template, _ := body["chat_template_kwargs"].(map[string]any)
					if body["enable_thinking"] != true || body["reasoning_effort"] != effort || body["reasoning_budget_tokens"] != float64(-1) || template["enable_thinking"] != true || template["reasoning_effort"] != effort || body["continue_final_message"] != nil || body["max_tokens"] != float64(512) {
						t.Errorf("ordinary reasoning lost: %+v", body)
					}
					format, _ := body["response_format"].(map[string]any)
					if format["type"] != "json_schema" || body["tools"] != nil {
						t.Error("reasoning bypassed schema channel")
					}
					content := `{"ok":true}`
					if mode == "actions" {
						content = `{"submit_result":{"work_product":{"ok":true},"evidence":[]}}`
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"id": "thinking-turn", "choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": content, "reasoning_content": "Check the evidence before returning the result."}}}})
				}))
				defer server.Close()
				model := OpenAIModel{Client: server.Client(), BaseURL: server.URL + "/v1", Model: "fixture", MaxTokens: 512, Reasoning: &ReasoningConfig{EnableThinking: true, Effort: effort, BudgetTokens: reasoningBudget(-1)}}
				transcript := []Message{{Role: "user", Content: "Validate."}}
				switch mode {
				case "actions":
					model.TurnContract = structuredTestContract(t)
				case "checkpoint":
					model.ResponseSchema = json.RawMessage(`{"type":"object"}`)
				case "final-repair":
					model.FinalTool, model.FinalSchema = "submit_result", json.RawMessage(`{"type":"object"}`)
					transcript = append(transcript, Message{Role: "tool", Name: "submit_result", CallID: "rejected", Content: `"schema error"`})
				}
				if _, err := model.Complete(context.Background(), transcript); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestThinkingContinuationResumesWithoutReopeningReasoning(t *testing.T) {
	for _, failTransport := range []bool{false, true} {
		t.Run(fmt.Sprint(failTransport), func(t *testing.T) {
			parts := []string{`{"calls":[{"read_file":{"path":`, `"README.md"}}]}`, `{"submit_result":{"work_product":{"ok":true},"evidence":[]}}`}
			part, requests := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				var body struct {
					Messages []Message      `json:"messages"`
					Continue bool           `json:"continue_final_message"`
					Thinking bool           `json:"enable_thinking"`
					Effort   string         `json:"reasoning_effort"`
					Budget   *int           `json:"reasoning_budget_tokens"`
					Template map[string]any `json:"chat_template_kwargs"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				if part == 1 {
					if !body.Continue || body.Thinking || body.Template["enable_thinking"] != false || body.Effort != "" || body.Budget != nil || len(body.Template) != 1 || body.Messages[len(body.Messages)-1].Content != parts[0] {
						t.Errorf("invalid JSON suffix request: %+v", body)
					}
				} else if body.Continue || !body.Thinking || body.Template["enable_thinking"] != true || body.Effort != "medium" || body.Budget == nil || *body.Budget != -1 {
					t.Errorf("new ordinary turn did not restore reasoning: %+v", body)
				}
				if failTransport && requests == 2 {
					http.Error(w, "disconnected", 503)
					return
				}
				reason := "stop"
				if part == 0 {
					reason = "length"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"id": fmt.Sprintf("r%d", requests), "choices": []any{map[string]any{"finish_reason": reason, "message": map[string]any{"content": parts[part]}}}})
				part++
			}))
			defer server.Close()
			journal, tools := &memoryJournal{}, &countingTool{}
			model := OpenAIModel{Client: server.Client(), BaseURL: server.URL + "/v1", Model: "fixture", MaxTokens: 64, TurnContract: structuredTestContract(t), Reasoning: &ReasoningConfig{EnableThinking: true, Effort: "medium", BudgetTokens: reasoningBudget(-1)}}
			runner := Runner{Journal: journal, Tools: tools, Model: model, FinalTool: "submit_result", MaxTurns: 2, Finalize: func(raw json.RawMessage) (string, error) { return string(raw), nil }}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result, err := runner.Run(ctx, "thinking-continuation", testRequestDigest, "Read and report.")
			if failTransport {
				if err == nil || tools.calls != 0 || len(journal.entries) != 2 || journal.entries[1].Kind != ModelPartial {
					t.Fatalf("lost checkpoint or dispatched partial: %v %+v", err, journal.entries)
				}
				// Reconstruct the runner as a process restart; only durable state survives.
				runner.Model = model
				result, err = runner.Run(ctx, "thinking-continuation", testRequestDigest, "Read and report.")
			}
			if err != nil || !json.Valid([]byte(result)) || tools.calls != 1 || part != 3 {
				t.Fatalf("result=%s err=%v calls=%d parts=%d", result, err, tools.calls, part)
			}
			before := requests
			if _, err := runner.Run(ctx, "thinking-continuation", testRequestDigest, "Read and report."); err != nil || requests != before {
				t.Fatalf("completed replay regenerated: %v", err)
			}
		})
	}
}

func TestThinkingContinuationRejectsReasoningDriftBeforeHTTP(t *testing.T) {
	for _, change := range []string{"enabled", "effort", "budget"} {
		t.Run(change, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				_, _ = w.Write([]byte(`{"id":"partial","choices":[{"finish_reason":"length","message":{"content":"{\"submit_result\":"}}]}`))
			}))
			defer server.Close()
			model := OpenAIModel{Client: server.Client(), BaseURL: server.URL + "/v1", Model: "fixture", MaxTokens: 64, TurnContract: structuredTestContract(t), Reasoning: &ReasoningConfig{EnableThinking: true, Effort: "medium", BudgetTokens: reasoningBudget(-1)}}
			_, err := model.Complete(context.Background(), []Message{{Role: "user", Content: "Finish."}})
			var partial *PartialTurnError
			if !errors.As(err, &partial) {
				t.Fatalf("missing partial: %v", err)
			}
			switch change {
			case "enabled":
				model.Reasoning = &ReasoningConfig{}
			case "effort":
				model.Reasoning.Effort = "low"
			case "budget":
				model.Reasoning.BudgetTokens = reasoningBudget(1024)
			}
			if _, err := model.Continue(context.Background(), *partial); !errors.Is(err, ErrInvalidTurn) || requests != 1 {
				t.Fatalf("drift reached server: %v requests=%d", err, requests)
			}
		})
	}
}

func TestReasoningOnlyTokenCapIsNotAJSONCheckpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"thinking-only","choices":[{"finish_reason":"length","message":{"content":"","reasoning_content":"still reasoning"}}]}`))
	}))
	defer server.Close()
	model := OpenAIModel{Client: server.Client(), BaseURL: server.URL + "/v1", Model: "fixture", MaxTokens: 64, TurnContract: structuredTestContract(t), Reasoning: &ReasoningConfig{EnableThinking: true, Effort: "medium", BudgetTokens: reasoningBudget(-1)}}
	_, err := model.Complete(context.Background(), []Message{{Role: "user", Content: "Finish."}})
	var partial *PartialTurnError
	if !errors.Is(err, ErrReasoningBudgetExhausted) || errors.As(err, &partial) {
		t.Fatalf("fabricated resumable JSON prefix: %v", err)
	}
}
