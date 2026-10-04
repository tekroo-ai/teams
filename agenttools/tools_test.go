package agenttools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func call(name, arguments string) Call {
	return Call{Name: name, Arguments: json.RawMessage(arguments)}
}

func testHost() Host { return Host{Timeout: 30 * time.Second} }

func TestAvailableAndAuthorization(t *testing.T) {
	if got := Available(nil); len(got) != 0 {
		t.Fatalf("unprivileged tools: %v", got)
	}
	read := Available([]string{"repository.read"})
	if len(read) != 10 || !slices.ContainsFunc(read, func(spec Spec) bool { return spec.Name == "git_status" }) {
		t.Fatalf("read-only surface: %v", read)
	}
	for _, spec := range read {
		var schema map[string]any
		if err := json.Unmarshal(spec.InputSchema, &schema); err != nil || schema["additionalProperties"] != false {
			t.Fatalf("invalid schema for %s: %v", spec.Name, err)
		}
	}
	read[0].InputSchema[0] = 'x'
	if Available([]string{"repository.read"})[0].InputSchema[0] != '{' {
		t.Fatal("caller mutated the canonical tool schema")
	}
	root := t.TempDir()
	_, err := testHost().execute(context.Background(), Authority{WorkspaceRoot: root}, call("read_file", `{"path":"a.txt"}`))
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("unauthorized call: %v", err)
	}
	_, err = testHost().execute(context.Background(), Authority{WorkspaceRoot: root, Permissions: []string{"repository.read"}}, call("run_go_tests", `{"package":"./..."}`))
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("read role ran tests: %v", err)
	}
}

func TestCheckGoFormatReportsWithoutWriting(t *testing.T) {
	root := t.TempDir()
	formatted := []byte("package greeting\n\nfunc Greeting(name string) string { return \"Hello, \" + name + \"!\" }\n")
	if err := os.WriteFile(filepath.Join(root, "greeting.go"), formatted, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "rough.go"), []byte("package greeting\nfunc Rough(){println(\"x\")}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	authority := Authority{WorkspaceRoot: root, Permissions: []string{"repository.read"}}
	result, err := testHost().execute(context.Background(), authority, call("check_go_format", `{"paths":["greeting.go","rough.go"]}`))
	if err != nil || result.FormatClean == nil || *result.FormatClean || !slices.Equal(result.Files, []string{"rough.go"}) {
		t.Fatalf("format check: %+v, %v", result, err)
	}
	result, err = testHost().execute(context.Background(), authority, call("check_go_format", `{"paths":["greeting.go"]}`))
	if err != nil || result.FormatClean == nil || !*result.FormatClean {
		t.Fatalf("clean format check: %+v, %v", result, err)
	}
	content, err := os.ReadFile(filepath.Join(root, "rough.go"))
	if err != nil || string(content) != "package greeting\nfunc Rough(){println(\"x\")}\n" {
		t.Fatalf("format checker changed source: %q, %v", content, err)
	}
	for _, arguments := range []string{`{"paths":["../outside.go"]}`, `{"paths":["greeting.go","greeting.go"]}`, `{"paths":["greeting.txt"]}`} {
		if _, err := testHost().execute(context.Background(), authority, call("check_go_format", arguments)); err == nil {
			t.Fatalf("invalid format check accepted: %s", arguments)
		}
	}
}

func TestReadAndListStayInWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "source.txt"), []byte("one\ntwo\nthree\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	authority := Authority{WorkspaceRoot: root, Permissions: []string{"repository.read"}}
	host := testHost()
	result, err := host.execute(context.Background(), authority, call("read_file", `{"path":"source.txt","start_line":2,"end_line":3}`))
	fullFileHash := sha256.Sum256([]byte("one\ntwo\nthree\n"))
	if err != nil || result.Output != "two\nthree" || result.SHA256 != hex.EncodeToString(fullFileHash[:]) || result.LineCount == nil || *result.LineCount != 3 {
		t.Fatalf("bounded file read: %+v, %v", result, err)
	}
	result, err = host.execute(context.Background(), authority, call("read_file", `{"path":"source.txt","start_line":2,"end_line":4}`))
	if err != nil || result.Output != "two\nthree" || result.LineCount == nil || *result.LineCount != 3 {
		t.Fatalf("range past EOF should return available lines: %+v, %v", result, err)
	}
	result, err = host.execute(context.Background(), authority, call("read_file", `{"path":"source.txt","start_line":4,"end_line":5}`))
	if err != nil || result.Output != "" || result.LineCount == nil || *result.LineCount != 3 {
		t.Fatalf("range after EOF should report file length: %+v, %v", result, err)
	}
	result, err = host.execute(context.Background(), authority, call("list_files", `{}`))
	if err != nil || !slices.Contains(result.Files, "source.txt") {
		t.Fatalf("directory listing: %+v, %v", result, err)
	}
	for _, arguments := range []string{`{"path":"../secret.txt"}`, `{"path":"escape"}`, `{"path":"/etc/passwd"}`} {
		_, err := host.execute(context.Background(), authority, call("read_file", arguments))
		if !errors.Is(err, ErrBoundary) {
			t.Fatalf("read %s: expected boundary denial, got %v", arguments, err)
		}
	}
	_, err = host.execute(context.Background(), authority, call("read_file", `{"path":"source.txt","unexpected":true}`))
	if !errors.Is(err, ErrInvalidCall) {
		t.Fatalf("unknown argument was accepted: %v", err)
	}
	_, err = host.execute(context.Background(), authority, call("list_files", `null`))
	if !errors.Is(err, ErrInvalidCall) {
		t.Fatalf("null arguments were accepted: %v", err)
	}
	_, err = host.execute(context.Background(), authority, call("read_file", `{"path":"source.txt","start_line":3,"end_line":2}`))
	if !errors.Is(err, ErrInvalidCall) {
		t.Fatalf("inverted range was accepted: %v", err)
	}
	_, err = host.execute(context.Background(), authority, call("read_file", `{"path":"source.txt","start_line":0}`))
	if !errors.Is(err, ErrInvalidCall) {
		t.Fatalf("line number outside advertised schema was accepted: %v", err)
	}
}

