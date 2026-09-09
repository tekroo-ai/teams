package operationalruntime

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func (service *ProductionService) RegisterHumanParticipant(ctx context.Context, operator kernel.PrincipalRef, registration organization.HumanParticipantRegistration) (kernel.HumanParticipantSnapshot, error) {
	if service == nil || operator != service.operatorIdentity.Principal || !registration.Valid() {
		return kernel.HumanParticipantSnapshot{}, organization.ErrInvalidHumanOperation
	}
	now := service.clock.Now().UTC()
	encoded, err := json.Marshal(registration)
	if err != nil {
		return kernel.HumanParticipantSnapshot{}, err
	}
	digest := digestBytes(encoded)
	evidenceID := deterministicOperationalUUID("human-participant-evidence", registration.Participant.ID, registration.IdempotencyKey, string(digest))
	evidencePayload := mustJSON(map[string]any{
		"access_partition": registration.ScopeID, "availability": "AVAILABLE", "byte_length": len(encoded), "canonical_digest": digest,
		"computation": nil, "deletion_tombstone": nil, "evidence_kind": "DECISION_RECORD", "integrity_state": "DIGEST_VERIFIED",
		"locator": "teams://human-participant/" + registration.Participant.ID + "/registration", "locator_immutable": true, "media_type": "application/json",
		"producing_component": "tekrood-human-directory", "producing_version": "1.0.0", "redacts": nil, "retention_policy": "human-participant-lifecycle",
		"sensitivity": "INTERNAL", "sha256": digest, "source_evidence_ids": []kernel.UUIDv7{}, "source_timestamp": now, "transport_provenance": "authenticated-operator-registration",
	})
	if _, err := service.submitStandaloneCommand(ctx, "tekroo.command.evidence.register", kernel.AggregateEvidence, evidenceID, service.serviceAuthority, 0, evidencePayload, nil, nil, "human-participant-evidence:"+registration.IdempotencyKey); err != nil {
		return kernel.HumanParticipantSnapshot{}, err
	}
	profileID := deterministicOperationalUUID("human-profile", registration.Participant.ID, registration.IdempotencyKey)
	roleBindingID := deterministicOperationalUUID("human-role", registration.Participant.ID, registration.RoleID, registration.ScopeKind, registration.ScopeID)
	authenticationBindingID := deterministicOperationalUUID("human-auth", registration.Participant.ID)
	deliveryBindingID := deterministicOperationalUUID("human-delivery", registration.Participant.ID, registration.Channel)
	participantID := humanParticipantAggregateID(registration.Participant)
	evidence := []kernel.EvidenceRef{{EvidenceID: evidenceID, SHA256: digest}}
	advisoryTopics := append([]string{}, registration.AdvisoryTopicIDs...)
	authorizedCommands := append([]string{}, registration.AuthorizedCommandTypes...)
	payload := mustJSON(map[string]any{
		"participant": registration.Participant, "expected_participant_revision": uint64(0), "profile_id": profileID, "profile_revision": uint64(1), "profile_digest": digest,
		"display_label": registration.DisplayLabel, "privacy_classification": registration.Confidentiality,
		"role_bindings":               []map[string]any{{"role_binding_id": roleBindingID, "role_class": registration.RoleClass, "role_id": registration.RoleID, "scope_kind": registration.ScopeKind, "scope_id": registration.ScopeID, "advisory_topic_ids": advisoryTopics, "authorized_command_types": authorizedCommands, "authority_policy_revision": service.provenance.PolicyRevision, "authority_policy_digest": service.provenance.PolicyDigest, "valid_from": now, "valid_until": nil, "active": true, "evidence_ids": []kernel.UUIDv7{evidenceID}}},
		"authentication_bindings":     []kernel.HumanAuthenticationBinding{{AuthenticationBindingID: authenticationBindingID, Method: "API_CREDENTIAL", IssuerDigest: digestBytes([]byte("tekrood-local-human-auth")), SubjectDigest: digestBytes([]byte(registration.Participant.ID)), Assurance: "STANDARD", BindingRevision: 1, Active: true, EvidenceIDs: []kernel.UUIDv7{evidenceID}}},
		"delivery_bindings":           []kernel.HumanDeliveryBinding{{DeliveryBindingID: deliveryBindingID, Channel: registration.Channel, EndpointDigest: digestBytes([]byte("local-human-endpoint\x00" + registration.Participant.ID)), AdapterProfileDigest: digestBytes([]byte("tekrood-local-console-v1")), ConfidentialityCeiling: registration.Confidentiality, AuthenticationBindingIDs: []kernel.UUIDv7{authenticationBindingID}, BindingRevision: 1, Active: true, EvidenceIDs: []kernel.UUIDv7{evidenceID}}},
		"participant_policy_revision": service.provenance.PolicyRevision, "participant_policy_digest": service.provenance.PolicyDigest, "supersedes_profile_id": nil, "evidence_ids": []kernel.UUIDv7{evidenceID},
	})
	if _, err := service.submitStandaloneCommand(ctx, "tekroo.command.human-participant.bind-profile", kernel.AggregateHumanParticipant, participantID, operator, 0, payload, nil, evidence, "human-participant:"+registration.IdempotencyKey); err != nil {
		return kernel.HumanParticipantSnapshot{}, err
	}
	return service.ReadHumanParticipant(ctx, registration.Participant)
}

