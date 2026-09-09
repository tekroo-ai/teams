package mongo

import (
	"context"
	"errors"

	"github.com/tekroo-ai/teams/kernel"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// ReadEvent returns one exact committed domain event without changing event,
// outbox, or projection state.
func (s *Store) ReadEvent(ctx context.Context, id kernel.UUIDv7) (kernel.DomainEvent, bool, error) {
	if err := requireDeadline(ctx); err != nil {
		return kernel.DomainEvent{}, false, err
	}
	if s == nil || s.db == nil || !id.Valid() {
		return kernel.DomainEvent{}, false, ErrCorruptAggregate
	}
	var document eventDocument
	err := s.db.Collection("events").FindOne(ctx, bson.D{{Key: "_id", Value: string(id)}}).Decode(&document)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return kernel.DomainEvent{}, false, nil
	}
	if err != nil {
		return kernel.DomainEvent{}, false, err
	}
	var event kernel.DomainEvent
	if decode(document.Data, &event) != nil || event.EventID != id {
		return kernel.DomainEvent{}, false, ErrCorruptAggregate
	}
	return event, true, nil
}

// ReadCommandReceipt returns the durable idempotency receipt for one exact
// command identity. Recovery code uses it to distinguish "not attempted" from
// "committed before the process stopped" without replaying a mutation.
func (s *Store) ReadCommandReceipt(ctx context.Context, id kernel.UUIDv7) (kernel.CommandReceipt, bool, error) {
	if err := requireDeadline(ctx); err != nil {
		return kernel.CommandReceipt{}, false, err
	}
	if s == nil || s.db == nil || !id.Valid() {
		return kernel.CommandReceipt{}, false, ErrCorruptAggregate
	}
	var document receiptDocument
	err := s.db.Collection("receipts").FindOne(ctx, bson.D{{Key: "_id", Value: string(id)}}).Decode(&document)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return kernel.CommandReceipt{}, false, nil
	}
	if err != nil {
		return kernel.CommandReceipt{}, false, err
	}
	var receipt kernel.CommandReceipt
	if decode(document.Data, &receipt) != nil || receipt.CommandID != id {
		return kernel.CommandReceipt{}, false, ErrCorruptAggregate
	}
	return receipt, true, nil
}
