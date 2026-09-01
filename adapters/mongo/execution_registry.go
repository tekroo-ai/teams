package mongo

import (
	"context"
	"errors"

	"github.com/tekroo-ai/teams/kernel"
	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
)

// ExecutionRegistration is the durable per-actor registry head. AggregateID
// remains stable while successive fenced role processes replace one another.
type ExecutionRegistration struct {
	ActorFQN          kernel.ActorFQN
	Execution         kernel.ExecutionTuple
	AggregateID       kernel.UUIDv7
	AggregateRevision uint64
	LastEventID       kernel.UUIDv7
}

func (registration ExecutionRegistration) Valid() bool {
	return registration.ActorFQN.Valid() && registration.Execution.Valid() && registration.AggregateID.Valid() && registration.AggregateRevision > 0 && registration.LastEventID.Valid()
}

// ReadExecutionRegistration returns the exact durable registry head for an
// actor. Legacy registration documents used the first execution ID as the
// aggregate ID; the fallback preserves that representation without mutation.
func (s *Store) ReadExecutionRegistration(ctx context.Context, actor kernel.ActorFQN) (ExecutionRegistration, bool, error) {
	if err := requireDeadline(ctx); err != nil {
		return ExecutionRegistration{}, false, err
	}
	if s == nil || s.db == nil || !actor.Valid() {
		return ExecutionRegistration{}, false, ErrInvalidDecision
	}
	var document executionDocument
	err := s.db.Collection("executions").FindOne(ctx, bson.D{{Key: "_id", Value: string(actor)}}).Decode(&document)
	if errors.Is(err, driver.ErrNoDocuments) {
		return ExecutionRegistration{}, false, nil
	}
	if err != nil {
		return ExecutionRegistration{}, false, err
	}
	aggregateID := kernel.UUIDv7(document.AggregateID)
	revision := document.AggregateRevision
	lastEventID := kernel.UUIDv7(document.LastEventID)
	if aggregateID == "" {
		aggregateID = kernel.UUIDv7(document.ExecutionID)
		revision = 1
		var event eventDocument
		err = s.db.Collection("events").FindOne(ctx, bson.D{{Key: "aggregate_key", Value: aggregateKey(kernel.AggregateRef{Kind: kernel.AggregateExecution, ID: aggregateID})}, {Key: "revision", Value: revision}}).Decode(&event)
		if err != nil {
			return ExecutionRegistration{}, false, err
		}
		lastEventID = kernel.UUIDv7(event.ID)
	}
	registration := ExecutionRegistration{
		ActorFQN:          actor,
		Execution:         kernel.ExecutionTuple{ExecutionID: kernel.UUIDv7(document.ExecutionID), FencingEpoch: document.FencingEpoch},
		AggregateID:       aggregateID,
		AggregateRevision: revision,
		LastEventID:       lastEventID,
	}
	if !registration.Valid() {
		return ExecutionRegistration{}, false, ErrCorruptAggregate
	}
	return registration, true, nil
}
