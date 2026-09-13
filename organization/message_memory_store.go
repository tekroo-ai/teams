package organization

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

type messageThreadState struct {
	LastMessageID   kernel.UUIDv7
	LastStepID      kernel.UUIDv7
	LastHop         uint32
	MaximumHops     uint32
	BudgetAccountID kernel.UUIDv7
	LifecycleEpoch  uint64
	ScopeRevision   uint64
	Visited         map[kernel.UUIDv7]struct{}
	Progress        map[kernel.Digest]struct{}
	LastRecipient   kernel.ActorFQN
}

type MemoryOrganizationalMessageStore struct {
	mu                 sync.Mutex
	messages           map[kernel.UUIDv7]MessageClaim
	threads            map[kernel.UUIDv7]messageThreadState
	workflows          map[kernel.UUIDv7]kernel.WorkflowInstance
	workflowProposals  map[kernel.UUIDv7]kernel.WorkProposal
	workflowAdmissions map[kernel.UUIDv7]kernel.WorkAdmissionResult
	workflowIntents    map[kernel.UUIDv7]kernel.OutboxIntent
	workflowRoots      map[kernel.AggregateRef]kernel.UUIDv7
	workflowEvents     map[kernel.UUIDv7]kernel.Digest
}

func NewMemoryOrganizationalMessageStore() *MemoryOrganizationalMessageStore {
	return &MemoryOrganizationalMessageStore{
		messages: make(map[kernel.UUIDv7]MessageClaim), threads: make(map[kernel.UUIDv7]messageThreadState),
		workflows: make(map[kernel.UUIDv7]kernel.WorkflowInstance), workflowProposals: make(map[kernel.UUIDv7]kernel.WorkProposal),
		workflowAdmissions: make(map[kernel.UUIDv7]kernel.WorkAdmissionResult), workflowIntents: make(map[kernel.UUIDv7]kernel.OutboxIntent),
		workflowRoots:  make(map[kernel.AggregateRef]kernel.UUIDv7),
		workflowEvents: make(map[kernel.UUIDv7]kernel.Digest),
	}
}

