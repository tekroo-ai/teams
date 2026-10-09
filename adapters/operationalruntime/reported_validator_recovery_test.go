package operationalruntime

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func reportedValidatorID(value int) kernel.UUIDv7 {
	return kernel.UUIDv7(fmt.Sprintf("00000000-0000-7000-8000-%012d", value))
}

func reportedValidatorBlockFixture(t *testing.T) (organization.PlannedTask, kernel.WorkInvocation, kernel.DomainEvent, kernel.PrincipalRef) {
	t.Helper()
	task := organization.PlannedTask{ID: reportedValidatorID(11311), Purpose: kernel.PurposeValidation, Validates: []kernel.UUIDv7{reportedValidatorID(11312)}}
	invocation := recoveryTerminalFixture(kernel.InvocationSucceeded, nil, nil)
	invocation.TaskID, invocation.Purpose, invocation.AttemptFamily = task.ID, task.Purpose, "validation"
	output := repeatedDigest('a')
	invocation.OutputDigest = &output
	authority := kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "teams-admission-policy"}
	payload, err := json.Marshal(planningOutputBlockPayload{BlockerRefs: []string{"teams://work-invocation/" + string(invocation.ID)}, Reason: "task reported blocked: The required deterministic go-test gate cannot execute to completion; an unavailable gate is not a pass.", ReviewPolicy: invalidStructuredReviewPolicy})
	if err != nil {
		t.Fatal(err)
	}
	actor, execution := invocation.ActorFQN, invocation.Execution
	event := kernel.DomainEvent{EventID: reportedValidatorID(11313), EventType: "tekroo.event.work.blocked", Aggregate: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}, Authority: authority, ActorFQN: &actor, Execution: &execution, Parents: []kernel.DagParent{{ParentEventID: invocation.LastEventID, EdgeKind: kernel.EdgeResponse}}, Payload: payload}
	return task, invocation, event, authority
}

func TestReportedValidatorBlockOperatorRecoveryIsExact(t *testing.T) {
	task, invocation, event, authority := reportedValidatorBlockFixture(t)
	if !isExactReportedValidatorBlock(event, task, invocation, authority) {
		t.Fatal("exact reported validator block was not recognized")
	}
	if isExactInvalidStructuredOutputBlock(event, task, invocation, authority) || isExactRecoverableStructuredDecisionBlock(event, task, invocation, authority) {
		t.Fatal("truthful reported block was misclassified as a structured-decision defect")
	}
	if ordinaryRetryableTerminalEligible(task, kernel.AggregateState{Phase: kernel.PhaseActive, Condition: kernel.ConditionBlocked}, invocation) || structuredOutputGlitchRetryAllowed(task, invocation) {
		t.Fatal("reported block admitted automatic retry")
	}
	for _, purpose := range []kernel.WorkPurpose{kernel.PurposeImplementation, kernel.PurposePromotion, kernel.PurposeHandoff} {
		other := task
		other.Purpose = purpose
		if isExactReportedValidatorBlock(event, other, invocation, authority) {
			t.Fatalf("unrelated purpose %s accepted", purpose)
		}
	}
	for _, mutate := range []struct {
		name  string
		apply func(*organization.PlannedTask, *kernel.WorkInvocation, *kernel.DomainEvent)
	}{
		{"no validation target", func(t *organization.PlannedTask, _ *kernel.WorkInvocation, _ *kernel.DomainEvent) { t.Validates = nil }},
		{"wrong task", func(_ *organization.PlannedTask, i *kernel.WorkInvocation, _ *kernel.DomainEvent) {
			i.TaskID = reportedValidatorID(11314)
		}},
		{"wrong invocation", func(_ *organization.PlannedTask, i *kernel.WorkInvocation, _ *kernel.DomainEvent) {
			i.ID = reportedValidatorID(11314)
		}},
		{"wrong purpose", func(_ *organization.PlannedTask, i *kernel.WorkInvocation, _ *kernel.DomainEvent) {
			i.Purpose = kernel.PurposeImplementation
		}},
		{"failed invocation", func(_ *organization.PlannedTask, i *kernel.WorkInvocation, _ *kernel.DomainEvent) {
			i.State = kernel.InvocationFailed
		}},
		{"missing output", func(_ *organization.PlannedTask, i *kernel.WorkInvocation, _ *kernel.DomainEvent) {
			i.OutputDigest = nil
		}},
		{"wrong authority", func(_ *organization.PlannedTask, _ *kernel.WorkInvocation, e *kernel.DomainEvent) {
			e.Authority = kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"}
		}},
		{"wrong actor", func(_ *organization.PlannedTask, _ *kernel.WorkInvocation, e *kernel.DomainEvent) {
			a := kernel.ActorFQN("teams::tester-9")
			e.ActorFQN = &a
		}},
		{"stale execution", func(_ *organization.PlannedTask, _ *kernel.WorkInvocation, e *kernel.DomainEvent) {
			x := *e.Execution
			x.FencingEpoch++
			e.Execution = &x
		}},
		{"wrong parent", func(_ *organization.PlannedTask, _ *kernel.WorkInvocation, e *kernel.DomainEvent) {
			e.Parents = []kernel.DagParent{{ParentEventID: reportedValidatorID(11315), EdgeKind: kernel.EdgeResponse}}
		}},
		{"causal parent", func(_ *organization.PlannedTask, _ *kernel.WorkInvocation, e *kernel.DomainEvent) {
			e.Parents = []kernel.DagParent{{ParentEventID: invocation.LastEventID, EdgeKind: kernel.EdgeCausal}}
		}},
		{"wrong aggregate", func(_ *organization.PlannedTask, _ *kernel.WorkInvocation, e *kernel.DomainEvent) {
			e.Aggregate.ID = reportedValidatorID(11315)
		}},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			a, b, c := task, invocation, event
			mutate.apply(&a, &b, &c)
			if isExactReportedValidatorBlock(c, a, b, authority) {
				t.Fatal("mismatched block accepted")
			}
		})
	}
	for _, reason := range []string{"human requested pause", "task reported needs_decision: missing input", "task reported failed: missing input", "task reported blocked: ", "task reported blocked: \t"} {
		other := event
		other.Payload, _ = json.Marshal(planningOutputBlockPayload{BlockerRefs: []string{"teams://work-invocation/" + string(invocation.ID)}, Reason: reason, ReviewPolicy: invalidStructuredReviewPolicy})
		if isExactReportedValidatorBlock(other, task, invocation, authority) {
			t.Fatalf("unrelated reason %q accepted", reason)
		}
	}
	for _, payload := range []planningOutputBlockPayload{
		{BlockerRefs: []string{"teams://work-invocation/" + string(invocation.ID), "human:pause"}, Reason: "task reported blocked: gate unavailable", ReviewPolicy: invalidStructuredReviewPolicy},
		{BlockerRefs: []string{"teams://work-invocation/" + string(invocation.ID)}, Reason: "task reported blocked: gate unavailable", ReviewPolicy: "human-only"},
	} {
		other := event
		other.Payload, _ = json.Marshal(payload)
		if isExactReportedValidatorBlock(other, task, invocation, authority) {
			t.Fatal("unrelated blockers or review policy accepted")
		}
	}
	task.Purpose = kernel.PurposeReview
	invocation.Purpose = kernel.PurposeReview
	if !isExactReportedValidatorBlock(event, task, invocation, authority) {
		t.Fatal("exact independent review block was not recognized")
	}
}
