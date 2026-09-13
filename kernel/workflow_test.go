package kernel

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"
)

func workflowTestID(value int) UUIDv7 {
	return UUIDv7(fmt.Sprintf("00000000-0000-7000-8000-%012d", value))
}

func workflowTestDefinition(t *testing.T) WorkflowDefinition {
	t.Helper()
	definition := WorkflowDefinition{
		SchemaVersion: WorkflowDefinitionSchemaVersion,
		Name:          "document-flow",
		Version:       "1.0.0",
		TriggerTypes:  []string{"document.requested"},
		Stages: []WorkflowStageDefinition{
			{StageID: "intake", DependsOn: []string{}, InputSchema: "input/v1", OutputSchema: "brief/v1", RequiredCapabilities: []string{"understand-request"}, PreferredFQRNs: []RoleFQRN{}, Purpose: WorkflowPurposeHandoff, Risk: WorkflowRiskLow, ConcurrencyGroup: "intake", MaximumParallelism: 1, AttemptLimit: 2, AllowedOutgoingPurposes: []string{"HANDOFF"}, TargetSelection: WorkflowTargetCapability, ValidationPolicy: WorkflowValidationDeterministic},
			{StageID: "produce-a", DependsOn: []string{"intake"}, InputSchema: "brief/v1", OutputSchema: "draft/v1", RequiredCapabilities: []string{"produce-a"}, PreferredFQRNs: []RoleFQRN{}, Purpose: WorkflowPurposeImplementation, Risk: WorkflowRiskModerate, ConcurrencyGroup: "production", MaximumParallelism: 2, AttemptLimit: 2, AllowedOutgoingPurposes: []string{"EVIDENCE"}, TargetSelection: WorkflowTargetCapability, ValidationPolicy: WorkflowValidationRiskSelected},
			{StageID: "produce-b", DependsOn: []string{"intake"}, InputSchema: "brief/v1", OutputSchema: "draft/v1", RequiredCapabilities: []string{"produce-b"}, PreferredFQRNs: []RoleFQRN{}, Purpose: WorkflowPurposeImplementation, Risk: WorkflowRiskModerate, ConcurrencyGroup: "production", MaximumParallelism: 2, AttemptLimit: 2, AllowedOutgoingPurposes: []string{"EVIDENCE"}, TargetSelection: WorkflowTargetCapability, ValidationPolicy: WorkflowValidationRiskSelected},
			{StageID: "publish", DependsOn: []string{"produce-a", "produce-b"}, InputSchema: "draft-set/v1", OutputSchema: "publication/v1", RequiredCapabilities: []string{"publish"}, PreferredFQRNs: []RoleFQRN{}, Purpose: WorkflowPurposePromotion, Risk: WorkflowRiskLow, ConcurrencyGroup: "publish", MaximumParallelism: 1, AttemptLimit: 1, AllowedOutgoingPurposes: []string{"NOTIFICATION"}, TargetSelection: WorkflowTargetCapability, ValidationPolicy: WorkflowValidationDeterministic},
		},
		RootBudgets:     WorkflowBudgetLimits{MaximumModelInvocations: 8, MaximumHops: 8, MaximumAttempts: 8, MaximumTokens: 100_000, MaximumElapsedSeconds: 3600},
		ProjectionRules: []string{"STATUS"},
	}
	digest, err := definition.CalculatedDigest()
	if err != nil {
		t.Fatal(err)
	}
	definition.ContentDigest = digest
	return definition
}

func workflowTestInstance(t *testing.T) (WorkflowDefinition, WorkflowInstance) {
	t.Helper()
	definition := workflowTestDefinition(t)
	instance, err := NewWorkflowInstance(
		definition,
		workflowTestID(1),
		AggregateRef{Kind: AggregateTask, ID: workflowTestID(2)},
		workflowTestID(3),
		map[string]UUIDv7{"intake": workflowTestID(10), "produce-a": workflowTestID(11), "produce-b": workflowTestID(12), "publish": workflowTestID(13)},
	)
	if err != nil {
		t.Fatal(err)
	}
	return definition, instance
}

func TestWorkflowDefinitionRejectsCycleAndDigestMismatch(t *testing.T) {
	definition := workflowTestDefinition(t)
	if err := definition.Validate(); err != nil {
		t.Fatalf("valid definition rejected: %v", err)
	}
	definition.Stages[0].DependsOn = []string{"publish"}
	digest, err := definition.CalculatedDigest()
	if err != nil {
		t.Fatal(err)
	}
	definition.ContentDigest = digest
	if err := definition.Validate(); err == nil {
		t.Fatal("cyclic definition accepted")
	}
	definition = workflowTestDefinition(t)
	definition.Name = "different"
	if err := definition.Validate(); err == nil {
		t.Fatal("digest mismatch accepted")
	}
}

