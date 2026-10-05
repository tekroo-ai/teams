package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	moduleRequest := request
	moduleRequest.ToolCallID = "module-test"
	moduleRequest.Call.Arguments = json.RawMessage(`{"package":"example.test/effect"}`)
	moduleResult, err := gateway.Execute(context.Background(), moduleRequest)
	if err != nil || moduleResult.Result.ExitCode != 0 {
		t.Fatalf("local module path test receipt: %+v, %v", moduleResult, err)
	}
	if _, found, err := gateway.Reconcile(context.Background(), moduleRequest); err != nil || !found {
		t.Fatalf("local module path test reconciliation: found=%t err=%v", found, err)
	}
	externalRequest := request
	externalRequest.ToolCallID = "external-test"
	externalRequest.Call.Arguments = json.RawMessage(`{"package":"example.test/other"}`)
	if _, err := gateway.Execute(context.Background(), externalRequest); !errors.Is(err, ErrInvalidCall) {
		t.Fatalf("external package was admitted: %v", err)
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
	// Lost reservation acknowledgement really is uncertain: execution must not
	// be started by a replay. A known preparation failure is a different case.
	ledger := &memoryEffectLedger{failReserve: true}
	gateway := TestGateway{Bindings: effectBinding{authority: Authority{WorkspaceRoot: root,
		Permissions: []string{"repository.read", "test.execute"}, Purpose: kernel.PurposeValidation, EffectPolicyDigest: digest}},
		Host: Host{Timeout: time.Second}, Ledger: ledger, Policy: digest}
	request := Request{InvocationID: kernel.UUIDv7("00000000-0000-7000-8000-000000000998"), RequestDigest: digest,
		ToolCallID: "test-unknown", Call: Call{Name: "run_go_tests", Arguments: json.RawMessage(`{"package":"./..."}`)}}
	_, err := gateway.Execute(context.Background(), request)
	if !errors.Is(err, ErrEffectUncertain) {
		t.Fatalf("lost reservation acknowledgement: %v", err)
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

func TestTestGatewayRetainsKnownPreparationFailure(t *testing.T) {
	root := t.TempDir() // No Git root: preparation fails before starting tests.
	digest := kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	ledger := &memoryEffectLedger{}
	gateway := TestGateway{Bindings: effectBinding{authority: Authority{WorkspaceRoot: root,
		Permissions: []string{"repository.read", "test.execute"}, Purpose: kernel.PurposeImplementation, EffectPolicyDigest: digest}},
		Host: Host{Timeout: time.Second}, Ledger: ledger, Policy: digest}
	request := Request{InvocationID: kernel.UUIDv7("00000000-0000-7000-8000-000000000996"), RequestDigest: digest,
		ToolCallID: "known-preparation-error", Call: Call{Name: "run_go_tests_worktree", Arguments: json.RawMessage(`{"package":"./..."}`)}}
	first, err := gateway.Execute(context.Background(), request)
	if err != nil || first.Result.ExitCode != -1 {
		t.Fatalf("known failure became uncertain: %+v, %v", first, err)
	}
	encoded, _ := json.Marshal(first.Result)
	var fields map[string]any
	_ = json.Unmarshal(encoded, &fields)
	if fields["execution_error"] != ErrInvalidCall.Error() {
		t.Fatalf("original failure not retained: %s", encoded)
	}
	// Changing the workspace must not replace the completed failure receipt.
	fixtureGit(t, root, "init", "-q")
	replayed, err := gateway.Execute(context.Background(), request)
	if err != nil || replayed.ResultHash != first.ResultHash {
		t.Fatalf("failure was replayed physically: %+v, %v", replayed, err)
	}
	reconciled, found, err := gateway.Reconcile(context.Background(), request)
	if err != nil || !found || reconciled.ResultHash != first.ResultHash {
		t.Fatalf("known failure reconciliation: %+v, %t, %v", reconciled, found, err)
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

func TestTestGatewayRetainsTimeoutAndCancellation(t *testing.T) {
	for _, cancelCaller := range []bool{false, true} {
		name := "timeout"
		if cancelCaller {
			name = "cancellation"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeSnapshotFixture(t, root, "go.mod", "module example.test/bounded\n\ngo 1.26.0\n")
			fixtureGit(t, root, "init", "-q")
			fixtureGit(t, root, "add", "go.mod")
			fixtureGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "-qm", "baseline")
			stub := filepath.Join(t.TempDir(), "go-stub")
			// A real executable exercises the sandbox/process path without
			// granting a shell interpreter any extra execution permission.
			stubSource := stub + ".go"
			if err := os.WriteFile(stubSource, []byte("package main\nimport (\"fmt\"; \"time\")\nfunc main() { fmt.Println(\"fixture-started\"); time.Sleep(30*time.Second) }\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if output, err := exec.Command("go", "build", "-o", stub, stubSource).CombinedOutput(); err != nil {
				t.Fatalf("build process fixture: %v: %s", err, output)
			}
			digest := kernel.Digest(strings.Repeat("a", 64))
			gateway := TestGateway{Bindings: effectBinding{authority: Authority{WorkspaceRoot: root,
				Permissions: []string{"repository.read", "test.execute"}, Purpose: kernel.PurposeImplementation, EffectPolicyDigest: digest}},
				Host: Host{Timeout: 2 * time.Second, GoBinary: stub}, Ledger: &memoryEffectLedger{}, Policy: digest}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if cancelCaller {
				gateway.Host.Timeout = 10 * time.Second
				timer := time.AfterFunc(2*time.Second, cancel)
				defer timer.Stop()
			}
			request := Request{InvocationID: "00000000-0000-7000-8000-000000000994", RequestDigest: digest, ToolCallID: name,
				Call: Call{Name: "run_go_tests_worktree", Arguments: json.RawMessage(`{"package":"./..."}`)}}
			started := time.Now()
			first, err := gateway.Execute(ctx, request)
			want := context.DeadlineExceeded.Error()
			if cancelCaller {
				want = context.Canceled.Error()
			}
			if err != nil || first.Result.ExitCode != -1 || first.Result.ExecutionError != want || !strings.Contains(first.Result.Output, "fixture-started") || time.Since(started) > 6*time.Second {
				t.Fatalf("bounded process failure was not retained: %+v, %v", first, err)
			}
			// Removing the executable makes any accidental second physical run
			// produce a different result; reconciliation must reuse the receipt.
			if err := os.Remove(stub); err != nil {
				t.Fatal(err)
			}
			replayed, err := gateway.Execute(context.Background(), request)
			if err != nil || replayed.ResultHash != first.ResultHash {
				t.Fatalf("failure re-executed: %+v, %v", replayed, err)
			}
			reconciled, found, err := gateway.Reconcile(context.Background(), request)
			if err != nil || !found || reconciled.ResultHash != first.ResultHash {
				t.Fatalf("failure reconciliation: %+v, %t, %v", reconciled, found, err)
			}
		})
	}
}

func TestTestGatewayLostCompletionNeverReruns(t *testing.T) {
	for _, storedBeforeError := range []bool{false, true} {
		t.Run(fmt.Sprint(storedBeforeError), func(t *testing.T) {
			root := t.TempDir()
			digest := kernel.Digest(strings.Repeat("a", 64))
			memory := &memoryEffectLedger{failComplete: !storedBeforeError}
			var ledger EffectLedger = memory
			if storedBeforeError {
				ledger = lostTestCompletionAck{memory}
			}
			gateway := TestGateway{Bindings: effectBinding{authority: Authority{WorkspaceRoot: root,
				Permissions: []string{"repository.read", "test.execute"}, Purpose: kernel.PurposeImplementation, EffectPolicyDigest: digest}},
				Host: Host{Timeout: time.Second}, Ledger: ledger, Policy: digest}
			request := Request{InvocationID: "00000000-0000-7000-8000-000000000993", RequestDigest: digest, ToolCallID: "lost-completion",
				Call: Call{Name: "run_go_tests_worktree", Arguments: json.RawMessage(`{"package":"./..."}`)}}
			if _, err := gateway.Execute(context.Background(), request); !errors.Is(err, ErrEffectUncertain) {
				t.Fatalf("lost acknowledgement not uncertain: %v", err)
			}
			fixtureGit(t, root, "init", "-q")
			replayed, err := gateway.Execute(context.Background(), request)
			if !storedBeforeError {
				if !errors.Is(err, ErrEffectUncertain) {
					t.Fatalf("missing receipt executed again: %+v, %v", replayed, err)
				}
			} else if err != nil || replayed.Result.ExecutionError != ErrInvalidCall.Error() {
				t.Fatalf("stored failure was not recovered: %+v, %v", replayed, err)
			}
		})
	}
}

type lostTestCompletionAck struct{ EffectLedger }

func (ledger lostTestCompletionAck) Complete(ctx context.Context, record EffectRecord, result json.RawMessage) error {
	if err := ledger.EffectLedger.Complete(ctx, record, result); err != nil {
		return err
	}
	return errors.New("simulated lost completion acknowledgement after storage")
}
