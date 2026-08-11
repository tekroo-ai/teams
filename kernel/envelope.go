package kernel

import (
	"encoding/json"
	"time"
)

type ExpectedRevision struct {
	MustNotExist bool
	Revision     uint64
}

func NewExpectedRevision(revision uint64) ExpectedRevision {
	return ExpectedRevision{Revision: revision}
}

func MustNotExist() ExpectedRevision { return ExpectedRevision{MustNotExist: true} }

type KernelCommand struct {
	ContractManifest string
	CommandID        UUIDv7
	CommandType      string
	CommandVersion   string
	Target           AggregateRef
	Authority        PrincipalRef
	ActorFQN         *ActorFQN
	Execution        *ExecutionTuple
	ExpectedRevision ExpectedRevision
	IdempotencyKey   string
	CorrelationID    UUIDv7
	Causation        []DagParent
	IssuedAt         *time.Time
	Payload          json.RawMessage
	EvidenceRefs     []EvidenceRef
}

type OutcomeCode string

const (
	OutcomeApplied                OutcomeCode = "APPLIED"
	OutcomeNoChange               OutcomeCode = "NO_CHANGE"
	OutcomeRejectedInvalid        OutcomeCode = "REJECTED_INVALID"
	OutcomeRejectedUnauthorized   OutcomeCode = "REJECTED_UNAUTHORIZED"
	OutcomeRejectedNotFound       OutcomeCode = "REJECTED_NOT_FOUND"
	OutcomeRejectedConflict       OutcomeCode = "REJECTED_CONFLICT"
	OutcomeRejectedStaleExecution OutcomeCode = "REJECTED_STALE_EXECUTION"
	OutcomeRejectedClosed         OutcomeCode = "REJECTED_CLOSED"
	OutcomeRejectedPolicy         OutcomeCode = "REJECTED_POLICY"
)

type CommandReceipt struct {
	ContractManifest  string       `json:"contract_manifest"`
	CommandID         UUIDv7       `json:"command_id"`
	CommandType       string       `json:"command_type"`
	Target            AggregateRef `json:"target"`
	OutcomeCode       OutcomeCode  `json:"outcome_code"`
	ReasonCode        string       `json:"reason_code"`
	StateChanged      bool         `json:"state_changed"`
	ResultingRevision *uint64      `json:"resulting_revision"`
	EventIDs          []UUIDv7     `json:"event_ids"`
	ReceivedAt        time.Time    `json:"received_at"`
	DecidedAt         time.Time    `json:"decided_at"`
	ProvenanceDigest  Digest       `json:"provenance_digest"`
}

type DomainEvent struct {
	ContractManifest  string          `json:"contract_manifest"`
	EventID           UUIDv7          `json:"event_id"`
	EventType         string          `json:"event_type"`
	EventVersion      string          `json:"event_version"`
	Aggregate         AggregateRef    `json:"aggregate"`
	AggregateRevision uint64          `json:"aggregate_revision"`
	LifecycleEpoch    uint64          `json:"lifecycle_epoch"`
	CommandID         UUIDv7          `json:"command_id"`
	Authority         PrincipalRef    `json:"authority"`
	ActorFQN          *ActorFQN       `json:"actor_fqn"`
	Execution         *ExecutionTuple `json:"execution"`
	Parents           []DagParent     `json:"parents"`
	CommittedAt       time.Time       `json:"committed_at"`
	Payload           json.RawMessage `json:"payload"`
	ProvenanceDigest  Digest          `json:"provenance_digest"`
}

type AuthorityDecision struct {
	Principal PrincipalRef
	Allowed   bool
	Reason    string
}

type OutboxIntent struct {
	IntentID UUIDv7
	EventID  UUIDv7
	Kind     string
}

type Decision struct {
	CommandFingerprint Digest
	NextState          *AggregateState
	Events             []DomainEvent
	Receipt            CommandReceipt
	Authority          AuthorityDecision
	Outbox             []OutboxIntent
}

type Snapshot struct {
	Exists   bool
	Revision uint64
	State    *AggregateState
}

type DecisionContext struct {
	ReceivedAt       time.Time
	DecidedAt        time.Time
	EventID          UUIDv7
	ProvenanceDigest Digest
}
