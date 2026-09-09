package operationalruntime

import (
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

func TestFeatureReplanRequestRequiresExactFiniteEvidenceBoundCorrection(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	evidence := kernel.EvidenceRef{EvidenceID: "018f0000-0000-7000-8000-000000000003", SHA256: kernel.Digest(strings.Repeat("a", 64))}
	valid := FeatureReplanRequest{ExpectedRevision: 4, ExpectedPlanVersion: 1, Reason: "replace the observed infeasible task graph", EvidenceRefs: []kernel.EvidenceRef{evidence}, DeadlineAt: now.Add(time.Hour), IdempotencyKey: "feature-replan-1"}
	if !valid.Valid() {
		t.Fatal("valid feature replan request rejected")
	}
	duplicate := valid
	duplicate.EvidenceRefs = []kernel.EvidenceRef{evidence, evidence}
	if duplicate.Valid() {
		t.Fatal("duplicate evidence accepted")
	}
	missingVersion := valid
	missingVersion.ExpectedPlanVersion = 0
	if missingVersion.Valid() {
		t.Fatal("missing plan version accepted")
	}
}
