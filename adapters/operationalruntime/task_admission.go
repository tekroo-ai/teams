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

var ErrInsufficientExecutionRunway = errors.New("insufficient execution runway")

func (service *ProductionService) ensureFeatureWorkBudget(ctx context.Context, feature organization.FeatureRequest, evidenceID kernel.UUIDv7, evidence []kernel.EvidenceRef, deadline time.Time) (kernel.WorkBudgetAccount, error) {
	budgetRef := kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: feature.BudgetAccountID}
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: budgetRef})
	if err != nil {
		return kernel.WorkBudgetAccount{}, err
	}
	if account, found := snapshot.WorkBudgetAccounts[budgetRef]; found {
		planningStory := kernel.AggregateRef{Kind: kernel.AggregateStory, ID: deterministicOperationalUUID("feature-planning-story", string(feature.ID))}
		if !account.Valid() || account.LifecycleEpoch != feature.LifecycleEpoch || account.RootWork != planningStory || account.DeadlineAt.Before(deadline) {
			return kernel.WorkBudgetAccount{}, organization.ErrInvalidFeature
		}
		if account.PolicyRevision < service.planning.PolicyRevision && service.clock.Now().UTC().Before(account.DeadlineAt) {
			account, err = service.upgradeFeatureBudgetPolicy(ctx, feature, account, evidence)
			if err != nil {
				return kernel.WorkBudgetAccount{}, err
			}
		}
		if !featureBudgetPolicyAccepted(account, service.planning, service.clock.Now().UTC()) {
			return kernel.WorkBudgetAccount{}, organization.ErrInvalidFeature
		}
		return account, nil
	}
	planningStoryID := deterministicOperationalUUID("feature-planning-story", string(feature.ID))
	storyState, storyEvent, found, err := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateStory, ID: planningStoryID})
	if err != nil || !found || storyState.Revision == 0 {
		return kernel.WorkBudgetAccount{}, errors.Join(organization.ErrInvalidFeature, err)
	}
	modelLimit := uint64(feature.Input.MaximumTasks)*4 + uint64(feature.Input.MaximumHops) + 16
	if modelLimit == 0 || modelLimit > 1000 {
		return kernel.WorkBudgetAccount{}, organization.ErrInvalidFeature
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
		return kernel.WorkBudgetAccount{}, err
	}
	_, err = service.submitDeterministicCommand(ctx, feature, "tekroo.command.work-budget.create", kernel.OperationalSchemaVersion, kernel.AggregateWorkBudget, feature.BudgetAccountID, service.policyAuthority, 0, payload, []kernel.DagParent{{ParentEventID: storyEvent, EdgeKind: kernel.EdgeDerivation}}, evidence, "budget", kernel.AggregatePrecondition{Aggregate: kernel.AggregateRef{Kind: kernel.AggregateStory, ID: planningStoryID}, Expected: kernel.NewExpectedRevision(storyState.Revision)})
	if err != nil {
		return kernel.WorkBudgetAccount{}, err
	}
	snapshot, err = service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: budgetRef})
	if err != nil {
		return kernel.WorkBudgetAccount{}, err
	}
	account, found := snapshot.WorkBudgetAccounts[budgetRef]
	if !found || !account.Valid() {
		return kernel.WorkBudgetAccount{}, organization.ErrInvalidFeature
	}
	return account, nil
}

func (service *ProductionService) upgradeFeatureBudgetPolicy(ctx context.Context, feature organization.FeatureRequest, account kernel.WorkBudgetAccount, evidence []kernel.EvidenceRef) (kernel.WorkBudgetAccount, error) {
	if !account.Valid() || account.PolicyRevision >= service.planning.PolicyRevision || account.DeadlineAt.IsZero() || !service.clock.Now().UTC().Before(account.DeadlineAt) {
		return kernel.WorkBudgetAccount{}, organization.ErrInvalidFeature
	}
	payload, err := json.Marshal(map[string]any{
		"budget_account_id": account.ID, "expected_budget_revision": account.Revision,
		"expected_lifecycle_epoch": account.LifecycleEpoch, "policy_revision": service.planning.PolicyRevision,
		"policy_digest": service.planning.BudgetPolicyDigest, "model_invocation_limit": account.ModelInvocationLimit,
		"purpose_limits": account.PurposeLimits, "deadline_at": account.DeadlineAt,
		"reason":       "advance an active feature budget to the configured planning policy",
		"evidence_ids": evidenceIDs(evidence), "authority": feature.SubmittedBy,
	})
	if err != nil {
		return kernel.WorkBudgetAccount{}, err
	}
	key := fmt.Sprintf("budget-policy-%d-to-%d", account.PolicyRevision, service.planning.PolicyRevision)
	if _, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.work-budget.amend", kernel.OperationalSchemaVersion, kernel.AggregateWorkBudget, account.ID, feature.SubmittedBy, account.Revision, payload, []kernel.DagParent{{ParentEventID: account.LastEventID, EdgeKind: kernel.EdgeCausal}}, evidence, key); err != nil {
		return kernel.WorkBudgetAccount{}, err
	}
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: account.Ref()})
	if err != nil {
		return kernel.WorkBudgetAccount{}, err
	}
	updated, found := snapshot.WorkBudgetAccounts[account.Ref()]
	if !found || !updated.Valid() || updated.PolicyRevision != service.planning.PolicyRevision || updated.PolicyDigest != service.planning.BudgetPolicyDigest || !updated.DeadlineAt.Equal(account.DeadlineAt) {
		return kernel.WorkBudgetAccount{}, organization.ErrInvalidFeature
	}
	return updated, nil
}

