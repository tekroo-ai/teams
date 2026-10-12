package application

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

type synchronizedInvocationRuntime struct {
	*operationalRuntime
	mu            sync.Mutex
	cancelEntered chan struct{}
	cancelRelease chan struct{}
	winner        CommittedTerminalDecision
	terminalCalls chan terminalRaceCall
}
type terminalRaceCall struct {
	command kernel.KernelCommand
	reply   chan terminalRaceReply
}
type terminalRaceReply struct {
	receipt kernel.CommandReceipt
	err     error
}

func newCancellationRaceRuntime(t *testing.T) *synchronizedInvocationRuntime {
	r := newOperationalRuntime(t)
	r.applyClaim(t)
	r.applyStarted(t)
	at := r.clock.Now()
	inv := r.context.Invocation
	payload, _ := json.Marshal(map[string]any{"invocation_id": inv.ID, "expected_invocation_revision": inv.Revision, "requested_at": at})
	next, ok := kernel.ApplyWorkInvocationEvent(inv, kernel.DomainEvent{EventID: testUUID(903), EventType: "tekroo.event.work-invocation.cancellation-requested", Aggregate: inv.Ref(), AggregateRevision: inv.Revision + 1, Payload: payload})
	if !ok {
		t.Fatal("invalid cancellation fixture")
	}
	r.context.Invocation = next
	r.cancelState = ExternalCancelled
	return &synchronizedInvocationRuntime{operationalRuntime: r}
}
func (r *synchronizedInvocationRuntime) LoadOperationalExecution(ctx context.Context, id kernel.UUIDv7) (OperationalExecutionContext, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, err := r.operationalRuntime.LoadOperationalExecution(ctx, id)
	c.Invocation = c.Invocation.Clone()
	return c, err
}
func (r *synchronizedInvocationRuntime) LoadOperationalExecutionByAuthorizationEvent(ctx context.Context, id kernel.UUIDv7) (OperationalExecutionContext, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, err := r.operationalRuntime.LoadOperationalExecutionByAuthorizationEvent(ctx, id)
	c.Invocation = c.Invocation.Clone()
	return c, err
}
func (r *synchronizedInvocationRuntime) Cancel(ctx context.Context, b ExecutionBrief, c string, d kernel.Digest) (ExternalExecutionObservation, error) {
	r.mu.Lock()
	obs, err := r.operationalRuntime.Cancel(ctx, b, c, d)
	entered, release := r.cancelEntered, r.cancelRelease
	if entered != nil {
		r.cancelEntered = nil
		close(entered)
	}
	r.mu.Unlock()
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return ExternalExecutionObservation{}, ctx.Err()
		}
	}
	return obs, err
}
func (r *synchronizedInvocationRuntime) RecordExecutionEvidence(ctx context.Context, i kernel.WorkInvocation, e []ExecutionEvidence) ([]kernel.EvidenceRef, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.operationalRuntime.RecordExecutionEvidence(ctx, i, e)
}
func (r *synchronizedInvocationRuntime) Handle(ctx context.Context, c kernel.KernelCommand, p kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
	if c.CommandType == "tekroo.command.work-invocation.record-terminal" && r.terminalCalls != nil {
		call := terminalRaceCall{c, make(chan terminalRaceReply, 1)}
		select {
		case r.terminalCalls <- call:
		case <-ctx.Done():
			return kernel.CommandReceipt{}, ctx.Err()
		}
		select {
		case reply := <-call.reply:
			return reply.receipt, reply.err
		case <-ctx.Done():
			return kernel.CommandReceipt{}, ctx.Err()
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commit(c, p)
}
func (r *synchronizedInvocationRuntime) commit(c kernel.KernelCommand, p kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
	receipt, err := r.operationalRuntime.Handle(context.Background(), c, p)
	if err == nil && c.CommandType == "tekroo.command.work-invocation.record-terminal" {
		receipt.ProvenanceDigest = testDigest('a')
		r.winner = CommittedTerminalDecision{Invocation: r.context.Invocation.Clone(), Receipt: receipt, Evidence: append([]kernel.EvidenceRef(nil), c.EvidenceRefs...), CurrentExecution: r.context.CurrentExecution, Event: kernel.DomainEvent{ContractManifest: kernel.ContractIdentity, EventID: receipt.EventIDs[0], EventType: "tekroo.event.work-invocation.terminal-recorded", EventVersion: kernel.OperationalSchemaVersion, Aggregate: c.Target, AggregateRevision: *receipt.ResultingRevision, CommandID: c.CommandID, Authority: c.Authority, Parents: append([]kernel.DagParent(nil), c.Causation...), Payload: append(json.RawMessage(nil), c.Payload...), ProvenanceDigest: receipt.ProvenanceDigest}}
	}
	return receipt, err
}
func (r *synchronizedInvocationRuntime) ReadCommittedTerminalDecision(context.Context, kernel.UUIDv7) (CommittedTerminalDecision, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.winner, r.winner.Invocation.State.Terminal(), nil
}
func cancellationRaceCoordinator(t *testing.T, r *synchronizedInvocationRuntime, at time.Time) *OperationalExecutionCoordinator {
	t.Helper()
	base := newTestOperationalCoordinator(t, r.operationalRuntime)
	c, err := NewOperationalExecutionCoordinator(r, r, r, r, r, &operationalClock{current: at}, base.policy)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

type raceResult struct {
	result OperationalExecutionResult
	err    error
}

func raceAwait[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(3 * time.Second):
		t.Fatal("bounded synchronization timeout")
		var z T
		return z
	}
}
func waitInvocationUsers(t *testing.T, c *OperationalExecutionCoordinator, id kernel.UUIDv7, n int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		c.gateMu.Lock()
		g := c.gates[id]
		users := 0
		if g != nil {
			users = g.users
		}
		c.gateMu.Unlock()
		if users == n {
			return
		}
		runtime.Gosched()
	}
	t.Fatal("gate waiter not observed")
}

func TestInvocationReconciliationSerializesCancellationBothWinnerOrders(t *testing.T) {
	for _, first := range []string{"worker", "cancellation-reconciler"} {
		t.Run(first, func(t *testing.T) {
			r := newCancellationRaceRuntime(t)
			r.cancelEntered = make(chan struct{})
			r.cancelRelease = make(chan struct{})
			entered := r.cancelEntered
			c := cancellationRaceCoordinator(t, r, r.clock.Now())
			results := make(chan raceResult, 2)
			worker := func() { v, e := c.Process(context.Background(), r.intent); results <- raceResult{v, e} }
			cancel := func() {
				v, e := c.ReconcileRequestedCancellation(context.Background(), r.context.Invocation.ID)
				results <- raceResult{v, e}
			}
			if first == "worker" {
				go worker()
			} else {
				go cancel()
			}
			raceAwait(t, entered)
			if first == "worker" {
				go cancel()
			} else {
				go worker()
			}
			waitInvocationUsers(t, c, r.context.Invocation.ID, 2)
			close(r.cancelRelease)
			for i := 0; i < 2; i++ {
				got := raceAwait(t, results)
				if got.err != nil || got.result.State != kernel.InvocationCancelled {
					t.Fatalf("result=%+v err=%v", got.result, got.err)
				}
			}
			r.mu.Lock()
			if r.cancelCalls != 1 || r.evidenceCalls != 1 || len(r.commandTypes) != 1 {
				t.Fatalf("cancel=%d evidence=%d commands=%v", r.cancelCalls, r.evidenceCalls, r.commandTypes)
			}
			settled := r.context.Invocation.Clone()
			r.mu.Unlock()
			// New coordinator simulates restart; durable terminal replay does no work.
			restarted := cancellationRaceCoordinator(t, r, r.clock.Now())
			v, e := restarted.ReconcileRequestedCancellation(context.Background(), settled.ID)
			if e != nil || v.State != kernel.InvocationCancelled || r.cancelCalls != 1 || r.context.Invocation.Revision != settled.Revision {
				t.Fatalf("restart result=%+v err=%v", v, e)
			}
			c.gateMu.Lock()
			defer c.gateMu.Unlock()
			if len(c.gates) != 0 {
				t.Fatal("gate retained after settlement")
			}
		})
	}
}

func TestInvocationReconciliationWaitIsBoundedAndUnrelatedWorkOverlaps(t *testing.T) {
	r := newCancellationRaceRuntime(t)
	c := cancellationRaceCoordinator(t, r, r.clock.Now())
	id := r.context.Invocation.ID
	release, err := c.acquireInvocation(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := c.ReconcileRequestedCancellation(ctx, id); done <- e }()
	waitInvocationUsers(t, c, id, 2)
	other, e := c.acquireInvocation(context.Background(), testUUID(777))
	if e != nil {
		t.Fatalf("unrelated gate blocked: %v", e)
	}
	other()
	cancel()
	if e := raceAwait(t, done); !errors.Is(e, context.Canceled) {
		t.Fatalf("wait err=%v", e)
	}
	ctx2, stop := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer stop()
	if _, e := c.acquireInvocation(ctx2, id); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("deadline err=%v", e)
	}
	release()
	if r.cancelCalls != 0 || len(c.gates) != 0 {
		t.Fatal("waiter performed effects or leaked gate")
	}
	ctx3, stop3 := context.WithCancel(context.Background())
	stop3()
	if _, e := c.acquireInvocation(ctx3, id); !errors.Is(e, context.Canceled) {
		t.Fatal("cancelled caller acquired gate")
	}
}

