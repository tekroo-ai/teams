package kernel

import (
	"encoding/json"
	"errors"
	"sort"
	"time"
)

type HumanInteractionPhase string

const (
	HumanInteractionOpen       HumanInteractionPhase = "OPEN"
	HumanInteractionCollecting HumanInteractionPhase = "COLLECTING"
	HumanInteractionSatisfied  HumanInteractionPhase = "SATISFIED"
	HumanInteractionClosed     HumanInteractionPhase = "CLOSED"
	HumanInteractionExpired    HumanInteractionPhase = "EXPIRED"
)

type HumanResponsePolicyKind string

const (
	HumanResponseExactOne HumanResponsePolicyKind = "EXACT_ONE"
	HumanResponseAnyOne   HumanResponsePolicyKind = "ANY_ONE"
	HumanResponseAll      HumanResponsePolicyKind = "ALL"
	HumanResponseQuorum   HumanResponsePolicyKind = "QUORUM"
)

type HumanResponsePolicy struct {
	Kind   HumanResponsePolicyKind `json:"kind" bson:"kind"`
	Quorum uint64                  `json:"quorum,omitempty" bson:"quorum,omitempty"`
}

func (policy HumanResponsePolicy) Valid(selected int) bool {
	switch policy.Kind {
	case HumanResponseExactOne:
		return selected == 1 && policy.Quorum == 0
	case HumanResponseAnyOne, HumanResponseAll:
		return selected > 0 && policy.Quorum == 0
	case HumanResponseQuorum:
		return policy.Quorum > 0 && policy.Quorum <= uint64(selected)
	default:
		return false
	}
}

type HumanInteractionRecipient struct {
	Principal                  PrincipalRef `json:"principal" bson:"principal"`
	ParticipantProfileID       UUIDv7       `json:"participant_profile_id" bson:"participant_profile_id"`
	ParticipantProfileRevision uint64       `json:"participant_profile_revision" bson:"participant_profile_revision"`
	ParticipantProfileDigest   Digest       `json:"participant_profile_digest" bson:"participant_profile_digest"`
	RoleBindingID              UUIDv7       `json:"role_binding_id" bson:"role_binding_id"`
	DeliveryBindingID          UUIDv7       `json:"delivery_binding_id" bson:"delivery_binding_id"`
}

type HumanDeliveryRecord struct {
	DeliveryID        UUIDv7       `json:"delivery_id" bson:"delivery_id"`
	EventID           UUIDv7       `json:"event_id" bson:"event_id"`
	Recipient         PrincipalRef `json:"recipient" bson:"recipient"`
	DeliveryBindingID UUIDv7       `json:"delivery_binding_id" bson:"delivery_binding_id"`
	Outcome           string       `json:"outcome" bson:"outcome"`
}

type HumanResponseRecord struct {
	EventID                     UUIDv7       `json:"event_id" bson:"event_id"`
	Respondent                  PrincipalRef `json:"respondent" bson:"respondent"`
	ParticipantProfileID        UUIDv7       `json:"participant_profile_id" bson:"participant_profile_id"`
	ParticipantProfileRevision  uint64       `json:"participant_profile_revision" bson:"participant_profile_revision"`
	RoleBindingID               UUIDv7       `json:"role_binding_id" bson:"role_binding_id"`
	AuthenticationBindingID     UUIDv7       `json:"authentication_binding_id" bson:"authentication_binding_id"`
	DeliveryID                  UUIDv7       `json:"delivery_id" bson:"delivery_id"`
	ResponseArtifactDigest      Digest       `json:"response_artifact_digest" bson:"response_artifact_digest"`
	ResponseSpecificationDigest Digest       `json:"response_specification_digest" bson:"response_specification_digest"`
}

