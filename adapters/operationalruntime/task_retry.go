package operationalruntime

import (
	"context"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// ordinaryRetryableTerminalEligible recognizes the exact terminal states that
// the execution coordinator has explicitly classified as retryable. The retry
// remains inside the task's accepted attempt budget and reuses the prior
// condition digest; it is not a changed-condition technical extension.
func ordinaryRetryableTerminalEligible(task organization.PlannedTask, state kernel.AggregateState, invocation kernel.WorkInvocation) bool {
	if state.Phase != kernel.PhaseActive || state.Condition != kernel.ConditionRunnable || invocation.Retryable == nil || !*invocation.Retryable {
		return false
	}
	if invocation.State != kernel.InvocationFailed && invocation.State != kernel.InvocationTimedOut && invocation.State != kernel.InvocationStartFailed {
		return false
	}
	limit := uint64(task.AttemptLimit)
	if invocation.Purpose == kernel.PurposeRepair {
		limit = uint64(task.ReviewRoundLimit)
	} else if invocation.Purpose != task.Purpose {
		return false
	}
	return invocation.AttemptOrdinal < limit
}

// reconcileRetryableTerminatedTasks closes the ordinary-admission path for a
// terminal attempt that the execution boundary marked retryable. Previously,
// those attempts were skipped by feature reconciliation even though the kernel
// continuation contract expressly permitted their bounded successor.
func (service *ProductionService) reconcileRetryableTerminatedTasks(ctx context.Context, feature organization.FeatureRequest, plan organization.FeaturePlan, states map[kernel.UUIDv7]kernel.AggregateState, invocations map[kernel.UUIDv7]kernel.WorkInvocation) (bool, error) {
	for _, task := range plan.Tasks {
		state, stateFound := states[task.ID]
		invocation, invocationFound := invocations[task.ID]
		if !stateFound || !invocationFound || !ordinaryRetryableTerminalEligible(task, state, invocation) {
			continue
		}
		taskRef := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}
		snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: taskRef})
		if err != nil {
			return false, err
		}
		if repeatedTerminalOutput(snapshot, invocation) {
			continue
		}
		budget, budgetFound := snapshot.WorkBudgetAccounts[kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: feature.BudgetAccountID}]
		if !budgetFound || !budget.Valid() {
			return false, organization.ErrInvalidFeature
		}
		evidence, err := evidenceRefsForIDs(snapshot, invocation.TerminalEvidenceIDs)
		if err != nil {
			return false, err
		}
		deadline := automaticRetryableRecoveryDeadline(invocation.DeadlineAt)
		if _, err := service.RetryFailedTask(ctx, service.operatorIdentity.Principal, invocation.ID, TaskRecoveryRequest{
			ExpectedRevision: invocation.Revision,
			Reason:           "automatic recovery of a retryable execution-boundary failure",
			EvidenceRefs:     evidence,
			DeadlineAt:       deadline,
			IdempotencyKey:   automaticRetryableIdempotencyKey(invocation.ID),
		}); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

func automaticRetryableRecoveryDeadline(invocationDeadline time.Time) time.Time {
	return invocationDeadline.UTC().Add(time.Nanosecond)
}

func automaticRetryableIdempotencyKey(invocationID kernel.UUIDv7) string {
	return "auto-r3-" + string(invocationID)
}
