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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

type boundaryJournal struct {
	mu      sync.Mutex
	entries []agentruntime.Entry
}

func (journal *boundaryJournal) Load(_ context.Context, _ string) ([]agentruntime.Entry, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	return append([]agentruntime.Entry(nil), journal.entries...), nil
}

func (journal *boundaryJournal) Append(_ context.Context, id string, expected uint64, kind agentruntime.Kind, payload json.RawMessage) (agentruntime.Entry, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if uint64(len(journal.entries)) != expected {
		return agentruntime.Entry{}, agentruntime.ErrConflict
	}
	entry := agentruntime.Entry{InvocationID: id, Sequence: expected + 1, Kind: kind, Payload: append(json.RawMessage(nil), payload...)}
	journal.entries = append(journal.entries, entry)
	return entry, nil
}

type boundaryControl struct {
	mu        sync.Mutex
	digest    string
	owner     string
	epoch     uint64
	until     time.Time
	cancelled bool
}

func (control *boundaryControl) Claim(_ context.Context, _, digest, owner string, now time.Time, ttl time.Duration) (agentruntime.RunLease, error) {
	control.mu.Lock()
	defer control.mu.Unlock()
	if control.digest != "" && control.digest != digest {
		return agentruntime.RunLease{}, agentruntime.ErrConflict
	}
	control.digest = digest
	if control.owner != "" && control.until.After(now) {
		return agentruntime.RunLease{Epoch: control.epoch, CancelRequested: control.cancelled}, nil
	}
	control.epoch++
	control.owner = owner
	control.until = now.Add(ttl)
	return agentruntime.RunLease{Acquired: true, Epoch: control.epoch, CancelRequested: control.cancelled}, nil
}

func (control *boundaryControl) Renew(_ context.Context, _, digest, owner string, epoch uint64, now time.Time, ttl time.Duration) (agentruntime.Renewal, error) {
	control.mu.Lock()
	defer control.mu.Unlock()
	if control.digest != digest || control.owner != owner || control.epoch != epoch || !control.until.After(now) {
		return agentruntime.Renewal{}, nil
	}
	control.until = now.Add(ttl)
	return agentruntime.Renewal{Held: true, CancelRequested: control.cancelled}, nil
}

func (control *boundaryControl) RequestCancel(_ context.Context, _, digest string) error {
	control.mu.Lock()
	defer control.mu.Unlock()
	if control.digest != "" && control.digest != digest {
		return agentruntime.ErrConflict
	}
	control.digest = digest
	control.cancelled = true
	return nil
}

func (control *boundaryControl) Release(_ context.Context, _, digest, owner string, epoch uint64, now time.Time) error {
	control.mu.Lock()
	defer control.mu.Unlock()
	if control.digest != digest || control.owner != owner || control.epoch != epoch {
		return agentruntime.ErrConflict
	}
	control.owner = ""
	control.until = now
	return nil
}

type boundaryBinding struct{ root string }

func (binding boundaryBinding) BindToolInvocation(context.Context, kernel.UUIDv7, kernel.Digest) (agenttools.Authority, error) {
	return agenttools.Authority{WorkspaceRoot: binding.root, Permissions: []string{"repository.read"}}, nil
}

type cancelRejectingEffectBinding struct {
	inner     nativeEffectBinding
	cancelled *atomic.Bool
}

func (binding cancelRejectingEffectBinding) BindToolInvocation(ctx context.Context, id kernel.UUIDv7, digest kernel.Digest) (agenttools.Authority, error) {
	if binding.cancelled.Load() {
		return agenttools.Authority{}, agenttools.ErrStaleBinding
	}
	return binding.inner.BindToolInvocation(ctx, id, digest)
}

