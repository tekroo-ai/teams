package federationhttp

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestTwoDeploymentExactFederatedSend(t *testing.T) {
	now := time.Date(2026, 9, 1, 20, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	source := kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	destination := kernel.Digest("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	route := organization.FederationRoute{SchemaVersion: organization.FederationSchemaVersion, RouteID: "00000000-0000-7000-8000-000000000510", Revision: 1, SourceDeployment: source, DestinationDeployment: destination, SourceActor: "source::product-owner-1", DestinationActor: "destination::architect-1", MessageTypes: []string{"tekroo.message.feature.request"}, Purposes: []organization.MessagePurpose{organization.PurposeRequest}, KeyID: "source-key", Endpoint: "http://" + listener.Addr().String() + "/v1/federation/ingress", Status: organization.FederationActive, AllowInsecureLoopbackForTest: true}
	grant := organization.FederationTrustGrant{SchemaVersion: organization.FederationSchemaVersion, PeerID: "source", DeploymentIdentity: source, KeyID: route.KeyID, KeyEpoch: 1, PublicKey: base64.StdEncoding.EncodeToString(publicKey), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), Status: organization.FederationActive}
	alias := organization.AliasBinding{SchemaVersion: organization.FederationSchemaVersion, Name: "destination-architect", Revision: 1, RouteID: route.RouteID, RouteRevision: route.Revision, DeploymentIdentity: destination, ActorFQN: route.DestinationActor}
	registry, err := organization.NewStaticFederationRegistry([]organization.AliasBinding{alias}, []organization.FederationRoute{route}, []organization.FederationTrustGrant{grant})
	if err != nil {
		t.Fatal(err)
	}
	destinationMessages := organization.NewMemoryOrganizationalMessageStore()
	destinationStore, _ := organization.NewMemoryFederationStore(destinationMessages)
	ingress, _ := organization.NewFederationIngress(registry, destinationStore, destination, 5*time.Second)
	handler, _ := NewHandler(ingress, func() time.Time { return now.Add(time.Second) }, DefaultMaximumBodyBytes)
	server := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Serve(listener) }()
	defer func() { _ = server.Shutdown(context.Background()); <-serverDone }()

	execution := kernel.ExecutionTuple{ExecutionID: "00000000-0000-7000-8000-000000000511", FencingEpoch: 1}
	roles := &e2eRoleStore{state: organization.RoleInstanceState{ActorFQN: route.SourceActor, Status: organization.RoleIdle, Execution: execution}}
	outbound := &e2eOutboundStore{}
	client, _ := NewClient(&http.Client{Timeout: 5 * time.Second}, DefaultMaximumBodyBytes)
	ids := &e2eIDs{values: []kernel.UUIDv7{"00000000-0000-7000-8000-000000000521", "00000000-0000-7000-8000-000000000522"}}
	coordinator, err := organization.NewFederationCoordinator(registry, roles, outbound, client, e2eClock{now}, ids, source, grant, privateKey, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	message := organization.OrganizationalMessage{SchemaVersion: organization.OrganizationalMessageSchemaVersion, ID: "00000000-0000-7000-8000-000000000520", Type: "tekroo.message.feature.request", Purpose: organization.PurposeRequest, Sender: route.SourceActor, SenderExecution: execution, CorrelationID: "00000000-0000-7000-8000-000000000512", Work: organization.MessageWorkLink{DAGNodeID: "00000000-0000-7000-8000-000000000513"}, Flow: organization.MessageFlow{ThreadID: "00000000-0000-7000-8000-000000000512", StepID: "00000000-0000-7000-8000-000000000513", Hop: 1, MaximumHops: 8, BudgetAccountID: "00000000-0000-7000-8000-000000000514", LifecycleEpoch: 1, ScopeRevision: 1, ProgressDigest: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}, Body: json.RawMessage(`{"request":"design"}`), CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	receipt, err := coordinator.SendByAlias(context.Background(), alias.Name, message)
	if err != nil || !receipt.Valid() || !outbound.recorded {
		t.Fatalf("receipt=%#v recorded=%v err=%v", receipt, outbound.recorded, err)
	}
	claim, found, err := destinationMessages.ReadMessage(context.Background(), message.ID)
	if err != nil || !found || claim.Message.Recipient != route.DestinationActor || claim.Message.Flow.BudgetAccountID != message.Flow.BudgetAccountID {
		t.Fatalf("claim=%#v found=%v err=%v", claim, found, err)
	}
}

type e2eClock struct{ now time.Time }

func (clock e2eClock) Now() time.Time { return clock.now }

type e2eIDs struct{ values []kernel.UUIDv7 }

func (ids *e2eIDs) Next() (kernel.UUIDv7, error) {
	value := ids.values[0]
	ids.values = ids.values[1:]
	return value, nil
}

type e2eRoleStore struct {
	state organization.RoleInstanceState
}

func (store *e2eRoleStore) LoadRole(_ context.Context, actor kernel.ActorFQN) (organization.RoleInstanceState, bool, error) {
	return store.state, store.state.ActorFQN == actor, nil
}
func (*e2eRoleStore) CompareAndSwapRole(context.Context, uint64, organization.RoleInstanceState) error {
	return nil
}
func (*e2eRoleStore) ListRoles(context.Context, string) ([]organization.RoleInstanceState, error) {
	return nil, nil
}

type e2eOutboundStore struct{ recorded bool }

func (store *e2eOutboundStore) RecordFederatedOutbound(context.Context, organization.FederatedEnvelope, organization.OrganizationalMessage, time.Time) (bool, error) {
	store.recorded = true
	return true, nil
}
