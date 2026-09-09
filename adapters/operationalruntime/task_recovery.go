package operationalruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// TaskRecoveryRequest records an explicit operator decision to extend one
// terminal implementation task or one exactly identified invalid validator
// result after the operator has supplied remediation evidence. It intentionally
// has the same evidence and deadline requirements as feature-planning recovery.
type TaskRecoveryRequest = PlanningRecoveryRequest

// RetryFailedTask creates one evidence-bound extension for either a terminal
// implementation task or a successful validator invocation whose exact output
// block is still the task head. Automatic reconciliation still obeys the
// invocation's retryable flag and the planned attempt limit; only this explicit
// operator operation can continue after the underlying software or
// configuration defect has been remediated.
func (service *ProductionService) RetryFailedTask(ctx context.Context, principal kernel.PrincipalRef, invocationID kernel.UUIDv7, request TaskRecoveryRequest) (InvocationStatus, error) {
	if service == nil || principal != service.operatorIdentity.Principal || !invocationID.Valid() || !request.Valid() {
		return InvocationStatus{}, application.ErrInvalidConfiguration
	}
	terminalStatus, found, err := service.ReadInvocation(ctx, invocationID)
	if err != nil || !found || terminalStatus.Revision != request.ExpectedRevision {
		return InvocationStatus{}, fmt.Errorf("read terminal invocation: %w", errors.Join(application.ErrInvalidOperationalExecution, err))
	}
	terminalContext, err := service.Store.LoadOperationalExecution(ctx, invocationID)
	if err != nil || terminalContext.Invocation.Revision != terminalStatus.Revision || !terminalContext.Invocation.Valid() {
		return InvocationStatus{}, fmt.Errorf("load recoverable terminal invocation: %w", errors.Join(application.ErrInvalidOperationalExecution, err))
	}
	terminal := terminalContext.Invocation

	feature, planned, found, err := service.plannedFeatureTask(ctx, terminal.TaskID)
	if err != nil || !found {
		return InvocationStatus{}, fmt.Errorf("locate planned task: %w", errors.Join(organization.ErrInvalidFeature, err))
	}
	taskRef := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: planned.ID}
	state, head, stateFound, err := service.Store.ReadAggregateHead(ctx, taskRef)
	if err != nil || !stateFound {
		return InvocationStatus{}, fmt.Errorf("read active task head: %w", errors.Join(organization.ErrInvalidFeature, err))
	}
	recoveryKind, err := service.classifyTaskRecovery(ctx, planned, state, head, terminal)
	if err != nil {
		return InvocationStatus{}, fmt.Errorf("classify task recovery: %w", err)
	}
	operatorRevalidation := operatorRevalidatableTaskTerminal(planned, terminal)
	reopenCompleted := state.Phase == kernel.PhaseCompleted && (recoveryKind == taskRecoveryOperatorRepair || operatorRevalidation)
	candidateRecovery := recoveryKind == taskRecoveryInvalidStructuredOutput || operatorRevalidation
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: taskRef})
	if err != nil {
		return InvocationStatus{}, fmt.Errorf("load recovery decision state: %w", err)
	}
	latest, latestFound := latestTaskInvocation(snapshot.WorkInvocations, planned.ID)
	profileSnapshot, profileFound := snapshot.WorkProfiles[taskRef]
	budgetRef := kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: feature.BudgetAccountID}
	budget, budgetFound := snapshot.WorkBudgetAccounts[budgetRef]
	recoveryCondition, err := planningRecoveryConditionDigest(invocationID, request)
	now := service.clock.Now().UTC()
	requestedDeadline := request.DeadlineAt.UTC()
	if err != nil || !latestFound || latest.ID != terminal.ID || !profileFound || !profileSnapshot.Valid() || !budgetFound || !budget.Valid() || !requestedDeadline.After(now) || !requestedDeadline.After(terminal.DeadlineAt) || requestedDeadline.After(now.Add(service.planningDeadline)) {
		return InvocationStatus{}, fmt.Errorf("validate recovery preconditions: %w", errors.Join(organization.ErrInvalidFeature, err))
	}
	// Host-suspension recovery can legitimately extend the already accepted
	// profile and shared account by a few seconds after the operator request was
	// written. Preserve the later durable deadline instead of invalidating the
	// otherwise current recovery request.
	deadline := effectiveTaskRecoveryDeadline(requestedDeadline, profileSnapshot.Profile.Budgets.DeadlineAt, budget.DeadlineAt)

	evidenceIDs := make([]kernel.UUIDv7, len(request.EvidenceRefs))
	for index, evidence := range request.EvidenceRefs {
		evidenceIDs[index] = evidence.EvidenceID
	}
	registered, err := evidenceRefsForIDs(snapshot, evidenceIDs)
	if err != nil || !sameEvidenceSet(registered, request.EvidenceRefs) {
		return InvocationStatus{}, fmt.Errorf("validate recovery evidence: %w", errors.Join(organization.ErrInvalidFeature, err))
	}
	profileEvidenceIDs := recoveryProfileEvidenceIDs(terminal, evidenceIDs)
	if _, err := evidenceRefsForIDs(snapshot, profileEvidenceIDs); err != nil {
		return InvocationStatus{}, fmt.Errorf("validate recovery profile evidence: %w", err)
	}
	var successorProfile kernel.WorkRiskProfile
	profileBound := false
	profileAlreadyAdvanced := profileSnapshot.Profile.Binding() != terminal.WorkProfile
	if profileAlreadyAdvanced {
		successorProfile, profileBound, err = continuedTaskRecoveryProfile(profileSnapshot.Profile, service.planning, recoveryCondition, deadline, profileEvidenceIDs, state.LifecycleEpoch, state.ScopeRevision)
	} else if reopenCompleted {
		successorProfile, err = reopenedTaskRecoveryProfile(profileSnapshot.Profile, service.planning, recoveryCondition, deadline, profileEvidenceIDs, state.LifecycleEpoch+1, state.ScopeRevision+1)
	} else if recoveryKind == taskRecoveryOperatorRepair || operatorRevalidation {
		successorProfile, profileBound, err = continuedTaskRecoveryProfile(profileSnapshot.Profile, service.planning, recoveryCondition, deadline, profileEvidenceIDs, state.LifecycleEpoch, state.ScopeRevision)
	} else {
		successorProfile, profileBound, err = planningRecoveryProfile(profileSnapshot.Profile, terminal.WorkProfile, service.planning, recoveryCondition, deadline, profileEvidenceIDs)
	}
	if err != nil {
		return InvocationStatus{}, fmt.Errorf("prepare recovery profile: %w", err)
	}
	budget, err = service.amendPlanningRecoveryBudget(ctx, principal, feature, terminal, budget, request, recoveryCondition, registered)
	if err != nil {
		return InvocationStatus{}, fmt.Errorf("amend recovery budget: %w", err)
	}
	if reopenCompleted {
		state, head, err = service.reopenCompletedTaskForRecovery(ctx, feature, planned, state, head, request, registered)
		if err != nil {
			return InvocationStatus{}, fmt.Errorf("reopen completed task for recovery: %w", err)
		}
	}
	owner, found, err := service.RoleHost.Status(ctx, planned.Owner)
	if err != nil {
		return InvocationStatus{}, fmt.Errorf("read recovery actor: %w", err)
	}
	transition, err := planRecoveryRoleTransition(owner, found, terminal.Execution)
	if err != nil {
		return InvocationStatus{}, fmt.Errorf("plan recovery actor transition: %w", err)
	}
	switch transition {
	case recoveryRoleStart:
		owner, err = service.StartRole(ctx, planned.Owner)
	case recoveryRoleRestart:
		owner, err = service.RestartRole(ctx, planned.Owner)
	}
	if err != nil || owner.Status != organization.RoleIdle || owner.Execution == terminal.Execution {
		return InvocationStatus{}, fmt.Errorf("start recovery actor: %w", errors.Join(organization.ErrRoleNotRunning, err))
	}
	profileConfig, configured := service.profilesByModel[owner.ModelProfile]
	if !configured || !profileConfig.qualifiedFor(planned.DecisionRoute, workKindForPurpose(planned.Purpose, planned.Risk), service.clock.Now().UTC()) {
		return InvocationStatus{}, fmt.Errorf("resolve recovery profile and workspace: %w", organization.ErrInvalidFeature)
	}
	var workspace ProductionWorkspace
	var candidateEvidence []kernel.EvidenceRef
	if candidateRecovery {
		if feature.Plan == nil {
			return InvocationStatus{}, fmt.Errorf("resolve validation plan: %w", organization.ErrInvalidFeature)
		}
		workspace, candidateEvidence, err = service.prepareCandidateConsumerWorkspace(ctx, feature, planned, owner, *feature.Plan, latestPlannedInvocations(*feature.Plan, snapshot.WorkInvocations), registered)
	} else {
		workspace, err = service.workspaceForExistingTask(ctx, planned, owner, snapshot)
	}
	if err != nil {
		return InvocationStatus{}, fmt.Errorf("resolve recovery workspace: %w", err)
	}
	if recoveryKind == taskRecoveryInvalidStructuredOutput {
		if err := service.unblockInvalidStructuredTask(ctx, feature, planned, state, head, terminal, registered, request.IdempotencyKey); err != nil {
			return InvocationStatus{}, fmt.Errorf("unblock invalid validator output: %w", err)
		}
	}
	state, head, stateFound, err = service.Store.ReadAggregateHead(ctx, taskRef)
	if err != nil || !stateFound || state.Phase != kernel.PhaseActive || state.Condition != kernel.ConditionRunnable {
		return InvocationStatus{}, fmt.Errorf("refresh task head: %w", errors.Join(organization.ErrInvalidFeature, err))
	}
	tracked := &trackedTask{plan: planned, revision: state.Revision, last: head, profile: successorProfile, owner: owner}
	if !profileBound {
		fresh, loadErr := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: taskRef})
		if loadErr != nil {
			return InvocationStatus{}, fmt.Errorf("refresh recovery evidence: %w", loadErr)
		}
		profileEvidence, evidenceErr := evidenceRefsForIDs(fresh, successorProfile.ClassificationEvidenceIDs)
		if evidenceErr != nil {
			return InvocationStatus{}, fmt.Errorf("load recovery profile evidence: %w", evidenceErr)
		}
		key := "task-recovery-profile-" + string(terminal.ID) + "-" + string(successorProfile.ProfileID) + "-" + request.IdempotencyKey
		if err := service.applyTaskCommand(ctx, feature, tracked, "tekroo.command.task.bind-work-profile", kernel.SchemaVersion, service.policyAuthority, successorProfile, profileEvidence, nil, key); err != nil {
			return InvocationStatus{}, fmt.Errorf("bind recovery profile: %w", err)
		}
	}
	if err := service.rebindPlanningRecoveryAssignment(ctx, feature, tracked, profileConfig, owner); err != nil {
		return InvocationStatus{}, fmt.Errorf("rebind recovery assignment: %w", err)
	}
	if candidateRecovery {
		if err := service.rebindTaskCandidateWorkspace(ctx, feature, tracked, workspace, candidateEvidence); err != nil {
			return InvocationStatus{}, fmt.Errorf("rebind recovery candidate: %w", err)
		}
	} else if err := service.refreshTaskExecutionBinding(ctx, feature, tracked, profileConfig, workspace); err != nil {
		return InvocationStatus{}, fmt.Errorf("refresh recovery execution binding: %w", err)
	}
	nextPurpose := planned.Purpose
	nextAttempt := terminal.AttemptOrdinal + 1
	predecessor := &terminal
	conditionDigests := []kernel.Digest{recoveryCondition}
	if recoveryKind == taskRecoveryOperatorRepair {
		nextPurpose = kernel.PurposeRepair
		nextAttempt = nextTaskPurposeAttempt(snapshot.WorkInvocations, planned.ID, nextPurpose)
		predecessor = nil
		conditionDigests = []kernel.Digest{*terminal.OutputDigest, recoveryCondition}
	}
	budgetRevision, err := service.extendTaskTechnicalRetryBudget(ctx, feature, tracked, nextPurpose, nextAttempt)
	if err != nil || budgetRevision != budget.Revision {
		return InvocationStatus{}, fmt.Errorf("extend technical retry budget: %w", errors.Join(organization.ErrInvalidFeature, err))
	}
	if recoveryKind == taskRecoveryInvalidStructuredOutput {
		conditionDigests, err = validationRecoveryConditionDigests(planned, snapshot.WorkInvocations, terminal)
		if err != nil {
			return InvocationStatus{}, fmt.Errorf("prepare validator recovery condition: %w", err)
		}
	} else if planned.Purpose == kernel.PurposeValidation || planned.Purpose == kernel.PurposeReview {
		conditionDigests, err = terminalValidationRecoveryConditionDigests(planned, snapshot.WorkInvocations, recoveryCondition)
		if err != nil {
			return InvocationStatus{}, fmt.Errorf("prepare terminal validator recovery condition: %w", err)
		}
	}
	if err := service.authorizeTaskInvocationWithConditionPolicy(ctx, feature, tracked, profileConfig, workspace, budgetRevision, nextPurpose, nextAttempt, predecessor, conditionDigests, true, false); err != nil {
		return InvocationStatus{}, fmt.Errorf("authorize recovery invocation: %w", err)
	}

	updated, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: taskRef})
	if err != nil {
		return InvocationStatus{}, fmt.Errorf("load authorized recovery invocation: %w", err)
	}
	next, nextFound := latestTaskInvocation(updated.WorkInvocations, planned.ID)
	if !nextFound || next.ID == invocationID || next.Purpose != nextPurpose || next.AttemptOrdinal != nextAttempt {
		return InvocationStatus{}, fmt.Errorf("verify authorized recovery invocation: %w", application.ErrInvalidOperationalExecution)
	}
	status, statusFound, err := service.ReadInvocation(ctx, next.ID)
	if err != nil || !statusFound {
		return InvocationStatus{}, fmt.Errorf("read authorized recovery invocation: %w", errors.Join(application.ErrInvalidOperationalExecution, err))
	}
	return status, nil
}

