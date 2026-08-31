package openhands

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/tekroo-ai/teams/kernel"
)

// BoundWorkspaceResolver is an immutable, exact workspace/worktree binding.
// It never derives a host path from model or task text.
type BoundWorkspaceResolver struct {
	bindings map[string]WorkspaceBinding
}

func NewBoundWorkspaceResolver(bindings []WorkspaceBinding) (*BoundWorkspaceResolver, error) {
	resolver := &BoundWorkspaceResolver{bindings: make(map[string]WorkspaceBinding, len(bindings))}
	if len(bindings) == 0 {
		return nil, ErrInvalidConfiguration
	}
	for _, binding := range bindings {
		if binding.WorkspaceID == "" || binding.WorktreeID == "" || !filepath.IsAbs(binding.WorkingDirectory) {
			return nil, ErrInvalidConfiguration
		}
		if _, duplicate := resolver.bindings[binding.WorkspaceID]; duplicate {
			return nil, ErrInvalidConfiguration
		}
		binding.WorkingDirectory = filepath.Clean(binding.WorkingDirectory)
		resolver.bindings[binding.WorkspaceID] = binding
	}
	return resolver, nil
}

func (resolver *BoundWorkspaceResolver) ResolveWorkspace(_ context.Context, scope kernel.TaskOperationalScope) (WorkspaceBinding, error) {
	if resolver == nil {
		return WorkspaceBinding{}, ErrInvalidConfiguration
	}
	binding, found := resolver.bindings[scope.WorkspaceID]
	if !found || binding.WorktreeID != scope.WorktreeID {
		return WorkspaceBinding{}, ErrProtocol
	}
	return binding, nil
}

type executionProfileKey struct {
	model   kernel.Digest
	runtime kernel.Digest
	tool    kernel.Digest
	effect  kernel.Digest
}

// BoundExecutionProfileResolver holds the exact qualified OpenHands settings
// for one model/runtime/tool/effect identity tuple.
type BoundExecutionProfileResolver struct {
	profiles map[executionProfileKey]ExecutionProfile
}

func NewBoundExecutionProfileResolver(profiles []ExecutionProfile) (*BoundExecutionProfileResolver, error) {
	resolver := &BoundExecutionProfileResolver{profiles: make(map[executionProfileKey]ExecutionProfile, len(profiles))}
	if len(profiles) == 0 {
		return nil, ErrInvalidConfiguration
	}
	for _, profile := range profiles {
		key := executionProfileKey{profile.ModelProfileDigest, profile.RuntimeIdentityDigest, profile.ToolPolicyDigest, profile.EffectPolicyDigest}
		if !key.model.Valid() || !key.runtime.Valid() || !key.tool.Valid() || !key.effect.Valid() || profile.MaxIterations == 0 || !profile.AgentDelegationDisabled || !jsonObject(profile.AgentSettings) || !qualifiedAgentSettings(profile.AgentSettings) || !jsonObject(profile.HookConfig) || containsDelegationTool(profile.AgentSettings) || !profile.SemanticMemory.valid(profile.HookConfig) {
			return nil, ErrInvalidConfiguration
		}
		if _, duplicate := resolver.profiles[key]; duplicate {
			return nil, ErrInvalidConfiguration
		}
		profile.AgentSettings = append(json.RawMessage(nil), profile.AgentSettings...)
		profile.HookConfig = append(json.RawMessage(nil), profile.HookConfig...)
		resolver.profiles[key] = profile
	}
	return resolver, nil
}

func (resolver *BoundExecutionProfileResolver) ResolveExecutionProfile(_ context.Context, model, runtime, tool, effect kernel.Digest) (ExecutionProfile, error) {
	if resolver == nil {
		return ExecutionProfile{}, ErrInvalidConfiguration
	}
	profile, found := resolver.profiles[executionProfileKey{model, runtime, tool, effect}]
	if !found {
		return ExecutionProfile{}, ErrProtocol
	}
	profile.AgentSettings = append(json.RawMessage(nil), profile.AgentSettings...)
	profile.HookConfig = append(json.RawMessage(nil), profile.HookConfig...)
	return profile, nil
}

var (
	_ WorkspaceResolver        = (*BoundWorkspaceResolver)(nil)
	_ ExecutionProfileResolver = (*BoundExecutionProfileResolver)(nil)
)
