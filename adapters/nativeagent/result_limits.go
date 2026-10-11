package nativeagent

import (
	"encoding/json"
	"fmt"
)

// ResultArrayLimit is supplied by the coordinator, never by the model. A path
// names nested object properties, e.g. work_product/tasks, not a role name.
type ResultArrayLimit struct {
	PropertyPath []string
	MaxItems     int
}

func narrowResultArrays(raw json.RawMessage, limits []ResultArrayLimit) (json.RawMessage, error) {
	if len(limits) == 0 {
		return raw, nil
	}
	var schema map[string]any
	if json.Unmarshal(raw, &schema) != nil || schema == nil {
		return nil, ErrInvalidBinding
	}
	for _, limit := range limits {
		if limit.MaxItems < 0 || len(limit.PropertyPath) == 0 {
			return nil, fmt.Errorf("%w: invalid result-array limit", ErrInvalidBinding)
		}
		node := schema
		for _, name := range limit.PropertyPath {
			if name == "*" && node["type"] == "array" {
				next, ok := node["items"].(map[string]any)
				if !ok {
					return nil, ErrInvalidBinding
				}
				node = next
				continue
			}
			properties, ok := node["properties"].(map[string]any)
			if !ok || name == "" {
				return nil, fmt.Errorf("%w: result-array path %v is not declared", ErrInvalidBinding, limit.PropertyPath)
			}
			node, ok = properties[name].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%w: result-array path %v is not declared", ErrInvalidBinding, limit.PropertyPath)
			}
		}
		if node["type"] != "array" {
			return nil, fmt.Errorf("%w: result-array path %v is not an array", ErrInvalidBinding, limit.PropertyPath)
		}
		maximum := limit.MaxItems
		if existing, ok := node["maxItems"].(float64); ok && existing < float64(maximum) {
			maximum = int(existing)
		}
		if minimum, ok := node["minItems"].(float64); ok && minimum > float64(maximum) {
			return nil, fmt.Errorf("%w: result-array admission limit conflicts with minItems", ErrInvalidBinding)
		}
		node["maxItems"] = float64(maximum)
	}
	return json.Marshal(schema)
}
