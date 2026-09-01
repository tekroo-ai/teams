package operationalruntime

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// PlanningRecoveryRequest is an explicit operator decision to continue one
// cancelled or timed-out feature-planning invocation under a new condition,
// execution, and finite deadline.
type PlanningRecoveryRequest struct {
	ExpectedRevision uint64               `json:"expected_revision"`
	Reason           string               `json:"reason"`
	EvidenceRefs     []kernel.EvidenceRef `json:"evidence_refs"`
	DeadlineAt       time.Time            `json:"deadline_at"`
	IdempotencyKey   string               `json:"idempotency_key"`
}

func (request PlanningRecoveryRequest) Valid() bool {
	if request.ExpectedRevision == 0 || request.Reason == "" || len(request.Reason) > 4096 || len(request.EvidenceRefs) == 0 || len(request.EvidenceRefs) > 64 || request.DeadlineAt.IsZero() || request.IdempotencyKey == "" || len(request.IdempotencyKey) > 256 {
		return false
	}
	seen := make(map[kernel.UUIDv7]struct{}, len(request.EvidenceRefs))
	for _, evidence := range request.EvidenceRefs {
		if !evidence.EvidenceID.Valid() || !evidence.SHA256.Valid() {
			return false
		}
		if _, duplicate := seen[evidence.EvidenceID]; duplicate {
			return false
		}
		seen[evidence.EvidenceID] = struct{}{}
	}
	return true
}

