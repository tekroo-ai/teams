package agenttools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// These host operations are deliberately absent from the read-only MCP gateway.
// An agent-facing write gateway still needs admitted execution identity, durable
// replay protection, and effect-policy enforcement before it may expose them.
func (host Host) gitStageFiles(ctx context.Context, root string, paths []string, expectedHead string) (Result, error) {
	result := Result{Name: "git_stage_files"}
	if err := host.requireGitRoot(ctx, root); err != nil {
		return result, notApplied(err)
	}
	if len(paths) < 1 || len(paths) > 32 {
		return result, notApplied(ErrInvalidCall)
	}
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		if _, err := relativePath(path); err != nil {
			return result, notApplied(err)
		}
		if path == ".git" || strings.HasPrefix(path, ".git/") || path == ".openhands" || strings.HasPrefix(path, ".openhands/") || seen[path] {
			return result, notApplied(ErrInvalidCall)
		}
		seen[path] = true
		parent, err := existingPath(root, filepath.Dir(path))
		if err != nil || !withinRoot(root, parent) {
			return result, notApplied(ErrBoundary)
		}
		if _, err := os.Lstat(filepath.Join(parent, filepath.Base(path))); err != nil && !errors.Is(err, os.ErrNotExist) {
			return result, notApplied(err)
		}
	}
	actualHead, err := host.gitValue(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return result, notApplied(err)
	}
	if actualHead != expectedHead {
		return result, notApplied(ErrConflict)
	}
	// Explicit pathspecs already constrain this operation. Previously staged
	// paths are preserved, not treated as a conflict with incremental staging.
	arguments := []string{"add", "-A", "--"}
	for _, path := range paths {
		arguments = append(arguments, ":(literal)"+path)
	}
	added, err := host.command(ctx, root, result.Name, host.gitBinary(), arguments)
	if err != nil || added.ExitCode != 0 {
		return result, errors.Join(ErrInvalidCall, err)
	}
	if head, err := host.gitValue(ctx, root, "rev-parse", "HEAD"); err != nil || head != expectedHead {
		return result, ErrConflict
	}
	result.IndexTree, err = host.gitValue(ctx, root, "write-tree")
	return result, err
}

func (host Host) gitCommit(ctx context.Context, root, expectedHead, expectedTree, subject, body string) (Result, error) {
	result := Result{Name: "git_commit"}
	if err := host.requireGitRoot(ctx, root); err != nil {
		return result, notApplied(err)
	}
	actualHead, err := host.gitValue(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return result, notApplied(err)
	}
	if actualHead != expectedHead {
		parent, parentErr := host.gitValue(ctx, root, "rev-parse", "HEAD^")
		tree, treeErr := host.gitValue(ctx, root, "rev-parse", "HEAD^{tree}")
		message, messageErr := host.gitCommitMessage(ctx, root)
		if parentErr == nil && treeErr == nil && messageErr == nil && parent == expectedHead && tree == expectedTree && message == commitMessage(subject, body) {
			result.CommitSHA, result.IndexTree = actualHead, tree
			return result, nil
		}
		return result, notApplied(ErrConflict)
	}
	indexTree, err := host.gitValue(ctx, root, "write-tree")
	if err != nil {
		return result, notApplied(err)
	}
	if indexTree != expectedTree {
		return result, notApplied(ErrConflict)
	}
	parentTree, err := host.gitValue(ctx, root, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return result, notApplied(err)
	}
	if parentTree == indexTree {
		return result, notApplied(ErrConflict)
	}
	arguments := []string{"-c", "user.name=Tekroo Agent", "-c", "user.email=agent@tekroo.local", "-c", "commit.gpgsign=false", "commit-tree", indexTree, "-p", expectedHead, "-m", subject}
	if body != "" {
		arguments = append(arguments, "-m", body)
	}
	commit, err := host.command(ctx, root, result.Name, host.gitBinary(), arguments)
	if err != nil || commit.ExitCode != 0 {
		return result, ErrInvalidCall
	}
	sha := strings.TrimSpace(commit.Output)
	if !validFullGitSHA(sha) {
		return result, ErrInvalidCall
	}
	updated, err := host.command(ctx, root, result.Name, host.gitBinary(), []string{"update-ref", "HEAD", sha, expectedHead})
	if err != nil || updated.ExitCode != 0 {
		return result, ErrConflict
	}
	result.CommitSHA, result.IndexTree = sha, indexTree
	return result, nil
}

func (host Host) gitValue(ctx context.Context, root string, arguments ...string) (string, error) {
	result, err := host.command(ctx, root, "git_value", host.gitBinary(), arguments)
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", ErrInvalidCall
	}
	return strings.TrimSpace(result.Output), nil
}

func (host Host) gitCommitMessage(ctx context.Context, root string) (string, error) {
	result, err := host.command(ctx, root, "git_commit_message", host.gitBinary(), []string{"cat-file", "-p", "HEAD"})
	if err != nil || result.ExitCode != 0 {
		return "", errors.Join(ErrInvalidCall, err)
	}
	separator := strings.Index(result.Output, "\n\n")
	if separator < 0 || !strings.HasSuffix(result.Output, "\n") {
		return "", ErrInvalidCall
	}
	return strings.TrimSuffix(result.Output[separator+2:], "\n"), nil
}

func commitMessage(subject, body string) string {
	if body == "" {
		return subject
	}
	return subject + "\n\n" + body
}
