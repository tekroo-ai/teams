package fake

import (
	"errors"
	"sync"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

var ErrIDsExhausted = errors.New("deterministic ID sequence exhausted")

type Clock struct {
	mu  sync.RWMutex
	now time.Time
}

func NewClock(now time.Time) *Clock { return &Clock{now: now} }

func (c *Clock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

func (c *Clock) Set(now time.Time) {
	c.mu.Lock()
	c.now = now
	c.mu.Unlock()
}

func (c *Clock) Advance(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	c.mu.Unlock()
}

type IDSource struct {
	mu       sync.Mutex
	sequence []kernel.UUIDv7
	next     int
}

func NewIDSource(sequence ...kernel.UUIDv7) *IDSource {
	return &IDSource{sequence: append([]kernel.UUIDv7(nil), sequence...)}
}

func (s *IDSource) Next() (kernel.UUIDv7, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.next >= len(s.sequence) {
		return "", ErrIDsExhausted
	}
	id := s.sequence[s.next]
	s.next++
	return id, nil
}
