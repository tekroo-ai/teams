package kernel_test

import (
	"errors"
	"testing"

	"github.com/tekroo-ai/teams/kernel"
)

func TestFoldAggregateReconstructsAndDetectsCorruption(t *testing.T) {
	aggregate := kernel.AggregateRef{Kind: kernel.AggregateStory, ID: mustUUID(t, "00000000-0000-7000-8000-000000000081")}
	types := []string{
		"tekroo.event.story.created",
		"tekroo.event.story.authorized",
		"tekroo.event.story.planning-started",
		"tekroo.event.story.activated",
		"tekroo.event.story.completed",
		"tekroo.event.story.accepted",
	}
	events := make([]kernel.DomainEvent, len(types))
	for index, eventType := range types {
		events[index] = kernel.DomainEvent{
			EventID:           mustUUID(t, eventIDForOrdinal(index+1)),
			EventType:         eventType,
			Aggregate:         aggregate,
			AggregateRevision: uint64(index + 1),
			LifecycleEpoch:    1,
		}
	}
	state, err := kernel.FoldAggregate(events)
	if err != nil {
		t.Fatal(err)
	}
	if state == nil || state.Phase != kernel.PhaseAccepted || state.Revision != 6 || state.LifecycleEpoch != 1 || state.ScopeRevision != 1 {
		t.Fatalf("folded state = %#v", state)
	}

	reordered := append([]kernel.DomainEvent(nil), events...)
	reordered[1], reordered[2] = reordered[2], reordered[1]
	if _, err := kernel.FoldAggregate(reordered); !errors.Is(err, kernel.ErrCorruptEventStream) {
		t.Fatalf("reordered stream error = %v", err)
	}
	missing := append([]kernel.DomainEvent(nil), events[:1]...)
	missing = append(missing, events[2:]...)
	if _, err := kernel.FoldAggregate(missing); !errors.Is(err, kernel.ErrCorruptEventStream) {
		t.Fatalf("missing stream error = %v", err)
	}
	wrongEpoch := append([]kernel.DomainEvent(nil), events...)
	wrongEpoch[4].LifecycleEpoch = 2
	if _, err := kernel.FoldAggregate(wrongEpoch); !errors.Is(err, kernel.ErrCorruptEventStream) {
		t.Fatalf("epoch-corrupt stream error = %v", err)
	}
}

func TestFoldAggregateChangesScopeOnlyOnExplicitReopening(t *testing.T) {
	aggregate := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: mustUUID(t, "00000000-0000-7000-8000-000000000081")}
	events := []kernel.DomainEvent{
		{EventID: mustUUID(t, "00000000-0000-7000-8000-000000000091"), EventType: "tekroo.event.task.created", Aggregate: aggregate, AggregateRevision: 1, LifecycleEpoch: 1, Payload: []byte(`{}`)},
		{EventID: mustUUID(t, "00000000-0000-7000-8000-000000000092"), EventType: "tekroo.event.task.readied", Aggregate: aggregate, AggregateRevision: 2, LifecycleEpoch: 1, Payload: []byte(`{}`)},
		{EventID: mustUUID(t, "00000000-0000-7000-8000-000000000093"), EventType: "tekroo.event.task.activated", Aggregate: aggregate, AggregateRevision: 3, LifecycleEpoch: 1, Payload: []byte(`{}`)},
		{EventID: mustUUID(t, "00000000-0000-7000-8000-000000000094"), EventType: "tekroo.event.task.completed", Aggregate: aggregate, AggregateRevision: 4, LifecycleEpoch: 1, Payload: []byte(`{}`)},
		{EventID: mustUUID(t, "00000000-0000-7000-8000-000000000095"), EventType: "tekroo.event.work.reopened", Aggregate: aggregate, AggregateRevision: 5, LifecycleEpoch: 2, Payload: []byte(`{"new_scope_revision":2}`)},
	}
	state, err := kernel.FoldAggregate(events)
	if err != nil {
		t.Fatal(err)
	}
	if state == nil || state.ScopeRevision != 2 || state.LifecycleEpoch != 2 || state.Phase != kernel.PhaseActive {
		t.Fatalf("reopened state = %#v", state)
	}

	stale := append([]kernel.DomainEvent(nil), events...)
	stale[4].Payload = []byte(`{"new_scope_revision":1}`)
	if _, err := kernel.FoldAggregate(stale); !errors.Is(err, kernel.ErrCorruptEventStream) {
		t.Fatalf("stale-scope stream error = %v", err)
	}
}

func eventIDForOrdinal(ordinal int) string {
	return []string{
		"00000000-0000-7000-8000-000000000091",
		"00000000-0000-7000-8000-000000000092",
		"00000000-0000-7000-8000-000000000093",
		"00000000-0000-7000-8000-000000000094",
		"00000000-0000-7000-8000-000000000095",
		"00000000-0000-7000-8000-000000000096",
	}[ordinal-1]
}
