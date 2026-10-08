package operationalruntime

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tekroo-ai/teams/organization"
)

func TestPlanningImpactCoverageReachesSelectedWorkflowInstruction(t *testing.T) {
	feature := organization.FeatureRequest{
		Refinement:    &organization.FeatureRefinement{},
		Specification: &organization.FeatureSpecification{},
		Design:        &organization.FeatureDesignCandidate{},
	}
	before := mustJSON(feature)
	for _, stage := range []featurePlanningStage{stageArchitecture, stagePlanFinalization} {
		t.Run(string(stage), func(t *testing.T) {
			// Exercise the shared builder used by signed workflow instructions
			// and by both initial and successor architecture descriptions.
			instruction := "IMMUTABLE_WORKFLOW_COMPLETION_PREFIX"
			description, err := featurePlanningDescription(feature, stage, instruction, softwareDevelopmentTaskRoutingPolicy())
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(description, instruction) || strings.Count(description, "INTERFACE_IMPACT_REVIEW:") != 1 {
				t.Fatal("lost workflow binding or duplicated planning checks")
			}
			for _, required := range []string{"production implementers", "construction/wiring", "callers", "test doubles", "write_scope", "verification"} {
				if !strings.Contains(description, required) {
					t.Fatalf("missing impact check %q", required)
				}
			}
			if stage == stageArchitecture && (!strings.Contains(description, "one coherent owning task") || !strings.Contains(description, "do not add undeclared result fields")) {
				t.Fatal("impact guidance invites task proliferation or schema changes")
			}
			if stage == stagePlanFinalization && (!strings.Contains(description, "bounded design-revision path") || !strings.Contains(description, "Do not silently rewrite")) {
				t.Fatal("PM may silently change immutable design")
			}
			parts := strings.Split(description, "AUTHORITATIVE_FEATURE_STATE_JSON:\n")
			if len(parts) != 2 || !json.Valid([]byte(parts[1])) {
				t.Fatal("guidance corrupted the authoritative state object")
			}
		})
	}
	if string(before) != string(mustJSON(feature)) {
		t.Fatal("planning review changed the accepted feature")
	}
	for _, stage := range []featurePlanningStage{stageRefinement, stageSpecification, stageArchitectureReview, stageArchitectureTaskReview} {
		if planningImpactCoverageGuidance(stage) != "" {
			t.Fatalf("impact review leaked into unrelated stage %s", stage)
		}
	}
}
