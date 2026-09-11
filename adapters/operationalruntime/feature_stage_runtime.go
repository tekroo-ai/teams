package operationalruntime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

type featurePlanningStage string

const (
	stageRefinement             featurePlanningStage = "refinement"
	stageSpecification          featurePlanningStage = "specification"
	stageArchitecture           featurePlanningStage = "architecture"
	stageArchitectureTaskReview featurePlanningStage = "architecture-task-review"
	stageArchitectureReview     featurePlanningStage = "architecture-review"

	invalidPlanningOutputReason         = "planning role returned an invalid structured handoff; a changed-condition recovery is required"
	invalidPlanningReviewPolicy         = "operator-or-product-owner-must-amend-scope-or-cancel"
	failedArchitectureTaskReviewReason  = "independent planned-task feasibility review did not pass"
	failedArchitectureReviewReason      = "independent architecture feasibility review did not pass"
	architectureAutomaticSuccessorLimit = uint32(1)
	architecturePlanRecordedRoundLimit  = uint32(128)
)

var errNewInvocationAdmissionSuspended = errors.New("new invocation admission is suspended")

type architecturePlanRejection struct {
	Round            uint32
	ReviewTaskID     kernel.UUIDv7
	ReviewHead       kernel.UUIDv7
	ReviewInvocation kernel.WorkInvocation
	ReviewOutput     []byte
}

type architectureTaskReviewProgress uint8

const (
	architectureTaskReviewPending architectureTaskReviewProgress = iota
	architectureTaskReviewPassed
	architectureTaskReviewRejected
)

func (service *ProductionService) materializeFeatureIntake(ctx context.Context, feature organization.FeatureRequest) error {
	_, _, _, _, _, err := service.ensureFeaturePlanningTask(ctx, feature, stageRefinement)
	return err
}

func (service *ProductionService) reconcileFeaturePlanning(ctx context.Context) error {
	features, err := service.Store.ListFeatures(ctx, []organization.FeatureStatus{organization.FeatureSubmitted, organization.FeatureReadyForPlanning, organization.FeatureSpecified}, 1000)
	if err != nil {
		return err
	}
	for _, feature := range features {
		stage, ok := planningStage(feature.Status)
		if !ok {
			continue
		}
		task, state, head, invocation, snapshot, err := service.ensureFeaturePlanningTask(ctx, feature, stage)
		if errors.Is(err, errNewInvocationAdmissionSuspended) {
			continue
		}
		if err != nil {
			return fmt.Errorf("feature %s %s: %w", feature.ID, stage, err)
		}
		if invocation.State != kernel.InvocationSucceeded {
			// Failed planning work remains visible and stopped. A successor model
			// call requires the operator recovery surface to bind a classified
			// failure and changed-condition evidence.
			continue
		}
		if invocation.OutputDigest == nil {
			continue
		}
		output, err := service.Runtime.ReadExecutionOutput(ctx, *invocation.OutputDigest)
		if err != nil {
			return err
		}
		if state.Condition == kernel.ConditionBlocked {
			// A deployment may repair a deterministic decoder defect after the
			// model invocation succeeded. Reuse that immutable output only when the
			// current head is the exact invalid-output block emitted for this exact
			// invocation and the repaired validator now accepts it. Other blockers
			// remain stopped and require their own explicit resolution.
			var recovered bool
			var recoveryErr error
			state, head, snapshot, recovered, recoveryErr = service.recoverValidFeatureStageOutput(ctx, feature, stage, task, state, head, invocation, snapshot, output)
			if recoveryErr != nil {
				return fmt.Errorf("feature %s %s output recovery: %w", feature.ID, stage, recoveryErr)
			}
			if !recovered {
				continue
			}
		}
		if state.Phase != kernel.PhaseCompleted {
			if err := service.validateFeatureStageOutput(feature, stage, output); err != nil {
				if _, blockErr := service.blockStructuredDecisionTask(ctx, feature, task, state, invocation, snapshot, invalidPlanningOutputReason, "invalid-planning-output"); blockErr != nil {
					return fmt.Errorf("feature %s %s invalid-output block: %w", feature.ID, stage, blockErr)
				}
				continue
			}
			if err := service.completeEvidenceTask(ctx, feature, task, state, head, invocation, snapshot); err != nil {
				return err
			}
		}
		// The independent second-architect plan review was dropped after
		// qualification history (59 runs, 130+ recorded review branch results)
		// showed zero plan corrections while contributing two planning-path
		// stalls. Plan shape is still validated deterministically by
		// validateFeatureStageOutput above, and plan semantics are verified
		// downstream by per-story validation, security review, whole-feature
		// validation, and product acceptance.
		if err := service.resolveFeatureStageMessage(ctx, feature, invocation); err != nil {
			return fmt.Errorf("feature %s stage %s resolve handoff: %w", feature.ID, stage, err)
		}
		if err := service.applyFeatureStageOutput(ctx, feature, stage, invocation, output); err != nil {
			return fmt.Errorf("feature %s stage %s apply: %w", feature.ID, stage, err)
		}
	}
	return nil
}

func (service *ProductionService) reconcileFeatureArchitectureReview(ctx context.Context, feature organization.FeatureRequest, architectureInvocation kernel.WorkInvocation, architectureOutput []byte) (bool, error) {
	if feature.Specification == nil {
		return false, organization.ErrInvalidFeature
	}
	candidate, err := parseArchitectureStageResult(architectureOutput, featureAuthorizedActorFQNs(feature)...)
	if err != nil {
		return false, err
	}
	task, state, head, invocation, snapshot, err := service.ensureFeaturePlanningTask(ctx, feature, stageArchitectureReview)
	if errors.Is(err, errNewInvocationAdmissionSuspended) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if state.Condition == kernel.ConditionBlocked || invocation.State != kernel.InvocationSucceeded || invocation.OutputDigest == nil {
		return false, nil
	}
	output, err := service.Runtime.ReadExecutionOutput(ctx, *invocation.OutputDigest)
	if err != nil {
		return false, err
	}
	result, resultErr := parseArchitectureReviewStageResult(output)
	if resultErr != nil || result.ReviewedPlanDigest != *architectureInvocation.OutputDigest || !validArchitectureReviewCoverage(result, feature.Specification.Stories, candidate.Tasks) {
		if state.Phase != kernel.PhaseCompleted {
			if _, blockErr := service.blockStructuredDecisionTask(ctx, feature, task, state, invocation, snapshot, invalidPlanningOutputReason, "invalid-architecture-review-output"); blockErr != nil {
				return false, blockErr
			}
		}
		// The candidate cannot advance from this review. Release the author's
		// claimed organizational handoff just as an explicit FAIL does, so a
		// malformed reviewer response cannot monopolize the planning actor while
		// the blocked review awaits changed-condition recovery.
		if err := service.resolveFeatureStageMessageWithDisposition(ctx, feature, architectureInvocation, "STRUCTURED_HANDOFF_REVIEW_INVALID"); err != nil {
			return false, err
		}
		return false, nil
	}
	if result.Outcome != "PASS" {
		if state.Phase != kernel.PhaseCompleted {
			if _, blockErr := service.blockStructuredDecisionTask(ctx, feature, task, state, invocation, snapshot, failedArchitectureReviewReason, "architecture-review-not-pass"); blockErr != nil {
				return false, blockErr
			}
		}
		if err := service.resolveFeatureStageMessageWithDisposition(ctx, feature, architectureInvocation, "STRUCTURED_HANDOFF_REJECTED"); err != nil {
			return false, err
		}
		return false, nil
	}
	if service.validateFeatureStageOutput(feature, stageArchitecture, architectureOutput) != nil {
		return false, organization.ErrInvalidFeature
	}
	if state.Phase != kernel.PhaseCompleted {
		if err := service.completeEvidenceTask(ctx, feature, task, state, head, invocation, snapshot); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (service *ProductionService) reconcileFeatureArchitectureTaskReview(ctx context.Context, feature organization.FeatureRequest, architectureInvocation kernel.WorkInvocation, architectureOutput []byte, taskIndex uint32, candidateTasks []architectureTaskResult) (architectureTaskReviewProgress, error) {
	if taskIndex >= uint32(len(candidateTasks)) {
		return architectureTaskReviewPending, organization.ErrInvalidFeature
	}
	candidateTask := candidateTasks[taskIndex]
	task, state, head, invocation, snapshot, err := service.ensureFeaturePlanningTaskFor(ctx, feature, stageArchitectureTaskReview, &taskIndex)
	if errors.Is(err, errNewInvocationAdmissionSuspended) {
		return architectureTaskReviewPending, nil
	}
	if err != nil {
		return architectureTaskReviewPending, err
	}
	if state.Condition == kernel.ConditionBlocked {
		round, roundErr := architecturePlanRound(feature.ID, architectureInvocation.TaskID)
		if roundErr != nil {
			return architectureTaskReviewPending, roundErr
		}
		_, rejected, rejectionErr := service.architectureTaskReviewRejection(ctx, feature, round, architectureInvocation, candidateTasks, taskIndex)
		if rejectionErr != nil {
			return architectureTaskReviewPending, rejectionErr
		}
		if rejected {
			return architectureTaskReviewRejected, nil
		}
		return architectureTaskReviewPending, nil
	}
	if invocation.State != kernel.InvocationSucceeded || invocation.OutputDigest == nil {
		return architectureTaskReviewPending, nil
	}
	output, err := service.Runtime.ReadExecutionOutput(ctx, *invocation.OutputDigest)
	if err != nil {
		return architectureTaskReviewPending, err
	}
	result, resultErr := parseArchitectureTaskReviewStageResult(output)
	taskDigest, digestErr := featurePlanningStateDigest(candidateTask)
	validIdentity := resultErr == nil && digestErr == nil && result.ReviewedPlanDigest == *architectureInvocation.OutputDigest && result.ReviewedTaskIndex == taskIndex && result.ReviewedTaskDigest == taskDigest && validArchitectureTaskReviewCoverage(result, candidateTasks, taskIndex)
	if !validIdentity {
		if state.Phase != kernel.PhaseCompleted {
			if _, blockErr := service.blockStructuredDecisionTask(ctx, feature, task, state, invocation, snapshot, invalidPlanningOutputReason, "invalid-architecture-task-review-output"); blockErr != nil {
				return architectureTaskReviewPending, blockErr
			}
		}
		if err := service.resolveFeatureStageMessageWithDisposition(ctx, feature, architectureInvocation, "STRUCTURED_HANDOFF_REVIEW_INVALID"); err != nil {
			return architectureTaskReviewPending, err
		}
		return architectureTaskReviewPending, nil
	}
	if result.Outcome != "PASS" {
		if state.Phase != kernel.PhaseCompleted {
			if _, blockErr := service.blockStructuredDecisionTask(ctx, feature, task, state, invocation, snapshot, failedArchitectureTaskReviewReason, "architecture-task-review-not-pass"); blockErr != nil {
				return architectureTaskReviewPending, blockErr
			}
		}
		if err := service.resolveFeatureStageMessageWithDisposition(ctx, feature, architectureInvocation, "STRUCTURED_HANDOFF_REJECTED"); err != nil {
			return architectureTaskReviewPending, err
		}
		return architectureTaskReviewRejected, nil
	}
	if service.validateFeatureStageOutput(feature, stageArchitecture, architectureOutput) != nil {
		return architectureTaskReviewPending, organization.ErrInvalidFeature
	}
	if state.Phase != kernel.PhaseCompleted {
		if err := service.completeEvidenceTask(ctx, feature, task, state, head, invocation, snapshot); err != nil {
			return architectureTaskReviewPending, err
		}
	}
	return architectureTaskReviewPassed, nil
}

func validArchitectureTaskReviewCoverage(result architectureTaskReviewStageResult, tasks []architectureTaskResult, taskIndex uint32) bool {
	if taskIndex >= uint32(len(tasks)) {
		return false
	}
	task := tasks[taskIndex]
	dependencies, err := architectureTaskDependencyContracts(tasks, taskIndex)
	if err != nil || len(result.ReviewedDependencyIndexes) != len(dependencies) {
		return false
	}
	for index := range dependencies {
		if result.ReviewedDependencyIndexes[index] != dependencies[index].TaskIndex {
			return false
		}
	}
	// Promotion requires complete, exact coverage: every criterion must be
	// supported once and no material prescription may remain unverified. A
	// rejection is different. One identity-bound, evidenced unsupported
	// description or in-range criterion is already sufficient to prove that the
	// candidate task cannot advance as written. Requiring a rejecting reviewer
	// to serialize every other positive check correctly turns harmless surplus
	// review detail into a retry and can discard the decisive negative finding.
	// Keep that asymmetry explicit and fail-closed: incomplete or surplus output
	// can reject a candidate, but can never promote one.
	if result.Outcome == "FAIL" {
		if result.DescriptionOutcome == "UNSUPPORTED" || result.DescriptionRequiresTaskChange || len(result.UnverifiedPrescriptions) > 0 {
			return true
		}
		for _, check := range result.CriterionChecks {
			if check.CriterionIndex < uint32(len(task.AcceptanceCriteria)) && (check.Outcome == "UNSUPPORTED" || check.RequiresTaskChange) {
				return true
			}
		}
		return false
	}
	if len(result.CriterionChecks) != len(task.AcceptanceCriteria) {
		return false
	}
	allSupportedWithoutChange := result.DescriptionOutcome == "SUPPORTED" && !result.DescriptionRequiresTaskChange && len(result.UnverifiedPrescriptions) == 0
	seen := make(map[uint32]struct{}, len(result.CriterionChecks))
	for _, check := range result.CriterionChecks {
		if check.CriterionIndex >= uint32(len(task.AcceptanceCriteria)) {
			return false
		}
		if _, duplicate := seen[check.CriterionIndex]; duplicate {
			return false
		}
		seen[check.CriterionIndex] = struct{}{}
		allSupportedWithoutChange = allSupportedWithoutChange && check.Outcome == "SUPPORTED" && !check.RequiresTaskChange
	}
	return allSupportedWithoutChange
}

type architectureTaskDependencyContract struct {
	TaskIndex  uint32                 `json:"task_index"`
	TaskDigest kernel.Digest          `json:"task_sha256"`
	Task       architectureTaskResult `json:"task"`
}

func architectureTaskDependencyContracts(tasks []architectureTaskResult, taskIndex uint32) ([]architectureTaskDependencyContract, error) {
	if taskIndex >= uint32(len(tasks)) {
		return nil, organization.ErrInvalidFeature
	}
	included := make([]bool, taskIndex)
	var include func(uint32) error
	include = func(index uint32) error {
		if index >= taskIndex || index >= uint32(len(tasks)) {
			return organization.ErrInvalidFeature
		}
		if included[index] {
			return nil
		}
		for _, dependency := range tasks[index].DependsOn {
			if err := include(dependency); err != nil {
				return err
			}
		}
		included[index] = true
		return nil
	}
	for _, dependency := range tasks[taskIndex].DependsOn {
		if err := include(dependency); err != nil {
			return nil, err
		}
	}
	contracts := make([]architectureTaskDependencyContract, 0, taskIndex)
	for index, present := range included {
		if !present {
			continue
		}
		digest, err := featurePlanningStateDigest(tasks[index])
		if err != nil {
			return nil, err
		}
		contracts = append(contracts, architectureTaskDependencyContract{TaskIndex: uint32(index), TaskDigest: digest, Task: tasks[index]})
	}
	return contracts, nil
}

type planningOutputBlockPayload struct {
	BlockerRefs  []string `json:"blocker_refs"`
	Reason       string   `json:"reason"`
	ReviewPolicy string   `json:"review_policy"`
}

func (service *ProductionService) recoverValidFeatureStageOutput(ctx context.Context, feature organization.FeatureRequest, stage featurePlanningStage, task organization.PlannedTask, state kernel.AggregateState, head kernel.UUIDv7, invocation kernel.WorkInvocation, snapshot kernel.Snapshot, output []byte) (kernel.AggregateState, kernel.UUIDv7, kernel.Snapshot, bool, error) {
	blocked, found, err := service.Store.ReadEvent(ctx, head)
	if err != nil || !found {
		return state, head, snapshot, false, err
	}
	if !isExactInvalidPlanningOutputBlock(blocked, task, invocation, service.policyAuthority) || service.validateFeatureStageOutput(feature, stage, output) != nil {
		return state, head, snapshot, false, nil
	}
	evidence, err := evidenceForInvocations(snapshot, invocation)
	if err != nil {
		return state, head, snapshot, false, err
	}
	resolvedRef := "teams://work-invocation/" + string(invocation.ID)
	payload, err := json.Marshal(map[string]any{
		"resolved_blocker_refs": []string{resolvedRef},
		"evidence_ids":          evidenceIDs(evidence),
	})
	if err != nil {
		return state, head, snapshot, false, err
	}
	key := "planning-output-revalidated-" + string(task.ID) + "-" + string(*invocation.OutputDigest)
	if _, err := service.submitStandaloneCommand(ctx, "tekroo.command.work.unblock", kernel.AggregateTask, task.ID, service.policyAuthority, state.Revision, payload, []kernel.DagParent{{ParentEventID: head, EdgeKind: kernel.EdgeResponse}}, evidence, key); err != nil {
		return state, head, snapshot, false, err
	}
	state, head, found, err = service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID})
	if err != nil || !found || state.Condition != kernel.ConditionRunnable {
		return state, head, snapshot, false, errors.Join(organization.ErrInvalidFeature, err)
	}
	snapshot, err = service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}})
	if err != nil {
		return state, head, snapshot, false, err
	}
	return state, head, snapshot, true, nil
}

