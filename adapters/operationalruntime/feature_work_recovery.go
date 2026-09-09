package operationalruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func (service *ProductionService) reconcilePlannedFeatureWork(ctx context.Context) error {
	features, err := service.Store.ListFeatures(ctx, []organization.FeatureStatus{organization.FeaturePlanned, organization.FeatureApproved}, 1000)
	if err != nil {
		return err
	}
	for _, feature := range features {
		if feature.Plan == nil {
			return organization.ErrInvalidFeature
		}
		if err := service.reconcileFeaturePlan(ctx, feature, *feature.Plan); err != nil {
			return fmt.Errorf("feature %s: %w", feature.ID, err)
		}
	}
	return nil
}

func (service *ProductionService) reconcileFeaturePlan(ctx context.Context, feature organization.FeatureRequest, plan organization.FeaturePlan) error {
	planBytes, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	planDigest := digestBytes(planBytes)
	evidenceID := deterministicOperationalUUID("feature-plan-evidence", string(feature.ID), fmt.Sprint(plan.Version), string(planDigest))
	evidence := []kernel.EvidenceRef{{EvidenceID: evidenceID, SHA256: planDigest}}
	budgetRef := kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: feature.BudgetAccountID}

	states := make(map[kernel.UUIDv7]kernel.AggregateState, len(plan.Tasks))
	heads := make(map[kernel.UUIDv7]kernel.UUIDv7, len(plan.Tasks))
	invocations := make(map[kernel.UUIDv7]kernel.WorkInvocation, len(plan.Tasks))
	var snapshot kernel.Snapshot
	for _, item := range plan.Tasks {
		state, head, found, err := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: item.ID})
		if err != nil {
			return fmt.Errorf("read task %s head: %w", item.ID, err)
		}
		if !found {
			return organization.ErrInvalidFeature
		}
		states[item.ID], heads[item.ID] = state, head
		loaded, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: item.ID}})
		if err != nil {
			return fmt.Errorf("load task %s decision state: %w", item.ID, err)
		}
		snapshot = loaded
		if latest, found := latestTaskInvocation(loaded.WorkInvocations, item.ID); found {
			invocations[item.ID] = latest
		}
	}
	budget, found := snapshot.WorkBudgetAccounts[budgetRef]
	if !found || !budget.Valid() {
		return errors.New("feature work budget is missing")
	}
	deadlinesExtended, err := service.reconcileFeatureProfileDeadlines(ctx, feature, plan, states, heads, invocations, snapshot, budget)
	if err != nil {
		return fmt.Errorf("reconcile profile deadlines: %w", err)
	}
	if deadlinesExtended {
		return nil
	}
	revalidationAuthorized, err := service.reconcileValidationRounds(ctx, feature, plan, states, heads, invocations, snapshot, budget.Revision, evidence)
	if err != nil {
		return fmt.Errorf("reconcile validation rounds: %w", err)
	}
	if revalidationAuthorized {
		return nil
	}
	completed, err := service.reconcileTaskCompletions(ctx, feature, plan, states, heads, invocations, snapshot)
	if err != nil {
		return fmt.Errorf("reconcile task completions: %w", err)
	}
	if completed {
		return nil
	}
	allCompleted := true
	for _, item := range plan.Tasks {
		if states[item.ID].Phase != kernel.PhaseCompleted {
			allCompleted = false
			break
		}
	}
	if allCompleted {
		return service.recordFeatureAcceptanceRecommendation(ctx, feature, plan, invocations)
	}
	if service.newInvocationAdmissionBlocked() {
		return nil
	}

	for _, item := range plan.Tasks {
		state := states[item.ID]
		if state.Condition != kernel.ConditionRunnable {
			continue
		}
		if state.Phase == kernel.PhaseActive {
			if _, found := invocations[item.ID]; found {
				// A terminal failure does not justify another model call by itself.
				// Operator recovery or a repaired candidate must provide explicit
				// changed-condition evidence before another invocation is authorized.
				continue
			}
		}
		if state.Phase != kernel.PhasePlanned && state.Phase != kernel.PhaseReady && state.Phase != kernel.PhaseActive {
			continue
		}
		dependencyEvents := make([]kernel.UUIDv7, 0, len(item.DependsOn))
		conditionDigests := make([]kernel.Digest, 0, len(item.DependsOn))
		ready := true
		bindDependencyOutputs := item.Purpose == kernel.PurposeValidation || item.Purpose == kernel.PurposeReview
		validationTargets := make(map[kernel.UUIDv7]struct{}, len(item.Validates))
		for _, targetID := range item.Validates {
			validationTargets[targetID] = struct{}{}
		}
		for _, dependencyID := range item.DependsOn {
			if _, validationTarget := validationTargets[dependencyID]; validationTarget {
				invocation, succeeded := invocations[dependencyID]
				if !plannedDependencyReady(true, states[dependencyID], invocation, succeeded) {
					ready = false
					break
				}
				dependencyEvents = append(dependencyEvents, invocation.LastEventID)
				conditionDigests = append(conditionDigests, *invocation.OutputDigest)
				continue
			}
			dependency := states[dependencyID]
			if !plannedDependencyReady(false, dependency, kernel.WorkInvocation{}, false) {
				ready = false
				break
			}
			dependencyEvents = append(dependencyEvents, heads[dependencyID])
			if bindDependencyOutputs {
				invocation, succeeded := invocations[dependencyID]
				if !succeeded || invocation.State != kernel.InvocationSucceeded || invocation.OutputDigest == nil {
					ready = false
					break
				}
				conditionDigests = append(conditionDigests, *invocation.OutputDigest)
			}
		}
		if !ready {
			continue
		}
		sort.Slice(dependencyEvents, func(left, right int) bool { return dependencyEvents[left] < dependencyEvents[right] })
		profileConfig, found := service.profilesByModel[item.ModelProfile]
		if !found || !profileConfig.qualifiedFor(item.DecisionRoute, workKindForPurpose(item.Purpose, item.Risk), service.clock.Now().UTC()) {
			return organization.ErrInvalidFeature
		}
		owner, active, err := service.RoleHost.Status(ctx, item.Owner)
		if err != nil {
			return err
		}
		if roleNeedsStart(active, owner.Status) {
			owner, err = service.RoleHost.EnsureStarted(ctx, item.Owner)
		}
		if err != nil || owner.ModelProfile != item.ModelProfile {
			return errors.Join(organization.ErrRoleNotRunning, err)
		}
		if owner.Status != organization.RoleIdle {
			// A long-running actor executes one assigned work item at a time.
			// Leave later ready DAG nodes pending until its current invocation
			// returns to idle; starting the role again cannot make it available.
			continue
		}
		if actorHasActiveInvocation(snapshot.WorkInvocations, owner.ActorFQN, owner.Execution) {
			// Role status describes the long-running role process, not whether one
			// of its model invocations is still in flight. Admit at most one active
			// work item per FQN; other role instances remain independently usable.
			continue
		}
		workspace, found := service.workspacesByID[owner.WorkspaceID]
		if !found {
			return organization.ErrInvalidFeature
		}
		if err := service.registerExecution(ctx, owner, profileConfig); err != nil {
			return err
		}
		profileSnapshot, found := snapshot.WorkProfiles[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: item.ID}]
		if !found || !profileSnapshot.Valid() {
			return organization.ErrInvalidFeature
		}
		tracked := &trackedTask{plan: item, revision: state.Revision, last: heads[item.ID], profile: profileSnapshot.Profile, owner: owner}
		taskEvidence := append([]kernel.EvidenceRef(nil), evidence...)
		if item.Purpose == kernel.PurposeImplementation || item.Purpose == kernel.PurposeRepair {
			var workspaceEvidence kernel.EvidenceRef
			workspace, workspaceEvidence, err = service.prepareImplementationWorkspace(ctx, feature, item, owner, plan)
			if err != nil {
				return err
			}
			taskEvidence = append(taskEvidence, workspaceEvidence)
		} else if len(item.Validates) > 0 || item.Purpose == kernel.PurposePromotion {
			workspace, taskEvidence, err = service.prepareCandidateConsumerWorkspace(ctx, feature, item, owner, plan, invocations, evidence)
			if err != nil {
				return err
			}
		}
		sort.Slice(taskEvidence, func(left, right int) bool { return taskEvidence[left].EvidenceID < taskEvidence[right].EvidenceID })
		if err := service.activateTask(ctx, feature, tracked, profileConfig, workspace, budget.Revision, dependencyEvents, taskEvidence, evidenceID, conditionDigests); err != nil {
			return err
		}
		// Activation mutates task, budget, actor, and invocation state. Reload
		// those authoritative snapshots before admitting another ready node.
		return nil
	}
	return nil
}

