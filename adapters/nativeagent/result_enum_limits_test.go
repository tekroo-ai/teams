package nativeagent

import (
	"encoding/json"
	"testing"
)

func TestResultEnumsNarrowRoutingWithoutChangingSignedSchema(t *testing.T) {
	raw := json.RawMessage(`{"type":"object","properties":{"tasks":{"type":"array","items":{"type":"object","properties":{"purpose":{"enum":["IMPLEMENTATION","VALIDATION"]}}}}}}`)
	before := string(raw)
	limit := ResultEnumLimit{PropertyPath: []string{"tasks", "*", "purpose"}, Values: []string{"IMPLEMENTATION"}}
	narrowed, err := narrowResultEnums(raw, []ResultEnumLimit{limit})
	if err != nil || !schemaAccepts(t, narrowed, []byte(`{"tasks":[{"purpose":"IMPLEMENTATION"}]}`)) || schemaAccepts(t, narrowed, []byte(`{"tasks":[{"purpose":"VALIDATION"}]}`)) || string(raw) != before {
		t.Fatalf("routing schema not narrowed safely: %s %v", narrowed, err)
	}
	for _, invalid := range []ResultEnumLimit{
		{PropertyPath: limit.PropertyPath, Values: []string{"INVENTED"}},
		{PropertyPath: limit.PropertyPath},
		{PropertyPath: []string{"unknown"}, Values: limit.Values},
		{PropertyPath: limit.PropertyPath, Values: []string{"IMPLEMENTATION", "IMPLEMENTATION"}},
	} {
		if _, err := narrowResultEnums(raw, []ResultEnumLimit{invalid}); err == nil {
			t.Fatal("invalid narrowing accepted")
		}
	}
}
