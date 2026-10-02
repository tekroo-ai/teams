package agenttools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
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
	if len(read) != 4 || !slices.ContainsFunc(read, func(spec Spec) bool { return spec.Name == "git_status" }) {
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
	if err != nil || result.Output != "two\nthree" {
		t.Fatalf("bounded file read: %+v, %v", result, err)
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
	if err != nil || status.ExitCode != 0 || !strings.Contains(status.Output, "change.txt") || len(status.HEAD) != 40 || status.Branch == "" {
		t.Fatalf("status: %+v, %v", status, err)
	}
	diff, err := host.execute(context.Background(), authority, call("git_diff", `{"path":"change.txt"}`))
	if err != nil || diff.ExitCode != 0 || !strings.Contains(diff.Output, "+after") {
		t.Fatalf("diff: %+v, %v", diff, err)
	}
	_, err = host.execute(context.Background(), authority, call("git_diff", `{"path":"../elsewhere"}`))
	if !errors.Is(err, ErrBoundary) {
		t.Fatalf("out-of-workspace diff: %v", err)
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

func TestGoTestsAreTypedAndBounded(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/agenttools\n\ngo 1.26.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sample_test.go"), []byte("package sample\nimport \"testing\"\nfunc TestSample(t *testing.T) {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	authority := Authority{WorkspaceRoot: root, Permissions: []string{"repository.read", "test.execute"}}
	host := testHost()
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
