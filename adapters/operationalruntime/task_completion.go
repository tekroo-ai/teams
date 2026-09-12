package operationalruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

type taskValidatorResult struct {
	Task       organization.PlannedTask
	Invocation kernel.WorkInvocation
	Result     structuredValidationResult
}

const (
	invalidStructuredOutputReason = "task returned an invalid structured result; a changed-condition recovery is required"
	invalidStructuredReviewPolicy = "operator-or-product-owner-must-amend-scope-or-cancel"
)

func (service *ProductionService) reconcileTaskCompletions(ctx context.Context, feature organization.FeatureRequest, plan organization.FeaturePlan, states map[kernel.UUIDv7]kernel.AggregateState, heads map[kernel.UUIDv7]kernel.UUIDv7, invocations map[kernel.UUIDv7]kernel.WorkInvocation, snapshot kernel.Snapshot) (bool, error) {
	promotionChanged, err := service.reconcilePromotionCompletion(ctx, feature, plan, states, heads, invocations, snapshot)
	if err != nil || promotionChanged {
		return promotionChanged, err
	}
	featureValidatorID, _ := wholeFeatureValidationTaskID(plan)
	validatorsByTarget := make(map[kernel.UUIDv7][]taskValidatorResult)
	for _, task := range plan.Tasks {
		// Whole-feature validation consumes the assembled candidate and is a
		// promotion dependency in its own right. It must not be folded into the
		// task-local review of its legacy Validates anchor: that review may bind
		// an earlier, narrower candidate even when both results are valid.
		if len(task.Validates) == 0 || task.ID == featureValidatorID {
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
		conditionDigests := make([]kernel.Digest, 0, len(task.DependsOn))
		for _, dependencyID := range task.DependsOn {
			candidate, present := invocations[dependencyID]
			if !present || candidate.State != kernel.InvocationSucceeded || candidate.OutputDigest == nil {
				conditionDigests = nil
				break
			}
			conditionDigests = append(conditionDigests, *candidate.OutputDigest)
		}
		if len(conditionDigests) != len(task.DependsOn) {
			continue
		}
		invocationProfileDigest := invocation.WorkProfile.ProfileDigest
		expectedCondition, digestErr := taskInvocationConditionDigest(invocationProfileDigest, digestBytes(criteria), conditionDigests)
		if digestErr != nil {
			return false, digestErr
		}
		if !validatorConditionMatches(snapshot, task.ID, invocation, invocationProfileDigest, digestBytes(criteria), conditionDigests, expectedCondition) {
			continue
		}
		output, err := service.Runtime.ReadExecutionOutput(ctx, *invocation.OutputDigest)
		if err != nil {
			return false, fmt.Errorf("read validator %s output: %w", task.ID, err)
		}
		result, err := parseStructuredValidationResult(output)
		if err == nil {
			err = service.validateCandidateResult(ctx, task, snapshot, result)
		}
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
		if len(validators) == 0 {
			continue
		}
		if states[target.ID].Phase == kernel.PhaseCompleted {
			// A previous pass completed the target but not every validator task's
			// own evidence review (for example the daemon stopped between review
			// open and finalize). Re-drive the pending validators; completeEvidenceTask
			// recovers expired completion reviews and is idempotent otherwise.
			for _, validator := range validators {
				validatorState := states[validator.Task.ID]
				if validatorState.Phase != kernel.PhaseActive || validator.Result.Outcome != "PASS" {
					continue
				}
				if err := service.completeEvidenceTask(ctx, feature, validator.Task, validatorState, heads[validator.Task.ID], validator.Invocation, snapshot); err != nil {
					return changed, fmt.Errorf("recover validator task %s completion: %w", validator.Task.ID, err)
				}
				validatorState.Phase = kernel.PhaseCompleted
				states[validator.Task.ID] = validatorState
				changed = true
			}
			continue
		}
		if states[target.ID].Phase != kernel.PhaseActive || states[target.ID].Condition != kernel.ConditionRunnable {
			continue
		}
		expected := 0
		for _, candidate := range plan.Tasks {
			if candidate.ID == featureValidatorID {
				continue
			}
			for _, validates := range candidate.Validates {
				if validates == target.ID {
					expected++
				}
			}
		}
		failed := false
		for _, validator := range validators {
			if validator.Result.Outcome != "PASS" {
				failed = true
				break
			}
		}
		// A material validation failure is sufficient to stop and repair the
		// candidate. Do not spend another model call waiting for the remaining
		// validators to confirm a candidate already known to be unacceptable.
		if len(validators) != expected && !failed {
			continue
		}
		sort.Slice(validators, func(left, right int) bool { return validators[left].Task.ID < validators[right].Task.ID })
		implementer, found := invocations[target.ID]
		if !found || implementer.State != kernel.InvocationSucceeded || implementer.OutputDigest == nil {
			continue
		}
		status, err := service.finalizeTaskReview(ctx, feature, target, states[target.ID], heads[target.ID], implementer, validators, snapshot)
		if err != nil {
			return false, fmt.Errorf("finalize task %s review: %w", target.ID, err)
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
				return false, fmt.Errorf("complete validator task %s: %w", validator.Task.ID, err)
			}
			validatorState.Phase = kernel.PhaseCompleted
			states[validator.Task.ID] = validatorState
			changed = true
		}
	}
	featureValidationChanged, err := service.reconcileFeatureValidationCompletion(ctx, feature, plan, states, heads, invocations, snapshot)
	if err != nil {
		return changed, err
	}
	return changed || featureValidationChanged, nil
}

