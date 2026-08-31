package organization

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/tekroo-ai/teams/kernel"
)

type RoleWorker interface {
	Run(context.Context, StartRoleRequest) error
}

type RoleWorkerFunc func(context.Context, StartRoleRequest) error

func (function RoleWorkerFunc) Run(ctx context.Context, request StartRoleRequest) error {
	return function(ctx, request)
}

type inProcess struct {
	request StartRoleRequest
	cancel  context.CancelFunc
	done    chan error
}

// InProcessRuntime hosts long-lived organizational workers. A worker blocks on
// Teams work and message feeds; it does not call a model merely because it is
// running. Model execution still requires a single-use kernel invocation.
type InProcessRuntime struct {
	worker    RoleWorker
	mu        sync.Mutex
	processes map[kernel.ActorFQN]*inProcess
}

func NewInProcessRuntime(worker RoleWorker) (*InProcessRuntime, error) {
	if worker == nil {
		return nil, ErrRoleRuntime
	}
	return &InProcessRuntime{worker: worker, processes: make(map[kernel.ActorFQN]*inProcess)}, nil
}

func (runtime *InProcessRuntime) Start(ctx context.Context, request StartRoleRequest) (RuntimeObservation, error) {
	if err := ctx.Err(); err != nil {
		return RuntimeObservation{}, err
	}
	if !request.ActorFQN.Valid() || !request.Execution.Valid() || !request.BundleDigest.Valid() || !request.ModelProfile.Valid() || request.WorkspaceID == "" || request.IdempotencyKey == "" || !request.ManifestDigest.Valid() || request.ManifestVersion == "" {
		return RuntimeObservation{}, ErrRoleRuntime
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if current := runtime.processes[request.ActorFQN]; current != nil {
		if current.request.Execution == request.Execution {
			return runningObservation(current.request), nil
		}
		return RuntimeObservation{}, ErrRoleStateConflict
	}
	processContext, cancel := context.WithCancel(context.Background())
	process := &inProcess{request: request, cancel: cancel, done: make(chan error, 1)}
	runtime.processes[request.ActorFQN] = process
	go func() {
		process.done <- runtime.worker.Run(processContext, request)
		close(process.done)
	}()
	return runningObservation(request), nil
}

func (runtime *InProcessRuntime) Stop(ctx context.Context, actor kernel.ActorFQN, execution kernel.ExecutionTuple) (RuntimeObservation, error) {
	runtime.mu.Lock()
	process := runtime.processes[actor]
	if process == nil {
		runtime.mu.Unlock()
		return RuntimeObservation{ActorFQN: actor, Execution: execution, State: RuntimeStopped}, nil
	}
	if process.request.Execution != execution {
		runtime.mu.Unlock()
		return RuntimeObservation{}, ErrRoleStateConflict
	}
	process.cancel()
	runtime.mu.Unlock()
	select {
	case err := <-process.done:
		if err != nil && !errors.Is(err, context.Canceled) {
			return RuntimeObservation{}, fmt.Errorf("%w: worker stop: %v", ErrRoleRuntime, err)
		}
	case <-ctx.Done():
		return RuntimeObservation{}, ctx.Err()
	}
	runtime.mu.Lock()
	if runtime.processes[actor] == process {
		delete(runtime.processes, actor)
	}
	runtime.mu.Unlock()
	return RuntimeObservation{ActorFQN: actor, Execution: execution, State: RuntimeStopped}, nil
}

func (runtime *InProcessRuntime) Inspect(ctx context.Context, actor kernel.ActorFQN, execution kernel.ExecutionTuple) (RuntimeObservation, error) {
	if err := ctx.Err(); err != nil {
		return RuntimeObservation{}, err
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	process := runtime.processes[actor]
	if process == nil {
		return RuntimeObservation{ActorFQN: actor, Execution: execution, State: RuntimeStopped}, nil
	}
	if process.request.Execution != execution {
		return RuntimeObservation{}, ErrRoleStateConflict
	}
	select {
	case err := <-process.done:
		delete(runtime.processes, actor)
		if err != nil && !errors.Is(err, context.Canceled) {
			return RuntimeObservation{ActorFQN: actor, Execution: execution, State: RuntimeUnknown}, fmt.Errorf("%w: worker exited: %v", ErrRoleRuntime, err)
		}
		return RuntimeObservation{ActorFQN: actor, Execution: execution, State: RuntimeStopped}, nil
	default:
		return runningObservation(process.request), nil
	}
}

func runningObservation(request StartRoleRequest) RuntimeObservation {
	return RuntimeObservation{
		ActorFQN: request.ActorFQN, Execution: request.Execution, State: RuntimeRunning,
		ProcessIdentity: "in-process:" + string(request.Execution.ExecutionID),
	}
}
