//go:build mongo_integration

package operationalruntime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/fake"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

type terminalCommitBarrierCall struct {
	expected kernel.Snapshot
	decision kernel.Decision
	result   chan error
}
type terminalCommitBarrier struct {
	kernel.KernelDecisionStore
	calls chan terminalCommitBarrierCall
}

func (b *terminalCommitBarrier) Commit(ctx context.Context, expected kernel.Snapshot, decision kernel.Decision) error {
	if decision.Receipt.CommandType != "tekroo.command.work-invocation.record-terminal" {
		return b.KernelDecisionStore.Commit(ctx, expected, decision)
	}
	call := terminalCommitBarrierCall{expected, decision, make(chan error, 1)}
	select {
	case b.calls <- call:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case v := <-call.result:
		return v
	case <-ctx.Done():
		return ctx.Err()
	}
}
func boundedTerminalCall(t *testing.T, c <-chan terminalCommitBarrierCall) terminalCommitBarrierCall {
	t.Helper()
	select {
	case v := <-c:
		return v
	case <-time.After(10 * time.Second):
		t.Fatal("two terminal commands did not reach bounded barrier")
		return terminalCommitBarrierCall{}
	}
}

// Two separate coordinators have independent local gates (as on restart or
// competing processes). The barrier controls both winner orders at the actual
// Mongo CAS boundary; neither validation nor the conflict itself is mocked.
func TestTerminalCommitRaceReconcilesActualMongoWinnerBothOrders(t *testing.T) {
	process, uri := startRuntimeMongod(t)
	defer stopRuntimeMongod(process)
	for _, winnerIndex := range []int{0, 1} {
		t.Run(string(rune('0'+winnerIndex)), func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Millisecond)
			fixture := newIntegratedFixture(t, now, 37000+winnerIndex*1000, "teams::coder-1", "phase5-product-workspace", "phase5-product-worktree")
			workspace := filepath.Join(t.TempDir(), "workspace")
			if err := os.Mkdir(workspace, 0700); err != nil {
				t.Fatal(err)
			}
			path := writeProductSurfaceConfiguration(t, uri, "http://127.0.0.1:1", workspace, fixture)
			config, err := LoadProductionConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			config.Worker.StartPaused = true
			config.Worker.MaximumReconciliations = 1
			boundary := &pausedCancellationBoundary{}
			config.QualificationExecutionBoundary = boundary
			service, err := NewProductionService(contextWithTimeout(t), config)
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close(contextWithTimeout(t))
			if err := service.Start(contextWithTimeout(t)); err != nil {
				t.Fatal(err)
			}
			provenance, err := fake.ProvenanceBasis()
			if err != nil {
				t.Fatal(err)
			}
			fixture.createAuthoritativeTask(t, service.Runtime, provenance)
			fixture.authorizeInvocation(t, service.Runtime, provenance)
			intent, err := service.Runtime.feed.Next(contextWithTimeout(t))
			if err != nil {
				t.Fatal(err)
			}
			if err := service.Runtime.ProcessOne(contextWithTimeout(t), intent.Intent); !errors.Is(err, application.ErrExternalOutcomeUnknown) {
				t.Fatalf("seed: %v", err)
			}
			current, found, err := service.ReadInvocation(contextWithTimeout(t), fixture.invocationID)
			if err != nil || !found || current.State != kernel.InvocationStarted {
				t.Fatalf("seed state=%+v err=%v", current, err)
			}
			// Stop background reconciliation before controlling two independent
			// coordinators. The seeded invocation and its authority stay durable.
			if err := service.Stop(contextWithTimeout(t)); err != nil {
				t.Fatal(err)
			}
			_, err = service.RequestInvocationCancellation(contextWithTimeout(t), fixture.human, fixture.invocationID, CancellationRequest{ExpectedRevision: current.Revision, Reason: "bounded terminal race regression", EvidenceRefs: []kernel.EvidenceRef{{EvidenceID: fixture.evidenceID, SHA256: digestByte('e')}}, IdempotencyKey: "terminal-race-cancel"})
			if err != nil {
				t.Fatal(err)
			}
			barrier := &terminalCommitBarrier{KernelDecisionStore: service.Store, calls: make(chan terminalCommitBarrierCall, 2)}
			tracingHandler, err := application.NewHandler(barrier, kernel.Evaluator{Catalogue: service.Runtime.catalogue}, SystemClock{}, service.ids)
			if err != nil {
				t.Fatal(err)
			}
			recorder, err := application.NewCommandEvidenceRecorder(service.Runtime.handler, service.Runtime.evidence, application.CommandEvidenceRecorderPolicy{PolicyRevision: 1, Authority: fixture.service, Provenance: provenance, ProducingVersion: "terminal-race-regression", RetentionPolicy: "terminal-race-regression"})
			if err != nil {
				t.Fatal(err)
			}
			makeCoordinator := func(at time.Time) *application.OperationalExecutionCoordinator {
				c, err := application.NewOperationalExecutionCoordinator(assembleOperationalExecutionReader(service.Store, nil), tracingHandler, boundary, recorder, service.roleGrounding, fixedClock{now: at}, application.OperationalExecutionPolicy{OperationTimeout: 3 * time.Second, MaximumBriefBytes: 1 << 20, ConsumerID: config.Execution.ConsumerID, PolicyRevision: 1, ServiceAuthority: fixture.service, ExpiryAuthority: fixture.policy, Provenance: provenance})
				if err != nil {
					t.Fatal(err)
				}
				return c
			}
			c1, c2 := makeCoordinator(now.Add(time.Second)), makeCoordinator(now.Add(time.Second+time.Millisecond))
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			type result struct {
				value application.OperationalExecutionResult
				err   error
			}
			done := make(chan result, 2)
			go func() { v, e := c1.Process(ctx, intent.Intent); done <- result{v, e} }()
			go func() { v, e := c2.ReconcileRequestedCancellation(ctx, fixture.invocationID); done <- result{v, e} }()
			calls := []terminalCommitBarrierCall{boundedTerminalCall(t, barrier.calls), boundedTerminalCall(t, barrier.calls)}
			if calls[0].decision.Receipt.CommandID == calls[1].decision.Receipt.CommandID {
				t.Fatal("distinct timestamp identities not reproduced")
			}
			var payloads [2]map[string]any
			for i := range calls {
				if calls[i].expected.Revision != 4 || calls[i].decision.Receipt.OutcomeCode != kernel.OutcomeApplied || len(calls[i].decision.Events) != 1 {
					t.Fatal("barrier did not retain two valid revision-4 decisions")
				}
				if err := json.Unmarshal(calls[i].decision.Events[0].Payload, &payloads[i]); err != nil {
					t.Fatal(err)
				}
			}
			winning := calls[winnerIndex].decision.Receipt
			e := service.Store.Commit(ctx, calls[winnerIndex].expected, calls[winnerIndex].decision)
			if e != nil {
				t.Fatal(e)
			}
			calls[winnerIndex].result <- nil
			e = service.Store.Commit(ctx, calls[1-winnerIndex].expected, calls[1-winnerIndex].decision)
			if !errors.Is(e, kernel.ErrDecisionPreconditionConflict) {
				t.Fatalf("unexpected loser: %v", e)
			}
			calls[1-winnerIndex].result <- e
			for i := 0; i < 2; i++ {
				select {
				case got := <-done:
					if got.err != nil || got.value.State != kernel.InvocationCancelled || got.value.Receipt == nil || got.value.Receipt.CommandID != winning.CommandID {
						t.Fatalf("result=%+v err=%v", got.value, got.err)
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			terminal, found, e := service.Store.ReadCommittedTerminalDecision(ctx, fixture.invocationID)
			if e != nil || !found || terminal.Receipt.CommandID != winning.CommandID || terminal.Invocation.Revision != *winning.ResultingRevision {
				t.Fatalf("terminal=%+v err=%v", terminal, e)
			}
			if len(terminal.Evidence) == 0 || boundary.starts != 1 {
				t.Fatalf("evidence=%v provider starts=%d", terminal.Evidence, boundary.starts)
			}
			// A fresh coordinator must see settled work without another boundary call.
			replayed, e := makeCoordinator(now.Add(2*time.Second)).ReconcileRequestedCancellation(ctx, fixture.invocationID)
			if e != nil || replayed.State != kernel.InvocationCancelled {
				t.Fatalf("restart=%+v err=%v", replayed, e)
			}
		})
	}
}
