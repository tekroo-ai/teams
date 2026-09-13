//go:build mongo_integration

package mongo

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestMongoWorkflowAdmissionCommitsMessageNodeAndAuthorizationOnce(t *testing.T) {
	store := openTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	now := testNow()
	recipient := kernel.ActorFQN("teams::coder-1")
	execution := kernel.ExecutionTuple{ExecutionID: testUUID(12001), FencingEpoch: 1}
	role := organization.RoleInstanceState{
		ActorFQN: recipient, Team: "teams", Role: "coder", Instance: 1, Revision: 1,
		BundleDigest: messageTestDigest('1'), ModelProfile: messageTestDigest('2'), WorkspaceID: "coder-1", Status: organization.RoleIdle,
		Execution: execution, ProcessIdentity: "test-process", StartedAt: now, LastHeartbeatAt: now,
		ManifestDigest: messageTestDigest('3'), ManifestVersion: "1.0.0", BundleVersion: "1.0.0", RuntimeGeneration: 1,
	}
	if err := store.CompareAndSwapRole(ctx, 0, role); err != nil {
		t.Fatal(err)
	}
	definition := mongoWorkflowDefinition(t)
	workflowID := testUUID(12002)
	nodeID := testUUID(12003)
	budgetID := testUUID(12004)
	root := kernel.AggregateRef{Kind: kernel.AggregateStory, ID: testUUID(12005)}
	instance, err := kernel.NewWorkflowInstance(definition, workflowID, root, budgetID, map[string]kernel.UUIDv7{"implement": nodeID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateWorkflowInstance(ctx, definition, instance); err != nil {
		t.Fatal(err)
	}
	limits := make(kernel.PurposeCounters, len(kernel.AllWorkPurposes))
	used := make(kernel.PurposeCounters, len(kernel.AllWorkPurposes))
	for _, purpose := range kernel.AllWorkPurposes {
		limits[purpose] = 2
		used[purpose] = 0
	}
	budget := kernel.WorkBudgetAccount{ID: budgetID, Revision: 1, RootWork: root, LifecycleEpoch: 1, PolicyRevision: 1, PolicyDigest: messageTestDigest('4'), ModelInvocationLimit: 2, PurposeLimits: limits, PurposeUsed: used, DeadlineAt: now.Add(time.Hour), LastEventID: testUUID(12006)}
	insertProjectionValue(t, store, "work_budget_accounts", aggregateKey(budget.Ref()), workBudgetValue{Reference: budget.Ref(), Account: budget})
	causationID := testUUID(12007)
	if _, err := store.db.Collection("events").InsertOne(ctx, eventDocument{ID: string(causationID), AggregateKey: aggregateKey(root), Revision: 1, EventType: "test.causation"}); err != nil {
		t.Fatal(err)
	}
	threadID := testUUID(12008)
	message := organization.OrganizationalMessage{
		SchemaVersion: organization.OrganizationalMessageSchemaVersion, ID: testUUID(12009), Type: "tekroo.message.task.assigned", Purpose: organization.PurposeHandoff,
		Sender: "teams::architect-1", SenderExecution: kernel.ExecutionTuple{ExecutionID: testUUID(12010), FencingEpoch: 1}, Recipient: recipient, CorrelationID: threadID,
		Work: organization.MessageWorkLink{StoryID: &root.ID, WorkflowInstanceID: &workflowID, WorkflowStageID: "implement", DAGNodeID: nodeID},
		Flow: organization.MessageFlow{ThreadID: threadID, StepID: nodeID, Hop: 1, MaximumHops: 8, BudgetAccountID: budgetID, LifecycleEpoch: 1, ScopeRevision: 1, ProgressDigest: messageTestDigest('5')},
		Body: json.RawMessage(`{"task":"implement"}`), CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := store.AppendMessage(ctx, message); err != nil {
		t.Fatal(err)
	}
	proposal, err := organization.WorkProposalFromMessage(message, testUUID(12011), execution, []kernel.UUIDv7{causationID}, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	request := organization.WorkflowAdmissionRequest{
		Message: message, Definition: definition, Proposal: proposal,
		Facts:           kernel.WorkflowAdmissionFacts{ActorEligible: true, BudgetAvailable: true, CausationValid: true},
		RecordedEventID: testUUID(12012), AuthorizedInvocationID: testUUID(12013), IntentID: testUUID(12014), RecordedAt: now,
	}
	result, replayed, err := store.AdmitWorkflowMessage(ctx, request)
	if err != nil || replayed || result.Outcome != kernel.WorkAdmitted || result.AuthorizedInvocationID == nil || *result.AuthorizedInvocationID != request.AuthorizedInvocationID {
		t.Fatalf("admission=%+v replayed=%t err=%v", result, replayed, err)
	}
	claim, found, err := store.ReadMessage(ctx, message.ID)
	if err != nil || !found || claim.State != organization.MessageResolved {
		t.Fatalf("claim=%+v found=%t err=%v", claim, found, err)
	}
	current, found, err := store.ReadWorkflowInstance(ctx, workflowID)
	if err != nil || !found || current.Nodes[0].State != kernel.WorkflowNodeAdmitted {
		t.Fatalf("workflow=%+v found=%t err=%v", current, found, err)
	}
	replayedResult, replayed, err := store.AdmitWorkflowMessage(ctx, request)
	if err != nil || !replayed || replayedResult.ProposalID != result.ProposalID || replayedResult.Outcome != result.Outcome || replayedResult.AuthorizedInvocationID == nil || *replayedResult.AuthorizedInvocationID != request.AuthorizedInvocationID {
		t.Fatalf("replay=%+v replayed=%t err=%v", replayedResult, replayed, err)
	}
	count, err := store.db.Collection("workflow_admissions").CountDocuments(ctx, bson.D{})
	if err != nil || count != 1 {
		t.Fatalf("admission count=%d err=%v", count, err)
	}
	transition := kernel.WorkflowTransition{Revision: current.Revision + 1, EventID: testUUID(12015), Kind: kernel.WorkflowTransitionStart, NodeID: nodeID, InvocationID: request.AuthorizedInvocationID, RecordedAt: now.Add(time.Second)}
	started, err := store.ApplyWorkflowTransition(ctx, definition, workflowID, transition)
	if err != nil || started.Nodes[0].State != kernel.WorkflowNodeRunning {
		t.Fatalf("start transition=%+v err=%v", started, err)
	}
	replayedStart, err := store.ApplyWorkflowTransition(ctx, definition, workflowID, transition)
	if err != nil || replayedStart.Revision != started.Revision || replayedStart.Nodes[0].State != kernel.WorkflowNodeRunning {
		t.Fatalf("start replay=%+v err=%v", replayedStart, err)
	}
}

func TestMongoWorkflowAdmissionRejectsStaleExecutionWithoutMutation(t *testing.T) {
	store := openTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	definition := mongoWorkflowDefinition(t)
	workflowID := testUUID(12102)
	nodeID := testUUID(12103)
	budgetID := testUUID(12104)
	root := kernel.AggregateRef{Kind: kernel.AggregateStory, ID: testUUID(12105)}
	instance, err := kernel.NewWorkflowInstance(definition, workflowID, root, budgetID, map[string]kernel.UUIDv7{"implement": nodeID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateWorkflowInstance(ctx, definition, instance); err != nil {
		t.Fatal(err)
	}
	now := testNow()
	threadID := testUUID(12108)
	message := organization.OrganizationalMessage{SchemaVersion: organization.OrganizationalMessageSchemaVersion, ID: testUUID(12109), Type: "tekroo.message.task.assigned", Purpose: organization.PurposeHandoff, Sender: "teams::architect-1", SenderExecution: kernel.ExecutionTuple{ExecutionID: testUUID(12110), FencingEpoch: 1}, Recipient: "teams::coder-1", CorrelationID: threadID, Work: organization.MessageWorkLink{WorkflowInstanceID: &workflowID, WorkflowStageID: "implement", DAGNodeID: nodeID}, Flow: organization.MessageFlow{ThreadID: threadID, StepID: nodeID, Hop: 1, MaximumHops: 8, BudgetAccountID: budgetID, LifecycleEpoch: 1, ScopeRevision: 1, ProgressDigest: messageTestDigest('6')}, Body: json.RawMessage(`{"task":"implement"}`), CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := store.AppendMessage(ctx, message); err != nil {
		t.Fatal(err)
	}
	proposal, err := organization.WorkProposalFromMessage(message, testUUID(12111), kernel.ExecutionTuple{ExecutionID: testUUID(12112), FencingEpoch: 1}, []kernel.UUIDv7{testUUID(12113)}, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.AdmitWorkflowMessage(ctx, organization.WorkflowAdmissionRequest{Message: message, Definition: definition, Proposal: proposal, Facts: kernel.WorkflowAdmissionFacts{ActorEligible: true, BudgetAvailable: true, CausationValid: true}, RecordedEventID: testUUID(12114), AuthorizedInvocationID: testUUID(12115), IntentID: testUUID(12116), RecordedAt: now})
	if !errors.Is(err, organization.ErrStaleOrganizationalClaim) {
		t.Fatalf("stale execution error=%v", err)
	}
	current, found, readErr := store.ReadWorkflowInstance(ctx, workflowID)
	if readErr != nil || !found || current.Revision != instance.Revision || current.Nodes[0].State != kernel.WorkflowNodeReady {
		t.Fatalf("workflow mutated=%+v found=%t err=%v", current, found, readErr)
	}
}

func TestMongoWorkflowBindingAtomicallyLinksInitialAndSuccessorMessages(t *testing.T) {
	store := openTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	now := testNow()
	definition := mongoWorkflowDefinition(t)
	definition.Stages = append(definition.Stages, kernel.WorkflowStageDefinition{StageID: "validate", DependsOn: []string{"implement"}, InputSchema: "patch/v1", OutputSchema: "validation/v1", RequiredCapabilities: []string{"validation"}, PreferredFQRNs: []kernel.RoleFQRN{}, Purpose: kernel.WorkflowPurposeValidation, Risk: kernel.WorkflowRiskModerate, ConcurrencyGroup: "validation", MaximumParallelism: 1, AttemptLimit: 1, AllowedOutgoingPurposes: []string{}, TargetSelection: kernel.WorkflowTargetCapability, ValidationPolicy: kernel.WorkflowValidationRiskSelected})
	digest, err := definition.CalculatedDigest()
	if err != nil {
		t.Fatal(err)
	}
	definition.ContentDigest = digest
	threadID := testUUID(12201)
	budgetID := testUUID(12202)
	first := organization.OrganizationalMessage{SchemaVersion: organization.OrganizationalMessageSchemaVersion, ID: testUUID(12203), Type: "tekroo.message.task.assigned", Purpose: organization.PurposeHandoff, Sender: "teams::architect-1", SenderExecution: kernel.ExecutionTuple{ExecutionID: testUUID(12204), FencingEpoch: 1}, Recipient: "teams::coder-1", CorrelationID: threadID, Work: organization.MessageWorkLink{DAGNodeID: testUUID(12205)}, Flow: organization.MessageFlow{ThreadID: threadID, StepID: testUUID(12205), Hop: 1, MaximumHops: 8, BudgetAccountID: budgetID, LifecycleEpoch: 1, ScopeRevision: 1, ProgressDigest: messageTestDigest('7')}, Body: json.RawMessage(`{"task":"implement"}`), CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := store.AppendMessage(ctx, first); err != nil {
		t.Fatal(err)
	}
	feed, err := store.OpenOrganizationalMessageFeed(ctx, "workflow-binding-observer", first.Recipient)
	if err != nil {
		t.Fatal(err)
	}
	defer feed.Close(context.Background())
	unbound, err := feed.Poll(ctx)
	if err != nil || unbound.Work.WorkflowInstanceID != nil {
		t.Fatalf("unbound feed message=%+v err=%v", unbound.Work, err)
	}
	instanceID := testUUID(12206)
	root := kernel.AggregateRef{Kind: kernel.AggregateStory, ID: testUUID(12207)}
	placeholder := testUUID(12208)
	instance, err := kernel.NewWorkflowInstance(definition, instanceID, root, budgetID, map[string]kernel.UUIDv7{"implement": first.Work.DAGNodeID, "validate": placeholder})
	if err != nil {
		t.Fatal(err)
	}
	last := first.ID
	instance.LastEventID = &last
	bound, err := store.BindMessageToWorkflow(ctx, first.ID, definition, instance, "implement")
	if err != nil || bound.Work.WorkflowInstanceID == nil || *bound.Work.WorkflowInstanceID != instanceID || bound.Work.WorkflowStageID != "implement" {
		t.Fatalf("initial binding=%+v err=%v", bound.Work, err)
	}
	var rebound organization.OrganizationalMessage
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		rebound, err = feed.Poll(ctx)
		if err == nil {
			break
		}
		if !errors.Is(err, organization.ErrOrganizationalMessageNotFound) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || rebound.Work.WorkflowInstanceID == nil || *rebound.Work.WorkflowInstanceID != instanceID {
		t.Fatalf("bound feed update=%+v err=%v", rebound.Work, err)
	}
	byBudget, found, err := store.ReadWorkflowInstanceByBudget(ctx, budgetID)
	if err != nil || !found || byBudget.InstanceID != instanceID {
		t.Fatalf("budget lookup=%+v found=%t err=%v", byBudget, found, err)
	}
	parentStep := first.Flow.StepID
	causation := first.ID
	second := organization.OrganizationalMessage{SchemaVersion: organization.OrganizationalMessageSchemaVersion, ID: testUUID(12209), Type: "tekroo.message.task.review-requested", Purpose: organization.PurposeHandoff, Sender: first.Recipient, SenderExecution: kernel.ExecutionTuple{ExecutionID: testUUID(12210), FencingEpoch: 1}, Recipient: "teams::tester-1", CorrelationID: threadID, CausationID: &causation, Work: organization.MessageWorkLink{DAGNodeID: testUUID(12211)}, Flow: organization.MessageFlow{ThreadID: threadID, StepID: testUUID(12211), ParentStepID: &parentStep, Hop: 2, MaximumHops: 8, BudgetAccountID: budgetID, LifecycleEpoch: 1, ScopeRevision: 1, ProgressDigest: messageTestDigest('8')}, Body: json.RawMessage(`{"task":"validate"}`), CreatedAt: now.Add(time.Second), ExpiresAt: now.Add(time.Hour)}
	if err := store.AppendMessage(ctx, second); err != nil {
		t.Fatal(err)
	}
	bound, err = store.BindMessageToWorkflow(ctx, second.ID, definition, byBudget, "validate")
	if err != nil || bound.Work.WorkflowStageID != "validate" {
		t.Fatalf("successor binding=%+v err=%v", bound.Work, err)
	}
	current, found, err := store.ReadWorkflowInstance(ctx, instanceID)
	if err != nil || !found || current.Revision != 2 || current.LastEventID == nil || *current.LastEventID != second.ID {
		t.Fatalf("current=%+v found=%t err=%v", current, found, err)
	}
	if current.Nodes[1].NodeID != second.Work.DAGNodeID || current.Nodes[1].PredecessorNodeIDs[0] != first.Work.DAGNodeID {
		t.Fatalf("successor node=%+v", current.Nodes[1])
	}
}

func mongoWorkflowDefinition(t *testing.T) kernel.WorkflowDefinition {
	t.Helper()
	definition := kernel.WorkflowDefinition{SchemaVersion: kernel.WorkflowDefinitionSchemaVersion, Name: "software", Version: "1.0.0", TriggerTypes: []string{"feature.requested"}, Stages: []kernel.WorkflowStageDefinition{{StageID: "implement", DependsOn: []string{}, InputSchema: "task/v1", OutputSchema: "patch/v1", RequiredCapabilities: []string{"implementation"}, PreferredFQRNs: []kernel.RoleFQRN{}, Purpose: kernel.WorkflowPurposeImplementation, Risk: kernel.WorkflowRiskModerate, ConcurrencyGroup: "implementation", MaximumParallelism: 4, AttemptLimit: 2, AllowedOutgoingPurposes: []string{}, TargetSelection: kernel.WorkflowTargetCapability, ValidationPolicy: kernel.WorkflowValidationRiskSelected}}, RootBudgets: kernel.WorkflowBudgetLimits{MaximumModelInvocations: 4, MaximumHops: 8, MaximumAttempts: 2}, ProjectionRules: []string{"MESSAGE", "TASK"}}
	digest, err := definition.CalculatedDigest()
	if err != nil {
		t.Fatal(err)
	}
	definition.ContentDigest = digest
	return definition
}
