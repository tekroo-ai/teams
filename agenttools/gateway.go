package agenttools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

// ErrStaleBinding means the caller's invocation or brief no longer matches
// Teams' authoritative execution state.
var ErrStaleBinding = errors.New("agent tool invocation binding is stale")

// WorkspaceBinding is resolved by the host from Teams' workspace identity.
// It is never supplied by the model or the tool-call transport.
type WorkspaceBinding struct {
	WorkspaceID string
	WorktreeID  string
	Root        string
	ReadOnly    bool
}

type WorkspaceResolver interface {
	ResolveToolWorkspace(context.Context, kernel.TaskOperationalScope) (WorkspaceBinding, error)
}

// BindingSource is a trusted server-side component. Its implementation below
// reconstructs the execution brief from durable Teams state.
type BindingSource interface {
	BindToolInvocation(context.Context, kernel.UUIDv7, kernel.Digest) (Authority, error)
}

type ExecutionBindingSource struct {
	Reader       application.OperationalExecutionReader
	Roles        application.RoleGroundingResolver
	Workspaces   WorkspaceResolver
	MaximumBytes int
	Now          func() time.Time
}

func (source ExecutionBindingSource) BindToolInvocation(ctx context.Context, id kernel.UUIDv7, expected kernel.Digest) (Authority, error) {
	if source.Reader == nil || source.Roles == nil || source.Workspaces == nil || source.Now == nil || !id.Valid() || !expected.Valid() {
		return Authority{}, ErrInvalidCall
	}
	current, err := source.Reader.LoadOperationalExecution(ctx, id)
	if err != nil {
		return Authority{}, err
	}
	invocation := current.Invocation
	if invocation.ID != id || (invocation.State != kernel.InvocationClaimed && invocation.State != kernel.InvocationStarted) || invocation.CancellationRequestedAt != nil {
		return Authority{}, ErrStaleBinding
	}
	if invocation.State == kernel.InvocationStarted && (invocation.RequestDigest == nil || *invocation.RequestDigest != expected) {
		return Authority{}, ErrStaleBinding
	}
	now := source.Now()
	effectiveDeadline := invocation.DeadlineAt
	if reader, ok := source.Reader.(application.OperationalDeadlineExtensionReader); ok {
		effectiveDeadline, err = reader.EffectiveWorkInvocationDeadline(ctx, invocation, now)
		if err != nil {
			return Authority{}, err
		}
		if effectiveDeadline.Before(invocation.DeadlineAt) {
			return Authority{}, ErrStaleBinding
		}
	}
	if !now.Before(effectiveDeadline) {
		return Authority{}, ErrStaleBinding
	}
	// A recorded host suspension can extend a started invocation without
	// rewriting its original brief. Validate structural identity against the
	// original deadlines, then apply the effective live deadline above.
	validationAt := now
	if !validationAt.Before(invocation.DeadlineAt) {
		validationAt = invocation.DeadlineAt.Add(-time.Nanosecond)
	}
	if !validationAt.Before(current.Budget.DeadlineAt) {
		validationAt = current.Budget.DeadlineAt.Add(-time.Nanosecond)
	}
	if current.Validate(validationAt) != nil {
		return Authority{}, ErrStaleBinding
	}
	var grounding application.RoleExecutionGrounding
	var brief application.ExecutionBrief
	var digest kernel.Digest
	if invocation.HandlerDispatch != nil {
		resolver, ok := source.Roles.(application.RoleHandlerGroundingResolver)
		if !ok {
			return Authority{}, ErrStaleBinding
		}
		var handler application.MessageHandlerGrounding
		grounding, handler, err = resolver.ResolveRoleHandlerGrounding(ctx, invocation.ActorFQN, invocation.HandlerDispatch.Clone())
		if err == nil {
			brief, digest, err = application.BuildExecutionBriefWithHandler(current, grounding, &handler, source.MaximumBytes)
		}
	} else {
		grounding, err = source.Roles.ResolveRoleGrounding(ctx, invocation.ActorFQN)
		if err == nil {
			brief, digest, err = application.BuildExecutionBrief(current, grounding, source.MaximumBytes)
		}
	}
	if err != nil || digest != expected || brief.InvocationID != id {
		return Authority{}, ErrStaleBinding
	}
	workspace, err := source.Workspaces.ResolveToolWorkspace(ctx, brief.Scope)
	if err != nil {
		return Authority{}, err
	}
	if workspace.WorkspaceID != brief.Scope.WorkspaceID || workspace.WorktreeID != brief.Scope.WorktreeID || workspace.Root == "" {
		return Authority{}, ErrStaleBinding
	}
	permissions := append([]string(nil), grounding.Permissions...)
	if workspace.ReadOnly {
		permissions = readOnlyPermissions(grounding.Permissions)
	}
	return Authority{WorkspaceRoot: workspace.Root, Permissions: permissions}, nil
}

func readOnlyPermissions(permissions []string) []string {
	if permitted(permissions, "repository.read") {
		return []string{"repository.read"}
	}
	return nil
}

// Request has no caller-supplied workspace, actor, or permission fields.
type Request struct {
	InvocationID  kernel.UUIDv7 `json:"invocation_id"`
	RequestDigest kernel.Digest `json:"request_digest"`
	ToolCallID    string        `json:"tool_call_id"`
	Call          Call          `json:"call"`
}

// Receipt binds a read result to the exact invocation, arguments, and tool
// call. Mutation is intentionally unavailable until durable replay protection
// and effect-policy enforcement are implemented.
type Receipt struct {
	InvocationID  kernel.UUIDv7 `json:"invocation_id"`
	RequestDigest kernel.Digest `json:"request_digest"`
	ToolCallID    string        `json:"tool_call_id"`
	ArgumentsHash string        `json:"arguments_sha256"`
	ResultHash    string        `json:"result_sha256"`
	Result        Result        `json:"result"`
}

type Gateway struct {
	Bindings BindingSource
	Host     Host
}

func (gateway Gateway) ExecuteReadOnly(ctx context.Context, request Request) (Receipt, error) {
	if gateway.Bindings == nil || gateway.Host.Timeout <= 0 || !request.InvocationID.Valid() || !request.RequestDigest.Valid() || strings.TrimSpace(request.ToolCallID) == "" || len(request.ToolCallID) > 256 {
		return Receipt{}, ErrInvalidCall
	}
	operation, cancel := context.WithTimeout(ctx, gateway.Host.Timeout)
	defer cancel()
	switch request.Call.Name {
	case "read_file", "list_files", "git_status", "git_diff":
	default:
		return Receipt{}, ErrForbidden
	}
	authority, err := gateway.Bindings.BindToolInvocation(operation, request.InvocationID, request.RequestDigest)
	if err != nil {
		return Receipt{}, err
	}
	result, err := gateway.Host.execute(operation, authority, request.Call)
	if err != nil {
		return Receipt{}, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return Receipt{}, err
	}
	argumentsHash := sha256.Sum256(request.Call.Arguments)
	resultHash := sha256.Sum256(encoded)
	return Receipt{InvocationID: request.InvocationID, RequestDigest: request.RequestDigest, ToolCallID: request.ToolCallID, ArgumentsHash: hex.EncodeToString(argumentsHash[:]), ResultHash: hex.EncodeToString(resultHash[:]), Result: result}, nil
}
