package kernel_test

import (
	"testing"

	"github.com/tekroo-ai/teams/kernel"
)

func TestQualifiedAssignmentRequiresUniqueConstraintsAndCompleteEvidence(t *testing.T) {
	authorization := qualifiedAssignmentFixture(
		"00000000-0000-7000-8000-000000000101",
		"00000000-0000-7000-8000-000000000102",
	)
	if !authorization.Valid() {
		t.Fatal("fixture must be valid")
	}

	missingEvidence := authorization.Clone()
	missingEvidence.HardConstraintResults[0].EvidenceIDs = []kernel.UUIDv7{"00000000-0000-7000-8000-000000000103"}
	if missingEvidence.Valid() {
		t.Fatal("constraint evidence absent from the authorization evidence set was accepted")
	}

	duplicateConstraint := authorization.Clone()
	duplicateConstraint.HardConstraintResults = append(duplicateConstraint.HardConstraintResults, duplicateConstraint.HardConstraintResults[0])
	if duplicateConstraint.Valid() {
		t.Fatal("duplicate hard constraint was accepted")
	}
}
