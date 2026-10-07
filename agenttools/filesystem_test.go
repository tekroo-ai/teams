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

	"github.com/tekroo-ai/teams/kernel"
)

func filesystemFixture(t *testing.T, name string) (MutationGateway, *memoryEffectLedger, string, Request) {
	t.Helper()
	g, l, root, r := effectFixture(t)
	writeSnapshotFixture(t, root, "source.go", "package fixture\nfunc Value( )int{return 1}\n")
	var args any
	switch name {
	case "make_directory":
		args = map[string]string{"path": "nested/child"}
	case "format_go_files":
		args = map[string]any{"files": []map[string]string{{"path": "source.go", "expected_sha256": textSHA("package fixture\nfunc Value( )int{return 1}\n")}}}
	case "move_file":
		args = map[string]string{"source": "source.go", "destination": "moved.go", "expected_sha256": textSHA("package fixture\nfunc Value( )int{return 1}\n")}
	case "remove_file":
		args = map[string]string{"path": "source.go", "expected_sha256": textSHA("package fixture\nfunc Value( )int{return 1}\n")}
	default:
		t.Fatal("missing filesystem fixture")
	}
	return g, l, root, recoveryRequest(r, "filesystem", name, args)
}

func TestFilesystemToolsLostReceiptAndUndo(t *testing.T) {
	for _, name := range filesystemToolNames {
		t.Run(name, func(t *testing.T) {
			g, l, root, r := filesystemFixture(t, name)
			before, _ := os.ReadFile(filepath.Join(root, "source.go"))
			l.failComplete = true
			if _, err := g.Execute(t.Context(), r); !errors.Is(err, ErrEffectUncertain) {
				t.Fatalf("lost completion: %v", err)
			}
			first, ok, err := g.Reconcile(t.Context(), r)
			if err != nil || !ok || first.Result.Name != name {
				t.Fatalf("recovery: %+v %t %v", first, ok, err)
			}
			again, err := g.Execute(t.Context(), r)
			if err != nil || again.ResultHash != first.ResultHash {
				t.Fatalf("replay: %+v %v", again, err)
			}
			if name == "make_directory" {
				if info, err := os.Stat(filepath.Join(root, "nested/child")); err != nil || !info.IsDir() {
					t.Fatalf("directory: %v", err)
				}
				return
			}
			undo := recoveryRequest(r, "undo", "undo_file_change", map[string]string{"change_id": first.Result.ChangeID, "expected_sha256": first.Result.SHA256})
			if _, err := g.Execute(t.Context(), undo); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(filepath.Join(root, "source.go"))
			if err != nil || string(got) != string(before) {
				t.Fatalf("undo: %q %v", got, err)
			}
			if name == "move_file" {
				if _, err := os.Stat(filepath.Join(root, "moved.go")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("destination after undo: %v", err)
				}
			}
			writeSnapshotFixture(t, root, "source.go", "later work")
			if _, err := g.Execute(t.Context(), undo); err != nil {
				t.Fatal(err)
			}
			got, _ = os.ReadFile(filepath.Join(root, "source.go"))
			if string(got) != "later work" {
				t.Fatal("replay changed later work")
			}
		})
	}
}

func TestFilesystemToolsLostReservationNeverExecute(t *testing.T) {
	for _, name := range filesystemToolNames {
		t.Run(name, func(t *testing.T) {
			g, l, root, r := filesystemFixture(t, name)
			l.failReserve = true
			before, _ := os.ReadFile(filepath.Join(root, "source.go"))
			for attempt := 0; attempt < 2; attempt++ {
				if _, err := g.Execute(t.Context(), r); !errors.Is(err, ErrEffectUncertain) {
					t.Fatalf("reservation: %v", err)
				}
			}
			got, _ := os.ReadFile(filepath.Join(root, "source.go"))
			if string(got) != string(before) {
				t.Fatal("uncertain reservation mutated source")
			}
			if _, err := os.Stat(filepath.Join(root, "nested")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("uncertain directory created: %v", err)
			}
		})
	}
}

