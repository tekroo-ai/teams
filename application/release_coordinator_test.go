package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/fake"
	"github.com/tekroo-ai/teams/adapters/gitprovider"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

func TestReleaseCoordinatorPersistsIntentBeforeDeterministicProviderOutcomes(t *testing.T) {
	for _, test := range []struct {
		name        string
		outcome     kernel.ReleaseOutcome
		state       kernel.ReleaseProviderState
		wantCommits bool
	}{
		{name: "merged", outcome: kernel.ReleaseOutcomeMerged, state: kernel.ReleaseProviderMerged, wantCommits: true},
		{name: "already-merged", outcome: kernel.ReleaseOutcomeAlreadyMerged, state: kernel.ReleaseProviderMerged, wantCommits: true},
		{name: "provider-failure", outcome: kernel.ReleaseOutcomeFailed, state: kernel.ReleaseProviderOpen},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := qualifiedReleasePlan()
			operation := releaseExecutionOperation(t, plan)
			provider := fake.NewReleaseProvider()
			observation := releaseObservation(plan, operation.AttemptID, test.outcome, test.state)
			if !test.wantCommits {
				observation.BaseCommit, observation.HeadCommit, observation.TreeDigest = "", "", ""
			}
			provider.ScriptMerge(operation.ProviderKey, fake.ReleaseProviderStep{Observation: observation})
			var commands []kernel.KernelCommand
			service := releaseReceiptService(t, &commands, plan.Revision)
			coordinator, err := application.NewReleaseCoordinator(service, provider, application.ReleaseCoordinatorPolicy{OperationTimeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}

			result, err := coordinator.ExecuteNext(context.Background(), operation)
			if err != nil || result.RequestReceipt == nil || result.ResultReceipt == nil || len(commands) != 2 {
				t.Fatalf("result=%#v err=%v commands=%d", result, err, len(commands))
			}
			if commands[0].CommandType != "tekroo.command.release-plan.request-execution" || commands[1].CommandType != "tekroo.command.release-plan.record-result" || commands[1].Causation[0].ParentEventID != commandsReceiptEvent(0) || provider.MergeCalls(operation.ProviderKey) != 1 {
				t.Fatalf("commands=%#v merge calls=%d", commands, provider.MergeCalls(operation.ProviderKey))
			}
			var payload struct {
				Outcome            kernel.ReleaseOutcome `json:"outcome"`
				ObservedBaseCommit string                `json:"observed_base_commit"`
				ObservedHeadCommit string                `json:"observed_head_commit"`
				ObservedTree       string                `json:"observed_tree_digest"`
			}
			if json.Unmarshal(commands[1].Payload, &payload) != nil || payload.Outcome != test.outcome {
				t.Fatalf("result payload = %s", commands[1].Payload)
			}
			if test.wantCommits && (payload.ObservedBaseCommit != plan.BaseCommit || payload.ObservedHeadCommit != plan.OrderedMerges[0].HeadCommit || payload.ObservedTree != plan.ExpectedQualifiedTree) {
				t.Fatalf("successful provider identity = %#v", payload)
			}
			if !test.wantCommits && (payload.ObservedBaseCommit != "" || payload.ObservedHeadCommit != "" || payload.ObservedTree != "") {
				t.Fatalf("failed provider identity was inferred = %#v", payload)
			}
		})
	}
}

