package daemon

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"time"

	"github.com/tekroo-ai/teams/adapters/stdio"
)

var (
	ErrInvalidConfiguration = errors.New("invalid daemon host configuration")
	ErrAlreadyRunning       = errors.New("daemon host is already running")
	ErrShutdownTimeout      = errors.New("daemon host shutdown timed out")
)

type Host struct {
	server          *stdio.Server
	shutdownTimeout time.Duration
	running         atomic.Bool
}

func NewHost(server *stdio.Server, shutdownTimeout time.Duration) (*Host, error) {
	if server == nil || shutdownTimeout <= 0 {
		return nil, ErrInvalidConfiguration
	}
	return &Host{server: server, shutdownTimeout: shutdownTimeout}, nil
}

func (host *Host) Run(ctx context.Context, input io.ReadCloser, output io.Writer) error {
	if input == nil || output == nil {
		return ErrInvalidConfiguration
	}
	if !host.running.CompareAndSwap(false, true) {
		return ErrAlreadyRunning
	}
	done := make(chan error, 1)
	go func() {
		defer host.running.Store(false)
		done <- host.server.Serve(ctx, input, output)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_ = input.Close()
		timer := time.NewTimer(host.shutdownTimeout)
		defer timer.Stop()
		select {
		case <-done:
			return ctx.Err()
		case <-timer.C:
			return ErrShutdownTimeout
		}
	}
}