func isExactInvalidPlanningOutputBlock(event kernel.DomainEvent, task organization.PlannedTask, invocation kernel.WorkInvocation, authority kernel.PrincipalRef) bool {
	if event.EventID == "" || event.EventType != "tekroo.event.work.blocked" || event.Aggregate != (kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}) || event.Authority != authority || event.ActorFQN == nil || *event.ActorFQN != invocation.ActorFQN || event.Execution == nil || *event.Execution != invocation.Execution || len(event.Parents) != 1 || event.Parents[0] != (kernel.DagParent{ParentEventID: invocation.LastEventID, EdgeKind: kernel.EdgeResponse}) {
		return false
	}
	var payload planningOutputBlockPayload
	if json.Unmarshal(event.Payload, &payload) != nil {
		return false
	}
	return len(payload.BlockerRefs) == 1 && payload.BlockerRefs[0] == "teams://work-invocation/"+string(invocation.ID) && payload.Reason == invalidPlanningOutputReason && payload.ReviewPolicy == invalidPlanningReviewPolicy
}

func planningStage(status organization.FeatureStatus) (featurePlanningStage, bool) {
	switch status {
	case organization.FeatureSubmitted:
		return stageRefinement, true
	case organization.FeatureReadyForPlanning:
		return stageSpecification, true
	case organization.FeatureSpecified:
		return stageArchitecture, true
	default:
		return "", false
	}
}

func (service *ProductionService) ensureFeaturePlanningTask(ctx context.Context, feature organization.FeatureRequest, stage featurePlanningStage) (organization.PlannedTask, kernel.AggregateState, kernel.UUIDv7, kernel.WorkInvocation, kernel.Snapshot, error) {
	return service.ensureFeaturePlanningTaskFor(ctx, feature, stage, nil)
}

func (service *ProductionService) ensureFeaturePlanningTaskFor(ctx context.Context, feature organization.FeatureRequest, stage featurePlanningStage, reviewedTaskIndex *uint32) (organization.PlannedTask, kernel.AggregateState, kernel.UUIDv7, kernel.WorkInvocation, kernel.Snapshot, error) {
	round := uint32(0)
	var err error
	switch stage {
	case stageArchitecture:
		round, err = service.desiredArchitecturePlanRound(ctx, feature)
	case stageArchitectureTaskReview, stageArchitectureReview:
		var invocation kernel.WorkInvocation
		invocation, _, err = service.featureArchitectureCandidate(ctx, feature)
		if err == nil {
			round, err = architecturePlanRound(feature.ID, invocation.TaskID)
		}
	}
	if err != nil {
		return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, err
	}
	return service.ensureFeaturePlanningTaskForRound(ctx, feature, stage, reviewedTaskIndex, round)
}

func (service *ProductionService) ensureFeaturePlanningTaskForRound(ctx context.Context, feature organization.FeatureRequest, stage featurePlanningStage, reviewedTaskIndex *uint32, architectureRound uint32) (organization.PlannedTask, kernel.AggregateState, kernel.UUIDv7, kernel.WorkInvocation, kernel.Snapshot, error) {
	role, purpose, requiredRoute, title, description, criteria, err := planningStageDefinition(stage)
	if err != nil {
		return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, err
	}
	taskKey := featurePlanningTaskKey(stage, architectureRound, reviewedTaskIndex)
	taskID := deterministicOperationalUUID("feature-planning-task", string(feature.ID), taskKey)
	state, head, taskFound, err := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: taskID})
	if err != nil {
		return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, errors.Join(organization.ErrInvalidFeature, err)
	}
	if !taskFound && service.newInvocationAdmissionBlocked() {
		return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, errNewInvocationAdmissionSuspended
	}
	var excludedActor kernel.ActorFQN
	if stage == stageArchitectureReview || stage == stageArchitectureTaskReview {
		var architectureInvocation kernel.WorkInvocation
		var architectureOutput []byte
		architectureInvocation, architectureOutput, err = service.featureArchitectureCandidate(ctx, feature)
		if err == nil {
			var authorRole kernel.RoleFQRN
			authorRole, err = kernel.RoleFQRNFromActor(architectureInvocation.ActorFQN)
			if err == nil {
				role = string(authorRole)
				excludedActor = architectureInvocation.ActorFQN
			}
		}
		if err == nil && stage == stageArchitectureReview {
			description, err = featureArchitectureReviewDescription(feature, architectureInvocation, architectureOutput, description)
		} else if err == nil {
			if reviewedTaskIndex == nil {
				err = organization.ErrInvalidFeature
			} else {
				excludedActor = architectureInvocation.ActorFQN
				title = fmt.Sprintf("Review planned task %d for feasibility", *reviewedTaskIndex+1)
				description, err = featureArchitectureTaskReviewDescription(feature, architectureInvocation, architectureOutput, *reviewedTaskIndex, description)
			}
		}
	} else if stage == stageArchitecture && architectureRound > 0 {
		description, err = service.featureArchitectureSuccessorDescription(ctx, feature, architectureRound, description)
	} else {
		description, err = featurePlanningDescription(feature, stage, description)
	}
	if err != nil {
		return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, err
	}
	owner, err := service.ensureExactPlanningRoleExcept(ctx, role, excludedActor)
	if err != nil {
		return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, err
	}
	profileConfig, found := service.profilesByModel[owner.ModelProfile]
	workspace, workspaceFound := service.workspacesByID[owner.WorkspaceID]
	if !found || !workspaceFound || !requiredRoute.ModelExecutable() || !profileConfig.qualifiedFor(requiredRoute, workKindForPurpose(purpose, organization.RiskModerate), service.clock.Now().UTC()) {
		return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, organization.ErrInvalidFeature
	}
	storyEvent, err := service.ensureFeaturePlanningStory(ctx, feature)
	if err != nil {
		return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, err
	}
	evidenceID, evidence, err := service.ensureFeaturePlanningEvidence(ctx, feature)
	if err != nil {
		return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, err
	}
	deadline := feature.CreatedAt.Add(service.planningDeadline)
	budget, err := service.ensureFeatureWorkBudget(ctx, feature, evidenceID, evidence, deadline)
	if err != nil {
		return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, err
	}
	deadline = budget.DeadlineAt
	planningStoryID := deterministicOperationalUUID("feature-planning-story", string(feature.ID))

	dependsOn := []kernel.UUIDv7{}
	parents := []kernel.DagParent{{ParentEventID: storyEvent, EdgeKind: kernel.EdgeCausal}}
	if prior, hasPrior := priorPlanningStage(stage); hasPrior {
		priorID := deterministicOperationalUUID("feature-planning-task", string(feature.ID), string(prior))
		priorState, priorHead, priorFound, priorErr := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: priorID})
		if priorErr != nil || !priorFound || priorState.Phase != kernel.PhaseCompleted {
			return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, errors.Join(organization.ErrFeatureConflict, priorErr)
		}
		dependsOn = append(dependsOn, priorID)
		parents = append(parents, kernel.DagParent{ParentEventID: priorHead, EdgeKind: kernel.EdgeCausal})
	}
	if stage == stageArchitectureTaskReview || stage == stageArchitectureReview {
		architectureTaskID := featurePlanningTaskID(feature.ID, stageArchitecture, architectureRound, nil)
		architectureState, architectureHead, architectureFound, architectureErr := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: architectureTaskID})
		if architectureErr != nil || !architectureFound || architectureState.Phase != kernel.PhaseCompleted {
			return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, errors.Join(organization.ErrFeatureConflict, architectureErr)
		}
		dependsOn = append(dependsOn, architectureTaskID)
		parents = append(parents, kernel.DagParent{ParentEventID: architectureHead, EdgeKind: kernel.EdgeCausal})
	}
	if stage == stageArchitecture && architectureRound > 0 {
		predecessorID := featurePlanningTaskID(feature.ID, stageArchitecture, architectureRound-1, nil)
		predecessorState, predecessorHead, predecessorFound, predecessorErr := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: predecessorID})
		rejections, rejected, rejectionErr := service.architecturePlanRejections(ctx, feature, architectureRound-1)
		operatorCorrection := feature.PlanSupersession != nil && feature.PlanSupersession.ArchitectureRound == architectureRound
		if predecessorErr != nil || rejectionErr != nil || !predecessorFound || predecessorState.Phase != kernel.PhaseCompleted || !rejected && !operatorCorrection {
			return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, errors.Join(organization.ErrFeatureConflict, predecessorErr, rejectionErr)
		}
		dependsOn = append(dependsOn, predecessorID)
		parents = append(parents, kernel.DagParent{ParentEventID: predecessorHead, EdgeKind: kernel.EdgeCausal})
		for _, rejection := range rejections {
			parents = append(parents, kernel.DagParent{ParentEventID: rejection.ReviewHead, EdgeKind: kernel.EdgeCausal})
		}
		if operatorCorrection {
			for _, evidence := range feature.PlanSupersession.EvidenceRefs {
				_, evidenceHead, evidenceFound, evidenceErr := service.Store.ReadAggregateRevisionHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateEvidence, ID: evidence.EvidenceID})
				if evidenceErr != nil || !evidenceFound {
					return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, errors.Join(organization.ErrFeatureConflict, evidenceErr)
				}
				parents = append(parents, kernel.DagParent{ParentEventID: evidenceHead, EdgeKind: kernel.EdgeDerivation})
			}
		}
	}
	if stage == stageArchitectureTaskReview && reviewedTaskIndex != nil && *reviewedTaskIndex > 0 {
		priorIndex := *reviewedTaskIndex - 1
		priorReviewID := featurePlanningTaskID(feature.ID, stageArchitectureTaskReview, architectureRound, &priorIndex)
		priorReviewState, priorReviewHead, priorReviewFound, priorReviewErr := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: priorReviewID})
		if priorReviewErr != nil || !priorReviewFound {
			return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, errors.Join(organization.ErrFeatureConflict, priorReviewErr)
		}
		if priorReviewState.Phase == kernel.PhaseCompleted {
			dependsOn = append(dependsOn, priorReviewID)
			parents = append(parents, kernel.DagParent{ParentEventID: priorReviewHead, EdgeKind: kernel.EdgeCausal})
		} else {
			architectureInvocation, architectureOutput, candidateErr := service.featureArchitectureCandidateForRound(ctx, feature, architectureRound)
			if candidateErr != nil {
				return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, errors.Join(organization.ErrFeatureConflict, candidateErr)
			}
			candidate, parseErr := parseArchitectureStageResult(architectureOutput, featureAuthorizedActorFQNs(feature)...)
			if parseErr != nil {
				return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, errors.Join(organization.ErrFeatureConflict, parseErr)
			}
			_, rejected, rejectionErr := service.architectureTaskReviewRejection(ctx, feature, architectureRound, architectureInvocation, candidate.Tasks, priorIndex)
			if rejectionErr != nil || !rejected {
				return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, errors.Join(organization.ErrFeatureConflict, rejectionErr)
			}
			parents = append(parents, kernel.DagParent{ParentEventID: priorReviewHead, EdgeKind: kernel.EdgeCausal})
		}
	}
	task := organization.PlannedTask{ID: taskID, StoryID: planningStoryID, Title: title, Description: description, AcceptanceCriteria: criteria, DependsOn: dependsOn, Owner: owner.ActorFQN, ModelProfile: owner.ModelProfile, DecisionRoute: requiredRoute, Purpose: purpose, Complexity: 4, Risk: organization.RiskModerate, CriticalPath: true, AttemptLimit: 3, ReviewRoundLimit: 1}
	if !taskFound {
		payload, _ := json.Marshal(map[string]any{"story_id": planningStoryID, "title": task.Title, "description": task.Description, "acceptance_criteria": task.AcceptanceCriteria, "depends_on": task.DependsOn})
		if _, err := service.submitPlannedCommand(ctx, feature, "tekroo.command.task.create", kernel.AggregateTask, task.ID, "planning-task-"+string(stage), payload, parents); err != nil {
			return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, err
		}
		state, head, taskFound, err = service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID})
		if err != nil || !taskFound {
			return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, errors.Join(organization.ErrInvalidFeature, err)
		}
	}
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}})
	if err != nil {
		return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, err
	}
	if state.Phase == kernel.PhaseCompleted {
		invocation, _ := latestTaskInvocation(snapshot.WorkInvocations, task.ID)
		return task, state, head, invocation, snapshot, nil
	}
	if state.Phase == kernel.PhaseActive {
		if invocation, found := latestTaskInvocation(snapshot.WorkInvocations, task.ID); found {
			return task, state, head, invocation, snapshot, nil
		}
	}
	if service.newInvocationAdmissionBlocked() {
		return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, errNewInvocationAdmissionSuspended
	}
	if state.Phase != kernel.PhasePlanned && state.Phase != kernel.PhaseReady && state.Phase != kernel.PhaseActive {
		return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, organization.ErrInvalidFeature
	}
	if err := service.registerExecution(ctx, owner, profileConfig); err != nil {
		return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, err
	}
	profile := service.workProfile(feature, task, state.LifecycleEpoch, state.ScopeRevision, evidenceID, deadline)
	tracked := &trackedTask{plan: task, revision: state.Revision, last: head, profile: profile, owner: owner}
	if err := service.applyTaskCommand(ctx, feature, tracked, "tekroo.command.task.bind-work-profile", kernel.SchemaVersion, service.policyAuthority, profile, evidence, nil, "profile"); err != nil {
		return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, err
	}
	dependencyEvents := make([]kernel.UUIDv7, 0, len(dependsOn))
	for _, parent := range parents[1:] {
		dependencyEvents = append(dependencyEvents, parent.ParentEventID)
	}
	if err := service.activateTask(ctx, feature, tracked, profileConfig, workspace, budget.Revision, dependencyEvents, evidence, evidenceID, nil); err != nil {
		return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, err
	}
	state, head, _, err = service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID})
	if err != nil {
		return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, err
	}
	snapshot, err = service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}})
	if err != nil {
		return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, err
	}
	invocation, _ := latestTaskInvocation(snapshot.WorkInvocations, task.ID)
	return task, state, head, invocation, snapshot, nil
}

