package fake_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/fake"
	"github.com/tekroo-ai/teams/kernel"
)

func TestClockAndIDSourceAreDeterministic(t *testing.T) {
	now := time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)
	clock := fake.NewClock(now)
	clock.Advance(time.Second)
	if got := clock.Now(); !got.Equal(now.Add(time.Second)) {
		t.Fatalf("clock = %s", got)
	}
	id := kernel.UUIDv7("00000000-0000-7000-8000-000000000001")
	source := fake.NewIDSource(id)
	if got, err := source.Next(); err != nil || got != id {
		t.Fatalf("first ID = %s, %v", got, err)
	}
	if _, err := source.Next(); !errors.Is(err, fake.ErrIDsExhausted) {
		t.Fatalf("exhaustion error = %v", err)
	}
}

func TestExecutionEngineReplaysStartAndStops(t *testing.T) {
	engine := fake.NewExecutionEngine()
	request := kernel.StartExecutionRequest{
		ActorFQN:        kernel.ActorFQN("teams::coder-1"),
		ExecutionID:     kernel.UUIDv7("00000000-0000-7000-8000-000000000001"),
		FencingEpoch:    1,
		RuntimeIdentity: kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		IdempotencyKey:  "start-coder-1-epoch-1",
	}
	first, err := engine.Start(context.Background(), request)
	if err != nil || first.State != kernel.ExecutionRunning {
		t.Fatalf("start = %#v, %v", first, err)
	}
	second, err := engine.Start(context.Background(), request)
	if err != nil || second != first {
		t.Fatalf("replayed start = %#v, %v", second, err)
	}
	stopped, err := engine.Stop(context.Background(), request.ExecutionID)
	if err != nil || stopped.State != kernel.ExecutionStopped {
		t.Fatalf("stop = %#v, %v", stopped, err)
	}
	reconciled, err := engine.Reconcile(context.Background(), request.ExecutionID)
	if err != nil || reconciled != stopped {
		t.Fatalf("reconcile = %#v, %v", reconciled, err)
	}
}
