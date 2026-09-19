package operationalruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
	"ROLE_REPOSITORY_MUTATION_NOT_AUTHORIZED":         true,
	"WORK_PURPOSE_REPOSITORY_MUTATION_NOT_AUTHORIZED": true,
	// A repeated repository inspection is an agent-control failure, not a
	// verdict about the assigned work. A fresh evidence-bound continuation gets
	// one bounded recovery attempt; an identical successor is escalated below.
	"REPEATED_CAPABILITY_MISMATCH_REPOSITORY_NO_PROGRESS": true,
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
	return invocation.OutputDigest != nil
}

type failedTaskDisposition uint8

const (
	failedTaskNoAction failedTaskDisposition = iota
	failedTaskRecover
	failedTaskEscalate
)

// classifyFailedTaskDisposition ensures that a terminal invocation does not
// silently abandon its task. It either gets one evidence-bound continuation or
// the task is durably blocked for operator escalation.
func classifyFailedTaskDisposition(task organization.PlannedTask, state kernel.AggregateState, invocation kernel.WorkInvocation, output []byte, repeated bool) failedTaskDisposition {
	if state.Phase == kernel.PhaseCompleted || invocation.State != kernel.InvocationFailed || invocation.OutputDigest == nil {
		return failedTaskNoAction
	}
	if invocation.AttemptOrdinal >= uint64(task.AttemptLimit) || repeated {
		return failedTaskEscalate
	}
	if invocation.Retryable != nil && *invocation.Retryable || terminationReasonIsAutoRetryableGlitch(output) {
		return failedTaskRecover
	}
	return failedTaskEscalate
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
		if !present || invocation.State != kernel.InvocationFailed || invocation.OutputDigest == nil {
			continue
		}
		snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}})
		if err != nil {
			return false, err
		}
		output, err := service.Runtime.ReadExecutionOutput(ctx, *invocation.OutputDigest)
		if err != nil {
			return false, fmt.Errorf("read terminal output for task %s: %w", task.ID, err)
		}
		evidence, err := evidenceRefsForIDs(snapshot, invocation.TerminalEvidenceIDs)
		if err != nil {
			return false, fmt.Errorf("glitch recovery evidence for task %s: %w", task.ID, err)
		}
		if len(evidence) == 0 {
			return false, fmt.Errorf("%w: glitch termination for task %s recorded no evidence", organization.ErrInvalidFeature, task.ID)
		}
		disposition := classifyFailedTaskDisposition(task, state, invocation, output, repeatedTerminalOutput(snapshot, invocation))
		if disposition == failedTaskNoAction {
			continue
		}
		if disposition == failedTaskEscalate {
			if state.Condition == kernel.ConditionBlocked {
				continue
			}
			// The command must be attributed to the actor's currently registered
			// execution, not the failed invocation's tuple: after a daemon restart
			// the invocation's execution is no longer current and the kernel
			// rejects attribution as STALE_EXECUTION. While the actor has no
			// registered execution (role stopped), escalation is deferred to a
			// later pass rather than poisoning the reconciliation loop.
			execution, attributable := snapshot.CurrentExecutions[invocation.ActorFQN]
			if !attributable {
				continue
			}
			payload, marshalErr := failedTaskEscalationPayload(invocation.ID)
			if marshalErr != nil {
				return false, marshalErr
			}
			key := failedTaskEscalationKey(task.ID, invocation.ID, payload)
			_, err = service.submitDeterministicActorTargetCommand(ctx, feature, "tekroo.command.work.block", kernel.AggregateTask, task.ID, service.policyAuthority, invocation.ActorFQN, execution, state.Revision, state.LifecycleEpoch, payload, []kernel.DagParent{{ParentEventID: invocation.LastEventID, EdgeKind: kernel.EdgeResponse}}, evidence, key)
			if err != nil {
				return false, fmt.Errorf("escalate failed task %s invocation %s: %w", task.ID, invocation.ID, err)
			}
			return true, nil
		}
		// The recovery deadline must be derived from the terminal invocation, not
		// the current shared-budget deadline or reconciler clock. The recovery
		// command has a deterministic idempotency key; changing its request as a
		// different task advances the shared account turns an interrupted recovery
		// into COMMAND_ID_REUSE. A strict successor of the terminal deadline is
		// stable across every reconciliation pass, and an already-extended shared
		// account is reused by RetryFailedTask.
		budgetRef := kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: feature.BudgetAccountID}
		budget, budgetFound := snapshot.WorkBudgetAccounts[budgetRef]
		now := service.clock.Now().UTC()
		if !budgetFound || !budget.Valid() {
			continue
		}
		deadline := automaticGlitchRecoveryDeadline(invocation.DeadlineAt)
		if !deadline.After(now) || deadline.After(now.Add(service.planningDeadline)) {
			continue
		}
		_, err = service.RetryFailedTask(ctx, service.operatorIdentity.Principal, invocation.ID, TaskRecoveryRequest{
			ExpectedRevision: invocation.Revision,
			Reason:           "automatic glitch recovery: " + workTerminationReasonLine(output),
			EvidenceRefs:     evidence,
			DeadlineAt:       deadline,
			IdempotencyKey:   "auto-glitch-retry-" + string(task.ID) + "-" + string(invocation.ID),
		})
		if err != nil {
			return false, fmt.Errorf("automatic glitch retry for task %s invocation %s: %w", task.ID, invocation.ID, err)
		}
		return true, nil
	}
	return false, nil
}

func repeatedTerminalOutput(snapshot kernel.Snapshot, current kernel.WorkInvocation) bool {
	if current.OutputDigest == nil {
		return false
	}
	for _, prior := range snapshot.WorkInvocations {
		if prior.ID == current.ID || prior.TaskID != current.TaskID || !prior.State.Terminal() || prior.OutputDigest == nil {
			continue
		}
		if *prior.OutputDigest == *current.OutputDigest {
			return true
		}
	}
	return false
}

func automaticGlitchRecoveryDeadline(invocationDeadline time.Time) time.Time {
	return invocationDeadline.Add(time.Nanosecond)
}

// failedTaskEscalationPayload builds the work.block payload for an
// unrecoverable failed invocation. The accepted payload schema requires
// review_policy alongside blocker_refs and reason; a payload without it is
// rejected by the catalogue with INVALID_PAYLOAD before the kernel ever sees
// the command.
func failedTaskEscalationPayload(invocationID kernel.UUIDv7) ([]byte, error) {
	return json.Marshal(map[string]any{
		"blocker_refs":  []string{"teams://work-invocation/" + string(invocationID)},
		"reason":        failedTaskEscalationReason,
		"review_policy": invalidStructuredReviewPolicy,
	})
}

const failedTaskEscalationReason = "task invocation failed without an admissible automatic recovery; operator escalation is required"

// failedTaskEscalationKey is content-derived: it includes the admitted
// payload digest so a durable rejection recorded under a different payload
// (for example one missing the schema-required review_policy) can never
// collide with a corrected retry as COMMAND_ID_REUSE. This mirrors the
// precedent in blockStructuredDecisionTask. The v2 namespace exists because
// the v2 command carries actor attribution the v1 shape never had, and the
// kernel's command-identity ledger has durable rejections recorded under
// every v2 key the earlier command shape attempted.
func failedTaskEscalationKey(taskID, invocationID kernel.UUIDv7, payload []byte) string {
	return "failed-task-escalation-v2-" + string(taskID) + "-" + string(invocationID) + "-" + string(digestBytes(payload))
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
