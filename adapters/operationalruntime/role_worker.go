package operationalruntime

import (
	"context"
	"errors"
	"time"

	"github.com/tekroo-ai/teams/adapters/mongo"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

type workflowMessageAdmission interface {
	Admit(context.Context, organization.OrganizationalMessage, kernel.ExecutionTuple) (kernel.WorkAdmissionResult, bool, bool, error)
}

type organizationalRoleWorker struct {
	store       *mongo.Store
	inbox       *organization.RoleInbox
	admission   workflowMessageAdmission
	featureWake chan<- struct{}
	waitTimeout time.Duration
	openTimeout time.Duration
}

func signalFeatureRecovery(wake chan<- struct{}) {
	if wake == nil {
		return
	}
	select {
	case wake <- struct{}{}:
	default:
		// Reconciliation consumes current database state, so one queued wake
		// represents any number of messages that arrived in the same burst.
	}
}

func (worker *organizationalRoleWorker) Run(ctx context.Context, request organization.StartRoleRequest) error {
	openContext, cancel := context.WithTimeout(ctx, worker.openTimeout)
	feed, err := worker.store.OpenOrganizationalMessageFeed(openContext, "role-"+string(request.Execution.ExecutionID), request.ActorFQN)
	cancel()
	if err != nil {
		return err
	}
	defer feed.Close(context.WithoutCancel(ctx))
	for {
		// Mongo's configured MaxAwaitTime is the idle cadence. The client
		// deadline includes a bounded transport grace period so a normal empty
		// batch returns before cancellation and leaves the stream reusable.
		pollContext, pollCancel := context.WithTimeout(ctx, worker.waitTimeout+worker.openTimeout)
		message, err := feed.Poll(pollContext)
		pollCancel()
		switch {
		case err == nil:
			if worker.admission != nil {
				admissionContext, admissionCancel := context.WithTimeout(ctx, worker.openTimeout)
				result, handled, _, admissionErr := worker.admission.Admit(admissionContext, message, request.Execution)
				admissionCancel()
				if admissionErr != nil {
					return admissionErr
				}
				if handled && result.Outcome != kernel.WorkAdmitted {
					continue
				}
			}
			if notifyErr := worker.inbox.Notify(message); notifyErr != nil {
				return notifyErr
			}
			signalFeatureRecovery(worker.featureWake)
		case errors.Is(err, organization.ErrOrganizationalMessageNotFound):
		case errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil:
			// Mongo did not honor the configured bounded await. Exit so role
			// recovery reopens a fresh stream rather than reusing a stream that
			// the client deadline has cancelled.
			return err
		case ctx.Err() != nil:
			return ctx.Err()
		default:
			return err
		}
	}
}
