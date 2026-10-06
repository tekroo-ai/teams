package mongo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/kernel"
	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
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
	PreparedSHA256   string `bson:"prepared_sha256,omitempty"`
	FilePath         string `bson:"file_path,omitempty"`
	HasPreimage      bool   `bson:"has_preimage,omitempty"`
	BeforeExists     bool   `bson:"before_exists,omitempty"`
	BeforeContent    []byte `bson:"before_content,omitempty"`
	BeforeSHA256     string `bson:"before_sha256,omitempty"`
	DesiredAbsent    bool   `bson:"desired_absent,omitempty"`
	Result           []byte `bson:"result,omitempty"`
}

var _ agenttools.EffectLedger = (*Store)(nil)
var _ agenttools.FileChangeLookup = (*Store)(nil)

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
	if record.PreparedSHA256 != "" {
		if _, err := hex.DecodeString(record.PreparedSHA256); err != nil || len(record.PreparedSHA256) != 64 || strings.ToLower(record.PreparedSHA256) != record.PreparedSHA256 {
			return toolEffectDocument{}, agenttools.ErrInvalidCall
		}
	}
	if record.HasPreimage {
		if record.FilePath == "" || len(record.BeforeContent) > 1<<20 || record.PreparedSHA256 == "" {
			return toolEffectDocument{}, agenttools.ErrInvalidCall
		}
		sum := sha256.Sum256(record.BeforeContent)
		if record.BeforeExists && record.BeforeSHA256 != hex.EncodeToString(sum[:]) || !record.BeforeExists && (len(record.BeforeContent) != 0 || record.BeforeSHA256 != "") {
			return toolEffectDocument{}, agenttools.ErrInvalidCall
		}
	}
	return toolEffectDocument{
		ID: toolEffectID(string(record.InvocationID), record.ToolCallID), InvocationID: string(record.InvocationID),
		RequestDigest: string(record.RequestDigest), ToolCallID: record.ToolCallID, Name: record.Name,
		ArgumentsSHA256: record.ArgumentsSHA256, WorkspaceRoot: record.WorkspaceRoot,
		EffectPolicyHash: string(record.EffectPolicyHash), PreparedSHA256: record.PreparedSHA256,
		FilePath: record.FilePath, HasPreimage: record.HasPreimage, BeforeExists: record.BeforeExists,
		BeforeContent: append([]byte(nil), record.BeforeContent...), BeforeSHA256: record.BeforeSHA256, DesiredAbsent: record.DesiredAbsent,
	}, nil
}

func sameEffectSnapshot(left, right toolEffectDocument) bool {
	return left.PreparedSHA256 == right.PreparedSHA256 && left.FilePath == right.FilePath && left.HasPreimage == right.HasPreimage && left.BeforeExists == right.BeforeExists && bytes.Equal(left.BeforeContent, right.BeforeContent) && left.BeforeSHA256 == right.BeforeSHA256 && left.DesiredAbsent == right.DesiredAbsent
}

func retainedEffect(document toolEffectDocument) (agenttools.EffectRecord, error) {
	record := agenttools.EffectRecord{InvocationID: kernel.UUIDv7(document.InvocationID), RequestDigest: kernel.Digest(document.RequestDigest), ToolCallID: document.ToolCallID, Name: document.Name, ArgumentsSHA256: document.ArgumentsSHA256, WorkspaceRoot: document.WorkspaceRoot, EffectPolicyHash: kernel.Digest(document.EffectPolicyHash), PreparedSHA256: document.PreparedSHA256, FilePath: document.FilePath, HasPreimage: document.HasPreimage, BeforeExists: document.BeforeExists, BeforeContent: append([]byte(nil), document.BeforeContent...), BeforeSHA256: document.BeforeSHA256, DesiredAbsent: document.DesiredAbsent, Result: append(json.RawMessage(nil), document.Result...)}
	if _, err := toolEffectCandidate(record); err != nil || len(record.Result) > 0 && !json.Valid(record.Result) {
		return agenttools.EffectRecord{}, agenttools.ErrEffectUncertain
	}
	return record, nil
}

