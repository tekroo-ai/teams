package kernel

import (
	"encoding/json"
	"errors"
	"sort"
)

const (
	reasonCriteriaRevisionConflict = "CRITERIA_REVISION_CONFLICT"
	reasonDependencyNotReady       = "DEPENDENCY_NOT_READY"
	reasonValidationIncomplete     = "VALIDATION_INCOMPLETE"
	reasonUnresolvedExceptions     = "UNRESOLVED_EXCEPTIONS"
	reasonAcceptanceGateFailed     = "ACCEPTANCE_GATE_FAILED"
	reasonStaleLifecycleEpoch      = "STALE_LIFECYCLE_EPOCH"
	reasonInvalidSuccessor         = "INVALID_SUCCESSOR"
	reasonCorrectionTarget         = "CORRECTION_TARGET_NOT_FOUND"
	reasonReviewAlreadyOpen        = "REVIEW_ALREADY_OPEN"
	reasonReviewNotFinalized       = "REVIEW_NOT_FINALIZED"
	reasonEscalationAlreadyExists  = "ESCALATION_ALREADY_EXISTS"
	reasonEscalationNotFound       = "ESCALATION_NOT_FOUND"
	reasonReleaseAlreadyExists     = "RELEASE_ALREADY_EXISTS"
	reasonReleaseNotFound          = "RELEASE_NOT_FOUND"
	reasonReleaseGateFailed        = "RELEASE_GATE_FAILED"
)

type CompletionReviewKey struct {
	Subject           AggregateRef `json:"subject"`
	LifecycleEpoch    uint64       `json:"lifecycle_epoch"`
	CriteriaRevision  uint64       `json:"criteria_revision"`
	EvidenceSetDigest Digest       `json:"evidence_set_digest"`
}

func CompletionReviewKeyFromPayload(payload json.RawMessage) (CompletionReviewKey, error) {
	object, err := decodePayloadObject(payload)
	if err != nil {
		return CompletionReviewKey{}, err
	}
	subject, ok := aggregateField(object, "subject_kind", "subject_id")
	epoch, epochOK := uint64Field(object, "lifecycle_epoch")
	criteria, criteriaOK := uint64Field(object, "criteria_revision")
	digestText, digestOK := object["evidence_set_digest"].(string)
	digest := Digest(digestText)
	if !ok || !epochOK || !criteriaOK || !digestOK || !digest.Valid() {
		return CompletionReviewKey{}, errors.New("invalid completion review key")
	}
	return CompletionReviewKey{Subject: subject, LifecycleEpoch: epoch, CriteriaRevision: criteria, EvidenceSetDigest: digest}, nil
}

