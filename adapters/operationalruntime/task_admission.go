package operationalruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

type trackedTask struct {
	plan     organization.PlannedTask
	revision uint64
	last     kernel.UUIDv7
	profile  kernel.WorkRiskProfile
	owner    organization.RoleInstanceState
}

func (service *ProductionService) ensureFeatureWorkBudget(ctx context.Context, feature organization.FeatureRequest, evidenceID kernel.UUIDv7, evidence []kernel.EvidenceRef, deadline time.Time) (uint64, error) {
	budgetRef := kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: feature.BudgetAccountID}
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: budgetRef})
	if err != nil {
		return 0, err
	}
	if account, found := snapshot.WorkBudgetAccounts[budgetRef]; found {
		if !account.Valid() || account.LifecycleEpoch != feature.LifecycleEpoch || account.PolicyRevision != service.planning.PolicyRevision || account.PolicyDigest != service.planning.BudgetPolicyDigest {
			return 0, organization.ErrInvalidFeature
		}
		return account.Revision, nil
	}
	planningStoryID := deterministicOperationalUUID("feature-planning-story", string(feature.ID))
	storyState, storyEvent, found, err := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateStory, ID: planningStoryID})
	if err != nil || !found || storyState.Revision == 0 {
		return 0, errors.Join(organization.ErrInvalidFeature, err)
	}
	modelLimit := uint64(feature.Input.MaximumTasks)*4 + uint64(feature.Input.MaximumHops) + 16
	if modelLimit == 0 || modelLimit > 1000 {
		return 0, organization.ErrInvalidFeature
	}
	limits := make(kernel.PurposeCounters, len(kernel.AllWorkPurposes))
	for _, purpose := range kernel.AllWorkPurposes {
		limits[purpose] = modelLimit
	}
	payload, err := json.Marshal(map[string]any{
		"budget_account_id": feature.BudgetAccountID, "root_work": kernel.AggregateRef{Kind: kernel.AggregateStory, ID: planningStoryID},
		"lifecycle_epoch": feature.LifecycleEpoch, "policy_revision": service.planning.PolicyRevision, "policy_digest": service.planning.BudgetPolicyDigest,
		"model_invocation_limit": modelLimit, "purpose_limits": limits, "deadline_at": deadline, "evidence_ids": []kernel.UUIDv7{evidenceID}, "authority": service.policyAuthority,
	})
	if err != nil {
		return 0, err
	}
	receipt, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.work-budget.create", kernel.OperationalSchemaVersion, kernel.AggregateWorkBudget, feature.BudgetAccountID, service.policyAuthority, 0, payload, []kernel.DagParent{{ParentEventID: storyEvent, EdgeKind: kernel.EdgeDerivation}}, evidence, "budget", kernel.AggregatePrecondition{Aggregate: kernel.AggregateRef{Kind: kernel.AggregateStory, ID: planningStoryID}, Expected: kernel.NewExpectedRevision(storyState.Revision)})
	if err != nil {
		return 0, err
	}
	return revisionOrOne(receipt), nil
}

func (service *ProductionService) preparePlannedTasks(ctx context.Context, feature organization.FeatureRequest, plan organization.FeaturePlan, storyEvents map[kernel.UUIDv7]kernel.UUIDv7, taskEvents map[kernel.UUIDv7]kernel.UUIDv7) error {
	planBytes, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	planDigest := digestBytes(planBytes)
	evidenceID := deterministicOperationalUUID("feature-plan-evidence", string(feature.ID), fmt.Sprint(plan.Version), string(planDigest))
	evidencePayload, err := json.Marshal(map[string]any{
		"access_partition": feature.Input.WorkspaceID, "availability": "AVAILABLE", "byte_length": len(planBytes), "canonical_digest": planDigest,
		"computation": nil, "deletion_tombstone": nil, "evidence_kind": "DECISION_RECORD", "integrity_state": "DIGEST_VERIFIED",
		"locator": fmt.Sprintf("teams://feature/%s/plan/%d", feature.ID, plan.Version), "locator_immutable": true, "media_type": "application/json",
		"producing_component": "tekrood-feature-planning", "producing_version": FeaturePlanningVersion, "redacts": nil,
		"retention_policy": "feature-lifecycle", "sensitivity": "INTERNAL", "sha256": planDigest, "source_evidence_ids": []kernel.UUIDv7{},
		"source_timestamp": plan.CreatedAt, "transport_provenance": "teams-organizational-feature-plan",
	})
	if err != nil {
		return err
	}
	if _, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.evidence.register", kernel.SchemaVersion, kernel.AggregateEvidence, evidenceID, service.serviceAuthority, 0, evidencePayload, nil, nil, "plan-evidence"); err != nil {
		return err
	}
	evidenceRefs := []kernel.EvidenceRef{{EvidenceID: evidenceID, SHA256: planDigest}}

	if len(plan.Stories) == 0 {
		return organization.ErrInvalidFeature
	}
	// Every stage and task consumes the one feature-level deadline established
	// at intake. Re-basing it on a later plan timestamp can make a task deadline
	// exceed its already-created budget account and must fail closed.
	deadline := feature.CreatedAt.Add(service.planningDeadline)
	budgetRevision, err := service.ensureFeatureWorkBudget(ctx, feature, evidenceID, evidenceRefs, deadline)
	if err != nil {
		return err
	}

	tasks := make(map[kernel.UUIDv7]*trackedTask, len(plan.Tasks))
	for _, item := range plan.Tasks {
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
		if err := service.registerExecution(ctx, owner, profileConfig); err != nil {
			return err
		}
		profile := service.workProfile(feature, item, evidenceID, deadline)
		tracked := &trackedTask{plan: item, revision: 1, last: taskEvents[item.ID], profile: profile, owner: owner}
		if err := service.applyTaskCommand(ctx, feature, tracked, "tekroo.command.task.bind-work-profile", kernel.SchemaVersion, service.policyAuthority, profile, evidenceRefs, nil, "profile"); err != nil {
			return err
		}
		if len(item.DependsOn) == 0 {
			if err := service.activateTask(ctx, feature, tracked, profileConfig, workspace, budgetRevision, nil, evidenceRefs, evidenceID, nil); err != nil {
				return err
			}
		}
		tasks[item.ID] = tracked
	}
	_ = tasks
	return nil
}