func featureBudgetPolicyAccepted(account kernel.WorkBudgetAccount, planning ProductionPlanning, now time.Time) bool {
	currentPolicy := account.PolicyRevision == planning.PolicyRevision && account.PolicyDigest == planning.BudgetPolicyDigest
	successorPolicy := account.PolicyRevision > planning.PolicyRevision
	expiredPredecessorPolicy := account.PolicyRevision < planning.PolicyRevision && !now.Before(account.DeadlineAt)
	return currentPolicy || successorPolicy || expiredPredecessorPolicy
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
	// Every stage and task consumes the durable feature budget deadline. The
	// account begins at the intake deadline and may move only through an
	// authorized work-budget amendment; a plan timestamp never rebases it.
	deadline := feature.CreatedAt.Add(service.planningDeadline)
	budget, err := service.ensureFeatureWorkBudget(ctx, feature, evidenceID, evidenceRefs, deadline)
	if err != nil {
		return err
	}
	deadline = budget.DeadlineAt

	for _, item := range plan.Tasks {
		taskState, _, taskFound, err := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: item.ID})
		if err != nil || !taskFound || taskState.LifecycleEpoch == 0 || taskState.ScopeRevision == 0 || taskState.Phase == kernel.PhaseClosed || taskState.Condition == kernel.ConditionBlocked {
			return errors.Join(organization.ErrInvalidFeature, err)
		}
		profileConfig, found := service.profilesByModel[item.ModelProfile]
		if !found || !profileConfig.qualifiedFor(item.DecisionRoute, workKindForPurpose(item.Purpose, item.Risk), service.clock.Now().UTC()) {
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
		if _, found := service.workspacesByID[owner.WorkspaceID]; !found {
			return organization.ErrInvalidFeature
		}
		if err := service.registerExecution(ctx, owner, profileConfig); err != nil {
			return err
		}
		profile := service.workProfile(feature, item, taskState.LifecycleEpoch, taskState.ScopeRevision, evidenceID, deadline)
		tracked := &trackedTask{plan: item, revision: 1, last: taskEvents[item.ID], profile: profile, owner: owner}
		profileKey := fmt.Sprintf("profile-policy-%d-%s", profile.ClassificationPolicyRevision, profile.ProfileDigest)
		if err := service.applyTaskCommand(ctx, feature, tracked, "tekroo.command.task.bind-work-profile", kernel.SchemaVersion, service.policyAuthority, profile, evidenceRefs, nil, profileKey); err != nil {
			return err
		}
		// Admission persists every task and its immutable work profile first.
		// Reconciliation subsequently activates ready DAG nodes one at a time,
		// reloading the budget and task projections between activations. This
		// permits multiple independent roots without reusing a stale budget
		// revision or sharing an editable repository checkout.
	}
	return nil
}

func (service *ProductionService) workProfile(feature organization.FeatureRequest, task organization.PlannedTask, lifecycleEpoch, scopeRevision uint64, evidenceID kernel.UUIDv7, deadline time.Time) kernel.WorkRiskProfile {
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
		TaskID: task.ID, ProfileID: profileID, ProfileRevision: 1, ProfileDigest: profileDigest, LifecycleEpoch: lifecycleEpoch, ScopeRevision: scopeRevision,
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
	return service.activateTaskWithInvocationID(ctx, feature, task, profileConfig, workspace, budgetRevision, dependencyEvents, evidence, evidenceID, conditionDigests, nil)
}

