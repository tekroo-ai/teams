package operationalruntime

import (
	"context"
	"strings"
	"testing"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/organization"
)

func TestPlanningNonCompletionDoesNotRequireOrAdmitCompletedWork(t *testing.T) {
	service := &ProductionService{}
	for _, stage := range []featurePlanningStage{stageRefinement, stageSpecification, stageArchitecture, stagePlanFinalization} {
		for _, outcome := range []string{"needs_decision", "blocked", "failed"} {
			t.Run(string(stage)+"/"+outcome, func(t *testing.T) {
				output := []byte(application.OrganizationalResultMarker + `{"outcome":"` + outcome + `","summary":"missing interface owner","work_product":{}}`)
				reason, key, reported := planningNonCompletion(stage, output)
				if !reported || key == "" || !strings.Contains(reason, "missing interface owner") {
					t.Fatalf("lost disposition: %q %q %t", reason, key, reported)
				}
				if err := service.validateFeatureStageOutputWithEvidence(context.Background(), organization.FeatureRequest{}, stage, output); err != nil {
					t.Fatalf("decision parsed as completed work: %v", err)
				}
			})
		}
	}
	for _, stage := range []featurePlanningStage{stageArchitectureReview, stageArchitectureTaskReview} {
		if _, _, ok := planningNonCompletion(stage, []byte(application.OrganizationalResultMarker+`{"outcome":"needs_decision","summary":"bypass review"}`)); ok {
			t.Fatal("review verdict bypassed")
		}
	}
	for _, raw := range []string{`{"outcome":"completed","summary":"done"}`, `{"outcome":"needs_decision","summary":" "}`, `{"outcome":"invented","summary":"question"}`} {
		if _, _, ok := planningNonCompletion(stagePlanFinalization, []byte(application.OrganizationalResultMarker+raw)); ok {
			t.Fatal("invalid/completed report treated as non-completion")
		}
	}
}
