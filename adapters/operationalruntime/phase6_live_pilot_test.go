//go:build phase6_pilot && mongo_integration

package operationalruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/fake"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

const (
	phase6SMAJar = "/Users/paul/work/tekroo-ai/sma/target/sma-1.0-SNAPSHOT.jar"
)

func TestPhase6PilotReleaseConfigurationPreflight(t *testing.T) {
	root := t.TempDir()
	workspaces, repository := createPhase6PilotWorkspaces(t, root)
	configPath := writePhase6PilotConfig(t, "mongodb://127.0.0.1:27017/?directConnection=true", workspaces, repository.AllowedRoot, 1)
	loaded, err := LoadProductionConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Git == nil || loaded.Git.AllowedRoot != repository.AllowedRoot {
		t.Fatalf("release provider config = %#v", loaded.Git)
	}
	policy := phase6PilotPolicy(t)
	checks := []struct {
		command   string
		kind      kernel.AggregateKind
		authority kernel.PrincipalRef
	}{
		{command: "tekroo.command.release-plan.record-qualification", kind: kernel.AggregateReleasePlan, authority: kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "teams-admission-policy"}},
		{command: "tekroo.command.release-plan.request-execution", kind: kernel.AggregateReleasePlan, authority: kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "teams-admission-policy"}},
		{command: "tekroo.command.release-plan.record-result", kind: kernel.AggregateReleasePlan, authority: kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "teams-operational-runtime"}},
		{command: "tekroo.command.completion-review.record-result", kind: kernel.AggregateCompletionReview, authority: kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "teams-operational-runtime"}},
	}
	for _, check := range checks {
		command := kernel.KernelCommand{CommandType: check.command, Target: kernel.AggregateRef{Kind: check.kind, ID: "00000000-0000-7000-8000-000000006901"}, Authority: check.authority}
		if result := policy.Authorize(command, nil, time.Now().UTC()); !result.Allowed {
			t.Fatalf("%s authorization = %#v", check.command, result)
		}
	}
}

