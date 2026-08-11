package kernel

import (
	"encoding/json"
	"errors"
	"time"
)

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

type EscalationTrigger string

const (
	EscalationRetryExhausted            EscalationTrigger = "RETRY_EXHAUSTED"
	EscalationValidationConflict        EscalationTrigger = "VALIDATION_CONFLICT"
	EscalationValidationInconclusive    EscalationTrigger = "VALIDATION_INCONCLUSIVE"
	EscalationValidationBudgetExhausted EscalationTrigger = "VALIDATION_BUDGET_EXHAUSTED"
	EscalationValidationDeadlineExpired EscalationTrigger = "VALIDATION_DEADLINE_EXPIRED"
	EscalationHandoffCycleDetected      EscalationTrigger = "HANDOFF_CYCLE_DETECTED"
	EscalationHandoffBudgetExhausted    EscalationTrigger = "HANDOFF_BUDGET_EXHAUSTED"
)

func (trigger EscalationTrigger) Valid() bool {
	switch trigger {
	case EscalationRetryExhausted,
		EscalationValidationConflict,
		EscalationValidationInconclusive,
		EscalationValidationBudgetExhausted,
		EscalationValidationDeadlineExpired,
		EscalationHandoffCycleDetected,
		EscalationHandoffBudgetExhausted:
		return true
	default:
		return false
	}
}

type EscalationOutcome string

const (
	EscalationResolved      EscalationOutcome = "RESOLVED"
	EscalationRejected      EscalationOutcome = "REJECTED"
	EscalationSplit         EscalationOutcome = "SPLIT"
	EscalationBlocked       EscalationOutcome = "BLOCKED"
	EscalationHumanRequired EscalationOutcome = "HUMAN_REQUIRED"
)

func (outcome EscalationOutcome) Valid() bool {
	switch outcome {
	case EscalationResolved, EscalationRejected, EscalationSplit, EscalationBlocked, EscalationHumanRequired:
		return true
	default:
		return false
	}
}

type EscalationKey struct {
	Subject         AggregateRef      `json:"subject"`
	LifecycleEpoch  uint64            `json:"lifecycle_epoch"`
	Trigger         EscalationTrigger `json:"trigger"`
	ConditionDigest Digest            `json:"condition_digest"`
}

func (key EscalationKey) Valid() bool {
	return (key.Subject.Kind == AggregateStory || key.Subject.Kind == AggregateTask) && key.Subject.Valid() && key.LifecycleEpoch > 0 && key.Trigger.Valid() && key.ConditionDigest.Valid()
}

type EscalationSnapshot struct {
	EscalationID            UUIDv7            `json:"escalation_id"`
	OpeningEventID          UUIDv7            `json:"opening_event_id,omitempty"`
	Revision                uint64            `json:"revision"`
	State                   EscalationState   `json:"state"`
	Subject                 AggregateRef      `json:"subject"`
	SubjectLifecycleEpoch   uint64            `json:"subject_lifecycle_epoch"`
	ExpectedSubjectRevision uint64            `json:"expected_subject_revision"`
	Trigger                 EscalationTrigger `json:"trigger"`
	ConditionDigest         Digest            `json:"condition_digest"`
	Adjudicator             PrincipalRef      `json:"adjudicator"`
	TimeoutPolicy           PrincipalRef      `json:"timeout_policy"`
	ResolutionOwnerFQN      ActorFQN          `json:"resolution_owner_fqn"`
	DeadlineAt              time.Time         `json:"deadline_at"`
	ResolutionRoundLimit    uint64            `json:"resolution_round_limit"`
	RouteLimit              uint64            `json:"route_limit"`
	PolicyRevision          uint64            `json:"policy_revision"`
	CausalPathEventIDs      []UUIDv7          `json:"causal_path_event_ids"`
	UnresolvedQuestion      string            `json:"unresolved_question"`
	EvidenceIDs             []UUIDv7          `json:"evidence_ids"`
	TerminalOutcome         EscalationOutcome `json:"terminal_outcome,omitempty"`
	ResolutionEventID       UUIDv7            `json:"resolution_event_id,omitempty"`
}

func (snapshot EscalationSnapshot) Key() EscalationKey {
	return EscalationKey{Subject: snapshot.Subject, LifecycleEpoch: snapshot.SubjectLifecycleEpoch, Trigger: snapshot.Trigger, ConditionDigest: snapshot.ConditionDigest}
}

func (snapshot EscalationSnapshot) Clone() EscalationSnapshot {
	copy := snapshot
	copy.CausalPathEventIDs = append([]UUIDv7(nil), snapshot.CausalPathEventIDs...)
	copy.EvidenceIDs = append([]UUIDv7(nil), snapshot.EvidenceIDs...)
	return copy
}

