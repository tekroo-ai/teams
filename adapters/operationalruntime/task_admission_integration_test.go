//go:build mongo_integration

package operationalruntime

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/executionruntime"
	"github.com/tekroo-ai/teams/adapters/fake"
	"github.com/tekroo-ai/teams/adapters/mongo"
	"github.com/tekroo-ai/teams/adapters/openhands"
	"github.com/tekroo-ai/teams/adapters/protocol"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/contract"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestMaterializeFeaturePlanCreatesExecutableRootTask(t *testing.T) {
	process, uri := startRuntimeMongod(t)
	defer stopRuntimeMongod(process)

	now := time.Now().UTC().Truncate(time.Millisecond)
	policy := integratedPolicy()
	policy.Grants[0].Scope.CommandTypes = append(policy.Grants[0].Scope.CommandTypes, "tekroo.command.work-budget.amend")
	policy.Grants[0].Scope.TargetKinds = append(policy.Grants[0].Scope.TargetKinds, kernel.AggregateWorkBudget)
	policy.Grants[3].Grantee.ID = "example::coder-1"
	policy.Grants[4].Grantee.ID = "example::coder-2"
	policy.Grants[4].Scope.CommandTypes = append(policy.Grants[4].Scope.CommandTypes, "tekroo.command.task.request-completion", "tekroo.command.completion-review.record-result")
	policy.Grants[4].Scope.TargetKinds = append(policy.Grants[4].Scope.TargetKinds, kernel.AggregateCompletionReview)
	policy.Grants[2].Scope.CommandTypes = append(policy.Grants[2].Scope.CommandTypes, "tekroo.command.completion-review.record-result")
	policy.Grants[2].Scope.TargetKinds = append(policy.Grants[2].Scope.TargetKinds, kernel.AggregateCompletionReview, kernel.AggregateReleasePlan)
	policy.Grants[1].Scope.CommandTypes = append(policy.Grants[1].Scope.CommandTypes, "tekroo.command.release-plan.record-qualification", "tekroo.command.release-plan.request-execution")
	policy.Grants[2].Scope.CommandTypes = append(policy.Grants[2].Scope.CommandTypes, "tekroo.command.release-plan.record-result")
	roleCommands := []string{"tekroo.command.task.acquire-ownership", "tekroo.command.task.activate", "tekroo.command.task.request-completion"}
	for index, actor := range []kernel.ActorFQN{"example::product-owner-1", "example::project-manager-1", "example::architect-1", "example::architect-2", "example::senior-coder-1", "example::tester-1", "example::tester-2"} {
		commands := append([]string(nil), roleCommands...)
		targets := []kernel.AggregateKind{kernel.AggregateTask}
		if actor == "example::tester-1" || actor == "example::tester-2" {
			commands = append(commands, "tekroo.command.completion-review.record-result")
			targets = append(targets, kernel.AggregateCompletionReview)
		}
		policy.Grants = append(policy.Grants, kernel.AuthorityGrant{GrantDigest: digestByte(byte('2' + index)), Grantee: kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: string(actor)}, Scope: kernel.AuthorityScope{CommandTypes: commands, TargetKinds: targets, CanReadTarget: true}})
	}
	policy.Grants = append(policy.Grants,
		kernel.AuthorityGrant{GrantDigest: digestByte('6'), Grantee: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"}, Scope: kernel.AuthorityScope{CommandTypes: []string{"tekroo.command.human-participant.bind-profile", "tekroo.command.human-interaction.open"}, TargetKinds: []kernel.AggregateKind{kernel.AggregateHumanParticipant, kernel.AggregateHumanInteraction}, CanReadTarget: true}},
		kernel.AuthorityGrant{GrantDigest: digestByte('7'), Grantee: kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "teams-operational-runtime"}, Scope: kernel.AuthorityScope{CommandTypes: []string{"tekroo.command.human-interaction.record-delivery"}, TargetKinds: []kernel.AggregateKind{kernel.AggregateHumanInteraction}, CanReadTarget: true}},
		kernel.AuthorityGrant{GrantDigest: digestByte('8'), Grantee: kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "teams-admission-policy"}, Scope: kernel.AuthorityScope{CommandTypes: []string{"tekroo.command.human-interaction.close", "tekroo.command.work.block", "tekroo.command.work.unblock"}, TargetKinds: []kernel.AggregateKind{kernel.AggregateHumanInteraction, kernel.AggregateTask, kernel.AggregateStory}, CanReadTarget: true}},
		kernel.AuthorityGrant{GrantDigest: digestByte('9'), Grantee: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "human:alice"}, Scope: kernel.AuthorityScope{CommandTypes: []string{"tekroo.command.human-interaction.respond"}, TargetKinds: []kernel.AggregateKind{kernel.AggregateHumanInteraction}, CanReadTarget: true}},
		kernel.AuthorityGrant{GrantDigest: digestByte('f'), Grantee: kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: "example::human-wait-1"}, Scope: kernel.AuthorityScope{CommandTypes: roleCommands, TargetKinds: []kernel.AggregateKind{kernel.AggregateTask}, CanReadTarget: true}},
	)
	store, err := mongo.Open(contextWithTimeout(t), mongo.Config{
		URI: uri, Database: "tekroo_phase6_task_admission", ContractIdentity: kernel.ContractIdentity,
		ManifestSHA256: phase4ManifestSHA, MigrationLevel: 1, Policy: policy,
		BacklogLimit: 1024, DeliveryPolicyRevision: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeRuntimeStore(t, store)

	catalogue, err := contract.Load(os.DirFS(filepath.Join("..", "..")), "CONTRACTS/tekroo.kernel.contracts/0.10.0")
	if err != nil {
		t.Fatal(err)
	}
	provenance, err := fake.ProvenanceBasis()
	if err != nil {
		t.Fatal(err)
	}
	workspace, baseline, _ := candidateRepository(t)
	serverState := &integratedOpenHands{t: t, conversations: make(map[string]*integratedConversation), delays: make(map[string]time.Duration)}
	server := httptest.NewServer(http.HandlerFunc(serverState.serveHTTP))
	defer server.Close()
	modelDigest := digestByte('2')
	runtimeDigest := digestByte('3')
	toolDigest := digestByte('4')
	effectDigest := digestByte('5')
	modelDigests := []kernel.Digest{digestByte('1'), modelDigest, digestByte('4'), digestByte('5'), digestByte('7'), digestByte('8')}
	executionProfiles := make([]openhands.ExecutionProfile, 0, len(modelDigests))
	productionProfiles := make(map[kernel.Digest]ProductionProfile, len(modelDigests))
	for _, currentModel := range modelDigests {
		route := kernel.RouteBoundedExecution
		if currentModel == digestByte('1') || currentModel == digestByte('7') {
			route = kernel.RouteComplexReasoning
		}
		profile, profileErr := openhands.NewAcceptedExecutionProfile(currentModel, runtimeDigest, toolDigest, effectDigest, 24, "tekroo_phase6_task_admission", "sma_step15_memory")
		if profileErr != nil {
			t.Fatal(profileErr)
		}
		executionProfiles = append(executionProfiles, profile)
		corpus, qualification := testQualificationBundle(t, currentModel, "programmer", route, toolDigest, allTestWorkKinds(), now.Add(-time.Minute))
		productionProfiles[currentModel] = ProductionProfile{ModelProfileDigest: currentModel, RoleFQRN: "programmer", DecisionRoute: route, RuntimeIdentityDigest: runtimeDigest, ToolPolicyDigest: toolDigest, EffectPolicyDigest: effectDigest, MaximumIterations: 24, QualificationCorpus: corpus, Qualification: qualification}
	}
	clock := SystemClock{}
	ids, err := NewUUIDv7Source(clock)
	if err != nil {
		t.Fatal(err)
	}
	workspaceBindings := []openhands.WorkspaceBinding{{WorkspaceID: "coder-1", WorktreeID: "worktree-coder-1", WorkingDirectory: workspace}, {WorkspaceID: "coder-2", WorktreeID: "worktree-coder-2", WorkingDirectory: workspace}, {WorkspaceID: "product-owner-1", WorktreeID: "worktree-product-owner-1", WorkingDirectory: workspace}, {WorkspaceID: "project-manager-1", WorktreeID: "worktree-project-manager-1", WorkingDirectory: workspace}, {WorkspaceID: "architect-1", WorktreeID: "worktree-architect-1", WorkingDirectory: workspace}, {WorkspaceID: "architect-2", WorktreeID: "worktree-architect-2", WorkingDirectory: workspace}, {WorkspaceID: "senior-coder-1", WorktreeID: "worktree-senior-coder-1", WorkingDirectory: workspace}, {WorkspaceID: "tester-1", WorktreeID: "worktree-tester-1", WorkingDirectory: workspace}, {WorkspaceID: "tester-2", WorktreeID: "worktree-tester-2", WorkingDirectory: workspace}}
	workspaceResolver, err := openhands.NewBoundWorkspaceResolver(workspaceBindings)
	if err != nil {
		t.Fatal(err)
	}
	evidenceRoot := t.TempDir()
	t.Cleanup(func() { makeWritableForCleanup(t, evidenceRoot) })
	candidateGates := []ProductionCandidateGate{{GateID: "git-clean", Command: []string{"git", "status", "--porcelain=v1"}, Timeout: "10s"}}
	candidates, err := newCandidateWorkspaceManager(evidenceRoot, "git", time.Minute, candidateGates, workspaceResolver)
	if err != nil {
		t.Fatal(err)
	}
	taskWorkspaces, err := newTaskWorkspaceManagerWithContext(context.Background(), evidenceRoot, "git", time.Minute, workspaceResolver)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := New(contextWithTimeout(t), Config{
		Store: store, Catalogue: catalogue, Clock: clock, IDs: ids,
		OpenHandsBaseURL: server.URL, OpenHandsSessionAPIKey: "step7-session-key",
		HTTPClient: &http.Client{Timeout: time.Second}, WorkspaceBindings: workspaceBindings, WorkspaceResolver: workspaceResolver,
		ExecutionProfiles: executionProfiles, RoleGrounding: testRoleGroundingResolver{}, OpenHandsPollInterval: time.Millisecond,
		OpenHandsMaximumPages: 8, OpenHandsMaximumEvidence: 1 << 20, EvidenceRoot: evidenceRoot,
		ExecutionPolicy: application.OperationalExecutionPolicy{OperationTimeout: time.Second, MaximumBriefBytes: 1 << 20, ConsumerID: "phase6-admission", PolicyRevision: 1, ServiceAuthority: kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "teams-operational-runtime"}, ExpiryAuthority: kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "teams-admission-policy"}, Provenance: provenance},
		EvidencePolicy:  application.CommandEvidenceRecorderPolicy{PolicyRevision: 1, Authority: kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "teams-operational-runtime"}, Provenance: provenance, ProducingVersion: "phase6", RetentionPolicy: "phase6"},
		WorkerPolicy:    executionruntime.Policy{ConsumerID: "phase6-admission", LeaseDuration: 3 * time.Second, ReconciliationInterval: 10 * time.Millisecond, MaximumReconciliations: 2, MaximumConcurrentInvocations: 1, LeaseOperationTimeout: time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(contextWithTimeout(t))

	team := loadStarterTeam(t)
	roleRuntime, err := organization.NewInProcessRuntime(organization.RoleWorkerFunc(func(ctx context.Context, _ organization.StartRoleRequest) error {
		<-ctx.Done()
		return ctx.Err()
	}))
	if err != nil {
		t.Fatal(err)
	}
	roleHost, err := organization.NewHost(team, store, roleRuntime, clock, ids)
	if err != nil {
		t.Fatal(err)
	}
	messageBus, err := organization.NewMessageBus(store, store)
	if err != nil {
		t.Fatal(err)
	}

	service := &ProductionService{
		Store: store, Runtime: runtime, RoleHost: roleHost, MessageBus: messageBus, provenance: provenance, clock: clock, ids: ids,
		requestTimeout:   time.Second,
		planningDeadline: 2 * time.Hour,
		recoveryTimeout:  time.Second, messageMaximumAttempts: 3,
		planning:        ProductionPlanning{PolicyRevision: 1, ClassificationPolicyDigest: digestByte('8'), PromotionPolicyDigest: digestByte('6'), VerificationTopologyDigest: digestByte('d'), SelectionPolicyDigest: digestByte('9'), BudgetPolicyDigest: digestByte('b'), RequiredGateIDs: []string{"git-clean"}, CandidateGates: candidateGates, Deadline: "2h"},
		profilesByModel: productionProfiles,
		workspacesByID: map[string]ProductionWorkspace{
			"coder-1":           {WorkspaceID: "coder-1", WorktreeID: "worktree-coder-1", WorkingDirectory: workspace, Branch: "source", BaselineSHA: baseline, WritablePaths: []string{"."}},
			"coder-2":           {WorkspaceID: "coder-2", WorktreeID: "worktree-coder-2", WorkingDirectory: workspace, Branch: "source", BaselineSHA: baseline, WritablePaths: []string{"."}},
			"product-owner-1":   {WorkspaceID: "product-owner-1", WorktreeID: "worktree-product-owner-1", WorkingDirectory: workspace, Branch: "source", BaselineSHA: baseline, WritablePaths: []string{"."}},
			"project-manager-1": {WorkspaceID: "project-manager-1", WorktreeID: "worktree-project-manager-1", WorkingDirectory: workspace, Branch: "source", BaselineSHA: baseline, WritablePaths: []string{"."}},
			"architect-1":       {WorkspaceID: "architect-1", WorktreeID: "worktree-architect-1", WorkingDirectory: workspace, Branch: "source", BaselineSHA: baseline, WritablePaths: []string{"."}},
			"architect-2":       {WorkspaceID: "architect-2", WorktreeID: "worktree-architect-2", WorkingDirectory: workspace, Branch: "source", BaselineSHA: baseline, WritablePaths: []string{"."}},
			"senior-coder-1":    {WorkspaceID: "senior-coder-1", WorktreeID: "worktree-senior-coder-1", WorkingDirectory: workspace, Branch: "source", BaselineSHA: baseline, WritablePaths: []string{"."}},
			"tester-1":          {WorkspaceID: "tester-1", WorktreeID: "worktree-tester-1", WorkingDirectory: workspace, Branch: "source", BaselineSHA: baseline, WritablePaths: []string{"."}},
			"tester-2":          {WorkspaceID: "tester-2", WorktreeID: "worktree-tester-2", WorkingDirectory: workspace, Branch: "source", BaselineSHA: baseline, WritablePaths: []string{"."}},
		},
		workspaceResolver: workspaceResolver,
		taskWorkspaces:    taskWorkspaces,
		candidates:        candidates,
		serviceAuthority:  kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "teams-operational-runtime"},
		policyAuthority:   kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "teams-admission-policy"},
		operatorIdentity:  protocol.AuthenticatedContext{Principal: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"}},
	}
	features, err := organization.NewFeatureCoordinator(store, roleHost, service, clock, ids)
	if err != nil {
		t.Fatal(err)
	}
	service.Features = features
	service.Releases, err = application.NewReleaseCoordinator(runtime, integratedReleaseProvider{}, application.ReleaseCoordinatorPolicy{OperationTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	exerciseHumanParticipation(t, service, runtime, store, provenance, now)
	feature := organization.FeatureRequest{
		SchemaVersion: organization.FeatureSchemaVersion, ID: kernel.UUIDv7("00000000-0000-7000-8000-000000006010"), Revision: 3, Status: organization.FeatureSpecified,
		Input:       organization.FeatureRequestInput{Team: "example", Title: "Admission integration", Description: "Create one executable root task.", AcceptanceCriteria: []string{"task is executable"}, Priority: organization.PriorityHigh, Repository: "tekroo-ai/teams", WorkspaceID: "engineering", IdempotencyKey: "phase6-admission", MaximumStories: 4, MaximumTasks: 8, MaximumHops: 8},
		SubmittedBy: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"}, OperatorActor: "example::operator-1", ProductOwnerActor: "example::product-owner-1",
		InitialMessageID: kernel.UUIDv7("00000000-0000-7000-8000-000000006014"), LastMessageID: kernel.UUIDv7("00000000-0000-7000-8000-000000006015"), LastStepID: kernel.UUIDv7("00000000-0000-7000-8000-000000006016"), LastHop: 3,
		BudgetAccountID: kernel.UUIDv7("00000000-0000-7000-8000-000000006011"), LifecycleEpoch: 1, ScopeRevision: 1, CreatedAt: now.Add(-time.Minute), UpdatedAt: now,
	}
	plan := organization.FeaturePlan{
		Version: 1, CreatedAt: now, PreparedBy: "example::architect-1", PreparedExecution: kernel.ExecutionTuple{ExecutionID: kernel.UUIDv7("00000000-0000-7000-8000-000000006017"), FencingEpoch: 1}, Architecture: "One bounded implementation task.",
		Stories: []organization.PlannedStory{{ID: kernel.UUIDv7("00000000-0000-7000-8000-000000006012"), Title: "Executable story", Description: "Materialize an admitted task.", AcceptanceCriteria: []string{"root task is active"}, Priority: organization.PriorityHigh}},
		Tasks: []organization.PlannedTask{
			{ID: kernel.UUIDv7("00000000-0000-7000-8000-000000006013"), StoryID: kernel.UUIDv7("00000000-0000-7000-8000-000000006012"), Title: "Implement", Description: "Implement the accepted change.", AcceptanceCriteria: []string{"go test passes"}, Owner: "example::coder-1", ModelProfile: modelDigest, DecisionRoute: kernel.RouteBoundedExecution, Purpose: kernel.PurposeImplementation, Complexity: 3, Risk: organization.RiskLow, CriticalPath: true, AttemptLimit: 2, ReviewRoundLimit: 1},
			{ID: kernel.UUIDv7("00000000-0000-7000-8000-000000006018"), StoryID: kernel.UUIDv7("00000000-0000-7000-8000-000000006012"), Title: "Validate", Description: "Independently validate the accepted change.", AcceptanceCriteria: []string{"validation passes"}, DependsOn: []kernel.UUIDv7{kernel.UUIDv7("00000000-0000-7000-8000-000000006013")}, Validates: []kernel.UUIDv7{kernel.UUIDv7("00000000-0000-7000-8000-000000006013")}, Owner: "example::coder-2", ModelProfile: modelDigest, DecisionRoute: kernel.RouteBoundedExecution, Purpose: kernel.PurposeValidation, Complexity: 2, Risk: organization.RiskLow, CriticalPath: true, AttemptLimit: 2, ReviewRoundLimit: 1},
		},
	}
	runContext, cancelRun := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- runtime.Run(runContext) }()
	runStopped := false
	defer func() {
		if runStopped {
			return
		}
		cancelRun()
		if err := <-runResult; !errors.Is(err, context.Canceled) {
			t.Errorf("runtime stop during cleanup = %v", err)
		}
	}()
	service.suspendNewInvocations = true
	if err := service.MaterializeFeaturePlan(contextWithTimeout(t), feature, plan); err != nil {
		t.Fatal(err)
	}
	if err := service.MaterializeFeaturePlan(contextWithTimeout(t), feature, plan); err != nil {
		t.Fatalf("idempotent materialization: %v", err)
	}
	firstInvocationID := deterministicOperationalUUID("work-invocation", string(feature.ID), string(plan.Tasks[0].ID), string(plan.Tasks[0].Purpose), "1")
	if _, found, err := service.ReadInvocation(contextWithTimeout(t), firstInvocationID); err != nil || found {
		t.Fatalf("suspended admission created root invocation: found=%t err=%v", found, err)
	}
	service.suspendNewInvocations = false
	serverState.mu.Lock()
	serverState.commitResults = true
	serverState.mu.Unlock()
	if err := service.reconcileFeaturePlan(contextWithTimeout(t), feature, plan); err != nil {
		t.Fatalf("resume root-task admission: %v", err)
	}
	waitForInvocationState(t, store, firstInvocationID, kernel.InvocationSucceeded)
	serverState.mu.Lock()
	serverState.commitResults = false
	serverState.mu.Unlock()
	if err := service.reconcileFeaturePlan(contextWithTimeout(t), feature, plan); err != nil {
		snapshot, _ := store.LoadDecision(contextWithTimeout(t), kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: plan.Tasks[1].ID}})
		t.Fatalf("reconcile validation: %v state=%#v", err, snapshot.State)
	}
	waitForTaskInvocationState(t, store, plan.Tasks[1].ID, kernel.PurposeValidation, kernel.InvocationSucceeded)
	if err := service.reconcileFeaturePlan(contextWithTimeout(t), feature, plan); err != nil {
		t.Fatal(err)
	}
	serverState.mu.Lock()
	serverState.failValidations = 1
	serverState.mu.Unlock()
	service.suspendNewInvocations = true
	automated, created := submitAutomatedFeature(t, service)
	if !created {
		t.Fatal("automated feature was not created")
	}
	refinementTaskID := deterministicOperationalUUID("feature-planning-task", string(automated.ID), string(stageRefinement))
	if _, _, exists, readErr := store.ReadAggregateHead(contextWithTimeout(t), kernel.AggregateRef{Kind: kernel.AggregateTask, ID: refinementTaskID}); readErr != nil || exists {
		t.Fatalf("suspended feature submission created refinement task: found=%t err=%v", exists, readErr)
	}
	service.suspendNewInvocations = false
	planningStory, err := service.ensureFeaturePlanningStory(contextWithTimeout(t), automated)
	if err != nil {
		t.Fatal(err)
	}
	priorPolicyRevision := service.provenance.PolicyRevision
	service.provenance.PolicyRevision++
	existingPlanningStory, err := service.ensureFeaturePlanningStory(contextWithTimeout(t), automated)
	service.provenance.PolicyRevision = priorPolicyRevision
	if err != nil || existingPlanningStory != planningStory {
		t.Fatalf("resume existing planning story after policy revision: event=%s want=%s err=%v", existingPlanningStory, planningStory, err)
	}
	planningEvidenceID, planningEvidence, err := service.ensureFeaturePlanningEvidence(contextWithTimeout(t), automated)
	if err != nil {
		t.Fatal(err)
	}
	service.provenance.PolicyRevision++
	existingEvidenceID, existingEvidence, err := service.ensureFeaturePlanningEvidence(contextWithTimeout(t), automated)
	service.provenance.PolicyRevision = priorPolicyRevision
	if err != nil || existingEvidenceID != planningEvidenceID || len(existingEvidence) != 1 || len(planningEvidence) != 1 || existingEvidence[0] != planningEvidence[0] {
		t.Fatalf("resume existing planning evidence after policy revision: id=%s want=%s evidence=%v want_evidence=%v err=%v", existingEvidenceID, planningEvidenceID, existingEvidence, planningEvidence, err)
	}
	if _, err := service.submitPlannedCommand(contextWithTimeout(t), automated, "tekroo.command.task.create", kernel.AggregateTask, refinementTaskID, "planning-task-"+string(stageRefinement), mustJSON(map[string]any{
		"story_id":            deterministicOperationalUUID("feature-planning-story", string(automated.ID)),
		"title":               "Refine feature request",
		"description":         "A prior binary created this durable task with an older planning prompt.",
		"acceptance_criteria": []string{"requirements are testable and ambiguities are explicit"},
		"depends_on":          []kernel.UUIDv7{},
	}), []kernel.DagParent{{ParentEventID: planningStory, EdgeKind: kernel.EdgeCausal}}); err != nil {
		t.Fatal(err)
	}
	service.admissionMu.Lock()
	service.admissionLimitEnabled = true
	service.admissionRemaining = 1
	service.admissionMu.Unlock()
	for _, expected := range []struct {
		stage featurePlanningStage
		next  organization.FeatureStatus
	}{{stageRefinement, organization.FeatureReadyForPlanning}, {stageSpecification, organization.FeatureSpecified}, {stageArchitecture, organization.FeaturePlanned}} {
		if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
			t.Fatalf("reconcile completed %s planning: %v", expected.stage, err)
		}
		taskID := deterministicOperationalUUID("feature-planning-task", string(automated.ID), string(expected.stage))
		purpose := kernel.PurposeHandoff
		if expected.stage == stageArchitecture {
			purpose = kernel.PurposeReplan
		}
		invocationID := deterministicOperationalUUID("work-invocation", string(automated.ID), string(taskID), string(purpose), "1")
		waitForInvocationState(t, store, invocationID, kernel.InvocationSucceeded)
		author := waitForTaskInvocationState(t, store, taskID, purpose, kernel.InvocationSucceeded)
		if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
			t.Fatalf("reconcile successful %s output: %v", expected.stage, err)
		}
		if expected.stage == stageArchitecture {
			beforeReview, found, readErr := service.ReadFeature(contextWithTimeout(t), automated.ID)
			if readErr != nil || !found || beforeReview.Status != organization.FeatureSpecified || beforeReview.Plan != nil {
				t.Fatalf("implementation plan materialized before independent architecture review: feature=%#v found=%t err=%v", beforeReview, found, readErr)
			}
			obsoleteReviewIndex := uint32(0)
			obsoleteReviewID := featurePlanningTaskID(automated.ID, stageArchitectureTaskReview, 0, &obsoleteReviewIndex)
			if _, _, exists, obsoleteErr := store.ReadAggregateHead(contextWithTimeout(t), kernel.AggregateRef{Kind: kernel.AggregateTask, ID: obsoleteReviewID}); obsoleteErr != nil || exists {
				t.Fatalf("obsolete per-task architecture review materialized: found=%t err=%v", exists, obsoleteErr)
			}
			reviewTaskID := deterministicOperationalUUID("feature-planning-task", string(automated.ID), string(stageArchitectureReview))
			reviewer := waitForPlanningInvocationState(t, service, store, reviewTaskID, kernel.PurposeReplan, kernel.InvocationSucceeded)
			if reviewer.ActorFQN == author.ActorFQN || reviewer.ActorFQN != "example::architect-2" {
				t.Fatalf("full-plan reviewer = %s, author = %s", reviewer.ActorFQN, author.ActorFQN)
			}
			if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
				t.Fatal(err)
			}
		}
		var found bool
		automated, found, err = service.ReadFeature(contextWithTimeout(t), automated.ID)
		if err != nil || !found || automated.Status != expected.next {
			t.Fatalf("automated feature stage %s = %#v found=%t err=%v", expected.stage, automated, found, err)
		}
		if expected.stage == stageRefinement {
			if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
				t.Fatalf("reconcile after exhausting planning admission limit: %v", err)
			}
			specificationID := deterministicOperationalUUID("feature-planning-task", string(automated.ID), string(stageSpecification))
			if _, _, exists, readErr := store.ReadAggregateHead(contextWithTimeout(t), kernel.AggregateRef{Kind: kernel.AggregateTask, ID: specificationID}); readErr != nil || exists {
				t.Fatalf("exhausted admission limit created specification task: found=%t err=%v", exists, readErr)
			}
			service.admissionMu.Lock()
			service.admissionLimitEnabled = false
			service.admissionRemaining = 0
			service.admissionMu.Unlock()
		}
	}
	if automated.Plan == nil || len(automated.Plan.Tasks) != 4 || automated.Plan.Tasks[1].Purpose != kernel.PurposeValidation || len(automated.Plan.Tasks[1].Validates) != 1 || automated.Plan.Tasks[1].Validates[0] != automated.Plan.Tasks[0].ID || automated.Plan.Tasks[2].ID != featureValidationTaskID(automated.ID, automated.Plan.Version) || automated.Plan.Tasks[2].Purpose != kernel.PurposeValidation || len(automated.Plan.Tasks[2].Validates) != 1 || automated.Plan.Tasks[2].Validates[0] != automated.Plan.Tasks[0].ID || automated.Plan.Tasks[3].Purpose != kernel.PurposePromotion || automated.Plan.Tasks[3].Owner != automated.ProductOwnerActor {
		t.Fatalf("automated plan = %#v", automated.Plan)
	}
	automatedImplementation := automated.Plan.Tasks[0]
	automatedValidation := automated.Plan.Tasks[1]
	automatedFeatureValidation := automated.Plan.Tasks[2]
	automatedAcceptance := automated.Plan.Tasks[3]
	if !strings.Contains(automatedAcceptance.Description, "same immutable candidate used by validation") || strings.Contains(automatedAcceptance.Description, "branch=task/phase6") || strings.Contains(automatedAcceptance.Description, "locate or switch branches") == false {
		t.Fatalf("acceptance target binding = %q", automatedAcceptance.Description)
	}
	serverState.mu.Lock()
	serverState.commitResults = true
	serverState.mu.Unlock()
	if err := service.reconcileFeaturePlan(contextWithTimeout(t), automated, *automated.Plan); err != nil {
		t.Fatal(err)
	}
	waitForInvocationState(t, store, deterministicOperationalUUID("work-invocation", string(automated.ID), string(automatedImplementation.ID), string(automatedImplementation.Purpose), "1"), kernel.InvocationSucceeded)
	serverState.mu.Lock()
	serverState.commitResults = false
	serverState.mu.Unlock()
	if err := service.reconcileFeaturePlan(contextWithTimeout(t), automated, *automated.Plan); err != nil {
		t.Fatal(err)
	}
	firstValidation := waitForTaskInvocationState(t, store, automatedValidation.ID, kernel.PurposeValidation, kernel.InvocationSucceeded)
	serverState.mu.Lock()
	serverState.commitResults = true
	serverState.mu.Unlock()
	if err := service.reconcileFeaturePlan(contextWithTimeout(t), automated, *automated.Plan); err != nil {
		t.Fatal(err)
	}
	repair := waitForTaskInvocationState(t, store, automatedImplementation.ID, kernel.PurposeRepair, kernel.InvocationSucceeded)
	serverState.mu.Lock()
	serverState.commitResults = false
	serverState.mu.Unlock()
	if repair.AttemptOrdinal != 1 || repair.OutputDigest == nil {
		t.Fatalf("repair invocation = %#v", repair)
	}
	if err := service.reconcileFeaturePlan(contextWithTimeout(t), automated, *automated.Plan); err != nil {
		t.Fatal(err)
	}
	secondValidation := waitForTaskInvocationState(t, store, automatedValidation.ID, kernel.PurposeValidation, kernel.InvocationSucceeded)
	if secondValidation.ID == firstValidation.ID || secondValidation.AttemptOrdinal != 2 || secondValidation.ConditionDigest == firstValidation.ConditionDigest {
		t.Fatalf("validation rounds failed=%#v repaired=%#v", firstValidation, secondValidation)
	}
	if err := service.reconcileFeaturePlan(contextWithTimeout(t), automated, *automated.Plan); err != nil {
		t.Fatal(err)
	}
	automatedState, _, found, err := store.ReadAggregateHead(contextWithTimeout(t), kernel.AggregateRef{Kind: kernel.AggregateTask, ID: automatedImplementation.ID})
	if err != nil || !found || automatedState.Phase != kernel.PhaseCompleted {
		t.Fatalf("automated implementation state=%#v found=%t err=%v", automatedState, found, err)
	}
	if err := service.reconcileFeaturePlan(contextWithTimeout(t), automated, *automated.Plan); err != nil {
		t.Fatal(err)
	}
	waitForTaskInvocationState(t, store, automatedFeatureValidation.ID, kernel.PurposeValidation, kernel.InvocationSucceeded)
	if err := service.reconcileFeaturePlan(contextWithTimeout(t), automated, *automated.Plan); err != nil {
		t.Fatal(err)
	}
	featureValidationState, _, found, err := store.ReadAggregateHead(contextWithTimeout(t), kernel.AggregateRef{Kind: kernel.AggregateTask, ID: automatedFeatureValidation.ID})
	if err != nil || !found || featureValidationState.Phase != kernel.PhaseCompleted {
		t.Fatalf("whole-feature validation state=%#v found=%t err=%v", featureValidationState, found, err)
	}
	if err := service.reconcileFeaturePlan(contextWithTimeout(t), automated, *automated.Plan); err != nil {
		t.Fatal(err)
	}
	waitForTaskInvocationState(t, store, automatedAcceptance.ID, kernel.PurposePromotion, kernel.InvocationSucceeded)
	if err := service.reconcileFeaturePlan(contextWithTimeout(t), automated, *automated.Plan); err != nil {
		t.Fatal(err)
	}
	if err := service.reconcileFeaturePlan(contextWithTimeout(t), automated, *automated.Plan); err != nil {
		t.Fatal(err)
	}
	automated, found, err = service.ReadFeature(contextWithTimeout(t), automated.ID)
	if err != nil || !found || automated.Status != organization.FeatureAwaitingAcceptance || automated.Acceptance == nil || automated.Acceptance.RecommendedBy != automated.ProductOwnerActor || automated.Acceptance.AcceptedBy != nil {
		t.Fatalf("automated acceptance recommendation = %#v found=%t err=%v", automated, found, err)
	}
	release := organization.StoryCodeRelease{StoryID: automated.Plan.Stories[0].ID, RepositoryURL: "file:///tmp/phase6-release.git", BaseRef: "main", BaseCommit: strings.Repeat("1", 40), ExpectedQualifiedTree: strings.Repeat("3", 40), ChangeRef: "refs/heads/phase6-release", HeadCommit: strings.Repeat("2", 40), GitVersion: "git version 2.51.0", GateDefinitionDigest: digestByte('a'), ToolchainDigest: digestByte('b'), DependencyLockDigest: digestByte('c'), QualificationArtifactHash: digestByte('d')}
	automated, err = service.AcceptFeatureWithRelease(contextWithTimeout(t), automated.ID, automated.SubmittedBy, organization.FeatureAcceptanceInput{ExpectedRevision: automated.Revision, Mode: organization.FeatureAcceptanceCode, CodeReleases: []organization.StoryCodeRelease{release}})
	if err != nil || automated.Status != organization.FeatureAccepted || automated.Acceptance == nil || automated.Acceptance.AcceptedBy == nil || len(automated.Acceptance.ReleasePlanIDs) != len(automated.Plan.Stories) {
		t.Fatalf("automated feature acceptance = %#v err=%v", automated, err)
	}
	exerciseArchitectureReviewFailure(t, service, store, serverState, false, 1)
	exerciseArchitectureReviewFailure(t, service, store, serverState, true, 0)
	exerciseArchitectureReviewFailure(t, service, store, serverState, false, 2)
	reviewed := exerciseSingleFullPlanReview(t, service, store)
	exerciseFeatureReplan(t, service, store, serverState, reviewed)
	exerciseInvalidValidatorRecovery(t, service, runtime, store, serverState, provenance, now)
	exerciseParallelDAGAdmission(t, service, store, serverState, modelDigest, digestByte('8'), now)
	cancelRun()
	if err := <-runResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("runtime stop = %v", err)
	}
	runStopped = true
	projectionContext, cancelProjection := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancelProjection()
	if _, err := store.ProjectPendingOperationalEvents(projectionContext, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	automatedStory, found, err := store.ReadStoryProjection(contextWithTimeout(t), automated.Plan.Stories[0].ID)
	if err != nil || !found || automatedStory.Phase != string(kernel.PhaseAccepted) || automatedStory.Release.State != string(kernel.ReleaseReadyForAcceptance) || automatedStory.Acceptance.State != "ACCEPTED" {
		t.Fatalf("automated accepted story = %#v found=%t err=%v", automatedStory, found, err)
	}
	projection, found, err := store.ReadTaskProjection(contextWithTimeout(t), plan.Tasks[0].ID)
	if err != nil || !found || !projection.Valid() || projection.Phase != string(kernel.PhaseCompleted) || projection.OwnerFQN == nil || *projection.OwnerFQN != plan.Tasks[0].Owner || !projection.WorkProfileID.Valid() || projection.QualifiedAssignment == nil || projection.Budget.AccountID != feature.BudgetAccountID || projection.OperationalScope == nil || projection.OperationalScope.WorkspaceID != "coder-1" || projection.LatestInvocation == nil || projection.LatestInvocation.State != kernel.InvocationSucceeded {
		t.Fatalf("projection=%+v found=%t err=%v", projection, found, err)
	}
	dependent, found, err := store.ReadTaskProjection(contextWithTimeout(t), plan.Tasks[1].ID)
	if err != nil || !found || !dependent.Valid() || dependent.Phase != string(kernel.PhaseCompleted) || dependent.LatestInvocation == nil || dependent.LatestInvocation.State != kernel.InvocationSucceeded {
		t.Fatalf("dependent projection=%+v found=%t err=%v", dependent, found, err)
	}
}

