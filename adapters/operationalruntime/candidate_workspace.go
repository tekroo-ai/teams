package operationalruntime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tekroo-ai/teams/adapters/openhands"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

const (
	candidateReceiptSchema        = "tekroo.teams.candidate-receipt/1.1.0"
	legacyCandidateReceiptSchema  = "tekroo.teams.candidate-receipt/1.0.0"
	candidateRehydrateParallelism = 4
)

var errInvalidCandidateWorkspace = errors.New("invalid immutable candidate workspace")

type candidateTargetReceipt struct {
	TaskID              kernel.UUIDv7   `json:"task_id"`
	InvocationID        kernel.UUIDv7   `json:"invocation_id"`
	OutputSHA256        kernel.Digest   `json:"output_sha256"`
	TerminalEvidenceIDs []kernel.UUIDv7 `json:"terminal_evidence_ids"`
}

type candidateGateReceipt struct {
	ReceiptID    kernel.UUIDv7 `json:"receipt_id"`
	GateID       string        `json:"gate_id"`
	Command      []string      `json:"command"`
	ExitCode     int           `json:"exit_code"`
	StdoutBase64 string        `json:"stdout_base64"`
	StderrBase64 string        `json:"stderr_base64"`
	StdoutSHA256 kernel.Digest `json:"stdout_sha256"`
	StderrSHA256 kernel.Digest `json:"stderr_sha256"`
}

type candidateReceipt struct {
	SchemaVersion              string                   `json:"schema_version"`
	CandidateID                kernel.UUIDv7            `json:"candidate_id"`
	FeatureID                  kernel.UUIDv7            `json:"feature_id"`
	ConsumerTaskID             kernel.UUIDv7            `json:"consumer_task_id"`
	ConsumerWorkspaceID        string                   `json:"consumer_workspace_id"`
	SourceWorkspaceID          string                   `json:"source_workspace_id"`
	SourceWorktreeID           string                   `json:"source_worktree_id"`
	SourceWorkingDirectory     string                   `json:"source_working_directory"`
	SourceBranch               string                   `json:"source_branch"`
	RepositoryDigest           kernel.Digest            `json:"repository_digest"`
	BaselineCommit             string                   `json:"baseline_commit"`
	CandidateCommit            string                   `json:"candidate_commit"`
	CandidateTree              string                   `json:"candidate_tree"`
	ChangedFileInventory       []string                 `json:"changed_file_inventory"`
	ChangedFileInventoryDigest kernel.Digest            `json:"changed_file_inventory_sha256"`
	DiffSHA256                 kernel.Digest            `json:"diff_sha256"`
	RuntimeHookSHA256          kernel.Digest            `json:"runtime_hook_sha256,omitempty"`
	Targets                    []candidateTargetReceipt `json:"targets"`
	GateReceipts               []candidateGateReceipt   `json:"gate_receipts"`
	Assumptions                []string                 `json:"assumptions"`
	UnresolvedExceptions       []string                 `json:"unresolved_exceptions"`
	MaterializedWorkspace      string                   `json:"materialized_workspace"`
	MaterializedReference      string                   `json:"materialized_reference"`
}

type candidateWorkspaceManager struct {
	root      string
	gitBinary string
	timeout   time.Duration
	gates     map[string]ProductionCandidateGate
	resolver  *openhands.BoundWorkspaceResolver
}

func newCandidateWorkspaceManager(evidenceRoot, gitBinary string, timeout time.Duration, gates []ProductionCandidateGate, resolver *openhands.BoundWorkspaceResolver) (*candidateWorkspaceManager, error) {
	return newCandidateWorkspaceManagerWithContext(context.Background(), evidenceRoot, gitBinary, timeout, gates, resolver)
}

