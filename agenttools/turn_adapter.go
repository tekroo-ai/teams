package agenttools

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/kernel"
)

// ReadOnlyTurnAdapter binds model tool calls to Teams' existing authority- and
// workspace-checked gateway. The model cannot choose its invocation identity.
type ReadOnlyTurnAdapter struct {
	Gateway       Gateway
	InvocationID  kernel.UUIDv7
	RequestDigest kernel.Digest
}

// MutationTurnAdapter is opt-in. It exposes only names backed by a durable
// intent and an operation-specific reconciliation path.
type MutationTurnAdapter struct {
	Gateway       MutationGateway
	InvocationID  kernel.UUIDv7
	RequestDigest kernel.Digest
}

// TestTurnAdapter keeps candidate execution on the durable-effect path even
// though the tested repository is mounted read-only in the sandbox.
type TestTurnAdapter struct {
	Gateway       TestGateway
	InvocationID  kernel.UUIDv7
	RequestDigest kernel.Digest
}

func (adapter TestTurnAdapter) Handles(name string) bool { return name == "run_go_tests" }

func (adapter TestTurnAdapter) ExecuteEffect(ctx context.Context, call agentruntime.ToolCall) (json.RawMessage, error) {
	if !adapter.Handles(call.Name) {
		return nil, ErrForbidden
	}
	receipt, err := adapter.Gateway.Execute(ctx, Request{InvocationID: adapter.InvocationID, RequestDigest: adapter.RequestDigest,
		ToolCallID: call.ID, Call: Call{Name: call.Name, Arguments: call.Arguments}})
	if err != nil {
		if errors.Is(err, ErrEffectUncertain) {
			return nil, errors.Join(agentruntime.ErrEffectUncertain, err)
		}
		return nil, err
	}
	encoded, err := json.Marshal(receipt.Result)
	return encoded, err
}

func (adapter TestTurnAdapter) ReconcileEffect(ctx context.Context, call agentruntime.ToolCall) (json.RawMessage, bool, error) {
	if !adapter.Handles(call.Name) {
		return nil, false, ErrForbidden
	}
	receipt, found, err := adapter.Gateway.Reconcile(ctx, Request{InvocationID: adapter.InvocationID, RequestDigest: adapter.RequestDigest,
		ToolCallID: call.ID, Call: Call{Name: call.Name, Arguments: call.Arguments}})
	if err != nil {
		if errors.Is(err, ErrEffectUncertain) {
			return nil, false, errors.Join(agentruntime.ErrEffectUncertain, err)
		}
		return nil, false, err
	}
	if !found {
		return nil, false, nil
	}
	encoded, err := json.Marshal(receipt.Result)
	return encoded, true, err
}

func TestDefinitions(permissions []string) []agentruntime.ToolDefinition {
	for _, spec := range Available(permissions) {
		if spec.Name == "run_go_tests" {
			return []agentruntime.ToolDefinition{{Name: spec.Name, Description: spec.Description, Parameters: spec.InputSchema}}
		}
	}
	return nil
}

func (adapter MutationTurnAdapter) Handles(name string) bool {
	return name == "write_file" || name == "git_stage_files" || name == "git_commit"
}

func (adapter MutationTurnAdapter) ExecuteEffect(ctx context.Context, call agentruntime.ToolCall) (json.RawMessage, error) {
	if !adapter.Handles(call.Name) {
		return nil, ErrForbidden
	}
	receipt, err := adapter.Gateway.Execute(ctx, Request{
		InvocationID: adapter.InvocationID, RequestDigest: adapter.RequestDigest,
		ToolCallID: call.ID, Call: Call{Name: call.Name, Arguments: call.Arguments},
	})
	if err != nil {
		if errors.Is(err, ErrEffectUncertain) {
			return nil, errors.Join(agentruntime.ErrEffectUncertain, err)
		}
		return nil, err
	}
	result, err := json.Marshal(receipt.Result)
	return json.RawMessage(result), err
}

func (adapter MutationTurnAdapter) ReconcileEffect(ctx context.Context, call agentruntime.ToolCall) (json.RawMessage, bool, error) {
	if !adapter.Handles(call.Name) {
		return nil, false, ErrForbidden
	}
	receipt, applied, err := adapter.Gateway.Reconcile(ctx, Request{
		InvocationID: adapter.InvocationID, RequestDigest: adapter.RequestDigest,
		ToolCallID: call.ID, Call: Call{Name: call.Name, Arguments: call.Arguments},
	})
	if err != nil || !applied {
		return nil, false, err
	}
	result, err := json.Marshal(receipt.Result)
	return json.RawMessage(result), true, err
}

func MutationDefinitions(permissions []string) []agentruntime.ToolDefinition {
	available := Available(permissions)
	definitions := make([]agentruntime.ToolDefinition, 0, 3)
	for _, spec := range available {
		if spec.Name == "write_file" || spec.Name == "git_stage_files" || spec.Name == "git_commit" {
			definitions = append(definitions, agentruntime.ToolDefinition{Name: spec.Name, Description: spec.Description, Parameters: spec.InputSchema})
		}
	}
	return definitions
}

func (adapter ReadOnlyTurnAdapter) ExecuteReadOnly(ctx context.Context, call agentruntime.ToolCall) (json.RawMessage, error) {
	receipt, err := adapter.Gateway.ExecuteReadOnly(ctx, Request{
		InvocationID: adapter.InvocationID, RequestDigest: adapter.RequestDigest,
		ToolCallID: call.ID, Call: Call{Name: call.Name, Arguments: call.Arguments},
	})
	if err != nil {
		return nil, err
	}
	result, err := json.Marshal(receipt.Result)
	return json.RawMessage(result), err
}

// ReadOnlyDefinitions derives the model-facing schema from the same canonical
// specs that the host enforces. No separate hand-written tool schema exists.
func ReadOnlyDefinitions(permissions []string) []agentruntime.ToolDefinition {
	available := Available(permissions)
	definitions := make([]agentruntime.ToolDefinition, 0, len(available))
	for _, spec := range available {
		if spec.Permission != "repository.read" {
			continue
		}
		definitions = append(definitions, agentruntime.ToolDefinition{Name: spec.Name, Description: spec.Description, Parameters: spec.InputSchema})
	}
	return definitions
}
