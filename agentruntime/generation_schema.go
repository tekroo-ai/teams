package agentruntime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// This is a semantics-preserving transport compilation, not another contract.
// The canonical validator remains authoritative. Small finite domains avoid
// relying on servers' soft numeric bounds; explicit branches avoid ignored
// if/then rules. Never walk enum/const/example values as though they are schemas.
func generationSchema(raw json.RawMessage) (json.RawMessage, error) {
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	compiled, err := lowerGenerationNode(document)
	if err != nil {
		return nil, err
	}
	return json.Marshal(compiled)
}

func lowerGenerationNode(value any) (any, error) {
	node, ok := value.(map[string]any)
	if !ok {
		return value, nil
	}
	if _, present := node["if"]; present {
		branches, err := expandFiniteCondition(node)
		if err != nil {
			return nil, err
		}
		return lowerGenerationNode(branches)
	}
	for _, key := range []string{"properties", "$defs", "definitions", "patternProperties", "dependentSchemas"} {
		if children, ok := node[key].(map[string]any); ok {
			for name, child := range children {
				lowered, err := lowerGenerationNode(child)
				if err != nil {
					return nil, fmt.Errorf("%s/%s: %w", key, name, err)
				}
				children[name] = lowered
			}
		}
	}
	for _, key := range []string{"items", "additionalProperties", "contains", "propertyNames", "not"} {
		if child, present := node[key]; present {
			lowered, err := lowerGenerationNode(child)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			node[key] = lowered
		}
	}
	for _, key := range []string{"oneOf", "anyOf", "allOf", "prefixItems"} {
		if children, ok := node[key].([]any); ok {
			for index, child := range children {
				lowered, err := lowerGenerationNode(child)
				if err != nil {
					return nil, fmt.Errorf("%s/%d: %w", key, index, err)
				}
				children[index] = lowered
			}
		}
	}
	if node["type"] == "integer" && node["enum"] == nil && node["const"] == nil {
		if values := finiteValues(node); values != nil {
			node["enum"] = values
		}
	}
	return node, nil
}