func validateCommandPolicy(command KernelCommand, snapshot Snapshot, context DecisionContext) (OutcomeCode, string) {
	object, err := decodePayloadObject(command.Payload)
	if err != nil {
		return OutcomeRejectedInvalid, reasonInvalidPayload
	}
	if payloadEpoch, present := object["lifecycle_epoch"]; present && commandRequiresLifecycleEpoch(command) {
		epoch, ok := uint64Field(object, "lifecycle_epoch")
		if !ok || command.ExpectedLifecycleEpoch == nil || epoch != *command.ExpectedLifecycleEpoch || payloadEpoch == nil {
			return OutcomeRejectedConflict, reasonStaleLifecycleEpoch
		}
	}
	if outcome, reason, handled := validateOperatorHumanContinuityCommand(command, snapshot, context); handled {
		return outcome, reason
	}
	// ExpectedPolicyRevision selects the authorization policy evaluated by the
	// envelope. Classification, selection, release, and escalation revisions in
	// payloads identify independent domain policies and must not be forced to
	// advance whenever an authority grant changes.
	switch command.CommandType {
	case "tekroo.command.task.bind-work-profile":
		if command.Authority.Kind != PrincipalPolicy && command.Authority.Kind != PrincipalHuman || !payloadEvidenceFieldMatches(object, "classification_evidence_ids", command.EvidenceRefs) || snapshot.State == nil {
			return OutcomeRejectedUnauthorized, reasonUnauthorized
		}
		profile, err := WorkRiskProfileFromPayload(command.Payload)
		current, found := snapshot.WorkProfiles[command.Target]
		var currentPointer *WorkProfileSnapshot
		if found {
			currentPointer = &current
		}
		if err != nil {
			return OutcomeRejectedInvalid, reasonInvalidPayload
		}
		if decision := PlanWorkProfileBinding(WorkProfileBindingInput{Task: *snapshot.State, Profile: profile, Current: currentPointer}); decision.Status != WorkProfileReady {
			return OutcomeRejectedConflict, decision.Reason
		}
	case "tekroo.command.task.authorize-qualified-assignment":
		authorization, err := QualifiedAssignmentAuthorizationFromPayload(command.Payload, context.EventID)
		profile, profileFound := snapshot.WorkProfiles[command.Target]
		currentExecution, executionFound := snapshot.CurrentExecutions[authorization.SelectedActorFQN]
		if err != nil || snapshot.State == nil || authorization.TaskID != command.Target.ID || authorization.ExpectedTaskRevision != snapshot.Revision || authorization.WorkProfile.TaskBindingMatches(*snapshot.State) == false || !profileFound || !profile.Valid() || profile.Profile.Binding() != authorization.WorkProfile || authorization.RequiredDecisionRoute != profile.Profile.MinimumDecisionRoute || !payloadEvidenceMatches(object, command.EvidenceRefs) {
			return OutcomeRejectedConflict, reasonRevisionConflict
		}
		if !executionFound || currentExecution != authorization.SelectedExecution() {
			return OutcomeRejectedStaleExecution, reasonStaleExecution
		}
	case "tekroo.command.task.dispatch":
		authorization, found := snapshot.QualifiedAssignments[command.Target]
		destination, destinationOK := object["destination"].(string)
		profile, profileFound := snapshot.WorkProfiles[command.Target]
		currentExecution, executionFound := snapshot.CurrentExecutions[authorization.SelectedActorFQN]
		if !found || !authorization.Valid() || snapshot.State == nil || authorization.ExpectedTaskRevision+1 != snapshot.Revision || !authorization.WorkProfile.TaskBindingMatches(*snapshot.State) || !profileFound || !profile.Valid() || profile.Profile.Binding() != authorization.WorkProfile || !destinationOK || destination != string(authorization.SelectedActorFQN) || !containsDagParent(command.Causation, authorization.AuthorizationEventID, EdgeCausal) {
			return OutcomeRejectedPolicy, reasonPolicy
		}
		if !executionFound || currentExecution != authorization.SelectedExecution() {
			return OutcomeRejectedStaleExecution, reasonStaleExecution
		}
	case "tekroo.command.variant-group.open":
		if command.Authority.Kind != PrincipalPolicy || !payloadEvidenceMatches(object, command.EvidenceRefs) {
			return OutcomeRejectedUnauthorized, reasonUnauthorized
		}
		group, err := VariantGroupFromOpenPayload(command.Payload, context.EventID)
		taskRef := AggregateRef{Kind: AggregateTask, ID: group.TaskID}
		task, taskFound := snapshot.Related[taskRef]
		profile, profileFound := snapshot.WorkProfiles[taskRef]
		if err != nil || group.VariantGroupID != command.Target.ID || !taskFound || task.State == nil || task.State.LifecycleEpoch != group.WorkProfile.LifecycleEpoch || task.State.ScopeRevision != group.WorkProfile.ScopeRevision || !profileFound || profile.Profile.Binding() != group.WorkProfile || !containsAggregatePrecondition(command.Preconditions, taskRef) {
			return OutcomeRejectedConflict, reasonRevisionConflict
		}
		if _, duplicate := snapshot.VariantGroupKeys[group.Key()]; duplicate {
			return OutcomeRejectedConflict, reasonRevisionConflict
		}
	case "tekroo.command.variant-group.submit-candidate", "tekroo.command.variant-group.record-comparison", "tekroo.command.variant-group.select":
		group, found := snapshot.VariantGroups[command.Target]
		if !found || group.Revision != snapshot.Revision || !payloadEvidenceMatches(object, command.EvidenceRefs) {
			return OutcomeRejectedConflict, reasonRevisionConflict
		}
		eventType := map[string]string{
			"tekroo.command.variant-group.submit-candidate":  "tekroo.event.variant-group.candidate-submitted",
			"tekroo.command.variant-group.record-comparison": "tekroo.event.variant-group.comparison-recorded",
			"tekroo.command.variant-group.select":            "tekroo.event.variant-group.selected",
		}[command.CommandType]
		if command.CommandType == "tekroo.command.variant-group.submit-candidate" {
			candidate, valid := variantCandidateFromPayload(command.Payload, context.EventID)
			if !valid || command.ActorFQN == nil || command.Execution == nil || candidate.ActorFQN != *command.ActorFQN || candidate.Execution != *command.Execution {
				return OutcomeRejectedUnauthorized, reasonUnauthorized
			}
		}
		if command.CommandType == "tekroo.command.variant-group.record-comparison" && command.Authority != group.Comparator {
			return OutcomeRejectedUnauthorized, reasonUnauthorized
		}
		if command.CommandType == "tekroo.command.variant-group.select" && command.Authority != group.Adjudicator {
			return OutcomeRejectedUnauthorized, reasonUnauthorized
		}
		if _, valid := ApplyVariantEvent(group, DomainEvent{EventID: context.EventID, EventType: eventType, Aggregate: command.Target, AggregateRevision: snapshot.Revision + 1, CommittedAt: context.DecidedAt, Payload: command.Payload}); !valid {
			return OutcomeRejectedPolicy, reasonPolicy
		}
	case "tekroo.command.story.approve-release":
		var value struct {
			StoryID               UUIDv7       `json:"story_id"`
			LifecycleEpoch        uint64       `json:"lifecycle_epoch"`
			ExpectedStoryRevision uint64       `json:"expected_story_revision"`
			Author                PrincipalRef `json:"author"`
			EvidenceIDs           []UUIDv7     `json:"evidence_ids"`
		}
		if json.Unmarshal(command.Payload, &value) != nil || value.StoryID != command.Target.ID || value.Author != command.Authority || snapshot.State == nil || snapshot.State.Phase != PhaseCompleted || snapshot.State.LifecycleEpoch != value.LifecycleEpoch || snapshot.Revision != value.ExpectedStoryRevision || !payloadEvidenceMatches(object, command.EvidenceRefs) {
			return OutcomeRejectedPolicy, reasonReleaseGateFailed
		}
	case "tekroo.command.release-plan.create":
		plan, err := ReleasePlanFromCreatePayload(command.Payload, context.EventID)
		if err != nil || command.Authority.Kind != PrincipalPolicy || plan.ReleasePlanID != command.Target.ID || !payloadEvidenceMatches(object, command.EvidenceRefs) {
			return OutcomeRejectedInvalid, reasonInvalidPayload
		}
		story, found := snapshot.Related[plan.Story]
		approval, approved := snapshot.AcceptedEvents[plan.AuthorApprovalEventID]
		if !found || !story.Exists || story.Revision != plan.ExpectedStoryRevision || story.State == nil || story.State.LifecycleEpoch != plan.StoryLifecycleEpoch || story.State.Phase != PhaseCompleted || !containsAggregatePrecondition(command.Preconditions, plan.Story) || !approved || approval.EventType != "tekroo.event.story.release-approved" || !containsDagParent(command.Causation, plan.AuthorApprovalEventID, EdgeResponse) {
			return OutcomeRejectedPolicy, reasonReleaseGateFailed
		}
		if _, exists := snapshot.ReleasePlanKeys[plan.Key()]; exists {
			return OutcomeRejectedConflict, reasonReleaseAlreadyExists
		}
	case "tekroo.command.release-plan.record-qualification",
		"tekroo.command.release-plan.request-execution",
		"tekroo.command.release-plan.record-result",
		"tekroo.command.release-plan.record-reconciliation",
		"tekroo.command.release-plan.finalize":
		plan, found := snapshot.ReleasePlans[command.Target]
		eventType, known := releaseEventTypeForCommand(command.CommandType)
		if !found || !known || plan.Revision != snapshot.Revision || !payloadEvidenceMatches(object, command.EvidenceRefs) {
			return OutcomeRejectedConflict, reasonReleaseNotFound
		}
		_, valid := ApplyReleaseEvent(plan, DomainEvent{EventID: context.EventID, EventType: eventType, Aggregate: command.Target, AggregateRevision: snapshot.Revision + 1, CommittedAt: context.DecidedAt, Payload: command.Payload})
		if !valid {
			return OutcomeRejectedPolicy, reasonReleaseGateFailed
		}
	case "tekroo.command.escalation.open":
		if command.Authority.Kind != PrincipalPolicy || !payloadEvidenceMatches(object, command.EvidenceRefs) {
			return OutcomeRejectedUnauthorized, reasonUnauthorized
		}
		escalation, err := EscalationFromOpenPayload(command.Payload)
		if err != nil || escalation.EscalationID != command.Target.ID || !context.DecidedAt.Before(escalation.DeadlineAt) || !causalPathMatchesCommand(escalation.CausalPathEventIDs, command.Causation) {
			return OutcomeRejectedInvalid, reasonInvalidPayload
		}
		related, found := snapshot.Related[escalation.Subject]
		if !found || !related.Exists || related.Revision != escalation.ExpectedSubjectRevision || related.State == nil || related.State.LifecycleEpoch != escalation.SubjectLifecycleEpoch || terminalPhase(related.State.Phase) || !containsAggregatePrecondition(command.Preconditions, escalation.Subject) {
			return OutcomeRejectedConflict, reasonStaleLifecycleEpoch
		}
		if _, exists := snapshot.EscalationKeys[escalation.Key()]; exists {
			return OutcomeRejectedConflict, reasonEscalationAlreadyExists
		}
	case "tekroo.command.escalation.resolve":
		if !payloadEvidenceMatches(object, command.EvidenceRefs) {
			return OutcomeRejectedInvalid, reasonInvalidEvidence
		}
		escalation, found := snapshot.Escalations[command.Target]
		transition, err := EscalationTransitionFromResolvePayload(command.Payload)
		if err != nil || !found || escalation.EscalationID != command.Target.ID || escalation.Revision != snapshot.Revision || transition.EscalationID != command.Target.ID || transition.ExpectedRevision != snapshot.Revision || transition.Subject != escalation.Subject || transition.SubjectLifecycleEpoch != escalation.SubjectLifecycleEpoch {
			return OutcomeRejectedConflict, reasonEscalationNotFound
		}
		related, relatedFound := snapshot.Related[escalation.Subject]
		if !relatedFound || !related.Exists || related.State == nil || related.State.LifecycleEpoch != escalation.SubjectLifecycleEpoch || terminalPhase(related.State.Phase) || !containsAggregatePrecondition(command.Preconditions, escalation.Subject) || !containsDagParent(command.Causation, escalation.OpeningEventID, EdgeResponse) {
			return OutcomeRejectedConflict, reasonStaleLifecycleEpoch
		}
		transition.Authority = command.Authority
		transition.DecidedAt = context.DecidedAt
		if result := EvaluateEscalation(escalation, transition); !result.Accepted {
			return OutcomeRejectedPolicy, result.Reason
		}
	case "tekroo.command.task.request-completion":
		if !payloadEvidenceMatches(object, command.EvidenceRefs) {
			return OutcomeRejectedInvalid, reasonInvalidEvidence
		}
		if revision, ok := uint64Field(object, "criteria_revision"); !ok || (snapshot.Authorization.Requirements.CompletionCriteriaRevision > 0 && revision != snapshot.Authorization.Requirements.CompletionCriteriaRevision) {
			return OutcomeRejectedConflict, reasonCriteriaRevisionConflict
		}
		if values, ok := stringArrayField(object, "unresolved_exceptions"); !ok || len(values) > 0 {
			return OutcomeRejectedPolicy, reasonUnresolvedExceptions
		}
		if !acceptedFinalizedReview(object, command.Target, snapshot) {
			return OutcomeRejectedPolicy, reasonReviewNotFinalized
		}
		if snapshot.State != nil && snapshot.State.Ownership.OwnerFQN != nil && command.Authority.Kind == PrincipalActor && (command.ActorFQN == nil || *command.ActorFQN != *snapshot.State.Ownership.OwnerFQN) {
			return OutcomeRejectedUnauthorized, reasonUnauthorized
		}
	case "tekroo.command.story.request-completion":
		if !payloadEvidenceMatches(object, command.EvidenceRefs) {
			return OutcomeRejectedInvalid, reasonInvalidEvidence
		}
		if revision, ok := uint64Field(object, "criteria_revision"); !ok || (snapshot.Authorization.Requirements.CompletionCriteriaRevision > 0 && revision != snapshot.Authorization.Requirements.CompletionCriteriaRevision) {
			return OutcomeRejectedConflict, reasonCriteriaRevisionConflict
		}
		if !acceptedFinalizedReview(object, command.Target, snapshot) {
			return OutcomeRejectedPolicy, reasonReviewNotFinalized
		}
		for _, precondition := range command.Preconditions {
			if precondition.Aggregate.Kind != AggregateTask {
				continue
			}
			related := snapshot.Related[precondition.Aggregate]
			if related.State == nil || related.State.Phase != PhaseCompleted {
				return OutcomeRejectedPolicy, reasonDependencyNotReady
			}
		}
	case "tekroo.command.story.request-acceptance":
		var value struct {
			ReleaseMode             ReleaseMode `json:"release_mode"`
			ReleasePlanID           UUIDv7      `json:"release_plan_id"`
			ReleasePlanRevision     uint64      `json:"release_plan_revision"`
			ReleaseFinalizedEventID UUIDv7      `json:"release_finalized_event_id"`
			QualifiedTreeDigest     string      `json:"qualified_tree_digest"`
		}
		if json.Unmarshal(command.Payload, &value) != nil || !payloadEvidenceMatches(object, command.EvidenceRefs) {
			return OutcomeRejectedInvalid, reasonInvalidPayload
		}
		planRef := AggregateRef{Kind: AggregateReleasePlan, ID: value.ReleasePlanID}
		plan, found := snapshot.ReleasePlans[planRef]
		if !found || plan.Story != command.Target || plan.Revision != value.ReleasePlanRevision || plan.FinalizationEventID != value.ReleaseFinalizedEventID || plan.Mode != value.ReleaseMode || !containsAggregatePrecondition(command.Preconditions, planRef) || !containsDagParent(command.Causation, value.ReleaseFinalizedEventID, EdgeResponse) {
			return OutcomeRejectedPolicy, reasonReleaseGateFailed
		}
		if value.ReleaseMode == ReleaseModeCode && (plan.State != ReleaseReadyForAcceptance || plan.ExpectedQualifiedTree != value.QualifiedTreeDigest || plan.ProviderTreeDigest != value.QualifiedTreeDigest) {
			return OutcomeRejectedPolicy, reasonReleaseGateFailed
		}
		if value.ReleaseMode == ReleaseModeNotRequired && plan.State != ReleaseNotRequired {
			return OutcomeRejectedPolicy, reasonReleaseGateFailed
		}
	case "tekroo.command.completion-review.open":
		if command.Authority.Kind != PrincipalPolicy {
			return OutcomeRejectedUnauthorized, reasonUnauthorized
		}
		subject, ok := aggregateField(object, "subject_kind", "subject_id")
		epoch, epochOK := uint64Field(object, "lifecycle_epoch")
		if !ok || !epochOK {
			return OutcomeRejectedInvalid, reasonInvalidPayload
		}
		related, found := snapshot.Related[subject]
		review, reviewErr := CompletionReviewFromPayload(command.Payload)
		if !found || related.State == nil || related.State.LifecycleEpoch != epoch || related.State.Phase != PhaseActive || !containsAggregatePrecondition(command.Preconditions, subject) || reviewErr != nil || review.Subject != subject || review.ScopeRevision != related.State.ScopeRevision {
			return OutcomeRejectedConflict, reasonStaleLifecycleEpoch
		}
		if subject.Kind == AggregateTask {
			profile, profileFound := snapshot.WorkProfiles[subject]
			if !profileFound || !profile.Valid() || profile.Profile.Binding() != review.WorkProfile {
				return OutcomeRejectedConflict, "STALE_WORK_PROFILE"
			}
		}
		if review.VariantGroupID != nil {
			groupRef := AggregateRef{Kind: AggregateVariantGroup, ID: *review.VariantGroupID}
			group, groupFound := snapshot.VariantGroups[groupRef]
			if !groupFound || group.State != VariantTerminal || group.Selection == nil || group.Selection.Outcome != VariantSelectCandidate || group.Selection.SelectedCandidateID == nil || !containsAggregatePrecondition(command.Preconditions, groupRef) || !containsDagParent(command.Causation, group.Selection.EventID, EdgeResponse) {
				return OutcomeRejectedPolicy, reasonValidationIncomplete
			}
			candidate, candidateFound := group.Candidates[*group.Selection.SelectedCandidateID]
			if !candidateFound || candidate.ArtifactDigest != review.CandidateArtifactDigest || group.WorkProfile != review.WorkProfile {
				return OutcomeRejectedPolicy, reasonValidationIncomplete
			}
		}
		key, err := CompletionReviewKeyFromPayload(command.Payload)
		if err != nil {
			return OutcomeRejectedInvalid, reasonInvalidPayload
		}
		if _, exists := snapshot.OpenReviews[key]; exists {
			return OutcomeRejectedConflict, reasonReviewAlreadyOpen
		}
	case "tekroo.command.completion-review.record-result":
		if !payloadEvidenceMatches(object, command.EvidenceRefs) {
			return OutcomeRejectedInvalid, reasonInvalidEvidence
		}
		reviewID, policyRevision, branchResult, resultErr := ReviewBranchResultFromPayload(command.Payload)
		review, found := snapshot.Reviews[command.Target]
		if resultErr != nil || reviewID != command.Target.ID || !found || review.BranchPolicyRevision != policyRevision {
			return OutcomeRejectedConflict, reasonCriteriaRevisionConflict
		}
		branchResult.Authority = command.Authority
		branchResult.EventID = context.EventID
		branchResult.DecidedAt = context.DecidedAt
		branchResult.ActorFQN = cloneActor(command.ActorFQN)
		branchResult.Execution = cloneExecution(command.Execution)
		if len(review.Branches) > 0 {
			if reason := BoundedReviewResultReason(review, branchResult); reason != "" {
				return OutcomeRejectedConflict, reason
			}
		} else if _, valid := ApplyReviewBranchResult(review, branchResult); !valid {
			return OutcomeRejectedConflict, reasonValidationIncomplete
		}
	case "tekroo.command.completion-review.finalize":
		if command.Authority.Kind != PrincipalPolicy || !payloadEvidenceMatches(object, command.EvidenceRefs) {
			return OutcomeRejectedUnauthorized, reasonUnauthorized
		}
		reviewID, ok := object["review_id"].(string)
		review, found := snapshot.Reviews[command.Target]
		if !ok || UUIDv7(reviewID) != command.Target.ID || !found {
			return OutcomeRejectedConflict, reasonValidationIncomplete
		}
		if _, valid := ApplyReviewFinalization(review, context.EventID, command.Payload); !valid {
			return OutcomeRejectedConflict, reasonValidationIncomplete
		}
	case "tekroo.command.work.reopen":
		if !payloadEvidenceMatches(object, command.EvidenceRefs) {
			return OutcomeRejectedInvalid, reasonInvalidEvidence
		}
		prior, ok := uint64Field(object, "prior_epoch")
		newScopeRevision, scopeOK := uint64Field(object, "new_scope_revision")
		if !ok || !scopeOK || snapshot.State == nil || prior != snapshot.State.LifecycleEpoch || newScopeRevision <= snapshot.State.ScopeRevision {
			return OutcomeRejectedClosed, reasonStaleLifecycleEpoch
		}
	case "tekroo.command.work.create-successor":
		values, ok := uuidArrayField(object, "successor_ids")
		if !ok || !CanonicalSuccessorIDs(values) || containsUUID(values, command.Target.ID) || snapshot.State == nil || !terminalPhase(snapshot.State.Phase) {
			return OutcomeRejectedPolicy, reasonInvalidSuccessor
		}
	case "tekroo.command.record.correct":
		if !payloadEvidenceMatches(object, command.EvidenceRefs) {
			return OutcomeRejectedInvalid, reasonInvalidEvidence
		}
		value, ok := object["target_event_id"].(string)
		accepted, found := snapshot.AcceptedEvents[UUIDv7(value)]
		if !ok || !UUIDv7(value).Valid() || !found || accepted.Quarantined {
			return OutcomeRejectedPolicy, reasonCorrectionTarget
		}
	}
	return OutcomeApplied, reasonApplied
}

