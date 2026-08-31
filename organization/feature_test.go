package organization_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestFeatureSubmissionRoutesAtomicallyToExactProductOwner(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	operator := activeRole("teams::operator-1", "operator", featureUUID(91), featureDigest('3'))
	productOwner := activeRole("teams::product-owner-1", "product-owner", featureUUID(92), featureDigest('4'))
	store := &featureStoreFake{}
	host := &featureHostFake{roles: map[kernel.ActorFQN]organization.RoleInstanceState{operator.ActorFQN: operator, productOwner.ActorFQN: productOwner}}
	coordinator, err := organization.NewFeatureCoordinator(store, host, materializerFake{}, fixedClock(now), &idQueue{ids: []kernel.UUIDv7{featureUUID(1), featureUUID(2), featureUUID(3), featureUUID(4)}})
	if err != nil {
		t.Fatal(err)
	}
	input := featureInput()
	principal := kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "paul"}
	feature, created, err := coordinator.Submit(context.Background(), principal, input)
	if err != nil || !created {
		t.Fatalf("submit created=%t err=%v", created, err)
	}
	if feature.ProductOwnerActor != productOwner.ActorFQN || store.message.Recipient != productOwner.ActorFQN || store.message.Sender != operator.ActorFQN || store.message.Work.FeatureID == nil || *store.message.Work.FeatureID != feature.ID {
		t.Fatalf("feature=%#v message=%#v", feature, store.message)
	}
	if store.message.Flow.MaximumHops != input.MaximumHops || store.message.Flow.BudgetAccountID != feature.BudgetAccountID {
		t.Fatalf("flow=%#v feature=%#v", store.message.Flow, feature)
	}
	again, created, err := coordinator.Submit(context.Background(), principal, input)
	if err != nil || created || again.ID != feature.ID {
		t.Fatalf("idempotent submit created=%t err=%v feature=%#v", created, err, again)
	}
}

func TestFeaturePlanRejectsCyclesAndMaterializesFiniteDAG(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	operator := activeRole("teams::operator-1", "operator", featureUUID(91), featureDigest('3'))
	productOwner := activeRole("teams::product-owner-1", "product-owner", featureUUID(92), featureDigest('4'))
	planner := activeRole("teams::architect-1", "architect", featureUUID(93), featureDigest('1'))
	owner := activeRole("teams::coder-1", "coder", featureUUID(94), featureDigest('2'))
	feature := organization.FeatureRequest{SchemaVersion: organization.FeatureSchemaVersion, ID: featureUUID(1), Revision: 1, SubmittedBy: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "paul"}, Input: featureInput(), Status: organization.FeatureSubmitted, OperatorActor: operator.ActorFQN, ProductOwnerActor: productOwner.ActorFQN, InitialMessageID: featureUUID(2), LastMessageID: featureUUID(2), LastStepID: featureUUID(3), LastHop: 1, BudgetAccountID: featureUUID(4), LifecycleEpoch: 1, ScopeRevision: 1, CreatedAt: now, UpdatedAt: now}
	store := &featureStoreFake{feature: feature}
	host := &featureHostFake{roles: map[kernel.ActorFQN]organization.RoleInstanceState{operator.ActorFQN: operator, productOwner.ActorFQN: productOwner, planner.ActorFQN: planner, owner.ActorFQN: owner}}
	materializer := &recordingMaterializer{}
	coordinator, err := organization.NewFeatureCoordinator(store, host, materializer, fixedClock(now.Add(time.Minute)), &idQueue{})
	if err != nil {
		t.Fatal(err)
	}
	storyID, firstTask, secondTask := featureUUID(10), featureUUID(11), featureUUID(12)
	plan := organization.FeaturePlan{Version: 1, PreparedBy: planner.ActorFQN, PreparedExecution: planner.Execution, Architecture: "One bounded implementation pipeline.", Stories: []organization.PlannedStory{{ID: storyID, Title: "Story", Description: "Deliver the feature.", AcceptanceCriteria: []string{"accepted"}, Priority: organization.PriorityHigh}}, Tasks: []organization.PlannedTask{
		{ID: firstTask, StoryID: storyID, Title: "First", Description: "Implement.", AcceptanceCriteria: []string{"passes"}, Owner: owner.ActorFQN, ModelProfile: owner.ModelProfile, DecisionRoute: kernel.RouteBoundedExecution, Purpose: kernel.PurposeImplementation, Complexity: 3, Risk: organization.RiskLow, CriticalPath: true, AttemptLimit: 2, ReviewRoundLimit: 1},
		{ID: secondTask, StoryID: storyID, Title: "Second", Description: "Verify.", AcceptanceCriteria: []string{"verified"}, DependsOn: []kernel.UUIDv7{firstTask}, Owner: owner.ActorFQN, ModelProfile: owner.ModelProfile, DecisionRoute: kernel.RouteBoundedExecution, Purpose: kernel.PurposeValidation, Complexity: 2, Risk: organization.RiskModerate, CriticalPath: true, AttemptLimit: 2, ReviewRoundLimit: 1},
	}, CreatedAt: now.Add(time.Minute)}
	feature.Status = organization.FeatureSpecified
	feature.Specification = &organization.FeatureSpecification{PreparedBy: "teams::project-manager-1", PreparedExecution: kernel.ExecutionTuple{ExecutionID: featureUUID(95), FencingEpoch: 1}, Stories: plan.Stories, PreparedAt: now.Add(time.Minute)}
	store.feature = feature
	planned, err := coordinator.ApplyPlan(context.Background(), feature.ID, 1, plan)
	if err != nil || materializer.calls != 1 || planned.Status != organization.FeaturePlanned || len(planned.Plan.Tasks) != 2 {
		t.Fatalf("apply calls=%d feature=%#v err=%v", materializer.calls, planned, err)
	}
	cycle := plan
	cycle.Tasks = append([]organization.PlannedTask(nil), plan.Tasks...)
	cycle.Tasks[0].DependsOn = []kernel.UUIDv7{secondTask}
	if cycle.Validate(feature) == nil {
		t.Fatal("cyclic task plan was accepted")
	}
}

