//go:build mongo_integration

package operationalruntime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/fake"
	"github.com/tekroo-ai/teams/adapters/mongo"
	"github.com/tekroo-ai/teams/kernel"
)

func TestTekroodAndTekrooExecuteNormalTaskThroughSupportedSurface(t *testing.T) {
	process, uri := startRuntimeMongod(t)
	defer stopRuntimeMongod(process)

	now := time.Now().UTC().Truncate(time.Millisecond)
	fixture := newIntegratedFixture(t, now, 7000, "teams::coder-1", "phase5-product-workspace", "phase5-product-worktree")
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	serverState := &integratedOpenHands{t: t, conversations: make(map[string]*integratedConversation)}
	openHands := httptest.NewServer(http.HandlerFunc(serverState.serveHTTP))
	defer openHands.Close()

	configPath := writeProductSurfaceConfiguration(t, uri, openHands.URL, workspace, fixture)
	bootstrapConfig, err := LoadProductionConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := NewProductionService(contextWithTimeout(t), bootstrapConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := bootstrap.Close(contextWithTimeout(t)); closeErr != nil {
			t.Errorf("close bootstrap service: %v", closeErr)
		}
	}()
	tekrood, tekroo := buildProductSurfaceBinaries(t)
	var stdout, stderr bytes.Buffer
	service := exec.Command(tekrood, "-config", configPath)
	service.Stdout = &stdout
	service.Stderr = &stderr
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	serviceStopped := false
	defer func() {
		if serviceStopped {
			return
		}
		_ = service.Process.Kill()
		_ = service.Wait()
	}()

	waitForProductHealth(t, tekroo, configPath, &stderr)
	productCLI(t, tekroo, configPath, "pause")
	productCLI(t, tekroo, configPath, "status")
	productCLI(t, tekroo, configPath, "resume")
	// A persistent service must remain healthy while idle across multiple
	// bounded change-stream polls before the first operator command arrives.
	time.Sleep(1500 * time.Millisecond)

	commandDirectory := filepath.Join(t.TempDir(), "commands")
	if err := os.Mkdir(commandDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	submit := func(t *testing.T, command kernel.KernelCommand) kernel.CommandReceipt {
		t.Helper()
		receipt, submitErr := bootstrap.Submit(contextWithTimeout(t), command)
		if submitErr != nil {
			t.Fatalf("submit %s: %v", command.CommandType, submitErr)
		}
		if receipt.OutcomeCode != kernel.OutcomeApplied || len(receipt.EventIDs) != 1 {
			t.Fatalf("%s receipt = %#v", command.CommandType, receipt)
		}
		return receipt
	}
	fixture.createAuthoritativeTaskWith(t, submit)
	storyAtCreation := waitForProductStory(t, tekroo, configPath, fixture.storyID, func(view mongo.StoryProjection) bool {
		return view.AggregateRevision == 1 && len(view.TaskIDs) == 1
	})
	activateProductStory(t, fixture, storyAtCreation, submit)
	fixture.authorizeInvocationWith(t, submit)

	task := waitForProductTask(t, tekroo, configPath, fixture.taskID, func(view mongo.TaskProjection) bool {
		return view.LatestInvocation != nil && view.LatestInvocation.State == kernel.InvocationSucceeded
	})
	if task.OwnerFQN == nil || *task.OwnerFQN != fixture.actor || task.Budget.ModelInvocationsUsed != 1 || task.LatestInvocation.InvocationID != fixture.invocationID {
		t.Fatalf("terminal task projection = %#v", task)
	}
	storyRaw := productCLI(t, tekroo, configPath, "story", string(fixture.storyID))
	var story mongo.StoryProjection
	if err := json.Unmarshal(storyRaw, &story); err != nil || len(story.TaskIDs) != 1 || story.TaskIDs[0] != fixture.taskID {
		t.Fatalf("story projection = %#v err=%v raw=%s", story, err, storyRaw)
	}
	serverState.assertAuthorizedPrompts(t, fixture)
	completeAndAcceptProductStory(t, fixture, task, story, submit)
	waitForProductTask(t, tekroo, configPath, fixture.taskID, func(view mongo.TaskProjection) bool {
		return view.Phase == string(kernel.PhaseCompleted) && view.Validation.State == "PASS" && view.Completion.State == "COMPLETED"
	})
	waitForProductStory(t, tekroo, configPath, fixture.storyID, func(view mongo.StoryProjection) bool {
		return view.Phase == string(kernel.PhaseAccepted) && view.Completion.State == "COMPLETED" && view.Acceptance.State == "ACCEPTED" && view.Release.State == string(kernel.ReleaseNotRequired)
	})

	cancelFixture := newIntegratedFixture(t, now, 8000, "teams::coder-2", fixture.workspaceID, fixture.worktreeID)
	serverState.mu.Lock()
	serverState.delays = map[string]time.Duration{string(cancelFixture.invocationID): 5 * time.Second}
	serverState.mu.Unlock()
	cancelFixture.createAuthoritativeTaskWith(t, submit)
	cancelFixture.authorizeInvocationWith(t, submit)
	active := waitForProductInvocation(t, tekroo, configPath, cancelFixture.invocationID, func(status InvocationStatus) bool {
		return status.State == kernel.InvocationStarted
	})
	cancelRequest := CancellationRequest{ExpectedRevision: active.Revision, Reason: "operator product-surface cancellation", EvidenceRefs: []kernel.EvidenceRef{{EvidenceID: cancelFixture.evidenceID, SHA256: digestByte('e')}}, IdempotencyKey: "product-surface-cancel"}
	cancelPath := filepath.Join(commandDirectory, "cancellation-request.json")
	writeJSON(t, cancelPath, cancelRequest, 0o600)
	cancelRaw := productCLI(t, tekroo, configPath, "cancel", string(cancelFixture.invocationID), cancelPath)
	var cancellationStatus InvocationStatus
	if err := json.Unmarshal(cancelRaw, &cancellationStatus); err != nil || cancellationStatus.CancellationRequestedAt == nil {
		t.Fatalf("cancellation status = %#v err=%v raw=%s", cancellationStatus, err, cancelRaw)
	}
	cancelled := waitForProductInvocation(t, tekroo, configPath, cancelFixture.invocationID, func(status InvocationStatus) bool {
		return status.State == kernel.InvocationCancelled
	})
	if cancelled.TerminalOutcome == nil || *cancelled.TerminalOutcome != kernel.InvocationCancelled || cancelled.CancellationRequestedAt == nil {
		t.Fatalf("cancelled invocation = %#v", cancelled)
	}
	waitForProductTask(t, tekroo, configPath, cancelFixture.taskID, func(view mongo.TaskProjection) bool {
		return view.LatestInvocation != nil && view.LatestInvocation.State == kernel.InvocationCancelled
	})
	serverState.mu.Lock()
	interrupted := serverState.conversations[string(cancelFixture.invocationID)] != nil && serverState.conversations[string(cancelFixture.invocationID)].interrupted
	serverState.mu.Unlock()
	if !interrupted {
		t.Fatal("OpenHands cancellation was not observed")
	}

	productCLI(t, tekroo, configPath, "stop")
	waitResult := make(chan error, 1)
	go func() { waitResult <- service.Wait() }()
	select {
	case err := <-waitResult:
		if err != nil {
			t.Fatalf("tekrood stop: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
		}
		serviceStopped = true
	case <-time.After(20 * time.Second):
		t.Fatalf("tekrood did not stop\nstdout=%s\nstderr=%s", stdout.String(), stderr.String())
	}
}

func buildProductSurfaceBinaries(t *testing.T) (string, string) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	tekrood := filepath.Join(directory, "tekrood")
	tekroo := filepath.Join(directory, "tekroo")
	for output, source := range map[string]string{tekrood: "./cmd/tekrood", tekroo: "./cmd/tekroo"} {
		command := exec.Command("go", "build", "-trimpath", "-o", output, source)
		command.Dir = root
		if raw, err := command.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", source, err, raw)
		}
	}
	return tekrood, tekroo
}

