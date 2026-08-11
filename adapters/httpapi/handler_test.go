package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tekroo-ai/teams/adapters/fake"
	"github.com/tekroo-ai/teams/adapters/httpapi"
	"github.com/tekroo-ai/teams/adapters/protocol"
	"github.com/tekroo-ai/teams/kernel"
)

type endpointFunc func(context.Context, protocol.Request) protocol.Response

func (function endpointFunc) Invoke(ctx context.Context, request protocol.Request) protocol.Response {
	return function(ctx, request)
}

func TestHTTPHandlerInjectsServerAuthenticatedIdentity(t *testing.T) {
	identity := testIdentity()
	var received protocol.Request
	handler := newHandler(t, endpointFunc(func(_ context.Context, request protocol.Request) protocol.Response {
		received = request
		receipt := kernel.CommandReceipt{ContractManifest: kernel.ContractIdentity, CommandID: request.Command.CommandID, OutcomeCode: kernel.OutcomeApplied}
		return protocol.Response{EnvelopeVersion: protocol.EnvelopeVersion, RequestID: request.RequestID, Status: protocol.StatusReceipt, Receipt: &receipt}
	}), identity, httpapi.OriginPolicyFunc(func(origin string) bool { return origin == "https://console.tekroo.test" }), httpapi.RateLimiterFunc(func(protocol.AuthenticatedContext) bool { return true }), httpapi.DefaultMaxBodyBytes)

	request := commandRequest(t, testInvocation(t))
	request.Header.Set("Origin", "https://console.tekroo.test")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	receivedIdentity := protocol.AuthenticatedContext{Principal: received.AuthenticatedPrincipal, ActorFQN: received.AuthenticatedActorFQN, Execution: received.AuthenticatedExecution}
	if response.Code != http.StatusOK || !receivedIdentity.Equal(identity) {
		t.Fatalf("status = %d, authenticated request = %#v", response.Code, received)
	}
	var decoded protocol.Response
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil || decoded.Status != protocol.StatusReceipt {
		t.Fatalf("response = %s, error = %v", response.Body.String(), err)
	}
}

func TestHTTPHandlerRejectsClientAssertedIdentityAndSecurityFailuresBeforeDispatch(t *testing.T) {
	identity := testIdentity()
	var calls atomic.Int32
	handler := newHandler(t, endpointFunc(func(context.Context, protocol.Request) protocol.Response {
		calls.Add(1)
		return protocol.Response{}
	}), identity, httpapi.OriginPolicyFunc(func(origin string) bool { return origin == "https://allowed.test" }), httpapi.RateLimiterFunc(func(protocol.AuthenticatedContext) bool { return true }), httpapi.DefaultMaxBodyBytes)

	fullRequest := testInvocation(t).Authenticate(identity)
	encoded, err := json.Marshal(fullRequest)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		body   []byte
		origin string
		status int
	}{
		{name: "client asserted identity", body: encoded, origin: "https://allowed.test", status: http.StatusBadRequest},
		{name: "forbidden origin", body: encodeInvocation(t, testInvocation(t)), origin: "https://denied.test", status: http.StatusForbidden},
		{name: "wrong content type", body: encodeInvocation(t, testInvocation(t)), origin: "https://allowed.test", status: http.StatusUnsupportedMediaType},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/v1/commands/invoke", bytes.NewReader(test.body))
			request.Header.Set("Origin", test.origin)
			if test.name != "wrong content type" {
				request.Header.Set("Content-Type", "application/json")
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.status, response.Body.String())
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("endpoint calls = %d, want 0", calls.Load())
	}
}

func TestHTTPHandlerFailsClosedOnAuthenticationRateAndBodyBound(t *testing.T) {
	identity := testIdentity()
	endpoint := endpointFunc(func(context.Context, protocol.Request) protocol.Response {
		t.Fatal("endpoint must not be called")
		return protocol.Response{}
	})
	tests := []struct {
		name   string
		auth   httpapi.Authenticator
		limit  httpapi.RateLimiter
		max    int64
		status int
	}{
		{name: "authentication", auth: httpapi.AuthenticatorFunc(func(*http.Request) (protocol.AuthenticatedContext, error) {
			return protocol.AuthenticatedContext{}, errors.New("denied")
		}), limit: httpapi.RateLimiterFunc(func(protocol.AuthenticatedContext) bool { return true }), max: httpapi.DefaultMaxBodyBytes, status: http.StatusUnauthorized},
		{name: "rate", auth: fixedAuthenticator(identity), limit: httpapi.RateLimiterFunc(func(protocol.AuthenticatedContext) bool { return false }), max: httpapi.DefaultMaxBodyBytes, status: http.StatusTooManyRequests},
		{name: "body", auth: fixedAuthenticator(identity), limit: httpapi.RateLimiterFunc(func(protocol.AuthenticatedContext) bool { return true }), max: 8, status: http.StatusRequestEntityTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, err := httpapi.NewHandler(endpoint, test.auth, httpapi.OriginPolicyFunc(func(string) bool { return true }), test.limit, test.max)
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, commandRequest(t, testInvocation(t)))
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.status, response.Body.String())
			}
		})
	}
}

