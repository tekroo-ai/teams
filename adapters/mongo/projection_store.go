package mongo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

type ProjectionApplyResult struct {
	EventID             kernel.UUIDv7
	Applied             bool
	Duplicate           bool
	ProjectionDocuments uint64
	Checkpoint          ProjectionCheckpoint
	Fault               *ProjectionFault
}

type projectionFaultDocument struct {
	ID   string `bson:"_id"`
	Data []byte `bson:"data"`
}

func (s *Store) ApplyOperationalProjectionEvent(ctx context.Context, eventID kernel.UUIDv7, observedAt time.Time) (ProjectionApplyResult, error) {
	if err := requireDeadline(ctx); err != nil {
		return ProjectionApplyResult{}, err
	}
	if !eventID.Valid() || observedAt.IsZero() {
		return ProjectionApplyResult{}, ErrInvalidDecision
	}
	var source eventDocument
	if err := s.db.Collection("events").FindOne(ctx, bson.D{{Key: "_id", Value: string(eventID)}}).Decode(&source); err != nil {
		return ProjectionApplyResult{}, err
	}
	var event kernel.DomainEvent
	if decode(source.Data, &event) != nil || event.EventID != eventID || source.AggregateKey != aggregateKey(event.Aggregate) || source.Revision != event.AggregateRevision {
		return ProjectionApplyResult{}, ErrCorruptAggregate
	}
	result := ProjectionApplyResult{EventID: eventID}
	if !projectionRelevant(event.Aggregate.Kind) {
		return result, nil
	}
	session, err := s.client.StartSession()
	if err != nil {
		return result, err
	}
	defer session.EndSession(ctx)
	_, err = session.WithTransaction(ctx, func(transactionContext context.Context) (any, error) {
		tasks, stories, targetErr := s.projectionTargets(transactionContext, event)
		if targetErr != nil {
			return nil, targetErr
		}
		checkpoint, found, loadErr := s.loadProjectionCheckpoint(transactionContext, event.Aggregate)
		if loadErr != nil {
			return nil, loadErr
		}
		var prior *ProjectionCheckpoint
		if found {
			prior = &checkpoint
		}
		if found && event.AggregateRevision < checkpoint.ProjectionRevision {
			result.Duplicate = true
			result.Checkpoint = checkpoint
			return nil, nil
		}
		advance := AssessProjectionAdvance(prior, event, observedAt)
		if advance.Duplicate {
			result.Duplicate = true
			result.Checkpoint = checkpoint
			return nil, nil
		}
		if !advance.Accepted {
			result.Fault = advance.Fault
			if advance.Fault != nil {
				bindProjectionFaultTarget(advance.Fault, tasks, stories)
				if advance.Fault.Aggregate.Kind == kernel.AggregateTask || advance.Fault.Aggregate.Kind == kernel.AggregateStory {
					if recordErr := s.recordProjectionFault(transactionContext, *advance.Fault); recordErr != nil {
						return nil, recordErr
					}
				}
			}
			return nil, nil
		}
		for _, task := range sortedProjectionRefs(tasks) {
			projection, materializeErr := s.materializeTaskProjection(transactionContext, task, event.EventID)
			if materializeErr != nil {
				return nil, materializeErr
			}
			if projection == nil {
				continue
			}
			document, documentErr := projectionDocument(*projection)
			if documentErr != nil {
				return nil, documentErr
			}
			if _, replaceErr := s.db.Collection("task_projections").ReplaceOne(transactionContext, bson.D{{Key: "_id", Value: document.ID}}, document, options.Replace().SetUpsert(true)); replaceErr != nil {
				return nil, replaceErr
			}
			result.ProjectionDocuments++
		}
		for _, story := range sortedProjectionRefs(stories) {
			projection, materializeErr := s.materializeStoryProjection(transactionContext, story, event.EventID)
			if materializeErr != nil {
				return nil, materializeErr
			}
			if projection == nil {
				continue
			}
			document, documentErr := projectionDocument(*projection)
			if documentErr != nil {
				return nil, documentErr
			}
			if _, replaceErr := s.db.Collection("story_projections").ReplaceOne(transactionContext, bson.D{{Key: "_id", Value: document.ID}}, document, options.Replace().SetUpsert(true)); replaceErr != nil {
				return nil, replaceErr
			}
			result.ProjectionDocuments++
		}
		digest, digestErr := operationalEventDigest(event)
		if digestErr != nil {
			return nil, digestErr
		}
		next := ProjectionCheckpoint{Kind: "projection_checkpoint", Projector: projectionCheckpointID(event.Aggregate), LastEventID: event.EventID, LastEventDigest: digest, ProjectionRevision: event.AggregateRevision}
		if checkpointErr := s.storeProjectionCheckpoint(transactionContext, next, prior); checkpointErr != nil {
			return nil, checkpointErr
		}
		result.Applied = true
		result.Checkpoint = next
		return nil, nil
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()).SetReadPreference(readpref.Primary()))
	if err != nil {
		if driver.IsDuplicateKeyError(err) {
			return result, ErrProjectionConflict
		}
		return result, err
	}
	if result.Fault != nil {
		switch result.Fault.FaultKind {
		case "REVISION_GAP":
			return result, ErrProjectionGap
		default:
			return result, ErrProjectionConflict
		}
	}
	return result, nil
}

