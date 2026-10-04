package operationalruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tekroo-ai/teams/adapters/filesystem"
	"github.com/tekroo-ai/teams/kernel"
)

type nativeEvidenceRegistryStub struct {
	id    kernel.UUIDv7
	event kernel.DomainEvent
}

func (stub nativeEvidenceRegistryStub) ReadAggregateRevisionHead(_ context.Context, ref kernel.AggregateRef) (uint64, kernel.UUIDv7, bool, error) {
	return 1, stub.event.EventID, ref == (kernel.AggregateRef{Kind: kernel.AggregateEvidence, ID: stub.id}), nil
}

func (stub nativeEvidenceRegistryStub) ReadEvent(_ context.Context, id kernel.UUIDv7) (kernel.DomainEvent, bool, error) {
	return stub.event, id == stub.event.EventID, nil
}

func TestNativeEvidenceResolverReadsRegisteredArtifactWithinRoot(t *testing.T) {
	root := t.TempDir()
	content := []byte(`{"gate":"go-test","outcome":"PASS"}`)
	sum := sha256.Sum256(content)
	digest := kernel.Digest(hex.EncodeToString(sum[:]))
	id := kernel.UUIDv7("00000000-0000-7000-8000-000000000021")
	eventID := kernel.UUIDv7("00000000-0000-7000-8000-000000000022")
	path := filepath.Join(root, "candidate-workspaces", "receipts", "candidate.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	registryPayload := func(locator string) json.RawMessage {
		value, err := json.Marshal(map[string]any{"availability": "AVAILABLE", "integrity_state": "DIGEST_VERIFIED", "locator_immutable": true,
			"locator": locator, "byte_length": len(content), "sha256": digest})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	registry := nativeEvidenceRegistryStub{id: id, event: kernel.DomainEvent{EventID: eventID, EventType: "tekroo.event.evidence.registered", Aggregate: kernel.AggregateRef{Kind: kernel.AggregateEvidence, ID: id}, Payload: registryPayload(path)}}
	blobs, err := filesystem.NewExecutionEvidenceStore(root)
	if err != nil {
		t.Fatal(err)
	}
	resolver := newNativeEvidenceResolver(registry, blobs, root, []kernel.EvidenceRef{{EvidenceID: id, SHA256: digest}})
	got, err := resolver.Read(t.Context(), digest)
	if err != nil || string(got) != string(content) {
		t.Fatalf("read registered artifact: %q, %v", got, err)
	}
	outside := filepath.Join(t.TempDir(), "candidate.json")
	if err := os.WriteFile(outside, content, 0600); err != nil {
		t.Fatal(err)
	}
	registry.event.Payload = registryPayload(outside)
	resolver = newNativeEvidenceResolver(registry, blobs, root, []kernel.EvidenceRef{{EvidenceID: id, SHA256: digest}})
	if _, err := resolver.Read(t.Context(), digest); !errors.Is(err, errNativeEvidenceUnavailable) {
		t.Fatalf("accepted artifact outside evidence root: %v", err)
	}
	registry.event.Payload = registryPayload(path)
	resolver = newNativeEvidenceResolver(registry, blobs, root, []kernel.EvidenceRef{{EvidenceID: id, SHA256: digest}})
	if err := os.WriteFile(path, []byte(`{"gate":"go-test","outcome":"FAIL"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Read(t.Context(), digest); !errors.Is(err, errNativeEvidenceUnavailable) {
		t.Fatalf("accepted altered artifact: %v", err)
	}
}
