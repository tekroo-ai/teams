package agenttools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFindFilesIsBoundedAndWorkspaceScoped(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"AGENTS.md", "src/AGENTS.md", "src/alias.go", "src/other.go"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.go"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.go"), filepath.Join(root, "src", "secret.go")); err != nil {
		t.Fatal(err)
	}
	authority := Authority{WorkspaceRoot: root, Permissions: []string{"repository.read"}}
	first, err := testHost().execute(context.Background(), authority, call("find_files", `{"filename_glob":"**/AGENTS.md","limit":1}`))
	if err != nil || !reflect.DeepEqual(first.Files, []string{"AGENTS.md"}) || !first.Truncated || first.NextAfter != "AGENTS.md" {
		t.Fatalf("first page: %+v, %v", first, err)
	}
	second, err := testHost().execute(context.Background(), authority, call("find_files", `{"filename_glob":"**/AGENTS.md","start_after":"AGENTS.md","limit":1}`))
	if err != nil || !reflect.DeepEqual(second.Files, []string{"src/AGENTS.md"}) || second.Truncated {
		t.Fatalf("second page: %+v, %v", second, err)
	}
	goFiles, err := testHost().execute(context.Background(), authority, call("find_files", `{"filename_glob":"*.go","directory":"src"}`))
	if err != nil || !reflect.DeepEqual(goFiles.Files, []string{"src/alias.go", "src/other.go"}) {
		t.Fatalf("directory glob: %+v, %v", goFiles, err)
	}
	for _, args := range []string{`{"filename_glob":"../*.go"}`, `{"filename_glob":"["}`, `{"filename_glob":"*.go","directory":"../"}`, `{"filename_glob":"*.go","directory":"src/secret.go"}`} {
		_, err := testHost().execute(context.Background(), authority, call("find_files", args))
		if err == nil {
			t.Fatalf("invalid find accepted: %s", args)
		}
	}
}

func TestFindFilesPaginatesInSortedPathOrder(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "a"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.go", "a/z.go"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	authority := Authority{WorkspaceRoot: root, Permissions: []string{"repository.read"}}
	first, err := testHost().execute(context.Background(), authority, call("find_files", `{"filename_glob":"*.go","limit":1}`))
	if err != nil || !reflect.DeepEqual(first.Files, []string{"a.go"}) || !first.Truncated || first.NextAfter != "a.go" {
		t.Fatalf("first sorted page: %+v, %v", first, err)
	}
	second, err := testHost().execute(context.Background(), authority, call("find_files", `{"filename_glob":"*.go","start_after":"a.go","limit":1}`))
	if err != nil || !reflect.DeepEqual(second.Files, []string{"a/z.go"}) || second.Truncated {
		t.Fatalf("second sorted page: %+v, %v", second, err)
	}
}

func TestSearchFileContentsReportsMatchesAndIncompleteSearch(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "alias.go"), []byte("func BindAlias() {}\nfunc ResolveAlias() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "binary.go"), []byte{'x', 0, 'y'}, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "other_test.go"), []byte("ALIAS\n"), 0600); err != nil {
		t.Fatal(err)
	}
	authority := Authority{WorkspaceRoot: root, Permissions: []string{"repository.read"}}
	result, err := testHost().execute(context.Background(), authority, call("search_file_contents", `{"regex":"Alias","path":"src","filename_glob":"*.go","max_results":1}`))
	if err != nil || len(result.Matches) != 1 || result.Matches[0].Path != "src/alias.go" || result.Matches[0].Line != 1 || !result.Truncated {
		t.Fatalf("bounded search: %+v, %v", result, err)
	}
	result, err = testHost().execute(context.Background(), authority, call("search_file_contents", `{"regex":"ResolveAlias","path":"src/alias.go"}`))
	if err != nil || len(result.Matches) != 1 || result.Matches[0].Line != 2 || result.Truncated {
		t.Fatalf("single-file search: %+v, %v", result, err)
	}
	contextResult, err := testHost().execute(context.Background(), authority, call("search_file_contents", `{"regex":"BindAlias","path":"src/alias.go","after_lines":1}`))
	if err != nil || len(contextResult.Matches) != 1 || !reflect.DeepEqual(contextResult.Matches[0].After, []string{"func ResolveAlias() {}"}) {
		t.Fatalf("following lines: %+v, %v", contextResult, err)
	}
	filenames, err := testHost().execute(context.Background(), authority, call("search_file_contents", `{"regex":"alias","path":"src","filename_glob":"alias*.go","exclude_glob":"*_test.go","ignore_case":true,"result_mode":"filenames"}`))
	if err != nil || !reflect.DeepEqual(filenames.Files, []string{"src/alias.go"}) || filenames.Truncated {
		t.Fatalf("case-insensitive filename search: %+v, %v", filenames, err)
	}
	count, err := testHost().execute(context.Background(), authority, call("search_file_contents", `{"regex":"Alias","path":"src/alias.go","result_mode":"count"}`))
	if err != nil || count.MatchCount == nil || *count.MatchCount != 2 || count.Truncated {
		t.Fatalf("count search: %+v, %v", count, err)
	}
	result, err = testHost().execute(context.Background(), authority, call("search_file_contents", `{"regex":"not-present","path":"src"}`))
	if err != nil || len(result.Matches) != 0 || result.SkippedFiles != 1 || !result.Truncated {
		t.Fatalf("binary skip was hidden: %+v, %v", result, err)
	}
	for _, args := range []string{`{"regex":"["}`, `{"regex":"Alias","path":"../"}`, `{"regex":"Alias","filename_glob":"["}`, `{"regex":"Alias","exclude_glob":"../x"}`, `{"regex":"Alias","max_results":0}`, `{"regex":"Alias","result_mode":"count","after_lines":1}`, `{"regex":"Alias","result_mode":"unknown"}`} {
		_, err := testHost().execute(context.Background(), authority, call("search_file_contents", args))
		if err == nil {
			t.Fatalf("invalid search accepted: %s", args)
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = testHost().execute(cancelled, authority, call("search_file_contents", `{"regex":"Alias"}`))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled search continued: %v", err)
	}
}
