package mongo

import (
	"context"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func (s *Store) ListActiveTaskProjections(ctx context.Context, limit int64) ([]TaskProjection, error) {
	if err := requireDeadline(ctx); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		return nil, ErrInvalidDecision
	}
	filter := bson.D{{Key: "phase", Value: bson.D{{Key: "$nin", Value: bson.A{string(kernel.PhaseCompleted), string(kernel.PhaseClosed)}}}}}
	cursor, err := s.db.Collection("task_projections").Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(limit))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	result := make([]TaskProjection, 0)
	for cursor.Next(ctx) {
		var document operationalProjectionDocument
		if cursor.Decode(&document) != nil {
			return nil, ErrCorruptAggregate
		}
		var projection TaskProjection
		if decode(document.Data, &projection) != nil || !projection.Valid() {
			return nil, ErrCorruptAggregate
		}
		result = append(result, projection)
	}
	return result, cursor.Err()
}

func (s *Store) ListMessageClaims(ctx context.Context, state organization.MessageDeliveryState, limit int64) ([]organization.MessageClaim, error) {
	if err := requireDeadline(ctx); err != nil {
		return nil, err
	}
	if state != organization.MessagePending && state != organization.MessageClaimed || limit <= 0 || limit > 1000 {
		return nil, organization.ErrInvalidOrganizationalMessage
	}
	cursor, err := s.db.Collection("organizational_messages").Find(ctx, bson.D{{Key: "state", Value: state}}, options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}, {Key: "_id", Value: 1}}).SetLimit(limit))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	result := make([]organization.MessageClaim, 0)
	for cursor.Next(ctx) {
		var document organizationalMessageDocument
		if cursor.Decode(&document) != nil {
			return nil, ErrCorruptAggregate
		}
		claim, decodeErr := decodeOrganizationalClaim(document)
		if decodeErr != nil {
			return nil, decodeErr
		}
		result = append(result, claim)
	}
	return result, cursor.Err()
}
