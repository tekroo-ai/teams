package operationalruntime

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tekroo-ai/teams/kernel"
)

const taskPackageGateScope = "task-packages"

func validGateScope(gate ProductionCandidateGate) bool {
	if gate.Scope == "" || gate.Scope == "repository" {
		return true
	}
	return gate.Scope == taskPackageGateScope && len(gate.Command) == 3 && filepath.Base(gate.Command[0]) == "go" && (gate.Command[1] == "build" || gate.Command[1] == "test") && gate.Command[2] == "./..."
}

// Select packages from the accepted write scope, never from compiler-error
// text or a guessed integration boundary. Imported Go dependencies still build.
func goScopePackages(directory string, scope []string) ([]string, error) {
	workspaceRoot, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return nil, err
	}
	packages := make(map[string]struct{})
	for _, path := range scope {
		path = strings.TrimSuffix(path, "/")
		if path == "" || filepath.IsAbs(path) || path != filepath.Clean(path) || path == ".." || strings.HasPrefix(path, "../") || strings.ContainsAny(path, "*?[\\") {
			return nil, fmt.Errorf("%w: invalid component scope %q", errInvalidCandidateWorkspace, path)
		}
		root := filepath.Join(directory, path)
		resolved, err := filepath.EvalSymlinks(root)
		if err != nil || resolved != filepath.Join(workspaceRoot, path) {
			return nil, fmt.Errorf("%w: component scope traverses a symlink or is unavailable: %q", errInvalidCandidateWorkspace, path)
		}
		info, err := os.Lstat(root)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errInvalidCandidateWorkspace
		}
		if !info.IsDir() {
			if strings.HasSuffix(path, ".go") {
				packages["./"+filepath.Dir(path)] = struct{}{}
			}
			continue
		}
		if err := filepath.WalkDir(root, func(file string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return errInvalidCandidateWorkspace
			}
			if entry.IsDir() && file != root && (entry.Name() == "vendor" || entry.Name() == "testdata" || strings.HasPrefix(entry.Name(), ".")) {
				return filepath.SkipDir
			}
			if !entry.IsDir() && strings.HasSuffix(file, ".go") {
				rel, err := filepath.Rel(directory, filepath.Dir(file))
				if err != nil {
					return err
				}
				packages["./"+filepath.ToSlash(rel)] = struct{}{}
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	result := make([]string, 0, len(packages))
	for p := range packages {
		result = append(result, p)
	}
	slices.Sort(result)
	if len(result) == 0 {
		return nil, fmt.Errorf("%w: component gate has no Go packages in its accepted write scope", errInvalidCandidateWorkspace)
	}
	return result, nil
}

func (manager *candidateWorkspaceManager) withGateScope(directory string, required []string, componentScope [][]string) (*candidateWorkspaceManager, kernel.Digest, error) {
	if len(componentScope) > 1 {
		return nil, "", errInvalidCandidateWorkspace
	}
	copy := *manager
	copy.gates = make(map[string]ProductionCandidateGate, len(manager.gates))
	for id, gate := range manager.gates {
		copy.gates[id] = gate
	}
	policy := make([]ProductionCandidateGate, 0, len(required))
	for _, id := range required {
		gate, found := manager.gates[id]
		if !found || !validGateScope(gate) {
			return nil, "", errInvalidCandidateWorkspace
		}
		if gate.Scope == taskPackageGateScope && len(componentScope) == 1 {
			packages, err := goScopePackages(directory, componentScope[0])
			if err != nil {
				return nil, "", err
			}
			gate.packageTargets = packages
			gate.Command = append(append([]string(nil), gate.Command[:2]...), packages...)
		}
		copy.gates[id] = gate
		policy = append(policy, gate)
	}
	encoded, err := json.Marshal(policy)
	if err != nil {
		return nil, "", err
	}
	return &copy, digestBytes(encoded), nil
}

func gateReceiptMatchesScope(receipt candidateGateReceipt, gate ProductionCandidateGate) bool {
	if len(receipt.PackageTargets) == 0 {
		return len(gate.packageTargets) == 0 && slices.Equal(receipt.Command, gate.Command)
	}
	if gate.Scope != taskPackageGateScope || !slices.IsSorted(receipt.PackageTargets) || len(slices.Compact(append([]string(nil), receipt.PackageTargets...))) != len(receipt.PackageTargets) {
		return false
	}
	for _, p := range receipt.PackageTargets {
		if !strings.HasPrefix(p, "./") || strings.Contains(p, "...") || strings.ContainsAny(p, "*?[\\") || strings.TrimPrefix(p, "./") != filepath.Clean(strings.TrimPrefix(p, "./")) || strings.HasPrefix(p, "./../") || p == "./.." {
			return false
		}
	}
	if len(gate.packageTargets) != 0 {
		return slices.Equal(gate.packageTargets, receipt.PackageTargets) && slices.Equal(gate.Command, receipt.Command)
	}
	return validGateScope(gate) && slices.Equal(receipt.Command, append(append([]string(nil), gate.Command[:2]...), receipt.PackageTargets...))
}
