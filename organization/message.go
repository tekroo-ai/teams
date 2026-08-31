package organization

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

const (
	OrganizationalMessageSchemaVersion = "1.0.0"
	MaximumOrganizationalMessageBytes  = 1 << 20
	MaximumOrganizationalHops          = 64
)

var (
	ErrInvalidOrganizationalMessage  = errors.New("invalid organizational message")
	ErrOrganizationalMessageConflict = errors.New("organizational message conflict")
	ErrOrganizationalMessageNotFound = errors.New("organizational message not found")
	ErrStaleOrganizationalClaim      = errors.New("stale organizational message claim")
	ErrOrganizationalLoop            = errors.New("organizational message would violate finite DAG progression")
	messageTypePattern               = regexp.MustCompile(`^tekroo\.message\.[a-z0-9]+(?:[.-][a-z0-9]+)*$`)
)

type MessagePurpose string

const (
	PurposeRequest      MessagePurpose = "REQUEST"
	PurposeHandoff      MessagePurpose = "HANDOFF"
	PurposeEvidence     MessagePurpose = "EVIDENCE"
	PurposeResponse     MessagePurpose = "RESPONSE"
	PurposeNotification MessagePurpose = "NOTIFICATION"
)

func (purpose MessagePurpose) Valid() bool {
	switch purpose {
	case PurposeRequest, PurposeHandoff, PurposeEvidence, PurposeResponse, PurposeNotification:
		return true
	default:
		return false
	}
}

type MessageWorkLink struct {
	StoryID   *kernel.UUIDv7 `json:"story_id,omitempty"`
	TaskID    *kernel.UUIDv7 `json:"task_id,omitempty"`
	DAGNodeID kernel.UUIDv7  `json:"dag_node_id"`
}

func (link MessageWorkLink) Valid() bool {
	if !link.DAGNodeID.Valid() {
		return false
	}
	return (link.StoryID == nil || link.StoryID.Valid()) && (link.TaskID == nil || link.TaskID.Valid())
}

type MessageFlow struct {
	ThreadID        kernel.UUIDv7  `json:"thread_id"`
	StepID          kernel.UUIDv7  `json:"step_id"`
	ParentStepID    *kernel.UUIDv7 `json:"parent_step_id,omitempty"`
	Hop             uint32         `json:"hop"`
	MaximumHops     uint32         `json:"maximum_hops"`
	BudgetAccountID kernel.UUIDv7  `json:"budget_account_id"`
	LifecycleEpoch  uint64         `json:"lifecycle_epoch"`
	ScopeRevision   uint64         `json:"scope_revision"`
	ProgressDigest  kernel.Digest  `json:"progress_digest"`
}

func (flow MessageFlow) Valid() bool {
	if !flow.ThreadID.Valid() || !flow.StepID.Valid() || flow.Hop == 0 || flow.MaximumHops == 0 || flow.MaximumHops > MaximumOrganizationalHops || flow.Hop > flow.MaximumHops || !flow.BudgetAccountID.Valid() || flow.LifecycleEpoch == 0 || flow.ScopeRevision == 0 || !flow.ProgressDigest.Valid() {
		return false
	}
	if flow.Hop == 1 {
		return flow.ParentStepID == nil
	}
	return flow.ParentStepID != nil && flow.ParentStepID.Valid()
}

type OrganizationalMessage struct {
	SchemaVersion    string                `json:"schema_version"`
	ID               kernel.UUIDv7         `json:"id"`
	Type             string                `json:"type"`
	Purpose          MessagePurpose        `json:"purpose"`
	Sender           kernel.ActorFQN       `json:"sender"`
	SenderExecution  kernel.ExecutionTuple `json:"sender_execution"`
	Recipient        kernel.ActorFQN       `json:"recipient"`
	CausationID      *kernel.UUIDv7        `json:"causation_id,omitempty"`
	CorrelationID    kernel.UUIDv7         `json:"correlation_id"`
	FanoutID         *kernel.UUIDv7        `json:"fanout_id,omitempty"`
	Work             MessageWorkLink       `json:"work"`
	Flow             MessageFlow           `json:"flow"`
	Body             json.RawMessage       `json:"body"`
	CreatedAt        time.Time             `json:"created_at"`
	ExpiresAt        time.Time             `json:"expires_at"`
	ReaddressHistory []MessageReaddress    `json:"readdress_history,omitempty"`
}

