package executionruntime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/mongo"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

func TestWorkerHoldsLeaseUntilTerminalAndResolvesOnce(t *testing.T) {
	intent := workerIntent()
	runtime := &workerRuntime{intent: intent, results: []application.OperationalExecutionResult{{InvocationID: workerUUID(3), State: kernel.InvocationStarted}, {InvocationID: workerUUID(3), State: kernel.InvocationSucceeded}}}
	worker := newTestWorker(t, runtime, 3)

	if err := worker.ProcessOne(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	if runtime.acquireCalls != 1 || runtime.extendCalls != 1 || runtime.resolveCalls != 1 || runtime.yieldCalls != 0 || runtime.processCalls != 2 {
		t.Fatalf("calls acquire=%d extend=%d resolve=%d yield=%d process=%d", runtime.acquireCalls, runtime.extendCalls, runtime.resolveCalls, runtime.yieldCalls, runtime.processCalls)
	}
	if runtime.resolution != "TERMINAL_SUCCEEDED" {
		t.Fatalf("resolution = %s", runtime.resolution)
	}
}

func TestWorkerYieldsUnknownOutcomeAfterBoundedReconciliation(t *testing.T) {
	intent := workerIntent()
	runtime := &workerRuntime{intent: intent, processErr: application.ErrExternalOutcomeUnknown}
	worker := newTestWorker(t, runtime, 2)

	err := worker.ProcessOne(context.Background(), intent)
	if !errors.Is(err, application.ErrExternalOutcomeUnknown) {
		t.Fatalf("error = %v", err)
	}
	if runtime.processCalls != 2 || runtime.extendCalls != 2 || runtime.yieldCalls != 1 || runtime.resolveCalls != 0 {
		t.Fatalf("calls process=%d extend=%d yield=%d resolve=%d", runtime.processCalls, runtime.extendCalls, runtime.yieldCalls, runtime.resolveCalls)
	}
}

func TestWorkerResolvesStaleAuthorityWithoutCallingAgain(t *testing.T) {
	intent := workerIntent()
	runtime := &workerRuntime{intent: intent, processErr: application.ErrStaleWorkInvocation}
	worker := newTestWorker(t, runtime, 3)

	if err := worker.ProcessOne(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	if runtime.processCalls != 1 || runtime.resolveCalls != 1 || runtime.resolution != "REJECTED_STALE_OR_INVALID_AUTHORITY" {
		t.Fatalf("calls=%d resolve=%d resolution=%s", runtime.processCalls, runtime.resolveCalls, runtime.resolution)
	}
}

func TestControlPausesAdmissionWithoutCancellingAndResumes(t *testing.T) {
	control := NewControl()
	control.Pause()
	waitContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- control.wait(waitContext) }()

	select {
	case err := <-result:
		t.Fatalf("paused admission returned early: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if status := control.Status(); status.State != ControlPaused || status.Active != 0 {
		t.Fatalf("paused status = %#v", status)
	}

	control.Resume()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	control.started()
	control.finished(nil)
	if status := control.Status(); status.State != ControlRunning || status.Active != 0 || status.Completed != 1 || status.LastError != "" {
		t.Fatalf("resumed status = %#v", status)
	}
}

func TestWorkerTreatsAnIdlePollAsHealthy(t *testing.T) {
	runtime := &workerRuntime{nextErrors: []error{mongo.ErrIntentNotFound, context.Canceled}}
	worker := newTestWorker(t, runtime, 1)
	if err := worker.Run(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v", err)
	}
}

func TestWorkerShutdownWinsOverConcurrentIdlePoll(t *testing.T) {
	runContext, cancel := context.WithCancel(context.Background())
	runtime := &workerRuntime{
		nextHook: func(context.Context) { cancel() },
		nextErrors: []error{mongo.ErrIntentNotFound},
	}
	worker := newTestWorker(t, runtime, 1)
	if err := worker.Run(runContext); !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v", err)
	}
}

type workerRuntime struct {
	intent       kernel.OutboxIntent
	results      []application.OperationalExecutionResult
	processErr   error
	processCalls int
	acquireCalls int
	extendCalls  int
	resolveCalls int
	yieldCalls   int
	resolution   string
	nextErrors   []error
	nextHook     func(context.Context)
}

func (runtime *workerRuntime) Next(ctx context.Context) (kernel.OutboxIntent, error) {
	if runtime.nextHook != nil {
		runtime.nextHook(ctx)
	}
	if len(runtime.nextErrors) > 0 {
		err := runtime.nextErrors[0]
		runtime.nextErrors = runtime.nextErrors[1:]
		return kernel.OutboxIntent{}, err
	}
	return runtime.intent, nil
}

func (runtime *workerRuntime) Acquire(_ context.Context, id kernel.UUIDv7, _ string, _ time.Time, _ time.Duration) (IntentClaim, error) {
	runtime.acquireCalls++
	if id != runtime.intent.IntentID {
		return IntentClaim{}, errors.New("wrong intent")
	}
	return IntentClaim{Intent: runtime.intent, ClaimEpoch: 1}, nil
}

func (runtime *workerRuntime) Extend(context.Context, kernel.UUIDv7, string, uint64, time.Time, time.Duration) error {
	runtime.extendCalls++
	return nil
}

func (runtime *workerRuntime) Resolve(_ context.Context, _ kernel.UUIDv7, _ string, _ uint64, _ time.Time, resolution string) error {
	runtime.resolveCalls++
	runtime.resolution = resolution
	return nil
}

func (runtime *workerRuntime) Yield(context.Context, kernel.UUIDv7, string, uint64, time.Time) error {
	runtime.yieldCalls++
	return nil
}

func (runtime *workerRuntime) Process(context.Context, kernel.OutboxIntent) (application.OperationalExecutionResult, error) {
	runtime.processCalls++
	if runtime.processErr != nil {
		return application.OperationalExecutionResult{}, runtime.processErr
	}
	if len(runtime.results) == 0 {
		return application.OperationalExecutionResult{}, errors.New("no result")
	}
	result := runtime.results[0]
	runtime.results = runtime.results[1:]
	return result, nil
}

type workerClock struct{ now time.Time }

func (clock workerClock) Now() time.Time { return clock.now }

func newTestWorker(t *testing.T, runtime *workerRuntime, attempts uint32) *Worker {
	t.Helper()
	worker, err := NewWorker(runtime, runtime, runtime, workerClock{now: time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)}, Policy{
		ConsumerID: "teams-openhands-runtime", LeaseDuration: time.Second,
		ReconciliationInterval: time.Millisecond, MaximumReconciliations: attempts,
		MaximumConcurrentInvocations: 1, LeaseOperationTimeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

func workerIntent() kernel.OutboxIntent {
	return kernel.OutboxIntent{IntentID: workerUUID(1), EventID: workerUUID(2), Kind: "WORK_INVOCATION_AUTHORIZED"}
}

func workerUUID(value int) kernel.UUIDv7 {
	if value == 1 {
		return "00000000-0000-7000-8000-000000000001"
	}
	if value == 2 {
		return "00000000-0000-7000-8000-000000000002"
	}
	return "00000000-0000-7000-8000-000000000003"
}