func planningStageDefinition(stage featurePlanningStage) (string, kernel.WorkPurpose, kernel.DecisionRoute, string, string, []string, error) {
	switch stage {
	case stageRefinement:
		return "product-owner", kernel.PurposeHandoff, kernel.RouteBoundedExecution, "Refine feature request", "Review the authoritative request for ambiguity and priority without changing its submitted acceptance criteria. Return exactly TEKROO_ORGANIZATIONAL_RESULT: followed by one JSON object with schema_version=1.0.0, result_type=FEATURE_REFINEMENT, acceptance_criteria_disposition=PRESERVE_SUBMITTED, clarification_questions (empty when none), and priority (LOW, NORMAL, HIGH, or CRITICAL). Do not add operational identities or delegate. Put only that result in finish.message and call finish once.", []string{"requirements are testable and ambiguities are explicit"}, nil
	case stageSpecification:
		return "project-manager", kernel.PurposeHandoff, kernel.RouteBoundedExecution, "Specify feature stories", "Turn the authoritative request into the smallest complete product specification. Default to one story. Split only when each proposed story would remain independently usable, testable, and releasable from the current baseline if every other proposed story were omitted. Apply that omission test before finish and merge dependent outcomes. A shared capability and its organization, persistence, operator surfaces, tests, documentation, and review are one story, not layer stories. Preserve every submitted acceptance criterion verbatim in at least one story; derived criteria may be added. Return exactly TEKROO_ORGANIZATIONAL_RESULT: followed by one JSON object with schema_version=1.0.0, result_type=FEATURE_SPECIFICATION, stories [{title, description, acceptance_criteria, priority}], and design_constraints. Priority is LOW, NORMAL, HIGH, or CRITICAL. Do not add operational identities or delegate. Put only that result in finish.message and call finish once.", []string{"stories are finite, testable, and within the accepted feature scope"}, nil
	case stageArchitecture:
		return "architect", kernel.PurposeReplan, kernel.RouteComplexReasoning, "Design executable feature DAG", "Read AGENTS.md and inspect relevant source, interfaces, and tests with read-only repository tools. Cite only repository-relative files actually inspected. Name an external package or API only when inspected repository evidence supports it; a dependency-manifest entry alone is insufficient. Otherwise describe the capability and make API verification an implementation responsibility. Produce the smallest complete acyclic plan. Every task is implementation work, fits one run, and has complexity at most authored_task_policy.maximum_task_complexity. Do not author test, validation, security-review, or product-acceptance tasks; Teams appends those and whole-feature validation independently. Describe the work, dependencies, complexity, and risk; do not select an operational role. Teams applies execution_routing_policy after validating the plan. Give each task measurable, task-specific acceptance criteria. The feature specification remains authoritative, and Teams carries its complete story-level acceptance criteria into independent whole-feature validation and final product acceptance; do not duplicate them across tasks merely for bookkeeping. For stateful or concurrent work identify invariants, ownership boundaries, state transitions, linearization points, and failure or compensation semantics. A read-then-act sequence is not proof of atomicity. Explain partial-failure safety without prescribing a particular implementation unless requirements or inspected architecture demand it. Assign each invariant to exactly one owning component or layer; other tasks consume that owner's abstraction. Lower-level storage and transport must not depend on higher-level workflow, deployment, or team configuration. Compare every task with the architecture and decisions; remove duplicate, conflicting, or inverted ownership. Self-check for one consistent state model and remove unresolved mutually exclusive alternatives. Treat changes that affect identity, authority, credentials, external control surfaces, or routing as HIGH risk. Compute materialized_total=2*len(tasks)+count(HIGH-or-CRITICAL tasks)+2 and keep it within task_budget.maximum_total_tasks. Combine cohesive work and obey maximum_moderate_or_lower_implementation_tasks. Obey authored_task_policy: purpose=IMPLEMENTATION, validates=[], and backward depends_on indexes. Risk is LOW, MODERATE, HIGH, or CRITICAL. Do not add contract verification unless a normative contract change is requested. Return exactly TEKROO_ORGANIZATIONAL_RESULT: then one JSON object with schema_version=1.0.0, result_type=FEATURE_PLAN, architecture, design_decisions, assumptions, and tasks. Each task has story_index, title, description, acceptance_criteria, depends_on, validates, purpose, complexity, risk, critical_path, attempt_limit, and review_round_limit. Do not edit, invent paths, assign operational identities, or delegate. Put only that result in finish.message and call finish once.", []string{"the result is a repository-grounded finite acyclic task plan with explicit validation"}, nil
	case stageArchitectureTaskReview:
		return "", kernel.PurposeReplan, kernel.RouteComplexReasoning, "Review one planned task for feasibility", "Read AGENTS.md, then perform a read-only technical review of the exact planned task in the authoritative state. Evaluate it against the current repository plus the supplied transitive dependency contracts: those dependencies are promised earlier DAG outcomes, so do not reject a task merely because a dependency-provided artifact is absent from the baseline. Do reject a missing or insufficient dependency contract. Independently inspect or execute non-mutating checks needed to test every operation, state mutation, invariant, concurrency or atomicity claim, external API capability, and partial-failure claim prescribed by the task. Review the task exactly as written. An alternative implementation, replacement mechanism, reordered transition, weakened invariant, or modified acceptance criterion means the task requires change and must produce FAIL; it is not evidence for PASS. A component, collection, method, dependency, or extension point existing is not proof that the prescribed transition is legal. Do not rely on remembered external-platform behavior as evidence: when available tools cannot prove a material external semantic claim, list it in unverified_prescriptions and return FAIL. Independently verdict the complete task description and every acceptance criterion. Copy reviewed_dependency_indexes exactly from expected_reviewed_dependency_indexes. Emit exactly one acceptance_criterion_checks entry for each index in expected_acceptance_criterion_indexes, in that order, and emit no other criterion indexes. Set description_requires_task_change or requires_task_change whenever feasibility depends on changing the reviewed text. PASS is valid only when the description and every criterion are SUPPORTED without task changes and unverified_prescriptions is empty. Do not edit files, redesign the task, select operational roles, or delegate. Return exactly TEKROO_ORGANIZATIONAL_RESULT: followed by one JSON object with schema_version=1.0.0, result_type=FEATURE_PLAN_TASK_REVIEW, outcome=PASS or FAIL, reviewed_plan_sha256, reviewed_task_index, reviewed_task_sha256, reviewed_dependency_indexes, description_outcome (SUPPORTED or UNSUPPORTED), description_requires_task_change, acceptance_criterion_checks [{criterion_index, outcome, requires_task_change, reasons, evidence}], verified_operations, unverified_prescriptions, reasons, and evidence. Put only that result in finish.message and call finish once.", []string{"the exact planned-task description and every acceptance criterion are independently verified as implementable without substitution"}, nil
	case stageArchitectureReview:
		return "architect", kernel.PurposeReplan, kernel.RouteComplexReasoning, "Review complete feature plan", "Read AGENTS.md, then independently inspect the current repository and review the exact plan. Decide whether every source story and planned task is materially supported as written and whether the tasks compose into one complete acyclic result. Focus on required capabilities, state ownership, invariants, concurrency and atomicity, dependency direction, interface compatibility, authorization, and partial-failure behavior. For every stated uniqueness, idempotency, or concurrency guarantee, identify its linearization point and verify that enforced constraints cover every key named by the invariant. Treat promised outputs of earlier DAG tasks as dependency contracts: do not reject an absent dependency-provided artifact, but reject a missing or insufficient dependency contract. Do not demand proof of ordinary implementation details left to the implementer. An alternative mechanism is not support when the prescribed mechanism cannot satisfy its invariant or source criterion. Treat command names, endpoint paths, tool names, payload fields, and output shapes explicitly stated by the source as compatibility requirements; any difference requires a finding unless the plan explicitly supplies a compatible alias or migration. Review every source-story description and criterion, every task description and criterion, and these plan subjects: STATE_OWNERSHIP, CONCURRENCY_ATOMICITY, DEPENDENCY_DIRECTION, INTERFACE_COMPATIBILITY, AUTHORIZATION_IDENTITY, PARTIAL_FAILURE. Continue after finding a failure so the author receives one complete review, but stop discovery once each item has enough evidence for a material decision. Keep supported-item notes private. In coverage, report the exact story_count, each story_acceptance_criterion_counts value in story order, task_count, each task_acceptance_criterion_counts value in task order, and plan_check_subjects in the listed order. Emit findings only for material unsupported items. Each finding has subject STORY_DESCRIPTION, STORY_ACCEPTANCE_CRITERION, TASK_DESCRIPTION, TASK_ACCEPTANCE_CRITERION, or PLAN_CHECK; include the applicable zero-based story_index, task_index, criterion_index, or plan_subject plus concise reasons and evidence. Do not emit repetitive per-item supported verdicts. Put unverifiable material claims in unverified_claims. PASS only when findings and unverified_claims are both empty; otherwise FAIL. Do not edit files, redesign the feature, select operational roles, or delegate. Return exactly TEKROO_ORGANIZATIONAL_RESULT: followed by one JSON object with schema_version=1.0.0, result_type=FEATURE_PLAN_REVIEW, outcome, reviewed_plan_sha256 copied from authoritative state, coverage, findings, unverified_claims, reasons, and evidence containing repository-relative files or concrete platform semantics actually checked. Put only that result in finish.message and call finish once.", []string{"every source story and planned task is materially supported and the complete plan composes into one consistent implementable result"}, nil
	default:
		return "", "", "", "", "", nil, organization.ErrInvalidFeature
	}
}