func nextTaskPurposeAttempt(invocations map[kernel.AggregateRef]kernel.WorkInvocation, taskID kernel.UUIDv7, purpose kernel.WorkPurpose) uint64 {
	next := uint64(1)
	for _, invocation := range invocations {
		if invocation.TaskID == taskID && invocation.Purpose == purpose && invocation.AttemptOrdinal >= next {
			next = invocation.AttemptOrdinal + 1
		}
	}
	return next
}

func effectiveTaskRecoveryDeadline(requested, profile, account time.Time) time.Time {
	result := requested.UTC()
	if profile.After(result) {
		result = profile.UTC()
	}
	if account.After(result) {
		result = account.UTC()
	}
	return result
}

func latestPlannedInvocations(plan organization.FeaturePlan, invocations map[kernel.AggregateRef]kernel.WorkInvocation) map[kernel.UUIDv7]kernel.WorkInvocation {
	result := make(map[kernel.UUIDv7]kernel.WorkInvocation, len(plan.Tasks))
	for _, task := range plan.Tasks {
		if latest, found := latestTaskInvocation(invocations, task.ID); found {
			result[task.ID] = latest
		}
	}
	return result
}

type taskRecoveryKind uint8

const (
	taskRecoveryTerminalInvocation taskRecoveryKind = iota
	taskRecoveryInvalidStructuredOutput
	taskRecoveryOperatorRepair
)

