package mongo

import (
	"context"
	"errors"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var ErrInvalidEventWait = errors.New("invalid event wait request")

// WaitForAggregateEvent blocks until the first matching event after the
// supplied aggregate revision is committed. The change stream is opened
// before the backlog query so a commit cannot fall between observation and
// subscription.
func (s *Store) WaitForAggregateEvent(ctx context.Context, aggregate kernel.AggregateRef, afterRevision uint64, eventTypes []string) (kernel.DomainEvent, error) {
	if err := requireDeadline(ctx); err != nil {
		return kernel.DomainEvent{}, err
	}
	if s == nil || s.db == nil || !aggregate.Valid() || !validEventTypeFilter(eventTypes) {
		return kernel.DomainEvent{}, ErrInvalidEventWait
	}

	filter := bson.D{
		{Key: "aggregate_key", Value: aggregateKey(aggregate)},
		{Key: "revision", Value: bson.D{{Key: "$gt", Value: afterRevision}}},
	}
	if len(eventTypes) > 0 {
		values := make(bson.A, len(eventTypes))
		for index, eventType := range eventTypes {
			values[index] = eventType
		}
		filter = append(filter, bson.E{Key: "event_type", Value: bson.D{{Key: "$in", Value: values}}})
	}

	pipelineFilter := bson.D{
		{Key: "operationType", Value: "insert"},
		{Key: "fullDocument.aggregate_key", Value: aggregateKey(aggregate)},
		{Key: "fullDocument.revision", Value: bson.D{{Key: "$gt", Value: afterRevision}}},
	}
	if len(eventTypes) > 0 {
		values := make(bson.A, len(eventTypes))
		for index, eventType := range eventTypes {
			values[index] = eventType
		}
		pipelineFilter = append(pipelineFilter, bson.E{Key: "fullDocument.event_type", Value: bson.D{{Key: "$in", Value: values}}})
	}

	stream, err := s.db.Collection("events").Watch(ctx, driver.Pipeline{bson.D{{Key: "$match", Value: pipelineFilter}}}, options.ChangeStream().SetFullDocument(options.UpdateLookup))
	if err != nil {
		return kernel.DomainEvent{}, err
	}
	defer func() {
		closeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = stream.Close(closeContext)
	}()

	var document eventDocument
	err = s.db.Collection("events").FindOne(ctx, filter, options.FindOne().SetSort(bson.D{{Key: "revision", Value: 1}})).Decode(&document)
	if err == nil {
		return decodeWaitEvent(document, aggregate, afterRevision)
	}
	if !errors.Is(err, driver.ErrNoDocuments) {
		return kernel.DomainEvent{}, err
	}

	if !stream.Next(ctx) {
		if err := stream.Err(); err != nil {
			return kernel.DomainEvent{}, err
		}
		if err := ctx.Err(); err != nil {
			return kernel.DomainEvent{}, err
		}
		return kernel.DomainEvent{}, ErrEventCursorUnavailable
	}
	var change struct {
		FullDocument eventDocument `bson:"fullDocument"`
	}
	if err := stream.Decode(&change); err != nil {
		return kernel.DomainEvent{}, err
	}
	return decodeWaitEvent(change.FullDocument, aggregate, afterRevision)
}

func decodeWaitEvent(document eventDocument, aggregate kernel.AggregateRef, afterRevision uint64) (kernel.DomainEvent, error) {
	var event kernel.DomainEvent
	if decode(document.Data, &event) != nil || event.Aggregate != aggregate || event.AggregateRevision != document.Revision || event.AggregateRevision <= afterRevision || event.EventID != kernel.UUIDv7(document.ID) || event.EventType != document.EventType {
		return kernel.DomainEvent{}, ErrCorruptAggregate
	}
	return event, nil
}

func validEventTypeFilter(eventTypes []string) bool {
	if len(eventTypes) > 64 {
		return false
	}
	seen := make(map[string]struct{}, len(eventTypes))
	for _, eventType := range eventTypes {
		if len(eventType) == 0 || len(eventType) > 256 {
			return false
		}
		if _, duplicate := seen[eventType]; duplicate {
			return false
		}
		seen[eventType] = struct{}{}
	}
	return true
}
