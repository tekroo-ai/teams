package kernel

import (
	"errors"
	"sort"
	"time"
)

var (
	ErrEvidenceAccessDenied = errors.New("evidence access denied")
	ErrEvidenceUnavailable  = errors.New("evidence raw bytes unavailable")
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
	EvidenceID          UUIDv7               `json:"evidence_id"`
	EvidenceKind        string               `json:"evidence_kind"`
	RawSHA256           Digest               `json:"raw_sha256"`
	ByteLength          uint64               `json:"byte_length"`
	MediaType           string               `json:"media_type"`
	Locator             string               `json:"locator"`
	LocatorImmutable    bool                 `json:"locator_immutable"`
	Availability        EvidenceAvailability `json:"availability"`
	RegisteredBy        PrincipalRef         `json:"registered_by"`
	ProducingComponent  string               `json:"producing_component"`
	ProducingVersion    string               `json:"producing_version"`
	IngestedAt          time.Time            `json:"ingested_at"`
	SourceTimestamp     *time.Time           `json:"source_timestamp,omitempty"`
	TransportProvenance string               `json:"transport_provenance"`
	Sensitivity         string               `json:"sensitivity"`
	AccessPartition     string               `json:"access_partition"`
	RetentionPolicy     string               `json:"retention_policy"`
	IntegrityState      IntegrityState       `json:"integrity_state"`
	SourceEvidenceIDs   []UUIDv7             `json:"source_evidence_ids"`
	Computation         *ComputationIdentity `json:"computation,omitempty"`
	CanonicalDigest     *Digest              `json:"canonical_digest,omitempty"`
	Redacts             *UUIDv7              `json:"redacts,omitempty"`
}

type EvidenceAccessPolicy struct {
	Principal          PrincipalRef `json:"principal"`
	ReadPartitions     []string     `json:"read_partitions"`
	RegisterPartitions []string     `json:"register_partitions"`
	CanDelete          bool         `json:"can_delete"`
}

type EvidenceDeletionTombstone struct {
	EvidenceID UUIDv7       `json:"evidence_id"`
	RawSHA256  Digest       `json:"raw_sha256"`
	Authority  PrincipalRef `json:"authority"`
	Basis      string       `json:"basis"`
	DeletedAt  time.Time    `json:"deleted_at"`
}

type EvidenceAccessRecord struct {
	EvidenceID UUIDv7       `json:"evidence_id"`
	Principal  PrincipalRef `json:"principal"`
	AccessedAt time.Time    `json:"accessed_at"`
	Allowed    bool         `json:"allowed"`
}

type EvidenceAuditEntry struct {
	Position       uint64 `json:"position"`
	Kind           string `json:"kind"`
	Identity       UUIDv7 `json:"identity"`
	Digest         Digest `json:"digest"`
	PreviousDigest Digest `json:"previous_digest,omitempty"`
	EntryDigest    Digest `json:"entry_digest"`
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
	records     map[UUIDv7]EvidenceRecord
	content     map[Digest][]byte
	deletions   map[UUIDv7]EvidenceDeletionTombstone
	assessments map[UUIDv7]ClaimAssessment
	access      []EvidenceAccessRecord
	audit       []EvidenceAuditEntry
}

func NewEvidenceRegistry() *EvidenceRegistry {
	return &EvidenceRegistry{
		records: make(map[UUIDv7]EvidenceRecord), content: make(map[Digest][]byte),
		deletions: make(map[UUIDv7]EvidenceDeletionTombstone), assessments: make(map[UUIDv7]ClaimAssessment),
	}
}

func (registry *EvidenceRegistry) Register(record EvidenceRecord, raw []byte, access EvidenceAccessPolicy) error {
	if !access.Principal.Valid() || access.Principal != record.RegisteredBy || !containsString(access.RegisterPartitions, record.AccessPartition) {
		return ErrEvidenceAccessDenied
	}
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
	registry.appendAudit("REGISTERED", record.EvidenceID, record)
	return nil
}

