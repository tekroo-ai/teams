package operationalruntime

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestPlanTaskExecutionRefreshRebindsSameActorProcessReplacement(t *testing.T) {
	task, profile, workspace, snapshot := taskExecutionRefreshFixture(t)

	plan, err := planTaskExecutionRefresh(task, profile, workspace, snapshot, taskExecutionRefreshAt())
	if err != nil {
		t.Fatal(err)
	}
	if !plan.assignment || !plan.scope {
		t.Fatalf("refresh plan = %+v, want assignment and scope refresh", plan)
	}

	taskRef := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.plan.ID}
	assignment := snapshot.QualifiedAssignments[taskRef]
	assignment.SelectedExecutionID = task.owner.Execution.ExecutionID
	assignment.SelectedFencingEpoch = task.owner.Execution.FencingEpoch
	snapshot.QualifiedAssignments[taskRef] = assignment
	plan, err = planTaskExecutionRefresh(task, profile, workspace, snapshot, taskExecutionRefreshAt())
	if err != nil {
		t.Fatal(err)
	}
	if plan.assignment || !plan.scope {
		t.Fatalf("partial refresh plan = %+v, want only scope refresh", plan)
	}

	scope := snapshot.TaskOperationalScopes[taskRef]
	scope.Execution = task.owner.Execution
	scope.BaselineSHA = workspace.BaselineSHA
	snapshot.TaskOperationalScopes[taskRef] = scope
	plan, err = planTaskExecutionRefresh(task, profile, workspace, snapshot, taskExecutionRefreshAt())
	if err != nil {
		t.Fatal(err)
	}
	if plan.assignment || plan.scope {
		t.Fatalf("current bindings produced refresh plan %+v", plan)
	}
}

func TestPlanTaskExecutionRefreshRejectsActorOrWorkspaceSubstitution(t *testing.T) {
	task, profile, workspace, snapshot := taskExecutionRefreshFixture(t)

	other := *task
	other.owner.ActorFQN = "teams::coder-2"
	if _, err := planTaskExecutionRefresh(&other, profile, workspace, snapshot, taskExecutionRefreshAt()); !errors.Is(err, organization.ErrInvalidFeature) {
		t.Fatalf("different FQN error = %v", err)
	}

	changedWorkspace := workspace
	changedWorkspace.WorktreeID = "worktree-coder-2"
	if _, err := planTaskExecutionRefresh(task, profile, changedWorkspace, snapshot, taskExecutionRefreshAt()); !errors.Is(err, organization.ErrInvalidFeature) {
		t.Fatalf("different worktree error = %v", err)
	}

	expandedWorkspace := workspace
	expandedWorkspace.WritablePaths = []string{".", "src"}
	if _, err := planTaskExecutionRefresh(task, profile, expandedWorkspace, snapshot, taskExecutionRefreshAt()); !errors.Is(err, organization.ErrInvalidFeature) {
		t.Fatalf("expanded writable scope error = %v", err)
	}
}

func TestPlanTaskExecutionRefreshRebindsMaintenanceSuccessorWorkProfile(t *testing.T) {
	task, profile, workspace, snapshot := taskExecutionRefreshFixture(t)
	taskRef := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.plan.ID}
	assignment := snapshot.QualifiedAssignments[taskRef]
	assignment.SelectedExecutionID = task.owner.Execution.ExecutionID
	assignment.SelectedFencingEpoch = task.owner.Execution.FencingEpoch
	snapshot.QualifiedAssignments[taskRef] = assignment

	predecessor := task.profile.Clone()
	predecessorID := predecessor.ProfileID
	task.profile.ProfileID = "00000000-0000-7000-8000-000000000121"
	task.profile.ProfileRevision++
	task.profile.SupersedesProfileID = &predecessorID
	task.profile.ProfileDigest = ""
	encoded, err := json.Marshal(task.profile)
	if err != nil {
		t.Fatal(err)
	}
	task.profile.ProfileDigest = digestBytes(encoded)
	snapshot.WorkProfiles[taskRef] = kernel.WorkProfileSnapshot{BoundEventID: "00000000-0000-7000-8000-000000000122", TaskRevision: snapshot.State.Revision, Profile: task.profile}
	snapshot.WorkProfileHistory = map[kernel.UUIDv7]kernel.WorkRiskProfile{predecessor.ProfileID: predecessor, task.profile.ProfileID: task.profile}

	plan, err := planTaskExecutionRefresh(task, profile, workspace, snapshot, taskExecutionRefreshAt())
	if err != nil {
		t.Fatal(err)
	}
	if !plan.assignment {
		t.Fatalf("successor profile refresh plan = %+v, want assignment refresh", plan)
	}

	unrelated := assignment
	unrelated.WorkProfile.ProfileID = "00000000-0000-7000-8000-000000000123"
	snapshot.QualifiedAssignments[taskRef] = unrelated
	if _, err := planTaskExecutionRefresh(task, profile, workspace, snapshot, taskExecutionRefreshAt()); !errors.Is(err, organization.ErrInvalidFeature) {
		t.Fatalf("unrelated profile error = %v", err)
	}
}

