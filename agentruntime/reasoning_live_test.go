package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// An explicit opt-in proves the deployed provider actually honors reasoning
// with a schema and can resume its JSON suffix. No tools or team state execute.
func TestLiveThinkingStructuredContinuation(t *testing.T) {
	base, name, output := os.Getenv("TEKROO_REASONING_PROBE_BASE_URL"), os.Getenv("TEKROO_REASONING_PROBE_MODEL"), os.Getenv("TEKROO_REASONING_PROBE_OUTPUT")
	if base == "" {
		t.Skip("requires explicit live reasoning probe")
	}
	if name == "" || !filepath.IsAbs(output) {
		t.Fatal("exact model and absolute output path required")
	}
	if err := os.Mkdir(output, 0700); err != nil {
		t.Fatal(err)
	}
	rawPath := filepath.Join(output, "raw-http.jsonl")
	client := &http.Client{Timeout: 3 * time.Minute, Transport: &generationProbeTransport{path: rawPath}}
	passed, partials := 0, 0
	defer func() {
		status := "PASS"
		if t.Failed() {
			status = "FAIL"
		}
		receipt, _ := json.Marshal(map[string]any{"result": status, "model": name, "base_url": base, "completed_cases": passed, "partial_checkpoints": partials, "tool_execution": "NOT_RUN", "production_changes": "NOT_RUN"})
		if err := os.WriteFile(filepath.Join(output, "receipt.json"), receipt, 0600); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Minute)
	defer cancel()
	for _, effort := range []string{"low", "medium"} {
		if os.Getenv("TEKROO_REASONING_PROBE_CASE") == "continuation" {
			break
		}
		contract, err := NewStructuredTurnContract([]ToolDefinition{{Name: "submit_result", Parameters: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["answer"],"properties":{"answer":{"type":"integer","const":595}}}`)}}, "submit_result")
		if err != nil {
			t.Fatal(err)
		}
		model := OpenAIModel{Client: client, BaseURL: base, Model: name, MaxTokens: 2048, TurnContract: contract, Reasoning: &ReasoningConfig{EnableThinking: true, Effort: effort, BudgetTokens: reasoningBudget(-1)}}
		completion, err := model.Complete(ctx, []Message{{Role: "system", Content: StructuredTurnInstruction}, {Role: "user", Content: "Compute 35 times 17 and submit the answer."}})
		if err != nil || len(completion.ToolCalls) != 1 || completion.ToolCalls[0].Name != "submit_result" {
			t.Fatalf("%s completion=%+v err=%v", effort, completion, err)
		}
		passed++
	}
	// An ordered sequence requires a long answer but only a short explanation;
	// repeated strings trigger loop detection, random strings invite long copying
	// during reasoning. Neither measures the JSON-suffix transport we are testing.
	value := make([]int, 1024)
	for index := range value {
		value[index] = index
	}
	schema, _ := json.Marshal(map[string]any{"type": "object", "additionalProperties": false, "required": []string{"receipt"}, "properties": map[string]any{"receipt": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "const": value}}})
	contract, err := NewStructuredTurnContract([]ToolDefinition{{Name: "submit_result", Parameters: schema}}, "submit_result")
	if err != nil {
		t.Fatal(err)
	}
	model := OpenAIModel{Client: client, BaseURL: base, Model: name, MaxTokens: 1024, TurnContract: contract, Reasoning: &ReasoningConfig{EnableThinking: true, Effort: "medium", BudgetTokens: reasoningBudget(-1)}}
	completion, err := model.Complete(ctx, []Message{{Role: "system", Content: StructuredTurnInstruction}, {Role: "user", Content: "Submit receipt as the array of consecutive integers from 0 through 1023, inclusive. No tools need execution."}})
	for err != nil {
		var partial *PartialTurnError
		if !errors.As(err, &partial) {
			t.Fatal(err)
		}
		partials++
		completion, err = model.Continue(ctx, *partial)
	}
	var result struct {
		Receipt []int `json:"receipt"`
	}
	if partials == 0 || len(completion.ToolCalls) != 1 || completion.ToolCalls[0].Name != "submit_result" || json.Unmarshal(completion.ToolCalls[0].Arguments, &result) != nil || !slices.Equal(result.Receipt, value) {
		t.Fatalf("invalid continuation: partials=%d result=%+v", partials, completion)
	}
	passed++
	raw, err := os.ReadFile(rawPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var record struct {
			Request struct {
				Thinking bool `json:"enable_thinking"`
				Continue bool `json:"continue_final_message"`
			} `json:"request"`
			Response struct {
				Model string `json:"model"`
				Usage struct {
					Details struct {
						Reasoning int `json:"reasoning_tokens"`
					} `json:"completion_tokens_details"`
				} `json:"usage"`
			} `json:"response"`
		}
		if json.Unmarshal([]byte(line), &record) != nil {
			t.Fatal("invalid retained provider record")
		}
		if record.Response.Model != name {
			t.Fatalf("provider model drift: %q", record.Response.Model)
		}
		if record.Request.Continue {
			if record.Request.Thinking || record.Response.Usage.Details.Reasoning != 0 {
				t.Fatal("JSON suffix reopened reasoning")
			}
		} else if !record.Request.Thinking || record.Response.Usage.Details.Reasoning == 0 {
			t.Fatal("provider silently disabled ordinary-turn reasoning")
		}
	}
	t.Logf("PASS completed_cases=%d, %d JSON-only suffixes, exact assembled receipt; no tools or team-state changes", passed, partials)
}
