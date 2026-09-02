package operationalruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

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
	tasksByID := make(map[kernel.UUIDv7]organization.PlannedTask, len(plan.Tasks))
	var snapshot kernel.Snapshot
	for _, item := range plan.Tasks {
		tasksByID[item.ID] = item
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
	revalidationAuthorized, err := service.reconcileValidationRounds(ctx, feature, plan, states, heads, invocations, snapshot, budget.Revision)
	if err != nil {
		return err
	}
	if revalidationAuthorized {
		return nil
	}
	completed, err := service.reconcileTaskCompletions(ctx, feature, plan, states, heads, invocations, snapshot)
	if err != nil {
		return err
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

	for _, item := range plan.Tasks {
		state := states[item.ID]
		if state.Phase == kernel.PhaseActive {
			if state.Condition != kernel.ConditionRunnable {
				continue
			}
			latest, found := invocations[item.ID]
			if found && (latest.State == kernel.InvocationFailed || latest.State == kernel.InvocationTimedOut || latest.State == kernel.InvocationStartFailed) && latest.Retryable != nil && *latest.Retryable && latest.AttemptOrdinal < uint64(item.AttemptLimit) {
				profileConfig, configured := service.profilesByModel[item.ModelProfile]
				owner, active, ownerErr := service.RoleHost.Status(ctx, item.Owner)
				workspace, workspaceFound := service.workspacesByID[owner.WorkspaceID]
				profileSnapshot, profileFound := snapshot.WorkProfiles[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: item.ID}]
				if !configured || ownerErr != nil || !active || owner.Status != organization.RoleIdle || !workspaceFound || !profileFound || !profileSnapshot.Valid() {
					return errors.Join(organization.ErrRoleNotRunning, ownerErr)
				}
				tracked := &trackedTask{plan: item, revision: state.Revision, last: heads[item.ID], profile: profileSnapshot.Profile, owner: owner}
				if err := service.authorizeTaskInvocationWithCondition(ctx, feature, tracked, profileConfig, workspace, budget.Revision, latest.Purpose, latest.AttemptOrdinal+1, &latest, nil); err != nil {
					return err
				}
				budget.Revision++
			}
			continue
		}
		if state.Phase != kernel.PhasePlanned {
			continue
		}
		dependencyEvents := make([]kernel.UUIDv7, 0, len(item.DependsOn))
		conditionDigests := make([]kernel.Digest, 0, len(item.Validates))
		ready := len(item.DependsOn) > 0
		validationTargets := make(map[kernel.UUIDv7]struct{}, len(item.Validates))
		for _, targetID := range item.Validates {
			validationTargets[targetID] = struct{}{}
		}
		for _, dependencyID := range item.DependsOn {
			if _, validationTarget := validationTargets[dependencyID]; validationTarget {
				invocation, succeeded := invocations[dependencyID]
				if !succeeded || invocation.State != kernel.InvocationSucceeded || invocation.OutputDigest == nil {
					ready = false
					break
				}
				dependencyEvents = append(dependencyEvents, invocation.LastEventID)
				conditionDigests = append(conditionDigests, *invocation.OutputDigest)
				continue
			}
			dependency := states[dependencyID]
			if dependency.Phase != kernel.PhaseCompleted {
				dependencyTask, planned := tasksByID[dependencyID]
				invocation, succeeded := invocations[dependencyID]
				if !planned || !implementationChainDependencyReady(item, dependencyTask, invocation, succeeded) {
					ready = false
					break
				}
				dependencyEvents = append(dependencyEvents, invocation.LastEventID)
				continue
			}
			dependencyEvents = append(dependencyEvents, heads[dependencyID])
		}
		if !ready {
			continue
		}
		sort.Slice(dependencyEvents, func(left, right int) bool { return dependencyEvents[left] < dependencyEvents[right] })
		profileConfig, found := service.profilesByModel[item.ModelProfile]
		if !found || profileConfig.Qualification.DecisionRoute != item.DecisionRoute {
			return organization.ErrInvalidFeature
		}
		owner, active, err := service.RoleHost.Status(ctx, item.Owner)
		if err != nil {
			return err
		}
		if !active || owner.Status != organization.RoleIdle {
			owner, err = service.RoleHost.EnsureStarted(ctx, item.Owner)
		}
		if err != nil || owner.ModelProfile != item.ModelProfile {
			return errors.Join(organization.ErrRoleNotRunning, err)
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
		if err := service.activateTask(ctx, feature, tracked, profileConfig, workspace, budget.Revision, dependencyEvents, evidence, evidenceID, conditionDigests); err != nil {
			return err
		}
		budget.Revision++
	}
	return nil
}

// implementationChainDependencyReady permits a later implementation stage to
// continue in the same actor workspace after the preceding implementation
// invocation succeeds, while independent validation remains pending. Without
// this rule, a plan with one validator covering a sequential implementation
// chain deadlocks: each stage waits for completion, but completion waits for
// the validator, which cannot run until every stage has produced its result.
// All other dependency kinds retain the stricter completed-phase requirement.
func implementationChainDependencyReady(task organization.PlannedTask, dependency organization.PlannedTask, invocation kernel.WorkInvocation, found bool) bool {
	return task.Purpose == kernel.PurposeImplementation &&
		dependency.Purpose == kernel.PurposeImplementation &&
		task.Owner == dependency.Owner &&
		found &&
		invocation.State == kernel.InvocationSucceeded &&
		invocation.OutputDigest != nil
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
	if err != nil || result.Outcome != "PASS" {
		return errors.Join(organization.ErrInvalidFeature, err)
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

func (service *ProductionService) reconcileValidationRounds(ctx context.Context, feature organization.FeatureRequest, plan organization.FeaturePlan, states map[kernel.UUIDv7]kernel.AggregateState, heads map[kernel.UUIDv7]kernel.UUIDv7, invocations map[kernel.UUIDv7]kernel.WorkInvocation, snapshot kernel.Snapshot, budgetRevision uint64) (bool, error) {
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
		conditionDigests := make([]kernel.Digest, 0, len(validator.Validates))
		repairedCandidate := false
		ready := true
		for _, dependencyID := range validator.DependsOn {
			if _, validates := validationTargets[dependencyID]; !validates {
				continue
			}
			candidate, present := invocations[dependencyID]
			if !present || candidate.State != kernel.InvocationSucceeded || candidate.OutputDigest == nil {
				ready = false
				break
			}
			conditionDigests = append(conditionDigests, *candidate.OutputDigest)
			repairedCandidate = repairedCandidate || candidate.Purpose == kernel.PurposeRepair
		}
		if !ready || !repairedCandidate {
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
		if latest.ConditionDigest == expectedCondition {
			continue
		}
		nextAttempt := latest.AttemptOrdinal + 1
		if nextAttempt > uint64(validator.AttemptLimit) {
			return false, organization.ErrInvalidFeature
		}
		profileConfig, configured := service.profilesByModel[validator.ModelProfile]
		owner, active, ownerErr := service.RoleHost.Status(ctx, validator.Owner)
		workspace, workspaceFound := service.workspacesByID[owner.WorkspaceID]
		if !configured || ownerErr != nil || !active || owner.Status != organization.RoleIdle || !workspaceFound {
			return false, errors.Join(organization.ErrRoleNotRunning, ownerErr)
		}
		tracked := &trackedTask{plan: validator, revision: states[validator.ID].Revision, last: heads[validator.ID], profile: profileSnapshot.Profile, owner: owner}
		if err := service.authorizeTaskInvocationWithCondition(ctx, feature, tracked, profileConfig, workspace, budgetRevision, validator.Purpose, nextAttempt, nil, conditionDigests); err != nil {
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
