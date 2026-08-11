package kernel_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

func TestAuthorizationMatrixCoversEveryPrincipalKindAndScope(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	kinds := []kernel.PrincipalKind{kernel.PrincipalActor, kernel.PrincipalHuman, kernel.PrincipalService, kernel.PrincipalPolicy}
	for index, kind := range kinds {
		principal := kernel.PrincipalRef{Kind: kind, ID: fmt.Sprintf("principal-%d", index)}
		command := authorizationCommand(principal)
		policy := authorizationPolicy(grantFor(principal, command, digestFor(index+1)))
		result := policy.Authorize(command, nil, now)
		if !result.Allowed || result.ReasonCode != "AUTHORIZED" || len(result.GrantDigests) != 1 {
			t.Fatalf("kind %s result = %#v", kind, result)
		}
		wrongTarget := command
		wrongTarget.Target.Kind = kernel.AggregateTask
		if result := policy.Authorize(wrongTarget, nil, now); result.Allowed {
			t.Fatalf("kind %s escaped target scope", kind)
		}
		wrongCommand := command
		wrongCommand.CommandType = "tekroo.command.story.activate"
		if result := policy.Authorize(wrongCommand, nil, now); result.Allowed {
			t.Fatalf("kind %s escaped command scope", kind)
		}
	}
}

func TestDelegationNarrowsScopeAndIsBoundedRevocableAndNonTransitive(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	expiry := now.Add(time.Hour)
	root := kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"}
	actors := []kernel.PrincipalRef{
		{Kind: kernel.PrincipalActor, ID: "teams::pm-1"},
		{Kind: kernel.PrincipalActor, ID: "teams::planner-1"},
		{Kind: kernel.PrincipalActor, ID: "teams::coder-1"},
		{Kind: kernel.PrincipalActor, ID: "teams::reviewer-1"},
		{Kind: kernel.PrincipalActor, ID: "teams::release-1"},
	}
	command := authorizationCommand(actors[3])
	grant := grantFor(root, command, digestFor(1))
	grant.AllowDelegation = true
	grant.ExpiresAt = &expiry
	policy := authorizationPolicy(grant)
	parent := grant.GrantDigest
	from := root
	for index := 0; index < 4; index++ {
		delegation := kernel.AuthorityDelegation{
			DelegationDigest: digestFor(10 + index), ParentDigest: parent,
			From: from, To: actors[index], Scope: grant.Scope, ExpiresAt: &expiry, AllowFurther: true,
		}
		policy.Delegations = append(policy.Delegations, delegation)
		parent = delegation.DelegationDigest
		from = actors[index]
	}
	result := policy.Authorize(command, nil, now)
	if !result.Allowed || len(result.DelegationDigests) != 4 {
		t.Fatalf("four-hop result = %#v", result)
	}

	fifth := kernel.AuthorityDelegation{
		DelegationDigest: digestFor(20), ParentDigest: parent, From: actors[3], To: actors[4],
		Scope: grant.Scope, ExpiresAt: &expiry, AllowFurther: true,
	}
	policy.Delegations = append(policy.Delegations, fifth)
	fifthCommand := authorizationCommand(actors[4])
	if result := policy.Authorize(fifthCommand, nil, now); result.Allowed {
		t.Fatalf("five-hop delegation was accepted: %#v", result)
	}

	revoked := policy
	revoked.Delegations = append([]kernel.AuthorityDelegation(nil), policy.Delegations...)
	revoked.Delegations[1].Revoked = true
	if result := revoked.Authorize(command, nil, now); result.Allowed {
		t.Fatal("revoked delegation remained authoritative")
	}

	nonTransitive := policy
	nonTransitive.Delegations = append([]kernel.AuthorityDelegation(nil), policy.Delegations...)
	nonTransitive.Delegations[0].AllowFurther = false
	if result := nonTransitive.Authorize(command, nil, now); result.Allowed {
		t.Fatal("implicit transitive delegation was accepted")
	}

	expired := policy
	expired.Grants = append([]kernel.AuthorityGrant(nil), policy.Grants...)
	past := now.Add(-time.Second)
	expired.Grants[0].ExpiresAt = &past
	if result := expired.Authorize(command, nil, now); result.Allowed {
		t.Fatal("expired grant remained authoritative")
	}
}

