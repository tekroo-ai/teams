package verification

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestVerificationQueuePrioritizesFocusedWithoutStarvingWholeModule(t *testing.T) {
	var queue verificationQueue
	active, err := queue.acquire(t.Context(), Focused)
	if err != nil {
		t.Fatal(err)
	}
	other, err := queue.acquire(t.Context(), Focused)
	if err != nil {
		t.Fatal(err)
	}
	defer other()
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
		queue.active[wanted.slot] = nil
		queue.nextLocked()
		queue.mu.Unlock()
	}
	if len(queue.waiting) != 0 {
		t.Fatal("slot leaked")
	}
	// A duplicate release must not consume another caller's active slot.
	active()
	release, err := queue.acquire(t.Context(), Focused)
	if err != nil {
		t.Fatal(err)
	}
	release()
	release()
	other()
	if queue.active != [MaxConcurrent]*verificationWaiter{} {
		t.Fatal("slot leaked")
	}
}

func TestVerificationQueueCancelledWaiterNeverOwnsSlot(t *testing.T) {
	var queue verificationQueue
	release, err := queue.acquire(t.Context(), WholeModule)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	queued, _ := queue.enqueue(ctx, WholeModule)
	cancel()
	release()
	if queued.granted || queue.active != [MaxConcurrent]*verificationWaiter{} {
		t.Fatal("cancelled waiter granted/leaked")
	}
	if _, err := queue.acquire(ctx, Focused); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestVerificationQueueBroadSuiteDoesNotBlockFocusedLane(t *testing.T) {
	var queue verificationQueue
	broadSlot, broad, err := queue.acquireSlot(t.Context(), WholeModule)
	if err != nil {
		t.Fatal(err)
	}
	defer broad()
	secondBroad, _ := queue.enqueue(t.Context(), WholeModule)
	focusedSlot, focused, err := queue.acquireSlot(t.Context(), Focused)
	if err != nil || focusedSlot == broadSlot {
		t.Fatalf("focused overlap: %d %v", focusedSlot, err)
	}
	if secondBroad.granted {
		t.Fatal("two broad suites ran concurrently")
	}
	third, _ := queue.enqueue(t.Context(), Focused)
	if third.granted {
		t.Fatal("third verification exceeded bound")
	}
	focused()
	if !third.granted || secondBroad.granted {
		t.Fatal("focused work blocked behind active broad suite")
	}
	queue.mu.Lock()
	queue.active[third.slot] = nil
	queue.nextLocked()
	queue.mu.Unlock()
	broad()
	if !secondBroad.granted {
		t.Fatal("next broad suite never admitted")
	}
	queue.mu.Lock()
	queue.active[secondBroad.slot] = nil
	queue.nextLocked()
	queue.mu.Unlock()
}

func TestVerificationQueueCancellationWhileBothLanesOccupied(t *testing.T) {
	var queue verificationQueue
	first, _ := queue.acquire(t.Context(), Focused)
	second, _ := queue.acquire(t.Context(), Focused)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := queue.acquireSlot(ctx, Focused); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if _, _, err := queue.acquireSlot(ctx, Focused); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	first()
	second()
	if len(queue.waiting) != 0 || queue.active != [MaxConcurrent]*verificationWaiter{} {
		t.Fatal("cancellation leaked a lane")
	}
}

func TestVerificationQueueConcurrentLeasesStayBounded(t *testing.T) {
	var queue verificationQueue
	var workers sync.WaitGroup
	var live, peak atomic.Int32
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for i := 0; i < 32; i++ {
		workers.Go(func() {
			for j := 0; j < 20; j++ {
				_, release, err := queue.acquireSlot(ctx, Focused)
				if err != nil {
					t.Error(err)
					return
				}
				n := live.Add(1)
				for previous := peak.Load(); n > previous && !peak.CompareAndSwap(previous, n); previous = peak.Load() {
				}
				if n > MaxConcurrent {
					t.Errorf("concurrency=%d", n)
				}
				live.Add(-1)
				release()
				release()
			}
		})
	}
	workers.Wait()
	if live.Load() != 0 || len(queue.waiting) != 0 || queue.active != [MaxConcurrent]*verificationWaiter{} {
		t.Fatal("lease leak")
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
