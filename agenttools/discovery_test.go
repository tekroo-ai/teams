package agenttools

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestDiscoveryReadPaginationAndConfinedMissingPaths(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	writeSnapshotFixture(t, root, "note.txt", "one\ntwo\nthree\n")
	authority := Authority{WorkspaceRoot: root, Permissions: []string{"repository.read"}}
	first, err := testHost().execute(t.Context(), authority, call("read_file", `{"path":"note.txt","line_count":2}`))
	if err != nil || first.NextStartLine == nil || *first.NextStartLine != 3 || first.Output != "one\ntwo" {
		t.Fatalf("first page %+v %v", first, err)
	}
	last, err := testHost().execute(t.Context(), authority, call("read_file", `{"path":"note.txt","start_line":3,"line_count":2}`))
	if err != nil || last.NextStartLine != nil || last.Output != "three" || first.SHA256 != last.SHA256 {
		t.Fatalf("last page %+v %v", last, err)
	}
	for _, path := range []string{"missing.txt", "missing/child.txt"} {
		args, _ := json.Marshal(map[string]string{"path": path})
		r, err := testHost().execute(t.Context(), authority, Call{Name: "read_file", Arguments: args})
		if err != nil || r.Status != "NOT_FOUND" || r.Path != path || r.Output != "" {
			t.Fatalf("missing %+v %v", r, err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "absent"), filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../absent", "escape/absent", "dangling"} {
		args, _ := json.Marshal(map[string]string{"path": path})
		_, err := testHost().execute(t.Context(), authority, Call{Name: "read_file", Arguments: args})
		if !errors.Is(err, ErrBoundary) {
			t.Fatalf("unsafe missing %s: %v", path, err)
		}
	}
	_, err = testHost().execute(t.Context(), Authority{WorkspaceRoot: root}, call("read_file", `{"path":"missing"}`))
	if !errors.Is(err, ErrForbidden) {
		t.Fatal("missing path bypassed authorization")
	}
}

func TestDiscoveryContractsMatchDispatch(t *testing.T) {
	root := t.TempDir()
	writeSnapshotFixture(t, root, "note.txt", "a(b)|[c]\nother\n")
	authority := Authority{WorkspaceRoot: root, Permissions: []string{"repository.read"}}
	cases := map[string][]struct {
		args  string
		valid bool
	}{
		"read_file":    {{`{"path":"note.txt","start_line":1,"line_count":2}`, true}, {`{"path":"note.txt","line_count":401}`, false}, {`{"path":"note.txt","line_count":0}`, false}, {`{"path":"note.txt","start_line":null}`, false}, {`{"path":"note.txt","line_count":null}`, false}, {`{"path":"note.txt","end_line":2}`, false}},
		"search_text":  {{`{"text":"a(b)|[c]"}`, true}, {`{"regex":"a"}`, false}, {`{"text":"a","regex":"a"}`, false}, {`{"text":"a","after_lines":2,"result_mode":"count"}`, false}},
		"search_regex": {{`{"regex":"a|other"}`, true}, {`{"text":"a"}`, false}, {`{"regex":"a","text":"a"}`, false}, {`{"regex":"a","max_results":101}`, false}},
	}
	for _, spec := range Available(authority.Permissions) {
		checks, ok := cases[spec.Name]
		if !ok {
			continue
		}
		var raw any
		if err := json.Unmarshal(spec.InputSchema, &raw); err != nil {
			t.Fatal(err)
		}
		compiler := jsonschema.NewCompiler()
		if err := compiler.AddResource("urn:test:discovery", raw); err != nil {
			t.Fatal(err)
		}
		schema, err := compiler.Compile("urn:test:discovery")
		if err != nil {
			t.Fatal(err)
		}
		for _, check := range checks {
			var value any
			_ = json.Unmarshal([]byte(check.args), &value)
			_, hostErr := testHost().execute(t.Context(), authority, call(spec.Name, check.args))
			if (schema.Validate(value) == nil) != check.valid || (hostErr == nil) != check.valid {
				t.Fatalf("%s %s schema/dispatch disagreement: %v", spec.Name, check.args, hostErr)
			}
		}
	}
	literal, err := testHost().execute(t.Context(), authority, call("search_text", `{"text":"a(b)|[c]"}`))
	if err != nil || len(literal.Matches) != 1 || literal.Matches[0].Text != "a(b)|[c]" {
		t.Fatalf("literal punctuation %+v %v", literal, err)
	}
	_, err = testHost().execute(t.Context(), authority, call("search_regex", `{"regex":"a)"}`))
	if !errors.Is(err, ErrInvalidCall) || !strings.Contains(err.Error(), "unexpected )") {
		t.Fatalf("regex diagnosis %v", err)
	}
}
