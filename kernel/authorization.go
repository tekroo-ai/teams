package kernel

import (
	"sort"
	"time"
)

const MaxDelegationDepth = 4

type AuthorityScope struct {
	CommandTypes      []string        `json:"command_types"`
	TargetKinds       []AggregateKind `json:"target_kinds"`
	TargetIDs         []UUIDv7        `json:"target_ids"`
	CanReadTarget     bool            `json:"can_read_target"`
	RequiresOwner     bool            `json:"requires_owner"`
	CanReopenAccepted bool            `json:"can_reopen_accepted"`
}

type AuthorityGrant struct {
	GrantDigest     Digest         `json:"grant_digest"`
	Grantee         PrincipalRef   `json:"grantee"`
	Scope           AuthorityScope `json:"scope"`
	ExpiresAt       *time.Time     `json:"expires_at,omitempty"`
	Revoked         bool           `json:"revoked"`
	AllowDelegation bool           `json:"allow_delegation"`
}

type AuthorityDelegation struct {
	DelegationDigest Digest         `json:"delegation_digest"`
	ParentDigest     Digest         `json:"parent_digest"`
	From             PrincipalRef   `json:"from"`
	To               PrincipalRef   `json:"to"`
	Scope            AuthorityScope `json:"scope"`
	ExpiresAt        *time.Time     `json:"expires_at,omitempty"`
	Revoked          bool           `json:"revoked"`
	AllowFurther     bool           `json:"allow_further"`
}

type AuthorizationPolicy struct {
	PolicyDigest Digest                     `json:"policy_digest"`
	Revision     uint64                     `json:"revision"`
	Requirements DecisionPolicyRequirements `json:"requirements"`
	Grants       []AuthorityGrant           `json:"grants"`
	Delegations  []AuthorityDelegation      `json:"delegations"`
}

type DecisionPolicyRequirements struct {
	CompletionCriteriaRevision uint64            `json:"completion_criteria_revision"`
	AcceptancePolicyRevision   uint64            `json:"acceptance_policy_revision"`
	RequireQualifiedTree       bool              `json:"require_qualified_tree"`
	AttemptLimits              map[string]uint32 `json:"attempt_limits"`
	AllowUnchangedRetry        bool              `json:"allow_unchanged_retry"`
}

type AuthorizationResult struct {
	Allowed           bool     `json:"allowed"`
	CanReadTarget     bool     `json:"can_read_target"`
	ReasonCode        string   `json:"reason_code"`
	GrantDigests      []Digest `json:"grant_digests"`
	DelegationDigests []Digest `json:"delegation_digests"`
}

func (policy AuthorizationPolicy) Authorize(command KernelCommand, state *AggregateState, now time.Time) AuthorizationResult {
	if !policy.PolicyDigest.Valid() || policy.Revision == 0 || now.IsZero() {
		return AuthorizationResult{ReasonCode: "INVALID_POLICY"}
	}
	grants := append([]AuthorityGrant(nil), policy.Grants...)
	sort.Slice(grants, func(i, j int) bool { return grants[i].GrantDigest < grants[j].GrantDigest })
	delegations := append([]AuthorityDelegation(nil), policy.Delegations...)
	sort.Slice(delegations, func(i, j int) bool { return delegations[i].DelegationDigest < delegations[j].DelegationDigest })
	canRead := false
	for _, grant := range grants {
		if !grantValid(grant, now) {
			continue
		}
		if grant.Grantee == command.Authority && readScopeAllows(grant.Scope, command) {
			canRead = true
		}
		if grant.Grantee == command.Authority && scopeAllows(grant.Scope, command, state) {
			return AuthorizationResult{Allowed: true, CanReadTarget: grant.Scope.CanReadTarget, ReasonCode: "AUTHORIZED", GrantDigests: []Digest{grant.GrantDigest}}
		}
		path := map[PrincipalRef]struct{}{grant.Grantee: {}}
		if result, ok := authorizeDelegated(command, state, now, grant.Grantee, grant.GrantDigest, grant.Scope, grant.ExpiresAt, grant.AllowDelegation, 0, []Digest{grant.GrantDigest}, nil, path, delegations); ok {
			return result
		}
		if delegatedCanRead(command, now, grant.Grantee, grant.GrantDigest, grant.Scope, grant.ExpiresAt, grant.AllowDelegation, 0, path, delegations) {
			canRead = true
		}
	}
	return AuthorizationResult{CanReadTarget: canRead, ReasonCode: "NO_APPLICABLE_GRANT"}
}

func delegatedCanRead(command KernelCommand, now time.Time, current PrincipalRef, parentDigest Digest, parentScope AuthorityScope, parentExpiry *time.Time, canDelegate bool, depth int, path map[PrincipalRef]struct{}, delegations []AuthorityDelegation) bool {
	if !canDelegate || depth >= MaxDelegationDepth {
		return false
	}
	for _, delegation := range delegations {
		if delegation.From != current || delegation.ParentDigest != parentDigest || !delegationValid(delegation, parentScope, parentExpiry, now) {
			continue
		}
		if _, cycle := path[delegation.To]; cycle {
			continue
		}
		if delegation.To == command.Authority && readScopeAllows(delegation.Scope, command) {
			return true
		}
		nextPath := clonePrincipalSet(path)
		nextPath[delegation.To] = struct{}{}
		if delegatedCanRead(command, now, delegation.To, delegation.DelegationDigest, delegation.Scope, delegation.ExpiresAt, delegation.AllowFurther, depth+1, nextPath, delegations) {
			return true
		}
	}
	return false
}

