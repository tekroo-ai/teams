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
	"strings"
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

const testManifestSHA = kernel.Digest("2752b876d5a71bb1367a088b9f8cc0ad5df6343b0833906c49aae5a404b8db98")

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
	manifest, err := os.ReadFile("../../CONTRACTS/tekroo.kernel.contracts/0.10.0/manifest.json")
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

func TestEscalationProjectionSurvivesRestartAndRejectsSemanticDuplicate(t *testing.T) {
	database := nextDatabase(t)
	config := testConfig(testMongoURI, database)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	store, err := Open(ctx, config)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	opening := mongoEscalationOpeningDecision(t, 91, testUUID(9101))
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	if err := store.Commit(ctx, kernel.Snapshot{}, opening); err != nil {
		t.Fatal(err)
	}
	cancel()
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
	snapshot, err := reopened.LoadDecision(ctx, kernel.KernelCommand{Target: opening.Receipt.Target})
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	projected, found := snapshot.Escalations[opening.Receipt.Target]
	if !found || projected.State != kernel.EscalationOpen || projected.OpeningEventID != opening.Events[0].EventID || snapshot.EscalationKeys[projected.Key()] != opening.Receipt.Target {
		t.Fatalf("opening projection after restart = %#v, keys=%#v", projected, snapshot.EscalationKeys)
	}

	duplicate := mongoEscalationOpeningDecision(t, 92, testUUID(9102))
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	err = reopened.Commit(ctx, kernel.Snapshot{}, duplicate)
	cancel()
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("semantic duplicate error = %v, want ErrConflict", err)
	}

	resolution := mongoEscalationResolutionDecision(t, 93, projected)
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	if err := reopened.Commit(ctx, snapshot, resolution); err != nil {
		t.Fatal(err)
	}
	cancel()
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	final, err := reopened.LoadDecision(ctx, kernel.KernelCommand{Target: opening.Receipt.Target})
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	terminal := final.Escalations[opening.Receipt.Target]
	if terminal.State != kernel.EscalationTerminal || terminal.TerminalOutcome != kernel.EscalationResolved || terminal.Revision != 2 || terminal.ResolutionEventID != resolution.Events[0].EventID {
		t.Fatalf("terminal projection = %#v", terminal)
	}
}

