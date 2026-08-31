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

	for _, item := range plan.Tasks {
		state := states[item.ID]
		if state.Phase == kernel.PhaseActive {
			latest, found := invocations[item.ID]
			if found && (latest.State == kernel.InvocationFailed || latest.State == kernel.InvocationTimedOut || latest.State == kernel.InvocationStartFailed) && latest.Retryable != nil && *latest.Retryable && latest.AttemptOrdinal < uint64(item.AttemptLimit) {
				profileConfig, configured := service.profilesByModel[item.ModelProfile]
				owner, active, ownerErr := service.RoleHost.Status(ctx, item.Owner)
				workspace, workspaceFound := service.workspacesByID[owner.WorkspaceID]
				profileSnapshot, profileFound := snapshot.WorkProfiles[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: item.ID}]
				if !configured || ownerErr != nil || !active || owner.Status != organization.RoleIdle || owner.Execution != latest.Execution || !workspaceFound || !profileFound || !profileSnapshot.Valid() {
					return errors.Join(organization.ErrRoleNotRunning, ownerErr)
				}
				tracked := &trackedTask{plan: item, revision: state.Revision, last: heads[item.ID], profile: profileSnapshot.Profile, owner: owner}
				if err := service.authorizeTaskInvocation(ctx, feature, tracked, profileConfig, workspace, budget.Revision, latest.AttemptOrdinal+1, &latest); err != nil {
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
		ready := len(item.DependsOn) > 0
		for _, dependencyID := range item.DependsOn {
			invocation, succeeded := invocations[dependencyID]
			if !succeeded || invocation.State != kernel.InvocationSucceeded {
				ready = false
				break
			}
			dependencyEvents = append(dependencyEvents, invocation.LastEventID)
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
		if err := service.registerExecution(ctx, feature, owner, profileConfig); err != nil {
			return err
		}
		profileSnapshot, found := snapshot.WorkProfiles[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: item.ID}]
		if !found || !profileSnapshot.Valid() {
			return organization.ErrInvalidFeature
		}
		tracked := &trackedTask{plan: item, revision: state.Revision, last: heads[item.ID], profile: profileSnapshot.Profile, owner: owner}
		if err := service.activateTask(ctx, feature, tracked, profileConfig, workspace, budget.Revision, dependencyEvents, evidence, evidenceID); err != nil {
			return err
		}
		budget.Revision++
	}
	return nil
}

func latestTaskInvocation(values map[kernel.AggregateRef]kernel.WorkInvocation, taskID kernel.UUIDv7) (kernel.WorkInvocation, bool) {
	var latest kernel.WorkInvocation
	found := false
	for _, candidate := range values {
		if candidate.TaskID != taskID {
			continue
		}
		if !found || candidate.AttemptOrdinal > latest.AttemptOrdinal || candidate.AttemptOrdinal == latest.AttemptOrdinal && candidate.Revision > latest.Revision {
			latest, found = candidate.Clone(), true
		}
	}
	return latest, found
}
