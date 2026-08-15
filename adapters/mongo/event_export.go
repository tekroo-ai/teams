package mongo

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/tekroo-ai/teams/eventexport"
	"github.com/tekroo-ai/teams/kernel"
	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

var (
	ErrEventBacklogLimit      = errors.New("event export backlog exceeds configured bound")
	ErrEventCursorUnavailable = errors.New("mongo change stream did not establish a resumable cursor")
)

type EventExportSource struct {
	store        *Store
	sourceID     string
	backlogLimit int64
	ownsClient   bool
}

type ReadOnlyEventExportConfig struct {
	URI              string
	Database         string
	SourceID         string
	ContractIdentity string
	ManifestSHA256   kernel.Digest
	MigrationLevel   uint64
	BacklogLimit     int64
}

type eventExportPosition struct {
	Version      uint8  `json:"version"`
	Phase        string `json:"phase"`
	ResumeToken  []byte `json:"resume_token,omitempty"`
	BacklogAfter string `json:"backlog_after,omitempty"`
}

const (
	eventExportBacklog = "BACKLOG"
	eventExportLive    = "LIVE"
)

func NewEventExportSource(store *Store, sourceID string, backlogLimit int64) (*EventExportSource, error) {
	if store == nil || sourceID == "" || backlogLimit <= 0 {
		return nil, eventexport.ErrInvalidRequest
	}
	return &EventExportSource{store: store, sourceID: sourceID, backlogLimit: backlogLimit}, nil
}

// OpenReadOnlyEventExportSource verifies existing kernel metadata and never
// creates collections, indexes, checkpoints, or metadata.
func OpenReadOnlyEventExportSource(ctx context.Context, config ReadOnlyEventExportConfig) (*EventExportSource, error) {
	if err := requireDeadline(ctx); err != nil {
		return nil, err
	}
	if config.URI == "" || config.Database == "" || config.SourceID == "" || config.ContractIdentity != kernel.ContractIdentity || !config.ManifestSHA256.Valid() || config.MigrationLevel == 0 || config.BacklogLimit <= 0 {
		return nil, eventexport.ErrInvalidRequest
	}
	client, err := driver.Connect(options.Client().ApplyURI(config.URI).SetReadConcern(readconcern.Majority()).SetReadPreference(readpref.Primary()).SetRetryWrites(false))
	if err != nil {
		return nil, err
	}
	closeOnError := func(openErr error) (*EventExportSource, error) {
		_ = client.Disconnect(context.Background())
		return nil, openErr
	}
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		return closeOnError(err)
	}
	database := client.Database(config.Database)
	var hello struct {
		SetName string `bson:"setName"`
	}
	if err := database.RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello); err != nil {
		return closeOnError(err)
	}
	if hello.SetName == "" {
		return closeOnError(ErrUnsupportedTopology)
	}
	var metadata metadataDocument
	if err := database.Collection("metadata").FindOne(ctx, bson.D{{Key: "_id", Value: "kernel"}}).Decode(&metadata); err != nil {
		return closeOnError(err)
	}
	if metadata.ContractIdentity != config.ContractIdentity || metadata.ManifestSHA256 != string(config.ManifestSHA256) || metadata.MigrationLevel != config.MigrationLevel {
		return closeOnError(ErrMetadataMismatch)
	}
	store := &Store{client: client, db: database, owns: false, backlogLimit: config.BacklogLimit}
	return &EventExportSource{store: store, sourceID: config.SourceID, backlogLimit: config.BacklogLimit, ownsClient: true}, nil
}

func (source *EventExportSource) Close(ctx context.Context) error {
	if err := requireDeadline(ctx); err != nil {
		return err
	}
	if !source.ownsClient {
		return nil
	}
	return source.store.client.Disconnect(ctx)
}