func TestInvocationTerminalCommitRaceUsesExactDurableWinnerBothOrders(t *testing.T) {
	for _, winnerIndex := range []int{0, 1} {
		t.Run(string(rune('0'+winnerIndex)), func(t *testing.T) {
			r := newCancellationRaceRuntime(t)
			r.terminalCalls = make(chan terminalRaceCall, 2)
			c1 := cancellationRaceCoordinator(t, r, r.clock.Now())
			c2 := cancellationRaceCoordinator(t, r, r.clock.Now().Add(time.Millisecond))
			results := make(chan raceResult, 2)
			go func() { v, e := c1.Process(context.Background(), r.intent); results <- raceResult{v, e} }()
			go func() {
				v, e := c2.ReconcileRequestedCancellation(context.Background(), r.context.Invocation.ID)
				results <- raceResult{v, e}
			}()
			calls := []terminalRaceCall{raceAwait(t, r.terminalCalls), raceAwait(t, r.terminalCalls)}
			if calls[0].command.CommandID == calls[1].command.CommandID {
				t.Fatal("fixture did not reproduce distinct command identities")
			}
			r.mu.Lock()
			receipt, e := r.commit(calls[winnerIndex].command, validTestProvenance())
			r.mu.Unlock()
			if e != nil {
				t.Fatal(e)
			}
			calls[winnerIndex].reply <- terminalRaceReply{receipt: receipt}
			calls[1-winnerIndex].reply <- terminalRaceReply{err: kernel.ErrDecisionPreconditionConflict}
			for i := 0; i < 2; i++ {
				got := raceAwait(t, results)
				if got.err != nil || got.result.State != kernel.InvocationCancelled || got.result.Receipt == nil || got.result.Receipt.CommandID != receipt.CommandID {
					t.Fatalf("result=%+v err=%v", got.result, got.err)
				}
			}
			if r.startCalls != 0 || r.context.Invocation.Revision != 5 || len(r.commandTypes) != 1 {
				t.Fatalf("start=%d revision=%d commands=%v", r.startCalls, r.context.Invocation.Revision, r.commandTypes)
			}
		})
	}
}

