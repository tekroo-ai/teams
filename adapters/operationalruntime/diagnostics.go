package operationalruntime

import (
	"context"
	"time"

	"github.com/tekroo-ai/teams/adapters/mongo"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

type Diagnostics struct {
	ObservedAt       time.Time                        `json:"observed_at"`
	Control          ControlStatus                    `json:"control"`
	Roles            []organization.RoleInstanceState `json:"roles"`
	ActiveTasks      []mongo.TaskProjection           `json:"active_tasks"`
	PendingMessages  []organization.MessageClaim      `json:"pending_messages"`
	ClaimedMessages  []organization.MessageClaim      `json:"claimed_messages"`
	DeadLetters      []organization.MessageClaim      `json:"dead_letters"`
	ProjectionFaults []mongo.ProjectionFault          `json:"projection_faults"`
	RoleLibraries    []organization.RoleLibraryEntry  `json:"role_libraries"`
}

func (service *ProductionService) Diagnostics(ctx context.Context) (Diagnostics, error) {
	if service == nil || service.Store == nil || service.RoleHost == nil || service.clock == nil {
		return Diagnostics{}, application.ErrInvalidConfiguration
	}
	roles, err := service.RoleHost.Roster(ctx)
	if err != nil {
		return Diagnostics{}, err
	}
	activeTasks, err := service.Store.ListActiveTaskProjections(ctx, 1000)
	if err != nil {
		return Diagnostics{}, err
	}
	pending, err := service.Store.ListMessageClaims(ctx, organization.MessagePending, 1000)
	if err != nil {
		return Diagnostics{}, err
	}
	claimed, err := service.Store.ListMessageClaims(ctx, organization.MessageClaimed, 1000)
	if err != nil {
		return Diagnostics{}, err
	}
	deadLetters, err := service.Store.ListDeadLetters(ctx, "", 1000)
	if err != nil {
		return Diagnostics{}, err
	}
	faults, err := service.Store.ReadProjectionFaults(ctx)
	if err != nil {
		return Diagnostics{}, err
	}
	return Diagnostics{ObservedAt: service.clock.Now().UTC(), Control: service.Status(), Roles: roles, ActiveTasks: activeTasks, PendingMessages: pending, ClaimedMessages: claimed, DeadLetters: deadLetters, ProjectionFaults: faults, RoleLibraries: service.RoleLibraries()}, nil
}

func (service *ProductionService) RepairDeadLetter(ctx context.Context, failedID kernel.UUIDv7, successor organization.OrganizationalMessage) error {
	if service == nil || service.MessageBus == nil {
		return application.ErrInvalidConfiguration
	}
	return service.MessageBus.RepairDeadLetter(ctx, failedID, successor)
}