func payloadEvidenceMatches(object map[string]any, references []EvidenceRef) bool {
	return payloadEvidenceFieldMatches(object, "evidence_ids", references)
}

func payloadEvidenceFieldMatches(object map[string]any, field string, references []EvidenceRef) bool {
	ids, ok := uuidArrayField(object, field)
	if !ok || len(ids) != len(references) {
		return false
	}
	want := make([]string, len(ids))
	got := make([]string, len(references))
	for index := range ids {
		want[index] = string(ids[index])
	}
	for index := range references {
		got[index] = string(references[index].EvidenceID)
	}
	sort.Strings(want)
	sort.Strings(got)
	for index := range want {
		if want[index] != got[index] {
			return false
		}
	}
	return true
}

func uint64Field(object map[string]any, name string) (uint64, bool) {
	number, ok := object[name].(json.Number)
	if !ok {
		return 0, false
	}
	value, err := number.Int64()
	return uint64(value), err == nil && value > 0
}

func nonnegativeUint64Field(object map[string]any, name string) (uint64, bool) {
	number, ok := object[name].(json.Number)
	if !ok {
		return 0, false
	}
	value, err := number.Int64()
	return uint64(value), err == nil && value >= 0
}

func stringArrayField(object map[string]any, name string) ([]string, bool) {
	values, ok := object[name].([]any)
	if !ok {
		return nil, false
	}
	result := make([]string, len(values))
	for index, value := range values {
		text, ok := value.(string)
		if !ok {
			return nil, false
		}
		result[index] = text
	}
	return result, true
}