func featurePlanningDescription(feature organization.FeatureRequest, stage featurePlanningStage, instruction string) (string, error) {
	type sourceDigests struct {
		Input         kernel.Digest `json:"input_sha256"`
		Refinement    kernel.Digest `json:"refinement_sha256"`
		Specification kernel.Digest `json:"specification_sha256"`
	}
	type refinementSummary struct {
		PreparedBy               kernel.ActorFQN              `json:"prepared_by"`
		PreparedExecution        kernel.ExecutionTuple        `json:"prepared_execution"`
		AcceptanceCriteriaDigest kernel.Digest                `json:"acceptance_criteria_sha256"`
		ClarificationQuestions   []string                     `json:"clarification_questions"`
		Priority                 organization.FeaturePriority `json:"priority"`
		PreparedAt               time.Time                    `json:"prepared_at"`
	}
	var state any
	switch stage {
	case stageRefinement:
		state = struct {
			FeatureID kernel.UUIDv7                    `json:"feature_id"`
			Input     organization.FeatureRequestInput `json:"input"`
		}{FeatureID: feature.ID, Input: feature.Input}
	case stageSpecification:
		if feature.Refinement == nil {
			return "", organization.ErrInvalidFeature
		}
		inputDigest, err := featurePlanningStateDigest(feature.Input)
		if err != nil {
			return "", err
		}
		criteriaDigest, err := featurePlanningStateDigest(feature.Refinement.AcceptanceCriteria)
		if err != nil {
			return "", err
		}
		state = struct {
			FeatureID   kernel.UUIDv7                    `json:"feature_id"`
			InputDigest kernel.Digest                    `json:"input_sha256"`
			Input       organization.FeatureRequestInput `json:"input"`
			Refinement  refinementSummary                `json:"refinement"`
		}{
			FeatureID: feature.ID, InputDigest: inputDigest, Input: feature.Input,
			Refinement: refinementSummary{
				PreparedBy: feature.Refinement.PreparedBy, PreparedExecution: feature.Refinement.PreparedExecution,
				AcceptanceCriteriaDigest: criteriaDigest,
				ClarificationQuestions:   append([]string(nil), feature.Refinement.ClarificationQuestions...),
				Priority:                 feature.Refinement.Priority, PreparedAt: feature.Refinement.PreparedAt,
			},
		}
	case stageArchitecture:
		if feature.Refinement == nil || feature.Specification == nil {
			return "", organization.ErrInvalidFeature
		}
		inputDigest, err := featurePlanningStateDigest(feature.Input)
		if err != nil {
			return "", err
		}
		refinementDigest, err := featurePlanningStateDigest(feature.Refinement)
		if err != nil {
			return "", err
		}
		specificationDigest, err := featurePlanningStateDigest(feature.Specification)
		if err != nil {
			return "", err
		}
		type taskBudget struct {
			MaximumTotalTasks                                 uint32 `json:"maximum_total_tasks"`
			ValidationTasksPerImplementation                  uint32 `json:"validation_tasks_per_implementation"`
			AdditionalSecurityReviewPerHighRiskImplementation uint32 `json:"additional_security_review_per_high_risk_implementation"`
			ReservedFeatureValidationTasks                    uint32 `json:"reserved_feature_validation_tasks"`
			ReservedProductAcceptanceTasks                    uint32 `json:"reserved_product_acceptance_tasks"`
			MaximumModerateOrLowerImplementationTasks         uint32 `json:"maximum_moderate_or_lower_implementation_tasks"`
		}
		type authoredTaskPolicy struct {
			AllowedPurposes       []kernel.WorkPurpose `json:"allowed_purposes"`
			MaximumTaskComplexity uint8                `json:"maximum_task_complexity"`
			MayAuthorValidates    bool                 `json:"may_author_validates"`
			RoleSelectionOwner    string               `json:"role_selection_owner"`
			TasksAreSerialized    bool                 `json:"tasks_are_serialized"`
		}
		maximumImplementationTasks := uint32(0)
		if feature.Input.MaximumTasks > 2 {
			maximumImplementationTasks = (feature.Input.MaximumTasks - 2) / 2
		}
		state = struct {
			FeatureID          kernel.UUIDv7                     `json:"feature_id"`
			Sources            sourceDigests                     `json:"source_sha256"`
			RequestTitle       string                            `json:"request_title"`
			RequestDescription string                            `json:"request_description"`
			Repository         string                            `json:"repository"`
			RequestConstraints []string                          `json:"request_constraints"`
			MaximumTasks       uint32                            `json:"maximum_tasks"`
			MaximumHops        uint32                            `json:"maximum_hops"`
			TaskBudget         taskBudget                        `json:"task_budget"`
			AuthoredTaskPolicy authoredTaskPolicy                `json:"authored_task_policy"`
			ExecutionRouting   workflowTaskRoutingPolicy         `json:"execution_routing_policy"`
			Specification      organization.FeatureSpecification `json:"specification"`
		}{
			FeatureID:    feature.ID,
			Sources:      sourceDigests{Input: inputDigest, Refinement: refinementDigest, Specification: specificationDigest},
			RequestTitle: feature.Input.Title, RequestDescription: feature.Input.Description,
			Repository: feature.Input.Repository, RequestConstraints: append([]string(nil), feature.Input.Constraints...),
			MaximumTasks: feature.Input.MaximumTasks, MaximumHops: feature.Input.MaximumHops,
			TaskBudget: taskBudget{
				MaximumTotalTasks: feature.Input.MaximumTasks, ValidationTasksPerImplementation: 1,
				AdditionalSecurityReviewPerHighRiskImplementation: 1, ReservedFeatureValidationTasks: 1, ReservedProductAcceptanceTasks: 1,
				MaximumModerateOrLowerImplementationTasks: maximumImplementationTasks,
			},
			AuthoredTaskPolicy: authoredTaskPolicy{
				AllowedPurposes:       []kernel.WorkPurpose{kernel.PurposeImplementation},
				MaximumTaskComplexity: 6,
				MayAuthorValidates:    false,
				RoleSelectionOwner:    "TEAMS_ROUTING_POLICY",
				TasksAreSerialized:    false,
			},
			ExecutionRouting: softwareDevelopmentTaskRoutingPolicy(),
			Specification:    *feature.Specification,
		}
	default:
		return "", organization.ErrInvalidFeature
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	description := instruction + "\n\nAUTHORITATIVE_FEATURE_STATE_JSON:\n" + string(encoded)
	if len(description) > 65536 {
		return "", organization.ErrInvalidFeature
	}
	return description, nil
}

func featurePlanningTaskKey(stage featurePlanningStage, architectureRound uint32, reviewedTaskIndex *uint32) string {
	if architectureRound == 0 {
		if reviewedTaskIndex == nil {
			return string(stage)
		}
		return fmt.Sprintf("%s-%d", stage, *reviewedTaskIndex)
	}
	if reviewedTaskIndex == nil {
		return fmt.Sprintf("%s-round-%d", stage, architectureRound)
	}
	return fmt.Sprintf("%s-round-%d-task-%d", stage, architectureRound, *reviewedTaskIndex)
}

func featurePlanningTaskID(featureID kernel.UUIDv7, stage featurePlanningStage, architectureRound uint32, reviewedTaskIndex *uint32) kernel.UUIDv7 {
	return deterministicOperationalUUID("feature-planning-task", string(featureID), featurePlanningTaskKey(stage, architectureRound, reviewedTaskIndex))
}

func architecturePlanRound(featureID, taskID kernel.UUIDv7) (uint32, error) {
	for round := uint32(0); round <= architecturePlanRecordedRoundLimit; round++ {
		if featurePlanningTaskID(featureID, stageArchitecture, round, nil) == taskID {
			return round, nil
		}
	}
	return 0, organization.ErrInvalidFeature
}

func (service *ProductionService) latestArchitecturePlanRound(ctx context.Context, feature organization.FeatureRequest) (uint32, error) {
	for round := uint32(0); round <= architecturePlanRecordedRoundLimit; round++ {
		_, _, found, err := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: featurePlanningTaskID(feature.ID, stageArchitecture, round, nil)})
		if err != nil {
			return 0, err
		}
		if !found && round == 0 {
			return 0, nil
		}
		if !found {
			return round - 1, nil
		}
	}
	return architecturePlanRecordedRoundLimit, nil
}

func (service *ProductionService) desiredArchitecturePlanRound(ctx context.Context, feature organization.FeatureRequest) (uint32, error) {
	round, err := service.latestArchitecturePlanRound(ctx, feature)
	if err != nil {
		return round, err
	}
	maximumAutomaticRound := architectureAutomaticSuccessorLimit
	if feature.PlanSupersession != nil {
		if round < feature.PlanSupersession.ArchitectureRound {
			return feature.PlanSupersession.ArchitectureRound, nil
		}
		if feature.PlanSupersession.ArchitectureRound < architecturePlanRecordedRoundLimit {
			maximumAutomaticRound = feature.PlanSupersession.ArchitectureRound + 1
		}
	}
	if round >= maximumAutomaticRound {
		return round, nil
	}
	_, rejected, err := service.architecturePlanRejections(ctx, feature, round)
	if err != nil {
		return 0, err
	}
	if rejected {
		return round + 1, nil
	}
	return round, nil
}

func (service *ProductionService) featureArchitectureCandidate(ctx context.Context, feature organization.FeatureRequest) (kernel.WorkInvocation, []byte, error) {
	round, err := service.latestArchitecturePlanRound(ctx, feature)
	if err != nil {
		return kernel.WorkInvocation{}, nil, err
	}
	return service.featureArchitectureCandidateForRound(ctx, feature, round)
}

func (service *ProductionService) featureArchitectureCandidateForRound(ctx context.Context, feature organization.FeatureRequest, round uint32) (kernel.WorkInvocation, []byte, error) {
	taskID := featurePlanningTaskID(feature.ID, stageArchitecture, round, nil)
	state, _, found, err := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: taskID})
	if err != nil || !found || state.Phase != kernel.PhaseCompleted {
		return kernel.WorkInvocation{}, nil, errors.Join(organization.ErrFeatureConflict, err)
	}
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: taskID}})
	if err != nil {
		return kernel.WorkInvocation{}, nil, err
	}
	invocation, found := latestTaskInvocation(snapshot.WorkInvocations, taskID)
	if !found || invocation.State != kernel.InvocationSucceeded || invocation.OutputDigest == nil {
		return kernel.WorkInvocation{}, nil, organization.ErrInvalidFeature
	}
	output, err := service.Runtime.ReadExecutionOutput(ctx, *invocation.OutputDigest)
	if err != nil || digestBytes(output) != *invocation.OutputDigest || service.validateFeatureStageOutput(feature, stageArchitecture, output) != nil {
		return kernel.WorkInvocation{}, nil, errors.Join(organization.ErrInvalidFeature, err)
	}
	return invocation, output, nil
}