func TestReleaseCoordinatorRecordsUnknownAfterTimeoutAndReplayIsIdentical(t *testing.T) {
	plan := qualifiedReleasePlan()
	operation := releaseExecutionOperation(t, plan)
	provider := fake.NewReleaseProvider()
	provider.ScriptMerge(operation.ProviderKey, fake.ReleaseProviderStep{WaitForCancellation: true})
	var commands []kernel.KernelCommand
	service := releaseReceiptService(t, &commands, plan.Revision)
	coordinator, _ := application.NewReleaseCoordinator(service, provider, application.ReleaseCoordinatorPolicy{OperationTimeout: 5 * time.Millisecond})

	first, firstErr := coordinator.ExecuteNext(context.Background(), operation)
	second, secondErr := coordinator.ExecuteNext(context.Background(), operation)
	if !errors.Is(firstErr, context.DeadlineExceeded) || !errors.Is(secondErr, context.DeadlineExceeded) || first.ResultReceipt == nil || second.ResultReceipt == nil || len(commands) != 4 {
		t.Fatalf("first=%#v err=%v second=%#v err=%v commands=%d", first, firstErr, second, secondErr, len(commands))
	}
	if !reflect.DeepEqual(commands[0], commands[2]) || !reflect.DeepEqual(commands[1], commands[3]) {
		t.Fatalf("replayed commands diverged\nfirst=%#v\nsecond=%#v", commands[:2], commands[2:])
	}
	var payload map[string]any
	if json.Unmarshal(commands[1].Payload, &payload) != nil || payload["outcome"] != "UNKNOWN" || payload["observed_base_commit"] != nil || payload["observed_head_commit"] != nil || payload["observed_tree_digest"] != nil {
		t.Fatalf("timeout payload = %s", commands[1].Payload)
	}
}

func TestReleaseCoordinatorReconcilesUnknownWithoutAnotherMerge(t *testing.T) {
	plan := reconcilingReleasePlan()
	provider := fake.NewReleaseProvider()
	observation := releaseObservation(plan, plan.ActiveAttempt.AttemptID, kernel.ReleaseOutcomeMerged, kernel.ReleaseProviderMerged)
	provider.ScriptReconciliation(plan.ActiveAttempt.AttemptID, fake.ReleaseProviderStep{Observation: observation})
	var commands []kernel.KernelCommand
	service := executionCommandFunc(func(_ context.Context, command kernel.KernelCommand, _ kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		emits, err := loadCatalogue(t).ValidateFixtureCommand(command.CommandType, command.Payload)
		if err != nil || !reflect.DeepEqual(emits, []string{"tekroo.event.release-plan.reconciliation-recorded"}) {
			t.Fatalf("reconciliation contract validation: emits=%v err=%v payload=%s", emits, err, command.Payload)
		}
		commands = append(commands, command)
		revision := plan.Revision + 1
		return kernel.CommandReceipt{OutcomeCode: kernel.OutcomeApplied, ResultingRevision: &revision, EventIDs: []kernel.UUIDv7{commandsReceiptEvent(2)}}, nil
	})
	coordinator, _ := application.NewReleaseCoordinator(service, provider, application.ReleaseCoordinatorPolicy{OperationTimeout: time.Second})
	operation := releaseReconciliationOperation(t, plan)

	result, err := coordinator.Reconcile(context.Background(), operation)
	if err != nil || result.ResultReceipt == nil || len(commands) != 1 || provider.ReconciliationCalls(plan.ActiveAttempt.AttemptID) != 1 || provider.MergeCalls(plan.ActiveAttempt.ProviderIdempotencyKey) != 0 {
		t.Fatalf("result=%#v err=%v commands=%d reconcile=%d merges=%d", result, err, len(commands), provider.ReconciliationCalls(plan.ActiveAttempt.AttemptID), provider.MergeCalls(plan.ActiveAttempt.ProviderIdempotencyKey))
	}
	var payload struct {
		Outcome          kernel.ReleaseOutcome       `json:"outcome"`
		ProviderState    kernel.ReleaseProviderState `json:"provider_state"`
		SupersedesResult kernel.UUIDv7               `json:"supersedes_result_event_id"`
	}
	if json.Unmarshal(commands[0].Payload, &payload) != nil || payload.Outcome != kernel.ReleaseOutcomeMerged || payload.ProviderState != kernel.ReleaseProviderMerged || payload.SupersedesResult != plan.Results[plan.ActiveAttempt.MergeID].ResultEventID {
		t.Fatalf("reconciliation payload = %s", commands[0].Payload)
	}
}

