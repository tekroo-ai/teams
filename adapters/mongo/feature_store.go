package mongo

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

type featureDocument struct {
	ID              string                     `bson:"_id"`
	Revision        uint64                     `bson:"revision"`
	SubmittedByKind kernel.PrincipalKind       `bson:"submitted_by_kind"`
	SubmittedByID   string                     `bson:"submitted_by_id"`
	IdempotencyKey  string                     `bson:"idempotency_key"`
	Status          organization.FeatureStatus `bson:"status"`
	CreatedAt       time.Time                  `bson:"created_at"`
	UpdatedAt       time.Time                  `bson:"updated_at"`
	Data            []byte                     `bson:"data"`
}

func (s *Store) CreateFeature(ctx context.Context, feature organization.FeatureRequest, message organization.OrganizationalMessage) (organization.FeatureRequest, bool, error) {
	if err := requireDeadline(ctx); err != nil {
		return organization.FeatureRequest{}, false, err
	}
	if s == nil || s.client == nil || feature.Validate() != nil || message.Validate() != nil || feature.InitialMessageID != message.ID || message.Work.FeatureID == nil || *message.Work.FeatureID != feature.ID {
		return organization.FeatureRequest{}, false, organization.ErrInvalidFeature
	}
	featureRaw, err := json.Marshal(feature)
	if err != nil {
		return organization.FeatureRequest{}, false, err
	}
	messageRaw, err := json.Marshal(message)
	if err != nil {
		return organization.FeatureRequest{}, false, err
	}
	session, err := s.client.StartSession()
	if err != nil {
		return organization.FeatureRequest{}, false, err
	}
	defer session.EndSession(ctx)
	_, err = session.WithTransaction(ctx, func(transactionContext context.Context) (any, error) {
		featureDoc := featureDocument{ID: string(feature.ID), Revision: feature.Revision, SubmittedByKind: feature.SubmittedBy.Kind, SubmittedByID: feature.SubmittedBy.ID, IdempotencyKey: feature.Input.IdempotencyKey, Status: feature.Status, CreatedAt: feature.CreatedAt, UpdatedAt: feature.UpdatedAt, Data: featureRaw}
		if _, insertErr := s.db.Collection("feature_requests").InsertOne(transactionContext, featureDoc); insertErr != nil {
			return nil, insertErr
		}
		if advanceErr := s.advanceMessageThread(transactionContext, message); advanceErr != nil {
			return nil, advanceErr
		}
		messageDoc := organizationalMessageDocument{ID: string(message.ID), Recipient: string(message.Recipient), ThreadID: string(message.Flow.ThreadID), StepID: string(message.Flow.StepID), Hop: message.Flow.Hop, CreatedAt: message.CreatedAt, ExpiresAt: message.ExpiresAt, State: organization.MessagePending, Data: messageRaw}
		_, insertErr := s.db.Collection("organizational_messages").InsertOne(transactionContext, messageDoc)
		return nil, insertErr
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()).SetReadPreference(readpref.Primary()))
	if err == nil {
		return feature, true, nil
	}
	if !driver.IsDuplicateKeyError(err) {
		return organization.FeatureRequest{}, false, err
	}
	prior, found, loadErr := s.LoadFeatureByIdempotencyKey(ctx, feature.SubmittedBy, feature.Input.IdempotencyKey)
	if loadErr != nil {
		return organization.FeatureRequest{}, false, loadErr
	}
	if !found || !reflect.DeepEqual(prior.Input, feature.Input) {
		return organization.FeatureRequest{}, false, organization.ErrFeatureConflict
	}
	return prior, false, nil
}

