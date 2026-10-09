//go:build mongo_integration

package operationalruntime

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
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

// Real command/event processing, no model worker: recognizing and resolving an
// operator-approved block must not publish PASS or spend a model invocation.
func TestReportedValidatorRecoveryClassifiesAndUnblocksExactKernelEvent(t *testing.T) {
	process, uri := startRuntimeMongod(t)
	defer stopRuntimeMongod(process)
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()
	policy := integratedPolicy()
	policy.Grants[1].Scope.CommandTypes = append(policy.Grants[1].Scope.CommandTypes, "tekroo.command.work.block", "tekroo.command.work.unblock")
	store, err := mongo.Open(ctx, mongo.Config{URI: uri, Database: "reported_validator_recovery", ContractIdentity: kernel.ContractIdentity, ManifestSHA256: phase4ManifestSHA, MigrationLevel: 1, Policy: policy, BacklogLimit: 1024, DeliveryPolicyRevision: 1})
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
	f := newIntegratedFixture(t, time.Now().UTC().Truncate(time.Millisecond), 11300, "teams::coder-1", "reported-validator-workspace", "reported-validator-worktree")
	profile, err := openhands.NewAcceptedExecutionProfile(f.modelDigest, f.runtimeDigest, f.toolDigest, f.effectDigest, 24, "reported_validator_fixture", "unused")
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := New(ctx, Config{Store: store, Catalogue: catalogue, Clock: clock, IDs: ids, OpenHandsBaseURL: "http://127.0.0.1:1", OpenHandsSessionAPIKey: "unused", HTTPClient: &http.Client{Timeout: time.Second}, WorkspaceBindings: []openhands.WorkspaceBinding{{WorkspaceID: f.workspaceID, WorktreeID: f.worktreeID, WorkingDirectory: t.TempDir()}}, ExecutionProfiles: []openhands.ExecutionProfile{profile}, RoleGrounding: testRoleGroundingResolver{}, OpenHandsPollInterval: time.Millisecond, OpenHandsMaximumPages: 8, OpenHandsMaximumEvidence: 1 << 20, EvidenceRoot: t.TempDir(), ExecutionPolicy: application.OperationalExecutionPolicy{OperationTimeout: time.Second, MaximumBriefBytes: 1 << 20, ConsumerID: "reported-validator-fixture", PolicyRevision: 1, ServiceAuthority: f.service, ExpiryAuthority: f.policy, Provenance: provenance}, EvidencePolicy: application.CommandEvidenceRecorderPolicy{PolicyRevision: 1, Authority: f.service, Provenance: provenance, ProducingVersion: "fixture", RetentionPolicy: "fixture"}, WorkerPolicy: executionruntime.Policy{ConsumerID: "reported-validator-fixture", LeaseDuration: 3 * time.Second, ReconciliationInterval: 10 * time.Millisecond, MaximumReconciliations: 20, MaximumConcurrentInvocations: 1, LeaseOperationTimeout: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(contextWithTimeout(t))
	f.createAuthoritativeTask(t, runtime, provenance)
	task, inv, _, _ := reportedValidatorBlockFixture(t)
	task.ID = f.taskID
	task.Owner = f.actor
	inv.TaskID = f.taskID
	inv.ActorFQN = f.actor
	inv.Execution = kernel.ExecutionTuple{ExecutionID: f.executionID, FencingEpoch: 1}
	inv.LastEventID = f.lastTaskEvent
	refs := []kernel.EvidenceRef{{EvidenceID: f.evidenceID, SHA256: digestByte('e')}}
	ref := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: f.taskID}
	before, err := store.LoadDecision(ctx, kernel.KernelCommand{Target: ref})
	if err != nil {
		t.Fatal(err)
	}
	block := f.command(t, "tekroo.command.work.block", kernel.SchemaVersion, kernel.AggregateTask, f.taskID, f.policy, f.taskRevision, map[string]any{"blocker_refs": []string{"teams://work-invocation/" + string(inv.ID)}, "reason": "task reported blocked: required deterministic gate unavailable", "review_policy": invalidStructuredReviewPolicy}, []kernel.DagParent{{ParentEventID: inv.LastEventID, EdgeKind: kernel.EdgeResponse}}, refs, nil)
	block.ActorFQN, block.Execution = &inv.ActorFQN, &inv.Execution
	receipt := applied(t, runtime, block, provenance)
	state, head, found, err := store.ReadAggregateHead(ctx, ref)
	if err != nil || !found || state.Condition != kernel.ConditionBlocked || head != receipt.EventIDs[0] {
		t.Fatalf("blocked state: %+v %v", state, err)
	}
	service := &ProductionService{Store: store, Runtime: runtime, provenance: provenance, clock: clock, ids: ids, policyAuthority: f.policy}
	kind, err := service.classifyTaskRecovery(ctx, task, state, head, inv)
	if err != nil || kind != taskRecoveryReportedValidatorBlock {
		t.Fatalf("classification: %v %v", kind, err)
	}
	if err := service.unblockInvalidStructuredTask(ctx, organization.FeatureRequest{ID: id(11400)}, task, state, head, inv, refs, "reported-validator-operator-repair"); err != nil {
		t.Fatal(err)
	}
	resumed, resumedHead, _, err := store.ReadAggregateHead(ctx, ref)
	if err != nil || resumed.Condition != kernel.ConditionRunnable || resumed.Phase != kernel.PhaseActive || resumed.LifecycleEpoch != state.LifecycleEpoch || resumed.ScopeRevision != state.ScopeRevision {
		t.Fatalf("resolved state: %+v %v", resumed, err)
	}
	event, found, err := store.ReadEvent(ctx, resumedHead)
	if err != nil || !found || event.EventType != "tekroo.event.work.unblocked" || len(event.Parents) != 1 || event.Parents[0] != (kernel.DagParent{ParentEventID: head, EdgeKind: kernel.EdgeResponse}) {
		t.Fatal("unblock lost exact response parent", err)
	}
	after, err := store.LoadDecision(ctx, kernel.KernelCommand{Target: ref})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.WorkBudgetAccounts, after.WorkBudgetAccounts) || !reflect.DeepEqual(before.WorkProfiles, after.WorkProfiles) || len(after.WorkInvocations) != 0 {
		t.Fatal("classification/unblock changed finite budgets, profiles, or spent model work")
	}
	if !reflect.DeepEqual(before.Reviews, after.Reviews) {
		t.Fatal("block resolution published validation or completion")
	}
}