type MessageReaddress struct {
	From kernel.ActorFQN `json:"from"`
	To   kernel.ActorFQN `json:"to"`
	By   kernel.ActorFQN `json:"by"`
	At   time.Time       `json:"at"`
}

func (message OrganizationalMessage) Validate() error {
	if message.SchemaVersion != OrganizationalMessageSchemaVersion || !message.ID.Valid() || !messageTypePattern.MatchString(message.Type) || !message.Purpose.Valid() || !message.Sender.Valid() || !message.SenderExecution.Valid() || !message.Recipient.Valid() || message.Sender == message.Recipient || !message.CorrelationID.Valid() || message.CorrelationID != message.Flow.ThreadID || message.FanoutID != nil && !message.FanoutID.Valid() || !message.Work.Valid() || !message.Flow.Valid() || message.Work.DAGNodeID != message.Flow.StepID || message.CreatedAt.IsZero() || !message.ExpiresAt.After(message.CreatedAt) || message.ExpiresAt.Sub(message.CreatedAt) > 30*24*time.Hour || len(message.Body) == 0 || len(message.Body) > MaximumOrganizationalMessageBytes {
		return ErrInvalidOrganizationalMessage
	}
	if message.Flow.Hop == 1 && message.CausationID != nil || message.Flow.Hop > 1 && (message.CausationID == nil || !message.CausationID.Valid()) {
		return ErrInvalidOrganizationalMessage
	}
	if len(message.ReaddressHistory) > 8 {
		return ErrInvalidOrganizationalMessage
	}
	for _, item := range message.ReaddressHistory {
		if !item.From.Valid() || !item.To.Valid() || item.From == item.To || item.By != message.Sender || item.At.Before(message.CreatedAt) {
			return ErrInvalidOrganizationalMessage
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(message.Body))
	var body map[string]any
	if err := decoder.Decode(&body); err != nil || body == nil || len(body) == 0 {
		return ErrInvalidOrganizationalMessage
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalidOrganizationalMessage
	}
	return nil
}

type MessageDeliveryState string

const (
	MessagePending    MessageDeliveryState = "PENDING"
	MessageClaimed    MessageDeliveryState = "CLAIMED"
	MessageResolved   MessageDeliveryState = "RESOLVED"
	MessageDeadLetter MessageDeliveryState = "DEAD_LETTER"
)

type MessageClaim struct {
	Message    OrganizationalMessage `json:"message"`
	State      MessageDeliveryState  `json:"state"`
	Holder     kernel.ActorFQN       `json:"holder,omitempty"`
	Execution  kernel.ExecutionTuple `json:"execution,omitempty"`
	ClaimEpoch uint64                `json:"claim_epoch"`
	LeaseUntil time.Time             `json:"lease_until,omitempty"`
	Attempts   uint32                `json:"attempts"`
	Resolution string                `json:"resolution,omitempty"`
	Evidence   kernel.Digest         `json:"evidence_digest,omitempty"`
	DeadReason string                `json:"dead_letter_reason,omitempty"`
}

type OrganizationalMessageStore interface {
	AppendMessage(context.Context, OrganizationalMessage) error
	AppendMessages(context.Context, []OrganizationalMessage) error
	AcquireMessage(context.Context, kernel.ActorFQN, kernel.ExecutionTuple, time.Time, time.Duration, uint32) (MessageClaim, error)
	RenewMessage(context.Context, kernel.UUIDv7, kernel.ActorFQN, kernel.ExecutionTuple, uint64, time.Time, time.Duration) error
	ResolveMessage(context.Context, kernel.UUIDv7, kernel.ActorFQN, kernel.ExecutionTuple, uint64, time.Time, string, kernel.Digest) error
	YieldMessage(context.Context, kernel.UUIDv7, kernel.ActorFQN, kernel.ExecutionTuple, uint64, time.Time) error
	ReaddressMessage(context.Context, kernel.UUIDv7, kernel.ActorFQN, kernel.ExecutionTuple, kernel.ActorFQN, time.Time, uint32) (OrganizationalMessage, error)
	SweepMessages(context.Context, time.Time, uint32) (released, dead int64, err error)
}

type OrganizationalMessageReader interface {
	ReadMessage(context.Context, kernel.UUIDv7) (MessageClaim, bool, error)
	TraceMessageThread(context.Context, kernel.UUIDv7) ([]MessageClaim, error)
	ListDeadLetters(context.Context, kernel.ActorFQN, int64) ([]MessageClaim, error)
}

type MessageBus struct {
	store OrganizationalMessageStore
	roles RoleStateStore
}

func NewMessageBus(store OrganizationalMessageStore, roles RoleStateStore) (*MessageBus, error) {
	if store == nil || roles == nil {
		return nil, ErrInvalidOrganizationalMessage
	}
	return &MessageBus{store: store, roles: roles}, nil
}

// Send persists organizational information only. MessageBus deliberately has
// no model or role-runtime dependency, so delivery cannot start an agent or a
// model invocation.
func (bus *MessageBus) Send(ctx context.Context, message OrganizationalMessage) error {
	if bus == nil || message.Validate() != nil || len(message.ReaddressHistory) != 0 {
		return ErrInvalidOrganizationalMessage
	}
	if err := bus.authorizeSender(ctx, message); err != nil {
		return err
	}
	return bus.store.AppendMessage(ctx, message)
}

func (bus *MessageBus) Readdress(ctx context.Context, id kernel.UUIDv7, sender kernel.ActorFQN, execution kernel.ExecutionTuple, recipient kernel.ActorFQN, now time.Time, maximumReaddresses uint32) (OrganizationalMessage, error) {
	if bus == nil || !id.Valid() || !sender.Valid() || !execution.Valid() || !recipient.Valid() || now.IsZero() || maximumReaddresses == 0 || maximumReaddresses > 8 {
		return OrganizationalMessage{}, ErrInvalidOrganizationalMessage
	}
	senderRole, found, err := bus.roles.LoadRole(ctx, sender)
	if err != nil || !found || senderRole.Execution != execution || senderRole.Status != RoleIdle {
		return OrganizationalMessage{}, errors.Join(ErrStaleOrganizationalClaim, err)
	}
	recipientRole, found, err := bus.roles.LoadRole(ctx, recipient)
	if err != nil || !found || recipientRole.Status != RoleIdle {
		return OrganizationalMessage{}, errors.Join(ErrRoleNotRunning, err)
	}
	return bus.store.ReaddressMessage(ctx, id, sender, execution, recipient, now, maximumReaddresses)
}

func (bus *MessageBus) SendFanout(ctx context.Context, messages []OrganizationalMessage) error {
	if len(messages) < 2 || len(messages) > 64 {
		return ErrInvalidOrganizationalMessage
	}
	seen := make(map[kernel.ActorFQN]struct{}, len(messages))
	first := messages[0]
	if first.FanoutID == nil || !first.FanoutID.Valid() {
		return ErrInvalidOrganizationalMessage
	}
	for _, message := range messages {
		if _, duplicate := seen[message.Recipient]; duplicate {
			return ErrInvalidOrganizationalMessage
		}
		seen[message.Recipient] = struct{}{}
		if message.Validate() != nil || len(message.ReaddressHistory) != 0 || message.FanoutID == nil || *message.FanoutID != *first.FanoutID || message.Sender != first.Sender || message.SenderExecution != first.SenderExecution || message.Type != first.Type || message.Purpose != first.Purpose || message.Flow.BudgetAccountID != first.Flow.BudgetAccountID || message.Flow.LifecycleEpoch != first.Flow.LifecycleEpoch || message.Flow.ScopeRevision != first.Flow.ScopeRevision {
			return ErrInvalidOrganizationalMessage
		}
	}
	if err := bus.authorizeSender(ctx, first); err != nil {
		return err
	}
	return bus.store.AppendMessages(ctx, messages)
}

func (bus *MessageBus) authorizeSender(ctx context.Context, message OrganizationalMessage) error {
	role, found, err := bus.roles.LoadRole(ctx, message.Sender)
	if err != nil {
		return err
	}
	if !found || role.Execution != message.SenderExecution || role.Status != RoleIdle {
		return ErrStaleOrganizationalClaim
	}
	return nil
}

func (bus *MessageBus) Claim(ctx context.Context, recipient kernel.ActorFQN, execution kernel.ExecutionTuple, now time.Time, lease time.Duration, maximumAttempts uint32) (MessageClaim, error) {
	if bus == nil || !recipient.Valid() || !execution.Valid() || now.IsZero() || lease <= 0 || maximumAttempts == 0 {
		return MessageClaim{}, ErrInvalidOrganizationalMessage
	}
	role, found, err := bus.roles.LoadRole(ctx, recipient)
	if err != nil {
		return MessageClaim{}, err
	}
	if !found || role.Execution != execution || role.Status != RoleIdle {
		return MessageClaim{}, ErrStaleOrganizationalClaim
	}
	return bus.store.AcquireMessage(ctx, recipient, execution, now, lease, maximumAttempts)
}

func (bus *MessageBus) Renew(ctx context.Context, claim MessageClaim, now time.Time, lease time.Duration) error {
	if bus == nil {
		return ErrInvalidOrganizationalMessage
	}
	return bus.store.RenewMessage(ctx, claim.Message.ID, claim.Holder, claim.Execution, claim.ClaimEpoch, now, lease)
}

func (bus *MessageBus) Resolve(ctx context.Context, claim MessageClaim, now time.Time, resolution string, evidence kernel.Digest) error {
	if bus == nil {
		return ErrInvalidOrganizationalMessage
	}
	return bus.store.ResolveMessage(ctx, claim.Message.ID, claim.Holder, claim.Execution, claim.ClaimEpoch, now, resolution, evidence)
}

func (bus *MessageBus) Yield(ctx context.Context, claim MessageClaim, now time.Time) error {
	if bus == nil {
		return ErrInvalidOrganizationalMessage
	}
	return bus.store.YieldMessage(ctx, claim.Message.ID, claim.Holder, claim.Execution, claim.ClaimEpoch, now)
}

func (bus *MessageBus) Sweep(ctx context.Context, now time.Time, maximumAttempts uint32) (int64, int64, error) {
	if bus == nil {
		return 0, 0, ErrInvalidOrganizationalMessage
	}
	return bus.store.SweepMessages(ctx, now, maximumAttempts)
}

func (bus *MessageBus) Read(ctx context.Context, id kernel.UUIDv7) (MessageClaim, bool, error) {
	reader, ok := bus.store.(OrganizationalMessageReader)
	if bus == nil || !ok {
		return MessageClaim{}, false, ErrInvalidOrganizationalMessage
	}
	return reader.ReadMessage(ctx, id)
}

func (bus *MessageBus) Trace(ctx context.Context, thread kernel.UUIDv7) ([]MessageClaim, error) {
	reader, ok := bus.store.(OrganizationalMessageReader)
	if bus == nil || !ok {
		return nil, ErrInvalidOrganizationalMessage
	}
	return reader.TraceMessageThread(ctx, thread)
}

func (bus *MessageBus) DeadLetters(ctx context.Context, recipient kernel.ActorFQN, limit int64) ([]MessageClaim, error) {
	reader, ok := bus.store.(OrganizationalMessageReader)
	if bus == nil || !ok {
		return nil, ErrInvalidOrganizationalMessage
	}
	return reader.ListDeadLetters(ctx, recipient, limit)
}
