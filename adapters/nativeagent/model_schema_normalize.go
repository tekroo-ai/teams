package nativeagent

import (
	"encoding/json"
	"reflect"
)

// A oneOf whose branches have distinct JSON types is equivalent to a type
// union with the branches' type-specific assertions. Keep the signed schema
// untouched; this only gives model servers a simpler tool-call grammar.
func simplifyDisjointOneOf(raw json.RawMessage) (json.RawMessage, error) {
	var schema any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, err
	}
	simplifySchemaNode(schema)
	return json.Marshal(schema)
}

func simplifySchemaNode(node any) {
	switch value := node.(type) {
	case []any:
		for _, item := range value {
			simplifySchemaNode(item)
		}
	case map[string]any:
		for _, child := range value {
			simplifySchemaNode(child)
		}
		branches, ok := value["oneOf"].([]any)
		if !ok || len(branches) < 2 || value["type"] != nil {
			return
		}
		types := make([]string, 0, len(branches))
		merged := make(map[string]any)
		seen := make(map[string]bool)
		for _, branch := range branches {
			item, ok := branch.(map[string]any)
			if !ok {
				return
			}
			kind, ok := item["type"].(string)
			if !ok || seen[kind] {
				return
			}
			seen[kind] = true
			types = append(types, kind)
			for key, assertion := range item {
				if key == "type" {
					continue
				}
				// Only lift assertions that JSON Schema applies to this
				// branch's type. An enum, const, or composition keyword
				// would also constrain the other branch after merging.
				if !typeSpecificAssertion(kind, key) {
					return
				}
				if existing, present := merged[key]; present && !reflect.DeepEqual(existing, assertion) {
					return
				}
				merged[key] = assertion
			}
		}
		for key, assertion := range merged {
			if existing, present := value[key]; present && !reflect.DeepEqual(existing, assertion) {
				return
			}
		}
		delete(value, "oneOf")
		value["type"] = types
		for key, assertion := range merged {
			value[key] = assertion
		}
	}
}

func typeSpecificAssertion(kind, key string) bool {
	switch kind {
	case "string":
		return key == "minLength" || key == "maxLength" || key == "pattern"
	case "array":
		return key == "items" || key == "minItems" || key == "maxItems" || key == "uniqueItems"
	case "object":
		return key == "properties" || key == "required" || key == "additionalProperties" || key == "minProperties" || key == "maxProperties"
	default:
		return false
	}
}
