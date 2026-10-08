//go:build mongo_integration

package operationalruntime

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/filesystem"
	"github.com/tekroo-ai/teams/adapters/mongo"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Real reconciliation and transactional persistence over explicitly seeded
// terminal projections. No worker, listener or model exists in this fixture;
// it qualifies rejection/expiry, not PM execution or kernel source admission.
func TestAmendmentCoordinatorRetainsOperatorDecisionWithoutAnotherAssessment(t *testing.T) {
	process, uri := startRuntimeMongod(t)
	defer stopRuntimeMongod(process)
	for _, tc := range []struct{ name, code string }{
		{"malformed", "INVALID_PROPOSAL_SCHEMA"},
		{"failed", "ASSESSMENT_FAILED"},
		{"expired", "DEADLINE_EXPIRED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			database := "amendment_rejection_" + tc.name
			config := mongo.Config{URI: uri, Database: database, ContractIdentity: kernel.ContractIdentity, ManifestSHA256: ManifestSHA256, MigrationLevel: 1, Policy: integratedPolicy(), BacklogLimit: 1024, DeliveryPolicyRevision: 1}
			store, err := mongo.Open(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close(contextWithTimeout(t)) })
			client, err := driver.Connect(options.Client().ApplyURI(uri))
			if err != nil {
				t.Fatal(err)
			}
			defer client.Disconnect(ctx)
			db := client.Database(database)
			put := func(collection string, document bson.M) {
				t.Helper()
				if _, err := db.Collection(collection).InsertOne(ctx, document); err != nil {
					t.Fatal(err)
				}
			}
			key := func(ref kernel.AggregateRef) string { return string(ref.Kind) + ":" + string(ref.ID) }
			seedState := func(state kernel.AggregateState) {
				t.Helper()
				ref := kernel.AggregateRef{Kind: state.Kind, ID: state.ID}
				put("aggregates", bson.M{"_id": key(ref), "revision": state.Revision, "state": []byte(mustJSON(state))})
				put("events", bson.M{"_id": string(deterministicOperationalUUID("rejection-head", key(ref))), "aggregate_key": key(ref), "revision": state.Revision})
			}
			now := time.Now().UTC().Truncate(time.Millisecond)
			story := organization.PlannedStory{ID: id(42001), Title: "Accepted requirement", Description: "Unchanged product", AcceptanceCriteria: []string{"works"}, Priority: organization.PriorityNormal}
			task := organization.PlannedTask{ID: id(42002), StoryID: story.ID, Title: "Bounded implementation", Description: "Implement accepted behavior", AcceptanceCriteria: []string{"verified"}, Owner: "teams::coder-1", ModelProfile: repeatedDigest('a'), Purpose: kernel.PurposeImplementation, DecisionRoute: kernel.RouteBoundedExecution, Complexity: 2, Risk: organization.RiskLow, AttemptLimit: 2, ReviewRoundLimit: 1, WriteScope: []string{"service/consumer.go"}}
			validator := task
			validator.ID, validator.Owner, validator.Purpose, validator.WriteScope = id(42003), "teams::tester-1", kernel.PurposeValidation, nil
			validator.DependsOn, validator.Validates = []kernel.UUIDv7{task.ID}, []kernel.UUIDv7{task.ID}
			pmExecution := kernel.ExecutionTuple{ExecutionID: id(42004), FencingEpoch: 1}
			plan := organization.FeaturePlan{Version: 1, PreparedBy: "teams::project-manager-1", PreparedExecution: pmExecution, Architecture: "Unchanged technical design", SourceDesignDigest: repeatedDigest('b'), Stories: []organization.PlannedStory{story}, Tasks: []organization.PlannedTask{task, validator}, CreatedAt: now.Add(-time.Minute)}
			feature := organization.FeatureRequest{SchemaVersion: organization.FeatureSchemaVersion, ID: id(42005), Revision: 6, SubmittedBy: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"}, Input: organization.FeatureRequestInput{IdempotencyKey: tc.name, Team: "teams", Title: "Feature", Description: "Accepted requirement", AcceptanceCriteria: []string{"works"}, Priority: organization.PriorityNormal, Repository: "teams", WorkspaceID: "operator-1", MaximumStories: 2, MaximumTasks: 8, MaximumHops: 16}, Status: organization.FeaturePlanned, OperatorActor: "teams::operator-1", ProductOwnerActor: "teams::product-owner-1", InitialMessageID: id(42006), LastMessageID: id(42007), LastStepID: id(42008), LastHop: 4, BudgetAccountID: id(42009), LifecycleEpoch: 1, ScopeRevision: 1, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Minute), Plan: &plan,
				Specification: &organization.FeatureSpecification{PreparedBy: plan.PreparedBy, PreparedExecution: pmExecution, Stories: plan.Stories, PreparedAt: now.Add(-time.Minute)},
				Design:        &organization.FeatureDesignCandidate{PreparedBy: "teams::architect-1", PreparedExecution: pmExecution, OutputDigest: plan.SourceDesignDigest, PreparedAt: now.Add(-time.Minute)}}
			blobs, err := filesystem.NewExecutionEvidenceStore(filepath.Join(t.TempDir(), "evidence"))
			if err != nil {
				t.Fatal(err)
			}
			w := organization.PlanAmendmentWitness{Tasks: map[kernel.UUIDv7]kernel.AggregateState{}, Invocations: map[kernel.AggregateRef]kernel.WorkInvocation{}, Evidence: map[kernel.UUIDv7]kernel.EvidenceMetadata{}, Now: now, ScopeChangeRequired: true}
			for _, state := range []kernel.AggregateState{
				{Kind: kernel.AggregateTask, ID: task.ID, Revision: 10, LifecycleEpoch: 1, ScopeRevision: 1, Phase: kernel.PhaseActive, Condition: kernel.ConditionBlocked},
				{Kind: kernel.AggregateTask, ID: validator.ID, Revision: 2, LifecycleEpoch: 1, ScopeRevision: 1, Phase: kernel.PhasePlanned, Condition: kernel.ConditionRunnable},
				{Kind: kernel.AggregateTask, ID: id(42010), Revision: 10, LifecycleEpoch: 1, ScopeRevision: 1, Phase: kernel.PhaseCompleted, Condition: kernel.ConditionRunnable},
			} {
				seedState(state)
				w.Tasks[state.ID] = state
			}
			seedInvocation := func(inv kernel.WorkInvocation, evidenceID kernel.UUIDv7, body []byte) kernel.WorkInvocation {
				t.Helper()
				inv.DeadlineAt, inv.BudgetAccountID, inv.AttemptOrdinal = now.Add(time.Hour), feature.BudgetAccountID, 1
				inv.AuthorizationEventID, inv.ParentEventID, inv.LastEventID = deterministicOperationalUUID("rejection-auth", string(inv.ID)), deterministicOperationalUUID("rejection-parent", string(inv.ID)), deterministicOperationalUUID("rejection-head", key(inv.Ref()))
				if body != nil {
					if _, err := blobs.Put(ctx, evidenceID, body); err != nil {
						t.Fatal(err)
					}
					digest := digestBytes(body)
					inv.OutputDigest, inv.TerminalEvidenceIDs = &digest, []kernel.UUIDv7{evidenceID}
					put("evidence", bson.M{"_id": string(evidenceID), "sha256": string(digest), "available": true})
					w.Evidence[evidenceID] = kernel.EvidenceMetadata{Available: true, SHA256: digest}
				}
				if !inv.Valid() {
					t.Fatal("invalid seeded invocation", inv.ID)
				}
				seedState(kernel.AggregateState{Kind: kernel.AggregateWorkInvocation, ID: inv.ID, Revision: inv.Revision, LifecycleEpoch: 1, ScopeRevision: 1, Phase: kernel.PhaseCompleted, Condition: kernel.ConditionRunnable})
				put("work_invocations", bson.M{"_id": key(inv.Ref()), "data": []byte(mustJSON(map[string]any{"reference": inv.Ref(), "invocation": inv}))})
				w.Invocations[inv.Ref()] = inv
				return inv
			}
			blocked := recoveryTerminalFixture(kernel.InvocationSucceeded, nil, nil)
			blocked.ID, blocked.TaskID, blocked.ActorFQN, blocked.Purpose = id(42011), task.ID, task.Owner, task.Purpose
			blocked = seedInvocation(blocked, id(42012), []byte(application.OrganizationalResultMarker+`{"outcome":"needs_decision","question":"Who owns the compatibility wiring?"}`))
			consult := recoveryTerminalFixture(kernel.InvocationSucceeded, nil, nil)
			consult.ID, consult.TaskID, consult.ActorFQN, consult.Purpose = id(42013), id(42010), feature.Design.PreparedBy, kernel.PurposeHandoff
			consult = seedInvocation(consult, id(42014), []byte(application.OrganizationalResultMarker+`{"disposition":"NEEDS_SCOPE_CHANGE","answer":"Repair execution ownership within the accepted design."}`))
			w.Consultation = consult
			limits, used := kernel.PurposeCounters{}, kernel.PurposeCounters{}
			for _, purpose := range kernel.AllWorkPurposes {
				limits[purpose], used[purpose] = 10, 0
			}
			w.Budget = kernel.WorkBudgetAccount{ID: feature.BudgetAccountID, Revision: 1, RootWork: kernel.AggregateRef{Kind: kernel.AggregateStory, ID: story.ID}, LifecycleEpoch: 1, PolicyRevision: 1, PolicyDigest: repeatedDigest('e'), ModelInvocationLimit: 20, ModelInvocationsUsed: 5, PurposeLimits: limits, PurposeUsed: used, DeadlineAt: now.Add(time.Hour), LastEventID: id(42015)}
			seedState(kernel.AggregateState{Kind: kernel.AggregateWorkBudget, ID: w.Budget.ID, Revision: 1, LifecycleEpoch: 1, ScopeRevision: 1, Phase: kernel.PhaseActive, Condition: kernel.ConditionRunnable})
			put("work_budget_accounts", bson.M{"_id": key(w.Budget.Ref()), "data": []byte(mustJSON(map[string]any{"reference": w.Budget.Ref(), "account": w.Budget}))})
			request := organization.PlanAmendmentReservation{AssessmentID: planAmendmentAssessmentID(feature.ID), BlockedTaskID: task.ID, BlockedTaskRevision: 10, BlockedInvocationID: blocked.ID, ConsultationID: consult.TaskID, AnswerDigest: *consult.OutputDigest}
			reserved, err := organization.ReservePlanAmendment(feature, request, w)
			if err != nil {
				t.Fatal(err)
			}
			if tc.name != "expired" {
				seedState(kernel.AggregateState{Kind: kernel.AggregateTask, ID: request.AssessmentID, Revision: 10, LifecycleEpoch: 1, ScopeRevision: 1, Phase: kernel.PhaseCompleted, Condition: kernel.ConditionRunnable})
				assessment := recoveryTerminalFixture(kernel.InvocationSucceeded, nil, nil)
				assessment.ID, assessment.TaskID, assessment.ActorFQN, assessment.Execution, assessment.Purpose = id(42016), request.AssessmentID, plan.PreparedBy, pmExecution, kernel.PurposeHandoff
				body := []byte(application.OrganizationalResultMarker + `{"successor_plan":"{}"}`)
				if tc.name == "failed" {
					assessment.State, body = kernel.InvocationStartFailed, nil
				}
				seedInvocation(assessment, id(42017), body)
			}
			put("feature_requests", bson.M{"_id": string(feature.ID), "revision": reserved.Revision, "status": reserved.Status, "submitted_by_kind": feature.SubmittedBy.Kind, "submitted_by_id": feature.SubmittedBy.ID, "idempotency_key": tc.name, "created_at": reserved.CreatedAt, "updated_at": reserved.UpdatedAt, "data": []byte(mustJSON(reserved))})
			clock := fixedClock{now: now.Add(time.Second)}
			if tc.name == "expired" {
				clock.now = w.Budget.DeadlineAt.Add(time.Second)
			}
			policy := organization.PlanAmendmentPolicy{ContractVersion: organization.PlanAmendmentContractVersion, ManifestDigest: repeatedDigest('f'), Authority: kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "amendment-policy"}, AllowedRoots: []string{"service"}}
			service := &ProductionService{Store: store, Runtime: &Runtime{evidence: blobs}, clock: clock, planning: ProductionPlanning{PlanAmendment: &ProductionPlanAmendment{Policy: policy}}}
			if handled, err := service.reconcilePlanAmendment(ctx, reserved); !handled || err != nil {
				t.Fatal("terminal reconciliation", handled, err)
			}
			stored, found, err := store.LoadFeature(ctx, feature.ID)
			if err != nil || !found || stored.PlanAmendment.State != organization.AmendmentRejected || stored.PlanAmendment.Decision.Code != tc.code || !reflect.DeepEqual(stored.Plan, feature.Plan) {
				t.Fatal("operator decision not retained", err, stored.PlanAmendment)
			}
			before := organization.PlanAmendmentDigest(stored)
			count, err := db.Collection("work_invocations").CountDocuments(ctx, bson.M{})
			if err != nil {
				t.Fatal(err)
			}
			// Reopen the persistence boundary, then replay the normal reconciler.
			reopened, err := mongo.Open(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close(ctx)
			service.Store = reopened
			for pass := 0; pass < 2; pass++ {
				if handled, err := service.reconcilePlanAmendment(ctx, stored); !handled || err != nil {
					t.Fatal("replay", handled, err)
				}
			}
			after, _, err := reopened.LoadFeature(ctx, feature.ID)
			afterCount, countErr := db.Collection("work_invocations").CountDocuments(ctx, bson.M{})
			if err != nil || countErr != nil || organization.PlanAmendmentDigest(after) != before || afterCount != count {
				t.Fatal("replay changed decision or created work", err, countErr)
			}
			if _, err := organization.ReservePlanAmendment(after, organization.PlanAmendmentReservation{AssessmentID: id(42018)}, w); err == nil {
				t.Fatal("rejection obtained a second assessment")
			}
			t.Log(fmt.Sprintf("operator receipt: assessment=%s state=%s code=%s; invocations=%d unchanged on restart", after.PlanAmendment.AssessmentID, after.PlanAmendment.State, after.PlanAmendment.Decision.Code, count))
		})
	}
}
