//go:build mongo_integration

package mongo

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// Projection fixtures isolate the actual transaction/CAS boundary. This does
// not claim kernel command admission or model-driven amendment qualification.
func newPlanAmendmentTransactionFixture(t *testing.T, ctx context.Context, store *Store) (organization.FeatureRequest, organization.PlanAmendmentWitness, organization.PlanAmendmentFence, organization.PlanAmendmentReservation) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Millisecond)
	digest := kernel.Digest(strings.Repeat("a", 64))
	story := organization.PlannedStory{ID: testUUID(9401), Title: "Feature", Description: "Accepted requirement", AcceptanceCriteria: []string{"works"}, Priority: organization.PriorityNormal}
	execution := kernel.ExecutionTuple{ExecutionID: testUUID(9402), FencingEpoch: 1}
	implementation := organization.PlannedTask{ID: testUUID(9403), StoryID: story.ID, Title: "Implement", Description: "Accepted behavior", AcceptanceCriteria: []string{"works"}, Owner: "teams::coder-1", ModelProfile: digest, DecisionRoute: kernel.RouteBoundedExecution, Purpose: kernel.PurposeImplementation, Complexity: 2, Risk: organization.RiskLow, AttemptLimit: 2, ReviewRoundLimit: 1}
	implementation.WriteScope = []string{"service/consumer.go"}
	validation := implementation
	validation.ID, validation.Owner, validation.Purpose = testUUID(9404), "teams::tester-1", kernel.PurposeValidation
	validation.DependsOn, validation.Validates = []kernel.UUIDv7{implementation.ID}, []kernel.UUIDv7{implementation.ID}
	validation.WriteScope = nil
	plan := organization.FeaturePlan{Version: 1, PreparedBy: "teams::project-manager-1", PreparedExecution: execution, Architecture: "Small design", SourceDesignDigest: digest, Stories: []organization.PlannedStory{story}, Tasks: []organization.PlannedTask{implementation, validation}, CreatedAt: now.Add(-time.Minute)}
	feature := organization.FeatureRequest{SchemaVersion: organization.FeatureSchemaVersion, ID: testUUID(9405), Revision: 6, SubmittedBy: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"}, Input: organization.FeatureRequestInput{IdempotencyKey: "amendment-cas", Team: "teams", Title: "Feature", Description: "Accepted requirement", AcceptanceCriteria: []string{"works"}, Priority: organization.PriorityNormal, Repository: "teams", WorkspaceID: "operator-1", MaximumStories: 2, MaximumTasks: 8, MaximumHops: 16}, Status: organization.FeaturePlanned, OperatorActor: "teams::operator-1", ProductOwnerActor: "teams::product-owner-1", InitialMessageID: testUUID(9406), LastMessageID: testUUID(9407), LastStepID: testUUID(9408), LastHop: 4, BudgetAccountID: testUUID(9409), LifecycleEpoch: 1, ScopeRevision: 1, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Minute), Plan: &plan,
		Specification: &organization.FeatureSpecification{PreparedBy: plan.PreparedBy, PreparedExecution: execution, Stories: plan.Stories, PreparedAt: now.Add(-time.Minute)}, Design: &organization.FeatureDesignCandidate{PreparedBy: "teams::architect-1", PreparedExecution: execution, OutputDigest: digest, PreparedAt: now.Add(-time.Minute)}}
	if err := feature.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(feature)
	if _, err := store.db.Collection("feature_requests").InsertOne(ctx, featureDocument{ID: string(feature.ID), Revision: feature.Revision, SubmittedByKind: feature.SubmittedBy.Kind, SubmittedByID: feature.SubmittedBy.ID, IdempotencyKey: feature.Input.IdempotencyKey, Status: feature.Status, CreatedAt: feature.CreatedAt, UpdatedAt: feature.UpdatedAt, Data: raw}); err != nil {
		t.Fatal(err)
	}
	witness := organization.PlanAmendmentWitness{Tasks: map[kernel.UUIDv7]kernel.AggregateState{}, Invocations: map[kernel.AggregateRef]kernel.WorkInvocation{}, Evidence: map[kernel.UUIDv7]kernel.EvidenceMetadata{testUUID(9413): {Available: true, SHA256: digest}}, Now: now, ScopeChangeRequired: true}
	var guards []kernel.AggregatePrecondition
	seed := func(ref kernel.AggregateRef, revision uint64, phase kernel.Phase, condition kernel.Condition) kernel.AggregateState {
		state := kernel.AggregateState{Kind: ref.Kind, ID: ref.ID, Revision: revision, LifecycleEpoch: 1, ScopeRevision: 1, Phase: phase, Condition: condition}
		insertProjectionAggregate(t, store, state)
		guards = append(guards, kernel.AggregatePrecondition{Aggregate: ref, Expected: kernel.NewExpectedRevision(revision)})
		return state
	}
	witness.Tasks[implementation.ID] = seed(kernel.AggregateRef{Kind: kernel.AggregateTask, ID: implementation.ID}, 10, kernel.PhaseActive, kernel.ConditionBlocked)
	witness.Tasks[validation.ID] = seed(kernel.AggregateRef{Kind: kernel.AggregateTask, ID: validation.ID}, 2, kernel.PhasePlanned, kernel.ConditionRunnable)
	seed(kernel.AggregateRef{Kind: kernel.AggregateTask, ID: testUUID(9410)}, 10, kernel.PhaseCompleted, kernel.ConditionRunnable)
	blocked := kernel.WorkInvocation{ID: testUUID(9411), Revision: 4, TaskID: implementation.ID, BudgetAccountID: feature.BudgetAccountID, State: kernel.InvocationSucceeded, OutputDigest: &digest, TerminalEvidenceIDs: []kernel.UUIDv7{testUUID(9413)}}
	blocked.Purpose, blocked.AttemptOrdinal = kernel.PurposeImplementation, 1
	consult := blocked
	consult.ID, consult.TaskID, consult.ActorFQN, consult.Purpose = testUUID(9412), testUUID(9410), feature.Design.PreparedBy, kernel.PurposeHandoff
	witness.Invocations[blocked.Ref()], witness.Consultation = blocked, consult
	for _, invocation := range []kernel.WorkInvocation{blocked, consult} {
		seed(invocation.Ref(), 4, kernel.PhaseCompleted, kernel.ConditionRunnable)
		insertProjectionValue(t, store, "work_invocations", aggregateKey(invocation.Ref()), invocationValue{Reference: invocation.Ref(), Invocation: invocation})
	}
	if _, err := store.db.Collection("evidence").InsertOne(ctx, evidenceDocument{ID: string(testUUID(9413)), SHA256: string(digest), Available: true}); err != nil {
		t.Fatal(err)
	}
	limits, used := kernel.PurposeCounters{}, kernel.PurposeCounters{}
	for _, purpose := range kernel.AllWorkPurposes {
		limits[purpose], used[purpose] = 10, 0
	}
	witness.Budget = kernel.WorkBudgetAccount{ID: feature.BudgetAccountID, Revision: 1, RootWork: kernel.AggregateRef{Kind: kernel.AggregateStory, ID: story.ID}, LifecycleEpoch: 1, PolicyRevision: 1, PolicyDigest: digest, ModelInvocationLimit: 20, ModelInvocationsUsed: 5, PurposeLimits: limits, PurposeUsed: used, DeadlineAt: now.Add(time.Hour), LastEventID: testUUID(9414)}
	seed(witness.Budget.Ref(), 1, kernel.PhaseActive, kernel.ConditionRunnable)
	insertProjectionValue(t, store, "work_budget_accounts", aggregateKey(witness.Budget.Ref()), workBudgetValue{Reference: witness.Budget.Ref(), Account: witness.Budget})
	request := organization.PlanAmendmentReservation{AssessmentID: testUUID(9415), BlockedTaskID: implementation.ID, BlockedTaskRevision: 10, BlockedInvocationID: blocked.ID, ConsultationID: consult.TaskID, AnswerDigest: digest}
	witness.PM, witness.PMExecution, witness.AllowedRoots = plan.PreparedBy, execution, []string{"service"}
	return feature, witness, organization.PlanAmendmentFence{Preconditions: guards, Budget: witness.Budget}, request
}

