package organization

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tekroo-ai/teams/kernel"
)

func TestLoadTeamManifestVerifiesExactBundleAndSignature(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bundle := testBundle()
	bundle.PublisherKeyID = "test-publisher"
	digest, err := bundle.ContentDigest()
	if err != nil {
		t.Fatal(err)
	}
	digestBytes, _ := hex.DecodeString(string(digest))
	bundle.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, digestBytes))

	root := t.TempDir()
	writeJSON(t, filepath.Join(root, "coder.json"), bundle)
	manifest := TeamManifest{
		SchemaVersion: TeamManifestSchemaVersion,
		Team:          "teams",
		Version:       "1.0.0",
		Roles: []RoleBinding{{
			Role: "coder", BundlePath: "coder.json", BundleDigest: digest,
			PublisherKeyID: "test-publisher", InitialInstances: 1, MaximumInstances: 2,
			LaunchMode: LaunchOnDemand, ModelProfileDigest: kernel.Digest(repeat("a", 64)),
			WorkspaceIDs: []string{"coder-1", "coder-2"},
		}},
	}
	manifestPath := filepath.Join(root, "team.json")
	manifestRaw := writeJSON(t, manifestPath, manifest)
	manifestHash := sha256.Sum256(manifestRaw)
	loaded, err := LoadTeamManifest(manifestPath, kernel.Digest(hex.EncodeToString(manifestHash[:])), map[string]ed25519.PublicKey{"test-publisher": publicKey})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Manifest.Team != "teams" || len(loaded.Roles) != 1 || loaded.Roles[0].Bundle.Role != "coder" {
		t.Fatalf("unexpected loaded team: %+v", loaded)
	}
}

func TestLoadRoleBundleFailsClosed(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bundle := testBundle()
	bundle.PublisherKeyID = "test-publisher"
	digest, _ := bundle.ContentDigest()
	digestBytes, _ := hex.DecodeString(string(digest))
	bundle.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, digestBytes))
	path := filepath.Join(t.TempDir(), "bundle.json")
	writeJSON(t, path, bundle)

	tests := []struct {
		name      string
		digest    kernel.Digest
		publisher string
		keys      map[string]ed25519.PublicKey
	}{
		{name: "wrong digest", digest: kernel.Digest(repeat("b", 64)), publisher: "test-publisher", keys: map[string]ed25519.PublicKey{"test-publisher": publicKey}},
		{name: "wrong publisher", digest: digest, publisher: "other", keys: map[string]ed25519.PublicKey{"test-publisher": publicKey}},
		{name: "untrusted", digest: digest, publisher: "test-publisher", keys: map[string]ed25519.PublicKey{"test-publisher": make(ed25519.PublicKey, ed25519.PublicKeySize)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := LoadRoleBundle(path, test.digest, test.publisher, test.keys); err == nil {
				t.Fatal("expected failure")
			}
		})
	}
}

func TestManifestRejectsUnsortedOrUnsafeBindings(t *testing.T) {
	manifest := TeamManifest{SchemaVersion: TeamManifestSchemaVersion, Team: "teams", Version: "1.0.0", Roles: []RoleBinding{
		{Role: "coder", BundlePath: "../coder.json", BundleDigest: kernel.Digest(repeat("a", 64)), PublisherKeyID: "publisher", InitialInstances: 1, MaximumInstances: 1, LaunchMode: LaunchEager, ModelProfileDigest: kernel.Digest(repeat("b", 64)), WorkspaceIDs: []string{"coder-1"}},
	}}
	if err := manifest.Validate(); err == nil {
		t.Fatal("expected unsafe bundle path rejection")
	}
	manifest.Roles[0].BundlePath = "coder.json"
	manifest.Roles = append(manifest.Roles, manifest.Roles[0])
	if err := manifest.Validate(); err == nil {
		t.Fatal("expected duplicate role rejection")
	}
}

func TestStarterTeamContainsEightVerifiedRoleBundles(t *testing.T) {
	manifestPath, err := filepath.Abs(filepath.Join("..", "config", "starter-team", "team.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	publicRaw, err := os.ReadFile(filepath.Join(filepath.Dir(manifestPath), "publisher.pub"))
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(publicRaw)))
	if err != nil {
		t.Fatal(err)
	}
	groundingRaw, err := os.ReadFile(filepath.Join(filepath.Dir(manifestPath), "role-grounding-publisher.pub"))
	if err != nil {
		t.Fatal(err)
	}
	groundingKey, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(groundingRaw)))
	if err != nil {
		t.Fatal(err)
	}
	team, err := LoadTeamManifest(manifestPath, kernel.Digest("fc23fd21129c69ebf4b22176b12191170b4b88702366bd5172b37e2098dbc98b"), map[string]ed25519.PublicKey{"tekroo-phase6-bootstrap": publicKey, "tekroo-role-grounding-20260901": groundingKey})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"architect", "coder", "operator", "product-owner", "project-manager", "security", "senior-coder", "tester"}
	if len(team.Roles) != len(want) {
		t.Fatalf("roles=%d want=%d", len(team.Roles), len(want))
	}
	for index, role := range team.Roles {
		if role.Bundle.Role != want[index] {
			t.Fatalf("role %d=%q want=%q", index, role.Bundle.Role, want[index])
		}
	}
	if team.Roles[0].Bundle.Version != "1.1.0" || !strings.Contains(strings.Join(team.Roles[0].Bundle.Permissions, "\n"), "repository.read") {
		t.Fatalf("architect bundle is not repository-grounded: %#v", team.Roles[0].Bundle)
	}
}

func TestRoleFQRNIsTheManifestRoleKey(t *testing.T) {
	if !validRoleName("coder-1") {
		t.Fatal("lexically valid FQRN role key was rejected")
	}
	if validRoleName("teams::coder-1") {
		t.Fatal("actor FQN was accepted as a role FQRN")
	}
}

func testBundle() RoleBundle {
	return RoleBundle{
		SchemaVersion: RoleBundleSchemaVersion,
		Role:          "coder",
		Version:       "1.0.0",
		Capabilities:  []string{"code", "test"},
		Subscriptions: []Subscription{{Type: "tekroo.message.task-assignment", Purpose: "IMPLEMENTATION"}},
		Permissions:   []string{"repository.read", "repository.write"},
		Instructions:  "Implement the exact assigned task and return evidence.",
		Handlers:      map[string]string{"tekroo.message.task-assignment": "implement"},
		Signature:     "placeholder",
	}
}

func writeJSON(t *testing.T, path string, value any) []byte {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return raw
}

func repeat(value string, count int) string {
	result := ""
	for range count {
		result += value
	}
	return result
}
