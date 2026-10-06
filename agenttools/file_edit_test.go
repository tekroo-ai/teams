package agenttools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tekroo-ai/teams/kernel"
)

func textSHA(text string) string {
	digest := sha256.Sum256([]byte(text))
	return hex.EncodeToString(digest[:])
}

func TestEditFileAtomicOrderedReplacements(t *testing.T) {
	for _, badSecond := range []bool{false, true} {
		t.Run(fmt.Sprint(badSecond), func(t *testing.T) {
			root := t.TempDir()
			before := "first\nsecond\nkeep\n"
			file := filepath.Join(root, "note.txt")
			if err := os.WriteFile(file, []byte(before), 0600); err != nil {
				t.Fatal(err)
			}
			edits := []textEdit{{"first", "updated"}, {"second\n", ""}}
			if badSecond {
				edits[1].OldText = "absent"
			}
			args, _ := json.Marshal(fileEdit{Path: "note.txt", ExpectedSHA256: textSHA(before), Edits: edits})
			result, err := testHost().execute(t.Context(), Authority{WorkspaceRoot: root, Permissions: []string{"repository.edit"}}, Call{Name: "edit_file", Arguments: args})
			got, readErr := os.ReadFile(file)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if badSecond {
				if err == nil || string(got) != before {
					t.Fatalf("partial edit: %q error=%v", got, err)
				}
			} else if err != nil || string(got) != "updated\nkeep\n" || result.SHA256 != textSHA(string(got)) {
				t.Fatalf("atomic edit: %q result=%+v error=%v", got, result, err)
			}
		})
	}
}

func TestAtomicEditEffectLostReceiptReconcilesWholePostimage(t *testing.T) {
	gateway, ledger, root, request := editEffectFixture(t)
	args, _ := json.Marshal(fileEdit{Path: "note.txt", ExpectedSHA256: textSHA("before\nkeep\n"), Edits: []textEdit{{"before", "after"}, {"keep", "preserved"}}})
	request.Call.Arguments = args
	ledger.failComplete = true
	if _, err := gateway.Execute(t.Context(), request); !errors.Is(err, ErrEffectUncertain) {
		t.Fatalf("lost receipt: %v", err)
	}
	result, complete, err := gateway.Reconcile(t.Context(), request)
	got, _ := os.ReadFile(filepath.Join(root, "note.txt"))
	if err != nil || !complete || string(got) != "after\npreserved\n" || result.Result.SHA256 != textSHA(string(got)) {
		t.Fatalf("atomic replay: result=%+v complete=%t content=%q error=%v", result, complete, got, err)
	}
}

func editArguments(path, before, oldText, newText string) json.RawMessage {
	encoded, _ := json.Marshal(fileEdit{Path: path, Edits: []textEdit{{OldText: oldText, NewText: newText}}, ExpectedSHA256: textSHA(before)})
	return encoded
}

func TestEditFilePreservesUnseenLargeFileContent(t *testing.T) {
	root := t.TempDir()
	before := strings.Repeat("untouched prefix\n", 800) + "const MigrationLevel = 1\n" + strings.Repeat("untouched suffix\n", 800)
	path := filepath.Join(root, "store.go")
	if err := os.WriteFile(path, []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	authority := Authority{WorkspaceRoot: root, Permissions: []string{"repository.edit"}, Purpose: kernel.PurposeImplementation}
	call := Call{Name: "edit_file", Arguments: editArguments("store.go", before, "const MigrationLevel = 1", "const MigrationLevel = 2")}
	result, err := testHost().execute(t.Context(), authority, call)
	want := strings.Replace(before, "const MigrationLevel = 1", "const MigrationLevel = 2", 1)
	got, readErr := os.ReadFile(path)
	info, statErr := os.Stat(path)
	if err != nil || readErr != nil || string(got) != want || result.Name != "edit_file" || result.SHA256 != textSHA(want) || statErr != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("precise edit did not preserve the file: result=%+v error=%v read=%v stat=%v", result, err, readErr, statErr)
	}
}