func (service *ProductionService) reconcileFeatureValidationCompletion(ctx context.Context, feature organization.FeatureRequest, plan organization.FeaturePlan, states map[kernel.UUIDv7]kernel.AggregateState, heads map[kernel.UUIDv7]kernel.UUIDv7, invocations map[kernel.UUIDv7]kernel.WorkInvocation, snapshot kernel.Snapshot) (bool, error) {
	featureValidatorID, found := wholeFeatureValidationTaskID(plan)
	if !found {
		return false, nil
	}
	var task *organization.PlannedTask
	for index := range plan.Tasks {
		if plan.Tasks[index].ID == featureValidatorID {
			task = &plan.Tasks[index]
			break
		}
	}
	if task == nil {
		return false, nil
	}
	state, found := states[task.ID]
	if !found || state.Phase != kernel.PhaseActive || state.Condition != kernel.ConditionRunnable {
		return false, nil
	}
	invocation, found := invocations[task.ID]
	if !found || invocation.State != kernel.InvocationSucceeded || invocation.OutputDigest == nil {
		return false, nil
	}
	profileSnapshot, profileFound := snapshot.WorkProfiles[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}]
	criteria, marshalErr := json.Marshal(task.AcceptanceCriteria)
	if marshalErr != nil || !profileFound || !profileSnapshot.Valid() {
		return false, errors.Join(organization.ErrInvalidFeature, marshalErr)
	}
	conditionDigests := make([]kernel.Digest, 0, len(task.DependsOn))
	for _, dependencyID := range task.DependsOn {
		candidate, present := invocations[dependencyID]
		if !present || candidate.State != kernel.InvocationSucceeded || candidate.OutputDigest == nil {
			return false, nil
		}
		conditionDigests = append(conditionDigests, *candidate.OutputDigest)
	}
	invocationProfileDigest := invocation.WorkProfile.ProfileDigest
	expectedCondition, err := taskInvocationConditionDigest(invocationProfileDigest, digestBytes(criteria), conditionDigests)
	if err != nil {
		return false, err
	}
	if !validatorConditionMatches(snapshot, task.ID, invocation, invocationProfileDigest, digestBytes(criteria), conditionDigests, expectedCondition) {
		return false, nil
	}
	output, err := service.Runtime.ReadExecutionOutput(ctx, *invocation.OutputDigest)
	if err != nil {
		return false, fmt.Errorf("read whole-feature validator %s output: %w", task.ID, err)
	}
	result, err := parseStructuredValidationResult(output)
	if err == nil {
		err = service.validateCandidateResult(ctx, *task, snapshot, result)
	}
	if err != nil {
		return service.handleInvalidStructuredTaskOutput(ctx, feature, *task, state, heads[task.ID], invocation, snapshot, conditionDigests)
	}
	if result.Outcome != "PASS" {
		return service.blockStructuredDecisionTask(ctx, feature, *task, state, invocation, snapshot, "whole-feature validation did not pass: "+strings.Join(result.Reasons, "; "), "whole-feature-validation-not-pass")
	}
	if err := service.completeEvidenceTask(ctx, feature, *task, state, heads[task.ID], invocation, snapshot); err != nil {
		return false, fmt.Errorf("complete whole-feature validator %s: %w", task.ID, err)
	}
	state.Phase = kernel.PhaseCompleted
	states[task.ID] = state
	return true, nil
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
		if err == nil {
			err = service.validateCandidateResult(ctx, task, snapshot, result)
		}
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
	_ = conditionDigests
	retried, err := service.authorizeStructuredOutputGlitchRetry(ctx, feature, task, state, head, invocation, snapshot)
	if err != nil {
		return false, err
	}
	if retried {
		return true, nil
	}
	return service.blockStructuredDecisionTask(ctx, feature, task, state, invocation, snapshot, invalidStructuredOutputReason, "invalid-structured-output")
}

// structuredOutputGlitchRetryAllowed reports whether a malformed structured
// result may be retried without an operator: validators and reviews whose
// result contract failed to parse, and the promotion, only while the planned
// attempt limit still has room. A structured FAIL is a verdict, never a
// glitch, and never reaches this predicate.
func structuredOutputGlitchRetryAllowed(task organization.PlannedTask, invocation kernel.WorkInvocation) bool {
	if invocation.State != kernel.InvocationSucceeded || invocation.AttemptOrdinal >= uint64(task.AttemptLimit) {
		return false
	}
	switch task.Purpose {
	case kernel.PurposeValidation, kernel.PurposeReview:
		return len(task.Validates) > 0
	case kernel.PurposePromotion:
		return true
	default:
		return false
	}
}

func (service *ProductionService) authorizeStructuredOutputGlitchRetry(ctx context.Context, feature organization.FeatureRequest, task organization.PlannedTask, state kernel.AggregateState, head kernel.UUIDv7, invocation kernel.WorkInvocation, snapshot kernel.Snapshot) (bool, error) {
	if !structuredOutputGlitchRetryAllowed(task, invocation) {
		return false, nil
	}
	nextAttempt := invocation.AttemptOrdinal + 1
	recoveryConditions, err := validationRecoveryConditionDigests(task, snapshot.WorkInvocations, invocation)
	if err != nil {
		return false, nil
	}
	profileConfig, configured := service.profilesByModel[task.ModelProfile]
	if !configured || !profileConfig.qualifiedFor(task.DecisionRoute, workKindForPurpose(task.Purpose, task.Risk), service.clock.Now().UTC()) {
		return false, nil
	}
	profileSnapshot, profileFound := snapshot.WorkProfiles[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}]
	if !profileFound || !profileSnapshot.Valid() {
		return false, organization.ErrInvalidFeature
	}
	owner, err := service.StartRole(ctx, task.Owner)
	if err != nil || owner.Status != organization.RoleIdle {
		return false, errors.Join(organization.ErrRoleNotRunning, err)
	}
	tracked := &trackedTask{plan: task, revision: state.Revision, last: head, profile: profileSnapshot.Profile, owner: owner}
	latestInvocations := make(map[kernel.UUIDv7]kernel.WorkInvocation, len(feature.Plan.Tasks))
	for _, planned := range feature.Plan.Tasks {
		if latest, found := latestTaskInvocation(snapshot.WorkInvocations, planned.ID); found {
			latestInvocations[planned.ID] = latest
		}
	}
	// Base evidence must include the failed invocation's terminal evidence, the
	// same role request evidence plays on the operator recovery path: the scope
	// rebind requires candidate receipt plus base evidence, and the candidate
	// receipt alone is a single reference.
	recoveryEvidence, evidenceErr := evidenceForInvocations(snapshot, invocation)
	if evidenceErr != nil {
		return false, evidenceErr
	}
	workspace, candidateEvidence, err := service.prepareCandidateConsumerWorkspace(ctx, feature, task, owner, *feature.Plan, latestInvocations, recoveryEvidence)
	if err != nil {
		return false, err
	}
	if err := service.rebindTaskCandidateWorkspace(ctx, feature, tracked, workspace, candidateEvidence); err != nil {
		return false, err
	}
	budgetRevision, err := service.extendTaskTechnicalRetryBudget(ctx, feature, tracked, task.Purpose, nextAttempt)
	if err != nil {
		return false, err
	}
	if err := service.authorizeTaskInvocationWithConditionPolicy(ctx, feature, tracked, profileConfig, workspace, budgetRevision, task.Purpose, nextAttempt, &invocation, recoveryConditions, true, false); err != nil {
		return false, err
	}
	return true, nil
}

