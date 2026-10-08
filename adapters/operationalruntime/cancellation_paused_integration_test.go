//go:build mongo_integration

package operationalruntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/fake"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

func TestRequestedCancellationSettlesAfterRestartWhileDispatchStaysPaused(t *testing.T) {
	process, uri := startRuntimeMongod(t)
	defer stopRuntimeMongod(process)
	now := time.Now().UTC().Truncate(time.Millisecond)
	fixture := newIntegratedFixture(t, now, 27000, "teams::coder-1", "paused-cancel-workspace", "paused-cancel-worktree")
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
	defer func() {
		if service != nil {
			_ = service.Close(contextWithTimeout(t))
		}
	}()
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
	// Seed a previously running invocation that has yielded its bounded worker.
	var current InvocationStatus
	var found bool
	for i := 0; i < 10; i++ {
		if err := service.Runtime.ProcessOne(contextWithTimeout(t), intent.Intent); !errors.Is(err, application.ErrExternalOutcomeUnknown) {
			t.Fatalf("seed invocation: %v", err)
		}
		current, found, err = service.ReadInvocation(contextWithTimeout(t), fixture.invocationID)
		if err != nil {
			t.Fatal(err)
		}
		if found && current.State == kernel.InvocationStarted {
			break
		}
	}
	if err != nil || !found || current.State != kernel.InvocationStarted {
		t.Fatalf("seed: %+v %v", current, err)
	}
	pending := newIntegratedFixture(t, now, 28000, "teams::coder-2", "phase5-product-workspace-2", "phase5-product-worktree-2")
	pending.modelDigest = fixture.modelDigest
	pending.baselineSHA = fixture.baselineSHA
	pending.createAuthoritativeTask(t, service.Runtime, provenance)
	pending.authorizeInvocation(t, service.Runtime, provenance)
	_, err = service.RequestInvocationCancellation(contextWithTimeout(t), fixture.human, fixture.invocationID, CancellationRequest{ExpectedRevision: current.Revision, Reason: "isolated paused cancellation regression", EvidenceRefs: []kernel.EvidenceRef{{EvidenceID: fixture.evidenceID, SHA256: digestByte('e')}}, IdempotencyKey: "paused-cancel"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Close(contextWithTimeout(t)); err != nil {
		t.Fatal(err)
	}
	service = nil
	service, err = NewProductionService(contextWithTimeout(t), config)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(contextWithTimeout(t)); err != nil {
		t.Fatal(err)
	}
	var settled InvocationStatus
	deadline := time.Now().Add(10 * time.Second)
	for {
		settled, found, err = service.ReadInvocation(contextWithTimeout(t), fixture.invocationID)
		if err != nil {
			t.Fatal(err)
		}
		if found && settled.State.Terminal() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("paused cancellation did not settle: %+v", settled)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || !found || settled.State != kernel.InvocationCancelled || settled.FinishedAt == nil || len(settled.TerminalEvidenceIDs) == 0 {
		t.Fatalf("settled: %+v %v", settled, err)
	}
	if status := service.Controller.Inspect(); status.State != ControlPaused || status.Worker.Active != 0 {
		t.Fatalf("admission changed: %+v", status)
	}
	waiting, found, err := service.ReadInvocation(contextWithTimeout(t), pending.invocationID)
	if err != nil || !found || waiting.State != kernel.InvocationAuthorized {
		t.Fatalf("pending work was admitted: %+v %v", waiting, err)
	}
	if err := service.reconcileRequestedCancellations(contextWithTimeout(t)); err != nil {
		t.Fatal(err)
	}
	replayed, _, err := service.ReadInvocation(contextWithTimeout(t), fixture.invocationID)
	if err != nil || replayed.Revision != settled.Revision {
		t.Fatalf("terminal replay: %+v %v", replayed, err)
	}
	boundary.mu.Lock()
	defer boundary.mu.Unlock()
	if boundary.starts != 1 {
		t.Fatalf("new provider work: %d starts", boundary.starts)
	}
}

// The provider stays running until cancellation; only the Teams database and
// service are replaced. No model or candidate code is executed by this fixture.
type pausedCancellationBoundary struct {
	mu     sync.Mutex
	starts int
}

func (b *pausedCancellationBoundary) observation(brief application.ExecutionBrief, d kernel.Digest, state application.ExternalExecutionState) application.ExternalExecutionObservation {
	return application.ExternalExecutionObservation{InvocationID: brief.InvocationID, RequestDigest: d, ConversationID: string(brief.InvocationID), State: state, ActorFQN: brief.ActorFQN, Execution: brief.Execution, ModelProfileDigest: brief.ModelProfileDigest, RuntimeIdentityDigest: brief.RuntimeIdentityDigest, WorkspaceID: brief.Scope.WorkspaceID, ToolPolicyDigest: brief.ToolPolicyDigest, EffectPolicyDigest: brief.EffectPolicyDigest, Output: []byte("cancelled fixture")}
}
func (b *pausedCancellationBoundary) Start(_ context.Context, brief application.ExecutionBrief, d kernel.Digest) (application.ExternalExecutionObservation, error) {
	b.mu.Lock()
	b.starts++
	b.mu.Unlock()
	return b.observation(brief, d, application.ExternalRunning), nil
}
func (b *pausedCancellationBoundary) ReconcileStart(_ context.Context, brief application.ExecutionBrief, d kernel.Digest) (application.ExternalExecutionObservation, error) {
	return b.observation(brief, d, application.ExternalRunning), nil
}
func (b *pausedCancellationBoundary) Inspect(_ context.Context, brief application.ExecutionBrief, _ string, d kernel.Digest) (application.ExternalExecutionObservation, error) {
	return b.observation(brief, d, application.ExternalRunning), nil
}
func (b *pausedCancellationBoundary) ReconcileSuperseded(_ context.Context, brief application.ExecutionBrief, _ string, d, _ kernel.Digest) (application.ExternalExecutionObservation, error) {
	return b.observation(brief, d, application.ExternalCancelled), nil
}
func (b *pausedCancellationBoundary) Cancel(_ context.Context, brief application.ExecutionBrief, _ string, d kernel.Digest) (application.ExternalExecutionObservation, error) {
	return b.observation(brief, d, application.ExternalCancelled), nil
}
