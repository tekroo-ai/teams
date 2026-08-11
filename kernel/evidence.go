package kernel

import (
	"errors"
	"time"
)

type EvidenceAvailability string

const (
	EvidenceAvailable   EvidenceAvailability = "AVAILABLE"
	EvidenceUnavailable EvidenceAvailability = "UNAVAILABLE"
	EvidenceDeleted     EvidenceAvailability = "DELETED_WITH_TOMBSTONE"
)

type IntegrityState string

const (
	IntegrityUnverified     IntegrityState = "UNVERIFIED"
	IntegrityDigestVerified IntegrityState = "DIGEST_VERIFIED"
	IntegrityMissing        IntegrityState = "MISSING"
	IntegrityMismatch       IntegrityState = "MISMATCH"
)

type ComputationIdentity struct {
	Method              string `json:"method"`
	BuildDigest         Digest `json:"build_digest"`
	ConfigurationDigest Digest `json:"configuration_digest"`
	Deterministic       bool   `json:"deterministic"`
}

type EvidenceRecord struct {
	EvidenceID         UUIDv7               `json:"evidence_id"`
	EvidenceKind       string               `json:"evidence_kind"`
	RawSHA256          Digest               `json:"raw_sha256"`
	ByteLength         uint64               `json:"byte_length"`
	MediaType          string               `json:"media_type"`
	Locator            string               `json:"locator"`
	LocatorImmutable   bool                 `json:"locator_immutable"`
	Availability       EvidenceAvailability `json:"availability"`
	RegisteredBy       PrincipalRef         `json:"registered_by"`
	ProducingComponent string               `json:"producing_component"`
	ProducingVersion   string               `json:"producing_version"`
	IngestedAt         time.Time            `json:"ingested_at"`
	Sensitivity        string               `json:"sensitivity"`
	AccessPartition    string               `json:"access_partition"`
	RetentionPolicy    string               `json:"retention_policy"`
	IntegrityState     IntegrityState       `json:"integrity_state"`
	SourceEvidenceIDs  []UUIDv7             `json:"source_evidence_ids"`
	Computation        *ComputationIdentity `json:"computation,omitempty"`
}

type ClaimClass string

const (
	ClaimObserved ClaimClass = "OBSERVED"
	ClaimComputed ClaimClass = "COMPUTED"
	ClaimInferred ClaimClass = "INFERRED"
	ClaimDecided  ClaimClass = "DECIDED"
	ClaimUnknown  ClaimClass = "UNKNOWN"
)

type ClaimAssessment struct {
	AssessmentID  UUIDv7       `json:"assessment_id"`
	Claim         string       `json:"claim"`
	Class         ClaimClass   `json:"class"`
	Supporting    []UUIDv7     `json:"supporting_evidence_ids"`
	Contradicting []UUIDv7     `json:"contradicting_evidence_ids"`
	Method        string       `json:"method,omitempty"`
	Assessor      PrincipalRef `json:"assessor"`
	Scope         string       `json:"scope"`
	AssessedAt    time.Time    `json:"assessed_at"`
	Supersedes    *UUIDv7      `json:"supersedes,omitempty"`
}

type EvidenceRegistry struct {
	records map[UUIDv7]EvidenceRecord
	content map[Digest][]byte
}

func NewEvidenceRegistry() *EvidenceRegistry {
	return &EvidenceRegistry{records: make(map[UUIDv7]EvidenceRecord), content: make(map[Digest][]byte)}
}

func (registry *EvidenceRegistry) Register(record EvidenceRecord, raw []byte) error {
	if err := record.Validate(raw); err != nil {
		return err
	}
	if _, exists := registry.records[record.EvidenceID]; exists {
		return errors.New("evidence identity already registered")
	}
	registry.records[record.EvidenceID] = cloneEvidenceRecord(record)
	if raw != nil {
		if _, exists := registry.content[record.RawSHA256]; !exists {
			registry.content[record.RawSHA256] = append([]byte(nil), raw...)
		}
	}
	return nil
}

func (record EvidenceRecord) Validate(raw []byte) error {
	if !record.EvidenceID.Valid() || record.EvidenceKind == "" || !record.RawSHA256.Valid() || record.MediaType == "" || record.Locator == "" || !record.LocatorImmutable || !record.RegisteredBy.Valid() || record.ProducingComponent == "" || record.ProducingVersion == "" || record.IngestedAt.IsZero() || record.Sensitivity == "" || record.AccessPartition == "" || record.RetentionPolicy == "" {
		return errors.New("invalid evidence metadata")
	}
	for _, source := range record.SourceEvidenceIDs {
		if !source.Valid() || source == record.EvidenceID {
			return errors.New("invalid evidence lineage")
		}
	}
	if record.Computation != nil && (record.Computation.Method == "" || !record.Computation.BuildDigest.Valid() || !record.Computation.ConfigurationDigest.Valid()) {
		return errors.New("incomplete computation identity")
	}
	if record.Availability == EvidenceAvailable {
		if raw == nil || uint64(len(raw)) != record.ByteLength {
			return errors.New("available evidence bytes are missing or wrong length")
		}
		digest, _ := DigestBytes(raw)
		if digest != record.RawSHA256 || record.IntegrityState != IntegrityDigestVerified {
			return errors.New("evidence digest mismatch")
		}
	} else if raw != nil {
		return errors.New("unavailable evidence cannot claim raw bytes")
	}
	return nil
}

func (assessment ClaimAssessment) Validate() error {
	if !assessment.AssessmentID.Valid() || assessment.Claim == "" || !assessment.Assessor.Valid() || assessment.Scope == "" || assessment.AssessedAt.IsZero() {
		return errors.New("invalid claim assessment")
	}
	switch assessment.Class {
	case ClaimObserved:
		if len(assessment.Supporting) == 0 || assessment.Method != "" {
			return errors.New("observed claim requires raw support and no computation method")
		}
	case ClaimComputed:
		if len(assessment.Supporting) == 0 || assessment.Method == "" {
			return errors.New("computed claim requires inputs and method")
		}
	case ClaimInferred, ClaimDecided, ClaimUnknown:
	default:
		return errors.New("unknown claim class")
	}
	return nil
}

func (registry *EvidenceRegistry) RegistrationCount() int { return len(registry.records) }
func (registry *EvidenceRegistry) ContentCount() int      { return len(registry.content) }

func cloneEvidenceRecord(record EvidenceRecord) EvidenceRecord {
	copy := record
	copy.SourceEvidenceIDs = append([]UUIDv7(nil), record.SourceEvidenceIDs...)
	if record.Computation != nil {
		computation := *record.Computation
		copy.Computation = &computation
	}
	return copy
}
