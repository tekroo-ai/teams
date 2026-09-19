package operationalruntime

import (
	"encoding/json"
	"testing"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func escalationBlockFixture(t *testing.T, task organization.PlannedTask, invocation kernel.WorkInvocation, authority kernel.PrincipalRef, mutate func(*planningOutputBlockPayload)) kernel.DomainEvent {
	t.Helper()
	payload := planningOutputBlockPayload{
		BlockerRefs:  []string{"teams://work-invocation/" + string(invocation.ID)},
		Reason:       failedTaskEscalationReason,
		ReviewPolicy: invalidStructuredReviewPolicy,
	}
	if mutate != nil {
		mutate(&payload)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	actor := invocation.ActorFQN
	execution := invocation.Execution
	return kernel.DomainEvent{
		EventID:   "00000000-0000-7000-8000-000000000221",
		EventType: "tekroo.event.work.blocked",
		Aggregate: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID},
		Authority: authority,
		ActorFQN:  &actor,
		Execution: &execution,
		Parents:   []kernel.DagParent{{ParentEventID: invocation.LastEventID, EdgeKind: kernel.EdgeResponse}},
		Payload:   encoded,
	}
}

func TestFailedTaskEscalationBlockMatchingIsExact(t *testing.T) {
	task := organization.PlannedTask{ID: "00000000-0000-7000-8000-000000000222"}
	output := repeatedDigest('b')
	invocation := recoveryTerminalFixture(kernel.InvocationFailed, nil, nil)
	invocation.TaskID = task.ID
	invocation.Purpose = kernel.PurposeImplementation
	invocation.AttemptFamily = "implementation"
	invocation.OutputDigest = &output
	authority := kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "teams-admission-policy"}
	event := escalationBlockFixture(t, task, invocation, authority, nil)
	if !isExactFailedTaskEscalationBlock(event, task, invocation, authority) {
		t.Fatal("exact failed-task escalation block was not recognized")
	}

	otherInvocation := invocation
	otherInvocation.ID = "00000000-0000-7000-8000-000000000223"
	if isExactFailedTaskEscalationBlock(event, task, otherInvocation, authority) {
		t.Fatal("escalation block for another invocation was accepted")
	}
	if isExactFailedTaskEscalationBlock(event, organization.PlannedTask{ID: "00000000-0000-7000-8000-000000000224"}, invocation, authority) {
		t.Fatal("escalation block for another task was accepted")
	}
	if isExactFailedTaskEscalationBlock(event, task, invocation, kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "other-authority"}) {
		t.Fatal("escalation block under another authority was accepted")
	}
	if isExactFailedTaskEscalationBlock(escalationBlockFixture(t, task, invocation, authority, func(p *planningOutputBlockPayload) {
		p.Reason = invalidStructuredOutputReason
	}), task, invocation, authority) {
		t.Fatal("invalid-structured-output block was misclassified as a failed-task escalation")
	}
	if isExactFailedTaskEscalationBlock(escalationBlockFixture(t, task, invocation, authority, func(p *planningOutputBlockPayload) {
		p.ReviewPolicy = "some-other-policy"
	}), task, invocation, authority) {
		t.Fatal("escalation block with a different review policy was accepted")
	}

	stale := event
	stale.Parents = []kernel.DagParent{{ParentEventID: event.EventID, EdgeKind: kernel.EdgeCausal}}
	if isExactFailedTaskEscalationBlock(stale, task, invocation, authority) {
		t.Fatal("escalation block with a different causal relationship was accepted")
	}

	mismatchedActor := event
	actor := kernel.ActorFQN("teams::coder-9")
	mismatchedActor.ActorFQN = &actor
	if isExactFailedTaskEscalationBlock(mismatchedActor, task, invocation, authority) {
		t.Fatal("escalation block attributed to another actor was accepted")
	}
}
