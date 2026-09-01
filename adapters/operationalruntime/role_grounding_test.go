package operationalruntime

import (
	"context"
	"strings"
	"testing"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

type testRoleGroundingResolver struct{}

func (testRoleGroundingResolver) ResolveRoleGrounding(_ context.Context, actor kernel.ActorFQN) (application.RoleExecutionGrounding, error) {
	fqrn, err := kernel.RoleFQRNFromActor(actor)
	if err != nil {
		return application.RoleExecutionGrounding{}, err
	}
	return application.RoleExecutionGrounding{
		ActorFQN: actor, RoleFQRN: fqrn,
		BundleVersion: "1.0.0", BundleDigest: kernel.Digest(strings.Repeat("a", 64)),
		Capabilities: []string{"execute"}, Permissions: []string{"repository.read"},
		Instructions: "Perform the exact duties of the assigned role.",
	}, nil
}

func TestBoundRoleGroundingSeparatesFQRNDefinitionFromFQNInstance(t *testing.T) {
	bundle := organization.RoleBundle{
		SchemaVersion: organization.RoleBundleSchemaVersion, Role: "coder", Version: "1.0.0",
		Capabilities: []string{"implement"}, Subscriptions: []organization.Subscription{},
		Permissions: []string{"repository.read"}, Instructions: "Implement assigned work.",
		Handlers: map[string]string{}, PublisherKeyID: "test-publisher", Signature: "signed",
	}
	digest, err := bundle.ContentDigest()
	if err != nil {
		t.Fatal(err)
	}
	binding := organization.RoleBinding{
		Role: "coder", BundlePath: "roles/coder.json", BundleDigest: digest, PublisherKeyID: "test-publisher",
		InitialInstances: 1, MaximumInstances: 2, LaunchMode: organization.LaunchOnDemand,
		ModelProfileDigest: kernel.Digest(strings.Repeat("b", 64)), WorkspaceIDs: []string{"coder-1", "coder-2"},
	}
	manifest := organization.TeamManifest{SchemaVersion: organization.TeamManifestSchemaVersion, Team: "teams", Version: "1.0.0", Roles: []organization.RoleBinding{binding}}
	team := organization.LoadedTeam{Manifest: manifest, Roles: []organization.LoadedRole{{Binding: binding, Bundle: bundle}}, Digest: kernel.Digest(strings.Repeat("c", 64))}
	resolver, err := newBoundRoleGroundingResolver(team)
	if err != nil {
		t.Fatal(err)
	}
	actor := kernel.ActorFQN("teams::coder-1")
	grounding, err := resolver.ResolveRoleGrounding(context.Background(), actor)
	if err != nil {
		t.Fatal(err)
	}
	if grounding.ActorFQN != actor || grounding.RoleFQRN != "coder" || grounding.BundleDigest != team.Roles[0].Binding.BundleDigest || grounding.Instructions != team.Roles[0].Bundle.Instructions {
		t.Fatalf("grounding = %#v", grounding)
	}
	second, err := resolver.ResolveRoleGrounding(context.Background(), "teams::coder-2")
	if err != nil || second.ActorFQN != "teams::coder-2" || second.RoleFQRN != grounding.RoleFQRN || second.BundleDigest != grounding.BundleDigest {
		t.Fatalf("second grounding = %#v err=%v", second, err)
	}
	grounding.Capabilities[0] = "mutated"
	again, err := resolver.ResolveRoleGrounding(context.Background(), actor)
	if err != nil || again.Capabilities[0] == "mutated" {
		t.Fatalf("resolver leaked mutable role state: %#v err=%v", again, err)
	}
	if _, err := resolver.ResolveRoleGrounding(context.Background(), "teams::coder-3"); err == nil {
		t.Fatal("unconfigured actor instance resolved a role bundle")
	}
}
