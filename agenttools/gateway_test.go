package agenttools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

type staticBinding struct {
	authority Authority
	id        kernel.UUIDv7
	digest    kernel.Digest
	called    int
}

func (binding *staticBinding) BindToolInvocation(_ context.Context, id kernel.UUIDv7, digest kernel.Digest) (Authority, error) {
	binding.called++
	if id != binding.id || digest != binding.digest {
		return Authority{}, ErrStaleBinding
	}
	return binding.authority, nil
}

func TestGatewayBindsReadToInvocationAndReceipts(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("first\nsecond\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	id := kernel.UUIDv7("00000000-0000-7000-8000-000000000951")
	digest := kernel.Digest(hex.EncodeToString(make([]byte, 32)))
	binding := &staticBinding{authority: Authority{WorkspaceRoot: root, Permissions: []string{"repository.read"}}, id: id, digest: digest}
	gateway := Gateway{Bindings: binding, Host: Host{Timeout: time.Second}}
	request := Request{InvocationID: id, RequestDigest: digest, ToolCallID: "call-1", Call: Call{Name: "read_file", Arguments: json.RawMessage(`{"path":"note.txt","start_line":2,"end_line":2}`)}}
	receipt, err := gateway.ExecuteReadOnly(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Result.Output != "second" || receipt.InvocationID != id || receipt.RequestDigest != digest || receipt.ToolCallID != "call-1" || binding.called != 1 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	argumentSum := sha256.Sum256(request.Call.Arguments)
	if receipt.ArgumentsHash != hex.EncodeToString(argumentSum[:]) || len(receipt.ResultHash) != 64 {
		t.Fatalf("unbound receipt hashes: %+v", receipt)
	}

	request.Call = Call{Name: "write_file", Arguments: json.RawMessage(`{"path":"note.txt","content":"changed","expected_sha256":""}`)}
	if _, err := gateway.ExecuteReadOnly(context.Background(), request); !errors.Is(err, ErrForbidden) || binding.called != 1 {
		t.Fatalf("mutation reached binder: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(root, "note.txt"))
	if err != nil || string(content) != "first\nsecond\n" {
		t.Fatalf("mutation changed file: %q, %v", content, err)
	}
	request.Call = Call{Name: "read_file", Arguments: json.RawMessage(`{"path":"note.txt"}`)}
	request.RequestDigest = kernel.Digest(hex.EncodeToString([]byte("wrong")))
	if _, err := gateway.ExecuteReadOnly(context.Background(), request); !errors.Is(err, ErrInvalidCall) {
		t.Fatalf("invalid digest accepted: %v", err)
	}
}

type staleReader struct {
	current application.OperationalExecutionContext
}

func (reader staleReader) LoadOperationalExecution(context.Context, kernel.UUIDv7) (application.OperationalExecutionContext, error) {
	return reader.current, nil
}

func (reader staleReader) LoadOperationalExecutionByAuthorizationEvent(context.Context, kernel.UUIDv7) (application.OperationalExecutionContext, error) {
	return reader.current, nil
}

type unusedRoles struct{}

func (unusedRoles) ResolveRoleGrounding(context.Context, kernel.ActorFQN) (application.RoleExecutionGrounding, error) {
	return application.RoleExecutionGrounding{}, errors.New("roles must not be read for stale execution")
}

type unusedWorkspace struct{}

func (unusedWorkspace) ResolveToolWorkspace(context.Context, kernel.TaskOperationalScope) (WorkspaceBinding, error) {
	return WorkspaceBinding{}, errors.New("workspace must not be read for stale execution")
}

func TestBindingSourceRejectsUnstartedExecutionBeforeRoleOrWorkspaceLookup(t *testing.T) {
	id := kernel.UUIDv7("00000000-0000-7000-8000-000000000951")
	digest := kernel.Digest(hex.EncodeToString(make([]byte, 32)))
	source := ExecutionBindingSource{Reader: staleReader{current: application.OperationalExecutionContext{Invocation: kernel.WorkInvocation{ID: id, State: kernel.InvocationClaimed}}}, Roles: unusedRoles{}, Workspaces: unusedWorkspace{}, MaximumBytes: 1 << 20, Now: time.Now}
	if _, err := source.BindToolInvocation(context.Background(), id, digest); !errors.Is(err, ErrStaleBinding) {
		t.Fatalf("unstarted invocation accepted: %v", err)
	}
}

func TestReadOnlyWorkspaceNeverGrantsMissingPermission(t *testing.T) {
	if got := readOnlyPermissions([]string{"model.invoke"}); len(got) != 0 {
		t.Fatalf("read-only workspace granted repository access: %v", got)
	}
	if got := readOnlyPermissions([]string{"repository.edit", "test.execute"}); len(got) != 1 || got[0] != "repository.read" {
		t.Fatalf("read-only workspace did not narrow permissions: %v", got)
	}
}
