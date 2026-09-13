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
	"github.com/tekroo-ai/teams/adapters/openhands"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestProductionProfileSeparatesConfigurationFromQualification(t *testing.T) {
	path, config := writeProductionFixture(t)
	config.Profiles[0].Qualification = nil
	config.Profiles[0].QualificationCorpus = nil
	writeJSON(t, path, config, 0o600)

	loaded, err := LoadProductionConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Profiles) != 1 || loaded.Profiles[0].Qualification != nil {
		t.Fatalf("loaded profiles = %#v", loaded.Profiles)
	}
	if loaded.Profiles[0].qualifiedFor(kernel.RouteBoundedExecution, kernel.WorkImplementation, time.Now().UTC()) {
		t.Fatal("unqualified profile became eligible for execution")
	}

	task := &trackedTask{plan: organization.PlannedTask{DecisionRoute: kernel.RouteBoundedExecution}}
	err = (&ProductionService{}).activateTask(context.Background(), organization.FeatureRequest{}, task, loaded.Profiles[0], ProductionWorkspace{}, 0, nil, nil, "", nil)
	if !errors.Is(err, organization.ErrInvalidFeature) {
		t.Fatalf("unqualified activation error = %v", err)
	}
}

func TestFeatureRecoveryOperationContextUsesPlanningDeadline(t *testing.T) {
	service := &ProductionService{recoveryTimeout: 5 * time.Second, planningDeadline: 20 * time.Minute}
	ctx, cancel := service.featureRecoveryOperationContext(context.Background())
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("feature recovery context has no deadline")
	}
	remaining := time.Until(deadline)
	if remaining < 19*time.Minute || remaining > 20*time.Minute {
		t.Fatalf("feature recovery deadline remaining = %v, want planning deadline near 20m", remaining)
	}
	if remaining <= service.recoveryTimeout {
		t.Fatalf("feature recovery inherited lease-operation timeout: remaining=%v lease=%v", remaining, service.recoveryTimeout)
	}
}

func TestLoadProductionConfigRejectsQualificationForDifferentProfileRoute(t *testing.T) {
	path, config := writeProductionFixture(t)
	config.Profiles[0].DecisionRoute = kernel.RouteComplexReasoning
	writeJSON(t, path, config, 0o600)

	if _, err := LoadProductionConfig(path); err == nil {
		t.Fatal("qualification for a different configured route was accepted")
	}
}

func TestLoadProductionConfigRejectsUnregisteredOrTamperedQualificationCorpus(t *testing.T) {
	path, config := writeProductionFixture(t)
	config.Profiles[0].QualificationCorpus.ScenarioIDs = append(config.Profiles[0].QualificationCorpus.ScenarioIDs, "unregistered-scenario")
	writeJSON(t, path, config, 0o600)

	if _, err := LoadProductionConfig(path); !errors.Is(err, ErrInvalidProductionConfiguration) {
		t.Fatalf("tampered qualification corpus error = %v", err)
	}
}

func TestLoadProductionConfigRejectsQualificationForDifferentToolSurface(t *testing.T) {
	path, config := writeProductionFixture(t)
	profile := &config.Profiles[0]
	profile.QualificationCorpus.ToolSurfaceDigest = repeatedDigest('6')
	corpusDigest, err := profile.QualificationCorpus.Digest()
	if err != nil {
		t.Fatal(err)
	}
	profile.Qualification.QualificationCorpusDigest = corpusDigest
	profile.Qualification.QualificationDigest, err = application.QualificationDigest(*profile.Qualification)
	if err != nil {
		t.Fatal(err)
	}
	writeJSON(t, path, config, 0o600)

	if _, err := LoadProductionConfig(path); !errors.Is(err, ErrInvalidProductionConfiguration) {
		t.Fatalf("qualification for a different tool surface error = %v", err)
	}
}

func TestNewProductionServiceRejectsMissingOperationalQualificationBeforeSideEffects(t *testing.T) {
	path, config := writeProductionFixture(t)
	config.Profiles[0].Qualification = nil
	config.Profiles[0].QualificationCorpus = nil
	writeJSON(t, path, config, 0o600)
	loaded, err := LoadProductionConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := NewProductionService(context.Background(), loaded); !errors.Is(err, ErrInvalidProductionConfiguration) || !strings.Contains(err.Error(), "no execution profile") {
		t.Fatalf("unqualified service start error = %v", err)
	}
	if _, err := os.Stat(loaded.EvidenceRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unqualified start created evidence root: %v", err)
	}
}

