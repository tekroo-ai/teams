package operationalruntime

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/openhands"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestAmendmentCheckpointPreservesDirtyWorkWithoutChangingSourceGit(t *testing.T) {
	repo, baseline, head := candidateRepository(t)
	write := func(path, value string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, path), []byte(value), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("file.txt", "staged work\n", 0644)
	candidateGit(t, repo, "add", "file.txt")
	write("file.txt", "unstaged latest work\n", 0644)
	write("new.sh", "#!/bin/sh\nexit 0\n", 0755)
	write(".gitignore", "ignored.bin\n", 0644)
	write("ignored.bin", "not source", 0644)
	status := candidateGit(t, repo, "status", "--porcelain=v1", "--untracked-files=all")
	index := candidateGit(t, repo, "write-tree")
	workspace := ProductionWorkspace{WorkspaceID: "coder-1", WorktreeID: "coder-1", WorkingDirectory: repo, Branch: "source", BaselineSHA: baseline, WritablePaths: []string{"."}}
	digest := candidateTestDigest('a')
	source := kernel.WorkInvocation{ID: candidateTestUUID(9701), TaskID: candidateTestUUID(9702), WorkspaceID: workspace.WorkspaceID, State: kernel.InvocationSucceeded, OutputDigest: &digest}
	manager := &taskWorkspaceManager{root: t.TempDir(), gitBinary: "git", timeout: time.Minute}
	checkpoint, err := manager.captureAmendmentCheckpoint(context.Background(), candidateTestUUID(9703), source, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if got := candidateGit(t, repo, "rev-parse", "HEAD"); got != head {
		t.Fatal("source HEAD changed", got)
	}
	if got := candidateGit(t, repo, "write-tree"); got != index {
		t.Fatal("source index changed", got)
	}
	if got := candidateGit(t, repo, "status", "--porcelain=v1", "--untracked-files=all"); got != status {
		t.Fatal("source status changed", got)
	}
	if got := candidateGit(t, checkpoint.WorkingDirectory, "status", "--porcelain=v1", "--untracked-files=all"); got != "" {
		t.Fatal("dirty checkpoint", got)
	}
	if got := candidateGit(t, checkpoint.WorkingDirectory, "show", "HEAD:file.txt"); got != "unstaged latest work" {
		t.Fatal("lost dirty work", got)
	}
	if got := candidateGit(t, checkpoint.WorkingDirectory, "ls-files", "-s", "new.sh"); len(got) < 6 || got[:6] != "100755" {
		t.Fatal("lost executable mode", got)
	}
	for _, excluded := range []string{"ignored.bin", ".openhands/hooks/sma_context_hook.py"} {
		if _, err := os.Lstat(filepath.Join(checkpoint.WorkingDirectory, excluded)); !os.IsNotExist(err) {
			t.Fatal("excluded material copied", excluded, err)
		}
	}
	// Reopening the manager must replay the receipt, not take a new snapshot.
	write("file.txt", "later accidental change\n", 0644)
	reopened := &taskWorkspaceManager{root: manager.root, gitBinary: "git", timeout: time.Minute}
	again, err := reopened.captureAmendmentCheckpoint(context.Background(), candidateTestUUID(9703), source, workspace)
	if err != nil || !reflect.DeepEqual(checkpoint, again) {
		t.Fatal("checkpoint replay changed", err)
	}
	// The helper fixture carries a legacy hook. Native workspace creation
	// correctly forbids it; remove it from the fixture before that separate check.
	if err := os.Remove(filepath.Join(repo, ".openhands", "hooks", "sma_context_hook.py")); err != nil {
		t.Fatal(err)
	}
	resolver, err := openhands.NewBoundWorkspaceResolver([]openhands.WorkspaceBinding{{WorkspaceID: workspace.WorkspaceID, WorktreeID: workspace.WorktreeID, WorkingDirectory: workspace.WorkingDirectory}})
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := newTaskWorkspaceManagerWithMode(context.Background(), t.TempDir(), "git", time.Minute, resolver, false)
	if err != nil {
		t.Fatal(err)
	}
	component := taskWorkspaceComponent{TaskID: source.TaskID, WorkspaceID: workspace.WorkspaceID, WorktreeID: "checkpoint-" + string(source.ID), WorkingDirectory: checkpoint.WorkingDirectory, Commit: checkpoint.Commit, Tree: checkpoint.Tree, InvocationID: source.ID, OutputSHA256: digest, Readiness: "BLOCKED_CHECKPOINT", ReadinessPolicy: digest}
	successor, receipt, err := fresh.PrepareTask(context.Background(), organization.FeatureRequest{ID: candidateTestUUID(9703)}, organization.PlannedTask{ID: candidateTestUUID(9704), Purpose: kernel.PurposeImplementation}, workspace.WorkspaceID, workspace, []taskWorkspaceComponent{component})
	if err != nil {
		t.Fatal("isolated successor", err)
	}
	content, err := os.ReadFile(filepath.Join(successor.WorkingDirectory, "file.txt"))
	if err != nil || string(content) != "unstaged latest work\n" || successor.WorkingDirectory == repo || len(receipt.Components) != 1 || receipt.Components[0].Readiness != "BLOCKED_CHECKPOINT" || receipt.Components[0].InvocationID != source.ID {
		t.Fatal("successor lost checkpoint provenance/bytes", err)
	}
	if err := os.WriteFile(filepath.Join(checkpoint.WorkingDirectory, "file.txt"), []byte("tampered"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.captureAmendmentCheckpoint(context.Background(), candidateTestUUID(9703), source, workspace); err == nil {
		t.Fatal("accepted tampered checkpoint")
	}
}

func TestAmendmentCheckpointPreservesDeletionAndRejectsSymlinks(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		t.Run(map[bool]string{false: "deletion", true: "symlink"}[symlink], func(t *testing.T) {
			repo, baseline, _ := candidateRepository(t)
			if err := os.Remove(filepath.Join(repo, "file.txt")); err != nil {
				t.Fatal(err)
			}
			if symlink {
				if err := os.Symlink("/etc/hosts", filepath.Join(repo, "external")); err != nil {
					t.Fatal(err)
				}
			}
			digest := candidateTestDigest('a')
			workspace := ProductionWorkspace{WorkspaceID: "coder-1", WorktreeID: "coder-1", WorkingDirectory: repo, Branch: "source", BaselineSHA: baseline, WritablePaths: []string{"."}}
			source := kernel.WorkInvocation{ID: candidateTestUUID(9711), TaskID: candidateTestUUID(9712), WorkspaceID: workspace.WorkspaceID, State: kernel.InvocationSucceeded, OutputDigest: &digest}
			manager := &taskWorkspaceManager{root: t.TempDir(), gitBinary: "git", timeout: time.Minute}
			checkpoint, err := manager.captureAmendmentCheckpoint(context.Background(), candidateTestUUID(9713), source, workspace)
			if symlink {
				if err == nil {
					t.Fatal("copied symlink")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := candidateGit(t, checkpoint.WorkingDirectory, "ls-files", "file.txt"); got != "" {
				t.Fatal("resurrected deletion", got)
			}
		})
	}
}
