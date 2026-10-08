//go:build mongo_integration

package operationalruntime

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// Qualification of preparation, not a live model-driven amendment. Real native
// service, signed role fixture, kernel commands, Mongo and service close/reopen;
// the fixture supplies an approved plan to isolate the prepublication boundary.
func TestAmendmentPrepublicationCreatesOnlyFreshUnassignedNodesAcrossRestart(t *testing.T) {
	process, uri := startRuntimeMongod(t)
	defer stopRuntimeMongod(process)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	path, config := writeNativeProductionFixture(t)
	dir := filepath.Dir(path)
	writeText(t, filepath.Join(dir, config.Mongo.URIFile), uri+"\n", 0600)
	writeJSON(t, filepath.Join(dir, config.AuthorizationPolicyFile), integratedPolicy(), 0600)
	var manifest organization.TeamManifest
	if err := readStrictJSONFile(filepath.Join(dir, "team.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Roles[0].InitialInstances, manifest.Roles[0].MaximumInstances = 2, 2
	manifest.Roles[0].WorkspaceIDs = []string{"workspace-1", "workspace-2"}
	writeJSON(t, filepath.Join(dir, "team.json"), manifest, 0600)
	raw, err := os.ReadFile(filepath.Join(dir, "team.json"))
	if err != nil {
		t.Fatal(err)
	}
	config.Organization.ManifestDigest = digestBytes(raw)
	second := config.Workspaces[0]
	second.WorkspaceID, second.WorktreeID = "workspace-2", "worktree-2"
	config.Workspaces = append(config.Workspaces, second)
	writeJSON(t, path, config, 0600)
	loaded, err := LoadProductionConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewProductionService(ctx, loaded)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(contextWithTimeout(t)) })
	t.Log("native service opened", time.Now().UTC())
	now := time.Now().UTC()
	f := newIntegratedFixture(t, now, 41000, "fixture::coder-1", "workspace-1", "worktree-1")
	feature := organization.FeatureRequest{ID: id(41011), SubmittedBy: f.human, ProductOwnerActor: "fixture::product-owner-1", Input: organization.FeatureRequestInput{Team: "fixture", WorkspaceID: "workspace-1", MaximumTasks: 8}, BudgetAccountID: f.budgetID, LifecycleEpoch: 1, ScopeRevision: 1, CreatedAt: now, UpdatedAt: now}
	story := organization.PlannedStory{ID: f.storyID, Title: "Unchanged product", Description: "Retain existing work", AcceptanceCriteria: []string{"works"}, Priority: organization.PriorityNormal}
	prior := organization.PlannedTask{ID: f.taskID, StoryID: story.ID, Title: "Retained task", Description: "Unchanged task", AcceptanceCriteria: []string{"verified"}, Owner: "fixture::coder-1", ModelProfile: loaded.Profiles[0].ModelProfileDigest, Purpose: kernel.PurposeImplementation, DecisionRoute: kernel.RouteBoundedExecution, Complexity: 2, Risk: organization.RiskLow, AttemptLimit: 2, ReviewRoundLimit: 1}
	plan := organization.FeaturePlan{Version: 1, PreparedBy: "fixture::project-manager-1", PreparedExecution: kernel.ExecutionTuple{ExecutionID: id(41012), FencingEpoch: 1}, Architecture: "Same design", Stories: []organization.PlannedStory{story}, Tasks: []organization.PlannedTask{prior}, CreatedAt: now}
	created, err := service.submitPlannedCommand(ctx, feature, "tekroo.command.story.create", kernel.AggregateStory, story.ID, "story", mustJSON(map[string]any{"title": story.Title, "description": story.Description, "acceptance_criteria": story.AcceptanceCriteria}), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.submitPlannedCommand(ctx, feature, "tekroo.command.task.create", kernel.AggregateTask, prior.ID, "task", mustJSON(map[string]any{"story_id": story.ID, "title": prior.Title, "description": prior.Description, "acceptance_criteria": prior.AcceptanceCriteria, "depends_on": []kernel.UUIDv7{}}), []kernel.DagParent{{ParentEventID: created.EventIDs[0], EdgeKind: kernel.EdgeCausal}})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.activatePlannedStory(ctx, feature, story.ID, created.EventIDs[0], []kernel.UUIDv7{prior.ID}); err != nil {
		t.Fatal(err)
	}
	before, beforeHead, _, err := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: prior.ID})
	if err != nil {
		t.Fatal(err)
	}
	limits := kernel.PurposeCounters{}
	for _, purpose := range kernel.AllWorkPurposes {
		limits[purpose] = 10
	}
	evidence := []kernel.EvidenceRef{{EvidenceID: f.evidenceID, SHA256: digestByte('e')}}
	registration := mustJSON(map[string]any{"access_partition": "fixture", "availability": "AVAILABLE", "byte_length": 2, "canonical_digest": nil, "computation": nil, "deletion_tombstone": nil, "evidence_kind": "TEST_RESULT", "integrity_state": "DIGEST_VERIFIED", "locator": "artifact://amendment-fixture/precondition", "locator_immutable": true, "media_type": "application/json", "producing_component": "amendment-fixture", "producing_version": "1", "redacts": nil, "retention_policy": "test", "sensitivity": "INTERNAL", "sha256": evidence[0].SHA256, "source_evidence_ids": []kernel.UUIDv7{}, "source_timestamp": nil, "transport_provenance": "teams://amendment-fixture"})
	if _, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.evidence.register", kernel.SchemaVersion, kernel.AggregateEvidence, f.evidenceID, service.serviceAuthority, 0, registration, nil, nil, "precondition"); err != nil {
		t.Fatal(err)
	}
	budgetPayload := mustJSON(map[string]any{"budget_account_id": feature.BudgetAccountID, "root_work": kernel.AggregateRef{Kind: kernel.AggregateStory, ID: story.ID}, "lifecycle_epoch": 1, "policy_revision": service.planning.PolicyRevision, "policy_digest": service.planning.BudgetPolicyDigest, "model_invocation_limit": 20, "purpose_limits": limits, "deadline_at": now.Add(time.Hour), "evidence_ids": evidenceIDs(evidence), "authority": f.policy})
	rootState, _, _, err := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateStory, ID: story.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.work-budget.create", kernel.OperationalSchemaVersion, kernel.AggregateWorkBudget, feature.BudgetAccountID, f.policy, 0, budgetPayload, []kernel.DagParent{{ParentEventID: created.EventIDs[0], EdgeKind: kernel.EdgeDerivation}}, evidence, "budget", kernel.AggregatePrecondition{Aggregate: kernel.AggregateRef{Kind: kernel.AggregateStory, ID: story.ID}, Expected: kernel.NewExpectedRevision(rootState.Revision)}); err != nil {
		t.Fatal(err)
	}
	fresh := prior
	fresh.ID, fresh.Title, fresh.WriteScope = id(41013), "Successor work", []string{"service/consumer.go"}
	fresh.DependsOn = []kernel.UUIDv7{prior.ID}
	validator := fresh
	validator.ID, validator.Owner, validator.Purpose, validator.WriteScope = id(41014), "fixture::coder-2", kernel.PurposeValidation, nil
	validator.DependsOn, validator.Validates = []kernel.UUIDv7{fresh.ID}, []kernel.UUIDv7{fresh.ID}
	successor := plan
	successor.Version++
	successor.Tasks = []organization.PlannedTask{prior, fresh, validator}
	feature.Plan = &plan
	feature.PlanAmendment = &organization.FeaturePlanAmendment{State: organization.AmendmentApproved, PriorPlan: plan, Proposal: &organization.PlanAmendmentProposal{SuccessorPlan: successor}}
	var priorBudget kernel.WorkBudgetAccount
	for pass := 0; pass < 2; pass++ {
		if err := service.materializeAmendmentSuccessors(ctx, feature); err != nil {
			t.Fatal("prepare", err)
		}
		t.Log("successors prepared", pass, time.Now().UTC())
		after, head, _, err := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: prior.ID})
		if err != nil || !reflect.DeepEqual(before, after) || beforeHead != head {
			t.Fatal("retained task changed", err)
		}
		for _, task := range []organization.PlannedTask{fresh, validator} {
			snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}})
			if err != nil || snapshot.State == nil || snapshot.State.Phase != kernel.PhasePlanned || snapshot.State.Revision != 2 || !snapshot.WorkProfiles[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}].Valid() || len(snapshot.WorkInvocations) != 0 {
				t.Fatal("prepared successor admitted or duplicated", err)
			}
			budget := snapshot.WorkBudgetAccounts[kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: feature.BudgetAccountID}]
			if pass == 0 {
				priorBudget = budget
			} else if !reflect.DeepEqual(priorBudget, budget) {
				t.Fatal("budget changed across replay")
			}
			if _, exists, err := service.MessageBus.Read(ctx, plannedTaskMessageID(feature.ID, task.ID)); err != nil || exists {
				t.Fatal("dispatchable message exposed before publication", err)
			}
		}
		if pass == 0 {
			if err := service.Close(ctx); err != nil {
				t.Fatal(err)
			}
			t.Log("native service closed", time.Now().UTC())
			service, err = NewProductionService(ctx, loaded)
			if err != nil {
				t.Fatal(err)
			}
			t.Log("native service reopened", time.Now().UTC())
		}
	}
}
