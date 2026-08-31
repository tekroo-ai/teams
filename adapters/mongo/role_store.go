package mongo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type roleInstanceDocument struct {
	ID       string `bson:"_id"`
	Team     string `bson:"team"`
	Revision uint64 `bson:"revision"`
	Status   string `bson:"status"`
	Data     []byte `bson:"data"`
}

// LoadRole reads the durable state for one exact actor identity.
func (s *Store) LoadRole(ctx context.Context, actor kernel.ActorFQN) (organization.RoleInstanceState, bool, error) {
	if err := requireDeadline(ctx); err != nil {
		return organization.RoleInstanceState{}, false, err
	}
	if s == nil || s.db == nil || !actor.Valid() {
		return organization.RoleInstanceState{}, false, organization.ErrRoleNotConfigured
	}
	var document roleInstanceDocument
	err := s.db.Collection("role_instances").FindOne(ctx, bson.D{{Key: "_id", Value: string(actor)}}).Decode(&document)
	if errors.Is(err, driver.ErrNoDocuments) {
		return organization.RoleInstanceState{}, false, nil
	}
	if err != nil {
		return organization.RoleInstanceState{}, false, err
	}
	state, err := decodeRoleInstance(document)
	if err != nil {
		return organization.RoleInstanceState{}, false, err
	}
	return state, true, nil
}

// CompareAndSwapRole persists one role transition if and only if the caller
// still owns the expected revision. Creation uses revision zero.
func (s *Store) CompareAndSwapRole(ctx context.Context, expectedRevision uint64, state organization.RoleInstanceState) error {
	if err := requireDeadline(ctx); err != nil {
		return err
	}
	if s == nil || s.db == nil || !state.Valid() || state.Revision != expectedRevision+1 {
		return organization.ErrRoleStateConflict
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	document := roleInstanceDocument{ID: string(state.ActorFQN), Team: state.Team, Revision: state.Revision, Status: string(state.Status), Data: raw}
	collection := s.db.Collection("role_instances")
	if expectedRevision == 0 {
		_, err = collection.InsertOne(ctx, document)
		if driver.IsDuplicateKeyError(err) {
			return organization.ErrRoleStateConflict
		}
		return err
	}
	result, err := collection.ReplaceOne(ctx, bson.D{{Key: "_id", Value: document.ID}, {Key: "revision", Value: expectedRevision}}, document)
	if err != nil {
		return err
	}
	if result.MatchedCount != 1 {
		return organization.ErrRoleStateConflict
	}
	return nil
}

// ListRoles returns one team's roster in stable actor-FQN order.
func (s *Store) ListRoles(ctx context.Context, team string) ([]organization.RoleInstanceState, error) {
	if err := requireDeadline(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.db == nil || team == "" {
		return nil, organization.ErrRoleNotConfigured
	}
	cursor, err := s.db.Collection("role_instances").Find(ctx, bson.D{{Key: "team", Value: team}}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	states := make([]organization.RoleInstanceState, 0)
	for cursor.Next(ctx) {
		var document roleInstanceDocument
		if err := cursor.Decode(&document); err != nil {
			return nil, err
		}
		state, err := decodeRoleInstance(document)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}
	return states, nil
}

func decodeRoleInstance(document roleInstanceDocument) (organization.RoleInstanceState, error) {
	decoder := json.NewDecoder(bytes.NewReader(document.Data))
	decoder.DisallowUnknownFields()
	var state organization.RoleInstanceState
	if err := decoder.Decode(&state); err != nil {
		return organization.RoleInstanceState{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return organization.RoleInstanceState{}, organization.ErrRoleStateConflict
	}
	if !state.Valid() || string(state.ActorFQN) != document.ID || state.Team != document.Team || state.Revision != document.Revision || string(state.Status) != document.Status {
		return organization.RoleInstanceState{}, organization.ErrRoleStateConflict
	}
	return state, nil
}
