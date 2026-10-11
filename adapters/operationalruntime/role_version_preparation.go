package operationalruntime

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tekroo-ai/teams/adapters/nativeagent"
	"github.com/tekroo-ai/teams/adapters/openhands"
	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

const RoleVersionPolicySchema = "tekroo.role-version-policy/1.0.0"
const RoleRunLockSchema = "tekroo.role-run-lock/1.0.0"

// The catalog is an explicit allowlist of exact previously qualified config
// publications, not a directory scan or a request to qualify a new model.
type RoleVersionPolicy struct {
	SchemaVersion       string                    `json:"schema_version"`
	DefaultVersionRange string                    `json:"default_version_range,omitempty"`
	Roles               []RoleVersionRequirement  `json:"roles"`
	Candidates          []RoleConfigurationSource `json:"candidates"`
}
type RoleVersionRequirement struct {
	Role         string `json:"role"`
	VersionRange string `json:"version_range,omitempty"`
}
type RoleConfigurationSource struct {
	ConfigurationFile string        `json:"configuration_file"`
	SHA256            kernel.Digest `json:"sha256"`
}
type LockedRunRole struct {
	Role                      string        `json:"role"`
	Version                   string        `json:"version"`
	BundleDigest              kernel.Digest `json:"bundle_digest"`
	PublisherKeyID            string        `json:"publisher_key_id"`
	ModelProfileDigest        kernel.Digest `json:"model_profile_digest"`
	QualificationDigest       kernel.Digest `json:"qualification_digest,omitempty"`
	SourceConfigurationDigest kernel.Digest `json:"source_configuration_digest"`
}
type RoleRunLock struct {
	SchemaVersion  string          `json:"schema_version"`
	RunID          string          `json:"run_id"`
	PreparedAt     time.Time       `json:"prepared_at"`
	PolicyDigest   kernel.Digest   `json:"policy_digest"`
	ManifestDigest kernel.Digest   `json:"manifest_digest"`
	Roles          []LockedRunRole `json:"roles"`
}
type preparedRole struct {
	role    organization.LoadedRole
	profile ProductionProfile
	source  kernel.Digest
}

func loadRolePublisherKeys(publishers []ProductionPublisher) (map[string]ed25519.PublicKey, error) {
	keys := make(map[string]ed25519.PublicKey, len(publishers))
	for _, publisher := range publishers {
		if publisher.KeyID == "" || publisher.PublicKeyFile == "" || keys[publisher.KeyID] != nil {
			return nil, invalidConfig("trusted role publishers are invalid or duplicated")
		}
		raw, err := os.ReadFile(publisher.PublicKeyFile)
		if err != nil {
			return nil, invalidConfig("trusted role publisher key is missing")
		}
		decoded, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(raw)))
		if err != nil || len(decoded) != ed25519.PublicKeySize {
			return nil, invalidConfig("trusted role publisher key is invalid")
		}
		keys[publisher.KeyID] = ed25519.PublicKey(decoded)
	}
	return keys, nil
}

func preparationDigest(raw []byte) kernel.Digest {
	sum := sha256.Sum256(raw)
	return kernel.Digest(hex.EncodeToString(sum[:]))
}

func readPreparationDocument(path string) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, invalidConfig("preparation path must be absolute")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximumConfigBytes {
		return nil, invalidConfig("preparation document is missing or exceeds its bound")
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 || len(raw) > maximumConfigBytes {
		return nil, invalidConfig("preparation document is unreadable or exceeds its bound")
	}
	return raw, nil
}

// PrepareRoleVersionRunFromFile shares strict configuration decoding while
// leaving authentication to PrepareRoleVersionRun, avoiding a second full
// load of the template's signed resources at the CLI boundary.
func PrepareRoleVersionRunFromFile(configPath, policyPath, output, runID string, at time.Time) (ProductionConfig, RoleRunLock, error) {
	raw, err := readPreparationDocument(configPath)
	if err != nil {
		return ProductionConfig{}, RoleRunLock{}, err
	}
	config, err := decodeProductionConfigBytes(raw, configPath)
	if err != nil {
		return ProductionConfig{}, RoleRunLock{}, err
	}
	return PrepareRoleVersionRun(config, policyPath, output, runID, at)
}

