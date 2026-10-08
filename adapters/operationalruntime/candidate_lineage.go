package operationalruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func plannedTaskByID(plan organization.FeaturePlan, id kernel.UUIDv7) (organization.PlannedTask, bool) {
	for _, task := range plan.Tasks {
		if task.ID == id {
			return task, true
		}
	}
	return organization.PlannedTask{}, false
}

func acceptedImplementationDependencies(plan organization.FeaturePlan, task organization.PlannedTask, states map[kernel.UUIDv7]kernel.AggregateState) bool {
	for _, id := range task.DependsOn {
		producer, found := plannedTaskByID(plan, id)
		if !found {
			return false
		}
		if producer.Purpose == kernel.PurposeImplementation && (states[id].Phase != kernel.PhaseCompleted || states[id].Condition != kernel.ConditionRunnable) {
			return false
		}
	}
	return true
}

// Each consumer's retained receipt pins the producer invocation, output and
// Git identity. Latest state cannot retroactively change what it consumed.
func candidateLineageChanged(receipt taskWorkspaceReceipt, states map[kernel.UUIDv7]kernel.AggregateState, invocations map[kernel.UUIDv7]kernel.WorkInvocation) (taskWorkspaceComponent, bool) {
	for _, component := range receipt.Components {
		if component.InvocationID == "" {
			continue
		} // accepted-dependency legacy receipt
		latest, found := invocations[component.TaskID]
		state := states[component.TaskID]
		if state.Condition == kernel.ConditionBlocked || !found || latest.ID != component.InvocationID || latest.State != kernel.InvocationSucceeded || latest.OutputDigest == nil || *latest.OutputDigest != component.OutputSHA256 {
			return component, true
		}
	}
	return taskWorkspaceComponent{}, false
}