func (service *ProductionService) RetryCancelledFeaturePlanning(ctx context.Context, principal kernel.PrincipalRef, invocationID kernel.UUIDv7, request PlanningRecoveryRequest) (InvocationStatus, error) {
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

	feature, stage, found, err := service.planningFeatureForTask(ctx, terminal.TaskID)
	if err != nil || !found {
		return InvocationStatus{}, errors.Join(organization.ErrInvalidFeature, err)
	}
	task, state, head, latest, snapshot, err := service.ensureFeaturePlanningTask(ctx, feature, stage)
	if err != nil {
		return InvocationStatus{}, err
	}
	taskRef := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}
	profileSnapshot, profileFound := snapshot.WorkProfiles[taskRef]
	budgetRef := kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: feature.BudgetAccountID}
	budget, budgetFound := snapshot.WorkBudgetAccounts[budgetRef]
	recoveryCondition, err := planningRecoveryConditionDigest(invocationID, request)
	now := service.clock.Now().UTC()
	deadline := request.DeadlineAt.UTC()
	if err != nil || !profileFound || !profileSnapshot.Valid() || !budgetFound || !budget.Valid() || !deadline.After(now) || !deadline.After(terminal.DeadlineAt) || deadline.Before(profileSnapshot.Profile.Budgets.DeadlineAt) || deadline.After(now.Add(service.planningDeadline)) {
		return InvocationStatus{}, errors.Join(organization.ErrInvalidFeature, err)
	}

	evidenceIDs := make([]kernel.UUIDv7, len(request.EvidenceRefs))
	for index, evidence := range request.EvidenceRefs {
		evidenceIDs[index] = evidence.EvidenceID
	}
	sort.Slice(evidenceIDs, func(left, right int) bool { return evidenceIDs[left] < evidenceIDs[right] })
	registered, err := evidenceRefsForIDs(snapshot, evidenceIDs)
	if err != nil || !sameEvidenceSet(registered, request.EvidenceRefs) {
		return InvocationStatus{}, errors.Join(organization.ErrInvalidFeature, err)
	}
	successorProfile, profileBound, err := planningRecoveryProfile(profileSnapshot.Profile, terminal.WorkProfile, service.planning, recoveryCondition, deadline, evidenceIDs)
	if err != nil {
		return InvocationStatus{}, err
	}
	criteria, err := json.Marshal(task.AcceptanceCriteria)
	if err != nil {
		return InvocationStatus{}, err
	}
	expectedCondition, err := taskInvocationConditionDigest(successorProfile.ProfileDigest, digestBytes(criteria), []kernel.Digest{recoveryCondition})
	if err != nil {
		return InvocationStatus{}, err
	}
	if latest.ID != terminal.ID {
		if latest.AttemptOrdinal == terminal.AttemptOrdinal+1 && latest.ConditionDigest == expectedCondition {
			if status, currentFound, currentErr := service.ReadInvocation(ctx, latest.ID); currentErr == nil && currentFound {
				return status, nil
			}
		}
		return InvocationStatus{}, application.ErrInvalidOperationalExecution
	}
	budget, err = service.amendPlanningRecoveryBudget(ctx, principal, feature, terminal, budget, request, recoveryCondition, registered)
	if err != nil {
		return InvocationStatus{}, err
	}
	tracked := &trackedTask{plan: task, revision: state.Revision, last: head, profile: successorProfile}
	if !profileBound {
		profileEvidence, evidenceErr := evidenceRefsForIDs(snapshot, successorProfile.ClassificationEvidenceIDs)
		if evidenceErr != nil {
			return InvocationStatus{}, evidenceErr
		}
		key := "planning-recovery-profile-" + string(terminal.ID) + "-" + request.IdempotencyKey
		if err := service.applyTaskCommand(ctx, feature, tracked, "tekroo.command.task.bind-work-profile", kernel.SchemaVersion, service.policyAuthority, successorProfile, profileEvidence, nil, key); err != nil {
			return InvocationStatus{}, err
		}
	}

	owner, active, err := service.RoleHost.Status(ctx, task.Owner)
	if err != nil {
		return InvocationStatus{}, err
	}
	if active {
		if owner.Status != organization.RoleIdle {
			return InvocationStatus{}, organization.ErrRoleNotRunning
		}
		if owner.Execution == terminal.Execution {
			owner, err = service.RestartRole(ctx, task.Owner)
		}
	} else {
		owner, err = service.StartRole(ctx, task.Owner)
	}
	if err != nil || owner.Status != organization.RoleIdle || owner.Execution == terminal.Execution {
		return InvocationStatus{}, errors.Join(organization.ErrRoleNotRunning, err)
	}
	profileConfig, configured := service.profilesByModel[owner.ModelProfile]
	workspace, workspaceFound := service.workspacesByID[owner.WorkspaceID]
	if !configured || !workspaceFound {
		return InvocationStatus{}, organization.ErrInvalidFeature
	}
	state, head, found, err = service.Store.ReadAggregateHead(ctx, taskRef)
	if err != nil || !found {
		return InvocationStatus{}, errors.Join(organization.ErrInvalidFeature, err)
	}
	tracked = &trackedTask{plan: task, revision: state.Revision, last: head, profile: successorProfile, owner: owner}
	if err := service.rebindPlanningRecoveryAssignment(ctx, feature, tracked, profileConfig, owner); err != nil {
		return InvocationStatus{}, err
	}
	if err := service.refreshTaskExecutionBinding(ctx, feature, tracked, profileConfig, workspace); err != nil {
		return InvocationStatus{}, err
	}
	budgetRevision, err := service.extendTaskTechnicalRetryBudget(ctx, feature, tracked, task.Purpose, terminal.AttemptOrdinal+1)
	if err != nil {
		return InvocationStatus{}, err
	}
	if budgetRevision != budget.Revision {
		return InvocationStatus{}, organization.ErrInvalidFeature
	}
	if err := service.authorizeTaskInvocationWithConditionPolicy(ctx, feature, tracked, profileConfig, workspace, budgetRevision, task.Purpose, terminal.AttemptOrdinal+1, &terminal, []kernel.Digest{recoveryCondition}, true, false); err != nil {
		return InvocationStatus{}, err
	}

	updated, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: taskRef})
	if err != nil {
		return InvocationStatus{}, err
	}
	next, nextFound := latestTaskInvocation(updated.WorkInvocations, task.ID)
	if !nextFound || next.ID == invocationID || next.AttemptOrdinal != terminal.AttemptOrdinal+1 || next.ConditionDigest != expectedCondition {
		return InvocationStatus{}, application.ErrInvalidOperationalExecution
	}
	status, statusFound, err := service.ReadInvocation(ctx, next.ID)
	if err != nil || !statusFound {
		return InvocationStatus{}, errors.Join(application.ErrInvalidOperationalExecution, err)
	}
	return status, nil
}

