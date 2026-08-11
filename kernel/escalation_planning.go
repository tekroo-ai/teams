package kernel

import (
	"sort"
	"time"
)

type EscalationPlanStatus string

const (
	EscalationPlanReady    EscalationPlanStatus = "READY"
	EscalationPlanNoEffect EscalationPlanStatus = "NO_EFFECT"
)

type EscalationOpeningInput struct {
	EscalationID            UUIDv7
	Subject                 AggregateState
	Trigger                 EscalationTrigger
	ConditionDigest         Digest
	Adjudicator             PrincipalRef
	TimeoutPolicy           PrincipalRef
	ResolutionOwnerFQN      ActorFQN
	OpenedAt                time.Time
	DeadlineAt              time.Time
	ResolutionRoundLimit    uint64
	PolicyRevision          uint64
	CausalPathEventIDs      []UUIDv7
	UnresolvedQuestion      string
	EvidenceRefs            []EvidenceRef
	EquivalentEscalationRef *AggregateRef
}

type EscalationOpeningDecision struct {
	Status       EscalationPlanStatus
	Reason       string
	Escalation   EscalationSnapshot
	EvidenceRefs []EvidenceRef
}

func PlanEscalationOpening(input EscalationOpeningInput) EscalationOpeningDecision {
	noEffect := func(reason string) EscalationOpeningDecision {
		return EscalationOpeningDecision{Status: EscalationPlanNoEffect, Reason: reason}
	}
	if input.EquivalentEscalationRef != nil {
		return noEffect("EQUIVALENT_ESCALATION_EXISTS")
	}
	if !input.EscalationID.Valid() || !input.Subject.ID.Valid() || (input.Subject.Kind != AggregateStory && input.Subject.Kind != AggregateTask) || input.Subject.Revision == 0 || input.Subject.LifecycleEpoch == 0 || terminalPhase(input.Subject.Phase) || !input.Trigger.Valid() || !input.ConditionDigest.Valid() || !input.Adjudicator.Valid() || input.TimeoutPolicy.Kind != PrincipalPolicy || !input.TimeoutPolicy.Valid() || !input.ResolutionOwnerFQN.Valid() || input.OpenedAt.IsZero() || !input.DeadlineAt.After(input.OpenedAt) || input.ResolutionRoundLimit == 0 || input.ResolutionRoundLimit > 1000 || input.PolicyRevision == 0 || len(input.CausalPathEventIDs) == 0 || len(input.CausalPathEventIDs) > 64 || !uniqueValidUUIDs(input.CausalPathEventIDs) || input.UnresolvedQuestion == "" || len(input.UnresolvedQuestion) > 4096 {
		return noEffect("INVALID_ESCALATION_OPENING")
	}
	evidence, ok := canonicalEvidenceRefs(input.EvidenceRefs)
	if !ok || len(evidence) == 0 {
		return noEffect("INVALID_ESCALATION_EVIDENCE")
	}
	evidenceIDs := make([]UUIDv7, len(evidence))
	for index, reference := range evidence {
		evidenceIDs[index] = reference.EvidenceID
	}
	escalation := EscalationSnapshot{
		EscalationID: input.EscalationID, Revision: 1, State: EscalationOpen,
		Subject: AggregateRef{Kind: input.Subject.Kind, ID: input.Subject.ID}, SubjectLifecycleEpoch: input.Subject.LifecycleEpoch,
		ExpectedSubjectRevision: input.Subject.Revision, Trigger: input.Trigger, ConditionDigest: input.ConditionDigest,
		Adjudicator: input.Adjudicator, TimeoutPolicy: input.TimeoutPolicy, ResolutionOwnerFQN: input.ResolutionOwnerFQN,
		DeadlineAt: input.DeadlineAt, ResolutionRoundLimit: input.ResolutionRoundLimit, RouteLimit: 1,
		PolicyRevision: input.PolicyRevision, CausalPathEventIDs: append([]UUIDv7(nil), input.CausalPathEventIDs...),
		UnresolvedQuestion: input.UnresolvedQuestion, EvidenceIDs: evidenceIDs,
	}
	if !escalation.Valid() {
		return noEffect("INVALID_ESCALATION_OPENING")
	}
	return EscalationOpeningDecision{Status: EscalationPlanReady, Reason: "READY", Escalation: escalation, EvidenceRefs: evidence}
}