func (service *ProductionService) architecturePlanRejections(ctx context.Context, feature organization.FeatureRequest, round uint32) ([]architecturePlanRejection, bool, error) {
	architectureTaskID := featurePlanningTaskID(feature.ID, stageArchitecture, round, nil)
	architectureState, _, found, err := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: architectureTaskID})
	if err != nil {
		return nil, false, err
	}
	if !found || architectureState.Phase != kernel.PhaseCompleted {
		return nil, false, nil
	}
	architectureInvocation, architectureOutput, err := service.featureArchitectureCandidateForRound(ctx, feature, round)
	if err != nil {
		return nil, false, err
	}
	candidate, err := parseArchitectureStageResult(architectureOutput, featureAuthorizedActorFQNs(feature)...)
	if err != nil {
		return nil, false, err
	}
	integrationRejection, rejected, err := service.architectureIntegrationReviewRejection(ctx, feature, round, architectureInvocation, candidate.Tasks)
	if err != nil || !rejected {
		return nil, false, err
	}
	return []architecturePlanRejection{integrationRejection}, true, nil
}

func (service *ProductionService) architectureTaskReviewRejection(ctx context.Context, feature organization.FeatureRequest, round uint32, architectureInvocation kernel.WorkInvocation, candidateTasks []architectureTaskResult, taskIndex uint32) (architecturePlanRejection, bool, error) {
	reviewTaskID := featurePlanningTaskID(feature.ID, stageArchitectureTaskReview, round, &taskIndex)
	state, head, found, err := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: reviewTaskID})
	if err != nil || !found || state.Condition != kernel.ConditionBlocked {
		return architecturePlanRejection{}, false, err
	}
	event, eventFound, err := service.Store.ReadEvent(ctx, head)
	if err != nil || !eventFound {
		return architecturePlanRejection{}, false, err
	}
	var block planningOutputBlockPayload
	if json.Unmarshal(event.Payload, &block) != nil || block.Reason != failedArchitectureTaskReviewReason && block.Reason != invalidPlanningOutputReason {
		return architecturePlanRejection{}, false, nil
	}
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: reviewTaskID}})
	if err != nil {
		return architecturePlanRejection{}, false, err
	}
	invocation, found := latestTaskInvocation(snapshot.WorkInvocations, reviewTaskID)
	if !found || invocation.State != kernel.InvocationSucceeded || invocation.OutputDigest == nil {
		return architecturePlanRejection{}, false, organization.ErrInvalidFeature
	}
	output, err := service.Runtime.ReadExecutionOutput(ctx, *invocation.OutputDigest)
	if err != nil || digestBytes(output) != *invocation.OutputDigest {
		return architecturePlanRejection{}, false, errors.Join(organization.ErrInvalidFeature, err)
	}
	result, resultErr := parseArchitectureTaskReviewStageResult(output)
	taskDigest, digestErr := featurePlanningStateDigest(candidateTasks[taskIndex])
	valid := resultErr == nil && digestErr == nil && result.Outcome == "FAIL" && architectureInvocation.OutputDigest != nil && result.ReviewedPlanDigest == *architectureInvocation.OutputDigest && result.ReviewedTaskIndex == taskIndex && result.ReviewedTaskDigest == taskDigest && validArchitectureTaskReviewCoverage(result, candidateTasks, taskIndex)
	if !valid {
		return architecturePlanRejection{}, false, nil
	}
	// A prior binary may have classified this same immutable output as invalid
	// because it demanded complete positive coverage from a rejecting review.
	// Re-evaluate exact invalid-output blocks so a repaired fail-closed validator
	// can consume the decisive negative finding without another model call.
	return architecturePlanRejection{Round: round, ReviewTaskID: reviewTaskID, ReviewHead: head, ReviewInvocation: invocation, ReviewOutput: output}, true, nil
}

func (service *ProductionService) architectureIntegrationReviewRejection(ctx context.Context, feature organization.FeatureRequest, round uint32, architectureInvocation kernel.WorkInvocation, candidateTasks []architectureTaskResult) (architecturePlanRejection, bool, error) {
	if feature.Specification == nil {
		return architecturePlanRejection{}, false, organization.ErrInvalidFeature
	}
	reviewTaskID := featurePlanningTaskID(feature.ID, stageArchitectureReview, round, nil)
	state, head, found, err := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: reviewTaskID})
	if err != nil || !found || state.Condition != kernel.ConditionBlocked {
		return architecturePlanRejection{}, false, err
	}
	event, eventFound, err := service.Store.ReadEvent(ctx, head)
	if err != nil || !eventFound {
		return architecturePlanRejection{}, false, err
	}
	var block planningOutputBlockPayload
	if json.Unmarshal(event.Payload, &block) != nil || block.Reason != failedArchitectureReviewReason {
		return architecturePlanRejection{}, false, nil
	}
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: reviewTaskID}})
	if err != nil {
		return architecturePlanRejection{}, false, err
	}
	invocation, found := latestTaskInvocation(snapshot.WorkInvocations, reviewTaskID)
	if !found || invocation.State != kernel.InvocationSucceeded || invocation.OutputDigest == nil {
		return architecturePlanRejection{}, false, organization.ErrInvalidFeature
	}
	output, err := service.Runtime.ReadExecutionOutput(ctx, *invocation.OutputDigest)
	if err != nil || digestBytes(output) != *invocation.OutputDigest {
		return architecturePlanRejection{}, false, errors.Join(organization.ErrInvalidFeature, err)
	}
	result, resultErr := parseArchitectureReviewStageResult(output)
	valid := resultErr == nil && result.Outcome == "FAIL" && architectureInvocation.OutputDigest != nil && result.ReviewedPlanDigest == *architectureInvocation.OutputDigest && validArchitectureReviewCoverage(result, feature.Specification.Stories, candidateTasks)
	if !valid {
		return architecturePlanRejection{}, false, nil
	}
	return architecturePlanRejection{Round: round, ReviewTaskID: reviewTaskID, ReviewHead: head, ReviewInvocation: invocation, ReviewOutput: output}, true, nil
}

func (service *ProductionService) featureArchitectureSuccessorDescription(ctx context.Context, feature organization.FeatureRequest, round uint32, instruction string) (string, error) {
	if round == 0 || round > architecturePlanRecordedRoundLimit {
		return "", organization.ErrInvalidFeature
	}
	predecessorInvocation, predecessorOutput, err := service.featureArchitectureCandidateForRound(ctx, feature, round-1)
	if err != nil || predecessorInvocation.OutputDigest == nil {
		return "", errors.Join(organization.ErrInvalidFeature, err)
	}
	rejections, rejected, err := service.architecturePlanRejections(ctx, feature, round-1)
	operatorCorrection := feature.PlanSupersession != nil && feature.PlanSupersession.ArchitectureRound == round
	if err != nil || !rejected && !operatorCorrection || rejected && len(rejections) == 0 {
		return "", errors.Join(organization.ErrInvalidFeature, err)
	}
	base, err := featurePlanningDescription(feature, stageArchitecture, instruction)
	if err != nil {
		return "", err
	}
	type rejectedReview struct {
		TaskID       kernel.UUIDv7 `json:"task_id"`
		OutputSHA256 kernel.Digest `json:"output_sha256"`
		Output       string        `json:"output"`
	}
	reviewFeedback := make([]rejectedReview, len(rejections))
	for index, rejection := range rejections {
		if rejection.ReviewInvocation.OutputDigest == nil {
			return "", organization.ErrInvalidFeature
		}
		reviewFeedback[index] = rejectedReview{rejection.ReviewTaskID, *rejection.ReviewInvocation.OutputDigest, string(rejection.ReviewOutput)}
	}
	type supersessionFeedback struct {
		PlanVersion  uint64               `json:"plan_version"`
		PlanSHA256   kernel.Digest        `json:"plan_sha256"`
		Reason       string               `json:"reason"`
		EvidenceRefs []kernel.EvidenceRef `json:"evidence_refs"`
	}
	var correction *supersessionFeedback
	if operatorCorrection {
		correction = &supersessionFeedback{
			PlanVersion: feature.PlanSupersession.PlanVersion, PlanSHA256: feature.PlanSupersession.PlanDigest,
			Reason: feature.PlanSupersession.Reason, EvidenceRefs: append([]kernel.EvidenceRef(nil), feature.PlanSupersession.EvidenceRefs...),
		}
	}
	feedback := struct {
		SuccessorRound               uint32                `json:"successor_round"`
		PredecessorPlanSHA256        kernel.Digest         `json:"predecessor_plan_sha256"`
		PredecessorPlanOutput        string                `json:"predecessor_plan_output"`
		RejectedReviews              []rejectedReview      `json:"rejected_reviews"`
		OperatorCorrection           *supersessionFeedback `json:"operator_correction,omitempty"`
		FurtherAutomaticSuccessorMax uint32                `json:"further_automatic_successor_max"`
	}{round, *predecessorInvocation.OutputDigest, string(predecessorOutput), reviewFeedback, correction, 0}
	if round < architecturePlanRecordedRoundLimit {
		feedback.FurtherAutomaticSuccessorMax = 1
	}
	encoded, err := json.Marshal(feedback)
	if err != nil {
		return "", err
	}
	correctionInstruction := "An independent review rejected the preceding plan. Produce one corrected successor plan using the immutable feedback below. Do not repeat a prescription the review proved unsupported."
	if operatorCorrection && !rejected {
		correctionInstruction = "The operator authorized correction of the materialized plan. Produce one corrected successor plan using the immutable reason and evidence references below."
	}
	description := base + "\n\n" + correctionInstruction + "\n\nAUTHORITATIVE_PLAN_REVIEW_FEEDBACK_JSON:\n" + string(encoded)
	if len(description) > 768<<10 {
		return "", organization.ErrInvalidFeature
	}
	return description, nil
}

