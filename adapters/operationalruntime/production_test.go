package operationalruntime

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/fake"
	"github.com/tekroo-ai/teams/adapters/mongo"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestExpiredLeaseRecoverySweepsImmediatelyAndAfterInterval(t *testing.T) {
	sweeper := &recordingExpiredIntentSweeper{}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	err := runExpiredLeaseRecovery(ctx, sweeper, func() time.Time { return time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC) }, time.Millisecond, time.Second, 7)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("recovery error = %v", err)
	}
	calls, attempts := sweeper.observed()
	if calls < 2 || attempts != 7 {
		t.Fatalf("sweeps=%d attempts=%d", calls, attempts)
	}
}

func TestExpiredLeaseRecoveryReportsStoreFailure(t *testing.T) {
	sweeper := &recordingExpiredIntentSweeper{err: errors.New("mongo unavailable")}
	err := runExpiredLeaseRecovery(context.Background(), sweeper, time.Now, time.Second, time.Second, 3)
	if err == nil || !strings.Contains(err.Error(), "recover expired invocation leases: mongo unavailable") {
		t.Fatalf("recovery error = %v", err)
	}
}

type recordingExpiredIntentSweeper struct {
	mu       sync.Mutex
	calls    int
	attempts uint32
	err      error
}

func (sweeper *recordingExpiredIntentSweeper) SweepExpiredIntents(_ context.Context, _ time.Time, attempts uint32) (mongo.SweepResult, error) {
	sweeper.mu.Lock()
	defer sweeper.mu.Unlock()
	sweeper.calls++
	sweeper.attempts = attempts
	return mongo.SweepResult{}, sweeper.err
}

func (sweeper *recordingExpiredIntentSweeper) observed() (int, uint32) {
	sweeper.mu.Lock()
	defer sweeper.mu.Unlock()
	return sweeper.calls, sweeper.attempts
}

func TestLoadProductionConfigResolvesAndValidatesExactLocalBindings(t *testing.T) {
	path, expected := writeProductionFixture(t)
	observed, err := LoadProductionConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if observed.Mongo.Database != expected.Mongo.Database || observed.TeamsDatabaseIdentity != expected.TeamsDatabaseIdentity || observed.SMADatabaseIdentity != expected.SMADatabaseIdentity || len(observed.Workspaces) != 1 || !filepath.IsAbs(observed.Workspaces[0].WorkingDirectory) || !filepath.IsAbs(observed.OpenHands.SessionAPIKeyFile) || !filepath.IsAbs(observed.Organization.ManifestFile) || !filepath.IsAbs(observed.Organization.Publishers[0].PublicKeyFile) {
		t.Fatalf("resolved config = %#v", observed)
	}
}

func TestLoadProductionConfigRejectsUnknownField(t *testing.T) {
	path, _ := writeProductionFixture(t)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	value["silent_fallback"] = true
	writeJSON(t, path, value, 0o600)
	if _, err := LoadProductionConfig(path); !errors.Is(err, ErrInvalidProductionConfiguration) {
		t.Fatalf("unknown-field error = %v", err)
	}
}

func TestLoadProductionConfigRejectsRemoteOpenHands(t *testing.T) {
	path, config := writeProductionFixture(t)
	config.OpenHands.BaseURL = "http://example.com:8000"
	writeJSON(t, path, config, 0o600)
	if _, err := LoadProductionConfig(path); !errors.Is(err, ErrInvalidProductionConfiguration) {
		t.Fatalf("remote endpoint error = %v", err)
	}
}

