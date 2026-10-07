package operationalruntime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFailedGateRetainsCandidateOutputExitAndTiming(t *testing.T) {
	dir, _, commit := candidateRepository(t)
	manager := &candidateWorkspaceManager{root: t.TempDir(), gitBinary: "git", timeout: time.Second, gates: map[string]ProductionCandidateGate{"failure": {GateID: "failure", Command: []string{"git", "show", "HEAD:no-such-file"}, Timeout: "1s"}}}
	receipts, err := manager.runGates(t.Context(), dir, []string{"failure"})
	if err == nil || len(receipts) != 1 || receipts[0].ExitCode == 0 {
		t.Fatalf("receipts=%+v error=%v", receipts, err)
	}
	entries, err := os.ReadDir(filepath.Join(manager.root, "gate-attempts"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("retained attempts=%v error=%v", entries, err)
	}
	encoded, err := os.ReadFile(filepath.Join(manager.root, "gate-attempts", entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var attempt struct {
		CandidateCommit       string `json:"candidate_commit"`
		CandidateTree         string `json:"candidate_tree"`
		StartedAt, FinishedAt time.Time
		Receipt               candidateGateReceipt
	}
	// Use an explicitly tagged timing view; snake_case is the retained format.
	var times struct {
		Started  time.Time `json:"started_at"`
		Finished time.Time `json:"finished_at"`
	}
	if json.Unmarshal(encoded, &attempt) != nil || json.Unmarshal(encoded, &times) != nil || attempt.CandidateCommit != commit || attempt.CandidateTree != candidateGit(t, dir, "rev-parse", "HEAD^{tree}") || times.Started.IsZero() || times.Finished.Before(times.Started) {
		t.Fatalf("attempt=%s", encoded)
	}
	stderr, err := base64.StdEncoding.DecodeString(attempt.Receipt.StderrBase64)
	if err != nil || !strings.Contains(string(stderr), "no-such-file") || digestBytes(stderr) != attempt.Receipt.StderrSHA256 || attempt.Receipt.ExecutionError == "" {
		t.Fatalf("stderr=%q receipt=%+v", stderr, attempt.Receipt)
	}
	canonical, _ := json.Marshal(receipts[0])
	if strings.Contains(string(canonical), "started_at") {
		t.Fatal("wall clock contaminated canonical candidate identity")
	}
}

func TestGateOutputBoundHashesCompleteStream(t *testing.T) {
	output := newGateOutput()
	data := []byte(strings.Repeat("x", 300<<10))
	n, err := output.Write(data)
	if err != nil || n != len(data) || output.Len() != 256<<10 || !output.truncated || output.digest() != digestBytes(data) {
		t.Fatalf("size=%d truncated=%v error=%v", output.Len(), output.truncated, err)
	}
}

func TestGateTimeoutReleasesProcessGroup(t *testing.T) {
	dir, _, _ := candidateRepository(t)
	manager := &candidateWorkspaceManager{root: t.TempDir(), gitBinary: "git", timeout: time.Second, gates: map[string]ProductionCandidateGate{"timeout": {GateID: "timeout", Command: []string{"/bin/sleep", "30"}, Timeout: "50ms"}}}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	start := time.Now()
	receipts, err := manager.runGates(ctx, dir, []string{"timeout"})
	if err == nil || len(receipts) != 1 || time.Since(start) > 2*time.Second {
		t.Fatalf("timeout did not release: receipts=%+v error=%v elapsed=%v", receipts, err, time.Since(start))
	}
}