func TestValidateOperationalProfileQualificationsDoesNotAssumeSoftwareRoles(t *testing.T) {
	at := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	qualified := func(role kernel.RoleFQRN, kinds ...kernel.WorkKind) ProductionProfile {
		profile := ProductionProfile{
			ModelProfileDigest: repeatedDigest(byte('1' + len(role)%8)),
			RoleFQRN:           role, DecisionRoute: kernel.RouteBoundedExecution,
			ToolPolicyDigest: repeatedDigest(byte('a' + len(role)%6)),
		}
		profile.QualificationCorpus, profile.Qualification = testQualificationBundle(t, profile.ModelProfileDigest, role, profile.DecisionRoute, profile.ToolPolicyDigest, kinds, at.Add(-time.Minute))
		return profile
	}
	profiles := []ProductionProfile{
		qualified("product-owner", kernel.WorkDesign, kernel.WorkRelease),
		qualified("project-manager", kernel.WorkDesign),
		qualified("architect", kernel.WorkDesign),
		qualified("coder", kernel.WorkImplementation),
		qualified("tester", kernel.WorkValidation),
		{RoleFQRN: "senior-coder", DecisionRoute: kernel.RouteBoundedExecution},
		{RoleFQRN: "security", DecisionRoute: kernel.RouteComplexReasoning},
	}
	if err := ValidateOperationalProfileQualifications(profiles, at); err != nil {
		t.Fatalf("qualified profiles rejected: %v", err)
	}
	profiles[3].Qualification = nil
	profiles[3].QualificationCorpus = nil
	if err := ValidateOperationalProfileQualifications(profiles, at); err != nil {
		t.Fatalf("non-software qualified profiles were rejected after removing coder qualification: %v", err)
	}
	for index := range profiles {
		profiles[index].Qualification = nil
		profiles[index].QualificationCorpus = nil
	}
	if err := ValidateOperationalProfileQualifications(profiles, at); !errors.Is(err, ErrInvalidProductionConfiguration) || !strings.Contains(err.Error(), "no execution profile") {
		t.Fatalf("missing all execution qualifications error = %v", err)
	}
}

func TestWorkflowTaskRoutingDerivesRolesFromQualifications(t *testing.T) {
	at := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	qualified := func(role kernel.RoleFQRN, route kernel.DecisionRoute, marker byte) ProductionProfile {
		profile := ProductionProfile{ModelProfileDigest: repeatedDigest(marker), RoleFQRN: role, DecisionRoute: route, ToolPolicyDigest: repeatedDigest(marker + 1)}
		profile.QualificationCorpus, profile.Qualification = testQualificationBundle(t, profile.ModelProfileDigest, role, route, profile.ToolPolicyDigest, []kernel.WorkKind{kernel.WorkImplementation}, at.Add(-time.Minute))
		return profile
	}
	routine := qualified("draft-writer", kernel.RouteBoundedExecution, '1')
	complex := qualified("investigative-writer", kernel.RouteComplexReasoning, '3')
	service := &ProductionService{profilesByModel: map[kernel.Digest]ProductionProfile{routine.ModelProfileDigest: routine, complex.ModelProfileDigest: complex}}
	policy, err := service.workflowTaskRoutingPolicy(at)
	if err != nil {
		t.Fatal(err)
	}
	bands := policy.PurposeRoutes[0].PlanComplexityRoleBands
	if len(bands) != 2 || bands[0].Role != "draft-writer" || bands[0].MinimumPlanComplexity != 1 || bands[1].Role != "investigative-writer" || bands[1].MinimumPlanComplexity != 5 {
		t.Fatalf("derived routing bands = %#v", bands)
	}
}