func (service *ProductionService) activateTaskWithInvocationID(ctx context.Context, feature organization.FeatureRequest, task *trackedTask, profileConfig ProductionProfile, workspace ProductionWorkspace, budgetRevision uint64, dependencyEvents []kernel.UUIDv7, evidence []kernel.EvidenceRef, evidenceID kernel.UUIDv7, conditionDigests []kernel.Digest, authorizedInvocationID *kernel.UUIDv7) error {
	if task == nil || !profileConfig.qualificationDefinitionValid() || service == nil || service.clock == nil || !profileConfig.qualifiedFor(task.plan.DecisionRoute, workKindForPurpose(task.plan.Purpose, task.plan.Risk), service.clock.Now().UTC()) {
		return organization.ErrInvalidFeature
	}
	taskRef := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.plan.ID}
	state, head, found, err := service.Store.ReadAggregateHead(ctx, taskRef)
	if err != nil || !found {
		return errors.Join(organization.ErrInvalidFeature, err)
	}
	task.revision, task.last = state.Revision, head
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: taskRef})
	if err != nil {
		return err
	}
	profileSnapshot, profileFound := snapshot.WorkProfiles[taskRef]
	if !profileFound || !profileSnapshot.Valid() || profileSnapshot.Profile.Binding() != task.profile.Binding() {
		return organization.ErrInvalidFeature
	}
	if state.Phase == kernel.PhasePlanned {
		if err := service.applyTaskCommand(ctx, feature, task, "tekroo.command.task.mark-ready", kernel.SchemaVersion, service.policyAuthority, map[string]any{"dependency_event_ids": append([]kernel.UUIDv7{}, dependencyEvents...), "readiness_policy_revision": service.planning.PolicyRevision}, nil, nil, "ready"); err != nil {
			return err
		}
		state.Phase = kernel.PhaseReady
		snapshot, err = service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: taskRef})
		if err != nil {
			return err
		}
		if snapshot.State == nil || snapshot.State.Revision != task.revision {
			return organization.ErrInvalidFeature
		}
	}
	if state.Phase != kernel.PhaseReady && state.Phase != kernel.PhaseActive {
		return organization.ErrInvalidFeature
	}
	if _, assignmentFound := snapshot.QualifiedAssignments[taskRef]; assignmentFound {
		if err := service.rebindPlanningRecoveryAssignment(ctx, feature, task, profileConfig, task.owner); err != nil {
			return err
		}
	} else {
		assignmentID := deterministicOperationalUUID("assignment", string(feature.ID), string(task.plan.ID))
		qualification, qualified := profileConfig.qualificationReceipt()
		if !qualified {
			return organization.ErrInvalidFeature
		}
		assignment := map[string]any{
			"assignment_id": assignmentID, "task_id": task.plan.ID, "expected_task_revision": task.revision, "work_profile": task.profile.Binding(),
			"required_decision_route": task.plan.DecisionRoute, "selected_decision_route": qualification.DecisionRoute, "selected_actor_fqn": task.owner.ActorFQN,
			"selected_execution_id": task.owner.Execution.ExecutionID, "selected_fencing_epoch": task.owner.Execution.FencingEpoch,
			"model_profile_digest": profileConfig.ModelProfileDigest, "runtime_identity_digest": profileConfig.RuntimeIdentityDigest, "qualification": qualification,
			"selection_policy_revision": service.planning.PolicyRevision, "selection_policy_digest": service.planning.SelectionPolicyDigest,
			"hard_constraint_results": []map[string]any{{"constraint_id": "exact-role-model-workspace", "outcome": "PASS", "evidence_ids": []kernel.UUIDv7{evidenceID}}},
			"selection_reasons":       []string{"exact configured role, qualified model profile, and workspace binding"}, "evidence_ids": evidenceIDs(evidence),
		}
		currentExecution, executionFound := snapshot.CurrentExecutions[task.owner.ActorFQN]
		profileSnapshot, profileFound := snapshot.WorkProfiles[taskRef]
		if snapshot.State == nil || snapshot.State.Revision != task.revision || !task.profile.Binding().TaskBindingMatches(*snapshot.State) || !profileFound || !profileSnapshot.Valid() || profileSnapshot.Profile.Binding() != task.profile.Binding() || profileSnapshot.Profile.MinimumDecisionRoute != task.plan.DecisionRoute || !executionFound || currentExecution != task.owner.Execution {
			return fmt.Errorf("qualified assignment preflight for task %s does not match current task, profile, or execution state: %w", task.plan.ID, organization.ErrInvalidFeature)
		}
		assignmentKey := "assignment-" + string(task.owner.Execution.ExecutionID) + "-" + string(task.profile.ProfileID)
		if err := service.applyTaskCommand(ctx, feature, task, "tekroo.command.task.authorize-qualified-assignment", kernel.SchemaVersion, service.policyAuthority, assignment, evidence, nil, assignmentKey); err != nil {
			return fmt.Errorf("authorize assignment at task revision %d: %w", task.revision, err)
		}
	}
	if state.Phase == kernel.PhaseReady {
		actorAuthority := kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: string(task.owner.ActorFQN)}
		if state.Ownership.OwnerFQN == nil {
			ownershipKey := "ownership-" + string(task.owner.Execution.ExecutionID)
			if err := service.applyActorTaskCommand(ctx, feature, task, "tekroo.command.task.acquire-ownership", actorAuthority, map[string]any{"owner_fqn": task.owner.ActorFQN, "expected_ownership_version": 0}, ownershipKey); err != nil {
				return err
			}
			state.Ownership.OwnerFQN = &task.owner.ActorFQN
			state.Ownership.OwnershipVersion = 1
		}
		if state.Ownership.OwnerFQN == nil || *state.Ownership.OwnerFQN != task.owner.ActorFQN || state.Ownership.OwnershipVersion != 1 {
			return organization.ErrInvalidFeature
		}
		activateKey := "activate-" + string(task.owner.Execution.ExecutionID)
		if err := service.applyActorTaskCommand(ctx, feature, task, "tekroo.command.task.activate", actorAuthority, map[string]any{"owner_fqn": task.owner.ActorFQN, "ownership_version": state.Ownership.OwnershipVersion}, activateKey); err != nil {
			return err
		}
		state.Phase = kernel.PhaseActive
	} else if state.Ownership.OwnerFQN == nil || *state.Ownership.OwnerFQN != task.owner.ActorFQN {
		return organization.ErrInvalidFeature
	}
	taskModelLimit := uint64(task.plan.AttemptLimit + task.plan.ReviewRoundLimit + 2)
	taskLimits := make(kernel.PurposeCounters, len(kernel.AllWorkPurposes))
	for _, purpose := range kernel.AllWorkPurposes {
		taskLimits[purpose] = 0
	}
	taskLimits[task.plan.Purpose] = uint64(task.plan.AttemptLimit)
	taskLimits[kernel.PurposeRepair] = uint64(task.plan.ReviewRoundLimit)
	taskLimits[kernel.PurposeEscalation] = 1
	if binding, bindingFound := snapshot.TaskWorkBudgets[taskRef]; bindingFound {
		if !binding.Valid() || binding.BudgetAccountID != feature.BudgetAccountID {
			return organization.ErrInvalidFeature
		}
	} else {
		budgetPayload := map[string]any{"task_id": task.plan.ID, "budget_account_id": feature.BudgetAccountID, "expected_task_revision": task.revision, "lifecycle_epoch": task.profile.LifecycleEpoch, "scope_revision": task.profile.ScopeRevision, "task_model_invocation_limit": taskModelLimit, "purpose_limits": taskLimits, "evidence_ids": evidenceIDs(evidence)}
		preconditions := []kernel.AggregatePrecondition{{Aggregate: kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: feature.BudgetAccountID}, Expected: kernel.NewExpectedRevision(budgetRevision)}}
		budgetKey := "task-budget-" + fmt.Sprint(budgetRevision)
		if err := service.applyTaskCommand(ctx, feature, task, "tekroo.command.task.bind-work-budget", kernel.OperationalSchemaVersion, service.policyAuthority, budgetPayload, evidence, preconditions, budgetKey); err != nil {
			return err
		}
	}
	if _, scopeFound := snapshot.TaskOperationalScopes[taskRef]; scopeFound {
		if err := service.refreshTaskExecutionBinding(ctx, feature, task, profileConfig, workspace); err != nil {
			return err
		}
	} else {
		scopePayload := map[string]any{"task_id": task.plan.ID, "expected_task_revision": task.revision, "lifecycle_epoch": task.profile.LifecycleEpoch, "scope_revision": task.profile.ScopeRevision, "owner_fqn": task.owner.ActorFQN, "execution_id": task.owner.Execution.ExecutionID, "fencing_epoch": task.owner.Execution.FencingEpoch, "workspace_id": workspace.WorkspaceID, "worktree_id": workspace.WorktreeID, "branch": workspace.Branch, "baseline_sha": workspace.BaselineSHA, "writable_paths": workspace.WritablePaths, "interface_constraint_evidence_ids": evidenceIDs(evidence)}
		scopeKey := "scope-" + string(task.owner.Execution.ExecutionID) + "-" + workspace.BaselineSHA
		if err := service.applyTaskCommand(ctx, feature, task, "tekroo.command.task.bind-operational-scope", kernel.OperationalSchemaVersion, service.policyAuthority, scopePayload, evidence, nil, scopeKey); err != nil {
			return err
		}
	}
	latestSnapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: taskRef})
	if err != nil {
		return err
	}
	if _, alreadyAuthorized := latestTaskInvocation(latestSnapshot.WorkInvocations, task.plan.ID); alreadyAuthorized {
		return nil
	}
	return service.authorizeTaskInvocationWithConditionPolicyAndID(ctx, feature, task, profileConfig, workspace, budgetRevision, task.plan.Purpose, 1, nil, conditionDigests, false, false, authorizedInvocationID)
}