func TestInvocationTerminalWinnerRejectsEveryMaterialMismatch(t *testing.T) {
	changes := map[string]func(*CommittedTerminalDecision){
		"outcome":          func(w *CommittedTerminalDecision) { w.Invocation.State = kernel.InvocationSucceeded },
		"fence":            func(w *CommittedTerminalDecision) { w.CurrentExecution.FencingEpoch++ },
		"actor":            func(w *CommittedTerminalDecision) { w.Invocation.ActorFQN = "teams::coder-2" },
		"budget":           func(w *CommittedTerminalDecision) { w.Invocation.RemainingGlobalBudget++ },
		"route":            func(w *CommittedTerminalDecision) { w.Invocation.OutputPredicateDigest = testDigest('e') },
		"evidence-digest":  func(w *CommittedTerminalDecision) { w.Evidence[0].SHA256 = testDigest('e') },
		"evidence-missing": func(w *CommittedTerminalDecision) { w.Evidence = nil },
		"receipt":          func(w *CommittedTerminalDecision) { w.Receipt.OutcomeCode = kernel.OutcomeRejectedConflict },
		"receipt-event":    func(w *CommittedTerminalDecision) { w.Receipt.EventIDs = []kernel.UUIDv7{testUUID(999)} },
		"authority": func(w *CommittedTerminalDecision) {
			w.Event.Authority = kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "different-service"}
		},
		"causation":  func(w *CommittedTerminalDecision) { w.Event.Parents = nil },
		"provenance": func(w *CommittedTerminalDecision) { w.Event.ProvenanceDigest = testDigest('e') },
		"revision":   func(w *CommittedTerminalDecision) { w.Invocation.Revision++ },
		"retryable":  func(w *CommittedTerminalDecision) { b := true; w.Invocation.Retryable = &b },
		"request":    func(w *CommittedTerminalDecision) { d := testDigest('e'); w.Invocation.RequestDigest = &d },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			r := newCancellationRaceRuntime(t)
			c := cancellationRaceCoordinator(t, r, r.clock.Now())
			prior := r.context.Invocation.Clone()
			got, err := c.ReconcileRequestedCancellation(context.Background(), prior.ID)
			if err != nil {
				t.Fatal(err)
			}
			var attempted operationalTerminalPayload
			if json.Unmarshal(r.winner.Event.Payload, &attempted) != nil {
				t.Fatal("payload")
			}
			refs := append([]kernel.EvidenceRef(nil), got.Evidence...)
			_, match, err := c.matchCommittedTerminal(context.Background(), prior, attempted, refs)
			if err != nil || !match {
				t.Fatalf("exact winner not recognized: %v", err)
			}
			change(&r.winner)
			_, match, err = c.matchCommittedTerminal(context.Background(), prior, attempted, refs)
			if err != nil || match {
				t.Fatalf("mismatch accepted: %v", err)
			}
		})
	}
}

