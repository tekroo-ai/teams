package eventexport

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/tekroo-ai/teams/adapters/protocol"
	"github.com/tekroo-ai/teams/kernel"
)

const (
	ProtocolVersion      = "1.0.0"
	ContractIdentity     = "tekroo.event-export.contracts/0.1.0"
	ContractManifestSHA  = "4aa87278b6812f967331bef98b2c7e63585b6742b39d0b64c0ff693632aa9731"
	SourceManifestSHA256 = "e2b9b5224a860a3eaa07451cf48fb5ac16a440b22b8dd592ff1662c1cff67f16"
	MaximumLimit         = 1000
	MaximumWait          = 30 * time.Second
)

var (
	ErrInvalidRequest = errors.New("invalid event export request")
	ErrUnauthorized   = errors.New("event export unauthorized")
	ErrInvalidCursor  = errors.New("invalid event export cursor")
	ErrResyncRequired = errors.New("event export resynchronization required")
	ErrCaughtUp       = errors.New("event export caught up")
	ErrCorruptEvent   = errors.New("corrupt persisted event")
)

type Status string

const (
	StatusRecords        Status = "RECORDS"
	StatusCaughtUp       Status = "CAUGHT_UP"
	StatusResyncRequired Status = "RESYNC_REQUIRED"
)

type Request struct {
	ProtocolVersion string        `json:"protocol_version"`
	RequestID       kernel.UUIDv7 `json:"request_id"`
	SourceID        string        `json:"source_id"`
	Cursor          string        `json:"cursor,omitempty"`
	Limit           int           `json:"limit"`
	WaitMillis      uint32        `json:"wait_millis"`
}

type Record struct {
	EventID          kernel.UUIDv7 `json:"event_id"`
	ContractManifest string        `json:"contract_manifest"`
	EventType        string        `json:"event_type"`
	EventVersion     string        `json:"event_version"`
	EventBytesBase64 string        `json:"event_bytes_base64"`
	EventBytesSHA256 kernel.Digest `json:"event_bytes_sha256"`
	EventBytesLength int           `json:"event_bytes_length"`
	Cursor           string        `json:"cursor"`
}

type Response struct {
	ProtocolVersion              string        `json:"protocol_version"`
	RequestID                    kernel.UUIDv7 `json:"request_id"`
	SourceID                     string        `json:"source_id"`
	SourceContractManifest       string        `json:"source_contract_manifest"`
	SourceContractManifestSHA256 kernel.Digest `json:"source_contract_manifest_sha256"`
	Status                       Status        `json:"status"`
	Records                      []Record      `json:"records"`
	NextCursor                   string        `json:"next_cursor"`
	ReasonCode                   string        `json:"reason_code,omitempty"`
}

type FailureCode string

const (
	FailureInvalidRequest  FailureCode = "INVALID_REQUEST"
	FailureUnauthenticated FailureCode = "UNAUTHENTICATED"
	FailureUnauthorized    FailureCode = "UNAUTHORIZED"
	FailureRateLimited     FailureCode = "RATE_LIMITED"
	FailureDeadline        FailureCode = "DEADLINE_EXCEEDED"
	FailureSource          FailureCode = "SOURCE_UNAVAILABLE"
	FailureInternal        FailureCode = "INTERNAL_ERROR"
)

type Failure struct {
	ProtocolVersion string        `json:"protocol_version"`
	RequestID       kernel.UUIDv7 `json:"request_id"`
	Code            FailureCode   `json:"code"`
	Retryable       bool          `json:"retryable"`
}

type RawRecord struct {
	Bytes    []byte
	Position []byte
}

type Feed interface {
	Next(context.Context) (RawRecord, error)
	Position() []byte
	Close(context.Context) error
}

type Source interface {
	Open(context.Context, string, []byte) (Feed, error)
}

type Authorizer interface {
	Allow(protocol.AuthenticatedContext, string) bool
}

type AuthorizerFunc func(protocol.AuthenticatedContext, string) bool

func (function AuthorizerFunc) Allow(identity protocol.AuthenticatedContext, sourceID string) bool {
	return function(identity, sourceID)
}

type TelemetryRecord struct {
	Status       string
	SourceID     string
	Principal    kernel.PrincipalRef
	RecordCount  int
	Duration     time.Duration
	CursorDigest kernel.Digest
}

type Telemetry interface {
	Observe(TelemetryRecord)
}

type TelemetryFunc func(TelemetryRecord)

func (function TelemetryFunc) Observe(record TelemetryRecord) { function(record) }

func DecodeDomainEvent(bytes []byte) (kernel.DomainEvent, error) {
	var event kernel.DomainEvent
	if len(bytes) == 0 || !json.Valid(bytes) || json.Unmarshal(bytes, &event) != nil || event.ContractManifest != kernel.ContractIdentity || !event.EventID.Valid() || event.EventType == "" || event.EventVersion == "" || !event.Aggregate.Valid() || event.AggregateRevision == 0 || event.LifecycleEpoch == 0 || !event.CommandID.Valid() || !event.Authority.Valid() || event.CommittedAt.IsZero() || !event.ProvenanceDigest.Valid() || !json.Valid(event.Payload) {
		return kernel.DomainEvent{}, ErrCorruptEvent
	}
	return event, nil
}
