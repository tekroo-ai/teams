package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

func TestContinuityCoordinatorFencesDispatchAndLateResults(t *testing.T) {
	system := kernel.AggregateRef{Kind: kernel.AggregateSystem, ID: "00000000-0000-7000-8000-000000000901"}
	state := continuityState(system, kernel.ContinuityActive, 7, true)
	loader := continuityLoaderFunc(func(context.Context, kernel.AggregateRef) (kernel.Snapshot, error) {
		copy := state.Clone()
		return kernel.Snapshot{Exists: true, Revision: copy.Revision, State: &copy}, nil
	})
	coordinator, err := application.NewContinuityCoordinator(loader, system)
	if err != nil {
		t.Fatal(err)
	}
	if decision, loadErr := coordinator.Admit(context.Background(), kernel.ContinuityDispatch, 7); loadErr != nil || !decision.Accepted {
		t.Fatalf("active dispatch decision=%#v err=%v", decision, loadErr)
	}
	state.Continuity.ControlState = kernel.ContinuityReconciling
	state.Continuity.AdmissionOpen = false
	if decision, loadErr := coordinator.Admit(context.Background(), kernel.ContinuityAcceptResult, 6); loadErr != nil || decision.Accepted || decision.Reason != "LATE_RESULT_QUARANTINED" {
		t.Fatalf("late result decision=%#v err=%v", decision, loadErr)
	}
	if decision, loadErr := coordinator.Admit(context.Background(), kernel.ContinuityDispatch, 7); loadErr != nil || decision.Accepted || decision.Reason != "ADMISSION_CLOSED" {
		t.Fatalf("closed dispatch decision=%#v err=%v", decision, loadErr)
	}
}

func TestContinuityCoordinatorFailsClosedWithoutDurableContinuityState(t *testing.T) {
	system := kernel.AggregateRef{Kind: kernel.AggregateSystem, ID: "00000000-0000-7000-8000-000000000901"}
	coordinator, err := application.NewContinuityCoordinator(continuityLoaderFunc(func(context.Context, kernel.AggregateRef) (kernel.Snapshot, error) {
		return kernel.Snapshot{}, nil
	}), system)
	if err != nil {
		t.Fatal(err)
	}
	_, err = coordinator.Admit(context.Background(), kernel.ContinuityDispatch, 1)
	if !errors.Is(err, application.ErrContinuityUnavailable) {
		t.Fatalf("missing continuity error = %v", err)
	}
}

func continuityState(system kernel.AggregateRef, control kernel.ContinuityControlState, epoch uint64, admission bool) kernel.AggregateState {
	continuity := kernel.TeamContinuitySnapshot{Revision: 1, OperatingPosture: "CONTINUOUS", ControlState: control, PowerEpoch: epoch, AdmissionOpen: admission, ContinuityPolicyRevision: 1, ContinuityPolicyDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", HealthRequirementDigest: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", LastTransitionEventID: "00000000-0000-7000-8000-000000000902"}
	return kernel.AggregateState{Kind: system.Kind, ID: system.ID, Revision: 1, LifecycleEpoch: 1, ScopeRevision: 1, Phase: kernel.PhaseDraft, Condition: kernel.ConditionRunnable, Continuity: &continuity}
}

type continuityLoaderFunc func(context.Context, kernel.AggregateRef) (kernel.Snapshot, error)

func (function continuityLoaderFunc) Load(ctx context.Context, target kernel.AggregateRef) (kernel.Snapshot, error) {
	return function(ctx, target)
}