func (service *ProductionService) classifyTaskRecovery(ctx context.Context, task organization.PlannedTask, state kernel.AggregateState, head kernel.UUIDv7, invocation kernel.WorkInvocation) (taskRecoveryKind, error) {
	recoverablePurpose := task.Purpose == kernel.PurposeImplementation || task.Purpose == kernel.PurposeRepair ||
		(task.Purpose == kernel.PurposeValidation || task.Purpose == kernel.PurposeReview) && len(task.Validates) > 0
	if recoverablePurpose && state.Condition == kernel.ConditionRunnable && (recoverableTaskTerminal(invocation) || operatorRevalidatableTaskTerminal(task, invocation)) {
		return taskRecoveryTerminalInvocation, nil
	}
	if task.Purpose == kernel.PurposeImplementation && (state.Phase == kernel.PhaseActive || state.Phase == kernel.PhaseCompleted) && state.Condition == kernel.ConditionRunnable && operatorRepairableTaskTerminal(task, invocation) {
		return taskRecoveryOperatorRepair, nil
	}
	if task.Purpose != kernel.PurposeValidation && task.Purpose != kernel.PurposeReview || len(task.Validates) == 0 || state.Condition != kernel.ConditionBlocked || invocation.State != kernel.InvocationSucceeded || invocation.OutputDigest == nil {
		return 0, application.ErrInvalidOperationalExecution
	}
	blocked, found, err := service.Store.ReadEvent(ctx, head)
	if err != nil || !found || !isExactInvalidStructuredOutputBlock(blocked, task, invocation, service.policyAuthority) {
		return 0, errors.Join(application.ErrInvalidOperationalExecution, err)
	}
	return taskRecoveryInvalidStructuredOutput, nil
}