func (service *ProductionService) workProfile(feature organization.FeatureRequest, task organization.PlannedTask, evidenceID kernel.UUIDv7, deadline time.Time) kernel.WorkRiskProfile {
	ambiguity, novelty, blast, security := kernel.AmbiguityLow, kernel.NoveltyRoutine, kernel.BlastLocal, kernel.SecurityOrdinary
	if task.Complexity >= 7 {
		ambiguity, novelty, blast = kernel.AmbiguityHigh, kernel.NoveltyUnfamiliar, kernel.BlastMultiComponent
	} else if task.Complexity >= 4 {
		ambiguity = kernel.AmbiguityMedium
	}
	if task.Risk == organization.RiskHigh {
		security = kernel.SecuritySensitive
	}
	if task.Risk == organization.RiskCritical {
		security, blast = kernel.SecurityCritical, kernel.BlastArchitectural
	}
	criteria, _ := json.Marshal(task.AcceptanceCriteria)
	independence := []kernel.IndependenceDimension{kernel.IndependencePrincipal, kernel.IndependenceActor, kernel.IndependenceExecution, kernel.IndependenceContext, kernel.IndependenceWorkspace, kernel.IndependenceMethod}
	if task.Purpose != kernel.PurposeImplementation && task.Purpose != kernel.PurposeRepair && task.Purpose != kernel.PurposePromotion {
		independence = []kernel.IndependenceDimension{kernel.IndependencePrincipal, kernel.IndependenceMethod}
	}
	profileID := deterministicOperationalUUID("work-profile", string(feature.ID), string(task.ID))
	profileDigest := digestBytes([]byte(string(feature.ID) + "\x00" + string(task.ID) + "\x00" + string(digestBytes(criteria)) + "\x00" + string(task.DecisionRoute)))
	return kernel.WorkRiskProfile{
		TaskID: task.ID, ProfileID: profileID, ProfileRevision: 1, ProfileDigest: profileDigest, LifecycleEpoch: feature.LifecycleEpoch, ScopeRevision: feature.ScopeRevision,
		WorkKind: workKindForPurpose(task.Purpose, task.Risk), Ambiguity: ambiguity, Novelty: novelty, BlastRadius: blast, SecuritySensitivity: security,
		MinimumDecisionRoute: task.DecisionRoute, AcceptanceCriteriaDigest: digestBytes(criteria), RequiredDeterministicGateIDs: append([]string(nil), service.planning.RequiredGateIDs...),
		RequiredValidationBranches: 1, RequiredIndependenceDimensions: independence,
		ImplementationVariantCount: 1, ValidCandidateQuorum: 1, VerificationTopologyDigest: service.planning.VerificationTopologyDigest,
		ClassificationPolicyRevision: service.planning.PolicyRevision, ClassificationPolicyDigest: service.planning.ClassificationPolicyDigest,
		PromotionPolicyRevision: service.planning.PolicyRevision, PromotionPolicyDigest: service.planning.PromotionPolicyDigest,
		Budgets:                 kernel.FiniteWorkBudgets{AttemptLimit: uint64(task.AttemptLimit), ReviewRoundLimit: uint64(task.ReviewRoundLimit), PromotionLimit: 1, EscalationLimit: 1, DeadlineAt: deadline},
		ClassificationAuthority: feature.SubmittedBy, ClassificationEvidenceIDs: []kernel.UUIDv7{evidenceID},
	}
}

