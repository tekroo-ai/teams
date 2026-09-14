package mongo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type OrganizationalMessageFeed struct {
	store          *Store
	consumerID     string
	recipient      kernel.ActorFQN
	stream         *driver.ChangeStream
	backlog        *driver.Cursor
	seen           map[string]struct{}
	backlogSeen    int64
	backlogLimit   int64
	resynchronized bool
}

// OpenOrganizationalMessageFeed opens the recipient-scoped change stream
// before querying pending backlog. This ordering closes the startup race while
// retaining MongoDB's lightweight blocking wakeup.
func (s *Store) OpenOrganizationalMessageFeed(ctx context.Context, consumerID string, recipient kernel.ActorFQN) (*OrganizationalMessageFeed, error) {
	if err := requireDeadline(ctx); err != nil {
		return nil, err
	}
	if consumerID == "" || !recipient.Valid() {
		return nil, organization.ErrInvalidOrganizationalMessage
	}
	checkpointID := "organizational-message:" + consumerID + ":" + string(recipient)
	maxAwait := s.organizationalMessageMaxAwait
	if maxAwait <= 0 {
		maxAwait = time.Minute
	}
	streamOptions := options.ChangeStream().SetFullDocument(options.UpdateLookup).SetMaxAwaitTime(maxAwait)
	var checkpoint valueDocument
	err := s.db.Collection("consumer_checkpoints").FindOne(ctx, bson.D{{Key: "_id", Value: checkpointID}}).Decode(&checkpoint)
	usedCheckpoint := false
	if err == nil && len(checkpoint.Data) > 0 {
		streamOptions.SetResumeAfter(bson.Raw(checkpoint.Data))
		usedCheckpoint = true
	} else if err != nil && !errors.Is(err, driver.ErrNoDocuments) {
		return nil, err
	}
	pipeline := organizationalMessagePipeline(recipient)
	stream, err := s.db.Collection("organizational_messages").Watch(ctx, pipeline, streamOptions)
	resynchronized := false
	if err != nil && usedCheckpoint && isResumeFailure(err) {
		if _, deleteErr := s.db.Collection("consumer_checkpoints").DeleteOne(ctx, bson.D{{Key: "_id", Value: checkpointID}}); deleteErr != nil {
			return nil, deleteErr
		}
		stream, err = s.db.Collection("organizational_messages").Watch(ctx, pipeline, options.ChangeStream().SetFullDocument(options.UpdateLookup).SetMaxAwaitTime(maxAwait))
		resynchronized = true
	}
	if err != nil {
		return nil, err
	}
	backlog, err := s.db.Collection("organizational_messages").Find(ctx, bson.D{{Key: "recipient", Value: string(recipient)}, {Key: "state", Value: organization.MessagePending}}, options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}, {Key: "_id", Value: 1}}).SetLimit(s.backlogLimit+1))
	if err != nil {
		_ = stream.Close(ctx)
		return nil, err
	}
	return &OrganizationalMessageFeed{store: s, consumerID: checkpointID, recipient: recipient, stream: stream, backlog: backlog, seen: make(map[string]struct{}), backlogLimit: s.backlogLimit, resynchronized: resynchronized}, nil
}

func (feed *OrganizationalMessageFeed) Close(ctx context.Context) error {
	_ = feed.backlog.Close(ctx)
	return feed.stream.Close(ctx)
}

func (feed *OrganizationalMessageFeed) Resynchronized() bool { return feed.resynchronized }

func (feed *OrganizationalMessageFeed) Poll(ctx context.Context) (organization.OrganizationalMessage, error) {
	if err := requireDeadline(ctx); err != nil {
		return organization.OrganizationalMessage{}, err
	}
	for feed.backlog.Next(ctx) {
		feed.backlogSeen++
		if feed.backlogSeen > feed.backlogLimit {
			return organization.OrganizationalMessage{}, ErrBacklogLimit
		}
		var document organizationalMessageDocument
		if err := feed.backlog.Decode(&document); err != nil {
			return organization.OrganizationalMessage{}, err
		}
		if message, ok, err := feed.accept(document); ok || err != nil {
			return message, err
		}
	}
	if err := feed.backlog.Err(); err != nil {
		if ctx.Err() != nil || driver.IsTimeout(err) {
			if ctx.Err() == nil {
				return organization.OrganizationalMessage{}, context.DeadlineExceeded
			}
			return organization.OrganizationalMessage{}, ctx.Err()
		}
		return organization.OrganizationalMessage{}, err
	}
	for feed.stream.TryNext(ctx) {
		var change struct {
			FullDocument organizationalMessageDocument `bson:"fullDocument"`
		}
		if err := feed.stream.Decode(&change); err != nil {
			return organization.OrganizationalMessage{}, err
		}
		if err := feed.saveCheckpoint(ctx); err != nil {
			return organization.OrganizationalMessage{}, err
		}
		if message, ok, err := feed.accept(change.FullDocument); ok || err != nil {
			return message, err
		}
	}
	if err := feed.stream.Err(); err != nil {
		if ctx.Err() != nil || driver.IsTimeout(err) {
			if ctx.Err() == nil {
				return organization.OrganizationalMessage{}, context.DeadlineExceeded
			}
			return organization.OrganizationalMessage{}, ctx.Err()
		}
		return organization.OrganizationalMessage{}, err
	}
	return organization.OrganizationalMessage{}, organization.ErrOrganizationalMessageNotFound
}

func (feed *OrganizationalMessageFeed) accept(document organizationalMessageDocument) (organization.OrganizationalMessage, bool, error) {
	digest := sha256.Sum256(document.Data)
	key := document.ID + ":" + strconv.FormatUint(document.ClaimEpoch, 10) + ":" + hex.EncodeToString(digest[:])
	if _, duplicate := feed.seen[key]; duplicate {
		return organization.OrganizationalMessage{}, false, nil
	}
	feed.seen[key] = struct{}{}
	claim, err := decodeOrganizationalClaim(document)
	if err != nil {
		return organization.OrganizationalMessage{}, false, err
	}
	if claim.Message.Recipient != feed.recipient {
		return organization.OrganizationalMessage{}, false, organization.ErrOrganizationalMessageConflict
	}
	return claim.Message, true, nil
}

func (feed *OrganizationalMessageFeed) saveCheckpoint(ctx context.Context) error {
	token := feed.stream.ResumeToken()
	if len(token) == 0 {
		return nil
	}
	_, err := feed.store.db.Collection("consumer_checkpoints").ReplaceOne(ctx, bson.D{{Key: "_id", Value: feed.consumerID}}, valueDocument{ID: feed.consumerID, Data: append([]byte(nil), token...)}, options.Replace().SetUpsert(true))
	return err
}

func organizationalMessagePipeline(recipient kernel.ActorFQN) driver.Pipeline {
	return driver.Pipeline{bson.D{{Key: "$match", Value: bson.D{
		{Key: "operationType", Value: bson.D{{Key: "$in", Value: bson.A{"insert", "update", "replace"}}}},
		{Key: "fullDocument.recipient", Value: string(recipient)},
		{Key: "fullDocument.state", Value: organization.MessagePending},
	}}}}
}
