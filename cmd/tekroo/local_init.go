package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/tekroo-ai/teams/adapters/openhands"
	"github.com/tekroo-ai/teams/adapters/operationalruntime"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

const (
	localModelIdentity        = "ddalcu--Qwen3.8-Flash-Next-MLX-Serve-mixed-4-8bit"
	localBoundedModelEndpoint = "http://127.0.0.1:8800/v1"
	localComplexModelEndpoint = "http://127.0.0.1:8800/v1"
	localOpenHandsEndpoint    = "http://127.0.0.1:8000"
	localDeploymentTeam       = "teams"
	localDeploymentVersion    = "1.0.0"
	localBootstrapPublisher   = "tekroo-phase6-bootstrap"
	localGroundingPublisher   = "tekroo-role-grounding-20260901"
	localBoundedOutputTokens  = 8192
	localComplexOutputTokens  = 32768
	// Editing profiles receive a smaller per-decision ceiling than read-only
	// complex reasoning profiles. Run 41 showed Qwen 3.8 repeating a completed
	// implementation plan until the 32K ceiling without emitting an action.
	// This bounds one response, not the actor lifetime or tool iterations, and
	// is selected from repository.edit authority rather than a named role.
	localComplexEditingOutputTokens = 16384
	localComplexReasoningEffort     = "medium"
	localCondenserOutputTokens      = 4096
	localCondenserMaximumEvents     = 80
	localEditCondenserMaximumEvents = 240
	localQualificationSchema        = "tekroo.local-model-profile-qualifications/1.0.0"
)

type localInitOptions struct {
	Root                    string
	SourceRoot              string
	RepositoryRoot          string
	OpenHandsKeyFile        string
	OpenHandsBaseURL        string
	SMAHookFile             string
	TekroodPath             string
	MongoURI                string
	Database                string
	SMADatabase             string
	OperatorAddress         string
	BranchPrefix            string
	QualificationBundleFile string
}

type localInitResult struct {
	Root                      string        `json:"root"`
	ConfigPath                string        `json:"config_path"`
	StateDirectory            string        `json:"state_directory"`
	LaunchAgentPath           string        `json:"launch_agent_path"`
	Database                  string        `json:"database"`
	SMADatabase               string        `json:"sma_database"`
	OperatorAddress           string        `json:"operator_address"`
	BranchPrefix              string        `json:"branch_prefix"`
	OpenHands                 string        `json:"openhands"`
	Model                     string        `json:"model"`
	Team                      string        `json:"team"`
	WorkspaceCount            int           `json:"workspace_count"`
	WorkspaceIDs              []string      `json:"workspace_ids"`
	BaselineCommit            string        `json:"baseline_commit"`
	ContractIdentity          string        `json:"contract_identity"`
	AutomaticStartup          bool          `json:"automatic_startup"`
	QualificationBundle       string        `json:"qualification_bundle"`
	QualificationBundleDigest kernel.Digest `json:"qualification_bundle_digest"`
}

type localQualificationBundle struct {
	SchemaVersion  string                             `json:"schema_version"`
	Qualifications []localProfileQualificationBinding `json:"qualifications"`
}

type localProfileQualificationBinding struct {
	RoleFQRN            kernel.RoleFQRN                           `json:"role_fqrn"`
	ModelProfileDigest  kernel.Digest                             `json:"model_profile_digest"`
	QualificationCorpus application.QualificationCorpusDefinition `json:"qualification_corpus"`
	Qualification       kernel.ModelProfileQualification          `json:"qualification"`
}

func runLocalInit(arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("tekroo init-local", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", "", "absolute deployment root")
	sourceRoot := flags.String("source-root", "", "absolute Teams source root containing contract and starter team")
	repositoryRoot := flags.String("repository", "", "absolute Git repository used by the team")
	openHandsKey := flags.String("openhands-key", "", "absolute OpenHands session API key file")
	openHandsURL := flags.String("openhands-url", localOpenHandsEndpoint, "loopback OpenHands agent-server URL")
	smaHook := flags.String("sma-hook", "", "absolute accepted SMA OpenHands hook")
	tekrood := flags.String("tekrood", "", "absolute installed tekrood binary")
	mongoURI := flags.String("mongo-uri", "mongodb://127.0.0.1:27017", "loopback MongoDB URI")
	database := flags.String("database", "tekroo_teams_v4_prod", "fresh Teams database identity")
	smaDatabase := flags.String("sma-database", "sma", "physically separate SMA database identity")
	operatorAddress := flags.String("operator-address", "127.0.0.1:8787", "loopback operator listener")
	branchPrefix := flags.String("branch-prefix", "tekroo/", "Git branch prefix for role worktrees")
	qualificationBundle := flags.String("qualification-bundle", "", "absolute accepted model-profile qualification bundle")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return usageError()
	}
	result, err := initializeLocalDeployment(context.Background(), localInitOptions{
		Root: *root, SourceRoot: *sourceRoot, RepositoryRoot: *repositoryRoot,
		OpenHandsKeyFile: *openHandsKey, OpenHandsBaseURL: *openHandsURL,
		SMAHookFile: *smaHook, TekroodPath: *tekrood,
		MongoURI: *mongoURI, Database: *database, SMADatabase: *smaDatabase,
		OperatorAddress:         *operatorAddress,
		BranchPrefix:            *branchPrefix,
		QualificationBundleFile: *qualificationBundle,
	})
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%s\n", encoded)
	return err
}

