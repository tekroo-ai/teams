package operationalruntime

import (
	"context"
	"errors"
	"fmt"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// TaskRecoveryRequest records an explicit operator decision to extend one
// exhausted, retryable implementation task. It intentionally has the same
// evidence and deadline requirements as feature-planning recovery.
type TaskRecoveryRequest = PlanningRecoveryRequest

// RetryFailedTask creates one evidence-bound technical extension for an
// exhausted implementation task. Automatic reconciliation never exceeds the
// task's planned attempt limit; only this explicit operator operation may do
// so.
func (service *ProductionService) RetryFailedTask(ctx context.Context, principal kernel.PrincipalRef, invocationID kernel.UUIDv7, request TaskRecoveryRequest) (InvocationStatus, error) {
	if service == nil || principal != service.operatorIdentity.Principal || !invocationID.Valid() || !request.Valid() {
		return InvocationStatus{}, application.ErrInvalidConfiguration
	}
	terminalStatus, found, err := service.ReadInvocation(ctx, invocationID)
	if err != nil || !found || terminalStatus.Revision != request.ExpectedRevision {
		return InvocationStatus{}, fmt.Errorf("read terminal invocation: %w", errors.Join(application.ErrInvalidOperationalExecution, err))
	}
	terminalContext, err := service.Store.LoadOperationalExecution(ctx, invocationID)
	if err != nil || terminalContext.Invocation.Revision != terminalStatus.Revision || !recoverablePlanningTerminal(terminalContext.Invocation) {
		return InvocationStatus{}, fmt.Errorf("load recoverable terminal invocation: %w", errors.Join(application.ErrInvalidOperationalExecution, err))
	}
	terminal := terminalContext.Invocation

	feature, planned, found, err := service.plannedFeatureTask(ctx, terminal.TaskID)
	if err != nil || !found || planned.Purpose != kernel.PurposeImplementation && planned.Purpose != kernel.PurposeRepair {
		return InvocationStatus{}, fmt.Errorf("locate planned task: %w", errors.Join(organization.ErrInvalidFeature, err))
	}
	taskRef := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: planned.ID}
	state, head, stateFound, err := service.Store.ReadAggregateHead(ctx, taskRef)
	if err != nil || !stateFound || state.Phase != kernel.PhaseActive || state.Condition != kernel.ConditionRunnable {
		return InvocationStatus{}, fmt.Errorf("read runnable task head: %w", errors.Join(organization.ErrInvalidFeature, err))
	}
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
	deadline := request.DeadlineAt.UTC()
	if err != nil || !latestFound || latest.ID != terminal.ID || !profileFound || !profileSnapshot.Valid() || !budgetFound || !budget.Valid() || !deadline.After(now) || !deadline.After(terminal.DeadlineAt) || deadline.Before(profileSnapshot.Profile.Budgets.DeadlineAt) || deadline.After(now.Add(service.planningDeadline)) {
		return InvocationStatus{}, fmt.Errorf("validate recovery preconditions: %w", errors.Join(organization.ErrInvalidFeature, err))
	}

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
	successorProfile, profileBound, err := planningRecoveryProfile(profileSnapshot.Profile, terminal.WorkProfile, service.planning, recoveryCondition, deadline, profileEvidenceIDs)
	if err != nil {
		return InvocationStatus{}, fmt.Errorf("prepare recovery profile: %w", err)
	}
	budget, err = service.amendPlanningRecoveryBudget(ctx, principal, feature, terminal, budget, request, recoveryCondition, registered)
	if err != nil {
		return InvocationStatus{}, fmt.Errorf("amend recovery budget: %w", err)
	}
	tracked := &trackedTask{plan: planned, revision: state.Revision, last: head, profile: successorProfile}
	if !profileBound {
		profileEvidence, evidenceErr := evidenceRefsForIDs(snapshot, successorProfile.ClassificationEvidenceIDs)
		if evidenceErr != nil {
			return InvocationStatus{}, fmt.Errorf("load recovery profile evidence: %w", evidenceErr)
		}
		key := "task-recovery-profile-" + string(terminal.ID) + "-" + string(successorProfile.ProfileID) + "-" + request.IdempotencyKey
		if err := service.applyTaskCommand(ctx, feature, tracked, "tekroo.command.task.bind-work-profile", kernel.SchemaVersion, service.policyAuthority, successorProfile, profileEvidence, nil, key); err != nil {
			return InvocationStatus{}, fmt.Errorf("bind recovery profile: %w", err)
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
	workspace, workspaceFound := service.workspacesByID[owner.WorkspaceID]
	if !configured || !workspaceFound {
		return InvocationStatus{}, fmt.Errorf("resolve recovery profile and workspace: %w", organization.ErrInvalidFeature)
	}
	state, head, stateFound, err = service.Store.ReadAggregateHead(ctx, taskRef)
	if err != nil || !stateFound {
		return InvocationStatus{}, fmt.Errorf("refresh task head: %w", errors.Join(organization.ErrInvalidFeature, err))
	}
	tracked = &trackedTask{plan: planned, revision: state.Revision, last: head, profile: successorProfile, owner: owner}
	if err := service.rebindPlanningRecoveryAssignment(ctx, feature, tracked, profileConfig, owner); err != nil {
		return InvocationStatus{}, fmt.Errorf("rebind recovery assignment: %w", err)
	}
	if err := service.refreshTaskExecutionBinding(ctx, feature, tracked, profileConfig, workspace); err != nil {
		return InvocationStatus{}, fmt.Errorf("refresh recovery execution binding: %w", err)
	}
	budgetRevision, err := service.extendTaskTechnicalRetryBudget(ctx, feature, tracked, planned.Purpose, terminal.AttemptOrdinal+1)
	if err != nil || budgetRevision != budget.Revision {
		return InvocationStatus{}, fmt.Errorf("extend technical retry budget: %w", errors.Join(organization.ErrInvalidFeature, err))
	}
	if err := service.authorizeTaskInvocationWithConditionPolicy(ctx, feature, tracked, profileConfig, workspace, budgetRevision, planned.Purpose, terminal.AttemptOrdinal+1, &terminal, []kernel.Digest{recoveryCondition}, true, false); err != nil {
		return InvocationStatus{}, fmt.Errorf("authorize recovery invocation: %w", err)
	}

	updated, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: taskRef})
	if err != nil {
		return InvocationStatus{}, fmt.Errorf("load authorized recovery invocation: %w", err)
	}
	next, nextFound := latestTaskInvocation(updated.WorkInvocations, planned.ID)
	if !nextFound || next.ID == invocationID || next.AttemptOrdinal != terminal.AttemptOrdinal+1 {
		return InvocationStatus{}, fmt.Errorf("verify authorized recovery invocation: %w", application.ErrInvalidOperationalExecution)
	}
	status, statusFound, err := service.ReadInvocation(ctx, next.ID)
	if err != nil || !statusFound {
		return InvocationStatus{}, fmt.Errorf("read authorized recovery invocation: %w", errors.Join(application.ErrInvalidOperationalExecution, err))
	}
	return status, nil
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
