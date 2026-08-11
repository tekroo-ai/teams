package mcp_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/fake"
	"github.com/tekroo-ai/teams/adapters/httpapi"
	"github.com/tekroo-ai/teams/adapters/mcp"
	"github.com/tekroo-ai/teams/adapters/protocol"
	"github.com/tekroo-ai/teams/kernel"
)

type endpointFunc func(context.Context, protocol.Request) protocol.Response

func (function endpointFunc) Invoke(ctx context.Context, request protocol.Request) protocol.Response {
	return function(ctx, request)
}

func TestMCPStatelessToolsListAndCommandCall(t *testing.T) {
	identity := mcpIdentity("principal")
	var received protocol.Request
	handler := newMCPHandler(t, endpointFunc(func(_ context.Context, request protocol.Request) protocol.Response {
		received = request
		receipt := kernel.CommandReceipt{ContractManifest: kernel.ContractIdentity, CommandID: request.Command.CommandID, OutcomeCode: kernel.OutcomeApplied}
		return protocol.Response{EnvelopeVersion: protocol.EnvelopeVersion, RequestID: request.RequestID, Status: protocol.StatusReceipt, Receipt: &receipt}
	}), identity, httpapi.OriginPolicyFunc(func(origin string) bool { return origin == "https://mcp.tekroo.test" }), allowRate(), mcp.DefaultMaxBodyBytes)
	discover := mcpRequest(t, "server/discover", "discover-1", map[string]any{})
	discover.Header.Set("Origin", "https://mcp.tekroo.test")
	discoverResponse := httptest.NewRecorder()
	handler.ServeHTTP(discoverResponse, discover)
	if discoverResponse.Code != http.StatusOK || !strings.Contains(discoverResponse.Body.String(), `"supportedVersions":["2026-07-28"]`) || !strings.Contains(discoverResponse.Body.String(), `"capabilities":{"tools":{}}`) {
		t.Fatalf("server/discover status=%d body=%s", discoverResponse.Code, discoverResponse.Body.String())
	}

	list := mcpRequest(t, "tools/list", "list-1", map[string]any{})
	list.Header.Set("Origin", "https://mcp.tekroo.test")
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, list)
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), mcp.CommandToolName) || !strings.Contains(listResponse.Body.String(), `"resultType":"complete"`) || !strings.Contains(listResponse.Body.String(), `"cacheScope":"private"`) {
		t.Fatalf("tools/list status=%d body=%s", listResponse.Code, listResponse.Body.String())
	}

	call := mcpRequest(t, "tools/call", 2, map[string]any{"name": mcp.CommandToolName, "arguments": mcpInvocation(t)})
	call.Header.Set(mcp.NameHeader, mcp.CommandToolName)
	callResponse := httptest.NewRecorder()
	handler.ServeHTTP(callResponse, call)
	receivedIdentity := protocol.AuthenticatedContext{Principal: received.AuthenticatedPrincipal, ActorFQN: received.AuthenticatedActorFQN, Execution: received.AuthenticatedExecution}
	if callResponse.Code != http.StatusOK || !receivedIdentity.Equal(identity) || !strings.Contains(callResponse.Body.String(), `"isError":false`) || !strings.Contains(callResponse.Body.String(), `"structuredContent"`) || !strings.Contains(callResponse.Body.String(), `"io.modelcontextprotocol/serverInfo"`) {
		t.Fatalf("tools/call status=%d identity=%#v body=%s", callResponse.Code, receivedIdentity, callResponse.Body.String())
	}
}

