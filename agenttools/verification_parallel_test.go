package agenttools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/internal/verification"
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
