package application

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

var (
	ErrInvalidModelProfile   = errors.New("invalid model profile")
	ErrModelProfileConflict  = errors.New("model profile digest conflict")
	ErrUnknownModelProfile   = errors.New("unknown model profile")
	ErrInvalidQualification  = errors.New("invalid model qualification")
	ErrQualificationConflict = errors.New("qualification identity conflict")
	ErrQualificationNotFound = errors.New("qualification not found")
	ErrInvalidRevocation     = errors.New("invalid qualification revocation")
)

type ModelProfileIdentity struct {
	ProviderIdentity            string        `json:"provider_identity"`
	EndpointClass               string        `json:"endpoint_class"`
	ModelIdentifier             string        `json:"model_identifier"`
	ModelRevision               string        `json:"model_revision"`
	WeightIdentityAvailable     bool          `json:"weight_identity_available"`
	WeightsDigest               kernel.Digest `json:"weights_digest,omitempty"`
	Quantization                string        `json:"quantization"`
	InferenceEngine             string        `json:"inference_engine"`
	InferenceEngineVersion      string        `json:"inference_engine_version"`
	SamplingConfigurationDigest kernel.Digest `json:"sampling_configuration_digest"`
	ContextLimit                uint64        `json:"context_limit"`
	OutputLimit                 uint64        `json:"output_limit"`
	ToolSurfaceDigest           kernel.Digest `json:"tool_surface_digest"`
	SystemPromptDigest          kernel.Digest `json:"system_prompt_digest"`
	RoleLibraryDigest           kernel.Digest `json:"role_library_digest"`
	RuntimeIdentityDigest       kernel.Digest `json:"runtime_identity_digest"`
	IsolationPolicyDigest       kernel.Digest `json:"isolation_policy_digest"`
	DataResidencyPolicyDigest   kernel.Digest `json:"data_residency_policy_digest"`
	CostReported                bool          `json:"cost_reported"`
	CostScheduleDigest          kernel.Digest `json:"cost_schedule_digest,omitempty"`
	CapabilityTags              []string      `json:"capability_tags"`
}

func (profile ModelProfileIdentity) Valid() bool {
	if profile.ProviderIdentity == "" || profile.EndpointClass == "" || profile.ModelIdentifier == "" || profile.ModelRevision == "" || profile.Quantization == "" || profile.InferenceEngine == "" || profile.InferenceEngineVersion == "" || !profile.SamplingConfigurationDigest.Valid() || profile.ContextLimit == 0 || profile.OutputLimit == 0 || profile.OutputLimit > profile.ContextLimit || !profile.ToolSurfaceDigest.Valid() || !profile.SystemPromptDigest.Valid() || !profile.RoleLibraryDigest.Valid() || !profile.RuntimeIdentityDigest.Valid() || !profile.IsolationPolicyDigest.Valid() || !profile.DataResidencyPolicyDigest.Valid() || !validRegistryStrings(profile.CapabilityTags) {
		return false
	}
	if profile.WeightIdentityAvailable {
		if !profile.WeightsDigest.Valid() {
			return false
		}
	} else if profile.WeightsDigest != "" {
		return false
	}
	if profile.CostReported {
		return profile.CostScheduleDigest.Valid()
	}
	return profile.CostScheduleDigest == ""
}

func (profile ModelProfileIdentity) Canonical() ModelProfileIdentity {
	copy := profile
	copy.CapabilityTags = append([]string(nil), profile.CapabilityTags...)
	sort.Strings(copy.CapabilityTags)
	return copy
}

func (profile ModelProfileIdentity) Digest() (kernel.Digest, error) {
	profile = profile.Canonical()
	if !profile.Valid() {
		return "", ErrInvalidModelProfile
	}
	return digestJSON(profile)
}

type QualificationRevocation struct {
	QualificationID  kernel.UUIDv7       `json:"qualification_id"`
	RevokedAt        time.Time           `json:"revoked_at"`
	Authority        kernel.PrincipalRef `json:"authority"`
	Reason           string              `json:"reason"`
	EvidenceIDs      []kernel.UUIDv7     `json:"evidence_ids"`
	RevocationDigest kernel.Digest       `json:"revocation_digest"`
}

func (revocation QualificationRevocation) Valid() bool {
	return revocation.QualificationID.Valid() && !revocation.RevokedAt.IsZero() && revocation.Authority.Valid() && revocation.Reason != "" && len(revocation.Reason) <= 4096 && validRegistryUUIDs(revocation.EvidenceIDs) && revocation.RevocationDigest.Valid()
}

type qualificationRecord struct {
	Qualification kernel.ModelProfileQualification
	Revocation    *QualificationRevocation
}

