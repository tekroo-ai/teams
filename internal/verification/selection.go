package verification

import (
	"context"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
)

// Inventory is bounded and metadata-only. Extra build-tagged files can only
// make classification more conservative; execution keeps the original selector.
func CommandClassAt(ctx context.Context, root string, argv []string) Class {
	if len(argv) < 3 {
		return WholeModule
	}
	if class := CommandClass(argv); class == Focused {
		return class
	}
	// Root suites and ambiguous commands need no filesystem scan at all.
	eligible := false
	for _, arg := range argv[2:] {
		if strings.HasPrefix(arg, "./") && arg != "./..." && strings.HasSuffix(arg, "/...") {
			probe := strings.TrimSuffix(strings.TrimPrefix(arg, "./"), "/...") + "/probe.go"
			eligible = CommandClassWithInventory(argv, []string{probe}) == Focused
			break
		}
	}
	if !eligible {
		return WholeModule
	}
	var files []string
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		if entry.IsDir() && rel != "." && ignoredDirectory(entry.Name()) {
			return filepath.SkipDir
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fs.ErrInvalid // A link cannot prove a single physical package.
		}
		if !entry.IsDir() {
			files = append(files, filepath.ToSlash(rel))
			if len(files) > 20000 {
				return fs.ErrInvalid
			}
		}
		return nil
	})
	if err != nil {
		return WholeModule
	}
	return CommandClassWithInventory(argv, files)
}

func ignoredDirectory(name string) bool {
	return name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

func singleRecursivePackage(selector string, files []string) bool {
	prefix := strings.TrimSuffix(strings.TrimPrefix(selector, "./"), "/...")
	if prefix == "" || path.Clean(prefix) != prefix || strings.HasPrefix(prefix, "../") {
		return false
	}
	// Go excludes nested modules from ./...; recognizing their go.mod files
	// avoids counting them as runnable packages in the surrounding module.
	modules := map[string]bool{}
	for _, file := range files {
		if path.Base(file) == "go.mod" && path.Dir(file) != "." {
			modules[path.Dir(file)] = true
		}
	}
	packageDir := ""
	for _, file := range files {
		if !strings.HasPrefix(file, prefix+"/") || !strings.HasSuffix(file, ".go") || strings.HasPrefix(path.Base(file), ".") || strings.HasPrefix(path.Base(file), "_") {
			continue
		}
		dir := path.Dir(file)
		excluded := false
		for parent := dir; parent != "."; parent = path.Dir(parent) {
			if ignoredDirectory(path.Base(parent)) || modules[parent] {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}
		if packageDir != "" && packageDir != dir {
			return false
		}
		packageDir = dir
	}
	return packageDir != ""
}
