package kernel

import (
	"errors"
	"fmt"
)

var ErrCorruptEventStream = errors.New("corrupt aggregate event stream")

func FoldAggregate(events []DomainEvent) (*AggregateState, error) {
	if len(events) == 0 {
		return nil, nil
	}
	aggregate := events[0].Aggregate
	var state *AggregateState
	for index, event := range events {
		expectedRevision := uint64(index + 1)
		if event.Aggregate != aggregate || event.AggregateRevision != expectedRevision || !event.EventID.Valid() || event.EventType == "" {
			return nil, fmt.Errorf("%w: invalid event at revision %d", ErrCorruptEventStream, expectedRevision)
		}
		if expectedRevision == 1 {
			switch event.EventType {
			case "tekroo.event.story.created":
				state = &AggregateState{Kind: AggregateStory, ID: aggregate.ID, LifecycleEpoch: 1, ScopeRevision: 1, Phase: PhaseDraft, Condition: ConditionRunnable}
			case "tekroo.event.task.created":
				state = &AggregateState{Kind: AggregateTask, ID: aggregate.ID, LifecycleEpoch: 1, ScopeRevision: 1, Phase: PhasePlanned, Condition: ConditionRunnable}
			case "tekroo.event.system.operator-role-bound":
				profile, err := OperatorRoleFromPayload(event.Payload)
				if err != nil {
					return nil, fmt.Errorf("%w: invalid operator role creation", ErrCorruptEventStream)
				}
				state = &AggregateState{Kind: AggregateSystem, ID: aggregate.ID, LifecycleEpoch: 1, ScopeRevision: 1, Phase: PhaseDraft, Condition: ConditionRunnable, OperatorRole: &profile}
			case "tekroo.event.system.continuity-configured":
				continuity, err := TeamContinuityFromConfigurePayload(event.Payload, event.EventID, 1)
				if err != nil {
					return nil, fmt.Errorf("%w: invalid continuity creation", ErrCorruptEventStream)
				}
				state = &AggregateState{Kind: AggregateSystem, ID: aggregate.ID, LifecycleEpoch: 1, ScopeRevision: 1, Phase: PhaseDraft, Condition: ConditionRunnable, Continuity: &continuity}
			case "tekroo.event.human-participant.profile-bound":
				participant, err := HumanParticipantFromPayload(event.Payload, 1)
				if err != nil {
					return nil, fmt.Errorf("%w: invalid human participant creation", ErrCorruptEventStream)
				}
				state = &AggregateState{Kind: AggregateHumanParticipant, ID: aggregate.ID, LifecycleEpoch: 1, ScopeRevision: 1, Phase: PhaseDraft, Condition: ConditionRunnable, Participant: &participant}
			case "tekroo.event.human-interaction.opened":
				interaction, err := HumanInteractionFromOpenPayload(event.Payload, 1)
				if err != nil || interaction.InteractionID != aggregate.ID {
					return nil, fmt.Errorf("%w: invalid human interaction creation", ErrCorruptEventStream)
				}
				state = &AggregateState{Kind: AggregateHumanInteraction, ID: aggregate.ID, LifecycleEpoch: 1, ScopeRevision: 1, Phase: PhaseDraft, Condition: ConditionRunnable, Interaction: &interaction}
			default:
				return nil, fmt.Errorf("%w: missing creation event", ErrCorruptEventStream)
			}
		} else if state == nil {
			return nil, fmt.Errorf("%w: state disappeared", ErrCorruptEventStream)
		} else {
			if err := foldEvent(state, event); err != nil {
				return nil, fmt.Errorf("%w: revision %d: %v", ErrCorruptEventStream, expectedRevision, err)
			}
		}
		state.Revision = expectedRevision
		if event.LifecycleEpoch != state.LifecycleEpoch {
			return nil, fmt.Errorf("%w: lifecycle epoch mismatch at revision %d", ErrCorruptEventStream, expectedRevision)
		}
	}
	return state, nil
}

