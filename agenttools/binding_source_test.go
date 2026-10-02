package agenttools

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

type fixedRoles struct {
	grounding application.RoleExecutionGrounding
}

func (roles fixedRoles) ResolveRoleGrounding(_ context.Context, actor kernel.ActorFQN) (application.RoleExecutionGrounding, error) {
	if actor != roles.grounding.ActorFQN {
		return application.RoleExecutionGrounding{}, ErrStaleBinding
	}
	return roles.grounding, nil
}

type fixedWorkspace struct{ binding WorkspaceBinding }

func (workspace fixedWorkspace) ResolveToolWorkspace(context.Context, kernel.TaskOperationalScope) (WorkspaceBinding, error) {
	return workspace.binding, nil
}

func TestExecutionBindingSourceReconstructsStartedTeamsAuthority(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	current, grounding := validStartedContext(t, now)
	root := t.TempDir()
	source := ExecutionBindingSource{
		Reader: staleReader{current: current}, Roles: fixedRoles{grounding: grounding},
		Workspaces:   fixedWorkspace{binding: WorkspaceBinding{WorkspaceID: current.Scope.WorkspaceID, WorktreeID: current.Scope.WorktreeID, Root: root}},
		MaximumBytes: 1 << 20, Now: func() time.Time { return now },
	}
	authority, err := source.BindToolInvocation(context.Background(), current.Invocation.ID, *current.Invocation.RequestDigest)
	if err != nil || authority.WorkspaceRoot != root || !permitted(authority.Permissions, "repository.read") {
		t.Fatalf("valid started invocation did not bind: %+v, %v", authority, err)
	}
	wrong := bindingDigest('0')
	if _, err := source.BindToolInvocation(context.Background(), current.Invocation.ID, wrong); err != ErrStaleBinding {
		t.Fatalf("wrong request digest accepted: %v", err)
	}
	source.Workspaces = fixedWorkspace{binding: WorkspaceBinding{WorkspaceID: "another-workspace", WorktreeID: current.Scope.WorktreeID, Root: root}}
	if _, err := source.BindToolInvocation(context.Background(), current.Invocation.ID, *current.Invocation.RequestDigest); err != ErrStaleBinding {
		t.Fatalf("workspace rebinding accepted: %v", err)
	}
	source.Workspaces = fixedWorkspace{binding: WorkspaceBinding{WorkspaceID: current.Scope.WorkspaceID, WorktreeID: current.Scope.WorktreeID, Root: root}}
	claimed := current
	claimed.Invocation.State = kernel.InvocationClaimed
	claimed.Invocation.Revision = 2
	claimed.Invocation.ConversationID = nil
	claimed.Invocation.RequestDigest = nil
	claimed.Invocation.StartedAt = nil
	claimed.Invocation.LastEventID = bindingUUID(122)
	if err := claimed.Validate(now); err != nil {
		t.Fatalf("claimed fixture invalid: %v", err)
	}
	source.Reader = staleReader{current: claimed}
	if _, err := source.BindToolInvocation(context.Background(), claimed.Invocation.ID, *current.Invocation.RequestDigest); err != nil {
		t.Fatalf("valid claimed invocation could not serve an early read: %v", err)
	}
	if _, err := source.BindToolInvocation(context.Background(), claimed.Invocation.ID, wrong); err != ErrStaleBinding {
		t.Fatalf("wrong claimed request digest accepted: %v", err)
	}
	claimed.Invocation.State = kernel.InvocationFailed
	source.Reader = staleReader{current: claimed}
	if _, err := source.BindToolInvocation(context.Background(), claimed.Invocation.ID, *current.Invocation.RequestDigest); err != ErrStaleBinding {
		t.Fatalf("terminal invocation retained read authority: %v", err)
	}
}

