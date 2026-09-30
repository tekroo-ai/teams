package operationalruntime

import (
	"context"
	"testing"

	"github.com/tekroo-ai/teams/adapters/filesystem"
	"github.com/tekroo-ai/teams/kernel"
)

func TestPlanningEvidenceBlobIsRetainedAndReadable(t *testing.T) {
	store, err := filesystem.NewExecutionEvidenceStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := &ProductionService{Runtime: &Runtime{evidence: store}}
	id := kernel.UUIDv7("00000000-0000-7000-8000-000000000123")
	content := []byte(`{"title":"Human-readable aliases"}`)
	digest := digestBytes(content)
	if err := service.retainPlanningEvidenceBlob(context.Background(), id, content, digest); err != nil {
		t.Fatal(err)
	}
	retained, err := store.Read(context.Background(), digest)
	if err != nil || string(retained) != string(content) {
		t.Fatalf("retained evidence mismatch: %q, %v", retained, err)
	}
	if err := service.retainPlanningEvidenceBlob(context.Background(), id, content, digest); err != nil {
		t.Fatalf("repeated retention must be idempotent: %v", err)
	}
}
