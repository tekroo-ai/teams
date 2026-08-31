package memory_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/fake"
	"github.com/tekroo-ai/teams/adapters/memory"
	"github.com/tekroo-ai/teams/kernel"
)

func TestStoreCommitsAtomicallyAndReplaysExactReceipt(t *testing.T) {
	store := memory.NewStore()
	target := kernel.AggregateRef{Kind: kernel.AggregateStory, ID: uuid("00000000-0000-7000-8000-000000000001")}
	commandID := uuid("00000000-0000-7000-8000-000000000002")
	command := kernel.KernelCommand{
		ContractManifest: kernel.ContractIdentity, CommandID: commandID,
		CommandType: "tekroo.command.story.create", Target: target,
		Authority: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"}, IdempotencyKey: "store-test",
	}
	store.SetAuthorizationPolicy(kernel.AuthorizationPolicy{
		PolicyDigest: kernel.Digest("9999999999999999999999999999999999999999999999999999999999999999"), Revision: 1,
		Grants: []kernel.AuthorityGrant{{
			GrantDigest: kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), Grantee: command.Authority,
			Scope: kernel.AuthorityScope{CommandTypes: []string{command.CommandType}, TargetKinds: []kernel.AggregateKind{target.Kind}, TargetIDs: []kernel.UUIDv7{target.ID}, CanReadTarget: true},
		}},
	})
	fingerprint, err := kernel.CommandFingerprint(command)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := kernel.IdempotencyScopeDigest(command)
	if err != nil {
		t.Fatal(err)
	}
	eventID := uuid("00000000-0000-7000-8000-000000000003")
	revision := uint64(1)
	decision := kernel.Decision{
		CommandFingerprint: fingerprint,
		IdempotencyScope:   scope,
		NextState: &kernel.AggregateState{
			Kind: kernel.AggregateStory, ID: target.ID, Revision: 1,
			LifecycleEpoch: 1, ScopeRevision: 1, Phase: kernel.PhaseDraft, Condition: kernel.ConditionRunnable,
		},
		Events: []kernel.DomainEvent{{EventID: eventID, EventType: "tekroo.event.story.created", Aggregate: target, AggregateRevision: 1, LifecycleEpoch: 1}},
		Receipt: kernel.CommandReceipt{
			CommandID: commandID, Target: target, OutcomeCode: kernel.OutcomeApplied,
			ResultingRevision: &revision, EventIDs: []kernel.UUIDv7{eventID},
			ProvenanceDigest: kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		},
		Authority: kernel.AuthorityDecision{Principal: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"}, Allowed: true, Reason: "APPLIED"},
		Outbox:    []kernel.OutboxIntent{{IntentID: uuid("00000000-0000-7000-8000-000000000005"), EventID: eventID, Kind: "DOMAIN_EVENT"}},
	}
	decision = attachProvenance(t, decision)

	if err := store.Commit(context.Background(), kernel.Snapshot{}, decision); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := store.Commit(context.Background(), kernel.Snapshot{}, decision); !errors.Is(err, kernel.ErrDecisionAlreadyCommitted) {
		t.Fatalf("exact replay error = %v, want ErrDecisionAlreadyCommitted", err)
	}
	conflicting := decision
	conflicting.Receipt.Target.ID = uuid("00000000-0000-7000-8000-000000000004")
	conflicting.NextState.ID = conflicting.Receipt.Target.ID
	conflicting.Events[0].Aggregate = conflicting.Receipt.Target
	conflictingCommand := command
	conflictingCommand.Target = conflicting.Receipt.Target
	conflicting.CommandFingerprint, err = kernel.CommandFingerprint(conflictingCommand)
	if err != nil {
		t.Fatal(err)
	}
	conflicting.IdempotencyScope, err = kernel.IdempotencyScopeDigest(conflictingCommand)
	if err != nil {
		t.Fatal(err)
	}
	conflicting = attachProvenance(t, conflicting)
	if err := store.Commit(context.Background(), kernel.Snapshot{}, conflicting); !errors.Is(err, memory.ErrReceiptConflict) {
		t.Fatalf("conflicting replay error = %v, want ErrReceiptConflict", err)
	}
	if store.EventCount() != 1 {
		t.Fatalf("event count = %d, want 1", store.EventCount())
	}
	if err := store.VerifyAggregate(context.Background(), target); err != nil {
		t.Fatalf("verify event fold: %v", err)
	}
	snapshot, err := store.Load(context.Background(), target)
	if err != nil || !snapshot.Exists || snapshot.Revision != 1 {
		t.Fatalf("load snapshot = %#v, %v", snapshot, err)
	}
	receipt, found, err := store.LookupReceipt(context.Background(), command, time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC))
	if err != nil || !found || receipt.OutcomeCode != kernel.OutcomeApplied {
		t.Fatalf("lookup receipt = %#v, %t, %v", receipt, found, err)
	}
}

