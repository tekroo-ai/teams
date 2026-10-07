//go:build mongo_integration

package operationalruntime

import (
	"context"
	"encoding/json"
	"errors"
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

// Real Mongo/kernel transitions with fixture provider-output bindings. No
// model server, live database, accepted feature, or feature source is changed.
func TestCandidateGateFindingBlocksOnlyItsVerifierAndRecoversOnChangedOutput(t *testing.T) {
	process, uri := startRuntimeMongod(t)
	defer stopRuntimeMongod(process)
	ctx, cancelTest := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancelTest()
	policy := integratedPolicy()
	policy.Grants[1].Scope.CommandTypes = append(policy.Grants[1].Scope.CommandTypes, "tekroo.command.work.block", "tekroo.command.work.unblock")
	store, err := mongo.Open(ctx, mongo.Config{URI: uri, Database: "candidate_gate_block_fixture", ContractIdentity: kernel.ContractIdentity, ManifestSHA256: phase4ManifestSHA, MigrationLevel: 1, Policy: policy, BacklogLimit: 1024, DeliveryPolicyRevision: 1})
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
	f := newIntegratedFixture(t, now, 33000, "teams::coder-1", "fixture-workspace", "fixture-worktree")
	other := newIntegratedFixture(t, now, 34000, "teams::coder-2", "other-workspace", "other-worktree")
	profile, err := openhands.NewAcceptedExecutionProfile(f.modelDigest, f.runtimeDigest, f.toolDigest, f.effectDigest, 24, "candidate_gate_block_fixture", "unused_fixture_memory")
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := New(ctx, Config{
		Store: store, Catalogue: catalogue, Clock: clock, IDs: ids,
		OpenHandsBaseURL: "http://127.0.0.1:1", OpenHandsSessionAPIKey: "unused-fixture", HTTPClient: &http.Client{Timeout: time.Second},
		WorkspaceBindings: []openhands.WorkspaceBinding{{WorkspaceID: f.workspaceID, WorktreeID: f.worktreeID, WorkingDirectory: t.TempDir()}},
		ExecutionProfiles: []openhands.ExecutionProfile{profile}, RoleGrounding: testRoleGroundingResolver{}, OpenHandsPollInterval: time.Millisecond,
		OpenHandsMaximumPages: 8, OpenHandsMaximumEvidence: 1 << 20, EvidenceRoot: t.TempDir(),
		ExecutionPolicy: application.OperationalExecutionPolicy{OperationTimeout: time.Second, MaximumBriefBytes: 1 << 20, ConsumerID: "gate-block-fixture", PolicyRevision: 1, ServiceAuthority: f.service, ExpiryAuthority: f.policy, Provenance: provenance},
		EvidencePolicy:  application.CommandEvidenceRecorderPolicy{PolicyRevision: 1, Authority: f.service, Provenance: provenance, ProducingVersion: "fixture", RetentionPolicy: "fixture"},
		WorkerPolicy:    executionruntime.Policy{ConsumerID: "gate-block-fixture", LeaseDuration: 3 * time.Second, ReconciliationInterval: 10 * time.Millisecond, MaximumReconciliations: 20, MaximumConcurrentInvocations: 1, LeaseOperationTimeout: time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(contextWithTimeout(t))
	f.createAuthoritativeTask(t, runtime, provenance)
	other.createAuthoritativeTask(t, runtime, provenance)
	dir, _, _ := candidateRepository(t)
	gate := ProductionCandidateGate{GateID: "compile", Command: []string{"git", "show", "HEAD:missing"}, Timeout: "1s"}
	manager := &candidateWorkspaceManager{root: t.TempDir(), gitBinary: "git", timeout: time.Second, gates: map[string]ProductionCandidateGate{gate.GateID: gate}}
	_, err = manager.runGates(ctx, dir, []string{gate.GateID})
	var failure *candidateGateFailure
	if !errors.As(err, &failure) {
		t.Fatal("expected real failing gate", err)
	}
	service := &ProductionService{Store: store, Runtime: runtime, provenance: provenance, clock: clock, ids: ids, policyAuthority: f.policy, serviceAuthority: f.service, candidates: manager}
	provider := organization.PlannedTask{ID: other.taskID, Purpose: kernel.PurposeImplementation}
	verifier := organization.PlannedTask{ID: f.taskID, Purpose: kernel.PurposeValidation, DependsOn: []kernel.UUIDv7{other.taskID}, Validates: []kernel.UUIDv7{other.taskID}}
	plan := organization.FeaturePlan{Tasks: []organization.PlannedTask{provider, verifier}}
	feature := organization.FeatureRequest{ID: candidateTestUUID(33900), LifecycleEpoch: 1, Input: organization.FeatureRequestInput{WorkspaceID: "engineering"}}
	output := digestByte('a')
	invocations := map[kernel.UUIDv7]kernel.WorkInvocation{provider.ID: {ID: other.invocationID, TaskID: provider.ID, State: kernel.InvocationSucceeded, OutputDigest: &output, TerminalEvidenceIDs: []kernel.UUIDv7{other.evidenceID}}}
	snapshot, err := store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: verifier.ID}})
	if err != nil {
		t.Fatal(err)
	}
	tracked := &trackedTask{plan: verifier, revision: f.taskRevision, last: f.lastTaskEvent, profile: snapshot.WorkProfiles[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: verifier.ID}].Profile, owner: organization.RoleInstanceState{ActorFQN: f.actor, Execution: kernel.ExecutionTuple{ExecutionID: f.executionID, FencingEpoch: 1}}}
	if err := service.blockCandidateGate(ctx, feature, plan, tracked, failure, invocations); err != nil {
		t.Fatal(err)
	}
	state, head, found, err := store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: verifier.ID})
	if err != nil || !found || state.Condition != kernel.ConditionBlocked {
		t.Fatalf("gate finding did not block task: %+v %v", state, err)
	}
	// A routine profile successor must not hide the unresolved blocker.
	currentProfile := tracked.profile
	successor := currentProfile.Clone()
	successor.ProfileID = candidateTestUUID(33901)
	successor.ProfileRevision++
	successor.SupersedesProfileID = &currentProfile.ProfileID
	successor.Budgets.DeadlineAt = successor.Budgets.DeadlineAt.Add(time.Minute)
	successor.ProfileDigest = ""
	encoded, err := json.Marshal(successor)
	if err != nil {
		t.Fatal(err)
	}
	successor.ProfileDigest = digestBytes(encoded)
	refs, err := evidenceRefsForIDs(snapshot, successor.ClassificationEvidenceIDs)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.applyTaskCommand(ctx, feature, tracked, "tekroo.command.task.bind-work-profile", kernel.SchemaVersion, service.policyAuthority, successor, refs, nil, "profile-maintenance"); err != nil {
		t.Fatal(err)
	}
	state, head, _, err = store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: verifier.ID})
	if err != nil || state.Condition != kernel.ConditionBlocked {
		t.Fatal("profile maintenance lost the block", err)
	}
	otherState, _, found, err := store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: provider.ID})
	if err != nil || !found || otherState.Condition != kernel.ConditionRunnable {
		t.Fatalf("unrelated task was blocked: %+v %v", otherState, err)
	}
	snapshot, err = store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: verifier.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.WorkInvocations) != 0 {
		t.Fatal("gate finding fabricated a model invocation")
	}
	states := map[kernel.UUIDv7]kernel.AggregateState{verifier.ID: state, provider.ID: otherState}
	heads := map[kernel.UUIDv7]kernel.UUIDv7{verifier.ID: head}
	for i := 0; i < 3; i++ {
		// Simulate service restart; only the registered finding and durable
		// lifecycle head survive, not a process-local blocked flag.
		service.candidates = &candidateWorkspaceManager{root: manager.root, gitBinary: "git", gates: manager.gates}
		changed, err := service.reconcileCandidateGateBlocks(ctx, feature, plan, states, heads, invocations, snapshot)
		if err != nil || changed {
			t.Fatal("unchanged finding did not stay blocked", err)
		}
	}
	output2 := digestByte('b')
	invocations[provider.ID] = kernel.WorkInvocation{ID: candidateTestUUID(34999), TaskID: provider.ID, State: kernel.InvocationSucceeded, OutputDigest: &output2, TerminalEvidenceIDs: []kernel.UUIDv7{other.evidenceID}}
	changed, err := service.reconcileCandidateGateBlocks(ctx, feature, plan, states, heads, invocations, snapshot)
	if err != nil || !changed {
		t.Fatal("changed output did not permit recovery", err)
	}
	state, _, _, err = store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: verifier.ID})
	if err != nil || state.Condition != kernel.ConditionRunnable {
		t.Fatal("changed output did not resolve block", err)
	}
	// Exercise the public pause wiring with the actual runtime worker, but
	// without starting a model invocation or runtime dispatch loop.
	service.Controller = &Controller{runtime: runtime, clock: clock, state: ControlRunning}
	service.planningDeadline = time.Minute
	op, cancel := service.featureRecoveryOperationContext(ctx)
	defer cancel()
	if err := service.Pause(ctx); err != nil || !errors.Is(op.Err(), context.Canceled) || !service.newInvocationAdmissionBlocked() {
		t.Fatal("public pause did not cancel gates and admission", err)
	}
}
