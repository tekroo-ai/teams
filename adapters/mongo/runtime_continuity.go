package mongo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

var ErrRuntimeContinuityConflict = errors.New("runtime continuity state conflict")

const (
	runtimeSessionRunning = "RUNNING"
	runtimeSessionStopped = "STOPPED"
)

// RuntimeSuspensionWindow is an observed interval during which this deployment
// could not advance local work. It is infrastructure state, not semantic memory
// or task state. ID is content-addressed so a retried observation cannot create
// a second deadline allowance for the same interval.
type RuntimeSuspensionWindow struct {
	ID                 kernel.Digest `json:"id" bson:"_id"`
	DeploymentIdentity kernel.Digest `json:"deployment_identity" bson:"deployment_identity"`
	SuspendedAt        time.Time     `json:"suspended_at" bson:"suspended_at"`
	ResumedAt          time.Time     `json:"resumed_at" bson:"resumed_at"`
}

func (window RuntimeSuspensionWindow) Valid() bool {
	return window.ID.Valid() && window.DeploymentIdentity.Valid() && !window.SuspendedAt.IsZero() && window.ResumedAt.After(window.SuspendedAt) && window.ID == runtimeSuspensionID(window.DeploymentIdentity, window.SuspendedAt, window.ResumedAt)
}

func (window RuntimeSuspensionWindow) Duration() time.Duration {
	if !window.Valid() {
		return 0
	}
	return window.ResumedAt.Sub(window.SuspendedAt)
}

type runtimeSessionDocument struct {
	ID                        string    `bson:"_id"`
	Revision                  uint64    `bson:"revision"`
	SessionID                 string    `bson:"session_id"`
	State                     string    `bson:"state"`
	LastHeartbeatAt           time.Time `bson:"last_heartbeat_at"`
	SuspendedAt               time.Time `bson:"suspended_at,omitempty"`
	SuspensionThresholdMillis int64     `bson:"suspension_threshold_millis"`
}

func runtimeSuspensionID(deployment kernel.Digest, suspendedAt, resumedAt time.Time) kernel.Digest {
	suspendedAt = suspendedAt.UTC().Truncate(time.Millisecond)
	resumedAt = resumedAt.UTC().Truncate(time.Millisecond)
	hash := sha256.Sum256([]byte(string(deployment) + "\x00" + suspendedAt.UTC().Format(time.RFC3339Nano) + "\x00" + resumedAt.UTC().Format(time.RFC3339Nano)))
	return kernel.Digest(hex.EncodeToString(hash[:]))
}

// StartRuntimeSession records a new process session and returns the exact
// unavailability window since the prior process heartbeat or clean stop.
func (s *Store) StartRuntimeSession(ctx context.Context, deployment kernel.Digest, session kernel.UUIDv7, now time.Time, threshold time.Duration) (RuntimeSuspensionWindow, bool, error) {
	if threshold <= 0 || threshold.Milliseconds() <= 0 {
		return RuntimeSuspensionWindow{}, false, ErrRuntimeContinuityConflict
	}
	return s.advanceRuntimeSession(ctx, deployment, session, now, threshold, true)
}

// HeartbeatRuntimeSession advances one live process heartbeat. A wall-clock gap
// larger than threshold is recorded as a suspension, including when the same
// process survives a laptop sleep.
func (s *Store) HeartbeatRuntimeSession(ctx context.Context, deployment kernel.Digest, session kernel.UUIDv7, now time.Time, threshold time.Duration) (RuntimeSuspensionWindow, bool, error) {
	if threshold <= 0 {
		return RuntimeSuspensionWindow{}, false, ErrRuntimeContinuityConflict
	}
	return s.advanceRuntimeSession(ctx, deployment, session, now, threshold, false)
}

