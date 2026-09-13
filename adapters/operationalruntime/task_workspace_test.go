package operationalruntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/openhands"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestTaskWorkspacesIsolateBranchesAndAssembleTheDAGDeterministically(t *testing.T) {
	rootDirectory, _, baseline := candidateRepository(t)
	root := ProductionWorkspace{WorkspaceID: "repository", WorktreeID: "main", WorkingDirectory: rootDirectory, Branch: "source", BaselineSHA: baseline, WritablePaths: []string{"."}}
	evidenceRoot := t.TempDir()
	resolver, err := openhands.NewBoundWorkspaceResolver([]openhands.WorkspaceBinding{{WorkspaceID: root.WorkspaceID, WorktreeID: root.WorktreeID, WorkingDirectory: root.WorkingDirectory}})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := newTaskWorkspaceManagerWithContext(context.Background(), evidenceRoot, "git", time.Minute, resolver)
	if err != nil {
		t.Fatal(err)
	}
	feature := organization.FeatureRequest{ID: candidateTestUUID(1001)}
	baseTask := organization.PlannedTask{ID: candidateTestUUID(1002), Purpose: kernel.PurposeImplementation}
	leftTask := organization.PlannedTask{ID: candidateTestUUID(1003), Purpose: kernel.PurposeImplementation, DependsOn: []kernel.UUIDv7{baseTask.ID}}
	rightTask := organization.PlannedTask{ID: candidateTestUUID(1004), Purpose: kernel.PurposeImplementation, DependsOn: []kernel.UUIDv7{baseTask.ID}}

	baseWorkspace, _, err := manager.PrepareTask(context.Background(), feature, baseTask, "coder-1", root, nil)
	if err != nil {
		t.Fatal(err)
	}
	commitTaskFile(t, baseWorkspace.WorkingDirectory, "base.txt", "base\n", "base task")
	baseComponent := taskWorkspaceComponent{TaskID: baseTask.ID, WorkspaceID: baseWorkspace.WorkspaceID, WorktreeID: baseWorkspace.WorktreeID, WorkingDirectory: baseWorkspace.WorkingDirectory}

	leftWorkspace, leftReceipt, err := manager.PrepareTask(context.Background(), feature, leftTask, "coder-2", root, []taskWorkspaceComponent{baseComponent})
	if err != nil {
		t.Fatal(err)
	}
	rightWorkspace, _, err := manager.PrepareTask(context.Background(), feature, rightTask, "coder-3", root, []taskWorkspaceComponent{baseComponent})
	if err != nil {
		t.Fatal(err)
	}
	if leftWorkspace.WorkingDirectory == rightWorkspace.WorkingDirectory || leftWorkspace.WorkingDirectory == baseWorkspace.WorkingDirectory {
		t.Fatal("independent DAG nodes shared an editable working directory")
	}
	commitTaskFile(t, leftWorkspace.WorkingDirectory, "left.txt", "left\n", "left task")
	commitTaskFile(t, rightWorkspace.WorkingDirectory, "right.txt", "right\n", "right task")
	if err := os.Remove(manager.receiptPath(leftReceipt)); err != nil {
		t.Fatal(err)
	}
	recoveredLeft, recoveredLeftReceipt, err := manager.PrepareTask(context.Background(), feature, leftTask, "coder-2", root, []taskWorkspaceComponent{baseComponent})
	if err != nil {
		t.Fatal(err)
	}
	if recoveredLeft.WorkingDirectory != leftWorkspace.WorkingDirectory || recoveredLeftReceipt.PreparedCommit != leftReceipt.PreparedCommit {
		t.Fatalf("workspace created before its receipt was not recovered: workspace=%#v receipt=%#v", recoveredLeft, recoveredLeftReceipt)
	}

	components := []taskWorkspaceComponent{
		{TaskID: leftTask.ID, WorkspaceID: leftWorkspace.WorkspaceID, WorktreeID: leftWorkspace.WorktreeID, WorkingDirectory: leftWorkspace.WorkingDirectory},
		{TaskID: rightTask.ID, WorkspaceID: rightWorkspace.WorkspaceID, WorktreeID: rightWorkspace.WorktreeID, WorkingDirectory: rightWorkspace.WorkingDirectory},
	}
	assembly, receipt, err := manager.PrepareAssembly(context.Background(), feature, "tester-1", root, components)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"base.txt", "left.txt", "right.txt"} {
		if _, err := os.Stat(filepath.Join(assembly.WorkingDirectory, name)); err != nil {
			t.Fatalf("assembled candidate omitted %s: %v", name, err)
		}
	}
	second, secondReceipt, err := manager.PrepareAssembly(context.Background(), feature, "tester-1", root, components)
	if err != nil {
		t.Fatal(err)
	}
	if second.WorkingDirectory != assembly.WorkingDirectory || secondReceipt.PreparedCommit != receipt.PreparedCommit || secondReceipt.PreparedTree != receipt.PreparedTree {
		t.Fatalf("assembly was not deterministic: first=%#v second=%#v", receipt, secondReceipt)
	}
	commitTaskFile(t, rightWorkspace.WorkingDirectory, "right-successor.txt", "right successor\n", "advance right task")
	successor, successorReceipt, err := manager.PrepareAssembly(context.Background(), feature, "tester-1", root, components)
	if err != nil {
		t.Fatal(err)
	}
	if successor.WorkingDirectory == assembly.WorkingDirectory || successorReceipt.WorkID == receipt.WorkID || successorReceipt.PreparedCommit == receipt.PreparedCommit {
		t.Fatalf("advanced component reused immutable assembly: first=%#v successor=%#v", receipt, successorReceipt)
	}
	if _, err := os.Stat(filepath.Join(successor.WorkingDirectory, "right-successor.txt")); err != nil {
		t.Fatalf("successor assembly omitted advanced component: %v", err)
	}
	if err := os.WriteFile(filepath.Join(leftWorkspace.WorkingDirectory, "in-progress.txt"), []byte("resume me\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	restartedResolver, err := openhands.NewBoundWorkspaceResolver([]openhands.WorkspaceBinding{{WorkspaceID: root.WorkspaceID, WorktreeID: root.WorktreeID, WorkingDirectory: root.WorkingDirectory}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newTaskWorkspaceManagerWithContext(context.Background(), evidenceRoot, "git", time.Minute, restartedResolver); err != nil {
		t.Fatal(err)
	}
	scope := kernel.TaskOperationalScope{WorkspaceID: leftWorkspace.WorkspaceID, WorktreeID: leftWorkspace.WorktreeID}
	if binding, err := restartedResolver.ResolveWorkspace(context.Background(), scope); err != nil || binding.WorkingDirectory != leftWorkspace.WorkingDirectory {
		t.Fatalf("editable workspace was not rehydrated: binding=%#v err=%v", binding, err)
	}
}

