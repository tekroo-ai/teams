package kernel

import (
	"encoding/json"
	"errors"
	"sort"
	"time"
)

type ContinuityControlState string

const (
	ContinuityActive      ContinuityControlState = "ACTIVE"
	ContinuityQuiescing   ContinuityControlState = "QUIESCING"
	ContinuitySuspended   ContinuityControlState = "SUSPENDED"
	ContinuityReconciling ContinuityControlState = "RECONCILING"
)

type TeamContinuitySnapshot struct {
	Revision                 uint64                 `json:"revision" bson:"revision"`
	OperatingPosture         string                 `json:"operating_posture" bson:"operating_posture"`
	ControlState             ContinuityControlState `json:"control_state" bson:"control_state"`
	PowerEpoch               uint64                 `json:"power_epoch" bson:"power_epoch"`
	AdmissionOpen            bool                   `json:"admission_open" bson:"admission_open"`
	ContinuityPolicyRevision uint64                 `json:"continuity_policy_revision" bson:"continuity_policy_revision"`
	ContinuityPolicyDigest   Digest                 `json:"continuity_policy_digest" bson:"continuity_policy_digest"`
	HealthRequirementDigest  Digest                 `json:"health_requirement_digest" bson:"health_requirement_digest"`
	InFlightExecutionIDs     []UUIDv7               `json:"in_flight_execution_ids" bson:"in_flight_execution_ids"`
	UnresolvedExecutionIDs   []UUIDv7               `json:"unresolved_execution_ids" bson:"unresolved_execution_ids"`
	LastTransitionEventID    UUIDv7                 `json:"last_transition_event_id" bson:"last_transition_event_id"`
}

type SuspensionDisposition string

const (
	CancelOnSuspend     SuspensionDisposition = "CANCEL_ON_SUSPEND"
	CompleteQuarantined SuspensionDisposition = "COMPLETE_AND_QUARANTINE"
	DetachAndReconcile  SuspensionDisposition = "DETACH_AND_RECONCILE"
)

type SuspensionOutcome string

const (
	SuspensionCancelled        SuspensionOutcome = "CANCELLED"
	SuspensionTerminalCaptured SuspensionOutcome = "TERMINAL_CAPTURED"
	SuspensionDetached         SuspensionOutcome = "DETACHED"
	SuspensionUnknown          SuspensionOutcome = "UNKNOWN"
)

type SuspensionExecutionRecord struct {
	ExecutionID           UUIDv7                `json:"execution_id"`
	ActorFQN              ActorFQN              `json:"actor_fqn"`
	ExecutionFencingEpoch uint64                `json:"execution_fencing_epoch"`
	ProviderProfileDigest Digest                `json:"provider_profile_digest"`
	Disposition           SuspensionDisposition `json:"disposition"`
	Outcome               SuspensionOutcome     `json:"outcome"`
	ResultEvidenceID      *UUIDv7               `json:"result_evidence_id"`
	EvidenceIDs           []UUIDv7              `json:"evidence_ids"`
}

func (record SuspensionExecutionRecord) Valid() bool {
	if !record.ExecutionID.Valid() || !record.ActorFQN.Valid() || record.ExecutionFencingEpoch == 0 || !record.ProviderProfileDigest.Valid() || !validUniqueUUIDs(record.EvidenceIDs, 1, 64) || record.ResultEvidenceID != nil && !record.ResultEvidenceID.Valid() {
		return false
	}
	switch record.Disposition {
	case CancelOnSuspend:
		return record.Outcome == SuspensionCancelled || record.Outcome == SuspensionUnknown
	case CompleteQuarantined:
		return record.Outcome == SuspensionTerminalCaptured || record.Outcome == SuspensionUnknown
	case DetachAndReconcile:
		return record.Outcome == SuspensionDetached || record.Outcome == SuspensionUnknown
	default:
		return false
	}
}

type ReconciliationOutcome string

