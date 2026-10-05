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

func structuredTestContract(t *testing.T) *StructuredTurnContract {
	t.Helper()
	contract, err := NewStructuredTurnContract([]ToolDefinition{
		{Name: "read_file", Parameters: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["path"],"properties":{"path":{"type":"string","minLength":1}}}`)},
		{Name: "submit_result", Parameters: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["work_product","evidence"],"properties":{"work_product":{"type":"object","additionalProperties":false,"required":["ok"],"properties":{"ok":{"const":true}}},"evidence":{"type":"array","items":{"type":"string"}}}}`)},
	}, "submit_result")
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func TestStructuredTurnRejectsMalformedCallsBeforeDispatch(t *testing.T) {
	contract := structuredTestContract(t)
	for _, raw := range []string{
		`{"calls":[]}`,
		`{}`,
		`{"calls":[{}]}`,
		`{"calls":[{"unknown":{}}]}`,
		`{"calls":[{"read_file":{"file_path":"README.md"}}]}`,
		`{"calls":[{"read_file":"{\"path\":\"README.md\"}"}]}`,
		`{"calls":[{"read_file":{"path":"a"},"submit_result":{"work_product":{"ok":true},"evidence":[]}}]}`,
		`{"submit_result":{"work_product":"{\"ok\":true}","evidence":[]}}`,
		`{"submit_result":{"work_product":{"ok":true},"evidence":"checked"}}`,
		`{"calls":[{"read_file":{"path":"a"}}],"submit_result":{"work_product":{"ok":true},"evidence":[]}}`,
		`{"calls":[{"read_file":{"path":"a"}},{"submit_result":{"work_product":{"ok":true},"evidence":[]}}]}`,
		`{"calls":[{"read_file":{"path":"a"}}],"extra":true}`,
		`{"calls":[{"read_file":{"path":"a"}}]} {}`,
	} {
		if _, err := contract.decode(json.RawMessage(raw), "response-1"); !errors.Is(err, ErrInvalidTurn) {
			t.Fatalf("accepted malformed response %s: %v", raw, err)
		}
	}
}

