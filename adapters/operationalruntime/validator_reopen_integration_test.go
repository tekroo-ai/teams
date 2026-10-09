//go:build mongo_integration

package operationalruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/executionruntime"
	"github.com/tekroo-ai/teams/adapters/fake"
	"github.com/tekroo-ai/teams/adapters/mongo"
	"github.com/tekroo-ai/teams/adapters/openhands"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/contract"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// Disposable Mongo + real kernel. The worker is never started and the provider
// URL is unused: reopen/profile recovery must not spend a model invocation.
func TestChangedCandidateValidatorReopenRetainsEvidenceAndResumesCheckpoint(t *testing.T) {
	process, uri := startRuntimeMongod(t)
	defer stopRuntimeMongod(process)
	for _, interrupted := range []bool{false, true} {
		name := "fresh"
		if interrupted {
			name = "rejected_binding_checkpoint"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
			defer cancel()
			policy := integratedPolicy()
			policy.Grants[1].Scope.CommandTypes = append(policy.Grants[1].Scope.CommandTypes, "tekroo.command.work.reopen")
			store, err := mongo.Open(ctx, mongo.Config{URI: uri, Database: "validator_reopen_" + name, ContractIdentity: kernel.ContractIdentity, ManifestSHA256: phase4ManifestSHA, MigrationLevel: 1, Policy: policy, BacklogLimit: 1024, DeliveryPolicyRevision: 1})
			if err != nil {
				t.Fatal(err)
			}
			defer closeRuntimeStore(t, store)
			catalogue, err := contract.Load(os.DirFS(filepath.Join("..", "..")), "CONTRACTS/tekroo.kernel.contracts/0.12.0")
			if err != nil {
				t.Fatal(err)
			}
			provenance, err := fake.ProvenanceBasis()
			if err != nil {
				t.Fatal(err)
			}
			clock := SystemClock{}
			ids, err := NewUUIDv7Source(clock)
			if err != nil {
				t.Fatal(err)
			}
			f := newIntegratedFixture(t, time.Now().UTC().Truncate(time.Millisecond), 36000, "teams::coder-1", "reopen-workspace", "reopen-worktree")
			provider := newIntegratedFixture(t, f.now, 37000, "teams::coder-2", "provider-workspace", "provider-worktree")
			executionProfile, err := openhands.NewAcceptedExecutionProfile(f.modelDigest, f.runtimeDigest, f.toolDigest, f.effectDigest, 24, "validator_reopen_fixture", "unused")
			if err != nil {
				t.Fatal(err)
			}
			runtime, err := New(ctx, Config{
				Store: store, Catalogue: catalogue, Clock: clock, IDs: ids,
				OpenHandsBaseURL: "http://127.0.0.1:1", OpenHandsSessionAPIKey: "unused", HTTPClient: &http.Client{Timeout: time.Second},
				WorkspaceBindings: []openhands.WorkspaceBinding{{WorkspaceID: f.workspaceID, WorktreeID: f.worktreeID, WorkingDirectory: t.TempDir()}},
				ExecutionProfiles: []openhands.ExecutionProfile{executionProfile}, RoleGrounding: testRoleGroundingResolver{}, OpenHandsPollInterval: time.Millisecond,
				OpenHandsMaximumPages: 8, OpenHandsMaximumEvidence: 1 << 20, EvidenceRoot: t.TempDir(),
				ExecutionPolicy: application.OperationalExecutionPolicy{OperationTimeout: time.Second, MaximumBriefBytes: 1 << 20, ConsumerID: "reopen-fixture", PolicyRevision: 1, ServiceAuthority: f.service, ExpiryAuthority: f.policy, Provenance: provenance},
				EvidencePolicy:  application.CommandEvidenceRecorderPolicy{PolicyRevision: 1, Authority: f.service, Provenance: provenance, ProducingVersion: "fixture", RetentionPolicy: "fixture"},
				WorkerPolicy:    executionruntime.Policy{ConsumerID: "reopen-fixture", LeaseDuration: 3 * time.Second, ReconciliationInterval: 10 * time.Millisecond, MaximumReconciliations: 20, MaximumConcurrentInvocations: 1, LeaseOperationTimeout: time.Second},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.Close(contextWithTimeout(t))
			f.createAuthoritativeTask(t, runtime, provenance)
			provider.createAuthoritativeTask(t, runtime, provenance)
			submit := runtimeCommandSubmitter(runtime, provenance)
			refs := []kernel.EvidenceRef{{EvidenceID: f.evidenceID, SHA256: digestByte('e')}}
			reviewID := id(36910)
			finalized := completeProductSubject(t, f, submit, reviewID, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: f.taskID}, f.taskRevision, f.lastTaskEvent, "fixture tests passed", refs)
			completion := f.command(t, "tekroo.command.task.request-completion", kernel.SchemaVersion, kernel.AggregateTask, f.taskID, kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: string(f.actor)}, f.taskRevision,
				map[string]any{"artifact_digests": []kernel.Digest{digestByte('7')}, "branch_policy_revision": uint64(1), "completion_review_id": reviewID, "completion_review_revision": uint64(2), "criteria_revision": uint64(1), "evidence_ids": []kernel.UUIDv7{f.evidenceID}, "lifecycle_epoch": uint64(1), "owner_fqn": f.actor, "unresolved_exceptions": []string{}, "validation_finalized_event_id": finalized.EventIDs[0]},
				[]kernel.DagParent{{ParentEventID: finalized.EventIDs[0], EdgeKind: kernel.EdgeResponse}}, refs,
				[]kernel.AggregatePrecondition{{Aggregate: kernel.AggregateRef{Kind: kernel.AggregateCompletionReview, ID: reviewID}, Expected: kernel.NewExpectedRevision(3)}})
			execution := kernel.ExecutionTuple{ExecutionID: f.executionID, FencingEpoch: 1}
			completion.ActorFQN, completion.Execution = &f.actor, &execution
			submit(t, completion)
			taskRef := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: f.taskID}
			state, head, found, err := store.ReadAggregateHead(ctx, taskRef)
			if err != nil || !found || state.Phase != kernel.PhaseCompleted {
				t.Fatalf("complete fixture: %+v %v", state, err)
			}
			snapshot, err := store.LoadDecision(ctx, kernel.KernelCommand{Target: taskRef})
			if err != nil {
				t.Fatal(err)
			}
			current := snapshot.WorkProfiles[taskRef].Profile
			budgetBefore := snapshot.WorkBudgetAccounts[kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: f.budgetID}]
			planning := ProductionPlanning{PolicyRevision: 1, ClassificationPolicyDigest: digestByte('f'), PromotionPolicyDigest: digestByte('6'), VerificationTopologyDigest: digestByte('d')}
			service := &ProductionService{Store: store, Runtime: runtime, provenance: provenance, clock: clock, ids: ids, policyAuthority: f.policy, planning: planning}
			feature := organization.FeatureRequest{ID: id(36900), LifecycleEpoch: 1}
			validator := organization.PlannedTask{ID: f.taskID, Owner: f.actor, Purpose: kernel.PurposeValidation, AcceptanceCriteria: f.acceptanceCriteria, DependsOn: []kernel.UUIDv7{provider.taskID}, Validates: []kernel.UUIDv7{provider.taskID}}
			candidateRefs := []kernel.EvidenceRef{{EvidenceID: provider.evidenceID, SHA256: digestByte('e')}}
			criteria, _ := json.Marshal(validator.AcceptanceCriteria)
			candidateOutput := digestByte('4')
			condition, err := taskInvocationConditionDigest(current.ProfileDigest, digestBytes(criteria), []kernel.Digest{candidateOutput})
			if err != nil {
				t.Fatal(err)
			}
			if interrupted {
				state, head, err = service.reopenCompletedTaskForRecovery(ctx, feature, validator, state, head, TaskRecoveryRequest{Reason: "A validated dependency produced a changed candidate; revalidate only this dependent task against the new immutable candidate.", IdempotencyKey: "changed-candidate-" + string(condition)}, candidateRefs)
				if err != nil {
					t.Fatal(err)
				}
				successor, err := reopenedTaskRecoveryProfile(current, planning, condition, current.Budgets.DeadlineAt, evidenceIDs(candidateRefs), state.LifecycleEpoch, state.ScopeRevision)
				if err != nil {
					t.Fatal(err)
				}
				tracked := &trackedTask{plan: validator, revision: state.Revision, last: head, profile: successor}
				// Retain the original r55 defect: a complete payload paired with an
				// incomplete envelope set is rejected by the real kernel.
				err = service.applyTaskCommand(ctx, feature, tracked, "tekroo.command.task.bind-work-profile", kernel.SchemaVersion, f.policy, successor, candidateRefs, nil, "legacy-incomplete-binding")
				var rejected *deterministicCommandRejection
				if !errors.As(err, &rejected) || rejected.Receipt.ReasonCode != "UNAUTHORIZED" || rejected.Receipt.StateChanged {
					t.Fatalf("legacy counterexample: %v", err)
				}
				snapshot, err = store.LoadDecision(ctx, kernel.KernelCommand{Target: taskRef})
				if err != nil {
					t.Fatal(err)
				}
				oldOutput := digestByte('5')
				invocations := map[kernel.UUIDv7]kernel.WorkInvocation{
					validator.ID:    {ID: f.invocationID, State: kernel.InvocationSucceeded, OutputDigest: &oldOutput},
					provider.taskID: {ID: provider.invocationID, State: kernel.InvocationSucceeded, OutputDigest: &candidateOutput},
				}
				changed, err := service.reconcileValidationRounds(ctx, feature, organization.FeaturePlan{Tasks: []organization.PlannedTask{validator}}, map[kernel.UUIDv7]kernel.AggregateState{validator.ID: state}, map[kernel.UUIDv7]kernel.UUIDv7{validator.ID: head}, invocations, snapshot, budgetBefore.Revision, nil)
				if err != nil || !changed {
					t.Fatalf("resume committed checkpoint: changed=%t err=%v", changed, err)
				}
			} else {
				_, _, _, err = service.reopenCompletedValidatorForChangedCandidate(ctx, feature, validator, state, head, current, condition, candidateRefs)
				if err != nil {
					t.Fatal(err)
				}
			}
			state, head, _, err = store.ReadAggregateHead(ctx, taskRef)
			if err != nil || state.LifecycleEpoch != 2 || state.ScopeRevision != 2 || state.Phase != kernel.PhaseActive {
				t.Fatalf("reopened state: %+v %v", state, err)
			}
			snapshot, err = store.LoadDecision(ctx, kernel.KernelCommand{Target: taskRef})
			if err != nil {
				t.Fatal(err)
			}
			bound := snapshot.WorkProfiles[taskRef].Profile
			if !bound.Binding().TaskBindingMatches(state) || !containsEveryUUID(bound.ClassificationEvidenceIDs, []kernel.UUIDv7{f.evidenceID, provider.evidenceID}) {
				t.Fatalf("bound profile lost epoch or evidence: %+v", bound)
			}
			if bound.Budgets != current.Budgets || bound.SupersedesProfileID == nil || *bound.SupersedesProfileID != current.ProfileID {
				t.Fatal("finite budgets or profile lineage changed")
			}
			if !reflect.DeepEqual(budgetBefore, snapshot.WorkBudgetAccounts[kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: f.budgetID}]) || len(snapshot.WorkInvocations) != 0 {
				t.Fatal("profile recovery spent model work or changed shared budget")
			}
			// A duplicate delivery of the complete binding is idempotent, not a
			// second epoch, profile revision, or model debit.
			evidence, err := evidenceRefsForIDs(snapshot, bound.ClassificationEvidenceIDs)
			if err != nil {
				t.Fatal(err)
			}
			tracked := &trackedTask{plan: validator, revision: snapshot.WorkProfiles[taskRef].TaskRevision - 1, last: "", profile: bound}
			bindingEvent, found, err := store.ReadEvent(ctx, snapshot.WorkProfiles[taskRef].BoundEventID)
			if err != nil || !found || len(bindingEvent.Parents) != 1 {
				t.Fatal("missing binding event", err)
			}
			tracked.last = bindingEvent.Parents[0].ParentEventID
			key := fmt.Sprintf("changed-candidate-profile-v2-%d-%s", bound.ProfileRevision, bound.ProfileDigest)
			if err := service.applyTaskCommand(ctx, feature, tracked, "tekroo.command.task.bind-work-profile", kernel.SchemaVersion, f.policy, bound, evidence, nil, key); err != nil {
				t.Fatal(err)
			}
			repeated, repeatedHead, _, err := store.ReadAggregateHead(ctx, taskRef)
			if err != nil || !reflect.DeepEqual(state, repeated) || repeatedHead != head {
				t.Fatal("duplicate binding changed task state", err)
			}
		})
	}
}
