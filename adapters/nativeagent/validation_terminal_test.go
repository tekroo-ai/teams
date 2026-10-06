package nativeagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

func TestTestOnlyBoundaryPublishesTerminalAfterRejectedSubmissions(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cause    error
		terminal agentruntime.Kind
		corrupt  bool
	}{
		{"failed", nil, agentruntime.Failed, false},
		{"cancelled", errCancelRequested, agentruntime.Cancelled, false},
		{"timed-out", context.DeadlineExceeded, agentruntime.TimedOut, false},
		{"forged-authority", nil, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("terminal reconciliation called model") }))
			defer server.Close()
			brief, _, profile := testBriefAndProfile()
			brief.Purpose = kernel.PurposeValidation
			brief.RoleGrounding.Permissions = []string{"repository.read", "test.execute"}
			brief.ResultProtocol = &application.ExecutionResultProtocol{SchemaVersion: "1.0.0", Marker: application.OrganizationalResultMarker}
			brief.MessageHandler = &application.MessageHandlerGrounding{ResultSchema: json.RawMessage(`{"type":"object","properties":{"outcome":{"type":"string"},"work_product":{"type":"object"}}}`)}
			encoded, _ := json.Marshal(brief)
			sum := sha256.Sum256(encoded)
			digest := kernel.Digest(hex.EncodeToString(sum[:]))
			profile.BaseURL = server.URL + "/v1"
			profile.AllowedEffectTools = []string{"run_go_tests"}
			root := t.TempDir()
			binding := staticReconciliationBinding{authority: agenttools.Authority{WorkspaceRoot: root, Permissions: brief.RoleGrounding.Permissions, Purpose: brief.Purpose, EffectPolicyDigest: brief.EffectPolicyDigest}}
			journal := &boundaryJournal{}
			control := &boundaryControl{}
			ledger := &nativeEffectLedger{}
			boundary := &Boundary{Control: control, Journal: journal, Owner: "test-only-boundary", LeaseTTL: time.Second, Heartbeat: 100 * time.Millisecond,
				Configure: func(context.Context, application.ExecutionBrief) (Config, error) {
					return Config{Bindings: binding, Gateway: agenttools.Gateway{Bindings: binding, Host: agenttools.Host{Timeout: time.Second}}, Effects: ledger, HTTP: server.Client(), Profile: profile}, nil
				},
			}
			systemPrompt, err := assembleInstructions(brief, profile, root)
			if err != nil {
				t.Fatal(err)
			}
			config, err := boundary.Configure(t.Context(), brief)
			if err != nil {
				t.Fatal(err)
			}
			config.Journal = journal
			session, err := PrepareWithEffects(t.Context(), brief, digest, config)
			if err != nil {
				t.Fatal(err)
			}
			snapshot := agentruntime.EffectAuthority{WorkspaceRoot: root, Permissions: brief.RoleGrounding.Permissions, Purpose: string(brief.Purpose), EffectPolicyDigest: string(brief.EffectPolicyDigest)}
			if tc.corrupt {
				snapshot.Permissions = []string{"repository.edit"}
			}
			start, _ := json.Marshal(map[string]any{"request_digest": string(digest), "prompt": session.Prompt, "system_prompt": systemPrompt, "final_tool": session.Runner.FinalTool, "effect_authority": snapshot})
			_, err = journal.Append(t.Context(), string(brief.InvocationID), 0, agentruntime.Started, start)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 3; i++ {
				call := agentruntime.ToolCall{ID: tc.name + string(rune('a'+i)), Name: "submit_result", Arguments: json.RawMessage(`{"outcome":"failed","work_product":{"schema_version":"1.0.0","outcome":"INCONCLUSIVE","reasons":["evidence incomplete"]}}`)}
				turn, _ := json.Marshal(agentruntime.Completion{ToolCalls: []agentruntime.ToolCall{call}})
				done, _ := json.Marshal(agentruntime.ToolResult{CallID: call.ID, Name: call.Name, Output: json.RawMessage(`null`), Error: "verdict mismatch"})
				_, err = journal.Append(t.Context(), string(brief.InvocationID), uint64(1+i*2), agentruntime.ModelTurn, turn)
				if err != nil {
					t.Fatal(err)
				}
				_, err = journal.Append(t.Context(), string(brief.InvocationID), uint64(2+i*2), agentruntime.ToolDone, done)
				if err != nil {
					t.Fatal(err)
				}
			}
			lease, err := control.Claim(t.Context(), string(brief.InvocationID), string(digest), boundary.Owner, time.Now(), time.Second)
			if err != nil || !lease.Acquired {
				t.Fatalf("lease: %+v %v", lease, err)
			}
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			if tc.cause != nil {
				cancel(tc.cause)
			}
			boundary.finishWorker(ctx, brief, digest, lease.Epoch, errors.New("final submission limit reached"))
			entries, _ := journal.Load(t.Context(), string(brief.InvocationID))
			if tc.corrupt {
				if len(entries) != 7 {
					t.Fatalf("forged authority published terminal: %+v", entries)
				}
			} else if len(entries) != 8 || entries[7].Kind != tc.terminal || control.owner != "" {
				t.Fatalf("test-only invocation stranded: entries=%d last=%s owner=%s reconcile=%v", len(entries), entries[len(entries)-1].Kind, control.owner, boundary.reconcileBeforeTerminal(brief, digest, lease.Epoch))
			}
			if ledger.record.ToolCallID != "" {
				t.Fatal("reconciliation created a new physical effect")
			}
		})
	}
}