func (service *ProductionService) authorizeTaskInvocation(ctx context.Context, feature organization.FeatureRequest, task *trackedTask, profile ProductionProfile, workspace ProductionWorkspace, budgetRevision, attempt uint64, retry *kernel.WorkInvocation) error {
	return service.authorizeTaskInvocationWithCondition(ctx, feature, task, profile, workspace, budgetRevision, task.plan.Purpose, attempt, retry, nil)
}

func (service *ProductionService) authorizeTaskInvocationWithCondition(ctx context.Context, feature organization.FeatureRequest, task *trackedTask, profile ProductionProfile, workspace ProductionWorkspace, budgetRevision uint64, purpose kernel.WorkPurpose, attempt uint64, retry *kernel.WorkInvocation, conditionDigests []kernel.Digest) error {
	return service.authorizeTaskInvocationWithConditionPolicy(ctx, feature, task, profile, workspace, budgetRevision, purpose, attempt, retry, conditionDigests, false, retry != nil)
}

func (service *ProductionService) authorizeTaskInvocationWithConditionPolicy(ctx context.Context, feature organization.FeatureRequest, task *trackedTask, profile ProductionProfile, workspace ProductionWorkspace, budgetRevision uint64, purpose kernel.WorkPurpose, attempt uint64, prior *kernel.WorkInvocation, conditionDigests []kernel.Digest, technicalExtension, reusePriorCondition bool) error {
	return service.authorizeTaskInvocationWithConditionPolicyAndID(ctx, feature, task, profile, workspace, budgetRevision, purpose, attempt, prior, conditionDigests, technicalExtension, reusePriorCondition, nil)
}

