package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// OpenAIModel talks directly to an OpenAI-compatible /v1/chat/completions
// server. It never executes a tool; the Teams runner owns that transition.
type OpenAIModel struct {
	Client    *http.Client
	BaseURL   string
	Model     string
	APIKey    string
	Tools     []ToolDefinition
	MaxTokens int
}

func (model OpenAIModel) Complete(ctx context.Context, transcript []Message) (Completion, error) {
	base, err := url.Parse(model.BaseURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || model.Client == nil || model.Model == "" || model.MaxTokens <= 0 || len(transcript) == 0 {
		return Completion{}, ErrInvalidTurn
	}
	messages := make([]map[string]any, 0, len(transcript))
	for _, item := range transcript {
		wire := map[string]any{"role": item.Role, "content": item.Content}
		switch item.Role {
		case "system":
		case "user":
		case "assistant":
			if len(item.ToolCalls) > 0 {
				calls := make([]map[string]any, 0, len(item.ToolCalls))
				for _, call := range item.ToolCalls {
					calls = append(calls, map[string]any{"id": call.ID, "type": "function", "function": map[string]any{"name": call.Name, "arguments": string(call.Arguments)}})
				}
				wire["tool_calls"] = calls
			}
		case "tool":
			if item.CallID == "" {
				return Completion{}, ErrInvalidTurn
			}
			wire["tool_call_id"] = item.CallID
		default:
			return Completion{}, ErrInvalidTurn
		}
		messages = append(messages, wire)
	}
	request := map[string]any{"model": model.Model, "messages": messages, "stream": false, "max_tokens": model.MaxTokens}
	if len(model.Tools) > 0 {
		tools := make([]map[string]any, 0, len(model.Tools))
		for _, tool := range model.Tools {
			if tool.Name == "" || !json.Valid(tool.Parameters) {
				return Completion{}, ErrInvalidTurn
			}
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": tool.Name, "description": tool.Description, "parameters": tool.Parameters}})
		}
		request["tools"] = tools
	}
	body, err := json.Marshal(request)
	if err != nil {
		return Completion{}, err
	}
	endpoint := strings.TrimRight(model.BaseURL, "/") + "/chat/completions"
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Completion{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if model.APIKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+model.APIKey)
	}
	response, err := model.Client.Do(httpRequest)
	if err != nil {
		return Completion{}, err
	}
	defer response.Body.Close()
	encoded, err := io.ReadAll(io.LimitReader(response.Body, 2<<20+1))
	if err != nil {
		return Completion{}, err
	}
	if len(encoded) > 2<<20 || response.StatusCode < 200 || response.StatusCode >= 300 {
		return Completion{}, fmt.Errorf("model completion HTTP %d (response bytes %d)", response.StatusCode, len(encoded))
	}
	var decoded struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil || len(decoded.Choices) != 1 {
		return Completion{}, fmt.Errorf("%w: malformed model completion", ErrInvalidTurn)
	}
	message := decoded.Choices[0].Message
	if reason := decoded.Choices[0].FinishReason; reason != "stop" && reason != "tool_calls" {
		return Completion{}, fmt.Errorf("%w: incomplete model response (finish_reason=%q)", ErrInvalidTurn, reason)
	}
	if len(message.ToolCalls) > 0 && decoded.Choices[0].FinishReason != "tool_calls" {
		return Completion{}, fmt.Errorf("%w: tool calls without tool-call finish reason", ErrInvalidTurn)
	}
	result := Completion{Text: message.Content}
	for _, call := range message.ToolCalls {
		if call.ID == "" || call.Function.Name == "" || !json.Valid([]byte(call.Function.Arguments)) {
			return Completion{}, fmt.Errorf("%w: malformed model tool call", ErrInvalidTurn)
		}
		result.ToolCalls = append(result.ToolCalls, ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: json.RawMessage(call.Function.Arguments)})
	}
	return result, nil
}