func exerciseSingleFullPlanReview(t *testing.T, service *ProductionService, store *mongo.Store) organization.FeatureRequest {
	t.Helper()
	feature, created, err := service.Features.Submit(contextWithTimeout(t), service.operatorIdentity.Principal, organization.FeatureRequestInput{
		IdempotencyKey: "phase9-single-full-plan-review", Team: "example", Title: "Review every planned task",
		Description:        "Prove that one independent complete-plan review covers task feasibility and plan composition.",
		AcceptanceCriteria: []string{"every planned task and the composed plan are independently reviewed exactly once before advancement"}, Priority: organization.PriorityHigh,
		Constraints: []string{"preserve the task dependency order"}, Repository: "tekroo-ai/teams", WorkspaceID: "engineering", MaximumStories: 4, MaximumTasks: 8, MaximumHops: 8,
	})
	if err != nil || !created {
		t.Fatalf("submit full-plan review feature: created=%t err=%v", created, err)
	}
	for _, stage := range []featurePlanningStage{stageRefinement, stageSpecification} {
		if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
			t.Fatal(err)
		}
		taskID := deterministicOperationalUUID("feature-planning-task", string(feature.ID), string(stage))
		waitForTaskInvocationState(t, store, taskID, kernel.PurposeHandoff, kernel.InvocationSucceeded)
		if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
			t.Fatal(err)
		}
		var found bool
		feature, found, err = service.ReadFeature(contextWithTimeout(t), feature.ID)
		if err != nil || !found {
			t.Fatalf("read full-plan-review feature after %s: found=%t err=%v", stage, found, err)
		}
	}
	if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
		t.Fatal(err)
	}
	architectureTaskID := deterministicOperationalUUID("feature-planning-task", string(feature.ID), string(stageArchitecture))
	author := waitForPlanningInvocationState(t, service, store, architectureTaskID, kernel.PurposeReplan, kernel.InvocationSucceeded)
	if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
		t.Fatal(err)
	}
	obsoleteReviewIndex := uint32(0)
	obsoleteReviewID := featurePlanningTaskID(feature.ID, stageArchitectureTaskReview, 0, &obsoleteReviewIndex)
	if _, _, exists, obsoleteErr := store.ReadAggregateHead(contextWithTimeout(t), kernel.AggregateRef{Kind: kernel.AggregateTask, ID: obsoleteReviewID}); obsoleteErr != nil || exists {
		t.Fatalf("obsolete per-task architecture review materialized: found=%t err=%v", exists, obsoleteErr)
	}
	fullReviewID := deterministicOperationalUUID("feature-planning-task", string(feature.ID), string(stageArchitectureReview))
	reviewer := waitForTaskInvocationState(t, store, fullReviewID, kernel.PurposeReplan, kernel.InvocationSucceeded)
	if reviewer.ActorFQN == author.ActorFQN || reviewer.ActorFQN != "example::architect-2" {
		t.Fatalf("full-plan reviewer=%s author=%s", reviewer.ActorFQN, author.ActorFQN)
	}
	if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
		t.Fatal(err)
	}
	planned, found, err := service.ReadFeature(contextWithTimeout(t), feature.ID)
	if err != nil || !found || planned.Status != organization.FeaturePlanned || planned.Plan == nil || len(planned.Plan.Tasks) != 6 {
		t.Fatalf("multi-task feature did not advance after full-plan review: feature=%#v found=%t err=%v", planned, found, err)
	}
	return planned
}