func (s *Store) advanceRuntimeSession(ctx context.Context, deployment kernel.Digest, session kernel.UUIDv7, now time.Time, threshold time.Duration, starting bool) (RuntimeSuspensionWindow, bool, error) {
	if err := requireDeadline(ctx); err != nil {
		return RuntimeSuspensionWindow{}, false, err
	}
	if s == nil || s.client == nil || !deployment.Valid() || !session.Valid() || now.IsZero() {
		return RuntimeSuspensionWindow{}, false, ErrRuntimeContinuityConflict
	}
	now = now.UTC().Truncate(time.Millisecond)
	var observed RuntimeSuspensionWindow
	var foundWindow bool
	driverSession, err := s.client.StartSession()
	if err != nil {
		return RuntimeSuspensionWindow{}, false, err
	}
	defer driverSession.EndSession(ctx)
	_, err = driverSession.WithTransaction(ctx, func(transactionContext context.Context) (any, error) {
		collection := s.db.Collection("runtime_sessions")
		var prior runtimeSessionDocument
		loadErr := collection.FindOne(transactionContext, bson.D{{Key: "_id", Value: string(deployment)}}).Decode(&prior)
		if errors.Is(loadErr, driver.ErrNoDocuments) {
			if !starting {
				return nil, ErrRuntimeContinuityConflict
			}
			_, insertErr := collection.InsertOne(transactionContext, runtimeSessionDocument{ID: string(deployment), Revision: 1, SessionID: string(session), State: runtimeSessionRunning, LastHeartbeatAt: now, SuspensionThresholdMillis: threshold.Milliseconds()})
			return nil, insertErr
		}
		if loadErr != nil {
			return nil, loadErr
		}
		if prior.Revision == 0 || prior.LastHeartbeatAt.IsZero() || prior.State != runtimeSessionRunning && prior.State != runtimeSessionStopped {
			return nil, ErrRuntimeContinuityConflict
		}
		if !starting && (prior.SessionID != string(session) || prior.State != runtimeSessionRunning) {
			return nil, ErrRuntimeContinuityConflict
		}
		if !starting && prior.SuspensionThresholdMillis != 0 && prior.SuspensionThresholdMillis != threshold.Milliseconds() {
			return nil, ErrRuntimeContinuityConflict
		}
		suspendedAt := prior.LastHeartbeatAt.UTC()
		if prior.State == runtimeSessionStopped {
			if prior.SuspendedAt.IsZero() {
				return nil, ErrRuntimeContinuityConflict
			}
			suspendedAt = prior.SuspendedAt.UTC()
		}
		observe := now.After(suspendedAt) && (starting || now.Sub(suspendedAt) > threshold)
		if observe {
			observed = RuntimeSuspensionWindow{ID: runtimeSuspensionID(deployment, suspendedAt, now), DeploymentIdentity: deployment, SuspendedAt: suspendedAt, ResumedAt: now}
			if !observed.Valid() {
				return nil, ErrRuntimeContinuityConflict
			}
			if _, insertErr := s.db.Collection("runtime_suspensions").InsertOne(transactionContext, observed); insertErr != nil && !driver.IsDuplicateKeyError(insertErr) {
				return nil, insertErr
			}
			foundWindow = true
		}
		next := runtimeSessionDocument{ID: prior.ID, Revision: prior.Revision + 1, SessionID: string(session), State: runtimeSessionRunning, LastHeartbeatAt: now, SuspensionThresholdMillis: threshold.Milliseconds()}
		result, replaceErr := collection.ReplaceOne(transactionContext, bson.D{{Key: "_id", Value: prior.ID}, {Key: "revision", Value: prior.Revision}}, next)
		if replaceErr != nil {
			return nil, replaceErr
		}
		if result.MatchedCount != 1 {
			return nil, ErrRuntimeContinuityConflict
		}
		return nil, nil
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()).SetReadPreference(readpref.Primary()))
	if err != nil {
		return RuntimeSuspensionWindow{}, false, err
	}
	return observed, foundWindow, nil
}

// StopRuntimeSession records the beginning of a cleanly requested suspension.
// The next start closes and persists that interval.
func (s *Store) StopRuntimeSession(ctx context.Context, deployment kernel.Digest, session kernel.UUIDv7, now time.Time) error {
	if err := requireDeadline(ctx); err != nil {
		return err
	}
	if s == nil || s.db == nil || !deployment.Valid() || !session.Valid() || now.IsZero() {
		return ErrRuntimeContinuityConflict
	}
	var prior runtimeSessionDocument
	if err := s.db.Collection("runtime_sessions").FindOne(ctx, bson.D{{Key: "_id", Value: string(deployment)}}).Decode(&prior); err != nil {
		return err
	}
	if prior.Revision == 0 || prior.SessionID != string(session) || prior.State != runtimeSessionRunning {
		return ErrRuntimeContinuityConflict
	}
	now = now.UTC().Truncate(time.Millisecond)
	next := runtimeSessionDocument{ID: prior.ID, Revision: prior.Revision + 1, SessionID: prior.SessionID, State: runtimeSessionStopped, LastHeartbeatAt: now, SuspendedAt: now, SuspensionThresholdMillis: prior.SuspensionThresholdMillis}
	result, err := s.db.Collection("runtime_sessions").ReplaceOne(ctx, bson.D{{Key: "_id", Value: prior.ID}, {Key: "revision", Value: prior.Revision}}, next)
	if err != nil {
		return err
	}
	if result.MatchedCount != 1 {
		return ErrRuntimeContinuityConflict
	}
	return nil
}