func TestTaskWorkspaceAssemblyStopsOnSiblingMergeConflict(t *testing.T) {
	rootDirectory, _, baseline := candidateRepository(t)
	root := ProductionWorkspace{WorkspaceID: "repository", WorktreeID: "main", WorkingDirectory: rootDirectory, Branch: "source", BaselineSHA: baseline, WritablePaths: []string{"."}}
	resolver, err := openhands.NewBoundWorkspaceResolver([]openhands.WorkspaceBinding{{WorkspaceID: root.WorkspaceID, WorktreeID: root.WorktreeID, WorkingDirectory: root.WorkingDirectory}})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := newTaskWorkspaceManagerWithContext(context.Background(), t.TempDir(), "git", time.Minute, resolver)
	if err != nil {
		t.Fatal(err)
	}
	feature := organization.FeatureRequest{ID: candidateTestUUID(1011)}
	leftTask := organization.PlannedTask{ID: candidateTestUUID(1012), Purpose: kernel.PurposeImplementation}
	rightTask := organization.PlannedTask{ID: candidateTestUUID(1013), Purpose: kernel.PurposeImplementation}
	left, _, err := manager.PrepareTask(context.Background(), feature, leftTask, "coder-1", root, nil)
	if err != nil {
		t.Fatal(err)
	}
	right, _, err := manager.PrepareTask(context.Background(), feature, rightTask, "coder-2", root, nil)
	if err != nil {
		t.Fatal(err)
	}
	commitTaskFile(t, left.WorkingDirectory, "file.txt", "left\n", "left conflict")
	commitTaskFile(t, right.WorkingDirectory, "file.txt", "right\n", "right conflict")
	components := []taskWorkspaceComponent{
		{TaskID: leftTask.ID, WorkspaceID: left.WorkspaceID, WorktreeID: left.WorktreeID, WorkingDirectory: left.WorkingDirectory},
		{TaskID: rightTask.ID, WorkspaceID: right.WorkspaceID, WorktreeID: right.WorktreeID, WorkingDirectory: right.WorkingDirectory},
	}
	if _, _, err := manager.PrepareAssembly(context.Background(), feature, "tester-1", root, components); !errors.Is(err, errTaskWorkspaceMergeConflict) {
		t.Fatalf("conflicting sibling branches returned %v", err)
	}
}