func TestStoreRejectsStaleSnapshot(t *testing.T) {
	store := memory.NewStore()
	target := kernel.AggregateRef{Kind: kernel.AggregateStory, ID: uuid("00000000-0000-7000-8000-000000000001")}
	revision := uint64(1)
	decision := kernel.Decision{
		CommandFingerprint: kernel.Digest("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
		IdempotencyScope:   kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		NextState:          &kernel.AggregateState{Kind: kernel.AggregateStory, ID: target.ID, Revision: 1},
		Receipt: kernel.CommandReceipt{
			CommandID: uuid("00000000-0000-7000-8000-000000000002"),
			Target:    target, OutcomeCode: kernel.OutcomeApplied, ResultingRevision: &revision,
			EventIDs: []kernel.UUIDv7{uuid("00000000-0000-7000-8000-000000000004")},
		},
		Events: []kernel.DomainEvent{{EventID: uuid("00000000-0000-7000-8000-000000000004"), EventType: "tekroo.event.story.created", Aggregate: target, AggregateRevision: 1, LifecycleEpoch: 1}},
		Outbox: []kernel.OutboxIntent{{IntentID: uuid("00000000-0000-7000-8000-000000000005"), EventID: uuid("00000000-0000-7000-8000-000000000004"), Kind: "DOMAIN_EVENT"}},
	}
	decision = attachProvenance(t, decision)
	if err := store.Commit(context.Background(), kernel.Snapshot{}, decision); err != nil {
		t.Fatal(err)
	}
	decision.Receipt.CommandID = uuid("00000000-0000-7000-8000-000000000003")
	decision.IdempotencyScope = kernel.Digest("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	decision = attachProvenance(t, decision)
	if err := store.Commit(context.Background(), kernel.Snapshot{}, decision); !errors.Is(err, memory.ErrConflict) {
		t.Fatalf("stale commit error = %v, want ErrConflict", err)
	}
}

func TestStoreTracksRevisionWithoutWorkState(t *testing.T) {
	store := memory.NewStore()
	target := kernel.AggregateRef{Kind: kernel.AggregateExecution, ID: uuid("00000000-0000-7000-8000-000000000001")}
	revision := uint64(1)
	eventID := uuid("00000000-0000-7000-8000-000000000003")
	decision := kernel.Decision{CommandFingerprint: kernel.Digest("cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"), IdempotencyScope: kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), Receipt: kernel.CommandReceipt{
		CommandID:         uuid("00000000-0000-7000-8000-000000000002"),
		Target:            target,
		OutcomeCode:       kernel.OutcomeApplied,
		ResultingRevision: &revision,
		EventIDs:          []kernel.UUIDv7{eventID},
	}, Events: []kernel.DomainEvent{{EventID: eventID, EventType: "tekroo.event.execution.registered", Aggregate: target, AggregateRevision: 1, LifecycleEpoch: 1}},
		Outbox: []kernel.OutboxIntent{{IntentID: uuid("00000000-0000-7000-8000-000000000004"), EventID: eventID, Kind: "DOMAIN_EVENT"}},
	}
	decision = attachProvenance(t, decision)
	if err := store.Commit(context.Background(), kernel.Snapshot{}, decision); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Load(context.Background(), target)
	if err != nil || !snapshot.Exists || snapshot.Revision != 1 || snapshot.State != nil {
		t.Fatalf("generic snapshot = %#v, %v", snapshot, err)
	}
}

func TestStoreAtomicallyConsumesWorkBudgetWithInvocationAuthorization(t *testing.T) {
	store := memory.NewStore()
	budget := kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: uuid("00000000-0000-7000-8000-000000000701")}
	task := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: uuid("00000000-0000-7000-8000-000000000702")}
	purposeLimits := make(kernel.PurposeCounters, len(kernel.AllWorkPurposes))
	for _, purpose := range kernel.AllWorkPurposes {
		purposeLimits[purpose] = 1
	}
	purposeLimits[kernel.PurposeImplementation] = 2
	budgetPayload, err := json.Marshal(map[string]any{
		"authority": kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"}, "budget_account_id": budget.ID,
		"deadline_at": "2026-09-30T00:00:00Z", "evidence_ids": []kernel.UUIDv7{uuid("00000000-0000-7000-8000-000000000703")},
		"lifecycle_epoch": 1, "model_invocation_limit": 3, "policy_digest": kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		"policy_revision": 1, "purpose_limits": purposeLimits, "root_work": task,
	})
	if err != nil {
		t.Fatal(err)
	}
	createBudget := releaseProjectionDecision(t, 70, budget, "tekroo.command.work-budget.create", "tekroo.event.work-budget.created", 1, budgetPayload)
	if err := store.Commit(context.Background(), kernel.Snapshot{}, createBudget); err != nil {
		t.Fatalf("create budget: %v", err)
	}

	bindingPayload, err := json.Marshal(map[string]any{
		"budget_account_id": budget.ID, "expected_task_revision": 0, "lifecycle_epoch": 1,
		"purpose_limits": purposeLimits, "scope_revision": 1, "task_id": task.ID, "task_model_invocation_limit": 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	bindBudget := releaseProjectionDecision(t, 71, task, "tekroo.command.task.bind-work-budget", "tekroo.event.task.work-budget-bound", 1, bindingPayload)
	if err := store.Commit(context.Background(), kernel.Snapshot{}, bindBudget); err != nil {
		t.Fatalf("bind budget: %v", err)
	}

	profile := kernel.WorkProfileBinding{
		ProfileID: uuid("00000000-0000-7000-8000-000000000704"), ProfileRevision: 1,
		ProfileDigest: kernel.Digest("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"), LifecycleEpoch: 1, ScopeRevision: 1,
	}
	invocationID := uuid("00000000-0000-7000-8000-000000000705")
	invocationPayload := func(id kernel.UUIDv7) json.RawMessage {
		t.Helper()
		payload, marshalErr := json.Marshal(map[string]any{
			"actor_fqn": kernel.ActorFQN("teams::coder-1"), "admission_policy_digest": kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
			"admission_policy_revision": 1, "allowed_terminal_outcomes": []kernel.WorkInvocationState{kernel.InvocationSucceeded, kernel.InvocationFailed},
			"attempt_family": "implementation", "attempt_ordinal": 1, "budget_account_id": budget.ID,
			"condition_digest": kernel.Digest("cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"),
			"deadline_at":      "2026-09-01T00:00:00Z", "effect_policy_digest": kernel.Digest("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"),
			"execution_id": uuid("00000000-0000-7000-8000-000000000706"), "expected_budget_revision": 1, "expected_task_revision": 1,
			"fencing_epoch": 1, "global_debit_ordinal": 1, "idempotency_key": "invocation-1", "invocation_id": id,
			"lifecycle_epoch": 1, "model_profile_digest": kernel.Digest("eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"),
			"output_predicate_digest": kernel.Digest("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"),
			"parent_event_id":         uuid("00000000-0000-7000-8000-000000000707"), "purpose": kernel.PurposeImplementation,
			"purpose_debit_ordinal": 1, "qualified_assignment_id": uuid("00000000-0000-7000-8000-000000000708"),
			"remaining_global_budget": 2, "remaining_purpose_budget": 1, "retry_of_invocation_id": nil, "retry_ordinal": 0,
			"runtime_identity_digest": kernel.Digest("1111111111111111111111111111111111111111111111111111111111111111"),
			"scope_revision":          1, "task_id": task.ID, "tool_policy_digest": kernel.Digest("2222222222222222222222222222222222222222222222222222222222222222"),
			"work_profile": profile, "workspace_id": "workspace-1",
		})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return payload
	}
	invocation := kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: invocationID}
	authorize := releaseProjectionDecision(t, 72, invocation, "tekroo.command.work-invocation.authorize", "tekroo.event.work-invocation.authorized", 1, invocationPayload(invocationID))
	authorize.Outbox[0].Kind = "WORK_INVOCATION_AUTHORIZED"
	authorize.WorkBudget = &kernel.WorkBudgetDebitDecision{
		Account: budget, Task: task, Purpose: kernel.PurposeImplementation, ExpectedAccountRevision: 1,
		ExpectedGlobalUsed: 0, ExpectedGlobalPurposeUsed: 0, ExpectedTaskUsed: 0, ExpectedTaskPurposeUsed: 0,
		NextGlobalUsed: 1, NextGlobalPurposeUsed: 1, NextTaskUsed: 1, NextTaskPurposeUsed: 1,
	}
	authorize = attachProvenance(t, authorize)
	if err := store.Commit(context.Background(), kernel.Snapshot{}, authorize); err != nil {
		t.Fatalf("authorize invocation: %v", err)
	}
	account, found := store.WorkBudgetAccount(budget)
	if !found || account.ModelInvocationsUsed != 1 || account.PurposeUsed[kernel.PurposeImplementation] != 1 {
		t.Fatalf("budget after debit = %#v, found=%t", account, found)
	}
	projected, found := store.WorkInvocation(invocation)
	if !found || projected.State != kernel.InvocationAuthorized || projected.GlobalDebitOrdinal != 1 {
		t.Fatalf("invocation projection = %#v, found=%t", projected, found)
	}

	secondID := uuid("00000000-0000-7000-8000-000000000709")
	stale := releaseProjectionDecision(t, 73, kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: secondID}, "tekroo.command.work-invocation.authorize", "tekroo.event.work-invocation.authorized", 1, invocationPayload(secondID))
	stale.WorkBudget = authorize.WorkBudget
	stale = attachProvenance(t, stale)
	if err := store.Commit(context.Background(), kernel.Snapshot{}, stale); !errors.Is(err, memory.ErrConflict) {
		t.Fatalf("stale competing debit error = %v, want ErrConflict", err)
	}
	account, _ = store.WorkBudgetAccount(budget)
	if account.ModelInvocationsUsed != 1 {
		t.Fatalf("stale debit changed usage to %d", account.ModelInvocationsUsed)
	}
}

