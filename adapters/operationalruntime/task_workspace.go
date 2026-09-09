package operationalruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tekroo-ai/teams/adapters/openhands"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

const taskWorkspaceReceiptSchema = "tekroo.teams.task-workspace-receipt/1.0.0"

var (
	errInvalidTaskWorkspace       = errors.New("invalid task workspace")
	errTaskWorkspaceMergeConflict = errors.New("task workspace dependency merge conflict")
)

type taskWorkspaceKind string

const (
	taskWorkspaceEditable taskWorkspaceKind = "EDITABLE_TASK"
	taskWorkspaceAssembly taskWorkspaceKind = "CANDIDATE_ASSEMBLY"
)

type taskWorkspaceComponent struct {
	TaskID           kernel.UUIDv7 `json:"task_id"`
	WorkspaceID      string        `json:"workspace_id"`
	WorktreeID       string        `json:"worktree_id"`
	WorkingDirectory string        `json:"working_directory"`
	Commit           string        `json:"commit"`
	Tree             string        `json:"tree"`
}

type taskWorkspaceReceipt struct {
	SchemaVersion      string                   `json:"schema_version"`
	Kind               taskWorkspaceKind        `json:"kind"`
	FeatureID          kernel.UUIDv7            `json:"feature_id"`
	WorkID             kernel.UUIDv7            `json:"work_id"`
	WorkspaceID        string                   `json:"workspace_id"`
	WorktreeID         string                   `json:"worktree_id"`
	WorkingDirectory   string                   `json:"working_directory"`
	Branch             string                   `json:"branch"`
	RootSource         string                   `json:"root_source"`
	RootBaselineCommit string                   `json:"root_baseline_commit"`
	PreparedCommit     string                   `json:"prepared_commit"`
	PreparedTree       string                   `json:"prepared_tree"`
	RuntimeHookSHA256  kernel.Digest            `json:"runtime_hook_sha256"`
	Components         []taskWorkspaceComponent `json:"components"`
}

type taskWorkspaceManager struct {
	root      string
	gitBinary string
	timeout   time.Duration
	resolver  *openhands.BoundWorkspaceResolver
}