func TestPersistedTaskWorkspaceOwnerDoesNotDependOnActorLiveness(t *testing.T) {
	task := organization.PlannedTask{ID: candidateTestUUID(1021), Owner: kernel.ActorFQN("teams::coder-1")}
	scope := kernel.TaskOperationalScope{
		TaskID: task.ID, TaskRevision: 2, LifecycleEpoch: 1, ScopeRevision: 1,
		OwnerFQN: task.Owner, Execution: kernel.ExecutionTuple{ExecutionID: candidateTestUUID(1022), FencingEpoch: 1},
		WorkspaceID: "coder-1", WorktreeID: "task-" + string(task.ID), Branch: "tekroo/task/example",
		BaselineSHA: strings.Repeat("a", 40), WritablePaths: []string{"."}, BoundEventID: candidateTestUUID(1023),
	}
	snapshot := kernel.Snapshot{TaskOperationalScopes: map[kernel.AggregateRef]kernel.TaskOperationalScope{{Kind: kernel.AggregateTask, ID: task.ID}: scope}}
	owner, err := persistedTaskWorkspaceOwner(task, snapshot)
	if err != nil || owner.ActorFQN != task.Owner || owner.WorkspaceID != scope.WorkspaceID {
		t.Fatalf("persisted owner=%#v err=%v", owner, err)
	}

	scope.OwnerFQN = kernel.ActorFQN("teams::coder-2")
	snapshot.TaskOperationalScopes[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}] = scope
	if _, err := persistedTaskWorkspaceOwner(task, snapshot); !errors.Is(err, errInvalidTaskWorkspace) {
		t.Fatalf("mismatched persisted owner returned %v", err)
	}
}

func TestRepairWorkspaceUsesCurrentCleanHeadAsSuccessorBaseline(t *testing.T) {
	rootDirectory, _, baseline := candidateRepository(t)
	root := ProductionWorkspace{WorkspaceID: "repository", WorktreeID: "main", WorkingDirectory: rootDirectory, Branch: "source", BaselineSHA: baseline, WritablePaths: []string{"."}}
	resolver, err := openhands.NewBoundWorkspaceResolver([]openhands.WorkspaceBinding{{WorkspaceID: root.WorkspaceID, WorktreeID: root.WorktreeID, WorkingDirectory: root.WorkingDirectory}})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := newTaskWorkspaceManagerWithContext(context.Background(), t.TempDir(), "git", time.Minute, resolver)
	if err != nil {
		t.Fatal(err)
	}
	feature := organization.FeatureRequest{ID: candidateTestUUID(1031)}
	task := organization.PlannedTask{ID: candidateTestUUID(1032), Purpose: kernel.PurposeImplementation}
	workspace, _, err := manager.PrepareTask(context.Background(), feature, task, "coder-1", root, nil)
	if err != nil {
		t.Fatal(err)
	}
	commitTaskFile(t, workspace.WorkingDirectory, "implemented.txt", "implemented\n", "implementation")
	implementationHead := candidateGit(t, workspace.WorkingDirectory, "rev-parse", "HEAD")
	repair, err := manager.prepareRepairWorkspace(context.Background(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	if repair.BaselineSHA != implementationHead || repair.BaselineSHA == workspace.BaselineSHA {
		t.Fatalf("repair baseline=%s implementation=%s original=%s", repair.BaselineSHA, implementationHead, workspace.BaselineSHA)
	}
	if err := os.WriteFile(filepath.Join(workspace.WorkingDirectory, "uncommitted.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.prepareRepairWorkspace(context.Background(), repair); !errors.Is(err, errInvalidTaskWorkspace) {
		t.Fatalf("dirty repair workspace returned %v", err)
	}
}

func commitTaskFile(t *testing.T, directory, name, content, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	candidateGit(t, directory, "add", name)
	candidateGit(t, directory, "-c", "user.name=Tekroo Test", "-c", "user.email=test@tekroo.invalid", "commit", "-m", message)
}