// At most 128 alternatives: bounded integer catalog parameters only, not an
// unbounded expansion of IDs, line numbers, or arbitrary numeric domains.
func finiteValues(node map[string]any) []any {
	if value, present := node["const"]; present {
		return []any{value}
	}
	if values, ok := node["enum"].([]any); ok {
		return values
	}
	if node["type"] != "integer" {
		return nil
	}
	low, lok := schemaInteger(node["minimum"])
	high, hok := schemaInteger(node["maximum"])
	if !lok || !hok || high < low || high > low+127 || low > 1<<52 || low < -(1<<52) {
		return nil
	}
	values := make([]any, 0, int(high-low+1))
	for number := low; number <= high; number++ {
		if bound, ok := schemaInteger(node["exclusiveMinimum"]); ok && number <= bound {
			continue
		}
		if bound, ok := schemaInteger(node["exclusiveMaximum"]); ok && number >= bound {
			continue
		}
		values = append(values, json.Number(fmt.Sprint(number)))
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

func schemaInteger(value any) (int64, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	integer, err := number.Int64()
	return integer, err == nil
}

// Supported conditionals discriminate one finite property. Compilation checks
// both presence and absence, preserving optional-field semantics. More complex
// conditions fail explicitly; they must not become unenforced generation rules.
func expandFiniteCondition(node map[string]any) (map[string]any, error) {
	condition, ok := node["if"].(map[string]any)
	if !ok || node["type"] != "object" || node["additionalProperties"] != false {
		return nil, fmt.Errorf("conditional requires a closed object")
	}
	for key := range condition {
		if key != "properties" && key != "required" {
			return nil, fmt.Errorf("unsupported conditional keyword %s", key)
		}
	}
	predicates, ok := condition["properties"].(map[string]any)
	if !ok || len(predicates) != 1 {
		return nil, fmt.Errorf("conditional requires one finite discriminator")
	}
	var name string
	for key := range predicates {
		name = key
	}
	for _, required := range schemaRequired(condition) {
		if required != name {
			return nil, fmt.Errorf("conditional depends on multiple fields")
		}
	}
	properties, _ := node["properties"].(map[string]any)
	property, ok := properties[name].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("conditional discriminator %s has no schema", name)
	}
	values := finiteValues(property)
	if len(values) == 0 {
		return nil, fmt.Errorf("conditional discriminator %s is not finite", name)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.UseLoader(turnSchemaLoader{})
	const resource = "urn:tekroo:generation-condition"
	if err := compiler.AddResource(resource, condition); err != nil {
		return nil, err
	}
	compiled, err := compiler.Compile(resource)
	if err != nil {
		return nil, err
	}
	var groups [2][]any
	for _, value := range values {
		group := 0
		if compiled.Validate(map[string]any{name: value}) == nil {
			group = 1
		}
		groups[group] = append(groups[group], value)
	}
	absent := -1
	if !slices.Contains(schemaRequired(node), name) {
		absent = 0
		if compiled.Validate(map[string]any{}) == nil {
			absent = 1
		}
	}
	base := cloneSchema(node)
	for _, key := range []string{"if", "then", "else", "$id", "$defs", "definitions"} {
		delete(base, key)
	}
	branches := make([]any, 0, 2)
	for group, allowed := range groups {
		if len(allowed) == 0 && absent != group {
			continue
		}
		branch := cloneSchema(base)
		props := branch["properties"].(map[string]any)
		if len(allowed) == 0 {
			delete(props, name) // Closed object: this branch permits absence only.
		} else {
			selected := props[name].(map[string]any)
			delete(selected, "const")
			selected["enum"] = allowed
			if absent != group {
				addRequired(branch, name)
			}
		}
		patchKey := "else"
		if group == 1 {
			patchKey = "then"
		}
		if patch, present := node[patchKey]; present {
			object, ok := patch.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("unsupported boolean conditional consequence")
			}
			if err := mergeConditionalAssertions(branch, object); err != nil {
				return nil, err
			}
		}
		branches = append(branches, branch)
	}
	result := map[string]any{"anyOf": branches}
	for _, key := range []string{"$id", "$defs", "definitions"} {
		if value, present := node[key]; present {
			result[key] = value
		}
	}
	return result, nil
}

func schemaRequired(node map[string]any) []string {
	var names []string
	for _, value := range arrayValues(node["required"]) {
		if name, ok := value.(string); ok {
			names = append(names, name)
		}
	}
	return names
}

func arrayValues(value any) []any { values, _ := value.([]any); return values }

func addRequired(node map[string]any, name string) {
	names := schemaRequired(node)
	if !slices.Contains(names, name) {
		names = append(names, name)
	}
	slices.Sort(names)
	values := make([]any, len(names))
	for index, item := range names {
		values[index] = item
	}
	node["required"] = values
}

func cloneSchema(value map[string]any) map[string]any {
	copy := make(map[string]any, len(value))
	for key, item := range value {
		switch item := item.(type) {
		case map[string]any:
			copy[key] = cloneSchema(item)
		case []any:
			items := make([]any, len(item))
			for index, child := range item {
				if object, ok := child.(map[string]any); ok {
					items[index] = cloneSchema(object)
				} else {
					items[index] = child
				}
			}
			copy[key] = items
		default:
			copy[key] = item
		}
	}
	return copy
}

// Consequences in the current catalog add required fields or refine existing
// properties. Conflicting assertions are rejected, never overwritten or relaxed.
func mergeConditionalAssertions(base, patch map[string]any) error {
	for key, value := range patch {
		switch key {
		case "required":
			for _, name := range schemaRequired(patch) {
				addRequired(base, name)
			}
		case "properties":
			properties, ok := base[key].(map[string]any)
			children, valid := value.(map[string]any)
			if !ok || !valid {
				return fmt.Errorf("unsupported conditional properties intersection")
			}
			for name, child := range children {
				object, ok := child.(map[string]any)
				existing, exists := properties[name].(map[string]any)
				if !ok || !exists {
					return fmt.Errorf("conditional property %s is not declared", name)
				}
				if err := mergeConditionalAssertions(existing, object); err != nil {
					return err
				}
			}
		case "const":
			if allowed, ok := base["enum"].([]any); ok && !slices.ContainsFunc(allowed, func(item any) bool { return reflect.DeepEqual(item, value) }) {
				return fmt.Errorf("conflicting conditional const")
			}
			if existing, present := base[key]; present && !reflect.DeepEqual(existing, value) {
				return fmt.Errorf("conflicting conditional const")
			}
			base[key] = value
		default:
			if existing, present := base[key]; present && reflect.DeepEqual(existing, value) {
				continue
			}
			return fmt.Errorf("unsupported conditional assertion %s", key)
		}
	}
	return nil
}