func (s *Store) LoadFeature(ctx context.Context, id kernel.UUIDv7) (organization.FeatureRequest, bool, error) {
	if err := requireDeadline(ctx); err != nil {
		return organization.FeatureRequest{}, false, err
	}
	if s == nil || !id.Valid() {
		return organization.FeatureRequest{}, false, organization.ErrInvalidFeature
	}
	var document featureDocument
	err := s.db.Collection("feature_requests").FindOne(ctx, bson.D{{Key: "_id", Value: string(id)}}).Decode(&document)
	if errors.Is(err, driver.ErrNoDocuments) {
		return organization.FeatureRequest{}, false, nil
	}
	if err != nil {
		return organization.FeatureRequest{}, false, err
	}
	feature, err := decodeFeature(document)
	return feature, err == nil, err
}

func (s *Store) LoadFeatureByIdempotencyKey(ctx context.Context, principal kernel.PrincipalRef, key string) (organization.FeatureRequest, bool, error) {
	if err := requireDeadline(ctx); err != nil {
		return organization.FeatureRequest{}, false, err
	}
	if s == nil || !principal.Valid() || key == "" {
		return organization.FeatureRequest{}, false, organization.ErrInvalidFeature
	}
	var document featureDocument
	err := s.db.Collection("feature_requests").FindOne(ctx, bson.D{{Key: "submitted_by_kind", Value: principal.Kind}, {Key: "submitted_by_id", Value: principal.ID}, {Key: "idempotency_key", Value: key}}).Decode(&document)
	if errors.Is(err, driver.ErrNoDocuments) {
		return organization.FeatureRequest{}, false, nil
	}
	if err != nil {
		return organization.FeatureRequest{}, false, err
	}
	feature, err := decodeFeature(document)
	return feature, err == nil, err
}

