package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

var (
	ErrConflict                = errors.New("decision precondition conflict")
	ErrReceiptConflict         = errors.New("command receipt conflict")
	ErrCommandIdentityConflict = kernel.ErrCommandIdentityConflict
	ErrIdempotencyKeyConflict  = kernel.ErrIdempotencyKeyConflict
	ErrInvalidDecision         = errors.New("invalid decision")
	ErrInjectedFault           = errors.New("injected commit fault")
)

type FaultPoint string

const (
	FaultNone                 FaultPoint = "NONE"
	FaultBeforeState          FaultPoint = "BEFORE_STATE"
	FaultBeforeEvents         FaultPoint = "BEFORE_EVENTS"
	FaultBeforeReceipt        FaultPoint = "BEFORE_RECEIPT"
	FaultBeforeAuthority      FaultPoint = "BEFORE_AUTHORITY"
	FaultBeforeOutbox         FaultPoint = "BEFORE_OUTBOX"
	FaultAfterCommitBeforeAck FaultPoint = "AFTER_COMMIT_BEFORE_ACK"
)

type Store struct {
	mu                  sync.RWMutex
	states              map[kernel.AggregateRef]kernel.AggregateState
	revisions           map[kernel.AggregateRef]uint64
	receipts            map[kernel.UUIDv7]kernel.CommandReceipt
	commandBindings     map[kernel.UUIDv7]decisionIdentity
	idempotency         map[kernel.Digest]decisionIdentity
	events              map[kernel.UUIDv7]kernel.DomainEvent
	executions          map[kernel.ActorFQN]kernel.ExecutionTuple
	evidence            map[kernel.UUIDv7]kernel.EvidenceMetadata
	authority           map[kernel.UUIDv7]kernel.AuthorityDecision
	outbox              map[kernel.UUIDv7]kernel.OutboxIntent
	identityConflicts   []kernel.IdentityConflictAudit
	provenance          map[kernel.Digest]kernel.DecisionProvenance
	authorizationPolicy kernel.AuthorizationPolicy
	openReviews         map[kernel.CompletionReviewKey]kernel.AggregateRef
	reviews             map[kernel.AggregateRef]kernel.CompletionReviewSnapshot
	eventQualifications map[kernel.UUIDv7]string
	attemptBudgets      map[kernel.AttemptBudgetKey]kernel.AttemptBudgetSnapshot
	faultPoint          FaultPoint
}

func (s *Store) RecordIdentityConflict(ctx context.Context, audit kernel.IdentityConflictAudit) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !audit.CommandID.Valid() || !audit.IdempotencyScope.Valid() || !audit.Fingerprint.Valid() || audit.ReasonCode == "" || audit.ObservedAt.IsZero() || !audit.ProvenanceDigest.Valid() {
		return ErrInvalidDecision
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.identityConflicts = append(s.identityConflicts, audit)
	return nil
}

func (s *Store) IdentityConflictCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.identityConflicts)
}

type decisionIdentity struct {
	CommandID   kernel.UUIDv7
	Fingerprint kernel.Digest
	Scope       kernel.Digest
}

func NewStoreWithFault(point FaultPoint, policy ...kernel.AuthorizationPolicy) *Store {
	store := NewStore(policy...)
	store.faultPoint = point
	return store
}

func NewStore(policy ...kernel.AuthorizationPolicy) *Store {
	store := &Store{
		states:              make(map[kernel.AggregateRef]kernel.AggregateState),
		revisions:           make(map[kernel.AggregateRef]uint64),
		receipts:            make(map[kernel.UUIDv7]kernel.CommandReceipt),
		commandBindings:     make(map[kernel.UUIDv7]decisionIdentity),
		idempotency:         make(map[kernel.Digest]decisionIdentity),
		events:              make(map[kernel.UUIDv7]kernel.DomainEvent),
		executions:          make(map[kernel.ActorFQN]kernel.ExecutionTuple),
		evidence:            make(map[kernel.UUIDv7]kernel.EvidenceMetadata),
		authority:           make(map[kernel.UUIDv7]kernel.AuthorityDecision),
		outbox:              make(map[kernel.UUIDv7]kernel.OutboxIntent),
		provenance:          make(map[kernel.Digest]kernel.DecisionProvenance),
		openReviews:         make(map[kernel.CompletionReviewKey]kernel.AggregateRef),
		reviews:             make(map[kernel.AggregateRef]kernel.CompletionReviewSnapshot),
		eventQualifications: make(map[kernel.UUIDv7]string),
		attemptBudgets:      make(map[kernel.AttemptBudgetKey]kernel.AttemptBudgetSnapshot),
	}
	if len(policy) == 1 {
		store.authorizationPolicy = cloneAuthorizationPolicy(policy[0])
	}
	return store
}

