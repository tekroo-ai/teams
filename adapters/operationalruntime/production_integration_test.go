//go:build mongo_integration

package operationalruntime

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/organization"
)

func TestProductionServiceStartsAndStopsAcceptedRuntimeFromFileConfiguration(t *testing.T) {
	process, uri := startRuntimeMongod(t)
	defer stopRuntimeMongod(process)
	path, config := writeProductionFixture(t)
	directory := filepath.Dir(path)
	writeText(t, filepath.Join(directory, config.Mongo.URIFile), uri+"\n", 0o600)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	writeText(t, filepath.Join(directory, "federation.key"), base64.StdEncoding.EncodeToString(privateKey)+"\n", 0o600)
	now := time.Now().UTC()
	destination := repeatedDigest('d')
	route := organization.FederationRoute{SchemaVersion: organization.FederationSchemaVersion, RouteID: "00000000-0000-7000-8000-000000000710", Revision: 1, SourceDeployment: config.DeploymentIdentity, DestinationDeployment: destination, SourceActor: "fixture::coder-1", DestinationActor: "remote::architect-1", MessageTypes: []string{"tekroo.message.feature.request"}, Purposes: []organization.MessagePurpose{organization.PurposeRequest}, KeyID: "fixture-federation-key", Endpoint: "http://127.0.0.1:18992/v1/federation/ingress", Status: organization.FederationActive, AllowInsecureLoopbackForTest: true}
	grant := organization.FederationTrustGrant{SchemaVersion: organization.FederationSchemaVersion, PeerID: "fixture", DeploymentIdentity: config.DeploymentIdentity, KeyID: route.KeyID, KeyEpoch: 1, PublicKey: base64.StdEncoding.EncodeToString(publicKey), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), Status: organization.FederationActive}
	alias := organization.AliasBinding{SchemaVersion: organization.FederationSchemaVersion, Name: "remote-architect", Revision: 1, RouteID: route.RouteID, RouteRevision: route.Revision, DeploymentIdentity: destination, ActorFQN: route.DestinationActor}
	config.Federation = &ProductionFederation{Address: "127.0.0.1:18991", MaximumBodyBytes: 1 << 20, RequestTimeout: "5s", AllowedFutureSkew: "10s", EnvelopeTTL: "1m", PrivateKeyFile: "federation.key", SigningIdentity: grant, Aliases: []organization.AliasBinding{alias}, Routes: []organization.FederationRoute{route}}
	writeJSON(t, path, config, 0o600)
	loaded, err := LoadProductionConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	startup, startupCancel := context.WithTimeout(context.Background(), 15*time.Second)
	service, err := NewProductionService(startup, loaded)
	startupCancel()
	if err != nil {
		t.Fatal(err)
	}
	address, maximumBody, ingress := service.FederationListener()
	if address != config.Federation.Address || maximumBody != config.Federation.MaximumBodyBytes || ingress == nil || service.Federation == nil {
		t.Fatalf("federation wiring address=%q body=%d ingress=%v coordinator=%v", address, maximumBody, ingress != nil, service.Federation != nil)
	}
	defer func() {
		closeContext, closeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer closeCancel()
		if err := service.Close(closeContext); err != nil {
			t.Error(err)
		}
	}()
	startContext, startCancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = service.Start(startContext)
	startCancel()
	if err != nil || service.Controller.Inspect().State != ControlRunning {
		t.Fatalf("start state=%s err=%v", service.Controller.Inspect().State, err)
	}
	stopContext, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	err = service.Stop(stopContext)
	stopCancel()
	if err != nil || service.Controller.Inspect().State != ControlStopped {
		t.Fatalf("stop state=%s err=%v", service.Controller.Inspect().State, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "evidence")); err != nil {
		t.Fatalf("evidence root not created: %v", err)
	}
}
