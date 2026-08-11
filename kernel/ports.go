package kernel

import (
	"context"
	"errors"
	"time"
)

var (
	ErrDecisionAlreadyCommitted = errors.New("decision already committed")
	ErrCommitUncertain          = errors.New("decision commit acknowledgement uncertain")
	ErrCommandIdentityConflict  = errors.New("command identity conflict")
	ErrIdempotencyKeyConflict   = errors.New("idempotency key reuse conflict")
)

type KernelDecisionStore interface {
	Load(context.Context, AggregateRef) (Snapshot, error)
	LookupReceipt(context.Context, KernelCommand) (CommandReceipt, bool, error)
	RecordIdentityConflict(context.Context, IdentityConflictAudit) error
	Commit(context.Context, Snapshot, Decision) error
}

type IdentityConflictAudit struct {
	CommandID        UUIDv7
	IdempotencyScope Digest
	Fingerprint      Digest
	ReasonCode       string
	ObservedAt       time.Time
	ProvenanceDigest Digest
}

type Clock interface {
	Now() time.Time
}

type IDSource interface {
	Next() (UUIDv7, error)
}

type ExecutionState string

const (
	ExecutionStarting  ExecutionState = "STARTING"
	ExecutionRunning   ExecutionState = "RUNNING"
	ExecutionStopping  ExecutionState = "STOPPING"
	ExecutionStopped   ExecutionState = "STOPPED"
	ExecutionUncertain ExecutionState = "UNCERTAIN"
)

type StartExecutionRequest struct {
	ActorFQN       ActorFQN
	ExecutionID    UUIDv7
	FencingEpoch   uint64
	IdempotencyKey string
}

type ExecutionObservation struct {
	ActorFQN     ActorFQN
	ExecutionID  UUIDv7
	FencingEpoch uint64
	State        ExecutionState
}

type AgentExecutionEngine interface {
	Start(context.Context, StartExecutionRequest) (ExecutionObservation, error)
	Stop(context.Context, UUIDv7) (ExecutionObservation, error)
	Inspect(context.Context, UUIDv7) (ExecutionObservation, error)
	Reconcile(context.Context, UUIDv7) (ExecutionObservation, error)
}
