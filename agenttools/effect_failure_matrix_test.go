package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

var effectToolNames = []string{"write_file", "git_stage_files", "git_commit", "run_go_tests", "run_go_tests_worktree"}

func TestFailureMatrixCoversEveryEffectfulTool(t *testing.T) {
	var actual []string
	for _, spec := range specs {
		if spec.Permission == "repository.edit" || spec.Permission == "test.execute" {
			actual = append(actual, spec.Name)
		}
	}
	expected := slices.Clone(effectToolNames)
	slices.Sort(actual)
	slices.Sort(expected)
	if !slices.Equal(actual, expected) {
		t.Fatalf("effectful catalog changed without failure/replay coverage: catalog=%v matrix=%v", actual, expected)
	}
}

func failureMatrixFixture(t *testing.T, name string) (MutationGateway, TestGateway, *memoryEffectLedger, string, Request) {
	t.Helper()
	root := t.TempDir()
	writeSnapshotFixture(t, root, "go.mod", "module example.test/matrix\n\ngo 1.26.0\n")
	writeSnapshotFixture(t, root, "a.go", "package matrix\nconst A = 1\n")
	writeSnapshotFixture(t, root, "b.txt", "baseline\n")
	fixtureGit(t, root, "init", "-q")
	fixtureGit(t, root, "add", "--", "go.mod", "a.go", "b.txt")
	fixtureGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "baseline")
	head := strings.TrimSpace(fixtureGit(t, root, "rev-parse", "HEAD"))
	policy := kernel.Digest(strings.Repeat("a", 64))
	l := &memoryEffectLedger{}
	authority := effectBinding{Authority{WorkspaceRoot: root, Permissions: []string{"repository.edit", "test.execute"}, Purpose: kernel.PurposeImplementation, EffectPolicyDigest: policy}}
	host := Host{Timeout: 5 * time.Second, GoBinary: filepath.Join(root, "missing-go")}
	mutation := MutationGateway{Bindings: authority, Host: host, Ledger: l, Policy: policy}
	tests := TestGateway{Bindings: authority, Host: host, Ledger: l, Policy: policy}
	var args any
	switch name {
	case "write_file":
		if err := os.Mkdir(filepath.Join(root, "readonly"), 0500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "readonly"), 0700) })
		args = map[string]any{"path": "readonly/new.txt", "content": "new", "expected_sha256": ""}
	case "git_stage_files":
		args = map[string]any{"paths": []string{"a.go"}, "expected_head": strings.Repeat("b", 40)}
	case "git_commit":
		args = map[string]any{"expected_head": head, "expected_index_tree": strings.Repeat("b", 40), "subject": "candidate"}
	default:
		args = map[string]any{"package": "./..."}
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	request := Request{InvocationID: kernel.UUIDv7("00000000-0000-7000-8000-000000000986"), RequestDigest: policy, ToolCallID: "matrix-call", Call: Call{Name: name, Arguments: encoded}}
	return mutation, tests, l, root, request
}