func (service *ProductionService) registerExecution(ctx context.Context, owner organization.RoleInstanceState, profile ProductionProfile) error {
	if service == nil || service.Store == nil || !owner.ActorFQN.Valid() || !owner.Execution.Valid() || profile.ModelProfileDigest != owner.ModelProfile || !profile.RuntimeIdentityDigest.Valid() {
		return organization.ErrRoleRuntime
	}
	registration, registered, err := service.Store.ReadExecutionRegistration(ctx, owner.ActorFQN)
	if err != nil {
		return err
	}
	if registered && registration.Execution == owner.Execution {
		return nil
	}
	commandType := "tekroo.command.execution.register"
	targetID := owner.Execution.ExecutionID
	expected := kernel.MustNotExist()
	parents := []kernel.DagParent(nil)
	payloadValue := map[string]any{
		"actor_fqn": owner.ActorFQN, "execution_id": owner.Execution.ExecutionID,
		"fencing_epoch": owner.Execution.FencingEpoch, "runtime_identity": profile.RuntimeIdentityDigest,
	}
	if registered {
		if owner.Execution.FencingEpoch != registration.Execution.FencingEpoch+1 || owner.Execution.ExecutionID == registration.Execution.ExecutionID {
			return fmt.Errorf("execution replacement for %s is not the next fenced execution", owner.ActorFQN)
		}
		commandType = "tekroo.command.execution.replace"
		targetID = registration.AggregateID
		expected = kernel.NewExpectedRevision(registration.AggregateRevision)
		parents = []kernel.DagParent{{ParentEventID: registration.LastEventID, EdgeKind: kernel.EdgeCausal}}
		payloadValue = map[string]any{
			"actor_fqn": owner.ActorFQN, "prior_execution_id": registration.Execution.ExecutionID,
			"new_execution_id": owner.Execution.ExecutionID, "new_fencing_epoch": owner.Execution.FencingEpoch,
			"reason": "role process restarted",
		}
	}
	payload, err := json.Marshal(payloadValue)
	if err != nil {
		return err
	}
	command := kernel.KernelCommand{
		ContractManifest: kernel.ContractIdentity,
		CommandID:        deterministicOperationalUUID("role-execution-command", string(owner.ActorFQN), string(owner.Execution.ExecutionID)),
		CommandType:      commandType, CommandVersion: kernel.SchemaVersion,
		Target: kernel.AggregateRef{Kind: kernel.AggregateExecution, ID: targetID}, Authority: service.serviceAuthority,
		ExpectedRevision: expected, Preconditions: []kernel.AggregatePrecondition{},
		ExpectedPolicyRevision: service.provenance.PolicyRevision, ExpectedCatalogueRevision: kernel.CatalogueRevision,
		IdempotencyKey: "role-execution:" + string(owner.ActorFQN) + ":" + string(owner.Execution.ExecutionID),
		CorrelationID:  owner.Execution.ExecutionID, Causation: parents, Payload: payload, EvidenceRefs: []kernel.EvidenceRef{},
	}
	receipt, err := service.Submit(ctx, command)
	if err != nil {
		return fmt.Errorf("%s %s for %s: %w", commandType, owner.Execution.ExecutionID, owner.ActorFQN, err)
	}
	if receipt.OutcomeCode != kernel.OutcomeApplied && receipt.OutcomeCode != kernel.OutcomeNoChange {
		return fmt.Errorf("%s %s for %s rejected: %s", commandType, owner.Execution.ExecutionID, owner.ActorFQN, receipt.ReasonCode)
	}
	return nil
}

func (service *ProductionService) activateTask(ctx context.Context, feature organization.FeatureRequest, task *trackedTask, profileConfig ProductionProfile, workspace ProductionWorkspace, budgetRevision uint64, dependencyEvents []kernel.UUIDv7, evidence []kernel.EvidenceRef, evidenceID kernel.UUIDv7, conditionDigests []kernel.Digest) error {
	if err := service.applyTaskCommand(ctx, feature, task, "tekroo.command.task.mark-ready", kernel.SchemaVersion, service.policyAuthority, map[string]any{"dependency_event_ids": append([]kernel.UUIDv7{}, dependencyEvents...), "readiness_policy_revision": service.planning.PolicyRevision}, nil, nil, "ready"); err != nil {
		return err
	}
	assignmentID := deterministicOperationalUUID("assignment", string(feature.ID), string(task.plan.ID))
	qualification := profileConfig.Qualification
	assignment := map[string]any{
		"assignment_id": assignmentID, "task_id": task.plan.ID, "expected_task_revision": task.revision, "work_profile": task.profile.Binding(),
		"required_decision_route": task.plan.DecisionRoute, "selected_decision_route": qualification.DecisionRoute, "selected_actor_fqn": task.owner.ActorFQN,
		"selected_execution_id": task.owner.Execution.ExecutionID, "selected_fencing_epoch": task.owner.Execution.FencingEpoch,
		"model_profile_digest": profileConfig.ModelProfileDigest, "runtime_identity_digest": profileConfig.RuntimeIdentityDigest, "qualification": qualification,
		"selection_policy_revision": service.planning.PolicyRevision, "selection_policy_digest": service.planning.SelectionPolicyDigest,
		"hard_constraint_results": []map[string]any{{"constraint_id": "exact-role-model-workspace", "outcome": "PASS", "evidence_ids": []kernel.UUIDv7{evidenceID}}},
		"selection_reasons":       []string{"exact configured role, qualified model profile, and workspace binding"}, "evidence_ids": []kernel.UUIDv7{evidenceID},
	}
	if err := service.applyTaskCommand(ctx, feature, task, "tekroo.command.task.authorize-qualified-assignment", kernel.SchemaVersion, service.policyAuthority, assignment, evidence, nil, "assignment"); err != nil {
		return err
	}
	actorAuthority := kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: string(task.owner.ActorFQN)}
	if err := service.applyActorTaskCommand(ctx, feature, task, "tekroo.command.task.acquire-ownership", actorAuthority, map[string]any{"owner_fqn": task.owner.ActorFQN, "expected_ownership_version": 0}, "ownership"); err != nil {
		return err
	}
	if err := service.applyActorTaskCommand(ctx, feature, task, "tekroo.command.task.activate", actorAuthority, map[string]any{"owner_fqn": task.owner.ActorFQN, "ownership_version": 1}, "activate"); err != nil {
		return err
	}
	taskModelLimit := uint64(task.plan.AttemptLimit + task.plan.ReviewRoundLimit + 2)
	taskLimits := make(kernel.PurposeCounters, len(kernel.AllWorkPurposes))
	for _, purpose := range kernel.AllWorkPurposes {
		taskLimits[purpose] = 0
	}
	taskLimits[task.plan.Purpose] = uint64(task.plan.AttemptLimit)
	taskLimits[kernel.PurposeRepair] = uint64(task.plan.ReviewRoundLimit)
	taskLimits[kernel.PurposeEscalation] = 1
	budgetPayload := map[string]any{"task_id": task.plan.ID, "budget_account_id": feature.BudgetAccountID, "expected_task_revision": task.revision, "lifecycle_epoch": feature.LifecycleEpoch, "scope_revision": feature.ScopeRevision, "task_model_invocation_limit": taskModelLimit, "purpose_limits": taskLimits, "evidence_ids": []kernel.UUIDv7{evidenceID}}
	preconditions := []kernel.AggregatePrecondition{{Aggregate: kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: feature.BudgetAccountID}, Expected: kernel.NewExpectedRevision(budgetRevision)}}
	if err := service.applyTaskCommand(ctx, feature, task, "tekroo.command.task.bind-work-budget", kernel.OperationalSchemaVersion, service.policyAuthority, budgetPayload, evidence, preconditions, "task-budget"); err != nil {
		return err
	}
	scopePayload := map[string]any{"task_id": task.plan.ID, "expected_task_revision": task.revision, "lifecycle_epoch": feature.LifecycleEpoch, "scope_revision": feature.ScopeRevision, "owner_fqn": task.owner.ActorFQN, "execution_id": task.owner.Execution.ExecutionID, "fencing_epoch": task.owner.Execution.FencingEpoch, "workspace_id": workspace.WorkspaceID, "worktree_id": workspace.WorktreeID, "branch": workspace.Branch, "baseline_sha": workspace.BaselineSHA, "writable_paths": workspace.WritablePaths, "interface_constraint_evidence_ids": []kernel.UUIDv7{evidenceID}}
	if err := service.applyTaskCommand(ctx, feature, task, "tekroo.command.task.bind-operational-scope", kernel.OperationalSchemaVersion, service.policyAuthority, scopePayload, evidence, nil, "scope"); err != nil {
		return err
	}
	return service.authorizeTaskInvocationWithCondition(ctx, feature, task, profileConfig, workspace, budgetRevision, task.plan.Purpose, 1, nil, conditionDigests)
}

