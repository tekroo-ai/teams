package operationalruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// AcceptFeature turns the product-owner recommendation into canonical story
// completion and acceptance. The exact human who submitted the feature remains
// the release/acceptance authority; model output is evidence, never authority.
func (service *ProductionService) AcceptFeature(ctx context.Context, featureID kernel.UUIDv7, expectedRevision uint64, principal kernel.PrincipalRef, noReleaseReason string) (organization.FeatureRequest, error) {
	return service.AcceptFeatureWithRelease(ctx, featureID, principal, organization.FeatureAcceptanceInput{ExpectedRevision: expectedRevision, Mode: organization.FeatureAcceptanceNoRelease, NoReleaseReason: noReleaseReason})
}

func (service *ProductionService) AcceptFeatureWithRelease(ctx context.Context, featureID kernel.UUIDv7, principal kernel.PrincipalRef, input organization.FeatureAcceptanceInput) (organization.FeatureRequest, error) {
	if service == nil || service.Features == nil || principal.Kind != kernel.PrincipalHuman || !principal.Valid() {
		return organization.FeatureRequest{}, organization.ErrInvalidFeature
	}
	feature, found, err := service.ReadFeature(ctx, featureID)
	if err != nil || !found {
		return organization.FeatureRequest{}, errors.Join(organization.ErrFeatureNotFound, err)
	}
	if feature.Revision != input.ExpectedRevision || feature.Status != organization.FeatureAwaitingAcceptance || feature.Acceptance == nil || feature.Plan == nil || feature.SubmittedBy != principal || input.Validate(feature.Plan.Stories) != nil {
		return organization.FeatureRequest{}, organization.ErrFeatureRevisionConflict
	}
	codeReleases := make(map[kernel.UUIDv7]organization.StoryCodeRelease, len(input.CodeReleases))
	for _, release := range input.CodeReleases {
		codeReleases[release.StoryID] = release
	}
	acceptanceTask, invocation, validators, snapshot, err := service.featureAcceptanceEvidence(ctx, feature)
	if err != nil {
		return organization.FeatureRequest{}, err
	}
	releasePlanIDs := make([]kernel.UUIDv7, 0, len(feature.Plan.Stories))
	for _, story := range feature.Plan.Stories {
		var codeRelease *organization.StoryCodeRelease
		if input.Mode == organization.FeatureAcceptanceCode {
			value := codeReleases[story.ID]
			codeRelease = &value
		}
		releasePlanID, acceptErr := service.completeAndAcceptFeatureStory(ctx, feature, story, acceptanceTask, invocation, validators, snapshot, principal, input.NoReleaseReason, codeRelease)
		if acceptErr != nil {
			return organization.FeatureRequest{}, fmt.Errorf("accept story %s: %w", story.ID, acceptErr)
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
		if len(task.Validates) > 0 {
			validatorTasks[task.ID] = struct{}{}
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
	acceptanceOutput, err := service.Runtime.ReadExecutionOutput(ctx, *invocation.OutputDigest)
	if err != nil {
		return organization.PlannedTask{}, kernel.WorkInvocation{}, nil, kernel.Snapshot{}, err
	}
	acceptanceResult, err := parseStructuredValidationResult(acceptanceOutput)
	if err != nil || acceptanceResult.Outcome != "PASS" {
		return organization.PlannedTask{}, kernel.WorkInvocation{}, nil, kernel.Snapshot{}, errors.Join(organization.ErrInvalidFeature, err)
	}
	for taskID := range validatorTasks {
		validator, present := latestTaskInvocation(snapshot.WorkInvocations, taskID)
		if !present || validator.State != kernel.InvocationSucceeded || validator.OutputDigest == nil {
			return organization.PlannedTask{}, kernel.WorkInvocation{}, nil, kernel.Snapshot{}, organization.ErrInvalidFeature
		}
		validatorOutput, readErr := service.Runtime.ReadExecutionOutput(ctx, *validator.OutputDigest)
		validatorResult, parseErr := parseStructuredValidationResult(validatorOutput)
		if readErr != nil || parseErr != nil || validatorResult.Outcome != "PASS" || validatorResult.CandidateID != acceptanceResult.CandidateID {
			return organization.PlannedTask{}, kernel.WorkInvocation{}, nil, kernel.Snapshot{}, errors.Join(organization.ErrInvalidFeature, readErr, parseErr)
		}
		validators = append(validators, validator)
	}
	sort.Slice(validators, func(left, right int) bool { return validators[left].ID < validators[right].ID })
	return acceptanceTask, invocation, validators, snapshot, nil
}

func (service *ProductionService) completeAndAcceptFeatureStory(ctx context.Context, feature organization.FeatureRequest, story organization.PlannedStory, acceptanceTask organization.PlannedTask, acceptance kernel.WorkInvocation, validators []kernel.WorkInvocation, snapshot kernel.Snapshot, principal kernel.PrincipalRef, noReleaseReason string, codeRelease *organization.StoryCodeRelease) (kernel.UUIDv7, error) {
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
	evidence, err = appendTaskScopeEvidence(snapshot, evidence, acceptanceTask.ID)
	if err != nil {
		return "", err
	}
	profileSnapshot, found := snapshot.WorkProfiles[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: acceptanceTask.ID}]
	if !found || !profileSnapshot.Valid() {
		return "", organization.ErrInvalidFeature
	}
	candidateArtifact, err := service.candidateArtifactDigest(ctx, acceptanceTask.ID, snapshot)
	if err != nil {
		return "", err
	}
	identity, err := service.workExecutionIdentity(ctx, acceptance)
	if err != nil {
		return "", err
	}
	reviewDeadline, err := postExecutionReviewDeadline(profileSnapshot.Profile.Budgets.DeadlineAt, service.planningDeadline, allInvocations...)
	if err != nil {
		return "", err
	}
	evidence, reviewID, err := service.prepareCompletionReview(ctx, feature, storyRef, candidateArtifact, "story-completion-review-v3", []string{string(feature.ID), string(story.ID)}, snapshot, evidence)
	if err != nil {
		return "", err
	}
	evidenceSet, _ := json.Marshal(evidence)
	evidenceSetDigest := digestBytes(evidenceSet)
	branchID := "product-owner-acceptance"
	openPayload := mustJSON(map[string]any{
		"subject_kind": kernel.AggregateStory, "subject_id": story.ID, "lifecycle_epoch": state.LifecycleEpoch,
		"scope_revision": state.ScopeRevision, "criteria_revision": uint64(1), "evidence_set_digest": evidenceSetDigest,
		"branch_policy_revision": service.planning.PolicyRevision,
		"branches":               []map[string]any{{"branch_id": branchID, "validator": service.serviceAuthority, "resolution_owner_fqn": feature.ProductOwnerActor, "acceptance_criteria": story.AcceptanceCriteria, "input_evidence_ids": evidenceIDs(evidence), "deadline_at": reviewDeadline, "round_limit": uint64(1), "required_independence_dimensions": []kernel.IndependenceDimension{kernel.IndependencePrincipal, kernel.IndependenceMethod}, "required_method_ids": []string{"teams-product-acceptance-gate"}}},
		"join_rule":              "ALL_PASS", "partial_result_policy": "WAIT_ALL", "adjudication": map[string]any{"adjudicator": service.policyAuthority, "deadline_at": reviewDeadline, "round_limit": uint64(1)},
		"work_profile": profileSnapshot.Profile.Binding(), "candidate_artifact_digest": candidateArtifact,
		"implementer": identity, "verification_topology_digest": profileSnapshot.Profile.VerificationTopologyDigest, "variant_group_id": nil,
	})
	opened, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.completion-review.open", kernel.SchemaVersion, kernel.AggregateCompletionReview, reviewID, service.policyAuthority, 0, openPayload, []kernel.DagParent{{ParentEventID: acceptance.LastEventID, EdgeKind: kernel.EdgeCausal}}, evidence, fmt.Sprintf("story-review-open-v3-%s-%s-%s-policy-%d", story.ID, candidateArtifact, evidenceSetDigest, service.provenance.PolicyRevision), kernel.AggregatePrecondition{Aggregate: storyRef, Expected: kernel.NewExpectedRevision(state.Revision)})
	if err != nil {
		return "", err
	}
	comparison, _ := json.Marshal(map[string]any{"implementer": identity, "validator": service.serviceAuthority})
	resultPayload := mustJSON(map[string]any{
		"review_id": reviewID, "branch_id": branchID, "branch_policy_revision": service.planning.PolicyRevision, "source_role": "VALIDATOR", "round": uint64(1), "result": "PASS",
		"reasons": []string{"the product-owner recommendation and independently validated feature DAG satisfy the story criteria"}, "evidence_ids": evidenceIDs(evidence), "findings": []any{}, "supersedes_result_event_ids": []kernel.UUIDv7{}, "changed_condition_evidence_ids": []kernel.UUIDv7{},
		"candidate_artifact_digest": candidateArtifact, "independence_receipt": map[string]any{"proven_dimensions": []kernel.IndependenceDimension{kernel.IndependencePrincipal, kernel.IndependenceMethod}, "identity_comparison_digest": digestBytes(comparison), "method_ids": []string{"teams-product-acceptance-gate"}, "evidence_ids": evidenceIDs(evidence)},
	})
	recorded, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.completion-review.record-result", kernel.SchemaVersion, kernel.AggregateCompletionReview, reviewID, service.serviceAuthority, 1, resultPayload, []kernel.DagParent{{ParentEventID: opened.EventIDs[0], EdgeKind: kernel.EdgeResponse}}, evidence, "story-review-result-"+string(story.ID))
	if err != nil {
		return "", err
	}
	finalPayload := mustJSON(map[string]any{"review_id": reviewID, "subject_kind": kernel.AggregateStory, "subject_id": story.ID, "lifecycle_epoch": state.LifecycleEpoch, "branch_policy_revision": service.planning.PolicyRevision, "expected_review_revision": uint64(2), "terminal_status": "PASS", "result_event_ids": recorded.EventIDs, "evidence_ids": evidenceIDs(evidence), "scope_revision": state.ScopeRevision, "work_profile": profileSnapshot.Profile.Binding(), "candidate_artifact_digest": candidateArtifact, "verification_topology_digest": profileSnapshot.Profile.VerificationTopologyDigest})
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
	completionPayload := mustJSON(map[string]any{"artifact_digests": []kernel.Digest{candidateArtifact}, "branch_policy_revision": service.planning.PolicyRevision, "completion_review_id": reviewID, "completion_review_revision": uint64(2), "criteria_revision": uint64(1), "evidence_ids": evidenceIDs(evidence), "lifecycle_epoch": state.LifecycleEpoch, "unresolved_exceptions": []string{}, "validation_finalized_event_id": finalized.EventIDs[0]})
	completed, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.story.request-completion", kernel.SchemaVersion, kernel.AggregateStory, story.ID, principal, state.Revision, completionPayload, parents, evidence, "story-complete-"+string(story.ID), preconditions...)
	if err != nil {
		return "", err
	}
	approvalPayload := mustJSON(map[string]any{"story_id": story.ID, "lifecycle_epoch": state.LifecycleEpoch, "expected_story_revision": state.Revision + 1, "author": principal, "approval_revision": uint64(1), "release_policy_revision": service.planning.PolicyRevision, "reasons": []string{"the submitting human accepted the product-owner recommendation"}, "evidence_ids": evidenceIDs(evidence)})
	approved, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.story.approve-release", kernel.SchemaVersion, kernel.AggregateStory, story.ID, principal, state.Revision+1, approvalPayload, []kernel.DagParent{{ParentEventID: completed.EventIDs[0], EdgeKind: kernel.EdgeCausal}}, evidence, "story-release-approval-"+string(story.ID))
	if err != nil {
		return "", err
	}
	if codeRelease != nil {
		return service.executeFeatureCodeRelease(ctx, feature, story, state.Revision, state.LifecycleEpoch, principal, candidateArtifact, approved.EventIDs[0], evidence, *codeRelease)
	}
	releasePlanID := deterministicOperationalUUID("release-plan", string(feature.ID), string(story.ID), string(candidateArtifact))
	planDigest := digestBytes([]byte(string(feature.ID) + "\x00" + string(story.ID) + "\x00NO_RELEASE_REQUIRED\x00" + string(candidateArtifact)))
	releasePayload := mustJSON(map[string]any{"release_plan_id": releasePlanID, "story_id": story.ID, "story_lifecycle_epoch": state.LifecycleEpoch, "expected_story_revision": state.Revision + 2, "author": principal, "author_approval_event_id": approved.EventIDs[0], "author_approval_revision": state.Revision + 2, "release_policy_revision": service.planning.PolicyRevision, "plan_digest": planDigest, "evidence_ids": evidenceIDs(evidence), "release_mode": kernel.ReleaseModeNotRequired, "no_release_reason": noReleaseReason})
	releaseOpened, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.release-plan.create", kernel.SchemaVersion, kernel.AggregateReleasePlan, releasePlanID, service.policyAuthority, 0, releasePayload, []kernel.DagParent{{ParentEventID: approved.EventIDs[0], EdgeKind: kernel.EdgeResponse}}, evidence, "release-open-"+string(story.ID), kernel.AggregatePrecondition{Aggregate: storyRef, Expected: kernel.NewExpectedRevision(state.Revision + 2)})
	if err != nil {
		return "", err
	}
	releaseFinalPayload := mustJSON(map[string]any{"release_plan_id": releasePlanID, "expected_release_revision": uint64(1), "plan_digest": planDigest, "release_mode": kernel.ReleaseModeNotRequired, "terminal_status": kernel.ReleaseNotRequired, "reasons": []string{noReleaseReason}, "evidence_ids": evidenceIDs(evidence)})
	releaseFinalized, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.release-plan.finalize", kernel.SchemaVersion, kernel.AggregateReleasePlan, releasePlanID, service.policyAuthority, 1, releaseFinalPayload, []kernel.DagParent{{ParentEventID: releaseOpened.EventIDs[0], EdgeKind: kernel.EdgeCausal}}, evidence, "release-finalize-"+string(story.ID))
	if err != nil {
		return "", err
	}
	acceptancePayload := mustJSON(map[string]any{"acceptance_policy_revision": service.planning.PolicyRevision, "evidence_ids": evidenceIDs(evidence), "lifecycle_epoch": state.LifecycleEpoch, "release_finalized_event_id": releaseFinalized.EventIDs[0], "release_mode": kernel.ReleaseModeNotRequired, "release_plan_id": releasePlanID, "release_plan_revision": uint64(2)})
	_, err = service.submitDeterministicCommand(ctx, feature, "tekroo.command.story.request-acceptance", kernel.SchemaVersion, kernel.AggregateStory, story.ID, principal, state.Revision+2, acceptancePayload, []kernel.DagParent{{ParentEventID: releaseFinalized.EventIDs[0], EdgeKind: kernel.EdgeResponse}}, evidence, "story-accept-"+string(story.ID), kernel.AggregatePrecondition{Aggregate: kernel.AggregateRef{Kind: kernel.AggregateReleasePlan, ID: releasePlanID}, Expected: kernel.NewExpectedRevision(2)})
	return releasePlanID, err
}

func (service *ProductionService) executeFeatureCodeRelease(ctx context.Context, feature organization.FeatureRequest, story organization.PlannedStory, storyRevision, storyLifecycleEpoch uint64, principal kernel.PrincipalRef, artifactDigest kernel.Digest, approvalEventID kernel.UUIDv7, evidence []kernel.EvidenceRef, specification organization.StoryCodeRelease) (kernel.UUIDv7, error) {
	if service.Releases == nil || specification.StoryID != story.ID {
		return "", fmt.Errorf("code release is not configured or story identity mismatched: %w", organization.ErrInvalidFeature)
	}
	releasePlanID := deterministicOperationalUUID("release-plan", string(feature.ID), string(story.ID), string(artifactDigest))
	mergeID := deterministicOperationalUUID("release-merge", string(releasePlanID), specification.ChangeRef, specification.HeadCommit)
	planBytes, err := json.Marshal(specification)
	if err != nil {
		return "", err
	}
	planDigest := digestBytes(planBytes)
	requiredProfiles := []string{"contract-structure", "core-hermetic", "mongo-integration", "synthesized-merge"}
	createPayload := mustJSON(map[string]any{
		"release_plan_id": releasePlanID, "story_id": story.ID, "story_lifecycle_epoch": storyLifecycleEpoch, "expected_story_revision": storyRevision + 2,
		"author": principal, "author_approval_event_id": approvalEventID, "author_approval_revision": storyRevision + 2,
		"release_policy_revision": service.planning.PolicyRevision, "plan_digest": planDigest, "evidence_ids": evidenceIDs(evidence), "release_mode": kernel.ReleaseModeCode,
		"repository_url": specification.RepositoryURL, "base_ref": specification.BaseRef, "base_commit": specification.BaseCommit,
		"ordered_merges": []map[string]any{{"merge_id": mergeID, "change_ref": specification.ChangeRef, "head_commit": specification.HeadCommit, "role": "story"}},
		"merge_strategy": "FF_ONLY_ORDERED", "git_version": specification.GitVersion, "conflict_policy": "FAIL_NO_IMPROVISATION",
		"contract_manifest": kernel.ContractIdentity, "manifest_sha256": ManifestSHA256, "required_profiles": requiredProfiles,
		"expected_qualified_tree": specification.ExpectedQualifiedTree, "execution_round_limit": uint64(1),
	})
	opened, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.release-plan.create", kernel.SchemaVersion, kernel.AggregateReleasePlan, releasePlanID, service.policyAuthority, 0, createPayload, []kernel.DagParent{{ParentEventID: approvalEventID, EdgeKind: kernel.EdgeResponse}}, evidence, "release-open-"+string(story.ID), kernel.AggregatePrecondition{Aggregate: kernel.AggregateRef{Kind: kernel.AggregateStory, ID: story.ID}, Expected: kernel.NewExpectedRevision(storyRevision + 2)})
	if err != nil {
		return "", err
	}
	qualificationID := deterministicOperationalUUID("release-qualification", string(releasePlanID), string(planDigest))
	qualificationPayload := mustJSON(map[string]any{
		"release_plan_id": releasePlanID, "expected_release_revision": uint64(1), "plan_digest": planDigest, "qualification_id": qualificationID,
		"contract_manifest": kernel.ContractIdentity, "manifest_sha256": ManifestSHA256, "required_profiles": requiredProfiles,
		"gate_definition_digest": specification.GateDefinitionDigest, "toolchain_digest": specification.ToolchainDigest, "dependency_lock_digest": specification.DependencyLockDigest,
		"qualified_base_commit": specification.BaseCommit, "ordered_head_commits": []string{specification.HeadCommit}, "qualified_tree_digest": specification.ExpectedQualifiedTree,
		"artifact_digests": []kernel.Digest{specification.QualificationArtifactHash}, "evidence_ids": evidenceIDs(evidence),
	})
	_, err = service.submitDeterministicCommand(ctx, feature, "tekroo.command.release-plan.record-qualification", kernel.SchemaVersion, kernel.AggregateReleasePlan, releasePlanID, service.policyAuthority, 1, qualificationPayload, []kernel.DagParent{{ParentEventID: opened.EventIDs[0], EdgeKind: kernel.EdgeResponse}}, evidence, "release-qualify-"+string(story.ID))
	if err != nil {
		return "", err
	}
	plan, err := service.readReleasePlan(ctx, releasePlanID)
	if err != nil || plan.State != kernel.ReleaseQualified {
		return "", fmt.Errorf("read qualified release plan state=%s: %w", plan.State, errors.Join(organization.ErrInvalidFeature, err))
	}
	attemptID := deterministicOperationalUUID("release-attempt", string(releasePlanID), string(mergeID), "1")
	providerKey := "teams:" + string(releasePlanID) + ":" + string(mergeID) + ":1"
	result, err := service.Releases.ExecuteNext(ctx, application.ReleaseExecutionPlan{
		Plan: plan, AttemptID: attemptID, ProviderKey: providerKey,
		Request: application.ReleaseCommandTemplate{CommandID: deterministicOperationalUUID("release-request", string(attemptID)), Authority: service.policyAuthority, ExpectedPolicyRevision: service.provenance.PolicyRevision, IdempotencyKey: "release-request-" + string(attemptID), CorrelationID: releasePlanID, EvidenceRefs: evidence}, RequestProvenance: service.provenance,
		Result: application.ReleaseCommandTemplate{CommandID: deterministicOperationalUUID("release-result", string(attemptID)), Authority: service.serviceAuthority, ExpectedPolicyRevision: service.provenance.PolicyRevision, IdempotencyKey: "release-result-" + string(attemptID), CorrelationID: releasePlanID, EvidenceRefs: evidence}, ResultProvenance: service.provenance,
		ObservedAt: service.clock.Now().UTC(),
	})
	if err != nil || result.ResultReceipt == nil || result.ResultReceipt.OutcomeCode != kernel.OutcomeApplied || (result.Observation.Outcome != kernel.ReleaseOutcomeMerged && result.Observation.Outcome != kernel.ReleaseOutcomeAlreadyMerged) {
		return "", fmt.Errorf("execute release outcome=%s receipt=%v: %w", result.Observation.Outcome, result.ResultReceipt, errors.Join(organization.ErrInvalidFeature, err))
	}
	plan, err = service.readReleasePlan(ctx, releasePlanID)
	if err != nil || plan.State != kernel.ReleaseQualified || plan.NextMergeIndex != uint64(len(plan.OrderedMerges)) || plan.Qualification == nil || plan.ProviderTreeDigest != specification.ExpectedQualifiedTree {
		return "", fmt.Errorf("verify release state=%s provider_tree=%s expected_tree=%s: %w", plan.State, plan.ProviderTreeDigest, specification.ExpectedQualifiedTree, errors.Join(organization.ErrInvalidFeature, err))
	}
	resultEventIDs := make([]kernel.UUIDv7, 0, len(plan.Results))
	for _, releaseResult := range plan.Results {
		resultEventIDs = append(resultEventIDs, releaseResult.ResultEventID)
	}
	sort.Slice(resultEventIDs, func(left, right int) bool { return resultEventIDs[left] < resultEventIDs[right] })
	finalPayload := mustJSON(map[string]any{"release_plan_id": releasePlanID, "expected_release_revision": plan.Revision, "plan_digest": plan.PlanDigest, "release_mode": kernel.ReleaseModeCode, "terminal_status": kernel.ReleaseReadyForAcceptance, "qualification_event_id": plan.Qualification.EventID, "result_event_ids": resultEventIDs, "qualified_tree_digest": plan.Qualification.QualifiedTreeDigest, "provider_tree_digest": plan.ProviderTreeDigest, "reasons": []string{"all planned merges and the provider tree are verified"}, "evidence_ids": evidenceIDs(evidence)})
	finalized, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.release-plan.finalize", kernel.SchemaVersion, kernel.AggregateReleasePlan, releasePlanID, service.policyAuthority, plan.Revision, finalPayload, []kernel.DagParent{{ParentEventID: resultEventIDs[len(resultEventIDs)-1], EdgeKind: kernel.EdgeResponse}}, evidence, "release-finalize-"+string(story.ID))
	if err != nil {
		return "", err
	}
	acceptancePayload := mustJSON(map[string]any{"acceptance_policy_revision": service.planning.PolicyRevision, "evidence_ids": evidenceIDs(evidence), "lifecycle_epoch": storyLifecycleEpoch, "release_finalized_event_id": finalized.EventIDs[0], "release_mode": kernel.ReleaseModeCode, "release_plan_id": releasePlanID, "release_plan_revision": plan.Revision + 1, "qualified_tree_digest": plan.Qualification.QualifiedTreeDigest})
	_, err = service.submitDeterministicCommand(ctx, feature, "tekroo.command.story.request-acceptance", kernel.SchemaVersion, kernel.AggregateStory, story.ID, principal, storyRevision+2, acceptancePayload, []kernel.DagParent{{ParentEventID: finalized.EventIDs[0], EdgeKind: kernel.EdgeResponse}}, evidence, "story-accept-"+string(story.ID), kernel.AggregatePrecondition{Aggregate: kernel.AggregateRef{Kind: kernel.AggregateReleasePlan, ID: releasePlanID}, Expected: kernel.NewExpectedRevision(plan.Revision + 1)})
	return releasePlanID, err
}

func (service *ProductionService) readReleasePlan(ctx context.Context, releasePlanID kernel.UUIDv7) (kernel.ReleasePlanSnapshot, error) {
	ref := kernel.AggregateRef{Kind: kernel.AggregateReleasePlan, ID: releasePlanID}
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: ref})
	if err != nil {
		return kernel.ReleasePlanSnapshot{}, err
	}
	plan, found := snapshot.ReleasePlans[ref]
	if !found || !plan.Valid() {
		return kernel.ReleasePlanSnapshot{}, organization.ErrInvalidFeature
	}
	return plan, nil
}