func newCandidateWorkspaceManagerWithContext(ctx context.Context, evidenceRoot, gitBinary string, timeout time.Duration, gates []ProductionCandidateGate, resolver *openhands.BoundWorkspaceResolver) (*candidateWorkspaceManager, error) {
	if ctx == nil {
		return nil, errInvalidCandidateWorkspace
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(errInvalidCandidateWorkspace, err)
	}
	if evidenceRoot == "" || !filepath.IsAbs(evidenceRoot) || gitBinary == "" || timeout <= 0 || resolver == nil {
		return nil, errInvalidCandidateWorkspace
	}
	manager := &candidateWorkspaceManager{
		root:      filepath.Join(filepath.Clean(evidenceRoot), "candidate-workspaces"),
		gitBinary: gitBinary, timeout: timeout,
		gates: make(map[string]ProductionCandidateGate, len(gates)), resolver: resolver,
	}
	for _, gate := range gates {
		invalidArgument := false
		for _, argument := range gate.Command {
			invalidArgument = invalidArgument || strings.TrimSpace(argument) == ""
		}
		if gate.GateID == "" || len(gate.Command) == 0 || invalidArgument {
			return nil, errInvalidCandidateWorkspace
		}
		gateTimeout, err := time.ParseDuration(gate.Timeout)
		if err != nil || gateTimeout <= 0 {
			return nil, errInvalidCandidateWorkspace
		}
		if _, duplicate := manager.gates[gate.GateID]; duplicate {
			return nil, errInvalidCandidateWorkspace
		}
		gate.Command = append([]string(nil), gate.Command...)
		manager.gates[gate.GateID] = gate
	}
	if err := os.MkdirAll(manager.receiptRoot(), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(manager.workspaceRoot(), 0o700); err != nil {
		return nil, err
	}
	if err := manager.rehydrate(ctx); err != nil {
		return nil, err
	}
	return manager, nil
}

func (manager *candidateWorkspaceManager) Prepare(ctx context.Context, feature organization.FeatureRequest, consumer organization.PlannedTask, consumerWorkspaceID string, source ProductionWorkspace, targets []candidateTargetReceipt, requiredGateIDs []string) (ProductionWorkspace, candidateReceipt, kernel.Digest, error) {
	if manager == nil || !feature.ID.Valid() || !consumer.ID.Valid() || consumerWorkspaceID == "" || source.WorkspaceID == "" || !filepath.IsAbs(source.WorkingDirectory) || len(source.BaselineSHA) != 40 || len(targets) == 0 || len(requiredGateIDs) == 0 {
		return ProductionWorkspace{}, candidateReceipt{}, "", fmt.Errorf("%w: consumer %s workspace %q workdir %q baseline %d targets %d gates %d", errInvalidCandidateWorkspace, consumer.ID, consumerWorkspaceID, source.WorkingDirectory, len(source.BaselineSHA), len(targets), len(requiredGateIDs))
	}
	if !slices.IsSortedFunc(targets, func(left, right candidateTargetReceipt) int {
		return strings.Compare(string(left.TaskID), string(right.TaskID))
	}) {
		return ProductionWorkspace{}, candidateReceipt{}, "", fmt.Errorf("%w: candidate targets not sorted", errInvalidCandidateWorkspace)
	}
	for _, target := range targets {
		if !target.TaskID.Valid() || !target.InvocationID.Valid() || !target.OutputSHA256.Valid() || len(target.TerminalEvidenceIDs) == 0 {
			return ProductionWorkspace{}, candidateReceipt{}, "", fmt.Errorf("%w: target %s invocation %s evid %d", errInvalidCandidateWorkspace, target.TaskID, target.InvocationID, len(target.TerminalEvidenceIDs))
		}
		for _, evidenceID := range target.TerminalEvidenceIDs {
			if !evidenceID.Valid() {
				return ProductionWorkspace{}, candidateReceipt{}, "", fmt.Errorf("%w: invalid terminal evidence id on target %s", errInvalidCandidateWorkspace, target.TaskID)
			}
		}
	}

	state, err := manager.inspectSource(ctx, source)
	if err != nil {
		return ProductionWorkspace{}, candidateReceipt{}, "", err
	}
	seed := struct {
		FeatureID          kernel.UUIDv7            `json:"feature_id"`
		RepositoryDigest   kernel.Digest            `json:"repository_digest"`
		BaselineCommit     string                   `json:"baseline_commit"`
		CandidateCommit    string                   `json:"candidate_commit"`
		CandidateTree      string                   `json:"candidate_tree"`
		ChangedFilesSHA256 kernel.Digest            `json:"changed_files_sha256"`
		DiffSHA256         kernel.Digest            `json:"diff_sha256"`
		RuntimeHookSHA256  kernel.Digest            `json:"runtime_hook_sha256"`
		Targets            []candidateTargetReceipt `json:"targets"`
	}{feature.ID, state.repositoryDigest, source.BaselineSHA, state.commit, state.tree, state.changedFilesDigest, state.diffDigest, state.runtimeHookDigest, targets}
	seedBytes, err := json.Marshal(seed)
	if err != nil {
		return ProductionWorkspace{}, candidateReceipt{}, "", err
	}
	candidateID := deterministicOperationalUUID("immutable-candidate", string(digestBytes(seedBytes)))
	reference := "refs/heads/candidate/" + string(candidateID)
	viewID := candidateViewID(candidateID, consumer.ID)
	workspacePath := filepath.Join(manager.workspaceRoot(), viewID)
	if prior, priorDigest, found, err := manager.existing(ctx, feature, consumer, consumerWorkspaceID, source, state, targets, requiredGateIDs, candidateID, workspacePath, reference); err != nil {
		return ProductionWorkspace{}, candidateReceipt{}, "", err
	} else if found {
		workspace := manager.productionWorkspace(consumer, prior)
		if err := manager.register(workspace, prior, priorDigest); err != nil {
			return ProductionWorkspace{}, candidateReceipt{}, "", err
		}
		return workspace, prior, priorDigest, nil
	}
	gateReceipts, err := manager.runGates(ctx, source.WorkingDirectory, requiredGateIDs)
	if err != nil {
		return ProductionWorkspace{}, candidateReceipt{}, "", err
	}
	rechecked, err := manager.inspectSource(ctx, source)
	if err != nil || !rechecked.equal(state) {
		return ProductionWorkspace{}, candidateReceipt{}, "", errors.Join(errInvalidCandidateWorkspace, err)
	}
	receipt := candidateReceipt{
		SchemaVersion: candidateReceiptSchema, CandidateID: candidateID, FeatureID: feature.ID, ConsumerTaskID: consumer.ID, ConsumerWorkspaceID: consumerWorkspaceID,
		SourceWorkspaceID: source.WorkspaceID, SourceWorktreeID: source.WorktreeID, SourceWorkingDirectory: filepath.Clean(source.WorkingDirectory), SourceBranch: source.Branch,
		RepositoryDigest: state.repositoryDigest, BaselineCommit: source.BaselineSHA, CandidateCommit: state.commit, CandidateTree: state.tree,
		ChangedFileInventory: state.changedFiles, ChangedFileInventoryDigest: state.changedFilesDigest, DiffSHA256: state.diffDigest, RuntimeHookSHA256: state.runtimeHookDigest,
		Targets: append([]candidateTargetReceipt(nil), targets...), GateReceipts: gateReceipts,
		Assumptions: []string{}, UnresolvedExceptions: []string{}, MaterializedWorkspace: workspacePath, MaterializedReference: reference,
	}
	receiptBytes, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return ProductionWorkspace{}, candidateReceipt{}, "", err
	}
	receiptBytes = append(receiptBytes, '\n')
	receiptDigest := digestBytes(receiptBytes)
	if err := manager.materialize(ctx, source.WorkingDirectory, receipt, receiptBytes); err != nil {
		return ProductionWorkspace{}, candidateReceipt{}, "", err
	}
	workspace := manager.productionWorkspace(consumer, receipt)
	if err := manager.register(workspace, receipt, receiptDigest); err != nil {
		return ProductionWorkspace{}, candidateReceipt{}, "", err
	}
	return workspace, receipt, receiptDigest, nil
}

// Reuse gives another read-only consumer an independently materialized view of
// an already verified candidate. It preserves the candidate and gate identity;
// only the consumer binding and its receipt change.
func (manager *candidateWorkspaceManager) Reuse(ctx context.Context, feature organization.FeatureRequest, consumer organization.PlannedTask, consumerWorkspaceID string, source candidateReceipt) (ProductionWorkspace, candidateReceipt, kernel.Digest, error) {
	if manager == nil || !feature.ID.Valid() || !consumer.ID.Valid() || consumerWorkspaceID == "" || source.FeatureID != feature.ID || !manager.validReceipt(source) {
		return ProductionWorkspace{}, candidateReceipt{}, "", errInvalidCandidateWorkspace
	}
	if err := manager.verifyMaterialized(ctx, source); err != nil {
		return ProductionWorkspace{}, candidateReceipt{}, "", err
	}
	receipt := source
	receipt.ConsumerTaskID = consumer.ID
	receipt.ConsumerWorkspaceID = consumerWorkspaceID
	receipt.MaterializedWorkspace = filepath.Join(manager.workspaceRoot(), candidateViewID(receipt.CandidateID, consumer.ID))
	receiptBytes, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return ProductionWorkspace{}, candidateReceipt{}, "", err
	}
	receiptBytes = append(receiptBytes, '\n')
	receiptDigest := digestBytes(receiptBytes)
	if err := manager.materialize(ctx, source.MaterializedWorkspace, receipt, receiptBytes); err != nil {
		return ProductionWorkspace{}, candidateReceipt{}, "", err
	}
	workspace := manager.productionWorkspace(consumer, receipt)
	if err := manager.register(workspace, receipt, receiptDigest); err != nil {
		return ProductionWorkspace{}, candidateReceipt{}, "", err
	}
	return workspace, receipt, receiptDigest, nil
}

func (manager *candidateWorkspaceManager) retainedReceipt(ctx context.Context, featureID, consumerTaskID, candidateID kernel.UUIDv7) (candidateReceipt, kernel.Digest, error) {
	if manager == nil || !featureID.Valid() || !consumerTaskID.Valid() || !candidateID.Valid() {
		return candidateReceipt{}, "", errInvalidCandidateWorkspace
	}
	path := filepath.Join(manager.receiptRoot(), candidateViewID(candidateID, consumerTaskID)+".json")
	content, err := os.ReadFile(path)
	if err != nil {
		return candidateReceipt{}, "", errors.Join(errInvalidCandidateWorkspace, err)
	}
	var receipt candidateReceipt
	if json.Unmarshal(content, &receipt) != nil || !manager.validReceipt(receipt) || receipt.FeatureID != featureID || receipt.ConsumerTaskID != consumerTaskID || receipt.CandidateID != candidateID {
		return candidateReceipt{}, "", errInvalidCandidateWorkspace
	}
	if err := manager.verifyMaterialized(ctx, receipt); err != nil {
		return candidateReceipt{}, "", err
	}
	return receipt, digestBytes(content), nil
}

