package memory

import (
	"context"
	"errors"
	"sync"

	"github.com/tekroo-ai/teams/kernel"
)

var (
	ErrConflict                = errors.New("decision precondition conflict")
	ErrReceiptConflict         = errors.New("command receipt conflict")
	ErrCommandIdentityConflict = errors.New("command identity conflict")
	ErrInvalidDecision         = errors.New("invalid decision")
)

type Store struct {
	mu           sync.RWMutex
	states       map[kernel.AggregateRef]kernel.AggregateState
	revisions    map[kernel.AggregateRef]uint64
	receipts     map[kernel.UUIDv7]kernel.CommandReceipt
	fingerprints map[kernel.UUIDv7]kernel.Digest
	events       map[kernel.UUIDv7]kernel.DomainEvent
}

func NewStore() *Store {
	return &Store{
		states:       make(map[kernel.AggregateRef]kernel.AggregateState),
		revisions:    make(map[kernel.AggregateRef]uint64),
		receipts:     make(map[kernel.UUIDv7]kernel.CommandReceipt),
		fingerprints: make(map[kernel.UUIDv7]kernel.Digest),
		events:       make(map[kernel.UUIDv7]kernel.DomainEvent),
	}
}

func (s *Store) Load(ctx context.Context, target kernel.AggregateRef) (kernel.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return kernel.Snapshot{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	revision, found := s.revisions[target]
	if !found {
		return kernel.Snapshot{}, nil
	}
	snapshot := kernel.Snapshot{Exists: true, Revision: revision}
	if state, hasState := s.states[target]; hasState {
		copy := state.Clone()
		snapshot.State = &copy
	}
	return snapshot, nil
}

func (s *Store) LookupReceipt(ctx context.Context, command kernel.KernelCommand) (kernel.CommandReceipt, bool, error) {
	if err := ctx.Err(); err != nil {
		return kernel.CommandReceipt{}, false, err
	}
	fingerprint, err := kernel.CommandFingerprint(command)
	if err != nil {
		return kernel.CommandReceipt{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	receipt, found := s.receipts[command.CommandID]
	if found && s.fingerprints[command.CommandID] != fingerprint {
		return kernel.CommandReceipt{}, false, ErrCommandIdentityConflict
	}
	return cloneReceipt(receipt), found, nil
}

func (s *Store) Commit(ctx context.Context, expected kernel.Snapshot, decision kernel.Decision) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	target := decision.Receipt.Target
	if !decision.CommandFingerprint.Valid() {
		return ErrInvalidDecision
	}
	if decision.NextState != nil && (decision.Receipt.ResultingRevision == nil || decision.NextState.Revision != *decision.Receipt.ResultingRevision) {
		return ErrInvalidDecision
	}
	if decision.Receipt.ResultingRevision != nil && *decision.Receipt.ResultingRevision == 0 {
		return ErrInvalidDecision
	}
	if _, duplicate := s.receipts[decision.Receipt.CommandID]; duplicate {
		if s.fingerprints[decision.Receipt.CommandID] == decision.CommandFingerprint {
			return kernel.ErrDecisionAlreadyCommitted
		}
		return ErrReceiptConflict
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

	if decision.NextState != nil {
		s.states[target] = decision.NextState.Clone()
	}
	if decision.Receipt.ResultingRevision != nil {
		s.revisions[target] = *decision.Receipt.ResultingRevision
	}
	for _, event := range decision.Events {
		s.events[event.EventID] = cloneEvent(event)
	}
	s.receipts[decision.Receipt.CommandID] = cloneReceipt(decision.Receipt)
	s.fingerprints[decision.Receipt.CommandID] = decision.CommandFingerprint
	return nil
}

func (s *Store) EventCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.events)
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