func (service *ProductionService) blockStructuredDecisionTask(ctx context.Context, feature organization.FeatureRequest, task organization.PlannedTask, state kernel.AggregateState, invocation kernel.WorkInvocation, snapshot kernel.Snapshot, reason, key string) (bool, error) {
	evidence, err := evidenceForInvocations(snapshot, invocation)
	if err != nil {
		return false, err
	}
	payload, err := json.Marshal(map[string]any{"blocker_refs": []string{"teams://work-invocation/" + string(invocation.ID)}, "reason": reason, "review_policy": invalidStructuredReviewPolicy})
	if err != nil {
		return false, err
	}
	_, err = service.submitDeterministicActorTargetCommand(ctx, feature, "tekroo.command.work.block", kernel.AggregateTask, task.ID, service.policyAuthority, invocation.ActorFQN, invocation.Execution, state.Revision, state.LifecycleEpoch, payload, []kernel.DagParent{{ParentEventID: invocation.LastEventID, EdgeKind: kernel.EdgeResponse}}, evidence, key+"-"+string(task.ID)+"-"+string(*invocation.OutputDigest))
	return err == nil, err
}

func isExactInvalidStructuredOutputBlock(event kernel.DomainEvent, task organization.PlannedTask, invocation kernel.WorkInvocation, authority kernel.PrincipalRef) bool {
	if event.EventID == "" || event.EventType != "tekroo.event.work.blocked" || event.Aggregate != (kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}) || event.Authority != authority || event.ActorFQN == nil || *event.ActorFQN != invocation.ActorFQN || event.Execution == nil || *event.Execution != invocation.Execution || len(event.Parents) != 1 || event.Parents[0] != (kernel.DagParent{ParentEventID: invocation.LastEventID, EdgeKind: kernel.EdgeResponse}) {
		return false
	}
	var payload planningOutputBlockPayload
	if json.Unmarshal(event.Payload, &payload) != nil {
		return false
	}
	return len(payload.BlockerRefs) == 1 && payload.BlockerRefs[0] == "teams://work-invocation/"+string(invocation.ID) && payload.Reason == invalidStructuredOutputReason && payload.ReviewPolicy == invalidStructuredReviewPolicy
}

func validatorConditionMatches(snapshot kernel.Snapshot, taskID kernel.UUIDv7, invocation kernel.WorkInvocation, profileDigest, criteriaDigest kernel.Digest, baseConditions []kernel.Digest, baseDigest kernel.Digest) bool {
	invocationBaseDigest := baseDigest
	if invocation.WorkProfile.ProfileDigest != profileDigest {
		var err error
		invocationBaseDigest, err = taskInvocationConditionDigest(invocation.WorkProfile.ProfileDigest, criteriaDigest, baseConditions)
		if err != nil || !workProfileBindingCurrentOrMaintenanceSuccessor(snapshot, taskID, invocation.WorkProfile, profileDigest) {
			return false
		}
	}
	if invocation.ConditionDigest == invocationBaseDigest {
		return true
	}
	if invocation.RetryOfInvocationID == nil || invocation.RetryOrdinal == 0 {
		return false
	}
	prior, found := snapshot.WorkInvocations[kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: *invocation.RetryOfInvocationID}]
	if !found || prior.TaskID != taskID || prior.Purpose != invocation.Purpose || prior.AttemptOrdinal+1 != invocation.AttemptOrdinal || prior.RetryOrdinal+1 != invocation.RetryOrdinal {
		return false
	}
	if prior.State == kernel.InvocationSucceeded && prior.OutputDigest != nil {
		conditions := append(append([]kernel.Digest(nil), baseConditions...), *prior.OutputDigest)
		digest, err := taskInvocationConditionDigest(invocation.WorkProfile.ProfileDigest, criteriaDigest, conditions)
		if err == nil && invocation.ConditionDigest == digest {
			return true
		}
		// An operator-authorized revalidation after a repaired candidate carries
		// the operator's evidence digest instead of the prior validator output.
		// That private digest is not recoverable from the folded task snapshot.
		// Accept it only when the current task profile is the direct successor of
		// the prior invocation's profile and every retry-lineage check above has
		// already matched. Candidate identity is verified independently before
		// the validation result can complete the task.
		invocationProfile, found := snapshot.WorkProfileHistory[invocation.WorkProfile.ProfileID]
		if !found {
			current, currentFound := snapshot.WorkProfiles[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: taskID}]
			if currentFound && current.Profile.Binding() == invocation.WorkProfile {
				invocationProfile, found = current.Profile, true
			}
		}
		return found && invocationProfile.TaskID == taskID &&
			invocationProfile.Binding() == invocation.WorkProfile &&
			invocationProfile.SupersedesProfileID != nil &&
			*invocationProfile.SupersedesProfileID == prior.WorkProfile.ProfileID &&
			invocation.ConditionDigest != invocationBaseDigest &&
			invocation.ConditionDigest != prior.ConditionDigest
	}
	// A terminal runtime recovery is authorized under a successor work profile
	// using the exact candidate conditions plus an operator-supplied recovery
	// condition. That private condition cannot be reconstructed from the folded
	// task snapshot, but its authority was already checked when the retry event
	// was admitted. Require exact retry lineage, the successor profile, and a
	// changed condition; candidate identity is independently verified from the
	// immutable workspace receipt before this result can complete any task.
	return recoverableTaskTerminal(prior) && invocation.ConditionDigest != invocationBaseDigest
}