func (manager *candidateWorkspaceManager) existing(ctx context.Context, feature organization.FeatureRequest, consumer organization.PlannedTask, consumerWorkspaceID string, source ProductionWorkspace, state inspectedCandidateSource, targets []candidateTargetReceipt, requiredGateIDs []string, candidateID kernel.UUIDv7, workspacePath, reference string) (candidateReceipt, kernel.Digest, bool, error) {
	path := filepath.Join(manager.receiptRoot(), candidateViewID(candidateID, consumer.ID)+".json")
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return candidateReceipt{}, "", false, nil
	}
	if err != nil {
		return candidateReceipt{}, "", false, err
	}
	var receipt candidateReceipt
	mismatch := ""
	switch {
	case json.Unmarshal(content, &receipt) != nil || !manager.validReceipt(receipt):
		mismatch = "receipt-unreadable"
	case receipt.SchemaVersion != candidateReceiptSchema:
		mismatch = "schema"
	case receipt.CandidateID != candidateID:
		mismatch = "candidate-id"
	case receipt.FeatureID != feature.ID:
		mismatch = "feature-id"
	case receipt.ConsumerTaskID != consumer.ID:
		mismatch = "consumer-task"
	case receipt.ConsumerWorkspaceID != consumerWorkspaceID:
		mismatch = "consumer-workspace"
	case receipt.SourceWorkspaceID != source.WorkspaceID:
		mismatch = "source-workspace"
	case receipt.SourceWorktreeID != source.WorktreeID:
		mismatch = "source-worktree"
	case receipt.SourceWorkingDirectory != filepath.Clean(source.WorkingDirectory):
		mismatch = "source-workdir"
	case receipt.SourceBranch != source.Branch:
		mismatch = "source-branch"
	case receipt.RepositoryDigest != state.repositoryDigest:
		mismatch = "repository-digest"
	case receipt.BaselineCommit != source.BaselineSHA:
		mismatch = "baseline-commit"
	case receipt.CandidateCommit != state.commit:
		mismatch = "candidate-commit"
	case receipt.CandidateTree != state.tree:
		mismatch = "candidate-tree"
	case receipt.ChangedFileInventoryDigest != state.changedFilesDigest:
		mismatch = "changed-files-digest"
	case receipt.DiffSHA256 != state.diffDigest:
		mismatch = "diff-sha256"
	case receipt.RuntimeHookSHA256 != state.runtimeHookDigest:
		mismatch = "runtime-hook-digest"
	case !slices.Equal(receipt.ChangedFileInventory, state.changedFiles):
		mismatch = "changed-file-inventory"
	case !candidateTargetsEqual(receipt.Targets, targets):
		mismatch = "targets"
	case receipt.MaterializedWorkspace != workspacePath:
		mismatch = "materialized-workspace"
	case receipt.MaterializedReference != reference:
		mismatch = "materialized-reference"
	case !gateIDsEqual(receipt.GateReceipts, requiredGateIDs):
		mismatch = "gates"
	}
	if mismatch != "" {
		return candidateReceipt{}, "", false, fmt.Errorf("%w: existing receipt mismatch on %s", errInvalidCandidateWorkspace, mismatch)
	}
	if err := manager.verifyMaterialized(ctx, receipt); err != nil {
		return candidateReceipt{}, "", false, err
	}
	return receipt, digestBytes(content), true, nil
}

func candidateTargetsEqual(left, right []candidateTargetReceipt) bool {
	return slices.EqualFunc(left, right, func(a, b candidateTargetReceipt) bool {
		return a.TaskID == b.TaskID && a.InvocationID == b.InvocationID && a.OutputSHA256 == b.OutputSHA256 && slices.Equal(a.TerminalEvidenceIDs, b.TerminalEvidenceIDs)
	})
}

func gateIDsEqual(receipts []candidateGateReceipt, required []string) bool {
	if len(receipts) != len(required) {
		return false
	}
	for index := range receipts {
		if receipts[index].GateID != required[index] {
			return false
		}
	}
	return true
}

type inspectedCandidateSource struct {
	repositoryDigest   kernel.Digest
	commit             string
	tree               string
	changedFiles       []string
	changedFilesDigest kernel.Digest
	diffDigest         kernel.Digest
	runtimeHookDigest  kernel.Digest
}

