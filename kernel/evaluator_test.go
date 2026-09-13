package kernel_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/contract"
	"github.com/tekroo-ai/teams/kernel"
)

func TestEvaluatorCreatesStoryDeterministically(t *testing.T) {
	evaluator := kernel.Evaluator{Catalogue: loadCatalogue(t)}
	command := validStoryCreateCommand(t)
	context := validDecisionContext(t)

	first := evaluate(t, evaluator, command, kernel.Snapshot{}, context)
	second := evaluate(t, evaluator, command, kernel.Snapshot{}, context)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("same command, snapshot, and context produced different decisions")
	}
	if first.Receipt.OutcomeCode != kernel.OutcomeApplied {
		t.Fatalf("outcome = %s, want APPLIED", first.Receipt.OutcomeCode)
	}
	if first.NextState == nil || first.NextState.Phase != kernel.PhaseDraft || first.NextState.Revision != 1 {
		t.Fatalf("unexpected next state: %#v", first.NextState)
	}
	if len(first.Events) != 1 || first.Events[0].EventType != "tekroo.event.story.created" {
		t.Fatalf("unexpected events: %#v", first.Events)
	}
}

func TestEvaluatorRejectsUnauthorizedTransitionWithoutEvents(t *testing.T) {
	evaluator := kernel.Evaluator{Catalogue: loadCatalogue(t)}
	command := validStoryCreateCommand(t)
	command.CommandType = "tekroo.command.story.authorize"
	command.ExpectedRevision = kernel.NewExpectedRevision(1)
	command.Authority = kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: "teams::coder-1"}
	command.Payload = json.RawMessage(`{"scope_revision":1,"reason":"approved"}`)
	state := &kernel.AggregateState{
		Kind: kernel.AggregateStory, ID: command.Target.ID, Revision: 1,
		LifecycleEpoch: 1, Phase: kernel.PhaseDraft, Condition: kernel.ConditionRunnable,
	}

	decision := evaluate(t, evaluator, command, kernel.Snapshot{Exists: true, Revision: 1, State: state}, validDecisionContext(t))
	if decision.Receipt.OutcomeCode != kernel.OutcomeRejectedUnauthorized {
		t.Fatalf("outcome = %s, want REJECTED_UNAUTHORIZED", decision.Receipt.OutcomeCode)
	}
	if len(decision.Events) != 0 || decision.NextState != nil || decision.Receipt.StateChanged {
		t.Fatal("rejected command created organizational effects")
	}
}

func TestEvaluatorRejectsMissingRequiredExecution(t *testing.T) {
	evaluator := kernel.Evaluator{Catalogue: loadCatalogue(t)}
	command := validStoryCreateCommand(t)
	command.CommandType = "tekroo.command.task.activate"
	command.Target.Kind = kernel.AggregateTask
	command.ExpectedRevision = kernel.NewExpectedRevision(2)
	command.Authority = kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: "teams::coder-1"}
	command.Payload = json.RawMessage(`{"owner_fqn":"teams::coder-1","ownership_version":1}`)
	state := &kernel.AggregateState{
		Kind: kernel.AggregateTask, ID: command.Target.ID, Revision: 2,
		LifecycleEpoch: 1, Phase: kernel.PhaseReady, Condition: kernel.ConditionRunnable,
	}

	decision := evaluate(t, evaluator, command, kernel.Snapshot{Exists: true, Revision: 2, State: state}, validDecisionContext(t))
	if decision.Receipt.OutcomeCode != kernel.OutcomeRejectedStaleExecution {
		t.Fatalf("outcome = %s, want REJECTED_STALE_EXECUTION", decision.Receipt.OutcomeCode)
	}
}

func TestEvaluatorCannotUseCreateCommandAsAnUpdate(t *testing.T) {
	evaluator := kernel.Evaluator{Catalogue: loadCatalogue(t)}
	command := validStoryCreateCommand(t)
	command.ExpectedRevision = kernel.NewExpectedRevision(1)
	state := &kernel.AggregateState{
		Kind: kernel.AggregateStory, ID: command.Target.ID, Revision: 1,
		LifecycleEpoch: 1, Phase: kernel.PhaseDraft, Condition: kernel.ConditionRunnable,
	}
	decision := evaluate(t, evaluator, command, kernel.Snapshot{Exists: true, Revision: 1, State: state}, validDecisionContext(t))
	if decision.Receipt.OutcomeCode != kernel.OutcomeRejectedConflict || decision.NextState != nil || len(decision.Events) != 0 {
		t.Fatalf("create-as-update decision = %#v", decision)
	}
}

