package openhands

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
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

func TestSubmitResultActionOutputSuppliesOnlyMissingFixedVersion(t *testing.T) {
	handler := application.MessageHandlerGrounding{
		ResultSchema:   json.RawMessage(`{"type":"object","additionalProperties":false,"required":["schema_version","outcome","summary","evidence","message_proposals","work_product"],"properties":{"schema_version":{"const":"1.0.0"},"outcome":{"const":"completed"},"summary":{"type":"string"},"evidence":{"type":"array"},"message_proposals":{"type":"array"},"work_product":{"type":"object"}}}`),
		AllowedResults: []string{"completed"},
	}
	base := `{"outcome":"completed","summary":"done","evidence":[],"message_proposals":[],"work_product":{},"kind":"ClientAction_submit_envelope"`
	for _, test := range []struct {
		name    string
		payload string
		valid   bool
	}{
		{"omitted", base + `}`, true},
		{"correct", base + `,"schema_version":"1.0.0"}`, true},
		{"conflicting", base + `,"schema_version":"2.0.0"}`, false},
		{"null", base + `,"schema_version":null}`, false},
		{"encoded-work-product", `{"outcome":"completed","summary":"done","evidence":[],"message_proposals":[],"work_product":"{\"answer\":\"not an object\"}","kind":"ClientAction_submit_envelope"}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			output, ok := submitResultActionOutput(json.RawMessage(test.payload))
			if !ok {
				t.Fatal("well-formed action payload was rejected before handler validation")
			}
			_, err := application.ValidateRoleHandlerResult(handler, output)
			if (err == nil) != test.valid {
				t.Fatalf("handler result validity = %t, want %t: %v", err == nil, test.valid, err)
			}
			if test.valid && !bytes.Contains(output, []byte(`"schema_version":"1.0.0"`)) {
				t.Fatalf("canonical result lacks fixed version: %s", output)
			}
		})
	}
	if _, ok := submitResultActionOutput(json.RawMessage(`null`)); ok {
		t.Fatal("null action payload accepted")
	}
}

func TestAcceptedSubmitResultNeedsNoModelSuppliedVersion(t *testing.T) {
	handler := application.MessageHandlerGrounding{
		ResultSchema:   json.RawMessage(`{"type":"object","required":["schema_version","outcome","work_product"],"properties":{"schema_version":{"const":"1.0.0"},"outcome":{"const":"completed"},"work_product":{"type":"object"}}}`),
		AllowedResults: []string{"completed"},
	}
	events := []rawEvent{
		{Kind: "MessageEvent", Source: "user"},
		{Kind: "ActionEvent", Source: "agent", ToolName: submitResultToolName, ToolCallID: "call-1", ActionPayload: json.RawMessage(`{"outcome":"completed","work_product":{},"kind":"ClientAction_submit_envelope"}`)},
		{Kind: "ObservationEvent", Source: "environment", ToolName: submitResultToolName, ToolCallID: "call-1", ObservationKind: "ClientToolObservation"},
	}
	output, accepted := acceptedSubmitResult(handler, events, 0)
	if !accepted || !bytes.Contains(output, []byte(`"schema_version":"1.0.0"`)) {
		t.Fatalf("version-normalized result not accepted: accepted=%t output=%s", accepted, output)
	}
	events[1].ActionPayload = json.RawMessage(`{"outcome":"completed","work_product":{},"schema_version":"2.0.0","kind":"ClientAction_submit_envelope"}`)
	if _, accepted := acceptedSubmitResult(handler, events, 0); accepted {
		t.Fatal("conflicting model-supplied version was accepted")
	}
}

func TestAcceptedSubmitResultRequiresSuccessfulDeliveryAndKeepsFirst(t *testing.T) {
	handler := application.MessageHandlerGrounding{
		ResultSchema:   json.RawMessage(`{"type":"object","required":["schema_version","outcome","work_product"],"properties":{"schema_version":{"const":"1.0.0"},"outcome":{"const":"completed"},"work_product":{"type":"object"}}}`),
		AllowedResults: []string{"completed"},
	}
	first := json.RawMessage(`{"outcome":"completed","work_product":{"answer":"first"},"kind":"ClientAction_submit_envelope_v2"}`)
	second := json.RawMessage(`{"outcome":"completed","work_product":{"answer":"second"},"kind":"ClientAction_submit_envelope_v2"}`)
	events := []rawEvent{
		{Kind: "MessageEvent", Source: "user"},
		{Kind: "ActionEvent", Source: "agent", ToolName: historicalSubmitResultToolName, ToolCallID: "call-1", ActionPayload: first},
		{Kind: "ObservationEvent", Source: "environment", ToolName: historicalSubmitResultToolName, ToolCallID: "call-1", ObservationKind: "ClientToolObservation"},
		{Kind: "ActionEvent", Source: "agent", ToolName: historicalSubmitResultToolName, ToolCallID: "call-2", ActionPayload: second},
		{Kind: "ObservationEvent", Source: "environment", ToolName: historicalSubmitResultToolName, ToolCallID: "call-2", ObservationKind: "ClientToolObservation"},
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
		MessageType:    "tekroo.message.task.assigned",
		ResultSchema:   json.RawMessage(`{"type":"object","required":["schema_version","outcome","work_product"],"properties":{"schema_version":{"const":"1.0.0"},"outcome":{"const":"completed"},"work_product":{"type":"object"}}}`),
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
		Workspaces:   staticWorkspace{binding: WorkspaceBinding{WorkspaceID: brief.Scope.WorkspaceID, WorktreeID: brief.Scope.WorktreeID, WorkingDirectory: workspace}},
		Profiles:     staticProfile{profile: ExecutionProfile{ModelProfileDigest: brief.ModelProfileDigest, RuntimeIdentityDigest: brief.RuntimeIdentityDigest, ToolPolicyDigest: brief.ToolPolicyDigest, EffectPolicyDigest: brief.EffectPolicyDigest, AgentSettings: profileRaw, HookConfig: hookConfig, AgentDelegationDisabled: true, SemanticMemory: acceptedSemanticMemoryBinding(t, hookConfig)}},
		PollInterval: time.Millisecond, MaximumPages: 4, MaximumEvidenceBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := client.Inspect(context.Background(), brief, string(brief.InvocationID), requestDigest)
	if err != nil || observation.State != application.ExternalSucceeded || interrupts != 1 || !strings.Contains(string(observation.Output), `"answer":"accepted"`) || !strings.Contains(string(observation.Output), `"schema_version":"1.0.0"`) {
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
	context := injected.(map[string]any)["agent_context"].(map[string]any)
	if context["system_message_suffix"] != structuredCompletionSystemSuffix {
		t.Fatal("read-only execution still advertises terminal instructions")
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
	if submitResultToolName != "submit_result" {
		t.Fatal("public result tool has an unstable or versioned name")
	}
	for _, field := range parameters["required"].([]any) {
		if field == "schema_version" {
			t.Fatal("model is still required to repeat the fixed outer schema version")
		}
	}
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

func TestEveryConfiguredHandlerSchemaMatchesSubmissionBoundary(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source")
	}
	root := filepath.Join(filepath.Dir(source), "..", "..")
	results, err := filepath.Glob(filepath.Join(root, "config", "starter-team", "roles-v4", "*", "handlers", "*", "result.schema.json"))
	if err != nil || len(results) == 0 {
		t.Fatalf("discover handler result schemas: count=%d err=%v", len(results), err)
	}
	parameters := submitResultToolSpec()["parameters"].(map[string]any)
	toolRequired := stringSetFromJSONArray(t, parameters["required"])
	for _, resultPath := range results {
		t.Run(strings.TrimPrefix(resultPath, root+string(filepath.Separator)), func(t *testing.T) {
			for _, name := range []string{"input.schema.json", "result.schema.json"} {
				path := filepath.Join(filepath.Dir(resultPath), name)
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
				if err != nil {
					t.Fatalf("decode %s: %v", name, err)
				}
				compiler := jsonschema.NewCompiler()
				compiler.DefaultDraft(jsonschema.Draft2020)
				const resource = "urn:tekroo:handler-contract-audit"
				if err := compiler.AddResource(resource, document); err != nil {
					t.Fatalf("add %s: %v", name, err)
				}
				if _, err := compiler.Compile(resource); err != nil {
					t.Fatalf("compile %s: %v", name, err)
				}
			}
			data, err := os.ReadFile(resultPath)
			if err != nil {
				t.Fatal(err)
			}
			var result map[string]any
			if err := json.Unmarshal(data, &result); err != nil {
				t.Fatal(err)
			}
			required := stringSetFromJSONArray(t, result["required"])
			if !slices.Contains(required, "schema_version") {
				t.Fatal("canonical handler result does not require schema_version")
			}
			required = slices.DeleteFunc(required, func(field string) bool { return field == "schema_version" })
			slices.Sort(required)
			if !slices.Equal(required, toolRequired) {
				t.Fatalf("handler result fields %v differ from tool fields %v", required, toolRequired)
			}
			properties := result["properties"].(map[string]any)
			version := properties["schema_version"].(map[string]any)
			if version["const"] != "1.0.0" {
				t.Fatalf("unexpected outer schema version: %#v", version)
			}
			workProduct := properties["work_product"].(map[string]any)
			if workProduct["type"] != "object" {
				t.Fatalf("work_product is not a JSON object: %#v", workProduct)
			}
		})
	}
}

func stringSetFromJSONArray(t *testing.T, raw any) []string {
	t.Helper()
	values, ok := raw.([]any)
	if !ok {
		t.Fatalf("required fields are not an array: %#v", raw)
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		field, ok := value.(string)
		if !ok || field == "" || slices.Contains(result, field) {
			t.Fatalf("invalid or duplicate required field: %#v", value)
		}
		result = append(result, field)
	}
	slices.Sort(result)
	return result
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
