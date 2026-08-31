package mongo

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const intentFeedMaxAwait = 100 * time.Millisecond

var (
	ErrIntentNotFound = errors.New("outbox intent not found")
	ErrStaleClaim     = errors.New("outbox claim holder or epoch is stale")
	ErrInvalidClaim   = errors.New("invalid outbox claim operation")
	ErrBacklogLimit   = errors.New("outbox backlog exceeds bounded resynchronization limit")
)

type DeliveryState string

const (
	DeliveryPending    DeliveryState = "PENDING"
	DeliveryClaimed    DeliveryState = "CLAIMED"
	DeliveryResolved   DeliveryState = "RESOLVED"
	DeliveryDeadLetter DeliveryState = "DEAD_LETTER"
)

type ClaimedIntent struct {
	Intent     kernel.OutboxIntent
	State      DeliveryState
	Address    string
	Holder     string
	ClaimEpoch uint64
	LeaseUntil time.Time
	Attempts   uint32
	Resolution string
}

type SweepResult struct {
	Released   int64
	DeadLetter int64
}

type outboxDocument struct {
	ID               string        `bson:"_id"`
	EventID          string        `bson:"event_id"`
	Kind             string        `bson:"kind"`
	State            DeliveryState `bson:"state"`
	Address          string        `bson:"address,omitempty"`
	Holder           string        `bson:"holder,omitempty"`
	ClaimEpoch       uint64        `bson:"claim_epoch"`
	LeaseUntil       time.Time     `bson:"lease_until,omitempty"`
	Attempts         uint32        `bson:"attempts"`
	Resolution       string        `bson:"resolution,omitempty"`
	ResolvedAt       time.Time     `bson:"resolved_at,omitempty"`
	DeadLetterReason string        `bson:"dead_letter_reason,omitempty"`
	ReaddressHistory []readdress   `bson:"readdress_history,omitempty"`
	Data             []byte        `bson:"data"`
}

type readdress struct {
	From       string    `bson:"from"`
	To         string    `bson:"to"`
	Holder     string    `bson:"holder"`
	ClaimEpoch uint64    `bson:"claim_epoch"`
	At         time.Time `bson:"at"`
}

func (document outboxDocument) claimedIntent() (ClaimedIntent, error) {
	var intent kernel.OutboxIntent
	if err := decode(document.Data, &intent); err != nil {
		return ClaimedIntent{}, err
	}
	return ClaimedIntent{
		Intent: intent, State: document.State, Address: document.Address,
		Holder: document.Holder, ClaimEpoch: document.ClaimEpoch,
		LeaseUntil: document.LeaseUntil, Attempts: document.Attempts,
		Resolution: document.Resolution,
	}, nil
}

func (s *Store) AcquireIntent(ctx context.Context, intentID kernel.UUIDv7, holder string, now time.Time, lease time.Duration) (ClaimedIntent, error) {
	if err := requireClaimInput(ctx, intentID, holder, now, lease); err != nil {
		return ClaimedIntent{}, err
	}
	filter := bson.D{
		{Key: "_id", Value: string(intentID)},
		{Key: "$or", Value: bson.A{
			bson.D{{Key: "state", Value: DeliveryPending}},
			bson.D{{Key: "state", Value: DeliveryClaimed}, {Key: "lease_until", Value: bson.D{{Key: "$lte", Value: now}}}},
		}},
	}
	update := bson.D{
		{Key: "$set", Value: bson.D{{Key: "state", Value: DeliveryClaimed}, {Key: "holder", Value: holder}, {Key: "lease_until", Value: now.Add(lease)}}},
		{Key: "$inc", Value: bson.D{{Key: "claim_epoch", Value: 1}, {Key: "attempts", Value: 1}}},
		{Key: "$unset", Value: bson.D{{Key: "resolution", Value: ""}, {Key: "resolved_at", Value: ""}, {Key: "dead_letter_reason", Value: ""}}},
	}
	var document outboxDocument
	err := s.db.Collection("outbox").FindOneAndUpdate(ctx, filter, update, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&document)
	if errors.Is(err, driver.ErrNoDocuments) {
		return ClaimedIntent{}, s.classifyIntentMiss(ctx, intentID)
	}
	if err != nil {
		return ClaimedIntent{}, err
	}
	return document.claimedIntent()
}