func initializeLocalDeployment(ctx context.Context, options localInitOptions) (result localInitResult, err error) {
	if options.BranchPrefix == "" {
		options.BranchPrefix = "tekroo/"
	}
	if options.OpenHandsBaseURL == "" {
		options.OpenHandsBaseURL = localOpenHandsEndpoint
	}
	if err := validateLocalInitOptions(options); err != nil {
		return result, err
	}
	baseline, err := gitOutput(ctx, options.RepositoryRoot, "rev-parse", "HEAD")
	if err != nil || len(baseline) != 40 {
		return result, fmt.Errorf("resolve repository baseline: %w", err)
	}
	tree, err := gitOutput(ctx, options.RepositoryRoot, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return result, fmt.Errorf("resolve repository tree: %w", err)
	}
	if err := gitCleanTracked(ctx, options.RepositoryRoot); err != nil {
		return result, err
	}
	starterRoot := filepath.Join(options.SourceRoot, "config", "starter-team")
	manifestPath := filepath.Join(starterRoot, "team.example.json")
	var manifest organization.TeamManifest
	if err := readStrictJSON(manifestPath, &manifest); err != nil || manifest.Validate() != nil {
		return result, errors.New("starter team manifest is invalid")
	}
	if err := requireRegularFile(filepath.Join(starterRoot, "publisher.pub"), false); err != nil {
		return result, err
	}
	if err := requireRegularFile(filepath.Join(starterRoot, "role-grounding-publisher.pub"), false); err != nil {
		return result, err
	}
	if err := requireRegularFile(options.OpenHandsKeyFile, true); err != nil {
		return result, fmt.Errorf("OpenHands key: %w", err)
	}
	if err := requireRegularFile(options.SMAHookFile, true); err != nil {
		return result, fmt.Errorf("SMA hook: %w", err)
	}
	if err := requireRegularFile(options.TekroodPath, true); err != nil {
		return result, fmt.Errorf("tekrood: %w", err)
	}
	if info, statErr := os.Stat(options.Root); statErr == nil {
		entries, readErr := os.ReadDir(options.Root)
		if readErr != nil || !info.IsDir() || len(entries) != 0 {
			return result, errors.New("deployment root already exists and is not empty")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return result, statErr
	}
	createdRoot := false
	createdWorktrees := make([]string, 0)
	createdBranches := make([]string, 0)
	defer func() {
		if err == nil {
			return
		}
		for index := len(createdWorktrees) - 1; index >= 0; index-- {
			_ = gitRun(context.Background(), options.RepositoryRoot, "worktree", "remove", "--force", createdWorktrees[index])
		}
		for index := len(createdBranches) - 1; index >= 0; index-- {
			_ = gitRun(context.Background(), options.RepositoryRoot, "branch", "-D", createdBranches[index])
		}
		if createdRoot {
			_ = os.RemoveAll(options.Root)
		}
	}()
	if err = os.MkdirAll(options.Root, 0o700); err != nil {
		return result, err
	}
	createdRoot = true
	configRoot := filepath.Join(options.Root, "config")
	stateRoot := filepath.Join(options.Root, "state")
	evidenceRoot := filepath.Join(options.Root, "data", "evidence")
	worktreeRoot := filepath.Join(options.Root, "workspaces")
	for _, directory := range []string{configRoot, stateRoot, evidenceRoot, worktreeRoot} {
		if err = os.MkdirAll(directory, 0o700); err != nil {
			return result, err
		}
	}
	deploymentTeamRoot := filepath.Join(configRoot, "starter-team")
	if err = copyTree(starterRoot, deploymentTeamRoot); err != nil {
		return result, err
	}
	manifest.Team = localDeploymentTeam
	manifest.Version = localDeploymentVersion
	profiles, err := localProductionProfiles(deploymentTeamRoot, &manifest)
	if err != nil {
		return result, err
	}
	profiles, qualificationBundle, err := bindLocalProductionQualifications(options.QualificationBundleFile, profiles, time.Now().UTC())
	if err != nil {
		return result, err
	}
	qualificationBundlePath := filepath.Join(configRoot, "model-profile-qualifications.json")
	if err = writeJSONFile(qualificationBundlePath, qualificationBundle, 0o600); err != nil {
		return result, err
	}
	qualificationBundleDigest, err := fileDigest(qualificationBundlePath)
	if err != nil {
		return result, err
	}
	teamPath := filepath.Join(deploymentTeamRoot, "team.json")
	if err = writeJSONFile(teamPath, manifest, 0o600); err != nil {
		return result, err
	}
	_ = os.Remove(filepath.Join(deploymentTeamRoot, "team.example.json"))
	manifestDigest, err := fileDigest(teamPath)
	if err != nil {
		return result, err
	}
	if err = ensureGitInfoExclude(ctx, options.RepositoryRoot, "/.openhands/hooks/sma_context_hook.py"); err != nil {
		return result, fmt.Errorf("exclude injected OpenHands hook: %w", err)
	}
	workspaces := make([]operationalruntime.ProductionWorkspace, 0)
	workspaceIDs := make([]string, 0)
	for _, role := range manifest.Roles {
		for _, workspaceID := range role.WorkspaceIDs {
			branch := options.BranchPrefix + workspaceID
			if gitRefExists(ctx, options.RepositoryRoot, "refs/heads/"+branch) {
				return result, fmt.Errorf("deployment branch already exists: %s", branch)
			}
			directory := filepath.Join(worktreeRoot, workspaceID)
			if err = gitRun(ctx, options.RepositoryRoot, "worktree", "add", "-b", branch, directory, baseline); err != nil {
				return result, fmt.Errorf("create %s worktree: %w", workspaceID, err)
			}
			createdWorktrees = append(createdWorktrees, directory)
			createdBranches = append(createdBranches, branch)
			hookTarget := filepath.Join(directory, ".openhands", "hooks", "sma_context_hook.py")
			if err = copyFile(options.SMAHookFile, hookTarget, 0o700); err != nil {
				return result, err
			}
			workspaces = append(workspaces, operationalruntime.ProductionWorkspace{
				WorkspaceID: workspaceID, WorktreeID: "teams-" + workspaceID,
				WorkingDirectory: directory, Branch: branch, BaselineSHA: baseline,
				WritablePaths: []string{"."},
			})
			workspaceIDs = append(workspaceIDs, workspaceID)
		}
	}
	policy := localProductionPolicy(manifest)
	policyPath := filepath.Join(configRoot, "authorization-policy.json")
	if err = writeJSONFile(policyPath, policy, 0o600); err != nil {
		return result, err
	}
	operatorToken, err := randomHex(32)
	if err != nil {
		return result, err
	}
	operatorTokenPath := filepath.Join(configRoot, "operator-token")
	if err = os.WriteFile(operatorTokenPath, []byte(operatorToken+"\n"), 0o600); err != nil {
		return result, err
	}
	mongoPath := filepath.Join(configRoot, "mongo-uri")
	if err = os.WriteFile(mongoPath, []byte(options.MongoURI+"\n"), 0o600); err != nil {
		return result, err
	}
	deploymentIdentity, err := randomHex(32)
	if err != nil {
		return result, err
	}
	goBinary, err := exec.LookPath("go")
	if err != nil {
		return result, fmt.Errorf("resolve Go toolchain for deterministic gate: %w", err)
	}
	goBinary, err = filepath.Abs(goBinary)
	if err != nil {
		return result, fmt.Errorf("resolve absolute Go toolchain path: %w", err)
	}
	configPath := filepath.Join(configRoot, "tekrood.json")
	config := operationalruntime.ProductionConfig{
		ContractRoot:          options.SourceRoot,
		Mongo:                 operationalruntime.ProductionMongoConfig{URIFile: mongoPath, Database: options.Database, BacklogLimit: 10000, DeliveryPolicyRevision: 1},
		OpenHands:             operationalruntime.ProductionOpenHandsConfig{BaseURL: options.OpenHandsBaseURL, SessionAPIKeyFile: options.OpenHandsKeyFile, RequestTimeout: "20m", PollInterval: "250ms", MaximumPages: 64, MaximumEvidenceBytes: 16 << 20},
		Operator:              operationalruntime.ProductionOperatorConfig{Address: options.OperatorAddress, BearerTokenFile: operatorTokenPath, Principal: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"}, OperationTimeout: "30s", MaximumBodyBytes: 1 << 20},
		TeamsDatabaseIdentity: options.Database, SMADatabaseIdentity: options.SMADatabase, DeploymentIdentity: kernel.Digest(deploymentIdentity),
		AuthorizationPolicyFile: policyPath, ProvenanceFile: filepath.Join(configRoot, "provenance.json"), EvidenceRoot: evidenceRoot,
		ServiceAuthority: kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "teams-operational-runtime"}, ExpiryAuthority: kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "teams-admission-policy"},
		Workspaces: workspaces, Profiles: profiles,
		Execution:    operationalruntime.ProductionExecution{ConsumerID: "tekrood-local-production", OperationTimeout: "20m", MaximumBriefBytes: 1 << 20, PolicyRevision: 6},
		Evidence:     operationalruntime.ProductionEvidence{PolicyRevision: 6, ProducingVersion: "teams-v4-phase8", RetentionPolicy: "local-production"},
		Worker:       operationalruntime.ProductionWorker{LeaseDuration: "90s", ReconciliationInterval: "250ms", MaximumReconciliations: 160, MaximumConcurrentInvocations: 8, LeaseOperationTimeout: "5s"},
		Projection:   operationalruntime.ProductionProjection{Interval: "100ms", OperationTimeout: "5s"},
		Continuity:   &operationalruntime.ProductionContinuity{HeartbeatInterval: "2s", SuspensionThreshold: "10s"},
		Organization: operationalruntime.ProductionOrganization{ManifestFile: teamPath, ManifestDigest: manifestDigest, Publishers: []operationalruntime.ProductionPublisher{{KeyID: localBootstrapPublisher, PublicKeyFile: filepath.Join(deploymentTeamRoot, "publisher.pub")}, {KeyID: localGroundingPublisher, PublicKeyFile: filepath.Join(deploymentTeamRoot, "role-grounding-publisher.pub")}}, ReconciliationInterval: "1s", MaximumRestarts: 3, MaximumDeliveryAttempts: 3},
		Planning:     operationalruntime.ProductionPlanning{PolicyRevision: 3, ClassificationPolicyDigest: labelDigest("classification-policy-v3"), PromotionPolicyDigest: labelDigest("promotion-policy-v3"), VerificationTopologyDigest: labelDigest("verification-topology-v3"), SelectionPolicyDigest: labelDigest("selection-policy-v3"), BudgetPolicyDigest: labelDigest("budget-policy-v3"), RequiredGateIDs: []string{"go-test"}, CandidateGates: []operationalruntime.ProductionCandidateGate{{GateID: "go-test", Command: []string{goBinary, "test", "./..."}, Timeout: "20m"}}, Deadline: "8h"},
		Git:          &operationalruntime.ProductionGitConfig{Binary: "git", AllowedRoot: filepath.Dir(options.RepositoryRoot), OperationTimeout: "2m"},
	}
	provenance, err := localProductionProvenance(options, config, policy, manifest, baseline, tree, qualificationBundleDigest)
	if err != nil {
		return result, err
	}
	if err = writeJSONFile(config.ProvenanceFile, provenance, 0o600); err != nil {
		return result, err
	}
	if err = writeJSONFile(configPath, config, 0o600); err != nil {
		return result, err
	}
	if _, err = operationalruntime.LoadProductionConfig(configPath); err != nil {
		return result, fmt.Errorf("validate generated configuration: %w", err)
	}
	launchAgentPath := filepath.Join(configRoot, "com.tekroo.tekrood.plist")
	if err = writeLaunchAgent(launchAgentPath, options.TekroodPath, configPath, stateRoot); err != nil {
		return result, err
	}
	sort.Strings(workspaceIDs)
	return localInitResult{
		Root: options.Root, ConfigPath: configPath, StateDirectory: stateRoot,
		LaunchAgentPath: launchAgentPath, Database: options.Database, SMADatabase: options.SMADatabase,
		OperatorAddress: options.OperatorAddress, OpenHands: options.OpenHandsBaseURL,
		BranchPrefix: options.BranchPrefix,
		Model:        localModelIdentity, Team: localDeploymentTeam, WorkspaceCount: len(workspaces),
		WorkspaceIDs: workspaceIDs, BaselineCommit: baseline, ContractIdentity: kernel.ContractIdentity,
		AutomaticStartup:    false,
		QualificationBundle: qualificationBundlePath, QualificationBundleDigest: qualificationBundleDigest,
	}, nil
}