func (service *ProductionService) AskHuman(ctx context.Context, origin kernel.PrincipalRef, request organization.HumanQuestionRequest) (organization.HumanNotification, error) {
	if service == nil || origin != service.operatorIdentity.Principal || !request.Valid(service.clock.Now().UTC()) {
		return organization.HumanNotification{}, organization.ErrInvalidHumanOperation
	}
	participant, err := service.ReadHumanParticipant(ctx, request.Recipient)
	if err != nil || !participant.Active {
		return organization.HumanNotification{}, errors.Join(organization.ErrInvalidHumanOperation, err)
	}
	participantRef := kernel.AggregateRef{Kind: kernel.AggregateHumanParticipant, ID: humanParticipantAggregateID(request.Recipient)}
	subject := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: request.SubjectTaskID}
	taskState, taskHead, found, err := service.Store.ReadAggregateHead(ctx, subject)
	if err != nil || !found || taskState.Phase != kernel.PhaseActive || taskState.Condition != kernel.ConditionRunnable {
		return organization.HumanNotification{}, errors.Join(organization.ErrInvalidHumanOperation, err)
	}
	role, delivery, ok := selectHumanRoute(participant, request.Confidentiality, service.clock.Now().UTC())
	if !ok {
		return organization.HumanNotification{}, organization.ErrInvalidHumanOperation
	}
	if len(role.EvidenceIDs) == 0 {
		return organization.HumanNotification{}, organization.ErrInvalidHumanOperation
	}
	evidenceIDs := []kernel.UUIDv7{role.EvidenceIDs[0]}
	evidenceRefs := []kernel.EvidenceRef{{EvidenceID: role.EvidenceIDs[0], SHA256: participant.ProfileDigest}}
	interactionID := deterministicOperationalUUID("human-interaction", origin.ID, request.IdempotencyKey)
	questionDigest := digestBytes([]byte(request.Question))
	responseSpecificationDigest := digestBytes([]byte(request.ResponseSpecification))
	recipient := kernel.HumanInteractionRecipient{Principal: request.Recipient, ParticipantProfileID: participant.ProfileID, ParticipantProfileRevision: participant.ProfileRevision, ParticipantProfileDigest: participant.ProfileDigest, RoleBindingID: role.RoleBindingID, DeliveryBindingID: delivery.DeliveryBindingID}
	payload := mustJSON(map[string]any{
		"interaction_id": interactionID, "subject_kind": subject.Kind, "subject_id": subject.ID, "subject_lifecycle_epoch": taskState.LifecycleEpoch, "expected_subject_revision": taskState.Revision,
		"question_revision": uint64(1), "canonical_question_digest": questionDigest, "response_specification_digest": responseSpecificationDigest,
		"origin_principal": origin, "origin_actor_fqn": nil, "origin_execution_id": nil, "origin_execution_fencing_epoch": nil,
		"recipients": []kernel.HumanInteractionRecipient{recipient}, "response_policy": kernel.HumanResponsePolicy{Kind: kernel.HumanResponseExactOne},
		"purpose": request.Purpose, "declared_effect": request.DeclaredEffect, "confidentiality": request.Confidentiality, "deadline_at": request.DeadlineAt,
		"timeout_policy": service.policyAuthority, "disclosure_scope_digest": digestBytes([]byte(string(subject.Kind) + "\x00" + string(subject.ID) + "\x00" + string(request.Confidentiality))),
		"interaction_policy_revision": service.provenance.PolicyRevision, "interaction_policy_digest": service.provenance.PolicyDigest, "predecessor_interaction_id": nil,
		"causal_path_event_ids": []kernel.UUIDv7{taskHead}, "evidence_ids": evidenceIDs,
	})
	preconditions := []kernel.AggregatePrecondition{{Aggregate: participantRef, Expected: kernel.NewExpectedRevision(participant.Revision)}, {Aggregate: subject, Expected: kernel.NewExpectedRevision(taskState.Revision)}}
	sort.Slice(preconditions, func(left, right int) bool {
		leftKey := string(preconditions[left].Aggregate.Kind) + ":" + string(preconditions[left].Aggregate.ID)
		rightKey := string(preconditions[right].Aggregate.Kind) + ":" + string(preconditions[right].Aggregate.ID)
		return leftKey < rightKey
	})
	opened, err := service.submitStandaloneCommand(ctx, "tekroo.command.human-interaction.open", kernel.AggregateHumanInteraction, interactionID, origin, 0, payload, []kernel.DagParent{{ParentEventID: taskHead, EdgeKind: kernel.EdgeCausal}}, evidenceRefs, "human-question:"+request.IdempotencyKey, preconditions...)
	if err != nil {
		return organization.HumanNotification{}, err
	}
	deliveryID := deterministicOperationalUUID("human-delivery", string(interactionID), request.Recipient.ID)
	deliveryPayload := mustJSON(map[string]any{
		"interaction_id": interactionID, "expected_interaction_revision": uint64(1), "question_revision": uint64(1), "delivery_id": deliveryID,
		"recipient": request.Recipient, "delivery_binding_id": delivery.DeliveryBindingID, "adapter_profile_digest": delivery.AdapterProfileDigest,
		"canonical_question_digest": questionDigest, "rendered_question_digest": questionDigest, "presenter_actor_fqn": nil, "presenter_execution_id": nil,
		"material_equivalence_evidence_id": nil, "outcome": "DELIVERED", "attempted_at": service.clock.Now().UTC(), "channel_provenance_digest": delivery.EndpointDigest, "evidence_ids": evidenceIDs,
	})
	delivered, err := service.submitStandaloneCommand(ctx, "tekroo.command.human-interaction.record-delivery", kernel.AggregateHumanInteraction, interactionID, service.serviceAuthority, 1, deliveryPayload, []kernel.DagParent{{ParentEventID: opened.EventIDs[0], EdgeKind: kernel.EdgeCausal}}, evidenceRefs, "human-delivery:"+request.IdempotencyKey)
	if err != nil {
		return organization.HumanNotification{}, err
	}
	blockPayload := mustJSON(map[string]any{"blocker_refs": []string{"teams://human-interaction/" + string(interactionID)}, "reason": "waiting for an exact authenticated human response", "review_policy": "review-after-human-response"})
	if _, err := service.submitStandaloneCommand(ctx, "tekroo.command.work.block", kernel.AggregateTask, request.SubjectTaskID, service.policyAuthority, taskState.Revision, blockPayload, []kernel.DagParent{{ParentEventID: delivered.EventIDs[0], EdgeKind: kernel.EdgeResponse}}, nil, "human-wait:"+request.IdempotencyKey); err != nil {
		return organization.HumanNotification{}, err
	}
	notification := organization.HumanNotification{SchemaVersion: "1.0.0", InteractionID: interactionID, SubjectTaskID: request.SubjectTaskID, Recipient: request.Recipient, Question: request.Question, ResponseSpecification: request.ResponseSpecification, DeliveryID: deliveryID, DeliveryEventID: delivered.EventIDs[0], State: kernel.HumanInteractionCollecting, CreatedAt: service.clock.Now().UTC(), UpdatedAt: service.clock.Now().UTC()}
	if err := service.Store.SaveHumanNotification(ctx, notification); err != nil {
		return organization.HumanNotification{}, err
	}
	return notification, nil
}