func TestReleaseProjectionSurvivesRestartAndReplaysExactTerminalState(t *testing.T) {
	database := nextDatabase(t)
	config := testConfig(testMongoURI, database)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	store, err := Open(ctx, config)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	createPayload := currentMongoContractPayload(mongoReleaseCreatePayload("00000000-0000-7000-8000-000000000751"))
	target := kernel.AggregateRef{Kind: kernel.AggregateReleasePlan, ID: testUUID(0x751)}
	create := mongoReleaseDecision(t, 101, target, "tekroo.command.release-plan.create", "tekroo.event.release-plan.created", 1, createPayload)
	key, err := kernel.ReleasePlanKeyFromCreatePayload(createPayload)
	if err != nil {
		t.Fatal(err)
	}
	create.Guards.AbsentReleaseKeys = []kernel.ReleasePlanKey{key}
	commitMongoRelease(t, store, kernel.Snapshot{}, create)

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

	snapshot := loadMongoRelease(t, reopened, target)
	if projected := snapshot.ReleasePlans[target]; projected.State != kernel.ReleasePlanned || projected.Revision != 1 || snapshot.ReleasePlanKeys[key] != target {
		t.Fatalf("created release projection after restart = %#v, keys=%#v", projected, snapshot.ReleasePlanKeys)
	}
	qualificationPayload := json.RawMessage(`{"artifact_digests":["ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"],"contract_manifest":"tekroo.kernel.contracts/0.6.0","dependency_lock_digest":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee","evidence_ids":["00000000-0000-7000-8000-000000000750"],"expected_release_revision":1,"gate_definition_digest":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","manifest_sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","ordered_head_commits":["2222222222222222222222222222222222222222"],"plan_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","qualification_id":"00000000-0000-7000-8000-000000000754","qualified_base_commit":"1111111111111111111111111111111111111111","qualified_tree_digest":"3333333333333333333333333333333333333333","release_plan_id":"00000000-0000-7000-8000-000000000751","required_profiles":["contract-structure","core-hermetic","mongo-integration","synthesized-merge"],"toolchain_digest":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}`)
	qualificationPayload = currentMongoContractPayload(qualificationPayload)
	qualification := mongoReleaseDecision(t, 102, target, "tekroo.command.release-plan.record-qualification", "tekroo.event.release-plan.qualification-recorded", 2, qualificationPayload)
	commitMongoRelease(t, reopened, snapshot, qualification)

	qualified := loadMongoRelease(t, reopened, target)
	requestPayload := json.RawMessage(`{"attempt_id":"00000000-0000-7000-8000-000000000755","evidence_ids":["00000000-0000-7000-8000-000000000750"],"expected_release_revision":2,"merge_id":"00000000-0000-7000-8000-000000000752","plan_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","provider_idempotency_key":"release-751-merge-752-round-1","release_plan_id":"00000000-0000-7000-8000-000000000751","round":1}`)
	request := mongoReleaseDecision(t, 103, target, "tekroo.command.release-plan.request-execution", "tekroo.event.release-plan.execution-requested", 3, requestPayload)
	commitMongoRelease(t, reopened, qualified, request)

	executing := loadMongoRelease(t, reopened, target)
	unknownPayload := json.RawMessage(`{"attempt_id":"00000000-0000-7000-8000-000000000755","evidence_ids":["00000000-0000-7000-8000-000000000756"],"expected_release_revision":3,"merge_id":"00000000-0000-7000-8000-000000000752","observed_at":"2026-08-11T12:00:00Z","outcome":"UNKNOWN","plan_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","reasons":["provider response was not terminal"],"release_plan_id":"00000000-0000-7000-8000-000000000751"}`)
	unknown := mongoReleaseDecision(t, 104, target, "tekroo.command.release-plan.record-result", "tekroo.event.release-plan.result-recorded", 4, unknownPayload)
	commitMongoRelease(t, reopened, executing, unknown)

	reconciling := loadMongoRelease(t, reopened, target)
	priorResultID := reconciling.ReleasePlans[target].Results[testUUID(0x752)].ResultEventID
	reconciliationPayload := json.RawMessage(fmt.Sprintf(`{"attempt_id":"00000000-0000-7000-8000-000000000755","evidence_ids":["00000000-0000-7000-8000-000000000756"],"expected_release_revision":4,"merge_id":"00000000-0000-7000-8000-000000000752","observed_at":"2026-08-11T12:01:00Z","observed_base_commit":"1111111111111111111111111111111111111111","observed_head_commit":"2222222222222222222222222222222222222222","observed_tree_digest":"3333333333333333333333333333333333333333","outcome":"MERGED","plan_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","provider_state":"MERGED","reasons":["provider reports exact planned head merged"],"reconciliation_id":"00000000-0000-7000-8000-000000000759","release_plan_id":"00000000-0000-7000-8000-000000000751","supersedes_result_event_id":"%s"}`, priorResultID))
	reconciliation := mongoReleaseDecision(t, 105, target, "tekroo.command.release-plan.record-reconciliation", "tekroo.event.release-plan.reconciliation-recorded", 5, reconciliationPayload)
	commitMongoRelease(t, reopened, reconciling, reconciliation)

	merged := loadMongoRelease(t, reopened, target)
	plan := merged.ReleasePlans[target]
	result := plan.Results[testUUID(0x752)]
	finalPayload := json.RawMessage(fmt.Sprintf(`{"evidence_ids":["00000000-0000-7000-8000-000000000750"],"expected_release_revision":5,"plan_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","provider_tree_digest":"3333333333333333333333333333333333333333","qualification_event_id":"%s","qualified_tree_digest":"3333333333333333333333333333333333333333","reasons":["all planned merges and the provider tree are verified"],"release_mode":"CODE","release_plan_id":"00000000-0000-7000-8000-000000000751","result_event_ids":["%s"],"terminal_status":"READY_FOR_ACCEPTANCE"}`, plan.Qualification.EventID, result.ReconciliationEventID))
	invalidPayload := json.RawMessage(strings.ReplaceAll(string(finalPayload), string(result.ReconciliationEventID), "00000000-0000-7000-8000-000000000799"))
	invalid := mongoReleaseDecision(t, 106, target, "tekroo.command.release-plan.finalize", "tekroo.event.release-plan.finalized", 6, invalidPayload)
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	err = reopened.Commit(ctx, merged, invalid)
	cancel()
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("invalid result-vector finalization error = %v, want ErrConflict", err)
	}
	finalization := mongoReleaseDecision(t, 107, target, "tekroo.command.release-plan.finalize", "tekroo.event.release-plan.finalized", 6, finalPayload)
	commitMongoRelease(t, reopened, merged, finalization)

	final := loadMongoRelease(t, reopened, target)
	projected := final.ReleasePlans[target]
	if projected.State != kernel.ReleaseReadyForAcceptance || projected.Revision != 6 || projected.ProviderTreeDigest != projected.ExpectedQualifiedTree || projected.NextMergeIndex != 1 || final.ReleasePlanKeys[key] != target {
		t.Fatalf("terminal release projection = %#v, keys=%#v", projected, final.ReleasePlanKeys)
	}
	acceptanceSnapshot := loadMongoRelease(t, reopened, key.Story)
	if acceptanceSnapshot.ReleasePlans[target].FinalizationEventID != projected.FinalizationEventID || acceptanceSnapshot.ReleasePlanKeys[key] != target {
		t.Fatalf("story acceptance lookup did not load the finalized plan: plans=%#v keys=%#v", acceptanceSnapshot.ReleasePlans, acceptanceSnapshot.ReleasePlanKeys)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	eventCount, err := reopened.db.Collection("events").CountDocuments(ctx, bson.D{{Key: "aggregate_key", Value: aggregateKey(target)}})
	cancel()
	if err != nil || eventCount != 6 {
		t.Fatalf("release event count = %d, err=%v, want 6", eventCount, err)
	}
	stale := mongoReleaseDecision(t, 108, target, "tekroo.command.release-plan.finalize", "tekroo.event.release-plan.finalized", 6, finalPayload)
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	err = reopened.Commit(ctx, merged, stale)
	cancel()
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale release transition error = %v, want ErrConflict", err)
	}
}

