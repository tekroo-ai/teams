package daemon_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/daemon"
	"github.com/tekroo-ai/teams/adapters/protocol"
	"github.com/tekroo-ai/teams/adapters/stdio"
)

type endpoint struct{}

func (endpoint) Invoke(context.Context, protocol.Request) protocol.Response {
	return protocol.Response{}
}

func TestHostCancellationClosesInputAndCompletes(t *testing.T) {
	server, err := stdio.NewServer(endpoint{}, stdio.DefaultMaxFrameBytes)
	if err != nil {
		t.Fatal(err)
	}
	host, err := daemon.NewHost(server, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- host.Run(ctx, reader, &bytes.Buffer{}) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("host did not complete after cancellation")
	}
}

func TestHostShutdownTimeoutKeepsRunFenceUntilServerStops(t *testing.T) {
	server, err := stdio.NewServer(endpoint{}, stdio.DefaultMaxFrameBytes)
	if err != nil {
		t.Fatal(err)
	}
	host, err := daemon.NewHost(server, 5*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	input := newBlockingReadCloser()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- host.Run(ctx, input, io.Discard) }()
	<-input.started
	cancel()
	if err := <-done; !errors.Is(err, daemon.ErrShutdownTimeout) {
		t.Fatalf("shutdown error = %v, want ErrShutdownTimeout", err)
	}
	if err := host.Run(context.Background(), io.NopCloser(bytes.NewReader(nil)), io.Discard); !errors.Is(err, daemon.ErrAlreadyRunning) {
		t.Fatalf("concurrent run error = %v, want ErrAlreadyRunning", err)
	}
	close(input.release)
	deadline := time.Now().Add(time.Second)
	for {
		err := host.Run(context.Background(), io.NopCloser(bytes.NewReader(nil)), io.Discard)
		if err == nil {
			break
		}
		if !errors.Is(err, daemon.ErrAlreadyRunning) || time.Now().After(deadline) {
			t.Fatalf("host did not become reusable after server exit: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
}

type blockingReadCloser struct {
	startedOnce sync.Once
	started     chan struct{}
	release     chan struct{}
}

func newBlockingReadCloser() *blockingReadCloser {
	return &blockingReadCloser{started: make(chan struct{}), release: make(chan struct{})}
}

func (reader *blockingReadCloser) Read([]byte) (int, error) {
	reader.startedOnce.Do(func() { close(reader.started) })
	<-reader.release
	return 0, io.EOF
}

func (*blockingReadCloser) Close() error { return nil }
