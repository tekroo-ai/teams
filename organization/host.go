package organization

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

var (
	ErrRoleNotConfigured = errors.New("role is not configured")
	ErrRoleStateConflict = errors.New("role state conflict")
	ErrRoleNotRunning    = errors.New("role is not running")
	ErrRoleRuntime       = errors.New("role runtime failure")
)

type RoleStatus string

const (
	RoleStarting RoleStatus = "STARTING"
	RoleIdle     RoleStatus = "IDLE"
	RolePaused   RoleStatus = "PAUSED"
	RoleStopping RoleStatus = "STOPPING"
	RoleStopped  RoleStatus = "STOPPED"
	RoleFailed   RoleStatus = "FAILED"
)

func (status RoleStatus) Valid() bool {
	switch status {
	case RoleStarting, RoleIdle, RolePaused, RoleStopping, RoleStopped, RoleFailed:
		return true
	default:
		return false
	}
}

type RoleInstanceState struct {
	ActorFQN          kernel.ActorFQN       `json:"actor_fqn"`
	Team              string                `json:"team"`
	Role              string                `json:"role"`
	Instance          uint32                `json:"instance"`
	Revision          uint64                `json:"revision"`
	BundleDigest      kernel.Digest         `json:"bundle_digest"`
	ModelProfile      kernel.Digest         `json:"model_profile_digest"`
	WorkspaceID       string                `json:"workspace_id"`
	Status            RoleStatus            `json:"status"`
	Execution         kernel.ExecutionTuple `json:"execution"`
	ProcessIdentity   string                `json:"process_identity,omitempty"`
	StartedAt         time.Time             `json:"started_at,omitempty"`
	StoppedAt         time.Time             `json:"stopped_at,omitempty"`
	LastHeartbeatAt   time.Time             `json:"last_heartbeat_at,omitempty"`
	CheckpointDigest  kernel.Digest         `json:"checkpoint_digest,omitempty"`
	LastFailure       string                `json:"last_failure,omitempty"`
	ManifestDigest    kernel.Digest         `json:"manifest_digest"`
	ManifestVersion   string                `json:"manifest_version"`
	BundleVersion     string                `json:"bundle_version"`
	RuntimeGeneration uint64                `json:"runtime_generation"`
}

func (state RoleInstanceState) Valid() bool {
	if !state.ActorFQN.Valid() || !namePattern.MatchString(state.Team) || !namePattern.MatchString(state.Role) || state.Instance == 0 || state.Revision == 0 || !state.BundleDigest.Valid() || !state.ModelProfile.Valid() || state.WorkspaceID == "" || len(state.WorkspaceID) > 1024 || !state.Status.Valid() || !state.ManifestDigest.Valid() || !versionPattern.MatchString(state.ManifestVersion) || !versionPattern.MatchString(state.BundleVersion) {
		return false
	}
	expected, err := kernel.ParseActorFQN(fmt.Sprintf("%s::%s-%d", state.Team, state.Role, state.Instance))
	if err != nil || expected != state.ActorFQN {
		return false
	}
	if state.Status == RoleStopped {
		return state.ProcessIdentity == ""
	}
	return state.Execution.Valid() && state.RuntimeGeneration == state.Execution.FencingEpoch
}

type RoleStateStore interface {
	LoadRole(context.Context, kernel.ActorFQN) (RoleInstanceState, bool, error)
	CompareAndSwapRole(context.Context, uint64, RoleInstanceState) error
	ListRoles(context.Context, string) ([]RoleInstanceState, error)
}

type RuntimeState string

const (
	RuntimeRunning RuntimeState = "RUNNING"
	RuntimeStopped RuntimeState = "STOPPED"
	RuntimeUnknown RuntimeState = "UNKNOWN"
)

type RuntimeObservation struct {
	ActorFQN        kernel.ActorFQN       `json:"actor_fqn"`
	Execution       kernel.ExecutionTuple `json:"execution"`
	State           RuntimeState          `json:"state"`
	ProcessIdentity string                `json:"process_identity,omitempty"`
}

func (observation RuntimeObservation) matches(actor kernel.ActorFQN, execution kernel.ExecutionTuple) bool {
	return observation.ActorFQN == actor && observation.Execution == execution
}