// A later validator or operator inspection can find a material defect after an
// implementation invocation succeeded. While the task is still active and
// runnable, preserve that result as evidence and authorize a repair on the
// same planned task instead of restarting the feature workflow.
func operatorRepairableTaskTerminal(task organization.PlannedTask, invocation kernel.WorkInvocation) bool {
	return task.Purpose == kernel.PurposeImplementation && invocation.Valid() &&
		invocation.TaskID == task.ID && invocation.Purpose == kernel.PurposeImplementation &&
		invocation.State == kernel.InvocationSucceeded && invocation.OutputDigest != nil
}

func reopenedTaskRecoveryProfile(current kernel.WorkRiskProfile, planning ProductionPlanning, condition kernel.Digest, deadline time.Time, evidenceIDs []kernel.UUIDv7, lifecycleEpoch, scopeRevision uint64) (kernel.WorkRiskProfile, error) {
	if !current.Valid() || planning.PolicyRevision == 0 || !planning.ClassificationPolicyDigest.Valid() || !planning.PromotionPolicyDigest.Valid() || !planning.VerificationTopologyDigest.Valid() || !condition.Valid() || deadline.IsZero() || lifecycleEpoch != current.LifecycleEpoch+1 || scopeRevision != current.ScopeRevision+1 {
		return kernel.WorkRiskProfile{}, organization.ErrInvalidFeature
	}
	next := current.Clone()
	next.ProfileID = deterministicOperationalUUID("task-repair-reopen-profile", string(current.ProfileID), string(condition))
	next.ProfileRevision = current.ProfileRevision + 1
	next.LifecycleEpoch = lifecycleEpoch
	next.ScopeRevision = scopeRevision
	next.Budgets.DeadlineAt = deadline
	next.ClassificationPolicyRevision = planning.PolicyRevision
	next.ClassificationPolicyDigest = planning.ClassificationPolicyDigest
	next.PromotionPolicyRevision = planning.PolicyRevision
	next.PromotionPolicyDigest = planning.PromotionPolicyDigest
	next.VerificationTopologyDigest = planning.VerificationTopologyDigest
	priorID := current.ProfileID
	next.SupersedesProfileID = &priorID
	next.ClassificationEvidenceIDs = append(next.ClassificationEvidenceIDs, evidenceIDs...)
	sort.Slice(next.ClassificationEvidenceIDs, func(left, right int) bool {
		return next.ClassificationEvidenceIDs[left] < next.ClassificationEvidenceIDs[right]
	})
	next.ClassificationEvidenceIDs = uniqueUUIDs(next.ClassificationEvidenceIDs)
	next.ProfileDigest = ""
	encoded, err := json.Marshal(next)
	if err != nil {
		return kernel.WorkRiskProfile{}, err
	}
	next.ProfileDigest = digestBytes(encoded)
	if !next.Valid() {
		return kernel.WorkRiskProfile{}, organization.ErrInvalidFeature
	}
	return next, nil
}