// TestPhase6LiveFeatureToAcceptance exercises autonomous refinement,
// specification, DAG design, implementation, independent validation, and the
// product-owner recommendation through the supported operator surface.
func TestPhase6LiveFeatureToAcceptance(t *testing.T) {
	requirePhase5LiveDependency(t, phase5OpenHandsURL+"/health", "status", "ok")
	requirePhase5LiveDependency(t, phase5ModelURL, "id", phase5ExpectedModelID)

	mongod, mongoURI := startRuntimeMongod(t)
	defer stopRuntimeMongod(mongod)
	root := t.TempDir()
	workspaces, repository := createPhase6PilotWorkspaces(t, root)
	bridge := startPhase6SMABridge(t, mongoURI, workspaces)
	defer stopPhase6Process(t, bridge, "SMA retrieval bridge")

	configPath := writePhase6PilotConfig(t, mongoURI, workspaces, repository.AllowedRoot, 1)
	tekrood, tekroo := buildProductSurfaceBinaries(t)
	service, stdout, stderr := startPhase5Tekrood(t, tekrood, configPath)
	defer func() {
		if t.Failed() {
			t.Logf("tekrood stdout:\n%s\ntekrood stderr:\n%s", stdout.String(), stderr.String())
		}
	}()
	serviceStopped := false
	defer func() {
		if !serviceStopped {
			_ = service.Process.Kill()
			_ = service.Wait()
		}
	}()
	waitForProductHealth(t, tekroo, configPath, stderr)

	featurePath := filepath.Join(root, "feature.json")
	writeJSON(t, featurePath, organization.FeatureRequestInput{
		IdempotencyKey: "phase6-live-feature-smoke", Team: "example",
		Title:              "Document the arithmetic package",
		Description:        "Add a concise package comment to arithmetic.go, preserve behavior, run go test ./..., and commit the change on the assigned branch.",
		AcceptanceCriteria: []string{"arithmetic.go contains a package comment", "go test ./... passes", "the implementation is committed"},
		Priority:           organization.PriorityNormal,
		Constraints:        []string{"Do not change exported function behavior", "Do not delegate to another agent"},
		Repository:         "phase6-pilot", WorkspaceID: "phase6-pilot",
		MaximumStories: 1, MaximumTasks: 8, MaximumHops: 8,
	}, 0o600)
	raw := productCLI(t, tekroo, configPath, "feature", featurePath)
	var feature organization.FeatureRequest
	if err := json.Unmarshal(raw, &feature); err != nil || feature.Status != organization.FeatureSubmitted {
		t.Fatalf("feature submission = %#v err=%v raw=%s", feature, err, raw)
	}
	feature = waitForPhase6Feature(t, tekroo, configPath, feature.ID, func(value organization.FeatureRequest) bool {
		return value.Status == organization.FeatureAwaitingAcceptance || value.Status == organization.FeatureClarificationRequired || value.Status == organization.FeatureCancelled
	})
	if feature.Status != organization.FeatureAwaitingAcceptance || feature.Plan == nil || feature.Acceptance == nil || feature.Acceptance.AcceptedBy != nil {
		t.Fatalf("feature pre-acceptance state = %#v", feature)
	}
	implementation, implementationWorkspace := requirePhase6Implementation(t, feature, workspaces)
	verification := exec.Command("go", "test", "./...")
	verification.Dir = implementationWorkspace.WorkingDirectory
	verificationOutput, err := verification.CombinedOutput()
	if err != nil {
		t.Fatalf("independent pre-release verification: %v\n%s", err, verificationOutput)
	}
	head := phase6GitValue(t, repository.BarePath, "rev-parse", "--verify", implementationWorkspace.Branch+"^{commit}")
	tree := phase6GitValue(t, repository.BarePath, "rev-parse", "--verify", head+"^{tree}")
	if head == repository.Baseline {
		t.Fatal("implementation branch did not advance beyond the qualified base")
	}
	goVersion, err := exec.Command("go", "version").Output()
	if err != nil {
		t.Fatal(err)
	}
	goMod, err := os.ReadFile(filepath.Join(implementationWorkspace.WorkingDirectory, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	criteria, err := json.Marshal(implementation.AcceptanceCriteria)
	if err != nil {
		t.Fatal(err)
	}
	releasePath := filepath.Join(root, "feature-release.json")
	writeJSON(t, releasePath, organization.FeatureAcceptanceInput{
		ExpectedRevision: feature.Revision,
		Mode:             organization.FeatureAcceptanceCode,
		CodeReleases: []organization.StoryCodeRelease{{
			StoryID: feature.Plan.Stories[0].ID, RepositoryURL: "file://" + repository.BarePath,
			BaseRef: "main", BaseCommit: repository.Baseline, ExpectedQualifiedTree: tree,
			ChangeRef: "refs/heads/" + implementationWorkspace.Branch, HeadCommit: head,
			GitVersion:           phase6GitValue(t, repository.BarePath, "--version"),
			GateDefinitionDigest: phase6Digest(criteria), ToolchainDigest: phase6Digest(goVersion),
			DependencyLockDigest: phase6Digest(goMod), QualificationArtifactHash: phase6Digest(verificationOutput),
		}},
	}, 0o600)
	acceptedRaw := productCLI(t, tekroo, configPath, "feature-release", string(feature.ID), releasePath)
	var accepted organization.FeatureRequest
	if err := json.Unmarshal(acceptedRaw, &accepted); err != nil {
		t.Fatalf("decode accepted feature: %v\n%s", err, acceptedRaw)
	}
	if accepted.Status != organization.FeatureAccepted || accepted.Acceptance == nil || accepted.Acceptance.AcceptedBy == nil || accepted.Acceptance.AcceptedBy.ID != "principal" || len(accepted.Acceptance.ReleasePlanIDs) != 1 {
		t.Fatalf("accepted feature state = %#v", accepted)
	}
	if released := phase6GitValue(t, repository.BarePath, "rev-parse", "--verify", "refs/heads/main^{commit}"); released != head {
		t.Fatalf("released main = %s, want %s", released, head)
	}
	if releasedTree := phase6GitValue(t, repository.BarePath, "rev-parse", "--verify", "refs/heads/main^{tree}"); releasedTree != tree {
		t.Fatalf("released tree = %s, want %s", releasedTree, tree)
	}
	t.Logf("accepted feature=%s implementation_commit=%s implementation_tree=%s release_plan=%s", feature.ID, head, tree, accepted.Acceptance.ReleasePlanIDs[0])

	productCLI(t, tekroo, configPath, "stop")
	waitPhase5Tekrood(t, service, stdout, stderr)
	serviceStopped = true
}

type phase6PilotRepository struct {
	AllowedRoot string
	BarePath    string
	Baseline    string
}

func createPhase6PilotWorkspaces(t *testing.T, root string) ([]ProductionWorkspace, phase6PilotRepository) {
	t.Helper()
	manifestPath := filepath.Join(projectRoot(t), "config", "starter-team", "team.example.json")
	var manifest organization.TeamManifest
	raw, err := os.ReadFile(manifestPath)
	if err != nil || json.Unmarshal(raw, &manifest) != nil {
		t.Fatalf("load starter manifest: %v", err)
	}
	seed := filepath.Join(root, "seed")
	if err := os.MkdirAll(filepath.Join(seed, ".openhands", "hooks"), 0o700); err != nil {
		t.Fatal(err)
	}
	hook, err := os.ReadFile(phase5SMAHookFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seed, ".openhands", "hooks", "sma_context_hook.py"), hook, 0o700); err != nil {
		t.Fatal(err)
	}
	writeText(t, filepath.Join(seed, "go.mod"), "module example.com/phase6/pilot\n\ngo 1.25\n", 0o600)
	writeText(t, filepath.Join(seed, "arithmetic.go"), "package arithmetic\n\nfunc Add(left, right int) int { return left + right }\n", 0o600)
	writeText(t, filepath.Join(seed, "arithmetic_test.go"), "package arithmetic\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if Add(2, 3) != 5 { t.Fatal(\"bad sum\") } }\n", 0o600)
	for _, arguments := range [][]string{{"init", "-b", "main"}, {"config", "user.email", "phase6@tekroo.local"}, {"config", "user.name", "Tekroo Phase 6"}, {"add", "."}, {"commit", "-m", "seed phase 6 pilot"}} {
		command := exec.Command("git", arguments...)
		command.Dir = seed
		if output, commandErr := command.CombinedOutput(); commandErr != nil {
			t.Fatalf("git %v: %v\n%s", arguments, commandErr, output)
		}
	}
	baselineCommand := exec.Command("git", "rev-parse", "HEAD")
	baselineCommand.Dir = seed
	baselineRaw, err := baselineCommand.Output()
	if err != nil {
		t.Fatal(err)
	}
	baseline := strings.TrimSpace(string(baselineRaw))
	allowedRoot := filepath.Join(root, "repositories")
	if err := os.MkdirAll(allowedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	repository := filepath.Join(allowedRoot, "phase6-pilot.git")
	clone := exec.Command("git", "clone", "--bare", seed, repository)
	if output, cloneErr := clone.CombinedOutput(); cloneErr != nil {
		t.Fatalf("create bare release repository: %v\n%s", cloneErr, output)
	}
	result := make([]ProductionWorkspace, 0)
	for _, role := range manifest.Roles {
		for _, workspaceID := range role.WorkspaceIDs {
			directory := filepath.Join(root, "workspaces", workspaceID)
			command := exec.Command("git", "--git-dir", repository, "worktree", "add", "-b", "phase6/"+workspaceID, directory, "main")
			command.Dir = root
			if output, commandErr := command.CombinedOutput(); commandErr != nil {
				t.Fatalf("create worktree %s: %v\n%s", workspaceID, commandErr, output)
			}
			result = append(result, ProductionWorkspace{WorkspaceID: workspaceID, WorktreeID: "phase6-" + workspaceID, WorkingDirectory: directory, Branch: "phase6/" + workspaceID, BaselineSHA: baseline, WritablePaths: []string{"."}})
		}
	}
	return result, phase6PilotRepository{AllowedRoot: allowedRoot, BarePath: repository, Baseline: baseline}
}

func writePhase6PilotConfig(t *testing.T, mongoURI string, workspaces []ProductionWorkspace, gitRoot string, maximumConcurrency uint32) string {
	t.Helper()
	path, config := writeProductionFixture(t)
	directory := filepath.Dir(path)
	writeText(t, filepath.Join(directory, config.Mongo.URIFile), mongoURI+"\n", 0o600)
	provenance, err := fake.ProvenanceBasis()
	if err != nil {
		t.Fatal(err)
	}
	policy := phase6PilotPolicy(t)
	writeJSON(t, filepath.Join(directory, config.AuthorizationPolicyFile), policy, 0o600)
	writeJSON(t, filepath.Join(directory, config.ProvenanceFile), provenance, 0o600)

	manifestPath := filepath.Join(projectRoot(t), "config", "starter-team", "team.example.json")
	manifestRaw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifestHash := sha256.Sum256(manifestRaw)
	var manifest organization.TeamManifest
	if json.Unmarshal(manifestRaw, &manifest) != nil {
		t.Fatal("starter team manifest is invalid")
	}
	profiles := make([]ProductionProfile, 0, len(manifest.Roles))
	for index, role := range manifest.Roles {
		profiles = append(profiles, ProductionProfile{
			ModelProfileDigest:    role.ModelProfileDigest,
			RuntimeIdentityDigest: digestByte(byte('1' + index)),
			ToolPolicyDigest:      digestByte('d'), EffectPolicyDigest: digestByte('e'), MaximumIterations: 24,
			Qualification: kernel.AssignmentQualificationReceipt{
				QualificationID:     kernel.UUIDv7(fmt.Sprintf("00000000-0000-7000-8000-%012d", index+1)),
				QualificationDigest: digestByte(byte('1' + index)), QualificationCorpusDigest: digestByte('f'),
				ModelProfileDigest: role.ModelProfileDigest, DecisionRoute: kernel.RouteBoundedExecution,
				QualifiedRole: role.Role, Status: kernel.QualificationPass, ObservedAt: time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC),
			},
		})
	}
	database := fmt.Sprintf("tekroo_phase6_pilot_%d", time.Now().UnixNano())
	config.Mongo.Database, config.TeamsDatabaseIdentity = database, database
	config.SMADatabaseIdentity = database + "_sma"
	config.DeploymentIdentity = digestByte('6')
	config.OpenHands.BaseURL = phase5OpenHandsURL
	config.OpenHands.SessionAPIKeyFile = phase5SessionKeyFile
	config.OpenHands.RequestTimeout = "20m"
	config.OpenHands.PollInterval = "250ms"
	config.Operator.Address = freeProductSurfaceAddress(t)
	config.Operator.BearerTokenFile = phase5SessionKeyFile
	config.Operator.Principal = kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"}
	config.Operator.OperationTimeout = "30s"
	config.Workspaces = workspaces
	config.Profiles = profiles
	config.Execution.ConsumerID = "teams-phase6-live-pilot"
	config.Execution.OperationTimeout = "20m"
	config.Worker.LeaseDuration = "90s"
	config.Worker.ReconciliationInterval = "250ms"
	config.Worker.MaximumReconciliations = 80
	config.Worker.MaximumConcurrentInvocations = maximumConcurrency
	config.Worker.LeaseOperationTimeout = "5s"
	config.Projection.Interval = "50ms"
	config.Projection.OperationTimeout = "2s"
	config.Organization.ManifestFile = manifestPath
	config.Organization.ManifestDigest = kernel.Digest(hex.EncodeToString(manifestHash[:]))
	config.Organization.Publishers = []ProductionPublisher{{KeyID: "tekroo-phase6-bootstrap", PublicKeyFile: filepath.Join(projectRoot(t), "config", "starter-team", "publisher.pub")}}
	config.Organization.ReconciliationInterval = "100ms"
	config.Git = &ProductionGitConfig{Binary: "git", AllowedRoot: gitRoot, OperationTimeout: "30s"}
	writeJSON(t, path, config, 0o600)
	return path
}

func requirePhase6Implementation(t *testing.T, feature organization.FeatureRequest, workspaces []ProductionWorkspace) (organization.PlannedTask, ProductionWorkspace) {
	t.Helper()
	implementations := make([]organization.PlannedTask, 0, 1)
	for _, task := range feature.Plan.Tasks {
		if task.Purpose == kernel.PurposeImplementation {
			implementations = append(implementations, task)
		}
	}
	if len(implementations) != 1 {
		t.Fatalf("pilot requires one implementation task, got %d: %#v", len(implementations), implementations)
	}
	workspaceID := strings.TrimPrefix(string(implementations[0].Owner), "example::")
	for _, workspace := range workspaces {
		if workspace.WorkspaceID == workspaceID {
			return implementations[0], workspace
		}
	}
	t.Fatalf("implementation owner %s has no configured workspace", implementations[0].Owner)
	return organization.PlannedTask{}, ProductionWorkspace{}
}

func phase6GitValue(t *testing.T, repository string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"--git-dir", repository}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
	return strings.TrimSpace(string(output))
}

