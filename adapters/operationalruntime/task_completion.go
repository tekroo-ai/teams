package operationalruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

type taskValidatorResult struct {
	Task       organization.PlannedTask
	Invocation kernel.WorkInvocation
	Result     structuredValidationResult
}

func (service *ProductionService) reconcileTaskCompletions(ctx context.Context, feature organization.FeatureRequest, plan organization.FeaturePlan, states map[kernel.UUIDv7]kernel.AggregateState, heads map[kernel.UUIDv7]kernel.UUIDv7, invocations map[kernel.UUIDv7]kernel.WorkInvocation, snapshot kernel.Snapshot) (bool, error) {
	promotionChanged, err := service.reconcilePromotionCompletion(ctx, feature, plan, states, heads, invocations, snapshot)
	if err != nil || promotionChanged {
		return promotionChanged, err
	}
	validatorsByTarget := make(map[kernel.UUIDv7][]taskValidatorResult)
	for _, task := range plan.Tasks {
		if len(task.Validates) == 0 {
			continue
		}
		invocation, found := invocations[task.ID]
		if !found || invocation.State != kernel.InvocationSucceeded || invocation.OutputDigest == nil {
			continue
		}
		profileSnapshot, profileFound := snapshot.WorkProfiles[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}]
		criteria, marshalErr := json.Marshal(task.AcceptanceCriteria)
		if marshalErr != nil || !profileFound || !profileSnapshot.Valid() {
			return false, errors.Join(organization.ErrInvalidFeature, marshalErr)
		}
		validatedTargets := make(map[kernel.UUIDv7]struct{}, len(task.Validates))
		for _, targetID := range task.Validates {
			validatedTargets[targetID] = struct{}{}
		}
		conditionDigests := make([]kernel.Digest, 0, len(task.Validates))
		for _, dependencyID := range task.DependsOn {
			if _, validates := validatedTargets[dependencyID]; !validates {
				continue
			}
			candidate, present := invocations[dependencyID]
			if !present || candidate.State != kernel.InvocationSucceeded || candidate.OutputDigest == nil {
				conditionDigests = nil
				break
			}
			conditionDigests = append(conditionDigests, *candidate.OutputDigest)
		}
		if len(conditionDigests) != len(task.Validates) {
			continue
		}
		expectedCondition, digestErr := taskInvocationConditionDigest(profileSnapshot.Profile.ProfileDigest, digestBytes(criteria), conditionDigests)
		if digestErr != nil {
			return false, digestErr
		}
		if !validatorConditionMatches(snapshot, task.ID, invocation, profileSnapshot.Profile.ProfileDigest, digestBytes(criteria), conditionDigests, expectedCondition) {
			continue
		}
		output, err := service.Runtime.ReadExecutionOutput(ctx, *invocation.OutputDigest)
		if err != nil {
			return false, fmt.Errorf("read validator %s output: %w", task.ID, err)
		}
		result, err := parseStructuredValidationResult(output)
		if err != nil {
			authorized, retryErr := service.handleInvalidStructuredTaskOutput(ctx, feature, task, states[task.ID], heads[task.ID], invocation, snapshot, conditionDigests)
			if retryErr != nil {
				return false, fmt.Errorf("validator %s invalid-output handling: %w", task.ID, retryErr)
			}
			return authorized, nil
		}
		for _, targetID := range task.Validates {
			validatorsByTarget[targetID] = append(validatorsByTarget[targetID], taskValidatorResult{Task: task, Invocation: invocation, Result: result})
		}
	}

	changed := false
	for _, target := range plan.Tasks {
		validators := validatorsByTarget[target.ID]
		if len(validators) == 0 || states[target.ID].Phase != kernel.PhaseActive || states[target.ID].Condition != kernel.ConditionRunnable {
			continue
		}
		expected := 0
		for _, candidate := range plan.Tasks {
			for _, validates := range candidate.Validates {
				if validates == target.ID {
					expected++
				}
			}
		}
		if len(validators) != expected {
			continue
		}
		sort.Slice(validators, func(left, right int) bool { return validators[left].Task.ID < validators[right].Task.ID })
		implementer, found := invocations[target.ID]
		if !found || implementer.State != kernel.InvocationSucceeded || implementer.OutputDigest == nil {
			continue
		}
		status, err := service.finalizeTaskReview(ctx, feature, target, states[target.ID], heads[target.ID], implementer, validators, snapshot)
		if err != nil {
			return false, err
		}
		if status != "PASS" {
			authorized, repairErr := service.authorizeRepairAfterFailedReview(ctx, feature, target, states[target.ID], heads[target.ID], implementer, validators, snapshot)
			if repairErr != nil {
				return false, repairErr
			}
			changed = changed || authorized
			continue
		}
		targetState := states[target.ID]
		targetState.Phase = kernel.PhaseCompleted
		states[target.ID] = targetState
		changed = true
		for _, validator := range validators {
			validatorState := states[validator.Task.ID]
			if validatorState.Phase != kernel.PhaseActive {
				continue
			}
			if err := service.completeEvidenceTask(ctx, feature, validator.Task, validatorState, heads[validator.Task.ID], validator.Invocation, snapshot); err != nil {
				return false, err
			}
			validatorState.Phase = kernel.PhaseCompleted
			states[validator.Task.ID] = validatorState
			changed = true
		}
	}
	return changed, nil
}