func TestFilesystemToolsRejectCorruptReceiptAndSnapshot(t *testing.T) {
	for _, name := range filesystemToolNames {
		t.Run(name, func(t *testing.T) {
			g, l, _, r := filesystemFixture(t, name)
			if _, err := g.Execute(t.Context(), r); err != nil {
				t.Fatal(err)
			}
			key := string(r.InvocationID) + ":" + r.ToolCallID
			record := l.records[key]
			record.PreparedSHA256 = strings.Repeat("f", 64)
			l.records[key] = record
			if _, err := g.Execute(t.Context(), r); !errors.Is(err, ErrEffectUncertain) {
				t.Fatalf("corrupt result: %v", err)
			}
		})
	}
}

func TestFilesystemPreconditionsAndFormattingAreValidatedBeforeEffects(t *testing.T) {
	g, l, root, r := filesystemFixture(t, "format_go_files")
	writeSnapshotFixture(t, root, "broken.go", "not go")
	r = recoveryRequest(r, "invalid-format", "format_go_files", map[string]any{"files": []map[string]string{
		{"path": "source.go", "expected_sha256": textSHA("package fixture\nfunc Value( )int{return 1}\n")},
		{"path": "broken.go", "expected_sha256": textSHA("not go")},
	}})
	if _, err := g.Execute(t.Context(), r); err == nil {
		t.Fatal("invalid source accepted")
	}
	got, _ := os.ReadFile(filepath.Join(root, "source.go"))
	if string(got) != "package fixture\nfunc Value( )int{return 1}\n" || len(l.records) != 0 {
		t.Fatal("formatted before all inputs validated")
	}
	for _, path := range []string{".", "../escape", ".git/objects", ".openhands/fixture"} {
		if _, err := g.Execute(t.Context(), recoveryRequest(r, path, "make_directory", map[string]string{"path": path})); err == nil {
			t.Fatalf("unsafe directory %q", path)
		}
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Execute(t.Context(), recoveryRequest(r, "link", "make_directory", map[string]string{"path": "escape/sub"})); err == nil {
		t.Fatal("followed symlink")
	}
	writeSnapshotFixture(t, root, "existing.go", "keep")
	if _, err := g.Execute(t.Context(), recoveryRequest(r, "no-overwrite", "move_file", map[string]string{"source": "source.go", "destination": "existing.go", "expected_sha256": textSHA(string(got))})); err == nil {
		t.Fatal("overwrote destination")
	}
}

func (l *memoryEffectLedger) ListFileChanges(_ context.Context, root string, policy kernel.Digest, path string) ([]EffectRecord, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var result []EffectRecord
	for _, r := range l.records {
		if r.WorkspaceRoot != root || r.EffectPolicyHash != policy || len(r.Result) == 0 {
			continue
		}
		if r.FilePath == path {
			result = append(result, r)
			continue
		}
		var intent filesystemIntent
		_ = json.Unmarshal(r.Filesystem, &intent)
		if slices.ContainsFunc(intent.Files, func(f fileTransition) bool { return f.Path == path }) {
			result = append(result, r)
		}
	}
	return result, nil
}

func TestFileHistoryReportsApplicableUndoWithoutPreimages(t *testing.T) {
	g, l, root, r := filesystemFixture(t, "remove_file")
	first, err := g.Execute(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	a := g.Bindings.(effectBinding).authority
	a.Permissions = []string{"repository.read"}
	read := Gateway{Host: g.Host, History: l}
	result, err := read.fileHistory(t.Context(), a, call("file_change_history", `{"path":"source.go"}`))
	if err != nil || len(result.Changes) != 1 || !result.Changes[0].ApplicableUndo || result.Changes[0].ChangeID != first.Result.ChangeID {
		t.Fatalf("history: %+v %v", result, err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "package fixture") {
		t.Fatal("history disclosed preimage")
	}
	writeSnapshotFixture(t, root, "source.go", "new content")
	result, err = read.fileHistory(t.Context(), a, call("file_change_history", `{"path":"source.go"}`))
	if err != nil || len(result.Changes) != 1 || result.Changes[0].ApplicableUndo {
		t.Fatalf("drift: %+v %v", result, err)
	}
}
