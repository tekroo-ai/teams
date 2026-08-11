package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

var (
	ErrInvalidExecutionPlan     = errors.New("invalid execution plan")
	ErrExecutionControlConflict = errors.New("execution control conflict")
	ErrExecutionLimit           = errors.New("execution instance limit reached")
	ErrExecutionThrash          = errors.New("execution restart-rate limit reached")
	ErrReconciliationExhausted  = errors.New("execution reconciliation attempts exhausted")
	ErrExecutionObservation     = errors.New("execution observation identity mismatch")
)

type ExecutionControlState string

const (
	ControlPendingAuthorization ExecutionControlState = "PENDING_AUTHORIZATION"
	ControlAuthorized           ExecutionControlState = "AUTHORIZED"
	ControlStarting             ExecutionControlState = "STARTING"
	ControlRunning              ExecutionControlState = "RUNNING"
	ControlStopping             ExecutionControlState = "STOPPING"
	ControlStopped              ExecutionControlState = "STOPPED"
	ControlRejected             ExecutionControlState = "REJECTED"
	ControlUncertain            ExecutionControlState = "UNCERTAIN"
)

type ExecutionPolicy struct {
	OperationTimeout        time.Duration
	MaxActiveTotal          uint32
	MaxActivePerActor       uint32
	MaxStartsPerActorWindow uint32
	StartWindow             time.Duration
	MaxReconciliations      uint32
}

func (policy ExecutionPolicy) valid() bool {
	return policy.OperationTimeout > 0 && policy.MaxActiveTotal > 0 && policy.MaxActivePerActor > 0 && policy.MaxStartsPerActorWindow > 0 && policy.StartWindow > 0 && policy.MaxReconciliations > 0
}

type ExecutionStartPlan struct {
	AuthorizationCommand kernel.KernelCommand
	Provenance           kernel.ProvenanceBasis
	Request              kernel.StartExecutionRequest
}

type ExecutionControlRecord struct {
	Request                kernel.StartExecutionRequest
	AuthorizationCommandID kernel.UUIDv7
	State                  ExecutionControlState
	Version                uint64
	ReservedAt             time.Time
	UpdatedAt              time.Time
	AuthorizationReceipt   *kernel.CommandReceipt
	Observation            *kernel.ExecutionObservation
	ReconciliationAttempts uint32
	FailureCode            string
}

func (record ExecutionControlRecord) Clone() ExecutionControlRecord {
	copy := record
	if record.AuthorizationReceipt != nil {
		receipt := *record.AuthorizationReceipt
		receipt.EventIDs = append([]kernel.UUIDv7(nil), record.AuthorizationReceipt.EventIDs...)
		copy.AuthorizationReceipt = &receipt
	}
	if record.Observation != nil {
		observation := *record.Observation
		copy.Observation = &observation
	}
	return copy
}

type ExecutionControlStore interface {
	Prepare(context.Context, ExecutionControlRecord, ExecutionPolicy) (ExecutionControlRecord, bool, error)
	Load(context.Context, string) (ExecutionControlRecord, bool, error)
	CompareAndSwap(context.Context, string, uint64, ExecutionControlRecord) error
}

type ExecutionCommandService interface {
	Handle(context.Context, kernel.KernelCommand, kernel.ProvenanceBasis) (kernel.CommandReceipt, error)
}

type ExecutionCoordinator struct {
	commands ExecutionCommandService
	controls ExecutionControlStore
	engine   kernel.AgentExecutionEngine
	clock    kernel.Clock
	policy   ExecutionPolicy
}

func NewExecutionCoordinator(commands ExecutionCommandService, controls ExecutionControlStore, engine kernel.AgentExecutionEngine, clock kernel.Clock, policy ExecutionPolicy) (*ExecutionCoordinator, error) {
	if commands == nil || controls == nil || engine == nil || clock == nil || !policy.valid() {
		return nil, ErrInvalidConfiguration
	}
	return &ExecutionCoordinator{commands: commands, controls: controls, engine: engine, clock: clock, policy: policy}, nil
}