func TestWorkflowFoldUnlocksParallelNodesAndPreservesCheckpoint(t *testing.T) {
	definition, initial := workflowTestInstance(t)
	now := time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC)
	actor := ActorFQN("demo::worker-1")
	transitions := []WorkflowTransition{
		{Revision: 2, EventID: workflowTestID(20), Kind: WorkflowTransitionAdmit, NodeID: workflowTestID(10), ActorFQN: actor, InvocationID: workflowTestID(30), RecordedAt: now},
		{Revision: 3, EventID: workflowTestID(21), Kind: WorkflowTransitionStart, NodeID: workflowTestID(10), InvocationID: workflowTestID(30), RecordedAt: now},
		{Revision: 4, EventID: workflowTestID(22), Kind: WorkflowTransitionCheckpoint, NodeID: workflowTestID(10), CheckpointID: workflowTestID(40), RecordedAt: now},
		{Revision: 5, EventID: workflowTestID(23), Kind: WorkflowTransitionComplete, NodeID: workflowTestID(10), InvocationID: workflowTestID(30), EvidenceIDs: []UUIDv7{workflowTestID(50)}, ProgressDigest: Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), RecordedAt: now},
	}
	state, err := FoldWorkflowInstance(definition, initial, transitions)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Revision != 1 || initial.Nodes[0].State != WorkflowNodeReady || initial.Nodes[0].CheckpointID != nil {
		t.Fatal("fold mutated the durable starting checkpoint")
	}
	ready := state.ReadyNodes()
	if len(ready) != 2 || ready[0].StageID != "produce-a" || ready[1].StageID != "produce-b" {
		t.Fatalf("dependency-ready parallel frontier mismatch: %+v", ready)
	}
	if state.Nodes[0].CheckpointID == nil || *state.Nodes[0].CheckpointID != workflowTestID(40) {
		t.Fatal("checkpoint was not retained")
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var restored WorkflowInstance
	if err := json.Unmarshal(raw, &restored); err != nil || restored.Validate(definition) != nil || restored.Revision != state.Revision {
		t.Fatalf("durable state round trip failed: %v", err)
	}
	replayed, err := FoldWorkflowInstance(definition, initial, transitions)
	if err != nil || replayed.Revision != state.Revision || len(replayed.ReadyNodes()) != 2 {
		t.Fatalf("deterministic replay failed: %v", err)
	}
}

