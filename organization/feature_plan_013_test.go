package organization

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tekroo-ai/teams/kernel"
)

func TestPlan013ProjectManagerBundleIsSignedAndLoadable(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(filepath.Dir(workingDirectory), "config", "starter-team")
	publicRaw, err := os.ReadFile(filepath.Join(root, "plan-013-publisher.pub"))
	if err != nil {
		t.Fatal(err)
	}
	publicBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(publicRaw)))
	if err != nil {
		t.Fatal(err)
	}
	bundlePath := filepath.Join(root, "roles-v4", "project-manager-2.2.0", "role.json")
	bundle, err := LoadRoleBundle(bundlePath, kernel.Digest("cd4ba17ff7f37f7b4004bd759f5b801052e5f571e788813528825c76f17ba003"), "tekroo-teams-plan-013-20260930", map[string]ed25519.PublicKey{"tekroo-teams-plan-013-20260930": ed25519.PublicKey(publicBytes)})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadRolePackage(bundlePath, bundle)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := loaded.Handler("tekroo.message.feature.design-proposed", "execution-planning"); !found {
		t.Fatal("PM finalization handler is not bound")
	}
}

func TestPlan013ProjectManagerSpecificationSchemaMatchesRuntimeBoundary(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(filepath.Dir(workingDirectory), "config", "starter-team")
	publicRaw, err := os.ReadFile(filepath.Join(root, "plan-0131-publisher.pub"))
	if err != nil {
		t.Fatal(err)
	}
	publicBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(publicRaw)))
	if err != nil {
		t.Fatal(err)
	}
	bundlePath := filepath.Join(root, "roles-v4", "project-manager-2.2.1", "role.json")
	const keyID = "tekroo-teams-plan-0131-20261001"
	const digest = "775f8eaa0af2f5ff4fd8490f7d3395bc2160159223372d0765e4fb66cdc65429"
	bundle, err := LoadRoleBundle(bundlePath, kernel.Digest(digest), keyID, map[string]ed25519.PublicKey{keyID: ed25519.PublicKey(publicBytes)})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadRolePackage(bundlePath, bundle)
	if err != nil {
		t.Fatal(err)
	}
	handler, found := loaded.Handler("tekroo.message.feature.refined", "story-planning")
	if !found {
		t.Fatal("PM specification handler is not bound")
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(handler.ResultSchema, &schema); err != nil {
		t.Fatal(err)
	}
	var workProduct struct {
		AdditionalProperties bool                       `json:"additionalProperties"`
		Required             []string                   `json:"required"`
		Properties           map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(schema.Properties["work_product"], &workProduct); err != nil {
		t.Fatal(err)
	}
	expected := []string{"schema_version", "result_type", "stories"}
	if workProduct.AdditionalProperties || len(workProduct.Required) != len(expected) || len(workProduct.Properties) != len(expected) {
		t.Fatalf("PM specification schema diverges from runtime fields: required=%v properties=%v", workProduct.Required, workProduct.Properties)
	}
	required := make(map[string]bool, len(workProduct.Required))
	for _, field := range workProduct.Required {
		required[field] = true
	}
	for _, field := range expected {
		if _, found := workProduct.Properties[field]; !found || !required[field] {
			t.Fatalf("PM specification schema is missing runtime field %q", field)
		}
	}
}