func (coordinator *ExecutionCoordinator) Start(ctx context.Context, plan ExecutionStartPlan) (ExecutionControlRecord, error) {
	if err := validateExecutionPlan(plan); err != nil {
		return ExecutionControlRecord{}, err
	}
	if err := ctx.Err(); err != nil {
		return ExecutionControlRecord{}, err
	}
	now := coordinator.clock.Now()
	prepared := ExecutionControlRecord{
		Request: plan.Request, AuthorizationCommandID: plan.AuthorizationCommand.CommandID,
		State: ControlPendingAuthorization, Version: 1, ReservedAt: now, UpdatedAt: now,
	}
	record, _, err := coordinator.controls.Prepare(ctx, prepared, coordinator.policy)
	if err != nil {
		return ExecutionControlRecord{}, err
	}
	if record.State == ControlPendingAuthorization {
		receipt, handleErr := coordinator.commands.Handle(ctx, plan.AuthorizationCommand, plan.Provenance)
		if handleErr != nil {
			return record, fmt.Errorf("authorize execution start: %w", handleErr)
		}
		next := record.Clone()
		next.AuthorizationReceipt = &receipt
		next.UpdatedAt = coordinator.clock.Now()
		if receipt.OutcomeCode != kernel.OutcomeApplied {
			next.State = ControlRejected
			next.FailureCode = receipt.ReasonCode
		} else {
			next.State = ControlAuthorized
		}
		record, _, err = coordinator.advance(ctx, record, next)
		if err != nil {
			return ExecutionControlRecord{}, err
		}
	}
	if record.State != ControlAuthorized {
		return record, nil
	}
	if err := ctx.Err(); err != nil {
		return record, err
	}
	next := record.Clone()
	next.State = ControlStarting
	next.UpdatedAt = coordinator.clock.Now()
	record, claimed, err := coordinator.advance(ctx, record, next)
	if err != nil {
		return ExecutionControlRecord{}, err
	}
	if !claimed {
		return record, nil
	}
	observation, effectErr := runExecutionEffect(ctx, coordinator.policy.OperationTimeout, func(effectCtx context.Context) (kernel.ExecutionObservation, error) {
		return coordinator.engine.Start(effectCtx, plan.Request)
	})
	return coordinator.recordEffect(ctx, record, observation, effectErr)
}

func (coordinator *ExecutionCoordinator) Stop(ctx context.Context, key string) (ExecutionControlRecord, error) {
	record, found, err := coordinator.controls.Load(ctx, key)
	if err != nil || !found {
		return record, err
	}
	if record.State == ControlStopped {
		return record, nil
	}
	if record.State != ControlRunning && record.State != ControlUncertain {
		return record, ErrExecutionControlConflict
	}
	next := record.Clone()
	next.State = ControlStopping
	next.UpdatedAt = coordinator.clock.Now()
	record, claimed, err := coordinator.advance(ctx, record, next)
	if err != nil || !claimed {
		return record, err
	}
	observation, effectErr := runExecutionEffect(ctx, coordinator.policy.OperationTimeout, func(effectCtx context.Context) (kernel.ExecutionObservation, error) {
		return coordinator.engine.Stop(effectCtx, record.Request.ExecutionID)
	})
	return coordinator.recordEffect(ctx, record, observation, effectErr)
}

func (coordinator *ExecutionCoordinator) Inspect(ctx context.Context, key string) (kernel.ExecutionObservation, error) {
	record, found, err := coordinator.controls.Load(ctx, key)
	if err != nil || !found {
		return kernel.ExecutionObservation{}, err
	}
	observation, effectErr := runExecutionEffect(ctx, coordinator.policy.OperationTimeout, func(effectCtx context.Context) (kernel.ExecutionObservation, error) {
		return coordinator.engine.Inspect(effectCtx, record.Request.ExecutionID)
	})
	if effectErr != nil {
		return kernel.ExecutionObservation{}, effectErr
	}
	if !observationMatches(record.Request, observation) {
		return kernel.ExecutionObservation{}, ErrExecutionObservation
	}
	return observation, nil
}

func (coordinator *ExecutionCoordinator) Reconcile(ctx context.Context, key string) (ExecutionControlRecord, error) {
	record, found, err := coordinator.controls.Load(ctx, key)
	if err != nil || !found {
		return record, err
	}
	if record.State != ControlUncertain && record.State != ControlStarting && record.State != ControlStopping {
		return record, nil
	}
	if record.ReconciliationAttempts >= coordinator.policy.MaxReconciliations {
		return record, ErrReconciliationExhausted
	}
	next := record.Clone()
	next.ReconciliationAttempts++
	next.UpdatedAt = coordinator.clock.Now()
	record, claimed, err := coordinator.advance(ctx, record, next)
	if err != nil {
		return ExecutionControlRecord{}, err
	}
	if !claimed {
		return record, nil
	}
	observation, effectErr := runExecutionEffect(ctx, coordinator.policy.OperationTimeout, func(effectCtx context.Context) (kernel.ExecutionObservation, error) {
		return coordinator.engine.Reconcile(effectCtx, record.Request.ExecutionID)
	})
	return coordinator.recordEffect(ctx, record, observation, effectErr)
}

