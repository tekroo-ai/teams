package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	original, err := os.Stat(filepath.Join(source, "artifact"))
	if err != nil {
		t.Fatal(err)
	}
	private, err := os.Stat(filepath.Join(destination, "artifact"))
	if err != nil || os.SameFile(original, private) {
		t.Fatalf("cache copy shares inode: %v", err)
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
		raw, err := json.Marshal(result.VerificationTimings)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("snapshot %d raw phase timing receipt: %s", i+1, raw)
		if result.VerificationTimings.TotalNS < result.VerificationTimings.ExecutionNS+result.VerificationTimings.CleanupNS {
			t.Fatal("execution and cleanup exceed total wall time")
		}
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
			if result.VerificationTimings.CacheCompileNS != 0 {
				t.Fatal("unchanged inputs repeated the compiler primer")
			}
			for name, size := range current {
				if strings.HasSuffix(name, "-d") && first[name] != size {
					t.Fatalf("unchanged source added cached output %s (%d bytes)", name, size)
				}
			}
		}
	}
	entries, err := os.ReadDir(cache)
	if err != nil || len(entries) != 1 {
		t.Fatalf("toolchain namespace: %v %v", entries, err)
	}
	// The hint is not proof that artifacts still exist. Eviction must remain a
	// safe miss: rebuild compiler artifacts, then actually execute the test.
	if err := os.RemoveAll(filepath.Join(cache, entries[0].Name())); err != nil {
		t.Fatal(err)
	}
	result, err := host.runIsolatedGoTests(t.Context(), root, "./...")
	if err != nil || result.ExitCode != 0 || result.VerificationTimings.CacheCompileNS <= 0 {
		t.Fatalf("evicted primer did not fall back to real compilation/execution: %+v %v", result, err)
	}
	writeSnapshotFixture(t, root, "value_test.go", "package cached\nimport \"testing\"\nfunc TestValue(t *testing.T) { t.Fatal(\"changed source really executed\") }\n")
	fixtureGit(t, root, "add", "--", ".")
	fixtureGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "changed test")
	for i := 0; i < 2; i++ {
		result, err := host.runIsolatedGoTests(t.Context(), root, "./...")
		if err != nil || result.ExitCode != 1 || !strings.Contains(result.Output, "changed source really executed") {
			t.Fatalf("fresh failing test replaced by prior PASS: %+v %v", result, err)
		}
		if (result.VerificationTimings.CacheCompileNS > 0) != (i == 0) {
			t.Fatalf("changed source primer/repeated execution mismatch: %+v", result.VerificationTimings)
		}
	}
	writeSnapshotFixture(t, root, "value.go", "package cached\nnot valid Go source\n")
	fixtureGit(t, root, "add", "--", ".")
	fixtureGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "invalid source")
	for i := 0; i < 2; i++ {
		result, err := host.runIsolatedGoTests(t.Context(), root, "./...")
		if err != nil || result.ExitCode != 1 || result.VerificationTimings.CacheCompileNS <= 0 {
			t.Fatalf("failed compilation memoized as a successful primer: %+v %v", result, err)
		}
	}
}

func TestCompilationPrimerBuildsArchivesWithoutLinking(t *testing.T) {
	want := []string{"/opt/bin/go", "list", "-p=2", "-export", "-test", "-deps", "-f={{.ImportPath}}", "./..."}
	if args := compilationPrimerArguments("/opt/bin/go", "./..."); !slices.Equal(args, want) {
		t.Fatalf("archive-only compilation changed: %v", args)
	}
}

func TestCompilationCacheBuildThenTestReusesPrimerButStillExecutes(t *testing.T) {
	root, _ := localTestFixture(t, "package localexecution\nimport \"testing\"\nfunc TestFixture(t *testing.T) { t.Fatal(\"TEST_ACTUALLY_EXECUTED\") }\n")
	host := cachedFixtureHost(t, Host{Timeout: time.Minute})
	build, err := host.runIsolatedGoTestsFrom(t.Context(), root, "./...", true, true)
	if err != nil || build.ExitCode != 0 || build.VerificationTimings.CacheCompileNS == 0 {
		t.Fatalf("build check: %+v %v", build, err)
	}
	execution, err := host.runIsolatedGoTestsFrom(t.Context(), root, "./...", true, false)
	if err != nil || execution.ExitCode != 1 || !strings.Contains(execution.Output, "TEST_ACTUALLY_EXECUTED") {
		t.Fatalf("build result replaced real test execution: %+v %v", execution, err)
	}
	if execution.SnapshotSHA256 != build.SnapshotSHA256 || execution.VerificationTimings.CacheCompileNS != 0 {
		t.Fatalf("unchanged build/test source repeated preparation: %+v", execution)
	}
}