func TestStoreProjectsModelCapabilityState(t *testing.T) {
	store := memory.NewStore()
	task := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: uuid("00000000-0000-7000-8000-000000000801")}
	profile := kernel.WorkRiskProfile{
		TaskID: task.ID, ProfileID: "00000000-0000-7000-8000-000000000802", ProfileRevision: 1,
		ProfileDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", LifecycleEpoch: 1, ScopeRevision: 1,
		WorkKind: kernel.WorkImplementation, Ambiguity: kernel.AmbiguityLow, Novelty: kernel.NoveltyRoutine, BlastRadius: kernel.BlastLocal, SecuritySensitivity: kernel.SecurityOrdinary,
		MinimumDecisionRoute: kernel.RouteBoundedExecution, AcceptanceCriteriaDigest: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		RequiredDeterministicGateIDs: []string{"go-test"}, RequiredValidationBranches: 1,
		RequiredIndependenceDimensions: []kernel.IndependenceDimension{kernel.IndependenceActor, kernel.IndependenceExecution, kernel.IndependenceContext, kernel.IndependenceWorkspace, kernel.IndependenceMethod},
		ImplementationVariantCount:     1, ValidCandidateQuorum: 1, VerificationTopologyDigest: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		ClassificationPolicyRevision: 1, ClassificationPolicyDigest: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		PromotionPolicyRevision: 1, PromotionPolicyDigest: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		Budgets:                 kernel.FiniteWorkBudgets{AttemptLimit: 2, ReviewRoundLimit: 2, PromotionLimit: 1, EscalationLimit: 1, DeadlineAt: time.Date(2026, time.August, 14, 0, 0, 0, 0, time.UTC)},
		ClassificationAuthority: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"}, ClassificationEvidenceIDs: []kernel.UUIDv7{"00000000-0000-7000-8000-000000000806"},
	}
	profilePayload, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	bind := releaseProjectionDecision(t, 81, task, "tekroo.command.task.bind-work-profile", "tekroo.event.task.work-profile-bound", 1, profilePayload)
	if err := store.Commit(context.Background(), kernel.Snapshot{}, bind); err != nil {
		t.Fatal(err)
	}

	snapshot, err := store.Load(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	projectedProfile, found := snapshot.WorkProfiles[task]
	if !found || projectedProfile.Profile.ProfileDigest != profile.ProfileDigest || projectedProfile.BoundEventID != bind.Events[0].EventID {
		t.Fatalf("work profile projection = %#v", projectedProfile)
	}

	authorization := kernel.QualifiedAssignmentAuthorization{
		AssignmentID: "00000000-0000-7000-8000-000000000803", TaskID: task.ID, ExpectedTaskRevision: 1, WorkProfile: profile.Binding(),
		RequiredDecisionRoute: kernel.RouteBoundedExecution, SelectedDecisionRoute: kernel.RouteBoundedExecution, SelectedActorFQN: "teams::coder-1",
		SelectedExecutionID: "00000000-0000-7000-8000-000000000804", SelectedFencingEpoch: 1,
		ModelProfileDigest: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", RuntimeIdentityDigest: "1111111111111111111111111111111111111111111111111111111111111111",
		Qualification:           kernel.AssignmentQualificationReceipt{QualificationID: "00000000-0000-7000-8000-000000000805", QualificationDigest: "2222222222222222222222222222222222222222222222222222222222222222", QualificationCorpusDigest: "3333333333333333333333333333333333333333333333333333333333333333", ModelProfileDigest: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", DecisionRoute: kernel.RouteBoundedExecution, QualifiedRole: "programmer", Status: kernel.QualificationPass, ObservedAt: time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)},
		SelectionPolicyRevision: 1, SelectionPolicyDigest: "4444444444444444444444444444444444444444444444444444444444444444",
		HardConstraintResults: []kernel.HardConstraintResult{{ConstraintID: "data-residency", Outcome: kernel.ConstraintPass, EvidenceIDs: []kernel.UUIDv7{"00000000-0000-7000-8000-000000000806"}}},
		SelectionReasons:      []string{"least-cost qualified profile"}, EvidenceIDs: []kernel.UUIDv7{"00000000-0000-7000-8000-000000000806"}, AuthorizationEventID: "00000000-0000-7000-8000-000000000807",
	}
	authorizationPayload, err := json.Marshal(authorization)
	if err != nil {
		t.Fatal(err)
	}
	authorize := releaseProjectionDecision(t, 82, task, "tekroo.command.task.authorize-qualified-assignment", "tekroo.event.task.qualified-assignment-authorized", 2, authorizationPayload)
	if err := store.Commit(context.Background(), snapshot, authorize); err != nil {
		t.Fatal(err)
	}

	snapshot, err = store.Load(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	projectedAuthorization, found := snapshot.QualifiedAssignments[task]
	if !found || projectedAuthorization.AssignmentID != authorization.AssignmentID || projectedAuthorization.AuthorizationEventID != authorize.Events[0].EventID {
		t.Fatalf("qualified assignment projection = %#v", projectedAuthorization)
	}
}

func TestStoreProjectsVariantGroupAndUniquenessKey(t *testing.T) {
	store := memory.NewStore()
	target := kernel.AggregateRef{Kind: kernel.AggregateVariantGroup, ID: uuid("00000000-0000-7000-8000-000000000807")}
	payload := json.RawMessage(`{"acceptance_manifest_digest":"9999999999999999999999999999999999999999999999999999999999999999","adjudicator":{"id":"principal","kind":"HUMAN"},"base_artifact_digest":"5555555555555555555555555555555555555555555555555555555555555555","candidate_count":2,"comparator":{"id":"teams::reviewer-1","kind":"ACTOR"},"comparison_method_id":"structured-diff-v1","comparison_policy_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","decision_deadline_at":"2026-08-15T00:00:00Z","dependency_lock_digest":"8888888888888888888888888888888888888888888888888888888888888888","evidence_ids":["00000000-0000-7000-8000-000000000806"],"input_evidence_set_digest":"6666666666666666666666666666666666666666666666666666666666666666","materiality_policy_digest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","replacement_budget":1,"required_independence_dimensions":["ACTOR","EXECUTION","CONTEXT","WORKSPACE"],"submission_deadline_at":"2026-08-14T00:00:00Z","task_id":"00000000-0000-7000-8000-000000000801","toolchain_digest":"7777777777777777777777777777777777777777777777777777777777777777","valid_candidate_quorum":2,"variant_group_id":"00000000-0000-7000-8000-000000000807","work_profile":{"lifecycle_epoch":1,"profile_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","profile_id":"00000000-0000-7000-8000-000000000802","profile_revision":1,"scope_revision":1}}`)
	group, err := kernel.VariantGroupFromOpenPayload(payload, "00000000-0000-7000-8000-000000000808")
	if err != nil {
		t.Fatal(err)
	}
	open := releaseProjectionDecision(t, 83, target, "tekroo.command.variant-group.open", "tekroo.event.variant-group.opened", 1, payload)
	open.Guards.AbsentVariantKeys = []kernel.VariantGroupKey{group.Key()}
	if err := store.Commit(context.Background(), kernel.Snapshot{}, open); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Load(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if projected, found := snapshot.VariantGroups[target]; !found || projected.VariantGroupID != target.ID {
		t.Fatalf("variant group projection = %#v", projected)
	}
	if reference, found := snapshot.VariantGroupKeys[group.Key()]; !found || reference != target {
		t.Fatalf("variant key projection = %#v, %t", reference, found)
	}
}

func TestStoreProjectsCompleteReleaseSequenceAndFencesDuplicateStoryPlan(t *testing.T) {
	store := memory.NewStore()
	createPayload := json.RawMessage(`{"author":{"id":"principal-author","kind":"HUMAN"},"author_approval_event_id":"00000000-0000-7000-8000-000000000749","author_approval_revision":1,"base_commit":"1111111111111111111111111111111111111111","base_ref":"main","conflict_policy":"FAIL_NO_IMPROVISATION","contract_manifest":"tekroo.kernel.contracts/0.6.0","evidence_ids":["00000000-0000-7000-8000-000000000750"],"execution_round_limit":2,"expected_qualified_tree":"3333333333333333333333333333333333333333","expected_story_revision":8,"git_version":"git version 2.51.0","manifest_sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","merge_strategy":"FF_ONLY_ORDERED","ordered_merges":[{"change_ref":"refs/heads/story-1","head_commit":"2222222222222222222222222222222222222222","merge_id":"00000000-0000-7000-8000-000000000752","role":"story"}],"plan_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","release_mode":"CODE","release_plan_id":"00000000-0000-7000-8000-000000000751","release_policy_revision":1,"repository_url":"https://example.invalid/tekroo/teams.git","required_profiles":["contract-structure","core-hermetic","mongo-integration","synthesized-merge"],"story_id":"00000000-0000-7000-8000-000000000101","story_lifecycle_epoch":1}`)
	createPayload = currentContractPayload(createPayload)
	target := kernel.AggregateRef{Kind: kernel.AggregateReleasePlan, ID: uuid("00000000-0000-7000-8000-000000000751")}
	create := releaseProjectionDecision(t, 21, target, "tekroo.command.release-plan.create", "tekroo.event.release-plan.created", 1, createPayload)
	key, err := kernel.ReleasePlanKeyFromCreatePayload(createPayload)
	if err != nil {
		t.Fatal(err)
	}
	create.Guards.AbsentReleaseKeys = []kernel.ReleasePlanKey{key}
	if err := store.Commit(context.Background(), kernel.Snapshot{}, create); err != nil {
		t.Fatal(err)
	}

	transitions := []struct {
		command string
		event   string
		payload json.RawMessage
	}{
		{command: "tekroo.command.release-plan.record-qualification", event: "tekroo.event.release-plan.qualification-recorded", payload: json.RawMessage(`{"artifact_digests":["ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"],"contract_manifest":"tekroo.kernel.contracts/0.6.0","dependency_lock_digest":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee","evidence_ids":["00000000-0000-7000-8000-000000000750"],"expected_release_revision":1,"gate_definition_digest":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","manifest_sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","ordered_head_commits":["2222222222222222222222222222222222222222"],"plan_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","qualification_id":"00000000-0000-7000-8000-000000000754","qualified_base_commit":"1111111111111111111111111111111111111111","qualified_tree_digest":"3333333333333333333333333333333333333333","release_plan_id":"00000000-0000-7000-8000-000000000751","required_profiles":["contract-structure","core-hermetic","mongo-integration","synthesized-merge"],"toolchain_digest":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}`)},
		{command: "tekroo.command.release-plan.request-execution", event: "tekroo.event.release-plan.execution-requested", payload: json.RawMessage(`{"attempt_id":"00000000-0000-7000-8000-000000000755","evidence_ids":["00000000-0000-7000-8000-000000000750"],"expected_release_revision":2,"merge_id":"00000000-0000-7000-8000-000000000752","plan_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","provider_idempotency_key":"release-751-merge-752-round-1","release_plan_id":"00000000-0000-7000-8000-000000000751","round":1}`)},
		{command: "tekroo.command.release-plan.record-result", event: "tekroo.event.release-plan.result-recorded", payload: json.RawMessage(`{"attempt_id":"00000000-0000-7000-8000-000000000755","evidence_ids":["00000000-0000-7000-8000-000000000756"],"expected_release_revision":3,"merge_id":"00000000-0000-7000-8000-000000000752","observed_at":"2026-08-11T12:00:00Z","outcome":"UNKNOWN","plan_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","reasons":["provider response was not terminal"],"release_plan_id":"00000000-0000-7000-8000-000000000751"}`)},
		{command: "tekroo.command.release-plan.record-reconciliation", event: "tekroo.event.release-plan.reconciliation-recorded", payload: nil},
		{command: "tekroo.command.release-plan.finalize", event: "tekroo.event.release-plan.finalized", payload: nil},
	}
	for index := 0; index < 3; index++ {
		transitions[index].payload = currentContractPayload(transitions[index].payload)
		snapshot, loadErr := store.Load(context.Background(), target)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		decision := releaseProjectionDecision(t, 22+index, target, transitions[index].command, transitions[index].event, uint64(index+2), transitions[index].payload)
		if err := store.Commit(context.Background(), snapshot, decision); err != nil {
			t.Fatalf("%s: %v", transitions[index].event, err)
		}
	}

	snapshot, err := store.Load(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	unknown := snapshot.ReleasePlans[target]
	priorResultID := unknown.Results[unknown.OrderedMerges[0].MergeID].ResultEventID
	transitions[3].payload = json.RawMessage(fmt.Sprintf(`{"attempt_id":"00000000-0000-7000-8000-000000000755","evidence_ids":["00000000-0000-7000-8000-000000000756"],"expected_release_revision":4,"merge_id":"00000000-0000-7000-8000-000000000752","observed_at":"2026-08-11T12:01:00Z","observed_base_commit":"1111111111111111111111111111111111111111","observed_head_commit":"2222222222222222222222222222222222222222","observed_tree_digest":"3333333333333333333333333333333333333333","outcome":"MERGED","plan_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","provider_state":"MERGED","reasons":["provider reports exact planned head merged"],"reconciliation_id":"00000000-0000-7000-8000-000000000759","release_plan_id":"00000000-0000-7000-8000-000000000751","supersedes_result_event_id":"%s"}`, priorResultID))
	reconciliation := releaseProjectionDecision(t, 25, target, transitions[3].command, transitions[3].event, 5, transitions[3].payload)
	if err := store.Commit(context.Background(), snapshot, reconciliation); err != nil {
		t.Fatal(err)
	}

	snapshot, _ = store.Load(context.Background(), target)
	plan := snapshot.ReleasePlans[target]
	result := plan.Results[plan.OrderedMerges[0].MergeID]
	transitions[4].payload = json.RawMessage(fmt.Sprintf(`{"evidence_ids":["00000000-0000-7000-8000-000000000750"],"expected_release_revision":5,"plan_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","provider_tree_digest":"3333333333333333333333333333333333333333","qualification_event_id":"%s","qualified_tree_digest":"3333333333333333333333333333333333333333","reasons":["all planned merges and the provider tree are verified"],"release_mode":"CODE","release_plan_id":"00000000-0000-7000-8000-000000000751","result_event_ids":["%s"],"terminal_status":"READY_FOR_ACCEPTANCE"}`, plan.Qualification.EventID, result.ReconciliationEventID))
	wrongFinalPayload := json.RawMessage(strings.ReplaceAll(string(transitions[4].payload), string(result.ReconciliationEventID), "00000000-0000-7000-8000-000000000799"))
	wrongFinalization := releaseProjectionDecision(t, 28, target, transitions[4].command, transitions[4].event, 6, wrongFinalPayload)
	if err := store.Commit(context.Background(), snapshot, wrongFinalization); !errors.Is(err, memory.ErrInvalidDecision) {
		t.Fatalf("unverified finalization error = %v, want ErrInvalidDecision", err)
	}
	finalization := releaseProjectionDecision(t, 26, target, transitions[4].command, transitions[4].event, 6, transitions[4].payload)
	if err := store.Commit(context.Background(), snapshot, finalization); err != nil {
		t.Fatal(err)
	}

	final, _ := store.Load(context.Background(), target)
	if projected := final.ReleasePlans[target]; projected.State != kernel.ReleaseReadyForAcceptance || projected.Revision != 6 || projected.ProviderTreeDigest != projected.ExpectedQualifiedTree || projected.NextMergeIndex != 1 {
		t.Fatalf("final release projection = %#v", projected)
	}

	duplicatePayload := json.RawMessage(strings.ReplaceAll(string(createPayload), "00000000-0000-7000-8000-000000000751", "00000000-0000-7000-8000-000000000771"))
	duplicateTarget := kernel.AggregateRef{Kind: kernel.AggregateReleasePlan, ID: uuid("00000000-0000-7000-8000-000000000771")}
	duplicate := releaseProjectionDecision(t, 27, duplicateTarget, "tekroo.command.release-plan.create", "tekroo.event.release-plan.created", 1, duplicatePayload)
	duplicate.Guards.AbsentReleaseKeys = []kernel.ReleasePlanKey{key}
	if err := store.Commit(context.Background(), kernel.Snapshot{}, duplicate); !errors.Is(err, memory.ErrConflict) {
		t.Fatalf("duplicate story release error = %v, want ErrConflict", err)
	}
}

func TestStoreRoundTripsAndClonesOperatorHumanContinuityState(t *testing.T) {
	store := memory.NewStore()
	target := kernel.AggregateRef{Kind: kernel.AggregateSystem, ID: uuid("00000000-0000-7000-8000-000000000931")}
	operator := kernel.OperatorRoleProfile{BindingID: uuid("00000000-0000-7000-8000-000000000932"), CapabilityIDs: []string{"coordinate"}}
	participant := kernel.HumanParticipantSnapshot{Participant: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "human:alice"}, Revision: 1, RoleBindings: []kernel.HumanRoleBinding{{RoleBindingID: uuid("00000000-0000-7000-8000-000000000933")}}, Active: true}
	interaction := kernel.HumanInteractionSnapshot{InteractionID: uuid("00000000-0000-7000-8000-000000000934"), Revision: 1, Recipients: []kernel.HumanInteractionRecipient{{Principal: participant.Participant}}, ResponsePolicy: kernel.HumanResponsePolicy{Kind: kernel.HumanResponseExactOne}}
	continuity := kernel.TeamContinuitySnapshot{Revision: 1, OperatingPosture: "CONTINUOUS", ControlState: kernel.ContinuityActive, PowerEpoch: 7, AdmissionOpen: true, InFlightExecutionIDs: []kernel.UUIDv7{uuid("00000000-0000-7000-8000-000000000935")}, LastTransitionEventID: uuid("00000000-0000-7000-8000-000000000936")}
	state := kernel.AggregateState{Kind: target.Kind, ID: target.ID, Revision: 1, LifecycleEpoch: 1, ScopeRevision: 1, Phase: kernel.PhaseDraft, Condition: kernel.ConditionRunnable, OperatorRole: &operator, Participant: &participant, Interaction: &interaction, Continuity: &continuity}
	decision := alternateDecision(t, 93)
	decision.NextState = &state
	decision.Receipt.Target = target
	decision.Receipt.CommandType = "tekroo.command.system.configure-continuity"
	revision := uint64(1)
	decision.Receipt.ResultingRevision = &revision
	decision.Events[0].Aggregate = target
	decision.Events[0].AggregateRevision = revision
	decision.Events[0].EventType = "tekroo.event.system.continuity-configured"
	decision = attachProvenance(t, decision)
	if err := store.Commit(context.Background(), kernel.Snapshot{}, decision); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(context.Background(), target)
	if err != nil || loaded.State == nil || !reflect.DeepEqual(*loaded.State, state) {
		t.Fatalf("round trip state=%#v err=%v", loaded.State, err)
	}
	loaded.State.Continuity.InFlightExecutionIDs[0] = uuid("00000000-0000-7000-8000-000000000937")
	reloaded, err := store.Load(context.Background(), target)
	if err != nil || reloaded.State == nil || !reflect.DeepEqual(*reloaded.State, state) {
		t.Fatalf("stored state aliased loaded mutation: %#v err=%v", reloaded.State, err)
	}
}

func TestStoreHonorsCancelledContext(t *testing.T) {
	store := memory.NewStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := store.Load(ctx, kernel.AggregateRef{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("load error = %v, want context.Canceled", err)
	}
}

func TestStoreFaultScheduleIsAllOrNone(t *testing.T) {
	precommitFaults := []memory.FaultPoint{
		memory.FaultBeforeState,
		memory.FaultBeforeEvents,
		memory.FaultBeforeReceipt,
		memory.FaultBeforeAuthority,
		memory.FaultBeforeOutbox,
	}
	for _, point := range precommitFaults {
		t.Run(string(point), func(t *testing.T) {
			store := memory.NewStoreWithFault(point)
			decision := completeDecision(t)
			if err := store.Commit(context.Background(), kernel.Snapshot{}, decision); !errors.Is(err, memory.ErrInjectedFault) {
				t.Fatalf("fault error = %v, want ErrInjectedFault", err)
			}
			states, events, receipts, authority, outbox := store.AtomicRecordCounts()
			if states != 0 || events != 0 || receipts != 0 || authority != 0 || outbox != 0 {
				t.Fatalf("partial commit at %s: state=%d events=%d receipts=%d authority=%d outbox=%d", point, states, events, receipts, authority, outbox)
			}
		})
	}

	store := memory.NewStoreWithFault(memory.FaultAfterCommitBeforeAck)
	decision := completeDecision(t)
	if err := store.Commit(context.Background(), kernel.Snapshot{}, decision); !errors.Is(err, kernel.ErrCommitUncertain) {
		t.Fatalf("post-commit error = %v, want ErrCommitUncertain", err)
	}
	states, events, receipts, authority, outbox := store.AtomicRecordCounts()
	if states != 1 || events != 1 || receipts != 1 || authority != 1 || outbox != 1 {
		t.Fatalf("uncertain commit tuple: state=%d events=%d receipts=%d authority=%d outbox=%d", states, events, receipts, authority, outbox)
	}
}

func TestStoreRejectsIncompleteOrMismatchedDecisionProvenance(t *testing.T) {
	decision := completeDecision(t)
	decision.Events[0].ProvenanceDigest = kernel.Digest("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
	store := memory.NewStore()
	if err := store.Commit(context.Background(), kernel.Snapshot{}, decision); !errors.Is(err, memory.ErrInvalidDecision) {
		t.Fatalf("mismatched provenance error = %v, want ErrInvalidDecision", err)
	}
	decision = completeDecision(t)
	decision.Provenance = kernel.DecisionProvenance{}
	if err := store.Commit(context.Background(), kernel.Snapshot{}, decision); !errors.Is(err, memory.ErrInvalidDecision) {
		t.Fatalf("missing provenance error = %v, want ErrInvalidDecision", err)
	}
}

func TestStoreRechecksExecutionFenceAtCommit(t *testing.T) {
	store := memory.NewStore()
	actor := kernel.ActorFQN("teams::coder-1")
	registered := executionDecision(t, actor, "00000000-0000-7000-8000-000000000011", 1)
	if err := store.Commit(context.Background(), kernel.Snapshot{}, registered); err != nil {
		t.Fatal(err)
	}
	target := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: uuid("00000000-0000-7000-8000-000000000021")}
	stale := completeDecision(t)
	stale.Receipt.Target = target
	stale.NextState.Kind = kernel.AggregateTask
	stale.NextState.ID = target.ID
	stale.Events[0].Aggregate = target
	stale.Guards.Executions = map[kernel.ActorFQN]kernel.ExecutionTuple{
		actor: {ExecutionID: uuid("00000000-0000-7000-8000-000000000099"), FencingEpoch: 1},
	}
	if err := store.Commit(context.Background(), kernel.Snapshot{}, stale); !errors.Is(err, memory.ErrConflict) {
		t.Fatalf("stale fence commit error = %v, want ErrConflict", err)
	}
}

func TestStoreRechecksRelatedAggregateAndPolicyGuardsAtomically(t *testing.T) {
	store := memory.NewStore()
	first := completeDecision(t)
	if err := store.Commit(context.Background(), kernel.Snapshot{}, first); err != nil {
		t.Fatal(err)
	}
	related := first.Receipt.Target
	target := kernel.AggregateRef{Kind: kernel.AggregateStory, ID: uuid("00000000-0000-7000-8000-0000000000b1")}
	candidate := alternateDecision(t, 51)
	candidate.Receipt.Target = target
	candidate.NextState.ID = target.ID
	candidate.Events[0].Aggregate = target
	candidate.Guards.Preconditions = []kernel.AggregatePrecondition{{Aggregate: related, Expected: kernel.NewExpectedRevision(1)}}
	candidate = attachProvenance(t, candidate)
	expected, err := store.LoadDecision(context.Background(), kernel.KernelCommand{
		Target: target, Preconditions: candidate.Guards.Preconditions,
	})
	if err != nil {
		t.Fatal(err)
	}

	updated := alternateDecision(t, 52)
	updated.Receipt.Target = related
	updated.NextState.ID = related.ID
	updated.NextState.Revision = 2
	updated.Events[0].Aggregate = related
	updated.Events[0].AggregateRevision = 2
	revision := uint64(2)
	updated.Receipt.ResultingRevision = &revision
	updated = attachProvenance(t, updated)
	if err := store.Commit(context.Background(), kernel.Snapshot{Exists: true, Revision: 1, State: first.NextState}, updated); err != nil {
		t.Fatal(err)
	}
	if err := store.Commit(context.Background(), expected, candidate); !errors.Is(err, memory.ErrConflict) {
		t.Fatalf("related guard error = %v, want ErrConflict", err)
	}
	if snapshot, err := store.Load(context.Background(), target); err != nil || snapshot.Exists {
		t.Fatalf("multi-aggregate loser changed target: %#v, %v", snapshot, err)
	}

	basis, err := fake.ProvenanceBasis()
	if err != nil {
		t.Fatal(err)
	}
	policyStore := memory.NewStore(kernel.AuthorizationPolicy{PolicyDigest: basis.PolicyDigest, Revision: basis.PolicyRevision})
	guarded := completeDecision(t)
	guarded.Authority.PolicyDigest = basis.PolicyDigest
	guarded.Authority.PolicyRevision = basis.PolicyRevision
	guarded.Authority.GrantDigests = append([]kernel.Digest(nil), basis.GrantDigests...)
	guarded.Guards.PolicyDigest = basis.PolicyDigest
	guarded.Guards.PolicyRevision = basis.PolicyRevision
	policyStore.SetAuthorizationPolicy(kernel.AuthorizationPolicy{PolicyDigest: basis.PolicyDigest, Revision: basis.PolicyRevision + 1})
	if err := policyStore.Commit(context.Background(), kernel.Snapshot{}, guarded); !errors.Is(err, memory.ErrConflict) {
		t.Fatalf("policy guard error = %v, want ErrConflict", err)
	}
}

func TestStorePersistsAttemptBudgetWithTheAtomicDecision(t *testing.T) {
	store := memory.NewStore()
	first := completeDecision(t)
	first.AttemptBudget = &kernel.AttemptBudgetDecision{
		Key:   kernel.AttemptBudgetKey{Subject: first.Receipt.Target, Operation: first.Receipt.CommandType},
		Limit: 2, AttemptKey: string(first.Receipt.CommandID),
		ConditionDigest: kernel.Digest("1111111111111111111111111111111111111111111111111111111111111111"),
	}
	if err := store.Commit(context.Background(), kernel.Snapshot{}, first); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.LoadDecision(context.Background(), kernel.KernelCommand{Target: first.Receipt.Target})
	if err != nil {
		t.Fatal(err)
	}
	budget := snapshot.AttemptBudgets[first.AttemptBudget.Key]
	if budget.Used != 1 || budget.Limit != 2 || budget.Attempts[first.AttemptBudget.AttemptKey] != first.AttemptBudget.ConditionDigest {
		t.Fatalf("persisted budget = %#v", budget)
	}

	second := alternateDecision(t, 61)
	second.Receipt.Target = first.Receipt.Target
	second.NextState.ID = first.Receipt.Target.ID
	second.NextState.Revision = 2
	second.Events[0].Aggregate = first.Receipt.Target
	second.Events[0].AggregateRevision = 2
	revision := uint64(2)
	second.Receipt.ResultingRevision = &revision
	second.AttemptBudget = &kernel.AttemptBudgetDecision{
		Key: first.AttemptBudget.Key, ExpectedUsed: 1, Limit: 2,
		AttemptKey:      string(second.Receipt.CommandID),
		ConditionDigest: kernel.Digest("2222222222222222222222222222222222222222222222222222222222222222"),
	}
	second = attachProvenance(t, second)
	if err := store.Commit(context.Background(), snapshot, second); err != nil {
		t.Fatal(err)
	}
	final, err := store.LoadDecision(context.Background(), kernel.KernelCommand{Target: first.Receipt.Target})
	if err != nil {
		t.Fatal(err)
	}
	if final.AttemptBudgets[first.AttemptBudget.Key].Used != 2 {
		t.Fatalf("final budget = %#v", final.AttemptBudgets[first.AttemptBudget.Key])
	}
}

func TestStoreAllowsOnlyOneCompletionReviewPerSemanticKey(t *testing.T) {
	payload := json.RawMessage(`{"subject_kind":"story","subject_id":"00000000-0000-7000-8000-0000000000c1","lifecycle_epoch":1,"criteria_revision":2,"evidence_set_digest":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","branch_policy_revision":1,"required_branch_ids":["review","tests"],"join_rule":"ALL_PASS","partial_result_policy":"WAIT_ALL"}`)
	key, err := kernel.CompletionReviewKeyFromPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	first := alternateDecision(t, 71)
	first.NextState = nil
	first.Receipt.Target = kernel.AggregateRef{Kind: kernel.AggregateCompletionReview, ID: uuid("00000000-0000-7000-8000-0000000000c2")}
	first.Events[0].Aggregate = first.Receipt.Target
	first.Events[0].EventType = "tekroo.event.completion-review.opened"
	first.Events[0].Payload = payload
	first.Guards.AbsentReviewKeys = []kernel.CompletionReviewKey{key}
	first = attachProvenance(t, first)
	store := memory.NewStore()
	if err := store.Commit(context.Background(), kernel.Snapshot{}, first); err != nil {
		t.Fatal(err)
	}
	second := alternateDecision(t, 72)
	second.NextState = nil
	second.Receipt.Target = kernel.AggregateRef{Kind: kernel.AggregateCompletionReview, ID: uuid("00000000-0000-7000-8000-0000000000c3")}
	second.Events[0].Aggregate = second.Receipt.Target
	second.Events[0].EventType = "tekroo.event.completion-review.opened"
	second.Events[0].Payload = payload
	second.Guards.AbsentReviewKeys = []kernel.CompletionReviewKey{key}
	second = attachProvenance(t, second)
	if err := store.Commit(context.Background(), kernel.Snapshot{}, second); !errors.Is(err, memory.ErrConflict) {
		t.Fatalf("duplicate semantic review error = %v, want ErrConflict", err)
	}
}

func TestStorePersistsOrderIndependentCompletionReviewJoin(t *testing.T) {
	target := kernel.AggregateRef{Kind: kernel.AggregateCompletionReview, ID: uuid("00000000-0000-7000-8000-0000000000d1")}
	openPayload := json.RawMessage(`{"subject_kind":"story","subject_id":"00000000-0000-7000-8000-0000000000d2","lifecycle_epoch":1,"criteria_revision":2,"evidence_set_digest":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd","branch_policy_revision":4,"required_branch_ids":["review","tests"],"join_rule":"ALL_PASS","partial_result_policy":"WAIT_ALL"}`)
	open := alternateDecision(t, 81)
	open.NextState = nil
	open.Receipt.Target = target
	open.Events[0].Aggregate = target
	open.Events[0].EventType = "tekroo.event.completion-review.opened"
	open.Events[0].Payload = openPayload
	open = attachProvenance(t, open)
	store := memory.NewStore()
	if err := store.Commit(context.Background(), kernel.Snapshot{}, open); err != nil {
		t.Fatal(err)
	}

	commitResult := func(ordinal int, branch string, revision uint64) kernel.UUIDv7 {
		t.Helper()
		snapshot, err := store.LoadDecision(context.Background(), kernel.KernelCommand{Target: target})
		if err != nil {
			t.Fatal(err)
		}
		decision := alternateDecision(t, ordinal)
		decision.NextState = nil
		decision.Receipt.Target = target
		decision.Receipt.ResultingRevision = &revision
		decision.Events[0].Aggregate = target
		decision.Events[0].AggregateRevision = revision
		decision.Events[0].EventType = "tekroo.event.completion-review.result-recorded"
		decision.Events[0].Payload = json.RawMessage(fmt.Sprintf(`{"review_id":"%s","branch_id":"%s","branch_policy_revision":4,"result":"PASS","reasons":[],"evidence_ids":["00000000-0000-7000-8000-0000000000d3"]}`, target.ID, branch))
		decision = attachProvenance(t, decision)
		if err := store.Commit(context.Background(), snapshot, decision); err != nil {
			t.Fatal(err)
		}
		return decision.Events[0].EventID
	}

	firstResult := commitResult(82, "tests", 2)
	secondResult := commitResult(83, "review", 3)
	final, err := store.LoadDecision(context.Background(), kernel.KernelCommand{Target: target})
	if err != nil {
		t.Fatal(err)
	}
	if final.Reviews[target].ReviewID != target.ID || final.Reviews[target].Join != (kernel.ReviewJoinResult{Complete: true, Status: "PASS"}) {
		t.Fatalf("final join = %#v", final.Reviews[target].Join)
	}
	if final.AcceptedEvents[firstResult].Qualification != "PENDING" || final.AcceptedEvents[secondResult].Qualification != "PASS" {
		t.Fatalf("result qualifications = %#v %#v", final.AcceptedEvents[firstResult], final.AcceptedEvents[secondResult])
	}
}

func TestStorePersistsEscalationOpeningAndTerminalResolution(t *testing.T) {
	store := memory.NewStore()
	opening := escalationOpeningDecision(t, 91, "00000000-0000-7000-8000-000000000901")
	if err := store.Commit(context.Background(), kernel.Snapshot{}, opening); err != nil {
		t.Fatal(err)
	}
	target := opening.Receipt.Target
	snapshot, err := store.LoadDecision(context.Background(), kernel.KernelCommand{Target: target})
	if err != nil {
		t.Fatal(err)
	}
	projected, found := snapshot.Escalations[target]
	if !found || projected.State != kernel.EscalationOpen || projected.OpeningEventID != opening.Events[0].EventID || snapshot.EscalationKeys[projected.Key()] != target {
		t.Fatalf("opening projection = %#v, keys = %#v", projected, snapshot.EscalationKeys)
	}

	resolution := escalationResolutionDecision(t, 92, projected)
	if err := store.Commit(context.Background(), snapshot, resolution); err != nil {
		t.Fatal(err)
	}
	final, err := store.LoadDecision(context.Background(), kernel.KernelCommand{Target: target})
	if err != nil {
		t.Fatal(err)
	}
	terminal := final.Escalations[target]
	if terminal.State != kernel.EscalationTerminal || terminal.TerminalOutcome != kernel.EscalationResolved || terminal.ResolutionEventID != resolution.Events[0].EventID || terminal.Revision != 2 {
		t.Fatalf("terminal projection = %#v", terminal)
	}
	if final.EscalationKeys[terminal.Key()] != target {
		t.Fatalf("terminal semantic key missing = %#v", final.EscalationKeys)
	}
}

func TestStoreAllowsOneConcurrentEscalationPerSemanticKey(t *testing.T) {
	store := memory.NewStore()
	const contenders = 16
	start := make(chan struct{})
	results := make(chan error, contenders)
	var ready sync.WaitGroup
	ready.Add(contenders)
	for index := 0; index < contenders; index++ {
		decision := escalationOpeningDecision(t, 100+index, fmt.Sprintf("00000000-0000-7000-8000-%012x", 0x920+index))
		go func(candidate kernel.Decision) {
			ready.Done()
			<-start
			results <- store.Commit(context.Background(), kernel.Snapshot{}, candidate)
		}(decision)
	}
	ready.Wait()
	close(start)
	winners := 0
	for index := 0; index < contenders; index++ {
		err := <-results
		if err == nil {
			winners++
		} else if !errors.Is(err, memory.ErrConflict) {
			t.Fatalf("loser error = %v, want ErrConflict", err)
		}
	}
	if winners != 1 || store.EventCount() != 1 {
		t.Fatalf("winners=%d events=%d", winners, store.EventCount())
	}
}

func TestStoreSystematicAndConcurrentOneWinnerSchedules(t *testing.T) {
	first := completeDecision(t)
	second := alternateDecision(t, 2)
	orders := [][2]kernel.Decision{{first, second}, {second, first}}
	for index, order := range orders {
		store := memory.NewStore()
		if err := store.Commit(context.Background(), kernel.Snapshot{}, order[0]); err != nil {
			t.Fatalf("schedule %d first commit: %v", index, err)
		}
		if err := store.Commit(context.Background(), kernel.Snapshot{}, order[1]); !errors.Is(err, memory.ErrConflict) {
			t.Fatalf("schedule %d second commit = %v, want ErrConflict", index, err)
		}
		_, events, receipts, authority, outbox := store.AtomicRecordCounts()
		if events != 1 || receipts != 1 || authority != 1 || outbox != 1 {
			t.Fatalf("schedule %d tuple events=%d receipts=%d authority=%d outbox=%d", index, events, receipts, authority, outbox)
		}
	}

	store := memory.NewStore()
	const contenders = 32
	start := make(chan struct{})
	results := make(chan error, contenders)
	var ready sync.WaitGroup
	ready.Add(contenders)
	for index := 1; index <= contenders; index++ {
		decision := alternateDecision(t, index)
		go func() {
			ready.Done()
			<-start
			results <- store.Commit(context.Background(), kernel.Snapshot{}, decision)
		}()
	}
	ready.Wait()
	close(start)
	winners := 0
	for index := 0; index < contenders; index++ {
		err := <-results
		if err == nil {
			winners++
		} else if !errors.Is(err, memory.ErrConflict) {
			t.Fatalf("concurrent loser error = %v, want ErrConflict", err)
		}
	}
	if winners != 1 || store.EventCount() != 1 {
		t.Fatalf("concurrent winners=%d events=%d", winners, store.EventCount())
	}
}

func completeDecision(t *testing.T) kernel.Decision {
	t.Helper()
	target := kernel.AggregateRef{Kind: kernel.AggregateStory, ID: uuid("00000000-0000-7000-8000-000000000001")}
	commandID := uuid("00000000-0000-7000-8000-000000000002")
	eventID := uuid("00000000-0000-7000-8000-000000000003")
	revision := uint64(1)
	decision := kernel.Decision{
		CommandFingerprint: kernel.Digest("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"),
		IdempotencyScope:   kernel.Digest("cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"),
		NextState: &kernel.AggregateState{
			Kind: kernel.AggregateStory, ID: target.ID, Revision: 1,
			LifecycleEpoch: 1, Phase: kernel.PhaseDraft, Condition: kernel.ConditionRunnable,
		},
		Events: []kernel.DomainEvent{{EventID: eventID, EventType: "tekroo.event.story.created", Aggregate: target, AggregateRevision: 1, LifecycleEpoch: 1}},
		Receipt: kernel.CommandReceipt{
			CommandID: commandID, Target: target, OutcomeCode: kernel.OutcomeApplied,
			ResultingRevision: &revision, EventIDs: []kernel.UUIDv7{eventID},
			ProvenanceDigest: kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		},
		Authority: kernel.AuthorityDecision{Principal: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"}, Allowed: true, Reason: "APPLIED"},
		Outbox:    []kernel.OutboxIntent{{IntentID: uuid("00000000-0000-7000-8000-000000000004"), EventID: eventID, Kind: "DOMAIN_EVENT"}},
	}
	return attachProvenance(t, decision)
}

func releaseProjectionDecision(t *testing.T, ordinal int, target kernel.AggregateRef, commandType, eventType string, revision uint64, payload json.RawMessage) kernel.Decision {
	t.Helper()
	decision := alternateDecision(t, ordinal)
	decision.NextState = nil
	decision.Receipt.Target = target
	decision.Receipt.CommandType = commandType
	decision.Receipt.ResultingRevision = &revision
	decision.Events[0].Aggregate = target
	decision.Events[0].EventType = eventType
	decision.Events[0].AggregateRevision = revision
	decision.Events[0].CommittedAt = time.Date(2026, time.August, 11, 12, 0, ordinal, 0, time.UTC)
	decision.Events[0].Payload = append(json.RawMessage(nil), payload...)
	return attachProvenance(t, decision)
}

func executionDecision(t *testing.T, actor kernel.ActorFQN, executionID string, epoch uint64) kernel.Decision {
	t.Helper()
	target := kernel.AggregateRef{Kind: kernel.AggregateExecution, ID: uuid(executionID)}
	commandID := uuid("00000000-0000-7000-8000-000000000012")
	eventID := uuid("00000000-0000-7000-8000-000000000013")
	revision := uint64(1)
	payload, err := json.Marshal(struct {
		ActorFQN        kernel.ActorFQN `json:"actor_fqn"`
		ExecutionID     kernel.UUIDv7   `json:"execution_id"`
		FencingEpoch    uint64          `json:"fencing_epoch"`
		RuntimeIdentity kernel.Digest   `json:"runtime_identity"`
	}{actor, uuid(executionID), epoch, kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")})
	if err != nil {
		t.Fatal(err)
	}
	decision := kernel.Decision{
		CommandFingerprint: kernel.Digest("eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"),
		IdempotencyScope:   kernel.Digest("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"),
		Events:             []kernel.DomainEvent{{EventID: eventID, EventType: "tekroo.event.execution.registered", Aggregate: target, AggregateRevision: 1, LifecycleEpoch: 1, Payload: payload}},
		Receipt:            kernel.CommandReceipt{CommandID: commandID, Target: target, OutcomeCode: kernel.OutcomeApplied, ResultingRevision: &revision, EventIDs: []kernel.UUIDv7{eventID}},
		Authority:          kernel.AuthorityDecision{Principal: kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "execution-registry"}, Allowed: true, Reason: "APPLIED"},
		Outbox:             []kernel.OutboxIntent{{IntentID: uuid("00000000-0000-7000-8000-000000000014"), EventID: eventID, Kind: "DOMAIN_EVENT"}},
	}
	return attachProvenance(t, decision)
}

func alternateDecision(t *testing.T, ordinal int) kernel.Decision {
	t.Helper()
	decision := completeDecision(t)
	decision.Receipt.CommandID = uuid(fmt.Sprintf("00000000-0000-7000-8000-%012x", 1000+ordinal))
	decision.Events[0].EventID = uuid(fmt.Sprintf("00000000-0000-7000-8000-%012x", 2000+ordinal))
	decision.Events[0].CommandID = decision.Receipt.CommandID
	decision.Receipt.EventIDs = []kernel.UUIDv7{decision.Events[0].EventID}
	decision.Outbox[0].IntentID = uuid(fmt.Sprintf("00000000-0000-7000-8000-%012x", 3000+ordinal))
	decision.Outbox[0].EventID = decision.Events[0].EventID
	decision.CommandFingerprint = kernel.Digest(fmt.Sprintf("%064x", 4000+ordinal))
	decision.IdempotencyScope = kernel.Digest(fmt.Sprintf("%064x", 5000+ordinal))
	return attachProvenance(t, decision)
}

func escalationOpeningDecision(t *testing.T, ordinal int, escalationID string) kernel.Decision {
	t.Helper()
	payload := json.RawMessage(fmt.Sprintf(`{"adjudicator":{"id":"principal-adjudicator","kind":"HUMAN"},"causal_path_event_ids":["00000000-0000-7000-8000-000000000902"],"deadline_at":"2026-08-12T00:00:00Z","escalation_id":"%s","escalation_policy_revision":1,"evidence_ids":["00000000-0000-7000-8000-000000000903"],"expected_subject_revision":7,"resolution_owner_fqn":"teams::coder-1","resolution_round_limit":1,"route_limit":1,"subject_id":"00000000-0000-7000-8000-000000000904","subject_kind":"task","subject_lifecycle_epoch":1,"timeout_policy":{"id":"escalation-timeout-policy","kind":"POLICY"},"trigger":"HANDOFF_CYCLE_DETECTED","triggering_condition_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","unresolved_question":"Which directed successor resolves the detected handoff cycle?"}`, escalationID))
	escalation, err := kernel.EscalationFromOpenPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	decision := alternateDecision(t, ordinal)
	decision.NextState = nil
	decision.Receipt.Target = kernel.AggregateRef{Kind: kernel.AggregateEscalation, ID: escalation.EscalationID}
	decision.Receipt.CommandType = "tekroo.command.escalation.open"
	decision.Events[0].Aggregate = decision.Receipt.Target
	decision.Events[0].EventType = "tekroo.event.escalation.opened"
	decision.Events[0].Payload = payload
	decision.Events[0].AggregateRevision = 1
	decision.Guards.AbsentEscalationKeys = []kernel.EscalationKey{escalation.Key()}
	return attachProvenance(t, decision)
}

func escalationResolutionDecision(t *testing.T, ordinal int, escalation kernel.EscalationSnapshot) kernel.Decision {
	t.Helper()
	payload := json.RawMessage(fmt.Sprintf(`{"decided_at":"2026-08-11T12:00:00Z","escalation_id":"%s","evidence_ids":["00000000-0000-7000-8000-000000000905"],"expected_escalation_revision":1,"outcome":"RESOLVED","reasons":["A directed successor was selected."],"round":1,"source_role":"ADJUDICATOR","subject_id":"%s","subject_kind":"%s","subject_lifecycle_epoch":%d}`, escalation.EscalationID, escalation.Subject.ID, escalation.Subject.Kind, escalation.SubjectLifecycleEpoch))
	decision := alternateDecision(t, ordinal)
	revision := uint64(2)
	decision.NextState = nil
	decision.Receipt.Target = kernel.AggregateRef{Kind: kernel.AggregateEscalation, ID: escalation.EscalationID}
	decision.Receipt.CommandType = "tekroo.command.escalation.resolve"
	decision.Receipt.ResultingRevision = &revision
	decision.Events[0].Aggregate = decision.Receipt.Target
	decision.Events[0].EventType = "tekroo.event.escalation.resolved"
	decision.Events[0].Payload = payload
	decision.Events[0].AggregateRevision = revision
	decision.Events[0].CommittedAt = time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	decision.Authority.Principal = escalation.Adjudicator
	return attachProvenance(t, decision)
}

func attachProvenance(t *testing.T, decision kernel.Decision) kernel.Decision {
	t.Helper()
	basis, err := fake.ProvenanceBasis()
	if err != nil {
		t.Fatal(err)
	}
	principal := decision.Authority.Principal
	if !principal.Valid() {
		principal = kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"}
		decision.Authority.Principal = principal
	}
	commandType := decision.Receipt.CommandType
	if commandType == "" {
		commandType = "tekroo.command.story.create"
		decision.Receipt.CommandType = commandType
	}
	decision.Receipt.ContractManifest = kernel.ContractIdentity
	command := kernel.KernelCommand{
		ContractManifest: kernel.ContractIdentity,
		CommandID:        decision.Receipt.CommandID,
		CommandType:      commandType,
		Target:           decision.Receipt.Target,
		Authority:        principal,
	}
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	eventID := decision.Receipt.CommandID
	if len(decision.Events) > 0 {
		eventID = decision.Events[0].EventID
	}
	context := kernel.DecisionContext{
		ReceivedAt: now, DecidedAt: now.Add(time.Millisecond), EventID: eventID,
		IntentID: decision.Receipt.CommandID, Provenance: basis,
	}
	provenance, digest, err := kernel.BuildDecisionProvenance(command, decision.CommandFingerprint, context)
	if err != nil {
		t.Fatal(err)
	}
	decision.Provenance = provenance
	decision.Receipt.ProvenanceDigest = digest
	for index := range decision.Events {
		decision.Events[index].ContractManifest = kernel.ContractIdentity
		decision.Events[index].CommandID = decision.Receipt.CommandID
		decision.Events[index].Authority = principal
		decision.Events[index].ProvenanceDigest = digest
	}
	return decision
}

func uuid(value string) kernel.UUIDv7 { return kernel.UUIDv7(value) }

func currentContractPayload(payload json.RawMessage) json.RawMessage {
	return json.RawMessage(strings.ReplaceAll(string(payload), "tekroo.kernel.contracts/0.6.0", kernel.ContractIdentity))
}
