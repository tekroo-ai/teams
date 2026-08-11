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
	if state == nil || state.Phase != kernel.PhaseAccepted || state.Revision != 6 || state.LifecycleEpoch != 1 {
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
