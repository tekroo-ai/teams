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

const QueuePolicy = "single-slot/focused-burst2/fifo/v1"

type verificationWaiter struct {
	ctx     context.Context
	class   Class
	ready   chan struct{}
	granted bool
}
type verificationQueue struct {
	mu           sync.Mutex
	held         bool
	focusedBurst int
	waiting      []*verificationWaiter
}

// Only queued work can be reordered; an active verifier is never interrupted.
// At most two focused checks bypass an older whole-module gate. FIFO within
// each class and the burst bound prevent a stream of short checks starving it.
// The compiler/process limits and verdict-cache identity remain unchanged.
func AcquireClass(ctx context.Context, class Class) (func(), error) { return slots.acquire(ctx, class) }

func (queue *verificationQueue) enqueue(ctx context.Context, class Class) (*verificationWaiter, error) {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	waiter := &verificationWaiter{ctx: ctx, class: class, ready: make(chan struct{})}
	if !queue.held {
		queue.held = true
		waiter.granted = true
		close(waiter.ready)
	} else {
		queue.waiting = append(queue.waiting, waiter)
	}
	return waiter, nil
}

func (queue *verificationQueue) acquire(ctx context.Context, class Class) (func(), error) {
	waiter, err := queue.enqueue(ctx, class)
	if err != nil {
		return nil, err
	}
	select {
	case <-waiter.ready:
	case <-ctx.Done():
	}
	queue.mu.Lock()
	if err := ctx.Err(); err != nil {
		if waiter.granted {
			queue.nextLocked()
		} else {
			for index, pending := range queue.waiting {
				if pending == waiter {
					queue.waiting = append(queue.waiting[:index], queue.waiting[index+1:]...)
					break
				}
			}
		}
		queue.mu.Unlock()
		return nil, err
	}
	queue.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { queue.mu.Lock(); defer queue.mu.Unlock(); queue.nextLocked() }) }, nil
}

func (queue *verificationQueue) nextLocked() {
	for len(queue.waiting) > 0 {
		index := 0
		if queue.focusedBurst < 2 {
			for i, pending := range queue.waiting {
				if pending.class == Focused {
					index = i
					break
				}
			}
		}
		waiter := queue.waiting[index]
		queue.waiting = append(queue.waiting[:index], queue.waiting[index+1:]...)
		if waiter.ctx.Err() != nil {
			continue
		}
		if waiter.class == Focused {
			queue.focusedBurst++
		} else {
			queue.focusedBurst = 0
		}
		waiter.granted = true
		close(waiter.ready)
		return
	}
	queue.held = false
	queue.focusedBurst = 0
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
