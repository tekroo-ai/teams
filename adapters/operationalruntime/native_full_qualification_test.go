//go:build mongo_integration && native_qualification

package operationalruntime

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/nativeagent"
	"github.com/tekroo-ai/teams/adapters/openhands"
	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// This opt-in qualification runs the normal feature coordinator with a native
// execution boundary, an isolated mongod, and disposable Git worktrees. It
// never changes the file-backed production configuration or daemon.
func TestNativeFullFeatureQualification(t *testing.T) {
	configPath := os.Getenv("TEKROO_NATIVE_QUAL_CONFIG")
	baseURL := os.Getenv("TEKROO_NATIVE_QUAL_BASE_URL")
	model := os.Getenv("TEKROO_NATIVE_QUAL_MODEL")
	if configPath == "" || baseURL == "" || model == "" {
		t.Skip("requires explicit source config and local model identity")
	}
	config, err := LoadProductionConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	modelClient := &http.Client{Timeout: 15 * time.Second}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := modelClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var available struct {
		Data []struct {
			ID    string `json:"id"`
			Ready bool   `json:"loaded"`
		} `json:"data"`
	}
	decodeErr := json.NewDecoder(response.Body).Decode(&available)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || decodeErr != nil {
		t.Fatalf("model preflight status=%d err=%v", response.StatusCode, decodeErr)
	}
	ready := false
	for _, item := range available.Data {
		ready = ready || item.ID == model && item.Ready
	}
	if !ready {
		t.Fatalf("exact local model %q is not ready", model)
	}

	mongod, mongoURI := startRuntimeMongod(t)
	defer stopRuntimeMongod(mongod)
	root := t.TempDir()
	t.Cleanup(func() { makeWritableForCleanup(t, root) })
	nativeQualificationBindSignedCoderSuccessor(t, &config, root)
	nativeQualificationRequireCoderTestPermission(t, config.Organization.ManifestFile)
	workspaces, bare, baseline := nativeQualificationWorkspaces(t, root, config.Workspaces)
	uriFile := filepath.Join(root, "mongo-uri")
	if err := os.WriteFile(uriFile, []byte(mongoURI+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	config.Mongo.URIFile = uriFile
	config.Mongo.Database = fmt.Sprintf("tekroo_native_qualification_%d", time.Now().UnixNano())
	config.TeamsDatabaseIdentity = config.Mongo.Database
	config.SMADatabaseIdentity = config.Mongo.Database + "_sma_unused"
	deployment := sha256.Sum256([]byte(config.Mongo.Database))
	config.DeploymentIdentity = kernel.Digest(hex.EncodeToString(deployment[:]))
	config.EvidenceRoot = filepath.Join(root, "evidence")
	config.Workspaces = workspaces
	config.OpenHands.BaseURL = "http://127.0.0.1:1"
	config.Operator.Address = freeProductSurfaceAddress(t)
	config.Git = &ProductionGitConfig{Binary: "git", AllowedRoot: filepath.Dir(bare), OperationTimeout: "2m"}
	config.Worker.StartPaused = false
	config.Worker.MaximumConcurrentInvocations = 2
	config.Execution.ConsumerID = "native-qualification"
	config.Worker.ReconciliationInterval = "250ms"
	config.Planning.Deadline = "90m"
	nativeQualificationBindNativeProfiles(t, &config, root, baseURL, model)
	startup, cancelStartup := context.WithTimeout(context.Background(), 30*time.Second)
	service, err := NewProductionService(startup, config)
	cancelStartup()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = service.Stop(closeCtx)
		if err := service.Close(closeCtx); err != nil {
			t.Error(err)
		}
	}()
	startCtx, cancelStart := context.WithTimeout(context.Background(), 30*time.Second)
	err = service.Start(startCtx)
	cancelStart()
	if err != nil {
		t.Fatal(err)
	}
	featureCtx, cancelFeature := context.WithTimeout(context.Background(), 90*time.Minute)
	defer cancelFeature()
	feature, created, err := service.SubmitFeature(featureCtx, config.Operator.Principal, organization.FeatureRequestInput{
		IdempotencyKey: "native-qualification-greeting", Team: "teams",
		Title:              "Complete the greeting function",
		Description:        "Make Greeting(name) return Hello, followed by the name and an exclamation mark. Keep the change simple.",
		AcceptanceCriteria: []string{"Greeting(\"Ada\") returns \"Hello, Ada!\"", "Go tests pass", "the implementation is committed"},
		Priority:           organization.PriorityNormal, Repository: "native-qualification", WorkspaceID: "coder-1",
		MaximumStories: 1, MaximumTasks: 4, MaximumHops: 8,
	})
	if err != nil || !created {
		t.Fatalf("submit feature: created=%t err=%v", created, err)
	}
	t.Logf("native feature=%s database=%s baseline=%s", feature.ID, config.Mongo.Database, baseline)
	defer func() {
		if output := os.Getenv("TEKROO_NATIVE_QUAL_OUTPUT"); t.Failed() && output != "" {
			if _, err := os.Stat(filepath.Join(output, "native-evidence.json")); err == nil {
				return
			}
			if current, found, err := service.ReadFeature(featureCtx, feature.ID); err == nil && found {
				retainNativeQualificationEvidence(t, featureCtx, service, config, current, baseline, output)
			}
		}
	}()
	deadline := time.Now().Add(90 * time.Minute)
	lastStatus := feature.Status
	var admittedAt time.Time
	for time.Now().Before(deadline) {
		for _, fault := range service.readRecoveryFaults() {
			if fault.Scope == "feature-work" && fault.Occurrences >= 10 && time.Since(fault.FirstObserved) > 3*time.Second {
				t.Fatalf("repeated feature-work recovery fault after %d occurrences: %s", fault.Occurrences, fault.Error)
			}
		}
		current, found, readErr := service.ReadFeature(featureCtx, feature.ID)
		if readErr != nil || !found {
			t.Fatalf("read feature: found=%t err=%v", found, readErr)
		}
		if current.Status != lastStatus {
			t.Logf("feature status %s -> %s", lastStatus, current.Status)
			lastStatus = current.Status
		}
		if current.Plan != nil {
			for _, planned := range current.Plan.Tasks {
				state, _, foundTask, stateErr := service.Store.ReadAggregateHead(featureCtx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: planned.ID})
				if stateErr != nil {
					t.Fatalf("read task %s state: %v", planned.ID, stateErr)
				}
				if foundTask && state.Condition == kernel.ConditionBlocked {
					if output := os.Getenv("TEKROO_NATIVE_QUAL_OUTPUT"); output != "" {
						retainNativeQualificationEvidence(t, featureCtx, service, config, current, baseline, output)
					}
					t.Fatalf("native task %s purpose=%s reached BLOCKED; feature remains %s", planned.ID, planned.Purpose, current.Status)
				}
				decision, decisionErr := service.Store.LoadDecision(featureCtx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: planned.ID}})
				if decisionErr != nil {
					continue
				}
				invocation, exists := latestTaskInvocation(decision.WorkInvocations, planned.ID)
				if !exists || invocation.State != kernel.InvocationFailed && invocation.State != kernel.InvocationStartFailed && invocation.State != kernel.InvocationTimedOut {
					continue
				}
				if invocation.OutputDigest != nil {
					output, outputErr := service.Runtime.ReadExecutionOutput(featureCtx, *invocation.OutputDigest)
					t.Logf("task %s failure output=%q read_error=%v", planned.ID, output, outputErr)
				}
				t.Fatalf("task %s purpose=%s invocation=%s state=%s retryable=%v", planned.ID, planned.Purpose, invocation.ID, invocation.State, invocation.Retryable)
			}
		}
		if (current.Status == organization.FeaturePlanned || current.Status == organization.FeatureApproved) && current.Plan != nil {
			if admittedAt.IsZero() {
				admittedAt = time.Now()
			}
			if time.Since(admittedAt) > 90*time.Second {
				started := false
				for _, planned := range current.Plan.Tasks {
					if planned.Purpose != kernel.PurposeImplementation {
						continue
					}
					decision, decisionErr := service.Store.LoadDecision(featureCtx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: planned.ID}})
					if decisionErr != nil {
						t.Logf("implementation task %s load decision: %v", planned.ID, decisionErr)
						continue
					}
					invocation, exists := latestTaskInvocation(decision.WorkInvocations, planned.ID)
					t.Logf("implementation task %s invocation_exists=%t state=%s", planned.ID, exists, invocation.State)
					started = started || exists
				}
				if !started {
					t.Fatal("no implementation invocation was admitted within 90 seconds of the plan")
				}
				admittedAt = time.Time{}
			}
		}
		for _, stage := range []featurePlanningStage{stageRefinement, stageSpecification, stageArchitecture, stagePlanFinalization} {
			taskID := featurePlanningTaskID(feature.ID, stage, 0, nil)
			state, _, foundTask, stateErr := service.Store.ReadAggregateHead(featureCtx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: taskID})
			if stateErr != nil || !foundTask {
				continue
			}
			decision, decisionErr := service.Store.LoadDecision(featureCtx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: taskID}})
			if decisionErr != nil {
				continue
			}
			invocation, exists := latestTaskInvocation(decision.WorkInvocations, taskID)
			if !exists || invocation.State != kernel.InvocationFailed && state.Condition != kernel.ConditionBlocked {
				continue
			}
			t.Logf("planning stage %s stopped: task_condition=%s invocation=%s actor=%s state=%s", stage, state.Condition, invocation.ID, invocation.ActorFQN, invocation.State)
			entries, journalErr := service.Store.Load(featureCtx, string(invocation.ID))
			if journalErr != nil {
				t.Logf("read native journal: %v", journalErr)
			} else {
				for _, entry := range entries {
					if entry.Kind == "tool_done" || entry.Kind == "failed" {
						t.Logf("native journal %d %s %s", entry.Sequence, entry.Kind, entry.Payload)
					} else if entry.Kind == "model_turn" && strings.Contains(string(entry.Payload), `"name":"submit_result"`) {
						t.Logf("native final proposal %d (first 16000 bytes): %.16000s", entry.Sequence, entry.Payload)
					}
				}
			}
			if invocation.OutputDigest != nil {
				output, outputErr := service.Runtime.ReadExecutionOutput(featureCtx, *invocation.OutputDigest)
				if outputErr == nil {
					t.Logf("stage output validation: %v", service.validateFeatureStageOutput(current, stage, output))
					t.Logf("stage output (first 16000 bytes): %.16000s", output)
				}
			}
			t.Fatalf("native planning stage %s stopped; no automatic feature retry", stage)
		}
		if current.Status == organization.FeatureAwaitingAcceptance {
			if current.Plan == nil || current.Acceptance == nil {
				t.Fatalf("incomplete final feature state: %#v", current)
			}
			if output := os.Getenv("TEKROO_NATIVE_QUAL_OUTPUT"); output != "" {
				retainNativeQualificationEvidence(t, featureCtx, service, config, current, baseline, output)
			}
			requireNoNativeToolErrors(t, featureCtx, service, current)
			t.Logf("native full feature reached acceptance review: %s", current.ID)
			return
		}
		if current.Status == organization.FeatureClarificationRequired || current.Status == organization.FeatureCancelled {
			t.Fatalf("native feature stopped at %s: %#v", current.Status, current)
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("native feature did not finish before bounded deadline; last status=%s", lastStatus)
}

