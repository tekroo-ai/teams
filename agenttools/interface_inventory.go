package agenttools

import (
	"go/ast"
	"go/parser"
	"go/token"
)

// ExportedGoDeclarations describes parsed source, not an inferred guarantee of
// behavior. Callers bind it to the exact immutable candidate commit/tree.
func ExportedGoDeclarations(path string, content []byte) ([]SymbolLocation, error) {
	if len(content) > 1<<20 || safeMutationPath(path) != nil {
		return nil, ErrBoundary
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, content, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var result []SymbolLocation
	add := func(name *ast.Ident, node ast.Node) {
		if name.IsExported() {
			p := fset.Position(name.Pos())
			result = append(result, SymbolLocation{Path: path, Line: p.Line, Column: p.Column, Symbol: name.Name, Signature: signature(fset, node)})
		}
	}
	for _, decl := range file.Decls {
		switch node := decl.(type) {
		case *ast.FuncDecl:
			copied := *node
			copied.Body = nil
			add(node.Name, &copied)
		case *ast.GenDecl:
			for _, spec := range node.Specs {
				switch item := spec.(type) {
				case *ast.TypeSpec:
					add(item.Name, item)
				case *ast.ValueSpec:
					for _, name := range item.Names {
						add(name, item)
					}
				}
			}
		}
	}
	if len(result) > 256 {
		return nil, ErrTooLarge
	}
	return result, nil
}