func workProfileBindingCurrentOrMaintenanceSuccessor(snapshot kernel.Snapshot, taskID kernel.UUIDv7, binding kernel.WorkProfileBinding, currentDigest kernel.Digest) bool {
	currentSnapshot, found := snapshot.WorkProfiles[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: taskID}]
	if !found || !currentSnapshot.Valid() || currentSnapshot.Profile.ProfileDigest != currentDigest || binding.LifecycleEpoch != currentSnapshot.Profile.LifecycleEpoch || binding.ScopeRevision != currentSnapshot.Profile.ScopeRevision {
		return false
	}
	current := currentSnapshot.Profile
	seen := make(map[kernel.UUIDv7]struct{})
	for current.ProfileID != binding.ProfileID {
		if _, duplicate := seen[current.ProfileID]; duplicate || current.SupersedesProfileID == nil {
			return false
		}
		seen[current.ProfileID] = struct{}{}
		prior, found := snapshot.WorkProfileHistory[*current.SupersedesProfileID]
		if !found || !prior.Valid() || prior.TaskID != taskID || prior.ProfileRevision+1 != current.ProfileRevision || current.Budgets.DeadlineAt.Before(prior.Budgets.DeadlineAt) || !sameWorkProfileSemantics(prior, current) || !evidenceSuperset(current.ClassificationEvidenceIDs, prior.ClassificationEvidenceIDs) {
			return false
		}
		current = prior
	}
	return current.Binding() == binding
}

func sameWorkProfileSemantics(left, right kernel.WorkRiskProfile) bool {
	left.ProfileID, right.ProfileID = "", ""
	left.ProfileRevision, right.ProfileRevision = 0, 0
	left.ProfileDigest, right.ProfileDigest = "", ""
	left.Budgets.DeadlineAt, right.Budgets.DeadlineAt = time.Time{}, time.Time{}
	left.ClassificationEvidenceIDs, right.ClassificationEvidenceIDs = nil, nil
	left.SupersedesProfileID, right.SupersedesProfileID = nil, nil
	return reflect.DeepEqual(left, right)
}