func (s *Store) SetAuthorizationPolicy(policy kernel.AuthorizationPolicy) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authorizationPolicy = cloneAuthorizationPolicy(policy)
}

func (s *Store) Load(ctx context.Context, target kernel.AggregateRef) (kernel.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return kernel.Snapshot{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loadLocked(target, nil), nil
}

func (s *Store) LoadDecision(ctx context.Context, command kernel.KernelCommand) (kernel.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return kernel.Snapshot{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loadLocked(command.Target, command.Preconditions), nil
}

func (s *Store) loadLocked(target kernel.AggregateRef, preconditions []kernel.AggregatePrecondition) kernel.Snapshot {
	revision, found := s.revisions[target]
	snapshot := kernel.Snapshot{Exists: found, Revision: revision}
	if found {
		if state, hasState := s.states[target]; hasState {
			copy := state.Clone()
			snapshot.State = &copy
		}
	}
	snapshot.AcceptedEvents = make(map[kernel.UUIDv7]kernel.AcceptedEvent, len(s.events))
	for eventID, event := range s.events {
		snapshot.AcceptedEvents[eventID] = kernel.AcceptedEvent{EventType: event.EventType, Qualification: s.eventQualifications[eventID]}
	}
	snapshot.CurrentExecutions = make(map[kernel.ActorFQN]kernel.ExecutionTuple, len(s.executions))
	for actor, execution := range s.executions {
		snapshot.CurrentExecutions[actor] = execution
	}
	snapshot.Evidence = make(map[kernel.UUIDv7]kernel.EvidenceMetadata, len(s.evidence))
	for evidenceID, metadata := range s.evidence {
		snapshot.Evidence[evidenceID] = metadata
	}
	snapshot.Related = make(map[kernel.AggregateRef]kernel.RelatedSnapshot, len(preconditions))
	for _, precondition := range preconditions {
		relatedRevision, relatedExists := s.revisions[precondition.Aggregate]
		related := kernel.RelatedSnapshot{Exists: relatedExists, Revision: relatedRevision}
		if state, hasState := s.states[precondition.Aggregate]; hasState {
			copy := state.Clone()
			related.State = &copy
		}
		snapshot.Related[precondition.Aggregate] = related
	}
	snapshot.Authorization = cloneAuthorizationPolicy(s.authorizationPolicy)
	snapshot.OpenReviews = make(map[kernel.CompletionReviewKey]kernel.AggregateRef, len(s.openReviews))
	for key, review := range s.openReviews {
		snapshot.OpenReviews[key] = review
	}
	snapshot.Reviews = make(map[kernel.AggregateRef]kernel.CompletionReviewSnapshot, len(s.reviews))
	for review, progress := range s.reviews {
		snapshot.Reviews[review] = progress.Clone()
	}
	snapshot.AttemptBudgets = make(map[kernel.AttemptBudgetKey]kernel.AttemptBudgetSnapshot, len(s.attemptBudgets))
	for key, budget := range s.attemptBudgets {
		snapshot.AttemptBudgets[key] = cloneAttemptBudgetSnapshot(budget)
	}
	return snapshot
}

func (s *Store) LookupReceipt(ctx context.Context, command kernel.KernelCommand, at time.Time) (kernel.CommandReceipt, bool, error) {
	if err := ctx.Err(); err != nil {
		return kernel.CommandReceipt{}, false, err
	}
	fingerprint, err := kernel.CommandFingerprint(command)
	if err != nil {
		return kernel.CommandReceipt{}, false, err
	}
	scope, err := kernel.IdempotencyScopeDigest(command)
	if err != nil {
		return kernel.CommandReceipt{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	receipt, found := s.receipts[command.CommandID]
	if found {
		if !s.canReadReceipt(command, at) {
			return kernel.CommandReceipt{}, false, kernel.ErrReceiptAccessDenied
		}
		binding := s.commandBindings[command.CommandID]
		if binding.Fingerprint != fingerprint || binding.Scope != scope {
			return kernel.CommandReceipt{}, false, ErrCommandIdentityConflict
		}
		return cloneReceipt(receipt), true, nil
	}
	if prior, scoped := s.idempotency[scope]; scoped {
		if !s.canReadReceipt(command, at) {
			return kernel.CommandReceipt{}, false, kernel.ErrReceiptAccessDenied
		}
		if prior.Fingerprint != fingerprint {
			return kernel.CommandReceipt{}, false, ErrIdempotencyKeyConflict
		}
		return cloneReceipt(s.receipts[prior.CommandID]), true, nil
	}
	return kernel.CommandReceipt{}, false, nil
}

func (s *Store) canReadReceipt(command kernel.KernelCommand, at time.Time) bool {
	var state *kernel.AggregateState
	if current, found := s.states[command.Target]; found {
		copy := current.Clone()
		state = &copy
	}
	return s.authorizationPolicy.Authorize(command, state, at).CanReadTarget
}

func (s *Store) Commit(ctx context.Context, expected kernel.Snapshot, decision kernel.Decision) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	target := decision.Receipt.Target
	if !decision.CommandFingerprint.Valid() || !decision.IdempotencyScope.Valid() {
		return ErrInvalidDecision
	}
	if !validDecisionTuple(expected, decision) {
		return ErrInvalidDecision
	}
	provenanceDigest, err := decision.Provenance.Digest()
	if err != nil || !decision.Provenance.Valid() || !provenanceDigest.Valid() || provenanceDigest != decision.Receipt.ProvenanceDigest || decision.Provenance.CommandID != decision.Receipt.CommandID || decision.Provenance.CommandFingerprint != decision.CommandFingerprint {
		return ErrInvalidDecision
	}
	if decision.Authority.PolicyDigest.Valid() && (decision.Authority.PolicyDigest != decision.Provenance.PolicyDigest || decision.Authority.PolicyRevision != decision.Provenance.PolicyRevision || !reflect.DeepEqual(decision.Authority.GrantDigests, decision.Provenance.GrantDigests) || !reflect.DeepEqual(decision.Authority.DelegationDigests, decision.Provenance.DelegationDigests)) {
		return ErrInvalidDecision
	}
	for _, event := range decision.Events {
		if event.ProvenanceDigest != provenanceDigest {
			return ErrInvalidDecision
		}
	}
	if decision.NextState != nil && (decision.Receipt.ResultingRevision == nil || decision.NextState.Revision != *decision.Receipt.ResultingRevision) {
		return ErrInvalidDecision
	}
	if decision.Receipt.ResultingRevision != nil && *decision.Receipt.ResultingRevision == 0 {
		return ErrInvalidDecision
	}
	if _, duplicate := s.receipts[decision.Receipt.CommandID]; duplicate {
		binding := s.commandBindings[decision.Receipt.CommandID]
		if binding.Fingerprint == decision.CommandFingerprint && binding.Scope == decision.IdempotencyScope {
			return kernel.ErrDecisionAlreadyCommitted
		}
		return ErrReceiptConflict
	}
	if prior, duplicate := s.idempotency[decision.IdempotencyScope]; duplicate {
		if prior.Fingerprint == decision.CommandFingerprint {
			return kernel.ErrDecisionAlreadyCommitted
		}
		return ErrIdempotencyKeyConflict
	}
	currentRevision, exists := s.revisions[target]
	if expected.Exists != exists || (exists && currentRevision != expected.Revision) {
		return ErrConflict
	}
	for _, event := range decision.Events {
		if _, duplicate := s.events[event.EventID]; duplicate {
			return ErrConflict
		}
	}
	for actor, expectedExecution := range decision.Guards.Executions {
		if current, found := s.executions[actor]; !found || current != expectedExecution {
			return ErrConflict
		}
	}
	for _, actor := range decision.Guards.AbsentExecutions {
		if _, found := s.executions[actor]; found {
			return ErrConflict
		}
	}
	for _, parentID := range decision.Guards.ParentIDs {
		if _, found := s.events[parentID]; !found {
			return ErrConflict
		}
	}
	for _, reference := range decision.Guards.EvidenceRefs {
		metadata, found := s.evidence[reference.EvidenceID]
		if !found || !metadata.Available || metadata.SHA256 != reference.SHA256 {
			return ErrConflict
		}
	}
	for _, precondition := range decision.Guards.Preconditions {
		currentRevision, found := s.revisions[precondition.Aggregate]
		if precondition.Expected.MustNotExist {
			if found {
				return ErrConflict
			}
			continue
		}
		if !found || currentRevision != precondition.Expected.Revision {
			return ErrConflict
		}
	}
	if decision.Guards.PolicyDigest.Valid() && (s.authorizationPolicy.PolicyDigest != decision.Guards.PolicyDigest || s.authorizationPolicy.Revision != decision.Guards.PolicyRevision) {
		return ErrConflict
	}
	for _, key := range decision.Guards.AbsentReviewKeys {
		if _, found := s.openReviews[key]; found {
			return ErrConflict
		}
	}
	if decision.AttemptBudget != nil {
		if err := validateAttemptBudgetCommit(s.attemptBudgets, *decision.AttemptBudget); err != nil {
			return err
		}
	}
	if s.faultPoint == FaultBeforeState || s.faultPoint == FaultBeforeEvents || s.faultPoint == FaultBeforeReceipt || s.faultPoint == FaultBeforeAuthority || s.faultPoint == FaultBeforeOutbox {
		return fmt.Errorf("%w: %s", ErrInjectedFault, s.faultPoint)
	}

	if decision.NextState != nil {
		s.states[target] = decision.NextState.Clone()
	}
	if decision.Receipt.ResultingRevision != nil {
		s.revisions[target] = *decision.Receipt.ResultingRevision
	}
	for _, event := range decision.Events {
		s.events[event.EventID] = cloneEvent(event)
		s.applyRegistryEvent(event)
		s.applyReviewEvent(event)
	}
	if decision.AttemptBudget != nil {
		applyAttemptBudget(s.attemptBudgets, *decision.AttemptBudget)
	}
	s.receipts[decision.Receipt.CommandID] = cloneReceipt(decision.Receipt)
	binding := decisionIdentity{CommandID: decision.Receipt.CommandID, Fingerprint: decision.CommandFingerprint, Scope: decision.IdempotencyScope}
	s.commandBindings[decision.Receipt.CommandID] = binding
	s.idempotency[decision.IdempotencyScope] = binding
	s.authority[decision.Receipt.CommandID] = cloneAuthorityDecision(decision.Authority)
	s.provenance[provenanceDigest] = decision.Provenance
	for _, intent := range decision.Outbox {
		s.outbox[intent.IntentID] = intent
	}
	if s.faultPoint == FaultAfterCommitBeforeAck {
		return kernel.ErrCommitUncertain
	}
	return nil
}

func (s *Store) applyReviewEvent(event kernel.DomainEvent) {
	switch event.EventType {
	case "tekroo.event.completion-review.opened":
		if key, err := kernel.CompletionReviewKeyFromPayload(event.Payload); err == nil {
			s.openReviews[key] = event.Aggregate
		}
		if review, err := kernel.CompletionReviewFromPayload(event.Payload); err == nil {
			s.reviews[event.Aggregate] = review
		}
	case "tekroo.event.completion-review.result-recorded":
		reviewID, policyRevision, result, err := kernel.ReviewBranchResultFromPayload(event.Payload)
		current, found := s.reviews[event.Aggregate]
		if err != nil || reviewID != event.Aggregate.ID || !found || current.BranchPolicyRevision != policyRevision {
			return
		}
		result.Authority = event.Authority
		result.EventID = event.EventID
		result.DecidedAt = event.CommittedAt
		next, valid := kernel.ApplyReviewBranchResult(current, result)
		if valid {
			s.reviews[event.Aggregate] = next
			s.eventQualifications[event.EventID] = next.Join.Status
		}
	case "tekroo.event.completion-review.finalized":
		current, found := s.reviews[event.Aggregate]
		if !found {
			return
		}
		next, valid := kernel.ApplyReviewFinalization(current, event.EventID, event.Payload)
		if valid {
			s.reviews[event.Aggregate] = next
			s.eventQualifications[event.EventID] = next.Join.Status
		}
	}
}

func validDecisionTuple(expected kernel.Snapshot, decision kernel.Decision) bool {
	if !decision.Receipt.CommandID.Valid() || !decision.Receipt.Target.Valid() || !decision.Authority.Principal.Valid() {
		return false
	}
	if decision.AttemptBudget != nil && (!decision.AttemptBudget.Valid() || decision.AttemptBudget.Key.Subject != decision.Receipt.Target) {
		return false
	}
	if len(decision.Events) == 0 {
		return decision.NextState == nil && decision.Receipt.ResultingRevision == nil && len(decision.Receipt.EventIDs) == 0 && len(decision.Outbox) == 0 && decision.Receipt.OutcomeCode != kernel.OutcomeApplied
	}
	if len(decision.Events) != len(decision.Receipt.EventIDs) || len(decision.Outbox) != len(decision.Events) {
		return false
	}
	eventIDs := make(map[kernel.UUIDv7]struct{}, len(decision.Events))
	intentIDs := make(map[kernel.UUIDv7]struct{}, len(decision.Outbox))
	for index, event := range decision.Events {
		if !event.EventID.Valid() || event.Aggregate != decision.Receipt.Target || event.AggregateRevision != expected.Revision+uint64(index)+1 || event.LifecycleEpoch == 0 || decision.Receipt.EventIDs[index] != event.EventID {
			return false
		}
		if _, duplicate := eventIDs[event.EventID]; duplicate {
			return false
		}
		eventIDs[event.EventID] = struct{}{}
		intent := decision.Outbox[index]
		if !intent.IntentID.Valid() || intent.EventID != event.EventID || intent.Kind == "" {
			return false
		}
		if _, duplicate := intentIDs[intent.IntentID]; duplicate {
			return false
		}
		intentIDs[intent.IntentID] = struct{}{}
	}
	return true
}

func (s *Store) applyRegistryEvent(event kernel.DomainEvent) {
	switch event.EventType {
	case "tekroo.event.execution.registered":
		var payload struct {
			ActorFQN     kernel.ActorFQN `json:"actor_fqn"`
			ExecutionID  kernel.UUIDv7   `json:"execution_id"`
			FencingEpoch uint64          `json:"fencing_epoch"`
		}
		if json.Unmarshal(event.Payload, &payload) == nil {
			s.executions[payload.ActorFQN] = kernel.ExecutionTuple{ExecutionID: payload.ExecutionID, FencingEpoch: payload.FencingEpoch}
		}
	case "tekroo.event.execution.replaced":
		var payload struct {
			ActorFQN        kernel.ActorFQN `json:"actor_fqn"`
			NewExecutionID  kernel.UUIDv7   `json:"new_execution_id"`
			NewFencingEpoch uint64          `json:"new_fencing_epoch"`
		}
		if json.Unmarshal(event.Payload, &payload) == nil {
			s.executions[payload.ActorFQN] = kernel.ExecutionTuple{ExecutionID: payload.NewExecutionID, FencingEpoch: payload.NewFencingEpoch}
		}
	case "tekroo.event.evidence.registered":
		var payload struct {
			SHA256 kernel.Digest `json:"sha256"`
		}
		if json.Unmarshal(event.Payload, &payload) == nil {
			s.evidence[event.Aggregate.ID] = kernel.EvidenceMetadata{SHA256: payload.SHA256, Available: true}
		}
	}
}

func (s *Store) EventCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.events)
}

func (s *Store) AtomicRecordCounts() (states, events, receipts, authority, outbox int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.states), len(s.events), len(s.receipts), len(s.authority), len(s.outbox)
}

