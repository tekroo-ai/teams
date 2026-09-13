package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/openhands"
	"github.com/tekroo-ai/teams/adapters/operationalruntime"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestInitializeLocalDeploymentProducesValidatedIsolatedInstallation(t *testing.T) {
	sourceRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	temporary := t.TempDir()
	repository := filepath.Join(temporary, "repository")
	if err := os.Mkdir(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, arguments := range [][]string{{"init", "-b", "main"}, {"config", "user.email", "test@tekroo.local"}, {"config", "user.name", "Tekroo Test"}} {
		localInitGit(t, repository, arguments...)
	}
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("# fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	localInitGit(t, repository, "add", "README.md")
	localInitGit(t, repository, "commit", "-m", "fixture")
	key := filepath.Join(temporary, "openhands-key")
	hook := filepath.Join(temporary, "sma_context_hook.py")
	if err := os.WriteFile(key, []byte("local-session-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/usr/bin/env python3\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(temporary, "deployment")
	qualificationBundle := filepath.Join(temporary, "model-profile-qualifications.json")
	writeLocalQualificationBundle(t, sourceRoot, qualificationBundle)
	result, err := initializeLocalDeployment(context.Background(), localInitOptions{
		Root: root, SourceRoot: sourceRoot, RepositoryRoot: repository,
		OpenHandsKeyFile: key, OpenHandsBaseURL: "http://127.0.0.1:18002",
		SMAHookFile: hook, TekroodPath: "/usr/bin/true",
		MongoURI: "mongodb://127.0.0.1:27017", Database: "teams_test_prod",
		SMADatabase: "sma_test", OperatorAddress: "127.0.0.1:18787", BranchPrefix: "tekroo-test/",
		QualificationBundleFile: qualificationBundle,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Team != "teams" || result.WorkspaceCount != 14 || result.AutomaticStartup || result.ContractIdentity != "tekroo.kernel.contracts/0.11.0" || result.BranchPrefix != "tekroo-test/" || result.OpenHands != "http://127.0.0.1:18002" {
		t.Fatalf("result = %#v", result)
	}
	config, err := operationalruntime.LoadProductionConfig(result.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if config.Mongo.Database != "teams_test_prod" || config.SMADatabaseIdentity != "sma_test" || config.OpenHands.BaseURL != "http://127.0.0.1:18002" || len(config.Workspaces) != 14 || len(config.Profiles) != 8 {
		t.Fatalf("config = %#v", config)
	}
	if len(config.Planning.CandidateGates) != 1 || len(config.Planning.CandidateGates[0].Command) == 0 || !filepath.IsAbs(config.Planning.CandidateGates[0].Command[0]) || filepath.Base(config.Planning.CandidateGates[0].Command[0]) != "go" {
		t.Fatalf("deterministic Go gate is not bound to an absolute toolchain path: %#v", config.Planning.CandidateGates)
	}
	if !result.QualificationBundleDigest.Valid() || result.QualificationBundle == "" {
		t.Fatalf("qualification bundle result = %#v", result)
	}
	for _, profile := range config.Profiles {
		if profile.RoleFQRN != "operator" && (profile.Qualification == nil || profile.QualificationCorpus == nil) {
			t.Fatalf("local profile %s did not bind supplied qualification evidence", profile.RoleFQRN)
		}
		wantEndpoint := localBoundedModelEndpoint
		wantRoute := kernel.RouteBoundedExecution
		wantThinking := false
		wantReasoningEffort := ""
		wantReasoningBudgetTokens := uint32(0)
		wantMaximumOutputTokens := uint32(localBoundedOutputTokens)
		if profile.RoleFQRN == "architect" || profile.RoleFQRN == "security" || profile.RoleFQRN == "senior-coder" {
			wantEndpoint = localComplexModelEndpoint
			wantRoute = kernel.RouteComplexReasoning
			wantThinking = true
			wantReasoningEffort = localComplexReasoningEffort
			wantMaximumOutputTokens = localComplexOutputTokens
			if profile.RoleFQRN == "senior-coder" {
				wantMaximumOutputTokens = localComplexEditingOutputTokens
			}
		}
		if profile.DecisionRoute != wantRoute {
			t.Fatalf("local profile %s route = %s, want %s", profile.RoleFQRN, profile.DecisionRoute, wantRoute)
		}
		digest, digestErr := openhands.ModelProfileDigest(profile.RoleFQRN, profile.RoleBundleDigest, profile.AgentSettings)
		if digestErr != nil || digest != profile.ModelProfileDigest {
			t.Fatalf("local profile %s digest = %s, want %s: %v", profile.RoleFQRN, profile.ModelProfileDigest, digest, digestErr)
		}
		var settings struct {
			SystemPrompt string `json:"system_prompt"`
			Tools        []struct {
				Name string `json:"name"`
			} `json:"tools"`
			LLM struct {
				BaseURL             string `json:"base_url"`
				MaximumOutputTokens uint32 `json:"max_output_tokens"`
				LiteLLMExtraBody    struct {
					EnableMTP             bool   `json:"enable_mtp"`
					ReasoningEffort       string `json:"reasoning_effort"`
					ReasoningBudgetTokens uint32 `json:"reasoning_budget_tokens"`
					ChatTemplateKwargs    struct {
						EnableThinking   bool `json:"enable_thinking"`
						PreserveThinking bool `json:"preserve_thinking"`
					} `json:"chat_template_kwargs"`
				} `json:"litellm_extra_body"`
			} `json:"llm"`
			Condenser struct {
				MaximumEvents uint32 `json:"max_size"`
				LLM           struct {
					MaximumOutputTokens uint32 `json:"max_output_tokens"`
					LiteLLMExtraBody    struct {
						EnableMTP          bool   `json:"enable_mtp"`
						ReasoningEffort    string `json:"reasoning_effort"`
						ChatTemplateKwargs struct {
							EnableThinking   bool `json:"enable_thinking"`
							PreserveThinking bool `json:"preserve_thinking"`
						} `json:"chat_template_kwargs"`
					} `json:"litellm_extra_body"`
				} `json:"llm"`
			} `json:"condenser"`
		}
		if err := json.Unmarshal(profile.AgentSettings, &settings); err != nil {
			t.Fatalf("decode local profile %s: %v", profile.RoleFQRN, err)
		}
		if settings.LLM.BaseURL != wantEndpoint || settings.LLM.MaximumOutputTokens != wantMaximumOutputTokens || settings.LLM.LiteLLMExtraBody.EnableMTP != wantThinking || settings.LLM.LiteLLMExtraBody.ReasoningEffort != wantReasoningEffort || settings.LLM.LiteLLMExtraBody.ReasoningBudgetTokens != wantReasoningBudgetTokens || settings.LLM.LiteLLMExtraBody.ChatTemplateKwargs.EnableThinking != wantThinking || settings.LLM.LiteLLMExtraBody.ChatTemplateKwargs.PreserveThinking != wantThinking {
			t.Fatalf("local profile %s endpoint/thinking = %s/%t, want %s/%t", profile.RoleFQRN, settings.LLM.BaseURL, settings.LLM.LiteLLMExtraBody.ChatTemplateKwargs.EnableThinking, wantEndpoint, wantThinking)
		}
		if settings.Condenser.LLM.MaximumOutputTokens != localCondenserOutputTokens || settings.Condenser.LLM.LiteLLMExtraBody.EnableMTP || settings.Condenser.LLM.LiteLLMExtraBody.ReasoningEffort != "" || settings.Condenser.LLM.LiteLLMExtraBody.ChatTemplateKwargs.EnableThinking || settings.Condenser.LLM.LiteLLMExtraBody.ChatTemplateKwargs.PreserveThinking {
			t.Fatalf("local profile %s condenser is not independently bounded and non-thinking", profile.RoleFQRN)
		}
		wantCondenserMaximumEvents := uint32(localCondenserMaximumEvents)
		if profile.RoleFQRN == "coder" || profile.RoleFQRN == "senior-coder" {
			wantCondenserMaximumEvents = localEditCondenserMaximumEvents
		}
		if settings.Condenser.MaximumEvents != wantCondenserMaximumEvents {
			t.Fatalf("local profile %s condenser maximum events = %d, want %d", profile.RoleFQRN, settings.Condenser.MaximumEvents, wantCondenserMaximumEvents)
		}
		if settings.SystemPrompt == "" {
			t.Fatalf("local profile %s inherited the generic OpenHands system prompt", profile.RoleFQRN)
		}
		wantTools := map[kernel.RoleFQRN][]string{
			"operator": {}, "product-owner": {}, "project-manager": {},
			"architect": {"glob", "repository_search", "repository_view"}, "security": {"terminal", "glob", "repository_search", "repository_view"}, "tester": {"terminal", "glob", "repository_search", "repository_view"},
			"coder": {"terminal", "glob", "repository_search", "file_editor", "task_tracker"}, "senior-coder": {"terminal", "glob", "repository_search", "file_editor", "task_tracker"},
		}[profile.RoleFQRN]
		gotTools := make([]string, len(settings.Tools))
		for index, tool := range settings.Tools {
			gotTools[index] = tool.Name
		}
		if !slices.Equal(gotTools, wantTools) {
			t.Fatalf("local profile %s tools = %#v, want %#v", profile.RoleFQRN, gotTools, wantTools)
		}
	}
	for _, workspace := range config.Workspaces {
		if len(workspace.Branch) <= len("tekroo-test/") || workspace.Branch[:len("tekroo-test/")] != "tekroo-test/" {
			t.Fatalf("workspace %s branch = %q", workspace.WorkspaceID, workspace.Branch)
		}
		if _, err := os.Stat(filepath.Join(workspace.WorkingDirectory, ".openhands", "hooks", "sma_context_hook.py")); err != nil {
			t.Fatalf("workspace %s hook: %v", workspace.WorkspaceID, err)
		}
		status, err := gitOutput(context.Background(), workspace.WorkingDirectory, "status", "--porcelain", "--untracked-files=all")
		if err != nil || status != "" {
			t.Fatalf("workspace %s is dirty after hook injection: status=%q err=%v", workspace.WorkspaceID, status, err)
		}
		note := filepath.Join(workspace.WorkingDirectory, ".openhands", "notes.txt")
		if err := os.WriteFile(note, []byte("must remain visible\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		status, err = gitOutput(context.Background(), workspace.WorkingDirectory, "status", "--porcelain", "--untracked-files=all")
		if err != nil || status != "?? .openhands/notes.txt" {
			t.Fatalf("workspace %s hid an unrelated OpenHands file: status=%q err=%v", workspace.WorkspaceID, status, err)
		}
		if err := os.Remove(note); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLocalProductionPolicyCanResolveSystemWorkBlocks(t *testing.T) {
	policy := localProductionPolicy(organization.TeamManifest{Team: "teams"})
	if policy.Revision != 6 || policy.PolicyDigest != labelDigest("teams-local-production-policy-v6") {
		t.Fatalf("local policy identity = revision %d digest %s", policy.Revision, policy.PolicyDigest)
	}
	for _, grant := range policy.Grants {
		if grant.Grantee == (kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "teams-admission-policy"}) {
			if !slices.Contains(grant.Scope.CommandTypes, "tekroo.command.work.unblock") {
				t.Fatal("admission policy can create work blocks but cannot resolve them")
			}
			if !slices.Contains(grant.Scope.CommandTypes, "tekroo.command.work.reopen") {
				t.Fatal("admission policy cannot reopen a completed task for evidence-bound repair")
			}
			if !slices.Contains(grant.Scope.CommandTypes, "tekroo.command.completion-review.record-result") {
				t.Fatal("admission policy cannot record the timeout required to close an expired completion review")
			}
			if !slices.Contains(grant.Scope.CommandTypes, "tekroo.command.work-budget.amend") {
				t.Fatal("admission policy cannot exclude a recorded host suspension from an active feature deadline")
			}
			return
		}
	}
	t.Fatal("admission-policy grant was not generated")
}

func TestInitializeLocalDeploymentFailsClosed(t *testing.T) {
	base := localInitOptions{
		Root: "/tmp/tekroo-test-root", SourceRoot: "/tmp/source", RepositoryRoot: "/tmp/repository",
		OpenHandsKeyFile: "/tmp/key", SMAHookFile: "/tmp/hook", TekroodPath: "/usr/bin/true",
		QualificationBundleFile: "/tmp/model-profile-qualifications.json",
		OpenHandsBaseURL:        "http://remote.example:8000",
		MongoURI:                "mongodb://remote.example:27017", Database: "teams", SMADatabase: "sma",
		OperatorAddress: "127.0.0.1:8787",
	}
	if err := validateLocalInitOptions(base); err == nil {
		t.Fatal("remote MongoDB URI accepted")
	}
	base.MongoURI = "mongodb://127.0.0.1:27017"
	if err := validateLocalInitOptions(base); err == nil {
		t.Fatal("remote OpenHands URL accepted")
	}
	base.OpenHandsBaseURL = "http://127.0.0.1:8000"
	base.SMADatabase = base.Database
	if err := validateLocalInitOptions(base); err == nil {
		t.Fatal("shared Teams/SMA database identity accepted")
	}
	base.SMADatabase = "sma"
	base.BranchPrefix = "bad prefix/"
	if err := validateLocalInitOptions(base); err == nil {
		t.Fatal("invalid branch prefix accepted")
	}
}

func TestBindLocalProductionQualificationsRejectsMismatchedProfile(t *testing.T) {
	sourceRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	starterRoot := filepath.Join(sourceRoot, "config", "starter-team")
	var manifest organization.TeamManifest
	if err := readStrictJSON(filepath.Join(starterRoot, "team.example.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	profiles, err := localProductionProfiles(starterRoot, &manifest)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "model-profile-qualifications.json")
	writeLocalQualificationBundle(t, sourceRoot, path)
	var bundle localQualificationBundle
	if err := readStrictJSON(path, &bundle); err != nil {
		t.Fatal(err)
	}
	bundle.Qualifications[0].ModelProfileDigest = kernel.Digest("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
	if err := writeJSONFile(path, bundle, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := bindLocalProductionQualifications(path, profiles, time.Now().UTC()); err == nil {
		t.Fatal("qualification bundle for mismatched model profile was accepted")
	}
}

func writeLocalQualificationBundle(t *testing.T, sourceRoot, path string) {
	t.Helper()
	starterRoot := filepath.Join(sourceRoot, "config", "starter-team")
	var manifest organization.TeamManifest
	if err := readStrictJSON(filepath.Join(starterRoot, "team.example.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	profiles, err := localProductionProfiles(starterRoot, &manifest)
	if err != nil {
		t.Fatal(err)
	}
	bundle := localQualificationBundle{SchemaVersion: localQualificationSchema}
	observedAt := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for _, profile := range profiles {
		if profile.RoleFQRN == "operator" {
			continue
		}
		workKinds := append([]kernel.WorkKind(nil), localTestWorkKinds(profile.RoleFQRN)...)
		corpus := application.QualificationCorpusDefinition{
			CorpusID: deterministicUUID("local-init-test-corpus:" + string(profile.RoleFQRN)),
			Revision: 1, DecisionRoute: profile.DecisionRoute, QualifiedRole: string(profile.RoleFQRN),
			WorkKinds: workKinds, ScenarioIDs: []string{"local-init-test-role-corpus"},
			ThresholdDigest: labelDigest("local-init-test-thresholds"), ToolSurfaceDigest: profile.ToolPolicyDigest,
		}
		corpusDigest, err := corpus.Digest()
		if err != nil {
			t.Fatal(err)
		}
		qualification := kernel.ModelProfileQualification{
			QualificationID:           deterministicUUID("local-init-test-qualification:" + string(profile.RoleFQRN)),
			QualificationCorpusDigest: corpusDigest, ModelProfileDigest: profile.ModelProfileDigest,
			DecisionRoute: profile.DecisionRoute, QualifiedRole: string(profile.RoleFQRN),
			QualifiedWorkKinds: append([]kernel.WorkKind(nil), workKinds...), Status: kernel.QualificationPass,
			ObservedAt: observedAt, EvidenceIDs: []kernel.UUIDv7{deterministicUUID("local-init-test-evidence:" + string(profile.RoleFQRN))},
		}
		qualification.QualificationDigest, err = application.QualificationDigest(qualification)
		if err != nil {
			t.Fatal(err)
		}
		bundle.Qualifications = append(bundle.Qualifications, localProfileQualificationBinding{
			RoleFQRN: profile.RoleFQRN, ModelProfileDigest: profile.ModelProfileDigest,
			QualificationCorpus: corpus, Qualification: qualification,
		})
	}
	if err := writeJSONFile(path, bundle, 0o600); err != nil {
		t.Fatal(err)
	}
}

func localTestWorkKinds(role kernel.RoleFQRN) []kernel.WorkKind {
	switch role {
	case "product-owner":
		return []kernel.WorkKind{kernel.WorkDesign, kernel.WorkRelease}
	case "project-manager", "architect":
		return []kernel.WorkKind{kernel.WorkDesign}
	case "coder", "senior-coder":
		return []kernel.WorkKind{kernel.WorkInvestigation, kernel.WorkImplementation, kernel.WorkDebugging}
	case "tester":
		return []kernel.WorkKind{kernel.WorkInvestigation, kernel.WorkValidation, kernel.WorkSecurityReview}
	case "security":
		return []kernel.WorkKind{kernel.WorkInvestigation, kernel.WorkSecurityReview}
	default:
		return nil
	}
}

func localInitGit(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
}