func validateLocalInitOptions(options localInitOptions) error {
	for name, value := range map[string]string{"root": options.Root, "source root": options.SourceRoot, "repository": options.RepositoryRoot, "OpenHands key": options.OpenHandsKeyFile, "SMA hook": options.SMAHookFile, "tekrood": options.TekroodPath, "qualification bundle": options.QualificationBundleFile} {
		if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value {
			return fmt.Errorf("%s must be a clean absolute path", name)
		}
	}
	if options.Database == "" || options.SMADatabase == "" || options.Database == options.SMADatabase || strings.ContainsAny(options.Database+options.SMADatabase, " /\\") {
		return errors.New("Teams and SMA database identities must be distinct and simple")
	}
	if options.BranchPrefix == "" || !strings.HasSuffix(options.BranchPrefix, "/") || exec.Command("git", "check-ref-format", "refs/heads/"+options.BranchPrefix+"probe").Run() != nil {
		return errors.New("branch prefix must form a valid Git branch namespace ending in slash")
	}
	parsed, err := url.Parse(options.MongoURI)
	if err != nil || parsed.Scheme != "mongodb" || parsed.Hostname() == "" || !isLoopbackHost(parsed.Hostname()) || parsed.User != nil {
		return errors.New("MongoDB URI must be an unauthenticated loopback mongodb endpoint")
	}
	openHandsURL, err := url.Parse(options.OpenHandsBaseURL)
	if err != nil || openHandsURL.Scheme != "http" || openHandsURL.Hostname() == "" || openHandsURL.Port() == "" || !isLoopbackHost(openHandsURL.Hostname()) || openHandsURL.User != nil || openHandsURL.Path != "" || openHandsURL.RawQuery != "" || openHandsURL.Fragment != "" {
		return errors.New("OpenHands URL must be an unauthenticated loopback HTTP endpoint with no path")
	}
	host, port, err := net.SplitHostPort(options.OperatorAddress)
	if err != nil || port == "" || !isLoopbackHost(host) {
		return errors.New("operator address must be an explicit loopback host and port")
	}
	if pathsOverlap(options.Root, options.SourceRoot) || pathsOverlap(options.Root, options.RepositoryRoot) {
		return errors.New("deployment root must be separate from source and repository roots")
	}
	return nil
}