type QualificationCorpusDefinition struct {
	CorpusID          kernel.UUIDv7        `json:"corpus_id"`
	Revision          uint64               `json:"revision"`
	DecisionRoute     kernel.DecisionRoute `json:"decision_route"`
	QualifiedRole     string               `json:"qualified_role"`
	WorkKinds         []kernel.WorkKind    `json:"work_kinds"`
	ScenarioIDs       []string             `json:"scenario_ids"`
	ThresholdDigest   kernel.Digest        `json:"threshold_digest"`
	ToolSurfaceDigest kernel.Digest        `json:"tool_surface_digest"`
}

func (corpus QualificationCorpusDefinition) Valid() bool {
	if !corpus.CorpusID.Valid() || corpus.Revision == 0 || !corpus.DecisionRoute.ModelExecutable() || corpus.QualifiedRole == "" || len(corpus.QualifiedRole) > 256 || len(corpus.WorkKinds) == 0 || len(corpus.WorkKinds) > 7 || !validRegistryStrings(corpus.ScenarioIDs) || !corpus.ThresholdDigest.Valid() || !corpus.ToolSurfaceDigest.Valid() {
		return false
	}
	seen := make(map[kernel.WorkKind]struct{}, len(corpus.WorkKinds))
	for _, kind := range corpus.WorkKinds {
		if !kind.Valid() {
			return false
		}
		if _, duplicate := seen[kind]; duplicate {
			return false
		}
		seen[kind] = struct{}{}
	}
	return true
}

func (corpus QualificationCorpusDefinition) Canonical() QualificationCorpusDefinition {
	copy := corpus
	copy.WorkKinds = append([]kernel.WorkKind(nil), corpus.WorkKinds...)
	copy.ScenarioIDs = append([]string(nil), corpus.ScenarioIDs...)
	sort.Slice(copy.WorkKinds, func(i, j int) bool { return copy.WorkKinds[i] < copy.WorkKinds[j] })
	sort.Strings(copy.ScenarioIDs)
	return copy
}

func (corpus QualificationCorpusDefinition) Digest() (kernel.Digest, error) {
	corpus = corpus.Canonical()
	if !corpus.Valid() {
		return "", ErrInvalidQualification
	}
	return digestJSON(corpus)
}

type InMemoryModelRegistry struct {
	mu             sync.RWMutex
	profiles       map[kernel.Digest]ModelProfileIdentity
	corpora        map[kernel.Digest]QualificationCorpusDefinition
	qualifications map[kernel.UUIDv7]qualificationRecord
}

func NewInMemoryModelRegistry() *InMemoryModelRegistry {
	return &InMemoryModelRegistry{profiles: make(map[kernel.Digest]ModelProfileIdentity), corpora: make(map[kernel.Digest]QualificationCorpusDefinition), qualifications: make(map[kernel.UUIDv7]qualificationRecord)}
}

