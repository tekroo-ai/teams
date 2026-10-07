package agenttools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCompilationCacheCopiesNotLinksAndRejectsSymlinks(t *testing.T) {
	source, destination := t.TempDir(), t.TempDir()
	writeSnapshotFixture(t, source, "artifact", "trusted compiler bytes")
	if err := copyCompilationCache(t.Context(), source, destination); err != nil {
		t.Fatal(err)
	}
	writeSnapshotFixture(t, destination, "artifact", "untrusted test bytes")
	got, err := os.ReadFile(filepath.Join(source, "artifact"))
	if err != nil || string(got) != "trusted compiler bytes" {
		t.Fatalf("shared writable inode: %q %v", got, err)
	}
	if err := os.Symlink(filepath.Join(source, "artifact"), filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}
	if err := copyCompilationCache(t.Context(), source, t.TempDir()); err == nil {
		t.Fatal("cache symlink accepted")
	}
}

func TestCompilationCacheSeedsFreshIsolatedGoTests(t *testing.T) {
	root := t.TempDir()
	writeSnapshotFixture(t, root, "go.mod", "module example.test/cached\n\ngo 1.26.0\n")
	writeSnapshotFixture(t, root, "value.go", "package cached\nconst Value = 1\n")
	writeSnapshotFixture(t, root, "value_test.go", "package cached\nimport \"testing\"\nfunc TestValue(t *testing.T) { if Value != 1 { t.Fatal(Value) } }\n")
	fixtureGit(t, root, "init", "-q")
	fixtureGit(t, root, "add", "--", ".")
	fixtureGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "baseline")
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	cache, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	host := Host{Timeout: time.Minute, CompilationCache: cache}
	for i := 0; i < 2; i++ {
		result, err := host.runIsolatedGoTests(t.Context(), root, "./...")
		if err != nil || result.ExitCode != 0 || !strings.Contains(result.Output, "example.test/cached") {
			t.Fatalf("cache seed run %d: %+v %v", i, result, err)
		}
	}
	entries, err := os.ReadDir(cache)
	if err != nil || len(entries) != 1 {
		t.Fatalf("toolchain namespace: %v %v", entries, err)
	}
}
