package kernel

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestWorkInvocationAdmissionBindsIdentityAndAtomicallyPlansDebit(t *testing.T) {
	fixture := phase4Fixture(t)
	payload := fixture.authorizationPayload(t, fixture.invocationID, Digest("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"), nil, 0)
	decision := PlanWorkInvocationAuthorization(payload, fixture.task, fixture.account, fixture.binding, fixture.scope, fixture.profile, fixture.assignment, fixture.execution, nil, fixture.now, fixture.eventID)
	if !decision.Accepted || decision.Reason != "ACCEPTED" || decision.BudgetDebit == nil || !decision.BudgetDebit.Valid() {
		t.Fatalf("admission = %#v", decision)
	}
	if decision.Invocation.GlobalDebitOrdinal != 3 || decision.Invocation.PurposeDebitOrdinal != 2 || decision.Invocation.RemainingGlobalBudget != 5 || decision.Invocation.RemainingPurposeBudget != 2 {
		t.Fatalf("debit receipt = %#v", decision.Invocation)
	}
	var eventPayload map[string]any
	if json.Unmarshal(decision.EventPayload, &eventPayload) != nil || eventPayload["global_debit_ordinal"] != float64(3) || eventPayload["remaining_global_budget"] != float64(5) {
		t.Fatalf("event payload = %s", decision.EventPayload)
	}
}

func TestWorkInvocationAdmissionFailsClosedAcrossEveryIdentityAndBudgetBoundary(t *testing.T) {
	fixture := phase4Fixture(t)
	base := fixture.authorizationObject(t, fixture.invocationID, Digest("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"), nil, 0)
	tests := []struct {
		name   string
		mutate func(map[string]any, *phase4TestFixture)
		reason string
	}{
		{"missing budget", func(_ map[string]any, f *phase4TestFixture) { f.account = WorkBudgetAccount{} }, "MISSING_BUDGET"},
		{"stale budget revision", func(v map[string]any, _ *phase4TestFixture) { v["expected_budget_revision"] = float64(2) }, "STALE_BUDGET_REVISION"},
		{"stale task revision", func(v map[string]any, _ *phase4TestFixture) { v["expected_task_revision"] = float64(3) }, "STALE_TASK_REVISION"},
		{"stale lifecycle", func(v map[string]any, _ *phase4TestFixture) { v["lifecycle_epoch"] = float64(2) }, "STALE_LIFECYCLE"},
		{"stale scope", func(v map[string]any, _ *phase4TestFixture) { v["scope_revision"] = float64(2) }, "STALE_SCOPE"},
		{"wrong actor", func(v map[string]any, _ *phase4TestFixture) { v["actor_fqn"] = "teams::coder-2" }, "RUNTIME_IDENTITY_MISMATCH"},
		{"wrong execution", func(v map[string]any, _ *phase4TestFixture) {
			v["execution_id"] = "00000000-0000-7000-8000-000000000999"
		}, "RUNTIME_IDENTITY_MISMATCH"},
		{"wrong fence", func(v map[string]any, _ *phase4TestFixture) { v["fencing_epoch"] = float64(2) }, "RUNTIME_IDENTITY_MISMATCH"},
		{"wrong model", func(v map[string]any, _ *phase4TestFixture) {
			v["model_profile_digest"] = "9999999999999999999999999999999999999999999999999999999999999999"
		}, "RUNTIME_IDENTITY_MISMATCH"},
		{"wrong runtime", func(v map[string]any, _ *phase4TestFixture) {
			v["runtime_identity_digest"] = "9999999999999999999999999999999999999999999999999999999999999999"
		}, "RUNTIME_IDENTITY_MISMATCH"},
		{"wrong workspace", func(v map[string]any, _ *phase4TestFixture) { v["workspace_id"] = "other" }, "RUNTIME_IDENTITY_MISMATCH"},
		{"root exhausted", func(_ map[string]any, f *phase4TestFixture) {
			f.account.ModelInvocationsUsed = f.account.ModelInvocationLimit
		}, "BUDGET_EXHAUSTED"},
		{"purpose exhausted", func(_ map[string]any, f *phase4TestFixture) {
			f.account.PurposeUsed[PurposeImplementation] = f.account.PurposeLimits[PurposeImplementation]
		}, "PURPOSE_BUDGET_EXHAUSTED"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			copyFixture := fixture.clone()
			value := cloneJSONMap(t, base)
			test.mutate(value, &copyFixture)
			payload, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			decision := PlanWorkInvocationAuthorization(payload, copyFixture.task, copyFixture.account, copyFixture.binding, copyFixture.scope, copyFixture.profile, copyFixture.assignment, copyFixture.execution, nil, copyFixture.now, copyFixture.eventID)
			if decision.Accepted || decision.Reason != test.reason {
				t.Fatalf("decision = %#v, want %s", decision, test.reason)
			}
		})
	}
}