func (manager *candidateWorkspaceManager) inspectSource(ctx context.Context, source ProductionWorkspace) (inspectedCandidateSource, error) {
	top, err := manager.git(ctx, source.WorkingDirectory, "rev-parse", "--show-toplevel")
	configuredTop, configuredErr := filepath.EvalSymlinks(filepath.Clean(source.WorkingDirectory))
	observedTop, observedErr := filepath.EvalSymlinks(filepath.Clean(strings.TrimSpace(string(top))))
	if err != nil || configuredErr != nil || observedErr != nil || observedTop != configuredTop {
		return inspectedCandidateSource{}, fmt.Errorf("%w: source repository root mismatch: git=%v configured=%v observed=%v", errInvalidCandidateWorkspace, err, configuredErr, observedErr)
	}
	status, err := manager.git(ctx, source.WorkingDirectory, "--no-optional-locks", "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil || !candidateSourceStatusClean(status) {
		return inspectedCandidateSource{}, fmt.Errorf("%w: source workspace is not clean: %v", errInvalidCandidateWorkspace, err)
	}
	branch, err := manager.git(ctx, source.WorkingDirectory, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || strings.TrimSpace(string(branch)) != source.Branch {
		return inspectedCandidateSource{}, fmt.Errorf("%w: source branch mismatch: %v", errInvalidCandidateWorkspace, err)
	}
	commit, err := manager.git(ctx, source.WorkingDirectory, "rev-parse", "HEAD")
	if err != nil {
		return inspectedCandidateSource{}, err
	}
	commitText := strings.TrimSpace(string(commit))
	tree, err := manager.git(ctx, source.WorkingDirectory, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return inspectedCandidateSource{}, err
	}
	if _, err := manager.git(ctx, source.WorkingDirectory, "merge-base", "--is-ancestor", source.BaselineSHA, commitText); err != nil || commitText == source.BaselineSHA {
		return inspectedCandidateSource{}, fmt.Errorf("%w: candidate is not a proper descendant of baseline: %v", errInvalidCandidateWorkspace, err)
	}
	common, err := manager.git(ctx, source.WorkingDirectory, "rev-parse", "--git-common-dir")
	if err != nil {
		return inspectedCandidateSource{}, err
	}
	commonPath := strings.TrimSpace(string(common))
	if !filepath.IsAbs(commonPath) {
		commonPath = filepath.Join(source.WorkingDirectory, commonPath)
	}
	commonPath, err = filepath.EvalSymlinks(filepath.Clean(commonPath))
	if err != nil {
		return inspectedCandidateSource{}, err
	}
	changed, err := manager.git(ctx, source.WorkingDirectory, "diff", "--name-only", "-z", source.BaselineSHA+".."+commitText)
	if err != nil || len(changed) == 0 {
		return inspectedCandidateSource{}, fmt.Errorf("%w: changed-file inventory is empty or unavailable: %v", errInvalidCandidateWorkspace, err)
	}
	diff, err := manager.git(ctx, source.WorkingDirectory, "diff", "--binary", "--full-index", source.BaselineSHA+".."+commitText)
	if err != nil || len(diff) == 0 {
		return inspectedCandidateSource{}, fmt.Errorf("%w: candidate diff is empty or unavailable: %v", errInvalidCandidateWorkspace, err)
	}
	runtimeHook, err := os.ReadFile(filepath.Join(source.WorkingDirectory, ".openhands", "hooks", "sma_context_hook.py"))
	if err != nil || len(runtimeHook) == 0 {
		return inspectedCandidateSource{}, fmt.Errorf("%w: required OpenHands runtime hook is unavailable: %v", errInvalidCandidateWorkspace, err)
	}
	changedFiles := strings.Split(strings.TrimSuffix(string(changed), "\x00"), "\x00")
	return inspectedCandidateSource{
		repositoryDigest: digestBytes([]byte(commonPath)), commit: commitText, tree: strings.TrimSpace(string(tree)), changedFiles: changedFiles,
		changedFilesDigest: digestBytes(changed), diffDigest: digestBytes(diff), runtimeHookDigest: digestBytes(runtimeHook),
	}, nil
}

func candidateSourceStatusClean(status []byte) bool {
	for _, line := range strings.Split(strings.TrimSpace(string(status)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "?? .openhands/hooks/sma_context_hook.py" {
			continue
		}
		return false
	}
	return true
}

func (source inspectedCandidateSource) equal(other inspectedCandidateSource) bool {
	return source.repositoryDigest == other.repositoryDigest && source.commit == other.commit && source.tree == other.tree && source.changedFilesDigest == other.changedFilesDigest && source.diffDigest == other.diffDigest && source.runtimeHookDigest == other.runtimeHookDigest && slices.Equal(source.changedFiles, other.changedFiles)
}

func (manager *candidateWorkspaceManager) runGates(ctx context.Context, directory string, required []string) ([]candidateGateReceipt, error) {
	receipts := make([]candidateGateReceipt, 0, len(required))
	seen := make(map[string]struct{}, len(required))
	for _, gateID := range required {
		gate, found := manager.gates[gateID]
		if !found {
			return nil, fmt.Errorf("%w: deterministic gate %q is not configured", errInvalidCandidateWorkspace, gateID)
		}
		if _, duplicate := seen[gateID]; duplicate {
			return nil, errInvalidCandidateWorkspace
		}
		seen[gateID] = struct{}{}
		timeout, _ := time.ParseDuration(gate.Timeout)
		gateCtx, cancel := context.WithTimeout(ctx, timeout)
		command := exec.CommandContext(gateCtx, gate.Command[0], gate.Command[1:]...)
		command.Dir = directory
		command.Env = append(os.Environ(), "CI=1", "GIT_PAGER=cat", "PAGER=cat", "GIT_TERMINAL_PROMPT=0")
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		runErr := command.Run()
		cancel()
		exitCode := 0
		if runErr != nil {
			exitCode = -1
			var exitError *exec.ExitError
			if errors.As(runErr, &exitError) {
				exitCode = exitError.ExitCode()
			}
		}
		stdoutDigest, stderrDigest := digestBytes(stdout.Bytes()), digestBytes(stderr.Bytes())
		receipt := candidateGateReceipt{
			ReceiptID: deterministicOperationalUUID("candidate-gate-receipt", gateID, strings.Join(gate.Command, "\x00"), fmt.Sprint(exitCode), string(stdoutDigest), string(stderrDigest)),
			GateID:    gateID, Command: append([]string(nil), gate.Command...), ExitCode: exitCode,
			StdoutBase64: base64.StdEncoding.EncodeToString(stdout.Bytes()), StderrBase64: base64.StdEncoding.EncodeToString(stderr.Bytes()),
			StdoutSHA256: stdoutDigest, StderrSHA256: stderrDigest,
		}
		receipts = append(receipts, receipt)
		if runErr != nil {
			return nil, fmt.Errorf("%w: deterministic gate %q failed with exit code %d: %v", errInvalidCandidateWorkspace, gateID, exitCode, runErr)
		}
	}
	return receipts, nil
}

func (manager *candidateWorkspaceManager) materialize(ctx context.Context, source string, receipt candidateReceipt, receiptBytes []byte) error {
	receiptPath := manager.receiptPath(receipt)
	workspacePath := receipt.MaterializedWorkspace
	if prior, err := os.ReadFile(receiptPath); err == nil {
		if !bytes.Equal(prior, receiptBytes) {
			return errInvalidCandidateWorkspace
		}
		return manager.verifyMaterialized(ctx, receipt)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if info, err := os.Lstat(workspacePath); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errInvalidCandidateWorkspace
		}
		if err := manager.verifyMaterialized(ctx, receipt); err != nil {
			return err
		}
		return writeExclusiveSynced(receiptPath, receiptBytes)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary, err := os.MkdirTemp(manager.workspaceRoot(), ".candidate-*")
	if err != nil {
		return err
	}
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.RemoveAll(temporary)
		}
	}()
	if _, err := manager.git(ctx, temporary, "init", "--initial-branch=bootstrap"); err != nil {
		return err
	}
	if _, err := manager.git(ctx, temporary, "fetch", "--no-tags", "--no-write-fetch-head", source, receipt.CandidateCommit); err != nil {
		return err
	}
	branch := strings.TrimPrefix(receipt.MaterializedReference, "refs/heads/")
	if _, err := manager.git(ctx, temporary, "checkout", "-b", branch, receipt.CandidateCommit); err != nil {
		return err
	}
	hook, err := os.ReadFile(filepath.Join(source, ".openhands", "hooks", "sma_context_hook.py"))
	if err != nil || digestBytes(hook) != receipt.RuntimeHookSHA256 {
		return errors.Join(errInvalidCandidateWorkspace, err)
	}
	hookPath := filepath.Join(temporary, ".openhands", "hooks", "sma_context_hook.py")
	if err := os.MkdirAll(filepath.Dir(hookPath), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(hookPath, hook, 0o700); err != nil {
		return err
	}
	if err := os.Rename(temporary, workspacePath); err != nil {
		return err
	}
	removeTemporary = false
	if err := makeTreeReadOnly(workspacePath); err != nil {
		return err
	}
	return writeExclusiveSynced(receiptPath, receiptBytes)
}

func writeExclusiveSynced(path string, content []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o400)
	if err != nil {
		return err
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func (manager *candidateWorkspaceManager) verifyMaterialized(ctx context.Context, receipt candidateReceipt) error {
	expectedPath := filepath.Join(manager.workspaceRoot(), candidateViewID(receipt.CandidateID, receipt.ConsumerTaskID))
	if filepath.Clean(receipt.MaterializedWorkspace) != expectedPath || receipt.MaterializedReference != "refs/heads/candidate/"+string(receipt.CandidateID) {
		return errInvalidCandidateWorkspace
	}
	checks := []struct {
		arguments []string
		expected  string
	}{
		{[]string{"rev-parse", "HEAD"}, receipt.CandidateCommit},
		{[]string{"rev-parse", "HEAD^{tree}"}, receipt.CandidateTree},
		{[]string{"for-each-ref", "--format=%(refname)"}, receipt.MaterializedReference},
	}
	for _, check := range checks {
		output, err := manager.git(ctx, receipt.MaterializedWorkspace, check.arguments...)
		if err != nil || strings.TrimSpace(string(output)) != check.expected {
			return errors.Join(errInvalidCandidateWorkspace, err)
		}
	}
	status, err := manager.git(ctx, receipt.MaterializedWorkspace, "--no-optional-locks", "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil || !candidateMaterializedStatusClean(status, receipt.RuntimeHookSHA256.Valid()) {
		return errors.Join(errInvalidCandidateWorkspace, err)
	}
	if receipt.RuntimeHookSHA256.Valid() {
		hook, readErr := os.ReadFile(filepath.Join(receipt.MaterializedWorkspace, ".openhands", "hooks", "sma_context_hook.py"))
		if readErr != nil || digestBytes(hook) != receipt.RuntimeHookSHA256 {
			return errors.Join(errInvalidCandidateWorkspace, readErr)
		}
	}
	if _, err := manager.git(ctx, receipt.MaterializedWorkspace, "merge-base", "--is-ancestor", receipt.BaselineCommit, receipt.CandidateCommit); err != nil || receipt.BaselineCommit == receipt.CandidateCommit {
		return errors.Join(errInvalidCandidateWorkspace, err)
	}
	changed, err := manager.git(ctx, receipt.MaterializedWorkspace, "diff", "--name-only", "-z", receipt.BaselineCommit+".."+receipt.CandidateCommit)
	if err != nil || digestBytes(changed) != receipt.ChangedFileInventoryDigest || !slices.Equal(strings.Split(strings.TrimSuffix(string(changed), "\x00"), "\x00"), receipt.ChangedFileInventory) {
		return errors.Join(errInvalidCandidateWorkspace, err)
	}
	diff, err := manager.git(ctx, receipt.MaterializedWorkspace, "diff", "--binary", "--full-index", receipt.BaselineCommit+".."+receipt.CandidateCommit)
	if err != nil || digestBytes(diff) != receipt.DiffSHA256 {
		return errors.Join(errInvalidCandidateWorkspace, err)
	}
	return nil
}

func candidateMaterializedStatusClean(status []byte, runtimeHookRequired bool) bool {
	trimmed := strings.TrimSpace(string(status))
	if !runtimeHookRequired {
		return trimmed == ""
	}
	return trimmed == "?? .openhands/hooks/sma_context_hook.py"
}

func makeTreeReadOnly(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		mode := info.Mode()
		if mode.IsDir() {
			return os.Chmod(path, mode.Perm()&0o555)
		}
		return os.Chmod(path, mode.Perm()&0o555)
	})
}