func requireNoNativeToolErrors(t *testing.T, ctx context.Context, service *ProductionService, feature organization.FeatureRequest) {
	t.Helper()
	taskIDs := make([]kernel.UUIDv7, 0, 4+len(feature.Plan.Tasks))
	for _, stage := range []featurePlanningStage{stageRefinement, stageSpecification, stageArchitecture, stagePlanFinalization} {
		taskIDs = append(taskIDs, featurePlanningTaskID(feature.ID, stage, 0, nil))
	}
	for _, task := range feature.Plan.Tasks {
		taskIDs = append(taskIDs, task.ID)
	}
	for _, taskID := range taskIDs {
		decision, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: taskID}})
		if err != nil {
			t.Fatalf("load task %s for tool-error audit: %v", taskID, err)
		}
		for _, invocation := range decision.WorkInvocations {
			if invocation.TaskID != taskID {
				continue
			}
			entries, err := service.Store.Load(ctx, string(invocation.ID))
			if err != nil {
				t.Fatalf("load invocation %s journal for tool-error audit: %v", invocation.ID, err)
			}
			for _, entry := range entries {
				if entry.Kind == "tool_done" {
					var result struct {
						Name  string `json:"name"`
						Error string `json:"error"`
					}
					if err := json.Unmarshal(entry.Payload, &result); err != nil {
						t.Fatalf("decode invocation %s tool result %d: %v", invocation.ID, entry.Sequence, err)
					}
					if result.Error != "" {
						t.Errorf("native tool error actor=%s invocation=%s sequence=%d tool=%s: %s", invocation.ActorFQN, invocation.ID, entry.Sequence, result.Name, result.Error)
					}
				}
				if strings.Contains(string(entry.Payload), "TEKROO_") && strings.Contains(string(entry.Payload), "CORRECTION") {
					t.Errorf("native Tekroo correction actor=%s invocation=%s sequence=%d", invocation.ActorFQN, invocation.ID, entry.Sequence)
				}
			}
		}
	}
}

