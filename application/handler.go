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

func (h *Handler) Handle(ctx context.Context, command kernel.KernelCommand, provenance kernel.Digest) (kernel.CommandReceipt, error) {
	if err := ctx.Err(); err != nil {
		return kernel.CommandReceipt{}, err
	}
	if receipt, found, err := h.store.LookupReceipt(ctx, command); err != nil {
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
	receivedAt := h.clock.Now()
	decision, err := h.evaluator.Evaluate(command, snapshot, kernel.DecisionContext{
		ReceivedAt:       receivedAt,
		DecidedAt:        h.clock.Now(),
		EventID:          eventID,
		ProvenanceDigest: provenance,
	})
	if err != nil {
		return kernel.CommandReceipt{}, fmt.Errorf("evaluate command: %w", err)
	}
	if err := h.store.Commit(ctx, snapshot, decision); err != nil {
		if errors.Is(err, kernel.ErrDecisionAlreadyCommitted) {
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