func writeProductSurfaceConfiguration(t *testing.T, mongoURI, openHandsURL, workspace string, fixture *integratedFixture) string {
	t.Helper()
	path, config := writeProductionFixture(t)
	directory := filepath.Dir(path)
	writeText(t, filepath.Join(directory, config.Mongo.URIFile), mongoURI+"\n", 0o600)
	writeText(t, filepath.Join(directory, config.OpenHands.SessionAPIKeyFile), "step7-session-key\n", 0o600)
	provenance, err := fake.ProvenanceBasis()
	if err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(directory, config.AuthorizationPolicyFile), integratedPolicy(), 0o600)
	writeJSON(t, filepath.Join(directory, config.ProvenanceFile), provenance, 0o600)
	database := fmt.Sprintf("tekroo_phase5_product_%d", time.Now().UnixNano())
	config.Mongo.Database = database
	config.TeamsDatabaseIdentity = database
	config.SMADatabaseIdentity = "sma_phase5_product_disposable"
	config.DeploymentIdentity = digestByte('6')
	config.OpenHands.BaseURL = openHandsURL
	config.Operator.Address = freeProductSurfaceAddress(t)
	config.Operator.Principal = fixture.human
	config.Workspaces = []ProductionWorkspace{{WorkspaceID: fixture.workspaceID, WorktreeID: fixture.worktreeID, WorkingDirectory: workspace, Branch: "task/product-surface", BaselineSHA: strings.Repeat("1", 40), WritablePaths: []string{"."}}}
	qualification := config.Profiles[0].Qualification
	qualification.ModelProfileDigest = fixture.modelDigest
	config.Profiles = []ProductionProfile{{ModelProfileDigest: fixture.modelDigest, RuntimeIdentityDigest: fixture.runtimeDigest, ToolPolicyDigest: fixture.toolDigest, EffectPolicyDigest: fixture.effectDigest, MaximumIterations: 24, Qualification: qualification}}
	config.Execution.ConsumerID = "teams-phase5-product-surface"
	config.Execution.OperationTimeout = "3s"
	config.Worker.LeaseDuration = "4s"
	config.Worker.ReconciliationInterval = "10ms"
	config.Worker.MaximumReconciliations = 20
	config.Worker.MaximumConcurrentInvocations = 2
	config.Worker.LeaseOperationTimeout = "1s"
	config.Projection.Interval = "10ms"
	config.Projection.OperationTimeout = "1s"
	writeJSON(t, path, config, 0o600)
	return path
}

func freeProductSurfaceAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func productCLI(t *testing.T, binary, config string, arguments ...string) []byte {
	t.Helper()
	commandArguments := append([]string{"-config", config}, arguments...)
	command := exec.Command(binary, commandArguments...)
	raw, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("tekroo %s: %v\n%s", strings.Join(arguments, " "), err, raw)
	}
	return raw
}

func waitForProductHealth(t *testing.T, tekroo, config string, serviceStderr *bytes.Buffer) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		command := exec.Command(tekroo, "-config", config, "health")
		if raw, err := command.CombinedOutput(); err == nil {
			return
		} else if time.Now().After(deadline) {
			t.Fatalf("tekrood health timeout: %v\n%s\nservice stderr=%s", err, raw, serviceStderr.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitForProductTask(t *testing.T, tekroo, config string, id kernel.UUIDv7, predicate func(mongo.TaskProjection) bool) mongo.TaskProjection {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		raw := productCLI(t, tekroo, config, "task", string(id))
		var view mongo.TaskProjection
		if err := json.Unmarshal(raw, &view); err != nil {
			t.Fatalf("decode task projection: %v\n%s", err, raw)
		}
		if predicate(view) {
			return view
		}
		if time.Now().After(deadline) {
			t.Fatalf("task did not reach expected state: %#v", view)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitForProductInvocation(t *testing.T, tekroo, config string, id kernel.UUIDv7, predicate func(InvocationStatus) bool) InvocationStatus {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		raw := productCLI(t, tekroo, config, "invocation", string(id))
		var status InvocationStatus
		if err := json.Unmarshal(raw, &status); err != nil {
			t.Fatalf("decode invocation status: %v\n%s", err, raw)
		}
		if predicate(status) {
			return status
		}
		if time.Now().After(deadline) {
			t.Fatalf("invocation did not reach expected state: %#v", status)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitForProductStory(t *testing.T, tekroo, config string, id kernel.UUIDv7, predicate func(mongo.StoryProjection) bool) mongo.StoryProjection {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		raw := productCLI(t, tekroo, config, "story", string(id))
		var view mongo.StoryProjection
		if err := json.Unmarshal(raw, &view); err != nil {
			t.Fatalf("decode story projection: %v\n%s", err, raw)
		}
		if predicate(view) {
			return view
		}
		if time.Now().After(deadline) {
			t.Fatalf("story did not reach expected state: %#v", view)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func completeAndAcceptProductStory(t *testing.T, fixture *integratedFixture, task mongo.TaskProjection, story mongo.StoryProjection, submit integratedCommandSubmitter) {
	t.Helper()
	evidenceRefs := []kernel.EvidenceRef{{EvidenceID: fixture.evidenceID, SHA256: digestByte('e')}}
	taskReviewID := id(9010)
	taskFinalized := completeProductSubject(t, fixture, submit, taskReviewID, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: fixture.taskID}, task.AggregateRevision, task.LastEventID, "task output passed its required test", evidenceRefs)
	taskCompletionPayload := map[string]any{
		"artifact_digests": []kernel.Digest{digestByte('7')}, "branch_policy_revision": uint64(1),
		"completion_review_id": taskReviewID, "completion_review_revision": uint64(2), "criteria_revision": uint64(1),
		"evidence_ids": []kernel.UUIDv7{fixture.evidenceID}, "lifecycle_epoch": uint64(1), "owner_fqn": fixture.actor,
		"unresolved_exceptions": []string{}, "validation_finalized_event_id": taskFinalized.EventIDs[0],
	}
	taskCompletion := fixture.command(t, "tekroo.command.task.request-completion", kernel.SchemaVersion, kernel.AggregateTask, fixture.taskID, kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: string(fixture.actor)}, task.AggregateRevision, taskCompletionPayload,
		[]kernel.DagParent{{ParentEventID: taskFinalized.EventIDs[0], EdgeKind: kernel.EdgeResponse}}, evidenceRefs,
		[]kernel.AggregatePrecondition{{Aggregate: kernel.AggregateRef{Kind: kernel.AggregateCompletionReview, ID: taskReviewID}, Expected: kernel.NewExpectedRevision(3)}})
	execution := kernel.ExecutionTuple{ExecutionID: fixture.executionID, FencingEpoch: 1}
	taskCompletion.ActorFQN = &fixture.actor
	taskCompletion.Execution = &execution
	taskCompleted := submit(t, taskCompletion)

	storyReviewID := id(9020)
	storyFinalized := completeProductSubject(t, fixture, submit, storyReviewID, kernel.AggregateRef{Kind: kernel.AggregateStory, ID: fixture.storyID}, story.AggregateRevision, taskCompleted.EventIDs[0], "story task and evidence passed review", evidenceRefs)
	storyCompletionPayload := map[string]any{
		"artifact_digests": []kernel.Digest{digestByte('7')}, "branch_policy_revision": uint64(1),
		"completion_review_id": storyReviewID, "completion_review_revision": uint64(2), "criteria_revision": uint64(1),
		"evidence_ids": []kernel.UUIDv7{fixture.evidenceID}, "lifecycle_epoch": uint64(1),
		"unresolved_exceptions": []string{}, "validation_finalized_event_id": storyFinalized.EventIDs[0],
	}
	storyCompletion := fixture.command(t, "tekroo.command.story.request-completion", kernel.SchemaVersion, kernel.AggregateStory, fixture.storyID, fixture.human, story.AggregateRevision, storyCompletionPayload,
		[]kernel.DagParent{{ParentEventID: storyFinalized.EventIDs[0], EdgeKind: kernel.EdgeResponse}, {ParentEventID: taskCompleted.EventIDs[0], EdgeKind: kernel.EdgeResponse}}, evidenceRefs,
		[]kernel.AggregatePrecondition{
			{Aggregate: kernel.AggregateRef{Kind: kernel.AggregateCompletionReview, ID: storyReviewID}, Expected: kernel.NewExpectedRevision(3)},
			{Aggregate: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: fixture.taskID}, Expected: kernel.NewExpectedRevision(task.AggregateRevision + 1)},
		})
	storyCompleted := submit(t, storyCompletion)

	approvalPayload := map[string]any{
		"story_id": fixture.storyID, "lifecycle_epoch": uint64(1), "expected_story_revision": story.AggregateRevision + 1,
		"author": fixture.human, "approval_revision": uint64(1), "release_policy_revision": uint64(1),
		"reasons": []string{"principal approves the completed story for release disposition"}, "evidence_ids": []kernel.UUIDv7{fixture.evidenceID},
	}
	approval := fixture.command(t, "tekroo.command.story.approve-release", kernel.SchemaVersion, kernel.AggregateStory, fixture.storyID, fixture.human, story.AggregateRevision+1, approvalPayload,
		[]kernel.DagParent{{ParentEventID: storyCompleted.EventIDs[0], EdgeKind: kernel.EdgeCausal}}, evidenceRefs, nil)
	approved := submit(t, approval)

	releasePlanID := id(9030)
	planDigest := digestByte('3')
	releaseCreatePayload := map[string]any{
		"release_plan_id": releasePlanID, "story_id": fixture.storyID, "story_lifecycle_epoch": uint64(1),
		"expected_story_revision": story.AggregateRevision + 2, "author": fixture.human,
		"author_approval_event_id": approved.EventIDs[0], "author_approval_revision": story.AggregateRevision + 2,
		"release_policy_revision": uint64(1), "plan_digest": planDigest, "evidence_ids": []kernel.UUIDv7{fixture.evidenceID},
		"release_mode": kernel.ReleaseModeNotRequired, "no_release_reason": "operator workflow changes no repository content",
	}
	releaseCreate := fixture.command(t, "tekroo.command.release-plan.create", kernel.SchemaVersion, kernel.AggregateReleasePlan, releasePlanID, fixture.policy, 0, releaseCreatePayload,
		[]kernel.DagParent{{ParentEventID: approved.EventIDs[0], EdgeKind: kernel.EdgeResponse}}, evidenceRefs,
		[]kernel.AggregatePrecondition{{Aggregate: kernel.AggregateRef{Kind: kernel.AggregateStory, ID: fixture.storyID}, Expected: kernel.NewExpectedRevision(story.AggregateRevision + 2)}})
	releaseOpened := submit(t, releaseCreate)
	releaseFinalizePayload := map[string]any{
		"release_plan_id": releasePlanID, "expected_release_revision": uint64(1), "plan_digest": planDigest,
		"release_mode": kernel.ReleaseModeNotRequired, "terminal_status": kernel.ReleaseNotRequired,
		"reasons": []string{"operator workflow changes no repository content"}, "evidence_ids": []kernel.UUIDv7{fixture.evidenceID},
	}
	releaseFinalize := fixture.command(t, "tekroo.command.release-plan.finalize", kernel.SchemaVersion, kernel.AggregateReleasePlan, releasePlanID, fixture.policy, 1, releaseFinalizePayload,
		[]kernel.DagParent{{ParentEventID: releaseOpened.EventIDs[0], EdgeKind: kernel.EdgeCausal}}, evidenceRefs, nil)
	releaseFinalize.ExpectedLifecycleEpoch = nil
	releaseFinalized := submit(t, releaseFinalize)
	acceptancePayload := map[string]any{
		"acceptance_policy_revision": uint64(1), "evidence_ids": []kernel.UUIDv7{fixture.evidenceID}, "lifecycle_epoch": uint64(1),
		"release_finalized_event_id": releaseFinalized.EventIDs[0], "release_mode": kernel.ReleaseModeNotRequired,
		"release_plan_id": releasePlanID, "release_plan_revision": uint64(2),
	}
	acceptance := fixture.command(t, "tekroo.command.story.request-acceptance", kernel.SchemaVersion, kernel.AggregateStory, fixture.storyID, fixture.human, story.AggregateRevision+2, acceptancePayload,
		[]kernel.DagParent{{ParentEventID: releaseFinalized.EventIDs[0], EdgeKind: kernel.EdgeResponse}}, evidenceRefs,
		[]kernel.AggregatePrecondition{{Aggregate: kernel.AggregateRef{Kind: kernel.AggregateReleasePlan, ID: releasePlanID}, Expected: kernel.NewExpectedRevision(2)}})
	submit(t, acceptance)
}

func activateProductStory(t *testing.T, fixture *integratedFixture, story mongo.StoryProjection, submit integratedCommandSubmitter) {
	t.Helper()
	authorize := fixture.command(t, "tekroo.command.story.authorize", kernel.SchemaVersion, kernel.AggregateStory, fixture.storyID, fixture.human, story.AggregateRevision,
		map[string]any{"reason": "scope approved for the product workflow", "scope_revision": uint64(1)},
		[]kernel.DagParent{{ParentEventID: story.LastEventID, EdgeKind: kernel.EdgeCausal}}, nil, nil)
	authorized := submit(t, authorize)
	begin := fixture.command(t, "tekroo.command.story.begin-planning", kernel.SchemaVersion, kernel.AggregateStory, fixture.storyID, fixture.human, story.AggregateRevision+1,
		map[string]any{"accountable_owner_fqn": "teams::pm-1", "planning_budget": uint64(4)},
		[]kernel.DagParent{{ParentEventID: authorized.EventIDs[0], EdgeKind: kernel.EdgeCausal}}, nil, nil)
	planning := submit(t, begin)
	activate := fixture.command(t, "tekroo.command.story.activate", kernel.SchemaVersion, kernel.AggregateStory, fixture.storyID, fixture.human, story.AggregateRevision+2,
		map[string]any{"plan_digest": digestByte('c'), "required_task_ids": []kernel.UUIDv7{fixture.taskID}},
		[]kernel.DagParent{{ParentEventID: planning.EventIDs[0], EdgeKind: kernel.EdgeCausal}}, nil, nil)
	submit(t, activate)
}

func completeProductSubject(t *testing.T, fixture *integratedFixture, submit integratedCommandSubmitter, reviewID kernel.UUIDv7, subject kernel.AggregateRef, subjectRevision uint64, parentEventID kernel.UUIDv7, criterion string, evidenceRefs []kernel.EvidenceRef) kernel.CommandReceipt {
	t.Helper()
	deadline := time.Now().UTC().Add(time.Hour)
	validator := kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "validation-service"}
	implementer := map[string]any{
		"principal": kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: string(fixture.actor)}, "actor_fqn": fixture.actor,
		"execution_id": fixture.executionID, "fencing_epoch": uint64(1), "model_profile_digest": fixture.modelDigest,
		"workspace_digest": digestByte('8'), "context_digest": digestByte('9'),
	}
	openPayload := map[string]any{
		"subject_kind": subject.Kind, "subject_id": subject.ID, "lifecycle_epoch": uint64(1), "criteria_revision": uint64(1),
		"evidence_set_digest": digestByte('e'), "branch_policy_revision": uint64(1),
		"branches": []map[string]any{{
			"branch_id": "tests", "validator": validator, "resolution_owner_fqn": fixture.actor,
			"acceptance_criteria": []string{criterion}, "input_evidence_ids": []kernel.UUIDv7{fixture.evidenceID},
			"deadline_at": deadline, "round_limit": uint64(1),
			"required_independence_dimensions": []string{"PRINCIPAL", "METHOD"}, "required_method_ids": []string{"operator-product-surface"},
		}},
		"join_rule": "ALL_PASS", "partial_result_policy": "WAIT_ALL",
		"adjudication":   map[string]any{"adjudicator": fixture.policy, "deadline_at": deadline, "round_limit": uint64(1)},
		"scope_revision": uint64(1), "work_profile": fixture.workProfileBinding(), "candidate_artifact_digest": digestByte('7'),
		"implementer": implementer, "verification_topology_digest": digestByte('d'), "variant_group_id": nil,
	}
	open := fixture.command(t, "tekroo.command.completion-review.open", kernel.SchemaVersion, kernel.AggregateCompletionReview, reviewID, fixture.policy, 0, openPayload,
		[]kernel.DagParent{{ParentEventID: parentEventID, EdgeKind: kernel.EdgeCausal}}, nil,
		[]kernel.AggregatePrecondition{{Aggregate: subject, Expected: kernel.NewExpectedRevision(subjectRevision)}})
	opened := submit(t, open)
	resultPayload := map[string]any{
		"review_id": reviewID, "branch_id": "tests", "branch_policy_revision": uint64(1), "source_role": "VALIDATOR", "round": uint64(1),
		"result": "PASS", "reasons": []string{"validated through the supported operator surface"}, "evidence_ids": []kernel.UUIDv7{fixture.evidenceID},
		"findings": []any{}, "supersedes_result_event_ids": []kernel.UUIDv7{}, "changed_condition_evidence_ids": []kernel.UUIDv7{},
		"candidate_artifact_digest": digestByte('7'),
		"independence_receipt": map[string]any{
			"proven_dimensions": []string{"PRINCIPAL", "METHOD"}, "identity_comparison_digest": digestByte('2'),
			"method_ids": []string{"operator-product-surface"}, "evidence_ids": []kernel.UUIDv7{fixture.evidenceID},
		},
	}
	result := fixture.command(t, "tekroo.command.completion-review.record-result", kernel.SchemaVersion, kernel.AggregateCompletionReview, reviewID, validator, 1, resultPayload,
		[]kernel.DagParent{{ParentEventID: opened.EventIDs[0], EdgeKind: kernel.EdgeResponse}}, evidenceRefs, nil)
	result.ExpectedLifecycleEpoch = nil
	recorded := submit(t, result)
	finalPayload := map[string]any{
		"review_id": reviewID, "subject_kind": subject.Kind, "subject_id": subject.ID, "lifecycle_epoch": uint64(1),
		"branch_policy_revision": uint64(1), "expected_review_revision": uint64(2), "terminal_status": "PASS",
		"result_event_ids": []kernel.UUIDv7{recorded.EventIDs[0]}, "evidence_ids": []kernel.UUIDv7{fixture.evidenceID},
		"scope_revision": uint64(1), "work_profile": fixture.workProfileBinding(), "candidate_artifact_digest": digestByte('7'),
		"verification_topology_digest": digestByte('d'),
	}
	finalize := fixture.command(t, "tekroo.command.completion-review.finalize", kernel.SchemaVersion, kernel.AggregateCompletionReview, reviewID, fixture.policy, 2, finalPayload,
		[]kernel.DagParent{{ParentEventID: recorded.EventIDs[0], EdgeKind: kernel.EdgeResponse}}, evidenceRefs, nil)
	finalize.ExpectedLifecycleEpoch = nil
	return submit(t, finalize)
}