type HumanInteractionSnapshot struct {
	InteractionID               UUIDv7                      `json:"interaction_id" bson:"interaction_id"`
	Revision                    uint64                      `json:"revision" bson:"revision"`
	Phase                       HumanInteractionPhase       `json:"phase" bson:"phase"`
	Subject                     AggregateRef                `json:"subject" bson:"subject"`
	SubjectLifecycleEpoch       uint64                      `json:"subject_lifecycle_epoch" bson:"subject_lifecycle_epoch"`
	ExpectedSubjectRevision     uint64                      `json:"expected_subject_revision" bson:"expected_subject_revision"`
	QuestionRevision            uint64                      `json:"question_revision" bson:"question_revision"`
	CanonicalQuestionDigest     Digest                      `json:"canonical_question_digest" bson:"canonical_question_digest"`
	ResponseSpecificationDigest Digest                      `json:"response_specification_digest" bson:"response_specification_digest"`
	OriginPrincipal             PrincipalRef                `json:"origin_principal" bson:"origin_principal"`
	OriginActorFQN              *ActorFQN                   `json:"origin_actor_fqn,omitempty" bson:"origin_actor_fqn,omitempty"`
	OriginExecution             *ExecutionTuple             `json:"origin_execution,omitempty" bson:"origin_execution,omitempty"`
	Recipients                  []HumanInteractionRecipient `json:"recipients" bson:"recipients"`
	ResponsePolicy              HumanResponsePolicy         `json:"response_policy" bson:"response_policy"`
	Purpose                     string                      `json:"purpose" bson:"purpose"`
	DeclaredEffect              string                      `json:"declared_effect" bson:"declared_effect"`
	Confidentiality             Confidentiality             `json:"confidentiality" bson:"confidentiality"`
	DeadlineAt                  time.Time                   `json:"deadline_at" bson:"deadline_at"`
	TimeoutPolicy               PrincipalRef                `json:"timeout_policy" bson:"timeout_policy"`
	DisclosureScopeDigest       Digest                      `json:"disclosure_scope_digest" bson:"disclosure_scope_digest"`
	InteractionPolicyRevision   uint64                      `json:"interaction_policy_revision" bson:"interaction_policy_revision"`
	InteractionPolicyDigest     Digest                      `json:"interaction_policy_digest" bson:"interaction_policy_digest"`
	PredecessorInteractionID    *UUIDv7                     `json:"predecessor_interaction_id,omitempty" bson:"predecessor_interaction_id,omitempty"`
	Deliveries                  []HumanDeliveryRecord       `json:"deliveries" bson:"deliveries"`
	Responses                   []HumanResponseRecord       `json:"responses" bson:"responses"`
}

func (snapshot HumanInteractionSnapshot) Clone() HumanInteractionSnapshot {
	copy := snapshot
	copy.Recipients = append([]HumanInteractionRecipient(nil), snapshot.Recipients...)
	copy.Deliveries = append([]HumanDeliveryRecord(nil), snapshot.Deliveries...)
	copy.Responses = append([]HumanResponseRecord(nil), snapshot.Responses...)
	if snapshot.OriginActorFQN != nil {
		value := *snapshot.OriginActorFQN
		copy.OriginActorFQN = &value
	}
	if snapshot.OriginExecution != nil {
		value := *snapshot.OriginExecution
		copy.OriginExecution = &value
	}
	if snapshot.PredecessorInteractionID != nil {
		value := *snapshot.PredecessorInteractionID
		copy.PredecessorInteractionID = &value
	}
	return copy
}

