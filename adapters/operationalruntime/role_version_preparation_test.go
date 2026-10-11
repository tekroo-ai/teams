package operationalruntime

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/nativeagent"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/internal/rolefixtures"
	"github.com/tekroo-ai/teams/organization"
)

type versionPreparationFixture struct {
	config           ProductionConfig
	policy           RoleVersionPolicy
	root, policyPath string
	key              ed25519.PrivateKey
	at               time.Time
}

func newVersionPreparationFixture(t *testing.T) *versionPreparationFixture {
	t.Helper()
	path, _ := writeNativeProductionFixture(t)
	config, err := LoadProductionConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f := &versionPreparationFixture{config: config, root: filepath.Dir(path), key: private, at: time.Now().UTC()}
	writeText(t, config.Organization.Publishers[0].PublicKeyFile, base64.StdEncoding.EncodeToString(public), 0600)
	var manifest organization.TeamManifest
	if err := readStrictJSONFile(config.Organization.ManifestFile, &manifest); err != nil {
		t.Fatal(err)
	}
	bundlePath := filepath.Join(f.root, manifest.Roles[0].BundlePath)
	var bundle organization.RoleBundle
	if err := readStrictJSONFile(bundlePath, &bundle); err != nil {
		t.Fatal(err)
	}
	f.sign(t, &bundle)
	writeJSON(t, bundlePath, bundle, 0600)
	f.policy = RoleVersionPolicy{SchemaVersion: RoleVersionPolicySchema, Roles: []RoleVersionRequirement{{"coder", "[1.0.0,2.0.0)"}}}
	f.policyPath = filepath.Join(f.root, "version-policy.json")
	f.add(t, "1.0.0", "qualified", "Implement bounded tasks and return evidence.")
	return f
}

func (f *versionPreparationFixture) sign(t *testing.T, bundle *organization.RoleBundle) {
	t.Helper()
	digest, err := bundle.ContentDigest()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := hex.DecodeString(string(digest))
	bundle.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(f.key, raw))
}

func (f *versionPreparationFixture) add(t *testing.T, version, eligibility, instructions string) {
	t.Helper()
	raw, _ := json.Marshal(f.config)
	var config ProductionConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	var manifest organization.TeamManifest
	if err := readStrictJSONFile(config.Organization.ManifestFile, &manifest); err != nil {
		t.Fatal(err)
	}
	var bundle organization.RoleBundle
	if err := readStrictJSONFile(filepath.Join(f.root, manifest.Roles[0].BundlePath), &bundle); err != nil {
		t.Fatal(err)
	}
	bundle.Version, bundle.Instructions = version, instructions
	f.sign(t, &bundle)
	digest, _ := bundle.ContentDigest()
	p := &config.Profiles[0]
	p.RoleBundleDigest = digest
	p.ModelProfileDigest, _ = nativeagent.ModelProfileDigest(p.RoleFQRN, digest, *p.NativeSettings)
	p.QualificationCorpus, p.Qualification = testQualificationBundle(t, p.ModelProfileDigest, "coder", p.DecisionRoute, p.ToolPolicyDigest, allTestWorkKinds(), f.at.Add(-time.Minute))
	switch eligibility {
	case "unqualified":
		p.QualificationCorpus, p.Qualification = nil, nil
	case "expired":
		p.QualificationCorpus, p.Qualification = testQualificationBundle(t, p.ModelProfileDigest, "coder", p.DecisionRoute, p.ToolPolicyDigest, allTestWorkKinds(), f.at.Add(-365*24*time.Hour))
		expiry := f.at.Add(-time.Minute)
		p.Qualification.ExpiresAt = &expiry
		p.Qualification.QualificationDigest, _ = application.QualificationDigest(*p.Qualification)
	}
	index := len(f.policy.Candidates)
	directory := filepath.Join(f.root, "catalog", version, eligibility)
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	manifest.Roles[0].BundlePath, manifest.Roles[0].BundleDigest, manifest.Roles[0].ModelProfileDigest = "role.json", digest, p.ModelProfileDigest
	writeJSON(t, filepath.Join(directory, "role.json"), bundle, 0600)
	config.Organization.ManifestFile = filepath.Join(directory, "team.json")
	writeJSON(t, config.Organization.ManifestFile, manifest, 0600)
	bytes, _ := os.ReadFile(config.Organization.ManifestFile)
	config.Organization.ManifestDigest = preparationDigest(bytes)
	path := filepath.Join(directory, "config.json")
	writeJSON(t, path, config, 0600)
	bytes, _ = os.ReadFile(path)
	f.policy.Candidates = append(f.policy.Candidates, RoleConfigurationSource{path, preparationDigest(bytes)})
	if len(f.policy.Candidates) != index+1 {
		t.Fatal("fixture catalog append failed")
	}
	writeJSON(t, f.policyPath, f.policy, 0600)
}

