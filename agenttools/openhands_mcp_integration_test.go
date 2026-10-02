package agenttools

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Set TEKROO_OPENHANDS_PYTHON to the Python interpreter of an installed
// OpenHands SDK to run this opt-in, no-model, loopback integration test.
func TestOpenHandsMCPReadRoundTrip(t *testing.T) {
	python := os.Getenv("TEKROO_OPENHANDS_PYTHON")
	if python == "" {
		t.Skip("OpenHands Python interpreter not supplied")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("TEKROO_NATIVE_READ_PROOF\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	current, grounding := validStartedContext(t, now)
	bindings := ExecutionBindingSource{
		Reader: staleReader{current: current}, Roles: fixedRoles{grounding: grounding},
		Workspaces:   fixedWorkspace{binding: WorkspaceBinding{WorkspaceID: current.Scope.WorkspaceID, WorktreeID: current.Scope.WorktreeID, Root: root}},
		MaximumBytes: 1 << 20, Now: func() time.Time { return now },
	}
	server, err := NewReadOnlyMCPServer(context.Background(), Gateway{Bindings: bindings, Host: Host{Timeout: 5 * time.Second}}, current.Invocation.ID, *current.Invocation.RequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	token, err := newToolCallID()
	if err != nil {
		t.Fatal(err)
	}
	transport := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	endpoint := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		given := request.Header.Get("Authorization")
		want := "Bearer " + token
		if subtle.ConstantTimeCompare([]byte(given), []byte(want)) != 1 {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		transport.ServeHTTP(writer, request)
	}))
	defer endpoint.Close()
	unauthorized, err := http.Get(endpoint.URL)
	if err != nil {
		t.Fatal(err)
	}
	unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated MCP request status = %d, want 401", unauthorized.StatusCode)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, python, "-c", openHandsMCPProbe, endpoint.URL, token)
	command.Env = append(os.Environ(), "OPENHANDS_SUPPRESS_BANNER=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("OpenHands SDK MCP probe failed: %v: %s", err, output)
	}
	var observation struct {
		IsError bool     `json:"is_error"`
		Texts   []string `json:"texts"`
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &observation); err != nil {
		t.Fatalf("OpenHands SDK MCP probe returned non-JSON: %v: %s", err, output)
	}
	if observation.IsError || !strings.Contains(strings.Join(observation.Texts, "\n"), "TEKROO_NATIVE_READ_PROOF") {
		t.Fatalf("OpenHands did not receive Teams workspace content: %+v", observation)
	}
}

const openHandsMCPProbe = `
import json
import sys
from openhands.sdk.mcp.config import MCPServer
from openhands.sdk.mcp.utils import create_mcp_tools

config = {"teams": MCPServer(url=sys.argv[1], transport="http", headers={"Authorization": "Bearer " + sys.argv[2]})}
client = create_mcp_tools(config, timeout=15.0)
try:
    tool = next(tool for tool in client.tools if tool.name == "teams_read_file")
    action = tool.action_from_arguments({"path": "note.txt"})
    observation = tool(action)
    print(json.dumps({"is_error": observation.is_error, "texts": [block.text for block in observation.content if hasattr(block, "text")]}))
finally:
    client.sync_close()
`
