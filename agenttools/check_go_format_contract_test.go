package agenttools

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestCheckGoFormatContractRejectsProsePaths(t *testing.T) {
	var raw []byte
	for _, spec := range Available([]string{"repository.read"}) {
		if spec.Name == "check_go_format" {
			raw = spec.InputSchema
		}
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("urn:format-check", doc); err != nil {
		t.Fatal(err)
	}
	schema, err := c.Compile("urn:format-check")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path  string
		valid bool
	}{{"placeholder", false}, {"criterion text", false}, {"a.txt", false}, {"a.go", true}, {"nested/a_test.go", true}} {
		err := schema.Validate(map[string]any{"paths": []any{tc.path}})
		if (err == nil) != tc.valid {
			t.Errorf("path %q: %v", tc.path, err)
		}
	}
}

func TestCheckGoFormatDiagnosticPreservesErrorClass(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "directory.go"), 0700); err != nil {
		t.Fatal(err)
	}
	a := Authority{WorkspaceRoot: root, Permissions: []string{"repository.read"}}
	for _, tc := range []struct {
		args, diagnostic string
		class            error
	}{
		{`{"paths":["placeholder"]}`, `paths[0]="placeholder": expected an existing workspace-relative Go file ending in .go`, ErrInvalidCall},
		{`{"paths":["a.go","a.go"]}`, `paths[1]="a.go": duplicate path`, ErrInvalidCall},
		{`{"paths":["missing.go"]}`, `paths[0]="missing.go": cannot resolve existing workspace file`, os.ErrNotExist},
		{`{"paths":["directory.go"]}`, `paths[0]="directory.go": expected a regular file`, ErrInvalidCall},
		{`{"paths":[]}`, `expected 1..32 paths`, ErrInvalidCall},
		{`{"paths":["../outside.go"]}`, `paths[0]="../outside.go"`, ErrBoundary},
	} {
		_, err := testHost().execute(context.Background(), a, call("check_go_format", tc.args))
		if !errors.Is(err, tc.class) || !strings.Contains(err.Error(), tc.diagnostic) {
			t.Errorf("%s: %v", tc.args, err)
		}
	}
}
