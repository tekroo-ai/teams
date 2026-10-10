//go:build mongo_integration

package operationalruntime

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/nativeagent"
	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// A canned model qualifies the harness, not model quality. The accepted-plan
// boundary is fixture input; its source invocations, terminal evidence and
// retained journals are produced by normal kernel admission and native workers.
// No terminal invocation, approval or publication projection is inserted.
func TestAmendmentForwardCoordinatorNativeAssessmentAndPublicationAcrossRestart(t *testing.T) {
	amendmentForwardRoundTrip(t, false, kernel.RouteComplexReasoning)
}

func TestAmendmentInScopeConsultationContinuesWithoutPMAssessment(t *testing.T) {
	amendmentForwardRoundTrip(t, true, kernel.RouteComplexReasoning)
}

func TestAmendmentForwardCoordinatorWithBoundedArchitect(t *testing.T) {
	amendmentForwardRoundTrip(t, false, kernel.RouteBoundedExecution)
}

func amendmentForwardRoundTrip(t *testing.T, inScope bool, architectRoute kernel.DecisionRoute) {
	process, uri := startRuntimeMongod(t)
	defer stopRuntimeMongod(process)
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Minute)
	defer cancel()
	model := &amendmentForwardModel{t: t, calls: make(map[string]int), inScope: inScope}
	server := httptest.NewServer(http.HandlerFunc(model.serve))
	defer server.Close()
	path, config := amendmentForwardConfig(t, uri, server.URL+"/v1", architectRoute)
	loaded, err := LoadProductionConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewProductionService(ctx, loaded)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(contextWithTimeout(t)) })
	client, err := driver.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(ctx)
	db := client.Database(config.Mongo.Database)
	initializeContinuity := func() {
		t.Helper()
		session, err := service.ids.Next()
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := service.Store.StartRuntimeSession(ctx, service.deploymentIdentity, session, service.clock.Now().UTC(), time.Hour); err != nil {
			t.Fatal("isolated runtime continuity", err)
		}
	}
	initializeContinuity()
	reopen := func(boundary string) {
		t.Helper()
		if err := service.Close(ctx); err != nil {
			t.Fatal(boundary, err)
		}
		service, err = NewProductionService(ctx, loaded)
		if err != nil {
			t.Fatal(boundary, err)
		}
		service.Controller = &Controller{runtime: service.Runtime, clock: service.clock, state: ControlRunning}
		initializeContinuity()
		t.Log("reopened", boundary)
	}
	service.Controller = &Controller{runtime: service.Runtime, clock: service.clock, state: ControlRunning}
	now := time.Now().UTC()
	feature := organization.FeatureRequest{SchemaVersion: organization.FeatureSchemaVersion, ID: id(51001), Revision: 6,
		SubmittedBy: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"},
		Input:       organization.FeatureRequestInput{IdempotencyKey: "forward-fixture", Team: "fixture", Title: "Accepted compatibility", Description: "Keep the accepted behavior", AcceptanceCriteria: []string{"works"}, Priority: organization.PriorityNormal, Repository: "fixture", WorkspaceID: "workspace-1", MaximumStories: 2, MaximumTasks: 8, MaximumHops: 20},
		Status:      organization.FeaturePlanned, OperatorActor: "fixture::operator-1", ProductOwnerActor: "fixture::product-owner-1", InitialMessageID: id(51002), LastMessageID: id(51003), LastStepID: id(51004), LastHop: 4,
		BudgetAccountID: id(51005), LifecycleEpoch: 1, ScopeRevision: 1, CreatedAt: now, UpdatedAt: now}
	storyID := deterministicOperationalUUID("feature-planning-story", string(feature.ID))
	story := organization.PlannedStory{ID: storyID, Title: "Accepted compatibility", Description: "Keep the accepted behavior", AcceptanceCriteria: []string{"works"}, Priority: organization.PriorityNormal}
	if _, err := service.ensureFeaturePlanningStory(ctx, feature); err != nil {
		t.Fatal(err)
	}
	evidenceID, refs, err := service.ensureFeaturePlanningEvidence(ctx, feature)
	if err != nil {
		t.Fatal(err)
	}
	budget, err := service.ensureFeatureWorkBudget(ctx, feature, evidenceID, refs, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	owner := func(role string) organization.RoleInstanceState {
		t.Helper()
		r, err := service.ensureExactPlanningRole(ctx, role)
		if err != nil {
			t.Fatal(role, err)
		}
		return r
	}
	architect, pm, coder := owner("architect"), owner("project-manager"), owner("coder")
	makeTask := func(taskID kernel.UUIDv7, actor organization.RoleInstanceState, purpose kernel.WorkPurpose, title, description string) organization.PlannedTask {
		return organization.PlannedTask{ID: taskID, StoryID: storyID, Title: title, Description: description, AcceptanceCriteria: []string{"verified"}, Owner: actor.ActorFQN, ModelProfile: actor.ModelProfile, Purpose: purpose, DecisionRoute: service.profilesByModel[actor.ModelProfile].DecisionRoute, Complexity: 4, Risk: organization.RiskModerate, AttemptLimit: 2, ReviewRoundLimit: 1}
	}
	sourceDescription := func(stage, output string) string {
		return fmt.Sprintf("Perform workflow stage %q using the immutable role bundle. Input schema: request/v1. Output schema: %s. Completion contract: retain-source", stage, output)
	}
	retained := makeTask(featurePlanningTaskID(feature.ID, stageArchitecture, 0, nil), architect, kernel.PurposeHandoff, "design: amendment-source", sourceDescription("design", "design/v1"))
	blocked := makeTask(id(51006), coder, kernel.PurposeImplementation, "Compatibility caller", "Implement the accepted caller")
	blocked.WriteScope = []string{"service/consumer.go"}
	blocked.DependsOn = []kernel.UUIDv7{retained.ID}
	validator := blocked
	validator.ID, validator.Owner, validator.Purpose, validator.WriteScope = id(51007), "fixture::coder-2", kernel.PurposeValidation, nil
	validator.Title = "Validate compatibility caller"
	validator.DependsOn, validator.Validates = []kernel.UUIDv7{blocked.ID}, []kernel.UUIDv7{blocked.ID}
	validator.AttemptLimit = 2
	plan := organization.FeaturePlan{Version: 1, PreparedBy: pm.ActorFQN, PreparedExecution: pm.Execution, Architecture: "Preserve the accepted compatibility interface", Stories: []organization.PlannedStory{story}, Tasks: []organization.PlannedTask{retained, blocked, validator}, CreatedAt: now}
	// The architect authors implementation work; retained planning and the
	// independent validator are execution-plan context, not authored design nodes.
	architectureTasks := []architectureTaskResult{{Title: blocked.Title, Description: blocked.Description, AcceptanceCriteria: blocked.AcceptanceCriteria, Covers: []uint32{0}, Purpose: blocked.Purpose, Complexity: blocked.Complexity, Risk: blocked.Risk, AttemptLimit: blocked.AttemptLimit, ReviewRoundLimit: blocked.ReviewRoundLimit, WriteScope: blocked.WriteScope, DependsOn: []uint32{}, Validates: []uint32{}}}
	model.design = map[string]any{"schema_version": "1.0.0", "outcome": "completed", "summary": "Accepted design", "evidence": []string{}, "message_proposals": []any{}, "work_product": architectureStageResult{SchemaVersion: "1.0.0", ResultType: "FEATURE_PLAN", Architecture: stageText(plan.Architecture), DesignDecisions: stageDecisionList{}, Assumptions: []string{}, Tasks: architectureTasks}}
	admit := func(task organization.PlannedTask, who organization.RoleInstanceState) kernel.WorkInvocation {
		t.Helper()
		ref := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}
		state, head, exists, err := service.Store.ReadAggregateHead(ctx, ref)
		if err != nil {
			t.Fatal(err)
		}
		if !exists {
			created, err := service.submitPlannedCommand(ctx, feature, "tekroo.command.task.create", kernel.AggregateTask, task.ID, "forward-fixture-task", mustJSON(map[string]any{"story_id": task.StoryID, "title": task.Title, "description": task.Description, "acceptance_criteria": task.AcceptanceCriteria, "depends_on": append([]kernel.UUIDv7{}, task.DependsOn...)}), nil)
			if err != nil {
				t.Fatal(task.Title, err)
			}
			state, head, _, err = service.Store.ReadAggregateHead(ctx, ref)
			if err != nil || len(created.EventIDs) != 1 {
				t.Fatal(err)
			}
		}
		profile := service.profilesByModel[who.ModelProfile]
		if err := service.registerExecution(ctx, who, profile); err != nil {
			t.Fatal(err)
		}
		wp := service.workProfile(feature, task, state.LifecycleEpoch, state.ScopeRevision, evidenceID, budget.DeadlineAt)
		tracked := &trackedTask{plan: task, owner: who, profile: wp, revision: state.Revision, last: head}
		if err := service.bindConsultationWorkProfile(ctx, feature, tracked, refs); err != nil {
			t.Fatal(err)
		}
		snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: ref})
		if err != nil {
			t.Fatal(err)
		}
		currentBudget := snapshot.WorkBudgetAccounts[budget.Ref()]
		var parents []kernel.UUIDv7
		for _, dependency := range task.DependsOn {
			_, head, _, err := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: dependency})
			if err != nil {
				t.Fatal(err)
			}
			parents = append(parents, head)
		}
		if err := service.activateTask(ctx, feature, tracked, profile, service.workspacesByID[who.WorkspaceID], currentBudget.Revision, parents, refs, evidenceID, []kernel.Digest{refs[0].SHA256}); err != nil {
			t.Fatal(task.Title, err)
		}
		snapshot, err = service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: ref})
		if err != nil {
			t.Fatal(err)
		}
		inv, exists := latestTaskInvocation(snapshot.WorkInvocations, task.ID)
		if !exists {
			t.Fatal("missing admitted invocation", task.ID)
		}
		return inv
	}
	captureBrief := func(inv kernel.WorkInvocation) (application.ExecutionBrief, kernel.Digest) {
		t.Helper()
		current, err := service.Store.LoadOperationalExecution(ctx, inv.ID)
		if err != nil {
			t.Fatal(err)
		}
		grounding, err := service.roleGrounding.ResolveRoleGrounding(ctx, inv.ActorFQN)
		if err != nil {
			t.Fatal(err)
		}
		brief, digest, err := application.BuildExecutionBrief(current, grounding, config.Execution.MaximumBriefBytes)
		if err != nil {
			t.Fatal(err)
		}
		return brief, digest
	}
	execute := func(inv kernel.WorkInvocation) kernel.WorkInvocation {
		t.Helper()
		var document struct {
			Data []byte `bson:"data"`
		}
		if err := db.Collection("outbox").FindOne(ctx, bson.M{"event_id": string(inv.AuthorizationEventID), "kind": workInvocationAuthorized}).Decode(&document); err != nil {
			t.Fatal("authorized intent", err)
		}
		var intent kernel.OutboxIntent
		if err := json.Unmarshal(document.Data, &intent); err != nil {
			t.Fatal(err)
		}
		if err := service.Runtime.ProcessOne(ctx, intent); err != nil {
			_, boundaryErr := service.Runtime.coordinator.Process(ctx, intent)
			entries, journalErr := service.Store.Load(ctx, string(inv.ID))
			for _, entry := range entries {
				if len(entry.Payload) < 2048 {
					t.Log(entry.Kind, string(entry.Payload))
				} else {
					t.Log(entry.Kind, "payload bytes", len(entry.Payload))
				}
			}
			t.Fatalf("native execution: %v; coordinator_error=%v journal_error=%v model_calls=%v", err, boundaryErr, journalErr, model.calls)
		}
		binding, err := service.Store.LoadOperationalExecution(ctx, inv.ID)
		if err != nil || binding.Invocation.State != kernel.InvocationSucceeded {
			var raw []byte
			if binding.Invocation.OutputDigest != nil {
				raw, _ = service.Runtime.ReadExecutionOutput(ctx, *binding.Invocation.OutputDigest)
			}
			t.Fatalf("native invocation %s/%s: %v; output=%s", binding.Invocation.ID, binding.Invocation.State, err, raw)
		}
		return binding.Invocation
	}
	complete := func(task organization.PlannedTask, inv kernel.WorkInvocation) {
		t.Helper()
		ref := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}
		state, head, _, err := service.Store.ReadAggregateHead(ctx, ref)
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: ref})
		if err != nil {
			t.Fatal(err)
		}
		if err := service.completeEvidenceTask(ctx, feature, task, state, head, inv, snapshot); err != nil {
			t.Fatal("source completion", err)
		}
	}
	designInv := execute(admit(retained, architect))
	complete(retained, designInv)
	plan.SourceDesignDigest = *designInv.OutputDigest
	pmTask := makeTask(featurePlanningTaskID(feature.ID, stagePlanFinalization, 0, nil), pm, kernel.PurposeHandoff, "assign: amendment-source", sourceDescription("assign", "plan/v1"))
	pmTask.DependsOn = []kernel.UUIDv7{retained.ID}
	model.plan = map[string]any{"work_product": map[string]any{"result_type": "FEATURE_EXECUTION_PLAN", "plan": plan}}
	pmInv := execute(admit(pmTask, pm))
	complete(pmTask, pmInv)
	feature.Plan = &plan
	feature.Specification = &organization.FeatureSpecification{PreparedBy: pm.ActorFQN, PreparedExecution: pm.Execution, Stories: plan.Stories, PreparedAt: now}
	feature.Design = &organization.FeatureDesignCandidate{PreparedBy: architect.ActorFQN, PreparedExecution: architect.Execution, OutputDigest: plan.SourceDesignDigest, PreparedAt: now}
	if err := feature.Validate(); err != nil {
		t.Fatal("accepted fixture boundary", err)
	}
	// Only the accepted feature context is installed. All kernel state above
	// and below is obtained through commands and workers, never projections.
	if _, err := db.Collection("feature_requests").InsertOne(ctx, bson.M{"_id": string(feature.ID), "revision": feature.Revision, "status": feature.Status, "submitted_by_kind": feature.SubmittedBy.Kind, "submitted_by_id": feature.SubmittedBy.ID, "idempotency_key": feature.Input.IdempotencyKey, "created_at": feature.CreatedAt, "updated_at": feature.UpdatedAt, "data": []byte(mustJSON(feature))}); err != nil {
		t.Fatal(err)
	}
	blockedInv := execute(admit(blocked, coder))
	sourceWorkspace := service.workspacesByID[coder.WorkspaceID]
	// Dirty bytes are explicit checkpoint fixture input, not model-authored work.
	writeText(t, filepath.Join(sourceWorkspace.WorkingDirectory, "file.txt"), "staged checkpoint fixture\n", 0644)
	candidateGit(t, sourceWorkspace.WorkingDirectory, "add", "file.txt")
	writeText(t, filepath.Join(sourceWorkspace.WorkingDirectory, "file.txt"), "latest checkpoint fixture\n", 0644)
	sourceHead := candidateGit(t, sourceWorkspace.WorkingDirectory, "rev-parse", "HEAD")
	sourceIndex := candidateGit(t, sourceWorkspace.WorkingDirectory, "write-tree")
	sourceStatus := candidateGit(t, sourceWorkspace.WorkingDirectory, "status", "--porcelain=v1", "--untracked-files=all")
	ref := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: blocked.ID}
	state, _, _, err := service.Store.ReadAggregateHead(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: ref})
	if err != nil {
		t.Fatal(err)
	}
	blockedOutput, err := service.Runtime.ReadExecutionOutput(ctx, *blockedInv.OutputDigest)
	if err != nil {
		t.Fatal(err)
	}
	reason, outcome, reported := reportedTaskNonCompletion(blockedOutput)
	if !reported || outcome != "needs_decision" {
		t.Fatal("missing bounded question", string(blockedOutput))
	}
	if _, err := service.blockStructuredDecisionTask(ctx, feature, blocked, state, blockedInv, snapshot, reason, "task-reported-"+outcome); err != nil {
		t.Fatal(err)
	}
	// Materialize the pending validator without assigning or executing it.
	if _, err := service.submitPlannedCommand(ctx, feature, "tekroo.command.task.create", kernel.AggregateTask, validator.ID, "forward-fixture-task", mustJSON(map[string]any{"story_id": validator.StoryID, "title": validator.Title, "description": validator.Description, "acceptance_criteria": validator.AcceptanceCriteria, "depends_on": validator.DependsOn}), nil); err != nil {
		t.Fatal(err)
	}
	storyCreated, exists, err := service.Store.ReadAggregateEventAtRevision(ctx, kernel.AggregateRef{Kind: kernel.AggregateStory, ID: storyID}, 1)
	if err != nil || !exists {
		t.Fatal("story create receipt", err)
	}
	if err := service.activatePlannedStory(ctx, feature, storyID, storyCreated.EventID, []kernel.UUIDv7{retained.ID, blocked.ID, validator.ID}); err != nil {
		t.Fatal("accepted story activation", err)
	}
	retainedState, retainedHead, _, err := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: retained.ID})
	if err != nil {
		t.Fatal(err)
	}
	load := func() organization.FeatureRequest {
		t.Helper()
		value, found, err := service.Store.LoadFeature(ctx, feature.ID)
		if err != nil || !found {
			t.Fatal("feature", err)
		}
		return value
	}
	consultID := taskConsultationID(feature, blocked.ID)
	if _, _, err := service.featureArchitectureCandidate(ctx, feature); err != nil {
		t.Fatal("accepted source architecture", err)
	}
	var consultationBrief application.ExecutionBrief
	var consultationDigest kernel.Digest
	checkedPreflight := false
	for pass := 0; pass < 4; pass++ {
		states := map[kernel.UUIDv7]kernel.AggregateState{}
		for _, task := range plan.Tasks {
			s, _, _, err := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID})
			if err != nil {
				t.Fatal(err)
			}
			states[task.ID] = s
		}
		if pass == 2 && !inScope {
			// Exercise the real reservation path with a unavailable PM package.
			// A rejected preflight must not consume the one amendment allowance
			// or change completed work/budget; restore only this test's profile.
			original := service.profilesByModel[pm.ModelProfile]
			unavailable := original
			unavailable.Qualification = nil
			service.profilesByModel[pm.ModelProfile] = unavailable
			before := amendmentForwardBudget(t, ctx, service, budget.Ref())
			changed, preflightErr := service.reconcileTaskConsultations(ctx, load(), plan, states, map[kernel.UUIDv7]kernel.WorkInvocation{blocked.ID: blockedInv})
			service.profilesByModel[pm.ModelProfile] = original
			if changed || !errors.Is(preflightErr, organization.ErrInvalidFeature) || !strings.Contains(preflightErr.Error(), "qualification package") || load().PlanAmendment != nil {
				t.Fatal("failed preflight consumed reservation", changed, preflightErr)
			}
			if after := amendmentForwardBudget(t, ctx, service, budget.Ref()); !reflect.DeepEqual(before, after) {
				t.Fatal("failed preflight changed budget")
			}
			if _, _, exists, err := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: planAmendmentAssessmentID(feature.ID)}); err != nil || exists {
				t.Fatal("failed preflight created assessment work", err)
			}
			checkedPreflight = true
		}
		if _, err := service.reconcileTaskConsultations(ctx, load(), plan, states, map[kernel.UUIDv7]kernel.WorkInvocation{blocked.ID: blockedInv}); err != nil {
			t.Fatal("consultation", err)
		}
		if inScope {
			current, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: ref})
			if err != nil {
				t.Fatal(err)
			}
			if latest, found := latestTaskInvocation(current.WorkInvocations, blocked.ID); found && latest.ID != blockedInv.ID {
				break // The normal frontier now observes this new invocation.
			}
		}
		cs, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: consultID}})
		if err != nil {
			t.Fatal(err)
		}
		if inv, found := latestTaskInvocation(cs.WorkInvocations, consultID); found && !inv.State.Terminal() {
			if got := cs.WorkProfiles[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: consultID}].Profile.MinimumDecisionRoute; got != architectRoute {
				t.Fatalf("consultation route=%s, source route=%s", got, architectRoute)
			}
			consultationBrief, consultationDigest = captureBrief(inv)
			execute(inv)
		}
		if load().PlanAmendment != nil {
			break
		}
	}
	reserved := load()
	if inScope {
		if reserved.PlanAmendment != nil {
			t.Fatal("in-scope answer consumed amendment allowance")
		}
		continued, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: ref})
		if err != nil {
			t.Fatal(err)
		}
		latest, found := latestTaskInvocation(continued.WorkInvocations, blocked.ID)
		if !found || latest.ID == blockedInv.ID || latest.State != kernel.InvocationAuthorized || latest.WorkspaceID != blockedInv.WorkspaceID {
			t.Fatal("in-scope answer did not admit existing-workspace continuation", latest)
		}
		model.mu.Lock()
		defer model.mu.Unlock()
		if model.calls["project-manager"] != 1 || model.calls["architect"] != 2 || model.calls["coder"] != 1 {
			t.Fatal("in-scope answer performed an extra model assessment", model.calls)
		}
		t.Logf("WITHIN_SCOPE source=%s continuation=%s; no PM assessment or scope expansion", blockedInv.ID, latest.ID)
		return
	}
	if reserved.PlanAmendment == nil || reserved.PlanAmendment.State != organization.AmendmentAssessing {
		t.Fatal("no automatic reservation", reserved.PlanAmendment)
	}
	if !checkedPreflight {
		t.Fatal("reservation preflight counterexample was not exercised")
	}
	model.feature = reserved
	reopen("reservation")
	if handled, err := service.reconcilePlanAmendment(ctx, load()); !handled || err != nil {
		t.Fatal("assessment admission", handled, err)
	}
	assessmentID := reserved.PlanAmendment.AssessmentID
	snapshot, err = service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: assessmentID}})
	if err != nil {
		t.Fatal(err)
	}
	assessment, found := latestTaskInvocation(snapshot.WorkInvocations, assessmentID)
	if !found || assessment.State != kernel.InvocationAuthorized || assessment.AttemptOrdinal != 1 {
		t.Fatal("assessment not admitted once", assessment)
	}
	if got := snapshot.WorkProfiles[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: assessmentID}].Profile.MinimumDecisionRoute; got != kernel.RouteBoundedExecution {
		t.Fatalf("PM assessment route=%s, want bounded source route", got)
	}
	reopen("assessment admission")
	assessmentBrief, assessmentDigest := captureBrief(assessment)
	execute(assessment)
	reopen("terminal assessment")
	if handled, err := service.reconcilePlanAmendment(ctx, load()); !handled || err != nil {
		t.Fatal("policy decision", handled, err)
	}
	approved := load()
	if approved.PlanAmendment.State != organization.AmendmentApproved {
		t.Fatal("independent policy rejected", approved.PlanAmendment.Decision)
	}
	if approved.PlanAmendment.DecidedBy == nil || approved.PlanAmendment.DecidedBy.Kind != kernel.PrincipalPolicy {
		t.Fatal("PM approved itself")
	}
	if err := service.completeAmendmentAssessment(ctx, approved); err != nil {
		t.Fatal(err)
	}
	if err := service.materializeAmendmentSuccessors(ctx, approved); err != nil {
		t.Fatal("preparation", err)
	}
	beforeBudget := amendmentForwardBudget(t, ctx, service, budget.Ref())
	for _, replacement := range approved.PlanAmendment.Proposal.Replacements {
		s, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: replacement.SuccessorTaskID}})
		if err != nil || s.State == nil || s.State.Phase != kernel.PhasePlanned {
			t.Fatal("premature successor admission", err)
		}
		if _, exists := latestTaskInvocation(s.WorkInvocations, replacement.SuccessorTaskID); exists {
			t.Fatal("successor executed before publication")
		}
	}
	reopen("preparation")
	if handled, err := service.reconcilePlanAmendment(ctx, load()); !handled || err != nil {
		t.Fatal("publication", handled, err)
	}
	applied := load()
	if applied.PlanAmendment.State != organization.AmendmentApplied || applied.Plan.Version != plan.Version+1 {
		t.Fatal("plan not published", applied.PlanAmendment.State)
	}
	reopen("publication")
	// Inspection of a completed journal must remain bound to the admitted
	// prior plan even after publication removes its blocked task from the DAG.
	inspection := &nativeagent.Boundary{Control: service.Store, Journal: service.Store, Owner: "historical-inspection", LeaseTTL: time.Minute, Heartbeat: time.Second,
		Configure: func(ctx context.Context, brief application.ExecutionBrief) (nativeagent.Config, error) {
			return configuredNativeSession(ctx, service, service.profilesByModel, nil, nil, service.requestTimeout, brief)
		}}
	for _, saved := range []struct {
		name   string
		brief  application.ExecutionBrief
		digest kernel.Digest
	}{{"consultation", consultationBrief, consultationDigest}, {"assessment", assessmentBrief, assessmentDigest}} {
		observed, err := inspection.Inspect(ctx, saved.brief, string(saved.brief.InvocationID), saved.digest)
		if err != nil || observed.State != application.ExternalSucceeded {
			t.Fatalf("historical %s inspection after publication: %v, %+v", saved.name, err, observed)
		}
		changed := saved.brief
		changed.InvocationID = id(51020)
		if _, err := configuredNativeSession(ctx, service, service.profilesByModel, nil, nil, service.requestTimeout, changed); !errors.Is(err, nativeagent.ErrInvalidBinding) {
			t.Fatalf("historical %s accepted a different invocation: %v", saved.name, err)
		}
	}
	secondQuestion := blockedInv
	secondQuestion.ID = id(51021)
	if reservedAgain, err := service.reservePlanAmendment(ctx, applied, secondQuestion, blockedInv); reservedAgain || err != nil || !reflect.DeepEqual(load().PlanAmendment, applied.PlanAmendment) {
		t.Fatal("used amendment allowance changed for another question", reservedAgain, err)
	}
	for pass := 0; pass < 2; pass++ {
		if handled, err := service.reconcilePlanAmendment(ctx, load()); handled || err != nil {
			t.Fatal("applied replay", handled, err)
		}
	}
	afterBudget := amendmentForwardBudget(t, ctx, service, budget.Ref())
	if !reflect.DeepEqual(beforeBudget, afterBudget) || !afterBudget.DeadlineAt.Equal(budget.DeadlineAt) || afterBudget.ModelInvocationsUsed != 5 {
		t.Fatal("amendment reset or duplicated the finite budget", beforeBudget, afterBudget)
	}
	afterState, afterHead, _, err := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: retained.ID})
	if err != nil || !reflect.DeepEqual(retainedState, afterState) || retainedHead != afterHead {
		t.Fatal("completed work changed", err)
	}
	if err := service.reconcileFeaturePlan(ctx, applied, *applied.Plan); err != nil {
		t.Fatal("normal successor frontier admission", err)
	}
	freshID := applied.PlanAmendment.Proposal.Replacements[0].SuccessorTaskID
	frontier, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: freshID}})
	if err != nil {
		t.Fatal(err)
	}
	freshInvocation, found := latestTaskInvocation(frontier.WorkInvocations, freshID)
	if !found || freshInvocation.State != kernel.InvocationAuthorized || freshInvocation.AttemptOrdinal != 1 || freshInvocation.ActorFQN != coder.ActorFQN {
		t.Fatal("successor not normally admitted exactly once", freshInvocation)
	}
	binding, err := service.Store.LoadOperationalExecution(ctx, freshInvocation.ID)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := service.workspaceResolver.ResolveWorkspace(ctx, binding.Scope)
	if err != nil {
		t.Fatal("successor workspace", err)
	}
	if workspace.WorkingDirectory == sourceWorkspace.WorkingDirectory {
		t.Fatal("successor reused blocked directory")
	}
	bytes, err := os.ReadFile(filepath.Join(workspace.WorkingDirectory, "file.txt"))
	if err != nil || string(bytes) != "latest checkpoint fixture\n" {
		t.Fatal("checkpoint bytes not inherited", err)
	}
	rawReceipt, err := os.ReadFile(filepath.Join(service.taskWorkspaces.receiptRoot(), binding.Scope.WorktreeID+".json"))
	var receipt taskWorkspaceReceipt
	if err != nil || json.Unmarshal(rawReceipt, &receipt) != nil || len(receipt.Components) != 1 {
		t.Fatal("successor checkpoint receipt", err)
	}
	component := receipt.Components[0]
	if component.TaskID != blockedInv.TaskID || component.InvocationID != blockedInv.ID || component.OutputSHA256 != *blockedInv.OutputDigest || component.Readiness != "BLOCKED_CHECKPOINT" || component.ReadinessPolicy != applied.PlanAmendment.PolicyDigest {
		t.Fatal("successor checkpoint provenance", component)
	}
	if candidateGit(t, sourceWorkspace.WorkingDirectory, "rev-parse", "HEAD") != sourceHead || candidateGit(t, sourceWorkspace.WorkingDirectory, "write-tree") != sourceIndex || candidateGit(t, sourceWorkspace.WorkingDirectory, "status", "--porcelain=v1", "--untracked-files=all") != sourceStatus {
		t.Fatal("original Git changed during publication/admission")
	}
	frontierBudget := frontier.WorkBudgetAccounts[budget.Ref()]
	if frontierBudget.ModelInvocationsUsed != afterBudget.ModelInvocationsUsed+1 || !frontierBudget.DeadlineAt.Equal(budget.DeadlineAt) {
		t.Fatal("successor budget debit/deadline changed", frontierBudget)
	}
	reopen("successor admission")
	if err := service.reconcileFeaturePlan(ctx, load(), *applied.Plan); err != nil {
		t.Fatal("frontier replay", err)
	}
	again := amendmentForwardBudget(t, ctx, service, budget.Ref())
	if !reflect.DeepEqual(frontierBudget, again) {
		t.Fatal("frontier replay debited budget again")
	}
	replayed, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: freshID}})
	if err != nil {
		t.Fatal(err)
	}
	if latest, found := latestTaskInvocation(replayed.WorkInvocations, freshID); !found || latest.ID != freshInvocation.ID || latest.AttemptOrdinal != 1 {
		t.Fatal("frontier restart duplicated invocation", latest)
	}
	// Local input mutation only: the durable feature, receipt and source are
	// untouched. A checkpoint label alone must never bypass candidate checks.
	wrongBinding := applied
	wrongAmendment := *applied.PlanAmendment
	wrongAmendment.PolicyDigest = repeatedDigest('6')
	wrongBinding.PlanAmendment = &wrongAmendment
	if changed, err := service.reconcileCandidateLineage(ctx, wrongBinding, *applied.Plan, nil, nil, replayed); changed || !errors.Is(err, errInvalidTaskWorkspace) {
		t.Fatal("changed checkpoint policy binding accepted", changed, err)
	}
	if current := amendmentForwardBudget(t, ctx, service, budget.Ref()); !reflect.DeepEqual(frontierBudget, current) {
		t.Fatal("rejected checkpoint inspection changed budget")
	}
	validatorID := applied.PlanAmendment.Proposal.Replacements[1].SuccessorTaskID
	vstate, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: validatorID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := latestTaskInvocation(vstate.WorkInvocations, validatorID); exists {
		t.Fatal("validator admitted before successor candidate")
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	if model.calls["architect"] != 2 || model.calls["project-manager"] != 2 || model.calls["coder"] != 1 {
		t.Fatal("extra model assessment or lost source", model.calls)
	}
	t.Logf("APPLIED assessment=%s invocation=%s source_architect=%s source_pm=%s successor=%s budget_used=%d; no seeded terminal/approved/applied state", assessmentID, assessment.ID, designInv.ID, pmInv.ID, freshInvocation.ID, frontierBudget.ModelInvocationsUsed)
}