func TestEditFileRejectsBadPreconditionsWithoutChangingFile(t *testing.T) {
	for _, tc := range []struct{ name, before, oldText, newText, expected string }{
		{"missing", "prefix\nbody\nsuffix\n", "absent", "new", ""},
		{"duplicate", "same\nsame\n", "same", "new", ""},
		{"overlapping", "aaa", "aa", "new", ""},
		{"stale", "old", "old", "new", strings.Repeat("f", 64)},
		{"empty-match", "old", "", "new", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "note.txt")
			if err := os.WriteFile(path, []byte(tc.before), 0600); err != nil {
				t.Fatal(err)
			}
			args := editArguments("note.txt", tc.before, tc.oldText, tc.newText)
			if tc.expected != "" {
				args, _ = json.Marshal(fileEdit{Path: "note.txt", Edits: []textEdit{{tc.oldText, tc.newText}}, ExpectedSHA256: tc.expected})
			}
			_, err := testHost().execute(t.Context(), Authority{WorkspaceRoot: root, Permissions: []string{"repository.edit"}}, Call{Name: "edit_file", Arguments: args})
			got, readErr := os.ReadFile(path)
			if err == nil || readErr != nil || string(got) != tc.before {
				t.Fatalf("bad edit mutated source: %q, %v, %v", got, err, readErr)
			}
		})
	}
}

func TestEditFileSupportsDeletionAndUnicodeWithoutShellInterpretation(t *testing.T) {
	root := t.TempDir()
	before := "你好\r\nremove this\r\nkeep $(not-a-command); &\r\n"
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := testHost().execute(t.Context(), Authority{WorkspaceRoot: root, Permissions: []string{"repository.edit"}},
		Call{Name: "edit_file", Arguments: editArguments("note.txt", before, "remove this\r\n", "")})
	got, _ := os.ReadFile(filepath.Join(root, "note.txt"))
	if err != nil || string(got) != "你好\r\nkeep $(not-a-command); &\r\n" || result.SHA256 != textSHA(string(got)) {
		t.Fatalf("literal deletion: %q %+v %v", got, result, err)
	}
}

func TestEditFileKeepsWorkspaceAndPermissionBoundaries(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, ".git"), filepath.Join(root, "metadata")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".openhands"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".openhands", "agent.yaml"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, ".openhands"), filepath.Join(root, "runtime")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"link", "../secret.txt", secret, ".git/config", "metadata/config", ".openhands/agent.yaml", "runtime/agent.yaml", "absent.txt"} {
		_, err := testHost().execute(t.Context(), Authority{WorkspaceRoot: root, Permissions: []string{"repository.edit"}}, Call{Name: "edit_file", Arguments: editArguments(path, "secret", "secret", "changed")})
		if err == nil {
			t.Fatalf("unsafe path accepted: %q", path)
		}
	}
	_, err := testHost().execute(t.Context(), Authority{WorkspaceRoot: root, Permissions: []string{"repository.read"}}, Call{Name: "edit_file", Arguments: editArguments("link", "secret", "secret", "changed")})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("read-only role edited: %v", err)
	}
	got, _ := os.ReadFile(secret)
	if string(got) != "secret" {
		t.Fatal("edited outside workspace")
	}
	for _, path := range []string{".git/config", ".openhands/agent.yaml"} {
		got, err := os.ReadFile(filepath.Join(root, path))
		if err != nil || string(got) != "secret" {
			t.Fatalf("protected metadata changed: %s", path)
		}
	}
}

func TestEditFileRequiresExplicitReplacementIncludingDeletion(t *testing.T) {
	for _, raw := range []string{
		`{"path":"a","edits":[],"expected_sha256":"` + strings.Repeat("a", 64) + `"}`,
		`{"path":"a","edits":[{"old_text":"x"}],"expected_sha256":"` + strings.Repeat("a", 64) + `"}`,
		`{"path":"a","edits":[{"old_text":"x","new_text":null}],"expected_sha256":"` + strings.Repeat("a", 64) + `"}`,
		`{"path":"a","edits":[{"old_text":"x","new_text":"y","extra":1}],"expected_sha256":"` + strings.Repeat("a", 64) + `"}`,
		`{"path":"a","edits":[{"old_text":"x","new_text":"y"}],"old_text":"x","expected_sha256":"` + strings.Repeat("a", 64) + `"}`,
		`{"path":"a","old_text":"x","expected_sha256":"` + strings.Repeat("a", 64) + `"}`,
		`{"path":"a","old_text":"x","new_text":null,"expected_sha256":"` + strings.Repeat("a", 64) + `"}`,
		`{"path":"a","old_text":"x","new_text":"y","expected_sha256":"` + strings.Repeat("a", 64) + `","extra":true}`,
	} {
		if _, err := decodeFileEdit(json.RawMessage(raw)); err == nil {
			t.Fatalf("invalid edit accepted: %s", raw)
		}
	}
	if _, err := decodeFileEdit(editArguments("a", "x", "x", "")); err != nil {
		t.Fatalf("explicit deletion rejected: %v", err)
	}
}