func exerciseFeatureReplan(t *testing.T, service *ProductionService, store *mongo.Store, serverState *integratedOpenHands, feature organization.FeatureRequest) {
	t.Helper()
	if feature.Plan == nil || len(feature.Plan.Tasks) == 0 || len(feature.Plan.Stories) == 0 {
		t.Fatalf("feature has no materialized plan to replace: %#v", feature)
	}
	oldPlan := *feature.Plan
	first := oldPlan.Tasks[0]
	serverState.mu.Lock()
	serverState.commitResults = true
	serverState.mu.Unlock()
	if err := service.reconcileFeaturePlan(contextWithTimeout(t), feature, oldPlan); err != nil {
		t.Fatal(err)
	}
	waitForTaskInvocationState(t, store, first.ID, first.Purpose, kernel.InvocationSucceeded)
	serverState.mu.Lock()
	serverState.commitResults = false
	serverState.mu.Unlock()
	_, evidence, err := service.ensureFeaturePlanningEvidence(contextWithTimeout(t), feature)
	if err != nil {
		t.Fatal(err)
	}
	request := FeatureReplanRequest{
		ExpectedRevision: feature.Revision, ExpectedPlanVersion: oldPlan.Version,
		Reason:         "the materialized plan contains an implementation prescription that cannot compile against the repository API",
		EvidenceRefs:   evidence,
		DeadlineAt:     service.clock.Now().UTC().Add(service.planningDeadline),
		IdempotencyKey: "phase9-explicit-feature-replan",
	}
	corrected, err := service.RequestFeatureReplan(contextWithTimeout(t), service.operatorIdentity.Principal, feature.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	if corrected.Status != organization.FeatureSpecified || corrected.Revision != feature.Revision+1 || corrected.Plan == nil || corrected.Plan.Version != oldPlan.Version || corrected.PlanSupersession == nil || corrected.PlanSupersession.PlanVersion != oldPlan.Version {
		t.Fatalf("corrected feature did not retain the superseded plan: %#v", corrected)
	}
	idempotent, err := service.RequestFeatureReplan(contextWithTimeout(t), service.operatorIdentity.Principal, feature.ID, request)
	if err != nil || idempotent.Revision != corrected.Revision {
		t.Fatalf("idempotent feature correction: revision=%d want=%d err=%v", idempotent.Revision, corrected.Revision, err)
	}
	for _, task := range oldPlan.Tasks {
		state, _, found, readErr := store.ReadAggregateHead(contextWithTimeout(t), kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID})
		if readErr != nil || !found || state.Condition != kernel.ConditionBlocked {
			t.Fatalf("superseded task %s not blocked: state=%#v found=%t err=%v", task.ID, state, found, readErr)
		}
	}
	for _, story := range oldPlan.Stories {
		state, _, found, readErr := store.ReadAggregateHead(contextWithTimeout(t), kernel.AggregateRef{Kind: kernel.AggregateStory, ID: story.ID})
		if readErr != nil || !found || state.Condition != kernel.ConditionBlocked {
			t.Fatalf("superseded story %s not blocked: state=%#v found=%t err=%v", story.ID, state, found, readErr)
		}
	}
	round := corrected.PlanSupersession.ArchitectureRound
	for label, ref := range map[string]kernel.AggregateRef{
		"planning story":        {Kind: kernel.AggregateStory, ID: deterministicOperationalUUID("feature-planning-story", string(feature.ID))},
		"specification task":    {Kind: kernel.AggregateTask, ID: featurePlanningTaskID(feature.ID, stageSpecification, 0, nil)},
		"predecessor architect": {Kind: kernel.AggregateTask, ID: featurePlanningTaskID(feature.ID, stageArchitecture, round-1, nil)},
	} {
		_, _, found, readErr := store.ReadAggregateHead(contextWithTimeout(t), ref)
		if readErr != nil || !found {
			t.Fatalf("%s head unavailable before replacement architecture: ref=%#v found=%t err=%v", label, ref, found, readErr)
		}
	}
	if _, _, found, readErr := store.ReadAggregateRevisionHead(contextWithTimeout(t), kernel.AggregateRef{Kind: kernel.AggregateEvidence, ID: evidence[0].EvidenceID}); readErr != nil || !found {
		t.Fatalf("correction evidence head unavailable before replacement architecture: found=%t err=%v", found, readErr)
	}
	if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
		t.Fatal(err)
	}
	author := waitForPlanningInvocationState(t, service, store, featurePlanningTaskID(feature.ID, stageArchitecture, round, nil), kernel.PurposeReplan, kernel.InvocationSucceeded)
	if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
		t.Fatal(err)
	}
	obsoleteReviewIndex := uint32(0)
	obsoleteReviewID := featurePlanningTaskID(feature.ID, stageArchitectureTaskReview, round, &obsoleteReviewIndex)
	if _, _, exists, obsoleteErr := store.ReadAggregateHead(contextWithTimeout(t), kernel.AggregateRef{Kind: kernel.AggregateTask, ID: obsoleteReviewID}); obsoleteErr != nil || exists {
		t.Fatalf("obsolete replacement per-task review materialized: found=%t err=%v", exists, obsoleteErr)
	}
	reviewID := featurePlanningTaskID(feature.ID, stageArchitectureReview, round, nil)
	reviewer := waitForPlanningInvocationState(t, service, store, reviewID, kernel.PurposeReplan, kernel.InvocationSucceeded)
	if reviewer.ActorFQN == author.ActorFQN {
		t.Fatalf("replacement full-plan review was not independent: author=%s reviewer=%s", author.ActorFQN, reviewer.ActorFQN)
	}
	if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
		t.Fatal(err)
	}
	replacement, found, err := service.ReadFeature(contextWithTimeout(t), feature.ID)
	if err != nil || !found || replacement.Status != organization.FeaturePlanned || replacement.Plan == nil || replacement.Plan.Version != oldPlan.Version+1 || replacement.PlanSupersession != nil {
		t.Fatalf("replacement plan did not advance exactly once: feature=%#v found=%t err=%v", replacement, found, err)
	}
	oldStories := make(map[kernel.UUIDv7]struct{}, len(oldPlan.Stories))
	oldTasks := make(map[kernel.UUIDv7]struct{}, len(oldPlan.Tasks))
	for _, story := range oldPlan.Stories {
		oldStories[story.ID] = struct{}{}
	}
	for _, task := range oldPlan.Tasks {
		oldTasks[task.ID] = struct{}{}
	}
	newStories := make(map[kernel.UUIDv7]struct{}, len(replacement.Plan.Stories))
	for _, story := range replacement.Plan.Stories {
		if _, reused := oldStories[story.ID]; reused {
			t.Fatalf("replacement reused superseded story identity %s", story.ID)
		}
		newStories[story.ID] = struct{}{}
	}
	for _, task := range replacement.Plan.Tasks {
		if _, reused := oldTasks[task.ID]; reused {
			t.Fatalf("replacement reused superseded task identity %s", task.ID)
		}
		if _, found := newStories[task.StoryID]; !found {
			t.Fatalf("replacement task %s refers to non-replacement story %s", task.ID, task.StoryID)
		}
	}
}

