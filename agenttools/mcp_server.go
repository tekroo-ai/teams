package agenttools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tekroo-ai/teams/kernel"
)

// readOnlySession is derived from Teams' persisted invocation and role
// authority. It is fixed when the MCP server is created; no model-supplied
// argument can select another invocation, digest, workspace, or permission set.
type readOnlySession struct {
	InvocationID  kernel.UUIDv7
	RequestDigest kernel.Digest
	Permissions   []string
}

// NewReadOnlyMCPServer derives its exposed tool surface from persisted Teams
// authority. The transport is supplied separately; this function opens no
// listener and does not register any mutating tool.
func NewReadOnlyMCPServer(ctx context.Context, gateway Gateway, invocationID kernel.UUIDv7, requestDigest kernel.Digest) (*mcp.Server, error) {
	if gateway.Bindings == nil || gateway.Host.Timeout <= 0 || !invocationID.Valid() || !requestDigest.Valid() {
		return nil, ErrInvalidCall
	}
	operation, cancel := context.WithTimeout(ctx, gateway.Host.Timeout)
	defer cancel()
	authority, err := gateway.Bindings.BindToolInvocation(operation, invocationID, requestDigest)
	if err != nil {
		return nil, err
	}
	session := readOnlySession{InvocationID: invocationID, RequestDigest: requestDigest, Permissions: authority.Permissions}
	server := mcp.NewServer(&mcp.Implementation{Name: "tekroo-agent-tools", Version: "0.1.0"}, nil)
	for _, spec := range specs {
		if !isReadOnlyTool(spec.Name) || !permitted(session.Permissions, spec.Permission) {
			continue
		}
		name := spec.Name
		mcp.AddTool(server, &mcp.Tool{
			Name: name, Description: spec.Description,
			InputSchema: json.RawMessage(spec.InputSchema),
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
		}, func(ctx context.Context, _ *mcp.CallToolRequest, input map[string]any) (*mcp.CallToolResult, any, error) {
			arguments, err := json.Marshal(input)
			if err != nil {
				return nil, nil, err
			}
			callID, err := newToolCallID()
			if err != nil {
				return nil, nil, err
			}
			receipt, err := gateway.ExecuteReadOnly(ctx, Request{
				InvocationID: session.InvocationID, RequestDigest: session.RequestDigest,
				ToolCallID: callID, Call: Call{Name: name, Arguments: arguments},
			})
			if err != nil {
				return &mcp.CallToolResult{
					IsError: true,
					Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("%s: %v", name, err)}},
				}, nil, nil
			}
			encoded, err := json.Marshal(receipt.Result)
			if err != nil {
				return nil, nil, err
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}}, nil, nil
		})
	}
	return server, nil
}

func isReadOnlyTool(name string) bool {
	switch name {
	case "read_file", "list_files", "find_files", "search_file_contents", "git_status", "git_diff", "git_log", "git_show", "git_check_ignore":
		return true
	default:
		return false
	}
}

func newToolCallID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", errors.Join(ErrInvalidCall, err)
	}
	return hex.EncodeToString(id[:]), nil
}
