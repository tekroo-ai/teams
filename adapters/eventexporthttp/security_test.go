package eventexporthttp_test

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/eventexporthttp"
	"github.com/tekroo-ai/teams/adapters/protocol"
	"github.com/tekroo-ai/teams/kernel"
)

func TestBearerAuthenticatorFailsClosedAndReturnsPinnedIdentity(t *testing.T) {
	identity := protocol.AuthenticatedContext{Principal: kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "sma"}}
	authenticator, err := eventexporthttp.NewBearerAuthenticator("0123456789abcdef0123456789abcdef", identity)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/", nil)
	if _, err := authenticator.Authenticate(request); !errors.Is(err, eventexporthttp.ErrAuthenticationFailed) {
		t.Fatalf("missing token = %v", err)
	}
	request.Header.Set("Authorization", "Bearer wrong-wrong-wrong-wrong-wrong-wrong")
	if _, err := authenticator.Authenticate(request); !errors.Is(err, eventexporthttp.ErrAuthenticationFailed) {
		t.Fatalf("wrong token = %v", err)
	}
	request.Header.Set("Authorization", "Bearer 0123456789abcdef0123456789abcdef")
	got, err := authenticator.Authenticate(request)
	if err != nil || !got.Equal(identity) {
		t.Fatalf("identity = %#v, %v", got, err)
	}
}

func TestFixedWindowRateLimiterIsPrincipalScopedAndDeterministic(t *testing.T) {
	now := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)
	limiter, err := eventexporthttp.NewFixedWindowRateLimiter(2, time.Minute, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	alpha := protocol.AuthenticatedContext{Principal: kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "alpha"}}
	beta := protocol.AuthenticatedContext{Principal: kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "beta"}}
	if !limiter.Allow(alpha) || !limiter.Allow(alpha) || limiter.Allow(alpha) {
		t.Fatal("alpha limit mismatch")
	}
	if !limiter.Allow(beta) {
		t.Fatal("beta was not independently limited")
	}
	now = now.Add(time.Minute)
	if !limiter.Allow(alpha) {
		t.Fatal("window did not reset")
	}
}
