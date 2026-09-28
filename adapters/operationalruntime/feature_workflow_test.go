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
	definition, err := organization.LoadWorkflowDefinition(path, kernel.Digest("2578a9e5e1c2b3da2874775bb97897e5b6593c76442d805579f465a4c25d4e0a"))
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
		// The completion contract must describe the task work_product (either by
		// pointing at the canonical handler result schema or enumerating the
		// task-specific work_product) and must not dictate the outer envelope.
		if strings.Contains(description, "Return exactly TEKROO_ORGANIZATIONAL_RESULT") || (!strings.Contains(description, "result schema") && !strings.Contains(description, "task-specific work_product")) {
			t.Fatalf("stage %s conflates task output with the selected handler envelope: %q", expected.stage, description)
		}
		if expected.stage == stageSpecification && !strings.Contains(description, "appears verbatim") {
			t.Fatalf("specification stage omitted deterministic acceptance-criteria traceability: %q", description)
		}
		if expected.stage == stageArchitecture && (!strings.Contains(description, "story_index is one zero-based integer") || !strings.Contains(description, "depends_on and validates are zero-based integer arrays")) {
			t.Fatalf("architecture stage omitted the scalar story-index and array dependency contract: %q", description)
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

func TestSpecificationCorrectionAuthorizesOnlyFreshSuccessorArchitectureRound(t *testing.T) {
	featureID := deterministicOperationalUUID("workflow-test-feature", "corrected")
	admittedID := workflowTestInvocation("admitted").ID
	admission := kernel.WorkAdmissionResult{AuthorizedInvocationID: &admittedID}
	feature := organization.FeatureRequest{ID: featureID, SpecificationCorrection: &organization.FeatureSpecificationCorrection{ArchitectureRound: 1}}
	successor := workflowTestInvocation("corrected-successor")
	successor.TaskID = featurePlanningTaskID(featureID, stageArchitecture, 1, nil)
	successor.Purpose = kernel.PurposeHandoff
	successor.AttemptOrdinal = 1
	if !workflowStageInvocationAccepted(feature, admission, successor, kernel.Snapshot{}) {
		t.Fatal("correction successor was detached from the admitted design stage")
	}
	successor.AttemptOrdinal = 2
	if workflowStageInvocationAccepted(feature, admission, successor, kernel.Snapshot{}) {
		t.Fatal("unrelated second attempt was accepted as the correction successor")
	}
	successor.AttemptOrdinal = 1
	feature.SpecificationCorrection = nil
	if workflowStageInvocationAccepted(feature, admission, successor, kernel.Snapshot{}) {
		t.Fatal("successor was accepted without an operator correction")
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
