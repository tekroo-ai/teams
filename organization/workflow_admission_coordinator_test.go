package organization

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/fake"
	"github.com/tekroo-ai/teams/kernel"
)

func TestNonSoftwareRoleAndWorkflowLoadThroughPublicConfiguration(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(workingDirectory)
	publicRaw, err := os.ReadFile(filepath.Join(root, "config", "editorial-team", "publisher.pub"))
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(publicRaw)))
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		t.Fatalf("public key err=%v", err)
	}
	team, err := LoadTeamManifest(filepath.Join(root, "config", "editorial-team", "team.example.json"), kernel.Digest("1a714f2719932aa422eaec72cd6bfe74d206ddd1e27a2d83a9cca3b42dbc9b1d"), map[string]ed25519.PublicKey{"tekroo-example-editorial-20260913": ed25519.PublicKey(publicKey)})
	if err != nil {
		t.Fatal(err)
	}
	definition, err := LoadWorkflowDefinition(filepath.Join(root, "config", "workflows", "editorial-publication.v1.json"), kernel.Digest("00e125b5b442f54ac1525f627ae5dab6ef63ea5fc1af2183a473239578d625b6"))
	if err != nil {
		t.Fatal(err)
	}
	if team.Manifest.Team != "editorial" || team.Roles[0].Bundle.Role != "editor" || definition.Name != "editorial-publication" || string(definition.Stages[0].PreferredFQRNs[0]) != team.Roles[0].Binding.Role {
		t.Fatalf("team=%+v workflow=%+v", team.Manifest, definition)
	}

	now := time.Date(2026, 9, 13, 11, 0, 0, 0, time.UTC)
	message := testMessage(70)
	message.Type = "tekroo.message.content.requested"
	message.Sender = "editorial::operator-1"
	message.Recipient = "editorial::editor-1"
	message.ID = testUUID(71)
	message.CorrelationID = testUUID(72)
	message.Flow.ThreadID = message.CorrelationID
	message.Work.StoryID = uuidPointer(testUUID(73))
	message.Work.DAGNodeID = testUUID(74)
	message.Flow.StepID = message.Work.DAGNodeID
	message.Flow.BudgetAccountID = testUUID(75)
	message.CreatedAt = now
	message.ExpiresAt = now.Add(time.Hour)
	store := NewMemoryOrganizationalMessageStore()
	if err := store.AppendMessage(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	instance, err := kernel.NewWorkflowInstance(definition, testUUID(76), kernel.AggregateRef{Kind: kernel.AggregateStory, ID: *message.Work.StoryID}, message.Flow.BudgetAccountID, map[string]kernel.UUIDv7{"draft": message.Work.DAGNodeID, "edit": testUUID(77)})
	if err != nil {
		t.Fatal(err)
	}
	last := message.ID
	instance.LastEventID = &last
	bound, err := store.BindMessageToWorkflow(context.Background(), message.ID, definition, instance, "draft")
	if err != nil || bound.Work.WorkflowInstanceID == nil || *bound.Work.WorkflowInstanceID != instance.InstanceID {
		t.Fatalf("bound=%+v err=%v", bound.Work, err)
	}
	library := NewWorkflowLibrary()
	if err := library.Add(definition); err != nil {
		t.Fatal(err)
	}
	execution := testExecution(78, 1)
	coordinator, err := NewWorkflowAdmissionCoordinator(store, library, workflowActorSource{eligible: true, state: testRoleState(bound.Recipient, execution)}, fake.NewClock(now), fake.NewIDSource(testUUID(79), testUUID(80), testUUID(81), testUUID(82)))
	if err != nil {
		t.Fatal(err)
	}
	admission, handled, _, err := coordinator.Admit(context.Background(), bound)
	if err != nil || !handled || admission.Outcome != kernel.WorkAdmitted || admission.AuthorizedInvocationID == nil {
		t.Fatalf("admission=%+v handled=%t err=%v", admission, handled, err)
	}
	current, found, err := store.ReadWorkflowInstance(context.Background(), instance.InstanceID)
	if err != nil || !found {
		t.Fatal(err)
	}
	current, err = store.ApplyWorkflowTransition(context.Background(), definition, instance.InstanceID, kernel.WorkflowTransition{Revision: current.Revision + 1, EventID: testUUID(83), Kind: kernel.WorkflowTransitionStart, NodeID: message.Work.DAGNodeID, InvocationID: *admission.AuthorizedInvocationID, RecordedAt: now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	current, err = store.ApplyWorkflowTransition(context.Background(), definition, instance.InstanceID, kernel.WorkflowTransition{Revision: current.Revision + 1, EventID: testUUID(84), Kind: kernel.WorkflowTransitionComplete, NodeID: message.Work.DAGNodeID, InvocationID: *admission.AuthorizedInvocationID, ProgressDigest: testDigest('e'), EvidenceIDs: []kernel.UUIDv7{}, RecordedAt: now.Add(2 * time.Second)})
	if err != nil || len(current.ReadyNodes()) != 1 || current.ReadyNodes()[0].StageID != "edit" {
		t.Fatalf("current=%+v err=%v", current, err)
	}
}

func uuidPointer(value kernel.UUIDv7) *kernel.UUIDv7 { return &value }

type workflowActorSource struct {
	state    RoleInstanceState
	eligible bool
}

func (source workflowActorSource) Status(context.Context, kernel.ActorFQN) (RoleInstanceState, bool, error) {
	return source.state, true, nil
}

func (source workflowActorSource) EligibleForWorkflowStage(kernel.ActorFQN, kernel.WorkflowStageDefinition) bool {
	return source.eligible
}

func TestWorkflowAdmissionCoordinatorConvertsOnlyLinkedWorkMessages(t *testing.T) {
	now := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	definition := organizationWorkflowDefinition(t, "publication")
	definition.TriggerTypes = []string{"tekroo.message.task.assigned"}
	digest, digestErr := definition.CalculatedDigest()
	if digestErr != nil {
		t.Fatal(digestErr)
	}
	definition.ContentDigest = digest
	workflowID := testUUID(8)
	message := testMessage(50)
	message.ID = testUUID(10)
	message.CorrelationID = testUUID(11)
	message.Flow.ThreadID = message.CorrelationID
	message.Work.WorkflowInstanceID = &workflowID
	message.Work.WorkflowStageID = "perform"
	message.Work.DAGNodeID = testUUID(9)
	message.Flow.StepID = message.Work.DAGNodeID
	message.Flow.BudgetAccountID = testUUID(12)
	message.Flow.ProgressDigest = testDigest('d')
	instance, err := kernel.NewWorkflowInstance(definition, workflowID, kernel.AggregateRef{Kind: kernel.AggregateStory, ID: testUUID(13)}, message.Flow.BudgetAccountID, map[string]kernel.UUIDv7{"perform": message.Work.DAGNodeID})
	if err != nil {
		t.Fatal(err)
	}
	causation := testUUID(14)
	instance.LastEventID = &causation
	store := NewMemoryOrganizationalMessageStore()
	if err := store.AppendMessage(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateWorkflowInstance(context.Background(), definition, instance); err != nil {
		t.Fatal(err)
	}
	library := NewWorkflowLibrary()
	if err := library.Add(definition); err != nil {
		t.Fatal(err)
	}
	execution := testExecution(15, 1)
	actorSource := workflowActorSource{eligible: true, state: testRoleState(message.Recipient, execution)}
	coordinator, err := NewWorkflowAdmissionCoordinator(store, library, actorSource, fake.NewClock(now), fake.NewIDSource(testUUID(16), testUUID(17), testUUID(18), testUUID(19)))
	if err != nil {
		t.Fatal(err)
	}
	result, handled, replayed, err := coordinator.Admit(context.Background(), message)
	if err != nil || !handled || replayed || result.Outcome != kernel.WorkAdmitted || result.AuthorizedInvocationID == nil || *result.AuthorizedInvocationID != testUUID(18) {
		t.Fatalf("result=%+v handled=%t replayed=%t err=%v", result, handled, replayed, err)
	}
	replayedResult, handled, replayed, err := coordinator.Admit(context.Background(), message)
	if err != nil || !handled || !replayed || replayedResult.ProposalID != result.ProposalID {
		t.Fatalf("replay=%+v handled=%t replayed=%t err=%v", replayedResult, handled, replayed, err)
	}
}

func TestWorkflowAdmissionCoordinatorLeavesInformationDeliverable(t *testing.T) {
	store := NewMemoryOrganizationalMessageStore()
	library := NewWorkflowLibrary()
	coordinator, err := NewWorkflowAdmissionCoordinator(store, library, workflowActorSource{}, fake.NewClock(time.Now()), fake.NewIDSource())
	if err != nil {
		t.Fatal(err)
	}
	message := testMessage(60)
	message.Purpose = PurposeEvidence
	if _, handled, replayed, err := coordinator.Admit(context.Background(), message); err != nil || handled || replayed {
		t.Fatalf("handled=%t replayed=%t err=%v", handled, replayed, err)
	}
}

func TestWorkflowAdmissionCoordinatorHoldsUnboundWorkflowMessages(t *testing.T) {
	definition := organizationWorkflowDefinition(t, "publication")
	definition.TriggerTypes = []string{"tekroo.message.task.assigned"}
	digest, digestErr := definition.CalculatedDigest()
	if digestErr != nil {
		t.Fatal(digestErr)
	}
	definition.ContentDigest = digest
	library := NewWorkflowLibrary()
	if err := library.Add(definition); err != nil {
		t.Fatal(err)
	}
	store := NewMemoryOrganizationalMessageStore()
	coordinator, err := NewWorkflowAdmissionCoordinator(store, library, workflowActorSource{}, fake.NewClock(time.Now()), fake.NewIDSource())
	if err != nil {
		t.Fatal(err)
	}
	trigger := testMessage(61)
	if result, handled, replayed, admitErr := coordinator.Admit(context.Background(), trigger); admitErr != nil || !handled || replayed || result.Outcome != "" {
		t.Fatalf("trigger result=%+v handled=%t replayed=%t err=%v", result, handled, replayed, admitErr)
	}
	instance, instanceErr := kernel.NewWorkflowInstance(definition, testUUID(62), kernel.AggregateRef{Kind: kernel.AggregateStory, ID: testUUID(63)}, trigger.Flow.BudgetAccountID, map[string]kernel.UUIDv7{"perform": testUUID(64)})
	if instanceErr != nil {
		t.Fatal(instanceErr)
	}
	if instanceErr = store.CreateWorkflowInstance(context.Background(), definition, instance); instanceErr != nil {
		t.Fatal(instanceErr)
	}
	followup := testMessage(65)
	followup.Flow.BudgetAccountID = trigger.Flow.BudgetAccountID
	if result, handled, replayed, admitErr := coordinator.Admit(context.Background(), followup); admitErr != nil || !handled || replayed || result.Outcome != "" {
		t.Fatalf("followup result=%+v handled=%t replayed=%t err=%v", result, handled, replayed, admitErr)
	}
}
