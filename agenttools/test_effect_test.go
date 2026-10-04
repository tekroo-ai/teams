package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

func TestTestGatewayReturnsDurableReceiptWithoutRerun(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/effect\n\ngo 1.26.0\n"), 0600); err != nil {
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
	digest := kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	ledger := &memoryEffectLedger{}
	gateway := TestGateway{Bindings: effectBinding{authority: Authority{WorkspaceRoot: root,
		Permissions: []string{"repository.read", "test.execute"}, Purpose: kernel.PurposeValidation, EffectPolicyDigest: digest}},
		Host: Host{Timeout: 30 * time.Second}, Ledger: ledger, Policy: digest}
	request := Request{InvocationID: kernel.UUIDv7("00000000-0000-7000-8000-000000000999"), RequestDigest: digest,
		ToolCallID: "test-1", Call: Call{Name: "run_go_tests", Arguments: json.RawMessage(`{"package":"./..."}`)}}
	first, err := gateway.Execute(context.Background(), request)
	if err != nil || first.Result.ExitCode != 0 || first.Result.CommitSHA == "" || first.Result.SHA256 == "" {
		t.Fatalf("first test receipt: %+v, %v", first, err)
	}
	// Rewriting the source after completion must not trigger a second test for
	// the same tool-call identity; the prior immutable result is returned.
	if err := os.WriteFile(filepath.Join(root, "sample_test.go"), []byte("invalid Go"), 0600); err != nil {
		t.Fatal(err)
	}
	second, err := gateway.Execute(context.Background(), request)
	if err != nil || second.ResultHash != first.ResultHash {
		t.Fatalf("replayed receipt: %+v, %v", second, err)
	}
	reconciled, found, err := gateway.Reconcile(context.Background(), request)
	if err != nil || !found || reconciled.ResultHash != first.ResultHash {
		t.Fatalf("reconciled receipt: %+v, %t, %v", reconciled, found, err)
	}
	request.Call.Arguments = json.RawMessage(`{"package":"./agenttools"}`)
	if _, err := gateway.Execute(context.Background(), request); !errors.Is(err, ErrConflict) {
		t.Fatalf("call-id argument drift: %v", err)
	}
}

func TestTestGatewayStopsOnUnresolvedIntent(t *testing.T) {
	root := t.TempDir()
	digest := kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	ledger := &memoryEffectLedger{}
	gateway := TestGateway{Bindings: effectBinding{authority: Authority{WorkspaceRoot: root,
		Permissions: []string{"repository.read", "test.execute"}, Purpose: kernel.PurposeValidation, EffectPolicyDigest: digest}},
		Host: Host{Timeout: time.Second}, Ledger: ledger, Policy: digest}
	request := Request{InvocationID: kernel.UUIDv7("00000000-0000-7000-8000-000000000998"), RequestDigest: digest,
		ToolCallID: "test-unknown", Call: Call{Name: "run_go_tests", Arguments: json.RawMessage(`{"package":"./..."}`)}}
	_, err := gateway.Execute(context.Background(), request)
	if !errors.Is(err, ErrEffectUncertain) {
		t.Fatalf("first attempt without Git candidate: %v", err)
	}
	_, err = gateway.Execute(context.Background(), request)
	if !errors.Is(err, ErrEffectUncertain) {
		t.Fatalf("unknown outcome was automatically retried: %v", err)
	}
	_, found, err := gateway.Reconcile(context.Background(), request)
	if !errors.Is(err, ErrEffectUncertain) || found {
		t.Fatalf("unresolved intent should remain uncertain: found=%t err=%v", found, err)
	}
}

func TestWorktreeTestGatewayReplaysCapturedResult(t *testing.T) {
	root := t.TempDir()
	writeSnapshotFixture(t, root, "go.mod", "module example.test/effect\n\ngo 1.26.0\n")
	writeSnapshotFixture(t, root, "baseline.go", "package effect\n")
	fixtureGit(t, root, "init", "-q")
	fixtureGit(t, root, "add", "go.mod", "baseline.go")
	fixtureGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "-qm", "baseline")
	writeSnapshotFixture(t, root, "value_test.go", "package effect\nimport \"testing\"\nfunc TestValue(t *testing.T) { if Value() != 42 { t.Fatal(\"wrong\") } }\n")
	digest := kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	ledger := &memoryEffectLedger{}
	authority := Authority{WorkspaceRoot: root, Permissions: []string{"repository.read", "test.execute"}, Purpose: kernel.PurposeImplementation, EffectPolicyDigest: digest}
	gateway := TestGateway{Bindings: effectBinding{authority: authority}, Host: Host{Timeout: 2 * time.Minute}, Ledger: ledger, Policy: digest}
	request := Request{InvocationID: kernel.UUIDv7("00000000-0000-7000-8000-000000000997"), RequestDigest: digest,
		ToolCallID: "worktree-test-1", Call: Call{Name: "run_go_tests_worktree", Arguments: json.RawMessage(`{"package":"./..."}`)}}
	first, err := gateway.Execute(context.Background(), request)
	if err != nil || first.Result.ExitCode == 0 || !validExpectedSHA(first.Result.SnapshotSHA256) {
		t.Fatalf("red worktree receipt: %+v, %v", first, err)
	}
	writeSnapshotFixture(t, root, "value.go", "package effect\nfunc Value() int { return 42 }\n")
	replayed, err := gateway.Execute(context.Background(), request)
	if err != nil || replayed.ResultHash != first.ResultHash {
		t.Fatalf("worktree result silently re-executed: %+v, %v", replayed, err)
	}
	reconciled, found, err := gateway.Reconcile(context.Background(), request)
	if err != nil || !found || reconciled.ResultHash != first.ResultHash {
		t.Fatalf("worktree reconciliation: %+v, %t, %v", reconciled, found, err)
	}
	request.ToolCallID = "worktree-test-2"
	green, err := gateway.Execute(context.Background(), request)
	if err != nil || green.Result.ExitCode != 0 || green.Result.SnapshotSHA256 == first.Result.SnapshotSHA256 {
		t.Fatalf("new worktree snapshot did not pass: %+v, %v", green, err)
	}
	authority.Purpose = kernel.PurposeValidation
	validation := TestGateway{Bindings: effectBinding{authority: authority}, Host: gateway.Host, Ledger: ledger, Policy: digest}
	request.ToolCallID = "validation-forbidden"
	if _, err := validation.Execute(context.Background(), request); !errors.Is(err, ErrForbidden) {
		t.Fatalf("validation received worktree test capability: %v", err)
	}
}
