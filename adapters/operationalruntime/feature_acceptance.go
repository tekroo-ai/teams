package operationalruntime

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// AcceptFeature turns the product-owner recommendation into canonical story
// completion and acceptance. The exact human who submitted the feature remains
// the release/acceptance authority; model output is evidence, never authority.
func (service *ProductionService) AcceptFeature(ctx context.Context, featureID kernel.UUIDv7, expectedRevision uint64, principal kernel.PrincipalRef, noReleaseReason string) (organization.FeatureRequest, error) {
	if service == nil || service.Features == nil || principal.Kind != kernel.PrincipalHuman || !principal.Valid() || noReleaseReason == "" || len(noReleaseReason) > 4096 {
		return organization.FeatureRequest{}, organization.ErrInvalidFeature
	}
	feature, found, err := service.ReadFeature(ctx, featureID)
	if err != nil || !found {
		return organization.FeatureRequest{}, errors.Join(organization.ErrFeatureNotFound, err)
	}
	if feature.Revision != expectedRevision || feature.Status != organization.FeatureAwaitingAcceptance || feature.Acceptance == nil || feature.Plan == nil || feature.SubmittedBy != principal {
		return organization.FeatureRequest{}, organization.ErrFeatureRevisionConflict
	}
	acceptanceTask, invocation, validators, snapshot, err := service.featureAcceptanceEvidence(ctx, feature)
	if err != nil {
		return organization.FeatureRequest{}, err
	}
	releasePlanIDs := make([]kernel.UUIDv7, 0, len(feature.Plan.Stories))
	for _, story := range feature.Plan.Stories {
		releasePlanID, acceptErr := service.completeAndAcceptFeatureStory(ctx, feature, story, acceptanceTask, invocation, validators, snapshot, principal, noReleaseReason)
		if acceptErr != nil {
			return organization.FeatureRequest{}, acceptErr
		}
		releasePlanIDs = append(releasePlanIDs, releasePlanID)
	}
	return service.Features.Accept(ctx, feature.ID, feature.Revision, principal, releasePlanIDs)
}

func (service *ProductionService) featureAcceptanceEvidence(ctx context.Context, feature organization.FeatureRequest) (organization.PlannedTask, kernel.WorkInvocation, []kernel.WorkInvocation, kernel.Snapshot, error) {
	var acceptanceTask organization.PlannedTask
	foundAcceptance := false
	validatorTasks := make(map[kernel.UUIDv7]struct{})
	for _, task := range feature.Plan.Tasks {
		if task.Purpose == kernel.PurposePromotion {
			if foundAcceptance {
				return organization.PlannedTask{}, kernel.WorkInvocation{}, nil, kernel.Snapshot{}, organization.ErrInvalidFeature
			}
			acceptanceTask, foundAcceptance = task, true
		}
	}
	if !foundAcceptance {
		return organization.PlannedTask{}, kernel.WorkInvocation{}, nil, kernel.Snapshot{}, organization.ErrInvalidFeature
	}
	for _, task := range feature.Plan.Tasks {
		for _, target := range task.Validates {
			if target == acceptanceTask.ID {
				validatorTasks[task.ID] = struct{}{}
			}
		}
	}
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: acceptanceTask.ID}})
	if err != nil {
		return organization.PlannedTask{}, kernel.WorkInvocation{}, nil, kernel.Snapshot{}, err
	}
	invocation, found := latestTaskInvocation(snapshot.WorkInvocations, acceptanceTask.ID)
	if !found || invocation.State != kernel.InvocationSucceeded || invocation.OutputDigest == nil || *invocation.OutputDigest != feature.Acceptance.RecommendationHash {
		return organization.PlannedTask{}, kernel.WorkInvocation{}, nil, kernel.Snapshot{}, organization.ErrInvalidFeature
	}
	validators := make([]kernel.WorkInvocation, 0, len(validatorTasks))
	for taskID := range validatorTasks {
		validator, present := latestTaskInvocation(snapshot.WorkInvocations, taskID)
		if !present || validator.State != kernel.InvocationSucceeded || validator.OutputDigest == nil {
			return organization.PlannedTask{}, kernel.WorkInvocation{}, nil, kernel.Snapshot{}, organization.ErrInvalidFeature
		}
		validators = append(validators, validator)
	}
	sort.Slice(validators, func(left, right int) bool { return validators[left].ID < validators[right].ID })
	return acceptanceTask, invocation, validators, snapshot, nil
}