func featureArchitectureReviewDescription(feature organization.FeatureRequest, invocation kernel.WorkInvocation, output []byte, instruction string) (string, error) {
	if invocation.OutputDigest == nil || digestBytes(output) != *invocation.OutputDigest || feature.Specification == nil {
		return "", organization.ErrInvalidFeature
	}
	candidate, err := parseArchitectureStageResult(output, featureAuthorizedActorFQNs(feature)...)
	if err != nil {
		return "", err
	}
	inputDigest, err := featurePlanningStateDigest(feature.Input)
	if err != nil {
		return "", err
	}
	specificationDigest, err := featurePlanningStateDigest(feature.Specification)
	if err != nil {
		return "", err
	}
	state := struct {
		FeatureID             kernel.UUIDv7                     `json:"feature_id"`
		Repository            string                            `json:"repository"`
		InputSHA256           kernel.Digest                     `json:"input_sha256"`
		SpecificationSHA256   kernel.Digest                     `json:"specification_sha256"`
		CandidateOutputSHA256 kernel.Digest                     `json:"candidate_output_sha256"`
		Input                 organization.FeatureRequestInput  `json:"input"`
		Specification         organization.FeatureSpecification `json:"specification"`
		Candidate             architectureStageResult           `json:"candidate"`
	}{
		FeatureID: feature.ID, Repository: feature.Input.Repository,
		InputSHA256: inputDigest, SpecificationSHA256: specificationDigest,
		CandidateOutputSHA256: *invocation.OutputDigest, Input: feature.Input,
		Specification: *feature.Specification, Candidate: candidate,
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	description := instruction + "\n\nAUTHORITATIVE_FEATURE_PLAN_REVIEW_STATE_JSON:\n" + string(encoded)
	if len(description) > 768<<10 {
		return "", organization.ErrInvalidFeature
	}
	return description, nil
}

func featureArchitectureTaskReviewDescription(feature organization.FeatureRequest, invocation kernel.WorkInvocation, output []byte, taskIndex uint32, instruction string) (string, error) {
	if invocation.OutputDigest == nil || digestBytes(output) != *invocation.OutputDigest || feature.Specification == nil {
		return "", organization.ErrInvalidFeature
	}
	candidate, err := parseArchitectureStageResult(output, featureAuthorizedActorFQNs(feature)...)
	if err != nil || taskIndex >= uint32(len(candidate.Tasks)) {
		return "", errors.Join(organization.ErrInvalidFeature, err)
	}
	taskDigest, err := featurePlanningStateDigest(candidate.Tasks[taskIndex])
	if err != nil {
		return "", err
	}
	dependencyContracts, err := architectureTaskDependencyContracts(candidate.Tasks, taskIndex)
	if err != nil {
		return "", err
	}
	inputDigest, err := featurePlanningStateDigest(feature.Input)
	if err != nil {
		return "", err
	}
	specificationDigest, err := featurePlanningStateDigest(feature.Specification)
	if err != nil {
		return "", err
	}
	expectedDependencyIndexes := make([]uint32, len(dependencyContracts))
	for index, dependency := range dependencyContracts {
		expectedDependencyIndexes[index] = dependency.TaskIndex
	}
	expectedCriterionIndexes := make([]uint32, len(candidate.Tasks[taskIndex].AcceptanceCriteria))
	for index := range expectedCriterionIndexes {
		expectedCriterionIndexes[index] = uint32(index)
	}
	state := struct {
		FeatureID             kernel.UUIDv7                        `json:"feature_id"`
		Repository            string                               `json:"repository"`
		InputSHA256           kernel.Digest                        `json:"input_sha256"`
		SpecificationSHA256   kernel.Digest                        `json:"specification_sha256"`
		CandidateOutputSHA256 kernel.Digest                        `json:"reviewed_plan_sha256"`
		ReviewedTaskIndex     uint32                               `json:"reviewed_task_index"`
		ReviewedTaskSHA256    kernel.Digest                        `json:"reviewed_task_sha256"`
		ExpectedDependencies  []uint32                             `json:"expected_reviewed_dependency_indexes"`
		ExpectedCriteria      []uint32                             `json:"expected_acceptance_criterion_indexes"`
		RequestConstraints    []string                             `json:"request_constraints"`
		Specification         organization.FeatureSpecification    `json:"specification"`
		Architecture          string                               `json:"architecture"`
		DesignDecisions       []string                             `json:"design_decisions"`
		Assumptions           []string                             `json:"assumptions"`
		DependencyContracts   []architectureTaskDependencyContract `json:"dependency_contracts"`
		Task                  architectureTaskResult               `json:"task"`
	}{
		FeatureID: feature.ID, Repository: feature.Input.Repository,
		InputSHA256: inputDigest, SpecificationSHA256: specificationDigest,
		CandidateOutputSHA256: *invocation.OutputDigest, ReviewedTaskIndex: taskIndex,
		ReviewedTaskSHA256: taskDigest, ExpectedDependencies: expectedDependencyIndexes,
		ExpectedCriteria: expectedCriterionIndexes, RequestConstraints: append([]string(nil), feature.Input.Constraints...),
		Specification: *feature.Specification, Architecture: string(candidate.Architecture),
		DesignDecisions: append([]string(nil), candidate.DesignDecisions...), Assumptions: append([]string(nil), candidate.Assumptions...),
		DependencyContracts: dependencyContracts,
		Task:                candidate.Tasks[taskIndex],
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	description := instruction + "\n\nAUTHORITATIVE_FEATURE_PLAN_TASK_REVIEW_STATE_JSON:\n" + string(encoded)
	if len(description) > 256<<10 {
		return "", organization.ErrInvalidFeature
	}
	return description, nil
}

func featurePlanningStateDigest(value any) (kernel.Digest, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return kernel.Digest(fmt.Sprintf("%x", digest[:])), nil
}

func specificationPreservesSubmittedCriteria(submitted []string, result specificationStageResult) bool {
	preserved := make(map[string]struct{})
	for _, story := range result.Stories {
		for _, criterion := range story.AcceptanceCriteria {
			preserved[criterion] = struct{}{}
		}
	}
	for _, criterion := range submitted {
		if _, found := preserved[criterion]; !found {
			return false
		}
	}
	return true
}

func priorPlanningStage(stage featurePlanningStage) (featurePlanningStage, bool) {
	switch stage {
	case stageSpecification:
		return stageRefinement, true
	case stageArchitecture:
		return stageSpecification, true
	default:
		return "", false
	}
}

func (service *ProductionService) ensureExactPlanningRole(ctx context.Context, role string) (organization.RoleInstanceState, error) {
	return service.ensureExactPlanningRoleExcept(ctx, role, "")
}

type planningRoleAllocator struct {
	host organization.FeatureRoleHost
	next map[string]int
}

func newPlanningRoleAllocator(service *ProductionService) *planningRoleAllocator {
	if service == nil {
		return &planningRoleAllocator{next: make(map[string]int)}
	}
	return &planningRoleAllocator{host: service.RoleHost, next: make(map[string]int)}
}

func (allocator *planningRoleAllocator) ensure(ctx context.Context, role string) (organization.RoleInstanceState, error) {
	if allocator == nil || allocator.host == nil {
		return organization.RoleInstanceState{}, organization.ErrRoleNotRunning
	}
	actors, err := allocator.host.ConfiguredRoleActors(role)
	if err != nil || len(actors) == 0 {
		return organization.RoleInstanceState{}, errors.Join(organization.ErrRoleNotRunning, err)
	}
	sort.Slice(actors, func(left, right int) bool { return actors[left] < actors[right] })
	start := allocator.next[role] % len(actors)
	failures := make([]error, 0, len(actors)*2)
	for offset := 0; offset < len(actors); offset++ {
		index := (start + offset) % len(actors)
		selected := actors[index]
		state, active, statusErr := allocator.host.Status(ctx, selected)
		if statusErr == nil && active && state.Status == organization.RoleIdle {
			allocator.next[role] = index + 1
			return state, nil
		}
		state, startErr := allocator.host.EnsureStarted(ctx, selected)
		if startErr == nil && state.Status == organization.RoleIdle {
			allocator.next[role] = index + 1
			return state, nil
		}
		failures = append(failures, statusErr, startErr)
	}
	return organization.RoleInstanceState{}, errors.Join(append([]error{organization.ErrRoleNotRunning}, failures...)...)
}

func (service *ProductionService) ensureExactPlanningRoleExcept(ctx context.Context, role string, excluded kernel.ActorFQN) (organization.RoleInstanceState, error) {
	actors, err := service.RoleHost.ConfiguredRoleActors(role)
	if err != nil {
		return organization.RoleInstanceState{}, errors.Join(organization.ErrRoleNotRunning, err)
	}
	return service.ensurePlanningActorExcept(ctx, actors, excluded)
}

func (service *ProductionService) ensurePlanningActorExcept(ctx context.Context, actors []kernel.ActorFQN, excluded kernel.ActorFQN) (organization.RoleInstanceState, error) {
	if len(actors) == 0 {
		return organization.RoleInstanceState{}, organization.ErrRoleNotRunning
	}
	sort.Slice(actors, func(left, right int) bool { return actors[left] < actors[right] })
	var selected kernel.ActorFQN
	for _, actor := range actors {
		if actor != excluded {
			selected = actor
			break
		}
	}
	if !selected.Valid() {
		return organization.RoleInstanceState{}, organization.ErrRoleNotRunning
	}
	state, active, err := service.RoleHost.Status(ctx, selected)
	if err == nil && active && state.Status == organization.RoleIdle {
		return state, nil
	}
	state, startErr := service.RoleHost.EnsureStarted(ctx, selected)
	if startErr != nil || state.Status != organization.RoleIdle {
		return organization.RoleInstanceState{}, errors.Join(organization.ErrRoleNotRunning, err, startErr)
	}
	return state, nil
}

func (service *ProductionService) ensureFeaturePlanningEvidence(ctx context.Context, feature organization.FeatureRequest) (kernel.UUIDv7, []kernel.EvidenceRef, error) {
	encoded, err := json.Marshal(feature.Input)
	if err != nil {
		return "", nil, err
	}
	digest := digestBytes(encoded)
	id := deterministicOperationalUUID("feature-request-evidence", string(feature.ID), string(digest))
	ref := kernel.AggregateRef{Kind: kernel.AggregateEvidence, ID: id}
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: ref})
	if err != nil {
		return "", nil, err
	}
	if metadata, found := snapshot.Evidence[id]; found {
		if !metadata.Available || metadata.SHA256 != digest {
			return "", nil, organization.ErrInvalidFeature
		}
		return id, []kernel.EvidenceRef{{EvidenceID: id, SHA256: digest}}, nil
	}
	payload, _ := json.Marshal(map[string]any{"access_partition": feature.Input.WorkspaceID, "availability": "AVAILABLE", "byte_length": len(encoded), "canonical_digest": digest, "computation": nil, "deletion_tombstone": nil, "evidence_kind": "DECISION_RECORD", "integrity_state": "DIGEST_VERIFIED", "locator": "teams://feature/" + string(feature.ID) + "/request", "locator_immutable": true, "media_type": "application/json", "producing_component": "tekrood-feature-intake", "producing_version": FeaturePlanningVersion, "redacts": nil, "retention_policy": "feature-lifecycle", "sensitivity": "INTERNAL", "sha256": digest, "source_evidence_ids": []kernel.UUIDv7{}, "source_timestamp": feature.CreatedAt, "transport_provenance": "teams-operator-feature-intake"})
	if _, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.evidence.register", kernel.SchemaVersion, kernel.AggregateEvidence, id, service.serviceAuthority, 0, payload, nil, nil, "feature-request-evidence"); err != nil {
		return "", nil, err
	}
	return id, []kernel.EvidenceRef{{EvidenceID: id, SHA256: digest}}, nil
}

func (service *ProductionService) validateFeatureStageOutput(feature organization.FeatureRequest, stage featurePlanningStage, output []byte) error {
	allowedActorFQNs := featureAuthorizedActorFQNs(feature)
	switch stage {
	case stageRefinement:
		_, err := parseRefinementStageResult(output, allowedActorFQNs...)
		return err
	case stageSpecification:
		result, err := parseSpecificationStageResult(output, allowedActorFQNs...)
		if err != nil || !specificationPreservesSubmittedCriteria(feature.Input.AcceptanceCriteria, result) {
			return organization.ErrInvalidFeature
		}
		return nil
	case stageArchitecture:
		result, err := parseArchitectureStageResult(output, allowedActorFQNs...)
		if err != nil {
			return err
		}
		_, err = normalizeArchitecturePlanTasks(feature, result.Tasks)
		return err
	case stageArchitectureTaskReview:
		_, err := parseArchitectureTaskReviewStageResult(output)
		return err
	case stageArchitectureReview:
		_, err := parseArchitectureReviewStageResult(output)
		return err
	default:
		return organization.ErrInvalidFeature
	}
}

func (service *ProductionService) applyFeatureStageOutput(ctx context.Context, feature organization.FeatureRequest, stage featurePlanningStage, invocation kernel.WorkInvocation, output []byte) error {
	allowedActorFQNs := featureAuthorizedActorFQNs(feature)
	preparedAt := service.clock.Now().UTC()
	if invocation.FinishedAt != nil {
		preparedAt = invocation.FinishedAt.UTC()
	}
	switch stage {
	case stageRefinement:
		result, err := parseRefinementStageResult(output, allowedActorFQNs...)
		if err != nil {
			return err
		}
		_, err = service.Features.Refine(ctx, feature.ID, feature.Revision, organization.FeatureRefinement{PreparedBy: invocation.ActorFQN, PreparedExecution: invocation.Execution, AcceptanceCriteria: append([]string(nil), feature.Input.AcceptanceCriteria...), ClarificationQuestions: result.ClarificationQuestions, Priority: result.Priority, PreparedAt: preparedAt})
		return err
	case stageSpecification:
		result, err := parseSpecificationStageResult(output, allowedActorFQNs...)
		if err != nil || !specificationPreservesSubmittedCriteria(feature.Input.AcceptanceCriteria, result) {
			return organization.ErrInvalidFeature
		}
		stories := make([]organization.PlannedStory, len(result.Stories))
		for index, item := range result.Stories {
			stories[index] = organization.PlannedStory{ID: deterministicOperationalUUID("feature-story", string(feature.ID), fmt.Sprint(index), item.Title), Title: item.Title, Description: item.Description, AcceptanceCriteria: item.AcceptanceCriteria, Priority: item.Priority}
		}
		_, err = service.Features.Specify(ctx, feature.ID, feature.Revision, organization.FeatureSpecification{PreparedBy: invocation.ActorFQN, PreparedExecution: invocation.Execution, Stories: stories, DesignConstraints: result.DesignConstraints, PreparedAt: preparedAt})
		return err
	case stageArchitecture:
		result, err := parseArchitectureStageResult(output, allowedActorFQNs...)
		if err != nil {
			return err
		}
		plan, err := service.buildExecutableFeaturePlan(ctx, feature, invocation, result, preparedAt)
		if err != nil {
			return err
		}
		_, err = service.Features.ApplyPlan(ctx, feature.ID, feature.Revision, plan)
		return err
	default:
		return organization.ErrInvalidFeature
	}
}

