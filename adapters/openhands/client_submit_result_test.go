package openhands

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

func TestHandlerPromptStartsWithTaskCardAndRecognizesEarlierPresentation(t *testing.T) {
	brief, _ := openHandsTestBrief(t)
	brief.MessageHandler = &application.MessageHandlerGrounding{MessageType: "tekroo.message.task.assigned"}
	encoded := mustJSON(brief)
	hash := sha256.Sum256(encoded)
	requestDigest := kernel.Digest(hex.EncodeToString(hash[:]))
	client := newOpenHandsTestClient(t, "http://127.0.0.1", t.TempDir(), brief)
	prepared, err := client.prepare(context.Background(), brief, requestDigest)
	if err != nil {
		t.Fatal(err)
	}
	var prompt map[string]any
	if err := json.Unmarshal([]byte(prepared.prompt), &prompt); err != nil {
		t.Fatal(err)
	}
	card, ok := prompt["agent_task"].(map[string]any)
	if !ok || card["title"] != brief.Task.Title || card["role"] != string(brief.RoleGrounding.RoleFQRN) || card["completion_tool"] != submitResultToolName {
		t.Fatalf("agent task card = %#v", prompt["agent_task"])
	}
	legacy := []rawEvent{{Kind: "MessageEvent", Source: "user", Text: string(encoded)}}
	if index := executionPromptIndex(legacy, prepared, brief, requestDigest); index != 0 {
		t.Fatalf("prior prompt presentation not recognized: %d", index)
	}
}