func TestStructuredTurnPreservesLocalReferencesAndExactNumbers(t *testing.T) {
	contract, err := NewStructuredTurnContract([]ToolDefinition{
		{Name: "submit_result", Parameters: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["receipt"],"properties":{"receipt":{"$ref":"#/$defs/receipt"}},"$defs":{"receipt":{"type":"integer","const":9007199254740993}}}`)},
	}, "submit_result")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := contract.decode(json.RawMessage(`{"submit_result":{"receipt":9007199254740993}}`), "exact"); err != nil {
		t.Fatal(err)
	}
	if _, err := contract.decode(json.RawMessage(`{"submit_result":{"receipt":9007199254740992}}`), "rounded"); !errors.Is(err, ErrInvalidTurn) {
		t.Fatalf("rounded number accepted: %v", err)
	}
	if _, err := NewStructuredTurnContract([]ToolDefinition{
		{Name: "submit_result", Parameters: json.RawMessage(`{"type":"object","properties":{"receipt":{"$ref":"https://example.invalid/unbound.json"}}}`)},
	}, "submit_result"); err == nil {
		t.Fatal("unbound external schema reference accepted")
	}
}

func TestStructuredTurnRoundTripHasNoReadinessOrCorrectionRequest(t *testing.T) {
	contract := structuredTestContract(t)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		var body struct {
			Tools      json.RawMessage              `json:"tools"`
			ToolChoice string                       `json:"tool_choice"`
			Messages   []map[string]json.RawMessage `json:"messages"`
			Format     struct {
				Type   string `json:"type"`
				Schema struct {
					Strict bool            `json:"strict"`
					Schema json.RawMessage `json:"schema"`
				} `json:"json_schema"`
			} `json:"response_format"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body.Tools) != 0 || body.ToolChoice != "none" || body.Format.Type != "json_schema" || !body.Format.Schema.Strict || string(body.Format.Schema.Schema) != string(contract.schema) {
			t.Errorf("first-pass schema missing or API tools still enabled: %+v", body)
		}
		content := `{"calls":[{"read_file":{"path":"README.md"}},{"read_file":{"path":"AGENTS.md"}}]}`
		if requests == 2 {
			if len(body.Messages) != 5 {
				t.Errorf("unexpected readiness/correction or missing batch history: %d messages", len(body.Messages))
			}
			for _, message := range body.Messages {
				if len(message["tool_calls"]) != 0 || len(message["tool_call_id"]) != 0 || string(message["role"]) == `"tool"` {
					t.Error("API-native tool history leaked into schema-action transport")
				}
			}
			var observation string
			_ = json.Unmarshal(body.Messages[3]["content"], &observation)
			var parsed struct {
				Result struct {
					Output map[string]any `json:"output"`
				} `json:"tool_result"`
			}
			if json.Unmarshal([]byte(observation), &parsed) != nil || parsed.Result.Output["content"] != "hello" {
				t.Errorf("tool result object was string-encoded: %s", observation)
			}
			content = `{"submit_result":{"work_product":{"ok":true},"evidence":["checked"]}}`
		} else if requests > 2 {
			t.Error("unexpected additional model request")
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"id": fmt.Sprintf("response-%d", requests), "choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": content}}}})
	}))
	defer server.Close()
	journal, tools := &memoryJournal{}, &countingTool{}
	runner := Runner{Journal: journal, Tools: tools, SystemPrompt: StructuredTurnInstruction, FinalTool: "submit_result", MaxTurns: 3,
		Model:    OpenAIModel{Client: server.Client(), BaseURL: server.URL + "/v1", Model: "local", MaxTokens: 512, FinalTool: "submit_result", TurnContract: contract},
		Finalize: func(raw json.RawMessage) (string, error) { return string(raw), nil },
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := runner.Run(ctx, "structured-invocation", testRequestDigest, "Inspect files and finish")
	if err != nil || !json.Valid([]byte(result)) || requests != 2 || tools.calls != 2 {
		t.Fatalf("result=%s error=%v requests=%d tools=%d", result, err, requests, tools.calls)
	}
	if _, err := runner.Run(ctx, "structured-invocation", testRequestDigest, "Inspect files and finish"); err != nil || requests != 2 || tools.calls != 2 {
		t.Fatalf("replay repeated model or tools: %v requests=%d tools=%d", err, requests, tools.calls)
	}
	for _, entry := range journal.entries {
		if entry.Kind == ToolDone {
			var done ToolResult
			_ = json.Unmarshal(entry.Payload, &done)
			if done.Error != "" {
				t.Fatalf("unexpected correction: %+v", done)
			}
		}
	}
}

func TestStructuredTurnRejectsIncompleteOrNativeToolResponses(t *testing.T) {
	contract := structuredTestContract(t)
	for _, response := range []string{
		`{"id":"r","choices":[{"finish_reason":"length","message":{"content":"{\"calls\":[]}"}}]}`,
		`{"id":"r","choices":[{"finish_reason":"tool_calls","message":{"tool_calls":[{"id":"c","function":{"name":"read_file","arguments":"{\"path\":\"a\"}"}}]}}]}`,
		`{"id":"r","choices":[{"finish_reason":"stop","message":{"content":"{\"calls\":[{\"submit_result\":{\"work_product\":{\"ok\":true},\"evidence\":\"checked\"}}]}"}}]}`,
	} {
		t.Run(response, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(response)) }))
			defer server.Close()
			model := OpenAIModel{Client: server.Client(), BaseURL: server.URL + "/v1", Model: "local", MaxTokens: 512, TurnContract: contract}
			if _, err := model.Complete(context.Background(), []Message{{Role: "user", Content: "finish"}}); !errors.Is(err, ErrInvalidTurn) {
				t.Fatalf("accepted unsupported response: %v", err)
			}
		})
	}
}
