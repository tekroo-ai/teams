// Package rolefixtures supplies authenticated role packages to adapter tests.
// Production code must not import it. Historical fixtures remain separately
// pinned; changing the current set requires an explicit manifest/digest update.
package rolefixtures

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/internal/testpaths"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

const CurrentManifestDigest = kernel.Digest("d2486cd8b520e5f0934f1d8957e6c0185b1bee59c4cecdfc22d8776f17fe240c")

// Current uses synthetic model identities for fake providers, never model
// qualification. Role signatures, digests and resources use the real loader.
func Current(t testing.TB) organization.LoadedTeam {
	t.Helper()
	base := filepath.Join(testpaths.Root(t), "config", "starter-team")
	keys := currentPublisherKeys(t, base)
	team, err := organization.LoadTeamManifest(filepath.Join(base, "team.v4.runtime-test.json"), CurrentManifestDigest, keys)
	if err != nil {
		t.Fatal(err)
	}
	return team
}

// Handler returns the authenticated handler without substituting its field
// set or authority. It is shared across transport/runtime adapter tests.
func Handler(t testing.TB, team organization.LoadedTeam, role, message string) application.MessageHandlerGrounding {
	t.Helper()
	for _, loaded := range team.Roles {
		if loaded.Binding.Role != role {
			continue
		}
		return packageHandler(t, loaded.Package, role, message)
	}
	t.Fatalf("missing role %s", role)
	return application.MessageHandlerGrounding{}
}

// HistoricalPlanHandler retains the exact PM 2.2.1 regression contract. It is
// not silently upgraded when the current runtime bundle changes.
func HistoricalPlanHandler(t testing.TB) application.MessageHandlerGrounding {
	t.Helper()
	base := filepath.Join(testpaths.Root(t), "config", "starter-team")
	const publisher = "tekroo-teams-plan-0131-20261001"
	keys := publisherKeys(t, base, map[string]string{publisher: "plan-0131-publisher.pub"})
	path := filepath.Join(base, "roles-v4", "project-manager-2.2.1", "role.json")
	bundle, err := organization.LoadRoleBundle(path, kernel.Digest("775f8eaa0af2f5ff4fd8490f7d3395bc2160159223372d0765e4fb66cdc65429"), publisher, keys)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := organization.LoadRolePackage(path, bundle)
	if err != nil {
		t.Fatal(err)
	}
	return packageHandler(t, &pkg, "project-manager", "tekroo.message.feature.design-proposed")
}

func packageHandler(t testing.TB, pkg *organization.LoadedRolePackage, role, message string) application.MessageHandlerGrounding {
	t.Helper()
	if pkg == nil {
		t.Fatalf("role %s has no verified package", role)
	}
	handler, found := pkg.Handlers[message]
	if !found {
		t.Fatalf("role %s has no handler %s", role, message)
	}
	return application.MessageHandlerGrounding{
		MessageType: message, Instructions: handler.Instructions,
		ResultSchema: handler.ResultSchema, ResultSchemaDigest: handler.Binding.ResultSchema.SHA256,
		AllowedResults: handler.Binding.AllowedResults, AllowedMessageProposals: handler.Binding.AllowedMessageProposals,
	}
}

func currentPublisherKeys(t testing.TB, base string) map[string]ed25519.PublicKey {
	t.Helper()
	return publisherKeys(t, base, map[string]string{
		"tekroo-teams-amendment-20260917": "role-amendment-publisher.pub",
		"tekroo-teams-plan-0131-20261001": "plan-0131-publisher.pub",
		"tekroo-native-coder-20261003":    "native-coder-publisher.pub",
	})
}

func publisherKeys(t testing.TB, base string, paths map[string]string) map[string]ed25519.PublicKey {
	t.Helper()
	keys := make(map[string]ed25519.PublicKey, len(paths))
	for id, relative := range paths {
		raw, err := os.ReadFile(filepath.Join(base, relative))
		if err != nil {
			t.Fatal(err)
		}
		key, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(raw)))
		if err != nil || len(key) != ed25519.PublicKeySize {
			t.Fatalf("publisher %s: err=%v length=%d", id, err, len(key))
		}
		keys[id] = key
	}
	return keys
}