func phase6Digest(value []byte) kernel.Digest {
	sum := sha256.Sum256(value)
	return kernel.Digest(hex.EncodeToString(sum[:]))
}

func phase6PilotPolicy(t *testing.T) kernel.AuthorizationPolicy {
	t.Helper()
	policy := integratedPolicy()
	policy.Grants[1].Scope.CommandTypes = append(policy.Grants[1].Scope.CommandTypes, "tekroo.command.release-plan.record-qualification", "tekroo.command.release-plan.request-execution")
	policy.Grants[2].Scope.CommandTypes = append(policy.Grants[2].Scope.CommandTypes, "tekroo.command.release-plan.record-result")
	policy.Grants[2].Scope.TargetKinds = append(policy.Grants[2].Scope.TargetKinds, kernel.AggregateReleasePlan)
	policy.Grants[2].Scope.CommandTypes = append(policy.Grants[2].Scope.CommandTypes, "tekroo.command.completion-review.record-result")
	policy.Grants[2].Scope.TargetKinds = append(policy.Grants[2].Scope.TargetKinds, kernel.AggregateCompletionReview)
	manifestPath := filepath.Join(projectRoot(t), "config", "starter-team", "team.example.json")
	var manifest organization.TeamManifest
	raw, err := os.ReadFile(manifestPath)
	if err != nil || json.Unmarshal(raw, &manifest) != nil {
		t.Fatalf("load starter team for policy: %v", err)
	}
	for _, role := range manifest.Roles {
		for index := uint32(1); index <= role.MaximumInstances; index++ {
			actor := kernel.ActorFQN(fmt.Sprintf("%s::%s-%d", manifest.Team, role.Role, index))
			policy.Grants = append(policy.Grants, kernel.AuthorityGrant{
				GrantDigest: digestBytes([]byte("phase6-pilot-actor\x00" + string(actor))),
				Grantee:     kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: string(actor)},
				Scope: kernel.AuthorityScope{
					CommandTypes: []string{"tekroo.command.task.acquire-ownership", "tekroo.command.task.activate", "tekroo.command.task.request-completion", "tekroo.command.completion-review.record-result"},
					TargetKinds:  []kernel.AggregateKind{kernel.AggregateTask, kernel.AggregateCompletionReview}, CanReadTarget: true,
				},
			})
		}
	}
	return policy
}