func (registry *InMemoryModelRegistry) RegisterCorpus(corpus QualificationCorpusDefinition) (kernel.Digest, error) {
	canonical := corpus.Canonical()
	digest, err := canonical.Digest()
	if err != nil {
		return "", err
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if prior, found := registry.corpora[digest]; found {
		priorBytes, _ := json.Marshal(prior)
		candidateBytes, _ := json.Marshal(canonical)
		if string(priorBytes) != string(candidateBytes) {
			return "", ErrQualificationConflict
		}
		return digest, nil
	}
	registry.corpora[digest] = canonical
	return digest, nil
}

func (registry *InMemoryModelRegistry) Corpus(digest kernel.Digest) (QualificationCorpusDefinition, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	corpus, found := registry.corpora[digest]
	return corpus.Canonical(), found
}

func (registry *InMemoryModelRegistry) RegisterProfile(profile ModelProfileIdentity) (kernel.Digest, error) {
	canonical := profile.Canonical()
	digest, err := canonical.Digest()
	if err != nil {
		return "", err
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if prior, found := registry.profiles[digest]; found {
		priorBytes, _ := json.Marshal(prior)
		candidateBytes, _ := json.Marshal(canonical)
		if string(priorBytes) != string(candidateBytes) {
			return "", ErrModelProfileConflict
		}
		return digest, nil
	}
	registry.profiles[digest] = canonical
	return digest, nil
}

func (registry *InMemoryModelRegistry) Profile(digest kernel.Digest) (ModelProfileIdentity, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	profile, found := registry.profiles[digest]
	return profile.Canonical(), found
}

func QualificationDigest(qualification kernel.ModelProfileQualification) (kernel.Digest, error) {
	copy := qualification.Clone()
	copy.QualificationDigest = ""
	copy.RevokedAt = nil
	sort.Slice(copy.QualifiedWorkKinds, func(i, j int) bool { return copy.QualifiedWorkKinds[i] < copy.QualifiedWorkKinds[j] })
	sort.Slice(copy.EvidenceIDs, func(i, j int) bool { return copy.EvidenceIDs[i] < copy.EvidenceIDs[j] })
	if !copy.QualificationID.Valid() || !copy.QualificationCorpusDigest.Valid() || !copy.ModelProfileDigest.Valid() || !copy.DecisionRoute.ModelExecutable() || copy.QualifiedRole == "" || !copy.Status.Valid() || copy.ObservedAt.IsZero() || len(copy.QualifiedWorkKinds) == 0 || len(copy.EvidenceIDs) == 0 {
		return "", ErrInvalidQualification
	}
	return digestJSON(copy)
}

func (registry *InMemoryModelRegistry) RecordQualification(qualification kernel.ModelProfileQualification) error {
	expectedDigest, err := QualificationDigest(qualification)
	if err != nil || !qualification.Valid() || qualification.QualificationDigest != expectedDigest || qualification.RevokedAt != nil {
		return ErrInvalidQualification
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, found := registry.profiles[qualification.ModelProfileDigest]; !found {
		return ErrUnknownModelProfile
	}
	if _, found := registry.corpora[qualification.QualificationCorpusDigest]; !found {
		return ErrInvalidQualification
	}
	if prior, found := registry.qualifications[qualification.QualificationID]; found {
		if prior.Qualification.QualificationDigest != qualification.QualificationDigest {
			return ErrQualificationConflict
		}
		return nil
	}
	registry.qualifications[qualification.QualificationID] = qualificationRecord{Qualification: qualification.Clone()}
	return nil
}

func (registry *InMemoryModelRegistry) RevokeQualification(revocation QualificationRevocation) error {
	copy := revocation
	copy.RevocationDigest = ""
	sort.Slice(copy.EvidenceIDs, func(i, j int) bool { return copy.EvidenceIDs[i] < copy.EvidenceIDs[j] })
	digest, err := digestJSON(copy)
	if err != nil || revocation.RevocationDigest != digest || !revocation.Valid() {
		return ErrInvalidRevocation
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	record, found := registry.qualifications[revocation.QualificationID]
	if !found {
		return ErrQualificationNotFound
	}
	if revocation.RevokedAt.Before(record.Qualification.ObservedAt) {
		return ErrInvalidRevocation
	}
	if record.Revocation != nil {
		if record.Revocation.RevocationDigest != revocation.RevocationDigest {
			return ErrQualificationConflict
		}
		return nil
	}
	revocation.EvidenceIDs = copy.EvidenceIDs
	record.Revocation = &revocation
	registry.qualifications[revocation.QualificationID] = record
	return nil
}

func RevocationDigest(revocation QualificationRevocation) (kernel.Digest, error) {
	copy := revocation
	copy.RevocationDigest = ""
	sort.Slice(copy.EvidenceIDs, func(i, j int) bool { return copy.EvidenceIDs[i] < copy.EvidenceIDs[j] })
	if !copy.QualificationID.Valid() || copy.RevokedAt.IsZero() || !copy.Authority.Valid() || copy.Reason == "" || !validRegistryUUIDs(copy.EvidenceIDs) {
		return "", ErrInvalidRevocation
	}
	return digestJSON(copy)
}

func (registry *InMemoryModelRegistry) EligibleQualifications(requiredRoute kernel.DecisionRoute, workKind kernel.WorkKind, at time.Time) []kernel.ModelProfileQualification {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	result := make([]kernel.ModelProfileQualification, 0)
	for _, record := range registry.qualifications {
		qualification := record.Qualification.Clone()
		if record.Revocation != nil {
			revokedAt := record.Revocation.RevokedAt
			qualification.RevokedAt = &revokedAt
		}
		if qualification.EligibleAt(at) && qualification.DecisionRoute.Satisfies(requiredRoute) && containsRegistryWorkKind(qualification.QualifiedWorkKinds, workKind) {
			result = append(result, qualification)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ModelProfileDigest != result[j].ModelProfileDigest {
			return result[i].ModelProfileDigest < result[j].ModelProfileDigest
		}
		return result[i].QualificationID < result[j].QualificationID
	})
	return result
}

func digestJSON(value any) (kernel.Digest, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return kernel.Digest(hex.EncodeToString(digest[:])), nil
}

func validRegistryStrings(values []string) bool {
	if len(values) == 0 || len(values) > 64 {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" || len(value) > 4096 {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validRegistryUUIDs(values []kernel.UUIDv7) bool {
	if len(values) == 0 || len(values) > 64 {
		return false
	}
	seen := make(map[kernel.UUIDv7]struct{}, len(values))
	for _, value := range values {
		if !value.Valid() {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func containsRegistryWorkKind(values []kernel.WorkKind, target kernel.WorkKind) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
