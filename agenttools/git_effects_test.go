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
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

func TestGitEffectsReconcileLostReceiptsWithoutDuplicateCommit(t *testing.T) {
	root := t.TempDir()
	git := func(arguments ...string) string {
		t.Helper()
		command := exec.Command("git", arguments...)
		command.Dir = root
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", arguments, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(root, "work.go"), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "--", "work.go")
	git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "baseline")
	head := git("rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(root, "work.go"), []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	ledger := &memoryEffectLedger{failComplete: true}
	gateway := MutationGateway{
		Bindings: effectBinding{authority: Authority{WorkspaceRoot: root, Permissions: []string{"repository.edit"}, Purpose: kernel.PurposeImplementation, EffectPolicyDigest: policy}},
		Host:     testHost(), Ledger: ledger, Policy: policy,
	}
	stageArgs, _ := json.Marshal(map[string]any{"paths": []string{"work.go"}, "expected_head": head})
	stage := Request{InvocationID: kernel.UUIDv7("00000000-0000-7000-8000-000000000983"), RequestDigest: policy,
		ToolCallID: "stage-1", Call: Call{Name: "git_stage_files", Arguments: stageArgs}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := gateway.Execute(ctx, stage); !errors.Is(err, ErrEffectUncertain) {
		t.Fatalf("lost stage receipt = %v", err)
	}
	staged, applied, err := gateway.Reconcile(ctx, stage)
	if err != nil || !applied || !validFullGitSHA(staged.Result.IndexTree) {
		t.Fatalf("stage reconciliation = %+v applied=%t err=%v", staged, applied, err)
	}
	commitArgs, _ := json.Marshal(map[string]any{"expected_head": head, "expected_index_tree": staged.Result.IndexTree, "subject": "Implement change"})
	commit := Request{InvocationID: stage.InvocationID, RequestDigest: stage.RequestDigest,
		ToolCallID: "commit-1", Call: Call{Name: "git_commit", Arguments: commitArgs}}
	ledger.failComplete = true
	if _, err := gateway.Execute(ctx, commit); !errors.Is(err, ErrEffectUncertain) {
		t.Fatalf("lost commit receipt = %v", err)
	}
	committed, applied, err := gateway.Reconcile(ctx, commit)
	if err != nil || !applied || !validFullGitSHA(committed.Result.CommitSHA) || git("rev-parse", "HEAD") != committed.Result.CommitSHA {
		t.Fatalf("commit reconciliation = %+v applied=%t err=%v", committed, applied, err)
	}
	if git("rev-parse", "HEAD^") != head {
		t.Fatal("commit parent changed on receipt recovery")
	}
	again, err := gateway.Execute(ctx, commit)
	if err != nil || again.Result.CommitSHA != committed.Result.CommitSHA || git("rev-parse", "HEAD") != committed.Result.CommitSHA {
		t.Fatalf("duplicate commit = %+v err=%v", again, err)
	}
}

func TestGitEffectCancellationDoesNotStartUnreservedStage(t *testing.T) {
	root := t.TempDir()
	command := exec.Command("git", "init", "-q")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	// Reconciliation is observational even for an uninitialized repository:
	// it must not stage a path or reserve an intent.
	policy := kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	gateway := MutationGateway{Bindings: effectBinding{authority: Authority{WorkspaceRoot: root, Permissions: []string{"repository.edit"}, Purpose: kernel.PurposeImplementation, EffectPolicyDigest: policy}},
		Host: testHost(), Ledger: &memoryEffectLedger{}, Policy: policy}
	request := Request{InvocationID: kernel.UUIDv7("00000000-0000-7000-8000-000000000984"), RequestDigest: policy, ToolCallID: "stage-1",
		Call: Call{Name: "git_stage_files", Arguments: json.RawMessage(`{"paths":["work.go"],"expected_head":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, applied, err := gateway.Reconcile(ctx, request); err != nil || applied {
		t.Fatalf("unreserved stage = applied %t, err %v", applied, err)
	}
}
