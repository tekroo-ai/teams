//go:build mongo_integration

package mongo

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/kernel"
)

func TestToolEffectLedgerPersistsAndRejectsIdentityDrift(t *testing.T) {
	store := openTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	identity := agenttools.EffectRecord{
		InvocationID:  kernel.UUIDv7("00000000-0000-7000-8000-000000000982"),
		RequestDigest: kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		ToolCallID:    "write-1", Name: "write_file",
		ArgumentsSHA256:  "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		WorkspaceRoot:    "/tmp/isolated-workspace",
		EffectPolicyHash: kernel.Digest("cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"),
	}
	if _, found, err := store.Lookup(ctx, identity); err != nil || found {
		t.Fatalf("missing lookup = found %t, err %v", found, err)
	}
	first, created, err := store.Reserve(ctx, identity)
	if err != nil || !created || len(first.Result) != 0 {
		t.Fatalf("first reserve = %+v created=%v err=%v", first, created, err)
	}
	second, created, err := store.Reserve(ctx, identity)
	if err != nil || created || len(second.Result) != 0 {
		t.Fatalf("second reserve = %+v created=%v err=%v", second, created, err)
	}
	result := json.RawMessage(`{"name":"write_file","sha256":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}`)
	if err := store.Complete(ctx, identity, result); err != nil {
		t.Fatal(err)
	}
	if found, ok, err := store.Lookup(ctx, identity); err != nil || !ok || string(found.Result) != string(result) {
		t.Fatalf("completed lookup = %+v found=%t err=%v", found, ok, err)
	}
	if err := store.Complete(ctx, identity, result); err != nil {
		t.Fatalf("idempotent completion = %v", err)
	}
	replayed, created, err := store.Reserve(ctx, identity)
	if err != nil || created || string(replayed.Result) != string(result) {
		t.Fatalf("replayed receipt = %+v created=%v err=%v", replayed, created, err)
	}
	drift := identity
	drift.ArgumentsSHA256 = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	if _, _, err := store.Reserve(ctx, drift); !errors.Is(err, agenttools.ErrConflict) {
		t.Fatalf("argument drift = %v", err)
	}
	if _, _, err := store.Lookup(ctx, drift); !errors.Is(err, agenttools.ErrConflict) {
		t.Fatalf("lookup drift = %v", err)
	}
	if err := store.Complete(ctx, drift, result); !errors.Is(err, agenttools.ErrConflict) {
		t.Fatalf("completion drift = %v", err)
	}
}
