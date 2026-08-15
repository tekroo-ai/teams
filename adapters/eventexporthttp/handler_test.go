package eventexporthttp_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/eventexporthttp"
	"github.com/tekroo-ai/teams/adapters/httpapi"
	"github.com/tekroo-ai/teams/adapters/memory"
	"github.com/tekroo-ai/teams/adapters/protocol"
	"github.com/tekroo-ai/teams/eventexport"
	"github.com/tekroo-ai/teams/kernel"
)

func TestEventExportHTTPAuthenticatesAuthorizesAndReturnsExactRecord(t *testing.T) {
	source, _ := memory.NewEventExportSource("teams-main")
	raw := httpEventBytes(t)
	if err := source.Append(raw); err != nil {
		t.Fatal(err)
	}
	handler := newEventExportHandler(t, source, false, eventexporthttp.DefaultEventExportMaxBodyBytes)
	request := exportHTTPRequest(t, exportRequest())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var decoded eventexport.Response
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Status != eventexport.StatusRecords || len(decoded.Records) != 1 || decoded.Records[0].EventBytesLength != len(raw) {
		t.Fatalf("response = %#v", decoded)
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("secure headers absent")
	}
}

func TestEventExportHTTPFailsClosedBeforeSourceDisclosure(t *testing.T) {
	source, _ := memory.NewEventExportSource("teams-main")
	if err := source.Append(httpEventBytes(t)); err != nil {
		t.Fatal(err)
	}
	handler := newEventExportHandler(t, source, true, eventexporthttp.DefaultEventExportMaxBodyBytes)
	request := exportHTTPRequest(t, exportRequest())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.Code)
	}
	if bytes.Contains(response.Body.Bytes(), []byte("event_bytes")) {
		t.Fatal("unauthenticated response disclosed event bytes")
	}

	valid := newEventExportHandler(t, source, false, 8)
	oversized := exportHTTPRequest(t, exportRequest())
	overResponse := httptest.NewRecorder()
	valid.ServeHTTP(overResponse, oversized)
	if overResponse.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status = %d", overResponse.Code)
	}
}

func TestEventExportHTTPRejectsMethodMediaTypeAndUnknownFields(t *testing.T) {
	source, _ := memory.NewEventExportSource("teams-main")
	handler := newEventExportHandler(t, source, false, eventexporthttp.DefaultEventExportMaxBodyBytes)
	get := httptest.NewRequest(http.MethodGet, eventexporthttp.EventExportPath, nil)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)
	if getResponse.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d", getResponse.Code)
	}
	unknown := httptest.NewRequest(http.MethodPost, eventexporthttp.EventExportPath, bytes.NewBufferString(`{"protocol_version":"1.0.0","unknown":true}`))
	unknown.Header.Set("Content-Type", "application/json")
	unknownResponse := httptest.NewRecorder()
	handler.ServeHTTP(unknownResponse, unknown)
	if unknownResponse.Code != http.StatusBadRequest {
		t.Fatalf("unknown status = %d", unknownResponse.Code)
	}
}

func newEventExportHandler(t *testing.T, source eventexport.Source, authFails bool, maxBody int64) *eventexporthttp.EventExportHandler {
	t.Helper()
	service, err := eventexport.NewService(eventexport.Config{Source: source, Authorizer: eventexport.AuthorizerFunc(func(_ protocol.AuthenticatedContext, sourceID string) bool { return sourceID == "teams-main" }), CursorKey: []byte("0123456789abcdef0123456789abcdef"), CursorTTL: time.Hour, MaximumLimit: 100, MaximumWait: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	auth := httpapi.AuthenticatorFunc(func(*http.Request) (protocol.AuthenticatedContext, error) {
		if authFails {
			return protocol.AuthenticatedContext{}, errors.New("denied")
		}
		return protocol.AuthenticatedContext{Principal: kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "sma"}}, nil
	})
	handler, err := eventexporthttp.NewEventExportHandler(service, auth, httpapi.OriginPolicyFunc(func(string) bool { return true }), httpapi.RateLimiterFunc(func(protocol.AuthenticatedContext) bool { return true }), maxBody)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func exportHTTPRequest(t *testing.T, request eventexport.Request) *http.Request {
	t.Helper()
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	httpRequest := httptest.NewRequest(http.MethodPost, eventexporthttp.EventExportPath, bytes.NewReader(encoded))
	httpRequest.Header.Set("Content-Type", "application/json")
	return httpRequest
}

func exportRequest() eventexport.Request {
	return eventexport.Request{ProtocolVersion: eventexport.ProtocolVersion, RequestID: "00000000-0000-7000-8000-000000000001", SourceID: "teams-main", Limit: 10, WaitMillis: 100}
}

func httpEventBytes(t *testing.T) []byte {
	t.Helper()
	event := kernel.DomainEvent{ContractManifest: kernel.ContractIdentity, EventID: "00000000-0000-7000-8000-000000000101", EventType: "tekroo.event.task.created", EventVersion: "1.6.0", Aggregate: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: "00000000-0000-7000-8000-000000000102"}, AggregateRevision: 1, LifecycleEpoch: 1, CommandID: "00000000-0000-7000-8000-000000000103", Authority: kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "teams"}, CommittedAt: time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC), Payload: json.RawMessage(`{"exact":true}`), ProvenanceDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	bytes, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return bytes
}
