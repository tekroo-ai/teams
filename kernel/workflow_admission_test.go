package kernel

import (
	"testing"
	"time"
)

func workflowAdmissionProposal(instance WorkflowInstance, node WorkflowNode, now time.Time) WorkProposal {
	return WorkProposal{
		SchemaVersion: WorkProposalSchemaVersion, ProposalID: workflowTestID(300), MessageID: workflowTestID(301),
		WorkflowInstanceID: instance.InstanceID, StageID: node.StageID, NodeID: node.NodeID,
		ActorFQN: "demo::worker-1", Execution: ExecutionTuple{ExecutionID: workflowTestID(302), FencingEpoch: 1},
		BudgetAccountID: instance.BudgetAccountID, CausationEventIDs: []UUIDv7{workflowTestID(303)},
		InputEvidenceIDs: []UUIDv7{}, ProposedAt: now,
	}
}

func TestWorkflowAdmissionAcceptsOneExactReadyNode(t *testing.T) {
	definition, instance := workflowTestInstance(t)
	now := time.Date(2026, 9, 13, 9, 0, 0, 0, time.UTC)
	proposal := workflowAdmissionProposal(instance, instance.Nodes[0], now)
	evaluation, err := EvaluateWorkflowAdmission(definition, instance, proposal, WorkflowAdmissionFacts{ActorEligible: true, BudgetAvailable: true, CausationValid: true}, workflowTestID(304), workflowTestID(305), now)
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.Result.Outcome != WorkAdmitted || evaluation.Result.AuthorizedInvocationID == nil || *evaluation.Result.AuthorizedInvocationID != workflowTestID(305) {
		t.Fatalf("admission result mismatch: %+v", evaluation.Result)
	}
	if evaluation.NextInstance.Revision != 2 || evaluation.NextInstance.Nodes[0].State != WorkflowNodeAdmitted || evaluation.NextInstance.Nodes[0].InvocationID == nil || *evaluation.NextInstance.Nodes[0].InvocationID != workflowTestID(305) {
		t.Fatalf("admission state mismatch: %+v", evaluation.NextInstance)
	}
	if len(instance.ProposedMessageIDs) != 0 || instance.Nodes[0].State != WorkflowNodeReady {
		t.Fatal("admission mutated the supplied snapshot")
	}
}

func TestWorkflowAdmissionRejectsWithoutAuthorizingInvocation(t *testing.T) {
	definition, instance := workflowTestInstance(t)
	now := time.Date(2026, 9, 13, 9, 0, 0, 0, time.UTC)
	base := workflowAdmissionProposal(instance, instance.Nodes[0], now)
	cases := []struct {
		name     string
		mutate   func(*WorkflowInstance, *WorkProposal)
		facts    WorkflowAdmissionFacts
		expected WorkAdmissionReason
	}{
		{name: "invalid causation", facts: WorkflowAdmissionFacts{ActorEligible: true, BudgetAvailable: true}, expected: AdmissionCausationInvalid},
		{name: "ineligible actor", facts: WorkflowAdmissionFacts{BudgetAvailable: true, CausationValid: true}, expected: AdmissionActorIneligible},
		{name: "exhausted budget", facts: WorkflowAdmissionFacts{ActorEligible: true, CausationValid: true}, expected: AdmissionBudgetExhausted},
		{name: "predecessor incomplete", mutate: func(state *WorkflowInstance, proposal *WorkProposal) {
			*proposal = workflowAdmissionProposal(*state, state.Nodes[1], now)
		}, facts: WorkflowAdmissionFacts{ActorEligible: true, BudgetAvailable: true, CausationValid: true}, expected: AdmissionPredecessorIncomplete},
		{name: "unknown node", mutate: func(_ *WorkflowInstance, proposal *WorkProposal) { proposal.NodeID = workflowTestID(399) }, facts: WorkflowAdmissionFacts{ActorEligible: true, BudgetAvailable: true, CausationValid: true}, expected: AdmissionUndeclaredTransition},
		{name: "duplicate message", mutate: func(state *WorkflowInstance, proposal *WorkProposal) {
			state.ProposedMessageIDs = []UUIDv7{proposal.MessageID}
		}, facts: WorkflowAdmissionFacts{ActorEligible: true, BudgetAvailable: true, CausationValid: true}, expected: AdmissionDuplicateNode},
		{name: "terminal workflow", mutate: func(state *WorkflowInstance, _ *WorkProposal) {
			state.State = WorkflowFailed
			state.Nodes[0].State = WorkflowNodeFailed
			digest := Digest("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd")
			state.Nodes[0].ProgressDigest = &digest
			state.Nodes[0].FailureClassification = WorkflowFailureTerminalPolicy
			actor := ActorFQN("demo::worker-1")
			invocation := workflowTestID(398)
			state.Nodes[0].ActorFQN = &actor
			state.Nodes[0].InvocationID = &invocation
			state.Nodes[0].Attempt = 1
		}, facts: WorkflowAdmissionFacts{ActorEligible: true, BudgetAvailable: true, CausationValid: true}, expected: AdmissionWorkflowTerminal},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			state := instance.Clone()
			proposal := base
			if testCase.mutate != nil {
				testCase.mutate(&state, &proposal)
			}
			evaluation, err := EvaluateWorkflowAdmission(definition, state, proposal, testCase.facts, workflowTestID(310), workflowTestID(311), now)
			if err != nil {
				t.Fatal(err)
			}
			if evaluation.Result.Outcome != WorkRejected || evaluation.Result.ReasonCode != testCase.expected || evaluation.Result.AuthorizedInvocationID != nil {
				t.Fatalf("unexpected rejection: %+v", evaluation.Result)
			}
			if evaluation.NextInstance.Nodes[0].State != state.Nodes[0].State {
				t.Fatal("rejection changed node state")
			}
		})
	}
}