func (service *ProductionService) authorizeTaskInvocationWithConditionPolicyAndID(ctx context.Context, feature organization.FeatureRequest, task *trackedTask, profile ProductionProfile, workspace ProductionWorkspace, budgetRevision uint64, purpose kernel.WorkPurpose, attempt uint64, prior *kernel.WorkInvocation, conditionDigests []kernel.Digest, technicalExtension, reusePriorCondition bool, authorizedInvocationID *kernel.UUIDv7) error {
	if task == nil || !profile.qualifiedFor(task.plan.DecisionRoute, invocationWorkKind(task, purpose), service.clock.Now().UTC()) {
		return organization.ErrInvalidFeature
	}
	releaseAdmission, err := service.beginNewInvocationAdmission()
	if err != nil {
		return err
	}
	invocationCreated := false
	defer func() { releaseAdmission(invocationCreated) }()
	if service.requestTimeout > 0 && !task.profile.Budgets.DeadlineAt.After(service.clock.Now().UTC().Add(service.requestTimeout)) {
		return ErrInsufficientExecutionRunway
	}
	limit := uint64(task.plan.AttemptLimit)
	if purpose == kernel.PurposeRepair {
		limit = uint64(task.plan.ReviewRoundLimit)
	}
	if attempt == 0 || attempt > limit && !technicalExtension || purpose != task.plan.Purpose && purpose != kernel.PurposeRepair || prior == nil && (reusePriorCondition || attempt != 1 && len(conditionDigests) == 0) || prior != nil && !validInvocationContinuation(*prior, purpose, attempt, technicalExtension, reusePriorCondition) {
		return organization.ErrInvalidFeature
	}
	if err := service.refreshTaskExecutionBinding(ctx, feature, task, profile, workspace); err != nil {
		return err
	}
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.plan.ID}})
	if err != nil {
		return err
	}
	account, found := snapshot.WorkBudgetAccounts[kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: feature.BudgetAccountID}]
	if !found || !account.Valid() || account.Revision != budgetRevision || task.profile.Budgets.DeadlineAt.After(account.DeadlineAt) {
		return organization.ErrInvalidFeature
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
	if prior != nil && reusePriorCondition {
		conditionDigest = prior.ConditionDigest
	}
	invocationIDParts := []string{"work-invocation", string(feature.ID), string(task.plan.ID), string(purpose), attemptLabel}
	if len(conditionDigests) > 0 {
		invocationIDParts = append(invocationIDParts, string(conditionDigest))
	}
	invocationID := deterministicOperationalUUID(invocationIDParts...)
	if authorizedInvocationID != nil {
		if !authorizedInvocationID.Valid() || prior != nil || attempt != 1 {
			return organization.ErrInvalidFeature
		}
		invocationID = *authorizedInvocationID
	}
	idempotencyKey := "feature:" + string(feature.ID) + ":invocation-" + string(task.plan.ID) + "-" + strings.ToLower(string(purpose)) + "-" + attemptLabel + "-" + string(conditionDigest) + "-execution-" + string(task.owner.Execution.ExecutionID)
	outputPredicateDigest := digestBytes([]byte("accepted-task-output\x00" + string(task.plan.ID) + "\x00" + string(criteriaDigest) + "\x00" + string(conditionDigest)))
	var retryID *kernel.UUIDv7
	retryOrdinal := uint64(0)
	if prior != nil {
		value := prior.ID
		retryID = &value
		retryOrdinal = prior.RetryOrdinal + 1
	}
	payload, err := json.Marshal(map[string]any{
		"invocation_id": invocationID, "task_id": task.plan.ID, "budget_account_id": feature.BudgetAccountID,
		"expected_budget_revision": budgetRevision, "expected_task_revision": task.revision,
		"lifecycle_epoch": task.profile.LifecycleEpoch, "scope_revision": task.profile.ScopeRevision, "parent_event_id": task.last,
		"work_profile": task.profile.Binding(), "qualified_assignment_id": deterministicOperationalUUID("assignment", string(feature.ID), string(task.plan.ID)),
		"purpose": purpose, "attempt_family": strings.ToLower(string(purpose)), "attempt_ordinal": attempt,
		"condition_digest": conditionDigest, "retry_of_invocation_id": retryID, "retry_ordinal": retryOrdinal,
		"output_predicate_digest": outputPredicateDigest, "allowed_terminal_outcomes": []kernel.WorkInvocationState{kernel.InvocationSucceeded, kernel.InvocationFailed, kernel.InvocationTimedOut, kernel.InvocationCancelled, kernel.InvocationStartFailed},
		"tool_policy_digest": profile.ToolPolicyDigest, "effect_policy_digest": profile.EffectPolicyDigest,
		"actor_fqn": task.owner.ActorFQN, "execution_id": task.owner.Execution.ExecutionID, "fencing_epoch": task.owner.Execution.FencingEpoch,
		"model_profile_digest": profile.ModelProfileDigest, "runtime_identity_digest": profile.RuntimeIdentityDigest,
		"workspace_id": workspace.WorkspaceID, "deadline_at": task.profile.Budgets.DeadlineAt,
		"idempotency_key": idempotencyKey, "admission_policy_revision": account.PolicyRevision, "admission_policy_digest": account.PolicyDigest,
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
	invocationCreated = receipt.OutcomeCode == kernel.OutcomeApplied
	return nil
}

// A repair is a new purpose within the already classified task, not a new
// task classification. Keep it bound to the task's qualified work kind so a
// failed review can return work to the selected implementer without requiring
// an unrelated assignment or silently bypassing profile qualification.
func invocationWorkKind(task *trackedTask, purpose kernel.WorkPurpose) kernel.WorkKind {
	if task != nil && purpose == kernel.PurposeRepair {
		return task.profile.WorkKind
	}
	if task == nil {
		return ""
	}
	return workKindForPurpose(purpose, task.plan.Risk)
}

func validInvocationContinuation(prior kernel.WorkInvocation, purpose kernel.WorkPurpose, attempt uint64, technicalExtension, reusePriorCondition bool) bool {
	if !prior.Valid() || purpose != prior.Purpose || attempt != prior.AttemptOrdinal+1 {
		return false
	}
	if !reusePriorCondition {
		// HANDOFF is the configured workflow's planning purpose and reaches this
		// path only through explicit invalid-planning-output recovery. Promotion is
		// likewise admitted only for the exact invalid-structured-output block; a
		// recorded product decision remains unrecoverable and still needs a passing
		// structured result from the next promotion attempt.
		return technicalExtension && (recoverableTaskTerminal(prior) || prior.State == kernel.InvocationSucceeded && (purpose == kernel.PurposeHandoff || purpose == kernel.PurposeValidation || purpose == kernel.PurposeReview || purpose == kernel.PurposeRepair || purpose == kernel.PurposeReplan || purpose == kernel.PurposePromotion)) || !technicalExtension && prior.State == kernel.InvocationSucceeded && (purpose == kernel.PurposeValidation || purpose == kernel.PurposeReview)
	}
	if prior.Retryable == nil || !*prior.Retryable {
		return false
	}
	return prior.State == kernel.InvocationFailed || prior.State == kernel.InvocationTimedOut || prior.State == kernel.InvocationStartFailed
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
	modelLimit, limits, extensionRequired, err := technicalRetryBudgetExtension(binding, account, purpose)
	if err != nil {
		return 0, err
	}
	// A recovery pass can be interrupted after the budget binding commits but
	// before the replacement invocation is authorized. Treat the committed
	// capacity and lifecycle as the durable checkpoint instead of submitting the
	// same command again with a newer task revision. A reopened task must rebind
	// even when it already has enough capacity: invocation authorization rejects
	// a budget binding from an earlier lifecycle or scope revision.
	if !taskRetryBudgetRebindRequired(binding, task.profile, extensionRequired) {
		return account.Revision, nil
	}
	evidenceIDs := profile.Profile.ClassificationEvidenceIDs
	evidence, err := evidenceRefsForIDs(snapshot, evidenceIDs)
	if err != nil {
		return 0, err
	}
	payload := map[string]any{
		"task_id": task.plan.ID, "budget_account_id": feature.BudgetAccountID, "expected_task_revision": task.revision,
		"lifecycle_epoch": task.profile.LifecycleEpoch, "scope_revision": task.profile.ScopeRevision,
		"task_model_invocation_limit": modelLimit, "purpose_limits": limits, "evidence_ids": evidenceIDs,
	}
	preconditions := []kernel.AggregatePrecondition{{Aggregate: budgetRef, Expected: kernel.NewExpectedRevision(account.Revision)}}
	key := "technical-retry-budget-" + strings.ToLower(string(purpose)) + "-" + fmt.Sprint(attempt) + "-" + fmt.Sprint(task.profile.LifecycleEpoch) + "-" + fmt.Sprint(task.profile.ScopeRevision) + "-" + string(task.owner.Execution.ExecutionID)
	if err := service.applyTaskCommand(ctx, feature, task, "tekroo.command.task.bind-work-budget", kernel.OperationalSchemaVersion, service.policyAuthority, payload, evidence, preconditions, key); err != nil {
		return 0, err
	}
	return account.Revision, nil
}

func taskRetryBudgetRebindRequired(binding kernel.TaskWorkBudgetBinding, profile kernel.WorkRiskProfile, extensionRequired bool) bool {
	return extensionRequired || binding.LifecycleEpoch != profile.LifecycleEpoch || binding.ScopeRevision != profile.ScopeRevision
}

func technicalRetryBudgetExtension(binding kernel.TaskWorkBudgetBinding, account kernel.WorkBudgetAccount, purpose kernel.WorkPurpose) (uint64, kernel.PurposeCounters, bool, error) {
	limits := binding.PurposeLimits.Clone()
	requiredPurposeLimit := binding.PurposeUsed[purpose] + 1
	extensionRequired := false
	if limits[purpose] < requiredPurposeLimit {
		limits[purpose] = requiredPurposeLimit
		extensionRequired = true
	}
	modelLimit := binding.ModelInvocationLimit
	requiredModelLimit := binding.ModelInvocationsUsed + 1
	if modelLimit < requiredModelLimit {
		modelLimit = requiredModelLimit
		extensionRequired = true
	}
	if modelLimit > account.ModelInvocationLimit || limits[purpose] > account.PurposeLimits[purpose] {
		return 0, nil, false, organization.ErrInvalidFeature
	}
	return modelLimit, limits, extensionRequired, nil
}

func workInvocationAuthorizationCommandID(featureID, invocationID, executionID kernel.UUIDv7) kernel.UUIDv7 {
	return deterministicOperationalUUID("command", string(featureID), "tekroo.command.work-invocation.authorize", string(invocationID), string(executionID))
}

type taskExecutionRefreshPlan struct {
	assignment bool
	scope      bool
}

func planTaskExecutionRefresh(task *trackedTask, profile ProductionProfile, workspace ProductionWorkspace, snapshot kernel.Snapshot, at time.Time) (taskExecutionRefreshPlan, error) {
	workKind := kernel.WorkImplementation
	if task != nil {
		workKind = workKindForPurpose(task.plan.Purpose, task.plan.Risk)
	}
	if task == nil || !profile.qualifiedFor(task.plan.DecisionRoute, workKind, at) || task.plan.Owner != task.owner.ActorFQN || task.owner.WorkspaceID != workspace.WorkspaceID || task.owner.ModelProfile != task.plan.ModelProfile || task.owner.Execution.Valid() == false {
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
	assignmentProfileCurrent := assignment.WorkProfile == task.profile.Binding()
	assignmentProfileMaintenancePredecessor := workProfileBindingCurrentOrMaintenanceSuccessor(snapshot, task.plan.ID, assignment.WorkProfile, task.profile.ProfileDigest)
	if assignment.TaskID != task.plan.ID || assignment.SelectedActorFQN != task.owner.ActorFQN || !assignmentProfileCurrent && !assignmentProfileMaintenancePredecessor || assignment.ModelProfileDigest != profile.ModelProfileDigest || assignment.RuntimeIdentityDigest != profile.RuntimeIdentityDigest || !profile.qualificationMatches(assignment.Qualification, task.plan.DecisionRoute, workKind, at) {
		return taskExecutionRefreshPlan{}, organization.ErrInvalidFeature
	}
	if scope.TaskID != task.plan.ID || scope.OwnerFQN != task.owner.ActorFQN || scope.WorkspaceID != workspace.WorkspaceID || scope.WorktreeID != workspace.WorktreeID || scope.Branch != workspace.Branch || !slices.Equal(scope.WritablePaths, sortedStrings(workspace.WritablePaths)) {
		return taskExecutionRefreshPlan{}, organization.ErrInvalidFeature
	}
	return taskExecutionRefreshPlan{
		assignment: !assignmentProfileCurrent || assignment.SelectedExecution() != task.owner.Execution,
		scope:      scope.Execution != task.owner.Execution || scope.BaselineSHA != workspace.BaselineSHA,
	}, nil
}

func (service *ProductionService) refreshTaskExecutionBinding(ctx context.Context, feature organization.FeatureRequest, task *trackedTask, profile ProductionProfile, workspace ProductionWorkspace) error {
	taskRef := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.plan.ID}
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: taskRef})
	if err != nil {
		return err
	}
	plan, err := planTaskExecutionRefresh(task, profile, workspace, snapshot, service.clock.Now().UTC())
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
			"work_profile": task.profile.Binding(), "required_decision_route": assignment.RequiredDecisionRoute,
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
			"task_id": task.plan.ID, "expected_task_revision": task.revision, "lifecycle_epoch": task.profile.LifecycleEpoch,
			"scope_revision": task.profile.ScopeRevision, "owner_fqn": task.owner.ActorFQN,
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
	case kernel.PurposeValidation:
		return kernel.WorkValidation
	case kernel.PurposeReview:
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
	receipt, err := service.submitDeterministicCommandAtLifecycle(ctx, feature, commandType, version, kernel.AggregateTask, task.plan.ID, authority, task.revision, task.profile.LifecycleEpoch, encoded, []kernel.DagParent{{ParentEventID: task.last, EdgeKind: kernel.EdgeCausal}}, evidence, key+"-"+string(task.plan.ID), preconditions...)
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
	receipt, err := service.submitDeterministicActorCommand(ctx, feature, commandType, task.plan.ID, authority, task.owner.ActorFQN, task.owner.Execution, task.revision, task.profile.LifecycleEpoch, encoded, []kernel.DagParent{{ParentEventID: task.last, EdgeKind: kernel.EdgeCausal}}, key+"-"+string(task.plan.ID))
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
	return service.submitDeterministicCommandAtLifecycle(ctx, feature, commandType, version, kind, id, authority, revision, feature.LifecycleEpoch, payload, parents, evidence, key, preconditions...)
}

