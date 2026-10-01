//go:build mongo_integration

package mongo

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestApplyFeaturePlanAcceptsCoordinatorPlanningStates(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     organization.FeatureStatus
		withDesign bool
	}{
		{name: "legacy specified", status: organization.FeatureSpecified},
		{name: "PM finalizes architect design", status: organization.FeatureDesigned, withDesign: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := openTestStore(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			created := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
			story := organization.PlannedStory{ID: testUUID(9101), Title: "Alias", Description: "Bind and resolve an alias.", AcceptanceCriteria: []string{"Alias resolves to the same actor."}, Priority: organization.PriorityNormal}
			specification := organization.FeatureSpecification{PreparedBy: "teams::project-manager-1", PreparedExecution: kernel.ExecutionTuple{ExecutionID: testUUID(9102), FencingEpoch: 1}, Stories: []organization.PlannedStory{story}, PreparedAt: created.Add(time.Minute)}
			feature := organization.FeatureRequest{SchemaVersion: organization.FeatureSchemaVersion, ID: testUUID(9103), Revision: 4, SubmittedBy: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"}, Input: organization.FeatureRequestInput{IdempotencyKey: "plan-state-regression", Team: "teams", Title: "Alias", Description: "Bind and resolve an alias.", AcceptanceCriteria: []string{"Alias resolves to the same actor."}, Priority: organization.PriorityNormal, Repository: "teams", WorkspaceID: "operator-1", MaximumStories: 2, MaximumTasks: 4, MaximumHops: 8}, Status: tc.status, OperatorActor: "teams::operator-1", ProductOwnerActor: "teams::product-owner-1", InitialMessageID: testUUID(9104), LastMessageID: testUUID(9105), LastStepID: testUUID(9106), LastHop: 4, BudgetAccountID: testUUID(9107), LifecycleEpoch: 1, ScopeRevision: 1, CreatedAt: created, UpdatedAt: created.Add(2 * time.Minute), Specification: &specification}
			planner := kernel.ActorFQN("teams::architect-1")
			plan := organization.FeaturePlan{Version: 1, PreparedBy: planner, PreparedExecution: kernel.ExecutionTuple{ExecutionID: testUUID(9108), FencingEpoch: 1}, Architecture: "Use one bounded alias store.", Stories: []organization.PlannedStory{story}, CreatedAt: created.Add(3 * time.Minute)}
			if tc.withDesign {
				feature.Design = &organization.FeatureDesignCandidate{PreparedBy: "teams::architect-1", PreparedExecution: kernel.ExecutionTuple{ExecutionID: testUUID(9109), FencingEpoch: 1}, OutputDigest: kernel.Digest(strings.Repeat("a", 64)), PreparedAt: created.Add(2 * time.Minute)}
				plan.PreparedBy = "teams::project-manager-1"
				plan.PreparedExecution = specification.PreparedExecution
				plan.SourceDesignDigest = feature.Design.OutputDigest
			}
			implementation := organization.PlannedTask{ID: testUUID(9110), StoryID: story.ID, Title: "Implement", Description: "Bind aliases.", AcceptanceCriteria: []string{"Binding works."}, Owner: "teams::coder-1", ModelProfile: kernel.Digest(strings.Repeat("b", 64)), DecisionRoute: kernel.RouteBoundedExecution, Purpose: kernel.PurposeImplementation, Complexity: 2, Risk: organization.RiskLow, AttemptLimit: 2, ReviewRoundLimit: 1}
			validation := organization.PlannedTask{ID: testUUID(9111), StoryID: story.ID, Title: "Validate", Description: "Verify binding.", AcceptanceCriteria: []string{"Binding verified."}, DependsOn: []kernel.UUIDv7{implementation.ID}, Validates: []kernel.UUIDv7{implementation.ID}, Owner: "teams::tester-1", ModelProfile: kernel.Digest(strings.Repeat("c", 64)), DecisionRoute: kernel.RouteBoundedExecution, Purpose: kernel.PurposeValidation, Complexity: 2, Risk: organization.RiskLow, AttemptLimit: 2, ReviewRoundLimit: 1}
			plan.Tasks = []organization.PlannedTask{implementation, validation}
			if err := feature.Validate(); err != nil {
				t.Fatalf("feature fixture: %v", err)
			}
			if err := plan.Validate(feature); err != nil {
				t.Fatalf("plan fixture: %v", err)
			}
			raw, err := json.Marshal(feature)
			if err != nil {
				t.Fatal(err)
			}
			_, err = store.db.Collection("feature_requests").InsertOne(ctx, featureDocument{ID: string(feature.ID), Revision: feature.Revision, SubmittedByKind: feature.SubmittedBy.Kind, SubmittedByID: feature.SubmittedBy.ID, IdempotencyKey: feature.Input.IdempotencyKey, Status: feature.Status, CreatedAt: feature.CreatedAt, UpdatedAt: feature.UpdatedAt, Data: raw})
			if err != nil {
				t.Fatal(err)
			}
			applied, err := store.ApplyFeaturePlan(ctx, feature.ID, 4, plan, created.Add(3*time.Minute))
			if err != nil || applied.Status != organization.FeaturePlanned || applied.Revision != 5 || applied.Plan == nil {
				t.Fatalf("apply feature=%#v err=%v", applied, err)
			}
			stored, found, err := store.LoadFeature(ctx, feature.ID)
			if err != nil || !found || stored.Status != organization.FeaturePlanned || stored.Revision != 5 || stored.Plan == nil {
				t.Fatalf("stored feature=%#v found=%t err=%v", stored, found, err)
			}
			if _, err := store.ApplyFeaturePlan(ctx, feature.ID, 4, plan, created.Add(4*time.Minute)); !errors.Is(err, organization.ErrFeatureRevisionConflict) {
				t.Fatalf("stale revision error=%v", err)
			}
		})
	}
}
