//go:build mongo_integration

package operationalruntime

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// Retain r57's rejection boundary using real kernel/Mongo admission. This is
// not a live model/coordinator run: it verifies that profile recovery preserves
// the complete history evidence in the authorized invocation's execution brief.
func TestPlanningProfileRecoversRejectedBindingAndRetainsHistoryAcrossRestart(t *testing.T) {
	process, uri := startRuntimeMongod(t)
	defer stopRuntimeMongod(process)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	path, config := writeNativeProductionFixture(t)
	writeText(t, filepath.Join(filepath.Dir(path), config.Mongo.URIFile), uri+"\n", 0o600)
	policy := integratedPolicy()
	policy.Grants[4].Grantee.ID = "fixture::coder-1"
	writeJSON(t, filepath.Join(filepath.Dir(path), config.AuthorizationPolicyFile), policy, 0o600)
	loaded, err := LoadProductionConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewProductionService(ctx, loaded)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(contextWithTimeout(t)) })
	f := newIntegratedFixture(t, time.Now().UTC(), 49000, "fixture::coder-1", "workspace-1", "worktree-1")
	submit := func(command kernel.KernelCommand) kernel.CommandReceipt {
		t.Helper()
		receipt, err := service.Submit(ctx, command)
		if err != nil || receipt.OutcomeCode != kernel.OutcomeApplied {
			t.Fatalf("%s: %+v %v", command.CommandType, receipt, err)
		}
		return receipt
	}
	story := submit(f.command(t, "tekroo.command.story.create", kernel.SchemaVersion, kernel.AggregateStory, f.storyID, f.human, 0,
		map[string]any{"title": "Returned design", "description": "Preserve the decision context", "acceptance_criteria": []string{"Return with prior history"}}, nil, nil, nil))
	refs := []kernel.EvidenceRef{{EvidenceID: f.evidenceID, SHA256: digestByte('e')}, {EvidenceID: id(49010), SHA256: digestByte('d')}}
	originalRefs := append([]kernel.EvidenceRef(nil), refs...)
	for _, ref := range refs {
		submit(f.command(t, "tekroo.command.evidence.register", kernel.SchemaVersion, kernel.AggregateEvidence, ref.EvidenceID, f.human, 0,
			map[string]any{"access_partition": "engineering", "availability": "AVAILABLE", "byte_length": 2, "canonical_digest": nil, "computation": nil, "deletion_tombstone": nil, "evidence_kind": "TEST_RESULT", "integrity_state": "DIGEST_VERIFIED", "locator": "artifact://planning/" + string(ref.EvidenceID), "locator_immutable": true, "media_type": "application/json", "producing_component": "planning-fixture", "producing_version": "1", "redacts": nil, "retention_policy": "test", "sensitivity": "INTERNAL", "sha256": ref.SHA256, "source_evidence_ids": []kernel.UUIDv7{}, "source_timestamp": nil, "transport_provenance": "teams://planning-fixture"}, nil, nil, nil))
	}
	task := organization.PlannedTask{ID: f.taskID, StoryID: f.storyID, Title: "Revise design", Description: "PM feedback: retain and revise the prior decision", AcceptanceCriteria: []string{"Answer from the original context"}, Purpose: kernel.PurposeHandoff, DecisionRoute: kernel.RouteComplexReasoning, Risk: organization.RiskModerate, AttemptLimit: 1, ReviewRoundLimit: 1}
	created := submit(f.command(t, "tekroo.command.task.create", kernel.SchemaVersion, kernel.AggregateTask, task.ID, f.human, 0,
		map[string]any{"story_id": task.StoryID, "title": task.Title, "description": task.Description, "acceptance_criteria": task.AcceptanceCriteria, "depends_on": []kernel.UUIDv7{}}, []kernel.DagParent{{ParentEventID: story.EventIDs[0], EdgeKind: kernel.EdgeCausal}}, nil, nil))
	feature := organization.FeatureRequest{ID: id(49011), LifecycleEpoch: 1, SubmittedBy: f.human, BudgetAccountID: f.budgetID}
	wp := service.workProfile(feature, task, 1, 1, refs[0].EvidenceID, time.Now().UTC().Add(time.Hour))
	tracked := &trackedTask{plan: task, profile: wp, revision: 1, last: created.EventIDs[0]}
	// Reproduce the durable pre-fix receipt, rather than silently replacing it.
	err = service.applyTaskCommand(ctx, feature, tracked, "tekroo.command.task.bind-work-profile", kernel.SchemaVersion, service.policyAuthority, wp, refs, nil, "profile")
	var rejected *deterministicCommandRejection
	if !errors.As(err, &rejected) || rejected.Receipt.ReasonCode != "UNAUTHORIZED" || rejected.Receipt.StateChanged {
		t.Fatalf("did not reproduce r57 rejection: %v", err)
	}
	for _, invalid := range [][]kernel.EvidenceRef{refs[1:], {refs[0], refs[0], refs[1]}, {{EvidenceID: refs[0].EvidenceID, SHA256: "invalid"}, refs[1]}} {
		if err := service.bindPlanningWorkProfile(ctx, feature, tracked, invalid); !errors.Is(err, organization.ErrInvalidFeature) {
			t.Fatalf("missing, duplicated or invalid classification evidence accepted: %v", err)
		}
	}
	if err := service.bindPlanningWorkProfile(ctx, feature, tracked, refs); err != nil {
		t.Fatalf("corrected profile could not pass admission: %v", err)
	}
	if !reflect.DeepEqual(tracked.profile, wp) || !reflect.DeepEqual(refs, originalRefs) {
		t.Fatal("profile recovery changed classification or discarded history")
	}
	ref := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}
	state, head, found, err := service.Store.ReadAggregateHead(ctx, ref)
	if err != nil || !found || state.Revision != 2 {
		t.Fatalf("expected one profile event: %+v %v", state, err)
	}
	if err := service.Close(ctx); err != nil {
		t.Fatal(err)
	}
	service, err = NewProductionService(ctx, loaded)
	if err != nil {
		t.Fatal(err)
	}
	tracked = &trackedTask{plan: task, profile: wp, revision: 1, last: created.EventIDs[0]}
	if err := service.bindPlanningWorkProfile(ctx, feature, tracked, refs); err != nil {
		t.Fatalf("restart: %v", err)
	}
	after, afterHead, found, err := service.Store.ReadAggregateHead(ctx, ref)
	if err != nil || !found || after.Revision != state.Revision || afterHead != head {
		t.Fatal("restart appended another profile event", err)
	}
	bad := &trackedTask{plan: task, profile: wp, revision: 1, last: created.EventIDs[0]}
	if err := service.applyTaskCommand(ctx, feature, bad, "tekroo.command.task.bind-work-profile", kernel.SchemaVersion, service.policyAuthority, wp, refs, nil, "profile"); err == nil || !strings.Contains(err.Error(), "UNAUTHORIZED") {
		t.Fatalf("old rejection or policy changed: %v", err)
	}
	profile := loaded.Profiles[0]
	profile.DecisionRoute = kernel.RouteComplexReasoning
	profile.QualificationCorpus, profile.Qualification = testQualificationBundle(t, profile.ModelProfileDigest, "coder", profile.DecisionRoute, profile.ToolPolicyDigest, allTestWorkKinds(), time.Now().UTC().Add(-time.Minute))
	owner := organization.RoleInstanceState{ActorFQN: f.actor, ModelProfile: profile.ModelProfileDigest, WorkspaceID: loaded.Workspaces[0].WorkspaceID, Execution: kernel.ExecutionTuple{ExecutionID: f.executionID, FencingEpoch: 1}}
	if err := service.registerExecution(ctx, owner, profile); err != nil {
		t.Fatal(err)
	}
	limits := make(kernel.PurposeCounters)
	for _, purpose := range kernel.AllWorkPurposes {
		limits[purpose] = 2
	}
	submit(f.command(t, "tekroo.command.work-budget.create", kernel.OperationalSchemaVersion, kernel.AggregateWorkBudget, f.budgetID, f.policy, 0,
		map[string]any{"budget_account_id": f.budgetID, "root_work": kernel.AggregateRef{Kind: kernel.AggregateStory, ID: f.storyID}, "lifecycle_epoch": 1, "policy_revision": service.planning.PolicyRevision, "policy_digest": service.planning.BudgetPolicyDigest, "model_invocation_limit": 4, "purpose_limits": limits, "deadline_at": wp.Budgets.DeadlineAt, "evidence_ids": evidenceIDs(refs), "authority": f.policy}, []kernel.DagParent{{ParentEventID: story.EventIDs[0], EdgeKind: kernel.EdgeDerivation}}, refs, []kernel.AggregatePrecondition{{Aggregate: kernel.AggregateRef{Kind: kernel.AggregateStory, ID: f.storyID}, Expected: kernel.NewExpectedRevision(1)}}))
	tracked.owner, tracked.plan.Owner, tracked.plan.ModelProfile = owner, owner.ActorFQN, owner.ModelProfile
	service.Controller = &Controller{runtime: service.Runtime, clock: service.clock, state: ControlRunning}
	if err := service.activateTask(ctx, feature, tracked, profile, loaded.Workspaces[0], 1, nil, refs, refs[0].EvidenceID, []kernel.Digest{refs[0].SHA256, refs[1].SHA256}); err != nil {
		t.Fatalf("normal admission: %v", err)
	}
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: ref})
	if err != nil {
		t.Fatal(err)
	}
	invocation, present := latestTaskInvocation(snapshot.WorkInvocations, task.ID)
	if !present || invocation.State != kernel.InvocationAuthorized || invocation.AttemptOrdinal != 1 || len(snapshot.WorkInvocations) != 1 {
		t.Fatalf("duplicate or missing invocation: %+v", invocation)
	}
	current, err := service.Store.LoadOperationalExecution(ctx, invocation.ID)
	if err != nil {
		t.Fatal(err)
	}
	grounding, err := service.roleGrounding.ResolveRoleGrounding(ctx, invocation.ActorFQN)
	if err != nil {
		t.Fatal(err)
	}
	brief, _, err := application.BuildExecutionBrief(current, grounding, loaded.Execution.MaximumBriefBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !sameEvidenceSet(brief.Evidence, refs) || brief.ActorFQN != owner.ActorFQN || brief.DeadlineAt != wp.Budgets.DeadlineAt || !strings.Contains(brief.Task.Description, "PM feedback") {
		t.Fatalf("admission lost context, owner, or finite deadline: %+v", brief)
	}
}
