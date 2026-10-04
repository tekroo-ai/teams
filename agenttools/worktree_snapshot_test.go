package agenttools

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

func TestWorktreeGoTestsRedGreenBeforeSingleCommit(t *testing.T) {
	root := t.TempDir()
	writeSnapshotFixture(t, root, "go.mod", "module example.test/worktree\n\ngo 1.26.0\n")
	writeSnapshotFixture(t, root, "baseline.go", "package worktree\n")
	fixtureGit(t, root, "init", "-q")
	fixtureGit(t, root, "add", "go.mod", "baseline.go")
	fixtureGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "-qm", "baseline")
	baseline := strings.TrimSpace(fixtureGit(t, root, "rev-parse", "HEAD"))
	authority := Authority{WorkspaceRoot: root, Permissions: []string{"repository.read", "test.execute"}, Purpose: kernel.PurposeImplementation}
	host := Host{Timeout: 2 * time.Minute}
	writeSnapshotFixture(t, root, "value_test.go", "package worktree\nimport \"testing\"\nfunc TestValue(t *testing.T) { if Value() != 42 { t.Fatal(\"wrong value\") } }\n")
	red, err := host.execute(context.Background(), authority, call("run_go_tests_worktree", `{"package":"./..."}`))
	if err != nil || red.ExitCode == 0 || !validExpectedSHA(red.SnapshotSHA256) || red.CommitSHA != baseline {
		t.Fatalf("uncommitted test did not fail: %+v, %v", red, err)
	}
	writeSnapshotFixture(t, root, "value.go", "package worktree\nfunc Value() int { return 42 }\n")
	green, err := host.execute(context.Background(), authority, call("run_go_tests_worktree", `{"package":"./..."}`))
	if err != nil || green.ExitCode != 0 || !strings.Contains(green.Output, "ok") || green.SnapshotSHA256 == red.SnapshotSHA256 || green.CommitSHA != baseline {
		t.Fatalf("uncommitted fix did not pass: %+v, %v", green, err)
	}
	if head := strings.TrimSpace(fixtureGit(t, root, "rev-parse", "HEAD")); head != baseline {
		t.Fatalf("working-tree test changed HEAD: %s", head)
	}
	// Independent validation still ignores dirty files and uses committed HEAD.
	beforeCommit, err := host.execute(context.Background(), authority, call("run_go_tests", `{"package":"./..."}`))
	if err != nil || beforeCommit.ExitCode != 0 || beforeCommit.SnapshotSHA256 != "" || beforeCommit.CommitSHA != baseline {
		t.Fatalf("committed gate observed dirty files: %+v, %v", beforeCommit, err)
	}
	fixtureGit(t, root, "add", "value.go", "value_test.go")
	fixtureGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "-qm", "one candidate")
	if count := strings.TrimSpace(fixtureGit(t, root, "rev-list", "--count", "HEAD")); count != "2" {
		t.Fatalf("expected baseline plus one candidate commit, got %s", count)
	}
	validated, err := host.execute(context.Background(), Authority{WorkspaceRoot: root, Permissions: authority.Permissions, Purpose: kernel.PurposeValidation}, call("run_go_tests", `{"package":"./..."}`))
	if err != nil || validated.ExitCode != 0 || !strings.Contains(validated.Output, "ok") {
		t.Fatalf("committed candidate failed independent validation: %+v, %v", validated, err)
	}
}

func TestWorktreeGoTestsRejectValidationAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	writeSnapshotFixture(t, root, "go.mod", "module example.test/worktree\n\ngo 1.26.0\n")
	fixtureGit(t, root, "init", "-q")
	fixtureGit(t, root, "add", "go.mod")
	fixtureGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "-qm", "baseline")
	host := Host{Timeout: 2 * time.Minute}
	permissions := []string{"repository.read", "test.execute"}
	_, err := host.execute(context.Background(), Authority{WorkspaceRoot: root, Permissions: permissions, Purpose: kernel.PurposeValidation}, call("run_go_tests_worktree", `{"package":"./..."}`))
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("validator gained dirty-worktree execution: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside.go")
	writeSnapshotFixture(t, filepath.Dir(outside), "outside.go", "package worktree\n")
	if err := os.Symlink(outside, filepath.Join(root, "escape.go")); err != nil {
		t.Fatal(err)
	}
	_, err = host.execute(context.Background(), Authority{WorkspaceRoot: root, Permissions: permissions, Purpose: kernel.PurposeImplementation}, call("run_go_tests_worktree", `{"package":"./..."}`))
	if !errors.Is(err, ErrBoundary) {
		t.Fatalf("symlink escaped snapshot boundary: %v", err)
	}
}

func writeSnapshotFixture(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func fixtureGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return string(output)
}
