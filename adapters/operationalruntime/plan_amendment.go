package operationalruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/tekroo-ai/teams/adapters/nativeagent"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func planAmendmentAssessmentID(featureID kernel.UUIDv7) kernel.UUIDv7 {
	// Feature-wide identity, not per question: changing the question cannot
	// mint a second assessment or another model invocation.
	return deterministicOperationalUUID("feature-plan-amendment-assessment", string(featureID))
}

func (service *ProductionService) amendmentWitness(ctx context.Context, feature organization.FeatureRequest, blockedID, consultID kernel.UUIDv7) (organization.PlanAmendmentWitness, kernel.Snapshot, organization.PlanAmendmentFence, error) {
	w := organization.PlanAmendmentWitness{Tasks: make(map[kernel.UUIDv7]kernel.AggregateState), Now: service.clock.Now().UTC()}
	var snapshot kernel.Snapshot
	fence := organization.PlanAmendmentFence{}
	refs := make(map[kernel.AggregateRef]bool)
	appendGuard := func(ref kernel.AggregateRef, revision uint64) {
		if !refs[ref] {
			refs[ref] = true
			fence.Preconditions = append(fence.Preconditions, kernel.AggregatePrecondition{Aggregate: ref, Expected: kernel.NewExpectedRevision(revision)})
		}
	}
	tasks := feature.Plan.Tasks
	if feature.PlanAmendment != nil {
		tasks = feature.PlanAmendment.PriorPlan.Tasks
	}
	ids := make([]kernel.UUIDv7, 0, len(tasks)+2)
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	ids = append(ids, consultID)
	if feature.PlanAmendment != nil {
		if _, _, exists, err := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: feature.PlanAmendment.AssessmentID}); err != nil {
			return w, snapshot, fence, err
		} else if exists {
			ids = append(ids, feature.PlanAmendment.AssessmentID)
		}
	}
	for _, id := range ids {
		ref := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: id}
		state, _, exists, err := service.Store.ReadAggregateHead(ctx, ref)
		if err != nil || !exists {
			return w, snapshot, fence, errors.Join(organization.ErrPlanAmendment, err)
		}
		w.Tasks[id] = state
		appendGuard(ref, state.Revision)
		snapshot, err = service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: ref})
		if err != nil {
			return w, snapshot, fence, err
		}
	}
	w.Invocations, w.Evidence = make(map[kernel.AggregateRef]kernel.WorkInvocation), snapshot.Evidence
	for ref, invocation := range snapshot.WorkInvocations {
		if refs[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: invocation.TaskID}] || invocation.ID == blockedID {
			w.Invocations[ref] = invocation
		}
	}
	var found bool
	w.Budget, found = snapshot.WorkBudgetAccounts[kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: feature.BudgetAccountID}]
	if !found || !w.Budget.Valid() {
		return w, snapshot, fence, organization.ErrPlanAmendment
	}
	w.Consultation, found = latestTaskInvocation(snapshot.WorkInvocations, consultID)
	if !found || w.Consultation.OutputDigest == nil {
		return w, snapshot, fence, organization.ErrPlanAmendment
	}
	raw, err := service.Runtime.ReadExecutionOutput(ctx, *w.Consultation.OutputDigest)
	if err != nil || digestBytes(raw) != *w.Consultation.OutputDigest {
		return w, snapshot, fence, errors.Join(organization.ErrPlanAmendment, err)
	}
	_, resolved, err := consultationAnswer(raw)
	if err != nil {
		return w, snapshot, fence, err
	}
	w.ScopeChangeRequired = !resolved
	for ref, invocation := range snapshot.WorkInvocations {
		if refs[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: invocation.TaskID}] || invocation.ID == blockedID {
			appendGuard(ref, invocation.Revision)
		}
	}
	appendGuard(w.Budget.Ref(), w.Budget.Revision)
	fence.Budget = w.Budget
	sort.Slice(fence.Preconditions, func(i, j int) bool {
		return fmt.Sprint(fence.Preconditions[i].Aggregate) < fmt.Sprint(fence.Preconditions[j].Aggregate)
	})
	return w, snapshot, fence, nil
}

