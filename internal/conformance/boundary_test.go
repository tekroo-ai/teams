package conformance_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestKernelProductionImportsRemainProviderNeutral(t *testing.T) {
	repositoryRoot := locateRepositoryRoot(t)
	kernelRoot := filepath.Join(repositoryRoot, "kernel")
	entries, err := os.ReadDir(kernelRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(kernelRoot, entry.Name())
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", path, parseErr)
		}
		for _, imported := range file.Imports {
			name, unquoteErr := strconv.Unquote(imported.Path.Value)
			if unquoteErr != nil {
				t.Fatalf("decode import in %s: %v", path, unquoteErr)
			}
			if forbiddenKernelImport(name) {
				t.Errorf("provider-neutral kernel imports forbidden package %q in %s", name, entry.Name())
			}
		}
	}
}

func forbiddenKernelImport(name string) bool {
	for _, prefix := range []string{"net", "os", "path/filepath", "io/fs", "database/sql", "go.mongodb.org", "github.com/", "gitlab.com/"} {
		if name == prefix || strings.HasPrefix(name, prefix+"/") {
			return true
		}
	}
	return false
}
