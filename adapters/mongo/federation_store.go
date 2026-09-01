package mongo

import (
	"context"
	"encoding/json"
	"errors"
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

type federationReceiptDocument struct {
	ReplayID      string    `bson:"_id"`
	DeliveryID    string    `bson:"delivery_id"`
	RouteID       string    `bson:"route_id"`
	RouteRevision uint64    `bson:"route_revision"`
	MessageID     string    `bson:"message_id"`
	MessageSHA256 string    `bson:"message_sha256"`
	AcceptedAt    time.Time `bson:"accepted_at"`
}

type federationOutboundDocument struct {
	DeliveryID    string    `bson:"_id"`
	ReplayID      string    `bson:"replay_id"`
	RouteID       string    `bson:"route_id"`
	RouteRevision uint64    `bson:"route_revision"`
	MessageID     string    `bson:"message_id"`
	MessageSHA256 string    `bson:"message_sha256"`
	RecordedAt    time.Time `bson:"recorded_at"`
	Envelope      []byte    `bson:"envelope"`
	Message       []byte    `bson:"message"`
}

// RecordFederatedOutbound advances local thread lineage and durably records the
// exact signed envelope before any network action. It deliberately does not
// create a local inbox item for the remote recipient.
func (s *Store) RecordFederatedOutbound(ctx context.Context, envelope organization.FederatedEnvelope, message organization.OrganizationalMessage, now time.Time) (bool, error) {
	if err := requireDeadline(ctx); err != nil {
		return false, err
	}
	if s == nil || s.client == nil || !envelope.DeliveryID.Valid() || !envelope.ReplayID.Valid() || !envelope.MessageSHA256.Valid() || message.Validate() != nil || now.IsZero() {
		return false, organization.ErrInvalidFederation
	}
	if found, err := s.matchFederationOutbound(ctx, envelope, message); err != nil || found {
		return false, err
	}
	envelopeRaw, err := json.Marshal(envelope)
	if err != nil {
		return false, err
	}
	messageRaw, err := json.Marshal(message)
	if err != nil {
		return false, err
	}
	document := federationOutboundDocument{DeliveryID: string(envelope.DeliveryID), ReplayID: string(envelope.ReplayID), RouteID: string(envelope.RouteID), RouteRevision: envelope.RouteRevision, MessageID: string(message.ID), MessageSHA256: string(envelope.MessageSHA256), RecordedAt: now.UTC(), Envelope: envelopeRaw, Message: messageRaw}
	session, err := s.client.StartSession()
	if err != nil {
		return false, err
	}
	defer session.EndSession(ctx)
	_, err = session.WithTransaction(ctx, func(transactionContext context.Context) (any, error) {
		if advanceErr := s.advanceMessageThread(transactionContext, message); advanceErr != nil {
			return nil, advanceErr
		}
		messageDocument := organizationalMessageDocument{ID: string(message.ID), Recipient: string(message.Recipient), ThreadID: string(message.Flow.ThreadID), StepID: string(message.Flow.StepID), Hop: message.Flow.Hop, CreatedAt: message.CreatedAt, ExpiresAt: message.ExpiresAt, State: organization.MessageResolved, Resolution: "FEDERATED_OUTBOUND", EvidenceDigest: string(envelope.MessageSHA256), Data: messageRaw}
		if _, insertErr := s.db.Collection("organizational_messages").InsertOne(transactionContext, messageDocument); insertErr != nil {
			return nil, insertErr
		}
		_, insertErr := s.db.Collection("federation_outbound").InsertOne(transactionContext, document)
		return nil, insertErr
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()).SetReadPreference(readpref.Primary()))
	if err == nil {
		return true, nil
	}
	if driver.IsDuplicateKeyError(err) {
		matched, matchErr := s.matchFederationOutbound(ctx, envelope, message)
		if matchErr != nil {
			return false, matchErr
		}
		if !matched {
			return false, organization.ErrFederationReplayConflict
		}
		return false, nil
	}
	return false, err
}

func (s *Store) matchFederationOutbound(ctx context.Context, envelope organization.FederatedEnvelope, message organization.OrganizationalMessage) (bool, error) {
	var retained federationOutboundDocument
	err := s.db.Collection("federation_outbound").FindOne(ctx, bson.D{{Key: "_id", Value: string(envelope.DeliveryID)}}).Decode(&retained)
	if errors.Is(err, driver.ErrNoDocuments) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if retained.ReplayID != string(envelope.ReplayID) || retained.RouteID != string(envelope.RouteID) || retained.RouteRevision != envelope.RouteRevision || retained.MessageID != string(message.ID) || retained.MessageSHA256 != string(envelope.MessageSHA256) {
		return false, organization.ErrFederationReplayConflict
	}
	return true, nil
}

// AcceptFederatedMessage atomically reserves the replay identity, advances the
// existing organizational-message DAG, appends the message, and retains the
// delivery receipt. A retry with identical identities returns the retained
// receipt; a replay ID reused for different content fails closed.
func (s *Store) AcceptFederatedMessage(ctx context.Context, envelope organization.FederatedEnvelope, message organization.OrganizationalMessage, now time.Time) (organization.FederationDeliveryReceipt, bool, error) {
	if err := requireDeadline(ctx); err != nil {
		return organization.FederationDeliveryReceipt{}, false, err
	}
	if s == nil || s.client == nil || !envelope.ReplayID.Valid() || !envelope.DeliveryID.Valid() || !envelope.RouteID.Valid() || envelope.RouteRevision == 0 || !envelope.MessageSHA256.Valid() || message.Validate() != nil || now.IsZero() {
		return organization.FederationDeliveryReceipt{}, false, organization.ErrInvalidFederation
	}
	if retained, found, err := s.readFederationReceipt(ctx, envelope.ReplayID); err != nil {
		return organization.FederationDeliveryReceipt{}, false, err
	} else if found {
		return compareFederationReceipt(retained, envelope, message)
	}
	session, err := s.client.StartSession()
	if err != nil {
		return organization.FederationDeliveryReceipt{}, false, err
	}
	defer session.EndSession(ctx)
	receipt := organization.FederationDeliveryReceipt{SchemaVersion: organization.FederationSchemaVersion, DeliveryID: envelope.DeliveryID, ReplayID: envelope.ReplayID, RouteID: envelope.RouteID, RouteRevision: envelope.RouteRevision, MessageID: message.ID, MessageSHA256: envelope.MessageSHA256, AcceptedAt: now.UTC(), Outcome: "ACCEPTED"}
	_, err = session.WithTransaction(ctx, func(transactionContext context.Context) (any, error) {
		if advanceErr := s.advanceMessageThread(transactionContext, message); advanceErr != nil {
			return nil, advanceErr
		}
		raw, marshalErr := json.Marshal(message)
		if marshalErr != nil {
			return nil, marshalErr
		}
		messageDocument := organizationalMessageDocument{ID: string(message.ID), Recipient: string(message.Recipient), ThreadID: string(message.Flow.ThreadID), StepID: string(message.Flow.StepID), Hop: message.Flow.Hop, CreatedAt: message.CreatedAt, ExpiresAt: message.ExpiresAt, State: organization.MessagePending, Data: raw}
		if _, insertErr := s.db.Collection("organizational_messages").InsertOne(transactionContext, messageDocument); insertErr != nil {
			return nil, insertErr
		}
		document := federationReceiptDocument{ReplayID: string(receipt.ReplayID), DeliveryID: string(receipt.DeliveryID), RouteID: string(receipt.RouteID), RouteRevision: receipt.RouteRevision, MessageID: string(receipt.MessageID), MessageSHA256: string(receipt.MessageSHA256), AcceptedAt: receipt.AcceptedAt}
		if _, insertErr := s.db.Collection("federation_receipts").InsertOne(transactionContext, document); insertErr != nil {
			return nil, insertErr
		}
		return nil, nil
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()).SetReadPreference(readpref.Primary()))
	if err == nil {
		return receipt, true, nil
	}
	if driver.IsDuplicateKeyError(err) {
		retained, found, readErr := s.readFederationReceipt(ctx, envelope.ReplayID)
		if readErr != nil {
			return organization.FederationDeliveryReceipt{}, false, readErr
		}
		if found {
			return compareFederationReceipt(retained, envelope, message)
		}
		return organization.FederationDeliveryReceipt{}, false, organization.ErrFederationReplayConflict
	}
	return organization.FederationDeliveryReceipt{}, false, err
}

func (s *Store) ReadFederationReceipt(ctx context.Context, replayID kernel.UUIDv7) (organization.FederationDeliveryReceipt, bool, error) {
	if err := requireDeadline(ctx); err != nil {
		return organization.FederationDeliveryReceipt{}, false, err
	}
	if !replayID.Valid() {
		return organization.FederationDeliveryReceipt{}, false, organization.ErrInvalidFederation
	}
	return s.readFederationReceipt(ctx, replayID)
}

func (s *Store) readFederationReceipt(ctx context.Context, replayID kernel.UUIDv7) (organization.FederationDeliveryReceipt, bool, error) {
	var document federationReceiptDocument
	err := s.db.Collection("federation_receipts").FindOne(ctx, bson.D{{Key: "_id", Value: string(replayID)}}).Decode(&document)
	if errors.Is(err, driver.ErrNoDocuments) {
		return organization.FederationDeliveryReceipt{}, false, nil
	}
	if err != nil {
		return organization.FederationDeliveryReceipt{}, false, err
	}
	receipt := organization.FederationDeliveryReceipt{SchemaVersion: organization.FederationSchemaVersion, DeliveryID: kernel.UUIDv7(document.DeliveryID), ReplayID: kernel.UUIDv7(document.ReplayID), RouteID: kernel.UUIDv7(document.RouteID), RouteRevision: document.RouteRevision, MessageID: kernel.UUIDv7(document.MessageID), MessageSHA256: kernel.Digest(document.MessageSHA256), AcceptedAt: document.AcceptedAt, Outcome: "ACCEPTED"}
	if !receipt.Valid() {
		return organization.FederationDeliveryReceipt{}, false, ErrCorruptAggregate
	}
	return receipt, true, nil
}

func compareFederationReceipt(retained organization.FederationDeliveryReceipt, envelope organization.FederatedEnvelope, message organization.OrganizationalMessage) (organization.FederationDeliveryReceipt, bool, error) {
	if retained.DeliveryID != envelope.DeliveryID || retained.ReplayID != envelope.ReplayID || retained.RouteID != envelope.RouteID || retained.RouteRevision != envelope.RouteRevision || retained.MessageID != message.ID || retained.MessageSHA256 != envelope.MessageSHA256 {
		return organization.FederationDeliveryReceipt{}, false, organization.ErrFederationReplayConflict
	}
	return retained, false, nil
}