func editEffectFixture(t *testing.T) (MutationGateway, *memoryEffectLedger, string, Request) {
	t.Helper()
	gateway, ledger, root, request := effectFixture(t)
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("before\nkeep\n"), 0600); err != nil {
		t.Fatal(err)
	}
	request.Call = Call{Name: "edit_file", Arguments: editArguments("note.txt", "before\nkeep\n", "before", "after")}
	return gateway, ledger, root, request
}

func TestEditEffectReconcilesLostReceiptAndReplaysWithoutEditingAgain(t *testing.T) {
	gateway, ledger, root, request := editEffectFixture(t)
	ledger.failComplete = true
	if _, err := gateway.Execute(t.Context(), request); !errors.Is(err, ErrEffectUncertain) {
		t.Fatalf("lost receipt: %v", err)
	}
	receipt, completed, err := gateway.Reconcile(t.Context(), request)
	if err != nil || !completed || receipt.Result.SHA256 != textSHA("after\nkeep\n") {
		t.Fatalf("edit reconciliation: %+v %t %v", receipt, completed, err)
	}
	// A later edit must not be overwritten when an old call is replayed.
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("later"), 0600); err != nil {
		t.Fatal(err)
	}
	again, err := gateway.Execute(t.Context(), request)
	got, _ := os.ReadFile(filepath.Join(root, "note.txt"))
	if err != nil || again.ResultHash != receipt.ResultHash || string(got) != "later" {
		t.Fatalf("edit replay mutated later source: %+v %q %v", again, got, err)
	}
	request.Call.Arguments = editArguments("note.txt", "before\nkeep\n", "before", "different")
	if _, err := gateway.Execute(t.Context(), request); !errors.Is(err, ErrEffectUncertain) {
		t.Fatalf("changed arguments reused intent: %v", err)
	}
}

func TestEditEffectLostReservationDoesNotApplyOrRepeatEdit(t *testing.T) {
	gateway, ledger, root, request := editEffectFixture(t)
	ledger.failReserve = true
	for i := 0; i < 2; i++ {
		if _, err := gateway.Execute(t.Context(), request); !errors.Is(err, ErrEffectUncertain) {
			t.Fatalf("lost reservation %d: %v", i, err)
		}
	}
	if _, completed, err := gateway.Reconcile(t.Context(), request); err != nil || completed {
		t.Fatalf("unapplied edit: %t %v", completed, err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "note.txt"))
	if string(got) != "before\nkeep\n" {
		t.Fatal("reserved edit ran during recovery")
	}
}

func TestEditEffectRejectedPreconditionHasNoIntent(t *testing.T) {
	gateway, ledger, root, request := editEffectFixture(t)
	request.Call.Arguments = editArguments("note.txt", "before\nkeep\n", "absent", "after")
	if _, err := gateway.Execute(context.Background(), request); !errors.Is(err, ErrConflict) {
		t.Fatalf("bad match: %v", err)
	}
	if len(ledger.records) != 0 {
		t.Fatal("rejected edit reserved an intent")
	}
	got, _ := os.ReadFile(filepath.Join(root, "note.txt"))
	if string(got) != "before\nkeep\n" {
		t.Fatal("rejected edit changed file")
	}
}

func TestEditEffectDoesNotReconcileDivergentFileOrCorruptPreparedHash(t *testing.T) {
	for _, mode := range []string{"file-drift", "invalid-hash"} {
		t.Run(mode, func(t *testing.T) {
			gateway, ledger, root, request := editEffectFixture(t)
			ledger.failComplete = true
			if _, err := gateway.Execute(t.Context(), request); !errors.Is(err, ErrEffectUncertain) {
				t.Fatal(err)
			}
			if mode == "file-drift" {
				if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("third version"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				key := string(request.InvocationID) + ":" + request.ToolCallID
				record := ledger.records[key]
				record.PreparedSHA256 = "invalid"
				ledger.records[key] = record
			}
			if _, completed, err := gateway.Reconcile(t.Context(), request); !errors.Is(err, ErrEffectUncertain) || completed {
				t.Fatalf("invented edit outcome: %t %v", completed, err)
			}
		})
	}
}

func TestEditEffectRejectsReadOnlyAndNonImplementationRoles(t *testing.T) {
	for _, purpose := range []kernel.WorkPurpose{kernel.PurposeReview, kernel.PurposeValidation} {
		gateway, ledger, _, request := editEffectFixture(t)
		binding := gateway.Bindings.(effectBinding)
		binding.authority.Purpose = purpose
		gateway.Bindings = binding
		if _, err := gateway.Execute(t.Context(), request); !errors.Is(err, ErrForbidden) {
			t.Fatalf("purpose %s edited: %v", purpose, err)
		}
		if len(ledger.records) != 0 {
			t.Fatal("forbidden role reserved edit")
		}
	}
}