func TestRepairSuccessorRequiresChangedConditionEvidence(t *testing.T) {
	now := time.Date(2026, 9, 13, 9, 30, 0, 0, time.UTC)
	definition := WorkflowDefinition{
		SchemaVersion: WorkflowDefinitionSchemaVersion, Name: "repair-flow", Version: "1.0.0", TriggerTypes: []string{"work.requested"},
		Stages: []WorkflowStageDefinition{
			{StageID: "work", DependsOn: []string{}, InputSchema: "request/v1", OutputSchema: "result/v1", RequiredCapabilities: []string{"work"}, PreferredFQRNs: []RoleFQRN{}, Purpose: WorkflowPurposeImplementation, Risk: WorkflowRiskModerate, ConcurrencyGroup: "work", MaximumParallelism: 1, AttemptLimit: 1, AllowedOutgoingPurposes: []string{}, TargetSelection: WorkflowTargetCapability, ValidationPolicy: WorkflowValidationRiskSelected},
			{StageID: "repair", DependsOn: []string{"work"}, InputSchema: "failure/v1", OutputSchema: "repair/v1", RequiredCapabilities: []string{"repair"}, PreferredFQRNs: []RoleFQRN{}, Purpose: WorkflowPurposeRepair, Risk: WorkflowRiskModerate, ConcurrencyGroup: "repair", MaximumParallelism: 1, AttemptLimit: 1, AllowedOutgoingPurposes: []string{}, TargetSelection: WorkflowTargetCapability, ValidationPolicy: WorkflowValidationRiskSelected},
		},
		RootBudgets: WorkflowBudgetLimits{MaximumModelInvocations: 2, MaximumHops: 4, MaximumAttempts: 2}, ProjectionRules: []string{},
	}
	digest, err := definition.CalculatedDigest()
	if err != nil {
		t.Fatal(err)
	}
	definition.ContentDigest = digest
	instance, err := NewWorkflowInstance(definition, workflowTestID(400), AggregateRef{Kind: AggregateTask, ID: workflowTestID(401)}, workflowTestID(402), map[string]UUIDv7{"work": workflowTestID(403), "repair": workflowTestID(404)})
	if err != nil {
		t.Fatal(err)
	}
	failed, err := FoldWorkflowInstance(definition, instance, []WorkflowTransition{
		{Revision: 2, EventID: workflowTestID(405), Kind: WorkflowTransitionAdmit, NodeID: workflowTestID(403), ActorFQN: "demo::worker-1", InvocationID: workflowTestID(406), RecordedAt: now},
		{Revision: 3, EventID: workflowTestID(407), Kind: WorkflowTransitionStart, NodeID: workflowTestID(403), InvocationID: workflowTestID(406), RecordedAt: now},
		{Revision: 4, EventID: workflowTestID(408), Kind: WorkflowTransitionFail, NodeID: workflowTestID(403), InvocationID: workflowTestID(406), ProgressDigest: Digest("eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"), FailureClassification: WorkflowFailureCorrectableWork, RecordedAt: now},
	})
	if err != nil || len(failed.ReadyNodes()) != 1 || failed.ReadyNodes()[0].StageID != "repair" {
		t.Fatalf("repair successor not readied: %+v err=%v", failed.ReadyNodes(), err)
	}
	proposal := workflowAdmissionProposal(failed, failed.Nodes[1], now)
	proposal.InputEvidenceIDs = []UUIDv7{workflowTestID(409)}
	evaluation, err := EvaluateWorkflowAdmission(definition, failed, proposal, WorkflowAdmissionFacts{ActorEligible: true, BudgetAvailable: true, CausationValid: true}, workflowTestID(410), workflowTestID(411), now)
	if err != nil || evaluation.Result.ReasonCode != AdmissionNoProgress || evaluation.Result.AuthorizedInvocationID != nil {
		t.Fatalf("unchanged repair admission=%+v err=%v", evaluation.Result, err)
	}
	proposal.MessageID = workflowTestID(412)
	evaluation, err = EvaluateWorkflowAdmission(definition, failed, proposal, WorkflowAdmissionFacts{ActorEligible: true, BudgetAvailable: true, CausationValid: true, ChangedConditionProven: true}, workflowTestID(413), workflowTestID(414), now)
	if err != nil || evaluation.Result.Outcome != WorkAdmitted {
		t.Fatalf("changed repair rejected=%+v err=%v", evaluation.Result, err)
	}
}
