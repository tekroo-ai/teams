package organization

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

func TestFederationCoordinatorResolvesAuthorizesRecordsThenSends(t *testing.T) {
	now := time.Date(2026, 9, 1, 16, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	route, grant, alias := federationFixtures(now, publicKey)
	route.SourceActor = "source::product-owner-1"
	registry, err := NewStaticFederationRegistry([]AliasBinding{alias}, []FederationRoute{route}, []FederationTrustGrant{grant})
	if err != nil {
		t.Fatal(err)
	}
	execution := kernel.ExecutionTuple{ExecutionID: "00000000-0000-7000-8000-000000000211", FencingEpoch: 3}
	roles := &federationRoleStore{role: RoleInstanceState{ActorFQN: route.SourceActor, Status: RoleIdle, Execution: execution}}
	store := &outboundRecorder{}
	transport := &federationTransport{}
	ids := &federationIDs{values: []kernel.UUIDv7{"00000000-0000-7000-8000-000000000221", "00000000-0000-7000-8000-000000000222"}}
	coordinator, err := NewFederationCoordinator(registry, roles, store, transport, fixedFederationClock{now}, ids, route.SourceDeployment, grant, privateKey, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	message := federationMessage(now)
	message.SenderExecution = execution
	message.Recipient = ""
	receipt, err := coordinator.SendByAlias(context.Background(), alias.Name, message)
	if err != nil || !receipt.Valid() {
		t.Fatalf("send = %#v %v", receipt, err)
	}
	if !store.recorded || !transport.called {
		t.Fatalf("recorded=%v sent=%v", store.recorded, transport.called)
	}
	if transport.envelope.AliasName != alias.Name || transport.envelope.AliasRevision != alias.Revision || transport.envelope.DestinationActor != alias.ActorFQN {
		t.Fatalf("envelope = %#v", transport.envelope)
	}
}

func TestFederationCoordinatorRejectsStaleActorBeforeRecording(t *testing.T) {
	now := time.Date(2026, 9, 1, 16, 0, 0, 0, time.UTC)
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	route, grant, alias := federationFixtures(now, publicKey)
	registry, _ := NewStaticFederationRegistry([]AliasBinding{alias}, []FederationRoute{route}, []FederationTrustGrant{grant})
	roles := &federationRoleStore{role: RoleInstanceState{ActorFQN: route.SourceActor, Status: RoleStopped}}
	store := &outboundRecorder{}
	transport := &federationTransport{}
	ids := &federationIDs{values: []kernel.UUIDv7{"00000000-0000-7000-8000-000000000231", "00000000-0000-7000-8000-000000000232"}}
	coordinator, _ := NewFederationCoordinator(registry, roles, store, transport, fixedFederationClock{now}, ids, route.SourceDeployment, grant, privateKey, time.Minute)
	_, err := coordinator.SendByAlias(context.Background(), alias.Name, federationMessage(now))
	if !errors.Is(err, ErrStaleOrganizationalClaim) || store.recorded || transport.called {
		t.Fatalf("err=%v recorded=%v sent=%v", err, store.recorded, transport.called)
	}
}

type fixedFederationClock struct{ value time.Time }

func (clock fixedFederationClock) Now() time.Time { return clock.value }

type federationIDs struct{ values []kernel.UUIDv7 }

func (ids *federationIDs) Next() (kernel.UUIDv7, error) {
	value := ids.values[0]
	ids.values = ids.values[1:]
	return value, nil
}

type federationRoleStore struct{ role RoleInstanceState }

func (store *federationRoleStore) LoadRole(_ context.Context, actor kernel.ActorFQN) (RoleInstanceState, bool, error) {
	return store.role, store.role.ActorFQN == actor, nil
}
func (*federationRoleStore) CompareAndSwapRole(context.Context, uint64, RoleInstanceState) error {
	return nil
}
func (*federationRoleStore) ListRoles(context.Context, string) ([]RoleInstanceState, error) {
	return nil, nil
}

type outboundRecorder struct{ recorded bool }

func (store *outboundRecorder) RecordFederatedOutbound(_ context.Context, _ FederatedEnvelope, _ OrganizationalMessage, _ time.Time) (bool, error) {
	store.recorded = true
	return true, nil
}

type federationTransport struct {
	called   bool
	envelope FederatedEnvelope
}

func (transport *federationTransport) Send(_ context.Context, _ FederationRoute, envelope FederatedEnvelope) (FederationDeliveryReceipt, error) {
	transport.called, transport.envelope = true, envelope
	return FederationDeliveryReceipt{SchemaVersion: FederationSchemaVersion, DeliveryID: envelope.DeliveryID, ReplayID: envelope.ReplayID, RouteID: envelope.RouteID, RouteRevision: envelope.RouteRevision, MessageID: "00000000-0000-7000-8000-000000000020", MessageSHA256: envelope.MessageSHA256, AcceptedAt: time.Date(2026, 9, 1, 16, 0, 1, 0, time.UTC), Outcome: "ACCEPTED"}, nil
}

func TestSigningGrantPublicKeyRoundTrip(t *testing.T) {
	publicKey, _, _ := ed25519.GenerateKey(rand.Reader)
	grant := FederationTrustGrant{SchemaVersion: FederationSchemaVersion, PeerID: "peer", DeploymentIdentity: fedSourceDeployment, KeyID: "key", KeyEpoch: 1, PublicKey: base64.StdEncoding.EncodeToString(publicKey), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), Status: FederationActive}
	decoded, err := grant.PublicEd25519Key()
	if err != nil || !decoded.Equal(publicKey) {
		t.Fatalf("key=%x err=%v", decoded, err)
	}
}