func (snapshot EscalationSnapshot) Valid() bool {
	if !snapshot.EscalationID.Valid() || (snapshot.OpeningEventID != "" && !snapshot.OpeningEventID.Valid()) || snapshot.Revision == 0 || !snapshot.Key().Valid() || snapshot.ExpectedSubjectRevision == 0 || !snapshot.Adjudicator.Valid() || snapshot.TimeoutPolicy.Kind != PrincipalPolicy || !snapshot.TimeoutPolicy.Valid() || !snapshot.ResolutionOwnerFQN.Valid() || snapshot.DeadlineAt.IsZero() || snapshot.ResolutionRoundLimit == 0 || snapshot.ResolutionRoundLimit > 1000 || snapshot.RouteLimit != 1 || snapshot.PolicyRevision == 0 || len(snapshot.CausalPathEventIDs) == 0 || len(snapshot.CausalPathEventIDs) > 64 || len(snapshot.UnresolvedQuestion) == 0 || len(snapshot.UnresolvedQuestion) > 4096 || len(snapshot.EvidenceIDs) == 0 || len(snapshot.EvidenceIDs) > 64 {
		return false
	}
	if !uniqueValidUUIDs(snapshot.CausalPathEventIDs) || !uniqueValidUUIDs(snapshot.EvidenceIDs) {
		return false
	}
	switch snapshot.State {
	case EscalationOpen:
		return snapshot.TerminalOutcome == "" && snapshot.ResolutionEventID == ""
	case EscalationTerminal:
		return snapshot.TerminalOutcome.Valid() && snapshot.ResolutionEventID.Valid()
	default:
		return false
	}
}

type EscalationTransition struct {
	Action                EscalationAction
	EscalationID          UUIDv7
	Subject               AggregateRef
	Authority             PrincipalRef
	SourceRole            EscalationSourceRole
	SubjectLifecycleEpoch uint64
	ExpectedRevision      uint64
	DecidedAt             time.Time
	Round                 uint64
	Outcome               EscalationOutcome
	Reasons               []string
	EvidenceIDs           []UUIDv7
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
	if !transition.Outcome.Valid() {
		return reject("INVALID_TERMINAL_OUTCOME")
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
		if transition.Outcome != EscalationBlocked && transition.Outcome != EscalationHumanRequired {
			return reject("INVALID_TIMEOUT_OUTCOME")
		}
	default:
		return reject("INVALID_SOURCE_ROLE")
	}
	return EscalationDecision{Accepted: true, Reason: "ACCEPTED", State: EscalationTerminal}
}

func EscalationFromOpenPayload(payload json.RawMessage) (EscalationSnapshot, error) {
	var value struct {
		EscalationID            UUIDv7            `json:"escalation_id"`
		SubjectKind             AggregateKind     `json:"subject_kind"`
		SubjectID               UUIDv7            `json:"subject_id"`
		SubjectLifecycleEpoch   uint64            `json:"subject_lifecycle_epoch"`
		ExpectedSubjectRevision uint64            `json:"expected_subject_revision"`
		Trigger                 EscalationTrigger `json:"trigger"`
		ConditionDigest         Digest            `json:"triggering_condition_digest"`
		Adjudicator             PrincipalRef      `json:"adjudicator"`
		TimeoutPolicy           PrincipalRef      `json:"timeout_policy"`
		ResolutionOwnerFQN      ActorFQN          `json:"resolution_owner_fqn"`
		DeadlineAt              time.Time         `json:"deadline_at"`
		ResolutionRoundLimit    uint64            `json:"resolution_round_limit"`
		RouteLimit              uint64            `json:"route_limit"`
		PolicyRevision          uint64            `json:"escalation_policy_revision"`
		CausalPathEventIDs      []UUIDv7          `json:"causal_path_event_ids"`
		UnresolvedQuestion      string            `json:"unresolved_question"`
		EvidenceIDs             []UUIDv7          `json:"evidence_ids"`
	}
	if err := json.Unmarshal(payload, &value); err != nil {
		return EscalationSnapshot{}, err
	}
	snapshot := EscalationSnapshot{
		EscalationID: value.EscalationID, Revision: 1, State: EscalationOpen,
		Subject: AggregateRef{Kind: value.SubjectKind, ID: value.SubjectID}, SubjectLifecycleEpoch: value.SubjectLifecycleEpoch,
		ExpectedSubjectRevision: value.ExpectedSubjectRevision, Trigger: value.Trigger, ConditionDigest: value.ConditionDigest,
		Adjudicator: value.Adjudicator, TimeoutPolicy: value.TimeoutPolicy, ResolutionOwnerFQN: value.ResolutionOwnerFQN,
		DeadlineAt: value.DeadlineAt, ResolutionRoundLimit: value.ResolutionRoundLimit, RouteLimit: value.RouteLimit,
		PolicyRevision: value.PolicyRevision, CausalPathEventIDs: append([]UUIDv7(nil), value.CausalPathEventIDs...),
		UnresolvedQuestion: value.UnresolvedQuestion, EvidenceIDs: append([]UUIDv7(nil), value.EvidenceIDs...),
	}
	if !snapshot.Valid() {
		return EscalationSnapshot{}, errors.New("invalid escalation opening")
	}
	return snapshot, nil
}