func TestLoadProductionConfigRejectsGroupReadableSecret(t *testing.T) {
	path, config := writeProductionFixture(t)
	if err := os.Chmod(filepath.Join(filepath.Dir(path), config.OpenHands.SessionAPIKeyFile), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProductionConfig(path); !errors.Is(err, ErrInvalidProductionConfiguration) {
		t.Fatalf("secret-permission error = %v", err)
	}
}

func TestLoadProductionConfigRejectsDuplicateWorkspace(t *testing.T) {
	path, config := writeProductionFixture(t)
	config.Workspaces = append(config.Workspaces, config.Workspaces[0])
	writeJSON(t, path, config, 0o600)
	if _, err := LoadProductionConfig(path); !errors.Is(err, ErrInvalidProductionConfiguration) {
		t.Fatalf("duplicate workspace error = %v", err)
	}
}

func TestLoadProductionConfigAcceptsExactLoopbackFederation(t *testing.T) {
	path, config := writeProductionFixture(t)
	directory := filepath.Dir(path)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	writeText(t, filepath.Join(directory, "federation.key"), base64.StdEncoding.EncodeToString(privateKey)+"\n", 0o600)
	now := time.Now().UTC()
	source := config.DeploymentIdentity
	destination := repeatedDigest('d')
	route := organization.FederationRoute{SchemaVersion: organization.FederationSchemaVersion, RouteID: "00000000-0000-7000-8000-000000000410", Revision: 1, SourceDeployment: source, DestinationDeployment: destination, SourceActor: "fixture::coder-1", DestinationActor: "remote::architect-1", MessageTypes: []string{"tekroo.message.feature.request"}, Purposes: []organization.MessagePurpose{organization.PurposeRequest}, KeyID: "local-federation-key", Endpoint: "http://127.0.0.1:18992/v1/federation/ingress", Status: organization.FederationActive, AllowInsecureLoopbackForTest: true}
	signing := organization.FederationTrustGrant{SchemaVersion: organization.FederationSchemaVersion, PeerID: "fixture", DeploymentIdentity: source, KeyID: route.KeyID, KeyEpoch: 1, PublicKey: base64.StdEncoding.EncodeToString(publicKey), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), Status: organization.FederationActive}
	alias := organization.AliasBinding{SchemaVersion: organization.FederationSchemaVersion, Name: "remote-architect", Revision: 1, RouteID: route.RouteID, RouteRevision: route.Revision, DeploymentIdentity: destination, ActorFQN: route.DestinationActor}
	config.Federation = &ProductionFederation{Address: "127.0.0.1:18991", MaximumBodyBytes: 1 << 20, RequestTimeout: "5s", AllowedFutureSkew: "10s", EnvelopeTTL: "1m", PrivateKeyFile: "federation.key", SigningIdentity: signing, Aliases: []organization.AliasBinding{alias}, Routes: []organization.FederationRoute{route}}
	writeJSON(t, path, config, 0o600)
	loaded, err := LoadProductionConfig(path)
	if err != nil || loaded.Federation == nil || !filepath.IsAbs(loaded.Federation.PrivateKeyFile) {
		t.Fatalf("loaded=%#v err=%v", loaded.Federation, err)
	}
	config.Federation.Routes[0].Endpoint = "http://remote.example/v1/federation/ingress"
	writeJSON(t, path, config, 0o600)
	if _, err := LoadProductionConfig(path); !errors.Is(err, ErrInvalidProductionConfiguration) {
		t.Fatalf("insecure remote endpoint err=%v", err)
	}
}

func writeProductionFixture(t *testing.T) (string, ProductionConfig) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	workspace := filepath.Join(directory, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	writeText(t, filepath.Join(directory, "mongo-uri"), "mongodb://127.0.0.1:27017\n", 0o600)
	writeText(t, filepath.Join(directory, "openhands-key"), "local-session-key\n", 0o600)
	writeText(t, filepath.Join(directory, "operator-token"), "0123456789abcdef0123456789abcdef\n", 0o600)
	provenance, err := fake.ProvenanceBasis()
	if err != nil {
		t.Fatal(err)
	}
	policy := kernel.AuthorizationPolicy{
		PolicyDigest: provenance.PolicyDigest, Revision: provenance.PolicyRevision,
		Grants: []kernel.AuthorityGrant{
			{GrantDigest: provenance.GrantDigests[0], Grantee: kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "teams-operational-runtime"}, Scope: kernel.AuthorityScope{CommandTypes: []string{"tekroo.command.execution.register", "tekroo.command.execution.replace", "tekroo.command.evidence.register", "tekroo.command.work-invocation.claim", "tekroo.command.work-invocation.record-started", "tekroo.command.work-invocation.record-terminal"}, TargetKinds: []kernel.AggregateKind{kernel.AggregateExecution, kernel.AggregateEvidence, kernel.AggregateWorkInvocation}, CanReadTarget: true}},
			{GrantDigest: repeatedDigest('b'), Grantee: kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "teams-admission-policy"}, Scope: kernel.AuthorityScope{CommandTypes: []string{"tekroo.command.work-invocation.expire", "tekroo.command.work.block"}, TargetKinds: []kernel.AggregateKind{kernel.AggregateWorkInvocation, kernel.AggregateTask}, CanReadTarget: true}},
		},
	}
	writeJSON(t, filepath.Join(directory, "authorization.json"), policy, 0o600)
	writeJSON(t, filepath.Join(directory, "provenance.json"), provenance, 0o600)
	organizationConfig := writeOrganizationFixture(t, directory)
	config := ProductionConfig{
		ContractRoot:          root,
		Mongo:                 ProductionMongoConfig{URIFile: "mongo-uri", Database: "tekroo_v4", BacklogLimit: 1024, DeliveryPolicyRevision: 1},
		OpenHands:             ProductionOpenHandsConfig{BaseURL: "http://127.0.0.1:8000", SessionAPIKeyFile: "openhands-key", RequestTimeout: "130s", PollInterval: "250ms", MaximumPages: 64, MaximumEvidenceBytes: 16 << 20},
		Operator:              ProductionOperatorConfig{Address: "127.0.0.1:8787", BearerTokenFile: "operator-token", Principal: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "operator"}, OperationTimeout: "10s", MaximumBodyBytes: 1 << 20},
		TeamsDatabaseIdentity: "tekroo_v4", SMADatabaseIdentity: "sma_v4", DeploymentIdentity: repeatedDigest('1'), AuthorizationPolicyFile: "authorization.json", ProvenanceFile: "provenance.json", EvidenceRoot: "evidence",
		ServiceAuthority: kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "teams-operational-runtime"}, ExpiryAuthority: kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "teams-admission-policy"},
		Workspaces:   []ProductionWorkspace{{WorkspaceID: "workspace-1", WorktreeID: "worktree-1", WorkingDirectory: "workspace", Branch: "task/workspace-1", BaselineSHA: strings.Repeat("1", 40), WritablePaths: []string{"."}}},
		Profiles:     []ProductionProfile{{ModelProfileDigest: repeatedDigest('2'), RuntimeIdentityDigest: repeatedDigest('3'), ToolPolicyDigest: repeatedDigest('4'), EffectPolicyDigest: repeatedDigest('5'), MaximumIterations: 24, Qualification: kernel.AssignmentQualificationReceipt{QualificationID: "00000000-0000-7000-8000-000000000099", QualificationDigest: repeatedDigest('6'), QualificationCorpusDigest: repeatedDigest('7'), ModelProfileDigest: repeatedDigest('2'), DecisionRoute: kernel.RouteBoundedExecution, QualifiedRole: "programmer", Status: kernel.QualificationPass, ObservedAt: time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)}}},
		Execution:    ProductionExecution{ConsumerID: "tekrood", OperationTimeout: "130s", MaximumBriefBytes: 1 << 20, PolicyRevision: 1},
		Evidence:     ProductionEvidence{PolicyRevision: 1, ProducingVersion: "phase5", RetentionPolicy: "local-operational"},
		Worker:       ProductionWorker{LeaseDuration: "150s", ReconciliationInterval: "1s", MaximumReconciliations: 600, MaximumConcurrentInvocations: 4, LeaseOperationTimeout: "5s"},
		Projection:   ProductionProjection{Interval: "100ms", OperationTimeout: "5s"},
		Organization: organizationConfig,
		Planning:     ProductionPlanning{PolicyRevision: 1, ClassificationPolicyDigest: repeatedDigest('8'), PromotionPolicyDigest: repeatedDigest('9'), VerificationTopologyDigest: repeatedDigest('a'), SelectionPolicyDigest: repeatedDigest('b'), BudgetPolicyDigest: repeatedDigest('c'), RequiredGateIDs: []string{"go-test"}, Deadline: "2h"},
	}
	path := filepath.Join(directory, "tekrood.json")
	writeJSON(t, path, config, 0o600)
	return path, config
}

