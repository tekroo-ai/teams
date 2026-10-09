package operationalruntime

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/internal/verification"
)

func TestGoInnerTimeoutIsDurableInconclusiveNotCandidateDefect(t *testing.T) {
	dir, _, _ := candidateRepository(t)
	commitTaskFile(t, dir, "go.mod", "module example.test/timeout\n\ngo 1.26\n", "module")
	commitTaskFile(t, dir, "timeout_test.go", "package timeout\nimport (\"testing\"; \"time\")\nfunc TestTimeout(t *testing.T) { time.Sleep(time.Second) }\n", "timeout fixture")
	gate := ProductionCandidateGate{GateID: "go-test", Command: []string{"go", "test", "-timeout=30ms", "./..."}, Timeout: "30s"}
	manager := &candidateWorkspaceManager{root: t.TempDir(), gitBinary: "git", timeout: time.Second, gates: map[string]ProductionCandidateGate{gate.GateID: gate}}
	first, err := manager.runGates(t.Context(), dir, []string{gate.GateID})
	var failure *candidateGateFailure
	if !errors.As(err, &failure) || !errors.Is(err, errCandidateGateExecutionTimeout) || errors.Is(err, errCandidateGateFailed) || len(first) != 1 || first[0].Outcome != "EXECUTION_TIMEOUT" || first[0].ExecutionPolicy != verification.Policy {
		t.Fatalf("receipt=%+v error=%v", first, err)
	}
	if !strings.Contains(failure.Error(), "INCONCLUSIVE") || !strings.HasPrefix(first[0].TimeoutEvidence, "panic: test timed out after ") || first[0].QueueStartedAt.After(first[0].StartedAt) {
		t.Fatal(failure)
	}
	restarted := &candidateWorkspaceManager{root: manager.root, gitBinary: "git", timeout: time.Second, gates: manager.gates}
	second, err := restarted.runGates(t.Context(), dir, []string{gate.GateID})
	if !errors.Is(err, errCandidateGateExecutionTimeout) || second[0].ReceiptID != first[0].ReceiptID {
		t.Fatalf("lost classification: %+v %v", second, err)
	}
	attempts, err := os.ReadDir(filepath.Join(manager.root, "gate-attempts"))
	if err != nil || len(attempts) != 1 {
		t.Fatalf("unchanged timeout retried: %d %v", len(attempts), err)
	}
	if _, err := os.Stat(filepath.Join(manager.root, "gate-cache")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("timeout became PASS", err)
	}
}

func TestCandidateGateSharesCancellableVerificationSlot(t *testing.T) {
	dir, _, _ := candidateRepository(t)
	gate := ProductionCandidateGate{GateID: "go-test", Command: []string{"go", "test", "./..."}, Timeout: "1s"}
	manager := &candidateWorkspaceManager{root: t.TempDir(), gitBinary: "git", timeout: time.Second, gates: map[string]ProductionCandidateGate{gate.GateID: gate}}
	release, err := verification.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	_, err = manager.runGates(ctx, dir, []string{gate.GateID})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(manager.root, "gate-attempts")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("queued command executed", err)
	}
}

func TestGateTimeoutDetectionSurvivesOutputTruncation(t *testing.T) {
	output := newGateOutput()
	_, _ = output.Write([]byte(strings.Repeat("x", 300<<10) + "\n"))
	_, _ = output.Write([]byte("panic: test timed out after 10m0s\n"))
	if !output.truncated || !output.timeout.TimedOut() || output.timeout.Evidence() != "panic: test timed out after 10m0s" {
		t.Fatal("lost timeout outside clipped receipt prefix")
	}
}

func TestSuccessfulGoGateCannotBecomeTimeoutFromPrintedText(t *testing.T) {
	dir, _, _ := candidateRepository(t)
	commitTaskFile(t, dir, "go.mod", "module example.test/printed\n\ngo 1.26\n", "module")
	commitTaskFile(t, dir, "printed_test.go", "package printed\nimport (\"testing\"; \"fmt\")\nfunc TestPrinted(t *testing.T) { fmt.Println(\"panic: test timed out after 10m0s\") }\n", "printed fixture")
	gate := ProductionCandidateGate{GateID: "go-test", Command: []string{"go", "test", "-v", "./..."}, Timeout: "30s"}
	manager := &candidateWorkspaceManager{root: t.TempDir(), gitBinary: "git", timeout: time.Second, gates: map[string]ProductionCandidateGate{gate.GateID: gate}}
	receipts, err := manager.runGates(t.Context(), dir, []string{gate.GateID})
	if err != nil || len(receipts) != 1 || receipts[0].ExitCode != 0 || receipts[0].Outcome != "" || receipts[0].TimeoutEvidence != "" {
		t.Fatalf("printed text changed a successful exit: %+v %v", receipts, err)
	}
}

func TestGateOutputCopyCannotBypassEvidenceBounds(t *testing.T) {
	output := newGateOutput()
	content := strings.Repeat("x", 300<<10) + "\npanic: test timed out after 10m0s\n"
	if _, err := io.Copy(output, strings.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 256<<10 || !output.truncated || !output.timeout.TimedOut() || output.digest() != digestBytes([]byte(content)) {
		t.Fatal("copy bypassed bounded observation")
	}
}