func TestWorkInvocationAuthorizationCommandIdentityChangesWithExecution(t *testing.T) {
	featureID := kernel.UUIDv7("00000000-0000-7000-8000-000000000111")
	invocationID := kernel.UUIDv7("00000000-0000-7000-8000-000000000112")
	first := workInvocationAuthorizationCommandID(featureID, invocationID, "00000000-0000-7000-8000-000000000113")
	replacement := workInvocationAuthorizationCommandID(featureID, invocationID, "00000000-0000-7000-8000-000000000114")
	if first == replacement {
		t.Fatalf("process replacement reused authorization command identity %s", first)
	}
	if repeated := workInvocationAuthorizationCommandID(featureID, invocationID, "00000000-0000-7000-8000-000000000114"); repeated != replacement {
		t.Fatalf("same execution command identity = %s, want %s", repeated, replacement)
	}
}

func TestTechnicalRetryBudgetExtensionUsesCommittedBindingAsCheckpoint(t *testing.T) {
	binding := kernel.TaskWorkBudgetBinding{
		ModelInvocationLimit: 3,
		ModelInvocationsUsed: 3,
		PurposeLimits:        kernel.PurposeCounters{kernel.PurposeReplan: 3},
		PurposeUsed:          kernel.PurposeCounters{kernel.PurposeReplan: 3},
	}
	account := kernel.WorkBudgetAccount{
		ModelInvocationLimit: 96,
		PurposeLimits:        kernel.PurposeCounters{kernel.PurposeReplan: 96},
	}

	modelLimit, limits, required, err := technicalRetryBudgetExtension(binding, account, kernel.PurposeReplan)
	if err != nil {
		t.Fatal(err)
	}
	if !required || modelLimit != 4 || limits[kernel.PurposeReplan] != 4 {
		t.Fatalf("extension = model:%d purpose:%d required:%t, want 4, 4, true", modelLimit, limits[kernel.PurposeReplan], required)
	}
	if binding.ModelInvocationLimit != 3 || binding.PurposeLimits[kernel.PurposeReplan] != 3 {
		t.Fatalf("extension mutated durable binding: %+v", binding)
	}

	committed := binding
	committed.ModelInvocationLimit = modelLimit
	committed.PurposeLimits = limits
	modelLimit, limits, required, err = technicalRetryBudgetExtension(committed, account, kernel.PurposeReplan)
	if err != nil {
		t.Fatal(err)
	}
	if required || modelLimit != 4 || limits[kernel.PurposeReplan] != 4 {
		t.Fatalf("committed checkpoint = model:%d purpose:%d required:%t, want 4, 4, false", modelLimit, limits[kernel.PurposeReplan], required)
	}

	exhausted := account
	exhausted.ModelInvocationLimit = 3
	exhausted.PurposeLimits = kernel.PurposeCounters{kernel.PurposeReplan: 3}
	if _, _, _, err := technicalRetryBudgetExtension(binding, exhausted, kernel.PurposeReplan); !errors.Is(err, organization.ErrInvalidFeature) {
		t.Fatalf("exhausted account error = %v", err)
	}
}

