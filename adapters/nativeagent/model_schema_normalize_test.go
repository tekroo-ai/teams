package nativeagent

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestDisjointOneOfBecomesEquivalentModelFacingUnion(t *testing.T) {
	source := json.RawMessage(`{"type":"object","required":["architecture","decision"],"properties":{"architecture":{"oneOf":[{"type":"string","minLength":1},{"type":"array","minItems":1,"items":{"type":"string","minLength":1}}]},"decision":{"oneOf":[{"type":"string","minLength":1},{"type":"object","additionalProperties":false,"required":["name"],"properties":{"name":{"type":"string","minLength":1}}}]}}}`)
	simplified, err := simplifyDisjointOneOf(source)
	if err != nil || bytes.Contains(simplified, []byte(`"oneOf"`)) {
		t.Fatalf("disjoint union not simplified: %s, err=%v", simplified, err)
	}
	for _, sample := range []string{
		`{"architecture":"one paragraph","decision":"keep it small"}`,
		`{"architecture":["first","second"],"decision":{"name":"keep it small"}}`,
		`{"architecture":"","decision":"valid"}`,
		`{"architecture":[],"decision":"valid"}`,
		`{"architecture":{},"decision":"valid"}`,
		`{"architecture":"valid","decision":{"extra":"not allowed"}}`,
	} {
		before := schemaAccepts(t, source, []byte(sample))
		after := schemaAccepts(t, simplified, []byte(sample))
		if before != after {
			t.Fatalf("simplification changed acceptance for %s: signed=%t model=%t", sample, before, after)
		}
	}
}

func TestDisjointOneOfWithCrossTypeAssertionStaysUnchanged(t *testing.T) {
	source := json.RawMessage(`{"oneOf":[{"type":"string","enum":["ready"]},{"type":"array","items":{"type":"string"}}]}`)
	simplified, err := simplifyDisjointOneOf(source)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(simplified, []byte(`"oneOf"`)) {
		t.Fatalf("cross-type enum was lifted into union: %s", simplified)
	}
}

func schemaAccepts(t *testing.T, schemaRaw, instanceRaw []byte) bool {
	t.Helper()
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaRaw))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	const resource = "urn:tekroo:model-schema-normalize-test"
	if err := compiler.AddResource(resource, document); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(resource)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(instanceRaw))
	if err != nil {
		t.Fatal(err)
	}
	return compiled.Validate(instance) == nil
}
