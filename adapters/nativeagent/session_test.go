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
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

type testBinding struct {
	root  string
	calls int
	drift bool
}

func (binding *testBinding) BindToolInvocation(_ context.Context, _ kernel.UUIDv7, _ kernel.Digest) (agenttools.Authority, error) {
	binding.calls++
	root := binding.root
	if binding.drift && binding.calls > 1 {
		root = filepath.Join(root, "drift")
	}
	return agenttools.Authority{WorkspaceRoot: root, Permissions: []string{"repository.read"}}, nil
}

type testJournal struct{ entries []agentruntime.Entry }

func (journal *testJournal) Load(_ context.Context, _ string) ([]agentruntime.Entry, error) {
	return append([]agentruntime.Entry(nil), journal.entries...), nil
}

func (journal *testJournal) Append(_ context.Context, id string, expected uint64, kind agentruntime.Kind, payload json.RawMessage) (agentruntime.Entry, error) {
	if uint64(len(journal.entries)) != expected {
		return agentruntime.Entry{}, agentruntime.ErrConflict
	}
	entry := agentruntime.Entry{InvocationID: id, Sequence: expected + 1, Kind: kind, Payload: append(json.RawMessage(nil), payload...)}
	journal.entries = append(journal.entries, entry)
	return entry, nil
}

func testBriefAndProfile() (application.ExecutionBrief, kernel.Digest, Profile) {
	digest := kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	actor := kernel.ActorFQN("teams::coder-1")
	brief := application.ExecutionBrief{
		ContractManifest: kernel.ContractIdentity,
		InvocationID:     kernel.UUIDv7("00000000-0000-7000-8000-000000000951"),
		ActorFQN:         actor,
		RoleGrounding: application.RoleExecutionGrounding{
			ActorFQN: actor, RoleFQRN: kernel.RoleFQRN("coder"), BundleVersion: "1.0.0", BundleDigest: digest,
			Capabilities: []string{"implement"}, Permissions: []string{"repository.read"}, Instructions: "Read the assigned file and report its content.",
		},
		ModelProfileDigest: digest, RuntimeIdentityDigest: digest, ToolPolicyDigest: digest, EffectPolicyDigest: digest,
	}
	encoded, _ := json.Marshal(brief)
	sum := sha256.Sum256(encoded)
	return brief, kernel.Digest(hex.EncodeToString(sum[:])), Profile{
		RoleFQRN: "coder", RoleBundleDigest: digest, ModelProfileDigest: digest,
		RuntimeIdentityDigest: digest, ToolPolicyDigest: digest, EffectPolicyDigest: digest,
		Model: "local-coder", MaxOutputTokens: 128, AllowedReadTools: []string{"read_file"},
	}
}

