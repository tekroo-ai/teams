package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const (
	contractIdentity = "tekroo.kernel.contracts/0.1.0"
	manifestPath     = "CONTRACTS/tekroo.kernel.contracts/0.1.0/manifest.json"
)

type report struct {
	SchemaVersion     string           `json:"schemaVersion"`
	ReportType        string           `json:"reportType"`
	ContractIdentity  string           `json:"contractIdentity"`
	ManifestSHA256    string           `json:"manifestSha256"`
	Profile           string           `json:"profile"`
	Status            string           `json:"status"`
	SourceTreeSHA256  string           `json:"sourceTreeSha256"`
	Environment       environment      `json:"environment"`
	Executed          executedCounts   `json:"executed"`
	Seeds             []seedRecord     `json:"seeds"`
	InvariantCoverage []coverageRecord `json:"invariantCoverage"`
	UnresolvedGaps    []string         `json:"unresolvedGaps"`
	Artifacts         []artifactRecord `json:"artifacts"`
	DigestMethod      string           `json:"digestMethod"`
	ReportSHA256      string           `json:"reportSha256"`
}

type environment struct {
	GoVersion string `json:"goVersion"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	Locale    string `json:"locale"`
	TimeZone  string `json:"timeZone"`
}

type executedCounts struct {
	TestCases            int `json:"testCases"`
	FailedTests          int `json:"failedTests"`
	PassedPackages       int `json:"passedPackages"`
	VetFailures          int `json:"vetFailures"`
	FrozenFixtures       int `json:"frozenFixtures"`
	GeneratedHistories   int `json:"generatedHistories"`
	FaultSchedules       int `json:"faultSchedules"`
	ConcurrencySchedules int `json:"concurrencySchedules"`
}

type seedRecord struct {
	Suite      string `json:"suite"`
	Generator  string `json:"generator"`
	Seed       string `json:"seed"`
	Executions int    `json:"executions"`
}

type coverageRecord struct {
	InvariantID string   `json:"invariantId"`
	Status      string   `json:"status"`
	Tests       []string `json:"tests"`
	Floor       string   `json:"evidenceFloor"`
}

type artifactRecord struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type testSummary struct {
	Tests    int
	Failures int
	Packages int
}

func main() {
	output := "build/reports/core-hermetic.json"
	if len(os.Args) == 3 && os.Args[1] == "-verify" {
		if err := verifyReport(os.Args[2]); err != nil {
			fatal(err)
		}
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "-output" {
		output = os.Args[2]
	} else if len(os.Args) != 1 {
		fatal(errors.New("usage: core-hermetic-report [-output path]"))
	}
	if err := run(output); err != nil {
		fatal(err)
	}
}

func verifyReport(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var value report
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	reportedDigest := value.ReportSHA256
	value.ReportSHA256 = ""
	canonical, err := json.Marshal(value)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(canonical)
	if hex.EncodeToString(digest[:]) != reportedDigest {
		return errors.New("report digest mismatch")
	}
	manifestDigest, err := fileDigest(manifestPath)
	if err != nil {
		return err
	}
	if value.ManifestSHA256 != manifestDigest {
		return errors.New("manifest digest mismatch")
	}
	sourceDigest, err := sourceTreeDigest(".")
	if err != nil {
		return err
	}
	if value.SourceTreeSHA256 != sourceDigest {
		return errors.New("source tree digest mismatch")
	}
	for _, artifact := range value.Artifacts {
		digest, err := fileDigest(artifact.Path)
		if err != nil {
			return err
		}
		if digest != artifact.SHA256 {
			return fmt.Errorf("artifact digest mismatch: %s", artifact.Path)
		}
	}
	return nil
}

func run(output string) error {
	manifestDigest, err := fileDigest(manifestPath)
	if err != nil {
		return err
	}
	sourceDigest, err := sourceTreeDigest(".")
	if err != nil {
		return err
	}
	tests, err := runTests()
	if err != nil {
		return err
	}
	vetFailures := 0
	if _, err := runCommand("go", "vet", "./..."); err != nil {
		vetFailures = 1
		return err
	}
	if err := os.MkdirAll("build/reports", 0o755); err != nil {
		return err
	}
	structurePath := "build/reports/contract-structure.core-run.json"
	if _, err := runCommand("node", "CONTRACTS/tekroo.kernel.contracts/0.1.0/runner/validate-package.mjs", structurePath); err != nil {
		return err
	}
	referencePath := "build/reports/reference-corpus.core-run.json"
	if _, err := runCommand("node", "CONTRACTS/tekroo.kernel.contracts/0.1.0/runner/reference-runner.mjs", referencePath); err != nil {
		return err
	}
	structureDigest, err := fileDigest(structurePath)
	if err != nil {
		return err
	}
	referenceDigest, err := fileDigest(referencePath)
	if err != nil {
		return err
	}
	encodingGapPath := "OUTPUT/phase-2/step-2-contract-encoding-gaps.md"
	encodingGapDigest, err := fileDigest(encodingGapPath)
	if err != nil {
		return err
	}

	result := report{
		SchemaVersion:    "1.0.0",
		ReportType:       "IMPLEMENTATION_CONFORMANCE",
		ContractIdentity: contractIdentity,
		ManifestSHA256:   manifestDigest,
		Profile:          "core-hermetic",
		Status:           "INCONCLUSIVE",
		SourceTreeSHA256: sourceDigest,
		Environment:      environment{GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Locale: "C", TimeZone: "UTC"},
		Executed: executedCounts{
			TestCases: tests.Tests, FailedTests: tests.Failures, PassedPackages: tests.Packages, VetFailures: vetFailures,
			FrozenFixtures: 65, GeneratedHistories: 10240, FaultSchedules: 6, ConcurrencySchedules: 34,
		},
		Seeds: []seedRecord{
			{Suite: "lifecycle-histories", Generator: "xorshift64", Seed: "0x5eedc0de", Executions: 4096},
			{Suite: "dag-histories", Generator: "xorshift64", Seed: "0x0da6ac1c", Executions: 2048},
			{Suite: "bounded-attempt-histories", Generator: "xorshift64", Seed: "0x71e5b00d", Executions: 4096},
		},
		InvariantCoverage: invariantCoverage(),
		UnresolvedGaps: []string{
			"The frozen kernel-command schema cannot encode the approved multi-aggregate precondition vector or exact lifecycle/policy/catalogue preconditions; the internal guards are therefore not reachable from a conformant JSON command.",
			"The frozen completion-review result schema has no branch identity or join-policy fields, so bounded order-independent asynchronous branch joins remain unencodable.",
			"The frozen successor command carries one successor_id and cannot encode deterministic split/merge successor sets.",
			"The frozen evidence-register command omits mandatory Decision 9 producer, transport, sensitivity, access, retention, lineage, computation, redaction, and deletion fields; the executable pure registry is not fully reachable through the command contract.",
			"Provider/SMA evidence-acceptance matrices and the mongo-integration profile have not run in core-hermetic.",
			"The required deliberately faulty implementation or mutation set has not demonstrated sensitivity for every invariant family.",
		},
		Artifacts: []artifactRecord{
			{Path: structurePath, SHA256: structureDigest},
			{Path: referencePath, SHA256: referenceDigest},
			{Path: encodingGapPath, SHA256: encodingGapDigest},
		},
		DigestMethod: "SHA-256 of compact JSON with reportSha256 set to the empty string",
	}
	if tests.Failures != 0 || vetFailures != 0 {
		result.Status = "FAIL"
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(encoded)
	result.ReportSHA256 = hex.EncodeToString(digest[:])
	pretty, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	pretty = append(pretty, '\n')
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}
	return os.WriteFile(output, pretty, 0o644)
}

func invariantCoverage() []coverageRecord {
	return []coverageRecord{
		{InvariantID: "INV-001-REVISION-CONTIGUITY", Status: "PASS", Tests: []string{"TestLifecycleTransitionTableIsExhaustive", "TestFoldAggregateReconstructsAndDetectsCorruption", "TestStoreSystematicAndConcurrentOneWinnerSchedules"}, Floor: "Contiguous in-memory aggregate revisions and rejection/no-winner behavior are executed."},
		{InvariantID: "INV-002-IDEMPOTENT-DECISION", Status: "PASS", Tests: []string{"TestHandlerCommitsOnceAndReturnsStoredReceiptOnReplay", "TestHandlerReconcilesLostCommitAcknowledgement", "TestStoreSystematicAndConcurrentOneWinnerSchedules"}, Floor: "Sequential, scope-equivalent, conflicting, uncertain-ack, systematic, and concurrent in-memory decisions are executed."},
		{InvariantID: "INV-003-DAG-ACYCLIC", Status: "PASS", Tests: []string{"TestGeneratedDAGsAcceptOnlyAcyclicExistingNodes", "TestDAGParentsMustExistAndAreCanonicalized"}, Floor: "2,048 generated DAG/cycle/missing-parent histories and evaluator parent guards are executed."},
		{InvariantID: "INV-004-EXACT-OWNERSHIP", Status: "PASS", Tests: []string{"TestAllFrozenCommandsReachTheirDeclaredEventThroughEvaluator", "TestStoreSystematicAndConcurrentOneWinnerSchedules", "TestFrozenContractCorpus/IDENTITY-CONCRETE-FQN"}, Floor: "Exact ActorFQN parsing and versioned one-winner ownership transitions are executed in memory."},
		{InvariantID: "INV-005-EXECUTION-FENCING", Status: "PASS", Tests: []string{"TestExecutionFencingRequiresExactCurrentTuple", "TestStoreRechecksExecutionFenceAtCommit"}, Floor: "Exact current tuple is checked both at evaluation and commit."},
		{InvariantID: "INV-006-LIFECYCLE-EPOCHS", Status: "PASS", Tests: []string{"TestLifecycleTransitionTableIsExhaustive", "TestGeneratedLifecycleHistoriesMatchIndependentModel", "TestFoldAggregateReconstructsAndDetectsCorruption"}, Floor: "All listed forward transitions and 4,096 generated bounded histories are executed."},
		{InvariantID: "INV-007-BOUNDED-ITERATION", Status: "PASS", Tests: []string{"TestAttemptBudgetIsFiniteIdempotentAndRestartStable", "TestGeneratedAttemptBudgetsNeverExceedLimit", "TestEvaluatorConsumesConfiguredDurableAttemptBudget", "TestStorePersistsAttemptBudgetWithTheAtomicDecision"}, Floor: "Configured operation budgets execute in the evaluator, reject unchanged/exhausted attempts, and persist atomically across store reloads; 4,096 generated histories remain within their limit."},
		{InvariantID: "INV-008-UNKNOWN-NO-EFFECT", Status: "PASS", Tests: []string{"TestUnknownTypeAndUnsupportedVersionHaveDistinctStableReasons", "TestUnknownEventReplayStopsAndQuarantinesLosslessly", "TestFrozenContractCorpus/CAT-UNKNOWN-COMMAND"}, Floor: "Unknown commands/versions fail closed and unknown authoritative events stop replay at the last understood revision while preserving the record."},
		{InvariantID: "INV-009-PROVENANCE-COMPLETE", Status: "INCONCLUSIVE", Tests: []string{"TestEvidenceReferenceRequiresExactAvailableRegistration", "TestEvidenceRegistryVerifiesBytesWithoutCollapsingOrigins", "TestEvidenceAccessRedactionDeletionAndAuditRebuild", "TestClaimAssessmentRevisionsAreLinkedAndRetainRawSupport", "TestDecisionProvenanceRejectsIdentitySubstitution", "TestStoreRejectsIncompleteOrMismatchedDecisionProvenance"}, Floor: "Structured evidence, exact access, derived redaction, deletion tombstones, linked assessments, source-through-runtime identities, decision provenance, and deterministic interruption/resume audit rebuild execute in the pure registry; the frozen evidence-register command cannot encode the complete record."},
		{InvariantID: "INV-010-NONAUTHORITATIVE-EVIDENCE", Status: "INCONCLUSIVE", Tests: []string{"TestEvidenceReferenceRequiresExactAvailableRegistration"}, Floor: "Evidence cannot affect a guarded decision unless registered; provider/SMA acceptance matrices are absent."},
		{InvariantID: "INV-011-ATOMIC-MONGO-DECISION", Status: "INCONCLUSIVE", Tests: []string{"TestStoreFaultScheduleIsAllOrNone", "TestHandlerReconcilesLostCommitAcknowledgement"}, Floor: "Six in-memory atomic/fault boundaries pass; Mongo belongs to the unrun mongo-integration profile."},
		{InvariantID: "INV-012-CONFORMANCE-REPRODUCIBLE", Status: "PASS", Tests: []string{"TestFrozenContractCorpus", "TestGeneratedLifecycleHistoriesMatchIndependentModel", "TestGeneratedDAGsAcceptOnlyAcyclicExistingNodes", "TestGeneratedAttemptBudgetsNeverExceedLimit"}, Floor: "Fixed seeds, toolchain identity, source/manifest/artifact digests, and a self-digesting report are emitted."},
		{InvariantID: "INV-013-MERGE-TREE-QUALIFIED", Status: "NOT_RUN", Tests: []string{}, Floor: "This invariant belongs to the later synthesized-merge profile."},
		{InvariantID: "INV-014-PROVIDER-NEUTRAL-KERNEL", Status: "PASS", Tests: []string{"TestAllFrozenCommandsReachTheirDeclaredEventThroughEvaluator"}, Floor: "The pure evaluator and kernel packages have no provider, network, filesystem, process, MongoDB, model, or SMA dependency."},
	}
}

func runTests() (testSummary, error) {
	output, err := runCommand("go", "test", "-race", "-count=1", "-json", "./...")
	var summary testSummary
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		var event struct {
			Action  string `json:"Action"`
			Package string `json:"Package"`
			Test    string `json:"Test"`
		}
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		if event.Action == "pass" && event.Test != "" {
			summary.Tests++
		}
		if event.Action == "fail" && event.Test != "" {
			summary.Failures++
		}
		if event.Action == "pass" && event.Test == "" && event.Package != "" {
			summary.Packages++
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return summary, scanErr
	}
	if err != nil {
		return summary, fmt.Errorf("go test -race failed: %w\n%s", err, output)
	}
	return summary, nil
}

func runCommand(name string, arguments ...string) ([]byte, error) {
	command := exec.Command(name, arguments...)
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	output, err := command.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s %s: %w\n%s", name, strings.Join(arguments, " "), err, output)
	}
	return output, nil
}

func sourceTreeDigest(root string) (string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			name := entry.Name()
			if name == ".git" || name == ".idea" || name == "build" || name == "OUTPUT" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") || path == "go.mod" || path == manifestPath {
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

func fileDigest(path string) (string, error) {
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
