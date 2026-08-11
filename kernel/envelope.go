package kernel

import (
	"encoding/json"
	"time"
)

type ExpectedRevision struct {
	MustNotExist bool   `json:"must_not_exist"`
	Revision     uint64 `json:"revision"`
}

func NewExpectedRevision(revision uint64) ExpectedRevision {
	return ExpectedRevision{Revision: revision}
}

func MustNotExist() ExpectedRevision { return ExpectedRevision{MustNotExist: true} }

type AggregatePrecondition struct {
	Aggregate AggregateRef     `json:"aggregate"`
	Expected  ExpectedRevision `json:"expected"`
}

type KernelCommand struct {
	ContractManifest          string
	CommandID                 UUIDv7
	CommandType               string
	CommandVersion            string
	Target                    AggregateRef
	Authority                 PrincipalRef
	ActorFQN                  *ActorFQN
	Execution                 *ExecutionTuple
	ExpectedRevision          ExpectedRevision
	Preconditions             []AggregatePrecondition
	ExpectedLifecycleEpoch    *uint64
	ExpectedPolicyRevision    uint64
	ExpectedCatalogueRevision uint64
	IdempotencyKey            string
	CorrelationID             UUIDv7
	Causation                 []DagParent
	IssuedAt                  *time.Time
	Payload                   json.RawMessage
	EvidenceRefs              []EvidenceRef
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
	Principal         PrincipalRef `json:"principal"`
	Allowed           bool         `json:"allowed"`
	CanReadTarget     bool         `json:"can_read_target"`
	Reason            string       `json:"reason"`
	PolicyDigest      Digest       `json:"policy_digest"`
	PolicyRevision    uint64       `json:"policy_revision"`
	GrantDigests      []Digest     `json:"grant_digests"`
	DelegationDigests []Digest     `json:"delegation_digests"`
}

type OutboxIntent struct {
	IntentID UUIDv7
	EventID  UUIDv7
	Kind     string
}

type Decision struct {
	CommandFingerprint Digest
	IdempotencyScope   Digest
	NextState          *AggregateState
	Events             []DomainEvent
	Receipt            CommandReceipt
	Authority          AuthorityDecision
	Outbox             []OutboxIntent
	Guards             DecisionGuards
	Provenance         DecisionProvenance
	AttemptBudget      *AttemptBudgetDecision
}

type DecisionGuards struct {
	Executions       map[ActorFQN]ExecutionTuple
	AbsentExecutions []ActorFQN
	ParentIDs        []UUIDv7
	EvidenceRefs     []EvidenceRef
	Preconditions    []AggregatePrecondition
	PolicyDigest     Digest
	PolicyRevision   uint64
	AbsentReviewKeys []CompletionReviewKey
}

type Snapshot struct {
	Exists            bool
	Revision          uint64
	State             *AggregateState
	AcceptedEvents    map[UUIDv7]AcceptedEvent
	CurrentExecutions map[ActorFQN]ExecutionTuple
	Evidence          map[UUIDv7]EvidenceMetadata
	Related           map[AggregateRef]RelatedSnapshot
	Authorization     AuthorizationPolicy
	OpenReviews       map[CompletionReviewKey]AggregateRef
	Reviews           map[AggregateRef]CompletionReviewSnapshot
	AttemptBudgets    map[AttemptBudgetKey]AttemptBudgetSnapshot
}

type RelatedSnapshot struct {
	Exists   bool
	Revision uint64
	State    *AggregateState
}

type AcceptedEvent struct {
	EventType     string
	Quarantined   bool
	Qualification string
}

type EvidenceMetadata struct {
	SHA256    Digest
	Available bool
}

type DecisionContext struct {
	ReceivedAt       time.Time
	DecidedAt        time.Time
	EventID          UUIDv7
	IntentID         UUIDv7
	Provenance       ProvenanceBasis
	ProvenanceDigest Digest
}
