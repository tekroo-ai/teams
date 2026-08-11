package application

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

var ErrInvalidEscalationCoordination = errors.New("invalid escalation coordination request")

type EscalationCommandIdentity struct {
	CommandID      kernel.UUIDv7
	CorrelationID  kernel.UUIDv7
	IdempotencyKey string
}

type EscalationOpeningRequest struct {
	Input             kernel.EscalationOpeningInput
	Identity          EscalationCommandIdentity
	PolicyAuthority   kernel.PrincipalRef
	PolicyRevision    uint64
	CatalogueRevision uint64
	Provenance        kernel.ProvenanceBasis
}

type EscalationOpeningResult struct {
	Decision kernel.EscalationOpeningDecision
	Receipt  *kernel.CommandReceipt
}

type EscalationResolutionRequest struct {
	Input             kernel.EscalationResolutionInput
	Identity          EscalationCommandIdentity
	ActorFQN          *kernel.ActorFQN
	Execution         *kernel.ExecutionTuple
	PolicyRevision    uint64
	CatalogueRevision uint64
	Provenance        kernel.ProvenanceBasis
}

type EscalationResolutionResult struct {
	Decision kernel.EscalationResolutionDecision
	Receipt  *kernel.CommandReceipt
}

type EscalationCoordinator struct {
	commands ExecutionCommandService
}

func NewEscalationCoordinator(commands ExecutionCommandService) (*EscalationCoordinator, error) {
	if commands == nil {
		return nil, ErrInvalidConfiguration
	}
	return &EscalationCoordinator{commands: commands}, nil
}

func (coordinator *EscalationCoordinator) Open(ctx context.Context, request EscalationOpeningRequest) (EscalationOpeningResult, error) {
	decision := kernel.PlanEscalationOpening(request.Input)
	result := EscalationOpeningResult{Decision: decision}
	if decision.Status != kernel.EscalationPlanReady {
		return result, nil
	}
	if !validEscalationIdentity(request.Identity) || request.PolicyAuthority.Kind != kernel.PrincipalPolicy || !request.PolicyAuthority.Valid() || request.PolicyRevision == 0 || request.PolicyRevision != decision.Escalation.PolicyRevision || request.CatalogueRevision != kernel.CatalogueRevision || !request.Provenance.Valid() {
		return result, ErrInvalidEscalationCoordination
	}
	payload, err := json.Marshal(struct {
		EscalationID            kernel.UUIDv7            `json:"escalation_id"`
		SubjectKind             kernel.AggregateKind     `json:"subject_kind"`
		SubjectID               kernel.UUIDv7            `json:"subject_id"`
		SubjectLifecycleEpoch   uint64                   `json:"subject_lifecycle_epoch"`
		ExpectedSubjectRevision uint64                   `json:"expected_subject_revision"`
		Trigger                 kernel.EscalationTrigger `json:"trigger"`
		ConditionDigest         kernel.Digest            `json:"triggering_condition_digest"`
		Adjudicator             kernel.PrincipalRef      `json:"adjudicator"`
		TimeoutPolicy           kernel.PrincipalRef      `json:"timeout_policy"`
		ResolutionOwnerFQN      kernel.ActorFQN          `json:"resolution_owner_fqn"`
		DeadlineAt              time.Time                `json:"deadline_at"`
		ResolutionRoundLimit    uint64                   `json:"resolution_round_limit"`
		RouteLimit              uint64                   `json:"route_limit"`
		PolicyRevision          uint64                   `json:"escalation_policy_revision"`
		CausalPathEventIDs      []kernel.UUIDv7          `json:"causal_path_event_ids"`
		UnresolvedQuestion      string                   `json:"unresolved_question"`
		EvidenceIDs             []kernel.UUIDv7          `json:"evidence_ids"`
	}{
		decision.Escalation.EscalationID, decision.Escalation.Subject.Kind, decision.Escalation.Subject.ID,
		decision.Escalation.SubjectLifecycleEpoch, decision.Escalation.ExpectedSubjectRevision,
		decision.Escalation.Trigger, decision.Escalation.ConditionDigest, decision.Escalation.Adjudicator,
		decision.Escalation.TimeoutPolicy, decision.Escalation.ResolutionOwnerFQN, decision.Escalation.DeadlineAt,
		decision.Escalation.ResolutionRoundLimit, decision.Escalation.RouteLimit, decision.Escalation.PolicyRevision,
		decision.Escalation.CausalPathEventIDs, decision.Escalation.UnresolvedQuestion, decision.Escalation.EvidenceIDs,
	})
	if err != nil {
		return result, ErrInvalidEscalationCoordination
	}
	parents := make([]kernel.DagParent, len(decision.Escalation.CausalPathEventIDs))
	for index, eventID := range decision.Escalation.CausalPathEventIDs {
		parents[index] = kernel.DagParent{ParentEventID: eventID, EdgeKind: kernel.EdgeCausal}
	}
	command := kernel.KernelCommand{
		ContractManifest: kernel.ContractIdentity, CommandID: request.Identity.CommandID,
		CommandType: "tekroo.command.escalation.open", CommandVersion: kernel.SchemaVersion,
		Target: kernel.AggregateRef{Kind: kernel.AggregateEscalation, ID: decision.Escalation.EscalationID}, Authority: request.PolicyAuthority,
		ExpectedRevision:       kernel.MustNotExist(),
		Preconditions:          []kernel.AggregatePrecondition{{Aggregate: decision.Escalation.Subject, Expected: kernel.NewExpectedRevision(decision.Escalation.ExpectedSubjectRevision)}},
		ExpectedPolicyRevision: request.PolicyRevision, ExpectedCatalogueRevision: request.CatalogueRevision,
		IdempotencyKey: request.Identity.IdempotencyKey, CorrelationID: request.Identity.CorrelationID,
		Causation: parents, Payload: payload, EvidenceRefs: append([]kernel.EvidenceRef(nil), decision.EvidenceRefs...),
	}
	receipt, err := coordinator.commands.Handle(ctx, command, request.Provenance)
	result.Receipt = &receipt
	if err != nil || receipt.OutcomeCode != kernel.OutcomeApplied {
		return result, err
	}
	if len(receipt.EventIDs) != 1 {
		return result, ErrInvalidEscalationCoordination
	}
	return result, nil
}