func HumanInteractionFromOpenPayload(payload json.RawMessage, revision uint64) (HumanInteractionSnapshot, error) {
	var value struct {
		InteractionID               UUIDv7                      `json:"interaction_id"`
		SubjectKind                 AggregateKind               `json:"subject_kind"`
		SubjectID                   UUIDv7                      `json:"subject_id"`
		SubjectLifecycleEpoch       uint64                      `json:"subject_lifecycle_epoch"`
		ExpectedSubjectRevision     uint64                      `json:"expected_subject_revision"`
		QuestionRevision            uint64                      `json:"question_revision"`
		CanonicalQuestionDigest     Digest                      `json:"canonical_question_digest"`
		ResponseSpecificationDigest Digest                      `json:"response_specification_digest"`
		OriginPrincipal             PrincipalRef                `json:"origin_principal"`
		OriginActorFQN              *ActorFQN                   `json:"origin_actor_fqn"`
		OriginExecutionID           *UUIDv7                     `json:"origin_execution_id"`
		OriginExecutionFencingEpoch *uint64                     `json:"origin_execution_fencing_epoch"`
		Recipients                  []HumanInteractionRecipient `json:"recipients"`
		ResponsePolicy              HumanResponsePolicy         `json:"response_policy"`
		Purpose                     string                      `json:"purpose"`
		DeclaredEffect              string                      `json:"declared_effect"`
		Confidentiality             Confidentiality             `json:"confidentiality"`
		DeadlineAt                  time.Time                   `json:"deadline_at"`
		TimeoutPolicy               PrincipalRef                `json:"timeout_policy"`
		DisclosureScopeDigest       Digest                      `json:"disclosure_scope_digest"`
		InteractionPolicyRevision   uint64                      `json:"interaction_policy_revision"`
		InteractionPolicyDigest     Digest                      `json:"interaction_policy_digest"`
		PredecessorInteractionID    *UUIDv7                     `json:"predecessor_interaction_id"`
	}
	if json.Unmarshal(payload, &value) != nil {
		return HumanInteractionSnapshot{}, errors.New("invalid human interaction")
	}
	var execution *ExecutionTuple
	if value.OriginExecutionID != nil && value.OriginExecutionFencingEpoch != nil {
		execution = &ExecutionTuple{ExecutionID: *value.OriginExecutionID, FencingEpoch: *value.OriginExecutionFencingEpoch}
	} else if value.OriginExecutionID != nil || value.OriginExecutionFencingEpoch != nil {
		return HumanInteractionSnapshot{}, errors.New("partial origin execution")
	}
	snapshot := HumanInteractionSnapshot{InteractionID: value.InteractionID, Revision: revision, Phase: HumanInteractionOpen, Subject: AggregateRef{Kind: value.SubjectKind, ID: value.SubjectID}, SubjectLifecycleEpoch: value.SubjectLifecycleEpoch, ExpectedSubjectRevision: value.ExpectedSubjectRevision, QuestionRevision: value.QuestionRevision, CanonicalQuestionDigest: value.CanonicalQuestionDigest, ResponseSpecificationDigest: value.ResponseSpecificationDigest, OriginPrincipal: value.OriginPrincipal, OriginActorFQN: value.OriginActorFQN, OriginExecution: execution, Recipients: value.Recipients, ResponsePolicy: value.ResponsePolicy, Purpose: value.Purpose, DeclaredEffect: value.DeclaredEffect, Confidentiality: value.Confidentiality, DeadlineAt: value.DeadlineAt, TimeoutPolicy: value.TimeoutPolicy, DisclosureScopeDigest: value.DisclosureScopeDigest, InteractionPolicyRevision: value.InteractionPolicyRevision, InteractionPolicyDigest: value.InteractionPolicyDigest, PredecessorInteractionID: value.PredecessorInteractionID}
	if !snapshot.Valid() {
		return HumanInteractionSnapshot{}, errors.New("invalid human interaction")
	}
	return snapshot, nil
}