func TestCompilationCacheUntrimmedBuildKeepsLocalArtifactsStable(t *testing.T) {
	root, _ := localTestFixture(t, "package localexecution\nimport \"testing\"\nfunc init() { panic(\"compile-only must never execute\") }\nfunc TestFixture(t *testing.T) {}\n")
	host := cachedFixtureHost(t, Host{Timeout: time.Minute})
	var first map[string]int64
	for i := 0; i < 2; i++ {
		result, err := host.runIsolatedGoTestsFrom(t.Context(), root, "./...", false, true)
		if err != nil || result.ExitCode != 0 {
			t.Fatalf("compile-only cache run %d: %+v %v", i, result, err)
		}
		current := compilationCacheFiles(t, host.CompilationCache)
		if i == 0 {
			first = current
			continue
		}
		for name, size := range current {
			if strings.HasSuffix(name, "-d") && first[name] != size {
				t.Fatalf("unchanged stable-path snapshot added cached output %s (%d bytes)", name, size)
			}
		}
	}
}

func TestCompilationCacheActuallyAvoidsRecompilationWithoutSkippingTests(t *testing.T) {
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	goBinary, err = filepath.EvalSymlinks(goBinary)
	if err != nil {
		t.Fatal(err)
	}
	modCache := filepath.Join(os.Getenv("HOME"), "go", "pkg", "mod")
	if resolved, err := filepath.EvalSymlinks(modCache); err == nil {
		modCache = resolved
	}
	for _, test := range []struct {
		name        string
		seedTrimmed bool
		wantCompile bool
	}{
		{"old-incompatible-seed", true, true},
		{"matching-seed", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			workspace, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			for _, dir := range []string{"source", "home", "tmp", "cache", "gopath"} {
				if err := os.Mkdir(filepath.Join(workspace, dir), 0700); err != nil {
					t.Fatal(err)
				}
			}
			source := filepath.Join(workspace, "source")
			writeSnapshotFixture(t, source, "go.mod", "module example.test/cachehit\n\ngo 1.26.0\n")
			writeSnapshotFixture(t, source, "hit_test.go", `package cachehit
import ("fmt"; "path/filepath"; "runtime"; "testing")
func TestExecuted(t *testing.T) {
 _, file, _, _ := runtime.Caller(0)
 if !filepath.IsAbs(file) { t.Fatalf("lost fixture path: %q", file) }
 fmt.Println("TEST_ACTUALLY_EXECUTED")
}
`)
			profile, err := goTestSandboxProfile(workspace, goBinary, modCache)
			if err != nil {
				t.Fatal(err)
			}
			environment := goTestEnvironment(workspace, "/usr/bin/git", modCache)
			timings := &VerificationTimings{}
			host := cachedFixtureHost(t, Host{Timeout: time.Minute})
			seedEnvironment := environment
			if test.seedTrimmed {
				// Retain the old flag mismatch as a counterexample; the production
				// primer and consumer now deliberately use untrimmed archives.
				seedEnvironment = replaceEnvironment(environment, "GOFLAGS", "-trimpath")
			}
			if err := host.seedCompilationCache(t.Context(), workspace, source, goBinary, profile, seedEnvironment, ".", "", timings); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), "/usr/bin/sandbox-exec", "-p", profile, goBinary, "test", "-p=1", "-parallel=2", "-count=1", "-x", "-v", ".")
			cmd.Dir, cmd.Env = source, environment
			started := time.Now()
			output, err := cmd.CombinedOutput()
			execution := time.Since(started)
			if err != nil || !strings.Contains(string(output), "TEST_ACTUALLY_EXECUTED") {
				t.Fatalf("test did not execute: %s %v", output, err)
			}
			// go test regenerates its tiny _testmain wrapper with execution-
			// specific debug flags. Only that wrapper may compile on a hit;
			// recompiling fixture packages or the toolchain is the regression.
			compiled := false
			compileCount := 0
			for _, line := range strings.Split(string(output), "\n") {
				if strings.Contains(line, "/compile -o ") && !strings.Contains(line, "_testmain.go") {
					compiled = true
					compileCount++
				}
			}
			if compiled != test.wantCompile {
				t.Fatalf("compiler cache regression (compiled=%t): %s", compiled, output)
			}
			t.Logf("raw command: go test -p=1 -parallel=2 -count=1 -x -v .; observed package compiler processes=%d; seed_ns=%d execution_ns=%d", compileCount, timings.CacheCompileNS, execution.Nanoseconds())
			if !test.wantCompile {
				// Repeating identical inputs reuses compiler work, not PASS. A
				// second real test execution must still emit its own marker.
				cmd := exec.CommandContext(t.Context(), "/usr/bin/sandbox-exec", "-p", profile, goBinary, "test", "-p=1", "-parallel=2", "-count=1", "-v", ".")
				cmd.Dir, cmd.Env = source, environment
				output, err := cmd.CombinedOutput()
				if err != nil || !strings.Contains(string(output), "TEST_ACTUALLY_EXECUTED") || strings.Contains(string(output), "(cached)") {
					t.Fatalf("PASS replaced fresh execution: %s %v", output, err)
				}
			}
		})
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
