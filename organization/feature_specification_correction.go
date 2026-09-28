package organization

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

// FeatureSpecificationCorrection retains the exact PM-authored specification
// when an operator narrows an unplanned feature. The replacement is the
// feature's current Specification; the original is never silently discarded.
type FeatureSpecificationCorrection struct {
	PriorSpecification FeatureSpecification `json:"prior_specification"`
	PriorDigest        kernel.Digest        `json:"prior_digest"`
	ReplacementDigest  kernel.Digest        `json:"replacement_digest"`
	ArchitectureRound  uint32               `json:"architecture_round"`
	RequestedBy        kernel.PrincipalRef  `json:"requested_by"`
	Reason             string               `json:"reason"`
	EvidenceRefs       []kernel.EvidenceRef `json:"evidence_refs"`
	RequestedAt        time.Time            `json:"requested_at"`
	IdempotencyKey     string               `json:"idempotency_key"`
}

func (correction FeatureSpecificationCorrection) Validate(feature FeatureRequest) error {
	if feature.Specification == nil || correction.ArchitectureRound == 0 || correction.ArchitectureRound > 128 || correction.PriorSpecification.Validate(feature) != nil || correction.PriorSpecification.AmendedBy != nil || correction.RequestedBy != feature.SubmittedBy || correction.RequestedBy.Kind != kernel.PrincipalHuman || correction.Reason == "" || len(correction.Reason) > 4096 || len(correction.EvidenceRefs) == 0 || len(correction.EvidenceRefs) > 64 || correction.RequestedAt.Before(feature.CreatedAt) || correction.IdempotencyKey == "" || len(correction.IdempotencyKey) > 256 || feature.ScopeRevision < 2 {
		return ErrInvalidFeature
	}
	if feature.Specification.AmendedBy == nil || *feature.Specification.AmendedBy != correction.RequestedBy || feature.Specification.AmendedAt == nil || !feature.Specification.AmendedAt.Equal(correction.RequestedAt) {
		return ErrInvalidFeature
	}
	prior, err := featureSpecificationDigest(correction.PriorSpecification)
	if err != nil || prior != correction.PriorDigest {
		return ErrInvalidFeature
	}
	replacement, err := featureSpecificationDigest(*feature.Specification)
	if err != nil || replacement != correction.ReplacementDigest || prior == replacement {
		return ErrInvalidFeature
	}
	seen := make(map[kernel.UUIDv7]struct{}, len(correction.EvidenceRefs))
	for _, evidence := range correction.EvidenceRefs {
		if !evidence.EvidenceID.Valid() || !evidence.SHA256.Valid() {
			return ErrInvalidFeature
		}
		if _, duplicate := seen[evidence.EvidenceID]; duplicate {
			return ErrInvalidFeature
		}
		seen[evidence.EvidenceID] = struct{}{}
	}
	return nil
}

func featureSpecificationDigest(specification FeatureSpecification) (kernel.Digest, error) {
	raw, err := json.Marshal(specification)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return kernel.Digest(hex.EncodeToString(digest[:])), nil
}