type StartRoleRequest struct {
	ActorFQN        kernel.ActorFQN
	Execution       kernel.ExecutionTuple
	Bundle          RoleBundle
	BundleDigest    kernel.Digest
	ModelProfile    kernel.Digest
	WorkspaceID     string
	IdempotencyKey  string
	ManifestDigest  kernel.Digest
	ManifestVersion string
}

type RoleRuntime interface {
	Start(context.Context, StartRoleRequest) (RuntimeObservation, error)
	Stop(context.Context, kernel.ActorFQN, kernel.ExecutionTuple) (RuntimeObservation, error)
	Inspect(context.Context, kernel.ActorFQN, kernel.ExecutionTuple) (RuntimeObservation, error)
}

type Host struct {
	team    LoadedTeam
	store   RoleStateStore
	runtime RoleRuntime
	clock   kernel.Clock
	ids     kernel.IDSource
	mu      sync.Mutex
}

func NewHost(team LoadedTeam, store RoleStateStore, runtime RoleRuntime, clock kernel.Clock, ids kernel.IDSource) (*Host, error) {
	if team.Manifest.Validate() != nil || !team.Digest.Valid() || len(team.Roles) != len(team.Manifest.Roles) || store == nil || runtime == nil || clock == nil || ids == nil {
		return nil, ErrInvalidTeamManifest
	}
	return &Host{team: team, store: store, runtime: runtime, clock: clock, ids: ids}, nil
}

func (host *Host) Start(ctx context.Context, actor kernel.ActorFQN) (RoleInstanceState, error) {
	host.mu.Lock()
	defer host.mu.Unlock()
	binding, bundle, instance, err := host.configured(actor)
	if err != nil {
		return RoleInstanceState{}, err
	}
	prior, found, err := host.store.LoadRole(ctx, actor)
	if err != nil {
		return RoleInstanceState{}, err
	}
	if found && (prior.Status == RoleIdle || prior.Status == RolePaused || prior.Status == RoleStarting) {
		return prior, ErrRoleStateConflict
	}
	executionID, err := host.ids.Next()
	if err != nil {
		return RoleInstanceState{}, err
	}
	epoch := uint64(1)
	expectedRevision := uint64(0)
	if found {
		epoch = prior.Execution.FencingEpoch + 1
		expectedRevision = prior.Revision
	}
	now := host.clock.Now().UTC()
	starting := RoleInstanceState{
		ActorFQN: actor, Team: host.team.Manifest.Team, Role: binding.Role, Instance: instance,
		Revision: expectedRevision + 1, BundleDigest: binding.BundleDigest, ModelProfile: binding.ModelProfileDigest,
		WorkspaceID: binding.WorkspaceIDs[instance-1], Status: RoleStarting,
		Execution: kernel.ExecutionTuple{ExecutionID: executionID, FencingEpoch: epoch}, StartedAt: now,
		ManifestDigest: host.team.Digest, ManifestVersion: host.team.Manifest.Version,
		BundleVersion: bundle.Version, RuntimeGeneration: epoch,
	}
	if err := host.store.CompareAndSwapRole(ctx, expectedRevision, starting); err != nil {
		return RoleInstanceState{}, err
	}
	request := StartRoleRequest{
		ActorFQN: actor, Execution: starting.Execution, Bundle: bundle, BundleDigest: starting.BundleDigest,
		ModelProfile: starting.ModelProfile, WorkspaceID: starting.WorkspaceID,
		IdempotencyKey: fmt.Sprintf("role-start:%s:%s", actor, executionID),
		ManifestDigest: host.team.Digest, ManifestVersion: host.team.Manifest.Version,
	}
	observation, startErr := host.runtime.Start(ctx, request)
	if startErr != nil || !observation.matches(actor, starting.Execution) || observation.State != RuntimeRunning || observation.ProcessIdentity == "" {
		observed, inspectErr := host.runtime.Inspect(context.WithoutCancel(ctx), actor, starting.Execution)
		if inspectErr == nil && observed.matches(actor, starting.Execution) && observed.State == RuntimeRunning && observed.ProcessIdentity != "" {
			observation = observed
			startErr = nil
		} else {
			failed := starting
			failed.Revision++
			failed.Status = RoleFailed
			failed.LastFailure = "runtime start did not establish an exact running process"
			_ = host.store.CompareAndSwapRole(context.WithoutCancel(ctx), starting.Revision, failed)
			if startErr == nil {
				startErr = inspectErr
			}
			return failed, errors.Join(ErrRoleRuntime, startErr)
		}
	}
	running := starting
	running.Revision++
	running.Status = RoleIdle
	running.ProcessIdentity = observation.ProcessIdentity
	running.LastHeartbeatAt = host.clock.Now().UTC()
	if err := host.store.CompareAndSwapRole(ctx, starting.Revision, running); err != nil {
		return RoleInstanceState{}, err
	}
	return running, nil
}

