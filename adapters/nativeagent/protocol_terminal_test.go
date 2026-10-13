package nativeagent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

type rejectedProtocolSubmissions struct{ calls int }

func (m *rejectedProtocolSubmissions) Complete(context.Context, []agentruntime.Message) (agentruntime.Completion, error) {
	m.calls++
	return agentruntime.Completion{ToolCalls: []agentruntime.ToolCall{{ID: fmt.Sprintf("rejected-%d", m.calls), Name: "submit_result", Arguments: json.RawMessage(`{"schema_version":"1.0.0","result_type":"FEATURE_SPECIFICATION","stories":[]}`)}}}, nil
}

// Use the real session/runner to exhaust its original three-submission budget.
// Finalization must not start another model turn or rewrite any rejected draft.
func TestProtocolBoundaryPublishesTerminalAfterRejectedSubmissions(t *testing.T) {
	for _, purpose := range []kernel.WorkPurpose{kernel.PurposeHandoff, kernel.PurposeReplan} {
		for _, tc := range []struct {
			name  string
			cause error
			kind  agentruntime.Kind
		}{
			{"failure", nil, agentruntime.Failed}, {"cancel", errCancelRequested, agentruntime.Cancelled}, {"deadline", context.DeadlineExceeded, agentruntime.TimedOut},
		} {
			t.Run(string(purpose)+"/"+tc.name, func(t *testing.T) {
				ctx, stop := context.WithTimeout(t.Context(), 5*time.Second)
				defer stop()
				server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("terminal reconciliation called provider") }))
				defer server.Close()
				brief, _, profile := testBriefAndProfile()
				brief.Task.TaskID = kernel.UUIDv7("00000000-0000-7000-8000-000000000961")
				brief.Task.SourceDigest = kernel.Digest(strings.Repeat("a", 64))
				brief.Purpose = purpose
				brief.ResultProtocol = &application.ExecutionResultProtocol{SchemaVersion: "1.0.0", Marker: application.OrganizationalResultMarker, Outcomes: []string{"STRUCTURED_HANDOFF"}}
				contract := testResultContract(brief)
				value := json.RawMessage(`{"remaining_task_allowances":{"ordinary":2,"repair":1}}`)
				sum := sha256.Sum256(value)
				input := &ResultInput{Evidence: kernel.EvidenceRef{EvidenceID: kernel.UUIDv7("00000000-0000-7000-8000-000000000962"), SHA256: kernel.Digest(hex.EncodeToString(sum[:]))}, Value: value}
				brief.Evidence = append(brief.Evidence, input.Evidence)
				encoded, _ := json.Marshal(brief)
				sum = sha256.Sum256(encoded)
				digest := kernel.Digest(hex.EncodeToString(sum[:]))
				journal := &boundaryJournal{}
				control := &boundaryControl{}
				binding := &testBinding{root: t.TempDir()}
				profile.BaseURL = server.URL + "/v1"
				config := Config{Bindings: binding, Gateway: agenttools.Gateway{Bindings: binding}, Journal: journal, HTTP: server.Client(), Profile: profile, ResultContract: &contract, ResultInput: input}
				boundary := &Boundary{Journal: journal, Control: control, Owner: "protocol-terminal-test", LeaseTTL: time.Second, Heartbeat: 100 * time.Millisecond, Configure: func(context.Context, application.ExecutionBrief) (Config, error) { return config, nil }}
				session, err := PrepareReadOnly(ctx, brief, digest, config)
				if err != nil {
					t.Fatal(err)
				}
				model := &rejectedProtocolSubmissions{}
				session.Runner.Model = model
				_, runErr := session.Run(ctx)
				if runErr == nil || model.calls != 3 || session.Runner.MaxFinalSubmissions != 3 {
					t.Fatal("original finite rejection limit", runErr, model.calls)
				}
				entries, err := journal.Load(ctx, string(brief.InvocationID))
				if err != nil {
					t.Fatal(err)
				}
				before, _ := json.Marshal(entries)
				if _, terminal, err := boundary.observeJournal(ctx, brief, digest, entries); err != nil || terminal {
					t.Fatal("valid nonterminal inspection", terminal, err)
				}
				lease, err := control.Claim(ctx, string(brief.InvocationID), string(digest), boundary.Owner, time.Now(), boundary.LeaseTTL)
				if err != nil || !lease.Acquired {
					t.Fatal(err)
				}
				worker, cancel := context.WithCancelCause(ctx)
				defer cancel(nil)
				if tc.cause != nil {
					cancel(tc.cause)
				}
				boundary.finishWorker(worker, brief, digest, lease.Epoch, runErr)
				after, err := journal.Load(ctx, string(brief.InvocationID))
				if err != nil {
					t.Fatal(err)
				}
				if len(after) != len(entries)+1 || after[len(entries)].Kind != tc.kind || control.owner != "" {
					t.Fatalf("stranded invocation: entries=%d -> %d last=%s owner=%s", len(entries), len(after), after[len(after)-1].Kind, control.owner)
				}
				prefix, _ := json.Marshal(after[:len(entries)])
				if !bytes.Equal(before, prefix) || model.calls != 3 {
					t.Fatal("historical drafts rewritten or model retried")
				}
				if tc.kind == agentruntime.Failed {
					failure, err := agentruntime.DecodeFailure(after[len(entries)].Payload)
					if err != nil || failure.Retryable {
						t.Fatal("failure classification changed", failure, err)
					}
				}
				observed, err := boundary.Inspect(ctx, brief, string(brief.InvocationID), digest)
				if err != nil || observed.State == application.ExternalRunning {
					t.Fatal("terminal replay", observed, err)
				}
				again, _ := journal.Load(ctx, string(brief.InvocationID))
				if len(again) != len(after) || model.calls != 3 {
					t.Fatal("terminal inspection re-executed work")
				}
			})
		}
	}
}

