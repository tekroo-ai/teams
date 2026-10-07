package nativeagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

type contextJournal struct {
	streams map[string][]agentruntime.Entry
}

func (j *contextJournal) Load(_ context.Context, id string) ([]agentruntime.Entry, error) {
	return append([]agentruntime.Entry(nil), j.streams[id]...), nil
}
func (j *contextJournal) Append(_ context.Context, id string, expected uint64, kind agentruntime.Kind, payload json.RawMessage) (agentruntime.Entry, error) {
	if uint64(len(j.streams[id])) != expected {
		return agentruntime.Entry{}, agentruntime.ErrConflict
	}
	e := agentruntime.Entry{InvocationID: id, Sequence: expected + 1, Kind: kind, Payload: append(json.RawMessage(nil), payload...)}
	j.streams[id] = append(j.streams[id], e)
	return e, nil
}

type priorModel struct{ calls int }

func (m *priorModel) Complete(context.Context, []agentruntime.Message) (agentruntime.Completion, error) {
	m.calls++
	return agentruntime.Completion{Text: "Original design rationale with retained assumptions"}, nil
}

type unusedTool struct{}

func (unusedTool) ExecuteReadOnly(context.Context, agentruntime.ToolCall) (json.RawMessage, error) {
	return nil, errors.New("historical call unexpectedly executed")
}

func contextFixture(t *testing.T) (*contextJournal, application.ExecutionBrief, kernel.WorkInvocation, agentruntime.ForkSource) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	j := &contextJournal{streams: make(map[string][]agentruntime.Entry)}
	child, _, _ := testBriefAndProfile()
	child.Scope.WorkspaceID = "project"
	child.RemainingGlobalBudget = 2
	child.RemainingPurposeBudget = 1
	child.DeadlineAt = time.Now().UTC().Add(time.Hour)
	digest := kernel.Digest(strings.Repeat("a", 64))
	sourceID := kernel.UUIDv7("00000000-0000-7000-8000-000000000950")
	child.Task.DependsOn = []kernel.UUIDv7{sourceID}
	r := agentruntime.Runner{Journal: j, Model: &priorModel{}, Tools: unusedTool{}}
	output, err := r.Run(ctx, string(sourceID), string(digest), "Original design task")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(output))
	outputDigest := kernel.Digest(hex.EncodeToString(sum[:]))
	finished := time.Now().UTC()
	source := kernel.WorkInvocation{
		ID: sourceID, Revision: 4, State: kernel.InvocationSucceeded, AuthorizationEventID: sourceID, ParentEventID: sourceID,
		TaskID: sourceID, BudgetAccountID: sourceID, LifecycleEpoch: 1, ScopeRevision: 1, TaskRevision: 1,
		WorkProfile:           kernel.WorkProfileBinding{ProfileID: sourceID, ProfileRevision: 1, ProfileDigest: digest, LifecycleEpoch: 1, ScopeRevision: 1},
		QualifiedAssignmentID: sourceID, Purpose: kernel.PurposeHandoff, AttemptFamily: "design", AttemptOrdinal: 1, ConditionDigest: digest, OutputPredicateDigest: digest,
		AllowedTerminalOutcomes: []kernel.WorkInvocationState{kernel.InvocationSucceeded}, ToolPolicyDigest: digest, EffectPolicyDigest: digest,
		ActorFQN: child.ActorFQN, Execution: kernel.ExecutionTuple{ExecutionID: sourceID, FencingEpoch: 1}, ModelProfileDigest: digest, RuntimeIdentityDigest: digest,
		WorkspaceID: "project", DeadlineAt: child.DeadlineAt, IdempotencyKey: "source", AdmissionPolicyRevision: 1, AdmissionPolicyDigest: digest,
		GlobalDebitOrdinal: 1, PurposeDebitOrdinal: 1, RequestDigest: &digest, OutputDigest: &outputDigest, FinishedAt: &finished, LastEventID: sourceID,
	}
	child.Evidence = []kernel.EvidenceRef{{EvidenceID: sourceID, SHA256: outputDigest}}
	ref, err := BindContextFork(ctx, j, child, source)
	if err != nil {
		t.Fatal(err)
	}
	return j, child, source, *ref
}

func TestContextBindingRejectsUnauthorizedAndChangedSources(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, test := range []struct {
		name   string
		mutate func(*application.ExecutionBrief, *kernel.WorkInvocation)
	}{
		{"different actor", func(c *application.ExecutionBrief, _ *kernel.WorkInvocation) { c.ActorFQN = "teams::coder-2" }},
		{"different workspace", func(c *application.ExecutionBrief, _ *kernel.WorkInvocation) { c.Scope.WorkspaceID = "other" }},
		{"missing admitted evidence", func(c *application.ExecutionBrief, _ *kernel.WorkInvocation) { c.Evidence = nil }},
		{"wrong output", func(c *application.ExecutionBrief, s *kernel.WorkInvocation) {
			d := kernel.Digest(strings.Repeat("b", 64))
			s.OutputDigest = &d
			c.Evidence[0].SHA256 = d
		}},
		{"running parent", func(_ *application.ExecutionBrief, s *kernel.WorkInvocation) { s.State = kernel.InvocationStarted }},
		{"missing finish", func(_ *application.ExecutionBrief, s *kernel.WorkInvocation) { s.FinishedAt = nil }},
		{"missing request", func(_ *application.ExecutionBrief, s *kernel.WorkInvocation) { s.RequestDigest = nil }},
		{"no forward dependency", func(c *application.ExecutionBrief, _ *kernel.WorkInvocation) { c.Task.DependsOn = nil }},
		{"expired deadline", func(c *application.ExecutionBrief, s *kernel.WorkInvocation) { c.DeadlineAt = *s.FinishedAt }},
		{"self", func(c *application.ExecutionBrief, s *kernel.WorkInvocation) { c.InvocationID = s.ID }},
	} {
		t.Run(test.name, func(t *testing.T) {
			j, c, s, _ := contextFixture(t)
			test.mutate(&c, &s)
			if _, err := BindContextFork(ctx, j, c, s); err == nil {
				t.Fatal("unauthorized history admitted")
			}
		})
	}
}