func newTaskWorkspaceManagerWithContext(ctx context.Context, evidenceRoot, gitBinary string, timeout time.Duration, resolver *openhands.BoundWorkspaceResolver) (*taskWorkspaceManager, error) {
	if ctx == nil || evidenceRoot == "" || !filepath.IsAbs(evidenceRoot) || gitBinary == "" || timeout <= 0 || resolver == nil {
		return nil, errInvalidTaskWorkspace
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(errInvalidTaskWorkspace, err)
	}
	manager := &taskWorkspaceManager{root: filepath.Join(filepath.Clean(evidenceRoot), "task-workspaces"), gitBinary: gitBinary, timeout: timeout, resolver: resolver}
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

func (manager *taskWorkspaceManager) PrepareTask(ctx context.Context, feature organization.FeatureRequest, task organization.PlannedTask, ownerWorkspaceID string, root ProductionWorkspace, dependencies []taskWorkspaceComponent) (ProductionWorkspace, taskWorkspaceReceipt, error) {
	if !task.ID.Valid() {
		return ProductionWorkspace{}, taskWorkspaceReceipt{}, errInvalidTaskWorkspace
	}
	return manager.prepare(ctx, taskWorkspaceEditable, feature, task.ID, ownerWorkspaceID, root, dependencies)
}

func (manager *taskWorkspaceManager) PrepareAssembly(ctx context.Context, feature organization.FeatureRequest, workspaceID string, root ProductionWorkspace, components []taskWorkspaceComponent) (ProductionWorkspace, taskWorkspaceReceipt, error) {
	if len(components) == 0 {
		return ProductionWorkspace{}, taskWorkspaceReceipt{}, errInvalidTaskWorkspace
	}
	seed, err := json.Marshal(struct {
		FeatureID  kernel.UUIDv7            `json:"feature_id"`
		Baseline   string                   `json:"baseline"`
		Components []taskWorkspaceComponent `json:"components"`
	}{feature.ID, root.BaselineSHA, components})
	if err != nil {
		return ProductionWorkspace{}, taskWorkspaceReceipt{}, err
	}
	workID := deterministicOperationalUUID("feature-candidate-assembly", string(feature.ID), string(digestBytes(seed)))
	return manager.prepare(ctx, taskWorkspaceAssembly, feature, workID, workspaceID, root, components)
}

func (manager *taskWorkspaceManager) prepare(ctx context.Context, kind taskWorkspaceKind, feature organization.FeatureRequest, workID kernel.UUIDv7, workspaceID string, root ProductionWorkspace, components []taskWorkspaceComponent) (ProductionWorkspace, taskWorkspaceReceipt, error) {
	if manager == nil || !feature.ID.Valid() || !workID.Valid() || workspaceID == "" || !validTaskWorkspaceRoot(root) || kind != taskWorkspaceEditable && kind != taskWorkspaceAssembly {
		return ProductionWorkspace{}, taskWorkspaceReceipt{}, errInvalidTaskWorkspace
	}
	if kind == taskWorkspaceAssembly && len(components) == 0 {
		return ProductionWorkspace{}, taskWorkspaceReceipt{}, errInvalidTaskWorkspace
	}
	observed, err := manager.observeComponents(ctx, root.BaselineSHA, components)
	if err != nil {
		return ProductionWorkspace{}, taskWorkspaceReceipt{}, err
	}
	worktreeID := "task-" + string(workID)
	branchPrefix := "task"
	if kind == taskWorkspaceAssembly {
		worktreeID = "assembly-" + string(workID)
		branchPrefix = "assembly"
	}
	branch := "tekroo/" + branchPrefix + "/" + string(feature.ID) + "/" + string(workID)
	path := filepath.Join(manager.workspaceRoot(), worktreeID)
	receiptPath := filepath.Join(manager.receiptRoot(), worktreeID+".json")
	if content, readErr := os.ReadFile(receiptPath); readErr == nil {
		var receipt taskWorkspaceReceipt
		if json.Unmarshal(content, &receipt) != nil || !manager.receiptMatches(receipt, kind, feature.ID, workID, workspaceID, root, observed, branch, path) {
			return ProductionWorkspace{}, taskWorkspaceReceipt{}, errInvalidTaskWorkspace
		}
		workspace := receipt.productionWorkspace()
		if err := manager.verify(ctx, receipt); err != nil {
			return ProductionWorkspace{}, taskWorkspaceReceipt{}, err
		}
		if err := manager.register(workspace); err != nil {
			return ProductionWorkspace{}, taskWorkspaceReceipt{}, err
		}
		return workspace, receipt, nil
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return ProductionWorkspace{}, taskWorkspaceReceipt{}, readErr
	}
	pathExists := false
	if info, err := os.Lstat(path); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return ProductionWorkspace{}, taskWorkspaceReceipt{}, errInvalidTaskWorkspace
		}
		pathExists = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return ProductionWorkspace{}, taskWorkspaceReceipt{}, err
	}
	temporary, err := os.MkdirTemp(manager.workspaceRoot(), ".task-*")
	if err != nil {
		return ProductionWorkspace{}, taskWorkspaceReceipt{}, err
	}
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.RemoveAll(temporary)
		}
	}()
	if _, err := manager.git(ctx, temporary, "init", "--initial-branch=bootstrap"); err != nil {
		return ProductionWorkspace{}, taskWorkspaceReceipt{}, err
	}
	if _, err := manager.git(ctx, temporary, "fetch", "--no-tags", "--no-write-fetch-head", root.WorkingDirectory, root.BaselineSHA); err != nil {
		return ProductionWorkspace{}, taskWorkspaceReceipt{}, err
	}
	if _, err := manager.git(ctx, temporary, "checkout", "-b", branch, root.BaselineSHA); err != nil {
		return ProductionWorkspace{}, taskWorkspaceReceipt{}, err
	}
	for index, component := range observed {
		if err := manager.integrate(ctx, temporary, feature.ID, index, component); err != nil {
			return ProductionWorkspace{}, taskWorkspaceReceipt{}, err
		}
	}
	hook, err := os.ReadFile(filepath.Join(root.WorkingDirectory, ".openhands", "hooks", "sma_context_hook.py"))
	if err != nil || len(hook) == 0 {
		return ProductionWorkspace{}, taskWorkspaceReceipt{}, errors.Join(errInvalidTaskWorkspace, err)
	}
	hookPath := filepath.Join(temporary, ".openhands", "hooks", "sma_context_hook.py")
	if err := os.MkdirAll(filepath.Dir(hookPath), 0o700); err != nil {
		return ProductionWorkspace{}, taskWorkspaceReceipt{}, err
	}
	if err := os.WriteFile(hookPath, hook, 0o700); err != nil {
		return ProductionWorkspace{}, taskWorkspaceReceipt{}, err
	}
	preparedCommit, err := manager.gitText(ctx, temporary, "rev-parse", "HEAD")
	if err != nil {
		return ProductionWorkspace{}, taskWorkspaceReceipt{}, err
	}
	preparedTree, err := manager.gitText(ctx, temporary, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return ProductionWorkspace{}, taskWorkspaceReceipt{}, err
	}
	receipt := taskWorkspaceReceipt{
		SchemaVersion: taskWorkspaceReceiptSchema, Kind: kind, FeatureID: feature.ID, WorkID: workID,
		WorkspaceID: workspaceID, WorktreeID: worktreeID, WorkingDirectory: path, Branch: branch,
		RootSource: filepath.Clean(root.WorkingDirectory), RootBaselineCommit: root.BaselineSHA,
		PreparedCommit: preparedCommit, PreparedTree: preparedTree, RuntimeHookSHA256: digestBytes(hook),
		Components: observed,
	}
	receiptBytes, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return ProductionWorkspace{}, taskWorkspaceReceipt{}, err
	}
	receiptBytes = append(receiptBytes, '\n')
	if pathExists {
		if err := manager.verify(ctx, receipt); err != nil {
			return ProductionWorkspace{}, taskWorkspaceReceipt{}, err
		}
		if err := writeExclusiveSynced(receiptPath, receiptBytes); err != nil {
			return ProductionWorkspace{}, taskWorkspaceReceipt{}, err
		}
		workspace := receipt.productionWorkspace()
		if err := manager.register(workspace); err != nil {
			return ProductionWorkspace{}, taskWorkspaceReceipt{}, err
		}
		return workspace, receipt, nil
	}
	if err := os.Rename(temporary, path); err != nil {
		return ProductionWorkspace{}, taskWorkspaceReceipt{}, err
	}
	removeTemporary = false
	if err := writeExclusiveSynced(receiptPath, receiptBytes); err != nil {
		return ProductionWorkspace{}, taskWorkspaceReceipt{}, err
	}
	workspace := receipt.productionWorkspace()
	if err := manager.register(workspace); err != nil {
		return ProductionWorkspace{}, taskWorkspaceReceipt{}, err
	}
	return workspace, receipt, nil
}