func TestUnchangedConditionRequiresExactRetryableTerminal(t *testing.T) {
	fixture := phase4Fixture(t)
	condition := Digest("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd")
	prior := fixture.authorizedInvocation(t, UUIDv7("00000000-0000-7000-8000-000000000950"), condition)
	prior.State = InvocationFailed
	prior.Revision = 4
	retryable := true
	prior.Retryable = &retryable
	invocations := map[AggregateRef]WorkInvocation{prior.Ref(): prior}

	withoutRetry := PlanWorkInvocationAuthorization(fixture.authorizationPayload(t, fixture.invocationID, condition, nil, 0), fixture.task, fixture.account, fixture.binding, fixture.scope, fixture.profile, fixture.assignment, fixture.execution, invocations, fixture.now, fixture.eventID)
	if withoutRetry.Accepted || withoutRetry.Reason != "UNCHANGED_CONDITION" {
		t.Fatalf("unchanged condition = %#v", withoutRetry)
	}
	retry := PlanWorkInvocationAuthorization(fixture.authorizationPayload(t, fixture.invocationID, condition, &prior.ID, 1), fixture.task, fixture.account, fixture.binding, fixture.scope, fixture.profile, fixture.assignment, fixture.execution, invocations, fixture.now, fixture.eventID)
	if !retry.Accepted {
		t.Fatalf("retry = %#v", retry)
	}
	notRetryable := false
	prior.Retryable = &notRetryable
	invocations[prior.Ref()] = prior
	rejected := PlanWorkInvocationAuthorization(fixture.authorizationPayload(t, fixture.invocationID, condition, &prior.ID, 1), fixture.task, fixture.account, fixture.binding, fixture.scope, fixture.profile, fixture.assignment, fixture.execution, invocations, fixture.now, fixture.eventID)
	if rejected.Accepted || rejected.Reason != "UNCHANGED_CONDITION" {
		t.Fatalf("nonretryable = %#v", rejected)
	}
}