func exerciseArchitectureReviewFailure(t *testing.T, service *ProductionService, store *mongo.Store, server *integratedOpenHands, malformed bool, rejectedReviews int) {
	t.Helper()
	variant := "failed"
	disposition := "STRUCTURED_HANDOFF_REJECTED"
	if malformed {
		variant = "malformed"
		disposition = "STRUCTURED_HANDOFF_REVIEW_INVALID"
	} else if rejectedReviews > 1 {
		variant = "successor-exhausted"
	}
	feature, created, err := service.Features.Submit(contextWithTimeout(t), service.operatorIdentity.Principal, organization.FeatureRequestInput{
		IdempotencyKey: "phase9-architecture-review-" + variant, Team: "example", Title: "Reject an infeasible plan",
		Description:        "Prove that implementation cannot begin before independent architecture review passes.",
		AcceptanceCriteria: []string{"no implementation starts from a rejected plan"}, Priority: organization.PriorityHigh,
		Constraints: []string{"preserve current interfaces"}, Repository: "tekroo-ai/teams", WorkspaceID: "engineering", MaximumStories: 4, MaximumTasks: 8, MaximumHops: 8,
	})
	if err != nil || !created {
		t.Fatalf("submit architecture-review feature: created=%t err=%v", created, err)
	}
	for _, stage := range []featurePlanningStage{stageRefinement, stageSpecification} {
		if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
			t.Fatal(err)
		}
		taskID := deterministicOperationalUUID("feature-planning-task", string(feature.ID), string(stage))
		waitForTaskInvocationState(t, store, taskID, kernel.PurposeHandoff, kernel.InvocationSucceeded)
		if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
			t.Fatal(err)
		}
		feature, _, err = service.ReadFeature(contextWithTimeout(t), feature.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
		t.Fatal(err)
	}
	architectureTaskID := deterministicOperationalUUID("feature-planning-task", string(feature.ID), string(stageArchitecture))
	author := waitForPlanningInvocationState(t, service, store, architectureTaskID, kernel.PurposeReplan, kernel.InvocationSucceeded)
	server.mu.Lock()
	if malformed {
		server.invalidArchitectureIntegrationReviews = 1
	} else {
		server.failArchitectureIntegrationReviews = rejectedReviews
	}
	server.mu.Unlock()
	if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
		t.Fatal(err)
	}
	feature, found, err := service.ReadFeature(contextWithTimeout(t), feature.ID)
	if err != nil || !found || feature.Status != organization.FeatureSpecified || feature.Plan != nil {
		t.Fatalf("feature advanced before architecture review: feature=%#v found=%t err=%v", feature, found, err)
	}
	obsoleteReviewIndex := uint32(0)
	obsoleteReviewID := featurePlanningTaskID(feature.ID, stageArchitectureTaskReview, 0, &obsoleteReviewIndex)
	if _, _, exists, obsoleteErr := store.ReadAggregateHead(contextWithTimeout(t), kernel.AggregateRef{Kind: kernel.AggregateTask, ID: obsoleteReviewID}); obsoleteErr != nil || exists {
		t.Fatalf("obsolete per-task review materialized before failed complete-plan review: found=%t err=%v", exists, obsoleteErr)
	}
	reviewTaskID := deterministicOperationalUUID("feature-planning-task", string(feature.ID), string(stageArchitectureReview))
	reviewer := waitForTaskInvocationState(t, store, reviewTaskID, kernel.PurposeReplan, kernel.InvocationSucceeded)
	if reviewer.ActorFQN == author.ActorFQN || reviewer.ActorFQN != "example::architect-2" {
		t.Fatalf("architecture review was not independent: author=%s reviewer=%s", author.ActorFQN, reviewer.ActorFQN)
	}
	if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
		t.Fatal(err)
	}
	if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
		t.Fatal(err)
	}
	feature, found, err = service.ReadFeature(contextWithTimeout(t), feature.ID)
	if err != nil || !found || feature.Status != organization.FeatureSpecified || feature.Plan != nil {
		t.Fatalf("rejected architecture plan advanced feature: feature=%#v found=%t err=%v", feature, found, err)
	}
	reviewState, _, found, err := store.ReadAggregateHead(contextWithTimeout(t), kernel.AggregateRef{Kind: kernel.AggregateTask, ID: reviewTaskID})
	if err != nil || !found || reviewState.Condition != kernel.ConditionBlocked {
		t.Fatalf("failed architecture review did not stop its task: state=%#v found=%t err=%v", reviewState, found, err)
	}
	handoff, found, err := service.MessageBus.Read(contextWithTimeout(t), feature.LastMessageID)
	if err != nil || !found || handoff.State != organization.MessageResolved || handoff.Resolution != disposition {
		t.Fatalf("rejected architecture handoff remained claimed: claim=%#v found=%t err=%v", handoff, found, err)
	}
	implementationTaskID := deterministicOperationalUUID("feature-task", string(feature.ID), "0", "Implement behavior")
	_, _, found, err = store.ReadAggregateHead(contextWithTimeout(t), kernel.AggregateRef{Kind: kernel.AggregateTask, ID: implementationTaskID})
	if err != nil || found {
		t.Fatalf("implementation materialized after failed architecture review: found=%t err=%v", found, err)
	}
	successorArchitectureTaskID := featurePlanningTaskID(feature.ID, stageArchitecture, 1, nil)
	if malformed {
		_, _, found, err = store.ReadAggregateHead(contextWithTimeout(t), kernel.AggregateRef{Kind: kernel.AggregateTask, ID: successorArchitectureTaskID})
		if err != nil || found {
			t.Fatalf("malformed review created an untrusted successor plan: found=%t err=%v", found, err)
		}
		return
	}
	successor := waitForPlanningInvocationState(t, service, store, successorArchitectureTaskID, kernel.PurposeReplan, kernel.InvocationSucceeded)
	if successor.ID == author.ID || successor.TaskID == author.TaskID || successor.ActorFQN != author.ActorFQN || successor.RetryOfInvocationID != nil {
		t.Fatalf("review-directed architecture successor identity = %#v author=%#v", successor, author)
	}
	if successor.ConversationID == nil {
		t.Fatal("review-directed architecture successor has no conversation")
	}
	server.mu.Lock()
	successorConversation := server.conversations[*successor.ConversationID]
	server.mu.Unlock()
	if successorConversation == nil || !strings.Contains(successorConversation.prompt, "AUTHORITATIVE_PLAN_REVIEW_FEEDBACK_JSON:") || !strings.Contains(successorConversation.prompt, string(*author.OutputDigest)) || !strings.Contains(successorConversation.prompt, string(*reviewer.OutputDigest)) {
		t.Fatalf("successor prompt did not bind predecessor and review evidence: conversation=%#v", successorConversation)
	}
	if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
		t.Fatal(err)
	}
	successorObsoleteReviewIndex := uint32(0)
	successorObsoleteReviewID := featurePlanningTaskID(feature.ID, stageArchitectureTaskReview, 1, &successorObsoleteReviewIndex)
	if _, _, exists, obsoleteErr := store.ReadAggregateHead(contextWithTimeout(t), kernel.AggregateRef{Kind: kernel.AggregateTask, ID: successorObsoleteReviewID}); obsoleteErr != nil || exists {
		t.Fatalf("obsolete successor per-task review materialized: found=%t err=%v", exists, obsoleteErr)
	}
	successorReviewID := featurePlanningTaskID(feature.ID, stageArchitectureReview, 1, nil)
	waitForPlanningInvocationState(t, service, store, successorReviewID, kernel.PurposeReplan, kernel.InvocationSucceeded)
	if rejectedReviews > 1 {
		if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
			t.Fatal(err)
		}
		feature, found, err = service.ReadFeature(contextWithTimeout(t), feature.ID)
		if err != nil || !found || feature.Status != organization.FeatureSpecified || feature.Plan != nil {
			t.Fatalf("exhausted architecture successor advanced the feature: feature=%#v found=%t err=%v", feature, found, err)
		}
		secondSuccessorID := featurePlanningTaskID(feature.ID, stageArchitecture, 2, nil)
		_, _, found, err = store.ReadAggregateHead(contextWithTimeout(t), kernel.AggregateRef{Kind: kernel.AggregateTask, ID: secondSuccessorID})
		if err != nil || found {
			t.Fatalf("architecture successor limit created a second automatic revision: found=%t err=%v", found, err)
		}
		return
	}
	if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
		t.Fatal(err)
	}
	feature, found, err = service.ReadFeature(contextWithTimeout(t), feature.ID)
	if err != nil || !found || feature.Status != organization.FeaturePlanned || feature.Plan == nil || feature.Plan.PreparedExecution != successor.Execution {
		t.Fatalf("passing review-directed successor did not become the feature plan: feature=%#v found=%t err=%v", feature, found, err)
	}
}

