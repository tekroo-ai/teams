package protocol_test

import (
	"go/build"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestThinAdapterProductionImportsRemainWithinBoundary(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate boundary test")
	}
	adaptersRoot := filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
	allowed := map[string]map[string]bool{
		"protocol": {"github.com/tekroo-ai/teams/kernel": true},
		"stdio":    {"github.com/tekroo-ai/teams/adapters/protocol": true},
		"daemon":   {"github.com/tekroo-ai/teams/adapters/stdio": true},
		"httpapi":  {"github.com/tekroo-ai/teams/adapters/protocol": true},
		"mcp": {
			"github.com/tekroo-ai/teams/adapters/httpapi":  true,
			"github.com/tekroo-ai/teams/adapters/protocol": true,
		},
		"cli": {
			"github.com/tekroo-ai/teams/adapters/daemon":   true,
			"github.com/tekroo-ai/teams/adapters/protocol": true,
			"github.com/tekroo-ai/teams/adapters/stdio":    true,
		},
	}
	for packageName, internalAllowed := range allowed {
		t.Run(packageName, func(t *testing.T) {
			definition, err := build.Default.ImportDir(filepath.Join(adaptersRoot, packageName), build.ImportComment)
			if err != nil {
				t.Fatal(err)
			}
			var rejected []string
			for _, imported := range definition.Imports {
				if strings.HasPrefix(imported, "github.com/tekroo-ai/teams/") && !internalAllowed[imported] {
					rejected = append(rejected, imported)
				}
				if strings.Contains(imported, "mongo") || strings.Contains(imported, "persistence") {
					rejected = append(rejected, imported)
				}
			}
			sort.Strings(rejected)
			if len(rejected) != 0 {
				t.Fatalf("production imports cross the thin-adapter boundary: %v", rejected)
			}
		})
	}
}