func (snapshot HumanInteractionSnapshot) Valid() bool {
	if !snapshot.InteractionID.Valid() || snapshot.Revision == 0 || !snapshot.Subject.Valid() || snapshot.SubjectLifecycleEpoch == 0 || snapshot.ExpectedSubjectRevision == 0 || snapshot.QuestionRevision == 0 || !snapshot.CanonicalQuestionDigest.Valid() || !snapshot.ResponseSpecificationDigest.Valid() || !snapshot.OriginPrincipal.Valid() || !snapshot.ResponsePolicy.Valid(len(snapshot.Recipients)) || !validHumanInteractionPurpose(snapshot.Purpose) || !validHumanInteractionEffect(snapshot.DeclaredEffect) || !snapshot.Confidentiality.Valid() || snapshot.DeadlineAt.IsZero() || snapshot.TimeoutPolicy.Kind != PrincipalPolicy || !snapshot.TimeoutPolicy.Valid() || !snapshot.DisclosureScopeDigest.Valid() || snapshot.InteractionPolicyRevision == 0 || !snapshot.InteractionPolicyDigest.Valid() || snapshot.PredecessorInteractionID != nil && (!snapshot.PredecessorInteractionID.Valid() || *snapshot.PredecessorInteractionID == snapshot.InteractionID) {
		return false
	}
	if snapshot.OriginPrincipal.Kind == PrincipalHuman && (snapshot.OriginActorFQN != nil || snapshot.OriginExecution != nil) {
		return false
	}
	if snapshot.OriginPrincipal.Kind == PrincipalActor && (snapshot.OriginActorFQN == nil || snapshot.OriginExecution == nil || snapshot.OriginPrincipal.ID != string(*snapshot.OriginActorFQN)) {
		return false
	}
	seen := make(map[string]bool, len(snapshot.Recipients))
	for _, recipient := range snapshot.Recipients {
		if recipient.Principal.Kind != PrincipalHuman || !recipient.Principal.Valid() || !recipient.ParticipantProfileID.Valid() || recipient.ParticipantProfileRevision == 0 || !recipient.ParticipantProfileDigest.Valid() || !recipient.RoleBindingID.Valid() || !recipient.DeliveryBindingID.Valid() || seen[recipient.Principal.ID] {
			return false
		}
		seen[recipient.Principal.ID] = true
	}
	deliveryIDs := make(map[UUIDv7]bool, len(snapshot.Deliveries))
	deliveryEvents := make(map[UUIDv7]bool, len(snapshot.Deliveries))
	for _, delivery := range snapshot.Deliveries {
		recipient, selected := snapshot.Selected(delivery.Recipient)
		if !delivery.DeliveryID.Valid() || !delivery.EventID.Valid() || !selected || recipient.DeliveryBindingID != delivery.DeliveryBindingID || delivery.Outcome != "DELIVERED" && delivery.Outcome != "FAILED" && delivery.Outcome != "UNKNOWN" || deliveryIDs[delivery.DeliveryID] || deliveryEvents[delivery.EventID] {
			return false
		}
		deliveryIDs[delivery.DeliveryID] = true
		deliveryEvents[delivery.EventID] = true
	}
	responseEvents := make(map[UUIDv7]bool, len(snapshot.Responses))
	responseHumans := make(map[string]bool, len(snapshot.Responses))
	responseDigests := make(map[Digest]bool, len(snapshot.Responses))
	for _, response := range snapshot.Responses {
		recipient, selected := snapshot.Selected(response.Respondent)
		if !response.EventID.Valid() || !selected || recipient.ParticipantProfileID != response.ParticipantProfileID || recipient.ParticipantProfileRevision != response.ParticipantProfileRevision || recipient.RoleBindingID != response.RoleBindingID || !response.AuthenticationBindingID.Valid() || !response.DeliveryID.Valid() || !response.ResponseArtifactDigest.Valid() || response.ResponseSpecificationDigest != snapshot.ResponseSpecificationDigest || responseEvents[response.EventID] || responseHumans[response.Respondent.ID] || responseDigests[response.ResponseArtifactDigest] {
			return false
		}
		delivered := false
		for _, delivery := range snapshot.Deliveries {
			delivered = delivered || delivery.DeliveryID == response.DeliveryID && delivery.Recipient == response.Respondent && delivery.Outcome == "DELIVERED"
		}
		if !delivered {
			return false
		}
		responseEvents[response.EventID] = true
		responseHumans[response.Respondent.ID] = true
		responseDigests[response.ResponseArtifactDigest] = true
	}
	switch snapshot.Phase {
	case HumanInteractionOpen:
		return len(snapshot.Deliveries) == 0 && len(snapshot.Responses) == 0
	case HumanInteractionCollecting:
		return !snapshot.ResponsePolicySatisfied()
	case HumanInteractionSatisfied, HumanInteractionClosed:
		return snapshot.ResponsePolicySatisfied()
	case HumanInteractionExpired:
		return true
	default:
		return false
	}
}

func validHumanInteractionPurpose(value string) bool {
	switch value {
	case "ADVISORY_CONSULTATION", "AUTHORIZED_DECISION", "EVIDENTIARY_QUESTION", "REQUIREMENTS_CLARIFICATION", "ACCEPTANCE_FEEDBACK":
		return true
	default:
		return false
	}
}

