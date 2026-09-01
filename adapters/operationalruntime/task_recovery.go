package operationalruntime

import (
	"context"
	"errors"

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
		return InvocationStatus{}, errors.Join(application.ErrInvalidOperationalExecution, err)
	}
	terminalContext, err := service.Store.LoadOperationalExecution(ctx, invocationID)
	if err != nil || terminalContext.Invocation.Revision != terminalStatus.Revision || !recoverablePlanningTerminal(terminalContext.Invocation) {
		return InvocationStatus{}, errors.Join(application.ErrInvalidOperationalExecution, err)
	}
	terminal := terminalContext.Invocation

	feature, planned, found, err := service.plannedFeatureTask(ctx, terminal.TaskID)
	if err != nil || !found || planned.Purpose != kernel.PurposeImplementation && planned.Purpose != kernel.PurposeRepair {
		return InvocationStatus{}, errors.Join(organization.ErrInvalidFeature, err)
	}
	taskRef := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: planned.ID}
	state, head, stateFound, err := service.Store.ReadAggregateHead(ctx, taskRef)
	if err != nil || !stateFound || state.Phase != kernel.PhaseActive || state.Condition != kernel.ConditionRunnable {
		return InvocationStatus{}, errors.Join(organization.ErrInvalidFeature, err)
	}
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: taskRef})
	if err != nil {
		return InvocationStatus{}, err
	}
	latest, latestFound := latestTaskInvocation(snapshot.WorkInvocations, planned.ID)
	profileSnapshot, profileFound := snapshot.WorkProfiles[taskRef]
	budgetRef := kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: feature.BudgetAccountID}
	budget, budgetFound := snapshot.WorkBudgetAccounts[budgetRef]
	recoveryCondition, err := planningRecoveryConditionDigest(invocationID, request)
	now := service.clock.Now().UTC()
	deadline := request.DeadlineAt.UTC()
	if err != nil || !latestFound || latest.ID != terminal.ID || !profileFound || !profileSnapshot.Valid() || !budgetFound || !budget.Valid() || !deadline.After(now) || !deadline.After(terminal.DeadlineAt) || deadline.Before(profileSnapshot.Profile.Budgets.DeadlineAt) || deadline.After(now.Add(service.planningDeadline)) {
		return InvocationStatus{}, errors.Join(organization.ErrInvalidFeature, err)
	}

	evidenceIDs := make([]kernel.UUIDv7, len(request.EvidenceRefs))
	for index, evidence := range request.EvidenceRefs {
		evidenceIDs[index] = evidence.EvidenceID
	}
	registered, err := evidenceRefsForIDs(snapshot, evidenceIDs)
	if err != nil || !sameEvidenceSet(registered, request.EvidenceRefs) {
		return InvocationStatus{}, errors.Join(organization.ErrInvalidFeature, err)
	}
	profileEvidenceIDs := recoveryProfileEvidenceIDs(terminal, evidenceIDs)
	if _, err := evidenceRefsForIDs(snapshot, profileEvidenceIDs); err != nil {
		return InvocationStatus{}, err
	}
	successorProfile, profileBound, err := planningRecoveryProfile(profileSnapshot.Profile, terminal.WorkProfile, service.planning, recoveryCondition, deadline, profileEvidenceIDs)
	if err != nil {
		return InvocationStatus{}, err
	}
	budget, err = service.amendPlanningRecoveryBudget(ctx, principal, feature, terminal, budget, request, recoveryCondition, registered)
	if err != nil {
		return InvocationStatus{}, err
	}
	tracked := &trackedTask{plan: planned, revision: state.Revision, last: head, profile: successorProfile}
	if !profileBound {
		profileEvidence, evidenceErr := evidenceRefsForIDs(snapshot, successorProfile.ClassificationEvidenceIDs)
		if evidenceErr != nil {
			return InvocationStatus{}, evidenceErr
		}
		key := "task-recovery-profile-" + string(terminal.ID) + "-" + request.IdempotencyKey
		if err := service.applyTaskCommand(ctx, feature, tracked, "tekroo.command.task.bind-work-profile", kernel.SchemaVersion, service.policyAuthority, successorProfile, profileEvidence, nil, key); err != nil {
			return InvocationStatus{}, err
		}
	}

	owner, active, err := service.RoleHost.Status(ctx, planned.Owner)
	if err != nil {
		return InvocationStatus{}, err
	}
	if active {
		if owner.Status != organization.RoleIdle {
			return InvocationStatus{}, organization.ErrRoleNotRunning
		}
		if owner.Execution == terminal.Execution {
			owner, err = service.RestartRole(ctx, planned.Owner)
		}
	} else {
		owner, err = service.StartRole(ctx, planned.Owner)
	}
	if err != nil || owner.Status != organization.RoleIdle || owner.Execution == terminal.Execution {
		return InvocationStatus{}, errors.Join(organization.ErrRoleNotRunning, err)
	}
	profileConfig, configured := service.profilesByModel[owner.ModelProfile]
	workspace, workspaceFound := service.workspacesByID[owner.WorkspaceID]
	if !configured || !workspaceFound {
		return InvocationStatus{}, organization.ErrInvalidFeature
	}
	state, head, stateFound, err = service.Store.ReadAggregateHead(ctx, taskRef)
	if err != nil || !stateFound {
		return InvocationStatus{}, errors.Join(organization.ErrInvalidFeature, err)
	}
	tracked = &trackedTask{plan: planned, revision: state.Revision, last: head, profile: successorProfile, owner: owner}
	if err := service.rebindPlanningRecoveryAssignment(ctx, feature, tracked, profileConfig, owner); err != nil {
		return InvocationStatus{}, err
	}
	if err := service.refreshTaskExecutionBinding(ctx, feature, tracked, profileConfig, workspace); err != nil {
		return InvocationStatus{}, err
	}
	budgetRevision, err := service.extendTaskTechnicalRetryBudget(ctx, feature, tracked, planned.Purpose, terminal.AttemptOrdinal+1)
	if err != nil || budgetRevision != budget.Revision {
		return InvocationStatus{}, errors.Join(organization.ErrInvalidFeature, err)
	}
	if err := service.authorizeTaskInvocationWithConditionPolicy(ctx, feature, tracked, profileConfig, workspace, budgetRevision, planned.Purpose, terminal.AttemptOrdinal+1, &terminal, []kernel.Digest{recoveryCondition}, true, false); err != nil {
		return InvocationStatus{}, err
	}

	updated, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: taskRef})
	if err != nil {
		return InvocationStatus{}, err
	}
	next, nextFound := latestTaskInvocation(updated.WorkInvocations, planned.ID)
	if !nextFound || next.ID == invocationID || next.AttemptOrdinal != terminal.AttemptOrdinal+1 {
		return InvocationStatus{}, application.ErrInvalidOperationalExecution
	}
	status, statusFound, err := service.ReadInvocation(ctx, next.ID)
	if err != nil || !statusFound {
		return InvocationStatus{}, errors.Join(application.ErrInvalidOperationalExecution, err)
	}
	return status, nil
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
