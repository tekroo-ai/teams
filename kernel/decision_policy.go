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

func validateCommandPolicy(command KernelCommand, snapshot Snapshot) (OutcomeCode, string) {
	object, err := decodePayloadObject(command.Payload)
	if err != nil {
		return OutcomeRejectedInvalid, reasonInvalidPayload
	}
	switch command.CommandType {
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
		validationIDs, ok := uuidArrayField(object, "validation_event_ids")
		if !ok || !acceptedEventSet(validationIDs, snapshot.AcceptedEvents) {
			return OutcomeRejectedPolicy, reasonValidationIncomplete
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
		if !payloadEvidenceMatches(object, command.EvidenceRefs) {
			return OutcomeRejectedInvalid, reasonInvalidEvidence
		}
		revision, ok := uint64Field(object, "acceptance_policy_revision")
		if !ok || (snapshot.Authorization.Requirements.AcceptancePolicyRevision > 0 && revision != snapshot.Authorization.Requirements.AcceptancePolicyRevision) {
			return OutcomeRejectedConflict, reasonCriteriaRevisionConflict
		}
		if snapshot.Authorization.Requirements.RequireQualifiedTree {
			if digest, ok := object["qualified_tree_digest"].(string); !ok || !Digest(digest).Valid() {
				return OutcomeRejectedPolicy, reasonAcceptanceGateFailed
			}
		}
	case "tekroo.command.completion-review.open":
		subject, ok := aggregateField(object, "subject_kind", "subject_id")
		epoch, epochOK := uint64Field(object, "lifecycle_epoch")
		if !ok || !epochOK {
			return OutcomeRejectedInvalid, reasonInvalidPayload
		}
		related, found := snapshot.Related[subject]
		if !found || related.State == nil || related.State.LifecycleEpoch != epoch || related.State.Phase != PhaseActive || !containsAggregatePrecondition(command.Preconditions, subject) {
			return OutcomeRejectedConflict, reasonStaleLifecycleEpoch
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
	case "tekroo.command.work.reopen":
		if !payloadEvidenceMatches(object, command.EvidenceRefs) {
			return OutcomeRejectedInvalid, reasonInvalidEvidence
		}
		prior, ok := uint64Field(object, "prior_epoch")
		if !ok || snapshot.State == nil || prior != snapshot.State.LifecycleEpoch {
			return OutcomeRejectedClosed, reasonStaleLifecycleEpoch
		}
	case "tekroo.command.work.create-successor":
		value, ok := object["successor_id"].(string)
		if !ok || !UUIDv7(value).Valid() || UUIDv7(value) == command.Target.ID || snapshot.State == nil || !terminalPhase(snapshot.State.Phase) {
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
	ids, ok := uuidArrayField(object, "evidence_ids")
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

func terminalPhase(phase Phase) bool {
	return phase == PhaseCompleted || phase == PhaseAccepted || phase == PhaseClosed
}