func TestPriorTurnsToolHasNoSelectableConversationAndRetainsRawObjects(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	j, _, _, source := contextFixture(t)
	tool := priorTurnsTool{journal: j, source: source, checkAuthority: func(context.Context) error { return nil }}
	for _, raw := range []string{`{"start_sequence":1,"entry_count":21}`, `{"start_sequence":0,"entry_count":1}`, `{"start_sequence":1,"entry_count":1,"invocation_id":"other"}`, `{"start_sequence":1,"conversation_id":2}`, `{"start_sequence":999,"entry_count":1}`} {
		if _, err := tool.read(ctx, json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	result, err := tool.read(ctx, json.RawMessage(`{"start_sequence":1,"entry_count":2}`))
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Entries []struct {
			Payload map[string]json.RawMessage `json:"payload"`
		} `json:"entries"`
		Next uint64 `json:"next_sequence"`
	}
	if json.Unmarshal(result, &parsed) != nil || len(parsed.Entries) != 2 || parsed.Next != 3 || parsed.Entries[1].Payload["text"] == nil {
		t.Fatalf("not structured original turns: %s", result)
	}
	result, err = tool.read(ctx, json.RawMessage(`{"start_sequence":3,"entry_count":20}`))
	if err != nil {
		t.Fatal(err)
	}
	var last map[string]json.RawMessage
	if json.Unmarshal(result, &last) != nil || last["next_sequence"] != nil {
		t.Fatalf("incorrect final page: %s", result)
	}
	tool.checkAuthority = func(context.Context) error { return agenttools.ErrForbidden }
	if _, err := tool.read(ctx, json.RawMessage(`{"start_sequence":1,"entry_count":1}`)); !errors.Is(err, agenttools.ErrForbidden) {
		t.Fatalf("revoked invocation can read history: %v", err)
	}
}

func TestPreparedContextForkSessionReceivesOriginalRationaleAndOnlyCurrentAuthority(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	j, brief, source, ref := contextFixture(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("Current project rules"), 0600); err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body struct {
			Messages []agentruntime.Message `json:"messages"`
			Tools    []struct {
				Function agentruntime.ToolDefinition `json:"function"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		encoded, _ := json.Marshal(body.Messages)
		if !strings.Contains(string(encoded), "Original design rationale with retained assumptions") || !strings.Contains(body.Messages[0].Content, "Current project rules") {
			t.Errorf("lost context/authority: %s", encoded)
		}
		historyTools := 0
		for _, tool := range body.Tools {
			if tool.Function.Name == "read_prior_turns" {
				historyTools++
			}
		}
		if historyTools != 1 {
			t.Error("history tool absent or duplicated")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"Clarification answer"}}]}`))
	}))
	defer server.Close()
	_, _, profile := testBriefAndProfile()
	profile.BaseURL = server.URL + "/v1"
	encoded, _ := json.Marshal(brief)
	sum := sha256.Sum256(encoded)
	digest := kernel.Digest(hex.EncodeToString(sum[:]))
	binding := &testBinding{root: root}
	config := Config{ContextSource: &ref, ContextInvocation: &source, Journal: j, Bindings: binding, Gateway: agenttools.Gateway{Bindings: binding}, HTTP: server.Client(), Profile: profile}
	session, err := PrepareReadOnly(ctx, brief, digest, config)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := session.Run(ctx); err != nil || got != "Clarification answer" {
		t.Fatalf("result=%q err=%v", got, err)
	}
	restarted, err := PrepareReadOnly(ctx, brief, digest, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Run(ctx); err != nil || requests != 1 {
		t.Fatalf("restart repeated request: %d err=%v", requests, err)
	}
	config.ContextInvocation = nil
	if _, err := PrepareReadOnly(ctx, brief, digest, config); err == nil {
		t.Fatal("unbound parent accepted")
	}
}

func TestContextForkSupportsAnExplicitEffectAllowlistWithoutReplayingOldEffects(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	j, brief, source, ref := contextFixture(t)
	brief.Purpose = kernel.PurposeImplementation
	brief.RoleGrounding.Permissions = []string{"repository.edit"}
	_, _, profile := testBriefAndProfile()
	profile.BaseURL = "http://127.0.0.1:1/v1"
	profile.AllowedReadTools = nil
	profile.AllowedEffectTools = []string{"create_file"}
	encoded, _ := json.Marshal(brief)
	sum := sha256.Sum256(encoded)
	digest := kernel.Digest(hex.EncodeToString(sum[:]))
	binding := nativeEffectBinding{root: t.TempDir(), policy: brief.EffectPolicyDigest}
	ledger := &nativeEffectLedger{}
	config := Config{ContextSource: &ref, ContextInvocation: &source, Bindings: binding, Gateway: agenttools.Gateway{Bindings: binding}, Effects: ledger, Journal: j, HTTP: http.DefaultClient, Profile: profile}
	session, err := PrepareWithEffects(ctx, brief, digest, config)
	if err != nil {
		t.Fatal(err)
	}
	if !session.Runner.Effects.Handles("create_file") || session.Runner.Effects.Handles("git_commit") {
		t.Fatal("fork changed the current effect allowlist")
	}
	if ledger.record.ToolCallID != "" {
		t.Fatal("preparing a fork executed a historical effect")
	}
}
