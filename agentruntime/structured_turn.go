package agentruntime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"path"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// StructuredTurnInstruction describes a transport, not a role or work method.
// The same tool names, argument schemas, and execution authorities still apply.
const StructuredTurnInstruction = "Respond with one JSON object matching the response schema. To continue working, return a calls array; each item has exactly one property naming an available tool, whose value is the actual argument object. To finish, return submit_result with its actual result object instead of calls. The response has exactly one top-level property: calls or submit_result, never both. There is no separate readiness call. Never JSON-encode argument objects or arrays in strings. Combine same-file replacements in one edit_file.edits array against one hash; never propose identical old_text and new_text. Group independent reads; inspect their results before proposing dependent calls."

// StructuredTurnContract puts each tool's argument schema under its own name.
// This avoids a union of generic name/arguments objects: model servers that
// relax oneOf cannot then lose the selected tool's nested type constraints.
type StructuredTurnContract struct {
	schema   json.RawMessage
	compiled *jsonschema.Schema
	final    string
}

// A complete JSON response rejected before dispatch is recoverable in the same
// invocation. Keep its exact structured value; never execute its proposed calls.
type RejectedTurnError struct {
	ResponseID  string            `json:"response_id"`
	Response    json.RawMessage   `json:"response"`
	Problem     string            `json:"problem"`
	Diagnostics *ModelDiagnostics `json:"diagnostics,omitempty"`
}

func (err *RejectedTurnError) Error() string { return err.Problem }
func (err *RejectedTurnError) Unwrap() error { return ErrInvalidTurn }

func (err *RejectedTurnError) valid() bool {
	return err != nil && err.ResponseID != "" && len(err.Response) <= 2<<20 && json.Valid(err.Response) && err.Problem != ""
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
	generation, err := generationSchema(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: generation schema: %v", ErrInvalidTurn, err)
	}
	return &StructuredTurnContract{schema: generation, compiled: compiled, final: final}, nil
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
		problem := fmt.Sprintf("%s: structured turn schema: %v", ErrInvalidTurn, err)
		// Add actionable pagination guidance only for an actual oversized read.
		// Keep schema enforcement intact and dispatch none of the rejected batch.
		var proposal struct {
			Calls []map[string]json.RawMessage `json:"calls"`
		}
		if json.Unmarshal(raw, &proposal) == nil {
			for _, call := range proposal.Calls {
				for _, name := range []string{"read_file", "read_file_from_commit"} {
					var read struct {
						Count int `json:"line_count"`
					}
					if json.Unmarshal(call[name], &read) == nil && read.Count > 400 {
						problem += "; " + name + ": maximum 400 lines PER CALL; use line_count=400 (or omit it), then continue at returned next_start_line. line_count is not an ending line or total file length"
					}
				}
			}
		}
		return Completion{}, &RejectedTurnError{ResponseID: responseID, Response: append(json.RawMessage(nil), raw...), Problem: problem}
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
	// Multiple mutations of one preimage cannot be independently hash-bound.
	// Reject the entire proposal before its first effect; edit_file accepts an
	// atomic ordered list for this case. Different files may still be batched.
	paths := make(map[string]bool)
	for _, call := range result.ToolCalls {
		if call.Name == "edit_file" {
			var edit struct {
				Edits []struct {
					Old string `json:"old_text"`
					New string `json:"new_text"`
				} `json:"edits"`
			}
			if json.Unmarshal(call.Arguments, &edit) == nil {
				for index, replacement := range edit.Edits {
					if replacement.Old == replacement.New {
						return Completion{}, &RejectedTurnError{ResponseID: responseID, Response: append(json.RawMessage(nil), raw...), Problem: fmt.Sprintf("edit_file.edits[%d] is a no-op: old_text equals new_text; no tools dispatched; omit the replacement", index)}
					}
				}
			}
		}
		if call.Name != "write_file" && call.Name != "create_file" && call.Name != "restore_file_from_commit" && call.Name != "edit_file" {
			continue
		}
		var mutation struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(call.Arguments, &mutation) == nil && mutation.Path != "" {
			mutation.Path = path.Clean(mutation.Path)
			if paths[mutation.Path] {
				return Completion{}, &RejectedTurnError{ResponseID: responseID, Response: append(json.RawMessage(nil), raw...), Problem: fmt.Sprintf("same-file mutation batch for %q: no tools dispatched; combine replacements in one edit_file.edits array against one hash", mutation.Path)}
			}
			paths[mutation.Path] = true
		}
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
