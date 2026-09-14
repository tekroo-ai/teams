package organization

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/tekroo-ai/teams/kernel"
)

const (
	TeamManifestSchemaVersion = "1.0.0"
	RoleBundleSchemaVersion   = "1.0.0"
	RolePackageSchemaVersion  = "2.0.0"
	maximumManifestBytes      = 1 << 20
	maximumBundleBytes        = 4 << 20
	maximumRoleResourceBytes  = 1 << 20
)

var (
	ErrInvalidTeamManifest = errors.New("invalid team manifest")
	ErrInvalidRoleBundle   = errors.New("invalid role bundle")
	namePattern            = regexp.MustCompile(`^(?:[a-z0-9]|[a-z0-9][a-z0-9-]{0,61}[a-z0-9])$`)
	versionPattern         = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
)

func validRoleName(value string) bool {
	if !namePattern.MatchString(value) {
		return false
	}
	_, err := kernel.ParseRoleFQRN(value)
	return err == nil
}

type LaunchMode string

const (
	LaunchEager    LaunchMode = "EAGER"
	LaunchOnDemand LaunchMode = "ON_DEMAND"
	LaunchManual   LaunchMode = "MANUAL"
)

func (mode LaunchMode) Valid() bool {
	return mode == LaunchEager || mode == LaunchOnDemand || mode == LaunchManual
}

type Subscription struct {
	Type    string `json:"type"`
	Purpose string `json:"purpose"`
}

type RoleBundle struct {
	SchemaVersion   string                        `json:"schema_version"`
	Role            string                        `json:"role"`
	Version         string                        `json:"version"`
	Capabilities    []string                      `json:"capabilities"`
	Subscriptions   []Subscription                `json:"subscriptions"`
	Permissions     []string                      `json:"permissions"`
	Instructions    string                        `json:"instructions,omitempty"`
	Handlers        map[string]string             `json:"handlers,omitempty"`
	Charter         *RoleResource                 `json:"charter,omitempty"`
	HandlerBindings map[string]RoleHandlerBinding `json:"handler_bindings,omitempty"`
	PublisherKeyID  string                        `json:"publisher_key_id"`
	Signature       string                        `json:"signature"`
}

type unsignedRoleBundle struct {
	SchemaVersion   string                        `json:"schema_version"`
	Role            string                        `json:"role"`
	Version         string                        `json:"version"`
	Capabilities    []string                      `json:"capabilities"`
	Subscriptions   []Subscription                `json:"subscriptions"`
	Permissions     []string                      `json:"permissions"`
	Instructions    string                        `json:"instructions,omitempty"`
	Handlers        map[string]string             `json:"handlers,omitempty"`
	Charter         *RoleResource                 `json:"charter,omitempty"`
	HandlerBindings map[string]RoleHandlerBinding `json:"handler_bindings,omitempty"`
	PublisherKeyID  string                        `json:"publisher_key_id"`
}

func (bundle RoleBundle) unsigned() unsignedRoleBundle {
	return unsignedRoleBundle{
		SchemaVersion: bundle.SchemaVersion, Role: bundle.Role, Version: bundle.Version,
		Capabilities: slices.Clone(bundle.Capabilities), Subscriptions: slices.Clone(bundle.Subscriptions),
		Permissions: slices.Clone(bundle.Permissions), Instructions: bundle.Instructions,
		Handlers: cloneMap(bundle.Handlers), Charter: cloneRoleResource(bundle.Charter),
		HandlerBindings: cloneHandlerBindings(bundle.HandlerBindings), PublisherKeyID: bundle.PublisherKeyID,
	}
}

func (bundle RoleBundle) ContentDigest() (kernel.Digest, error) {
	raw, err := json.Marshal(bundle.unsigned())
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return kernel.Digest(hex.EncodeToString(digest[:])), nil
}

func (bundle RoleBundle) Validate() error {
	if bundle.SchemaVersion != RoleBundleSchemaVersion && bundle.SchemaVersion != RolePackageSchemaVersion || !validRoleName(bundle.Role) || !versionPattern.MatchString(bundle.Version) || bundle.PublisherKeyID == "" || len(bundle.PublisherKeyID) > 256 || len(bundle.Signature) == 0 {
		return ErrInvalidRoleBundle
	}
	if !sortedUniqueNonempty(bundle.Capabilities, 128, 256) || !sortedUniqueNonempty(bundle.Permissions, 128, 256) {
		return ErrInvalidRoleBundle
	}
	if len(bundle.Subscriptions) > 256 || len(bundle.Handlers) > 256 || len(bundle.HandlerBindings) > 256 {
		return ErrInvalidRoleBundle
	}
	previous := ""
	for _, subscription := range bundle.Subscriptions {
		key := subscription.Type + "\x00" + subscription.Purpose
		if subscription.Type == "" || len(subscription.Type) > 256 || subscription.Purpose == "" || len(subscription.Purpose) > 128 || key <= previous {
			return ErrInvalidRoleBundle
		}
		previous = key
	}
	if bundle.SchemaVersion == RoleBundleSchemaVersion {
		return validateLegacyRoleBundle(bundle)
	}
	return validateRolePackage(bundle)
}