func validTaskWorkspaceRoot(root ProductionWorkspace) bool {
	return root.WorkspaceID != "" && root.WorktreeID != "" && filepath.IsAbs(root.WorkingDirectory) && root.Branch != "" && validGitCommit(root.BaselineSHA) && len(root.WritablePaths) > 0
}

func (manager *taskWorkspaceManager) observeComponents(ctx context.Context, rootBaseline string, components []taskWorkspaceComponent) ([]taskWorkspaceComponent, error) {
	result := make([]taskWorkspaceComponent, len(components))
	seen := make(map[kernel.UUIDv7]struct{}, len(components))
	for index, component := range components {
		if !component.TaskID.Valid() || component.WorkspaceID == "" || component.WorktreeID == "" || !filepath.IsAbs(component.WorkingDirectory) {
			return nil, errInvalidTaskWorkspace
		}
		if _, duplicate := seen[component.TaskID]; duplicate {
			return nil, errInvalidTaskWorkspace
		}
		seen[component.TaskID] = struct{}{}
		status, err := manager.git(ctx, component.WorkingDirectory, "--no-optional-locks", "status", "--porcelain=v1", "--untracked-files=all")
		if err != nil || !candidateSourceStatusClean(status) {
			return nil, errors.Join(errInvalidTaskWorkspace, err)
		}
		commit, err := manager.gitText(ctx, component.WorkingDirectory, "rev-parse", "HEAD")
		if err != nil {
			return nil, err
		}
		tree, err := manager.gitText(ctx, component.WorkingDirectory, "rev-parse", "HEAD^{tree}")
		if err != nil {
			return nil, err
		}
		ancestor, err := manager.isAncestor(ctx, component.WorkingDirectory, rootBaseline, commit)
		if err != nil || !ancestor || commit == rootBaseline {
			return nil, errors.Join(errInvalidTaskWorkspace, err)
		}
		component.Commit, component.Tree = commit, tree
		result[index] = component
	}
	return result, nil
}