// PrepareRoleVersionRun is offline: no dispatch, model calls, database writes,
// new qualifications, or changes to existing configurations. Only a fresh
// output directory is published. Failures leave an incomplete directory that
// cannot be loaded/reused; they never overwrite an earlier run's lock.
func PrepareRoleVersionRun(config ProductionConfig, policyPath, output, runID string, at time.Time) (ProductionConfig, RoleRunLock, error) {
	if !filepath.IsAbs(output) || runID == "" || len(runID) > 256 || strings.TrimSpace(runID) != runID || at.IsZero() {
		return ProductionConfig{}, RoleRunLock{}, invalidConfig("invalid role preparation identity")
	}
	resolved, err := resolveProductionConfig(config)
	if err != nil {
		return ProductionConfig{}, RoleRunLock{}, err
	}
	keys, template := resolved.trustedRolePublishers, resolved.team
	raw, err := readPreparationDocument(policyPath)
	if err != nil {
		return ProductionConfig{}, RoleRunLock{}, err
	}
	var policy RoleVersionPolicy
	if err := decodeStrict(raw, &policy); err != nil || policy.SchemaVersion != RoleVersionPolicySchema || len(policy.Roles) != len(template.Roles) || len(policy.Candidates) == 0 || len(policy.Candidates) > 128 {
		return ProductionConfig{}, RoleRunLock{}, invalidConfig("invalid role version policy")
	}
	ranges := make(map[string]organization.VersionRange, len(policy.Roles))
	if policy.DefaultVersionRange != "" {
		if _, err := organization.ParseVersionRange(policy.DefaultVersionRange); err != nil {
			return ProductionConfig{}, RoleRunLock{}, err
		}
	}
	for i, requirement := range policy.Roles {
		constraint := requirement.VersionRange
		if constraint == "" {
			constraint = policy.DefaultVersionRange
		}
		r, err := organization.ParseVersionRange(constraint)
		if err != nil {
			return ProductionConfig{}, RoleRunLock{}, fmt.Errorf("role %s version range: %w", requirement.Role, err)
		}
		if requirement.Role != template.Roles[i].Binding.Role {
			return ProductionConfig{}, RoleRunLock{}, invalidConfig("role requirements must exactly cover the sorted template roles")
		}
		ranges[requirement.Role] = r
	}
	selected := make(map[string]preparedRole, len(ranges))
	baselineProfiles := make(map[string]ProductionProfile, len(config.Profiles))
	for _, p := range config.Profiles {
		baselineProfiles[string(p.RoleFQRN)] = p
	}
	publications := make(map[string]preparedRole)
	seenSources := make(map[kernel.Digest]bool)
	for _, source := range policy.Candidates {
		if !source.SHA256.Valid() || seenSources[source.SHA256] || source.ConfigurationFile == "" {
			return ProductionConfig{}, RoleRunLock{}, invalidConfig("invalid or duplicate role catalog source")
		}
		seenSources[source.SHA256] = true
		path := absoluteFrom(filepath.Dir(policyPath), source.ConfigurationFile)
		bytes, err := readPreparationDocument(path)
		if err != nil || preparationDigest(bytes) != source.SHA256 {
			return ProductionConfig{}, RoleRunLock{}, invalidConfig("role catalog configuration digest mismatch")
		}
		candidate, err := decodeProductionConfigBytes(bytes, path)
		if err != nil || candidate.ExecutionBackend != config.ExecutionBackend {
			return ProductionConfig{}, RoleRunLock{}, invalidConfig("role catalog execution backend mismatch")
		}
		// Use only the template's trusted keys. A candidate cannot grant itself
		// trust by adding a publisher to its own configuration.
		team, err := organization.LoadTeamManifest(candidate.Organization.ManifestFile, candidate.Organization.ManifestDigest, keys)
		if err != nil {
			return ProductionConfig{}, RoleRunLock{}, fmt.Errorf("authenticate role catalog: %w", err)
		}
		if err := validateProductionRoleLock(candidate, team); err != nil {
			return ProductionConfig{}, RoleRunLock{}, err
		}
		if len(candidate.Profiles) != len(team.Roles) {
			return ProductionConfig{}, RoleRunLock{}, invalidConfig("catalog profiles do not exactly cover its roles")
		}
		profiles := make(map[string]ProductionProfile)
		for _, p := range candidate.Profiles {
			if _, duplicate := profiles[string(p.RoleFQRN)]; duplicate {
				return ProductionConfig{}, RoleRunLock{}, invalidConfig("duplicate catalog profile role")
			}
			profiles[string(p.RoleFQRN)] = p
		}
		for _, role := range team.Roles {
			p, ok := profiles[role.Binding.Role]
			if !ok || p.ModelProfileDigest != role.Binding.ModelProfileDigest || p.RoleBundleDigest != role.Binding.BundleDigest {
				return ProductionConfig{}, RoleRunLock{}, invalidConfig("catalog profile does not bind signed role")
			}
			if _, err := validateConfiguredExecutionProfile(config, p); err != nil {
				return ProductionConfig{}, RoleRunLock{}, err
			}
			if err := validateSignedProfileBinding(config.ExecutionBackend, p, role); err != nil {
				return ProductionConfig{}, RoleRunLock{}, err
			}
			item := preparedRole{role, p, source.SHA256}
			// Numeric aliases (01.0.0 and 1.0.0) cannot hide a publication conflict.
			for _, prior := range publications {
				if prior.role.Binding.Role != role.Binding.Role {
					continue
				}
				c, _ := organization.CompareRoleVersions(prior.role.Bundle.Version, role.Bundle.Version)
				if c == 0 && (prior.role.Binding.BundleDigest != role.Binding.BundleDigest || prior.role.Binding.PublisherKeyID != role.Binding.PublisherKeyID || prior.profile.ModelProfileDigest != p.ModelProfileDigest) {
					return ProductionConfig{}, RoleRunLock{}, invalidConfig("ambiguous role version publication")
				}
			}
			publicationKey := role.Binding.Role + ":" + role.Bundle.Version
			publications[publicationKey] = item
			rangeForRole, wanted := ranges[role.Binding.Role]
			if !wanted || !rangeForRole.Contains(role.Bundle.Version) {
				continue
			}
			index := slices.IndexFunc(template.Roles, func(r organization.LoadedRole) bool { return r.Binding.Role == role.Binding.Role })
			baseline := template.Roles[index]
			if !roleContractsCompatible(baseline.Bundle, role.Bundle) {
				continue
			}
			if !preparationProfileEligible(p, baseline.Binding, baselineProfiles[role.Binding.Role], at) {
				continue
			}
			prior, exists := selected[role.Binding.Role]
			comparison := 1
			if exists {
				comparison, _ = organization.CompareRoleVersions(role.Bundle.Version, prior.role.Bundle.Version)
			}
			if !exists || comparison > 0 || comparison == 0 && source.SHA256 < prior.source {
				selected[role.Binding.Role] = item
			}
		}
	}
	for _, role := range template.Roles {
		if _, ok := selected[role.Binding.Role]; !ok {
			return ProductionConfig{}, RoleRunLock{}, invalidConfig(fmt.Sprintf("role %s: no compatible, exactly qualified version satisfies its range", role.Binding.Role))
		}
	}
	manifest := template.Manifest
	manifest.Roles = slices.Clone(template.Manifest.Roles)
	lock := RoleRunLock{SchemaVersion: RoleRunLockSchema, RunID: runID, PreparedAt: at.UTC(), PolicyDigest: preparationDigest(raw), Roles: make([]LockedRunRole, 0, len(selected))}
	config.Profiles = make([]ProductionProfile, 0, len(selected))
	if err := os.Mkdir(output, 0700); err != nil {
		return ProductionConfig{}, RoleRunLock{}, fmt.Errorf("create fresh role lock directory: %w", err)
	}
	for i := range manifest.Roles {
		binding := &manifest.Roles[i]
		item := selected[binding.Role]
		binding.BundlePath = filepath.Join("packages", binding.Role, "role.json")
		binding.BundleDigest, binding.PublisherKeyID, binding.ModelProfileDigest = item.role.Binding.BundleDigest, item.role.Binding.PublisherKeyID, item.profile.ModelProfileDigest
		if err := writePreparedRole(output, binding.BundlePath, item.role); err != nil {
			return ProductionConfig{}, RoleRunLock{}, err
		}
		config.Profiles = append(config.Profiles, item.profile)
		lock.Roles = append(lock.Roles, LockedRunRole{binding.Role, item.role.Bundle.Version, binding.BundleDigest, binding.PublisherKeyID, binding.ModelProfileDigest, profileQualificationDigest(item.profile), item.source})
	}
	manifestBytes, err := marshalPreparation(manifest)
	if err != nil {
		return ProductionConfig{}, RoleRunLock{}, err
	}
	lock.ManifestDigest = preparationDigest(manifestBytes)
	config.Organization.ManifestFile, config.Organization.ManifestDigest = filepath.Join(output, "team.json"), lock.ManifestDigest
	if err := writePreparationFile(config.Organization.ManifestFile, manifestBytes); err != nil {
		return ProductionConfig{}, RoleRunLock{}, err
	}
	// Re-authenticate the snapshot, not just the original source, before publishing.
	team, err := organization.LoadTeamManifest(config.Organization.ManifestFile, lock.ManifestDigest, keys)
	if err != nil {
		return ProductionConfig{}, RoleRunLock{}, err
	}
	lockBytes, err := marshalPreparation(lock)
	if err != nil {
		return ProductionConfig{}, RoleRunLock{}, err
	}
	config.Organization.RoleLockFile, config.Organization.RoleLockDigest, config.Organization.RoleLockRunID = filepath.Join(output, "role-lock.json"), preparationDigest(lockBytes), runID
	if err := writePreparationFile(config.Organization.RoleLockFile, lockBytes); err != nil {
		return ProductionConfig{}, RoleRunLock{}, err
	}
	if err := validateProductionRoleLock(config, team); err != nil {
		return ProductionConfig{}, RoleRunLock{}, err
	}
	if err := writePreparationFile(filepath.Join(output, "version-policy.json"), raw); err != nil {
		return ProductionConfig{}, RoleRunLock{}, err
	}
	if _, err := resolveProductionConfig(config); err != nil {
		return ProductionConfig{}, RoleRunLock{}, err
	}
	configBytes, err := marshalPreparation(config)
	if err != nil {
		return ProductionConfig{}, RoleRunLock{}, err
	}
	if err := writePreparationFile(filepath.Join(output, "prepared-config.json"), configBytes); err != nil {
		return ProductionConfig{}, RoleRunLock{}, err
	}
	return config, lock, nil
}

