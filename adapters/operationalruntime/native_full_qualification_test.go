//go:build mongo_integration && native_qualification

package operationalruntime

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	schemas := nativeQualificationSchemas(t, config.Organization.ManifestFile)
	profiles := make(map[kernel.Digest]ProductionProfile, len(config.Profiles))
	for _, profile := range config.Profiles {
		profiles[profile.ModelProfileDigest] = profile
	}
	boundary := &nativeagent.Boundary{Owner: "native-qualification", LeaseTTL: 90 * time.Second, Heartbeat: 15 * time.Second}
	var service *ProductionService
	boundary.Configure = func(ctx context.Context, brief application.ExecutionBrief) (nativeagent.Config, error) {
		profile, found := profiles[brief.ModelProfileDigest]
		if !found || service == nil {
			return nativeagent.Config{}, nativeagent.ErrInvalidBinding
		}
		permissions := brief.RoleGrounding.Permissions
		readTools := make([]string, 0)
		for _, definition := range agenttools.ReadOnlyDefinitions(permissions) {
			readTools = append(readTools, definition.Name)
		}
		slices.Sort(readTools)
		effectTools := make([]string, 0)
		if brief.Purpose == kernel.PurposeImplementation || brief.Purpose == kernel.PurposeRepair {
			for _, definition := range agenttools.MutationDefinitions(permissions) {
				effectTools = append(effectTools, definition.Name)
			}
		}
		if brief.Purpose == kernel.PurposeValidation || brief.Purpose == kernel.PurposeImplementation || brief.Purpose == kernel.PurposeRepair {
			for _, definition := range agenttools.TestDefinitions(permissions) {
				effectTools = append(effectTools, definition.Name)
			}
		}
		slices.Sort(effectTools)
		var resultContract *nativeagent.ResultContract
		if brief.MessageHandler == nil && brief.ResultProtocol != nil && (brief.Purpose == kernel.PurposeHandoff || brief.Purpose == kernel.PurposeReplan) {
			definition, lookupErr := service.WorkflowLibrary.LookupTrigger("tekroo.message.feature.submitted")
			if lookupErr != nil {
				return nativeagent.Config{}, lookupErr
			}
			resultContract, lookupErr = ResolveNativeWorkflowResultContract(brief, definition, schemas)
			if lookupErr != nil {
				return nativeagent.Config{}, lookupErr
			}
		}
		maximumOutput := 8192
		var settings struct {
			LLM struct {
				MaximumOutput int `json:"max_output_tokens"`
			} `json:"llm"`
		}
		if json.Unmarshal(profile.AgentSettings, &settings) == nil && settings.LLM.MaximumOutput > 0 {
			maximumOutput = settings.LLM.MaximumOutput
		}
		// An 8192-token project-manager response ended with finish_reason=length
		// during qualification. Give signed planning results enough output
		// headroom without changing the existing production profile.
		if (brief.Purpose == kernel.PurposeHandoff || brief.Purpose == kernel.PurposeReplan) && maximumOutput < 32768 {
			maximumOutput = 32768
		}
		result := nativeagent.Config{
			Bindings: service.Runtime.toolGateway.Bindings,
			Gateway:  service.Runtime.toolGateway,
			HTTP:     &http.Client{Timeout: 20 * time.Minute},
			Profile: nativeagent.Profile{
				RoleFQRN: profile.RoleFQRN, RoleBundleDigest: profile.RoleBundleDigest,
				ModelProfileDigest: profile.ModelProfileDigest, RuntimeIdentityDigest: profile.RuntimeIdentityDigest,
				ToolPolicyDigest: profile.ToolPolicyDigest, EffectPolicyDigest: profile.EffectPolicyDigest,
				BaseURL: baseURL, Model: model, MaxOutputTokens: maximumOutput,
				AllowedReadTools: readTools, AllowedEffectTools: effectTools,
			},
			ResultContract: resultContract,
		}
		if len(effectTools) > 0 {
			result.Effects = service.Store
		}
		if strings.HasPrefix(brief.Scope.WorktreeID, "candidate-") {
			workspace, resolveErr := service.workspaceResolver.ResolveWorkspace(ctx, brief.Scope)
			if resolveErr != nil || workspace.Candidate == nil {
				return nativeagent.Config{}, nativeagent.ErrInvalidBinding
			}
			result.Candidate = &nativeagent.CandidateBinding{
				ID: kernel.UUIDv7(workspace.Candidate.CandidateID), ReceiptSHA256: workspace.Candidate.ReceiptSHA256,
			}
		}
		feature, stage, _, _, planning, lookupErr := service.planningFeatureForTask(ctx, brief.Task.TaskID)
		if lookupErr != nil {
			return nativeagent.Config{}, lookupErr
		}
		if planning && stage == stagePlanFinalization {
			_, architectureOutput, sourceErr := service.featureArchitectureCandidate(ctx, feature)
			if sourceErr != nil || feature.Design == nil || digestBytes(architectureOutput) != feature.Design.OutputDigest {
				return nativeagent.Config{}, errors.Join(nativeagent.ErrInvalidBinding, sourceErr)
			}
			result.PlanFinalization = &nativeagent.PlanFinalizationBinding{
				SourceOutput: append([]byte(nil), architectureOutput...), SourceDigest: feature.Design.OutputDigest,
			}
		}
		if planning {
			result.ValidateFinalResult = func(finalCtx context.Context, output []byte) error {
				return service.validateFeatureStageOutputWithEvidence(finalCtx, feature, stage, output)
			}
		}
		return result, nil
	}
	config.QualificationExecutionBoundary = boundary
	startup, cancelStartup := context.WithTimeout(context.Background(), 30*time.Second)
	service, err = NewProductionService(startup, config)
	cancelStartup()
	if err != nil {
		t.Fatal(err)
	}
	boundary.Control, boundary.Journal = service.Store, service.Store
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
