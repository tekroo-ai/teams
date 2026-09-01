package mongo

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type humanNotificationDocument struct {
	ID        string              `bson:"_id"`
	Recipient kernel.PrincipalRef `bson:"recipient"`
	State     string              `bson:"state"`
	CreatedAt int64               `bson:"created_at_unix_nano"`
	Data      []byte              `bson:"data"`
}

func (s *Store) SaveHumanNotification(ctx context.Context, notification organization.HumanNotification) error {
	if s == nil || notification.Valid() == false {
		return ErrInvalidDecision
	}
	if err := requireDeadline(ctx); err != nil {
		return err
	}
	raw, err := json.Marshal(notification)
	if err != nil {
		return err
	}
	document := humanNotificationDocument{ID: string(notification.InteractionID), Recipient: notification.Recipient, State: string(notification.State), CreatedAt: notification.CreatedAt.UnixNano(), Data: raw}
	_, err = s.db.Collection("human_notifications").ReplaceOne(ctx, bson.D{{Key: "_id", Value: document.ID}}, document, options.Replace().SetUpsert(true))
	return err
}

func (s *Store) ReadHumanNotification(ctx context.Context, interactionID kernel.UUIDv7) (organization.HumanNotification, bool, error) {
	if s == nil || !interactionID.Valid() {
		return organization.HumanNotification{}, false, ErrInvalidDecision
	}
	if err := requireDeadline(ctx); err != nil {
		return organization.HumanNotification{}, false, err
	}
	var document humanNotificationDocument
	err := s.db.Collection("human_notifications").FindOne(ctx, bson.D{{Key: "_id", Value: string(interactionID)}}).Decode(&document)
	if errors.Is(err, driver.ErrNoDocuments) {
		return organization.HumanNotification{}, false, nil
	}
	var result organization.HumanNotification
	if err != nil || json.Unmarshal(document.Data, &result) != nil || !result.Valid() {
		return organization.HumanNotification{}, false, errors.Join(ErrCorruptAggregate, err)
	}
	return result, true, nil
}

func (s *Store) ListHumanNotifications(ctx context.Context, recipient kernel.PrincipalRef, openOnly bool, limit int64) ([]organization.HumanNotification, error) {
	if s == nil || recipient.Kind != kernel.PrincipalHuman || !recipient.Valid() || limit <= 0 || limit > 1000 {
		return nil, ErrInvalidDecision
	}
	if err := requireDeadline(ctx); err != nil {
		return nil, err
	}
	filter := bson.D{{Key: "recipient.kind", Value: string(recipient.Kind)}, {Key: "recipient.id", Value: recipient.ID}}
	if openOnly {
		filter = append(filter, bson.E{Key: "state", Value: bson.D{{Key: "$in", Value: bson.A{string(kernel.HumanInteractionOpen), string(kernel.HumanInteractionCollecting)}}}})
	}
	cursor, err := s.db.Collection("human_notifications").Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "created_at_unix_nano", Value: 1}, {Key: "_id", Value: 1}}).SetLimit(limit))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	result := make([]organization.HumanNotification, 0)
	for cursor.Next(ctx) {
		var document humanNotificationDocument
		var item organization.HumanNotification
		if cursor.Decode(&document) != nil || json.Unmarshal(document.Data, &item) != nil || !item.Valid() {
			return nil, ErrCorruptAggregate
		}
		result = append(result, item)
	}
	return result, cursor.Err()
}