func (service *ProductionService) buildExecutableFeaturePlan(ctx context.Context, feature organization.FeatureRequest, invocation kernel.WorkInvocation, result architectureStageResult, createdAt time.Time) (organization.FeaturePlan, error) {
	normalizedTasks, err := normalizeArchitecturePlanTasks(feature, result.Tasks)
	if err != nil {
		return organization.FeaturePlan{}, err
	}
	result.Tasks = normalizedTasks
	planVersion := uint64(1)
	if feature.Plan != nil {
		if feature.PlanSupersession == nil || feature.PlanSupersession.PlanVersion != feature.Plan.Version {
			return organization.FeaturePlan{}, organization.ErrInvalidFeature
		}
		planVersion = feature.Plan.Version + 1
	}
	planStories := append([]organization.PlannedStory(nil), feature.Specification.Stories...)
	if planVersion > 1 {
		for index := range planStories {
			planStories[index].ID = deterministicOperationalUUID("feature-story", string(feature.ID), fmt.Sprint(planVersion), fmt.Sprint(index), planStories[index].Title)
		}
	}
	taskIDs := make([]kernel.UUIDv7, len(result.Tasks))
	for index := range taskIDs {
		if planVersion == 1 {
			taskIDs[index] = deterministicOperationalUUID("feature-task", string(feature.ID), fmt.Sprint(index), result.Tasks[index].Title)
		} else {
			taskIDs[index] = deterministicOperationalUUID("feature-task", string(feature.ID), fmt.Sprint(planVersion), fmt.Sprint(index), result.Tasks[index].Title)
		}
	}
	allocator := newPlanningRoleAllocator(service)
	tasks := make([]organization.PlannedTask, 0, feature.Input.MaximumTasks)
	for index, item := range result.Tasks {
		if int(item.StoryIndex) >= len(feature.Specification.Stories) {
			return organization.FeaturePlan{}, organization.ErrInvalidFeature
		}
		if item.Purpose != kernel.PurposeImplementation && item.Purpose != kernel.PurposeInvestigation && item.Purpose != kernel.PurposeValidation && item.Purpose != kernel.PurposeReview {
			return organization.FeaturePlan{}, organization.ErrInvalidFeature
		}
		owner, err := allocator.ensure(ctx, item.Role)
		if err != nil {
			return organization.FeaturePlan{}, err
		}
		profile, found := service.profilesByModel[owner.ModelProfile]
		if !found || !profile.qualifiedFor(profile.DecisionRoute, workKindForPurpose(item.Purpose, item.Risk), service.clock.Now().UTC()) {
			return organization.FeaturePlan{}, organization.ErrInvalidFeature
		}
		dependencies := indexesToTaskIDs(item.DependsOn, taskIDs)
		validates := indexesToTaskIDs(item.Validates, taskIDs)
		tasks = append(tasks, organization.PlannedTask{ID: taskIDs[index], StoryID: planStories[item.StoryIndex].ID, Title: item.Title, Description: item.Description, AcceptanceCriteria: item.AcceptanceCriteria, DependsOn: dependencies, Validates: validates, Owner: owner.ActorFQN, ModelProfile: owner.ModelProfile, DecisionRoute: profile.DecisionRoute, Purpose: item.Purpose, Complexity: item.Complexity, Risk: item.Risk, CriticalPath: item.CriticalPath, AttemptLimit: item.AttemptLimit, ReviewRoundLimit: item.ReviewRoundLimit})
	}
	tasks, err = service.addRequiredValidationTasks(ctx, feature, tasks, allocator)
	if err != nil {
		return organization.FeaturePlan{}, err
	}
	tasks, err = service.addFeatureValidationTask(ctx, feature, planStories, tasks, planVersion, allocator)
	if err != nil {
		return organization.FeaturePlan{}, err
	}
	tasks, err = service.addStoryAcceptanceTasks(ctx, feature, planStories, tasks, planVersion, allocator)
	if err != nil {
		return organization.FeaturePlan{}, err
	}
	tasks, err = service.bindValidationWorkspaceContext(ctx, tasks)
	if err != nil {
		return organization.FeaturePlan{}, err
	}
	plan := organization.FeaturePlan{Version: planVersion, PreparedBy: invocation.ActorFQN, PreparedExecution: invocation.Execution, Architecture: string(result.Architecture), DesignDecisions: result.DesignDecisions, Assumptions: result.Assumptions, Stories: planStories, Tasks: tasks, CreatedAt: createdAt}
	if plan.Validate(feature) != nil {
		return organization.FeaturePlan{}, organization.ErrInvalidFeature
	}
	return plan, nil
}

func normalizeArchitecturePlanTasks(feature organization.FeatureRequest, tasks []architectureTaskResult) ([]architectureTaskResult, error) {
	if feature.Specification == nil || requiredMaterializedTaskCount(tasks) > int(feature.Input.MaximumTasks) {
		return nil, organization.ErrInvalidFeature
	}
	normalizedTasks, err := normalizeArchitectureTaskCriteria(*feature.Specification, tasks)
	if err != nil {
		return nil, err
	}
	return normalizeArchitectureTaskRelations(normalizedTasks, softwareDevelopmentTaskRoutingPolicy())
}

func requiredMaterializedTaskCount(tasks []architectureTaskResult) int {
	count := len(tasks) + 2 // whole-feature validation and final product acceptance
	for _, task := range tasks {
		if task.Purpose != kernel.PurposeImplementation {
			continue
		}
		count++ // independent tester task
		if task.Risk == organization.RiskHigh || task.Risk == organization.RiskCritical {
			count++ // independent security-review task
		}
	}
	return count
}

func featureValidationTaskID(featureID kernel.UUIDv7, planVersion uint64) kernel.UUIDv7 {
	if planVersion > 1 {
		return deterministicOperationalUUID("feature-validation", string(featureID), fmt.Sprint(planVersion))
	}
	return deterministicOperationalUUID("feature-validation", string(featureID))
}

// normalizeArchitectureTaskCriteria keeps task-local completion checks separate
// from authoritative story acceptance. The latter is preserved in the feature
// specification and carried into Teams-generated final acceptance work. Legacy
// numeric coverage hints are discarded because they have no operational role.
func normalizeArchitectureTaskCriteria(specification organization.FeatureSpecification, tasks []architectureTaskResult) ([]architectureTaskResult, error) {
	result := make([]architectureTaskResult, len(tasks))
	for index, task := range tasks {
		result[index] = task
		result[index].AcceptanceCriteria = append([]string(nil), task.AcceptanceCriteria...)
		result[index].Covers = nil
		if int(task.StoryIndex) >= len(specification.Stories) {
			return nil, organization.ErrInvalidFeature
		}
		if !validStageStrings(result[index].AcceptanceCriteria, true) {
			return nil, organization.ErrInvalidFeature
		}
	}
	return result, nil
}

type planComplexityRoleBand struct {
	MinimumPlanComplexity uint8  `json:"minimum_plan_complexity"`
	Role                  string `json:"role"`
}

type purposeTaskRoutingPolicy struct {
	Purpose                  kernel.WorkPurpose       `json:"purpose"`
	MaximumTaskComplexity    uint8                    `json:"maximum_task_complexity"`
	MayAuthorValidationLinks bool                     `json:"may_author_validation_links"`
	SerializeTasks           bool                     `json:"serialize_tasks"`
	PlanComplexityRoleBands  []planComplexityRoleBand `json:"plan_complexity_role_bands"`
}

type workflowTaskRoutingPolicy struct {
	PurposeRoutes []purposeTaskRoutingPolicy `json:"purpose_routes"`
}

// softwareDevelopmentTaskRoutingPolicy is supplied workflow data. The routing
// engine below has no knowledge of these role names and accepts any FQRN.
func softwareDevelopmentTaskRoutingPolicy() workflowTaskRoutingPolicy {
	return workflowTaskRoutingPolicy{PurposeRoutes: []purposeTaskRoutingPolicy{{
		Purpose:                  kernel.PurposeImplementation,
		MaximumTaskComplexity:    6,
		MayAuthorValidationLinks: false,
		SerializeTasks:           false,
		PlanComplexityRoleBands: []planComplexityRoleBand{
			{MinimumPlanComplexity: 1, Role: "coder"},
			{MinimumPlanComplexity: 5, Role: "senior-coder"},
		},
	}}}
}

func normalizeArchitectureTaskRelations(tasks []architectureTaskResult, policy workflowTaskRoutingPolicy) ([]architectureTaskResult, error) {
	result := append([]architectureTaskResult(nil), tasks...)
	routes := make(map[kernel.WorkPurpose]purposeTaskRoutingPolicy, len(policy.PurposeRoutes))
	maximumComplexity := make(map[kernel.WorkPurpose]uint8, len(policy.PurposeRoutes))
	lastTask := make(map[kernel.WorkPurpose]int, len(policy.PurposeRoutes))
	for _, route := range policy.PurposeRoutes {
		if !route.Purpose.Valid() || route.MaximumTaskComplexity == 0 || len(route.PlanComplexityRoleBands) == 0 {
			return nil, organization.ErrInvalidFeature
		}
		if _, duplicate := routes[route.Purpose]; duplicate {
			return nil, organization.ErrInvalidFeature
		}
		thresholds := make(map[uint8]struct{}, len(route.PlanComplexityRoleBands))
		for _, band := range route.PlanComplexityRoleBands {
			if _, err := kernel.ParseRoleFQRN(band.Role); band.MinimumPlanComplexity == 0 || band.MinimumPlanComplexity > route.MaximumTaskComplexity || err != nil {
				return nil, organization.ErrInvalidFeature
			}
			if _, duplicate := thresholds[band.MinimumPlanComplexity]; duplicate {
				return nil, organization.ErrInvalidFeature
			}
			thresholds[band.MinimumPlanComplexity] = struct{}{}
		}
		routes[route.Purpose] = route
		lastTask[route.Purpose] = -1
	}
	for index := range result {
		result[index].DependsOn = append([]uint32(nil), result[index].DependsOn...)
		result[index].Validates = append([]uint32(nil), result[index].Validates...)
		route, found := routes[result[index].Purpose]
		if !found || result[index].Complexity > route.MaximumTaskComplexity || !route.MayAuthorValidationLinks && len(result[index].Validates) != 0 {
			return nil, organization.ErrInvalidFeature
		}
		if result[index].Complexity > maximumComplexity[result[index].Purpose] {
			maximumComplexity[result[index].Purpose] = result[index].Complexity
		}
		if route.SerializeTasks {
			dependencies := make(map[uint32]struct{}, len(result[index].DependsOn)+1)
			for _, dependency := range result[index].DependsOn {
				dependencies[dependency] = struct{}{}
			}
			if lastTask[result[index].Purpose] >= 0 {
				prior := uint32(lastTask[result[index].Purpose])
				if _, found := dependencies[prior]; !found {
					result[index].DependsOn = append(result[index].DependsOn, prior)
				}
			}
			sort.Slice(result[index].DependsOn, func(left, right int) bool { return result[index].DependsOn[left] < result[index].DependsOn[right] })
			lastTask[result[index].Purpose] = index
		}
	}
	for purpose, complexity := range maximumComplexity {
		route := routes[purpose]
		selectedRole := ""
		selectedThreshold := uint8(0)
		for _, band := range route.PlanComplexityRoleBands {
			if band.MinimumPlanComplexity <= complexity && band.MinimumPlanComplexity > selectedThreshold {
				selectedRole = band.Role
				selectedThreshold = band.MinimumPlanComplexity
			}
		}
		if selectedRole == "" {
			return nil, organization.ErrInvalidFeature
		}
		for index := range result {
			if result[index].Purpose == purpose {
				result[index].Role = selectedRole
			}
		}
	}
	return result, nil
}

func (service *ProductionService) bindValidationWorkspaceContext(ctx context.Context, tasks []organization.PlannedTask) ([]organization.PlannedTask, error) {
	byID := make(map[kernel.UUIDv7]organization.PlannedTask, len(tasks))
	for _, task := range tasks {
		byID[task.ID] = task
	}
	result := append([]organization.PlannedTask(nil), tasks...)
	for index := range result {
		if len(result[index].Validates) == 0 {
			continue
		}
		for _, targetID := range result[index].Validates {
			target, found := byID[targetID]
			if !found {
				return nil, organization.ErrInvalidFeature
			}
			owner, active, err := service.RoleHost.Status(ctx, target.Owner)
			if err != nil || !active {
				return nil, errors.Join(organization.ErrRoleNotRunning, err)
			}
			if _, found := service.workspacesByID[owner.WorkspaceID]; !found {
				return nil, organization.ErrInvalidFeature
			}
		}
		result[index].Description += "\n\nTeams will materialize and preflight one immutable candidate workspace before this task starts. Validate only the candidate identity supplied in the execution brief. Do not locate, enumerate, or switch branches, and do not modify the candidate."
		if len(result[index].Description) > 65536 {
			return nil, organization.ErrInvalidFeature
		}
	}
	return result, nil
}

func indexesToTaskIDs(indexes []uint32, ids []kernel.UUIDv7) []kernel.UUIDv7 {
	result := make([]kernel.UUIDv7, len(indexes))
	for index, value := range indexes {
		result[index] = ids[value]
	}
	return result
}