func recoverablePlanningTerminal(invocation kernel.WorkInvocation) bool {
	if !invocation.Valid() {
		return false
	}
	switch invocation.State {
	case kernel.InvocationCancelled:
		return invocation.CancellationRequestedAt != nil
	case kernel.InvocationTimedOut:
		return true
	default:
		return false
	}
}

func planningRecoveryProfile(current kernel.WorkRiskProfile, prior kernel.WorkProfileBinding, planning ProductionPlanning, condition kernel.Digest, deadline time.Time, evidenceIDs []kernel.UUIDv7) (kernel.WorkRiskProfile, bool, error) {
	if !current.Valid() || !prior.Valid() || planning.PolicyRevision == 0 || !planning.ClassificationPolicyDigest.Valid() || !planning.PromotionPolicyDigest.Valid() || !planning.VerificationTopologyDigest.Valid() || !condition.Valid() || deadline.IsZero() {
		return kernel.WorkRiskProfile{}, false, organization.ErrInvalidFeature
	}
	targetID := deterministicOperationalUUID("planning-recovery-profile", string(prior.ProfileID), string(condition))
	targetRevision := prior.ProfileRevision + 1
	if current.Binding() != prior {
		if current.ProfileID != targetID || current.ProfileRevision != targetRevision || current.SupersedesProfileID == nil || *current.SupersedesProfileID != prior.ProfileID || current.ClassificationPolicyRevision != planning.PolicyRevision || current.ClassificationPolicyDigest != planning.ClassificationPolicyDigest || current.PromotionPolicyRevision != planning.PolicyRevision || current.PromotionPolicyDigest != planning.PromotionPolicyDigest || current.VerificationTopologyDigest != planning.VerificationTopologyDigest || !current.Budgets.DeadlineAt.Equal(deadline) || !containsEveryUUID(current.ClassificationEvidenceIDs, evidenceIDs) {
			return kernel.WorkRiskProfile{}, false, organization.ErrInvalidFeature
		}
		claimedDigest := current.ProfileDigest
		current.ProfileDigest = ""
		encoded, err := json.Marshal(current)
		if err != nil || digestBytes(encoded) != claimedDigest {
			return kernel.WorkRiskProfile{}, false, errors.Join(organization.ErrInvalidFeature, err)
		}
		current.ProfileDigest = claimedDigest
		return current, true, nil
	}
	next := current.Clone()
	next.ProfileID = targetID
	next.ProfileRevision = targetRevision
	next.Budgets.DeadlineAt = deadline
	next.ClassificationPolicyRevision = planning.PolicyRevision
	next.ClassificationPolicyDigest = planning.ClassificationPolicyDigest
	next.PromotionPolicyRevision = planning.PolicyRevision
	next.PromotionPolicyDigest = planning.PromotionPolicyDigest
	next.VerificationTopologyDigest = planning.VerificationTopologyDigest
	priorID := prior.ProfileID
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
		return kernel.WorkRiskProfile{}, false, organization.ErrInvalidFeature
	}
	return next, false, nil
}

