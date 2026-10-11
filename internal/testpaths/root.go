// Package testpaths locates test fixtures in the executing source snapshot.
// It must only be imported by tests, never by production kernel code.
package testpaths

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const module = "github.com/tekroo-ai/teams"

// Root avoids runtime.Caller: -trimpath records module-relative compiler paths,
// not filesystem paths. Go runs each package's tests from its source directory.
func Root(t testing.TB) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := rootFrom(wd)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func rootFrom(directory string) (string, error) {
	directory, err := filepath.Abs(directory)
	if err != nil {
		return "", err
	}
	for {
		data, err := os.ReadFile(filepath.Join(directory, "go.mod"))
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				fields := strings.Fields(line)
				if len(fields) >= 2 && fields[0] == "module" {
					if fields[1] == module {
						return directory, nil
					}
					return "", fmt.Errorf("test snapshot module %q, expected %q", fields[1], module)
				}
			}
			return "", fmt.Errorf("test snapshot has no module directive: %s", directory)
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("locate test snapshot: %w", err)
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", fmt.Errorf("test snapshot root not found")
		}
		directory = parent
	}
}