func exerciseRejectedTaskReviewAggregation(t *testing.T, service *ProductionService, store *mongo.Store, server *integratedOpenHands) {
	t.Helper()
	feature, created, err := service.Features.Submit(contextWithTimeout(t), service.operatorIdentity.Principal, organization.FeatureRequestInput{
		IdempotencyKey: "phase9-architecture-review-feedback-aggregation", Team: "example", Title: "Review every planned task",
		Description:        "Prove that one successor receives every valid task-review rejection before replanning.",
		AcceptanceCriteria: []string{"all rejected task reviews are immutable successor inputs"}, Priority: organization.PriorityHigh,
		Constraints: []string{"preserve the task dependency order"}, Repository: "tekroo-ai/teams", WorkspaceID: "engineering", MaximumStories: 4, MaximumTasks: 8, MaximumHops: 8,
	})
	if err != nil || !created {
		t.Fatalf("submit review-feedback aggregation feature: created=%t err=%v", created, err)
	}
	for _, stage := range []featurePlanningStage{stageRefinement, stageSpecification} {
		if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
			t.Fatal(err)
		}
		taskID := featurePlanningTaskID(feature.ID, stage, 0, nil)
		waitForTaskInvocationState(t, store, taskID, kernel.PurposeHandoff, kernel.InvocationSucceeded)
		if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
			t.Fatal(err)
		}
		feature, _, err = service.ReadFeature(contextWithTimeout(t), feature.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
		t.Fatal(err)
	}
	author := waitForPlanningInvocationState(t, service, store, featurePlanningTaskID(feature.ID, stageArchitecture, 0, nil), kernel.PurposeReplan, kernel.InvocationSucceeded)
	server.mu.Lock()
	server.failArchitectureTaskReviews = 2
	server.mu.Unlock()
	if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
		t.Fatal(err)
	}
	firstIndex := uint32(0)
	firstReview := waitForPlanningInvocationState(t, service, store, featurePlanningTaskID(feature.ID, stageArchitectureTaskReview, 0, &firstIndex), kernel.PurposeReplan, kernel.InvocationSucceeded)
	if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
		t.Fatal(err)
	}
	secondIndex := uint32(1)
	secondReview := waitForPlanningInvocationState(t, service, store, featurePlanningTaskID(feature.ID, stageArchitectureTaskReview, 0, &secondIndex), kernel.PurposeReplan, kernel.InvocationSucceeded)
	if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
		t.Fatal(err)
	}
	if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
		t.Fatal(err)
	}
	successor := waitForPlanningInvocationState(t, service, store, featurePlanningTaskID(feature.ID, stageArchitecture, 1, nil), kernel.PurposeReplan, kernel.InvocationSucceeded)
	if successor.ConversationID == nil || firstReview.OutputDigest == nil || secondReview.OutputDigest == nil || author.OutputDigest == nil {
		t.Fatalf("review-feedback successor identities are incomplete: author=%#v first=%#v second=%#v successor=%#v", author, firstReview, secondReview, successor)
	}
	server.mu.Lock()
	conversation := server.conversations[*successor.ConversationID]
	server.mu.Unlock()
	// The conversation stores the complete execution brief as JSON, so the
	// nested task-description object is escaped. Verify the field name and each
	// immutable identity instead of assuming an unescaped nested representation.
	if conversation == nil || !strings.Contains(conversation.prompt, "rejected_reviews") || !strings.Contains(conversation.prompt, string(*author.OutputDigest)) || !strings.Contains(conversation.prompt, string(*firstReview.OutputDigest)) || !strings.Contains(conversation.prompt, string(*secondReview.OutputDigest)) {
		t.Fatalf("successor did not receive the complete rejected-review set: conversation=%#v", conversation)
	}
	if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
		t.Fatal(err)
	}
	for index := uint32(0); index < 2; index++ {
		reviewID := featurePlanningTaskID(feature.ID, stageArchitectureTaskReview, 1, &index)
		waitForPlanningInvocationState(t, service, store, reviewID, kernel.PurposeReplan, kernel.InvocationSucceeded)
		if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
			t.Fatal(err)
		}
	}
	waitForPlanningInvocationState(t, service, store, featurePlanningTaskID(feature.ID, stageArchitectureReview, 1, nil), kernel.PurposeReplan, kernel.InvocationSucceeded)
	if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
		t.Fatal(err)
	}
	planned, found, err := service.ReadFeature(contextWithTimeout(t), feature.ID)
	if err != nil || !found || planned.Status != organization.FeaturePlanned || planned.Plan == nil || planned.Plan.PreparedExecution != successor.Execution {
		t.Fatalf("aggregated review-feedback successor did not advance: feature=%#v found=%t err=%v", planned, found, err)
	}
}