func (s *Store) VerifyAggregate(ctx context.Context, target kernel.AggregateRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.RLock()
	events := make([]kernel.DomainEvent, 0)
	for _, event := range s.events {
		if event.Aggregate == target {
			events = append(events, cloneEvent(event))
		}
	}
	stored, found := s.states[target]
	s.mu.RUnlock()
	if !found {
		return ErrInvalidDecision
	}
	sort.Slice(events, func(i, j int) bool { return events[i].AggregateRevision < events[j].AggregateRevision })
	folded, err := kernel.FoldAggregate(events)
	if err != nil {
		return err
	}
	if folded == nil || !reflect.DeepEqual(*folded, stored) {
		return ErrInvalidDecision
	}
	return nil
}

func cloneReceipt(value kernel.CommandReceipt) kernel.CommandReceipt {
	copy := value
	copy.EventIDs = append([]kernel.UUIDv7(nil), value.EventIDs...)
	if value.ResultingRevision != nil {
		revision := *value.ResultingRevision
		copy.ResultingRevision = &revision
	}
	return copy
}

func cloneEvent(value kernel.DomainEvent) kernel.DomainEvent {
	copy := value
	copy.ActorFQN = cloneActor(value.ActorFQN)
	copy.Execution = cloneExecution(value.Execution)
	copy.Parents = append([]kernel.DagParent(nil), value.Parents...)
	copy.Payload = append([]byte(nil), value.Payload...)
	return copy
}