func (s *Store) ExtendIntent(ctx context.Context, intentID kernel.UUIDv7, holder string, epoch uint64, now time.Time, lease time.Duration) error {
	if err := requireClaimInput(ctx, intentID, holder, now, lease); err != nil {
		return err
	}
	if epoch == 0 {
		return ErrInvalidClaim
	}
	filter := activeClaimFilter(intentID, holder, epoch, now)
	result, err := s.db.Collection("outbox").UpdateOne(ctx, filter, bson.D{{Key: "$set", Value: bson.D{{Key: "lease_until", Value: now.Add(lease)}}}})
	return claimUpdateResult(result, err)
}

func (s *Store) ResolveIntent(ctx context.Context, intentID kernel.UUIDv7, holder string, epoch uint64, now time.Time, resolution string) error {
	if err := requirePinnedClaim(ctx, intentID, holder, epoch, now); err != nil {
		return err
	}
	if resolution == "" {
		return ErrInvalidClaim
	}
	update := bson.D{
		{Key: "$set", Value: bson.D{{Key: "state", Value: DeliveryResolved}, {Key: "resolution", Value: resolution}, {Key: "resolved_at", Value: now}}},
		{Key: "$unset", Value: bson.D{{Key: "holder", Value: ""}, {Key: "lease_until", Value: ""}}},
	}
	result, err := s.db.Collection("outbox").UpdateOne(ctx, activeClaimFilter(intentID, holder, epoch, now), update)
	return claimUpdateResult(result, err)
}

func (s *Store) YieldIntent(ctx context.Context, intentID kernel.UUIDv7, holder string, epoch uint64, now time.Time) error {
	if err := requirePinnedClaim(ctx, intentID, holder, epoch, now); err != nil {
		return err
	}
	update := bson.D{
		{Key: "$set", Value: bson.D{{Key: "state", Value: DeliveryPending}}},
		{Key: "$unset", Value: bson.D{{Key: "holder", Value: ""}, {Key: "lease_until", Value: ""}}},
	}
	result, err := s.db.Collection("outbox").UpdateOne(ctx, activeClaimFilter(intentID, holder, epoch, now), update)
	return claimUpdateResult(result, err)
}

func (s *Store) ReaddressIntent(ctx context.Context, intentID kernel.UUIDv7, holder string, epoch uint64, now time.Time, address string) error {
	if err := requirePinnedClaim(ctx, intentID, holder, epoch, now); err != nil {
		return err
	}
	if address == "" {
		return ErrInvalidClaim
	}
	var current outboxDocument
	if err := s.db.Collection("outbox").FindOne(ctx, activeClaimFilter(intentID, holder, epoch, now)).Decode(&current); err != nil {
		if errors.Is(err, driver.ErrNoDocuments) {
			return ErrStaleClaim
		}
		return err
	}
	history := readdress{From: current.Address, To: address, Holder: holder, ClaimEpoch: epoch, At: now}
	update := bson.D{
		{Key: "$set", Value: bson.D{{Key: "address", Value: address}, {Key: "state", Value: DeliveryPending}}},
		{Key: "$push", Value: bson.D{{Key: "readdress_history", Value: history}}},
		{Key: "$unset", Value: bson.D{{Key: "holder", Value: ""}, {Key: "lease_until", Value: ""}}},
	}
	result, err := s.db.Collection("outbox").UpdateOne(ctx, activeClaimFilter(intentID, holder, epoch, now), update)
	return claimUpdateResult(result, err)
}

func (s *Store) SweepExpiredIntents(ctx context.Context, now time.Time, maxAttempts uint32) (SweepResult, error) {
	if err := requireDeadline(ctx); err != nil {
		return SweepResult{}, err
	}
	if now.IsZero() || maxAttempts == 0 {
		return SweepResult{}, ErrInvalidClaim
	}
	cursor, err := s.db.Collection("outbox").Find(ctx, bson.D{{Key: "state", Value: DeliveryClaimed}, {Key: "lease_until", Value: bson.D{{Key: "$lte", Value: now}}}})
	if err != nil {
		return SweepResult{}, err
	}
	defer cursor.Close(ctx)
	var result SweepResult
	for cursor.Next(ctx) {
		var document outboxDocument
		if err := cursor.Decode(&document); err != nil {
			return result, err
		}
		filter := bson.D{{Key: "_id", Value: document.ID}, {Key: "state", Value: DeliveryClaimed}, {Key: "holder", Value: document.Holder}, {Key: "claim_epoch", Value: document.ClaimEpoch}, {Key: "lease_until", Value: bson.D{{Key: "$lte", Value: now}}}}
		state := DeliveryPending
		set := bson.D{{Key: "state", Value: state}}
		if document.Attempts >= maxAttempts {
			state = DeliveryDeadLetter
			set = bson.D{{Key: "state", Value: state}, {Key: "dead_letter_reason", Value: "MAX_ATTEMPTS_EXHAUSTED"}}
		}
		update := bson.D{{Key: "$set", Value: set}, {Key: "$unset", Value: bson.D{{Key: "holder", Value: ""}, {Key: "lease_until", Value: ""}}}}
		changed, err := s.db.Collection("outbox").UpdateOne(ctx, filter, update)
		if err != nil {
			return result, err
		}
		if changed.ModifiedCount == 1 {
			if state == DeliveryDeadLetter {
				result.DeadLetter++
			} else {
				result.Released++
			}
		}
	}
	return result, cursor.Err()
}