func (service *ProductionService) authorizeTaskInvocation(ctx context.Context, feature organization.FeatureRequest, task *trackedTask, profile ProductionProfile, workspace ProductionWorkspace, budgetRevision, attempt uint64, retry *kernel.WorkInvocation) error {
	return service.authorizeTaskInvocationWithCondition(ctx, feature, task, profile, workspace, budgetRevision, task.plan.Purpose, attempt, retry, nil)
}

func (service *ProductionService) authorizeTaskInvocationWithCondition(ctx context.Context, feature organization.FeatureRequest, task *trackedTask, profile ProductionProfile, workspace ProductionWorkspace, budgetRevision uint64, purpose kernel.WorkPurpose, attempt uint64, retry *kernel.WorkInvocation, conditionDigests []kernel.Digest) error {
	return service.authorizeTaskInvocationWithConditionPolicy(ctx, feature, task, profile, workspace, budgetRevision, purpose, attempt, retry, conditionDigests, false)
}

func (service *ProductionService) authorizeTaskInvocationWithConditionPolicy(ctx context.Context, feature organization.FeatureRequest, task *trackedTask, profile ProductionProfile, workspace ProductionWorkspace, budgetRevision uint64, purpose kernel.WorkPurpose, attempt uint64, retry *kernel.WorkInvocation, conditionDigests []kernel.Digest, technicalExtension bool) error {
	limit := uint64(task.plan.AttemptLimit)
	if purpose == kernel.PurposeRepair {
		limit = uint64(task.plan.ReviewRoundLimit)
	}
	if attempt == 0 || attempt > limit && !technicalExtension || purpose != task.plan.Purpose && purpose != kernel.PurposeRepair || retry == nil && attempt != 1 && len(conditionDigests) == 0 || retry != nil && (purpose != retry.Purpose || attempt != retry.AttemptOrdinal+1 || retry.State != kernel.InvocationFailed && retry.State != kernel.InvocationTimedOut && retry.State != kernel.InvocationStartFailed || retry.Retryable == nil || !*retry.Retryable) {
		return organization.ErrInvalidFeature
	}
	if err := service.refreshTaskExecutionBinding(ctx, feature, task, profile, workspace); err != nil {
		return err
	}
	attemptLabel := fmt.Sprint(attempt)
	criteria, err := json.Marshal(task.plan.AcceptanceCriteria)
	if err != nil {
		return err
	}
	criteriaDigest := digestBytes(criteria)
	conditionDigest, err := taskInvocationConditionDigest(task.profile.ProfileDigest, criteriaDigest, conditionDigests)
	if err != nil {
		return err
	}
	if retry != nil {
		conditionDigest = retry.ConditionDigest
	}
	invocationIDParts := []string{"work-invocation", string(feature.ID), string(task.plan.ID), string(purpose), attemptLabel}
	if len(conditionDigests) > 0 {
		invocationIDParts = append(invocationIDParts, string(conditionDigest))
	}
	invocationID := deterministicOperationalUUID(invocationIDParts...)
	idempotencyKey := "feature:" + string(feature.ID) + ":invocation-" + string(task.plan.ID) + "-" + strings.ToLower(string(purpose)) + "-" + attemptLabel + "-" + string(conditionDigest) + "-execution-" + string(task.owner.Execution.ExecutionID)
	outputPredicateDigest := digestBytes([]byte("accepted-task-output\x00" + string(task.plan.ID) + "\x00" + string(criteriaDigest) + "\x00" + string(conditionDigest)))
	var retryID *kernel.UUIDv7
	retryOrdinal := uint64(0)
	if retry != nil {
		value := retry.ID
		retryID = &value
		retryOrdinal = retry.RetryOrdinal + 1
	}
	payload, err := json.Marshal(map[string]any{
		"invocation_id": invocationID, "task_id": task.plan.ID, "budget_account_id": feature.BudgetAccountID,
		"expected_budget_revision": budgetRevision, "expected_task_revision": task.revision,
		"lifecycle_epoch": feature.LifecycleEpoch, "scope_revision": feature.ScopeRevision, "parent_event_id": task.last,
		"work_profile": task.profile.Binding(), "qualified_assignment_id": deterministicOperationalUUID("assignment", string(feature.ID), string(task.plan.ID)),
		"purpose": purpose, "attempt_family": strings.ToLower(string(purpose)), "attempt_ordinal": attempt,
		"condition_digest": conditionDigest, "retry_of_invocation_id": retryID, "retry_ordinal": retryOrdinal,
		"output_predicate_digest": outputPredicateDigest, "allowed_terminal_outcomes": []kernel.WorkInvocationState{kernel.InvocationSucceeded, kernel.InvocationFailed, kernel.InvocationTimedOut, kernel.InvocationCancelled, kernel.InvocationStartFailed},
		"tool_policy_digest": profile.ToolPolicyDigest, "effect_policy_digest": profile.EffectPolicyDigest,
		"actor_fqn": task.owner.ActorFQN, "execution_id": task.owner.Execution.ExecutionID, "fencing_epoch": task.owner.Execution.FencingEpoch,
		"model_profile_digest": profile.ModelProfileDigest, "runtime_identity_digest": profile.RuntimeIdentityDigest,
		"workspace_id": workspace.WorkspaceID, "deadline_at": task.profile.Budgets.DeadlineAt,
		"idempotency_key": idempotencyKey, "admission_policy_revision": service.planning.PolicyRevision, "admission_policy_digest": service.planning.BudgetPolicyDigest,
	})
	if err != nil {
		return err
	}
	command := kernel.KernelCommand{
		ContractManifest: kernel.ContractIdentity,
		CommandID:        workInvocationAuthorizationCommandID(feature.ID, invocationID, task.owner.Execution.ExecutionID),
		CommandType:      "tekroo.command.work-invocation.authorize", CommandVersion: kernel.OperationalSchemaVersion,
		Target: kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: invocationID}, Authority: service.policyAuthority,
		ExpectedRevision: kernel.MustNotExist(),
		Preconditions: []kernel.AggregatePrecondition{
			{Aggregate: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.plan.ID}, Expected: kernel.NewExpectedRevision(task.revision)},
			{Aggregate: kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: feature.BudgetAccountID}, Expected: kernel.NewExpectedRevision(budgetRevision)},
		},
		ExpectedPolicyRevision: service.provenance.PolicyRevision, ExpectedCatalogueRevision: kernel.CatalogueRevision,
		IdempotencyKey: idempotencyKey, CorrelationID: feature.ID,
		Causation: []kernel.DagParent{{ParentEventID: task.last, EdgeKind: kernel.EdgeCausal}}, Payload: payload, EvidenceRefs: []kernel.EvidenceRef{},
	}
	receipt, err := service.Submit(ctx, command)
	if err != nil {
		return fmt.Errorf("%s: %w", command.CommandType, err)
	}
	if receipt.OutcomeCode != kernel.OutcomeApplied && receipt.OutcomeCode != kernel.OutcomeNoChange {
		return fmt.Errorf("%s rejected: %s", command.CommandType, receipt.ReasonCode)
	}
	return nil
}