func TestWorkflowExpandsOneStageIntoAConcreteParallelDAG(t *testing.T) {
	definition, initial := workflowTestInstance(t)
	now := time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC)
	prepared, err := FoldWorkflowInstance(definition, initial, []WorkflowTransition{
		{Revision: 2, EventID: workflowTestID(200), Kind: WorkflowTransitionAdmit, NodeID: workflowTestID(10), ActorFQN: "demo::worker-1", InvocationID: workflowTestID(201), RecordedAt: now},
		{Revision: 3, EventID: workflowTestID(202), Kind: WorkflowTransitionStart, NodeID: workflowTestID(10), InvocationID: workflowTestID(201), RecordedAt: now},
		{Revision: 4, EventID: workflowTestID(203), Kind: WorkflowTransitionComplete, NodeID: workflowTestID(10), InvocationID: workflowTestID(201), EvidenceIDs: []UUIDv7{workflowTestID(204)}, ProgressDigest: Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), RecordedAt: now},
	})
	if err != nil {
		t.Fatal(err)
	}
	expanded, err := ExpandWorkflowNode(definition, prepared, workflowTestID(11), workflowTestID(205), []WorkflowNodeSpec{
		{NodeID: workflowTestID(210), PredecessorNodeIDs: []UUIDv7{workflowTestID(10)}, InputEvidenceIDs: []UUIDv7{workflowTestID(204)}},
		{NodeID: workflowTestID(211), PredecessorNodeIDs: []UUIDv7{workflowTestID(10)}, InputEvidenceIDs: []UUIDv7{workflowTestID(204)}},
		{NodeID: workflowTestID(212), PredecessorNodeIDs: []UUIDv7{workflowTestID(210)}, InputEvidenceIDs: []UUIDv7{workflowTestID(204)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if expanded.Revision != 5 || len(expanded.Nodes) != 6 {
		t.Fatalf("expanded workflow=%+v", expanded)
	}
	ready := expanded.ReadyNodes()
	if len(ready) != 3 || ready[0].NodeID != workflowTestID(210) || ready[1].NodeID != workflowTestID(211) || ready[2].StageID != "produce-b" {
		t.Fatalf("ready frontier=%+v", ready)
	}
	admissible := expanded.AdmissibleReadyNodes(definition)
	if len(admissible) != 2 {
		t.Fatalf("admissible frontier=%+v", admissible)
	}
	publish := workflowNodeByID(&expanded, workflowTestID(13))
	if publish == nil || !slices.Equal(publish.PredecessorNodeIDs, []UUIDv7{workflowTestID(210), workflowTestID(211), workflowTestID(212), workflowTestID(12)}) {
		t.Fatalf("downstream join=%+v", publish)
	}
}

func TestWorkflowParallelFrontierHonorsDeclaredCapacity(t *testing.T) {
	definition, initial := workflowTestInstance(t)
	now := time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC)
	state, err := FoldWorkflowInstance(definition, initial, []WorkflowTransition{
		{Revision: 2, EventID: workflowTestID(220), Kind: WorkflowTransitionAdmit, NodeID: workflowTestID(10), ActorFQN: "demo::worker-1", InvocationID: workflowTestID(221), RecordedAt: now},
		{Revision: 3, EventID: workflowTestID(222), Kind: WorkflowTransitionStart, NodeID: workflowTestID(10), InvocationID: workflowTestID(221), RecordedAt: now},
		{Revision: 4, EventID: workflowTestID(223), Kind: WorkflowTransitionComplete, NodeID: workflowTestID(10), InvocationID: workflowTestID(221), EvidenceIDs: []UUIDv7{}, ProgressDigest: Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), RecordedAt: now},
	})
	if err != nil {
		t.Fatal(err)
	}
	if nodes := state.AdmissibleReadyNodes(definition); len(nodes) != 2 {
		t.Fatalf("parallel frontier=%+v", nodes)
	}
	state, err = FoldWorkflowInstance(definition, state, []WorkflowTransition{{Revision: 5, EventID: workflowTestID(224), Kind: WorkflowTransitionAdmit, NodeID: workflowTestID(11), ActorFQN: "demo::worker-1", InvocationID: workflowTestID(225), RecordedAt: now}})
	if err != nil {
		t.Fatal(err)
	}
	if nodes := state.AdmissibleReadyNodes(definition); len(nodes) != 1 || nodes[0].NodeID != workflowTestID(12) {
		t.Fatalf("capacity-adjusted frontier=%+v", nodes)
	}
}

func TestWorkflowBlockResumeFailureAndCancellation(t *testing.T) {
	definition, initial := workflowTestInstance(t)
	now := time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC)
	blocked, err := FoldWorkflowInstance(definition, initial, []WorkflowTransition{{Revision: 2, EventID: workflowTestID(60), Kind: WorkflowTransitionBlock, NodeID: workflowTestID(10), RecordedAt: now}})
	if err != nil || blocked.State != WorkflowBlocked {
		t.Fatalf("block failed: %v", err)
	}
	resumed, err := FoldWorkflowInstance(definition, blocked, []WorkflowTransition{{Revision: 3, EventID: workflowTestID(61), Kind: WorkflowTransitionReady, NodeID: workflowTestID(10), RecordedAt: now}})
	if err != nil || resumed.State != WorkflowActive || len(resumed.ReadyNodes()) != 1 {
		t.Fatalf("resume failed: %v", err)
	}
	actor := ActorFQN("demo::worker-1")
	admitted, err := FoldWorkflowInstance(definition, resumed, []WorkflowTransition{
		{Revision: 4, EventID: workflowTestID(62), Kind: WorkflowTransitionAdmit, NodeID: workflowTestID(10), ActorFQN: actor, InvocationID: workflowTestID(70), RecordedAt: now},
	})
	if err != nil {
		t.Fatal(err)
	}
	paused, err := FoldWorkflowInstance(definition, admitted, []WorkflowTransition{{Revision: 5, EventID: workflowTestID(63), Kind: WorkflowTransitionBlock, NodeID: workflowTestID(10), RecordedAt: now}})
	if err != nil || paused.Nodes[0].BlockedFrom == nil || *paused.Nodes[0].BlockedFrom != WorkflowNodeAdmitted {
		t.Fatalf("active block did not retain prior state: %v", err)
	}
	continued, err := FoldWorkflowInstance(definition, paused, []WorkflowTransition{{Revision: 6, EventID: workflowTestID(64), Kind: WorkflowTransitionReady, NodeID: workflowTestID(10), RecordedAt: now}})
	if err != nil || continued.Nodes[0].State != WorkflowNodeAdmitted {
		t.Fatalf("active resume did not restore prior state: %v", err)
	}
	failed, err := FoldWorkflowInstance(definition, continued, []WorkflowTransition{
		{Revision: 7, EventID: workflowTestID(65), Kind: WorkflowTransitionStart, NodeID: workflowTestID(10), InvocationID: workflowTestID(70), RecordedAt: now},
		{Revision: 8, EventID: workflowTestID(66), Kind: WorkflowTransitionFail, NodeID: workflowTestID(10), InvocationID: workflowTestID(70), ProgressDigest: Digest("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"), FailureClassification: WorkflowFailureCorrectableWork, RecordedAt: now},
	})
	if err != nil || failed.State != WorkflowActive || failed.Nodes[0].State != WorkflowNodeFailed {
		t.Fatalf("correctable failure failed: %v", err)
	}
	cancelled, err := FoldWorkflowInstance(definition, failed, []WorkflowTransition{{Revision: 9, EventID: workflowTestID(67), Kind: WorkflowTransitionCancel, RecordedAt: now}})
	if err != nil || cancelled.State != WorkflowCancelled {
		t.Fatalf("cancellation failed: %v", err)
	}
	if _, err := FoldWorkflowInstance(definition, cancelled, []WorkflowTransition{{Revision: 10, EventID: workflowTestID(68), Kind: WorkflowTransitionCancel, RecordedAt: now}}); err == nil {
		t.Fatal("transition after terminal state accepted")
	}
}

