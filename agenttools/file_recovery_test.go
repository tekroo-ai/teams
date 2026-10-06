package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func recoveryRequest(original Request, id, name string, args any) Request {
	original.ToolCallID, original.Call.Name = id, name
	original.Call.Arguments, _ = json.Marshal(args)
	return original
}

func TestUndoFileChangeRestoresUncommittedPreimageAndNeverReplays(t *testing.T) {
	gateway, ledger, root, edit := editEffectFixture(t)
	before := "before\nkeep\n"
	first, err := gateway.Execute(t.Context(), edit)
	if err != nil || first.Result.ChangeID == "" {
		t.Fatalf("edit: %+v %v", first, err)
	}
	undo := recoveryRequest(edit, "undo-1", "undo_file_change", map[string]string{"change_id": first.Result.ChangeID, "expected_sha256": first.Result.SHA256})
	ledger.failComplete = true
	if _, err := gateway.Execute(t.Context(), undo); !errors.Is(err, ErrEffectUncertain) {
		t.Fatalf("lost undo receipt: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "note.txt"))
	if err != nil || string(got) != before {
		t.Fatalf("uncommitted preimage: %q %v", got, err)
	}
	// A newly constructed gateway reconciles retained metadata; it does not
	// need the previous process's memory or the model to supply file bytes.
	restarted := MutationGateway{Bindings: gateway.Bindings, Host: gateway.Host, Ledger: ledger, Policy: gateway.Policy}
	receipt, applied, err := restarted.Reconcile(t.Context(), undo)
	if err != nil || !applied || receipt.Result.SHA256 != textSHA(before) {
		t.Fatalf("reconcile: %+v %t %v", receipt, applied, err)
	}
	writeSnapshotFixture(t, root, "note.txt", "later work\n")
	again, err := restarted.Execute(t.Context(), undo)
	got, _ = os.ReadFile(filepath.Join(root, "note.txt"))
	if err != nil || again.ResultHash != receipt.ResultHash || string(got) != "later work\n" {
		t.Fatalf("replay overwrote later work: %+v %q %v", again, got, err)
	}
}

func TestCreateUndoRemovesOnlyItsUnchangedNewFile(t *testing.T) {
	gateway, _, root, create := effectFixture(t)
	first, err := gateway.Execute(t.Context(), create)
	if err != nil {
		t.Fatal(err)
	}
	undo := recoveryRequest(create, "undo-create", "undo_file_change", map[string]string{"change_id": first.Result.ChangeID, "expected_sha256": first.Result.SHA256})
	receipt, err := gateway.Execute(t.Context(), undo)
	if err != nil || !receipt.Result.Removed {
		t.Fatalf("undo creation: %+v %v", receipt, err)
	}
	if _, err := os.Stat(filepath.Join(root, "note.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file remains: %v", err)
	}
	writeSnapshotFixture(t, root, "note.txt", "replacement by another action")
	if _, err := gateway.Execute(t.Context(), undo); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "note.txt"))
	if string(got) != "replacement by another action" {
		t.Fatal("replay deleted newer file")
	}
}

func TestUndoRejectsDriftForgeryAndCrossWorkspaceWithoutMutation(t *testing.T) {
	for _, mode := range []string{"drift", "forged", "cross-workspace", "corrupt-preimage", "read-only"} {
		t.Run(mode, func(t *testing.T) {
			gateway, ledger, root, edit := editEffectFixture(t)
			first, err := gateway.Execute(t.Context(), edit)
			if err != nil {
				t.Fatal(err)
			}
			id := first.Result.ChangeID
			if mode == "drift" {
				writeSnapshotFixture(t, root, "note.txt", "later work")
			}
			if mode == "forged" {
				id = strings.Repeat("f", 64)
			}
			if mode == "corrupt-preimage" {
				key := string(edit.InvocationID) + ":" + edit.ToolCallID
				r := ledger.records[key]
				r.BeforeContent = []byte("injected")
				ledger.records[key] = r
			}
			if mode == "cross-workspace" {
				root = t.TempDir()
				writeSnapshotFixture(t, root, "note.txt", "after\nkeep\n")
				binding := gateway.Bindings.(effectBinding)
				binding.authority.WorkspaceRoot = root
				gateway.Bindings = binding
			}
			if mode == "read-only" {
				binding := gateway.Bindings.(effectBinding)
				binding.authority.Permissions = []string{"repository.read"}
				gateway.Bindings = binding
			}
			before, _ := os.ReadFile(filepath.Join(root, "note.txt"))
			undo := recoveryRequest(edit, "undo-rejected", "undo_file_change", map[string]string{"change_id": id, "expected_sha256": first.Result.SHA256})
			if _, err := gateway.Execute(t.Context(), undo); err == nil {
				t.Fatal("unsafe undo accepted")
			}
			got, _ := os.ReadFile(filepath.Join(root, "note.txt"))
			if string(got) != string(before) {
				t.Fatal("unsafe undo mutated file")
			}
			if _, exists := ledger.records[string(edit.InvocationID)+":undo-rejected"]; exists {
				t.Fatal("rejected undo reserved effect")
			}
		})
	}
}