func (service *ProductionService) extendTaskTechnicalRetryBudget(ctx context.Context, feature organization.FeatureRequest, task *trackedTask, purpose kernel.WorkPurpose, attempt uint64) (uint64, error) {
	taskRef := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.plan.ID}
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: taskRef})
	if err != nil {
		return 0, err
	}
	binding, bindingFound := snapshot.TaskWorkBudgets[taskRef]
	budgetRef := kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: feature.BudgetAccountID}
	account, accountFound := snapshot.WorkBudgetAccounts[budgetRef]
	profile, profileFound := snapshot.WorkProfiles[taskRef]
	if snapshot.State == nil || snapshot.State.Revision != task.revision || !bindingFound || !binding.Valid() || binding.BudgetAccountID != feature.BudgetAccountID || !accountFound || !account.Valid() || !profileFound || !profile.Valid() {
		return 0, organization.ErrInvalidFeature
	}
	limits := binding.PurposeLimits.Clone()
	requiredPurposeLimit := binding.PurposeUsed[purpose] + 1
	if limits[purpose] < requiredPurposeLimit {
		limits[purpose] = requiredPurposeLimit
	}
	modelLimit := binding.ModelInvocationLimit
	if modelLimit < binding.ModelInvocationsUsed+1 {
		modelLimit = binding.ModelInvocationsUsed + 1
	}
	if modelLimit > account.ModelInvocationLimit || limits[purpose] > account.PurposeLimits[purpose] {
		return 0, organization.ErrInvalidFeature
	}
	evidenceIDs := profile.Profile.ClassificationEvidenceIDs
	evidence, err := evidenceRefsForIDs(snapshot, evidenceIDs)
	if err != nil {
		return 0, err
	}
	payload := map[string]any{
		"task_id": task.plan.ID, "budget_account_id": feature.BudgetAccountID, "expected_task_revision": task.revision,
		"lifecycle_epoch": feature.LifecycleEpoch, "scope_revision": feature.ScopeRevision,
		"task_model_invocation_limit": modelLimit, "purpose_limits": limits, "evidence_ids": evidenceIDs,
	}
	preconditions := []kernel.AggregatePrecondition{{Aggregate: budgetRef, Expected: kernel.NewExpectedRevision(account.Revision)}}
	key := "technical-retry-budget-" + strings.ToLower(string(purpose)) + "-" + fmt.Sprint(attempt) + "-" + string(task.owner.Execution.ExecutionID)
	if err := service.applyTaskCommand(ctx, feature, task, "tekroo.command.task.bind-work-budget", kernel.OperationalSchemaVersion, service.policyAuthority, payload, evidence, preconditions, key); err != nil {
		return 0, err
	}
	return account.Revision, nil
}

