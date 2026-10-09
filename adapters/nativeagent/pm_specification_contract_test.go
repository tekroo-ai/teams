package nativeagent

import (
	"os"
	"testing"

	"github.com/tekroo-ai/teams/agentruntime"
)

func TestPM222SpecificationKeepsStrictCompletionAndDecisionContracts(t *testing.T) {
	schema, err := os.ReadFile("../../config/starter-team/roles-v4/project-manager-2.2.2/handlers/feature.refined/result.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agentruntime.NewStructuredTurnContract([]agentruntime.ToolDefinition{{Name: "submit_result", Parameters: schema}}, "submit_result"); err != nil {
		t.Fatal("strict generation contract cannot be compiled", err)
	}
	envelope := func(outcome, work string) []byte {
		return []byte(`{"schema_version":"1.0.0","outcome":"` + outcome + `","summary":"Missing product decision","evidence":[],"message_proposals":[],"work_product":` + work + `}`)
	}
	for _, outcome := range []string{"needs_decision", "blocked", "failed"} {
		if !schemaAccepts(t, schema, envelope(outcome, `{}`)) {
			t.Fatal("decision-only report rejected", outcome)
		}
		if schemaAccepts(t, schema, envelope(outcome, `{"invented_field":true}`)) {
			t.Fatal("strict field set weakened")
		}
		if schemaAccepts(t, schema, envelope(outcome, `{"stories":[{"title":"fake"}]}`)) {
			t.Fatal("malformed partial story accepted")
		}
	}
	if schemaAccepts(t, schema, envelope("completed", `{}`)) || schemaAccepts(t, schema, envelope("completed", `{"schema_version":"1.0.0","result_type":"FEATURE_SPECIFICATION","stories":[]}`)) {
		t.Fatal("completion contract weakened")
	}
	complete := `{"schema_version":"1.0.0","result_type":"FEATURE_SPECIFICATION","stories":[{"title":"Address an agent","description":"Use an admitted alias","acceptance_criteria":["The alias reaches the named actor"],"priority":"NORMAL"}]}`
	if !schemaAccepts(t, schema, envelope("completed", complete)) {
		t.Fatal("valid completed specification rejected")
	}
}