func roleContractsCompatible(baseline, candidate organization.RoleBundle) bool {
	// Range membership is not compatibility: retain every configured capability,
	// permission and message contract. Exact model qualification remains separate.
	for _, c := range baseline.Capabilities {
		if !slices.Contains(candidate.Capabilities, c) {
			return false
		}
	}
	for _, p := range baseline.Permissions {
		if !slices.Contains(candidate.Permissions, p) {
			return false
		}
	}
	for _, subscription := range baseline.Subscriptions {
		if !slices.Contains(candidate.Subscriptions, subscription) {
			return false
		}
	}
	for message, handler := range baseline.HandlerBindings {
		other, ok := candidate.HandlerBindings[message]
		if !ok || other.SubscriptionPurpose != handler.SubscriptionPurpose || other.MessagePurpose != handler.MessagePurpose || other.Disposition != handler.Disposition {
			return false
		}
		for _, result := range handler.AllowedResults {
			if !slices.Contains(other.AllowedResults, result) {
				return false
			}
		}
		for _, proposal := range handler.AllowedMessageProposals {
			if !slices.Contains(other.AllowedMessageProposals, proposal) {
				return false
			}
		}
	}
	return baseline.SchemaVersion == candidate.SchemaVersion
}

// Shared by preparation and daemon configuration loading: resolution must not
// admit a model/settings/tool identity that runtime would reject.
func validateConfiguredExecutionProfile(config ProductionConfig, profile ProductionProfile) (openhands.ExecutionProfile, error) {
	var resolved openhands.ExecutionProfile
	if config.ExecutionBackend == "native" {
		if profile.NativeSettings == nil || len(profile.AgentSettings) != 0 || profile.MaximumIterations != 0 || !profile.RuntimeIdentityDigest.Valid() || !profile.ToolPolicyDigest.Valid() || !profile.EffectPolicyDigest.Valid() {
			return resolved, invalidConfig("native execution profile is invalid")
		}
		digest, err := nativeagent.ModelProfileDigest(profile.RoleFQRN, profile.RoleBundleDigest, *profile.NativeSettings)
		if err != nil || digest != profile.ModelProfileDigest || !profile.DecisionRoute.ModelExecutable() {
			return resolved, invalidConfig("native execution profile digest is invalid")
		}
	} else {
		var err error
		resolved, err = openhands.NewBoundExecutionProfile(profile.ModelProfileDigest, profile.RoleFQRN, profile.RoleBundleDigest, profile.RuntimeIdentityDigest, profile.ToolPolicyDigest, profile.EffectPolicyDigest, profile.AgentSettings, profile.MaximumIterations, config.TeamsDatabaseIdentity, config.SMADatabaseIdentity)
		if profile.NativeSettings != nil || err != nil || !profile.DecisionRoute.ModelExecutable() {
			return resolved, invalidConfig("execution profile is invalid")
		}
	}
	if (profile.Qualification == nil) != (profile.QualificationCorpus == nil) || profile.Qualification != nil && !profile.qualificationDefinitionValid() {
		return resolved, invalidConfig("execution profile is invalid")
	}
	return resolved, nil
}

