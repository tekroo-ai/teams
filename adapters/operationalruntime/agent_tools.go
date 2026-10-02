package operationalruntime

import (
	"context"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tekroo-ai/teams/adapters/openhands"
	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

type toolSessionAdapter struct{ service *agenttools.HTTPService }

func (adapter toolSessionAdapter) IssueReadOnlyToolSession(ctx context.Context, id kernel.UUIDv7, digest kernel.Digest) (openhands.ReadOnlyToolSession, error) {
	session, err := adapter.service.IssueReadOnlySession(ctx, id, digest)
	if err != nil {
		return openhands.ReadOnlyToolSession{}, err
	}
	return openhands.ReadOnlyToolSession{URL: session.URL, BearerToken: session.BearerToken}, nil
}

// toolWorkspaceAdapter reuses the existing verified workspace registry during
// the transition to Teams-native tool execution. OpenHands is not called to
// execute the tool; its resolver currently owns the configured path binding.
type toolWorkspaceAdapter struct {
	resolver openhands.WorkspaceResolver
}

func (adapter toolWorkspaceAdapter) ResolveToolWorkspace(ctx context.Context, scope kernel.TaskOperationalScope) (agenttools.WorkspaceBinding, error) {
	binding, err := adapter.resolver.ResolveWorkspace(ctx, scope)
	if err != nil {
		return agenttools.WorkspaceBinding{}, err
	}
	return agenttools.WorkspaceBinding{
		WorkspaceID: binding.WorkspaceID,
		WorktreeID:  binding.WorktreeID,
		Root:        binding.WorkingDirectory,
		ReadOnly:    binding.Candidate != nil && binding.Candidate.ReadOnly,
	}, nil
}

// ExecuteAgentToolReadOnly is the trusted in-process boundary. The HTTP/MCP
// transport separately authenticates its caller and binds it to the invocation
// before forwarding a request here.
func (runtime *Runtime) ExecuteAgentToolReadOnly(ctx context.Context, request agenttools.Request) (agenttools.Receipt, error) {
	if runtime == nil {
		return agenttools.Receipt{}, application.ErrInvalidConfiguration
	}
	return runtime.toolGateway.ExecuteReadOnly(ctx, request)
}

// NewReadOnlyAgentToolSession binds one MCP tool surface to persisted Teams
// invocation authority. This method creates no listener or credential; the
// daemon's HTTP transport supplies those separately.
func (runtime *Runtime) NewReadOnlyAgentToolSession(ctx context.Context, invocationID kernel.UUIDv7, requestDigest kernel.Digest) (*mcp.Server, error) {
	if runtime == nil {
		return nil, application.ErrInvalidConfiguration
	}
	return agenttools.NewReadOnlyMCPServer(ctx, runtime.toolGateway, invocationID, requestDigest)
}

func (runtime *Runtime) AgentToolHandler() http.Handler {
	if runtime == nil || runtime.toolService == nil {
		return nil
	}
	return runtime.toolService.Handler()
}
