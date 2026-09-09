package operationalruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// FeatureReplanRequest is an explicit operator correction of an already
// materialized feature plan. It preserves the accepted specification and all
// prior evidence while giving the replacement architecture work a new finite
// deadline and scope revision.
type FeatureReplanRequest struct {
	ExpectedRevision    uint64               `json:"expected_revision"`
	ExpectedPlanVersion uint64               `json:"expected_plan_version"`
	Reason              string               `json:"reason"`
	EvidenceRefs        []kernel.EvidenceRef `json:"evidence_refs"`
	DeadlineAt          time.Time            `json:"deadline_at"`
	IdempotencyKey      string               `json:"idempotency_key"`
}

func (request FeatureReplanRequest) Valid() bool {
	if request.ExpectedRevision == 0 || request.ExpectedPlanVersion == 0 || request.Reason == "" || len(request.Reason) > 4096 || len(request.EvidenceRefs) == 0 || len(request.EvidenceRefs) > 64 || request.DeadlineAt.IsZero() || request.IdempotencyKey == "" || len(request.IdempotencyKey) > 256 {
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

// RequestFeatureReplan retires only an uncompleted materialized task graph and
// returns the same feature to architecture. Completed product work cannot be
// discarded through this operation; that requires explicit compensation.
func (service *ProductionService) RequestFeatureReplan(ctx context.Context, principal kernel.PrincipalRef, featureID kernel.UUIDv7, request FeatureReplanRequest) (organization.FeatureRequest, error) {
	if service == nil || service.Features == nil || principal != service.operatorIdentity.Principal || !featureID.Valid() || !request.Valid() {
		return organization.FeatureRequest{}, application.ErrInvalidConfiguration
	}
	feature, found, err := service.ReadFeature(ctx, featureID)
	if err != nil || !found {
		return organization.FeatureRequest{}, errors.Join(organization.ErrFeatureNotFound, err)
	}
	if feature.Status == organization.FeatureSpecified && feature.PlanSupersession != nil && feature.PlanSupersession.IdempotencyKey == request.IdempotencyKey {
		if feature.Revision == request.ExpectedRevision+1 && feature.PlanSupersession.PlanVersion == request.ExpectedPlanVersion && feature.PlanSupersession.RequestedBy == principal && feature.PlanSupersession.Reason == request.Reason && feature.PlanSupersession.DeadlineAt.Equal(request.DeadlineAt.UTC()) && sameEvidenceSet(feature.PlanSupersession.EvidenceRefs, request.EvidenceRefs) {
			return feature, nil
		}
		return organization.FeatureRequest{}, organization.ErrFeatureRevisionConflict
	}
	if feature.Status != organization.FeaturePlanned || feature.Revision != request.ExpectedRevision || feature.Plan == nil || feature.Plan.Version != request.ExpectedPlanVersion {
		return organization.FeatureRequest{}, organization.ErrFeatureRevisionConflict
	}
	planRaw, err := json.Marshal(*feature.Plan)
	if err != nil {
		return organization.FeatureRequest{}, err
	}
	planDigest := digestBytes(planRaw)
	now := service.clock.Now().UTC()
	deadline := request.DeadlineAt.UTC()
	if !deadline.After(now.Add(service.requestTimeout)) || deadline.After(now.Add(service.planningDeadline)) {
		return organization.FeatureRequest{}, organization.ErrInvalidFeature
	}

	budgetRef := kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: feature.BudgetAccountID}
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: budgetRef})
	if err != nil {
		return organization.FeatureRequest{}, err
	}
	evidenceIDs := make([]kernel.UUIDv7, len(request.EvidenceRefs))
	for index, evidence := range request.EvidenceRefs {
		evidenceIDs[index] = evidence.EvidenceID
	}
	registered, err := evidenceRefsForIDs(snapshot, evidenceIDs)
	if err != nil || !sameEvidenceSet(registered, request.EvidenceRefs) {
		return organization.FeatureRequest{}, errors.Join(organization.ErrInvalidFeature, err)
	}
	budget, found := snapshot.WorkBudgetAccounts[budgetRef]
	if !found || !budget.Valid() || deadline.Before(budget.DeadlineAt) {
		return organization.FeatureRequest{}, organization.ErrInvalidFeature
	}

	type taskHead struct {
		plan      organization.PlannedTask
		state     kernel.AggregateState
		head      kernel.UUIDv7
		execution kernel.ExecutionTuple
	}
	tasks := make([]taskHead, 0, len(feature.Plan.Tasks))
	for _, planned := range feature.Plan.Tasks {
		state, head, taskFound, readErr := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: planned.ID})
		if readErr != nil || !taskFound || state.Phase == kernel.PhaseCompleted || state.Phase == kernel.PhaseAccepted {
			return organization.FeatureRequest{}, errors.Join(organization.ErrInvalidFeature, readErr)
		}
		decision, loadErr := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: planned.ID}})
		if loadErr != nil {
			return organization.FeatureRequest{}, loadErr
		}
		if invocation, invocationFound := latestTaskInvocation(decision.WorkInvocations, planned.ID); invocationFound && !invocation.State.Terminal() {
			return organization.FeatureRequest{}, application.ErrInvalidOperationalExecution
		}
		execution, executionFound := decision.CurrentExecutions[planned.Owner]
		if !executionFound {
			return organization.FeatureRequest{}, organization.ErrRoleNotRunning
		}
		tasks = append(tasks, taskHead{plan: planned, state: state, head: head, execution: execution})
	}
	type storyHead struct {
		plan      organization.PlannedStory
		state     kernel.AggregateState
		head      kernel.UUIDv7
		execution kernel.ExecutionTuple
	}
	stories := make([]storyHead, 0, len(feature.Plan.Stories))
	for _, planned := range feature.Plan.Stories {
		state, head, storyFound, readErr := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateStory, ID: planned.ID})
		if readErr != nil || !storyFound || state.Phase == kernel.PhaseCompleted || state.Phase == kernel.PhaseAccepted {
			return organization.FeatureRequest{}, errors.Join(organization.ErrInvalidFeature, readErr)
		}
		decision, loadErr := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateStory, ID: planned.ID}})
		if loadErr != nil {
			return organization.FeatureRequest{}, loadErr
		}
		execution, executionFound := decision.CurrentExecutions[feature.ProductOwnerActor]
		if !executionFound {
			return organization.FeatureRequest{}, organization.ErrRoleNotRunning
		}
		stories = append(stories, storyHead{plan: planned, state: state, head: head, execution: execution})
	}

	architectureRound, err := service.latestArchitecturePlanRound(ctx, feature)
	if err != nil || architectureRound >= architecturePlanRecordedRoundLimit {
		return organization.FeatureRequest{}, errors.Join(organization.ErrInvalidFeature, err)
	}
	architectureRound++
	budget, err = service.amendFeatureReplanBudget(ctx, principal, feature, budget, request, planDigest, registered)
	if err != nil {
		return organization.FeatureRequest{}, err
	}
	_ = budget
	for _, task := range tasks {
		if task.state.Phase == kernel.PhaseClosed || task.state.Condition == kernel.ConditionBlocked {
			continue
		}
		payload, marshalErr := json.Marshal(map[string]any{
			"blocker_refs":  []string{"teams://feature/" + string(feature.ID) + "/plan/" + fmt.Sprint(feature.Plan.Version)},
			"reason":        "feature plan superseded: " + request.Reason,
			"review_policy": "operator-authorized replacement architecture required",
		})
		if marshalErr != nil {
			return organization.FeatureRequest{}, marshalErr
		}
		key := "feature-replan-retire-" + string(feature.ID) + "-" + fmt.Sprint(feature.Plan.Version) + "-" + string(task.plan.ID) + "-" + request.IdempotencyKey
		if _, err := service.submitDeterministicActorTargetCommand(ctx, feature, "tekroo.command.work.block", kernel.AggregateTask, task.plan.ID, service.policyAuthority, task.plan.Owner, task.execution, task.state.Revision, task.state.LifecycleEpoch, payload, []kernel.DagParent{{ParentEventID: task.head, EdgeKind: kernel.EdgeCausal}}, registered, key); err != nil {
			return organization.FeatureRequest{}, err
		}
	}
	for _, story := range stories {
		if story.state.Phase == kernel.PhaseClosed || story.state.Condition == kernel.ConditionBlocked {
			continue
		}
		payload, marshalErr := json.Marshal(map[string]any{
			"blocker_refs":  []string{"teams://feature/" + string(feature.ID) + "/plan/" + fmt.Sprint(feature.Plan.Version)},
			"reason":        "feature plan superseded: " + request.Reason,
			"review_policy": "operator-authorized replacement architecture required",
		})
		if marshalErr != nil {
			return organization.FeatureRequest{}, marshalErr
		}
		key := "feature-replan-retire-story-" + string(feature.ID) + "-" + fmt.Sprint(feature.Plan.Version) + "-" + string(story.plan.ID) + "-" + request.IdempotencyKey
		if _, err := service.submitDeterministicActorTargetCommand(ctx, feature, "tekroo.command.work.block", kernel.AggregateStory, story.plan.ID, service.policyAuthority, feature.ProductOwnerActor, story.execution, story.state.Revision, story.state.LifecycleEpoch, payload, []kernel.DagParent{{ParentEventID: story.head, EdgeKind: kernel.EdgeCausal}}, registered, key); err != nil {
			return organization.FeatureRequest{}, err
		}
	}
	supersession := organization.FeaturePlanSupersession{
		PlanVersion: feature.Plan.Version, PlanDigest: planDigest, ArchitectureRound: architectureRound,
		RequestedBy: principal, Reason: request.Reason, EvidenceRefs: append([]kernel.EvidenceRef(nil), registered...),
		DeadlineAt: deadline, RequestedAt: now, IdempotencyKey: request.IdempotencyKey,
	}
	return service.Features.RequestReplan(ctx, feature.ID, feature.Revision, supersession)
}

