package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOpenAIModelRoundTrip(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if request.URL.Path != "/v1/chat/completions" || request.Method != http.MethodPost {
			t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		var body struct {
			Model    string            `json:"model"`
			Messages []json.RawMessage `json:"messages"`
			Tools    []json.RawMessage `json:"tools"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		expectedMessages := 1
		if requests == 2 {
			expectedMessages = 3
		}
		if body.Model != "local-coder" || len(body.Tools) != 1 || len(body.Messages) != expectedMessages {
			t.Errorf("request %d model=%q tools=%d messages=%d", requests, body.Model, len(body.Tools), len(body.Messages))
		}
		writer.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			_, _ = writer.Write([]byte(`{"choices":[{"finish_reason":"tool_calls","message":{"content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"README.md\"}"}}]}}]}`))
		} else {
			_, _ = writer.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"done"}}]}`))
		}
	}))
	defer server.Close()
	model := OpenAIModel{Client: server.Client(), BaseURL: server.URL + "/v1", Model: "local-coder", MaxTokens: 100, Tools: []ToolDefinition{{Name: "read_file", Parameters: json.RawMessage(`{"type":"object"}`)}}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	first, err := model.Complete(ctx, []Message{{Role: "user", Content: "read"}})
	if err != nil || len(first.ToolCalls) != 1 || first.ToolCalls[0].Name != "read_file" {
		t.Fatalf("first completion=%+v err=%v", first, err)
	}
	second, err := model.Complete(ctx, []Message{{Role: "user", Content: "read"}, {Role: "assistant", ToolCalls: first.ToolCalls}, {Role: "tool", CallID: "c1", Content: `{"content":"hi"}`}})
	if err != nil || second.Text != "done" {
		t.Fatalf("second completion=%+v err=%v", second, err)
	}
}

func TestOpenAIModelRejectsTruncatedCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"choices":[{"finish_reason":"length","message":{"content":"partial"}}]}`))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	model := OpenAIModel{Client: server.Client(), BaseURL: server.URL + "/v1", Model: "local", MaxTokens: 10}
	if _, err := model.Complete(ctx, []Message{{Role: "user", Content: "hello"}}); !errors.Is(err, ErrInvalidTurn) {
		t.Fatalf("truncated response treated as complete: %v", err)
	}
}

func TestOpenAIModelSendsStrictToolSchema(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Tools []struct {
				Function struct {
					Strict     bool            `json:"strict"`
					Parameters json.RawMessage `json:"parameters"`
				} `json:"function"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body.Tools) != 1 || !body.Tools[0].Function.Strict || string(body.Tools[0].Function.Parameters) != `{"type":"object","required":["schema_version"]}` {
			t.Errorf("strict tool contract not sent: %+v", body.Tools)
		}
		_, _ = writer.Write([]byte(`{"choices":[{"finish_reason":"tool_calls","message":{"tool_calls":[{"id":"c1","function":{"name":"submit_result","arguments":"{\"schema_version\":\"1.0.0\"}"}}]}}]}`))
	}))
	defer server.Close()
	model := OpenAIModel{Client: server.Client(), BaseURL: server.URL + "/v1", Model: "local", MaxTokens: 100, Tools: []ToolDefinition{{Name: "submit_result", Strict: true, Parameters: json.RawMessage(`{"type":"object","required":["schema_version"]}`)}}}
	if _, err := model.Complete(context.Background(), []Message{{Role: "user", Content: "result"}}); err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIModelConstrainedFinalCorrection(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","additionalProperties":false,"required":["schema_version"],"properties":{"schema_version":{"const":"1.0.0"}}}`)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Tools          []json.RawMessage `json:"tools"`
			ResponseFormat struct {
				Type       string `json:"type"`
				JSONSchema struct {
					Strict bool            `json:"strict"`
					Schema json.RawMessage `json:"schema"`
				} `json:"json_schema"`
			} `json:"response_format"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body.Tools) != 0 || body.ResponseFormat.Type != "json_schema" || !body.ResponseFormat.JSONSchema.Strict || string(body.ResponseFormat.JSONSchema.Schema) != string(schema) {
			t.Errorf("constrained repair request malformed: %+v", body)
		}
		_, _ = writer.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"{\"schema_version\":\"1.0.0\"}"}}]}`))
	}))
	defer server.Close()
	model := OpenAIModel{Client: server.Client(), BaseURL: server.URL + "/v1", Model: "local", MaxTokens: 100, FinalTool: "submit_result", FinalSchema: schema}
	transcript := []Message{{Role: "user", Content: "do work"}, {Role: "assistant", ToolCalls: []ToolCall{{ID: "bad", Name: "submit_result", Arguments: json.RawMessage(`{}`)}}}, {Role: "tool", Name: "submit_result", CallID: "bad", Content: `"schema violation"`}}
	result, err := model.Complete(context.Background(), transcript)
	if err != nil || len(result.ToolCalls) != 1 || result.ToolCalls[0].Name != "submit_result" || string(result.ToolCalls[0].Arguments) != `{"schema_version":"1.0.0"}` {
		t.Fatalf("constrained repair=%+v err=%v", result, err)
	}
}
