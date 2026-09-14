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

func TestStarterSuccessorRolePackagesLoadWithPublishedKey(t *testing.T) {
	manifestPath, err := filepath.Abs(filepath.Join("..", "config", "starter-team", "team.v4.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifestRaw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifestHash := sha256.Sum256(manifestRaw)
	publicRaw, err := os.ReadFile(filepath.Join(filepath.Dir(manifestPath), "message-handler-publisher.pub"))
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(publicRaw)))
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		t.Fatalf("published key: %v", err)
	}
	qualificationRaw, err := os.ReadFile(filepath.Join(filepath.Dir(manifestPath), "message-handler-publisher-qualification.pub"))
	if err != nil {
		t.Fatal(err)
	}
	qualificationKey, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(qualificationRaw)))
	if err != nil || len(qualificationKey) != ed25519.PublicKeySize {
		t.Fatalf("qualification key: %v", err)
	}
	loaded, err := LoadTeamManifest(manifestPath, kernel.Digest(hex.EncodeToString(manifestHash[:])), map[string]ed25519.PublicKey{"tekroo-message-handlers-20260913": publicKey, "tekroo-message-handlers-qualification-20260913": qualificationKey})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Roles) != 8 {
		t.Fatalf("loaded roles = %d", len(loaded.Roles))
	}
	for _, role := range loaded.Roles {
		if role.Bundle.SchemaVersion != RolePackageSchemaVersion || role.Package == nil || len(role.Package.Handlers) != len(role.Bundle.Subscriptions) {
			t.Fatalf("incomplete successor role package: %s", role.Binding.Role)
		}
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

func TestLoadTeamManifestLoadsContentAddressedRolePackage(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	packageRoot := filepath.Join(root, "roles", "coder")
	for _, path := range []string{
		filepath.Join(packageRoot, "handlers", "task.assigned"),
	} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	charter := []byte("# Coder\n\nImplement assigned work without changing its scope.\n")
	handler := []byte("# task.assigned\n\nImplement the admitted task and return evidence.\n")
	inputSchema := []byte(`{"type":"object","required":["task_id"]}`)
	resultSchema := []byte(`{"type":"object","required":["result"]}`)
	writeRolePackageFile(t, filepath.Join(packageRoot, "ROLE.md"), charter)
	writeRolePackageFile(t, filepath.Join(packageRoot, "handlers", "task.assigned", "HANDLER.md"), handler)
	writeRolePackageFile(t, filepath.Join(packageRoot, "handlers", "task.assigned", "input.schema.json"), inputSchema)
	writeRolePackageFile(t, filepath.Join(packageRoot, "handlers", "task.assigned", "result.schema.json"), resultSchema)

	bundle := RoleBundle{
		SchemaVersion: RolePackageSchemaVersion, Role: "coder", Version: "2.0.0",
		Capabilities:  []string{"code", "test"},
		Subscriptions: []Subscription{{Type: "tekroo.message.task.assigned", Purpose: "implementation"}},
		Permissions:   []string{"repository.read", "repository.write"},
		Charter:       roleResourceForTest("ROLE.md", "text/markdown", charter),
		HandlerBindings: map[string]RoleHandlerBinding{
			"tekroo.message.task.assigned": {
				SubscriptionPurpose: "implementation", MessagePurpose: PurposeHandoff, Disposition: HandlerModel,
				Resource:                roleResourceForTest("handlers/task.assigned/HANDLER.md", "text/markdown", handler),
				InputSchema:             roleResourceForTest("handlers/task.assigned/input.schema.json", "application/schema+json", inputSchema),
				ResultSchema:            roleResourceForTest("handlers/task.assigned/result.schema.json", "application/schema+json", resultSchema),
				AllowedResults:          []string{"blocked", "completed", "failed", "needs_decision"},
				AllowedMessageProposals: []string{"tekroo.message.task.blocked", "tekroo.message.task.completed"},
			},
		},
		PublisherKeyID: "test-publisher", Signature: "placeholder",
	}
	digest, err := bundle.ContentDigest()
	if err != nil {
		t.Fatal(err)
	}
	digestBytes, _ := hex.DecodeString(string(digest))
	bundle.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, digestBytes))
	bundlePath := filepath.Join(packageRoot, "role.json")
	writeJSON(t, bundlePath, bundle)
	manifest := TeamManifest{
		SchemaVersion: TeamManifestSchemaVersion, Team: "teams", Version: "2.0.0",
		Roles: []RoleBinding{{Role: "coder", BundlePath: "roles/coder/role.json", BundleDigest: digest, PublisherKeyID: "test-publisher", InitialInstances: 1, MaximumInstances: 1, LaunchMode: LaunchOnDemand, ModelProfileDigest: kernel.Digest(repeat("a", 64)), WorkspaceIDs: []string{"coder-1"}}},
	}
	manifestPath := filepath.Join(root, "team.json")
	manifestRaw := writeJSON(t, manifestPath, manifest)
	manifestDigest := sha256.Sum256(manifestRaw)
	loaded, err := LoadTeamManifest(manifestPath, kernel.Digest(hex.EncodeToString(manifestDigest[:])), map[string]ed25519.PublicKey{"test-publisher": publicKey})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Roles[0].Package == nil || loaded.Roles[0].Package.Charter != string(charter) {
		t.Fatalf("role package was not loaded: %#v", loaded.Roles[0].Package)
	}
	loadedHandler, found := loaded.Roles[0].Package.Handler("tekroo.message.task.assigned", "implementation")
	if !found || loadedHandler.Instructions != string(handler) || string(loadedHandler.InputSchema) != string(inputSchema) || string(loadedHandler.ResultSchema) != string(resultSchema) {
		t.Fatalf("handler was not loaded exactly: found=%v handler=%#v", found, loadedHandler)
	}
}