func TestMCPValidatesPerRequestMetadataAndMirroredHeaders(t *testing.T) {
	identity := mcpIdentity("principal")
	var calls atomic.Int32
	handler := newMCPHandler(t, endpointFunc(func(_ context.Context, request protocol.Request) protocol.Response {
		calls.Add(1)
		receipt := kernel.CommandReceipt{CommandID: request.Command.CommandID, OutcomeCode: kernel.OutcomeApplied}
		return protocol.Response{EnvelopeVersion: protocol.EnvelopeVersion, RequestID: request.RequestID, Status: protocol.StatusReceipt, Receipt: &receipt}
	}), identity, allowOrigin(), allowRate(), mcp.DefaultMaxBodyBytes)

	tests := []struct {
		name   string
		mutate func(*http.Request)
		status int
		code   string
	}{
		{name: "missing version header", mutate: func(request *http.Request) { request.Header.Del(mcp.ProtocolVersionHeader) }, status: http.StatusBadRequest, code: `"code":-32020`},
		{name: "unsupported version", mutate: func(request *http.Request) { request.Header.Set(mcp.ProtocolVersionHeader, "2099-01-01") }, status: http.StatusBadRequest, code: `"code":-32022`},
		{name: "method mismatch", mutate: func(request *http.Request) { request.Header.Set(mcp.MethodHeader, "tools/list") }, status: http.StatusBadRequest, code: `"code":-32020`},
		{name: "name mismatch", mutate: func(request *http.Request) { request.Header.Set(mcp.NameHeader, "other.tool") }, status: http.StatusBadRequest, code: `"code":-32020`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := mcpRequest(t, "tools/call", 1, map[string]any{"name": mcp.CommandToolName, "arguments": mcpInvocation(t)})
			request.Header.Set(mcp.NameHeader, mcp.CommandToolName)
			test.mutate(request)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status || !strings.Contains(response.Body.String(), test.code) {
				t.Fatalf("status=%d, want=%d body=%s", response.Code, test.status, response.Body.String())
			}
		})
	}
	encodedName := base64.StdEncoding.EncodeToString([]byte(mcp.CommandToolName))
	validEncoded := mcpRequest(t, "tools/call", 2, map[string]any{"name": mcp.CommandToolName, "arguments": mcpInvocation(t)})
	validEncoded.Header.Set(mcp.NameHeader, "=?base64?"+encodedName+"?=")
	validResponse := httptest.NewRecorder()
	handler.ServeHTTP(validResponse, validEncoded)
	if validResponse.Code != http.StatusOK || calls.Load() != 1 {
		t.Fatalf("encoded header status=%d calls=%d body=%s", validResponse.Code, calls.Load(), validResponse.Body.String())
	}
	bodyMismatch := rawMCPRequest(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/list",
		"params": map[string]any{"_meta": map[string]any{
			"io.modelcontextprotocol/protocolVersion":    "2099-01-01",
			"io.modelcontextprotocol/clientCapabilities": map[string]any{},
		}},
	})
	bodyMismatch.Header.Set(mcp.MethodHeader, "tools/list")
	bodyMismatchResponse := httptest.NewRecorder()
	handler.ServeHTTP(bodyMismatchResponse, bodyMismatch)
	if bodyMismatchResponse.Code != http.StatusBadRequest || !strings.Contains(bodyMismatchResponse.Body.String(), `"code":-32020`) {
		t.Fatalf("body mismatch status=%d body=%s", bodyMismatchResponse.Code, bodyMismatchResponse.Body.String())
	}
}