type EscalationResolutionInput struct {
	Escalation   EscalationSnapshot
	Subject      AggregateState
	Authority    PrincipalRef
	SourceRole   EscalationSourceRole
	Round        uint64
	Outcome      EscalationOutcome
	Reasons      []string
	EvidenceRefs []EvidenceRef
	DecidedAt    time.Time
	Parents      []DagParent
}

type EscalationResolutionDecision struct {
	Status       EscalationPlanStatus
	Reason       string
	Escalation   EscalationSnapshot
	Subject      AggregateState
	Transition   EscalationTransition
	EvidenceRefs []EvidenceRef
	Parents      []DagParent
}

func PlanEscalationResolution(input EscalationResolutionInput) EscalationResolutionDecision {
	noEffect := func(reason string) EscalationResolutionDecision {
		return EscalationResolutionDecision{Status: EscalationPlanNoEffect, Reason: reason}
	}
	if !input.Escalation.Valid() || input.Escalation.State != EscalationOpen || !input.Escalation.OpeningEventID.Valid() || input.Subject.Kind != input.Escalation.Subject.Kind || input.Subject.ID != input.Escalation.Subject.ID || input.Subject.Revision == 0 || input.Subject.LifecycleEpoch != input.Escalation.SubjectLifecycleEpoch || terminalPhase(input.Subject.Phase) || !input.Authority.Valid() || input.DecidedAt.IsZero() || !input.Outcome.Valid() || len(input.Reasons) == 0 || len(input.Reasons) > 64 || !uniqueNonemptyStrings(input.Reasons) {
		return noEffect("INVALID_ESCALATION_RESOLUTION")
	}
	evidence, ok := canonicalEvidenceRefs(input.EvidenceRefs)
	if !ok || len(evidence) == 0 {
		return noEffect("INVALID_ESCALATION_EVIDENCE")
	}
	parents, ok := canonicalResolutionParents(input.Escalation.OpeningEventID, input.Parents)
	if !ok {
		return noEffect("INVALID_ESCALATION_CAUSATION")
	}
	evidenceIDs := make([]UUIDv7, len(evidence))
	for index, reference := range evidence {
		evidenceIDs[index] = reference.EvidenceID
	}
	reasons := append([]string(nil), input.Reasons...)
	sort.Strings(reasons)
	transition := EscalationTransition{
		Action: EscalationActionResolve, EscalationID: input.Escalation.EscalationID, Subject: input.Escalation.Subject,
		Authority: input.Authority, SourceRole: input.SourceRole, SubjectLifecycleEpoch: input.Subject.LifecycleEpoch,
		ExpectedRevision: input.Escalation.Revision, DecidedAt: input.DecidedAt, Round: input.Round,
		Outcome: input.Outcome, Reasons: reasons, EvidenceIDs: evidenceIDs,
	}
	result := EvaluateEscalation(input.Escalation, transition)
	if !result.Accepted {
		return noEffect(result.Reason)
	}
	return EscalationResolutionDecision{
		Status: EscalationPlanReady, Reason: "READY", Escalation: input.Escalation.Clone(), Subject: input.Subject.Clone(),
		Transition: transition, EvidenceRefs: evidence, Parents: parents,
	}
}

func canonicalEvidenceRefs(values []EvidenceRef) ([]EvidenceRef, bool) {
	if len(values) > 64 {
		return nil, false
	}
	result := append([]EvidenceRef(nil), values...)
	sort.Slice(result, func(i, j int) bool { return result[i].EvidenceID < result[j].EvidenceID })
	for index, value := range result {
		if !value.EvidenceID.Valid() || !value.SHA256.Valid() || (index > 0 && result[index-1].EvidenceID == value.EvidenceID) {
			return nil, false
		}
	}
	return result, true
}

func canonicalResolutionParents(openingEventID UUIDv7, values []DagParent) ([]DagParent, bool) {
	parents := append([]DagParent(nil), values...)
	parents = append(parents, DagParent{ParentEventID: openingEventID, EdgeKind: EdgeResponse})
	parents = canonicalParents(parents)
	if len(parents) > 64 {
		return nil, false
	}
	for index, parent := range parents {
		if !parent.ParentEventID.Valid() || !parent.EdgeKind.Valid() || (index > 0 && parents[index-1] == parent) {
			return nil, false
		}
	}
	return parents, true
}