func TestFeatureRoleHandoffsFormFiniteProductOwnerProjectManagerArchitectDAG(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	operator := activeRole("teams::operator-1", "operator", featureUUID(71), featureDigest('3'))
	productOwner := activeRole("teams::product-owner-1", "product-owner", featureUUID(72), featureDigest('4'))
	projectManager := activeRole("teams::project-manager-1", "project-manager", featureUUID(73), featureDigest('5'))
	architect := activeRole("teams::architect-1", "architect", featureUUID(74), featureDigest('1'))
	feature := organization.FeatureRequest{SchemaVersion: organization.FeatureSchemaVersion, ID: featureUUID(1), Revision: 1, SubmittedBy: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "paul"}, Input: featureInput(), Status: organization.FeatureSubmitted, OperatorActor: operator.ActorFQN, ProductOwnerActor: productOwner.ActorFQN, InitialMessageID: featureUUID(2), LastMessageID: featureUUID(2), LastStepID: featureUUID(3), LastHop: 1, BudgetAccountID: featureUUID(4), LifecycleEpoch: 1, ScopeRevision: 1, CreatedAt: now, UpdatedAt: now}
	store := &featureStoreFake{feature: feature}
	host := &featureHostFake{roles: map[kernel.ActorFQN]organization.RoleInstanceState{operator.ActorFQN: operator, productOwner.ActorFQN: productOwner, projectManager.ActorFQN: projectManager, architect.ActorFQN: architect}}
	coordinator, err := organization.NewFeatureCoordinator(store, host, materializerFake{}, fixedClock(now.Add(time.Minute)), &idQueue{ids: []kernel.UUIDv7{featureUUID(20), featureUUID(21), featureUUID(22), featureUUID(23)}})
	if err != nil {
		t.Fatal(err)
	}
	refinement := organization.FeatureRefinement{PreparedBy: productOwner.ActorFQN, PreparedExecution: productOwner.Execution, AcceptanceCriteria: []string{"finite DAG"}, Priority: organization.PriorityHigh, PreparedAt: now.Add(time.Minute)}
	refined, err := coordinator.Refine(context.Background(), feature.ID, 1, refinement)
	if err != nil || refined.Status != organization.FeatureReadyForPlanning || store.message.Recipient != projectManager.ActorFQN || store.message.Flow.Hop != 2 {
		t.Fatalf("refine feature=%#v message=%#v err=%v", refined, store.message, err)
	}
	story := organization.PlannedStory{ID: featureUUID(30), Title: "Restore runtime", Description: "Implement the bounded team path.", AcceptanceCriteria: []string{"works"}, Priority: organization.PriorityHigh}
	specification := organization.FeatureSpecification{PreparedBy: projectManager.ActorFQN, PreparedExecution: projectManager.Execution, Stories: []organization.PlannedStory{story}, PreparedAt: now.Add(2 * time.Minute)}
	specified, err := coordinator.Specify(context.Background(), feature.ID, 2, specification)
	if err != nil || specified.Status != organization.FeatureSpecified || store.message.Recipient != architect.ActorFQN || store.message.Type != "tekroo.message.story.design-requested" || store.message.Flow.Hop != 3 {
		t.Fatalf("specify feature=%#v message=%#v err=%v", specified, store.message, err)
	}
}

type featureStoreFake struct {
	feature organization.FeatureRequest
	message organization.OrganizationalMessage
}

