package agenttools

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func localTestFixture(t *testing.T, source string) (string, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeSnapshotFixture(t, root, "go.mod", "module example.test/localexecution\n\ngo 1.26.0\n")
	writeSnapshotFixture(t, root, "fixture.txt", "committed fixture\n")
	writeSnapshotFixture(t, root, "local_test.go", source)
	fixtureGit(t, root, "init", "-q")
	fixtureGit(t, root, "add", "--", ".")
	fixtureGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "baseline")
	return root, strings.TrimSpace(fixtureGit(t, root, "rev-parse", "HEAD"))
}

func TestLocalTestExecutionPreservesSourcePathsHelpersAndCommittedReceipt(t *testing.T) {
	root, head := localTestFixture(t, `package localexecution
import ("os"; "os/exec"; "path/filepath"; "runtime"; "strings"; "testing")
func TestFixture(t *testing.T) {
 _, file, _, ok := runtime.Caller(0)
 if !ok || !filepath.IsAbs(file) { t.Fatalf("source path was trimmed: %q", file) }
 data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "fixture.txt"))
 if err != nil || string(data) != "committed fixture\n" { t.Fatalf("fixture: %q %v", data, err) }
 if err := exec.Command("/bin/sleep", "0").Run(); err != nil { t.Fatalf("helper unavailable: %v", err) }
 if info, err := os.Stat(".git"); err != nil || !info.IsDir() { t.Fatalf("independent Git metadata missing: %v", err) }
 if _, err := os.Stat(".git/objects/info/alternates"); !os.IsNotExist(err) { t.Fatalf("shared candidate objects: %v", err) }
 if output, err := exec.Command("git", "status", "--porcelain").Output(); err != nil || strings.TrimSpace(string(output)) != "" { t.Fatalf("snapshot is not a clean checkout: %q %v", output, err) }
}
`)
	writeSnapshotFixture(t, root, "local_test.go", "invalid uncommitted source")
	host := cachedFixtureHost(t, Host{Timeout: 2 * time.Minute, LocalTestExecution: true})
	result, err := host.runIsolatedGoTests(t.Context(), root, "./...")
	if err != nil || result.ExitCode != 0 || result.CommitSHA != head || !validExpectedSHA(result.SHA256) || !strings.Contains(result.Output, "ok") {
		t.Fatalf("local committed execution: %+v %v", result, err)
	}
	if actual := strings.TrimSpace(fixtureGit(t, root, "rev-parse", "HEAD")); actual != head {
		t.Fatalf("candidate HEAD changed: %s", actual)
	}
}

func TestLocalTestExecutionStillStopsAtDeadline(t *testing.T) {
	root, _ := localTestFixture(t, "package localexecution\nimport (\"testing\"; \"time\")\nfunc TestSlow(t *testing.T) { time.Sleep(time.Minute) }\n")
	host := Host{Timeout: 5 * time.Second, LocalTestExecution: true}
	started := time.Now()
	_, err := host.runIsolatedGoTests(t.Context(), root, "./...")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 15*time.Second {
		t.Fatalf("local execution deadline after %s: %v", time.Since(started), err)
	}
}

func TestLocalTestExecutionDoesNotMakeCompileCheckRunInitializers(t *testing.T) {
	root, _ := localTestFixture(t, "package localexecution\nimport \"testing\"\nfunc init() { panic(\"must not execute\") }\nfunc TestFixture(t *testing.T) {}\n")
	host := cachedFixtureHost(t, Host{Timeout: 2 * time.Minute, LocalTestExecution: true})
	result, err := host.runIsolatedGoTestsFrom(t.Context(), root, "./...", false, true)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("compile-only ran initializer: %+v %v", result, err)
	}
}