func exerciseParallelDAGAdmission(t *testing.T, service *ProductionService, store *mongo.Store, server *integratedOpenHands, coderModel, testerModel kernel.Digest, now time.Time) {
	t.Helper()
	storyID := kernel.UUIDv7("00000000-0000-7000-8000-000000006022")
	firstID := kernel.UUIDv7("00000000-0000-7000-8000-000000006023")
	secondID := kernel.UUIDv7("00000000-0000-7000-8000-000000006024")
	firstValidationID := kernel.UUIDv7("00000000-0000-7000-8000-000000006025")
	secondValidationID := kernel.UUIDv7("00000000-0000-7000-8000-000000006026")
	feature := organization.FeatureRequest{
		SchemaVersion: organization.FeatureSchemaVersion, ID: kernel.UUIDv7("00000000-0000-7000-8000-000000006020"), Revision: 3, Status: organization.FeatureSpecified,
		Input:       organization.FeatureRequestInput{Team: "example", Title: "Parallel admission", Description: "Execute two independent DAG roots.", AcceptanceCriteria: []string{"both roots complete"}, Priority: organization.PriorityHigh, Repository: "tekroo-ai/teams", WorkspaceID: "engineering", IdempotencyKey: "parallel-admission", MaximumStories: 4, MaximumTasks: 8, MaximumHops: 8},
		SubmittedBy: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"}, OperatorActor: "example::operator-1", ProductOwnerActor: "example::product-owner-1",
		InitialMessageID: kernel.UUIDv7("00000000-0000-7000-8000-000000006027"), LastMessageID: kernel.UUIDv7("00000000-0000-7000-8000-000000006028"), LastStepID: kernel.UUIDv7("00000000-0000-7000-8000-000000006029"), LastHop: 3,
		BudgetAccountID: kernel.UUIDv7("00000000-0000-7000-8000-000000006021"), LifecycleEpoch: 1, ScopeRevision: 1, CreatedAt: now.Add(-time.Minute), UpdatedAt: now,
	}
	plan := organization.FeaturePlan{
		Version: 1, CreatedAt: now, PreparedBy: "example::architect-1", PreparedExecution: kernel.ExecutionTuple{ExecutionID: kernel.UUIDv7("00000000-0000-7000-8000-000000006030"), FencingEpoch: 1}, Architecture: "Two independent roots followed by independent validation.",
		Stories: []organization.PlannedStory{{ID: storyID, Title: "Parallel work", Description: "Execute both independent changes.", AcceptanceCriteria: []string{"both changes work"}, Priority: organization.PriorityHigh}},
		Tasks: []organization.PlannedTask{
			{ID: firstID, StoryID: storyID, Title: "First root", Description: "Implement the first independent change.", AcceptanceCriteria: []string{"first change works"}, Owner: "example::coder-1", ModelProfile: coderModel, DecisionRoute: kernel.RouteBoundedExecution, Purpose: kernel.PurposeImplementation, Complexity: 3, Risk: organization.RiskLow, CriticalPath: true, AttemptLimit: 2, ReviewRoundLimit: 1},
			{ID: secondID, StoryID: storyID, Title: "Second root", Description: "Implement the second independent change.", AcceptanceCriteria: []string{"second change works"}, Owner: "example::coder-2", ModelProfile: coderModel, DecisionRoute: kernel.RouteBoundedExecution, Purpose: kernel.PurposeImplementation, Complexity: 3, Risk: organization.RiskLow, CriticalPath: true, AttemptLimit: 2, ReviewRoundLimit: 1},
			{ID: firstValidationID, StoryID: storyID, Title: "Validate first", Description: "Validate the first independent change.", AcceptanceCriteria: []string{"first change passes"}, DependsOn: []kernel.UUIDv7{firstID}, Validates: []kernel.UUIDv7{firstID}, Owner: "example::tester-1", ModelProfile: testerModel, DecisionRoute: kernel.RouteBoundedExecution, Purpose: kernel.PurposeValidation, Complexity: 3, Risk: organization.RiskLow, CriticalPath: true, AttemptLimit: 2, ReviewRoundLimit: 1},
			{ID: secondValidationID, StoryID: storyID, Title: "Validate second", Description: "Validate the second independent change.", AcceptanceCriteria: []string{"second change passes"}, DependsOn: []kernel.UUIDv7{secondID}, Validates: []kernel.UUIDv7{secondID}, Owner: "example::tester-2", ModelProfile: testerModel, DecisionRoute: kernel.RouteBoundedExecution, Purpose: kernel.PurposeValidation, Complexity: 3, Risk: organization.RiskLow, CriticalPath: true, AttemptLimit: 2, ReviewRoundLimit: 1},
		},
	}
	if err := service.MaterializeFeaturePlan(contextWithTimeout(t), feature, plan); err != nil {
		t.Fatal(err)
	}
	firstInvocationID := deterministicOperationalUUID("work-invocation", string(feature.ID), string(firstID), string(kernel.PurposeImplementation), "1")
	secondInvocationID := deterministicOperationalUUID("work-invocation", string(feature.ID), string(secondID), string(kernel.PurposeImplementation), "1")
	server.mu.Lock()
	server.commitResults = true
	server.delays[string(firstInvocationID)] = 750 * time.Millisecond
	server.delays[string(secondInvocationID)] = 750 * time.Millisecond
	server.mu.Unlock()
	if err := service.reconcileFeaturePlan(contextWithTimeout(t), feature, plan); err != nil {
		t.Fatal(err)
	}
	if err := service.reconcileFeaturePlan(contextWithTimeout(t), feature, plan); err != nil {
		t.Fatal(err)
	}
	firstAuthorized, firstErr := store.LoadOperationalExecution(contextWithTimeout(t), firstInvocationID)
	secondAuthorized, secondErr := store.LoadOperationalExecution(contextWithTimeout(t), secondInvocationID)
	if firstErr != nil || secondErr != nil || firstAuthorized.Invocation.State.Terminal() || secondAuthorized.Invocation.State.Terminal() {
		t.Fatalf("independent DAG roots were not admitted together: first=%#v second=%#v first_err=%v second_err=%v", firstAuthorized.Invocation, secondAuthorized.Invocation, firstErr, secondErr)
	}
	waitForInvocationState(t, store, firstInvocationID, kernel.InvocationSucceeded)
	waitForInvocationState(t, store, secondInvocationID, kernel.InvocationSucceeded)
	server.mu.Lock()
	server.commitResults = false
	firstConversation := server.conversations[string(firstInvocationID)]
	secondConversation := server.conversations[string(secondInvocationID)]
	server.mu.Unlock()
	if firstConversation == nil || secondConversation == nil || firstConversation.workspace == secondConversation.workspace {
		t.Fatalf("parallel DAG roots did not receive isolated workspaces: first=%#v second=%#v", firstConversation, secondConversation)
	}
	snapshot, err := store.LoadDecision(contextWithTimeout(t), kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: firstID}})
	if err != nil {
		t.Fatal(err)
	}
	firstInvocation, firstFound := latestTaskInvocation(snapshot.WorkInvocations, firstID)
	secondInvocation, secondFound := latestTaskInvocation(snapshot.WorkInvocations, secondID)
	consumer := organization.PlannedTask{ID: kernel.UUIDv7("00000000-0000-7000-8000-000000006031"), StoryID: storyID, Purpose: kernel.PurposeValidation, DependsOn: []kernel.UUIDv7{firstID, secondID}, Validates: []kernel.UUIDv7{secondID}, Owner: "example::tester-1", ModelProfile: testerModel}
	assembledPlan := plan
	assembledPlan.Tasks = append(append([]organization.PlannedTask(nil), plan.Tasks...), consumer)
	owner, active, err := service.RoleHost.Status(contextWithTimeout(t), consumer.Owner)
	if err != nil || !active || !firstFound || !secondFound {
		t.Fatalf("assembled candidate preconditions: owner=%#v active=%t first=%t second=%t err=%v", owner, active, firstFound, secondFound, err)
	}
	candidate, _, err := service.prepareCandidateConsumerWorkspace(contextWithTimeout(t), feature, consumer, owner, assembledPlan, map[kernel.UUIDv7]kernel.WorkInvocation{firstID: firstInvocation, secondID: secondInvocation}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, invocationID := range []kernel.UUIDv7{firstInvocationID, secondInvocationID} {
		name := "result-" + string(invocationID) + ".txt"
		if _, err := os.Stat(filepath.Join(candidate.WorkingDirectory, "src", name)); err != nil {
			t.Fatalf("assembled immutable candidate omitted %s: %v", name, err)
		}
	}
}