func bindLocalProductionQualifications(path string, profiles []operationalruntime.ProductionProfile, at time.Time) ([]operationalruntime.ProductionProfile, localQualificationBundle, error) {
	if err := requireRegularFile(path, true); err != nil {
		return nil, localQualificationBundle{}, fmt.Errorf("qualification bundle: %w", err)
	}
	var bundle localQualificationBundle
	if err := readStrictJSON(path, &bundle); err != nil {
		return nil, localQualificationBundle{}, fmt.Errorf("qualification bundle is invalid: %w", err)
	}
	if bundle.SchemaVersion != localQualificationSchema || len(bundle.Qualifications) == 0 || len(bundle.Qualifications) > len(profiles) {
		return nil, localQualificationBundle{}, errors.New("qualification bundle has invalid schema or coverage")
	}
	bound := append([]operationalruntime.ProductionProfile(nil), profiles...)
	byRole := make(map[kernel.RoleFQRN]int, len(bound))
	for index, profile := range bound {
		byRole[profile.RoleFQRN] = index
	}
	seenRoles := make(map[kernel.RoleFQRN]struct{}, len(bundle.Qualifications))
	seenModels := make(map[kernel.Digest]struct{}, len(bundle.Qualifications))
	for _, binding := range bundle.Qualifications {
		index, found := byRole[binding.RoleFQRN]
		_, duplicateRole := seenRoles[binding.RoleFQRN]
		_, duplicateModel := seenModels[binding.ModelProfileDigest]
		if !found || duplicateRole || duplicateModel || bound[index].ModelProfileDigest != binding.ModelProfileDigest {
			return nil, localQualificationBundle{}, errors.New("qualification bundle contains an unknown, duplicated, or mismatched profile binding")
		}
		seenRoles[binding.RoleFQRN] = struct{}{}
		seenModels[binding.ModelProfileDigest] = struct{}{}
		corpus := binding.QualificationCorpus.Canonical()
		qualification := binding.Qualification.Clone()
		bound[index].QualificationCorpus = &corpus
		bound[index].Qualification = &qualification
	}
	if err := operationalruntime.ValidateOperationalProfileQualifications(bound, at); err != nil {
		return nil, localQualificationBundle{}, fmt.Errorf("qualification bundle is not operationally eligible: %w", err)
	}
	return bound, bundle, nil
}