func TestListFilesUsesBoundedPagesWithoutDroppingEntries(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	authority := Authority{WorkspaceRoot: root, Permissions: []string{"repository.read"}}
	first, err := testHost().execute(context.Background(), authority, call("list_files", `{"limit":2}`))
	if err != nil || !reflect.DeepEqual(first.Files, []string{"a.txt", "b.txt"}) || !first.Truncated || first.NextAfter != "b.txt" {
		t.Fatalf("first page: %+v, %v", first, err)
	}
	second, err := testHost().execute(context.Background(), authority, call("list_files", `{"start_after":"b.txt","limit":2}`))
	if err != nil || !reflect.DeepEqual(second.Files, []string{"c.txt"}) || second.Truncated || second.NextAfter != "" {
		t.Fatalf("second page: %+v, %v", second, err)
	}
	_, err = testHost().execute(context.Background(), authority, call("list_files", `{"limit":0}`))
	if !errors.Is(err, ErrInvalidCall) {
		t.Fatalf("zero limit accepted: %v", err)
	}
}

func TestListFilesCanTraverseDirectoryBeyondFormerTwoHundredEntryLimit(t *testing.T) {
	root := t.TempDir()
	for index := range 201 {
		name := fmt.Sprintf("file-%03d", index)
		if err := os.WriteFile(filepath.Join(root, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	authority := Authority{WorkspaceRoot: root, Permissions: []string{"repository.read"}}
	first, err := testHost().execute(context.Background(), authority, call("list_files", `{}`))
	if err != nil || len(first.Files) != 200 || !first.Truncated || first.NextAfter != "file-199" {
		t.Fatalf("first page: %+v, %v", first, err)
	}
	second, err := testHost().execute(context.Background(), authority, call("list_files", `{"start_after":"file-199"}`))
	if err != nil || !reflect.DeepEqual(second.Files, []string{"file-200"}) || second.Truncated {
		t.Fatalf("second page: %+v, %v", second, err)
	}
}

func TestGitInspectionUsesAssignedRepository(t *testing.T) {
	root := t.TempDir()
	command := exec.Command("git", "init", "-q")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	if err := os.WriteFile(filepath.Join(root, "change.txt"), []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	command = exec.Command("git", "add", "--", "change.txt")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, output)
	}
	command = exec.Command("git", "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "baseline")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, output)
	}
	if err := os.WriteFile(filepath.Join(root, "change.txt"), []byte("after\n"), 0600); err != nil {
		t.Fatal(err)
	}
	authority := Authority{WorkspaceRoot: root, Permissions: []string{"repository.read"}}
	host := testHost()
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), ".git"))
	status, err := host.execute(context.Background(), authority, call("git_status", `{}`))
	if err != nil || status.ExitCode != 0 || !strings.Contains(status.Output, "change.txt") || len(status.HEAD) != 40 || status.Branch == "" || status.Clean == nil || *status.Clean {
		t.Fatalf("status: %+v, %v", status, err)
	}
	diff, err := host.execute(context.Background(), authority, call("git_diff", `{"path":"change.txt"}`))
	if err != nil || diff.ExitCode != 0 || !strings.Contains(diff.Output, "+after") {
		t.Fatalf("diff: %+v, %v", diff, err)
	}
	log, err := host.execute(context.Background(), authority, call("git_log", `{"limit":1,"format":"message"}`))
	if err != nil || log.ExitCode != 0 || strings.TrimSpace(log.Output) != "baseline" {
		t.Fatalf("log: %+v, %v", log, err)
	}
	show, err := host.execute(context.Background(), authority, call("git_show", `{"commit":"HEAD","path":"change.txt"}`))
	if err != nil || show.ExitCode != 0 || !strings.Contains(show.Output, "+before") {
		t.Fatalf("show: %+v, %v", show, err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".openhands/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ignored, err := host.execute(context.Background(), authority, call("git_check_ignore", `{"path":".openhands/"}`))
	if err != nil || ignored.Ignored == nil || !*ignored.Ignored || ignored.ExitCode != 0 {
		t.Fatalf("ignored path: %+v, %v", ignored, err)
	}
	notIgnored, err := host.execute(context.Background(), authority, call("git_check_ignore", `{"path":"change.txt"}`))
	if err != nil || notIgnored.Ignored == nil || *notIgnored.Ignored || notIgnored.ExitCode != 0 {
		t.Fatalf("non-ignored path: %+v, %v", notIgnored, err)
	}
	for _, invalid := range []Call{
		call("git_diff", `{"from_commit":"--output=/tmp/out"}`),
		call("git_diff", `{"to_commit":"HEAD"}`),
		call("git_diff", `{"staged":true,"from_commit":"HEAD"}`),
		call("git_diff", `{"format":"unsafe"}`),
		call("git_log", `{"limit":21}`),
		call("git_show", `{"commit":"--exec=bad"}`),
		call("git_show", `{"commit":"HEAD","path":"../elsewhere"}`),
		call("git_check_ignore", `{"path":"../elsewhere"}`),
	} {
		if _, err := host.execute(context.Background(), authority, invalid); err == nil {
			t.Fatalf("invalid Git inspection accepted: %+v", invalid)
		}
	}
	_, err = host.execute(context.Background(), authority, call("git_diff", `{"path":"../elsewhere"}`))
	if !errors.Is(err, ErrBoundary) {
		t.Fatalf("out-of-workspace diff: %v", err)
	}
	command = exec.Command("git", "add", "--", "change.txt")
	command.Dir, command.Env = root, toolEnvironment()
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git add for staged inspection: %v: %s", err, output)
	}
	staged, err := host.execute(context.Background(), authority, call("git_diff", `{"staged":true,"format":"name_only"}`))
	if err != nil || staged.ExitCode != 0 || strings.TrimSpace(staged.Output) != "change.txt" {
		t.Fatalf("staged diff: %+v, %v", staged, err)
	}
	command = exec.Command("git", "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "successor")
	command.Dir, command.Env = root, toolEnvironment()
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("successor commit: %v: %s", err, output)
	}
	commits, err := host.execute(context.Background(), authority, call("git_log", `{"limit":2}`))
	if err != nil || commits.ExitCode != 0 || !strings.Contains(commits.Output, "baseline") || !strings.Contains(commits.Output, "successor") {
		t.Fatalf("two-commit log: %+v, %v", commits, err)
	}
	between, err := host.execute(context.Background(), authority, Call{Name: "git_diff", Arguments: json.RawMessage(fmt.Sprintf(`{"from_commit":%q,"to_commit":"HEAD","format":"stat"}`, status.HEAD))})
	if err != nil || between.ExitCode != 0 || !strings.Contains(between.Output, "change.txt") {
		t.Fatalf("commit-to-commit diff: %+v, %v", between, err)
	}
}