const (
	ReconciliationBlocked          ReconciliationOutcome = "BLOCKED"
	ReconciliationCancelled        ReconciliationOutcome = "CONFIRMED_CANCELLED"
	ReconciliationHumanRequired    ReconciliationOutcome = "HUMAN_REQUIRED"
	ReconciliationRejectedLate     ReconciliationOutcome = "REJECTED_LATE_RESULT"
	ReconciliationSuperseded       ReconciliationOutcome = "SUPERSEDED"
	ReconciliationEvidenceCaptured ReconciliationOutcome = "TERMINAL_EVIDENCE_CAPTURED"
)

type ExecutionReconciliationRecord struct {
	ExecutionID              UUIDv7                `json:"execution_id"`
	Outcome                  ReconciliationOutcome `json:"outcome"`
	AuthoritativeStateDigest Digest                `json:"authoritative_state_digest"`
	EvidenceIDs              []UUIDv7              `json:"evidence_ids"`
}

func (record ExecutionReconciliationRecord) Valid() bool {
	if !record.ExecutionID.Valid() || !record.AuthoritativeStateDigest.Valid() || !validUniqueUUIDs(record.EvidenceIDs, 1, 64) {
		return false
	}
	switch record.Outcome {
	case ReconciliationBlocked, ReconciliationCancelled, ReconciliationHumanRequired, ReconciliationRejectedLate, ReconciliationSuperseded, ReconciliationEvidenceCaptured:
		return true
	default:
		return false
	}
}

func (snapshot TeamContinuitySnapshot) Clone() TeamContinuitySnapshot {
	copy := snapshot
	copy.InFlightExecutionIDs = append([]UUIDv7(nil), snapshot.InFlightExecutionIDs...)
	copy.UnresolvedExecutionIDs = append([]UUIDv7(nil), snapshot.UnresolvedExecutionIDs...)
	return copy
}

func (snapshot TeamContinuitySnapshot) Valid() bool {
	if snapshot.Revision == 0 || snapshot.PowerEpoch == 0 || snapshot.ContinuityPolicyRevision == 0 || !snapshot.ContinuityPolicyDigest.Valid() || !snapshot.HealthRequirementDigest.Valid() || !snapshot.LastTransitionEventID.Valid() || !validUniqueUUIDs(snapshot.InFlightExecutionIDs, 0, 1024) || !validUniqueUUIDs(snapshot.UnresolvedExecutionIDs, 0, 1024) {
		return false
	}
	if snapshot.OperatingPosture != "CONTINUOUS" && snapshot.OperatingPosture != "PLANNED_SUSPEND_CAPABLE" {
		return false
	}
	switch snapshot.ControlState {
	case ContinuityActive:
		return snapshot.AdmissionOpen && len(snapshot.UnresolvedExecutionIDs) == 0
	case ContinuityQuiescing, ContinuitySuspended, ContinuityReconciling:
		return !snapshot.AdmissionOpen
	default:
		return false
	}
}

func TeamContinuityFromConfigurePayload(payload json.RawMessage, eventID UUIDv7, revision uint64) (TeamContinuitySnapshot, error) {
	var value struct {
		ExpectedSystemRevision   uint64 `json:"expected_system_revision"`
		InitialPowerEpoch        uint64 `json:"initial_power_epoch"`
		OperatingPosture         string `json:"operating_posture"`
		ContinuityPolicyRevision uint64 `json:"continuity_policy_revision"`
		ContinuityPolicyDigest   Digest `json:"continuity_policy_digest"`
		HealthRequirementDigest  Digest `json:"health_requirement_digest"`
	}
	if json.Unmarshal(payload, &value) != nil || value.ExpectedSystemRevision+1 != revision {
		return TeamContinuitySnapshot{}, errors.New("invalid continuity configuration")
	}
	snapshot := TeamContinuitySnapshot{Revision: revision, OperatingPosture: value.OperatingPosture, ControlState: ContinuityActive, PowerEpoch: value.InitialPowerEpoch, AdmissionOpen: true, ContinuityPolicyRevision: value.ContinuityPolicyRevision, ContinuityPolicyDigest: value.ContinuityPolicyDigest, HealthRequirementDigest: value.HealthRequirementDigest, LastTransitionEventID: eventID}
	if !snapshot.Valid() {
		return TeamContinuitySnapshot{}, errors.New("invalid continuity configuration")
	}
	return snapshot, nil
}

