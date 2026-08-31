package filesystem

import (
	"context"
	"os"
	"testing"

	"github.com/tekroo-ai/teams/kernel"
)

func TestExecutionEvidenceStoreIsContentAddressedAndIdempotent(t *testing.T) {
	store, err := NewExecutionEvidenceStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	evidenceID := kernel.UUIDv7("00000000-0000-7000-8000-000000000001")
	first, err := store.Put(context.Background(), evidenceID, []byte("retained evidence"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Put(context.Background(), evidenceID, []byte("retained evidence"))
	if err != nil || first != second {
		t.Fatalf("first=%#v second=%#v err=%v", first, second, err)
	}
	content, err := os.ReadFile(first.Locator)
	if err != nil || string(content) != "retained evidence" {
		t.Fatalf("content=%q err=%v", content, err)
	}
}