func (coordinator *ExecutionCoordinator) recordEffect(ctx context.Context, record ExecutionControlRecord, observation kernel.ExecutionObservation, effectErr error) (ExecutionControlRecord, error) {
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), coordinator.policy.OperationTimeout)
	defer cancel()
	next := record.Clone()
	next.UpdatedAt = coordinator.clock.Now()
	if effectErr != nil || !observationMatches(record.Request, observation) {
		next.State = ControlUncertain
		next.FailureCode = "PROVIDER_EFFECT_UNCERTAIN"
		updated, _, err := coordinator.advance(persistCtx, record, next)
		if err != nil {
			return ExecutionControlRecord{}, err
		}
		if effectErr != nil {
			return updated, effectErr
		}
		return updated, ErrExecutionObservation
	}
	next.Observation = &observation
	next.FailureCode = ""
	switch observation.State {
	case kernel.ExecutionRunning:
		next.State = ControlRunning
	case kernel.ExecutionStopped:
		next.State = ControlStopped
	default:
		next.State = ControlUncertain
		next.FailureCode = "PROVIDER_EFFECT_UNCERTAIN"
	}
	updated, _, err := coordinator.advance(persistCtx, record, next)
	return updated, err
}

func (coordinator *ExecutionCoordinator) advance(ctx context.Context, prior, next ExecutionControlRecord) (ExecutionControlRecord, bool, error) {
	next.Version = prior.Version + 1
	if err := coordinator.controls.CompareAndSwap(ctx, prior.Request.IdempotencyKey, prior.Version, next); err != nil {
		if errors.Is(err, ErrExecutionControlConflict) {
			current, found, loadErr := coordinator.controls.Load(ctx, prior.Request.IdempotencyKey)
			if loadErr == nil && found {
				return current, false, nil
			}
		}
		return ExecutionControlRecord{}, false, err
	}
	return next, true, nil
}

func validateExecutionPlan(plan ExecutionStartPlan) error {
	request := plan.Request
	command := plan.AuthorizationCommand
	if !request.ActorFQN.Valid() || !request.ExecutionID.Valid() || request.FencingEpoch == 0 || !request.RuntimeIdentity.Valid() || request.IdempotencyKey == "" || request.IdempotencyKey != command.IdempotencyKey || !plan.Provenance.Valid() {
		return ErrInvalidExecutionPlan
	}
	var register struct {
		ActorFQN        kernel.ActorFQN `json:"actor_fqn"`
		ExecutionID     kernel.UUIDv7   `json:"execution_id"`
		FencingEpoch    uint64          `json:"fencing_epoch"`
		RuntimeIdentity kernel.Digest   `json:"runtime_identity"`
		NewExecutionID  kernel.UUIDv7   `json:"new_execution_id"`
		NewFencingEpoch uint64          `json:"new_fencing_epoch"`
	}
	if json.Unmarshal(command.Payload, &register) != nil || register.ActorFQN != request.ActorFQN {
		return ErrInvalidExecutionPlan
	}
	switch command.CommandType {
	case "tekroo.command.execution.register":
		if register.ExecutionID != request.ExecutionID || register.FencingEpoch != request.FencingEpoch || register.RuntimeIdentity != request.RuntimeIdentity {
			return ErrInvalidExecutionPlan
		}
	case "tekroo.command.execution.replace":
		if register.NewExecutionID != request.ExecutionID || register.NewFencingEpoch != request.FencingEpoch {
			return ErrInvalidExecutionPlan
		}
	default:
		return ErrInvalidExecutionPlan
	}
	return nil
}

func observationMatches(request kernel.StartExecutionRequest, observation kernel.ExecutionObservation) bool {
	return observation.ActorFQN == request.ActorFQN && observation.ExecutionID == request.ExecutionID && observation.FencingEpoch == request.FencingEpoch
}

func runExecutionEffect(parent context.Context, timeout time.Duration, effect func(context.Context) (kernel.ExecutionObservation, error)) (kernel.ExecutionObservation, error) {
	if err := parent.Err(); err != nil {
		return kernel.ExecutionObservation{}, err
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	type result struct {
		observation kernel.ExecutionObservation
		err         error
	}
	completed := make(chan result, 1)
	go func() {
		observation, err := effect(ctx)
		completed <- result{observation: observation, err: err}
	}()
	select {
	case observed := <-completed:
		return observed.observation, observed.err
	case <-ctx.Done():
		return kernel.ExecutionObservation{}, ctx.Err()
	}
}
