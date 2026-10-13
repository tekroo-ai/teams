//go:build mongo_integration && native_qualification

package operationalruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	storage "github.com/tekroo-ai/teams/adapters/mongo"
	"github.com/tekroo-ai/teams/adapters/nativeagent"
	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type protocolTerminalSignalJournal struct {
	agentruntime.Journal
	terminal chan struct{}
	once     sync.Once
}

func (j *protocolTerminalSignalJournal) Append(ctx context.Context, id string, expected uint64, kind agentruntime.Kind, payload json.RawMessage) (agentruntime.Entry, error) {
	e, err := j.Journal.Append(ctx, id, expected, kind, payload)
	if err == nil && slices.Contains([]agentruntime.Kind{agentruntime.Finished, agentruntime.Failed, agentruntime.Cancelled, agentruntime.TimedOut}, kind) {
		j.once.Do(func() { close(j.terminal) })
	}
	return e, err
}

// Real signed-role providers, real isolated Mongo ownership/journal fencing,
// and the public Boundary lifecycle. Rejected negative fixtures are expected
// FAILED executions, but qualification PASS requires their exact terminal
// evidence, lease release and no restart or invocation retry.
func TestQualifyNativeProtocolTerminalLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 80*time.Minute)
	defer cancel()
	config, team, profiles, output := terminalQualificationEnvironment(t, ctx)
	if len(profiles) != 7 {
		t.Fatal("expected seven unchanged signed profiles")
	}
	process, uri := startRuntimeMongod(t)
	defer stopRuntimeMongod(process)
	const database = "native_protocol_terminal_qualification"
	store, err := storage.Open(ctx, storage.Config{URI: uri, Database: database, ContractIdentity: kernel.ContractIdentity, ManifestSHA256: phase4ManifestSHA, MigrationLevel: 1, Policy: integratedPolicy(), BacklogLimit: 1024, DeliveryPolicyRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer closeRuntimeStore(t, store)
	client, err := driver.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(ctx)
	pm := slices.IndexFunc(profiles, func(p nativeagent.Profile) bool { return p.RoleFQRN == "project-manager" })
	if pm < 0 {
		t.Fatal("PM absent")
	}
	cases := append(slices.Clone(profiles), profiles[pm])
	for index, p := range cases {
		correcting := index == len(profiles)
		name := string(p.RoleFQRN) + "-exhaustion"
		if correcting {
			name = "project-manager-correction"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
			defer cancel()
			root := terminalQualificationWorkspace(t, output, name)
			id := deterministicOperationalUUID("protocol-terminal-qualified", root)
			description := "Synthetic negative terminal-lifecycle qualification, not feature work. The deterministic fixture intentionally has no admissible completion. Submit ordinary=3 and the exact source_marker supplied in result_input.value. Retain and obey each rejection within the original finite three-submission limit. Do not invent a success, run tests, edit files or start another invocation. No repository discovery is required."
			if correcting {
				description = "Synthetic bounded failure-and-correction qualification, not feature work. Continue the original invocation from its retained invalid ordinary=3 draft and exact ATTEMPT_ALLOWANCE_EXHAUSTED receipt. Correct ordinary to the remaining admitted value 2, retaining the exact original source_marker from result_input.value. Submit that corrected result without any invocation retry, tests, edits or repository discovery."
			}
			brief := terminalQualificationBrief(t, team, p, id, root, description)
			brief.Task.Title = "Protocol terminal-lifecycle delta qualification"
			brief.Task.AcceptanceCriteria = []string{"Preserve original evidence and finite submission limits; report only the admitted synthetic result."}
			marker := string(digestBytes([]byte(root + " exact immutable source")))
			inputRaw, _ := json.Marshal(map[string]any{"remaining_ordinary": 2, "source_marker": marker, "negative_fixture": !correcting})
			inputRef := kernel.EvidenceRef{EvidenceID: deterministicOperationalUUID("protocol-input", root), SHA256: digestBytes(inputRaw)}
			brief.Evidence = append(brief.Evidence, inputRef)
			raw, _ := json.Marshal(brief)
			digest := digestBytes(raw)
			journal := &protocolTerminalSignalJournal{Journal: store, terminal: make(chan struct{})}
			cfg := terminalQualificationConfig(team, p, root, journal, &http.Client{Timeout: 10 * time.Minute, Transport: &nativeHTTPCapture{directory: output, scenario: name}}, config.Native.ContextDelivery)
			cfg.ResultInput = &nativeagent.ResultInput{Evidence: inputRef, Value: inputRaw}
			schema := json.RawMessage(`{"type":"object","additionalProperties":false,"required":["ordinary","source_marker"],"properties":{"ordinary":{"type":"integer","enum":[2,3]},"source_marker":{"type":"string","minLength":64,"maxLength":64}}}`)
			cfg.ResultContract = terminalQualificationContract(brief, schema)
			var assessments atomic.Int32
			cfg.ValidateFinalResult = func(_ context.Context, result []byte) error {
				assessments.Add(1)
				var v struct {
					Ordinary int    `json:"ordinary"`
					Marker   string `json:"source_marker"`
				}
				if json.Unmarshal(bytes.TrimPrefix(result, []byte(application.OrganizationalResultMarker+"\n")), &v) != nil || v.Marker != marker {
					return errors.New("exact original source marker required")
				}
				if !correcting {
					return errors.New("SYNTHETIC_TERMINAL_REJECTION: negative qualification has no admissible completion; retain the original draft and rejection, no retries or false success")
				}
				if v.Ordinary != 2 {
					return errors.New("ATTEMPT_ALLOWANCE_EXHAUSTED: remaining ordinary allowance is 2; correct ordinary from 3 to 2 while preserving the original source_marker")
				}
				return nil
			}
			var prefix []byte
			seededEntries := 0
			if correcting {
				lease, err := store.Claim(ctx, string(id), string(digest), "explicit-synthetic-seed", time.Now(), time.Minute)
				if err != nil || !lease.Acquired {
					t.Fatal("seed ownership", err)
				}
				owned := agentruntime.WithRunOwnership(ctx, agentruntime.RunOwnership{InvocationID: string(id), RequestDigest: string(digest), Owner: "explicit-synthetic-seed", Epoch: lease.Epoch})
				session := terminalQualificationSession(t, owned, brief, cfg)
				args, _ := json.Marshal(map[string]any{"ordinary": 3, "source_marker": marker})
				session.Runner.Model = &terminalQualificationModel{calls: []agentruntime.ToolCall{{ID: "explicit-invalid-draft", Name: "submit_result", Arguments: args}}}
				session.Runner.MaxTurns = 1
				if _, err := session.Run(owned); !errors.Is(err, agentruntime.ErrTurnLimit) {
					t.Fatal("seed rejection", err)
				}
				entries, err := store.Load(ctx, string(id))
				if err != nil {
					t.Fatal(err)
				}
				seededEntries = len(entries)
				prefix, _ = json.Marshal(entries)
				if err := store.Release(ctx, string(id), string(digest), "explicit-synthetic-seed", lease.Epoch, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			boundary := &nativeagent.Boundary{Journal: journal, Control: store, Owner: "isolated-qualification", LeaseTTL: 30 * time.Second, Heartbeat: time.Second, Configure: func(context.Context, application.ExecutionBrief) (nativeagent.Config, error) { return cfg, nil }}
			// After starting, even an assertion failure requests supported
			// cancellation only for this disposable invocation, never r73.
			t.Cleanup(func() {
				cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
				defer c()
				select {
				case <-journal.terminal:
				default:
					boundary.Cancel(cleanup, brief, string(id), digest)
					select {
					case <-journal.terminal:
					case <-cleanup.Done():
						t.Error("disposable worker did not drain after cancellation")
					}
				}
				entries, err := store.Load(cleanup, string(id))
				if err == nil {
					terminalQualificationWrite(t, output, string(id)+"-cleanup-journal.json", entries)
				}
			})
			started, err := boundary.Start(ctx, brief, digest)
			if err != nil || started.State != application.ExternalRunning {
				t.Fatal("boundary start", started, err)
			}
			select {
			case <-ctx.Done():
				t.Fatal("terminal execution deadline", ctx.Err())
			case <-journal.terminal:
			}
			// Append is durable before Release. Read the real ownership document
			// until the worker's existing finalization releases its exact lease.
			var lease bson.M
			releaseDeadline, stop := context.WithTimeout(ctx, 5*time.Second)
			defer stop()
			for {
				if err := client.Database(database).Collection("agent_run_control").FindOne(releaseDeadline, bson.D{{Key: "_id", Value: string(id)}}).Decode(&lease); err != nil {
					t.Fatal(err)
				}
				if lease["owner"] == "" {
					break
				}
				select {
				case <-releaseDeadline.Done():
					t.Fatal("terminal lease not released", lease)
				case <-time.After(10 * time.Millisecond):
				}
			}
			entries, err := store.Load(ctx, string(id))
			if err != nil {
				t.Fatal(err)
			}
			terminalQualificationWrite(t, output, string(id)+"-journal.json", entries)
			terminalQualificationWrite(t, output, name+"-raw-lease.json", lease)
			wantKind, wantState := agentruntime.Failed, application.ExternalFailed
			wantSubmissions := 3
			if correcting {
				wantKind, wantState, wantSubmissions = agentruntime.Finished, application.ExternalSucceeded, 2
				retained, _ := json.Marshal(entries[:seededEntries])
				if !bytes.Equal(prefix, retained) {
					t.Fatal("original draft or correction receipt changed")
				}
			}
			if entries[len(entries)-1].Kind != wantKind || int(assessments.Load()) != wantSubmissions {
				t.Fatal("terminal/submission allowance", entries[len(entries)-1].Kind, assessments.Load())
			}
			observed, err := boundary.Inspect(ctx, brief, string(id), digest)
			if err != nil || observed.State != wantState || observed.Retryable {
				t.Fatal("durable terminal observation", observed, err)
			}
			before, _ := json.Marshal(entries)
			for pass := 0; pass < 2; pass++ {
				replayed, err := boundary.ReconcileStart(ctx, brief, digest)
				if err != nil || replayed.State != wantState {
					t.Fatal("terminal replay", replayed, err)
				}
			}
			after, err := store.Load(ctx, string(id))
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(after)
			if !bytes.Equal(before, encoded) || int(assessments.Load()) != wantSubmissions {
				t.Fatal("replay changed journal or re-executed submission")
			}
			terminalQualificationWrite(t, output, name+"-result.json", map[string]any{"status": "PASS", "scope": "synthetic terminal lifecycle, not feature completion", "profile": p, "invocation_id": id, "state": observed.State, "terminal_kind": wantKind, "submissions": assessments.Load(), "scripted_seed_entries": seededEntries, "original_prefix_preserved": true, "terminal_lease_released": true, "terminal_replay_unchanged": true, "invocation_retries": 0, "observation": observed})
		})
		if t.Failed() {
			break
		}
	}
}