func (service *ProductionService) reservePlanAmendment(ctx context.Context, feature organization.FeatureRequest, blocked, consult kernel.WorkInvocation) (bool, error) {
	if service.planning.PlanAmendment == nil || !service.qualificationNative || service.newInvocationAdmissionBlocked() {
		return false, nil
	}
	if feature.PlanAmendment != nil {
		return false, nil
	} // one allowance, including rejection
	w, _, fence, err := service.amendmentWitness(ctx, feature, blocked.ID, consult.TaskID)
	if err != nil {
		return false, err
	}
	request := organization.PlanAmendmentReservation{AssessmentID: planAmendmentAssessmentID(feature.ID), BlockedTaskID: blocked.TaskID, BlockedTaskRevision: w.Tasks[blocked.TaskID].Revision, BlockedInvocationID: blocked.ID, ConsultationID: consult.TaskID, AnswerDigest: *consult.OutputDigest}
	next, err := organization.ReservePlanAmendment(feature, request, w)
	if err != nil {
		return false, err
	}
	source, err := service.amendmentPMSource(ctx, feature)
	if err != nil {
		return false, err
	}
	owner, err := service.ensurePlanningActorExcept(ctx, []kernel.ActorFQN{source.ActorFQN}, "")
	if err != nil {
		return false, err
	}
	route, err := service.retainedDecisionRoute(ctx, source)
	if err != nil {
		return false, err
	}
	// Check the generated requirement before reserving the feature-wide allowance.
	// An unavailable profile must not leave a durable ASSESSING reservation.
	if _, _, err := service.qualifiedDecisionOwner(owner, amendmentAssessmentTask(next, source, owner.ModelProfile, route), source); err != nil {
		return false, err
	}
	input, err := service.registerAmendmentInput(ctx, next, owner, w)
	if err != nil {
		return false, err
	}
	next.PlanAmendment.AssessmentInput = &input
	_, err = service.Store.CompareAndSwapPlanAmendment(ctx, next, feature.Revision, fence)
	return err == nil, err
}

func (service *ProductionService) amendmentPMSource(ctx context.Context, feature organization.FeatureRequest) (kernel.WorkInvocation, error) {
	// The accepted plan's producing PM turn, not an arbitrary recent PM turn.
	plan := feature.Plan
	if feature.PlanAmendment != nil {
		// Publication changes PreparedExecution to the assessment execution.
		// The retained context source remains the original plan's producer.
		plan = &feature.PlanAmendment.PriorPlan
	}
	if plan == nil {
		return kernel.WorkInvocation{}, nativeagent.ErrInvalidBinding
	}
	round, err := service.latestArchitecturePlanRound(ctx, feature)
	if err != nil {
		return kernel.WorkInvocation{}, err
	}
	taskID := featurePlanningTaskID(feature.ID, stagePlanFinalization, round, nil)
	state, _, exists, err := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: taskID})
	if err != nil || !exists || state.Phase != kernel.PhaseCompleted {
		return kernel.WorkInvocation{}, errors.Join(organization.ErrPlanAmendment, err)
	}
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: taskID}})
	if err != nil {
		return kernel.WorkInvocation{}, err
	}
	source, found := latestTaskInvocation(snapshot.WorkInvocations, taskID)
	if !found || source.State != kernel.InvocationSucceeded || source.OutputDigest == nil || source.ActorFQN != plan.PreparedBy || source.Execution != plan.PreparedExecution {
		return kernel.WorkInvocation{}, nativeagent.ErrInvalidBinding
	}
	return source, nil
}

func amendmentAssessmentTask(feature organization.FeatureRequest, source kernel.WorkInvocation, model kernel.Digest, route kernel.DecisionRoute) organization.PlannedTask {
	a := feature.PlanAmendment
	return organization.PlannedTask{ID: a.AssessmentID, StoryID: a.PriorPlan.Tasks[0].StoryID, Title: "Assess bounded execution-plan amendment", Description: amendmentAssessmentGuidance, AcceptanceCriteria: []string{"The proposal binds the original reservation, accepted specification, exact blocked invocation and architect answer, with narrow impact ownership, closed DAG handoffs and independent validation."}, DependsOn: []kernel.UUIDv7{source.TaskID, a.ConsultationID}, Owner: a.PriorPlan.PreparedBy, ModelProfile: model, Purpose: kernel.PurposeHandoff, DecisionRoute: route, Complexity: 4, Risk: organization.RiskModerate, AttemptLimit: 1, ReviewRoundLimit: 1}
}