func TestReleaseCoordinatorConvertsChangedProviderIdentityToUnknown(t *testing.T) {
	plan := qualifiedReleasePlan()
	operation := releaseExecutionOperation(t, plan)
	provider := fake.NewReleaseProvider()
	observation := releaseObservation(plan, operation.AttemptID, kernel.ReleaseOutcomeMerged, kernel.ReleaseProviderMerged)
	observation.HeadCommit = "9999999999999999999999999999999999999999"
	provider.ScriptMerge(operation.ProviderKey, fake.ReleaseProviderStep{Observation: observation})
	var commands []kernel.KernelCommand
	coordinator, _ := application.NewReleaseCoordinator(releaseReceiptService(t, &commands, plan.Revision), provider, application.ReleaseCoordinatorPolicy{OperationTimeout: time.Second})

	result, err := coordinator.ExecuteNext(context.Background(), operation)
	if !errors.Is(err, application.ErrReleaseObservation) || result.ResultReceipt == nil || result.Observation.Outcome != kernel.ReleaseOutcomeUnknown || len(commands) != 2 {
		t.Fatalf("result=%#v err=%v commands=%d", result, err, len(commands))
	}
}

func TestReleaseCoordinatorLinksNextOrderedMergeToPriorEffectiveResult(t *testing.T) {
	plan := qualifiedReleasePlan()
	previousMerge := plan.OrderedMerges[0]
	plan.OrderedMerges = append(plan.OrderedMerges, kernel.ReleaseMergePlan{MergeID: "00000000-0000-7000-8000-000000000765", ChangeRef: "refs/heads/story-2", HeadCommit: "4444444444444444444444444444444444444444", Role: "story"})
	plan.Qualification.OrderedHeadCommits = append(plan.Qualification.OrderedHeadCommits, plan.OrderedMerges[1].HeadCommit)
	plan.NextMergeIndex = 1
	previousResultEvent := kernel.UUIDv7("00000000-0000-7000-8000-000000000766")
	plan.Results[previousMerge.MergeID] = kernel.ReleaseResult{MergeID: previousMerge.MergeID, AttemptID: "00000000-0000-7000-8000-000000000755", Outcome: kernel.ReleaseOutcomeMerged, ResultEventID: previousResultEvent, ObservedTree: "6666666666666666666666666666666666666666"}
	operation := releaseExecutionOperation(t, plan)
	operation.AttemptID = "00000000-0000-7000-8000-000000000767"
	operation.ProviderKey = "release-751-merge-765-round-1"
	provider := fake.NewReleaseProvider()
	provider.ScriptMerge(operation.ProviderKey, fake.ReleaseProviderStep{Observation: releaseObservation(plan, operation.AttemptID, kernel.ReleaseOutcomeMerged, kernel.ReleaseProviderMerged)})
	var commands []kernel.KernelCommand
	coordinator, _ := application.NewReleaseCoordinator(releaseReceiptService(t, &commands, plan.Revision), provider, application.ReleaseCoordinatorPolicy{OperationTimeout: time.Second})

	result, err := coordinator.ExecuteNext(context.Background(), operation)
	if err != nil || result.ResultReceipt == nil || len(commands) != 2 || len(commands[0].Causation) != 1 || commands[0].Causation[0] != (kernel.DagParent{ParentEventID: previousResultEvent, EdgeKind: kernel.EdgeResponse}) {
		t.Fatalf("result=%#v err=%v request parents=%#v", result, err, commands[0].Causation)
	}
}

