package organization

import (
	"errors"
	"testing"

	"github.com/tekroo-ai/teams/kernel"
)

func TestRoleHandlerResolverSelectsPerRoleHandlerForSameMessageType(t *testing.T) {
	messageType := "tekroo.message.review.requested"
	architect := dispatchTestRole(t, "architect", messageType, "design-review", "Review architecture boundaries.")
	coder := dispatchTestRole(t, "coder", messageType, "implementation-review", "Review the implementation evidence.")
	team := LoadedTeam{
		Manifest: TeamManifest{SchemaVersion: TeamManifestSchemaVersion, Team: "teams", Version: "2.0.0", Roles: []RoleBinding{architect.Binding, coder.Binding}},
		Roles:    []LoadedRole{architect, coder}, Digest: kernel.Digest(repeat("f", 64)),
	}
	resolver, err := NewRoleHandlerResolver(team)
	if err != nil {
		t.Fatal(err)
	}
	architectDispatch, err := resolver.Resolve("teams::architect-1", messageType, "design-review")
	if err != nil {
		t.Fatal(err)
	}
	coderDispatch, err := resolver.Resolve("teams::coder-1", messageType, "implementation-review")
	if err != nil {
		t.Fatal(err)
	}
	if architectDispatch.HandlerInstructions == coderDispatch.HandlerInstructions || architectDispatch.RoleFQRN != "architect" || coderDispatch.RoleFQRN != "coder" {
		t.Fatalf("per-role handlers were not distinct: architect=%#v coder=%#v", architectDispatch, coderDispatch)
	}
	if _, err := resolver.Resolve("teams::coder-1", messageType, "design-review"); !errors.Is(err, ErrRoleHandlerMismatch) {
		t.Fatalf("purpose mismatch error=%v", err)
	}
	if _, err := resolver.Resolve("teams::coder-1", "tekroo.message.unknown", "implementation-review"); !errors.Is(err, ErrRoleHandlerMismatch) {
		t.Fatalf("unmapped message error=%v", err)
	}
	message := testMessage(70)
	message.Type = messageType
	message.Recipient = "teams::coder-1"
	message.Purpose = PurposeRequest
	if _, err := resolver.ResolveMessage(message.Recipient, message); !errors.Is(err, ErrRoleHandlerMismatch) {
		t.Fatalf("transport-purpose mismatch error=%v", err)
	}
	message.Purpose = PurposeHandoff
	if _, err := resolver.ResolveMessage(message.Recipient, message); err != nil {
		t.Fatalf("exact transport purpose rejected: %v", err)
	}
}

func TestRoleHandlerResolverAtomicallyReplacesCurrentVersionAndRetainsBoundVersion(t *testing.T) {
	messageType := "tekroo.message.task.assigned"
	initialRole := dispatchTestRole(t, "coder", messageType, "implementation", "Initial implementation procedure.")
	initialTeam := dispatchTestTeam(initialRole, "2.0.0", kernel.Digest(repeat("e", 64)))
	resolver, err := NewRoleHandlerResolver(initialTeam)
	if err != nil {
		t.Fatal(err)
	}
	initialDigest := initialRole.Binding.BundleDigest

	successorRole := dispatchTestRole(t, "coder", messageType, "implementation", "Successor implementation procedure.")
	successorRole.Bundle.Version = "2.1.0"
	successorDigest, err := successorRole.Bundle.ContentDigest()
	if err != nil {
		t.Fatal(err)
	}
	successorRole.Binding.BundleDigest = successorDigest
	successorTeam := dispatchTestTeam(successorRole, "2.1.0", kernel.Digest(repeat("d", 64)))
	if err := resolver.Sync(successorTeam); err != nil {
		t.Fatal(err)
	}

	current, err := resolver.Resolve("teams::coder-1", messageType, "implementation")
	if err != nil || current.BundleDigest != successorDigest || current.HandlerInstructions != "Successor implementation procedure." {
		t.Fatalf("current dispatch=%+v err=%v", current, err)
	}
	bound, err := resolver.ResolveVersion("teams::coder-1", initialDigest, messageType, "implementation")
	if err != nil || bound.BundleDigest != initialDigest || bound.HandlerInstructions != "Initial implementation procedure." {
		t.Fatalf("bound dispatch=%+v err=%v", bound, err)
	}

	// Sync owns an immutable copy rather than aliases to caller-controlled maps.
	successorRole.Package.Handlers[messageType] = initialRole.Package.Handlers[messageType]
	current, err = resolver.Resolve("teams::coder-1", messageType, "implementation")
	if err != nil || current.HandlerInstructions != "Successor implementation procedure." {
		t.Fatalf("cached dispatch mutated through source package: %+v err=%v", current, err)
	}
}

func dispatchTestTeam(role LoadedRole, version string, digest kernel.Digest) LoadedTeam {
	return LoadedTeam{
		Manifest: TeamManifest{SchemaVersion: TeamManifestSchemaVersion, Team: "teams", Version: version, Roles: []RoleBinding{role.Binding}},
		Roles:    []LoadedRole{role}, Digest: digest,
	}
}

func dispatchTestRole(t *testing.T, role, messageType, purpose, instructions string) LoadedRole {
	t.Helper()
	charter := "# " + role + "\n\nStay within the assigned role.\n"
	inputSchema := []byte(`{"type":"object"}`)
	resultSchema := []byte(`{"type":"object"}`)
	charterRef := roleResourceForTest("ROLE.md", "text/markdown", []byte(charter))
	handlerRef := roleResourceForTest("HANDLER.md", "text/markdown", []byte(instructions))
	inputRef := roleResourceForTest("input.schema.json", "application/schema+json", inputSchema)
	resultRef := roleResourceForTest("result.schema.json", "application/schema+json", resultSchema)
	bundle := RoleBundle{
		SchemaVersion: RolePackageSchemaVersion, Role: role, Version: "2.0.0",
		Capabilities: []string{"review"}, Subscriptions: []Subscription{{Type: messageType, Purpose: purpose}}, Permissions: []string{"repository.read"},
		Charter: charterRef,
		HandlerBindings: map[string]RoleHandlerBinding{messageType: {
			SubscriptionPurpose: purpose, MessagePurpose: PurposeHandoff, Disposition: HandlerModel, Resource: handlerRef, InputSchema: inputRef, ResultSchema: resultRef,
			AllowedResults: []string{"blocked", "completed", "failed", "needs_decision"}, AllowedMessageProposals: []string{"tekroo.message.review.completed"},
		}},
		PublisherKeyID: "test", Signature: "signed",
	}
	digest, err := bundle.ContentDigest()
	if err != nil {
		t.Fatal(err)
	}
	binding := RoleBinding{Role: role, BundlePath: "roles/" + role + "/role.json", BundleDigest: digest, PublisherKeyID: "test", InitialInstances: 1, MaximumInstances: 1, LaunchMode: LaunchOnDemand, ModelProfileDigest: kernel.Digest(repeat("a", 64)), WorkspaceIDs: []string{role + "-1"}}
	return LoadedRole{Binding: binding, Bundle: bundle, Package: &LoadedRolePackage{Charter: charter, Handlers: map[string]LoadedRoleHandler{messageType: {
		Binding: bundle.HandlerBindings[messageType], Instructions: instructions, InputSchema: inputSchema, ResultSchema: resultSchema,
	}}}}
}