func localProductionProfiles(teamRoot string, manifest *organization.TeamManifest) ([]operationalruntime.ProductionProfile, error) {
	if teamRoot == "" || manifest == nil {
		return nil, errors.New("team manifest is required")
	}
	profiles := make([]operationalruntime.ProductionProfile, 0, len(manifest.Roles))
	for index := range manifest.Roles {
		role := &manifest.Roles[index]
		roleFQRN, err := kernel.ParseRoleFQRN(role.Role)
		if err != nil {
			return nil, err
		}
		var bundle organization.RoleBundle
		if err := readStrictJSON(filepath.Join(teamRoot, role.BundlePath), &bundle); err != nil || bundle.Validate() != nil || bundle.Role != role.Role {
			return nil, errors.New("role bundle is invalid")
		}
		bundleDigest, err := bundle.ContentDigest()
		if err != nil || bundleDigest != role.BundleDigest {
			return nil, errors.New("role bundle digest mismatch")
		}
		executionTools := openhands.ExecutionToolsForPermissions(bundle.Permissions)
		endpoint := localBoundedModelEndpoint
		route := kernel.RouteBoundedExecution
		thinking := false
		reasoningEffort := ""
		maximumOutputTokens := uint32(localBoundedOutputTokens)
		condenserMaximumEvents := uint32(localCondenserMaximumEvents)
		if roleFQRN == "coder" || roleFQRN == "senior-coder" {
			condenserMaximumEvents = localEditCondenserMaximumEvents
		}
		if roleFQRN == "architect" || roleFQRN == "security" || roleFQRN == "senior-coder" {
			endpoint = localComplexModelEndpoint
			route = kernel.RouteComplexReasoning
			thinking = true
			reasoningEffort = localComplexReasoningEffort
			maximumOutputTokens = localComplexOutputTokens
			if slices.Contains(bundle.Permissions, "repository.edit") {
				maximumOutputTokens = localComplexEditingOutputTokens
			}
		}
		agentSettings, err := openhands.NewOpenAICompatibleAgentSettings(openhands.AgentSettingsConfig{
			Model: "openai/" + localModelIdentity, ModelCanonicalName: "openai/gpt-4o", BaseURL: endpoint,
			APIKey: "teams-loopback-only", Tools: executionTools, EnableThinking: thinking, CondenserEnableThinking: false, EnableMTP: thinking, CondenserEnableMTP: false,
			ReasoningEffort: reasoningEffort, MaximumOutputTokens: maximumOutputTokens, CondenserOutputTokens: localCondenserOutputTokens, TimeoutSeconds: 1200, CondenserMaximumEvents: condenserMaximumEvents, CondenserMaximumTokens: 96000,
		})
		if err != nil {
			return nil, err
		}
		modelDigest, err := openhands.ModelProfileDigest(roleFQRN, role.BundleDigest, agentSettings)
		if err != nil {
			return nil, err
		}
		role.ModelProfileDigest = modelDigest
		profiles = append(profiles, operationalruntime.ProductionProfile{
			ModelProfileDigest: modelDigest, RoleFQRN: roleFQRN, RoleBundleDigest: role.BundleDigest,
			DecisionRoute:         route,
			RuntimeIdentityDigest: labelDigest("runtime:" + endpoint + ":" + localModelIdentity + ":" + role.Role),
			ToolPolicyDigest:      labelDigest("tool-policy:role-tools:v3:" + strings.Join(executionTools, ",")), EffectPolicyDigest: labelDigest("effect-policy:teams-only:v1"), MaximumIterations: 0,
			AgentSettings: agentSettings,
		})
	}
	return profiles, nil
}

