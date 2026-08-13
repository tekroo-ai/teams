package kernel

import (
	"encoding/json"
	"sort"
	"time"
)

func validateOperatorHumanContinuityCommand(command KernelCommand, snapshot Snapshot, context DecisionContext) (OutcomeCode, string, bool) {
	object, err := decodePayloadObject(command.Payload)
	if err != nil {
		return OutcomeRejectedInvalid, reasonInvalidPayload, true
	}
	switch command.CommandType {
	case "tekroo.command.system.bind-operator-role":
		profile, parseErr := OperatorRoleFromPayload(command.Payload)
		expected, expectedOK := nonnegativeUint64Field(object, "expected_system_revision")
		if parseErr != nil || !expectedOK || expected != snapshot.Revision || profile.AuthorityPolicyRevision != command.ExpectedPolicyRevision || !payloadEvidenceMatches(object, command.EvidenceRefs) || !evidenceIDsCovered(profile.RoleBundleSignatureEvidenceIDs, command.EvidenceRefs) {
			return OutcomeRejectedConflict, reasonRevisionConflict, true
		}
		if !snapshot.Exists && profile.ReplacesBindingID != nil || snapshot.Exists && (snapshot.State == nil || snapshot.State.OperatorRole == nil || profile.ReplacesBindingID == nil || *profile.ReplacesBindingID != snapshot.State.OperatorRole.BindingID) {
			return OutcomeRejectedPolicy, reasonPolicy, true
		}
		return OutcomeApplied, reasonApplied, true
	case "tekroo.command.system.configure-continuity":
		expected, expectedOK := nonnegativeUint64Field(object, "expected_system_revision")
		configuredBy, principalOK := principalObjectField(object, "configured_by")
		if !expectedOK || expected != snapshot.Revision || !principalOK || configuredBy != command.Authority || !payloadEvidenceMatches(object, command.EvidenceRefs) {
			return OutcomeRejectedConflict, reasonRevisionConflict, true
		}
		if snapshot.Exists && (snapshot.State == nil || snapshot.State.Continuity != nil && snapshot.State.Continuity.ControlState != ContinuityActive) {
			return OutcomeRejectedPolicy, reasonPolicy, true
		}
		return OutcomeApplied, reasonApplied, true
	case "tekroo.command.system.request-quiescence", "tekroo.command.system.record-suspended", "tekroo.command.system.record-unexpected-outage", "tekroo.command.system.begin-reconciliation", "tekroo.command.system.resume":
		if snapshot.State == nil || snapshot.State.Continuity == nil || !payloadEvidenceMatches(object, command.EvidenceRefs) {
			return OutcomeRejectedPolicy, reasonPolicy, true
		}
		eventType := map[string]string{
			"tekroo.command.system.request-quiescence":       "tekroo.event.system.quiescence-requested",
			"tekroo.command.system.record-suspended":         "tekroo.event.system.suspended",
			"tekroo.command.system.record-unexpected-outage": "tekroo.event.system.unexpected-outage-recorded",
			"tekroo.command.system.begin-reconciliation":     "tekroo.event.system.reconciliation-started",
			"tekroo.command.system.resume":                   "tekroo.event.system.resumed",
		}[command.CommandType]
		_, valid := ApplyContinuityEvent(*snapshot.State.Continuity, DomainEvent{EventID: context.EventID, EventType: eventType, Aggregate: command.Target, AggregateRevision: snapshot.Revision + 1, Payload: command.Payload})
		if !valid {
			return OutcomeRejectedPolicy, reasonPolicy, true
		}
		return OutcomeApplied, reasonApplied, true
	case "tekroo.command.human-participant.bind-profile":
		participant, parseErr := HumanParticipantFromPayload(command.Payload, snapshot.Revision+1)
		if parseErr != nil || !payloadEvidenceMatches(object, command.EvidenceRefs) || !participantEvidenceCovered(participant, command.EvidenceRefs) {
			return OutcomeRejectedInvalid, reasonInvalidPayload, true
		}
		if !snapshot.Exists && (participant.ProfileRevision != 1 || participant.SupersedesProfileID != nil) || snapshot.Exists && (snapshot.State == nil || snapshot.State.Participant == nil || participant.Participant != snapshot.State.Participant.Participant || participant.ProfileRevision != snapshot.State.Participant.ProfileRevision+1 || participant.SupersedesProfileID == nil || *participant.SupersedesProfileID != snapshot.State.Participant.ProfileID) {
			return OutcomeRejectedConflict, reasonRevisionConflict, true
		}
		return OutcomeApplied, reasonApplied, true
	case "tekroo.command.human-participant.revoke":
		var value struct {
			Participant                 PrincipalRef `json:"participant"`
			ExpectedParticipantRevision uint64       `json:"expected_participant_revision"`
			ExpectedProfileID           UUIDv7       `json:"expected_profile_id"`
			RevokedBy                   PrincipalRef `json:"revoked_by"`
			RevokedAt                   time.Time    `json:"revoked_at"`
			Reason                      string       `json:"reason"`
		}
		if json.Unmarshal(command.Payload, &value) != nil || snapshot.State == nil || snapshot.State.Participant == nil || !snapshot.State.Participant.Active || value.Participant != snapshot.State.Participant.Participant || value.ExpectedParticipantRevision != snapshot.Revision || value.ExpectedProfileID != snapshot.State.Participant.ProfileID || value.RevokedBy != command.Authority || value.RevokedAt.IsZero() || value.Reason == "" || len(value.Reason) > 4096 || !payloadEvidenceMatches(object, command.EvidenceRefs) {
			return OutcomeRejectedConflict, reasonRevisionConflict, true
		}
		return OutcomeApplied, reasonApplied, true
	case "tekroo.command.human-interaction.open":
		interaction, parseErr := HumanInteractionFromOpenPayload(command.Payload, 1)
		if parseErr != nil || interaction.InteractionID != command.Target.ID || interaction.OriginPrincipal != command.Authority || !payloadEvidenceMatches(object, command.EvidenceRefs) || !causalPathMatchesCommandField(object, command.Causation) {
			return OutcomeRejectedInvalid, reasonInvalidPayload, true
		}
		if interaction.OriginPrincipal.Kind == PrincipalActor {
			if command.ActorFQN == nil || command.Execution == nil || interaction.OriginActorFQN == nil || interaction.OriginExecution == nil || *command.ActorFQN != *interaction.OriginActorFQN || *command.Execution != *interaction.OriginExecution {
				return OutcomeRejectedStaleExecution, reasonStaleExecution, true
			}
			if current, found := snapshot.CurrentExecutions[*command.ActorFQN]; !found || current != *command.Execution {
				return OutcomeRejectedStaleExecution, reasonStaleExecution, true
			}
		}
		related, found := snapshot.Related[interaction.Subject]
		if !found || !related.Exists || related.Revision != interaction.ExpectedSubjectRevision || related.State == nil || related.State.LifecycleEpoch != interaction.SubjectLifecycleEpoch || !containsAggregatePrecondition(command.Preconditions, interaction.Subject) {
			return OutcomeRejectedConflict, reasonRevisionConflict, true
		}
		for _, recipient := range interaction.Recipients {
			profile, profileFound := findParticipantProfile(snapshot.Related, recipient)
			if !profileFound || !profile.Active || profile.ProfileID != recipient.ParticipantProfileID || profile.ProfileRevision != recipient.ParticipantProfileRevision || profile.ProfileDigest != recipient.ParticipantProfileDigest {
				return OutcomeRejectedPolicy, reasonPolicy, true
			}
			if _, routeOK := profile.Delivery(recipient.DeliveryBindingID, interaction.Confidentiality); !routeOK || !profileHasRole(profile, recipient.RoleBindingID, context.DecidedAt) {
				return OutcomeRejectedPolicy, reasonPolicy, true
			}
		}
		return OutcomeApplied, reasonApplied, true
	case "tekroo.command.human-interaction.record-delivery", "tekroo.command.human-interaction.respond", "tekroo.command.human-interaction.close", "tekroo.command.human-interaction.expire":
		if snapshot.State == nil || snapshot.State.Interaction == nil || !payloadEvidenceMatches(object, command.EvidenceRefs) {
			return OutcomeRejectedPolicy, reasonPolicy, true
		}
		if command.CommandType == "tekroo.command.human-interaction.respond" {
			if command.Authority.Kind != PrincipalHuman || command.ActorFQN != nil || command.Execution != nil {
				return OutcomeRejectedUnauthorized, reasonUnauthorized, true
			}
			response, ok := humanResponseFromPayload(command.Payload, context.EventID)
			if !ok || response.Record.Respondent != command.Authority {
				return OutcomeRejectedUnauthorized, reasonUnauthorized, true
			}
			recipient, selected := snapshot.State.Interaction.Selected(response.Record.Respondent)
			profile, profileFound := findParticipantProfile(snapshot.Related, recipient)
			if !selected || !profileFound || !profile.Active || profile.ProfileID != response.Record.ParticipantProfileID || profile.ProfileRevision != response.Record.ParticipantProfileRevision || !profileHasRole(profile, response.Record.RoleBindingID, context.DecidedAt) {
				return OutcomeRejectedUnauthorized, reasonUnauthorized, true
			}
			if _, authenticated := profile.Authentication(response.Record.AuthenticationBindingID); !authenticated {
				return OutcomeRejectedUnauthorized, reasonUnauthorized, true
			}
		}
		eventType := map[string]string{
			"tekroo.command.human-interaction.record-delivery": "tekroo.event.human-interaction.delivery-recorded",
			"tekroo.command.human-interaction.respond":         "tekroo.event.human-interaction.response-recorded",
			"tekroo.command.human-interaction.close":           "tekroo.event.human-interaction.closed",
			"tekroo.command.human-interaction.expire":          "tekroo.event.human-interaction.expired",
		}[command.CommandType]
		_, valid := ApplyHumanInteractionEvent(*snapshot.State.Interaction, DomainEvent{EventID: context.EventID, EventType: eventType, Aggregate: command.Target, AggregateRevision: snapshot.Revision + 1, Payload: command.Payload})
		if !valid {
			return OutcomeRejectedPolicy, reasonPolicy, true
		}
		return OutcomeApplied, reasonApplied, true
	default:
		return OutcomeApplied, reasonApplied, false
	}
}

