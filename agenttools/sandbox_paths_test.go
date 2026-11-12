package agenttools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

func TestSandboxAllowsCanonicalSnapshotPathsWithoutParentDirectoryReads(t *testing.T) {
	root := t.TempDir()
	writeSnapshotFixture(t, root, "go.mod", "module example.test/paths\n\ngo 1.26.0\n")
	if err := os.Mkdir(filepath.Join(root, "data"), 0700); err != nil {
		t.Fatal(err)
	}
	writeSnapshotFixture(t, root, "data/value.txt", "fixture")
	writeSnapshotFixture(t, root, "paths_test.go", `package paths
import ("os"; "path/filepath"; "testing")
func TestCanonicalPath(t *testing.T) {
    wd, err := os.Getwd()
    if err != nil { t.Fatal(err) }
    path, err := filepath.EvalSymlinks(filepath.Join(wd, "data", "value.txt"))
    if err != nil { t.Fatal(err) }
    data, err := os.ReadFile(path)
    if err != nil || string(data) != "fixture" { t.Fatalf("fixture read: %s %v", data, err) }
    // Traversal metadata is necessary, parent directory contents are not.
    if _, err := os.ReadDir(filepath.Dir(filepath.Dir(wd))); err == nil {
        t.Fatal("sandbox exposed parent directory contents")
    }
}
`)
	fixtureGit(t, root, "init", "-q")
	fixtureGit(t, root, "add", "--", "go.mod", "paths_test.go", "data/value.txt")
	fixtureGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "baseline")
	host := cachedFixtureHost(t, Host{Timeout: 2 * time.Minute})
	authority := Authority{WorkspaceRoot: root, Permissions: []string{"repository.read", "test.execute"}, Purpose: kernel.PurposeImplementation}
	for _, tool := range []string{"run_go_tests", "run_go_tests_worktree"} {
		t.Run(tool, func(t *testing.T) {
			result, err := host.execute(t.Context(), authority, call(tool, `{"package":"./..."}`))
			if err != nil || result.ExitCode != 0 || !strings.Contains(result.Output, "ok") {
				t.Fatalf("canonical snapshot path: %+v %v", result, err)
			}
		})
	}
}

func TestSandboxRunsRepositoryOrganizationRolePackageChecks(t *testing.T) {
	root := cleanRepositoryFixture(t)
	host := cachedFixtureHost(t, Host{Timeout: 2 * time.Minute})
	authority := Authority{WorkspaceRoot: root, Permissions: []string{"repository.read", "test.execute"}, Purpose: kernel.PurposeImplementation}
	result, err := host.execute(t.Context(), authority, call("run_go_tests_worktree", `{"package":"./organization"}`))
	if err != nil || result.ExitCode != 0 || !strings.Contains(result.Output, "ok") {
		t.Fatalf("real repository role-package checks: %+v %v", result, err)
	}
}

func TestSandboxLoadsFrozenKernelFixturesWithTrimpath(t *testing.T) {
	// r55's deployed -trimpath runner failed the accepted catalogue tests even
	// though the archived CONTRACTS files were present. Exercise the real corpus
	// through the unchanged sandbox, not just a mocked path resolver.
	root := cleanRepositoryFixture(t)
	host := cachedFixtureHost(t, Host{Timeout: 2 * time.Minute})
	authority := Authority{WorkspaceRoot: root, Permissions: []string{"repository.read", "test.execute"}, Purpose: kernel.PurposeImplementation}
	result, err := host.execute(t.Context(), authority, call("run_go_tests_worktree", `{"package":"./kernel"}`))
	if err != nil || result.ExitCode != 0 || result.PermissionDenial || !strings.Contains(result.Output, "ok") {
		t.Fatalf("frozen kernel fixtures in trimmed snapshot: %+v %v", result, err)
	}
}

// Keep real-corpus checks independent of private runtime and scratch files.
func cleanRepositoryFixture(t *testing.T) string {
	t.Helper()
	source, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "source")
	command := exec.CommandContext(t.Context(), "git", "clone", "--no-hardlinks", "--no-local", source, root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("clone tracked test corpus: %v %s", err, output)
	}
	return root
}