func roleNeedsStart(found bool, status organization.RoleStatus) bool {
	return !found || status == organization.RoleStopped || status == organization.RoleFailed
}

func actorHasActiveInvocation(invocations map[kernel.AggregateRef]kernel.WorkInvocation, actor kernel.ActorFQN, execution kernel.ExecutionTuple) bool {
	for _, invocation := range invocations {
		// A role restart fences its prior execution. Work authorized to that old
		// execution can no longer run or report a valid result, so it must not
		// occupy the restarted actor indefinitely.
		if invocation.ActorFQN == actor && invocation.Execution == execution && !invocation.State.Terminal() {
			return true
		}
	}
	return false
}

func (service *ProductionService) reconcileFeatureProfileDeadlines(ctx context.Context, feature organization.FeatureRequest, plan organization.FeaturePlan, states map[kernel.UUIDv7]kernel.AggregateState, heads map[kernel.UUIDv7]kernel.UUIDv7, invocations map[kernel.UUIDv7]kernel.WorkInvocation, snapshot kernel.Snapshot, budget kernel.WorkBudgetAccount) (bool, error) {
	now := service.clock.Now().UTC()
	if !budget.DeadlineAt.After(now) {
		return false, nil
	}
	evidenceIDs, err := service.featureDeadlineExtensionEvidenceIDs(ctx, plan, snapshot, budget)
	if err != nil {
		return false, err
	}
	changed := false
	for _, item := range plan.Tasks {
		state, stateFound := states[item.ID]
		if !stateFound || state.Phase == kernel.PhaseCompleted {
			continue
		}
		// Once a task has an invocation, that invocation's recovery path owns any
		// deadline extension. Advancing the task profile independently would make
		// an in-flight or completed result appear stale during reconciliation.
		if _, found := invocations[item.ID]; found {
			continue
		}
		taskRef := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: item.ID}
		profileSnapshot, found := snapshot.WorkProfiles[taskRef]
		if !found || !profileSnapshot.Valid() {
			return false, organization.ErrInvalidFeature
		}
		current := profileSnapshot.Profile
		if current.Budgets.DeadlineAt.After(now) || !current.Budgets.DeadlineAt.Before(budget.DeadlineAt) {
			continue
		}
		if len(evidenceIDs) == 0 {
			return false, organization.ErrInvalidFeature
		}
		condition := digestBytes([]byte("feature-deadline-profile\x00" + string(feature.ID) + "\x00" + string(item.ID) + "\x00" + string(budget.PolicyDigest) + "\x00" + budget.DeadlineAt.Format(time.RFC3339Nano)))
		successor, alreadyBound, err := planningRecoveryProfile(current, current.Binding(), service.planning, condition, budget.DeadlineAt, evidenceIDs)
		if err != nil {
			return false, err
		}
		if alreadyBound {
			continue
		}
		evidence, err := evidenceRefsForIDs(snapshot, successor.ClassificationEvidenceIDs)
		if err != nil {
			return false, err
		}
		tracked := &trackedTask{plan: item, revision: state.Revision, last: heads[item.ID], profile: successor}
		key := "feature-deadline-profile-" + string(successor.ProfileID)
		if err := service.applyTaskCommand(ctx, feature, tracked, "tekroo.command.task.bind-work-profile", kernel.SchemaVersion, service.policyAuthority, successor, evidence, nil, key); err != nil {
			return false, err
		}
		changed = true
	}
	return changed, nil
}

