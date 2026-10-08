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

// Retain r54's actual failure boundary: an advisory task exists, its old
// profile command has a durable unauthorized receipt, and reconciliation
// must bind the complete source/question evidence without relaxing policy.
// Uses an isolated mongod and native service, never the live team or model.
func TestConsultationProfileRecoversRejectedEvidenceBindingAcrossRestart(t *testing.T) {
	process, uri := startRuntimeMongod(t)
	defer stopRuntimeMongod(process)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	path, config := writeNativeProductionFixture(t)
	dir := filepath.Dir(path)
	writeText(t, filepath.Join(dir, config.Mongo.URIFile), uri+"\n", 0o600)
	policy := integratedPolicy()
	for index, actor := range []string{"teams::coder-3", "teams::coder-4", "teams::coder-5"} {
		isolatedActor := policy.Grants[4]
		isolatedActor.Grantee.ID, isolatedActor.GrantDigest = actor, digestByte(byte('7'+index))
		policy.Grants = append(policy.Grants, isolatedActor)
	}
	writeJSON(t, filepath.Join(dir, config.AuthorizationPolicyFile), policy, 0o600)
	loaded, err := LoadProductionConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewProductionService(ctx, loaded)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(contextWithTimeout(t)) })
	f := newIntegratedFixture(t, time.Now().UTC(), 39000, "teams::coder-1", "workspace-1", "worktree-1")
	submit := func(command kernel.KernelCommand) kernel.CommandReceipt {
		t.Helper()
		r, err := service.Submit(ctx, command)
		if err != nil || r.OutcomeCode != kernel.OutcomeApplied {
			t.Fatalf("%s: receipt=%+v error=%v", command.CommandType, r, err)
		}
		return r
	}
	story := submit(f.command(t, "tekroo.command.story.create", kernel.SchemaVersion, kernel.AggregateStory, f.storyID, f.human, 0,
		map[string]any{"title": "Advisory fixture", "description": "Retain blocked work", "acceptance_criteria": []string{"Question reaches design owner"}}, nil, nil, nil))
	refs := []kernel.EvidenceRef{{EvidenceID: f.evidenceID, SHA256: digestByte('e')}, {EvidenceID: id(39010), SHA256: digestByte('d')}, {EvidenceID: id(39012), SHA256: digestByte('c')}, {EvidenceID: id(39013), SHA256: digestByte('b')}}
	for _, ref := range refs {
		submit(f.command(t, "tekroo.command.evidence.register", kernel.SchemaVersion, kernel.AggregateEvidence, ref.EvidenceID, f.human, 0,
			map[string]any{"access_partition": "engineering", "availability": "AVAILABLE", "byte_length": 2, "canonical_digest": nil, "computation": nil, "deletion_tombstone": nil, "evidence_kind": "TEST_RESULT", "integrity_state": "DIGEST_VERIFIED", "locator": "artifact://consultation/" + string(ref.EvidenceID), "locator_immutable": true, "media_type": "application/json", "producing_component": "consultation-fixture", "producing_version": "1", "redacts": nil, "retention_policy": "test", "sensitivity": "INTERNAL", "sha256": ref.SHA256, "source_evidence_ids": []kernel.UUIDv7{}, "source_timestamp": nil, "transport_provenance": "teams://consultation-fixture"}, nil, nil, nil))
	}
	task := organization.PlannedTask{ID: f.taskID, StoryID: f.storyID, Title: "Clarify blocked interface", Description: "Answer without expanding authority", AcceptanceCriteria: []string{"Answer within scope or escalate"}, Purpose: kernel.PurposeHandoff, DecisionRoute: kernel.RouteComplexReasoning, Risk: organization.RiskModerate, AttemptLimit: 1, ReviewRoundLimit: 1}
	r := submit(f.command(t, "tekroo.command.task.create", kernel.SchemaVersion, kernel.AggregateTask, task.ID, f.human, 0,
		map[string]any{"story_id": task.StoryID, "title": task.Title, "description": task.Description, "acceptance_criteria": task.AcceptanceCriteria, "depends_on": []kernel.UUIDv7{}}, []kernel.DagParent{{ParentEventID: story.EventIDs[0], EdgeKind: kernel.EdgeCausal}}, nil, nil))
	feature := organization.FeatureRequest{ID: id(39011), LifecycleEpoch: 1, SubmittedBy: f.human}
	wp := service.workProfile(feature, task, 1, 1, refs[0].EvidenceID, time.Now().UTC().Add(time.Hour))
	tracked := &trackedTask{plan: task, profile: wp, revision: 1, last: r.EventIDs[0]}
	// Raw r54 provenance retained four attached references, not just its first ID.
	err = service.applyTaskCommand(ctx, feature, tracked, "tekroo.command.task.bind-work-profile", kernel.SchemaVersion, service.policyAuthority, wp, refs, nil, "profile")
	var rejected *deterministicCommandRejection
	if !errors.As(err, &rejected) || rejected.Receipt.ReasonCode != "UNAUTHORIZED" || rejected.Receipt.StateChanged || !strings.Contains(err.Error(), "UNAUTHORIZED") {
		t.Fatalf("r54 mismatch was not reproduced: %v", err)
	}
	qualifyConsultationFaultTerminalIsolation(t, ctx, service, feature, submit, false)
	qualifyConsultationFaultTerminalIsolation(t, ctx, service, feature, submit, true)
	if err := service.bindConsultationWorkProfile(ctx, feature, tracked, refs); err != nil {
		t.Fatalf("corrected builder replayed the old rejection: %v", err)
	}
	if !reflect.DeepEqual(tracked.profile.ClassificationEvidenceIDs, evidenceIDs(refs)) {
		t.Fatal("source or question evidence omitted from advisory classification")
	}
	ref := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}
	state, head, found, err := service.Store.ReadAggregateHead(ctx, ref)
	if err != nil || !found || state.Revision != 2 {
		t.Fatalf("expected exactly one profile binding: %+v %v", state, err)
	}
	if err := service.Close(ctx); err != nil {
		t.Fatal(err)
	}
	service, err = NewProductionService(ctx, loaded)
	if err != nil {
		t.Fatal(err)
	}
	// Reconstruct the pre-command checkpoint: replay must return the same
	// durable applied receipt, not append an event or create a second node.
	tracked = &trackedTask{plan: task, profile: wp, revision: 1, last: r.EventIDs[0]}
	if err := service.bindConsultationWorkProfile(ctx, feature, tracked, refs); err != nil {
		t.Fatalf("restart could not recover the binding: %v", err)
	}
	after, afterHead, found, err := service.Store.ReadAggregateHead(ctx, ref)
	if err != nil || !found || after.Revision != state.Revision || afterHead != head {
		t.Fatal("replay appended another profile event", err)
	}
	// The old malformed command must still be rejected; no kernel rule or
	// authority grant was widened to make the corrected command pass.
	bad := &trackedTask{plan: task, profile: wp, revision: 1, last: r.EventIDs[0]}
	if err := service.applyTaskCommand(ctx, feature, bad, "tekroo.command.task.bind-work-profile", kernel.SchemaVersion, service.policyAuthority, wp, refs, nil, "profile"); err == nil || !strings.Contains(err.Error(), "UNAUTHORIZED") {
		t.Fatalf("old unauthorized receipt was altered: %v", err)
	}
	// Continue through normal admission, not a hand-built invocation. The
	// model worker remains stopped: only isolated kernel/Mongo state is used.
	profile := loaded.Profiles[0]
	profile.DecisionRoute = kernel.RouteComplexReasoning
	profile.QualificationCorpus, profile.Qualification = testQualificationBundle(t, profile.ModelProfileDigest, "coder", profile.DecisionRoute, profile.ToolPolicyDigest, allTestWorkKinds(), time.Now().UTC().Add(-time.Minute))
	owner := organization.RoleInstanceState{ActorFQN: f.actor, ModelProfile: profile.ModelProfileDigest, WorkspaceID: loaded.Workspaces[0].WorkspaceID, Execution: kernel.ExecutionTuple{ExecutionID: f.executionID, FencingEpoch: 1}}
	if err := service.registerExecution(ctx, owner, profile); err != nil {
		t.Fatal(err)
	}
	feature.BudgetAccountID = f.budgetID
	limits := make(kernel.PurposeCounters)
	for _, purpose := range kernel.AllWorkPurposes {
		limits[purpose] = 2
	}
	submit(f.command(t, "tekroo.command.work-budget.create", kernel.OperationalSchemaVersion, kernel.AggregateWorkBudget, f.budgetID, f.policy, 0,
		map[string]any{"budget_account_id": f.budgetID, "root_work": kernel.AggregateRef{Kind: kernel.AggregateStory, ID: f.storyID}, "lifecycle_epoch": 1, "policy_revision": service.planning.PolicyRevision, "policy_digest": service.planning.BudgetPolicyDigest, "model_invocation_limit": 4, "purpose_limits": limits, "deadline_at": tracked.profile.Budgets.DeadlineAt, "evidence_ids": evidenceIDs(refs), "authority": f.policy},
		[]kernel.DagParent{{ParentEventID: story.EventIDs[0], EdgeKind: kernel.EdgeDerivation}}, refs,
		[]kernel.AggregatePrecondition{{Aggregate: kernel.AggregateRef{Kind: kernel.AggregateStory, ID: f.storyID}, Expected: kernel.NewExpectedRevision(1)}}))
	tracked.owner = owner
	tracked.plan.Owner = owner.ActorFQN
	tracked.plan.ModelProfile = owner.ModelProfile
	workspace := loaded.Workspaces[0]
	// Supply RUNNING admission state without calling Start: no worker,
	// recovery loop, HTTP listener or model request is launched by this test.
	service.Controller = &Controller{runtime: service.Runtime, clock: service.clock, state: ControlRunning}
	if err := service.activateTask(ctx, feature, tracked, profile, workspace, 1, nil, refs, refs[0].EvidenceID, []kernel.Digest{refs[0].SHA256, refs[1].SHA256}); err != nil {
		t.Fatalf("corrected profile did not reach normal advisory admission: %v", err)
	}
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: ref})
	if err != nil {
		t.Fatal(err)
	}
	invocation, present := latestTaskInvocation(snapshot.WorkInvocations, task.ID)
	if !present || invocation.State != kernel.InvocationAuthorized || invocation.Purpose != kernel.PurposeHandoff || invocation.AttemptOrdinal != 1 || invocation.ActorFQN != owner.ActorFQN || snapshot.State == nil || snapshot.State.Phase != kernel.PhaseActive {
		t.Fatalf("advisory was not admitted exactly once to its decision owner: %+v", invocation)
	}
	if len(snapshot.WorkInvocations) != 1 || len(tracked.plan.WriteScope) != 0 {
		t.Fatal("admission created duplicate work or an implementation write scope")
	}
}