func (manager *candidateWorkspaceManager) git(ctx context.Context, directory string, arguments ...string) ([]byte, error) {
	commandCtx, cancel := context.WithTimeout(ctx, manager.timeout)
	defer cancel()
	command := exec.CommandContext(commandCtx, manager.gitBinary, arguments...)
	command.Dir = directory
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC", "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat", "PAGER=cat")
	output, err := command.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("git %s: %w: %s", arguments[0], err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func (manager *candidateWorkspaceManager) productionWorkspace(consumer organization.PlannedTask, receipt candidateReceipt) ProductionWorkspace {
	_ = consumer
	return ProductionWorkspace{WorkspaceID: receipt.ConsumerWorkspaceID, WorktreeID: "candidate-" + candidateViewID(receipt.CandidateID, receipt.ConsumerTaskID), WorkingDirectory: receipt.MaterializedWorkspace, Branch: strings.TrimPrefix(receipt.MaterializedReference, "refs/heads/"), BaselineSHA: receipt.CandidateCommit, WritablePaths: []string{"."}}
}

func (manager *candidateWorkspaceManager) register(workspace ProductionWorkspace, receipt candidateReceipt, digest kernel.Digest) error {
	return manager.resolver.RegisterWorkspace(openhands.WorkspaceBinding{
		WorkspaceID: workspace.WorkspaceID, WorktreeID: workspace.WorktreeID, WorkingDirectory: workspace.WorkingDirectory,
		Candidate: &openhands.CandidateWorkspaceBinding{CandidateID: string(receipt.CandidateID), ReceiptSHA256: digest, RepositoryDigest: receipt.RepositoryDigest, BaselineCommit: receipt.BaselineCommit, CandidateCommit: receipt.CandidateCommit, CandidateTree: receipt.CandidateTree, DiffSHA256: receipt.DiffSHA256, ChangedFilesSHA256: receipt.ChangedFileInventoryDigest, RuntimeHookSHA256: receipt.RuntimeHookSHA256, AllowedReference: receipt.MaterializedReference, ReadOnly: true},
	})
}

func (manager *candidateWorkspaceManager) rehydrate(ctx context.Context) error {
	entries, err := os.ReadDir(manager.receiptRoot())
	if err != nil {
		return err
	}
	type retainedCandidate struct {
		receipt   candidateReceipt
		workspace ProductionWorkspace
		digest    kernel.Digest
	}
	retained := make([]retainedCandidate, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		content, err := os.ReadFile(filepath.Join(manager.receiptRoot(), entry.Name()))
		if err != nil {
			return err
		}
		var receipt candidateReceipt
		if json.Unmarshal(content, &receipt) != nil || !manager.validReceipt(receipt) || filepath.Base(entry.Name()) != candidateViewID(receipt.CandidateID, receipt.ConsumerTaskID)+".json" {
			return errInvalidCandidateWorkspace
		}
		workspace := manager.productionWorkspace(organization.PlannedTask{}, receipt)
		retained = append(retained, retainedCandidate{receipt: receipt, workspace: workspace, digest: digestBytes(content)})
	}

	// Each retained candidate is an independent, read-only Git worktree. Verify
	// them concurrently so restart time does not grow as the sum of every
	// historical candidate's Git inspection cost. Registration remains ordered
	// below, preserving deterministic collision and error behavior.
	verificationErrors := make([]error, len(retained))
	semaphore := make(chan struct{}, candidateRehydrateParallelism)
	var wait sync.WaitGroup
	for index := range retained {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				verificationErrors[index] = ctx.Err()
				return
			}
			verificationErrors[index] = manager.verifyMaterialized(ctx, retained[index].receipt)
		}(index)
	}
	wait.Wait()
	for index, verificationErr := range verificationErrors {
		if verificationErr != nil {
			return verificationErr
		}
		candidate := retained[index]
		if err := manager.register(candidate.workspace, candidate.receipt, candidate.digest); err != nil {
			return err
		}
	}
	return nil
}

func (manager *candidateWorkspaceManager) validReceipt(receipt candidateReceipt) bool {
	legacy := receipt.SchemaVersion == legacyCandidateReceiptSchema && receipt.RuntimeHookSHA256 == ""
	current := receipt.SchemaVersion == candidateReceiptSchema && receipt.RuntimeHookSHA256.Valid()
	if !legacy && !current || !receipt.CandidateID.Valid() || !receipt.FeatureID.Valid() || !receipt.ConsumerTaskID.Valid() || receipt.ConsumerWorkspaceID == "" || receipt.SourceWorkspaceID == "" || receipt.SourceWorktreeID == "" || !filepath.IsAbs(receipt.SourceWorkingDirectory) || receipt.SourceBranch == "" || !receipt.RepositoryDigest.Valid() || !validGitCommit(receipt.BaselineCommit) || !validGitCommit(receipt.CandidateCommit) || !validGitCommit(receipt.CandidateTree) || !receipt.ChangedFileInventoryDigest.Valid() || !receipt.DiffSHA256.Valid() || len(receipt.ChangedFileInventory) == 0 || len(receipt.Targets) == 0 || len(receipt.GateReceipts) == 0 {
		return false
	}
	if filepath.Clean(receipt.MaterializedWorkspace) != filepath.Join(manager.workspaceRoot(), candidateViewID(receipt.CandidateID, receipt.ConsumerTaskID)) || receipt.MaterializedReference != "refs/heads/candidate/"+string(receipt.CandidateID) {
		return false
	}
	if !slices.IsSorted(receipt.ChangedFileInventory) || !slices.IsSortedFunc(receipt.Targets, func(left, right candidateTargetReceipt) int {
		return strings.Compare(string(left.TaskID), string(right.TaskID))
	}) {
		return false
	}
	for _, path := range receipt.ChangedFileInventory {
		if path == "" || filepath.IsAbs(path) || path != filepath.Clean(path) || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
			return false
		}
	}
	for _, target := range receipt.Targets {
		if !target.TaskID.Valid() || !target.InvocationID.Valid() || !target.OutputSHA256.Valid() || len(target.TerminalEvidenceIDs) == 0 {
			return false
		}
		for _, evidenceID := range target.TerminalEvidenceIDs {
			if !evidenceID.Valid() {
				return false
			}
		}
	}
	for _, gate := range receipt.GateReceipts {
		stdout, stdoutErr := base64.StdEncoding.DecodeString(gate.StdoutBase64)
		stderr, stderrErr := base64.StdEncoding.DecodeString(gate.StderrBase64)
		configured, found := manager.gates[gate.GateID]
		if !gate.ReceiptID.Valid() || !found || !slices.Equal(gate.Command, configured.Command) || gate.ExitCode != 0 || stdoutErr != nil || stderrErr != nil || digestBytes(stdout) != gate.StdoutSHA256 || digestBytes(stderr) != gate.StderrSHA256 {
			return false
		}
	}
	return true
}

