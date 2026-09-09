package openhands

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

var qualifiedSMAHookConfig = json.RawMessage(`{"hooks":{"UserPromptSubmit":[{"matcher":"*","hooks":[{"type":"command","command":"/usr/bin/python3 ./.openhands/hooks/sma_context_hook.py","timeout":1}]}]}}`)
var qualifiedSMAAgentSettings = json.RawMessage(qualifiedAgentSettingsJSON)

func acceptedSemanticMemoryBinding(t testing.TB, hookConfig json.RawMessage) SemanticMemoryBinding {
	t.Helper()
	binding, err := NewAcceptedSemanticMemoryBinding(hookConfig, "teams-authority-test", "sma-memory-test")
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func TestBoundResolversRequireExactImmutableIdentityTuple(t *testing.T) {
	workingDirectory := filepath.Join(t.TempDir(), "worktree")
	workspaces, err := NewBoundWorkspaceResolver([]WorkspaceBinding{{WorkspaceID: "workspace-1", WorktreeID: "worktree-1", WorkingDirectory: workingDirectory}})
	if err != nil {
		t.Fatal(err)
	}
	resolvedWorkspace, err := workspaces.ResolveWorkspace(context.Background(), kernel.TaskOperationalScope{WorkspaceID: "workspace-1", WorktreeID: "worktree-1"})
	if err != nil || resolvedWorkspace.WorkingDirectory != workingDirectory {
		t.Fatalf("workspace=%#v err=%v", resolvedWorkspace, err)
	}
	if _, err := workspaces.ResolveWorkspace(context.Background(), kernel.TaskOperationalScope{WorkspaceID: "workspace-1", WorktreeID: "stale"}); err != ErrProtocol {
		t.Fatalf("stale worktree error = %v", err)
	}

	profile := ExecutionProfile{ModelProfileDigest: digest('a'), RuntimeIdentityDigest: digest('b'), ToolPolicyDigest: digest('c'), EffectPolicyDigest: digest('d'), AgentSettings: qualifiedSMAAgentSettings, HookConfig: qualifiedSMAHookConfig, MaxIterations: 0, AgentDelegationDisabled: true}
	profile.SemanticMemory = acceptedSemanticMemoryBinding(t, profile.HookConfig)
	profiles, err := NewBoundExecutionProfileResolver([]ExecutionProfile{profile})
	if err != nil {
		t.Fatal(err)
	}
	resolvedProfile, err := profiles.ResolveExecutionProfile(context.Background(), profile.ModelProfileDigest, profile.RuntimeIdentityDigest, profile.ToolPolicyDigest, profile.EffectPolicyDigest)
	if err != nil || resolvedProfile.MaxIterations != profile.MaxIterations {
		t.Fatalf("profile=%#v err=%v", resolvedProfile, err)
	}
	resolvedProfile.AgentSettings[0] = 'X'
	again, err := profiles.ResolveExecutionProfile(context.Background(), profile.ModelProfileDigest, profile.RuntimeIdentityDigest, profile.ToolPolicyDigest, profile.EffectPolicyDigest)
	if err != nil || again.AgentSettings[0] != '{' {
		t.Fatalf("resolver state mutated: %#v err=%v", again, err)
	}
	if _, err := profiles.ResolveExecutionProfile(context.Background(), digest('e'), profile.RuntimeIdentityDigest, profile.ToolPolicyDigest, profile.EffectPolicyDigest); err != ErrProtocol {
		t.Fatalf("stale model error = %v", err)
	}
	profile.AgentSettings = json.RawMessage(`{"tools":[{"name":"TaskToolSet"}]}`)
	if _, err := NewBoundExecutionProfileResolver([]ExecutionProfile{profile}); err != ErrInvalidConfiguration {
		t.Fatalf("delegating profile error = %v", err)
	}
}

func TestBoundExecutionProfileBindsExactRoleBundleAndAgentSettings(t *testing.T) {
	role := kernel.RoleFQRN("architect")
	bundle := digest('9')
	settings, err := NewOpenAICompatibleAgentSettings(AgentSettingsConfig{
		Model: "openai/local-architect", ModelCanonicalName: "openai/gpt-4o", BaseURL: "http://127.0.0.1:8800/v1", APIKey: "fixture",
		Tools:          []string{"terminal", "file_editor"},
		EnableThinking: true, CondenserEnableThinking: false, ReasoningEffort: "medium", MaximumOutputTokens: 32768, CondenserOutputTokens: 4096, TimeoutSeconds: 1200, CondenserMaximumEvents: 80, CondenserMaximumTokens: 96000,
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded conversationAgentSettings
	if err := json.Unmarshal(settings, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.LLM.LiteLLMExtraBody.ReasoningEffort != "medium" || decoded.LLM.LiteLLMExtraBody.ReasoningBudgetTokens != 0 || decoded.LLM.LiteLLMExtraBody.ChatTemplateKwargs.EnableThinking == nil || !*decoded.LLM.LiteLLMExtraBody.ChatTemplateKwargs.EnableThinking || decoded.LLM.LiteLLMExtraBody.ChatTemplateKwargs.PreserveThinking == nil || !*decoded.LLM.LiteLLMExtraBody.ChatTemplateKwargs.PreserveThinking {
		t.Fatalf("thinking settings were not bound to the model profile: %#v", decoded.LLM.LiteLLMExtraBody)
	}
	model, err := ModelProfileDigest(role, bundle, settings)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := NewBoundExecutionProfile(model, role, bundle, digest('1'), digest('2'), digest('3'), settings, 0, "teams", "sma")
	if err != nil || !profile.valid() {
		t.Fatalf("bound profile error = %v, profile = %#v", err, profile)
	}
	brief := application.ExecutionBrief{RoleGrounding: application.RoleExecutionGrounding{RoleFQRN: role, BundleDigest: bundle}}
	if !profile.validFor(brief) {
		t.Fatal("exact role bundle did not match bound profile")
	}
	brief.RoleGrounding.BundleDigest = digest('8')
	if profile.validFor(brief) {
		t.Fatal("different role bundle matched bound profile")
	}

	tampered := profile
	tampered.AgentSettings = append(json.RawMessage(nil), profile.AgentSettings...)
	tampered.AgentSettings = bytes.Replace(tampered.AgentSettings, []byte("local-architect"), []byte("other-architect"), 1)
	if tampered.valid() {
		t.Fatal("agent settings changed without changing the profile digest")
	}
}

func TestAgentSettingsBindExactToolsDerivedFromRolePermissions(t *testing.T) {
	tests := []struct {
		name        string
		permissions []string
		want        []string
	}{
		{name: "organizational-role", permissions: []string{"feature.refine", "story.propose"}, want: []string{}},
		{name: "read-only-design-role", permissions: []string{"repository.read", "task.create"}, want: []string{"glob", "repository_search", "repository_view"}},
		{name: "read-only-test-role", permissions: []string{"repository.read", "test.execute"}, want: []string{"terminal", "glob", "repository_search", "repository_view"}},
		{name: "implementation-role", permissions: []string{"repository.edit", "task.complete-propose"}, want: []string{"terminal", "glob", "repository_search", "file_editor", "task_tracker"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tools := ExecutionToolsForPermissions(test.permissions)
			if !slices.Equal(tools, test.want) {
				t.Fatalf("tools = %#v, want %#v", tools, test.want)
			}
			settings, err := NewOpenAICompatibleAgentSettings(AgentSettingsConfig{
				Model: "openai/local", ModelCanonicalName: "openai/gpt-4o", BaseURL: "http://127.0.0.1:8802/v1", APIKey: "fixture", Tools: tools,
				MaximumOutputTokens: 8192, CondenserOutputTokens: 4096, TimeoutSeconds: 1200, CondenserMaximumEvents: 80, CondenserMaximumTokens: 96000,
			})
			if err != nil {
				t.Fatal(err)
			}
			var decoded conversationAgentSettings
			if err := json.Unmarshal(settings, &decoded); err != nil {
				t.Fatal(err)
			}
			got := make([]string, len(decoded.Tools))
			for index, tool := range decoded.Tools {
				got[index] = tool.Name
			}
			if !slices.Equal(got, test.want) {
				t.Fatalf("serialized tools = %#v, want %#v", got, test.want)
			}
			if decoded.LLM.LiteLLMExtraBody.ReasoningEffort != "" || decoded.LLM.LiteLLMExtraBody.ChatTemplateKwargs.EnableThinking == nil || *decoded.LLM.LiteLLMExtraBody.ChatTemplateKwargs.EnableThinking || decoded.LLM.LiteLLMExtraBody.ChatTemplateKwargs.PreserveThinking == nil || *decoded.LLM.LiteLLMExtraBody.ChatTemplateKwargs.PreserveThinking {
				t.Fatalf("bounded settings were not explicitly non-thinking: %#v", decoded.LLM.LiteLLMExtraBody)
			}
		})
	}
}

func TestAgentSettingsRejectImplicitOrInvalidTools(t *testing.T) {
	base := AgentSettingsConfig{
		Model: "openai/local", ModelCanonicalName: "openai/gpt-4o", BaseURL: "http://127.0.0.1:8802/v1", APIKey: "fixture",
		MaximumOutputTokens: 8192, CondenserOutputTokens: 4096, TimeoutSeconds: 1200, CondenserMaximumEvents: 80, CondenserMaximumTokens: 96000,
	}
	if _, err := NewOpenAICompatibleAgentSettings(base); err != ErrInvalidConfiguration {
		t.Fatalf("implicit tools error = %v", err)
	}
	base.Tools = []string{"file_editor", "repository_search"}
	if _, err := NewOpenAICompatibleAgentSettings(base); err != ErrInvalidConfiguration {
		t.Fatalf("non-canonical tools error = %v", err)
	}
	base.Tools = []string{"browser_tool_set"}
	if _, err := NewOpenAICompatibleAgentSettings(base); err != ErrInvalidConfiguration {
		t.Fatalf("unapproved tools error = %v", err)
	}
}

func TestAgentSettingsRejectInvalidThinkingEffort(t *testing.T) {
	base := AgentSettingsConfig{
		Model: "openai/local", ModelCanonicalName: "openai/gpt-4o", BaseURL: "http://127.0.0.1:8800/v1", APIKey: "fixture",
		Tools: []string{"repository_search"}, EnableThinking: true,
		MaximumOutputTokens: 32768, CondenserOutputTokens: 4096, TimeoutSeconds: 1200, CondenserMaximumEvents: 80, CondenserMaximumTokens: 96000,
	}
	if _, err := NewOpenAICompatibleAgentSettings(base); err != ErrInvalidConfiguration {
		t.Fatalf("missing reasoning effort error = %v", err)
	}
	base.ReasoningEffort = "xhigh"
	if _, err := NewOpenAICompatibleAgentSettings(base); err != ErrInvalidConfiguration {
		t.Fatalf("runaway reasoning effort error = %v", err)
	}
	base.ReasoningEffort = "medium"
	base.CondenserEnableThinking = true
	if _, err := NewOpenAICompatibleAgentSettings(base); err != ErrInvalidConfiguration {
		t.Fatalf("unbounded condenser thinking error = %v", err)
	}
	base.CondenserEnableThinking = false
	if _, err := NewOpenAICompatibleAgentSettings(base); err != nil {
		t.Fatalf("controlled thinking error = %v", err)
	}
}

func TestSemanticMemoryBindingRejectsTupleHookAndAuthorityDrift(t *testing.T) {
	binding := acceptedSemanticMemoryBinding(t, qualifiedSMAHookConfig)
	base := ExecutionProfile{
		ModelProfileDigest: digest('a'), RuntimeIdentityDigest: digest('b'),
		ToolPolicyDigest: digest('c'), EffectPolicyDigest: digest('d'),
		AgentSettings: qualifiedSMAAgentSettings,
		HookConfig:    qualifiedSMAHookConfig, MaxIterations: 12,
		AgentDelegationDisabled: true, SemanticMemory: binding,
	}
	tests := map[string]func(*ExecutionProfile){
		"package identity":   func(profile *ExecutionProfile) { profile.SemanticMemory.PackageIdentity = digest('9') },
		"execution identity": func(profile *ExecutionProfile) { profile.SemanticMemory.ExecutionIdentity = digest('8') },
		"test receipt":       func(profile *ExecutionProfile) { profile.SemanticMemory.Step15TestReceiptSHA256 = digest('7') },
		"acceptance record":  func(profile *ExecutionProfile) { profile.SemanticMemory.Step15AcceptanceRecordSHA256 = digest('5') },
		"s2 acceptance":      func(profile *ExecutionProfile) { profile.SemanticMemory.S2AcceptanceSHA256 = digest('4') },
		"hook digest":        func(profile *ExecutionProfile) { profile.SemanticMemory.HookConfigurationDigest = digest('6') },
		"hook command": func(profile *ExecutionProfile) {
			profile.HookConfig = json.RawMessage(`{"hooks":{"UserPromptSubmit":[{"matcher":"*","hooks":[{"type":"command","command":"/usr/bin/python3 ./unqualified.py","timeout":1}]}]}}`)
		},
		"hook timeout": func(profile *ExecutionProfile) {
			profile.HookConfig = json.RawMessage(`{"hooks":{"UserPromptSubmit":[{"matcher":"*","hooks":[{"type":"command","command":"/usr/bin/python3 ./.openhands/hooks/sma_context_hook.py","timeout":3}]}]}}`)
		},
		"model": func(profile *ExecutionProfile) {
			profile.AgentSettings = json.RawMessage(`{"llm":{"model":"openai/unqualified","base_url":"http://127.0.0.1:8802/v1","api_mode":"chat","native_tool_calling":true,"max_output_tokens":8192,"timeout":120,"litellm_extra_body":{"chat_template_kwargs":{"enable_thinking":false}}}}`)
		},
		"thinking": func(profile *ExecutionProfile) {
			profile.AgentSettings = json.RawMessage(`{"llm":{"model":"openai/ddalcu--Qwen3.8-27B-MLX-Serve-8bit","base_url":"http://127.0.0.1:8802/v1","api_mode":"chat","native_tool_calling":true,"max_output_tokens":8192,"timeout":120,"litellm_extra_body":{"chat_template_kwargs":{"enable_thinking":true}}}}`)
		},
		"context bound":        func(profile *ExecutionProfile) { profile.SemanticMemory.MaximumContextCharacters = 4097 },
		"authoritative":        func(profile *ExecutionProfile) { profile.SemanticMemory.NonAuthoritative = false },
		"teams memory access":  func(profile *ExecutionProfile) { profile.SemanticMemory.TeamsDirectMemoryDatabaseAccess = true },
		"sma authority access": func(profile *ExecutionProfile) { profile.SemanticMemory.SMADirectAuthorityDatabaseAccess = true },
		"shared database": func(profile *ExecutionProfile) {
			profile.SemanticMemory.SMAMemoryDatabaseIdentity = profile.SemanticMemory.TeamsAuthorityDatabaseIdentity
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			profile := base
			profile.HookConfig = append(json.RawMessage(nil), base.HookConfig...)
			mutate(&profile)
			if _, err := NewBoundExecutionProfileResolver([]ExecutionProfile{profile}); err != ErrInvalidConfiguration {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestAcceptedSemanticMemoryBindingRequiresQualifiedHookAndSeparateStores(t *testing.T) {
	if _, err := NewAcceptedSemanticMemoryBinding(json.RawMessage(`{"hooks":{}}`), "teams", "sma"); err != ErrInvalidConfiguration {
		t.Fatalf("unqualified hook error = %v", err)
	}
	if _, err := NewAcceptedSemanticMemoryBinding(json.RawMessage(`{"hooks":{"UserPromptSubmit":[{"matcher":"*","hooks":[{"type":"command","command":"/usr/bin/python3 ./.openhands/hooks/sma_context_hook.py","timeout":1}]}],"Stop":[{"matcher":"*","hooks":[]}]}}`), "teams", "sma"); err != ErrInvalidConfiguration {
		t.Fatalf("additional hook error = %v", err)
	}
	if _, err := NewAcceptedSemanticMemoryBinding(qualifiedSMAHookConfig, "shared", "shared"); err != ErrInvalidConfiguration {
		t.Fatalf("shared store error = %v", err)
	}
}

func TestQualifiedAgentSettingsUseOperationalRequestTimeout(t *testing.T) {
	if !qualifiedAgentSettings(qualifiedSMAAgentSettings) {
		t.Fatal("accepted settings rejected")
	}
	mutatedSystemSuffix := json.RawMessage(strings.Replace(string(qualifiedSMAAgentSettings), qualifiedShellDisciplineSystemSuffix, "combine related terminal commands", 1))
	if qualifiedAgentSettings(mutatedSystemSuffix) {
		t.Fatal("mutated shell-discipline system suffix accepted")
	}
	thinkEnabled := json.RawMessage(strings.Replace(string(qualifiedSMAAgentSettings), `["FinishTool"]`, `["FinishTool","ThinkTool"]`, 1))
	if qualifiedAgentSettings(thinkEnabled) {
		t.Fatal("no-op think tool accepted")
	}
	missingDefaultTools := json.RawMessage(strings.Replace(string(qualifiedSMAAgentSettings), `"include_default_tools":["FinishTool"],`, "", 1))
	if qualifiedAgentSettings(missingDefaultTools) {
		t.Fatal("missing exact default-tool restriction accepted")
	}
	shortTimeout := json.RawMessage(strings.Replace(string(qualifiedSMAAgentSettings), `"timeout":1200`, `"timeout":300`, 1))
	if qualifiedAgentSettings(shortTimeout) {
		t.Fatal("five-minute LLM timeout accepted")
	}
	prematureCondensation := json.RawMessage(strings.Replace(string(qualifiedSMAAgentSettings), `"max_tokens":96000`, `"max_tokens":48000`, 1))
	if qualifiedAgentSettings(prematureCondensation) {
		t.Fatal("premature condenser token limit accepted")
	}
	lateCondensation := json.RawMessage(strings.Replace(string(qualifiedSMAAgentSettings), `"max_tokens":96000`, `"max_tokens":120000`, 1))
	if qualifiedAgentSettings(lateCondensation) {
		t.Fatal("memory-unsafe condenser token limit accepted")
	}
	var settings map[string]any
	if json.Unmarshal(qualifiedSMAAgentSettings, &settings) != nil {
		t.Fatal("decode accepted settings")
	}
	delete(settings, "agent_context")
	missingSystemSuffix, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if qualifiedAgentSettings(missingSystemSuffix) {
		t.Fatal("profile without the shell-discipline system suffix accepted")
	}
	if json.Unmarshal(qualifiedSMAAgentSettings, &settings) != nil {
		t.Fatal("decode accepted settings")
	}
	delete(settings, "condenser")
	missingCondenser, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if qualifiedAgentSettings(missingCondenser) {
		t.Fatal("profile without a token-bounded condenser accepted")
	}
}

func TestAcceptedSemanticMemoryBindingMatchesRetainedStep15Evidence(t *testing.T) {
	root := filepath.Join("..", "..")
	acceptanceRecord := readJSONFixture(t, filepath.Join(root, "OUTPUT", "phase-3", "sma-e1-ddalcu-final-scientific-adjudication-v2", "scientific-adjudication.json"))
	if sha256Digest(acceptanceRecord) != AcceptedSMAE1AcceptanceRecordSHA {
		t.Fatalf("acceptance record sha = %s", sha256Digest(acceptanceRecord))
	}
	var accepted struct {
		Status            string        `json:"status"`
		AcceptedClaim     string        `json:"acceptedClaim"`
		Step15Status      string        `json:"step15Status"`
		PackageIdentity   kernel.Digest `json:"packageIdentity"`
		ExecutionIdentity kernel.Digest `json:"executionIdentity"`
		ReceiptSHA256     kernel.Digest `json:"receiptSha256"`
	}
	if json.Unmarshal(acceptanceRecord, &accepted) != nil || accepted.Status != "PASS_ACCEPTED" || accepted.AcceptedClaim != semanticMemoryAcceptedClaim || accepted.Step15Status != semanticMemoryStep15Status || accepted.PackageIdentity != AcceptedSMAE1PackageIdentity || accepted.ExecutionIdentity != AcceptedSMAE1ExecutionIdentity || accepted.ReceiptSHA256 != AcceptedSMAE1TestReceiptSHA {
		t.Fatalf("accepted record = %#v", accepted)
	}

	packageIdentity := readJSONFixture(t, filepath.Join(root, "investigations", "sma-q1", "layered", "sma-e1-ddalcu-final-package-identity.json"))
	var packageRecord struct {
		PackageIdentity kernel.Digest `json:"packageIdentity"`
		IdentitySubject struct {
			LaunchDefinitionSHA256 kernel.Digest `json:"launchDefinitionSha256"`
		} `json:"identitySubject"`
	}
	if json.Unmarshal(packageIdentity, &packageRecord) != nil || packageRecord.PackageIdentity != AcceptedSMAE1PackageIdentity || packageRecord.IdentitySubject.LaunchDefinitionSHA256 != AcceptedSMAE1LaunchDefinitionSHA {
		t.Fatalf("package record = %#v", packageRecord)
	}

	launch := readJSONFixture(t, filepath.Join(root, "investigations", "sma-q1", "layered", "sma-e1-ddalcu-native-tool-repair", "e1_native", "live-launch-definition.json"))
	if sha256Digest(launch) != AcceptedSMAE1LaunchDefinitionSHA {
		t.Fatalf("launch sha = %s", sha256Digest(launch))
	}
	var launchRecord struct {
		ModelProfile struct {
			Model    string `json:"model"`
			APIRoot  string `json:"apiRoot"`
			Thinking bool   `json:"thinking"`
		} `json:"modelProfile"`
		UpstreamClaims map[string]struct {
			SHA256 kernel.Digest `json:"sha256"`
		} `json:"upstreamClaims"`
	}
	if json.Unmarshal(launch, &launchRecord) != nil || "openai/"+launchRecord.ModelProfile.Model != qualifiedModelID || launchRecord.ModelProfile.APIRoot != qualifiedModelAPIRoot || launchRecord.ModelProfile.Thinking || launchRecord.UpstreamClaims["S1"].SHA256 != AcceptedSMAS1AcceptanceSHA || launchRecord.UpstreamClaims["S2"].SHA256 != AcceptedSMAS2AcceptanceSHA || launchRecord.UpstreamClaims["M1"].SHA256 != AcceptedSMAM1AcceptanceSHA {
		t.Fatalf("launch record = %#v", launchRecord)
	}
}

func readJSONFixture(t testing.TB, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil || !json.Valid(content) {
		t.Fatalf("read %s: %v", path, err)
	}
	return content
}

func sha256Digest(content []byte) kernel.Digest {
	digest := sha256.Sum256(content)
	return kernel.Digest(hex.EncodeToString(digest[:]))
}
