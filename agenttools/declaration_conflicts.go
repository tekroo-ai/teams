package agenttools

import (
	"context"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/tekroo-ai/teams/internal/validation"
)

// ProposedGoSource is explicit Go syntax, never names inferred from prose.
// Existing source is inspected but never edited or executed.
type ProposedGoSource struct{ Path, Source string }

// CheckNewGoDeclarations rejects new package-level names already present in the
// owning package (including private names), or in another proposed source file.
// It does not type-check signatures or certify behavior. Methods have their own
// receiver namespace and are not mistaken for package-level declarations.
func CheckNewGoDeclarations(ctx context.Context, root string, proposals []ProposedGoSource) error {
	root, err := canonicalRoot(root)
	if err != nil {
		return err
	}
	if len(proposals) > 256 {
		return ErrTooLarge
	}
	type packageNames struct {
		names       map[string]SymbolLocation
		packageName string
	}
	packages := map[string]*packageNames{}
	seenPaths := map[string]string{}
	total := 0
	var findings validation.Findings
	for _, proposal := range proposals {
		if findings.Full() {
			break
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := safeMutationPath(proposal.Path); err != nil {
			findings.Add(fmt.Errorf("declaration path %q: %w", proposal.Path, err))
			continue
		}
		if filepath.Ext(proposal.Path) != ".go" || strings.HasSuffix(proposal.Path, "_test.go") {
			findings.Add(fmt.Errorf("declaration check requires non-test Go source %q: %w", proposal.Path, ErrInvalidCall))
			continue
		}
		total += len(proposal.Source)
		if total > 2<<20 {
			return ErrTooLarge
		}
		if old, exists := seenPaths[proposal.Path]; exists {
			if old != proposal.Source {
				findings.Add(fmt.Errorf("conflicting proposed source for %q: %w", proposal.Path, ErrConflict))
			}
			continue
		}
		seenPaths[proposal.Path] = proposal.Source
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, proposal.Path, proposal.Source, parser.SkipObjectResolution)
		if err != nil {
			findings.Add(fmt.Errorf("proposed Go declarations %q: %w; use a package clause, complete type/var/const syntax and bodyless function signatures; do not use { ... }", proposal.Path, err))
			continue
		}
		directory := filepath.Dir(proposal.Path)
		key := directory + "/" + file.Name.Name
		group := packages[key]
		if group == nil {
			group = &packageNames{names: map[string]SymbolLocation{}, packageName: file.Name.Name}
			packages[key] = group
			absolute, resolveErr := existingPath(root, directory)
			if resolveErr != nil && !os.IsNotExist(resolveErr) {
				return resolveErr
			}
			if os.IsNotExist(resolveErr) {
				if err := checkMissingFilePath(root, proposal.Path); err != nil {
					return err
				}
			}
			if resolveErr == nil {
				entries, err := os.ReadDir(absolute)
				if err != nil {
					return err
				}
				for _, entry := range entries {
					if err := ctx.Err(); err != nil {
						return err
					}
					if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
						continue
					}
					path := filepath.Join(directory, entry.Name())
					info, err := entry.Info()
					if err != nil {
						return err
					}
					if !info.Mode().IsRegular() {
						return fmt.Errorf("cannot inspect declaration source %q: %w", path, ErrBoundary)
					}
					if info.Size() > 1<<20 {
						return ErrTooLarge
					}
					buildContext := build.Default
					buildContext.CgoEnabled = false
					included, err := buildContext.MatchFile(absolute, entry.Name())
					if err != nil {
						return err
					}
					if !included {
						continue
					}
					raw, err := os.ReadFile(filepath.Join(absolute, entry.Name()))
					if err != nil {
						return err
					}
					existing, err := parser.ParseFile(fset, path, raw, parser.SkipObjectResolution)
					if err != nil {
						return fmt.Errorf("cannot certify declaration inventory %q: %w", path, err)
					}
					if existing.Name.Name != file.Name.Name {
						continue
					}
					for _, name := range packageIdentifiers(existing) {
						position := fset.Position(name.Pos())
						group.names[name.Name] = SymbolLocation{Path: path, Line: position.Line, Symbol: name.Name}
					}
				}
			}
		}
		for _, name := range packageIdentifiers(file) {
			if previous, found := group.names[name.Name]; found {
				findings.Add(fmt.Errorf("new declaration %s in %s conflicts with %s:%d; choose a distinct name or explicitly modify/reuse the existing declaration: %w", name.Name, proposal.Path, previous.Path, previous.Line, ErrConflict))
				if findings.Full() {
					break
				}
				continue
			}
			position := fset.Position(name.Pos())
			group.names[name.Name] = SymbolLocation{Path: proposal.Path, Line: position.Line, Symbol: name.Name}
		}
	}
	return findings.Err()
}

func packageIdentifiers(file *ast.File) []*ast.Ident {
	var names []*ast.Ident
	for _, declaration := range file.Decls {
		switch node := declaration.(type) {
		case *ast.FuncDecl:
			if node.Recv == nil && node.Name.Name != "init" && node.Name.Name != "_" {
				names = append(names, node.Name)
			}
		case *ast.GenDecl:
			for _, spec := range node.Specs {
				switch item := spec.(type) {
				case *ast.TypeSpec:
					if item.Name.Name != "_" {
						names = append(names, item.Name)
					}
				case *ast.ValueSpec:
					for _, name := range item.Names {
						if name.Name != "_" {
							names = append(names, name)
						}
					}
				}
			}
		}
	}
	return names
}
