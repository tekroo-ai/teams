package agenttools

import (
	"os"
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
	host := Host{Timeout: 2 * time.Minute}
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
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	host := Host{Timeout: 2 * time.Minute}
	authority := Authority{WorkspaceRoot: root, Permissions: []string{"repository.read", "test.execute"}, Purpose: kernel.PurposeImplementation}
	result, err := host.execute(t.Context(), authority, call("run_go_tests_worktree", `{"package":"./organization"}`))
	if err != nil || result.ExitCode != 0 || !strings.Contains(result.Output, "ok") {
		t.Fatalf("real repository role-package checks: %+v %v", result, err)
	}
}
