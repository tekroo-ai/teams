//go:build mongo_integration

package mongo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/agentruntime"
)

func TestRunControlLeaseCancellationAndFencing(t *testing.T) {
	store := openTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const id = "native-run-1"
	const digest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	now := time.Now().UTC().Truncate(time.Millisecond)
	first, err := store.Claim(ctx, id, digest, "daemon-a", now, time.Second)
	if err != nil || !first.Acquired || first.Epoch != 1 {
		t.Fatalf("first claim = %+v, %v", first, err)
	}
	duplicate, err := store.Claim(ctx, id, digest, "daemon-b", now.Add(100*time.Millisecond), time.Second)
	if err != nil || duplicate.Acquired {
		t.Fatalf("duplicate claim = %+v, %v", duplicate, err)
	}
	if err := store.RequestCancel(ctx, id, digest); err != nil {
		t.Fatal(err)
	}
	renewal, err := store.Renew(ctx, id, digest, "daemon-a", first.Epoch, now.Add(200*time.Millisecond), time.Second)
	if err != nil || !renewal.Held || !renewal.CancelRequested {
		t.Fatalf("renewal = %+v, %v", renewal, err)
	}
	successor, err := store.Claim(ctx, id, digest, "daemon-b", now.Add(2*time.Second), time.Second)
	if err != nil || !successor.Acquired || !successor.CancelRequested || successor.Epoch != 2 {
		t.Fatalf("successor claim = %+v, %v", successor, err)
	}
	stale, err := store.Renew(ctx, id, digest, "daemon-a", first.Epoch, now.Add(2*time.Second), time.Second)
	if err != nil || stale.Held {
		t.Fatalf("stale renewal = %+v, %v", stale, err)
	}
	if err := store.Release(ctx, id, digest, "daemon-a", first.Epoch, now.Add(2*time.Second)); !errors.Is(err, agentruntime.ErrConflict) {
		t.Fatalf("stale release = %v", err)
	}
	if err := store.Release(ctx, id, digest, "daemon-b", successor.Epoch, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(ctx, id, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "daemon-c", now.Add(3*time.Second), time.Second); !errors.Is(err, agentruntime.ErrConflict) {
		t.Fatalf("digest drift = %v", err)
	}
}
