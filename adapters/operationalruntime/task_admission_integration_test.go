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
	policy.Grants[3].Grantee.ID = "example::coder-1"
	policy.Grants[4].Grantee.ID = "example::coder-2"
	policy.Grants[4].Scope.CommandTypes = append(policy.Grants[4].Scope.CommandTypes, "tekroo.command.task.request-completion", "tekroo.command.completion-review.record-result")
	policy.Grants[4].Scope.TargetKinds = append(policy.Grants[4].Scope.TargetKinds, kernel.AggregateCompletionReview)
	policy.Grants[2].Scope.CommandTypes = append(policy.Grants[2].Scope.CommandTypes, "tekroo.command.completion-review.record-result")
	policy.Grants[2].Scope.TargetKinds = append(policy.Grants[2].Scope.TargetKinds, kernel.AggregateCompletionReview)
	roleCommands := []string{"tekroo.command.task.acquire-ownership", "tekroo.command.task.activate", "tekroo.command.task.request-completion"}
	for index, actor := range []kernel.ActorFQN{"example::product-owner-1", "example::project-manager-1", "example::architect-1", "example::tester-1"} {
		commands := append([]string(nil), roleCommands...)
		targets := []kernel.AggregateKind{kernel.AggregateTask}
		if actor == "example::tester-1" {
			commands = append(commands, "tekroo.command.completion-review.record-result")
			targets = append(targets, kernel.AggregateCompletionReview)
		}
		policy.Grants = append(policy.Grants, kernel.AuthorityGrant{GrantDigest: digestByte(byte('2' + index)), Grantee: kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: string(actor)}, Scope: kernel.AuthorityScope{CommandTypes: commands, TargetKinds: targets, CanReadTarget: true}})
	}
	store, err := mongo.Open(contextWithTimeout(t), mongo.Config{
		URI: uri, Database: "tekroo_phase6_task_admission", ContractIdentity: kernel.ContractIdentity,
		ManifestSHA256: phase4ManifestSHA, MigrationLevel: 1, Policy: policy,
		BacklogLimit: 1024, DeliveryPolicyRevision: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeRuntimeStore(t, store)

	catalogue, err := contract.Load(os.DirFS(filepath.Join("..", "..")), "CONTRACTS/tekroo.kernel.contracts/0.8.0")
	if err != nil {
		t.Fatal(err)
	}
	provenance, err := fake.ProvenanceBasis()
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	serverState := &integratedOpenHands{t: t, conversations: make(map[string]*integratedConversation)}
	server := httptest.NewServer(http.HandlerFunc(serverState.serveHTTP))
	defer server.Close()
	modelDigest := digestByte('2')
	runtimeDigest := digestByte('3')
	toolDigest := digestByte('4')
	effectDigest := digestByte('5')
	modelDigests := []kernel.Digest{digestByte('1'), modelDigest, digestByte('4'), digestByte('5'), digestByte('8')}
	executionProfiles := make([]openhands.ExecutionProfile, 0, len(modelDigests))
	productionProfiles := make(map[kernel.Digest]ProductionProfile, len(modelDigests))
	for index, currentModel := range modelDigests {
		profile, profileErr := openhands.NewAcceptedExecutionProfile(currentModel, runtimeDigest, toolDigest, effectDigest, 24, "tekroo_phase6_task_admission", "sma_step15_memory")
		if profileErr != nil {
			t.Fatal(profileErr)
		}
		executionProfiles = append(executionProfiles, profile)
		productionProfiles[currentModel] = ProductionProfile{ModelProfileDigest: currentModel, RuntimeIdentityDigest: runtimeDigest, ToolPolicyDigest: toolDigest, EffectPolicyDigest: effectDigest, MaximumIterations: 24, Qualification: kernel.AssignmentQualificationReceipt{QualificationID: deterministicOperationalUUID("qualification", string(currentModel)), QualificationDigest: digestByte(byte('9' - index)), QualificationCorpusDigest: digestByte('7'), ModelProfileDigest: currentModel, DecisionRoute: kernel.RouteBoundedExecution, QualifiedRole: "programmer", Status: kernel.QualificationPass, ObservedAt: now.Add(-time.Minute)}}
	}
	clock := SystemClock{}
	ids, err := NewUUIDv7Source(clock)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := New(contextWithTimeout(t), Config{
		Store: store, Catalogue: catalogue, Clock: clock, IDs: ids,
		OpenHandsBaseURL: server.URL, OpenHandsSessionAPIKey: "step7-session-key",
		HTTPClient:        &http.Client{Timeout: time.Second},
		WorkspaceBindings: []openhands.WorkspaceBinding{{WorkspaceID: "coder-1", WorktreeID: "worktree-coder-1", WorkingDirectory: workspace}, {WorkspaceID: "coder-2", WorktreeID: "worktree-coder-2", WorkingDirectory: workspace}, {WorkspaceID: "product-owner-1", WorktreeID: "worktree-product-owner-1", WorkingDirectory: workspace}, {WorkspaceID: "project-manager-1", WorktreeID: "worktree-project-manager-1", WorkingDirectory: workspace}, {WorkspaceID: "architect-1", WorktreeID: "worktree-architect-1", WorkingDirectory: workspace}, {WorkspaceID: "tester-1", WorktreeID: "worktree-tester-1", WorkingDirectory: workspace}},
		ExecutionProfiles: executionProfiles, OpenHandsPollInterval: time.Millisecond,
		OpenHandsMaximumPages: 8, OpenHandsMaximumEvidence: 1 << 20, EvidenceRoot: t.TempDir(),
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
		planningDeadline: 2 * time.Hour,
		recoveryTimeout:  time.Second, messageMaximumAttempts: 3,
		planning:        ProductionPlanning{PolicyRevision: 1, ClassificationPolicyDigest: digestByte('8'), PromotionPolicyDigest: digestByte('6'), VerificationTopologyDigest: digestByte('d'), SelectionPolicyDigest: digestByte('9'), BudgetPolicyDigest: digestByte('b'), RequiredGateIDs: []string{"go-test"}, Deadline: "2h"},
		profilesByModel: productionProfiles,
		workspacesByID: map[string]ProductionWorkspace{
			"coder-1":           {WorkspaceID: "coder-1", WorktreeID: "worktree-coder-1", WorkingDirectory: workspace, Branch: "task/phase6", BaselineSHA: strings.Repeat("1", 40), WritablePaths: []string{"src/"}},
			"coder-2":           {WorkspaceID: "coder-2", WorktreeID: "worktree-coder-2", WorkingDirectory: workspace, Branch: "task/phase6-review", BaselineSHA: strings.Repeat("1", 40), WritablePaths: []string{"src/"}},
			"product-owner-1":   {WorkspaceID: "product-owner-1", WorktreeID: "worktree-product-owner-1", WorkingDirectory: workspace, Branch: "planning/product-owner", BaselineSHA: strings.Repeat("1", 40), WritablePaths: []string{"."}},
			"project-manager-1": {WorkspaceID: "project-manager-1", WorktreeID: "worktree-project-manager-1", WorkingDirectory: workspace, Branch: "planning/project-manager", BaselineSHA: strings.Repeat("1", 40), WritablePaths: []string{"."}},
			"architect-1":       {WorkspaceID: "architect-1", WorktreeID: "worktree-architect-1", WorkingDirectory: workspace, Branch: "planning/architect", BaselineSHA: strings.Repeat("1", 40), WritablePaths: []string{"."}},
			"tester-1":          {WorkspaceID: "tester-1", WorktreeID: "worktree-tester-1", WorkingDirectory: workspace, Branch: "validation/tester", BaselineSHA: strings.Repeat("1", 40), WritablePaths: []string{"."}},
		},
		serviceAuthority: kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "teams-operational-runtime"},
		policyAuthority:  kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "teams-admission-policy"},
	}
	features, err := organization.NewFeatureCoordinator(store, roleHost, service, clock, ids)
	if err != nil {
		t.Fatal(err)
	}
	service.Features = features
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
	defer cancelRun()
	runResult := make(chan error, 1)
	go func() { runResult <- runtime.Run(runContext) }()
	if err := service.MaterializeFeaturePlan(contextWithTimeout(t), feature, plan); err != nil {
		t.Fatal(err)
	}
	if err := service.MaterializeFeaturePlan(contextWithTimeout(t), feature, plan); err != nil {
		t.Fatalf("idempotent materialization: %v", err)
	}
	firstInvocationID := deterministicOperationalUUID("work-invocation", string(feature.ID), string(plan.Tasks[0].ID), string(plan.Tasks[0].Purpose), "1")
	waitForInvocationState(t, store, firstInvocationID, kernel.InvocationSucceeded)
	if err := service.reconcileFeaturePlan(contextWithTimeout(t), feature, plan); err != nil {
		t.Fatal(err)
	}
	waitForTaskInvocationState(t, store, plan.Tasks[1].ID, kernel.PurposeValidation, kernel.InvocationSucceeded)
	if err := service.reconcileFeaturePlan(contextWithTimeout(t), feature, plan); err != nil {
		t.Fatal(err)
	}
	serverState.mu.Lock()
	serverState.failValidations = 1
	serverState.mu.Unlock()
	automated, created := submitAutomatedFeature(t, service)
	if !created {
		t.Fatal("automated feature was not created")
	}
	for _, expected := range []struct {
		stage featurePlanningStage
		next  organization.FeatureStatus
	}{{stageRefinement, organization.FeatureReadyForPlanning}, {stageSpecification, organization.FeatureSpecified}, {stageArchitecture, organization.FeaturePlanned}} {
		if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
			t.Fatal(err)
		}
		taskID := deterministicOperationalUUID("feature-planning-task", string(automated.ID), string(expected.stage))
		purpose := kernel.PurposeHandoff
		if expected.stage == stageArchitecture {
			purpose = kernel.PurposeReplan
		}
		invocationID := deterministicOperationalUUID("work-invocation", string(automated.ID), string(taskID), string(purpose), "1")
		waitForInvocationState(t, store, invocationID, kernel.InvocationSucceeded)
		if err := service.reconcileFeaturePlanning(contextWithTimeout(t)); err != nil {
			t.Fatal(err)
		}
		var found bool
		automated, found, err = service.ReadFeature(contextWithTimeout(t), automated.ID)
		if err != nil || !found || automated.Status != expected.next {
			t.Fatalf("automated feature stage %s = %#v found=%t err=%v", expected.stage, automated, found, err)
		}
	}
	if automated.Plan == nil || len(automated.Plan.Tasks) != 4 || automated.Plan.Tasks[1].Purpose != kernel.PurposeValidation || len(automated.Plan.Tasks[1].Validates) != 1 || automated.Plan.Tasks[1].Validates[0] != automated.Plan.Tasks[0].ID || automated.Plan.Tasks[2].Purpose != kernel.PurposePromotion || automated.Plan.Tasks[2].Owner != automated.ProductOwnerActor || automated.Plan.Tasks[3].Purpose != kernel.PurposeValidation || len(automated.Plan.Tasks[3].Validates) != 1 || automated.Plan.Tasks[3].Validates[0] != automated.Plan.Tasks[2].ID {
		t.Fatalf("automated plan = %#v", automated.Plan)
	}
	automatedImplementation := automated.Plan.Tasks[0]
	automatedValidation := automated.Plan.Tasks[1]
	automatedAcceptance := automated.Plan.Tasks[2]
	automatedAcceptanceValidation := automated.Plan.Tasks[3]
	waitForInvocationState(t, store, deterministicOperationalUUID("work-invocation", string(automated.ID), string(automatedImplementation.ID), string(automatedImplementation.Purpose), "1"), kernel.InvocationSucceeded)
	if err := service.reconcileFeaturePlan(contextWithTimeout(t), automated, *automated.Plan); err != nil {
		t.Fatal(err)
	}
	firstValidation := waitForTaskInvocationState(t, store, automatedValidation.ID, kernel.PurposeValidation, kernel.InvocationSucceeded)
	if err := service.reconcileFeaturePlan(contextWithTimeout(t), automated, *automated.Plan); err != nil {
		t.Fatal(err)
	}
	repair := waitForTaskInvocationState(t, store, automatedImplementation.ID, kernel.PurposeRepair, kernel.InvocationSucceeded)
	if repair.AttemptOrdinal != 1 || repair.OutputDigest == nil {
		t.Fatalf("repair invocation = %#v", repair)
	}
	if err := service.reconcileFeaturePlan(contextWithTimeout(t), automated, *automated.Plan); err != nil {
		t.Fatal(err)
	}
	secondValidation := waitForTaskInvocationState(t, store, automatedValidation.ID, kernel.PurposeValidation, kernel.InvocationSucceeded)
	if secondValidation.ID == firstValidation.ID || secondValidation.AttemptOrdinal != 2 || secondValidation.ConditionDigest == firstValidation.ConditionDigest {
		t.Fatalf("validation rounds first=%#v second=%#v", firstValidation, secondValidation)
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
	waitForTaskInvocationState(t, store, automatedAcceptance.ID, kernel.PurposePromotion, kernel.InvocationSucceeded)
	if err := service.reconcileFeaturePlan(contextWithTimeout(t), automated, *automated.Plan); err != nil {
		t.Fatal(err)
	}
	waitForTaskInvocationState(t, store, automatedAcceptanceValidation.ID, kernel.PurposeValidation, kernel.InvocationSucceeded)
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
	automated, err = service.AcceptFeature(contextWithTimeout(t), automated.ID, automated.Revision, automated.SubmittedBy, "this integration feature produces retained evidence but no repository release")
	if err != nil || automated.Status != organization.FeatureAccepted || automated.Acceptance == nil || automated.Acceptance.AcceptedBy == nil || len(automated.Acceptance.ReleasePlanIDs) != len(automated.Plan.Stories) {
		t.Fatalf("automated feature acceptance = %#v err=%v", automated, err)
	}
	cancelRun()
	if err := <-runResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("runtime stop = %v", err)
	}
	if _, err := store.ProjectPendingOperationalEvents(contextWithTimeout(t), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	automatedStory, found, err := store.ReadStoryProjection(contextWithTimeout(t), automated.Plan.Stories[0].ID)
	if err != nil || !found || automatedStory.Phase != string(kernel.PhaseAccepted) || automatedStory.Release.State != string(kernel.ReleaseNotRequired) || automatedStory.Acceptance.State != "ACCEPTED" {
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
		if time.Now().After(deadline) {
			t.Fatalf("invocation %s state=%s err=%v", invocationID, current.Invocation.State, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForTaskInvocationState(t *testing.T, store *mongo.Store, taskID kernel.UUIDv7, purpose kernel.WorkPurpose, expected kernel.WorkInvocationState) kernel.WorkInvocation {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		snapshot, err := store.LoadDecision(contextWithTimeout(t), kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: taskID}})
		if err == nil {
			latest, found := latestTaskInvocation(snapshot.WorkInvocations, taskID)
			if found && latest.Purpose == purpose && latest.State == expected {
				return latest
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s purpose=%s state=%s err=%v", taskID, purpose, expected, err)
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
	team, err := organization.LoadTeamManifest(manifestPath, kernel.Digest("a6c918f9a454509e1f72be950497a05cd87db5d69b3232ff64a0e9c8b9d0dbdd"), map[string]ed25519.PublicKey{"tekroo-phase6-bootstrap": publicKey})
	if err != nil {
		t.Fatal(err)
	}
	return team
}
