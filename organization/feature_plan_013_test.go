package organization

import (
	"crypto/ed25519"
	"encoding/base64"
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
