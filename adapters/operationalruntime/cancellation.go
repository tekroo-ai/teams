package operationalruntime

import (
	"context"
	"errors"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

type CancellationRequest struct {
	ExpectedRevision uint64               `json:"expected_revision"`
	Reason           string               `json:"reason"`
	EvidenceRefs     []kernel.EvidenceRef `json:"evidence_refs"`
	IdempotencyKey   string               `json:"idempotency_key"`
}

func (request CancellationRequest) Valid() bool {
	if request.ExpectedRevision == 0 || request.Reason == "" || len(request.Reason) > 4096 || len(request.EvidenceRefs) == 0 || len(request.EvidenceRefs) > 64 || request.IdempotencyKey == "" || len(request.IdempotencyKey) > 256 {
		return false
	}
	for _, evidence := range request.EvidenceRefs {
		if !evidence.EvidenceID.Valid() || !evidence.SHA256.Valid() {
			return false
		}
	}
	return true
}

func (service *ProductionService) RequestInvocationCancellation(ctx context.Context, principal kernel.PrincipalRef, invocationID kernel.UUIDv7, request CancellationRequest) (InvocationStatus, error) {
	if service == nil || principal != service.operatorIdentity.Principal || !invocationID.Valid() || !request.Valid() {
		return InvocationStatus{}, application.ErrInvalidConfiguration
	}
	current, found, err := service.ReadInvocation(ctx, invocationID)
	if err != nil || !found || current.Revision != request.ExpectedRevision || current.State != kernel.InvocationStarted || current.CancellationRequestedAt != nil {
		return InvocationStatus{}, errors.Join(application.ErrInvalidOperationalExecution, err)
	}
	evidenceIDs := make([]kernel.UUIDv7, len(request.EvidenceRefs))
	for index, evidence := range request.EvidenceRefs {
		evidenceIDs[index] = evidence.EvidenceID
	}
	now := service.clock.Now().UTC()
	payload := mustJSON(map[string]any{
		"invocation_id": invocationID, "expected_invocation_revision": current.Revision,
		"reason": request.Reason, "evidence_ids": evidenceIDs, "authority": principal, "requested_at": now,
	})
	command := kernel.KernelCommand{
		ContractManifest:          kernel.ContractIdentity,
		CommandID:                 deterministicOperationalUUID("cancellation-command", string(invocationID), request.IdempotencyKey),
		CommandType:               "tekroo.command.work-invocation.request-cancellation",
		CommandVersion:            kernel.OperationalSchemaVersion,
		Target:                    kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: invocationID},
		Authority:                 principal,
		ExpectedRevision:          kernel.NewExpectedRevision(current.Revision),
		ExpectedPolicyRevision:    service.provenance.PolicyRevision,
		ExpectedCatalogueRevision: kernel.CatalogueRevision,
		IdempotencyKey:            request.IdempotencyKey,
		CorrelationID:             invocationID,
		Causation:                 []kernel.DagParent{{ParentEventID: current.LastEventID, EdgeKind: kernel.EdgeCausal}},
		IssuedAt:                  &now,
		Payload:                   payload,
		EvidenceRefs:              append([]kernel.EvidenceRef(nil), request.EvidenceRefs...),
	}
	receipt, err := service.Submit(ctx, command)
	if err != nil {
		return InvocationStatus{}, err
	}
	if receipt.OutcomeCode != kernel.OutcomeApplied && receipt.OutcomeCode != kernel.OutcomeNoChange {
		return InvocationStatus{}, errors.New("cancellation rejected: " + receipt.ReasonCode)
	}
	updated, found, err := service.ReadInvocation(ctx, invocationID)
	if err != nil || !found {
		return InvocationStatus{}, errors.Join(application.ErrInvalidOperationalExecution, err)
	}
	return updated, nil
}