func (coordinator *EscalationCoordinator) Resolve(ctx context.Context, request EscalationResolutionRequest) (EscalationResolutionResult, error) {
	decision := kernel.PlanEscalationResolution(request.Input)
	result := EscalationResolutionResult{Decision: decision}
	if decision.Status != kernel.EscalationPlanReady {
		return result, nil
	}
	if !validEscalationIdentity(request.Identity) || request.PolicyRevision == 0 || request.CatalogueRevision != kernel.CatalogueRevision || !request.Provenance.Valid() || !validEscalationActorAttribution(decision.Transition.Authority, request.ActorFQN, request.Execution) {
		return result, ErrInvalidEscalationCoordination
	}
	payload, err := json.Marshal(struct {
		EscalationID          kernel.UUIDv7               `json:"escalation_id"`
		SubjectKind           kernel.AggregateKind        `json:"subject_kind"`
		SubjectID             kernel.UUIDv7               `json:"subject_id"`
		SubjectLifecycleEpoch uint64                      `json:"subject_lifecycle_epoch"`
		ExpectedRevision      uint64                      `json:"expected_escalation_revision"`
		SourceRole            kernel.EscalationSourceRole `json:"source_role"`
		Round                 uint64                      `json:"round"`
		Outcome               kernel.EscalationOutcome    `json:"outcome"`
		Reasons               []string                    `json:"reasons"`
		EvidenceIDs           []kernel.UUIDv7             `json:"evidence_ids"`
		DecidedAt             time.Time                   `json:"decided_at"`
	}{
		decision.Transition.EscalationID, decision.Transition.Subject.Kind, decision.Transition.Subject.ID,
		decision.Transition.SubjectLifecycleEpoch, decision.Transition.ExpectedRevision, decision.Transition.SourceRole,
		decision.Transition.Round, decision.Transition.Outcome, decision.Transition.Reasons,
		decision.Transition.EvidenceIDs, decision.Transition.DecidedAt,
	})
	if err != nil {
		return result, ErrInvalidEscalationCoordination
	}
	command := kernel.KernelCommand{
		ContractManifest: kernel.ContractIdentity, CommandID: request.Identity.CommandID,
		CommandType: "tekroo.command.escalation.resolve", CommandVersion: kernel.SchemaVersion,
		Target: kernel.AggregateRef{Kind: kernel.AggregateEscalation, ID: decision.Escalation.EscalationID}, Authority: decision.Transition.Authority,
		ActorFQN: request.ActorFQN, Execution: request.Execution,
		ExpectedRevision:       kernel.NewExpectedRevision(decision.Escalation.Revision),
		Preconditions:          []kernel.AggregatePrecondition{{Aggregate: decision.Escalation.Subject, Expected: kernel.NewExpectedRevision(decision.Subject.Revision)}},
		ExpectedPolicyRevision: request.PolicyRevision, ExpectedCatalogueRevision: request.CatalogueRevision,
		IdempotencyKey: request.Identity.IdempotencyKey, CorrelationID: request.Identity.CorrelationID,
		Causation: append([]kernel.DagParent(nil), decision.Parents...), Payload: payload,
		EvidenceRefs: append([]kernel.EvidenceRef(nil), decision.EvidenceRefs...),
	}
	receipt, err := coordinator.commands.Handle(ctx, command, request.Provenance)
	result.Receipt = &receipt
	if err != nil || receipt.OutcomeCode != kernel.OutcomeApplied {
		return result, err
	}
	if len(receipt.EventIDs) != 1 {
		return result, ErrInvalidEscalationCoordination
	}
	return result, nil
}

func validEscalationIdentity(identity EscalationCommandIdentity) bool {
	return identity.CommandID.Valid() && identity.CorrelationID.Valid() && identity.IdempotencyKey != "" && len(identity.IdempotencyKey) <= 256
}

func validEscalationActorAttribution(authority kernel.PrincipalRef, actor *kernel.ActorFQN, execution *kernel.ExecutionTuple) bool {
	if !authority.Valid() {
		return false
	}
	if authority.Kind != kernel.PrincipalActor {
		return actor == nil && execution == nil
	}
	if actor == nil || !actor.Valid() || authority.ID != string(*actor) {
		return false
	}
	return execution == nil || execution.Valid()
}