func validHumanInteractionEffect(value string) bool {
	return value == "ADVISORY_ONLY" || value == "AUTHORITY_IF_AUTHORIZED" || value == "EVIDENCE_ONLY"
}

func (snapshot HumanInteractionSnapshot) Selected(principal PrincipalRef) (HumanInteractionRecipient, bool) {
	if principal.Kind != PrincipalHuman {
		return HumanInteractionRecipient{}, false
	}
	for _, recipient := range snapshot.Recipients {
		if recipient.Principal == principal {
			return recipient, true
		}
	}
	return HumanInteractionRecipient{}, false
}

func (snapshot HumanInteractionSnapshot) ResponsePolicySatisfied() bool {
	accepted := make(map[string]bool, len(snapshot.Responses))
	for _, response := range snapshot.Responses {
		accepted[response.Respondent.ID] = true
	}
	switch snapshot.ResponsePolicy.Kind {
	case HumanResponseExactOne:
		return len(snapshot.Recipients) == 1 && len(accepted) == 1
	case HumanResponseAnyOne:
		return len(accepted) >= 1
	case HumanResponseAll:
		return len(accepted) == len(snapshot.Recipients)
	case HumanResponseQuorum:
		return uint64(len(accepted)) >= snapshot.ResponsePolicy.Quorum
	default:
		return false
	}
}

