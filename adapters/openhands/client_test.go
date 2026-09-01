package openhands

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

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
	if !ok || tags["tekrooinvocation"] != string(brief.InvocationID) || tags["tekroorequest"] != string(digest) {
		t.Fatalf("conversation tags = %#v", state.createPayload["tags"])
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

func TestClientInterruptsImplementationAfterTwelveReadOnlyRepositoryActions(t *testing.T) {
	brief, digest := openHandsTestBrief(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	events := []map[string]any{event("evt-user", "MessageEvent", "user", string(mustJSON(brief)))}
	// Retained regression for the observed alias-feature loop: the commands kept
	// changing text while remaining read-only repository discovery.
	commands := []string{
		"ls -la && cat AGENTS.md | head -100",
		"ls organization/ && rg -l 'ActorFQN|type Actor' organization/",
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
	if observation.State != application.ExternalFailed || !observation.Retryable || !strings.Contains(string(observation.Output), "REPOSITORY_DISCOVERY_LIMIT_EXCEEDED") {
		t.Fatalf("observation = %#v", observation)
	}
	if state.interruptCalls != 1 {
		t.Fatalf("interrupt calls = %d, want 1", state.interruptCalls)
	}
}

func TestClientProgressGuardRequiresSuccessfulMutationObservation(t *testing.T) {
	brief, digest := openHandsTestBrief(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	tests := []struct {
		name          string
		mutationError bool
		wantState     application.ExternalExecutionState
		wantInterrupt int
	}{
		{name: "successful mutation continues", wantState: application.ExternalRunning},
		{name: "failed mutation does not evade guard", mutationError: true, wantState: application.ExternalFailed, wantInterrupt: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			events := []map[string]any{event("evt-user", "MessageEvent", "user", string(mustJSON(brief)))}
			for index := 0; index < maximumRepositoryDiscoveryActions; index++ {
				events = append(events,
					actionEvent(fmt.Sprintf("action-%02d", index), "terminal", "sed -n '1,80p' organization/message.go"),
					observationEvent(fmt.Sprintf("observation-%02d", index), "terminal", false, 0),
				)
				if index == 3 {
					exitCode := 0
					if test.mutationError {
						exitCode = 1
					}
					events = append(events,
						actionEvent("edit-action", "file_editor", "str_replace"),
						observationEvent("edit-observation", "file_editor", test.mutationError, exitCode),
					)
				}
			}
			state := &progressGuardServerState{prompt: string(mustJSON(brief)), workspace: workspace, events: events}
			server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
			defer server.Close()
			client := newOpenHandsTestClient(t, server.URL, workspace, brief)

			observation, err := client.Inspect(context.Background(), brief, string(brief.InvocationID), digest)
			if err != nil || observation.State != test.wantState || state.interruptCalls != test.wantInterrupt {
				t.Fatalf("observation=%#v err=%v interrupts=%d", observation, err, state.interruptCalls)
			}
		})
	}
}

type progressGuardServerState struct {
	prompt         string
	workspace      string
	events         []map[string]any
	interruptCalls int
}

func (state *progressGuardServerState) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	conversationID := "00000000-0000-7000-8000-000000000201"
	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/api/conversations/"+conversationID:
		status := "running"
		if state.interruptCalls > 0 {
			status = "paused"
		}
		writeJSON(writer, map[string]any{"id": conversationID, "execution_status": status, "created_at": "2026-08-31T12:00:00Z", "workspace": map[string]any{"kind": "LocalWorkspace", "working_dir": state.workspace}, "tags": map[string]string{"tekrooinvocation": conversationID, "tekroorequest": testRequestDigest(state.prompt)}})
	case request.Method == http.MethodGet && request.URL.Path == "/api/conversations/"+conversationID+"/events/search":
		writeJSON(writer, map[string]any{"items": state.events, "next_page_id": nil})
	case request.Method == http.MethodPost && request.URL.Path == "/api/conversations/"+conversationID+"/interrupt":
		state.interruptCalls++
		writer.WriteHeader(http.StatusNoContent)
	default:
		writer.WriteHeader(http.StatusNotFound)
	}
}

func actionEvent(id, tool, command string) map[string]any {
	return map[string]any{"id": id, "kind": "ActionEvent", "source": "agent", "timestamp": "2026-08-31T12:00:01Z", "tool_name": tool, "action": map[string]any{"command": command, "kind": "TestAction"}}
}

func observationEvent(id, tool string, isError bool, exitCode int) map[string]any {
	return map[string]any{"id": id, "kind": "ObservationEvent", "source": "environment", "timestamp": "2026-08-31T12:00:02Z", "tool_name": tool, "observation": map[string]any{"kind": "TestObservation", "is_error": isError, "exit_code": exitCode}}
}

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
		writeJSON(writer, map[string]any{"id": conversationID, "execution_status": status, "created_at": "2026-08-31T12:00:00Z", "updated_at": "2026-08-31T12:00:01Z", "workspace": map[string]any{"kind": "LocalWorkspace", "working_dir": state.workspace}, "tags": map[string]string{"tekrooinvocation": conversationID, "tekroorequest": testRequestDigest(state.prompt)}})
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
		Profiles:     staticProfile{profile: ExecutionProfile{ModelProfileDigest: brief.ModelProfileDigest, RuntimeIdentityDigest: brief.RuntimeIdentityDigest, ToolPolicyDigest: brief.ToolPolicyDigest, EffectPolicyDigest: brief.EffectPolicyDigest, AgentSettings: qualifiedSMAAgentSettings, HookConfig: hookConfig, MaxIterations: 30, AgentDelegationDisabled: true, SemanticMemory: acceptedSemanticMemoryBinding(t, hookConfig)}},
		PollInterval: time.Millisecond, MaximumPages: 4, MaximumEvidenceBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func openHandsTestBrief(t *testing.T) (application.ExecutionBrief, kernel.Digest) {
	t.Helper()
	actor := kernel.ActorFQN("teams::coder-1")
	brief := application.ExecutionBrief{
		ContractManifest: kernel.ContractIdentity, InvocationID: "00000000-0000-7000-8000-000000000201",
		AuthorizationEventID: "00000000-0000-7000-8000-000000000202", ParentEventID: "00000000-0000-7000-8000-000000000203",
		Task:         application.TaskExecutionSpecification{TaskID: "00000000-0000-7000-8000-000000000204", CreatedEventID: "00000000-0000-7000-8000-000000000205", SourceDigest: digest('a'), StoryID: "00000000-0000-7000-8000-000000000206", Title: "test", Description: "test", AcceptanceCriteria: []string{"pass"}},
		TaskRevision: 5, LifecycleEpoch: 1, ScopeRevision: 1, Purpose: kernel.PurposeImplementation,
		AttemptFamily: "implementation", AttemptOrdinal: 1, ConditionDigest: digest('b'), OutputPredicateDigest: digest('c'),
		ToolPolicyDigest: digest('d'), EffectPolicyDigest: digest('e'), AssignmentID: "00000000-0000-7000-8000-000000000207",
		DecisionRoute: kernel.RouteBoundedExecution, ActorFQN: actor,
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
