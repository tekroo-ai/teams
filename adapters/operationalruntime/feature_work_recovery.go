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
	glitchRetried, err := service.reconcileGlitchTerminatedTasks(ctx, feature, plan, states, invocations)
	if err != nil {
		return fmt.Errorf("reconcile glitch terminations: %w", err)
	}
	if glitchRetried {
		return nil
	}
	completed, err := service.reconcileTaskCompletions(ctx, feature, plan, states, heads, invocations, snapshot)
	if err != nil {
		return fmt.Errorf("reconcile task completions: %w", err)
	}
	if completed {
		// Completion is deterministic and changes dependency readiness. Reload
		// immediately so the next frontier can be admitted without waiting for a
		// timer tick; each recursion closes at least one finite plan node.
		return service.reconcileFeaturePlan(ctx, feature, plan)
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
		// those authoritative snapshots before admitting another ready node in
		// the same reconciliation pass. Each recursion consumes one previously
		// inactive finite plan node, so it terminates after at most len(tasks)
		// admissions while allowing independent actors to begin concurrently.
		return service.reconcileFeaturePlan(ctx, feature, plan)
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
		taskRef := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: item.ID}
		profileSnapshot, found := snapshot.WorkProfiles[taskRef]
		if !found || !profileSnapshot.Valid() {
			return false, organization.ErrInvalidFeature
		}
		current := profileSnapshot.Profile
		// An invocation owns deadline recovery only while it is bound to the
		// task's current lifecycle, scope, and profile. A reopened validator keeps
		// its prior invocation as history; that stale invocation must not prevent
		// the successor profile from inheriting an extended feature deadline.
		if invocation, found := invocations[item.ID]; invocationOwnsProfileDeadline(invocation, found, current) {
			continue
		}
		if !profileDeadlineNeedsExtension(current.Budgets.DeadlineAt, budget.DeadlineAt, now, service.requestTimeout) {
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

func invocationOwnsProfileDeadline(invocation kernel.WorkInvocation, found bool, current kernel.WorkRiskProfile) bool {
	return found && invocation.WorkProfile == current.Binding()
}

func profileDeadlineNeedsExtension(current, account, now time.Time, requestTimeout time.Duration) bool {
	requiredRunway := now
	if requestTimeout > 0 {
		requiredRunway = now.Add(requestTimeout)
	}
	return !current.After(requiredRunway) && current.Before(account)
}

func (service *ProductionService) featureDeadlineExtensionEvidenceIDs(ctx context.Context, plan organization.FeaturePlan, snapshot kernel.Snapshot, budget kernel.WorkBudgetAccount) ([]kernel.UUIDv7, error) {
	// The causal evidence for the budget's current deadline is the evidence
	// carried by its last amendment event. Collecting the union of every task
	// profile's classification evidence instead grows without bound as the
	// feature ages and eventually exceeds the profile Valid() cap of 64,
	// fault-looping the reconciler permanently.
	var evidenceIDs []kernel.UUIDv7
	if service.Store != nil && budget.LastEventID.Valid() {
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
	if len(evidenceIDs) == 0 {
		evidenceIDs = profileDeadlineExtensionEvidenceIDs(plan, snapshot, budget.DeadlineAt)
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
		state := states[validator.ID]
		if len(validator.Validates) == 0 || !changedCandidateValidatorPhase(state.Phase) {
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
		if featureValidationAtCeiling(feature, snapshot, service.clock.Now().UTC()) {
			return service.blockStructuredDecisionTask(ctx, feature, validator, state, latest, snapshot, validationTimeCeilingReason, "changed-candidate-validation-time-ceiling")
		}
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
		if state.Phase == kernel.PhaseCompleted {
			updated, updatedHead, successor, reopenErr := service.reopenCompletedValidatorForChangedCandidate(ctx, feature, validator, state, heads[validator.ID], profileSnapshot.Profile, expectedCondition, candidateEvidence)
			if reopenErr != nil {
				return false, reopenErr
			}
			state = updated
			states[validator.ID] = updated
			heads[validator.ID] = updatedHead
			tracked.revision = updated.Revision
			tracked.last = updatedHead
			tracked.profile = successor
		}
		// A daemon or role restart may replace the validator execution between
		// candidate rounds. Keep the qualified assignment aligned with the
		// current profile and execution before rebinding scope or authorizing the
		// successor invocation, exactly as explicit task recovery does.
		if err := service.rebindPlanningRecoveryAssignment(ctx, feature, tracked, profileConfig, owner); err != nil {
			return false, err
		}
		if err := service.rebindTaskCandidateWorkspace(ctx, feature, tracked, workspace, candidateEvidence); err != nil {
			return false, err
		}
		if state.Condition == kernel.ConditionBlocked {
			if err := service.unblockValidationForChangedCandidate(ctx, tracked, latest, workspace, candidateEvidence); err != nil {
				return false, err
			}
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

func changedCandidateValidatorPhase(phase kernel.Phase) bool {
	return phase == kernel.PhaseActive || phase == kernel.PhaseCompleted
}

func (service *ProductionService) reopenCompletedValidatorForChangedCandidate(ctx context.Context, feature organization.FeatureRequest, validator organization.PlannedTask, state kernel.AggregateState, head kernel.UUIDv7, current kernel.WorkRiskProfile, condition kernel.Digest, evidence []kernel.EvidenceRef) (kernel.AggregateState, kernel.UUIDv7, kernel.WorkRiskProfile, error) {
	if state.Phase != kernel.PhaseCompleted || !current.Valid() || !condition.Valid() || len(evidence) == 0 {
		return kernel.AggregateState{}, "", kernel.WorkRiskProfile{}, organization.ErrInvalidFeature
	}
	evidenceIDs := evidenceIDs(evidence)
	request := TaskRecoveryRequest{
		Reason:         "A validated dependency produced a changed candidate; revalidate only this dependent task against the new immutable candidate.",
		IdempotencyKey: "changed-candidate-" + string(condition),
	}
	updated, updatedHead, err := service.reopenCompletedTaskForRecovery(ctx, feature, validator, state, head, request, evidence)
	if err != nil {
		return kernel.AggregateState{}, "", kernel.WorkRiskProfile{}, err
	}
	successor, err := reopenedTaskRecoveryProfile(current, service.planning, condition, current.Budgets.DeadlineAt, evidenceIDs, updated.LifecycleEpoch, updated.ScopeRevision)
	if err != nil {
		return kernel.AggregateState{}, "", kernel.WorkRiskProfile{}, err
	}
	tracked := &trackedTask{plan: validator, revision: updated.Revision, last: updatedHead, profile: successor}
	profileKey := fmt.Sprintf("changed-candidate-profile-%d-%s", successor.ProfileRevision, successor.ProfileDigest)
	if err := service.applyTaskCommand(ctx, feature, tracked, "tekroo.command.task.bind-work-profile", kernel.SchemaVersion, service.policyAuthority, successor, evidence, nil, profileKey); err != nil {
		return kernel.AggregateState{}, "", kernel.WorkRiskProfile{}, err
	}
	updated.Revision = tracked.revision
	return updated, tracked.last, successor, nil
}

func (service *ProductionService) unblockValidationForChangedCandidate(ctx context.Context, task *trackedTask, prior kernel.WorkInvocation, workspace ProductionWorkspace, evidence []kernel.EvidenceRef) error {
	if task == nil || prior.State != kernel.InvocationSucceeded || prior.OutputDigest == nil || !validGitCommit(workspace.BaselineSHA) || len(evidence) == 0 {
		return organization.ErrInvalidFeature
	}
	payload, err := json.Marshal(map[string]any{
		"resolved_blocker_refs": []string{"teams://work-invocation/" + string(prior.ID)},
		"evidence_ids":          evidenceIDs(evidence),
	})
	if err != nil {
		return err
	}
	key := "changed-validation-candidate-unblock-" + string(prior.ID) + "-" + workspace.BaselineSHA
	receipt, err := service.submitStandaloneCommand(ctx, "tekroo.command.work.unblock", kernel.AggregateTask, task.plan.ID, service.policyAuthority, task.revision, payload, []kernel.DagParent{{ParentEventID: task.last, EdgeKind: kernel.EdgeResponse}}, evidence, key)
	if err != nil {
		return err
	}
	if len(receipt.EventIDs) != 1 {
		return organization.ErrInvalidFeature
	}
	task.revision = revisionAfter(receipt, task.revision)
	task.last = receipt.EventIDs[0]
	return nil
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