// A crash or rejected downstream command can leave a reopened task active with
// its recovery profile already committed. Resume from that durable profile. If
// the operator supplies a later deadline, create a direct successor in the
// same lifecycle instead of trying to reconstruct the pre-reopen profile.
func continuedTaskRecoveryProfile(current kernel.WorkRiskProfile, planning ProductionPlanning, condition kernel.Digest, deadline time.Time, evidenceIDs []kernel.UUIDv7, lifecycleEpoch, scopeRevision uint64) (kernel.WorkRiskProfile, bool, error) {
	if !current.Valid() {
		return kernel.WorkRiskProfile{}, false, fmt.Errorf("%w: current recovery profile is invalid", organization.ErrInvalidFeature)
	}
	if planning.PolicyRevision == 0 || !planning.ClassificationPolicyDigest.Valid() || !planning.PromotionPolicyDigest.Valid() || !planning.VerificationTopologyDigest.Valid() {
		return kernel.WorkRiskProfile{}, false, fmt.Errorf("%w: recovery planning policy is invalid", organization.ErrInvalidFeature)
	}
	if !condition.Valid() || deadline.IsZero() || lifecycleEpoch != current.LifecycleEpoch || scopeRevision != current.ScopeRevision || deadline.Before(current.Budgets.DeadlineAt) {
		return kernel.WorkRiskProfile{}, false, fmt.Errorf("%w: recovery condition, deadline, or task epoch is invalid", organization.ErrInvalidFeature)
	}
	if deadline.Equal(current.Budgets.DeadlineAt) &&
		current.ClassificationPolicyRevision == planning.PolicyRevision && current.ClassificationPolicyDigest == planning.ClassificationPolicyDigest &&
		current.PromotionPolicyRevision == planning.PolicyRevision && current.PromotionPolicyDigest == planning.PromotionPolicyDigest &&
		current.VerificationTopologyDigest == planning.VerificationTopologyDigest && containsEveryUUID(current.ClassificationEvidenceIDs, evidenceIDs) && planningRecoveryProfileDigestMatches(current) {
		return current, true, nil
	}
	next := current.Clone()
	next.ProfileID = deterministicOperationalUUID("task-repair-continuation-profile", string(current.ProfileID), string(condition))
	next.ProfileRevision = current.ProfileRevision + 1
	next.Budgets.DeadlineAt = deadline
	next.ClassificationPolicyRevision = planning.PolicyRevision
	next.ClassificationPolicyDigest = planning.ClassificationPolicyDigest
	next.PromotionPolicyRevision = planning.PolicyRevision
	next.PromotionPolicyDigest = planning.PromotionPolicyDigest
	next.VerificationTopologyDigest = planning.VerificationTopologyDigest
	priorID := current.ProfileID
	next.SupersedesProfileID = &priorID
	next.ClassificationEvidenceIDs = append(next.ClassificationEvidenceIDs, evidenceIDs...)
	sort.Slice(next.ClassificationEvidenceIDs, func(left, right int) bool {
		return next.ClassificationEvidenceIDs[left] < next.ClassificationEvidenceIDs[right]
	})
	next.ClassificationEvidenceIDs = uniqueUUIDs(next.ClassificationEvidenceIDs)
	next.ProfileDigest = ""
	encoded, err := json.Marshal(next)
	if err != nil {
		return kernel.WorkRiskProfile{}, false, err
	}
	next.ProfileDigest = digestBytes(encoded)
	if !next.Valid() {
		return kernel.WorkRiskProfile{}, false, fmt.Errorf("%w: successor recovery profile is invalid", organization.ErrInvalidFeature)
	}
	return next, false, nil
}

