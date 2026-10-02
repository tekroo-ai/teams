package operationalruntime

import (
	"context"
	"errors"
	"testing"

	"github.com/tekroo-ai/teams/adapters/openhands"
	"github.com/tekroo-ai/teams/kernel"
)

type toolTestWorkspace struct {
	binding openhands.WorkspaceBinding
	err     error
}

func (workspace toolTestWorkspace) ResolveWorkspace(context.Context, kernel.TaskOperationalScope) (openhands.WorkspaceBinding, error) {
	return workspace.binding, workspace.err
}

func TestToolWorkspaceAdapterPreservesVerifiedIdentityAndCandidateReadOnly(t *testing.T) {
	root := t.TempDir()
	binding := openhands.WorkspaceBinding{WorkspaceID: "workspace-1", WorktreeID: "worktree-1", WorkingDirectory: root,
		Candidate: &openhands.CandidateWorkspaceBinding{ReadOnly: true}}
	adapter := toolWorkspaceAdapter{resolver: toolTestWorkspace{binding: binding}}
	got, err := adapter.ResolveToolWorkspace(context.Background(), kernel.TaskOperationalScope{})
	if err != nil || got.WorkspaceID != binding.WorkspaceID || got.WorktreeID != binding.WorktreeID || got.Root != root || !got.ReadOnly {
		t.Fatalf("workspace binding lost identity or read-only state: %+v, %v", got, err)
	}
	failure := errors.New("candidate drift")
	adapter.resolver = toolTestWorkspace{err: failure}
	if _, err := adapter.ResolveToolWorkspace(context.Background(), kernel.TaskOperationalScope{}); !errors.Is(err, failure) {
		t.Fatalf("workspace verification failure was swallowed: %v", err)
	}
}
