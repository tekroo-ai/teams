package operationalruntime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/openhands"
	"github.com/tekroo-ai/teams/internal/verification"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestCandidateAdmissionBoundsBroadAndFocusedJobsWithoutBlocking(t *testing.T) {
	p := &candidatePreflights{}
	lifetime, cancel := context.WithCancel(t.Context())
	defer func() { cancel(); p.wait.Wait() }()
	binding := candidateAdmissionContext{lifetime: lifetime, wake: make(chan struct{}, 4), timeout: time.Minute}
	started := make(chan string, 4)
	finish := make(chan struct{})
	run := func(name string) func(context.Context) ([]candidateGateReceipt, error) {
		return func(ctx context.Context) ([]candidateGateReceipt, error) {
			started <- name
			select {
			case <-finish:
				return nil, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	admit := func(key string, class verification.Class) {
		t.Helper()
		if _, err := p.admit(t.Context(), key, class, binding, run(key)); !errors.Is(err, errCandidatePreflightPending) {
			t.Fatalf("admission %s: %v", key, err)
		}
	}
	admit("broad", verification.WholeModule)
	admit("second-broad", verification.WholeModule)
	admit("focused", verification.Focused)
	for i := 0; i < 2; i++ {
		select {
		case name := <-started:
			if name == "second-broad" {
				t.Fatal("second broad displaced focused")
			}
		case <-time.After(time.Second):
			t.Fatal("focused handoff blocked")
		}
	}
	admit("third", verification.Focused)
	select {
	case name := <-started:
		t.Fatalf("exceeded two preparations: %s", name)
	default:
	}
	close(finish)
	p.wait.Wait()
	for _, key := range []string{"broad", "focused"} {
		if _, err := p.admit(t.Context(), key, verification.Focused, binding, run("unexpected")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCandidateAsyncPreparationRechecksChangedSourceBeforeAdmission(t *testing.T) {
	source, baseline, original := candidateRepository(t)
	resolver, err := openhands.NewBoundWorkspaceResolver([]openhands.WorkspaceBinding{{WorkspaceID: "tester-1", WorktreeID: "default", WorkingDirectory: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	m, err := newCandidateWorkspaceManager(t.TempDir(), "git", time.Minute, []ProductionCandidateGate{{GateID: "head", Command: []string{"git", "rev-parse", "HEAD"}, Timeout: "10s"}}, resolver)
	if err != nil {
		t.Fatal(err)
	}
	lifetime, cancel := context.WithCancel(t.Context())
	defer func() { cancel(); m.preflights.wait.Wait() }()
	ctx := context.WithValue(t.Context(), candidateAdmissionContextKey{}, candidateAdmissionContext{lifetime: lifetime, wake: make(chan struct{}, 2), timeout: time.Minute})
	feature := organization.FeatureRequest{ID: candidateTestUUID(2301)}
	consumer := organization.PlannedTask{ID: candidateTestUUID(2302), Owner: "teams::tester-1"}
	targets := []candidateTargetReceipt{{TaskID: candidateTestUUID(2303), InvocationID: candidateTestUUID(2304), OutputSHA256: candidateTestDigest('a'), TerminalEvidenceIDs: []kernel.UUIDv7{candidateTestUUID(2305)}}}
	sourceWorkspace := ProductionWorkspace{WorkspaceID: "coder-1", WorktreeID: "source", WorkingDirectory: source, Branch: "source", BaselineSHA: baseline, WritablePaths: []string{"."}}
	prepare := func() (candidateReceipt, error) {
		_, r, _, err := m.Prepare(ctx, feature, consumer, "tester-1", sourceWorkspace, targets, []string{"head"})
		return r, err
	}
	if _, err := prepare(); !errors.Is(err, errCandidatePreflightPending) {
		t.Fatalf("first admission: %v", err)
	}
	m.preflights.wait.Wait()
	commitTaskFile(t, source, "later.txt", "changed after gate\n", "new candidate")
	if _, err := prepare(); !errors.Is(err, errCandidatePreflightPending) {
		t.Fatalf("changed source bypassed new gate: %v", err)
	}
	m.preflights.wait.Wait()
	r, err := prepare()
	if err != nil {
		t.Fatal(err)
	}
	if r.CandidateCommit == original || r.CandidateCommit != candidateGit(t, source, "rev-parse", "HEAD") {
		t.Fatal("old preflight admitted changed source")
	}
	makeWritableForCleanup(t, r.MaterializedWorkspace)
}

func TestCandidateAdmissionRetainsFailureAndOwnsServiceLifetime(t *testing.T) {
	p := &candidatePreflights{}
	lifetime, cancel := context.WithCancel(t.Context())
	defer func() { cancel(); p.wait.Wait() }()
	binding := candidateAdmissionContext{lifetime: lifetime, wake: make(chan struct{}, 1), timeout: time.Minute}
	pass, cancelPass := context.WithCancel(t.Context())
	started := make(chan context.Context, 1)
	finish := make(chan struct{})
	failure := errors.New("observed gate failure")
	run := func(ctx context.Context) ([]candidateGateReceipt, error) {
		started <- ctx
		<-finish
		return []candidateGateReceipt{{GateID: "gate"}}, failure
	}
	if _, err := p.admit(pass, "candidate", verification.Focused, binding, run); !errors.Is(err, errCandidatePreflightPending) {
		t.Fatal(err)
	}
	jobContext := <-started
	cancelPass()
	if jobContext.Err() != nil {
		t.Fatal("reconciliation pass canceled the gate")
	}
	close(finish)
	p.wait.Wait()
	// Returning from reconciliation cannot cancel a successfully settled job.
	if !errors.Is(jobContext.Err(), context.Canceled) {
		t.Fatal("settled context not released")
	}
	receipts, err := p.admit(t.Context(), "candidate", verification.Focused, binding, func(context.Context) ([]candidateGateReceipt, error) {
		t.Fatal("gate rerun instead of consuming result")
		return nil, nil
	})
	if !errors.Is(err, failure) || len(receipts) != 1 {
		t.Fatalf("failure lost: %+v %v", receipts, err)
	}
	waitStarted := make(chan struct{})
	p.admit(t.Context(), "cancel", verification.Focused, binding, func(ctx context.Context) ([]candidateGateReceipt, error) {
		close(waitStarted)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	<-waitStarted
	p.cancelAll()
	p.wait.Wait()
	_, err = p.admit(t.Context(), "cancel", verification.Focused, binding, run)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("pause lost cancellation receipt: %v", err)
	}
}

func TestCandidateGateRejectsLateSourceChangeBeforeExecuting(t *testing.T) {
	root, baseline, _ := candidateRepository(t)
	m := &candidateWorkspaceManager{gitBinary: "git", timeout: time.Minute, gates: map[string]ProductionCandidateGate{"head": {GateID: "head", Command: []string{"git", "rev-parse", "HEAD"}, Timeout: "10s"}}}
	ctx := context.WithValue(t.Context(), candidateGateIdentityContextKey{}, candidateGateIdentity{commit: baseline, tree: candidateGit(t, root, "rev-parse", baseline+"^{tree}")})
	receipts, err := m.runGates(ctx, root, []string{"head"})
	if !errors.Is(err, errInvalidCandidateWorkspace) || len(receipts) != 0 {
		t.Fatalf("late change executed a gate: %+v %v", receipts, err)
	}
}
