package agentruntime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// StructuredTurnInstruction describes a transport, not a role or work method.
// The same tool names, argument schemas, and execution authorities still apply.
const StructuredTurnInstruction = "Respond with one JSON object matching the response schema. To continue working, return a calls array; each item has exactly one property naming an available tool, whose value is the actual argument object. To finish, return submit_result with its actual result object instead of calls. The response has exactly one top-level property: calls or submit_result, never both. There is no separate readiness call. Never JSON-encode argument objects or arrays in strings."

// StructuredTurnContract puts each tool's argument schema under its own name.
// This avoids a union of generic name/arguments objects: model servers that
// relax oneOf cannot then lose the selected tool's nested type constraints.
type StructuredTurnContract struct {
	schema   json.RawMessage
	compiled *jsonschema.Schema
	final    string
}

type turnSchemaLoader struct{}

func (turnSchemaLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("unbound turn schema resource: %s", url)
}

func NewStructuredTurnContract(tools []ToolDefinition, final string) (*StructuredTurnContract, error) {
	if len(tools) == 0 || final == "" || final == "calls" {
		return nil, ErrInvalidTurn
	}
	properties := make(map[string]any, len(tools))
	for _, tool := range tools {
		if tool.Name == "" || properties[tool.Name] != nil {
			return nil, ErrInvalidTurn
		}
		document, err := jsonschema.UnmarshalJSON(bytes.NewReader(tool.Parameters))
		arguments, object := document.(map[string]any)
		if err != nil || !object || arguments["type"] != "object" {
			return nil, ErrInvalidTurn
		}
		// Keep local references relative to this tool's schema root when the
		// document is embedded in the response schema; never fetch resources.
		if _, present := arguments["$id"]; !present {
			arguments["$id"] = "urn:tekroo:structured-turn-tool:" + url.PathEscape(tool.Name)
		}
		if tool.Description != "" {
			arguments["description"] = tool.Description
		}
		properties[tool.Name] = arguments
	}
	if properties[final] == nil {
		return nil, ErrInvalidTurn
	}
	finalSchema := properties[final]
	delete(properties, final)
	choices := map[string]any{final: finalSchema}
	if len(properties) > 0 {
		choices["calls"] = map[string]any{
			"type": "array", "minItems": 1,
			"items": map[string]any{"type": "object", "additionalProperties": false,
				"minProperties": 1, "maxProperties": 1, "properties": properties},
		}
	}
	raw, err := json.Marshal(map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type":    "object", "additionalProperties": false, "minProperties": 1, "maxProperties": 1,
		"properties": choices,
	})
	if err != nil {
		return nil, err
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	compiler.UseLoader(turnSchemaLoader{})
	const resource = "urn:tekroo:structured-turn"
	if err := compiler.AddResource(resource, document); err != nil {
		return nil, err
	}
	compiled, err := compiler.Compile(resource)
	if err != nil {
		return nil, fmt.Errorf("%w: compile turn schema: %v", ErrInvalidTurn, err)
	}
	return &StructuredTurnContract{schema: raw, compiled: compiled, final: final}, nil
}

func (contract *StructuredTurnContract) decode(raw json.RawMessage, responseID string) (Completion, error) {
	if contract == nil || contract.compiled == nil || responseID == "" {
		return Completion{}, ErrInvalidTurn
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return Completion{}, fmt.Errorf("%w: decode structured turn: %v", ErrInvalidTurn, err)
	}
	if err := contract.compiled.Validate(instance); err != nil {
		return Completion{}, fmt.Errorf("%w: structured turn schema: %v", ErrInvalidTurn, err)
	}
	var turn map[string]json.RawMessage
	if err := json.Unmarshal(raw, &turn); err != nil {
		return Completion{}, ErrInvalidTurn
	}
	if arguments, present := turn[contract.final]; present {
		return Completion{ToolCalls: []ToolCall{{ID: responseID + "-0", Name: contract.final, Arguments: arguments}}}, nil
	}
	var calls []map[string]json.RawMessage
	if err := json.Unmarshal(turn["calls"], &calls); err != nil {
		return Completion{}, ErrInvalidTurn
	}
	result := Completion{ToolCalls: make([]ToolCall, 0, len(calls))}
	for index, entry := range calls {
		for name, arguments := range entry {
			result.ToolCalls = append(result.ToolCalls, ToolCall{
				ID: fmt.Sprintf("%s-%d", responseID, index), Name: name, Arguments: arguments,
			})
		}
	}
	if err := validateCompletion(result, contract.final); err != nil {
		return Completion{}, err
	}
	return result, nil
}

func structuredAssistantContent(calls []ToolCall, final string) (string, error) {
	if len(calls) == 1 && calls[0].Name == final && json.Valid(calls[0].Arguments) {
		raw, err := json.Marshal(map[string]json.RawMessage{final: calls[0].Arguments})
		return string(raw), err
	}
	items := make([]map[string]json.RawMessage, 0, len(calls))
	for _, call := range calls {
		if call.Name == "" || !json.Valid(call.Arguments) {
			return "", ErrInvalidTurn
		}
		items = append(items, map[string]json.RawMessage{call.Name: call.Arguments})
	}
	raw, err := json.Marshal(struct {
		Calls []map[string]json.RawMessage `json:"calls"`
	}{items})
	return string(raw), err
}

func structuredToolContent(message Message) (string, error) {
	if message.CallID == "" || message.Name == "" || !json.Valid([]byte(message.Content)) {
		return "", ErrInvalidTurn
	}
	type observation struct {
		CallID string          `json:"call_id"`
		Name   string          `json:"name"`
		Output json.RawMessage `json:"output"`
	}
	raw, err := json.Marshal(struct {
		Result observation `json:"tool_result"`
	}{observation{message.CallID, message.Name, json.RawMessage(message.Content)}})
	return string(raw), err
}