func cloneActor(value *kernel.ActorFQN) *kernel.ActorFQN {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneExecution(value *kernel.ExecutionTuple) *kernel.ExecutionTuple {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneAuthorizationPolicy(value kernel.AuthorizationPolicy) kernel.AuthorizationPolicy {
	copy := value
	copy.Requirements.AttemptLimits = make(map[string]uint32, len(value.Requirements.AttemptLimits))
	for operation, limit := range value.Requirements.AttemptLimits {
		copy.Requirements.AttemptLimits[operation] = limit
	}
	copy.Grants = append([]kernel.AuthorityGrant(nil), value.Grants...)
	for index := range copy.Grants {
		copy.Grants[index].Scope = cloneAuthorityScope(copy.Grants[index].Scope)
	}
	copy.Delegations = append([]kernel.AuthorityDelegation(nil), value.Delegations...)
	for index := range copy.Delegations {
		copy.Delegations[index].Scope = cloneAuthorityScope(copy.Delegations[index].Scope)
	}
	return copy
}

func cloneAttemptBudgetSnapshot(value kernel.AttemptBudgetSnapshot) kernel.AttemptBudgetSnapshot {
	copy := value
	copy.Attempts = make(map[string]kernel.Digest, len(value.Attempts))
	for attempt, digest := range value.Attempts {
		copy.Attempts[attempt] = digest
	}
	return copy
}

func validateAttemptBudgetCommit(budgets map[kernel.AttemptBudgetKey]kernel.AttemptBudgetSnapshot, decision kernel.AttemptBudgetDecision) error {
	if !decision.Valid() {
		return ErrInvalidDecision
	}
	current, found := budgets[decision.Key]
	if !found {
		if decision.ExpectedUsed != 0 {
			return ErrConflict
		}
		return nil
	}
	if current.Limit != decision.Limit || current.Used != decision.ExpectedUsed {
		return ErrConflict
	}
	if prior, duplicate := current.Attempts[decision.AttemptKey]; duplicate && prior != decision.ConditionDigest {
		return ErrConflict
	} else if duplicate {
		return kernel.ErrDecisionAlreadyCommitted
	}
	return nil
}

func applyAttemptBudget(budgets map[kernel.AttemptBudgetKey]kernel.AttemptBudgetSnapshot, decision kernel.AttemptBudgetDecision) {
	current, found := budgets[decision.Key]
	if !found {
		current = kernel.AttemptBudgetSnapshot{Limit: decision.Limit, Attempts: make(map[string]kernel.Digest)}
	} else {
		current = cloneAttemptBudgetSnapshot(current)
	}
	current.Attempts[decision.AttemptKey] = decision.ConditionDigest
	current.Used++
	budgets[decision.Key] = current
}

func cloneAuthorityScope(value kernel.AuthorityScope) kernel.AuthorityScope {
	copy := value
	copy.CommandTypes = append([]string(nil), value.CommandTypes...)
	copy.TargetKinds = append([]kernel.AggregateKind(nil), value.TargetKinds...)
	copy.TargetIDs = append([]kernel.UUIDv7(nil), value.TargetIDs...)
	return copy
}

func cloneAuthorityDecision(value kernel.AuthorityDecision) kernel.AuthorityDecision {
	copy := value
	copy.GrantDigests = append([]kernel.Digest(nil), value.GrantDigests...)
	copy.DelegationDigests = append([]kernel.Digest(nil), value.DelegationDigests...)
	return copy
}