func (service *ProductionService) reconcilePromotionCompletion(ctx context.Context, feature organization.FeatureRequest, plan organization.FeaturePlan, states map[kernel.UUIDv7]kernel.AggregateState, heads map[kernel.UUIDv7]kernel.UUIDv7, invocations map[kernel.UUIDv7]kernel.WorkInvocation, snapshot kernel.Snapshot) (bool, error) {
	for _, task := range plan.Tasks {
		if task.Purpose != kernel.PurposePromotion || len(task.Validates) != 0 || states[task.ID].Phase != kernel.PhaseActive || states[task.ID].Condition != kernel.ConditionRunnable {
			continue
		}
		invocation, found := invocations[task.ID]
		if !found || invocation.State != kernel.InvocationSucceeded || invocation.OutputDigest == nil {
			continue
		}
		output, err := service.Runtime.ReadExecutionOutput(ctx, *invocation.OutputDigest)
		if err != nil {
			return false, fmt.Errorf("read promotion %s output: %w", task.ID, err)
		}
		result, err := parseStructuredValidationResult(output)
		if err != nil {
			return service.handleInvalidStructuredTaskOutput(ctx, feature, task, states[task.ID], heads[task.ID], invocation, snapshot, nil)
		}
		if result.Outcome != "PASS" {
			return service.blockStructuredDecisionTask(ctx, feature, task, states[task.ID], invocation, snapshot, "product acceptance did not pass: "+strings.Join(result.Reasons, "; "), "promotion-not-pass")
		}
		if err := service.completeEvidenceTask(ctx, feature, task, states[task.ID], heads[task.ID], invocation, snapshot); err != nil {
			return false, err
		}
		promotionState := states[task.ID]
		promotionState.Phase = kernel.PhaseCompleted
		states[task.ID] = promotionState
		return true, nil
	}
	return false, nil
}

func (service *ProductionService) handleInvalidStructuredTaskOutput(ctx context.Context, feature organization.FeatureRequest, task organization.PlannedTask, state kernel.AggregateState, head kernel.UUIDv7, invocation kernel.WorkInvocation, snapshot kernel.Snapshot, conditionDigests []kernel.Digest) (bool, error) {
	if state.Phase != kernel.PhaseActive || invocation.State != kernel.InvocationSucceeded || invocation.OutputDigest == nil {
		return false, organization.ErrInvalidFeature
	}
	if invocation.AttemptOrdinal < uint64(task.AttemptLimit) {
		profile, configured := service.profilesByModel[task.ModelProfile]
		profileSnapshot, profileFound := snapshot.WorkProfiles[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}]
		budget, budgetFound := snapshot.WorkBudgetAccounts[kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: feature.BudgetAccountID}]
		owner, active, ownerErr := service.RoleHost.Status(ctx, task.Owner)
		if ownerErr != nil {
			return false, ownerErr
		}
		if !configured || !profileFound || !profileSnapshot.Valid() || !budgetFound || !budget.Valid() {
			return false, organization.ErrInvalidFeature
		}
		if !active || owner.Status != organization.RoleIdle {
			return false, nil
		}
		workspace, workspaceFound := service.workspacesByID[owner.WorkspaceID]
		if !workspaceFound {
			return false, organization.ErrInvalidFeature
		}
		tracked := &trackedTask{plan: task, revision: state.Revision, last: head, profile: profileSnapshot.Profile, owner: owner}
		retryConditions := append(append([]kernel.Digest(nil), conditionDigests...), *invocation.OutputDigest)
		if err := service.authorizeTaskInvocationWithCondition(ctx, feature, tracked, profile, workspace, budget.Revision, task.Purpose, invocation.AttemptOrdinal+1, nil, retryConditions); err != nil {
			return false, err
		}
		return true, nil
	}

	return service.blockStructuredDecisionTask(ctx, feature, task, state, invocation, snapshot, "task exhausted its bounded attempts without a valid structured result", "invalid-structured-output-exhausted")
}