func TestInvocationGateConcurrentCleanupAndReuse(t *testing.T) {
	r := newCancellationRaceRuntime(t)
	c := cancellationRaceCoordinator(t, r, r.clock.Now())
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				release, e := c.acquireInvocation(context.Background(), testUUID(777))
				if e != nil {
					t.Error(e)
					return
				}
				release()
			}
		}()
	}
	wg.Wait()
	if !reflect.DeepEqual(c.gates, map[kernel.UUIDv7]*invocationReconciliationGate{}) {
		t.Fatal("leaked gates")
	}
}

type terminalErrorCommands struct{ err error }

func (c terminalErrorCommands) Handle(context.Context, kernel.KernelCommand, kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
	return kernel.CommandReceipt{}, c.err
}

type countedTerminalReader struct {
	OperationalExecutionReader
	proof CommittedTerminalDecision
	calls int
	err   error
}

func (r *countedTerminalReader) ReadCommittedTerminalDecision(context.Context, kernel.UUIDv7) (CommittedTerminalDecision, bool, error) {
	r.calls++
	return r.proof, true, r.err
}
func TestInvocationTerminalRaceDoesNotHideGenericErrorsOrMissingProof(t *testing.T) {
	r := newCancellationRaceRuntime(t)
	c := cancellationRaceCoordinator(t, r, r.clock.Now())
	prior := r.context
	brief, d, err := c.buildBrief(context.Background(), prior)
	if err != nil {
		t.Fatal(err)
	}
	obs := r.observation(brief, d, ExternalCancelled)
	if _, err := c.recordTerminalObservation(context.Background(), prior, obs, kernel.InvocationCancelled); err != nil {
		t.Fatal(err)
	}
	for _, err := range []error{errors.New("database unavailable"), kernel.ErrCommitUncertain, kernel.ErrCommandIdentityConflict, kernel.ErrIdempotencyKeyConflict} {
		p := &countedTerminalReader{OperationalExecutionReader: r, proof: r.winner}
		c.reader = p
		c.commands = terminalErrorCommands{err}
		_, got := c.recordTerminalObservation(context.Background(), prior, obs, kernel.InvocationCancelled)
		if !errors.Is(got, err) || p.calls != 0 {
			t.Fatalf("generic failure hidden: %v reads=%d", got, p.calls)
		}
	}
	c.reader = struct{ OperationalExecutionReader }{r}
	c.commands = terminalErrorCommands{kernel.ErrDecisionPreconditionConflict}
	if _, err := c.recordTerminalObservation(context.Background(), prior, obs, kernel.InvocationCancelled); !errors.Is(err, kernel.ErrDecisionPreconditionConflict) {
		t.Fatalf("missing proof accepted: %v", err)
	}
	p := &countedTerminalReader{OperationalExecutionReader: r, proof: r.winner, err: context.DeadlineExceeded}
	c.reader = p
	if _, err := c.recordTerminalObservation(context.Background(), prior, obs, kernel.InvocationCancelled); !errors.Is(err, kernel.ErrDecisionPreconditionConflict) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("proof failure hidden: %v", err)
	}
}