func evidenceSuperset(values, required []kernel.UUIDv7) bool {
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
		_, err := service.submitDeterministicActorTargetCommand(ctx, feature, "tekroo.command.work.block", kernel.AggregateTask, target.ID, service.policyAuthority, owner.ActorFQN, owner.Execution, state.Revision, state.LifecycleEpoch, payload, []kernel.DagParent{{ParentEventID: reviewHead, EdgeKind: kernel.EdgeResponse}}, evidence, "repair-exhausted-"+string(target.ID)+"-"+string(*implementer.OutputDigest))
		return err == nil, err
	}
	profileConfig, configured := service.profilesByModel[target.ModelProfile]
	owner, active, ownerErr := service.RoleHost.Status(ctx, target.Owner)
	workspace, workspaceErr := service.workspaceForExistingTask(ctx, target, owner, snapshot)
	budget := snapshot.WorkBudgetAccounts[kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: feature.BudgetAccountID}]
	if !configured || !profileConfig.qualifiedFor(target.DecisionRoute, workKindForPurpose(target.Purpose, target.Risk), service.clock.Now().UTC()) || ownerErr != nil || !active || owner.Status != organization.RoleIdle || workspaceErr != nil || !budget.Valid() {
		return false, errors.Join(organization.ErrRoleNotRunning, ownerErr, workspaceErr)
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
	validatorTaskIDs := make([]kernel.UUIDv7, len(validators))
	for index := range validators {
		validatorTaskIDs[index] = validators[index].Task.ID
	}
	evidence, err = appendTaskScopeEvidence(snapshot, evidence, validatorTaskIDs...)
	if err != nil {
		return "", err
	}
	validatorCandidateArtifacts := make([]kernel.Digest, 0, len(validators))
	for _, validator := range validators {
		digest, digestErr := service.candidateArtifactDigest(ctx, validator.Task.ID, snapshot)
		if digestErr != nil {
			return "", fmt.Errorf("resolve validator candidate artifact task=%s: %w", validator.Task.ID, digestErr)
		}
		validatorCandidateArtifacts = append(validatorCandidateArtifacts, digest)
	}
	if !candidateArtifactsValid(validatorCandidateArtifacts) {
		return "", errInvalidCandidateWorkspace
	}
	// Validators may inspect task-local or larger assembled candidates. Every
	// candidate is independently verified above; the review subject remains the
	// implementer's immutable output to which those invocations were bound.
	candidateArtifact := *implementer.OutputDigest
	if completedReview, found := reusableFinalizedTaskReview(snapshot, target, profileSnapshot.Profile, candidateArtifact, validators, service.planning.PolicyRevision); found {
		return service.completeTaskFromFinalizedReview(ctx, feature, target, state, candidateArtifact, evidence, completedReview)
	}
	reviewInvocations := append([]kernel.WorkInvocation{implementer}, validatorInvocations(validators)...)
	reviewDeadline, err := postExecutionReviewDeadline(profileSnapshot.Profile.Budgets.DeadlineAt, service.planningDeadline, reviewInvocations...)
	if err != nil {
		return "", err
	}
	evidence, reviewID, err := service.prepareCompletionReview(ctx, feature, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: target.ID}, candidateArtifact, "completion-review-v3", []string{string(feature.ID), string(target.ID)}, snapshot, evidence)
	if err != nil {
		return "", err
	}
	branchSpecs := make([]map[string]any, len(validators))
	parents := []kernel.DagParent{{ParentEventID: implementer.LastEventID, EdgeKind: kernel.EdgeCausal}}
	for index, validator := range validators {
		branchID := "validator-" + string(validator.Task.ID)
		branchSpecs[index] = map[string]any{
			"branch_id": branchID, "validator": kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: string(validator.Invocation.ActorFQN)},
			"resolution_owner_fqn": target.Owner, "acceptance_criteria": target.AcceptanceCriteria,
			"input_evidence_ids": evidenceIDs(evidence), "deadline_at": reviewDeadline,
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
	implementerIdentity, err := service.workExecutionIdentity(ctx, implementer)
	if err != nil {
		return "", err
	}
	evidenceSet, _ := json.Marshal(evidence)
	evidenceSetDigest := digestBytes(evidenceSet)
	openPayload, _ := json.Marshal(map[string]any{
		"subject_kind": kernel.AggregateTask, "subject_id": target.ID, "lifecycle_epoch": profileSnapshot.Profile.LifecycleEpoch,
		"scope_revision": profileSnapshot.Profile.ScopeRevision, "criteria_revision": uint64(1), "evidence_set_digest": evidenceSetDigest,
		"branch_policy_revision": service.planning.PolicyRevision, "branches": branchSpecs, "join_rule": "ALL_PASS", "partial_result_policy": "WAIT_ALL",
		"adjudication": map[string]any{"adjudicator": service.policyAuthority, "deadline_at": reviewDeadline, "round_limit": uint64(target.ReviewRoundLimit)},
		"work_profile": profileSnapshot.Profile.Binding(), "candidate_artifact_digest": candidateArtifact,
		"implementer": implementerIdentity, "verification_topology_digest": profileSnapshot.Profile.VerificationTopologyDigest, "variant_group_id": nil,
	})
	reviewRef := kernel.AggregateRef{Kind: kernel.AggregateCompletionReview, ID: reviewID}
	fresh, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: reviewRef})
	if err != nil {
		return "", err
	}
	review, reviewExists := fresh.Reviews[reviewRef]
	var priorEvent kernel.UUIDv7
	var reviewRevision uint64
	if reviewExists {
		if review.ReviewID != reviewID || review.Subject != (kernel.AggregateRef{Kind: kernel.AggregateTask, ID: target.ID}) || review.LifecycleEpoch != profileSnapshot.Profile.LifecycleEpoch || review.CandidateArtifactDigest != candidateArtifact || review.BranchPolicyRevision != service.planning.PolicyRevision || len(review.RequiredBranchIDs) != len(validators) {
			return "", organization.ErrInvalidFeature
		}
		for _, validator := range validators {
			branchID := "validator-" + string(validator.Task.ID)
			branch, found := review.Branches[branchID]
			if !found || branch.Validator != (kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: string(validator.Invocation.ActorFQN)}) {
				return "", organization.ErrInvalidFeature
			}
		}
		var headFound bool
		reviewRevision, priorEvent, headFound, err = service.Store.ReadAggregateRevisionHead(ctx, reviewRef)
		if err != nil || !headFound || reviewRevision != review.ReviewRevision {
			return "", errors.Join(organization.ErrInvalidFeature, err)
		}
	} else {
		opened, openErr := service.submitDeterministicCommand(ctx, feature, "tekroo.command.completion-review.open", kernel.SchemaVersion, kernel.AggregateCompletionReview, reviewID, service.policyAuthority, 0, openPayload, parents, evidence, fmt.Sprintf("review-open-v3-%s-%s-%s-policy-%d", target.ID, candidateArtifact, evidenceSetDigest, service.provenance.PolicyRevision), kernel.AggregatePrecondition{Aggregate: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: target.ID}, Expected: kernel.NewExpectedRevision(state.Revision)})
		if openErr != nil {
			return "", openErr
		}
		priorEvent = opened.EventIDs[0]
		reviewRevision = 1
		review = kernel.CompletionReviewSnapshot{ReviewID: reviewID, ResultRecords: make(map[string]kernel.ReviewBranchResult)}
	}
	resultEvents := make([]kernel.UUIDv7, 0, len(validators))
	terminalStatus := "PASS"
	for _, validator := range validators {
		branchID := "validator-" + string(validator.Task.ID)
		if recorded, found := review.ResultRecords[branchID]; found {
			if recorded.SourceRole != "VALIDATOR" || recorded.Authority != (kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: string(validator.Invocation.ActorFQN)}) || recorded.Result != validator.Result.Outcome || !reflect.DeepEqual(recorded.Reasons, validator.Result.Reasons) || recorded.CandidateArtifactDigest != candidateArtifact {
				return "", organization.ErrInvalidFeature
			}
			resultEvents = append(resultEvents, recorded.EventID)
			if recorded.Result != "PASS" && terminalStatus == "PASS" {
				terminalStatus = recorded.Result
			}
			continue
		}
		if review.Finalization != nil {
			return "", organization.ErrInvalidFeature
		}
		identity, identityErr := service.workExecutionIdentity(ctx, validator.Invocation)
		if identityErr != nil {
			return "", identityErr
		}
		comparison, _ := json.Marshal(map[string]any{"implementer": implementerIdentity, "validator": identity})
		resultPayload, _ := json.Marshal(map[string]any{
			"review_id": reviewID, "branch_id": branchID, "branch_policy_revision": service.planning.PolicyRevision,
			"source_role": "VALIDATOR", "round": uint64(1), "result": validator.Result.Outcome, "reasons": validator.Result.Reasons,
			"evidence_ids": evidenceIDs(evidence), "findings": reviewFindings(feature, target.ID, branchID, validator.Result, evidenceIDs(evidence)),
			"supersedes_result_event_ids": []kernel.UUIDv7{}, "changed_condition_evidence_ids": []kernel.UUIDv7{},
			"candidate_artifact_digest": candidateArtifact,
			"independence_receipt":      map[string]any{"proven_dimensions": profileSnapshot.Profile.RequiredIndependenceDimensions, "identity_comparison_digest": digestBytes(comparison), "method_ids": []string{"openhands-independent-validation"}, "evidence_ids": evidenceIDs(evidence)},
		})
		recorded, recordErr := service.submitDeterministicReviewResult(ctx, feature, reviewID, reviewRevision, validator.Invocation, resultPayload, priorEvent, evidence, "review-result-"+string(target.ID)+"-"+string(validator.Task.ID)+"-"+string(candidateArtifact))
		if recordErr != nil {
			return "", recordErr
		}
		priorEvent = recorded.EventIDs[0]
		reviewRevision++
		resultEvents = append(resultEvents, priorEvent)
		if validator.Result.Outcome != "PASS" && terminalStatus == "PASS" {
			terminalStatus = validator.Result.Outcome
		}
	}
	finalizedEventID := priorEvent
	completionReviewRevision := reviewRevision
	if review.Finalization != nil {
		terminalStatus = review.Finalization.TerminalStatus
		finalizedEventID = review.Finalization.EventID
		completionReviewRevision = review.Finalization.ReviewRevision
	} else {
		reviewProfile := profileSnapshot.Profile.Binding()
		verificationTopology := profileSnapshot.Profile.VerificationTopologyDigest
		scopeRevision := profileSnapshot.Profile.ScopeRevision
		if reviewExists {
			reviewProfile = review.WorkProfile
			verificationTopology = review.VerificationTopologyDigest
			scopeRevision = review.ScopeRevision
		}
		finalPayload, _ := json.Marshal(map[string]any{
			"review_id": reviewID, "subject_kind": kernel.AggregateTask, "subject_id": target.ID, "lifecycle_epoch": profileSnapshot.Profile.LifecycleEpoch,
			"branch_policy_revision": service.planning.PolicyRevision, "expected_review_revision": reviewRevision, "terminal_status": terminalStatus,
			"result_event_ids": resultEvents, "evidence_ids": evidenceIDs(evidence), "scope_revision": scopeRevision,
			"work_profile": reviewProfile, "candidate_artifact_digest": candidateArtifact,
			"verification_topology_digest": verificationTopology,
		})
		finalized, finalizeErr := service.submitDeterministicCommand(ctx, feature, "tekroo.command.completion-review.finalize", kernel.SchemaVersion, kernel.AggregateCompletionReview, reviewID, service.policyAuthority, reviewRevision, finalPayload, []kernel.DagParent{{ParentEventID: priorEvent, EdgeKind: kernel.EdgeResponse}}, evidence, "review-finalize-"+string(target.ID)+"-"+string(candidateArtifact))
		if finalizeErr != nil {
			return terminalStatus, finalizeErr
		}
		finalizedEventID = finalized.EventIDs[0]
		completionReviewRevision = reviewRevision
	}
	if terminalStatus != "PASS" {
		return terminalStatus, nil
	}
	owner, err := service.StartRole(ctx, target.Owner)
	if err != nil {
		return "", errors.Join(organization.ErrRoleNotRunning, err)
	}
	completionPayload, _ := json.Marshal(map[string]any{
		"lifecycle_epoch": profileSnapshot.Profile.LifecycleEpoch, "criteria_revision": uint64(1), "evidence_ids": evidenceIDs(evidence),
		"artifact_digests": []kernel.Digest{candidateArtifact}, "unresolved_exceptions": []string{},
		"completion_review_id": reviewID, "completion_review_revision": completionReviewRevision,
		"branch_policy_revision": service.planning.PolicyRevision, "validation_finalized_event_id": finalizedEventID, "owner_fqn": target.Owner,
	})
	completionKey := fmt.Sprintf("task-complete-%s-%s-execution-%s-%d", target.ID, candidateArtifact, owner.Execution.ExecutionID, owner.Execution.FencingEpoch)
	_, err = service.submitDeterministicActorTargetCommand(ctx, feature, "tekroo.command.task.request-completion", kernel.AggregateTask, target.ID, kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: string(target.Owner)}, owner.ActorFQN, owner.Execution, state.Revision, state.LifecycleEpoch, completionPayload, []kernel.DagParent{{ParentEventID: finalizedEventID, EdgeKind: kernel.EdgeCausal}}, evidence, completionKey)
	return terminalStatus, err
}

