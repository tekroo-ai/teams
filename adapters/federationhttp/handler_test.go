package federationhttp

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestHandlerAndClientAcceptExactlyOnce(t *testing.T) {
	now := time.Date(2026, 9, 1, 14, 0, 0, 0, time.UTC)
	route, grant, envelope, ingress := testIngress(t, now)
	handler, err := NewHandler(ingress, func() time.Time { return now.Add(time.Second) }, DefaultMaximumBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	route.Endpoint = server.URL + "/v1/federation/ingress"
	route.AllowInsecureLoopbackForTest = true
	client, err := NewClient(&http.Client{Timeout: time.Second}, DefaultMaximumBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := client.Send(context.Background(), route, envelope)
	if err != nil || !receipt.Valid() {
		t.Fatalf("first = %#v %v", receipt, err)
	}
	repeated, err := client.Send(context.Background(), route, envelope)
	if err != nil || repeated != receipt {
		t.Fatalf("repeat = %#v %v", repeated, err)
	}
	_ = grant
}

func TestHandlerRejectsMalformedAndTamperedInput(t *testing.T) {
	now := time.Date(2026, 9, 1, 14, 0, 0, 0, time.UTC)
	_, _, envelope, ingress := testIngress(t, now)
	handler, err := NewHandler(ingress, func() time.Time { return now }, DefaultMaximumBodyBytes)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/federation/ingress", strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("empty status = %d", response.Code)
	}

	envelope.Signature = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	raw, _ := json.Marshal(envelope)
	request = httptest.NewRequest(http.MethodPost, "/v1/federation/ingress", strings.NewReader(string(raw)))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("tampered status = %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/federation/ingress", strings.NewReader(string(raw)))
	request.Header.Set("Content-Type", "text/plain")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("content type status = %d", response.Code)
	}
}

func testIngress(t *testing.T, now time.Time) (organization.FederationRoute, organization.FederationTrustGrant, organization.FederatedEnvelope, *organization.FederationIngress) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	source := kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	destination := kernel.Digest("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	route := organization.FederationRoute{SchemaVersion: organization.FederationSchemaVersion, RouteID: "00000000-0000-7000-8000-000000000110", Revision: 1, SourceDeployment: source, DestinationDeployment: destination, SourceActor: "source::product-owner-1", DestinationActor: "destination::architect-1", MessageTypes: []string{"tekroo.message.feature.request"}, Purposes: []organization.MessagePurpose{organization.PurposeRequest}, KeyID: "source-key", Endpoint: "http://127.0.0.1:19091/v1/federation/ingress", Status: organization.FederationActive, AllowInsecureLoopbackForTest: true}
	grant := organization.FederationTrustGrant{SchemaVersion: organization.FederationSchemaVersion, PeerID: "source", DeploymentIdentity: source, KeyID: "source-key", KeyEpoch: 1, PublicKey: base64.StdEncoding.EncodeToString(publicKey), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), Status: organization.FederationActive}
	message := organization.OrganizationalMessage{SchemaVersion: organization.OrganizationalMessageSchemaVersion, ID: "00000000-0000-7000-8000-000000000120", Type: "tekroo.message.feature.request", Purpose: organization.PurposeRequest, Sender: route.SourceActor, SenderExecution: kernel.ExecutionTuple{ExecutionID: "00000000-0000-7000-8000-000000000111", FencingEpoch: 1}, Recipient: route.DestinationActor, CorrelationID: "00000000-0000-7000-8000-000000000112", Work: organization.MessageWorkLink{DAGNodeID: "00000000-0000-7000-8000-000000000113"}, Flow: organization.MessageFlow{ThreadID: "00000000-0000-7000-8000-000000000112", StepID: "00000000-0000-7000-8000-000000000113", Hop: 1, MaximumHops: 8, BudgetAccountID: "00000000-0000-7000-8000-000000000114", LifecycleEpoch: 1, ScopeRevision: 1, ProgressDigest: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}, Body: json.RawMessage(`{"request":"design"}`), CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	envelope, err := organization.NewFederatedEnvelope(message, route, nil, grant, "00000000-0000-7000-8000-000000000121", "00000000-0000-7000-8000-000000000122", now, time.Minute, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := organization.NewStaticFederationRegistry(nil, []organization.FederationRoute{route}, []organization.FederationTrustGrant{grant})
	if err != nil {
		t.Fatal(err)
	}
	store, err := organization.NewMemoryFederationStore(organization.NewMemoryOrganizationalMessageStore())
	if err != nil {
		t.Fatal(err)
	}
	ingress, err := organization.NewFederationIngress(registry, store, destination, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return route, grant, envelope, ingress
}

type errorTransport struct{ err error }

func (transport errorTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, transport.err
}

func TestClientReturnsBoundedTransportFailure(t *testing.T) {
	client, err := NewClient(&http.Client{Timeout: time.Second, Transport: errorTransport{err: errors.New("offline")}}, DefaultMaximumBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	_, _, envelope, _ := testIngress(t, time.Date(2026, 9, 1, 14, 0, 0, 0, time.UTC))
	route := organization.FederationRoute{SchemaVersion: organization.FederationSchemaVersion, RouteID: envelope.RouteID, Revision: envelope.RouteRevision, SourceDeployment: envelope.SourceDeployment, DestinationDeployment: envelope.DestinationDeployment, SourceActor: envelope.SourceActor, DestinationActor: envelope.DestinationActor, MessageTypes: []string{"tekroo.message.feature.request"}, Purposes: []organization.MessagePurpose{organization.PurposeRequest}, KeyID: envelope.KeyID, Endpoint: "https://example.invalid/v1/federation/ingress", Status: organization.FederationActive}
	if _, err := client.Send(context.Background(), route, envelope); err == nil {
		t.Fatal("transport failure was hidden")
	}
}
