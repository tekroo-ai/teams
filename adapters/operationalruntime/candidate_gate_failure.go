package operationalruntime

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/tekroo-ai/teams/internal/verification"
	"github.com/tekroo-ai/teams/kernel"
)

var errCandidateGateFailed = errors.New("candidate verification gate failed")
var errCandidateGateExecutionTimeout = errors.New("verification execution timed out; candidate verdict INCONCLUSIVE")

// Kept separately from successful candidate receipts: a failed gate must never
// qualify a candidate, and restarting the daemon must not erase its finding.
type candidateGateFailure struct {
	InputSHA256   kernel.Digest        `json:"input_sha256"`
	Directory     string               `json:"directory"`
	Commit        string               `json:"commit"`
	Receipt       candidateGateReceipt `json:"receipt"`
	ReceiptSHA256 kernel.Digest        `json:"receipt_sha256"`
}

func (failure *candidateGateFailure) Error() string {
	return fmt.Sprintf("%s: gate %s, commit %s, exit %d, input %s, receipt %s", failure.Unwrap(), failure.Receipt.GateID, failure.Commit, failure.Receipt.ExitCode, failure.InputSHA256, failure.Receipt.ReceiptID)
}

func (failure *candidateGateFailure) Unwrap() error {
	if failure.Receipt.Outcome == "EXECUTION_TIMEOUT" {
		return errCandidateGateExecutionTimeout
	}
	return errCandidateGateFailed
}

func (manager *candidateWorkspaceManager) gateFailurePath(key string) string {
	return filepath.Join(manager.root, "gate-failures", key+".json")
}

func (manager *candidateWorkspaceManager) readGateFailure(key string, gate ProductionCandidateGate, directory, commit string) (*candidateGateFailure, error) {
	encoded, err := os.ReadFile(manager.gateFailurePath(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var failure candidateGateFailure
	if len(encoded) > 1<<20 || json.Unmarshal(encoded, &failure) != nil {
		return nil, errInvalidCandidateWorkspace
	}
	r := failure.Receipt
	receiptBytes, marshalErr := json.Marshal(r)
	out, outErr := base64.StdEncoding.DecodeString(r.StdoutBase64)
	stderr, stderrErr := base64.StdEncoding.DecodeString(r.StderrBase64)
	if marshalErr != nil || digestBytes(receiptBytes) != failure.ReceiptSHA256 || failure.InputSHA256 != kernel.Digest(key) || failure.Directory != directory || failure.Commit != commit || r.GateID != gate.GateID || !slices.Equal(r.Command, gate.Command) || r.ExitCode <= 0 || !r.ReceiptID.Valid() || outErr != nil || stderrErr != nil || digestBytes(out) != r.StdoutSHA256 || digestBytes(stderr) != r.StderrSHA256 {
		return nil, errInvalidCandidateWorkspace
	}
	if r.Outcome != "" && r.Outcome != "EXECUTION_TIMEOUT" {
		return nil, errInvalidCandidateWorkspace
	}
	if r.Outcome == "EXECUTION_TIMEOUT" {
		var detector verification.TimeoutDetector
		_, _ = detector.Write([]byte(r.TimeoutEvidence))
		if !detector.TimedOut() {
			return nil, errInvalidCandidateWorkspace
		}
	}
	return &failure, nil
}

func (manager *candidateWorkspaceManager) retainGateFailure(failure *candidateGateFailure) error {
	if !failure.InputSHA256.Valid() || failure.Receipt.ExitCode <= 0 || manager.root == "" || !filepath.IsAbs(manager.root) {
		return errInvalidCandidateWorkspace
	}
	if err := os.MkdirAll(filepath.Dir(manager.gateFailurePath(string(failure.InputSHA256))), 0700); err != nil {
		return err
	}
	receiptBytes, err := json.Marshal(failure.Receipt)
	if err != nil {
		return err
	}
	failure.ReceiptSHA256 = digestBytes(receiptBytes)
	encoded, err := json.Marshal(failure)
	if err != nil {
		return err
	}
	return retainGateArtifact(manager.gateFailurePath(string(failure.InputSHA256)), encoded)
}

func retainGateArtifact(path string, content []byte) error {
	if err := writeExclusiveSynced(path, content); !errors.Is(err, os.ErrExist) {
		return err
	}
	prior, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !slices.Equal(prior, content) {
		return errInvalidCandidateWorkspace
	}
	return nil
}