func TestPreparedSessionBindsModelAndReadToolToInvocation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		var body struct {
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body.Messages) < 2 || body.Messages[0].Role != "system" || body.Messages[1].Role != "user" {
			t.Errorf("missing admitted role and task: %+v", body.Messages)
		}
		if len(body.Tools) != 1 || body.Tools[0].Function.Name != "read_file" {
			t.Errorf("profile tool restriction not honored: %+v", body.Tools)
		}
		writer.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			_, _ = writer.Write([]byte(`{"choices":[{"finish_reason":"tool_calls","message":{"content":"","tool_calls":[{"id":"call-1","function":{"name":"read_file","arguments":"{\"path\":\"note.txt\"}"}}]}}]}`))
		} else {
			_, _ = writer.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"File says hello."}}]}`))
		}
	}))
	defer server.Close()
	brief, digest, profile := testBriefAndProfile()
	profile.BaseURL = server.URL + "/v1"
	binding := &testBinding{root: root}
	journal := &testJournal{}
	config := Config{Bindings: binding, Gateway: agenttools.Gateway{Bindings: binding, Host: agenttools.Host{Timeout: time.Second}}, Journal: journal, HTTP: server.Client(), Profile: profile}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := PrepareReadOnly(ctx, brief, digest, config)
	if err != nil {
		t.Fatal(err)
	}
	output, err := session.Run(ctx)
	if err != nil || output != "File says hello." || requests != 2 || binding.calls < 4 || len(journal.entries) != 5 {
		t.Fatalf("output=%q err=%v requests=%d binding=%d journal=%d", output, err, requests, binding.calls, len(journal.entries))
	}
	output, err = session.Run(ctx)
	if err != nil || output != "File says hello." || requests != 2 {
		t.Fatalf("replayed model call: output=%q err=%v requests=%d", output, err, requests)
	}
}

func TestPreparedSessionRejectsDigestDriftAndBindingDrift(t *testing.T) {
	brief, digest, profile := testBriefAndProfile()
	profile.BaseURL = "http://127.0.0.1:1/v1"
	binding := &testBinding{root: t.TempDir(), drift: true}
	config := Config{Bindings: binding, Gateway: agenttools.Gateway{Bindings: binding, Host: agenttools.Host{Timeout: time.Second}}, Journal: &testJournal{}, HTTP: http.DefaultClient, Profile: profile}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := PrepareReadOnly(ctx, brief, kernel.Digest("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"), config); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("digest drift accepted: %v", err)
	}
	session, err := PrepareReadOnly(ctx, brief, digest, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Run(ctx); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("workspace drift reached model: %v", err)
	}
}

func TestPreparedHandlerSessionAcceptsOnlyValidatedStructuredResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Tools []struct {
				Function struct {
					Name   string `json:"name"`
					Strict bool   `json:"strict"`
				} `json:"function"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		found := false
		for _, tool := range body.Tools {
			found = found || tool.Function.Name == "submit_result" && tool.Function.Strict
		}
		if !found {
			t.Error("handler result schema not exposed")
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"choices":[{"finish_reason":"tool_calls","message":{"tool_calls":[{"id":"final-1","function":{"name":"submit_result","arguments":"{\"outcome\":\"completed\"}"}}]}}]}`))
	}))
	defer server.Close()
	brief, _, profile := testBriefAndProfile()
	brief.MessageHandler = &application.MessageHandlerGrounding{
		ResultSchema:   json.RawMessage(`{"type":"object","additionalProperties":false,"required":["schema_version","outcome"],"properties":{"schema_version":{"const":"1.0.0"},"outcome":{"const":"completed"}}}`),
		AllowedResults: []string{"completed"},
	}
	brief.ResultProtocol = &application.ExecutionResultProtocol{Marker: application.OrganizationalResultMarker}
	encoded, _ := json.Marshal(brief)
	sum := sha256.Sum256(encoded)
	digest := kernel.Digest(hex.EncodeToString(sum[:]))
	profile.BaseURL = server.URL + "/v1"
	binding := &testBinding{root: t.TempDir()}
	journal := &testJournal{}
	config := Config{Bindings: binding, Gateway: agenttools.Gateway{Bindings: binding, Host: agenttools.Host{Timeout: time.Second}}, Journal: journal, HTTP: server.Client(), Profile: profile}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := PrepareReadOnly(ctx, brief, digest, config)
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.Run(ctx)
	if err != nil || result != application.OrganizationalResultMarker+"\n"+`{"outcome":"completed","schema_version":"1.0.0"}` || len(journal.entries) != 3 {
		t.Fatalf("handler result=%q err=%v journal=%d", result, err, len(journal.entries))
	}
}

type nativeEffectBinding struct {
	root   string
	policy kernel.Digest
	tests  bool
}

type nativeTestBinding struct {
	root   string
	policy kernel.Digest
}

func (binding nativeTestBinding) BindToolInvocation(context.Context, kernel.UUIDv7, kernel.Digest) (agenttools.Authority, error) {
	return agenttools.Authority{WorkspaceRoot: binding.root, Permissions: []string{"repository.read", "test.execute"},
		Purpose: kernel.PurposeValidation, EffectPolicyDigest: binding.policy}, nil
}

func (binding nativeEffectBinding) BindToolInvocation(context.Context, kernel.UUIDv7, kernel.Digest) (agenttools.Authority, error) {
	permissions := []string{"repository.edit"}
	if binding.tests {
		permissions = append(permissions, "test.execute")
	}
	return agenttools.Authority{WorkspaceRoot: binding.root, Permissions: permissions, Purpose: kernel.PurposeImplementation, EffectPolicyDigest: binding.policy}, nil
}

func TestImplementationWithGoTestGateRequiresSignedTestPermission(t *testing.T) {
	brief, _, profile := testBriefAndProfile()
	brief.Purpose = kernel.PurposeImplementation
	brief.WorkProfile.RequiredDeterministicGateIDs = []string{"go-test"}
	brief.RoleGrounding.Permissions = []string{"repository.edit"}
	profile.BaseURL = "http://127.0.0.1:1/v1"
	profile.AllowedEffectTools = []string{"write_file"}
	encoded, _ := json.Marshal(brief)
	sum := sha256.Sum256(encoded)
	digest := kernel.Digest(hex.EncodeToString(sum[:]))
	binding := nativeEffectBinding{root: t.TempDir(), policy: brief.EffectPolicyDigest}
	config := Config{Bindings: binding, Gateway: agenttools.Gateway{Bindings: binding, Host: agenttools.Host{Timeout: time.Second}},
		Journal: &testJournal{}, Effects: &nativeEffectLedger{}, HTTP: http.DefaultClient, Profile: profile}
	if _, err := PrepareWithEffects(context.Background(), brief, digest, config); !errors.Is(err, ErrRequiredTestToolUnavailable) {
		t.Fatalf("missing required test permission: %v", err)
	}
	brief.RoleGrounding.Permissions = []string{"repository.edit", "test.execute"}
	encoded, _ = json.Marshal(brief)
	sum = sha256.Sum256(encoded)
	digest = kernel.Digest(hex.EncodeToString(sum[:]))
	profile.AllowedEffectTools = []string{"run_go_tests", "run_go_tests_worktree", "write_file"}
	binding.tests = true
	config.Bindings, config.Gateway.Bindings, config.Profile = binding, binding, profile
	session, err := PrepareWithEffects(context.Background(), brief, digest, config)
	if err != nil || !session.Runner.Effects.Handles("run_go_tests") || !session.Runner.Effects.Handles("run_go_tests_worktree") {
		t.Fatalf("signed test permission did not enable required tool: %v", err)
	}
	model := session.Runner.Model.(boundModel).model.(agentruntime.OpenAIModel)
	if !slices.ContainsFunc(model.Tools, func(tool agentruntime.ToolDefinition) bool { return tool.Name == "run_go_tests_worktree" }) {
		t.Fatal("implementation session did not expose working-tree test schema")
	}
}

type nativeEffectLedger struct {
	mu           sync.Mutex
	record       agenttools.EffectRecord
	failComplete bool
}

func (ledger *nativeEffectLedger) Reserve(_ context.Context, record agenttools.EffectRecord) (agenttools.EffectRecord, bool, error) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if ledger.record.ToolCallID == "" {
		ledger.record = record
		return record, true, nil
	}
	if ledger.record.ToolCallID != record.ToolCallID || ledger.record.ArgumentsSHA256 != record.ArgumentsSHA256 {
		return agenttools.EffectRecord{}, false, agenttools.ErrConflict
	}
	return ledger.record, false, nil
}

func (ledger *nativeEffectLedger) Lookup(_ context.Context, record agenttools.EffectRecord) (agenttools.EffectRecord, bool, error) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if ledger.record.ToolCallID == "" {
		return record, false, nil
	}
	if ledger.record.ToolCallID != record.ToolCallID || ledger.record.ArgumentsSHA256 != record.ArgumentsSHA256 {
		return agenttools.EffectRecord{}, false, agenttools.ErrConflict
	}
	return ledger.record, true, nil
}

func (ledger *nativeEffectLedger) Complete(_ context.Context, record agenttools.EffectRecord, result json.RawMessage) error {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if ledger.failComplete {
		ledger.failComplete = false
		return errors.New("lost effect receipt")
	}
	ledger.record.Result = append(json.RawMessage(nil), result...)
	return nil
}

func TestPreparedEffectSessionReconcilesPendingWriteWithoutRepeatingModel(t *testing.T) {
	root := t.TempDir()
	modelCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		modelCalls++
		writer.Header().Set("Content-Type", "application/json")
		if modelCalls == 1 {
			_, _ = writer.Write([]byte(`{"choices":[{"finish_reason":"tool_calls","message":{"tool_calls":[{"id":"write-1","function":{"name":"write_file","arguments":"{\"path\":\"result.txt\",\"content\":\"written\",\"expected_sha256\":\"\"}"}}]}}]}`))
		} else {
			_, _ = writer.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"done"}}]}`))
		}
	}))
	defer server.Close()
	brief, _, profile := testBriefAndProfile()
	brief.Purpose = kernel.PurposeImplementation
	brief.RoleGrounding.Permissions = []string{"repository.edit"}
	encoded, _ := json.Marshal(brief)
	sum := sha256.Sum256(encoded)
	digest := kernel.Digest(hex.EncodeToString(sum[:]))
	profile.BaseURL = server.URL + "/v1"
	profile.AllowedEffectTools = []string{"write_file"}
	binding := nativeEffectBinding{root: root, policy: brief.EffectPolicyDigest}
	ledger := &nativeEffectLedger{failComplete: true}
	journal := &testJournal{}
	config := Config{Bindings: binding, Gateway: agenttools.Gateway{Bindings: binding, Host: agenttools.Host{Timeout: time.Second}}, Journal: journal, Effects: ledger, HTTP: server.Client(), Profile: profile}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := PrepareReadOnly(ctx, brief, digest, config); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("read-only session accepted mutation profile: %v", err)
	}
	session, err := PrepareWithEffects(ctx, brief, digest, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Run(ctx); !errors.Is(err, agentruntime.ErrEffectUncertain) {
		t.Fatalf("uncertain receipt did not stop turn: %v", err)
	}
	if modelCalls != 1 || len(journal.entries) != 2 {
		t.Fatalf("modelCalls=%d journal=%d", modelCalls, len(journal.entries))
	}
	content, err := os.ReadFile(filepath.Join(root, "result.txt"))
	if err != nil || string(content) != "written" {
		t.Fatalf("physical write=%q err=%v", content, err)
	}
	restarted, err := PrepareWithEffects(ctx, brief, digest, config)
	if err != nil {
		t.Fatal(err)
	}
	result, err := restarted.Run(ctx)
	if err != nil || result != "done" || modelCalls != 2 || len(journal.entries) != 5 {
		t.Fatalf("resumed result=%q err=%v modelCalls=%d journal=%d", result, err, modelCalls, len(journal.entries))
	}
}

