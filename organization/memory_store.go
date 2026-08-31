package organization

import (
	"context"
	"sort"
	"sync"

	"github.com/tekroo-ai/teams/kernel"
)

type MemoryRoleStore struct {
	mu     sync.Mutex
	states map[kernel.ActorFQN]RoleInstanceState
}

func NewMemoryRoleStore() *MemoryRoleStore {
	return &MemoryRoleStore{states: make(map[kernel.ActorFQN]RoleInstanceState)}
}

func (store *MemoryRoleStore) LoadRole(ctx context.Context, actor kernel.ActorFQN) (RoleInstanceState, bool, error) {
	if err := ctx.Err(); err != nil {
		return RoleInstanceState{}, false, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	state, found := store.states[actor]
	return state, found, nil
}

func (store *MemoryRoleStore) CompareAndSwapRole(ctx context.Context, expectedRevision uint64, next RoleInstanceState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !next.Valid() || next.Revision != expectedRevision+1 {
		return ErrRoleStateConflict
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	current, found := store.states[next.ActorFQN]
	if !found && expectedRevision != 0 || found && current.Revision != expectedRevision {
		return ErrRoleStateConflict
	}
	store.states[next.ActorFQN] = next
	return nil
}

func (store *MemoryRoleStore) ListRoles(ctx context.Context, team string) ([]RoleInstanceState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	result := make([]RoleInstanceState, 0)
	for _, state := range store.states {
		if state.Team == team {
			result = append(result, state)
		}
	}
	sort.Slice(result, func(left, right int) bool { return result[left].ActorFQN < result[right].ActorFQN })
	return result, nil
}