func (service *ProductionService) amendPlanningRecoveryBudget(ctx context.Context, principal kernel.PrincipalRef, feature organization.FeatureRequest, terminal kernel.WorkInvocation, account kernel.WorkBudgetAccount, request PlanningRecoveryRequest, condition kernel.Digest, evidence []kernel.EvidenceRef) (kernel.WorkBudgetAccount, error) {
	if !account.Valid() || account.ID != feature.BudgetAccountID || account.LifecycleEpoch != feature.LifecycleEpoch || terminal.BudgetAccountID != account.ID || !condition.Valid() {
		return kernel.WorkBudgetAccount{}, organization.ErrInvalidFeature
	}
	deadline := request.DeadlineAt.UTC()
	targetRevision := terminal.AdmissionPolicyRevision + 1
	targetDigest := digestBytes([]byte("planning-recovery-budget\x00" + string(terminal.AdmissionPolicyDigest) + "\x00" + string(condition) + "\x00" + deadline.Format(time.RFC3339Nano)))
	if targetRevision <= service.planning.PolicyRevision {
		targetRevision = service.planning.PolicyRevision
		targetDigest = service.planning.BudgetPolicyDigest
	}
	if account.PolicyRevision == targetRevision && account.PolicyDigest == targetDigest && account.DeadlineAt.Equal(deadline) {
		return account, nil
	}
	if account.PolicyRevision != terminal.AdmissionPolicyRevision || account.PolicyDigest != terminal.AdmissionPolicyDigest || !deadline.After(account.DeadlineAt) {
		return kernel.WorkBudgetAccount{}, organization.ErrInvalidFeature
	}
	evidenceIDs := make([]kernel.UUIDv7, len(evidence))
	for index := range evidence {
		evidenceIDs[index] = evidence[index].EvidenceID
	}
	payload := map[string]any{
		"budget_account_id": account.ID, "expected_budget_revision": account.Revision,
		"expected_lifecycle_epoch": account.LifecycleEpoch, "policy_revision": targetRevision,
		"policy_digest": targetDigest, "model_invocation_limit": account.ModelInvocationLimit,
		"purpose_limits": account.PurposeLimits, "deadline_at": deadline, "reason": request.Reason,
		"evidence_ids": evidenceIDs, "authority": principal,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return kernel.WorkBudgetAccount{}, err
	}
	key := "planning-recovery-budget-" + string(terminal.ID) + "-" + request.IdempotencyKey
	if _, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.work-budget.amend", kernel.OperationalSchemaVersion, kernel.AggregateWorkBudget, account.ID, principal, account.Revision, encoded, []kernel.DagParent{{ParentEventID: account.LastEventID, EdgeKind: kernel.EdgeCausal}}, evidence, key); err != nil {
		return kernel.WorkBudgetAccount{}, err
	}
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: account.Ref()})
	if err != nil {
		return kernel.WorkBudgetAccount{}, err
	}
	updated, found := snapshot.WorkBudgetAccounts[account.Ref()]
	if !found || !updated.Valid() || updated.PolicyRevision != targetRevision || updated.PolicyDigest != targetDigest || !updated.DeadlineAt.Equal(deadline) {
		return kernel.WorkBudgetAccount{}, organization.ErrInvalidFeature
	}
	return updated, nil
}

