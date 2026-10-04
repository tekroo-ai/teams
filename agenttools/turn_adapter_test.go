package agenttools

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/kernel"
)

func TestReadOnlyTurnAdapterUsesBoundGatewayAndCanonicalSchema(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	id := kernel.UUIDv7("00000000-0000-7000-8000-000000000951")
	digest := kernel.Digest(hex.EncodeToString(make([]byte, 32)))
	binding := &staticBinding{authority: Authority{WorkspaceRoot: root, Permissions: []string{"repository.read"}}, id: id, digest: digest}
	adapter := ReadOnlyTurnAdapter{Gateway: Gateway{Bindings: binding, Host: Host{Timeout: time.Second}}, InvocationID: id, RequestDigest: digest}
	output, err := adapter.ExecuteReadOnly(context.Background(), agentruntime.ToolCall{ID: "call-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"note.txt"}`)})
	var result Result
	if err != nil || json.Unmarshal(output, &result) != nil || result.Output != "hello" || binding.called != 1 {
		t.Fatalf("bound read: output=%s err=%v calls=%d", output, err, binding.called)
	}
	if _, err := adapter.ExecuteReadOnly(context.Background(), agentruntime.ToolCall{ID: "call-2", Name: "write_file", Arguments: json.RawMessage(`{}`)}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("mutation crossed read-only adapter: %v", err)
	}
	definitions := ReadOnlyDefinitions([]string{"repository.edit", "test.execute"})
	if len(definitions) == 0 {
		t.Fatal("no read-only model-facing definitions")
	}
	for _, definition := range definitions {
		if definition.Name == "write_file" || isGoTestTool(definition.Name) || !json.Valid(definition.Parameters) {
			t.Fatalf("unsafe model-facing definition: %+v", definition)
		}
	}
}

func TestTestDefinitionsExposeBothIsolatedPaths(t *testing.T) {
	definitions := TestDefinitions([]string{"repository.read", "test.execute"})
	if len(definitions) != 2 || definitions[0].Name != "run_go_tests" || definitions[1].Name != "run_go_tests_worktree" {
		t.Fatalf("test tool definitions: %+v", definitions)
	}
	for _, definition := range definitions {
		if !json.Valid(definition.Parameters) {
			t.Fatalf("invalid test schema: %s", definition.Name)
		}
	}
	if len(TestDefinitions(nil)) != 0 {
		t.Fatal("unprivileged test definitions exposed")
	}
}
