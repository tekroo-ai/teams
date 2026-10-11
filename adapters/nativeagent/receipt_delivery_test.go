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
	"github.com/tekroo-ai/teams/kernel"
)

func TestPreparedSessionProjectsLargeReceiptAndReadsExactSource(t *testing.T) {
	root := t.TempDir()
	content := strings.Repeat("retain exact bytes and no reexecution\n", 350)
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	requests := 0
	journal := &testJournal{}
	var ref agentruntime.RecordedResultRef
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body struct {
			Messages []agentruntime.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		var response agentruntime.Completion
		switch requests {
		case 1:
			response.ToolCalls = []agentruntime.ToolCall{{ID: "original", Name: "read_file", Arguments: json.RawMessage(`{"path":"large.txt"}`)}}
		case 2:
			m := body.Messages[len(body.Messages)-1]
			var view struct {
				Delivery  string
				Reference agentruntime.RecordedResultRef
			}
			if json.Unmarshal([]byte(m.Content), &view) != nil || view.Delivery != "RECORDED_RESULT_REFERENCE" || len(m.Content) > 8192 {
				t.Error("native wiring did not project receipt")
			}
			ref = view.Reference
			args, _ := json.Marshal(map[string]any{"invocation_id": ref.InvocationID, "sequence": ref.Sequence, "sha256": ref.SHA256, "json_pointer": "/output", "byte_limit": 100})
			response.ToolCalls = []agentruntime.ToolCall{{ID: "lookup", Name: "read_recorded_result", Arguments: args}}
		case 3:
			m := body.Messages[len(body.Messages)-1]
			if !strings.Contains(m.Content, "retain exact bytes") || !strings.Contains(m.Content, `"page_complete":false`) {
				t.Errorf("lookup missing exact data: %s", m.Content)
			}
			response.Text = "done"
		default:
			t.Error("unexpected model call")
		}
		calls := []map[string]any{}
		for _, c := range response.ToolCalls {
			calls = append(calls, map[string]any{"id": c.ID, "type": "function", "function": map[string]any{"name": c.Name, "arguments": string(c.Arguments)}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": map[bool]string{true: "tool_calls", false: "stop"}[len(calls) > 0], "message": map[string]any{"content": response.Text, "tool_calls": calls}}}})
	}))
	defer server.Close()
	brief, digest, profile := testBriefAndProfile()
	profile.BaseURL = server.URL + "/v1"
	binding := &testBinding{root: root}
	cfg := Config{Bindings: binding, Gateway: agenttools.Gateway{Bindings: binding, Host: agenttools.Host{Timeout: time.Second}}, Journal: journal, HTTP: server.Client(), Profile: profile}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := PrepareReadOnly(ctx, brief, digest, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := session.Run(ctx); err != nil || got != "done" || requests != 3 {
		t.Fatalf("%q %v calls=%d", got, err, requests)
	}
	var original agentruntime.ToolResult
	if json.Unmarshal(journal.entries[ref.Sequence-1].Payload, &original) != nil {
		t.Fatal("no original receipt")
	}
	var result agenttools.Result
	_ = json.Unmarshal(original.Output, &result)
	fileHash := sha256.Sum256([]byte(content))
	if result.Output != strings.TrimSuffix(content, "\n") || result.SHA256 != hex.EncodeToString(fileHash[:]) || result.LineCount == nil || *result.LineCount != 350 {
		t.Fatal("raw file evidence changed")
	}
	before, _ := json.Marshal(journal.entries)
	if _, err := session.Run(ctx); err != nil || requests != 3 {
		t.Fatalf("restart reexecuted: %v", err)
	}
	after, _ := json.Marshal(journal.entries)
	if string(before) != string(after) {
		t.Fatal("restart changed journal")
	}
	reader := session.Runner.Tools.(allowedReadTools)
	binding.root = t.TempDir()
	args, _ := json.Marshal(ref)
	if _, err := reader.ExecuteReadOnly(ctx, agentruntime.ToolCall{Name: "read_recorded_result", Arguments: args}); !errors.Is(err, ErrInvalidBinding) {
		t.Fatal("revoked accessor authority admitted")
	}
}

func TestReceiptPresentationCannotBecomeValidationEvidence(t *testing.T) {
	evidence := agenttools.Result{TestEvidence: &agenttools.GoTestEvidence{Complete: true, Tests: []agenttools.GoTestCase{{Package: "p", Name: "TestRequired", Status: "SKIP"}}}, Output: strings.Repeat("diagnostics", 2000)}
	raw, _ := json.Marshal(evidence)
	sum := sha256.Sum256(raw)
	m := agentruntime.Message{Role: "tool", Name: "run_go_tests", CallID: "test", Content: string(raw), Receipt: &agentruntime.RecordedResultRef{InvocationID: "own", Sequence: 1, SHA256: hex.EncodeToString(sum[:])}}
	view, err := (agentruntime.ReceiptCondenser{Policy: agentruntime.DefaultContextDeliveryPolicy()}).Condense(context.Background(), []agentruntime.Message{m})
	if err != nil || len(view[0].Content) > 8192 {
		t.Fatal(err)
	}
	if checkClaimedTestEvidence("TestRequired proves criterion", map[string]agenttools.Result{"raw": evidence}) == nil {
		t.Fatal("SKIP became PASS")
	}
	var projected agenttools.Result
	_ = json.Unmarshal([]byte(view[0].Content), &projected)
	if checkClaimedTestEvidence("TestRequired proves criterion", map[string]agenttools.Result{"projected": projected}) == nil {
		t.Fatal("receipt presentation accepted as execution proof")
	}
	evidence.TestEvidence.Tests[0].Status = "PASS"
	if err := checkClaimedTestEvidence("TestRequired proves criterion", map[string]agenttools.Result{"actual": evidence}); err != nil {
		t.Fatal(err)
	}
}

func TestPreparedSessionRejectsInvalidContextPolicy(t *testing.T) {
	brief, digest, profile := testBriefAndProfile()
	profile.BaseURL = "http://127.0.0.1:1/v1" // Preparation only; no model request.
	binding := &testBinding{root: t.TempDir()}
	policy := agentruntime.DefaultContextDeliveryPolicy()
	policy.ResultBytes = policy.HistoryBytes + 1
	cfg := Config{Bindings: binding, Gateway: agenttools.Gateway{Bindings: binding, Host: agenttools.Host{Timeout: time.Second}}, Journal: &testJournal{}, Profile: profile, ContextDelivery: &policy, HTTP: http.DefaultClient}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	valid := cfg
	valid.ContextDelivery = nil
	if _, err := PrepareReadOnly(ctx, brief, digest, valid); err != nil {
		t.Fatalf("valid control fixture rejected: %v", err)
	}
	if _, err := PrepareReadOnly(ctx, brief, kernel.Digest(digest), cfg); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("invalid policy admitted: %v", err)
	}
}
