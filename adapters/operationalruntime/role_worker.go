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
	Admit(context.Context, organization.OrganizationalMessage) (kernel.WorkAdmissionResult, bool, bool, error)
}

type organizationalRoleWorker struct {
	store        *mongo.Store
	inbox        *organization.RoleInbox
	admission    workflowMessageAdmission
	pollInterval time.Duration
	openTimeout  time.Duration
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
		pollContext, pollCancel := context.WithTimeout(ctx, worker.pollInterval)
		message, err := feed.Poll(pollContext)
		pollCancel()
		switch {
		case err == nil:
			if worker.admission != nil {
				admissionContext, admissionCancel := context.WithTimeout(ctx, worker.openTimeout)
				result, handled, _, admissionErr := worker.admission.Admit(admissionContext, message)
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
		case errors.Is(err, organization.ErrOrganizationalMessageNotFound):
		case errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil:
			// A bounded change-stream poll expiring with no message is the
			// normal idle path. Only the parent runtime deadline is terminal.
		case ctx.Err() != nil:
			return ctx.Err()
		default:
			return err
		}
	}
}
