package mongo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/tekroo-ai/teams/agenttools"
	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
)

type toolEffectDocument struct {
	ID               string `bson:"_id"`
	InvocationID     string `bson:"invocation_id"`
	RequestDigest    string `bson:"request_digest"`
	ToolCallID       string `bson:"tool_call_id"`
	Name             string `bson:"name"`
	ArgumentsSHA256  string `bson:"arguments_sha256"`
	WorkspaceRoot    string `bson:"workspace_root"`
	EffectPolicyHash string `bson:"effect_policy_digest"`
	Result           []byte `bson:"result,omitempty"`
}

var _ agenttools.EffectLedger = (*Store)(nil)

func toolEffectID(invocationID, callID string) string {
	sum := sha256.Sum256([]byte(invocationID + "\x00" + callID))
	return hex.EncodeToString(sum[:])
}

func toolEffectCandidate(record agenttools.EffectRecord) (toolEffectDocument, error) {
	if !record.InvocationID.Valid() || !record.RequestDigest.Valid() || strings.TrimSpace(record.ToolCallID) == "" || len(record.ToolCallID) > 256 || record.Name == "" || len(record.ArgumentsSHA256) != 64 || record.WorkspaceRoot == "" || !record.EffectPolicyHash.Valid() {
		return toolEffectDocument{}, agenttools.ErrInvalidCall
	}
	if _, err := hex.DecodeString(record.ArgumentsSHA256); err != nil || strings.ToLower(record.ArgumentsSHA256) != record.ArgumentsSHA256 {
		return toolEffectDocument{}, agenttools.ErrInvalidCall
	}
	return toolEffectDocument{
		ID: toolEffectID(string(record.InvocationID), record.ToolCallID), InvocationID: string(record.InvocationID),
		RequestDigest: string(record.RequestDigest), ToolCallID: record.ToolCallID, Name: record.Name,
		ArgumentsSHA256: record.ArgumentsSHA256, WorkspaceRoot: record.WorkspaceRoot,
		EffectPolicyHash: string(record.EffectPolicyHash),
	}, nil
}

func sameEffectIdentity(left, right toolEffectDocument) bool {
	return left.ID == right.ID && left.InvocationID == right.InvocationID && left.RequestDigest == right.RequestDigest && left.ToolCallID == right.ToolCallID && left.Name == right.Name && left.ArgumentsSHA256 == right.ArgumentsSHA256 && left.WorkspaceRoot == right.WorkspaceRoot && left.EffectPolicyHash == right.EffectPolicyHash
}

func (s *Store) Reserve(ctx context.Context, record agenttools.EffectRecord) (agenttools.EffectRecord, bool, error) {
	if err := requireDeadline(ctx); err != nil {
		return agenttools.EffectRecord{}, false, err
	}
	candidate, err := toolEffectCandidate(record)
	if err != nil {
		return agenttools.EffectRecord{}, false, err
	}
	collection := s.db.Collection("agent_tool_effects")
	if _, err := collection.InsertOne(ctx, candidate); err == nil {
		return record, true, nil
	} else if !driver.IsDuplicateKeyError(err) {
		return agenttools.EffectRecord{}, false, err
	}
	var existing toolEffectDocument
	if err := collection.FindOne(ctx, bson.D{{Key: "_id", Value: candidate.ID}}).Decode(&existing); err != nil {
		return agenttools.EffectRecord{}, false, err
	}
	if !sameEffectIdentity(existing, candidate) {
		return agenttools.EffectRecord{}, false, agenttools.ErrConflict
	}
	record.Result = append(json.RawMessage(nil), existing.Result...)
	if len(record.Result) > 0 && !json.Valid(record.Result) {
		return agenttools.EffectRecord{}, false, agenttools.ErrEffectUncertain
	}
	return record, false, nil
}

func (s *Store) Lookup(ctx context.Context, record agenttools.EffectRecord) (agenttools.EffectRecord, bool, error) {
	if err := requireDeadline(ctx); err != nil {
		return agenttools.EffectRecord{}, false, err
	}
	candidate, err := toolEffectCandidate(record)
	if err != nil {
		return agenttools.EffectRecord{}, false, err
	}
	var existing toolEffectDocument
	err = s.db.Collection("agent_tool_effects").FindOne(ctx, bson.D{{Key: "_id", Value: candidate.ID}}).Decode(&existing)
	if errors.Is(err, driver.ErrNoDocuments) {
		return record, false, nil
	}
	if err != nil {
		return agenttools.EffectRecord{}, false, err
	}
	if !sameEffectIdentity(existing, candidate) {
		return agenttools.EffectRecord{}, false, agenttools.ErrConflict
	}
	record.Result = append(json.RawMessage(nil), existing.Result...)
	if len(record.Result) > 0 && !json.Valid(record.Result) {
		return agenttools.EffectRecord{}, false, agenttools.ErrEffectUncertain
	}
	return record, true, nil
}

func (s *Store) Complete(ctx context.Context, record agenttools.EffectRecord, result json.RawMessage) error {
	if err := requireDeadline(ctx); err != nil {
		return err
	}
	candidate, err := toolEffectCandidate(record)
	if err != nil || !json.Valid(result) || len(result) > 1<<20 {
		return agenttools.ErrInvalidCall
	}
	collection := s.db.Collection("agent_tool_effects")
	filter := bson.D{{Key: "_id", Value: candidate.ID}, {Key: "request_digest", Value: candidate.RequestDigest}, {Key: "tool_call_id", Value: candidate.ToolCallID}, {Key: "name", Value: candidate.Name}, {Key: "arguments_sha256", Value: candidate.ArgumentsSHA256}, {Key: "workspace_root", Value: candidate.WorkspaceRoot}, {Key: "effect_policy_digest", Value: candidate.EffectPolicyHash}, {Key: "result", Value: bson.D{{Key: "$exists", Value: false}}}}
	updated, err := collection.UpdateOne(ctx, filter, bson.D{{Key: "$set", Value: bson.D{{Key: "result", Value: []byte(result)}}}})
	if err != nil {
		return err
	}
	if updated.MatchedCount == 1 {
		return nil
	}
	var existing toolEffectDocument
	if err := collection.FindOne(ctx, bson.D{{Key: "_id", Value: candidate.ID}}).Decode(&existing); err != nil {
		if errors.Is(err, driver.ErrNoDocuments) {
			return agenttools.ErrConflict
		}
		return err
	}
	if !sameEffectIdentity(existing, candidate) || !bytes.Equal(existing.Result, result) {
		return agenttools.ErrConflict
	}
	return nil
}