func TestConcurrentReleaseCreationHasOneSemanticWinner(t *testing.T) {
	store := openTestStore(t)
	firstTarget := kernel.AggregateRef{Kind: kernel.AggregateReleasePlan, ID: testUUID(0x771)}
	secondTarget := kernel.AggregateRef{Kind: kernel.AggregateReleasePlan, ID: testUUID(0x772)}
	firstPayload := currentMongoContractPayload(mongoReleaseCreatePayload(string(firstTarget.ID)))
	secondPayload := currentMongoContractPayload(mongoReleaseCreatePayload(string(secondTarget.ID)))
	key, err := kernel.ReleasePlanKeyFromCreatePayload(firstPayload)
	if err != nil {
		t.Fatal(err)
	}
	first := mongoReleaseDecision(t, 111, firstTarget, "tekroo.command.release-plan.create", "tekroo.event.release-plan.created", 1, firstPayload)
	second := mongoReleaseDecision(t, 112, secondTarget, "tekroo.command.release-plan.create", "tekroo.event.release-plan.created", 1, secondPayload)
	first.Guards.AbsentReleaseKeys = []kernel.ReleasePlanKey{key}
	second.Guards.AbsentReleaseKeys = []kernel.ReleasePlanKey{key}
	errorsByAttempt := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	start := make(chan struct{})
	for _, decision := range []kernel.Decision{first, second} {
		go func(decision kernel.Decision) {
			ready.Done()
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			errorsByAttempt <- store.Commit(ctx, kernel.Snapshot{}, decision)
		}(decision)
	}
	ready.Wait()
	close(start)
	firstErr := <-errorsByAttempt
	secondErr := <-errorsByAttempt
	successes := 0
	conflicts := 0
	for _, err := range []error{firstErr, secondErr} {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatalf("concurrent release creation error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent release outcomes: successes=%d conflicts=%d", successes, conflicts)
	}
}