// Retain the raw native journal and execution outputs before the disposable
// MongoDB and workspaces are removed. This is a measurement receipt, not a
// production model qualification: admission above uses synthetic fixtures.
func retainNativeQualificationEvidence(t *testing.T, ctx context.Context, service *ProductionService, config ProductionConfig, feature organization.FeatureRequest, baseline, output string) {
	t.Helper()
	if !filepath.IsAbs(output) {
		t.Fatal("native qualification output must be an absolute new directory")
	}
	if err := os.Mkdir(output, 0700); err != nil {
		t.Fatalf("create new native qualification output: %v", err)
	}
	type invocationEvidence struct {
		Invocation kernel.WorkInvocation `json:"invocation"`
		Journal    any                   `json:"journal"`
		Output     any                   `json:"output,omitempty"`
	}
	type taskEvidence struct {
		TaskID      kernel.UUIDv7         `json:"task_id"`
		StateFound  bool                  `json:"state_found"`
		State       kernel.AggregateState `json:"state"`
		Invocations []invocationEvidence  `json:"invocations"`
	}
	var plannedTasks []organization.PlannedTask
	if feature.Plan != nil {
		plannedTasks = feature.Plan.Tasks
	}
	tasks := make([]taskEvidence, 0, 4+len(plannedTasks))
	collect := func(taskID kernel.UUIDv7) {
		t.Helper()
		decision, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: taskID}})
		if err != nil {
			t.Fatalf("load task %s for raw native evidence: %v", taskID, err)
		}
		state, _, found, stateErr := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: taskID})
		if stateErr != nil {
			t.Fatalf("read task %s state for raw native evidence: %v", taskID, stateErr)
		}
		item := taskEvidence{TaskID: taskID, StateFound: found, State: state}
		for _, invocation := range decision.WorkInvocations {
			if invocation.TaskID != taskID {
				continue
			}
			journal, err := service.Store.Load(ctx, string(invocation.ID))
			if err != nil {
				t.Fatalf("load native journal %s: %v", invocation.ID, err)
			}
			entry := invocationEvidence{Invocation: invocation, Journal: journal}
			if invocation.OutputDigest != nil {
				value, err := service.Runtime.ReadExecutionOutput(ctx, *invocation.OutputDigest)
				if err != nil {
					t.Fatalf("load native output %s: %v", invocation.ID, err)
				}
				entry.Output = value
			}
			item.Invocations = append(item.Invocations, entry)
		}
		slices.SortFunc(item.Invocations, func(a, b invocationEvidence) int {
			return strings.Compare(string(a.Invocation.ID), string(b.Invocation.ID))
		})
		tasks = append(tasks, item)
	}
	for _, stage := range []featurePlanningStage{stageRefinement, stageSpecification, stageArchitecture, stagePlanFinalization} {
		collect(featurePlanningTaskID(feature.ID, stage, 0, nil))
	}
	for _, planned := range plannedTasks {
		collect(planned.ID)
	}
	receipt := struct {
		SchemaVersion string                      `json:"schema_version"`
		MeasuredAt    time.Time                   `json:"measured_at"`
		Baseline      string                      `json:"baseline"`
		Model         string                      `json:"model"`
		Manifest      kernel.Digest               `json:"manifest_digest"`
		Profiles      []ProductionProfile         `json:"profiles"`
		Feature       organization.FeatureRequest `json:"feature"`
		Tasks         []taskEvidence              `json:"tasks"`
	}{"native-feature-measurement/1", time.Now().UTC(), baseline, config.Profiles[0].NativeSettings.Model, config.Organization.ManifestDigest, config.Profiles, feature, tasks}
	raw, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	path := filepath.Join(output, "native-evidence.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatalf("retain native qualification evidence: %v", err)
	}
	digest := sha256.Sum256(raw)
	t.Logf("native raw evidence path=%s sha256=%x", path, digest)
}

