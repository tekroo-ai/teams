package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

const manifestPath = "CONTRACTS/tekroo.kernel.contracts/0.2.0/manifest.json"

var (
	pointerPattern  = regexp.MustCompile(`\(0x[0-9a-f]+\)`)
	durationPattern = regexp.MustCompile(`\b[0-9]+(?:\.[0-9]+)?s\b`)
)

type edit struct {
	Old string
	New string
}

type mutant struct {
	ID          string
	InvariantID string
	Description string
	File        string
	Edits       []edit
	Package     string
	Test        string
}

type mutationResult struct {
	MutantID             string `json:"mutantId"`
	InvariantID          string `json:"invariantId"`
	Description          string `json:"description"`
	SourceFile           string `json:"sourceFile"`
	MutationSHA256       string `json:"mutationSha256"`
	DetectorPackage      string `json:"detectorPackage"`
	DetectorTest         string `json:"detectorTest"`
	Status               string `json:"status"`
	ExitCode             int    `json:"exitCode"`
	DetectorOutputPath   string `json:"detectorOutputPath"`
	DetectorOutputSHA256 string `json:"detectorOutputSha256"`
}

type report struct {
	SchemaVersion    string           `json:"schemaVersion"`
	ReportType       string           `json:"reportType"`
	ContractIdentity string           `json:"contractIdentity"`
	ManifestSHA256   string           `json:"manifestSha256"`
	Profile          string           `json:"profile"`
	Status           string           `json:"status"`
	SourceTreeSHA256 string           `json:"sourceTreeSha256"`
	GoVersion        string           `json:"goVersion"`
	MutantCount      int              `json:"mutantCount"`
	Killed           int              `json:"killed"`
	Survived         int              `json:"survived"`
	Errors           int              `json:"errors"`
	Results          []mutationResult `json:"results"`
	OutOfScope       []string         `json:"outOfScopeInvariantIds"`
	DigestMethod     string           `json:"digestMethod"`
	ReportSHA256     string           `json:"reportSha256"`
}