func ApplyContinuityEvent(snapshot TeamContinuitySnapshot, event DomainEvent) (TeamContinuitySnapshot, bool) {
	if !snapshot.Valid() || event.Aggregate.Kind != AggregateSystem || event.AggregateRevision != snapshot.Revision+1 {
		return snapshot, false
	}
	next := snapshot.Clone()
	switch event.EventType {
	case "tekroo.event.system.quiescence-requested":
		var value struct {
			ExpectedSystemRevision uint64       `json:"expected_system_revision"`
			ExpectedPowerEpoch     uint64       `json:"expected_power_epoch"`
			NextPowerEpoch         uint64       `json:"next_power_epoch"`
			InFlightExecutionIDs   []UUIDv7     `json:"in_flight_execution_ids"`
			Reason                 string       `json:"reason"`
			RequestedBy            PrincipalRef `json:"requested_by"`
			DeadlineAt             time.Time    `json:"deadline_at"`
			DispositionPlanDigest  Digest       `json:"disposition_plan_digest"`
			EvidenceIDs            []UUIDv7     `json:"evidence_ids"`
		}
		if json.Unmarshal(event.Payload, &value) != nil || snapshot.ControlState != ContinuityActive || value.ExpectedSystemRevision != snapshot.Revision || value.ExpectedPowerEpoch != snapshot.PowerEpoch || value.NextPowerEpoch != snapshot.PowerEpoch+1 || !validUniqueUUIDs(value.InFlightExecutionIDs, 0, 1024) || !validQuiescenceReason(value.Reason) || !value.RequestedBy.Valid() || value.DeadlineAt.IsZero() || !value.DispositionPlanDigest.Valid() || !validUniqueUUIDs(value.EvidenceIDs, 1, 64) {
			return snapshot, false
		}
		next.ControlState = ContinuityQuiescing
		next.PowerEpoch = value.NextPowerEpoch
		next.AdmissionOpen = false
		next.InFlightExecutionIDs = canonicalUUIDs(value.InFlightExecutionIDs)
		next.UnresolvedExecutionIDs = nil
	case "tekroo.event.system.suspended":
		var value struct {
			ExpectedSystemRevision   uint64                      `json:"expected_system_revision"`
			ExpectedPowerEpoch       uint64                      `json:"expected_power_epoch"`
			AllRecorded              bool                        `json:"all_in_flight_executions_recorded"`
			QuiescenceRequestEventID UUIDv7                      `json:"quiescence_request_event_id"`
			ExecutionRecords         []SuspensionExecutionRecord `json:"execution_records"`
			UnresolvedExecutionIDs   []UUIDv7                    `json:"unresolved_execution_ids"`
			RecordedBy               PrincipalRef                `json:"recorded_by"`
			EvidenceIDs              []UUIDv7                    `json:"evidence_ids"`
		}
		if json.Unmarshal(event.Payload, &value) != nil || snapshot.ControlState != ContinuityQuiescing || value.ExpectedSystemRevision != snapshot.Revision || value.ExpectedPowerEpoch != snapshot.PowerEpoch || value.QuiescenceRequestEventID != snapshot.LastTransitionEventID || !value.AllRecorded || !validUniqueUUIDs(value.UnresolvedExecutionIDs, 0, 1024) || !value.RecordedBy.Valid() || !validUniqueUUIDs(value.EvidenceIDs, 1, 64) {
			return snapshot, false
		}
		recorded := make([]UUIDv7, 0, len(value.ExecutionRecords))
		unresolved := make([]UUIDv7, 0, len(value.ExecutionRecords))
		for _, record := range value.ExecutionRecords {
			if !record.Valid() {
				return snapshot, false
			}
			recorded = append(recorded, record.ExecutionID)
			if record.Outcome == SuspensionDetached || record.Outcome == SuspensionUnknown {
				unresolved = append(unresolved, record.ExecutionID)
			}
		}
		if !sameUUIDSet(recorded, snapshot.InFlightExecutionIDs) || !sameUUIDSet(value.UnresolvedExecutionIDs, unresolved) {
			return snapshot, false
		}
		next.ControlState = ContinuitySuspended
		next.UnresolvedExecutionIDs = canonicalUUIDs(value.UnresolvedExecutionIDs)
	case "tekroo.event.system.reconciliation-started":
		var value struct {
			ExpectedSystemRevision uint64       `json:"expected_system_revision"`
			ExpectedPowerEpoch     uint64       `json:"expected_power_epoch"`
			UnresolvedExecutionIDs []UUIDv7     `json:"unresolved_execution_ids"`
			Trigger                string       `json:"trigger"`
			ProviderSnapshotDigest Digest       `json:"provider_snapshot_digest"`
			InitiatedBy            PrincipalRef `json:"initiated_by"`
			EvidenceIDs            []UUIDv7     `json:"evidence_ids"`
		}
		if json.Unmarshal(event.Payload, &value) != nil || snapshot.ControlState != ContinuitySuspended || value.ExpectedSystemRevision != snapshot.Revision || value.ExpectedPowerEpoch != snapshot.PowerEpoch || !sameUUIDSet(value.UnresolvedExecutionIDs, snapshot.UnresolvedExecutionIDs) || value.Trigger != "HOST_RECOVERY" && value.Trigger != "OPERATOR_REQUEST" && value.Trigger != "WAKE" || !value.ProviderSnapshotDigest.Valid() || !value.InitiatedBy.Valid() || !validUniqueUUIDs(value.EvidenceIDs, 1, 64) {
			return snapshot, false
		}
		next.ControlState = ContinuityReconciling
	case "tekroo.event.system.unexpected-outage-recorded":
		var value struct {
			ExpectedSystemRevision    uint64                 `json:"expected_system_revision"`
			ObservedPowerEpoch        uint64                 `json:"observed_power_epoch"`
			NextPowerEpoch            uint64                 `json:"next_power_epoch"`
			PreviousControlState      ContinuityControlState `json:"previous_control_state"`
			KnownInFlightExecutionIDs []UUIDv7               `json:"known_in_flight_execution_ids"`
			DetectedAt                time.Time              `json:"detected_at"`
			OutageKind                string                 `json:"outage_kind"`
			LastDurableEventID        *UUIDv7                `json:"last_durable_event_id"`
			DetectedBy                PrincipalRef           `json:"detected_by"`
			EvidenceIDs               []UUIDv7               `json:"evidence_ids"`
		}
		if json.Unmarshal(event.Payload, &value) != nil || value.ExpectedSystemRevision != snapshot.Revision || value.ObservedPowerEpoch != snapshot.PowerEpoch || value.NextPowerEpoch != snapshot.PowerEpoch+1 || value.PreviousControlState != snapshot.ControlState || !validUniqueUUIDs(value.KnownInFlightExecutionIDs, 0, 1024) || value.DetectedAt.IsZero() || !validOutageKind(value.OutageKind) || value.LastDurableEventID != nil && !value.LastDurableEventID.Valid() || !value.DetectedBy.Valid() || !validUniqueUUIDs(value.EvidenceIDs, 1, 64) {
			return snapshot, false
		}
		next.ControlState = ContinuityReconciling
		next.PowerEpoch = value.NextPowerEpoch
		next.AdmissionOpen = false
		next.InFlightExecutionIDs = canonicalUUIDs(value.KnownInFlightExecutionIDs)
		next.UnresolvedExecutionIDs = canonicalUUIDs(value.KnownInFlightExecutionIDs)
	case "tekroo.event.system.resumed":
		var value struct {
			ExpectedSystemRevision  uint64                          `json:"expected_system_revision"`
			ExpectedPowerEpoch      uint64                          `json:"expected_power_epoch"`
			ReconciliationRecords   []ExecutionReconciliationRecord `json:"reconciliation_records"`
			RequiredServicesHealthy bool                            `json:"required_services_healthy"`
			OutboxReconciled        bool                            `json:"outbox_reconciled"`
			ChangeStreamReconciled  bool                            `json:"change_stream_reconciled"`
			AdmissionReopen         bool                            `json:"admission_reopen"`
			ResumedBy               PrincipalRef                    `json:"resumed_by"`
			EvidenceIDs             []UUIDv7                        `json:"evidence_ids"`
		}
		if json.Unmarshal(event.Payload, &value) != nil || snapshot.ControlState != ContinuityReconciling || value.ExpectedSystemRevision != snapshot.Revision || value.ExpectedPowerEpoch != snapshot.PowerEpoch || !value.RequiredServicesHealthy || !value.OutboxReconciled || !value.ChangeStreamReconciled || !value.AdmissionReopen || !value.ResumedBy.Valid() || !validUniqueUUIDs(value.EvidenceIDs, 1, 64) {
			return snapshot, false
		}
		reconciled := make([]UUIDv7, 0, len(value.ReconciliationRecords))
		for _, record := range value.ReconciliationRecords {
			if !record.Valid() {
				return snapshot, false
			}
			reconciled = append(reconciled, record.ExecutionID)
		}
		if !sameUUIDSet(reconciled, snapshot.UnresolvedExecutionIDs) {
			return snapshot, false
		}
		next.ControlState = ContinuityActive
		next.AdmissionOpen = true
		next.InFlightExecutionIDs = nil
		next.UnresolvedExecutionIDs = nil
	default:
		return snapshot, false
	}
	next.Revision++
	next.LastTransitionEventID = event.EventID
	return next, next.Valid()
}

