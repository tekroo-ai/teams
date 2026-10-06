package agentruntime

import (
	"context"
	"errors"
	"time"
)

var ErrLeaseLost = errors.New("native agent run lease lost")

// RunOwnership is supplied by the host, never by the model. Storage adapters
// use it to fence new journal writes and effect reservations atomically with
// lease handover. Completing an already-reserved effect remains permitted:
// its outcome must survive cancellation or host sleep.
type RunOwnership struct {
	InvocationID  string
	RequestDigest string
	Owner         string
	Epoch         uint64
}

type runOwnershipKey struct{}

func WithRunOwnership(ctx context.Context, ownership RunOwnership) context.Context {
	return context.WithValue(ctx, runOwnershipKey{}, ownership)
}

func RunOwnershipFromContext(ctx context.Context) (RunOwnership, bool) {
	ownership, ok := ctx.Value(runOwnershipKey{}).(RunOwnership)
	return ownership, ok
}

// RunLease fences one active native worker. Epoch advances on every claim so
// a worker that wakes after its lease was replaced cannot publish a terminal
// outcome for the replacement worker.
type RunLease struct {
	Acquired        bool
	CancelRequested bool
	Epoch           uint64
}

type Renewal struct {
	Held            bool
	CancelRequested bool
}

type RunControl interface {
	Claim(context.Context, string, string, string, time.Time, time.Duration) (RunLease, error)
	Renew(context.Context, string, string, string, uint64, time.Time, time.Duration) (Renewal, error)
	RequestCancel(context.Context, string, string) error
	Release(context.Context, string, string, string, uint64, time.Time) error
}
