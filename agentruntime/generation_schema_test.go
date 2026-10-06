package agentruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const conditionalResultFixture = `{"type":"object","additionalProperties":false,"required":["outcome","work_product"],"properties":{"outcome":{"enum":["completed","blocked","failed","needs_decision"]},"work_product":{"type":"object","additionalProperties":false,"properties":{"architecture":{"type":"string"},"tasks":{"type":"array","minItems":1,"items":{"type":"string"}}}}},"if":{"properties":{"outcome":{"const":"completed"}}},"then":{"properties":{"work_product":{"required":["architecture","tasks"]}}}}`

func compileGenerationTestSchema(t *testing.T, raw []byte) *jsonschema.Schema {
	t.Helper()
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.UseLoader(turnSchemaLoader{})
	if err := compiler.AddResource("urn:tekroo:generation-test", document); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile("urn:tekroo:generation-test")
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func generationTestAccepts(t *testing.T, schema *jsonschema.Schema, raw []byte) bool {
	t.Helper()
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return schema.Validate(value) == nil
}

func TestGenerationConditionalPreservesOutcomeSemantics(t *testing.T) {
	source := json.RawMessage(conditionalResultFixture)
	before := append([]byte(nil), source...)
	generation, err := generationSchema(source)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(source, before) || bytes.Contains(generation, []byte(`"if"`)) || bytes.Contains(generation, []byte(`"then"`)) {
		t.Fatalf("source changed or conditional not lowered: %s", generation)
	}
	canonical, lowered := compileGenerationTestSchema(t, source), compileGenerationTestSchema(t, generation)
	for _, outcome := range []string{"completed", "blocked", "failed", "needs_decision", "invented"} {
		for _, product := range []string{`{}`, `{"architecture":"a"}`, `{"tasks":["t"]}`, `{"architecture":"a","tasks":["t"]}`, `{"architecture":"a","tasks":[]}`, `{"tasks":"[\"t\"]"}`, `{"unexpected":true}`} {
			for _, sample := range []string{fmt.Sprintf(`{"outcome":%q,"work_product":%s}`, outcome, product), fmt.Sprintf(`{"work_product":%s}`, product)} {
				if generationTestAccepts(t, canonical, []byte(sample)) != generationTestAccepts(t, lowered, []byte(sample)) {
					t.Fatalf("acceptance changed: %s", sample)
				}
			}
		}
	}
}

func TestGenerationNumericEnumPreservesSmallRange(t *testing.T) {
	source := json.RawMessage(`{"type":"integer","minimum":0,"maximum":40}`)
	generation, err := generationSchema(source)
	if err != nil {
		t.Fatal(err)
	}
	var node struct {
		Enum []int `json:"enum"`
	}
	if err := json.Unmarshal(generation, &node); err != nil || len(node.Enum) != 41 || node.Enum[0] != 0 || node.Enum[40] != 40 {
		t.Fatalf("range not enumerated: %s", generation)
	}
	canonical, lowered := compileGenerationTestSchema(t, source), compileGenerationTestSchema(t, generation)
	for value := -2; value <= 45; value++ {
		raw := []byte(fmt.Sprint(value))
		if generationTestAccepts(t, canonical, raw) != generationTestAccepts(t, lowered, raw) {
			t.Fatalf("range changed at %d", value)
		}
	}
	for _, raw := range []string{`"2"`, `2.5`, `null`} {
		if generationTestAccepts(t, lowered, []byte(raw)) {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestGenerationOptionalNumericConditionPreservesAbsence(t *testing.T) {
	source := json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"after_lines":{"type":"integer","minimum":0,"maximum":40},"result_mode":{"enum":["lines","filenames","count"]}},"if":{"required":["after_lines"],"properties":{"after_lines":{"minimum":1}}},"then":{"properties":{"result_mode":{"const":"lines"}}}}`)
	generation, err := generationSchema(source)
	if err != nil {
		t.Fatal(err)
	}
	canonical, lowered := compileGenerationTestSchema(t, source), compileGenerationTestSchema(t, generation)
	for _, mode := range []string{"", "lines", "filenames", "count", "invented"} {
		for after := -1; after <= 42; after++ {
			fields := map[string]any{}
			if mode != "" {
				fields["result_mode"] = mode
			}
			if after != -1 {
				fields["after_lines"] = after
			}
			raw, _ := json.Marshal(fields)
			if generationTestAccepts(t, canonical, raw) != generationTestAccepts(t, lowered, raw) {
				t.Fatalf("optional condition changed: %s", raw)
			}
		}
	}
}

func TestGenerationDoesNotTransformInstanceData(t *testing.T) {
	source := json.RawMessage(`{"type":"object","const":{"if":{"type":"integer","minimum":0,"maximum":5},"then":true},"examples":[{"if":{},"then":false}]}`)
	generation, err := generationSchema(source)
	if err != nil {
		t.Fatal(err)
	}
	var before, after any
	_ = json.Unmarshal(source, &before)
	_ = json.Unmarshal(generation, &after)
	beforeRaw, _ := json.Marshal(before)
	afterRaw, _ := json.Marshal(after)
	if !bytes.Equal(beforeRaw, afterRaw) {
		t.Fatalf("instance data changed: %s", generation)
	}
}

func TestGenerationUnsupportedConditionFailsExplicitly(t *testing.T) {
	for _, source := range []string{
		`{"type":"object","additionalProperties":false,"properties":{"value":{"type":"string"}},"if":{"properties":{"value":{"const":"ready"}}},"then":{"required":["value"]}}`,
		`{"type":"object","additionalProperties":false,"properties":{"value":{"enum":["ready"]}},"if":{"not":{"required":["value"]}},"then":{"required":["value"]}}`,
		`{"type":"object","additionalProperties":false,"properties":{"value":{"enum":["ready","other"]}},"if":{"properties":{"value":{"const":"ready"}}},"then":{"properties":{"undeclared":{"const":true}}}}`,
	} {
		if _, err := generationSchema([]byte(source)); err == nil {
			t.Fatalf("silently relaxed %s", source)
		}
	}
}

func TestStructuredTurnKeepsCanonicalValidatorAfterGenerationCompilation(t *testing.T) {
	contract, err := NewStructuredTurnContract([]ToolDefinition{{Name: "submit_result", Parameters: json.RawMessage(conditionalResultFixture)}}, "submit_result")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(contract.schema, []byte(`"if"`)) {
		t.Fatalf("conditional remained on wire: %s", contract.schema)
	}
	for _, raw := range []string{`{"submit_result":{"outcome":"completed","work_product":{"architecture":"a"}}}`, `{"submit_result":{"outcome":"completed","work_product":{"architecture":"a","tasks":"[\"t\"]"}}}`} {
		if _, err := contract.decode([]byte(raw), "bad"); !errors.Is(err, ErrInvalidTurn) {
			t.Fatalf("canonical validation weakened: %s %v", raw, err)
		}
	}
	for _, raw := range []string{`{"submit_result":{"outcome":"completed","work_product":{"architecture":"a","tasks":["t"]}}}`, `{"submit_result":{"outcome":"blocked","work_product":{}}}`} {
		if _, err := contract.decode([]byte(raw), "good"); err != nil {
			t.Fatal(err)
		}
	}
}
