//go:build mongo_integration

package mongo

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

func TestOperationalExecutionReaderReconstructsAuthoritativeMongoContext(t *testing.T) {
	store := openTestStore(t)
	now := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	task := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: testUUID(9901)}
	budget := kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: testUUID(9902)}
	invocationRef := kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: testUUID(9903)}
	actor := kernel.ActorFQN("teams::coder-1")
	execution := kernel.ExecutionTuple{ExecutionID: testUUID(9904), FencingEpoch: 2}
	evidenceID := testUUID(9905)
	createdEventID := testUUID(9906)
	authorizationEventID := testUUID(9907)

	taskState := kernel.AggregateState{Kind: kernel.AggregateTask, ID: task.ID, Revision: 5, LifecycleEpoch: 1, ScopeRevision: 1, Phase: kernel.PhaseActive, Condition: kernel.ConditionRunnable, Ownership: kernel.Ownership{OwnerFQN: &actor, OwnershipVersion: 1}}
	insertProjectionAggregate(t, store, taskState)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := store.db.Collection("aggregates").InsertOne(ctx, aggregateDocument{ID: aggregateKey(budget), Revision: 1}); err != nil {
		t.Fatal(err)
	}

	created := projectionMongoEvent(task, 1, createdEventID, "tekroo.event.task.created", json.RawMessage(`{"story_id":"00000000-0000-7000-8000-000000009908","title":"Execute bounded task","description":"Perform the exact authorized work.","acceptance_criteria":["Evidence is retained."],"depends_on":[]}`))
	authorized := projectionMongoEvent(invocationRef, 1, authorizationEventID, "tekroo.event.work-invocation.authorized", json.RawMessage(`{}`))
	insertProjectionEvent(t, store, created)
	insertProjectionEvent(t, store, authorized)

	limits, used := make(kernel.PurposeCounters, len(kernel.AllWorkPurposes)), make(kernel.PurposeCounters, len(kernel.AllWorkPurposes))
	for _, purpose := range kernel.AllWorkPurposes {
		limits[purpose] = 3
		used[purpose] = 0
	}
	used[kernel.PurposeImplementation] = 1
	account := kernel.WorkBudgetAccount{ID: budget.ID, Revision: 1, RootWork: task, LifecycleEpoch: 1, PolicyRevision: 1, PolicyDigest: mongoDigest('a'), ModelInvocationLimit: 3, ModelInvocationsUsed: 1, PurposeLimits: limits.Clone(), PurposeUsed: used.Clone(), DeadlineAt: now.Add(time.Hour), LastEventID: authorizationEventID}
	binding := kernel.TaskWorkBudgetBinding{TaskID: task.ID, BudgetAccountID: budget.ID, TaskRevision: 2, LifecycleEpoch: 1, ScopeRevision: 1, ModelInvocationLimit: 3, ModelInvocationsUsed: 1, PurposeLimits: limits.Clone(), PurposeUsed: used.Clone(), BoundEventID: testUUID(9909)}
	insertProjectionValue(t, store, "work_budget_accounts", aggregateKey(budget), workBudgetValue{Reference: budget, Account: account})
	insertProjectionValue(t, store, "task_work_budgets", aggregateKey(task), taskBudgetValue{Task: task, Binding: binding})

	profile := mongoTestWorkProfile(task.ID)
	profile.Budgets.DeadlineAt = now.Add(time.Hour)
	profile.ClassificationEvidenceIDs = []kernel.UUIDv7{evidenceID}
	profileSnapshot := kernel.WorkProfileSnapshot{Profile: profile, TaskRevision: 4, BoundEventID: testUUID(9910)}
	assignment := mongoTestAssignmentAuthorization(task.ID, profile.Binding())
	assignment.ExpectedTaskRevision = 4
	assignment.SelectedExecutionID = execution.ExecutionID
	assignment.SelectedFencingEpoch = execution.FencingEpoch
	assignment.EvidenceIDs = []kernel.UUIDv7{evidenceID}
	assignment.HardConstraintResults[0].EvidenceIDs = []kernel.UUIDv7{evidenceID}
	insertProjectionValue(t, store, "work_profiles", aggregateKey(task), struct {
		Task    kernel.AggregateRef        `json:"task"`
		Profile kernel.WorkProfileSnapshot `json:"profile"`
	}{task, profileSnapshot})
	insertProjectionValue(t, store, "qualified_assignments", aggregateKey(task), struct {
		Task          kernel.AggregateRef                     `json:"task"`
		Authorization kernel.QualifiedAssignmentAuthorization `json:"authorization"`
	}{task, assignment})

	scope := kernel.TaskOperationalScope{TaskID: task.ID, TaskRevision: 4, LifecycleEpoch: 1, ScopeRevision: 1, OwnerFQN: actor, Execution: execution, WorkspaceID: "workspace-9901", WorktreeID: "worktree-9901", Branch: "task/9901", BaselineSHA: strings.Repeat("1", 40), WritablePaths: []string{"src"}, InterfaceEvidenceIDs: []kernel.UUIDv7{evidenceID}, BoundEventID: testUUID(9911)}
	insertProjectionValue(t, store, "task_operational_scopes", aggregateKey(task), taskScopeValue{Task: task, Scope: scope})

	invocation := kernel.WorkInvocation{ID: invocationRef.ID, Revision: 1, State: kernel.InvocationAuthorized, AuthorizationEventID: authorizationEventID, ParentEventID: createdEventID, TaskID: task.ID, BudgetAccountID: budget.ID, LifecycleEpoch: 1, ScopeRevision: 1, TaskRevision: 5, WorkProfile: profile.Binding(), QualifiedAssignmentID: assignment.AssignmentID, Purpose: kernel.PurposeImplementation, AttemptFamily: "implementation", AttemptOrdinal: 1, ConditionDigest: mongoDigest('b'), OutputPredicateDigest: mongoDigest('c'), AllowedTerminalOutcomes: []kernel.WorkInvocationState{kernel.InvocationSucceeded, kernel.InvocationFailed, kernel.InvocationTimedOut, kernel.InvocationCancelled, kernel.InvocationStartFailed}, ToolPolicyDigest: mongoDigest('d'), EffectPolicyDigest: mongoDigest('e'), ActorFQN: actor, Execution: execution, ModelProfileDigest: assignment.ModelProfileDigest, RuntimeIdentityDigest: assignment.RuntimeIdentityDigest, WorkspaceID: scope.WorkspaceID, DeadlineAt: now.Add(time.Hour), IdempotencyKey: "invocation-9903", AdmissionPolicyRevision: 1, AdmissionPolicyDigest: account.PolicyDigest, GlobalDebitOrdinal: 1, PurposeDebitOrdinal: 1, RemainingGlobalBudget: 2, RemainingPurposeBudget: 2, LastEventID: authorizationEventID}
	if !invocation.Valid() {
		t.Fatal("invalid operational invocation fixture")
	}
	insertProjectionValue(t, store, "work_invocations", aggregateKey(invocationRef), invocationValue{Reference: invocationRef, Invocation: invocation})
	if _, err := store.db.Collection("executions").InsertOne(ctx, executionDocument{ID: string(actor), ExecutionID: string(execution.ExecutionID), FencingEpoch: execution.FencingEpoch}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Collection("evidence").InsertOne(ctx, evidenceDocument{ID: string(evidenceID), SHA256: string(mongoDigest('f')), Available: true}); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.LoadOperationalExecutionByAuthorizationEvent(ctx, authorizationEventID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Invocation.ID != invocation.ID || loaded.Specification.CreatedEventID != createdEventID || len(loaded.Evidence) != 1 {
		t.Fatalf("loaded context = %#v", loaded)
	}
	if err := loaded.Validate(now); err != nil {
		t.Fatalf("validate authoritative context: %v", err)
	}
}

func mongoDigest(value byte) kernel.Digest {
	return kernel.Digest(strings.Repeat(string(value), 64))
}
