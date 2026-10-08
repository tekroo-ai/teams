package operationalruntime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

type amendmentCheckpoint struct {
	SchemaVersion      string        `json:"schema_version"`
	FeatureID          kernel.UUIDv7 `json:"feature_id"`
	SourceTaskID       kernel.UUIDv7 `json:"source_task_id"`
	SourceInvocationID kernel.UUIDv7 `json:"source_invocation_id"`
	SourceOutputDigest kernel.Digest `json:"source_output_digest"`
	SourceWorkspaceID  string        `json:"source_workspace_id"`
	SourceWorktreeID   string        `json:"source_worktree_id"`
	SourceHead         string        `json:"source_head"`
	FilesDigest        kernel.Digest `json:"files_digest"`
	Commit             string        `json:"commit"`
	Tree               string        `json:"tree"`
	WorkingDirectory   string        `json:"working_directory"`
}

// Capture creates Git objects in a private repository, never in the original
// task. Ignored build artifacts and runtime hooks are not candidate source.
// Stable terminal-invocation identity permits replay without recapturing files.
func (manager *taskWorkspaceManager) captureAmendmentCheckpoint(ctx context.Context, featureID kernel.UUIDv7, source kernel.WorkInvocation, workspace ProductionWorkspace) (amendmentCheckpoint, error) {
	if manager == nil || manager.requireRuntimeHook || !featureID.Valid() || !source.ID.Valid() || source.State != kernel.InvocationSucceeded || source.OutputDigest == nil || !source.OutputDigest.Valid() || source.TaskID == "" || source.WorkspaceID != workspace.WorkspaceID || !validTaskWorkspaceRoot(workspace) {
		return amendmentCheckpoint{}, errInvalidTaskWorkspace
	}
	base := filepath.Join(manager.root, "amendment-checkpoints")
	if err := os.MkdirAll(base, 0700); err != nil {
		return amendmentCheckpoint{}, err
	}
	key := string(deterministicOperationalUUID("amendment-checkpoint", string(featureID), string(source.ID)))
	directory, receiptPath := filepath.Join(base, key), filepath.Join(base, key+".json")
	if raw, err := os.ReadFile(receiptPath); err == nil {
		var retained amendmentCheckpoint
		if json.Unmarshal(raw, &retained) != nil || retained.SchemaVersion != "tekroo.teams.amendment-checkpoint/1.0.0" || retained.FeatureID != featureID || retained.SourceTaskID != source.TaskID || retained.SourceInvocationID != source.ID || retained.SourceOutputDigest != *source.OutputDigest || retained.SourceWorkspaceID != workspace.WorkspaceID || retained.SourceWorktreeID != workspace.WorktreeID || retained.WorkingDirectory != directory || !retained.FilesDigest.Valid() || !validGitCommit(retained.Commit) || !validGitCommit(retained.Tree) {
			return amendmentCheckpoint{}, errInvalidTaskWorkspace
		}
		head, err := manager.gitText(ctx, directory, "rev-parse", "HEAD")
		if err != nil || head != retained.Commit {
			return amendmentCheckpoint{}, errors.Join(errInvalidTaskWorkspace, err)
		}
		tree, err := manager.gitText(ctx, directory, "rev-parse", "HEAD^{tree}")
		if err != nil || tree != retained.Tree {
			return amendmentCheckpoint{}, errors.Join(errInvalidTaskWorkspace, err)
		}
		status, err := manager.git(ctx, directory, "--no-optional-locks", "status", "--porcelain=v1", "--untracked-files=all")
		if err != nil || len(status) != 0 {
			return amendmentCheckpoint{}, errors.Join(errInvalidTaskWorkspace, err)
		}
		return retained, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return amendmentCheckpoint{}, err
	}
	// A leftover directory without its synced receipt is recoverable. Build a
	// fresh deterministic capture; compare the exact commit before recording it.
	temporary, err := os.MkdirTemp(base, ".capture-")
	if err != nil {
		return amendmentCheckpoint{}, err
	}
	defer os.RemoveAll(temporary)
	head, err := manager.gitText(ctx, workspace.WorkingDirectory, "rev-parse", "HEAD")
	if err != nil {
		return amendmentCheckpoint{}, err
	}
	if _, err := manager.git(ctx, temporary, "init", "--initial-branch=checkpoint"); err != nil {
		return amendmentCheckpoint{}, err
	}
	if _, err := manager.git(ctx, temporary, "fetch", "--no-tags", "--no-write-fetch-head", workspace.WorkingDirectory, head); err != nil {
		return amendmentCheckpoint{}, err
	}
	if _, err := manager.git(ctx, temporary, "read-tree", head); err != nil {
		return amendmentCheckpoint{}, err
	}
	filesDigest, err := agenttools.CaptureRepositoryFiles(ctx, workspace.WorkingDirectory, temporary, manager.gitBinary)
	if err != nil {
		return amendmentCheckpoint{}, err
	}
	if _, err := manager.git(ctx, temporary, "-c", "core.hooksPath=/dev/null", "add", "--all", "--", "."); err != nil {
		return amendmentCheckpoint{}, err
	}
	tree, err := manager.gitText(ctx, temporary, "write-tree")
	if err != nil {
		return amendmentCheckpoint{}, err
	}
	stamp := "2000-01-01T00:00:00Z"
	environment := []string{"GIT_AUTHOR_NAME=Tekroo Teams Checkpoint", "GIT_AUTHOR_EMAIL=teams@tekroo.invalid", "GIT_AUTHOR_DATE=" + stamp, "GIT_COMMITTER_NAME=Tekroo Teams Checkpoint", "GIT_COMMITTER_EMAIL=teams@tekroo.invalid", "GIT_COMMITTER_DATE=" + stamp}
	commitRaw, err := manager.gitWithEnvironment(ctx, temporary, environment, "commit-tree", tree, "-p", head, "-m", "Preserve terminal Tekroo invocation "+string(source.ID)+" (not implementation acceptance)")
	if err != nil {
		return amendmentCheckpoint{}, err
	}
	commit := strings.TrimSpace(string(commitRaw))
	if _, err := manager.git(ctx, temporary, "update-ref", "refs/heads/checkpoint", commit); err != nil {
		return amendmentCheckpoint{}, err
	}
	// read-tree does not set HEAD's worktree baseline. The symbolic branch now
	// points at the captured bytes; no checkout/reset is needed or permitted.
	retained := amendmentCheckpoint{SchemaVersion: "tekroo.teams.amendment-checkpoint/1.0.0", FeatureID: featureID, SourceTaskID: source.TaskID, SourceInvocationID: source.ID, SourceOutputDigest: *source.OutputDigest, SourceWorkspaceID: workspace.WorkspaceID, SourceWorktreeID: workspace.WorktreeID, SourceHead: head, FilesDigest: kernel.Digest(filesDigest), Commit: commit, Tree: tree, WorkingDirectory: directory}
	if info, err := os.Lstat(directory); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return amendmentCheckpoint{}, errInvalidTaskWorkspace
		}
		priorHead, err := manager.gitText(ctx, directory, "rev-parse", "HEAD")
		if err != nil || priorHead != commit {
			return amendmentCheckpoint{}, errors.Join(errInvalidTaskWorkspace, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return amendmentCheckpoint{}, err
	} else if err := os.Rename(temporary, directory); err != nil {
		return amendmentCheckpoint{}, err
	}
	raw, err := json.Marshal(retained)
	if err != nil {
		return amendmentCheckpoint{}, err
	}
	if err := writeExclusiveSynced(receiptPath, append(raw, '\n')); err != nil {
		return amendmentCheckpoint{}, err
	}
	return retained, nil
}