func (service *ProductionService) RespondToHumanQuestion(ctx context.Context, respondent kernel.PrincipalRef, input organization.HumanResponseInput) (organization.HumanNotification, error) {
	if service == nil || respondent.Kind != kernel.PrincipalHuman || !respondent.Valid() || !input.Valid() {
		return organization.HumanNotification{}, organization.ErrInvalidHumanOperation
	}
	interaction, err := service.ReadHumanInteraction(ctx, input.InteractionID)
	if err != nil || interaction.Revision != input.ExpectedRevision || interaction.Phase != kernel.HumanInteractionCollecting {
		return organization.HumanNotification{}, errors.Join(organization.ErrInvalidHumanOperation, err)
	}
	recipient, selected := interaction.Selected(respondent)
	participant, participantErr := service.ReadHumanParticipant(ctx, respondent)
	if !selected || participantErr != nil || participant.ProfileID != recipient.ParticipantProfileID || participant.ProfileRevision != recipient.ParticipantProfileRevision {
		return organization.HumanNotification{}, errors.Join(organization.ErrInvalidHumanOperation, participantErr)
	}
	delivery, delivered := latestDelivered(interaction, respondent)
	role, roleFound := participantRole(participant, recipient.RoleBindingID)
	binding, bindingFound := participant.Delivery(recipient.DeliveryBindingID, interaction.Confidentiality)
	if !delivered || !roleFound || !bindingFound || len(binding.AuthenticationBindingIDs) == 0 {
		return organization.HumanNotification{}, organization.ErrInvalidHumanOperation
	}
	authentication, authenticated := participant.Authentication(binding.AuthenticationBindingIDs[0])
	if !authenticated {
		return organization.HumanNotification{}, organization.ErrInvalidHumanOperation
	}
	if len(role.EvidenceIDs) == 0 {
		return organization.HumanNotification{}, organization.ErrInvalidHumanOperation
	}
	evidenceIDs := []kernel.UUIDv7{role.EvidenceIDs[0]}
	evidenceRefs := []kernel.EvidenceRef{{EvidenceID: role.EvidenceIDs[0], SHA256: participant.ProfileDigest}}
	now := service.clock.Now().UTC()
	responseDigest := digestBytes([]byte(input.Response))
	payload := mustJSON(map[string]any{
		"interaction_id": interaction.InteractionID, "expected_interaction_revision": interaction.Revision, "question_revision": interaction.QuestionRevision,
		"respondent": respondent, "participant_profile_id": participant.ProfileID, "participant_profile_revision": participant.ProfileRevision,
		"role_binding_id": role.RoleBindingID, "authentication_binding_id": authentication.AuthenticationBindingID, "delivery_id": delivery.DeliveryID, "delivery_event_id": delivery.EventID,
		"response_artifact_digest": responseDigest, "response_specification_digest": interaction.ResponseSpecificationDigest, "response_classification": input.ResponseClassification,
		"asserted_effect": interaction.DeclaredEffect, "credential_provenance_digest": digestBytes([]byte(string(authentication.AuthenticationBindingID) + "\x00" + string(authentication.SubjectDigest))),
		"channel_provenance_digest": digestBytes([]byte(string(binding.EndpointDigest) + "\x00" + string(binding.AdapterProfileDigest))), "responded_at": now, "evidence_ids": evidenceIDs,
	})
	participantRef := kernel.AggregateRef{Kind: kernel.AggregateHumanParticipant, ID: humanParticipantAggregateID(respondent)}
	recorded, err := service.submitStandaloneCommand(ctx, "tekroo.command.human-interaction.respond", kernel.AggregateHumanInteraction, interaction.InteractionID, respondent, interaction.Revision, payload, []kernel.DagParent{{ParentEventID: delivery.EventID, EdgeKind: kernel.EdgeResponse}}, evidenceRefs, "human-response:"+string(interaction.InteractionID)+":"+string(responseDigest), kernel.AggregatePrecondition{Aggregate: participantRef, Expected: kernel.NewExpectedRevision(participant.Revision)})
	if err != nil {
		return organization.HumanNotification{}, err
	}
	closePayload := mustJSON(map[string]any{
		"interaction_id": interaction.InteractionID, "expected_interaction_revision": interaction.Revision + 1, "question_revision": interaction.QuestionRevision,
		"response_policy_satisfied": true, "accepted_response_event_ids": recorded.EventIDs, "accepted_respondents": []kernel.PrincipalRef{respondent},
		"outcome": "SATISFIED", "resolved_effect": interaction.DeclaredEffect, "authorization_evidence_id": nil, "closed_at": now, "closed_by": service.policyAuthority,
		"reasons": []string{"the exact selected participant supplied an authenticated response"}, "evidence_ids": evidenceIDs,
	})
	closed, err := service.submitStandaloneCommand(ctx, "tekroo.command.human-interaction.close", kernel.AggregateHumanInteraction, interaction.InteractionID, service.policyAuthority, interaction.Revision+1, closePayload, []kernel.DagParent{{ParentEventID: recorded.EventIDs[0], EdgeKind: kernel.EdgeResponse}}, evidenceRefs, "human-close:"+string(interaction.InteractionID))
	if err != nil {
		return organization.HumanNotification{}, err
	}
	taskRef := interaction.Subject
	taskState, _, found, err := service.Store.ReadAggregateHead(ctx, taskRef)
	if err != nil || !found || taskState.Condition != kernel.ConditionBlocked {
		return organization.HumanNotification{}, errors.Join(organization.ErrInvalidHumanOperation, err)
	}
	unblockPayload := mustJSON(map[string]any{"resolved_blocker_refs": []string{"teams://human-interaction/" + string(interaction.InteractionID)}, "evidence_ids": evidenceIDs})
	if _, err := service.submitStandaloneCommand(ctx, "tekroo.command.work.unblock", taskRef.Kind, taskRef.ID, service.policyAuthority, taskState.Revision, unblockPayload, []kernel.DagParent{{ParentEventID: closed.EventIDs[0], EdgeKind: kernel.EdgeResponse}}, evidenceRefs, "human-unblock:"+string(interaction.InteractionID)); err != nil {
		return organization.HumanNotification{}, err
	}
	notification, found, err := service.Store.ReadHumanNotification(ctx, interaction.InteractionID)
	if err != nil || !found {
		return organization.HumanNotification{}, errors.Join(organization.ErrInvalidHumanOperation, err)
	}
	responseEventID := recorded.EventIDs[0]
	notification.Response = input.Response
	notification.ResponseEventID = &responseEventID
	notification.State = kernel.HumanInteractionClosed
	notification.UpdatedAt = now
	if err := service.Store.SaveHumanNotification(ctx, notification); err != nil {
		return organization.HumanNotification{}, err
	}
	return notification, nil
}