func TestReleaseProjectionLoadFailsClosedOnCorruptSemanticBinding(t *testing.T) {
	store := openTestStore(t)
	target := kernel.AggregateRef{Kind: kernel.AggregateReleasePlan, ID: testUUID(0x781)}
	payload := currentMongoContractPayload(mongoReleaseCreatePayload(string(target.ID)))
	decision := mongoReleaseDecision(t, 121, target, "tekroo.command.release-plan.create", "tekroo.event.release-plan.created", 1, payload)
	key, err := kernel.ReleasePlanKeyFromCreatePayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	decision.Guards.AbsentReleaseKeys = []kernel.ReleasePlanKey{key}
	commitMongoRelease(t, store, kernel.Snapshot{}, decision)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	_, err = store.db.Collection("release_plan_keys").UpdateOne(ctx, bson.D{{Key: "_id", Value: releasePlanKey(key)}}, bson.D{{Key: "$set", Value: bson.D{{Key: "data", Value: []byte(`{}`)}}}})
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	_, err = store.LoadDecision(ctx, kernel.KernelCommand{Target: target})
	cancel()
	if !errors.Is(err, ErrCorruptAggregate) {
		t.Fatalf("corrupt release binding load error = %v, want ErrCorruptAggregate", err)
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

func TestModelCapabilityProjectionsRoundTrip(t *testing.T) {
	store := openTestStore(t)
	task := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: testUUID(801)}
	profile := mongoTestWorkProfile(task.ID)
	profilePayload, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	bind := mongoReleaseDecision(t, 81, task, "tekroo.command.task.bind-work-profile", "tekroo.event.task.work-profile-bound", 1, profilePayload)
	commitMongoRelease(t, store, kernel.Snapshot{}, bind)

	snapshot := loadMongoRelease(t, store, task)
	projectedProfile, found := snapshot.WorkProfiles[task]
	if !found || projectedProfile.Profile.ProfileDigest != profile.ProfileDigest || projectedProfile.BoundEventID != bind.Events[0].EventID {
		t.Fatalf("work profile projection = %#v", projectedProfile)
	}

	authorization := mongoTestAssignmentAuthorization(task.ID, profile.Binding())
	authorizationPayload, err := json.Marshal(authorization)
	if err != nil {
		t.Fatal(err)
	}
	authorize := mongoReleaseDecision(t, 82, task, "tekroo.command.task.authorize-qualified-assignment", "tekroo.event.task.qualified-assignment-authorized", 2, authorizationPayload)
	commitMongoRelease(t, store, snapshot, authorize)

	snapshot = loadMongoRelease(t, store, task)
	projectedAuthorization, found := snapshot.QualifiedAssignments[task]
	if !found || projectedAuthorization.AssignmentID != authorization.AssignmentID || projectedAuthorization.AuthorizationEventID != authorize.Events[0].EventID {
		t.Fatalf("qualified assignment projection = %#v", projectedAuthorization)
	}

	variantTarget := kernel.AggregateRef{Kind: kernel.AggregateVariantGroup, ID: testUUID(807)}
	variantPayload := mongoTestVariantOpenPayload(variantTarget.ID, task.ID)
	group, err := kernel.VariantGroupFromOpenPayload(variantPayload, testUUID(808))
	if err != nil {
		t.Fatal(err)
	}
	open := mongoReleaseDecision(t, 83, variantTarget, "tekroo.command.variant-group.open", "tekroo.event.variant-group.opened", 1, variantPayload)
	open.Guards.AbsentVariantKeys = []kernel.VariantGroupKey{group.Key()}
	commitMongoRelease(t, store, kernel.Snapshot{}, open)

	variantSnapshot := loadMongoRelease(t, store, variantTarget)
	if projected, found := variantSnapshot.VariantGroups[variantTarget]; !found || projected.VariantGroupID != variantTarget.ID {
		t.Fatalf("variant group projection = %#v", projected)
	}
	if reference, found := variantSnapshot.VariantGroupKeys[group.Key()]; !found || reference != variantTarget {
		t.Fatalf("variant key projection = %#v, %t", reference, found)
	}
}

func TestOperationalProjectionsApplyIdempotentlyAndRebuildExactly(t *testing.T) {
	store := openTestStore(t)
	story := kernel.AggregateRef{Kind: kernel.AggregateStory, ID: testUUID(8701)}
	task := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: testUUID(8702)}
	budget := kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: testUUID(8703)}
	storyState := kernel.AggregateState{Kind: kernel.AggregateStory, ID: story.ID, Revision: 1, LifecycleEpoch: 1, ScopeRevision: 1, Phase: kernel.PhaseDraft, Condition: kernel.ConditionRunnable}
	taskState := kernel.AggregateState{Kind: kernel.AggregateTask, ID: task.ID, Revision: 3, LifecycleEpoch: 1, ScopeRevision: 1, Phase: kernel.PhasePlanned, Condition: kernel.ConditionRunnable}
	insertProjectionAggregate(t, store, storyState)
	insertProjectionAggregate(t, store, taskState)

	storyCreated := projectionMongoEvent(story, 1, testUUID(8711), "tekroo.event.story.created", json.RawMessage(`{"acceptance_criteria":["all projections agree"],"description":"Operational read model.","title":"Projection story"}`))
	taskCreated := projectionMongoEvent(task, 1, testUUID(8712), "tekroo.event.task.created", json.RawMessage(fmt.Sprintf(`{"acceptance_criteria":["view is rebuildable"],"depends_on":[],"description":"Project one task.","story_id":"%s","title":"Projection task"}`, story.ID)))
	profile := mongoTestWorkProfile(task.ID)
	profilePayload, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	profileBound := projectionMongoEvent(task, 2, testUUID(8713), "tekroo.event.task.work-profile-bound", profilePayload)
	purposeLimits := make(kernel.PurposeCounters, len(kernel.AllWorkPurposes))
	purposeUsed := make(kernel.PurposeCounters, len(kernel.AllWorkPurposes))
	for _, purpose := range kernel.AllWorkPurposes {
		purposeLimits[purpose] = 1
		purposeUsed[purpose] = 0
	}
	budgetBoundPayload, err := json.Marshal(map[string]any{"budget_account_id": budget.ID, "expected_task_revision": 2, "lifecycle_epoch": 1, "purpose_limits": purposeLimits, "scope_revision": 1, "task_id": task.ID, "task_model_invocation_limit": 3})
	if err != nil {
		t.Fatal(err)
	}
	budgetBound := projectionMongoEvent(task, 3, testUUID(8714), "tekroo.event.task.work-budget-bound", budgetBoundPayload)
	for _, event := range []kernel.DomainEvent{storyCreated, taskCreated, profileBound, budgetBound} {
		insertProjectionEvent(t, store, event)
	}
	profileSnapshot := kernel.WorkProfileSnapshot{Profile: profile, TaskRevision: 2, BoundEventID: profileBound.EventID}
	insertProjectionValue(t, store, "work_profiles", aggregateKey(task), struct {
		Task    kernel.AggregateRef        `json:"task"`
		Profile kernel.WorkProfileSnapshot `json:"profile"`
	}{task, profileSnapshot})
	account := kernel.WorkBudgetAccount{ID: budget.ID, Revision: 1, RootWork: story, LifecycleEpoch: 1, PolicyRevision: 1, PolicyDigest: kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), ModelInvocationLimit: 3, PurposeLimits: purposeLimits, PurposeUsed: purposeUsed, DeadlineAt: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), LastEventID: testUUID(8715)}
	insertProjectionValue(t, store, "work_budget_accounts", aggregateKey(budget), workBudgetValue{Reference: budget, Account: account})
	binding := kernel.TaskWorkBudgetBinding{TaskID: task.ID, BudgetAccountID: budget.ID, TaskRevision: 3, LifecycleEpoch: 1, ScopeRevision: 1, ModelInvocationLimit: 3, PurposeLimits: purposeLimits, PurposeUsed: purposeUsed, BoundEventID: budgetBound.EventID}
	insertProjectionValue(t, store, "task_work_budgets", aggregateKey(task), taskBudgetValue{Task: task, Binding: binding})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	applied, err := store.ProjectPendingOperationalEvents(ctx, time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC))
	if err != nil || applied != 4 {
		t.Fatalf("project pending = %d, %v", applied, err)
	}
	taskProjection, found, err := store.ReadTaskProjection(ctx, task.ID)
	if err != nil || !found || taskProjection.StoryID != story.ID || taskProjection.Budget.AccountID != budget.ID || taskProjection.ProjectionRevision != 3 {
		t.Fatalf("task projection = %#v, found=%t, err=%v", taskProjection, found, err)
	}
	storyProjection, found, err := store.ReadStoryProjection(ctx, story.ID)
	if err != nil || !found || len(storyProjection.TaskIDs) != 1 || storyProjection.TaskIDs[0] != task.ID || storyProjection.ProjectionRevision != 4 {
		t.Fatalf("story projection = %#v, found=%t, err=%v", storyProjection, found, err)
	}
	duplicate, err := store.ApplyOperationalProjectionEvent(ctx, budgetBound.EventID, time.Date(2026, 8, 31, 12, 1, 0, 0, time.UTC))
	if err != nil || !duplicate.Duplicate || duplicate.Applied {
		t.Fatalf("duplicate application = %#v, %v", duplicate, err)
	}
	olderDuplicate, err := store.ApplyOperationalProjectionEvent(ctx, taskCreated.EventID, time.Date(2026, 8, 31, 12, 1, 30, 0, time.UTC))
	if err != nil || !olderDuplicate.Duplicate || olderDuplicate.Applied {
		t.Fatalf("older duplicate application = %#v, %v", olderDuplicate, err)
	}
	rebuild, err := store.RebuildOperationalProjections(ctx, time.Date(2026, 8, 31, 12, 2, 0, 0, time.UTC))
	if err != nil || !rebuild.ExactMatch || !rebuild.ReplacementApplied || rebuild.StoryCount != 1 || rebuild.TaskCount != 1 {
		t.Fatalf("rebuild = %#v, %v", rebuild, err)
	}
	database := store.db.Name()
	restartCtx, restartCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := store.Close(restartCtx); err != nil {
		restartCancel()
		t.Fatal(err)
	}
	restartCancel()
	restartCtx, restartCancel = context.WithTimeout(context.Background(), 15*time.Second)
	reopened, err := Open(restartCtx, testConfig(testMongoURI, database))
	restartCancel()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_ = reopened.db.Drop(cleanupCtx)
		_ = reopened.Close(cleanupCtx)
	})
	store = reopened
	postRestart, err := store.ProjectPendingOperationalEvents(ctx, time.Date(2026, 8, 31, 12, 2, 30, 0, time.UTC))
	if err != nil || postRestart != 0 {
		t.Fatalf("post-restart projection count = %d, %v", postRestart, err)
	}
	taskProjection, found, err = store.ReadTaskProjection(ctx, task.ID)
	if err != nil || !found || taskProjection.ProjectionRevision != 3 {
		t.Fatalf("projection after restart = %#v, found=%t, err=%v", taskProjection, found, err)
	}

	taskProjection.Phase = "CORRUPTED"
	corrupt, err := projectionDocument(taskProjection)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Collection("task_projections").ReplaceOne(ctx, bson.D{{Key: "_id", Value: corrupt.ID}}, corrupt); err != nil {
		t.Fatal(err)
	}
	authoritative, err := store.loadSnapshot(ctx, task, nil)
	if err != nil || authoritative.State == nil || authoritative.State.Phase != kernel.PhasePlanned {
		t.Fatalf("corrupt read projection affected authority = %#v, %v", authoritative.State, err)
	}
	mismatch, err := store.RebuildOperationalProjections(ctx, time.Date(2026, 8, 31, 12, 3, 0, 0, time.UTC))
	if !errors.Is(err, ErrProjectionRebuildMismatch) || mismatch.ExactMatch || mismatch.ReplacementApplied {
		t.Fatalf("mismatch rebuild = %#v, %v", mismatch, err)
	}
	retained, found, err := store.ReadTaskProjection(ctx, task.ID)
	if err != nil || !found || retained.Phase != "CORRUPTED" {
		t.Fatalf("mismatch replaced live projection = %#v, found=%t, err=%v", retained, found, err)
	}
}

