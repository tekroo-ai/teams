package fake

import (
	"context"
	"errors"
	"sync"

	"github.com/tekroo-ai/teams/kernel"
)

var (
	ErrExecutionNotFound = errors.New("execution not found")
	ErrExecutionConflict = errors.New("execution identity conflict")
)

type ExecutionEngine struct {
	mu          sync.Mutex
	executions  map[kernel.UUIDv7]kernel.ExecutionObservation
	startsByKey map[string]kernel.UUIDv7
}

func NewExecutionEngine() *ExecutionEngine {
	return &ExecutionEngine{
		executions:  make(map[kernel.UUIDv7]kernel.ExecutionObservation),
		startsByKey: make(map[string]kernel.UUIDv7),
	}
}

func (e *ExecutionEngine) Start(ctx context.Context, request kernel.StartExecutionRequest) (kernel.ExecutionObservation, error) {
	if err := ctx.Err(); err != nil {
		return kernel.ExecutionObservation{}, err
	}
	if !request.ActorFQN.Valid() || !request.ExecutionID.Valid() || request.FencingEpoch == 0 || !request.RuntimeIdentity.Valid() || request.IdempotencyKey == "" {
		return kernel.ExecutionObservation{}, ErrExecutionConflict
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if priorID, exists := e.startsByKey[request.IdempotencyKey]; exists {
		if priorID != request.ExecutionID {
			return kernel.ExecutionObservation{}, ErrExecutionConflict
		}
		return e.executions[priorID], nil
	}
	if _, exists := e.executions[request.ExecutionID]; exists {
		return kernel.ExecutionObservation{}, ErrExecutionConflict
	}
	observation := kernel.ExecutionObservation{
		ActorFQN:     request.ActorFQN,
		ExecutionID:  request.ExecutionID,
		FencingEpoch: request.FencingEpoch,
		State:        kernel.ExecutionRunning,
	}
	e.executions[request.ExecutionID] = observation
	e.startsByKey[request.IdempotencyKey] = request.ExecutionID
	return observation, nil
}

func (e *ExecutionEngine) Stop(ctx context.Context, executionID kernel.UUIDv7) (kernel.ExecutionObservation, error) {
	if err := ctx.Err(); err != nil {
		return kernel.ExecutionObservation{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	observation, exists := e.executions[executionID]
	if !exists {
		return kernel.ExecutionObservation{}, ErrExecutionNotFound
	}
	observation.State = kernel.ExecutionStopped
	e.executions[executionID] = observation
	return observation, nil
}

func (e *ExecutionEngine) Inspect(ctx context.Context, executionID kernel.UUIDv7) (kernel.ExecutionObservation, error) {
	if err := ctx.Err(); err != nil {
		return kernel.ExecutionObservation{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	observation, exists := e.executions[executionID]
	if !exists {
		return kernel.ExecutionObservation{}, ErrExecutionNotFound
	}
	return observation, nil
}

func (e *ExecutionEngine) Reconcile(ctx context.Context, executionID kernel.UUIDv7) (kernel.ExecutionObservation, error) {
	return e.Inspect(ctx, executionID)
}
