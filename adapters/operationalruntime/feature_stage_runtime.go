package operationalruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
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
		if invocation.State != kernel.InvocationSucceeded {
			if retryableFeaturePlanningInvocation(invocation, task.AttemptLimit) {
				if retryErr := service.retryFeaturePlanningInvocation(ctx, feature, task, state, head, invocation, snapshot, &invocation, nil, false); retryErr != nil {
					return fmt.Errorf("feature %s %s execution retry: %w", feature.ID, stage, retryErr)
				}
			} else if recoverable, recoveryErr := service.retryableTechnicalPlanningFailure(ctx, task, invocation); recoveryErr != nil {
				return fmt.Errorf("feature %s %s technical failure inspection: %w", feature.ID, stage, recoveryErr)
			} else if recoverable {
				if retryErr := service.retryFeaturePlanningInvocation(ctx, feature, task, state, head, invocation, snapshot, &invocation, nil, true); retryErr != nil {
					return fmt.Errorf("feature %s %s technical execution recovery: %w", feature.ID, stage, retryErr)
				}
			}
			continue
		}
		if invocation.OutputDigest == nil {
			continue
		}
		output, err := service.Runtime.ReadExecutionOutput(ctx, *invocation.OutputDigest)
		if err != nil {
			return err
		}
		if state.Phase != kernel.PhaseCompleted {
			if err := service.validateFeatureStageOutput(feature, stage, output); err != nil {
				if invocation.AttemptOrdinal < uint64(task.AttemptLimit) {
					if retryErr := service.retryFeaturePlanningInvocation(ctx, feature, task, state, head, invocation, snapshot, nil, []kernel.Digest{*invocation.OutputDigest}, false); retryErr != nil {
						return fmt.Errorf("feature %s %s invalid-output retry: %w", feature.ID, stage, retryErr)
					}
					continue
				}
				continue
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
	description, err = featurePlanningDescription(feature, stage, description)
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
	taskID := deterministicOperationalUUID("feature-planning-task", string(feature.ID), string(stage))
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
	task := organization.PlannedTask{ID: taskID, StoryID: planningStoryID, Title: title, Description: description, AcceptanceCriteria: criteria, DependsOn: dependsOn, Owner: owner.ActorFQN, ModelProfile: owner.ModelProfile, DecisionRoute: profileConfig.Qualification.DecisionRoute, Purpose: purpose, Complexity: 4, Risk: organization.RiskModerate, CriticalPath: true, AttemptLimit: 3, ReviewRoundLimit: 1}
	state, head, taskFound, err := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID})
	if err != nil {
		return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, errors.Join(organization.ErrInvalidFeature, err)
	}
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
	if state.Phase != kernel.PhasePlanned && state.Phase != kernel.PhaseReady && state.Phase != kernel.PhaseActive {
		return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, organization.ErrInvalidFeature
	}
	if err := service.registerExecution(ctx, owner, profileConfig); err != nil {
		return organization.PlannedTask{}, kernel.AggregateState{}, "", kernel.WorkInvocation{}, kernel.Snapshot{}, err
	}
	profile := service.workProfile(feature, task, evidenceID, deadline)
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

