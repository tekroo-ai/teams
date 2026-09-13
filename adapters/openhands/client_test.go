package openhands

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

// These sizes construct histories large enough to prove that distinct work is
// not rejected merely because it crosses the former fixed action threshold.
const (
	maximumRepositoryDiscoveryActions = 12
	maximumRetryDiscoveryActions      = 6
)

func TestRecoveryWorkspaceMatchesSuccessorImmutableCandidateOnly(t *testing.T) {
	taskID := kernel.UUIDv7("00000000-0000-7000-8000-000000000741")
	currentID := "00000000-0000-7000-8000-000000000742"
	priorID := "00000000-0000-7000-8000-000000000743"
	root := t.TempDir()
	current := WorkspaceBinding{
		WorkingDirectory: filepath.Join(root, currentID+"-"+string(taskID)),
		Candidate:        &CandidateWorkspaceBinding{CandidateID: currentID},
	}
	if !recoveryWorkspaceMatches(filepath.Join(root, priorID+"-"+string(taskID)), current, taskID) {
		t.Fatal("successor immutable candidate rejected its predecessor sibling")
	}
	if recoveryWorkspaceMatches(filepath.Join(t.TempDir(), priorID+"-"+string(taskID)), current, taskID) {
		t.Fatal("candidate recovery accepted a path outside its managed workspace root")
	}
	if recoveryWorkspaceMatches(filepath.Join(root, priorID+"-00000000-0000-7000-8000-000000000744"), current, taskID) {
		t.Fatal("candidate recovery accepted another task's workspace")
	}
	if recoveryWorkspaceMatches(filepath.Join(root, "malformed-"+string(taskID)), current, taskID) {
		t.Fatal("candidate recovery accepted a malformed predecessor identity")
	}
	current.Candidate = nil
	if recoveryWorkspaceMatches(filepath.Join(root, priorID+"-"+string(taskID)), current, taskID) {
		t.Fatal("ordinary workspace recovery accepted a different path")
	}
	if !recoveryWorkspaceMatches(current.WorkingDirectory, current, taskID) {
		t.Fatal("ordinary same-workspace recovery was rejected")
	}
}

