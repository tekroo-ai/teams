package nativeagent

import (
	"encoding/json"
	"fmt"
	"slices"
)

// ResultEnumLimit narrows an existing signed enum to coordinator-admitted
// values. "*" traverses array items; it is not a model-selectable path.
type ResultEnumLimit struct {
	PropertyPath []string
	Values       []string
}

func narrowResultEnums(raw json.RawMessage, limits []ResultEnumLimit) (json.RawMessage, error) {
	if len(limits) == 0 {
		return raw, nil
	}
	var schema map[string]any
	if json.Unmarshal(raw, &schema) != nil || schema == nil {
		return nil, ErrInvalidBinding
	}
	for _, limit := range limits {
		if len(limit.PropertyPath) == 0 || len(limit.Values) == 0 {
			return nil, ErrInvalidBinding
		}
		node := schema
		for _, name := range limit.PropertyPath {
			var next map[string]any
			if name == "*" && node["type"] == "array" {
				next, _ = node["items"].(map[string]any)
			} else {
				properties, _ := node["properties"].(map[string]any)
				next, _ = properties[name].(map[string]any)
			}
			if next == nil {
				return nil, fmt.Errorf("%w: result enum path is not declared", ErrInvalidBinding)
			}
			node = next
		}
		existing, ok := node["enum"].([]any)
		if !ok {
			return nil, ErrInvalidBinding
		}
		values := append([]string(nil), limit.Values...)
		slices.Sort(values)
		for i, value := range values {
			if value == "" || i > 0 && value == values[i-1] || !slices.Contains(existing, any(value)) {
				return nil, ErrInvalidBinding
			}
		}
		node["enum"] = values
	}
	return json.Marshal(schema)
}