func (manager *taskWorkspaceManager) integrate(ctx context.Context, directory string, featureID kernel.UUIDv7, index int, component taskWorkspaceComponent) error {
	if _, err := manager.git(ctx, directory, "fetch", "--no-tags", "--no-write-fetch-head", component.WorkingDirectory, component.Commit); err != nil {
		return err
	}
	current, err := manager.gitText(ctx, directory, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	currentBeforeTarget, err := manager.isAncestor(ctx, directory, current, component.Commit)
	if err != nil {
		return err
	}
	if currentBeforeTarget {
		_, err = manager.git(ctx, directory, "merge", "--ff-only", component.Commit)
		return err
	}
	targetBeforeCurrent, err := manager.isAncestor(ctx, directory, component.Commit, current)
	if err != nil {
		return err
	}
	if targetBeforeCurrent {
		return nil
	}
	if _, err := manager.git(ctx, directory, "merge", "--no-commit", "--no-ff", component.Commit); err != nil {
		return fmt.Errorf("%w: task %s: %v", errTaskWorkspaceMergeConflict, component.TaskID, err)
	}
	message := fmt.Sprintf("Integrate Tekroo task %s (%d)", component.TaskID, index+1)
	stamp := "2000-01-01T00:00:00Z"
	environment := []string{
		"GIT_AUTHOR_NAME=Tekroo Teams", "GIT_AUTHOR_EMAIL=teams@tekroo.invalid", "GIT_AUTHOR_DATE=" + stamp,
		"GIT_COMMITTER_NAME=Tekroo Teams", "GIT_COMMITTER_EMAIL=teams@tekroo.invalid", "GIT_COMMITTER_DATE=" + stamp,
		"TEKROO_FEATURE_ID=" + string(featureID),
	}
	if _, err := manager.gitWithEnvironment(ctx, directory, environment, "commit", "--no-gpg-sign", "-m", message); err != nil {
		return err
	}
	return nil
}

func (manager *taskWorkspaceManager) isAncestor(ctx context.Context, directory, ancestor, descendant string) (bool, error) {
	commandCtx, cancel := context.WithTimeout(ctx, manager.timeout)
	defer cancel()
	command := exec.CommandContext(commandCtx, manager.gitBinary, "merge-base", "--is-ancestor", ancestor, descendant)
	command.Dir = directory
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC", "GIT_TERMINAL_PROMPT=0")
	err := command.Run()
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

func (manager *taskWorkspaceManager) gitText(ctx context.Context, directory string, arguments ...string) (string, error) {
	output, err := manager.git(ctx, directory, arguments...)
	return strings.TrimSpace(string(output)), err
}

func (manager *taskWorkspaceManager) git(ctx context.Context, directory string, arguments ...string) ([]byte, error) {
	return manager.gitWithEnvironment(ctx, directory, nil, arguments...)
}

func (manager *taskWorkspaceManager) gitWithEnvironment(ctx context.Context, directory string, environment []string, arguments ...string) ([]byte, error) {
	commandCtx, cancel := context.WithTimeout(ctx, manager.timeout)
	defer cancel()
	command := exec.CommandContext(commandCtx, manager.gitBinary, arguments...)
	command.Dir = directory
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC", "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat", "PAGER=cat")
	command.Env = append(command.Env, environment...)
	output, err := command.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("git %s: %w: %s", arguments[0], err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func (receipt taskWorkspaceReceipt) productionWorkspace() ProductionWorkspace {
	baseline := receipt.PreparedCommit
	if receipt.Kind == taskWorkspaceAssembly {
		baseline = receipt.RootBaselineCommit
	}
	return ProductionWorkspace{WorkspaceID: receipt.WorkspaceID, WorktreeID: receipt.WorktreeID, WorkingDirectory: receipt.WorkingDirectory, Branch: receipt.Branch, BaselineSHA: baseline, WritablePaths: []string{"."}}
}

func (manager *taskWorkspaceManager) receiptMatches(receipt taskWorkspaceReceipt, kind taskWorkspaceKind, featureID, workID kernel.UUIDv7, workspaceID string, root ProductionWorkspace, components []taskWorkspaceComponent, branch, path string) bool {
	return receipt.SchemaVersion == taskWorkspaceReceiptSchema && receipt.Kind == kind && receipt.FeatureID == featureID && receipt.WorkID == workID && receipt.WorkspaceID == workspaceID && receipt.WorktreeID == filepath.Base(path) && receipt.WorkingDirectory == path && receipt.Branch == branch && receipt.RootSource == filepath.Clean(root.WorkingDirectory) && receipt.RootBaselineCommit == root.BaselineSHA && receipt.PreparedCommit != "" && receipt.PreparedTree != "" && receipt.RuntimeHookSHA256.Valid() && slices.Equal(receipt.Components, components)
}

func (manager *taskWorkspaceManager) verify(ctx context.Context, receipt taskWorkspaceReceipt) error {
	if receipt.SchemaVersion != taskWorkspaceReceiptSchema || receipt.Kind != taskWorkspaceEditable && receipt.Kind != taskWorkspaceAssembly || !receipt.FeatureID.Valid() || !receipt.WorkID.Valid() || receipt.WorkspaceID == "" || receipt.WorktreeID == "" || !filepath.IsAbs(receipt.WorkingDirectory) || filepath.Clean(receipt.WorkingDirectory) != filepath.Join(manager.workspaceRoot(), receipt.WorktreeID) || receipt.Branch == "" || !filepath.IsAbs(receipt.RootSource) || !validGitCommit(receipt.RootBaselineCommit) || !validGitCommit(receipt.PreparedCommit) || !validGitCommit(receipt.PreparedTree) || !receipt.RuntimeHookSHA256.Valid() {
		return errInvalidTaskWorkspace
	}
	branch, err := manager.gitText(ctx, receipt.WorkingDirectory, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || branch != receipt.Branch {
		return errors.Join(errInvalidTaskWorkspace, err)
	}
	head, err := manager.gitText(ctx, receipt.WorkingDirectory, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	ancestor, err := manager.isAncestor(ctx, receipt.WorkingDirectory, receipt.PreparedCommit, head)
	if err != nil || !ancestor {
		return errors.Join(errInvalidTaskWorkspace, err)
	}
	if receipt.Kind == taskWorkspaceAssembly && head != receipt.PreparedCommit {
		return errInvalidTaskWorkspace
	}
	preparedTree, err := manager.gitText(ctx, receipt.WorkingDirectory, "rev-parse", receipt.PreparedCommit+"^{tree}")
	if err != nil || preparedTree != receipt.PreparedTree {
		return errors.Join(errInvalidTaskWorkspace, err)
	}
	hook, err := os.ReadFile(filepath.Join(receipt.WorkingDirectory, ".openhands", "hooks", "sma_context_hook.py"))
	if err != nil || digestBytes(hook) != receipt.RuntimeHookSHA256 {
		return errors.Join(errInvalidTaskWorkspace, err)
	}
	if receipt.Kind == taskWorkspaceAssembly {
		status, err := manager.git(ctx, receipt.WorkingDirectory, "--no-optional-locks", "status", "--porcelain=v1", "--untracked-files=all")
		if err != nil || !candidateSourceStatusClean(status) {
			return errors.Join(errInvalidTaskWorkspace, err)
		}
	}
	return nil
}

func (manager *taskWorkspaceManager) register(workspace ProductionWorkspace) error {
	return manager.resolver.RegisterWorkspace(openhands.WorkspaceBinding{WorkspaceID: workspace.WorkspaceID, WorktreeID: workspace.WorktreeID, WorkingDirectory: workspace.WorkingDirectory})
}

func (manager *taskWorkspaceManager) receiptPath(receipt taskWorkspaceReceipt) string {
	return filepath.Join(manager.receiptRoot(), receipt.WorktreeID+".json")
}

func (manager *taskWorkspaceManager) receiptRoot() string {
	return filepath.Join(manager.root, "receipts")
}

func (manager *taskWorkspaceManager) workspaceRoot() string {
	return filepath.Join(manager.root, "workspaces")
}

func (manager *taskWorkspaceManager) rehydrate(ctx context.Context) error {
	entries, err := os.ReadDir(manager.receiptRoot())
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		content, err := os.ReadFile(filepath.Join(manager.receiptRoot(), entry.Name()))
		if err != nil {
			return err
		}
		var receipt taskWorkspaceReceipt
		if json.Unmarshal(content, &receipt) != nil || entry.Name() != receipt.WorktreeID+".json" {
			return errInvalidTaskWorkspace
		}
		if err := manager.verify(ctx, receipt); err != nil {
			return err
		}
		if err := manager.register(receipt.productionWorkspace()); err != nil {
			return err
		}
	}
	return nil
}

func (service *ProductionService) prepareImplementationWorkspace(ctx context.Context, feature organization.FeatureRequest, task organization.PlannedTask, owner organization.RoleInstanceState, plan organization.FeaturePlan) (ProductionWorkspace, kernel.EvidenceRef, error) {
	if service == nil || service.taskWorkspaces == nil || task.Purpose != kernel.PurposeImplementation && task.Purpose != kernel.PurposeRepair || owner.ActorFQN != task.Owner || owner.WorkspaceID == "" {
		return ProductionWorkspace{}, kernel.EvidenceRef{}, errInvalidTaskWorkspace
	}
	root, found := service.workspacesByID[owner.WorkspaceID]
	if !found {
		return ProductionWorkspace{}, kernel.EvidenceRef{}, errInvalidTaskWorkspace
	}
	wanted := make(map[kernel.UUIDv7]struct{}, len(task.DependsOn))
	for _, dependencyID := range task.DependsOn {
		wanted[dependencyID] = struct{}{}
	}
	components := make([]taskWorkspaceComponent, 0, len(wanted))
	for _, dependency := range plan.Tasks {
		if _, required := wanted[dependency.ID]; !required || dependency.Purpose != kernel.PurposeImplementation && dependency.Purpose != kernel.PurposeRepair {
			continue
		}
		workspace, err := service.plannedTaskWorkspace(ctx, dependency)
		if err != nil {
			return ProductionWorkspace{}, kernel.EvidenceRef{}, err
		}
		components = append(components, taskWorkspaceComponent{TaskID: dependency.ID, WorkspaceID: workspace.WorkspaceID, WorktreeID: workspace.WorktreeID, WorkingDirectory: workspace.WorkingDirectory})
	}
	workspace, receipt, err := service.taskWorkspaces.PrepareTask(ctx, feature, task, owner.WorkspaceID, root, components)
	if err != nil {
		return ProductionWorkspace{}, kernel.EvidenceRef{}, err
	}
	evidence, err := service.registerTaskWorkspaceReceipt(ctx, feature, receipt)
	if err != nil {
		return ProductionWorkspace{}, kernel.EvidenceRef{}, err
	}
	return workspace, evidence, nil
}

func (service *ProductionService) plannedTaskWorkspace(ctx context.Context, task organization.PlannedTask) (ProductionWorkspace, error) {
	owner, active, err := service.RoleHost.Status(ctx, task.Owner)
	if err != nil || !active {
		return ProductionWorkspace{}, errors.Join(errInvalidTaskWorkspace, err)
	}
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}})
	if err != nil {
		return ProductionWorkspace{}, err
	}
	return service.workspaceForExistingTask(ctx, task, owner, snapshot)
}

func (service *ProductionService) prepareAssembledCandidateSource(ctx context.Context, feature organization.FeatureRequest, plan organization.FeaturePlan, targetIDs []kernel.UUIDv7) (ProductionWorkspace, kernel.EvidenceRef, error) {
	if service == nil || service.taskWorkspaces == nil || len(targetIDs) == 0 {
		return ProductionWorkspace{}, kernel.EvidenceRef{}, errInvalidTaskWorkspace
	}
	targetSet := make(map[kernel.UUIDv7]struct{}, len(targetIDs))
	for _, targetID := range targetIDs {
		targetSet[targetID] = struct{}{}
	}
	components := make([]taskWorkspaceComponent, 0, len(targetIDs))
	var root ProductionWorkspace
	for _, target := range plan.Tasks {
		if _, wanted := targetSet[target.ID]; !wanted {
			continue
		}
		workspace, err := service.plannedTaskWorkspace(ctx, target)
		if err != nil {
			return ProductionWorkspace{}, kernel.EvidenceRef{}, err
		}
		if root.WorkspaceID == "" {
			owner, active, err := service.RoleHost.Status(ctx, target.Owner)
			if err != nil || !active {
				return ProductionWorkspace{}, kernel.EvidenceRef{}, errors.Join(errInvalidTaskWorkspace, err)
			}
			var found bool
			root, found = service.workspacesByID[owner.WorkspaceID]
			if !found {
				return ProductionWorkspace{}, kernel.EvidenceRef{}, errInvalidTaskWorkspace
			}
		}
		components = append(components, taskWorkspaceComponent{TaskID: target.ID, WorkspaceID: workspace.WorkspaceID, WorktreeID: workspace.WorktreeID, WorkingDirectory: workspace.WorkingDirectory})
	}
	if len(components) != len(targetSet) {
		return ProductionWorkspace{}, kernel.EvidenceRef{}, errInvalidTaskWorkspace
	}
	workspace, receipt, err := service.taskWorkspaces.PrepareAssembly(ctx, feature, root.WorkspaceID, root, components)
	if err != nil {
		return ProductionWorkspace{}, kernel.EvidenceRef{}, err
	}
	evidence, err := service.registerTaskWorkspaceReceipt(ctx, feature, receipt)
	if err != nil {
		return ProductionWorkspace{}, kernel.EvidenceRef{}, err
	}
	return workspace, evidence, nil
}

func (service *ProductionService) registerTaskWorkspaceReceipt(ctx context.Context, feature organization.FeatureRequest, receipt taskWorkspaceReceipt) (kernel.EvidenceRef, error) {
	path := service.taskWorkspaces.receiptPath(receipt)
	content, err := os.ReadFile(path)
	if err != nil {
		return kernel.EvidenceRef{}, err
	}
	digest := digestBytes(content)
	evidenceID := deterministicOperationalUUID("task-workspace-receipt-evidence", string(feature.ID), string(receipt.WorkID), string(digest))
	payload, err := json.Marshal(map[string]any{
		"access_partition": feature.Input.WorkspaceID, "availability": "AVAILABLE", "byte_length": len(content), "canonical_digest": digest,
		"computation":        map[string]any{"method": "tekrood-dag-workspace-v1", "build_digest": digestBytes([]byte(taskWorkspaceReceiptSchema)), "configuration_digest": digestBytes([]byte(receipt.Kind)), "deterministic": true},
		"deletion_tombstone": nil, "evidence_kind": "ARTIFACT", "integrity_state": "DIGEST_VERIFIED", "locator": path, "locator_immutable": true, "media_type": "application/json",
		"producing_component": "tekrood-task-workspace-manager", "producing_version": taskWorkspaceReceiptSchema, "redacts": nil,
		"retention_policy": "feature-lifecycle", "sensitivity": "INTERNAL", "sha256": digest, "source_evidence_ids": []kernel.UUIDv7{},
		"source_timestamp": nil, "transport_provenance": "teams-git-dag-workspace",
	})
	if err != nil {
		return kernel.EvidenceRef{}, err
	}
	if _, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.evidence.register", kernel.SchemaVersion, kernel.AggregateEvidence, evidenceID, service.serviceAuthority, 0, payload, nil, nil, "task-workspace-receipt-"+string(receipt.WorkID)); err != nil {
		return kernel.EvidenceRef{}, err
	}
	return kernel.EvidenceRef{EvidenceID: evidenceID, SHA256: digest}, nil
}
