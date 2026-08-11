//go:build mongo_integration

package mongo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/fake"
	"github.com/tekroo-ai/teams/adapters/memory"
	"github.com/tekroo-ai/teams/kernel"
	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const testManifestSHA = kernel.Digest("5ff83483ce43ace2e06f2cc2f57dd552342553fc2389f3cc775b761c0c6d6d7c")

var (
	testMongoURI string
	databaseSeq  atomic.Uint64
)

func TestMain(m *testing.M) {
	process, uri, err := startMongod(true)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	testMongoURI = uri
	code := m.Run()
	stopMongod(process)
	os.Exit(code)
}

func TestStartupRejectsUnsupportedTopology(t *testing.T) {
	process, uri, err := startMongod(false)
	if err != nil {
		t.Fatal(err)
	}
	defer stopMongod(process)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = Open(ctx, testConfig(uri, nextDatabase(t)))
	if !errors.Is(err, ErrUnsupportedTopology) {
		t.Fatalf("Open standalone error = %v, want ErrUnsupportedTopology", err)
	}
}

func TestStartupPinsMetadataAndRequiredIndexes(t *testing.T) {
	manifest, err := os.ReadFile("../../CONTRACTS/tekroo.kernel.contracts/0.4.0/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	manifestDigest := sha256.Sum256(manifest)
	if got := hex.EncodeToString(manifestDigest[:]); got != string(testManifestSHA) {
		t.Fatalf("configured manifest digest = %s, canonical manifest digest = %s", testManifestSHA, got)
	}
	database := nextDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	store, err := Open(ctx, testConfig(testMongoURI, database))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = store.db.Drop(ctx)
		_ = store.Close(ctx)
	})
	wanted := map[string]bool{"aggregate_revision_unique": true, "idempotency_scope_unique": true}
	for collection, indexName := range map[string]string{"events": "aggregate_revision_unique", "receipts": "idempotency_scope_unique"} {
		cursor, err := store.db.Collection(collection).Indexes().List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for cursor.Next(ctx) {
			var index bson.M
			if err := cursor.Decode(&index); err != nil {
				t.Fatal(err)
			}
			name, _ := index["name"].(string)
			unique, _ := index["unique"].(bool)
			if name == indexName && unique {
				wanted[indexName] = false
			}
		}
		_ = cursor.Close(ctx)
	}
	for name, missing := range wanted {
		if missing {
			t.Fatalf("required unique index %q was not observed", name)
		}
	}
	wrong := testConfig(testMongoURI, database)
	wrong.ManifestSHA256 = kernel.Digest("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
	if _, err := Open(ctx, wrong); !errors.Is(err, ErrMetadataMismatch) {
		t.Fatalf("metadata mismatch error = %v, want ErrMetadataMismatch", err)
	}
	wrong = testConfig(testMongoURI, database)
	wrong.BacklogLimit = 1
	if _, err := Open(ctx, wrong); !errors.Is(err, ErrMetadataMismatch) {
		t.Fatalf("delivery policy mismatch error = %v, want ErrMetadataMismatch", err)
	}
}

func TestTransactionFaultScheduleIsAllOrNone(t *testing.T) {
	for _, point := range []string{"before-state", "before-events", "before-receipt", "before-authority", "before-outbox"} {
		t.Run(point, func(t *testing.T) {
			store := openTestStore(t)
			store.fault = point
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := store.Commit(ctx, kernel.Snapshot{}, completeDecision(t, 1)); !errors.Is(err, ErrInjectedFault) {
				t.Fatalf("Commit error = %v, want ErrInjectedFault", err)
			}
			for _, collection := range []string{"aggregates", "events", "receipts", "authority", "provenance", "outbox"} {
				count, err := store.db.Collection(collection).CountDocuments(ctx, bson.D{})
				if err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Fatalf("partial commit at %s: %s count = %d", point, collection, count)
				}
			}
		})
	}
}