func TestEffectfulSessionExposesOnlyExplicitMutationAllowlist(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("preparation must not call the model")
	}))
	defer server.Close()
	brief, _, profile := testBriefAndProfile()
	brief.Purpose = kernel.PurposeImplementation
	brief.RoleGrounding.Permissions = []string{"repository.edit", "repository.read"}
	encoded, _ := json.Marshal(brief)
	sum := sha256.Sum256(encoded)
	digest := kernel.Digest(hex.EncodeToString(sum[:]))
	profile.BaseURL = server.URL + "/v1"
	profile.AllowedEffectTools = []string{"git_commit", "git_stage_files", "write_file"}
	binding := nativeEffectBinding{root: t.TempDir(), policy: brief.EffectPolicyDigest}
	config := Config{Bindings: binding, Gateway: agenttools.Gateway{Bindings: binding, Host: agenttools.Host{Timeout: time.Second}},
		Journal: &testJournal{}, Effects: &nativeEffectLedger{}, HTTP: server.Client(), Profile: profile}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	session, err := PrepareWithEffects(ctx, brief, digest, config)
	if err != nil {
		t.Fatal(err)
	}
	effects := session.Runner.Effects
	for _, name := range profile.AllowedEffectTools {
		if !effects.Handles(name) {
			t.Fatalf("admitted effect %s missing", name)
		}
	}
	if effects.Handles("run_command") || effects.Handles("run_go_tests") {
		t.Fatal("unlisted effect was executable")
	}
	model := session.Runner.Model.(boundModel).model.(agentruntime.OpenAIModel)
	var exposed []string
	for _, tool := range model.Tools {
		exposed = append(exposed, tool.Name)
	}
	for _, name := range profile.AllowedEffectTools {
		if !slices.Contains(exposed, name) {
			t.Fatalf("admitted effect %s not in model schema: %v", name, exposed)
		}
	}
	profile.AllowedEffectTools = []string{"arbitrary_command"}
	config.Profile = profile
	if _, err := PrepareWithEffects(ctx, brief, digest, config); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("unsupported effect accepted: %v", err)
	}
}

