package organization

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/fake"
	"github.com/tekroo-ai/teams/kernel"
)

func TestHostRestartPreservesFQNAndFencesPriorExecution(t *testing.T) {
	actor := kernel.ActorFQN("teams::coder-1")
	clock := fake.NewClock(time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC))
	ids := fake.NewIDSource(
		kernel.UUIDv7("00000000-0000-7000-8000-000000001001"),
		kernel.UUIDv7("00000000-0000-7000-8000-000000001002"),
	)
	runtime := newFakeRoleRuntime()
	host, err := NewHost(testLoadedTeam(), NewMemoryRoleStore(), runtime, clock, ids)
	if err != nil {
		t.Fatal(err)
	}
	first, err := host.Start(context.Background(), actor)
	if err != nil {
		t.Fatal(err)
	}
	if first.ActorFQN != actor || first.Status != RoleIdle || first.Execution.FencingEpoch != 1 {
		t.Fatalf("unexpected first state: %+v", first)
	}
	second, err := host.Restart(context.Background(), actor)
	if err != nil {
		t.Fatal(err)
	}
	if second.ActorFQN != actor || second.Execution.ExecutionID == first.Execution.ExecutionID || second.Execution.FencingEpoch != 2 || second.WorkspaceID != first.WorkspaceID {
		t.Fatalf("continuity/fencing failure: first=%+v second=%+v", first, second)
	}
	if _, err := host.Heartbeat(context.Background(), actor, first.Execution); !errors.Is(err, ErrRoleStateConflict) {
		t.Fatalf("stale execution heartbeat was not fenced: %v", err)
	}
}

func TestHostLifecycleAndCheckpoint(t *testing.T) {
	actor := kernel.ActorFQN("teams::coder-1")
	clock := fake.NewClock(time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC))
	host, err := NewHost(testLoadedTeam(), NewMemoryRoleStore(), newFakeRoleRuntime(), clock, fake.NewIDSource(kernel.UUIDv7("00000000-0000-7000-8000-000000001003")))
	if err != nil {
		t.Fatal(err)
	}
	started, err := host.Start(context.Background(), actor)
	if err != nil {
		t.Fatal(err)
	}
	paused, err := host.Pause(context.Background(), actor)
	if err != nil || paused.Status != RolePaused {
		t.Fatalf("pause: state=%+v err=%v", paused, err)
	}
	digest := kernel.Digest(repeat("c", 64))
	checkpointed, err := host.Checkpoint(context.Background(), actor, started.Execution, digest)
	if err != nil || checkpointed.CheckpointDigest != digest {
		t.Fatalf("checkpoint: state=%+v err=%v", checkpointed, err)
	}
	resumed, err := host.Resume(context.Background(), actor)
	if err != nil || resumed.Status != RoleIdle {
		t.Fatalf("resume: state=%+v err=%v", resumed, err)
	}
	stopped, err := host.Stop(context.Background(), actor)
	if err != nil || stopped.Status != RoleStopped || stopped.ProcessIdentity != "" {
		t.Fatalf("stop: state=%+v err=%v", stopped, err)
	}
}

func TestHostReconcilesUncertainStartAndFailsClosedOnUncertainStop(t *testing.T) {
	actor := kernel.ActorFQN("teams::coder-1")
	clock := fake.NewClock(time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC))
	runtime := newFakeRoleRuntime()
	runtime.startErr = errors.New("acknowledgement lost")
	host, err := NewHost(testLoadedTeam(), NewMemoryRoleStore(), runtime, clock, fake.NewIDSource(kernel.UUIDv7("00000000-0000-7000-8000-000000001004")))
	if err != nil {
		t.Fatal(err)
	}
	state, err := host.Start(context.Background(), actor)
	if err != nil || state.Status != RoleIdle {
		t.Fatalf("uncertain start was not reconciled: state=%+v err=%v", state, err)
	}
	runtime.stopLeavesRunning = true
	failed, err := host.Stop(context.Background(), actor)
	if !errors.Is(err, ErrRoleRuntime) || failed.Status != RoleFailed {
		t.Fatalf("uncertain stop did not fail closed: state=%+v err=%v", failed, err)
	}
}

func TestEnsureStartedReplacesStaleProcessWithoutChangingActorOrWorkspace(t *testing.T) {
	actor := kernel.ActorFQN("teams::coder-1")
	clock := fake.NewClock(time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC))
	store := NewMemoryRoleStore()
	firstHost, err := NewHost(testLoadedTeam(), store, newFakeRoleRuntime(), clock, fake.NewIDSource(kernel.UUIDv7("00000000-0000-7000-8000-000000001005")))
	if err != nil {
		t.Fatal(err)
	}
	first, err := firstHost.Start(context.Background(), actor)
	if err != nil {
		t.Fatal(err)
	}
	secondHost, err := NewHost(testLoadedTeam(), store, newFakeRoleRuntime(), clock, fake.NewIDSource(kernel.UUIDv7("00000000-0000-7000-8000-000000001006")))
	if err != nil {
		t.Fatal(err)
	}
	second, err := secondHost.EnsureStarted(context.Background(), actor)
	if err != nil {
		t.Fatal(err)
	}
	if second.ActorFQN != first.ActorFQN || second.WorkspaceID != first.WorkspaceID || second.Execution.FencingEpoch != first.Execution.FencingEpoch+1 || second.Execution.ExecutionID == first.Execution.ExecutionID {
		t.Fatalf("restart continuity/fencing failure: first=%+v second=%+v", first, second)
	}
}

