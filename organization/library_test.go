package organization

import (
	"errors"
	"testing"

	"github.com/tekroo-ai/teams/kernel"
)

func TestRoleLibrarySynchronizesMonotonically(t *testing.T) {
	initial := libraryTeam("1.0.0", kernel.Digest(repeat("a", 64)))
	library, err := NewRoleLibrary(initial)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := library.Sync(initial); err != nil {
		t.Fatalf("idempotent sync: %v", err)
	}
	changedAtSameVersion := libraryTeam("1.0.0", kernel.Digest(repeat("b", 64)))
	if _, err := library.Sync(changedAtSameVersion); !errors.Is(err, ErrRoleLibraryConflict) {
		t.Fatalf("same-version replacement error = %v", err)
	}
	if _, err := library.Sync(libraryTeam("0.9.0", kernel.Digest(repeat("c", 64)))); !errors.Is(err, ErrRoleLibraryConflict) {
		t.Fatalf("rollback error = %v", err)
	}
	updated := libraryTeam("1.1.0", kernel.Digest(repeat("d", 64)))
	if _, err := library.Sync(updated); err != nil {
		t.Fatalf("newer sync: %v", err)
	}
	entries := library.List()
	if len(entries) != 1 || entries[0].Version != "1.1.0" || entries[0].ManifestDigest != updated.Digest {
		t.Fatalf("entries = %+v", entries)
	}
}

func libraryTeam(version string, digest kernel.Digest) LoadedTeam {
	bundle := RoleBundle{
		SchemaVersion: RoleBundleSchemaVersion, Role: "operator", Version: version,
		Capabilities: []string{"operate"}, Permissions: []string{"read"},
		Subscriptions: []Subscription{}, Instructions: "Operate.", Handlers: map[string]string{},
		PublisherKeyID: "test", Signature: "signed",
	}
	binding := RoleBinding{Role: "operator", BundlePath: "roles/operator.json", BundleDigest: kernel.Digest(repeat("e", 64)), PublisherKeyID: "test", InitialInstances: 1, MaximumInstances: 1, LaunchMode: LaunchManual, ModelProfileDigest: kernel.Digest(repeat("f", 64)), WorkspaceIDs: []string{"workspace"}}
	return LoadedTeam{Manifest: TeamManifest{SchemaVersion: TeamManifestSchemaVersion, Team: "teams", Version: version, Roles: []RoleBinding{binding}}, Roles: []LoadedRole{{Binding: binding, Bundle: bundle}}, Digest: digest}
}