func exerciseInvalidValidatorRecovery(t *testing.T, service *ProductionService, runtime *Runtime, store *mongo.Store, server *integratedOpenHands, provenance kernel.ProvenanceBasis, now time.Time) {
	t.Helper()
	feature, created, err := service.Features.Submit(contextWithTimeout(t), service.operatorIdentity.Principal, organization.FeatureRequestInput{
		IdempotencyKey: "phase6-invalid-validator-recovery", Team: "example", Title: "Recover invalid validator output",
		Description:        "Exercise one evidence-bound retry after the validator transport omits its structured result.",
		AcceptanceCriteria: []string{"the exact blocked validation is retried against an immutable candidate"}, Priority: organization.PriorityHigh,
		Constraints: []string{"do not repeat implementation"}, Repository: "tekroo-ai/teams", WorkspaceID: "engineering", MaximumStories: 4, MaximumTasks: 8, MaximumHops: 8,
	})
	if err != nil || !created {
		t.Fatalf("submit validator-recovery feature: created=%t err=%v", created, err)
	}
	for _, expected := range []struct {
		stage  featurePlanningStage
		status organization.FeatureStatus
	}{{stageRefinement, organization.FeatureReadyForPlanning}, {stageSpecification, organization.FeatureSpecified}, {stageArchitecture, organization.FeaturePlanned}} {
		stage := expected.stage
		if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
			t.Fatal(err)
		}
		taskID := deterministicOperationalUUID("feature-planning-task", string(feature.ID), string(stage))
		purpose := kernel.PurposeHandoff
		if stage == stageArchitecture {
			purpose = kernel.PurposeReplan
		}
		invocationID := deterministicOperationalUUID("work-invocation", string(feature.ID), string(taskID), string(purpose), "1")
		waitForInvocationState(t, store, invocationID, kernel.InvocationSucceeded)
		if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
			t.Fatal(err)
		}
		if stage == stageArchitecture {
			obsoleteReviewIndex := uint32(0)
			obsoleteReviewID := featurePlanningTaskID(feature.ID, stageArchitectureTaskReview, 0, &obsoleteReviewIndex)
			if _, _, exists, obsoleteErr := store.ReadAggregateHead(contextWithTimeout(t), kernel.AggregateRef{Kind: kernel.AggregateTask, ID: obsoleteReviewID}); obsoleteErr != nil || exists {
				t.Fatalf("obsolete per-task review materialized during validator recovery setup: found=%t err=%v", exists, obsoleteErr)
			}
			reviewTaskID := deterministicOperationalUUID("feature-planning-task", string(feature.ID), string(stageArchitectureReview))
			reviewInvocationID := deterministicOperationalUUID("work-invocation", string(feature.ID), string(reviewTaskID), string(kernel.PurposeReplan), "1")
			waitForInvocationState(t, store, reviewInvocationID, kernel.InvocationSucceeded)
			if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
				t.Fatal(err)
			}
		}
		var found bool
		feature, found, err = service.ReadFeature(contextWithTimeout(t), feature.ID)
		if err != nil || !found {
			t.Fatalf("read validator-recovery feature after %s: found=%t err=%v", stage, found, err)
		}
		if feature.Status != expected.status {
			t.Fatalf("validator-recovery feature after %s: status=%s want=%s", stage, feature.Status, expected.status)
		}
	}
	if feature.Plan == nil || len(feature.Plan.Tasks) < 2 {
		t.Fatalf("validator-recovery plan = %#v", feature.Plan)
	}
	implementation := feature.Plan.Tasks[0]
	validator := feature.Plan.Tasks[1]
	server.mu.Lock()
	server.commitResults = true
	server.mu.Unlock()
	if err := service.reconcileFeaturePlan(contextWithTimeout(t), feature, *feature.Plan); err != nil {
		t.Fatal(err)
	}
	waitForTaskInvocationState(t, store, implementation.ID, kernel.PurposeImplementation, kernel.InvocationSucceeded)
	server.mu.Lock()
	server.commitResults = false
	server.invalidValidations = 1
	server.mu.Unlock()
	if err := service.reconcileFeaturePlan(contextWithTimeout(t), feature, *feature.Plan); err != nil {
		t.Fatal(err)
	}
	invalid := waitForTaskInvocationState(t, store, validator.ID, kernel.PurposeValidation, kernel.InvocationSucceeded)
	if err := service.reconcileFeaturePlan(contextWithTimeout(t), feature, *feature.Plan); err != nil {
		t.Fatal(err)
	}
	state, _, found, err := store.ReadAggregateHead(contextWithTimeout(t), kernel.AggregateRef{Kind: kernel.AggregateTask, ID: validator.ID})
	if err != nil || !found || state.Condition != kernel.ConditionBlocked {
		t.Fatalf("invalid validator output did not block exact task: state=%#v found=%t err=%v", state, found, err)
	}
	evidenceID := kernel.UUIDv7("00000000-0000-7000-8000-000000006019")
	evidenceDigest := digestByte('7')
	fixture := newIntegratedFixture(t, now, 6100, "example::coder-1", "coder-1", "worktree-coder-1")
	evidence := fixture.command(t, "tekroo.command.evidence.register", kernel.SchemaVersion, kernel.AggregateEvidence, evidenceID, service.operatorIdentity.Principal, 0,
		map[string]any{"access_partition": "engineering", "availability": "AVAILABLE", "byte_length": 2, "canonical_digest": nil, "computation": nil, "deletion_tombstone": nil, "evidence_kind": "TEST_RESULT", "integrity_state": "DIGEST_VERIFIED", "locator": "artifact://phase9/invalid-validator-recovery", "locator_immutable": true, "media_type": "application/json", "producing_component": "phase9-integration", "producing_version": "1", "redacts": nil, "retention_policy": "phase9", "sensitivity": "INTERNAL", "sha256": evidenceDigest, "source_evidence_ids": []kernel.UUIDv7{}, "source_timestamp": nil, "transport_provenance": "teams://phase9/integration"}, nil, nil, nil)
	applied(t, runtime, evidence, provenance)
	invalidStatus, found, err := service.ReadInvocation(contextWithTimeout(t), invalid.ID)
	if err != nil || !found {
		t.Fatalf("read invalid validator invocation: found=%t err=%v", found, err)
	}
	recovered, err := service.RetryFailedTask(contextWithTimeout(t), service.operatorIdentity.Principal, invalid.ID, TaskRecoveryRequest{
		ExpectedRevision: invalidStatus.Revision,
		Reason:           "restore the required immutable-workspace runtime hook",
		EvidenceRefs:     []kernel.EvidenceRef{{EvidenceID: evidenceID, SHA256: evidenceDigest}},
		DeadlineAt:       time.Now().UTC().Add(service.planningDeadline),
		IdempotencyKey:   "invalid-validator-output-recovery",
	})
	if err != nil || recovered.InvocationID == invalid.ID || recovered.AttemptOrdinal != invalid.AttemptOrdinal+1 {
		t.Fatalf("recover invalid validator output: status=%#v err=%v", recovered, err)
	}
	recoveredInvocation := waitForTaskInvocationState(t, store, validator.ID, kernel.PurposeValidation, kernel.InvocationSucceeded)
	if recoveredInvocation.ID != recovered.InvocationID {
		t.Fatalf("recovered validator invocation=%s want=%s", recoveredInvocation.ID, recovered.InvocationID)
	}
	if err := service.reconcileFeaturePlan(contextWithTimeout(t), feature, *feature.Plan); err != nil {
		t.Fatal(err)
	}
	featureValidatorID := featureValidationTaskID(feature.ID, feature.Plan.Version)
	if err := service.reconcileFeaturePlan(contextWithTimeout(t), feature, *feature.Plan); err != nil {
		t.Fatal(err)
	}
	waitForTaskInvocationState(t, store, featureValidatorID, kernel.PurposeValidation, kernel.InvocationSucceeded)
	if err := service.reconcileFeaturePlan(contextWithTimeout(t), feature, *feature.Plan); err != nil {
		t.Fatal(err)
	}
	state, _, found, err = store.ReadAggregateHead(contextWithTimeout(t), kernel.AggregateRef{Kind: kernel.AggregateTask, ID: implementation.ID})
	if err != nil || !found || state.Phase != kernel.PhaseCompleted {
		t.Fatalf("validator recovery did not complete implementation: state=%#v found=%t err=%v", state, found, err)
	}
}

