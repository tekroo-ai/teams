//go:build mongo_integration

package mongo

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/fake"
	"github.com/tekroo-ai/teams/adapters/gitprovider"
	"github.com/tekroo-ai/teams/adapters/httpapi"
	"github.com/tekroo-ai/teams/adapters/protocol"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/contract"
	"github.com/tekroo-ai/teams/kernel"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestIsolatedCapstonePersistsExactReleaseBeforeStoryAcceptance(t *testing.T) {
	fixture := newCapstoneGitFixture(t)
	database := nextDatabase(t)
	config := testConfig(testMongoURI, database)
	config.Policy = capstonePolicy()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	store, err := Open(ctx, config)
	cancel()
	if err != nil {
		t.Fatal(err)
	}

	story := kernel.AggregateRef{Kind: kernel.AggregateStory, ID: testUUID(0x101)}
	approval := seedCapstoneCompletedStory(t, store, story)
	evidence := seedCapstoneEvidence(t, store)
	catalogue := loadCapstoneCatalogue(t)
	basis, err := fake.ProvenanceBasis()
	if err != nil {
		t.Fatal(err)
	}
	clock := fake.NewClock(time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC))
	ids := make([]kernel.UUIDv7, 0, 14)
	for ordinal := 0x9001; ordinal <= 0x900e; ordinal++ {
		ids = append(ids, testUUID(ordinal))
	}
	handler, err := application.NewHandler(store, kernel.Evaluator{Catalogue: catalogue}, clock, fake.NewIDSource(ids...))
	if err != nil {
		t.Fatal(err)
	}

	policyPrincipal := kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "release-policy"}
	servicePrincipal := kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "local-git-provider"}
	author := kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal-author"}
	planRef := kernel.AggregateRef{Kind: kernel.AggregateReleasePlan, ID: testUUID(0x751)}
	mergeID := testUUID(0x752)
	planDigest := kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	manifestDigest := kernel.Digest("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	repositoryURL := (&url.URL{Scheme: "file", Path: fixture.bare}).String()
	createPayload := mustCapstoneJSON(t, map[string]any{
		"author": author, "author_approval_event_id": approval, "author_approval_revision": uint64(1),
		"base_commit": fixture.base, "base_ref": "main", "conflict_policy": "FAIL_NO_IMPROVISATION", "contract_manifest": kernel.ContractIdentity,
		"evidence_ids": []kernel.UUIDv7{evidence.primary.EvidenceID}, "execution_round_limit": uint64(2), "expected_qualified_tree": fixture.tree,
		"expected_story_revision": uint64(1), "git_version": fixture.version, "manifest_sha256": manifestDigest,
		"merge_strategy": "FF_ONLY_ORDERED", "ordered_merges": []kernel.ReleaseMergePlan{{MergeID: mergeID, ChangeRef: "refs/heads/story-1", HeadCommit: fixture.head, Role: "story"}},
		"plan_digest": planDigest, "release_mode": kernel.ReleaseModeCode, "release_plan_id": planRef.ID, "release_policy_revision": uint64(1),
		"repository_url": repositoryURL, "required_profiles": []string{"contract-structure", "core-hermetic", "mongo-integration", "synthesized-merge"},
		"story_id": story.ID, "story_lifecycle_epoch": uint64(1),
	})
	create := capstoneCommand(testUUID(0x8101), "tekroo.command.release-plan.create", planRef, policyPrincipal, kernel.MustNotExist(),
		[]kernel.AggregatePrecondition{{Aggregate: story, Expected: kernel.NewExpectedRevision(1)}},
		[]kernel.DagParent{{ParentEventID: approval, EdgeKind: kernel.EdgeResponse}}, createPayload, []kernel.EvidenceRef{evidence.primary})
	created, err := handleCapstoneCommand(t, handler, create, basis)
	if err != nil || created.OutcomeCode != kernel.OutcomeApplied || created.ResultingRevision == nil || *created.ResultingRevision != 1 {
		t.Fatalf("create receipt=%#v err=%v", created, err)
	}

	qualificationPayload := mustCapstoneJSON(t, map[string]any{
		"artifact_digests":  []kernel.Digest{kernel.Digest("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")},
		"contract_manifest": kernel.ContractIdentity, "dependency_lock_digest": kernel.Digest("eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"),
		"evidence_ids": []kernel.UUIDv7{evidence.primary.EvidenceID}, "expected_release_revision": uint64(1),
		"gate_definition_digest": kernel.Digest("cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"), "manifest_sha256": manifestDigest,
		"ordered_head_commits": []string{fixture.head}, "plan_digest": planDigest, "qualification_id": testUUID(0x754),
		"qualified_base_commit": fixture.base, "qualified_tree_digest": fixture.tree, "release_plan_id": planRef.ID,
		"required_profiles": []string{"contract-structure", "core-hermetic", "mongo-integration", "synthesized-merge"},
		"toolchain_digest":  kernel.Digest("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"),
	})
	qualification := capstoneCommand(testUUID(0x8102), "tekroo.command.release-plan.record-qualification", planRef, servicePrincipal, kernel.NewExpectedRevision(1), nil,
		[]kernel.DagParent{{ParentEventID: created.EventIDs[0], EdgeKind: kernel.EdgeResponse}}, qualificationPayload, []kernel.EvidenceRef{evidence.primary})
	qualifiedReceipt, err := handleCapstoneCommand(t, handler, qualification, basis)
	if err != nil || qualifiedReceipt.OutcomeCode != kernel.OutcomeApplied || qualifiedReceipt.ResultingRevision == nil || *qualifiedReceipt.ResultingRevision != 2 {
		t.Fatalf("qualification receipt=%#v err=%v", qualifiedReceipt, err)
	}

	qualified := loadMongoRelease(t, store, planRef).ReleasePlans[planRef]
	innerProvider, err := gitprovider.New(gitprovider.Config{AllowedRoot: fixture.root, OperationTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	provider := &capstoneObservingProvider{store: store, inner: innerProvider}
	coordinator, err := application.NewReleaseCoordinator(handler, provider, application.ReleaseCoordinatorPolicy{OperationTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	operation := application.ReleaseExecutionPlan{
		Plan: qualified, AttemptID: testUUID(0x755), ProviderKey: "release-751-merge-752-round-1",
		Request:           application.ReleaseCommandTemplate{CommandID: testUUID(0x8103), Authority: policyPrincipal, ExpectedPolicyRevision: 1, IdempotencyKey: "request-release-751-752-1", CorrelationID: testUUID(0x8203), EvidenceRefs: []kernel.EvidenceRef{evidence.primary}},
		RequestProvenance: basis,
		Result:            application.ReleaseCommandTemplate{CommandID: testUUID(0x8104), Authority: servicePrincipal, ExpectedPolicyRevision: 1, IdempotencyKey: "result-release-751-752-1", CorrelationID: testUUID(0x8204), EvidenceRefs: []kernel.EvidenceRef{evidence.provider}},
		ResultProvenance:  basis, ObservedAt: clock.Now(),
	}
	coordinationContext, coordinationCancel := context.WithTimeout(context.Background(), 5*time.Second)
	coordination, err := coordinator.ExecuteNext(coordinationContext, operation)
	coordinationCancel()
	if err != nil || coordination.ResultReceipt == nil || coordination.ResultReceipt.OutcomeCode != kernel.OutcomeApplied || coordination.Observation.Outcome != kernel.ReleaseOutcomeMerged || !provider.observedPersistedIntent {
		t.Fatalf("coordination=%#v persistedIntent=%t err=%v", coordination, provider.observedPersistedIntent, err)
	}
	if current := runCapstoneGit(t, "--git-dir", fixture.bare, "rev-parse", "refs/heads/main"); current != fixture.head {
		t.Fatalf("provider main=%s, want %s", current, fixture.head)
	}

	merged := loadMongoRelease(t, store, planRef).ReleasePlans[planRef]
	result := merged.Results[mergeID]
	prematurePayload := capstoneAcceptancePayload(t, merged, result.ResultEventID, fixture.tree)
	premature := capstoneCommand(testUUID(0x8105), "tekroo.command.story.request-acceptance", story, author, kernel.NewExpectedRevision(1),
		[]kernel.AggregatePrecondition{{Aggregate: planRef, Expected: kernel.NewExpectedRevision(merged.Revision)}},
		[]kernel.DagParent{{ParentEventID: result.ResultEventID, EdgeKind: kernel.EdgeResponse}}, prematurePayload, []kernel.EvidenceRef{evidence.primary})
	premature.ExpectedLifecycleEpoch = capstoneUint64(1)
	prematureReceipt := invokeCapstoneHTTP(t, handler, author, premature, basis)
	if prematureReceipt.OutcomeCode != kernel.OutcomeRejectedPolicy || prematureReceipt.ResultingRevision != nil {
		t.Fatalf("premature acceptance receipt=%#v", prematureReceipt)
	}

	finalPayload := mustCapstoneJSON(t, map[string]any{
		"evidence_ids": []kernel.UUIDv7{evidence.primary.EvidenceID}, "expected_release_revision": merged.Revision,
		"plan_digest": planDigest, "provider_tree_digest": fixture.tree, "qualification_event_id": merged.Qualification.EventID,
		"qualified_tree_digest": fixture.tree, "reasons": []string{"exact planned head and tree verified"}, "release_mode": kernel.ReleaseModeCode,
		"release_plan_id": planRef.ID, "result_event_ids": []kernel.UUIDv7{result.ResultEventID}, "terminal_status": kernel.ReleaseReadyForAcceptance,
	})
	finalize := capstoneCommand(testUUID(0x8106), "tekroo.command.release-plan.finalize", planRef, policyPrincipal, kernel.NewExpectedRevision(merged.Revision), nil,
		[]kernel.DagParent{{ParentEventID: result.ResultEventID, EdgeKind: kernel.EdgeResponse}}, finalPayload, []kernel.EvidenceRef{evidence.primary})
	finalizedReceipt, err := handleCapstoneCommand(t, handler, finalize, basis)
	if err != nil || finalizedReceipt.OutcomeCode != kernel.OutcomeApplied {
		t.Fatalf("finalization receipt=%#v err=%v", finalizedReceipt, err)
	}

	final := loadMongoRelease(t, store, planRef).ReleasePlans[planRef]
	acceptancePayload := capstoneAcceptancePayload(t, final, final.FinalizationEventID, fixture.tree)
	acceptance := capstoneCommand(testUUID(0x8107), "tekroo.command.story.request-acceptance", story, author, kernel.NewExpectedRevision(1),
		[]kernel.AggregatePrecondition{{Aggregate: planRef, Expected: kernel.NewExpectedRevision(final.Revision)}},
		[]kernel.DagParent{{ParentEventID: final.FinalizationEventID, EdgeKind: kernel.EdgeResponse}}, acceptancePayload, []kernel.EvidenceRef{evidence.primary})
	acceptance.ExpectedLifecycleEpoch = capstoneUint64(1)
	accepted := invokeCapstoneHTTP(t, handler, author, acceptance, basis)
	replayed := invokeCapstoneHTTP(t, handler, author, acceptance, basis)
	if accepted.OutcomeCode != kernel.OutcomeApplied || replayed.OutcomeCode != kernel.OutcomeApplied || len(accepted.EventIDs) != 1 || len(replayed.EventIDs) != 1 || accepted.EventIDs[0] != replayed.EventIDs[0] {
		t.Fatalf("acceptance receipts=%#v %#v", accepted, replayed)
	}

	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	if err := store.Close(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	ctx, cancel = context.WithTimeout(context.Background(), 15*time.Second)
	reopened, err := Open(ctx, config)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = reopened.db.Drop(ctx)
		_ = reopened.Close(ctx)
	})
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	storySnapshot, err := reopened.LoadDecision(ctx, kernel.KernelCommand{Target: story})
	cancel()
	if err != nil || storySnapshot.State == nil || storySnapshot.State.Phase != kernel.PhaseAccepted || storySnapshot.Revision != 2 {
		t.Fatalf("story after restart=%#v err=%v", storySnapshot.State, err)
	}
	terminal := storySnapshot.ReleasePlans[planRef]
	if terminal.State != kernel.ReleaseReadyForAcceptance || terminal.ProviderTreeDigest != fixture.tree || terminal.FinalizationEventID != finalizedReceipt.EventIDs[0] {
		t.Fatalf("release after restart=%#v", terminal)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	releaseEvents, err := reopened.db.Collection("events").CountDocuments(ctx, bson.D{{Key: "aggregate_key", Value: aggregateKey(planRef)}})
	cancel()
	if err != nil || releaseEvents != 5 {
		t.Fatalf("release events after replay=%d err=%v, want 5", releaseEvents, err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	storyEvents, err := reopened.db.Collection("events").CountDocuments(ctx, bson.D{{Key: "aggregate_key", Value: aggregateKey(story)}})
	cancel()
	if err != nil || storyEvents != 2 {
		t.Fatalf("story events after replay=%d err=%v, want 2", storyEvents, err)
	}
}

type capstoneEvidence struct{ primary, provider kernel.EvidenceRef }

func seedCapstoneEvidence(t *testing.T, store *Store) capstoneEvidence {
	t.Helper()
	value := capstoneEvidence{
		primary:  kernel.EvidenceRef{EvidenceID: testUUID(0x750), SHA256: kernel.Digest("1111111111111111111111111111111111111111111111111111111111111111")},
		provider: kernel.EvidenceRef{EvidenceID: testUUID(0x756), SHA256: kernel.Digest("2222222222222222222222222222222222222222222222222222222222222222")},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, item := range []kernel.EvidenceRef{value.primary, value.provider} {
		if _, err := store.db.Collection("evidence").InsertOne(ctx, evidenceDocument{ID: string(item.EvidenceID), SHA256: string(item.SHA256), Available: true}); err != nil {
			t.Fatal(err)
		}
	}
	return value
}

func seedCapstoneCompletedStory(t *testing.T, store *Store, story kernel.AggregateRef) kernel.UUIDv7 {
	t.Helper()
	decision := retargetDecision(t, completeDecision(t, 131), story.ID)
	decision.Authority.Principal = kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal-author"}
	decision.NextState.Phase = kernel.PhaseCompleted
	decision.Events[0].EventType = "tekroo.event.story.release-approved"
	decision.Events[0].Authority = decision.Authority.Principal
	decision.Events[0].Payload = mustCapstoneJSON(t, decision.NextState)
	command := commandForDecision(decision)
	var err error
	decision.CommandFingerprint, err = kernel.CommandFingerprint(command)
	if err != nil {
		t.Fatal(err)
	}
	decision.IdempotencyScope, err = kernel.IdempotencyScopeDigest(command)
	if err != nil {
		t.Fatal(err)
	}
	decision = attachProvenance(t, decision)
	commitDecision(t, store, decision)
	return decision.Events[0].EventID
}

func capstonePolicy() kernel.AuthorizationPolicy {
	return kernel.AuthorizationPolicy{
		PolicyDigest: kernel.Digest("9999999999999999999999999999999999999999999999999999999999999999"), Revision: 1,
		Requirements: kernel.DecisionPolicyRequirements{AcceptancePolicyRevision: 1, RequireQualifiedTree: true},
		Grants: []kernel.AuthorityGrant{
			{GrantDigest: kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), Grantee: kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "release-policy"}, Scope: kernel.AuthorityScope{CommandTypes: []string{"tekroo.command.release-plan.create", "tekroo.command.release-plan.request-execution", "tekroo.command.release-plan.finalize"}, TargetKinds: []kernel.AggregateKind{kernel.AggregateReleasePlan}, CanReadTarget: true}},
			{GrantDigest: kernel.Digest("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"), Grantee: kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "local-git-provider"}, Scope: kernel.AuthorityScope{CommandTypes: []string{"tekroo.command.release-plan.record-qualification", "tekroo.command.release-plan.record-result"}, TargetKinds: []kernel.AggregateKind{kernel.AggregateReleasePlan}, CanReadTarget: true}},
			{GrantDigest: kernel.Digest("cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"), Grantee: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal-author"}, Scope: kernel.AuthorityScope{CommandTypes: []string{"tekroo.command.story.request-acceptance"}, TargetKinds: []kernel.AggregateKind{kernel.AggregateStory}, CanReadTarget: true}},
		},
	}
}

func capstoneCommand(id kernel.UUIDv7, commandType string, target kernel.AggregateRef, authority kernel.PrincipalRef, expected kernel.ExpectedRevision, preconditions []kernel.AggregatePrecondition, causation []kernel.DagParent, payload json.RawMessage, evidence []kernel.EvidenceRef) kernel.KernelCommand {
	return kernel.KernelCommand{
		ContractManifest: kernel.ContractIdentity, CommandID: id, CommandType: commandType, CommandVersion: kernel.SchemaVersion,
		Target: target, Authority: authority, ExpectedRevision: expected, Preconditions: preconditions,
		ExpectedPolicyRevision: 1, ExpectedCatalogueRevision: kernel.CatalogueRevision,
		IdempotencyKey: "capstone-" + string(id), CorrelationID: id,
		Causation: causation, Payload: payload, EvidenceRefs: evidence,
	}
}

func capstoneAcceptancePayload(t *testing.T, plan kernel.ReleasePlanSnapshot, finalizedEvent kernel.UUIDv7, tree string) json.RawMessage {
	t.Helper()
	return mustCapstoneJSON(t, map[string]any{
		"acceptance_policy_revision": uint64(1), "evidence_ids": []kernel.UUIDv7{testUUID(0x750)}, "lifecycle_epoch": uint64(1),
		"qualified_tree_digest": tree, "release_finalized_event_id": finalizedEvent, "release_mode": kernel.ReleaseModeCode,
		"release_plan_id": plan.ReleasePlanID, "release_plan_revision": plan.Revision,
	})
}

func handleCapstoneCommand(t *testing.T, handler *application.Handler, command kernel.KernelCommand, basis kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return handler.Handle(ctx, command, basis)
}

func capstoneUint64(value uint64) *uint64 { return &value }

func invokeCapstoneHTTP(t *testing.T, service *application.Handler, authority kernel.PrincipalRef, command kernel.KernelCommand, basis kernel.ProvenanceBasis) kernel.CommandReceipt {
	t.Helper()
	gateway, err := protocol.NewGateway(service, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	identity := protocol.AuthenticatedContext{Principal: authority}
	handler, err := httpapi.NewHandler(gateway, httpapi.AuthenticatorFunc(func(*http.Request) (protocol.AuthenticatedContext, error) { return identity, nil }), httpapi.OriginPolicyFunc(func(string) bool { return true }), httpapi.RateLimiterFunc(func(protocol.AuthenticatedContext) bool { return true }), httpapi.DefaultMaxBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	invocation := protocol.Invocation{EnvelopeVersion: protocol.EnvelopeVersion, RequestID: command.CommandID, TimeoutMillis: 2500, Command: protocol.CommandFromKernel(command), Provenance: basis}
	body := mustCapstoneJSON(t, invocation)
	request := httptest.NewRequest(http.MethodPost, "/commands", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("HTTP status=%d body=%s", response.Code, response.Body.String())
	}
	var decoded protocol.Response
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil || decoded.Receipt == nil {
		t.Fatalf("HTTP response=%s err=%v", response.Body.String(), err)
	}
	return *decoded.Receipt
}

type capstoneObservingProvider struct {
	store                   *Store
	inner                   kernel.ReleaseProvider
	observedPersistedIntent bool
}

func (provider *capstoneObservingProvider) Merge(ctx context.Context, request kernel.ReleaseMergeRequest) (kernel.ReleaseProviderObservation, error) {
	snapshot, err := provider.store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateReleasePlan, ID: request.ReleasePlanID}})
	if err != nil {
		return kernel.ReleaseProviderObservation{}, err
	}
	plan := snapshot.ReleasePlans[kernel.AggregateRef{Kind: kernel.AggregateReleasePlan, ID: request.ReleasePlanID}]
	provider.observedPersistedIntent = plan.State == kernel.ReleaseExecuting && plan.ActiveAttempt != nil && plan.ActiveAttempt.AttemptID == request.AttemptID
	return provider.inner.Merge(ctx, request)
}

func (provider *capstoneObservingProvider) Reconcile(ctx context.Context, request kernel.ReleaseMergeRequest) (kernel.ReleaseProviderObservation, error) {
	return provider.inner.Reconcile(ctx, request)
}

type capstoneGitFixture struct{ root, bare, base, head, tree, version string }

func newCapstoneGitFixture(t *testing.T) capstoneGitFixture {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	bare := filepath.Join(root, "target.git")
	runCapstoneGit(t, "init", "-b", "main", source)
	runCapstoneGit(t, "-C", source, "config", "user.name", "Tekroo Capstone")
	runCapstoneGit(t, "-C", source, "config", "user.email", "tekroo@example.invalid")
	if err := os.WriteFile(filepath.Join(source, "artifact.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCapstoneGit(t, "-C", source, "add", "artifact.txt")
	runCapstoneGit(t, "-C", source, "commit", "-m", "base")
	base := runCapstoneGit(t, "-C", source, "rev-parse", "HEAD")
	runCapstoneGit(t, "-C", source, "switch", "-c", "story-1")
	if err := os.WriteFile(filepath.Join(source, "artifact.txt"), []byte("qualified\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCapstoneGit(t, "-C", source, "commit", "-am", "qualified story")
	head := runCapstoneGit(t, "-C", source, "rev-parse", "HEAD")
	tree := runCapstoneGit(t, "-C", source, "rev-parse", "HEAD^{tree}")
	runCapstoneGit(t, "clone", "--bare", source, bare)
	runCapstoneGit(t, "--git-dir", bare, "update-ref", "refs/heads/main", base)
	return capstoneGitFixture{root: root, bare: bare, base: base, head: head, tree: tree, version: runCapstoneGit(t, "--version")}
}

func runCapstoneGit(t *testing.T, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC", "GIT_TERMINAL_PROMPT=0", "GIT_NO_LAZY_FETCH=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func loadCapstoneCatalogue(t *testing.T) kernel.CatalogueSnapshot {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate capstone test")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	catalogue, err := contract.Load(os.DirFS(root), "CONTRACTS/tekroo.kernel.contracts/0.11.0")
	if err != nil {
		t.Fatal(err)
	}
	return catalogue
}

func mustCapstoneJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCapstoneFixtureUsesNoNetworkRepository(t *testing.T) {
	fixture := newCapstoneGitFixture(t)
	parsed, err := url.Parse((&url.URL{Scheme: "file", Path: fixture.bare}).String())
	if err != nil || parsed.Scheme != "file" || parsed.Host != "" {
		t.Fatalf("fixture URI=%v err=%v", parsed, err)
	}
	if _, err := os.Stat(fixture.bare); err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(fixture.bare, "http") || strings.Contains(fixture.bare, "github.com") {
		t.Fatalf("unexpected network fixture path %q", fixture.bare)
	}
}
