package testpaths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRootUsesExecutingSnapshotAndRejectsAnotherModule(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+module+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	packageDir := filepath.Join(root, "adapters", "sample")
	if err := os.MkdirAll(packageDir, 0700); err != nil {
		t.Fatal(err)
	}
	if got, err := rootFrom(packageDir); err != nil || got != root {
		t.Fatalf("snapshot root=%q error=%v", got, err)
	}
	// A nested foreign module must not silently borrow the outer fixtures.
	if err := os.WriteFile(filepath.Join(packageDir, "go.mod"), []byte("module example.test/other\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := rootFrom(packageDir); err == nil {
		t.Fatal("foreign module accepted")
	}
}

func TestRootRejectsMissingModule(t *testing.T) {
	if _, err := rootFrom(t.TempDir()); err == nil {
		t.Fatal("missing module accepted")
	}
}