func TestProductionProfileQualificationIsWorkSpecificAndTimeBound(t *testing.T) {
	_, config := writeProductionFixture(t)
	profile := config.Profiles[0]
	observedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	profile.QualificationCorpus, profile.Qualification = testQualificationBundle(t, profile.ModelProfileDigest, profile.RoleFQRN, profile.DecisionRoute, profile.ToolPolicyDigest, []kernel.WorkKind{kernel.WorkImplementation}, observedAt)

	if !profile.qualifiedFor(kernel.RouteBoundedExecution, kernel.WorkImplementation, observedAt.Add(time.Minute)) {
		t.Fatal("exact qualified work became ineligible")
	}
	if profile.qualifiedFor(kernel.RouteBoundedExecution, kernel.WorkDesign, observedAt.Add(time.Minute)) {
		t.Fatal("qualification escaped its registered work kind")
	}
	service := &ProductionService{clock: fixedClock{now: observedAt.Add(time.Minute)}}
	task := &trackedTask{plan: organization.PlannedTask{DecisionRoute: kernel.RouteBoundedExecution, Purpose: kernel.PurposeReplan, Risk: organization.RiskModerate}}
	if err := service.activateTask(context.Background(), organization.FeatureRequest{}, task, profile, ProductionWorkspace{}, 0, nil, nil, "", nil); !errors.Is(err, organization.ErrInvalidFeature) {
		t.Fatalf("wrong-work activation error = %v", err)
	}
	expiresAt := observedAt.Add(2 * time.Minute)
	profile.Qualification.ExpiresAt = &expiresAt
	var err error
	profile.Qualification.QualificationDigest, err = application.QualificationDigest(*profile.Qualification)
	if err != nil {
		t.Fatal(err)
	}
	if profile.qualifiedFor(kernel.RouteBoundedExecution, kernel.WorkImplementation, expiresAt) {
		t.Fatal("expired qualification remained eligible")
	}
}

func TestTaskInvocationRequiresOneFullTransportTimeoutOfRunway(t *testing.T) {
	_, config := writeProductionFixture(t)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	profile := config.Profiles[0]
	profile.QualificationCorpus, profile.Qualification = testQualificationBundle(t, profile.ModelProfileDigest, profile.RoleFQRN, profile.DecisionRoute, profile.ToolPolicyDigest, []kernel.WorkKind{kernel.WorkImplementation}, now)
	task := &trackedTask{
		plan:    organization.PlannedTask{DecisionRoute: kernel.RouteBoundedExecution, Purpose: kernel.PurposeImplementation, Risk: organization.RiskLow},
		profile: kernel.WorkRiskProfile{Budgets: kernel.FiniteWorkBudgets{DeadlineAt: now.Add(10 * time.Minute)}},
	}
	service := &ProductionService{clock: fixedClock{now: now}, requestTimeout: 10 * time.Minute}
	err := service.authorizeTaskInvocationWithConditionPolicy(context.Background(), organization.FeatureRequest{}, task, profile, ProductionWorkspace{}, 0, kernel.PurposeImplementation, 1, nil, nil, false, false)
	if !errors.Is(err, ErrInsufficientExecutionRunway) {
		t.Fatalf("authorization error=%v want=%v", err, ErrInsufficientExecutionRunway)
	}
}

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

