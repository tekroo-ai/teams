//go:build phase6_pilot && mongo_integration

package operationalruntime

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/fake"
	"github.com/tekroo-ai/teams/adapters/mongo"
	"github.com/tekroo-ai/teams/kernel"
)

// TestPhase6LiveConcurrencyLadder proves that the production worker admits and
// completes exact task invocations at the four required concurrency levels.
// Every request reaches the configured local OpenHands and model endpoints.
func TestPhase6LiveConcurrencyLadder(t *testing.T) {
	requirePhase5LiveDependency(t, phase5OpenHandsURL+"/health", "status", "ok")
	requirePhase5LiveDependency(t, phase5ModelURL, "id", phase5ExpectedModelID)

	mongod, mongoURI := startRuntimeMongod(t)
	defer stopRuntimeMongod(mongod)
	root := t.TempDir()
	counts := []int{1, 2, 4, 8}
	total := 0
	for _, count := range counts {
		total += count
	}
	base := randomPhase5FixtureBase(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	fixtures := make([]*integratedFixture, 0, total)
	workspaces := make([]ProductionWorkspace, 0, total)
	actors := make([]kernel.ActorFQN, 0, total)
	for index := 0; index < total; index++ {
		actor := kernel.ActorFQN(fmt.Sprintf("teams::pilot-worker-%d", index+1))
		workspaceID := fmt.Sprintf("phase6-concurrency-%02d", index+1)
		workspace, baseline := createPhase6ConcurrencyWorkspace(t, root, workspaceID)
		fixture := newIntegratedFixture(t, now, base+index*0x100, actor, workspaceID, workspaceID)
		fixture.title = fmt.Sprintf("Concurrency probe %d", index+1)
		fixture.description = "Do not inspect the workspace and do not use any tools. Immediately return a final response confirming this bounded concurrency probe."
		fixture.acceptanceCriteria = []string{"one authorized model-backed invocation reaches a terminal result"}
		fixture.baselineSHA = baseline
		fixture.writablePaths = []string{"."}
		fixtures = append(fixtures, fixture)
		actors = append(actors, actor)
		workspaces = append(workspaces, ProductionWorkspace{WorkspaceID: workspaceID, WorktreeID: workspaceID, WorkingDirectory: workspace, Branch: "main", BaselineSHA: baseline, WritablePaths: []string{"."}})
	}
	bridge := startPhase6SMABridge(t, mongoURI, workspaces)
	defer stopPhase6Process(t, bridge, "SMA retrieval bridge")

	configPath := writePhase6ConcurrencyConfig(t, mongoURI, workspaces, actors, fixtures[0], 8)
	config, err := LoadProductionConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewProductionService(contextWithTimeout(t), config)
	if err != nil {
		t.Fatal(err)
	}
	serviceStopped := false
	defer func() {
		if !serviceStopped {
			_ = service.Stop(contextWithTimeout(t))
		}
		_ = service.Close(contextWithTimeout(t))
	}()
	if err := service.Start(contextWithTimeout(t)); err != nil {
		t.Fatal(err)
	}
	if err := service.Pause(contextWithTimeout(t)); err != nil {
		t.Fatal(err)
	}
	submit := func(t *testing.T, command kernel.KernelCommand) kernel.CommandReceipt {
		t.Helper()
		receipt, submitErr := service.Submit(contextWithTimeout(t), command)
		if submitErr != nil {
			t.Fatalf("%s: %v", command.CommandType, submitErr)
		}
		if receipt.OutcomeCode != kernel.OutcomeApplied || len(receipt.EventIDs) != 1 {
			t.Fatalf("%s receipt = %#v", command.CommandType, receipt)
		}
		return receipt
	}
	offset := 0
	for _, count := range counts {
		batch := fixtures[offset : offset+count]
		offset += count
		t.Run(fmt.Sprintf("concurrency-%d", count), func(t *testing.T) {
			for _, fixture := range batch {
				requirePhase5ConversationAbsent(t, fixture.invocationID)
				fixture.createAuthoritativeTaskWith(t, submit)
				story := waitForPhase6ConcurrencyStory(t, service, fixture.storyID, func(view mongo.StoryProjection) bool {
					return view.AggregateRevision == 1 && len(view.TaskIDs) == 1
				})
				activateProductStory(t, fixture, story, submit)
				fixture.authorizeInvocationWith(t, submit)
				waitForPhase6ConcurrencyInvocation(t, service, fixture.invocationID, func(value InvocationStatus) bool {
					return value.State == kernel.InvocationAuthorized
				})
			}
			if err := service.Resume(contextWithTimeout(t)); err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			peak := waitForPhase6ConcurrentBatch(t, service, batch)
			t.Logf("concurrency=%d peak=%d elapsed=%s", count, peak, time.Since(started).Round(time.Millisecond))
			if err := service.Pause(contextWithTimeout(t)); err != nil {
				t.Fatal(err)
			}
			if peak < count {
				t.Fatalf("concurrency %d observed peak %d", count, peak)
			}
		})
	}

	if err := service.Stop(contextWithTimeout(t)); err != nil {
		t.Fatal(err)
	}
	serviceStopped = true
}

func createPhase6ConcurrencyWorkspace(t *testing.T, root, name string) (string, string) {
	t.Helper()
	workspace := filepath.Join(root, "workspaces", name)
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
	writeText(t, filepath.Join(workspace, "README.md"), "# Bounded concurrency probe\n", 0o600)
	for _, arguments := range [][]string{{"init", "-b", "main"}, {"config", "user.email", "phase6@tekroo.local"}, {"config", "user.name", "Tekroo Phase 6"}, {"add", "."}, {"commit", "-m", "seed concurrency probe"}} {
		command := exec.Command("git", arguments...)
		command.Dir = workspace
		if output, commandErr := command.CombinedOutput(); commandErr != nil {
			t.Fatalf("git %v: %v\n%s", arguments, commandErr, output)
		}
	}
	command := exec.Command("git", "rev-parse", "HEAD")
	command.Dir = workspace
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	return workspace, string(bytes.TrimSpace(output))
}

func writePhase6ConcurrencyConfig(t *testing.T, mongoURI string, workspaces []ProductionWorkspace, actors []kernel.ActorFQN, fixture *integratedFixture, maximumConcurrency uint32) string {
	t.Helper()
	path, config := writeProductionFixture(t)
	directory := filepath.Dir(path)
	writeText(t, filepath.Join(directory, config.Mongo.URIFile), mongoURI+"\n", 0o600)
	provenance, err := fake.ProvenanceBasis()
	if err != nil {
		t.Fatal(err)
	}
	policy := integratedPolicy()
	for index, actor := range actors {
		policy.Grants = append(policy.Grants, kernel.AuthorityGrant{
			GrantDigest: digestBytes([]byte(fmt.Sprintf("phase6-concurrency-grant-%d", index))),
			Grantee:     kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: string(actor)},
			Scope: kernel.AuthorityScope{
				CommandTypes: []string{"tekroo.command.task.acquire-ownership", "tekroo.command.task.activate", "tekroo.command.task.request-completion"},
				TargetKinds:  []kernel.AggregateKind{kernel.AggregateTask}, CanReadTarget: true,
			},
		})
	}
	writeJSON(t, filepath.Join(directory, config.AuthorizationPolicyFile), policy, 0o600)
	writeJSON(t, filepath.Join(directory, config.ProvenanceFile), provenance, 0o600)
	database := fmt.Sprintf("tekroo_phase6_concurrency_%d", time.Now().UnixNano())
	config.Mongo.Database, config.TeamsDatabaseIdentity = database, database
	config.SMADatabaseIdentity = database + "_sma"
	config.DeploymentIdentity = digestByte('6')
	config.OpenHands.BaseURL = phase5OpenHandsURL
	config.OpenHands.SessionAPIKeyFile = phase5SessionKeyFile
	config.OpenHands.RequestTimeout = "20m"
	config.OpenHands.PollInterval = "100ms"
	config.Operator.Address = freeProductSurfaceAddress(t)
	config.Operator.BearerTokenFile = phase5SessionKeyFile
	config.Operator.Principal = fixture.human
	config.Workspaces = workspaces
	config.Profiles[0].MaximumIterations = 4
	config.Execution.ConsumerID = "teams-phase6-concurrency"
	config.Execution.OperationTimeout = "20m"
	config.Worker.LeaseDuration = "90s"
	config.Worker.ReconciliationInterval = "100ms"
	config.Worker.MaximumReconciliations = 40
	config.Worker.MaximumConcurrentInvocations = maximumConcurrency
	config.Worker.LeaseOperationTimeout = "5s"
	config.Projection.Interval = "50ms"
	config.Projection.OperationTimeout = "2s"
	writeJSON(t, path, config, 0o600)
	return path
}