func TestRoleWideResolutionNeverLaunchesAndReturnsOnlyExactActiveActors(t *testing.T) {
	runtime := newFakeRoleRuntime()
	host, err := NewHost(testLoadedTeam(), NewMemoryRoleStore(), runtime, fake.NewClock(time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)), fake.NewIDSource(kernel.UUIDv7("00000000-0000-7000-8000-000000001007")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.ResolveRoleRecipients(context.Background(), "coder"); !errors.Is(err, ErrRoleNotRunning) {
		t.Fatalf("inactive role resolution error=%v", err)
	}
	if len(runtime.observations) != 0 {
		t.Fatalf("resolution launched a role: %+v", runtime.observations)
	}
	actor := kernel.ActorFQN("teams::coder-1")
	if _, err := host.Start(context.Background(), actor); err != nil {
		t.Fatal(err)
	}
	resolved, err := host.ResolveRoleRecipients(context.Background(), "coder")
	if err != nil || len(resolved) != 1 || resolved[0] != actor {
		t.Fatalf("resolved=%v err=%v", resolved, err)
	}
}

func TestConfiguredCapabilityActorsUsesVerifiedRoleBundles(t *testing.T) {
	team := testLoadedTeam()
	bundle := testBundle()
	bundle.Role = "senior-coder"
	bundle.Capabilities = []string{"complex-implementation", "technical-review"}
	binding := RoleBinding{
		Role: "senior-coder", BundlePath: "roles/senior-coder.json", BundleDigest: kernel.Digest(repeat("e", 64)), PublisherKeyID: "test",
		InitialInstances: 1, MaximumInstances: 2, LaunchMode: LaunchOnDemand,
		ModelProfileDigest: kernel.Digest(repeat("f", 64)), WorkspaceIDs: []string{"senior-coder-1", "senior-coder-2"},
	}
	team.Manifest.Roles = append(team.Manifest.Roles, binding)
	team.Roles = append(team.Roles, LoadedRole{Binding: binding, Bundle: bundle})
	host, err := NewHost(team, NewMemoryRoleStore(), newFakeRoleRuntime(), fake.NewClock(time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)), fake.NewIDSource())
	if err != nil {
		t.Fatal(err)
	}
	actors, err := host.ConfiguredCapabilityActors("technical-review")
	if err != nil || len(actors) != 2 || actors[0] != "teams::senior-coder-1" || actors[1] != "teams::senior-coder-2" {
		t.Fatalf("technical-review actors=%v err=%v", actors, err)
	}
	if _, err := host.ConfiguredCapabilityActors("missing-capability"); !errors.Is(err, ErrRoleNotConfigured) {
		t.Fatalf("missing capability error=%v", err)
	}
}

func testLoadedTeam() LoadedTeam {
	bundle := testBundle()
	bundle.Signature = "signed-for-host-test"
	binding := RoleBinding{
		Role: "coder", BundlePath: "roles/coder.json", BundleDigest: kernel.Digest(repeat("a", 64)), PublisherKeyID: "test",
		InitialInstances: 1, MaximumInstances: 2, LaunchMode: LaunchOnDemand,
		ModelProfileDigest: kernel.Digest(repeat("b", 64)), WorkspaceIDs: []string{"coder-1", "coder-2"},
	}
	manifest := TeamManifest{SchemaVersion: TeamManifestSchemaVersion, Team: "teams", Version: "1.0.0", Roles: []RoleBinding{binding}}
	return LoadedTeam{Manifest: manifest, Roles: []LoadedRole{{Binding: binding, Bundle: bundle}}, Digest: kernel.Digest(repeat("d", 64))}
}

type fakeRoleRuntime struct {
	observations      map[kernel.ActorFQN]RuntimeObservation
	startErr          error
	stopLeavesRunning bool
}

func newFakeRoleRuntime() *fakeRoleRuntime {
	return &fakeRoleRuntime{observations: make(map[kernel.ActorFQN]RuntimeObservation)}
}

func (runtime *fakeRoleRuntime) Start(_ context.Context, request StartRoleRequest) (RuntimeObservation, error) {
	observation := RuntimeObservation{ActorFQN: request.ActorFQN, Execution: request.Execution, State: RuntimeRunning, ProcessIdentity: "process-" + string(request.Execution.ExecutionID)}
	runtime.observations[request.ActorFQN] = observation
	return observation, runtime.startErr
}

func (runtime *fakeRoleRuntime) Stop(_ context.Context, actor kernel.ActorFQN, execution kernel.ExecutionTuple) (RuntimeObservation, error) {
	observation := runtime.observations[actor]
	if !runtime.stopLeavesRunning {
		observation.State = RuntimeStopped
		observation.ProcessIdentity = ""
		runtime.observations[actor] = observation
	}
	return observation, nil
}

func (runtime *fakeRoleRuntime) Inspect(_ context.Context, actor kernel.ActorFQN, execution kernel.ExecutionTuple) (RuntimeObservation, error) {
	observation, found := runtime.observations[actor]
	if !found {
		return RuntimeObservation{ActorFQN: actor, Execution: execution, State: RuntimeStopped}, nil
	}
	return observation, nil
}