func validGitCommit(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}

func (manager *candidateWorkspaceManager) receiptRoot() string {
	return filepath.Join(manager.root, "receipts")
}

func (manager *candidateWorkspaceManager) workspaceRoot() string {
	return filepath.Join(manager.root, "workspaces")
}

func (manager *candidateWorkspaceManager) receiptPath(receipt candidateReceipt) string {
	return filepath.Join(manager.receiptRoot(), candidateViewID(receipt.CandidateID, receipt.ConsumerTaskID)+".json")
}

func candidateViewID(candidateID, consumerTaskID kernel.UUIDv7) string {
	return string(candidateID) + "-" + string(consumerTaskID)
}

func (service *ProductionService) prepareCandidateConsumerWorkspace(ctx context.Context, feature organization.FeatureRequest, consumer organization.PlannedTask, owner organization.RoleInstanceState, plan organization.FeaturePlan, invocations map[kernel.UUIDv7]kernel.WorkInvocation, baseEvidence []kernel.EvidenceRef) (ProductionWorkspace, []kernel.EvidenceRef, error) {
	if service == nil || service.candidates == nil {
		return ProductionWorkspace{}, nil, fmt.Errorf("%w: candidate manager unavailable", errInvalidCandidateWorkspace)
	}
	targetIDs := validationCandidateTargetIDs(plan, consumer)
	if consumer.Purpose == kernel.PurposePromotion && len(targetIDs) == 0 {
		featureValidatorID, found, err := service.assembledValidationCandidateTask(ctx, feature, consumer, plan)
		if err != nil {
			return ProductionWorkspace{}, nil, err
		}
		if found {
			return service.reuseFeatureValidationCandidateWorkspace(ctx, feature, consumer, owner, featureValidatorID, baseEvidence)
		}
		// Preserve legacy materialized plans that predate whole-feature
		// validation. New plans always take the exact candidate used by the
		// generated feature validator above.
		if len(targetIDs) == 0 {
			for _, dependencyID := range consumer.DependsOn {
				for _, task := range plan.Tasks {
					if task.ID == dependencyID && (task.Purpose == kernel.PurposeImplementation || task.Purpose == kernel.PurposeRepair) {
						targetIDs = append(targetIDs, task.ID)
					}
				}
			}
		}
	}
	if len(targetIDs) == 0 {
		return ProductionWorkspace{}, nil, errInvalidCandidateWorkspace
	}
	slices.Sort(targetIDs)
	targets := make([]candidateTargetReceipt, 0, len(targetIDs))
	var source ProductionWorkspace
	multipleSources := false
	for _, targetID := range targetIDs {
		var targetTask organization.PlannedTask
		foundTask := false
		for _, item := range plan.Tasks {
			if item.ID == targetID {
				targetTask, foundTask = item, true
				break
			}
		}
		invocation, foundInvocation := invocations[targetID]
		if !foundTask || !foundInvocation || invocation.State != kernel.InvocationSucceeded || invocation.OutputDigest == nil || len(invocation.TerminalEvidenceIDs) == 0 {
			return ProductionWorkspace{}, nil, fmt.Errorf("%w: consumer %s target %s task-found %t inv-found %t state %s output %v evid %d", errInvalidCandidateWorkspace, consumer.ID, targetID, foundTask, foundInvocation, invocation.State, invocation.OutputDigest != nil, len(invocation.TerminalEvidenceIDs))
		}
		targetWorkspace, err := service.plannedTaskWorkspace(ctx, targetTask)
		if err != nil {
			return ProductionWorkspace{}, nil, err
		}
		if source.WorkspaceID == "" {
			source = targetWorkspace
		} else if source.WorkspaceID != targetWorkspace.WorkspaceID || source.WorktreeID != targetWorkspace.WorktreeID || source.WorkingDirectory != targetWorkspace.WorkingDirectory {
			multipleSources = true
		}
		evidenceIDs := append([]kernel.UUIDv7(nil), invocation.TerminalEvidenceIDs...)
		slices.Sort(evidenceIDs)
		targets = append(targets, candidateTargetReceipt{TaskID: targetID, InvocationID: invocation.ID, OutputSHA256: *invocation.OutputDigest, TerminalEvidenceIDs: evidenceIDs})
	}
	if multipleSources {
		assembled, assemblyEvidence, err := service.prepareAssembledCandidateSource(ctx, feature, plan, targetIDs)
		if err != nil {
			return ProductionWorkspace{}, nil, err
		}
		source = assembled
		baseEvidence = append(append([]kernel.EvidenceRef(nil), baseEvidence...), assemblyEvidence)
	}
	workspace, receipt, receiptDigest, err := service.candidates.Prepare(ctx, feature, consumer, owner.WorkspaceID, source, targets, service.planning.RequiredGateIDs)
	if err != nil {
		return ProductionWorkspace{}, nil, err
	}
	evidenceID, err := service.registerCandidateReceipt(ctx, feature, receipt, receiptDigest)
	if err != nil {
		return ProductionWorkspace{}, nil, err
	}
	evidence := append([]kernel.EvidenceRef(nil), baseEvidence...)
	evidence = append(evidence, kernel.EvidenceRef{EvidenceID: evidenceID, SHA256: receiptDigest})
	slices.SortFunc(evidence, func(left, right kernel.EvidenceRef) int {
		return strings.Compare(string(left.EvidenceID), string(right.EvidenceID))
	})
	return workspace, evidence, nil
}

// assembledValidationCandidateTask identifies a whole-feature validator by
// the immutable candidate it actually inspected. This preserves compatibility
// with materialized plans created before the current deterministic task-ID
// convention.
func (service *ProductionService) assembledValidationCandidateTask(ctx context.Context, feature organization.FeatureRequest, consumer organization.PlannedTask, plan organization.FeaturePlan) (kernel.UUIDv7, bool, error) {
	implementationIDs := make([]kernel.UUIDv7, 0)
	dependencies := make(map[kernel.UUIDv7]struct{}, len(consumer.DependsOn))
	for _, dependencyID := range consumer.DependsOn {
		dependencies[dependencyID] = struct{}{}
	}
	tasks := make(map[kernel.UUIDv7]organization.PlannedTask, len(plan.Tasks))
	for _, task := range plan.Tasks {
		tasks[task.ID] = task
		if _, required := dependencies[task.ID]; required && (task.Purpose == kernel.PurposeImplementation || task.Purpose == kernel.PurposeRepair) {
			implementationIDs = append(implementationIDs, task.ID)
		}
	}
	slices.Sort(implementationIDs)
	if len(implementationIDs) == 0 {
		return "", false, nil
	}
	var selected kernel.UUIDv7
	for _, dependencyID := range consumer.DependsOn {
		dependency, found := tasks[dependencyID]
		if !found || dependency.Purpose != kernel.PurposeValidation {
			continue
		}
		snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: dependencyID}})
		if err != nil {
			return "", false, err
		}
		scope, found := snapshot.TaskOperationalScopes[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: dependencyID}]
		if !found || !strings.HasPrefix(scope.WorktreeID, "candidate-") || service.workspaceResolver == nil {
			continue
		}
		binding, err := service.workspaceResolver.ResolveWorkspace(ctx, scope)
		if err != nil || binding.Candidate == nil {
			return "", false, errors.Join(errInvalidCandidateWorkspace, err)
		}
		receipt, receiptDigest, err := service.candidates.retainedReceipt(ctx, feature.ID, dependencyID, kernel.UUIDv7(binding.Candidate.CandidateID))
		if err != nil || receiptDigest != binding.Candidate.ReceiptSHA256 {
			return "", false, errors.Join(errInvalidCandidateWorkspace, err)
		}
		if !candidateTargetsMatch(receipt.Targets, implementationIDs) {
			continue
		}
		if selected != "" && selected != dependencyID {
			return "", false, fmt.Errorf("%w: multiple assembled validation candidates", errInvalidCandidateWorkspace)
		}
		selected = dependencyID
	}
	return selected, selected != "", nil
}

