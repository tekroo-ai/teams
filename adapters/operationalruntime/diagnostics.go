package operationalruntime

import (
	"context"
	"sort"
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
	RecoveryFaults   []RecoveryFault                  `json:"recovery_faults"`
}

type RecoveryFault struct {
	Scope         string    `json:"scope"`
	Error         string    `json:"error"`
	FirstObserved time.Time `json:"first_observed"`
	LastObserved  time.Time `json:"last_observed"`
	Occurrences   uint64    `json:"occurrences"`
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
	return Diagnostics{ObservedAt: service.clock.Now().UTC(), Control: service.Status(), Roles: roles, ActiveTasks: activeTasks, PendingMessages: pending, ClaimedMessages: claimed, DeadLetters: deadLetters, ProjectionFaults: faults, RoleLibraries: service.RoleLibraries(), RecoveryFaults: service.readRecoveryFaults()}, nil
}

func (service *ProductionService) recordRecoveryFault(scope string, cause error) {
	if service == nil || scope == "" || cause == nil {
		return
	}
	now := time.Now().UTC()
	if service.clock != nil {
		now = service.clock.Now().UTC()
	}
	service.faultMu.Lock()
	defer service.faultMu.Unlock()
	if service.recoveryFaults == nil {
		service.recoveryFaults = make(map[string]RecoveryFault)
	}
	fault, found := service.recoveryFaults[scope]
	if !found || fault.Error != cause.Error() {
		fault = RecoveryFault{Scope: scope, Error: cause.Error(), FirstObserved: now}
	}
	fault.LastObserved = now
	fault.Occurrences++
	service.recoveryFaults[scope] = fault
}

func (service *ProductionService) clearRecoveryFault(scope string) {
	if service == nil || scope == "" {
		return
	}
	service.faultMu.Lock()
	defer service.faultMu.Unlock()
	delete(service.recoveryFaults, scope)
}

func (service *ProductionService) readRecoveryFaults() []RecoveryFault {
	service.faultMu.Lock()
	defer service.faultMu.Unlock()
	result := make([]RecoveryFault, 0, len(service.recoveryFaults))
	for _, fault := range service.recoveryFaults {
		result = append(result, fault)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Scope < result[right].Scope })
	return result
}

func (service *ProductionService) RepairDeadLetter(ctx context.Context, failedID kernel.UUIDv7, successor organization.OrganizationalMessage) error {
	if service == nil || service.MessageBus == nil {
		return application.ErrInvalidConfiguration
	}
	return service.MessageBus.RepairDeadLetter(ctx, failedID, successor)
}