type IntentFeed struct {
	store          *Store
	consumerID     string
	stream         *driver.ChangeStream
	backlog        *driver.Cursor
	seen           map[string]struct{}
	backlogSeen    int64
	backlogLimit   int64
	resynchronized bool
	kind           string
}

// OpenIntentFeed deliberately opens the change stream before it queries the
// backlog. Both observation paths converge on immutable intent IDs.
func (s *Store) OpenIntentFeed(ctx context.Context, consumerID string) (*IntentFeed, error) {
	return s.openIntentFeed(ctx, consumerID, "")
}

// OpenIntentFeedForKind observes only one exact outbox kind in both the
// initial backlog and the change stream. This prevents a specialized consumer
// from treating unrelated organizational events as executable work.
func (s *Store) OpenIntentFeedForKind(ctx context.Context, consumerID, kind string) (*IntentFeed, error) {
	if kind == "" {
		return nil, ErrInvalidClaim
	}
	return s.openIntentFeed(ctx, consumerID, kind)
}

func (s *Store) openIntentFeed(ctx context.Context, consumerID, kind string) (*IntentFeed, error) {
	if err := requireDeadline(ctx); err != nil {
		return nil, err
	}
	if consumerID == "" {
		return nil, ErrInvalidClaim
	}
	streamOptions := options.ChangeStream().SetFullDocument(options.UpdateLookup).SetMaxAwaitTime(intentFeedMaxAwait)
	var checkpoint valueDocument
	err := s.db.Collection("consumer_checkpoints").FindOne(ctx, bson.D{{Key: "_id", Value: consumerID}}).Decode(&checkpoint)
	usedCheckpoint := false
	if err == nil && len(checkpoint.Data) > 0 {
		streamOptions.SetResumeAfter(bson.Raw(checkpoint.Data))
		usedCheckpoint = true
	} else if err != nil && !errors.Is(err, driver.ErrNoDocuments) {
		return nil, err
	}
	pipeline := intentFeedPipeline(kind)
	stream, err := s.db.Collection("outbox").Watch(ctx, pipeline, streamOptions)
	resynchronized := false
	if err != nil && usedCheckpoint && isResumeFailure(err) {
		if _, deleteErr := s.db.Collection("consumer_checkpoints").DeleteOne(ctx, bson.D{{Key: "_id", Value: consumerID}}); deleteErr != nil {
			return nil, deleteErr
		}
		stream, err = s.db.Collection("outbox").Watch(ctx, pipeline, options.ChangeStream().SetFullDocument(options.UpdateLookup).SetMaxAwaitTime(intentFeedMaxAwait))
		resynchronized = true
	}
	if err != nil {
		return nil, err
	}
	backlog, err := s.openBoundedBacklog(ctx, kind)
	if err != nil {
		_ = stream.Close(ctx)
		return nil, err
	}
	return &IntentFeed{store: s, consumerID: consumerID, stream: stream, backlog: backlog, seen: make(map[string]struct{}), backlogLimit: s.backlogLimit, resynchronized: resynchronized, kind: kind}, nil
}

func (s *Store) openBoundedBacklog(ctx context.Context, kind string) (*driver.Cursor, error) {
	filter := bson.D{{Key: "state", Value: bson.D{{Key: "$in", Value: bson.A{DeliveryPending, DeliveryClaimed}}}}}
	if kind != "" {
		filter = append(filter, bson.E{Key: "kind", Value: kind})
	}
	return s.db.Collection("outbox").Find(ctx, filter, options.Find().SetLimit(s.backlogLimit+1))
}