func (service *ProductionService) blockStructuredDecisionTask(ctx context.Context, feature organization.FeatureRequest, task organization.PlannedTask, state kernel.AggregateState, invocation kernel.WorkInvocation, snapshot kernel.Snapshot, reason, key string) (bool, error) {
	evidence, err := evidenceForInvocations(snapshot, invocation)
	if err != nil {
		return false, err
	}
	payload, err := json.Marshal(map[string]any{"blocker_refs": []string{"teams://work-invocation/" + string(invocation.ID)}, "reason": reason, "review_policy": "operator-or-product-owner-must-amend-scope-or-cancel"})
	if err != nil {
		return false, err
	}
	_, err = service.submitDeterministicActorTargetCommand(ctx, feature, "tekroo.command.work.block", kernel.AggregateTask, task.ID, service.policyAuthority, invocation.ActorFQN, invocation.Execution, state.Revision, payload, []kernel.DagParent{{ParentEventID: invocation.LastEventID, EdgeKind: kernel.EdgeResponse}}, evidence, key+"-"+string(task.ID)+"-"+string(*invocation.OutputDigest))
	return err == nil, err
}

func validatorConditionMatches(snapshot kernel.Snapshot, taskID kernel.UUIDv7, invocation kernel.WorkInvocation, profileDigest, criteriaDigest kernel.Digest, baseConditions []kernel.Digest, baseDigest kernel.Digest) bool {
	if invocation.ConditionDigest == baseDigest {
		return true
	}
	for _, prior := range snapshot.WorkInvocations {
		if prior.TaskID != taskID || prior.State != kernel.InvocationSucceeded || prior.OutputDigest == nil || prior.AttemptOrdinal+1 != invocation.AttemptOrdinal {
			continue
		}
		conditions := append(append([]kernel.Digest(nil), baseConditions...), *prior.OutputDigest)
		digest, err := taskInvocationConditionDigest(profileDigest, criteriaDigest, conditions)
		if err == nil && invocation.ConditionDigest == digest {
			return true
		}
	}
	return false
}

func (service *ProductionService) authorizeRepairAfterFailedReview(ctx context.Context, feature organization.FeatureRequest, target organization.PlannedTask, state kernel.AggregateState, head kernel.UUIDv7, implementer kernel.WorkInvocation, validators []taskValidatorResult, snapshot kernel.Snapshot) (bool, error) {
	if implementer.OutputDigest == nil || target.ReviewRoundLimit == 0 {
		return false, organization.ErrInvalidFeature
	}
	conditionParts := []kernel.Digest{*implementer.OutputDigest}
	for _, validator := range validators {
		if validator.Invocation.OutputDigest == nil {
			return false, organization.ErrInvalidFeature
		}
		conditionParts = append(conditionParts, *validator.Invocation.OutputDigest)
	}
	profileSnapshot, profileFound := snapshot.WorkProfiles[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: target.ID}]
	criteria, marshalErr := json.Marshal(target.AcceptanceCriteria)
	if marshalErr != nil || !profileFound || !profileSnapshot.Valid() {
		return false, errors.Join(organization.ErrInvalidFeature, marshalErr)
	}
	conditionDigest, digestErr := taskInvocationConditionDigest(profileSnapshot.Profile.ProfileDigest, digestBytes(criteria), conditionParts)
	if digestErr != nil {
		return false, digestErr
	}
	nextRound := uint64(1)
	for _, invocation := range snapshot.WorkInvocations {
		if invocation.TaskID != target.ID || invocation.Purpose != kernel.PurposeRepair {
			continue
		}
		if invocation.ConditionDigest == conditionDigest {
			return false, nil
		}
		if invocation.AttemptOrdinal >= nextRound {
			nextRound = invocation.AttemptOrdinal + 1
		}
	}
	if nextRound > uint64(target.ReviewRoundLimit) {
		reviewID := deterministicOperationalUUID("completion-review", string(feature.ID), string(target.ID), string(*implementer.OutputDigest))
		_, reviewHead, reviewFound, reviewErr := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateCompletionReview, ID: reviewID})
		owner, active, ownerErr := service.RoleHost.Status(ctx, target.Owner)
		evidence, evidenceErr := evidenceForInvocations(snapshot, append([]kernel.WorkInvocation{implementer}, validatorInvocations(validators)...)...)
		if reviewErr != nil || !reviewFound || ownerErr != nil || !active || evidenceErr != nil {
			return false, errors.Join(organization.ErrInvalidFeature, reviewErr, ownerErr, evidenceErr)
		}
		payload, _ := json.Marshal(map[string]any{"blocker_refs": []string{"teams://completion-review/" + string(reviewID)}, "reason": "independent validation still fails after the authorized repair rounds", "review_policy": "operator-or-product-owner-must-amend-scope-or-cancel"})
		_, err := service.submitDeterministicActorTargetCommand(ctx, feature, "tekroo.command.work.block", kernel.AggregateTask, target.ID, service.policyAuthority, owner.ActorFQN, owner.Execution, state.Revision, payload, []kernel.DagParent{{ParentEventID: reviewHead, EdgeKind: kernel.EdgeResponse}}, evidence, "repair-exhausted-"+string(target.ID)+"-"+string(*implementer.OutputDigest))
		return err == nil, err
	}
	profileConfig, configured := service.profilesByModel[target.ModelProfile]
	owner, active, ownerErr := service.RoleHost.Status(ctx, target.Owner)
	workspace, workspaceFound := service.workspacesByID[owner.WorkspaceID]
	budget := snapshot.WorkBudgetAccounts[kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: feature.BudgetAccountID}]
	if !configured || ownerErr != nil || !active || owner.Status != organization.RoleIdle || !workspaceFound || !budget.Valid() {
		return false, errors.Join(organization.ErrRoleNotRunning, ownerErr)
	}
	tracked := &trackedTask{plan: target, revision: state.Revision, last: head, profile: profileSnapshot.Profile, owner: owner}
	if err := service.authorizeTaskInvocationWithCondition(ctx, feature, tracked, profileConfig, workspace, budget.Revision, kernel.PurposeRepair, nextRound, nil, conditionParts); err != nil {
		return false, err
	}
	return true, nil
}

