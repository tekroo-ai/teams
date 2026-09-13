package organization_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	validator := activeRole("teams::tester-1", "tester", featureUUID(96), featureDigest('8'))
	feature := organization.FeatureRequest{SchemaVersion: organization.FeatureSchemaVersion, ID: featureUUID(1), Revision: 1, SubmittedBy: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "paul"}, Input: featureInput(), Status: organization.FeatureSubmitted, OperatorActor: operator.ActorFQN, ProductOwnerActor: productOwner.ActorFQN, InitialMessageID: featureUUID(2), LastMessageID: featureUUID(2), LastStepID: featureUUID(3), LastHop: 1, BudgetAccountID: featureUUID(4), LifecycleEpoch: 1, ScopeRevision: 1, CreatedAt: now, UpdatedAt: now}
	store := &featureStoreFake{feature: feature}
	host := &featureHostFake{roles: map[kernel.ActorFQN]organization.RoleInstanceState{operator.ActorFQN: operator, productOwner.ActorFQN: productOwner, planner.ActorFQN: planner, owner.ActorFQN: owner, validator.ActorFQN: validator}}
	materializer := &recordingMaterializer{}
	coordinator, err := organization.NewFeatureCoordinator(store, host, materializer, fixedClock(now.Add(time.Minute)), &idQueue{})
	if err != nil {
		t.Fatal(err)
	}
	storyID, firstTask, secondTask := featureUUID(10), featureUUID(11), featureUUID(12)
	plan := organization.FeaturePlan{Version: 1, PreparedBy: planner.ActorFQN, PreparedExecution: planner.Execution, Architecture: "One bounded implementation pipeline.", Stories: []organization.PlannedStory{{ID: storyID, Title: "Story", Description: "Deliver the feature.", AcceptanceCriteria: []string{"accepted"}, Priority: organization.PriorityHigh}}, Tasks: []organization.PlannedTask{
		{ID: firstTask, StoryID: storyID, Title: "First", Description: "Implement.", AcceptanceCriteria: []string{"passes"}, Owner: owner.ActorFQN, ModelProfile: owner.ModelProfile, DecisionRoute: kernel.RouteBoundedExecution, Purpose: kernel.PurposeImplementation, Complexity: 3, Risk: organization.RiskLow, CriticalPath: true, AttemptLimit: 2, ReviewRoundLimit: 1},
		{ID: secondTask, StoryID: storyID, Title: "Second", Description: "Verify.", AcceptanceCriteria: []string{"verified"}, DependsOn: []kernel.UUIDv7{firstTask}, Validates: []kernel.UUIDv7{firstTask}, Owner: validator.ActorFQN, ModelProfile: validator.ModelProfile, DecisionRoute: kernel.RouteBoundedExecution, Purpose: kernel.PurposeValidation, Complexity: 2, Risk: organization.RiskModerate, CriticalPath: true, AttemptLimit: 2, ReviewRoundLimit: 1},
	}, CreatedAt: now.Add(time.Minute)}
	feature.Status = organization.FeatureSpecified
	feature.Specification = &organization.FeatureSpecification{PreparedBy: "teams::project-manager-1", PreparedExecution: kernel.ExecutionTuple{ExecutionID: featureUUID(95), FencingEpoch: 1}, Stories: plan.Stories, PreparedAt: now.Add(time.Minute)}
	store.feature = feature
	restartedPlanner := planner
	restartedPlanner.Execution = kernel.ExecutionTuple{ExecutionID: featureUUID(97), FencingEpoch: planner.Execution.FencingEpoch + 1}
	host.roles[planner.ActorFQN] = restartedPlanner
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
	restartedProductOwner := productOwner
	restartedProductOwner.Execution = kernel.ExecutionTuple{ExecutionID: featureUUID(75), FencingEpoch: productOwner.Execution.FencingEpoch + 1}
	host.roles[productOwner.ActorFQN] = restartedProductOwner
	refined, err := coordinator.Refine(context.Background(), feature.ID, 1, refinement)
	if err != nil || refined.Status != organization.FeatureReadyForPlanning || store.message.Recipient != projectManager.ActorFQN || store.message.Flow.Hop != 2 {
		t.Fatalf("refine feature=%#v message=%#v err=%v", refined, store.message, err)
	}
	story := organization.PlannedStory{ID: featureUUID(30), Title: "Restore runtime", Description: "Implement the bounded team path.", AcceptanceCriteria: []string{"works"}, Priority: organization.PriorityHigh}
	specification := organization.FeatureSpecification{PreparedBy: projectManager.ActorFQN, PreparedExecution: projectManager.Execution, Stories: []organization.PlannedStory{story}, PreparedAt: now.Add(2 * time.Minute)}
	restartedProjectManager := projectManager
	restartedProjectManager.Execution = kernel.ExecutionTuple{ExecutionID: featureUUID(76), FencingEpoch: projectManager.Execution.FencingEpoch + 1}
	host.roles[projectManager.ActorFQN] = restartedProjectManager
	specified, err := coordinator.Specify(context.Background(), feature.ID, 2, specification)
	if err != nil || specified.Status != organization.FeatureSpecified || store.message.Recipient != architect.ActorFQN || store.message.Type != "tekroo.message.story.design-requested" || store.message.Flow.Hop != 3 {
		t.Fatalf("specify feature=%#v message=%#v err=%v", specified, store.message, err)
	}
}

