package openhands

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

func TestSubmitResultActionOutputStripsKindAndMarks(t *testing.T) {
	payload := json.RawMessage(`{"schema_version":"1.0.0","outcome":"completed","summary":"s","evidence":[],"message_proposals":[],"work_product":{"a":1},"kind":"ClientAction_submit_result"}`)
	output, ok := submitResultActionOutput(payload)
	if !ok {
		t.Fatal("conversion rejected a valid action payload")
	}
	text := string(output)
	if !strings.HasPrefix(text, application.OrganizationalResultMarker+"\n") {
		t.Fatalf("output missing marker prefix: %q", text[:40])
	}
	if strings.Contains(text, "ClientAction_submit_result") {
		t.Fatal("SDK kind discriminator leaked into handler output")
	}
	var envelope map[string]any
	if err := json.Unmarshal(output[len(application.OrganizationalResultMarker)+1:], &envelope); err != nil {
		t.Fatalf("payload after marker is not one JSON object: %v", err)
	}
	if envelope["outcome"] != "completed" {
		t.Fatalf("envelope = %#v", envelope)
	}
}

func TestWithSubmitResultToolInjectsSpecOnce(t *testing.T) {
	var settings any
	if err := json.Unmarshal([]byte(qualifiedAgentSettingsJSON), &settings); err != nil {
		t.Fatal(err)
	}
	settings.(map[string]any)["tools"] = []any{
		map[string]any{"name": "glob", "params": map[string]any{}},
		map[string]any{"name": "repository_search", "params": map[string]any{}},
		map[string]any{"name": "repository_view", "params": map[string]any{}},
	}
	injected, ok := withSubmitResultTool(settings)
	if !ok {
		t.Fatal("injection rejected qualified settings")
	}
	tools := injected.(map[string]any)["tools"].([]any)
	if len(tools) != 4 {
		t.Fatalf("tools = %d", len(tools))
	}
	entry := tools[3].(map[string]any)
	if entry["name"] != submitResultToolName {
		t.Fatalf("entry = %#v", entry)
	}
	spec := entry["params"].(map[string]any)["spec"].(map[string]any)
	if spec["name"] != submitResultToolName {
		t.Fatalf("spec = %#v", spec)
	}
	// Idempotent: a second injection must not duplicate the entry.
	again, ok := withSubmitResultTool(injected)
	if !ok || len(again.(map[string]any)["tools"].([]any)) != 4 {
		t.Fatal("injection is not idempotent")
	}
	// The original settings must be unmodified.
	if len(settings.(map[string]any)["tools"].([]any)) != 3 {
		t.Fatal("injection mutated the stored profile settings")
	}
	// A profile without an explicit tools list must be refused.
	var defaults any
	if err := json.Unmarshal([]byte(qualifiedAgentSettingsJSON), &defaults); err != nil {
		t.Fatal(err)
	}
	if _, ok := withSubmitResultTool(defaults); ok {
		t.Fatal("injection accepted a profile without an explicit tools list")
	}
}

func TestConversationAgentMatchesIgnoresInjectedSubmitResult(t *testing.T) {
	var agent map[string]any
	if err := json.Unmarshal([]byte(qualifiedAgentSettingsJSON), &agent); err != nil {
		t.Fatal(err)
	}
	agent["tools"] = []any{
		map[string]any{"name": "glob", "params": map[string]any{}},
		map[string]any{"name": "repository_search", "params": map[string]any{}},
		map[string]any{"name": "repository_view", "params": map[string]any{}},
		map[string]any{"name": submitResultToolName, "params": map[string]any{"spec": submitResultToolSpec()}},
	}
	profile := map[string]any{}
	if err := json.Unmarshal([]byte(qualifiedAgentSettingsJSON), &profile); err != nil {
		t.Fatal(err)
	}
	profile["tools"] = []any{
		map[string]any{"name": "glob", "params": map[string]any{}},
		map[string]any{"name": "repository_search", "params": map[string]any{}},
		map[string]any{"name": "repository_view", "params": map[string]any{}},
	}
	profileRaw, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"agent": agent})
	if err != nil {
		t.Fatal(err)
	}
	var info conversationInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		t.Fatal(err)
	}
	if !conversationAgentMatches(info, profileRaw) {
		t.Fatal("materialized agent with injected submit_result rejected against clean profile")
	}
}

