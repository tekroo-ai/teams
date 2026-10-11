package rolefixtures

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/tekroo-ai/teams/internal/testpaths"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestCurrentFixtureRetainsDigestAndPublisherChecks(t *testing.T) {
	base := filepath.Join(testpaths.Root(t), "config", "starter-team")
	path := filepath.Join(base, "team.v4.runtime-test.json")
	if _, err := organization.LoadTeamManifest(path, kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), currentPublisherKeys(t, base)); err == nil {
		t.Fatal("mismatched fixture manifest digest accepted")
	}
	keys := currentPublisherKeys(t, base)
	keys["tekroo-teams-amendment-20260917"][0] ^= 1
	if _, err := organization.LoadTeamManifest(path, CurrentManifestDigest, keys); err == nil {
		t.Fatal("changed publisher key accepted")
	}
	keys = currentPublisherKeys(t, base)
	delete(keys, "tekroo-native-coder-20261003")
	if _, err := organization.LoadTeamManifest(path, CurrentManifestDigest, keys); err == nil {
		t.Fatal("untrusted role publisher accepted")
	}
	if team := Current(t); team.Digest != CurrentManifestDigest || len(team.Roles) == 0 {
		t.Fatal("authenticated current fixture did not load")
	}
}

func TestCanonicalCoderPublisherMatchesPinnedBundle(t *testing.T) {
	team := Current(t)
	base := filepath.Join(testpaths.Root(t), "config", "starter-team")
	canonical, err := os.ReadFile(filepath.Join(base, "native-coder-publisher.pub"))
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range team.Roles {
		if role.Binding.Role != "coder" {
			continue
		}
		original, err := os.ReadFile(filepath.Join(base, filepath.Dir(role.Binding.BundlePath), "publisher.pub"))
		if err != nil || !bytes.Equal(bytes.TrimSpace(canonical), bytes.TrimSpace(original)) {
			t.Fatalf("canonical publisher differs from the pinned signed bundle: %v", err)
		}
		return
	}
	t.Fatal("coder fixture absent")
}