func TestOperationalProjectionGapFailsClosedAndRetainsFault(t *testing.T) {
	store := openTestStore(t)
	task := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: testUUID(8751)}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := store.db.Collection("aggregates").InsertOne(ctx, aggregateDocument{ID: aggregateKey(task), Revision: 2}); err != nil {
		t.Fatal(err)
	}
	event := projectionMongoEvent(task, 2, testUUID(8752), "tekroo.event.task.work-budget-bound", json.RawMessage(`{}`))
	insertProjectionEvent(t, store, event)
	result, err := store.ApplyOperationalProjectionEvent(ctx, event.EventID, time.Date(2026, 8, 31, 13, 0, 0, 0, time.UTC))
	if !errors.Is(err, ErrProjectionGap) || result.Fault == nil || result.Fault.FaultKind != "REVISION_GAP" || result.Applied {
		t.Fatalf("gap result = %#v, %v", result, err)
	}
	faults, err := store.db.Collection("projection_faults").CountDocuments(ctx, bson.D{})
	if err != nil || faults != 1 {
		t.Fatalf("fault count = %d, %v", faults, err)
	}
	faultRecords, err := store.ReadProjectionFaults(ctx)
	if err != nil || len(faultRecords) != 1 || faultRecords[0].Aggregate != task || faultRecords[0].FaultKind != "REVISION_GAP" {
		t.Fatalf("fault records = %#v, %v", faultRecords, err)
	}
	checkpoints, err := store.db.Collection("projection_checkpoints").CountDocuments(ctx, bson.D{})
	if err != nil || checkpoints != 0 {
		t.Fatalf("checkpoint count after gap = %d, %v", checkpoints, err)
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

func TestKindFilteredIntentFeedExcludesUnrelatedBacklog(t *testing.T) {
	store := openTestStore(t)
	invocationIntent := kernel.OutboxIntent{IntentID: testUUID(8891), EventID: testUUID(8892), Kind: "WORK_INVOCATION_AUTHORIZED"}
	unrelatedIntent := kernel.OutboxIntent{IntentID: testUUID(8893), EventID: testUUID(8894), Kind: "TASK_UPDATED"}
	for _, intent := range []kernel.OutboxIntent{unrelatedIntent, invocationIntent} {
		data, err := encode(intent)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, err = store.db.Collection("outbox").InsertOne(ctx, outboxDocument{ID: string(intent.IntentID), EventID: string(intent.EventID), Kind: intent.Kind, State: DeliveryPending, Data: data})
		cancel()
		if err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	feed, err := store.OpenIntentFeedForKind(ctx, "operational-execution-test", "WORK_INVOCATION_AUTHORIZED")
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		_ = feed.Close(closeCtx)
	}()
	readCtx, readCancel := context.WithTimeout(context.Background(), 10*time.Second)
	observed, err := feed.Next(readCtx)
	readCancel()
	if err != nil || observed.Intent != invocationIntent || feed.Kind() != invocationIntent.Kind {
		t.Fatalf("observed=%#v kind=%q err=%v", observed, feed.Kind(), err)
	}
}

func TestIntentFeedPollNormalizesExpiredMongoDeadline(t *testing.T) {
	store := openTestStore(t)
	openCtx, openCancel := context.WithTimeout(context.Background(), 10*time.Second)
	feed, err := store.OpenIntentFeedForKind(openCtx, "expired-poll-test", "WORK_INVOCATION_AUTHORIZED")
	openCancel()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		_ = feed.Close(closeCtx)
	}()
	pollCtx, pollCancel := context.WithTimeout(context.Background(), time.Millisecond)
	_, err = feed.Poll(pollCtx)
	pollCancel()
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, ErrIntentNotFound) {
		t.Fatalf("expired idle poll = %v", err)
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

func TestDeploymentIdentityBindsFreshDatabaseAndRejectsReplacement(t *testing.T) {
	store := openTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	identity := kernel.Digest(strings.Repeat("1", 64))
	if err := store.BindDeploymentIdentity(ctx, identity); err != nil {
		t.Fatal(err)
	}
	if err := store.BindDeploymentIdentity(ctx, identity); err != nil {
		t.Fatalf("repeat binding = %v", err)
	}
	if err := store.BindDeploymentIdentity(ctx, kernel.Digest(strings.Repeat("2", 64))); !errors.Is(err, ErrDeploymentIdentityMismatch) {
		t.Fatalf("replacement binding = %v", err)
	}
}

func TestOpenBindsFreshDeploymentAndAllowsExactRestart(t *testing.T) {
	database := nextDatabase(t)
	identity := kernel.Digest(strings.Repeat("4", 64))
	config := testConfig(testMongoURI, database)
	config.DeploymentIdentity = identity
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	first, err := Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(ctx); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, config)
	if err != nil {
		t.Fatalf("exact restart = %v", err)
	}
	defer func() {
		_ = second.db.Drop(ctx)
		_ = second.Close(ctx)
	}()
	config.DeploymentIdentity = kernel.Digest(strings.Repeat("5", 64))
	if replacement, err := Open(ctx, config); !errors.Is(err, ErrDeploymentIdentityMismatch) {
		if replacement != nil {
			_ = replacement.Close(ctx)
		}
		t.Fatalf("replacement deployment open = %v", err)
	}
}