func amendmentForwardBudget(t *testing.T, ctx context.Context, service *ProductionService, ref kernel.AggregateRef) kernel.WorkBudgetAccount {
	t.Helper()
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: ref})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.WorkBudgetAccounts[ref]
}

type amendmentForwardModel struct {
	t            *testing.T
	mu           sync.Mutex
	calls        map[string]int
	design, plan any
	feature      organization.FeatureRequest
	inScope      bool
}

func (model *amendmentForwardModel) serve(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Model    string          `json:"model"`
		Messages json.RawMessage `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		model.t.Error(err)
		http.Error(w, "invalid request", 400)
		return
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	model.calls[request.Model]++
	ordinal := model.calls[request.Model]
	var result any
	switch request.Model {
	case "architect":
		if ordinal == 1 {
			result = model.design
		} else {
			if !strings.Contains(string(request.Messages), "Preserve the accepted compatibility interface") {
				model.t.Error("architect source reasoning missing")
			}
			result = map[string]any{"disposition": "NEEDS_SCOPE_CHANGE", "answer": "Add cmd/main.go to the caller assignment; preserve the accepted interface."}
			if model.inScope {
				result = map[string]any{"disposition": "ANSWERED_WITHIN_SCOPE", "answer": "Use the accepted caller interface within service/consumer.go; no wiring change is needed."}
			}
		}
	case "project-manager":
		if ordinal == 1 {
			result = model.plan
		} else {
			if !strings.Contains(string(request.Messages), "source_outputs") || !strings.Contains(string(request.Messages), "NEEDS_SCOPE_CHANGE") {
				model.t.Error("raw amendment input/source context absent")
			}
			f, a := model.feature, model.feature.PlanAmendment
			successor := a.PriorPlan
			successor.Version++
			var messages []struct{ Role, Content string }
			if err := json.Unmarshal(request.Messages, &messages); err != nil {
				model.t.Error(err)
			}
			for _, message := range messages {
				if message.Role != "user" {
					continue
				}
				var prompt struct {
					ExecutionBrief application.ExecutionBrief `json:"execution_brief"`
				}
				if json.Unmarshal([]byte(message.Content), &prompt) == nil && prompt.ExecutionBrief.InvocationID.Valid() {
					successor.PreparedExecution = prompt.ExecutionBrief.Execution
				}
			}
			successor.CreatedAt = time.Now().UTC()
			successor.Tasks = append([]organization.PlannedTask(nil), a.PriorPlan.Tasks...)
			newWork, newTest := successor.Tasks[1], successor.Tasks[2]
			newWork.ID, newTest.ID = id(51008), id(51009)
			newWork.AttemptLimit = 1
			newWork.WriteScope = []string{"service/consumer.go", "cmd/main.go"}
			newTest.DependsOn, newTest.Validates = []kernel.UUIDv7{newWork.ID}, []kernel.UUIDv7{newWork.ID}
			successor.Tasks[1], successor.Tasks[2] = newWork, newTest
			result = organization.PlanAmendmentProposal{SchemaVersion: organization.PlanAmendmentContractVersion, FeatureID: f.ID, FeatureRevision: a.FeatureRevision, PlanVersion: a.PriorPlan.Version, PlanDigest: a.PriorDigest, SpecificationDigest: a.SpecificationDigest, BlockedTaskID: a.BlockedTaskID, BlockedTaskRevision: a.BlockedTaskRevision, BlockedInvocationID: a.BlockedInvocationID, ConsultationID: a.ConsultationID, AnswerDigest: a.AnswerDigest, Reason: "Repair existing caller wiring ownership only", SuccessorPlan: successor,
				Replacements: []organization.PlanTaskReplacement{{PriorTaskID: a.PriorPlan.Tasks[1].ID, SuccessorTaskID: newWork.ID}, {PriorTaskID: a.PriorPlan.Tasks[2].ID, SuccessorTaskID: newTest.ID}},
				Impacts:      []organization.PlanAmendmentImpact{{Kind: "CALLER", Path: "service/consumer.go", OwnerTaskID: newWork.ID, Verification: "Compile and exercise accepted caller behavior"}, {Kind: "WIRING", Path: "cmd/main.go", OwnerTaskID: newWork.ID, Verification: "Compile production construction"}}}
		}
	case "coder":
		result = map[string]any{"outcome": "needs_decision", "summary": "Compatibility caller needs wiring ownership", "work_product": map[string]any{}, "message_proposals": []any{}}
	default:
		model.t.Error("unexpected role", request.Model)
		http.Error(w, "unexpected role", 500)
		return
	}
	content := string(mustJSON(map[string]any{"submit_result": result}))
	if request.Model == "coder" {
		content = application.OrganizationalResultMarker + "\n" + string(mustJSON(result))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"id": fmt.Sprintf("fixture-%s-%d", request.Model, ordinal), "choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": content}}}})
}

func amendmentForwardConfig(t *testing.T, uri, url string, architectRoute kernel.DecisionRoute) (string, ProductionConfig) {
	t.Helper()
	path, config := writeNativeProductionFixture(t)
	dir := filepath.Dir(path)
	writeText(t, filepath.Join(dir, config.Mongo.URIFile), uri+"\n", 0600)
	policy := integratedPolicy()
	// Existing in-scope continuation uses the operator recovery-budget command
	// and the policy unblock command. Supply those explicit fixture grants;
	// neither grant is inferred from a model answer or changed in production.
	policy.Grants[0].Scope.CommandTypes = append(policy.Grants[0].Scope.CommandTypes, "tekroo.command.work-budget.amend")
	policy.Grants[0].Scope.TargetKinds = append(policy.Grants[0].Scope.TargetKinds, kernel.AggregateWorkBudget)
	policy.Grants[1].Scope.CommandTypes = append(policy.Grants[1].Scope.CommandTypes, "tekroo.command.work.unblock")
	policy.Grants[2].Scope.CommandTypes = append(policy.Grants[2].Scope.CommandTypes, "tekroo.command.completion-review.record-result")
	policy.Grants[2].Scope.TargetKinds = append(policy.Grants[2].Scope.TargetKinds, kernel.AggregateCompletionReview)
	for index, actor := range []string{"fixture::architect-1", "fixture::project-manager-1", "fixture::coder-1", "fixture::coder-2"} {
		grant := policy.Grants[3]
		grant.Grantee.ID, grant.GrantDigest = actor, digestByte(byte('2'+index))
		policy.Grants = append(policy.Grants, grant)
	}
	writeJSON(t, filepath.Join(dir, config.AuthorizationPolicyFile), policy, 0600)
	config.Operator.Principal = kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	writeText(t, filepath.Join(dir, "forward.pub"), base64.StdEncoding.EncodeToString(public)+"\n", 0600)
	config.Organization.Publishers = []ProductionPublisher{{KeyID: "forward-fixture", PublicKeyFile: "forward.pub"}}
	manifest := organization.TeamManifest{SchemaVersion: organization.TeamManifestSchemaVersion, Team: "fixture", Version: "1.0.0"}
	config.Profiles, config.Workspaces = nil, nil
	for _, role := range []string{"architect", "coder", "project-manager"} {
		permissions := []string{"repository.read"}
		if role == "coder" {
			permissions = []string{"repository.edit", "test.execute"}
		}
		bundle := organization.RoleBundle{SchemaVersion: organization.RoleBundleSchemaVersion, Role: role, Version: "1.0.0", Capabilities: []string{"implement"}, Subscriptions: []organization.Subscription{{Type: "tekroo.message.task.assigned", Purpose: "implementation"}}, Permissions: permissions, Instructions: "Preserve the accepted compatibility interface. Use only admitted evidence and report bounded results.", Handlers: map[string]string{"tekroo.message.task.assigned": "implement"}, PublisherKeyID: "forward-fixture"}
		sha, err := bundle.ContentDigest()
		if err != nil {
			t.Fatal(err)
		}
		bytes, err := hex.DecodeString(string(sha))
		if err != nil {
			t.Fatal(err)
		}
		bundle.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(private, bytes))
		if err := bundle.Validate(); err != nil {
			t.Fatal("signed fixture bundle", role, err)
		}
		bundlePath := "roles/" + role + ".json"
		writeJSON(t, filepath.Join(dir, bundlePath), bundle, 0600)
		settings := nativeagent.ProfileSettings{SchemaVersion: nativeagent.ProfileSettingsVersion, BaseURL: url, Model: role, MaxOutputTokens: 16384}
		if role != "coder" {
			settings.ResponseMode = nativeagent.ResponseModeJSONSchemaActions
		}
		settings.MaxTurns = 3
		model, err := nativeagent.ModelProfileDigest(kernel.RoleFQRN(role), sha, settings)
		if err != nil {
			t.Fatal(err)
		}
		surface, err := agenttools.NativeToolSurfaceDigest(permissions)
		if err != nil {
			t.Fatal(err)
		}
		route, kinds := kernel.RouteBoundedExecution, []kernel.WorkKind{kernel.WorkDesign}
		if role == "architect" {
			route = architectRoute
		}
		if role == "coder" {
			// This fixture's second coder is an independent validator. Neither
			// actor is qualified for unrelated design, debugging or release work.
			kinds = []kernel.WorkKind{kernel.WorkImplementation, kernel.WorkValidation}
		}
		profile := ProductionProfile{RoleFQRN: kernel.RoleFQRN(role), RoleBundleDigest: sha, ModelProfileDigest: model, RuntimeIdentityDigest: repeatedDigest('3'), ToolPolicyDigest: surface, EffectPolicyDigest: repeatedDigest('5'), NativeSettings: &settings, DecisionRoute: route}
		profile.QualificationCorpus, profile.Qualification = testQualificationBundle(t, model, kernel.RoleFQRN(role), profile.DecisionRoute, surface, kinds, time.Now().UTC().Add(-time.Minute))
		config.Profiles = append(config.Profiles, profile)
		workspaceIDs := []string{role + "-1"}
		instances := uint32(1)
		if role == "coder" {
			workspaceIDs, instances = []string{"workspace-1", "workspace-2"}, 2
		}
		for _, workspaceID := range workspaceIDs {
			repo, baseline, _ := candidateRepository(t)
			// The legacy candidate helper installs an OH hook; this fixture is native.
			if err := os.Remove(filepath.Join(repo, ".openhands/hooks/sma_context_hook.py")); err != nil {
				t.Fatal(err)
			}
			config.Workspaces = append(config.Workspaces, ProductionWorkspace{WorkspaceID: workspaceID, WorktreeID: workspaceID, WorkingDirectory: repo, Branch: "source", BaselineSHA: baseline, WritablePaths: []string{"."}})
		}
		manifest.Roles = append(manifest.Roles, organization.RoleBinding{Role: role, BundlePath: bundlePath, BundleDigest: sha, PublisherKeyID: bundle.PublisherKeyID, InitialInstances: instances, MaximumInstances: instances, LaunchMode: organization.LaunchEager, ModelProfileDigest: model, WorkspaceIDs: workspaceIDs})
	}
	writeJSON(t, filepath.Join(dir, "team.json"), manifest, 0600)
	if err := manifest.Validate(); err != nil {
		t.Fatal("fixture manifest", err, manifest)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "team.json"))
	if err != nil {
		t.Fatal(err)
	}
	config.Organization.ManifestDigest = digestBytes(raw)
	definition := kernel.WorkflowDefinition{SchemaVersion: kernel.WorkflowDefinitionSchemaVersion, Name: "amendment-source", Version: "1.0.0", TriggerTypes: []string{"fixture.source.requested"}, RootBudgets: kernel.WorkflowBudgetLimits{MaximumModelInvocations: 2, MaximumHops: 4, MaximumAttempts: 2}, ProjectionRules: []string{}}
	for _, value := range []struct{ stage, role, output string }{{"design", "architect", "design/v1"}, {"assign", "project-manager", "plan/v1"}} {
		definition.Stages = append(definition.Stages, kernel.WorkflowStageDefinition{StageID: value.stage, DependsOn: []string{}, InputSchema: "request/v1", OutputSchema: value.output, CompletionCondition: "retain-source", RequiredCapabilities: []string{"implement"}, PreferredFQRNs: []kernel.RoleFQRN{kernel.RoleFQRN(value.role)}, Purpose: kernel.WorkflowPurposeHandoff, Risk: kernel.WorkflowRiskModerate, ConcurrencyGroup: value.stage, MaximumParallelism: 1, AttemptLimit: 1, AllowedOutgoingPurposes: []string{}, TargetSelection: kernel.WorkflowTargetCapability, ValidationPolicy: kernel.WorkflowValidationDeterministic})
	}
	definition.ContentDigest, err = definition.CalculatedDigest()
	if err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(dir, "forward.workflow.json"), definition, 0600)
	config.Organization.WorkflowDefinitions = []ProductionWorkflowSource{{DefinitionFile: "forward.workflow.json", DefinitionDigest: definition.ContentDigest}}
	config.Native.WorkflowTriggers = definition.TriggerTypes
	config.Native.ResultSchemas = nil
	// Source contracts use the checked-in closed architect envelope. PM source
	// is separately bound to a closed fixture plan record, not an open dict.
	designSchema, err := os.ReadFile("../../config/starter-team/transport-neutral-20260929/roles-v4/architect-2.1.1/handlers/story.design-requested/result.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var amendmentSchema map[string]any
	if err := json.Unmarshal(organization.PlanAmendmentProposalSchema(), &amendmentSchema); err != nil {
		t.Fatal(err)
	}
	planContract := amendmentSchema["properties"].(map[string]any)["successor_plan"].(map[string]any)
	planContract["properties"].(map[string]any)["version"].(map[string]any)["minimum"] = 1
	planSchema := mustJSON(map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object", "additionalProperties": false, "required": []string{"work_product"}, "properties": map[string]any{"work_product": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"result_type", "plan"}, "properties": map[string]any{"result_type": map[string]any{"const": "FEATURE_EXECUTION_PLAN"}, "plan": planContract}}}})
	for index, schema := range []json.RawMessage{designSchema, planSchema} {
		name := fmt.Sprintf("forward-%d.schema.json", index)
		writeText(t, filepath.Join(dir, name), string(schema), 0600)
		config.Native.ResultSchemas = append(config.Native.ResultSchemas, ProductionNativeResultSchema{Reference: []string{"design/v1", "plan/v1"}[index], Path: name, SHA256: digestBytes(schema)})
	}
	config.Worker.ReconciliationInterval = "10ms"
	// This synthetic acceptance exists only in t.TempDir, not in the candidate
	// package or any live production config.
	schema := organization.PlanAmendmentProposalSchema()
	schemaPath := filepath.Join(dir, "schemas", "feature-plan-amendment-proposal.schema.json")
	if err := os.MkdirAll(filepath.Dir(schemaPath), 0700); err != nil {
		t.Fatal(err)
	}
	writeText(t, schemaPath, string(schema), 0600)
	manifestBytes := mustJSON(map[string]any{"contract": map[string]any{"version": organization.PlanAmendmentContractVersion, "status": "FROZEN_CANDIDATE"}, "files": []map[string]any{{"path": "schemas/feature-plan-amendment-proposal.schema.json", "sha256": digestBytes(schema)}}})
	manifestPath, acceptancePath := filepath.Join(dir, "amendment-manifest.json"), filepath.Join(dir, "amendment-acceptance.json")
	writeText(t, manifestPath, string(manifestBytes), 0600)
	authority := ProductionPlanAmendment{ManifestFile: manifestPath, AcceptanceFile: acceptancePath, Policy: organization.PlanAmendmentPolicy{ContractVersion: organization.PlanAmendmentContractVersion, ManifestDigest: digestBytes(manifestBytes), Authority: kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "fixture-amendment-policy"}, AllowedRoots: []string{"service", "cmd"}}}
	acceptance := mustJSON(planAmendmentAcceptance{ContractVersion: authority.Policy.ContractVersion, ManifestDigest: authority.Policy.ManifestDigest, Decision: "ACCEPT/FREEZE", AcceptedBy: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "fixture-principal"}, AcceptedAt: time.Now().UTC().Add(-time.Minute), PolicyDigest: organization.PlanAmendmentDigest(authority.Policy)})
	writeText(t, acceptancePath, string(acceptance), 0600)
	authority.AcceptanceSHA256 = digestBytes(acceptance)
	config.Planning.PlanAmendment = &authority
	writeJSON(t, path, config, 0600)
	return path, config
}