func candidateTargetsMatch(targets []candidateTargetReceipt, expected []kernel.UUIDv7) bool {
	if len(targets) != len(expected) {
		return false
	}
	for index := range targets {
		if targets[index].TaskID != expected[index] {
			return false
		}
	}
	return true
}

func (service *ProductionService) reuseFeatureValidationCandidateWorkspace(ctx context.Context, feature organization.FeatureRequest, consumer organization.PlannedTask, owner organization.RoleInstanceState, validatorTaskID kernel.UUIDv7, baseEvidence []kernel.EvidenceRef) (ProductionWorkspace, []kernel.EvidenceRef, error) {
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: validatorTaskID}})
	if err != nil {
		return ProductionWorkspace{}, nil, err
	}
	scope, found := snapshot.TaskOperationalScopes[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: validatorTaskID}]
	if !found || !strings.HasPrefix(scope.WorktreeID, "candidate-") || service.workspaceResolver == nil {
		return ProductionWorkspace{}, nil, errInvalidCandidateWorkspace
	}
	binding, err := service.workspaceResolver.ResolveWorkspace(ctx, scope)
	if err != nil || binding.Candidate == nil {
		return ProductionWorkspace{}, nil, errors.Join(errInvalidCandidateWorkspace, err)
	}
	candidateID := kernel.UUIDv7(binding.Candidate.CandidateID)
	receipt, receiptDigest, err := service.candidates.retainedReceipt(ctx, feature.ID, validatorTaskID, candidateID)
	if err != nil || receiptDigest != binding.Candidate.ReceiptSHA256 {
		return ProductionWorkspace{}, nil, errors.Join(errInvalidCandidateWorkspace, err)
	}
	workspace, reused, reusedDigest, err := service.candidates.Reuse(ctx, feature, consumer, owner.WorkspaceID, receipt)
	if err != nil {
		return ProductionWorkspace{}, nil, err
	}
	evidenceID, err := service.registerCandidateReceipt(ctx, feature, reused, reusedDigest)
	if err != nil {
		return ProductionWorkspace{}, nil, err
	}
	evidence := append([]kernel.EvidenceRef(nil), baseEvidence...)
	evidence = append(evidence, kernel.EvidenceRef{EvidenceID: evidenceID, SHA256: reusedDigest})
	slices.SortFunc(evidence, func(left, right kernel.EvidenceRef) int {
		return strings.Compare(string(left.EvidenceID), string(right.EvidenceID))
	})
	return workspace, evidence, nil
}

// DependsOn identifies every implementation artifact a validator consumes;
// Validates identifies the task whose completion that result gates. Keeping
// those meanings separate lets a whole-feature validator bind the complete
// assembled candidate while still acting as the final gate for one DAG node.
func validationCandidateTargetIDs(plan organization.FeaturePlan, consumer organization.PlannedTask) []kernel.UUIDv7 {
	if consumer.Purpose != kernel.PurposeValidation && consumer.Purpose != kernel.PurposeReview {
		return append([]kernel.UUIDv7(nil), consumer.Validates...)
	}
	implementation := make(map[kernel.UUIDv7]struct{}, len(plan.Tasks))
	for _, task := range plan.Tasks {
		if task.Purpose == kernel.PurposeImplementation || task.Purpose == kernel.PurposeRepair {
			implementation[task.ID] = struct{}{}
		}
	}
	targets := make([]kernel.UUIDv7, 0, len(consumer.DependsOn))
	for _, dependencyID := range consumer.DependsOn {
		if _, found := implementation[dependencyID]; found {
			targets = append(targets, dependencyID)
		}
	}
	return targets
}

// wholeFeatureValidationTaskID recognizes the complete-candidate validation
// node from the DAG it consumes, not from a version-specific generated ID.
// This keeps already materialized plans executable across coordinator upgrades.
func wholeFeatureValidationTaskID(plan organization.FeaturePlan) (kernel.UUIDv7, bool) {
	implementationIDs := make([]kernel.UUIDv7, 0)
	var promotion *organization.PlannedTask
	for index := range plan.Tasks {
		task := &plan.Tasks[index]
		if task.Purpose == kernel.PurposeImplementation || task.Purpose == kernel.PurposeRepair {
			implementationIDs = append(implementationIDs, task.ID)
		}
		if task.Purpose == kernel.PurposePromotion {
			if promotion != nil {
				return "", false
			}
			promotion = task
		}
	}
	slices.Sort(implementationIDs)
	if promotion == nil || len(implementationIDs) == 0 {
		return "", false
	}
	promotionDependencies := make(map[kernel.UUIDv7]struct{}, len(promotion.DependsOn))
	for _, dependencyID := range promotion.DependsOn {
		promotionDependencies[dependencyID] = struct{}{}
	}
	var selected kernel.UUIDv7
	for _, task := range plan.Tasks {
		if task.Purpose != kernel.PurposeValidation {
			continue
		}
		if _, found := promotionDependencies[task.ID]; !found {
			continue
		}
		targets := validationCandidateTargetIDs(plan, task)
		slices.Sort(targets)
		if !slices.Equal(targets, implementationIDs) {
			continue
		}
		if selected != "" {
			return "", false
		}
		selected = task.ID
	}
	return selected, selected != ""
}

func (service *ProductionService) registerCandidateReceipt(ctx context.Context, feature organization.FeatureRequest, receipt candidateReceipt, digest kernel.Digest) (kernel.UUIDv7, error) {
	content, err := os.ReadFile(service.candidates.receiptPath(receipt))
	if err != nil || digestBytes(content) != digest {
		return "", errors.Join(errInvalidCandidateWorkspace, err)
	}
	sourceEvidenceIDs := make([]kernel.UUIDv7, 0)
	for _, target := range receipt.Targets {
		sourceEvidenceIDs = append(sourceEvidenceIDs, target.TerminalEvidenceIDs...)
	}
	slices.Sort(sourceEvidenceIDs)
	sourceEvidenceIDs = slices.Compact(sourceEvidenceIDs)
	evidenceID := deterministicOperationalUUID("candidate-receipt-evidence", string(receipt.CandidateID), string(digest))
	payload, err := json.Marshal(map[string]any{
		"access_partition": feature.Input.WorkspaceID, "availability": "AVAILABLE", "byte_length": len(content), "canonical_digest": digest,
		"computation":        map[string]any{"method": "tekrood-immutable-git-candidate-v1", "build_digest": digestBytes([]byte(candidateReceiptSchema)), "configuration_digest": digestBytes([]byte(strings.Join(service.planning.RequiredGateIDs, "\x00"))), "deterministic": true},
		"deletion_tombstone": nil, "evidence_kind": "ARTIFACT", "integrity_state": "DIGEST_VERIFIED",
		"locator": service.candidates.receiptPath(receipt), "locator_immutable": true, "media_type": "application/json",
		"producing_component": "tekrood-candidate-materializer", "producing_version": candidateReceiptSchema, "redacts": nil,
		"retention_policy": "feature-lifecycle", "sensitivity": "INTERNAL", "sha256": digest, "source_evidence_ids": sourceEvidenceIDs,
		"source_timestamp": nil, "transport_provenance": "teams-git-candidate-preflight",
	})
	if err != nil {
		return "", err
	}
	_, err = service.submitDeterministicCommand(ctx, feature, "tekroo.command.evidence.register", kernel.SchemaVersion, kernel.AggregateEvidence, evidenceID, service.serviceAuthority, 0, payload, nil, nil, "candidate-receipt-"+string(receipt.CandidateID))
	return evidenceID, err
}

