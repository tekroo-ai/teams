//go:build (phase5_soak || phase6_pilot) && mongo_integration

package operationalruntime

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/fake"
	"github.com/tekroo-ai/teams/adapters/mongo"
	"github.com/tekroo-ai/teams/kernel"
)

const (
	phase5OpenHandsURL    = "http://127.0.0.1:8000"
	phase5ModelURL        = "http://127.0.0.1:8802/v1/models"
	phase5SessionKeyFile  = "/Users/paul/.openhands/agent-canvas/api-key.txt"
	phase5SMAHookFile     = "/Users/paul/.openhands/hooks/sma_context_hook.py"
	phase5ExpectedModelID = "ddalcu--Qwen3.8-27B-MLX-Serve-8bit"
)

func TestPhase5LiveSoakRunsConcurrentLocalWorkAndRestartsCleanly(t *testing.T) {
	requirePhase5LiveDependency(t, phase5OpenHandsURL+"/health", "status", "ok")
	requirePhase5LiveDependency(t, phase5ModelURL, "id", phase5ExpectedModelID)
	for _, path := range []string{phase5SessionKeyFile, phase5SMAHookFile} {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			t.Fatalf("required live file unavailable: %s", path)
		}
	}

	process, mongoURI := startRuntimeMongod(t)
	defer stopRuntimeMongod(process)
	now := time.Now().UTC().Truncate(time.Millisecond)
	fixtureBase := randomPhase5FixtureBase(t)
	first := newIntegratedFixture(t, now, fixtureBase, "teams::coder-1", "phase5-soak-alpha", "phase5-soak-alpha-main")
	second := newIntegratedFixture(t, now, fixtureBase+0x100, "teams::coder-2", "phase5-soak-beta", "phase5-soak-beta-main")
	requirePhase5ConversationAbsent(t, first.invocationID)
	requirePhase5ConversationAbsent(t, second.invocationID)
	root := t.TempDir()
	firstWorkspace, firstBaseline := createPhase5SoakRepository(t, root, "alpha", "Add", "return left - right", "Add(2, 3)", "5")
	secondWorkspace, secondBaseline := createPhase5SoakRepository(t, root, "beta", "Multiply", "return left + right", "Multiply(3, 4)", "12")
	first.title, first.description = "Repair Add in the isolated alpha repository", "Inspect the repository, correct Add, and run go test ./.... Do not modify tests."
	first.acceptanceCriteria, first.baselineSHA = []string{"go test ./... passes without changing tests"}, firstBaseline
	second.title, second.description = "Repair Multiply in the isolated beta repository", "Inspect the repository, correct Multiply, and run go test ./.... Do not modify tests."
	second.acceptanceCriteria, second.baselineSHA = []string{"go test ./... passes without changing tests"}, secondBaseline

	configPath := writePhase5LiveSoakConfig(t, mongoURI, phase5OpenHandsURL, []ProductionWorkspace{
		{WorkspaceID: first.workspaceID, WorktreeID: first.worktreeID, WorkingDirectory: firstWorkspace, Branch: "main", BaselineSHA: firstBaseline, WritablePaths: []string{"."}},
		{WorkspaceID: second.workspaceID, WorktreeID: second.worktreeID, WorkingDirectory: secondWorkspace, Branch: "main", BaselineSHA: secondBaseline, WritablePaths: []string{"."}},
	})
	tekrood, tekroo := buildProductSurfaceBinaries(t)
	service, stdout, stderr := startPhase5Tekrood(t, tekrood, configPath)
	serviceStopped := false
	defer func() {
		if serviceStopped {
			return
		}
		_ = service.Process.Kill()
		_ = service.Wait()
	}()
	waitForProductHealth(t, tekroo, configPath, stderr)

	commandDirectory := filepath.Join(t.TempDir(), "commands")
	if err := os.Mkdir(commandDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	submit := phase5ProductSubmitter(t, tekroo, configPath, commandDirectory)
	for _, fixture := range []*integratedFixture{first, second} {
		fixture.createAuthoritativeTaskWith(t, submit)
		story := waitForProductStory(t, tekroo, configPath, fixture.storyID, func(view mongo.StoryProjection) bool {
			return view.AggregateRevision == 1 && len(view.TaskIDs) == 1
		})
		activateProductStory(t, fixture, story, submit)
	}
	productCLI(t, tekroo, configPath, "pause")
	first.authorizeInvocationWith(t, submit)
	second.authorizeInvocationWith(t, submit)
	for _, fixture := range []*integratedFixture{first, second} {
		status := waitForLiveInvocation(t, tekroo, configPath, fixture.invocationID, func(value InvocationStatus) bool {
			return value.State == kernel.InvocationAuthorized
		})
		if status.ConversationID != nil {
			t.Fatalf("paused invocation %s created conversation %q", fixture.invocationID, *status.ConversationID)
		}
	}

	startedAt := time.Now().UTC()
	productCLI(t, tekroo, configPath, "resume")
	firstTerminal := waitForLiveInvocation(t, tekroo, configPath, first.invocationID, terminalInvocation)
	secondTerminal := waitForLiveInvocation(t, tekroo, configPath, second.invocationID, terminalInvocation)
	for _, status := range []InvocationStatus{firstTerminal, secondTerminal} {
		if status.State != kernel.InvocationSucceeded || status.TerminalOutcome == nil || *status.TerminalOutcome != kernel.InvocationSucceeded || len(status.TerminalEvidenceIDs) == 0 || status.OutputDigest == nil || status.FinishedAt == nil {
			t.Fatalf("live terminal invocation = %#v", status)
		}
	}
	requirePhase5ConversationWorkspace(t, first.invocationID, firstWorkspace)
	requirePhase5ConversationWorkspace(t, second.invocationID, secondWorkspace)
	for _, workspace := range []string{firstWorkspace, secondWorkspace} {
		command := exec.Command("go", "test", "./...")
		command.Dir = workspace
		if raw, err := command.CombinedOutput(); err != nil {
			t.Fatalf("workspace verification %s: %v\n%s", workspace, err, raw)
		}
	}
	statusRaw := productCLI(t, tekroo, configPath, "status")
	var control ControlStatus
	if err := json.Unmarshal(statusRaw, &control); err != nil || control.State != ControlRunning || control.Worker.Completed < 2 || control.Worker.Active != 0 {
		t.Fatalf("live control status = %#v err=%v raw=%s", control, err, statusRaw)
	}

	productCLI(t, tekroo, configPath, "stop")
	waitPhase5Tekrood(t, service, stdout, stderr)
	serviceStopped = true

	restarted, restartStdout, restartStderr := startPhase5Tekrood(t, tekrood, configPath)
	service, stdout, stderr, serviceStopped = restarted, restartStdout, restartStderr, false
	waitForProductHealth(t, tekroo, configPath, stderr)
	for _, fixture := range []*integratedFixture{first, second} {
		view := waitForProductTask(t, tekroo, configPath, fixture.taskID, func(value mongo.TaskProjection) bool {
			return value.LatestInvocation != nil && value.LatestInvocation.State == kernel.InvocationSucceeded
		})
		if view.Budget.ModelInvocationsUsed != 1 || view.LatestInvocation.InvocationID != fixture.invocationID {
			t.Fatalf("post-restart task projection = %#v", view)
		}
	}
	productCLI(t, tekroo, configPath, "stop")
	waitPhase5Tekrood(t, service, stdout, stderr)
	serviceStopped = true

	receipt := map[string]any{
		"record_type": "PHASE_5_LIVE_MULTI_TASK_SOAK", "status": "PASS", "recorded_at": time.Now().UTC(),
		"openhands_endpoint": phase5OpenHandsURL, "model_endpoint": "http://127.0.0.1:8802/v1", "model_id": phase5ExpectedModelID,
		"teams_database": "disposable_replica_set", "sma_database_access_by_teams": false,
		"task_count": 2, "maximum_concurrency": 2, "elapsed_ms": time.Since(startedAt).Milliseconds(),
		"invocations": []InvocationStatus{firstTerminal, secondTerminal},
		"checks":      []string{"pause prevented admission", "both isolated repositories repaired", "go test passed in both repositories", "budget usage remained one invocation per task", "projections survived tekrood restart", "service stopped cleanly"},
		"not_run":     []string{"v3 data migration", "production or historical database access", "physical host sleep", "model-server shutdown"},
	}
	writePhase5SoakReceipt(t, receipt)
}

