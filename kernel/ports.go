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
	ErrReceiptAccessDenied      = errors.New("receipt read access denied")
)

type KernelDecisionStore interface {
	LoadDecision(context.Context, KernelCommand) (Snapshot, error)
	LookupReceipt(context.Context, KernelCommand, time.Time) (CommandReceipt, bool, error)
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
	ActorFQN        ActorFQN
	ExecutionID     UUIDv7
	FencingEpoch    uint64
	RuntimeIdentity Digest
	IdempotencyKey  string
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

type ReleaseProviderState string

const (
	ReleaseProviderMerged         ReleaseProviderState = "MERGED"
	ReleaseProviderOpen           ReleaseProviderState = "OPEN"
	ReleaseProviderClosedUnmerged ReleaseProviderState = "CLOSED_UNMERGED"
	ReleaseProviderMissing        ReleaseProviderState = "MISSING"
	ReleaseProviderUnavailable    ReleaseProviderState = "UNAVAILABLE"
)

// ReleaseMergeRequest is an immutable projection of one entry in the durable,
// ordered release plan. Provider adapters must use ProviderIdempotencyKey as
// the external mutation key and must not infer a different head, base, or
// merge strategy.
type ReleaseMergeRequest struct {
	ReleasePlanID          UUIDv7
	PlanDigest             Digest
	MergeID                UUIDv7
	AttemptID              UUIDv7
	Round                  uint64
	ProviderIdempotencyKey string
	RepositoryURL          string
	BaseRef                string
	BaseCommit             string
	ChangeRef              string
	HeadCommit             string
	MergeStrategy          string
	GitVersion             string
	ConflictPolicy         string
}

func (request ReleaseMergeRequest) Valid() bool {
	return request.ReleasePlanID.Valid() && request.PlanDigest.Valid() && request.MergeID.Valid() && request.AttemptID.Valid() && request.Round > 0 && request.ProviderIdempotencyKey != "" && request.RepositoryURL != "" && request.BaseRef != "" && request.BaseCommit != "" && request.ChangeRef != "" && request.HeadCommit != "" && request.MergeStrategy == "FF_ONLY_ORDERED" && request.GitVersion != "" && request.ConflictPolicy == "FAIL_NO_IMPROVISATION"
}

// ReleaseProviderObservation is authoritative provider evidence for one
// planned merge. Commit and tree fields are required only for successful
// outcomes; failed and unavailable observations intentionally carry no
// inferred repository identity.
type ReleaseProviderObservation struct {
	ReleasePlanID UUIDv7
	MergeID       UUIDv7
	AttemptID     UUIDv7
	State         ReleaseProviderState
	Outcome       ReleaseOutcome
	BaseCommit    string
	HeadCommit    string
	TreeDigest    string
	Reasons       []string
}

type ReleaseProvider interface {
	Merge(context.Context, ReleaseMergeRequest) (ReleaseProviderObservation, error)
	Reconcile(context.Context, ReleaseMergeRequest) (ReleaseProviderObservation, error)
}