func TestRolePackageRejectsMutatedAndEscapingResources(t *testing.T) {
	root := t.TempDir()
	charterPath := filepath.Join(root, "ROLE.md")
	charter := []byte("# Role\n")
	writeRolePackageFile(t, charterPath, charter)
	bundle := RoleBundle{
		SchemaVersion: RolePackageSchemaVersion, Role: "observer", Version: "2.0.0",
		Capabilities: []string{"observe"}, Subscriptions: []Subscription{{Type: "tekroo.message.status.changed", Purpose: "observe"}}, Permissions: []string{"status.read"},
		Charter:         roleResourceForTest("ROLE.md", "text/markdown", charter),
		HandlerBindings: map[string]RoleHandlerBinding{"tekroo.message.status.changed": {SubscriptionPurpose: "observe", MessagePurpose: PurposeNotification, Disposition: HandlerObserveOnly, AllowedResults: []string{"completed"}}},
		PublisherKeyID:  "test", Signature: "placeholder",
	}
	if _, err := LoadRolePackage(filepath.Join(root, "role.json"), bundle); err != nil {
		t.Fatalf("valid observe-only package: %v", err)
	}
	writeRolePackageFile(t, charterPath, []byte("mutated"))
	if _, err := LoadRolePackage(filepath.Join(root, "role.json"), bundle); err == nil {
		t.Fatal("mutated charter was accepted")
	}
	bundle.Charter.Path = "../ROLE.md"
	if bundle.Validate() == nil {
		t.Fatal("escaping charter path was accepted")
	}
}

func TestRolePackageRejectsSchemaThatCannotCompile(t *testing.T) {
	root := t.TempDir()
	charter := []byte("# Role\n")
	handler := []byte("# Handle\n")
	invalidSchema := []byte(`{"type":"not-a-json-schema-type"}`)
	resultSchema := []byte(`{"type":"object"}`)
	writeRolePackageFile(t, filepath.Join(root, "ROLE.md"), charter)
	writeRolePackageFile(t, filepath.Join(root, "HANDLER.md"), handler)
	writeRolePackageFile(t, filepath.Join(root, "input.schema.json"), invalidSchema)
	writeRolePackageFile(t, filepath.Join(root, "result.schema.json"), resultSchema)
	bundle := RoleBundle{
		SchemaVersion: RolePackageSchemaVersion, Role: "editor", Version: "2.0.0",
		Capabilities: []string{"editing"}, Subscriptions: []Subscription{{Type: "tekroo.message.copy.edit-requested", Purpose: "editing"}}, Permissions: []string{"document.read"},
		Charter: roleResourceForTest("ROLE.md", "text/markdown", charter),
		HandlerBindings: map[string]RoleHandlerBinding{"tekroo.message.copy.edit-requested": {
			SubscriptionPurpose: "editing", MessagePurpose: PurposeRequest, Disposition: HandlerModel,
			Resource:       roleResourceForTest("HANDLER.md", "text/markdown", handler),
			InputSchema:    roleResourceForTest("input.schema.json", "application/schema+json", invalidSchema),
			ResultSchema:   roleResourceForTest("result.schema.json", "application/schema+json", resultSchema),
			AllowedResults: []string{"completed"},
		}},
		PublisherKeyID: "test", Signature: "placeholder",
	}
	if bundle.Validate() != nil {
		t.Fatal("test bundle metadata is invalid")
	}
	if _, err := LoadRolePackage(filepath.Join(root, "role.json"), bundle); err == nil {
		t.Fatal("uncompilable input schema was accepted")
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
	team, err := LoadTeamManifest(manifestPath, kernel.Digest("3478f27988da4f7c022df0ca7145af88b8e8cc1eb6fd446402ff69b33519c693"), map[string]ed25519.PublicKey{"tekroo-phase6-bootstrap": publicKey, "tekroo-role-grounding-20260901": groundingKey})
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

func writeRolePackageFile(t *testing.T, path string, raw []byte) {
	t.Helper()
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func roleResourceForTest(path, mediaType string, raw []byte) *RoleResource {
	digest := sha256.Sum256(raw)
	return &RoleResource{Path: path, SHA256: kernel.Digest(hex.EncodeToString(digest[:])), MediaType: mediaType}
}

func repeat(value string, count int) string {
	result := ""
	for range count {
		result += value
	}
	return result
}