func TestClientInjectsSubmitResultToolForHandlerExecution(t *testing.T) {
	brief, requestDigest := openHandsTestBrief(t)
	messageID := kernel.UUIDv7("00000000-0000-7000-8000-000000000810")
	brief.MessageHandler = &application.MessageHandlerGrounding{
		MessageID: messageID, MessageType: "tekroo.message.task.assigned", MessagePurpose: "HANDOFF",
		SubscriptionPurpose: "implementation", CharterDigest: digest('a'), HandlerDigest: digest('b'),
		Instructions: "Implement the admitted task.", InputSchemaDigest: digest('c'), InputSchema: json.RawMessage(`{"type":"object"}`),
		ResultSchemaDigest: digest('d'), ResultSchema: json.RawMessage(`{"type":"object"}`),
		AllowedResults: []string{"completed"},
	}
	brief.AdmittedMessage = &application.AdmittedMessage{ID: messageID, Type: "tekroo.message.task.assigned", Purpose: "HANDOFF", Body: json.RawMessage(`{"task":"bounded"}`)}

	workspace := filepath.Join(t.TempDir(), "workspace")
	var createPayload map[string]any
	profileSettings := map[string]any{}
	if err := json.Unmarshal([]byte(qualifiedAgentSettingsJSON), &profileSettings); err != nil {
		t.Fatal(err)
	}
	profileSettings["tools"] = []any{
		map[string]any{"name": "glob", "params": map[string]any{}},
		map[string]any{"name": "repository_search", "params": map[string]any{}},
		map[string]any{"name": "repository_view", "params": map[string]any{}},
	}
	profileRaw, err := json.Marshal(profileSettings)
	if err != nil {
		t.Fatal(err)
	}
	agentResponse, err := json.Marshal(map[string]any{"agent": profileSettings})
	if err != nil {
		t.Fatal(err)
	}
	var agentEcho map[string]any
	if err := json.Unmarshal(agentResponse, &agentEcho); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/api/conversations":
			_ = json.NewDecoder(request.Body).Decode(&createPayload)
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write([]byte(`{"id":"` + string(brief.InvocationID) + `"}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/conversations/"+string(brief.InvocationID):
			writeJSON(writer, map[string]any{"id": string(brief.InvocationID), "execution_status": "idle", "created_at": "2026-08-31T12:00:00Z", "workspace": map[string]any{"kind": "LocalWorkspace", "working_dir": workspace}, "agent": agentEcho["agent"], "tags": map[string]string{"tekrooinvocation": string(brief.InvocationID), "tekroorequest": testRequestDigest(string(mustJSON(brief)))}})
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	hookConfig := append(json.RawMessage(nil), qualifiedSMAHookConfig...)
	semanticMemory := acceptedSemanticMemoryBinding(t, hookConfig)
	client, err := NewClient(Config{
		BaseURL: server.URL, SessionAPIKey: "session-key", HTTPClient: &http.Client{Timeout: time.Second},
		Workspaces: staticWorkspace{binding: WorkspaceBinding{WorkspaceID: brief.Scope.WorkspaceID, WorktreeID: brief.Scope.WorktreeID, WorkingDirectory: workspace}},
		Profiles:   staticProfile{profile: ExecutionProfile{ModelProfileDigest: brief.ModelProfileDigest, RuntimeIdentityDigest: brief.RuntimeIdentityDigest, ToolPolicyDigest: brief.ToolPolicyDigest, EffectPolicyDigest: brief.EffectPolicyDigest, AgentSettings: profileRaw, HookConfig: hookConfig, AgentDelegationDisabled: true, SemanticMemory: semanticMemory}},
		PollInterval: time.Millisecond, MaximumPages: 4, MaximumEvidenceBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	status, _, err := client.createConversation(context.Background(), brief, preparedExecution{
		prompt: "prompt", requestDigest: requestDigest,
		workspace: WorkspaceBinding{WorkspaceID: brief.Scope.WorkspaceID, WorktreeID: brief.Scope.WorktreeID, WorkingDirectory: workspace},
		profile:   ExecutionProfile{ModelProfileDigest: brief.ModelProfileDigest, RuntimeIdentityDigest: brief.RuntimeIdentityDigest, ToolPolicyDigest: brief.ToolPolicyDigest, EffectPolicyDigest: brief.EffectPolicyDigest, AgentSettings: profileRaw, HookConfig: hookConfig, AgentDelegationDisabled: true, SemanticMemory: semanticMemory},
	})
	if err != nil || (status != http.StatusCreated && status != http.StatusOK) {
		t.Fatalf("create = %d, err = %v", status, err)
	}
	tools, _ := createPayload["client_tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("client_tools = %#v", createPayload["client_tools"])
	}
	spec := tools[0].(map[string]any)
	if spec["name"] != submitResultToolName {
		t.Fatalf("spec = %#v", spec)
	}
	settings := createPayload["agent_settings"].(map[string]any)
	agentTools := settings["tools"].([]any)
	if len(agentTools) != 4 || agentTools[3].(map[string]any)["name"] != submitResultToolName {
		t.Fatalf("agent_settings tools = %#v", agentTools)
	}
}
