package operationalruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// autoGlitchTerminationReasons are terminal failure reasons that prove nothing
// about the work's correctness: the agent tripped an enforcement guardrail
// (for example a read-only validator calling mkdir) and the invocation was
// fenced without producing a verdict. They are retried through the same
// evidence-bound operator recovery path an operator would use, bounded by the
// planned attempt limit. Reasons outside this list remain an explicit operator
// decision; failure reason codes that suggest deliberate boundary-crossing are
// deliberately excluded.
var autoGlitchTerminationReasons = map[string]bool{
	"WORK_PURPOSE_REPOSITORY_MUTATION_NOT_AUTHORIZED": true,
}

type workTerminationReason struct {
	Reason  string `json:"reason"`
	Command string `json:"command"`
}

// glitchTerminatedTaskEligible reports whether the latest terminal invocation
// of a non-completed planned task is an automatically retryable glitch.
func glitchTerminatedTaskEligible(task organization.PlannedTask, state kernel.AggregateState, invocation kernel.WorkInvocation) bool {
	if state.Phase == kernel.PhaseCompleted || invocation.State != kernel.InvocationFailed {
		return false
	}
	if invocation.AttemptOrdinal >= uint64(task.AttemptLimit) {
		return false
	}
	if invocation.Retryable != nil && *invocation.Retryable {
		// The kernel already marked this retryable; ordinary admission owns it.
		return false
	}
	return invocation.OutputDigest != nil
}

// terminationReasonIsAutoRetryableGlitch classifies the recorded terminal
// reason output. Only an exactly-parseable reason envelope naming an allow-listed
// code qualifies; anything unrecognizable stays with the operator.
func terminationReasonIsAutoRetryableGlitch(output []byte) bool {
	var termination workTerminationReason
	if err := json.Unmarshal(output, &termination); err != nil {
		return false
	}
	return autoGlitchTerminationReasons[strings.TrimSpace(termination.Reason)]
}

// reconcileGlitchTerminatedTasks advances tasks whose latest invocation ended
// in an allow-listed enforcement glitch, reusing RetryFailedTask so automatic
// and operator recovery share one authorized path.
func (service *ProductionService) reconcileGlitchTerminatedTasks(ctx context.Context, feature organization.FeatureRequest, plan organization.FeaturePlan, states map[kernel.UUIDv7]kernel.AggregateState, invocations map[kernel.UUIDv7]kernel.WorkInvocation) (bool, error) {
	if feature.Plan == nil {
		return false, nil
	}
	for _, task := range plan.Tasks {
		state, found := states[task.ID]
		if !found || state.Phase == kernel.PhaseCompleted {
			continue
		}
		invocation, present := invocations[task.ID]
		if !present || !glitchTerminatedTaskEligible(task, state, invocation) {
			continue
		}
		output, err := service.Runtime.ReadExecutionOutput(ctx, *invocation.OutputDigest)
		if err != nil {
			return false, fmt.Errorf("read terminal output for task %s: %w", task.ID, err)
		}
		if !terminationReasonIsAutoRetryableGlitch(output) {
			continue
		}
		snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}})
		if err != nil {
			return false, err
		}
		evidence, err := evidenceRefsForIDs(snapshot, invocation.TerminalEvidenceIDs)
		if err != nil {
			return false, fmt.Errorf("glitch recovery evidence for task %s: %w", task.ID, err)
		}
		if len(evidence) == 0 {
			return false, fmt.Errorf("%w: glitch termination for task %s recorded no evidence", organization.ErrInvalidFeature, task.ID)
		}
		// The recovery deadline must be derived from durable state, not from the
		// reconciler clock: commands use deterministic IDs over the request, so a
		// per-pass deadline change turns an interrupted retry into a permanent
		// COMMAND_ID_REUSE conflict.
		budgetRef := kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: feature.BudgetAccountID}
		budget, budgetFound := snapshot.WorkBudgetAccounts[budgetRef]
		now := service.clock.Now().UTC()
		if !budgetFound || !budget.Valid() || !budget.DeadlineAt.After(now) || !budget.DeadlineAt.After(invocation.DeadlineAt) {
			continue
		}
		_, err = service.RetryFailedTask(ctx, service.operatorIdentity.Principal, invocation.ID, TaskRecoveryRequest{
			ExpectedRevision: invocation.Revision,
			Reason:           "automatic glitch recovery: " + workTerminationReasonLine(output),
			EvidenceRefs:     evidence,
			DeadlineAt:       budget.DeadlineAt,
			IdempotencyKey:   "auto-glitch-retry-" + string(task.ID) + "-" + string(invocation.ID),
		})
		if err != nil {
			return false, fmt.Errorf("automatic glitch retry for task %s invocation %s: %w", task.ID, invocation.ID, err)
		}
		return true, nil
	}
	return false, nil
}

func workTerminationReasonLine(output []byte) string {
	var termination workTerminationReason
	if json.Unmarshal(output, &termination) != nil {
		return "unclassified termination"
	}
	if termination.Command == "" {
		return termination.Reason
	}
	return termination.Reason + " (command: " + termination.Command + ")"
}

