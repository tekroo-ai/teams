package operationalruntime

import (
	"testing"

	"github.com/tekroo-ai/teams/application"
)

func TestFeatureStageResultsAreStrictAndBounded(t *testing.T) {
	refinement := []byte(application.OrganizationalResultMarker + "\n{\"schema_version\":\"1.0.0\",\"result_type\":\"FEATURE_REFINEMENT\",\"acceptance_criteria\":[\"works\"],\"clarification_questions\":[],\"priority\":\"HIGH\"}")
	if _, err := parseRefinementStageResult(refinement); err != nil {
		t.Fatal(err)
	}
	specification := []byte(application.OrganizationalResultMarker + "\n{\"schema_version\":\"1.0.0\",\"result_type\":\"FEATURE_SPECIFICATION\",\"stories\":[{\"title\":\"Story\",\"description\":\"Deliver it.\",\"acceptance_criteria\":[\"works\"],\"priority\":\"HIGH\"}],\"design_constraints\":[]}")
	if _, err := parseSpecificationStageResult(specification); err != nil {
		t.Fatal(err)
	}
	plan := []byte(application.OrganizationalResultMarker + "\n{\"schema_version\":\"1.0.0\",\"result_type\":\"FEATURE_PLAN\",\"architecture\":\"bounded\",\"design_decisions\":[],\"assumptions\":[],\"tasks\":[{\"story_index\":0,\"title\":\"Implement\",\"description\":\"Implement it.\",\"acceptance_criteria\":[\"works\"],\"depends_on\":[],\"validates\":[],\"role\":\"coder\",\"purpose\":\"IMPLEMENTATION\",\"complexity\":3,\"risk\":\"LOW\",\"critical_path\":true,\"attempt_limit\":2,\"review_round_limit\":2}]}")
	if _, err := parseArchitectureStageResult(plan); err != nil {
		t.Fatal(err)
	}
	invalid := append([]byte(nil), plan...)
	invalid = append(invalid, []byte("{}")...)
	if _, err := parseArchitectureStageResult(invalid); err == nil {
		t.Fatal("trailing JSON was accepted")
	}
}
