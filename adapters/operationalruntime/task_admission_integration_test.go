//go:build mongo_integration

package operationalruntime

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"net/http"
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
	modelDigest := digestByte('2')
	runtimeDigest := digestByte('3')
	toolDigest := digestByte('4')
	effectDigest := digestByte('5')
	profile, err := openhands.NewAcceptedExecutionProfile(modelDigest, runtimeDigest, toolDigest, effectDigest, 24, "tekroo_phase6_task_admission", "sma_step15_memory")
	if err != nil {
		t.Fatal(err)
	}
	clock := SystemClock{}
	ids, err := NewUUIDv7Source(clock)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := New(contextWithTimeout(t), Config{
		Store: store, Catalogue: catalogue, Clock: clock, IDs: ids,
		OpenHandsBaseURL: "http://127.0.0.1:1", OpenHandsSessionAPIKey: "not-used",
		HTTPClient:        &http.Client{Timeout: time.Second},
		WorkspaceBindings: []openhands.WorkspaceBinding{{WorkspaceID: "coder-1", WorktreeID: "worktree-coder-1", WorkingDirectory: workspace}},
		ExecutionProfiles: []openhands.ExecutionProfile{profile}, OpenHandsPollInterval: time.Millisecond,
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
	roleHost, err := organization.NewHost(team, organization.NewMemoryRoleStore(), roleRuntime, clock, ids)
	if err != nil {
		t.Fatal(err)
	}

	qualification := kernel.AssignmentQualificationReceipt{
		QualificationID: kernel.UUIDv7("00000000-0000-7000-8000-000000006001"), QualificationDigest: digestByte('6'),
		QualificationCorpusDigest: digestByte('7'), ModelProfileDigest: modelDigest, DecisionRoute: kernel.RouteBoundedExecution,
		QualifiedRole: "programmer", Status: kernel.QualificationPass, ObservedAt: now.Add(-time.Minute),
	}
	service := &ProductionService{
		Runtime: runtime, RoleHost: roleHost, provenance: provenance, clock: clock, ids: ids,
		planningDeadline: 2 * time.Hour,
		planning:         ProductionPlanning{PolicyRevision: 1, ClassificationPolicyDigest: digestByte('8'), PromotionPolicyDigest: digestByte('6'), VerificationTopologyDigest: digestByte('d'), SelectionPolicyDigest: digestByte('9'), BudgetPolicyDigest: digestByte('b'), RequiredGateIDs: []string{"go-test"}, Deadline: "2h"},
		profilesByModel:  map[kernel.Digest]ProductionProfile{modelDigest: {ModelProfileDigest: modelDigest, RuntimeIdentityDigest: runtimeDigest, ToolPolicyDigest: toolDigest, EffectPolicyDigest: effectDigest, MaximumIterations: 24, Qualification: qualification}},
		workspacesByID:   map[string]ProductionWorkspace{"coder-1": {WorkspaceID: "coder-1", WorktreeID: "worktree-coder-1", WorkingDirectory: workspace, Branch: "task/phase6", BaselineSHA: strings.Repeat("1", 40), WritablePaths: []string{"src/"}}},
		serviceAuthority: kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "teams-operational-runtime"},
		policyAuthority:  kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "teams-admission-policy"},
	}
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
		Tasks:   []organization.PlannedTask{{ID: kernel.UUIDv7("00000000-0000-7000-8000-000000006013"), StoryID: kernel.UUIDv7("00000000-0000-7000-8000-000000006012"), Title: "Implement", Description: "Implement the accepted change.", AcceptanceCriteria: []string{"go test passes"}, Owner: "example::coder-1", ModelProfile: modelDigest, DecisionRoute: kernel.RouteBoundedExecution, Complexity: 3, Risk: organization.RiskLow, CriticalPath: true, AttemptLimit: 2, ReviewRoundLimit: 1}},
	}
	if err := service.MaterializeFeaturePlan(contextWithTimeout(t), feature, plan); err != nil {
		t.Fatal(err)
	}
	if err := service.MaterializeFeaturePlan(contextWithTimeout(t), feature, plan); err != nil {
		t.Fatalf("idempotent materialization: %v", err)
	}
	if _, err := store.ProjectPendingOperationalEvents(contextWithTimeout(t), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	projection, found, err := store.ReadTaskProjection(contextWithTimeout(t), plan.Tasks[0].ID)
	if err != nil || !found || !projection.Valid() || projection.Phase != string(kernel.PhaseActive) || projection.OwnerFQN == nil || *projection.OwnerFQN != plan.Tasks[0].Owner || !projection.WorkProfileID.Valid() || projection.QualifiedAssignment == nil || projection.Budget.AccountID != feature.BudgetAccountID || projection.OperationalScope == nil || projection.OperationalScope.WorkspaceID != "coder-1" {
		t.Fatalf("projection=%+v found=%t err=%v", projection, found, err)
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