func (service *ProductionService) reopenCompletedTaskForRecovery(ctx context.Context, feature organization.FeatureRequest, task organization.PlannedTask, state kernel.AggregateState, head kernel.UUIDv7, request TaskRecoveryRequest, evidence []kernel.EvidenceRef) (kernel.AggregateState, kernel.UUIDv7, error) {
	if state.Kind != kernel.AggregateTask || state.ID != task.ID || state.Phase != kernel.PhaseCompleted || state.Condition != kernel.ConditionRunnable || state.Ownership.OwnerFQN == nil || *state.Ownership.OwnerFQN != task.Owner {
		return kernel.AggregateState{}, "", organization.ErrInvalidFeature
	}
	payload, err := json.Marshal(map[string]any{
		"prior_epoch": state.LifecycleEpoch, "new_scope_revision": state.ScopeRevision + 1,
		"reason": request.Reason, "owner_carry_forward": true, "evidence_ids": evidenceIDs(evidence),
	})
	if err != nil {
		return kernel.AggregateState{}, "", err
	}
	key := "operator-task-recovery-reopen-" + string(task.ID) + "-" + request.IdempotencyKey
	if _, err := service.submitStandaloneCommand(ctx, "tekroo.command.work.reopen", kernel.AggregateTask, task.ID, service.policyAuthority, state.Revision, payload, []kernel.DagParent{{ParentEventID: head, EdgeKind: kernel.EdgeResponse}}, evidence, key); err != nil {
		return kernel.AggregateState{}, "", err
	}
	updated, updatedHead, found, err := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID})
	if err != nil || !found || updated.Phase != kernel.PhaseActive || updated.Condition != kernel.ConditionRunnable || updated.LifecycleEpoch != state.LifecycleEpoch+1 || updated.ScopeRevision != state.ScopeRevision+1 || updated.Ownership.OwnerFQN == nil || *updated.Ownership.OwnerFQN != task.Owner {
		return kernel.AggregateState{}, "", errors.Join(organization.ErrInvalidFeature, err)
	}
	return updated, updatedHead, nil
}

