package operationalruntime

import (
	"context"
	"testing"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestPlanningRoleAllocatorDistributesWorkAcrossConfiguredInstances(t *testing.T) {
	host := &allocationRoleHost{states: map[kernel.ActorFQN]organization.RoleInstanceState{
		"teams::coder-3": {ActorFQN: "teams::coder-3", Role: "coder", Status: organization.RoleIdle},
		"teams::coder-1": {ActorFQN: "teams::coder-1", Role: "coder", Status: organization.RoleIdle},
		"teams::coder-2": {ActorFQN: "teams::coder-2", Role: "coder", Status: organization.RoleIdle},
	}}
	allocator := &planningRoleAllocator{host: host, next: make(map[string]int)}
	want := []kernel.ActorFQN{"teams::coder-1", "teams::coder-2", "teams::coder-3", "teams::coder-1"}
	for index, expected := range want {
		owner, err := allocator.ensure(context.Background(), "coder")
		if err != nil {
			t.Fatal(err)
		}
		if owner.ActorFQN != expected {
			t.Fatalf("allocation %d = %s, want %s", index, owner.ActorFQN, expected)
		}
	}
	paused := host.states["teams::coder-2"]
	paused.Status = organization.RolePaused
	host.states["teams::coder-2"] = paused
	allocator = &planningRoleAllocator{host: host, next: make(map[string]int)}
	if _, err := allocator.ensure(context.Background(), "coder"); err != nil {
		t.Fatal(err)
	}
	owner, err := allocator.ensure(context.Background(), "coder")
	if err != nil || owner.ActorFQN != "teams::coder-3" {
		t.Fatalf("allocator did not skip unavailable instance: owner=%s err=%v", owner.ActorFQN, err)
	}
}

type allocationRoleHost struct {
	states map[kernel.ActorFQN]organization.RoleInstanceState
}

func (host *allocationRoleHost) ConfiguredRoleActors(role string) ([]kernel.ActorFQN, error) {
	result := make([]kernel.ActorFQN, 0)
	for actor, state := range host.states {
		if state.Role == role {
			result = append(result, actor)
		}
	}
	return result, nil
}

func (host *allocationRoleHost) EnsureStarted(_ context.Context, actor kernel.ActorFQN) (organization.RoleInstanceState, error) {
	return host.states[actor], nil
}

func (host *allocationRoleHost) ResolveRoleRecipients(context.Context, string) ([]kernel.ActorFQN, error) {
	return nil, nil
}

func (host *allocationRoleHost) Status(_ context.Context, actor kernel.ActorFQN) (organization.RoleInstanceState, bool, error) {
	state, found := host.states[actor]
	return state, found, nil
}