func TestLostAcknowledgementReconcilesByCommandID(t *testing.T) {
	store := openTestStore(t)
	decision := completeDecision(t, 1)
	store.uncertain = true
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := store.Commit(ctx, kernel.Snapshot{}, decision); !errors.Is(err, kernel.ErrCommitUncertain) {
		t.Fatalf("Commit error = %v, want ErrCommitUncertain", err)
	}
	command := commandForDecision(decision)
	receipt, found, err := store.LookupReceipt(ctx, command, testNow())
	if err != nil || !found || receipt.CommandID != decision.Receipt.CommandID {
		t.Fatalf("reconciled receipt = %#v, found=%t, err=%v", receipt, found, err)
	}
	if err := store.Commit(ctx, kernel.Snapshot{}, decision); !errors.Is(err, kernel.ErrDecisionAlreadyCommitted) {
		t.Fatalf("exact retry error = %v, want ErrDecisionAlreadyCommitted", err)
	}
}

func TestCommittedStateSurvivesApplicationRestart(t *testing.T) {
	database := nextDatabase(t)
	config := testConfig(testMongoURI, database)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	store, err := Open(ctx, config)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	decision := completeDecision(t, 1)
	commitDecision(t, store, decision)
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	if err := store.Close(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	ctx, cancel = context.WithTimeout(context.Background(), 15*time.Second)
	reopened, err := Open(ctx, config)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = reopened.db.Drop(ctx)
		_ = reopened.Close(ctx)
	})
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	receipt, found, err := reopened.LookupReceipt(ctx, commandForDecision(decision), testNow())
	if err != nil || !found || receipt.CommandID != decision.Receipt.CommandID {
		t.Fatalf("receipt after restart = %#v, found=%t, err=%v", receipt, found, err)
	}
}

func TestCommittedStateSurvivesMongoCrashRecovery(t *testing.T) {
	process, uri, err := startMongod(true)
	if err != nil {
		t.Fatal(err)
	}
	defer stopMongod(process)
	database := nextDatabase(t)
	config := testConfig(uri, database)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	store, err := Open(ctx, config)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	decision := completeDecision(t, 1)
	commitDecision(t, store, decision)
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	if err := store.Close(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	crashMongod(process)
	if err := restartMongod(process); err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 15*time.Second)
	recovered, err := Open(ctx, config)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = recovered.Close(ctx)
	}()
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	receipt, found, err := recovered.LookupReceipt(ctx, commandForDecision(decision), testNow())
	if err != nil || !found || receipt.CommandID != decision.Receipt.CommandID {
		t.Fatalf("receipt after mongod crash = %#v, found=%t, err=%v", receipt, found, err)
	}
}

func TestConcurrentCommitsProduceOneDecision(t *testing.T) {
	store := openTestStore(t)
	const contenders = 32
	start := make(chan struct{})
	results := make(chan error, contenders)
	var group sync.WaitGroup
	for ordinal := 1; ordinal <= contenders; ordinal++ {
		decision := retargetDecision(t, completeDecision(t, ordinal), testUUID(999))
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			results <- store.Commit(ctx, kernel.Snapshot{}, decision)
		}()
	}
	close(start)
	group.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("contender error = %v, want ErrConflict", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful commits = %d, want 1", successes)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, collection := range []string{"aggregates", "events", "receipts", "outbox"} {
		count, err := store.db.Collection(collection).CountDocuments(ctx, bson.D{})
		if err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("%s count = %d, want 1", collection, count)
		}
	}
}

