package agenttools

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/build"
	"go/format"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type SymbolLocation struct {
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	Symbol    string `json:"symbol"`
	Signature string `json:"signature,omitempty"`
}

var symbolName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type parsedSymbolPackage struct {
	path     string
	files    []*ast.File
	info     *types.Info
	pkg      *types.Package
	checking bool
}
type symbolIndex struct {
	ctx          context.Context
	root, module string
	fset         *token.FileSet
	groups       map[string]*parsedSymbolPackage
	unresolved   []string
	standard     types.Importer
}

func (host Host) findSymbols(ctx context.Context, root string, call Call) (Result, error) {
	result := Result{Name: call.Name}
	canonical, err := canonicalRoot(root)
	if err != nil {
		return result, err
	}
	root = canonical
	var args struct {
		Symbol    string `json:"symbol"`
		Directory string `json:"directory"`
		Path      string `json:"path"`
		Line      int    `json:"line"`
	}
	if decode(call.Arguments, &args) != nil {
		return result, ErrInvalidCall
	}
	if call.Name == "find_definitions" {
		if !symbolName.MatchString(args.Symbol) || args.Path != "" || args.Line != 0 {
			return result, ErrInvalidCall
		}
	} else if args.Path == "" || args.Line < 1 || args.Symbol != "" || args.Directory != "" {
		return result, ErrInvalidCall
	}
	directory := root
	if args.Directory != "" {
		var err error
		directory, err = existingPath(root, args.Directory)
		if err != nil {
			return result, err
		}
		info, err := os.Stat(directory)
		if err != nil || !info.IsDir() {
			return result, ErrBoundary
		}
	}
	if call.Name == "find_references" {
		if _, err := existingPath(root, args.Path); err != nil {
			return result, err
		}
	}
	index := &symbolIndex{ctx: ctx, root: root, fset: token.NewFileSet(), groups: map[string]*parsedSymbolPackage{}, standard: importer.Default()}
	moduleBytes, _, _, err := readFile(root, "go.mod", 1, 100)
	if err == nil {
		for _, line := range strings.Split(moduleBytes, "\n") {
			if fields := strings.Fields(line); len(fields) == 2 && fields[0] == "module" {
				index.module = strings.Trim(fields[1], `"`)
				break
			}
		}
	}
	total := 0
	err = walkWorkspaceFiles(ctx, root, directory, func(absolute, relative string, entry os.DirEntry) error {
		if !strings.HasSuffix(relative, ".go") || strings.HasPrefix(relative, "vendor/") || strings.Contains(relative, "/testdata/") {
			return nil
		}
		buildContext := build.Default
		buildContext.CgoEnabled = false // Match the qualified Go tool sandbox.
		included, matchErr := buildContext.MatchFile(filepath.Dir(absolute), filepath.Base(absolute))
		if matchErr != nil {
			index.unresolved = append(index.unresolved, relative+": build constraints invalid")
			return nil
		}
		if !included {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > 1<<20 {
			index.unresolved = append(index.unresolved, relative+": file too large")
			return nil
		}
		total += int(info.Size())
		if total > 32<<20 {
			return ErrTooLarge
		}
		content, err := os.ReadFile(absolute)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(index.fset, relative, content, parser.SkipObjectResolution)
		if err != nil {
			index.unresolved = append(index.unresolved, relative+": parse failed")
			return nil
		}
		key := filepath.ToSlash(filepath.Dir(relative))
		if strings.HasSuffix(file.Name.Name, "_test") {
			index.unresolved = append(index.unresolved, relative+": external test package not indexed")
			return nil
		}
		group := index.groups[key]
		if group == nil {
			path := index.module
			if key != "." {
				path += "/" + key
			}
			group = &parsedSymbolPackage{path: path}
			index.groups[key] = group
		}
		group.files = append(group.files, file)
		return nil
	})
	if err != nil {
		return result, err
	}
	keys := make([]string, 0, len(index.groups))
	for key := range index.groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if call.Name == "find_definitions" {
		for _, key := range keys {
			for _, file := range index.groups[key].files {
				for _, decl := range file.Decls {
					switch node := decl.(type) {
					case *ast.FuncDecl:
						if node.Name.Name == args.Symbol {
							copy := *node
							copy.Body = nil
							result.Definitions = append(result.Definitions, index.location(node.Name, signature(index.fset, &copy)))
						}
					case *ast.GenDecl:
						for _, spec := range node.Specs {
							switch item := spec.(type) {
							case *ast.TypeSpec:
								if item.Name.Name == args.Symbol {
									result.Definitions = append(result.Definitions, index.location(item.Name, signature(index.fset, item)))
								}
							case *ast.ValueSpec:
								for _, name := range item.Names {
									if name.Name == args.Symbol {
										result.Definitions = append(result.Definitions, index.location(name, signature(index.fset, item)))
									}
								}
							}
						}
					}
				}
			}
		}
	} else {
		key := filepath.ToSlash(filepath.Dir(args.Path))
		group := index.groups[key]
		if group == nil {
			return result, fmt.Errorf("%w: no parsed Go package at %s", ErrInvalidCall, args.Path)
		}
		index.check(group)
		var target types.Object
		for ident, object := range group.info.Defs {
			if object == nil {
				continue
			}
			packageDeclaration := object.Parent() == group.pkg.Scope()
			if signature, ok := object.Type().(*types.Signature); ok && signature.Recv() != nil {
				packageDeclaration = true
			}
			if !packageDeclaration {
				continue
			}
			position := index.fset.Position(ident.Pos())
			if object != nil && position.Filename == args.Path && position.Line == args.Line {
				if target != nil {
					return result, fmt.Errorf("%w: line has multiple declarations", ErrInvalidCall)
				}
				target = object
			}
		}
		if target == nil {
			return result, fmt.Errorf("%w: path/line must identify a resolved declaration", ErrInvalidCall)
		}
		for _, key := range keys {
			index.check(index.groups[key])
			for ident, object := range index.groups[key].info.Uses {
				if object == target {
					result.References = append(result.References, index.location(ident, ""))
				}
			}
		}
	}
	sortLocations(result.Definitions)
	sortLocations(result.References)
	if len(result.Definitions) > 100 {
		result.Definitions = result.Definitions[:100]
		result.Truncated = true
	}
	if len(result.References) > 100 {
		result.References = result.References[:100]
		result.Truncated = true
	}
	result.Unresolved = index.unresolved
	sort.Strings(result.Unresolved)
	if len(result.Unresolved) > 100 {
		result.Unresolved = result.Unresolved[:100]
		result.Truncated = true
	}
	return result, ctx.Err()
}

func (index *symbolIndex) location(ident *ast.Ident, signature string) SymbolLocation {
	p := index.fset.Position(ident.Pos())
	return SymbolLocation{p.Filename, p.Line, p.Column, ident.Name, signature}
}
func signature(fset *token.FileSet, node ast.Node) string {
	var out bytes.Buffer
	_ = format.Node(&out, fset, node)
	text := out.String()
	if len(text) > 1024 {
		text = text[:1024] + "…"
	}
	return text
}
func sortLocations(locations []SymbolLocation) {
	sort.Slice(locations, func(i, j int) bool {
		if locations[i].Path != locations[j].Path {
			return locations[i].Path < locations[j].Path
		}
		if locations[i].Line != locations[j].Line {
			return locations[i].Line < locations[j].Line
		}
		return locations[i].Column < locations[j].Column
	})
}

func (index *symbolIndex) check(group *parsedSymbolPackage) {
	if group.info != nil {
		return
	}
	group.info = &types.Info{Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}}
	if index.ctx.Err() != nil {
		return
	}
	group.checking = true
	defer func() { group.checking = false }()
	config := types.Config{Importer: index, Error: func(err error) {
		if len(index.unresolved) < 100 {
			index.unresolved = append(index.unresolved, err.Error())
		}
	}}
	group.pkg, _ = config.Check(group.path, index.fset, group.files, group.info)
}

func (index *symbolIndex) Import(path string) (*types.Package, error) {
	if err := index.ctx.Err(); err != nil {
		return nil, err
	}
	if path == index.module || strings.HasPrefix(path, index.module+"/") {
		key := strings.TrimPrefix(strings.TrimPrefix(path, index.module), "/")
		if key == "" {
			key = "."
		}
		group := index.groups[key]
		if group == nil || group.checking {
			return nil, fmt.Errorf("package %s unresolved in workspace index", path)
		}
		index.check(group)
		if group.pkg == nil {
			return nil, fmt.Errorf("package %s did not type-check", path)
		}
		return group.pkg, nil
	}
	// External modules are never loaded by executing candidate commands.
	if strings.Contains(strings.Split(path, "/")[0], ".") {
		return nil, fmt.Errorf("external package %s not resolved", path)
	}
	return index.standard.Import(path)
}
