package organization

import (
	"context"
	"errors"

	"github.com/tekroo-ai/teams/kernel"
)

var ErrWorkflowAdmissionUnavailable = errors.New("workflow admission unavailable")

type WorkflowAdmissionActorSource interface {
	Status(context.Context, kernel.ActorFQN) (RoleInstanceState, bool, error)
	EligibleForWorkflowStage(kernel.ActorFQN, kernel.WorkflowStageDefinition) bool
}

type WorkflowAdmissionCoordinator struct {
	store   WorkflowMessageAdmissionStore
	library *WorkflowLibrary
	actors  WorkflowAdmissionActorSource
	clock   kernel.Clock
	ids     kernel.IDSource
}

func NewWorkflowAdmissionCoordinator(store WorkflowMessageAdmissionStore, library *WorkflowLibrary, actors WorkflowAdmissionActorSource, clock kernel.Clock, ids kernel.IDSource) (*WorkflowAdmissionCoordinator, error) {
	if store == nil || library == nil || actors == nil || clock == nil || ids == nil {
		return nil, ErrWorkflowAdmissionUnavailable
	}
	return &WorkflowAdmissionCoordinator{store: store, library: library, actors: actors, clock: clock, ids: ids}, nil
}

// Admit handles only workflow-linked REQUEST and HANDOFF messages. A false
// handled result means the message is informational or belongs to the legacy
// compatibility path and must remain deliverable without creating work.
func (coordinator *WorkflowAdmissionCoordinator) Admit(ctx context.Context, message OrganizationalMessage) (kernel.WorkAdmissionResult, bool, bool, error) {
	if coordinator == nil || message.Validate() != nil {
		return kernel.WorkAdmissionResult{}, false, false, ErrWorkflowAdmissionUnavailable
	}
	if !MessageCreatesWorkflowWork(message) {
		return kernel.WorkAdmissionResult{}, false, false, nil
	}
	if message.Work.WorkflowInstanceID == nil {
		if _, found, err := coordinator.store.ReadWorkflowInstanceByBudget(ctx, message.Flow.BudgetAccountID); err != nil {
			return kernel.WorkAdmissionResult{}, true, false, err
		} else if found {
			// The compatibility producer has committed the next message, but has
			// not yet atomically bound its workflow node. Keep it pending until
			// the binding update arrives; it must never bypass admission.
			return kernel.WorkAdmissionResult{}, true, false, nil
		}
		if _, err := coordinator.library.LookupTrigger(message.Type); err == nil {
			// The first trigger may be observed in the short interval before its
			// workflow instance is bound. It follows the same pending path.
			return kernel.WorkAdmissionResult{}, true, false, nil
		} else if !errors.Is(err, ErrWorkflowDefinitionNotFound) {
			return kernel.WorkAdmissionResult{}, true, false, err
		}
		return kernel.WorkAdmissionResult{}, false, false, nil
	}
	if prior, found, err := coordinator.store.WorkflowAdmission(ctx, message.ID); err != nil {
		return kernel.WorkAdmissionResult{}, true, false, err
	} else if found {
		return prior, true, true, nil
	}
	instance, found, err := coordinator.store.ReadWorkflowInstance(ctx, *message.Work.WorkflowInstanceID)
	if err != nil || !found {
		return kernel.WorkAdmissionResult{}, true, false, errors.Join(ErrWorkflowAdmissionUnavailable, err)
	}
	definition, err := coordinator.library.Lookup(instance.DefinitionName, instance.DefinitionVersion)
	if err != nil || definition.ContentDigest != instance.DefinitionDigest {
		return kernel.WorkAdmissionResult{}, true, false, errors.Join(ErrWorkflowAdmissionUnavailable, err)
	}
	stage, found := workflowStage(definition, message.Work.WorkflowStageID)
	if !found || instance.LastEventID == nil {
		return kernel.WorkAdmissionResult{}, true, false, ErrWorkflowAdmissionUnavailable
	}
	role, found, err := coordinator.actors.Status(ctx, message.Recipient)
	if err != nil || !found || role.Status != RoleIdle || !role.Execution.Valid() {
		return kernel.WorkAdmissionResult{}, true, false, errors.Join(ErrWorkflowAdmissionUnavailable, err)
	}
	proposalID, err := coordinator.ids.Next()
	if err != nil {
		return kernel.WorkAdmissionResult{}, true, false, err
	}
	eventID, err := coordinator.ids.Next()
	if err != nil {
		return kernel.WorkAdmissionResult{}, true, false, err
	}
	invocationID, err := coordinator.ids.Next()
	if err != nil {
		return kernel.WorkAdmissionResult{}, true, false, err
	}
	intentID, err := coordinator.ids.Next()
	if err != nil {
		return kernel.WorkAdmissionResult{}, true, false, err
	}
	now := coordinator.clock.Now().UTC()
	proposal, err := WorkProposalFromMessage(message, proposalID, role.Execution, []kernel.UUIDv7{*instance.LastEventID}, workflowNodeInputEvidence(instance, message.Work.DAGNodeID), now)
	if err != nil {
		return kernel.WorkAdmissionResult{}, true, false, err
	}
	request := WorkflowAdmissionRequest{
		Message: message, Definition: definition, Proposal: proposal,
		Facts:                  kernel.WorkflowAdmissionFacts{ActorEligible: coordinator.actors.EligibleForWorkflowStage(message.Recipient, stage), BudgetAvailable: true, CausationValid: true},
		RecordedEventID:        eventID,
		AuthorizedInvocationID: invocationID,
		IntentID:               intentID,
		RecordedAt:             now,
	}
	result, replayed, err := coordinator.store.AdmitWorkflowMessage(ctx, request)
	return result, true, replayed, err
}

func workflowStage(definition kernel.WorkflowDefinition, stageID string) (kernel.WorkflowStageDefinition, bool) {
	for _, stage := range definition.Stages {
		if stage.StageID == stageID {
			return stage, true
		}
	}
	return kernel.WorkflowStageDefinition{}, false
}

func workflowNodeInputEvidence(instance kernel.WorkflowInstance, nodeID kernel.UUIDv7) []kernel.UUIDv7 {
	for _, node := range instance.Nodes {
		if node.NodeID == nodeID {
			return append([]kernel.UUIDv7(nil), node.InputEvidenceIDs...)
		}
	}
	return nil
}