// A finalized PASS review is durable evidence for its immutable candidate. A
// daemon restart or a later deadline/evidence-only profile successor must not
// reopen that review or rerun its validators. Reuse is deliberately strict:
// the subject, lifecycle, scope, topology, branch set, validator identities,
// results, and candidate must all still match.
func reusableFinalizedTaskReview(snapshot kernel.Snapshot, target organization.PlannedTask, currentProfile kernel.WorkRiskProfile, candidate kernel.Digest, validators []taskValidatorResult, branchPolicyRevision uint64) (kernel.CompletionReviewSnapshot, bool) {
	reviewIDs := make([]kernel.UUIDv7, 0, len(snapshot.Reviews))
	for ref := range snapshot.Reviews {
		if ref.Kind == kernel.AggregateCompletionReview {
			reviewIDs = append(reviewIDs, ref.ID)
		}
	}
	sort.Slice(reviewIDs, func(left, right int) bool { return reviewIDs[left] < reviewIDs[right] })
	for _, reviewID := range reviewIDs {
		review := snapshot.Reviews[kernel.AggregateRef{Kind: kernel.AggregateCompletionReview, ID: reviewID}]
		if review.Subject != (kernel.AggregateRef{Kind: kernel.AggregateTask, ID: target.ID}) ||
			review.LifecycleEpoch != currentProfile.LifecycleEpoch ||
			review.ScopeRevision != currentProfile.ScopeRevision ||
			review.CriteriaRevision != 1 ||
			review.BranchPolicyRevision != branchPolicyRevision ||
			review.CandidateArtifactDigest != candidate ||
			review.VerificationTopologyDigest != currentProfile.VerificationTopologyDigest ||
			review.Finalization == nil ||
			review.Finalization.TerminalStatus != "PASS" ||
			review.Finalization.ReviewRevision != review.ReviewRevision ||
			review.Finalization.ScopeRevision != review.ScopeRevision ||
			review.Finalization.WorkProfile != review.WorkProfile ||
			review.Finalization.CandidateArtifactDigest != candidate ||
			review.Finalization.VerificationTopologyDigest != review.VerificationTopologyDigest ||
			!review.Join.Complete || review.Join.Status != "PASS" ||
			len(review.RequiredBranchIDs) != len(validators) || len(review.ResultRecords) != len(validators) ||
			!workProfileBindingCurrentOrMaintenanceSuccessor(snapshot, target.ID, review.WorkProfile, currentProfile.ProfileDigest) {
			continue
		}
		finalResultEvents := make(map[kernel.UUIDv7]struct{}, len(review.Finalization.ResultEventIDs))
		for _, eventID := range review.Finalization.ResultEventIDs {
			finalResultEvents[eventID] = struct{}{}
		}
		matches := len(finalResultEvents) == len(validators)
		for _, validator := range validators {
			branchID := "validator-" + string(validator.Task.ID)
			branch, branchFound := review.Branches[branchID]
			result, resultFound := review.ResultRecords[branchID]
			_, finalResultFound := finalResultEvents[result.EventID]
			if !branchFound || !resultFound || !finalResultFound ||
				branch.Validator != (kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: string(validator.Invocation.ActorFQN)}) ||
				result.SourceRole != "VALIDATOR" ||
				result.Authority != branch.Validator ||
				result.Result != validator.Result.Outcome ||
				!reflect.DeepEqual(result.Reasons, validator.Result.Reasons) ||
				result.CandidateArtifactDigest != candidate {
				matches = false
				break
			}
		}
		finalized, eventFound := snapshot.AcceptedEvents[review.Finalization.EventID]
		if matches && eventFound && !finalized.Quarantined && finalized.EventType == "tekroo.event.completion-review.finalized" {
			return review, true
		}
	}
	return kernel.CompletionReviewSnapshot{}, false
}