func TestPlanAmendmentFailedAssessmentWithoutOutputIsDurablyRejected(t *testing.T) {
	store := openTestStore(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	feature, w, fence, request := newPlanAmendmentTransactionFixture(t, ctx, store)
	reserved, err := organization.ReservePlanAmendment(feature, request, w)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompareAndSwapPlanAmendment(ctx, reserved, feature.Revision, fence); err != nil {
		t.Fatal(err)
	}
	failed := kernel.WorkInvocation{ID: testUUID(9450), Revision: 4, TaskID: request.AssessmentID, ActorFQN: feature.Plan.PreparedBy, Execution: feature.Plan.PreparedExecution, Purpose: kernel.PurposeHandoff, AttemptOrdinal: 1, BudgetAccountID: feature.BudgetAccountID, State: kernel.InvocationStartFailed, TerminalEvidenceIDs: []kernel.UUIDv7{testUUID(9413)}}
	for _, state := range []kernel.AggregateState{{Kind: kernel.AggregateTask, ID: request.AssessmentID, Revision: 10, Phase: kernel.PhaseActive, Condition: kernel.ConditionBlocked}, {Kind: kernel.AggregateWorkInvocation, ID: failed.ID, Revision: 4, Phase: kernel.PhaseCompleted, Condition: kernel.ConditionRunnable}} {
		insertProjectionAggregate(t, store, state)
		fence.Preconditions = append(fence.Preconditions, kernel.AggregatePrecondition{Aggregate: kernel.AggregateRef{Kind: state.Kind, ID: state.ID}, Expected: kernel.NewExpectedRevision(state.Revision)})
	}
	insertProjectionValue(t, store, "work_invocations", aggregateKey(failed.Ref()), invocationValue{Reference: failed.Ref(), Invocation: failed})
	policy := organization.PlanAmendmentPolicy{ContractVersion: organization.PlanAmendmentContractVersion, ManifestDigest: w.Budget.PolicyDigest, Authority: kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "amendment-policy"}, AllowedRoots: []string{"service"}}
	rejected, err := organization.RecordPlanAmendmentDecision(reserved, []byte(`{"state":"START_FAILED"}`), w, policy, policy.Authority)
	if err != nil {
		t.Fatal(err)
	}
	rejected.PlanAmendment.AssessmentInvocationID = failed.ID
	rejected.PlanAmendment.Decision.Code = "ASSESSMENT_FAILED"
	if _, err := store.CompareAndSwapPlanAmendment(ctx, rejected, reserved.Revision, fence); err != nil {
		t.Fatal("failed assessment not retained", err)
	}
	stored, found, err := store.LoadFeature(ctx, feature.ID)
	if err != nil || !found || stored.PlanAmendment.State != organization.AmendmentRejected || !reflect.DeepEqual(stored.Plan, feature.Plan) {
		t.Fatal("failed assessment changed plan", err)
	}
	request.AssessmentID = testUUID(9451)
	if _, err := organization.ReservePlanAmendment(stored, request, w); err == nil {
		t.Fatal("failed assessment received another allowance")
	}
}

