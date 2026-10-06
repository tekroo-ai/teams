//go:build mongo_integration

package mongo

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/kernel"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestSleepFencesOldJournalAndNewEffectsButRetainsAppliedReceipts(t *testing.T) {
	store := openTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	const id = "00000000-0000-7000-8000-000000000983"
	const digest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	lease, err := store.Claim(ctx, id, digest, "old", time.Now().UTC(), time.Minute)
	if err != nil || !lease.Acquired {
		t.Fatalf("claim=%+v %v", lease, err)
	}
	old := agentruntime.WithRunOwnership(ctx, agentruntime.RunOwnership{InvocationID: id, RequestDigest: digest, Owner: "old", Epoch: lease.Epoch})
	if _, err := store.Append(old, id, 0, agentruntime.Started, json.RawMessage(`"prompt"`)); err != nil {
		t.Fatal(err)
	}
	intent := agenttools.EffectRecord{InvocationID: kernel.UUIDv7(id), RequestDigest: kernel.Digest(digest), ToolCallID: "write-before-sleep", Name: "write_file", ArgumentsSHA256: digest, WorkspaceRoot: "/tmp/isolated-workspace", EffectPolicyHash: kernel.Digest(digest)}
	if _, created, err := store.Reserve(old, intent); err != nil || !created {
		t.Fatalf("reserve=%t %v", created, err)
	}
	// Simulate the elapsed sleep, not a timer race or a real host suspension.
	if _, err := store.db.Collection("agent_run_control").UpdateOne(ctx, bson.D{{Key: "_id", Value: id}}, bson.D{{Key: "$set", Value: bson.D{{Key: "lease_until", Value: time.Now().Add(-time.Second)}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(old, id, 1, agentruntime.ModelTurn, json.RawMessage(`{}`)); !errors.Is(err, agentruntime.ErrLeaseLost) {
		t.Fatalf("expired owner append=%v", err)
	}
	newLease, err := store.Claim(ctx, id, digest, "replacement", time.Now().UTC(), time.Minute)
	if err != nil || !newLease.Acquired || newLease.Epoch != lease.Epoch+1 {
		t.Fatalf("replacement=%+v %v", newLease, err)
	}
	for _, kind := range []agentruntime.Kind{agentruntime.ModelTurn, agentruntime.ToolDone, agentruntime.Finished, agentruntime.Failed, agentruntime.Cancelled, agentruntime.TimedOut} {
		if _, err := store.Append(old, id, 1, kind, json.RawMessage(`{}`)); !errors.Is(err, agentruntime.ErrLeaseLost) {
			t.Fatalf("stale %s=%v", kind, err)
		}
	}
	late := intent
	late.ToolCallID = "late-write"
	if _, _, err := store.Reserve(old, late); !errors.Is(err, agentruntime.ErrLeaseLost) {
		t.Fatalf("stale effect=%v", err)
	}
	if _, found, err := store.Lookup(ctx, late); err != nil || found {
		t.Fatalf("stale effect was reserved: %t %v", found, err)
	}
	// An admitted physical operation can finish after expiry. Preserve its
	// immutable receipt; the replacement must reconcile it, never repeat it.
	result := json.RawMessage(`{"name":"write_file","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)
	if err := store.Complete(old, intent, result); err != nil {
		t.Fatalf("late receipt=%v", err)
	}
	current := agentruntime.WithRunOwnership(ctx, agentruntime.RunOwnership{InvocationID: id, RequestDigest: digest, Owner: "replacement", Epoch: newLease.Epoch})
	if receipt, created, err := store.Reserve(current, intent); err != nil || created || string(receipt.Result) != string(result) {
		t.Fatalf("receipt replay=%+v created=%t err=%v", receipt, created, err)
	}
	if _, err := store.Append(current, id, 1, agentruntime.ToolDone, json.RawMessage(`{}`)); err != nil {
		t.Fatalf("current append=%v", err)
	}
	if err := store.RequestCancel(ctx, id, digest); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Reserve(current, late); !errors.Is(err, agentruntime.ErrLeaseLost) {
		t.Fatalf("effect admitted after cancellation=%v", err)
	}
	if _, err := store.Append(current, id, 2, agentruntime.Cancelled, json.RawMessage(`"operator cancel"`)); err != nil {
		t.Fatalf("current cancellation=%v", err)
	}
	entries, err := store.Load(ctx, id)
	if err != nil || len(entries) != 3 {
		t.Fatalf("journal=%+v %v", entries, err)
	}
}