func (service *ProductionService) completeTaskFromFinalizedReview(ctx context.Context, feature organization.FeatureRequest, target organization.PlannedTask, state kernel.AggregateState, candidate kernel.Digest, evidence []kernel.EvidenceRef, review kernel.CompletionReviewSnapshot) (string, error) {
	owner, err := service.StartRole(ctx, target.Owner)
	if err != nil {
		return "", errors.Join(organization.ErrRoleNotRunning, err)
	}
	completionPayload, _ := json.Marshal(map[string]any{
		"lifecycle_epoch": review.LifecycleEpoch, "criteria_revision": review.CriteriaRevision, "evidence_ids": evidenceIDs(evidence),
		"artifact_digests": []kernel.Digest{candidate}, "unresolved_exceptions": []string{},
		"completion_review_id": review.ReviewID, "completion_review_revision": review.Finalization.ReviewRevision,
		"branch_policy_revision": review.BranchPolicyRevision, "validation_finalized_event_id": review.Finalization.EventID, "owner_fqn": target.Owner,
	})
	completionKey := fmt.Sprintf("task-complete-%s-%s-review-%s-execution-%s-%d", target.ID, candidate, review.ReviewID, owner.Execution.ExecutionID, owner.Execution.FencingEpoch)
	_, err = service.submitDeterministicActorTargetCommand(ctx, feature, "tekroo.command.task.request-completion", kernel.AggregateTask, target.ID, kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: string(target.Owner)}, owner.ActorFQN, owner.Execution, state.Revision, state.LifecycleEpoch, completionPayload, []kernel.DagParent{{ParentEventID: review.Finalization.EventID, EdgeKind: kernel.EdgeCausal}}, evidence, completionKey)
	return "PASS", err
}

func candidateArtifactsValid(values []kernel.Digest) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		if !value.Valid() {
			return false
		}
	}
	return true
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
	reviewDeadline, err := postExecutionReviewDeadline(profileSnapshot.Profile.Budgets.DeadlineAt, service.planningDeadline, invocation)
	if err != nil {
		return err
	}
	evidence, reviewID, err := service.prepareCompletionReview(ctx, feature, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}, *invocation.OutputDigest, "evidence-completion-review-v3", []string{string(feature.ID), string(task.ID)}, snapshot, evidence)
	if err != nil {
		return err
	}
	identity, err := service.workExecutionIdentity(ctx, invocation)
	if err != nil {
		return err
	}
	evidenceSet, _ := json.Marshal(evidence)
	evidenceSetDigest := digestBytes(evidenceSet)
	branchID := "deterministic-execution-evidence"
	openPayload, _ := json.Marshal(map[string]any{
		"subject_kind": kernel.AggregateTask, "subject_id": task.ID, "lifecycle_epoch": profileSnapshot.Profile.LifecycleEpoch, "scope_revision": profileSnapshot.Profile.ScopeRevision,
		"criteria_revision": uint64(1), "evidence_set_digest": evidenceSetDigest, "branch_policy_revision": service.planning.PolicyRevision,
		"branches":  []map[string]any{{"branch_id": branchID, "validator": service.serviceAuthority, "resolution_owner_fqn": task.Owner, "acceptance_criteria": task.AcceptanceCriteria, "input_evidence_ids": evidenceIDs(evidence), "deadline_at": reviewDeadline, "round_limit": uint64(1), "required_independence_dimensions": []kernel.IndependenceDimension{kernel.IndependencePrincipal, kernel.IndependenceMethod}, "required_method_ids": []string{"teams-terminal-evidence-check"}}},
		"join_rule": "ALL_PASS", "partial_result_policy": "WAIT_ALL", "adjudication": map[string]any{"adjudicator": service.policyAuthority, "deadline_at": reviewDeadline, "round_limit": uint64(1)},
		"work_profile": profileSnapshot.Profile.Binding(), "candidate_artifact_digest": *invocation.OutputDigest, "implementer": identity,
		"verification_topology_digest": profileSnapshot.Profile.VerificationTopologyDigest, "variant_group_id": nil,
	})
	opened, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.completion-review.open", kernel.SchemaVersion, kernel.AggregateCompletionReview, reviewID, service.policyAuthority, 0, openPayload, []kernel.DagParent{{ParentEventID: invocation.LastEventID, EdgeKind: kernel.EdgeCausal}}, evidence, fmt.Sprintf("evidence-review-open-v3-%s-%s-%s-policy-%d", task.ID, *invocation.OutputDigest, evidenceSetDigest, service.provenance.PolicyRevision), kernel.AggregatePrecondition{Aggregate: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}, Expected: kernel.NewExpectedRevision(state.Revision)})
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
	finalPayload, _ := json.Marshal(map[string]any{"review_id": reviewID, "subject_kind": kernel.AggregateTask, "subject_id": task.ID, "lifecycle_epoch": profileSnapshot.Profile.LifecycleEpoch, "branch_policy_revision": service.planning.PolicyRevision, "expected_review_revision": uint64(2), "terminal_status": "PASS", "result_event_ids": recorded.EventIDs, "evidence_ids": evidenceIDs(evidence), "scope_revision": profileSnapshot.Profile.ScopeRevision, "work_profile": profileSnapshot.Profile.Binding(), "candidate_artifact_digest": *invocation.OutputDigest, "verification_topology_digest": profileSnapshot.Profile.VerificationTopologyDigest})
	finalized, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.completion-review.finalize", kernel.SchemaVersion, kernel.AggregateCompletionReview, reviewID, service.policyAuthority, 2, finalPayload, []kernel.DagParent{{ParentEventID: recorded.EventIDs[0], EdgeKind: kernel.EdgeResponse}}, evidence, "evidence-review-finalize-"+string(task.ID)+"-"+string(*invocation.OutputDigest))
	if err != nil {
		return err
	}
	owner, active, err := service.RoleHost.Status(ctx, task.Owner)
	if err != nil || !active {
		return errors.Join(organization.ErrRoleNotRunning, err)
	}
	completionPayload, _ := json.Marshal(map[string]any{"lifecycle_epoch": profileSnapshot.Profile.LifecycleEpoch, "criteria_revision": uint64(1), "evidence_ids": evidenceIDs(evidence), "artifact_digests": []kernel.Digest{*invocation.OutputDigest}, "unresolved_exceptions": []string{}, "completion_review_id": reviewID, "completion_review_revision": uint64(2), "branch_policy_revision": service.planning.PolicyRevision, "validation_finalized_event_id": finalized.EventIDs[0], "owner_fqn": task.Owner})
	_, err = service.submitDeterministicActorTargetCommand(ctx, feature, "tekroo.command.task.request-completion", kernel.AggregateTask, task.ID, kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: string(task.Owner)}, owner.ActorFQN, owner.Execution, state.Revision, state.LifecycleEpoch, completionPayload, []kernel.DagParent{{ParentEventID: finalized.EventIDs[0], EdgeKind: kernel.EdgeCausal}}, evidence, "evidence-task-complete-"+string(task.ID)+"-"+string(*invocation.OutputDigest))
	return err
}

