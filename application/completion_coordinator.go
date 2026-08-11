package application

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/tekroo-ai/teams/kernel"
)

var ErrInvalidCompletionCoordination = errors.New("invalid completion coordination request")

type CompletionCommandIdentity struct {
	CommandID      kernel.UUIDv7
	CorrelationID  kernel.UUIDv7
	IdempotencyKey string
}

type CompletionCoordinationRequest struct {
	Input             kernel.CompletionInput
	Identity          CompletionCommandIdentity
	Authority         kernel.PrincipalRef
	ActorFQN          *kernel.ActorFQN
	Execution         *kernel.ExecutionTuple
	PolicyRevision    uint64
	CatalogueRevision uint64
	Provenance        kernel.ProvenanceBasis
}

type CompletionCoordinationResult struct {
	Decision kernel.CompletionDecision
	Receipt  *kernel.CommandReceipt
}

type ReopeningCoordinationRequest struct {
	Input             kernel.ReopeningInput
	Identity          CompletionCommandIdentity
	Authority         kernel.PrincipalRef
	PolicyRevision    uint64
	CatalogueRevision uint64
	Provenance        kernel.ProvenanceBasis
}

type ReopeningCoordinationResult struct {
	Decision kernel.ReopeningDecision
	Receipt  *kernel.CommandReceipt
}

type CompletionCoordinator struct {
	commands ExecutionCommandService
}

func NewCompletionCoordinator(commands ExecutionCommandService) (*CompletionCoordinator, error) {
	if commands == nil {
		return nil, ErrInvalidConfiguration
	}
	return &CompletionCoordinator{commands: commands}, nil
}

func (coordinator *CompletionCoordinator) Complete(ctx context.Context, request CompletionCoordinationRequest) (CompletionCoordinationResult, error) {
	decision := kernel.PlanCompletion(request.Input)
	result := CompletionCoordinationResult{Decision: decision}
	if decision.Status != kernel.CompletionReady {
		return result, nil
	}
	if err := validateCompletionCoordination(request, decision); err != nil {
		return result, err
	}
	evidenceIDs := completionEvidenceIDs(decision.EvidenceRefs)
	payload := map[string]any{
		"lifecycle_epoch":               decision.Work.LifecycleEpoch,
		"criteria_revision":             decision.CriteriaRevision,
		"evidence_ids":                  evidenceIDs,
		"artifact_digests":              decision.ArtifactDigests,
		"unresolved_exceptions":         []string{},
		"completion_review_id":          decision.ReviewID,
		"completion_review_revision":    decision.ReviewRevision,
		"branch_policy_revision":        decision.BranchPolicyRevision,
		"validation_finalized_event_id": decision.FinalizedEventID,
	}
	commandType := "tekroo.command.story.request-completion"
	if decision.Work.Kind == kernel.AggregateTask {
		commandType = "tekroo.command.task.request-completion"
		payload["owner_fqn"] = *decision.Work.Ownership.OwnerFQN
	}
	payloadBytes, _ := json.Marshal(payload)
	preconditions := make([]kernel.AggregatePrecondition, len(decision.DependencyTasks))
	for index, dependency := range decision.DependencyTasks {
		preconditions[index] = kernel.AggregatePrecondition{Aggregate: kernel.AggregateRef{Kind: dependency.Kind, ID: dependency.ID}, Expected: kernel.NewExpectedRevision(dependency.Revision)}
	}
	sort.Slice(preconditions, func(i, j int) bool { return preconditions[i].Aggregate.ID < preconditions[j].Aggregate.ID })
	epoch := decision.Work.LifecycleEpoch
	command := kernel.KernelCommand{
		ContractManifest: kernel.ContractIdentity, CommandID: request.Identity.CommandID, CommandType: commandType, CommandVersion: kernel.SchemaVersion,
		Target: kernel.AggregateRef{Kind: decision.Work.Kind, ID: decision.Work.ID}, Authority: request.Authority,
		ActorFQN: request.ActorFQN, Execution: request.Execution, ExpectedRevision: kernel.NewExpectedRevision(decision.Work.Revision), Preconditions: preconditions, ExpectedLifecycleEpoch: &epoch,
		ExpectedPolicyRevision: request.PolicyRevision, ExpectedCatalogueRevision: request.CatalogueRevision,
		IdempotencyKey: request.Identity.IdempotencyKey, CorrelationID: request.Identity.CorrelationID,
		Causation: append([]kernel.DagParent(nil), decision.Parents...), Payload: payloadBytes, EvidenceRefs: append([]kernel.EvidenceRef(nil), decision.EvidenceRefs...),
	}
	receipt, err := coordinator.commands.Handle(ctx, command, request.Provenance)
	result.Receipt = &receipt
	if err != nil || receipt.OutcomeCode != kernel.OutcomeApplied {
		return result, err
	}
	if len(receipt.EventIDs) != 1 {
		return result, ErrInvalidCompletionCoordination
	}
	return result, nil
}

