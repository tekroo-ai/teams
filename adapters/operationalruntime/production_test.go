package operationalruntime

import (
	"context"
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
	if observed.Mongo.Database != expected.Mongo.Database || observed.TeamsDatabaseIdentity != expected.TeamsDatabaseIdentity || observed.SMADatabaseIdentity != expected.SMADatabaseIdentity || len(observed.Workspaces) != 1 || !filepath.IsAbs(observed.Workspaces[0].WorkingDirectory) || !filepath.IsAbs(observed.OpenHands.SessionAPIKeyFile) {
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
			{GrantDigest: provenance.GrantDigests[0], Grantee: kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "teams-operational-runtime"}, Scope: kernel.AuthorityScope{CommandTypes: []string{"tekroo.command.execution.register", "tekroo.command.evidence.register", "tekroo.command.work-invocation.claim", "tekroo.command.work-invocation.record-started", "tekroo.command.work-invocation.record-terminal"}, TargetKinds: []kernel.AggregateKind{kernel.AggregateExecution, kernel.AggregateEvidence, kernel.AggregateWorkInvocation}, CanReadTarget: true}},
			{GrantDigest: repeatedDigest('b'), Grantee: kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "teams-admission-policy"}, Scope: kernel.AuthorityScope{CommandTypes: []string{"tekroo.command.work-invocation.expire"}, TargetKinds: []kernel.AggregateKind{kernel.AggregateWorkInvocation}, CanReadTarget: true}},
		},
	}
	writeJSON(t, filepath.Join(directory, "authorization.json"), policy, 0o600)
	writeJSON(t, filepath.Join(directory, "provenance.json"), provenance, 0o600)
	config := ProductionConfig{
		ContractRoot:          root,
		Mongo:                 ProductionMongoConfig{URIFile: "mongo-uri", Database: "tekroo_v4", BacklogLimit: 1024, DeliveryPolicyRevision: 1},
		OpenHands:             ProductionOpenHandsConfig{BaseURL: "http://127.0.0.1:8000", SessionAPIKeyFile: "openhands-key", RequestTimeout: "130s", PollInterval: "250ms", MaximumPages: 64, MaximumEvidenceBytes: 16 << 20},
		Operator:              ProductionOperatorConfig{Address: "127.0.0.1:8787", BearerTokenFile: "operator-token", OperationTimeout: "10s", MaximumBodyBytes: 1 << 20},
		TeamsDatabaseIdentity: "tekroo_v4", SMADatabaseIdentity: "sma_v4", DeploymentIdentity: repeatedDigest('1'), AuthorizationPolicyFile: "authorization.json", ProvenanceFile: "provenance.json", EvidenceRoot: "evidence",
		ServiceAuthority: kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "teams-operational-runtime"}, ExpiryAuthority: kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "teams-admission-policy"},
		Workspaces: []ProductionWorkspace{{WorkspaceID: "workspace-1", WorktreeID: "worktree-1", WorkingDirectory: "workspace"}},
		Profiles:   []ProductionProfile{{ModelProfileDigest: repeatedDigest('2'), RuntimeIdentityDigest: repeatedDigest('3'), ToolPolicyDigest: repeatedDigest('4'), EffectPolicyDigest: repeatedDigest('5'), MaximumIterations: 24}},
		Execution:  ProductionExecution{ConsumerID: "tekrood", OperationTimeout: "130s", MaximumBriefBytes: 1 << 20, PolicyRevision: 1},
		Evidence:   ProductionEvidence{PolicyRevision: 1, ProducingVersion: "phase5", RetentionPolicy: "local-operational"},
		Worker:     ProductionWorker{LeaseDuration: "150s", ReconciliationInterval: "1s", MaximumReconciliations: 600, MaximumConcurrentInvocations: 4, LeaseOperationTimeout: "5s"},
		Projection: ProductionProjection{Interval: "100ms", OperationTimeout: "5s"},
	}
	path := filepath.Join(directory, "tekrood.json")
	writeJSON(t, path, config, 0o600)
	return path, config
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