func TestDeploymentIdentityRejectsPopulatedUnboundDatabase(t *testing.T) {
	store := openTestStore(t)
	commitDecision(t, store, completeDecision(t, 1))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	config := testConfig(testMongoURI, store.db.Name())
	config.DeploymentIdentity = kernel.Digest(strings.Repeat("3", 64))
	if reopened, err := Open(ctx, config); !errors.Is(err, ErrDeploymentIdentityMismatch) {
		if reopened != nil {
			_ = reopened.Close(ctx)
		}
		t.Fatalf("populated database open = %v", err)
	}
	if err := store.db.Collection("metadata").FindOne(ctx, bson.D{{Key: "_id", Value: deploymentMetadataID}}).Err(); !errors.Is(err, driver.ErrNoDocuments) {
		t.Fatalf("failed open wrote deployment marker: %v", err)
	}
}

func TestIntentFeedRedeliversYieldedIntentInSameSession(t *testing.T) {
	store := openTestStore(t)
	decision := completeDecision(t, 1)
	commitDecision(t, store, decision)
	intent := decision.Outbox[0]

	openCtx, openCancel := context.WithTimeout(context.Background(), 10*time.Second)
	feed, err := store.OpenIntentFeedForKind(openCtx, "yield-redelivery-test", intent.Kind)
	openCancel()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		_ = feed.Close(closeCtx)
	}()

	readCtx, readCancel := context.WithTimeout(context.Background(), 10*time.Second)
	first, err := feed.Next(readCtx)
	readCancel()
	if err != nil || first.Intent != intent {
		t.Fatalf("first delivery = %#v, err=%v", first, err)
	}
	now := testNow()
	claimCtx, claimCancel := context.WithTimeout(context.Background(), 10*time.Second)
	claim, err := store.AcquireIntent(claimCtx, intent.IntentID, "yielding-worker", now, time.Minute)
	claimCancel()
	if err != nil {
		t.Fatal(err)
	}
	yieldCtx, yieldCancel := context.WithTimeout(context.Background(), 10*time.Second)
	err = store.YieldIntent(yieldCtx, intent.IntentID, claim.Holder, claim.ClaimEpoch, now.Add(time.Second))
	yieldCancel()
	if err != nil {
		t.Fatal(err)
	}

	retryCtx, retryCancel := context.WithTimeout(context.Background(), 10*time.Second)
	retry, err := feed.Next(retryCtx)
	retryCancel()
	if err != nil || retry.Intent != intent {
		t.Fatalf("retry delivery = %#v, err=%v", retry, err)
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
	state := kernel.AggregateState{Kind: kernel.AggregateStory, ID: target.ID, Revision: 1, LifecycleEpoch: 1, ScopeRevision: 1, Phase: kernel.PhaseDraft, Condition: kernel.ConditionRunnable}
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

func projectionMongoEvent(aggregate kernel.AggregateRef, revision uint64, eventID kernel.UUIDv7, eventType string, payload json.RawMessage) kernel.DomainEvent {
	return kernel.DomainEvent{
		ContractManifest: kernel.ContractIdentity, EventID: eventID, EventType: eventType,
		EventVersion: kernel.OperationalSchemaVersion, Aggregate: aggregate, AggregateRevision: revision,
		LifecycleEpoch: 1, CommittedAt: time.Date(2026, time.August, 31, 12, 0, int(revision), 0, time.UTC), Payload: payload,
	}
}

func insertProjectionAggregate(t *testing.T, store *Store, state kernel.AggregateState) {
	t.Helper()
	data, err := encode(state)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := store.db.Collection("aggregates").InsertOne(ctx, aggregateDocument{ID: aggregateKey(kernel.AggregateRef{Kind: state.Kind, ID: state.ID}), Revision: state.Revision, State: data}); err != nil {
		t.Fatal(err)
	}
}

func insertProjectionEvent(t *testing.T, store *Store, event kernel.DomainEvent) {
	t.Helper()
	data, err := encode(event)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	document := eventDocument{ID: string(event.EventID), AggregateKey: aggregateKey(event.Aggregate), Revision: event.AggregateRevision, EventType: event.EventType, Data: data}
	if _, err := store.db.Collection("events").InsertOne(ctx, document); err != nil {
		t.Fatal(err)
	}
}

func insertProjectionValue(t *testing.T, store *Store, collection, id string, value any) {
	t.Helper()
	data, err := encode(value)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := store.db.Collection(collection).InsertOne(ctx, valueDocument{ID: id, Data: data}); err != nil {
		t.Fatal(err)
	}
}

func mongoEscalationOpeningDecision(t *testing.T, ordinal int, escalationID kernel.UUIDv7) kernel.Decision {
	t.Helper()
	payload := json.RawMessage(fmt.Sprintf(`{"adjudicator":{"id":"principal-adjudicator","kind":"HUMAN"},"causal_path_event_ids":["00000000-0000-7000-8000-000000009102"],"deadline_at":"2026-08-12T00:00:00Z","escalation_id":"%s","escalation_policy_revision":1,"evidence_ids":["00000000-0000-7000-8000-000000009103"],"expected_subject_revision":7,"resolution_owner_fqn":"teams::coder-1","resolution_round_limit":1,"route_limit":1,"subject_id":"00000000-0000-7000-8000-000000009104","subject_kind":"task","subject_lifecycle_epoch":1,"timeout_policy":{"id":"escalation-timeout-policy","kind":"POLICY"},"trigger":"HANDOFF_CYCLE_DETECTED","triggering_condition_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","unresolved_question":"Which directed successor resolves the detected handoff cycle?"}`, escalationID))
	escalation, err := kernel.EscalationFromOpenPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	decision := completeDecision(t, ordinal)
	decision.NextState = nil
	decision.Receipt.Target = kernel.AggregateRef{Kind: kernel.AggregateEscalation, ID: escalationID}
	decision.Events[0].Aggregate = decision.Receipt.Target
	decision.Events[0].EventType = "tekroo.event.escalation.opened"
	decision.Events[0].Payload = payload
	decision.Guards.AbsentEscalationKeys = []kernel.EscalationKey{escalation.Key()}
	return attachProvenance(t, decision)
}

func mongoEscalationResolutionDecision(t *testing.T, ordinal int, escalation kernel.EscalationSnapshot) kernel.Decision {
	t.Helper()
	payload := json.RawMessage(fmt.Sprintf(`{"decided_at":"2026-08-11T12:00:00Z","escalation_id":"%s","evidence_ids":["00000000-0000-7000-8000-000000009105"],"expected_escalation_revision":1,"outcome":"RESOLVED","reasons":["A directed successor was selected."],"round":1,"source_role":"ADJUDICATOR","subject_id":"%s","subject_kind":"%s","subject_lifecycle_epoch":%d}`, escalation.EscalationID, escalation.Subject.ID, escalation.Subject.Kind, escalation.SubjectLifecycleEpoch))
	decision := completeDecision(t, ordinal)
	revision := uint64(2)
	decision.NextState = nil
	decision.Receipt.Target = kernel.AggregateRef{Kind: kernel.AggregateEscalation, ID: escalation.EscalationID}
	decision.Receipt.ResultingRevision = &revision
	decision.Events[0].Aggregate = decision.Receipt.Target
	decision.Events[0].AggregateRevision = revision
	decision.Events[0].EventType = "tekroo.event.escalation.resolved"
	decision.Events[0].Payload = payload
	decision.Events[0].CommittedAt = testNow()
	decision.Authority.Principal = escalation.Adjudicator
	return attachProvenance(t, decision)
}

func mongoReleaseCreatePayload(releasePlanID string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"author":{"id":"principal-author","kind":"HUMAN"},"author_approval_event_id":"00000000-0000-7000-8000-000000000749","author_approval_revision":1,"base_commit":"1111111111111111111111111111111111111111","base_ref":"main","conflict_policy":"FAIL_NO_IMPROVISATION","contract_manifest":"tekroo.kernel.contracts/0.6.0","evidence_ids":["00000000-0000-7000-8000-000000000750"],"execution_round_limit":2,"expected_qualified_tree":"3333333333333333333333333333333333333333","expected_story_revision":8,"git_version":"git version 2.51.0","manifest_sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","merge_strategy":"FF_ONLY_ORDERED","ordered_merges":[{"change_ref":"refs/heads/story-1","head_commit":"2222222222222222222222222222222222222222","merge_id":"00000000-0000-7000-8000-000000000752","role":"story"}],"plan_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","release_mode":"CODE","release_plan_id":"%s","release_policy_revision":1,"repository_url":"https://example.invalid/tekroo/teams.git","required_profiles":["contract-structure","core-hermetic","mongo-integration","synthesized-merge"],"story_id":"00000000-0000-7000-8000-000000000101","story_lifecycle_epoch":1}`, releasePlanID))
}

func mongoReleaseDecision(t *testing.T, ordinal int, target kernel.AggregateRef, commandType, eventType string, revision uint64, payload json.RawMessage) kernel.Decision {
	t.Helper()
	decision := completeDecision(t, ordinal)
	decision.NextState = nil
	decision.Receipt.Target = target
	decision.Receipt.CommandType = commandType
	decision.Receipt.ResultingRevision = &revision
	decision.Events[0].Aggregate = target
	decision.Events[0].EventType = eventType
	decision.Events[0].AggregateRevision = revision
	decision.Events[0].CommittedAt = time.Date(2026, time.August, 11, 12, 0, ordinal, 0, time.UTC)
	decision.Events[0].Payload = append(json.RawMessage(nil), payload...)
	return attachProvenance(t, decision)
}

func mongoTestWorkProfile(taskID kernel.UUIDv7) kernel.WorkRiskProfile {
	return kernel.WorkRiskProfile{
		TaskID: taskID, ProfileID: testUUID(802), ProfileRevision: 1,
		ProfileDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", LifecycleEpoch: 1, ScopeRevision: 1,
		WorkKind: kernel.WorkImplementation, Ambiguity: kernel.AmbiguityLow, Novelty: kernel.NoveltyRoutine, BlastRadius: kernel.BlastLocal, SecuritySensitivity: kernel.SecurityOrdinary,
		MinimumDecisionRoute: kernel.RouteBoundedExecution, AcceptanceCriteriaDigest: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		RequiredDeterministicGateIDs: []string{"go-test"}, RequiredValidationBranches: 1,
		RequiredIndependenceDimensions: []kernel.IndependenceDimension{kernel.IndependenceActor, kernel.IndependenceExecution, kernel.IndependenceContext, kernel.IndependenceWorkspace, kernel.IndependenceMethod},
		ImplementationVariantCount:     1, ValidCandidateQuorum: 1, VerificationTopologyDigest: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		ClassificationPolicyRevision: 1, ClassificationPolicyDigest: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		PromotionPolicyRevision: 1, PromotionPolicyDigest: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		Budgets:                 kernel.FiniteWorkBudgets{AttemptLimit: 2, ReviewRoundLimit: 2, PromotionLimit: 1, EscalationLimit: 1, DeadlineAt: time.Date(2026, time.August, 14, 0, 0, 0, 0, time.UTC)},
		ClassificationAuthority: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"}, ClassificationEvidenceIDs: []kernel.UUIDv7{testUUID(806)},
	}
}

func mongoTestAssignmentAuthorization(taskID kernel.UUIDv7, profile kernel.WorkProfileBinding) kernel.QualifiedAssignmentAuthorization {
	return kernel.QualifiedAssignmentAuthorization{
		AssignmentID: testUUID(803), TaskID: taskID, ExpectedTaskRevision: 1, WorkProfile: profile,
		RequiredDecisionRoute: kernel.RouteBoundedExecution, SelectedDecisionRoute: kernel.RouteBoundedExecution, SelectedActorFQN: "teams::coder-1",
		SelectedExecutionID: testUUID(804), SelectedFencingEpoch: 1,
		ModelProfileDigest: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", RuntimeIdentityDigest: "1111111111111111111111111111111111111111111111111111111111111111",
		Qualification:           kernel.AssignmentQualificationReceipt{QualificationID: testUUID(805), QualificationDigest: "2222222222222222222222222222222222222222222222222222222222222222", QualificationCorpusDigest: "3333333333333333333333333333333333333333333333333333333333333333", ModelProfileDigest: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", DecisionRoute: kernel.RouteBoundedExecution, QualifiedRole: "programmer", Status: kernel.QualificationPass, ObservedAt: time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)},
		SelectionPolicyRevision: 1, SelectionPolicyDigest: "4444444444444444444444444444444444444444444444444444444444444444",
		HardConstraintResults: []kernel.HardConstraintResult{{ConstraintID: "data-residency", Outcome: kernel.ConstraintPass, EvidenceIDs: []kernel.UUIDv7{testUUID(806)}}},
		SelectionReasons:      []string{"least-cost qualified profile"}, EvidenceIDs: []kernel.UUIDv7{testUUID(806)}, AuthorizationEventID: testUUID(807),
	}
}

func mongoTestVariantOpenPayload(groupID, taskID kernel.UUIDv7) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"acceptance_manifest_digest":"9999999999999999999999999999999999999999999999999999999999999999","adjudicator":{"id":"principal","kind":"HUMAN"},"base_artifact_digest":"5555555555555555555555555555555555555555555555555555555555555555","candidate_count":2,"comparator":{"id":"teams::reviewer-1","kind":"ACTOR"},"comparison_method_id":"structured-diff-v1","comparison_policy_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","decision_deadline_at":"2026-08-15T00:00:00Z","dependency_lock_digest":"8888888888888888888888888888888888888888888888888888888888888888","evidence_ids":["00000000-0000-7000-8000-000000000806"],"input_evidence_set_digest":"6666666666666666666666666666666666666666666666666666666666666666","materiality_policy_digest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","replacement_budget":1,"required_independence_dimensions":["ACTOR","EXECUTION","CONTEXT","WORKSPACE"],"submission_deadline_at":"2026-08-14T00:00:00Z","task_id":"%s","toolchain_digest":"7777777777777777777777777777777777777777777777777777777777777777","valid_candidate_quorum":2,"variant_group_id":"%s","work_profile":{"lifecycle_epoch":1,"profile_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","profile_id":"00000000-0000-7000-8000-000000000802","profile_revision":1,"scope_revision":1}}`, taskID, groupID))
}

func commitMongoRelease(t *testing.T, store *Store, expected kernel.Snapshot, decision kernel.Decision) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := store.Commit(ctx, expected, decision); err != nil {
		t.Fatal(err)
	}
}

func loadMongoRelease(t *testing.T, store *Store, target kernel.AggregateRef) kernel.Snapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	snapshot, err := store.LoadDecision(ctx, kernel.KernelCommand{Target: target})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
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

func currentMongoContractPayload(payload json.RawMessage) json.RawMessage {
	return json.RawMessage(strings.ReplaceAll(string(payload), "tekroo.kernel.contracts/0.6.0", kernel.ContractIdentity))
}
