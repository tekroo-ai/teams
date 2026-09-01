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

type BearerBinding struct {
	Token    string
	Identity protocol.AuthenticatedContext
}

type MultiStaticBearerAuthenticator struct {
	bindings []StaticBearerAuthenticator
}

func NewMultiStaticBearerAuthenticator(bindings []BearerBinding) (*MultiStaticBearerAuthenticator, error) {
	if len(bindings) == 0 || len(bindings) > 256 {
		return nil, ErrInvalidConfiguration
	}
	result := &MultiStaticBearerAuthenticator{bindings: make([]StaticBearerAuthenticator, len(bindings))}
	seenDigests := make(map[[32]byte]struct{}, len(bindings))
	seenPrincipals := make(map[protocolPrincipalKey]struct{}, len(bindings))
	for index, binding := range bindings {
		resolved, err := NewStaticBearerAuthenticator(binding.Token, binding.Identity)
		key := protocolPrincipalKey{kind: string(binding.Identity.Principal.Kind), id: binding.Identity.Principal.ID}
		if err != nil {
			return nil, err
		}
		if _, duplicate := seenDigests[resolved.digest]; duplicate {
			return nil, ErrInvalidConfiguration
		}
		if _, duplicate := seenPrincipals[key]; duplicate {
			return nil, ErrInvalidConfiguration
		}
		seenDigests[resolved.digest] = struct{}{}
		seenPrincipals[key] = struct{}{}
		result.bindings[index] = *resolved
	}
	return result, nil
}

type protocolPrincipalKey struct {
	kind string
	id   string
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

func (authenticator *MultiStaticBearerAuthenticator) Authenticate(request *http.Request) (protocol.AuthenticatedContext, error) {
	if authenticator == nil || request == nil {
		return protocol.AuthenticatedContext{}, ErrAuthenticationFailed
	}
	value := request.Header.Get("Authorization")
	if !strings.HasPrefix(value, "Bearer ") {
		return protocol.AuthenticatedContext{}, ErrAuthenticationFailed
	}
	observed := sha256.Sum256([]byte(strings.TrimPrefix(value, "Bearer ")))
	selected := -1
	for index := range authenticator.bindings {
		if subtle.ConstantTimeCompare(observed[:], authenticator.bindings[index].digest[:]) == 1 {
			selected = index
		}
	}
	if selected < 0 {
		return protocol.AuthenticatedContext{}, ErrAuthenticationFailed
	}
	return authenticator.bindings[selected].identity, nil
}

var _ Authenticator = (*StaticBearerAuthenticator)(nil)
var _ Authenticator = (*MultiStaticBearerAuthenticator)(nil)