func TestSubmitResultActionOutputStripsKindAndMarks(t *testing.T) {
	payload := json.RawMessage(`{"schema_version":"1.0.0","outcome":"completed","summary":"s","evidence":[],"message_proposals":[],"work_product":{"a":1},"kind":"ClientAction_submit_envelope"}`)
	output, ok := submitResultActionOutput(payload)
	if !ok {
		t.Fatal("conversion rejected a valid action payload")
	}
	text := string(output)
	if !strings.HasPrefix(text, application.OrganizationalResultMarker+"\n") {
		t.Fatalf("output missing marker prefix: %q", text[:40])
	}
	if strings.Contains(text, "ClientAction_submit_envelope") {
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

func TestAcceptedSubmitResultRequiresSuccessfulDeliveryAndKeepsFirst(t *testing.T) {
	handler := application.MessageHandlerGrounding{
		ResultSchema: json.RawMessage(`{"type":"object","required":["outcome","work_product"],"properties":{"outcome":{"const":"completed"},"work_product":{"type":"object"}}}`),
		AllowedResults: []string{"completed"},
	}
	first := json.RawMessage(`{"outcome":"completed","work_product":{"answer":"first"},"kind":"ClientAction_submit_envelope"}`)
	second := json.RawMessage(`{"outcome":"completed","work_product":{"answer":"second"},"kind":"ClientAction_submit_envelope"}`)
	events := []rawEvent{
		{Kind: "MessageEvent", Source: "user"},
		{Kind: "ActionEvent", Source: "agent", ToolName: submitResultToolName, ToolCallID: "call-1", ActionPayload: first},
		{Kind: "ObservationEvent", Source: "environment", ToolName: submitResultToolName, ToolCallID: "call-1", ObservationKind: "ClientToolObservation"},
		{Kind: "ActionEvent", Source: "agent", ToolName: submitResultToolName, ToolCallID: "call-2", ActionPayload: second},
		{Kind: "ObservationEvent", Source: "environment", ToolName: submitResultToolName, ToolCallID: "call-2", ObservationKind: "ClientToolObservation"},
	}
	if _, accepted := acceptedSubmitResult(handler, events[:2], 0); accepted {
		t.Fatal("unobserved client tool call was accepted")
	}
	failed := append([]rawEvent(nil), events[:3]...)
	failed[2].ObservationError = true
	if _, accepted := acceptedSubmitResult(handler, failed, 0); accepted {
		t.Fatal("failed client tool delivery was accepted")
	}
	output, accepted := acceptedSubmitResult(handler, events, 0)
	if !accepted || !strings.Contains(string(output), `"answer":"first"`) || strings.Contains(string(output), `"answer":"second"`) {
		t.Fatalf("first delivered result not retained: accepted=%t output=%s", accepted, output)
	}
	invalid := append([]rawEvent(nil), events[:3]...)
	invalid[1].ActionPayload = json.RawMessage(`{"outcome":"failed","work_product":{}}`)
	if _, accepted := acceptedSubmitResult(handler, invalid, 0); accepted {
		t.Fatal("handler-invalid result was accepted")
	}
}

func TestInspectStopsAfterDeliveredHandlerResult(t *testing.T) {
	brief, _ := openHandsTestBrief(t)
	brief.MessageHandler = &application.MessageHandlerGrounding{
		MessageType: "tekroo.message.task.assigned",
		ResultSchema: json.RawMessage(`{"type":"object","required":["outcome","work_product"],"properties":{"outcome":{"const":"completed"},"work_product":{"type":"object"}}}`),
		AllowedResults: []string{"completed"},
	}
	encoded := mustJSON(brief)
	hash := sha256.Sum256(encoded)
	requestDigest := kernel.Digest(hex.EncodeToString(hash[:]))
	workspace := filepath.Join(t.TempDir(), "workspace")
	preliminary := newOpenHandsTestClient(t, "http://127.0.0.1", workspace, brief)
	prepared, err := preliminary.prepare(context.Background(), brief, requestDigest)
	if err != nil {
		t.Fatal(err)
	}
	var profile map[string]any
	if err := json.Unmarshal([]byte(qualifiedAgentSettingsJSON), &profile); err != nil {
		t.Fatal(err)
	}
	profile["tools"] = []any{map[string]any{"name": "glob", "params": map[string]any{}}}
	injected, ok := withSubmitResultTool(profile)
	if !ok {
		t.Fatal("cannot inject completion tool into test profile")
	}
	profileRaw := mustJSON(profile)
	result := map[string]any{"outcome": "completed", "work_product": map[string]any{"answer": "accepted"}, "kind": "ClientAction_submit_envelope"}
	events := []map[string]any{
		event("prompt", "MessageEvent", "user", prepared.prompt),
		{"id": "result-action", "kind": "ActionEvent", "source": "agent", "timestamp": "2026-08-31T12:00:02Z", "tool_name": submitResultToolName, "tool_call_id": "result-call", "action": result},
		{"id": "result-observation", "kind": "ObservationEvent", "source": "environment", "timestamp": "2026-08-31T12:00:03Z", "tool_name": submitResultToolName, "tool_call_id": "result-call", "observation": map[string]any{"kind": "ClientToolObservation", "is_error": false}},
	}
	interrupts := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		path := "/api/conversations/" + string(brief.InvocationID)
		switch {
		case request.Method == http.MethodGet && request.URL.Path == path:
			status := "running"
			if interrupts > 0 {
				status = "paused"
			}
			writeJSON(writer, map[string]any{"id": string(brief.InvocationID), "execution_status": status, "created_at": "2026-08-31T12:00:00Z", "workspace": map[string]any{"kind": "LocalWorkspace", "working_dir": workspace}, "agent": injected, "tags": map[string]string{"tekrooinvocation": string(brief.InvocationID), "tekroorequest": string(requestDigest)}})
		case request.Method == http.MethodGet && request.URL.Path == path+"/events/search":
			writeJSON(writer, map[string]any{"items": events, "next_page_id": nil})
		case request.Method == http.MethodPost && request.URL.Path == path+"/interrupt":
			interrupts++
			writer.WriteHeader(http.StatusNoContent)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	hookConfig := append(json.RawMessage(nil), qualifiedSMAHookConfig...)
	client, err := NewClient(Config{
		BaseURL: server.URL, SessionAPIKey: "session-key", HTTPClient: &http.Client{Timeout: time.Second},
		Workspaces: staticWorkspace{binding: WorkspaceBinding{WorkspaceID: brief.Scope.WorkspaceID, WorktreeID: brief.Scope.WorktreeID, WorkingDirectory: workspace}},
		Profiles: staticProfile{profile: ExecutionProfile{ModelProfileDigest: brief.ModelProfileDigest, RuntimeIdentityDigest: brief.RuntimeIdentityDigest, ToolPolicyDigest: brief.ToolPolicyDigest, EffectPolicyDigest: brief.EffectPolicyDigest, AgentSettings: profileRaw, HookConfig: hookConfig, AgentDelegationDisabled: true, SemanticMemory: acceptedSemanticMemoryBinding(t, hookConfig)}},
		PollInterval: time.Millisecond, MaximumPages: 4, MaximumEvidenceBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := client.Inspect(context.Background(), brief, string(brief.InvocationID), requestDigest)
	if err != nil || observation.State != application.ExternalSucceeded || interrupts != 1 || !strings.Contains(string(observation.Output), `"answer":"accepted"`) {
		t.Fatalf("observation=%#v err=%v interrupts=%d", observation, err, interrupts)
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
	if enabled, ok := injected.(map[string]any)["require_tool_call_for_completion"].(bool); !ok || !enabled {
		t.Fatal("injection did not require structured completion")
	}
	if defaults, ok := injected.(map[string]any)["include_default_tools"].([]string); !ok || len(defaults) != 0 {
		t.Fatalf("handler-bound invocation still advertises finish: %#v", injected.(map[string]any)["include_default_tools"])
	}
	if prompt, ok := injected.(map[string]any)["system_prompt"].(string); !ok || !strings.Contains(prompt, structuredCompletionInstruction) || strings.Contains(prompt, finishCompletionInstruction) {
		t.Fatal("handler-bound system prompt has conflicting completion instructions")
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
	parameters := spec["parameters"].(map[string]any)
	workProduct := parameters["properties"].(map[string]any)["work_product"].(map[string]any)
	description, _ := workProduct["description"].(string)
	if workProduct["type"] != "object" || !strings.Contains(description, `{"key":"value"}`) || !strings.Contains(description, `"{\"key\":\"value\"}"`) {
		t.Fatalf("work_product must remain an object with an object-versus-string example: %#v", workProduct)
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
		t.Fatal("materialized agent with injected submit_envelope rejected against clean profile")
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
		Workspaces:   staticWorkspace{binding: WorkspaceBinding{WorkspaceID: brief.Scope.WorkspaceID, WorktreeID: brief.Scope.WorktreeID, WorkingDirectory: workspace}},
		Profiles:     staticProfile{profile: ExecutionProfile{ModelProfileDigest: brief.ModelProfileDigest, RuntimeIdentityDigest: brief.RuntimeIdentityDigest, ToolPolicyDigest: brief.ToolPolicyDigest, EffectPolicyDigest: brief.EffectPolicyDigest, AgentSettings: profileRaw, HookConfig: hookConfig, AgentDelegationDisabled: true, SemanticMemory: semanticMemory}},
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
	if defaults, ok := settings["include_default_tools"].([]any); !ok || len(defaults) != 0 {
		t.Fatalf("handler-bound invocation still advertises finish: %#v", settings["include_default_tools"])
	}
	agentTools := settings["tools"].([]any)
	if len(agentTools) != 4 || agentTools[3].(map[string]any)["name"] != submitResultToolName {
		t.Fatalf("agent_settings tools = %#v", agentTools)
	}
}
