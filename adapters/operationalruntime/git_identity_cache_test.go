package operationalruntime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGitIdentityCacheReadsMutableRefsAndObjectBytes(t *testing.T) {
	root, _, _ := candidateRepository(t)
	m := &taskWorkspaceManager{gitBinary: "git", timeout: time.Minute, identities: &gitIdentityCache{}}
	first, tree, err := m.observeGitIdentity(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	// A hit must not start Git. Refs and loose object bytes are still reread.
	m.gitBinary = "/not-an-executable"
	if c, tr, err := m.observeGitIdentity(t.Context(), root); err != nil || c != first || tr != tree {
		t.Fatalf("cache miss: %s %s %v", c, tr, err)
	}
	commitTaskFile(t, root, "new.txt", "changed\n", "advance ref")
	if _, _, err := m.observeGitIdentity(t.Context(), root); err == nil {
		t.Fatal("changed HEAD reused stale identity")
	}
	m.gitBinary = "git"
	second, _, err := m.observeGitIdentity(t.Context(), root)
	if err != nil || second == first {
		t.Fatalf("changed identity not resolved: %v", err)
	}
	object := filepath.Join(root, ".git", "objects", second[:2], second[2:])
	if err := os.Remove(object); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.observeGitIdentity(t.Context(), root); err == nil {
		t.Fatal("missing object reused cached tree")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := m.observeGitIdentity(ctx, root); err != context.Canceled {
		t.Fatalf("canceled lookup: %v", err)
	}
}

func TestGitIdentityCachePackedObjectsFallBackToGit(t *testing.T) {
	root, _, _ := candidateRepository(t)
	candidateGit(t, root, "gc", "--prune=now")
	if _, err := gitIdentityFingerprint(root); err == nil {
		t.Fatal("packed identity trusted metadata")
	}
	m := &taskWorkspaceManager{gitBinary: "git", timeout: time.Minute, identities: &gitIdentityCache{}}
	if _, _, err := m.observeGitIdentity(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	m.gitBinary = "/not-an-executable"
	if _, _, err := m.observeGitIdentity(t.Context(), root); err == nil {
		t.Fatal("packed lookup unexpectedly cached")
	}
}

func TestGitIdentityCacheNeverHidesReplacementRefs(t *testing.T) {
	root, baseline, head := candidateRepository(t)
	m := &taskWorkspaceManager{gitBinary: "git", timeout: time.Minute, identities: &gitIdentityCache{}}
	_, original, err := m.observeGitIdentity(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	candidateGit(t, root, "replace", head, baseline)
	for _, pack := range []bool{false, true} {
		if pack {
			candidateGit(t, root, "pack-refs", "--all")
		}
		if _, err := gitIdentityFingerprint(root); err == nil {
			t.Fatal("replacement ref admitted to cache")
		}
		_, tree, err := m.observeGitIdentity(t.Context(), root)
		if err != nil || tree == original || tree != candidateGit(t, root, "rev-parse", "HEAD^{tree}") {
			t.Fatalf("replacement hidden: %s %v", tree, err)
		}
	}
}
