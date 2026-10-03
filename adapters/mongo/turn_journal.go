package mongo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tekroo-ai/teams/agentruntime"
	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

type turnHeadDocument struct {
	ID       string `bson:"_id"`
	Sequence uint64 `bson:"sequence"`
}

type turnEntryDocument struct {
	ID           string `bson:"_id"`
	InvocationID string `bson:"invocation_id"`
	Sequence     uint64 `bson:"sequence"`
	Kind         string `bson:"kind"`
	Payload      []byte `bson:"payload"`
}

// Load reads the Teams-owned execution transcript. This collection is separate
// from organizational events and is not an SMA memory collection.
func (s *Store) Load(ctx context.Context, invocationID string) ([]agentruntime.Entry, error) {
	if err := requireDeadline(ctx); err != nil {
		return nil, err
	}
	if strings.TrimSpace(invocationID) == "" {
		return nil, agentruntime.ErrInvalidTurn
	}
	cursor, err := s.db.Collection("agent_turn_entries").Find(ctx, bson.D{{Key: "invocation_id", Value: invocationID}}, options.Find().SetSort(bson.D{{Key: "sequence", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	entries := make([]agentruntime.Entry, 0)
	for cursor.Next(ctx) {
		var document turnEntryDocument
		if err := cursor.Decode(&document); err != nil {
			return nil, err
		}
		if document.InvocationID != invocationID || document.Sequence != uint64(len(entries)+1) || !json.Valid(document.Payload) {
			return nil, agentruntime.ErrInvalidTurn
		}
		entries = append(entries, agentruntime.Entry{InvocationID: invocationID, Sequence: document.Sequence, Kind: agentruntime.Kind(document.Kind), Payload: append(json.RawMessage(nil), document.Payload...)})
	}
	return entries, cursor.Err()
}

// Append atomically advances the per-invocation sequence and inserts its event.
// A failed or uncertain commit is resolved by the runner through Load; it must
// never be treated as permission to repeat a model/tool effect blindly.
func (s *Store) Append(ctx context.Context, invocationID string, expected uint64, kind agentruntime.Kind, payload json.RawMessage) (agentruntime.Entry, error) {
	if err := requireDeadline(ctx); err != nil {
		return agentruntime.Entry{}, err
	}
	if strings.TrimSpace(invocationID) == "" || kind == "" || !json.Valid(payload) || len(payload) > 1<<20 {
		return agentruntime.Entry{}, agentruntime.ErrInvalidTurn
	}
	entry := agentruntime.Entry{InvocationID: invocationID, Sequence: expected + 1, Kind: kind, Payload: append(json.RawMessage(nil), payload...)}
	session, err := s.client.StartSession()
	if err != nil {
		return agentruntime.Entry{}, err
	}
	defer session.EndSession(ctx)
	_, err = session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		heads := s.db.Collection("agent_turn_heads")
		if expected == 0 {
			if _, err := heads.InsertOne(tx, turnHeadDocument{ID: invocationID, Sequence: 1}); err != nil {
				return nil, err
			}
		} else {
			result, err := heads.UpdateOne(tx, bson.D{{Key: "_id", Value: invocationID}, {Key: "sequence", Value: expected}}, bson.D{{Key: "$set", Value: bson.D{{Key: "sequence", Value: expected + 1}}}})
			if err != nil {
				return nil, err
			}
			if result.MatchedCount != 1 {
				return nil, agentruntime.ErrConflict
			}
		}
		_, err := s.db.Collection("agent_turn_entries").InsertOne(tx, turnEntryDocument{ID: fmt.Sprintf("%s:%020d", invocationID, expected+1), InvocationID: invocationID, Sequence: expected + 1, Kind: string(kind), Payload: payload})
		return nil, err
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()).SetReadPreference(readpref.Primary()))
	if err != nil {
		if driver.IsDuplicateKeyError(err) || errors.Is(err, agentruntime.ErrConflict) {
			return agentruntime.Entry{}, agentruntime.ErrConflict
		}
		return agentruntime.Entry{}, err
	}
	return entry, nil
}
