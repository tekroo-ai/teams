package operationalruntime

import (
	"reflect"
	"testing"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func overlapPolicy() ProductionPlanning {
	return ProductionPlanning{CandidateReadiness: &CandidateReadinessPolicy{SchemaVersion: "1.0.0", Mode: "CANDIDATE_READY", PolicyDigest: repeatedDigest('a')}}
}

func TestCandidateOverlapRequiresExplicitPolicyAndNeverReleasesAcceptance(t *testing.T) {
	producer := organization.PlannedTask{ID: candidateTestUUID(701), Purpose: kernel.PurposeImplementation}
	consumer := organization.PlannedTask{ID: candidateTestUUID(702), Purpose: kernel.PurposeImplementation, DependsOn: []kernel.UUIDv7{producer.ID}}
	digest := repeatedDigest('b')
	inv := kernel.WorkInvocation{ID: candidateTestUUID(703), State: kernel.InvocationSucceeded, OutputDigest: &digest, LastEventID: candidateTestUUID(704), TerminalEvidenceIDs: []kernel.UUIDv7{candidateTestUUID(705)}}
	active := kernel.AggregateState{Phase: kernel.PhaseActive, Condition: kernel.ConditionRunnable}
	if candidateDependencyReady(ProductionPlanning{}, consumer, producer, active, inv, true) {
		t.Fatal("default changed accepted dependency semantics")
	}
	if !candidateDependencyReady(overlapPolicy(), consumer, producer, active, inv, true) {
		t.Fatal("candidate did not release downstream coding")
	}
	for _, purpose := range []kernel.WorkPurpose{kernel.PurposePromotion, kernel.PurposeReview, kernel.PurposeValidation} {
		other := consumer
		other.Purpose = purpose
		if candidateDependencyReady(overlapPolicy(), other, producer, active, inv, true) {
			t.Fatalf("candidate released %s without explicit validation target", purpose)
		}
	}
	validator := consumer
	validator.Purpose = kernel.PurposeValidation
	validator.Validates = []kernel.UUIDv7{producer.ID}
	if !candidateDependencyReady(ProductionPlanning{}, validator, producer, active, inv, true) {
		t.Fatal("independent validator lost candidate access")
	}
	for _, bad := range []kernel.WorkInvocation{{State: kernel.InvocationStarted}, {State: kernel.InvocationSucceeded}, {ID: inv.ID, State: kernel.InvocationSucceeded, OutputDigest: &digest}} {
		if candidateDependencyReady(overlapPolicy(), consumer, producer, active, bad, true) {
			t.Fatal("incomplete candidate admitted")
		}
	}
	active.Condition = kernel.ConditionBlocked
	if candidateDependencyReady(overlapPolicy(), consumer, producer, active, inv, true) {
		t.Fatal("blocked candidate released work")
	}
}

func TestProvisionalCompletionWaitsForAcceptedInputsAndRepairInvalidatesLineage(t *testing.T) {
	a, b := candidateTestUUID(711), candidateTestUUID(712)
	digest := repeatedDigest('b')
	inv := kernel.WorkInvocation{ID: candidateTestUUID(713), State: kernel.InvocationSucceeded, OutputDigest: &digest}
	plan := organization.FeaturePlan{Tasks: []organization.PlannedTask{{ID: a, Purpose: kernel.PurposeImplementation}, {ID: b, Purpose: kernel.PurposeImplementation, DependsOn: []kernel.UUIDv7{a}}}}
	states := map[kernel.UUIDv7]kernel.AggregateState{a: {Phase: kernel.PhaseActive, Condition: kernel.ConditionRunnable}}
	if acceptedImplementationDependencies(plan, plan.Tasks[1], states) {
		t.Fatal("provisional input allowed acceptance")
	}
	states[a] = kernel.AggregateState{Phase: kernel.PhaseCompleted, Condition: kernel.ConditionRunnable}
	if !acceptedImplementationDependencies(plan, plan.Tasks[1], states) {
		t.Fatal("accepted input did not release completion")
	}
	receipt := taskWorkspaceReceipt{Components: []taskWorkspaceComponent{{TaskID: a, InvocationID: inv.ID, OutputSHA256: digest}}}
	invocations := map[kernel.UUIDv7]kernel.WorkInvocation{a: inv}
	if _, changed := candidateLineageChanged(receipt, states, invocations); changed {
		t.Fatal("same candidate invalidated")
	}
	inv.ID = candidateTestUUID(714)
	inv.State = kernel.InvocationAuthorized
	inv.OutputDigest = nil
	invocations[a] = inv
	if changed, ok := candidateLineageChanged(receipt, states, invocations); !ok || changed.TaskID != a {
		t.Fatal("repair authorization did not invalidate old consumer")
	}
	states[a] = kernel.AggregateState{Phase: kernel.PhaseCompleted, Condition: kernel.ConditionBlocked}
	if acceptedImplementationDependencies(plan, plan.Tasks[1], states) {
		t.Fatal("blocked completed input released promotion")
	}
}

func TestFocusedGatesNeverReplaceWholeFeatureOrPromotionGates(t *testing.T) {
	a, b, v, all, p := candidateTestUUID(721), candidateTestUUID(722), candidateTestUUID(723), candidateTestUUID(724), candidateTestUUID(725)
	plan := organization.FeaturePlan{Tasks: []organization.PlannedTask{
		{ID: a, Purpose: kernel.PurposeImplementation},
		{ID: b, Purpose: kernel.PurposeImplementation, DependsOn: []kernel.UUIDv7{a}},
		{ID: v, Purpose: kernel.PurposeValidation, DependsOn: []kernel.UUIDv7{a}, Validates: []kernel.UUIDv7{a}},
		{ID: all, Purpose: kernel.PurposeValidation, DependsOn: []kernel.UUIDv7{a, b}, Validates: []kernel.UUIDv7{a}},
		{ID: p, Purpose: kernel.PurposePromotion, DependsOn: []kernel.UUIDv7{a, b, v, all}},
	}}
	policy := ProductionPlanning{RequiredGateIDs: []string{"full"}, TaskGateIDs: []string{"compile"}}
	for _, task := range plan.Tasks {
		want := []string{"compile"}
		if task.ID == all || task.ID == p {
			want = []string{"full"}
		}
		if got := policy.gateIDs(&plan, task); !reflect.DeepEqual(got, want) {
			t.Fatalf("task %s: %v want %v", task.ID, got, want)
		}
	}
	if got := policy.gateIDs(nil, plan.Tasks[0]); !reflect.DeepEqual(got, []string{"full"}) {
		t.Fatal("unknown plan weakened required gates")
	}
	for _, ids := range [][]string{{"missing"}, {"compile", "compile"}} {
		policy.TaskGateIDs = ids
		if policy.validateEfficiencyPolicy(map[string]struct{}{"compile": {}}) == nil {
			t.Fatal("invalid gate policy accepted")
		}
	}
}

func TestBudgetAccountingDoesNotForceUnrelatedTasksIntoASerialNode(t *testing.T) {
	tasks := make([]architectureTaskResult, 8)
	for i := range tasks {
		tasks[i].Purpose = kernel.PurposeImplementation
		tasks[i].Risk = organization.RiskLow
	}
	if got := requiredMaterializedTaskCount(tasks); got != 10 {
		t.Fatalf("independent tasks=%d want 10", got)
	}
	for i := 1; i < len(tasks); i++ {
		tasks[i].DependsOn = []uint32{uint32(i - 1)}
	}
	if got := requiredMaterializedTaskCount(tasks); got != 17 {
		t.Fatalf("handoff chain=%d want 17", got)
	}
	// The explicit operator limit is unchanged. A coherent chain over 16 needs
	// a budget decision, not an invented merge of unrelated responsibilities.
	if got := maximumBudgetedImplementationTasks(16); got != 14 {
		t.Fatalf("upper bound=%d", got)
	}
}