func validateLegacyRoleBundle(bundle RoleBundle) error {
	if len(bundle.Instructions) == 0 || len(bundle.Instructions) > 1<<20 || bundle.Charter != nil || len(bundle.HandlerBindings) != 0 {
		return ErrInvalidRoleBundle
	}
	for messageType, handler := range bundle.Handlers {
		if messageType == "" || len(messageType) > 256 || handler == "" || len(handler) > 65536 {
			return ErrInvalidRoleBundle
		}
	}
	return nil
}

func validateRolePackage(bundle RoleBundle) error {
	if bundle.Instructions != "" || len(bundle.Handlers) != 0 || bundle.Charter == nil || !bundle.Charter.Valid("text/markdown") || len(bundle.HandlerBindings) != len(bundle.Subscriptions) {
		return ErrInvalidRoleBundle
	}
	subscriptions := make(map[string]string, len(bundle.Subscriptions))
	for _, subscription := range bundle.Subscriptions {
		if _, duplicate := subscriptions[subscription.Type]; duplicate {
			return ErrInvalidRoleBundle
		}
		subscriptions[subscription.Type] = subscription.Purpose
	}
	for messageType, binding := range bundle.HandlerBindings {
		purpose, found := subscriptions[messageType]
		if !found || binding.SubscriptionPurpose != purpose || binding.Validate() != nil {
			return ErrInvalidRoleBundle
		}
	}
	return nil
}

type RoleBinding struct {
	Role               string        `json:"role"`
	BundlePath         string        `json:"bundle_path"`
	BundleDigest       kernel.Digest `json:"bundle_digest"`
	PublisherKeyID     string        `json:"publisher_key_id"`
	InitialInstances   uint32        `json:"initial_instances"`
	MaximumInstances   uint32        `json:"maximum_instances"`
	LaunchMode         LaunchMode    `json:"launch_mode"`
	ModelProfileDigest kernel.Digest `json:"model_profile_digest"`
	WorkspaceIDs       []string      `json:"workspace_ids"`
}

type TeamManifest struct {
	SchemaVersion string        `json:"schema_version"`
	Team          string        `json:"team"`
	Version       string        `json:"version"`
	Roles         []RoleBinding `json:"roles"`
}

func (manifest TeamManifest) Validate() error {
	if manifest.SchemaVersion != TeamManifestSchemaVersion || !namePattern.MatchString(manifest.Team) || !versionPattern.MatchString(manifest.Version) || len(manifest.Roles) == 0 || len(manifest.Roles) > 128 {
		return ErrInvalidTeamManifest
	}
	previous := ""
	for _, role := range manifest.Roles {
		if !validRoleName(role.Role) || role.Role <= previous || role.BundlePath == "" || filepath.IsAbs(role.BundlePath) || filepath.Clean(role.BundlePath) != role.BundlePath || strings.HasPrefix(role.BundlePath, "../") || !role.BundleDigest.Valid() || role.PublisherKeyID == "" || len(role.PublisherKeyID) > 256 || role.InitialInstances == 0 || role.MaximumInstances < role.InitialInstances || role.MaximumInstances > 64 || !role.LaunchMode.Valid() || !role.ModelProfileDigest.Valid() || len(role.WorkspaceIDs) != int(role.MaximumInstances) || !sortedUniqueNonempty(role.WorkspaceIDs, 64, 1024) {
			return ErrInvalidTeamManifest
		}
		for index := uint32(1); index <= role.MaximumInstances; index++ {
			if _, err := kernel.ParseActorFQN(fmt.Sprintf("%s::%s-%d", manifest.Team, role.Role, index)); err != nil {
				return ErrInvalidTeamManifest
			}
		}
		previous = role.Role
	}
	return nil
}

type LoadedRole struct {
	Binding RoleBinding
	Bundle  RoleBundle
	Package *LoadedRolePackage
}

type LoadedTeam struct {
	Manifest TeamManifest
	Roles    []LoadedRole
	Digest   kernel.Digest
}