func workInvocationAuthorizationCommandID(featureID, invocationID, executionID kernel.UUIDv7) kernel.UUIDv7 {
	return deterministicOperationalUUID("command", string(featureID), "tekroo.command.work-invocation.authorize", string(invocationID), string(executionID))
}

type taskExecutionRefreshPlan struct {
	assignment bool
	scope      bool
}

func planTaskExecutionRefresh(task *trackedTask, profile ProductionProfile, workspace ProductionWorkspace, snapshot kernel.Snapshot) (taskExecutionRefreshPlan, error) {
	if task == nil || task.plan.Owner != task.owner.ActorFQN || task.owner.WorkspaceID != workspace.WorkspaceID || task.owner.ModelProfile != task.plan.ModelProfile || task.owner.Execution.Valid() == false {
		return taskExecutionRefreshPlan{}, organization.ErrInvalidFeature
	}
	taskRef := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.plan.ID}
	assignment, assignmentFound := snapshot.QualifiedAssignments[taskRef]
	scope, scopeFound := snapshot.TaskOperationalScopes[taskRef]
	profileSnapshot, profileFound := snapshot.WorkProfiles[taskRef]
	currentExecution, executionFound := snapshot.CurrentExecutions[task.owner.ActorFQN]
	if snapshot.State == nil || snapshot.State.Revision != task.revision || snapshot.State.Ownership.OwnerFQN == nil || *snapshot.State.Ownership.OwnerFQN != task.owner.ActorFQN || !assignmentFound || !assignment.Valid() || !scopeFound || !scope.Valid() || !profileFound || !profileSnapshot.Valid() || !executionFound || currentExecution != task.owner.Execution {
		return taskExecutionRefreshPlan{}, organization.ErrInvalidFeature
	}
	if assignment.TaskID != task.plan.ID || assignment.SelectedActorFQN != task.owner.ActorFQN || assignment.WorkProfile != task.profile.Binding() || assignment.ModelProfileDigest != profile.ModelProfileDigest || assignment.RuntimeIdentityDigest != profile.RuntimeIdentityDigest || assignment.Qualification.QualificationID != profile.Qualification.QualificationID || assignment.Qualification.QualificationDigest != profile.Qualification.QualificationDigest || assignment.Qualification.QualificationCorpusDigest != profile.Qualification.QualificationCorpusDigest || assignment.Qualification.ModelProfileDigest != profile.Qualification.ModelProfileDigest || assignment.Qualification.DecisionRoute != profile.Qualification.DecisionRoute || assignment.Qualification.QualifiedRole != profile.Qualification.QualifiedRole || assignment.Qualification.Status != profile.Qualification.Status || !assignment.Qualification.ObservedAt.Equal(profile.Qualification.ObservedAt) {
		return taskExecutionRefreshPlan{}, organization.ErrInvalidFeature
	}
	if scope.TaskID != task.plan.ID || scope.OwnerFQN != task.owner.ActorFQN || scope.WorkspaceID != workspace.WorkspaceID || scope.WorktreeID != workspace.WorktreeID || scope.Branch != workspace.Branch || !slices.Equal(scope.WritablePaths, sortedStrings(workspace.WritablePaths)) {
		return taskExecutionRefreshPlan{}, organization.ErrInvalidFeature
	}
	return taskExecutionRefreshPlan{
		assignment: assignment.SelectedExecution() != task.owner.Execution,
		scope:      scope.Execution != task.owner.Execution || scope.BaselineSHA != workspace.BaselineSHA,
	}, nil
}