func (service *ProductionService) featureDeadlineExtensionEvidenceIDs(ctx context.Context, plan organization.FeaturePlan, snapshot kernel.Snapshot, budget kernel.WorkBudgetAccount) ([]kernel.UUIDv7, error) {
	evidenceIDs := profileDeadlineExtensionEvidenceIDs(plan, snapshot, budget.DeadlineAt)
	if len(evidenceIDs) == 0 {
		event, found, err := service.Store.ReadEvent(ctx, budget.LastEventID)
		if err != nil {
			return nil, err
		}
		var payload struct {
			DeadlineAt  time.Time       `json:"deadline_at"`
			EvidenceIDs []kernel.UUIDv7 `json:"evidence_ids"`
		}
		if found && event.EventType == "tekroo.event.work-budget.amended" && json.Unmarshal(event.Payload, &payload) == nil && payload.DeadlineAt.Equal(budget.DeadlineAt) {
			evidenceIDs = append(evidenceIDs, payload.EvidenceIDs...)
		}
	}
	sort.Slice(evidenceIDs, func(left, right int) bool { return evidenceIDs[left] < evidenceIDs[right] })
	return uniqueUUIDs(evidenceIDs), nil
}

func profileDeadlineExtensionEvidenceIDs(plan organization.FeaturePlan, snapshot kernel.Snapshot, deadline time.Time) []kernel.UUIDv7 {
	var evidenceIDs []kernel.UUIDv7
	for _, item := range plan.Tasks {
		profile, found := snapshot.WorkProfiles[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: item.ID}]
		if !found || !profile.Valid() || profile.Profile.SupersedesProfileID == nil || !profile.Profile.Budgets.DeadlineAt.Equal(deadline) {
			continue
		}
		evidenceIDs = append(evidenceIDs, profile.Profile.ClassificationEvidenceIDs...)
	}
	sort.Slice(evidenceIDs, func(left, right int) bool { return evidenceIDs[left] < evidenceIDs[right] })
	return uniqueUUIDs(evidenceIDs)
}

