package agenttools

import (
	"context"
	"errors"
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
	var first map[string]int64
	for i := 0; i < 2; i++ {
		result, err := host.runIsolatedGoTests(t.Context(), root, "./...")
		if err != nil || result.ExitCode != 0 || !strings.Contains(result.Output, "example.test/cached") {
			t.Fatalf("cache seed run %d: %+v %v", i, result, err)
		}
		current := compilationCacheFiles(t, cache)
		var dataBytes int64
		var dataFiles int
		for name, size := range current {
			if strings.HasSuffix(name, "-d") {
				dataBytes += size
				dataFiles++
			}
		}
		t.Logf("snapshot %d: persistent output artifacts=%d bytes=%d", i+1, dataFiles, dataBytes)
		if i == 0 {
			first = current
		} else {
			for name, size := range current {
				if strings.HasSuffix(name, "-d") && first[name] != size {
					t.Fatalf("unchanged source in a fresh snapshot added cached output %s (%d bytes)", name, size)
				}
			}
		}
	}
	entries, err := os.ReadDir(cache)
	if err != nil || len(entries) != 1 {
		t.Fatalf("toolchain namespace: %v %v", entries, err)
	}
}

func compilationCacheFiles(t *testing.T, root string) map[string]int64 {
	t.Helper()
	files := make(map[string]int64)
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			files[path] = info.Size()
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func TestCompilationCacheRetentionBoundsBytesAndEntries(t *testing.T) {
	for _, test := range []struct {
		name    string
		bytes   int64
		entries int
	}{
		{"bytes", 8, 100}, {"entries", 100, 3}, {"exact-boundary", 12, 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			for i, name := range []string{"old", "middle", "new"} {
				writeSnapshotFixture(t, root, name, "data")
				at := time.Unix(int64(i+1), 0)
				if err := os.Chtimes(filepath.Join(root, name), at, at); err != nil {
					t.Fatal(err)
				}
			}
			if err := retainCompilationCache(t.Context(), root, test.bytes, test.entries); err != nil {
				t.Fatal(err)
			}
			files := compilationCacheFiles(t, root)
			want := 2
			if test.name == "exact-boundary" {
				want = 3
			}
			if len(files) != want {
				t.Fatalf("retained %d files, want %d", len(files), want)
			}
			if want == 2 {
				if _, err := os.Stat(filepath.Join(root, "old")); !os.IsNotExist(err) {
					t.Fatalf("oldest entry retained: %v", err)
				}
			}
			if err := retainCompilationCache(t.Context(), root, test.bytes, test.entries); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCompilationCacheRetentionRejectsSymlinksBeforeEviction(t *testing.T) {
	root := t.TempDir()
	writeSnapshotFixture(t, root, "old", "keep")
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := retainCompilationCache(t.Context(), root, 0, 1); !errors.Is(err, ErrBoundary) {
		t.Fatalf("symlink error: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "old")); err != nil || string(got) != "keep" {
		t.Fatalf("evicted before validation: %q %v", got, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := retainCompilationCache(ctx, root, 0, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestWorkspaceQuotaReportsBytesSeparatelyFromDiagnosticOutput(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "large-generated-artifact")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxTestWorkspaceBytes + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	err = boundedWorkspace(root)
	if !errors.Is(err, ErrTooLarge) || !strings.Contains(err.Error(), "workspace byte quota: 2147483649 exceeds 2147483648") {
		t.Fatalf("ambiguous quota: %v", err)
	}
}
