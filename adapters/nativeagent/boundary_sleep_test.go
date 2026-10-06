package nativeagent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/application"
)

type sleepTransport struct {
	calls     atomic.Int32
	entered   chan struct{}
	returnOld chan struct{}
}

func (transport *sleepTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	text := "resumed"
	if transport.calls.Add(1) == 1 {
		close(transport.entered)
		// Deliberately ignore cancellation: an old request can deliver its
		// response after wake, before the heartbeat goroutine gets scheduled.
		<-transport.returnOld
		text = "stale"
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{"choices":[{"finish_reason":"stop","message":{"content":"` + text + `"}}]}`)), Request: request}, nil
}

func TestBoundarySleepDrainsOldWorkerBeforeResumingSameInvocation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected server call") }))
	defer server.Close()
	boundary, brief, digest, journal := newBoundaryFixture(t, server)
	transport := &sleepTransport{entered: make(chan struct{}), returnOld: make(chan struct{})}
	configure := boundary.Configure
	boundary.Configure = func(ctx context.Context, b application.ExecutionBrief) (Config, error) {
		config, err := configure(ctx, b)
		config.HTTP = &http.Client{Transport: transport}
		return config, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := boundary.Start(ctx, brief, digest); err != nil {
		t.Fatal(err)
	}
	select {
	case <-transport.entered:
	case <-ctx.Done():
		t.Fatal("model did not start")
	}
	control := boundary.Control.(*boundaryControl)
	control.mu.Lock()
	control.until = time.Now().Add(-time.Second)
	control.mu.Unlock()
	observed, err := boundary.Inspect(ctx, brief, string(brief.InvocationID), digest)
	if err != nil || observed.State != application.ExternalRunning {
		t.Fatalf("wake inspect = %+v, %v", observed, err)
	}
	// An inspection must not register a replacement while the old call is
	// still capable of returning. No wall-clock sleep is needed to force it.
	boundary.mu.Lock()
	control.mu.Lock()
	epoch := control.epoch
	control.mu.Unlock()
	boundary.mu.Unlock()
	if epoch != 1 {
		close(transport.returnOld)
		t.Fatalf("reclaimed before worker drained: epoch=%d", epoch)
	}
	close(transport.returnOld)
	finished := waitBoundaryState(t, boundary, brief, digest, application.ExternalSucceeded)
	if string(finished.Output) != "resumed" || transport.calls.Load() != 2 {
		t.Fatalf("resume = %s, calls=%d", finished.Output, transport.calls.Load())
	}
	entries, err := journal.Load(ctx, string(brief.InvocationID))
	if err != nil || len(entries) != 3 {
		t.Fatalf("journal entries=%d err=%v", len(entries), err)
	}
	for _, entry := range entries {
		if strings.Contains(string(entry.Payload), `"stale"`) {
			t.Fatal("stale response entered journal")
		}
	}
}
