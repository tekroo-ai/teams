package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/fake"
	"github.com/tekroo-ai/teams/adapters/memory"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

type executionCommandFunc func(context.Context, kernel.KernelCommand, kernel.ProvenanceBasis) (kernel.CommandReceipt, error)

func (function executionCommandFunc) Handle(ctx context.Context, command kernel.KernelCommand, provenance kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
	return function(ctx, command, provenance)
}

type executionEngineStub struct {
	start     func(context.Context, kernel.StartExecutionRequest) (kernel.ExecutionObservation, error)
	stop      func(context.Context, kernel.UUIDv7) (kernel.ExecutionObservation, error)
	inspect   func(context.Context, kernel.UUIDv7) (kernel.ExecutionObservation, error)
	reconcile func(context.Context, kernel.UUIDv7) (kernel.ExecutionObservation, error)
}

func (stub executionEngineStub) Start(ctx context.Context, request kernel.StartExecutionRequest) (kernel.ExecutionObservation, error) {
	return stub.start(ctx, request)
}
func (stub executionEngineStub) Stop(ctx context.Context, id kernel.UUIDv7) (kernel.ExecutionObservation, error) {
	return stub.stop(ctx, id)
}
func (stub executionEngineStub) Inspect(ctx context.Context, id kernel.UUIDv7) (kernel.ExecutionObservation, error) {
	return stub.inspect(ctx, id)
}
func (stub executionEngineStub) Reconcile(ctx context.Context, id kernel.UUIDv7) (kernel.ExecutionObservation, error) {
	return stub.reconcile(ctx, id)
}

func TestExecutionCoordinatorAuthorizesBeforeOneIdempotentStart(t *testing.T) {
	plan := executionPlan(t, "start-1", "00000000-0000-7000-8000-000000000071", 1)
	var authorized atomic.Bool
	var starts atomic.Int32
	commands := executionCommandFunc(func(_ context.Context, command kernel.KernelCommand, _ kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		authorized.Store(true)
		return appliedExecutionReceipt(command), nil
	})
	engine := runningEngine(plan.Request, func() {
		if !authorized.Load() {
			t.Error("provider start preceded kernel authorization")
		}
		starts.Add(1)
	})
	controls := memory.NewExecutionControls()
	coordinator := newExecutionCoordinator(t, commands, controls, engine, executionPolicy())

	first, err := coordinator.Start(context.Background(), plan)
	if err != nil || first.State != application.ControlRunning || first.AuthorizationReceipt == nil {
		t.Fatalf("first start = %#v, %v", first, err)
	}
	secondCoordinator := newExecutionCoordinator(t, commands, controls, engine, executionPolicy())
	second, err := secondCoordinator.Start(context.Background(), plan)
	if err != nil || second.State != application.ControlRunning || starts.Load() != 1 {
		t.Fatalf("replayed start = %#v, %v; starts=%d", second, err, starts.Load())
	}
}