func (service *ProductionService) ReadHumanParticipant(ctx context.Context, participant kernel.PrincipalRef) (kernel.HumanParticipantSnapshot, error) {
	if service == nil || participant.Kind != kernel.PrincipalHuman || !participant.Valid() {
		return kernel.HumanParticipantSnapshot{}, organization.ErrInvalidHumanOperation
	}
	state, _, found, err := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateHumanParticipant, ID: humanParticipantAggregateID(participant)})
	if err != nil || !found || state.Participant == nil || state.Participant.Participant != participant {
		return kernel.HumanParticipantSnapshot{}, errors.Join(organization.ErrInvalidHumanOperation, err)
	}
	return state.Participant.Clone(), nil
}

func (service *ProductionService) ReadHumanInteraction(ctx context.Context, interactionID kernel.UUIDv7) (kernel.HumanInteractionSnapshot, error) {
	if service == nil || !interactionID.Valid() {
		return kernel.HumanInteractionSnapshot{}, organization.ErrInvalidHumanOperation
	}
	state, _, found, err := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateHumanInteraction, ID: interactionID})
	if err != nil || !found || state.Interaction == nil {
		return kernel.HumanInteractionSnapshot{}, errors.Join(organization.ErrInvalidHumanOperation, err)
	}
	return state.Interaction.Clone(), nil
}