func (service *ProductionService) finalizeTaskReview(ctx context.Context, feature organization.FeatureRequest, target organization.PlannedTask, state kernel.AggregateState, head kernel.UUIDv7, implementer kernel.WorkInvocation, validators []taskValidatorResult, snapshot kernel.Snapshot) (string, error) {
	profileSnapshot, found := snapshot.WorkProfiles[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: target.ID}]
	if !found || !profileSnapshot.Valid() || implementer.OutputDigest == nil {
		return "", organization.ErrInvalidFeature
	}
	evidence, err := evidenceForInvocations(snapshot, append([]kernel.WorkInvocation{implementer}, validatorInvocations(validators)...)...)
	if err != nil {
		return "", err
	}
	reviewID := deterministicOperationalUUID("completion-review", string(feature.ID), string(target.ID), string(*implementer.OutputDigest))
	branchSpecs := make([]map[string]any, len(validators))
	parents := []kernel.DagParent{{ParentEventID: implementer.LastEventID, EdgeKind: kernel.EdgeCausal}}
	for index, validator := range validators {
		branchID := "validator-" + string(validator.Task.ID)
		branchSpecs[index] = map[string]any{
			"branch_id": branchID, "validator": kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: string(validator.Invocation.ActorFQN)},
			"resolution_owner_fqn": target.Owner, "acceptance_criteria": target.AcceptanceCriteria,
			"input_evidence_ids": evidenceIDs(evidence), "deadline_at": profileSnapshot.Profile.Budgets.DeadlineAt,
			"round_limit": uint64(target.ReviewRoundLimit), "required_independence_dimensions": profileSnapshot.Profile.RequiredIndependenceDimensions,
			"required_method_ids": []string{"openhands-independent-validation"},
		}
		parents = append(parents, kernel.DagParent{ParentEventID: validator.Invocation.LastEventID, EdgeKind: kernel.EdgeResponse})
	}
	sort.Slice(parents, func(left, right int) bool {
		if parents[left].EdgeKind != parents[right].EdgeKind {
			return parents[left].EdgeKind < parents[right].EdgeKind
		}
		return parents[left].ParentEventID < parents[right].ParentEventID
	})
	implementerIdentity, err := service.workExecutionIdentity(implementer)
	if err != nil {
		return "", err
	}
	evidenceSet, _ := json.Marshal(evidence)
	openPayload, _ := json.Marshal(map[string]any{
		"subject_kind": kernel.AggregateTask, "subject_id": target.ID, "lifecycle_epoch": feature.LifecycleEpoch,
		"scope_revision": feature.ScopeRevision, "criteria_revision": uint64(1), "evidence_set_digest": digestBytes(evidenceSet),
		"branch_policy_revision": service.planning.PolicyRevision, "branches": branchSpecs, "join_rule": "ALL_PASS", "partial_result_policy": "WAIT_ALL",
		"adjudication": map[string]any{"adjudicator": service.policyAuthority, "deadline_at": profileSnapshot.Profile.Budgets.DeadlineAt, "round_limit": uint64(target.ReviewRoundLimit)},
		"work_profile": profileSnapshot.Profile.Binding(), "candidate_artifact_digest": *implementer.OutputDigest,
		"implementer": implementerIdentity, "verification_topology_digest": profileSnapshot.Profile.VerificationTopologyDigest, "variant_group_id": nil,
	})
	opened, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.completion-review.open", kernel.SchemaVersion, kernel.AggregateCompletionReview, reviewID, service.policyAuthority, 0, openPayload, parents, evidence, "review-open-"+string(target.ID)+"-"+string(*implementer.OutputDigest), kernel.AggregatePrecondition{Aggregate: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: target.ID}, Expected: kernel.NewExpectedRevision(state.Revision)})
	if err != nil {
		return "", err
	}
	priorEvent := opened.EventIDs[0]
	resultEvents := make([]kernel.UUIDv7, 0, len(validators))
	terminalStatus := "PASS"
	for index, validator := range validators {
		branchID := "validator-" + string(validator.Task.ID)
		identity, identityErr := service.workExecutionIdentity(validator.Invocation)
		if identityErr != nil {
			return "", identityErr
		}
		comparison, _ := json.Marshal(map[string]any{"implementer": implementerIdentity, "validator": identity})
		resultPayload, _ := json.Marshal(map[string]any{
			"review_id": reviewID, "branch_id": branchID, "branch_policy_revision": service.planning.PolicyRevision,
			"source_role": "VALIDATOR", "round": uint64(1), "result": validator.Result.Outcome, "reasons": validator.Result.Reasons,
			"evidence_ids": evidenceIDs(evidence), "findings": reviewFindings(feature, target.ID, branchID, validator.Result, evidenceIDs(evidence)),
			"supersedes_result_event_ids": []kernel.UUIDv7{}, "changed_condition_evidence_ids": []kernel.UUIDv7{},
			"candidate_artifact_digest": *implementer.OutputDigest,
			"independence_receipt":      map[string]any{"proven_dimensions": profileSnapshot.Profile.RequiredIndependenceDimensions, "identity_comparison_digest": digestBytes(comparison), "method_ids": []string{"openhands-independent-validation"}, "evidence_ids": evidenceIDs(evidence)},
		})
		recorded, recordErr := service.submitDeterministicReviewResult(ctx, feature, reviewID, uint64(index+1), validator.Invocation, resultPayload, priorEvent, evidence, "review-result-"+string(target.ID)+"-"+string(validator.Task.ID)+"-"+string(*implementer.OutputDigest))
		if recordErr != nil {
			return "", recordErr
		}
		priorEvent = recorded.EventIDs[0]
		resultEvents = append(resultEvents, priorEvent)
		if validator.Result.Outcome != "PASS" && terminalStatus == "PASS" {
			terminalStatus = validator.Result.Outcome
		}
	}
	finalPayload, _ := json.Marshal(map[string]any{
		"review_id": reviewID, "subject_kind": kernel.AggregateTask, "subject_id": target.ID, "lifecycle_epoch": feature.LifecycleEpoch,
		"branch_policy_revision": service.planning.PolicyRevision, "expected_review_revision": uint64(len(validators) + 1), "terminal_status": terminalStatus,
		"result_event_ids": resultEvents, "evidence_ids": evidenceIDs(evidence), "scope_revision": feature.ScopeRevision,
		"work_profile": profileSnapshot.Profile.Binding(), "candidate_artifact_digest": *implementer.OutputDigest,
		"verification_topology_digest": profileSnapshot.Profile.VerificationTopologyDigest,
	})
	finalized, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.completion-review.finalize", kernel.SchemaVersion, kernel.AggregateCompletionReview, reviewID, service.policyAuthority, uint64(len(validators)+1), finalPayload, []kernel.DagParent{{ParentEventID: priorEvent, EdgeKind: kernel.EdgeResponse}}, evidence, "review-finalize-"+string(target.ID)+"-"+string(*implementer.OutputDigest))
	if err != nil || terminalStatus != "PASS" {
		return terminalStatus, err
	}
	owner, active, err := service.RoleHost.Status(ctx, target.Owner)
	if err != nil || !active {
		return "", errors.Join(organization.ErrRoleNotRunning, err)
	}
	completionPayload, _ := json.Marshal(map[string]any{
		"lifecycle_epoch": feature.LifecycleEpoch, "criteria_revision": uint64(1), "evidence_ids": evidenceIDs(evidence),
		"artifact_digests": []kernel.Digest{*implementer.OutputDigest}, "unresolved_exceptions": []string{},
		"completion_review_id": reviewID, "completion_review_revision": uint64(len(validators) + 1),
		"branch_policy_revision": service.planning.PolicyRevision, "validation_finalized_event_id": finalized.EventIDs[0], "owner_fqn": target.Owner,
	})
	_, err = service.submitDeterministicActorTargetCommand(ctx, feature, "tekroo.command.task.request-completion", kernel.AggregateTask, target.ID, kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: string(target.Owner)}, owner.ActorFQN, owner.Execution, state.Revision, completionPayload, []kernel.DagParent{{ParentEventID: finalized.EventIDs[0], EdgeKind: kernel.EdgeCausal}}, evidence, "task-complete-"+string(target.ID)+"-"+string(*implementer.OutputDigest))
	return terminalStatus, err
}