func (registry *EvidenceRegistry) Read(evidenceID UUIDv7, access EvidenceAccessPolicy, at time.Time) (EvidenceRecord, []byte, error) {
	record, found := registry.records[evidenceID]
	allowed := found && !at.IsZero() && access.Principal.Valid() && containsString(access.ReadPartitions, record.AccessPartition)
	registry.access = append(registry.access, EvidenceAccessRecord{EvidenceID: evidenceID, Principal: access.Principal, AccessedAt: at, Allowed: allowed})
	if !allowed {
		return EvidenceRecord{}, nil, ErrEvidenceAccessDenied
	}
	copy := cloneEvidenceRecord(record)
	if _, deleted := registry.deletions[evidenceID]; deleted {
		copy.Availability = EvidenceDeleted
		copy.IntegrityState = IntegrityMissing
		return copy, nil, ErrEvidenceUnavailable
	}
	if record.Availability != EvidenceAvailable {
		return copy, nil, ErrEvidenceUnavailable
	}
	return copy, append([]byte(nil), registry.content[record.RawSHA256]...), nil
}

func (registry *EvidenceRegistry) Delete(tombstone EvidenceDeletionTombstone, access EvidenceAccessPolicy) error {
	record, found := registry.records[tombstone.EvidenceID]
	if !found || !access.Principal.Valid() || access.Principal != tombstone.Authority || !access.CanDelete || !containsString(access.ReadPartitions, record.AccessPartition) {
		return ErrEvidenceAccessDenied
	}
	if tombstone.RawSHA256 != record.RawSHA256 || tombstone.Basis == "" || tombstone.DeletedAt.IsZero() {
		return errors.New("invalid evidence deletion tombstone")
	}
	if _, exists := registry.deletions[tombstone.EvidenceID]; exists {
		return errors.New("evidence deletion already recorded")
	}
	registry.deletions[tombstone.EvidenceID] = tombstone
	contentStillRequired := false
	for evidenceID, candidate := range registry.records {
		if evidenceID != tombstone.EvidenceID && candidate.RawSHA256 == record.RawSHA256 && candidate.Availability == EvidenceAvailable {
			if _, deleted := registry.deletions[evidenceID]; !deleted {
				contentStillRequired = true
				break
			}
		}
	}
	if !contentStillRequired {
		delete(registry.content, record.RawSHA256)
	}
	registry.appendAudit("DELETED", tombstone.EvidenceID, tombstone)
	return nil
}

func (registry *EvidenceRegistry) RegisterAssessment(assessment ClaimAssessment) error {
	if err := assessment.Validate(); err != nil {
		return err
	}
	if _, exists := registry.assessments[assessment.AssessmentID]; exists {
		return errors.New("assessment identity already registered")
	}
	for _, evidenceID := range append(append([]UUIDv7(nil), assessment.Supporting...), assessment.Contradicting...) {
		record, found := registry.records[evidenceID]
		if !found || record.Availability != EvidenceAvailable || record.IntegrityState != IntegrityDigestVerified {
			return errors.New("assessment evidence unavailable")
		}
		if _, deleted := registry.deletions[evidenceID]; deleted {
			return errors.New("assessment evidence deleted")
		}
	}
	if assessment.Supersedes != nil {
		prior, found := registry.assessments[*assessment.Supersedes]
		if !found || prior.Claim != assessment.Claim || prior.Scope != assessment.Scope {
			return errors.New("superseded assessment not found")
		}
	}
	foundPrior := false
	for _, prior := range registry.assessments {
		if prior.Claim == assessment.Claim && prior.Scope == assessment.Scope {
			foundPrior = true
		}
	}
	if foundPrior && assessment.Supersedes == nil {
		return errors.New("assessment revision must explicitly supersede prior assessment")
	}
	registry.assessments[assessment.AssessmentID] = cloneClaimAssessment(assessment)
	registry.appendAudit("ASSESSED", assessment.AssessmentID, assessment)
	return nil
}

