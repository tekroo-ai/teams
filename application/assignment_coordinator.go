package application

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/tekroo-ai/teams/kernel"
)

var ErrInvalidAssignmentCoordination = errors.New("invalid assignment coordination request")

type AssignmentCommandIdentity struct {
	ReadinessCommandID kernel.UUIDv7
	DispatchCommandID  kernel.UUIDv7
	CorrelationID      kernel.UUIDv7
	ReadinessKey       string
	DispatchKey        string
}

type AssignmentCoordinationRequest struct {
	Input             kernel.AssignmentInput
	Identity          AssignmentCommandIdentity
	PolicyAuthority   kernel.PrincipalRef
	PolicyRevision    uint64
	CatalogueRevision uint64
	Provenance        kernel.ProvenanceBasis
}

type AssignmentCoordinationResult struct {
	Decision         kernel.AssignmentDecision
	ReadinessReceipt *kernel.CommandReceipt
	DispatchReceipt  *kernel.CommandReceipt
}

type AssignmentCoordinator struct {
	commands ExecutionCommandService
}

func NewAssignmentCoordinator(commands ExecutionCommandService) (*AssignmentCoordinator, error) {
	if commands == nil {
		return nil, ErrInvalidConfiguration
	}
	return &AssignmentCoordinator{commands: commands}, nil
}

func (coordinator *AssignmentCoordinator) Coordinate(ctx context.Context, request AssignmentCoordinationRequest) (AssignmentCoordinationResult, error) {
	decision := kernel.PlanAssignment(request.Input)
	result := AssignmentCoordinationResult{Decision: decision}
	if decision.Status != kernel.AssignmentReady {
		return result, nil
	}
	if err := validateAssignmentCoordination(request, decision); err != nil {
		return result, err
	}
	revision := decision.Task.Revision
	parents := append([]kernel.DagParent(nil), decision.ReadinessParents...)
	if decision.NeedsReadiness {
		payload, _ := json.Marshal(struct {
			DependencyEventIDs      []kernel.UUIDv7 `json:"dependency_event_ids"`
			ReadinessPolicyRevision uint64          `json:"readiness_policy_revision"`
		}{decision.DependencyEventIDs, decision.ReadinessPolicyRevision})
		command := assignmentCommand(request, request.Identity.ReadinessCommandID, request.Identity.ReadinessKey, "tekroo.command.task.mark-ready", request.PolicyAuthority, nil, nil, revision, parents, payload)
		receipt, err := coordinator.commands.Handle(ctx, command, request.Provenance)
		result.ReadinessReceipt = &receipt
		if err != nil || receipt.OutcomeCode != kernel.OutcomeApplied {
			return result, err
		}
		if len(receipt.EventIDs) != 1 {
			return result, ErrInvalidAssignmentCoordination
		}
		parents = []kernel.DagParent{{ParentEventID: receipt.EventIDs[0], EdgeKind: kernel.EdgeCausal}}
		revision++
	}

	dispatchPayload, _ := json.Marshal(struct {
		Destination kernel.ActorFQN `json:"destination"`
		RoutingMode string          `json:"routing_mode"`
	}{*decision.ActorFQN, "EXACT"})
	dispatch := assignmentCommand(request, request.Identity.DispatchCommandID, request.Identity.DispatchKey, "tekroo.command.task.dispatch", request.PolicyAuthority, nil, nil, revision, parents, dispatchPayload)
	dispatchReceipt, err := coordinator.commands.Handle(ctx, dispatch, request.Provenance)
	result.DispatchReceipt = &dispatchReceipt
	if err != nil || dispatchReceipt.OutcomeCode != kernel.OutcomeApplied {
		return result, err
	}
	if len(dispatchReceipt.EventIDs) != 1 {
		return result, ErrInvalidAssignmentCoordination
	}
	return result, err
}

func assignmentCommand(request AssignmentCoordinationRequest, commandID kernel.UUIDv7, key, commandType string, authority kernel.PrincipalRef, actor *kernel.ActorFQN, execution *kernel.ExecutionTuple, revision uint64, parents []kernel.DagParent, payload json.RawMessage) kernel.KernelCommand {
	epoch := request.Input.Task.LifecycleEpoch
	return kernel.KernelCommand{
		ContractManifest: kernel.ContractIdentity, CommandID: commandID, CommandType: commandType, CommandVersion: kernel.SchemaVersion,
		Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: request.Input.Task.ID}, Authority: authority,
		ActorFQN: actor, Execution: execution, ExpectedRevision: kernel.NewExpectedRevision(revision), ExpectedLifecycleEpoch: &epoch,
		ExpectedPolicyRevision: request.PolicyRevision, ExpectedCatalogueRevision: request.CatalogueRevision,
		IdempotencyKey: key, CorrelationID: request.Identity.CorrelationID, Causation: append([]kernel.DagParent(nil), parents...), Payload: payload,
	}
}

func validateAssignmentCoordination(request AssignmentCoordinationRequest, decision kernel.AssignmentDecision) error {
	identity := request.Identity
	if decision.Status != kernel.AssignmentReady || decision.ActorFQN == nil || decision.Execution == nil || !decision.ActorFQN.Valid() || !decision.Execution.Valid() || decision.Task.Kind != kernel.AggregateTask || !decision.Task.ID.Valid() || decision.Task.Revision == 0 || decision.Task.LifecycleEpoch == 0 || request.PolicyAuthority.Kind != kernel.PrincipalPolicy || !request.PolicyAuthority.Valid() || request.PolicyRevision == 0 || request.CatalogueRevision != kernel.CatalogueRevision || !request.Provenance.Valid() {
		return ErrInvalidAssignmentCoordination
	}
	if !identity.DispatchCommandID.Valid() || !identity.CorrelationID.Valid() || identity.DispatchKey == "" {
		return ErrInvalidAssignmentCoordination
	}
	if decision.NeedsReadiness && (!identity.ReadinessCommandID.Valid() || identity.ReadinessKey == "") {
		return ErrInvalidAssignmentCoordination
	}
	if identity.ReadinessCommandID == identity.DispatchCommandID {
		return ErrInvalidAssignmentCoordination
	}
	return nil
}
