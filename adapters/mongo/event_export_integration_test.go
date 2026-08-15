//go:build mongo_integration

package mongo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/eventexport"
	"github.com/tekroo-ai/teams/kernel"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestEventExportReadsExactEventsStreamFirstWithoutOutboxMutation(t *testing.T) {
	store := openTestStore(t)
	first := exportDecision(t, 1)
	commitDecision(t, store, first)
	before := rawOutboxDocument(t, store, first.Outbox[0].IntentID)
	source, err := NewEventExportSource(store, "teams-main", 100)
	if err != nil {
		t.Fatal(err)
	}
	openContext, openCancel := context.WithTimeout(context.Background(), 10*time.Second)
	feed, err := source.Open(openContext, "teams-main", nil)
	openCancel()
	if err != nil {
		t.Fatal(err)
	}
	var initialPosition eventExportPosition
	if json.Unmarshal(feed.Position(), &initialPosition) != nil || len(initialPosition.ResumeToken) == 0 {
		t.Fatal("initial live boundary did not expose a resumable change-stream token")
	}
	defer closeEventFeed(t, feed)
	second := exportDecision(t, 2)
	commitDecision(t, store, second)
	wanted := map[kernel.UUIDv7][]byte{}
	for _, decision := range []kernel.Decision{first, second} {
		encoded, marshalErr := json.Marshal(decision.Events[0])
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		wanted[decision.Events[0].EventID] = encoded
	}
	seen := map[kernel.UUIDv7]bool{}
	for len(seen) < len(wanted) {
		nextContext, nextCancel := context.WithTimeout(context.Background(), 10*time.Second)
		raw, nextErr := feed.Next(nextContext)
		nextCancel()
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		event, decodeErr := eventexport.DecodeDomainEvent(raw.Bytes)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if expected, exists := wanted[event.EventID]; !exists || !bytes.Equal(raw.Bytes, expected) {
			t.Fatalf("unexpected or changed event %s", event.EventID)
		}
		seen[event.EventID] = true
	}
	after := rawOutboxDocument(t, store, first.Outbox[0].IntentID)
	if !bytes.Equal(before, after) {
		t.Fatal("event export mutated outbox state")
	}
	countContext, countCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer countCancel()
	checkpoints, err := store.db.Collection("consumer_checkpoints").CountDocuments(countContext, bson.D{})
	if err != nil || checkpoints != 0 {
		t.Fatalf("export checkpoints = %d, %v", checkpoints, err)
	}
}

func TestEventExportResumeIsAtLeastOnceAndDoesNotOmitNewEvent(t *testing.T) {
	store := openTestStore(t)
	first := exportDecision(t, 1)
	commitDecision(t, store, first)
	source, _ := NewEventExportSource(store, "teams-main", 100)
	openContext, openCancel := context.WithTimeout(context.Background(), 10*time.Second)
	feed, err := source.Open(openContext, "teams-main", nil)
	openCancel()
	if err != nil {
		t.Fatal(err)
	}
	nextContext, nextCancel := context.WithTimeout(context.Background(), 10*time.Second)
	_, err = feed.Next(nextContext)
	nextCancel()
	if err != nil {
		t.Fatal(err)
	}
	position := append([]byte(nil), feed.Position()...)
	closeEventFeed(t, feed)
	second := exportDecision(t, 2)
	commitDecision(t, store, second)
	resumeContext, resumeCancel := context.WithTimeout(context.Background(), 10*time.Second)
	resumed, err := source.Open(resumeContext, "teams-main", position)
	resumeCancel()
	if err != nil {
		t.Fatal(err)
	}
	defer closeEventFeed(t, resumed)
	found := false
	for attempts := 0; attempts < 4 && !found; attempts++ {
		readContext, readCancel := context.WithTimeout(context.Background(), 10*time.Second)
		raw, readErr := resumed.Next(readContext)
		readCancel()
		if readErr != nil {
			t.Fatal(readErr)
		}
		event, decodeErr := eventexport.DecodeDomainEvent(raw.Bytes)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		found = event.EventID == second.Events[0].EventID
	}
	if !found {
		t.Fatal("resumed export omitted the newly committed event")
	}
}

func TestEventExportBacklogAndContextsAreBounded(t *testing.T) {
	store := openTestStore(t)
	commitDecision(t, store, exportDecision(t, 1))
	commitDecision(t, store, exportDecision(t, 2))
	source, _ := NewEventExportSource(store, "teams-main", 1)
	if _, err := source.Open(context.Background(), "teams-main", nil); !errors.Is(err, ErrDeadlineRequired) {
		t.Fatalf("unbounded open = %v", err)
	}
	openContext, openCancel := context.WithTimeout(context.Background(), 10*time.Second)
	feed, err := source.Open(openContext, "teams-main", nil)
	openCancel()
	if err != nil {
		t.Fatal(err)
	}
	defer closeEventFeed(t, feed)
	readContext, readCancel := context.WithTimeout(context.Background(), 10*time.Second)
	_, err = feed.Next(readContext)
	readCancel()
	if err != nil {
		t.Fatal(err)
	}
	readContext, readCancel = context.WithTimeout(context.Background(), 10*time.Second)
	_, err = feed.Next(readContext)
	readCancel()
	if !errors.Is(err, ErrEventBacklogLimit) {
		t.Fatalf("backlog error = %v", err)
	}
}

func TestReadOnlyEventExportSourceVerifiesExistingMetadataWithoutCreatingState(t *testing.T) {
	store := openTestStore(t)
	decision := exportDecision(t, 1)
	commitDecision(t, store, decision)
	before := collectionNames(t, store)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	source, err := OpenReadOnlyEventExportSource(ctx, ReadOnlyEventExportConfig{URI: testMongoURI, Database: store.db.Name(), SourceID: "teams-main", ContractIdentity: kernel.ContractIdentity, ManifestSHA256: testManifestSHA, MigrationLevel: 1, BacklogLimit: 100})
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		closeContext, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		if err := source.Close(closeContext); err != nil {
			t.Fatal(err)
		}
	}()
	openContext, openCancel := context.WithTimeout(context.Background(), 10*time.Second)
	feed, err := source.Open(openContext, "teams-main", nil)
	openCancel()
	if err != nil {
		t.Fatal(err)
	}
	readContext, readCancel := context.WithTimeout(context.Background(), 10*time.Second)
	raw, err := feed.Next(readContext)
	readCancel()
	if err != nil {
		t.Fatal(err)
	}
	event, err := eventexport.DecodeDomainEvent(raw.Bytes)
	if err != nil || event.EventID != decision.Events[0].EventID {
		t.Fatalf("read-only event = %#v, %v", event, err)
	}
	closeEventFeed(t, feed)
	after := collectionNames(t, store)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("read-only source changed collections: before=%v after=%v", before, after)
	}
}

func rawOutboxDocument(t *testing.T, store *Store, intentID kernel.UUIDv7) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, err := store.db.Collection("outbox").FindOne(ctx, bson.D{{Key: "_id", Value: string(intentID)}}).Raw()
	if err != nil {
		t.Fatal(err)
	}
	return append([]byte(nil), raw...)
}

func exportDecision(t *testing.T, ordinal int) kernel.Decision {
	t.Helper()
	decision := completeDecision(t, ordinal)
	decision.Events[0].EventVersion = "1.6.0"
	decision.Events[0].CommittedAt = testNow()
	return decision
}

func closeEventFeed(t *testing.T, feed eventexport.Feed) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := feed.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func collectionNames(t *testing.T, store *Store) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	names, err := store.db.ListCollectionNames(ctx, bson.D{})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	return names
}
