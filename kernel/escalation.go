package kernel

import "time"

type EscalationState string

const (
	EscalationOpen     EscalationState = "OPEN"
	EscalationTerminal EscalationState = "TERMINAL"
)

type EscalationAction string

const (
	EscalationActionOpen    EscalationAction = "OPEN"
	EscalationActionResolve EscalationAction = "RESOLVE"
	EscalationActionReroute EscalationAction = "REROUTE"
)

type EscalationSourceRole string

const (
	EscalationSourceAdjudicator   EscalationSourceRole = "ADJUDICATOR"
	EscalationSourcePolicyTimeout EscalationSourceRole = "POLICY_TIMEOUT"
)

type EscalationSnapshot struct {
	State                 EscalationState
	Adjudicator           PrincipalRef
	TimeoutPolicy         PrincipalRef
	SubjectLifecycleEpoch uint64
	DeadlineAt            time.Time
	ResolutionRoundLimit  uint64
}

type EscalationTransition struct {
	Action                EscalationAction
	Authority             PrincipalRef
	SourceRole            EscalationSourceRole
	SubjectLifecycleEpoch uint64
	DecidedAt             time.Time
	Round                 uint64
	Outcome               string
}

type EscalationDecision struct {
	Accepted bool            `json:"accepted"`
	Reason   string          `json:"reason"`
	State    EscalationState `json:"state"`
}

func EvaluateEscalation(snapshot EscalationSnapshot, transition EscalationTransition) EscalationDecision {
	reject := func(reason string) EscalationDecision {
		return EscalationDecision{Accepted: false, Reason: reason, State: snapshot.State}
	}
	if snapshot.State == EscalationTerminal {
		return reject("ESCALATION_TERMINAL")
	}
	switch transition.Action {
	case EscalationActionOpen:
		return reject("ESCALATION_ALREADY_OPEN")
	case EscalationActionReroute:
		return reject("REROUTE_PROHIBITED")
	case EscalationActionResolve:
	default:
		return reject("INVALID_ACTION")
	}
	if transition.SubjectLifecycleEpoch != snapshot.SubjectLifecycleEpoch {
		return reject("LIFECYCLE_EPOCH_MISMATCH")
	}
	switch transition.SourceRole {
	case EscalationSourceAdjudicator:
		if transition.Authority != snapshot.Adjudicator {
			return reject("ADJUDICATOR_MISMATCH")
		}
		if transition.Round == 0 || transition.Round > snapshot.ResolutionRoundLimit {
			return reject("RESOLUTION_BUDGET_EXHAUSTED")
		}
		if transition.DecidedAt.After(snapshot.DeadlineAt) {
			return reject("ADJUDICATOR_DEADLINE_EXCEEDED")
		}
	case EscalationSourcePolicyTimeout:
		if transition.Authority != snapshot.TimeoutPolicy {
			return reject("TIMEOUT_POLICY_MISMATCH")
		}
		if !transition.DecidedAt.After(snapshot.DeadlineAt) {
			return reject("DEADLINE_NOT_REACHED")
		}
		if transition.Outcome != "BLOCKED" && transition.Outcome != "HUMAN_REQUIRED" {
			return reject("INVALID_TIMEOUT_OUTCOME")
		}
	default:
		return reject("INVALID_SOURCE_ROLE")
	}
	return EscalationDecision{Accepted: true, Reason: "ACCEPTED", State: EscalationTerminal}
}