func EscalationTransitionFromResolvePayload(payload json.RawMessage) (EscalationTransition, error) {
	var value struct {
		EscalationID          UUIDv7               `json:"escalation_id"`
		SubjectKind           AggregateKind        `json:"subject_kind"`
		SubjectID             UUIDv7               `json:"subject_id"`
		SubjectLifecycleEpoch uint64               `json:"subject_lifecycle_epoch"`
		ExpectedRevision      uint64               `json:"expected_escalation_revision"`
		SourceRole            EscalationSourceRole `json:"source_role"`
		Round                 uint64               `json:"round"`
		Outcome               EscalationOutcome    `json:"outcome"`
		Reasons               []string             `json:"reasons"`
		EvidenceIDs           []UUIDv7             `json:"evidence_ids"`
		DecidedAt             time.Time            `json:"decided_at"`
	}
	if err := json.Unmarshal(payload, &value); err != nil {
		return EscalationTransition{}, err
	}
	transition := EscalationTransition{
		Action: EscalationActionResolve, EscalationID: value.EscalationID,
		Subject: AggregateRef{Kind: value.SubjectKind, ID: value.SubjectID}, SubjectLifecycleEpoch: value.SubjectLifecycleEpoch,
		ExpectedRevision: value.ExpectedRevision, SourceRole: value.SourceRole, Round: value.Round,
		Outcome: value.Outcome, Reasons: append([]string(nil), value.Reasons...), EvidenceIDs: append([]UUIDv7(nil), value.EvidenceIDs...),
		DecidedAt: value.DecidedAt,
	}
	if !transition.EscalationID.Valid() || !transition.Subject.Valid() || (transition.Subject.Kind != AggregateStory && transition.Subject.Kind != AggregateTask) || transition.SubjectLifecycleEpoch == 0 || transition.ExpectedRevision == 0 || (transition.SourceRole != EscalationSourceAdjudicator && transition.SourceRole != EscalationSourcePolicyTimeout) || transition.Round == 0 || transition.Round > 1000 || !transition.Outcome.Valid() || len(transition.Reasons) == 0 || len(transition.Reasons) > 64 || !uniqueNonemptyStrings(transition.Reasons) || len(transition.EvidenceIDs) == 0 || len(transition.EvidenceIDs) > 64 || !uniqueValidUUIDs(transition.EvidenceIDs) || transition.DecidedAt.IsZero() {
		return EscalationTransition{}, errors.New("invalid escalation resolution")
	}
	return transition, nil
}

func ApplyEscalationResolution(snapshot EscalationSnapshot, authority PrincipalRef, eventID UUIDv7, committedAt time.Time, payload json.RawMessage) (EscalationSnapshot, bool) {
	transition, err := EscalationTransitionFromResolvePayload(payload)
	if err != nil || !snapshot.Valid() || snapshot.State != EscalationOpen || transition.EscalationID != snapshot.EscalationID || transition.Subject != snapshot.Subject || transition.SubjectLifecycleEpoch != snapshot.SubjectLifecycleEpoch || transition.ExpectedRevision != snapshot.Revision || !eventID.Valid() || committedAt.IsZero() {
		return EscalationSnapshot{}, false
	}
	transition.Authority = authority
	transition.DecidedAt = committedAt
	decision := EvaluateEscalation(snapshot, transition)
	if !decision.Accepted {
		return EscalationSnapshot{}, false
	}
	next := snapshot.Clone()
	next.Revision++
	next.State = EscalationTerminal
	next.TerminalOutcome = transition.Outcome
	next.ResolutionEventID = eventID
	return next, next.Valid()
}

func uniqueValidUUIDs(values []UUIDv7) bool {
	seen := make(map[UUIDv7]struct{}, len(values))
	for _, value := range values {
		if !value.Valid() {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func uniqueNonemptyStrings(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" || len(value) > 4096 {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}
