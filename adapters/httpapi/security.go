package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/tekroo-ai/teams/adapters/protocol"
)

var ErrAuthenticationFailed = errors.New("authentication failed")

// StaticBearerAuthenticator binds one local bearer credential to one trusted
// transport identity. The identity is configuration, never client input.
type StaticBearerAuthenticator struct {
	digest   [32]byte
	identity protocol.AuthenticatedContext
}

func NewStaticBearerAuthenticator(token string, identity protocol.AuthenticatedContext) (*StaticBearerAuthenticator, error) {
	if len(token) < 32 || !identity.Valid() {
		return nil, ErrInvalidConfiguration
	}
	return &StaticBearerAuthenticator{digest: sha256.Sum256([]byte(token)), identity: identity}, nil
}

func (authenticator *StaticBearerAuthenticator) Authenticate(request *http.Request) (protocol.AuthenticatedContext, error) {
	if authenticator == nil || request == nil {
		return protocol.AuthenticatedContext{}, ErrAuthenticationFailed
	}
	value := request.Header.Get("Authorization")
	if !strings.HasPrefix(value, "Bearer ") {
		return protocol.AuthenticatedContext{}, ErrAuthenticationFailed
	}
	observed := sha256.Sum256([]byte(strings.TrimPrefix(value, "Bearer ")))
	if subtle.ConstantTimeCompare(observed[:], authenticator.digest[:]) != 1 {
		return protocol.AuthenticatedContext{}, ErrAuthenticationFailed
	}
	return authenticator.identity, nil
}

var _ Authenticator = (*StaticBearerAuthenticator)(nil)