func (service *ProductionService) HumanNotifications(ctx context.Context, recipient kernel.PrincipalRef, openOnly bool) ([]organization.HumanNotification, error) {
	return service.Store.ListHumanNotifications(ctx, recipient, openOnly, 100)
}

func (service *ProductionService) submitStandaloneCommand(ctx context.Context, commandType string, kind kernel.AggregateKind, id kernel.UUIDv7, authority kernel.PrincipalRef, revision uint64, payload []byte, parents []kernel.DagParent, evidence []kernel.EvidenceRef, idempotencyKey string, preconditions ...kernel.AggregatePrecondition) (kernel.CommandReceipt, error) {
	now := service.clock.Now().UTC()
	command := kernel.KernelCommand{ContractManifest: kernel.ContractIdentity, CommandID: deterministicOperationalUUID("standalone-command", commandType, string(id), idempotencyKey), CommandType: commandType, CommandVersion: kernel.SchemaVersion, Target: kernel.AggregateRef{Kind: kind, ID: id}, Authority: authority, ExpectedRevision: expectedRevision(revision), Preconditions: append([]kernel.AggregatePrecondition(nil), preconditions...), ExpectedPolicyRevision: service.provenance.PolicyRevision, ExpectedCatalogueRevision: kernel.CatalogueRevision, IdempotencyKey: idempotencyKey, CorrelationID: id, Causation: append([]kernel.DagParent(nil), parents...), IssuedAt: &now, Payload: payload, EvidenceRefs: append([]kernel.EvidenceRef(nil), evidence...)}
	if revision > 0 && (kind == kernel.AggregateTask || kind == kernel.AggregateStory) {
		state, _, found, err := service.Store.ReadAggregateHead(ctx, command.Target)
		if err != nil || !found {
			return kernel.CommandReceipt{}, errors.Join(organization.ErrInvalidHumanOperation, err)
		}
		command.ExpectedLifecycleEpoch = &state.LifecycleEpoch
		if kind == kernel.AggregateTask && state.Ownership.OwnerFQN != nil {
			decision, decisionErr := service.Store.LoadDecision(ctx, command)
			assignment, assigned := decision.QualifiedAssignments[command.Target]
			if decisionErr != nil || !assigned || assignment.SelectedActorFQN != *state.Ownership.OwnerFQN {
				return kernel.CommandReceipt{}, errors.Join(organization.ErrInvalidHumanOperation, decisionErr)
			}
			actor := assignment.SelectedActorFQN
			execution := assignment.SelectedExecution()
			// Lifecycle control may legitimately occur after the assigned actor was
			// replaced. Stamp the command with the owner's current execution when it
			// is available; the superseded assignment execution would be rejected by
			// the kernel's fencing check even though the task owner is unchanged.
			if service.RoleHost != nil {
				current, active, statusErr := service.RoleHost.Status(ctx, actor)
				if statusErr != nil {
					return kernel.CommandReceipt{}, statusErr
				}
				if active {
					if current.ActorFQN != actor || !current.Execution.Valid() {
						return kernel.CommandReceipt{}, organization.ErrInvalidHumanOperation
					}
					profile, configured := service.profilesByModel[current.ModelProfile]
					if !configured {
						return kernel.CommandReceipt{}, organization.ErrInvalidHumanOperation
					}
					if err := service.registerExecution(ctx, current, profile); err != nil {
						return kernel.CommandReceipt{}, err
					}
					execution = current.Execution
				}
			}
			command.ActorFQN = &actor
			command.Execution = &execution
			// The execution tuple participates in the command fingerprint. Bind it
			// into both deterministic identities so a control retry after process
			// replacement cannot collide with the receipt from the prior execution.
			executionKey := idempotencyKey + ":execution:" + string(execution.ExecutionID) + ":" + strconv.FormatUint(execution.FencingEpoch, 10)
			command.CommandID = deterministicOperationalUUID("standalone-command", commandType, string(id), executionKey)
			command.IdempotencyKey = executionKey
		}
	}
	if _, err := service.Runtime.catalogue.ResolveCommand(command.CommandType, command.CommandVersion, command.Target.Kind, command.Payload); err != nil {
		return kernel.CommandReceipt{}, err
	}
	receipt, err := service.Submit(ctx, command)
	if err != nil {
		return receipt, err
	}
	if receipt.OutcomeCode != kernel.OutcomeApplied && receipt.OutcomeCode != kernel.OutcomeNoChange {
		return receipt, errors.New(commandType + " rejected: " + receipt.ReasonCode)
	}
	return receipt, nil
}