func TestLoadProductionConfigLoadsVersionedWorkflowDefinitions(t *testing.T) {
	path, config := writeProductionFixture(t)
	definition := kernel.WorkflowDefinition{
		SchemaVersion: kernel.WorkflowDefinitionSchemaVersion, Name: "publication", Version: "1.0.0", TriggerTypes: []string{"publication.requested"},
		Stages:      []kernel.WorkflowStageDefinition{{StageID: "publish", DependsOn: []string{}, InputSchema: "request/v1", OutputSchema: "publication/v1", RequiredCapabilities: []string{"publish"}, PreferredFQRNs: []kernel.RoleFQRN{}, Purpose: kernel.WorkflowPurposeImplementation, Risk: kernel.WorkflowRiskLow, ConcurrencyGroup: "publication", MaximumParallelism: 1, AttemptLimit: 1, AllowedOutgoingPurposes: []string{}, TargetSelection: kernel.WorkflowTargetCapability, ValidationPolicy: kernel.WorkflowValidationDeterministic}},
		RootBudgets: kernel.WorkflowBudgetLimits{MaximumModelInvocations: 1, MaximumHops: 2, MaximumAttempts: 1}, ProjectionRules: []string{},
	}
	digest, err := definition.CalculatedDigest()
	if err != nil {
		t.Fatal(err)
	}
	definition.ContentDigest = digest
	definitionPath := filepath.Join(filepath.Dir(path), "publication.workflow.json")
	writeJSON(t, definitionPath, definition, 0o600)
	config.Organization.WorkflowDefinitions = []ProductionWorkflowSource{{DefinitionFile: "publication.workflow.json", DefinitionDigest: digest}}
	writeJSON(t, path, config, 0o600)
	loaded, err := LoadProductionConfig(path)
	if err != nil || !filepath.IsAbs(loaded.Organization.WorkflowDefinitions[0].DefinitionFile) {
		t.Fatalf("workflow config=%+v err=%v", loaded.Organization.WorkflowDefinitions, err)
	}
	resolved, err := resolveProductionConfig(loaded)
	if err != nil || resolved.workflowLibrary == nil {
		t.Fatalf("workflow library missing: %v", err)
	}
	if _, err := resolved.workflowLibrary.Lookup("publication", "1.0.0"); err != nil {
		t.Fatalf("workflow lookup: %v", err)
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

func TestLoadProductionConfigValidatesRuntimeContinuityCadence(t *testing.T) {
	path, config := writeProductionFixture(t)
	config.Continuity = &ProductionContinuity{HeartbeatInterval: "5s", SuspensionThreshold: "10s"}
	writeJSON(t, path, config, 0o600)
	if _, err := LoadProductionConfig(path); err != nil {
		t.Fatalf("valid continuity cadence: %v", err)
	}
	config.Continuity.SuspensionThreshold = "9s"
	writeJSON(t, path, config, 0o600)
	if _, err := LoadProductionConfig(path); !errors.Is(err, ErrInvalidProductionConfiguration) {
		t.Fatalf("unsafe continuity cadence error = %v", err)
	}
}

func TestLoadProductionConfigRejectsMissingOrInvalidCandidateGate(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*ProductionConfig)
	}{
		{name: "missing", mutate: func(config *ProductionConfig) { config.Planning.CandidateGates = nil }},
		{name: "blank argument", mutate: func(config *ProductionConfig) { config.Planning.CandidateGates[0].Command = []string{"go", ""} }},
		{name: "duplicate required identity", mutate: func(config *ProductionConfig) { config.Planning.RequiredGateIDs = []string{"go-test", "go-test"} }},
		{name: "longer than planning deadline", mutate: func(config *ProductionConfig) { config.Planning.CandidateGates[0].Timeout = "3h" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			path, config := writeProductionFixture(t)
			test.mutate(&config)
			writeJSON(t, path, config, 0o600)
			if _, err := LoadProductionConfig(path); !errors.Is(err, ErrInvalidProductionConfiguration) {
				t.Fatalf("candidate gate error = %v", err)
			}
		})
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
	agentSettings, err := openhands.NewOpenAICompatibleAgentSettings(openhands.AgentSettingsConfig{
		Model: "openai/local-fixture", ModelCanonicalName: "openai/gpt-4o", BaseURL: "http://127.0.0.1:8802/v1", APIKey: "fixture",
		Tools:               openhands.ExecutionToolsForPermissions([]string{"repository.edit"}),
		MaximumOutputTokens: 8192, CondenserOutputTokens: 4096, TimeoutSeconds: 1200, CondenserMaximumEvents: 80, CondenserMaximumTokens: 96000,
	})
	if err != nil {
		t.Fatal(err)
	}
	organizationConfig, modelDigest, bundleDigest := writeOrganizationFixture(t, directory, agentSettings)
	qualificationCorpus, qualification := testQualificationBundle(t, modelDigest, "coder", kernel.RouteBoundedExecution, repeatedDigest('4'), allTestWorkKinds(), time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC))
	config := ProductionConfig{
		ContractRoot:          root,
		Mongo:                 ProductionMongoConfig{URIFile: "mongo-uri", Database: "tekroo_v4", BacklogLimit: 1024, DeliveryPolicyRevision: 1},
		OpenHands:             ProductionOpenHandsConfig{BaseURL: "http://127.0.0.1:8000", SessionAPIKeyFile: "openhands-key", RequestTimeout: "130s", PollInterval: "250ms", MaximumPages: 64, MaximumEvidenceBytes: 16 << 20},
		Operator:              ProductionOperatorConfig{Address: "127.0.0.1:8787", BearerTokenFile: "operator-token", Principal: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "operator"}, OperationTimeout: "10s", MaximumBodyBytes: 1 << 20},
		TeamsDatabaseIdentity: "tekroo_v4", SMADatabaseIdentity: "sma_v4", DeploymentIdentity: repeatedDigest('1'), AuthorizationPolicyFile: "authorization.json", ProvenanceFile: "provenance.json", EvidenceRoot: "evidence",
		ServiceAuthority: kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "teams-operational-runtime"}, ExpiryAuthority: kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "teams-admission-policy"},
		Workspaces:   []ProductionWorkspace{{WorkspaceID: "workspace-1", WorktreeID: "worktree-1", WorkingDirectory: "workspace", Branch: "task/workspace-1", BaselineSHA: strings.Repeat("1", 40), WritablePaths: []string{"."}}},
		Profiles:     []ProductionProfile{{ModelProfileDigest: modelDigest, RoleFQRN: "coder", RoleBundleDigest: bundleDigest, DecisionRoute: kernel.RouteBoundedExecution, RuntimeIdentityDigest: repeatedDigest('3'), ToolPolicyDigest: repeatedDigest('4'), EffectPolicyDigest: repeatedDigest('5'), AgentSettings: agentSettings, MaximumIterations: 24, QualificationCorpus: qualificationCorpus, Qualification: qualification}},
		Execution:    ProductionExecution{ConsumerID: "tekrood", OperationTimeout: "130s", MaximumBriefBytes: 1 << 20, PolicyRevision: 1},
		Evidence:     ProductionEvidence{PolicyRevision: 1, ProducingVersion: "phase5", RetentionPolicy: "local-operational"},
		Worker:       ProductionWorker{LeaseDuration: "150s", ReconciliationInterval: "1s", MaximumReconciliations: 600, MaximumConcurrentInvocations: 4, LeaseOperationTimeout: "5s"},
		Projection:   ProductionProjection{Interval: "100ms", OperationTimeout: "5s"},
		Organization: organizationConfig,
		Planning:     ProductionPlanning{PolicyRevision: 1, ClassificationPolicyDigest: repeatedDigest('8'), PromotionPolicyDigest: repeatedDigest('9'), VerificationTopologyDigest: repeatedDigest('a'), SelectionPolicyDigest: repeatedDigest('b'), BudgetPolicyDigest: repeatedDigest('c'), RequiredGateIDs: []string{"go-test"}, CandidateGates: []ProductionCandidateGate{{GateID: "go-test", Command: []string{"go", "test", "./..."}, Timeout: "30s"}}, Deadline: "2h"},
	}
	path := filepath.Join(directory, "tekrood.json")
	writeJSON(t, path, config, 0o600)
	return path, config
}

