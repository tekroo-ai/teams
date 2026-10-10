package agenttools

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestVerificationWorkspaceReusesOnlyPathNeverContents(t *testing.T) {
	host := Host{CompilationCache: t.TempDir()}
	first, err := host.newVerificationWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(first) })
	writeSnapshotFixture(t, first, "prior", "must not be reused or overwritten")
	if _, err := host.newVerificationWorkspace(); !errors.Is(err, os.ErrExist) {
		t.Fatalf("occupied directory was reused: %v", err)
	}
	if content, err := os.ReadFile(filepath.Join(first, "prior")); err != nil || string(content) != "must not be reused or overwritten" {
		t.Fatalf("prior contents changed: %q %v", content, err)
	}
	if err := os.RemoveAll(first); err != nil {
		t.Fatal(err)
	}
	second, err := host.newVerificationWorkspace()
	if err != nil || second != first {
		t.Fatalf("cache pathname not stable: %s %s %v", first, second, err)
	}
	if entries, err := os.ReadDir(second); err != nil || len(entries) != 0 {
		t.Fatalf("old contents retained: %v %v", entries, err)
	}
	other, err := (Host{CompilationCache: t.TempDir()}).newVerificationWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(other) })
	if other == first {
		t.Fatal("different cache shared checkout")
	}
}

func TestVerificationWorkspaceRejectsLeafSymlinkWithoutRemovingTarget(t *testing.T) {
	parent := t.TempDir()
	target := t.TempDir()
	writeSnapshotFixture(t, target, "sentinel", "preserve foreign contents")
	path := filepath.Join(parent, "workspace")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveVerificationWorkspace(path); !errors.Is(err, ErrBoundary) {
		t.Fatalf("workspace symlink accepted: %v", err)
	}
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(target, "sentinel")); err != nil || string(got) != "preserve foreign contents" {
		t.Fatalf("foreign contents changed: %q %v", got, err)
	}
}

func TestVerificationWorkspaceWithoutCacheRemainsDisposable(t *testing.T) {
	first, err := (Host{}).newVerificationWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(first) })
	second, err := (Host{}).newVerificationWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(second) })
	if first == second {
		t.Fatal("uncached runs shared a checkout")
	}
}
