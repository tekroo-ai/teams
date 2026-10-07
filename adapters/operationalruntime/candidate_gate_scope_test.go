package operationalruntime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/openhands"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestComponentBuildDoesNotRequireUnfinishedConsumerButIntegrationDoes(t *testing.T) {
	dir, _, _ := candidateRepository(t)
	for name, text := range map[string]string{
		"go.mod":           "module example.test/gate\n\ngo 1.24\n",
		"provider/api.go":  "package provider\ntype API interface { NewMethod() }\nfunc Use(a API) {}\n",
		"consumer/main.go": "package main\nimport \"example.test/gate/provider\"\ntype Impl struct{}\nfunc main() { provider.Use(Impl{}) }\n",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0700); err != nil {
			t.Fatal(err)
		}
		commitTaskFile(t, dir, name, text, "fixture "+name)
	}
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	gate := ProductionCandidateGate{GateID: "build", Command: []string{goBinary, "build", "./..."}, Timeout: "30s", Scope: taskPackageGateScope}
	manager := &candidateWorkspaceManager{root: t.TempDir(), gitBinary: "git", timeout: time.Second, gates: map[string]ProductionCandidateGate{gate.GateID: gate}}
	component, digest, err := manager.withGateScope(dir, []string{gate.GateID}, [][]string{{"provider/"}})
	if err != nil || !digest.Valid() || !slices.Equal(component.gates[gate.GateID].Command, []string{goBinary, "build", "./provider"}) {
		t.Fatal("incorrect component boundary", err)
	}
	receipts, err := component.runGates(t.Context(), dir, []string{gate.GateID})
	if err != nil || len(receipts) != 1 || !gateReceiptMatchesScope(receipts[0], gate) {
		t.Fatal("component rejected unfinished integration", err)
	}
	whole, fullDigest, err := manager.withGateScope(dir, []string{gate.GateID}, nil)
	if err != nil || digest == fullDigest || !slices.Equal(whole.gates[gate.GateID].Command, gate.Command) {
		t.Fatal("whole-feature boundary weakened", err)
	}
	if _, err := whole.runGates(t.Context(), dir, []string{gate.GateID}); err == nil {
		t.Fatal("broken whole-feature candidate passed")
	}
	commitTaskFile(t, dir, "consumer/main.go", "package main\nimport \"example.test/gate/provider\"\ntype Impl struct{}\nfunc (Impl) NewMethod() {}\nfunc main() { provider.Use(Impl{}) }\n", "consumer completes handoff")
	if _, err := whole.runGates(t.Context(), dir, []string{gate.GateID}); err != nil {
		t.Fatal("completed integration failed", err)
	}
	bad := receipts[0]
	bad.PackageTargets = []string{"./../escape"}
	if gateReceiptMatchesScope(bad, gate) {
		t.Fatal("escaping package target accepted")
	}
	if _, _, err := manager.withGateScope(dir, []string{gate.GateID}, [][]string{{}}); err == nil {
		t.Fatal("empty component scope silently widened")
	}
	if _, err := goScopePackages(dir, []string{"../outside"}); err == nil {
		t.Fatal("escaping write scope accepted")
	}
}

func TestScopedCandidateReceiptSurvivesMaterializationAndRestart(t *testing.T) {
	repository, baseline, _ := candidateRepository(t)
	if err := os.Remove(filepath.Join(repository, ".openhands", "hooks", "sma_context_hook.py")); err != nil {
		t.Fatal(err)
	}
	commitTaskFile(t, repository, "go.mod", "module example.test/scoped\n\ngo 1.24\n", "module")
	if err := os.MkdirAll(filepath.Join(repository, "provider"), 0700); err != nil {
		t.Fatal(err)
	}
	commitTaskFile(t, repository, "provider/api.go", "package provider\ntype API interface { NewMethod() }\n", "provider")
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	gates := []ProductionCandidateGate{{GateID: "build", Command: []string{goBinary, "build", "./..."}, Timeout: "30s", Scope: taskPackageGateScope}}
	root := t.TempDir()
	t.Cleanup(func() { makeWritableForCleanup(t, root) })
	newManager := func() *candidateWorkspaceManager {
		resolver, err := openhands.NewBoundWorkspaceResolver([]openhands.WorkspaceBinding{{WorkspaceID: "tester-1", WorktreeID: "tester-default", WorkingDirectory: t.TempDir()}})
		if err != nil {
			t.Fatal(err)
		}
		manager, err := newCandidateWorkspaceManagerWithMode(context.Background(), root, "git", time.Minute, gates, resolver, false)
		if err != nil {
			t.Fatal(err)
		}
		return manager
	}
	feature := organization.FeatureRequest{ID: candidateTestUUID(2911)}
	consumer := organization.PlannedTask{ID: candidateTestUUID(2912), Owner: "teams::tester-1"}
	targets := []candidateTargetReceipt{{TaskID: candidateTestUUID(2913), InvocationID: candidateTestUUID(2914), OutputSHA256: candidateTestDigest('a'), TerminalEvidenceIDs: []kernel.UUIDv7{candidateTestUUID(2915)}}}
	source := ProductionWorkspace{WorkspaceID: "coder-1", WorktreeID: "coder-source", WorkingDirectory: repository, Branch: "source", BaselineSHA: baseline, WritablePaths: []string{"."}}
	manager := newManager()
	workspace, receipt, digest, err := manager.Prepare(t.Context(), feature, consumer, "tester-1", source, targets, []string{"build"}, []string{"provider/"})
	if err != nil || len(receipt.GateReceipts) != 1 || !slices.Equal(receipt.GateReceipts[0].PackageTargets, []string{"./provider"}) {
		t.Fatalf("scoped materialization: %+v %v", receipt, err)
	}
	for _, current := range []*candidateWorkspaceManager{manager, newManager()} {
		reused, _, retainedDigest, err := current.Prepare(t.Context(), feature, consumer, "tester-1", source, targets, []string{"build"}, []string{"provider/"})
		if err != nil || reused.WorkingDirectory != workspace.WorkingDirectory || retainedDigest != digest {
			t.Fatalf("scoped restart: %+v %s %v", reused, retainedDigest, err)
		}
	}
}
