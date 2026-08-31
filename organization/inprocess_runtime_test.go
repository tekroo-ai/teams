package organization

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

func TestInProcessRuntimeStartsIdempotentlyAndStops(t *testing.T) {
	started := make(chan StartRoleRequest, 1)
	runtime, err := NewInProcessRuntime(RoleWorkerFunc(func(ctx context.Context, request StartRoleRequest) error {
		started <- request
		<-ctx.Done()
		return ctx.Err()
	}))
	if err != nil {
		t.Fatal(err)
	}
	request := testStartRequest()
	observation, err := runtime.Start(context.Background(), request)
	if err != nil || observation.State != RuntimeRunning {
		t.Fatalf("start: observation=%+v err=%v", observation, err)
	}
	select {
	case received := <-started:
		if received.ActorFQN != request.ActorFQN {
			t.Fatalf("wrong request: %+v", received)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	if duplicate, err := runtime.Start(context.Background(), request); err != nil || duplicate != observation {
		t.Fatalf("idempotent start: observation=%+v err=%v", duplicate, err)
	}
	stopContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stopped, err := runtime.Stop(stopContext, request.ActorFQN, request.Execution)
	if err != nil || stopped.State != RuntimeStopped {
		t.Fatalf("stop: observation=%+v err=%v", stopped, err)
	}
	inspected, err := runtime.Inspect(context.Background(), request.ActorFQN, request.Execution)
	if err != nil || inspected.State != RuntimeStopped {
		t.Fatalf("inspect: observation=%+v err=%v", inspected, err)
	}
}

func TestInProcessRuntimeFencesReplacementExecution(t *testing.T) {
	runtime, err := NewInProcessRuntime(RoleWorkerFunc(func(ctx context.Context, _ StartRoleRequest) error {
		<-ctx.Done()
		return ctx.Err()
	}))
	if err != nil {
		t.Fatal(err)
	}
	request := testStartRequest()
	if _, err := runtime.Start(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	replacement := request
	replacement.Execution = kernel.ExecutionTuple{ExecutionID: kernel.UUIDv7("00000000-0000-7000-8000-000000001202"), FencingEpoch: 2}
	if _, err := runtime.Start(context.Background(), replacement); !errors.Is(err, ErrRoleStateConflict) {
		t.Fatalf("expected replacement fence, got %v", err)
	}
	stopContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _ = runtime.Stop(stopContext, request.ActorFQN, request.Execution)
}

func testStartRequest() StartRoleRequest {
	return StartRoleRequest{
		ActorFQN:  kernel.ActorFQN("teams::coder-1"),
		Execution: kernel.ExecutionTuple{ExecutionID: kernel.UUIDv7("00000000-0000-7000-8000-000000001201"), FencingEpoch: 1},
		Bundle:    testBundle(), BundleDigest: kernel.Digest(repeat("a", 64)), ModelProfile: kernel.Digest(repeat("b", 64)),
		WorkspaceID: "coder-1", IdempotencyKey: "start-coder-1", ManifestDigest: kernel.Digest(repeat("c", 64)), ManifestVersion: "1.0.0",
	}
}