func (service *ProductionService) refreshTaskExecutionBinding(ctx context.Context, feature organization.FeatureRequest, task *trackedTask, profile ProductionProfile, workspace ProductionWorkspace) error {
	taskRef := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.plan.ID}
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: taskRef})
	if err != nil {
		return err
	}
	plan, err := planTaskExecutionRefresh(task, profile, workspace, snapshot)
	if err != nil {
		return err
	}
	assignment := snapshot.QualifiedAssignments[taskRef]
	scope := snapshot.TaskOperationalScopes[taskRef]
	if plan.assignment {
		evidence, err := evidenceRefsForIDs(snapshot, assignment.EvidenceIDs)
		if err != nil {
			return err
		}
		payload := map[string]any{
			"assignment_id": assignment.AssignmentID, "task_id": task.plan.ID, "expected_task_revision": task.revision,
			"work_profile": assignment.WorkProfile, "required_decision_route": assignment.RequiredDecisionRoute,
			"selected_decision_route": assignment.SelectedDecisionRoute, "selected_actor_fqn": task.owner.ActorFQN,
			"selected_execution_id": task.owner.Execution.ExecutionID, "selected_fencing_epoch": task.owner.Execution.FencingEpoch,
			"model_profile_digest": assignment.ModelProfileDigest, "runtime_identity_digest": assignment.RuntimeIdentityDigest,
			"qualification": assignment.Qualification, "selection_policy_revision": assignment.SelectionPolicyRevision,
			"selection_policy_digest": assignment.SelectionPolicyDigest, "hard_constraint_results": assignment.HardConstraintResults,
			"selection_reasons": assignment.SelectionReasons, "evidence_ids": assignment.EvidenceIDs,
		}
		key := "assignment-execution-refresh-" + string(task.owner.Execution.ExecutionID)
		if err := service.applyTaskCommand(ctx, feature, task, "tekroo.command.task.authorize-qualified-assignment", kernel.SchemaVersion, service.policyAuthority, payload, evidence, nil, key); err != nil {
			return err
		}
	}
	if plan.scope {
		evidence, err := evidenceRefsForIDs(snapshot, scope.InterfaceEvidenceIDs)
		if err != nil {
			return err
		}
		payload := map[string]any{
			"task_id": task.plan.ID, "expected_task_revision": task.revision, "lifecycle_epoch": feature.LifecycleEpoch,
			"scope_revision": feature.ScopeRevision, "owner_fqn": task.owner.ActorFQN,
			"execution_id": task.owner.Execution.ExecutionID, "fencing_epoch": task.owner.Execution.FencingEpoch,
			"workspace_id": workspace.WorkspaceID, "worktree_id": workspace.WorktreeID, "branch": workspace.Branch,
			"baseline_sha": workspace.BaselineSHA, "writable_paths": workspace.WritablePaths,
			"interface_constraint_evidence_ids": scope.InterfaceEvidenceIDs,
		}
		key := "scope-execution-refresh-" + string(task.owner.Execution.ExecutionID) + "-" + workspace.BaselineSHA
		if err := service.applyTaskCommand(ctx, feature, task, "tekroo.command.task.bind-operational-scope", kernel.OperationalSchemaVersion, service.policyAuthority, payload, evidence, nil, key); err != nil {
			return err
		}
	}
	return nil
}

func evidenceRefsForIDs(snapshot kernel.Snapshot, ids []kernel.UUIDv7) ([]kernel.EvidenceRef, error) {
	refs := make([]kernel.EvidenceRef, 0, len(ids))
	for _, id := range ids {
		metadata, found := snapshot.Evidence[id]
		if !found || !metadata.Available || !metadata.SHA256.Valid() {
			return nil, organization.ErrInvalidFeature
		}
		refs = append(refs, kernel.EvidenceRef{EvidenceID: id, SHA256: metadata.SHA256})
	}
	return refs, nil
}

func sortedStrings(values []string) []string {
	result := append([]string(nil), values...)
	slices.Sort(result)
	return result
}

func taskInvocationConditionDigest(profileDigest, criteriaDigest kernel.Digest, conditionDigests []kernel.Digest) (kernel.Digest, error) {
	if !profileDigest.Valid() || !criteriaDigest.Valid() {
		return "", organization.ErrInvalidFeature
	}
	conditionInput := string(profileDigest) + "\x00" + string(criteriaDigest)
	for _, digest := range conditionDigests {
		if !digest.Valid() {
			return "", organization.ErrInvalidFeature
		}
		conditionInput += "\x00" + string(digest)
	}
	return digestBytes([]byte(conditionInput)), nil
}

func workKindForPurpose(purpose kernel.WorkPurpose, risk organization.RiskLevel) kernel.WorkKind {
	switch purpose {
	case kernel.PurposeInvestigation:
		return kernel.WorkInvestigation
	case kernel.PurposeValidation, kernel.PurposeReview:
		if risk == organization.RiskHigh || risk == organization.RiskCritical {
			return kernel.WorkSecurityReview
		}
		return kernel.WorkValidation
	case kernel.PurposeRepair:
		return kernel.WorkDebugging
	case kernel.PurposePromotion:
		return kernel.WorkRelease
	case kernel.PurposeHandoff, kernel.PurposeReplan, kernel.PurposeEscalation:
		return kernel.WorkDesign
	default:
		return kernel.WorkImplementation
	}
}

func (service *ProductionService) applyTaskCommand(ctx context.Context, feature organization.FeatureRequest, task *trackedTask, commandType, version string, authority kernel.PrincipalRef, payload any, evidence []kernel.EvidenceRef, preconditions []kernel.AggregatePrecondition, key string) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	receipt, err := service.submitDeterministicCommand(ctx, feature, commandType, version, kernel.AggregateTask, task.plan.ID, authority, task.revision, encoded, []kernel.DagParent{{ParentEventID: task.last, EdgeKind: kernel.EdgeCausal}}, evidence, key+"-"+string(task.plan.ID), preconditions...)
	if err != nil {
		return err
	}
	if len(receipt.EventIDs) != 1 {
		return errors.New("task transition did not return one event")
	}
	task.revision = revisionAfter(receipt, task.revision)
	task.last = receipt.EventIDs[0]
	return nil
}

