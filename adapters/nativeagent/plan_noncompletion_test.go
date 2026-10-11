package nativeagent

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

func TestPlanNonCompletionDeltaPreservesDispositionAgainstSignedSchema(t *testing.T) {
	for _, fixture := range signedPlanFixtures(t) {
		t.Run(fixture.name, func(t *testing.T) {
			testPlanNonCompletionDelta(t, fixture.handler)
		})
	}
}

func testPlanNonCompletionDelta(t *testing.T, handler application.MessageHandlerGrounding) {
	signed := handler.ResultSchema
	source := []byte(application.OrganizationalResultMarker + `{"work_product":{"result_type":"FEATURE_PLAN","tasks":[{"purpose":"IMPLEMENTATION","depends_on":[]}]}}`)
	sum := sha256.Sum256(source)
	plan := &PlanFinalizationBinding{SourceOutput: source, SourceDigest: kernel.Digest(fmt.Sprintf("%x", sum))}
	model, err := modelFacingHandlerSchema(handler, kernel.PurposeHandoff)
	if err != nil {
		t.Fatal(err)
	}
	model, err = modelFacingPlanSchema(model, plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agentruntime.NewStructuredTurnContract([]agentruntime.ToolDefinition{{Name: "submit_result", Parameters: model}}, "submit_result"); err != nil {
		t.Fatal("strict generation contract cannot be compiled", err)
	}
	for _, outcome := range []string{"needs_decision", "blocked", "failed"} {
		t.Run(outcome, func(t *testing.T) {
			// This is the transport shape actually required of the r56 PM.
			raw := json.RawMessage(`{"outcome":"` + outcome + `","summary":"Duplicate placeholder task requires architect resolution","evidence":[],"message_proposals":[],"work_product":{"task_dependencies":[],"handoffs":[]}}`)
			if !schemaAccepts(t, model, raw) {
				t.Fatal("model contract rejects non-completion")
			}
			bound, err := bindHandlerPlanResult(bindHandlerResultVersion(raw, handler), plan)
			if err != nil {
				t.Fatal(err)
			}
			if !schemaAccepts(t, signed, bound) {
				t.Fatalf("model-admitted disposition violates signed contract: %s", bound)
			}
			var result struct {
				Outcome     string
				Summary     string
				WorkProduct map[string]json.RawMessage `json:"work_product"`
			}
			if json.Unmarshal(bound, &result) != nil || result.Outcome != outcome || result.Summary != "Duplicate placeholder task requires architect resolution" || len(result.WorkProduct) != 0 {
				t.Fatalf("decision became an executable plan or lost its reason: %s", bound)
			}
			for _, work := range []string{
				`{"task_dependencies":[{"task_index":0,"depends_on":[]}],"handoffs":[]}`,
				`{"task_dependencies":[],"handoffs":[{}]}`,
				`{"task_dependencies":[],"handoffs":[],"tasks":[]}`,
				`{"task_dependencies":null,"handoffs":[]}`,
			} {
				bad := json.RawMessage(`{"outcome":"` + outcome + `","summary":"decision","evidence":[],"message_proposals":[],"work_product":` + work + `}`)
				if schemaAccepts(t, model, bad) {
					t.Fatalf("non-completion may not author a plan: %s", bad)
				}
				if _, err := bindHandlerPlanResult(bad, plan); err == nil {
					t.Fatalf("non-completed delta silently discarded: %s", bad)
				}
			}
		})
	}
	if string(plan.SourceOutput) != string(source) {
		t.Fatal("source proposal changed")
	}
}