func TestEvaluatorSeparatesInvalidTrustedContextFromDomainRejection(t *testing.T) {
	evaluator := kernel.Evaluator{Catalogue: loadCatalogue(t)}
	_, err := evaluator.Evaluate(validStoryCreateCommand(t), kernel.Snapshot{}, kernel.DecisionContext{})
	if !errors.Is(err, kernel.ErrInvalidDecisionContext) {
		t.Fatalf("error = %v, want ErrInvalidDecisionContext", err)
	}
}

func TestEvaluatorUsesCurrentPolicyAndDoesNotDiscloseUnreadableTargets(t *testing.T) {
	evaluator := kernel.Evaluator{Catalogue: loadCatalogue(t)}
	command := validStoryCreateCommand(t)
	command.CommandType = "tekroo.command.story.authorize"
	command.ExpectedRevision = kernel.NewExpectedRevision(1)
	command.Payload = json.RawMessage(`{"scope_revision":1,"reason":"approved"}`)
	context := validDecisionContext(t)
	grant := grantFor(command.Authority, command, context.Provenance.GrantDigests[0])
	grant.Scope.CanReadTarget = false
	snapshot := kernel.Snapshot{Authorization: authorizationPolicy(grant)}

	decision := evaluate(t, evaluator, command, snapshot, context)
	if decision.Receipt.OutcomeCode != kernel.OutcomeRejectedUnauthorized || decision.Receipt.ReasonCode != "UNAUTHORIZED" {
		t.Fatalf("non-disclosing decision = %#v", decision.Receipt)
	}
	grant.Scope.CanReadTarget = true
	snapshot.Authorization = authorizationPolicy(grant)
	decision = evaluate(t, evaluator, command, snapshot, context)
	if decision.Receipt.OutcomeCode != kernel.OutcomeRejectedNotFound || decision.Receipt.ReasonCode != "TARGET_NOT_FOUND" {
		t.Fatalf("read-authorized absent decision = %#v", decision.Receipt)
	}

	grant.Revoked = true
	snapshot.Authorization = authorizationPolicy(grant)
	decision = evaluate(t, evaluator, command, snapshot, context)
	if decision.Receipt.OutcomeCode != kernel.OutcomeRejectedUnauthorized || len(decision.Provenance.GrantDigests) != 0 {
		t.Fatalf("revoked decision = %#v, provenance = %#v", decision.Receipt, decision.Provenance)
	}
}

func TestEvaluatorEnforcesCanonicalExactRelatedPreconditions(t *testing.T) {
	evaluator := kernel.Evaluator{Catalogue: loadCatalogue(t)}
	command := validStoryCreateCommand(t)
	context := validDecisionContext(t)
	related := kernel.AggregateRef{Kind: kernel.AggregateStory, ID: mustUUID(t, "00000000-0000-7000-8000-0000000000d1")}
	command.Preconditions = []kernel.AggregatePrecondition{{Aggregate: related, Expected: kernel.NewExpectedRevision(3)}}
	snapshot := kernel.Snapshot{Related: map[kernel.AggregateRef]kernel.RelatedSnapshot{related: {Exists: true, Revision: 3}}}

	if decision := evaluate(t, evaluator, command, snapshot, context); decision.Receipt.OutcomeCode != kernel.OutcomeApplied {
		t.Fatalf("exact related precondition = %#v", decision.Receipt)
	}
	for name, relatedSnapshot := range map[string]kernel.RelatedSnapshot{
		"stale":   {Exists: true, Revision: 2},
		"future":  {Exists: true, Revision: 4},
		"missing": {},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := snapshot
			candidate.Related = map[kernel.AggregateRef]kernel.RelatedSnapshot{related: relatedSnapshot}
			if decision := evaluate(t, evaluator, command, candidate, context); decision.Receipt.OutcomeCode != kernel.OutcomeRejectedConflict {
				t.Fatalf("decision = %#v", decision.Receipt)
			}
		})
	}

	absent := command
	absent.Preconditions = []kernel.AggregatePrecondition{{Aggregate: related, Expected: kernel.MustNotExist()}}
	snapshot.Related[related] = kernel.RelatedSnapshot{}
	if decision := evaluate(t, evaluator, absent, snapshot, context); decision.Receipt.OutcomeCode != kernel.OutcomeApplied {
		t.Fatalf("MUST_NOT_EXIST absent = %#v", decision.Receipt)
	}
	snapshot.Related[related] = kernel.RelatedSnapshot{Exists: true, Revision: 1}
	if decision := evaluate(t, evaluator, absent, snapshot, context); decision.Receipt.OutcomeCode != kernel.OutcomeRejectedConflict {
		t.Fatalf("MUST_NOT_EXIST existing = %#v", decision.Receipt)
	}

	zero := command
	zero.Preconditions[0].Expected = kernel.ExpectedRevision{}
	if decision := evaluate(t, evaluator, zero, snapshot, context); decision.Receipt.OutcomeCode != kernel.OutcomeRejectedInvalid {
		t.Fatalf("zero precondition = %#v", decision.Receipt)
	}
	second := kernel.AggregateRef{Kind: kernel.AggregateStory, ID: mustUUID(t, "00000000-0000-7000-8000-0000000000d0")}
	unordered := command
	unordered.Preconditions = []kernel.AggregatePrecondition{
		{Aggregate: related, Expected: kernel.NewExpectedRevision(3)},
		{Aggregate: second, Expected: kernel.NewExpectedRevision(1)},
	}
	if decision := evaluate(t, evaluator, unordered, snapshot, context); decision.Receipt.OutcomeCode != kernel.OutcomeRejectedInvalid {
		t.Fatalf("unordered vector = %#v", decision.Receipt)
	}
}