func (service *ProductionService) resumeInvocationlessActiveTask(ctx context.Context, feature organization.FeatureRequest, item organization.PlannedTask, state kernel.AggregateState, head kernel.UUIDv7, snapshot kernel.Snapshot, budget kernel.WorkBudgetAccount) error {
	taskRef := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: item.ID}
	profileSnapshot, profileFound := snapshot.WorkProfiles[taskRef]
	assignment, assignmentFound := snapshot.QualifiedAssignments[taskRef]
	binding, bindingFound := snapshot.TaskWorkBudgets[taskRef]
	scope, scopeFound := snapshot.TaskOperationalScopes[taskRef]
	if state.Phase != kernel.PhaseActive || state.Condition != kernel.ConditionRunnable || !profileFound || !profileSnapshot.Valid() || !assignmentFound || !assignment.Valid() || !bindingFound || !binding.Valid() || binding.BudgetAccountID != feature.BudgetAccountID || !scopeFound || !scope.Valid() || scope.TaskID != item.ID || !budget.Valid() || !budget.DeadlineAt.After(service.clock.Now().UTC()) {
		return organization.ErrInvalidFeature
	}
	owner, active, err := service.RoleHost.Status(ctx, item.Owner)
	if err != nil {
		return err
	}
	if !active || owner.Status != organization.RoleIdle {
		owner, err = service.RoleHost.EnsureStarted(ctx, item.Owner)
	}
	if err != nil || owner.Status != organization.RoleIdle {
		return errors.Join(organization.ErrRoleNotRunning, err)
	}
	profileConfig, configured := service.profilesByModel[item.ModelProfile]
	workspace, workspaceErr := service.workspaceForExistingTask(ctx, item, owner, snapshot)
	if !configured || !profileConfig.qualifiedFor(item.DecisionRoute, workKindForPurpose(item.Purpose, item.Risk), service.clock.Now().UTC()) || workspaceErr != nil || owner.ModelProfile != item.ModelProfile {
		return organization.ErrInvalidFeature
	}
	if err := service.registerExecution(ctx, owner, profileConfig); err != nil {
		return err
	}
	tracked := &trackedTask{plan: item, revision: state.Revision, last: head, profile: profileSnapshot.Profile, owner: owner}
	if err := service.rebindPlanningRecoveryAssignment(ctx, feature, tracked, profileConfig, owner); err != nil {
		return err
	}
	if err := service.refreshTaskExecutionBinding(ctx, feature, tracked, profileConfig, workspace); err != nil {
		return err
	}
	return service.authorizeTaskInvocationWithCondition(ctx, feature, tracked, profileConfig, workspace, budget.Revision, item.Purpose, 1, nil, nil)
}

// plannedDependencyReady exposes an unaccepted candidate only to a task that
// is explicitly assigned to validate that candidate. Every other DAG edge
// waits for the dependency to complete, which means its required independent
// validation has passed. This rule is independent of role and work domain.
func plannedDependencyReady(validationTarget bool, dependency kernel.AggregateState, invocation kernel.WorkInvocation, found bool) bool {
	if validationTarget {
		return found && invocation.State == kernel.InvocationSucceeded && invocation.OutputDigest != nil
	}
	return dependency.Phase == kernel.PhaseCompleted
}