func humanParticipantAggregateID(participant kernel.PrincipalRef) kernel.UUIDv7 {
	return deterministicOperationalUUID("human-participant", string(participant.Kind), participant.ID)
}

func selectHumanRoute(participant kernel.HumanParticipantSnapshot, confidentiality kernel.Confidentiality, at time.Time) (kernel.HumanRoleBinding, kernel.HumanDeliveryBinding, bool) {
	for _, role := range participant.RoleBindings {
		if !role.Current(at) {
			continue
		}
		for _, delivery := range participant.DeliveryBindings {
			if selected, found := participant.Delivery(delivery.DeliveryBindingID, confidentiality); found {
				return role, selected, true
			}
		}
	}
	return kernel.HumanRoleBinding{}, kernel.HumanDeliveryBinding{}, false
}

func latestDelivered(interaction kernel.HumanInteractionSnapshot, participant kernel.PrincipalRef) (kernel.HumanDeliveryRecord, bool) {
	for index := len(interaction.Deliveries) - 1; index >= 0; index-- {
		if interaction.Deliveries[index].Recipient == participant && interaction.Deliveries[index].Outcome == "DELIVERED" {
			return interaction.Deliveries[index], true
		}
	}
	return kernel.HumanDeliveryRecord{}, false
}

func participantRole(participant kernel.HumanParticipantSnapshot, id kernel.UUIDv7) (kernel.HumanRoleBinding, bool) {
	for _, role := range participant.RoleBindings {
		if role.RoleBindingID == id && role.Active {
			return role, true
		}
	}
	return kernel.HumanRoleBinding{}, false
}
