//go:build mongo_integration

package operationalruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/executionruntime"
	"github.com/tekroo-ai/teams/adapters/fake"
	"github.com/tekroo-ai/teams/adapters/mongo"
	"github.com/tekroo-ai/teams/adapters/openhands"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/contract"
	"github.com/tekroo-ai/teams/kernel"
	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const phase4ManifestSHA kernel.Digest = "c7eb4baae3a8312e44f9946fabde5a9cddb1937c7a41eb02d9b014ef21027ad1"

func TestAssembledRuntimeExecutesIndependentAuthorizedTasksConcurrentlyAndBuildsOperationalViews(t *testing.T) {
	process, uri := startRuntimeMongod(t)
	defer stopRuntimeMongod(process)

	now := time.Now().UTC().Truncate(time.Millisecond)
	policy := integratedPolicy()
	store, err := mongo.Open(contextWithTimeout(t), mongo.Config{
		URI: uri, Database: "tekroo_phase4_step7", ContractIdentity: kernel.ContractIdentity,
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
	workspace := filepath.Join(t.TempDir(), "task-worktree-1")
	secondWorkspace := filepath.Join(t.TempDir(), "task-worktree-2")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(secondWorkspace, 0o700); err != nil {
		t.Fatal(err)
	}
	serverState := &integratedOpenHands{t: t, conversations: make(map[string]*integratedConversation)}
	server := httptest.NewServer(http.HandlerFunc(serverState.serveHTTP))
	defer server.Close()

	fixture := newIntegratedFixture(t, now, 1000, "teams::coder-1", "workspace-phase4-step7-1", "worktree-phase4-step7-1")
	secondFixture := newIntegratedFixture(t, now, 2000, "teams::coder-2", "workspace-phase4-step7-2", "worktree-phase4-step7-2")
	profile, err := openhands.NewAcceptedExecutionProfile(
		fixture.modelDigest, fixture.runtimeDigest, fixture.toolDigest, fixture.effectDigest,
		24, "tekroo_phase4_step7", "sma_step15_memory",
	)
	if err != nil {
		t.Fatal(err)
	}
	clock := SystemClock{}
	identitySource, err := NewUUIDv7Source(clock)
	if err != nil {
		t.Fatal(err)
	}
	consumer := "teams-phase4-step7"
	runtime, err := New(contextWithTimeout(t), Config{
		Store: store, Catalogue: catalogue, Clock: clock, IDs: identitySource,
		OpenHandsBaseURL: server.URL, OpenHandsSessionAPIKey: "step7-session-key",
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
		WorkspaceBindings: []openhands.WorkspaceBinding{
			{WorkspaceID: fixture.workspaceID, WorktreeID: fixture.worktreeID, WorkingDirectory: workspace},
			{WorkspaceID: secondFixture.workspaceID, WorktreeID: secondFixture.worktreeID, WorkingDirectory: secondWorkspace},
		},
		ExecutionProfiles: []openhands.ExecutionProfile{profile}, OpenHandsPollInterval: time.Millisecond,
		OpenHandsMaximumPages: 8, OpenHandsMaximumEvidence: 1 << 20, EvidenceRoot: filepath.Join(t.TempDir(), "evidence"),
		ExecutionPolicy: application.OperationalExecutionPolicy{
			OperationTimeout: 2 * time.Second, MaximumBriefBytes: 1 << 20, ConsumerID: consumer,
			PolicyRevision: 1, ServiceAuthority: fixture.service, ExpiryAuthority: fixture.policy, Provenance: provenance,
		},
		EvidencePolicy: application.CommandEvidenceRecorderPolicy{
			PolicyRevision: 1, Authority: fixture.service, Provenance: provenance,
			ProducingVersion: "phase4-step7", RetentionPolicy: "phase4-step7",
		},
		WorkerPolicy: executionruntime.Policy{
			ConsumerID: consumer, LeaseDuration: 3 * time.Second, ReconciliationInterval: 10 * time.Millisecond,
			MaximumReconciliations: 20, MaximumConcurrentInvocations: 2, LeaseOperationTimeout: time.Second,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(contextWithTimeout(t))

	runContext, cancelRun := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- runtime.Run(runContext) }()

	fixture.createAuthoritativeTask(t, runtime, provenance)
	secondFixture.createAuthoritativeTask(t, runtime, provenance)
	fixture.authorizeInvocation(t, runtime, provenance)
	secondFixture.authorizeInvocation(t, runtime, provenance)

	deadline := time.Now().Add(10 * time.Second)
	for {
		first, firstErr := store.LoadOperationalExecution(contextWithTimeout(t), fixture.invocationID)
		second, secondErr := store.LoadOperationalExecution(contextWithTimeout(t), secondFixture.invocationID)
		if firstErr == nil && secondErr == nil && first.Invocation.State == kernel.InvocationSucceeded && second.Invocation.State == kernel.InvocationSucceeded {
			if first.Invocation.ConversationID == nil || *first.Invocation.ConversationID != string(fixture.invocationID) || first.Invocation.OutputDigest == nil {
				t.Fatalf("first terminal invocation = %#v", first.Invocation)
			}
			if second.Invocation.ConversationID == nil || *second.Invocation.ConversationID != string(secondFixture.invocationID) || second.Invocation.OutputDigest == nil {
				t.Fatalf("second terminal invocation = %#v", second.Invocation)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("invocations did not succeed: first=%s/%v second=%s/%v peak=%d", first.Invocation.State, firstErr, second.Invocation.State, secondErr, serverState.peakActive)
		}
		select {
		case runErr := <-runResult:
			t.Fatalf("runtime stopped before terminal state: %v", runErr)
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancelRun()
	if runErr := <-runResult; !errors.Is(runErr, context.Canceled) {
		t.Fatalf("runtime stop = %v", runErr)
	}

	projected, err := store.ProjectPendingOperationalEvents(contextWithTimeout(t), time.Now().UTC())
	if err != nil || projected == 0 {
		t.Fatalf("project events = %d, %v", projected, err)
	}
	for _, current := range []*integratedFixture{fixture, secondFixture} {
		taskView, found, readErr := store.ReadTaskProjection(contextWithTimeout(t), current.taskID)
		if readErr != nil || !found || !taskView.Valid() || taskView.LatestInvocation == nil || taskView.LatestInvocation.InvocationID != current.invocationID || taskView.LatestInvocation.State != kernel.InvocationSucceeded || taskView.Budget.ModelInvocationsUsed != 1 || taskView.OwnerFQN == nil || *taskView.OwnerFQN != current.actor {
			t.Fatalf("task projection %s = %#v, found=%t err=%v", current.taskID, taskView, found, readErr)
		}
		storyView, found, readErr := store.ReadStoryProjection(contextWithTimeout(t), current.storyID)
		if readErr != nil || !found || !storyView.Valid() || len(storyView.TaskIDs) != 1 || storyView.TaskIDs[0] != current.taskID {
			t.Fatalf("story projection %s = %#v, found=%t err=%v", current.storyID, storyView, found, readErr)
		}
	}
	serverState.assertAuthorizedPrompts(t, fixture, secondFixture)
	if serverState.peakActive < 2 {
		t.Fatalf("peak concurrent model-backed invocations = %d", serverState.peakActive)
	}
}

type integratedFixture struct {
	now                                                               time.Time
	storyID, taskID, evidenceID, executionID, profileID, assignmentID kernel.UUIDv7
	qualificationID                                                   kernel.UUIDv7
	budgetID, invocationID                                            kernel.UUIDv7
	actor                                                             kernel.ActorFQN
	human, policy, service                                            kernel.PrincipalRef
	modelDigest, runtimeDigest, toolDigest, effectDigest              kernel.Digest
	profileDigest, budgetPolicyDigest                                 kernel.Digest
	workspaceID, worktreeID                                           string
	title, description                                                string
	acceptanceCriteria                                                []string
	baselineSHA                                                       string
	writablePaths                                                     []string
	lastTaskEvent, lastBudgetEvent                                    kernel.UUIDv7
	taskRevision, budgetRevision                                      uint64
}

func newIntegratedFixture(t *testing.T, now time.Time, base int, actor kernel.ActorFQN, workspaceID, worktreeID string) *integratedFixture {
	t.Helper()
	return &integratedFixture{
		now:     now,
		storyID: id(base + 1), taskID: id(base + 2), evidenceID: id(base + 3), executionID: id(base + 4),
		profileID: id(base + 5), assignmentID: id(base + 6), budgetID: id(base + 7), invocationID: id(base + 8), qualificationID: id(base + 9),
		actor: actor, human: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"},
		policy:      kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "teams-admission-policy"},
		service:     kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "teams-operational-runtime"},
		modelDigest: digestByte('2'), runtimeDigest: digestByte('3'), toolDigest: digestByte('4'), effectDigest: digestByte('5'),
		profileDigest: digestByte('a'), budgetPolicyDigest: digestByte('b'),
		workspaceID: workspaceID, worktreeID: worktreeID,
		title: "Implement integrated path", description: "Use Teams authority and OpenHands execution.",
		acceptanceCriteria: []string{"terminal evidence is retained"}, baselineSHA: strings.Repeat("1", 40), writablePaths: []string{"src/"},
	}
}

type integratedCommandSubmitter func(*testing.T, kernel.KernelCommand) kernel.CommandReceipt

func runtimeCommandSubmitter(runtime *Runtime, provenance kernel.ProvenanceBasis) integratedCommandSubmitter {
	return func(t *testing.T, command kernel.KernelCommand) kernel.CommandReceipt {
		return applied(t, runtime, command, provenance)
	}
}

func (f *integratedFixture) createAuthoritativeTask(t *testing.T, runtime *Runtime, provenance kernel.ProvenanceBasis) {
	f.createAuthoritativeTaskWith(t, runtimeCommandSubmitter(runtime, provenance))
}

func (f *integratedFixture) createAuthoritativeTaskWith(t *testing.T, submit integratedCommandSubmitter) {
	t.Helper()
	story := f.command(t, "tekroo.command.story.create", kernel.SchemaVersion, kernel.AggregateStory, f.storyID, f.human, 0,
		map[string]any{"title": "Integrated runtime", "description": "Execute one authorized task.", "acceptance_criteria": []string{"authorized work completes"}}, nil, nil, nil)
	storyReceipt := submit(t, story)

	evidence := f.command(t, "tekroo.command.evidence.register", kernel.SchemaVersion, kernel.AggregateEvidence, f.evidenceID, f.human, 0,
		map[string]any{"access_partition": "engineering", "availability": "AVAILABLE", "byte_length": 2, "canonical_digest": nil, "computation": nil, "deletion_tombstone": nil, "evidence_kind": "TEST_RESULT", "integrity_state": "DIGEST_VERIFIED", "locator": "artifact://phase4/step7/precondition", "locator_immutable": true, "media_type": "application/json", "producing_component": "phase4-step7", "producing_version": "1", "redacts": nil, "retention_policy": "phase4-step7", "sensitivity": "INTERNAL", "sha256": digestByte('e'), "source_evidence_ids": []kernel.UUIDv7{}, "source_timestamp": nil, "transport_provenance": "teams://phase4-step7"}, nil, nil, nil)
	submit(t, evidence)
	evidenceRefs := []kernel.EvidenceRef{{EvidenceID: f.evidenceID, SHA256: digestByte('e')}}

	execution := f.command(t, "tekroo.command.execution.register", kernel.SchemaVersion, kernel.AggregateExecution, f.executionID, f.service, 0,
		map[string]any{"actor_fqn": f.actor, "execution_id": f.executionID, "fencing_epoch": 1, "runtime_identity": f.runtimeDigest}, nil, nil, nil)
	submit(t, execution)

	task := f.command(t, "tekroo.command.task.create", kernel.SchemaVersion, kernel.AggregateTask, f.taskID, f.human, 0,
		map[string]any{"story_id": f.storyID, "title": f.title, "description": f.description, "acceptance_criteria": f.acceptanceCriteria, "depends_on": []kernel.UUIDv7{}}, []kernel.DagParent{{ParentEventID: storyReceipt.EventIDs[0], EdgeKind: kernel.EdgeCausal}}, nil, nil)
	taskReceipt := submit(t, task)
	f.taskRevision, f.lastTaskEvent = 1, taskReceipt.EventIDs[0]

	profile := map[string]any{
		"task_id": f.taskID, "profile_id": f.profileID, "profile_revision": 1, "profile_digest": f.profileDigest,
		"lifecycle_epoch": 1, "scope_revision": 1, "work_kind": "IMPLEMENTATION", "ambiguity": "LOW", "novelty": "ROUTINE", "blast_radius": "LOCAL", "security_sensitivity": "ORDINARY",
		"minimum_decision_route": "BOUNDED_EXECUTION", "acceptance_criteria_digest": digestByte('c'), "required_deterministic_gate_ids": []string{"go-test"}, "required_validation_branches": 1,
		"required_independence_dimensions": []string{"PRINCIPAL", "ACTOR", "EXECUTION", "CONTEXT", "WORKSPACE", "METHOD"}, "implementation_variant_count": 1, "valid_candidate_quorum": 1,
		"verification_topology_digest": digestByte('d'), "classification_policy_revision": 1, "classification_policy_digest": digestByte('f'), "promotion_policy_revision": 1, "promotion_policy_digest": digestByte('6'),
		"budgets":                  map[string]any{"attempt_limit": 2, "review_round_limit": 2, "promotion_limit": 1, "escalation_limit": 1, "deadline_at": f.now.Add(2 * time.Hour)},
		"classification_authority": f.human, "classification_evidence_ids": []kernel.UUIDv7{f.evidenceID}, "supersedes_profile_id": nil,
	}
	f.applyTask(t, submit, "tekroo.command.task.bind-work-profile", kernel.SchemaVersion, f.policy, profile, evidenceRefs, nil)
	f.applyTask(t, submit, "tekroo.command.task.mark-ready", kernel.SchemaVersion, f.policy, map[string]any{"dependency_event_ids": []kernel.UUIDv7{}, "readiness_policy_revision": 1}, nil, nil)

	assignment := map[string]any{
		"assignment_id": f.assignmentID, "task_id": f.taskID, "expected_task_revision": f.taskRevision,
		"work_profile": f.workProfileBinding(), "required_decision_route": "BOUNDED_EXECUTION", "selected_decision_route": "BOUNDED_EXECUTION",
		"selected_actor_fqn": f.actor, "selected_execution_id": f.executionID, "selected_fencing_epoch": 1,
		"model_profile_digest": f.modelDigest, "runtime_identity_digest": f.runtimeDigest,
		"qualification":             map[string]any{"qualification_id": f.qualificationID, "qualification_digest": digestByte('7'), "qualification_corpus_digest": digestByte('8'), "model_profile_digest": f.modelDigest, "decision_route": "BOUNDED_EXECUTION", "qualified_role": "programmer", "status": "PASS", "observed_at": f.now.Add(-time.Minute)},
		"selection_policy_revision": 1, "selection_policy_digest": digestByte('9'),
		"hard_constraint_results": []map[string]any{{"constraint_id": "local-qualified-runtime", "outcome": "PASS", "evidence_ids": []kernel.UUIDv7{f.evidenceID}}},
		"selection_reasons":       []string{"exact accepted local runtime"}, "evidence_ids": []kernel.UUIDv7{f.evidenceID},
	}
	f.applyTask(t, submit, "tekroo.command.task.authorize-qualified-assignment", kernel.SchemaVersion, f.policy, assignment, evidenceRefs, nil)
	f.applyActorTask(t, submit, "tekroo.command.task.acquire-ownership", map[string]any{"owner_fqn": f.actor, "expected_ownership_version": 0})
	f.applyActorTask(t, submit, "tekroo.command.task.activate", map[string]any{"owner_fqn": f.actor, "ownership_version": 1})

	limits := make(kernel.PurposeCounters)
	for _, purpose := range kernel.AllWorkPurposes {
		limits[purpose] = 1
	}
	limits[kernel.PurposeImplementation] = 2
	budget := f.command(t, "tekroo.command.work-budget.create", kernel.OperationalSchemaVersion, kernel.AggregateWorkBudget, f.budgetID, f.policy, 0,
		map[string]any{"budget_account_id": f.budgetID, "root_work": map[string]any{"kind": "story", "id": f.storyID}, "lifecycle_epoch": 1, "policy_revision": 1, "policy_digest": f.budgetPolicyDigest, "model_invocation_limit": 2, "purpose_limits": limits, "deadline_at": f.now.Add(2 * time.Hour), "evidence_ids": []kernel.UUIDv7{f.evidenceID}, "authority": f.policy},
		[]kernel.DagParent{{ParentEventID: storyReceipt.EventIDs[0], EdgeKind: kernel.EdgeDerivation}}, evidenceRefs,
		[]kernel.AggregatePrecondition{{Aggregate: kernel.AggregateRef{Kind: kernel.AggregateStory, ID: f.storyID}, Expected: kernel.NewExpectedRevision(1)}})
	budgetReceipt := submit(t, budget)
	f.budgetRevision, f.lastBudgetEvent = 1, budgetReceipt.EventIDs[0]

	f.applyTask(t, submit, "tekroo.command.task.bind-work-budget", kernel.OperationalSchemaVersion, f.policy,
		map[string]any{"task_id": f.taskID, "budget_account_id": f.budgetID, "expected_task_revision": f.taskRevision, "lifecycle_epoch": 1, "scope_revision": 1, "task_model_invocation_limit": 2, "purpose_limits": limits, "evidence_ids": []kernel.UUIDv7{f.evidenceID}}, evidenceRefs,
		[]kernel.AggregatePrecondition{{Aggregate: kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: f.budgetID}, Expected: kernel.NewExpectedRevision(f.budgetRevision)}})
	f.applyTask(t, submit, "tekroo.command.task.bind-operational-scope", kernel.OperationalSchemaVersion, f.policy,
		map[string]any{"task_id": f.taskID, "expected_task_revision": f.taskRevision, "lifecycle_epoch": 1, "scope_revision": 1, "owner_fqn": f.actor, "execution_id": f.executionID, "fencing_epoch": 1, "workspace_id": f.workspaceID, "worktree_id": f.worktreeID, "branch": "phase4/step7", "baseline_sha": f.baselineSHA, "writable_paths": f.writablePaths, "interface_constraint_evidence_ids": []kernel.UUIDv7{f.evidenceID}}, evidenceRefs, nil)
}

func (f *integratedFixture) authorizeInvocation(t *testing.T, runtime *Runtime, provenance kernel.ProvenanceBasis) {
	f.authorizeInvocationWith(t, runtimeCommandSubmitter(runtime, provenance))
}

func (f *integratedFixture) authorizeInvocationWith(t *testing.T, submit integratedCommandSubmitter) {
	t.Helper()
	payload := map[string]any{
		"invocation_id": f.invocationID, "task_id": f.taskID, "budget_account_id": f.budgetID,
		"expected_budget_revision": f.budgetRevision, "expected_task_revision": f.taskRevision,
		"lifecycle_epoch": 1, "scope_revision": 1, "parent_event_id": f.lastTaskEvent,
		"work_profile": f.workProfileBinding(), "qualified_assignment_id": f.assignmentID,
		"purpose": "IMPLEMENTATION", "attempt_family": "implementation", "attempt_ordinal": 1,
		"condition_digest": digestByte('0'), "retry_of_invocation_id": nil, "retry_ordinal": 0,
		"output_predicate_digest": digestByte('1'), "allowed_terminal_outcomes": []string{"SUCCEEDED", "FAILED", "TIMED_OUT", "CANCELLED", "START_FAILED"},
		"tool_policy_digest": f.toolDigest, "effect_policy_digest": f.effectDigest,
		"actor_fqn": f.actor, "execution_id": f.executionID, "fencing_epoch": 1,
		"model_profile_digest": f.modelDigest, "runtime_identity_digest": f.runtimeDigest,
		"workspace_id": f.workspaceID, "deadline_at": f.now.Add(time.Hour),
		"idempotency_key": "phase4-step7-invocation", "admission_policy_revision": 1, "admission_policy_digest": f.budgetPolicyDigest,
	}
	command := f.command(t, "tekroo.command.work-invocation.authorize", kernel.OperationalSchemaVersion, kernel.AggregateWorkInvocation, f.invocationID, f.policy, 0, payload,
		[]kernel.DagParent{{ParentEventID: f.lastTaskEvent, EdgeKind: kernel.EdgeCausal}}, nil,
		[]kernel.AggregatePrecondition{
			{Aggregate: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: f.taskID}, Expected: kernel.NewExpectedRevision(f.taskRevision)},
			{Aggregate: kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: f.budgetID}, Expected: kernel.NewExpectedRevision(f.budgetRevision)},
		})
	command.IdempotencyKey = "phase4-step7-invocation"
	submit(t, command)
}

func (f *integratedFixture) requestCancellation(t *testing.T, runtime *Runtime, store *mongo.Store, provenance kernel.ProvenanceBasis, reason string) {
	t.Helper()
	current, err := store.LoadOperationalExecution(contextWithTimeout(t), f.invocationID)
	if err != nil {
		t.Fatal(err)
	}
	evidenceRefs := []kernel.EvidenceRef{{EvidenceID: f.evidenceID, SHA256: digestByte('e')}}
	payload := map[string]any{
		"invocation_id": f.invocationID, "expected_invocation_revision": current.Invocation.Revision,
		"reason": reason, "evidence_ids": []kernel.UUIDv7{f.evidenceID},
		"authority": f.human, "requested_at": time.Now().UTC(),
	}
	command := f.command(t, "tekroo.command.work-invocation.request-cancellation", kernel.OperationalSchemaVersion, kernel.AggregateWorkInvocation, f.invocationID, f.human, current.Invocation.Revision, payload,
		[]kernel.DagParent{{ParentEventID: current.Invocation.LastEventID, EdgeKind: kernel.EdgeCausal}}, evidenceRefs, nil)
	command.ExpectedLifecycleEpoch = nil
	applied(t, runtime, command, provenance)
}

func (f *integratedFixture) applyTask(t *testing.T, submit integratedCommandSubmitter, commandType, version string, authority kernel.PrincipalRef, payload any, evidence []kernel.EvidenceRef, preconditions []kernel.AggregatePrecondition) {
	t.Helper()
	command := f.command(t, commandType, version, kernel.AggregateTask, f.taskID, authority, f.taskRevision, payload,
		[]kernel.DagParent{{ParentEventID: f.lastTaskEvent, EdgeKind: kernel.EdgeCausal}}, evidence, preconditions)
	receipt := submit(t, command)
	f.taskRevision++
	f.lastTaskEvent = receipt.EventIDs[0]
}

func (f *integratedFixture) applyActorTask(t *testing.T, submit integratedCommandSubmitter, commandType string, payload any) {
	t.Helper()
	command := f.command(t, commandType, kernel.SchemaVersion, kernel.AggregateTask, f.taskID, kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: string(f.actor)}, f.taskRevision, payload,
		[]kernel.DagParent{{ParentEventID: f.lastTaskEvent, EdgeKind: kernel.EdgeCausal}}, nil, nil)
	execution := kernel.ExecutionTuple{ExecutionID: f.executionID, FencingEpoch: 1}
	command.ActorFQN = &f.actor
	command.Execution = &execution
	receipt := submit(t, command)
	f.taskRevision++
	f.lastTaskEvent = receipt.EventIDs[0]
}

func (f *integratedFixture) command(t *testing.T, commandType, version string, kind kernel.AggregateKind, targetID kernel.UUIDv7, authority kernel.PrincipalRef, revision uint64, payload any, parents []kernel.DagParent, evidence []kernel.EvidenceRef, preconditions []kernel.AggregatePrecondition) kernel.KernelCommand {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := NewUUIDv7Source(SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	commandID, err := ids.Next()
	if err != nil {
		t.Fatal(err)
	}
	correlationID, err := ids.Next()
	if err != nil {
		t.Fatal(err)
	}
	expected := kernel.MustNotExist()
	var epoch *uint64
	if revision > 0 {
		expected = kernel.NewExpectedRevision(revision)
		value := uint64(1)
		epoch = &value
	}
	return kernel.KernelCommand{
		ContractManifest: kernel.ContractIdentity, CommandID: commandID, CommandType: commandType, CommandVersion: version,
		Target: kernel.AggregateRef{Kind: kind, ID: targetID}, Authority: authority, ExpectedRevision: expected,
		ExpectedLifecycleEpoch: epoch, ExpectedPolicyRevision: 1, ExpectedCatalogueRevision: kernel.CatalogueRevision,
		IdempotencyKey: string(commandID), CorrelationID: correlationID, Causation: parents,
		Preconditions: preconditions, Payload: encoded, EvidenceRefs: evidence,
	}
}

func (f *integratedFixture) workProfileBinding() map[string]any {
	return map[string]any{"profile_id": f.profileID, "profile_revision": 1, "profile_digest": f.profileDigest, "lifecycle_epoch": 1, "scope_revision": 1}
}

func applied(t *testing.T, runtime *Runtime, command kernel.KernelCommand, provenance kernel.ProvenanceBasis) kernel.CommandReceipt {
	t.Helper()
	receipt, err := runtime.Handle(contextWithTimeout(t), command, provenance)
	if err != nil {
		t.Fatalf("%s: %v", command.CommandType, err)
	}
	if receipt.OutcomeCode != kernel.OutcomeApplied || len(receipt.EventIDs) != 1 {
		t.Fatalf("%s = %#v", command.CommandType, receipt)
	}
	return receipt
}

func integratedPolicy() kernel.AuthorizationPolicy {
	return kernel.AuthorizationPolicy{
		PolicyDigest: digestByte('9'), Revision: 1,
		Grants: []kernel.AuthorityGrant{
			{GrantDigest: digestByte('a'), Grantee: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"}, Scope: kernel.AuthorityScope{CommandTypes: []string{"tekroo.command.story.create", "tekroo.command.story.begin-planning", "tekroo.command.story.authorize", "tekroo.command.story.activate", "tekroo.command.task.create", "tekroo.command.evidence.register", "tekroo.command.work-invocation.request-cancellation", "tekroo.command.story.request-completion", "tekroo.command.story.approve-release", "tekroo.command.story.request-acceptance"}, TargetKinds: []kernel.AggregateKind{kernel.AggregateStory, kernel.AggregateTask, kernel.AggregateEvidence, kernel.AggregateWorkInvocation}, CanReadTarget: true}},
			{GrantDigest: digestByte('b'), Grantee: kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "teams-admission-policy"}, Scope: kernel.AuthorityScope{CommandTypes: []string{"tekroo.command.task.bind-work-profile", "tekroo.command.task.mark-ready", "tekroo.command.task.authorize-qualified-assignment", "tekroo.command.work-budget.create", "tekroo.command.task.bind-work-budget", "tekroo.command.task.bind-operational-scope", "tekroo.command.work-invocation.authorize", "tekroo.command.work-invocation.expire", "tekroo.command.completion-review.open", "tekroo.command.completion-review.finalize", "tekroo.command.release-plan.create", "tekroo.command.release-plan.finalize"}, TargetKinds: []kernel.AggregateKind{kernel.AggregateTask, kernel.AggregateStory, kernel.AggregateCompletionReview, kernel.AggregateReleasePlan, kernel.AggregateWorkBudget, kernel.AggregateWorkInvocation}, CanReadTarget: true}},
			{GrantDigest: digestByte('c'), Grantee: kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "teams-operational-runtime"}, Scope: kernel.AuthorityScope{CommandTypes: []string{"tekroo.command.execution.register", "tekroo.command.evidence.register", "tekroo.command.work-invocation.claim", "tekroo.command.work-invocation.record-started", "tekroo.command.work-invocation.record-terminal"}, TargetKinds: []kernel.AggregateKind{kernel.AggregateExecution, kernel.AggregateEvidence, kernel.AggregateWorkInvocation}, CanReadTarget: true}},
			{GrantDigest: digestByte('d'), Grantee: kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: "teams::coder-1"}, Scope: kernel.AuthorityScope{CommandTypes: []string{"tekroo.command.task.acquire-ownership", "tekroo.command.task.activate", "tekroo.command.task.request-completion"}, TargetKinds: []kernel.AggregateKind{kernel.AggregateTask}, CanReadTarget: true}},
			{GrantDigest: digestByte('e'), Grantee: kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: "teams::coder-2"}, Scope: kernel.AuthorityScope{CommandTypes: []string{"tekroo.command.task.acquire-ownership", "tekroo.command.task.activate"}, TargetKinds: []kernel.AggregateKind{kernel.AggregateTask}, CanReadTarget: true}},
			{GrantDigest: digestByte('1'), Grantee: kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "validation-service"}, Scope: kernel.AuthorityScope{CommandTypes: []string{"tekroo.command.completion-review.record-result"}, TargetKinds: []kernel.AggregateKind{kernel.AggregateCompletionReview}, CanReadTarget: true}},
		},
	}
}

