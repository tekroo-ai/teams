package organization

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

const (
	fedSourceDeployment      = kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	fedDestinationDeployment = kernel.Digest("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
)

func TestFederationExactSignedIngressAndReplay(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	route, grant, alias := federationFixtures(now, publicKey)
	registry, err := NewStaticFederationRegistry([]AliasBinding{alias}, []FederationRoute{route}, []FederationTrustGrant{grant})
	if err != nil {
		t.Fatal(err)
	}
	messages := NewMemoryOrganizationalMessageStore()
	store, err := NewMemoryFederationStore(messages)
	if err != nil {
		t.Fatal(err)
	}
	ingress, err := NewFederationIngress(registry, store, fedDestinationDeployment, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	message := federationMessage(now)
	envelope, err := NewFederatedEnvelope(message, route, &alias, grant, "00000000-0000-7000-8000-000000000021", "00000000-0000-7000-8000-000000000022", now, time.Minute, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	receipt, created, err := ingress.Accept(context.Background(), envelope, now.Add(time.Second))
	if err != nil || !created || !receipt.Valid() || receipt.MessageID != message.ID {
		t.Fatalf("accept = %#v %v %v", receipt, created, err)
	}
	repeated, created, err := ingress.Accept(context.Background(), envelope, now.Add(2*time.Second))
	if err != nil || created || repeated != receipt {
		t.Fatalf("repeat = %#v %v %v", repeated, created, err)
	}
	claim, found, err := messages.ReadMessage(context.Background(), message.ID)
	if err != nil || !found || claim.Message.Recipient != route.DestinationActor {
		t.Fatalf("message = %#v %v %v", claim, found, err)
	}
	resolved, found, err := registry.ResolveAlias(context.Background(), "remote-architect")
	if err != nil || !found || resolved != alias {
		t.Fatalf("alias = %#v %v %v", resolved, found, err)
	}
}

func TestFederationRejectsTamperAuthorityClockReplayAndLoop(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	route, grant, alias := federationFixtures(now, publicKey)
	registry, err := NewStaticFederationRegistry([]AliasBinding{alias}, []FederationRoute{route}, []FederationTrustGrant{grant})
	if err != nil {
		t.Fatal(err)
	}
	message := federationMessage(now)
	original, err := NewFederatedEnvelope(message, route, &alias, grant, "00000000-0000-7000-8000-000000000031", "00000000-0000-7000-8000-000000000032", now, time.Minute, privateKey)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(FederatedEnvelope) FederatedEnvelope
		at     time.Time
		target error
	}{
		{"payload", func(value FederatedEnvelope) FederatedEnvelope {
			value.Message = json.RawMessage(`{"changed":true}`)
			return value
		}, now, ErrFederationUnauthorized},
		{"signature", func(value FederatedEnvelope) FederatedEnvelope {
			value.Signature = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
			return value
		}, now, ErrFederationUnauthorized},
		{"destination", func(value FederatedEnvelope) FederatedEnvelope {
			value.DestinationActor = "remote::architect-1"
			return value
		}, now, ErrFederationUnauthorized},
		{"route-loop", func(value FederatedEnvelope) FederatedEnvelope {
			value.RouteTrace = append(value.RouteTrace, fedDestinationDeployment)
			return value
		}, now, ErrFederationUnauthorized},
		{"future", func(value FederatedEnvelope) FederatedEnvelope { return value }, now.Add(-time.Minute), ErrFederationClock},
		{"expired", func(value FederatedEnvelope) FederatedEnvelope { return value }, now.Add(2 * time.Minute), ErrFederationClock},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, _ := NewMemoryFederationStore(NewMemoryOrganizationalMessageStore())
			ingress, _ := NewFederationIngress(registry, store, fedDestinationDeployment, 5*time.Second)
			_, _, err := ingress.Accept(context.Background(), test.mutate(original), test.at)
			if !errors.Is(err, test.target) {
				t.Fatalf("error = %v, want %v", err, test.target)
			}
		})
	}

	store, _ := NewMemoryFederationStore(NewMemoryOrganizationalMessageStore())
	ingress, _ := NewFederationIngress(registry, store, fedDestinationDeployment, 5*time.Second)
	if _, _, err := ingress.Accept(context.Background(), original, now); err != nil {
		t.Fatal(err)
	}
	conflicting := original
	conflicting.DeliveryID = "00000000-0000-7000-8000-000000000039"
	material, _ := json.Marshal(conflicting.signingMaterial())
	conflicting.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, material))
	if _, _, err := ingress.Accept(context.Background(), conflicting, now); !errors.Is(err, ErrFederationReplayConflict) {
		t.Fatalf("replay error = %v", err)
	}
}

