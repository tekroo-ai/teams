package mongo

import (
	"context"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// ReadCommittedTerminalDecision binds a competing terminal result to its
// accepted event, applied receipt and available evidence. No mutation/retry.
func (s *Store) ReadCommittedTerminalDecision(ctx context.Context, id kernel.UUIDv7) (application.CommittedTerminalDecision, bool, error) {
	if err := requireDeadline(ctx); err != nil {
		return application.CommittedTerminalDecision{}, false, err
	}
	ref := kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: id}
	snapshot, err := s.loadSnapshot(ctx, ref, nil)
	if err != nil {
		return application.CommittedTerminalDecision{}, false, err
	}
	inv, found := snapshot.WorkInvocations[ref]
	if !found || !inv.State.Terminal() {
		return application.CommittedTerminalDecision{}, false, nil
	}
	event, found, err := s.ReadEvent(ctx, inv.LastEventID)
	if err != nil {
		return application.CommittedTerminalDecision{}, false, err
	}
	if !found || event.EventType != "tekroo.event.work-invocation.terminal-recorded" || event.Aggregate != ref || event.AggregateRevision != inv.Revision {
		return application.CommittedTerminalDecision{}, false, ErrCorruptAggregate
	}
	var stored receiptDocument
	if err := s.db.Collection("receipts").FindOne(ctx, bson.D{{Key: "_id", Value: string(event.CommandID)}}).Decode(&stored); err != nil {
		return application.CommittedTerminalDecision{}, false, err
	}
	var receipt kernel.CommandReceipt
	if err := decode(stored.Data, &receipt); err != nil {
		return application.CommittedTerminalDecision{}, false, err
	}
	refs := make([]kernel.EvidenceRef, 0, len(inv.TerminalEvidenceIDs))
	for _, id := range inv.TerminalEvidenceIDs {
		meta, found := snapshot.Evidence[id]
		if !found || !meta.Available || !meta.SHA256.Valid() {
			return application.CommittedTerminalDecision{}, false, ErrCorruptAggregate
		}
		refs = append(refs, kernel.EvidenceRef{EvidenceID: id, SHA256: meta.SHA256})
	}
	return application.CommittedTerminalDecision{Invocation: inv.Clone(), Event: event, Receipt: receipt, Evidence: refs, CurrentExecution: snapshot.CurrentExecutions[inv.ActorFQN]}, true, nil
}
