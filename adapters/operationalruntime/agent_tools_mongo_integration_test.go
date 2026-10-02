//go:build mongo_integration

package operationalruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tekroo-ai/teams/adapters/executionruntime"
	"github.com/tekroo-ai/teams/adapters/fake"
	"github.com/tekroo-ai/teams/adapters/mongo"
	"github.com/tekroo-ai/teams/adapters/openhands"
	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/contract"
	"github.com/tekroo-ai/teams/kernel"
)

// This test uses an isolated Mongo replica set and a fake conversation server.
// Setting TEKROO_OPENHANDS_PYTHON also exercises the installed OpenHands SDK's
// MCP tool path. TEKROO_LIVE_AGENT_CANARY additionally makes model calls and
// creates an Agent Canvas conversation using TEKROO_OPENHANDS_SESSION_KEY_FILE.
func TestPersistedInvocationBindsReadOnlyOpenHandsToolSession(t *testing.T) {
	process, uri := startRuntimeMongod(t)
	defer stopRuntimeMongod(process)
	now := time.Now().UTC().Truncate(time.Millisecond)
	policy := integratedPolicy()
	store, err := mongo.Open(contextWithTimeout(t), mongo.Config{
		URI: uri, Database: "tekroo_tool_session_test", ContractIdentity: kernel.ContractIdentity,
		ManifestSHA256: phase4ManifestSHA, MigrationLevel: 1, Policy: policy,
		BacklogLimit: 1024, DeliveryPolicyRevision: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeRuntimeStore(t, store)
	catalogue, err := contract.Load(os.DirFS(filepath.Join("..", "..")), "CONTRACTS/tekroo.kernel.contracts/0.12.0")
	if err != nil {
		t.Fatal(err)
	}
	provenance, err := fake.ProvenanceBasis()
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "note.txt"), []byte("PERSISTED_TEAMS_READ_PROOF\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newIntegratedFixture(t, now, 7300, "teams::coder-1", "workspace-tool-session", "worktree-tool-session")
	fakeConversationDelay := time.Minute
	if os.Getenv("TEKROO_LIVE_AGENT_CANARY") != "" {
		fakeConversationDelay = 5 * time.Minute
	}
	conversation := &integratedOpenHands{t: t, conversations: make(map[string]*integratedConversation), delays: map[string]time.Duration{string(fixture.invocationID): fakeConversationDelay}}
	conversationServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPatch && strings.HasPrefix(request.URL.Path, "/api/conversations/") {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write([]byte(`{"updated":true}`))
			return
		}
		conversation.serveHTTP(writer, request)
	}))
	defer conversationServer.Close()
	profile, err := openhands.NewAcceptedExecutionProfile(fixture.modelDigest, fixture.runtimeDigest, fixture.toolDigest, fixture.effectDigest, 24, "tekroo_tool_session_test", "sma_step15_memory")
	if err != nil {
		t.Fatal(err)
	}
	clock := SystemClock{}
	ids, err := NewUUIDv7Source(clock)
	if err != nil {
		t.Fatal(err)
	}
	const consumer = "teams-tool-session-test"
	toolListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer toolListener.Close()
	toolBaseURL := "http://" + toolListener.Addr().String()
	toolKey := []byte(strings.Repeat("k", 32))
	runtime, err := New(contextWithTimeout(t), Config{
		Store: store, Catalogue: catalogue, Clock: clock, IDs: ids,
		OpenHandsBaseURL: conversationServer.URL, OpenHandsSessionAPIKey: "step7-session-key",
		AgentToolBaseURL: toolBaseURL, AgentToolSigningKey: toolKey,
		HTTPClient:        &http.Client{Timeout: 2 * time.Second},
		WorkspaceBindings: []openhands.WorkspaceBinding{{WorkspaceID: fixture.workspaceID, WorktreeID: fixture.worktreeID, WorkingDirectory: workspace}},
		ExecutionProfiles: []openhands.ExecutionProfile{profile}, RoleGrounding: testRoleGroundingResolver{},
		OpenHandsPollInterval: time.Millisecond, OpenHandsMaximumPages: 8, OpenHandsMaximumEvidence: 1 << 20,
		EvidenceRoot: filepath.Join(t.TempDir(), "evidence"),
		ExecutionPolicy: application.OperationalExecutionPolicy{
			OperationTimeout: 2 * time.Second, MaximumBriefBytes: 1 << 20, ConsumerID: consumer,
			PolicyRevision: 1, ServiceAuthority: fixture.service, ExpiryAuthority: fixture.policy, Provenance: provenance,
		},
		EvidencePolicy: application.CommandEvidenceRecorderPolicy{
			PolicyRevision: 1, Authority: fixture.service, Provenance: provenance,
			ProducingVersion: "tool-session-test", RetentionPolicy: "tool-session-test",
		},
		WorkerPolicy: executionruntime.Policy{
			ConsumerID: consumer, LeaseDuration: 3 * time.Second, ReconciliationInterval: 10 * time.Millisecond,
			MaximumReconciliations: 20, MaximumConcurrentInvocations: 1, LeaseOperationTimeout: time.Second,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(contextWithTimeout(t))
	toolHTTPServer := &http.Server{Handler: runtime.AgentToolHandler(), ReadHeaderTimeout: 2 * time.Second}
	toolHTTPResult := make(chan error, 1)
	go func() { toolHTTPResult <- toolHTTPServer.Serve(toolListener) }()
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := toolHTTPServer.Shutdown(shutdown); err != nil {
			t.Errorf("tool HTTP shutdown: %v", err)
		}
		if err := <-toolHTTPResult; !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("tool HTTP serve: %v", err)
		}
	}()
	fixture.createAuthoritativeTask(t, runtime, provenance)
	fixture.authorizeInvocation(t, runtime, provenance)
	runCtx, cancelRun := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- runtime.Run(runCtx) }()
	defer func() {
		cancelRun()
		select {
		case err := <-runResult:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("runtime stop: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("runtime did not stop")
		}
	}()
	var requestDigest kernel.Digest
	var lastState kernel.WorkInvocationState
	var lastReadErr error
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		current, readErr := store.LoadOperationalExecution(contextWithTimeout(t), fixture.invocationID)
		lastReadErr = readErr
		if readErr == nil {
			lastState = current.Invocation.State
		}
		if readErr == nil && current.Invocation.State == kernel.InvocationStarted && current.Invocation.RequestDigest != nil {
			requestDigest = *current.Invocation.RequestDigest
			break
		}
		select {
		case err := <-runResult:
			t.Fatalf("runtime stopped before invocation STARTED: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if !requestDigest.Valid() {
		conversation.mu.Lock()
		created := len(conversation.conversations)
		active := conversation.active
		conversation.mu.Unlock()
		t.Fatalf("persisted invocation did not reach STARTED: state=%s read_error=%v created_conversations=%d active=%d", lastState, lastReadErr, created, active)
	}
	conversation.mu.Lock()
	created := conversation.conversations[string(fixture.invocationID)]
	var agent any
	if created != nil {
		agent = created.agent
	}
	conversation.mu.Unlock()
	settings, ok := agent.(map[string]any)
	if !ok {
		t.Fatalf("OpenHands conversation has no agent settings: %T", agent)
	}
	servers, ok := settings["mcp_config"].(map[string]any)
	if !ok {
		t.Fatalf("OpenHands conversation has no MCP configuration: %#v", settings["mcp_config"])
	}
	toolConfig, ok := servers["tekroo_agent_tools"].(map[string]any)
	if !ok {
		t.Fatalf("Teams tool service was not injected: %#v", servers)
	}
	endpointURL, _ := toolConfig["url"].(string)
	headers, _ := toolConfig["headers"].(map[string]any)
	authorization, _ := headers["Authorization"].(string)
	if !strings.HasPrefix(authorization, "Bearer ") || !strings.HasPrefix(endpointURL, toolBaseURL+agenttools.ReadOnlyMCPPath+string(fixture.invocationID)+"/") {
		t.Fatal("injected MCP endpoint is not invocation-bound")
	}
	restartedService, err := agenttools.NewHTTPService(runtime.toolGateway, toolBaseURL, toolKey)
	if err != nil {
		t.Fatal(err)
	}
	reconstructed, err := restartedService.IssueReadOnlySession(contextWithTimeout(t), fixture.invocationID, requestDigest)
	if err != nil || reconstructed.URL != endpointURL || "Bearer "+reconstructed.BearerToken != authorization {
		t.Fatalf("restart did not reconstruct the same scoped credential: %v", err)
	}
	unauthorized, err := http.Get(endpointURL)
	if err != nil {
		t.Fatal(err)
	}
	unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated tool endpoint status = %d, want 401", unauthorized.StatusCode)
	}
	wrongURL := strings.TrimSuffix(endpointURL, string(requestDigest)) + string(digestByte('0'))
	wrongRequest, err := http.NewRequest(http.MethodGet, wrongURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	wrongRequest.Header.Set("Authorization", authorization)
	wrongResponse, err := http.DefaultClient.Do(wrongRequest)
	if err != nil {
		t.Fatal(err)
	}
	wrongResponse.Body.Close()
	if wrongResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("same token with another digest status = %d, want 401", wrongResponse.StatusCode)
	}
	toolServer, err := runtime.NewReadOnlyAgentToolSession(contextWithTimeout(t), fixture.invocationID, requestDigest)
	if err != nil {
		t.Fatalf("persisted invocation could not bind a tool session: %v", err)
	}
	if _, err := runtime.NewReadOnlyAgentToolSession(contextWithTimeout(t), fixture.invocationID, digestByte('0')); !errors.Is(err, agenttools.ErrStaleBinding) {
		t.Fatalf("wrong persisted request digest accepted: %v", err)
	}
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	serverSession, err := toolServer.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	clientSession, err := mcp.NewClient(&mcp.Implementation{Name: "persisted-invocation-probe", Version: "0.1.0"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()
	listed, err := clientSession.ListTools(ctx, nil)
	if err != nil || len(listed.Tools) != 4 {
		t.Fatalf("persisted invocation MCP tools: %+v, %v", listed, err)
	}
	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "teams_read_file", Arguments: map[string]any{"path": "note.txt"}})
	if err != nil || result == nil {
		t.Fatalf("persisted invocation read failed: %v", err)
	}
	if result.IsError || len(result.Content) != 1 {
		var detail string
		if result != nil && len(result.Content) > 0 {
			if text, ok := result.Content[0].(*mcp.TextContent); ok {
				detail = text.Text
			}
		}
		t.Fatalf("persisted invocation read: detail=%q is_error=%t", detail, result.IsError)
	}
	if content, ok := result.Content[0].(*mcp.TextContent); !ok || !strings.Contains(content.Text, "PERSISTED_TEAMS_READ_PROOF") {
		t.Fatalf("Teams workspace content missing from MCP result: %+v", result)
	}
	if python := os.Getenv("TEKROO_OPENHANDS_PYTHON"); python != "" {
		probePersistedInvocationFromOpenHands(t, python, toolConfig)
		if os.Getenv("TEKROO_LIVE_AGENT_CANARY") != "" {
			probeLiveAgentRoundTrip(t, python, toolConfig, workspace)
			probeLiveAgentServerRoundTrip(t, python, settings, workspace)
		}
	}
}