func ApplyHumanInteractionEvent(snapshot HumanInteractionSnapshot, event DomainEvent) (HumanInteractionSnapshot, bool) {
	if !snapshot.Valid() || event.Aggregate.Kind != AggregateHumanInteraction || event.Aggregate.ID != snapshot.InteractionID || event.AggregateRevision != snapshot.Revision+1 {
		return snapshot, false
	}
	next := snapshot.Clone()
	switch event.EventType {
	case "tekroo.event.human-interaction.delivery-recorded":
		var value struct {
			InteractionID                 UUIDv7       `json:"interaction_id"`
			ExpectedInteractionRevision   uint64       `json:"expected_interaction_revision"`
			QuestionRevision              uint64       `json:"question_revision"`
			DeliveryID                    UUIDv7       `json:"delivery_id"`
			Recipient                     PrincipalRef `json:"recipient"`
			DeliveryBindingID             UUIDv7       `json:"delivery_binding_id"`
			AdapterProfileDigest          Digest       `json:"adapter_profile_digest"`
			CanonicalQuestionDigest       Digest       `json:"canonical_question_digest"`
			RenderedQuestionDigest        Digest       `json:"rendered_question_digest"`
			PresenterActorFQN             *ActorFQN    `json:"presenter_actor_fqn"`
			PresenterExecutionID          *UUIDv7      `json:"presenter_execution_id"`
			MaterialEquivalenceEvidenceID *UUIDv7      `json:"material_equivalence_evidence_id"`
			Outcome                       string       `json:"outcome"`
			AttemptedAt                   time.Time    `json:"attempted_at"`
			ChannelProvenanceDigest       Digest       `json:"channel_provenance_digest"`
		}
		if json.Unmarshal(event.Payload, &value) != nil || snapshot.Phase == HumanInteractionClosed || snapshot.Phase == HumanInteractionExpired || value.InteractionID != snapshot.InteractionID || value.ExpectedInteractionRevision != snapshot.Revision || value.QuestionRevision != snapshot.QuestionRevision || value.CanonicalQuestionDigest != snapshot.CanonicalQuestionDigest || !value.DeliveryID.Valid() || !value.AdapterProfileDigest.Valid() || !value.RenderedQuestionDigest.Valid() || !value.ChannelProvenanceDigest.Valid() || value.AttemptedAt.IsZero() || value.Outcome != "DELIVERED" && value.Outcome != "FAILED" && value.Outcome != "UNKNOWN" || value.PresenterActorFQN == nil != (value.PresenterExecutionID == nil) || value.PresenterActorFQN != nil && (!value.PresenterActorFQN.Valid() || !value.PresenterExecutionID.Valid()) || value.MaterialEquivalenceEvidenceID != nil && !value.MaterialEquivalenceEvidenceID.Valid() {
			return snapshot, false
		}
		recipient, selected := snapshot.Selected(value.Recipient)
		if !selected || recipient.DeliveryBindingID != value.DeliveryBindingID {
			return snapshot, false
		}
		if value.RenderedQuestionDigest != value.CanonicalQuestionDigest && value.MaterialEquivalenceEvidenceID == nil {
			return snapshot, false
		}
		for _, delivery := range snapshot.Deliveries {
			if delivery.DeliveryID == value.DeliveryID {
				return snapshot, false
			}
		}
		next.Deliveries = append(next.Deliveries, HumanDeliveryRecord{DeliveryID: value.DeliveryID, EventID: event.EventID, Recipient: value.Recipient, DeliveryBindingID: value.DeliveryBindingID, Outcome: value.Outcome})
		if next.Phase == HumanInteractionOpen {
			next.Phase = HumanInteractionCollecting
		}
	case "tekroo.event.human-interaction.response-recorded":
		response, ok := humanResponseFromPayload(event.Payload, event.EventID)
		if !ok || snapshot.Phase == HumanInteractionClosed || snapshot.Phase == HumanInteractionExpired || response.ExpectedInteractionRevision != snapshot.Revision || response.QuestionRevision != snapshot.QuestionRevision || response.InteractionID != snapshot.InteractionID || response.Record.ResponseSpecificationDigest != snapshot.ResponseSpecificationDigest {
			return snapshot, false
		}
		recipient, selected := snapshot.Selected(response.Record.Respondent)
		if !selected || recipient.ParticipantProfileID != response.Record.ParticipantProfileID || recipient.ParticipantProfileRevision != response.Record.ParticipantProfileRevision || recipient.RoleBindingID != response.Record.RoleBindingID {
			return snapshot, false
		}
		deliveryFound := false
		for _, delivery := range snapshot.Deliveries {
			if delivery.DeliveryID == response.Record.DeliveryID && delivery.EventID == response.DeliveryEventID && delivery.Recipient == response.Record.Respondent && delivery.Outcome == "DELIVERED" {
				deliveryFound = true
			}
		}
		if !deliveryFound {
			return snapshot, false
		}
		for _, existing := range snapshot.Responses {
			if existing.Respondent == response.Record.Respondent || existing.ResponseArtifactDigest == response.Record.ResponseArtifactDigest {
				return snapshot, false
			}
		}
		next.Responses = append(next.Responses, response.Record)
		sort.Slice(next.Responses, func(i, j int) bool { return next.Responses[i].Respondent.ID < next.Responses[j].Respondent.ID })
		if next.ResponsePolicySatisfied() {
			next.Phase = HumanInteractionSatisfied
		} else {
			next.Phase = HumanInteractionCollecting
		}
	case "tekroo.event.human-interaction.closed":
		var value struct {
			InteractionID               UUIDv7         `json:"interaction_id"`
			ExpectedInteractionRevision uint64         `json:"expected_interaction_revision"`
			QuestionRevision            uint64         `json:"question_revision"`
			AcceptedResponseEventIDs    []UUIDv7       `json:"accepted_response_event_ids"`
			AcceptedRespondents         []PrincipalRef `json:"accepted_respondents"`
			ResponsePolicySatisfied     bool           `json:"response_policy_satisfied"`
			Outcome                     string         `json:"outcome"`
			ResolvedEffect              string         `json:"resolved_effect"`
			AuthorizationEvidenceID     *UUIDv7        `json:"authorization_evidence_id"`
			ClosedAt                    time.Time      `json:"closed_at"`
			ClosedBy                    PrincipalRef   `json:"closed_by"`
			Reasons                     []string       `json:"reasons"`
			EvidenceIDs                 []UUIDv7       `json:"evidence_ids"`
		}
		if json.Unmarshal(event.Payload, &value) != nil || snapshot.Phase != HumanInteractionSatisfied || !snapshot.ResponsePolicySatisfied() || !value.ResponsePolicySatisfied || value.InteractionID != snapshot.InteractionID || value.ExpectedInteractionRevision != snapshot.Revision || value.QuestionRevision != snapshot.QuestionRevision || value.Outcome != "SATISFIED" || value.ClosedAt.IsZero() || !value.ClosedBy.Valid() || !validUniqueStrings(value.Reasons, 1, 64) || !validUniqueUUIDs(value.EvidenceIDs, 1, 64) || !exactAcceptedHumanResponses(snapshot.Responses, value.AcceptedResponseEventIDs, value.AcceptedRespondents) || !validResolvedHumanEffect(snapshot.DeclaredEffect, value.ResolvedEffect, value.AuthorizationEvidenceID, value.EvidenceIDs) {
			return snapshot, false
		}
		next.Phase = HumanInteractionClosed
	case "tekroo.event.human-interaction.expired":
		var value struct {
			InteractionID               UUIDv7       `json:"interaction_id"`
			ExpectedInteractionRevision uint64       `json:"expected_interaction_revision"`
			QuestionRevision            uint64       `json:"question_revision"`
			DeadlineAt                  time.Time    `json:"deadline_at"`
			ExpiredAt                   time.Time    `json:"expired_at"`
			SilenceIsConsent            bool         `json:"silence_is_consent"`
			Outcome                     string       `json:"outcome"`
			TimeoutPolicy               PrincipalRef `json:"timeout_policy"`
		}
		if json.Unmarshal(event.Payload, &value) != nil || snapshot.Phase == HumanInteractionClosed || snapshot.Phase == HumanInteractionExpired || value.InteractionID != snapshot.InteractionID || value.ExpectedInteractionRevision != snapshot.Revision || value.QuestionRevision != snapshot.QuestionRevision || !value.DeadlineAt.Equal(snapshot.DeadlineAt) || value.ExpiredAt.Before(snapshot.DeadlineAt) || value.SilenceIsConsent || value.Outcome != "EXPIRED" && value.Outcome != "BLOCKED" && value.Outcome != "HUMAN_REQUIRED" || value.TimeoutPolicy != snapshot.TimeoutPolicy {
			return snapshot, false
		}
		next.Phase = HumanInteractionExpired
	default:
		return snapshot, false
	}
	next.Revision++
	return next, next.Valid()
}