type integratedReleaseProvider struct{}

func (integratedReleaseProvider) Merge(_ context.Context, request kernel.ReleaseMergeRequest) (kernel.ReleaseProviderObservation, error) {
	return kernel.ReleaseProviderObservation{ReleasePlanID: request.ReleasePlanID, MergeID: request.MergeID, AttemptID: request.AttemptID, State: kernel.ReleaseProviderMerged, Outcome: kernel.ReleaseOutcomeMerged, BaseCommit: request.BaseCommit, HeadCommit: request.HeadCommit, TreeDigest: strings.Repeat("3", 40), Reasons: []string{"exact qualified head was fast-forwarded"}}, nil
}

func (integratedReleaseProvider) Reconcile(_ context.Context, request kernel.ReleaseMergeRequest) (kernel.ReleaseProviderObservation, error) {
	return integratedReleaseProvider{}.Merge(context.Background(), request)
}

func exerciseHumanParticipation(t *testing.T, service *ProductionService, runtime *Runtime, store *mongo.Store, provenance kernel.ProvenanceBasis, now time.Time) {
	t.Helper()
	fixture := newIntegratedFixture(t, now, 9000, "example::human-wait-1", "human-wait-1", "worktree-human-wait-1")
	fixture.createAuthoritativeTaskWith(t, runtimeCommandSubmitter(runtime, provenance))
	participant := kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "human:alice"}
	profile, err := service.RegisterHumanParticipant(contextWithTimeout(t), service.operatorIdentity.Principal, organization.HumanParticipantRegistration{
		Participant: participant, DisplayLabel: "Alice SME", RoleClass: "SME", RoleID: "domain-sme", ScopeKind: "WORK", ScopeID: string(fixture.taskID), Channel: "WEB_PORTAL", Confidentiality: kernel.ConfidentialityInternal, IdempotencyKey: "human-alice-registration",
	})
	if err != nil || profile.Participant != participant || !profile.Active {
		t.Fatalf("human profile=%+v err=%v", profile, err)
	}
	notification, err := service.AskHuman(contextWithTimeout(t), service.operatorIdentity.Principal, organization.HumanQuestionRequest{
		IdempotencyKey: "human-alice-question", SubjectTaskID: fixture.taskID, Recipient: participant,
		Question: "Which public interface must remain stable?", ResponseSpecification: "Identify the existing public interface by name.",
		Purpose: "REQUIREMENTS_CLARIFICATION", DeclaredEffect: "ADVISORY_ONLY", Confidentiality: kernel.ConfidentialityInternal, DeadlineAt: now.Add(time.Hour),
	})
	if err != nil || notification.State != kernel.HumanInteractionCollecting {
		t.Fatalf("human question=%+v err=%v", notification, err)
	}
	taskState, _, found, err := store.ReadAggregateHead(contextWithTimeout(t), kernel.AggregateRef{Kind: kernel.AggregateTask, ID: fixture.taskID})
	if err != nil || !found || taskState.Condition != kernel.ConditionBlocked {
		t.Fatalf("blocked task=%+v found=%t err=%v", taskState, found, err)
	}
	decision, err := store.LoadDecision(contextWithTimeout(t), kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: fixture.taskID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, invoked := latestTaskInvocation(decision.WorkInvocations, fixture.taskID); invoked {
		t.Fatal("human wait created a model invocation")
	}
	// Reconstruct the service from durable ports only. No participant,
	// interaction, notification, or blocker state is copied in process memory.
	service = &ProductionService{
		Store: service.Store, Runtime: service.Runtime, provenance: service.provenance,
		clock: service.clock, ids: service.ids, serviceAuthority: service.serviceAuthority,
		policyAuthority: service.policyAuthority, operatorIdentity: service.operatorIdentity,
	}
	interactionBeforeResponse, err := service.ReadHumanInteraction(contextWithTimeout(t), notification.InteractionID)
	if err != nil || interactionBeforeResponse.Phase != kernel.HumanInteractionCollecting {
		t.Fatalf("restarted service interaction=%+v err=%v", interactionBeforeResponse, err)
	}
	notification, err = service.RespondToHumanQuestion(contextWithTimeout(t), participant, organization.HumanResponseInput{InteractionID: notification.InteractionID, ExpectedRevision: 2, Response: "Preserve the existing public interface.", ResponseClassification: "ANSWER"})
	if err != nil || notification.State != kernel.HumanInteractionClosed || notification.ResponseEventID == nil {
		t.Fatalf("human response=%+v err=%v", notification, err)
	}
	taskState, _, found, err = store.ReadAggregateHead(contextWithTimeout(t), kernel.AggregateRef{Kind: kernel.AggregateTask, ID: fixture.taskID})
	if err != nil || !found || taskState.Condition != kernel.ConditionRunnable {
		t.Fatalf("unblocked task=%+v found=%t err=%v", taskState, found, err)
	}
	interaction, err := service.ReadHumanInteraction(contextWithTimeout(t), notification.InteractionID)
	if err != nil || interaction.Phase != kernel.HumanInteractionClosed || len(interaction.Responses) != 1 {
		t.Fatalf("human interaction=%+v err=%v", interaction, err)
	}
	notifications, err := service.HumanNotifications(contextWithTimeout(t), participant, false)
	if err != nil || len(notifications) != 1 || notifications[0].InteractionID != notification.InteractionID {
		t.Fatalf("human notifications=%+v err=%v", notifications, err)
	}
}

func submitAutomatedFeature(t *testing.T, service *ProductionService) (organization.FeatureRequest, bool) {
	t.Helper()
	returnValue, created, err := service.SubmitFeature(contextWithTimeout(t), kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"}, organization.FeatureRequestInput{IdempotencyKey: "phase6-automated-feature", Team: "example", Title: "Automate a bounded change", Description: "Implement one small repository change and verify it independently.", AcceptanceCriteria: []string{"the requested behavior works"}, Priority: organization.PriorityHigh, Constraints: []string{"preserve current interfaces"}, Repository: "tekroo-ai/teams", WorkspaceID: "engineering", MaximumStories: 4, MaximumTasks: 8, MaximumHops: 8})
	if err != nil {
		t.Fatal(err)
	}
	return returnValue, created
}

func waitForInvocationState(t *testing.T, store *mongo.Store, invocationID kernel.UUIDv7, expected kernel.WorkInvocationState) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		current, err := store.LoadOperationalExecution(contextWithTimeout(t), invocationID)
		if err == nil && current.Invocation.State == expected {
			return
		}
		if err == nil && current.Invocation.State.Terminal() {
			t.Fatalf("invocation %s reached unexpected terminal state: %#v", invocationID, current.Invocation)
		}
		if time.Now().After(deadline) {
			t.Fatalf("invocation %s state=%s err=%v", invocationID, current.Invocation.State, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForTaskInvocationState(t *testing.T, store *mongo.Store, taskID kernel.UUIDv7, purpose kernel.WorkPurpose, expected kernel.WorkInvocationState) kernel.WorkInvocation {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var observed kernel.WorkInvocation
	foundObserved := false
	for {
		snapshot, err := store.LoadDecision(contextWithTimeout(t), kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: taskID}})
		if err == nil {
			latest, found := latestTaskInvocation(snapshot.WorkInvocations, taskID)
			if found {
				observed, foundObserved = latest, true
			}
			if found && latest.Purpose == purpose && latest.State == expected {
				return latest
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s expected purpose/state=%s/%s observed=%#v found=%t err=%v", taskID, purpose, expected, observed, foundObserved, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForPlanningInvocationState(t *testing.T, service *ProductionService, store *mongo.Store, taskID kernel.UUIDv7, purpose kernel.WorkPurpose, expected kernel.WorkInvocationState) kernel.WorkInvocation {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		snapshot, err := store.LoadDecision(contextWithTimeout(t), kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: taskID}})
		if err == nil {
			latest, found := latestTaskInvocation(snapshot.WorkInvocations, taskID)
			if found && latest.Purpose == purpose && latest.State == expected {
				return latest
			}
			if found && latest.State.Terminal() {
				var output []byte
				var outputErr error
				if latest.OutputDigest != nil {
					output, outputErr = service.Runtime.ReadExecutionOutput(contextWithTimeout(t), *latest.OutputDigest)
				}
				t.Fatalf("planning task %s ended %s: invocation=%#v output=%q output_err=%v", taskID, latest.State, latest, output, outputErr)
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("planning task %s purpose=%s state=%s err=%v", taskID, purpose, expected, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func loadStarterTeam(t *testing.T) organization.LoadedTeam {
	t.Helper()
	manifestPath, err := filepath.Abs(filepath.Join("..", "..", "config", "starter-team", "team.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	publicRaw, err := os.ReadFile(filepath.Join(filepath.Dir(manifestPath), "publisher.pub"))
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(publicRaw)))
	if err != nil {
		t.Fatal(err)
	}
	groundingRaw, err := os.ReadFile(filepath.Join(filepath.Dir(manifestPath), "role-grounding-publisher.pub"))
	if err != nil {
		t.Fatal(err)
	}
	groundingKey, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(groundingRaw)))
	if err != nil {
		t.Fatal(err)
	}
	team, err := organization.LoadTeamManifest(manifestPath, kernel.Digest("3478f27988da4f7c022df0ca7145af88b8e8cc1eb6fd446402ff69b33519c693"), map[string]ed25519.PublicKey{"tekroo-phase6-bootstrap": publicKey, "tekroo-role-grounding-20260901": groundingKey})
	if err != nil {
		t.Fatal(err)
	}
	return team
}
