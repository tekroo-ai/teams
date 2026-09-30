package operationalruntime

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

//go:embed assets/sma_context_hook.py
var runtimeHookAssets embed.FS

var errRuntimeHookUnavailable = errors.New("required OpenHands runtime hook is unavailable")

func canonicalRuntimeHook() ([]byte, error) {
	return runtimeHookAssets.ReadFile("assets/sma_context_hook.py")
}

// ensureConfiguredRuntimeHooks makes the qualified prompt hook a fixture of
// every bound Teams workspace. A missing hook is installed atomically; a
// divergent or linked hook is never overwritten.
func ensureConfiguredRuntimeHooks(workspaces []ProductionWorkspace) error {
	hook, err := canonicalRuntimeHook()
	if err != nil {
		return fmt.Errorf("%w: embedded hook: %v", errRuntimeHookUnavailable, err)
	}
	ordered := append([]ProductionWorkspace(nil), workspaces...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].WorkspaceID < ordered[j].WorkspaceID })
	for _, workspace := range ordered {
		if err := ensureWorkspaceRuntimeHook(workspace.WorkingDirectory, hook); err != nil {
			return fmt.Errorf("%w: workspace %s: %v", errRuntimeHookUnavailable, workspace.WorkspaceID, err)
		}
	}
	return nil
}

func ensureWorkspaceRuntimeHook(workspace string, hook []byte) error {
	root := filepath.Join(workspace, ".openhands")
	directory := filepath.Join(root, "hooks")
	for _, path := range []string{root, directory} {
		if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("hook directory %s is not a real directory", path)
		}
	}
	path := filepath.Join(directory, "sma_context_hook.py")
	if _, err := os.Lstat(path); err == nil {
		return verifyWorkspaceRuntimeHook(path, hook)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".sma_context_hook-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if err := temporary.Chmod(0o700); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(hook); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Link(temporary.Name(), path); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return verifyWorkspaceRuntimeHook(path, hook)
}

func verifyWorkspaceRuntimeHook(path string, hook []byte) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o700 {
		return fmt.Errorf("hook %s is missing, linked, or does not have mode 0700", path)
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(actual, hook) {
		return fmt.Errorf("hook %s differs from Teams fixture (sha256 %x, expected %x)", path, sha256.Sum256(actual), sha256.Sum256(hook))
	}
	return nil
}