func (service *ProductionService) submitDeterministicCommandAtLifecycle(ctx context.Context, feature organization.FeatureRequest, commandType, version string, kind kernel.AggregateKind, id kernel.UUIDv7, authority kernel.PrincipalRef, revision, lifecycleEpoch uint64, payload []byte, parents []kernel.DagParent, evidence []kernel.EvidenceRef, key string, preconditions ...kernel.AggregatePrecondition) (kernel.CommandReceipt, error) {
	command := kernel.KernelCommand{ContractManifest: kernel.ContractIdentity, CommandID: deterministicOperationalUUID("command", string(feature.ID), commandType, string(id), key), CommandType: commandType, CommandVersion: version, Target: kernel.AggregateRef{Kind: kind, ID: id}, Authority: authority, ExpectedRevision: expectedRevision(revision), ExpectedLifecycleEpoch: expectedLifecycleEpoch(kind, revision, lifecycleEpoch), Preconditions: append([]kernel.AggregatePrecondition(nil), preconditions...), ExpectedPolicyRevision: service.provenance.PolicyRevision, ExpectedCatalogueRevision: kernel.CatalogueRevision, IdempotencyKey: "feature:" + string(feature.ID) + ":" + key, CorrelationID: feature.ID, Causation: append([]kernel.DagParent(nil), parents...), Payload: append([]byte(nil), payload...), EvidenceRefs: append([]kernel.EvidenceRef(nil), evidence...)}
	if _, err := service.Runtime.catalogue.ResolveCommand(command.CommandType, command.CommandVersion, command.Target.Kind, command.Payload); err != nil {
		return kernel.CommandReceipt{}, fmt.Errorf("%s payload: %w", commandType, err)
	}
	receipt, err := service.Submit(ctx, command)
	if err != nil {
		return receipt, fmt.Errorf("%s: %w", commandType, err)
	}
	if receipt.OutcomeCode != kernel.OutcomeApplied && receipt.OutcomeCode != kernel.OutcomeNoChange {
		return receipt, fmt.Errorf("%s rejected: %s", commandType, receipt.ReasonCode)
	}
	return receipt, nil
}

