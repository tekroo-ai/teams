package organization

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

func TestMemoryWorkflowAdmissionAtomicallyResolvesMessageAndAuthorizesOnce(t *testing.T) {
	definition := organizationWorkflowDefinition(t, "publication")
	workflowID := testUUID(8)
	message := testMessage(20)
	message.Work.WorkflowInstanceID = &workflowID
	message.Work.WorkflowStageID = "perform"
	message.Work.DAGNodeID = testUUID(9)
	message.Flow.StepID = message.Work.DAGNodeID
	message.ID = testUUID(10)
	message.CorrelationID = testUUID(11)
	message.Flow.ThreadID = message.CorrelationID
	message.Flow.BudgetAccountID = testUUID(12)
	message.Flow.ProgressDigest = testDigest('f')
	nodes := map[string]kernel.UUIDv7{"perform": message.Work.DAGNodeID}
	instance, err := kernel.NewWorkflowInstance(definition, workflowID, kernel.AggregateRef{Kind: kernel.AggregateStory, ID: testUUID(13)}, message.Flow.BudgetAccountID, nodes)
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryOrganizationalMessageStore()
	if err := store.AppendMessage(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateWorkflowInstance(context.Background(), definition, instance); err != nil {
		t.Fatal(err)
	}
	now := message.CreatedAt.Add(time.Second)
	proposal, err := WorkProposalFromMessage(message, testUUID(14), testExecution(15, 1), []kernel.UUIDv7{testUUID(16)}, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	request := WorkflowAdmissionRequest{
		Message: message, Definition: definition, Proposal: proposal,
		Facts:           kernel.WorkflowAdmissionFacts{ActorEligible: true, BudgetAvailable: true, CausationValid: true},
		RecordedEventID: testUUID(17), AuthorizedInvocationID: testUUID(18), IntentID: testUUID(19), RecordedAt: now,
	}
	result, replayed, err := store.AdmitWorkflowMessage(context.Background(), request)
	if err != nil || replayed || result.Outcome != kernel.WorkAdmitted || result.AuthorizedInvocationID == nil {
		t.Fatalf("admission result=%+v replayed=%t err=%v", result, replayed, err)
	}
	claim, found, err := store.ReadMessage(context.Background(), message.ID)
	if err != nil || !found || claim.State != MessageResolved || claim.Resolution != string(kernel.AdmissionAccepted) {
		t.Fatalf("message claim=%+v found=%t err=%v", claim, found, err)
	}
	current, found, err := store.ReadWorkflowInstance(context.Background(), workflowID)
	if err != nil || !found || current.Nodes[0].State != kernel.WorkflowNodeAdmitted || current.Nodes[0].InvocationID == nil || *current.Nodes[0].InvocationID != request.AuthorizedInvocationID {
		t.Fatalf("workflow=%+v found=%t err=%v", current, found, err)
	}
	if store.WorkflowIntentCount() != 1 {
		t.Fatalf("intent count=%d", store.WorkflowIntentCount())
	}
	replayedResult, replayed, err := store.AdmitWorkflowMessage(context.Background(), request)
	if err != nil || !replayed || replayedResult != result || store.WorkflowIntentCount() != 1 {
		t.Fatalf("replay result=%+v replayed=%t intents=%d err=%v", replayedResult, replayed, store.WorkflowIntentCount(), err)
	}
	conflict := request
	conflict.Proposal.ProposalID = testUUID(20)
	if _, _, err := store.AdmitWorkflowMessage(context.Background(), conflict); !errors.Is(err, ErrOrganizationalMessageConflict) {
		t.Fatalf("conflicting replay error=%v", err)
	}
}

func TestMemoryWorkflowRejectionCreatesNoInvocationIntent(t *testing.T) {
	definition := organizationWorkflowDefinition(t, "publication")
	workflowID := testUUID(8)
	message := testMessage(30)
	message.Work.WorkflowInstanceID = &workflowID
	message.Work.WorkflowStageID = "perform"
	message.Work.DAGNodeID = testUUID(9)
	message.Flow.StepID = message.Work.DAGNodeID
	message.ID = testUUID(10)
	message.CorrelationID = testUUID(11)
	message.Flow.ThreadID = message.CorrelationID
	message.Flow.BudgetAccountID = testUUID(12)
	message.Flow.ProgressDigest = testDigest('e')
	instance, err := kernel.NewWorkflowInstance(definition, workflowID, kernel.AggregateRef{Kind: kernel.AggregateStory, ID: testUUID(13)}, message.Flow.BudgetAccountID, map[string]kernel.UUIDv7{"perform": message.Work.DAGNodeID})
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryOrganizationalMessageStore()
	if err := store.AppendMessage(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateWorkflowInstance(context.Background(), definition, instance); err != nil {
		t.Fatal(err)
	}
	now := message.CreatedAt.Add(time.Second)
	proposal, err := WorkProposalFromMessage(message, testUUID(14), testExecution(15, 1), []kernel.UUIDv7{testUUID(16)}, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	result, _, err := store.AdmitWorkflowMessage(context.Background(), WorkflowAdmissionRequest{
		Message: message, Definition: definition, Proposal: proposal,
		Facts:           kernel.WorkflowAdmissionFacts{ActorEligible: true, CausationValid: true},
		RecordedEventID: testUUID(17), AuthorizedInvocationID: testUUID(18), IntentID: testUUID(19), RecordedAt: now,
	})
	if err != nil || result.Outcome != kernel.WorkRejected || result.ReasonCode != kernel.AdmissionBudgetExhausted || result.AuthorizedInvocationID != nil || store.WorkflowIntentCount() != 0 {
		t.Fatalf("rejection result=%+v intents=%d err=%v", result, store.WorkflowIntentCount(), err)
	}
}

func TestMemoryWorkflowRootCannotMintAReplacementBudget(t *testing.T) {
	definition := organizationWorkflowDefinition(t, "publication")
	root := kernel.AggregateRef{Kind: kernel.AggregateStory, ID: testUUID(1)}
	first, err := kernel.NewWorkflowInstance(definition, testUUID(2), root, testUUID(3), map[string]kernel.UUIDv7{"perform": testUUID(4)})
	if err != nil {
		t.Fatal(err)
	}
	second, err := kernel.NewWorkflowInstance(definition, testUUID(5), root, testUUID(6), map[string]kernel.UUIDv7{"perform": testUUID(7)})
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryOrganizationalMessageStore()
	if err := store.CreateWorkflowInstance(context.Background(), definition, first); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateWorkflowInstance(context.Background(), definition, second); !errors.Is(err, ErrOrganizationalMessageConflict) {
		t.Fatalf("replacement workflow budget accepted: %v", err)
	}
}
