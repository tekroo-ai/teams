package kernel

import (
	"reflect"
	"testing"
	"time"
)

func TestEscalationIsExactBoundedAndTerminal(t *testing.T) {
	deadline := time.Date(2026, time.August, 12, 0, 0, 0, 0, time.UTC)
	adjudicator := PrincipalRef{Kind: PrincipalHuman, ID: "principal-adjudicator"}
	timeoutPolicy := PrincipalRef{Kind: PrincipalPolicy, ID: "escalation-timeout-policy"}
	snapshot := EscalationSnapshot{
		State: EscalationOpen, Adjudicator: adjudicator, TimeoutPolicy: timeoutPolicy,
		SubjectLifecycleEpoch: 1, DeadlineAt: deadline, ResolutionRoundLimit: 1,
	}

	tests := []struct {
		name       string
		transition EscalationTransition
		accepted   bool
		reason     string
	}{
		{name: "exact adjudicator", transition: EscalationTransition{Action: EscalationActionResolve, Authority: adjudicator, SourceRole: EscalationSourceAdjudicator, SubjectLifecycleEpoch: 1, DecidedAt: deadline, Round: 1, Outcome: EscalationResolved}, accepted: true, reason: "ACCEPTED"},
		{name: "wrong adjudicator", transition: EscalationTransition{Action: EscalationActionResolve, Authority: timeoutPolicy, SourceRole: EscalationSourceAdjudicator, SubjectLifecycleEpoch: 1, DecidedAt: deadline, Round: 1, Outcome: EscalationResolved}, reason: "ADJUDICATOR_MISMATCH"},
		{name: "budget exhausted", transition: EscalationTransition{Action: EscalationActionResolve, Authority: adjudicator, SourceRole: EscalationSourceAdjudicator, SubjectLifecycleEpoch: 1, DecidedAt: deadline, Round: 2, Outcome: EscalationResolved}, reason: "RESOLUTION_BUDGET_EXHAUSTED"},
		{name: "timeout policy after deadline", transition: EscalationTransition{Action: EscalationActionResolve, Authority: timeoutPolicy, SourceRole: EscalationSourcePolicyTimeout, SubjectLifecycleEpoch: 1, DecidedAt: deadline.Add(time.Nanosecond), Round: 1, Outcome: EscalationHumanRequired}, accepted: true, reason: "ACCEPTED"},
		{name: "no reroute", transition: EscalationTransition{Action: EscalationActionReroute}, reason: "REROUTE_PROHIBITED"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := EvaluateEscalation(snapshot, test.transition)
			if decision.Accepted != test.accepted || decision.Reason != test.reason {
				t.Fatalf("decision = %+v, want accepted=%t reason=%s", decision, test.accepted, test.reason)
			}
		})
	}
}

