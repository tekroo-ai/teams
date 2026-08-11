package kernel_test

import (
	"bytes"
	"errors"
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
	access := evidenceAccess(record.RegisteredBy)
	if err := registry.Register(record, raw, access); err != nil {
		t.Fatal(err)
	}
	second := record
	second.EvidenceID = mustUUID(t, "00000000-0000-7000-8000-0000000000b2")
	second.Locator = "artifact://run/two"
	if err := registry.Register(second, raw, access); err != nil {
		t.Fatal(err)
	}
	if registry.RegistrationCount() != 2 || registry.ContentCount() != 1 {
		t.Fatalf("registrations=%d content=%d", registry.RegistrationCount(), registry.ContentCount())
	}
	mutated := append([]byte(nil), raw...)
	mutated[0] = '['
	third := record
	third.EvidenceID = mustUUID(t, "00000000-0000-7000-8000-0000000000b3")
	if err := registry.Register(third, mutated, access); err == nil {
		t.Fatal("mutated evidence passed digest verification")
	}
	mutable := record
	mutable.EvidenceID = mustUUID(t, "00000000-0000-7000-8000-0000000000b4")
	mutable.LocatorImmutable = false
	if err := registry.Register(mutable, raw, access); err == nil {
		t.Fatal("mutable locator was accepted as evidence identity")
	}
}

func TestEvidenceAccessRedactionDeletionAndAuditRebuild(t *testing.T) {
	raw := []byte(`{"secret":"value"}`)
	digest, _ := kernel.DigestBytes(raw)
	record := evidenceRecord(t, "00000000-0000-7000-8000-0000000000d1", digest, uint64(len(raw)), "artifact://raw/one")
	registry := kernel.NewEvidenceRegistry()
	access := evidenceAccess(record.RegisteredBy)
	if err := registry.Register(record, raw, access); err != nil {
		t.Fatal(err)
	}
	unauthorized := access
	unauthorized.Principal = kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "intruder"}
	unauthorized.ReadPartitions = []string{"public"}
	if _, _, err := registry.Read(record.EvidenceID, unauthorized, record.IngestedAt); !errors.Is(err, kernel.ErrEvidenceAccessDenied) {
		t.Fatalf("unauthorized read error = %v", err)
	}

	redactedRaw := []byte(`{"secret":"[redacted]"}`)
	redactedDigest, _ := kernel.DigestBytes(redactedRaw)
	redacted := evidenceRecord(t, "00000000-0000-7000-8000-0000000000d2", redactedDigest, uint64(len(redactedRaw)), "artifact://redacted/one")
	redacted.SourceEvidenceIDs = []kernel.UUIDv7{record.EvidenceID}
	redacted.Redacts = &record.EvidenceID
	if err := registry.Register(redacted, redactedRaw, access); err != nil {
		t.Fatal(err)
	}
	if _, got, err := registry.Read(redacted.EvidenceID, access, record.IngestedAt); err != nil || !bytes.Equal(got, redactedRaw) {
		t.Fatalf("redacted read = %q, %v", got, err)
	}
	tombstone := kernel.EvidenceDeletionTombstone{
		EvidenceID: record.EvidenceID, RawSHA256: record.RawSHA256, Authority: record.RegisteredBy,
		Basis: "retention expiry", DeletedAt: record.IngestedAt.Add(time.Hour),
	}
	if err := registry.Delete(tombstone, access); err != nil {
		t.Fatal(err)
	}
	deleted, got, err := registry.Read(record.EvidenceID, access, tombstone.DeletedAt)
	if !errors.Is(err, kernel.ErrEvidenceUnavailable) || got != nil || deleted.Availability != kernel.EvidenceDeleted || deleted.IntegrityState != kernel.IntegrityMissing {
		t.Fatalf("deleted evidence = %#v, %q, %v", deleted, got, err)
	}

	entries := registry.AuditEntries()
	oneShot := kernel.NewEvidenceProjection()
	if err := oneShot.Apply(entries); err != nil {
		t.Fatal(err)
	}
	resumed := kernel.NewEvidenceProjection()
	if err := resumed.Apply(entries[:1]); err != nil {
		t.Fatal(err)
	}
	if err := resumed.Apply(entries[1:]); err != nil {
		t.Fatal(err)
	}
	oneDigest, _ := oneShot.Digest()
	resumedDigest, _ := resumed.Digest()
	if oneDigest != resumedDigest {
		t.Fatalf("projection digest one-shot=%s resumed=%s", oneDigest, resumedDigest)
	}
	corrupt := append([]kernel.EvidenceAuditEntry(nil), entries...)
	corrupt[0].Digest = "invalid"
	if err := kernel.NewEvidenceProjection().Apply(corrupt); err == nil {
		t.Fatal("corrupt audit source was accepted")
	}
}

func TestClaimAssessmentRevisionsAreLinkedAndRetainRawSupport(t *testing.T) {
	raw := []byte("observed")
	digest, _ := kernel.DigestBytes(raw)
	record := evidenceRecord(t, "00000000-0000-7000-8000-0000000000d3", digest, uint64(len(raw)), "artifact://claim/one")
	registry := kernel.NewEvidenceRegistry()
	if err := registry.Register(record, raw, evidenceAccess(record.RegisteredBy)); err != nil {
		t.Fatal(err)
	}
	first := kernel.ClaimAssessment{
		AssessmentID: mustUUID(t, "00000000-0000-7000-8000-0000000000d4"), Claim: "value was present", Class: kernel.ClaimObserved,
		Supporting: []kernel.UUIDv7{record.EvidenceID}, Assessor: record.RegisteredBy, Scope: "test", AssessedAt: record.IngestedAt,
	}
	if err := registry.RegisterAssessment(first); err != nil {
		t.Fatal(err)
	}
	unlinked := first
	unlinked.AssessmentID = mustUUID(t, "00000000-0000-7000-8000-0000000000d5")
	unlinked.Class = kernel.ClaimInferred
	if err := registry.RegisterAssessment(unlinked); err == nil {
		t.Fatal("unlinked assessment revision was accepted")
	}
	linked := unlinked
	linked.Supersedes = &first.AssessmentID
	if err := registry.RegisterAssessment(linked); err != nil {
		t.Fatal(err)
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
		IngestedAt: time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC), TransportProvenance: "test://local",
		Sensitivity: "INTERNAL", AccessPartition: "engineering", RetentionPolicy: "phase-2",
		IntegrityState: kernel.IntegrityDigestVerified, SourceEvidenceIDs: []kernel.UUIDv7{},
	}
}

func evidenceAccess(principal kernel.PrincipalRef) kernel.EvidenceAccessPolicy {
	return kernel.EvidenceAccessPolicy{
		Principal: principal, ReadPartitions: []string{"engineering"}, RegisterPartitions: []string{"engineering"}, CanDelete: true,
	}
}