// A syntactically valid validator result can still be materially wrong. While
// the task remains runnable, an operator may bind contrary evidence and request
// one successor validation instead of allowing that result to advance. Once
// task review consumes the result and changes task state, this path closes.
func operatorRevalidatableTaskTerminal(task organization.PlannedTask, invocation kernel.WorkInvocation) bool {
	return (task.Purpose == kernel.PurposeValidation || task.Purpose == kernel.PurposeReview) &&
		len(task.Validates) > 0 && invocation.Valid() && invocation.State == kernel.InvocationSucceeded && invocation.OutputDigest != nil
}

func validationRecoveryConditionDigests(task organization.PlannedTask, invocations map[kernel.AggregateRef]kernel.WorkInvocation, prior kernel.WorkInvocation) ([]kernel.Digest, error) {
	conditions := make([]kernel.Digest, 0, len(task.DependsOn)+1)
	for _, dependencyID := range task.DependsOn {
		candidate, found := latestTaskInvocation(invocations, dependencyID)
		if !found || candidate.State != kernel.InvocationSucceeded || candidate.OutputDigest == nil {
			return nil, organization.ErrInvalidFeature
		}
		conditions = append(conditions, *candidate.OutputDigest)
	}
	if len(conditions) != len(task.DependsOn) || prior.OutputDigest == nil {
		return nil, organization.ErrInvalidFeature
	}
	return append(conditions, *prior.OutputDigest), nil
}