func (service *ProductionService) completeAndAcceptFeatureStory(ctx context.Context, feature organization.FeatureRequest, story organization.PlannedStory, acceptanceTask organization.PlannedTask, acceptance kernel.WorkInvocation, validators []kernel.WorkInvocation, snapshot kernel.Snapshot, principal kernel.PrincipalRef, noReleaseReason string) (kernel.UUIDv7, error) {
	storyRef := kernel.AggregateRef{Kind: kernel.AggregateStory, ID: story.ID}
	state, _, found, err := service.Store.ReadAggregateHead(ctx, storyRef)
	if err != nil || !found || state.Phase != kernel.PhaseActive || acceptance.OutputDigest == nil {
		return "", errors.Join(organization.ErrInvalidFeature, err)
	}
	allInvocations := append([]kernel.WorkInvocation{acceptance}, validators...)
	evidence, err := evidenceForInvocations(snapshot, allInvocations...)
	if err != nil {
		return "", err
	}
	profileSnapshot, found := snapshot.WorkProfiles[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: acceptanceTask.ID}]
	if !found || !profileSnapshot.Valid() {
		return "", organization.ErrInvalidFeature
	}
	identity, err := service.workExecutionIdentity(acceptance)
	if err != nil {
		return "", err
	}
	reviewID := deterministicOperationalUUID("story-completion-review", string(feature.ID), string(story.ID), string(*acceptance.OutputDigest))
	evidenceSet, _ := json.Marshal(evidence)
	branchID := "product-owner-acceptance"
	openPayload := mustJSON(map[string]any{
		"subject_kind": kernel.AggregateStory, "subject_id": story.ID, "lifecycle_epoch": feature.LifecycleEpoch,
		"scope_revision": feature.ScopeRevision, "criteria_revision": uint64(1), "evidence_set_digest": digestBytes(evidenceSet),
		"branch_policy_revision": service.planning.PolicyRevision,
		"branches":               []map[string]any{{"branch_id": branchID, "validator": service.serviceAuthority, "resolution_owner_fqn": feature.ProductOwnerActor, "acceptance_criteria": story.AcceptanceCriteria, "input_evidence_ids": evidenceIDs(evidence), "deadline_at": profileSnapshot.Profile.Budgets.DeadlineAt, "round_limit": uint64(1), "required_independence_dimensions": []kernel.IndependenceDimension{kernel.IndependencePrincipal, kernel.IndependenceMethod}, "required_method_ids": []string{"teams-product-acceptance-gate"}}},
		"join_rule":              "ALL_PASS", "partial_result_policy": "WAIT_ALL", "adjudication": map[string]any{"adjudicator": service.policyAuthority, "deadline_at": profileSnapshot.Profile.Budgets.DeadlineAt, "round_limit": uint64(1)},
		"work_profile": profileSnapshot.Profile.Binding(), "candidate_artifact_digest": *acceptance.OutputDigest,
		"implementer": identity, "verification_topology_digest": profileSnapshot.Profile.VerificationTopologyDigest, "variant_group_id": nil,
	})
	opened, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.completion-review.open", kernel.SchemaVersion, kernel.AggregateCompletionReview, reviewID, service.policyAuthority, 0, openPayload, []kernel.DagParent{{ParentEventID: acceptance.LastEventID, EdgeKind: kernel.EdgeCausal}}, evidence, "story-review-open-"+string(story.ID), kernel.AggregatePrecondition{Aggregate: storyRef, Expected: kernel.NewExpectedRevision(state.Revision)})
	if err != nil {
		return "", err
	}
	comparison, _ := json.Marshal(map[string]any{"implementer": identity, "validator": service.serviceAuthority})
	resultPayload := mustJSON(map[string]any{
		"review_id": reviewID, "branch_id": branchID, "branch_policy_revision": service.planning.PolicyRevision, "source_role": "VALIDATOR", "round": uint64(1), "result": "PASS",
		"reasons": []string{"the product-owner recommendation and independently validated feature DAG satisfy the story criteria"}, "evidence_ids": evidenceIDs(evidence), "findings": []any{}, "supersedes_result_event_ids": []kernel.UUIDv7{}, "changed_condition_evidence_ids": []kernel.UUIDv7{},
		"candidate_artifact_digest": *acceptance.OutputDigest, "independence_receipt": map[string]any{"proven_dimensions": []kernel.IndependenceDimension{kernel.IndependencePrincipal, kernel.IndependenceMethod}, "identity_comparison_digest": digestBytes(comparison), "method_ids": []string{"teams-product-acceptance-gate"}, "evidence_ids": evidenceIDs(evidence)},
	})
	recorded, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.completion-review.record-result", kernel.SchemaVersion, kernel.AggregateCompletionReview, reviewID, service.serviceAuthority, 1, resultPayload, []kernel.DagParent{{ParentEventID: opened.EventIDs[0], EdgeKind: kernel.EdgeResponse}}, evidence, "story-review-result-"+string(story.ID))
	if err != nil {
		return "", err
	}
	finalPayload := mustJSON(map[string]any{"review_id": reviewID, "subject_kind": kernel.AggregateStory, "subject_id": story.ID, "lifecycle_epoch": feature.LifecycleEpoch, "branch_policy_revision": service.planning.PolicyRevision, "expected_review_revision": uint64(2), "terminal_status": "PASS", "result_event_ids": recorded.EventIDs, "evidence_ids": evidenceIDs(evidence), "scope_revision": feature.ScopeRevision, "work_profile": profileSnapshot.Profile.Binding(), "candidate_artifact_digest": *acceptance.OutputDigest, "verification_topology_digest": profileSnapshot.Profile.VerificationTopologyDigest})
	finalized, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.completion-review.finalize", kernel.SchemaVersion, kernel.AggregateCompletionReview, reviewID, service.policyAuthority, 2, finalPayload, []kernel.DagParent{{ParentEventID: recorded.EventIDs[0], EdgeKind: kernel.EdgeResponse}}, evidence, "story-review-finalize-"+string(story.ID))
	if err != nil {
		return "", err
	}
	preconditions := []kernel.AggregatePrecondition{{Aggregate: kernel.AggregateRef{Kind: kernel.AggregateCompletionReview, ID: reviewID}, Expected: kernel.NewExpectedRevision(3)}}
	parents := []kernel.DagParent{{ParentEventID: finalized.EventIDs[0], EdgeKind: kernel.EdgeResponse}}
	for _, task := range feature.Plan.Tasks {
		if task.StoryID != story.ID {
			continue
		}
		taskState, taskHead, taskFound, taskErr := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID})
		if taskErr != nil || !taskFound || taskState.Phase != kernel.PhaseCompleted {
			return "", errors.Join(organization.ErrInvalidFeature, taskErr)
		}
		preconditions = append(preconditions, kernel.AggregatePrecondition{Aggregate: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}, Expected: kernel.NewExpectedRevision(taskState.Revision)})
		parents = append(parents, kernel.DagParent{ParentEventID: taskHead, EdgeKind: kernel.EdgeResponse})
	}
	sort.Slice(preconditions, func(left, right int) bool {
		leftKey := string(preconditions[left].Aggregate.Kind) + ":" + string(preconditions[left].Aggregate.ID)
		rightKey := string(preconditions[right].Aggregate.Kind) + ":" + string(preconditions[right].Aggregate.ID)
		return leftKey < rightKey
	})
	sort.Slice(parents, func(left, right int) bool { return parents[left].ParentEventID < parents[right].ParentEventID })
	completionPayload := mustJSON(map[string]any{"artifact_digests": []kernel.Digest{*acceptance.OutputDigest}, "branch_policy_revision": service.planning.PolicyRevision, "completion_review_id": reviewID, "completion_review_revision": uint64(2), "criteria_revision": uint64(1), "evidence_ids": evidenceIDs(evidence), "lifecycle_epoch": feature.LifecycleEpoch, "unresolved_exceptions": []string{}, "validation_finalized_event_id": finalized.EventIDs[0]})
	completed, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.story.request-completion", kernel.SchemaVersion, kernel.AggregateStory, story.ID, principal, state.Revision, completionPayload, parents, evidence, "story-complete-"+string(story.ID), preconditions...)
	if err != nil {
		return "", err
	}
	approvalPayload := mustJSON(map[string]any{"story_id": story.ID, "lifecycle_epoch": feature.LifecycleEpoch, "expected_story_revision": state.Revision + 1, "author": principal, "approval_revision": uint64(1), "release_policy_revision": service.planning.PolicyRevision, "reasons": []string{"the submitting human accepted the product-owner recommendation"}, "evidence_ids": evidenceIDs(evidence)})
	approved, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.story.approve-release", kernel.SchemaVersion, kernel.AggregateStory, story.ID, principal, state.Revision+1, approvalPayload, []kernel.DagParent{{ParentEventID: completed.EventIDs[0], EdgeKind: kernel.EdgeCausal}}, evidence, "story-release-approval-"+string(story.ID))
	if err != nil {
		return "", err
	}
	releasePlanID := deterministicOperationalUUID("release-plan", string(feature.ID), string(story.ID), string(*acceptance.OutputDigest))
	planDigest := digestBytes([]byte(string(feature.ID) + "\x00" + string(story.ID) + "\x00NO_RELEASE_REQUIRED\x00" + string(*acceptance.OutputDigest)))
	releasePayload := mustJSON(map[string]any{"release_plan_id": releasePlanID, "story_id": story.ID, "story_lifecycle_epoch": feature.LifecycleEpoch, "expected_story_revision": state.Revision + 2, "author": principal, "author_approval_event_id": approved.EventIDs[0], "author_approval_revision": state.Revision + 2, "release_policy_revision": service.planning.PolicyRevision, "plan_digest": planDigest, "evidence_ids": evidenceIDs(evidence), "release_mode": kernel.ReleaseModeNotRequired, "no_release_reason": noReleaseReason})
	releaseOpened, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.release-plan.create", kernel.SchemaVersion, kernel.AggregateReleasePlan, releasePlanID, service.policyAuthority, 0, releasePayload, []kernel.DagParent{{ParentEventID: approved.EventIDs[0], EdgeKind: kernel.EdgeResponse}}, evidence, "release-open-"+string(story.ID), kernel.AggregatePrecondition{Aggregate: storyRef, Expected: kernel.NewExpectedRevision(state.Revision + 2)})
	if err != nil {
		return "", err
	}
	releaseFinalPayload := mustJSON(map[string]any{"release_plan_id": releasePlanID, "expected_release_revision": uint64(1), "plan_digest": planDigest, "release_mode": kernel.ReleaseModeNotRequired, "terminal_status": kernel.ReleaseNotRequired, "reasons": []string{noReleaseReason}, "evidence_ids": evidenceIDs(evidence)})
	releaseFinalized, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.release-plan.finalize", kernel.SchemaVersion, kernel.AggregateReleasePlan, releasePlanID, service.policyAuthority, 1, releaseFinalPayload, []kernel.DagParent{{ParentEventID: releaseOpened.EventIDs[0], EdgeKind: kernel.EdgeCausal}}, evidence, "release-finalize-"+string(story.ID))
	if err != nil {
		return "", err
	}
	acceptancePayload := mustJSON(map[string]any{"acceptance_policy_revision": service.planning.PolicyRevision, "evidence_ids": evidenceIDs(evidence), "lifecycle_epoch": feature.LifecycleEpoch, "release_finalized_event_id": releaseFinalized.EventIDs[0], "release_mode": kernel.ReleaseModeNotRequired, "release_plan_id": releasePlanID, "release_plan_revision": uint64(2)})
	_, err = service.submitDeterministicCommand(ctx, feature, "tekroo.command.story.request-acceptance", kernel.SchemaVersion, kernel.AggregateStory, story.ID, principal, state.Revision+2, acceptancePayload, []kernel.DagParent{{ParentEventID: releaseFinalized.EventIDs[0], EdgeKind: kernel.EdgeResponse}}, evidence, "story-accept-"+string(story.ID), kernel.AggregatePrecondition{Aggregate: kernel.AggregateRef{Kind: kernel.AggregateReleasePlan, ID: releasePlanID}, Expected: kernel.NewExpectedRevision(2)})
	return releasePlanID, err
}