func (service *ProductionService) recordFeatureAcceptanceRecommendation(ctx context.Context, feature organization.FeatureRequest, plan organization.FeaturePlan, invocations map[kernel.UUIDv7]kernel.WorkInvocation) error {
	var acceptanceTask *organization.PlannedTask
	for index := range plan.Tasks {
		if plan.Tasks[index].Purpose != kernel.PurposePromotion {
			continue
		}
		if acceptanceTask != nil {
			return organization.ErrInvalidFeature
		}
		acceptanceTask = &plan.Tasks[index]
	}
	if acceptanceTask == nil {
		return nil
	}
	invocation, found := invocations[acceptanceTask.ID]
	if !found || invocation.State != kernel.InvocationSucceeded || invocation.OutputDigest == nil || invocation.ActorFQN != feature.ProductOwnerActor {
		return organization.ErrInvalidFeature
	}
	output, err := service.Runtime.ReadExecutionOutput(ctx, *invocation.OutputDigest)
	if err != nil {
		return err
	}
	result, err := parseStructuredValidationResult(output)
	if err == nil {
		decision, loadErr := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: acceptanceTask.ID}})
		if loadErr != nil {
			return loadErr
		}
		err = service.validateCandidateResult(ctx, *acceptanceTask, decision, result)
	}
	if err != nil || result.Outcome != "PASS" {
		return errors.Join(organization.ErrInvalidFeature, err)
	}
	featureValidatorID, featureValidatorFound, err := service.assembledValidationCandidateTask(ctx, feature, *acceptanceTask, plan)
	if err != nil {
		return err
	}
	for _, task := range plan.Tasks {
		if len(task.Validates) == 0 {
			continue
		}
		validator, present := invocations[task.ID]
		if !present || validator.State != kernel.InvocationSucceeded || validator.OutputDigest == nil {
			return organization.ErrInvalidFeature
		}
		validatorOutput, readErr := service.Runtime.ReadExecutionOutput(ctx, *validator.OutputDigest)
		validatorResult, parseErr := parseStructuredValidationResult(validatorOutput)
		candidateMismatch := task.ID == featureValidatorID && validatorResult.CandidateID != result.CandidateID
		if readErr != nil || parseErr != nil || validatorResult.Outcome != "PASS" || candidateMismatch {
			return errors.Join(organization.ErrInvalidFeature, readErr, parseErr)
		}
	}
	if !featureValidatorFound {
		// Legacy plans had no whole-feature validator. Their validators must
		// still have used the acceptance candidate exactly.
		for _, task := range plan.Tasks {
			if len(task.Validates) == 0 {
				continue
			}
			validator := invocations[task.ID]
			validatorOutput, readErr := service.Runtime.ReadExecutionOutput(ctx, *validator.OutputDigest)
			validatorResult, parseErr := parseStructuredValidationResult(validatorOutput)
			if readErr != nil || parseErr != nil || validatorResult.CandidateID != result.CandidateID {
				return errors.Join(organization.ErrInvalidFeature, readErr, parseErr)
			}
		}
	}
	stories := make([]kernel.UUIDv7, len(plan.Stories))
	for index := range plan.Stories {
		stories[index] = plan.Stories[index].ID
	}
	sort.Slice(stories, func(left, right int) bool { return stories[left] < stories[right] })
	_, err = service.Features.RecordAcceptanceRecommendation(ctx, feature.ID, feature.Revision, organization.FeatureAcceptance{
		RecommendedBy: feature.ProductOwnerActor, RecommendationRun: invocation.Execution,
		Recommendation: "PASS", RecommendationHash: *invocation.OutputDigest,
		StoryIDs: stories, RecordedAt: service.clock.Now().UTC(),
	})
	return err
}