func validateSignedProfileBinding(backend string, profile ProductionProfile, role organization.LoadedRole) error {
	if string(profile.RoleFQRN) != role.Binding.Role || profile.ModelProfileDigest != role.Binding.ModelProfileDigest || profile.RoleBundleDigest != role.Binding.BundleDigest {
		return invalidConfig("execution profile does not bind the signed team role")
	}
	if backend == "native" {
		surface, err := agenttools.NativeToolSurfaceDigest(role.Bundle.Permissions)
		if err != nil || surface != profile.ToolPolicyDigest {
			return invalidConfig("native execution profile does not bind the signed role tool surface")
		}
	}
	return nil
}

func preparationProfileEligible(p ProductionProfile, baseline organization.RoleBinding, baselineProfile ProductionProfile, at time.Time) bool {
	if p.Qualification == nil && p.QualificationCorpus == nil {
		// Preserve an explicitly manual, unqualified binding; never silently
		// upgrade it or convert it into an automatically eligible profile.
		return baseline.LaunchMode == organization.LaunchManual && p.RoleBundleDigest == baseline.BundleDigest && p.ModelProfileDigest == baseline.ModelProfileDigest
	}
	if !p.qualificationDefinitionValid() || !p.Qualification.EligibleAt(at) || !p.DecisionRoute.Satisfies(baselineProfile.DecisionRoute) {
		return false
	}
	if baselineProfile.Qualification != nil {
		for _, kind := range baselineProfile.Qualification.QualifiedWorkKinds {
			if !slices.Contains(p.Qualification.QualifiedWorkKinds, kind) {
				return false
			}
		}
	}
	return true
}