func TestPhase5LiveSoakRecoversFromDependencyOutageAndCancelsAfterSuspend(t *testing.T) {
	requirePhase5LiveDependency(t, phase5OpenHandsURL+"/health", "status", "ok")
	requirePhase5LiveDependency(t, phase5ModelURL, "id", phase5ExpectedModelID)
	for _, path := range []string{phase5SessionKeyFile, phase5SMAHookFile} {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			t.Fatalf("required live file unavailable: %s", path)
		}
	}

	gate := newPhase5OpenHandsGate(t)
	process, mongoURI := startRuntimeMongod(t)
	defer stopRuntimeMongod(process)
	now := time.Now().UTC().Truncate(time.Millisecond)
	fixture := newIntegratedFixture(t, now, randomPhase5FixtureBase(t), "teams::coder-1", "phase5-soak-recovery", "phase5-soak-recovery-main")
	root := t.TempDir()
	workspace, baseline := createPhase5SoakRepository(t, root, "recovery", "Subtract", "return left + right", "Subtract(8, 3)", "5")
	fixture.title = "Exercise recoverable local execution cancellation"
	fixture.description = "Before inspecting or editing anything, run the shell command sleep 30. Then inspect the repository, correct Subtract, and run go test ./.... Do not modify tests."
	fixture.acceptanceCriteria, fixture.baselineSHA = []string{"operator cancellation interrupts the authorized invocation"}, baseline
	requirePhase5ConversationAbsent(t, fixture.invocationID)

	configPath := writePhase5LiveSoakConfig(t, mongoURI, gate.URL(), []ProductionWorkspace{{WorkspaceID: fixture.workspaceID, WorktreeID: fixture.worktreeID, WorkingDirectory: workspace, Branch: "main", BaselineSHA: baseline, WritablePaths: []string{"."}}})
	tekrood, tekroo := buildProductSurfaceBinaries(t)
	service, stdout, stderr := startPhase5Tekrood(t, tekrood, configPath)
	serviceStopped := false
	defer func() {
		if serviceStopped {
			return
		}
		_ = service.Process.Kill()
		_ = service.Wait()
	}()
	waitForProductHealth(t, tekroo, configPath, stderr)

	config, err := LoadProductionConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	commandService, err := NewProductionService(contextWithTimeout(t), config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = commandService.Close(contextWithTimeout(t)) }()
	submit := func(t *testing.T, command kernel.KernelCommand) kernel.CommandReceipt {
		t.Helper()
		receipt, submitErr := commandService.Submit(contextWithTimeout(t), command)
		if submitErr != nil {
			t.Fatalf("%s: %v", command.CommandType, submitErr)
		}
		if receipt.OutcomeCode != kernel.OutcomeApplied || len(receipt.EventIDs) != 1 {
			t.Fatalf("%s receipt = %#v", command.CommandType, receipt)
		}
		return receipt
	}
	fixture.createAuthoritativeTaskWith(t, submit)
	story := waitForProductStory(t, tekroo, configPath, fixture.storyID, func(view mongo.StoryProjection) bool {
		return view.AggregateRevision == 1 && len(view.TaskIDs) == 1
	})
	activateProductStory(t, fixture, story, submit)
	productCLI(t, tekroo, configPath, "pause")
	fixture.authorizeInvocationWith(t, submit)
	waitForLiveInvocation(t, tekroo, configPath, fixture.invocationID, func(value InvocationStatus) bool { return value.State == kernel.InvocationAuthorized })

	gate.SetAvailable(false)
	productCLI(t, tekroo, configPath, "resume")
	waitForLiveInvocation(t, tekroo, configPath, fixture.invocationID, func(value InvocationStatus) bool { return value.State == kernel.InvocationClaimed })
	waitForPhase5GateRejection(t, gate)
	gate.SetAvailable(true)
	waitForPhase5Conversation(t, fixture.invocationID)
	waitForLiveInvocation(t, tekroo, configPath, fixture.invocationID, func(value InvocationStatus) bool {
		return value.State == kernel.InvocationStarted && value.ConversationID != nil
	})
	// Hold the OpenHands boundary unavailable while the daemon is suspended and
	// the operator records cancellation. This keeps the external conversation
	// from racing a terminal observation into Teams before cancellation wins.
	gate.SetAvailable(false)

	if err := service.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatalf("suspend tekrood: %v", err)
	}
	time.Sleep(2 * time.Second)
	if err := service.Process.Signal(syscall.SIGCONT); err != nil {
		t.Fatalf("resume tekrood: %v", err)
	}
	waitForProductHealth(t, tekroo, configPath, stderr)
	currentRaw := productCLI(t, tekroo, configPath, "invocation", string(fixture.invocationID))
	var current InvocationStatus
	if json.Unmarshal(currentRaw, &current) != nil || current.State != kernel.InvocationStarted {
		t.Fatalf("invocation changed before cancellation: %#v raw=%s", current, currentRaw)
	}

	cancelPath := filepath.Join(t.TempDir(), "cancellation.json")
	writeJSON(t, cancelPath, CancellationRequest{
		ExpectedRevision: current.Revision,
		Reason:           "Phase 5 operator cancellation after process resume",
		EvidenceRefs:     []kernel.EvidenceRef{{EvidenceID: fixture.evidenceID, SHA256: digestByte('e')}},
		IdempotencyKey:   "phase5-recovery-cancellation",
	}, 0o600)
	raw := productCLI(t, tekroo, configPath, "cancel", string(fixture.invocationID), cancelPath)
	var cancellationStatus InvocationStatus
	if json.Unmarshal(raw, &cancellationStatus) != nil || cancellationStatus.CancellationRequestedAt == nil {
		t.Fatalf("cancellation status = %#v raw=%s", cancellationStatus, raw)
	}
	gate.SetAvailable(true)
	cancelled := waitForLiveInvocation(t, tekroo, configPath, fixture.invocationID, func(value InvocationStatus) bool { return value.State == kernel.InvocationCancelled })
	if cancelled.TerminalOutcome == nil || *cancelled.TerminalOutcome != kernel.InvocationCancelled || cancelled.CancellationRequestedAt == nil || len(cancelled.TerminalEvidenceIDs) == 0 {
		t.Fatalf("cancelled invocation = %#v", cancelled)
	}
	requirePhase5ConversationWorkspace(t, fixture.invocationID, workspace)

	productCLI(t, tekroo, configPath, "stop")
	waitPhase5Tekrood(t, service, stdout, stderr)
	serviceStopped = true
	restarted, restartStdout, restartStderr := startPhase5Tekrood(t, tekrood, configPath)
	service, stdout, stderr, serviceStopped = restarted, restartStdout, restartStderr, false
	waitForProductHealth(t, tekroo, configPath, stderr)
	view := waitForProductTask(t, tekroo, configPath, fixture.taskID, func(value mongo.TaskProjection) bool {
		return value.LatestInvocation != nil && value.LatestInvocation.State == kernel.InvocationCancelled
	})
	if view.Budget.ModelInvocationsUsed != 1 || view.LatestInvocation.InvocationID != fixture.invocationID {
		t.Fatalf("post-restart cancellation projection = %#v", view)
	}
	productCLI(t, tekroo, configPath, "stop")
	waitPhase5Tekrood(t, service, stdout, stderr)
	serviceStopped = true

	receipt := map[string]any{
		"record_type": "PHASE_5_LIVE_RECOVERY_AND_CANCELLATION", "status": "PASS", "recorded_at": time.Now().UTC(),
		"openhands_endpoint": phase5OpenHandsURL, "model_endpoint": "http://127.0.0.1:8802/v1", "model_id": phase5ExpectedModelID,
		"teams_database": "disposable_replica_set", "sma_database_access_by_teams": false,
		"outage_responses_observed": gate.Rejected(), "invocation": cancelled,
		"checks":  []string{"OpenHands proxy outage observed", "invocation recovered without reauthorization", "tekrood SIGSTOP/SIGCONT during active execution", "tekroo cancellation interrupted OpenHands", "budget remained one invocation", "cancelled projection survived tekrood restart", "conversation had no parent agent"},
		"not_run": []string{"v3 data migration", "production or historical database access", "physical host sleep", "model-server shutdown"},
	}
	writePhase5Receipt(t, "step-6-live-recovery-cancellation-receipt.json", receipt)
}

