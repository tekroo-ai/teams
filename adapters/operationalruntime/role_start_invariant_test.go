package operationalruntime

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Every exported ProductionService start/restart variant must finish through
// completeRoleStart. This is intentionally checked structurally: a future API
// variant must not be able to make durable execution registration optional by
// copying only part of the lifecycle sequence.
func TestProductionRoleStartMethodsUseMandatoryCompletionPath(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	directory := filepath.Dir(testFile)
	packages, err := parser.ParseDir(token.NewFileSet(), directory, func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	parsed, ok := packages["operationalruntime"]
	if !ok {
		t.Fatal("operationalruntime package not found")
	}
	found := make(map[string]bool)
	for _, file := range parsed.Files {
		for method, usesCompletionPath := range productionRoleStartCompletionPaths(file) {
			found[method] = usesCompletionPath
		}
	}
	for _, required := range []string{"StartRole", "RestartRole"} {
		if !found[required] {
			t.Errorf("ProductionService.%s must use completeRoleStart", required)
		}
	}
	for method, usesCompletionPath := range found {
		if !usesCompletionPath {
			t.Errorf("ProductionService.%s bypasses completeRoleStart", method)
		}
	}
}

func TestProductionRoleStartInvariantDetectsNewBypass(t *testing.T) {
	parsed, err := parser.ParseFile(token.NewFileSet(), "candidate.go", `package operationalruntime
type ProductionService struct{}
func (service *ProductionService) StartRoleWithName() { service.registerRoleState() }
`, 0)
	if err != nil {
		t.Fatal(err)
	}
	if usesCompletionPath, found := productionRoleStartCompletionPaths(parsed)["StartRoleWithName"]; !found || usesCompletionPath {
		t.Fatalf("new start variant bypass was not detected: found=%t uses_completion_path=%t", found, usesCompletionPath)
	}
}

func productionRoleStartCompletionPaths(parsed *ast.File) map[string]bool {
	found := make(map[string]bool)
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || !productionServiceReceiver(function) || function.Name == nil ||
			(!strings.HasPrefix(function.Name.Name, "StartRole") && !strings.HasPrefix(function.Name.Name, "RestartRole")) {
			continue
		}
		usesCompletionPath := false
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if ok && selector.Sel.Name == "completeRoleStart" {
				usesCompletionPath = true
			}
			return true
		})
		found[function.Name.Name] = usesCompletionPath
	}
	return found
}

func productionServiceReceiver(function *ast.FuncDecl) bool {
	if function.Recv == nil || len(function.Recv.List) != 1 {
		return false
	}
	receiver := function.Recv.List[0].Type
	if pointer, ok := receiver.(*ast.StarExpr); ok {
		receiver = pointer.X
	}
	identifier, ok := receiver.(*ast.Ident)
	return ok && identifier.Name == "ProductionService"
}
