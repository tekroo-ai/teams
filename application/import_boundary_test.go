package application_test

import (
	"go/build"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tekroo-ai/teams/internal/testpaths"
)

func TestApplicationProductionImportsRemainKernelOnly(t *testing.T) {
	definition, err := build.Default.ImportDir(filepath.Join(testpaths.Root(t), "application"), build.ImportComment)
	if err != nil {
		t.Fatal(err)
	}
	for _, imported := range definition.Imports {
		if strings.HasPrefix(imported, "github.com/tekroo-ai/teams/") && imported != "github.com/tekroo-ai/teams/kernel" {
			t.Fatalf("application production import crosses boundary: %s", imported)
		}
		if strings.Contains(imported, "mongo") || strings.Contains(imported, "provider") {
			t.Fatalf("application production import crosses boundary: %s", imported)
		}
	}
}