type phase5OpenHandsGate struct {
	available atomic.Bool
	rejected  atomic.Uint64
	proxy     *httputil.ReverseProxy
	server    *httptest.Server
}

func newPhase5OpenHandsGate(t *testing.T) *phase5OpenHandsGate {
	t.Helper()
	target, err := url.Parse(phase5OpenHandsURL)
	if err != nil {
		t.Fatal(err)
	}
	gate := &phase5OpenHandsGate{proxy: httputil.NewSingleHostReverseProxy(target)}
	gate.available.Store(true)
	gate.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !gate.available.Load() {
			gate.rejected.Add(1)
			http.Error(writer, "temporary OpenHands outage", http.StatusServiceUnavailable)
			return
		}
		gate.proxy.ServeHTTP(writer, request)
	}))
	t.Cleanup(gate.server.Close)
	return gate
}

func (gate *phase5OpenHandsGate) URL() string             { return gate.server.URL }
func (gate *phase5OpenHandsGate) SetAvailable(value bool) { gate.available.Store(value) }
func (gate *phase5OpenHandsGate) Rejected() uint64        { return gate.rejected.Load() }

func waitForPhase5GateRejection(t *testing.T, gate *phase5OpenHandsGate) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for gate.Rejected() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("Teams did not exercise the unavailable OpenHands boundary")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func waitForPhase5Conversation(t *testing.T, conversationID kernel.UUIDv7) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		request, err := http.NewRequest(http.MethodGet, phase5OpenHandsURL+"/api/conversations/"+string(conversationID), nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("X-Session-API-Key", phase5SessionAPIKey(t))
		response, requestErr := (&http.Client{Timeout: 5 * time.Second}).Do(request)
		if requestErr == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("OpenHands conversation %s was not created: %v", conversationID, requestErr)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func randomPhase5FixtureBase(t *testing.T) int {
	t.Helper()
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	// The integration fixture reserves ten consecutive UUID suffixes. Keeping
	// the low twelve bits clear gives each run two disjoint 0x100-sized ranges.
	return int(binary.BigEndian.Uint64(raw[:]) & 0x0000fffffffff000)
}

func phase5SessionAPIKey(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(phase5SessionKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	return string(bytes.TrimSpace(raw))
}

func requirePhase5ConversationAbsent(t *testing.T, conversationID kernel.UUIDv7) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, phase5OpenHandsURL+"/api/conversations/"+string(conversationID), nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Session-API-Key", phase5SessionAPIKey(t))
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("OpenHands conversation preflight: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("OpenHands conversation %s must be absent before the soak; status=%d", conversationID, response.StatusCode)
	}
}

func requirePhase5ConversationWorkspace(t *testing.T, conversationID kernel.UUIDv7, expected string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, phase5OpenHandsURL+"/api/conversations/"+string(conversationID), nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Session-API-Key", phase5SessionAPIKey(t))
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("OpenHands conversation verification: %v", err)
	}
	defer response.Body.Close()
	var value struct {
		ParentConversationID *string `json:"parent_conversation_id"`
		Workspace            struct {
			WorkingDirectory string `json:"working_dir"`
		} `json:"workspace"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&value) != nil || filepath.Clean(value.Workspace.WorkingDirectory) != filepath.Clean(expected) || value.ParentConversationID != nil {
		t.Fatalf("OpenHands conversation %s workspace=%q status=%d, want %q", conversationID, value.Workspace.WorkingDirectory, response.StatusCode, expected)
	}
}

func requirePhase5LiveDependency(t *testing.T, endpoint, field, expected string) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get(endpoint)
	if err != nil {
		t.Fatalf("live dependency %s: %v", endpoint, err)
	}
	defer response.Body.Close()
	var value any
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil || response.StatusCode != http.StatusOK || !jsonContainsString(value, field, expected) {
		t.Fatalf("live dependency %s status=%d expected %s=%s err=%v", endpoint, response.StatusCode, field, expected, err)
	}
}

func jsonContainsString(value any, field, expected string) bool {
	switch current := value.(type) {
	case map[string]any:
		if text, ok := current[field].(string); ok && text == expected {
			return true
		}
		for _, child := range current {
			if jsonContainsString(child, field, expected) {
				return true
			}
		}
	case []any:
		for _, child := range current {
			if jsonContainsString(child, field, expected) {
				return true
			}
		}
	}
	return false
}

func createPhase5SoakRepository(t *testing.T, root, name, function, brokenReturn, call, want string) (string, string) {
	t.Helper()
	workspace := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(workspace, ".openhands", "hooks"), 0o700); err != nil {
		t.Fatal(err)
	}
	hook, err := os.ReadFile(phase5SMAHookFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".openhands", "hooks", "sma_context_hook.py"), hook, 0o700); err != nil {
		t.Fatal(err)
	}
	writeText(t, filepath.Join(workspace, "go.mod"), "module example.com/phase5/"+name+"\n\ngo 1.25\n", 0o600)
	writeText(t, filepath.Join(workspace, "arithmetic.go"), fmt.Sprintf("package arithmetic\n\nfunc %s(left, right int) int {\n\t%s\n}\n", function, brokenReturn), 0o600)
	writeText(t, filepath.Join(workspace, "arithmetic_test.go"), fmt.Sprintf("package arithmetic\n\nimport \"testing\"\n\nfunc Test%s(t *testing.T) {\n\tif got := %s; got != %s {\n\t\tt.Fatalf(\"got %%d, want %s\", got)\n\t}\n}\n", function, call, want, want), 0o600)
	for _, arguments := range [][]string{{"init", "-b", "main"}, {"config", "user.email", "phase5@tekroo.local"}, {"config", "user.name", "Tekroo Phase 5"}, {"add", "."}, {"commit", "-m", "seed failing repository"}} {
		command := exec.Command("git", arguments...)
		command.Dir = workspace
		if raw, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", arguments, err, raw)
		}
	}
	command := exec.Command("git", "rev-parse", "HEAD")
	command.Dir = workspace
	raw, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	return workspace, string(bytes.TrimSpace(raw))
}

func writePhase5LiveSoakConfig(t *testing.T, mongoURI, openHandsURL string, workspaces []ProductionWorkspace) string {
	t.Helper()
	path, config := writeProductionFixture(t)
	directory := filepath.Dir(path)
	writeText(t, filepath.Join(directory, config.Mongo.URIFile), mongoURI+"\n", 0o600)
	provenance, err := fake.ProvenanceBasis()
	if err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(directory, config.AuthorizationPolicyFile), integratedPolicy(), 0o600)
	writeJSON(t, filepath.Join(directory, config.ProvenanceFile), provenance, 0o600)
	database := fmt.Sprintf("tekroo_phase5_live_soak_%d", time.Now().UnixNano())
	config.Mongo.Database, config.TeamsDatabaseIdentity = database, database
	config.SMADatabaseIdentity = "sma_memory_live_separate"
	config.DeploymentIdentity = digestByte('5')
	config.OpenHands.BaseURL = openHandsURL
	config.OpenHands.SessionAPIKeyFile = phase5SessionKeyFile
	config.OpenHands.RequestTimeout = "20m"
	config.OpenHands.PollInterval = "250ms"
	config.Operator.Address = freeProductSurfaceAddress(t)
	config.Operator.BearerTokenFile = phase5SessionKeyFile
	config.Operator.Principal = kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"}
	config.Workspaces = workspaces
	config.Profiles[0].MaximumIterations = 12
	config.Execution.ConsumerID = "teams-phase5-live-soak"
	config.Execution.OperationTimeout = "20m"
	config.Worker.LeaseDuration = "60s"
	config.Worker.ReconciliationInterval = "1s"
	config.Worker.MaximumReconciliations = 20
	config.Worker.MaximumConcurrentInvocations = 2
	config.Worker.LeaseOperationTimeout = "5s"
	config.Projection.Interval = "50ms"
	config.Projection.OperationTimeout = "2s"
	writeJSON(t, path, config, 0o600)
	return path
}

func startPhase5Tekrood(t *testing.T, binary, configPath string) (*exec.Cmd, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	service := exec.Command(binary, "-config", configPath)
	service.Stdout, service.Stderr = stdout, stderr
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	return service, stdout, stderr
}

func waitPhase5Tekrood(t *testing.T, service *exec.Cmd, stdout, stderr *bytes.Buffer) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- service.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("tekrood stop: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("tekrood stop timeout\nstdout=%s\nstderr=%s", stdout.String(), stderr.String())
	}
}

func phase5ProductSubmitter(t *testing.T, tekroo, configPath, commandDirectory string) integratedCommandSubmitter {
	t.Helper()
	return func(t *testing.T, command kernel.KernelCommand) kernel.CommandReceipt {
		t.Helper()
		path := filepath.Join(commandDirectory, string(command.CommandID)+".json")
		writeJSON(t, path, command, 0o600)
		raw := productCLI(t, tekroo, configPath, "submit", path)
		var receipt kernel.CommandReceipt
		if err := json.Unmarshal(raw, &receipt); err != nil || receipt.OutcomeCode != kernel.OutcomeApplied || len(receipt.EventIDs) != 1 {
			t.Fatalf("%s receipt=%#v err=%v raw=%s", command.CommandType, receipt, err, raw)
		}
		return receipt
	}
}

func waitForLiveInvocation(t *testing.T, tekroo, config string, id kernel.UUIDv7, predicate func(InvocationStatus) bool) InvocationStatus {
	t.Helper()
	deadline := time.Now().Add(25 * time.Minute)
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
			t.Fatalf("live invocation timeout: %#v", status)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func terminalInvocation(status InvocationStatus) bool {
	switch status.State {
	case kernel.InvocationSucceeded, kernel.InvocationFailed, kernel.InvocationTimedOut, kernel.InvocationCancelled, kernel.InvocationStartFailed:
		return true
	default:
		return false
	}
}

func writePhase5SoakReceipt(t *testing.T, receipt map[string]any) {
	writePhase5Receipt(t, "step-6-live-soak-receipt.json", receipt)
}

func writePhase5Receipt(t *testing.T, name string, receipt map[string]any) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "OUTPUT", "phase-5")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(directory, name), receipt, 0o600)
}