func (service *ProductionService) addRequiredValidationTasks(ctx context.Context, feature organization.FeatureRequest, tasks []organization.PlannedTask, allocator *planningRoleAllocator) ([]organization.PlannedTask, error) {
	coverage := make(map[kernel.UUIDv7]map[kernel.WorkPurpose]bool)
	for _, task := range tasks {
		for _, target := range task.Validates {
			if coverage[target] == nil {
				coverage[target] = make(map[kernel.WorkPurpose]bool)
			}
			coverage[target][task.Purpose] = true
		}
	}
	for _, target := range append([]organization.PlannedTask(nil), tasks...) {
		if target.Purpose != kernel.PurposeImplementation && target.Purpose != kernel.PurposeRepair {
			continue
		}
		if !coverage[target.ID][kernel.PurposeValidation] {
			validator, err := allocator.ensure(ctx, "tester")
			if err != nil {
				return nil, err
			}
			profile, found := service.profilesByModel[validator.ModelProfile]
			workKind := workKindForPurpose(kernel.PurposeValidation, target.Risk)
			if !found || !profile.qualifiedFor(profile.DecisionRoute, workKind, service.clock.Now().UTC()) {
				return nil, fmt.Errorf("tester profile %s is not qualified for %s: %w", validator.ModelProfile, workKind, organization.ErrInvalidFeature)
			}
			id := deterministicOperationalUUID("required-validator", string(feature.ID), string(target.ID), "tester")
			tasks = append(tasks, organization.PlannedTask{ID: id, StoryID: target.StoryID, Title: "Validate: " + target.Title, Description: "Independently inspect the implementation, run the acceptance checks, and report the structured validation result.", AcceptanceCriteria: append([]string(nil), target.AcceptanceCriteria...), DependsOn: []kernel.UUIDv7{target.ID}, Validates: []kernel.UUIDv7{target.ID}, Owner: validator.ActorFQN, ModelProfile: validator.ModelProfile, DecisionRoute: profile.DecisionRoute, Purpose: kernel.PurposeValidation, Complexity: target.Complexity, Risk: target.Risk, CriticalPath: true, AttemptLimit: target.ReviewRoundLimit + 1, ReviewRoundLimit: target.ReviewRoundLimit})
			if coverage[target.ID] == nil {
				coverage[target.ID] = make(map[kernel.WorkPurpose]bool)
			}
			coverage[target.ID][kernel.PurposeValidation] = true
		}
		if (target.Risk == organization.RiskHigh || target.Risk == organization.RiskCritical) && !coverage[target.ID][kernel.PurposeReview] {
			security, err := allocator.ensure(ctx, "security")
			if err != nil {
				return nil, err
			}
			profile, found := service.profilesByModel[security.ModelProfile]
			workKind := workKindForPurpose(kernel.PurposeReview, target.Risk)
			if !found || !profile.qualifiedFor(profile.DecisionRoute, workKind, service.clock.Now().UTC()) {
				return nil, fmt.Errorf("security profile %s is not qualified for %s: %w", security.ModelProfile, workKind, organization.ErrInvalidFeature)
			}
			description, err := securityReviewDescription(target)
			if err != nil {
				return nil, err
			}
			id := deterministicOperationalUUID("required-validator", string(feature.ID), string(target.ID), "security")
			tasks = append(tasks, organization.PlannedTask{ID: id, StoryID: target.StoryID, Title: "Security review: " + target.Title, Description: description, AcceptanceCriteria: securityReviewAcceptanceCriteria(), DependsOn: []kernel.UUIDv7{target.ID}, Validates: []kernel.UUIDv7{target.ID}, Owner: security.ActorFQN, ModelProfile: security.ModelProfile, DecisionRoute: profile.DecisionRoute, Purpose: kernel.PurposeReview, Complexity: target.Complexity, Risk: target.Risk, CriticalPath: true, AttemptLimit: 2, ReviewRoundLimit: target.ReviewRoundLimit})
		}
		if len(tasks) > int(feature.Input.MaximumTasks) {
			return nil, organization.ErrInvalidFeature
		}
	}
	return tasks, nil
}

// addFeatureValidationTask adds one independent validation of the complete
// assembled candidate against the authoritative story criteria. Per-task
// validators prove local handoffs; this task prevents criteria lost during
// task decomposition from reaching product acceptance unnoticed.
func (service *ProductionService) addFeatureValidationTask(ctx context.Context, feature organization.FeatureRequest, stories []organization.PlannedStory, tasks []organization.PlannedTask, planVersion uint64, allocator *planningRoleAllocator) ([]organization.PlannedTask, error) {
	if len(stories) == 0 || planVersion == 0 {
		return nil, organization.ErrInvalidFeature
	}
	var finalImplementation *organization.PlannedTask
	maximumComplexity := uint8(1)
	risk := organization.RiskLow
	dependencies := make([]kernel.UUIDv7, 0, len(tasks))
	for index := range tasks {
		task := &tasks[index]
		if task.Purpose != kernel.PurposeImplementation && task.Purpose != kernel.PurposeRepair {
			continue
		}
		dependencies = append(dependencies, task.ID)
		finalImplementation = task
		if task.Complexity > maximumComplexity {
			maximumComplexity = task.Complexity
		}
		if riskOrdinal(task.Risk) > riskOrdinal(risk) {
			risk = task.Risk
		}
	}
	if finalImplementation == nil {
		return nil, organization.ErrInvalidFeature
	}
	validator, err := allocator.ensure(ctx, "tester")
	if err != nil {
		return nil, err
	}
	profile, found := service.profilesByModel[validator.ModelProfile]
	workKind := workKindForPurpose(kernel.PurposeValidation, risk)
	if !found || !profile.qualifiedFor(profile.DecisionRoute, workKind, service.clock.Now().UTC()) {
		return nil, fmt.Errorf("tester profile %s is not qualified for %s: %w", validator.ModelProfile, workKind, organization.ErrInvalidFeature)
	}
	criteria := make([]string, 0)
	for _, story := range stories {
		for _, criterion := range story.AcceptanceCriteria {
			criteria = append(criteria, story.Title+": "+criterion)
		}
	}
	sort.Slice(dependencies, func(left, right int) bool { return dependencies[left] < dependencies[right] })
	tasks = append(tasks, organization.PlannedTask{
		ID:                 featureValidationTaskID(feature.ID, planVersion),
		StoryID:            stories[0].ID,
		Title:              "Validate complete feature: " + feature.Input.Title,
		Description:        "Independently inspect and test the complete assembled candidate against every authoritative story acceptance criterion. Report failures even when every task-local validation passed. Do not make product-priority or release decisions.",
		AcceptanceCriteria: criteria,
		DependsOn:          dependencies,
		Validates:          []kernel.UUIDv7{finalImplementation.ID},
		Owner:              validator.ActorFQN,
		ModelProfile:       validator.ModelProfile,
		DecisionRoute:      profile.DecisionRoute,
		Purpose:            kernel.PurposeValidation,
		Complexity:         maximumComplexity,
		Risk:               risk,
		CriticalPath:       true,
		AttemptLimit:       finalImplementation.ReviewRoundLimit + 1,
		ReviewRoundLimit:   finalImplementation.ReviewRoundLimit,
	})
	if len(tasks) > int(feature.Input.MaximumTasks) {
		return nil, organization.ErrInvalidFeature
	}
	return tasks, nil
}

func securityReviewDescription(target organization.PlannedTask) (string, error) {
	contextValue := struct {
		Title              string   `json:"title"`
		Description        string   `json:"description"`
		AcceptanceCriteria []string `json:"acceptance_criteria"`
	}{
		Title:              target.Title,
		Description:        target.Description,
		AcceptanceCriteria: append([]string(nil), target.AcceptanceCriteria...),
	}
	encoded, err := json.Marshal(contextValue)
	if err != nil {
		return "", err
	}
	description := "Independently inspect the candidate for security and authority-boundary defects implicated by the implementation context below. The implementation criteria are context, not security-review acceptance criteria. Do not make a general functional, architecture, release, or product-acceptance determination.\n\nTARGET_IMPLEMENTATION_CONTEXT_JSON:\n" + string(encoded)
	if len(description) > 65536 {
		return "", organization.ErrInvalidFeature
	}
	return description, nil
}

func securityReviewAcceptanceCriteria() []string {
	return []string{
		"the candidate is assessed only for security and authority-boundary defects implicated by the supplied implementation context",
		"every security finding and every no-finding conclusion cites inspected candidate evidence or executed security-check evidence",
		"the result makes no general functional, architecture, release, or product-acceptance determination",
	}
}

func (service *ProductionService) addStoryAcceptanceTasks(ctx context.Context, feature organization.FeatureRequest, stories []organization.PlannedStory, tasks []organization.PlannedTask, planVersion uint64, allocator *planningRoleAllocator) ([]organization.PlannedTask, error) {
	if len(stories) == 0 || planVersion == 0 {
		return nil, organization.ErrInvalidFeature
	}
	productOwner, err := allocator.ensure(ctx, "product-owner")
	if err != nil {
		return nil, err
	}
	profile, found := service.profilesByModel[productOwner.ModelProfile]
	if !found || !profile.qualifiedFor(profile.DecisionRoute, kernel.WorkRelease, service.clock.Now().UTC()) {
		return nil, organization.ErrInvalidFeature
	}
	dependencies := make([]kernel.UUIDv7, len(tasks))
	criteria := make([]string, 0)
	maximumComplexity := uint8(1)
	risk := organization.RiskLow
	for index, task := range tasks {
		dependencies[index] = task.ID
		if task.Purpose == kernel.PurposeImplementation || task.Purpose == kernel.PurposeRepair {
			owner, active, statusErr := service.RoleHost.Status(ctx, task.Owner)
			workspace, workspaceFound := service.workspacesByID[owner.WorkspaceID]
			if statusErr != nil || !active || !workspaceFound {
				return nil, errors.Join(organization.ErrRoleNotRunning, statusErr)
			}
			_ = workspace
		}
		if task.Complexity > maximumComplexity {
			maximumComplexity = task.Complexity
		}
		if riskOrdinal(task.Risk) > riskOrdinal(risk) {
			risk = task.Risk
		}
	}
	for _, story := range feature.Specification.Stories {
		for _, criterion := range story.AcceptanceCriteria {
			criteria = append(criteria, story.Title+": "+criterion)
		}
	}
	sort.Slice(dependencies, func(left, right int) bool { return dependencies[left] < dependencies[right] })
	description := "Review the completed feature evidence against every story acceptance criterion and return the structured product acceptance result. Teams will materialize and preflight the same immutable candidate used by validation before this task starts. Evaluate only that candidate and the supplied validation evidence. Do not locate or switch branches, modify the candidate, delegate, or start another agent."
	id := deterministicOperationalUUID("feature-acceptance", string(feature.ID))
	if planVersion > 1 {
		id = deterministicOperationalUUID("feature-acceptance", string(feature.ID), fmt.Sprint(planVersion))
	}
	tasks = append(tasks, organization.PlannedTask{
		ID: id, StoryID: stories[0].ID, Title: "Accept feature: " + feature.Input.Title,
		Description:        description,
		AcceptanceCriteria: criteria, DependsOn: dependencies,
		Owner: productOwner.ActorFQN, ModelProfile: productOwner.ModelProfile, DecisionRoute: profile.DecisionRoute,
		Purpose: kernel.PurposePromotion, Complexity: maximumComplexity, Risk: risk, CriticalPath: true,
		AttemptLimit: 2, ReviewRoundLimit: 1,
	})
	if len(tasks) > int(feature.Input.MaximumTasks) {
		return nil, organization.ErrInvalidFeature
	}
	return tasks, nil
}

func riskOrdinal(risk organization.RiskLevel) int {
	switch risk {
	case organization.RiskCritical:
		return 4
	case organization.RiskHigh:
		return 3
	case organization.RiskModerate:
		return 2
	default:
		return 1
	}
}

func (service *ProductionService) resolveFeatureStageMessage(ctx context.Context, feature organization.FeatureRequest, invocation kernel.WorkInvocation) error {
	return service.resolveFeatureStageMessageWithDisposition(ctx, feature, invocation, "STRUCTURED_HANDOFF_APPLIED")
}

func (service *ProductionService) resolveFeatureStageMessageWithDisposition(ctx context.Context, feature organization.FeatureRequest, invocation kernel.WorkInvocation, disposition string) error {
	claim, found, err := service.MessageBus.Read(ctx, feature.LastMessageID)
	if err != nil || !found {
		return errors.Join(organization.ErrOrganizationalMessageNotFound, err)
	}
	if claim.State == organization.MessageResolved {
		return nil
	}
	claimExecution := invocation.Execution
	if claim.State == organization.MessagePending {
		// The invocation may have completed immediately before a daemon restart.
		// Its immutable output remains the handoff evidence, while the current
		// process execution performs the durable acknowledgement.
		if service.RoleHost != nil {
			current, active, statusErr := service.RoleHost.Status(ctx, invocation.ActorFQN)
			if statusErr != nil {
				return statusErr
			}
			if active {
				if current.ActorFQN != invocation.ActorFQN || !current.Execution.Valid() {
					return organization.ErrStaleOrganizationalClaim
				}
				claimExecution = current.Execution
			}
		}
		claim, err = service.MessageBus.Claim(ctx, invocation.ActorFQN, claimExecution, service.clock.Now().UTC(), service.recoveryTimeout, service.messageMaximumAttempts)
		if err != nil {
			return err
		}
	}
	if claim.Message.ID != feature.LastMessageID || claim.State != organization.MessageClaimed || claim.Holder != invocation.ActorFQN || claim.Execution != claimExecution || invocation.OutputDigest == nil {
		return organization.ErrStaleOrganizationalClaim
	}
	return service.MessageBus.Resolve(ctx, claim, service.clock.Now().UTC(), disposition, *invocation.OutputDigest)
}
