package mongo

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

type ProjectionRebuildReceipt struct {
	Projector           string        `json:"projector"`
	StoryCount          uint64        `json:"story_count"`
	TaskCount           uint64        `json:"task_count"`
	SourceEventCount    uint64        `json:"source_event_count"`
	IncrementalDigest   kernel.Digest `json:"incremental_digest"`
	RebuiltDigest       kernel.Digest `json:"rebuilt_digest"`
	ComparedAt          time.Time     `json:"compared_at"`
	CheckpointsComplete bool          `json:"checkpoints_complete"`
	ExactMatch          bool          `json:"exact_match"`
	ReplacementApplied  bool          `json:"replacement_applied"`
}

type projectionMetadata struct {
	Revision uint64
	Last     kernel.UUIDv7
}

func (s *Store) RebuildOperationalProjections(ctx context.Context, comparedAt time.Time) (ProjectionRebuildReceipt, error) {
	if err := requireDeadline(ctx); err != nil {
		return ProjectionRebuildReceipt{}, err
	}
	if comparedAt.IsZero() {
		return ProjectionRebuildReceipt{}, ErrInvalidDecision
	}
	events, err := s.allProjectionEvents(ctx)
	if err != nil {
		return ProjectionRebuildReceipt{}, err
	}
	if err := validateProjectionSourceStreams(events); err != nil {
		return ProjectionRebuildReceipt{}, err
	}
	taskMetadata := map[kernel.AggregateRef]projectionMetadata{}
	storyMetadata := map[kernel.AggregateRef]projectionMetadata{}
	for _, event := range events {
		if !projectionRelevant(event.Aggregate.Kind) {
			continue
		}
		tasks, stories, targetErr := s.projectionTargets(ctx, event)
		if targetErr != nil {
			return ProjectionRebuildReceipt{}, targetErr
		}
		for task := range tasks {
			metadata := taskMetadata[task]
			metadata.Revision++
			metadata.Last = event.EventID
			taskMetadata[task] = metadata
		}
		for story := range stories {
			metadata := storyMetadata[story]
			metadata.Revision++
			metadata.Last = event.EventID
			storyMetadata[story] = metadata
		}
	}

	rebuiltTasks := map[kernel.AggregateRef]TaskProjection{}
	for _, task := range sortedProjectionRefs(refSet(taskMetadata)) {
		metadata := taskMetadata[task]
		projection, materializeErr := s.materializeTaskProjection(ctx, task, metadata.Last)
		if materializeErr != nil {
			return ProjectionRebuildReceipt{}, materializeErr
		}
		if projection == nil {
			continue
		}
		projection.ProjectionRevision = metadata.Revision
		projection.LastEventID = metadata.Last
		rebuiltTasks[task] = *projection
	}
	rebuiltStories := map[kernel.AggregateRef]StoryProjection{}
	for _, story := range sortedProjectionRefs(refSet(storyMetadata)) {
		metadata := storyMetadata[story]
		projection, materializeErr := s.materializeStoryProjection(ctx, story, metadata.Last)
		if materializeErr != nil {
			return ProjectionRebuildReceipt{}, materializeErr
		}
		if projection == nil {
			continue
		}
		projection.ProjectionRevision = metadata.Revision
		projection.LastEventID = metadata.Last
		rebuiltStories[story] = *projection
	}
	temporarySuffix := fmt.Sprintf("%d", comparedAt.UTC().UnixNano())
	temporaryStoryCollection := "story_projections_rebuild_" + temporarySuffix
	temporaryTaskCollection := "task_projections_rebuild_" + temporarySuffix
	if err := s.writeTemporaryProjectionCollections(ctx, temporaryStoryCollection, temporaryTaskCollection, rebuiltStories, rebuiltTasks); err != nil {
		return ProjectionRebuildReceipt{}, err
	}
	defer func() {
		dropContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.db.Collection(temporaryStoryCollection).Drop(dropContext)
		_ = s.db.Collection(temporaryTaskCollection).Drop(dropContext)
	}()
	rebuiltTasks, err = s.readAllTaskProjectionsFrom(ctx, temporaryTaskCollection)
	if err != nil {
		return ProjectionRebuildReceipt{}, err
	}
	rebuiltStories, err = s.readAllStoryProjectionsFrom(ctx, temporaryStoryCollection)
	if err != nil {
		return ProjectionRebuildReceipt{}, err
	}

	incrementalTasks, err := s.readAllTaskProjections(ctx)
	if err != nil {
		return ProjectionRebuildReceipt{}, err
	}
	incrementalStories, err := s.readAllStoryProjections(ctx)
	if err != nil {
		return ProjectionRebuildReceipt{}, err
	}
	incrementalCanonical := projectionCanonicalSet(incrementalStories, incrementalTasks)
	rebuiltCanonical := projectionCanonicalSet(rebuiltStories, rebuiltTasks)
	checkpointsComplete, err := s.projectionCheckpointsCover(ctx, events)
	if err != nil {
		return ProjectionRebuildReceipt{}, err
	}
	incrementalDigest, err := canonicalProjectionDigest(incrementalCanonical)
	if err != nil {
		return ProjectionRebuildReceipt{}, err
	}
	rebuiltDigest, err := canonicalProjectionDigest(rebuiltCanonical)
	if err != nil {
		return ProjectionRebuildReceipt{}, err
	}
	receipt := ProjectionRebuildReceipt{
		Projector: operationalProjectorName, StoryCount: uint64(len(rebuiltStories)), TaskCount: uint64(len(rebuiltTasks)),
		SourceEventCount: uint64(len(events)), IncrementalDigest: incrementalDigest, RebuiltDigest: rebuiltDigest,
		ComparedAt: comparedAt.UTC(), CheckpointsComplete: checkpointsComplete,
		ExactMatch: checkpointsComplete && incrementalDigest == rebuiltDigest && canonicalProjectionEqual(incrementalCanonical, rebuiltCanonical),
	}
	if !receipt.ExactMatch {
		if mismatch, found := firstProjectionReference(rebuiltStories, rebuiltTasks, incrementalStories, incrementalTasks); found {
			kind := "TASK"
			if mismatch.Kind == kernel.AggregateStory {
				kind = "STORY"
			}
			lastEventID := eventsLastID(events)
			if len(events) > 0 {
				lastEventDigest, digestErr := operationalEventDigest(events[len(events)-1])
				if digestErr == nil {
					fault := ProjectionFault{ProjectionKind: kind, Aggregate: mismatch, ExpectedRevision: 1, ObservedRevision: 1, EventID: lastEventID, EventDigest: lastEventDigest, FaultKind: "REBUILD_MISMATCH", ObservedAt: comparedAt.UTC()}
					_ = s.recordProjectionFault(ctx, fault)
				}
			}
		}
		return receipt, ErrProjectionRebuildMismatch
	}
	if err := s.replaceOperationalProjectionCollections(ctx, rebuiltStories, rebuiltTasks); err != nil {
		return receipt, err
	}
	receipt.ReplacementApplied = true
	return receipt, nil
}