func profileQualificationDigest(p ProductionProfile) kernel.Digest {
	if p.Qualification == nil {
		return ""
	}
	return p.Qualification.QualificationDigest
}

func marshalPreparation(value any) ([]byte, error) {
	raw, err := json.MarshalIndent(value, "", "  ")
	return append(raw, '\n'), err
}
func writePreparationFile(path string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(raw)
	return errors.Join(writeErr, file.Sync(), file.Close())
}
func writePreparedRole(root, bundlePath string, role organization.LoadedRole) error {
	base := filepath.Dir(filepath.Join(root, bundlePath))
	resources := make(map[string][]byte)
	add := func(resource *organization.RoleResource, raw []byte) error {
		if resource == nil {
			return nil
		}
		if preparationDigest(raw) != resource.SHA256 {
			return invalidConfig("snapshot resource digest mismatch")
		}
		if previous, exists := resources[resource.Path]; exists && preparationDigest(previous) != resource.SHA256 {
			return invalidConfig("conflicting snapshot resource paths")
		}
		resources[resource.Path] = raw
		return nil
	}
	if role.Package != nil {
		if err := add(role.Bundle.Charter, []byte(role.Package.Charter)); err != nil {
			return err
		}
		for _, h := range role.Package.Handlers {
			if err := add(h.Binding.Resource, []byte(h.Instructions)); err != nil {
				return err
			}
			if err := add(h.Binding.InputSchema, h.InputSchema); err != nil {
				return err
			}
			if err := add(h.Binding.ResultSchema, h.ResultSchema); err != nil {
				return err
			}
		}
	}
	for path, raw := range resources {
		if err := writePreparationFile(filepath.Join(base, path), raw); err != nil {
			return err
		}
	}
	raw, err := marshalPreparation(role.Bundle)
	if err != nil {
		return err
	}
	return writePreparationFile(filepath.Join(root, bundlePath), raw)
}

