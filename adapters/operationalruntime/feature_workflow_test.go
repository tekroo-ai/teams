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
	definition, err := organization.LoadWorkflowDefinition(path, kernel.Digest("1624fb8ba139c3c1e1ebe880cf79cfa1f9edd025b946f9676a2c3f3ab9ea3967"))
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
		if strings.Contains(description, "Return exactly TEKROO_ORGANIZATIONAL_RESULT") || !strings.Contains(description, "task-specific work_product") {
			t.Fatalf("stage %s conflates task output with the selected handler envelope: %q", expected.stage, description)
		}
		if expected.stage == stageSpecification && !strings.Contains(description, "appears verbatim") {
			t.Fatalf("specification stage omitted deterministic acceptance-criteria traceability: %q", description)
		}
	}
}

func TestWorkflowInvocationAdmissionFamilyIncludesChangedConditionRetries(t *testing.T) {
	root := workflowTestInvocation("root")
	retry := workflowTestInvocation("retry")
	retry.RetryOfInvocationID = &root.ID
	secondRetry := workflowTestInvocation("second-retry")
	secondRetry.RetryOfInvocationID = &retry.ID
	invocations := map[kernel.AggregateRef]kernel.WorkInvocation{root.Ref(): root, retry.Ref(): retry, secondRetry.Ref(): secondRetry}

	if !workflowInvocationInAdmissionFamily(secondRetry, root.ID, invocations) {
		t.Fatal("changed-condition retry was detached from admitted workflow stage")
	}
	unrelated := workflowTestInvocation("unrelated")
	if workflowInvocationInAdmissionFamily(unrelated, root.ID, invocations) {
		t.Fatal("unrelated invocation joined admitted workflow stage")
	}
}

func TestPlanningInvocationWaitsWhileRecoveryProfileIsBeingRebound(t *testing.T) {
	invocation := workflowTestInvocation("stale-output")
	taskID := deterministicOperationalUUID("workflow-test-task")
	current := kernel.WorkRiskProfile{ProfileID: deterministicOperationalUUID("workflow-test-profile", "current"), ProfileRevision: invocation.WorkProfile.ProfileRevision + 1, ProfileDigest: kernel.Digest(strings.Repeat("b", 64)), LifecycleEpoch: invocation.WorkProfile.LifecycleEpoch, ScopeRevision: invocation.WorkProfile.ScopeRevision}
	snapshot := kernel.Snapshot{WorkProfiles: map[kernel.AggregateRef]kernel.WorkProfileSnapshot{
		{Kind: kernel.AggregateTask, ID: taskID}: {Profile: current},
	}}
	if planningInvocationUsesCurrentProfile(snapshot, taskID, invocation) {
		t.Fatal("prior output remained eligible during recovery profile rebind")
	}
	snapshot.WorkProfiles[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: taskID}] = kernel.WorkProfileSnapshot{Profile: kernel.WorkRiskProfile{ProfileID: invocation.WorkProfile.ProfileID, ProfileRevision: invocation.WorkProfile.ProfileRevision, ProfileDigest: invocation.WorkProfile.ProfileDigest, LifecycleEpoch: invocation.WorkProfile.LifecycleEpoch, ScopeRevision: invocation.WorkProfile.ScopeRevision}}
	if !planningInvocationUsesCurrentProfile(snapshot, taskID, invocation) {
		t.Fatal("current invocation profile was not accepted")
	}
}

func workflowTestInvocation(label string) kernel.WorkInvocation {
	return kernel.WorkInvocation{
		ID: deterministicOperationalUUID("workflow-test-invocation", label),
		WorkProfile: kernel.WorkProfileBinding{
			ProfileID:       deterministicOperationalUUID("workflow-test-profile", label),
			ProfileRevision: 1,
			ProfileDigest:   kernel.Digest(strings.Repeat("a", 64)),
			LifecycleEpoch:  1,
			ScopeRevision:   1,
		},
	}
}