func TestWorkflowFoldRejectsRevisionGapAndWrongInvocation(t *testing.T) {
	definition, initial := workflowTestInstance(t)
	now := time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC)
	if _, err := FoldWorkflowInstance(definition, initial, []WorkflowTransition{{Revision: 3, EventID: workflowTestID(80), Kind: WorkflowTransitionBlock, NodeID: workflowTestID(10), RecordedAt: now}}); err == nil {
		t.Fatal("revision gap accepted")
	}
	if _, err := FoldWorkflowInstance(definition, initial, []WorkflowTransition{{Revision: 2, EventID: workflowTestID(81), Kind: WorkflowTransitionStart, NodeID: workflowTestID(10), InvocationID: workflowTestID(90), RecordedAt: now}}); err == nil {
		t.Fatal("start without admission accepted")
	}
}

func TestWorkflowCompletesOnlyAfterJoinAndTerminalFailureStopsIt(t *testing.T) {
	definition, initial := workflowTestInstance(t)
	now := time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC)
	actor := ActorFQN("demo::worker-1")
	digest := Digest("cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")
	revision := uint64(1)
	transitions := make([]WorkflowTransition, 0, 12)
	complete := func(node, invocation UUIDv7) {
		revision++
		transitions = append(transitions, WorkflowTransition{Revision: revision, EventID: workflowTestID(100 + int(revision)), Kind: WorkflowTransitionAdmit, NodeID: node, ActorFQN: actor, InvocationID: invocation, RecordedAt: now})
		revision++
		transitions = append(transitions, WorkflowTransition{Revision: revision, EventID: workflowTestID(100 + int(revision)), Kind: WorkflowTransitionStart, NodeID: node, InvocationID: invocation, RecordedAt: now})
		revision++
		transitions = append(transitions, WorkflowTransition{Revision: revision, EventID: workflowTestID(100 + int(revision)), Kind: WorkflowTransitionComplete, NodeID: node, InvocationID: invocation, EvidenceIDs: []UUIDv7{}, ProgressDigest: digest, RecordedAt: now})
	}
	complete(workflowTestID(10), workflowTestID(210))
	complete(workflowTestID(11), workflowTestID(211))
	complete(workflowTestID(12), workflowTestID(212))
	complete(workflowTestID(13), workflowTestID(213))
	state, err := FoldWorkflowInstance(definition, initial, transitions)
	if err != nil || state.State != WorkflowCompleted || len(state.ReadyNodes()) != 0 {
		t.Fatalf("workflow did not complete at the join: %v", err)
	}

	_, fresh := workflowTestInstance(t)
	failed, err := FoldWorkflowInstance(definition, fresh, []WorkflowTransition{
		{Revision: 2, EventID: workflowTestID(220), Kind: WorkflowTransitionAdmit, NodeID: workflowTestID(10), ActorFQN: actor, InvocationID: workflowTestID(221), RecordedAt: now},
		{Revision: 3, EventID: workflowTestID(222), Kind: WorkflowTransitionStart, NodeID: workflowTestID(10), InvocationID: workflowTestID(221), RecordedAt: now},
		{Revision: 4, EventID: workflowTestID(223), Kind: WorkflowTransitionFail, NodeID: workflowTestID(10), InvocationID: workflowTestID(221), ProgressDigest: digest, FailureClassification: WorkflowFailureTerminalPolicy, RecordedAt: now},
	})
	if err != nil || failed.State != WorkflowFailed {
		t.Fatalf("terminal failure did not stop workflow: %v", err)
	}
}