func (host *Host) Stop(ctx context.Context, actor kernel.ActorFQN) (RoleInstanceState, error) {
	host.mu.Lock()
	defer host.mu.Unlock()
	if _, _, _, err := host.configured(actor); err != nil {
		return RoleInstanceState{}, err
	}
	current, found, err := host.store.LoadRole(ctx, actor)
	if err != nil {
		return RoleInstanceState{}, err
	}
	if !found || current.Status == RoleStopped {
		return current, ErrRoleNotRunning
	}
	if current.Status != RoleIdle && current.Status != RolePaused && current.Status != RoleFailed {
		return current, ErrRoleStateConflict
	}
	stopping := current
	stopping.Revision++
	stopping.Status = RoleStopping
	if err := host.store.CompareAndSwapRole(ctx, current.Revision, stopping); err != nil {
		return RoleInstanceState{}, err
	}
	observation, stopErr := host.runtime.Stop(ctx, actor, current.Execution)
	if stopErr != nil || !observation.matches(actor, current.Execution) || observation.State != RuntimeStopped {
		observed, inspectErr := host.runtime.Inspect(context.WithoutCancel(ctx), actor, current.Execution)
		if inspectErr == nil && observed.matches(actor, current.Execution) && observed.State == RuntimeStopped {
			observation = observed
			stopErr = nil
		} else {
			failed := stopping
			failed.Revision++
			failed.Status = RoleFailed
			failed.LastFailure = "runtime stop could not prove process termination"
			_ = host.store.CompareAndSwapRole(context.WithoutCancel(ctx), stopping.Revision, failed)
			if stopErr == nil {
				stopErr = inspectErr
			}
			return failed, errors.Join(ErrRoleRuntime, stopErr)
		}
	}
	stopped := stopping
	stopped.Revision++
	stopped.Status = RoleStopped
	stopped.ProcessIdentity = ""
	stopped.StoppedAt = host.clock.Now().UTC()
	stopped.LastHeartbeatAt = time.Time{}
	stopped.LastFailure = ""
	if err := host.store.CompareAndSwapRole(ctx, stopping.Revision, stopped); err != nil {
		return RoleInstanceState{}, err
	}
	return stopped, nil
}

func (host *Host) Restart(ctx context.Context, actor kernel.ActorFQN) (RoleInstanceState, error) {
	current, found, err := host.store.LoadRole(ctx, actor)
	if err != nil {
		return RoleInstanceState{}, err
	}
	if found && current.Status != RoleStopped {
		if _, err := host.Stop(ctx, actor); err != nil {
			return RoleInstanceState{}, err
		}
	}
	return host.Start(ctx, actor)
}

func (host *Host) Pause(ctx context.Context, actor kernel.ActorFQN) (RoleInstanceState, error) {
	return host.transition(ctx, actor, RoleIdle, RolePaused)
}

func (host *Host) Resume(ctx context.Context, actor kernel.ActorFQN) (RoleInstanceState, error) {
	return host.transition(ctx, actor, RolePaused, RoleIdle)
}

func (host *Host) transition(ctx context.Context, actor kernel.ActorFQN, from, to RoleStatus) (RoleInstanceState, error) {
	host.mu.Lock()
	defer host.mu.Unlock()
	if _, _, _, err := host.configured(actor); err != nil {
		return RoleInstanceState{}, err
	}
	current, found, err := host.store.LoadRole(ctx, actor)
	if err != nil {
		return RoleInstanceState{}, err
	}
	if !found || current.Status != from {
		return current, ErrRoleStateConflict
	}
	next := current
	next.Revision++
	next.Status = to
	if err := host.store.CompareAndSwapRole(ctx, current.Revision, next); err != nil {
		return RoleInstanceState{}, err
	}
	return next, nil
}

