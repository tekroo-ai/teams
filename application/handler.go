package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/tekroo-ai/teams/kernel"
)

var ErrInvalidConfiguration = errors.New("invalid application configuration")

type Handler struct {
	store     kernel.KernelDecisionStore
	evaluator kernel.Evaluator
	clock     kernel.Clock
	ids       kernel.IDSource
}

func NewHandler(store kernel.KernelDecisionStore, evaluator kernel.Evaluator, clock kernel.Clock, ids kernel.IDSource) (*Handler, error) {
	if store == nil || evaluator.Catalogue == nil || clock == nil || ids == nil {
		return nil, ErrInvalidConfiguration
	}
	return &Handler{store: store, evaluator: evaluator, clock: clock, ids: ids}, nil
}

func (h *Handler) Handle(ctx context.Context, command kernel.KernelCommand, provenance kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
	if err := ctx.Err(); err != nil {
		return kernel.CommandReceipt{}, err
	}
	if receipt, found, err := h.store.LookupReceipt(ctx, command); err != nil {
		if errors.Is(err, kernel.ErrCommandIdentityConflict) || errors.Is(err, kernel.ErrIdempotencyKeyConflict) {
			return h.recordIdentityConflict(ctx, command, provenance, err)
		}
		return kernel.CommandReceipt{}, fmt.Errorf("lookup command receipt: %w", err)
	} else if found {
		return receipt, nil
	}

	snapshot, err := h.store.Load(ctx, command.Target)
	if err != nil {
		return kernel.CommandReceipt{}, fmt.Errorf("load decision snapshot: %w", err)
	}
	eventID, err := h.ids.Next()
	if err != nil {
		return kernel.CommandReceipt{}, fmt.Errorf("allocate event identity: %w", err)
	}
	intentID, err := h.ids.Next()
	if err != nil {
		return kernel.CommandReceipt{}, fmt.Errorf("allocate intent identity: %w", err)
	}
	receivedAt := h.clock.Now()
	decision, err := h.evaluator.Evaluate(command, snapshot, kernel.DecisionContext{
		ReceivedAt: receivedAt,
		DecidedAt:  h.clock.Now(),
		EventID:    eventID,
		IntentID:   intentID,
		Provenance: provenance,
	})
	if err != nil {
		return kernel.CommandReceipt{}, fmt.Errorf("evaluate command: %w", err)
	}
	if err := h.store.Commit(ctx, snapshot, decision); err != nil {
		if errors.Is(err, kernel.ErrDecisionAlreadyCommitted) || errors.Is(err, kernel.ErrCommitUncertain) {
			receipt, found, lookupErr := h.store.LookupReceipt(ctx, command)
			if lookupErr != nil {
				return kernel.CommandReceipt{}, fmt.Errorf("reconcile committed decision: %w", lookupErr)
			}
			if found {
				return receipt, nil
			}
		}
		return kernel.CommandReceipt{}, fmt.Errorf("commit decision: %w", err)
	}
	return decision.Receipt, nil
}

func (h *Handler) recordIdentityConflict(ctx context.Context, command kernel.KernelCommand, provenance kernel.ProvenanceBasis, conflict error) (kernel.CommandReceipt, error) {
	fingerprint, err := kernel.CommandFingerprint(command)
	if err != nil {
		return kernel.CommandReceipt{}, fmt.Errorf("fingerprint conflicting command: %w", err)
	}
	scope, err := kernel.IdempotencyScopeDigest(command)
	if err != nil {
		return kernel.CommandReceipt{}, fmt.Errorf("fingerprint conflicting idempotency scope: %w", err)
	}
	reason := "IDEMPOTENCY_KEY_REUSE"
	if errors.Is(conflict, kernel.ErrCommandIdentityConflict) {
		reason = "COMMAND_ID_REUSE"
	}
	observedAt := h.clock.Now()
	decisionContext := kernel.DecisionContext{
		ReceivedAt: observedAt, DecidedAt: h.clock.Now(), EventID: command.CommandID,
		IntentID: command.CorrelationID, Provenance: provenance,
	}
	_, provenanceDigest, err := kernel.BuildDecisionProvenance(command, fingerprint, decisionContext)
	if err != nil {
		return kernel.CommandReceipt{}, fmt.Errorf("build conflict provenance: %w", err)
	}
	receipt := kernel.CommandReceipt{
		ContractManifest: command.ContractManifest,
		CommandID:        command.CommandID,
		CommandType:      command.CommandType,
		Target:           command.Target,
		OutcomeCode:      kernel.OutcomeRejectedConflict,
		ReasonCode:       reason,
		EventIDs:         []kernel.UUIDv7{},
		ReceivedAt:       observedAt,
		DecidedAt:        decisionContext.DecidedAt,
		ProvenanceDigest: provenanceDigest,
	}
	if err := h.store.RecordIdentityConflict(ctx, kernel.IdentityConflictAudit{
		CommandID:        command.CommandID,
		IdempotencyScope: scope,
		Fingerprint:      fingerprint,
		ReasonCode:       reason,
		ObservedAt:       receipt.DecidedAt,
		ProvenanceDigest: provenanceDigest,
	}); err != nil {
		return kernel.CommandReceipt{}, fmt.Errorf("record identity conflict: %w", err)
	}
	return receipt, nil
}
