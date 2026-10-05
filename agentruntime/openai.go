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
	Strict      bool            `json:"strict,omitempty"`
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
	// A rejected final tool call is retried through the server's constrained
	// JSON response channel, then presented to the runner as a final tool call.
	FinalTool   string
	FinalSchema json.RawMessage
	// TurnContract constrains every choice (ordinary tool or final result) in
	// the original request. It never asks the model for a readiness turn.
	TurnContract *StructuredTurnContract
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
				if model.TurnContract != nil {
					content, err := structuredAssistantContent(item.ToolCalls, model.TurnContract.final)
					if err != nil {
						return Completion{}, err
					}
					wire["content"] = content
					break
				}
				calls := make([]map[string]any, 0, len(item.ToolCalls))
				for _, call := range item.ToolCalls {
					calls = append(calls, map[string]any{"id": call.ID, "type": "function", "function": map[string]any{"name": call.Name, "arguments": string(call.Arguments)}})
				}
				wire["tool_calls"] = calls
			}
		case "tool":
			if model.TurnContract != nil {
				content, err := structuredToolContent(item)
				if err != nil {
					return Completion{}, err
				}
				wire["role"], wire["content"] = "user", content
				break
			}
			if item.CallID == "" {
				return Completion{}, ErrInvalidTurn
			}
			wire["tool_call_id"] = item.CallID
		default:
			return Completion{}, ErrInvalidTurn
		}
		messages = append(messages, wire)
	}
	repairFinal := len(transcript) > 0 && model.FinalTool != "" && transcript[len(transcript)-1].Role == "tool" && transcript[len(transcript)-1].Name == model.FinalTool
	request := map[string]any{"model": model.Model, "messages": messages, "stream": false, "max_tokens": model.MaxTokens}
	if model.TurnContract != nil {
		request["tool_choice"] = "none"
		request["response_format"] = map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "tekroo_turn", "strict": true, "schema": model.TurnContract.schema}}
	} else if repairFinal {
		if !json.Valid(model.FinalSchema) {
			return Completion{}, ErrInvalidTurn
		}
		messages = append(messages, map[string]any{"role": "user", "content": "Return the corrected final-result arguments as one JSON object matching the response schema."})
		request["messages"] = messages
		request["response_format"] = map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "tekroo_final_result", "strict": true, "schema": model.FinalSchema}}
	} else if len(model.Tools) > 0 {
		tools := make([]map[string]any, 0, len(model.Tools))
		for _, tool := range model.Tools {
			if tool.Name == "" || !json.Valid(tool.Parameters) {
				return Completion{}, ErrInvalidTurn
			}
			function := map[string]any{"name": tool.Name, "description": tool.Description, "parameters": tool.Parameters}
			if tool.Strict {
				function["strict"] = true
			}
			tools = append(tools, map[string]any{"type": "function", "function": function})
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
		ID      string `json:"id"`
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
	if model.TurnContract != nil {
		if decoded.Choices[0].FinishReason != "stop" || len(message.ToolCalls) != 0 {
			return Completion{}, fmt.Errorf("%w: incomplete structured turn", ErrInvalidTurn)
		}
		return model.TurnContract.decode(json.RawMessage(message.Content), decoded.ID)
	}
	if repairFinal {
		if decoded.Choices[0].FinishReason != "stop" || len(message.ToolCalls) != 0 || !json.Valid([]byte(message.Content)) {
			return Completion{}, fmt.Errorf("%w: constrained final result was not one JSON object", ErrInvalidTurn)
		}
		var object map[string]json.RawMessage
		if json.Unmarshal([]byte(message.Content), &object) != nil || object == nil {
			return Completion{}, fmt.Errorf("%w: constrained final result was not one JSON object", ErrInvalidTurn)
		}
		return Completion{ToolCalls: []ToolCall{{ID: fmt.Sprintf("final-json-%d", len(transcript)), Name: model.FinalTool, Arguments: json.RawMessage(message.Content)}}}, nil
	}
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