func (service *ProductionService) amendmentCheckpointComponent(ctx context.Context, feature organization.FeatureRequest, task organization.PlannedTask) (*taskWorkspaceComponent, error) {
	a := feature.PlanAmendment
	if a == nil || a.State != organization.AmendmentApplied {
		return nil, nil
	}
	for _, replacement := range a.Proposal.Replacements {
		if replacement.SuccessorTaskID != task.ID || replacement.PriorTaskID != a.BlockedTaskID {
			continue
		}
		original, found := plannedTaskByID(a.PriorPlan, a.BlockedTaskID)
		if !found {
			return nil, organization.ErrPlanAmendment
		}
		workspace, err := service.plannedTaskWorkspace(ctx, original)
		if err != nil {
			return nil, err
		}
		snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: original.ID}})
		if err != nil {
			return nil, err
		}
		source, found := snapshot.WorkInvocations[kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: a.BlockedInvocationID}]
		if !found || source.State != kernel.InvocationSucceeded || source.OutputDigest == nil {
			return nil, organization.ErrPlanAmendment
		}
		for _, invocation := range snapshot.WorkInvocations {
			if invocation.TaskID == original.ID && !invocation.State.Terminal() {
				return nil, organization.ErrPlanAmendment
			}
		}
		checkpoint, err := service.taskWorkspaces.captureAmendmentCheckpoint(ctx, feature.ID, source, workspace)
		if err != nil {
			return nil, err
		}
		return &taskWorkspaceComponent{TaskID: original.ID, WorkspaceID: workspace.WorkspaceID, WorktreeID: "checkpoint-" + string(source.ID), WorkingDirectory: checkpoint.WorkingDirectory, Commit: checkpoint.Commit, Tree: checkpoint.Tree, InvocationID: source.ID, OutputSHA256: *source.OutputDigest, Readiness: "BLOCKED_CHECKPOINT", ReadinessPolicy: a.PolicyDigest}, nil
	}
	return nil, nil
}
