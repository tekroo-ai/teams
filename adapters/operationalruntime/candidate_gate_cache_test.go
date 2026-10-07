package operationalruntime

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGateReuseOnlyBindsIdenticalInputsAndNeverCachesFailure(t *testing.T) {
	dir, _, _ := candidateRepository(t)
	gate := ProductionCandidateGate{GateID: "head", Command: []string{"git", "rev-parse", "HEAD"}, Timeout: "1s", ReuseInputsSHA256: repeatedDigest('c')}
	manager := &candidateWorkspaceManager{root: t.TempDir(), gitBinary: "git", timeout: time.Second, gates: map[string]ProductionCandidateGate{"head": gate}}
	first, err := manager.runGates(t.Context(), dir, []string{"head"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.runGates(t.Context(), dir, []string{"head"})
	if err != nil {
		t.Fatal(err)
	}
	if second[0].ReusedFrom == "" || first[0].ReceiptID != second[0].ReceiptID {
		t.Fatalf("not reused: %+v %+v", first, second)
	}
	commitTaskFile(t, dir, "successor.txt", "successor\n", "successor")
	third, err := manager.runGates(t.Context(), dir, []string{"head"})
	if err != nil || third[0].ReusedFrom != "" || third[0].ReceiptID == first[0].ReceiptID {
		t.Fatalf("changed tree reused: %+v %v", third, err)
	}
	gate.ReuseInputsSHA256 = repeatedDigest('d')
	manager.gates["head"] = gate
	fourth, err := manager.runGates(t.Context(), dir, []string{"head"})
	if err != nil || fourth[0].ReusedFrom != "" {
		t.Fatalf("changed declared external inputs reused: %+v %v", fourth, err)
	}
	gate.Command = []string{"git", "show", "HEAD:missing"}
	manager.gates["head"] = gate
	if _, err := manager.runGates(t.Context(), dir, []string{"head"}); err == nil {
		t.Fatal("failure passed")
	}
	entries, err := os.ReadDir(filepath.Join(manager.root, "gate-cache"))
	if err != nil || len(entries) != 3 {
		t.Fatalf("cache count=%d error=%v", len(entries), err)
	}
	gate.ReuseInputsSHA256 = ""
	manager.gates["head"] = gate
	if _, err := candidateGateCacheKey(t.Context(), gate, nil, dir, "invalid", "invalid"); err != nil {
		t.Fatal("disabled cache evaluated input fingerprint")
	}
}

func TestGateCacheBindsEnvironmentExecutableAndRejectsCorruption(t *testing.T) {
	dir, _, commit := candidateRepository(t)
	tree := candidateGit(t, dir, "rev-parse", "HEAD^{tree}")
	binary := filepath.Join(t.TempDir(), "fixture-tool")
	if err := os.WriteFile(binary, []byte("binary-v1"), 0700); err != nil {
		t.Fatal(err)
	}
	gate := ProductionCandidateGate{GateID: "fixture", Command: []string{binary}, Timeout: "1s", ReuseInputsSHA256: repeatedDigest('c')}
	key, err := candidateGateCacheKey(t.Context(), gate, []string{"LANG=C"}, dir, commit, tree)
	if err != nil {
		t.Fatal(err)
	}
	other, err := candidateGateCacheKey(t.Context(), gate, []string{"LANG=en_US"}, dir, commit, tree)
	if err != nil || key == other {
		t.Fatal("environment mutation reused fingerprint", err)
	}
	if err := os.WriteFile(binary, []byte("binary-v2"), 0700); err != nil {
		t.Fatal(err)
	}
	other, err = candidateGateCacheKey(t.Context(), gate, []string{"LANG=C"}, dir, commit, tree)
	if err != nil || key == other {
		t.Fatal("executable mutation reused fingerprint", err)
	}
	manager := &candidateWorkspaceManager{root: t.TempDir()}
	root := filepath.Join(manager.root, "gate-cache")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, key+".json"), []byte(`{"input_sha256":"corrupt"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, found, err := manager.readGateCache(key, gate); err == nil || found {
		t.Fatal("corrupted receipt reused")
	}
}