func TestChangedConditionAllowsOnlyEvidenceBoundOperatorRecovery(t *testing.T) {
	fixture := phase4Fixture(t)
	priorCondition := Digest("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd")
	prior := fixture.authorizedInvocation(t, UUIDv7("00000000-0000-7000-8000-000000000951"), priorCondition)
	prior.State = InvocationCancelled
	prior.Revision = 5
	cancelledAt := fixture.now.Add(time.Minute)
	prior.CancellationRequestedAt = &cancelledAt
	prior.TerminalEvidenceIDs = append([]UUIDv7(nil), fixture.profile.Profile.ClassificationEvidenceIDs...)
	invocations := map[AggregateRef]WorkInvocation{prior.Ref(): prior}

	priorProfileID := fixture.profile.Profile.ProfileID
	fixture.profile.Profile.ProfileID = UUIDv7("00000000-0000-7000-8000-000000000952")
	fixture.profile.Profile.ProfileRevision++
	fixture.profile.Profile.ProfileDigest = Digest("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
	fixture.profile.Profile.SupersedesProfileID = &priorProfileID
	fixture.profile.Profile.Budgets.DeadlineAt = prior.DeadlineAt.Add(time.Hour)
	fixture.assignment.WorkProfile = fixture.profile.Profile.Binding()
	fixture.account.PolicyRevision++
	fixture.account.PolicyDigest = Digest("eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee")
	fixture.account.DeadlineAt = fixture.profile.Profile.Budgets.DeadlineAt

	nextCondition := Digest("cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")
	value := fixture.authorizationObject(t, fixture.invocationID, nextCondition, &prior.ID, prior.RetryOrdinal+1)
	value["attempt_ordinal"] = float64(prior.AttemptOrdinal + 1)
	value["deadline_at"] = fixture.account.DeadlineAt.Format(time.RFC3339)
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	decision := PlanWorkInvocationAuthorization(payload, fixture.task, fixture.account, fixture.binding, fixture.scope, fixture.profile, fixture.assignment, fixture.execution, invocations, fixture.now, fixture.eventID)
	if !decision.Accepted || decision.Invocation.RetryOfInvocationID == nil || *decision.Invocation.RetryOfInvocationID != prior.ID {
		t.Fatalf("operator recovery = %#v", decision)
	}
	retryable := true
	prior.State = InvocationFailed
	prior.CancellationRequestedAt = nil
	prior.Retryable = &retryable
	invocations[prior.Ref()] = prior
	decision = PlanWorkInvocationAuthorization(payload, fixture.task, fixture.account, fixture.binding, fixture.scope, fixture.profile, fixture.assignment, fixture.execution, invocations, fixture.now, fixture.eventID)
	if !decision.Accepted {
		t.Fatalf("retryable failed operator recovery = %#v", decision)
	}
	notRetryable := false
	prior.Retryable = &notRetryable
	invocations[prior.Ref()] = prior
	rejected := PlanWorkInvocationAuthorization(payload, fixture.task, fixture.account, fixture.binding, fixture.scope, fixture.profile, fixture.assignment, fixture.execution, invocations, fixture.now, fixture.eventID)
	if rejected.Accepted || rejected.Reason != "INVALID_RETRY" {
		t.Fatalf("non-retryable failed recovery = %#v", rejected)
	}
	prior.Retryable = &retryable
	invocations[prior.Ref()] = prior

	fixture.profile.Profile.ClassificationEvidenceIDs = []UUIDv7{"00000000-0000-7000-8000-000000000953"}
	rejected = PlanWorkInvocationAuthorization(payload, fixture.task, fixture.account, fixture.binding, fixture.scope, fixture.profile, fixture.assignment, fixture.execution, invocations, fixture.now, fixture.eventID)
	if rejected.Accepted || rejected.Reason != "INVALID_RETRY" {
		t.Fatalf("recovery without terminal evidence = %#v", rejected)
	}
}

func TestWorkInvocationPermitIsSingleUseAndCancellationIsNotTerminal(t *testing.T) {
	fixture := phase4Fixture(t)
	current := fixture.authorizedInvocation(t, fixture.invocationID, Digest("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"))
	claimPayload := json.RawMessage(fmt.Sprintf(`{"actor_fqn":"teams::coder-1","claim_id":"00000000-0000-7000-8000-000000000960","claimed_at":"2026-08-31T12:01:00Z","consumer_id":"openhands","execution_id":"%s","expected_invocation_revision":1,"fencing_epoch":1,"invocation_id":"%s","model_profile_digest":"%s","runtime_identity_digest":"%s"}`, fixture.execution.ExecutionID, current.ID, current.ModelProfileDigest, current.RuntimeIdentityDigest))
	claimEvent := DomainEvent{EventID: UUIDv7("00000000-0000-7000-8000-000000000961"), EventType: "tekroo.event.work-invocation.claimed", Aggregate: current.Ref(), AggregateRevision: 2, Payload: claimPayload}
	claimed, valid := ApplyWorkInvocationEvent(current, claimEvent)
	if !valid || claimed.State != InvocationClaimed {
		t.Fatalf("claim = %#v, %v", claimed, valid)
	}
	if _, reused := ApplyWorkInvocationEvent(claimed, claimEvent); reused {
		t.Fatal("claimed permit was reusable")
	}
	startPayload := json.RawMessage(fmt.Sprintf(`{"claim_id":"%s","conversation_id":"conversation-1","expected_invocation_revision":2,"invocation_id":"%s","request_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","started_at":"2026-08-31T12:02:00Z"}`, *claimed.ClaimID, current.ID))
	startEvent := DomainEvent{EventID: UUIDv7("00000000-0000-7000-8000-000000000962"), EventType: "tekroo.event.work-invocation.started", Aggregate: current.Ref(), AggregateRevision: 3, Payload: startPayload}
	started, valid := ApplyWorkInvocationEvent(claimed, startEvent)
	if !valid || started.State != InvocationStarted {
		t.Fatalf("start = %#v, %v", started, valid)
	}
	cancelPayload := json.RawMessage(fmt.Sprintf(`{"authority":{"id":"principal","kind":"HUMAN"},"evidence_ids":["00000000-0000-7000-8000-000000000966"],"expected_invocation_revision":3,"invocation_id":"%s","reason":"operator requested","requested_at":"2026-08-31T12:03:00Z"}`, current.ID))
	cancelEvent := DomainEvent{EventID: UUIDv7("00000000-0000-7000-8000-000000000963"), EventType: "tekroo.event.work-invocation.cancellation-requested", Aggregate: current.Ref(), AggregateRevision: 4, Payload: cancelPayload}
	cancelledRequested, valid := ApplyWorkInvocationEvent(started, cancelEvent)
	if !valid || cancelledRequested.State != InvocationStarted || cancelledRequested.CancellationRequestedAt == nil {
		t.Fatalf("cancellation request = %#v, %v", cancelledRequested, valid)
	}
}

func TestEveryNamedResetMechanismRetainsTheRootBound(t *testing.T) {
	fixture := phase4Fixture(t)
	t.Logf("computed remaining invocation ceiling: %d", fixture.account.MaximumAdditionalInvocations())
	mechanisms := []string{"CHILD_TASK", "HANDOFF", "REPLAN", "REVIEW", "REPAIR", "PROMOTION", "ACTOR_REPLACEMENT", "MODEL_REPLACEMENT", "RESTART"}
	for _, mechanism := range mechanisms {
		t.Run(mechanism, func(t *testing.T) {
			preserved := PlanWorkBudgetInheritance(fixture.account, fixture.account.ID, fixture.account.ModelInvocationLimit)
			if !preserved.Accepted || preserved.Reason != "ACCEPTED" || preserved.AccountID != fixture.account.ID || preserved.MaximumAdditionalInvocations != 6 {
				t.Fatalf("%s preserved inheritance = %#v", mechanism, preserved)
			}
			newAccount := UUIDv7("00000000-0000-7000-8000-000000000999")
			decision := PlanWorkBudgetInheritance(fixture.account, newAccount, fixture.account.ModelInvocationLimit)
			if decision.Accepted || decision.Reason != "BUDGET_RESET_PROHIBITED" || decision.MaximumAdditionalInvocations != 6 {
				t.Fatalf("%s reset = %#v", mechanism, decision)
			}
		})
	}
	mint := PlanWorkBudgetInheritance(fixture.account, fixture.account.ID, fixture.account.ModelInvocationLimit+1)
	if mint.Accepted || mint.Reason != "CAPACITY_MINT_PROHIBITED" || mint.MaximumAdditionalInvocations != 6 {
		t.Fatalf("capacity mint = %#v", mint)
	}
}

func TestFiniteRootBudgetBoundsAllAcceptedChangedConditionInvocations(t *testing.T) {
	for limit := uint64(1); limit <= 32; limit++ {
		for initiallyUsed := uint64(0); initiallyUsed <= limit; initiallyUsed++ {
			fixture := phase4Fixture(t)
			fixture.account.ModelInvocationLimit = limit
			fixture.account.ModelInvocationsUsed = initiallyUsed
			fixture.account.PurposeLimits[PurposeImplementation] = limit
			fixture.account.PurposeUsed[PurposeImplementation] = initiallyUsed
			fixture.binding.ModelInvocationLimit = limit
			fixture.binding.ModelInvocationsUsed = initiallyUsed
			fixture.binding.PurposeLimits[PurposeImplementation] = limit
			fixture.binding.PurposeUsed[PurposeImplementation] = initiallyUsed
			accepted := uint64(0)
			for ordinal := initiallyUsed + 1; ordinal <= limit+1; ordinal++ {
				invocationID := UUIDv7(fmt.Sprintf("00000000-0000-7000-8000-%012x", 0x1000+ordinal))
				eventID := UUIDv7(fmt.Sprintf("00000000-0000-7000-8000-%012x", 0x2000+ordinal))
				condition := Digest(fmt.Sprintf("%064x", ordinal))
				decision := PlanWorkInvocationAuthorization(fixture.authorizationPayload(t, invocationID, condition, nil, 0), fixture.task, fixture.account, fixture.binding, fixture.scope, fixture.profile, fixture.assignment, fixture.execution, nil, fixture.now, eventID)
				if !decision.Accepted {
					break
				}
				accepted++
				fixture.account.ModelInvocationsUsed = decision.BudgetDebit.NextGlobalUsed
				fixture.account.PurposeUsed[PurposeImplementation] = decision.BudgetDebit.NextGlobalPurposeUsed
				fixture.binding.ModelInvocationsUsed = decision.BudgetDebit.NextTaskUsed
				fixture.binding.PurposeUsed[PurposeImplementation] = decision.BudgetDebit.NextTaskPurposeUsed
			}
			if accepted != limit-initiallyUsed {
				t.Fatalf("limit=%d used=%d accepted=%d", limit, initiallyUsed, accepted)
			}
		}
	}
}

func TestOnlyInvocationAuthorizationCreatesExecutableWork(t *testing.T) {
	if kind := outboxKindForCommand("tekroo.command.work-invocation.authorize"); kind != "WORK_INVOCATION_AUTHORIZED" {
		t.Fatalf("authorization outbox kind = %s", kind)
	}
	for _, commandType := range []string{
		"tekroo.command.task.dispatch",
		"tekroo.command.task.handoff",
		"tekroo.command.task.request-review",
		"tekroo.command.task.request-changes",
		"tekroo.command.task.reopen",
		"tekroo.command.escalation.open",
	} {
		if kind := outboxKindForCommand(commandType); kind != "DOMAIN_EVENT" {
			t.Fatalf("%s created executable outbox kind %s", commandType, kind)
		}
	}
}

type phase4TestFixture struct {
	now          time.Time
	task         AggregateState
	account      WorkBudgetAccount
	binding      TaskWorkBudgetBinding
	scope        TaskOperationalScope
	profile      WorkProfileSnapshot
	assignment   QualifiedAssignmentAuthorization
	execution    ExecutionTuple
	invocationID UUIDv7
	eventID      UUIDv7
}

func phase4Fixture(t *testing.T) phase4TestFixture {
	t.Helper()
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	taskID := UUIDv7("00000000-0000-7000-8000-000000000902")
	actor := ActorFQN("teams::coder-1")
	execution := ExecutionTuple{ExecutionID: UUIDv7("00000000-0000-7000-8000-000000000908"), FencingEpoch: 1}
	profileBinding := WorkProfileBinding{ProfileID: UUIDv7("00000000-0000-7000-8000-000000000903"), ProfileRevision: 1, ProfileDigest: Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), LifecycleEpoch: 1, ScopeRevision: 1}
	profile := WorkProfileSnapshot{BoundEventID: UUIDv7("00000000-0000-7000-8000-000000000904"), TaskRevision: 2, Profile: WorkRiskProfile{
		TaskID: taskID, ProfileID: profileBinding.ProfileID, ProfileRevision: 1, ProfileDigest: profileBinding.ProfileDigest, LifecycleEpoch: 1, ScopeRevision: 1,
		WorkKind: WorkImplementation, Ambiguity: AmbiguityLow, Novelty: NoveltyRoutine, BlastRadius: BlastLocal, SecuritySensitivity: SecurityOrdinary,
		MinimumDecisionRoute: RouteBoundedExecution, AcceptanceCriteriaDigest: Digest("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"), RequiredDeterministicGateIDs: []string{"go-test"}, RequiredValidationBranches: 1,
		RequiredIndependenceDimensions: []IndependenceDimension{IndependencePrincipal, IndependenceActor, IndependenceExecution, IndependenceContext, IndependenceWorkspace, IndependenceMethod}, ImplementationVariantCount: 1, ValidCandidateQuorum: 1,
		VerificationTopologyDigest: Digest("cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"), ClassificationPolicyRevision: 1, ClassificationPolicyDigest: Digest("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"), PromotionPolicyRevision: 1, PromotionPolicyDigest: Digest("eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"),
		Budgets: FiniteWorkBudgets{AttemptLimit: 2, ReviewRoundLimit: 2, PromotionLimit: 1, EscalationLimit: 1, DeadlineAt: now.Add(24 * time.Hour)}, ClassificationAuthority: PrincipalRef{Kind: PrincipalHuman, ID: "principal"}, ClassificationEvidenceIDs: []UUIDv7{UUIDv7("00000000-0000-7000-8000-000000000904")},
	}}
	assignment := QualifiedAssignmentAuthorization{
		AssignmentID: UUIDv7("00000000-0000-7000-8000-000000000905"), TaskID: taskID, ExpectedTaskRevision: 2, WorkProfile: profileBinding,
		RequiredDecisionRoute: RouteBoundedExecution, SelectedDecisionRoute: RouteBoundedExecution, SelectedActorFQN: actor, SelectedExecutionID: execution.ExecutionID, SelectedFencingEpoch: execution.FencingEpoch,
		ModelProfileDigest: Digest("2222222222222222222222222222222222222222222222222222222222222222"), RuntimeIdentityDigest: Digest("3333333333333333333333333333333333333333333333333333333333333333"),
		Qualification:           AssignmentQualificationReceipt{QualificationID: UUIDv7("00000000-0000-7000-8000-000000000906"), QualificationDigest: Digest("4444444444444444444444444444444444444444444444444444444444444444"), QualificationCorpusDigest: Digest("5555555555555555555555555555555555555555555555555555555555555555"), ModelProfileDigest: Digest("2222222222222222222222222222222222222222222222222222222222222222"), DecisionRoute: RouteBoundedExecution, QualifiedRole: "programmer", Status: QualificationPass, ObservedAt: now.Add(-time.Hour)},
		SelectionPolicyRevision: 1, SelectionPolicyDigest: Digest("6666666666666666666666666666666666666666666666666666666666666666"), HardConstraintResults: []HardConstraintResult{{ConstraintID: "local", Outcome: ConstraintPass, EvidenceIDs: []UUIDv7{UUIDv7("00000000-0000-7000-8000-000000000904")}}}, SelectionReasons: []string{"qualified"}, EvidenceIDs: []UUIDv7{UUIDv7("00000000-0000-7000-8000-000000000904")}, AuthorizationEventID: UUIDv7("00000000-0000-7000-8000-000000000907"),
	}
	purposeLimits := zeroPurposeCounters()
	purposeLimits[PurposeImplementation] = 4
	for _, purpose := range AllWorkPurposes {
		if purposeLimits[purpose] == 0 {
			purposeLimits[purpose] = 1
		}
	}
	purposeUsed := zeroPurposeCounters()
	purposeUsed[PurposeImplementation] = 1
	return phase4TestFixture{
		now:     now,
		task:    AggregateState{Kind: AggregateTask, ID: taskID, Revision: 4, LifecycleEpoch: 1, ScopeRevision: 1, Phase: PhaseActive, Condition: ConditionRunnable, Ownership: Ownership{OwnerFQN: &actor, OwnershipVersion: 1}},
		account: WorkBudgetAccount{ID: UUIDv7("00000000-0000-7000-8000-000000000901"), Revision: 1, RootWork: AggregateRef{Kind: AggregateTask, ID: taskID}, LifecycleEpoch: 1, PolicyRevision: 1, PolicyDigest: Digest("7777777777777777777777777777777777777777777777777777777777777777"), ModelInvocationLimit: 8, ModelInvocationsUsed: 2, PurposeLimits: purposeLimits.Clone(), PurposeUsed: purposeUsed.Clone(), DeadlineAt: now.Add(30 * 24 * time.Hour), LastEventID: UUIDv7("00000000-0000-7000-8000-000000000909")},
		binding: TaskWorkBudgetBinding{TaskID: taskID, BudgetAccountID: UUIDv7("00000000-0000-7000-8000-000000000901"), TaskRevision: 4, LifecycleEpoch: 1, ScopeRevision: 1, ModelInvocationLimit: 8, ModelInvocationsUsed: 2, PurposeLimits: purposeLimits.Clone(), PurposeUsed: purposeUsed.Clone(), BoundEventID: UUIDv7("00000000-0000-7000-8000-000000000910")},
		scope:   TaskOperationalScope{TaskID: taskID, TaskRevision: 4, LifecycleEpoch: 1, ScopeRevision: 1, OwnerFQN: actor, Execution: execution, WorkspaceID: "workspace-1", WorktreeID: "worktree-1", Branch: "phase4", BaselineSHA: "1111111111111111111111111111111111111111", WritablePaths: []string{"kernel"}, BoundEventID: UUIDv7("00000000-0000-7000-8000-000000000911")},
		profile: profile, assignment: assignment, execution: execution,
		invocationID: UUIDv7("00000000-0000-7000-8000-000000000912"), eventID: UUIDv7("00000000-0000-7000-8000-000000000913"),
	}
}