// The terminal invocation bindings are fixture projections; the task admission,
// evidence registration and block transition run through real Mongo/kernel.
// This tests terminal ingestion, not a model-driven coordinator round trip.
func qualifyConsultationFaultTerminalIsolation(t *testing.T, ctx context.Context, service *ProductionService, feature organization.FeatureRequest, submit func(kernel.KernelCommand) kernel.CommandReceipt, malformed bool) {
	t.Helper()
	now := time.Now().UTC()
	question := newIntegratedFixture(t, now, 40000, "teams::coder-3", "question-workspace", "question-worktree")
	verifier := newIntegratedFixture(t, now, 42000, "teams::coder-2", "verifier-workspace", "verifier-worktree")
	evidenceBase := 43010
	if malformed {
		question = newIntegratedFixture(t, now, 44000, "teams::coder-4", "question-2-workspace", "question-2-worktree")
		verifier = newIntegratedFixture(t, now, 46000, "teams::coder-5", "verifier-2-workspace", "verifier-2-worktree")
		evidenceBase = 47010
	}
	writer := func(_ *testing.T, c kernel.KernelCommand) kernel.CommandReceipt { return submit(c) }
	question.createAuthoritativeTaskWith(t, writer)
	verifier.createAuthoritativeTaskWith(t, writer)
	plan := organization.FeaturePlan{Tasks: []organization.PlannedTask{
		{ID: question.taskID, Purpose: kernel.PurposeImplementation},
		{ID: verifier.taskID, Purpose: kernel.PurposeValidation},
	}}
	if malformed {
		// This purpose and allowance would normally permit a technical retry.
		// Terminal-only mode must record the block instead of invoking RoleHost.
		plan.Tasks[1].Purpose, plan.Tasks[1].AttemptLimit = kernel.PurposePromotion, 2
	}
	states := make(map[kernel.UUIDv7]kernel.AggregateState)
	heads := make(map[kernel.UUIDv7]kernel.UUIDv7)
	invocations := make(map[kernel.UUIDv7]kernel.WorkInvocation)
	for index, f := range []*integratedFixture{question, verifier} {
		output := []byte(application.OrganizationalResultMarker + `{"outcome":"needs_decision","summary":"Missing interface owner"}`)
		if index == 1 {
			output = []byte(application.OrganizationalResultMarker + `{"outcome":"failed","summary":"Public persistence operations were not tested"}`)
			if malformed {
				output = []byte(application.OrganizationalResultMarker + `{`)
			}
		}
		evidenceID := id(evidenceBase + index)
		blob, err := service.Runtime.evidence.Put(ctx, evidenceID, output)
		if err != nil {
			t.Fatal(err)
		}
		submit(f.command(t, "tekroo.command.evidence.register", kernel.SchemaVersion, kernel.AggregateEvidence, evidenceID, f.human, 0,
			map[string]any{"access_partition": "engineering", "availability": "AVAILABLE", "byte_length": blob.ByteLength, "canonical_digest": nil, "computation": nil, "deletion_tombstone": nil, "evidence_kind": "TEST_RESULT", "integrity_state": "DIGEST_VERIFIED", "locator": blob.Locator, "locator_immutable": true, "media_type": "application/json", "producing_component": "terminal-isolation-fixture", "producing_version": "1", "redacts": nil, "retention_policy": "test", "sensitivity": "INTERNAL", "sha256": blob.SHA256, "source_evidence_ids": []kernel.UUIDv7{}, "source_timestamp": nil, "transport_provenance": "teams://terminal-isolation-fixture"}, nil, nil, nil))
		state, head, found, err := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: f.taskID})
		if err != nil || !found {
			t.Fatal("task fixture unavailable", err)
		}
		states[f.taskID], heads[f.taskID] = state, head
		invocations[f.taskID] = kernel.WorkInvocation{ID: f.invocationID, TaskID: f.taskID, State: kernel.InvocationSucceeded, OutputDigest: &blob.SHA256, ActorFQN: f.actor, Execution: kernel.ExecutionTuple{ExecutionID: f.executionID, FencingEpoch: 1}, LastEventID: head, TerminalEvidenceIDs: []kernel.UUIDv7{evidenceID}, AttemptOrdinal: 1}
	}
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: verifier.taskID}})
	if err != nil {
		t.Fatal(err)
	}
	changed, err := service.reconcileCompletionsAfterConsultationRejection(ctx, feature, plan, states, heads, invocations, snapshot)
	if err != nil || !changed {
		t.Fatal("unrelated terminal finding was starved", err)
	}
	qState, qHead, _, err := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: question.taskID})
	if err != nil || !reflect.DeepEqual(qState, states[question.taskID]) || qHead != heads[question.taskID] {
		t.Fatal("terminal-only recovery changed the pending question", err)
	}
	vState, _, _, err := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: verifier.taskID})
	if err != nil || vState.Condition != kernel.ConditionBlocked {
		t.Fatal("negative finding not recorded", err)
	}
	snapshot, err = service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: verifier.taskID}})
	if err != nil || len(snapshot.WorkInvocations) != 0 {
		t.Fatal("terminal-only path admitted new model work", err)
	}
}