func (s *Store) ListFeatures(ctx context.Context, statuses []organization.FeatureStatus, limit int64) ([]organization.FeatureRequest, error) {
	if err := requireDeadline(ctx); err != nil {
		return nil, err
	}
	if s == nil || len(statuses) == 0 || len(statuses) > 16 || limit <= 0 || limit > 1000 {
		return nil, organization.ErrInvalidFeature
	}
	values := make(bson.A, len(statuses))
	for index, status := range statuses {
		if !status.Valid() {
			return nil, organization.ErrInvalidFeature
		}
		values[index] = status
	}
	cursor, err := s.db.Collection("feature_requests").Find(ctx, bson.D{{Key: "status", Value: bson.D{{Key: "$in", Value: values}}}}, options.Find().SetSort(bson.D{{Key: "updated_at", Value: 1}, {Key: "_id", Value: 1}}).SetLimit(limit))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	result := make([]organization.FeatureRequest, 0)
	for cursor.Next(ctx) {
		var document featureDocument
		if err := cursor.Decode(&document); err != nil {
			return nil, err
		}
		feature, err := decodeFeature(document)
		if err != nil {
			return nil, err
		}
		result = append(result, feature)
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Store) ApplyFeaturePlan(ctx context.Context, id kernel.UUIDv7, expectedRevision uint64, plan organization.FeaturePlan, now time.Time) (organization.FeatureRequest, error) {
	if err := requireDeadline(ctx); err != nil {
		return organization.FeatureRequest{}, err
	}
	feature, found, err := s.LoadFeature(ctx, id)
	if err != nil {
		return organization.FeatureRequest{}, err
	}
	if !found {
		return organization.FeatureRequest{}, organization.ErrFeatureNotFound
	}
	if feature.Revision != expectedRevision || feature.Status != organization.FeatureSpecified || plan.Validate(feature) != nil || now.Before(feature.UpdatedAt) {
		return organization.FeatureRequest{}, organization.ErrFeatureRevisionConflict
	}
	feature.Revision++
	feature.Status = organization.FeaturePlanned
	feature.Plan = &plan
	feature.UpdatedAt = now
	raw, err := json.Marshal(feature)
	if err != nil {
		return organization.FeatureRequest{}, err
	}
	result, err := s.db.Collection("feature_requests").UpdateOne(ctx, bson.D{{Key: "_id", Value: string(id)}, {Key: "revision", Value: expectedRevision}}, bson.D{{Key: "$set", Value: bson.D{{Key: "revision", Value: feature.Revision}, {Key: "status", Value: feature.Status}, {Key: "updated_at", Value: now}, {Key: "data", Value: raw}}}})
	if err != nil {
		return organization.FeatureRequest{}, err
	}
	if result.ModifiedCount != 1 {
		return organization.FeatureRequest{}, organization.ErrFeatureRevisionConflict
	}
	return feature, nil
}

func (s *Store) AdvanceFeature(ctx context.Context, next organization.FeatureRequest, expectedRevision uint64, message *organization.OrganizationalMessage) (organization.FeatureRequest, error) {
	if err := requireDeadline(ctx); err != nil {
		return organization.FeatureRequest{}, err
	}
	if s == nil || s.client == nil || expectedRevision == 0 || next.Revision != expectedRevision+1 || next.Validate() != nil {
		return organization.FeatureRequest{}, organization.ErrInvalidFeature
	}
	if message != nil && (message.Validate() != nil || message.Work.FeatureID == nil || *message.Work.FeatureID != next.ID || next.LastMessageID != message.ID || next.LastStepID != message.Flow.StepID || next.LastHop != message.Flow.Hop) {
		return organization.FeatureRequest{}, organization.ErrInvalidFeature
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return organization.FeatureRequest{}, err
	}
	var messageRaw []byte
	if message != nil {
		messageRaw, err = json.Marshal(*message)
		if err != nil {
			return organization.FeatureRequest{}, err
		}
	}
	session, err := s.client.StartSession()
	if err != nil {
		return organization.FeatureRequest{}, err
	}
	defer session.EndSession(ctx)
	_, err = session.WithTransaction(ctx, func(transactionContext context.Context) (any, error) {
		result, updateErr := s.db.Collection("feature_requests").UpdateOne(transactionContext, bson.D{{Key: "_id", Value: string(next.ID)}, {Key: "revision", Value: expectedRevision}}, bson.D{{Key: "$set", Value: bson.D{{Key: "revision", Value: next.Revision}, {Key: "status", Value: next.Status}, {Key: "updated_at", Value: next.UpdatedAt}, {Key: "data", Value: raw}}}})
		if updateErr != nil {
			return nil, updateErr
		}
		if result.ModifiedCount != 1 {
			return nil, organization.ErrFeatureRevisionConflict
		}
		if message == nil {
			return nil, nil
		}
		if advanceErr := s.advanceMessageThread(transactionContext, *message); advanceErr != nil {
			return nil, advanceErr
		}
		document := organizationalMessageDocument{ID: string(message.ID), Recipient: string(message.Recipient), ThreadID: string(message.Flow.ThreadID), StepID: string(message.Flow.StepID), Hop: message.Flow.Hop, CreatedAt: message.CreatedAt, ExpiresAt: message.ExpiresAt, State: organization.MessagePending, Data: messageRaw}
		_, insertErr := s.db.Collection("organizational_messages").InsertOne(transactionContext, document)
		return nil, insertErr
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()).SetReadPreference(readpref.Primary()))
	if driver.IsDuplicateKeyError(err) {
		return organization.FeatureRequest{}, organization.ErrFeatureConflict
	}
	if err != nil {
		return organization.FeatureRequest{}, err
	}
	return next, nil
}

func decodeFeature(document featureDocument) (organization.FeatureRequest, error) {
	var feature organization.FeatureRequest
	if err := json.Unmarshal(document.Data, &feature); err != nil || feature.Validate() != nil || string(feature.ID) != document.ID || feature.Revision != document.Revision || feature.Status != document.Status || feature.SubmittedBy.Kind != document.SubmittedByKind || feature.SubmittedBy.ID != document.SubmittedByID || feature.Input.IdempotencyKey != document.IdempotencyKey {
		return organization.FeatureRequest{}, organization.ErrInvalidFeature
	}
	return feature, nil
}

var _ organization.FeatureStore = (*Store)(nil)