func startPhase6SMABridge(t *testing.T, mongoURI string, workspaces []ProductionWorkspace) *exec.Cmd {
	t.Helper()
	if _, err := os.Stat(phase6SMAJar); err != nil {
		t.Fatalf("SMA jar unavailable: %v", err)
	}
	if listener, err := net.DialTimeout("tcp", "127.0.0.1:8130", 100*time.Millisecond); err == nil {
		_ = listener.Close()
		t.Fatal("port 8130 is already occupied; refusing to replace an existing SMA bridge")
	}
	allowlist := make([]string, 0, len(workspaces))
	for _, workspace := range workspaces {
		allowlist = append(allowlist, workspace.WorkingDirectory)
	}
	suffix := fmt.Sprint(time.Now().UnixNano())
	command := exec.Command("/usr/bin/java", "-cp", phase6SMAJar, "ai.tekroo.sma.runtime.SmaRetrievalBridgeMain")
	command.Env = append(os.Environ(),
		"SMA_AGENT_ID=openhands:phase6:pilot",
		"SMA_MONGO_URI="+mongoURI,
		"SMA_MONGO_DATABASE=phase6_sma_"+suffix,
		"SMA_OPENHANDS_API_KEY_FILE="+phase5SessionKeyFile,
		"SMA_OPENHANDS_BASE_URL="+phase5OpenHandsURL,
		"SMA_OPENHANDS_BRIDGE_HOST=127.0.0.1", "SMA_OPENHANDS_BRIDGE_PORT=8130",
		"SMA_OPENHANDS_CAPTURE_ENABLED=false", "SMA_OPENHANDS_RETRIEVAL_ENABLED=true",
		"SMA_OPENHANDS_WORKSPACE_ALLOWLIST="+strings.Join(allowlist, ","),
		"SMA_QDRANT_SEMANTIC_COLLECTION=phase6_semantic_"+suffix,
		"SMA_QDRANT_EPISODIC_COLLECTION=phase6_episodic_"+suffix,
	)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		connection, err := net.DialTimeout("tcp", "127.0.0.1:8130", 250*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return command
		}
		if command.ProcessState != nil && command.ProcessState.Exited() || time.Now().After(deadline) {
			_ = command.Process.Kill()
			_ = command.Wait()
			t.Fatalf("SMA bridge did not start: %v\n%s", err, output.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func stopPhase6Process(t *testing.T, command *exec.Cmd, name string) {
	t.Helper()
	if command == nil || command.Process == nil {
		return
	}
	_ = command.Process.Signal(os.Interrupt)
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		_ = command.Process.Kill()
		_ = <-done
		t.Errorf("%s required forced termination", name)
	}
}

func waitForPhase6Feature(t *testing.T, tekroo, config string, id kernel.UUIDv7, predicate func(organization.FeatureRequest) bool) organization.FeatureRequest {
	t.Helper()
	deadline := time.Now().Add(25 * time.Minute)
	for {
		raw := productCLI(t, tekroo, config, "feature-status", string(id))
		var feature organization.FeatureRequest
		if err := json.Unmarshal(raw, &feature); err != nil {
			t.Fatalf("decode feature: %v\n%s", err, raw)
		}
		if predicate(feature) {
			return feature
		}
		if time.Now().After(deadline) {
			t.Fatalf("feature timeout: %#v", feature)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func projectRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func requirePhase6BridgeResponse(t *testing.T, endpoint string) {
	t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, endpoint, bytes.NewReader([]byte(`{"session_id":"missing","working_dir":"/tmp","prompt":"health"}`)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
}