func (service *ProductionService) submitDeterministicActorCommand(ctx context.Context, feature organization.FeatureRequest, commandType string, id kernel.UUIDv7, authority kernel.PrincipalRef, actor kernel.ActorFQN, execution kernel.ExecutionTuple, revision, lifecycleEpoch uint64, payload []byte, parents []kernel.DagParent, key string) (kernel.CommandReceipt, error) {
	command := kernel.KernelCommand{ContractManifest: kernel.ContractIdentity, CommandID: deterministicOperationalUUID("command", string(feature.ID), commandType, string(id), key), CommandType: commandType, CommandVersion: kernel.SchemaVersion, Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: id}, Authority: authority, ActorFQN: &actor, Execution: &execution, ExpectedRevision: kernel.NewExpectedRevision(revision), ExpectedLifecycleEpoch: expectedLifecycleEpoch(kernel.AggregateTask, revision, lifecycleEpoch), Preconditions: []kernel.AggregatePrecondition{}, ExpectedPolicyRevision: service.provenance.PolicyRevision, ExpectedCatalogueRevision: kernel.CatalogueRevision, IdempotencyKey: "feature:" + string(feature.ID) + ":" + key, CorrelationID: feature.ID, Causation: append([]kernel.DagParent(nil), parents...), Payload: append([]byte(nil), payload...), EvidenceRefs: []kernel.EvidenceRef{}}
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