// Execution deadlines bound model work. Completion reviews are deterministic
// ingestion performed after terminal evidence exists and need their own finite
// window so a daemon outage cannot invalidate work that already finished. The
// terminal timestamps are durable event state, making this deadline replayable.
func postExecutionReviewDeadline(profileDeadline time.Time, allowance time.Duration, invocations ...kernel.WorkInvocation) (time.Time, error) {
	if profileDeadline.IsZero() || allowance <= 0 || len(invocations) == 0 {
		return time.Time{}, organization.ErrInvalidFeature
	}
	deadline := profileDeadline.UTC()
	for _, invocation := range invocations {
		if invocation.FinishedAt == nil || invocation.FinishedAt.IsZero() || !invocation.State.Terminal() {
			return time.Time{}, organization.ErrInvalidFeature
		}
		candidate := invocation.FinishedAt.UTC().Add(allowance)
		if candidate.After(deadline) {
			deadline = candidate
		}
	}
	return deadline, nil
}

func (service *ProductionService) submitDeterministicReviewResult(ctx context.Context, feature organization.FeatureRequest, reviewID kernel.UUIDv7, revision uint64, validator kernel.WorkInvocation, payload []byte, parent kernel.UUIDv7, evidence []kernel.EvidenceRef, key string) (kernel.CommandReceipt, error) {
	// The invocation proves which persistent actor performed the validation, but
	// its execution fence may be historical by the time deterministic review
	// ingestion runs. Re-establish that same actor and use its current execution
	// solely to authenticate this command; the immutable invocation and evidence
	// continue to identify the execution that performed the work.
	current, err := service.StartRole(ctx, validator.ActorFQN)
	if err != nil || current.ActorFQN != validator.ActorFQN {
		return kernel.CommandReceipt{}, errors.Join(organization.ErrRoleNotRunning, err)
	}
	executionKey := fmt.Sprintf("%s-execution-%s-%d", key, current.Execution.ExecutionID, current.Execution.FencingEpoch)
	return service.submitDeterministicActorTargetCommand(ctx, feature, "tekroo.command.completion-review.record-result", kernel.AggregateCompletionReview, reviewID, kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: string(validator.ActorFQN)}, validator.ActorFQN, current.Execution, revision, 0, payload, []kernel.DagParent{{ParentEventID: parent, EdgeKind: kernel.EdgeResponse}}, evidence, executionKey)
}

func (service *ProductionService) submitDeterministicActorTargetCommand(ctx context.Context, feature organization.FeatureRequest, commandType string, kind kernel.AggregateKind, id kernel.UUIDv7, authority kernel.PrincipalRef, actor kernel.ActorFQN, execution kernel.ExecutionTuple, revision, lifecycleEpoch uint64, payload []byte, parents []kernel.DagParent, evidence []kernel.EvidenceRef, key string) (kernel.CommandReceipt, error) {
	command := kernel.KernelCommand{ContractManifest: kernel.ContractIdentity, CommandID: deterministicOperationalUUID("command", string(feature.ID), commandType, string(id), key), CommandType: commandType, CommandVersion: kernel.SchemaVersion, Target: kernel.AggregateRef{Kind: kind, ID: id}, Authority: authority, ActorFQN: &actor, Execution: &execution, ExpectedRevision: kernel.NewExpectedRevision(revision), ExpectedLifecycleEpoch: expectedLifecycleEpoch(kind, revision, lifecycleEpoch), Preconditions: []kernel.AggregatePrecondition{}, ExpectedPolicyRevision: service.provenance.PolicyRevision, ExpectedCatalogueRevision: kernel.CatalogueRevision, IdempotencyKey: "feature:" + string(feature.ID) + ":" + key, CorrelationID: feature.ID, Causation: append([]kernel.DagParent(nil), parents...), Payload: append([]byte(nil), payload...), EvidenceRefs: append([]kernel.EvidenceRef(nil), evidence...)}
	receipt, err := service.Submit(ctx, command)
	if err != nil {
		return receipt, fmt.Errorf("%s: %w", commandType, err)
	}
	if receipt.OutcomeCode != kernel.OutcomeApplied && receipt.OutcomeCode != kernel.OutcomeNoChange {
		return receipt, fmt.Errorf("%s rejected: %s", commandType, receipt.ReasonCode)
	}
	return receipt, nil
}

func (service *ProductionService) workExecutionIdentity(ctx context.Context, invocation kernel.WorkInvocation) (kernel.WorkExecutionIdentity, error) {
	if invocation.RequestDigest == nil {
		return kernel.WorkExecutionIdentity{}, organization.ErrInvalidFeature
	}
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: invocation.TaskID}})
	if err != nil {
		return kernel.WorkExecutionIdentity{}, err
	}
	scope, found := snapshot.TaskOperationalScopes[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: invocation.TaskID}]
	if !found || scope.WorkspaceID != invocation.WorkspaceID {
		return kernel.WorkExecutionIdentity{}, organization.ErrInvalidFeature
	}
	workspaceDigest, err := durableWorkspaceIdentityDigest(scope)
	if err != nil {
		return kernel.WorkExecutionIdentity{}, err
	}
	return kernel.WorkExecutionIdentity{Principal: kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: string(invocation.ActorFQN)}, ActorFQN: invocation.ActorFQN, ExecutionID: invocation.Execution.ExecutionID, FencingEpoch: invocation.Execution.FencingEpoch, ModelProfileDigest: invocation.ModelProfileDigest, WorkspaceDigest: workspaceDigest, ContextDigest: *invocation.RequestDigest}, nil
}

func durableWorkspaceIdentityDigest(scope kernel.TaskOperationalScope) (kernel.Digest, error) {
	identity := struct {
		WorkspaceID   string   `json:"workspace_id"`
		WorktreeID    string   `json:"worktree_id"`
		Branch        string   `json:"branch"`
		BaselineSHA   string   `json:"baseline_sha"`
		WritablePaths []string `json:"writable_paths"`
	}{scope.WorkspaceID, scope.WorktreeID, scope.Branch, scope.BaselineSHA, append([]string(nil), scope.WritablePaths...)}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	return digestBytes(encoded), nil
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