func (s *Store) LoadFileChange(ctx context.Context, id string) (agenttools.EffectRecord, bool, error) {
	if err := requireDeadline(ctx); err != nil {
		return agenttools.EffectRecord{}, false, err
	}
	if decoded, err := hex.DecodeString(id); err != nil || len(decoded) != 32 || strings.ToLower(id) != id {
		return agenttools.EffectRecord{}, false, agenttools.ErrInvalidCall
	}
	var document toolEffectDocument
	err := s.db.Collection("agent_tool_effects").FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&document)
	if errors.Is(err, driver.ErrNoDocuments) {
		return agenttools.EffectRecord{}, false, nil
	}
	if err != nil {
		return agenttools.EffectRecord{}, false, err
	}
	record, err := retainedEffect(document)
	return record, err == nil, err
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
	if record.Name == "edit_file" && record.PreparedSHA256 == "" || (record.Name == "create_file" || record.Name == "undo_file_change" || record.Name == "restore_file_from_commit") && !record.HasPreimage {
		return agenttools.EffectRecord{}, false, agenttools.ErrInvalidCall
	}
	if _, fenced := agentruntime.RunOwnershipFromContext(ctx); fenced {
		return s.reserveOwnedEffect(ctx, record, candidate)
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
	if !sameEffectIdentity(existing, candidate) || !sameEffectSnapshot(existing, candidate) {
		return agenttools.EffectRecord{}, false, agenttools.ErrConflict
	}
	record, err = retainedEffect(existing)
	return record, false, err
}

func (s *Store) reserveOwnedEffect(ctx context.Context, record agenttools.EffectRecord, candidate toolEffectDocument) (agenttools.EffectRecord, bool, error) {
	session, err := s.client.StartSession()
	if err != nil {
		return agenttools.EffectRecord{}, false, err
	}
	defer session.EndSession(ctx)
	var created bool
	var stored toolEffectDocument
	_, err = session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		created = false
		if err := s.fenceRunWrite(tx, candidate.InvocationID, candidate.RequestDigest, false); err != nil {
			return nil, err
		}
		collection := s.db.Collection("agent_tool_effects")
		err := collection.FindOne(tx, bson.D{{Key: "_id", Value: candidate.ID}}).Decode(&stored)
		if errors.Is(err, driver.ErrNoDocuments) {
			stored = candidate
			_, err = collection.InsertOne(tx, candidate)
			created = err == nil
			return nil, err
		}
		if err != nil {
			return nil, err
		}
		if !sameEffectIdentity(stored, candidate) || !sameEffectSnapshot(stored, candidate) {
			return nil, agenttools.ErrConflict
		}
		return nil, nil
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()).SetReadPreference(readpref.Primary()))
	if err != nil {
		return agenttools.EffectRecord{}, false, err
	}
	record, err = retainedEffect(stored)
	return record, created, err
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
	record, err = retainedEffect(existing)
	return record, err == nil, err
}

func (s *Store) Complete(ctx context.Context, record agenttools.EffectRecord, result json.RawMessage) error {
	if err := requireDeadline(ctx); err != nil {
		return err
	}
	candidate, err := toolEffectCandidate(record)
	if err != nil || !json.Valid(result) || len(result) > 1<<20 {
		return agenttools.ErrInvalidCall
	}
	if record.Name == "edit_file" && record.PreparedSHA256 == "" || (record.Name == "create_file" || record.Name == "undo_file_change" || record.Name == "restore_file_from_commit") && !record.HasPreimage {
		return agenttools.ErrInvalidCall
	}
	collection := s.db.Collection("agent_tool_effects")
	filter := bson.D{{Key: "_id", Value: candidate.ID}, {Key: "request_digest", Value: candidate.RequestDigest}, {Key: "tool_call_id", Value: candidate.ToolCallID}, {Key: "name", Value: candidate.Name}, {Key: "arguments_sha256", Value: candidate.ArgumentsSHA256}, {Key: "workspace_root", Value: candidate.WorkspaceRoot}, {Key: "effect_policy_digest", Value: candidate.EffectPolicyHash}, {Key: "result", Value: bson.D{{Key: "$exists", Value: false}}}}
	if candidate.PreparedSHA256 != "" {
		filter = append(filter, bson.E{Key: "prepared_sha256", Value: candidate.PreparedSHA256})
	}
	if candidate.HasPreimage {
		filter = append(filter, bson.E{Key: "has_preimage", Value: true}, bson.E{Key: "file_path", Value: candidate.FilePath})
		for key, value := range map[string]bool{"before_exists": candidate.BeforeExists, "desired_absent": candidate.DesiredAbsent} {
			if value {
				filter = append(filter, bson.E{Key: key, Value: true})
			} else {
				filter = append(filter, bson.E{Key: key, Value: bson.D{{Key: "$ne", Value: true}}})
			}
		}
		if candidate.BeforeExists {
			filter = append(filter, bson.E{Key: "before_sha256", Value: candidate.BeforeSHA256})
			if len(candidate.BeforeContent) > 0 {
				filter = append(filter, bson.E{Key: "before_content", Value: candidate.BeforeContent})
			}
		}
	}
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
	if !sameEffectIdentity(existing, candidate) || !sameEffectSnapshot(existing, candidate) || !bytes.Equal(existing.Result, result) {
		return agenttools.ErrConflict
	}
	return nil
}