func (store *MemoryOrganizationalMessageStore) AppendMessage(ctx context.Context, message OrganizationalMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if message.Validate() != nil {
		return ErrInvalidOrganizationalMessage
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.appendMessageLocked(message)
}

func (store *MemoryOrganizationalMessageStore) AppendMessages(ctx context.Context, messages []OrganizationalMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(messages) == 0 {
		return ErrInvalidOrganizationalMessage
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	scratch := &MemoryOrganizationalMessageStore{messages: make(map[kernel.UUIDv7]MessageClaim, len(store.messages)), threads: make(map[kernel.UUIDv7]messageThreadState, len(store.threads))}
	for id, claim := range store.messages {
		scratch.messages[id] = claim
	}
	for id, thread := range store.threads {
		copyThread := thread
		copyThread.Visited = make(map[kernel.UUIDv7]struct{}, len(thread.Visited))
		copyThread.Progress = make(map[kernel.Digest]struct{}, len(thread.Progress))
		for value := range thread.Visited {
			copyThread.Visited[value] = struct{}{}
		}
		for value := range thread.Progress {
			copyThread.Progress[value] = struct{}{}
		}
		scratch.threads[id] = copyThread
	}
	for _, message := range messages {
		if err := scratch.appendMessageLocked(message); err != nil {
			return err
		}
	}
	store.messages = scratch.messages
	store.threads = scratch.threads
	return nil
}

func (store *MemoryOrganizationalMessageStore) appendMessageLocked(message OrganizationalMessage) error {
	if message.Validate() != nil {
		return ErrInvalidOrganizationalMessage
	}
	if _, exists := store.messages[message.ID]; exists {
		return ErrOrganizationalMessageConflict
	}
	thread, exists := store.threads[message.Flow.ThreadID]
	if !exists {
		if message.Flow.Hop != 1 {
			return ErrOrganizationalLoop
		}
		thread = messageThreadState{
			MaximumHops: message.Flow.MaximumHops, BudgetAccountID: message.Flow.BudgetAccountID,
			LifecycleEpoch: message.Flow.LifecycleEpoch, ScopeRevision: message.Flow.ScopeRevision,
			Visited: make(map[kernel.UUIDv7]struct{}), Progress: make(map[kernel.Digest]struct{}),
		}
	} else if message.Flow.Hop != thread.LastHop+1 || message.Flow.MaximumHops != thread.MaximumHops || message.Flow.BudgetAccountID != thread.BudgetAccountID || message.Flow.LifecycleEpoch != thread.LifecycleEpoch || message.Flow.ScopeRevision != thread.ScopeRevision || message.CausationID == nil || *message.CausationID != thread.LastMessageID || message.Flow.ParentStepID == nil || *message.Flow.ParentStepID != thread.LastStepID || message.Sender != thread.LastRecipient {
		return ErrOrganizationalLoop
	}
	if _, repeated := thread.Visited[message.Flow.StepID]; repeated {
		return ErrOrganizationalLoop
	}
	if _, repeated := thread.Progress[message.Flow.ProgressDigest]; repeated {
		return ErrOrganizationalLoop
	}
	thread.LastMessageID = message.ID
	thread.LastStepID = message.Flow.StepID
	thread.LastHop = message.Flow.Hop
	thread.Visited[message.Flow.StepID] = struct{}{}
	thread.Progress[message.Flow.ProgressDigest] = struct{}{}
	thread.LastRecipient = message.Recipient
	store.threads[message.Flow.ThreadID] = thread
	store.messages[message.ID] = MessageClaim{Message: message, State: MessagePending}
	return nil
}

func actorRole(actor kernel.ActorFQN) string {
	text := string(actor)
	separator := strings.Index(text, "::")
	instance := strings.LastIndex(text, "-")
	if separator < 0 || instance <= separator+2 {
		return text
	}
	return text[separator+2 : instance]
}

func (store *MemoryOrganizationalMessageStore) AcquireMessage(ctx context.Context, recipient kernel.ActorFQN, execution kernel.ExecutionTuple, now time.Time, lease time.Duration, maximumAttempts uint32) (MessageClaim, error) {
	if err := ctx.Err(); err != nil {
		return MessageClaim{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	ids := make([]kernel.UUIDv7, 0)
	for id, claim := range store.messages {
		if claim.Message.Recipient == recipient && claim.Message.ExpiresAt.After(now) && (claim.State == MessagePending || claim.State == MessageClaimed && !claim.LeaseUntil.After(now)) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(left, right int) bool { return ids[left] < ids[right] })
	for _, id := range ids {
		claim := store.messages[id]
		if claim.Attempts >= maximumAttempts {
			claim.State = MessageDeadLetter
			claim.DeadReason = "MAX_ATTEMPTS_EXHAUSTED"
			store.messages[id] = claim
			continue
		}
		claim.State = MessageClaimed
		claim.Holder = recipient
		claim.Execution = execution
		claim.ClaimEpoch++
		claim.Attempts++
		claim.LeaseUntil = now.Add(lease)
		store.messages[id] = claim
		return claim, nil
	}
	return MessageClaim{}, ErrOrganizationalMessageNotFound
}

func (store *MemoryOrganizationalMessageStore) RenewMessage(ctx context.Context, id kernel.UUIDv7, holder kernel.ActorFQN, execution kernel.ExecutionTuple, epoch uint64, now time.Time, lease time.Duration) error {
	return store.updateClaim(ctx, id, holder, execution, epoch, now, func(claim *MessageClaim) {
		claim.LeaseUntil = now.Add(lease)
	})
}

func (store *MemoryOrganizationalMessageStore) ResolveMessage(ctx context.Context, id kernel.UUIDv7, holder kernel.ActorFQN, execution kernel.ExecutionTuple, epoch uint64, now time.Time, resolution string, evidence kernel.Digest) error {
	if resolution == "" || !evidence.Valid() {
		return ErrInvalidOrganizationalMessage
	}
	return store.updateClaim(ctx, id, holder, execution, epoch, now, func(claim *MessageClaim) {
		claim.State = MessageResolved
		claim.Resolution = resolution
		claim.Evidence = evidence
		claim.Holder = ""
		claim.Execution = kernel.ExecutionTuple{}
		claim.LeaseUntil = time.Time{}
	})
}

func (store *MemoryOrganizationalMessageStore) YieldMessage(ctx context.Context, id kernel.UUIDv7, holder kernel.ActorFQN, execution kernel.ExecutionTuple, epoch uint64, now time.Time) error {
	return store.updateClaim(ctx, id, holder, execution, epoch, now, func(claim *MessageClaim) {
		claim.State = MessagePending
		claim.Holder = ""
		claim.Execution = kernel.ExecutionTuple{}
		claim.LeaseUntil = time.Time{}
	})
}

func (store *MemoryOrganizationalMessageStore) ReaddressMessage(ctx context.Context, id kernel.UUIDv7, sender kernel.ActorFQN, execution kernel.ExecutionTuple, recipient kernel.ActorFQN, now time.Time, maximumReaddresses uint32) (OrganizationalMessage, error) {
	if err := ctx.Err(); err != nil {
		return OrganizationalMessage{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	claim, found := store.messages[id]
	if !found {
		return OrganizationalMessage{}, ErrOrganizationalMessageNotFound
	}
	message := claim.Message
	thread := store.threads[message.Flow.ThreadID]
	if claim.State != MessagePending || message.Sender != sender || message.SenderExecution != execution || message.Recipient == recipient || uint32(len(message.ReaddressHistory)) >= maximumReaddresses || thread.LastMessageID != id {
		return OrganizationalMessage{}, ErrOrganizationalMessageConflict
	}
	prior := message.Recipient
	message.Recipient = recipient
	message.ReaddressHistory = append(message.ReaddressHistory, MessageReaddress{From: prior, To: recipient, By: sender, At: now})
	if message.Validate() != nil {
		return OrganizationalMessage{}, ErrInvalidOrganizationalMessage
	}
	thread.LastRecipient = recipient
	store.threads[message.Flow.ThreadID] = thread
	claim.Message = message
	store.messages[id] = claim
	return message, nil
}

func (store *MemoryOrganizationalMessageStore) updateClaim(ctx context.Context, id kernel.UUIDv7, holder kernel.ActorFQN, execution kernel.ExecutionTuple, epoch uint64, now time.Time, update func(*MessageClaim)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	claim, exists := store.messages[id]
	if !exists {
		return ErrOrganizationalMessageNotFound
	}
	if claim.State != MessageClaimed || claim.Holder != holder || claim.Execution != execution || claim.ClaimEpoch != epoch || !claim.LeaseUntil.After(now) {
		return ErrStaleOrganizationalClaim
	}
	update(&claim)
	store.messages[id] = claim
	return nil
}

func (store *MemoryOrganizationalMessageStore) SweepMessages(ctx context.Context, now time.Time, maximumAttempts uint32) (int64, int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	var released, dead int64
	for id, claim := range store.messages {
		if claim.State != MessageClaimed || claim.LeaseUntil.After(now) {
			continue
		}
		claim.Holder = ""
		claim.Execution = kernel.ExecutionTuple{}
		claim.LeaseUntil = time.Time{}
		if claim.Attempts >= maximumAttempts {
			claim.State = MessageDeadLetter
			claim.DeadReason = "MAX_ATTEMPTS_EXHAUSTED"
			dead++
		} else {
			claim.State = MessagePending
			released++
		}
		store.messages[id] = claim
	}
	return released, dead, nil
}

func (store *MemoryOrganizationalMessageStore) ReadMessage(ctx context.Context, id kernel.UUIDv7) (MessageClaim, bool, error) {
	if err := ctx.Err(); err != nil {
		return MessageClaim{}, false, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	claim, found := store.messages[id]
	return claim, found, nil
}

func (store *MemoryOrganizationalMessageStore) TraceMessageThread(ctx context.Context, thread kernel.UUIDv7) ([]MessageClaim, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	result := make([]MessageClaim, 0)
	for _, claim := range store.messages {
		if claim.Message.Flow.ThreadID == thread {
			result = append(result, claim)
		}
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Message.Flow.Hop < result[right].Message.Flow.Hop })
	return result, nil
}

func (store *MemoryOrganizationalMessageStore) ListDeadLetters(ctx context.Context, recipient kernel.ActorFQN, limit int64) ([]MessageClaim, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		return nil, ErrInvalidOrganizationalMessage
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	result := make([]MessageClaim, 0)
	for _, claim := range store.messages {
		if claim.State == MessageDeadLetter && (recipient == "" || claim.Message.Recipient == recipient) {
			result = append(result, claim)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Message.CreatedAt.Before(result[right].Message.CreatedAt)
	})
	if int64(len(result)) > limit {
		result = result[:limit]
	}
	return result, nil
}
