package application

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/tekroo-ai/teams/kernel"
)

var ErrInvalidAssignmentCoordination = errors.New("invalid assignment coordination request")

type AssignmentCommandIdentity struct {
	ReadinessCommandID     kernel.UUIDv7
	AuthorizationCommandID kernel.UUIDv7
	DispatchCommandID      kernel.UUIDv7
	CorrelationID          kernel.UUIDv7
	ReadinessKey           string
	AuthorizationKey       string
	DispatchKey            string
}

type AssignmentCoordinationRequest struct {
	Input             kernel.AssignmentInput
	PowerEpoch        uint64
	Identity          AssignmentCommandIdentity
	PolicyAuthority   kernel.PrincipalRef
	PolicyRevision    uint64
	CatalogueRevision uint64
	Provenance        kernel.ProvenanceBasis
}

type AssignmentCoordinationResult struct {
	Decision             kernel.AssignmentDecision
	ReadinessReceipt     *kernel.CommandReceipt
	AuthorizationReceipt *kernel.CommandReceipt
	DispatchReceipt      *kernel.CommandReceipt
}

type AssignmentCoordinator struct {
	commands  ExecutionCommandService
	admission ContinuityAdmissionService
}

func NewAssignmentCoordinator(commands ExecutionCommandService, admission ContinuityAdmissionService) (*AssignmentCoordinator, error) {
	if commands == nil || admission == nil {
		return nil, ErrInvalidConfiguration
	}
	return &AssignmentCoordinator{commands: commands, admission: admission}, nil
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
	admission, err := coordinator.admission.Admit(ctx, kernel.ContinuityDispatch, request.PowerEpoch)
	if err != nil {
		return result, err
	}
	if !admission.Accepted {
		return result, AdmissionDeniedError{Reason: admission.Reason}
	}
	ctx = WithPowerFenceContext(ctx, PowerFenceContext{Operation: OperationAssignment, PowerEpoch: request.PowerEpoch})
	revision := decision.Task.Revision
	parents := append([]kernel.DagParent(nil), decision.ReadinessParents...)
	if decision.NeedsReadiness {
		payload, _ := json.Marshal(struct {
			DependencyEventIDs      []kernel.UUIDv7 `json:"dependency_event_ids"`
			ReadinessPolicyRevision uint64          `json:"readiness_policy_revision"`
		}{decision.DependencyEventIDs, decision.ReadinessPolicyRevision})
		command := assignmentCommand(request, request.Identity.ReadinessCommandID, request.Identity.ReadinessKey, "tekroo.command.task.mark-ready", request.PolicyAuthority, nil, nil, revision, parents, payload, nil)
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

	evidenceIDs := make([]kernel.UUIDv7, len(decision.SelectionEvidence))
	for index, reference := range decision.SelectionEvidence {
		evidenceIDs[index] = reference.EvidenceID
	}
	qualification := *decision.Qualification
	authorizationPayload, _ := json.Marshal(struct {
		AssignmentID          kernel.UUIDv7             `json:"assignment_id"`
		TaskID                kernel.UUIDv7             `json:"task_id"`
		ExpectedTaskRevision  uint64                    `json:"expected_task_revision"`
		WorkProfile           kernel.WorkProfileBinding `json:"work_profile"`
		RequiredDecisionRoute kernel.DecisionRoute      `json:"required_decision_route"`
		SelectedDecisionRoute kernel.DecisionRoute      `json:"selected_decision_route"`
		SelectedActorFQN      kernel.ActorFQN           `json:"selected_actor_fqn"`
		SelectedExecutionID   kernel.UUIDv7             `json:"selected_execution_id"`
		SelectedFencingEpoch  uint64                    `json:"selected_fencing_epoch"`
		ModelProfileDigest    kernel.Digest             `json:"model_profile_digest"`
		RuntimeIdentityDigest kernel.Digest             `json:"runtime_identity_digest"`
		Qualification         struct {
			QualificationID           kernel.UUIDv7        `json:"qualification_id"`
			QualificationDigest       kernel.Digest        `json:"qualification_digest"`
			QualificationCorpusDigest kernel.Digest        `json:"qualification_corpus_digest"`
			ModelProfileDigest        kernel.Digest        `json:"model_profile_digest"`
			DecisionRoute             kernel.DecisionRoute `json:"decision_route"`
			QualifiedRole             string               `json:"qualified_role"`
			Status                    string               `json:"status"`
			ObservedAt                string               `json:"observed_at"`
		} `json:"qualification"`
		SelectionPolicyRevision uint64                        `json:"selection_policy_revision"`
		SelectionPolicyDigest   kernel.Digest                 `json:"selection_policy_digest"`
		HardConstraintResults   []kernel.HardConstraintResult `json:"hard_constraint_results"`
		SelectionReasons        []string                      `json:"selection_reasons"`
		EvidenceIDs             []kernel.UUIDv7               `json:"evidence_ids"`
	}{
		AssignmentID: request.Identity.AuthorizationCommandID, TaskID: decision.Task.ID, ExpectedTaskRevision: revision,
		WorkProfile: decision.WorkProfile, RequiredDecisionRoute: decision.RequiredDecisionRoute, SelectedDecisionRoute: decision.SelectedDecisionRoute,
		SelectedActorFQN: *decision.ActorFQN, SelectedExecutionID: decision.Execution.ExecutionID, SelectedFencingEpoch: decision.Execution.FencingEpoch,
		ModelProfileDigest: decision.ModelProfileDigest, RuntimeIdentityDigest: decision.RuntimeIdentityDigest,
		Qualification: struct {
			QualificationID           kernel.UUIDv7        `json:"qualification_id"`
			QualificationDigest       kernel.Digest        `json:"qualification_digest"`
			QualificationCorpusDigest kernel.Digest        `json:"qualification_corpus_digest"`
			ModelProfileDigest        kernel.Digest        `json:"model_profile_digest"`
			DecisionRoute             kernel.DecisionRoute `json:"decision_route"`
			QualifiedRole             string               `json:"qualified_role"`
			Status                    string               `json:"status"`
			ObservedAt                string               `json:"observed_at"`
		}{qualification.QualificationID, qualification.QualificationDigest, qualification.QualificationCorpusDigest, qualification.ModelProfileDigest, qualification.DecisionRoute, qualification.QualifiedRole, string(qualification.Status), qualification.ObservedAt.Format("2006-01-02T15:04:05Z07:00")},
		SelectionPolicyRevision: decision.SelectionPolicyRevision, SelectionPolicyDigest: decision.SelectionPolicyDigest,
		HardConstraintResults: decision.HardConstraints, SelectionReasons: decision.SelectionReasons, EvidenceIDs: evidenceIDs,
	})
	authorization := assignmentCommand(request, request.Identity.AuthorizationCommandID, request.Identity.AuthorizationKey, "tekroo.command.task.authorize-qualified-assignment", request.PolicyAuthority, nil, nil, revision, parents, authorizationPayload, decision.SelectionEvidence)
	authorizationReceipt, err := coordinator.commands.Handle(ctx, authorization, request.Provenance)
	result.AuthorizationReceipt = &authorizationReceipt
	if err != nil || authorizationReceipt.OutcomeCode != kernel.OutcomeApplied {
		return result, err
	}
	if len(authorizationReceipt.EventIDs) != 1 {
		return result, ErrInvalidAssignmentCoordination
	}
	parents = []kernel.DagParent{{ParentEventID: authorizationReceipt.EventIDs[0], EdgeKind: kernel.EdgeCausal}}
	revision++

	dispatchPayload, _ := json.Marshal(struct {
		Destination kernel.ActorFQN `json:"destination"`
		RoutingMode string          `json:"routing_mode"`
	}{*decision.ActorFQN, "EXACT"})
	dispatch := assignmentCommand(request, request.Identity.DispatchCommandID, request.Identity.DispatchKey, "tekroo.command.task.dispatch", request.PolicyAuthority, nil, nil, revision, parents, dispatchPayload, nil)
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

func assignmentCommand(request AssignmentCoordinationRequest, commandID kernel.UUIDv7, key, commandType string, authority kernel.PrincipalRef, actor *kernel.ActorFQN, execution *kernel.ExecutionTuple, revision uint64, parents []kernel.DagParent, payload json.RawMessage, evidence []kernel.EvidenceRef) kernel.KernelCommand {
	epoch := request.Input.Task.LifecycleEpoch
	return kernel.KernelCommand{
		ContractManifest: kernel.ContractIdentity, CommandID: commandID, CommandType: commandType, CommandVersion: kernel.SchemaVersion,
		Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: request.Input.Task.ID}, Authority: authority,
		ActorFQN: actor, Execution: execution, ExpectedRevision: kernel.NewExpectedRevision(revision), ExpectedLifecycleEpoch: &epoch,
		ExpectedPolicyRevision: request.PolicyRevision, ExpectedCatalogueRevision: request.CatalogueRevision,
		IdempotencyKey: key, CorrelationID: request.Identity.CorrelationID, Causation: append([]kernel.DagParent(nil), parents...), Payload: payload, EvidenceRefs: append([]kernel.EvidenceRef(nil), evidence...),
	}
}

func validateAssignmentCoordination(request AssignmentCoordinationRequest, decision kernel.AssignmentDecision) error {
	identity := request.Identity
	if decision.Status != kernel.AssignmentReady || decision.ActorFQN == nil || decision.Execution == nil || decision.Qualification == nil || !decision.ActorFQN.Valid() || !decision.Execution.Valid() || !decision.Qualification.Valid() || !decision.WorkProfile.Valid() || !decision.ModelProfileDigest.Valid() || !decision.RuntimeIdentityDigest.Valid() || decision.Task.Kind != kernel.AggregateTask || !decision.Task.ID.Valid() || decision.Task.Revision == 0 || decision.Task.LifecycleEpoch == 0 || decision.Task.ScopeRevision == 0 || request.PowerEpoch == 0 || request.PolicyAuthority.Kind != kernel.PrincipalPolicy || !request.PolicyAuthority.Valid() || request.PolicyRevision == 0 || request.CatalogueRevision != kernel.CatalogueRevision || !request.Provenance.Valid() {
		return ErrInvalidAssignmentCoordination
	}
	if !identity.AuthorizationCommandID.Valid() || identity.AuthorizationKey == "" || !identity.DispatchCommandID.Valid() || !identity.CorrelationID.Valid() || identity.DispatchKey == "" {
		return ErrInvalidAssignmentCoordination
	}
	if decision.NeedsReadiness && (!identity.ReadinessCommandID.Valid() || identity.ReadinessKey == "") {
		return ErrInvalidAssignmentCoordination
	}
	if identity.ReadinessCommandID == identity.AuthorizationCommandID || identity.ReadinessCommandID == identity.DispatchCommandID || identity.AuthorizationCommandID == identity.DispatchCommandID {
		return ErrInvalidAssignmentCoordination
	}
	return nil
}