func TestFeatureClarificationResponsePreservesQuestionsAndResumesPlanning(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	operator := activeRole("teams::operator-1", "operator", featureUUID(81), featureDigest('3'))
	productOwner := activeRole("teams::product-owner-1", "product-owner", featureUUID(82), featureDigest('4'))
	projectManager := activeRole("teams::project-manager-1", "project-manager", featureUUID(83), featureDigest('5'))
	feature := organization.FeatureRequest{SchemaVersion: organization.FeatureSchemaVersion, ID: featureUUID(1), Revision: 1, SubmittedBy: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "paul"}, Input: featureInput(), Status: organization.FeatureSubmitted, OperatorActor: operator.ActorFQN, ProductOwnerActor: productOwner.ActorFQN, InitialMessageID: featureUUID(2), LastMessageID: featureUUID(2), LastStepID: featureUUID(3), LastHop: 1, BudgetAccountID: featureUUID(4), LifecycleEpoch: 1, ScopeRevision: 1, CreatedAt: now, UpdatedAt: now}
	store := &featureStoreFake{feature: feature}
	host := &featureHostFake{roles: map[kernel.ActorFQN]organization.RoleInstanceState{operator.ActorFQN: operator, productOwner.ActorFQN: productOwner, projectManager.ActorFQN: projectManager}}
	coordinator, err := organization.NewFeatureCoordinator(store, host, materializerFake{}, fixedClock(now.Add(time.Minute)), &idQueue{ids: []kernel.UUIDv7{featureUUID(20), featureUUID(21)}})
	if err != nil {
		t.Fatal(err)
	}
	questions := []string{"Which syntax is canonical?", "Should invalid input fail before launch?"}
	refinement := organization.FeatureRefinement{PreparedBy: productOwner.ActorFQN, PreparedExecution: productOwner.Execution, AcceptanceCriteria: []string{"finite DAG"}, ClarificationQuestions: questions, Priority: organization.PriorityHigh, PreparedAt: now.Add(time.Minute)}
	blocked, err := coordinator.Refine(context.Background(), feature.ID, feature.Revision, refinement)
	if err != nil || blocked.Status != organization.FeatureClarificationRequired || blocked.Revision != 2 {
		t.Fatalf("blocked feature=%#v err=%v", blocked, err)
	}
	if _, err := coordinator.RespondToClarification(context.Background(), feature.ID, kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "someone-else"}, organization.FeatureClarificationResponseInput{ExpectedRevision: 2, Answers: []string{"A", "B"}}); err == nil {
		t.Fatal("different human answered submitting human's clarification")
	}
	resumed, err := coordinator.RespondToClarification(context.Background(), feature.ID, feature.SubmittedBy, organization.FeatureClarificationResponseInput{ExpectedRevision: 2, Answers: []string{"TEAM followed by FQRN instance", "Yes"}})
	if err != nil || resumed.Status != organization.FeatureReadyForPlanning || resumed.Revision != 3 || resumed.Clarification == nil || len(resumed.Clarification.Answers) != 2 {
		t.Fatalf("resumed feature=%#v err=%v", resumed, err)
	}
	if resumed.Clarification.Answers[0].Question != questions[0] || resumed.Clarification.Answers[0].Answer != "TEAM followed by FQRN instance" || store.message.Sender != productOwner.ActorFQN || store.message.Recipient != projectManager.ActorFQN || store.message.Type != "tekroo.message.feature.refined" || store.message.CausationID == nil || *store.message.CausationID != feature.InitialMessageID || store.message.Flow.ParentStepID == nil || *store.message.Flow.ParentStepID != feature.LastStepID {
		t.Fatalf("clarification=%#v message=%#v", resumed.Clarification, store.message)
	}
}