func (service *ProductionService) rebindPlanningRecoveryAssignment(ctx context.Context, feature organization.FeatureRequest, task *trackedTask, profile ProductionProfile, owner organization.RoleInstanceState) error {
	if task == nil || !task.profile.Valid() || owner.ActorFQN != task.plan.Owner || owner.Execution.Valid() == false {
		return organization.ErrInvalidFeature
	}
	taskRef := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.plan.ID}
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: taskRef})
	if err != nil {
		return err
	}
	assignment, found := snapshot.QualifiedAssignments[taskRef]
	profileSnapshot, profileFound := snapshot.WorkProfiles[taskRef]
	if !found || !assignment.Valid() || !profileFound || !profileSnapshot.Valid() || profileSnapshot.Profile.Binding() != task.profile.Binding() || assignment.TaskID != task.plan.ID || assignment.SelectedActorFQN != owner.ActorFQN || assignment.ModelProfileDigest != profile.ModelProfileDigest || assignment.RuntimeIdentityDigest != profile.RuntimeIdentityDigest {
		return organization.ErrInvalidFeature
	}
	if assignment.WorkProfile == task.profile.Binding() && assignment.SelectedExecution() == owner.Execution && assignment.SelectionPolicyRevision == service.planning.PolicyRevision && assignment.SelectionPolicyDigest == service.planning.SelectionPolicyDigest {
		return nil
	}
	evidence, err := evidenceRefsForIDs(snapshot, assignment.EvidenceIDs)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"assignment_id": assignment.AssignmentID, "task_id": task.plan.ID, "expected_task_revision": task.revision,
		"work_profile": task.profile.Binding(), "required_decision_route": assignment.RequiredDecisionRoute,
		"selected_decision_route": assignment.SelectedDecisionRoute, "selected_actor_fqn": owner.ActorFQN,
		"selected_execution_id": owner.Execution.ExecutionID, "selected_fencing_epoch": owner.Execution.FencingEpoch,
		"model_profile_digest": assignment.ModelProfileDigest, "runtime_identity_digest": assignment.RuntimeIdentityDigest,
		"qualification": assignment.Qualification, "selection_policy_revision": service.planning.PolicyRevision,
		"selection_policy_digest": service.planning.SelectionPolicyDigest, "hard_constraint_results": assignment.HardConstraintResults,
		"selection_reasons": assignment.SelectionReasons, "evidence_ids": assignment.EvidenceIDs,
	}
	key := "planning-recovery-assignment-" + string(owner.Execution.ExecutionID) + "-" + string(task.profile.ProfileID)
	return service.applyTaskCommand(ctx, feature, task, "tekroo.command.task.authorize-qualified-assignment", kernel.SchemaVersion, service.policyAuthority, payload, evidence, nil, key)
}

func uniqueUUIDs(values []kernel.UUIDv7) []kernel.UUIDv7 {
	if len(values) < 2 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

func containsEveryUUID(values, required []kernel.UUIDv7) bool {
	present := make(map[kernel.UUIDv7]struct{}, len(values))
	for _, value := range values {
		present[value] = struct{}{}
	}
	for _, value := range required {
		if _, found := present[value]; !found {
			return false
		}
	}
	return true
}

func (service *ProductionService) planningFeatureForTask(ctx context.Context, taskID kernel.UUIDv7) (organization.FeatureRequest, featurePlanningStage, bool, error) {
	statuses := []organization.FeatureStatus{organization.FeatureSubmitted, organization.FeatureReadyForPlanning, organization.FeatureSpecified}
	features, err := service.Store.ListFeatures(ctx, statuses, 1000)
	if err != nil {
		return organization.FeatureRequest{}, "", false, err
	}
	for _, feature := range features {
		stage, planning := planningStage(feature.Status)
		if planning && deterministicOperationalUUID("feature-planning-task", string(feature.ID), string(stage)) == taskID {
			return feature, stage, true, nil
		}
	}
	return organization.FeatureRequest{}, "", false, nil
}

func planningRecoveryConditionDigest(invocationID kernel.UUIDv7, request PlanningRecoveryRequest) (kernel.Digest, error) {
	evidence := append([]kernel.EvidenceRef(nil), request.EvidenceRefs...)
	sort.Slice(evidence, func(left, right int) bool { return evidence[left].EvidenceID < evidence[right].EvidenceID })
	encoded, err := json.Marshal(struct {
		InvocationID   kernel.UUIDv7        `json:"invocation_id"`
		Reason         string               `json:"reason"`
		EvidenceRefs   []kernel.EvidenceRef `json:"evidence_refs"`
		DeadlineAt     time.Time            `json:"deadline_at"`
		IdempotencyKey string               `json:"idempotency_key"`
	}{invocationID, request.Reason, evidence, request.DeadlineAt.UTC(), request.IdempotencyKey})
	if err != nil {
		return "", err
	}
	return digestBytes(encoded), nil
}

func sameEvidenceSet(left, right []kernel.EvidenceRef) bool {
	if len(left) != len(right) {
		return false
	}
	want := make(map[kernel.UUIDv7]kernel.Digest, len(right))
	for _, evidence := range right {
		want[evidence.EvidenceID] = evidence.SHA256
	}
	for _, evidence := range left {
		if want[evidence.EvidenceID] != evidence.SHA256 {
			return false
		}
	}
	return true
}