func bindProjectionFaultTarget(fault *ProjectionFault, tasks, stories map[kernel.AggregateRef]struct{}) {
	if ordered := sortedProjectionRefs(tasks); len(ordered) > 0 {
		fault.Aggregate = ordered[0]
		fault.ProjectionKind = "TASK"
		return
	}
	if ordered := sortedProjectionRefs(stories); len(ordered) > 0 {
		fault.Aggregate = ordered[0]
		fault.ProjectionKind = "STORY"
	}
}

func (s *Store) ProjectPendingOperationalEvents(ctx context.Context, observedAt time.Time) (uint64, error) {
	if err := requireDeadline(ctx); err != nil {
		return 0, err
	}
	cursor, err := s.db.Collection("events").Find(ctx, bson.D{}, options.Find().SetSort(bson.D{{Key: "aggregate_key", Value: 1}, {Key: "revision", Value: 1}}))
	if err != nil {
		return 0, err
	}
	defer cursor.Close(ctx)
	events := make([]eventDocument, 0)
	for cursor.Next(ctx) {
		var document eventDocument
		if cursor.Decode(&document) != nil {
			return 0, ErrCorruptAggregate
		}
		events = append(events, document)
	}
	if err := cursor.Err(); err != nil {
		return 0, err
	}
	var applied uint64
	for _, document := range events {
		var event kernel.DomainEvent
		if decode(document.Data, &event) != nil {
			return applied, ErrCorruptAggregate
		}
		if !projectionRelevant(event.Aggregate.Kind) {
			continue
		}
		checkpoint, found, loadErr := s.loadProjectionCheckpoint(ctx, event.Aggregate)
		if loadErr != nil {
			return applied, loadErr
		}
		if found && checkpoint.ProjectionRevision >= event.AggregateRevision {
			continue
		}
		result, applyErr := s.ApplyOperationalProjectionEvent(ctx, event.EventID, observedAt)
		if applyErr != nil {
			return applied, applyErr
		}
		if result.Applied {
			applied++
		}
	}
	return applied, nil
}

func (s *Store) ReadTaskProjection(ctx context.Context, taskID kernel.UUIDv7) (TaskProjection, bool, error) {
	if err := requireDeadline(ctx); err != nil {
		return TaskProjection{}, false, err
	}
	var result TaskProjection
	found, err := s.readProjection(ctx, "task_projections", aggregateKey(kernel.AggregateRef{Kind: kernel.AggregateTask, ID: taskID}), &result)
	if err == nil && found && !result.Valid() {
		return TaskProjection{}, false, ErrCorruptAggregate
	}
	return result, found, err
}

