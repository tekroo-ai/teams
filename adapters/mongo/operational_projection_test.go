package mongo

import (
	"errors"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

func TestProjectionAdvanceAcceptsNextAndExactDuplicate(t *testing.T) {
	event := projectionTestEvent(4, "00000000-0000-7000-8000-000000000904")
	priorEvent := projectionTestEvent(3, "00000000-0000-7000-8000-000000000903")
	digest, err := operationalEventDigest(priorEvent)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := ProjectionCheckpoint{Kind: "projection_checkpoint", Projector: projectionCheckpointID(event.Aggregate), LastEventID: priorEvent.EventID, LastEventDigest: digest, ProjectionRevision: 3}
	advance := AssessProjectionAdvance(&checkpoint, event, time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC))
	if !advance.Accepted || advance.Duplicate || advance.Reason != "NEXT" {
		t.Fatalf("next advance = %#v", advance)
	}
	eventDigest, err := operationalEventDigest(event)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.LastEventID = event.EventID
	checkpoint.LastEventDigest = eventDigest
	checkpoint.ProjectionRevision = 4
	duplicate := AssessProjectionAdvance(&checkpoint, event, time.Date(2026, 8, 31, 12, 1, 0, 0, time.UTC))
	if !duplicate.Accepted || !duplicate.Duplicate || duplicate.Reason != "DUPLICATE" {
		t.Fatalf("duplicate advance = %#v", duplicate)
	}
}

func TestProjectionAdvanceFailsClosedOnGapAndConflict(t *testing.T) {
	prior := projectionTestEvent(3, "00000000-0000-7000-8000-000000000903")
	digest, err := operationalEventDigest(prior)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := ProjectionCheckpoint{Kind: "projection_checkpoint", Projector: projectionCheckpointID(prior.Aggregate), LastEventID: prior.EventID, LastEventDigest: digest, ProjectionRevision: 3}
	gap := AssessProjectionAdvance(&checkpoint, projectionTestEvent(5, "00000000-0000-7000-8000-000000000905"), time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC))
	if gap.Accepted || gap.Fault == nil || gap.Fault.FaultKind != "REVISION_GAP" || gap.Fault.ExpectedRevision != 4 || gap.Fault.ObservedRevision != 5 {
		t.Fatalf("gap advance = %#v", gap)
	}
	conflict := AssessProjectionAdvance(&checkpoint, projectionTestEvent(3, "00000000-0000-7000-8000-000000000999"), time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC))
	if conflict.Accepted || conflict.Fault == nil || conflict.Fault.FaultKind != "REVISION_CONFLICT" {
		t.Fatalf("conflict advance = %#v", conflict)
	}
}

func TestProjectionSourceValidationRejectsRevisionGap(t *testing.T) {
	events := []kernel.DomainEvent{
		projectionTestEvent(1, "00000000-0000-7000-8000-000000000901"),
		projectionTestEvent(3, "00000000-0000-7000-8000-000000000903"),
	}
	for index := range events {
		events[index].Aggregate.Kind = kernel.AggregateWorkBudget
		events[index].EventType = "tekroo.event.work-budget.amended"
	}
	if err := validateProjectionSourceStreams(events); !errors.Is(err, ErrProjectionGap) {
		t.Fatalf("gap validation error = %v", err)
	}
}

func TestCanonicalUUIDsSortsAndDeduplicates(t *testing.T) {
	values := canonicalUUIDs([]kernel.UUIDv7{
		"00000000-0000-7000-8000-000000000903",
		"00000000-0000-7000-8000-000000000901",
		"00000000-0000-7000-8000-000000000903",
	})
	if len(values) != 2 || values[0] != "00000000-0000-7000-8000-000000000901" || values[1] != "00000000-0000-7000-8000-000000000903" {
		t.Fatalf("canonical values = %#v", values)
	}
}

func projectionTestEvent(revision uint64, eventID kernel.UUIDv7) kernel.DomainEvent {
	return kernel.DomainEvent{
		ContractManifest: kernel.ContractIdentity, EventID: eventID,
		EventType: "tekroo.event.task.work-budget-bound", EventVersion: kernel.OperationalSchemaVersion,
		Aggregate:         kernel.AggregateRef{Kind: kernel.AggregateTask, ID: "00000000-0000-7000-8000-000000000900"},
		AggregateRevision: revision, LifecycleEpoch: 1, CommittedAt: time.Date(2026, 8, 31, 12, 0, int(revision), 0, time.UTC),
	}
}