func TestEvaluatorRejectsStaleCataloguePolicyAndLifecycleContext(t *testing.T) {
	evaluator := kernel.Evaluator{Catalogue: loadCatalogue(t)}
	context := validDecisionContext(t)

	create := validStoryCreateCommand(t)
	staleCatalogue := create
	staleCatalogue.ExpectedCatalogueRevision = kernel.CatalogueRevision - 1
	if decision := evaluate(t, evaluator, staleCatalogue, kernel.Snapshot{}, context); decision.Receipt.OutcomeCode != kernel.OutcomeRejectedConflict || decision.Receipt.ReasonCode != "CATALOGUE_REVISION_CONFLICT" {
		t.Fatalf("stale catalogue receipt = %#v", decision.Receipt)
	}

	stalePolicy := create
	stalePolicy.ExpectedPolicyRevision = context.Provenance.PolicyRevision + 1
	if decision := evaluate(t, evaluator, stalePolicy, kernel.Snapshot{}, context); decision.Receipt.OutcomeCode != kernel.OutcomeRejectedConflict || decision.Receipt.ReasonCode != "POLICY_REVISION_CONFLICT" {
		t.Fatalf("stale policy receipt = %#v", decision.Receipt)
	}

	authorize := create
	authorize.CommandType = "tekroo.command.story.authorize"
	authorize.ExpectedRevision = kernel.NewExpectedRevision(1)
	staleEpoch := uint64(1)
	authorize.ExpectedLifecycleEpoch = &staleEpoch
	authorize.Payload = json.RawMessage(`{"scope_revision":1,"reason":"approved"}`)
	snapshot := storySnapshot(authorize.Target, kernel.PhaseDraft, 1)
	snapshot.State.LifecycleEpoch = 2
	grant := grantFor(authorize.Authority, authorize, context.Provenance.GrantDigests[0])
	snapshot.Authorization = authorizationPolicy(grant)
	if decision := evaluate(t, evaluator, authorize, snapshot, context); decision.Receipt.OutcomeCode != kernel.OutcomeRejectedConflict || decision.Receipt.ReasonCode != "LIFECYCLE_EPOCH_CONFLICT" {
		t.Fatalf("stale lifecycle receipt = %#v", decision.Receipt)
	}
}

func evaluate(t *testing.T, evaluator kernel.Evaluator, command kernel.KernelCommand, snapshot kernel.Snapshot, context kernel.DecisionContext) kernel.Decision {
	t.Helper()
	if command.ExpectedPolicyRevision == 0 {
		command.ExpectedPolicyRevision = context.Provenance.PolicyRevision
	}
	if command.ExpectedCatalogueRevision == 0 {
		command.ExpectedCatalogueRevision = kernel.CatalogueRevision
	}
	if !command.ExpectedRevision.MustNotExist && command.ExpectedLifecycleEpoch == nil && snapshot.State != nil && command.CommandType != "tekroo.command.record.correct" && (command.Target.Kind == kernel.AggregateStory || command.Target.Kind == kernel.AggregateTask) {
		epoch := snapshot.State.LifecycleEpoch
		command.ExpectedLifecycleEpoch = &epoch
	}
	if !snapshot.Authorization.PolicyDigest.Valid() {
		snapshot.Authorization = kernel.AuthorizationPolicy{
			PolicyDigest: context.Provenance.PolicyDigest, Revision: context.Provenance.PolicyRevision,
			Grants: []kernel.AuthorityGrant{{
				GrantDigest: context.Provenance.GrantDigests[0], Grantee: command.Authority,
				Scope: kernel.AuthorityScope{CommandTypes: []string{command.CommandType}, TargetKinds: []kernel.AggregateKind{command.Target.Kind}, TargetIDs: []kernel.UUIDv7{command.Target.ID}, CanReadTarget: true},
			}},
		}
	}
	decision, err := evaluator.Evaluate(command, snapshot, context)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	return decision
}

