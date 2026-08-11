package kernel_test

import (
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

func TestEvidenceRegistryVerifiesBytesWithoutCollapsingOrigins(t *testing.T) {
	raw := []byte(`{"passed":true}`)
	digest, err := kernel.DigestBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	record := evidenceRecord(t, "00000000-0000-7000-8000-0000000000b1", digest, uint64(len(raw)), "artifact://run/one")
	registry := kernel.NewEvidenceRegistry()
	if err := registry.Register(record, raw); err != nil {
		t.Fatal(err)
	}
	second := record
	second.EvidenceID = mustUUID(t, "00000000-0000-7000-8000-0000000000b2")
	second.Locator = "artifact://run/two"
	if err := registry.Register(second, raw); err != nil {
		t.Fatal(err)
	}
	if registry.RegistrationCount() != 2 || registry.ContentCount() != 1 {
		t.Fatalf("registrations=%d content=%d", registry.RegistrationCount(), registry.ContentCount())
	}
	mutated := append([]byte(nil), raw...)
	mutated[0] = '['
	third := record
	third.EvidenceID = mustUUID(t, "00000000-0000-7000-8000-0000000000b3")
	if err := registry.Register(third, mutated); err == nil {
		t.Fatal("mutated evidence passed digest verification")
	}
	mutable := record
	mutable.EvidenceID = mustUUID(t, "00000000-0000-7000-8000-0000000000b4")
	mutable.LocatorImmutable = false
	if err := registry.Register(mutable, raw); err == nil {
		t.Fatal("mutable locator was accepted as evidence identity")
	}
}

func TestClaimClassificationCannotCallUnsupportedClaimObserved(t *testing.T) {
	assessment := kernel.ClaimAssessment{
		AssessmentID: mustUUID(t, "00000000-0000-7000-8000-0000000000c1"),
		Claim:        "the build passed", Class: kernel.ClaimObserved,
		Assessor: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"},
		Scope:    "build", AssessedAt: time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC),
	}
	if err := assessment.Validate(); err == nil {
		t.Fatal("unsupported claim was classified OBSERVED")
	}
	assessment.Supporting = []kernel.UUIDv7{mustUUID(t, "00000000-0000-7000-8000-0000000000c2")}
	if err := assessment.Validate(); err != nil {
		t.Fatal(err)
	}
	assessment.Class = kernel.ClaimComputed
	if err := assessment.Validate(); err == nil {
		t.Fatal("computed claim without method was accepted")
	}
}

func TestDecisionProvenanceRejectsIdentitySubstitution(t *testing.T) {
	basis := validProvenanceBasis(t)
	if !basis.Valid() {
		t.Fatal("valid provenance basis rejected")
	}
	basis.Runtime.BuildArtifactDigest = basis.Source.TreeDigest
	if basis.Valid() {
		t.Fatal("source tree identity substituted for build artifact identity")
	}
}

func evidenceRecord(t *testing.T, id string, digest kernel.Digest, length uint64, locator string) kernel.EvidenceRecord {
	t.Helper()
	return kernel.EvidenceRecord{
		EvidenceID: mustUUID(t, id), EvidenceKind: "TEST_RESULT", RawSHA256: digest,
		ByteLength: length, MediaType: "application/json", Locator: locator, LocatorImmutable: true,
		Availability:       kernel.EvidenceAvailable,
		RegisteredBy:       kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "test-runner"},
		ProducingComponent: "go-test", ProducingVersion: "go1.26.4",
		IngestedAt:  time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC),
		Sensitivity: "INTERNAL", AccessPartition: "engineering", RetentionPolicy: "phase-2",
		IntegrityState: kernel.IntegrityDigestVerified, SourceEvidenceIDs: []kernel.UUIDv7{},
	}
}