func validateProductionRoleLock(config ProductionConfig, team organization.LoadedTeam) error {
	o := config.Organization
	if o.RoleLockFile == "" && o.RoleLockDigest == "" && o.RoleLockRunID == "" {
		return nil
	} // historical exact configurations
	if o.RoleLockFile == "" || !o.RoleLockDigest.Valid() || o.RoleLockRunID == "" {
		return invalidConfig("incomplete role run lock binding")
	}
	raw, err := readPreparationDocument(o.RoleLockFile)
	if err != nil || preparationDigest(raw) != o.RoleLockDigest {
		return invalidConfig("role run lock digest mismatch")
	}
	var lock RoleRunLock
	if err := decodeStrict(raw, &lock); err != nil || lock.SchemaVersion != RoleRunLockSchema || lock.RunID != o.RoleLockRunID || lock.PreparedAt.IsZero() || !lock.PolicyDigest.Valid() || lock.ManifestDigest != team.Digest || len(lock.Roles) != len(team.Roles) {
		return invalidConfig("role run lock identity mismatch")
	}
	for i, role := range team.Roles {
		l := lock.Roles[i]
		index := slices.IndexFunc(config.Profiles, func(p ProductionProfile) bool { return string(p.RoleFQRN) == role.Binding.Role })
		if index < 0 || l.Role != role.Binding.Role || l.Version != role.Bundle.Version || l.BundleDigest != role.Binding.BundleDigest || l.PublisherKeyID != role.Binding.PublisherKeyID || l.ModelProfileDigest != role.Binding.ModelProfileDigest || l.QualificationDigest != profileQualificationDigest(config.Profiles[index]) || !l.SourceConfigurationDigest.Valid() {
			return invalidConfig("signed role or profile differs from its frozen run lock")
		}
	}
	return nil
}