func validStartedContext(t *testing.T, now time.Time) (application.OperationalExecutionContext, application.RoleExecutionGrounding) {
	t.Helper()
	taskID, budgetID, invocationID := bindingUUID(101), bindingUUID(102), bindingUUID(103)
	actor := kernel.ActorFQN("teams::coder-1")
	execution := kernel.ExecutionTuple{ExecutionID: bindingUUID(104), FencingEpoch: 3}
	evidenceID, assignmentEvidence, scopeEvidence := bindingUUID(105), bindingUUID(107), bindingUUID(113)
	purposes := make(kernel.PurposeCounters, len(kernel.AllWorkPurposes))
	used := make(kernel.PurposeCounters, len(kernel.AllWorkPurposes))
	for _, purpose := range kernel.AllWorkPurposes {
		purposes[purpose] = 2
		used[purpose] = 0
	}
	used[kernel.PurposeImplementation] = 1
	profile := kernel.WorkRiskProfile{
		TaskID: taskID, ProfileID: bindingUUID(106), ProfileRevision: 1, ProfileDigest: bindingDigest('a'),
		LifecycleEpoch: 1, ScopeRevision: 1, WorkKind: kernel.WorkImplementation,
		Ambiguity: kernel.AmbiguityLow, Novelty: kernel.NoveltyRoutine, BlastRadius: kernel.BlastLocal,
		SecuritySensitivity: kernel.SecurityOrdinary, MinimumDecisionRoute: kernel.RouteBoundedExecution,
		AcceptanceCriteriaDigest: bindingDigest('b'), RequiredDeterministicGateIDs: []string{"go-test"},
		RequiredValidationBranches: 1, RequiredIndependenceDimensions: []kernel.IndependenceDimension{kernel.IndependenceActor},
		ImplementationVariantCount: 1, ValidCandidateQuorum: 1, VerificationTopologyDigest: bindingDigest('c'),
		ClassificationPolicyRevision: 1, ClassificationPolicyDigest: bindingDigest('d'),
		PromotionPolicyRevision: 1, PromotionPolicyDigest: bindingDigest('e'),
		Budgets:                   kernel.FiniteWorkBudgets{AttemptLimit: 2, ReviewRoundLimit: 2, PromotionLimit: 1, EscalationLimit: 1, DeadlineAt: now.Add(time.Hour)},
		ClassificationAuthority:   kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "operator"},
		ClassificationEvidenceIDs: []kernel.UUIDv7{evidenceID},
	}
	assignment := kernel.QualifiedAssignmentAuthorization{
		AssignmentID: bindingUUID(108), TaskID: taskID, ExpectedTaskRevision: 4,
		WorkProfile: profile.Binding(), RequiredDecisionRoute: kernel.RouteBoundedExecution,
		SelectedDecisionRoute: kernel.RouteBoundedExecution, SelectedActorFQN: actor,
		SelectedExecutionID: execution.ExecutionID, SelectedFencingEpoch: execution.FencingEpoch,
		ModelProfileDigest: bindingDigest('f'), RuntimeIdentityDigest: bindingDigest('1'),
		Qualification: kernel.AssignmentQualificationReceipt{
			QualificationID: bindingUUID(109), QualificationDigest: bindingDigest('2'),
			QualificationCorpusDigest: bindingDigest('3'), ModelProfileDigest: bindingDigest('f'),
			DecisionRoute: kernel.RouteBoundedExecution, QualifiedRole: "coder", Status: kernel.QualificationPass, ObservedAt: now,
		},
		SelectionPolicyRevision: 1, SelectionPolicyDigest: bindingDigest('4'),
		HardConstraintResults: []kernel.HardConstraintResult{{ConstraintID: "runtime", Outcome: kernel.ConstraintPass, EvidenceIDs: []kernel.UUIDv7{assignmentEvidence}}},
		SelectionReasons:      []string{"qualified exact runtime"}, EvidenceIDs: []kernel.UUIDv7{assignmentEvidence},
		AuthorizationEventID: bindingUUID(110),
	}
	authorizationEvent, parentEvent := bindingUUID(111), bindingUUID(112)
	invocation := kernel.WorkInvocation{
		ID: invocationID, Revision: 1, State: kernel.InvocationAuthorized,
		AuthorizationEventID: authorizationEvent, ParentEventID: parentEvent,
		TaskID: taskID, BudgetAccountID: budgetID, LifecycleEpoch: 1, ScopeRevision: 1, TaskRevision: 5,
		WorkProfile: profile.Binding(), QualifiedAssignmentID: assignment.AssignmentID,
		Purpose: kernel.PurposeImplementation, AttemptFamily: "implementation", AttemptOrdinal: 1,
		ConditionDigest: bindingDigest('5'), OutputPredicateDigest: bindingDigest('6'),
		AllowedTerminalOutcomes: []kernel.WorkInvocationState{kernel.InvocationSucceeded, kernel.InvocationFailed, kernel.InvocationTimedOut, kernel.InvocationCancelled, kernel.InvocationStartFailed},
		ToolPolicyDigest:        bindingDigest('7'), EffectPolicyDigest: bindingDigest('8'), ActorFQN: actor, Execution: execution,
		ModelProfileDigest: assignment.ModelProfileDigest, RuntimeIdentityDigest: assignment.RuntimeIdentityDigest,
		WorkspaceID: "workspace-task-101", DeadlineAt: now.Add(time.Hour), IdempotencyKey: "invocation-103",
		AdmissionPolicyRevision: 1, AdmissionPolicyDigest: bindingDigest('9'), GlobalDebitOrdinal: 1, PurposeDebitOrdinal: 1,
		RemainingGlobalBudget: 1, RemainingPurposeBudget: 1, LastEventID: authorizationEvent,
	}
	current := application.OperationalExecutionContext{
		Invocation: invocation,
		Task:       kernel.AggregateState{Kind: kernel.AggregateTask, ID: taskID, Revision: 5, LifecycleEpoch: 1, ScopeRevision: 1, Phase: kernel.PhaseActive, Condition: kernel.ConditionRunnable, Ownership: kernel.Ownership{OwnerFQN: &actor, OwnershipVersion: 1}},
		Budget:     kernel.WorkBudgetAccount{ID: budgetID, Revision: 1, RootWork: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: taskID}, LifecycleEpoch: 1, PolicyRevision: 1, PolicyDigest: bindingDigest('9'), ModelInvocationLimit: 2, ModelInvocationsUsed: 1, PurposeLimits: purposes.Clone(), PurposeUsed: used.Clone(), DeadlineAt: now.Add(time.Hour), LastEventID: bindingUUID(114)},
		TaskBudget: kernel.TaskWorkBudgetBinding{TaskID: taskID, BudgetAccountID: budgetID, TaskRevision: 2, LifecycleEpoch: 1, ScopeRevision: 1, ModelInvocationLimit: 2, ModelInvocationsUsed: 1, PurposeLimits: purposes.Clone(), PurposeUsed: used.Clone(), BoundEventID: bindingUUID(115)},
		Scope:      kernel.TaskOperationalScope{TaskID: taskID, TaskRevision: 3, LifecycleEpoch: 1, ScopeRevision: 1, OwnerFQN: actor, Execution: execution, WorkspaceID: "workspace-task-101", WorktreeID: "worktree-task-101", Branch: "task/101", BaselineSHA: "1111111111111111111111111111111111111111", WritablePaths: []string{"src"}, InterfaceEvidenceIDs: []kernel.UUIDv7{scopeEvidence}, BoundEventID: bindingUUID(116)},
		Profile:    kernel.WorkProfileSnapshot{Profile: profile, BoundEventID: bindingUUID(117), TaskRevision: 4},
		Assignment: assignment, CurrentExecution: execution,
		Specification:          application.TaskExecutionSpecification{TaskID: taskID, CreatedEventID: bindingUUID(118), SourceDigest: bindingDigest('a'), StoryID: bindingUUID(119), Title: "Bounded feature", Description: "Implement the bounded feature.", AcceptanceCriteria: []string{"The test passes."}, DependsOn: []kernel.UUIDv7{}},
		Evidence:               []kernel.EvidenceRef{{EvidenceID: evidenceID, SHA256: bindingDigest('b')}, {EvidenceID: assignmentEvidence, SHA256: bindingDigest('c')}, {EvidenceID: scopeEvidence, SHA256: bindingDigest('d')}},
		AuthorizationEventSeen: true, ParentEventSeen: true,
	}
	fqrn, err := kernel.RoleFQRNFromActor(actor)
	if err != nil {
		t.Fatal(err)
	}
	grounding := application.RoleExecutionGrounding{ActorFQN: actor, RoleFQRN: fqrn, BundleVersion: "1.0.0", BundleDigest: bindingDigest('b'), Capabilities: []string{"implement"}, Permissions: []string{"repository.read"}, Instructions: "Read assigned source and return evidence."}
	_, digest, err := application.BuildExecutionBrief(current, grounding, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	claimID, conversationID, startedAt := bindingUUID(120), "conversation-103", now
	current.Invocation.State = kernel.InvocationStarted
	current.Invocation.Revision = 3
	current.Invocation.ClaimID = &claimID
	current.Invocation.ClaimedAt = &startedAt
	current.Invocation.ConversationID = &conversationID
	current.Invocation.RequestDigest = &digest
	current.Invocation.StartedAt = &startedAt
	current.Invocation.LastEventID = bindingUUID(121)
	if err := current.Validate(now); err != nil {
		t.Fatalf("binding fixture invalid: %v", err)
	}
	return current, grounding
}

func bindingUUID(ordinal int) kernel.UUIDv7 {
	return kernel.UUIDv7(fmt.Sprintf("00000000-0000-7000-8000-%012d", ordinal))
}

func bindingDigest(value byte) kernel.Digest {
	return kernel.Digest(strings.Repeat(string(value), 64))
}
