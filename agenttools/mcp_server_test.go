package agenttools

import (
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tekroo-ai/teams/kernel"
)

func TestReadOnlyMCPRoundTripThroughTeamsGateway(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("actual Teams workspace content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	id := kernel.UUIDv7("00000000-0000-7000-8000-000000000951")
	digest := kernel.Digest(hex.EncodeToString(make([]byte, 32)))
	binding := &staticBinding{authority: Authority{WorkspaceRoot: root, Permissions: []string{"repository.read"}}, id: id, digest: digest}
	server, err := NewReadOnlyMCPServer(context.Background(), Gateway{Bindings: binding, Host: Host{Timeout: time.Second}}, id, digest)
	if err != nil {
		t.Fatal(err)
	}
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "openhands-compatible-probe", Version: "0.1.0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()
	listed, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 4 {
		t.Fatalf("read-only MCP tool count = %d, want 4", len(listed.Tools))
	}
	for _, tool := range listed.Tools {
		if !isReadOnlyTool(tool.Name) {
			t.Fatalf("mutating tool exposed: %s", tool.Name)
		}
	}
	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "read_file", Arguments: map[string]any{"path": "note.txt"}})
	if err != nil || result.IsError || len(result.Content) != 1 {
		t.Fatalf("MCP read failed: %+v, %v", result, err)
	}
	content, ok := result.Content[0].(*mcp.TextContent)
	if !ok || !strings.Contains(content.Text, "actual Teams workspace content") || binding.called != 2 {
		t.Fatalf("OpenHands-compatible client did not receive workspace data: %+v", result)
	}
	denied, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "read_file", Arguments: map[string]any{"path": "../outside.txt"}})
	if err != nil || !denied.IsError {
		t.Fatalf("boundary escape was not a tool error: %+v, %v", denied, err)
	}
}
