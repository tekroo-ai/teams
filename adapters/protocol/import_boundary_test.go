package protocol_test

import (
	"go/build"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/tekroo-ai/teams/internal/testpaths"
)

func TestThinAdapterProductionImportsRemainWithinBoundary(t *testing.T) {
	adaptersRoot := filepath.Join(testpaths.Root(t), "adapters")
	allowed := map[string]map[string]bool{
		"protocol": {"github.com/tekroo-ai/teams/kernel": true},
		"channel": {
			"github.com/tekroo-ai/teams/adapters/protocol": true,
			"github.com/tekroo-ai/teams/kernel":            true,
		},
		"stdio":   {"github.com/tekroo-ai/teams/adapters/protocol": true},
		"daemon":  {"github.com/tekroo-ai/teams/adapters/stdio": true},
		"httpapi": {"github.com/tekroo-ai/teams/adapters/protocol": true},
		"eventexporthttp": {
			"github.com/tekroo-ai/teams/adapters/httpapi":  true,
			"github.com/tekroo-ai/teams/adapters/protocol": true,
			"github.com/tekroo-ai/teams/eventexport":       true,
		},
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
