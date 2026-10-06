package agenttools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/tekroo-ai/teams/kernel"
)

func TestBothGoToolsExecuteRecursiveSubtreeInSandbox(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"adapters/mongo/nested", "unrelated"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeSnapshotFixture(t, root, "go.mod", "module example.test/subtree\n\ngo 1.26.0\n")
	writeSnapshotFixture(t, root, "adapters/mongo/unit.go", "package mongo\nconst Value = 1\n")
	writeSnapshotFixture(t, root, "adapters/mongo/nested/unit.go", "package nested\nconst Value = 2\n")
	// If the selector is broadened accidentally, this package makes it fail.
	writeSnapshotFixture(t, root, "unrelated/broken.go", "not go source\n")
	fixtureGit(t, root, "init", "-q")
	fixtureGit(t, root, "add", "--", ".")
	fixtureGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "baseline")
	host := Host{Timeout: time.Minute}
	permissions := []string{"repository.read", "test.execute"}
	authority := Authority{WorkspaceRoot: root, Permissions: permissions, Purpose: kernel.PurposeImplementation}
	for _, spec := range Available(permissions) {
		if !isGoTestTool(spec.Name) {
			continue
		}
		compiler := jsonschema.NewCompiler()
		var document any
		if err := json.Unmarshal(spec.InputSchema, &document); err != nil {
			t.Fatal(err)
		}
		if err := compiler.AddResource("urn:test:go-package", document); err != nil {
			t.Fatal(err)
		}
		schema, err := compiler.Compile("urn:test:go-package")
		if err != nil {
			t.Fatal(err)
		}
		for _, selector := range []string{"./adapters/mongo/...", "example.test/subtree/adapters/mongo/..."} {
			t.Run(spec.Name+"/"+selector, func(t *testing.T) {
				if err := schema.Validate(map[string]any{"package": selector}); err != nil {
					t.Fatalf("JSON Schema rejected subtree: %v", err)
				}
				encoded, _ := json.Marshal(map[string]string{"package": selector})
				result, err := host.execute(context.Background(), authority, Call{Name: spec.Name, Arguments: encoded})
				if err != nil || result.ExitCode != 0 || !strings.Contains(result.Output, "example.test/subtree/adapters/mongo/nested") || strings.Contains(result.Output, "unrelated") {
					t.Fatalf("bounded recursive execution: %+v %v", result, err)
				}
			})
		}
	}
}
