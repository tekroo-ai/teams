package operationalruntime

import (
	"context"
	"fmt"
	"slices"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

type boundRoleDefinition struct {
	version      string
	digest       kernel.Digest
	capabilities []string
	permissions  []string
	instructions string
}

// boundRoleGroundingResolver resolves an actor FQN to its configured FQRN,
// then resolves that FQRN to the authenticated role bundle loaded with the
// team manifest. Executions fail closed at either missing link.
type boundRoleGroundingResolver struct {
	roleByActor map[kernel.ActorFQN]kernel.RoleFQRN
	byRole      map[kernel.RoleFQRN]boundRoleDefinition
}

func newBoundRoleGroundingResolver(team organization.LoadedTeam) (*boundRoleGroundingResolver, error) {
	if team.Manifest.Validate() != nil || !team.Digest.Valid() || len(team.Roles) != len(team.Manifest.Roles) {
		return nil, application.ErrInvalidConfiguration
	}
	resolver := &boundRoleGroundingResolver{
		roleByActor: make(map[kernel.ActorFQN]kernel.RoleFQRN),
		byRole:      make(map[kernel.RoleFQRN]boundRoleDefinition),
	}
	for _, loaded := range team.Roles {
		digest, err := loaded.Bundle.ContentDigest()
		if err != nil || loaded.Bundle.Validate() != nil || loaded.Binding.Role != loaded.Bundle.Role || loaded.Binding.BundleDigest != digest {
			return nil, application.ErrInvalidConfiguration
		}
		fqrn, err := kernel.ParseRoleFQRN(loaded.Binding.Role)
		if err != nil {
			return nil, application.ErrInvalidConfiguration
		}
		resolver.byRole[fqrn] = boundRoleDefinition{
			version: loaded.Bundle.Version, digest: loaded.Binding.BundleDigest,
			capabilities: slices.Clone(loaded.Bundle.Capabilities), permissions: slices.Clone(loaded.Bundle.Permissions),
			instructions: loaded.Bundle.Instructions,
		}
		for instance := uint32(1); instance <= loaded.Binding.MaximumInstances; instance++ {
			actor, err := kernel.ParseActorFQN(fmt.Sprintf("%s::%s-%d", team.Manifest.Team, loaded.Binding.Role, instance))
			if err != nil {
				return nil, application.ErrInvalidConfiguration
			}
			resolver.roleByActor[actor] = fqrn
		}
	}
	return resolver, nil
}

func (resolver *boundRoleGroundingResolver) ResolveRoleGrounding(_ context.Context, actor kernel.ActorFQN) (application.RoleExecutionGrounding, error) {
	if resolver == nil || !actor.Valid() {
		return application.RoleExecutionGrounding{}, application.ErrInvalidOperationalExecution
	}
	fqrn, found := resolver.roleByActor[actor]
	if !found {
		return application.RoleExecutionGrounding{}, application.ErrInvalidOperationalExecution
	}
	definition, found := resolver.byRole[fqrn]
	if !found {
		return application.RoleExecutionGrounding{}, application.ErrInvalidOperationalExecution
	}
	grounding := application.RoleExecutionGrounding{
		ActorFQN: actor, RoleFQRN: fqrn,
		BundleVersion: definition.version, BundleDigest: definition.digest,
		Capabilities: slices.Clone(definition.capabilities), Permissions: slices.Clone(definition.permissions),
		Instructions: definition.instructions,
	}
	if !grounding.Valid(actor) {
		return application.RoleExecutionGrounding{}, application.ErrInvalidOperationalExecution
	}
	return grounding, nil
}