func (service *ProductionService) completeEvidenceTask(ctx context.Context, feature organization.FeatureRequest, task organization.PlannedTask, state kernel.AggregateState, head kernel.UUIDv7, invocation kernel.WorkInvocation, snapshot kernel.Snapshot) error {
	if invocation.OutputDigest == nil {
		return organization.ErrInvalidFeature
	}
	profileSnapshot, found := snapshot.WorkProfiles[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}]
	if !found || !profileSnapshot.Valid() {
		return organization.ErrInvalidFeature
	}
	evidence, err := evidenceForInvocations(snapshot, invocation)
	if err != nil {
		return err
	}
	reviewID := deterministicOperationalUUID("completion-review", string(feature.ID), string(task.ID), string(*invocation.OutputDigest))
	identity, err := service.workExecutionIdentity(invocation)
	if err != nil {
		return err
	}
	evidenceSet, _ := json.Marshal(evidence)
	branchID := "deterministic-execution-evidence"
	openPayload, _ := json.Marshal(map[string]any{
		"subject_kind": kernel.AggregateTask, "subject_id": task.ID, "lifecycle_epoch": feature.LifecycleEpoch, "scope_revision": feature.ScopeRevision,
		"criteria_revision": uint64(1), "evidence_set_digest": digestBytes(evidenceSet), "branch_policy_revision": service.planning.PolicyRevision,
		"branches":  []map[string]any{{"branch_id": branchID, "validator": service.serviceAuthority, "resolution_owner_fqn": task.Owner, "acceptance_criteria": task.AcceptanceCriteria, "input_evidence_ids": evidenceIDs(evidence), "deadline_at": profileSnapshot.Profile.Budgets.DeadlineAt, "round_limit": uint64(1), "required_independence_dimensions": []kernel.IndependenceDimension{kernel.IndependencePrincipal, kernel.IndependenceMethod}, "required_method_ids": []string{"teams-terminal-evidence-check"}}},
		"join_rule": "ALL_PASS", "partial_result_policy": "WAIT_ALL", "adjudication": map[string]any{"adjudicator": service.policyAuthority, "deadline_at": profileSnapshot.Profile.Budgets.DeadlineAt, "round_limit": uint64(1)},
		"work_profile": profileSnapshot.Profile.Binding(), "candidate_artifact_digest": *invocation.OutputDigest, "implementer": identity,
		"verification_topology_digest": profileSnapshot.Profile.VerificationTopologyDigest, "variant_group_id": nil,
	})
	opened, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.completion-review.open", kernel.SchemaVersion, kernel.AggregateCompletionReview, reviewID, service.policyAuthority, 0, openPayload, []kernel.DagParent{{ParentEventID: invocation.LastEventID, EdgeKind: kernel.EdgeCausal}}, evidence, "evidence-review-open-"+string(task.ID)+"-"+string(*invocation.OutputDigest), kernel.AggregatePrecondition{Aggregate: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}, Expected: kernel.NewExpectedRevision(state.Revision)})
	if err != nil {
		return err
	}
	comparison, _ := json.Marshal(map[string]any{"implementer": identity, "validator": service.serviceAuthority})
	resultPayload, _ := json.Marshal(map[string]any{
		"review_id": reviewID, "branch_id": branchID, "branch_policy_revision": service.planning.PolicyRevision, "source_role": "VALIDATOR", "round": uint64(1), "result": "PASS",
		"reasons": []string{"authorized execution reached a retained terminal result"}, "evidence_ids": evidenceIDs(evidence), "findings": []any{}, "supersedes_result_event_ids": []kernel.UUIDv7{}, "changed_condition_evidence_ids": []kernel.UUIDv7{},
		"candidate_artifact_digest": *invocation.OutputDigest, "independence_receipt": map[string]any{"proven_dimensions": []kernel.IndependenceDimension{kernel.IndependencePrincipal, kernel.IndependenceMethod}, "identity_comparison_digest": digestBytes(comparison), "method_ids": []string{"teams-terminal-evidence-check"}, "evidence_ids": evidenceIDs(evidence)},
	})
	recorded, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.completion-review.record-result", kernel.SchemaVersion, kernel.AggregateCompletionReview, reviewID, service.serviceAuthority, 1, resultPayload, []kernel.DagParent{{ParentEventID: opened.EventIDs[0], EdgeKind: kernel.EdgeResponse}}, evidence, "evidence-review-result-"+string(task.ID)+"-"+string(*invocation.OutputDigest))
	if err != nil {
		return err
	}
	finalPayload, _ := json.Marshal(map[string]any{"review_id": reviewID, "subject_kind": kernel.AggregateTask, "subject_id": task.ID, "lifecycle_epoch": feature.LifecycleEpoch, "branch_policy_revision": service.planning.PolicyRevision, "expected_review_revision": uint64(2), "terminal_status": "PASS", "result_event_ids": recorded.EventIDs, "evidence_ids": evidenceIDs(evidence), "scope_revision": feature.ScopeRevision, "work_profile": profileSnapshot.Profile.Binding(), "candidate_artifact_digest": *invocation.OutputDigest, "verification_topology_digest": profileSnapshot.Profile.VerificationTopologyDigest})
	finalized, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.completion-review.finalize", kernel.SchemaVersion, kernel.AggregateCompletionReview, reviewID, service.policyAuthority, 2, finalPayload, []kernel.DagParent{{ParentEventID: recorded.EventIDs[0], EdgeKind: kernel.EdgeResponse}}, evidence, "evidence-review-finalize-"+string(task.ID)+"-"+string(*invocation.OutputDigest))
	if err != nil {
		return err
	}
	owner, active, err := service.RoleHost.Status(ctx, task.Owner)
	if err != nil || !active {
		return errors.Join(organization.ErrRoleNotRunning, err)
	}
	completionPayload, _ := json.Marshal(map[string]any{"lifecycle_epoch": feature.LifecycleEpoch, "criteria_revision": uint64(1), "evidence_ids": evidenceIDs(evidence), "artifact_digests": []kernel.Digest{*invocation.OutputDigest}, "unresolved_exceptions": []string{}, "completion_review_id": reviewID, "completion_review_revision": uint64(2), "branch_policy_revision": service.planning.PolicyRevision, "validation_finalized_event_id": finalized.EventIDs[0], "owner_fqn": task.Owner})
	_, err = service.submitDeterministicActorTargetCommand(ctx, feature, "tekroo.command.task.request-completion", kernel.AggregateTask, task.ID, kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: string(task.Owner)}, owner.ActorFQN, owner.Execution, state.Revision, completionPayload, []kernel.DagParent{{ParentEventID: finalized.EventIDs[0], EdgeKind: kernel.EdgeCausal}}, evidence, "evidence-task-complete-"+string(task.ID)+"-"+string(*invocation.OutputDigest))
	_ = head
	return err
}