func (service *ProductionService) reconcileValidationRounds(ctx context.Context, feature organization.FeatureRequest, plan organization.FeaturePlan, states map[kernel.UUIDv7]kernel.AggregateState, heads map[kernel.UUIDv7]kernel.UUIDv7, invocations map[kernel.UUIDv7]kernel.WorkInvocation, snapshot kernel.Snapshot, budgetRevision uint64, evidence []kernel.EvidenceRef) (bool, error) {
	for _, validator := range plan.Tasks {
		if len(validator.Validates) == 0 || states[validator.ID].Phase != kernel.PhaseActive {
			continue
		}
		latest, found := invocations[validator.ID]
		if !found || latest.State != kernel.InvocationSucceeded {
			continue
		}
		validationTargets := make(map[kernel.UUIDv7]struct{}, len(validator.Validates))
		for _, targetID := range validator.Validates {
			validationTargets[targetID] = struct{}{}
		}
		conditionDigests := make([]kernel.Digest, 0, len(validator.DependsOn))
		ready := true
		for _, dependencyID := range validator.DependsOn {
			if _, validates := validationTargets[dependencyID]; !validates {
				dependency := states[dependencyID]
				if !plannedDependencyReady(false, dependency, kernel.WorkInvocation{}, false) {
					ready = false
					break
				}
			}
			candidate, present := invocations[dependencyID]
			if !present || candidate.State != kernel.InvocationSucceeded || candidate.OutputDigest == nil {
				ready = false
				break
			}
			conditionDigests = append(conditionDigests, *candidate.OutputDigest)
		}
		if !ready {
			continue
		}
		profileSnapshot, profileFound := snapshot.WorkProfiles[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: validator.ID}]
		criteria, marshalErr := json.Marshal(validator.AcceptanceCriteria)
		if marshalErr != nil || !profileFound || !profileSnapshot.Valid() {
			return false, errors.Join(organization.ErrInvalidFeature, marshalErr)
		}
		expectedCondition, digestErr := taskInvocationConditionDigest(profileSnapshot.Profile.ProfileDigest, digestBytes(criteria), conditionDigests)
		if digestErr != nil {
			return false, digestErr
		}
		// An operator-authorized revalidation also binds its recovery condition.
		// Use the same exact-lineage check as completion reconciliation so that a
		// valid successor is not mistaken for a still-stale validator and charged
		// as another attempt.
		if validatorConditionMatches(snapshot, validator.ID, latest, profileSnapshot.Profile.ProfileDigest, digestBytes(criteria), conditionDigests, expectedCondition) {
			continue
		}
		nextAttempt := latest.AttemptOrdinal + 1
		technicalExtension := nextAttempt > uint64(validator.AttemptLimit)
		profileConfig, configured := service.profilesByModel[validator.ModelProfile]
		owner, ownerErr := service.StartRole(ctx, validator.Owner)
		if !configured || !profileConfig.qualifiedFor(validator.DecisionRoute, workKindForPurpose(validator.Purpose, validator.Risk), service.clock.Now().UTC()) || ownerErr != nil || owner.Status != organization.RoleIdle {
			return false, errors.Join(organization.ErrRoleNotRunning, ownerErr)
		}
		tracked := &trackedTask{plan: validator, revision: states[validator.ID].Revision, last: heads[validator.ID], profile: profileSnapshot.Profile, owner: owner}
		workspace, candidateEvidence, workspaceErr := service.prepareCandidateConsumerWorkspace(ctx, feature, validator, owner, plan, invocations, evidence)
		if workspaceErr != nil {
			return false, workspaceErr
		}
		if err := service.rebindTaskCandidateWorkspace(ctx, feature, tracked, workspace, candidateEvidence); err != nil {
			return false, err
		}
		invocationBudgetRevision, budgetErr := service.extendTaskTechnicalRetryBudget(ctx, feature, tracked, validator.Purpose, nextAttempt)
		if budgetErr != nil || invocationBudgetRevision != budgetRevision {
			return false, errors.Join(organization.ErrInvalidFeature, budgetErr)
		}
		// A changed dependency set is a new candidate, not another attempt to
		// validate the old candidate. Permit its successor ordinal even when the
		// prior candidate consumed the planned validation-attempt allowance.
		if err := service.authorizeTaskInvocationWithConditionPolicy(ctx, feature, tracked, profileConfig, workspace, invocationBudgetRevision, validator.Purpose, nextAttempt, &latest, conditionDigests, technicalExtension, false); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

func latestTaskInvocation(values map[kernel.AggregateRef]kernel.WorkInvocation, taskID kernel.UUIDv7) (kernel.WorkInvocation, bool) {
	var latest kernel.WorkInvocation
	found := false
	for _, candidate := range values {
		if candidate.TaskID != taskID {
			continue
		}
		if !found || candidate.GlobalDebitOrdinal > latest.GlobalDebitOrdinal || candidate.GlobalDebitOrdinal == latest.GlobalDebitOrdinal && candidate.Revision > latest.Revision {
			latest, found = candidate.Clone(), true
		}
	}
	return latest, found
}
