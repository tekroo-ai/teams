package application_test

import (
	"go/build"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestApplicationProductionImportsRemainKernelOnly(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate boundary test")
	}
	definition, err := build.Default.ImportDir(filepath.Dir(file), build.ImportComment)
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
