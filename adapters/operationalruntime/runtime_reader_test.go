package operationalruntime

import (
	"context"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

func TestAssembleOperationalExecutionReaderExposesDeadlineExtensionOnlyWhenBound(t *testing.T) {
	store := deadlineCapableExecutionReader{}
	withoutContinuity := assembleOperationalExecutionReader(store, nil)
	if _, exposed := withoutContinuity.(application.OperationalDeadlineExtensionReader); exposed {
		t.Fatal("bare runtime exposed an uninitialized continuity capability")
	}
	withContinuity := assembleOperationalExecutionReader(store, store)
	if _, exposed := withContinuity.(application.OperationalDeadlineExtensionReader); !exposed {
		t.Fatal("production runtime did not expose its bound continuity capability")
	}
}

type deadlineCapableExecutionReader struct{}

func (deadlineCapableExecutionReader) LoadOperationalExecution(context.Context, kernel.UUIDv7) (application.OperationalExecutionContext, error) {
	return application.OperationalExecutionContext{}, nil
}

func (deadlineCapableExecutionReader) LoadOperationalExecutionByAuthorizationEvent(context.Context, kernel.UUIDv7) (application.OperationalExecutionContext, error) {
	return application.OperationalExecutionContext{}, nil
}

func (deadlineCapableExecutionReader) EffectiveWorkInvocationDeadline(context.Context, kernel.WorkInvocation, time.Time) (time.Time, error) {
	return time.Time{}, nil
}