func writeOrganizationFixture(t *testing.T, directory string) ProductionOrganization {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bundle := organization.RoleBundle{
		SchemaVersion: organization.RoleBundleSchemaVersion, Role: "coder", Version: "1.0.0",
		Capabilities: []string{"implement"}, Subscriptions: []organization.Subscription{{Type: "tekroo.message.task.assigned", Purpose: "implementation"}},
		Permissions: []string{"repository.read"}, Instructions: "Implement bounded tasks and return evidence.",
		Handlers: map[string]string{"tekroo.message.task.assigned": "implement"}, PublisherKeyID: "fixture-publisher",
	}
	bundleDigest, err := bundle.ContentDigest()
	if err != nil {
		t.Fatal(err)
	}
	digestBytes, err := hex.DecodeString(string(bundleDigest))
	if err != nil {
		t.Fatal(err)
	}
	bundle.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, digestBytes))
	if err := os.Mkdir(filepath.Join(directory, "roles"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(directory, "roles", "coder.json"), bundle, 0o600)
	manifest := organization.TeamManifest{
		SchemaVersion: organization.TeamManifestSchemaVersion, Team: "fixture", Version: "1.0.0",
		Roles: []organization.RoleBinding{{
			Role: "coder", BundlePath: "roles/coder.json", BundleDigest: bundleDigest, PublisherKeyID: "fixture-publisher",
			InitialInstances: 1, MaximumInstances: 1, LaunchMode: organization.LaunchEager,
			ModelProfileDigest: repeatedDigest('2'), WorkspaceIDs: []string{"workspace-1"},
		}},
	}
	manifestPath := filepath.Join(directory, "team.json")
	writeJSON(t, manifestPath, manifest, 0o600)
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifestHash := sha256.Sum256(raw)
	writeText(t, filepath.Join(directory, "role-publisher.pub"), base64.StdEncoding.EncodeToString(publicKey)+"\n", 0o644)
	return ProductionOrganization{
		ManifestFile: "team.json", ManifestDigest: kernel.Digest(hex.EncodeToString(manifestHash[:])),
		Publishers:             []ProductionPublisher{{KeyID: "fixture-publisher", PublicKeyFile: "role-publisher.pub"}},
		ReconciliationInterval: "100ms", MaximumRestarts: 3, MaximumDeliveryAttempts: 3,
	}
}

func writeJSON(t *testing.T, path string, value any, mode os.FileMode) {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), mode); err != nil {
		t.Fatal(err)
	}
}

func writeText(t *testing.T, path, value string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), mode); err != nil {
		t.Fatal(err)
	}
}

func repeatedDigest(value byte) kernel.Digest {
	buffer := make([]byte, 64)
	for index := range buffer {
		buffer[index] = value
	}
	return kernel.Digest(buffer)
}