func TestExecutionCoordinatorCommitsRegistryBeforeFakeProviderStart(t *testing.T) {
	plan := executionPlan(t, "integrated-start", "00000000-0000-7000-8000-000000000079", 1)
	store := memory.NewStore()
	store.SetAuthorizationPolicy(testAuthorizationPolicy(plan.AuthorizationCommand, plan.Provenance))
	handler, err := application.NewHandler(
		store, kernel.Evaluator{Catalogue: loadCatalogue(t)},
		fake.NewClock(time.Date(2026, 8, 11, 13, 0, 0, 0, time.UTC)),
		fake.NewIDSource(
			kernel.UUIDv7("00000000-0000-7000-8000-000000000081"),
			kernel.UUIDv7("00000000-0000-7000-8000-000000000082"),
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	var starts atomic.Int32
	engine := runningEngine(plan.Request, func() {
		snapshot, loadErr := store.LoadDecision(context.Background(), plan.AuthorizationCommand)
		if loadErr != nil {
			t.Errorf("load registry at provider start: %v", loadErr)
			return
		}
		registered, found := snapshot.CurrentExecutions[plan.Request.ActorFQN]
		if !found || registered.ExecutionID != plan.Request.ExecutionID || registered.FencingEpoch != plan.Request.FencingEpoch {
			t.Errorf("registry at provider start = %#v", snapshot.CurrentExecutions)
		}
		starts.Add(1)
	})
	coordinator := newExecutionCoordinator(t, handler, memory.NewExecutionControls(), engine, executionPolicy())
	first, err := coordinator.Start(context.Background(), plan)
	if err != nil || first.State != application.ControlRunning || store.EventCount() != 1 {
		t.Fatalf("integrated start = %#v, %v; events=%d", first, err, store.EventCount())
	}
	second, err := coordinator.Start(context.Background(), plan)
	if err != nil || second.State != application.ControlRunning || starts.Load() != 1 || store.EventCount() != 1 {
		t.Fatalf("integrated replay = %#v, %v; starts=%d events=%d", second, err, starts.Load(), store.EventCount())
	}
}

func TestExecutionCoordinatorRejectsMisalignedOrDeniedStartBeforeProvider(t *testing.T) {
	plan := executionPlan(t, "start-2", "00000000-0000-7000-8000-000000000072", 1)
	var commandCalls, starts atomic.Int32
	commands := executionCommandFunc(func(_ context.Context, command kernel.KernelCommand, _ kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		commandCalls.Add(1)
		return kernel.CommandReceipt{CommandID: command.CommandID, OutcomeCode: kernel.OutcomeRejectedUnauthorized, ReasonCode: "UNAUTHORIZED"}, nil
	})
	engine := runningEngine(plan.Request, func() { starts.Add(1) })
	coordinator := newExecutionCoordinator(t, commands, memory.NewExecutionControls(), engine, executionPolicy())

	misaligned := plan
	misaligned.Request.FencingEpoch++
	if _, err := coordinator.Start(context.Background(), misaligned); !errors.Is(err, application.ErrInvalidExecutionPlan) {
		t.Fatalf("misaligned error = %v", err)
	}
	record, err := coordinator.Start(context.Background(), plan)
	if err != nil || record.State != application.ControlRejected || commandCalls.Load() != 1 || starts.Load() != 0 {
		t.Fatalf("denied record=%#v err=%v commandCalls=%d starts=%d", record, err, commandCalls.Load(), starts.Load())
	}
}

func TestExecutionCoordinatorConcurrentReplayHasOneProviderEffect(t *testing.T) {
	plan := executionPlan(t, "start-3", "00000000-0000-7000-8000-000000000073", 1)
	var starts atomic.Int32
	commands := executionCommandFunc(func(_ context.Context, command kernel.KernelCommand, _ kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		return appliedExecutionReceipt(command), nil
	})
	release := make(chan struct{})
	engine := runningEngine(plan.Request, func() {
		starts.Add(1)
		<-release
	})
	coordinator := newExecutionCoordinator(t, commands, memory.NewExecutionControls(), engine, executionPolicy())
	var wait sync.WaitGroup
	wait.Add(2)
	results := make(chan application.ExecutionControlRecord, 2)
	for range 2 {
		go func() {
			defer wait.Done()
			record, _ := coordinator.Start(context.Background(), plan)
			results <- record
		}()
	}
	deadline := time.After(time.Second)
	for starts.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("provider start did not begin")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	close(release)
	wait.Wait()
	close(results)
	if starts.Load() != 1 {
		t.Fatalf("provider starts = %d", starts.Load())
	}
}

func TestExecutionCoordinatorTimeoutIsUncertainAndReconcilesWithinBudget(t *testing.T) {
	plan := executionPlan(t, "start-4", "00000000-0000-7000-8000-000000000074", 1)
	release := make(chan struct{})
	engine := executionEngineStub{
		start: func(context.Context, kernel.StartExecutionRequest) (kernel.ExecutionObservation, error) {
			<-release
			return observation(plan.Request, kernel.ExecutionRunning), nil
		},
		stop: func(context.Context, kernel.UUIDv7) (kernel.ExecutionObservation, error) {
			return kernel.ExecutionObservation{}, errors.New("unused")
		},
		inspect: func(context.Context, kernel.UUIDv7) (kernel.ExecutionObservation, error) {
			return kernel.ExecutionObservation{}, errors.New("unused")
		},
		reconcile: func(context.Context, kernel.UUIDv7) (kernel.ExecutionObservation, error) {
			return observation(plan.Request, kernel.ExecutionRunning), nil
		},
	}
	policy := executionPolicy()
	policy.OperationTimeout = 5 * time.Millisecond
	commands := executionCommandFunc(func(_ context.Context, command kernel.KernelCommand, _ kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		return appliedExecutionReceipt(command), nil
	})
	coordinator := newExecutionCoordinator(t, commands, memory.NewExecutionControls(), engine, policy)
	record, err := coordinator.Start(context.Background(), plan)
	if !errors.Is(err, context.DeadlineExceeded) || record.State != application.ControlUncertain {
		t.Fatalf("timed-out start = %#v, %v", record, err)
	}
	close(release)
	record, err = coordinator.Reconcile(context.Background(), plan.Request.IdempotencyKey)
	if err != nil || record.State != application.ControlRunning || record.ReconciliationAttempts != 1 {
		t.Fatalf("reconciled = %#v, %v", record, err)
	}
}

func TestExecutionCoordinatorRejectsWrongObservationAndBoundsReconciliation(t *testing.T) {
	plan := executionPlan(t, "start-5", "00000000-0000-7000-8000-000000000075", 1)
	wrong := observation(plan.Request, kernel.ExecutionRunning)
	wrong.FencingEpoch++
	engine := executionEngineStub{
		start: func(context.Context, kernel.StartExecutionRequest) (kernel.ExecutionObservation, error) {
			return wrong, nil
		},
		stop:      func(context.Context, kernel.UUIDv7) (kernel.ExecutionObservation, error) { return wrong, nil },
		inspect:   func(context.Context, kernel.UUIDv7) (kernel.ExecutionObservation, error) { return wrong, nil },
		reconcile: func(context.Context, kernel.UUIDv7) (kernel.ExecutionObservation, error) { return wrong, nil },
	}
	policy := executionPolicy()
	policy.MaxReconciliations = 1
	commands := executionCommandFunc(func(_ context.Context, command kernel.KernelCommand, _ kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		return appliedExecutionReceipt(command), nil
	})
	coordinator := newExecutionCoordinator(t, commands, memory.NewExecutionControls(), engine, policy)
	record, err := coordinator.Start(context.Background(), plan)
	if !errors.Is(err, application.ErrExecutionObservation) || record.State != application.ControlUncertain {
		t.Fatalf("wrong start observation = %#v, %v", record, err)
	}
	record, err = coordinator.Reconcile(context.Background(), plan.Request.IdempotencyKey)
	if !errors.Is(err, application.ErrExecutionObservation) || record.ReconciliationAttempts != 1 {
		t.Fatalf("wrong reconciliation = %#v, %v", record, err)
	}
	if _, err := coordinator.Reconcile(context.Background(), plan.Request.IdempotencyKey); !errors.Is(err, application.ErrReconciliationExhausted) {
		t.Fatalf("exhaustion error = %v", err)
	}
}

func TestExecutionControlStoreEnforcesCapacityAndAntiThrash(t *testing.T) {
	store := memory.NewExecutionControls()
	policy := executionPolicy()
	policy.MaxActivePerActor = 1
	policy.MaxStartsPerActorWindow = 1
	firstPlan := executionPlan(t, "limit-1", "00000000-0000-7000-8000-000000000076", 1)
	first := controlRecord(firstPlan, time.Date(2026, 8, 11, 13, 0, 0, 0, time.UTC))
	if _, created, err := store.Prepare(context.Background(), first, policy); err != nil || !created {
		t.Fatalf("prepare first: created=%t err=%v", created, err)
	}
	current := first.Clone()
	for _, state := range []application.ExecutionControlState{application.ControlAuthorized, application.ControlStarting, application.ControlUncertain} {
		next := current.Clone()
		next.State = state
		next.Version++
		next.UpdatedAt = next.UpdatedAt.Add(time.Second)
		if err := store.CompareAndSwap(context.Background(), first.Request.IdempotencyKey, current.Version, next); err != nil {
			t.Fatal(err)
		}
		current = next
	}
	secondPlan := executionPlan(t, "limit-2", "00000000-0000-7000-8000-000000000077", 2)
	second := controlRecord(secondPlan, first.ReservedAt.Add(time.Second))
	if _, _, err := store.Prepare(context.Background(), second, policy); !errors.Is(err, application.ErrExecutionLimit) {
		t.Fatalf("capacity error = %v", err)
	}
	for _, state := range []application.ExecutionControlState{application.ControlStopping, application.ControlStopped} {
		next := current.Clone()
		next.State = state
		next.Version++
		next.UpdatedAt = next.UpdatedAt.Add(time.Second)
		if err := store.CompareAndSwap(context.Background(), first.Request.IdempotencyKey, current.Version, next); err != nil {
			t.Fatal(err)
		}
		current = next
	}
	if _, _, err := store.Prepare(context.Background(), second, policy); !errors.Is(err, application.ErrExecutionThrash) {
		t.Fatalf("anti-thrash error = %v", err)
	}
	second.ReservedAt = first.ReservedAt.Add(policy.StartWindow + time.Second)
	second.UpdatedAt = second.ReservedAt
	if _, created, err := store.Prepare(context.Background(), second, policy); err != nil || !created {
		t.Fatalf("post-window prepare: created=%t err=%v", created, err)
	}
}

func TestExecutionCoordinatorStopAndInspectPreserveExactIdentity(t *testing.T) {
	plan := executionPlan(t, "start-6", "00000000-0000-7000-8000-000000000078", 1)
	commands := executionCommandFunc(func(_ context.Context, command kernel.KernelCommand, _ kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		return appliedExecutionReceipt(command), nil
	})
	engine := fake.NewExecutionEngine()
	coordinator := newExecutionCoordinator(t, commands, memory.NewExecutionControls(), engine, executionPolicy())
	if record, err := coordinator.Start(context.Background(), plan); err != nil || record.State != application.ControlRunning {
		t.Fatalf("start = %#v, %v", record, err)
	}
	if observed, err := coordinator.Inspect(context.Background(), plan.Request.IdempotencyKey); err != nil || observed.State != kernel.ExecutionRunning {
		t.Fatalf("inspect = %#v, %v", observed, err)
	}
	if record, err := coordinator.Stop(context.Background(), plan.Request.IdempotencyKey); err != nil || record.State != application.ControlStopped {
		t.Fatalf("stop = %#v, %v", record, err)
	}
}

func executionPlan(t *testing.T, key, executionID string, epoch uint64) application.ExecutionStartPlan {
	t.Helper()
	basis, err := fake.ProvenanceBasis()
	if err != nil {
		t.Fatal(err)
	}
	actor := kernel.ActorFQN("teams::coder-1")
	runtimeIdentity := kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	payload, err := json.Marshal(map[string]any{
		"actor_fqn": actor, "execution_id": executionID, "fencing_epoch": epoch, "runtime_identity": runtimeIdentity,
	})
	if err != nil {
		t.Fatal(err)
	}
	commandID := kernel.UUIDv7("00000000-0000-7000-8000-000000000061")
	return application.ExecutionStartPlan{
		AuthorizationCommand: kernel.KernelCommand{
			ContractManifest: kernel.ContractIdentity, CommandID: commandID,
			CommandType: "tekroo.command.execution.register", CommandVersion: kernel.SchemaVersion,
			Target:           kernel.AggregateRef{Kind: kernel.AggregateExecution, ID: kernel.UUIDv7(executionID)},
			Authority:        kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "execution-coordinator"},
			ExpectedRevision: kernel.MustNotExist(), ExpectedPolicyRevision: 1,
			ExpectedCatalogueRevision: kernel.CatalogueRevision, IdempotencyKey: key,
			CorrelationID: kernel.UUIDv7("00000000-0000-7000-8000-000000000062"), Payload: payload,
		},
		Provenance: basis,
		Request: kernel.StartExecutionRequest{
			ActorFQN: actor, ExecutionID: kernel.UUIDv7(executionID), FencingEpoch: epoch,
			RuntimeIdentity: runtimeIdentity, IdempotencyKey: key,
		},
	}
}

func newExecutionCoordinator(t *testing.T, commands application.ExecutionCommandService, controls application.ExecutionControlStore, engine kernel.AgentExecutionEngine, policy application.ExecutionPolicy) *application.ExecutionCoordinator {
	t.Helper()
	coordinator, err := application.NewExecutionCoordinator(commands, controls, engine, fake.NewClock(time.Date(2026, 8, 11, 13, 0, 0, 0, time.UTC)), policy)
	if err != nil {
		t.Fatal(err)
	}
	return coordinator
}

func executionPolicy() application.ExecutionPolicy {
	return application.ExecutionPolicy{
		OperationTimeout: 100 * time.Millisecond, MaxActiveTotal: 8, MaxActivePerActor: 2,
		MaxStartsPerActorWindow: 4, StartWindow: time.Minute, MaxReconciliations: 3,
	}
}

func runningEngine(request kernel.StartExecutionRequest, before func()) executionEngineStub {
	return executionEngineStub{
		start: func(context.Context, kernel.StartExecutionRequest) (kernel.ExecutionObservation, error) {
			before()
			return observation(request, kernel.ExecutionRunning), nil
		},
		stop: func(context.Context, kernel.UUIDv7) (kernel.ExecutionObservation, error) {
			return observation(request, kernel.ExecutionStopped), nil
		},
		inspect: func(context.Context, kernel.UUIDv7) (kernel.ExecutionObservation, error) {
			return observation(request, kernel.ExecutionRunning), nil
		},
		reconcile: func(context.Context, kernel.UUIDv7) (kernel.ExecutionObservation, error) {
			return observation(request, kernel.ExecutionRunning), nil
		},
	}
}

func observation(request kernel.StartExecutionRequest, state kernel.ExecutionState) kernel.ExecutionObservation {
	return kernel.ExecutionObservation{ActorFQN: request.ActorFQN, ExecutionID: request.ExecutionID, FencingEpoch: request.FencingEpoch, State: state}
}

func appliedExecutionReceipt(command kernel.KernelCommand) kernel.CommandReceipt {
	return kernel.CommandReceipt{ContractManifest: kernel.ContractIdentity, CommandID: command.CommandID, CommandType: command.CommandType, Target: command.Target, OutcomeCode: kernel.OutcomeApplied, ReasonCode: "APPLIED"}
}

func controlRecord(plan application.ExecutionStartPlan, now time.Time) application.ExecutionControlRecord {
	return application.ExecutionControlRecord{
		Request: plan.Request, AuthorizationCommandID: plan.AuthorizationCommand.CommandID,
		State: application.ControlPendingAuthorization, Version: 1, ReservedAt: now, UpdatedAt: now,
	}
}
