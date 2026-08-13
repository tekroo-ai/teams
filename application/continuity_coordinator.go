package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/tekroo-ai/teams/kernel"
)

var (
	ErrContinuityUnavailable = errors.New("team continuity state unavailable")
	ErrWorkAdmissionDenied   = errors.New("team continuity denied work admission")
)

type ContinuitySnapshotLoader interface {
	Load(context.Context, kernel.AggregateRef) (kernel.Snapshot, error)
}

type ContinuityAdmissionService interface {
	Admit(context.Context, kernel.ContinuityAction, uint64) (kernel.PolicyResult, error)
}

type ContinuityCoordinator struct {
	snapshots ContinuitySnapshotLoader
	system    kernel.AggregateRef
}

func NewContinuityCoordinator(snapshots ContinuitySnapshotLoader, system kernel.AggregateRef) (*ContinuityCoordinator, error) {
	if snapshots == nil || system.Kind != kernel.AggregateSystem || !system.ID.Valid() {
		return nil, ErrInvalidConfiguration
	}
	return &ContinuityCoordinator{snapshots: snapshots, system: system}, nil
}

func (coordinator *ContinuityCoordinator) Admit(ctx context.Context, action kernel.ContinuityAction, powerEpoch uint64) (kernel.PolicyResult, error) {
	if err := ctx.Err(); err != nil {
		return kernel.PolicyResult{}, err
	}
	if action != kernel.ContinuityDispatch && action != kernel.ContinuityAcceptResult || powerEpoch == 0 {
		return kernel.PolicyResult{}, ErrInvalidAssignmentCoordination
	}
	snapshot, err := coordinator.snapshots.Load(ctx, coordinator.system)
	if err != nil {
		return kernel.PolicyResult{}, fmt.Errorf("load continuity snapshot: %w", err)
	}
	if !snapshot.Exists || snapshot.State == nil || snapshot.State.Kind != kernel.AggregateSystem || snapshot.State.ID != coordinator.system.ID || snapshot.State.Continuity == nil || !snapshot.State.Continuity.Valid() {
		return kernel.PolicyResult{}, ErrContinuityUnavailable
	}
	return kernel.EvaluateContinuityAdmission(*snapshot.State.Continuity, action, powerEpoch), nil
}

type AdmissionDeniedError struct {
	Reason string
}

func (failure AdmissionDeniedError) Error() string {
	return fmt.Sprintf("%s: %s", ErrWorkAdmissionDenied, failure.Reason)
}

func (failure AdmissionDeniedError) Unwrap() error { return ErrWorkAdmissionDenied }