func TestGitStatusStillWorksBeforeFirstCommit(t *testing.T) {
	root := t.TempDir()
	command := exec.Command("git", "init", "-q")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	authority := Authority{WorkspaceRoot: root, Permissions: []string{"repository.read"}}
	status, err := testHost().execute(context.Background(), authority, call("git_status", `{}`))
	if err != nil || status.ExitCode != 0 || status.HEAD != "UNBORN" || status.Branch == "" {
		t.Fatalf("unborn Git status: %+v, %v", status, err)
	}
}

func TestGitStatusExplicitlyReportsCleanWorktree(t *testing.T) {
	root := t.TempDir()
	command := exec.Command("git", "init", "-q")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("baseline\n"), 0600); err != nil {
		t.Fatal(err)
	}
	command = exec.Command("git", "add", "tracked.txt")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, output)
	}
	command = exec.Command("git", "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "baseline")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, output)
	}
	authority := Authority{WorkspaceRoot: root, Permissions: []string{"repository.read"}}
	status, err := testHost().execute(context.Background(), authority, call("git_status", `{}`))
	if err != nil || status.Clean == nil || !*status.Clean {
		t.Fatalf("clean status: %+v, %v", status, err)
	}
	if err := os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("new\n"), 0600); err != nil {
		t.Fatal(err)
	}
	status, err = testHost().execute(context.Background(), authority, call("git_status", `{}`))
	if err != nil || status.Clean == nil || *status.Clean {
		t.Fatalf("untracked file not reported dirty: %+v, %v", status, err)
	}
}

