package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/internal/verification"
	"github.com/tekroo-ai/teams/kernel"
)

func TestFocusedGoToolRunsWhileBroadPreflightOwnsLane(t *testing.T) {
	root, _ := localTestFixture(t, "package localexecution\nimport \"testing\"\nfunc TestFixture(t *testing.T) {}\n")
	release, err := verification.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	host := cachedFixtureHost(t, Host{Timeout: 30 * time.Second, LocalTestExecution: true})
	result, err := host.runIsolatedGoTests(ctx, root, ".")
	if err != nil || result.ExitCode != 0 || !strings.Contains(result.Output, "ok") {
		t.Fatalf("focused tool blocked behind broad preflight: %+v %v", result, err)
	}
	if result.VerificationTimings.QueueNS > int64(time.Second) {
		t.Fatalf("free focused lane unexpectedly queued: %+v", result.VerificationTimings)
	}
}

func TestSinglePackageRecursiveCommittedToolDoesNotWaitForBroadLane(t *testing.T) {
	root, _ := localTestFixture(t, "package localexecution\n")
	if err := os.MkdirAll(filepath.Join(root, "operator", "dirty"), 0700); err != nil {
		t.Fatal(err)
	}
	writeSnapshotFixture(t, root, "operator/service_test.go", "package operator\nimport \"testing\"\nfunc TestFixture(t *testing.T) {}\n")
	fixtureGit(t, root, "add", "operator/service_test.go")
	fixtureGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "-qm", "operator fixture")
	// Dirty live source has a second package. The committed tool must classify
	// its pinned commit, not this unrelated uncommitted directory.
	writeSnapshotFixture(t, root, "operator/dirty/x.go", "package dirty\n")
	release, err := verification.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	host := cachedFixtureHost(t, Host{Timeout: 30 * time.Second, LocalTestExecution: true})
	result, err := host.runIsolatedGoTests(ctx, root, "./operator/...")
	if err != nil || result.ExitCode != 0 || !strings.Contains(result.Output, "operator") {
		t.Fatalf("recursive focused execution: %+v %v", result, err)
	}
}

type verificationBatchModel struct {
	calls []agentruntime.ToolCall
	turns int
}

func (model *verificationBatchModel) Complete(context.Context, []agentruntime.Message) (agentruntime.Completion, error) {
	model.turns++
	if model.turns == 1 {
		return agentruntime.Completion{ToolCalls: model.calls}, nil
	}
	return agentruntime.Completion{Text: "done"}, nil
}

type verificationBatchJournal struct {
	editTurnJournal
	fail      bool
	doneCount int
}

func (journal *verificationBatchJournal) Append(ctx context.Context, id string, seq uint64, kind agentruntime.Kind, payload json.RawMessage) (agentruntime.Entry, error) {
	if kind == agentruntime.ToolDone {
		journal.doneCount++
		if journal.fail && journal.doneCount == 2 {
			return agentruntime.Entry{}, errors.New("journal interruption")
		}
	}
	return journal.editTurnJournal.Append(ctx, id, seq, kind, payload)
}

func TestCommittedVerificationBatchUsesDurableGatewayAndOverlaps(t *testing.T) {
	for _, lostAck := range []bool{false, true} {
		t.Run(fmt.Sprintf("lost-completion-ack=%t", lostAck), func(t *testing.T) { qualifyCommittedVerificationBatch(t, lostAck) })
	}
}

