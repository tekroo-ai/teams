package agenttools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
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
	request.ToolCallID = "call-2"
	request.Call = Call{Name: "search_file_contents", Arguments: json.RawMessage(`{"regex":"second","filename_glob":"*.txt"}`)}
	searched, err := gateway.ExecuteReadOnly(context.Background(), request)
	if err != nil || len(searched.Result.Matches) != 1 || searched.Result.Matches[0].Line != 2 || binding.called != 2 {
		t.Fatalf("invocation-bound search: %+v, %v", searched, err)
	}

	request.Call = Call{Name: "write_file", Arguments: json.RawMessage(`{"path":"note.txt","content":"changed","expected_sha256":""}`)}
	if _, err := gateway.ExecuteReadOnly(context.Background(), request); !errors.Is(err, ErrForbidden) || binding.called != 2 {
		t.Fatalf("mutation reached binder: %v", err)
	}
	for _, name := range []string{"git_stage_files", "git_commit"} {
		request.Call = Call{Name: name, Arguments: json.RawMessage(`{}`)}
		if _, err := gateway.ExecuteReadOnly(context.Background(), request); !errors.Is(err, ErrForbidden) || binding.called != 2 {
			t.Fatalf("%s crossed read-only gateway: %v", name, err)
		}
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

func TestReadOnlyValidationRetainsOnlyIsolatedTestCapability(t *testing.T) {
	permissions := []string{"repository.read", "repository.edit", "test.execute", "task.validation-propose"}
	if got := readOnlyPermissionsForPurpose(permissions, kernel.PurposeValidation); !slices.Equal(got, []string{"repository.read", "test.execute"}) {
		t.Fatalf("read-only validation permissions = %v", got)
	}
	if got := readOnlyPermissionsForPurpose(permissions, kernel.PurposeReview); !slices.Equal(got, []string{"repository.read"}) {
		t.Fatalf("read-only review permissions = %v", got)
	}
	if got := readOnlyPermissionsForPurpose([]string{"repository.read"}, kernel.PurposeValidation); !slices.Equal(got, []string{"repository.read"}) {
		t.Fatalf("test capability was invented: %v", got)
	}
}
