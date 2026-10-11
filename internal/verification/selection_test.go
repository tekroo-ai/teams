package verification

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRecursiveSelectionRequiresCompleteSinglePackageInventory(t *testing.T) {
	for _, test := range []struct {
		name        string
		args, files []string
		want        Class
	}{
		{"one", []string{"go", "test", "./adapters/operator/..."}, []string{"go.mod", "adapters/operator/service.go", "adapters/operator/service_test.go"}, Focused},
		{"missing", []string{"go", "test", "./adapters/operator/..."}, nil, WholeModule},
		{"nested", []string{"go", "test", "./adapters/operator/..."}, []string{"adapters/operator/service.go", "adapters/operator/child/child.go"}, WholeModule},
		{"excluded", []string{"go", "test", "./adapters/operator/..."}, []string{"adapters/operator/service.go", "adapters/operator/testdata/example.go", "adapters/operator/_hidden/example.go", "adapters/operator/vendor/x/x.go", "adapters/operator/other/go.mod", "adapters/operator/other/x.go"}, Focused},
		{"root", []string{"go", "test", "./..."}, []string{"service.go"}, WholeModule},
		{"external", []string{"go", "test", "example.test/operator/..."}, []string{"operator/x.go"}, WholeModule},
		{"multiple", []string{"go", "test", "./adapters/operator/...", "./kernel"}, []string{"adapters/operator/service.go"}, WholeModule},
		{"flag-value", []string{"go", "test", "-run", "./adapters/operator/...", "./..."}, []string{"adapters/operator/service.go"}, WholeModule},
		{"overlay", []string{"go", "test", "-overlay=elsewhere.json", "./adapters/operator/..."}, []string{"adapters/operator/service.go"}, WholeModule},
		{"coverage", []string{"go", "test", "-coverpkg", "./...", "./adapters/operator/..."}, []string{"adapters/operator/service.go"}, WholeModule},
		{"escape", []string{"go", "test", "./../operator/..."}, []string{"../operator/x.go"}, WholeModule},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := append([]string(nil), test.args...)
			if got := CommandClassWithInventory(test.args, test.files); got != test.want {
				t.Fatalf("class=%v want=%v", got, test.want)
			}
			if !reflect.DeepEqual(before, test.args) {
				t.Fatal("classification changed execution selection")
			}
		})
	}
}

func TestFilesystemClassificationBoundedAndConservative(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "operator")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x_test.go"), []byte("package operator"), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"go", "test", "./operator/..."}
	if got := CommandClassAt(t.Context(), root, args); got != Focused {
		t.Fatal(got)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got := CommandClassAt(ctx, root, args); got != WholeModule {
		t.Fatal("cancelled inventory qualified")
	}
	if got := CommandClassAt(t.Context(), root, nil); got != WholeModule {
		t.Fatal(got)
	}
	if got := CommandClassAt(t.Context(), "nonexistent", []string{"go", "test", "./..."}); got != WholeModule {
		t.Fatal(got)
	}
	if err := os.Symlink(dir, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if got := CommandClassAt(t.Context(), root, args); got != WholeModule {
		t.Fatal("incomplete symlink inventory qualified")
	}
}
