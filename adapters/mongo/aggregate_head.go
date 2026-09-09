package mongo

import (
	"context"
	"errors"

	"github.com/tekroo-ai/teams/kernel"
	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
)

func (s *Store) ReadAggregateHead(ctx context.Context, aggregate kernel.AggregateRef) (kernel.AggregateState, kernel.UUIDv7, bool, error) {
	if err := requireDeadline(ctx); err != nil {
		return kernel.AggregateState{}, "", false, err
	}
	if s == nil || !aggregate.Valid() {
		return kernel.AggregateState{}, "", false, ErrInvalidDecision
	}
	var document aggregateDocument
	if err := s.db.Collection("aggregates").FindOne(ctx, bson.D{{Key: "_id", Value: aggregateKey(aggregate)}}).Decode(&document); err != nil {
		if errors.Is(err, driver.ErrNoDocuments) {
			return kernel.AggregateState{}, "", false, nil
		}
		return kernel.AggregateState{}, "", false, err
	}
	var state kernel.AggregateState
	if len(document.State) == 0 || decode(document.State, &state) != nil || state.Kind != aggregate.Kind || state.ID != aggregate.ID || state.Revision != document.Revision {
		return kernel.AggregateState{}, "", false, ErrCorruptAggregate
	}
	var event eventDocument
	if err := s.db.Collection("events").FindOne(ctx, bson.D{{Key: "aggregate_key", Value: aggregateKey(aggregate)}, {Key: "revision", Value: document.Revision}}).Decode(&event); err != nil {
		return kernel.AggregateState{}, "", false, err
	}
	eventID := kernel.UUIDv7(event.ID)
	if !eventID.Valid() {
		return kernel.AggregateState{}, "", false, ErrCorruptAggregate
	}
	return state, eventID, true, nil
}

// ReadAggregateRevisionHead returns the committed revision and last event for
// aggregates such as completion reviews that intentionally have no generic
// lifecycle AggregateState projection.
func (s *Store) ReadAggregateRevisionHead(ctx context.Context, aggregate kernel.AggregateRef) (uint64, kernel.UUIDv7, bool, error) {
	if err := requireDeadline(ctx); err != nil {
		return 0, "", false, err
	}
	if s == nil || !aggregate.Valid() {
		return 0, "", false, ErrInvalidDecision
	}
	var document aggregateDocument
	if err := s.db.Collection("aggregates").FindOne(ctx, bson.D{{Key: "_id", Value: aggregateKey(aggregate)}}).Decode(&document); err != nil {
		if errors.Is(err, driver.ErrNoDocuments) {
			return 0, "", false, nil
		}
		return 0, "", false, err
	}
	if document.Revision == 0 {
		return 0, "", false, ErrCorruptAggregate
	}
	var event eventDocument
	if err := s.db.Collection("events").FindOne(ctx, bson.D{{Key: "aggregate_key", Value: aggregateKey(aggregate)}, {Key: "revision", Value: document.Revision}}).Decode(&event); err != nil {
		return 0, "", false, err
	}
	var domain kernel.DomainEvent
	if decode(event.Data, &domain) != nil || domain.EventID != kernel.UUIDv7(event.ID) || domain.Aggregate != aggregate || domain.AggregateRevision != document.Revision {
		return 0, "", false, ErrCorruptAggregate
	}
	return document.Revision, domain.EventID, true, nil
}
