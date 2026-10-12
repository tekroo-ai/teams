package application

import (
	"context"
	"encoding/json"
	"reflect"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

type invocationReconciliationGate struct {
	token chan struct{}
	users int
}

// Gates are local to an invocation, not a role or the worker fleet. Count
// holders and waiters so releasing/cancelling the last user removes the entry.
func (coordinator *OperationalExecutionCoordinator) acquireInvocation(ctx context.Context, id kernel.UUIDv7) (func(), error) {
	if !id.Valid() {
		return nil, ErrInvalidOperationalExecution
	}
	waitCtx, cancel := context.WithTimeout(ctx, coordinator.policy.OperationTimeout)
	defer cancel()
	if err := waitCtx.Err(); err != nil {
		return nil, err
	}
	coordinator.gateMu.Lock()
	if coordinator.gates == nil {
		coordinator.gates = make(map[kernel.UUIDv7]*invocationReconciliationGate)
	}
	gate := coordinator.gates[id]
	if gate == nil {
		gate = &invocationReconciliationGate{token: make(chan struct{}, 1)}
		coordinator.gates[id] = gate
	}
	gate.users++
	coordinator.gateMu.Unlock()
	drop := func() {
		coordinator.gateMu.Lock()
		gate.users--
		if gate.users == 0 {
			delete(coordinator.gates, id)
		}
		coordinator.gateMu.Unlock()
	}
	select {
	case gate.token <- struct{}{}:
		if err := waitCtx.Err(); err != nil {
			<-gate.token
			drop()
			return nil, err
		}
		return func() { <-gate.token; drop() }, nil
	case <-waitCtx.Done():
		drop()
		return nil, waitCtx.Err()
	}
}

type operationalTerminalPayload struct {
	InvocationID     kernel.UUIDv7              `json:"invocation_id"`
	ExpectedRevision uint64                     `json:"expected_invocation_revision"`
	ClaimID          kernel.UUIDv7              `json:"claim_id"`
	Outcome          kernel.WorkInvocationState `json:"outcome"`
	Retryable        bool                       `json:"retryable"`
	EvidenceIDs      []kernel.UUIDv7            `json:"evidence_ids"`
	OutputDigest     kernel.Digest              `json:"output_digest"`
	FinishedAt       time.Time                  `json:"finished_at"`
}

// CommittedTerminalDecision is authoritative durable data, never a provider
// assertion. Readers must bind event, applied receipt, folded invocation and
// currently available evidence metadata. It is used only after a commit race.
type CommittedTerminalDecision struct {
	Invocation       kernel.WorkInvocation
	Event            kernel.DomainEvent
	Receipt          kernel.CommandReceipt
	Evidence         []kernel.EvidenceRef
	CurrentExecution kernel.ExecutionTuple
}

type OperationalTerminalDecisionReader interface {
	ReadCommittedTerminalDecision(context.Context, kernel.UUIDv7) (CommittedTerminalDecision, bool, error)
}

// Optional narrow lookup avoids loading a full execution snapshot merely to
// identify its gate. The full authority read still happens after acquisition.
type OperationalAuthorizationIdentityReader interface {
	ReadInvocationIDForAuthorization(context.Context, kernel.UUIDv7) (kernel.UUIDv7, bool, error)
}

func (coordinator *OperationalExecutionCoordinator) matchCommittedTerminal(ctx context.Context, prior kernel.WorkInvocation, attempted operationalTerminalPayload, refs []kernel.EvidenceRef) (CommittedTerminalDecision, bool, error) {
	reader, ok := coordinator.reader.(OperationalTerminalDecisionReader)
	if !ok {
		return CommittedTerminalDecision{}, false, nil // No proof: preserve failure.
	}
	winner, found, err := reader.ReadCommittedTerminalDecision(ctx, prior.ID)
	if err != nil || !found {
		return winner, false, err
	}
	event, receipt, inv := winner.Event, winner.Receipt, winner.Invocation
	if !inv.Valid() || !inv.State.Terminal() || winner.CurrentExecution != prior.Execution || event.ContractManifest != kernel.ContractIdentity || event.EventType != "tekroo.event.work-invocation.terminal-recorded" || event.EventVersion != kernel.OperationalSchemaVersion || !event.CommandID.Valid() || event.Aggregate != prior.Ref() || event.AggregateRevision != prior.Revision+1 || event.EventID != inv.LastEventID || event.Authority != coordinator.policy.ServiceAuthority || len(event.Parents) != 1 || event.Parents[0] != (kernel.DagParent{ParentEventID: prior.LastEventID, EdgeKind: kernel.EdgeCausal}) || !event.ProvenanceDigest.Valid() || receipt.ProvenanceDigest != event.ProvenanceDigest || receipt.ContractManifest != kernel.ContractIdentity || receipt.CommandID != event.CommandID || receipt.CommandType != "tekroo.command.work-invocation.record-terminal" || receipt.Target != prior.Ref() || receipt.OutcomeCode != kernel.OutcomeApplied || !receipt.StateChanged || receipt.ResultingRevision == nil || *receipt.ResultingRevision != event.AggregateRevision || len(receipt.EventIDs) != 1 || receipt.EventIDs[0] != event.EventID {
		return winner, false, nil
	}
	var recorded operationalTerminalPayload
	if json.Unmarshal(event.Payload, &recorded) != nil || recorded.FinishedAt.IsZero() {
		return winner, false, nil
	}
	// Competing observers may choose different finish timestamps. Preserve the
	// committed timestamp/receipt; every other payload field must match exactly.
	attempted.FinishedAt = recorded.FinishedAt
	if !reflect.DeepEqual(attempted, recorded) || !reflect.DeepEqual(refs, winner.Evidence) {
		return winner, false, nil
	}
	folded, valid := kernel.ApplyWorkInvocationEvent(prior, event)
	// Rare conflict path: full comparison prevents omitted identity/budget/route
	// fields from accidentally turning a different execution into a duplicate.
	return winner, valid && reflect.DeepEqual(folded, inv), nil
}