func TestDelegationRejectsExpansionCyclesAndOwnerBypass(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	root := kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"}
	actor := kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: "teams::coder-1"}
	command := authorizationCommand(actor)
	actorFQN := kernel.ActorFQN("teams::coder-1")
	command.ActorFQN = &actorFQN
	grant := grantFor(root, command, digestFor(1))
	grant.AllowDelegation = true
	grant.Scope.RequiresOwner = true
	policy := authorizationPolicy(grant)
	policy.Delegations = []kernel.AuthorityDelegation{{
		DelegationDigest: digestFor(2), ParentDigest: grant.GrantDigest, From: root, To: actor,
		Scope: grant.Scope,
	}}
	wrongOwner := kernel.ActorFQN("teams::coder-2")
	state := &kernel.AggregateState{Ownership: kernel.Ownership{OwnerFQN: &wrongOwner, OwnershipVersion: 1}}
	if result := policy.Authorize(command, state, now); result.Allowed {
		t.Fatal("ownership requirement was bypassed")
	}
	state.Ownership.OwnerFQN = &actorFQN
	if result := policy.Authorize(command, state, now); !result.Allowed {
		t.Fatalf("exact owner was rejected: %#v", result)
	}

	expanded := policy
	expanded.Delegations = append([]kernel.AuthorityDelegation(nil), policy.Delegations...)
	expanded.Delegations[0].Scope.CommandTypes = append(expanded.Delegations[0].Scope.CommandTypes, "tekroo.command.story.activate")
	if result := expanded.Authorize(command, state, now); result.Allowed {
		t.Fatal("scope-expanding delegation was accepted")
	}

	cycle := policy
	cycle.Delegations[0].AllowFurther = true
	cycle.Delegations = append(cycle.Delegations, kernel.AuthorityDelegation{
		DelegationDigest: digestFor(3), ParentDigest: cycle.Delegations[0].DelegationDigest,
		From: actor, To: root, Scope: grant.Scope, AllowFurther: true,
	})
	third := kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: "teams::reviewer-1"}
	cycle.Delegations = append(cycle.Delegations, kernel.AuthorityDelegation{
		DelegationDigest: digestFor(4), ParentDigest: cycle.Delegations[1].DelegationDigest,
		From: root, To: third, Scope: grant.Scope,
	})
	if result := cycle.Authorize(authorizationCommand(third), state, now); result.Allowed {
		t.Fatal("delegation cycle was accepted")
	}
}

func authorizationCommand(principal kernel.PrincipalRef) kernel.KernelCommand {
	return kernel.KernelCommand{
		CommandType: "tekroo.command.story.authorize",
		Target:      kernel.AggregateRef{Kind: kernel.AggregateStory, ID: kernel.UUIDv7("00000000-0000-7000-8000-0000000000d1")},
		Authority:   principal,
	}
}

func grantFor(principal kernel.PrincipalRef, command kernel.KernelCommand, digest kernel.Digest) kernel.AuthorityGrant {
	return kernel.AuthorityGrant{
		GrantDigest: digest, Grantee: principal,
		Scope: kernel.AuthorityScope{
			CommandTypes: []string{command.CommandType}, TargetKinds: []kernel.AggregateKind{command.Target.Kind},
			TargetIDs: []kernel.UUIDv7{command.Target.ID}, CanReadTarget: true,
		},
	}
}

func authorizationPolicy(grant kernel.AuthorityGrant) kernel.AuthorizationPolicy {
	return kernel.AuthorizationPolicy{
		PolicyDigest: kernel.Digest("9999999999999999999999999999999999999999999999999999999999999999"),
		Revision:     1, Grants: []kernel.AuthorityGrant{grant},
	}
}

func digestFor(ordinal int) kernel.Digest {
	return kernel.Digest(fmt.Sprintf("%064x", ordinal))
}