// ListRuntimeSuspensions returns the immutable suspension ledger in time order.
func (s *Store) ListRuntimeSuspensions(ctx context.Context, deployment kernel.Digest) ([]RuntimeSuspensionWindow, error) {
	if err := requireDeadline(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.db == nil || !deployment.Valid() {
		return nil, ErrRuntimeContinuityConflict
	}
	cursor, err := s.db.Collection("runtime_suspensions").Find(ctx, bson.D{{Key: "deployment_identity", Value: deployment}}, options.Find().SetSort(bson.D{{Key: "suspended_at", Value: 1}, {Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var result []RuntimeSuspensionWindow
	for cursor.Next(ctx) {
		var window RuntimeSuspensionWindow
		if err := cursor.Decode(&window); err != nil {
			return nil, err
		}
		if !window.Valid() {
			return nil, ErrRuntimeContinuityConflict
		}
		result = append(result, window)
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// EffectiveWorkInvocationDeadline adds only suspension windows that occurred
// after this invocation was authorized and before its then-effective deadline.
// Historical windows already incorporated into a successor work profile are
// therefore never counted again.
func (s *Store) EffectiveWorkInvocationDeadline(ctx context.Context, invocation kernel.WorkInvocation, now time.Time) (time.Time, error) {
	if s == nil || s.db == nil || !invocation.Valid() || !s.deploymentIdentity.Valid() || now.IsZero() {
		return time.Time{}, ErrRuntimeContinuityConflict
	}
	authorized, found, err := s.ReadEvent(ctx, invocation.AuthorizationEventID)
	if err != nil || !found || authorized.EventType != "tekroo.event.work-invocation.authorized" {
		return time.Time{}, errors.Join(ErrRuntimeContinuityConflict, err)
	}
	var session runtimeSessionDocument
	if err := s.db.Collection("runtime_sessions").FindOne(ctx, bson.D{{Key: "_id", Value: string(s.deploymentIdentity)}}).Decode(&session); err != nil {
		return time.Time{}, err
	}
	pending, pendingObserved, err := pendingRuntimeSuspension(s.deploymentIdentity, session, now.UTC())
	if err != nil {
		return time.Time{}, err
	}
	windows, err := s.ListRuntimeSuspensions(ctx, s.deploymentIdentity)
	if err != nil {
		return time.Time{}, err
	}
	if pendingObserved && !runtimeSuspensionCovered(windows, pending) {
		// The invocation worker and heartbeat goroutine wake concurrently after
		// host sleep. Include the still-uncommitted gap here so the worker can
		// never cancel a valid in-flight model call merely because it won that
		// scheduling race. If the heartbeat committed between the session read
		// and ledger read, the coverage check uses that durable window instead.
		windows = append(windows, pending)
	}
	return extendDeadlineAcrossSuspensions(invocation.DeadlineAt, authorized.CommittedAt, windows)
}

func runtimeSuspensionCovered(windows []RuntimeSuspensionWindow, pending RuntimeSuspensionWindow) bool {
	for _, window := range windows {
		if window.DeploymentIdentity == pending.DeploymentIdentity && window.SuspendedAt.Equal(pending.SuspendedAt) && !window.ResumedAt.Before(pending.ResumedAt) {
			return true
		}
	}
	return false
}

func pendingRuntimeSuspension(deployment kernel.Digest, session runtimeSessionDocument, now time.Time) (RuntimeSuspensionWindow, bool, error) {
	if !deployment.Valid() || session.ID != string(deployment) || session.Revision == 0 || session.SessionID == "" || session.LastHeartbeatAt.IsZero() || session.State != runtimeSessionRunning || session.SuspensionThresholdMillis <= 0 {
		return RuntimeSuspensionWindow{}, false, ErrRuntimeContinuityConflict
	}
	now = now.UTC().Truncate(time.Millisecond)
	threshold := time.Duration(session.SuspensionThresholdMillis) * time.Millisecond
	if !now.After(session.LastHeartbeatAt) || now.Sub(session.LastHeartbeatAt) <= threshold {
		return RuntimeSuspensionWindow{}, false, nil
	}
	window := RuntimeSuspensionWindow{ID: runtimeSuspensionID(deployment, session.LastHeartbeatAt, now), DeploymentIdentity: deployment, SuspendedAt: session.LastHeartbeatAt.UTC(), ResumedAt: now}
	if !window.Valid() {
		return RuntimeSuspensionWindow{}, false, ErrRuntimeContinuityConflict
	}
	return window, true, nil
}

func extendDeadlineAcrossSuspensions(deadline, authorizedAt time.Time, windows []RuntimeSuspensionWindow) (time.Time, error) {
	if deadline.IsZero() || authorizedAt.IsZero() || !deadline.After(authorizedAt) {
		return time.Time{}, ErrRuntimeContinuityConflict
	}
	effective := deadline
	for _, window := range windows {
		if !window.Valid() {
			return time.Time{}, ErrRuntimeContinuityConflict
		}
		if !window.ResumedAt.After(authorizedAt) || !window.SuspendedAt.Before(effective) {
			continue
		}
		start := window.SuspendedAt
		if start.Before(authorizedAt) {
			start = authorizedAt
		}
		if window.ResumedAt.After(start) {
			effective = effective.Add(window.ResumedAt.Sub(start))
		}
	}
	return effective, nil
}
