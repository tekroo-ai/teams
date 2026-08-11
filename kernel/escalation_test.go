package kernel_test

import (
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

func TestEscalationIsExactBoundedAndTerminal(t *testing.T) {
	deadline := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	adjudicator := kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal-adjudicator"}
	timeoutPolicy := kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "escalation-timeout-policy"}
	snapshot := kernel.EscalationSnapshot{
		State: kernel.EscalationOpen, Adjudicator: adjudicator, TimeoutPolicy: timeoutPolicy,
		SubjectLifecycleEpoch: 1, DeadlineAt: deadline, ResolutionRoundLimit: 1,
	}

	tests := []struct {
		name       string
		transition kernel.EscalationTransition
		accepted   bool
		reason     string
	}{
		{name: "exact adjudicator", transition: kernel.EscalationTransition{Action: kernel.EscalationActionResolve, Authority: adjudicator, SourceRole: kernel.EscalationSourceAdjudicator, SubjectLifecycleEpoch: 1, DecidedAt: deadline, Round: 1, Outcome: "RESOLVED"}, accepted: true, reason: "ACCEPTED"},
		{name: "wrong adjudicator", transition: kernel.EscalationTransition{Action: kernel.EscalationActionResolve, Authority: timeoutPolicy, SourceRole: kernel.EscalationSourceAdjudicator, SubjectLifecycleEpoch: 1, DecidedAt: deadline, Round: 1, Outcome: "RESOLVED"}, reason: "ADJUDICATOR_MISMATCH"},
		{name: "budget exhausted", transition: kernel.EscalationTransition{Action: kernel.EscalationActionResolve, Authority: adjudicator, SourceRole: kernel.EscalationSourceAdjudicator, SubjectLifecycleEpoch: 1, DecidedAt: deadline, Round: 2, Outcome: "RESOLVED"}, reason: "RESOLUTION_BUDGET_EXHAUSTED"},
		{name: "timeout policy after deadline", transition: kernel.EscalationTransition{Action: kernel.EscalationActionResolve, Authority: timeoutPolicy, SourceRole: kernel.EscalationSourcePolicyTimeout, SubjectLifecycleEpoch: 1, DecidedAt: deadline.Add(time.Nanosecond), Round: 1, Outcome: "HUMAN_REQUIRED"}, accepted: true, reason: "ACCEPTED"},
		{name: "no reroute", transition: kernel.EscalationTransition{Action: kernel.EscalationActionReroute}, reason: "REROUTE_PROHIBITED"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := kernel.EvaluateEscalation(snapshot, test.transition)
			if decision.Accepted != test.accepted || decision.Reason != test.reason {
				t.Fatalf("decision = %+v, want accepted=%t reason=%s", decision, test.accepted, test.reason)
			}
		})
	}
}

func TestTerminalEscalationCannotBeReopened(t *testing.T) {
	decision := kernel.EvaluateEscalation(
		kernel.EscalationSnapshot{State: kernel.EscalationTerminal},
		kernel.EscalationTransition{Action: kernel.EscalationActionOpen},
	)
	if decision.Accepted || decision.Reason != "ESCALATION_TERMINAL" || decision.State != kernel.EscalationTerminal {
		t.Fatalf("decision = %+v", decision)
	}
}