func validStoryCreateCommand(t *testing.T) kernel.KernelCommand {
	t.Helper()
	commandID := mustUUID(t, "00000000-0000-7000-8000-000000000001")
	targetID := mustUUID(t, "00000000-0000-7000-8000-000000000002")
	correlationID := mustUUID(t, "00000000-0000-7000-8000-000000000003")
	return kernel.KernelCommand{
		ContractManifest:          kernel.ContractIdentity,
		CommandID:                 commandID,
		CommandType:               "tekroo.command.story.create",
		CommandVersion:            kernel.SchemaVersion,
		Target:                    kernel.AggregateRef{Kind: kernel.AggregateStory, ID: targetID},
		Authority:                 kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"},
		ExpectedRevision:          kernel.MustNotExist(),
		ExpectedPolicyRevision:    1,
		ExpectedCatalogueRevision: kernel.CatalogueRevision,
		IdempotencyKey:            "story-create-1",
		CorrelationID:             correlationID,
		Payload:                   json.RawMessage(`{"acceptance_criteria":["one owner wins"],"description":"Exact ownership.","title":"Ownership"}`),
	}
}

func validDecisionContext(t *testing.T) kernel.DecisionContext {
	t.Helper()
	received := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	return kernel.DecisionContext{
		ReceivedAt: received,
		DecidedAt:  received.Add(time.Millisecond),
		EventID:    mustUUID(t, "00000000-0000-7000-8000-000000000004"),
		IntentID:   mustUUID(t, "00000000-0000-7000-8000-000000000005"),
		Provenance: validProvenanceBasis(t),
	}
}

func validProvenanceBasis(t *testing.T) kernel.ProvenanceBasis {
	t.Helper()
	sourceDigest := mustDigest(t, "1111111111111111111111111111111111111111111111111111111111111111")
	overlay := kernel.OverlayIdentity{
		SourceTreeDigest: sourceDigest,
		ChangeDigest:     mustDigest(t, "2222222222222222222222222222222222222222222222222222222222222222"),
	}
	overlayDigest, err := overlay.Digest()
	if err != nil {
		t.Fatal(err)
	}
	artifactDigest := mustDigest(t, "7777777777777777777777777777777777777777777777777777777777777777")
	return kernel.ProvenanceBasis{
		CatalogueDigest: mustDigest(t, "8888888888888888888888888888888888888888888888888888888888888888"),
		PolicyDigest:    mustDigest(t, "9999999999999999999999999999999999999999999999999999999999999999"),
		PolicyRevision:  1,
		GrantDigests:    []kernel.Digest{mustDigest(t, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")},
		Source: kernel.SourceIdentity{
			Repository: "github.com/tekroo-ai/teams", Commit: "test-source", TreeDigest: sourceDigest, Scope: ".",
		},
		Overlay: overlay,
		Build: kernel.BuildIdentity{
			SourceTreeDigest: sourceDigest, OverlayDigest: overlayDigest,
			DependencyLockDigest:  mustDigest(t, "3333333333333333333333333333333333333333333333333333333333333333"),
			ToolchainDigest:       mustDigest(t, "4444444444444444444444444444444444444444444444444444444444444444"),
			BuildDefinitionDigest: mustDigest(t, "5555555555555555555555555555555555555555555555555555555555555555"),
			ArtifactDigest:        artifactDigest,
		},
		Runtime: kernel.RuntimeIdentity{
			BuildArtifactDigest: artifactDigest, ContractManifest: kernel.ContractIdentity,
			ConfigurationDigest: mustDigest(t, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
			EnvironmentDigest:   mustDigest(t, "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"),
			RoleLibraryDigests:  []kernel.Digest{}, Capabilities: []string{"kernel-evaluate"}, ProviderIdentities: []string{},
		},
	}
}

func loadCatalogue(t *testing.T) *contract.Catalogue {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate evaluator test")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
	catalogue, err := contract.Load(os.DirFS(repositoryRoot), "CONTRACTS/tekroo.kernel.contracts/0.11.0")
	if err != nil {
		t.Fatalf("load catalogue: %v", err)
	}
	return catalogue
}

func mustUUID(t *testing.T, value string) kernel.UUIDv7 {
	t.Helper()
	id, err := kernel.ParseUUIDv7(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mustDigest(t *testing.T, value string) kernel.Digest {
	t.Helper()
	digest, err := kernel.ParseDigest(value)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