type parsedHumanResponse struct {
	InteractionID               UUIDv7
	ExpectedInteractionRevision uint64
	QuestionRevision            uint64
	DeliveryEventID             UUIDv7
	Record                      HumanResponseRecord
}

func humanResponseFromPayload(payload json.RawMessage, eventID UUIDv7) (parsedHumanResponse, bool) {
	var value struct {
		InteractionID               UUIDv7       `json:"interaction_id"`
		ExpectedInteractionRevision uint64       `json:"expected_interaction_revision"`
		QuestionRevision            uint64       `json:"question_revision"`
		Respondent                  PrincipalRef `json:"respondent"`
		ParticipantProfileID        UUIDv7       `json:"participant_profile_id"`
		ParticipantProfileRevision  uint64       `json:"participant_profile_revision"`
		RoleBindingID               UUIDv7       `json:"role_binding_id"`
		AuthenticationBindingID     UUIDv7       `json:"authentication_binding_id"`
		DeliveryID                  UUIDv7       `json:"delivery_id"`
		DeliveryEventID             UUIDv7       `json:"delivery_event_id"`
		ResponseArtifactDigest      Digest       `json:"response_artifact_digest"`
		ResponseSpecificationDigest Digest       `json:"response_specification_digest"`
		ResponseClassification      string       `json:"response_classification"`
		AssertedEffect              string       `json:"asserted_effect"`
		CredentialProvenanceDigest  Digest       `json:"credential_provenance_digest"`
		ChannelProvenanceDigest     Digest       `json:"channel_provenance_digest"`
		RespondedAt                 time.Time    `json:"responded_at"`
	}
	if json.Unmarshal(payload, &value) != nil || value.Respondent.Kind != PrincipalHuman || !eventID.Valid() {
		return parsedHumanResponse{}, false
	}
	record := HumanResponseRecord{EventID: eventID, Respondent: value.Respondent, ParticipantProfileID: value.ParticipantProfileID, ParticipantProfileRevision: value.ParticipantProfileRevision, RoleBindingID: value.RoleBindingID, AuthenticationBindingID: value.AuthenticationBindingID, DeliveryID: value.DeliveryID, ResponseArtifactDigest: value.ResponseArtifactDigest, ResponseSpecificationDigest: value.ResponseSpecificationDigest}
	validClassification := value.ResponseClassification == "ANSWER" || value.ResponseClassification == "DECLINE" || value.ResponseClassification == "REQUEST_CLARIFICATION"
	return parsedHumanResponse{InteractionID: value.InteractionID, ExpectedInteractionRevision: value.ExpectedInteractionRevision, QuestionRevision: value.QuestionRevision, DeliveryEventID: value.DeliveryEventID, Record: record}, record.ParticipantProfileID.Valid() && record.ParticipantProfileRevision > 0 && record.RoleBindingID.Valid() && record.AuthenticationBindingID.Valid() && record.DeliveryID.Valid() && value.DeliveryEventID.Valid() && record.ResponseArtifactDigest.Valid() && record.ResponseSpecificationDigest.Valid() && validClassification && validHumanInteractionEffect(value.AssertedEffect) && value.CredentialProvenanceDigest.Valid() && value.ChannelProvenanceDigest.Valid() && !value.RespondedAt.IsZero()
}