func (store *featureStoreFake) CreateFeature(_ context.Context, feature organization.FeatureRequest, message organization.OrganizationalMessage) (organization.FeatureRequest, bool, error) {
	if store.feature.ID.Valid() {
		return store.feature, false, nil
	}
	store.feature, store.message = feature, message
	return feature, true, nil
}
func (store *featureStoreFake) LoadFeature(_ context.Context, id kernel.UUIDv7) (organization.FeatureRequest, bool, error) {
	return store.feature, store.feature.ID == id, nil
}
func (store *featureStoreFake) LoadFeatureByIdempotencyKey(_ context.Context, principal kernel.PrincipalRef, key string) (organization.FeatureRequest, bool, error) {
	found := store.feature.SubmittedBy == principal && store.feature.Input.IdempotencyKey == key
	return store.feature, found, nil
}
func (store *featureStoreFake) ApplyFeaturePlan(_ context.Context, id kernel.UUIDv7, revision uint64, plan organization.FeaturePlan, now time.Time) (organization.FeatureRequest, error) {
	if store.feature.ID != id || store.feature.Revision != revision {
		return organization.FeatureRequest{}, organization.ErrFeatureRevisionConflict
	}
	store.feature.Revision++
	store.feature.Status = organization.FeaturePlanned
	store.feature.Plan = &plan
	store.feature.UpdatedAt = now
	return store.feature, nil
}
func (store *featureStoreFake) AdvanceFeature(_ context.Context, next organization.FeatureRequest, revision uint64, message *organization.OrganizationalMessage) (organization.FeatureRequest, error) {
	if store.feature.ID != next.ID || store.feature.Revision != revision {
		return organization.FeatureRequest{}, organization.ErrFeatureRevisionConflict
	}
	store.feature = next
	if message != nil {
		store.message = *message
	}
	return next, nil
}

type featureHostFake struct {
	roles map[kernel.ActorFQN]organization.RoleInstanceState
}

func (host *featureHostFake) ConfiguredRoleActors(role string) ([]kernel.ActorFQN, error) {
	if role == "operator" {
		return []kernel.ActorFQN{"teams::operator-1"}, nil
	}
	return nil, organization.ErrRoleNotConfigured
}
func (host *featureHostFake) EnsureStarted(_ context.Context, actor kernel.ActorFQN) (organization.RoleInstanceState, error) {
	state, found := host.roles[actor]
	if !found {
		return state, organization.ErrRoleNotRunning
	}
	return state, nil
}
func (host *featureHostFake) ResolveRoleRecipients(_ context.Context, role string) ([]kernel.ActorFQN, error) {
	switch role {
	case "product-owner":
		return []kernel.ActorFQN{"teams::product-owner-1"}, nil
	case "project-manager":
		return []kernel.ActorFQN{"teams::project-manager-1"}, nil
	case "architect":
		return []kernel.ActorFQN{"teams::architect-1"}, nil
	}
	return nil, organization.ErrRoleNotRunning
}
func (host *featureHostFake) Status(_ context.Context, actor kernel.ActorFQN) (organization.RoleInstanceState, bool, error) {
	state, found := host.roles[actor]
	return state, found, nil
}

type materializerFake struct{}

func (materializerFake) MaterializeFeaturePlan(context.Context, organization.FeatureRequest, organization.FeaturePlan) error {
	return nil
}

type recordingMaterializer struct{ calls int }

func (materializer *recordingMaterializer) MaterializeFeaturePlan(context.Context, organization.FeatureRequest, organization.FeaturePlan) error {
	materializer.calls++
	return nil
}

type fixedClock time.Time

func (clock fixedClock) Now() time.Time { return time.Time(clock) }

type idQueue struct {
	ids   []kernel.UUIDv7
	index int
}

func (source *idQueue) Next() (kernel.UUIDv7, error) {
	if source.index >= len(source.ids) {
		return "", errors.New("no IDs")
	}
	id := source.ids[source.index]
	source.index++
	return id, nil
}

func featureInput() organization.FeatureRequestInput {
	return organization.FeatureRequestInput{IdempotencyKey: "feature-1", Team: "teams", Title: "Restore team workflow", Description: "Recover the productive organizational path.", AcceptanceCriteria: []string{"a finite DAG is created"}, Priority: organization.PriorityHigh, Constraints: []string{"no direct model chaining"}, Repository: "/work/teams", WorkspaceID: "operator-1", MaximumStories: 4, MaximumTasks: 16, MaximumHops: 16}
}
func activeRole(actor kernel.ActorFQN, role string, execution kernel.UUIDv7, model kernel.Digest) organization.RoleInstanceState {
	return organization.RoleInstanceState{ActorFQN: actor, Team: "teams", Role: role, Instance: 1, Revision: 1, BundleDigest: featureDigest('a'), ModelProfile: model, WorkspaceID: role + "-1", Status: organization.RoleIdle, Execution: kernel.ExecutionTuple{ExecutionID: execution, FencingEpoch: 1}, ManifestDigest: featureDigest('b'), ManifestVersion: "1.0.0", BundleVersion: "1.0.0", RuntimeGeneration: 1}
}

func featureUUID(value int) kernel.UUIDv7 {
	return kernel.UUIDv7(fmt.Sprintf("00000000-0000-7000-8000-%012d", value))
}
func featureDigest(character byte) kernel.Digest {
	return kernel.Digest(strings.Repeat(string(character), 64))
}