// Reconciliation is durable and forward-only. Reserving freezes new plan work
// while the assessment/publication is outstanding; already active workers may
// finish and are re-read by the independent policy validator.
func (service *ProductionService) reconcilePlanAmendment(ctx context.Context, feature organization.FeatureRequest) (bool, error) {
	a := feature.PlanAmendment
	if a == nil {
		return false, nil
	}
	if a.State == organization.AmendmentApplied {
		return false, service.ensurePublishedAmendmentMessages(ctx, feature)
	}
	if service.planning.PlanAmendment == nil {
		return true, fmt.Errorf("amendment %s has no configured acceptance: %w", a.AssessmentID, organization.ErrPlanAmendment)
	}
	if a.State == organization.AmendmentRejected {
		return true, service.completeAmendmentAssessment(ctx, feature)
	} // persisted operator-visible decision, no repeated model call
	if a.State == organization.AmendmentApproved {
		if err := service.completeAmendmentAssessment(ctx, feature); err != nil {
			return true, err
		}
		return true, service.publishPlanAmendment(ctx, feature)
	}
	w, snapshot, fence, err := service.amendmentWitness(ctx, feature, a.BlockedInvocationID, a.ConsultationID)
	if err != nil {
		return true, err
	}
	if inv, exists := latestTaskInvocation(snapshot.WorkInvocations, a.AssessmentID); exists {
		if !inv.State.Terminal() {
			return true, nil
		}
		var raw, full []byte
		if inv.State == kernel.InvocationSucceeded && inv.OutputDigest != nil {
			full, err = service.Runtime.ReadExecutionOutput(ctx, *inv.OutputDigest)
			if err != nil || digestBytes(full) != *inv.OutputDigest {
				return true, errors.Join(organization.ErrPlanAmendment, err)
			}
			raw = bytes.TrimSpace(bytes.TrimPrefix(full, []byte(application.OrganizationalResultMarker)))
		} else {
			// Invalid schema deliberately records a rejection; never normalize a
			// failure into an accepted model proposal or retry it automatically.
			raw = mustJSON(map[string]any{"terminal_invocation_id": inv.ID, "state": inv.State})
		}
		if inv.TaskID != a.AssessmentID || inv.Purpose != kernel.PurposeHandoff || inv.ActorFQN != a.PriorPlan.PreparedBy || inv.BudgetAccountID != feature.BudgetAccountID || inv.AttemptOrdinal != 1 {
			return true, nativeagent.ErrInvalidBinding
		}
		w.PM, w.PMExecution = inv.ActorFQN, inv.Execution
		policy := service.planning.PlanAmendment.Policy
		next, err := organization.RecordPlanAmendmentDecision(feature, raw, w, policy, policy.Authority)
		if err != nil {
			return true, err
		}
		next.PlanAmendment.AssessmentInvocationID = inv.ID
		if inv.OutputDigest != nil {
			next.PlanAmendment.AssessmentInvocationID, next.PlanAmendment.AssessmentOutputDigest = inv.ID, *inv.OutputDigest
		}
		if inv.State != kernel.InvocationSucceeded {
			next.PlanAmendment.Decision.Code = "ASSESSMENT_FAILED"
		}
		_, err = service.Store.CompareAndSwapPlanAmendment(ctx, next, feature.Revision, fence)
		return true, err
	}
	if !w.Now.Before(a.DeadlineAt) {
		policy := service.planning.PlanAmendment.Policy
		next, err := organization.RecordPlanAmendmentDecision(feature, nil, w, policy, policy.Authority)
		if err != nil {
			return true, err
		}
		_, err = service.Store.CompareAndSwapPlanAmendment(ctx, next, feature.Revision, fence)
		return true, err
	}
	if service.newInvocationAdmissionBlocked() {
		return true, nil
	}
	source, err := service.amendmentPMSource(ctx, feature)
	if err != nil {
		return true, err
	}
	owner, err := service.ensurePlanningActorExcept(ctx, []kernel.ActorFQN{source.ActorFQN}, "")
	if err != nil {
		return true, err
	}
	if owner.Status != organization.RoleIdle || actorHasActiveInvocation(snapshot.WorkInvocations, owner.ActorFQN, owner.Execution) {
		return true, nil
	}
	route, err := service.retainedDecisionRoute(ctx, source)
	if err != nil {
		return true, err
	}
	task := amendmentAssessmentTask(feature, source, owner.ModelProfile, route)
	profile, workspace, err := service.qualifiedDecisionOwner(owner, task, source)
	if err != nil {
		return true, err
	}
	refs, err := evidenceForInvocations(snapshot, w.Invocations[kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: a.BlockedInvocationID}], w.Consultation, source)
	if err != nil {
		return true, err
	}
	if a.AssessmentInput == nil {
		return true, organization.ErrPlanAmendment
	}
	inputRef := *a.AssessmentInput
	if metadata, found := snapshot.Evidence[inputRef.EvidenceID]; !found || !metadata.Available || metadata.SHA256 != inputRef.SHA256 {
		return true, organization.ErrPlanAmendment
	}
	refs = append(refs, inputRef)
	ref := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}
	state, head, exists, err := service.Store.ReadAggregateHead(ctx, ref)
	if err != nil {
		return true, err
	}
	if !exists {
		payload := mustJSON(map[string]any{"story_id": task.StoryID, "title": task.Title, "description": task.Description, "acceptance_criteria": task.AcceptanceCriteria, "depends_on": task.DependsOn})
		_, err := service.submitPlannedCommand(ctx, feature, "tekroo.command.task.create", kernel.AggregateTask, task.ID, "amendment-assessment", payload, []kernel.DagParent{{ParentEventID: source.LastEventID, EdgeKind: kernel.EdgeCausal}, {ParentEventID: w.Consultation.LastEventID, EdgeKind: kernel.EdgeResponse}})
		if err != nil {
			return true, err
		}
		state, head, _, err = service.Store.ReadAggregateHead(ctx, ref)
		if err != nil {
			return true, err
		}
	}
	if err := service.registerExecution(ctx, owner, profile); err != nil {
		return true, err
	}
	wp := service.workProfile(feature, task, state.LifecycleEpoch, state.ScopeRevision, inputRef.EvidenceID, a.DeadlineAt)
	tracked := &trackedTask{plan: task, owner: owner, profile: wp, revision: state.Revision, last: head}
	loaded, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: ref})
	if err != nil {
		return true, err
	}
	if existing, exists := loaded.WorkProfiles[ref]; exists {
		tracked.profile = existing.Profile
	} else if err := service.bindConsultationWorkProfile(ctx, feature, tracked, refs); err != nil {
		return true, err
	}
	err = service.activateTask(ctx, feature, tracked, profile, workspace, w.Budget.Revision, []kernel.UUIDv7{source.LastEventID, w.Consultation.LastEventID}, refs, inputRef.EvidenceID, []kernel.Digest{a.PriorDigest, a.AnswerDigest, inputRef.SHA256})
	return true, err
}