func TestEveryEffectToolRetainsKnownFailureAndReplaysIt(t *testing.T) {
	for _, name := range effectToolNames {
		t.Run(name, func(t *testing.T) {
			mutation, tests, l, root, request := failureMatrixFixture(t, name)
			execute := mutation.Execute
			reconcile := mutation.Reconcile
			if isGoTestTool(name) {
				execute, reconcile = tests.Execute, tests.Reconcile
			}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			first, err := execute(ctx, request)
			if err != nil || !validFailedEffectResult(name, first.Result) {
				t.Fatalf("known failure was not completed: %+v, %v", first, err)
			}
			if len(l.records[string(request.InvocationID)+":"+request.ToolCallID].Result) == 0 {
				t.Fatal("failure has no durable receipt")
			}
			// Remove the write failure condition: the same identity must still
			// return the recorded failure, never create the file now.
			if name == "write_file" {
				if err := os.Chmod(filepath.Join(root, "readonly"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			again, err := execute(ctx, request)
			if err != nil || again.ResultHash != first.ResultHash {
				t.Fatalf("failure replay changed: %+v %v", again, err)
			}
			receipt, completed, err := reconcile(ctx, request)
			if err != nil || !completed || receipt.ResultHash != first.ResultHash {
				t.Fatalf("completed failure reconciliation: %+v %t %v", receipt, completed, err)
			}
			if name == "write_file" {
				if _, err := os.Stat(filepath.Join(root, "readonly", "new.txt")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("failure replay wrote a file: %v", err)
				}
			}
		})
	}
}

func TestEveryEffectToolStopsAfterLostFailureReceipt(t *testing.T) {
	for _, name := range effectToolNames {
		t.Run(name, func(t *testing.T) {
			mutation, tests, l, root, request := failureMatrixFixture(t, name)
			execute := mutation.Execute
			if isGoTestTool(name) {
				execute = tests.Execute
			}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			l.failComplete = true
			if _, err := execute(ctx, request); !errors.Is(err, ErrEffectUncertain) {
				t.Fatalf("lost failure receipt = %v", err)
			}
			if name == "write_file" {
				if err := os.Chmod(filepath.Join(root, "readonly"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := execute(ctx, request); !errors.Is(err, ErrEffectUncertain) {
				t.Fatalf("unresolved call was run again: %v", err)
			}
			if name == "write_file" {
				if _, err := os.Stat(filepath.Join(root, "readonly", "new.txt")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("lost-receipt replay wrote a file: %v", err)
				}
			}
		})
	}
}

func TestEveryEffectToolStopsAfterLostReservation(t *testing.T) {
	for _, name := range effectToolNames {
		t.Run(name, func(t *testing.T) {
			mutation, tests, l, _, request := failureMatrixFixture(t, name)
			execute := mutation.Execute
			if isGoTestTool(name) {
				execute = tests.Execute
			}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			l.failReserve = true
			for attempt := 0; attempt < 2; attempt++ {
				if _, err := execute(ctx, request); !errors.Is(err, ErrEffectUncertain) {
					t.Fatalf("lost reservation attempt %d = %v", attempt, err)
				}
			}
		})
	}
}

func TestEveryEffectToolRecoversLostCompletionAcknowledgment(t *testing.T) {
	for _, name := range effectToolNames {
		t.Run(name, func(t *testing.T) {
			mutation, tests, l, _, request := failureMatrixFixture(t, name)
			mutation.Ledger = lostTestCompletionAck{l}
			tests.Ledger = mutation.Ledger
			execute, reconcile := mutation.Execute, mutation.Reconcile
			if isGoTestTool(name) {
				execute, reconcile = tests.Execute, tests.Reconcile
			}
			if _, err := execute(t.Context(), request); !errors.Is(err, ErrEffectUncertain) {
				t.Fatalf("lost completion acknowledgment = %v", err)
			}
			receipt, completed, err := reconcile(t.Context(), request)
			if err != nil || !completed || !validFailedEffectResult(name, receipt.Result) {
				t.Fatalf("committed failure was not recovered: %+v %t %v", receipt, completed, err)
			}
			again, err := execute(t.Context(), request)
			if err != nil || again.ResultHash != receipt.ResultHash {
				t.Fatalf("replay did not return recovered outcome: %+v %v", again, err)
			}
		})
	}
}

func TestEveryEffectToolRejectsCorruptFailureReceipt(t *testing.T) {
	for _, name := range effectToolNames {
		t.Run(name, func(t *testing.T) {
			mutation, tests, l, _, request := failureMatrixFixture(t, name)
			execute, reconcile := mutation.Execute, mutation.Reconcile
			if isGoTestTool(name) {
				execute, reconcile = tests.Execute, tests.Reconcile
			}
			if _, err := execute(t.Context(), request); err != nil {
				t.Fatal(err)
			}
			key := string(request.InvocationID) + ":" + request.ToolCallID
			record := l.records[key]
			var result Result
			if err := json.Unmarshal(record.Result, &result); err != nil {
				t.Fatal(err)
			}
			result.SHA256 = strings.Repeat("f", 64)
			record.Result, _ = json.Marshal(result)
			l.records[key] = record
			if _, err := execute(t.Context(), request); !errors.Is(err, ErrEffectUncertain) {
				t.Fatalf("corrupt result replay = %v", err)
			}
			if _, completed, err := reconcile(t.Context(), request); !errors.Is(err, ErrEffectUncertain) || completed {
				t.Fatalf("corrupt result reconciliation: completed=%t error=%v", completed, err)
			}
		})
	}
}

func TestIncrementalStageAndLostReceiptDoNotRestageChangedFiles(t *testing.T) {
	mutation, _, l, root, request := failureMatrixFixture(t, "git_stage_files")
	head := strings.TrimSpace(fixtureGit(t, root, "rev-parse", "HEAD"))
	writeSnapshotFixture(t, root, "a.go", "package matrix\nconst A = 2\n")
	writeSnapshotFixture(t, root, "b.txt", "first change\n")
	request.Call.Arguments, _ = json.Marshal(map[string]any{"paths": []string{"a.go", "b.txt"}, "expected_head": head})
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if _, err := mutation.Execute(ctx, request); err != nil {
		t.Fatal(err)
	}
	writeSnapshotFixture(t, root, "a.go", "package matrix\nconst A = 3\n")
	request.ToolCallID = "subset-restage"
	request.Call.Arguments, _ = json.Marshal(map[string]any{"paths": []string{"a.go"}, "expected_head": head})
	l.failComplete = true
	if _, err := mutation.Execute(ctx, request); !errors.Is(err, ErrEffectUncertain) {
		t.Fatalf("expected lost completion, got %v", err)
	}
	index := strings.TrimSpace(fixtureGit(t, root, "write-tree"))
	if got := strings.TrimSpace(fixtureGit(t, root, "show", ":b.txt")); got != "first change" {
		t.Fatalf("incremental staging changed another entry: %q", got)
	}
	writeSnapshotFixture(t, root, "a.go", "package matrix\nconst A = 4\n")
	if _, err := mutation.Execute(ctx, request); !errors.Is(err, ErrEffectUncertain) {
		t.Fatalf("changed-source replay = %v", err)
	}
	if got := strings.TrimSpace(fixtureGit(t, root, "write-tree")); got != index {
		t.Fatal("same pending call restaged later source")
	}
	if len(l.records[string(request.InvocationID)+":"+request.ToolCallID].Result) != 0 {
		t.Fatal("unknown result was invented")
	}
}

type completionContextLedger struct{ valid bool }

func (l *completionContextLedger) Reserve(context.Context, EffectRecord) (EffectRecord, bool, error) {
	panic("unused")
}
func (l *completionContextLedger) Lookup(context.Context, EffectRecord) (EffectRecord, bool, error) {
	panic("unused")
}
func (l *completionContextLedger) Complete(ctx context.Context, _ EffectRecord, _ json.RawMessage) error {
	l.valid = ctx.Err() == nil
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 5*time.Second || !l.valid {
		return errors.New("receipt completion context is invalid")
	}
	return nil
}

func TestSharedCompletionIsBoundedForEveryEffectTool(t *testing.T) {
	for _, name := range effectToolNames {
		l := &completionContextLedger{}
		request := Request{Call: Call{Name: name}}
		if _, err := completeEffect(l, request, EffectRecord{}, failedEffectResult(name, Result{}, context.DeadlineExceeded)); err != nil || !l.valid {
			t.Fatalf("%s could not complete known timeout: %v", name, err)
		}
	}
}
