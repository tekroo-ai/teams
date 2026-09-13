package operationalruntime

import (
	"context"
	"errors"
	"time"

	"github.com/tekroo-ai/teams/adapters/mongo"
	"github.com/tekroo-ai/teams/kernel"
)

const MaximumEventWait = 24 * time.Hour

var ErrInvalidEventWait = errors.New("invalid event wait request")

type EventWaitRequest struct {
	Aggregate     kernel.AggregateRef `json:"aggregate"`
	AfterRevision uint64              `json:"after_revision"`
	EventTypes    []string            `json:"event_types,omitempty"`
	TimeoutMillis uint64              `json:"timeout_millis"`
}

func (request EventWaitRequest) Valid() bool {
	if !request.Aggregate.Valid() || request.TimeoutMillis == 0 || request.TimeoutMillis > uint64(MaximumEventWait/time.Millisecond) || len(request.EventTypes) > 64 {
		return false
	}
	seen := make(map[string]struct{}, len(request.EventTypes))
	for _, eventType := range request.EventTypes {
		if len(eventType) == 0 || len(eventType) > 256 {
			return false
		}
		if _, duplicate := seen[eventType]; duplicate {
			return false
		}
		seen[eventType] = struct{}{}
	}
	return true
}

type EventWaitOutcome string

const (
	EventWaitMatched  EventWaitOutcome = "MATCHED"
	EventWaitTimedOut EventWaitOutcome = "TIMED_OUT"
)

type EventWaitResult struct {
	Outcome       EventWaitOutcome    `json:"outcome"`
	Aggregate     kernel.AggregateRef `json:"aggregate"`
	AfterRevision uint64              `json:"after_revision"`
	Event         *EventNotice        `json:"event,omitempty"`
}

type EventNotice struct {
	EventID           kernel.UUIDv7       `json:"event_id"`
	EventType         string              `json:"event_type"`
	Aggregate         kernel.AggregateRef `json:"aggregate"`
	AggregateRevision uint64              `json:"aggregate_revision"`
	CommittedAt       time.Time           `json:"committed_at"`
}

func (service *ProductionService) WaitForEvent(ctx context.Context, request EventWaitRequest) (EventWaitResult, error) {
	if service == nil || service.Store == nil || !request.Valid() {
		return EventWaitResult{}, ErrInvalidEventWait
	}
	waitContext, cancel := context.WithTimeout(ctx, time.Duration(request.TimeoutMillis)*time.Millisecond)
	defer cancel()
	event, err := service.Store.WaitForAggregateEvent(waitContext, request.Aggregate, request.AfterRevision, request.EventTypes)
	if errors.Is(err, context.DeadlineExceeded) {
		return EventWaitResult{Outcome: EventWaitTimedOut, Aggregate: request.Aggregate, AfterRevision: request.AfterRevision}, nil
	}
	if err != nil {
		if errors.Is(err, mongo.ErrInvalidEventWait) {
			return EventWaitResult{}, ErrInvalidEventWait
		}
		return EventWaitResult{}, err
	}
	notice := EventNotice{EventID: event.EventID, EventType: event.EventType, Aggregate: event.Aggregate, AggregateRevision: event.AggregateRevision, CommittedAt: event.CommittedAt}
	return EventWaitResult{Outcome: EventWaitMatched, Aggregate: request.Aggregate, AfterRevision: request.AfterRevision, Event: &notice}, nil
}