type integratedConversation struct {
	workspace, requestDigest, prompt string
	created, submitted, finished     bool
	interrupted                      bool
	readyAt                          time.Time
}

type integratedOpenHands struct {
	t             *testing.T
	mu            sync.Mutex
	conversations map[string]*integratedConversation
	delays        map[string]time.Duration
	active        int
	peakActive    int
}

func (server *integratedOpenHands) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	server.mu.Lock()
	defer server.mu.Unlock()
	if request.Header.Get("X-Session-API-Key") != "step7-session-key" {
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if request.Method == http.MethodPost && request.URL.Path == "/api/conversations" {
		var payload map[string]any
		if json.NewDecoder(request.Body).Decode(&payload) != nil {
			server.t.Error("invalid create payload")
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		conversationID, _ := payload["conversation_id"].(string)
		metadata, _ := payload["observability_metadata"].(map[string]any)
		workspace, _ := payload["workspace"].(map[string]any)
		server.conversations[conversationID] = &integratedConversation{created: true, workspace: fmt.Sprint(workspace["working_dir"]), requestDigest: fmt.Sprint(metadata["tekroo_request_digest"])}
		writer.WriteHeader(http.StatusCreated)
		writeIntegratedJSON(writer, map[string]any{"id": conversationID})
		return
	}
	if len(parts) < 3 || parts[0] != "api" || parts[1] != "conversations" {
		writer.WriteHeader(http.StatusNotFound)
		return
	}
	conversationID := parts[2]
	conversation := server.conversations[conversationID]
	if conversation == nil {
		writer.WriteHeader(http.StatusNotFound)
		return
	}
	if request.Method == http.MethodGet && len(parts) == 3 {
		server.refresh(conversation)
		status := "idle"
		if conversation.submitted {
			status = "running"
		}
		if conversation.finished {
			status = "finished"
		}
		if conversation.interrupted {
			status = "paused"
		}
		writeIntegratedJSON(writer, map[string]any{"id": conversationID, "execution_status": status, "created_at": time.Now().UTC(), "updated_at": time.Now().UTC(), "workspace": map[string]any{"kind": "LocalWorkspace", "working_dir": conversation.workspace}, "tags": map[string]string{"tekrooinvocation": conversationID, "tekroorequest": conversation.requestDigest}})
		return
	}
	if request.Method == http.MethodPost && len(parts) == 4 && parts[3] == "events" {
		var payload struct {
			Role    string `json:"role"`
			Run     bool   `json:"run"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.NewDecoder(request.Body).Decode(&payload) != nil || payload.Role != "user" || !payload.Run || len(payload.Content) != 1 {
			server.t.Error("invalid event submission")
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		conversation.prompt = payload.Content[0].Text
		if !conversation.submitted {
			conversation.submitted = true
			delay := 50 * time.Millisecond
			if configured := server.delays[conversationID]; configured > 0 {
				delay = configured
			}
			conversation.readyAt = time.Now().Add(delay)
			server.active++
			if server.active > server.peakActive {
				server.peakActive = server.active
			}
		}
		writeIntegratedJSON(writer, map[string]any{"accepted": true})
		return
	}
	if request.Method == http.MethodPost && len(parts) == 4 && parts[3] == "interrupt" {
		if conversation.submitted && !conversation.finished {
			conversation.interrupted = true
			server.active--
		}
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	if request.Method == http.MethodGet && len(parts) == 5 && parts[3] == "events" && parts[4] == "search" {
		server.refresh(conversation)
		items := []any{}
		if conversation.submitted {
			items = []any{
				map[string]any{"id": "user-" + conversationID, "kind": "MessageEvent", "source": "user", "timestamp": time.Now().UTC(), "llm_message": map[string]any{"content": []map[string]any{{"type": "text", "text": conversation.prompt}}}},
			}
			if conversation.finished {
				agentText := "completed authorized task"
				var brief application.ExecutionBrief
				if json.Unmarshal([]byte(conversation.prompt), &brief) == nil && brief.ResultProtocol != nil && brief.ResultProtocol.Marker == application.ValidationResultMarker {
					agentText = "completed independent validation\n" + application.ValidationResultMarker + "\n{\"schema_version\":\"1.0.0\",\"outcome\":\"PASS\",\"reasons\":[\"repository checks passed\"]}"
				} else if brief.ResultProtocol != nil && brief.ResultProtocol.Marker == application.OrganizationalResultMarker {
					switch brief.Task.Title {
					case "Refine feature request":
						agentText = application.OrganizationalResultMarker + "\n{\"schema_version\":\"1.0.0\",\"result_type\":\"FEATURE_REFINEMENT\",\"acceptance_criteria\":[\"the requested behavior works\"],\"clarification_questions\":[],\"priority\":\"HIGH\"}"
					case "Specify feature stories":
						agentText = application.OrganizationalResultMarker + "\n{\"schema_version\":\"1.0.0\",\"result_type\":\"FEATURE_SPECIFICATION\",\"stories\":[{\"title\":\"Deliver behavior\",\"description\":\"Implement and verify the requested behavior.\",\"acceptance_criteria\":[\"the requested behavior works\"],\"priority\":\"HIGH\"}],\"design_constraints\":[\"preserve current interfaces\"]}"
					case "Design executable feature DAG":
						agentText = application.OrganizationalResultMarker + "\n{\"schema_version\":\"1.0.0\",\"result_type\":\"FEATURE_PLAN\",\"architecture\":\"One implementation followed by independent validation.\",\"design_decisions\":[\"use the existing interface\"],\"assumptions\":[],\"tasks\":[{\"story_index\":0,\"title\":\"Implement behavior\",\"description\":\"Implement the accepted behavior.\",\"acceptance_criteria\":[\"the requested behavior works\"],\"depends_on\":[],\"validates\":[],\"role\":\"coder\",\"purpose\":\"IMPLEMENTATION\",\"complexity\":3,\"risk\":\"LOW\",\"critical_path\":true,\"attempt_limit\":2,\"review_round_limit\":2}]}"
					}
				}
				items = append(items, map[string]any{"id": "agent-" + conversationID, "kind": "MessageEvent", "source": "agent", "timestamp": time.Now().UTC(), "llm_message": map[string]any{"content": []map[string]any{{"type": "text", "text": agentText}}}})
			}
		}
		writeIntegratedJSON(writer, map[string]any{"items": items, "next_page_id": nil})
		return
	}
	writer.WriteHeader(http.StatusNotFound)
}

func (server *integratedOpenHands) refresh(conversation *integratedConversation) {
	if conversation.submitted && !conversation.finished && !conversation.interrupted && !time.Now().Before(conversation.readyAt) {
		conversation.finished = true
		server.active--
	}
}

func (server *integratedOpenHands) assertAuthorizedPrompts(t *testing.T, fixtures ...*integratedFixture) {
	t.Helper()
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.conversations) != len(fixtures) {
		t.Fatalf("conversation count = %d", len(server.conversations))
	}
	for _, fixture := range fixtures {
		conversation := server.conversations[string(fixture.invocationID)]
		if conversation == nil || !conversation.submitted || !conversation.finished {
			t.Fatalf("conversation = %#v", conversation)
		}
		var brief application.ExecutionBrief
		if json.Unmarshal([]byte(conversation.prompt), &brief) != nil || brief.InvocationID != fixture.invocationID || brief.SemanticContext.TaskID != fixture.taskID || !brief.SemanticContext.NonAuthoritative || !brief.SemanticContext.NoTeamsAuthorityFallback || brief.CoordinationRule != "RETURN_EVIDENCE_AND_PROPOSALS_TO_TEAMS_ONLY;DO_NOT_ADDRESS_OR_INVOKE_ANOTHER_AGENT" {
			t.Fatalf("execution brief = %#v", brief)
		}
		digest := sha256.Sum256([]byte(conversation.prompt))
		if conversation.requestDigest != hex.EncodeToString(digest[:]) {
			t.Fatalf("request digest = %s", conversation.requestDigest)
		}
	}
}

func writeIntegratedJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(value)
}

func id(ordinal int) kernel.UUIDv7 {
	return kernel.UUIDv7(fmt.Sprintf("00000000-0000-7000-8000-%012x", ordinal))
}

func digestByte(value byte) kernel.Digest { return kernel.Digest(strings.Repeat(string(value), 64)) }

func contextWithTimeout(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

type runtimeMongod struct {
	command *exec.Cmd
	dir     string
}

func startRuntimeMongod(t *testing.T) (*runtimeMongod, string) {
	t.Helper()
	executable, err := exec.LookPath("mongod")
	if err != nil {
		t.Skipf("mongod is required: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	dir := t.TempDir()
	arguments := []string{"--bind_ip", "127.0.0.1", "--port", fmt.Sprint(port), "--dbpath", dir, "--logpath", filepath.Join(dir, "mongod.log"), "--oplogSize", "128", "--replSet", "tekroo-step7"}
	command := exec.Command(executable, arguments...)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	process := &runtimeMongod{command: command, dir: dir}
	uri := fmt.Sprintf("mongodb://127.0.0.1:%d/?directConnection=true", port)
	client, err := driver.Connect(options.Client().ApplyURI(uri).SetServerSelectionTimeout(500 * time.Millisecond))
	if err != nil {
		stopRuntimeMongod(process)
		t.Fatal(err)
	}
	defer client.Disconnect(context.Background())
	deadline := time.Now().Add(20 * time.Second)
	for client.Ping(context.Background(), nil) != nil {
		if time.Now().After(deadline) {
			stopRuntimeMongod(process)
			t.Fatal("mongod did not start")
		}
		time.Sleep(50 * time.Millisecond)
	}
	configuration := bson.D{{Key: "_id", Value: "tekroo-step7"}, {Key: "members", Value: bson.A{bson.D{{Key: "_id", Value: 0}, {Key: "host", Value: fmt.Sprintf("127.0.0.1:%d", port)}}}}}
	if err := client.Database("admin").RunCommand(contextWithTimeout(t), bson.D{{Key: "replSetInitiate", Value: configuration}}).Err(); err != nil {
		stopRuntimeMongod(process)
		t.Fatal(err)
	}
	deadline = time.Now().Add(20 * time.Second)
	for {
		var hello struct {
			Primary bool `bson:"isWritablePrimary"`
		}
		err = client.Database("admin").RunCommand(context.Background(), bson.D{{Key: "hello", Value: 1}}).Decode(&hello)
		if err == nil && hello.Primary {
			break
		}
		if time.Now().After(deadline) {
			stopRuntimeMongod(process)
			t.Fatalf("replica set not primary: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	return process, uri
}

func stopRuntimeMongod(process *runtimeMongod) {
	if process == nil || process.command == nil || process.command.Process == nil {
		return
	}
	_ = process.command.Process.Signal(os.Interrupt)
	done := make(chan struct{})
	go func() { _ = process.command.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = process.command.Process.Kill()
		<-done
	}
}

func closeRuntimeStore(t *testing.T, store *mongo.Store) {
	t.Helper()
	if err := store.Close(contextWithTimeout(t)); err != nil && !errors.Is(err, fs.ErrClosed) {
		t.Errorf("close store: %v", err)
	}
}
