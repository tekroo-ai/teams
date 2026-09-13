package mongo

import (
	"testing"

	"github.com/tekroo-ai/teams/kernel"
)

func TestRecoveryBudgetEvidenceMustBelongToCurrentTaskProfile(t *testing.T) {
	current := kernel.UUIDv7("00000000-0000-7000-8000-000000000101")
	unrelated := kernel.UUIDv7("00000000-0000-7000-8000-000000000102")
	bound := profileEvidenceSet([]kernel.UUIDv7{current})
	if !recoveryEvidenceBoundToProfile([]kernel.UUIDv7{current}, bound) {
		t.Fatal("current task recovery evidence was rejected")
	}
	if recoveryEvidenceBoundToProfile([]kernel.UUIDv7{unrelated}, bound) {
		t.Fatal("another task's shared-budget recovery evidence was accepted")
	}
	if recoveryEvidenceBoundToProfile(nil, bound) {
		t.Fatal("empty recovery evidence was accepted")
	}
}