func TestValidationSessionBindsOnlyExplicitTestTool(t *testing.T) {
	brief, _, profile := testBriefAndProfile()
	brief.Purpose = kernel.PurposeValidation
	brief.RoleGrounding.Permissions = []string{"repository.read", "test.execute"}
	encoded, _ := json.Marshal(brief)
	sum := sha256.Sum256(encoded)
	digest := kernel.Digest(hex.EncodeToString(sum[:]))
	profile.BaseURL = "http://127.0.0.1:1/v1"
	profile.AllowedEffectTools = []string{"run_go_tests"}
	binding := nativeTestBinding{root: t.TempDir(), policy: brief.EffectPolicyDigest}
	config := Config{Bindings: binding, Gateway: agenttools.Gateway{Bindings: binding, Host: agenttools.Host{Timeout: time.Minute}},
		Journal: &testJournal{}, Effects: &nativeEffectLedger{}, HTTP: http.DefaultClient, Profile: profile}
	session, err := PrepareWithEffects(context.Background(), brief, digest, config)
	if err != nil {
		t.Fatal(err)
	}
	if !session.Runner.Effects.Handles("run_go_tests") || session.Runner.Effects.Handles("run_go_tests_worktree") || session.Runner.Effects.Handles("write_file") {
		t.Fatal("validation session effect boundary is wrong")
	}
	model := session.Runner.Model.(boundModel).model.(agentruntime.OpenAIModel)
	if !slices.ContainsFunc(model.Tools, func(tool agentruntime.ToolDefinition) bool { return tool.Name == "run_go_tests" }) {
		t.Fatal("bound test schema not exposed")
	}
	if slices.ContainsFunc(model.Tools, func(tool agentruntime.ToolDefinition) bool { return tool.Name == "run_go_tests_worktree" }) {
		t.Fatal("validation session exposed working-tree test schema")
	}
	profile.AllowedEffectTools = []string{"run_go_tests", "run_go_tests_worktree"}
	config.Profile = profile
	if _, err := PrepareWithEffects(context.Background(), brief, digest, config); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("validation role acquired working-tree test effect: %v", err)
	}
	profile.AllowedEffectTools = []string{"write_file"}
	config.Profile = profile
	if _, err := PrepareWithEffects(context.Background(), brief, digest, config); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("validation role acquired write effect: %v", err)
	}
}
