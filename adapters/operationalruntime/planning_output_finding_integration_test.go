//go:build mongo_integration

package operationalruntime

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// Kernel/Mongo admission of a harness finding, not a model invocation. Keep
// the exact diagnostic and source digests without changing block classification.
func TestPlanningOutputFindingIsRegisteredAndBoundWithoutSpendingWork(t *testing.T) {
	process, uri := startRuntimeMongod(t)
	defer stopRuntimeMongod(process)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	path, config := writeNativeProductionFixture(t)
	writeText(t, filepath.Join(filepath.Dir(path), config.Mongo.URIFile), uri+"\n", 0600)
	policy := integratedPolicy()
	policy.Grants[3].Grantee.ID = "fixture::coder-1"
	policy.Grants[1].Scope.CommandTypes = append(policy.Grants[1].Scope.CommandTypes, "tekroo.command.work.block")
	writeJSON(t, filepath.Join(filepath.Dir(path), config.AuthorizationPolicyFile), policy, 0600)
	loaded, err := LoadProductionConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewProductionService(ctx, loaded)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close(contextWithTimeout(t))
	f := newIntegratedFixture(t, time.Now().UTC().Truncate(time.Millisecond), 55000, "fixture::coder-1", "workspace-1", "worktree-1")
	f.createAuthoritativeTaskWith(t, func(t *testing.T, command kernel.KernelCommand) kernel.CommandReceipt {
		receipt, err := service.Submit(ctx, command)
		if err != nil || receipt.OutcomeCode != kernel.OutcomeApplied {
			t.Fatalf("%s: %+v %v", command.CommandType, receipt, err)
		}
		return receipt
	})
	feature := organization.FeatureRequest{ID: id(55100), LifecycleEpoch: 1, Input: organization.FeatureRequestInput{WorkspaceID: "engineering"}, Design: &organization.FeatureDesignCandidate{OutputDigest: digestByte('a')}}
	output := digestByte('b')
	finished := time.Now().UTC()
	invocation := kernel.WorkInvocation{ID: f.invocationID, TaskID: f.taskID, State: kernel.InvocationSucceeded, ActorFQN: f.actor, Execution: kernel.ExecutionTuple{ExecutionID: f.executionID, FencingEpoch: 1}, LastEventID: f.lastTaskEvent, OutputDigest: &output, FinishedAt: &finished, TerminalEvidenceIDs: []kernel.UUIDv7{f.evidenceID}}
	task := organization.PlannedTask{ID: f.taskID, Owner: f.actor}
	ref := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}
	before, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: ref})
	if err != nil {
		t.Fatal(err)
	}
	diagnostic := errors.New("finalized plan changed accepted architecture, decisions, or assumptions")
	finding, err := service.registerPlanningOutputFinding(ctx, feature, stagePlanFinalization, invocation, diagnostic)
	if err != nil {
		t.Fatal(err)
	}
	// Exact retries retain one content-addressed finding and one registration.
	duplicate, err := service.registerPlanningOutputFinding(ctx, feature, stagePlanFinalization, invocation, diagnostic)
	if err != nil || duplicate != finding {
		t.Fatal("finding was not idempotent", err)
	}
	raw, err := service.Runtime.ReadExecutionOutput(ctx, finding.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	var record planningOutputFinding
	if json.Unmarshal(raw, &record) != nil || record.Message != diagnostic.Error() || record.OutputDigest != output || record.DesignDigest == nil || *record.DesignDigest != feature.Design.OutputDigest || record.Classification != invalidPlanningOutputReason {
		t.Fatalf("diagnostic receipt lost: %s", raw)
	}
	state, _, found, err := service.Store.ReadAggregateHead(ctx, ref)
	if err != nil || !found {
		t.Fatal(err)
	}
	if _, err := service.blockStructuredDecisionTask(ctx, feature, task, state, invocation, before, invalidPlanningOutputReason, "invalid-planning-output", finding); err != nil {
		t.Fatal(err)
	}
	_, head, _, err := service.Store.ReadAggregateHead(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	event, found, err := service.Store.ReadEvent(ctx, head)
	if err != nil || !found || !isExactInvalidPlanningOutputBlock(event, task, invocation, service.policyAuthority) {
		t.Fatalf("finding/classification absent in block: %+v %v", event, err)
	}
	after, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: ref})
	if err != nil {
		t.Fatal(err)
	}
	metadata, registered := after.Evidence[finding.EvidenceID]
	if !registered || !metadata.Available || metadata.SHA256 != finding.SHA256 {
		t.Fatal("diagnostic was not durably registered")
	}
	if !reflect.DeepEqual(before.WorkBudgetAccounts, after.WorkBudgetAccounts) || !reflect.DeepEqual(before.WorkInvocations, after.WorkInvocations) {
		t.Fatal("diagnostic spent work or changed finite budgets")
	}
}
