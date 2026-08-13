package application

import (
	"context"
	"errors"

	"github.com/tekroo-ai/teams/kernel"
)

var ErrPowerFenceContextRequired = errors.New("power-fence context required")

type PowerFencedOperation string

const (
	OperationAssignment            PowerFencedOperation = "ASSIGNMENT"
	OperationModelDispatch         PowerFencedOperation = "MODEL_DISPATCH"
	OperationLocalToolExecution    PowerFencedOperation = "LOCAL_TOOL_EXECUTION"
	OperationProviderResultIntake  PowerFencedOperation = "PROVIDER_RESULT_INTAKE"
	OperationOrganizationalCommand PowerFencedOperation = "ORGANIZATIONAL_COMMAND"
)

type PowerFence struct {
	admission ContinuityAdmissionService
}

type powerFenceContextKey struct{}

type PowerFenceContext struct {
	Operation  PowerFencedOperation
	PowerEpoch uint64
}

func WithPowerFenceContext(ctx context.Context, value PowerFenceContext) context.Context {
	return context.WithValue(ctx, powerFenceContextKey{}, value)
}

func PowerFenceContextFrom(ctx context.Context) (PowerFenceContext, bool) {
	value, ok := ctx.Value(powerFenceContextKey{}).(PowerFenceContext)
	return value, ok && value.PowerEpoch > 0
}

func NewPowerFence(admission ContinuityAdmissionService) (*PowerFence, error) {
	if admission == nil {
		return nil, ErrInvalidConfiguration
	}
	return &PowerFence{admission: admission}, nil
}

type PowerFencedCommandService struct {
	commands ExecutionCommandService
	fence    *PowerFence
}

func NewPowerFencedCommandService(commands ExecutionCommandService, fence *PowerFence) (*PowerFencedCommandService, error) {
	if commands == nil || fence == nil {
		return nil, ErrInvalidConfiguration
	}
	return &PowerFencedCommandService{commands: commands, fence: fence}, nil
}

func (service *PowerFencedCommandService) Handle(ctx context.Context, command kernel.KernelCommand, provenance kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
	if command.Target.Kind != kernel.AggregateSystem {
		admission, ok := PowerFenceContextFrom(ctx)
		if !ok {
			return kernel.CommandReceipt{}, ErrPowerFenceContextRequired
		}
		if err := service.fence.Require(ctx, admission.Operation, admission.PowerEpoch); err != nil {
			return kernel.CommandReceipt{}, err
		}
	}
	return service.commands.Handle(ctx, command, provenance)
}

func (fence *PowerFence) Require(ctx context.Context, operation PowerFencedOperation, powerEpoch uint64) error {
	action := kernel.ContinuityDispatch
	switch operation {
	case OperationProviderResultIntake:
		action = kernel.ContinuityAcceptResult
	case OperationAssignment, OperationModelDispatch, OperationLocalToolExecution, OperationOrganizationalCommand:
	default:
		return ErrInvalidConfiguration
	}
	decision, err := fence.admission.Admit(ctx, action, powerEpoch)
	if err != nil {
		return err
	}
	if !decision.Accepted {
		return AdmissionDeniedError{Reason: decision.Reason}
	}
	return nil
}

// ProviderSuspensionPort is the provider-neutral boundary for a preregistered
// suspension disposition. Implementations must return the authoritative record;
// a cancellation request alone is not a cancellation outcome.
type ProviderSuspensionPort interface {
	ApplySuspensionDisposition(context.Context, kernel.SuspensionExecutionRecord, uint64) (kernel.SuspensionExecutionRecord, error)
}

// ProviderReconciliationPort reconciles one execution idempotently after a
// planned or unexpected power boundary.
type ProviderReconciliationPort interface {
	ReconcileExecution(context.Context, kernel.UUIDv7, uint64) (kernel.ExecutionReconciliationRecord, error)
}

type LocalEffectReconciliationPort interface {
	ReconcileLocalEffects(context.Context, kernel.UUIDv7, uint64) (kernel.Digest, []kernel.UUIDv7, error)
}

type DurablePositionReconciliationPort interface {
	ReconcileOutbox(context.Context, uint64) (kernel.Digest, error)
	ReconcileChangeStream(context.Context, uint64) (kernel.Digest, error)
}