func newBoundaryFixture(t *testing.T, server *httptest.Server) (*Boundary, application.ExecutionBrief, kernel.Digest, *boundaryJournal) {
	t.Helper()
	brief, _, profile := testBriefAndProfile()
	brief.DeadlineAt = time.Now().Add(4 * time.Second)
	encoded, err := json.Marshal(brief)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(encoded)
	digest := kernel.Digest(hex.EncodeToString(sum[:]))
	profile.BaseURL = server.URL + "/v1"
	binding := boundaryBinding{root: t.TempDir()}
	journal := &boundaryJournal{}
	control := &boundaryControl{}
	boundary := &Boundary{
		Control: control, Journal: journal, Owner: "test-daemon", LeaseTTL: time.Second, Heartbeat: 100 * time.Millisecond,
		Configure: func(context.Context, application.ExecutionBrief) (Config, error) {
			return Config{Bindings: binding, Gateway: agenttools.Gateway{Bindings: binding, Host: agenttools.Host{Timeout: time.Second}}, HTTP: server.Client(), Profile: profile}, nil
		},
	}
	return boundary, brief, digest, journal
}

func waitBoundaryState(t *testing.T, boundary *Boundary, brief application.ExecutionBrief, digest kernel.Digest, want application.ExternalExecutionState) application.ExternalExecutionObservation {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		observation, err := boundary.Inspect(ctx, brief, string(brief.InvocationID), digest)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if observation.State == want {
			return observation
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("native boundary did not reach %s", want)
	return application.ExternalExecutionObservation{}
}

func TestBoundaryDuplicateStartAndTerminalReplay(t *testing.T) {
	entered := make(chan struct{}, 1)
	unblock := make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-unblock:
		case <-request.Context().Done():
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"done"}}]}`))
	}))
	defer server.Close()
	boundary, brief, digest, journal := newBoundaryFixture(t, server)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	first, err := boundary.Start(ctx, brief, digest)
	if err != nil || first.State != application.ExternalRunning {
		t.Fatalf("start = %+v, %v", first, err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("model request did not start")
	}
	second, err := boundary.Start(ctx, brief, digest)
	if err != nil || second.State != application.ExternalRunning || calls.Load() != 1 {
		t.Fatalf("duplicate start = %+v, %v, calls=%d", second, err, calls.Load())
	}
	close(unblock)
	finished := waitBoundaryState(t, boundary, brief, digest, application.ExternalSucceeded)
	if string(finished.Output) != "done" || calls.Load() != 1 {
		t.Fatalf("finished = %+v, calls=%d", finished, calls.Load())
	}
	entries, _ := journal.Load(ctx, string(brief.InvocationID))
	if len(entries) != 3 || entries[2].Kind != agentruntime.Finished {
		t.Fatalf("journal = %+v", entries)
	}
}

func TestBoundaryCancellationPersists(t *testing.T) {
	entered := make(chan struct{}, 1)
	stopHandler := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		entered <- struct{}{}
		select {
		case <-request.Context().Done():
		case <-stopHandler:
		}
	}))
	defer server.Close()
	defer close(stopHandler)
	boundary, brief, digest, journal := newBoundaryFixture(t, server)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := boundary.Start(ctx, brief, digest); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("model request did not start")
	}
	if _, err := boundary.Cancel(ctx, brief, string(brief.InvocationID), digest); err != nil {
		t.Fatal(err)
	}
	finished := waitBoundaryState(t, boundary, brief, digest, application.ExternalCancelled)
	if finished.State != application.ExternalCancelled {
		t.Fatal(finished.State)
	}
	entries, _ := journal.Load(ctx, string(brief.InvocationID))
	if entries[len(entries)-1].Kind != agentruntime.Cancelled {
		t.Fatalf("journal = %+v", entries)
	}
}

func TestBoundaryCancelBeforeWorkerPersistsWithoutModelCall(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()
	boundary, brief, digest, journal := newBoundaryFixture(t, server)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := boundary.Cancel(ctx, brief, string(brief.InvocationID), digest)
	if err != nil || result.State != application.ExternalCancelled || calls.Load() != 0 {
		t.Fatalf("pre-start cancel = %+v, %v, model calls=%d", result, err, calls.Load())
	}
	entries, _ := journal.Load(ctx, string(brief.InvocationID))
	if len(entries) != 2 || entries[0].Kind != agentruntime.Started || entries[1].Kind != agentruntime.Cancelled {
		t.Fatalf("pre-start journal = %+v", entries)
	}
	// A new boundary instance (simulated daemon restart) sees the same result.
	restarted := &Boundary{Control: boundary.Control, Journal: journal, Configure: boundary.Configure, Owner: "restarted-daemon", LeaseTTL: time.Second, Heartbeat: 100 * time.Millisecond}
	replayed, err := restarted.Inspect(ctx, brief, string(brief.InvocationID), digest)
	if err != nil || replayed.State != application.ExternalCancelled || calls.Load() != 0 {
		t.Fatalf("restarted cancel = %+v, %v, model calls=%d", replayed, err, calls.Load())
	}
}

func TestEffectfulBoundaryCancelReconcilesAppliedWriteWithoutModelCall(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()
	brief, _, profile := testBriefAndProfile()
	brief.Purpose = kernel.PurposeImplementation
	brief.DeadlineAt = time.Now().Add(4 * time.Second)
	brief.RoleGrounding.Permissions = []string{"repository.edit"}
	encoded, _ := json.Marshal(brief)
	sum := sha256.Sum256(encoded)
	digest := kernel.Digest(hex.EncodeToString(sum[:]))
	profile.BaseURL = server.URL + "/v1"
	profile.AllowedReadTools = nil
	profile.AllowedEffectTools = []string{"write_file"}
	root := t.TempDir()
	liveCancelled := &atomic.Bool{}
	binding := cancelRejectingEffectBinding{inner: nativeEffectBinding{root: root, policy: brief.EffectPolicyDigest}, cancelled: liveCancelled}
	ledger := &nativeEffectLedger{failComplete: true}
	journal := &boundaryJournal{}
	boundary := &Boundary{
		Control: &boundaryControl{}, Journal: journal, Owner: "effect-test", LeaseTTL: time.Second, Heartbeat: 100 * time.Millisecond,
		Configure: func(context.Context, application.ExecutionBrief) (Config, error) {
			return Config{Bindings: binding, Gateway: agenttools.Gateway{Bindings: binding, Host: agenttools.Host{Timeout: time.Second}}, Effects: ledger, HTTP: server.Client(), Profile: profile}, nil
		},
	}
	arguments := json.RawMessage(`{"path":"note.txt","content":"new content","expected_sha256":""}`)
	start, _ := json.Marshal(struct {
		RequestDigest   string                        `json:"request_digest"`
		Prompt          string                        `json:"prompt"`
		SystemPrompt    string                        `json:"system_prompt"`
		EffectAuthority *agentruntime.EffectAuthority `json:"effect_authority"`
	}{string(digest), string(encoded), brief.RoleGrounding.Instructions,
		&agentruntime.EffectAuthority{WorkspaceRoot: root, Permissions: []string{"repository.edit"}, Purpose: string(brief.Purpose), EffectPolicyDigest: string(brief.EffectPolicyDigest)}})
	turn, _ := json.Marshal(agentruntime.Completion{ToolCalls: []agentruntime.ToolCall{{ID: "write-1", Name: "write_file", Arguments: arguments}}})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := journal.Append(ctx, string(brief.InvocationID), 0, agentruntime.Started, start); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.Append(ctx, string(brief.InvocationID), 1, agentruntime.ModelTurn, turn); err != nil {
		t.Fatal(err)
	}
	gateway := agenttools.MutationGateway{Bindings: binding, Host: agenttools.Host{Timeout: time.Second}, Ledger: ledger, Policy: brief.EffectPolicyDigest}
	request := agenttools.Request{InvocationID: brief.InvocationID, RequestDigest: digest, ToolCallID: "write-1", Call: agenttools.Call{Name: "write_file", Arguments: arguments}}
	if _, err := gateway.Execute(ctx, request); !errors.Is(err, agenttools.ErrEffectUncertain) {
		t.Fatalf("lost receipt = %v", err)
	}
	liveCancelled.Store(true)
	result, err := boundary.Cancel(ctx, brief, string(brief.InvocationID), digest)
	if err != nil || result.State != application.ExternalCancelled || calls.Load() != 0 {
		t.Fatalf("cancel = %+v, %v; model calls=%d", result, err, calls.Load())
	}
	entries, _ := journal.Load(ctx, string(brief.InvocationID))
	if len(entries) != 4 || entries[2].Kind != agentruntime.ToolDone || entries[3].Kind != agentruntime.Cancelled {
		t.Fatalf("reconciled cancellation journal = %+v", entries)
	}
	content, err := os.ReadFile(filepath.Join(root, "note.txt"))
	if err != nil || string(content) != "new content" {
		t.Fatalf("physical effect = %q, %v", content, err)
	}
}

func TestBoundaryRecoversRecordedModelTurn(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	boundary, brief, digest, journal := newBoundaryFixture(t, server)
	encoded, _ := json.Marshal(brief)
	start, _ := json.Marshal(map[string]string{
		"request_digest": string(digest), "prompt": string(encoded), "system_prompt": brief.RoleGrounding.Instructions,
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := journal.Append(ctx, string(brief.InvocationID), 0, agentruntime.Started, start); err != nil {
		t.Fatal(err)
	}
	turn := json.RawMessage(`{"text":"recovered"}`)
	if _, err := journal.Append(ctx, string(brief.InvocationID), 1, agentruntime.ModelTurn, turn); err != nil {
		t.Fatal(err)
	}
	result := waitBoundaryState(t, boundary, brief, digest, application.ExternalSucceeded)
	if string(result.Output) != "recovered" || calls.Load() != 0 {
		t.Fatalf("result = %+v, model calls=%d", result, calls.Load())
	}
	entries, _ := journal.Load(ctx, string(brief.InvocationID))
	if len(entries) != 3 || entries[2].Kind != agentruntime.Finished {
		t.Fatalf("journal = %+v", entries)
	}
}

func TestBoundaryRejectsDifferentJournalRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		t.Error("model should not be called")
	}))
	defer server.Close()
	boundary, brief, digest, journal := newBoundaryFixture(t, server)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := json.RawMessage(`{"request_digest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","prompt":"wrong"}`)
	if _, err := journal.Append(ctx, string(brief.InvocationID), 0, agentruntime.Started, start); err != nil {
		t.Fatal(err)
	}
	if _, err := boundary.Start(ctx, brief, digest); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("accepted journal drift: %v", err)
	}
}

func TestBoundarySupersededBriefCancelsOldDigestOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		t.Error("model should not be called")
	}))
	defer server.Close()
	boundary, oldBrief, oldDigest, journal := newBoundaryFixture(t, server)
	encodedOld, _ := json.Marshal(oldBrief)
	start, _ := json.Marshal(map[string]string{
		"request_digest": string(oldDigest), "prompt": string(encodedOld), "system_prompt": oldBrief.RoleGrounding.Instructions,
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := journal.Append(ctx, string(oldBrief.InvocationID), 0, agentruntime.Started, start); err != nil {
		t.Fatal(err)
	}
	newBrief := oldBrief
	newBrief.RoleGrounding.Instructions = "replacement instructions"
	encodedNew, _ := json.Marshal(newBrief)
	sum := sha256.Sum256(encodedNew)
	newDigest := kernel.Digest(hex.EncodeToString(sum[:]))
	result, err := boundary.ReconcileSuperseded(ctx, newBrief, string(oldBrief.InvocationID), oldDigest, newDigest)
	if err != nil || result.State != application.ExternalCancelled || result.RequestDigest != oldDigest {
		t.Fatalf("superseded result = %+v, %v", result, err)
	}
	entries, _ := journal.Load(ctx, string(oldBrief.InvocationID))
	if len(entries) != 2 || entries[1].Kind != agentruntime.Cancelled {
		t.Fatalf("superseded journal = %+v", entries)
	}
}