func (service *ProductionService) amendFeatureReplanBudget(ctx context.Context, principal kernel.PrincipalRef, feature organization.FeatureRequest, account kernel.WorkBudgetAccount, request FeatureReplanRequest, planDigest kernel.Digest, evidence []kernel.EvidenceRef) (kernel.WorkBudgetAccount, error) {
	deadline := request.DeadlineAt.UTC()
	targetDigest := digestBytes([]byte("feature-replan-budget\x00" + string(feature.ID) + "\x00" + fmt.Sprint(request.ExpectedPlanVersion) + "\x00" + string(planDigest) + "\x00" + deadline.Format(time.RFC3339Nano) + "\x00" + request.IdempotencyKey))
	if account.PolicyDigest == targetDigest && account.DeadlineAt.Equal(deadline) {
		return account, nil
	}
	if !account.Valid() || account.ID != feature.BudgetAccountID || account.LifecycleEpoch != feature.LifecycleEpoch || deadline.Before(account.DeadlineAt) {
		return kernel.WorkBudgetAccount{}, organization.ErrInvalidFeature
	}
	targetRevision := account.PolicyRevision + 1
	payload, err := json.Marshal(map[string]any{
		"budget_account_id": account.ID, "expected_budget_revision": account.Revision,
		"expected_lifecycle_epoch": account.LifecycleEpoch, "policy_revision": targetRevision,
		"policy_digest": targetDigest, "model_invocation_limit": account.ModelInvocationLimit,
		"purpose_limits": account.PurposeLimits, "deadline_at": deadline, "reason": request.Reason,
		"evidence_ids": evidenceIDs(evidence), "authority": principal,
	})
	if err != nil {
		return kernel.WorkBudgetAccount{}, err
	}
	key := "feature-replan-budget-" + string(feature.ID) + "-" + fmt.Sprint(request.ExpectedPlanVersion) + "-" + request.IdempotencyKey
	if _, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.work-budget.amend", kernel.OperationalSchemaVersion, kernel.AggregateWorkBudget, account.ID, principal, account.Revision, payload, []kernel.DagParent{{ParentEventID: account.LastEventID, EdgeKind: kernel.EdgeCausal}}, evidence, key); err != nil {
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
