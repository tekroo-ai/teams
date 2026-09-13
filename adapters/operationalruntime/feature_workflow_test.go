package operationalruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestWorkflowPlanningStageDefinitionComesFromConfiguredWorkflow(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(filepath.Dir(workingDirectory)), "config", "workflows", "software-development.v1.json")
	definition, err := organization.LoadWorkflowDefinition(path, kernel.Digest("0fad72aa1ffd2a5a671a610165d8686fa68331f8206627874bf6ce3628b4bfa6"))
	if err != nil {
		t.Fatal(err)
	}
	library := organization.NewWorkflowLibrary()
	if err := library.Add(definition); err != nil {
		t.Fatal(err)
	}
	service := &ProductionService{WorkflowLibrary: library}

	for _, expected := range []struct {
		stage featurePlanningStage
		role  string
		id    string
	}{
		{stage: stageRefinement, role: "product-owner", id: "refine"},
		{stage: stageSpecification, role: "project-manager", id: "specify"},
		{stage: stageArchitecture, role: "architect", id: "design"},
	} {
		role, purpose, _, title, description, criteria, err := service.workflowPlanningStageDefinition(expected.stage)
		if err != nil {
			t.Fatal(err)
		}
		if role != expected.role || purpose != kernel.PurposeHandoff || !strings.HasPrefix(title, expected.id+": ") || len(criteria) != 1 || criteria[0] == "" || !strings.Contains(description, "Completion contract: ") {
			t.Fatalf("stage %s binding role=%s purpose=%s title=%q description=%q criteria=%v", expected.stage, role, purpose, title, description, criteria)
		}
	}
}
