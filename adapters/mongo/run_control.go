package mongo

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/tekroo-ai/teams/agentruntime"
	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
)

type runControlDocument struct {
	ID              string    `bson:"_id"`
	RequestDigest   string    `bson:"request_digest"`
	Owner           string    `bson:"owner"`
	Epoch           uint64    `bson:"epoch"`
	LeaseUntil      time.Time `bson:"lease_until"`
	CancelRequested bool      `bson:"cancel_requested"`
}

func validRunControl(id, digest string) bool {
	if strings.TrimSpace(id) == "" || len(digest) != 64 || digest != strings.ToLower(digest) {
		return false
	}
	decoded, err := hex.DecodeString(digest)
	return err == nil && len(decoded) == 32
}

func (s *Store) Claim(ctx context.Context, id, digest, owner string, now time.Time, ttl time.Duration) (agentruntime.RunLease, error) {
	if err := requireDeadline(ctx); err != nil {
		return agentruntime.RunLease{}, err
	}
	if !validRunControl(id, digest) || owner == "" || now.IsZero() || ttl <= 0 {
		return agentruntime.RunLease{}, agentruntime.ErrInvalidTurn
	}
	collection := s.db.Collection("agent_run_control")
	var current runControlDocument
	err := collection.FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&current)
	if errors.Is(err, driver.ErrNoDocuments) {
		candidate := runControlDocument{ID: id, RequestDigest: digest, Owner: owner, Epoch: 1, LeaseUntil: now.Add(ttl)}
		if _, err := collection.InsertOne(ctx, candidate); err != nil {
			if driver.IsDuplicateKeyError(err) {
				return agentruntime.RunLease{}, agentruntime.ErrConflict
			}
			return agentruntime.RunLease{}, err
		}
		return agentruntime.RunLease{Acquired: true, Epoch: 1}, nil
	}
	if err != nil {
		return agentruntime.RunLease{}, err
	}
	if current.RequestDigest != digest {
		return agentruntime.RunLease{}, agentruntime.ErrConflict
	}
	if current.Owner != "" && current.LeaseUntil.After(now) {
		return agentruntime.RunLease{CancelRequested: current.CancelRequested, Epoch: current.Epoch}, nil
	}
	result, err := collection.UpdateOne(ctx,
		bson.D{{Key: "_id", Value: id}, {Key: "request_digest", Value: digest}, {Key: "epoch", Value: current.Epoch}, {Key: "lease_until", Value: current.LeaseUntil}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "owner", Value: owner}, {Key: "lease_until", Value: now.Add(ttl)}}}, {Key: "$inc", Value: bson.D{{Key: "epoch", Value: 1}}}},
	)
	if err != nil {
		return agentruntime.RunLease{}, err
	}
	if result.MatchedCount != 1 {
		return agentruntime.RunLease{}, agentruntime.ErrConflict
	}
	return agentruntime.RunLease{Acquired: true, CancelRequested: current.CancelRequested, Epoch: current.Epoch + 1}, nil
}

func (s *Store) Renew(ctx context.Context, id, digest, owner string, epoch uint64, now time.Time, ttl time.Duration) (agentruntime.Renewal, error) {
	if err := requireDeadline(ctx); err != nil {
		return agentruntime.Renewal{}, err
	}
	if !validRunControl(id, digest) || owner == "" || epoch == 0 || now.IsZero() || ttl <= 0 {
		return agentruntime.Renewal{}, agentruntime.ErrInvalidTurn
	}
	collection := s.db.Collection("agent_run_control")
	filter := bson.D{{Key: "_id", Value: id}, {Key: "request_digest", Value: digest}, {Key: "owner", Value: owner}, {Key: "epoch", Value: epoch}, {Key: "lease_until", Value: bson.D{{Key: "$gt", Value: now}}}}
	result, err := collection.UpdateOne(ctx, filter, bson.D{{Key: "$set", Value: bson.D{{Key: "lease_until", Value: now.Add(ttl)}}}})
	if err != nil {
		return agentruntime.Renewal{}, err
	}
	if result.MatchedCount == 0 {
		return agentruntime.Renewal{}, nil
	}
	var current runControlDocument
	if err := collection.FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&current); err != nil {
		return agentruntime.Renewal{}, err
	}
	if current.RequestDigest != digest || current.Owner != owner || current.Epoch != epoch {
		return agentruntime.Renewal{}, nil
	}
	return agentruntime.Renewal{Held: true, CancelRequested: current.CancelRequested}, nil
}

func (s *Store) RequestCancel(ctx context.Context, id, digest string) error {
	if err := requireDeadline(ctx); err != nil {
		return err
	}
	if !validRunControl(id, digest) {
		return agentruntime.ErrInvalidTurn
	}
	collection := s.db.Collection("agent_run_control")
	result, err := collection.UpdateOne(ctx, bson.D{{Key: "_id", Value: id}, {Key: "request_digest", Value: digest}}, bson.D{{Key: "$set", Value: bson.D{{Key: "cancel_requested", Value: true}}}})
	if err != nil {
		return err
	}
	if result.MatchedCount != 0 {
		return nil
	}
	var existing runControlDocument
	if err := collection.FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&existing); err == nil {
		return agentruntime.ErrConflict
	} else if !errors.Is(err, driver.ErrNoDocuments) {
		return err
	}
	_, err = collection.InsertOne(ctx, runControlDocument{ID: id, RequestDigest: digest, CancelRequested: true})
	if driver.IsDuplicateKeyError(err) {
		var raced runControlDocument
		if readErr := collection.FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&raced); readErr != nil {
			return readErr
		}
		if raced.RequestDigest == digest {
			_, updateErr := collection.UpdateOne(ctx, bson.D{{Key: "_id", Value: id}, {Key: "request_digest", Value: digest}}, bson.D{{Key: "$set", Value: bson.D{{Key: "cancel_requested", Value: true}}}})
			return updateErr
		}
		return agentruntime.ErrConflict
	}
	return err
}

func (s *Store) Release(ctx context.Context, id, digest, owner string, epoch uint64, now time.Time) error {
	if err := requireDeadline(ctx); err != nil {
		return err
	}
	if !validRunControl(id, digest) || owner == "" || epoch == 0 || now.IsZero() {
		return agentruntime.ErrInvalidTurn
	}
	result, err := s.db.Collection("agent_run_control").UpdateOne(ctx,
		bson.D{{Key: "_id", Value: id}, {Key: "request_digest", Value: digest}, {Key: "owner", Value: owner}, {Key: "epoch", Value: epoch}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "owner", Value: ""}, {Key: "lease_until", Value: now}}}},
	)
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return agentruntime.ErrConflict
	}
	return nil
}