func terminalValidationRecoveryConditionDigests(task organization.PlannedTask, invocations map[kernel.AggregateRef]kernel.WorkInvocation, recoveryCondition kernel.Digest) ([]kernel.Digest, error) {
	if !recoveryCondition.Valid() {
		return nil, organization.ErrInvalidFeature
	}
	conditions := make([]kernel.Digest, 0, len(task.DependsOn)+1)
	for _, dependencyID := range task.DependsOn {
		candidate, found := latestTaskInvocation(invocations, dependencyID)
		if !found || candidate.State != kernel.InvocationSucceeded || candidate.OutputDigest == nil {
			return nil, organization.ErrInvalidFeature
		}
		conditions = append(conditions, *candidate.OutputDigest)
	}
	if len(conditions) != len(task.DependsOn) {
		return nil, organization.ErrInvalidFeature
	}
	return append(conditions, recoveryCondition), nil
}

func (service *ProductionService) unblockInvalidStructuredTask(ctx context.Context, feature organization.FeatureRequest, task organization.PlannedTask, state kernel.AggregateState, head kernel.UUIDv7, invocation kernel.WorkInvocation, evidence []kernel.EvidenceRef, idempotencyKey string) error {
	payload, err := json.Marshal(map[string]any{
		"resolved_blocker_refs": []string{"teams://work-invocation/" + string(invocation.ID)},
		"evidence_ids":          evidenceIDs(evidence),
	})
	if err != nil {
		return err
	}
	key := "invalid-validator-output-recovery-" + string(task.ID) + "-" + idempotencyKey
	_, err = service.submitStandaloneCommand(ctx, "tekroo.command.work.unblock", kernel.AggregateTask, task.ID, service.policyAuthority, state.Revision, payload, []kernel.DagParent{{ParentEventID: head, EdgeKind: kernel.EdgeResponse}}, evidence, key)
	return err
}

func recoverableTaskTerminal(invocation kernel.WorkInvocation) bool {
	if !invocation.Valid() {
		return false
	}
	switch invocation.State {
	case kernel.InvocationCancelled:
		return invocation.CancellationRequestedAt != nil
	case kernel.InvocationTimedOut, kernel.InvocationFailed, kernel.InvocationStartFailed:
		return true
	default:
		return false
	}
}

type recoveryRoleTransition uint8

const (
	recoveryRoleReuse recoveryRoleTransition = iota
	recoveryRoleStart
	recoveryRoleRestart
)

// planRecoveryRoleTransition distinguishes a durable role record from a
// running role. Host.Status reports whether a record exists, so STOPPED must
// start a new execution rather than being rejected as an active non-idle role.
func planRecoveryRoleTransition(owner organization.RoleInstanceState, found bool, terminal kernel.ExecutionTuple) (recoveryRoleTransition, error) {
	if !found || owner.Status == organization.RoleStopped {
		return recoveryRoleStart, nil
	}
	switch owner.Status {
	case organization.RoleIdle:
		if owner.Execution == terminal {
			return recoveryRoleRestart, nil
		}
		return recoveryRoleReuse, nil
	case organization.RoleFailed:
		return recoveryRoleRestart, nil
	default:
		return recoveryRoleReuse, organization.ErrRoleNotRunning
	}
}

func (service *ProductionService) plannedFeatureTask(ctx context.Context, taskID kernel.UUIDv7) (organization.FeatureRequest, organization.PlannedTask, bool, error) {
	features, err := service.Store.ListFeatures(ctx, []organization.FeatureStatus{organization.FeaturePlanned, organization.FeatureApproved}, 1000)
	if err != nil {
		return organization.FeatureRequest{}, organization.PlannedTask{}, false, err
	}
	for _, feature := range features {
		if feature.Plan == nil {
			continue
		}
		for _, task := range feature.Plan.Tasks {
			if task.ID == taskID {
				return feature, task, true, nil
			}
		}
	}
	return organization.FeatureRequest{}, organization.PlannedTask{}, false, nil
}