func qualifyCommittedVerificationBatch(t *testing.T, lostAck bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	var mu sync.Mutex
	seen := map[string]int{}
	both := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path]++
		if len(seen) == 2 {
			select {
			case <-both:
			default:
				close(both)
			}
		}
		mu.Unlock()
		select {
		case <-both:
			_, _ = w.Write([]byte("both tests running"))
		case <-ctx.Done():
			http.Error(w, "no overlap", 504)
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	root, _ := localTestFixture(t, "package localexecution\n")
	var calls []agentruntime.ToolCall
	for i := range 2 {
		path := fmt.Sprintf("package%d", i)
		if err := os.MkdirAll(filepath.Join(root, path), 0700); err != nil {
			t.Fatal(err)
		}
		writeSnapshotFixture(t, root, path+"/overlap_test.go", fmt.Sprintf(`package overlap
import ("io";"net/http";"testing";"time")
func TestOverlap(t *testing.T) {
 c:=&http.Client{Timeout:30*time.Second};r,e:=c.Get(%q);if e!=nil {t.Fatal(e)};defer r.Body.Close()
 b,e:=io.ReadAll(r.Body);if e!=nil || r.StatusCode!=200 || string(b)!="both tests running" {t.Fatalf("barrier: %%q %%v",b,e)}
}
`, fmt.Sprintf("%s/%d", server.URL, i)))
		calls = append(calls, agentruntime.ToolCall{ID: path, Name: "run_go_tests", Arguments: json.RawMessage(fmt.Sprintf(`{"package":"./%s/..."}`, path))})
	}
	fixtureGit(t, root, "add", "package0", "package1")
	fixtureGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "-qm", "barrier fixture")
	digest := kernel.Digest(strings.Repeat("a", 64))
	var ledger EffectLedger = &memoryEffectLedger{}
	if lostAck {
		ledger = lostTestCompletionAck{ledger}
	}
	adapter := TestTurnAdapter{Gateway: TestGateway{
		Bindings: effectBinding{authority: Authority{WorkspaceRoot: root, Permissions: []string{"repository.read", "test.execute"}, Purpose: kernel.PurposeValidation, EffectPolicyDigest: digest}},
		Host:     cachedFixtureHost(t, Host{Timeout: 45 * time.Second, LocalTestExecution: true}), Ledger: ledger, Policy: digest},
		InvocationID: "00000000-0000-7000-8000-000000000955", RequestDigest: digest}
	journal := &verificationBatchJournal{fail: !lostAck}
	model := &verificationBatchModel{calls: calls}
	runner := agentruntime.Runner{Journal: journal, Model: model, Effects: adapter, MaxParallelEffects: 2,
		Tools: ReadOnlyTurnAdapter{Gateway: Gateway{Bindings: adapter.Gateway.Bindings, Host: adapter.Gateway.Host}, InvocationID: adapter.InvocationID, RequestDigest: digest}}
	if _, err := runner.Run(ctx, string(adapter.InvocationID), string(digest), "verify both packages"); err == nil || lostAck && !errors.Is(err, agentruntime.ErrEffectUncertain) {
		t.Fatalf("interruption/lost receipt hidden: %v", err)
	}
	journal.fail = false
	if output, err := runner.Run(ctx, string(adapter.InvocationID), string(digest), "verify both packages"); err != nil || output != "done" {
		t.Fatalf("restart: %q %v", output, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if seen["/0"] != 1 || seen["/1"] != 1 {
		t.Fatalf("execution did not overlap once or was repeated: %v", seen)
	}
	for _, call := range calls {
		output, found, err := adapter.ReconcileEffect(ctx, call)
		var result Result
		if err != nil || !found || json.Unmarshal(output, &result) != nil || result.ExitCode != 0 {
			t.Fatalf("durable completion: %s %v", output, err)
		}
	}
}

func TestConcurrentGoToolsSharingCompilerCacheActuallyOverlap(t *testing.T) {
	// The two real test binaries must meet at a barrier, not merely finish
	// eventually. The original single-slot scheduler and shared writable path
	// both fail this fixture. Every candidate still gets fresh execution.
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	var mu sync.Mutex
	seen := make(map[string]bool)
	both := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = true
		if len(seen) == 2 {
			select {
			case <-both:
			default:
				close(both)
			}
		}
		mu.Unlock()
		select {
		case <-both:
			_, _ = w.Write([]byte("both tests running"))
		case <-ctx.Done():
			http.Error(w, "tests never overlapped", http.StatusGatewayTimeout)
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	roots := make([]string, 2)
	for i := range roots {
		roots[i], _ = localTestFixture(t, fmt.Sprintf(`package localexecution
import ("io"; "net/http"; "testing"; "time")
func TestOverlap(t *testing.T) {
 client := &http.Client{Timeout: 30*time.Second}
 response, err := client.Get(%q)
 if err != nil { t.Fatal(err) }
 defer response.Body.Close()
 body, err := io.ReadAll(response.Body)
 if err != nil || response.StatusCode != 200 || string(body) != "both tests running" { t.Fatalf("barrier: %%q %%v", body, err) }
}
`, fmt.Sprintf("%s/%d", server.URL, i)))
	}
	host := cachedFixtureHost(t, Host{Timeout: 45 * time.Second, LocalTestExecution: true})
	type observation struct {
		result Result
		err    error
	}
	completed := make(chan observation, 2)
	for _, root := range roots {
		go func() { result, err := host.runIsolatedGoTests(ctx, root, "."); completed <- observation{result, err} }()
	}
	for range roots {
		observed := <-completed
		if observed.err != nil || observed.result.ExitCode != 0 || !strings.Contains(observed.result.Output, "ok") {
			t.Errorf("concurrent real test execution: %+v %v", observed.result, observed.err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 {
		t.Fatal("two real binaries did not reach the barrier")
	}
}
