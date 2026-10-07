//go:build mongo_integration

package operationalruntime

import (
	"net/http"
	"os"
	"path/filepath"
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

// Uses a disposable database and execution fixture, never a live model or team.
// Workspace pinning is tested separately against real Git repositories.
func TestSupersededCandidateCancellationUsesSupportedKernelBoundary(t *testing.T) {
	process, uri := startRuntimeMongod(t)
	defer stopRuntimeMongod(process)
	policy := integratedPolicy()
	policy.Grants[1].Scope.CommandTypes = append(policy.Grants[1].Scope.CommandTypes, "tekroo.command.work-invocation.request-cancellation")
	store, err := mongo.Open(contextWithTimeout(t), mongo.Config{URI: uri, Database: "candidate_lineage_fixture", ContractIdentity: kernel.ContractIdentity, ManifestSHA256: phase4ManifestSHA, MigrationLevel: 1, Policy: policy, BacklogLimit: 1024, DeliveryPolicyRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer closeRuntimeStore(t, store)
	catalogue, err := contract.Load(os.DirFS(filepath.Join("..", "..")), "CONTRACTS/tekroo.kernel.contracts/0.12.0")
	if err != nil {
		t.Fatal(err)
	}
	provenance, err := fake.ProvenanceBasis()
	if err != nil {
		t.Fatal(err)
	}
	clock := SystemClock{}
	ids, err := NewUUIDv7Source(clock)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	f := newIntegratedFixture(t, now, 31000, "teams::coder-1", "fixture-workspace", "fixture-worktree")
	profile, err := openhands.NewAcceptedExecutionProfile(f.modelDigest, f.runtimeDigest, f.toolDigest, f.effectDigest, 24, "candidate_lineage_fixture", "unused_fixture_memory")
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := New(contextWithTimeout(t), Config{
		Store: store, Catalogue: catalogue, Clock: clock, IDs: ids,
		OpenHandsBaseURL: "http://127.0.0.1:1", OpenHandsSessionAPIKey: "fixture", HTTPClient: &http.Client{Timeout: time.Second},
		WorkspaceBindings: []openhands.WorkspaceBinding{{WorkspaceID: f.workspaceID, WorktreeID: f.worktreeID, WorkingDirectory: t.TempDir()}},
		ExecutionProfiles: []openhands.ExecutionProfile{profile}, RoleGrounding: testRoleGroundingResolver{}, OpenHandsPollInterval: time.Millisecond,
		OpenHandsMaximumPages: 8, OpenHandsMaximumEvidence: 1 << 20, EvidenceRoot: t.TempDir(),
		ExecutionPolicy: application.OperationalExecutionPolicy{OperationTimeout: time.Second, MaximumBriefBytes: 1 << 20, ConsumerID: "lineage-fixture", PolicyRevision: 1, ServiceAuthority: f.service, ExpiryAuthority: f.policy, Provenance: provenance},
		EvidencePolicy:  application.CommandEvidenceRecorderPolicy{PolicyRevision: 1, Authority: f.service, Provenance: provenance, ProducingVersion: "fixture", RetentionPolicy: "fixture"},
		WorkerPolicy:    executionruntime.Policy{ConsumerID: "lineage-fixture", LeaseDuration: 3 * time.Second, ReconciliationInterval: 10 * time.Millisecond, MaximumReconciliations: 20, MaximumConcurrentInvocations: 1, LeaseOperationTimeout: time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(contextWithTimeout(t))
	f.createAuthoritativeTask(t, runtime, provenance)
	f.authorizeInvocation(t, runtime, provenance)
	authorized, err := store.LoadOperationalExecution(contextWithTimeout(t), f.invocationID)
	if err != nil {
		t.Fatal(err)
	}
	// Drive the kernel protocol explicitly. No external execution server is
	// needed to test the supported cancellation command and its persistence.
	claimID := candidateTestUUID(31901)
	claim := f.command(t, "tekroo.command.work-invocation.claim", kernel.OperationalSchemaVersion, kernel.AggregateWorkInvocation, f.invocationID, f.service, 1,
		map[string]any{"invocation_id": f.invocationID, "expected_invocation_revision": 1, "claim_id": claimID, "actor_fqn": f.actor, "execution_id": f.executionID, "fencing_epoch": 1, "model_profile_digest": f.modelDigest, "runtime_identity_digest": f.runtimeDigest, "consumer_id": "lineage-fixture", "claimed_at": now}, nil, nil, nil)
	claim.ActorFQN = &f.actor
	claim.Causation = []kernel.DagParent{{ParentEventID: authorized.Invocation.LastEventID, EdgeKind: kernel.EdgeCausal}}
	claim.ExpectedLifecycleEpoch = nil
	claim.Preconditions = []kernel.AggregatePrecondition{{Aggregate: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: f.taskID}, Expected: kernel.NewExpectedRevision(f.taskRevision)}, {Aggregate: kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: f.budgetID}, Expected: kernel.NewExpectedRevision(f.budgetRevision)}}
	executionTuple := kernel.ExecutionTuple{ExecutionID: f.executionID, FencingEpoch: 1}
	claim.Execution = &executionTuple
	applied(t, runtime, claim, provenance)
	claimed, err := store.LoadOperationalExecution(contextWithTimeout(t), f.invocationID)
	if err != nil {
		t.Fatal(err)
	}
	started := f.command(t, "tekroo.command.work-invocation.record-started", kernel.OperationalSchemaVersion, kernel.AggregateWorkInvocation, f.invocationID, f.service, 2,
		map[string]any{"invocation_id": f.invocationID, "expected_invocation_revision": 2, "claim_id": claimID, "conversation_id": string(f.invocationID), "request_digest": digestByte('d'), "started_at": now}, nil, nil, nil)
	started.ExpectedLifecycleEpoch = nil
	started.Causation = []kernel.DagParent{{ParentEventID: claimed.Invocation.LastEventID, EdgeKind: kernel.EdgeCausal}}
	applied(t, runtime, started, provenance)
	execution, err := store.LoadOperationalExecution(contextWithTimeout(t), f.invocationID)
	if err != nil {
		t.Fatal(err)
	}
	service := &ProductionService{Store: store, Runtime: runtime, provenance: provenance, clock: clock, ids: ids, policyAuthority: f.policy}
	feature := organization.FeatureRequest{ID: candidateTestUUID(31900), LifecycleEpoch: 1}
	evidence := []kernel.EvidenceRef{{EvidenceID: f.evidenceID, SHA256: digestByte('e')}}
	if err := service.cancelSupersededCandidate(contextWithTimeout(t), feature, execution.Invocation, evidence, "pinned producer superseded"); err != nil {
		t.Fatal(err)
	}
	execution, err = store.LoadOperationalExecution(contextWithTimeout(t), f.invocationID)
	if err != nil || execution.Invocation.CancellationRequestedAt == nil {
		t.Fatalf("cancellation not retained: %+v %v", execution.Invocation, err)
	}
}