func (f phase4TestFixture) clone() phase4TestFixture {
	f.account = f.account.Clone()
	f.binding = f.binding.Clone()
	f.scope = f.scope.Clone()
	f.profile = f.profile.Clone()
	f.assignment = f.assignment.Clone()
	return f
}

func (f phase4TestFixture) authorizationObject(t *testing.T, invocationID UUIDv7, condition Digest, retryOf *UUIDv7, retryOrdinal uint64) map[string]any {
	t.Helper()
	retry := any(nil)
	if retryOf != nil {
		retry = string(*retryOf)
	}
	return map[string]any{
		"invocation_id": invocationID, "task_id": f.task.ID, "budget_account_id": f.account.ID,
		"expected_budget_revision": f.account.Revision, "expected_task_revision": f.task.Revision,
		"lifecycle_epoch": f.task.LifecycleEpoch, "scope_revision": f.task.ScopeRevision,
		"parent_event_id": UUIDv7("00000000-0000-7000-8000-000000000914"), "work_profile": f.profile.Profile.Binding(),
		"qualified_assignment_id": f.assignment.AssignmentID, "purpose": PurposeImplementation,
		"attempt_family": "implementation", "attempt_ordinal": uint64(1), "condition_digest": condition,
		"retry_of_invocation_id": retry, "retry_ordinal": retryOrdinal,
		"output_predicate_digest":   Digest("8888888888888888888888888888888888888888888888888888888888888888"),
		"allowed_terminal_outcomes": []WorkInvocationState{InvocationSucceeded, InvocationFailed, InvocationTimedOut, InvocationCancelled, InvocationStartFailed},
		"tool_policy_digest":        Digest("9999999999999999999999999999999999999999999999999999999999999999"),
		"effect_policy_digest":      Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		"actor_fqn":                 f.assignment.SelectedActorFQN, "execution_id": f.execution.ExecutionID, "fencing_epoch": f.execution.FencingEpoch,
		"model_profile_digest": f.assignment.ModelProfileDigest, "runtime_identity_digest": f.assignment.RuntimeIdentityDigest,
		"workspace_id": f.scope.WorkspaceID, "deadline_at": f.now.Add(24 * time.Hour).Format(time.RFC3339),
		"idempotency_key": fmt.Sprintf("invocation-%s", invocationID), "admission_policy_revision": f.account.PolicyRevision,
		"admission_policy_digest": f.account.PolicyDigest,
	}
}

func (f phase4TestFixture) authorizationPayload(t *testing.T, invocationID UUIDv7, condition Digest, retryOf *UUIDv7, retryOrdinal uint64) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(f.authorizationObject(t, invocationID, condition, retryOf, retryOrdinal))
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func (f phase4TestFixture) authorizedInvocation(t *testing.T, invocationID UUIDv7, condition Digest) WorkInvocation {
	t.Helper()
	decision := PlanWorkInvocationAuthorization(f.authorizationPayload(t, invocationID, condition, nil, 0), f.task, f.account, f.binding, f.scope, f.profile, f.assignment, f.execution, nil, f.now, f.eventID)
	if !decision.Accepted {
		t.Fatalf("fixture authorization = %#v", decision)
	}
	return decision.Invocation
}

func cloneJSONMap(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var copy map[string]any
	if json.Unmarshal(encoded, &copy) != nil {
		t.Fatal("clone json")
	}
	return copy
}
