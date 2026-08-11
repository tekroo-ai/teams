package memory_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tekroo-ai/teams/adapters/memory"
	"github.com/tekroo-ai/teams/kernel"
)

func TestStoreCommitsAtomicallyAndReplaysExactReceipt(t *testing.T) {
	store := memory.NewStore()
	target := kernel.AggregateRef{Kind: kernel.AggregateStory, ID: uuid("00000000-0000-7000-8000-000000000001")}
	commandID := uuid("00000000-0000-7000-8000-000000000002")
	command := kernel.KernelCommand{CommandID: commandID, Target: target}
	fingerprint, err := kernel.CommandFingerprint(command)
	if err != nil {
		t.Fatal(err)
	}
	eventID := uuid("00000000-0000-7000-8000-000000000003")
	revision := uint64(1)
	decision := kernel.Decision{
		CommandFingerprint: fingerprint,
		NextState: &kernel.AggregateState{
			Kind: kernel.AggregateStory, ID: target.ID, Revision: 1,
			LifecycleEpoch: 1, Phase: kernel.PhaseDraft, Condition: kernel.ConditionRunnable,
		},
		Events: []kernel.DomainEvent{{EventID: eventID, Aggregate: target, AggregateRevision: 1}},
		Receipt: kernel.CommandReceipt{
			CommandID: commandID, Target: target, OutcomeCode: kernel.OutcomeApplied,
			ResultingRevision: &revision, EventIDs: []kernel.UUIDv7{eventID},
			ProvenanceDigest: kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		},
	}

	if err := store.Commit(context.Background(), kernel.Snapshot{}, decision); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := store.Commit(context.Background(), kernel.Snapshot{}, decision); !errors.Is(err, kernel.ErrDecisionAlreadyCommitted) {
		t.Fatalf("exact replay error = %v, want ErrDecisionAlreadyCommitted", err)
	}
	conflicting := decision
	conflicting.Receipt.Target.ID = uuid("00000000-0000-7000-8000-000000000004")
	conflictingCommand := command
	conflictingCommand.Target = conflicting.Receipt.Target
	conflicting.CommandFingerprint, err = kernel.CommandFingerprint(conflictingCommand)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Commit(context.Background(), kernel.Snapshot{}, conflicting); !errors.Is(err, memory.ErrReceiptConflict) {
		t.Fatalf("conflicting replay error = %v, want ErrReceiptConflict", err)
	}
	if store.EventCount() != 1 {
		t.Fatalf("event count = %d, want 1", store.EventCount())
	}
	snapshot, err := store.Load(context.Background(), target)
	if err != nil || !snapshot.Exists || snapshot.Revision != 1 {
		t.Fatalf("load snapshot = %#v, %v", snapshot, err)
	}
	receipt, found, err := store.LookupReceipt(context.Background(), command)
	if err != nil || !found || receipt.OutcomeCode != kernel.OutcomeApplied {
		t.Fatalf("lookup receipt = %#v, %t, %v", receipt, found, err)
	}
}

func TestStoreRejectsStaleSnapshot(t *testing.T) {
	store := memory.NewStore()
	target := kernel.AggregateRef{Kind: kernel.AggregateStory, ID: uuid("00000000-0000-7000-8000-000000000001")}
	revision := uint64(1)
	decision := kernel.Decision{
		CommandFingerprint: kernel.Digest("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
		NextState:          &kernel.AggregateState{Kind: kernel.AggregateStory, ID: target.ID, Revision: 1},
		Receipt: kernel.CommandReceipt{
			CommandID: uuid("00000000-0000-7000-8000-000000000002"),
			Target:    target, ResultingRevision: &revision,
		},
	}
	if err := store.Commit(context.Background(), kernel.Snapshot{}, decision); err != nil {
		t.Fatal(err)
	}
	decision.Receipt.CommandID = uuid("00000000-0000-7000-8000-000000000003")
	if err := store.Commit(context.Background(), kernel.Snapshot{}, decision); !errors.Is(err, memory.ErrConflict) {
		t.Fatalf("stale commit error = %v, want ErrConflict", err)
	}
}

func TestStoreTracksRevisionWithoutWorkState(t *testing.T) {
	store := memory.NewStore()
	target := kernel.AggregateRef{Kind: kernel.AggregateExecution, ID: uuid("00000000-0000-7000-8000-000000000001")}
	revision := uint64(1)
	decision := kernel.Decision{CommandFingerprint: kernel.Digest("cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"), Receipt: kernel.CommandReceipt{
		CommandID:         uuid("00000000-0000-7000-8000-000000000002"),
		Target:            target,
		OutcomeCode:       kernel.OutcomeApplied,
		ResultingRevision: &revision,
	}}
	if err := store.Commit(context.Background(), kernel.Snapshot{}, decision); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Load(context.Background(), target)
	if err != nil || !snapshot.Exists || snapshot.Revision != 1 || snapshot.State != nil {
		t.Fatalf("generic snapshot = %#v, %v", snapshot, err)
	}
}

func TestStoreHonorsCancelledContext(t *testing.T) {
	store := memory.NewStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := store.Load(ctx, kernel.AggregateRef{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("load error = %v, want context.Canceled", err)
	}
}

func uuid(value string) kernel.UUIDv7 { return kernel.UUIDv7(value) }