// This completes only the read-only assessment receipt, not the proposal's
// engineering validity or the implementation. Policy approval is separate.
func (service *ProductionService) completeAmendmentAssessment(ctx context.Context, feature organization.FeatureRequest) error {
	a := feature.PlanAmendment
	if a.AssessmentInvocationID == "" {
		return nil
	}
	ref := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: a.AssessmentID}
	state, head, exists, err := service.Store.ReadAggregateHead(ctx, ref)
	if err != nil || !exists {
		return errors.Join(organization.ErrPlanAmendment, err)
	}
	if state.Phase == kernel.PhaseCompleted {
		return nil
	}
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: ref})
	if err != nil {
		return err
	}
	inv, exists := snapshot.WorkInvocations[kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: a.AssessmentInvocationID}]
	if !exists || !inv.State.Terminal() {
		return organization.ErrPlanAmendment
	}
	if inv.State != kernel.InvocationSucceeded {
		return nil
	} // durable rejection; never retry
	source, err := service.amendmentPMSource(ctx, feature)
	if err != nil {
		return err
	}
	bound, found := snapshot.WorkProfiles[ref]
	if !found || !bound.Valid() || bound.Profile.Binding() != inv.WorkProfile {
		return nativeagent.ErrInvalidBinding
	}
	// Completion uses this assessment's immutable classification, even if the
	// source policy or selected model has changed since admission.
	task := amendmentAssessmentTask(feature, source, inv.ModelProfileDigest, bound.Profile.MinimumDecisionRoute)
	return service.completeEvidenceTask(ctx, feature, task, state, head, inv, snapshot)
}

