package verification

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
)

type Class uint8

const (
	WholeModule Class = iota
	Focused
)

const QueuePolicy = "two-slots/whole-max1/focused-burst2/fifo/v2"
const MaxConcurrent = 2

type verificationWaiter struct {
	ctx     context.Context
	class   Class
	ready   chan struct{}
	granted bool
	slot    int
}
type verificationQueue struct {
	mu           sync.Mutex
	active       [MaxConcurrent]*verificationWaiter
	focusedBurst int
	waiting      []*verificationWaiter
}

// Only queued work can be reordered; an active verifier is never interrupted.
// One broad suite plus one focused check, or two focused checks, may overlap.
// At most two focused grants bypass an eligible broad suite. FIFO within
// each class and the burst bound prevent a stream of short checks starving it.
// Per-command compiler/process limits are still enforced independently.
func AcquireClass(ctx context.Context, class Class) (func(), error) { return slots.acquire(ctx, class) }

// AcquireSlot identifies the bounded lane so consumers can retain stable
// compiler paths without sharing a writable checkout with another lane.
func AcquireSlot(ctx context.Context, class Class) (int, func(), error) {
	return slots.acquireSlot(ctx, class)
}

func (queue *verificationQueue) enqueue(ctx context.Context, class Class) (*verificationWaiter, error) {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	waiter := &verificationWaiter{ctx: ctx, class: class, ready: make(chan struct{}), slot: -1}
	queue.waiting = append(queue.waiting, waiter)
	queue.nextLocked()
	return waiter, nil
}

func (queue *verificationQueue) acquire(ctx context.Context, class Class) (func(), error) {
	_, release, err := queue.acquireSlot(ctx, class)
	return release, err
}

func (queue *verificationQueue) acquireSlot(ctx context.Context, class Class) (int, func(), error) {
	waiter, err := queue.enqueue(ctx, class)
	if err != nil {
		return -1, nil, err
	}
	select {
	case <-waiter.ready:
	case <-ctx.Done():
	}
	queue.mu.Lock()
	if err := ctx.Err(); err != nil {
		if waiter.granted {
			queue.active[waiter.slot] = nil
		} else {
			for index, pending := range queue.waiting {
				if pending == waiter {
					queue.waiting = append(queue.waiting[:index], queue.waiting[index+1:]...)
					break
				}
			}
		}
		queue.nextLocked()
		queue.mu.Unlock()
		return -1, nil, err
	}
	queue.mu.Unlock()
	var once sync.Once
	return waiter.slot, func() {
		once.Do(func() {
			queue.mu.Lock()
			defer queue.mu.Unlock()
			queue.active[waiter.slot] = nil
			queue.nextLocked()
		})
	}, nil
}

func (queue *verificationQueue) nextLocked() {
	for len(queue.waiting) > 0 {
		free, wholeActive := -1, false
		for slot, active := range queue.active {
			if active == nil && free < 0 {
				free = slot
			}
			wholeActive = wholeActive || active != nil && active.class == WholeModule
		}
		if free < 0 {
			return
		}
		whole, focused := -1, -1
		for i := 0; i < len(queue.waiting); {
			pending := queue.waiting[i]
			if pending.ctx.Err() != nil {
				queue.waiting = append(queue.waiting[:i], queue.waiting[i+1:]...)
				continue
			}
			if pending.class == WholeModule && !wholeActive && whole < 0 {
				whole = i
			} else if pending.class == Focused && focused < 0 {
				focused = i
			}
			i++
		}
		index := whole
		if focused >= 0 && (whole < 0 || queue.focusedBurst < 2) {
			index = focused
		}
		if index < 0 {
			return
		}
		waiter := queue.waiting[index]
		queue.waiting = append(queue.waiting[:index], queue.waiting[index+1:]...)
		if waiter.class == Focused && whole >= 0 {
			queue.focusedBurst = min(queue.focusedBurst+1, 2)
		} else if waiter.class == WholeModule {
			queue.focusedBurst = 0
		}
		waiter.granted = true
		waiter.slot = free
		queue.active[free] = waiter
		close(waiter.ready)
	}
}

// CommandClass is a server-side scheduling hint, not execution authority. Only
// compile-only or a single local package is prioritized; ambiguous selections
// and recursive suites remain whole-module. No assertions or flags are removed.
func CommandClass(argv []string) Class {
	if len(argv) < 3 || filepath.Base(argv[0]) != "go" {
		return WholeModule
	}
	if argv[1] == "build" {
		return Focused
	}
	if argv[1] != "test" {
		return WholeModule
	}
	packages := 0
	for i := 2; i < len(argv); i++ {
		arg := argv[i]
		if arg == "-args" || arg == "--" {
			break
		}
		if goFlagTakesValue(arg) {
			i++
			continue
		}
		if arg == "-c" {
			return Focused
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if strings.HasSuffix(arg, "/...") || arg == "..." {
			return WholeModule
		}
		packages++
	}
	if packages == 1 {
		return Focused
	}
	return WholeModule
}
