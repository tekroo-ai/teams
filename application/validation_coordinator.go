package application

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/tekroo-ai/teams/kernel"
)

var ErrInvalidValidationCoordination = errors.New("invalid validation coordination request")

type ValidationReviewCommandIdentity struct {
	CommandID      kernel.UUIDv7
	CorrelationID  kernel.UUIDv7
	IdempotencyKey string
}

type ValidationReviewRequest struct {
	Input             kernel.ValidationReviewInput
	Identity          ValidationReviewCommandIdentity
	PolicyAuthority   kernel.PrincipalRef
	PolicyRevision    uint64
	CatalogueRevision uint64
	Provenance        kernel.ProvenanceBasis
}

type ValidationReviewResult struct {
	Decision kernel.ValidationReviewDecision
	Receipt  *kernel.CommandReceipt
}

type ValidationReviewCoordinator struct {
	commands ExecutionCommandService
}

func NewValidationReviewCoordinator(commands ExecutionCommandService) (*ValidationReviewCoordinator, error) {
	if commands == nil {
		return nil, ErrInvalidConfiguration
	}
	return &ValidationReviewCoordinator{commands: commands}, nil
}

func (coordinator *ValidationReviewCoordinator) Open(ctx context.Context, request ValidationReviewRequest) (ValidationReviewResult, error) {
	decision := kernel.PlanValidationReview(request.Input)
	result := ValidationReviewResult{Decision: decision}
	if decision.Status != kernel.ValidationOpeningReady {
		return result, nil
	}
	if err := validateValidationReviewRequest(request); err != nil {
		return result, err
	}
	payload, _ := json.Marshal(struct {
		SubjectKind          kernel.AggregateKind      `json:"subject_kind"`
		SubjectID            kernel.UUIDv7             `json:"subject_id"`
		LifecycleEpoch       uint64                    `json:"lifecycle_epoch"`
		CriteriaRevision     uint64                    `json:"criteria_revision"`
		EvidenceSetDigest    kernel.Digest             `json:"evidence_set_digest"`
		BranchPolicyRevision uint64                    `json:"branch_policy_revision"`
		Branches             []kernel.ReviewBranchSpec `json:"branches"`
		JoinRule             string                    `json:"join_rule"`
		PartialResultPolicy  string                    `json:"partial_result_policy"`
		Adjudication         kernel.ReviewAdjudication `json:"adjudication"`
	}{
		decision.Subject.Kind, decision.Subject.ID, decision.Subject.LifecycleEpoch,
		decision.CriteriaRevision, decision.EvidenceSetDigest, decision.BranchPolicyRevision,
		decision.Branches, "ALL_PASS", decision.PartialResultPolicy, decision.Adjudication,
	})
	command := kernel.KernelCommand{
		ContractManifest: kernel.ContractIdentity,
		CommandID:        request.Identity.CommandID, CommandType: "tekroo.command.completion-review.open", CommandVersion: kernel.SchemaVersion,
		Target: kernel.AggregateRef{Kind: kernel.AggregateCompletionReview, ID: decision.ReviewID}, Authority: request.PolicyAuthority,
		ExpectedRevision:       kernel.MustNotExist(),
		Preconditions:          []kernel.AggregatePrecondition{{Aggregate: kernel.AggregateRef{Kind: decision.Subject.Kind, ID: decision.Subject.ID}, Expected: kernel.NewExpectedRevision(decision.Subject.Revision)}},
		ExpectedPolicyRevision: request.PolicyRevision, ExpectedCatalogueRevision: request.CatalogueRevision,
		IdempotencyKey: request.Identity.IdempotencyKey, CorrelationID: request.Identity.CorrelationID,
		Causation: append([]kernel.DagParent(nil), decision.Parents...), Payload: payload,
	}
	receipt, err := coordinator.commands.Handle(ctx, command, request.Provenance)
	result.Receipt = &receipt
	if err != nil || receipt.OutcomeCode != kernel.OutcomeApplied {
		return result, err
	}
	if len(receipt.EventIDs) != 1 {
		return result, ErrInvalidValidationCoordination
	}
	return result, nil
}

func validateValidationReviewRequest(request ValidationReviewRequest) error {
	if !request.Identity.CommandID.Valid() || !request.Identity.CorrelationID.Valid() || request.Identity.IdempotencyKey == "" || len(request.Identity.IdempotencyKey) > 256 || request.PolicyAuthority.Kind != kernel.PrincipalPolicy || !request.PolicyAuthority.Valid() || request.PolicyRevision == 0 || request.CatalogueRevision != kernel.CatalogueRevision || !request.Provenance.Valid() {
		return ErrInvalidValidationCoordination
	}
	return nil
}