func (service *ProductionService) submitDeterministicReviewResult(ctx context.Context, feature organization.FeatureRequest, reviewID kernel.UUIDv7, revision uint64, validator kernel.WorkInvocation, payload []byte, parent kernel.UUIDv7, evidence []kernel.EvidenceRef, key string) (kernel.CommandReceipt, error) {
	return service.submitDeterministicActorTargetCommand(ctx, feature, "tekroo.command.completion-review.record-result", kernel.AggregateCompletionReview, reviewID, kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: string(validator.ActorFQN)}, validator.ActorFQN, validator.Execution, revision, payload, []kernel.DagParent{{ParentEventID: parent, EdgeKind: kernel.EdgeResponse}}, evidence, key)
}

func (service *ProductionService) submitDeterministicActorTargetCommand(ctx context.Context, feature organization.FeatureRequest, commandType string, kind kernel.AggregateKind, id kernel.UUIDv7, authority kernel.PrincipalRef, actor kernel.ActorFQN, execution kernel.ExecutionTuple, revision uint64, payload []byte, parents []kernel.DagParent, evidence []kernel.EvidenceRef, key string) (kernel.CommandReceipt, error) {
	command := kernel.KernelCommand{ContractManifest: kernel.ContractIdentity, CommandID: deterministicOperationalUUID("command", string(feature.ID), commandType, string(id), key), CommandType: commandType, CommandVersion: kernel.SchemaVersion, Target: kernel.AggregateRef{Kind: kind, ID: id}, Authority: authority, ActorFQN: &actor, Execution: &execution, ExpectedRevision: kernel.NewExpectedRevision(revision), ExpectedLifecycleEpoch: expectedLifecycleEpoch(kind, revision, feature.LifecycleEpoch), Preconditions: []kernel.AggregatePrecondition{}, ExpectedPolicyRevision: service.provenance.PolicyRevision, ExpectedCatalogueRevision: kernel.CatalogueRevision, IdempotencyKey: "feature:" + string(feature.ID) + ":" + key, CorrelationID: feature.ID, Causation: append([]kernel.DagParent(nil), parents...), Payload: append([]byte(nil), payload...), EvidenceRefs: append([]kernel.EvidenceRef(nil), evidence...)}
	receipt, err := service.Submit(ctx, command)
	if err != nil {
		return receipt, fmt.Errorf("%s: %w", commandType, err)
	}
	if receipt.OutcomeCode != kernel.OutcomeApplied && receipt.OutcomeCode != kernel.OutcomeNoChange {
		return receipt, fmt.Errorf("%s rejected: %s", commandType, receipt.ReasonCode)
	}
	return receipt, nil
}