func TestReleaseCoordinatorAndLocalGitProviderCompleteExactPortBoundary(t *testing.T) {
	fixture := localGitFixture(t)
	plan := qualifiedReleasePlan()
	plan.RepositoryURL = (&url.URL{Scheme: "file", Path: fixture.bare}).String()
	plan.BaseCommit = fixture.base
	plan.OrderedMerges[0].HeadCommit = fixture.head
	plan.OrderedMerges[0].ChangeRef = "refs/heads/story-1"
	plan.GitVersion = fixture.version
	plan.ExpectedQualifiedTree = fixture.tree
	plan.Qualification.QualifiedBaseCommit = fixture.base
	plan.Qualification.OrderedHeadCommits = []string{fixture.head}
	plan.Qualification.QualifiedTreeDigest = fixture.tree
	createPayload, err := json.Marshal(map[string]any{
		"author": plan.Author, "author_approval_event_id": plan.AuthorApprovalEventID, "author_approval_revision": plan.AuthorApprovalRevision,
		"base_commit": plan.BaseCommit, "base_ref": plan.BaseRef, "conflict_policy": plan.ConflictPolicy, "contract_manifest": plan.ContractManifest,
		"evidence_ids": plan.EvidenceIDs, "execution_round_limit": plan.ExecutionRoundLimit, "expected_qualified_tree": plan.ExpectedQualifiedTree,
		"expected_story_revision": plan.ExpectedStoryRevision, "git_version": plan.GitVersion, "manifest_sha256": plan.ManifestSHA256,
		"merge_strategy": plan.MergeStrategy, "ordered_merges": plan.OrderedMerges, "plan_digest": plan.PlanDigest, "release_mode": plan.Mode,
		"release_plan_id": plan.ReleasePlanID, "release_policy_revision": plan.PolicyRevision, "repository_url": plan.RepositoryURL,
		"required_profiles": plan.RequiredProfiles, "story_id": plan.Story.ID, "story_lifecycle_epoch": plan.StoryLifecycleEpoch,
	})
	if err != nil {
		t.Fatal(err)
	}
	if emits, validationErr := loadCatalogue(t).ValidateFixtureCommand("tekroo.command.release-plan.create", createPayload); validationErr != nil || !reflect.DeepEqual(emits, []string{"tekroo.event.release-plan.created"}) {
		t.Fatalf("file URI release plan contract validation: emits=%v err=%v payload=%s", emits, validationErr, createPayload)
	}
	provider, err := gitprovider.New(gitprovider.Config{AllowedRoot: fixture.root, OperationTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	operation := releaseExecutionOperation(t, plan)
	var commands []kernel.KernelCommand
	coordinator, _ := application.NewReleaseCoordinator(releaseReceiptService(t, &commands, plan.Revision), provider, application.ReleaseCoordinatorPolicy{OperationTimeout: 2 * time.Second})

	result, err := coordinator.ExecuteNext(context.Background(), operation)
	if err != nil || result.ResultReceipt == nil || result.Observation.Outcome != kernel.ReleaseOutcomeMerged || result.Observation.TreeDigest != fixture.tree || len(commands) != 2 {
		t.Fatalf("result=%#v err=%v commands=%d", result, err, len(commands))
	}
	if current := runLocalGit(t, "--git-dir", fixture.bare, "rev-parse", "refs/heads/main"); current != fixture.head {
		t.Fatalf("base ref=%s, want %s", current, fixture.head)
	}
}

func releaseReceiptService(t *testing.T, commands *[]kernel.KernelCommand, initialRevision uint64) executionCommandFunc {
	t.Helper()
	catalogue := loadCatalogue(t)
	receipts := make(map[kernel.UUIDv7]kernel.CommandReceipt)
	return func(_ context.Context, command kernel.KernelCommand, _ kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		emits, err := catalogue.ValidateFixtureCommand(command.CommandType, command.Payload)
		if err != nil || len(emits) != 1 {
			t.Fatalf("release contract validation: emits=%v err=%v payload=%s", emits, err, command.Payload)
		}
		*commands = append(*commands, command)
		if receipt, found := receipts[command.CommandID]; found {
			return receipt, nil
		}
		index := len(receipts)
		revision := initialRevision + uint64(index) + 1
		receipt := kernel.CommandReceipt{OutcomeCode: kernel.OutcomeApplied, ResultingRevision: &revision, EventIDs: []kernel.UUIDv7{commandsReceiptEvent(index)}}
		receipts[command.CommandID] = receipt
		return receipt, nil
	}
}

func releaseExecutionOperation(t *testing.T, plan kernel.ReleasePlanSnapshot) application.ReleaseExecutionPlan {
	t.Helper()
	basis, err := fake.ProvenanceBasis()
	if err != nil {
		t.Fatal(err)
	}
	return application.ReleaseExecutionPlan{
		Plan: plan, AttemptID: kernel.UUIDv7("00000000-0000-7000-8000-000000000755"), ProviderKey: "release-751-merge-752-round-1",
		Request: releaseTemplate("00000000-0000-7000-8000-000000000761", kernel.PrincipalPolicy, "request-release-751-752-1", "00000000-0000-7000-8000-000000000750"), RequestProvenance: basis,
		Result: releaseTemplate("00000000-0000-7000-8000-000000000762", kernel.PrincipalService, "result-release-751-752-1", "00000000-0000-7000-8000-000000000756"), ResultProvenance: basis,
		ObservedAt: time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC),
	}
}

func releaseReconciliationOperation(t *testing.T, plan kernel.ReleasePlanSnapshot) application.ReleaseReconciliationPlan {
	t.Helper()
	basis, err := fake.ProvenanceBasis()
	if err != nil {
		t.Fatal(err)
	}
	return application.ReleaseReconciliationPlan{Plan: plan, Command: releaseTemplate("00000000-0000-7000-8000-000000000759", kernel.PrincipalPolicy, "reconcile-release-751-752-1", "00000000-0000-7000-8000-000000000756"), Provenance: basis, ObservedAt: time.Date(2026, time.August, 11, 12, 1, 0, 0, time.UTC)}
}

func releaseTemplate(commandID string, kind kernel.PrincipalKind, key, evidenceID string) application.ReleaseCommandTemplate {
	return application.ReleaseCommandTemplate{
		CommandID: kernel.UUIDv7(commandID), Authority: kernel.PrincipalRef{Kind: kind, ID: "release-policy"}, ExpectedPolicyRevision: 1,
		IdempotencyKey: key, CorrelationID: kernel.UUIDv7("00000000-0000-7000-8000-000000000753"),
		EvidenceRefs: []kernel.EvidenceRef{{EvidenceID: kernel.UUIDv7(evidenceID), SHA256: kernel.Digest("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd")}},
	}
}

func qualifiedReleasePlan() kernel.ReleasePlanSnapshot {
	plan := baseReleasePlan()
	plan.Revision = 2
	plan.State = kernel.ReleaseQualified
	plan.Qualification = &kernel.ReleaseQualification{EventID: kernel.UUIDv7("00000000-0000-7000-8000-000000000754"), QualificationID: kernel.UUIDv7("00000000-0000-7000-8000-000000000764"), QualifiedBaseCommit: plan.BaseCommit, OrderedHeadCommits: []string{plan.OrderedMerges[0].HeadCommit}, QualifiedTreeDigest: plan.ExpectedQualifiedTree, GateDefinition: kernel.Digest("cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"), Toolchain: kernel.Digest("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"), DependencyLock: kernel.Digest("eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"), ArtifactDigests: []kernel.Digest{"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"}}
	return plan
}

func reconcilingReleasePlan() kernel.ReleasePlanSnapshot {
	plan := qualifiedReleasePlan()
	plan.Revision = 4
	plan.State = kernel.ReleaseReconciling
	plan.ActiveAttempt = &kernel.ReleaseAttempt{MergeID: plan.OrderedMerges[0].MergeID, AttemptID: kernel.UUIDv7("00000000-0000-7000-8000-000000000755"), Round: 1, ProviderIdempotencyKey: "release-751-merge-752-round-1", RequestEventID: commandsReceiptEvent(0)}
	plan.Results[plan.ActiveAttempt.MergeID] = kernel.ReleaseResult{MergeID: plan.ActiveAttempt.MergeID, AttemptID: plan.ActiveAttempt.AttemptID, Outcome: kernel.ReleaseOutcomeUnknown, ResultEventID: commandsReceiptEvent(1)}
	return plan
}

func baseReleasePlan() kernel.ReleasePlanSnapshot {
	return kernel.ReleasePlanSnapshot{
		ReleasePlanID: kernel.UUIDv7("00000000-0000-7000-8000-000000000751"), OpeningEventID: kernel.UUIDv7("00000000-0000-7000-8000-000000000748"), Revision: 1, State: kernel.ReleasePlanned, Mode: kernel.ReleaseModeCode,
		Story: kernel.AggregateRef{Kind: kernel.AggregateStory, ID: kernel.UUIDv7("00000000-0000-7000-8000-000000000101")}, StoryLifecycleEpoch: 1, ExpectedStoryRevision: 8,
		Author: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal-author"}, AuthorApprovalEventID: kernel.UUIDv7("00000000-0000-7000-8000-000000000749"), AuthorApprovalRevision: 1,
		PolicyRevision: 1, PlanDigest: kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), EvidenceIDs: []kernel.UUIDv7{"00000000-0000-7000-8000-000000000750"},
		RepositoryURL: "https://example.invalid/tekroo/teams.git", BaseRef: "main", BaseCommit: "1111111111111111111111111111111111111111",
		OrderedMerges: []kernel.ReleaseMergePlan{{MergeID: kernel.UUIDv7("00000000-0000-7000-8000-000000000752"), ChangeRef: "refs/heads/story-1", HeadCommit: "2222222222222222222222222222222222222222", Role: "story"}},
		MergeStrategy: "FF_ONLY_ORDERED", GitVersion: "git version 2.51.0", ConflictPolicy: "FAIL_NO_IMPROVISATION", ContractManifest: kernel.ContractIdentity,
		ManifestSHA256: kernel.Digest("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"), RequiredProfiles: []string{"contract-structure", "core-hermetic", "mongo-integration", "synthesized-merge"},
		ExpectedQualifiedTree: "3333333333333333333333333333333333333333", ExecutionRoundLimit: 2, NextRound: 1, Results: map[kernel.UUIDv7]kernel.ReleaseResult{},
	}
}

