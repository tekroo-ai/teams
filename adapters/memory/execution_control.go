package memory

import (
	"context"
	"sync"

	"github.com/tekroo-ai/teams/application"
)

type ExecutionControls struct {
	mu      sync.Mutex
	records map[string]application.ExecutionControlRecord
}

func NewExecutionControls() *ExecutionControls {
	return &ExecutionControls{records: make(map[string]application.ExecutionControlRecord)}
}

func (store *ExecutionControls) Prepare(ctx context.Context, candidate application.ExecutionControlRecord, policy application.ExecutionPolicy) (application.ExecutionControlRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return application.ExecutionControlRecord{}, false, err
	}
	if !candidate.Request.ActorFQN.Valid() || !candidate.Request.ExecutionID.Valid() || candidate.Request.FencingEpoch == 0 || !candidate.Request.RuntimeIdentity.Valid() || candidate.Request.IdempotencyKey == "" || !candidate.AuthorizationCommandID.Valid() || candidate.State != application.ControlPendingAuthorization || candidate.Version != 1 || candidate.ReservedAt.IsZero() || candidate.UpdatedAt != candidate.ReservedAt || policy.OperationTimeout <= 0 || policy.MaxActiveTotal == 0 || policy.MaxActivePerActor == 0 || policy.MaxStartsPerActorWindow == 0 || policy.StartWindow <= 0 || policy.MaxReconciliations == 0 {
		return application.ExecutionControlRecord{}, false, application.ErrInvalidExecutionPlan
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	key := candidate.Request.IdempotencyKey
	if prior, exists := store.records[key]; exists {
		if prior.Request != candidate.Request || prior.AuthorizationCommandID != candidate.AuthorizationCommandID {
			return application.ExecutionControlRecord{}, false, application.ErrExecutionControlConflict
		}
		return prior.Clone(), false, nil
	}
	activeTotal := uint32(0)
	activeActor := uint32(0)
	recentActor := uint32(0)
	windowStart := candidate.ReservedAt.Add(-policy.StartWindow)
	for _, record := range store.records {
		if controlConsumesCapacity(record.State) {
			activeTotal++
			if record.Request.ActorFQN == candidate.Request.ActorFQN {
				activeActor++
			}
		}
		if record.Request.ActorFQN == candidate.Request.ActorFQN && !record.ReservedAt.Before(windowStart) {
			recentActor++
		}
	}
	if activeTotal >= policy.MaxActiveTotal || activeActor >= policy.MaxActivePerActor {
		return application.ExecutionControlRecord{}, false, application.ErrExecutionLimit
	}
	if recentActor >= policy.MaxStartsPerActorWindow {
		return application.ExecutionControlRecord{}, false, application.ErrExecutionThrash
	}
	store.records[key] = candidate.Clone()
	return candidate.Clone(), true, nil
}

func (store *ExecutionControls) Load(ctx context.Context, key string) (application.ExecutionControlRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return application.ExecutionControlRecord{}, false, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	record, found := store.records[key]
	return record.Clone(), found, nil
}

func (store *ExecutionControls) CompareAndSwap(ctx context.Context, key string, expectedVersion uint64, next application.ExecutionControlRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	current, found := store.records[key]
	if !found || current.Version != expectedVersion || next.Version != expectedVersion+1 || next.Request != current.Request || next.AuthorizationCommandID != current.AuthorizationCommandID || next.ReservedAt != current.ReservedAt || next.UpdatedAt.Before(current.UpdatedAt) || !validControlTransition(current.State, next.State) {
		return application.ErrExecutionControlConflict
	}
	store.records[key] = next.Clone()
	return nil
}

func validControlTransition(current, next application.ExecutionControlState) bool {
	if current == next {
		return current == application.ControlStarting || current == application.ControlStopping || current == application.ControlUncertain
	}
	switch current {
	case application.ControlPendingAuthorization:
		return next == application.ControlAuthorized || next == application.ControlRejected
	case application.ControlAuthorized:
		return next == application.ControlStarting
	case application.ControlStarting:
		return next == application.ControlRunning || next == application.ControlStopped || next == application.ControlUncertain
	case application.ControlRunning:
		return next == application.ControlStopping || next == application.ControlUncertain
	case application.ControlStopping:
		return next == application.ControlStopped || next == application.ControlRunning || next == application.ControlUncertain
	case application.ControlUncertain:
		return next == application.ControlRunning || next == application.ControlStopped || next == application.ControlStopping
	default:
		return false
	}
}

func controlConsumesCapacity(state application.ExecutionControlState) bool {
	switch state {
	case application.ControlPendingAuthorization, application.ControlAuthorized, application.ControlStarting, application.ControlRunning, application.ControlStopping, application.ControlUncertain:
		return true
	default:
		return false
	}
}

var _ application.ExecutionControlStore = (*ExecutionControls)(nil)