// Successors consume the actual decision trail, including the predecessor
// result and architect answer. Unchanged tasks retain their existing evidence.
func amendmentSuccessorEvidence(feature organization.FeatureRequest, task organization.PlannedTask, snapshot kernel.Snapshot, base []kernel.EvidenceRef) ([]kernel.EvidenceRef, error) {
	a := feature.PlanAmendment
	if a == nil || a.State != organization.AmendmentApplied {
		return base, nil
	}
	if _, retained := plannedTaskByID(a.PriorPlan, task.ID); retained {
		return base, nil
	}
	if a.AssessmentInput == nil {
		return nil, organization.ErrPlanAmendment
	}
	invocations := make([]kernel.WorkInvocation, 0, 3)
	for _, id := range []kernel.UUIDv7{a.BlockedInvocationID, a.ConsultationInvocationID, a.AssessmentInvocationID} {
		inv, exists := snapshot.WorkInvocations[kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: id}]
		if !exists || inv.State != kernel.InvocationSucceeded || inv.OutputDigest == nil {
			return nil, organization.ErrPlanAmendment
		}
		bound := false
		for _, evidenceID := range inv.TerminalEvidenceIDs {
			metadata := snapshot.Evidence[evidenceID]
			bound = bound || metadata.Available && metadata.SHA256 == *inv.OutputDigest
		}
		if !bound {
			return nil, organization.ErrPlanAmendment
		}
		invocations = append(invocations, inv)
	}
	extra, err := evidenceForInvocations(snapshot, invocations...)
	if err != nil {
		return nil, err
	}
	extra = append(extra, *a.AssessmentInput)
	result := append([]kernel.EvidenceRef(nil), base...)
	seen := make(map[kernel.UUIDv7]kernel.Digest, len(result))
	for _, ref := range result {
		seen[ref.EvidenceID] = ref.SHA256
	}
	for _, ref := range extra {
		metadata, exists := snapshot.Evidence[ref.EvidenceID]
		if !exists || !metadata.Available || metadata.SHA256 != ref.SHA256 {
			return nil, organization.ErrPlanAmendment
		}
		if prior, exists := seen[ref.EvidenceID]; exists {
			if prior != ref.SHA256 {
				return nil, organization.ErrPlanAmendment
			}
			continue
		}
		seen[ref.EvidenceID] = ref.SHA256
		result = append(result, ref)
	}
	return result, nil
}

