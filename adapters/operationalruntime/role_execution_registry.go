package operationalruntime

import (
	"context"
	"errors"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// productionFeatureRoleHost ensures feature-driven lifecycle starts cannot
// outrun the kernel execution registry.
type productionFeatureRoleHost struct {
	service *ProductionService
}

func (host productionFeatureRoleHost) ConfiguredRoleActors(role string) ([]kernel.ActorFQN, error) {
	return host.service.RoleHost.ConfiguredRoleActors(role)
}

func (host productionFeatureRoleHost) EnsureStarted(ctx context.Context, actor kernel.ActorFQN) (organization.RoleInstanceState, error) {
	return host.service.StartRole(ctx, actor)
}

func (host productionFeatureRoleHost) ResolveRoleRecipients(ctx context.Context, role string) ([]kernel.ActorFQN, error) {
	return host.service.RoleHost.ResolveRoleRecipients(ctx, role)
}

func (host productionFeatureRoleHost) Status(ctx context.Context, actor kernel.ActorFQN) (organization.RoleInstanceState, bool, error) {
	return host.service.RoleHost.Status(ctx, actor)
}

func (service *ProductionService) registerRoleState(ctx context.Context, state organization.RoleInstanceState) error {
	if service == nil || !state.ActorFQN.Valid() || !state.Execution.Valid() {
		return organization.ErrRoleRuntime
	}
	profile, found := service.profilesByModel[state.ModelProfile]
	if !found {
		return organization.ErrRoleNotConfigured
	}
	return service.registerExecution(ctx, state, profile)
}

// reconcileDurableRoleExecutions closes any registry transition persisted by
// the role host before a prior daemon stopped. It runs before eager roles are
// relaunched so fencing epochs remain contiguous.
func (service *ProductionService) reconcileDurableRoleExecutions(ctx context.Context) error {
	if service == nil || service.RoleHost == nil {
		return organization.ErrRoleRuntime
	}
	roster, err := service.RoleHost.Roster(ctx)
	if err != nil {
		return err
	}
	for _, state := range roster {
		if !state.Execution.Valid() {
			return errors.New("durable role state has no valid execution")
		}
		if err := service.registerRoleState(ctx, state); err != nil {
			return err
		}
	}
	return nil
}