func TestRecoveryLostReservationNeverReapplies(t *testing.T) {
	gateway, ledger, root, edit := editEffectFixture(t)
	first, err := gateway.Execute(t.Context(), edit)
	if err != nil {
		t.Fatal(err)
	}
	undo := recoveryRequest(edit, "undo-reservation", "undo_file_change", map[string]string{"change_id": first.Result.ChangeID, "expected_sha256": first.Result.SHA256})
	ledger.failReserve = true
	for i := 0; i < 2; i++ {
		if _, err := gateway.Execute(t.Context(), undo); !errors.Is(err, ErrEffectUncertain) {
			t.Fatalf("uncertain reservation %d: %v", i, err)
		}
	}
	if _, applied, err := gateway.Reconcile(t.Context(), undo); err != nil || applied {
		t.Fatalf("unapplied reservation: %t %v", applied, err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "note.txt"))
	if string(got) != "after\nkeep\n" {
		t.Fatal("recovery ran after uncertain reservation")
	}
}

func TestCommittedFileReadAndRestoreUseContentNotCommitPatch(t *testing.T) {
	gateway, _, root, request := editEffectFixture(t)
	gateway.Host = testHost()
	fixtureGit(t, root, "init", "-q")
	fixtureGit(t, root, "add", "--", "note.txt")
	fixtureGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "baseline")
	baseline := strings.TrimSpace(fixtureGit(t, root, "rev-parse", "HEAD"))
	writeSnapshotFixture(t, root, "other.txt", "unrelated file")
	fixtureGit(t, root, "add", "--", "other.txt")
	fixtureGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "unrelated commit")
	head := strings.TrimSpace(fixtureGit(t, root, "rev-parse", "HEAD"))
	authority := gateway.Bindings.(effectBinding).authority
	read := recoveryRequest(request, "history", "read_file_from_commit", map[string]any{"path": "note.txt", "commit": head, "start_line": 2, "line_count": 1})
	result, err := gateway.Host.execute(t.Context(), authority, read.Call)
	if err != nil || result.Output != "keep\n" || result.SHA256 != textSHA("before\nkeep\n") || result.ContentComplete == nil || *result.ContentComplete || *result.LineCount != 2 {
		t.Fatalf("historical content: %+v %v", result, err)
	}
	writeSnapshotFixture(t, root, "note.txt", "damaged\n")
	index := strings.TrimSpace(fixtureGit(t, root, "write-tree"))
	restore := recoveryRequest(request, "restore", "restore_file_from_commit", map[string]string{"path": "note.txt", "commit": baseline, "expected_sha256": textSHA("damaged\n")})
	receipt, err := gateway.Execute(t.Context(), restore)
	got, _ := os.ReadFile(filepath.Join(root, "note.txt"))
	if err != nil || string(got) != "before\nkeep\n" || receipt.Result.ChangeID == "" {
		t.Fatalf("restore: %+v %q %v", receipt, got, err)
	}
	if strings.TrimSpace(fixtureGit(t, root, "rev-parse", "HEAD")) != head || strings.TrimSpace(fixtureGit(t, root, "write-tree")) != index {
		t.Fatal("restore changed HEAD/index")
	}
	undo := recoveryRequest(request, "undo-restore", "undo_file_change", map[string]string{"change_id": receipt.Result.ChangeID, "expected_sha256": receipt.Result.SHA256})
	if _, err := gateway.Execute(t.Context(), undo); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(filepath.Join(root, "note.txt"))
	if string(got) != "damaged\n" {
		t.Fatal("restore did not retain preimage")
	}
	// A valid commit on another branch is not restore authority.
	fixtureGit(t, root, "checkout", "-qb", "other")
	writeSnapshotFixture(t, root, "note.txt", "foreign branch\n")
	fixtureGit(t, root, "add", "--", "note.txt")
	fixtureGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "foreign")
	foreign := strings.TrimSpace(fixtureGit(t, root, "rev-parse", "HEAD"))
	fixtureGit(t, root, "checkout", "-q", "--detach", head)
	rejected := recoveryRequest(request, "restore-foreign", "restore_file_from_commit", map[string]string{"path": "note.txt", "commit": foreign, "expected_sha256": textSHA("before\nkeep\n")})
	if _, err := gateway.Execute(context.Background(), rejected); !errors.Is(err, ErrForbidden) {
		t.Fatalf("foreign commit accepted: %v", err)
	}
}

func TestFileToolsRejectWholeReplacementIncludingBatchBypass(t *testing.T) {
	before := strings.Repeat("retained source line\n", 699)
	for _, name := range []string{"create_file", "write_file", "edit_file"} {
		root := t.TempDir()
		writeSnapshotFixture(t, root, "host.go", before)
		args := map[string]any{"path": "host.go", "content": "package organization\n"}
		if name == "write_file" {
			args["expected_sha256"] = textSHA(before)
		}
		if name == "edit_file" {
			args = map[string]any{"path": "host.go", "expected_sha256": textSHA(before), "edits": []textEdit{{before, "package organization\n"}}}
		}
		raw, _ := json.Marshal(args)
		if _, err := testHost().execute(t.Context(), Authority{WorkspaceRoot: root, Permissions: []string{"repository.edit"}}, Call{Name: name, Arguments: raw}); err == nil {
			t.Fatalf("%s replaced entire file", name)
		}
		got, _ := os.ReadFile(filepath.Join(root, "host.go"))
		if string(got) != before {
			t.Fatalf("%s damaged file", name)
		}
	}
	root := t.TempDir()
	writeSnapshotFixture(t, root, "note.txt", "one\ntwo\n")
	raw, _ := json.Marshal(fileEdit{Path: "note.txt", ExpectedSHA256: textSHA("one\ntwo\n"), Edits: []textEdit{{"one", "new one"}, {"two", "new two"}}})
	if _, err := testHost().execute(t.Context(), Authority{WorkspaceRoot: root, Permissions: []string{"repository.edit"}}, Call{Name: "edit_file", Arguments: raw}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("batch bypass: %v", err)
	}
}