func (service *ProductionService) registerAmendmentInput(ctx context.Context, feature organization.FeatureRequest, owner organization.RoleInstanceState, w organization.PlanAmendmentWitness) (kernel.EvidenceRef, error) {
	a := feature.PlanAmendment
	// Stable reservation input: do not replace it after restart or embed JSON
	// in a description string. Dynamic authoritative state is checked anew at
	// decision time. Evidence is a raw object accessible through the evidence tool.
	outputs := make(map[string]json.RawMessage)
	for name, inv := range map[string]kernel.WorkInvocation{"blocked_result": w.Invocations[kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: a.BlockedInvocationID}], "architect_answer": w.Consultation} {
		if inv.OutputDigest == nil {
			return kernel.EvidenceRef{}, organization.ErrPlanAmendment
		}
		full, err := service.Runtime.ReadExecutionOutput(ctx, *inv.OutputDigest)
		if err != nil || digestBytes(full) != *inv.OutputDigest {
			return kernel.EvidenceRef{}, errors.Join(organization.ErrPlanAmendment, err)
		}
		body := bytes.TrimSpace(bytes.TrimPrefix(full, []byte(application.OrganizationalResultMarker)))
		if !json.Valid(body) || !bytes.HasPrefix(body, []byte("{")) {
			return kernel.EvidenceRef{}, organization.ErrPlanAmendment
		}
		outputs[name] = append(json.RawMessage(nil), body...)
	}
	remaining := make(map[kernel.UUIDv7]map[string]uint64)
	for _, task := range a.PriorPlan.Tasks {
		var attempts, repairs uint64
		for _, invocation := range w.Invocations {
			if invocation.TaskID != task.ID {
				continue
			}
			if invocation.Purpose == task.Purpose && invocation.AttemptOrdinal > attempts {
				attempts = invocation.AttemptOrdinal
			}
			if invocation.Purpose == kernel.PurposeRepair && invocation.AttemptOrdinal > repairs {
				repairs = invocation.AttemptOrdinal
			}
		}
		ordinary, repair := uint64(0), uint64(0)
		if uint64(task.AttemptLimit) > attempts {
			ordinary = uint64(task.AttemptLimit) - attempts
		}
		if uint64(task.ReviewRoundLimit) > repairs {
			repair = uint64(task.ReviewRoundLimit) - repairs
		}
		remaining[task.ID] = map[string]uint64{"ordinary": ordinary, "repair": repair}
	}
	// The reservation can outlive a role restart. Current execution authority
	// comes from the new execution brief, never this historical input snapshot.
	raw := mustJSON(map[string]any{"schema_version": organization.PlanAmendmentContractVersion, "reservation": a, "specification": feature.Specification, "design": feature.Design, "pm_actor": owner.ActorFQN, "task_states": w.Tasks, "remaining_task_allowances": remaining, "source_outputs": outputs, "remaining_budget": w.Budget, "allowed_roots": service.planning.PlanAmendment.Policy.AllowedRoots})
	if len(raw) > 1<<20 {
		return kernel.EvidenceRef{}, organization.ErrPlanAmendment
	}
	digest := digestBytes(raw)
	id := deterministicOperationalUUID("plan-amendment-input", string(a.AssessmentID), string(digest))
	if err := service.retainPlanningEvidenceBlob(ctx, id, raw, digest); err != nil {
		return kernel.EvidenceRef{}, err
	}
	payload := mustJSON(map[string]any{"access_partition": feature.Input.WorkspaceID, "availability": "AVAILABLE", "byte_length": len(raw), "canonical_digest": digest, "computation": nil, "deletion_tombstone": nil, "evidence_kind": "DECISION_RECORD", "integrity_state": "DIGEST_VERIFIED", "locator": fmt.Sprintf("teams://feature/%s/amendment/input/%s", feature.ID, digest), "locator_immutable": true, "media_type": "application/json", "producing_component": "tekrood-plan-amendment", "producing_version": organization.PlanAmendmentContractVersion, "redacts": nil, "retention_policy": "feature-lifecycle", "sensitivity": "INTERNAL", "sha256": digest, "source_evidence_ids": []kernel.UUIDv7{}, "source_timestamp": a.RequestedAt, "transport_provenance": "teams-plan-amendment"})
	_, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.evidence.register", kernel.SchemaVersion, kernel.AggregateEvidence, id, service.serviceAuthority, 0, payload, nil, nil, "amendment-input-"+string(digest))
	return kernel.EvidenceRef{EvidenceID: id, SHA256: digest}, err
}

func (service *ProductionService) nativePlanAmendment(ctx context.Context, brief application.ExecutionBrief) (*nativeagent.ResultContract, *kernel.WorkInvocation, error) {
	features, err := service.nativeBriefFeatures(ctx, brief)
	if err != nil {
		return nil, nil, err
	}
	for _, feature := range features {
		if feature.PlanAmendment == nil || feature.PlanAmendment.AssessmentID != brief.Task.TaskID {
			continue
		}
		a := feature.PlanAmendment
		if brief.ActorFQN != a.PriorPlan.PreparedBy || brief.Purpose != kernel.PurposeHandoff ||
			a.State == organization.AmendmentAssessing && (service.planning.PlanAmendment == nil || feature.Status != organization.FeaturePlanned) ||
			a.State != organization.AmendmentAssessing && a.AssessmentInvocationID != brief.InvocationID {
			return nil, nil, nativeagent.ErrInvalidBinding
		}
		source, err := service.amendmentPMSource(ctx, feature)
		if err != nil {
			return nil, nil, err
		}
		schema, err := boundPlanAmendmentSchema(feature, brief)
		if err != nil {
			return nil, nil, err
		}
		return &nativeagent.ResultContract{TaskID: brief.Task.TaskID, TaskSourceDigest: brief.Task.SourceDigest, SchemaRef: "tekroo.feature.plan-amendment", SchemaSHA256: digestBytes(schema), Schema: schema}, &source, nil
	}
	return nil, nil, nil
}

// Session reconstruction includes terminal features so immutable journals stay
// inspectable. This is not an admission filter: reconciliation still admits
// amendments only against a PLANNED feature and its single-use reservation.
func nativePlanningInspectionStatuses() []organization.FeatureStatus {
	return []organization.FeatureStatus{organization.FeaturePlanned, organization.FeatureApproved, organization.FeatureAwaitingAcceptance, organization.FeatureAccepted, organization.FeatureCancelled}
}