func TestPlanAmendmentTransactionPublishesOnlyPinnedAssessmentAndFreshSuccessors(t *testing.T) {
	store := openTestStore(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	feature, w, fence, request := newPlanAmendmentTransactionFixture(t, ctx, store)
	reserved, err := organization.ReservePlanAmendment(feature, request, w)
	if err != nil {
		t.Fatal(err)
	}
	input := kernel.EvidenceRef{EvidenceID: testUUID(9430), SHA256: kernel.Digest(strings.Repeat("b", 64))}
	reserved.PlanAmendment.AssessmentInput = &input
	if _, err := store.db.Collection("evidence").InsertOne(ctx, evidenceDocument{ID: string(input.EvidenceID), SHA256: string(input.SHA256), Available: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompareAndSwapPlanAmendment(ctx, reserved, feature.Revision, fence); err != nil {
		t.Fatal(err)
	}
	planRaw, _ := json.Marshal(feature.Plan)
	var plan organization.FeaturePlan
	_ = json.Unmarshal(planRaw, &plan)
	plan.Version++
	plan.CreatedAt = w.Now
	plan.Tasks[0].ID, plan.Tasks[0].AttemptLimit = testUUID(9431), 1
	plan.Tasks[1].ID = testUUID(9432)
	plan.Tasks[1].DependsOn, plan.Tasks[1].Validates = []kernel.UUIDv7{plan.Tasks[0].ID}, []kernel.UUIDv7{plan.Tasks[0].ID}
	proposal := organization.PlanAmendmentProposal{SchemaVersion: organization.PlanAmendmentContractVersion, FeatureID: feature.ID, FeatureRevision: feature.Revision, PlanVersion: feature.Plan.Version, PlanDigest: organization.PlanAmendmentDigest(*feature.Plan), SpecificationDigest: organization.PlanAmendmentDigest(*feature.Specification), BlockedTaskID: request.BlockedTaskID, BlockedTaskRevision: request.BlockedTaskRevision, BlockedInvocationID: request.BlockedInvocationID, ConsultationID: request.ConsultationID, AnswerDigest: request.AnswerDigest, Reason: "Repair execution ownership within existing requirement", SuccessorPlan: plan, Replacements: []organization.PlanTaskReplacement{{PriorTaskID: feature.Plan.Tasks[0].ID, SuccessorTaskID: plan.Tasks[0].ID}, {PriorTaskID: feature.Plan.Tasks[1].ID, SuccessorTaskID: plan.Tasks[1].ID}}, Impacts: []organization.PlanAmendmentImpact{{Kind: "CALLER", Path: "service/consumer.go", OwnerTaskID: plan.Tasks[0].ID, Verification: "Compile and test consumer"}}}
	policy := organization.PlanAmendmentPolicy{ContractVersion: organization.PlanAmendmentContractVersion, ManifestDigest: w.Budget.PolicyDigest, Authority: kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "amendment-policy"}, AllowedRoots: []string{"service"}}
	raw, _ := json.Marshal(proposal)
	approved, err := organization.RecordPlanAmendmentDecision(reserved, raw, w, policy, policy.Authority)
	if err != nil || approved.PlanAmendment.State != organization.AmendmentApproved {
		t.Fatalf("policy: %v %+v", err, approved.PlanAmendment)
	}
	if _, err := store.CompareAndSwapPlanAmendment(ctx, approved, reserved.Revision, fence); err == nil {
		t.Fatal("approved without actual assessment invocation")
	}
	digest := kernel.Digest(strings.Repeat("c", 64))
	assessment := kernel.WorkInvocation{ID: testUUID(9433), Revision: 4, TaskID: request.AssessmentID, ActorFQN: plan.PreparedBy, Execution: plan.PreparedExecution, Purpose: kernel.PurposeHandoff, AttemptOrdinal: 1, BudgetAccountID: feature.BudgetAccountID, State: kernel.InvocationSucceeded, OutputDigest: &digest, TerminalEvidenceIDs: []kernel.UUIDv7{testUUID(9434)}}
	for _, state := range []kernel.AggregateState{{Kind: kernel.AggregateTask, ID: request.AssessmentID, Revision: 10, Phase: kernel.PhaseCompleted, Condition: kernel.ConditionRunnable}, {Kind: kernel.AggregateWorkInvocation, ID: assessment.ID, Revision: 4, Phase: kernel.PhaseCompleted, Condition: kernel.ConditionRunnable}} {
		insertProjectionAggregate(t, store, state)
		fence.Preconditions = append(fence.Preconditions, kernel.AggregatePrecondition{Aggregate: kernel.AggregateRef{Kind: state.Kind, ID: state.ID}, Expected: kernel.NewExpectedRevision(state.Revision)})
	}
	insertProjectionValue(t, store, "work_invocations", aggregateKey(assessment.Ref()), invocationValue{Reference: assessment.Ref(), Invocation: assessment})
	if _, err := store.db.Collection("evidence").InsertOne(ctx, evidenceDocument{ID: string(testUUID(9434)), SHA256: string(digest), Available: true}); err != nil {
		t.Fatal(err)
	}
	approved.PlanAmendment.AssessmentInvocationID, approved.PlanAmendment.AssessmentOutputDigest = assessment.ID, digest
	if _, err := store.CompareAndSwapPlanAmendment(ctx, approved, reserved.Revision, fence); err != nil {
		t.Fatal("approval", err)
	}
	w.Now = w.Now.Add(time.Millisecond)
	published, err := organization.PublishPlanAmendment(approved, w, policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompareAndSwapPlanAmendment(ctx, published, approved.Revision, fence); err == nil {
		t.Fatal("published nonexistent successors")
	}
	for _, task := range plan.Tasks {
		state := kernel.AggregateState{Kind: kernel.AggregateTask, ID: task.ID, Revision: 2, Phase: kernel.PhasePlanned, Condition: kernel.ConditionRunnable}
		insertProjectionAggregate(t, store, state)
		fence.Preconditions = append(fence.Preconditions, kernel.AggregatePrecondition{Aggregate: kernel.AggregateRef{Kind: state.Kind, ID: state.ID}, Expected: kernel.NewExpectedRevision(state.Revision)})
	}
	if _, err := store.CompareAndSwapPlanAmendment(ctx, published, approved.Revision, fence); err != nil {
		t.Fatal("publication", err)
	}
	reopened := &Store{client: store.client, db: store.db}
	stored, exists, err := reopened.LoadFeature(ctx, feature.ID)
	if err != nil || !exists || stored.PlanAmendment.State != organization.AmendmentApplied || !reflect.DeepEqual(stored.Plan, published.Plan) || !reflect.DeepEqual(stored.Specification, feature.Specification) {
		t.Fatal("publication not durable", err)
	}
	if _, err := reopened.CompareAndSwapPlanAmendment(ctx, published, approved.Revision, fence); !errors.Is(err, organization.ErrFeatureRevisionConflict) {
		t.Fatal("duplicate publication", err)
	}
}

func TestPlanAmendmentTransactionFencesBudgetDebitsAndPreservesRejection(t *testing.T) {
	store := openTestStore(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	feature, witness, fence, request := newPlanAmendmentTransactionFixture(t, ctx, store)
	reserved, err := organization.ReservePlanAmendment(feature, request, witness)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AdvanceFeature(ctx, reserved, feature.Revision, nil); !errors.Is(err, organization.ErrPlanAmendment) {
		t.Fatalf("generic CAS bypass: %v", err)
	}
	// Simulate a concurrent debit with an unchanged policy revision. The old
	// revision-only approach would approve against stale available capacity.
	debited := witness.Budget.Clone()
	debited.ModelInvocationsUsed++
	debited.PurposeUsed[kernel.PurposeHandoff]++
	debited.LastEventID = testUUID(9416)
	if err := store.replaceValue(ctx, "work_budget_accounts", aggregateKey(debited.Ref()), workBudgetValue{Reference: debited.Ref(), Account: debited}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompareAndSwapPlanAmendment(ctx, reserved, feature.Revision, fence); !errors.Is(err, organization.ErrFeatureRevisionConflict) {
		t.Fatalf("stale debit accepted: %v", err)
	}
	stored, found, err := store.LoadFeature(ctx, feature.ID)
	if err != nil || !found || stored.PlanAmendment != nil || stored.Revision != feature.Revision {
		t.Fatal("failed transaction consumed allowance", err)
	}
	fence.Budget = debited
	if _, err := store.CompareAndSwapPlanAmendment(ctx, reserved, feature.Revision, fence); err != nil {
		t.Fatal(err)
	}
	// A fresh Store object reads the durable ledger; no process-local allowance.
	reopened := &Store{client: store.client, db: store.db}
	stored, found, err = reopened.LoadFeature(ctx, feature.ID)
	if err != nil || !found || stored.PlanAmendment.State != organization.AmendmentAssessing || !reflect.DeepEqual(stored.Plan, feature.Plan) {
		t.Fatal("durable reservation lost", err)
	}
	policy := organization.PlanAmendmentPolicy{ContractVersion: organization.PlanAmendmentContractVersion, ManifestDigest: witness.Budget.PolicyDigest, Authority: kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "amendment-policy"}, AllowedRoots: []string{"service"}}
	rejected, err := organization.RecordPlanAmendmentDecision(stored, []byte(`{"successor_plan":"{}"}`), witness, policy, policy.Authority)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.CompareAndSwapPlanAmendment(ctx, rejected, stored.Revision, fence); err != nil {
		t.Fatal(err)
	}
	stored, _, err = reopened.LoadFeature(ctx, feature.ID)
	if err != nil || stored.PlanAmendment.State != organization.AmendmentRejected || stored.PlanAmendment.Decision.Code != "INVALID_PROPOSAL_SCHEMA" || !reflect.DeepEqual(stored.Plan, feature.Plan) {
		t.Fatal("rejection changed work", err)
	}
	request.AssessmentID = testUUID(9417)
	if _, err := organization.ReservePlanAmendment(stored, request, witness); err == nil {
		t.Fatal("rejected allowance was renewed")
	}
	if _, err := reopened.CompareAndSwapPlanAmendment(ctx, rejected, reserved.Revision, fence); !errors.Is(err, organization.ErrFeatureRevisionConflict) {
		t.Fatalf("duplicate append: %v", err)
	}
}
