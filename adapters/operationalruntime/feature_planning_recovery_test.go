package operationalruntime

import (
	"testing"

	"github.com/tekroo-ai/teams/kernel"
)

func TestPlanningRecoveryRequestValidationAndDigestAreDeterministic(t *testing.T) {
	invocationID := kernel.UUIDv7("00000000-0000-7000-8000-000000000301")
	first := kernel.EvidenceRef{EvidenceID: "00000000-0000-7000-8000-000000000302", SHA256: repeatedDigest('a')}
	second := kernel.EvidenceRef{EvidenceID: "00000000-0000-7000-8000-000000000303", SHA256: repeatedDigest('b')}
	request := PlanningRecoveryRequest{ExpectedRevision: 5, Reason: "correct observed planning drift", EvidenceRefs: []kernel.EvidenceRef{second, first}, IdempotencyKey: "planning-recovery-1"}
	if !request.Valid() {
		t.Fatal("valid recovery request was rejected")
	}
	digest, err := planningRecoveryConditionDigest(invocationID, request)
	if err != nil {
		t.Fatal(err)
	}
	reordered := request
	reordered.EvidenceRefs = []kernel.EvidenceRef{first, second}
	reorderedDigest, err := planningRecoveryConditionDigest(invocationID, reordered)
	if err != nil {
		t.Fatal(err)
	}
	if digest != reorderedDigest {
		t.Fatalf("evidence order changed recovery digest: %s != %s", digest, reorderedDigest)
	}
	duplicate := request
	duplicate.EvidenceRefs = []kernel.EvidenceRef{first, first}
	if duplicate.Valid() {
		t.Fatal("duplicate recovery evidence was accepted")
	}
}
