package eventexporthttp

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tekroo-ai/teams/adapters/protocol"
)

var ErrAuthenticationFailed = errors.New("event export authentication failed")

type BearerAuthenticator struct {
	tokenHash [sha256.Size]byte
	identity  protocol.AuthenticatedContext
}

func NewBearerAuthenticator(token string, identity protocol.AuthenticatedContext) (*BearerAuthenticator, error) {
	if len(token) < 32 || !identity.Valid() {
		return nil, ErrInvalidConfiguration
	}
	return &BearerAuthenticator{tokenHash: sha256.Sum256([]byte(token)), identity: identity}, nil
}

func (authenticator *BearerAuthenticator) Authenticate(request *http.Request) (protocol.AuthenticatedContext, error) {
	value := request.Header.Get("Authorization")
	if !strings.HasPrefix(value, "Bearer ") || subtle.ConstantTimeCompare(authenticator.tokenHash[:], sha256Bytes(strings.TrimPrefix(value, "Bearer "))) != 1 {
		return protocol.AuthenticatedContext{}, ErrAuthenticationFailed
	}
	return authenticator.identity, nil
}

func sha256Bytes(value string) []byte {
	digest := sha256.Sum256([]byte(value))
	return digest[:]
}

type FixedWindowRateLimiter struct {
	mu      sync.Mutex
	limit   uint32
	window  time.Duration
	clock   func() time.Time
	entries map[protocolIdentityKey]rateWindow
}

type protocolIdentityKey struct {
	principal string
	actor     string
}

type rateWindow struct {
	started time.Time
	count   uint32
}

func NewFixedWindowRateLimiter(limit uint32, window time.Duration, clock func() time.Time) (*FixedWindowRateLimiter, error) {
	if limit == 0 || window <= 0 {
		return nil, ErrInvalidConfiguration
	}
	if clock == nil {
		clock = time.Now
	}
	return &FixedWindowRateLimiter{limit: limit, window: window, clock: clock, entries: make(map[protocolIdentityKey]rateWindow)}, nil
}

func (limiter *FixedWindowRateLimiter) Allow(identity protocol.AuthenticatedContext) bool {
	actor := ""
	if identity.ActorFQN != nil {
		actor = string(*identity.ActorFQN)
	}
	key := protocolIdentityKey{principal: string(identity.Principal.Kind) + ":" + identity.Principal.ID, actor: actor}
	now := limiter.clock()
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	entry := limiter.entries[key]
	if entry.started.IsZero() || !now.Before(entry.started.Add(limiter.window)) {
		limiter.entries[key] = rateWindow{started: now, count: 1}
		return true
	}
	if entry.count >= limiter.limit {
		return false
	}
	entry.count++
	limiter.entries[key] = entry
	return true
}