func evidenceIDsCovered(ids []UUIDv7, refs []EvidenceRef) bool {
	covered := make(map[UUIDv7]bool, len(refs))
	for _, ref := range refs {
		covered[ref.EvidenceID] = true
	}
	for _, id := range ids {
		if !covered[id] {
			return false
		}
	}
	return true
}

func participantEvidenceCovered(participant HumanParticipantSnapshot, refs []EvidenceRef) bool {
	for _, binding := range participant.RoleBindings {
		if !evidenceIDsCovered(binding.EvidenceIDs, refs) {
			return false
		}
	}
	for _, binding := range participant.AuthenticationBindings {
		if !evidenceIDsCovered(binding.EvidenceIDs, refs) {
			return false
		}
	}
	for _, binding := range participant.DeliveryBindings {
		if !evidenceIDsCovered(binding.EvidenceIDs, refs) {
			return false
		}
	}
	return true
}

func principalObjectField(object map[string]any, name string) (PrincipalRef, bool) {
	value, ok := object[name].(map[string]any)
	if !ok {
		return PrincipalRef{}, false
	}
	kind, kindOK := value["kind"].(string)
	id, idOK := value["id"].(string)
	principal := PrincipalRef{Kind: PrincipalKind(kind), ID: id}
	return principal, kindOK && idOK && principal.Valid()
}

func causalPathMatchesCommandField(object map[string]any, parents []DagParent) bool {
	ids, ok := uuidArrayField(object, "causal_path_event_ids")
	if !ok || len(ids) != len(parents) {
		return false
	}
	canonical := canonicalParents(parents)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for index, id := range ids {
		if canonical[index].ParentEventID != id || canonical[index].EdgeKind != EdgeCausal {
			return false
		}
	}
	return true
}

func findParticipantProfile(related map[AggregateRef]RelatedSnapshot, recipient HumanInteractionRecipient) (HumanParticipantSnapshot, bool) {
	for _, value := range related {
		if value.State != nil && value.State.Participant != nil && value.State.Participant.Participant == recipient.Principal {
			return value.State.Participant.Clone(), true
		}
	}
	return HumanParticipantSnapshot{}, false
}

func profileHasRole(profile HumanParticipantSnapshot, roleID UUIDv7, at time.Time) bool {
	for _, role := range profile.RoleBindings {
		if role.RoleBindingID == roleID && role.Current(at) {
			return true
		}
	}
	return false
}