func TestClientUsesExactQualifiedOpenHandsSurfaceAndRetainsAllEventPages(t *testing.T) {
	brief, digest := openHandsTestBrief(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	state := &openHandsServerState{t: t, prompt: string(mustJSON(brief)), workspace: workspace}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Start(context.Background(), brief, digest)
	if err != nil {
		t.Fatal(err)
	}
	if observation.State != application.ExternalSucceeded || string(observation.Output) != "done" || len(observation.Evidence) != 3 {
		t.Fatalf("observation = %#v", observation)
	}
	if observation.Evidence[1].Kind != "ARTIFACT" || !strings.Contains(string(observation.Evidence[1].Content), "evt-tool") {
		t.Fatalf("tool/artifact evidence = %#v", observation.Evidence[1])
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.createCalls != 1 || state.submitCalls != 1 || state.eventPageCalls < 3 {
		t.Fatalf("calls create=%d submit=%d pages=%d", state.createCalls, state.submitCalls, state.eventPageCalls)
	}
	if _, present := state.createPayload["client_tools"]; present {
		t.Fatal("client_tools unexpectedly present")
	}
	if _, present := state.createPayload["agent_definitions"]; present {
		t.Fatal("agent_definitions unexpectedly present")
	}
	var expectedHookConfig any
	if json.Unmarshal(qualifiedSMAHookConfig, &expectedHookConfig) != nil || !reflect.DeepEqual(state.createPayload["hook_config"], expectedHookConfig) {
		t.Fatalf("hook config = %#v", state.createPayload["hook_config"])
	}
	metadata := state.createPayload["observability_metadata"].(map[string]any)
	if metadata["tekroo_request_digest"] != string(digest) {
		t.Fatalf("request digest metadata = %v", metadata)
	}
	tags, ok := state.createPayload["tags"].(map[string]any)
	if !ok || tags["tekrooinvocation"] != string(brief.InvocationID) || tags["tekroorequest"] != string(digest) || tags[pauseAfterCondensationTag] != "true" {
		t.Fatalf("conversation tags = %#v", state.createPayload["tags"])
	}
	if state.createPayload["max_iterations"] != float64(openHandsOperationallyUnboundedIterations) {
		t.Fatalf("max iterations = %#v", state.createPayload["max_iterations"])
	}
	agentSettings, ok := state.createPayload["agent_settings"].(map[string]any)
	if !ok {
		t.Fatalf("agent settings = %#v", state.createPayload["agent_settings"])
	}
	defaultTools, ok := agentSettings["include_default_tools"].([]any)
	if !ok || len(defaultTools) != 1 || defaultTools[0] != "FinishTool" {
		t.Fatalf("default tools = %#v", agentSettings["include_default_tools"])
	}
	agentContext, ok := agentSettings["agent_context"].(map[string]any)
	if !ok || agentContext["system_message_suffix"] != qualifiedShellDisciplineSystemSuffix {
		t.Fatalf("agent context = %#v", agentSettings["agent_context"])
	}
}

func TestOpenHandsIterationLimitPreservesExplicitBoundsAndEncodesUnbounded(t *testing.T) {
	if got := openHandsIterationLimit(37); got != 37 {
		t.Fatalf("explicit limit = %d", got)
	}
	if got := openHandsIterationLimit(0); got != openHandsOperationallyUnboundedIterations {
		t.Fatalf("unbounded transport value = %d", got)
	}
}

func TestClientBindsCandidateIdentityIntoPromptAndRequiresReceiptEvidence(t *testing.T) {
	brief, _ := openHandsTestBrief(t)
	receiptDigest := digest('7')
	evidence := kernel.EvidenceRef{EvidenceID: "00000000-0000-7000-8000-000000000299", SHA256: receiptDigest}
	brief.Evidence = []kernel.EvidenceRef{evidence}
	brief.SemanticContext.Evidence = []kernel.EvidenceRef{evidence}
	encoded := mustJSON(brief)
	hash := sha256.Sum256(encoded)
	requestDigest := kernel.Digest(hex.EncodeToString(hash[:]))
	candidate := &CandidateWorkspaceBinding{CandidateID: "00000000-0000-7000-8000-000000000298", ReceiptSHA256: receiptDigest, RepositoryDigest: digest('6'), BaselineCommit: strings.Repeat("1", 40), CandidateCommit: strings.Repeat("2", 40), CandidateTree: strings.Repeat("3", 40), DiffSHA256: digest('4'), ChangedFilesSHA256: digest('5'), AllowedReference: "refs/heads/candidate/00000000-0000-7000-8000-000000000298", ReadOnly: true}
	hookConfig := append(json.RawMessage(nil), qualifiedSMAHookConfig...)
	client, err := NewClient(Config{
		BaseURL: "http://127.0.0.1:1", SessionAPIKey: "session-key", HTTPClient: &http.Client{Timeout: time.Second},
		Workspaces:   staticWorkspace{binding: WorkspaceBinding{WorkspaceID: brief.Scope.WorkspaceID, WorktreeID: brief.Scope.WorktreeID, WorkingDirectory: filepath.Join(t.TempDir(), "candidate"), Candidate: candidate}},
		Profiles:     staticProfile{profile: ExecutionProfile{ModelProfileDigest: brief.ModelProfileDigest, RuntimeIdentityDigest: brief.RuntimeIdentityDigest, ToolPolicyDigest: brief.ToolPolicyDigest, EffectPolicyDigest: brief.EffectPolicyDigest, AgentSettings: qualifiedSMAAgentSettings, HookConfig: hookConfig, AgentDelegationDisabled: true, SemanticMemory: acceptedSemanticMemoryBinding(t, hookConfig)}},
		PollInterval: time.Millisecond, MaximumPages: 4, MaximumEvidenceBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := client.prepare(context.Background(), brief, requestDigest)
	if err != nil {
		t.Fatal(err)
	}
	var prompt map[string]any
	if json.Unmarshal([]byte(prepared.prompt), &prompt) != nil || prompt["candidate"] == nil || prompt["candidate_result_requirement"] == nil {
		t.Fatalf("candidate prompt = %s", prepared.prompt)
	}
	brief.Evidence = nil
	brief.SemanticContext.Evidence = nil
	encoded = mustJSON(brief)
	hash = sha256.Sum256(encoded)
	if _, err := client.prepare(context.Background(), brief, kernel.Digest(hex.EncodeToString(hash[:]))); err != ErrProtocol {
		t.Fatalf("missing candidate evidence error = %v", err)
	}
}

func TestConversationAgentMatchRejectsInheritedStaleTimeout(t *testing.T) {
	var info conversationInfo
	if err := json.Unmarshal(mustJSON(map[string]any{"agent": testConversationAgent()}), &info); err != nil {
		t.Fatal(err)
	}
	if !conversationAgentMatches(info, qualifiedSMAAgentSettings) {
		t.Fatal("qualified agent settings did not match")
	}
	stale := float64(300)
	info.Agent.LLM.Timeout = &stale
	if conversationAgentMatches(info, qualifiedSMAAgentSettings) {
		t.Fatal("stale inherited timeout matched the qualified profile")
	}
}

func TestConversationAgentMatchRejectsShellDisciplineSystemSuffixDrift(t *testing.T) {
	var info conversationInfo
	if err := json.Unmarshal(mustJSON(map[string]any{"agent": testConversationAgent()}), &info); err != nil {
		t.Fatal(err)
	}
	if !conversationAgentMatches(info, qualifiedSMAAgentSettings) {
		t.Fatal("qualified agent settings did not match")
	}
	info.Agent.AgentContext.SystemMessageSuffix = "combine related terminal commands"
	if conversationAgentMatches(info, qualifiedSMAAgentSettings) {
		t.Fatal("mutated shell-discipline system suffix matched the qualified profile")
	}
}

func TestConversationAgentMatchRejectsTeamsRoleSystemPromptDrift(t *testing.T) {
	settings, err := NewOpenAICompatibleAgentSettings(AgentSettingsConfig{
		Model: "openai/local", ModelCanonicalName: "openai/gpt-4o", BaseURL: "http://127.0.0.1:8802/v1", APIKey: "fixture", Tools: []string{},
		MaximumOutputTokens: 8192, CondenserOutputTokens: 4096, TimeoutSeconds: 1200, CondenserMaximumEvents: 80, CondenserMaximumTokens: 96000,
	})
	if err != nil {
		t.Fatal(err)
	}
	var info conversationInfo
	if err := json.Unmarshal(mustJSON(map[string]any{"agent": json.RawMessage(settings)}), &info); err != nil {
		t.Fatal(err)
	}
	if !conversationAgentMatches(info, settings) {
		t.Fatal("generated Teams role settings did not match")
	}
	info.Agent.SystemPrompt = "generic coding agent"
	if conversationAgentMatches(info, settings) {
		t.Fatal("mutated Teams role system prompt matched")
	}
}

func TestConversationAgentMatchAcceptsOnlyOpenHandsDefaultPrimaryUsageID(t *testing.T) {
	settings, err := NewOpenAICompatibleAgentSettings(AgentSettingsConfig{
		Model: "openai/local", ModelCanonicalName: "openai/gpt-4o", BaseURL: "http://127.0.0.1:8802/v1", APIKey: "fixture", Tools: []string{},
		MaximumOutputTokens: 8192, CondenserOutputTokens: 4096, TimeoutSeconds: 1200, CondenserMaximumEvents: 80, CondenserMaximumTokens: 96000,
	})
	if err != nil {
		t.Fatal(err)
	}
	var info conversationInfo
	if err := json.Unmarshal(mustJSON(map[string]any{"agent": json.RawMessage(settings)}), &info); err != nil {
		t.Fatal(err)
	}
	info.Agent.LLM.UsageID = "default"
	if !conversationAgentMatches(info, settings) {
		t.Fatal("OpenHands materialized default usage ID did not match omitted request default")
	}
	info.Agent.LLM.UsageID = "another-partition"
	if conversationAgentMatches(info, settings) {
		t.Fatal("unexpected usage ID matched omitted request default")
	}
}

func TestConversationAgentMatchRejectsCondenserConfigurationDrift(t *testing.T) {
	settings, err := NewOpenAICompatibleAgentSettings(AgentSettingsConfig{
		Model: "openai/local", ModelCanonicalName: "openai/gpt-4o", BaseURL: "http://127.0.0.1:8800/v1", APIKey: "fixture", Tools: []string{"glob", "repository_search", "repository_view"},
		EnableThinking: true, CondenserEnableThinking: false, ReasoningEffort: "medium", MaximumOutputTokens: 32768, CondenserOutputTokens: 4096, TimeoutSeconds: 1200, CondenserMaximumEvents: 80, CondenserMaximumTokens: 96000,
	})
	if err != nil {
		t.Fatal(err)
	}
	var info conversationInfo
	if err := json.Unmarshal(mustJSON(map[string]any{"agent": json.RawMessage(settings)}), &info); err != nil {
		t.Fatal(err)
	}
	if !conversationAgentMatches(info, settings) {
		t.Fatal("generated Teams role settings did not match")
	}
	thinking := true
	info.Agent.Condenser.LLM.LiteLLMExtraBody.ChatTemplateKwargs.EnableThinking = &thinking
	if conversationAgentMatches(info, settings) {
		t.Fatal("condenser thinking drift matched the configured profile")
	}
}

func TestPrepareDoesNotLetTaskProseOverrideRoleToolAuthority(t *testing.T) {
	brief, _ := openHandsTestBrief(t)
	brief.Task.Description = "This is a reasoning-only task: refine the request without repository access."
	prompt := mustJSON(brief)
	digest := kernel.Digest(testRequestDigest(string(prompt)))
	workspace := filepath.Join(t.TempDir(), "workspace")
	client := newOpenHandsTestClient(t, "http://127.0.0.1:1", workspace, brief)
	agentSettings, err := NewOpenAICompatibleAgentSettings(AgentSettingsConfig{
		Model: qualifiedModelID, ModelCanonicalName: "openai/gpt-4o", BaseURL: qualifiedModelAPIRoot, APIKey: "sma-e1-loopback-only",
		Tools: []string{"terminal", "glob", "repository_search", "file_editor", "task_tracker"}, EnableThinking: false, CondenserEnableThinking: false,
		MaximumOutputTokens: 8192, CondenserOutputTokens: 8192, TimeoutSeconds: 1200, CondenserMaximumEvents: 80, CondenserMaximumTokens: 96000,
	})
	if err != nil {
		t.Fatal(err)
	}
	profile := client.profiles.(staticProfile).profile
	profile.AgentSettings = agentSettings
	client.profiles = staticProfile{profile: profile}

	prepared, err := client.prepare(context.Background(), brief, digest)
	if err != nil {
		t.Fatal(err)
	}
	var decodedSettings map[string]any
	if err := json.Unmarshal(prepared.profile.AgentSettings, &decodedSettings); err != nil {
		t.Fatal(err)
	}
	tools, ok := decodedSettings["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("task prose removed signed role tools: %#v", decodedSettings["tools"])
	}
}

func TestClientAcceptsFinishObservationAsFinalOutput(t *testing.T) {
	brief, digest := openHandsTestBrief(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	state := &openHandsServerState{t: t, prompt: string(mustJSON(brief)), workspace: workspace, finalAsFinish: true}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Start(context.Background(), brief, digest)
	if err != nil {
		t.Fatal(err)
	}
	if observation.State != application.ExternalSucceeded || string(observation.Output) != "done through finish" || len(observation.Evidence) != 3 || observation.Evidence[2].Kind != "MODEL_OUTPUT" {
		t.Fatalf("finish observation = %#v", observation)
	}
}

func TestClientForksPriorConversationForRetryContinuity(t *testing.T) {
	brief, _ := openHandsTestBrief(t)
	priorID := kernel.UUIDv7("00000000-0000-7000-8000-000000000200")
	priorConversationID := string(priorID)
	brief.RetryOfInvocationID = &priorID
	brief.RetryOfConversationID = &priorConversationID
	brief.RetryOrdinal = 1
	brief.AttemptOrdinal = 2
	brief.ExecutionGuidance = append(brief.ExecutionGuidance, "reuse the prior conversation")
	encoded := mustJSON(brief)
	hash := sha256.Sum256(encoded)
	digest := kernel.Digest(hex.EncodeToString(hash[:]))
	workspace := filepath.Join(t.TempDir(), "workspace")
	state := &retryForkServerState{t: t, prompt: string(encoded), workspace: workspace, priorID: string(priorID), currentID: string(brief.InvocationID), requestDigest: string(digest)}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Start(context.Background(), brief, digest)
	if err != nil {
		t.Fatal(err)
	}
	if observation.State != application.ExternalSucceeded || string(observation.Output) != "continued" {
		t.Fatalf("observation = %#v", observation)
	}
	if state.forkCalls != 1 || state.createCalls != 0 || state.submitCalls != 1 {
		t.Fatalf("fork=%d create=%d submit=%d", state.forkCalls, state.createCalls, state.submitCalls)
	}
}

func TestClientCreatesCleanConversationForExplicitRecovery(t *testing.T) {
	brief, _ := openHandsTestBrief(t)
	priorID := kernel.UUIDv7("00000000-0000-7000-8000-000000000200")
	priorConversationID := string(priorID)
	priorProfileID := brief.WorkProfile.ProfileID
	brief.RetryOfInvocationID = &priorID
	brief.RetryOfConversationID = &priorConversationID
	brief.RetryOrdinal = 2
	brief.AttemptOrdinal = 3
	brief.WorkProfile.ProfileID = "00000000-0000-7000-8000-000000000211"
	brief.WorkProfile.ProfileRevision = 2
	brief.WorkProfile.SupersedesProfileID = &priorProfileID
	brief.SemanticContext.WorkProfile = brief.WorkProfile.Binding()
	encoded := mustJSON(brief)
	hash := sha256.Sum256(encoded)
	digest := kernel.Digest(hex.EncodeToString(hash[:]))
	workspace := filepath.Join(t.TempDir(), "workspace")
	state := &retryForkServerState{t: t, prompt: string(encoded), workspace: workspace, priorID: string(priorID), currentID: string(brief.InvocationID), requestDigest: string(digest)}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Start(context.Background(), brief, digest)
	if err != nil {
		t.Fatal(err)
	}
	if observation.State != application.ExternalSucceeded || string(observation.Output) != "continued" {
		t.Fatalf("observation = %#v", observation)
	}
	if state.createCalls != 1 || state.forkCalls != 0 || state.priorGets < 1 || state.submitCalls != 1 || !strings.Contains(state.submittedPrompt, `"recovery_checkpoint"`) {
		t.Fatalf("create=%d fork=%d prior_get=%d submit=%d", state.createCalls, state.forkCalls, state.priorGets, state.submitCalls)
	}
}

func TestExecutionPromptIndexAcceptsCheckpointSerializationEvolutionWithoutWeakeningBindings(t *testing.T) {
	brief, _ := openHandsTestBrief(t)
	priorID := kernel.UUIDv7("00000000-0000-7000-8000-000000000200")
	priorProfileID := brief.WorkProfile.ProfileID
	brief.RetryOfInvocationID = &priorID
	brief.RetryOrdinal = 2
	brief.AttemptOrdinal = 3
	brief.WorkProfile.ProfileID = "00000000-0000-7000-8000-000000000211"
	brief.WorkProfile.ProfileRevision = 2
	brief.WorkProfile.SupersedesProfileID = &priorProfileID
	brief.SemanticContext.WorkProfile = brief.WorkProfile.Binding()
	brief.ResultProtocol = &application.ExecutionResultProtocol{SchemaVersion: "tekroo.validation-result/1.0.0", Marker: application.ValidationResultMarker, Outcomes: []string{"PASS", "FAIL"}, Instruction: "Return an evidence-grounded validation result."}
	receiptDigest := digest('7')
	evidence := kernel.EvidenceRef{EvidenceID: "00000000-0000-7000-8000-000000000299", SHA256: receiptDigest}
	brief.Evidence = []kernel.EvidenceRef{evidence}
	brief.SemanticContext.Evidence = []kernel.EvidenceRef{evidence}
	encoded := mustJSON(brief)
	hash := sha256.Sum256(encoded)
	requestDigest := kernel.Digest(hex.EncodeToString(hash[:]))
	candidate := &CandidateWorkspaceBinding{CandidateID: "00000000-0000-7000-8000-000000000298", ReceiptSHA256: receiptDigest, RepositoryDigest: digest('6'), BaselineCommit: strings.Repeat("1", 40), CandidateCommit: strings.Repeat("2", 40), CandidateTree: strings.Repeat("3", 40), DiffSHA256: digest('4'), ChangedFilesSHA256: digest('5'), AllowedReference: "refs/heads/candidate/00000000-0000-7000-8000-000000000298", ReadOnly: true}
	checkpoint := progressCheckpoint{
		SchemaVersion: "tekroo.teams.execution-progress-checkpoint/1.1.0", InvocationID: brief.InvocationID, PriorInvocationID: brief.RetryOfInvocationID,
		AuthoritativeExecution: checkpointAuthority(brief), Source: "OPENHANDS_EVENT_JOURNAL", SourceEventCount: 17, SourceJournalSHA256: digest('8'),
	}
	makePrompt := func(t *testing.T, checkpoint progressCheckpoint, candidate CandidateWorkspaceBinding, mutate func(map[string]any)) string {
		t.Helper()
		var envelope map[string]any
		if err := json.Unmarshal(encoded, &envelope); err != nil {
			t.Fatal(err)
		}
		envelope["recovery_checkpoint"] = checkpoint
		envelope["candidate"] = candidate
		envelope["candidate_result_requirement"] = candidateResultRequirement{CandidateID: candidate.CandidateID, CandidateReceiptSHA256: candidate.ReceiptSHA256, Instruction: candidateResultRequirementInstruction}
		envelope["result_protocol"].(map[string]any)["instruction"] = candidateResultProtocolInstruction
		if mutate != nil {
			mutate(envelope)
		}
		prompt, err := json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		return string(prompt)
	}
	oldPrompt := makePrompt(t, checkpoint, *candidate, nil)
	checkpoint.SchemaVersion = "tekroo.teams.execution-progress-checkpoint/1.2.0"
	checkpoint.SourceEventCount++
	checkpoint.SourceJournalSHA256 = digest('9')
	newPrompt := makePrompt(t, checkpoint, *candidate, nil)
	prepared := preparedExecution{prompt: newPrompt, requestDigest: requestDigest, workspace: WorkspaceBinding{Candidate: candidate}}
	if index := executionPromptIndex([]rawEvent{{Kind: "MessageEvent", Source: "user", Text: oldPrompt}}, prepared, brief, requestDigest); index != 0 {
		t.Fatalf("schema-compatible recovery prompt index = %d", index)
	}

	tests := map[string]func(map[string]any){
		"mutated brief": func(envelope map[string]any) { envelope["remaining_global_budget"] = float64(999) },
		"mutated candidate": func(envelope map[string]any) {
			mutated := *candidate
			mutated.ReceiptSHA256 = digest('0')
			envelope["candidate"] = mutated
		},
		"mutated requirement": func(envelope map[string]any) {
			envelope["candidate_result_requirement"] = candidateResultRequirement{CandidateID: candidate.CandidateID, CandidateReceiptSHA256: digest('0'), Instruction: candidateResultRequirementInstruction}
		},
		"mutated recovery lineage": func(envelope map[string]any) {
			mutated := checkpoint
			wrongPrior := kernel.UUIDv7("00000000-0000-7000-8000-000000000212")
			mutated.PriorInvocationID = &wrongPrior
			envelope["recovery_checkpoint"] = mutated
		},
		"unknown checkpoint schema": func(envelope map[string]any) {
			mutated := checkpoint
			mutated.SchemaVersion = "tekroo.teams.execution-progress-checkpoint/9.0.0"
			envelope["recovery_checkpoint"] = mutated
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			prompt := makePrompt(t, checkpoint, *candidate, mutate)
			if index := executionPromptIndex([]rawEvent{{Kind: "MessageEvent", Source: "user", Text: prompt}}, prepared, brief, requestDigest); index != -1 {
				t.Fatalf("mutated recovery prompt index = %d", index)
			}
		})
	}
}

func TestClientReconcileStartReturnsAbsentWithoutSubmittingDuplicate(t *testing.T) {
	brief, digest := openHandsTestBrief(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	state := &openHandsServerState{t: t, prompt: string(mustJSON(brief)), workspace: workspace, created: true}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.ReconcileStart(context.Background(), brief, digest)
	if err != nil || observation.State != application.ExternalAbsent {
		t.Fatalf("observation=%#v err=%v", observation, err)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.submitCalls != 0 || state.createCalls != 0 {
		t.Fatalf("submit=%d create=%d", state.submitCalls, state.createCalls)
	}
}

func TestClientRejectsWorkspaceOrProfileDriftBeforeHTTP(t *testing.T) {
	brief, digest := openHandsTestBrief(t)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()
	workspace := filepath.Join(t.TempDir(), "workspace")
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)
	client.workspaces = staticWorkspace{binding: WorkspaceBinding{WorkspaceID: "wrong", WorktreeID: brief.Scope.WorktreeID, WorkingDirectory: workspace}}

	_, err := client.Start(context.Background(), brief, digest)
	if !errorsIs(err, ErrProtocol) || requests != 0 {
		t.Fatalf("err=%v requests=%d", err, requests)
	}
}

func TestClientRejectsAuthoritativeOrIncompleteSemanticContextBeforeHTTP(t *testing.T) {
	base, _ := openHandsTestBrief(t)
	mutations := map[string]func(*application.ExecutionBrief){
		"authoritative": func(brief *application.ExecutionBrief) { brief.SemanticContext.NonAuthoritative = false },
		"fallback":      func(brief *application.ExecutionBrief) { brief.SemanticContext.NoTeamsAuthorityFallback = false },
		"wrong task": func(brief *application.ExecutionBrief) {
			brief.SemanticContext.TaskID = "00000000-0000-7000-8000-000000000299"
		},
		"missing provenance": func(brief *application.ExecutionBrief) { brief.SemanticContext.TaskCreatedSourceDigest = "" },
		"permission removed": func(brief *application.ExecutionBrief) {
			brief.SemanticContext.ForbiddenEffects = brief.SemanticContext.ForbiddenEffects[:len(brief.SemanticContext.ForbiddenEffects)-1]
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			brief := base
			brief.SemanticContext.ForbiddenEffects = append([]string(nil), base.SemanticContext.ForbiddenEffects...)
			mutate(&brief)
			encoded := mustJSON(brief)
			hash := sha256.Sum256(encoded)
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
			defer server.Close()
			workspace := filepath.Join(t.TempDir(), "workspace")
			client := newOpenHandsTestClient(t, server.URL, workspace, brief)
			_, err := client.Start(context.Background(), brief, kernel.Digest(hex.EncodeToString(hash[:])))
			if !errorsIs(err, ErrProtocol) || requests != 0 {
				t.Fatalf("err=%v requests=%d", err, requests)
			}
		})
	}
}

func TestClientRejectsMissingOrMismatchedRoleGroundingBeforeHTTP(t *testing.T) {
	base, _ := openHandsTestBrief(t)
	mutations := map[string]func(*application.ExecutionBrief){
		"wrong actor": func(brief *application.ExecutionBrief) { brief.RoleGrounding.ActorFQN = "teams::coder-2" },
		"wrong fqrn":  func(brief *application.ExecutionBrief) { brief.RoleGrounding.RoleFQRN = "tester" },
		"no bundle":   func(brief *application.ExecutionBrief) { brief.RoleGrounding.BundleDigest = "" },
		"no duties":   func(brief *application.ExecutionBrief) { brief.RoleGrounding.Instructions = "" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			brief := base
			mutate(&brief)
			encoded := mustJSON(brief)
			hash := sha256.Sum256(encoded)
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
			defer server.Close()
			workspace := filepath.Join(t.TempDir(), "workspace")
			client := newOpenHandsTestClient(t, server.URL, workspace, brief)
			_, err := client.Start(context.Background(), brief, kernel.Digest(hex.EncodeToString(hash[:])))
			if !errorsIs(err, ErrProtocol) || requests != 0 {
				t.Fatalf("err=%v requests=%d", err, requests)
			}
		})
	}
}

func TestClientAllowsExtendedDistinctImplementationInspection(t *testing.T) {
	brief, digest := openHandsTestBrief(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	events := []map[string]any{
		event("evt-user", "MessageEvent", "user", string(mustJSON(brief))),
		actionEvent("instructions", "terminal", "cat AGENTS.md"),
		observationEventWithText("instructions-observation", "terminal", false, 0, "# repository instructions"),
	}
	// Retained regression for the observed alias-feature run: these were
	// distinct, relevant inspections and must not be stopped by a fixed count.
	commands := []string{
		"rg --files",
		"rg -l 'ActorFQN|type Actor' organization/",
		"rg -n 'type Actor|ActorFQN|type Team' organization/*.go",
		"rg -n 'type ActorFQN|func ParseActorFQN' kernel/*.go",
		"sed -n '1,80p' kernel/types.go",
		"rg -n 'alias|Alias' organization/federation.go",
		"sed -n '80,200p' organization/manifest.go",
		"rg -n 'type.*Store|interface' organization/*.go",
		"cat organization/memory_store.go",
		"sed -n '1,60p' organization/manifest_test.go",
		"rg -n 'type.*interface' organization/*.go",
		"sed -n '370,420p' organization/feature.go",
		"rg -n 'func sortedUniqueNonempty|func cloneMap' organization/*.go",
		"sed -n '1,80p' organization/message.go",
		"sed -n '150,200p' organization/message.go",
		"sed -n '1,60p' organization/message_memory_store.go",
	}
	for index, command := range commands {
		events = append(events,
			actionEvent(fmt.Sprintf("action-%02d", index), "terminal", command),
			observationEvent(fmt.Sprintf("observation-%02d", index), "terminal", false, 0),
		)
	}
	state := &progressGuardServerState{prompt: string(mustJSON(brief)), workspace: workspace, events: events}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil {
		t.Fatal(err)
	}
	if observation.State != application.ExternalRunning || observation.Retryable {
		t.Fatalf("observation = %#v", observation)
	}
	if state.interruptCalls != 0 {
		t.Fatalf("interrupt calls = %d, want 0", state.interruptCalls)
	}
}

func TestClientAllowsExtendedDistinctFileInspection(t *testing.T) {
	brief, digest := openHandsTestBrief(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	events := []map[string]any{
		event("evt-user", "MessageEvent", "user", string(mustJSON(brief))),
		actionEvent("instructions", "terminal", "cat AGENTS.md"),
		observationEventWithText("instructions-observation", "terminal", false, 0, "# repository instructions"),
		actionEvent("discovery", "terminal", "rg --files"),
		observationEvent("discovery-observation", "terminal", false, 0),
	}
	for index := 0; index < maximumRepositoryDiscoveryActions-2; index++ {
		events = append(events,
			actionEventWithPath(fmt.Sprintf("view-%02d", index), "file_editor", "view", fmt.Sprintf("/workspace/file-%02d.go", index)),
			observationEvent(fmt.Sprintf("view-observation-%02d", index), "file_editor", false, 0),
		)
	}
	state := &progressGuardServerState{prompt: string(mustJSON(brief)), workspace: workspace, events: events}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalRunning || state.interruptCalls != 0 {
		t.Fatalf("full read allowance observation=%#v err=%v interrupts=%d", observation, err, state.interruptCalls)
	}
	events = append(events,
		actionEventWithPath("view-excess", "file_editor", "view", "/workspace/file-final.go"),
		observationEvent("view-excess-observation", "file_editor", false, 0),
	)
	state.events = events
	observation, err = client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalRunning || observation.Retryable || state.interruptCalls != 0 {
		t.Fatalf("extended read observation=%#v err=%v interrupts=%d", observation, err, state.interruptCalls)
	}
}

func TestClientAllowsConcurrentDistinctImplementationReads(t *testing.T) {
	brief, digest := openHandsTestBrief(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	events := []map[string]any{event("evt-user", "MessageEvent", "user", string(mustJSON(brief)))}
	for index := 0; index < maximumRepositoryDiscoveryActions+1; index += 2 {
		batchSize := 2
		if remaining := maximumRepositoryDiscoveryActions + 1 - index; remaining < batchSize {
			batchSize = remaining
		}
		for offset := 0; offset < batchSize; offset++ {
			item := index + offset
			events = append(events, actionEventWithPath(fmt.Sprintf("action-%02d", item), "file_editor", "view", fmt.Sprintf("/workspace/file-%02d.go", item)))
		}
		for offset := 0; offset < batchSize; offset++ {
			item := index + offset
			events = append(events, observationEvent(fmt.Sprintf("observation-%02d", item), "file_editor", false, 0))
		}
	}
	state := &progressGuardServerState{prompt: string(mustJSON(brief)), workspace: workspace, events: events}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalRunning || observation.Retryable || state.interruptCalls != 0 {
		t.Fatalf("observation=%#v err=%v interrupts=%d", observation, err, state.interruptCalls)
	}
}

func TestClientAllowsReadOnlyValidatorTargetedFileInspection(t *testing.T) {
	brief, _ := openHandsTestBrief(t)
	brief.Purpose = kernel.PurposeValidation
	brief.RoleGrounding.Permissions = []string{"repository.read"}
	encoded := mustJSON(brief)
	hash := sha256.Sum256(encoded)
	digest := kernel.Digest(hex.EncodeToString(hash[:]))
	workspace := filepath.Join(t.TempDir(), "workspace")
	events := []map[string]any{event("evt-user", "MessageEvent", "user", string(encoded))}
	for index := 0; index < maximumRepositoryDiscoveryActions+1; index++ {
		events = append(events,
			actionEventWithPath(fmt.Sprintf("view-%02d", index), "file_editor", "view", fmt.Sprintf("/candidate/file-%02d.go", index)),
			observationEvent(fmt.Sprintf("view-observation-%02d", index), "file_editor", false, 0),
		)
	}
	state := &progressGuardServerState{prompt: string(encoded), workspace: workspace, events: events}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalRunning || state.interruptCalls != 0 {
		t.Fatalf("validator inspection observation=%#v err=%v interrupts=%d", observation, err, state.interruptCalls)
	}
}

func TestClientCorrectsExactRepeatedPlannerAction(t *testing.T) {
	brief, _ := openHandsTestBrief(t)
	brief.Purpose = kernel.PurposeReplan
	brief.RoleGrounding.Permissions = []string{"repository.read"}
	encoded := mustJSON(brief)
	hash := sha256.Sum256(encoded)
	digest := kernel.Digest(hex.EncodeToString(hash[:]))
	workspace := filepath.Join(t.TempDir(), "workspace")
	events := []map[string]any{event("evt-user", "MessageEvent", "user", string(encoded))}
	for index := 0; index < maximumRepositoryDiscoveryActions; index++ {
		events = append(events,
			actionEvent(fmt.Sprintf("action-%02d", index), "terminal", "ls adapters"),
			observationEvent(fmt.Sprintf("observation-%02d", index), "terminal", false, 0),
		)
	}
	state := &progressGuardServerState{prompt: string(encoded), workspace: workspace, events: events}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalRunning || state.interruptCalls != 1 || state.correctionCalls != 1 || !strings.HasPrefix(state.correctionText, repositoryProgressCorrectionPrefix+"action-01\n") {
		t.Fatalf("observation=%#v err=%v interrupts=%d", observation, err, state.interruptCalls)
	}
}

func TestClientRetryCorrectsExactActionRepeatedInsideRetry(t *testing.T) {
	brief, _ := openHandsTestBrief(t)
	priorID := kernel.UUIDv7("00000000-0000-7000-8000-000000000200")
	brief.RetryOfInvocationID = &priorID
	brief.RetryOrdinal = 1
	brief.AttemptOrdinal = 2
	encoded := mustJSON(brief)
	hash := sha256.Sum256(encoded)
	digest := kernel.Digest(hex.EncodeToString(hash[:]))
	workspace := filepath.Join(t.TempDir(), "workspace")
	events := []map[string]any{event("prior-user", "MessageEvent", "user", "prior attempt")}
	for index := 0; index < maximumRepositoryDiscoveryActions; index++ {
		events = append(events,
			actionEvent(fmt.Sprintf("prior-action-%02d", index), "terminal", "rg -n Actor organization/*.go"),
			observationEvent(fmt.Sprintf("prior-observation-%02d", index), "terminal", false, 0),
		)
	}
	events = append(events, event("retry-user", "MessageEvent", "user", string(encoded)))
	for index := 0; index < maximumRetryDiscoveryActions; index++ {
		events = append(events,
			actionEvent(fmt.Sprintf("retry-action-%02d", index), "terminal", "sed -n '1,80p' organization/host.go"),
			observationEvent(fmt.Sprintf("retry-observation-%02d", index), "terminal", false, 0),
		)
	}
	state := &progressGuardServerState{prompt: string(encoded), workspace: workspace, events: events}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil {
		t.Fatal(err)
	}
	if observation.State != application.ExternalRunning || observation.Retryable || state.correctionCalls != 1 || !strings.HasPrefix(state.correctionText, repositoryProgressCorrectionPrefix+"retry-action-01\n") {
		t.Fatalf("observation = %#v", observation)
	}
	if state.interruptCalls != 1 {
		t.Fatalf("interrupt calls = %d, want 1", state.interruptCalls)
	}
}

func TestClientRetryAllowsDistinctActionAfterLargePriorHistory(t *testing.T) {
	brief, _ := openHandsTestBrief(t)
	priorID := kernel.UUIDv7("00000000-0000-7000-8000-000000000200")
	brief.RetryOfInvocationID = &priorID
	brief.RetryOrdinal = 1
	brief.AttemptOrdinal = 2
	encoded := mustJSON(brief)
	hash := sha256.Sum256(encoded)
	digest := kernel.Digest(hex.EncodeToString(hash[:]))
	workspace := filepath.Join(t.TempDir(), "workspace")
	events := []map[string]any{event("prior-user", "MessageEvent", "user", "prior attempt")}
	for index := 0; index < maximumRepositoryDiscoveryActions+4; index++ {
		events = append(events,
			actionEvent(fmt.Sprintf("prior-action-%02d", index), "terminal", "ls adapters"),
			observationEvent(fmt.Sprintf("prior-observation-%02d", index), "terminal", false, 0),
		)
	}
	events = append(events, event("retry-user", "MessageEvent", "user", string(encoded)))
	for index := 0; index < maximumRetryDiscoveryActions-1; index++ {
		events = append(events,
			actionEvent(fmt.Sprintf("retry-action-%02d", index), "terminal", fmt.Sprintf("sed -n '%d,%dp' organization/host.go", index*20+1, index*20+20)),
			observationEvent(fmt.Sprintf("retry-observation-%02d", index), "terminal", false, 0),
		)
	}
	state := &progressGuardServerState{prompt: string(encoded), workspace: workspace, events: events}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalRunning || state.interruptCalls != 0 {
		t.Fatalf("allowed continuation observation=%#v err=%v interrupts=%d", observation, err, state.interruptCalls)
	}
	events = append(events,
		actionEvent("retry-action-final", "terminal", "sed -n '1,80p' adapters/operatorhttp/handler.go"),
		observationEvent("retry-observation-final", "terminal", false, 0),
	)
	state.events = events
	observation, err = client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalRunning || state.interruptCalls != 0 {
		t.Fatalf("distinct continuation observation=%#v err=%v interrupts=%d", observation, err, state.interruptCalls)
	}
}

func TestClientExplicitRecoveryCannotStartWithoutPriorCheckpoint(t *testing.T) {
	brief, digest := explicitRecoveryTestBrief(t)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		writer.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, filepath.Join(t.TempDir(), "workspace"), brief)

	_, err := client.Start(context.Background(), brief, digest)
	if !errorsIs(err, ErrProtocol) || requests != 2 {
		t.Fatalf("err=%v requests=%d, want prior/current reads and no recovery start", err, requests)
	}
}

func TestClientExplicitRecoveryAfterPreStartFailureCreatesCleanConversation(t *testing.T) {
	brief, _ := explicitRecoveryTestBrief(t)
	brief.RetryOfConversationID = nil
	encoded := mustJSON(brief)
	hash := sha256.Sum256(encoded)
	digest := kernel.Digest(hex.EncodeToString(hash[:]))
	workspace := filepath.Join(t.TempDir(), "workspace")
	state := &retryForkServerState{t: t, prompt: string(encoded), workspace: workspace, priorID: string(*brief.RetryOfInvocationID), currentID: string(brief.InvocationID), requestDigest: string(digest)}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Start(context.Background(), brief, digest)
	if err != nil || observation.State != application.ExternalSucceeded {
		t.Fatalf("observation=%#v err=%v", observation, err)
	}
	if state.createCalls != 1 || state.forkCalls != 0 || state.priorGets != 0 || state.submitCalls != 1 || strings.Contains(state.submittedPrompt, `"recovery_checkpoint"`) {
		t.Fatalf("create=%d fork=%d prior_get=%d submit=%d prompt=%s", state.createCalls, state.forkCalls, state.priorGets, state.submitCalls, state.submittedPrompt)
	}
}

func TestEquivalentActionHistoryResetsAfterSuccessfulMutation(t *testing.T) {
	exitSuccess := 0
	events := []rawEvent{
		{ID: "first-read", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ActionCommand: "sed -n '1,80p' organization/host.go"},
		{Kind: "ObservationEvent", ToolName: "terminal", ObservationExitCode: &exitSuccess},
		{ID: "edit", Kind: "ActionEvent", Source: "agent", ToolName: "file_editor", ActionCommand: "str_replace", ActionPath: "/workspace/organization/host.go"},
		{Kind: "ObservationEvent", ToolName: "file_editor", ObservationExitCode: &exitSuccess},
		{ID: "read-after-edit", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ActionCommand: "sed -n '1,80p' organization/host.go"},
	}
	if violation, found := repositorySearchLoopViolation(events, -1); found {
		t.Fatalf("unexpected violation after repository mutation: %+v", violation)
	}
}

func TestRepositoryProgressGuardAllowsMixedParallelBatch(t *testing.T) {
	exitSuccess := 0
	events := []rawEvent{
		{ID: "first-host-view", Kind: "ActionEvent", Source: "agent", ToolName: "repository_view", ToolCallID: "call-1", ActionPath: "organization/host.go"},
		{Kind: "ObservationEvent", ToolName: "repository_view", ToolCallID: "call-1", ObservationExitCode: &exitSuccess},
		{ID: "repeated-host-view", Kind: "ActionEvent", Source: "agent", ToolName: "repository_view", ToolCallID: "call-2", ActionPath: "organization/host.go"},
		{ID: "new-mongo-search", Kind: "ActionEvent", Source: "agent", ToolName: "repository_search", ToolCallID: "call-3", ActionPayload: json.RawMessage(`{"kind":"RepositorySearchAction","pattern":"WidgetStore","path":"adapters/mongo"}`), ActionPath: "adapters/mongo"},
		{Kind: "ObservationEvent", ToolName: "repository_view", ToolCallID: "call-2", ObservationExitCode: &exitSuccess},
		{Kind: "ObservationEvent", ToolName: "repository_search", ToolCallID: "call-3", ObservationExitCode: &exitSuccess},
	}

	if violation, found := repositorySearchLoopViolation(events, -1); found {
		t.Fatalf("mixed batch made progress but was rejected: %+v", violation)
	}
}

func TestRepositoryProgressGuardStartsNewWindowAfterCompactionCheckpoint(t *testing.T) {
	exitSuccess := 0
	events := []rawEvent{
		{ID: "pre-checkpoint-host-view", Kind: "ActionEvent", Source: "agent", ToolName: "repository_view", ToolCallID: "call-1", ActionPath: "organization/host.go"},
		{Kind: "ObservationEvent", ToolName: "repository_view", ToolCallID: "call-1", ObservationExitCode: &exitSuccess},
		{ID: "checkpoint", Kind: "MessageEvent", Source: "user", Text: compactionCheckpointPrefix + "1\n{}"},
		{ID: "post-checkpoint-host-view", Kind: "ActionEvent", Source: "agent", ToolName: "repository_view", ToolCallID: "call-2", ActionPath: "organization/host.go"},
		{Kind: "ObservationEvent", ToolName: "repository_view", ToolCallID: "call-2", ObservationExitCode: &exitSuccess},
	}
	if violation, found := repositorySearchLoopViolation(events, -1); found {
		t.Fatalf("first focused reread after checkpoint was rejected: %+v", violation)
	}

	events = append(events,
		rawEvent{ID: "same-window-host-view", Kind: "ActionEvent", Source: "agent", ToolName: "repository_view", ToolCallID: "call-3", ActionPath: "organization/host.go"},
		rawEvent{Kind: "ObservationEvent", ToolName: "repository_view", ToolCallID: "call-3", ObservationExitCode: &exitSuccess},
	)
	violation, found := repositorySearchLoopViolation(events, -1)
	if !found || violation.ID != "same-window-host-view" {
		t.Fatalf("same-window repeat was not rejected: found=%t violation=%+v", found, violation)
	}
}

func TestCheckpointCompletionAllowsBoundedReadsAndRejectsPostAnnouncementWork(t *testing.T) {
	checkpoint := progressCheckpoint{
		SchemaVersion:       "tekroo.teams.execution-progress-checkpoint/1.2.0",
		SourceJournalSHA256: kernel.Digest(strings.Repeat("a", 64)),
		NextAction:          "Evaluate retained evidence and submit the required result through the finish tool.",
	}
	events := []rawEvent{
		{ID: "checkpoint", Kind: "MessageEvent", Source: "user", Text: compactionCheckpointPrefix + "1\nrestored\n" + string(mustJSON(checkpoint))},
		{ID: "focused-search", Kind: "ActionEvent", Source: "agent", ToolName: "repository_search", ActionPath: "adapters/mcp"},
		{ID: "focused-view", Kind: "ActionEvent", Source: "agent", ToolName: "repository_view", ActionPath: "adapters/mcp/handler.go"},
	}
	if violation, repeated, found := checkpointCompletionRepositoryViolation(events, -1); found {
		t.Fatalf("reads after the completion announcement were rejected: violation=%+v repeated=%t", violation, repeated)
	}
	// Composing the completion result may require a bounded number of reads.
	for index := 0; index < maximumCheckpointCompletionReads-2; index++ {
		events = append(events, rawEvent{ID: fmt.Sprintf("late-read-%d", index), Kind: "ActionEvent", Source: "agent", ToolName: "file_editor", ActionCommand: "view", ActionPath: "organization/host.go"})
	}
	if violation, repeated, found := checkpointCompletionRepositoryViolation(events, -1); found {
		t.Fatalf("verification reads after the completion announcement were rejected: violation=%+v repeated=%t", violation, repeated)
	}
	events = append(events, rawEvent{ID: "excess-read", Kind: "ActionEvent", Source: "agent", ToolName: "repository_view", ActionPath: "organization/host.go"})
	violation, repeated, found := checkpointCompletionRepositoryViolation(events, -1)
	if !found || repeated || violation.ID != "excess-read" {
		t.Fatalf("excess completion read was not detected: found=%t repeated=%t violation=%+v", found, repeated, violation)
	}
	events = append(events, rawEvent{ID: "correction", Kind: "MessageEvent", Source: "user", Text: checkpointCompletionCorrectionPrefix + violation.ID})
	if violation, repeated, found := checkpointCompletionRepositoryViolation(events, -1); found {
		t.Fatalf("correction did not close prior excess reads: violation=%+v repeated=%t", violation, repeated)
	}
	events = append(events, rawEvent{ID: "post-correction-read", Kind: "ActionEvent", Source: "agent", ToolName: "repository_view", ActionPath: "cmd/tekroo"})
	violation, repeated, found = checkpointCompletionRepositoryViolation(events, -1)
	if !found || !repeated || violation.ID != "post-correction-read" {
		t.Fatalf("repository read after correction was not detected: found=%t repeated=%t violation=%+v", found, repeated, violation)
	}

	// A fresh sequence still rejects mutations immediately.
	events = events[:3]
	events = append(events, rawEvent{ID: "post-announcement-edit", Kind: "ActionEvent", Source: "agent", ToolName: "file_editor", ActionCommand: "str_replace", ActionPath: "organization/host.go"})
	violation, repeated, found = checkpointCompletionRepositoryViolation(events, -1)
	if !found || repeated || violation.ID != "post-announcement-edit" {
		t.Fatalf("repository mutation after completion handoff was not detected: found=%t repeated=%t violation=%+v", found, repeated, violation)
	}
	events = append(events, rawEvent{ID: "correction", Kind: "MessageEvent", Source: "user", Text: checkpointCompletionCorrectionPrefix + violation.ID})
	if violation, repeated, found := checkpointCompletionRepositoryViolation(events, -1); found {
		t.Fatalf("correction did not close prior mutation: violation=%+v repeated=%t", violation, repeated)
	}
	events = append(events, rawEvent{ID: "post-mutation-correction-read", Kind: "ActionEvent", Source: "agent", ToolName: "repository_view", ActionPath: "cmd/tekroo"})
	violation, repeated, found = checkpointCompletionRepositoryViolation(events, -1)
	if !found || !repeated || violation.ID != "post-mutation-correction-read" {
		t.Fatalf("repository work after mutation correction was not detected: found=%t repeated=%t violation=%+v", found, repeated, violation)
	}
}

func TestCheckpointCompletionAllowanceDoesNotResetAtLaterCompaction(t *testing.T) {
	checkpoint := progressCheckpoint{
		SchemaVersion:       "tekroo.teams.execution-progress-checkpoint/1.2.0",
		SourceJournalSHA256: kernel.Digest(strings.Repeat("c", 64)),
		NextAction:          "Submit the required result through the finish tool.",
	}
	checkpointEvent := func(id string) rawEvent {
		return rawEvent{ID: id, Kind: "MessageEvent", Source: "user", Text: compactionCheckpointPrefix + "1\nrestored\n" + string(mustJSON(checkpoint))}
	}
	events := []rawEvent{checkpointEvent("checkpoint-1")}
	for index := 0; index < maximumCheckpointCompletionReads/2; index++ {
		events = append(events, rawEvent{ID: fmt.Sprintf("first-window-%d", index), Kind: "ActionEvent", Source: "agent", ToolName: "repository_view", ActionPath: "organization/host.go"})
	}
	events = append(events, checkpointEvent("checkpoint-2"))
	for index := maximumCheckpointCompletionReads / 2; index <= maximumCheckpointCompletionReads; index++ {
		events = append(events, rawEvent{ID: fmt.Sprintf("second-window-%d", index), Kind: "ActionEvent", Source: "agent", ToolName: "repository_view", ActionPath: "organization/host.go"})
	}
	violation, repeated, found := checkpointCompletionRepositoryViolation(events, -1)
	if !found || repeated || violation.ID != fmt.Sprintf("second-window-%d", maximumCheckpointCompletionReads) {
		t.Fatalf("later checkpoint reset completion allowance: found=%t repeated=%t violation=%+v", found, repeated, violation)
	}
}

func TestResultBoundaryRecoveryAllowsNoNewRepositoryWork(t *testing.T) {
	invocationID := kernel.UUIDv7("018f0000-0000-7000-8000-000000000101")
	priorInvocationID := kernel.UUIDv7("018f0000-0000-7000-8000-000000000102")
	checkpoint := progressCheckpoint{
		SchemaVersion:       "tekroo.teams.execution-progress-checkpoint/1.2.0",
		InvocationID:        invocationID,
		PriorInvocationID:   &priorInvocationID,
		Source:              "OPENHANDS_EVENT_JOURNAL",
		SourceJournalSHA256: kernel.Digest(strings.Repeat("d", 64)),
		NextAction:          "Evaluate retained evidence and submit the required result through the finish tool.",
	}
	prompt := rawEvent{ID: "prompt", Kind: "MessageEvent", Source: "user", Text: string(mustJSON(map[string]any{"recovery_checkpoint": checkpoint}))}
	read := rawEvent{ID: "unexpected-read", Kind: "ActionEvent", Source: "agent", ToolName: "repository_view", ActionPath: "organization/host.go"}
	violation, repeated, found := checkpointCompletionRepositoryViolation([]rawEvent{prompt, read}, 0)
	if !found || repeated || violation.ID != read.ID {
		t.Fatalf("result-boundary recovery allowed repository work: found=%t repeated=%t violation=%+v", found, repeated, violation)
	}
}

func TestCheckpointCompletionGuardDoesNotFenceWritingWork(t *testing.T) {
	if checkpointCompletionGuardApplies(kernel.PurposeImplementation) {
		t.Fatal("implementation was incorrectly subject to the read-only completion guard")
	}
	if checkpointCompletionGuardApplies(kernel.PurposeRepair) {
		t.Fatal("repair was incorrectly subject to the read-only completion guard")
	}
	for _, purpose := range []kernel.WorkPurpose{kernel.PurposeInvestigation, kernel.PurposeReview, kernel.PurposeValidation, kernel.PurposePromotion} {
		if !checkpointCompletionGuardApplies(purpose) {
			t.Fatalf("%s unexpectedly bypassed the completion guard", purpose)
		}
	}
}

func TestCheckpointCompletionRejectsRepositoryWorkAfterCorrection(t *testing.T) {
	checkpoint := progressCheckpoint{
		SchemaVersion:       "tekroo.teams.execution-progress-checkpoint/1.2.0",
		SourceJournalSHA256: kernel.Digest(strings.Repeat("b", 64)),
		NextAction:          "Submit the required result through the finish tool.",
	}
	events := []rawEvent{
		{ID: "checkpoint", Kind: "MessageEvent", Source: "user", Text: compactionCheckpointPrefix + "2\nrestored\n" + string(mustJSON(checkpoint))},
		{ID: "correction", Kind: "MessageEvent", Source: "user", Text: checkpointCompletionCorrectionPrefix + "prior"},
		{ID: "ignored-correction", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ActionCommand: "touch organization/host.go"},
	}
	violation, repeated, found := checkpointCompletionRepositoryViolation(events, -1)
	if !found || !repeated || violation.ID != "ignored-correction" {
		t.Fatalf("repository work after correction was not detected: found=%t repeated=%t violation=%+v", found, repeated, violation)
	}
}

func TestProgressCheckpointRecordsActionOutcomesAndPaths(t *testing.T) {
	brief, requestDigest := openHandsTestBrief(t)
	exitFailure := 1
	exitSuccess := 0
	events := []rawEvent{
		{ID: "failed-view", Kind: "ActionEvent", Source: "agent", ToolName: "file_editor", ActionCommand: "view", ActionPath: "/workspace/a.go"},
		{Kind: "ObservationEvent", ToolName: "file_editor", ObservationError: true, ObservationExitCode: &exitFailure},
		{ID: "timed-read", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ActionCommand: "sed -n '1,80p' adapters/mcp/handler.go"},
		{Kind: "ObservationEvent", ToolName: "terminal", ObservationTimeout: true},
		{ID: "condensation", Kind: "Condensation", Source: "environment", Summary: "COMPLETED: inspected transport\nPENDING: implement binding"},
		{ID: "successful-view", Kind: "ActionEvent", Source: "agent", ToolName: "file_editor", ActionCommand: "view", ActionPath: "/workspace/b.go"},
		{Kind: "ObservationEvent", ToolName: "file_editor", Text: "package example", ObservationExitCode: &exitSuccess},
	}
	checkpoint := buildProgressCheckpoint(brief, events, -1)
	if checkpoint.SchemaVersion != "tekroo.teams.execution-progress-checkpoint/1.2.0" || checkpoint.AuthoritativeExecution.ExecutionBriefSHA256 != requestDigest || !reflect.DeepEqual(checkpoint.AuthoritativeExecution.Task, brief.Task) || checkpoint.AuthoritativeExecution.ActorFQN != brief.ActorFQN || checkpoint.AuthoritativeExecution.RoleGrounding.RoleFQRN != brief.RoleGrounding.RoleFQRN || checkpoint.AuthoritativeExecution.Scope.WorkspaceID != brief.Scope.WorkspaceID || len(checkpoint.Actions) != 3 || checkpoint.Actions[0].Outcome != "FAILED" || checkpoint.Actions[1].Outcome != "TIMED_OUT" || checkpoint.Actions[2].Outcome != "SUCCEEDED" || checkpoint.Actions[2].ObservationExcerpt == "" || len(checkpoint.InspectedPaths) != 1 || checkpoint.InspectedPaths[0] != "/workspace/b.go" || !checkpoint.SourceJournalSHA256.Valid() || checkpoint.NextAction == "" {
		t.Fatalf("checkpoint = %#v", checkpoint)
	}
}

func TestProgressCheckpointRecordsReadOnlyRepositoryTools(t *testing.T) {
	brief, _ := openHandsTestBrief(t)
	exitSuccess := 0
	searchPayload := json.RawMessage(`{"kind":"RepositorySearchAction","pattern":"RoleStore","path":"organization"}`)
	viewPayload := json.RawMessage(`{"kind":"RepositoryViewAction","path":"organization/host.go","view_range":[1,80]}`)
	events := []rawEvent{
		{Raw: json.RawMessage(`{"id":"search"}`), ID: "search", Kind: "ActionEvent", Source: "agent", ToolName: "repository_search", ToolCallID: "search-call", ActionPayload: searchPayload, ActionPath: "organization"},
		{Raw: json.RawMessage(`{"id":"search-result"}`), Kind: "ObservationEvent", ToolName: "repository_search", ToolCallID: "search-call", Text: "organization/host.go:24:type RoleStore interface", ObservationExitCode: &exitSuccess},
		{Raw: json.RawMessage(`{"id":"view"}`), ID: "view", Kind: "ActionEvent", Source: "agent", ToolName: "repository_view", ToolCallID: "view-call", ActionPayload: viewPayload, ActionPath: "organization/host.go"},
		{Raw: json.RawMessage(`{"id":"view-result"}`), Kind: "ObservationEvent", ToolName: "repository_view", ToolCallID: "view-call", Text: "package organization", ObservationExitCode: &exitSuccess},
	}

	checkpoint := buildProgressCheckpoint(brief, events, -1)
	if len(checkpoint.Actions) != 2 || checkpoint.Actions[0].Tool != "repository_search" || !strings.Contains(checkpoint.Actions[0].Command, `"pattern":"RoleStore"`) || checkpoint.Actions[1].Tool != "repository_view" || len(checkpoint.InspectedPaths) != 2 || checkpoint.InspectedPaths[0] != "organization" || checkpoint.InspectedPaths[1] != "organization/host.go" {
		t.Fatalf("checkpoint = %#v", checkpoint)
	}
}

func TestProgressCheckpointCompactsLongActionHistory(t *testing.T) {
	brief, _ := openHandsTestBrief(t)
	exitSuccess := 0
	events := make([]rawEvent, 0, 40)
	for index := 0; index < 20; index++ {
		callID := fmt.Sprintf("call-%02d", index)
		events = append(events,
			rawEvent{Raw: json.RawMessage(fmt.Sprintf(`{"id":"action-%02d"}`, index)), ID: fmt.Sprintf("action-%02d", index), Kind: "ActionEvent", Source: "agent", ToolName: "repository_search", ToolCallID: callID, ActionCommand: fmt.Sprintf("search-%02d", index), ActionPath: "organization"},
			rawEvent{Raw: json.RawMessage(fmt.Sprintf(`{"id":"observation-%02d"}`, index)), Kind: "ObservationEvent", ToolName: "repository_search", ToolCallID: callID, Text: "no match", ObservationExitCode: &exitSuccess},
		)
	}

	checkpoint := buildProgressCheckpoint(brief, events, -1)
	if len(checkpoint.Actions) != maximumCheckpointActions || checkpoint.Actions[0].EventID != "action-08" || checkpoint.Actions[len(checkpoint.Actions)-1].EventID != "action-19" {
		t.Fatalf("compacted actions = %#v", checkpoint.Actions)
	}
	if len(checkpoint.RepositoryEvidence) != maximumCheckpointRepositoryEvidence || checkpoint.SourceEventCount != len(events) {
		t.Fatalf("checkpoint = %#v", checkpoint)
	}
}

func TestCheckpointNextActionIgnoresFailedReadOnlyInspection(t *testing.T) {
	brief, _ := openHandsTestBrief(t)
	actions := []checkpointAction{
		{Tool: "terminal", Command: `grep -rn RetentionPolicy CONTRACTS 2>/dev/null`, Outcome: "FAILED"},
		{Tool: "repository_search", Command: `{"pattern":"RetentionPolicy"}`, Path: "organization", Outcome: "PENDING"},
	}
	next := checkpointNextAction(brief, actions, nil, nil)
	if !strings.Contains(next, "implement the first unmet acceptance criterion") {
		t.Fatalf("next action = %q", next)
	}
}

func TestCheckpointNextActionRequiresRepairBeforeFailedValidationRerun(t *testing.T) {
	brief, _ := openHandsTestBrief(t)
	actions := []checkpointAction{{Tool: "terminal", Command: "go test -run TestActorName ./adapters/operationalruntime", Outcome: "FAILED"}}
	next := checkpointNextAction(brief, actions, nil, nil)
	if !strings.Contains(next, "repair its root cause") || !strings.Contains(next, "Do not rerun the same validation") {
		t.Fatalf("next action = %q", next)
	}
}

func TestProgressCheckpointPairsParallelToolResultsByToolCallID(t *testing.T) {
	brief, _ := openHandsTestBrief(t)
	exitFailure := 1
	exitSuccess := 0
	events := []rawEvent{
		{ID: "validate", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ToolCallID: "call-validate", ActionCommand: "go test ./adapters/openhands"},
		{ID: "inspect", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ToolCallID: "call-inspect", ActionCommand: "rg -n Candidate adapters/openhands"},
		{Kind: "ObservationEvent", ToolName: "terminal", ToolCallID: "call-inspect", ObservationError: true, ObservationExitCode: &exitFailure},
		{Kind: "ObservationEvent", ToolName: "terminal", ToolCallID: "call-validate", ObservationExitCode: &exitSuccess},
	}
	checkpoint := buildProgressCheckpoint(brief, events, -1)
	if len(checkpoint.Actions) != 2 || checkpoint.Actions[0].Outcome != "SUCCEEDED" || checkpoint.Actions[1].Outcome != "FAILED" || len(checkpoint.Validations) != 1 || checkpoint.Validations[0].Outcome != "SUCCEEDED" {
		t.Fatalf("checkpoint = %#v", checkpoint)
	}
}

func TestDecodeEventPreservesCondenserSummary(t *testing.T) {
	event, err := decodeEvent(json.RawMessage(`{"id":"condense-1","kind":"Condensation","source":"environment","summary":"COMPLETED: repository inspection\nPENDING: focused edit"}`))
	if err != nil || event.Kind != "Condensation" || event.Summary != "COMPLETED: repository inspection\nPENDING: focused edit" {
		t.Fatalf("event=%#v err=%v", event, err)
	}
}

func TestDecodeEventPreservesToolCallIdentity(t *testing.T) {
	event, err := decodeEvent(json.RawMessage(`{"id":"observation-1","kind":"ObservationEvent","source":"environment","tool_name":"terminal","tool_call_id":"call-1","observation":{"kind":"TerminalObservation","exit_code":0}}`))
	if err != nil || event.ToolCallID != "call-1" {
		t.Fatalf("event=%#v err=%v", event, err)
	}
}

func TestDecodeEventPreservesCustomRepositoryAction(t *testing.T) {
	event, err := decodeEvent(json.RawMessage(`{"id":"search-1","kind":"ActionEvent","source":"agent","tool_name":"repository_search","tool_call_id":"call-1","action":{"kind":"RepositorySearchAction","pattern":"RoleStore","path":"organization"}}`))
	if err != nil || event.ToolName != "repository_search" || event.ActionPath != "organization" || !strings.Contains(string(event.ActionPayload), `"pattern":"RoleStore"`) {
		t.Fatalf("event=%#v err=%v", event, err)
	}
}

func TestProgressCheckpointRecordsValidationAndChangedPath(t *testing.T) {
	brief, _ := openHandsTestBrief(t)
	exitSuccess := 0
	events := []rawEvent{
		{ID: "edit", Kind: "ActionEvent", Source: "agent", ToolName: "file_editor", ActionCommand: "str_replace", ActionPath: "/workspace/a.go"},
		{Kind: "ObservationEvent", ToolName: "file_editor", ObservationExitCode: &exitSuccess},
		{ID: "test", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ActionCommand: "go test ./adapters/mcp ./adapters/operatortools"},
		{Kind: "ObservationEvent", ToolName: "terminal", ObservationExitCode: &exitSuccess},
	}
	checkpoint := buildProgressCheckpoint(brief, events, -1)
	if len(checkpoint.ChangedPaths) != 1 || checkpoint.ChangedPaths[0] != "/workspace/a.go" || len(checkpoint.Validations) != 1 || checkpoint.Validations[0].Command != "go test ./adapters/mcp ./adapters/operatortools" {
		t.Fatalf("checkpoint = %#v", checkpoint)
	}
}

func TestProgressCheckpointDoesNotLetCondenserRedefineAssignment(t *testing.T) {
	brief, _ := openHandsTestBrief(t)
	brief.Purpose = kernel.PurposeReplan
	brief.ActorFQN = "teams::architect-1"
	brief.RoleGrounding.ActorFQN = brief.ActorFQN
	brief.RoleGrounding.RoleFQRN = "architect"
	brief.Task.Title = "Produce the implementation plan"
	brief.Task.Description = "Inspect the repository, make an evidence-grounded plan, and return FEATURE_PLAN."
	brief.ResultProtocol = &application.ExecutionResultProtocol{SchemaVersion: "1.0.0", Marker: "TEKROO_ORGANIZATIONAL_RESULT:", Outcomes: []string{"FEATURE_PLAN"}, Instruction: "Return the required plan."}
	exitSuccess := 0
	events := []rawEvent{
		{ID: "view", Kind: "ActionEvent", Source: "agent", ToolName: "file_editor", ActionCommand: "view", ActionPath: "/workspace/organization/feature.go"},
		{ID: "view-result", Kind: "ObservationEvent", ToolName: "file_editor", Text: "package organization\ntype Feature struct{}", ObservationExitCode: &exitSuccess},
		{ID: "condensation", Kind: "Condensation", Source: "environment", Summary: "USER_CONTEXT: Explore and understand the repository.\nPENDING: No task specified."},
	}

	checkpoint := buildProgressCheckpoint(brief, events, -1)
	encoded, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if checkpoint.AuthoritativeExecution.Task.Title != "Produce the implementation plan" || checkpoint.AuthoritativeExecution.RoleGrounding.RoleFQRN != "architect" || checkpoint.AuthoritativeExecution.Purpose != kernel.PurposeReplan || !strings.Contains(checkpoint.NextAction, "complete the authoritative task") || strings.Contains(text, "Explore and understand the repository") || strings.Contains(text, "untrusted_condenser_summary") || strings.Contains(text, "untrusted_agent_reported_state") {
		t.Fatalf("checkpoint = %s", encoded)
	}
}

func TestProgressCheckpointDoesNotMakeArchitectRetryFailedDiscovery(t *testing.T) {
	brief, _ := openHandsTestBrief(t)
	brief.Purpose = kernel.PurposeReplan
	brief.ActorFQN = "teams::architect-1"
	brief.RoleGrounding.ActorFQN = brief.ActorFQN
	brief.RoleGrounding.RoleFQRN = "architect"
	brief.ResultProtocol = &application.ExecutionResultProtocol{SchemaVersion: "1.0.0", Marker: "TEKROO_ORGANIZATIONAL_RESULT:", Outcomes: []string{"FEATURE_PLAN"}, Instruction: "Return the required plan."}
	exitFailure := 1
	exitSuccess := 0
	events := []rawEvent{
		{ID: "missing-directory", Kind: "ActionEvent", Source: "agent", ToolName: "repository_view", ActionPath: "domain"},
		{Kind: "ObservationEvent", ToolName: "repository_view", ObservationError: true, ObservationExitCode: &exitFailure},
		{ID: "host-view", Kind: "ActionEvent", Source: "agent", ToolName: "repository_view", ActionPath: "organization/host.go"},
		{Kind: "ObservationEvent", ToolName: "repository_view", Text: "package organization", ObservationExitCode: &exitSuccess},
	}

	checkpoint := buildProgressCheckpoint(brief, events, -1)
	if strings.Contains(checkpoint.NextAction, "Resolve the retained failed") || !strings.Contains(checkpoint.NextAction, "complete the authoritative task") || !strings.Contains(checkpoint.NextAction, "do not retry failed broad discovery") {
		t.Fatalf("next action = %q", checkpoint.NextAction)
	}
}

func TestProgressCheckpointNextActionAdvancesImplementation(t *testing.T) {
	brief, _ := openHandsTestBrief(t)
	exitSuccess := 0
	discovery := []rawEvent{
		{ID: "view", Kind: "ActionEvent", Source: "agent", ToolName: "file_editor", ActionCommand: "view", ActionPath: "/workspace/a.go"},
		{Kind: "ObservationEvent", ToolName: "file_editor", Text: "package example", ObservationExitCode: &exitSuccess},
	}
	checkpoint := buildProgressCheckpoint(brief, discovery, -1)
	if !strings.Contains(checkpoint.NextAction, "implement the first unmet acceptance criterion") {
		t.Fatalf("discovery next action = %q", checkpoint.NextAction)
	}

	changed := append(discovery,
		rawEvent{ID: "edit", Kind: "ActionEvent", Source: "agent", ToolName: "file_editor", ActionCommand: "str_replace", ActionPath: "/workspace/a.go"},
		rawEvent{Kind: "ObservationEvent", ToolName: "file_editor", Text: "Done!", ObservationExitCode: &exitSuccess},
	)
	checkpoint = buildProgressCheckpoint(brief, changed, -1)
	if !strings.Contains(checkpoint.NextAction, "Run the narrowest deterministic validation") {
		t.Fatalf("changed next action = %q", checkpoint.NextAction)
	}

	validated := append(changed,
		rawEvent{ID: "test", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ToolCallID: "test-call", ActionCommand: "go test ./..."},
		rawEvent{Kind: "ObservationEvent", ToolName: "terminal", ToolCallID: "test-call", ObservationExitCode: &exitSuccess},
	)
	checkpoint = buildProgressCheckpoint(brief, validated, -1)
	if !strings.Contains(checkpoint.NextAction, "complete any remaining implementation and tests") || !strings.Contains(checkpoint.NextAction, "after the final repository change") || !strings.Contains(checkpoint.NextAction, "commit only the intended source and test changes") {
		t.Fatalf("validated next action = %q", checkpoint.NextAction)
	}
}

func TestClientAllowsManyDistinctRepositoryActions(t *testing.T) {
	brief, digest := openHandsTestBrief(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	events := []map[string]any{event("evt-user", "MessageEvent", "user", string(mustJSON(brief)))}
	for index := 0; index < maximumRepositoryDiscoveryActions*3; index++ {
		events = append(events,
			actionEvent(fmt.Sprintf("view-%02d", index), "terminal", fmt.Sprintf("sed -n '%d,%dp' organization/host.go", index*10+1, index*10+10)),
			observationEvent(fmt.Sprintf("view-observation-%02d", index), "terminal", false, 0),
		)
	}
	state := &progressGuardServerState{prompt: string(mustJSON(brief)), workspace: workspace, events: events}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalRunning || state.interruptCalls != 0 {
		t.Fatalf("observation=%#v err=%v interrupts=%d", observation, err, state.interruptCalls)
	}
}

func TestClientStopsModelResponseWithoutActionOrResult(t *testing.T) {
	brief, digest := openHandsTestBrief(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	state := &progressGuardServerState{
		prompt: string(mustJSON(brief)), workspace: workspace, terminal: true,
		events: []map[string]any{
			event("evt-user", "MessageEvent", "user", string(mustJSON(brief))),
			event("reasoning-only", "MessageEvent", "agent", ""),
		},
	}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalFailed || observation.Retryable || state.interruptCalls != 0 || state.correctionCalls != 0 || !strings.Contains(string(observation.Output), "MODEL_RESPONSE_WITHOUT_ACTION_OR_RESULT") {
		t.Fatalf("observation=%#v err=%v interrupts=%d corrections=%d", observation, err, state.interruptCalls, state.correctionCalls)
	}
}

func TestClientAllowsOneInFlightModelResponseWithoutActionForOpenHandsRecovery(t *testing.T) {
	brief, digest := openHandsTestBrief(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	state := &progressGuardServerState{
		prompt: string(mustJSON(brief)), workspace: workspace,
		events: []map[string]any{
			event("evt-user", "MessageEvent", "user", string(mustJSON(brief))),
			event("reasoning-only", "MessageEvent", "agent", ""),
			event("framework-recovery", "MessageEvent", "environment", "Please use a tool to proceed with the task."),
		},
	}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalRunning || state.interruptCalls != 0 {
		t.Fatalf("observation=%#v err=%v interrupts=%d", observation, err, state.interruptCalls)
	}
}

func TestClientStopsRepeatedInFlightModelResponsesWithoutAction(t *testing.T) {
	brief, digest := openHandsTestBrief(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	state := &progressGuardServerState{
		prompt: string(mustJSON(brief)), workspace: workspace,
		events: []map[string]any{
			event("evt-user", "MessageEvent", "user", string(mustJSON(brief))),
			event("reasoning-only-1", "MessageEvent", "agent", ""),
			event("framework-recovery-1", "MessageEvent", "environment", "Please use a tool to proceed with the task."),
			event("reasoning-only-2", "MessageEvent", "agent", ""),
		},
	}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalFailed || observation.Retryable || state.interruptCalls != 1 || !strings.Contains(string(observation.Output), "MODEL_RESPONSE_WITHOUT_ACTION_OR_RESULT") {
		t.Fatalf("observation=%#v err=%v interrupts=%d", observation, err, state.interruptCalls)
	}
}

func TestClientWaitsForRequestedCondensationAfterRepeatedModelResponsesWithoutAction(t *testing.T) {
	brief, digest := openHandsTestBrief(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	state := &progressGuardServerState{
		prompt: string(mustJSON(brief)), workspace: workspace,
		events: []map[string]any{
			event("evt-user", "MessageEvent", "user", string(mustJSON(brief))),
			event("reasoning-only-1", "MessageEvent", "agent", ""),
			event("framework-recovery-1", "MessageEvent", "environment", "Please use a tool to proceed with the task."),
			event("reasoning-only-2", "MessageEvent", "agent", ""),
			event("condensation-request", "CondensationRequest", "environment", ""),
		},
	}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalRunning || state.interruptCalls != 0 {
		t.Fatalf("observation=%#v err=%v interrupts=%d", observation, err, state.interruptCalls)
	}
}

func TestClientAllowsRecoveredModelResponseWithoutAction(t *testing.T) {
	brief, digest := openHandsTestBrief(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	state := &progressGuardServerState{
		prompt: string(mustJSON(brief)), workspace: workspace,
		events: []map[string]any{
			event("evt-user", "MessageEvent", "user", string(mustJSON(brief))),
			event("reasoning-only", "MessageEvent", "agent", ""),
			event("framework-recovery", "MessageEvent", "environment", "Please use a tool to proceed with the task."),
			actionEvent("recovered-view", "repository_view", "organization/host.go"),
			observationEvent("recovered-view-result", "repository_view", false, 0),
		},
	}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalRunning || state.interruptCalls != 0 {
		t.Fatalf("observation=%#v err=%v interrupts=%d", observation, err, state.interruptCalls)
	}
}

func TestEquivalentActionHistoryDoesNotResetAfterFailedMutation(t *testing.T) {
	exitSuccess := 0
	exitFailure := 1
	events := []rawEvent{
		{ID: "first-read", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ActionCommand: "sed -n '1,80p' organization/message.go"},
		{Kind: "ObservationEvent", ToolName: "terminal", ObservationExitCode: &exitSuccess},
		{ID: "failed-edit", Kind: "ActionEvent", Source: "agent", ToolName: "file_editor", ActionCommand: "str_replace", ActionPath: "/workspace/organization/message.go"},
		{Kind: "ObservationEvent", ToolName: "file_editor", ObservationError: true, ObservationExitCode: &exitFailure},
		{ID: "repeated-read", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ActionCommand: "sed -n '1,80p' organization/message.go"},
		{Kind: "ObservationEvent", ToolName: "terminal", ObservationExitCode: &exitSuccess},
	}
	violation, found := repositorySearchLoopViolation(events, -1)
	if !found || violation.ID != "repeated-read" {
		t.Fatalf("violation=%+v found=%t", violation, found)
	}
}

func TestEquivalentActionHistoryAllowsRereadAfterOutOfOrderSuccessfulMutation(t *testing.T) {
	exitSuccess := 0
	events := []rawEvent{
		{ID: "first-read", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ToolCallID: "call-read", ActionCommand: "sed -n '1,80p' organization/message.go"},
		{ID: "edit", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ToolCallID: "call-edit", ActionCommand: "gofmt -w organization/message.go"},
		{Kind: "ObservationEvent", ToolName: "terminal", ToolCallID: "call-edit", ObservationExitCode: &exitSuccess},
		{Kind: "ObservationEvent", ToolName: "terminal", ToolCallID: "call-read", ObservationExitCode: &exitSuccess},
		{ID: "repeated-read", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ToolCallID: "call-repeat", ActionCommand: "sed -n '1,80p' organization/message.go"},
	}
	if violation, found := repositorySearchLoopViolation(events, -1); found {
		t.Fatalf("unexpected violation after successful mutation: %+v", violation)
	}
}

func TestRepositoryProgressGuardDoesNotTreatRepeatedValidationAsDiscoveryLoop(t *testing.T) {
	exitSuccess := 0
	events := []rawEvent{
		{ID: "first-validation", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ActionCommand: "go test -tags mongo_integration -count=1 ./adapters/mongo/..."},
		{Kind: "ObservationEvent", ToolName: "terminal", ObservationExitCode: &exitSuccess},
		{ID: "repeated-validation", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ActionCommand: "go test -tags mongo_integration -count=1 ./adapters/mongo/..."},
		{Kind: "ObservationEvent", ToolName: "terminal", ObservationExitCode: &exitSuccess},
	}

	if violation, found := repositorySearchLoopViolation(events, -1); found {
		t.Fatalf("repeated deterministic validation was misclassified as a repository discovery loop: %+v", violation)
	}
}

func TestDeterministicValidationGuardStopsThirdEquivalentSuccess(t *testing.T) {
	exitSuccess := 0
	events := []rawEvent{
		{ID: "first", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ToolCallID: "call-1", ActionCommand: "go test -tags mongo_integration -run 'TestRetentionPolicyStore' ./adapters/mongo/"},
		{Kind: "ObservationEvent", ToolName: "terminal", ToolCallID: "call-1", ObservationExitCode: &exitSuccess},
		{ID: "second", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ToolCallID: "call-2", ActionCommand: "go test -tags mongo_integration -run 'TestRetentionPolicyStore' -count=3 ./adapters/mongo/"},
		{Kind: "ObservationEvent", ToolName: "terminal", ToolCallID: "call-2", ObservationExitCode: &exitSuccess},
		{ID: "third", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ToolCallID: "call-3", ActionCommand: "go test -tags mongo_integration -run 'TestRetentionPolicyStore' -v ./adapters/mongo/"},
		{Kind: "ObservationEvent", ToolName: "terminal", ToolCallID: "call-3", ObservationExitCode: &exitSuccess},
	}
	violation, repeated, found := repeatedDeterministicValidationViolation(events, -1)
	if !found || repeated || violation.ID != "third" {
		t.Fatalf("violation=%+v repeated=%t found=%t", violation, repeated, found)
	}
}

func TestFailedDeterministicValidationGuardStopsUnchangedSecondFailure(t *testing.T) {
	exitFailure := 1
	command := "go test -run TestActorName ./adapters/operationalruntime"
	events := []rawEvent{
		{ID: "first", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ToolCallID: "call-1", ActionCommand: command},
		{Kind: "ObservationEvent", ToolName: "terminal", ToolCallID: "call-1", Text: "invalid team manifest", ObservationExitCode: &exitFailure},
		{ID: "second", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ToolCallID: "call-2", ActionCommand: command},
		{Kind: "ObservationEvent", ToolName: "terminal", ToolCallID: "call-2", Text: "invalid team manifest", ObservationExitCode: &exitFailure},
	}
	violation, repeated, found := repeatedFailedDeterministicValidationViolation(events, -1)
	if !found || repeated || violation.ID != "second" {
		t.Fatalf("violation=%+v repeated=%t found=%t", violation, repeated, found)
	}
}

func TestFailedDeterministicValidationGuardAllowsOnePostCompactionRetry(t *testing.T) {
	exitFailure := 1
	command := "go test -run TestActorName ./adapters/operationalruntime"
	events := []rawEvent{
		{ID: "before", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ToolCallID: "call-1", ActionCommand: command},
		{Kind: "ObservationEvent", ToolName: "terminal", ToolCallID: "call-1", Text: "invalid team manifest", ObservationExitCode: &exitFailure},
		{Kind: "Condensation"},
		{Kind: "MessageEvent", Source: "user", Text: compactionCheckpointPrefix + "1\nrestored\n{}"},
		{ID: "first-after", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ToolCallID: "call-2", ActionCommand: command},
		{Kind: "ObservationEvent", ToolName: "terminal", ToolCallID: "call-2", Text: "invalid team manifest", ObservationExitCode: &exitFailure},
	}
	if violation, repeated, found := repeatedFailedDeterministicValidationViolation(events, -1); found {
		t.Fatalf("first post-compaction retry was rejected: violation=%+v repeated=%t", violation, repeated)
	}
	events = append(events,
		rawEvent{ID: "second-after", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ToolCallID: "call-3", ActionCommand: command},
		rawEvent{Kind: "ObservationEvent", ToolName: "terminal", ToolCallID: "call-3", Text: "invalid team manifest", ObservationExitCode: &exitFailure},
	)
	violation, repeated, found := repeatedFailedDeterministicValidationViolation(events, -1)
	if !found || repeated || violation.ID != "second-after" {
		t.Fatalf("violation=%+v repeated=%t found=%t", violation, repeated, found)
	}
}

func TestFailedDeterministicValidationGuardResetsAfterMutation(t *testing.T) {
	exitFailure := 1
	exitSuccess := 0
	command := "go test -run TestActorName ./adapters/operationalruntime"
	events := []rawEvent{
		{ID: "first", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ToolCallID: "call-1", ActionCommand: command},
		{Kind: "ObservationEvent", ToolName: "terminal", ToolCallID: "call-1", Text: "invalid team manifest", ObservationExitCode: &exitFailure},
		{ID: "edit", Kind: "ActionEvent", Source: "agent", ToolName: "file_editor", ToolCallID: "call-2", ActionCommand: "str_replace", ActionPath: "fixture_test.go"},
		{Kind: "ObservationEvent", ToolName: "file_editor", ToolCallID: "call-2", ObservationExitCode: &exitSuccess},
		{ID: "after-edit", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ToolCallID: "call-3", ActionCommand: command},
		{Kind: "ObservationEvent", ToolName: "terminal", ToolCallID: "call-3", Text: "invalid team manifest", ObservationExitCode: &exitFailure},
	}
	if violation, repeated, found := repeatedFailedDeterministicValidationViolation(events, -1); found {
		t.Fatalf("post-mutation validation was rejected: violation=%+v repeated=%t", violation, repeated)
	}
}

func TestFailedDeterministicValidationGuardRejectsRepeatAfterCorrection(t *testing.T) {
	exitFailure := 1
	command := "go test -run TestActorName ./adapters/operationalruntime"
	events := []rawEvent{
		{ID: "first", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ToolCallID: "call-1", ActionCommand: command},
		{Kind: "ObservationEvent", ToolName: "terminal", ToolCallID: "call-1", Text: "invalid team manifest", ObservationExitCode: &exitFailure},
		{ID: "second", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ToolCallID: "call-2", ActionCommand: command},
		{Kind: "ObservationEvent", ToolName: "terminal", ToolCallID: "call-2", Text: "invalid team manifest", ObservationExitCode: &exitFailure},
		{Kind: "MessageEvent", Source: "user", Text: failedDeterministicValidationCorrectionPrefix + "second\nrepair it"},
		{ID: "ignored", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ToolCallID: "call-3", ActionCommand: command},
		{Kind: "ObservationEvent", ToolName: "terminal", ToolCallID: "call-3", Text: "invalid team manifest", ObservationExitCode: &exitFailure},
	}
	violation, repeated, found := repeatedFailedDeterministicValidationViolation(events, -1)
	if !found || !repeated || violation.ID != "ignored" {
		t.Fatalf("violation=%+v repeated=%t found=%t", violation, repeated, found)
	}
}

func TestDeterministicValidationGuardAllowsDifferentChecksAndResetsAfterMutation(t *testing.T) {
	exitSuccess := 0
	events := []rawEvent{
		{ID: "first", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ToolCallID: "call-1", ActionCommand: "go test -run TestOne ./adapters/mongo"},
		{Kind: "ObservationEvent", ToolName: "terminal", ToolCallID: "call-1", ObservationExitCode: &exitSuccess},
		{ID: "second", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ToolCallID: "call-2", ActionCommand: "go test -run TestTwo ./adapters/mongo"},
		{Kind: "ObservationEvent", ToolName: "terminal", ToolCallID: "call-2", ObservationExitCode: &exitSuccess},
		{ID: "repeat-before-edit", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ToolCallID: "call-3", ActionCommand: "go test -run TestOne -count=2 ./adapters/mongo/"},
		{Kind: "ObservationEvent", ToolName: "terminal", ToolCallID: "call-3", ObservationExitCode: &exitSuccess},
		{ID: "edit", Kind: "ActionEvent", Source: "agent", ToolName: "file_editor", ToolCallID: "call-4", ActionCommand: "str_replace", ActionPath: "/workspace/a.go"},
		{Kind: "ObservationEvent", ToolName: "file_editor", ToolCallID: "call-4", ObservationExitCode: &exitSuccess},
		{ID: "first-after-edit", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ToolCallID: "call-5", ActionCommand: "go test -run TestOne -v ./adapters/mongo"},
		{Kind: "ObservationEvent", ToolName: "terminal", ToolCallID: "call-5", ObservationExitCode: &exitSuccess},
	}
	if violation, repeated, found := repeatedDeterministicValidationViolation(events, -1); found {
		t.Fatalf("unexpected violation=%+v repeated=%t", violation, repeated)
	}
}

func TestDeterministicValidationGuardRejectsCorrectedEquivalentAction(t *testing.T) {
	exitSuccess := 0
	command := "go test -run TestOne ./adapters/mongo"
	events := []rawEvent{
		{ID: "first", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ToolCallID: "call-1", ActionCommand: command},
		{Kind: "ObservationEvent", ToolName: "terminal", ToolCallID: "call-1", ObservationExitCode: &exitSuccess},
		{ID: "second", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ToolCallID: "call-2", ActionCommand: command},
		{Kind: "ObservationEvent", ToolName: "terminal", ToolCallID: "call-2", ObservationExitCode: &exitSuccess},
		{ID: "third", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ToolCallID: "call-3", ActionCommand: command},
		{Kind: "ObservationEvent", ToolName: "terminal", ToolCallID: "call-3", ObservationExitCode: &exitSuccess},
		{ID: "correction", Kind: "MessageEvent", Source: "user", Text: deterministicValidationCorrectionPrefix + "third\nreuse evidence"},
		{ID: "ignored", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ToolCallID: "call-4", ActionCommand: "go test -run TestOne -count=5 ./adapters/mongo/"},
	}
	violation, repeated, found := repeatedDeterministicValidationViolation(events, -1)
	if !found || !repeated || violation.ID != "ignored" {
		t.Fatalf("violation=%+v repeated=%t found=%t", violation, repeated, found)
	}
}

func TestClientProgressGuardTreatsSuccessfulValidationAsProgress(t *testing.T) {
	brief, digest := openHandsTestBrief(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	events := []map[string]any{event("evt-user", "MessageEvent", "user", string(mustJSON(brief)))}
	commands := []string{
		`grep -n "ValidTeamName\\|ActorTeamOf" organization/*.go`,
		`grep -n "aggregate_revision" adapters/mongo/store.go`,
		`sed -n '240,275p' adapters/mongo/store.go`,
		`ls adapters/mongo/store_test.go`,
		`sed -n '1,260p' adapters/mongo/store_test.go`,
		`go test -tags mongo_integration -run TestStartupPinsMetadataAndRequiredIndexes ./adapters/mongo/`,
		`go test -count=1 -tags mongo_integration -run TestStartupPinsMetadataAndRequiredIndexes ./adapters/mongo/`,
		`go test -count=1 ./adapters/mongo/`,
		`go test -count=1 ./organization/`,
		`git diff --check`,
		`git status`,
		`git --no-pager diff adapters/mongo/store.go`,
	}
	for index, command := range commands {
		events = append(events,
			actionEvent(fmt.Sprintf("action-%02d", index), "terminal", command),
			observationEvent(fmt.Sprintf("observation-%02d", index), "terminal", false, 0),
		)
	}
	state := &progressGuardServerState{prompt: string(mustJSON(brief)), workspace: workspace, events: events}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalRunning || state.interruptCalls != 0 {
		t.Fatalf("validation progress observation=%#v err=%v interrupts=%d", observation, err, state.interruptCalls)
	}
}

func TestClientAllowsDistinctInspectionAfterSuccessfulValidation(t *testing.T) {
	brief, digest := openHandsTestBrief(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	events := []map[string]any{
		event("evt-user", "MessageEvent", "user", string(mustJSON(brief))),
		actionEvent("test-action", "terminal", "go test ./organization"),
		observationEvent("test-observation", "terminal", false, 0),
	}
	for index := 0; index < maximumRepositoryDiscoveryActions+1; index++ {
		events = append(events,
			actionEvent(fmt.Sprintf("action-%02d", index), "terminal", fmt.Sprintf("sed -n '%d,%dp' organization/message.go", index*20+1, index*20+20)),
			observationEvent(fmt.Sprintf("observation-%02d", index), "terminal", false, 0),
		)
	}
	state := &progressGuardServerState{prompt: string(mustJSON(brief)), workspace: workspace, events: events}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalRunning || state.interruptCalls != 0 {
		t.Fatalf("post-validation bound observation=%#v err=%v interrupts=%d", observation, err, state.interruptCalls)
	}
}

func TestProgressCheckpointCarriesSuccessfulValidationAcrossCompactionBoundary(t *testing.T) {
	brief, _ := openHandsTestBrief(t)
	brief.Purpose = kernel.PurposeValidation
	exitSuccess := 0
	prior := progressCheckpoint{
		SchemaVersion:       "tekroo.teams.execution-progress-checkpoint/1.1.0",
		SourceEventCount:    5,
		SourceJournalSHA256: kernel.Digest(strings.Repeat("a", 64)),
		InspectedPaths:      []string{"adapters/mongo/widget_store.go"},
		Actions: []checkpointAction{{
			EventID: "source-1", Tool: "repository_view", Command: "adapters/mongo/widget_store.go", Path: "adapters/mongo/widget_store.go", Outcome: "SUCCEEDED", ObservationExcerpt: "func Save()",
		}},
		Validations: []checkpointAction{{
			EventID: "validation-1", Tool: "terminal", Command: "go test ./adapters/mongo/...", Outcome: "SUCCEEDED",
		}},
	}
	events := []rawEvent{
		{Kind: "MessageEvent", Source: "user", Text: compactionCheckpointPrefix + "1\nrestore\n" + string(mustJSON(prior))},
		{ID: "post-checkpoint-read", Kind: "ActionEvent", Source: "agent", ToolName: "repository_view", ToolCallID: "read-1", ActionPath: "adapters/mongo/store.go"},
		{Kind: "ObservationEvent", ToolName: "repository_view", ToolCallID: "read-1", Text: "package mongo", ObservationExitCode: &exitSuccess},
	}

	checkpoint := buildProgressCheckpoint(brief, events, 0)
	if checkpoint.SourceEventCount != 7 || len(checkpoint.Validations) != 1 || checkpoint.Validations[0].Outcome != "SUCCEEDED" || !strings.Contains(checkpoint.NextAction, "Evaluate the retained validation evidence") {
		t.Fatalf("cumulative checkpoint=%#v", checkpoint)
	}
	if !slices.Contains(checkpoint.InspectedPaths, "adapters/mongo/widget_store.go") || !slices.Contains(checkpoint.InspectedPaths, "adapters/mongo/store.go") {
		t.Fatalf("cumulative inspected paths=%v", checkpoint.InspectedPaths)
	}
	if len(checkpoint.RepositoryEvidence) != 2 || checkpoint.RepositoryEvidence[0].Path != "adapters/mongo/widget_store.go" || checkpoint.RepositoryEvidence[0].ObservationExcerpt != "func Save()" || checkpoint.RepositoryEvidence[1].Path != "adapters/mongo/store.go" || checkpoint.RepositoryEvidence[1].ObservationExcerpt != "package mongo" {
		t.Fatalf("cumulative repository evidence=%#v", checkpoint.RepositoryEvidence)
	}
}

func TestProgressCheckpointCarriesBoundedRepositoryEvidenceAcrossCompactionBoundary(t *testing.T) {
	brief, _ := openHandsTestBrief(t)
	prior := progressCheckpoint{
		SchemaVersion:       "tekroo.teams.execution-progress-checkpoint/1.2.0",
		SourceEventCount:    5,
		SourceJournalSHA256: kernel.Digest(strings.Repeat("b", 64)),
		RepositoryEvidence: []checkpointAction{{
			EventID: "prior-view", Tool: "repository_view", Command: "adapters/mongo/widget_store.go", Path: "adapters/mongo/widget_store.go", Outcome: "SUCCEEDED", ObservationExcerpt: "func Save()",
		}},
	}
	exitSuccess := 0
	events := []rawEvent{
		{Kind: "MessageEvent", Source: "user", Text: compactionCheckpointPrefix + "1\nrestore\n" + string(mustJSON(prior))},
		{ID: "current-view", Kind: "ActionEvent", Source: "agent", ToolName: "repository_view", ToolCallID: "read-2", ActionPath: "adapters/mongo/store.go"},
		{Kind: "ObservationEvent", ToolName: "repository_view", ToolCallID: "read-2", Text: "func ensureIndexes()", ObservationExitCode: &exitSuccess},
	}

	checkpoint := buildProgressCheckpoint(brief, events, 0)
	if len(checkpoint.RepositoryEvidence) != 2 || checkpoint.RepositoryEvidence[0].ObservationExcerpt != "func Save()" || checkpoint.RepositoryEvidence[1].ObservationExcerpt != "func ensureIndexes()" {
		t.Fatalf("retained repository evidence=%#v", checkpoint.RepositoryEvidence)
	}
}

func TestProgressCheckpointCarriesRepositoryEvidenceAcrossExplicitRecoveryChain(t *testing.T) {
	brief, _ := openHandsTestBrief(t)
	priorID := kernel.UUIDv7("00000000-0000-7000-8000-000000000280")
	prior := progressCheckpoint{
		SchemaVersion:       "tekroo.teams.execution-progress-checkpoint/1.2.0",
		InvocationID:        "00000000-0000-7000-8000-000000000281",
		PriorInvocationID:   &priorID,
		Source:              "OPENHANDS_EVENT_JOURNAL",
		SourceEventCount:    12,
		SourceJournalSHA256: digest('7'),
		RepositoryEvidence: []checkpointAction{
			{Tool: "repository_view", Path: "AGENTS.md", Outcome: "SUCCEEDED", ObservationSHA256: digest('8'), ObservationExcerpt: "instructions"},
			{Tool: "repository_view", Path: "organization/host.go", Outcome: "SUCCEEDED", ObservationSHA256: digest('9'), ObservationExcerpt: "package organization"},
		},
	}
	prompt := mustJSON(map[string]any{"recovery_checkpoint": prior})
	events := []rawEvent{{Raw: prompt, Kind: "MessageEvent", Source: "user", Text: string(prompt)}}
	checkpoint := buildProgressCheckpoint(brief, events, -1)
	if checkpoint.SourceEventCount != 13 || len(checkpoint.RepositoryEvidence) != 2 || checkpoint.RepositoryEvidence[0].Path != "AGENTS.md" || checkpoint.RepositoryEvidence[1].Path != "organization/host.go" {
		t.Fatalf("recovery-chain checkpoint = %#v", checkpoint)
	}
}

func TestClientInjectsOneDeterministicCheckpointPerCompaction(t *testing.T) {
	brief, digest := openHandsTestBrief(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	events := []map[string]any{
		event("evt-user", "MessageEvent", "user", string(mustJSON(brief))),
		actionEventWithPath("view", "file_editor", "view", "/workspace/organization/host.go"),
		observationEvent("view-result", "file_editor", false, 0),
	}
	state := &progressGuardServerState{prompt: string(mustJSON(brief)), workspace: workspace, events: events, condenserRuns: 1}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalRunning || state.interruptCalls != 1 || state.correctionCalls != 1 || !strings.HasPrefix(state.correctionText, compactionCheckpointPrefix+"1\n") || !strings.Contains(state.correctionText, `"inspected_paths":["/workspace/organization/host.go"]`) {
		t.Fatalf("observation=%#v err=%v interrupts=%d checkpoints=%d text=%q", observation, err, state.interruptCalls, state.correctionCalls, state.correctionText)
	}
	observation, err = client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalRunning || state.interruptCalls != 1 || state.correctionCalls != 1 {
		t.Fatalf("duplicate checkpoint observation=%#v err=%v interrupts=%d checkpoints=%d", observation, err, state.interruptCalls, state.correctionCalls)
	}
}

func TestClientResumesPausedCompactionOnlyAfterCheckpoint(t *testing.T) {
	brief, digest := openHandsTestBrief(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	events := []map[string]any{
		event("evt-user", "MessageEvent", "user", string(mustJSON(brief))),
		actionEventWithPath("view", "file_editor", "view", "/workspace/organization/host.go"),
		observationEvent("view-result", "file_editor", false, 0),
		{"id": "condensation", "kind": "Condensation", "source": "environment", "timestamp": "2026-08-31T12:00:02Z", "summary": "repository evidence retained"},
	}
	state := &progressGuardServerState{prompt: string(mustJSON(brief)), workspace: workspace, events: events, condenserRuns: 1, terminal: true}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalRunning || state.interruptCalls != 0 || state.correctionCalls != 1 || !strings.HasPrefix(state.correctionText, compactionCheckpointPrefix+"1\n") {
		t.Fatalf("observation=%#v err=%v interrupts=%d checkpoints=%d text=%q", observation, err, state.interruptCalls, state.correctionCalls, state.correctionText)
	}
}

func TestClientDoesNotResumeExplicitPauseAfterCompaction(t *testing.T) {
	brief, digest := openHandsTestBrief(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	events := []map[string]any{
		event("evt-user", "MessageEvent", "user", string(mustJSON(brief))),
		{"id": "condensation", "kind": "Condensation", "source": "environment", "timestamp": "2026-08-31T12:00:02Z", "summary": "repository evidence retained"},
		{"id": "operator-pause", "kind": "InterruptEvent", "source": "user", "timestamp": "2026-08-31T12:00:03Z"},
	}
	state := &progressGuardServerState{prompt: string(mustJSON(brief)), workspace: workspace, events: events, condenserRuns: 1, terminal: true}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalRunning || state.interruptCalls != 0 || state.correctionCalls != 0 {
		t.Fatalf("observation=%#v err=%v interrupts=%d checkpoints=%d", observation, err, state.interruptCalls, state.correctionCalls)
	}
}

func TestClientCorrectsOneCompoundShellActionInsideTheInvocation(t *testing.T) {
	brief, digest := openHandsTestBrief(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	events := []map[string]any{
		event("evt-user", "MessageEvent", "user", string(mustJSON(brief))),
		actionEvent("compound-action", "terminal", "rg -n name . | head"),
		observationEvent("compound-observation", "terminal", false, 0),
	}
	state := &progressGuardServerState{prompt: string(mustJSON(brief)), workspace: workspace, events: events}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalRunning || state.interruptCalls != 1 || state.correctionCalls != 1 {
		t.Fatalf("observation=%#v err=%v interrupts=%d corrections=%d", observation, err, state.interruptCalls, state.correctionCalls)
	}
	if !strings.Contains(state.correctionText, shellDisciplineCorrectionPrefix+"compound-action") || !strings.Contains(state.correctionText, "exactly one command") {
		t.Fatalf("correction text = %q", state.correctionText)
	}

	state.events = append(state.events, actionEvent("second-compound-action", "terminal", "rg --files && pwd"))
	observation, err = client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalFailed || !observation.Retryable || state.correctionCalls != 1 || !strings.Contains(string(observation.Output), "REPEATED_SHELL_DISCIPLINE_VIOLATION") {
		t.Fatalf("repeated observation=%#v err=%v corrections=%d", observation, err, state.correctionCalls)
	}
}

func TestClientCorrectsLaterShellDisciplineIncidentAfterCompliantProgress(t *testing.T) {
	brief, digest := openHandsTestBrief(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	events := []map[string]any{
		event("evt-user", "MessageEvent", "user", string(mustJSON(brief))),
		actionEvent("first-compound-action", "terminal", "rg -n name . | head"),
		observationEvent("first-compound-observation", "terminal", false, 0),
		event("first-correction", "MessageEvent", "user", shellDisciplineCorrectionPrefix+"first-compound-action\ncorrect it"),
		actionEvent("compliant-action", "terminal", "git status"),
		observationEvent("compliant-observation", "terminal", false, 0),
		actionEvent("later-compound-action", "terminal", "rg --files && pwd"),
	}
	state := &progressGuardServerState{prompt: string(mustJSON(brief)), workspace: workspace, events: events}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalRunning || state.interruptCalls != 1 || state.correctionCalls != 1 || !strings.Contains(state.correctionText, shellDisciplineCorrectionPrefix+"later-compound-action") {
		t.Fatalf("observation=%#v err=%v interrupts=%d corrections=%d text=%q", observation, err, state.interruptCalls, state.correctionCalls, state.correctionText)
	}
}

func TestClientCorrectsGitWorkingDirectoryOverrideAndRequiresAgentsGrounding(t *testing.T) {
	brief, _ := openHandsTestBrief(t)
	brief.ExecutionGuidance = []string{"Read and follow AGENTS.md before taking repository actions."}
	encoded := mustJSON(brief)
	hash := sha256.Sum256(encoded)
	digest := kernel.Digest(hex.EncodeToString(hash[:]))
	workspace := filepath.Join(t.TempDir(), "workspace")
	events := []map[string]any{
		event("evt-user", "MessageEvent", "user", string(mustJSON(brief))),
		actionEvent("git-c-action", "terminal", "git -C /workspace status"),
		observationEvent("git-c-observation", "terminal", false, 0),
	}
	state := &progressGuardServerState{prompt: string(mustJSON(brief)), workspace: workspace, events: events}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalRunning || state.interruptCalls != 1 || state.correctionCalls != 1 || !strings.Contains(state.correctionText, "git -C") {
		t.Fatalf("shell observation=%#v err=%v interrupts=%d corrections=%d text=%q", observation, err, state.interruptCalls, state.correctionCalls, state.correctionText)
	}
	state.events = append(state.events,
		actionEventWithPath("agents-read", "file_editor", "view", filepath.Join(workspace, "AGENTS.md")),
		observationEventWithText("agents-observation", "file_editor", false, 0, "# repository instructions"),
		actionEvent("status-after-grounding", "terminal", "git status"),
	)
	observation, err = client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalRunning || state.interruptCalls != 1 || state.correctionCalls != 1 {
		t.Fatalf("grounded observation=%#v err=%v interrupts=%d corrections=%d", observation, err, state.interruptCalls, state.correctionCalls)
	}
}

func TestClientCorrectsRepositoryActionBeforeAgentsGroundingOnce(t *testing.T) {
	brief, _ := openHandsTestBrief(t)
	brief.ExecutionGuidance = []string{"Read and follow AGENTS.md before taking repository actions."}
	encoded := mustJSON(brief)
	hash := sha256.Sum256(encoded)
	digest := kernel.Digest(hex.EncodeToString(hash[:]))
	workspace := filepath.Join(t.TempDir(), "workspace")
	events := []map[string]any{
		event("evt-user", "MessageEvent", "user", string(mustJSON(brief))),
		actionEvent("source-before-grounding", "terminal", "sed -n '1,80p' organization/host.go"),
		observationEvent("source-observation", "terminal", false, 0),
	}
	state := &progressGuardServerState{prompt: string(mustJSON(brief)), workspace: workspace, events: events}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalRunning || state.interruptCalls != 1 || state.correctionCalls != 1 || !strings.HasPrefix(state.correctionText, repositoryGroundingCorrectionPrefix+"source-before-grounding\n") {
		t.Fatalf("first observation=%#v err=%v interrupts=%d corrections=%d text=%q", observation, err, state.interruptCalls, state.correctionCalls, state.correctionText)
	}
	state.events = append(state.events, actionEvent("second-source-before-grounding", "terminal", "sed -n '1,80p' adapters/mongo/store.go"))
	observation, err = client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalFailed || observation.Retryable || state.interruptCalls != 2 || state.correctionCalls != 1 || !strings.Contains(string(observation.Output), "REPEATED_REPOSITORY_ACTION_BEFORE_AGENTS_GROUNDING") {
		t.Fatalf("second observation=%#v err=%v interrupts=%d corrections=%d", observation, err, state.interruptCalls, state.correctionCalls)
	}
}

func TestClientAllowsAcceptedContractReadButCorrectsMutationOnce(t *testing.T) {
	brief, _ := openHandsTestBrief(t)
	brief.RoleGrounding.Permissions = []string{"repository.edit"}
	brief.ExecutionGuidance = []string{
		"Read and follow AGENTS.md before taking repository actions.",
		"Read accepted CONTRACTS packages only when relevant. Never modify accepted CONTRACTS packages.",
	}
	encoded := mustJSON(brief)
	hash := sha256.Sum256(encoded)
	digest := kernel.Digest(hex.EncodeToString(hash[:]))
	workspace := filepath.Join(t.TempDir(), "workspace")
	events := []map[string]any{
		event("evt-user", "MessageEvent", "user", string(encoded)),
		actionEventWithPath("agents-read", "file_editor", "view", filepath.Join(workspace, "AGENTS.md")),
		observationEventWithText("agents-observation", "file_editor", false, 0, "# repository instructions"),
		actionEvent("contract-list", "terminal", "ls CONTRACTS"),
	}
	state := &progressGuardServerState{prompt: string(encoded), workspace: workspace, events: events}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalRunning || state.interruptCalls != 0 || state.correctionCalls != 0 {
		t.Fatalf("read-only observation=%#v err=%v interrupts=%d corrections=%d", observation, err, state.interruptCalls, state.correctionCalls)
	}

	state.events = append(state.events, actionEventWithPath("contract-edit", "file_editor", "str_replace", filepath.Join(workspace, "CONTRACTS/tekroo.kernel.contracts/0.10.0/manifest.json")))
	observation, err = client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalRunning || state.interruptCalls != 1 || state.correctionCalls != 1 || !strings.HasPrefix(state.correctionText, repositoryScopeCorrectionPrefix+"contract-edit\n") {
		t.Fatalf("first mutation observation=%#v err=%v interrupts=%d corrections=%d text=%q", observation, err, state.interruptCalls, state.correctionCalls, state.correctionText)
	}

	state.events = append(state.events, actionEventWithPath("contract-edit-again", "file_editor", "str_replace", filepath.Join(workspace, "CONTRACTS/tekroo.kernel.contracts/0.10.0/manifest.json")))
	observation, err = client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalFailed || observation.Retryable || state.interruptCalls != 2 || state.correctionCalls != 1 || !strings.Contains(string(observation.Output), "REPEATED_ACCEPTED_CONTRACT_SCOPE_VIOLATION") {
		t.Fatalf("second observation=%#v err=%v interrupts=%d corrections=%d", observation, err, state.interruptCalls, state.correctionCalls)
	}
}

func TestAcceptedContractInspectionIgnoresResultProse(t *testing.T) {
	result := rawEvent{
		Kind:          "ActionEvent",
		Source:        "agent",
		ToolName:      "finish",
		ActionPayload: json.RawMessage(`{"kind":"FinishAction","message":"The accepted CONTRACTS package remains unchanged."}`),
	}
	if acceptedContractInspectionAction(result) {
		t.Fatal("finish result prose was misclassified as repository inspection")
	}

	inspection := rawEvent{
		Kind:       "ActionEvent",
		Source:     "agent",
		ToolName:   "repository_view",
		ActionPath: "CONTRACTS/tekroo.kernel.contracts/0.10.0/manifest.json",
	}
	if !acceptedContractInspectionAction(inspection) {
		t.Fatal("actual contract repository inspection was not detected")
	}
}

func TestRepositoryGroundingRequiresSuccessfulAgentsReadBeforeOtherActions(t *testing.T) {
	events := []rawEvent{
		{Kind: "MessageEvent", Source: "user"},
		{ID: "agents-read", Kind: "ActionEvent", Source: "agent", ToolName: "file_editor", ToolCallID: "agents-call", ActionCommand: "view", ActionPath: "/workspace/AGENTS.md"},
		{Kind: "ObservationEvent", ToolName: "file_editor", ToolCallID: "agents-call", Text: "# instructions"},
		{ID: "source", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ActionCommand: "sed -n '1,80p' organization/host.go"},
	}
	if violation, found := repositoryGroundingViolation(events, 0, false); found {
		t.Fatalf("unexpected violation: %+v", violation)
	}
	events[2].Text = ""
	violation, found := repositoryGroundingViolation(events, 0, false)
	if !found || violation.ID != "source" {
		t.Fatalf("violation=%+v found=%t", violation, found)
	}
}

func TestRepositoryGroundingAllowsWorkspaceOrientationBeforeAgentsRead(t *testing.T) {
	events := []rawEvent{
		{Kind: "MessageEvent", Source: "user"},
		{ID: "pwd", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ActionCommand: "pwd"},
		{ID: "status", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ActionCommand: "git status --short"},
		{ID: "list", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ActionCommand: "ls -la /workspace"},
		{ID: "root", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ActionCommand: "git rev-parse --show-toplevel"},
		{ID: "agents-read", Kind: "ActionEvent", Source: "agent", ToolName: "file_editor", ToolCallID: "agents-call", ActionCommand: "view", ActionPath: "/workspace/AGENTS.md"},
		{Kind: "ObservationEvent", ToolName: "file_editor", ToolCallID: "agents-call", Text: "# instructions"},
		{ID: "source", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ActionCommand: "git rev-parse HEAD"},
	}
	if violation, found := repositoryGroundingViolation(events, 0, false); found {
		t.Fatalf("unexpected violation: %+v", violation)
	}
}

func TestRepositoryGroundingAcceptsSuccessfulReadFromRecoveryCheckpoint(t *testing.T) {
	events := []rawEvent{
		{Kind: "MessageEvent", Source: "user"},
		{ID: "focused-source", Kind: "ActionEvent", Source: "agent", ToolName: "repository_view", ToolCallID: "source-call", ActionPath: "adapters/mongo/retention_policy_store.go"},
	}
	if violation, found := repositoryGroundingViolation(events, 0, true); found {
		t.Fatalf("retained recovery grounding was ignored: %+v", violation)
	}
}

func TestRepositoryGroundingSurvivesLaterShellCorrection(t *testing.T) {
	events := []rawEvent{
		{Kind: "MessageEvent", Source: "user"},
		{ID: "agents-read", Kind: "ActionEvent", Source: "agent", ToolName: "file_editor", ToolCallID: "agents-call", ActionCommand: "view", ActionPath: "/workspace/AGENTS.md"},
		{Kind: "ObservationEvent", ToolName: "file_editor", ToolCallID: "agents-call", Text: "# instructions"},
		{ID: "compound", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ToolCallID: "compound-call", ActionCommand: "rg name . | head"},
		{Kind: "MessageEvent", Source: "user", Text: shellDisciplineCorrectionPrefix + "compound"},
		{ID: "search", Kind: "ActionEvent", Source: "agent", ToolName: "repository_search", ToolCallID: "search-call", ActionPayload: json.RawMessage(`{"pattern":"ActorFQN","path":"kernel"}`)},
	}
	if violation, found := repositoryGroundingViolation(events, 0, false); found {
		t.Fatalf("unexpected violation after retained grounding: %+v", violation)
	}
}

func TestRepositoryGroundingDoesNotRediscoverCorrectedAction(t *testing.T) {
	events := []rawEvent{
		{Kind: "MessageEvent", Source: "user"},
		{ID: "source", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ToolCallID: "source-call", ActionCommand: "sed -n '1,80p' organization/host.go"},
		{Kind: "MessageEvent", Source: "user", Text: repositoryGroundingCorrectionPrefix + "source"},
	}
	if violation, found := repositoryGroundingViolation(events, 0, false); found {
		t.Fatalf("corrected action was rediscovered: %+v", violation)
	}

	events = append(events, rawEvent{ID: "second-source", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ToolCallID: "second-source-call", ActionCommand: "sed -n '1,80p' adapters/mongo/store.go"})
	violation, found := repositoryGroundingViolation(events, 0, false)
	if !found || violation.ID != "second-source" {
		t.Fatalf("violation=%+v found=%t", violation, found)
	}
}

func TestRepositoryGroundingPreservesSuccessfulReadAcrossItsOwnCorrection(t *testing.T) {
	events := []rawEvent{
		{Kind: "MessageEvent", Source: "user"},
		{ID: "agents-read", Kind: "ActionEvent", Source: "agent", ToolName: "file_editor", ToolCallID: "agents-call", ActionCommand: "view", ActionPath: "/workspace/AGENTS.md"},
		{ID: "premature-list", Kind: "ActionEvent", Source: "agent", ToolName: "repository_view", ToolCallID: "list-call", ActionPath: "."},
		{Kind: "ObservationEvent", ToolName: "file_editor", ToolCallID: "agents-call", Text: "# repository instructions"},
		{Kind: "ObservationEvent", ToolName: "repository_view", ToolCallID: "list-call", Text: "organization/"},
		{Kind: "MessageEvent", Source: "user", Text: repositoryGroundingCorrectionPrefix + "premature-list"},
		{ID: "focused-search", Kind: "ActionEvent", Source: "agent", ToolName: "repository_search", ToolCallID: "search-call", ActionPayload: json.RawMessage(`{"pattern":"ActorFQN","path":"organization"}`)},
	}
	if violation, found := repositoryGroundingViolation(events, 0, false); found {
		t.Fatalf("successful AGENTS.md read was discarded by its correction: %+v", violation)
	}
}

func TestClientRetainsGroundingAcrossIndependentPolicyCorrections(t *testing.T) {
	brief, _ := openHandsTestBrief(t)
	brief.ExecutionGuidance = []string{"Read and follow AGENTS.md before taking repository actions."}
	encoded := mustJSON(brief)
	hash := sha256.Sum256(encoded)
	digest := kernel.Digest(hex.EncodeToString(hash[:]))
	workspace := filepath.Join(t.TempDir(), "workspace")
	events := []map[string]any{
		event("evt-user", "MessageEvent", "user", string(encoded)),
		actionEvent("status-before-grounding", "terminal", "git status"),
		event("grounding-correction", "MessageEvent", "user", repositoryGroundingCorrectionPrefix+"status-before-grounding"),
		actionEventWithPath("agents-read", "file_editor", "view", filepath.Join(workspace, "AGENTS.md")),
		observationEventWithText("agents-observation", "file_editor", false, 0, "# repository instructions"),
		actionEvent("status-after-grounding", "terminal", "git status"),
		observationEvent("status-observation", "terminal", false, 0),
		actionEvent("compound-after-progress", "terminal", "rg -n ActorFQN kernel/*.go | head"),
		event("shell-correction", "MessageEvent", "user", shellDisciplineCorrectionPrefix+"compound-after-progress"),
		repositorySearchActionEvent("focused-search", "focused-call", "ActorFQN", "kernel"),
		repositorySearchObservationEvent("focused-observation", "focused-call", false),
	}
	state := &progressGuardServerState{prompt: string(encoded), workspace: workspace, events: events}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalRunning || state.interruptCalls != 0 || state.correctionCalls != 0 {
		t.Fatalf("observation=%#v err=%v interrupts=%d corrections=%d", observation, err, state.interruptCalls, state.correctionCalls)
	}
}

func TestClientOneCorrectionCoversConcurrentCompoundShellActions(t *testing.T) {
	brief, digest := openHandsTestBrief(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	events := []map[string]any{
		event("evt-user", "MessageEvent", "user", string(mustJSON(brief))),
		actionEvent("first-compound-action", "terminal", "rg -n name . | head"),
		actionEvent("second-compound-action", "terminal", "rg -n alias . | head"),
	}
	state := &progressGuardServerState{prompt: string(mustJSON(brief)), workspace: workspace, events: events}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalRunning || state.interruptCalls != 1 || state.correctionCalls != 1 {
		t.Fatalf("first observation=%#v err=%v interrupts=%d corrections=%d", observation, err, state.interruptCalls, state.correctionCalls)
	}
	observation, err = client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalRunning || state.interruptCalls != 1 || state.correctionCalls != 1 {
		t.Fatalf("second observation=%#v err=%v interrupts=%d corrections=%d", observation, err, state.interruptCalls, state.correctionCalls)
	}

	state.events = append(state.events, actionEvent("post-correction-compound-action", "terminal", "rg --files && pwd"))
	observation, err = client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalFailed || !observation.Retryable || state.correctionCalls != 1 || !strings.Contains(string(observation.Output), "REPEATED_SHELL_DISCIPLINE_VIOLATION") {
		t.Fatalf("post-correction observation=%#v err=%v corrections=%d", observation, err, state.correctionCalls)
	}
}

func TestClientCorrectsExactRepeatedToolActionThenStopsImmediateRepeat(t *testing.T) {
	brief, digest := openHandsTestBrief(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	events := []map[string]any{
		event("evt-user", "MessageEvent", "user", string(mustJSON(brief))),
		actionEvent("first-search", "terminal", `rg -n "type.*interface" organization/host.go`),
		observationEventWithText("first-search-observation", "terminal", false, 0, "organization/host.go:24:type RoleStore interface"),
		actionEvent("repeated-search", "terminal", `rg -n "type.*interface" organization/host.go`),
		observationEventWithText("repeated-search-observation", "terminal", false, 0, "organization/host.go:24:type RoleStore interface"),
	}
	state := &progressGuardServerState{prompt: string(mustJSON(brief)), workspace: workspace, events: events}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalRunning || state.interruptCalls != 1 || state.correctionCalls != 1 || !strings.HasPrefix(state.correctionText, repositoryProgressCorrectionPrefix+"repeated-search\n") {
		t.Fatalf("observation=%#v err=%v interrupts=%d corrections=%d", observation, err, state.interruptCalls, state.correctionCalls)
	}
	if strings.Contains(strings.ToLower(state.correctionText), "implement") {
		t.Fatalf("role-neutral progress correction assigned implementation work: %q", state.correctionText)
	}

	state.events = append(state.events,
		actionEvent("post-correction-search", "terminal", `rg -n "type.*interface" organization/host.go`),
		observationEventWithText("post-correction-observation", "terminal", false, 0, "organization/host.go:24:type RoleStore interface"),
		actionEvent("post-correction-repeat", "terminal", `rg -n "type.*interface" organization/host.go`),
		observationEventWithText("post-correction-repeat-observation", "terminal", false, 0, "organization/host.go:24:type RoleStore interface"),
	)
	observation, err = client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalFailed || observation.Retryable || state.interruptCalls != 2 || state.correctionCalls != 1 || !strings.Contains(string(observation.Output), "REPEATED_CAPABILITY_MISMATCH_REPOSITORY_NO_PROGRESS") {
		t.Fatalf("repeat observation=%#v err=%v interrupts=%d corrections=%d", observation, err, state.interruptCalls, state.correctionCalls)
	}
}

func TestRepositoryProgressCorrectionAllowsLaterIncidentAfterSubstantiveProgress(t *testing.T) {
	exitSuccess := 0
	events := []rawEvent{
		{Kind: "MessageEvent", Source: "user"},
		{Kind: "MessageEvent", Source: "user", Text: repositoryProgressCorrectionPrefix + "prior"},
		{Kind: "ActionEvent", Source: "agent", ToolName: "repository_view", ToolCallID: "read-call", ActionPath: "organization/host.go"},
		{Kind: "ObservationEvent", ToolName: "repository_view", ToolCallID: "read-call", Text: "package organization", ObservationExitCode: &exitSuccess},
		{ID: "later-repeat", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ActionCommand: "ls organization"},
	}
	if !repositoryProgressCorrectionAllowed(events, 0, events[len(events)-1]) {
		t.Fatal("later isolated incident was rejected after successful source inspection")
	}

	events[3].Text = ""
	if repositoryProgressCorrectionAllowed(events, 0, events[len(events)-1]) {
		t.Fatal("empty source observation incorrectly reset the correction episode")
	}
}

func TestClientAllowsNarrowerRepositorySearches(t *testing.T) {
	brief, digest := openHandsTestBrief(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	events := []map[string]any{
		event("evt-user", "MessageEvent", "user", string(mustJSON(brief))),
		repositorySearchActionEvent("root-search", "root-call", "Widget|Label", ""),
		repositorySearchObservationEvent("root-observation", "root-call", false),
		repositorySearchActionEvent("descendant-search", "descendant-call", "Label", "adapters/operationalruntime"),
		repositorySearchObservationEvent("descendant-observation", "descendant-call", false),
	}
	state := &progressGuardServerState{prompt: string(mustJSON(brief)), workspace: workspace, events: events}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalRunning || state.interruptCalls != 0 || state.correctionCalls != 0 {
		t.Fatalf("observation=%#v err=%v interrupts=%d corrections=%d text=%q", observation, err, state.interruptCalls, state.correctionCalls, state.correctionText)
	}
}

func TestRepositorySearchLoopAllowsDistinctSearchAndInspectionActions(t *testing.T) {
	events := []rawEvent{
		{Kind: "MessageEvent", Source: "user"},
		{ID: "empty-search", Kind: "ActionEvent", Source: "agent", ToolName: "repository_search", ActionPayload: json.RawMessage(`{"kind":"RepositorySearchAction","pattern":"Actor","path":"missing"}`), ActionPath: "missing"},
		{Kind: "ObservationEvent", ToolName: "repository_search", ObservationExitCode: intPointer(1)},
		{ID: "different-search", Kind: "ActionEvent", Source: "agent", ToolName: "repository_search", ActionPayload: json.RawMessage(`{"kind":"RepositorySearchAction","pattern":"Actor","path":"organization"}`), ActionPath: "organization"},
		{Kind: "ObservationEvent", ToolName: "repository_search", Text: "organization/host.go:Actor", ObservationExitCode: intPointer(0)},
		{ID: "inspection", Kind: "ActionEvent", Source: "agent", ToolName: "repository_view", ActionPayload: json.RawMessage(`{"kind":"RepositoryViewAction","path":"organization/host.go"}`), ActionPath: "organization/host.go"},
		{Kind: "ObservationEvent", ToolName: "repository_view", Text: "package organization", ObservationExitCode: intPointer(0)},
		{ID: "third-search", Kind: "ActionEvent", Source: "agent", ToolName: "repository_search", ActionPayload: json.RawMessage(`{"kind":"RepositorySearchAction","pattern":"Actor","path":"adapters"}`), ActionPath: "adapters"},
	}
	if violation, found := repositorySearchLoopViolation(events, 0); found {
		t.Fatalf("unexpected violation: %+v", violation)
	}
}

func TestRepositorySearchLoopRejectsExactRepeatedCustomToolAction(t *testing.T) {
	events := []rawEvent{
		{Kind: "MessageEvent", Source: "user"},
		{ID: "first-search", Kind: "ActionEvent", Source: "agent", ToolName: "repository_search", ActionPayload: json.RawMessage(`{"kind":"RepositorySearchAction","path":"organization","pattern":"Actor"}`)},
		{Kind: "ObservationEvent", ToolName: "repository_search", Text: "organization/host.go:Actor", ObservationExitCode: intPointer(0)},
		{ID: "repeated-search", Kind: "ActionEvent", Source: "agent", ToolName: "repository_search", ActionPayload: json.RawMessage(`{"pattern":"Actor","kind":"RepositorySearchAction","path":"organization"}`)},
		{Kind: "ObservationEvent", ToolName: "repository_search", Text: "organization/host.go:Actor", ObservationExitCode: intPointer(0)},
	}
	violation, found := repositorySearchLoopViolation(events, 0)
	if !found || violation.ID != "repeated-search" {
		t.Fatalf("violation=%+v found=%t", violation, found)
	}
}

func TestRepositorySearchLoopRejectsExactRepeatedGrepAction(t *testing.T) {
	command := `grep -ri "RetentionPolicy" CONTRACTS/tekroo.kernel.contracts/0.10.0`
	events := []rawEvent{
		{Kind: "MessageEvent", Source: "user"},
		{ID: "first-grep", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ActionCommand: command},
		{Kind: "ObservationEvent", ToolName: "terminal", Text: "", ObservationExitCode: intPointer(1)},
		{ID: "repeated-grep", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ActionCommand: command},
		{Kind: "ObservationEvent", ToolName: "terminal", Text: "", ObservationExitCode: intPointer(1)},
	}
	violation, found := repositorySearchLoopViolation(events, 0)
	if !found || violation.ID != "repeated-grep" {
		t.Fatalf("violation=%+v found=%t", violation, found)
	}
}

func TestRepositorySearchLoopAllowsSameActionWhenResultChanges(t *testing.T) {
	events := []rawEvent{
		{Kind: "MessageEvent", Source: "user"},
		{ID: "first-search", Kind: "ActionEvent", Source: "agent", ToolName: "repository_search", ActionPayload: json.RawMessage(`{"kind":"RepositorySearchAction","path":"organization","pattern":"Actor"}`)},
		{Kind: "ObservationEvent", ToolName: "repository_search", Text: "organization/host.go:Actor", ObservationExitCode: intPointer(0)},
		{ID: "search-after-state-change", Kind: "ActionEvent", Source: "agent", ToolName: "repository_search", ActionPayload: json.RawMessage(`{"kind":"RepositorySearchAction","path":"organization","pattern":"Actor"}`)},
		{Kind: "ObservationEvent", ToolName: "repository_search", Text: "organization/host.go:Actor\norganization/manifest.go:Actor", ObservationExitCode: intPointer(0)},
	}
	if violation, found := repositorySearchLoopViolation(events, 0); found {
		t.Fatalf("changed result was misclassified as no progress: %+v", violation)
	}
}

func TestRepositorySearchLoopAllowsDifferentFileRanges(t *testing.T) {
	events := []rawEvent{
		{Kind: "MessageEvent", Source: "user"},
		{ID: "first-range", Kind: "ActionEvent", Source: "agent", ToolName: "repository_view", ActionPayload: json.RawMessage(`{"kind":"RepositoryViewAction","path":"adapters/operationalruntime/production.go","view_range":[800,900]}`)},
		{Kind: "ObservationEvent", ToolName: "repository_view", Text: "first range", ObservationExitCode: intPointer(0)},
		{ID: "second-range", Kind: "ActionEvent", Source: "agent", ToolName: "repository_view", ActionPayload: json.RawMessage(`{"kind":"RepositoryViewAction","path":"adapters/operationalruntime/production.go","view_range":[1040,1120]}`)},
		{Kind: "ObservationEvent", ToolName: "repository_view", Text: "second range", ObservationExitCode: intPointer(0)},
	}
	if violation, found := repositorySearchLoopViolation(events, 0); found {
		t.Fatalf("different file range was misclassified as no progress: %+v", violation)
	}
}

func TestRepositorySearchLoopAllowsDistinctDiscoveryActions(t *testing.T) {
	events := []rawEvent{
		{Kind: "MessageEvent", Source: "user"},
		{ID: "root-glob", Kind: "ActionEvent", Source: "agent", ToolName: "glob", ActionPayload: json.RawMessage(`{"kind":"GlobAction","pattern":"**/*.go"}`)},
		{Kind: "ObservationEvent", ToolName: "glob", Text: "organization/host.go"},
		{ID: "organization-glob", Kind: "ActionEvent", Source: "agent", ToolName: "glob", ActionPayload: json.RawMessage(`{"kind":"GlobAction","pattern":"organization/*.go"}`)},
	}
	if violation, found := repositorySearchLoopViolation(events, 0); found {
		t.Fatalf("unexpected violation: %+v", violation)
	}
}

func TestRepositorySearchLoopResetsAfterSuccessfulMutation(t *testing.T) {
	events := []rawEvent{
		{Kind: "MessageEvent", Source: "user"},
		{ID: "first-search", Kind: "ActionEvent", Source: "agent", ToolName: "repository_search", ActionPayload: json.RawMessage(`{"kind":"RepositorySearchAction","pattern":"Actor"}`)},
		{Kind: "ObservationEvent", ToolName: "repository_search", Text: "organization/host.go:Actor", ObservationExitCode: intPointer(0)},
		{ID: "edit", Kind: "ActionEvent", Source: "agent", ToolName: "file_editor", ToolCallID: "edit-call", ActionCommand: "str_replace", ActionPath: "organization/host.go"},
		{Kind: "ObservationEvent", ToolName: "file_editor", ToolCallID: "edit-call", ObservationExitCode: intPointer(0)},
		{ID: "search-after-edit", Kind: "ActionEvent", Source: "agent", ToolName: "repository_search", ActionPayload: json.RawMessage(`{"kind":"RepositorySearchAction","pattern":"Actor"}`)},
	}
	if violation, found := repositorySearchLoopViolation(events, 0); found {
		t.Fatalf("unexpected violation after mutation: %+v", violation)
	}
}

func TestRepositorySearchLoopResetsAfterGroundingCorrection(t *testing.T) {
	events := []rawEvent{
		{Kind: "MessageEvent", Source: "user"},
		{ID: "rejected-list", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ActionCommand: "ls organization/"},
		{Kind: "ObservationEvent", ToolName: "terminal", ObservationExitCode: intPointer(0)},
		{Kind: "MessageEvent", Source: "user", Text: repositoryGroundingCorrectionPrefix + "rejected-list"},
		{ID: "grounded-list", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ActionCommand: "ls organization/"},
	}
	if violation, found := repositorySearchLoopViolation(events, 0); found {
		t.Fatalf("unexpected violation after grounding correction: %+v", violation)
	}
}

func TestRepositoryProgressViolationIsCoveredByLaterScopeCorrection(t *testing.T) {
	events := []rawEvent{
		{Kind: "MessageEvent", Source: "user"},
		{ID: "first-search", Kind: "ActionEvent", Source: "agent", ToolName: "repository_search", ActionPayload: json.RawMessage(`{"kind":"RepositorySearchAction","pattern":"WidgetStore"}`)},
		{Kind: "ObservationEvent", ToolName: "repository_search", Text: "organization/widget.go:WidgetStore", ObservationExitCode: intPointer(0)},
		{ID: "repeated-search", Kind: "ActionEvent", Source: "agent", ToolName: "repository_search", ActionPayload: json.RawMessage(`{"kind":"RepositorySearchAction","pattern":"WidgetStore"}`)},
		{ID: "contract-search", Kind: "ActionEvent", Source: "agent", ToolName: "repository_search", ActionPayload: json.RawMessage(`{"kind":"RepositorySearchAction","pattern":"WidgetStore","path":"CONTRACTS"}`)},
		{Kind: "MessageEvent", Source: "user", Text: repositoryScopeCorrectionPrefix + "contract-search"},
	}
	if violation, found := repositorySearchLoopViolation(events, 0); found {
		t.Fatalf("same-response progress violation was not covered by scope correction: %+v", violation)
	}
}

func TestRepositorySearchLoopAllowsRepeatedValidationAfterProgressCorrection(t *testing.T) {
	events := []rawEvent{
		{Kind: "MessageEvent", Source: "user"},
		{ID: "first-test", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ActionCommand: "go test ./organization/ -count=1"},
		{Kind: "ObservationEvent", ToolName: "terminal", ObservationExitCode: intPointer(0)},
		{ID: "repeated-test", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ActionCommand: "go test ./organization/ -count=1"},
		{Kind: "MessageEvent", Source: "user", Text: repositoryProgressCorrectionPrefix + "repeated-test"},
		{ID: "repeat-after-correction", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ActionCommand: "go test ./organization/ -count=1"},
	}
	if violation, found := repositorySearchLoopViolation(events, 0); found {
		t.Fatalf("repeated validation was misclassified as a repository discovery loop: %+v", violation)
	}
}

func TestRepositorySearchLoopResetsAfterSuccessfulGitStateChange(t *testing.T) {
	events := []rawEvent{
		{Kind: "MessageEvent", Source: "user"},
		{ID: "first-status", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ToolCallID: "status-1", ActionCommand: "git status"},
		{Kind: "ObservationEvent", ToolName: "terminal", ToolCallID: "status-1", ObservationExitCode: intPointer(0)},
		{ID: "stage", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ToolCallID: "stage-1", ActionCommand: "git add calculator/calculator.go"},
		{Kind: "ObservationEvent", ToolName: "terminal", ToolCallID: "stage-1", ObservationExitCode: intPointer(0)},
		{ID: "second-status", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ToolCallID: "status-2", ActionCommand: "git status"},
	}
	if violation, found := repositorySearchLoopViolation(events, 0); found {
		t.Fatalf("unexpected violation after git state change: %+v", violation)
	}
}

func TestMutationActionClassifiesGitStateChangesOnly(t *testing.T) {
	tests := []struct {
		command string
		want    bool
	}{
		{command: "git add calculator/calculator.go", want: true},
		{command: "git commit -m repair", want: true},
		{command: "git status", want: false},
		{command: "git diff --check", want: false},
		{command: "git log -1", want: false},
		{command: "ls adapters/mcp adapters/operatortools adapters/operatorhttp", want: false},
		{command: "rg 'cp ' adapters/mcp", want: false},
		{command: `grep -rn "RetentionPolicy" CONTRACTS 2>/dev/null`, want: false},
		{command: `grep ">" organization/*.go`, want: false},
		{command: "go test ./... 2>&1", want: false},
		{command: "go test ./... >/dev/null", want: false},
		{command: "go test ./... > test.log", want: true},
		{command: "cp source target", want: true},
		{command: "git show HEAD:file.go > /tmp/base_file.go", want: false},
		{command: "go test ./... > /tmp/out.log", want: false},
		{command: "go test ./... > /var/tmp/out.log", want: false},
		{command: "mkdir -p /tmp/actornameprobe", want: false},
		{command: "touch /var/tmp/validator-probe", want: false},
		{command: "rm -f /tmp/validator-probe", want: false},
		{command: "cp /tmp/probe/go.mod /tmp/foldprobe/go.mod", want: false},
		{command: "cp organization/actor_name.go /tmp/actor_name.go", want: false},
		{command: "cp /tmp/actor_name.go organization/actor_name.go", want: true},
		{command: "mv /tmp/probe /tmp/probe-done", want: false},
		{command: "mv organization/actor_name.go /tmp/actor_name.go", want: true},
		{command: "tee /tmp/probe.txt", want: false},
		{command: "mkdir -p /tmp/../repository-write", want: true},
		{command: "mkdir -p scratch", want: true},
		{command: "rm -rf /tmp", want: true},
	}
	for _, test := range tests {
		if got := mutationAction(rawEvent{ToolName: "terminal", ActionCommand: test.command}); got != test.want {
			t.Fatalf("mutationAction(%q) = %t, want %t", test.command, got, test.want)
		}
	}
}

func TestReplanGroundingRequiresSuccessfulInstructionAndRepositoryReads(t *testing.T) {
	brief := application.ExecutionBrief{Purpose: kernel.PurposeReplan}
	events := []rawEvent{
		{Kind: "MessageEvent", Source: "user"},
		{Kind: "ActionEvent", Source: "agent", ToolName: "repository_view", ToolCallID: "agents", ActionPayload: json.RawMessage(`{"kind":"RepositoryViewAction","path":"AGENTS.md"}`), ActionPath: "AGENTS.md"},
		{Kind: "ObservationEvent", ToolName: "repository_view", ToolCallID: "agents", Text: "instructions"},
	}
	if reason := successfulRepositoryGroundingFailure(brief, events, 0); reason != "REPLAN_REPOSITORY_NOT_INSPECTED" {
		t.Fatalf("grounding failure = %q", reason)
	}
	events = append(events,
		rawEvent{Kind: "ActionEvent", Source: "agent", ToolName: "repository_view", ToolCallID: "source", ActionPayload: json.RawMessage(`{"kind":"RepositoryViewAction","path":"organization/manifest.go"}`), ActionPath: "organization/manifest.go"},
		rawEvent{Kind: "ObservationEvent", ToolName: "repository_view", ToolCallID: "source", Text: "package organization"},
	)
	if reason := successfulRepositoryGroundingFailure(brief, events, 0); reason != "" {
		t.Fatalf("grounded architect failure = %q", reason)
	}
}

func TestExplicitRecoveryGroundingAcceptsDigestBoundCheckpointReads(t *testing.T) {
	brief, _ := explicitRecoveryTestBrief(t)
	brief.Purpose = kernel.PurposeReplan
	brief.RoleGrounding.Permissions = []string{"repository.read"}
	encoded := mustJSON(brief)
	briefHash := sha256.Sum256(encoded)
	checkpoint := progressCheckpoint{
		SchemaVersion:       "tekroo.teams.execution-progress-checkpoint/1.2.0",
		InvocationID:        brief.InvocationID,
		PriorInvocationID:   brief.RetryOfInvocationID,
		Source:              "OPENHANDS_EVENT_JOURNAL",
		SourceJournalSHA256: kernel.Digest(hex.EncodeToString(briefHash[:])),
		AuthoritativeExecution: checkpointExecutionAuthority{
			ExecutionBriefSHA256: kernel.Digest(hex.EncodeToString(briefHash[:])),
		},
		RepositoryEvidence: []checkpointAction{
			{Tool: "repository_view", Path: "AGENTS.md", Outcome: "SUCCEEDED", ObservationSHA256: digest('3')},
			{Tool: "repository_view", Path: "organization/manifest.go", Outcome: "SUCCEEDED", ObservationSHA256: digest('4')},
		},
	}
	prompt := mustJSON(map[string]any{"recovery_checkpoint": checkpoint})
	events := []rawEvent{{Kind: "MessageEvent", Source: "user", Text: string(prompt)}}
	if reason := successfulRepositoryGroundingFailure(brief, events, 0); reason != "" {
		t.Fatalf("digest-bound recovery checkpoint was ignored: %q", reason)
	}
	checkpoint.AuthoritativeExecution.ExecutionBriefSHA256 = digest('5')
	prompt = mustJSON(map[string]any{"recovery_checkpoint": checkpoint})
	events[0].Text = string(prompt)
	if reason := successfulRepositoryGroundingFailure(brief, events, 0); reason != "REPLAN_AGENTS_INSTRUCTIONS_NOT_INSPECTED" {
		t.Fatalf("tampered recovery checkpoint grounding failure = %q", reason)
	}
}

func TestSuccessfulRepositoryGroundingIsPurposeAndPermissionBased(t *testing.T) {
	brief := application.ExecutionBrief{
		Purpose: kernel.PurposeReplan,
		RoleGrounding: application.RoleExecutionGrounding{
			RoleFQRN:    "custom-design-reviewer",
			Permissions: []string{"repository.read"},
		},
	}
	if !executionRequiresSuccessfulRepositoryGrounding(brief) {
		t.Fatal("read-only replan work did not require successful repository grounding")
	}
	brief.Purpose = kernel.PurposeHandoff
	if executionRequiresSuccessfulRepositoryGrounding(brief) {
		t.Fatal("non-replan work unexpectedly required successful repository grounding")
	}
	brief.Purpose = kernel.PurposeReplan
	brief.RoleGrounding.Permissions = []string{"story.create"}
	if executionRequiresSuccessfulRepositoryGrounding(brief) {
		t.Fatal("replan work without repository authority unexpectedly required repository grounding")
	}
}

func TestRoleToolPolicyEnforcesSignedRepositoryPermissions(t *testing.T) {
	terminalRead := []rawEvent{
		{Kind: "MessageEvent", Source: "user"},
		{ID: "read", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ActionCommand: "sed -n '1,80p' organization/host.go"},
	}
	withoutRepositoryAccess := application.RoleExecutionGrounding{Permissions: []string{"story.create"}}
	if violation, reason, found := roleToolPolicyViolation(withoutRepositoryAccess, terminalRead, 0); !found || violation.ID != "read" || reason != "ROLE_REPOSITORY_TOOL_NOT_AUTHORIZED" {
		t.Fatalf("no-access violation=%+v reason=%q found=%t", violation, reason, found)
	}

	readOnly := application.RoleExecutionGrounding{Permissions: []string{"repository.read", "test.execute"}}
	mutation := append(append([]rawEvent(nil), terminalRead...), rawEvent{ID: "edit", Kind: "ActionEvent", Source: "agent", ToolName: "file_editor", ActionCommand: "str_replace", ActionPath: "organization/host.go"})
	if violation, reason, found := roleToolPolicyViolation(readOnly, mutation, 0); !found || violation.ID != "edit" || reason != "ROLE_REPOSITORY_MUTATION_NOT_AUTHORIZED" {
		t.Fatalf("read-only violation=%+v reason=%q found=%t", violation, reason, found)
	}

	readWrite := application.RoleExecutionGrounding{Permissions: []string{"repository.edit"}}
	if violation, reason, found := roleToolPolicyViolation(readWrite, mutation, 0); found {
		t.Fatalf("read-write violation=%+v reason=%q", violation, reason)
	}

	customRead := []rawEvent{
		{Kind: "MessageEvent", Source: "user"},
		{ID: "search", Kind: "ActionEvent", Source: "agent", ToolName: "repository_search", ActionPayload: json.RawMessage(`{"kind":"RepositorySearchAction","pattern":"Actor"}`)},
	}
	if violation, reason, found := roleToolPolicyViolation(withoutRepositoryAccess, customRead, 0); !found || violation.ID != "search" || reason != "ROLE_REPOSITORY_TOOL_NOT_AUTHORIZED" {
		t.Fatalf("custom-read violation=%+v reason=%q found=%t", violation, reason, found)
	}
	if violation, reason, found := roleToolPolicyViolation(readOnly, customRead, 0); found {
		t.Fatalf("authorized custom-read violation=%+v reason=%q", violation, reason)
	}
}

func TestWorkPurposeToolPolicyPreventsRoleAuthorityFromWideningReadOnlyWork(t *testing.T) {
	mutation := rawEvent{ID: "edit", Kind: "ActionEvent", Source: "agent", ToolName: "file_editor", ActionCommand: "str_replace", ActionPath: "/workspace/a.go"}
	success := rawEvent{Kind: "ObservationEvent", Source: "agent", ToolName: "file_editor", ToolCallID: mutation.ToolCallID}
	events := []rawEvent{{Kind: "MessageEvent", Source: "user"}, mutation, success}
	for _, purpose := range []kernel.WorkPurpose{kernel.PurposeReplan, kernel.PurposeReview, kernel.PurposeValidation, kernel.PurposePromotion} {
		violation, repeated, found := workPurposeToolPolicyViolation(purpose, events, 0)
		if !found || repeated || violation.ID != "edit" {
			t.Fatalf("purpose=%s violation=%#v repeated=%t found=%t", purpose, violation, repeated, found)
		}
	}
	for _, purpose := range []kernel.WorkPurpose{kernel.PurposeImplementation, kernel.PurposeRepair} {
		if violation, _, found := workPurposeToolPolicyViolation(purpose, events, 0); found {
			t.Fatalf("purpose=%s violation=%#v", purpose, violation)
		}
	}
}

func TestWorkPurposeToolPolicyIgnoresRefusedMutationsAndFencesRepeats(t *testing.T) {
	purpose := kernel.PurposeValidation
	refused := rawEvent{ID: "mkdir", Kind: "ActionEvent", Source: "agent", ToolName: "terminal", ActionCommand: "mkdir -p /tmp/tk-repro/db"}
	exit1 := 1
	events := []rawEvent{
		{Kind: "MessageEvent", Source: "user"},
		refused,
		{Kind: "ObservationEvent", Source: "agent", ToolName: "terminal", ObservationExitCode: &exit1},
	}
	if violation, repeated, found := workPurposeToolPolicyViolation(purpose, events, 0); found {
		t.Fatalf("a mutation the sandbox refused was treated as a violation: violation=%#v repeated=%t", violation, repeated)
	}
	exit0 := 0
	didEdit := rawEvent{ID: "edit", Kind: "ActionEvent", Source: "agent", ToolName: "file_editor", ActionCommand: "str_replace", ActionPath: "organization/host.go"}
	events = append(events, didEdit, rawEvent{Kind: "ObservationEvent", Source: "agent", ToolName: "file_editor", ObservationExitCode: &exit0})
	violation, repeated, found := workPurposeToolPolicyViolation(purpose, events, 0)
	if !found || repeated || violation.ID != "edit" {
		t.Fatalf("first effective mutation must correct, not fence: violation=%#v repeated=%t", violation, repeated)
	}
	// After the correction for that action, it no longer counts; a second
	// effective mutation fences.
	events = append(events, rawEvent{Kind: "MessageEvent", Source: "user", Text: workPurposeMutationCorrectionPrefix + violation.ID + "\nkeep going"})
	if violation, _, found := workPurposeToolPolicyViolation(purpose, events, 0); found {
		t.Fatalf("corrected mutation was counted again: violation=%#v", violation)
	}
	second := rawEvent{ID: "edit2", Kind: "ActionEvent", Source: "agent", ToolName: "file_editor", ActionCommand: "str_replace", ActionPath: "kernel/types.go"}
	events = append(events, second, rawEvent{Kind: "ObservationEvent", Source: "agent", ToolName: "file_editor", ObservationExitCode: &exit0})
	violation, repeated, found = workPurposeToolPolicyViolation(purpose, events, 0)
	if !found || !repeated || violation.ID != "edit2" {
		t.Fatalf("second effective mutation must fence: violation=%#v repeated=%t", violation, repeated)
	}
}

func TestViolatesShellDisciplineDistinguishesQuotedLiteralsFromOperators(t *testing.T) {
	tests := []struct {
		command string
		want    bool
	}{
		{command: "rg -n 'ActorFQN|type Actor' organization/", want: false},
		{command: `rg -n "literal;pipe|text" organization/`, want: false},
		{command: `printf '%s' '$HOME'`, want: false},
		{command: `rg -n ActorFQN organization/`, want: false},
		{command: `cd /tmp`, want: true},
		{command: `rg -n ActorFQN organization/ | head`, want: true},
		{command: `rg --files && pwd`, want: true},
		{command: `rg --files; pwd`, want: true},
		{command: `printf "%s" "$HOME"`, want: true},
		{command: "rg --files\npwd", want: true},
	}
	for _, test := range tests {
		if got := violatesShellDiscipline(test.command); got != test.want {
			t.Fatalf("command=%q got=%t want=%t", test.command, got, test.want)
		}
	}
}

func TestClientReconcilesSupersededBriefWithoutNewModelCall(t *testing.T) {
	original, originalDigest := openHandsTestBrief(t)
	originalPrompt := string(mustJSON(original))
	current := original
	current.ExecutionGuidance = []string{"Use the upgraded execution policy."}
	currentDigest := kernel.Digest(testRequestDigest(string(mustJSON(current))))
	workspace := filepath.Join(t.TempDir(), "workspace")
	state := &progressGuardServerState{
		prompt: originalPrompt, workspace: workspace,
		events: []map[string]any{event("evt-user", "MessageEvent", "user", originalPrompt)},
	}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, current)

	observation, err := client.ReconcileSuperseded(context.Background(), current, string(current.InvocationID), originalDigest, currentDigest)
	if err != nil || observation.State != application.ExternalCancelled || observation.RequestDigest != originalDigest || state.interruptCalls != 1 {
		t.Fatalf("observation=%#v err=%v interrupts=%d", observation, err, state.interruptCalls)
	}
}

func TestClientRequiresCommittedEditableCandidateBeforeSuccess(t *testing.T) {
	workspace := t.TempDir()
	runOpenHandsGit(t, workspace, "init", "--initial-branch=task/204")
	runOpenHandsGit(t, workspace, "config", "user.name", "Tekroo Test")
	runOpenHandsGit(t, workspace, "config", "user.email", "test@tekroo.invalid")
	if err := os.WriteFile(filepath.Join(workspace, "base.go"), []byte("package base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runOpenHandsGit(t, workspace, "add", "base.go")
	runOpenHandsGit(t, workspace, "commit", "-m", "baseline")
	baseline := runOpenHandsGit(t, workspace, "rev-parse", "HEAD")

	brief, _ := openHandsTestBrief(t)
	brief.RoleGrounding.Permissions = []string{"repository.edit"}
	brief.Scope.Branch = "task/204"
	brief.Scope.BaselineSHA = baseline
	brief.SemanticContext.BaselineSHA = baseline
	encoded := mustJSON(brief)
	sum := sha256.Sum256(encoded)
	digest := kernel.Digest(hex.EncodeToString(sum[:]))
	if err := os.WriteFile(filepath.Join(workspace, "candidate.go"), []byte("package base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := &progressGuardServerState{
		prompt: string(encoded), workspace: workspace, finished: true,
		events: []map[string]any{event("evt-user", "MessageEvent", "user", string(encoded)), finishEvent("first-finish", "done")},
	}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalRunning || state.correctionCalls != 1 || !strings.HasPrefix(state.correctionText, editableCandidateCompletionCorrectionPrefix) {
		t.Fatalf("observation=%#v err=%v corrections=%d text=%q", observation, err, state.correctionCalls, state.correctionText)
	}
	runOpenHandsGit(t, workspace, "add", "candidate.go")
	runOpenHandsGit(t, workspace, "commit", "-m", "candidate")
	if err := os.MkdirAll(filepath.Join(workspace, ".openhands", "hooks"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".openhands", "hooks", "sma_context_hook.py"), []byte("# injected\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	state.events = append(state.events, finishEvent("second-finish", "committed"))
	state.finished = true

	observation, err = client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalSucceeded || state.correctionCalls != 1 {
		t.Fatalf("observation=%#v err=%v corrections=%d", observation, err, state.correctionCalls)
	}
}

func TestClientDoesNotDemandCandidateFromEditCapableReadOnlyWork(t *testing.T) {
	workspace := t.TempDir()
	runOpenHandsGit(t, workspace, "init", "--initial-branch=task/204")
	runOpenHandsGit(t, workspace, "config", "user.name", "Tekroo Test")
	runOpenHandsGit(t, workspace, "config", "user.email", "test@tekroo.invalid")
	if err := os.WriteFile(filepath.Join(workspace, "base.go"), []byte("package base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runOpenHandsGit(t, workspace, "add", "base.go")
	runOpenHandsGit(t, workspace, "commit", "-m", "baseline")
	baseline := runOpenHandsGit(t, workspace, "rev-parse", "HEAD")

	brief, _ := openHandsTestBrief(t)
	brief.Purpose = kernel.PurposeReview
	brief.RoleGrounding.Permissions = []string{"repository.edit"}
	brief.Scope.Branch = "task/204"
	brief.Scope.BaselineSHA = baseline
	brief.SemanticContext.BaselineSHA = baseline
	encoded := mustJSON(brief)
	sum := sha256.Sum256(encoded)
	digest := kernel.Digest(hex.EncodeToString(sum[:]))
	state := &progressGuardServerState{
		prompt: string(encoded), workspace: workspace, finished: true,
		events: []map[string]any{event("evt-user", "MessageEvent", "user", string(encoded)), finishEvent("finish", "reviewed")},
	}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	client := newOpenHandsTestClient(t, server.URL, workspace, brief)

	observation, err := client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
	if err != nil || observation.State != application.ExternalSucceeded || state.correctionCalls != 0 {
		t.Fatalf("observation=%#v err=%v corrections=%d", observation, err, state.correctionCalls)
	}
}

func runOpenHandsGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
	return strings.TrimSpace(string(output))
}

type progressGuardServerState struct {
	prompt          string
	workspace       string
	events          []map[string]any
	interruptCalls  int
	correctionCalls int
	correctionText  string
	terminal        bool
	finished        bool
	condenserRuns   int
}

func (state *progressGuardServerState) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	conversationID := "00000000-0000-7000-8000-000000000201"
	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/api/conversations/"+conversationID:
		status := "running"
		if state.finished {
			status = "finished"
		} else if state.terminal {
			status = "paused"
		} else if state.interruptCalls > state.correctionCalls {
			status = "paused"
		}
		tokenUsages := make([]map[string]any, state.condenserRuns)
		writeJSON(writer, map[string]any{"id": conversationID, "execution_status": status, "created_at": "2026-08-31T12:00:00Z", "workspace": map[string]any{"kind": "LocalWorkspace", "working_dir": state.workspace}, "agent": testConversationAgent(), "tags": map[string]string{"tekrooinvocation": conversationID, "tekroorequest": testRequestDigest(state.prompt)}, "stats": map[string]any{"usage_to_metrics": map[string]any{"condenser": map[string]any{"token_usages": tokenUsages}}}})
	case request.Method == http.MethodGet && request.URL.Path == "/api/conversations/"+conversationID+"/events/search":
		writeJSON(writer, map[string]any{"items": state.events, "next_page_id": nil})
	case request.Method == http.MethodPost && request.URL.Path == "/api/conversations/"+conversationID+"/interrupt":
		state.interruptCalls++
		writer.WriteHeader(http.StatusNoContent)
	case request.Method == http.MethodPost && request.URL.Path == "/api/conversations/"+conversationID+"/events":
		var payload struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.NewDecoder(request.Body).Decode(&payload) != nil || len(payload.Content) != 1 {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		state.correctionCalls++
		state.correctionText = payload.Content[0].Text
		state.events = append(state.events, event("policy-correction", "MessageEvent", "user", state.correctionText))
		state.finished = false
		writer.WriteHeader(http.StatusOK)
	default:
		writer.WriteHeader(http.StatusNotFound)
	}
}

func actionEvent(id, tool, command string) map[string]any {
	return map[string]any{"id": id, "kind": "ActionEvent", "source": "agent", "timestamp": "2026-08-31T12:00:01Z", "tool_name": tool, "action": map[string]any{"command": command, "kind": "TestAction"}}
}

func actionEventWithPath(id, tool, command, path string) map[string]any {
	return map[string]any{"id": id, "kind": "ActionEvent", "source": "agent", "timestamp": "2026-08-31T12:00:01Z", "tool_name": tool, "action": map[string]any{"command": command, "kind": "TestAction", "path": path}}
}

func observationEvent(id, tool string, isError bool, exitCode int) map[string]any {
	return map[string]any{"id": id, "kind": "ObservationEvent", "source": "environment", "timestamp": "2026-08-31T12:00:02Z", "tool_name": tool, "observation": map[string]any{"kind": "TestObservation", "is_error": isError, "exit_code": exitCode}}
}

func observationEventWithText(id, tool string, isError bool, exitCode int, text string) map[string]any {
	return map[string]any{"id": id, "kind": "ObservationEvent", "source": "environment", "timestamp": "2026-08-31T12:00:02Z", "tool_name": tool, "observation": map[string]any{"kind": "TestObservation", "is_error": isError, "exit_code": exitCode, "content": []map[string]any{{"type": "text", "text": text}}}}
}

func repositorySearchActionEvent(id, toolCallID, pattern, path string) map[string]any {
	return map[string]any{"id": id, "kind": "ActionEvent", "source": "agent", "timestamp": "2026-08-31T12:00:01Z", "tool_name": "repository_search", "tool_call_id": toolCallID, "action": map[string]any{"kind": "RepositorySearchAction", "pattern": pattern, "path": path}}
}

func repositorySearchObservationEvent(id, toolCallID string, truncated bool) map[string]any {
	return map[string]any{"id": id, "kind": "ObservationEvent", "source": "environment", "timestamp": "2026-08-31T12:00:02Z", "tool_name": "repository_search", "tool_call_id": toolCallID, "observation": map[string]any{"kind": "RepositorySearchObservation", "is_error": false, "truncated": truncated}}
}

func intPointer(value int) *int    { return &value }
func boolPointer(value bool) *bool { return &value }

type openHandsServerState struct {
	t              *testing.T
	mu             sync.Mutex
	prompt         string
	workspace      string
	created        bool
	submitted      bool
	createCalls    int
	submitCalls    int
	eventPageCalls int
	createPayload  map[string]any
	finalAsFinish  bool
}

type retryForkServerState struct {
	t               *testing.T
	prompt          string
	workspace       string
	priorID         string
	currentID       string
	requestDigest   string
	submittedPrompt string
	forked          bool
	cleanCreated    bool
	submitted       bool
	forkCalls       int
	createCalls     int
	priorGets       int
	submitCalls     int
}

func (state *retryForkServerState) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Header.Get("X-Session-API-Key") != "session-key" {
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/api/conversations/"+state.currentID:
		if !state.forked && !state.cleanCreated {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		status := "idle"
		if state.submitted {
			status = "finished"
		}
		info := map[string]any{"id": state.currentID, "execution_status": status, "created_at": "2026-08-31T12:00:00Z", "workspace": map[string]any{"kind": "LocalWorkspace", "working_dir": state.workspace}, "agent": testConversationAgent(), "tags": map[string]string{"tekrooinvocation": state.currentID, "tekroorequest": state.requestDigest}}
		if state.forked {
			info["forked_from_conversation_id"] = state.priorID
		}
		writeJSON(writer, info)
	case request.Method == http.MethodGet && request.URL.Path == "/api/conversations/"+state.priorID:
		state.priorGets++
		writeJSON(writer, map[string]any{"id": state.priorID, "execution_status": "paused", "created_at": "2026-08-31T11:00:00Z", "workspace": map[string]any{"kind": "LocalWorkspace", "working_dir": state.workspace}, "tags": map[string]string{"tekrooinvocation": state.priorID, "tekroorequest": strings.Repeat("a", 64)}})
	case request.Method == http.MethodGet && request.URL.Path == "/api/conversations/"+state.priorID+"/events/search":
		writeJSON(writer, map[string]any{"items": []any{actionEvent("prior-read", "terminal", "git status"), observationEvent("prior-read-result", "terminal", false, 0)}, "next_page_id": nil})
	case request.Method == http.MethodPost && request.URL.Path == "/api/conversations/"+state.priorID+"/fork":
		state.forkCalls++
		var payload struct {
			ID            string            `json:"id"`
			ResetMetrics  bool              `json:"reset_metrics"`
			AgentSettings map[string]any    `json:"agent_settings"`
			Tags          map[string]string `json:"tags"`
		}
		decodeErr := json.NewDecoder(request.Body).Decode(&payload)
		llm, _ := payload.AgentSettings["llm"].(map[string]any)
		if decodeErr != nil || payload.ID != state.currentID || !payload.ResetMetrics || payload.Tags["tekrooinvocation"] != state.currentID || payload.Tags["tekroorequest"] != state.requestDigest || payload.Tags[pauseAfterCondensationTag] != "true" || llm["timeout"] != float64(1200) {
			state.t.Errorf("fork payload = %#v", payload)
		}
		state.forked = true
		writer.WriteHeader(http.StatusCreated)
	case request.Method == http.MethodPost && request.URL.Path == "/api/conversations":
		state.createCalls++
		state.cleanCreated = true
		writer.WriteHeader(http.StatusCreated)
	case request.Method == http.MethodPost && request.URL.Path == "/api/conversations/"+state.currentID+"/events":
		state.submitCalls++
		var payload struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.NewDecoder(request.Body).Decode(&payload) != nil || len(payload.Content) != 1 {
			state.t.Error("decode retry submit payload")
		} else {
			state.submittedPrompt = payload.Content[0].Text
		}
		state.submitted = true
		writeJSON(writer, map[string]any{"accepted": true})
	case request.Method == http.MethodGet && request.URL.Path == "/api/conversations/"+state.currentID+"/events/search":
		if !state.submitted {
			writeJSON(writer, map[string]any{"items": []any{}, "next_page_id": nil})
			return
		}
		prompt := state.prompt
		if state.submittedPrompt != "" {
			prompt = state.submittedPrompt
		}
		writeJSON(writer, map[string]any{"items": []any{event("evt-user", "MessageEvent", "user", prompt), event("evt-agent", "MessageEvent", "agent", "continued")}, "next_page_id": nil})
	default:
		writer.WriteHeader(http.StatusNotFound)
	}
}

func (state *openHandsServerState) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if request.Header.Get("X-Session-API-Key") != "session-key" {
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}
	conversationID := "00000000-0000-7000-8000-000000000201"
	switch {
	case request.Method == http.MethodPost && request.URL.Path == "/api/conversations":
		state.createCalls++
		if json.NewDecoder(request.Body).Decode(&state.createPayload) != nil {
			state.t.Error("decode create payload")
		}
		state.created = true
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte(`{"id":"` + conversationID + `"}`))
	case request.Method == http.MethodGet && request.URL.Path == "/api/conversations/"+conversationID:
		if !state.created {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		status := "idle"
		if state.submitted {
			status = "finished"
		}
		writeJSON(writer, map[string]any{"id": conversationID, "execution_status": status, "created_at": "2026-08-31T12:00:00Z", "updated_at": "2026-08-31T12:00:01Z", "workspace": map[string]any{"kind": "LocalWorkspace", "working_dir": state.workspace}, "agent": testConversationAgent(), "tags": map[string]string{"tekrooinvocation": conversationID, "tekroorequest": testRequestDigest(state.prompt)}})
	case request.Method == http.MethodPost && request.URL.Path == "/api/conversations/"+conversationID+"/events":
		state.submitCalls++
		var payload struct {
			Role    string `json:"role"`
			Run     bool   `json:"run"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.NewDecoder(request.Body).Decode(&payload) != nil || payload.Role != "user" || !payload.Run || len(payload.Content) != 1 || payload.Content[0].Text != state.prompt {
			state.t.Errorf("submit payload = %#v", payload)
		}
		state.submitted = true
		writeJSON(writer, map[string]any{"accepted": true})
	case request.Method == http.MethodGet && request.URL.Path == "/api/conversations/"+conversationID+"/events/search":
		state.eventPageCalls++
		if !state.submitted {
			writeJSON(writer, map[string]any{"items": []any{}, "next_page_id": nil})
			return
		}
		if request.URL.Query().Get("page_id") == "page-2" {
			if state.finalAsFinish {
				writeJSON(writer, map[string]any{"items": []any{finishEvent("evt-finish", "done through finish")}, "next_page_id": nil})
				return
			}
			writeJSON(writer, map[string]any{"items": []any{event("evt-agent", "MessageEvent", "agent", "done")}, "next_page_id": nil})
			return
		}
		writeJSON(writer, map[string]any{"items": []any{event("evt-user", "MessageEvent", "user", state.prompt), event("evt-tool", "ActionEvent", "agent", "")}, "next_page_id": "page-2"})
	default:
		writer.WriteHeader(http.StatusNotFound)
	}
}

func finishEvent(id, text string) map[string]any {
	return map[string]any{"id": id, "kind": "ObservationEvent", "source": "environment", "timestamp": "2026-08-31T12:00:01Z", "observation": map[string]any{"kind": "FinishObservation", "content": []map[string]any{{"type": "text", "text": text}}}}
}

func event(id, kind, source, text string) map[string]any {
	return map[string]any{"id": id, "kind": kind, "source": source, "timestamp": "2026-08-31T12:00:01Z", "llm_message": map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}}
}

func testConversationAgent() any {
	var agent any
	if err := json.Unmarshal(qualifiedSMAAgentSettings, &agent); err != nil {
		panic(err)
	}
	return agent
}

func writeJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(value)
}

func testRequestDigest(prompt string) string {
	hash := sha256.Sum256([]byte(prompt))
	return hex.EncodeToString(hash[:])
}

type staticWorkspace struct{ binding WorkspaceBinding }

func (workspace staticWorkspace) ResolveWorkspace(context.Context, kernel.TaskOperationalScope) (WorkspaceBinding, error) {
	return workspace.binding, nil
}

type staticProfile struct{ profile ExecutionProfile }

func (profile staticProfile) ResolveExecutionProfile(context.Context, kernel.Digest, kernel.Digest, kernel.Digest, kernel.Digest) (ExecutionProfile, error) {
	return profile.profile, nil
}

func newOpenHandsTestClient(t *testing.T, baseURL, workspace string, brief application.ExecutionBrief) *Client {
	t.Helper()
	hookConfig := append(json.RawMessage(nil), qualifiedSMAHookConfig...)
	client, err := NewClient(Config{
		BaseURL: baseURL, SessionAPIKey: "session-key", HTTPClient: &http.Client{Timeout: time.Second},
		Workspaces:   staticWorkspace{binding: WorkspaceBinding{WorkspaceID: brief.Scope.WorkspaceID, WorktreeID: brief.Scope.WorktreeID, WorkingDirectory: workspace}},
		Profiles:     staticProfile{profile: ExecutionProfile{ModelProfileDigest: brief.ModelProfileDigest, RuntimeIdentityDigest: brief.RuntimeIdentityDigest, ToolPolicyDigest: brief.ToolPolicyDigest, EffectPolicyDigest: brief.EffectPolicyDigest, AgentSettings: qualifiedSMAAgentSettings, HookConfig: hookConfig, MaxIterations: 0, AgentDelegationDisabled: true, SemanticMemory: acceptedSemanticMemoryBinding(t, hookConfig)}},
		PollInterval: time.Millisecond, MaximumPages: 4, MaximumEvidenceBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func explicitRecoveryTestBrief(t *testing.T) (application.ExecutionBrief, kernel.Digest) {
	brief, _ := openHandsTestBrief(t)
	priorID := kernel.UUIDv7("00000000-0000-7000-8000-000000000200")
	priorConversationID := string(priorID)
	priorProfileID := brief.WorkProfile.ProfileID
	brief.RetryOfInvocationID = &priorID
	brief.RetryOfConversationID = &priorConversationID
	brief.RetryOrdinal = 1
	brief.AttemptOrdinal = 2
	brief.WorkProfile.ProfileID = "00000000-0000-7000-8000-000000000211"
	brief.WorkProfile.ProfileRevision = 2
	brief.WorkProfile.SupersedesProfileID = &priorProfileID
	brief.SemanticContext.WorkProfile = brief.WorkProfile.Binding()
	encoded := mustJSON(brief)
	digest := sha256.Sum256(encoded)
	return brief, kernel.Digest(hex.EncodeToString(digest[:]))
}

func openHandsTestBrief(t *testing.T) (application.ExecutionBrief, kernel.Digest) {
	t.Helper()
	actor := kernel.ActorFQN("teams::coder-1")
	fqrn, _ := kernel.RoleFQRNFromActor(actor)
	brief := application.ExecutionBrief{
		ContractManifest: kernel.ContractIdentity, InvocationID: "00000000-0000-7000-8000-000000000201",
		AuthorizationEventID: "00000000-0000-7000-8000-000000000202", ParentEventID: "00000000-0000-7000-8000-000000000203",
		Task:         application.TaskExecutionSpecification{TaskID: "00000000-0000-7000-8000-000000000204", CreatedEventID: "00000000-0000-7000-8000-000000000205", SourceDigest: digest('a'), StoryID: "00000000-0000-7000-8000-000000000206", Title: "test", Description: "test", AcceptanceCriteria: []string{"pass"}},
		TaskRevision: 5, LifecycleEpoch: 1, ScopeRevision: 1, Purpose: kernel.PurposeImplementation,
		AttemptFamily: "implementation", AttemptOrdinal: 1, ConditionDigest: digest('b'), OutputPredicateDigest: digest('c'),
		ToolPolicyDigest: digest('d'), EffectPolicyDigest: digest('e'), AssignmentID: "00000000-0000-7000-8000-000000000207",
		DecisionRoute: kernel.RouteBoundedExecution, ActorFQN: actor,
		RoleGrounding:      application.RoleExecutionGrounding{ActorFQN: actor, RoleFQRN: fqrn, BundleVersion: "1.0.0", BundleDigest: digest('9'), Capabilities: []string{"implement"}, Permissions: []string{"repository.read", "repository.write"}, Instructions: "Implement the assigned task."},
		Execution:          kernel.ExecutionTuple{ExecutionID: "00000000-0000-7000-8000-000000000208", FencingEpoch: 1},
		ModelProfileDigest: digest('f'), RuntimeIdentityDigest: digest('1'),
		WorkProfile: kernel.WorkRiskProfile{ProfileID: "00000000-0000-7000-8000-000000000210", ProfileRevision: 1, ProfileDigest: digest('2'), LifecycleEpoch: 1, ScopeRevision: 1},
		Scope:       kernel.TaskOperationalScope{TaskID: "00000000-0000-7000-8000-000000000204", TaskRevision: 3, LifecycleEpoch: 1, ScopeRevision: 1, OwnerFQN: actor, Execution: kernel.ExecutionTuple{ExecutionID: "00000000-0000-7000-8000-000000000208", FencingEpoch: 1}, WorkspaceID: "workspace-204", WorktreeID: "worktree-204", Branch: "task/204", BaselineSHA: strings.Repeat("1", 40), WritablePaths: []string{"src"}, BoundEventID: "00000000-0000-7000-8000-000000000209"},
		DeadlineAt:  time.Date(2026, 8, 31, 13, 0, 0, 0, time.UTC), CoordinationRule: "RETURN_EVIDENCE_ONLY",
	}
	brief.SemanticContext = application.SemanticContextRequest{
		Label: application.SemanticContextLabel, AllowedUse: application.SemanticContextUse,
		CurrentRealityRule: application.SemanticContextPrecedence, NonAuthoritative: true,
		NoTeamsAuthorityFallback: true, InvocationID: brief.InvocationID,
		AuthorizationEventID: brief.AuthorizationEventID, ParentEventID: brief.ParentEventID,
		TaskID: brief.Task.TaskID, StoryID: brief.Task.StoryID,
		TaskCreatedEventID: brief.Task.CreatedEventID, TaskCreatedSourceDigest: brief.Task.SourceDigest,
		TaskRevision: brief.TaskRevision, LifecycleEpoch: brief.LifecycleEpoch, ScopeRevision: brief.ScopeRevision,
		OperationalScopeEventID: brief.Scope.BoundEventID, AssignmentID: brief.AssignmentID,
		ActorFQN: brief.ActorFQN, Execution: brief.Execution, WorkspaceID: brief.Scope.WorkspaceID,
		WorktreeID: brief.Scope.WorktreeID, BaselineSHA: brief.Scope.BaselineSHA,
		WorkProfile: brief.WorkProfile.Binding(), ModelProfileDigest: brief.ModelProfileDigest,
		RuntimeIdentityDigest: brief.RuntimeIdentityDigest, ToolPolicyDigest: brief.ToolPolicyDigest,
		EffectPolicyDigest: brief.EffectPolicyDigest, ForbiddenEffects: application.SemanticContextForbiddenEffects(),
	}
	encoded, err := json.Marshal(brief)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(encoded)
	return brief, kernel.Digest(hex.EncodeToString(hash[:]))
}

func digest(value byte) kernel.Digest { return kernel.Digest(strings.Repeat(string(value), 64)) }

func errorsIs(err, target error) bool {
	return err == target || strings.Contains(err.Error(), target.Error())
}