func TestHTTPHandlerMapsUncertainOutcomeWithoutClaimingRollback(t *testing.T) {
	identity := testIdentity()
	handler := newHandler(t, endpointFunc(func(_ context.Context, request protocol.Request) protocol.Response {
		return protocol.Response{
			EnvelopeVersion: protocol.EnvelopeVersion, RequestID: request.RequestID,
			Status:  protocol.StatusInvocationFailure,
			Failure: &protocol.InvocationFailure{Code: protocol.FailureTimeout, Message: "deadline exceeded", OutcomeUnknown: true},
		}
	}), identity, httpapi.OriginPolicyFunc(func(string) bool { return true }), httpapi.RateLimiterFunc(func(protocol.AuthenticatedContext) bool { return true }), httpapi.DefaultMaxBodyBytes)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, commandRequest(t, testInvocation(t)))
	if response.Code != http.StatusGatewayTimeout || response.Header().Get(httpapi.OutcomeUnknownHeader) != "true" || !strings.Contains(response.Body.String(), `"outcome_unknown":true`) {
		t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
}

func newHandler(t *testing.T, endpoint protocol.Endpoint, identity protocol.AuthenticatedContext, origins httpapi.OriginPolicy, limiter httpapi.RateLimiter, max int64) *httpapi.Handler {
	t.Helper()
	handler, err := httpapi.NewHandler(endpoint, fixedAuthenticator(identity), origins, limiter, max)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func fixedAuthenticator(identity protocol.AuthenticatedContext) httpapi.Authenticator {
	return httpapi.AuthenticatorFunc(func(*http.Request) (protocol.AuthenticatedContext, error) { return identity, nil })
}

func commandRequest(t *testing.T, invocation protocol.Invocation) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/commands/invoke", bytes.NewReader(encodeInvocation(t, invocation)))
	request.Header.Set("Content-Type", "application/json")
	return request
}

func encodeInvocation(t *testing.T, invocation protocol.Invocation) []byte {
	t.Helper()
	encoded, err := json.Marshal(invocation)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func testIdentity() protocol.AuthenticatedContext {
	actor := kernel.ActorFQN("teams::coder-1")
	execution := kernel.ExecutionTuple{ExecutionID: kernel.UUIDv7("00000000-0000-7000-8000-000000000091"), FencingEpoch: 7}
	return protocol.AuthenticatedContext{Principal: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"}, ActorFQN: &actor, Execution: &execution}
}

func testInvocation(t *testing.T) protocol.Invocation {
	t.Helper()
	identity := testIdentity()
	provenance, err := fake.ProvenanceBasis()
	if err != nil {
		t.Fatal(err)
	}
	command := kernel.KernelCommand{
		ContractManifest: kernel.ContractIdentity,
		CommandID:        kernel.UUIDv7("00000000-0000-7000-8000-000000000001"),
		CommandType:      "tekroo.command.story.create", CommandVersion: kernel.SchemaVersion,
		Target:    kernel.AggregateRef{Kind: kernel.AggregateStory, ID: kernel.UUIDv7("00000000-0000-7000-8000-000000000002")},
		Authority: identity.Principal, ActorFQN: identity.ActorFQN, Execution: identity.Execution,
		ExpectedRevision: kernel.MustNotExist(), ExpectedPolicyRevision: 1, ExpectedCatalogueRevision: kernel.CatalogueRevision,
		IdempotencyKey: "http-test", CorrelationID: kernel.UUIDv7("00000000-0000-7000-8000-000000000003"),
		Payload: json.RawMessage(`{"title":"transport test"}`),
	}
	return protocol.Invocation{
		EnvelopeVersion: protocol.EnvelopeVersion,
		RequestID:       kernel.UUIDv7("00000000-0000-7000-8000-000000000004"),
		TimeoutMillis:   100,
		Command:         protocol.CommandFromKernel(command),
		Provenance:      provenance,
	}
}