func planningStageDefinition(stage featurePlanningStage) (string, kernel.WorkPurpose, string, string, []string, error) {
	switch stage {
	case stageRefinement:
		return "product-owner", kernel.PurposeHandoff, "Refine feature request", "Analyze only the authoritative feature state below. Return FEATURE_REFINEMENT JSON with schema_version, result_type, acceptance_criteria, clarification_questions, and priority. priority must be exactly one of LOW, NORMAL, HIGH, or CRITICAL. If the request is already unambiguous, return an empty clarification_questions array. Do not add or change product requirements to name an actor, actor instance, branch, worktree, workspace path, model profile, execution identity, or fencing epoch; Teams assigns operational identities only after the product definition is validated. This is a reasoning-only task: do not use terminal, browser, repository, or other tools. Do not infer a different feature from workspace contents. Do not delegate or start another agent. Call the OpenHands finish tool exactly once. Put TEKROO_ORGANIZATIONAL_RESULT: on its own line followed by exactly one FEATURE_REFINEMENT JSON object in finish.message. finish.message is the only result Teams receives. Do not answer with an assistant text message, summary, explanation, Markdown fence, XML, or trailing content. A prose assessment is not a result and will be rejected.", []string{"requirements are testable and ambiguities are explicit"}, nil
	case stageSpecification:
		return "project-manager", kernel.PurposeHandoff, "Specify feature stories", "Analyze only the authoritative feature state below. Return FEATURE_SPECIFICATION JSON with schema_version, result_type, stories (title, description, acceptance_criteria, priority), and design_constraints. Every priority must be exactly one of LOW, NORMAL, HIGH, or CRITICAL. Return the smallest complete specification. When the request is one cohesive feature, return exactly one story containing all accepted behavior; do not split it by API or storage layer. Keep the JSON compact. Do not add or change product requirements to name an actor, actor instance, branch, worktree, workspace path, model profile, execution identity, or fencing epoch; Teams assigns operational identities only after the product definition is validated. This is a reasoning-only task: do not use terminal, browser, repository, or other tools. Do not infer a different feature from workspace contents. Do not delegate or start another agent. Before calling finish, verify that the result parses as JSON and contains exactly one root object. Call the OpenHands finish tool exactly once. Put TEKROO_ORGANIZATIONAL_RESULT: on its own line followed by exactly one FEATURE_SPECIFICATION JSON object in finish.message. The final character of finish.message must be }; a FEATURE_SPECIFICATION result ends with ]}, never ]}}. finish.message is the only result Teams receives. Do not answer with an assistant text message, summary, explanation, Markdown fence, XML, or trailing content. A prose assessment is not a result and will be rejected.", []string{"stories are finite, testable, and within the accepted feature scope"}, nil
	case stageArchitecture:
		return "architect", kernel.PurposeReplan, "Design executable feature DAG", "Ground the plan in both the authoritative feature state below and the current repository. Before planning, use read-only repository tools to read AGENTS.md and inspect the relevant current architecture, implementation interfaces, and tests. Use rg or rg --files for discovery, then inspect the files it locates. Do not edit files, run mutating commands, or invent paths or interfaces that you did not observe. Include the relevant repository-relative file paths in architecture or design_decisions so the plan is reviewable. Return FEATURE_PLAN JSON with schema_version, result_type, architecture, design_decisions, assumptions, and tasks. Each task supplies story_index, title, description, acceptance_criteria, depends_on, validates, role, purpose, complexity, risk, critical_path, attempt_limit, and review_round_limit. role is a role class and must be exactly one of coder, senior-coder, tester, or security; never include an instance suffix. purpose must be exactly one of IMPLEMENTATION, INVESTIGATION, VALIDATION, or REVIEW. complexity must be a JSON integer from 1 through 10, never a string or label. risk must be exactly one of LOW, MODERATE, HIGH, or CRITICAL. attempt_limit and review_round_limit must be JSON integers. depends_on and validates must be JSON arrays containing only zero-based integer task indexes that point backward; they are task relationships, never acceptance-criteria text. IMPLEMENTATION and INVESTIGATION tasks must always use \"validates\":[]; express their prerequisites only in depends_on. Only VALIDATION and REVIEW tasks may contain validates, but do not create those tasks because Teams adds required independent validation and product acceptance structurally. Aggressively decompose work for a bounded local coding model: every task must be independently completable within one bounded OpenHands run. If the change spans three or more architectural layers, is expected to touch more than five files, or has overall complexity 7 or greater, split it into two through six causal IMPLEMENTATION tasks, each with complexity no greater than 6 and a narrow package or interface boundary. Chain all implementation tasks for this repository in a single causal order so two agents never write the same repository workspace concurrently. Put domain and persistence foundations before transport surfaces, then CLI or integration wiring. Only a genuinely single-file routine change may use exactly one IMPLEMENTATION task with \"depends_on\":[],\"validates\":[] . Every implementation task description must direct the executor to read AGENTS.md, use rg for discovery, inspect a located file instead of repeating the same search, extend existing interfaces, and run focused tests. The exact single-file minimal shape is: TEKROO_ORGANIZATIONAL_RESULT: followed by {\"schema_version\":\"1.0.0\",\"result_type\":\"FEATURE_PLAN\",\"architecture\":\"...\",\"design_decisions\":[],\"assumptions\":[],\"tasks\":[{\"story_index\":0,\"title\":\"...\",\"description\":\"...\",\"acceptance_criteria\":[\"...\"],\"depends_on\":[],\"validates\":[],\"role\":\"coder\",\"purpose\":\"IMPLEMENTATION\",\"complexity\":1,\"risk\":\"LOW\",\"critical_path\":true,\"attempt_limit\":2,\"review_round_limit\":1}]}. Call the OpenHands finish tool exactly once. Put the marker and JSON themselves in finish.message; finish.message is the only result Teams receives. Do not put a summary or paraphrase in finish.message. Include no prose, Markdown fence, XML, tool call, or trailing content around the marker and JSON. Do not choose or mention an actor instance, branch, worktree, workspace path, model profile, or execution identity; Teams binds those after validating the DAG. Do not infer a different feature from unrelated workspace contents. Do not delegate or start another agent.", []string{"the result is a repository-grounded finite acyclic task plan with explicit validation"}, nil
	default:
		return "", "", "", "", nil, organization.ErrInvalidFeature
	}
}