func LoadTeamManifest(path string, expectedDigest kernel.Digest, trustedKeys map[string]ed25519.PublicKey) (LoadedTeam, error) {
	if !filepath.IsAbs(path) || !expectedDigest.Valid() || len(trustedKeys) == 0 {
		return LoadedTeam{}, ErrInvalidTeamManifest
	}
	raw, err := readBounded(path, maximumManifestBytes)
	if err != nil {
		return LoadedTeam{}, fmt.Errorf("%w: %v", ErrInvalidTeamManifest, err)
	}
	actual := sha256.Sum256(raw)
	actualDigest := kernel.Digest(hex.EncodeToString(actual[:]))
	if actualDigest != expectedDigest {
		return LoadedTeam{}, fmt.Errorf("%w: manifest digest mismatch", ErrInvalidTeamManifest)
	}
	var manifest TeamManifest
	if err := decodeStrict(raw, &manifest); err != nil || manifest.Validate() != nil {
		return LoadedTeam{}, ErrInvalidTeamManifest
	}
	base := filepath.Dir(path)
	loaded := LoadedTeam{Manifest: manifest, Digest: actualDigest, Roles: make([]LoadedRole, 0, len(manifest.Roles))}
	for _, binding := range manifest.Roles {
		bundlePath := filepath.Join(base, binding.BundlePath)
		bundle, err := LoadRoleBundle(bundlePath, binding.BundleDigest, binding.PublisherKeyID, trustedKeys)
		if err != nil || bundle.Role != binding.Role {
			return LoadedTeam{}, fmt.Errorf("%w: role %s: %v", ErrInvalidTeamManifest, binding.Role, err)
		}
		var rolePackage *LoadedRolePackage
		if bundle.SchemaVersion == RolePackageSchemaVersion {
			loadedPackage, loadErr := LoadRolePackage(bundlePath, bundle)
			if loadErr != nil {
				return LoadedTeam{}, fmt.Errorf("%w: role %s: %v", ErrInvalidTeamManifest, binding.Role, loadErr)
			}
			rolePackage = &loadedPackage
		}
		loaded.Roles = append(loaded.Roles, LoadedRole{Binding: binding, Bundle: bundle, Package: rolePackage})
	}
	return loaded, nil
}

func LoadRoleBundle(path string, expectedDigest kernel.Digest, expectedPublisher string, trustedKeys map[string]ed25519.PublicKey) (RoleBundle, error) {
	if !filepath.IsAbs(path) || !expectedDigest.Valid() || expectedPublisher == "" {
		return RoleBundle{}, ErrInvalidRoleBundle
	}
	raw, err := readBounded(path, maximumBundleBytes)
	if err != nil {
		return RoleBundle{}, fmt.Errorf("%w: %v", ErrInvalidRoleBundle, err)
	}
	var bundle RoleBundle
	if err := decodeStrict(raw, &bundle); err != nil || bundle.Validate() != nil || bundle.PublisherKeyID != expectedPublisher {
		return RoleBundle{}, ErrInvalidRoleBundle
	}
	digest, err := bundle.ContentDigest()
	if err != nil || digest != expectedDigest {
		return RoleBundle{}, fmt.Errorf("%w: bundle digest mismatch", ErrInvalidRoleBundle)
	}
	publicKey := trustedKeys[bundle.PublisherKeyID]
	if len(publicKey) != ed25519.PublicKeySize {
		return RoleBundle{}, fmt.Errorf("%w: publisher is not trusted", ErrInvalidRoleBundle)
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(bundle.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return RoleBundle{}, fmt.Errorf("%w: invalid signature encoding", ErrInvalidRoleBundle)
	}
	digestBytes, _ := hex.DecodeString(string(digest))
	if !ed25519.Verify(publicKey, digestBytes, signature) {
		return RoleBundle{}, fmt.Errorf("%w: signature verification failed", ErrInvalidRoleBundle)
	}
	return bundle, nil
}

func readBounded(path string, maximum int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || len(raw) == 0 || int64(len(raw)) > maximum {
		return nil, errors.New("file is empty, unreadable, or exceeds its bound")
	}
	return raw, nil
}

func decodeStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func sortedUniqueNonempty(values []string, maximumItems, maximumLength int) bool {
	if len(values) == 0 || len(values) > maximumItems {
		return false
	}
	for index, value := range values {
		if value == "" || len(value) > maximumLength || index > 0 && value <= values[index-1] {
			return false
		}
	}
	return true
}

func cloneMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