func (feed *IntentFeed) Resynchronized() bool { return feed.resynchronized }

func (feed *IntentFeed) Kind() string { return feed.kind }

func (feed *IntentFeed) Close(ctx context.Context) error {
	_ = feed.backlog.Close(ctx)
	return feed.stream.Close(ctx)
}

func (feed *IntentFeed) Next(ctx context.Context) (ClaimedIntent, error) {
	if err := requireDeadline(ctx); err != nil {
		return ClaimedIntent{}, err
	}
	for feed.backlog.Next(ctx) {
		feed.backlogSeen++
		if feed.backlogSeen > feed.backlogLimit {
			return ClaimedIntent{}, ErrBacklogLimit
		}
		var document outboxDocument
		if err := feed.backlog.Decode(&document); err != nil {
			return ClaimedIntent{}, err
		}
		key := intentDeliveryKey(document)
		if _, duplicate := feed.seen[key]; duplicate {
			continue
		}
		feed.seen[key] = struct{}{}
		return document.claimedIntent()
	}
	if err := feed.backlog.Err(); err != nil {
		return ClaimedIntent{}, err
	}
	for feed.stream.Next(ctx) {
		var change struct {
			FullDocument outboxDocument `bson:"fullDocument"`
		}
		if err := feed.stream.Decode(&change); err != nil {
			return ClaimedIntent{}, err
		}
		if err := feed.saveCheckpoint(ctx); err != nil {
			return ClaimedIntent{}, err
		}
		key := intentDeliveryKey(change.FullDocument)
		if _, duplicate := feed.seen[key]; duplicate {
			continue
		}
		feed.seen[key] = struct{}{}
		return change.FullDocument.claimedIntent()
	}
	if err := feed.stream.Err(); err != nil {
		if !feed.resynchronized && isResumeFailure(err) {
			if resyncErr := feed.resynchronize(ctx); resyncErr != nil {
				return ClaimedIntent{}, resyncErr
			}
			return feed.Next(ctx)
		}
		return ClaimedIntent{}, err
	}
	return ClaimedIntent{}, ctx.Err()
}

// Poll returns one available intent without cancelling an otherwise healthy
// change stream merely because the service is idle. Mongo change-stream Next
// treats context expiry as a stream error; TryNext plus a server-side bounded
// await gives persistent workers a clean no-work result instead.
func (feed *IntentFeed) Poll(ctx context.Context) (ClaimedIntent, error) {
	if err := requireDeadline(ctx); err != nil {
		return ClaimedIntent{}, err
	}
	for feed.backlog.Next(ctx) {
		feed.backlogSeen++
		if feed.backlogSeen > feed.backlogLimit {
			return ClaimedIntent{}, ErrBacklogLimit
		}
		var document outboxDocument
		if err := feed.backlog.Decode(&document); err != nil {
			return ClaimedIntent{}, err
		}
		key := intentDeliveryKey(document)
		if _, duplicate := feed.seen[key]; duplicate {
			continue
		}
		feed.seen[key] = struct{}{}
		return document.claimedIntent()
	}
	if err := feed.backlog.Err(); err != nil {
		return ClaimedIntent{}, err
	}
	for feed.stream.TryNext(ctx) {
		var change struct {
			FullDocument outboxDocument `bson:"fullDocument"`
		}
		if err := feed.stream.Decode(&change); err != nil {
			return ClaimedIntent{}, err
		}
		if err := feed.saveCheckpoint(ctx); err != nil {
			return ClaimedIntent{}, err
		}
		key := intentDeliveryKey(change.FullDocument)
		if _, duplicate := feed.seen[key]; duplicate {
			continue
		}
		feed.seen[key] = struct{}{}
		return change.FullDocument.claimedIntent()
	}
	if err := feed.stream.Err(); err != nil {
		if !feed.resynchronized && isResumeFailure(err) {
			if resyncErr := feed.resynchronize(ctx); resyncErr != nil {
				return ClaimedIntent{}, resyncErr
			}
			return feed.Poll(ctx)
		}
		return ClaimedIntent{}, err
	}
	return ClaimedIntent{}, ErrIntentNotFound
}

