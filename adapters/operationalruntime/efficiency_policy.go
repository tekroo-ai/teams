package operationalruntime

import (
	"fmt"
	"slices"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// This is an explicit successor orchestration policy, never a change to the
// default accepted-dependency predicate or to kernel completion/acceptance.
type CandidateReadinessPolicy struct {
	SchemaVersion string        `json:"schema_version"`
	Mode          string        `json:"mode"`
	PolicyDigest  kernel.Digest `json:"policy_digest"`
}

func (planning ProductionPlanning) candidateOverlapEnabled() bool {
	p := planning.CandidateReadiness
	return p != nil && p.SchemaVersion == "1.0.0" && p.Mode == "CANDIDATE_READY" && p.PolicyDigest.Valid()
}

func (planning ProductionPlanning) validateEfficiencyPolicy(gates map[string]struct{}) error {
	if planning.CandidateReadiness != nil && !planning.candidateOverlapEnabled() {
		return fmt.Errorf("invalid candidate-readiness policy")
	}
	if len(planning.TaskGateIDs) > 32 {
		return fmt.Errorf("too many task gates")
	}
	seen := make(map[string]bool)
	for _, id := range planning.TaskGateIDs {
		if _, ok := gates[id]; !ok || seen[id] {
			return fmt.Errorf("missing or duplicate task gate %q", id)
		}
		seen[id] = true
	}
	return nil
}

func (planning ProductionPlanning) gateIDs(plan *organization.FeaturePlan, task organization.PlannedTask) []string {
	ids := planning.RequiredGateIDs
	// Whole-feature validation and promotion always run the complete gate set.
	// Unknown plan identity also defaults to that stricter set.
	if plan != nil && len(planning.TaskGateIDs) > 0 && task.Purpose != kernel.PurposePromotion {
		whole, found := wholeFeatureValidationTaskID(*plan)
		if found && whole != task.ID {
			ids = planning.TaskGateIDs
		}
	}
	return append([]string(nil), ids...)
}

func candidateReceiptGateIDs(receipt candidateReceipt) []string {
	ids := make([]string, len(receipt.GateReceipts))
	for i, gate := range receipt.GateReceipts {
		ids[i] = gate.GateID
	}
	return ids
}

func candidateDependencyReady(policy ProductionPlanning, consumer, producer organization.PlannedTask, state kernel.AggregateState, invocation kernel.WorkInvocation, found bool) bool {
	if state.Condition != kernel.ConditionRunnable {
		return false
	}
	if slices.Contains(consumer.Validates, producer.ID) {
		return plannedDependencyReady(true, state, invocation, found)
	}
	if state.Phase == kernel.PhaseCompleted {
		return true
	}
	return policy.candidateOverlapEnabled() && consumer.Purpose == kernel.PurposeImplementation && producer.Purpose == kernel.PurposeImplementation && state.Phase == kernel.PhaseActive && found && invocation.State == kernel.InvocationSucceeded && invocation.OutputDigest != nil && invocation.OutputDigest.Valid() && invocation.ID.Valid() && invocation.LastEventID.Valid() && len(invocation.TerminalEvidenceIDs) > 0
}