func TestGoTestsAreTypedAndBounded(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/agenttools\n\ngo 1.26.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sample_test.go"), []byte("package sample\nimport \"testing\"\nfunc TestSample(t *testing.T) {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, arguments := range [][]string{{"init", "-q"}, {"add", "go.mod", "sample_test.go"}, {"-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "-qm", "test fixture"}} {
		command := exec.Command("git", arguments...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", arguments, err, output)
		}
	}
	// A later uncommitted edit must not change what the test runner executes.
	if err := os.WriteFile(filepath.Join(root, "sample_test.go"), []byte("not valid Go"), 0600); err != nil {
		t.Fatal(err)
	}
	authority := Authority{WorkspaceRoot: root, Permissions: []string{"repository.read", "test.execute"}}
	host := Host{Timeout: 2 * time.Minute}
	result, err := host.execute(context.Background(), authority, call("run_go_tests", `{"package":"./..."}`))
	if err != nil || result.ExitCode != 0 || !strings.Contains(result.Output, "ok") {
		t.Fatalf("go test: %+v, %v", result, err)
	}
	for _, arguments := range []string{`{"package":"./../other"}`, `{"package":"./...;echo bad"}`, `{"package":"./...","flags":["-exec","bad"]}`} {
		_, err := host.execute(context.Background(), authority, call("run_go_tests", arguments))
		if !errors.Is(err, ErrInvalidCall) {
			t.Fatalf("unsafe package %s: %v", arguments, err)
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = host.execute(cancelled, authority, call("run_go_tests", `{"package":"./..."}`))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled test execution: %v", err)
	}
}

func TestGoTestsCannotReadWriteOutsideSnapshotOrConnect(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret")
	if err := os.WriteFile(secret, []byte("hidden"), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	source := "package sample\nimport (\"net\"; \"os\"; \"os/exec\"; \"testing\"; \"time\")\n" +
		"func TestBoundary(t *testing.T) {\n" +
		" if _, err := os.ReadFile(" + strconv.Quote(secret) + "); err == nil { t.Error(\"outside read succeeded\") }\n" +
		" if err := os.Symlink(" + strconv.Quote(secret) + ", \"escaped-link\"); err == nil { if _, readErr := os.ReadFile(\"escaped-link\"); readErr == nil { t.Error(\"symlink escaped\") } }\n" +
		" if err := os.WriteFile(" + strconv.Quote(filepath.Join(outside, "write")) + ", []byte(\"bad\"), 0600); err == nil { t.Error(\"outside write succeeded\") }\n" +
		" if conn, err := net.DialTimeout(\"tcp\", " + strconv.Quote(listener.Addr().String()) + ", time.Second); err == nil { conn.Close(); t.Error(\"network succeeded\") }\n" +
		" if err := exec.Command(\"/usr/bin/true\").Run(); err == nil { t.Error(\"system executable succeeded\") }\n" +
		" if sink, err := os.OpenFile(\"/dev/null\", os.O_WRONLY, 0); err != nil { t.Errorf(\"/dev/null unavailable: %v\", err) } else { sink.Close() }\n" +
		"}\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/boundary\n\ngo 1.26.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "boundary_test.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	for _, arguments := range [][]string{{"init", "-q"}, {"add", "go.mod", "boundary_test.go"}, {"-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "-qm", "boundary fixture"}} {
		command := exec.Command("git", arguments...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", arguments, err, output)
		}
	}
	host := Host{Timeout: 2 * time.Minute}
	result, err := host.execute(context.Background(), Authority{WorkspaceRoot: root, Permissions: []string{"repository.read", "test.execute"}}, call("run_go_tests", `{"package":"./..."}`))
	if err != nil || result.ExitCode != 0 || !strings.Contains(result.Output, "ok") {
		t.Fatalf("sandbox boundary: %+v, %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(outside, "write")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside file was created: %v", err)
	}
}

func TestGoTestsStopAtDeadline(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/deadline\n\ngo 1.26.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "slow_test.go"), []byte("package slow\nimport (\"testing\"; \"time\")\nfunc TestSlow(t *testing.T) { time.Sleep(time.Minute) }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, arguments := range [][]string{{"init", "-q"}, {"add", "go.mod", "slow_test.go"}, {"-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "-qm", "deadline fixture"}} {
		command := exec.Command("git", arguments...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", arguments, err, output)
		}
	}
	host := Host{Timeout: 5 * time.Second}
	started := time.Now()
	_, err := host.execute(context.Background(), Authority{WorkspaceRoot: root, Permissions: []string{"repository.read", "test.execute"}}, call("run_go_tests", `{"package":"./..."}`))
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 15*time.Second {
		t.Fatalf("test did not stop at deadline after %s: %v", time.Since(started), err)
	}
}