func (s *Store) projectionCheckpointsCover(ctx context.Context, events []kernel.DomainEvent) (bool, error) {
	latest := map[kernel.AggregateRef]kernel.DomainEvent{}
	for _, event := range events {
		if projectionRelevant(event.Aggregate.Kind) {
			latest[event.Aggregate] = event
		}
	}
	for aggregate, event := range latest {
		checkpoint, found, err := s.loadProjectionCheckpoint(ctx, aggregate)
		if err != nil {
			return false, err
		}
		digest, err := operationalEventDigest(event)
		if err != nil {
			return false, err
		}
		if !found || checkpoint.ProjectionRevision != event.AggregateRevision || checkpoint.LastEventID != event.EventID || checkpoint.LastEventDigest != digest {
			return false, nil
		}
	}
	return true, nil
}

func (s *Store) allProjectionEvents(ctx context.Context) ([]kernel.DomainEvent, error) {
	cursor, err := s.db.Collection("events").Find(ctx, bson.D{}, options.Find().SetSort(bson.D{{Key: "aggregate_key", Value: 1}, {Key: "revision", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	result := make([]kernel.DomainEvent, 0)
	for cursor.Next(ctx) {
		var document eventDocument
		if cursor.Decode(&document) != nil {
			return nil, ErrCorruptAggregate
		}
		var event kernel.DomainEvent
		if decode(document.Data, &event) != nil || aggregateKey(event.Aggregate) != document.AggregateKey || event.AggregateRevision != document.Revision || string(event.EventID) != document.ID {
			return nil, ErrCorruptAggregate
		}
		result = append(result, event)
	}
	return result, cursor.Err()
}

func validateProjectionSourceStreams(events []kernel.DomainEvent) error {
	groups := map[kernel.AggregateRef][]kernel.DomainEvent{}
	for _, event := range events {
		group := groups[event.Aggregate]
		if event.AggregateRevision != uint64(len(group)+1) {
			return ErrProjectionGap
		}
		groups[event.Aggregate] = append(group, event)
	}
	for aggregate, group := range groups {
		if aggregate.Kind != kernel.AggregateStory && aggregate.Kind != kernel.AggregateTask {
			continue
		}
		state, err := kernel.FoldAggregate(group)
		if err != nil || state == nil || state.Kind != aggregate.Kind || state.ID != aggregate.ID || state.Revision != uint64(len(group)) {
			return ErrCorruptAggregate
		}
	}
	return nil
}

func (s *Store) readAllTaskProjections(ctx context.Context) (map[kernel.AggregateRef]TaskProjection, error) {
	return s.readAllTaskProjectionsFrom(ctx, "task_projections")
}

func (s *Store) readAllTaskProjectionsFrom(ctx context.Context, collection string) (map[kernel.AggregateRef]TaskProjection, error) {
	result := map[kernel.AggregateRef]TaskProjection{}
	err := scan(ctx, s.db.Collection(collection), bson.D{}, func(document operationalProjectionDocument) error {
		var projection TaskProjection
		if decode(document.Data, &projection) != nil || !projection.Valid() {
			return ErrCorruptAggregate
		}
		result[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: projection.ID}] = projection
		return nil
	})
	return result, err
}

func (s *Store) readAllStoryProjections(ctx context.Context) (map[kernel.AggregateRef]StoryProjection, error) {
	return s.readAllStoryProjectionsFrom(ctx, "story_projections")
}

func (s *Store) readAllStoryProjectionsFrom(ctx context.Context, collection string) (map[kernel.AggregateRef]StoryProjection, error) {
	result := map[kernel.AggregateRef]StoryProjection{}
	err := scan(ctx, s.db.Collection(collection), bson.D{}, func(document operationalProjectionDocument) error {
		var projection StoryProjection
		if decode(document.Data, &projection) != nil || !projection.Valid() {
			return ErrCorruptAggregate
		}
		result[kernel.AggregateRef{Kind: kernel.AggregateStory, ID: projection.ID}] = projection
		return nil
	})
	return result, err
}

func (s *Store) writeTemporaryProjectionCollections(ctx context.Context, storyCollection, taskCollection string, stories map[kernel.AggregateRef]StoryProjection, tasks map[kernel.AggregateRef]TaskProjection) error {
	for _, reference := range sortedProjectionRefs(refSet(stories)) {
		document, err := projectionDocument(stories[reference])
		if err != nil {
			return err
		}
		if _, err := s.db.Collection(storyCollection).InsertOne(ctx, document); err != nil {
			return err
		}
	}
	for _, reference := range sortedProjectionRefs(refSet(tasks)) {
		document, err := projectionDocument(tasks[reference])
		if err != nil {
			return err
		}
		if _, err := s.db.Collection(taskCollection).InsertOne(ctx, document); err != nil {
			return err
		}
	}
	return nil
}

func projectionCanonicalSet(stories map[kernel.AggregateRef]StoryProjection, tasks map[kernel.AggregateRef]TaskProjection) []any {
	keys := make([]string, 0, len(stories)+len(tasks))
	values := make(map[string]any, len(stories)+len(tasks))
	for reference, projection := range stories {
		key := aggregateKey(reference)
		keys = append(keys, key)
		values[key] = projection
	}
	for reference, projection := range tasks {
		key := aggregateKey(reference)
		keys = append(keys, key)
		values[key] = projection
	}
	sort.Strings(keys)
	result := make([]any, 0, len(keys))
	for _, key := range keys {
		result = append(result, values[key])
	}
	return result
}

func (s *Store) replaceOperationalProjectionCollections(ctx context.Context, stories map[kernel.AggregateRef]StoryProjection, tasks map[kernel.AggregateRef]TaskProjection) error {
	session, err := s.client.StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(ctx)
	_, err = session.WithTransaction(ctx, func(transactionContext context.Context) (any, error) {
		if _, deleteErr := s.db.Collection("story_projections").DeleteMany(transactionContext, bson.D{}); deleteErr != nil {
			return nil, deleteErr
		}
		if _, deleteErr := s.db.Collection("task_projections").DeleteMany(transactionContext, bson.D{}); deleteErr != nil {
			return nil, deleteErr
		}
		for _, reference := range sortedProjectionRefs(refSet(stories)) {
			document, documentErr := projectionDocument(stories[reference])
			if documentErr != nil {
				return nil, documentErr
			}
			if _, insertErr := s.db.Collection("story_projections").InsertOne(transactionContext, document); insertErr != nil {
				return nil, insertErr
			}
		}
		for _, reference := range sortedProjectionRefs(refSet(tasks)) {
			document, documentErr := projectionDocument(tasks[reference])
			if documentErr != nil {
				return nil, documentErr
			}
			if _, insertErr := s.db.Collection("task_projections").InsertOne(transactionContext, document); insertErr != nil {
				return nil, insertErr
			}
		}
		return nil, nil
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()).SetReadPreference(readpref.Primary()))
	return err
}

func refSet[T any](values map[kernel.AggregateRef]T) map[kernel.AggregateRef]struct{} {
	result := make(map[kernel.AggregateRef]struct{}, len(values))
	for reference := range values {
		result[reference] = struct{}{}
	}
	return result
}

func eventsLastID(events []kernel.DomainEvent) kernel.UUIDv7 {
	if len(events) == 0 {
		return ""
	}
	return events[len(events)-1].EventID
}

func firstProjectionReference(rebuiltStories map[kernel.AggregateRef]StoryProjection, rebuiltTasks map[kernel.AggregateRef]TaskProjection, incrementalStories map[kernel.AggregateRef]StoryProjection, incrementalTasks map[kernel.AggregateRef]TaskProjection) (kernel.AggregateRef, bool) {
	references := map[kernel.AggregateRef]struct{}{}
	for reference := range rebuiltStories {
		references[reference] = struct{}{}
	}
	for reference := range rebuiltTasks {
		references[reference] = struct{}{}
	}
	for reference := range incrementalStories {
		references[reference] = struct{}{}
	}
	for reference := range incrementalTasks {
		references[reference] = struct{}{}
	}
	ordered := sortedProjectionRefs(references)
	if len(ordered) == 0 {
		return kernel.AggregateRef{}, false
	}
	return ordered[0], true
}

func (r ProjectionRebuildReceipt) String() string {
	return fmt.Sprintf("stories=%d tasks=%d events=%d match=%t", r.StoryCount, r.TaskCount, r.SourceEventCount, r.ExactMatch)
}