func writeOrganizationFixture(t *testing.T, directory string, agentSettings json.RawMessage) (ProductionOrganization, kernel.Digest, kernel.Digest) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bundle := organization.RoleBundle{
		SchemaVersion: organization.RoleBundleSchemaVersion, Role: "coder", Version: "1.0.0",
		Capabilities: []string{"implement"}, Subscriptions: []organization.Subscription{{Type: "tekroo.message.task.assigned", Purpose: "implementation"}},
		Permissions: []string{"repository.edit"}, Instructions: "Implement bounded tasks and return evidence.",
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
	modelDigest, err := openhands.ModelProfileDigest("coder", bundleDigest, agentSettings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(directory, "roles"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(directory, "roles", "coder.json"), bundle, 0o600)
	manifest := organization.TeamManifest{
		SchemaVersion: organization.TeamManifestSchemaVersion, Team: "fixture", Version: "1.0.0",
		Roles: []organization.RoleBinding{{
			Role: "coder", BundlePath: "roles/coder.json", BundleDigest: bundleDigest, PublisherKeyID: "fixture-publisher",
			InitialInstances: 1, MaximumInstances: 1, LaunchMode: organization.LaunchEager,
			ModelProfileDigest: modelDigest, WorkspaceIDs: []string{"workspace-1"},
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
	}, modelDigest, bundleDigest
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
