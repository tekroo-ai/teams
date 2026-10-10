package operationalruntime

import (
	"context"
	"fmt"

	"github.com/tekroo-ai/teams/adapters/nativeagent"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// Advisory work inherits the original admitted decision's minimum, not the
// currently selected model's route. A model rebind cannot lower that minimum.
// LoadOperationalExecution replays the source task at its invocation revision;
// classification evidence remains separate from the retained conversation.
func (service *ProductionService) retainedDecisionRoute(ctx context.Context, source kernel.WorkInvocation) (kernel.DecisionRoute, error) {
	bound, err := service.Store.LoadOperationalExecution(ctx, source.ID)
	if err != nil {
		return "", err
	}
	return decisionSourceRoute(source, bound)
}

func decisionSourceRoute(source kernel.WorkInvocation, bound application.OperationalExecutionContext) (kernel.DecisionRoute, error) {
	profile := bound.Profile.Profile
	if !source.Valid() || source.State != kernel.InvocationSucceeded || bound.Invocation.State != source.State || source.ID != bound.Invocation.ID ||
		source.TaskID != bound.Invocation.TaskID || source.TaskRevision != bound.Invocation.TaskRevision ||
		source.ActorFQN != bound.Invocation.ActorFQN || source.Execution != bound.Invocation.Execution ||
		source.ModelProfileDigest != bound.Invocation.ModelProfileDigest ||
		!bound.Profile.Valid() || profile.TaskID != source.TaskID || profile.Binding() != source.WorkProfile ||
		bound.Invocation.WorkProfile != source.WorkProfile ||
		!profile.MinimumDecisionRoute.ModelExecutable() {
		return "", fmt.Errorf("retained decision classification source %s: %w", source.ID, nativeagent.ErrInvalidBinding)
	}
	return profile.MinimumDecisionRoute, nil
}

func (service *ProductionService) qualifiedDecisionOwner(owner organization.RoleInstanceState, task organization.PlannedTask, source kernel.WorkInvocation) (ProductionProfile, ProductionWorkspace, error) {
	profile, err := service.qualifiedTaskOwnerProfile(owner, task)
	if err != nil {
		return ProductionProfile{}, ProductionWorkspace{}, err
	}
	workspace, exists := service.workspacesByID[owner.WorkspaceID]
	if owner.ActorFQN != source.ActorFQN || !exists || workspace.WorkspaceID != source.WorkspaceID {
		return ProductionProfile{}, ProductionWorkspace{}, fmt.Errorf("decision task %s source %s owner/workspace binding: %w", task.ID, source.ID, nativeagent.ErrInvalidBinding)
	}
	return profile, workspace, nil
}

// Shared owner-aware admission check for planned and generated work. Preserve the
// sentinel for callers, but expose the actual failed requirement in diagnostics.
func (service *ProductionService) qualifiedTaskOwnerProfile(owner organization.RoleInstanceState, item organization.PlannedTask) (ProductionProfile, error) {
	kind := workKindForPurpose(item.Purpose, item.Risk)
	reject := func(selected kernel.DecisionRoute, reason string) (ProductionProfile, error) {
		return ProductionProfile{}, fmt.Errorf("task %s owner %s role %s required_route=%s selected_route=%s work_kind=%s: %s: %w", item.ID, owner.ActorFQN, owner.Role, item.DecisionRoute, selected, kind, reason, organization.ErrInvalidFeature)
	}
	if service == nil || service.clock == nil || owner.ActorFQN != item.Owner {
		return reject("", "owner binding or admission clock unavailable")
	}
	// The plan's model digest is historical. A role may be rebound to a newer
	// qualified model; the live role identity and exact qualification must match.
	profile, found := service.profilesByModel[owner.ModelProfile]
	if !found {
		return reject("", "selected model profile unavailable")
	}
	if profile.RoleFQRN != kernel.RoleFQRN(owner.Role) {
		return reject(profile.DecisionRoute, "selected profile belongs to another role")
	}
	if reason := profile.qualificationFailure(item.DecisionRoute, kind, service.clock.Now().UTC()); reason != "" {
		return reject(profile.DecisionRoute, reason)
	}
	return profile, nil
}