func retryableFeaturePlanningInvocation(invocation kernel.WorkInvocation, attemptLimit uint32) bool {
	if invocation.AttemptOrdinal == 0 || invocation.AttemptOrdinal >= uint64(attemptLimit) || invocation.Retryable == nil || !*invocation.Retryable {
		return false
	}
	switch invocation.State {
	case kernel.InvocationFailed, kernel.InvocationTimedOut, kernel.InvocationStartFailed:
		return true
	default:
		return false
	}
}

func (service *ProductionService) retryFeaturePlanningInvocation(ctx context.Context, feature organization.FeatureRequest, task organization.PlannedTask, state kernel.AggregateState, head kernel.UUIDv7, invocation kernel.WorkInvocation, snapshot kernel.Snapshot, retry *kernel.WorkInvocation, conditionDigests []kernel.Digest, technicalExtension bool) error {
	if invocation.AttemptOrdinal == 0 || !technicalExtension && invocation.AttemptOrdinal >= uint64(task.AttemptLimit) || state.Phase != kernel.PhaseActive {
		return organization.ErrInvalidFeature
	}
	owner, active, err := service.RoleHost.Status(ctx, task.Owner)
	if err != nil || !active || owner.Status != organization.RoleIdle {
		return errors.Join(organization.ErrRoleNotRunning, err)
	}
	profileConfig, found := service.profilesByModel[owner.ModelProfile]
	workspace, workspaceFound := service.workspacesByID[owner.WorkspaceID]
	profile, profileFound := snapshot.WorkProfiles[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}]
	budget, budgetFound := snapshot.WorkBudgetAccounts[kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: feature.BudgetAccountID}]
	if !found || !workspaceFound || !profileFound || !budgetFound {
		return organization.ErrInvalidFeature
	}
	tracked := &trackedTask{plan: task, revision: state.Revision, last: head, profile: profile.Profile, owner: owner}
	budgetRevision := budget.Revision
	if technicalExtension {
		if owner.Execution == invocation.Execution {
			return organization.ErrInvalidFeature
		}
		budgetRevision, err = service.extendTaskTechnicalRetryBudget(ctx, feature, tracked, task.Purpose, invocation.AttemptOrdinal+1)
		if err != nil {
			return err
		}
	}
	return service.authorizeTaskInvocationWithConditionPolicy(ctx, feature, tracked, profileConfig, workspace, budgetRevision, task.Purpose, invocation.AttemptOrdinal+1, retry, conditionDigests, technicalExtension, retry != nil)
}