func TestTerminalEscalationCannotBeReopened(t *testing.T) {
	decision := EvaluateEscalation(
		EscalationSnapshot{State: EscalationTerminal},
		EscalationTransition{Action: EscalationActionOpen},
	)
	if decision.Accepted || decision.Reason != "ESCALATION_TERMINAL" || decision.State != EscalationTerminal {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestPlanEscalationOpeningCanonicalizesEvidenceAndRetainsDirectedPath(t *testing.T) {
	input := validEscalationOpeningInput()
	originalPath := append([]UUIDv7(nil), input.CausalPathEventIDs...)
	originalEvidence := append([]EvidenceRef(nil), input.EvidenceRefs...)

	decision := PlanEscalationOpening(input)
	if decision.Status != EscalationPlanReady || decision.Reason != "READY" || !decision.Escalation.Valid() {
		t.Fatalf("opening decision = %#v", decision)
	}
	if !reflect.DeepEqual(decision.Escalation.CausalPathEventIDs, originalPath) {
		t.Fatalf("causal path = %#v, want directed input order %#v", decision.Escalation.CausalPathEventIDs, originalPath)
	}
	if decision.EvidenceRefs[0].EvidenceID != escalationUUID("00000000-0000-7000-8000-000000000811") || decision.EvidenceRefs[1].EvidenceID != escalationUUID("00000000-0000-7000-8000-000000000812") {
		t.Fatalf("canonical evidence = %#v", decision.EvidenceRefs)
	}
	if !reflect.DeepEqual(input.CausalPathEventIDs, originalPath) || !reflect.DeepEqual(input.EvidenceRefs, originalEvidence) {
		t.Fatal("planner mutated caller-owned input")
	}
	if decision.Escalation.RouteLimit != 1 || decision.Escalation.ExpectedSubjectRevision != input.Subject.Revision || decision.Escalation.SubjectLifecycleEpoch != input.Subject.LifecycleEpoch {
		t.Fatalf("bounded opening = %#v", decision.Escalation)
	}
}

func TestPlanEscalationOpeningFailsClosedOnDuplicateAndBounds(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*EscalationOpeningInput)
		reason string
	}{
		{name: "equivalent", mutate: func(input *EscalationOpeningInput) {
			ref := AggregateRef{Kind: AggregateEscalation, ID: escalationUUID("00000000-0000-7000-8000-000000000820")}
			input.EquivalentEscalationRef = &ref
		}, reason: "EQUIVALENT_ESCALATION_EXISTS"},
		{name: "terminal subject", mutate: func(input *EscalationOpeningInput) { input.Subject.Phase = PhaseCompleted }, reason: "INVALID_ESCALATION_OPENING"},
		{name: "expired deadline", mutate: func(input *EscalationOpeningInput) { input.DeadlineAt = input.OpenedAt }, reason: "INVALID_ESCALATION_OPENING"},
		{name: "unbounded rounds", mutate: func(input *EscalationOpeningInput) { input.ResolutionRoundLimit = 1001 }, reason: "INVALID_ESCALATION_OPENING"},
		{name: "duplicate evidence", mutate: func(input *EscalationOpeningInput) { input.EvidenceRefs[1] = input.EvidenceRefs[0] }, reason: "INVALID_ESCALATION_EVIDENCE"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validEscalationOpeningInput()
			test.mutate(&input)
			decision := PlanEscalationOpening(input)
			if decision.Status != EscalationPlanNoEffect || decision.Reason != test.reason {
				t.Fatalf("decision = %#v", decision)
			}
		})
	}
}

func TestPlanEscalationResolutionEnforcesAuthorityDeadlineAndTerminality(t *testing.T) {
	escalation := validOpenEscalation()
	input := validEscalationResolutionInput(escalation)
	decision := PlanEscalationResolution(input)
	if decision.Status != EscalationPlanReady || decision.Transition.Outcome != EscalationResolved {
		t.Fatalf("resolution decision = %#v", decision)
	}
	if len(decision.Parents) != 2 || !containsTestParent(decision.Parents, escalation.OpeningEventID, EdgeResponse) {
		t.Fatalf("canonical resolution parents = %#v", decision.Parents)
	}
	if !reflect.DeepEqual(decision.Transition.Reasons, []string{"A reason", "Z reason"}) {
		t.Fatalf("canonical reasons = %#v", decision.Transition.Reasons)
	}

	tests := []struct {
		name   string
		mutate func(*EscalationResolutionInput)
		reason string
	}{
		{name: "wrong adjudicator", mutate: func(input *EscalationResolutionInput) { input.Authority.ID = "other" }, reason: "ADJUDICATOR_MISMATCH"},
		{name: "adjudicator after deadline", mutate: func(input *EscalationResolutionInput) { input.DecidedAt = escalation.DeadlineAt.Add(time.Nanosecond) }, reason: "ADJUDICATOR_DEADLINE_EXCEEDED"},
		{name: "round budget", mutate: func(input *EscalationResolutionInput) { input.Round = 3 }, reason: "RESOLUTION_BUDGET_EXHAUSTED"},
		{name: "stale lifecycle", mutate: func(input *EscalationResolutionInput) { input.Subject.LifecycleEpoch++ }, reason: "INVALID_ESCALATION_RESOLUTION"},
		{name: "terminal escalation", mutate: func(input *EscalationResolutionInput) {
			input.Escalation.State = EscalationTerminal
			input.Escalation.TerminalOutcome = EscalationResolved
			input.Escalation.ResolutionEventID = escalationUUID("00000000-0000-7000-8000-000000000821")
		}, reason: "INVALID_ESCALATION_RESOLUTION"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := validEscalationResolutionInput(escalation)
			test.mutate(&candidate)
			result := PlanEscalationResolution(candidate)
			if result.Status != EscalationPlanNoEffect || result.Reason != test.reason {
				t.Fatalf("decision = %#v", result)
			}
		})
	}
}