// Reconciliation and inspection must reject the same tampering. A structured
// prompt is accepted only by exact reconstruction from server-resolved inputs.
func TestProtocolPreTerminalBindingUsesInspectionChecks(t *testing.T) {
	for _, variant := range []string{"workflow", "validation"} {
		t.Run(variant, func(t *testing.T) {
			brief, _, _ := testBriefAndProfile()
			brief.Purpose = kernel.PurposeHandoff
			brief.Task.TaskID = kernel.UUIDv7("00000000-0000-7000-8000-000000000961")
			brief.Task.SourceDigest = kernel.Digest(strings.Repeat("a", 64))
			brief.ResultProtocol = &application.ExecutionResultProtocol{SchemaVersion: "1.0.0", Marker: application.OrganizationalResultMarker, Outcomes: []string{"STRUCTURED_HANDOFF"}}
			contract := testResultContract(brief)
			config := Config{ResultContract: &contract}
			if variant == "validation" {
				brief.Purpose = kernel.PurposeValidation
				brief.ResultProtocol.Marker = application.ValidationResultMarker
				brief.ResultProtocol.Outcomes = []string{"PASS", "FAIL", "BLOCKED", "INCONCLUSIVE"}
				config.ResultContract = nil
			}
			raw, _ := json.Marshal(brief)
			sum := sha256.Sum256(raw)
			digest := kernel.Digest(hex.EncodeToString(sum[:]))
			prompt, err := nativeProtocolPrompt(brief, digest, config.ResultContract, nil)
			if err != nil {
				t.Fatal(err)
			}
			base := []agentruntime.Entry{{InvocationID: string(brief.InvocationID), Sequence: 1, Kind: agentruntime.Started, Payload: mustPromptJSON(t, map[string]any{"request_digest": digest, "prompt": prompt})}, {InvocationID: string(brief.InvocationID), Sequence: 2, Kind: agentruntime.ToolDone, Payload: json.RawMessage(`{"call_id":"rejected","name":"submit_result","output":null,"error":"invalid draft"}`)}}
			for _, change := range []string{"none", "prompt", "sequence", "invocation", "digest", "contract", "resolver-error"} {
				t.Run(change, func(t *testing.T) {
					entries := append([]agentruntime.Entry(nil), base...)
					cfg := config
					switch change {
					case "prompt":
						entries[0].Payload = mustPromptJSON(t, map[string]any{"request_digest": digest, "prompt": prompt + " "})
					case "sequence":
						entries[1].Sequence = 3
					case "invocation":
						entries[1].InvocationID = "wrong"
					case "digest":
						entries[0].Payload = mustPromptJSON(t, map[string]any{"request_digest": strings.Repeat("b", 64), "prompt": prompt})
					case "contract":
						if variant == "validation" {
							cfg.ResultContract = &contract
						} else {
							changed := contract
							changed.SchemaRef = "other/contract"
							cfg.ResultContract = &changed
						}
					}
					journal := &testJournal{entries: entries}
					boundary := &Boundary{Journal: journal, LeaseTTL: time.Second, Configure: func(context.Context, application.ExecutionBrief) (Config, error) {
						if change == "resolver-error" {
							return Config{}, errors.New("resolver unavailable")
						}
						return cfg, nil
					}}
					_, _, inspectErr := boundary.observeJournal(t.Context(), brief, digest, entries)
					terminalErr := boundary.reconcileBeforeTerminal(brief, digest, 1)
					if change == "none" {
						if inspectErr != nil || terminalErr != nil {
							t.Fatal("valid prompt rejected", inspectErr, terminalErr)
						}
					} else if inspectErr == nil || terminalErr == nil {
						t.Fatal("tampering/configuration failure accepted", inspectErr, terminalErr)
					}
					if len(journal.entries) != 2 {
						t.Fatal("read-only reconciliation changed journal")
					}
				})
			}
		})
	}
}