func TestAggregateFoldDetectsMaterializedCorruption(t *testing.T) {
	store := openTestStore(t)
	decision := completeDecision(t, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := store.Commit(ctx, kernel.Snapshot{}, decision); err != nil {
		t.Fatal(err)
	}
	if err := store.VerifyAggregate(ctx, decision.Receipt.Target); err != nil {
		t.Fatalf("valid fold: %v", err)
	}
	corrupt := decision.NextState.Clone()
	corrupt.Condition = kernel.ConditionBlocked
	data, err := encode(corrupt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Collection("aggregates").UpdateOne(ctx, bson.D{{Key: "_id", Value: aggregateKey(decision.Receipt.Target)}}, bson.D{{Key: "$set", Value: bson.D{{Key: "state", Value: data}}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.VerifyAggregate(ctx, decision.Receipt.Target); !errors.Is(err, ErrCorruptAggregate) {
		t.Fatalf("corrupt fold error = %v, want ErrCorruptAggregate", err)
	}
}

func TestChangeStreamOpensBeforeBacklogWithoutGap(t *testing.T) {
	store := openTestStore(t)
	first := completeDecision(t, 1)
	commitDecision(t, store, first)
	openCtx, openCancel := context.WithTimeout(context.Background(), 10*time.Second)
	feed, err := store.OpenIntentFeed(openCtx, "boundary-consumer")
	openCancel()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = feed.Close(ctx)
	}()
	second := completeDecision(t, 2)
	commitDecision(t, store, second)
	seen := make(map[kernel.UUIDv7]bool)
	for len(seen) < 2 {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		intent, err := feed.Next(ctx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		seen[intent.Intent.IntentID] = true
	}
	if !seen[first.Outbox[0].IntentID] || !seen[second.Outbox[0].IntentID] {
		t.Fatalf("boundary intents = %#v", seen)
	}
}

func TestClaimLifecycleIsOneWinnerAndEpochFenced(t *testing.T) {
	store := openTestStore(t)
	decision := completeDecision(t, 1)
	commitDecision(t, store, decision)
	now := testNow()
	const contenders = 24
	results := make(chan ClaimedIntent, contenders)
	errorsSeen := make(chan error, contenders)
	var group sync.WaitGroup
	for index := 0; index < contenders; index++ {
		holder := fmt.Sprintf("worker-%02d", index)
		group.Add(1)
		go func() {
			defer group.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			claim, err := store.AcquireIntent(ctx, decision.Outbox[0].IntentID, holder, now, time.Minute)
			if err != nil {
				errorsSeen <- err
				return
			}
			results <- claim
		}()
	}
	group.Wait()
	close(results)
	close(errorsSeen)
	claims := make([]ClaimedIntent, 0)
	for claim := range results {
		claims = append(claims, claim)
	}
	if len(claims) != 1 || claims[0].ClaimEpoch != 1 {
		t.Fatalf("winning claims = %#v, want one claim at epoch 1", claims)
	}
	for err := range errorsSeen {
		if !errors.Is(err, ErrStaleClaim) {
			t.Fatalf("loser error = %v, want ErrStaleClaim", err)
		}
	}
	winner := claims[0]
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := store.YieldIntent(ctx, winner.Intent.IntentID, winner.Holder, winner.ClaimEpoch, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	second, err := store.AcquireIntent(ctx, winner.Intent.IntentID, "replacement", now.Add(2*time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if second.ClaimEpoch != 2 {
		t.Fatalf("replacement epoch = %d, want 2", second.ClaimEpoch)
	}
	if err := store.ResolveIntent(ctx, winner.Intent.IntentID, winner.Holder, winner.ClaimEpoch, now.Add(3*time.Second), "STALE"); !errors.Is(err, ErrStaleClaim) {
		t.Fatalf("stale resolve error = %v, want ErrStaleClaim", err)
	}
	if err := store.ExtendIntent(ctx, second.Intent.IntentID, second.Holder, second.ClaimEpoch, now.Add(3*time.Second), time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := store.ResolveIntent(ctx, second.Intent.IntentID, second.Holder, second.ClaimEpoch, now.Add(4*time.Second), "DELIVERED"); err != nil {
		t.Fatal(err)
	}
}

func TestReaddressIsEpochPinnedAndCannotMutateOrganizationalState(t *testing.T) {
	store := openTestStore(t)
	decision := completeDecision(t, 1)
	commitDecision(t, store, decision)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var before aggregateDocument
	if err := store.db.Collection("aggregates").FindOne(ctx, bson.D{{Key: "_id", Value: aggregateKey(decision.Receipt.Target)}}).Decode(&before); err != nil {
		t.Fatal(err)
	}
	now := testNow()
	claim, err := store.AcquireIntent(ctx, decision.Outbox[0].IntentID, "router-1", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReaddressIntent(ctx, claim.Intent.IntentID, claim.Holder, claim.ClaimEpoch, now.Add(time.Second), "teams::coder-2"); err != nil {
		t.Fatal(err)
	}
	if err := store.ReaddressIntent(ctx, claim.Intent.IntentID, claim.Holder, claim.ClaimEpoch, now.Add(2*time.Second), "teams::coder-3"); !errors.Is(err, ErrStaleClaim) {
		t.Fatalf("stale readdress error = %v, want ErrStaleClaim", err)
	}
	replacement, err := store.AcquireIntent(ctx, claim.Intent.IntentID, "router-2", now.Add(3*time.Second), time.Minute)
	if err != nil || replacement.Address != "teams::coder-2" || replacement.ClaimEpoch != 2 {
		t.Fatalf("readdressed claim = %#v, %v", replacement, err)
	}
	var after aggregateDocument
	if err := store.db.Collection("aggregates").FindOne(ctx, bson.D{{Key: "_id", Value: aggregateKey(decision.Receipt.Target)}}).Decode(&after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("delivery mutation changed aggregate\nbefore=%#v\nafter=%#v", before, after)
	}
}

func TestSweepReleasesThenDeadLettersAtFiniteAttemptLimit(t *testing.T) {
	store := openTestStore(t)
	decision := completeDecision(t, 1)
	commitDecision(t, store, decision)
	now := testNow()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	first, err := store.AcquireIntent(ctx, decision.Outbox[0].IntentID, "worker-1", now, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	swept, err := store.SweepExpiredIntents(ctx, now.Add(2*time.Second), 2)
	if err != nil || swept.Released != 1 || swept.DeadLetter != 0 {
		t.Fatalf("first sweep = %#v, %v", swept, err)
	}
	second, err := store.AcquireIntent(ctx, first.Intent.IntentID, "worker-2", now.Add(3*time.Second), time.Second)
	if err != nil || second.ClaimEpoch != 2 {
		t.Fatalf("second claim = %#v, %v", second, err)
	}
	swept, err = store.SweepExpiredIntents(ctx, now.Add(5*time.Second), 2)
	if err != nil || swept.Released != 0 || swept.DeadLetter != 1 {
		t.Fatalf("second sweep = %#v, %v", swept, err)
	}
	if _, err := store.AcquireIntent(ctx, first.Intent.IntentID, "worker-3", now.Add(6*time.Second), time.Second); !errors.Is(err, ErrStaleClaim) {
		t.Fatalf("dead-letter acquire error = %v, want ErrStaleClaim", err)
	}
}

func TestResumeCheckpointSurvivesFeedRestart(t *testing.T) {
	store := openTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	feed, err := store.OpenIntentFeed(ctx, "restart-consumer")
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	first := completeDecision(t, 1)
	commitDecision(t, store, first)
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	observed, err := feed.Next(ctx)
	cancel()
	if err != nil || observed.Intent.IntentID != first.Outbox[0].IntentID {
		t.Fatalf("first observed = %#v, %v", observed, err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	claim, err := store.AcquireIntent(ctx, first.Outbox[0].IntentID, "restart-worker", testNow(), time.Minute)
	if err == nil {
		err = store.ResolveIntent(ctx, claim.Intent.IntentID, claim.Holder, claim.ClaimEpoch, testNow().Add(time.Second), "DELIVERED")
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := feed.Close(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	second := completeDecision(t, 2)
	commitDecision(t, store, second)
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	restarted, err := store.OpenIntentFeed(ctx, "restart-consumer")
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = restarted.Close(ctx)
	}()
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	observed, err = restarted.Next(ctx)
	cancel()
	if err != nil || observed.Intent.IntentID != second.Outbox[0].IntentID {
		t.Fatalf("restart observed = %#v, %v", observed, err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	count, err := store.db.Collection("consumer_checkpoints").CountDocuments(ctx, bson.D{{Key: "_id", Value: "restart-consumer"}})
	if err != nil || count != 1 {
		t.Fatalf("durable checkpoint count = %d, %v", count, err)
	}
}

func TestBacklogResynchronizationIsBounded(t *testing.T) {
	config := testConfig(testMongoURI, nextDatabase(t))
	config.BacklogLimit = 1
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	store, err := Open(ctx, config)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = store.db.Drop(ctx)
		_ = store.Close(ctx)
	})
	commitDecision(t, store, completeDecision(t, 1))
	commitDecision(t, store, completeDecision(t, 2))
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	feed, err := store.OpenIntentFeed(ctx, "bounded-consumer")
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = feed.Close(ctx)
	}()
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	if _, err := feed.Next(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := feed.Next(ctx); !errors.Is(err, ErrBacklogLimit) {
		t.Fatalf("second backlog read error = %v, want ErrBacklogLimit", err)
	}
}

func TestResumeFailureClassificationIsNarrow(t *testing.T) {
	if !isResumeFailure(driver.CommandError{Code: 286, Name: "ChangeStreamHistoryLost"}) {
		t.Fatal("change-stream history loss was not classified for bounded resynchronization")
	}
	if isResumeFailure(driver.CommandError{Code: 13, Name: "Unauthorized"}) {
		t.Fatal("authorization failure must propagate instead of triggering resynchronization")
	}
	if isResumeFailure(errors.New("network unavailable")) {
		t.Fatal("ordinary infrastructure failure must propagate instead of triggering resynchronization")
	}
}

func TestMongoAndMemoryStoresProduceEquivalentSemanticSnapshot(t *testing.T) {
	policy := testPolicy()
	mongoStore := openTestStore(t)
	memoryStore := memory.NewStore()
	memoryStore.SetAuthorizationPolicy(policy)
	decision := completeDecision(t, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := mongoStore.Commit(ctx, kernel.Snapshot{}, decision); err != nil {
		t.Fatal(err)
	}
	if err := memoryStore.Commit(context.Background(), kernel.Snapshot{}, decision); err != nil {
		t.Fatal(err)
	}
	command := commandForDecision(decision)
	mongoSnapshot, err := mongoStore.LoadDecision(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	memorySnapshot, err := memoryStore.LoadDecision(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(mongoSnapshot, memorySnapshot) {
		t.Fatalf("semantic snapshots differ\nmongo=%#v\nmemory=%#v", mongoSnapshot, memorySnapshot)
	}
}

func TestOperationsRequireBoundedContexts(t *testing.T) {
	store := openTestStore(t)
	decision := completeDecision(t, 1)
	if err := store.Commit(context.Background(), kernel.Snapshot{}, decision); !errors.Is(err, ErrDeadlineRequired) {
		t.Fatalf("unbounded commit error = %v, want ErrDeadlineRequired", err)
	}
	if _, err := store.AcquireIntent(context.Background(), decision.Outbox[0].IntentID, "worker", testNow(), time.Second); !errors.Is(err, ErrDeadlineRequired) {
		t.Fatalf("unbounded claim error = %v, want ErrDeadlineRequired", err)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	store, err := Open(ctx, testConfig(testMongoURI, nextDatabase(t)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = store.db.Drop(ctx)
		_ = store.Close(ctx)
	})
	return store
}

func testConfig(uri, database string) Config {
	return Config{URI: uri, Database: database, ContractIdentity: kernel.ContractIdentity, ManifestSHA256: testManifestSHA, MigrationLevel: 1, Policy: testPolicy(), DeliveryPolicyRevision: 1}
}

func testPolicy() kernel.AuthorizationPolicy {
	return kernel.AuthorizationPolicy{
		PolicyDigest: kernel.Digest("9999999999999999999999999999999999999999999999999999999999999999"), Revision: 1,
		Grants: []kernel.AuthorityGrant{{
			GrantDigest: kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
			Grantee:     kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"},
			Scope: kernel.AuthorityScope{
				CommandTypes:  []string{"tekroo.command.story.create"},
				TargetKinds:   []kernel.AggregateKind{kernel.AggregateStory},
				CanReadTarget: true,
			},
		}},
	}
}

func nextDatabase(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("tekroo_integration_%d", databaseSeq.Add(1))
}

func commitDecision(t *testing.T, store *Store, decision kernel.Decision) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := store.Commit(ctx, kernel.Snapshot{}, decision); err != nil {
		t.Fatal(err)
	}
}

func completeDecision(t *testing.T, ordinal int) kernel.Decision {
	t.Helper()
	target := kernel.AggregateRef{Kind: kernel.AggregateStory, ID: testUUID(1000 + ordinal)}
	commandID := testUUID(2000 + ordinal)
	eventID := testUUID(3000 + ordinal)
	intentID := testUUID(4000 + ordinal)
	revision := uint64(1)
	state := kernel.AggregateState{Kind: kernel.AggregateStory, ID: target.ID, Revision: 1, LifecycleEpoch: 1, Phase: kernel.PhaseDraft, Condition: kernel.ConditionRunnable}
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	decision := kernel.Decision{
		NextState: &state,
		Events:    []kernel.DomainEvent{{EventID: eventID, EventType: "tekroo.event.story.created", Aggregate: target, AggregateRevision: 1, LifecycleEpoch: 1, Payload: payload}},
		Receipt:   kernel.CommandReceipt{CommandID: commandID, Target: target, OutcomeCode: kernel.OutcomeApplied, ResultingRevision: &revision, EventIDs: []kernel.UUIDv7{eventID}},
		Authority: kernel.AuthorityDecision{Principal: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"}, Allowed: true, Reason: "APPLIED"},
		Outbox:    []kernel.OutboxIntent{{IntentID: intentID, EventID: eventID, Kind: "DOMAIN_EVENT"}},
	}
	command := commandForDecision(decision)
	decision.CommandFingerprint, err = kernel.CommandFingerprint(command)
	if err != nil {
		t.Fatal(err)
	}
	decision.IdempotencyScope, err = kernel.IdempotencyScopeDigest(command)
	if err != nil {
		t.Fatal(err)
	}
	return attachProvenance(t, decision)
}

func retargetDecision(t *testing.T, decision kernel.Decision, targetID kernel.UUIDv7) kernel.Decision {
	t.Helper()
	decision.Receipt.Target.ID = targetID
	decision.NextState.ID = targetID
	decision.Events[0].Aggregate.ID = targetID
	command := commandForDecision(decision)
	var err error
	decision.CommandFingerprint, err = kernel.CommandFingerprint(command)
	if err != nil {
		t.Fatal(err)
	}
	decision.IdempotencyScope, err = kernel.IdempotencyScopeDigest(command)
	if err != nil {
		t.Fatal(err)
	}
	return attachProvenance(t, decision)
}

func attachProvenance(t *testing.T, decision kernel.Decision) kernel.Decision {
	t.Helper()
	basis, err := fake.ProvenanceBasis()
	if err != nil {
		t.Fatal(err)
	}
	command := commandForDecision(decision)
	context := kernel.DecisionContext{ReceivedAt: testNow(), DecidedAt: testNow().Add(time.Millisecond), EventID: decision.Events[0].EventID, IntentID: decision.Outbox[0].IntentID, Provenance: basis}
	provenance, digest, err := kernel.BuildDecisionProvenance(command, decision.CommandFingerprint, context)
	if err != nil {
		t.Fatal(err)
	}
	decision.Provenance = provenance
	decision.Receipt.ContractManifest = kernel.ContractIdentity
	decision.Receipt.CommandType = command.CommandType
	decision.Receipt.ProvenanceDigest = digest
	for index := range decision.Events {
		decision.Events[index].ContractManifest = kernel.ContractIdentity
		decision.Events[index].CommandID = command.CommandID
		decision.Events[index].Authority = command.Authority
		decision.Events[index].ProvenanceDigest = digest
	}
	return decision
}

func commandForDecision(decision kernel.Decision) kernel.KernelCommand {
	return kernel.KernelCommand{ContractManifest: kernel.ContractIdentity, CommandID: decision.Receipt.CommandID, CommandType: "tekroo.command.story.create", Target: decision.Receipt.Target, Authority: decision.Authority.Principal, IdempotencyKey: string(decision.Receipt.CommandID)}
}

func testUUID(ordinal int) kernel.UUIDv7 {
	return kernel.UUIDv7(fmt.Sprintf("00000000-0000-7000-8000-%012x", ordinal))
}

func testNow() time.Time {
	return time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
}

type mongodProcess struct {
	command    *exec.Cmd
	dir        string
	executable string
	arguments  []string
	uri        string
}

func startMongod(replicaSet bool) (*mongodProcess, string, error) {
	path, err := exec.LookPath("mongod")
	if err != nil {
		return nil, "", fmt.Errorf("mongod is required for mongo-integration: %w", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	dir, err := os.MkdirTemp("", "tekroo-mongod-")
	if err != nil {
		return nil, "", err
	}
	logPath := filepath.Join(dir, "mongod.log")
	args := []string{"--bind_ip", "127.0.0.1", "--port", fmt.Sprint(port), "--dbpath", dir, "--logpath", logPath, "--oplogSize", "128"}
	if replicaSet {
		args = append(args, "--replSet", "tekroo-test")
	}
	command := exec.Command(path, args...)
	if err := command.Start(); err != nil {
		_ = os.RemoveAll(dir)
		return nil, "", err
	}
	uri := fmt.Sprintf("mongodb://127.0.0.1:%d/?directConnection=true", port)
	process := &mongodProcess{command: command, dir: dir, executable: path, arguments: append([]string(nil), args...), uri: uri}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := driver.Connect(options.Client().ApplyURI(uri).SetServerSelectionTimeout(500 * time.Millisecond))
	if err != nil {
		stopMongod(process)
		return nil, "", err
	}
	defer client.Disconnect(context.Background())
	for {
		err = client.Ping(ctx, nil)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			stopMongod(process)
			return nil, "", fmt.Errorf("mongod did not start: %w (log %s)", err, logPath)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if replicaSet {
		configuration := bson.D{{Key: "_id", Value: "tekroo-test"}, {Key: "members", Value: bson.A{bson.D{{Key: "_id", Value: 0}, {Key: "host", Value: fmt.Sprintf("127.0.0.1:%d", port)}}}}}
		if err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "replSetInitiate", Value: configuration}}).Err(); err != nil {
			stopMongod(process)
			return nil, "", err
		}
		for {
			var hello struct {
				Primary bool `bson:"isWritablePrimary"`
			}
			err = client.Database("admin").RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello)
			if err == nil && hello.Primary {
				break
			}
			if ctx.Err() != nil {
				stopMongod(process)
				return nil, "", fmt.Errorf("replica set did not elect primary: %w", err)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	return process, uri, nil
}

func crashMongod(process *mongodProcess) {
	if process == nil || process.command == nil || process.command.Process == nil {
		return
	}
	_ = process.command.Process.Kill()
	_ = process.command.Wait()
}

func restartMongod(process *mongodProcess) error {
	process.command = exec.Command(process.executable, process.arguments...)
	if err := process.command.Start(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := driver.Connect(options.Client().ApplyURI(process.uri).SetServerSelectionTimeout(500 * time.Millisecond))
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	for {
		var hello struct {
			Primary bool `bson:"isWritablePrimary"`
		}
		err = client.Database("admin").RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello)
		if err == nil && hello.Primary {
			return nil
		}
		if ctx.Err() != nil {
			return fmt.Errorf("mongod did not recover as primary: %w", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func stopMongod(process *mongodProcess) {
	if process == nil {
		return
	}
	_ = process.command.Process.Signal(os.Interrupt)
	done := make(chan struct{})
	go func() {
		_ = process.command.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = process.command.Process.Kill()
		<-done
	}
	_ = os.RemoveAll(process.dir)
}
