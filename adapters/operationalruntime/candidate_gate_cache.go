package operationalruntime

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"

	"github.com/tekroo-ai/teams/kernel"
)

func candidateGateCacheKey(ctx context.Context, gate ProductionCandidateGate, environment []string, directory, commit, tree string) (string, error) {
	if gate.ReuseInputsSHA256 == "" {
		return "", nil
	}
	return candidateGateInputKey(ctx, gate, environment, directory, commit, tree)
}

// Failure identity is mandatory even when successful-result reuse is disabled.
// A failure is a durable finding, not permission to execute unchanged work again.
func candidateGateInputKey(ctx context.Context, gate ProductionCandidateGate, environment []string, directory, commit, tree string) (string, error) {
	if gate.ReuseInputsSHA256 != "" && !gate.ReuseInputsSHA256.Valid() || gate.VerificationInputsSHA256 != "" && !gate.VerificationInputsSHA256.Valid() || !validGitCommit(commit) || !validGitCommit(tree) || !filepath.IsAbs(directory) {
		return "", errInvalidCandidateWorkspace
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	binary, err := exec.LookPath(gate.Command[0])
	if err != nil {
		return "", err
	}
	file, err := os.Open(binary)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, err = io.Copy(h, file)
	_ = file.Close()
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(struct {
		Gate                                  ProductionCandidateGate
		Environment                           []string
		Directory, Commit, Tree, BinarySHA256 string
	}{gate, environment, directory, commit, tree, hex.EncodeToString(h.Sum(nil))})
	if err != nil {
		return "", err
	}
	return string(digestBytes(encoded)), nil
}

type gateCacheRecord struct {
	InputSHA256   kernel.Digest        `json:"input_sha256"`
	ReceiptSHA256 kernel.Digest        `json:"receipt_sha256"`
	Receipt       candidateGateReceipt `json:"receipt"`
}

func (manager *candidateWorkspaceManager) readGateCache(key string, gate ProductionCandidateGate) (candidateGateReceipt, bool, error) {
	if key == "" {
		return candidateGateReceipt{}, false, nil
	}
	encoded, err := os.ReadFile(filepath.Join(manager.root, "gate-cache", key+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return candidateGateReceipt{}, false, nil
	}
	if err != nil {
		return candidateGateReceipt{}, false, err
	}
	var record gateCacheRecord
	if len(encoded) > 1<<20 || json.Unmarshal(encoded, &record) != nil || record.InputSHA256 != kernel.Digest(key) {
		return candidateGateReceipt{}, false, errInvalidCandidateWorkspace
	}
	receiptBytes, _ := json.Marshal(record.Receipt)
	r := record.Receipt
	out, outErr := base64.StdEncoding.DecodeString(r.StdoutBase64)
	stderr, stderrErr := base64.StdEncoding.DecodeString(r.StderrBase64)
	if digestBytes(receiptBytes) != record.ReceiptSHA256 || r.GateID != gate.GateID || !slices.Equal(r.Command, gate.Command) || r.ExitCode != 0 || r.ExecutionError != "" || !r.ReceiptID.Valid() || outErr != nil || stderrErr != nil || digestBytes(out) != r.StdoutSHA256 || digestBytes(stderr) != r.StderrSHA256 {
		return candidateGateReceipt{}, false, errInvalidCandidateWorkspace
	}
	return r, true, nil
}

func (manager *candidateWorkspaceManager) writeGateCache(key string, receipt candidateGateReceipt) error {
	if key == "" || receipt.ExitCode != 0 || receipt.ExecutionError != "" {
		return nil
	}
	root := filepath.Join(manager.root, "gate-cache")
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	receiptBytes, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(gateCacheRecord{kernel.Digest(key), digestBytes(receiptBytes), receipt})
	if err != nil {
		return err
	}
	path := filepath.Join(root, key+".json")
	if prior, err := os.ReadFile(path); err == nil {
		if !slices.Equal(prior, encoded) {
			return errInvalidCandidateWorkspace
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeExclusiveSynced(path, encoded)
}