func TestMCPRejectsMissingBodyMetadataAndNonRequestMessages(t *testing.T) {
	identity := mcpIdentity("principal")
	handler := newMCPHandler(t, endpointFunc(func(context.Context, protocol.Request) protocol.Response {
		t.Fatal("endpoint must not be called")
		return protocol.Response{}
	}), identity, allowOrigin(), allowRate(), mcp.DefaultMaxBodyBytes)
	tests := []struct {
		name   string
		body   map[string]any
		method string
		status int
		code   string
	}{
		{name: "missing metadata", body: map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": map[string]any{}}, method: "tools/list", status: http.StatusBadRequest, code: `"code":-32602`},
		{name: "response from client", body: map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"resultType": "complete"}}, method: "tools/list", status: http.StatusBadRequest, code: `"code":-32600`},
		{name: "notification", body: map[string]any{"jsonrpc": "2.0", "method": "notifications/cancelled", "params": map[string]any{"_meta": mcpMetadata()}}, method: "notifications/cancelled", status: http.StatusBadRequest, code: `"code":-32600`},
		{name: "fractional request id", body: map[string]any{"jsonrpc": "2.0", "id": 1.5, "method": "tools/list", "params": map[string]any{"_meta": mcpMetadata()}}, method: "tools/list", status: http.StatusBadRequest, code: `"code":-32600`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := rawMCPRequest(t, test.body)
			request.Header.Set(mcp.MethodHeader, test.method)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status || !strings.Contains(response.Body.String(), test.code) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestMCPFailsClosedOnOriginAuthenticationRateAcceptBodyAndMethod(t *testing.T) {
	identity := mcpIdentity("principal")
	endpoint := endpointFunc(func(context.Context, protocol.Request) protocol.Response {
		t.Fatal("endpoint must not be called")
		return protocol.Response{}
	})
	tests := []struct {
		name    string
		auth    httpapi.Authenticator
		origin  httpapi.OriginPolicy
		limiter httpapi.RateLimiter
		max     int64
		mutate  func(*http.Request)
		status  int
	}{
		{name: "origin", auth: mcpAuthenticator(identity), origin: httpapi.OriginPolicyFunc(func(string) bool { return false }), limiter: allowRate(), max: mcp.DefaultMaxBodyBytes, mutate: func(request *http.Request) { request.Header.Set("Origin", "https://denied.test") }, status: http.StatusForbidden},
		{name: "authentication", auth: httpapi.AuthenticatorFunc(func(*http.Request) (protocol.AuthenticatedContext, error) {
			return protocol.AuthenticatedContext{}, errors.New("denied")
		}), origin: allowOrigin(), limiter: allowRate(), max: mcp.DefaultMaxBodyBytes, status: http.StatusUnauthorized},
		{name: "rate", auth: mcpAuthenticator(identity), origin: allowOrigin(), limiter: httpapi.RateLimiterFunc(func(protocol.AuthenticatedContext) bool { return false }), max: mcp.DefaultMaxBodyBytes, status: http.StatusTooManyRequests},
		{name: "accept wildcard", auth: mcpAuthenticator(identity), origin: allowOrigin(), limiter: allowRate(), max: mcp.DefaultMaxBodyBytes, mutate: func(request *http.Request) { request.Header.Set("Accept", "*/*") }, status: http.StatusNotAcceptable},
		{name: "body", auth: mcpAuthenticator(identity), origin: allowOrigin(), limiter: allowRate(), max: 8, status: http.StatusRequestEntityTooLarge},
		{name: "GET", auth: mcpAuthenticator(identity), origin: allowOrigin(), limiter: allowRate(), max: mcp.DefaultMaxBodyBytes, mutate: func(request *http.Request) { request.Method = http.MethodGet }, status: http.StatusMethodNotAllowed},
		{name: "DELETE", auth: mcpAuthenticator(identity), origin: allowOrigin(), limiter: allowRate(), max: mcp.DefaultMaxBodyBytes, mutate: func(request *http.Request) { request.Method = http.MethodDelete }, status: http.StatusMethodNotAllowed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, err := mcp.NewHandler(endpoint, test.auth, test.origin, test.limiter, test.max)
			if err != nil {
				t.Fatal(err)
			}
			request := mcpRequest(t, "tools/list", 1, map[string]any{})
			if test.mutate != nil {
				test.mutate(request)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d, want=%d body=%s", response.Code, test.status, response.Body.String())
			}
		})
	}
}

func TestMCPUnknownMethodUsesHTTP404AndJSONRPCMethodNotFound(t *testing.T) {
	handler := newMCPHandler(t, endpointFunc(func(context.Context, protocol.Request) protocol.Response { return protocol.Response{} }), mcpIdentity("principal"), allowOrigin(), allowRate(), mcp.DefaultMaxBodyBytes)
	request := mcpRequest(t, "unknown/method", 1, map[string]any{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"code":-32601`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestMCPToolPreservesGatewayTimeoutUncertainty(t *testing.T) {
	identity := mcpIdentity("principal")
	service := serviceFunc(func(ctx context.Context, _ kernel.KernelCommand, _ kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		<-ctx.Done()
		return kernel.CommandReceipt{}, ctx.Err()
	})
	gateway, err := protocol.NewGateway(service, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	handler := newMCPHandler(t, gateway, identity, allowOrigin(), allowRate(), mcp.DefaultMaxBodyBytes)
	invocation := mcpInvocation(t)
	invocation.TimeoutMillis = 5
	call := mcpRequest(t, "tools/call", 2, map[string]any{"name": mcp.CommandToolName, "arguments": invocation})
	call.Header.Set(mcp.NameHeader, mcp.CommandToolName)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"isError":true`) || !strings.Contains(response.Body.String(), `"outcome_unknown":true`) || !strings.Contains(response.Body.String(), `"code":"TIMEOUT"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

type serviceFunc func(context.Context, kernel.KernelCommand, kernel.ProvenanceBasis) (kernel.CommandReceipt, error)

func (function serviceFunc) Handle(ctx context.Context, command kernel.KernelCommand, provenance kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
	return function(ctx, command, provenance)
}

func newMCPHandler(t *testing.T, endpoint protocol.Endpoint, identity protocol.AuthenticatedContext, origins httpapi.OriginPolicy, limiter httpapi.RateLimiter, max int64) *mcp.Handler {
	t.Helper()
	handler, err := mcp.NewHandler(endpoint, mcpAuthenticator(identity), origins, limiter, max)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func mcpAuthenticator(identity protocol.AuthenticatedContext) httpapi.Authenticator {
	return httpapi.AuthenticatorFunc(func(*http.Request) (protocol.AuthenticatedContext, error) { return identity, nil })
}

func allowOrigin() httpapi.OriginPolicy {
	return httpapi.OriginPolicyFunc(func(string) bool { return true })
}

func allowRate() httpapi.RateLimiter {
	return httpapi.RateLimiterFunc(func(protocol.AuthenticatedContext) bool { return true })
}

func mcpRequest(t *testing.T, method string, id any, fields map[string]any) *http.Request {
	t.Helper()
	params := make(map[string]any, len(fields)+1)
	for key, value := range fields {
		params[key] = value
	}
	params["_meta"] = mcpMetadata()
	request := rawMCPRequest(t, map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	request.Header.Set(mcp.MethodHeader, method)
	return request
}

func rawMCPRequest(t *testing.T, value any) *http.Request {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set(mcp.ProtocolVersionHeader, mcp.ProtocolVersion)
	return request
}

func mcpMetadata() map[string]any {
	return map[string]any{
		"io.modelcontextprotocol/protocolVersion":    mcp.ProtocolVersion,
		"io.modelcontextprotocol/clientCapabilities": map[string]any{},
		"io.modelcontextprotocol/clientInfo":         map[string]any{"name": "test", "version": "1"},
	}
}

func mcpIdentity(principal string) protocol.AuthenticatedContext {
	actor := kernel.ActorFQN("teams::coder-1")
	execution := kernel.ExecutionTuple{ExecutionID: kernel.UUIDv7("00000000-0000-7000-8000-000000000091"), FencingEpoch: 7}
	return protocol.AuthenticatedContext{Principal: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: principal}, ActorFQN: &actor, Execution: &execution}
}

func mcpInvocation(t *testing.T) protocol.Invocation {
	t.Helper()
	identity := mcpIdentity("principal")
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
		IdempotencyKey: "mcp-test", CorrelationID: kernel.UUIDv7("00000000-0000-7000-8000-000000000003"),
		Payload: json.RawMessage(`{"title":"MCP transport test"}`),
	}
	return protocol.Invocation{EnvelopeVersion: protocol.EnvelopeVersion, RequestID: kernel.UUIDv7("00000000-0000-7000-8000-000000000004"), TimeoutMillis: 100, Command: protocol.CommandFromKernel(command), Provenance: provenance}
}
