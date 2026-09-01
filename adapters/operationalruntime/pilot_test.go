//go:build mongo_integration && phase4_pilot

package operationalruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
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
)

func TestControlledLocalOperatingPilot(t *testing.T) {
	if os.Getenv("TEKROO_PHASE4_STEP8") != "1" {
		t.Skip("live Phase 4 Step 8 pilot is not enabled")
	}
	primaryWorkspace := requirePilotDirectory(t, "TEKROO_PHASE4_STEP8_PRIMARY_WORKSPACE")
	cancelWorkspace := requirePilotDirectory(t, "TEKROO_PHASE4_STEP8_CANCEL_WORKSPACE")
	evidenceRoot := requirePilotDirectory(t, "TEKROO_PHASE4_STEP8_EVIDENCE_ROOT")
	mongoURI := requirePilotValue(t, "TEKROO_PHASE4_STEP8_MONGO_URI")
	database := requirePilotValue(t, "TEKROO_PHASE4_STEP8_TEAMS_DATABASE")
	smaDatabase := requirePilotValue(t, "TEKROO_PHASE4_STEP8_SMA_DATABASE")
	keyBytes, err := os.ReadFile(requirePilotValue(t, "TEKROO_PHASE4_STEP8_SESSION_KEY_FILE"))
	sessionKey := strings.TrimSpace(string(keyBytes))
	if err != nil || sessionKey == "" {
		t.Fatalf("read OpenHands session key: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	store, err := mongo.Open(contextWithTimeout(t), mongo.Config{
		URI: mongoURI, Database: database, ContractIdentity: kernel.ContractIdentity,
		ManifestSHA256: phase4ManifestSHA, MigrationLevel: 1, Policy: integratedPolicy(),
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
	primary := newIntegratedFixture(t, now, 5000, "teams::coder-1", "phase4-step8-primary", "phase4-step8-primary")
	primary.title = "Repair the calculator"
	primary.description = "Inspect this Go repository, run its tests, identify the defect in the production code, make the smallest correct repair without changing the test, and run go test ./... until it passes."
	primary.acceptanceCriteria = []string{"go test ./... exits successfully", "the existing test remains unchanged"}
	primary.baselineSHA = pilotGitHead(t, primaryWorkspace)
	primary.writablePaths = []string{"calculator/"}
	cancelProbe := newIntegratedFixture(t, now, 6000, "teams::coder-2", "phase4-step8-cancel", "phase4-step8-cancel")
	cancelProbe.title = "Cancellation control probe"
	cancelProbe.description = "Before inspecting or editing anything, run the shell command sleep 30. After it finishes, inspect this Go repository and repair its failing test without changing the test."
	cancelProbe.acceptanceCriteria = []string{"operator cancellation interrupts the authorized invocation"}
	cancelProbe.baselineSHA = pilotGitHead(t, cancelWorkspace)
	cancelProbe.writablePaths = []string{"calculator/"}

	profile, err := openhands.NewAcceptedExecutionProfile(primary.modelDigest, primary.runtimeDigest, primary.toolDigest, primary.effectDigest, 24, database, smaDatabase)
	if err != nil {
		t.Fatal(err)
	}
	clock := SystemClock{}
	ids, err := NewUUIDv7Source(clock)
	if err != nil {
		t.Fatal(err)
	}
	consumer := "teams-phase4-step8-pilot"
	runtime, err := New(contextWithTimeout(t), Config{
		Store: store, Catalogue: catalogue, Clock: clock, IDs: ids,
		OpenHandsBaseURL: "http://127.0.0.1:8000", OpenHandsSessionAPIKey: sessionKey,
		HTTPClient: &http.Client{Timeout: 130 * time.Second},
		WorkspaceBindings: []openhands.WorkspaceBinding{
			{WorkspaceID: primary.workspaceID, WorktreeID: primary.worktreeID, WorkingDirectory: primaryWorkspace},
			{WorkspaceID: cancelProbe.workspaceID, WorktreeID: cancelProbe.worktreeID, WorkingDirectory: cancelWorkspace},
		},
		ExecutionProfiles: []openhands.ExecutionProfile{profile}, RoleGrounding: testRoleGroundingResolver{}, OpenHandsPollInterval: 100 * time.Millisecond,
		OpenHandsMaximumPages: 64, OpenHandsMaximumEvidence: 16 << 20, EvidenceRoot: evidenceRoot,
		ExecutionPolicy: application.OperationalExecutionPolicy{
			OperationTimeout: 130 * time.Second, MaximumBriefBytes: 1 << 20, ConsumerID: consumer,
			PolicyRevision: 1, ServiceAuthority: primary.service, ExpiryAuthority: primary.policy, Provenance: provenance,
		},
		EvidencePolicy: application.CommandEvidenceRecorderPolicy{
			PolicyRevision: 1, Authority: primary.service, Provenance: provenance,
			ProducingVersion: "phase4-step8", RetentionPolicy: "phase4-step8",
		},
		WorkerPolicy: executionruntime.Policy{
			ConsumerID: consumer, LeaseDuration: 150 * time.Second, ReconciliationInterval: 200 * time.Millisecond,
			MaximumReconciliations: 100, MaximumConcurrentInvocations: 2, LeaseOperationTimeout: 5 * time.Second,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(contextWithTimeout(t))
	controller, err := NewController(runtime, clock)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Start(contextWithTimeout(t)); err != nil {
		t.Fatal(err)
	}
	if err := controller.Pause(contextWithTimeout(t)); err != nil {
		t.Fatal(err)
	}
	primary.createAuthoritativeTask(t, runtime, provenance)
	cancelProbe.createAuthoritativeTask(t, runtime, provenance)
	primary.authorizeInvocation(t, runtime, provenance)
	cancelProbe.authorizeInvocation(t, runtime, provenance)
	if status := controller.Inspect(); status.State != ControlPaused || status.Worker.Active != 0 {
		t.Fatalf("paused runtime status = %#v", status)
	}
	assertPilotConversationAbsent(t, sessionKey, primary.invocationID)
	assertPilotConversationAbsent(t, sessionKey, cancelProbe.invocationID)
	if err := controller.Resume(contextWithTimeout(t)); err != nil {
		t.Fatal(err)
	}

	waitForPilotInvocation(t, store, cancelProbe.invocationID, func(invocation kernel.WorkInvocation) bool {
		return invocation.State == kernel.InvocationStarted && invocation.ConversationID != nil
	})
	cancelProbe.requestCancellation(t, runtime, store, provenance, "Phase 4 Step 8 operator cancellation control")
	primaryTerminal := waitForPilotInvocation(t, store, primary.invocationID, func(invocation kernel.WorkInvocation) bool { return invocation.State.Terminal() })
	cancelTerminal := waitForPilotInvocation(t, store, cancelProbe.invocationID, func(invocation kernel.WorkInvocation) bool { return invocation.State.Terminal() })
	if primaryTerminal.State != kernel.InvocationSucceeded {
		t.Fatalf("primary invocation state = %s", primaryTerminal.State)
	}
	if cancelTerminal.State != kernel.InvocationCancelled {
		t.Fatalf("cancellation invocation state = %s", cancelTerminal.State)
	}
	verification := exec.Command("go", "test", "./...")
	verification.Dir = primaryWorkspace
	if output, err := verification.CombinedOutput(); err != nil {
		t.Fatalf("sandbox verification failed: %v\n%s", err, output)
	}

	projected, err := store.ProjectPendingOperationalEvents(contextWithTimeout(t), time.Now().UTC())
	if err != nil || projected == 0 {
		t.Fatalf("project pending events = %d, %v", projected, err)
	}
	primaryView, found, err := store.ReadTaskProjection(contextWithTimeout(t), primary.taskID)
	if err != nil || !found || primaryView.LatestInvocation == nil || primaryView.LatestInvocation.State != kernel.InvocationSucceeded {
		t.Fatalf("primary task projection = %#v found=%t err=%v", primaryView, found, err)
	}
	cancelView, found, err := store.ReadTaskProjection(contextWithTimeout(t), cancelProbe.taskID)
	if err != nil || !found || cancelView.LatestInvocation == nil || cancelView.LatestInvocation.State != kernel.InvocationCancelled {
		t.Fatalf("cancel task projection = %#v found=%t err=%v", cancelView, found, err)
	}
	storyView, found, err := store.ReadStoryProjection(contextWithTimeout(t), primary.storyID)
	if err != nil || !found || !storyView.Valid() {
		t.Fatalf("story projection = %#v found=%t err=%v", storyView, found, err)
	}
	status := controller.Inspect()
	if status.State != ControlRunning || status.Worker.Active != 0 || status.Worker.Completed != 2 {
		t.Fatalf("terminal runtime status = %#v", status)
	}
	if err := controller.Stop(contextWithTimeout(t)); err != nil {
		t.Fatal(err)
	}
	if status = controller.Inspect(); status.State != ControlStopped {
		t.Fatalf("stopped runtime status = %#v", status)
	}
	receipt := map[string]any{
		"recordType": "TEKROO_TEAMS_PHASE4_STEP8_LIVE_PILOT_RAW_RECEIPT", "recordedAt": time.Now().UTC(),
		"database": database, "smaDatabase": smaDatabase, "primaryInvocation": primaryTerminal.State,
		"cancelInvocation": cancelTerminal.State, "operatorControls": []string{"START", "INSPECT", "PAUSE", "RESUME", "CANCEL", "STOP"},
		"taskProjection": primaryView, "cancelProjection": cancelView, "storyProjection": storyView,
	}
	encoded, _ := json.Marshal(receipt)
	t.Log(string(encoded))
}

// TestControlledLocalOperatingPilotContinuation resumes the exact durable
// pilot state after a worker has yielded an in-flight invocation. It does not
// create tasks, authorize work, submit prompts, or repeat completed controls.
func TestControlledLocalOperatingPilotContinuation(t *testing.T) {
	if os.Getenv("TEKROO_PHASE4_STEP8_CONTINUE") != "1" {
		t.Skip("live Phase 4 Step 8 continuation is not enabled")
	}
	primaryWorkspace := requirePilotDirectory(t, "TEKROO_PHASE4_STEP8_PRIMARY_WORKSPACE")
	cancelWorkspace := requirePilotDirectory(t, "TEKROO_PHASE4_STEP8_CANCEL_WORKSPACE")
	evidenceRoot := requirePilotDirectory(t, "TEKROO_PHASE4_STEP8_EVIDENCE_ROOT")
	mongoURI := requirePilotValue(t, "TEKROO_PHASE4_STEP8_MONGO_URI")
	database := requirePilotValue(t, "TEKROO_PHASE4_STEP8_TEAMS_DATABASE")
	smaDatabase := requirePilotValue(t, "TEKROO_PHASE4_STEP8_SMA_DATABASE")
	keyBytes, err := os.ReadFile(requirePilotValue(t, "TEKROO_PHASE4_STEP8_SESSION_KEY_FILE"))
	sessionKey := strings.TrimSpace(string(keyBytes))
	if err != nil || sessionKey == "" {
		t.Fatalf("read OpenHands session key: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	store, err := mongo.Open(contextWithTimeout(t), mongo.Config{
		URI: mongoURI, Database: database, ContractIdentity: kernel.ContractIdentity,
		ManifestSHA256: phase4ManifestSHA, MigrationLevel: 1, Policy: integratedPolicy(),
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
	primary := newIntegratedFixture(t, now, 5000, "teams::coder-1", "phase4-step8-primary", "phase4-step8-primary")
	cancelProbe := newIntegratedFixture(t, now, 6000, "teams::coder-2", "phase4-step8-cancel", "phase4-step8-cancel")
	profile, err := openhands.NewAcceptedExecutionProfile(primary.modelDigest, primary.runtimeDigest, primary.toolDigest, primary.effectDigest, 24, database, smaDatabase)
	if err != nil {
		t.Fatal(err)
	}
	clock := SystemClock{}
	ids, err := NewUUIDv7Source(clock)
	if err != nil {
		t.Fatal(err)
	}
	consumer := "teams-phase4-step8-pilot-continuation"
	runtime, err := New(contextWithTimeout(t), Config{
		Store: store, Catalogue: catalogue, Clock: clock, IDs: ids,
		OpenHandsBaseURL: "http://127.0.0.1:8000", OpenHandsSessionAPIKey: sessionKey,
		HTTPClient: &http.Client{Timeout: 130 * time.Second},
		WorkspaceBindings: []openhands.WorkspaceBinding{
			{WorkspaceID: primary.workspaceID, WorktreeID: primary.worktreeID, WorkingDirectory: primaryWorkspace},
			{WorkspaceID: cancelProbe.workspaceID, WorktreeID: cancelProbe.worktreeID, WorkingDirectory: cancelWorkspace},
		},
		ExecutionProfiles: []openhands.ExecutionProfile{profile}, RoleGrounding: testRoleGroundingResolver{}, OpenHandsPollInterval: 100 * time.Millisecond,
		OpenHandsMaximumPages: 64, OpenHandsMaximumEvidence: 16 << 20, EvidenceRoot: evidenceRoot,
		ExecutionPolicy: application.OperationalExecutionPolicy{
			OperationTimeout: 130 * time.Second, MaximumBriefBytes: 1 << 20, ConsumerID: consumer,
			PolicyRevision: 1, ServiceAuthority: primary.service, ExpiryAuthority: primary.policy, Provenance: provenance,
		},
		EvidencePolicy: application.CommandEvidenceRecorderPolicy{
			PolicyRevision: 1, Authority: primary.service, Provenance: provenance,
			ProducingVersion: "phase4-step8", RetentionPolicy: "phase4-step8",
		},
		WorkerPolicy: executionruntime.Policy{
			ConsumerID: consumer, LeaseDuration: 150 * time.Second, ReconciliationInterval: 200 * time.Millisecond,
			MaximumReconciliations: 100, MaximumConcurrentInvocations: 2, LeaseOperationTimeout: 5 * time.Second,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(contextWithTimeout(t))
	controller, err := NewController(runtime, clock)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Start(contextWithTimeout(t)); err != nil {
		t.Fatal(err)
	}
	primaryTerminal := waitForPilotInvocation(t, store, primary.invocationID, func(invocation kernel.WorkInvocation) bool { return invocation.State.Terminal() })
	cancelTerminal := waitForPilotInvocation(t, store, cancelProbe.invocationID, func(invocation kernel.WorkInvocation) bool { return invocation.State.Terminal() })
	if primaryTerminal.State != kernel.InvocationSucceeded || cancelTerminal.State != kernel.InvocationCancelled {
		t.Fatalf("terminal states primary=%s cancel=%s", primaryTerminal.State, cancelTerminal.State)
	}
	verification := exec.Command("go", "test", "./...")
	verification.Dir = primaryWorkspace
	if output, err := verification.CombinedOutput(); err != nil {
		t.Fatalf("sandbox verification failed: %v\n%s", err, output)
	}
	projected, err := store.ProjectPendingOperationalEvents(contextWithTimeout(t), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	primaryView, found, err := store.ReadTaskProjection(contextWithTimeout(t), primary.taskID)
	if err != nil || !found || primaryView.LatestInvocation == nil || primaryView.LatestInvocation.State != kernel.InvocationSucceeded {
		t.Fatalf("primary task projection = %#v found=%t err=%v", primaryView, found, err)
	}
	cancelView, found, err := store.ReadTaskProjection(contextWithTimeout(t), cancelProbe.taskID)
	if err != nil || !found || cancelView.LatestInvocation == nil || cancelView.LatestInvocation.State != kernel.InvocationCancelled {
		t.Fatalf("cancel task projection = %#v found=%t err=%v", cancelView, found, err)
	}
	storyView, found, err := store.ReadStoryProjection(contextWithTimeout(t), primary.storyID)
	if err != nil || !found || !storyView.Valid() {
		t.Fatalf("story projection = %#v found=%t err=%v", storyView, found, err)
	}
	if err := controller.Stop(contextWithTimeout(t)); err != nil {
		t.Fatal(err)
	}
	receipt := map[string]any{
		"recordType": "TEKROO_TEAMS_PHASE4_STEP8_LIVE_PILOT_CONTINUATION_RECEIPT", "recordedAt": time.Now().UTC(),
		"database": database, "smaDatabase": smaDatabase, "primaryInvocation": primaryTerminal.State,
		"cancelInvocation": cancelTerminal.State, "projectedEvents": projected,
		"taskProjection": primaryView, "cancelProjection": cancelView, "storyProjection": storyView,
	}
	encoded, _ := json.Marshal(receipt)
	t.Log(string(encoded))
}

func requirePilotValue(t *testing.T, name string) string {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		t.Fatalf("%s is required", name)
	}
	return value
}

func requirePilotDirectory(t *testing.T, name string) string {
	t.Helper()
	value := requirePilotValue(t, name)
	info, err := os.Stat(value)
	if err != nil || !info.IsDir() || !filepath.IsAbs(value) {
		t.Fatalf("%s must be an existing absolute directory", name)
	}
	return value
}

func pilotGitHead(t *testing.T, directory string) string {
	t.Helper()
	command := exec.Command("git", "rev-parse", "HEAD")
	command.Dir = directory
	value, err := command.Output()
	if err != nil || len(value) != 41 {
		t.Fatalf("sandbox git head: %v", err)
	}
	return string(value[:40])
}

func assertPilotConversationAbsent(t *testing.T, key string, invocationID kernel.UUIDv7) {
	t.Helper()
	request, _ := http.NewRequestWithContext(contextWithTimeout(t), http.MethodGet, "http://127.0.0.1:8000/api/conversations/"+string(invocationID), nil)
	request.Header.Set("X-Session-API-Key", key)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("conversation %s status while paused = %d", invocationID, response.StatusCode)
	}
}

func waitForPilotInvocation(t *testing.T, store *mongo.Store, invocationID kernel.UUIDv7, accepted func(kernel.WorkInvocation) bool) kernel.WorkInvocation {
	t.Helper()
	deadline := time.Now().Add(4 * time.Minute)
	for time.Now().Before(deadline) {
		current, err := store.LoadOperationalExecution(contextWithTimeout(t), invocationID)
		if err == nil && accepted(current.Invocation) {
			return current.Invocation
		}
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			t.Logf("waiting for invocation %s: %v", invocationID, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("invocation %s did not reach required state", invocationID)
	return kernel.WorkInvocation{}
}