func (host *Host) Heartbeat(ctx context.Context, actor kernel.ActorFQN, execution kernel.ExecutionTuple) (RoleInstanceState, error) {
	host.mu.Lock()
	defer host.mu.Unlock()
	current, found, err := host.store.LoadRole(ctx, actor)
	if err != nil {
		return RoleInstanceState{}, err
	}
	if !found || current.Execution != execution || current.Status != RoleIdle && current.Status != RolePaused {
		return current, ErrRoleStateConflict
	}
	next := current
	next.Revision++
	next.LastHeartbeatAt = host.clock.Now().UTC()
	if err := host.store.CompareAndSwapRole(ctx, current.Revision, next); err != nil {
		return RoleInstanceState{}, err
	}
	return next, nil
}

func (host *Host) Checkpoint(ctx context.Context, actor kernel.ActorFQN, execution kernel.ExecutionTuple, digest kernel.Digest) (RoleInstanceState, error) {
	if !digest.Valid() {
		return RoleInstanceState{}, ErrRoleStateConflict
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	current, found, err := host.store.LoadRole(ctx, actor)
	if err != nil {
		return RoleInstanceState{}, err
	}
	if !found || current.Execution != execution || current.Status != RoleIdle && current.Status != RolePaused {
		return current, ErrRoleStateConflict
	}
	next := current
	next.Revision++
	next.CheckpointDigest = digest
	if err := host.store.CompareAndSwapRole(ctx, current.Revision, next); err != nil {
		return RoleInstanceState{}, err
	}
	return next, nil
}

func (host *Host) Status(ctx context.Context, actor kernel.ActorFQN) (RoleInstanceState, bool, error) {
	return host.store.LoadRole(ctx, actor)
}

func (host *Host) Roster(ctx context.Context) ([]RoleInstanceState, error) {
	states, err := host.store.ListRoles(ctx, host.team.Manifest.Team)
	if err != nil {
		return nil, err
	}
	sort.Slice(states, func(left, right int) bool { return states[left].ActorFQN < states[right].ActorFQN })
	return states, nil
}

func (host *Host) StartEager(ctx context.Context) ([]RoleInstanceState, error) {
	result := make([]RoleInstanceState, 0)
	for _, role := range host.team.Roles {
		if role.Binding.LaunchMode != LaunchEager {
			continue
		}
		for instance := uint32(1); instance <= role.Binding.InitialInstances; instance++ {
			actor, _ := kernel.ParseActorFQN(fmt.Sprintf("%s::%s-%d", host.team.Manifest.Team, role.Binding.Role, instance))
			state, err := host.EnsureStarted(ctx, actor)
			if err != nil {
				return result, err
			}
			result = append(result, state)
		}
	}
	return result, nil
}

// EnsureStarted reconciles durable role state with the exact runtime process
// before starting a replacement. This is the normal process-restart path: an
// actor keeps its FQN and workspace while receiving a new fenced execution.
func (host *Host) EnsureStarted(ctx context.Context, actor kernel.ActorFQN) (RoleInstanceState, error) {
	if _, _, _, err := host.configured(actor); err != nil {
		return RoleInstanceState{}, err
	}
	current, found, err := host.store.LoadRole(ctx, actor)
	if err != nil {
		return RoleInstanceState{}, err
	}
	if !found || current.Status == RoleStopped || current.Status == RoleFailed {
		return host.Start(ctx, actor)
	}
	if current.Status != RoleStarting && current.Status != RoleIdle && current.Status != RolePaused && current.Status != RoleStopping {
		return current, ErrRoleStateConflict
	}
	observed, err := host.runtime.Inspect(ctx, actor, current.Execution)
	if err == nil && observed.matches(actor, current.Execution) && observed.State == RuntimeRunning && observed.ProcessIdentity != "" {
		return current, nil
	}
	if err != nil || !observed.matches(actor, current.Execution) || observed.State != RuntimeStopped {
		return current, errors.Join(ErrRoleRuntime, err)
	}

	host.mu.Lock()
	reloaded, stillFound, loadErr := host.store.LoadRole(ctx, actor)
	if loadErr != nil {
		host.mu.Unlock()
		return RoleInstanceState{}, loadErr
	}
	if !stillFound || reloaded.Revision != current.Revision || reloaded.Execution != current.Execution {
		host.mu.Unlock()
		return reloaded, ErrRoleStateConflict
	}
	stopped := reloaded
	stopped.Revision++
	stopped.Status = RoleStopped
	stopped.ProcessIdentity = ""
	stopped.StoppedAt = host.clock.Now().UTC()
	stopped.LastHeartbeatAt = time.Time{}
	stopped.LastFailure = ""
	casErr := host.store.CompareAndSwapRole(ctx, reloaded.Revision, stopped)
	host.mu.Unlock()
	if casErr != nil {
		return RoleInstanceState{}, casErr
	}
	return host.Start(ctx, actor)
}

// Reconcile proves that a durably active role still has its exact fenced
// process. A missing process is replaced once; callers own the retry bound.
// An interrupted stop is completed without relaunching the role.
func (host *Host) Reconcile(ctx context.Context, actor kernel.ActorFQN) (RoleInstanceState, bool, error) {
	current, found, err := host.store.LoadRole(ctx, actor)
	if err != nil || !found {
		return current, false, err
	}
	if current.Status == RoleStopped {
		return current, false, nil
	}
	if current.Status == RoleFailed {
		recovered, recoverErr := host.EnsureStarted(ctx, actor)
		return recovered, recoverErr == nil, recoverErr
	}
	observed, inspectErr := host.runtime.Inspect(ctx, actor, current.Execution)
	if inspectErr == nil && observed.matches(actor, current.Execution) && observed.State == RuntimeRunning && observed.ProcessIdentity != "" {
		if current.Status == RoleIdle || current.Status == RolePaused {
			heartbeat, heartbeatErr := host.Heartbeat(ctx, actor, current.Execution)
			return heartbeat, false, heartbeatErr
		}
		return current, false, nil
	}
	if inspectErr != nil || !observed.matches(actor, current.Execution) || observed.State != RuntimeStopped {
		return current, false, errors.Join(ErrRoleRuntime, inspectErr)
	}
	if current.Status == RoleStopping {
		host.mu.Lock()
		defer host.mu.Unlock()
		reloaded, stillFound, loadErr := host.store.LoadRole(ctx, actor)
		if loadErr != nil || !stillFound || reloaded.Revision != current.Revision {
			return reloaded, false, errors.Join(ErrRoleStateConflict, loadErr)
		}
		stopped := reloaded
		stopped.Revision++
		stopped.Status = RoleStopped
		stopped.ProcessIdentity = ""
		stopped.StoppedAt = host.clock.Now().UTC()
		stopped.LastHeartbeatAt = time.Time{}
		if err := host.store.CompareAndSwapRole(ctx, reloaded.Revision, stopped); err != nil {
			return RoleInstanceState{}, false, err
		}
		return stopped, false, nil
	}
	wasPaused := current.Status == RolePaused
	recovered, err := host.EnsureStarted(ctx, actor)
	if err != nil {
		return recovered, false, err
	}
	if wasPaused {
		recovered, err = host.Pause(ctx, actor)
	}
	return recovered, true, err
}

func (host *Host) configured(actor kernel.ActorFQN) (RoleBinding, RoleBundle, uint32, error) {
	prefix := host.team.Manifest.Team + "::"
	text := string(actor)
	if !actor.Valid() || !stringsHasPrefix(text, prefix) {
		return RoleBinding{}, RoleBundle{}, 0, ErrRoleNotConfigured
	}
	for _, role := range host.team.Roles {
		for instance := uint32(1); instance <= role.Binding.MaximumInstances; instance++ {
			expected := fmt.Sprintf("%s::%s-%d", host.team.Manifest.Team, role.Binding.Role, instance)
			if text == expected {
				return role.Binding, role.Bundle, instance, nil
			}
		}
	}
	return RoleBinding{}, RoleBundle{}, 0, ErrRoleNotConfigured
}

func stringsHasPrefix(value, prefix string) bool {
	return len(value) >= len(prefix) && value[:len(prefix)] == prefix
}