func TestGoTestWorkspaceBudget(t *testing.T) {
	root := t.TempDir()
	file, err := os.Create(filepath.Join(root, "sparse"))
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxTestWorkspaceBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := boundedWorkspace(root); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized workspace: %v", err)
	}
}

func TestGoPackageSchemaMatchesHostValidation(t *testing.T) {
	var schema struct {
		Properties map[string]struct {
			Pattern string `json:"pattern"`
		} `json:"properties"`
	}
	for _, spec := range Available([]string{"repository.read", "test.execute"}) {
		if spec.Name == "run_go_tests" {
			if err := json.Unmarshal(spec.InputSchema, &schema); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	pattern, err := regexp.Compile(schema.Properties["package"].Pattern)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{"./", "./...", "./foo", "./foo/bar", "./foo-bar"} {
		if !pattern.MatchString(candidate) || !validGoPackage(candidate) {
			t.Fatalf("supported package %q rejected", candidate)
		}
	}
	for _, candidate := range []string{".", "./.", "./..", "./../other", "./foo/", "./.hidden", "./foo;echo"} {
		if pattern.MatchString(candidate) || validGoPackage(candidate) {
			t.Fatalf("unsupported package %q accepted", candidate)
		}
	}
}

func TestWriteFileRequiresEditPermissionAndExactPrecondition(t *testing.T) {
	root := t.TempDir()
	host := testHost()
	readOnly := Authority{WorkspaceRoot: root, Permissions: []string{"repository.read"}}
	create := call("write_file", `{"path":"note.txt","content":"first\n","expected_sha256":""}`)
	if _, err := host.execute(context.Background(), readOnly, create); !errors.Is(err, ErrForbidden) {
		t.Fatalf("read-only actor wrote a file: %v", err)
	}
	writer := Authority{WorkspaceRoot: root, Permissions: []string{"repository.edit"}}
	result, err := host.execute(context.Background(), writer, create)
	firstDigest := sha256.Sum256([]byte("first\n"))
	if err != nil || result.SHA256 != hex.EncodeToString(firstDigest[:]) {
		t.Fatalf("create: %+v, %v", result, err)
	}
	if _, err := host.execute(context.Background(), writer, create); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale create was accepted: %v", err)
	}
	replace := call("write_file", `{"path":"note.txt","content":"second\n","expected_sha256":"`+result.SHA256+`"}`)
	if _, err := host.execute(context.Background(), writer, replace); err != nil {
		t.Fatalf("replace: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(root, "note.txt"))
	if err != nil || string(content) != "second\n" {
		t.Fatalf("written content: %q, %v", content, err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	for _, arguments := range []string{
		`{"path":"linked/escape.txt","content":"bad","expected_sha256":""}`,
		`{"path":"../escape.txt","content":"bad","expected_sha256":""}`,
	} {
		if _, err := host.execute(context.Background(), writer, call("write_file", arguments)); !errors.Is(err, ErrBoundary) {
			t.Fatalf("out-of-workspace write %s: %v", arguments, err)
		}
	}
}