func probeLiveAgentServerRoundTrip(t *testing.T, python string, settings map[string]any, workspace string) {
	t.Helper()
	sessionKeyFile := os.Getenv("TEKROO_OPENHANDS_SESSION_KEY_FILE")
	if sessionKeyFile == "" {
		t.Fatal("TEKROO_OPENHANDS_SESSION_KEY_FILE is required for the live Agent Canvas canary")
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, python, "-c", liveAgentServerRoundTripProbe, string(encoded), workspace, sessionKeyFile)
	command.Env = append(os.Environ(), "OPENHANDS_SUPPRESS_BANNER=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("live Agent Canvas/model round trip failed: %v: %s", err, output)
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	var observation struct {
		ConversationID string `json:"conversation_id"`
		ToolCalls      int    `json:"tool_calls"`
		Finished       bool   `json:"finished"`
		Answer         string `json:"answer"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &observation); err != nil || observation.ToolCalls != 1 || !observation.Finished || !strings.Contains(observation.Answer, "PERSISTED_TEAMS_READ_PROOF") {
		t.Fatalf("Agent Canvas did not complete one Teams tool round trip: %+v, %v: %s", observation, err, output)
	}
	t.Logf("live Agent Canvas/model round trip: conversation=%s one Teams MCP read; final answer %q", observation.ConversationID, observation.Answer)
}

const liveAgentServerRoundTripProbe = `
import json
import sys
import time
import urllib.request

settings = json.loads(sys.argv[1])
workspace = sys.argv[2]
with open(sys.argv[3], encoding="utf-8") as key_file:
    session_key = key_file.read().strip()
base = "http://127.0.0.1:8000"

def request(method, path, payload=None):
    data = json.dumps(payload).encode() if payload is not None else None
    headers = {"X-Session-API-Key": session_key, "Content-Type": "application/json"}
    req = urllib.request.Request(base + path, data=data, headers=headers, method=method)
    with urllib.request.urlopen(req, timeout=25) as response:
        body = response.read()
        return json.loads(body) if body else {}

for llm in (settings["llm"], settings["condenser"]["llm"]):
    llm["model"] = "openai/ddalcu--Qwen3.8-Flash-Next-MLX-Serve-mixed-4-8bit"
    llm["base_url"] = "http://127.0.0.1:8800/v1"
    llm["api_key"] = "local-canary-only"
    llm["max_output_tokens"] = 1024
    llm["timeout"] = 120
    llm["litellm_extra_body"] = {"enable_mtp": False, "chat_template_kwargs": {"enable_thinking": False}}
settings["tools"] = []
settings["system_prompt"] = "This is a single diagnostic tool-connectivity conversation. Use the available teams_read_file tool to read the requested file, then finish with its exact content. Do not modify files."
created = request("POST", "/api/conversations", {
    "agent_settings": settings,
    "secrets_encrypted": True,
    "workspace": {"kind": "LocalWorkspace", "working_dir": workspace},
    "worktree": False,
    "max_iterations": 5,
    "stuck_detection": True,
    "autotitle": False,
})
conversation_id = created["id"]
request("PATCH", "/api/conversations/" + conversation_id, {"title": "Tekroo isolated native-tool round trip"})
request("POST", "/api/conversations/" + conversation_id + "/events", {
    "role": "user", "run": True,
    "content": [{"type": "text", "text": "Call teams_read_file exactly once with path note.txt. Then finish with the exact file content, without other text or tools."}],
})
deadline = time.monotonic() + 120
events = []
while time.monotonic() < deadline:
    events = request("GET", "/api/conversations/" + conversation_id + "/events/search?limit=100")["items"]
    finishes = [event for event in events if event.get("kind") == "ActionEvent" and event.get("tool_name") == "finish"]
    if finishes:
        break
    time.sleep(0.5)
calls = [event for event in events if event.get("kind") == "ActionEvent" and event.get("tool_name") == "teams_read_file"]
finishes = [event for event in events if event.get("kind") == "ActionEvent" and event.get("tool_name") == "finish"]
answer = finishes[-1].get("action", {}).get("message", "") if finishes else ""
print(json.dumps({"conversation_id": conversation_id, "tool_calls": len(calls), "finished": len(finishes) > 0, "answer": answer[-2000:]}))
`

func probeLiveAgentRoundTrip(t *testing.T, python string, toolConfig map[string]any, workspace string) {
	t.Helper()
	encoded, err := json.Marshal(toolConfig)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, python, "-c", liveAgentRoundTripProbe, string(encoded), workspace)
	command.Env = append(os.Environ(), "OPENHANDS_SUPPRESS_BANNER=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("live OpenHands/model round trip failed: %v: %s", err, output)
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	var observation struct {
		ToolCalls int    `json:"tool_calls"`
		Finished  bool   `json:"finished"`
		Answer    string `json:"answer"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &observation); err != nil || observation.ToolCalls != 1 || !observation.Finished || !strings.Contains(observation.Answer, "PERSISTED_TEAMS_READ_PROOF") {
		t.Fatalf("live agent did not complete one Teams tool round trip: %+v, %v: %s", observation, err, output)
	}
	t.Logf("live OpenHands/model round trip: one Teams MCP read; final answer %q", observation.Answer)
}

const liveAgentRoundTripProbe = `
import json
import sys
from pydantic import SecretStr
from openhands.sdk import LLM, Agent, Conversation
from openhands.sdk.mcp.config import MCPServer

config = MCPServer.model_validate(json.loads(sys.argv[1]))
llm = LLM(
    model="openai/ddalcu--Qwen3.8-Flash-Next-MLX-Serve-mixed-4-8bit",
    model_canonical_name="openai/gpt-4o",
    base_url="http://127.0.0.1:8800/v1",
    api_key=SecretStr("local-canary-only"),
    api_mode="chat",
    native_tool_calling=True,
    stream=False,
    temperature=0,
    max_output_tokens=1024,
    timeout=120,
    litellm_extra_body={"enable_mtp": False, "chat_template_kwargs": {"enable_thinking": False}},
)
agent = Agent(llm=llm, tools=[], mcp_config={"tekroo_agent_tools": config})
conversation = Conversation(agent=agent, workspace=sys.argv[2], max_iteration_per_run=5)
conversation.send_message("This is a tool-connectivity test. Call teams_read_file exactly once with path note.txt. Then finish with the exact file content, without other text or tools.")
conversation.run()
events = [event.model_dump(mode="json") for event in conversation.state.events]
calls = [event for event in events if event.get("kind") == "ActionEvent" and event.get("tool_name") == "teams_read_file"]
finishes = [event for event in events if event.get("kind") == "ActionEvent" and event.get("tool_name") == "finish"]
answer = finishes[-1].get("action", {}).get("message", "") if finishes else ""
print(json.dumps({"tool_calls": len(calls), "finished": len(finishes) > 0, "answer": answer[-2000:]}))
`

func probePersistedInvocationFromOpenHands(t *testing.T, python string, toolConfig map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(toolConfig)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, python, "-c", persistedInvocationOpenHandsProbe, string(encoded))
	command.Env = append(os.Environ(), "OPENHANDS_SUPPRESS_BANNER=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("OpenHands SDK tool probe failed: %v: %s", err, output)
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	var observation struct {
		IsError bool     `json:"is_error"`
		Texts   []string `json:"texts"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &observation); err != nil || observation.IsError || !strings.Contains(strings.Join(observation.Texts, "\n"), "PERSISTED_TEAMS_READ_PROOF") {
		t.Fatalf("OpenHands SDK did not receive persisted Teams workspace content: %+v, %v: %s", observation, err, output)
	}
}

const persistedInvocationOpenHandsProbe = `
import json
import sys
from openhands.sdk.mcp.config import MCPServer
from openhands.sdk.mcp.utils import create_mcp_tools

config = {"teams": MCPServer.model_validate(json.loads(sys.argv[1]))}
client = create_mcp_tools(config, timeout=15.0)
try:
    tool = next(tool for tool in client.tools if tool.name == "teams_read_file")
    observation = tool(tool.action_from_arguments({"path": "note.txt"}))
    print(json.dumps({"is_error": observation.is_error, "texts": [block.text for block in observation.content if hasattr(block, "text")]}))
finally:
    client.sync_close()
`