func (f *versionPreparationFixture) prepare(t *testing.T, name string) (ProductionConfig, RoleRunLock, error) {
	t.Helper()
	return PrepareRoleVersionRun(f.config, f.policyPath, filepath.Join(f.root, name), name, f.at)
}

func TestPrepareVersionRunSelectsHighestEligibleAndFreezesRestart(t *testing.T) {
	f := newVersionPreparationFixture(t)
	f.add(t, "1.1.0", "qualified", "compatible successor")
	f.add(t, "1.2.0", "unqualified", "not qualified")
	f.add(t, "1.3.0", "expired", "qualification expired")
	config, lock, err := f.prepare(t, "run-one")
	if err != nil {
		t.Fatal(err)
	}
	if lock.Roles[0].Version != "1.1.0" || lock.Roles[0].QualificationDigest != config.Profiles[0].Qualification.QualificationDigest {
		t.Fatalf("wrong exact selection: %+v", lock.Roles)
	}
	if _, err := LoadProductionConfig(filepath.Join(f.root, "run-one", "prepared-config.json")); err != nil {
		t.Fatal(err)
	}
	// New publication and changed central policy only affect a subsequent run.
	f.add(t, "1.4.0", "qualified", "later compatible publication")
	config2, lock2, err := f.prepare(t, "run-two")
	if err != nil {
		t.Fatal(err)
	}
	if lock2.Roles[0].Version != "1.4.0" || config2.Organization.ManifestDigest == config.Organization.ManifestDigest {
		t.Fatal("new run did not resolve the new eligible release")
	}
	if err := os.WriteFile(f.policyPath, []byte("not JSON anymore"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(f.root, "catalog")); err != nil {
		t.Fatal(err)
	} // fixture-local source removal
	restarted, err := LoadProductionConfig(filepath.Join(f.root, "run-one", "prepared-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Organization.RoleLockDigest != config.Organization.RoleLockDigest {
		t.Fatal("restart re-resolved the lock")
	}
	if _, _, err := f.prepare(t, "run-one"); err == nil {
		t.Fatal("existing output was reused")
	}
}

func TestVersionPreparationFailsClosed(t *testing.T) {
	for _, scenario := range []string{"malformed range", "no match", "missing role", "wrong digest", "invalid signature", "unknown publisher", "ambiguous publication", "unqualified", "wrong profile", "wrong tool surface", "unknown field"} {
		t.Run(scenario, func(t *testing.T) {
			f := newVersionPreparationFixture(t)
			switch scenario {
			case "malformed range":
				f.policy.Roles[0].VersionRange = "(,)"
			case "no match":
				f.policy.Roles[0].VersionRange = "2.*"
			case "missing role":
				f.policy.Roles = nil
			case "wrong digest":
				f.policy.Candidates[0].SHA256 = repeatedDigest('a')
			case "ambiguous publication":
				f.add(t, "01.0.0", "qualified", "different content at same numeric release")
			case "unknown field":
				writeText(t, f.policyPath, `{"schema_version":"tekroo.role-version-policy/1.0.0","unexpected":true}`, 0600)
			default:
				path := f.policy.Candidates[0].ConfigurationFile
				var candidate ProductionConfig
				if err := readStrictJSONFile(path, &candidate); err != nil {
					t.Fatal(err)
				}
				switch scenario {
				case "unqualified":
					candidate.Profiles[0].QualificationCorpus, candidate.Profiles[0].Qualification = nil, nil
				case "wrong profile":
					candidate.Profiles[0].ModelProfileDigest = repeatedDigest('b')
				case "wrong tool surface":
					candidate.Profiles[0].ToolPolicyDigest = repeatedDigest('b')
				default:
					bundlePath := filepath.Join(filepath.Dir(candidate.Organization.ManifestFile), "role.json")
					var bundle organization.RoleBundle
					if err := readStrictJSONFile(bundlePath, &bundle); err != nil {
						t.Fatal(err)
					}
					if scenario == "invalid signature" {
						bundle.Signature = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
					} else {
						bundle.PublisherKeyID = "untrusted"
					}
					writeJSON(t, bundlePath, bundle, 0600)
				}
				writeJSON(t, path, candidate, 0600)
				bytes, _ := os.ReadFile(path)
				f.policy.Candidates[0].SHA256 = preparationDigest(bytes)
			}
			if scenario != "unknown field" {
				writeJSON(t, f.policyPath, f.policy, 0600)
			}
			if _, _, err := f.prepare(t, "must-not-publish"); err == nil {
				t.Fatal("invalid selection was accepted")
			}
			if _, err := os.Stat(filepath.Join(f.root, "must-not-publish", "prepared-config.json")); !os.IsNotExist(err) {
				t.Fatal("failed selection published a configuration")
			}
		})
	}
}

func TestFrozenRoleLockRejectsChangedIdentityAndBytes(t *testing.T) {
	for _, scenario := range []string{"lock bytes", "version", "manifest", "run identity", "qualification", "partial binding", "signed bundle bytes"} {
		t.Run(scenario, func(t *testing.T) {
			f := newVersionPreparationFixture(t)
			config, lock, err := f.prepare(t, "frozen")
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "lock bytes":
				writeText(t, config.Organization.RoleLockFile, "tampered", 0600)
			case "version":
				lock.Roles[0].Version = "9.9.9"
			case "manifest":
				lock.ManifestDigest = repeatedDigest('a')
			case "run identity":
				config.Organization.RoleLockRunID = "different-run"
			case "qualification":
				lock.Roles[0].QualificationDigest = repeatedDigest('a')
			case "partial binding":
				config.Organization.RoleLockFile = ""
			case "signed bundle bytes":
				writeText(t, filepath.Join(f.root, "frozen", "packages", "coder", "role.json"), "tampered", 0600)
			}
			if slices.Contains([]string{"version", "manifest", "qualification"}, scenario) {
				writeJSON(t, config.Organization.RoleLockFile, lock, 0600)
				raw, _ := os.ReadFile(config.Organization.RoleLockFile)
				config.Organization.RoleLockDigest = preparationDigest(raw)
			}
			path := filepath.Join(f.root, "frozen", "prepared-config.json")
			writeJSON(t, path, config, 0600)
			if _, err := LoadProductionConfig(path); err == nil {
				t.Fatal("changed frozen identity was accepted")
			}
		})
	}
}

func TestRoleRangeDoesNotAuthorizeContractOrManualRoleChanges(t *testing.T) {
	f := newVersionPreparationFixture(t)
	keys, err := loadRolePublisherKeys(f.config.Organization.Publishers)
	if err != nil {
		t.Fatal(err)
	}
	team, err := organization.LoadTeamManifest(f.config.Organization.ManifestFile, f.config.Organization.ManifestDigest, keys)
	if err != nil {
		t.Fatal(err)
	}
	baseline := team.Roles[0]
	changed := baseline.Bundle
	changed.Capabilities = []string{"different"}
	if roleContractsCompatible(baseline.Bundle, changed) {
		t.Fatal("removed capability accepted")
	}
	p := f.config.Profiles[0]
	p.QualificationCorpus, p.Qualification = nil, nil
	if preparationProfileEligible(p, baseline.Binding, f.config.Profiles[0], f.at) {
		t.Fatal("automatic unqualified role accepted")
	}
	baseline.Binding.LaunchMode = organization.LaunchManual
	if !preparationProfileEligible(p, baseline.Binding, f.config.Profiles[0], f.at) {
		t.Fatal("unchanged manual role was not preserved")
	}
	p.RoleBundleDigest = repeatedDigest('a')
	if preparationProfileEligible(p, baseline.Binding, f.config.Profiles[0], f.at) {
		t.Fatal("unqualified manual upgrade accepted")
	}
}

func TestVersionRunSnapshotPreservesCurrentSignedResources(t *testing.T) {
	team := rolefixtures.Current(t)
	root := t.TempDir()
	for _, role := range team.Roles {
		bundlePath := filepath.Join("packages", role.Binding.Role, "role.json")
		if err := writePreparedRole(root, bundlePath, role); err != nil {
			t.Fatal(err)
		}
		var bundle organization.RoleBundle
		if err := readStrictJSONFile(filepath.Join(root, bundlePath), &bundle); err != nil {
			t.Fatal(err)
		}
		digest, err := bundle.ContentDigest()
		if err != nil || digest != role.Binding.BundleDigest || bundle.Signature != role.Bundle.Signature {
			t.Fatal("signed descriptor changed")
		}
		loaded, err := organization.LoadRolePackage(filepath.Join(root, bundlePath), bundle)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.Charter != role.Package.Charter || len(loaded.Handlers) != len(role.Package.Handlers) {
			t.Fatal("snapshot lost signed package resources")
		}
		for message, handler := range role.Package.Handlers {
			got := loaded.Handlers[message]
			if got.Instructions != handler.Instructions || string(got.InputSchema) != string(handler.InputSchema) || string(got.ResultSchema) != string(handler.ResultSchema) {
				t.Fatal("snapshot altered hash-bound handler bytes")
			}
		}
	}
}

func TestVersionPolicyDefaultOverrideAndCatalogOrder(t *testing.T) {
	f := newVersionPreparationFixture(t)
	f.add(t, "1.1.0", "qualified", "compatible successor")
	f.policy.DefaultVersionRange = "1.*"
	f.policy.Roles[0].VersionRange = ""
	writeJSON(t, f.policyPath, f.policy, 0600)
	one, lock, err := f.prepare(t, "order-one")
	if err != nil {
		t.Fatal(err)
	}
	if lock.Roles[0].Version != "1.1.0" {
		t.Fatal("central default was ignored")
	}
	slices.Reverse(f.policy.Candidates)
	writeJSON(t, f.policyPath, f.policy, 0600)
	two, _, err := f.prepare(t, "order-two")
	if err != nil {
		t.Fatal(err)
	}
	if one.Organization.ManifestDigest != two.Organization.ManifestDigest {
		t.Fatal("catalog ordering changed exact selection")
	}
	f.policy.Roles[0].VersionRange = "[1.0.0]"
	writeJSON(t, f.policyPath, f.policy, 0600)
	_, exact, err := f.prepare(t, "exact-override")
	if err != nil {
		t.Fatal(err)
	}
	if exact.Roles[0].Version != "1.0.0" {
		t.Fatal("per-role override did not override central default")
	}
}

func TestVersionSelectionRetainsQualifiedCoverageAndRoute(t *testing.T) {
	f := newVersionPreparationFixture(t)
	baseline := f.config.Profiles[0]
	var manifest organization.TeamManifest
	if err := readStrictJSONFile(f.config.Organization.ManifestFile, &manifest); err != nil {
		t.Fatal(err)
	}
	candidate := baseline
	candidate.QualificationCorpus, candidate.Qualification = testQualificationBundle(t, candidate.ModelProfileDigest, candidate.RoleFQRN, candidate.DecisionRoute, candidate.ToolPolicyDigest, allTestWorkKinds()[:1], f.at.Add(-time.Minute))
	if preparationProfileEligible(candidate, manifest.Roles[0], baseline, f.at) {
		t.Fatal("narrower qualification replaced full template coverage")
	}
	candidate = baseline
	if !preparationProfileEligible(candidate, manifest.Roles[0], baseline, f.at) {
		t.Fatal("exact eligible coverage was rejected")
	}
}