func (coordinator *CompletionCoordinator) Reopen(ctx context.Context, request ReopeningCoordinationRequest) (ReopeningCoordinationResult, error) {
	decision := kernel.PlanReopening(request.Input)
	result := ReopeningCoordinationResult{Decision: decision}
	if decision.Status != kernel.ReopeningReady {
		return result, nil
	}
	if err := validateReopeningCoordination(request); err != nil {
		return result, err
	}
	payload, _ := json.Marshal(struct {
		PriorEpoch       uint64          `json:"prior_epoch"`
		NewScopeRevision uint64          `json:"new_scope_revision"`
		Reason           string          `json:"reason"`
		OwnerCarry       bool            `json:"owner_carry_forward"`
		EvidenceIDs      []kernel.UUIDv7 `json:"evidence_ids"`
	}{decision.Work.LifecycleEpoch, decision.NewScopeRevision, decision.ReopeningReason, decision.OwnerCarryForward, completionEvidenceIDs(decision.EvidenceRefs)})
	epoch := decision.Work.LifecycleEpoch
	command := kernel.KernelCommand{
		ContractManifest: kernel.ContractIdentity, CommandID: request.Identity.CommandID, CommandType: "tekroo.command.work.reopen", CommandVersion: kernel.SchemaVersion,
		Target: kernel.AggregateRef{Kind: decision.Work.Kind, ID: decision.Work.ID}, Authority: request.Authority,
		ExpectedRevision: kernel.NewExpectedRevision(decision.Work.Revision), ExpectedLifecycleEpoch: &epoch,
		ExpectedPolicyRevision: request.PolicyRevision, ExpectedCatalogueRevision: request.CatalogueRevision,
		IdempotencyKey: request.Identity.IdempotencyKey, CorrelationID: request.Identity.CorrelationID,
		Causation: append([]kernel.DagParent(nil), decision.Parents...), Payload: payload, EvidenceRefs: append([]kernel.EvidenceRef(nil), decision.EvidenceRefs...),
	}
	receipt, err := coordinator.commands.Handle(ctx, command, request.Provenance)
	result.Receipt = &receipt
	if err != nil || receipt.OutcomeCode != kernel.OutcomeApplied {
		return result, err
	}
	if len(receipt.EventIDs) != 1 {
		return result, ErrInvalidCompletionCoordination
	}
	return result, nil
}

func validateCompletionCoordination(request CompletionCoordinationRequest, decision kernel.CompletionDecision) error {
	if !validCompletionIdentity(request.Identity) || request.PolicyRevision == 0 || request.CatalogueRevision != kernel.CatalogueRevision || !request.Provenance.Valid() || !request.Authority.Valid() {
		return ErrInvalidCompletionCoordination
	}
	if decision.Work.Kind == kernel.AggregateTask {
		if decision.Work.Ownership.OwnerFQN == nil || request.Authority.Kind != kernel.PrincipalActor || request.ActorFQN == nil || request.Execution == nil || !request.Execution.Valid() || *request.ActorFQN != *decision.Work.Ownership.OwnerFQN || request.Authority.ID != string(*request.ActorFQN) {
			return ErrInvalidCompletionCoordination
		}
		return nil
	}
	if (request.Authority.Kind != kernel.PrincipalHuman && request.Authority.Kind != kernel.PrincipalPolicy) || request.ActorFQN != nil || request.Execution != nil {
		return ErrInvalidCompletionCoordination
	}
	return nil
}

func validateReopeningCoordination(request ReopeningCoordinationRequest) error {
	if !validCompletionIdentity(request.Identity) || (request.Authority.Kind != kernel.PrincipalHuman && request.Authority.Kind != kernel.PrincipalPolicy) || !request.Authority.Valid() || request.PolicyRevision == 0 || request.CatalogueRevision != kernel.CatalogueRevision || !request.Provenance.Valid() {
		return ErrInvalidCompletionCoordination
	}
	return nil
}

func validCompletionIdentity(identity CompletionCommandIdentity) bool {
	return identity.CommandID.Valid() && identity.CorrelationID.Valid() && identity.IdempotencyKey != "" && len(identity.IdempotencyKey) <= 256
}

func completionEvidenceIDs(values []kernel.EvidenceRef) []kernel.UUIDv7 {
	result := make([]kernel.UUIDv7, len(values))
	for index, value := range values {
		result[index] = value.EvidenceID
	}
	return result
}