func releaseObservation(plan kernel.ReleasePlanSnapshot, attemptID kernel.UUIDv7, outcome kernel.ReleaseOutcome, state kernel.ReleaseProviderState) kernel.ReleaseProviderObservation {
	return kernel.ReleaseProviderObservation{
		ReleasePlanID: plan.ReleasePlanID, MergeID: plan.OrderedMerges[plan.NextMergeIndex].MergeID, AttemptID: attemptID, State: state, Outcome: outcome,
		BaseCommit: plan.BaseCommit, HeadCommit: plan.OrderedMerges[plan.NextMergeIndex].HeadCommit, TreeDigest: plan.ExpectedQualifiedTree, Reasons: []string{"scripted authoritative provider observation"},
	}
}

func commandsReceiptEvent(index int) kernel.UUIDv7 {
	return []kernel.UUIDv7{"00000000-0000-7000-8000-000000000757", "00000000-0000-7000-8000-000000000758", "00000000-0000-7000-8000-000000000759"}[index]
}

type localGitRepository struct {
	root, bare, base, head, tree, version string
}

func localGitFixture(t *testing.T) localGitRepository {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	bare := filepath.Join(root, "target.git")
	runLocalGit(t, "init", "-b", "main", source)
	runLocalGit(t, "-C", source, "config", "user.name", "Tekroo Test")
	runLocalGit(t, "-C", source, "config", "user.email", "tekroo@example.invalid")
	if err := os.WriteFile(filepath.Join(source, "artifact.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runLocalGit(t, "-C", source, "add", "artifact.txt")
	runLocalGit(t, "-C", source, "commit", "-m", "base")
	base := runLocalGit(t, "-C", source, "rev-parse", "HEAD")
	runLocalGit(t, "-C", source, "switch", "-c", "story-1")
	if err := os.WriteFile(filepath.Join(source, "artifact.txt"), []byte("base\nstory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runLocalGit(t, "-C", source, "commit", "-am", "story")
	head := runLocalGit(t, "-C", source, "rev-parse", "HEAD")
	tree := runLocalGit(t, "-C", source, "rev-parse", "HEAD^{tree}")
	runLocalGit(t, "clone", "--bare", source, bare)
	return localGitRepository{root: root, bare: bare, base: base, head: head, tree: tree, version: runLocalGit(t, "--version")}
}

func runLocalGit(t *testing.T, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC", "GIT_TERMINAL_PROMPT=0", "GIT_NO_LAZY_FETCH=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
	value := string(output)
	for len(value) > 0 && (value[len(value)-1] == '\n' || value[len(value)-1] == '\r' || value[len(value)-1] == ' ' || value[len(value)-1] == '\t') {
		value = value[:len(value)-1]
	}
	return value
}
