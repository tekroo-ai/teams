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

type featurePlanningStage string

const (
	stageRefinement    featurePlanningStage = "refinement"
	stageSpecification featurePlanningStage = "specification"
	stageArchitecture  featurePlanningStage = "architecture"
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
		if err != nil {
			return fmt.Errorf("feature %s %s: %w", feature.ID, stage, err)
		}
		if invocation.State != kernel.InvocationSucceeded || invocation.OutputDigest == nil {
			continue
		}
		output, err := service.Runtime.ReadExecutionOutput(ctx, *invocation.OutputDigest)
		if err != nil {
			return err
		}
		if state.Phase != kernel.PhaseCompleted {
			if err := service.validateFeatureStageOutput(stage, output); err != nil {
				return fmt.Errorf("feature %s %s output: %w", feature.ID, stage, err)
			}
			if err := service.completeEvidenceTask(ctx, feature, task, state, head, invocation, snapshot); err != nil {
				return err
			}
		}
		if err := service.resolveFeatureStageMessage(ctx, feature, invocation); err != nil {
			return err
		}
		if err := service.applyFeatureStageOutput(ctx, feature, stage, invocation, output); err != nil {
			return err
		}
	}
	return nil
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
	role, purpose, title, description, criteria, err := planningStageDefinition(stage)
	if err != nil {
		return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, err
	}
	owner, err := service.ensureExactPlanningRole(ctx, role)
	if err != nil {
		return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, err
	}
	profileConfig, found := service.profilesByModel[owner.ModelProfile]
	workspace, workspaceFound := service.workspacesByID[owner.WorkspaceID]
	if !found || !workspaceFound || profileConfig.Qualification.DecisionRoute.ModelExecutable() == false {
		return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, organization.ErrInvalidFeature
	}
	storyReceipt, err := service.ensureFeaturePlanningStory(ctx, feature)
	if err != nil {
		return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, err
	}
	evidenceID, evidence, err := service.ensureFeaturePlanningEvidence(ctx, feature)
	if err != nil {
		return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, err
	}
	deadline := feature.CreatedAt.Add(service.planningDeadline)
	budgetRevision, err := service.ensureFeatureWorkBudget(ctx, feature, evidenceID, evidence, deadline)
	if err != nil {
		return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, err
	}
	planningStoryID := deterministicOperationalUUID("feature-planning-story", string(feature.ID))
	taskID := deterministicOperationalUUID("feature-planning-task", string(feature.ID), string(stage))
	dependsOn := []kernel.UUIDv7{}
	parents := []kernel.DagParent{{ParentEventID: storyReceipt.EventIDs[0], EdgeKind: kernel.EdgeCausal}}
	if prior, hasPrior := priorPlanningStage(stage); hasPrior {
		priorID := deterministicOperationalUUID("feature-planning-task", string(feature.ID), string(prior))
		priorState, priorHead, priorFound, priorErr := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: priorID})
		if priorErr != nil || !priorFound || priorState.Phase != kernel.PhaseCompleted {
			return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, errors.Join(organization.ErrFeatureConflict, priorErr)
		}
		dependsOn = append(dependsOn, priorID)
		parents = append(parents, kernel.DagParent{ParentEventID: priorHead, EdgeKind: kernel.EdgeCausal})
	}
	task := organization.PlannedTask{ID: taskID, StoryID: planningStoryID, Title: title, Description: description, AcceptanceCriteria: criteria, DependsOn: dependsOn, Owner: owner.ActorFQN, ModelProfile: owner.ModelProfile, DecisionRoute: profileConfig.Qualification.DecisionRoute, Purpose: purpose, Complexity: 4, Risk: organization.RiskModerate, CriticalPath: true, AttemptLimit: 2, ReviewRoundLimit: 1}
	payload, _ := json.Marshal(map[string]any{"story_id": planningStoryID, "title": task.Title, "description": task.Description, "acceptance_criteria": task.AcceptanceCriteria, "depends_on": task.DependsOn})
	created, err := service.submitPlannedCommand(ctx, feature, "tekroo.command.task.create", kernel.AggregateTask, task.ID, "planning-task-"+string(stage), payload, parents)
	if err != nil {
		return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, err
	}
	state, head, taskFound, err := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID})
	if err != nil || !taskFound {
		return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, errors.Join(organization.ErrInvalidFeature, err)
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
	if state.Phase != kernel.PhasePlanned && state.Phase != kernel.PhaseReady && state.Phase != kernel.PhaseActive {
		return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, organization.ErrInvalidFeature
	}
	if err := service.registerExecution(ctx, feature, owner, profileConfig); err != nil {
		return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, err
	}
	profile := service.workProfile(feature, task, evidenceID, deadline)
	tracked := &trackedTask{plan: task, revision: 1, last: created.EventIDs[0], profile: profile, owner: owner}
	if err := service.applyTaskCommand(ctx, feature, tracked, "tekroo.command.task.bind-work-profile", kernel.SchemaVersion, service.policyAuthority, profile, evidence, nil, "profile"); err != nil {
		return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, err
	}
	dependencyEvents := make([]kernel.UUIDv7, 0, len(dependsOn))
	for _, parent := range parents[1:] {
		dependencyEvents = append(dependencyEvents, parent.ParentEventID)
	}
	if err := service.activateTask(ctx, feature, tracked, profileConfig, workspace, budgetRevision, dependencyEvents, evidence, evidenceID, nil); err != nil {
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

func planningStageDefinition(stage featurePlanningStage) (string, kernel.WorkPurpose, string, string, []string, error) {
	switch stage {
	case stageRefinement:
		return "product-owner", kernel.PurposeHandoff, "Refine feature request", "Analyze the feature request and return FEATURE_REFINEMENT JSON with schema_version, result_type, acceptance_criteria, clarification_questions, and priority. Do not delegate or start another agent.", []string{"requirements are testable and ambiguities are explicit"}, nil
	case stageSpecification:
		return "project-manager", kernel.PurposeHandoff, "Specify feature stories", "Return FEATURE_SPECIFICATION JSON with schema_version, result_type, stories (title, description, acceptance_criteria, priority), and design_constraints. Do not delegate or start another agent.", []string{"stories are finite, testable, and within the accepted feature scope"}, nil
	case stageArchitecture:
		return "architect", kernel.PurposeReplan, "Design executable feature DAG", "Return FEATURE_PLAN JSON with schema_version, result_type, architecture, design_decisions, assumptions, and tasks. Each task supplies story_index, title, description, acceptance_criteria, depends_on, validates, role, purpose, complexity, risk, critical_path, attempt_limit, and review_round_limit. Index references are zero-based and must point backward. Teams selects exact actors and model profiles. Do not delegate or start another agent.", []string{"the result is a finite acyclic task plan with explicit validation"}, nil
	default:
		return "", "", "", "", nil, organization.ErrInvalidFeature
	}
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
	actors, err := service.RoleHost.ConfiguredRoleActors(role)
	if err != nil || len(actors) == 0 {
		return organization.RoleInstanceState{}, errors.Join(organization.ErrRoleNotRunning, err)
	}
	sort.Slice(actors, func(left, right int) bool { return actors[left] < actors[right] })
	state, active, err := service.RoleHost.Status(ctx, actors[0])
	if err == nil && active && state.Status == organization.RoleIdle {
		return state, nil
	}
	state, startErr := service.RoleHost.EnsureStarted(ctx, actors[0])
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
	payload, _ := json.Marshal(map[string]any{"access_partition": feature.Input.WorkspaceID, "availability": "AVAILABLE", "byte_length": len(encoded), "canonical_digest": digest, "computation": nil, "deletion_tombstone": nil, "evidence_kind": "DECISION_RECORD", "integrity_state": "DIGEST_VERIFIED", "locator": "teams://feature/" + string(feature.ID) + "/request", "locator_immutable": true, "media_type": "application/json", "producing_component": "tekrood-feature-intake", "producing_version": FeaturePlanningVersion, "redacts": nil, "retention_policy": "feature-lifecycle", "sensitivity": "INTERNAL", "sha256": digest, "source_evidence_ids": []kernel.UUIDv7{}, "source_timestamp": feature.CreatedAt, "transport_provenance": "teams-operator-feature-intake"})
	if _, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.evidence.register", kernel.SchemaVersion, kernel.AggregateEvidence, id, service.serviceAuthority, 0, payload, nil, nil, "feature-request-evidence"); err != nil {
		return "", nil, err
	}
	return id, []kernel.EvidenceRef{{EvidenceID: id, SHA256: digest}}, nil
}

func (service *ProductionService) validateFeatureStageOutput(stage featurePlanningStage, output []byte) error {
	switch stage {
	case stageRefinement:
		_, err := parseRefinementStageResult(output)
		return err
	case stageSpecification:
		_, err := parseSpecificationStageResult(output)
		return err
	case stageArchitecture:
		_, err := parseArchitectureStageResult(output)
		return err
	default:
		return organization.ErrInvalidFeature
	}
}

func (service *ProductionService) applyFeatureStageOutput(ctx context.Context, feature organization.FeatureRequest, stage featurePlanningStage, invocation kernel.WorkInvocation, output []byte) error {
	preparedAt := service.clock.Now().UTC()
	if invocation.FinishedAt != nil {
		preparedAt = invocation.FinishedAt.UTC()
	}
	switch stage {
	case stageRefinement:
		result, err := parseRefinementStageResult(output)
		if err != nil {
			return err
		}
		_, err = service.Features.Refine(ctx, feature.ID, feature.Revision, organization.FeatureRefinement{PreparedBy: invocation.ActorFQN, PreparedExecution: invocation.Execution, AcceptanceCriteria: result.AcceptanceCriteria, ClarificationQuestions: result.ClarificationQuestions, Priority: result.Priority, PreparedAt: preparedAt})
		return err
	case stageSpecification:
		result, err := parseSpecificationStageResult(output)
		if err != nil {
			return err
		}
		stories := make([]organization.PlannedStory, len(result.Stories))
		for index, item := range result.Stories {
			stories[index] = organization.PlannedStory{ID: deterministicOperationalUUID("feature-story", string(feature.ID), fmt.Sprint(index), item.Title), Title: item.Title, Description: item.Description, AcceptanceCriteria: item.AcceptanceCriteria, Priority: item.Priority}
		}
		_, err = service.Features.Specify(ctx, feature.ID, feature.Revision, organization.FeatureSpecification{PreparedBy: invocation.ActorFQN, PreparedExecution: invocation.Execution, Stories: stories, DesignConstraints: result.DesignConstraints, PreparedAt: preparedAt})
		return err
	case stageArchitecture:
		result, err := parseArchitectureStageResult(output)
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
	if feature.Specification == nil || len(result.Tasks) > int(feature.Input.MaximumTasks) {
		return organization.FeaturePlan{}, organization.ErrInvalidFeature
	}
	taskIDs := make([]kernel.UUIDv7, len(result.Tasks))
	for index := range taskIDs {
		taskIDs[index] = deterministicOperationalUUID("feature-task", string(feature.ID), fmt.Sprint(index), result.Tasks[index].Title)
	}
	tasks := make([]organization.PlannedTask, 0, feature.Input.MaximumTasks)
	for index, item := range result.Tasks {
		if int(item.StoryIndex) >= len(feature.Specification.Stories) {
			return organization.FeaturePlan{}, organization.ErrInvalidFeature
		}
		if item.Purpose != kernel.PurposeImplementation && item.Purpose != kernel.PurposeInvestigation && item.Purpose != kernel.PurposeValidation && item.Purpose != kernel.PurposeReview {
			return organization.FeaturePlan{}, organization.ErrInvalidFeature
		}
		owner, err := service.ensureExactPlanningRole(ctx, item.Role)
		if err != nil {
			return organization.FeaturePlan{}, err
		}
		profile := service.profilesByModel[owner.ModelProfile]
		dependencies := indexesToTaskIDs(item.DependsOn, taskIDs)
		validates := indexesToTaskIDs(item.Validates, taskIDs)
		tasks = append(tasks, organization.PlannedTask{ID: taskIDs[index], StoryID: feature.Specification.Stories[item.StoryIndex].ID, Title: item.Title, Description: item.Description, AcceptanceCriteria: item.AcceptanceCriteria, DependsOn: dependencies, Validates: validates, Owner: owner.ActorFQN, ModelProfile: owner.ModelProfile, DecisionRoute: profile.Qualification.DecisionRoute, Purpose: item.Purpose, Complexity: item.Complexity, Risk: item.Risk, CriticalPath: item.CriticalPath, AttemptLimit: item.AttemptLimit, ReviewRoundLimit: item.ReviewRoundLimit})
	}
	tasks, err := service.addRequiredValidationTasks(ctx, feature, tasks)
	if err != nil {
		return organization.FeaturePlan{}, err
	}
	tasks, err = service.addStoryAcceptanceTasks(ctx, feature, tasks)
	if err != nil {
		return organization.FeaturePlan{}, err
	}
	tasks, err = service.addRequiredValidationTasks(ctx, feature, tasks)
	if err != nil {
		return organization.FeaturePlan{}, err
	}
	plan := organization.FeaturePlan{Version: 1, PreparedBy: invocation.ActorFQN, PreparedExecution: invocation.Execution, Architecture: result.Architecture, DesignDecisions: result.DesignDecisions, Assumptions: result.Assumptions, Stories: append([]organization.PlannedStory(nil), feature.Specification.Stories...), Tasks: tasks, CreatedAt: createdAt}
	if plan.Validate(feature) != nil {
		return organization.FeaturePlan{}, organization.ErrInvalidFeature
	}
	return plan, nil
}

func indexesToTaskIDs(indexes []uint32, ids []kernel.UUIDv7) []kernel.UUIDv7 {
	result := make([]kernel.UUIDv7, len(indexes))
	for index, value := range indexes {
		result[index] = ids[value]
	}
	return result
}

func (service *ProductionService) addRequiredValidationTasks(ctx context.Context, feature organization.FeatureRequest, tasks []organization.PlannedTask) ([]organization.PlannedTask, error) {
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
		if target.Purpose != kernel.PurposeImplementation && target.Purpose != kernel.PurposeRepair && target.Purpose != kernel.PurposePromotion {
			continue
		}
		if !coverage[target.ID][kernel.PurposeValidation] {
			validator, err := service.ensureExactPlanningRole(ctx, "tester")
			if err != nil {
				return nil, err
			}
			profile := service.profilesByModel[validator.ModelProfile]
			id := deterministicOperationalUUID("required-validator", string(feature.ID), string(target.ID), "tester")
			tasks = append(tasks, organization.PlannedTask{ID: id, StoryID: target.StoryID, Title: "Validate: " + target.Title, Description: "Independently inspect the implementation, run the acceptance checks, and report the structured validation result.", AcceptanceCriteria: append([]string(nil), target.AcceptanceCriteria...), DependsOn: []kernel.UUIDv7{target.ID}, Validates: []kernel.UUIDv7{target.ID}, Owner: validator.ActorFQN, ModelProfile: validator.ModelProfile, DecisionRoute: profile.Qualification.DecisionRoute, Purpose: kernel.PurposeValidation, Complexity: target.Complexity, Risk: target.Risk, CriticalPath: true, AttemptLimit: target.ReviewRoundLimit + 1, ReviewRoundLimit: target.ReviewRoundLimit})
			if coverage[target.ID] == nil {
				coverage[target.ID] = make(map[kernel.WorkPurpose]bool)
			}
			coverage[target.ID][kernel.PurposeValidation] = true
		}
		if (target.Risk == organization.RiskHigh || target.Risk == organization.RiskCritical) && !coverage[target.ID][kernel.PurposeReview] {
			security, err := service.ensureExactPlanningRole(ctx, "security")
			if err != nil {
				return nil, err
			}
			profile := service.profilesByModel[security.ModelProfile]
			id := deterministicOperationalUUID("required-validator", string(feature.ID), string(target.ID), "security")
			tasks = append(tasks, organization.PlannedTask{ID: id, StoryID: target.StoryID, Title: "Security review: " + target.Title, Description: "Independently inspect the security-sensitive implementation and report the structured validation result.", AcceptanceCriteria: append([]string(nil), target.AcceptanceCriteria...), DependsOn: []kernel.UUIDv7{target.ID}, Validates: []kernel.UUIDv7{target.ID}, Owner: security.ActorFQN, ModelProfile: security.ModelProfile, DecisionRoute: profile.Qualification.DecisionRoute, Purpose: kernel.PurposeReview, Complexity: target.Complexity, Risk: target.Risk, CriticalPath: true, AttemptLimit: 2, ReviewRoundLimit: target.ReviewRoundLimit})
		}
		if len(tasks) > int(feature.Input.MaximumTasks) {
			return nil, organization.ErrInvalidFeature
		}
	}
	return tasks, nil
}

func (service *ProductionService) addStoryAcceptanceTasks(ctx context.Context, feature organization.FeatureRequest, tasks []organization.PlannedTask) ([]organization.PlannedTask, error) {
	productOwner, err := service.ensureExactPlanningRole(ctx, "product-owner")
	if err != nil {
		return nil, err
	}
	profile, found := service.profilesByModel[productOwner.ModelProfile]
	if !found {
		return nil, organization.ErrInvalidFeature
	}
	dependencies := make([]kernel.UUIDv7, len(tasks))
	criteria := make([]string, 0)
	maximumComplexity := uint8(1)
	risk := organization.RiskLow
	for index, task := range tasks {
		dependencies[index] = task.ID
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
	id := deterministicOperationalUUID("feature-acceptance", string(feature.ID))
	tasks = append(tasks, organization.PlannedTask{
		ID: id, StoryID: feature.Specification.Stories[0].ID, Title: "Accept feature: " + feature.Input.Title,
		Description:        "Review the completed feature evidence against every story acceptance criterion and return the structured product acceptance result. Do not delegate or start another agent.",
		AcceptanceCriteria: criteria, DependsOn: dependencies,
		Owner: productOwner.ActorFQN, ModelProfile: productOwner.ModelProfile, DecisionRoute: profile.Qualification.DecisionRoute,
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
	claim, found, err := service.MessageBus.Read(ctx, feature.LastMessageID)
	if err != nil || !found {
		return errors.Join(organization.ErrOrganizationalMessageNotFound, err)
	}
	if claim.State == organization.MessageResolved {
		return nil
	}
	if claim.State == organization.MessagePending {
		claim, err = service.MessageBus.Claim(ctx, invocation.ActorFQN, invocation.Execution, service.clock.Now().UTC(), service.recoveryTimeout, service.messageMaximumAttempts)
		if err != nil {
			return err
		}
	}
	if claim.Message.ID != feature.LastMessageID || claim.State != organization.MessageClaimed || claim.Holder != invocation.ActorFQN || claim.Execution != invocation.Execution || invocation.OutputDigest == nil {
		return organization.ErrStaleOrganizationalClaim
	}
	return service.MessageBus.Resolve(ctx, claim, service.clock.Now().UTC(), "STRUCTURED_HANDOFF_APPLIED", *invocation.OutputDigest)
}