func TestTimeoutResolutionRequiresPolicyAfterDeadlineAndBoundedOutcome(t *testing.T) {
	escalation := validOpenEscalation()
	input := validEscalationResolutionInput(escalation)
	input.Authority = escalation.TimeoutPolicy
	input.SourceRole = EscalationSourcePolicyTimeout
	input.DecidedAt = escalation.DeadlineAt.Add(time.Nanosecond)
	input.Outcome = EscalationHumanRequired

	if decision := PlanEscalationResolution(input); decision.Status != EscalationPlanReady {
		t.Fatalf("timeout resolution = %#v", decision)
	}
	input.DecidedAt = escalation.DeadlineAt
	if decision := PlanEscalationResolution(input); decision.Reason != "DEADLINE_NOT_REACHED" {
		t.Fatalf("at-deadline resolution = %#v", decision)
	}
	input.DecidedAt = escalation.DeadlineAt.Add(time.Nanosecond)
	input.Outcome = EscalationResolved
	if decision := PlanEscalationResolution(input); decision.Reason != "INVALID_TIMEOUT_OUTCOME" {
		t.Fatalf("invalid timeout outcome = %#v", decision)
	}
}

func validEscalationOpeningInput() EscalationOpeningInput {
	openedAt := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	return EscalationOpeningInput{
		EscalationID: escalationUUID("00000000-0000-7000-8000-000000000801"),
		Subject:      AggregateState{Kind: AggregateTask, ID: escalationUUID("00000000-0000-7000-8000-000000000802"), Revision: 7, LifecycleEpoch: 2, Phase: PhaseActive, Condition: ConditionRunnable},
		Trigger:      EscalationHandoffCycleDetected, ConditionDigest: Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		Adjudicator: PrincipalRef{Kind: PrincipalHuman, ID: "principal-adjudicator"}, TimeoutPolicy: PrincipalRef{Kind: PrincipalPolicy, ID: "escalation-timeout-policy"},
		ResolutionOwnerFQN: ActorFQN("teams::coder-1"), OpenedAt: openedAt, DeadlineAt: openedAt.Add(time.Hour), ResolutionRoundLimit: 2, PolicyRevision: 4,
		CausalPathEventIDs: []UUIDv7{escalationUUID("00000000-0000-7000-8000-000000000806"), escalationUUID("00000000-0000-7000-8000-000000000805")},
		UnresolvedQuestion: "Which directed successor resolves the cycle?",
		EvidenceRefs: []EvidenceRef{
			{EvidenceID: escalationUUID("00000000-0000-7000-8000-000000000812"), SHA256: Digest("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")},
			{EvidenceID: escalationUUID("00000000-0000-7000-8000-000000000811"), SHA256: Digest("cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")},
		},
	}
}

func validOpenEscalation() EscalationSnapshot {
	decision := PlanEscalationOpening(validEscalationOpeningInput())
	escalation := decision.Escalation
	escalation.OpeningEventID = escalationUUID("00000000-0000-7000-8000-000000000813")
	return escalation
}

func validEscalationResolutionInput(escalation EscalationSnapshot) EscalationResolutionInput {
	return EscalationResolutionInput{
		Escalation: escalation,
		Subject:    AggregateState{Kind: escalation.Subject.Kind, ID: escalation.Subject.ID, Revision: escalation.ExpectedSubjectRevision, LifecycleEpoch: escalation.SubjectLifecycleEpoch, Phase: PhaseActive, Condition: ConditionRunnable},
		Authority:  escalation.Adjudicator, SourceRole: EscalationSourceAdjudicator, Round: 1, Outcome: EscalationResolved,
		Reasons:      []string{"Z reason", "A reason"},
		EvidenceRefs: []EvidenceRef{{EvidenceID: escalationUUID("00000000-0000-7000-8000-000000000814"), SHA256: Digest("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd")}},
		DecidedAt:    escalation.DeadlineAt.Add(-time.Nanosecond),
		Parents:      []DagParent{{ParentEventID: escalationUUID("00000000-0000-7000-8000-000000000815"), EdgeKind: EdgeCausal}},
	}
}

func containsTestParent(parents []DagParent, eventID UUIDv7, edge EdgeKind) bool {
	for _, parent := range parents {
		if parent.ParentEventID == eventID && parent.EdgeKind == edge {
			return true
		}
	}
	return false
}

func escalationUUID(value string) UUIDv7 {
	return UUIDv7(value)
}