func waitForPhase6ConcurrencyStory(t *testing.T, service *ProductionService, id kernel.UUIDv7, predicate func(mongo.StoryProjection) bool) mongo.StoryProjection {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		story, found, err := service.ReadStory(contextWithTimeout(t), id)
		if err != nil {
			t.Fatal(err)
		}
		if found && predicate(story) {
			return story
		}
		if time.Now().After(deadline) {
			t.Fatalf("story %s projection timeout", id)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func waitForPhase6ConcurrencyInvocation(t *testing.T, service *ProductionService, id kernel.UUIDv7, predicate func(InvocationStatus) bool) InvocationStatus {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		status, found, err := service.ReadInvocation(contextWithTimeout(t), id)
		if err != nil {
			t.Fatal(err)
		}
		if found && predicate(status) {
			return status
		}
		if time.Now().After(deadline) {
			t.Fatalf("invocation %s projection timeout", id)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func waitForPhase6ConcurrentBatch(t *testing.T, service *ProductionService, fixtures []*integratedFixture) int {
	t.Helper()
	deadline := time.Now().Add(20 * time.Minute)
	peak := 0
	for {
		active, terminal := 0, 0
		for _, fixture := range fixtures {
			status, found, err := service.ReadInvocation(contextWithTimeout(t), fixture.invocationID)
			if err != nil {
				t.Fatal(err)
			}
			if !found {
				t.Fatalf("invocation %s disappeared", fixture.invocationID)
			}
			if status.State == kernel.InvocationClaimed || status.State == kernel.InvocationStarted {
				active++
			}
			if status.State.Terminal() {
				terminal++
				if status.State != kernel.InvocationSucceeded {
					var output []byte
					if status.OutputDigest != nil {
						output, _ = service.Runtime.ReadExecutionOutput(contextWithTimeout(t), *status.OutputDigest)
					}
					t.Fatalf("concurrency invocation %s = %#v\noutput=%s", fixture.invocationID, status, output)
				}
			}
		}
		if active > peak {
			peak = active
		}
		if terminal == len(fixtures) {
			return peak
		}
		if time.Now().After(deadline) {
			t.Fatalf("concurrency batch timeout: terminal=%d/%d peak=%d", terminal, len(fixtures), peak)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