func validQuiescenceReason(value string) bool {
	return value == "HOST_MAINTENANCE" || value == "LID_CLOSE" || value == "OPERATOR_REQUEST" || value == "POWER_EVENT" || value == "ENERGY_POLICY"
}

func validOutageKind(value string) bool {
	return value == "HOST_SLEEP" || value == "HOST_RESTART" || value == "NETWORK_PARTITION" || value == "PROCESS_LOSS" || value == "UNKNOWN"
}

func canonicalUUIDs(values []UUIDv7) []UUIDv7 {
	copy := append([]UUIDv7(nil), values...)
	sort.Slice(copy, func(i, j int) bool { return copy[i] < copy[j] })
	return copy
}

func sameUUIDSet(left, right []UUIDv7) bool {
	left = canonicalUUIDs(left)
	right = canonicalUUIDs(right)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func uuidSubset(values, universe []UUIDv7) bool {
	allowed := make(map[UUIDv7]bool, len(universe))
	for _, value := range universe {
		allowed[value] = true
	}
	for _, value := range values {
		if !allowed[value] {
			return false
		}
	}
	return true
}

type ContinuityAction string

const (
	ContinuityDispatch     ContinuityAction = "DISPATCH"
	ContinuityAcceptResult ContinuityAction = "ACCEPT_RESULT"
)

func EvaluateContinuityAdmission(snapshot TeamContinuitySnapshot, action ContinuityAction, powerEpoch uint64) PolicyResult {
	if powerEpoch != snapshot.PowerEpoch {
		if action == ContinuityAcceptResult {
			return PolicyResult{Reason: "LATE_RESULT_QUARANTINED"}
		}
		return PolicyResult{Reason: "STALE_POWER_EPOCH"}
	}
	if !snapshot.AdmissionOpen {
		return PolicyResult{Reason: "ADMISSION_CLOSED"}
	}
	return PolicyResult{Accepted: true, Reason: "ACCEPTED"}
}