func foldEvent(state *AggregateState, event DomainEvent) error {
	if state.Continuity != nil {
		switch event.EventType {
		case "tekroo.event.system.quiescence-requested", "tekroo.event.system.suspended", "tekroo.event.system.unexpected-outage-recorded", "tekroo.event.system.reconciliation-started", "tekroo.event.system.resumed":
			next, valid := ApplyContinuityEvent(*state.Continuity, event)
			if !valid {
				return errors.New("illegal continuity transition")
			}
			state.Continuity = &next
		}
	}
	if state.Interaction != nil {
		switch event.EventType {
		case "tekroo.event.human-interaction.delivery-recorded", "tekroo.event.human-interaction.response-recorded", "tekroo.event.human-interaction.closed", "tekroo.event.human-interaction.expired":
			next, valid := ApplyHumanInteractionEvent(*state.Interaction, event)
			if !valid {
				return errors.New("illegal human interaction transition")
			}
			state.Interaction = &next
		}
	}
	if event.EventType == "tekroo.event.human-participant.profile-bound" {
		participant, err := HumanParticipantFromPayload(event.Payload, event.AggregateRevision)
		if err != nil || state.Participant == nil || participant.Participant != state.Participant.Participant {
			return errors.New("illegal human participant profile transition")
		}
		state.Participant = &participant
	}
	if event.EventType == "tekroo.event.human-participant.revoked" {
		if state.Participant == nil || !state.Participant.Active {
			return errors.New("illegal human participant revocation")
		}
		participant := state.Participant.Clone()
		participant.Active = false
		participant.Revision = event.AggregateRevision
		state.Participant = &participant
	}
	if event.EventType == "tekroo.event.system.operator-role-bound" {
		profile, err := OperatorRoleFromPayload(event.Payload)
		if err != nil || state.OperatorRole == nil || profile.ReplacesBindingID == nil || *profile.ReplacesBindingID != state.OperatorRole.BindingID {
			return errors.New("illegal operator role replacement")
		}
		state.OperatorRole = &profile
	}
	if event.EventType == "tekroo.event.system.continuity-configured" {
		continuity, err := TeamContinuityFromConfigurePayload(event.Payload, event.EventID, event.AggregateRevision)
		if err != nil || state.Continuity != nil && state.Continuity.ControlState != ContinuityActive {
			return errors.New("illegal continuity reconfiguration")
		}
		state.Continuity = &continuity
	}
	if event.EventType == "tekroo.event.work.reopened" {
		object, err := decodePayloadObject(event.Payload)
		if err != nil {
			return errors.New("invalid reopening payload")
		}
		scopeRevision, ok := uint64Field(object, "new_scope_revision")
		if !ok || scopeRevision <= state.ScopeRevision {
			return errors.New("invalid scope revision")
		}
		state.ScopeRevision = scopeRevision
	}
	if action, ok := eventLifecycleAction(event.EventType); ok {
		model := LifecycleStory
		if state.Kind == AggregateTask {
			model = LifecycleTask
		}
		result := ApplyLifecycleActions(model, LifecycleState{Phase: state.Phase, Condition: state.Condition, LifecycleEpoch: state.LifecycleEpoch}, []LifecycleAction{action})
		if result.Rejected != nil {
			return errors.New("illegal lifecycle transition")
		}
		state.Phase = result.Phase
		state.Condition = result.Condition
		state.LifecycleEpoch = result.LifecycleEpoch
	}
	commandType := eventOwnershipCommand(event.EventType)
	if commandType != "" {
		command := KernelCommand{CommandType: commandType, Payload: event.Payload}
		if outcome, _ := applyOwnership(command, state, event.EventID); outcome != OutcomeApplied {
			return errors.New("illegal ownership transition")
		}
	}
	return nil
}

func eventLifecycleAction(eventType string) (LifecycleAction, bool) {
	switch eventType {
	case "tekroo.event.story.authorized":
		return ActionAuthorize, true
	case "tekroo.event.story.planning-started":
		return ActionBeginPlanning, true
	case "tekroo.event.story.activated", "tekroo.event.task.activated":
		return ActionActivate, true
	case "tekroo.event.story.completed", "tekroo.event.task.completed":
		return ActionComplete, true
	case "tekroo.event.story.accepted":
		return ActionAccept, true
	case "tekroo.event.task.readied":
		return ActionMarkReady, true
	case "tekroo.event.work.blocked":
		return ActionBlock, true
	case "tekroo.event.work.unblocked":
		return ActionUnblock, true
	case "tekroo.event.work.reopened":
		return ActionReopen, true
	case "tekroo.event.work.closed":
		return ActionClose, true
	default:
		return "", false
	}
}

func eventOwnershipCommand(eventType string) string {
	switch eventType {
	case "tekroo.event.task.ownership-acquired":
		return "tekroo.command.task.acquire-ownership"
	case "tekroo.event.task.ownership-released":
		return "tekroo.command.task.release-ownership"
	case "tekroo.event.task.handed-off":
		return "tekroo.command.task.handoff"
	case "tekroo.event.task.force-reassigned":
		return "tekroo.command.task.force-reassign"
	default:
		return ""
	}
}
