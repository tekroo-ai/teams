package application

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strings"
	"testing"
)

func TestGoDeclarationGuidanceContainsParseableBodylessManifest(t *testing.T) {
	if !slices.Contains(readOnlyExecutionGuidance, GoDeclarationManifestGuidance) {
		t.Fatal("live read-only briefs lack the qualified guidance")
	}
	_, block, found := strings.Cut(GoDeclarationManifestGuidance, "```go\n")
	if !found {
		t.Fatal("missing exact syntax example")
	}
	block, _, found = strings.Cut(block, "```")
	if !found || !strings.HasPrefix(block, "// path: organization/new_alias.go\n") {
		t.Fatal("missing unannotated declaration path")
	}
	file, err := parser.ParseFile(token.NewFileSet(), "new_alias.go", block, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal("model-facing example is invalid Go: ", err)
	}
	var signatures int
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			signatures++
			if fn.Body != nil {
				t.Fatal("example contains an implementation body")
			}
		}
	}
	if signatures != 1 {
		t.Fatal("example does not demonstrate an omitted function body")
	}
	for _, phrase := range []string{"// modify: organization/existing.go", "cannot satisfy a new file's declaration requirement", "may_author_validates=false", "zero-based indexes of earlier tasks", "retained draft and repository evidence", "Actual IMPLEMENTATION tasks must still deliver working function bodies", "Do not require bodyless functions in the implementation files"} {
		if !strings.Contains(GoDeclarationManifestGuidance, phrase) {
			t.Fatalf("missing instruction %q", phrase)
		}
	}
}
