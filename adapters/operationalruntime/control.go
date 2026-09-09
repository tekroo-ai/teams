package operationalruntime

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/tekroo-ai/teams/adapters/executionruntime"
	"github.com/tekroo-ai/teams/application"
)

var ErrControlConflict = errors.New("operational runtime control conflict")

type ControlState string

const (
	ControlStopped ControlState = "STOPPED"
	ControlRunning ControlState = "RUNNING"
	ControlPaused  ControlState = "PAUSED"
	ControlFailed  ControlState = "FAILED"
)

type ControlStatus struct {
	State     ControlState
	StartedAt time.Time
	StoppedAt time.Time
	Worker    executionruntime.Status
	LastError string
}

// Controller supplies the local operator lifecycle for one assembled Runtime.
// Pause and resume gate new invocation admission; they do not impersonate task
// cancellation or discard work that is already in flight.
type Controller struct {
	runtime *Runtime
	clock   interface{ Now() time.Time }

	mu        sync.Mutex
	state     ControlState
	startedAt time.Time
	stoppedAt time.Time
	lastError string
	cancel    context.CancelFunc
	done      chan error
	failures  chan error
}

func NewController(runtime *Runtime, clock interface{ Now() time.Time }) (*Controller, error) {
	if runtime == nil || runtime.worker == nil || clock == nil {
		return nil, application.ErrInvalidConfiguration
	}
	return &Controller{runtime: runtime, clock: clock, state: ControlStopped, failures: make(chan error, 1)}, nil
}

func (controller *Controller) Start(ctx context.Context) error {
	return controller.start(ctx, false)
}

// StartPaused initializes the worker and its durable feed without admitting
// work. It is used when an operator must inspect or checkpoint a deployment
// before allowing the next pending invocation to begin.
func (controller *Controller) StartPaused(ctx context.Context) error {
	return controller.start(ctx, true)
}

func (controller *Controller) start(ctx context.Context, paused bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.state != ControlStopped || controller.done != nil {
		return ErrControlConflict
	}
	runContext, cancel := context.WithCancel(context.Background())
	controller.cancel = cancel
	controller.done = make(chan error, 1)
	if paused {
		controller.runtime.worker.Pause()
		controller.state = ControlPaused
	} else {
		controller.state = ControlRunning
	}
	controller.startedAt = controller.clock.Now()
	go func() {
		err := controller.runtime.Run(runContext)
		controller.mu.Lock()
		if !errors.Is(err, context.Canceled) {
			controller.state = ControlFailed
			if err != nil {
				controller.lastError = err.Error()
			}
		}
		controller.mu.Unlock()
		if !errors.Is(err, context.Canceled) {
			if err == nil {
				err = errors.New("operational runtime stopped unexpectedly")
			}
			controller.failures <- err
		}
		controller.done <- err
	}()
	return nil
}

// Failures reports an unexpected worker termination without consuming the
// lifecycle completion used by Stop. No value is emitted for an operator stop.
func (controller *Controller) Failures() <-chan error {
	if controller == nil {
		return nil
	}
	return controller.failures
}

func (controller *Controller) Pause(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.state != ControlRunning {
		return ErrControlConflict
	}
	controller.runtime.worker.Pause()
	controller.state = ControlPaused
	return nil
}

func (controller *Controller) Resume(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.state != ControlPaused {
		return ErrControlConflict
	}
	controller.runtime.worker.Resume()
	controller.state = ControlRunning
	return nil
}

func (controller *Controller) Inspect() ControlStatus {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return ControlStatus{
		State: controller.state, StartedAt: controller.startedAt,
		StoppedAt: controller.stoppedAt, Worker: controller.runtime.worker.Status(),
		LastError: controller.lastError,
	}
}

func (controller *Controller) Stop(ctx context.Context) error {
	controller.mu.Lock()
	if controller.state != ControlRunning && controller.state != ControlPaused && controller.state != ControlFailed {
		controller.mu.Unlock()
		return ErrControlConflict
	}
	cancel, done := controller.cancel, controller.done
	controller.runtime.worker.Resume()
	controller.mu.Unlock()
	cancel()
	select {
	case err := <-done:
		controller.mu.Lock()
		controller.state = ControlStopped
		controller.stoppedAt = controller.clock.Now()
		controller.mu.Unlock()
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