func (s *Store) ReadStoryProjection(ctx context.Context, storyID kernel.UUIDv7) (StoryProjection, bool, error) {
	if err := requireDeadline(ctx); err != nil {
		return StoryProjection{}, false, err
	}
	var result StoryProjection
	found, err := s.readProjection(ctx, "story_projections", aggregateKey(kernel.AggregateRef{Kind: kernel.AggregateStory, ID: storyID}), &result)
	if err == nil && found && !result.Valid() {
		return StoryProjection{}, false, ErrCorruptAggregate
	}
	return result, found, err
}

func (s *Store) ReadProjectionFaults(ctx context.Context) ([]ProjectionFault, error) {
	if err := requireDeadline(ctx); err != nil {
		return nil, err
	}
	cursor, err := s.db.Collection("projection_faults").Find(ctx, bson.D{}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	result := make([]ProjectionFault, 0)
	for cursor.Next(ctx) {
		var document projectionFaultDocument
		if cursor.Decode(&document) != nil {
			return nil, ErrCorruptAggregate
		}
		var fault ProjectionFault
		if decode(document.Data, &fault) != nil {
			return nil, ErrCorruptAggregate
		}
		if _, validationErr := fault.RecordFaultPayload(); validationErr != nil {
			return nil, ErrCorruptAggregate
		}
		result = append(result, fault)
	}
	return result, cursor.Err()
}

func (s *Store) loadProjectionCheckpoint(ctx context.Context, aggregate kernel.AggregateRef) (ProjectionCheckpoint, bool, error) {
	var document valueDocument
	err := s.db.Collection("projection_checkpoints").FindOne(ctx, bson.D{{Key: "_id", Value: projectionCheckpointID(aggregate)}}).Decode(&document)
	if errors.Is(err, driver.ErrNoDocuments) {
		return ProjectionCheckpoint{}, false, nil
	}
	if err != nil {
		return ProjectionCheckpoint{}, false, err
	}
	var checkpoint ProjectionCheckpoint
	if decode(document.Data, &checkpoint) != nil || checkpoint.Kind != "projection_checkpoint" || checkpoint.Projector != projectionCheckpointID(aggregate) || !checkpoint.LastEventID.Valid() || !checkpoint.LastEventDigest.Valid() || checkpoint.ProjectionRevision == 0 {
		return ProjectionCheckpoint{}, false, ErrCorruptAggregate
	}
	return checkpoint, true, nil
}

func (s *Store) storeProjectionCheckpoint(ctx context.Context, checkpoint ProjectionCheckpoint, prior *ProjectionCheckpoint) error {
	data, err := encode(checkpoint)
	if err != nil {
		return err
	}
	document := valueDocument{ID: checkpoint.Projector, Data: data}
	if prior == nil {
		_, err = s.db.Collection("projection_checkpoints").InsertOne(ctx, document)
		return err
	}
	priorData, err := encode(*prior)
	if err != nil {
		return err
	}
	result, err := s.db.Collection("projection_checkpoints").ReplaceOne(ctx, bson.D{{Key: "_id", Value: checkpoint.Projector}, {Key: "data", Value: priorData}}, document)
	if err != nil {
		return err
	}
	if result.ModifiedCount != 1 {
		return ErrProjectionConflict
	}
	return nil
}

func (s *Store) recordProjectionFault(ctx context.Context, fault ProjectionFault) error {
	data, err := fault.RecordFaultPayload()
	if err != nil {
		return err
	}
	id := fmt.Sprintf("%s:%d:%s", projectionCheckpointID(fault.Aggregate), fault.ObservedRevision, fault.EventDigest)
	_, err = s.db.Collection("projection_faults").ReplaceOne(ctx, bson.D{{Key: "_id", Value: id}}, projectionFaultDocument{ID: id, Data: data}, options.Replace().SetUpsert(true))
	return err
}

func projectionRelevant(kind kernel.AggregateKind) bool {
	switch kind {
	case kernel.AggregateStory, kernel.AggregateTask, kernel.AggregateWorkBudget, kernel.AggregateWorkInvocation,
		kernel.AggregateCompletionReview, kernel.AggregateEscalation, kernel.AggregateVariantGroup, kernel.AggregateReleasePlan:
		return true
	default:
		return false
	}
}

func (s *Store) projectionTargets(ctx context.Context, event kernel.DomainEvent) (map[kernel.AggregateRef]struct{}, map[kernel.AggregateRef]struct{}, error) {
	tasks := map[kernel.AggregateRef]struct{}{}
	stories := map[kernel.AggregateRef]struct{}{}
	if event.Aggregate.Kind == kernel.AggregateTask {
		tasks[event.Aggregate] = struct{}{}
		if story, found, err := s.storyForTask(ctx, event.Aggregate); err != nil {
			return nil, nil, err
		} else if found {
			stories[story] = struct{}{}
		}
	}
	if event.Aggregate.Kind == kernel.AggregateStory {
		stories[event.Aggregate] = struct{}{}
	}
	snapshot, err := s.loadSnapshot(ctx, event.Aggregate, nil)
	if err != nil {
		return nil, nil, err
	}
	switch event.Aggregate.Kind {
	case kernel.AggregateWorkInvocation:
		if invocation, found := snapshot.WorkInvocations[event.Aggregate]; found {
			task := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: invocation.TaskID}
			tasks[task] = struct{}{}
			if story, storyFound, storyErr := s.storyForTask(ctx, task); storyErr != nil {
				return nil, nil, storyErr
			} else if storyFound {
				stories[story] = struct{}{}
			}
		}
	case kernel.AggregateWorkBudget:
		for task, binding := range snapshot.TaskWorkBudgets {
			if binding.BudgetAccountID != event.Aggregate.ID {
				continue
			}
			tasks[task] = struct{}{}
			if story, found, storyErr := s.storyForTask(ctx, task); storyErr != nil {
				return nil, nil, storyErr
			} else if found {
				stories[story] = struct{}{}
			}
		}
	case kernel.AggregateCompletionReview:
		if review, found := snapshot.Reviews[event.Aggregate]; found {
			addProjectionSubject(review.Subject, tasks, stories)
		}
	case kernel.AggregateEscalation:
		if escalation, found := snapshot.Escalations[event.Aggregate]; found {
			addProjectionSubject(escalation.Subject, tasks, stories)
		}
	case kernel.AggregateVariantGroup:
		if group, found := snapshot.VariantGroups[event.Aggregate]; found {
			tasks[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: group.TaskID}] = struct{}{}
		}
	case kernel.AggregateReleasePlan:
		if plan, found := snapshot.ReleasePlans[event.Aggregate]; found {
			stories[plan.Story] = struct{}{}
		}
	}
	return tasks, stories, nil
}

func addProjectionSubject(subject kernel.AggregateRef, tasks, stories map[kernel.AggregateRef]struct{}) {
	if subject.Kind == kernel.AggregateTask {
		tasks[subject] = struct{}{}
	}
	if subject.Kind == kernel.AggregateStory {
		stories[subject] = struct{}{}
	}
}

func (s *Store) storyForTask(ctx context.Context, task kernel.AggregateRef) (kernel.AggregateRef, bool, error) {
	event, err := s.creationEvent(ctx, task, "tekroo.event.task.created")
	if errors.Is(err, driver.ErrNoDocuments) {
		return kernel.AggregateRef{}, false, nil
	}
	if err != nil {
		return kernel.AggregateRef{}, false, err
	}
	var payload struct {
		StoryID kernel.UUIDv7 `json:"story_id"`
	}
	if decode(event.Payload, &payload) != nil || !payload.StoryID.Valid() {
		return kernel.AggregateRef{}, false, ErrCorruptAggregate
	}
	return kernel.AggregateRef{Kind: kernel.AggregateStory, ID: payload.StoryID}, true, nil
}
