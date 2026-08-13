package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

func TestPowerFenceMapsEveryExecutionBoundaryToContinuityAdmission(t *testing.T) {
	var actions []kernel.ContinuityAction
	admission := continuityAdmissionFunc(func(_ context.Context, action kernel.ContinuityAction, epoch uint64) (kernel.PolicyResult, error) {
		if epoch != 8 {
			t.Fatalf("epoch = %d", epoch)
		}
		actions = append(actions, action)
		return kernel.PolicyResult{Accepted: true, Reason: "ACCEPTED"}, nil
	})
	fence, err := application.NewPowerFence(admission)
	if err != nil {
		t.Fatal(err)
	}
	operations := []application.PowerFencedOperation{application.OperationAssignment, application.OperationModelDispatch, application.OperationLocalToolExecution, application.OperationProviderResultIntake, application.OperationOrganizationalCommand}
	for _, operation := range operations {
		if err := fence.Require(context.Background(), operation, 8); err != nil {
			t.Fatalf("%s: %v", operation, err)
		}
	}
	want := []kernel.ContinuityAction{kernel.ContinuityDispatch, kernel.ContinuityDispatch, kernel.ContinuityDispatch, kernel.ContinuityAcceptResult, kernel.ContinuityDispatch}
	for index := range want {
		if actions[index] != want[index] {
			t.Fatalf("actions = %#v", actions)
		}
	}
}

func TestPowerFencePreservesLateResultQuarantineReason(t *testing.T) {
	fence, err := application.NewPowerFence(continuityAdmissionFunc(func(context.Context, kernel.ContinuityAction, uint64) (kernel.PolicyResult, error) {
		return kernel.PolicyResult{Reason: "LATE_RESULT_QUARANTINED"}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	err = fence.Require(context.Background(), application.OperationProviderResultIntake, 7)
	var denied application.AdmissionDeniedError
	if !errors.As(err, &denied) || denied.Reason != "LATE_RESULT_QUARANTINED" {
		t.Fatalf("late result error = %v", err)
	}
}

func TestPowerFencedCommandServiceFailsClosedWithoutEpochContext(t *testing.T) {
	var calls int
	commands := executionCommandFunc(func(context.Context, kernel.KernelCommand, kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		calls++
		return kernel.CommandReceipt{OutcomeCode: kernel.OutcomeApplied}, nil
	})
	fence, err := application.NewPowerFence(acceptingAdmission())
	if err != nil {
		t.Fatal(err)
	}
	service, err := application.NewPowerFencedCommandService(commands, fence)
	if err != nil {
		t.Fatal(err)
	}
	command := kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: "00000000-0000-7000-8000-000000000901"}}
	if _, err := service.Handle(context.Background(), command, kernel.ProvenanceBasis{}); !errors.Is(err, application.ErrPowerFenceContextRequired) || calls != 0 {
		t.Fatalf("missing-context err=%v calls=%d", err, calls)
	}
	ctx := application.WithPowerFenceContext(context.Background(), application.PowerFenceContext{Operation: application.OperationOrganizationalCommand, PowerEpoch: 7})
	if _, err := service.Handle(ctx, command, kernel.ProvenanceBasis{}); err != nil || calls != 1 {
		t.Fatalf("admitted command err=%v calls=%d", err, calls)
	}
	command.Target.Kind = kernel.AggregateSystem
	if _, err := service.Handle(context.Background(), command, kernel.ProvenanceBasis{}); err != nil || calls != 2 {
		t.Fatalf("system-control command err=%v calls=%d", err, calls)
	}
}