func (service *ProductionService) retryableTechnicalPlanningFailure(ctx context.Context, task organization.PlannedTask, invocation kernel.WorkInvocation) (bool, error) {
	if invocation.AttemptOrdinal < uint64(task.AttemptLimit) || invocation.Retryable == nil || !*invocation.Retryable || invocation.OutputDigest == nil {
		return false, nil
	}
	switch invocation.State {
	case kernel.InvocationFailed, kernel.InvocationTimedOut, kernel.InvocationStartFailed:
	default:
		return false, nil
	}
	output, err := service.Runtime.ReadExecutionOutput(ctx, *invocation.OutputDigest)
	if err != nil {
		return false, err
	}
	return technicalPlanningFailure(output), nil
}

func technicalPlanningFailure(output []byte) bool {
	var value struct {
		Reason string `json:"reason"`
	}
	if json.Unmarshal(output, &value) != nil {
		return false
	}
	switch value.Reason {
	case "EXECUTION_BRIEF_SUPERSEDED", "SHELL_DISCIPLINE_VIOLATION", "REPEATED_SHELL_DISCIPLINE_VIOLATION", "REPEATED_REPOSITORY_SEARCH":
		return true
	default:
		return false
	}
}

func featurePlanningDescription(feature organization.FeatureRequest, stage featurePlanningStage, instruction string) (string, error) {
	state := struct {
		FeatureID     kernel.UUIDv7                      `json:"feature_id"`
		Input         organization.FeatureRequestInput   `json:"input"`
		Refinement    *organization.FeatureRefinement    `json:"refinement,omitempty"`
		Specification *organization.FeatureSpecification `json:"specification,omitempty"`
	}{FeatureID: feature.ID, Input: feature.Input}
	switch stage {
	case stageRefinement:
	case stageSpecification:
		if feature.Refinement == nil {
			return "", organization.ErrInvalidFeature
		}
		state.Refinement = feature.Refinement
	case stageArchitecture:
		if feature.Refinement == nil || feature.Specification == nil {
			return "", organization.ErrInvalidFeature
		}
		state.Refinement = feature.Refinement
		state.Specification = feature.Specification
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
		_, err := parseSpecificationStageResult(output, allowedActorFQNs...)
		return err
	case stageArchitecture:
		_, err := parseArchitectureStageResult(output, allowedActorFQNs...)
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
		_, err = service.Features.Refine(ctx, feature.ID, feature.Revision, organization.FeatureRefinement{PreparedBy: invocation.ActorFQN, PreparedExecution: invocation.Execution, AcceptanceCriteria: result.AcceptanceCriteria, ClarificationQuestions: result.ClarificationQuestions, Priority: result.Priority, PreparedAt: preparedAt})
		return err
	case stageSpecification:
		result, err := parseSpecificationStageResult(output, allowedActorFQNs...)
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
	if feature.Specification == nil || len(result.Tasks) > int(feature.Input.MaximumTasks) {
		return organization.FeaturePlan{}, organization.ErrInvalidFeature
	}
	normalizedTasks, err := normalizeArchitectureTaskRelations(result.Tasks)
	if err != nil {
		return organization.FeaturePlan{}, err
	}
	result.Tasks = normalizedTasks
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
	tasks, err = service.addRequiredValidationTasks(ctx, feature, tasks)
	if err != nil {
		return organization.FeaturePlan{}, err
	}
	tasks, err = service.addStoryAcceptanceTasks(ctx, feature, tasks)
	if err != nil {
		return organization.FeaturePlan{}, err
	}
	tasks, err = service.bindValidationWorkspaceContext(ctx, tasks)
	if err != nil {
		return organization.FeaturePlan{}, err
	}
	plan := organization.FeaturePlan{Version: 1, PreparedBy: invocation.ActorFQN, PreparedExecution: invocation.Execution, Architecture: result.Architecture, DesignDecisions: result.DesignDecisions, Assumptions: result.Assumptions, Stories: append([]organization.PlannedStory(nil), feature.Specification.Stories...), Tasks: tasks, CreatedAt: createdAt}
	if plan.Validate(feature) != nil {
		return organization.FeaturePlan{}, organization.ErrInvalidFeature
	}
	return plan, nil
}

func normalizeArchitectureTaskRelations(tasks []architectureTaskResult) ([]architectureTaskResult, error) {
	result := append([]architectureTaskResult(nil), tasks...)
	lastImplementation := -1
	implementationRole := ""
	for index := range result {
		result[index].DependsOn = append([]uint32(nil), result[index].DependsOn...)
		result[index].Validates = append([]uint32(nil), result[index].Validates...)
		switch result[index].Purpose {
		case kernel.PurposeImplementation:
			if implementationRole == "" {
				implementationRole = result[index].Role
				if implementationRole != "coder" && implementationRole != "senior-coder" {
					implementationRole = "coder"
				}
			}
			result[index].Role = implementationRole
			dependencies := make(map[uint32]struct{}, len(result[index].DependsOn)+1)
			for _, dependency := range result[index].DependsOn {
				dependencies[dependency] = struct{}{}
			}
			for _, target := range result[index].Validates {
				if _, found := dependencies[target]; !found {
					return nil, organization.ErrInvalidFeature
				}
			}
			result[index].Validates = nil
			if lastImplementation >= 0 {
				prior := uint32(lastImplementation)
				if _, found := dependencies[prior]; !found {
					result[index].DependsOn = append(result[index].DependsOn, prior)
				}
			}
			sort.Slice(result[index].DependsOn, func(left, right int) bool { return result[index].DependsOn[left] < result[index].DependsOn[right] })
			lastImplementation = index
		case kernel.PurposeInvestigation:
			if len(result[index].Validates) != 0 {
				return nil, organization.ErrInvalidFeature
			}
		case kernel.PurposeValidation, kernel.PurposeReview:
			if len(result[index].Validates) == 0 {
				return nil, organization.ErrInvalidFeature
			}
		default:
			return nil, organization.ErrInvalidFeature
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
		var contextLines []string
		for _, targetID := range result[index].Validates {
			target, found := byID[targetID]
			if !found {
				return nil, organization.ErrInvalidFeature
			}
			owner, active, err := service.RoleHost.Status(ctx, target.Owner)
			if err != nil || !active {
				return nil, errors.Join(organization.ErrRoleNotRunning, err)
			}
			workspace, found := service.workspacesByID[owner.WorkspaceID]
			if !found {
				return nil, organization.ErrInvalidFeature
			}
			contextLines = append(contextLines, fmt.Sprintf("target_task=%s owner=%s branch=%s workspace=%s baseline=%s", target.ID, target.Owner, workspace.Branch, workspace.WorkingDirectory, workspace.BaselineSHA))
		}
		result[index].Description += "\n\nAUTHORITATIVE_VALIDATION_TARGETS:\n" + strings.Join(contextLines, "\n") + "\nInspect the exact target branch/workspace without modifying it; base the structured validation result on current repository state and executed checks."
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
		if target.Purpose != kernel.PurposeImplementation && target.Purpose != kernel.PurposeRepair {
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
	acceptanceTargets := make([]string, 0)
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
			acceptanceTargets = append(acceptanceTargets, fmt.Sprintf("target_task=%s owner=%s branch=%s workspace=%s baseline=%s", task.ID, task.Owner, workspace.Branch, workspace.WorkingDirectory, workspace.BaselineSHA))
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
	sort.Strings(acceptanceTargets)
	description := "Review the completed feature evidence against every story acceptance criterion and return the structured product acceptance result. This is a read-only judgment: do not modify files, commits, branches, worktrees, or Git refs. Do not delegate or start another agent."
	if len(acceptanceTargets) > 0 {
		description += "\n\nAUTHORITATIVE_ACCEPTANCE_TARGETS:\n" + strings.Join(acceptanceTargets, "\n") + "\nInspect these exact completed implementation branches/workspaces read-only. The product-owner workspace is not the implementation artifact and must not be used as a substitute."
	}
	id := deterministicOperationalUUID("feature-acceptance", string(feature.ID))
	tasks = append(tasks, organization.PlannedTask{
		ID: id, StoryID: feature.Specification.Stories[0].ID, Title: "Accept feature: " + feature.Input.Title,
		Description:        description,
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