func localProductionPolicy(manifest organization.TeamManifest) kernel.AuthorizationPolicy {
	principal := kernel.AuthorityGrant{Grantee: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"}, Scope: kernel.AuthorityScope{CommandTypes: []string{"tekroo.command.story.create", "tekroo.command.story.begin-planning", "tekroo.command.story.authorize", "tekroo.command.story.activate", "tekroo.command.task.create", "tekroo.command.evidence.register", "tekroo.command.work-budget.amend", "tekroo.command.work-invocation.request-cancellation", "tekroo.command.story.request-completion", "tekroo.command.story.approve-release", "tekroo.command.story.request-acceptance"}, TargetKinds: []kernel.AggregateKind{kernel.AggregateStory, kernel.AggregateTask, kernel.AggregateEvidence, kernel.AggregateWorkBudget, kernel.AggregateWorkInvocation}, CanReadTarget: true}}
	policyGrant := kernel.AuthorityGrant{Grantee: kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "teams-admission-policy"}, Scope: kernel.AuthorityScope{CommandTypes: []string{"tekroo.command.task.bind-work-profile", "tekroo.command.task.mark-ready", "tekroo.command.task.authorize-qualified-assignment", "tekroo.command.work-budget.create", "tekroo.command.work-budget.amend", "tekroo.command.task.bind-work-budget", "tekroo.command.task.bind-operational-scope", "tekroo.command.work-invocation.authorize", "tekroo.command.work-invocation.expire", "tekroo.command.work.block", "tekroo.command.work.unblock", "tekroo.command.work.reopen", "tekroo.command.completion-review.open", "tekroo.command.completion-review.record-result", "tekroo.command.completion-review.finalize", "tekroo.command.release-plan.create", "tekroo.command.release-plan.finalize", "tekroo.command.release-plan.record-qualification", "tekroo.command.release-plan.request-execution"}, TargetKinds: []kernel.AggregateKind{kernel.AggregateTask, kernel.AggregateStory, kernel.AggregateCompletionReview, kernel.AggregateReleasePlan, kernel.AggregateWorkBudget, kernel.AggregateWorkInvocation}, CanReadTarget: true}}
	service := kernel.AuthorityGrant{Grantee: kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "teams-operational-runtime"}, Scope: kernel.AuthorityScope{CommandTypes: []string{"tekroo.command.execution.register", "tekroo.command.execution.replace", "tekroo.command.evidence.register", "tekroo.command.work-invocation.claim", "tekroo.command.work-invocation.record-started", "tekroo.command.work-invocation.record-terminal", "tekroo.command.release-plan.record-result", "tekroo.command.completion-review.record-result"}, TargetKinds: []kernel.AggregateKind{kernel.AggregateExecution, kernel.AggregateEvidence, kernel.AggregateWorkInvocation, kernel.AggregateReleasePlan, kernel.AggregateCompletionReview}, CanReadTarget: true}}
	grants := []kernel.AuthorityGrant{principal, policyGrant, service}
	for _, role := range manifest.Roles {
		for instance := uint32(1); instance <= role.MaximumInstances; instance++ {
			grants = append(grants, kernel.AuthorityGrant{Grantee: kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: fmt.Sprintf("%s::%s-%d", manifest.Team, role.Role, instance)}, Scope: kernel.AuthorityScope{CommandTypes: []string{"tekroo.command.task.acquire-ownership", "tekroo.command.task.activate", "tekroo.command.task.request-completion", "tekroo.command.completion-review.record-result"}, TargetKinds: []kernel.AggregateKind{kernel.AggregateTask, kernel.AggregateCompletionReview}, CanReadTarget: true}})
		}
	}
	for index := range grants {
		grants[index].GrantDigest = labelDigest(fmt.Sprintf("grant:%d:%s:%s", index, grants[index].Grantee.Kind, grants[index].Grantee.ID))
	}
	return kernel.AuthorizationPolicy{PolicyDigest: labelDigest("teams-local-production-policy-v6"), Revision: 6, Grants: grants}
}

