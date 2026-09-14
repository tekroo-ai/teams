package operationalruntime

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/openhands"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestValidationCandidateTargetsIncludeEveryImplementationDependency(t *testing.T) {
	first := candidateTestUUID(901)
	second := candidateTestUUID(902)
	validatorID := candidateTestUUID(903)
	plan := organization.FeaturePlan{Tasks: []organization.PlannedTask{
		{ID: first, Purpose: kernel.PurposeImplementation},
		{ID: second, Purpose: kernel.PurposeImplementation},
		{ID: candidateTestUUID(904), Purpose: kernel.PurposeValidation},
	}}
	validator := organization.PlannedTask{ID: validatorID, Purpose: kernel.PurposeValidation, DependsOn: []kernel.UUIDv7{first, second}, Validates: []kernel.UUIDv7{second}}
	targets := validationCandidateTargetIDs(plan, validator)
	if len(targets) != 2 || targets[0] != first || targets[1] != second {
		t.Fatalf("validation candidate targets=%v", targets)
	}
	validator.DependsOn = []kernel.UUIDv7{first, candidateTestUUID(904)}
	targets = validationCandidateTargetIDs(plan, validator)
	if len(targets) != 1 || targets[0] != first {
		t.Fatalf("non-implementation dependency entered candidate=%v", targets)
	}
}

