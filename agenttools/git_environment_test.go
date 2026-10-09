package agenttools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGitCommandSeparatesDiagnosticsFromMachineOutput(t *testing.T) {
	t.Setenv("GIT_ATTR_NOSYSTEM", "0")
	root := t.TempDir()
	binary := filepath.Join(root, "git-fixture")
	// A successful command may emit warnings. They must remain observable but
	// must never become part of a machine-readable hash or clean-worktree test.
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n[ \"$GIT_ATTR_NOSYSTEM\" = 1 ] || exit 9\nprintf '0123456789012345678901234567890123456789\\n'\nprintf 'warning: fixture diagnostic\\n' >&2\n"), 0700); err != nil {
		t.Fatal(err)
	}
	host := Host{Timeout: 10 * time.Second}
	result, err := host.command(t.Context(), root, "git_head", binary, nil)
	if err != nil || result.ExitCode != 0 || !validFullGitSHA(strings.TrimSpace(result.Output)) || result.Stderr != "warning: fixture diagnostic\n" {
		t.Fatalf("Git stdout/stderr: %+v %v", result, err)
	}
}

func TestGitStatusRemainsMachineReadableInSandbox(t *testing.T) {
	// Raw r55 receipt f22c7cb1:24 had a successful Git command whose merged
	// stderr contaminated its hash/status output with /etc/gitattributes EPERM.
	t.Setenv("GIT_ATTR_NOSYSTEM", "0")
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, workspace, "init", "-q")
	writeSnapshotFixture(t, workspace, "fixture.txt", "fixture\n")
	fixtureGit(t, workspace, "add", "--", "fixture.txt")
	fixtureGit(t, workspace, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "fixture")
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	goBinary, err = filepath.EvalSymlinks(goBinary)
	if err != nil {
		t.Fatal(err)
	}
	modCache, err := exec.Command(goBinary, "env", "GOMODCACHE").Output()
	if err != nil {
		t.Fatal(err)
	}
	git, err := exec.Command("/usr/bin/xcrun", "--find", "git").Output()
	if err != nil {
		t.Fatal(err)
	}
	gitBinary := strings.TrimSpace(string(git))
	profile, err := goTestSandboxProfileWithGit(workspace, goBinary, strings.TrimSpace(string(modCache)), gitBinary)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"status", "--porcelain=v1"}, {"write-tree"}} {
		command := exec.CommandContext(t.Context(), "/usr/bin/sandbox-exec", append([]string{"-p", profile, gitBinary}, args...)...)
		command.Dir = workspace
		command.Env = append(toolEnvironment(), "HOME="+workspace)
		output, err := command.CombinedOutput()
		if err != nil || strings.Contains(string(output), "warning:") {
			t.Fatalf("machine-readable Git %v: %q %v", args, output, err)
		}
		if args[0] == "status" && len(output) != 0 || args[0] == "write-tree" && !validFullGitSHA(strings.TrimSpace(string(output))) {
			t.Fatalf("unexpected Git %v output: %q", args, output)
		}
	}
}