func (feed *IntentFeed) resynchronize(ctx context.Context) error {
	pipeline := intentFeedPipeline(feed.kind)
	stream, err := feed.store.db.Collection("outbox").Watch(ctx, pipeline, options.ChangeStream().SetFullDocument(options.UpdateLookup).SetMaxAwaitTime(intentFeedMaxAwait))
	if err != nil {
		return err
	}
	backlog, err := feed.store.openBoundedBacklog(ctx, feed.kind)
	if err != nil {
		_ = stream.Close(ctx)
		return err
	}
	_ = feed.stream.Close(ctx)
	_ = feed.backlog.Close(ctx)
	if _, err := feed.store.db.Collection("consumer_checkpoints").DeleteOne(ctx, bson.D{{Key: "_id", Value: feed.consumerID}}); err != nil {
		_ = stream.Close(ctx)
		_ = backlog.Close(ctx)
		return err
	}
	feed.stream = stream
	feed.backlog = backlog
	feed.backlogSeen = 0
	feed.resynchronized = true
	return nil
}

func intentFeedPipeline(kind string) driver.Pipeline {
	// Inserts expose newly committed intents. Updates that return an intent to
	// PENDING expose retries yielded by a consumer. Claim/lease extensions and
	// terminal resolutions are deliberately excluded.
	match := bson.D{
		{Key: "operationType", Value: bson.D{{Key: "$in", Value: bson.A{"insert", "update", "replace"}}}},
		{Key: "fullDocument.state", Value: DeliveryPending},
	}
	if kind != "" {
		match = append(match, bson.E{Key: "fullDocument.kind", Value: kind})
	}
	return driver.Pipeline{bson.D{{Key: "$match", Value: match}}}
}

func intentDeliveryKey(document outboxDocument) string {
	// The claim epoch changes on every acquisition. Using it in the feed key
	// suppresses the backlog/change-stream overlap for one delivery while still
	// allowing the same immutable intent to be delivered after YieldIntent.
	return document.ID + ":" + strconv.FormatUint(document.ClaimEpoch, 10)
}

func (feed *IntentFeed) saveCheckpoint(ctx context.Context) error {
	token := feed.stream.ResumeToken()
	if len(token) == 0 {
		return nil
	}
	_, err := feed.store.db.Collection("consumer_checkpoints").ReplaceOne(ctx, bson.D{{Key: "_id", Value: feed.consumerID}}, valueDocument{ID: feed.consumerID, Data: append([]byte(nil), token...)}, options.Replace().SetUpsert(true))
	return err
}

func (s *Store) classifyIntentMiss(ctx context.Context, intentID kernel.UUIDv7) error {
	err := s.db.Collection("outbox").FindOne(ctx, bson.D{{Key: "_id", Value: string(intentID)}}).Err()
	if errors.Is(err, driver.ErrNoDocuments) {
		return ErrIntentNotFound
	}
	if err != nil {
		return err
	}
	return ErrStaleClaim
}

func activeClaimFilter(intentID kernel.UUIDv7, holder string, epoch uint64, now time.Time) bson.D {
	return bson.D{{Key: "_id", Value: string(intentID)}, {Key: "state", Value: DeliveryClaimed}, {Key: "holder", Value: holder}, {Key: "claim_epoch", Value: epoch}, {Key: "lease_until", Value: bson.D{{Key: "$gt", Value: now}}}}
}

func claimUpdateResult(result *driver.UpdateResult, err error) error {
	if err != nil {
		return err
	}
	if result.MatchedCount != 1 {
		return ErrStaleClaim
	}
	return nil
}

func requireClaimInput(ctx context.Context, intentID kernel.UUIDv7, holder string, now time.Time, lease time.Duration) error {
	if err := requirePinnedClaim(ctx, intentID, holder, 1, now); err != nil {
		return err
	}
	if lease <= 0 {
		return ErrInvalidClaim
	}
	return nil
}

func requirePinnedClaim(ctx context.Context, intentID kernel.UUIDv7, holder string, epoch uint64, now time.Time) error {
	if err := requireDeadline(ctx); err != nil {
		return err
	}
	if !intentID.Valid() || holder == "" || epoch == 0 || now.IsZero() {
		return ErrInvalidClaim
	}
	return nil
}

func isResumeFailure(err error) bool {
	var commandError driver.CommandError
	if !errors.As(err, &commandError) {
		return false
	}
	return commandError.HasErrorCode(280) || commandError.HasErrorCode(286) || commandError.Name == "ChangeStreamHistoryLost" || commandError.Name == "InvalidResumeToken"
}