type invocationIdentityProbe struct {
	*synchronizedInvocationRuntime
	identityCalls, fullReads, authorizationReads int
	id                                           kernel.UUIDv7
	found                                        bool
	err                                          error
}

func (p *invocationIdentityProbe) ReadInvocationIDForAuthorization(context.Context, kernel.UUIDv7) (kernel.UUIDv7, bool, error) {
	p.identityCalls++
	return p.id, p.found, p.err
}
func (p *invocationIdentityProbe) LoadOperationalExecution(ctx context.Context, id kernel.UUIDv7) (OperationalExecutionContext, error) {
	p.fullReads++
	return p.synchronizedInvocationRuntime.LoadOperationalExecution(ctx, id)
}
func (p *invocationIdentityProbe) LoadOperationalExecutionByAuthorizationEvent(ctx context.Context, id kernel.UUIDv7) (OperationalExecutionContext, error) {
	p.authorizationReads++
	return p.synchronizedInvocationRuntime.LoadOperationalExecutionByAuthorizationEvent(ctx, id)
}
func TestInvocationIdentityLookupAvoidsDuplicateFullAuthorityRead(t *testing.T) {
	for _, supported := range []bool{false, true} {
		r := newCancellationRaceRuntime(t)
		c := cancellationRaceCoordinator(t, r, r.clock.Now())
		p := &invocationIdentityProbe{synchronizedInvocationRuntime: r, found: supported}
		if supported {
			p.id = r.context.Invocation.ID
		}
		c.reader = p
		v, e := c.Process(context.Background(), r.intent)
		wantAuth := 1
		if supported {
			wantAuth = 0
		}
		if e != nil || v.State != kernel.InvocationCancelled || p.identityCalls != 1 || p.fullReads != 1 || p.authorizationReads != wantAuth {
			t.Fatalf("supported=%v result=%+v err=%v identity=%d full=%d authorization=%d", supported, v, e, p.identityCalls, p.fullReads, p.authorizationReads)
		}
	}
}
func TestInvocationIdentityInvalidAndFailedLookupCannotPerformEffects(t *testing.T) {
	for _, probe := range []invocationIdentityProbe{{found: true, id: "invalid"}, {found: false, id: testUUID(777)}, {err: errors.New("authorization unavailable")}, {found: true, id: testUUID(777)}} {
		r := newCancellationRaceRuntime(t)
		c := cancellationRaceCoordinator(t, r, r.clock.Now())
		p := probe
		p.synchronizedInvocationRuntime = r
		c.reader = &p
		_, err := c.Process(context.Background(), r.intent)
		if err == nil || r.cancelCalls != 0 || r.startCalls != 0 || r.evidenceCalls != 0 {
			t.Fatalf("invalid identity performed work: err=%v", err)
		}
	}
}