func nativeQualificationWorkspaces(t *testing.T, root string, original []ProductionWorkspace) ([]ProductionWorkspace, string, string) {
	t.Helper()
	seed := filepath.Join(root, "seed")
	if err := os.MkdirAll(seed, 0700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"AGENTS.md":        "Use the assigned Teams role. This Go module is the whole repository. Keep changes simple and test them.\n",
		"go.mod":           "module example.test/nativequalification\n\ngo 1.26.0\n",
		"greeting.go":      "package greeting\n\nfunc Greeting(name string) string { return \"Hello\" }\n",
		"greeting_test.go": "package greeting\nimport \"testing\"\nfunc TestGreeting(t *testing.T) { if Greeting(\"Ada\") == \"\" { t.Fatal(\"empty greeting\") } }\n",
	} {
		if err := os.WriteFile(filepath.Join(seed, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-b", "main"}, {"config", "user.name", "Native Qualification"}, {"config", "user.email", "native@example.test"}, {"add", "."}, {"commit", "-m", "baseline"}} {
		nativeQualificationGit(t, seed, args...)
	}
	baseline := nativeQualificationGit(t, seed, "rev-parse", "HEAD")
	allowed := filepath.Join(root, "repositories")
	if err := os.MkdirAll(allowed, 0700); err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(allowed, "native-qualification.git")
	nativeQualificationGit(t, root, "clone", "--bare", seed, bare)
	workspaces := make([]ProductionWorkspace, 0, len(original))
	for _, existing := range original {
		branch := "native/" + existing.WorkspaceID
		workspace := filepath.Join(root, "workspaces", existing.WorkspaceID)
		nativeQualificationGit(t, root, "--git-dir", bare, "worktree", "add", "-b", branch, workspace, "main")
		nativeQualificationGit(t, workspace, "config", "user.name", "Native Qualification")
		nativeQualificationGit(t, workspace, "config", "user.email", "native@example.test")
		workspaces = append(workspaces, ProductionWorkspace{WorkspaceID: existing.WorkspaceID, WorktreeID: existing.WorktreeID,
			WorkingDirectory: workspace, Branch: branch, BaselineSHA: baseline, WritablePaths: []string{"."}})
	}
	return workspaces, bare, baseline
}

func nativeQualificationGit(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func nativeQualificationBindSignedCoderSuccessor(t *testing.T, config *ProductionConfig, root string) {
	t.Helper()
	sourceManifest := config.Organization.ManifestFile
	raw, err := os.ReadFile(sourceManifest)
	if err != nil {
		t.Fatal(err)
	}
	originalDigest := sha256.Sum256(raw)
	if kernel.Digest(hex.EncodeToString(originalDigest[:])) != config.Organization.ManifestDigest {
		t.Fatal("source team manifest changed before isolated qualification")
	}
	var manifest organization.TeamManifest
	if err := json.Unmarshal(raw, &manifest); err != nil || manifest.Validate() != nil {
		t.Fatalf("source team manifest is invalid: %v", err)
	}
	successor, err := filepath.Abs(filepath.Join("..", "..", "config", "starter-team", "transport-neutral-20260929", "roles-v4", "coder-2.1.1", "role.json"))
	if err != nil {
		t.Fatal(err)
	}
	publicPath := filepath.Join(filepath.Dir(successor), "publisher.pub")
	publicRaw, err := os.ReadFile(publicPath)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(publicRaw)))
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		t.Fatal("candidate coder publisher key is invalid")
	}
	bundleRaw, err := os.ReadFile(successor)
	if err != nil {
		t.Fatal(err)
	}
	var bundle organization.RoleBundle
	if err := json.Unmarshal(bundleRaw, &bundle); err != nil || bundle.Validate() != nil || bundle.Role != "coder" || bundle.Version != "2.1.1" || !slices.Contains(bundle.Permissions, "test.execute") {
		t.Fatal("candidate coder role package is invalid")
	}
	bundleDigest, err := bundle.ContentDigest()
	if err != nil {
		t.Fatal(err)
	}
	var coderProfile *ProductionProfile
	for index := range config.Profiles {
		if config.Profiles[index].RoleFQRN == "coder" {
			coderProfile = &config.Profiles[index]
			break
		}
	}
	if coderProfile == nil || coderProfile.Qualification == nil || coderProfile.QualificationCorpus == nil {
		t.Fatal("source coder profile lacks a qualification fixture")
	}
	modelDigest, err := openhands.ModelProfileDigest(coderProfile.RoleFQRN, bundleDigest, coderProfile.AgentSettings)
	if err != nil {
		t.Fatal(err)
	}
	// This synthetic admission fixture exists only in the disposable test config.
	// It is not evidence that the successor model profile is production-qualified.
	ids, err := NewUUIDv7Source(SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	qualificationID, err := ids.Next()
	if err != nil {
		t.Fatal(err)
	}
	evidenceID, err := ids.Next()
	if err != nil {
		t.Fatal(err)
	}
	qualification := coderProfile.Qualification.Clone()
	qualification.QualificationID = qualificationID
	qualification.EvidenceIDs = []kernel.UUIDv7{evidenceID}
	qualification.ModelProfileDigest = modelDigest
	qualification.ObservedAt = time.Now().UTC().Add(-time.Second)
	expires := qualification.ObservedAt.Add(2 * time.Hour)
	qualification.ExpiresAt = &expires
	qualification.RevokedAt = nil
	qualification.QualificationDigest, err = application.QualificationDigest(qualification)
	if err != nil {
		t.Fatal(err)
	}
	coderProfile.Qualification = &qualification
	coderProfile.ModelProfileDigest = modelDigest
	coderProfile.RoleBundleDigest = bundleDigest
	if _, err := organization.LoadRoleBundle(successor, bundleDigest, bundle.PublisherKeyID, map[string]ed25519.PublicKey{bundle.PublisherKeyID: publicKey}); err != nil {
		t.Fatalf("candidate coder signature: %v", err)
	}
	teamRoot := filepath.Join(root, "team")
	if err := os.MkdirAll(filepath.Join(teamRoot, "roles"), 0700); err != nil {
		t.Fatal(err)
	}
	foundCoder := false
	for index := range manifest.Roles {
		role := &manifest.Roles[index]
		packagePath := filepath.Join(filepath.Dir(sourceManifest), role.BundlePath)
		if role.Role == "coder" {
			foundCoder = true
			packagePath = successor
			role.BundleDigest = bundleDigest
			role.PublisherKeyID = bundle.PublisherKeyID
			role.ModelProfileDigest = modelDigest
		}
		link := filepath.Join(teamRoot, "roles", role.Role)
		if err := os.Symlink(filepath.Dir(packagePath), link); err != nil {
			t.Fatal(err)
		}
		role.BundlePath = filepath.Join("roles", role.Role, "role.json")
	}
	if !foundCoder {
		t.Fatal("source team manifest has no coder role")
	}
	manifest.Version = "1.0.3"
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil || manifest.Validate() != nil {
		t.Fatalf("candidate team manifest: %v", err)
	}
	encoded = append(encoded, '\n')
	manifestPath := filepath.Join(teamRoot, "team.json")
	if err := os.WriteFile(manifestPath, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	manifestDigest := sha256.Sum256(encoded)
	config.Organization.ManifestFile = manifestPath
	config.Organization.ManifestDigest = kernel.Digest(hex.EncodeToString(manifestDigest[:]))
	config.Organization.Publishers = append(config.Organization.Publishers, ProductionPublisher{KeyID: bundle.PublisherKeyID, PublicKeyFile: publicPath})
	t.Logf("isolated signed coder successor bundle=%s manifest=%s profile=%s synthetic_admission=true", bundleDigest, config.Organization.ManifestDigest, modelDigest)
}

// The model-admission records below are test-only bootstrap fixtures. The
// measured run must publish its own raw receipts before any native profile can
// be treated as qualified outside this disposable namespace.
func nativeQualificationBindNativeProfiles(t *testing.T, config *ProductionConfig, root, baseURL, model string) {
	t.Helper()
	manifestPath := config.Organization.ManifestFile
	var manifest organization.TeamManifest
	if err := readStrictJSONFile(manifestPath, &manifest); err != nil {
		t.Fatal(err)
	}
	ids, err := NewUUIDv7Source(SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	for index := range config.Profiles {
		profile := &config.Profiles[index]
		var old struct {
			LLM struct {
				MaximumOutput int `json:"max_output_tokens"`
			} `json:"llm"`
		}
		if err := json.Unmarshal(profile.AgentSettings, &old); err != nil {
			t.Fatal(err)
		}
		maximumOutput := old.LLM.MaximumOutput
		if maximumOutput < 8192 {
			maximumOutput = 8192
		}
		if profile.RoleFQRN == "project-manager" && maximumOutput < 32768 {
			maximumOutput = 32768
		}
		settings := nativeagent.ProfileSettings{SchemaVersion: nativeagent.ProfileSettingsVersion, BaseURL: baseURL, Model: model, ResponseMode: os.Getenv("TEKROO_NATIVE_QUAL_RESPONSE_MODE"), MaxOutputTokens: maximumOutput}
		modelDigest, err := nativeagent.ModelProfileDigest(profile.RoleFQRN, profile.RoleBundleDigest, settings)
		if err != nil {
			t.Fatal(err)
		}
		var permissions []string
		for bindingIndex := range manifest.Roles {
			if manifest.Roles[bindingIndex].Role != string(profile.RoleFQRN) {
				continue
			}
			bundlePath := filepath.Join(filepath.Dir(manifestPath), manifest.Roles[bindingIndex].BundlePath)
			var bundle organization.RoleBundle
			if err := readStrictJSONFile(bundlePath, &bundle); err != nil {
				t.Fatal(err)
			}
			permissions = bundle.Permissions
			manifest.Roles[bindingIndex].ModelProfileDigest = modelDigest
			break
		}
		if permissions == nil {
			t.Fatalf("native qualification role %s is absent from signed manifest", profile.RoleFQRN)
		}
		surface, err := agenttools.NativeToolSurfaceDigest(permissions)
		if err != nil {
			t.Fatal(err)
		}
		profile.ModelProfileDigest = modelDigest
		profile.ToolPolicyDigest = surface
		profile.NativeSettings = &settings
		profile.AgentSettings = nil
		profile.MaximumIterations = 0
		if profile.Qualification == nil || profile.QualificationCorpus == nil {
			continue
		}
		corpus := *profile.QualificationCorpus
		corpus.ToolSurfaceDigest = surface
		corpusDigest, err := corpus.Digest()
		if err != nil {
			t.Fatal(err)
		}
		qualification := profile.Qualification.Clone()
		qualification.QualificationID, err = ids.Next()
		if err != nil {
			t.Fatal(err)
		}
		evidenceID, err := ids.Next()
		if err != nil {
			t.Fatal(err)
		}
		qualification.EvidenceIDs = []kernel.UUIDv7{evidenceID}
		qualification.ModelProfileDigest = modelDigest
		qualification.QualificationCorpusDigest = corpusDigest
		qualification.ObservedAt = time.Now().UTC().Add(-time.Second)
		expires := qualification.ObservedAt.Add(2 * time.Hour)
		qualification.ExpiresAt = &expires
		qualification.RevokedAt = nil
		qualification.QualificationDigest, err = application.QualificationDigest(qualification)
		if err != nil {
			t.Fatal(err)
		}
		profile.QualificationCorpus = &corpus
		profile.Qualification = &qualification
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil || manifest.Validate() != nil {
		t.Fatalf("native qualification manifest: %v", err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(manifestPath, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	manifestDigest := sha256.Sum256(encoded)
	config.Organization.ManifestDigest = kernel.Digest(hex.EncodeToString(manifestDigest[:]))
	schemas := nativeQualificationSchemas(t, manifestPath)
	refs := make([]string, 0, len(schemas))
	for ref := range schemas {
		refs = append(refs, ref)
	}
	slices.Sort(refs)
	bound := make([]ProductionNativeResultSchema, 0, len(refs))
	for index, ref := range refs {
		path := filepath.Join(root, fmt.Sprintf("native-result-%d.schema.json", index))
		if err := os.WriteFile(path, schemas[ref].JSON, 0600); err != nil {
			t.Fatal(err)
		}
		bound = append(bound, ProductionNativeResultSchema{Reference: ref, Path: path, SHA256: schemas[ref].SHA256})
	}
	config.ExecutionBackend = "native"
	config.Native = &ProductionNativeConfig{Owner: "native-qualification", LeaseDuration: "90s", Heartbeat: "15s", RequestTimeout: "20m", WorkflowTriggers: []string{"tekroo.message.feature.submitted"}, ResultSchemas: bound}
	config.OpenHands = ProductionOpenHandsConfig{}
	t.Logf("isolated native profile manifest=%s bootstrap_admission=synthetic", config.Organization.ManifestDigest)
}

func nativeQualificationRequireCoderTestPermission(t *testing.T, manifestPath string) {
	t.Helper()
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest organization.TeamManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, role := range manifest.Roles {
		if role.Role != "coder" {
			continue
		}
		bundleRaw, err := os.ReadFile(filepath.Join(filepath.Dir(manifestPath), role.BundlePath))
		if err != nil {
			t.Fatal(err)
		}
		var bundle struct {
			Permissions []string `json:"permissions"`
		}
		if err := json.Unmarshal(bundleRaw, &bundle); err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(bundle.Permissions, "test.execute") {
			t.Fatal("native qualification cannot run: signed coder role lacks test.execute while admitted implementation tasks require go-test")
		}
		return
	}
	t.Fatal("native qualification cannot run: team manifest has no coder role")
}

func nativeQualificationSchemas(t *testing.T, manifestPath string) map[string]NativeResultSchema {
	t.Helper()
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest organization.TeamManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	type source struct{ ref, role, message string }
	sources := []source{
		{"tekroo.feature-refinement/1.0.0", "product-owner", "tekroo.message.feature.submitted"},
		{"tekroo.feature-specification/1.0.0", "project-manager", "tekroo.message.feature.refined"},
		{"tekroo.feature-design/1.0.0", "architect", "tekroo.message.story.design-requested"},
		{"tekroo.feature-execution-plan/1.0.0", "project-manager", "tekroo.message.feature.design-proposed"},
	}
	result := make(map[string]NativeResultSchema, len(sources))
	for _, item := range sources {
		var bundlePath string
		for _, role := range manifest.Roles {
			if role.Role == item.role {
				bundlePath = filepath.Join(filepath.Dir(manifestPath), role.BundlePath)
			}
		}
		if bundlePath == "" {
			t.Fatalf("missing role %s", item.role)
		}
		bundleRaw, err := os.ReadFile(bundlePath)
		if err != nil {
			t.Fatal(err)
		}
		var bundle struct {
			Handlers map[string]struct {
				ResultSchema struct {
					Path   string        `json:"path"`
					SHA256 kernel.Digest `json:"sha256"`
				} `json:"result_schema"`
			} `json:"handler_bindings"`
		}
		if err := json.Unmarshal(bundleRaw, &bundle); err != nil {
			t.Fatal(err)
		}
		handler, found := bundle.Handlers[item.message]
		if !found {
			t.Fatalf("role %s lacks %s", item.role, item.message)
		}
		schemaRaw, err := os.ReadFile(filepath.Join(filepath.Dir(bundlePath), handler.ResultSchema.Path))
		if err != nil {
			t.Fatal(err)
		}
		fullHash := sha256.Sum256(schemaRaw)
		if kernel.Digest(hex.EncodeToString(fullHash[:])) != handler.ResultSchema.SHA256 {
			t.Fatalf("signed result schema changed for %s", item.message)
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(schemaRaw, &schema); err != nil {
			t.Fatal(err)
		}
		workProduct := schema.Properties["work_product"]
		if len(workProduct) == 0 {
			t.Fatalf("role %s has no work_product schema", item.role)
		}
		productHash := sha256.Sum256(workProduct)
		result[item.ref] = NativeResultSchema{SHA256: kernel.Digest(hex.EncodeToString(productHash[:])), JSON: workProduct}
	}
	return result
}