func localProductionProvenance(options localInitOptions, config operationalruntime.ProductionConfig, policy kernel.AuthorizationPolicy, manifest organization.TeamManifest, commit, tree string, qualificationBundleDigest kernel.Digest) (kernel.ProvenanceBasis, error) {
	treeDigest := labelDigest("git-tree:" + tree)
	overlay := kernel.OverlayIdentity{SourceTreeDigest: treeDigest, ChangeDigest: labelDigest("tracked-clean")}
	overlayDigest, err := overlay.Digest()
	if err != nil {
		return kernel.ProvenanceBasis{}, err
	}
	artifactDigest, err := fileDigest(options.TekroodPath)
	if err != nil {
		return kernel.ProvenanceBasis{}, err
	}
	goSumDigest, err := fileDigest(filepath.Join(options.SourceRoot, "go.sum"))
	if err != nil {
		return kernel.ProvenanceBasis{}, err
	}
	installerDigest, err := fileDigest(filepath.Join(options.SourceRoot, "scripts", "tekroo-install-local"))
	if err != nil {
		return kernel.ProvenanceBasis{}, err
	}
	toolchain, err := exec.Command("go", "version").Output()
	if err != nil {
		return kernel.ProvenanceBasis{}, err
	}
	catalogueDigest, err := fileDigest(filepath.Join(options.SourceRoot, operationalruntime.ContractPackagePath, "catalogue", "kernel-catalogue.json"))
	if err != nil {
		return kernel.ProvenanceBasis{}, err
	}
	grantDigests := make([]kernel.Digest, len(policy.Grants))
	for index := range policy.Grants {
		grantDigests[index] = policy.Grants[index].GrantDigest
	}
	roleDigests := []kernel.Digest{config.Organization.ManifestDigest}
	for _, role := range manifest.Roles {
		roleDigests = append(roleDigests, role.BundleDigest)
	}
	return kernel.ProvenanceBasis{
		CatalogueDigest: catalogueDigest, PolicyDigest: policy.PolicyDigest, PolicyRevision: policy.Revision, GrantDigests: grantDigests,
		Source: kernel.SourceIdentity{Repository: options.RepositoryRoot, Commit: commit, TreeDigest: treeDigest, Scope: "."}, Overlay: overlay,
		Build:   kernel.BuildIdentity{SourceTreeDigest: treeDigest, OverlayDigest: overlayDigest, DependencyLockDigest: goSumDigest, ToolchainDigest: digestBytes(toolchain), BuildDefinitionDigest: installerDigest, ArtifactDigest: artifactDigest},
		Runtime: kernel.RuntimeIdentity{BuildArtifactDigest: artifactDigest, ContractManifest: kernel.ContractIdentity, ConfigurationDigest: labelDigest(options.Root + ":" + config.Mongo.Database + ":" + config.OpenHands.BaseURL + ":" + string(qualificationBundleDigest)), EnvironmentDigest: labelDigest(runtime.GOOS + ":" + runtime.GOARCH), RoleLibraryDigests: roleDigests, Capabilities: []string{"teams-kernel", "role-host", "exact-routing", "federation"}, ProviderIdentities: []string{"openhands:" + options.OpenHandsBaseURL, "model-bounded:" + localBoundedModelEndpoint + ":" + localModelIdentity, "model-complex:" + localComplexModelEndpoint + ":" + localModelIdentity, "sma:http://127.0.0.1:8130"}},
	}, nil
}