func (record EvidenceRecord) Validate(raw []byte) error {
	if !record.EvidenceID.Valid() || record.EvidenceKind == "" || !record.RawSHA256.Valid() || record.MediaType == "" || record.Locator == "" || !record.LocatorImmutable || !record.RegisteredBy.Valid() || record.ProducingComponent == "" || record.ProducingVersion == "" || record.IngestedAt.IsZero() || record.TransportProvenance == "" || record.Sensitivity == "" || record.AccessPartition == "" || record.RetentionPolicy == "" {
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
	if record.Computation != nil && len(record.SourceEvidenceIDs) == 0 {
		return errors.New("computed evidence requires source evidence")
	}
	if record.CanonicalDigest != nil && !record.CanonicalDigest.Valid() {
		return errors.New("invalid canonical digest")
	}
	if record.Redacts != nil {
		if !record.Redacts.Valid() || !containsUUID(record.SourceEvidenceIDs, *record.Redacts) {
			return errors.New("redaction requires explicit source lineage")
		}
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
func (registry *EvidenceRegistry) AccessLog() []EvidenceAccessRecord {
	return append([]EvidenceAccessRecord(nil), registry.access...)
}
func (registry *EvidenceRegistry) AuditEntries() []EvidenceAuditEntry {
	return append([]EvidenceAuditEntry(nil), registry.audit...)
}

func (registry *EvidenceRegistry) appendAudit(kind string, identity UUIDv7, value any) {
	digest, _ := digestValue(value)
	previous := Digest("")
	if len(registry.audit) > 0 {
		previous = registry.audit[len(registry.audit)-1].EntryDigest
	}
	entry := EvidenceAuditEntry{Position: uint64(len(registry.audit) + 1), Kind: kind, Identity: identity, Digest: digest, PreviousDigest: previous}
	entry.EntryDigest, _ = evidenceAuditEntryDigest(entry)
	registry.audit = append(registry.audit, entry)
}

func evidenceAuditEntryDigest(entry EvidenceAuditEntry) (Digest, error) {
	return digestValue(struct {
		Position       uint64 `json:"position"`
		Kind           string `json:"kind"`
		Identity       UUIDv7 `json:"identity"`
		Digest         Digest `json:"digest"`
		PreviousDigest Digest `json:"previous_digest,omitempty"`
	}{entry.Position, entry.Kind, entry.Identity, entry.Digest, entry.PreviousDigest})
}

func cloneEvidenceRecord(record EvidenceRecord) EvidenceRecord {
	copy := record
	copy.SourceEvidenceIDs = append([]UUIDv7(nil), record.SourceEvidenceIDs...)
	if record.Computation != nil {
		computation := *record.Computation
		copy.Computation = &computation
	}
	if record.SourceTimestamp != nil {
		value := *record.SourceTimestamp
		copy.SourceTimestamp = &value
	}
	if record.CanonicalDigest != nil {
		value := *record.CanonicalDigest
		copy.CanonicalDigest = &value
	}
	if record.Redacts != nil {
		value := *record.Redacts
		copy.Redacts = &value
	}
	return copy
}

func cloneClaimAssessment(value ClaimAssessment) ClaimAssessment {
	copy := value
	copy.Supporting = append([]UUIDv7(nil), value.Supporting...)
	copy.Contradicting = append([]UUIDv7(nil), value.Contradicting...)
	return copy
}

type EvidenceProjection struct {
	NextPosition    uint64
	Entries         map[UUIDv7]Digest
	LastEntryDigest Digest
}

func NewEvidenceProjection() *EvidenceProjection {
	return &EvidenceProjection{NextPosition: 1, Entries: make(map[UUIDv7]Digest)}
}

func (projection *EvidenceProjection) Apply(entries []EvidenceAuditEntry) error {
	for _, entry := range entries {
		digest, err := evidenceAuditEntryDigest(entry)
		if err != nil || entry.Position != projection.NextPosition || entry.Kind == "" || !entry.Identity.Valid() || !entry.Digest.Valid() || !entry.EntryDigest.Valid() || entry.EntryDigest != digest || entry.PreviousDigest != projection.LastEntryDigest {
			return errors.New("corrupt or discontinuous audit source")
		}
		projection.Entries[entry.Identity] = entry.Digest
		projection.LastEntryDigest = entry.EntryDigest
		projection.NextPosition++
	}
	return nil
}

func (projection *EvidenceProjection) Digest() (Digest, error) {
	identities := make([]UUIDv7, 0, len(projection.Entries))
	for identity := range projection.Entries {
		identities = append(identities, identity)
	}
	sort.Slice(identities, func(i, j int) bool { return identities[i] < identities[j] })
	values := make([]struct {
		Identity UUIDv7 `json:"identity"`
		Digest   Digest `json:"digest"`
	}, len(identities))
	for index, identity := range identities {
		values[index].Identity = identity
		values[index].Digest = projection.Entries[identity]
	}
	return digestValue(struct {
		NextPosition    uint64 `json:"next_position"`
		Entries         any    `json:"entries"`
		LastEntryDigest Digest `json:"last_entry_digest"`
	}{projection.NextPosition, values, projection.LastEntryDigest})
}
