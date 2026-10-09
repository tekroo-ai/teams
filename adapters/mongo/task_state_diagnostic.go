package mongo

import (
	"context"
	"encoding/json"

	"github.com/tekroo-ai/teams/kernel"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// TaskStateDiagnostic is an explicitly partial, raw-state read, NOT the
// contract's budget-bound task_projection. Missing admission data stays absent.
type TaskStateDiagnostic struct {
	ID                kernel.UUIDv7        `json:"id"`
	Title             string               `json:"title"`
	AggregateRevision uint64               `json:"aggregate_revision"`
	LastEventID       kernel.UUIDv7        `json:"last_event_id"`
	Phase             kernel.Phase         `json:"phase"`
	Condition         kernel.Condition     `json:"condition"`
	OwnerFQN          *kernel.ActorFQN     `json:"owner_fqn"`
	ProjectionStatus  string               `json:"projection_status"`
	Block             *TaskBlockDiagnostic `json:"block,omitempty"`
}

type TaskBlockDiagnostic struct {
	EventID      kernel.UUIDv7 `json:"event_id"`
	Reason       string        `json:"reason"`
	BlockerRefs  []string      `json:"blocker_refs"`
	ReviewPolicy string        `json:"review_policy"`
}

// ListUnprojectedTasks reveals pre-activation blocks without changing accepted
// projections. Bounds the raw scan and fails visibly rather than truncating
// unfinished work into a misleading "all done" status.
func (s *Store) ListUnprojectedTasks(ctx context.Context, limit int64) ([]TaskStateDiagnostic, error) {
	if err := requireDeadline(ctx); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		return nil, ErrInvalidDecision
	}
	const maximum = 10000
	cursor, err := s.db.Collection("aggregates").Find(ctx, bson.D{{Key: "_id", Value: bson.D{{Key: "$regex", Value: "^task:"}}}}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(maximum+1))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	result := make([]TaskStateDiagnostic, 0)
	count := 0
	for cursor.Next(ctx) {
		count++
		if count > maximum {
			return nil, ErrBacklogLimit
		}
		var document aggregateDocument
		var state kernel.AggregateState
		if cursor.Decode(&document) != nil || decode(document.State, &state) != nil || state.Kind != kernel.AggregateTask || !state.ID.Valid() || state.Revision == 0 || state.Revision != document.Revision || document.ID != aggregateKey(kernel.AggregateRef{Kind: state.Kind, ID: state.ID}) {
			return nil, ErrCorruptAggregate
		}
		if state.Phase == kernel.PhaseCompleted || state.Phase == kernel.PhaseClosed {
			continue
		}
		projection, found, err := s.ReadTaskProjection(ctx, state.ID)
		if err != nil {
			return nil, err
		}
		if found && projection.AggregateRevision >= state.Revision {
			continue
		}
		if int64(len(result)) == limit {
			return nil, ErrBacklogLimit
		}
		ref := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: state.ID}
		created, err := s.creationEvent(ctx, ref, "tekroo.event.task.created")
		if err != nil {
			return nil, err
		}
		headEvent, exists, err := s.ReadAggregateEventAtRevision(ctx, ref, state.Revision)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, ErrCorruptAggregate
		}
		var blocked *kernel.DomainEvent
		if state.Condition == kernel.ConditionBlocked {
			// Profile maintenance can follow work.blocked. Select the latest
			// block at or before the state revision, not just the moving head.
			var document eventDocument
			filter := bson.D{{Key: "aggregate_key", Value: aggregateKey(ref)}, {Key: "event_type", Value: "tekroo.event.work.blocked"}, {Key: "revision", Value: bson.D{{Key: "$lte", Value: state.Revision}}}}
			if err := s.db.Collection("events").FindOne(ctx, filter, options.FindOne().SetSort(bson.D{{Key: "revision", Value: -1}})).Decode(&document); err != nil {
				return nil, err
			}
			blocked = new(kernel.DomainEvent)
			if decode(document.Data, blocked) != nil {
				return nil, ErrCorruptAggregate
			}
		}
		value, err := taskStateDiagnostic(state, headEvent.EventID, created, blocked)
		if err != nil {
			return nil, err
		}
		if found {
			value.ProjectionStatus = "STALE"
		}
		result = append(result, value)
	}
	return result, cursor.Err()
}

func taskStateDiagnostic(state kernel.AggregateState, head kernel.UUIDv7, created kernel.DomainEvent, blocked *kernel.DomainEvent) (TaskStateDiagnostic, error) {
	ref := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: state.ID}
	var description struct {
		Title string `json:"title"`
	}
	if state.Kind != kernel.AggregateTask || !ref.Valid() || !head.Valid() || state.Revision == 0 || created.Aggregate != ref || created.EventType != "tekroo.event.task.created" || created.AggregateRevision != 1 || json.Unmarshal(created.Payload, &description) != nil || description.Title == "" {
		return TaskStateDiagnostic{}, ErrCorruptAggregate
	}
	result := TaskStateDiagnostic{ID: state.ID, Title: description.Title, AggregateRevision: state.Revision, LastEventID: head, Phase: state.Phase, Condition: state.Condition, OwnerFQN: state.Ownership.OwnerFQN, ProjectionStatus: "NOT_READY"}
	if state.Condition == kernel.ConditionBlocked {
		var block TaskBlockDiagnostic
		if blocked == nil || blocked.Aggregate != ref || blocked.EventType != "tekroo.event.work.blocked" || !blocked.EventID.Valid() || blocked.AggregateRevision > state.Revision || json.Unmarshal(blocked.Payload, &block) != nil || block.Reason == "" || len(block.BlockerRefs) == 0 || block.ReviewPolicy == "" {
			return TaskStateDiagnostic{}, ErrCorruptAggregate
		}
		block.EventID = blocked.EventID
		result.Block = &block
	}
	return result, nil
}