func uuidArrayField(object map[string]any, name string) ([]UUIDv7, bool) {
	values, ok := stringArrayField(object, name)
	if !ok {
		return nil, false
	}
	result := make([]UUIDv7, len(values))
	for index, value := range values {
		result[index] = UUIDv7(value)
		if !result[index].Valid() {
			return nil, false
		}
	}
	return result, true
}

func acceptedEventSet(ids []UUIDv7, accepted map[UUIDv7]AcceptedEvent) bool {
	for _, id := range ids {
		event, found := accepted[id]
		if !found || event.Quarantined || event.EventType == "" {
			return false
		}
	}
	return true
}

func acceptedValidationSet(ids []UUIDv7, accepted map[UUIDv7]AcceptedEvent) bool {
	for _, id := range ids {
		event, found := accepted[id]
		if !found || event.Quarantined || event.EventType != "tekroo.event.completion-review.result-recorded" || event.Qualification != "PASS" {
			return false
		}
	}
	return true
}

func acceptedFinalizedReview(object map[string]any, subject AggregateRef, snapshot Snapshot) bool {
	reviewText, reviewOK := object["completion_review_id"].(string)
	reviewRevision, revisionOK := uint64Field(object, "completion_review_revision")
	branchRevision, branchOK := uint64Field(object, "branch_policy_revision")
	finalizedText, finalizedOK := object["validation_finalized_event_id"].(string)
	reviewRef := AggregateRef{Kind: AggregateCompletionReview, ID: UUIDv7(reviewText)}
	finalizedID := UUIDv7(finalizedText)
	review, found := snapshot.Reviews[reviewRef]
	event, eventFound := snapshot.AcceptedEvents[finalizedID]
	criteria, criteriaOK := uint64Field(object, "criteria_revision")
	epoch, epochOK := uint64Field(object, "lifecycle_epoch")
	return reviewOK && revisionOK && branchOK && finalizedOK && criteriaOK && epochOK && reviewRef.Valid() && finalizedID.Valid() && found && review.Subject == subject && review.LifecycleEpoch == epoch && review.CriteriaRevision == criteria && review.BranchPolicyRevision == branchRevision && review.ReviewRevision == reviewRevision && review.Finalization != nil && review.Finalization.EventID == finalizedID && review.Finalization.ReviewRevision == reviewRevision && review.Finalization.TerminalStatus == "PASS" && eventFound && !event.Quarantined && event.EventType == "tekroo.event.completion-review.finalized"
}

func aggregateField(object map[string]any, kindName, idName string) (AggregateRef, bool) {
	kind, kindOK := object[kindName].(string)
	id, idOK := object[idName].(string)
	value := AggregateRef{Kind: AggregateKind(kind), ID: UUIDv7(id)}
	return value, kindOK && idOK && value.Valid()
}

func containsAggregatePrecondition(values []AggregatePrecondition, target AggregateRef) bool {
	for _, value := range values {
		if value.Aggregate == target {
			return true
		}
	}
	return false
}

func causalPathMatchesCommand(path []UUIDv7, parents []DagParent) bool {
	if len(path) == 0 || len(path) != len(parents) {
		return false
	}
	for index, eventID := range path {
		if parents[index].ParentEventID != eventID || parents[index].EdgeKind != EdgeCausal {
			return false
		}
	}
	return true
}

func containsDagParent(values []DagParent, eventID UUIDv7, edge EdgeKind) bool {
	for _, value := range values {
		if value.ParentEventID == eventID && value.EdgeKind == edge {
			return true
		}
	}
	return false
}

func terminalPhase(phase Phase) bool {
	return phase == PhaseCompleted || phase == PhaseAccepted || phase == PhaseClosed
}