func (service *ProductionService) applyActorTaskCommand(ctx context.Context, feature organization.FeatureRequest, task *trackedTask, commandType string, authority kernel.PrincipalRef, payload any, key string) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	receipt, err := service.submitDeterministicActorCommand(ctx, feature, commandType, task.plan.ID, authority, task.owner.ActorFQN, task.owner.Execution, task.revision, encoded, []kernel.DagParent{{ParentEventID: task.last, EdgeKind: kernel.EdgeCausal}}, key+"-"+string(task.plan.ID))
	if err != nil {
		return err
	}
	if len(receipt.EventIDs) != 1 {
		return errors.New("actor task transition did not return one event")
	}
	task.revision = revisionAfter(receipt, task.revision)
	task.last = receipt.EventIDs[0]
	return nil
}

func (service *ProductionService) submitDeterministicCommand(ctx context.Context, feature organization.FeatureRequest, commandType, version string, kind kernel.AggregateKind, id kernel.UUIDv7, authority kernel.PrincipalRef, revision uint64, payload []byte, parents []kernel.DagParent, evidence []kernel.EvidenceRef, key string, preconditions ...kernel.AggregatePrecondition) (kernel.CommandReceipt, error) {
	command := kernel.KernelCommand{ContractManifest: kernel.ContractIdentity, CommandID: deterministicOperationalUUID("command", string(feature.ID), commandType, string(id), key), CommandType: commandType, CommandVersion: version, Target: kernel.AggregateRef{Kind: kind, ID: id}, Authority: authority, ExpectedRevision: expectedRevision(revision), ExpectedLifecycleEpoch: expectedLifecycleEpoch(kind, revision, feature.LifecycleEpoch), Preconditions: append([]kernel.AggregatePrecondition(nil), preconditions...), ExpectedPolicyRevision: service.provenance.PolicyRevision, ExpectedCatalogueRevision: kernel.CatalogueRevision, IdempotencyKey: "feature:" + string(feature.ID) + ":" + key, CorrelationID: feature.ID, Causation: append([]kernel.DagParent(nil), parents...), Payload: append([]byte(nil), payload...), EvidenceRefs: append([]kernel.EvidenceRef(nil), evidence...)}
	receipt, err := service.Submit(ctx, command)
	if err != nil {
		return receipt, fmt.Errorf("%s: %w", commandType, err)
	}
	if receipt.OutcomeCode != kernel.OutcomeApplied && receipt.OutcomeCode != kernel.OutcomeNoChange {
		return receipt, fmt.Errorf("%s rejected: %s", commandType, receipt.ReasonCode)
	}
	return receipt, nil
}

func (service *ProductionService) submitDeterministicActorCommand(ctx context.Context, feature organization.FeatureRequest, commandType string, id kernel.UUIDv7, authority kernel.PrincipalRef, actor kernel.ActorFQN, execution kernel.ExecutionTuple, revision uint64, payload []byte, parents []kernel.DagParent, key string) (kernel.CommandReceipt, error) {
	command := kernel.KernelCommand{ContractManifest: kernel.ContractIdentity, CommandID: deterministicOperationalUUID("command", string(feature.ID), commandType, string(id), key), CommandType: commandType, CommandVersion: kernel.SchemaVersion, Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: id}, Authority: authority, ActorFQN: &actor, Execution: &execution, ExpectedRevision: kernel.NewExpectedRevision(revision), ExpectedLifecycleEpoch: expectedLifecycleEpoch(kernel.AggregateTask, revision, feature.LifecycleEpoch), Preconditions: []kernel.AggregatePrecondition{}, ExpectedPolicyRevision: service.provenance.PolicyRevision, ExpectedCatalogueRevision: kernel.CatalogueRevision, IdempotencyKey: "feature:" + string(feature.ID) + ":" + key, CorrelationID: feature.ID, Causation: append([]kernel.DagParent(nil), parents...), Payload: append([]byte(nil), payload...), EvidenceRefs: []kernel.EvidenceRef{}}
	receipt, err := service.Submit(ctx, command)
	if err != nil {
		return receipt, fmt.Errorf("%s: %w", commandType, err)
	}
	if receipt.OutcomeCode != kernel.OutcomeApplied && receipt.OutcomeCode != kernel.OutcomeNoChange {
		return receipt, fmt.Errorf("%s rejected: %s", commandType, receipt.ReasonCode)
	}
	return receipt, nil
}

func deterministicOperationalUUID(parts ...string) kernel.UUIDv7 {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(part))
	}
	value := hex.EncodeToString(hash.Sum(nil)[:16])
	value = value[:12] + "7" + value[13:16] + "8" + value[17:]
	return kernel.UUIDv7(value[:8] + "-" + value[8:12] + "-" + value[12:16] + "-" + value[16:20] + "-" + value[20:32])
}

func digestBytes(value []byte) kernel.Digest {
	digest := sha256.Sum256(value)
	return kernel.Digest(hex.EncodeToString(digest[:]))
}
func expectedRevision(revision uint64) kernel.ExpectedRevision {
	if revision == 0 {
		return kernel.MustNotExist()
	}
	return kernel.NewExpectedRevision(revision)
}
func expectedLifecycleEpoch(kind kernel.AggregateKind, revision, epoch uint64) *uint64 {
	if revision == 0 || kind != kernel.AggregateTask && kind != kernel.AggregateStory {
		return nil
	}
	value := epoch
	return &value
}
func revisionOrOne(receipt kernel.CommandReceipt) uint64 {
	if receipt.ResultingRevision != nil {
		return *receipt.ResultingRevision
	}
	return 1
}
func revisionAfter(receipt kernel.CommandReceipt, prior uint64) uint64 {
	if receipt.ResultingRevision != nil {
		return *receipt.ResultingRevision
	}
	return prior + 1
}

const FeaturePlanningVersion = "phase6"
