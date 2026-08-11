package kernel

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

type SourceIdentity struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	TreeDigest Digest `json:"tree_digest"`
	Scope      string `json:"scope"`
}

type OverlayIdentity struct {
	SourceTreeDigest Digest `json:"source_tree_digest"`
	ChangeDigest     Digest `json:"change_digest"`
}

type BuildIdentity struct {
	SourceTreeDigest      Digest `json:"source_tree_digest"`
	OverlayDigest         Digest `json:"overlay_digest"`
	DependencyLockDigest  Digest `json:"dependency_lock_digest"`
	ToolchainDigest       Digest `json:"toolchain_digest"`
	BuildDefinitionDigest Digest `json:"build_definition_digest"`
	ArtifactDigest        Digest `json:"artifact_digest"`
}

type RuntimeIdentity struct {
	BuildArtifactDigest Digest   `json:"build_artifact_digest"`
	ContractManifest    string   `json:"contract_manifest"`
	ConfigurationDigest Digest   `json:"configuration_digest"`
	EnvironmentDigest   Digest   `json:"environment_digest"`
	RoleLibraryDigests  []Digest `json:"role_library_digests"`
	Capabilities        []string `json:"capabilities"`
	ProviderIdentities  []string `json:"provider_identities"`
}

type ProvenanceBasis struct {
	CatalogueDigest   Digest          `json:"catalogue_digest"`
	PolicyDigest      Digest          `json:"policy_digest"`
	PolicyRevision    uint64          `json:"policy_revision"`
	GrantDigests      []Digest        `json:"grant_digests"`
	DelegationDigests []Digest        `json:"delegation_digests"`
	Source            SourceIdentity  `json:"source"`
	Overlay           OverlayIdentity `json:"overlay"`
	Build             BuildIdentity   `json:"build"`
	Runtime           RuntimeIdentity `json:"runtime"`
}

type DecisionProvenance struct {
	ContractManifest   string          `json:"contract_manifest"`
	CatalogueDigest    Digest          `json:"catalogue_digest"`
	CommandID          UUIDv7          `json:"command_id"`
	CommandFingerprint Digest          `json:"command_fingerprint"`
	Principal          PrincipalRef    `json:"principal"`
	PolicyDigest       Digest          `json:"policy_digest"`
	PolicyRevision     uint64          `json:"policy_revision"`
	GrantDigests       []Digest        `json:"grant_digests"`
	DelegationDigests  []Digest        `json:"delegation_digests"`
	ActorFQN           *ActorFQN       `json:"actor_fqn"`
	Execution          *ExecutionTuple `json:"execution"`
	Parents            []DagParent     `json:"parents"`
	EvidenceRefs       []EvidenceRef   `json:"evidence_refs"`
	Source             SourceIdentity  `json:"source"`
	Overlay            OverlayIdentity `json:"overlay"`
	Build              BuildIdentity   `json:"build"`
	Runtime            RuntimeIdentity `json:"runtime"`
	ReceivedAt         time.Time       `json:"received_at"`
	DecidedAt          time.Time       `json:"decided_at"`
}

func (basis ProvenanceBasis) Valid() bool {
	if !basis.CatalogueDigest.Valid() || !basis.PolicyDigest.Valid() || basis.PolicyRevision == 0 || !basis.Source.Valid() || !basis.Overlay.Valid() || !basis.Build.Valid() || !basis.Runtime.Valid() {
		return false
	}
	for _, digest := range basis.GrantDigests {
		if !digest.Valid() {
			return false
		}
	}
	for _, digest := range basis.DelegationDigests {
		if !digest.Valid() {
			return false
		}
	}
	if basis.Overlay.SourceTreeDigest != basis.Source.TreeDigest || basis.Build.SourceTreeDigest != basis.Source.TreeDigest || basis.Runtime.BuildArtifactDigest != basis.Build.ArtifactDigest || basis.Runtime.ContractManifest != ContractIdentity {
		return false
	}
	overlayDigest, err := digestValue(basis.Overlay)
	return err == nil && basis.Build.OverlayDigest == overlayDigest
}

func (identity SourceIdentity) Valid() bool {
	return identity.Repository != "" && identity.Commit != "" && identity.Scope != "" && identity.TreeDigest.Valid()
}

func (identity OverlayIdentity) Valid() bool {
	return identity.SourceTreeDigest.Valid() && identity.ChangeDigest.Valid()
}