func TestCandidateWorkspaceMaterializesExactIsolatedReadOnlyRepository(t *testing.T) {
	source, baseline, candidate := candidateRepository(t)
	resolver, err := openhands.NewBoundWorkspaceResolver([]openhands.WorkspaceBinding{{WorkspaceID: "tester-1", WorktreeID: "tester-default", WorkingDirectory: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	evidenceRoot := t.TempDir()
	gates := []ProductionCandidateGate{{GateID: "candidate-head", Command: []string{"git", "rev-parse", "HEAD"}, Timeout: "10s"}}
	manager, err := newCandidateWorkspaceManager(evidenceRoot, "git", time.Minute, gates, resolver)
	if err != nil {
		t.Fatal(err)
	}
	feature := organization.FeatureRequest{ID: candidateTestUUID(1)}
	consumer := organization.PlannedTask{ID: candidateTestUUID(2), Owner: "teams::tester-1"}
	targets := []candidateTargetReceipt{{TaskID: candidateTestUUID(3), InvocationID: candidateTestUUID(4), OutputSHA256: candidateTestDigest('a'), TerminalEvidenceIDs: []kernel.UUIDv7{candidateTestUUID(5)}}}
	sourceWorkspace := ProductionWorkspace{WorkspaceID: "coder-1", WorktreeID: "coder-source", WorkingDirectory: source, Branch: "source", BaselineSHA: baseline, WritablePaths: []string{"."}}
	if _, err := manager.inspectSource(context.Background(), sourceWorkspace); err != nil {
		t.Fatalf("source preflight: %v", err)
	}
	if _, err := manager.runGates(context.Background(), source, []string{"candidate-head"}); err != nil {
		t.Fatalf("gate preflight: %v", err)
	}
	workspace, receipt, receiptDigest, err := manager.Prepare(context.Background(), feature, consumer, "tester-1", sourceWorkspace, targets, []string{"candidate-head"})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.CandidateCommit != candidate || receipt.BaselineCommit != baseline || receipt.CandidateTree != candidateGit(t, source, "rev-parse", "HEAD^{tree}") || receipt.DiffSHA256 == "" || receipt.ChangedFileInventoryDigest == "" || !receipt.RuntimeHookSHA256.Valid() || len(receipt.GateReceipts) != 1 || !receipt.GateReceipts[0].ReceiptID.Valid() {
		t.Fatalf("receipt = %#v", receipt)
	}
	gateOutput, err := base64.StdEncoding.DecodeString(receipt.GateReceipts[0].StdoutBase64)
	if err != nil || strings.TrimSpace(string(gateOutput)) != candidate || digestBytes(gateOutput) != receipt.GateReceipts[0].StdoutSHA256 {
		t.Fatalf("retained gate output=%q err=%v", gateOutput, err)
	}
	scope := kernel.TaskOperationalScope{WorkspaceID: workspace.WorkspaceID, WorktreeID: workspace.WorktreeID}
	binding, err := resolver.ResolveWorkspace(context.Background(), scope)
	if err != nil || binding.Candidate == nil || binding.Candidate.ReceiptSHA256 != receiptDigest || candidateGit(t, binding.WorkingDirectory, "rev-parse", "HEAD") != candidate || candidateGit(t, binding.WorkingDirectory, "for-each-ref", "--format=%(refname)") != receipt.MaterializedReference {
		t.Fatalf("binding=%#v err=%v", binding, err)
	}
	hook, err := os.ReadFile(filepath.Join(binding.WorkingDirectory, ".openhands", "hooks", "sma_context_hook.py"))
	if err != nil || digestBytes(hook) != receipt.RuntimeHookSHA256 || binding.Candidate.RuntimeHookSHA256 != receipt.RuntimeHookSHA256 {
		t.Fatalf("materialized runtime hook digest=%s binding=%s err=%v", digestBytes(hook), binding.Candidate.RuntimeHookSHA256, err)
	}
	restartedResolver, err := openhands.NewBoundWorkspaceResolver([]openhands.WorkspaceBinding{{WorkspaceID: "tester-1", WorktreeID: "tester-default", WorkingDirectory: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newCandidateWorkspaceManager(evidenceRoot, "git", time.Minute, gates, restartedResolver); err != nil {
		t.Fatal(err)
	}
	if restarted, err := restartedResolver.ResolveWorkspace(context.Background(), scope); err != nil || restarted.Candidate == nil || restarted.Candidate.ReceiptSHA256 != receiptDigest {
		t.Fatalf("rehydrated binding=%#v err=%v", restarted, err)
	}
	secondConsumer := organization.PlannedTask{ID: candidateTestUUID(6), Owner: "teams::product-owner-1"}
	secondWorkspace, secondReceipt, _, err := manager.Prepare(context.Background(), feature, secondConsumer, "product-owner-1", sourceWorkspace, targets, []string{"candidate-head"})
	if err != nil || secondReceipt.CandidateID != receipt.CandidateID || secondWorkspace.WorkingDirectory == workspace.WorkingDirectory {
		t.Fatalf("second candidate view workspace=%#v receipt=%#v err=%v", secondWorkspace, secondReceipt, err)
	}
	thirdConsumer := organization.PlannedTask{ID: candidateTestUUID(7), Owner: "teams::product-owner-1"}
	thirdWorkspace, thirdReceipt, thirdDigest, err := manager.Reuse(context.Background(), feature, thirdConsumer, "product-owner-1", receipt)
	if err != nil || thirdReceipt.CandidateID != receipt.CandidateID || thirdReceipt.DiffSHA256 != receipt.DiffSHA256 || thirdWorkspace.WorkingDirectory == workspace.WorkingDirectory || thirdDigest == receiptDigest || thirdReceipt.GateReceipts[0].ReceiptID != receipt.GateReceipts[0].ReceiptID {
		t.Fatalf("reused candidate view workspace=%#v receipt=%#v digest=%s err=%v", thirdWorkspace, thirdReceipt, thirdDigest, err)
	}
	thirdScope := kernel.TaskOperationalScope{WorkspaceID: thirdWorkspace.WorkspaceID, WorktreeID: thirdWorkspace.WorktreeID}
	if rebound, err := resolver.ResolveWorkspace(context.Background(), thirdScope); err != nil || rebound.Candidate == nil || rebound.Candidate.ReceiptSHA256 != thirdDigest {
		t.Fatalf("reused candidate binding=%#v err=%v", rebound, err)
	}
	makeWritableForCleanup(t, thirdWorkspace.WorkingDirectory)
	makeWritableForCleanup(t, secondWorkspace.WorkingDirectory)
	tracked := filepath.Join(binding.WorkingDirectory, "file.txt")
	if err := os.WriteFile(tracked, []byte("mutation\n"), 0o644); err == nil {
		t.Fatal("read-only candidate accepted a direct source mutation")
	}

	makeWritableForCleanup(t, binding.WorkingDirectory)
	if err := os.WriteFile(tracked, []byte("mutation\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ResolveWorkspace(context.Background(), scope); !errors.Is(err, openhands.ErrProtocol) {
		t.Fatalf("mutated candidate error = %v", err)
	}
}

func TestWholeFeatureValidationRecognitionUsesCompleteDAGCoverage(t *testing.T) {
	first := candidateTestUUID(911)
	second := candidateTestUUID(912)
	localValidator := candidateTestUUID(913)
	wholeValidator := candidateTestUUID(914)
	promotion := candidateTestUUID(915)
	plan := organization.FeaturePlan{Tasks: []organization.PlannedTask{
		{ID: first, Purpose: kernel.PurposeImplementation},
		{ID: second, Purpose: kernel.PurposeImplementation, DependsOn: []kernel.UUIDv7{first}},
		{ID: localValidator, Purpose: kernel.PurposeValidation, DependsOn: []kernel.UUIDv7{first}, Validates: []kernel.UUIDv7{first}},
		{ID: wholeValidator, Purpose: kernel.PurposeValidation, DependsOn: []kernel.UUIDv7{first, second}, Validates: []kernel.UUIDv7{first}},
		{ID: promotion, Purpose: kernel.PurposePromotion, DependsOn: []kernel.UUIDv7{first, second, localValidator, wholeValidator}},
	}}
	if got, found := wholeFeatureValidationTaskID(plan); !found || got != wholeValidator {
		t.Fatalf("whole-feature validator=(%s,%v), want (%s,true)", got, found, wholeValidator)
	}
	targets := []candidateTargetReceipt{{TaskID: first}, {TaskID: second}}
	if !candidateTargetsMatch(targets, []kernel.UUIDv7{first, second}) || candidateTargetsMatch(targets[:1], []kernel.UUIDv7{first, second}) {
		t.Fatal("assembled candidate target identity was not exact")
	}
}

func TestInvocationOutputEvidenceSelectsExactTerminalOutput(t *testing.T) {
	outputID := candidateTestUUID(916)
	journalID := candidateTestUUID(917)
	outputDigest := candidateTestDigest('a')
	snapshot := kernel.Snapshot{Evidence: map[kernel.UUIDv7]kernel.EvidenceMetadata{
		journalID: {SHA256: candidateTestDigest('b'), Available: true},
		outputID:  {SHA256: outputDigest, Available: true},
	}}
	invocation := kernel.WorkInvocation{OutputDigest: &outputDigest, TerminalEvidenceIDs: []kernel.UUIDv7{journalID, outputID}}
	reference, found := invocationOutputEvidence(snapshot, invocation)
	if !found || reference.EvidenceID != outputID || reference.SHA256 != outputDigest {
		t.Fatalf("output evidence=%#v found=%t", reference, found)
	}
}

func TestCandidateWorkspaceRecoversWorkspaceCreatedBeforeReceipt(t *testing.T) {
	source, baseline, _ := candidateRepository(t)
	resolver, err := openhands.NewBoundWorkspaceResolver([]openhands.WorkspaceBinding{{WorkspaceID: "tester-1", WorktreeID: "tester-default", WorkingDirectory: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	gates := []ProductionCandidateGate{{GateID: "candidate-head", Command: []string{"git", "rev-parse", "HEAD"}, Timeout: "10s"}}
	manager, err := newCandidateWorkspaceManager(t.TempDir(), "git", time.Minute, gates, resolver)
	if err != nil {
		t.Fatal(err)
	}
	feature := organization.FeatureRequest{ID: candidateTestUUID(21)}
	consumer := organization.PlannedTask{ID: candidateTestUUID(22), Owner: "teams::tester-1"}
	targets := []candidateTargetReceipt{{TaskID: candidateTestUUID(23), InvocationID: candidateTestUUID(24), OutputSHA256: candidateTestDigest('c'), TerminalEvidenceIDs: []kernel.UUIDv7{candidateTestUUID(25)}}}
	sourceWorkspace := ProductionWorkspace{WorkspaceID: "coder-1", WorktreeID: "coder-source", WorkingDirectory: source, Branch: "source", BaselineSHA: baseline, WritablePaths: []string{"."}}
	workspace, receipt, digest, err := manager.Prepare(context.Background(), feature, consumer, "tester-1", sourceWorkspace, targets, []string{"candidate-head"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(manager.receiptPath(receipt)); err != nil {
		t.Fatal(err)
	}
	recoveredWorkspace, recoveredReceipt, recoveredDigest, err := manager.Prepare(context.Background(), feature, consumer, "tester-1", sourceWorkspace, targets, []string{"candidate-head"})
	if err != nil {
		t.Fatal(err)
	}
	if recoveredWorkspace.WorkingDirectory != workspace.WorkingDirectory || recoveredReceipt.CandidateID != receipt.CandidateID || recoveredDigest != digest {
		t.Fatalf("recovered workspace=%#v receipt=%#v digest=%s", recoveredWorkspace, recoveredReceipt, recoveredDigest)
	}
	makeWritableForCleanup(t, workspace.WorkingDirectory)
}

func TestCandidateWorkspaceReusesVerifiedReceiptWithoutRerunningGate(t *testing.T) {
	source, baseline, _ := candidateRepository(t)
	resolver, err := openhands.NewBoundWorkspaceResolver([]openhands.WorkspaceBinding{{WorkspaceID: "tester-1", WorktreeID: "tester-default", WorkingDirectory: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	gateDirectory := t.TempDir()
	counterPath := filepath.Join(gateDirectory, "calls")
	gatePath := filepath.Join(gateDirectory, "gate.sh")
	if err := os.WriteFile(gatePath, []byte("#!/bin/sh\nprintf x >> \"$1\"\nprintf gate-output\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	gates := []ProductionCandidateGate{{GateID: "counted", Command: []string{gatePath, counterPath}, Timeout: "10s"}}
	manager, err := newCandidateWorkspaceManager(t.TempDir(), "git", time.Minute, gates, resolver)
	if err != nil {
		t.Fatal(err)
	}
	feature := organization.FeatureRequest{ID: candidateTestUUID(41)}
	consumer := organization.PlannedTask{ID: candidateTestUUID(42), Owner: "teams::tester-1"}
	targets := []candidateTargetReceipt{{TaskID: candidateTestUUID(43), InvocationID: candidateTestUUID(44), OutputSHA256: candidateTestDigest('e'), TerminalEvidenceIDs: []kernel.UUIDv7{candidateTestUUID(45)}}}
	sourceWorkspace := ProductionWorkspace{WorkspaceID: "coder-1", WorktreeID: "coder-source", WorkingDirectory: source, Branch: "source", BaselineSHA: baseline, WritablePaths: []string{"."}}
	firstWorkspace, firstReceipt, firstDigest, err := manager.Prepare(context.Background(), feature, consumer, "tester-1", sourceWorkspace, targets, []string{"counted"})
	if err != nil {
		t.Fatal(err)
	}
	secondWorkspace, secondReceipt, secondDigest, err := manager.Prepare(context.Background(), feature, consumer, "tester-1", sourceWorkspace, targets, []string{"counted"})
	if err != nil {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(counterPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(calls) != "x" || firstWorkspace.WorkingDirectory != secondWorkspace.WorkingDirectory || firstReceipt.CandidateID != secondReceipt.CandidateID || firstDigest != secondDigest {
		t.Fatalf("calls=%q first=%#v second=%#v", calls, firstReceipt, secondReceipt)
	}
	makeWritableForCleanup(t, firstWorkspace.WorkingDirectory)
}

func TestCandidateWorkspaceRehydrateRejectsMutatedWorkspace(t *testing.T) {
	source, baseline, _ := candidateRepository(t)
	resolver, err := openhands.NewBoundWorkspaceResolver([]openhands.WorkspaceBinding{{WorkspaceID: "tester-1", WorktreeID: "tester-default", WorkingDirectory: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	evidenceRoot := t.TempDir()
	gates := []ProductionCandidateGate{{GateID: "candidate-head", Command: []string{"git", "rev-parse", "HEAD"}, Timeout: "10s"}}
	manager, err := newCandidateWorkspaceManager(evidenceRoot, "git", time.Minute, gates, resolver)
	if err != nil {
		t.Fatal(err)
	}
	feature := organization.FeatureRequest{ID: candidateTestUUID(31)}
	consumer := organization.PlannedTask{ID: candidateTestUUID(32), Owner: "teams::tester-1"}
	targets := []candidateTargetReceipt{{TaskID: candidateTestUUID(33), InvocationID: candidateTestUUID(34), OutputSHA256: candidateTestDigest('d'), TerminalEvidenceIDs: []kernel.UUIDv7{candidateTestUUID(35)}}}
	sourceWorkspace := ProductionWorkspace{WorkspaceID: "coder-1", WorktreeID: "coder-source", WorkingDirectory: source, Branch: "source", BaselineSHA: baseline, WritablePaths: []string{"."}}
	workspace, _, _, err := manager.Prepare(context.Background(), feature, consumer, "tester-1", sourceWorkspace, targets, []string{"candidate-head"})
	if err != nil {
		t.Fatal(err)
	}
	makeWritableForCleanup(t, workspace.WorkingDirectory)
	if err := os.WriteFile(filepath.Join(workspace.WorkingDirectory, "file.txt"), []byte("mutated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	restartedResolver, err := openhands.NewBoundWorkspaceResolver([]openhands.WorkspaceBinding{{WorkspaceID: "tester-1", WorktreeID: "tester-default", WorkingDirectory: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newCandidateWorkspaceManager(evidenceRoot, "git", time.Minute, gates, restartedResolver); !errors.Is(err, errInvalidCandidateWorkspace) {
		t.Fatalf("mutated rehydration error = %v", err)
	}
}

func TestCandidateWorkspaceRejectsDirtyStaleAndUngatedSourcesBeforeMaterialization(t *testing.T) {
	source, baseline, _ := candidateRepository(t)
	resolver, err := openhands.NewBoundWorkspaceResolver([]openhands.WorkspaceBinding{{WorkspaceID: "tester-1", WorktreeID: "tester-default", WorkingDirectory: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := newCandidateWorkspaceManager(t.TempDir(), "git", time.Minute, nil, resolver)
	if err != nil {
		t.Fatal(err)
	}
	feature := organization.FeatureRequest{ID: candidateTestUUID(11)}
	consumer := organization.PlannedTask{ID: candidateTestUUID(12), Owner: "teams::tester-1"}
	targets := []candidateTargetReceipt{{TaskID: candidateTestUUID(13), InvocationID: candidateTestUUID(14), OutputSHA256: candidateTestDigest('b'), TerminalEvidenceIDs: []kernel.UUIDv7{candidateTestUUID(15)}}}
	configured := ProductionWorkspace{WorkspaceID: "coder-1", WorktreeID: "coder-source", WorkingDirectory: source, Branch: "source", BaselineSHA: baseline, WritablePaths: []string{"."}}
	if _, _, _, err := manager.Prepare(context.Background(), feature, consumer, "tester-1", configured, targets, []string{"missing"}); err == nil {
		t.Fatal("missing deterministic gate accepted")
	}
	configured.BaselineSHA = strings.Repeat("f", 40)
	if _, _, _, err := manager.Prepare(context.Background(), feature, consumer, "tester-1", configured, targets, []string{"missing"}); err == nil {
		t.Fatal("stale baseline accepted")
	}
	configured.BaselineSHA = baseline
	if err := os.WriteFile(filepath.Join(source, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := manager.Prepare(context.Background(), feature, consumer, "tester-1", configured, targets, []string{"missing"}); err == nil {
		t.Fatal("dirty source accepted")
	}
}

func TestCandidateSourceStatusAllowsOnlyInjectedOpenHandsHook(t *testing.T) {
	for _, test := range []struct {
		name   string
		status string
		clean  bool
	}{
		{name: "clean", clean: true},
		{name: "injected hook", status: "?? .openhands/hooks/sma_context_hook.py\n", clean: true},
		{name: "dirty source", status: " M organization/host.go\n", clean: false},
		{name: "other OpenHands file", status: "?? .openhands/notes.txt\n", clean: false},
		{name: "hook plus source", status: "?? .openhands/hooks/sma_context_hook.py\n?? organization/untracked_source.go\n", clean: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := candidateSourceStatusClean([]byte(test.status)); got != test.clean {
				t.Fatalf("candidateSourceStatusClean(%q) = %t, want %t", test.status, got, test.clean)
			}
		})
	}
}

func TestWorkspaceForExistingTaskResolvesEditableDynamicScope(t *testing.T) {
	baseDirectory := t.TempDir()
	dynamicDirectory := t.TempDir()
	resolver, err := openhands.NewBoundWorkspaceResolver([]openhands.WorkspaceBinding{
		{WorkspaceID: "coder-1", WorktreeID: "coder-default", WorkingDirectory: baseDirectory},
		{WorkspaceID: "coder-1", WorktreeID: "task-worktree", WorkingDirectory: dynamicDirectory},
		{WorkspaceID: "coder-1", WorktreeID: "candidate-without-binding-metadata", WorkingDirectory: dynamicDirectory},
	})
	if err != nil {
		t.Fatal(err)
	}
	task := organization.PlannedTask{ID: candidateTestUUID(71)}
	owner := organization.RoleInstanceState{WorkspaceID: "coder-1"}
	scope := kernel.TaskOperationalScope{TaskID: task.ID, WorkspaceID: "coder-1", WorktreeID: "task-worktree", Branch: "tekroo/task/example", BaselineSHA: strings.Repeat("a", 40), WritablePaths: []string{"."}}
	snapshot := kernel.Snapshot{TaskOperationalScopes: map[kernel.AggregateRef]kernel.TaskOperationalScope{{Kind: kernel.AggregateTask, ID: task.ID}: scope}}
	service := &ProductionService{
		workspaceResolver: resolver,
		workspacesByID: map[string]ProductionWorkspace{
			"coder-1": {WorkspaceID: "coder-1", WorktreeID: "coder-default", WorkingDirectory: baseDirectory, Branch: "main", BaselineSHA: strings.Repeat("b", 40), WritablePaths: []string{"."}},
		},
	}
	workspace, err := service.workspaceForExistingTask(context.Background(), task, owner, snapshot)
	if err != nil || workspace.WorkingDirectory != dynamicDirectory || workspace.WorktreeID != scope.WorktreeID || workspace.BaselineSHA != scope.BaselineSHA {
		t.Fatalf("dynamic task workspace=%#v err=%v", workspace, err)
	}
	scope.WorktreeID = "candidate-without-binding-metadata"
	snapshot.TaskOperationalScopes[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}] = scope
	if _, err := service.workspaceForExistingTask(context.Background(), task, owner, snapshot); err == nil {
		t.Fatal("candidate-prefixed scope without immutable candidate metadata was accepted")
	}
}

func candidateRepository(t *testing.T) (string, string, string) {
	t.Helper()
	repository := t.TempDir()
	candidateGit(t, repository, "init", "--initial-branch=source")
	candidateGit(t, repository, "config", "user.name", "Tekroo Test")
	candidateGit(t, repository, "config", "user.email", "test@tekroo.invalid")
	if err := os.WriteFile(filepath.Join(repository, "file.txt"), []byte("baseline\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	candidateGit(t, repository, "add", "file.txt")
	candidateGit(t, repository, "commit", "-m", "baseline")
	baseline := candidateGit(t, repository, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repository, "file.txt"), []byte("candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	candidateGit(t, repository, "add", "file.txt")
	candidateGit(t, repository, "commit", "-m", "candidate")
	if err := os.MkdirAll(filepath.Join(repository, ".openhands", "hooks"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, ".openhands", "hooks", "sma_context_hook.py"), []byte("# injected runtime hook\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return repository, baseline, candidateGit(t, repository, "rev-parse", "HEAD")
}

func candidateGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	command.Env = append(os.Environ(), "LC_ALL=C", "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
	return strings.TrimSpace(string(output))
}

func makeWritableForCleanup(t *testing.T, root string) {
	t.Helper()
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return os.Chmod(path, info.Mode().Perm()|0o200)
	}); err != nil {
		t.Fatal(err)
	}
}

func candidateTestUUID(value int) kernel.UUIDv7 {
	return kernel.UUIDv7("00000000-0000-7000-8000-" + fmt.Sprintf("%012d", value))
}

func candidateTestDigest(value byte) kernel.Digest {
	return kernel.Digest(strings.Repeat(string(value), 64))
}