func (service *ProductionService) workExecutionIdentity(invocation kernel.WorkInvocation) (kernel.WorkExecutionIdentity, error) {
	workspace, found := service.workspacesByID[invocation.WorkspaceID]
	if !found || invocation.RequestDigest == nil {
		return kernel.WorkExecutionIdentity{}, organization.ErrInvalidFeature
	}
	encoded, err := json.Marshal(workspace)
	if err != nil {
		return kernel.WorkExecutionIdentity{}, err
	}
	return kernel.WorkExecutionIdentity{Principal: kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: string(invocation.ActorFQN)}, ActorFQN: invocation.ActorFQN, ExecutionID: invocation.Execution.ExecutionID, FencingEpoch: invocation.Execution.FencingEpoch, ModelProfileDigest: invocation.ModelProfileDigest, WorkspaceDigest: digestBytes(encoded), ContextDigest: *invocation.RequestDigest}, nil
}

func validatorInvocations(values []taskValidatorResult) []kernel.WorkInvocation {
	result := make([]kernel.WorkInvocation, len(values))
	for index := range values {
		result[index] = values[index].Invocation
	}
	return result
}

func evidenceForInvocations(snapshot kernel.Snapshot, invocations ...kernel.WorkInvocation) ([]kernel.EvidenceRef, error) {
	byID := make(map[kernel.UUIDv7]kernel.EvidenceRef)
	for _, invocation := range invocations {
		for _, id := range invocation.TerminalEvidenceIDs {
			metadata, found := snapshot.Evidence[id]
			if !found || !metadata.Available || !metadata.SHA256.Valid() {
				return nil, errors.New("terminal execution evidence is unavailable")
			}
			byID[id] = kernel.EvidenceRef{EvidenceID: id, SHA256: metadata.SHA256}
		}
	}
	result := make([]kernel.EvidenceRef, 0, len(byID))
	for _, reference := range byID {
		result = append(result, reference)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].EvidenceID < result[right].EvidenceID })
	if len(result) == 0 || len(result) > 64 {
		return nil, errors.New("invalid terminal execution evidence set")
	}
	return result, nil
}

func evidenceIDs(values []kernel.EvidenceRef) []kernel.UUIDv7 {
	result := make([]kernel.UUIDv7, len(values))
	for index := range values {
		result[index] = values[index].EvidenceID
	}
	return result
}

func reviewFindings(feature organization.FeatureRequest, targetID kernel.UUIDv7, branchID string, result structuredValidationResult, evidence []kernel.UUIDv7) []map[string]any {
	if result.Outcome == "PASS" {
		return []map[string]any{}
	}
	summary := strings.Join(result.Reasons, "; ")
	key := digestBytes([]byte(string(targetID) + "\x00" + branchID + "\x00" + summary))
	return []map[string]any{{"finding_id": deterministicOperationalUUID("review-finding", string(feature.ID), string(targetID), branchID, string(key)), "finding_key": key, "classification": "DEFECT", "summary": summary, "evidence_ids": evidence}}
}