func authorizeDelegated(command KernelCommand, state *AggregateState, now time.Time, current PrincipalRef, parentDigest Digest, parentScope AuthorityScope, parentExpiry *time.Time, canDelegate bool, depth int, grants, chain []Digest, path map[PrincipalRef]struct{}, delegations []AuthorityDelegation) (AuthorizationResult, bool) {
	if !canDelegate || depth >= MaxDelegationDepth {
		return AuthorizationResult{}, false
	}
	for _, delegation := range delegations {
		if delegation.From != current || delegation.ParentDigest != parentDigest || !delegationValid(delegation, parentScope, parentExpiry, now) {
			continue
		}
		if _, cycle := path[delegation.To]; cycle {
			continue
		}
		nextChain := append(append([]Digest(nil), chain...), delegation.DelegationDigest)
		if delegation.To == command.Authority && scopeAllows(delegation.Scope, command, state) {
			return AuthorizationResult{Allowed: true, CanReadTarget: delegation.Scope.CanReadTarget, ReasonCode: "AUTHORIZED", GrantDigests: append([]Digest(nil), grants...), DelegationDigests: nextChain}, true
		}
		nextPath := clonePrincipalSet(path)
		nextPath[delegation.To] = struct{}{}
		if result, ok := authorizeDelegated(command, state, now, delegation.To, delegation.DelegationDigest, delegation.Scope, delegation.ExpiresAt, delegation.AllowFurther, depth+1, grants, nextChain, nextPath, delegations); ok {
			return result, true
		}
	}
	return AuthorizationResult{}, false
}

func grantValid(grant AuthorityGrant, now time.Time) bool {
	return grant.GrantDigest.Valid() && grant.Grantee.Valid() && !grant.Revoked && !expired(grant.ExpiresAt, now) && scopeValid(grant.Scope)
}

func delegationValid(delegation AuthorityDelegation, parentScope AuthorityScope, parentExpiry *time.Time, now time.Time) bool {
	if !delegation.DelegationDigest.Valid() || !delegation.ParentDigest.Valid() || !delegation.From.Valid() || !delegation.To.Valid() || delegation.Revoked || expired(delegation.ExpiresAt, now) || !scopeValid(delegation.Scope) || !scopeSubset(delegation.Scope, parentScope) {
		return false
	}
	return parentExpiry == nil || (delegation.ExpiresAt != nil && !delegation.ExpiresAt.After(*parentExpiry))
}

func expired(expiry *time.Time, now time.Time) bool {
	return expiry != nil && !now.Before(*expiry)
}

func scopeAllows(scope AuthorityScope, command KernelCommand, state *AggregateState) bool {
	if !containsString(scope.CommandTypes, command.CommandType) || !containsAggregateKind(scope.TargetKinds, command.Target.Kind) {
		return false
	}
	if len(scope.TargetIDs) > 0 && !containsUUID(scope.TargetIDs, command.Target.ID) {
		return false
	}
	if command.CommandType == "tekroo.command.work.reopen" && state != nil && state.Phase == PhaseAccepted && !scope.CanReopenAccepted {
		return false
	}
	if scope.RequiresOwner {
		return state != nil && state.Ownership.OwnerFQN != nil && command.ActorFQN != nil && *state.Ownership.OwnerFQN == *command.ActorFQN
	}
	return true
}

func readScopeAllows(scope AuthorityScope, command KernelCommand) bool {
	if !scope.CanReadTarget || !containsAggregateKind(scope.TargetKinds, command.Target.Kind) {
		return false
	}
	return len(scope.TargetIDs) == 0 || containsUUID(scope.TargetIDs, command.Target.ID)
}

func scopeValid(scope AuthorityScope) bool {
	if len(scope.CommandTypes) == 0 || len(scope.TargetKinds) == 0 {
		return false
	}
	for _, commandType := range scope.CommandTypes {
		if commandType == "" {
			return false
		}
	}
	for _, kind := range scope.TargetKinds {
		if !kind.Valid() {
			return false
		}
	}
	for _, id := range scope.TargetIDs {
		if !id.Valid() {
			return false
		}
	}
	return true
}

func scopeSubset(child, parent AuthorityScope) bool {
	for _, commandType := range child.CommandTypes {
		if !containsString(parent.CommandTypes, commandType) {
			return false
		}
	}
	for _, kind := range child.TargetKinds {
		if !containsAggregateKind(parent.TargetKinds, kind) {
			return false
		}
	}
	if len(parent.TargetIDs) > 0 {
		if len(child.TargetIDs) == 0 {
			return false
		}
		for _, id := range child.TargetIDs {
			if !containsUUID(parent.TargetIDs, id) {
				return false
			}
		}
	}
	if child.CanReadTarget && !parent.CanReadTarget {
		return false
	}
	if parent.RequiresOwner && !child.RequiresOwner {
		return false
	}
	if child.CanReopenAccepted && !parent.CanReopenAccepted {
		return false
	}
	return true
}

func clonePrincipalSet(source map[PrincipalRef]struct{}) map[PrincipalRef]struct{} {
	copy := make(map[PrincipalRef]struct{}, len(source)+1)
	for principal := range source {
		copy[principal] = struct{}{}
	}
	return copy
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsAggregateKind(values []AggregateKind, target AggregateKind) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsUUID(values []UUIDv7, target UUIDv7) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
