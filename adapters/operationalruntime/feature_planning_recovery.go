package operationalruntime

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// PlanningRecoveryRequest is an explicit operator decision to continue one
// cancelled feature-planning invocation under a new condition and execution.
type PlanningRecoveryRequest struct {
	ExpectedRevision uint64               `json:"expected_revision"`
	Reason           string               `json:"reason"`
	EvidenceRefs     []kernel.EvidenceRef `json:"evidence_refs"`
	IdempotencyKey   string               `json:"idempotency_key"`
}

func (request PlanningRecoveryRequest) Valid() bool {
	if request.ExpectedRevision == 0 || request.Reason == "" || len(request.Reason) > 4096 || len(request.EvidenceRefs) == 0 || len(request.EvidenceRefs) > 64 || request.IdempotencyKey == "" || len(request.IdempotencyKey) > 256 {
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
	cancelled, found, err := service.ReadInvocation(ctx, invocationID)
	if err != nil || !found || cancelled.Revision != request.ExpectedRevision || cancelled.State != kernel.InvocationCancelled || cancelled.CancellationRequestedAt == nil {
		return InvocationStatus{}, errors.Join(application.ErrInvalidOperationalExecution, err)
	}

	feature, stage, found, err := service.cancelledPlanningFeature(ctx, cancelled.TaskID)
	if err != nil || !found {
		return InvocationStatus{}, errors.Join(organization.ErrInvalidFeature, err)
	}
	task, state, head, latest, snapshot, err := service.ensureFeaturePlanningTask(ctx, feature, stage)
	if err != nil {
		return InvocationStatus{}, err
	}
	taskRef := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}
	profile, profileFound := snapshot.WorkProfiles[taskRef]
	recoveryCondition, err := planningRecoveryConditionDigest(invocationID, request)
	if err != nil || !profileFound || !profile.Valid() {
		return InvocationStatus{}, errors.Join(organization.ErrInvalidFeature, err)
	}
	criteria, err := json.Marshal(task.AcceptanceCriteria)
	if err != nil {
		return InvocationStatus{}, err
	}
	expectedCondition, err := taskInvocationConditionDigest(profile.Profile.ProfileDigest, digestBytes(criteria), []kernel.Digest{recoveryCondition})
	if err != nil {
		return InvocationStatus{}, err
	}
	if latest.ID != cancelled.InvocationID {
		if latest.AttemptOrdinal == cancelled.AttemptOrdinal+1 && latest.ConditionDigest == expectedCondition {
			if status, currentFound, currentErr := service.ReadInvocation(ctx, latest.ID); currentErr == nil && currentFound {
				return status, nil
			}
		}
		return InvocationStatus{}, application.ErrInvalidOperationalExecution
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

	owner, active, err := service.RoleHost.Status(ctx, task.Owner)
	if err != nil {
		return InvocationStatus{}, err
	}
	if active {
		if owner.Status != organization.RoleIdle {
			return InvocationStatus{}, organization.ErrRoleNotRunning
		}
		owner, err = service.RestartRole(ctx, task.Owner)
	} else {
		owner, err = service.StartRole(ctx, task.Owner)
	}
	if err != nil || owner.Status != organization.RoleIdle || owner.Execution == cancelled.Execution {
		return InvocationStatus{}, errors.Join(organization.ErrRoleNotRunning, err)
	}
	profileConfig, configured := service.profilesByModel[owner.ModelProfile]
	workspace, workspaceFound := service.workspacesByID[owner.WorkspaceID]
	budget, budgetFound := snapshot.WorkBudgetAccounts[kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: feature.BudgetAccountID}]
	if !configured || !workspaceFound || !profileFound || !profile.Valid() || !budgetFound || !budget.Valid() {
		return InvocationStatus{}, organization.ErrInvalidFeature
	}
	tracked := &trackedTask{plan: task, revision: state.Revision, last: head, profile: profile.Profile, owner: owner}
	budgetRevision, err := service.extendTaskTechnicalRetryBudget(ctx, feature, tracked, task.Purpose, cancelled.AttemptOrdinal+1)
	if err != nil {
		return InvocationStatus{}, err
	}
	if err := service.authorizeTaskInvocationWithConditionPolicy(ctx, feature, tracked, profileConfig, workspace, budgetRevision, task.Purpose, cancelled.AttemptOrdinal+1, nil, []kernel.Digest{recoveryCondition}, true); err != nil {
		return InvocationStatus{}, err
	}

	updated, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: taskRef})
	if err != nil {
		return InvocationStatus{}, err
	}
	next, nextFound := latestTaskInvocation(updated.WorkInvocations, task.ID)
	if !nextFound || next.ID == invocationID || next.AttemptOrdinal != cancelled.AttemptOrdinal+1 {
		return InvocationStatus{}, application.ErrInvalidOperationalExecution
	}
	status, statusFound, err := service.ReadInvocation(ctx, next.ID)
	if err != nil || !statusFound {
		return InvocationStatus{}, errors.Join(application.ErrInvalidOperationalExecution, err)
	}
	return status, nil
}

func (service *ProductionService) cancelledPlanningFeature(ctx context.Context, taskID kernel.UUIDv7) (organization.FeatureRequest, featurePlanningStage, bool, error) {
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
		IdempotencyKey string               `json:"idempotency_key"`
	}{invocationID, request.Reason, evidence, request.IdempotencyKey})
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