func (identity OverlayIdentity) Digest() (Digest, error) {
	return digestValue(identity)
}

func (identity BuildIdentity) Valid() bool {
	return identity.SourceTreeDigest.Valid() && identity.OverlayDigest.Valid() && identity.DependencyLockDigest.Valid() && identity.ToolchainDigest.Valid() && identity.BuildDefinitionDigest.Valid() && identity.ArtifactDigest.Valid()
}

func (identity RuntimeIdentity) Valid() bool {
	if !identity.BuildArtifactDigest.Valid() || identity.ContractManifest != ContractIdentity || !identity.ConfigurationDigest.Valid() || !identity.EnvironmentDigest.Valid() {
		return false
	}
	for _, digest := range identity.RoleLibraryDigests {
		if !digest.Valid() {
			return false
		}
	}
	return true
}

func BuildDecisionProvenance(command KernelCommand, fingerprint Digest, context DecisionContext) (DecisionProvenance, Digest, error) {
	if !basisAndContextValid(context) || !fingerprint.Valid() {
		return DecisionProvenance{}, "", errors.New("invalid provenance basis")
	}
	record := DecisionProvenance{
		ContractManifest: command.ContractManifest, CatalogueDigest: context.Provenance.CatalogueDigest,
		CommandID: command.CommandID, CommandFingerprint: fingerprint, Principal: command.Authority,
		PolicyDigest: context.Provenance.PolicyDigest, PolicyRevision: context.Provenance.PolicyRevision,
		GrantDigests: append([]Digest(nil), context.Provenance.GrantDigests...), DelegationDigests: append([]Digest(nil), context.Provenance.DelegationDigests...),
		ActorFQN: cloneActor(command.ActorFQN), Execution: cloneExecution(command.Execution),
		Parents: canonicalParents(command.Causation), EvidenceRefs: append([]EvidenceRef(nil), command.EvidenceRefs...),
		Source: context.Provenance.Source, Overlay: context.Provenance.Overlay, Build: context.Provenance.Build, Runtime: context.Provenance.Runtime,
		ReceivedAt: context.ReceivedAt, DecidedAt: context.DecidedAt,
	}
	digest, err := digestValue(record)
	return record, digest, err
}

func (record DecisionProvenance) Digest() (Digest, error) {
	return digestValue(record)
}

func (record DecisionProvenance) Valid() bool {
	basis := ProvenanceBasis{
		CatalogueDigest: record.CatalogueDigest, PolicyDigest: record.PolicyDigest, PolicyRevision: record.PolicyRevision,
		GrantDigests: record.GrantDigests, DelegationDigests: record.DelegationDigests, Source: record.Source, Overlay: record.Overlay,
		Build: record.Build, Runtime: record.Runtime,
	}
	if record.ContractManifest != ContractIdentity || !record.CommandID.Valid() || !record.CommandFingerprint.Valid() || !record.Principal.Valid() || !basis.Valid() || record.ReceivedAt.IsZero() || record.DecidedAt.IsZero() || record.DecidedAt.Before(record.ReceivedAt) {
		return false
	}
	if record.ActorFQN != nil && !record.ActorFQN.Valid() {
		return false
	}
	if record.Execution != nil && (record.ActorFQN == nil || !record.Execution.Valid()) {
		return false
	}
	for _, parent := range record.Parents {
		if !parent.ParentEventID.Valid() || !parent.EdgeKind.Valid() {
			return false
		}
	}
	for _, evidence := range record.EvidenceRefs {
		if !evidence.EvidenceID.Valid() || !evidence.SHA256.Valid() {
			return false
		}
	}
	return true
}

func digestValue(value any) (Digest, error) {
	canonical, err := canonicalJSON(value)
	if err != nil {
		return "", err
	}
	digest, err := DigestBytes(canonical)
	if err != nil {
		return "", err
	}
	return digest, nil
}

func DigestBytes(value []byte) (Digest, error) {
	if value == nil {
		return "", errors.New("nil content")
	}
	digest := sha256.Sum256(value)
	return Digest(hex.EncodeToString(digest[:])), nil
}

func basisAndContextValid(context DecisionContext) bool {
	return context.Provenance.Valid() && !context.ReceivedAt.IsZero() && !context.DecidedAt.IsZero() && !context.DecidedAt.Before(context.ReceivedAt)
}
