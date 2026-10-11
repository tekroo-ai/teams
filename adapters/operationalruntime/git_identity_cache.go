package operationalruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type observedGitIdentity struct {
	fingerprint  string
	commit, tree string
}
type gitIdentityCache struct {
	sync.Mutex
	entries map[string]observedGitIdentity
}

// Task repositories own independent .git directories. Re-read mutable refs and
// object-storage identity on every observation; cache only the immutable
// commit->tree resolution. Linked worktrees/unusual layouts fall back to Git.
// No dirty-worktree, ancestry, receipt or readiness check is cached here.
func gitIdentityFingerprint(directory string) (string, error) {
	// Git can redirect its object/ref interpretation through the environment.
	// Those exceptional repositories retain ordinary Git resolution each time.
	for _, name := range []string{"GIT_DIR", "GIT_COMMON_DIR", "GIT_WORK_TREE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_REPLACE_REF_BASE", "GIT_NAMESPACE", "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS"} {
		if os.Getenv(name) != "" {
			return "", errInvalidTaskWorkspace
		}
	}
	root := filepath.Join(directory, ".git")
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errInvalidTaskWorkspace
	}
	h := sha256.New()
	read := func(path string, optional bool) ([]byte, error) {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) && optional {
			fmt.Fprintln(h, path, "absent")
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return nil, errInvalidTaskWorkspace
		}
		data, err := os.ReadFile(path)
		if err == nil {
			fmt.Fprintln(h, path)
			h.Write(data)
		}
		return data, err
	}
	if _, err := read(filepath.Join(root, "config"), false); err != nil {
		return "", err
	}
	packed, err := read(filepath.Join(root, "packed-refs"), true)
	if err != nil {
		return "", err
	}
	if strings.Contains(string(packed), " refs/replace/") {
		return "", errInvalidTaskWorkspace
	}
	replacements, err := os.ReadDir(filepath.Join(root, "refs", "replace"))
	if err != nil && !os.IsNotExist(err) || len(replacements) > 0 {
		return "", errInvalidTaskWorkspace
	}
	head, err := read(filepath.Join(root, "HEAD"), false)
	if err != nil {
		return "", err
	}
	commit := strings.TrimSpace(string(head))
	if strings.HasPrefix(commit, "ref: ") {
		ref := strings.TrimPrefix(commit, "ref: ")
		if !strings.HasPrefix(ref, "refs/") || filepath.Clean(ref) != ref || strings.Contains(ref, "..") {
			return "", errInvalidTaskWorkspace
		}
		data, err := read(filepath.Join(root, ref), true)
		if err != nil {
			return "", err
		}
		commit = strings.TrimSpace(string(data))
		if commit == "" {
			for _, line := range strings.Split(string(packed), "\n") {
				fields := strings.Fields(line)
				if len(fields) == 2 && fields[1] == ref {
					commit = fields[0]
				}
			}
		}
	}
	if !validGitCommit(commit) {
		return "", errInvalidTaskWorkspace
	}
	// Cache only loose commit objects whose actual bytes can be reread cheaply.
	// Packed identities fall back to Git; file metadata is not content evidence.
	if _, err := read(filepath.Join(root, "objects", commit[:2], commit[2:]), false); err != nil {
		return "", err
	}
	// Alternates/shared objects are not this cache's ownership boundary.
	if _, err := os.Lstat(filepath.Join(root, "objects", "info", "alternates")); !os.IsNotExist(err) {
		return "", errInvalidTaskWorkspace
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (manager *taskWorkspaceManager) observeGitIdentity(ctx context.Context, directory string) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	fingerprint, fpErr := gitIdentityFingerprint(directory)
	cache := manager.identities
	if cache != nil && fpErr == nil {
		cache.Lock()
		previous, found := cache.entries[directory]
		cache.Unlock()
		if found && previous.fingerprint == fingerprint {
			return previous.commit, previous.tree, nil
		}
	}
	// Resolve both identities in one process, with ordinary Git error semantics.
	output, err := manager.gitText(ctx, directory, "rev-parse", "HEAD", "HEAD^{tree}")
	if err != nil {
		return "", "", err
	}
	lines := strings.Fields(output)
	if len(lines) != 2 || !validGitCommit(lines[0]) || !validGitCommit(lines[1]) {
		return "", "", errInvalidTaskWorkspace
	}
	if cache != nil && fpErr == nil {
		after, err := gitIdentityFingerprint(directory)
		if err != nil || after != fingerprint {
			return "", "", errInvalidTaskWorkspace
		}
		cache.Lock()
		if cache.entries == nil {
			cache.entries = map[string]observedGitIdentity{}
		}
		if len(cache.entries) >= 256 {
			clear(cache.entries)
		}
		cache.entries[directory] = observedGitIdentity{fingerprint, lines[0], lines[1]}
		cache.Unlock()
	}
	return lines[0], lines[1], nil
}
