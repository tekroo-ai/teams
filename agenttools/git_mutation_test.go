package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitStageAndCommitAreBoundedAndReplaySafe(t *testing.T) {
	root := t.TempDir()
	runGit := func(arguments ...string) string {
		t.Helper()
		command := exec.Command("git", arguments...)
		command.Dir = root
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", arguments, err, output)
		}
		return string(output)
	}
	runGit("init", "-q")
	if err := os.WriteFile(filepath.Join(root, "work.go"), []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGit("add", "--", "work.go")
	runGit("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "baseline")
	head := trimGitOutput(runGit("rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(root, "work.go"), []byte("after\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".openhands"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".openhands", "runtime"), []byte("hook"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	host := testHost()
	read := Authority{WorkspaceRoot: root, Permissions: []string{"repository.read"}}
	edit := Authority{WorkspaceRoot: root, Permissions: []string{"repository.edit"}}
	stageArguments, _ := json.Marshal(map[string]any{"paths": []string{"work.go"}, "expected_head": head})
	_, err := host.execute(ctx, read, Call{Name: "git_stage_files", Arguments: stageArguments})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("read authority staged files: %v", err)
	}
	for _, paths := range [][]string{{".git/config"}, {".openhands/runtime"}, {"../outside"}, {"work.go", "work.go"}} {
		arguments, _ := json.Marshal(map[string]any{"paths": paths, "expected_head": head})
		if _, err := host.execute(ctx, edit, Call{Name: "git_stage_files", Arguments: arguments}); err == nil {
			t.Fatalf("unsafe stage accepted: %v", paths)
		}
	}
	staged, err := host.execute(ctx, edit, Call{Name: "git_stage_files", Arguments: stageArguments})
	if err != nil || !validFullGitSHA(staged.IndexTree) {
		t.Fatalf("stage: %+v, %v", staged, err)
	}
	stagedAgain, err := host.execute(ctx, edit, Call{Name: "git_stage_files", Arguments: stageArguments})
	if err != nil || stagedAgain.IndexTree != staged.IndexTree {
		t.Fatalf("replayed stage: %+v, %v", stagedAgain, err)
	}
	commitArguments, _ := json.Marshal(map[string]any{"expected_head": head, "expected_index_tree": staged.IndexTree, "subject": "Implement change", "body": "Verified in test. "})
	_, err = host.execute(ctx, read, Call{Name: "git_commit", Arguments: commitArguments})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("read authority committed: %v", err)
	}
	committed, err := host.execute(ctx, edit, Call{Name: "git_commit", Arguments: commitArguments})
	if err != nil || !validFullGitSHA(committed.CommitSHA) || committed.CommitSHA == head {
		t.Fatalf("commit: %+v, %v", committed, err)
	}
	if got := trimGitOutput(runGit("rev-parse", "HEAD")); got != committed.CommitSHA {
		t.Fatalf("HEAD %s != %s", got, committed.CommitSHA)
	}
	if got := trimGitOutput(runGit("rev-parse", "HEAD^")); got != head {
		t.Fatalf("parent %s != %s", got, head)
	}
	if got := trimGitOutput(runGit("show", "--format=", "--name-only", "HEAD")); got != "work.go" {
		t.Fatalf("commit included unexpected files: %q", got)
	}
	replayed, err := host.execute(ctx, edit, Call{Name: "git_commit", Arguments: commitArguments})
	if err != nil || replayed.CommitSHA != committed.CommitSHA {
		t.Fatalf("replayed commit: %+v, %v", replayed, err)
	}
	if _, err := host.execute(ctx, edit, Call{Name: "git_stage_files", Arguments: stageArguments}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale HEAD stage: %v", err)
	}
	wrongMessage, _ := json.Marshal(map[string]any{"expected_head": head, "expected_index_tree": staged.IndexTree, "subject": "Different"})
	if _, err := host.execute(ctx, edit, Call{Name: "git_commit", Arguments: wrongMessage}); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed-message replay: %v", err)
	}
}

func trimGitOutput(output string) string {
	return strings.TrimSpace(output)
}
