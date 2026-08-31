package openhands

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tekroo-ai/teams/kernel"
)

var qualifiedSMAHookConfig = json.RawMessage(`{"hooks":{"UserPromptSubmit":[{"matcher":"*","hooks":[{"type":"command","command":"./.openhands/hooks/sma_context_hook.py","timeout":1}]}]}}`)
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

	profile := ExecutionProfile{ModelProfileDigest: digest('a'), RuntimeIdentityDigest: digest('b'), ToolPolicyDigest: digest('c'), EffectPolicyDigest: digest('d'), AgentSettings: qualifiedSMAAgentSettings, HookConfig: qualifiedSMAHookConfig, MaxIterations: 12, AgentDelegationDisabled: true}
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
			profile.HookConfig = json.RawMessage(`{"hooks":{"UserPromptSubmit":[{"matcher":"*","hooks":[{"type":"command","command":"./unqualified.py","timeout":1}]}]}}`)
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
	if _, err := NewAcceptedSemanticMemoryBinding(json.RawMessage(`{"hooks":{"UserPromptSubmit":[{"matcher":"*","hooks":[{"type":"command","command":"./.openhands/hooks/sma_context_hook.py","timeout":1}]}],"Stop":[{"matcher":"*","hooks":[]}]}}`), "teams", "sma"); err != ErrInvalidConfiguration {
		t.Fatalf("additional hook error = %v", err)
	}
	if _, err := NewAcceptedSemanticMemoryBinding(qualifiedSMAHookConfig, "shared", "shared"); err != ErrInvalidConfiguration {
		t.Fatalf("shared store error = %v", err)
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
