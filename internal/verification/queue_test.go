package verification

import (
	"context"
	"errors"
	"testing"
)

func TestVerificationQueuePrioritizesFocusedWithoutStarvingWholeModule(t *testing.T) {
	var queue verificationQueue
	active, err := queue.acquire(t.Context(), WholeModule)
	if err != nil {
		t.Fatal(err)
	}
	whole, _ := queue.enqueue(t.Context(), WholeModule)
	first, _ := queue.enqueue(t.Context(), Focused)
	second, _ := queue.enqueue(t.Context(), Focused)
	third, _ := queue.enqueue(t.Context(), Focused)
	active()
	for _, wanted := range []*verificationWaiter{first, second, whole, third} {
		select {
		case <-wanted.ready:
		default:
			t.Fatal("wrong queue order")
		}
		queue.mu.Lock()
		queue.nextLocked()
		queue.mu.Unlock()
	}
	if queue.held || len(queue.waiting) != 0 {
		t.Fatal("slot leaked")
	}
	// A duplicate release must not consume another caller's active slot.
	active()
	release, err := queue.acquire(t.Context(), WholeModule)
	if err != nil {
		t.Fatal(err)
	}
	release()
	release()
}

func TestVerificationQueueCancelledWaiterNeverOwnsSlot(t *testing.T) {
	var queue verificationQueue
	release, err := queue.acquire(t.Context(), WholeModule)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	queued, _ := queue.enqueue(ctx, Focused)
	cancel()
	release()
	if queued.granted || queue.held {
		t.Fatal("cancelled waiter granted/leaked")
	}
	if _, err := queue.acquire(ctx, Focused); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestVerificationCommandClassificationDoesNotChangeSelection(t *testing.T) {
	for _, test := range []struct {
		args  []string
		class Class
	}{
		{[]string{"go", "test", "-p=1", "-parallel=2", "./..."}, WholeModule},
		{[]string{"go", "test", "-p=1", "-c", "-o", "out", "./..."}, Focused},
		{[]string{"go", "test", "-run", "TestSomething", "./adapters/mongo"}, Focused},
		{[]string{"go", "test", "./one", "./two"}, WholeModule},
		{[]string{"go", "test", "-run", "-c", "./..."}, WholeModule},
	} {
		if got := CommandClass(test.args); got != test.class {
			t.Fatalf("%v class=%v want=%v", test.args, got, test.class)
		}
	}
}