func (service *ProductionService) reconcileCandidateLineage(ctx context.Context, feature organization.FeatureRequest, plan organization.FeaturePlan, states map[kernel.UUIDv7]kernel.AggregateState, invocations map[kernel.UUIDv7]kernel.WorkInvocation, snapshot kernel.Snapshot) (bool, error) {
	if service.taskWorkspaces == nil {
		return false, nil
	}
	for _, task := range plan.Tasks {
		if task.Purpose != kernel.PurposeImplementation {
			continue
		}
		state := states[task.ID]
		ref := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}
		scope, found := snapshot.TaskOperationalScopes[ref]
		if !found || scope.WorktreeID == "" {
			continue
		}
		if filepath.Base(scope.WorktreeID) != scope.WorktreeID {
			return false, errInvalidTaskWorkspace
		}
		encoded, err := os.ReadFile(filepath.Join(service.taskWorkspaces.receiptRoot(), scope.WorktreeID+".json"))
		if err != nil {
			return false, err
		}
		var receipt taskWorkspaceReceipt
		if json.Unmarshal(encoded, &receipt) != nil || receipt.WorkID != task.ID || receipt.FeatureID != feature.ID || receipt.WorktreeID != scope.WorktreeID {
			return false, errInvalidTaskWorkspace
		}
		if !taskWorkspaceEvidenceBinds(snapshot, scope, encoded) {
			return false, errInvalidTaskWorkspace
		}
		// A published amendment preserves an explicitly blocked terminal output;
		// it is not an accepted candidate in the successor DAG. Revalidate its
		// exact amendment/checkpoint binding before excluding it from live
		// producer-readiness checks. Git identity is still checked below.
		liveReceipt := receipt
		liveReceipt.Components = make([]taskWorkspaceComponent, 0, len(receipt.Components))
		for _, component := range receipt.Components {
			if component.Readiness == "BLOCKED_CHECKPOINT" {
				expected, err := service.amendmentCheckpointComponent(ctx, feature, task)
				if err != nil {
					return false, err
				}
				if expected == nil || !taskWorkspaceComponentsMatch([]taskWorkspaceComponent{component}, []taskWorkspaceComponent{*expected}) {
					return false, fmt.Errorf("task %s checkpoint binding: %w", task.ID, errInvalidTaskWorkspace)
				}
				continue
			}
			liveReceipt.Components = append(liveReceipt.Components, component)
		}
		producer, stale := candidateLineageChanged(liveReceipt, states, invocations)
		if !stale {
			for _, component := range receipt.Components {
				if component.InvocationID == "" {
					continue
				}
				commit, err := service.taskWorkspaces.gitText(ctx, component.WorkingDirectory, "rev-parse", "HEAD")
				if err != nil {
					return false, err
				}
				tree, err := service.taskWorkspaces.gitText(ctx, component.WorkingDirectory, "rev-parse", "HEAD^{tree}")
				if err != nil {
					return false, err
				}
				if commit != component.Commit || tree != component.Tree {
					producer, stale = component, true
					break
				}
			}
		}
		if !stale {
			continue
		}
		// A finite forward recovery is required; never replay completed discovery
		// or overwrite the consumer's branch to pretend its inputs were current.
		reason := fmt.Sprintf("candidate input from task %s invocation %s was superseded or blocked; this consumer requires changed-input recovery", producer.TaskID, producer.InvocationID)
		service.recordRecoveryFault("candidate-lineage:"+string(task.ID), fmt.Errorf("%s", reason))
		latest, hasInvocation := invocations[task.ID]
		producerInvocation, hasProducer := invocations[producer.TaskID]
		if !hasProducer {
			return false, errInvalidTaskWorkspace
		}
		evidence, err := evidenceForInvocations(snapshot, producerInvocation)
		// A repair authorization has no terminal output yet. Bind the immutable
		// workspace receipt as evidence instead of inventing a repaired result.
		if err != nil {
			evidence = []kernel.EvidenceRef{}
			for _, id := range scope.InterfaceEvidenceIDs {
				if e, ok := snapshot.Evidence[id]; ok && e.SHA256 == digestBytes(encoded) {
					evidence = append(evidence, kernel.EvidenceRef{EvidenceID: id, SHA256: e.SHA256})
				}
			}
		}
		if len(evidence) == 0 {
			return false, errInvalidTaskWorkspace
		}
		if hasInvocation && latest.State == kernel.InvocationStarted && latest.CancellationRequestedAt == nil {
			if err := service.cancelSupersededCandidate(ctx, feature, latest, evidence, reason); err != nil {
				return false, err
			}
		}
		if state.Condition == kernel.ConditionRunnable && state.Phase != kernel.PhaseClosed {
			payload := mustJSON(map[string]any{"blocker_refs": []string{"teams://task/" + string(producer.TaskID)}, "reason": reason, "review_policy": "changed-input-forward-recovery"})
			_, err := service.submitStandaloneCommand(ctx, "tekroo.command.work.block", kernel.AggregateTask, task.ID, service.policyAuthority, state.Revision, payload, []kernel.DagParent{{ParentEventID: producerInvocation.LastEventID, EdgeKind: kernel.EdgeResponse}}, evidence, "candidate-lineage-"+string(task.ID)+"-"+string(producerInvocation.ID))
			return err == nil, err
		}
	}
	return false, nil
}

func taskWorkspaceEvidenceBinds(snapshot kernel.Snapshot, scope kernel.TaskOperationalScope, encoded []byte) bool {
	for _, id := range scope.InterfaceEvidenceIDs {
		if e, found := snapshot.Evidence[id]; found && e.Available && e.SHA256 == digestBytes(encoded) {
			return true
		}
	}
	return false
}

func (service *ProductionService) cancelSupersededCandidate(ctx context.Context, feature organization.FeatureRequest, invocation kernel.WorkInvocation, evidence []kernel.EvidenceRef, reason string) error {
	now := service.clock.Now().UTC()
	payload := mustJSON(map[string]any{"invocation_id": invocation.ID, "expected_invocation_revision": invocation.Revision, "reason": reason, "evidence_ids": evidenceIDs(evidence), "authority": service.policyAuthority, "requested_at": now})
	_, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.work-invocation.request-cancellation", kernel.OperationalSchemaVersion, kernel.AggregateWorkInvocation, invocation.ID, service.policyAuthority, invocation.Revision, payload, []kernel.DagParent{{ParentEventID: invocation.LastEventID, EdgeKind: kernel.EdgeCausal}}, evidence, "superseded-input-cancel-"+string(invocation.ID))
	return err
}