func writeLaunchAgent(path, tekrood, config, state string) error {
	content := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>com.tekroo.tekrood</string>
<key>ProgramArguments</key><array><string>%s</string><string>-config</string><string>%s</string></array>
<key>RunAtLoad</key><false/><key>KeepAlive</key><false/><key>ProcessType</key><string>Background</string>
<key>StandardOutPath</key><string>%s</string><key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, xmlEscape(tekrood), xmlEscape(config), xmlEscape(filepath.Join(state, "tekrood.log")), xmlEscape(filepath.Join(state, "tekrood.log")))
	return os.WriteFile(path, []byte(content), 0o600)
}

func xmlEscape(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return replacer.Replace(value)
}

func gitCleanTracked(ctx context.Context, repository string) error {
	if err := gitRun(ctx, repository, "diff", "--quiet"); err != nil {
		return errors.New("repository has unstaged tracked changes")
	}
	if err := gitRun(ctx, repository, "diff", "--cached", "--quiet"); err != nil {
		return errors.New("repository has staged changes")
	}
	return nil
}

func gitRefExists(ctx context.Context, repository, ref string) bool {
	return gitRun(ctx, repository, "show-ref", "--verify", "--quiet", ref) == nil
}

func gitRun(ctx context.Context, repository string, arguments ...string) error {
	command := exec.CommandContext(ctx, "git", arguments...)
	command.Dir = repository
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(arguments, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func gitOutput(ctx context.Context, repository string, arguments ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", arguments...)
	command.Dir = repository
	output, err := command.Output()
	return strings.TrimSpace(string(output)), err
}

func ensureGitInfoExclude(ctx context.Context, repository, pattern string) error {
	path, err := gitOutput(ctx, repository, "rev-parse", "--git-path", "info/exclude")
	if err != nil || path == "" {
		return errors.Join(errors.New("Git exclude path is unavailable"), err)
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(repository, path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == pattern {
			return nil
		}
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	prefix := ""
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		prefix = "\n"
	}
	_, err = io.WriteString(file, prefix+pattern+"\n")
	return err
}

func copyTree(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o700)
		}
		return copyFile(path, destination, 0o600)
	})
}

func copyFile(source, target string, mode os.FileMode) error {
	raw, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	return os.WriteFile(target, raw, mode)
}

func readStrictJSON(path string, target any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("JSON file contains trailing content")
	}
	return nil
}

func writeJSONFile(path string, value any, mode os.FileMode) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), mode)
}

func requireRegularFile(path string, nonempty bool) error {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || nonempty && info.Size() == 0 {
		return fmt.Errorf("required file is missing or invalid: %s", path)
	}
	return nil
}

func fileDigest(path string) (kernel.Digest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return digestBytes(raw), nil
}

func digestBytes(value []byte) kernel.Digest {
	sum := sha256.Sum256(value)
	return kernel.Digest(hex.EncodeToString(sum[:]))
}

func labelDigest(value string) kernel.Digest { return digestBytes([]byte(value)) }

func randomHex(bytes int) (string, error) {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

func deterministicUUID(label string) kernel.UUIDv7 {
	sum := sha256.Sum256([]byte(label))
	buffer := append([]byte(nil), sum[:16]...)
	buffer[6] = buffer[6]&0x0f | 0x70
	buffer[8] = buffer[8]&0x3f | 0x80
	value := hex.EncodeToString(buffer)
	return kernel.UUIDv7(value[:8] + "-" + value[8:12] + "-" + value[12:16] + "-" + value[16:20] + "-" + value[20:])
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func pathsOverlap(left, right string) bool {
	separator := string(filepath.Separator)
	return left == right || strings.HasPrefix(left+separator, right+separator) || strings.HasPrefix(right+separator, left+separator)
}