func TestTaskRetryBudgetRebindRequiredAcrossLifecycleAndScope(t *testing.T) {
	binding := kernel.TaskWorkBudgetBinding{LifecycleEpoch: 1, ScopeRevision: 1}
	profile := kernel.WorkRiskProfile{LifecycleEpoch: 1, ScopeRevision: 1}
	if taskRetryBudgetRebindRequired(binding, profile, false) {
		t.Fatal("current binding unexpectedly requires rebind")
	}
	if !taskRetryBudgetRebindRequired(binding, profile, true) {
		t.Fatal("capacity extension did not require rebind")
	}
	profile.LifecycleEpoch = 2
	if !taskRetryBudgetRebindRequired(binding, profile, false) {
		t.Fatal("reopened lifecycle did not require rebind")
	}
	profile.LifecycleEpoch = 1
	profile.ScopeRevision = 2
	if !taskRetryBudgetRebindRequired(binding, profile, false) {
		t.Fatal("revised scope did not require rebind")
	}
}

func taskExecutionRefreshFixture(t *testing.T) (*trackedTask, ProductionProfile, ProductionWorkspace, kernel.Snapshot) {
	t.Helper()
	now := taskExecutionRefreshAt()
	taskID := kernel.UUIDv7("00000000-0000-7000-8000-000000000101")
	actor := kernel.ActorFQN("teams::coder-1")
	oldExecution := kernel.ExecutionTuple{ExecutionID: "00000000-0000-7000-8000-000000000102", FencingEpoch: 1}
	newExecution := kernel.ExecutionTuple{ExecutionID: "00000000-0000-7000-8000-000000000103", FencingEpoch: 2}
	evidenceID := kernel.UUIDv7("00000000-0000-7000-8000-000000000104")
	profileBinding := kernel.WorkProfileBinding{
		ProfileID: "00000000-0000-7000-8000-000000000105", ProfileRevision: 1,
		ProfileDigest: repeatedDigest('1'), LifecycleEpoch: 1, ScopeRevision: 1,
	}
	workProfile := kernel.WorkRiskProfile{
		TaskID: taskID, ProfileID: profileBinding.ProfileID, ProfileRevision: profileBinding.ProfileRevision,
		ProfileDigest: profileBinding.ProfileDigest, LifecycleEpoch: 1, ScopeRevision: 1,
		WorkKind: kernel.WorkImplementation, Ambiguity: kernel.AmbiguityLow, Novelty: kernel.NoveltyRoutine,
		BlastRadius: kernel.BlastLocal, SecuritySensitivity: kernel.SecurityOrdinary,
		MinimumDecisionRoute: kernel.RouteBoundedExecution, AcceptanceCriteriaDigest: repeatedDigest('2'),
		RequiredDeterministicGateIDs: []string{"go-test"}, RequiredValidationBranches: 1,
		RequiredIndependenceDimensions: []kernel.IndependenceDimension{kernel.IndependencePrincipal},
		ImplementationVariantCount:     1, ValidCandidateQuorum: 1, VerificationTopologyDigest: repeatedDigest('3'),
		ClassificationPolicyRevision: 1, ClassificationPolicyDigest: repeatedDigest('4'),
		PromotionPolicyRevision: 1, PromotionPolicyDigest: repeatedDigest('5'),
		Budgets:                   kernel.FiniteWorkBudgets{AttemptLimit: 3, ReviewRoundLimit: 1, PromotionLimit: 1, EscalationLimit: 1, DeadlineAt: now.Add(time.Hour)},
		ClassificationAuthority:   kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"},
		ClassificationEvidenceIDs: []kernel.UUIDv7{evidenceID},
	}
	corpus, modelQualification := testQualificationBundle(t, repeatedDigest('8'), "coder", kernel.RouteBoundedExecution, repeatedDigest('a'), []kernel.WorkKind{kernel.WorkImplementation}, now.Add(-time.Hour))
	productionProfile := ProductionProfile{
		ModelProfileDigest: modelQualification.ModelProfileDigest, RoleFQRN: "coder", DecisionRoute: kernel.RouteBoundedExecution, RuntimeIdentityDigest: repeatedDigest('9'),
		ToolPolicyDigest: repeatedDigest('a'), EffectPolicyDigest: repeatedDigest('b'),
		MaximumIterations: 0, QualificationCorpus: corpus, Qualification: modelQualification,
	}
	qualification, qualified := productionProfile.qualificationReceipt()
	if !qualified {
		t.Fatal("fixture model profile is not qualified")
	}
	assignment := kernel.QualifiedAssignmentAuthorization{
		AssignmentID: "00000000-0000-7000-8000-000000000107", TaskID: taskID, ExpectedTaskRevision: 3,
		WorkProfile: profileBinding, RequiredDecisionRoute: kernel.RouteBoundedExecution,
		SelectedDecisionRoute: kernel.RouteBoundedExecution, SelectedActorFQN: actor,
		SelectedExecutionID: oldExecution.ExecutionID, SelectedFencingEpoch: oldExecution.FencingEpoch,
		ModelProfileDigest: productionProfile.ModelProfileDigest, RuntimeIdentityDigest: productionProfile.RuntimeIdentityDigest,
		Qualification: qualification, SelectionPolicyRevision: 1, SelectionPolicyDigest: repeatedDigest('c'),
		HardConstraintResults: []kernel.HardConstraintResult{{ConstraintID: "exact-role-model-workspace", Outcome: kernel.ConstraintPass, EvidenceIDs: []kernel.UUIDv7{evidenceID}}},
		SelectionReasons:      []string{"exact configured role"}, EvidenceIDs: []kernel.UUIDv7{evidenceID},
		AuthorizationEventID: "00000000-0000-7000-8000-000000000108",
	}
	workspace := ProductionWorkspace{
		WorkspaceID: "coder-1", WorktreeID: "worktree-coder-1", WorkingDirectory: "/tmp/coder-1",
		Branch: "tekroo/coder-1", BaselineSHA: "2222222222222222222222222222222222222222", WritablePaths: []string{"."},
	}
	scope := kernel.TaskOperationalScope{
		TaskID: taskID, TaskRevision: 7, LifecycleEpoch: 1, ScopeRevision: 1, OwnerFQN: actor,
		Execution: oldExecution, WorkspaceID: workspace.WorkspaceID, WorktreeID: workspace.WorktreeID,
		Branch: workspace.Branch, BaselineSHA: "1111111111111111111111111111111111111111",
		WritablePaths: []string{"."}, InterfaceEvidenceIDs: []kernel.UUIDv7{evidenceID},
		BoundEventID: "00000000-0000-7000-8000-000000000109",
	}
	state := kernel.AggregateState{
		Kind: kernel.AggregateTask, ID: taskID, Revision: 8, LifecycleEpoch: 1, ScopeRevision: 1,
		Phase: kernel.PhaseActive, Condition: kernel.ConditionRunnable,
		Ownership: kernel.Ownership{OwnerFQN: &actor, OwnershipVersion: 1},
	}
	tracked := &trackedTask{
		plan:     organization.PlannedTask{ID: taskID, Owner: actor, ModelProfile: productionProfile.ModelProfileDigest, DecisionRoute: kernel.RouteBoundedExecution},
		revision: state.Revision, last: "00000000-0000-7000-8000-000000000110", profile: workProfile,
		owner: organization.RoleInstanceState{ActorFQN: actor, Execution: newExecution, WorkspaceID: workspace.WorkspaceID, ModelProfile: productionProfile.ModelProfileDigest},
	}
	taskRef := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: taskID}
	return tracked, productionProfile, workspace, kernel.Snapshot{
		State: &state, Revision: state.Revision,
		CurrentExecutions:     map[kernel.ActorFQN]kernel.ExecutionTuple{actor: newExecution},
		Evidence:              map[kernel.UUIDv7]kernel.EvidenceMetadata{evidenceID: {SHA256: repeatedDigest('d'), Available: true}},
		WorkProfiles:          map[kernel.AggregateRef]kernel.WorkProfileSnapshot{taskRef: {BoundEventID: evidenceID, TaskRevision: 2, Profile: workProfile}},
		QualifiedAssignments:  map[kernel.AggregateRef]kernel.QualifiedAssignmentAuthorization{taskRef: assignment},
		TaskOperationalScopes: map[kernel.AggregateRef]kernel.TaskOperationalScope{taskRef: scope},
	}
}

func taskExecutionRefreshAt() time.Time {
	return time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
}