// Open establishes the immutable insert stream before opening backlog. It does
// not use or mutate outbox state and does not persist consumer checkpoints.
func (source *EventExportSource) Open(ctx context.Context, sourceID string, encodedPosition []byte) (eventexport.Feed, error) {
	if err := requireDeadline(ctx); err != nil {
		return nil, err
	}
	if sourceID != source.sourceID {
		return nil, eventexport.ErrUnauthorized
	}
	position := eventExportPosition{Version: 1, Phase: eventExportBacklog}
	if len(encodedPosition) > 0 {
		if json.Unmarshal(encodedPosition, &position) != nil || position.Version != 1 || position.Phase != eventExportBacklog && position.Phase != eventExportLive {
			return nil, eventexport.ErrResyncRequired
		}
	}
	streamOptions := options.ChangeStream().SetFullDocument(options.UpdateLookup)
	if len(position.ResumeToken) > 0 {
		streamOptions.SetResumeAfter(bson.Raw(position.ResumeToken))
	}
	pipeline := driver.Pipeline{bson.D{{Key: "$match", Value: bson.D{{Key: "operationType", Value: "insert"}}}}}
	stream, err := source.store.db.Collection("events").Watch(ctx, pipeline, streamOptions)
	if err != nil {
		if len(position.ResumeToken) > 0 && isResumeFailure(err) {
			return nil, eventexport.ErrResyncRequired
		}
		return nil, err
	}
	if len(position.ResumeToken) == 0 {
		position.ResumeToken = append([]byte(nil), stream.ResumeToken()...)
		if len(position.ResumeToken) == 0 {
			_ = stream.Close(ctx)
			return nil, ErrEventCursorUnavailable
		}
	}
	var backlog *driver.Cursor
	if position.Phase == eventExportBacklog || len(position.ResumeToken) == 0 {
		position.Phase = eventExportBacklog
		filter := bson.D{}
		if position.BacklogAfter != "" {
			filter = bson.D{{Key: "_id", Value: bson.D{{Key: "$gt", Value: position.BacklogAfter}}}}
		}
		backlog, err = source.store.db.Collection("events").Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(source.backlogLimit+1))
		if err != nil {
			_ = stream.Close(ctx)
			return nil, err
		}
	}
	return &mongoEventFeed{source: source, stream: stream, backlog: backlog, position: position, seen: make(map[string]struct{})}, nil
}

type mongoEventFeed struct {
	source      *EventExportSource
	stream      *driver.ChangeStream
	backlog     *driver.Cursor
	position    eventExportPosition
	seen        map[string]struct{}
	backlogSeen int64
}

func (feed *mongoEventFeed) Next(ctx context.Context) (eventexport.RawRecord, error) {
	if err := requireDeadline(ctx); err != nil {
		return eventexport.RawRecord{}, err
	}
	for feed.backlog != nil && feed.backlog.Next(ctx) {
		feed.backlogSeen++
		if feed.backlogSeen > feed.source.backlogLimit {
			return eventexport.RawRecord{}, ErrEventBacklogLimit
		}
		var document eventDocument
		if err := feed.backlog.Decode(&document); err != nil {
			return eventexport.RawRecord{}, err
		}
		feed.position.BacklogAfter = document.ID
		if _, duplicate := feed.seen[document.ID]; duplicate {
			continue
		}
		feed.seen[document.ID] = struct{}{}
		return eventexport.RawRecord{Bytes: append([]byte(nil), document.Data...), Position: feed.Position()}, nil
	}
	if feed.backlog != nil {
		if err := feed.backlog.Err(); err != nil {
			return eventexport.RawRecord{}, err
		}
		_ = feed.backlog.Close(ctx)
		feed.backlog = nil
		feed.position.Phase = eventExportLive
	}
	for feed.stream.Next(ctx) {
		var change struct {
			FullDocument eventDocument `bson:"fullDocument"`
		}
		if err := feed.stream.Decode(&change); err != nil {
			return eventexport.RawRecord{}, err
		}
		feed.position.ResumeToken = append(feed.position.ResumeToken[:0], feed.stream.ResumeToken()...)
		if _, duplicate := feed.seen[change.FullDocument.ID]; duplicate {
			continue
		}
		feed.seen[change.FullDocument.ID] = struct{}{}
		return eventexport.RawRecord{Bytes: append([]byte(nil), change.FullDocument.Data...), Position: feed.Position()}, nil
	}
	if err := feed.stream.Err(); err != nil {
		if isResumeFailure(err) {
			return eventexport.RawRecord{}, eventexport.ErrResyncRequired
		}
		return eventexport.RawRecord{}, err
	}
	if err := ctx.Err(); err != nil {
		return eventexport.RawRecord{}, err
	}
	return eventexport.RawRecord{}, eventexport.ErrCaughtUp
}

func (feed *mongoEventFeed) Position() []byte {
	encoded, _ := json.Marshal(feed.position)
	return encoded
}

func (feed *mongoEventFeed) Close(ctx context.Context) error {
	if err := requireDeadline(ctx); err != nil {
		return err
	}
	if feed.backlog != nil {
		_ = feed.backlog.Close(ctx)
	}
	return feed.stream.Close(ctx)
}
