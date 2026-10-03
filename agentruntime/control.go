package agentruntime

import (
	"context"
	"time"
)

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
