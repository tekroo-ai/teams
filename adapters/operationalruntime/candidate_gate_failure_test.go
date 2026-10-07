package operationalruntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGateFailureSurvivesRestartAndRequiresChangedInputs(t *testing.T) {
	dir, _, _ := candidateRepository(t)
	gate := ProductionCandidateGate{GateID: "compile", Command: []string{"git", "show", "HEAD:missing"}, Timeout: "1s"}
	manager := &candidateWorkspaceManager{root: t.TempDir(), gitBinary: "git", timeout: time.Second, gates: map[string]ProductionCandidateGate{gate.GateID: gate}}
	first, err := manager.runGates(t.Context(), dir, []string{gate.GateID})
	var failure *candidateGateFailure
	if !errors.As(err, &failure) || len(first) != 1 || first[0].ExitCode <= 0 {
		t.Fatalf("missing negative finding: %+v %v", first, err)
	}
	restarted := &candidateWorkspaceManager{root: manager.root, gitBinary: "git", timeout: time.Second, gates: manager.gates}
	for i := 0; i < 4; i++ {
		second, err := restarted.runGates(t.Context(), dir, []string{gate.GateID})
		if !errors.As(err, &failure) || len(second) != 1 || second[0].ReceiptID != first[0].ReceiptID {
			t.Fatalf("negative finding lost: %+v %v", second, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(manager.root, "gate-attempts"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("unchanged failure executed %d times: %v", len(entries), err)
	}
	if _, err := os.Stat(filepath.Join(manager.root, "gate-cache")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("negative finding entered successful-result cache", err)
	}
	commitTaskFile(t, dir, "missing", "now supplied\n", "changed candidate")
	if _, err := restarted.runGates(t.Context(), dir, []string{gate.GateID}); err != nil {
		t.Fatal("successor candidate could not be checked", err)
	}
	entries, err = os.ReadDir(filepath.Join(manager.root, "gate-attempts"))
	if err != nil || len(entries) != 2 {
		t.Fatalf("successor candidate not executed exactly once: %d %v", len(entries), err)
	}
}

func TestGateCancellationIsNotACandidateFailure(t *testing.T) {
	dir, _, _ := candidateRepository(t)
	gate := ProductionCandidateGate{GateID: "long", Command: []string{"/bin/sleep", "30"}, Timeout: "1m"}
	manager := &candidateWorkspaceManager{root: t.TempDir(), gitBinary: "git", timeout: time.Second, gates: map[string]ProductionCandidateGate{gate.GateID: gate}}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := manager.runGates(ctx, dir, []string{gate.GateID})
	if err == nil || errors.Is(err, errCandidateGateFailed) || time.Since(start) > 3*time.Second {
		t.Fatal("cancellation became a candidate failure or did not kill the gate", err)
	}
	if _, err := os.Stat(filepath.Join(manager.root, "gate-failures")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancellation persisted as a compiler finding", err)
	}
}

func TestExplicitVerificationInputChangePermitsNewCheckWithoutPassReuse(t *testing.T) {
	dir, _, _ := candidateRepository(t)
	gate := ProductionCandidateGate{GateID: "compile", Command: []string{"git", "show", "HEAD:missing"}, Timeout: "1s"}
	manager := &candidateWorkspaceManager{root: t.TempDir(), gitBinary: "git", timeout: time.Second, gates: map[string]ProductionCandidateGate{gate.GateID: gate}}
	_, err := manager.runGates(t.Context(), dir, []string{gate.GateID})
	if !errors.Is(err, errCandidateGateFailed) {
		t.Fatal(err)
	}
	gate.VerificationInputsSHA256 = candidateTestDigest('a')
	manager.gates[gate.GateID] = gate
	_, err = manager.runGates(t.Context(), dir, []string{gate.GateID})
	if !errors.Is(err, errCandidateGateFailed) {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(manager.root, "gate-attempts"))
	if err != nil || len(entries) != 2 {
		t.Fatalf("explicit changed inputs did not permit one new check: %d %v", len(entries), err)
	}
	if _, err := os.Stat(filepath.Join(manager.root, "gate-cache")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("verification input identity enabled PASS reuse", err)
	}
}

func TestPausedFeatureRecoveryCancelsActiveOperation(t *testing.T) {
	service := &ProductionService{Controller: &Controller{state: ControlRunning}, planningDeadline: time.Minute}
	ctx, cancel := service.featureRecoveryOperationContext(t.Context())
	defer cancel()
	service.Controller.mu.Lock()
	service.Controller.state = ControlPaused
	service.Controller.mu.Unlock()
	service.cancelFeatureRecovery()
	if !errors.Is(ctx.Err(), context.Canceled) || !service.newInvocationAdmissionBlocked() {
		t.Fatal("pause left operation or invocation admission enabled")
	}
	next, stop := service.featureRecoveryOperationContext(t.Context())
	defer stop()
	if !errors.Is(next.Err(), context.Canceled) {
		t.Fatal("pause admitted a later recovery operation")
	}
}