func exactAcceptedHumanResponses(responses []HumanResponseRecord, eventIDs []UUIDv7, respondents []PrincipalRef) bool {
	if len(responses) == 0 || len(eventIDs) != len(responses) || len(respondents) != len(responses) || !validUniqueUUIDs(eventIDs, 1, 64) {
		return false
	}
	events := make(map[UUIDv7]bool, len(responses))
	humans := make(map[PrincipalRef]bool, len(responses))
	for _, response := range responses {
		events[response.EventID] = true
		humans[response.Respondent] = true
	}
	for _, eventID := range eventIDs {
		if !events[eventID] {
			return false
		}
	}
	seen := make(map[PrincipalRef]bool, len(respondents))
	for _, respondent := range respondents {
		if respondent.Kind != PrincipalHuman || !respondent.Valid() || !humans[respondent] || seen[respondent] {
			return false
		}
		seen[respondent] = true
	}
	return true
}

func validResolvedHumanEffect(declared, resolved string, authorizationEvidenceID *UUIDv7, evidenceIDs []UUIDv7) bool {
	switch declared {
	case "ADVISORY_ONLY":
		return resolved == "ADVISORY_ONLY" && authorizationEvidenceID == nil
	case "EVIDENCE_ONLY":
		return resolved == "EVIDENCE_ONLY" && authorizationEvidenceID == nil
	case "AUTHORITY_IF_AUTHORIZED":
		if resolved == "EVIDENCE_ONLY" {
			return authorizationEvidenceID == nil
		}
		if resolved != "AUTHORIZED_EFFECT" || authorizationEvidenceID == nil || !authorizationEvidenceID.Valid() {
			return false
		}
		for _, evidenceID := range evidenceIDs {
			if evidenceID == *authorizationEvidenceID {
				return true
			}
		}
	}
	return false
}

func EvaluateHumanResponsePolicy(policy HumanResponsePolicy, selected, accepted []string) PolicyResult {
	selectedSet := make(map[string]bool, len(selected))
	for _, id := range selected {
		selectedSet[id] = true
	}
	acceptedSet := make(map[string]bool, len(accepted))
	for _, id := range accepted {
		if !selectedSet[id] {
			return PolicyResult{Reason: "RECIPIENT_MISMATCH"}
		}
		acceptedSet[id] = true
	}
	satisfied := policy.Kind == HumanResponseExactOne && len(selectedSet) == 1 && len(acceptedSet) == 1 || policy.Kind == HumanResponseAnyOne && len(acceptedSet) >= 1 || policy.Kind == HumanResponseAll && len(selectedSet) == len(acceptedSet) || policy.Kind == HumanResponseQuorum && uint64(len(acceptedSet)) >= policy.Quorum
	if !satisfied {
		return PolicyResult{Reason: "RESPONSE_POLICY_UNSATISFIED"}
	}
	return PolicyResult{Accepted: true, Reason: "RESPONSE_POLICY_SATISFIED"}
}