func TestFederationRejectsWildcardsAndInsecureRemoteEndpoint(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	publicKey, _, _ := ed25519.GenerateKey(rand.Reader)
	route, _, alias := federationFixtures(now, publicKey)
	route.MessageTypes = []string{"tekroo.message.*"}
	if !errors.Is(route.Validate(), ErrInvalidFederation) {
		t.Fatal("wildcard route was accepted")
	}
	route, _, _ = federationFixtures(now, publicKey)
	route.Endpoint = "http://remote.example/v1/federation/ingress"
	route.AllowInsecureLoopbackForTest = true
	if !errors.Is(route.Validate(), ErrInvalidFederation) {
		t.Fatal("insecure remote route was accepted")
	}
	alias.Name = "remote-*"
	if !errors.Is(alias.Validate(), ErrInvalidFederation) {
		t.Fatal("wildcard alias was accepted")
	}
}

func federationFixtures(now time.Time, publicKey ed25519.PublicKey) (FederationRoute, FederationTrustGrant, AliasBinding) {
	route := FederationRoute{SchemaVersion: FederationSchemaVersion, RouteID: "00000000-0000-7000-8000-000000000010", Revision: 1, SourceDeployment: fedSourceDeployment, DestinationDeployment: fedDestinationDeployment, SourceActor: "source::product-owner-1", DestinationActor: "destination::architect-1", MessageTypes: []string{"tekroo.message.feature.request"}, Purposes: []MessagePurpose{PurposeRequest}, KeyID: "source-key", Endpoint: "http://127.0.0.1:19091/v1/federation/ingress", Status: FederationActive, AllowInsecureLoopbackForTest: true}
	grant := FederationTrustGrant{SchemaVersion: FederationSchemaVersion, PeerID: "source", DeploymentIdentity: fedSourceDeployment, KeyID: "source-key", KeyEpoch: 1, PublicKey: base64.StdEncoding.EncodeToString(publicKey), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), Status: FederationActive}
	alias := AliasBinding{SchemaVersion: FederationSchemaVersion, Name: "remote-architect", Revision: 1, RouteID: route.RouteID, RouteRevision: route.Revision, DeploymentIdentity: route.DestinationDeployment, ActorFQN: route.DestinationActor}
	return route, grant, alias
}

func federationMessage(now time.Time) OrganizationalMessage {
	return OrganizationalMessage{SchemaVersion: OrganizationalMessageSchemaVersion, ID: "00000000-0000-7000-8000-000000000020", Type: "tekroo.message.feature.request", Purpose: PurposeRequest, Sender: "source::product-owner-1", SenderExecution: kernel.ExecutionTuple{ExecutionID: "00000000-0000-7000-8000-000000000011", FencingEpoch: 1}, Recipient: "destination::architect-1", CorrelationID: "00000000-0000-7000-8000-000000000012", Work: MessageWorkLink{DAGNodeID: "00000000-0000-7000-8000-000000000013"}, Flow: MessageFlow{ThreadID: "00000000-0000-7000-8000-000000000012", StepID: "00000000-0000-7000-8000-000000000013", Hop: 1, MaximumHops: 8, BudgetAccountID: "00000000-0000-7000-8000-000000000014", LifecycleEpoch: 1, ScopeRevision: 1, ProgressDigest: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}, Body: json.RawMessage(`{"request":"design"}`), CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
}