func (service *ProductionService) workspaceForExistingTask(ctx context.Context, task organization.PlannedTask, owner organization.RoleInstanceState, snapshot kernel.Snapshot) (ProductionWorkspace, error) {
	base, found := service.workspacesByID[owner.WorkspaceID]
	if !found {
		return ProductionWorkspace{}, errInvalidCandidateWorkspace
	}
	scope, scoped := snapshot.TaskOperationalScopes[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}]
	if !scoped || scope.WorkspaceID == base.WorkspaceID && scope.WorktreeID == base.WorktreeID {
		return base, nil
	}
	if scope.WorkspaceID != owner.WorkspaceID || service.workspaceResolver == nil {
		return ProductionWorkspace{}, errInvalidCandidateWorkspace
	}
	binding, err := service.workspaceResolver.ResolveWorkspace(ctx, scope)
	if err != nil {
		return ProductionWorkspace{}, errors.Join(errInvalidCandidateWorkspace, err)
	}
	if strings.HasPrefix(scope.WorktreeID, "candidate-") {
		if binding.Candidate == nil || binding.Candidate.CandidateCommit != scope.BaselineSHA {
			return ProductionWorkspace{}, errInvalidCandidateWorkspace
		}
	} else if binding.Candidate != nil {
		return ProductionWorkspace{}, errInvalidCandidateWorkspace
	}
	return ProductionWorkspace{WorkspaceID: scope.WorkspaceID, WorktreeID: scope.WorktreeID, WorkingDirectory: binding.WorkingDirectory, Branch: scope.Branch, BaselineSHA: scope.BaselineSHA, WritablePaths: append([]string(nil), scope.WritablePaths...)}, nil
}

func (service *ProductionService) validateCandidateResult(ctx context.Context, task organization.PlannedTask, snapshot kernel.Snapshot, result structuredValidationResult) error {
	scope, found := snapshot.TaskOperationalScopes[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}]
	if !found {
		return errInvalidCandidateWorkspace
	}
	if !strings.HasPrefix(scope.WorktreeID, "candidate-") {
		if result.CandidateID != "" || result.CandidateReceiptSHA256 != "" {
			return errInvalidCandidateWorkspace
		}
		return nil
	}
	binding, err := service.workspaceResolver.ResolveWorkspace(ctx, scope)
	if err != nil || binding.Candidate == nil || result.CandidateID != kernel.UUIDv7(binding.Candidate.CandidateID) || result.CandidateReceiptSHA256 != binding.Candidate.ReceiptSHA256 {
		return errors.Join(errInvalidCandidateWorkspace, err)
	}
	return nil
}

func (service *ProductionService) rebindTaskCandidateWorkspace(ctx context.Context, feature organization.FeatureRequest, task *trackedTask, workspace ProductionWorkspace, evidence []kernel.EvidenceRef) error {
	if task == nil || !strings.HasPrefix(workspace.WorktreeID, "candidate-") || len(evidence) < 2 {
		return fmt.Errorf("%w: rebind task %v worktree %q evidence %d", errInvalidCandidateWorkspace, task, workspace.WorktreeID, len(evidence))
	}
	taskRef := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.plan.ID}
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: taskRef})
	if err != nil {
		return err
	}
	if current, found := snapshot.TaskOperationalScopes[taskRef]; found && current.Valid() &&
		current.TaskID == task.plan.ID && current.LifecycleEpoch == task.profile.LifecycleEpoch && current.ScopeRevision == task.profile.ScopeRevision &&
		current.OwnerFQN == task.owner.ActorFQN && current.Execution == task.owner.Execution &&
		current.WorkspaceID == workspace.WorkspaceID && current.WorktreeID == workspace.WorktreeID && current.Branch == workspace.Branch &&
		current.BaselineSHA == workspace.BaselineSHA && slices.Equal(current.WritablePaths, sortedStrings(workspace.WritablePaths)) &&
		slices.Equal(current.InterfaceEvidenceIDs, evidenceIDs(evidence)) {
		return nil
	}
	payload := map[string]any{
		"task_id": task.plan.ID, "expected_task_revision": task.revision, "lifecycle_epoch": task.profile.LifecycleEpoch,
		"scope_revision": task.profile.ScopeRevision, "owner_fqn": task.owner.ActorFQN,
		"execution_id": task.owner.Execution.ExecutionID, "fencing_epoch": task.owner.Execution.FencingEpoch,
		"workspace_id": workspace.WorkspaceID, "worktree_id": workspace.WorktreeID, "branch": workspace.Branch,
		"baseline_sha": workspace.BaselineSHA, "writable_paths": workspace.WritablePaths,
		"interface_constraint_evidence_ids": evidenceIDs(evidence),
	}
	key := fmt.Sprintf("candidate-scope-%s-execution-%s-%d", workspace.WorktreeID, task.owner.Execution.ExecutionID, task.owner.Execution.FencingEpoch)
	return service.applyTaskCommand(ctx, feature, task, "tekroo.command.task.bind-operational-scope", kernel.OperationalSchemaVersion, service.policyAuthority, payload, evidence, nil, key)
}

func (service *ProductionService) candidateArtifactDigest(ctx context.Context, taskID kernel.UUIDv7, snapshot kernel.Snapshot) (kernel.Digest, error) {
	scope, found := snapshot.TaskOperationalScopes[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: taskID}]
	if !found || !strings.HasPrefix(scope.WorktreeID, "candidate-") || service.workspaceResolver == nil {
		return "", errInvalidCandidateWorkspace
	}
	binding, err := service.workspaceResolver.ResolveWorkspace(ctx, scope)
	if err != nil || binding.Candidate == nil || !binding.Candidate.DiffSHA256.Valid() {
		return "", errors.Join(errInvalidCandidateWorkspace, err)
	}
	return binding.Candidate.DiffSHA256, nil
}

func appendTaskScopeEvidence(snapshot kernel.Snapshot, evidence []kernel.EvidenceRef, taskIDs ...kernel.UUIDv7) ([]kernel.EvidenceRef, error) {
	byID := make(map[kernel.UUIDv7]kernel.EvidenceRef, len(evidence))
	for _, item := range evidence {
		byID[item.EvidenceID] = item
	}
	for _, taskID := range taskIDs {
		scope, found := snapshot.TaskOperationalScopes[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: taskID}]
		if !found {
			return nil, errInvalidCandidateWorkspace
		}
		for _, evidenceID := range scope.InterfaceEvidenceIDs {
			metadata, present := snapshot.Evidence[evidenceID]
			if !present || !metadata.Available || !metadata.SHA256.Valid() {
				return nil, errInvalidCandidateWorkspace
			}
			byID[evidenceID] = kernel.EvidenceRef{EvidenceID: evidenceID, SHA256: metadata.SHA256}
		}
	}
	result := make([]kernel.EvidenceRef, 0, len(byID))
	for _, item := range byID {
		result = append(result, item)
	}
	slices.SortFunc(result, func(left, right kernel.EvidenceRef) int {
		return strings.Compare(string(left.EvidenceID), string(right.EvidenceID))
	})
	return result, nil
}