func main() {
	output := "build/reports/core-mutations.json"
	if len(os.Args) == 3 && os.Args[1] == "-verify" {
		if err := verify(os.Args[2]); err != nil {
			fatal(err)
		}
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "-output" {
		output = os.Args[2]
	} else if len(os.Args) != 1 {
		fatal(errors.New("usage: core-mutation-report [-output path] | -verify path"))
	}
	if err := run(output); err != nil {
		fatal(err)
	}
}

func run(output string) error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return errors.New("run from repository root")
	}
	temporaryRoot, err := os.MkdirTemp("", "tekroo-core-mutations-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporaryRoot)
	if err := copyRepository(root, temporaryRoot); err != nil {
		return err
	}

	artifactRoot := strings.TrimSuffix(output, filepath.Ext(output))
	if err := os.MkdirAll(artifactRoot, 0o755); err != nil {
		return err
	}
	results := make([]mutationResult, 0, len(mutants()))
	for _, item := range mutants() {
		result, err := executeMutant(temporaryRoot, artifactRoot, item)
		if err != nil {
			return err
		}
		results = append(results, result)
	}

	manifestDigest, err := digestFile(manifestPath)
	if err != nil {
		return err
	}
	sourceDigest, err := sourceTreeDigest(".")
	if err != nil {
		return err
	}
	value := report{
		SchemaVersion: "1.1.0", ReportType: "MUTATION_SENSITIVITY", ContractIdentity: "tekroo.kernel.contracts/0.2.0",
		ManifestSHA256: manifestDigest, Profile: "core-hermetic", Status: "PASS", SourceTreeSHA256: sourceDigest,
		GoVersion: runtime.Version(), MutantCount: len(results), Results: results,
		OutOfScope:   []string{"INV-011-ATOMIC-MONGO-DECISION", "INV-013-MERGE-TREE-QUALIFIED"},
		DigestMethod: "SHA-256 of compact JSON with reportSha256 set to the empty string",
	}
	for _, result := range results {
		switch result.Status {
		case "KILLED":
			value.Killed++
		case "SURVIVED":
			value.Survived++
		case "ERROR":
			value.Errors++
		}
	}
	if value.Survived > 0 || value.Errors > 0 {
		value.Status = "FAIL"
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(encoded)
	value.ReportSHA256 = hex.EncodeToString(digest[:])
	pretty, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(output, append(pretty, '\n'), 0o644); err != nil {
		return err
	}
	if value.Status != "PASS" {
		return errors.New("one or more core mutants survived or errored")
	}
	return nil
}

func executeMutant(temporaryRoot, artifactRoot string, item mutant) (mutationResult, error) {
	path := filepath.Join(temporaryRoot, filepath.FromSlash(item.File))
	original, err := os.ReadFile(path)
	if err != nil {
		return mutationResult{}, err
	}
	mutated := string(original)
	mutationHash := sha256.New()
	mutationHash.Write([]byte(item.File))
	for _, change := range item.Edits {
		if strings.Count(mutated, change.Old) != 1 {
			return mutationResult{}, fmt.Errorf("mutant %s source anchor count is not one", item.ID)
		}
		mutated = strings.Replace(mutated, change.Old, change.New, 1)
		mutationHash.Write([]byte{0})
		mutationHash.Write([]byte(change.Old))
		mutationHash.Write([]byte{0})
		mutationHash.Write([]byte(change.New))
	}
	if err := os.WriteFile(path, []byte(mutated), 0o644); err != nil {
		return mutationResult{}, err
	}
	defer os.WriteFile(path, original, 0o644)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-count=1", item.Package, "-run", "^"+item.Test+"$")
	command.Dir = temporaryRoot
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	output, runErr := command.CombinedOutput()
	normalized := strings.ReplaceAll(string(output), temporaryRoot, "<DISPOSABLE_REPOSITORY>")
	normalized = pointerPattern.ReplaceAllString(normalized, "(<POINTER>)")
	normalized = durationPattern.ReplaceAllString(normalized, "<DURATION>")
	artifactPath := filepath.Join(artifactRoot, item.ID+".txt")
	if err := os.WriteFile(artifactPath, []byte(normalized), 0o644); err != nil {
		return mutationResult{}, err
	}
	artifactDigest := sha256.Sum256([]byte(normalized))
	result := mutationResult{
		MutantID: item.ID, InvariantID: item.InvariantID, Description: item.Description, SourceFile: item.File,
		MutationSHA256: hex.EncodeToString(mutationHash.Sum(nil)), DetectorPackage: item.Package, DetectorTest: item.Test,
		ExitCode: 0, DetectorOutputPath: filepath.ToSlash(artifactPath), DetectorOutputSHA256: hex.EncodeToString(artifactDigest[:]),
	}
	if ctx.Err() != nil {
		result.Status = "ERROR"
		result.ExitCode = -1
		return result, nil
	}
	if runErr == nil {
		result.Status = "SURVIVED"
		return result, nil
	}
	var exitError *exec.ExitError
	if !errors.As(runErr, &exitError) {
		result.Status = "ERROR"
		result.ExitCode = -1
		return result, nil
	}
	result.ExitCode = exitError.ExitCode()
	detectorName := strings.Split(item.Test, "/")[0]
	if strings.Contains(normalized, "--- FAIL: "+detectorName) {
		result.Status = "KILLED"
	} else {
		result.Status = "ERROR"
	}
	return result, nil
}

func verify(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var value report
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	reported := value.ReportSHA256
	value.ReportSHA256 = ""
	canonical, err := json.Marshal(value)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(canonical)
	if hex.EncodeToString(digest[:]) != reported {
		return errors.New("mutation report digest mismatch")
	}
	manifestDigest, err := digestFile(manifestPath)
	if err != nil || manifestDigest != value.ManifestSHA256 {
		return errors.New("mutation report manifest mismatch")
	}
	sourceDigest, err := sourceTreeDigest(".")
	if err != nil || sourceDigest != value.SourceTreeSHA256 {
		return errors.New("mutation report source tree mismatch")
	}
	if value.Status != "PASS" || value.Killed != value.MutantCount || value.Survived != 0 || value.Errors != 0 {
		return errors.New("mutation report is not a complete pass")
	}
	for _, result := range value.Results {
		artifactDigest, err := digestFile(result.DetectorOutputPath)
		if err != nil || artifactDigest != result.DetectorOutputSHA256 {
			return fmt.Errorf("mutation artifact mismatch: %s", result.MutantID)
		}
	}
	return nil
}

func mutants() []mutant {
	return []mutant{
		{ID: "MUT-INV001-NONCONTIGUOUS", InvariantID: "INV-001-REVISION-CONTIGUITY", Description: "Accept event revision gaps.", File: "adapters/memory/store.go", Edits: []edit{{Old: "event.AggregateRevision != expected.Revision+uint64(index)+1", New: "event.AggregateRevision != expected.Revision+uint64(index)+2"}}, Package: "./adapters/memory", Test: "TestStoreFaultScheduleIsAllOrNone"},
		{ID: "MUT-INV002-REPLAY-REEXECUTES", InvariantID: "INV-002-IDEMPOTENT-DECISION", Description: "Ignore a stored receipt and execute a replay again.", File: "application/handler.go", Edits: []edit{{Old: "} else if found {\n\t\treturn receipt, nil\n\t}", New: "} else if false && found {\n\t\treturn receipt, nil\n\t}"}}, Package: "./application", Test: "TestHandlerCommitsOnceAndReturnsStoredReceiptOnReplay"},
		{ID: "MUT-INV003-ALLOW-CYCLE", InvariantID: "INV-003-DAG-ACYCLIC", Description: "Accept a graph after topological traversal misses nodes.", File: "kernel/dag.go", Edits: []edit{{Old: "if visited != len(nodes) {", New: "if false && visited != len(nodes) {"}}, Package: "./kernel", Test: "TestGeneratedDAGsAcceptOnlyAcyclicExistingNodes"},
		{ID: "MUT-INV004-WILDCARD-ACTOR", InvariantID: "INV-004-EXACT-OWNERSHIP", Description: "Accept an invalid actor FQN.", File: "kernel/types.go", Edits: []edit{{Old: "if !actorFQNPattern.MatchString(value) {", New: "if false && !actorFQNPattern.MatchString(value) {"}}, Package: "./internal/conformance", Test: "TestFrozenContractCorpus/IDENTITY-REJECT-WILDCARD"},
		{ID: "MUT-INV005-STALE-EXECUTION", InvariantID: "INV-005-EXECUTION-FENCING", Description: "Accept any registered execution instead of the exact current tuple.", File: "kernel/evaluator.go", Edits: []edit{{Old: "return found && current == *command.Execution", New: "return found || current == *command.Execution"}}, Package: "./kernel", Test: "TestExecutionFencingRequiresExactCurrentTuple"},
		{ID: "MUT-INV006-STALE-LIFECYCLE", InvariantID: "INV-006-LIFECYCLE-EPOCHS", Description: "Bypass the exact lifecycle epoch comparison.", File: "kernel/evaluator.go", Edits: []edit{{Old: "return *command.ExpectedLifecycleEpoch == snapshot.State.LifecycleEpoch", New: "return true"}}, Package: "./kernel", Test: "TestEvaluatorRejectsStaleCataloguePolicyAndLifecycleContext"},
		{ID: "MUT-INV006-REVIEW-CONFLICT", InvariantID: "INV-006-LIFECYCLE-EPOCHS", Description: "Ignore contradictory duplicate review-branch results.", File: "kernel/coordination.go", Edits: []edit{{Old: "if prior, duplicate := observed[result.BranchID]; duplicate && prior != result.Result {", New: "if prior, duplicate := observed[result.BranchID]; false && duplicate && prior != result.Result {"}}, Package: "./internal/conformance", Test: "TestFrozenContractCorpus/REVIEW-JOIN-DUPLICATE-CONFLICT"},
		{ID: "MUT-INV006-UNORDERED-SUCCESSORS", InvariantID: "INV-006-LIFECYCLE-EPOCHS", Description: "Accept a non-canonical successor set.", File: "kernel/coordination.go", Edits: []edit{{Old: "if index > 0 && ids[index-1] >= id {", New: "if false && index > 0 && ids[index-1] >= id {"}, {Old: "return sort.SliceIsSorted(ids, func(i, j int) bool { return ids[i] < ids[j] })", New: "return len(ids) > 0 || sort.SliceIsSorted(ids, func(i, j int) bool { return ids[i] < ids[j] })"}}, Package: "./internal/conformance", Test: "TestFrozenContractCorpus/SUCCESSOR-SET-REJECT-UNORDERED"},
		{ID: "MUT-INV007-EXCEED-BUDGET", InvariantID: "INV-007-BOUNDED-ITERATION", Description: "Permit one attempt after the configured limit.", File: "kernel/budget.go", Edits: []edit{{Old: "if b.Limit == 0 || b.Used >= b.Limit {", New: "if b.Limit == 0 || b.Used > b.Limit {"}}, Package: "./kernel", Test: "TestAttemptBudgetIsFiniteIdempotentAndRestartStable"},
		{ID: "MUT-INV008-UNKNOWN-AS-VERSION", InvariantID: "INV-008-UNKNOWN-NO-EFFECT", Description: "Skip the unknown-command rejection and reinterpret it as a version error.", File: "contract/catalogue.go", Edits: []edit{{Old: "entry, found := c.commands[commandType]\n\tif !found {\n\t\treturn kernel.CommandDefinition{}, fmt.Errorf(\"%w: %w\", ErrInvalidCommand, kernel.ErrUnknownCommand)\n\t}", New: "entry, found := c.commands[commandType]\n\tif false && !found {\n\t\treturn kernel.CommandDefinition{}, fmt.Errorf(\"%w: %w\", ErrInvalidCommand, kernel.ErrUnknownCommand)\n\t}"}}, Package: "./kernel", Test: "TestUnknownTypeAndUnsupportedVersionHaveDistinctStableReasons"},
		{ID: "MUT-INV009-SUBSTITUTE-BUILD", InvariantID: "INV-009-PROVENANCE-COMPLETE", Description: "Allow runtime build identity to differ from the built artifact.", File: "kernel/provenance.go", Edits: []edit{{Old: "basis.Runtime.BuildArtifactDigest != basis.Build.ArtifactDigest", New: "false"}}, Package: "./kernel", Test: "TestDecisionProvenanceRejectsIdentitySubstitution"},
		{ID: "MUT-INV010-UNREGISTERED-EVIDENCE", InvariantID: "INV-010-NONAUTHORITATIVE-EVIDENCE", Description: "Bypass evidence registration and availability guards.", File: "kernel/evaluator.go", Edits: []edit{{Old: "if !validEvidenceRefs(command, snapshot) {", New: "if false && !validEvidenceRefs(command, snapshot) {"}}, Package: "./kernel", Test: "TestEvidenceReferenceRequiresExactAvailableRegistration"},
		{ID: "MUT-INV012-NONCANONICAL-PRECONDITIONS", InvariantID: "INV-012-CONFORMANCE-REPRODUCIBLE", Description: "Reverse canonical aggregate-ID ordering within one kind.", File: "kernel/evaluator.go", Edits: []edit{{Old: "return preconditions[i].Aggregate.ID < preconditions[j].Aggregate.ID", New: "return preconditions[i].Aggregate.ID > preconditions[j].Aggregate.ID"}}, Package: "./kernel", Test: "TestEvaluatorEnforcesCanonicalExactRelatedPreconditions"},
		{ID: "MUT-INV014-NETWORK-DEPENDENCY", InvariantID: "INV-014-PROVIDER-NEUTRAL-KERNEL", Description: "Introduce a production network dependency into the pure kernel.", File: "kernel/types.go", Edits: []edit{{Old: "\t\"errors\"\n", New: "\t\"errors\"\n\t_ \"net/http\"\n"}}, Package: "./internal/conformance", Test: "TestKernelProductionImportsRemainProviderNeutral"},
	}
}

func copyRepository(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if relative == ".git" || relative == "build" || relative == "OUTPUT" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(destination, relative), 0o755)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			input.Close()
			return err
		}
		output, err := os.OpenFile(filepath.Join(destination, relative), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		inputCloseErr := input.Close()
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if inputCloseErr != nil {
			return inputCloseErr
		}
		return closeErr
	})
}

func sourceTreeDigest(root string) (string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "build" || entry.Name() == "OUTPUT" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") || path == "go.mod" || filepath.ToSlash(path) == manifestPath {
			paths = append(paths, filepath.ToSlash(path))
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		hash.Write([]byte(path))
		hash.Write([]byte{0})
		hash.Write(data)
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func digestFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