func TestFeaturePlanSupersessionPreservesSpecificationAndRequiresNextPlanVersion(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	planner := activeRole("teams::architect-1", "architect", featureUUID(93), featureDigest('1'))
	owner := activeRole("teams::coder-1", "coder", featureUUID(94), featureDigest('2'))
	validator := activeRole("teams::tester-1", "tester", featureUUID(96), featureDigest('8'))
	story := organization.PlannedStory{ID: featureUUID(10), Title: "Story", Description: "Deliver the feature.", AcceptanceCriteria: []string{"accepted"}, Priority: organization.PriorityHigh}
	firstPlan := organization.FeaturePlan{Version: 1, PreparedBy: planner.ActorFQN, PreparedExecution: planner.Execution, Architecture: "Initial architecture.", Stories: []organization.PlannedStory{story}, Tasks: []organization.PlannedTask{
		{ID: featureUUID(11), StoryID: story.ID, Title: "Initial task", Description: "Implement.", AcceptanceCriteria: []string{"passes"}, Owner: owner.ActorFQN, ModelProfile: owner.ModelProfile, DecisionRoute: kernel.RouteBoundedExecution, Purpose: kernel.PurposeImplementation, Complexity: 3, Risk: organization.RiskLow, CriticalPath: true, AttemptLimit: 2, ReviewRoundLimit: 1},
		{ID: featureUUID(13), StoryID: story.ID, Title: "Validate", Description: "Validate.", AcceptanceCriteria: []string{"verified"}, DependsOn: []kernel.UUIDv7{featureUUID(11)}, Validates: []kernel.UUIDv7{featureUUID(11)}, Owner: validator.ActorFQN, ModelProfile: validator.ModelProfile, DecisionRoute: kernel.RouteBoundedExecution, Purpose: kernel.PurposeValidation, Complexity: 2, Risk: organization.RiskLow, CriticalPath: true, AttemptLimit: 2, ReviewRoundLimit: 1},
	}, CreatedAt: now}
	feature := organization.FeatureRequest{SchemaVersion: organization.FeatureSchemaVersion, ID: featureUUID(1), Revision: 4, SubmittedBy: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "paul"}, Input: featureInput(), Status: organization.FeaturePlanned, OperatorActor: "teams::operator-1", ProductOwnerActor: "teams::product-owner-1", InitialMessageID: featureUUID(2), LastMessageID: featureUUID(2), LastStepID: featureUUID(3), LastHop: 3, BudgetAccountID: featureUUID(4), LifecycleEpoch: 1, ScopeRevision: 1, CreatedAt: now, UpdatedAt: now, Specification: &organization.FeatureSpecification{PreparedBy: "teams::project-manager-1", PreparedExecution: kernel.ExecutionTuple{ExecutionID: featureUUID(95), FencingEpoch: 1}, Stories: []organization.PlannedStory{story}, PreparedAt: now}, Plan: &firstPlan}
	store := &featureStoreFake{feature: feature}
	host := &featureHostFake{roles: map[kernel.ActorFQN]organization.RoleInstanceState{planner.ActorFQN: planner, owner.ActorFQN: owner, validator.ActorFQN: validator}}
	materializer := &recordingMaterializer{}
	clock := fixedClock(now.Add(time.Minute))
	coordinator, err := organization.NewFeatureCoordinator(store, host, materializer, clock, &idQueue{})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(firstPlan)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	supersession := organization.FeaturePlanSupersession{PlanVersion: 1, PlanDigest: kernel.Digest(hex.EncodeToString(digest[:])), ArchitectureRound: 2, RequestedBy: feature.SubmittedBy, Reason: "independent review found material plan defects", EvidenceRefs: []kernel.EvidenceRef{{EvidenceID: featureUUID(30), SHA256: featureDigest('a')}}, DeadlineAt: now.Add(4 * time.Hour), RequestedAt: now.Add(time.Minute), IdempotencyKey: "replan-1"}
	if err := supersession.Validate(feature); err != nil {
		t.Fatalf("valid supersession rejected: %v", err)
	}
	candidate := feature
	candidate.Revision++
	candidate.Status = organization.FeatureSpecified
	candidate.ScopeRevision++
	candidate.PlanSupersession = &supersession
	candidate.UpdatedAt = now.Add(time.Minute)
	if err := candidate.Input.Validate(); err != nil {
		t.Fatalf("candidate input rejected: %v", err)
	}
	if err := candidate.Plan.Validate(candidate); err != nil {
		t.Fatalf("candidate plan rejected: %v", err)
	}
	if err := candidate.Specification.Validate(candidate); err != nil {
		t.Fatalf("candidate specification rejected: %v", err)
	}
	if err := candidate.PlanSupersession.Validate(candidate); err != nil {
		t.Fatalf("candidate supersession rejected: %v", err)
	}
	if err := candidate.Validate(); err != nil {
		t.Fatalf("valid replanning projection rejected: %v", err)
	}
	reopened, err := coordinator.RequestReplan(context.Background(), feature.ID, feature.Revision, supersession)
	if err != nil || reopened.Status != organization.FeatureSpecified || reopened.Revision != 5 || reopened.ScopeRevision != 2 || reopened.Plan == nil || reopened.Plan.Version != 1 || reopened.PlanSupersession == nil {
		t.Fatalf("reopened feature=%#v err=%v", reopened, err)
	}
	invalid := firstPlan
	invalid.Architecture = "Replacement architecture."
	invalid.CreatedAt = now.Add(2 * time.Minute)
	if _, err := coordinator.ApplyPlan(context.Background(), feature.ID, reopened.Revision, invalid); err == nil {
		t.Fatal("replacement plan reused superseded version")
	}
	replacement := invalid
	replacement.Version = 2
	replacement.Stories = append([]organization.PlannedStory(nil), invalid.Stories...)
	replacement.Stories[0].ID = featureUUID(14)
	replacement.Tasks = append([]organization.PlannedTask(nil), invalid.Tasks...)
	replacement.Tasks[0].ID = featureUUID(12)
	replacement.Tasks[0].StoryID = replacement.Stories[0].ID
	replacement.Tasks[1].StoryID = replacement.Stories[0].ID
	replacement.Tasks[1].DependsOn = []kernel.UUIDv7{featureUUID(12)}
	replacement.Tasks[1].Validates = []kernel.UUIDv7{featureUUID(12)}
	reusedStory := replacement
	reusedStory.Stories = append([]organization.PlannedStory(nil), replacement.Stories...)
	reusedStory.Stories[0].ID = story.ID
	reusedStory.Tasks = append([]organization.PlannedTask(nil), replacement.Tasks...)
	reusedStory.Tasks[0].StoryID = story.ID
	reusedStory.Tasks[1].StoryID = story.ID
	if _, err := coordinator.ApplyPlan(context.Background(), feature.ID, reopened.Revision, reusedStory); err == nil {
		t.Fatal("replacement plan reused the superseded story aggregate")
	}
	planned, err := coordinator.ApplyPlan(context.Background(), feature.ID, reopened.Revision, replacement)
	if err != nil || materializer.calls != 1 || planned.Status != organization.FeaturePlanned || planned.Plan == nil || planned.Plan.Version != 2 || planned.PlanSupersession != nil {
		t.Fatalf("replacement feature=%#v calls=%d err=%v", planned, materializer.calls, err)
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
	store.feature.PlanSupersession = nil
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
	switch role {
	case "operator":
		return []kernel.ActorFQN{"teams::operator-1"}, nil
	case "product-owner":
		return []kernel.ActorFQN{"teams::product-owner-1"}, nil
	case "project-manager":
		return []kernel.ActorFQN{"teams::project-manager-1"}, nil
	case "architect":
		return []kernel.ActorFQN{"teams::architect-1", "teams::architect-2"}, nil
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
