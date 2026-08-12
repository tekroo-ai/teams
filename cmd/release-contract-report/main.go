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
	baseCommit             = "6dc8bc98a56d45829fdbb58778242d5f2077aaef"
	baseTree               = "f7c77fd2ef7e5bec106ff598dfc3d3ae8c790198"
	contractRoot           = "CONTRACTS/tekroo.kernel.contracts/0.5.0"
	predecessorManifest    = "CONTRACTS/tekroo.kernel.contracts/0.4.0/manifest.json"
	predecessorManifestSHA = "5ff83483ce43ace2e06f2cc2f57dd552342553fc2389f3cc775b761c0c6d6d7c"
	reportPath             = "OUTPUT/phase-3/step-10-release-contract-gate.json"
	focusPath              = "OUTPUT/phase-3/step-10-release-contract-tests.jsonl"
	fullPath               = "OUTPUT/phase-3/step-10-full-regression-tests.jsonl"
	structurePath          = "OUTPUT/phase-3/step-10-contract-structure.json"
	referencePath          = "OUTPUT/phase-3/step-10-contract-reference.json"
)

type report struct {
	SchemaVersion      string      `json:"schemaVersion"`
	ReportType         string      `json:"reportType"`
	Status             string      `json:"status"`
	AcceptedBaseCommit string      `json:"acceptedBaseCommit"`
	AcceptedBaseTree   string      `json:"acceptedBaseTree"`
	ContractIdentity   string      `json:"contractIdentity"`
	ManifestSHA256     string      `json:"manifestSha256"`
	SourceTreeSHA256   string      `json:"sourceTreeSha256"`
	Environment        environment `json:"environment"`
	Executed           executed    `json:"executed"`
	Assertions         []assertion `json:"assertions"`
	ExcludedProfiles   []excluded  `json:"excludedProfiles"`
	Artifacts          []artifact  `json:"artifacts"`
	DigestMethod       string      `json:"digestMethod"`
	ReportSHA256       string      `json:"reportSha256"`
}

type environment struct {
	GoVersion string `json:"goVersion"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	Locale    string `json:"locale"`
	TimeZone  string `json:"timeZone"`
}

type executed struct {
	ContractStructureChecks int `json:"contractStructureChecks"`
	ContractFixtureCases    int `json:"contractFixtureCases"`
	FocusedTestCases        int `json:"focusedTestCases"`
	FocusedTestFailures     int `json:"focusedTestFailures"`
	RegressionTestCases     int `json:"regressionTestCases"`
	RegressionTestFailures  int `json:"regressionTestFailures"`
	VetFailures             int `json:"vetFailures"`
}

type assertion struct {
	ID            string   `json:"id"`
	Status        string   `json:"status"`
	Tests         []string `json:"tests"`
	EvidenceFloor string   `json:"evidenceFloor"`
}

type excluded struct {
	Profile string `json:"profile"`
	Reason  string `json:"reason"`
}

type artifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type summary struct {
	tests    int
	failures int
}

func main() {
	if len(os.Args) == 3 && os.Args[1] == "-verify" {
		if err := verify(os.Args[2]); err != nil {
			fatal(err)
		}
		return
	}
	if len(os.Args) != 1 {
		fatal(errors.New("usage: release-contract-report | -verify path"))
	}
	if err := run(); err != nil {
		fatal(err)
	}
}

func run() error {
	if err := verifyBase(); err != nil {
		return err
	}
	predecessorDigest, err := digestFile(predecessorManifest)
	if err != nil || predecessorDigest != predecessorManifestSHA {
		return errors.New("released predecessor manifest digest mismatch")
	}
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		return err
	}
	if _, err := command("node", filepath.Join(contractRoot, "runner", "validate-package.mjs"), structurePath); err != nil {
		return err
	}
	if _, err := command("node", filepath.Join(contractRoot, "runner", "reference-runner.mjs"), referencePath); err != nil {
		return err
	}
	structureChecks, err := reportCount(structurePath, "checks")
	if err != nil {
		return err
	}
	fixtureCases, err := reportCount(referencePath, "cases")
	if err != nil {
		return err
	}
	focused, focusedErr := command("go", "test", "-race", "-count=1", "-json", "./kernel", "./contract", "./internal/conformance", "./application")
	if err := os.WriteFile(focusPath, focused, 0o644); err != nil {
		return err
	}
	focusedSummary, err := summarize(focused)
	if err != nil || focusedErr != nil {
		return firstError(err, focusedErr)
	}
	regression, regressionErr := command("go", "test", "-race", "-count=1", "-json", "./...")
	if err := os.WriteFile(fullPath, regression, 0o644); err != nil {
		return err
	}
	regressionSummary, err := summarize(regression)
	if err != nil || regressionErr != nil {
		return firstError(err, regressionErr)
	}
	if _, err := command("go", "vet", "./..."); err != nil {
		return err
	}
	manifest, err := digestFile(filepath.Join(contractRoot, "manifest.json"))
	if err != nil {
		return err
	}
	source, err := digestTree(".")
	if err != nil {
		return err
	}
	paths := []string{
		structurePath,
		referencePath,
		focusPath,
		fullPath,
		"OUTPUT/phase-3/step-9-acceptance.json",
		"OUTPUT/phase-3/step-9-release-receipt.json",
		"OUTPUT/phase-3/step-10-authorization.json",
		"OUTPUT/phase-3/step-10-contract-revision-authorization.json",
		"OUTPUT/phase-3/step-10-release-contract-sufficiency.json",
		"OUTPUT/phase-3/step-10-release-contract-encoding-gaps.md",
		"CONTRACTS/tekroo.kernel.contracts/0.5.0/compatibility/from-0.4.0.json",
		"docs/architecture/012-deterministic-release-contract-revision.md",
	}
	artifacts := make([]artifact, 0, len(paths))
	for _, path := range paths {
		digest, err := digestFile(path)
		if err != nil {
			return err
		}
		artifacts = append(artifacts, artifact{Path: path, SHA256: digest})
	}
	value := report{
		SchemaVersion: "1.0.0", ReportType: "PHASE_3_RELEASE_CONTRACT_REVISION", Status: "PASS",
		AcceptedBaseCommit: baseCommit, AcceptedBaseTree: baseTree,
		ContractIdentity: "tekroo.kernel.contracts/0.5.0", ManifestSHA256: manifest, SourceTreeSHA256: source,
		Environment: environment{runtime.Version(), runtime.GOOS, runtime.GOARCH, "C", "UTC"},
		Executed:    executed{structureChecks, fixtureCases, focusedSummary.tests, focusedSummary.failures, regressionSummary.tests, regressionSummary.failures, 0},
		Assertions: []assertion{
			{"RELEASE-CONTRACT-001-DEDICATED-TYPES", "PASS", []string{"TestFrozenContractCorpus/CAT-036-RELEASE-APPROVAL-VALID", "TestFrozenContractCorpus/CAT-030-RELEASE-CREATE-VALID", "TestFrozenContractCorpus/RELEASE-EXACT-AUTHOR-APPROVAL", "TestFrozenContractCorpus/RELEASE-AUTHOR-APPROVAL-MISMATCH", "TestFrozenContractCorpus/CAT-035-RELEASE-FINALIZE-VALID"}, "Author approval is an explicit event bound exactly by release creation; release planning is a dedicated aggregate with six command/event pairs; evidence IDs do not substitute for either record."},
			{"RELEASE-CONTRACT-002-FROZEN-QUALIFICATION", "PASS", []string{"TestFrozenContractCorpus/RELEASE-EXACT-QUALIFICATION", "TestFrozenContractCorpus/RELEASE-CHANGED-PLAN", "TestFrozenContractCorpus/RELEASE-CHANGED-BASE", "TestFrozenContractCorpus/RELEASE-CHANGED-HEAD"}, "Qualification is accepted only for the exact persisted plan digest, base, ordered heads, and qualified tree."},
			{"RELEASE-CONTRACT-003-ORDERED-EXECUTION", "PASS", []string{"TestFrozenContractCorpus/RELEASE-EXECUTE-BEFORE-QUALIFICATION", "TestFrozenContractCorpus/RELEASE-FIRST-ORDERED-REQUEST", "TestFrozenContractCorpus/RELEASE-WRONG-ORDER", "TestFrozenContractCorpus/RELEASE-CONCURRENT-WORKER", "TestFrozenContractCorpus/RELEASE-IDEMPOTENCY-DUPLICATE-AND-CONFLICT", "TestFrozenContractCorpus/RELEASE-BOUNDED-RETRY", "TestFrozenContractCorpus/RELEASE-RETRY-BUDGET-EXHAUSTED"}, "Execution requires qualification, the exact next merge, one active attempt, stable idempotency, and an exact finite retry round."},
			{"RELEASE-CONTRACT-004-UNCERTAINTY", "PASS", []string{"TestFrozenContractCorpus/RELEASE-UNKNOWN-RESULT", "TestFrozenContractCorpus/RELEASE-CLEAN-MERGE", "TestFrozenContractCorpus/RELEASE-ALREADY-MERGED", "TestFrozenContractCorpus/RELEASE-GENUINE-FAILURE-RETRYABLE", "TestFrozenContractCorpus/RELEASE-GENUINE-FAILURE-EXHAUSTED", "TestFrozenContractCorpus/RELEASE-NO-RETRY-WHILE-UNKNOWN", "TestFrozenContractCorpus/RELEASE-EXACT-RECONCILIATION", "TestFrozenContractCorpus/RELEASE-PROVIDER-UNAVAILABLE", "TestFrozenContractCorpus/RELEASE-WRONG-SUPERSESSION"}, "Every result remains explicit; UNKNOWN blocks retry and advances only through exact provider reconciliation, while genuine failure consumes the finite retry budget."},
			{"RELEASE-CONTRACT-005-VERIFIED-ACCEPTANCE", "PASS", []string{"TestFrozenContractCorpus/RELEASE-PARTIAL-FINALIZE", "TestFrozenContractCorpus/RELEASE-TREE-MISMATCH", "TestFrozenContractCorpus/RELEASE-VERIFIED-FINALIZE", "TestFrozenContractCorpus/RELEASE-NO-CODE-FINALIZE"}, "Code release finalization requires all ordered merges and exact provider/qualified tree equality; non-code work is explicit."},
			{"RELEASE-CONTRACT-006-COMPATIBILITY", "PASS", []string{"TestFrozenContractCorpus", "TestCatalogueRejectsManifestIdentityMismatch", "TestCompletionCoordinatorStoryDependenciesAndReplayAreDeterministic"}, "Released 0.4.0 remains byte-identical; unchanged semantics migrate identically while story acceptance requires explicit 1.4.0 release context."},
			{"RELEASE-CONTRACT-007-BOUNDARY", "PASS", []string{"TestAllFrozenCommandsReachTheirDeclaredEventThroughEvaluator", "TestAcceptanceFailsClosedUntilReleaseImplementationAndOtherTerminalPathsRemainExplicit"}, "The pure transition model is qualified, while release-dependent authoritative commands fail closed with RELEASE_NOT_IMPLEMENTED."},
		},
		ExcludedProfiles: []excluded{
			{"release-coordination", "Durable release projection and application coordination are not implemented."},
			{"git-provider-execution", "No Git or provider mutation, authentication, retry, or reconciliation I/O is exercised."},
			{"mongo-integration", "Release-plan persistence and projection are not implemented or exercised."},
			{"synthesized-merge", "No candidate merge tree is synthesized by this contract-only gate."},
			{"deployment-migration-performance", "Production deployment, migration execution, load, latency, and throughput are not exercised."},
		},
		Artifacts:    artifacts,
		DigestMethod: "SHA-256 of compact JSON with reportSha256 set to the empty string",
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(canonical)
	value.ReportSHA256 = hex.EncodeToString(digest[:])
	pretty, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(reportPath, append(pretty, '\n'), 0o644)
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
	if value.Status != "PASS" || reported != hex.EncodeToString(digest[:]) {
		return errors.New("report status or self-digest mismatch")
	}
	if err := verifyBase(); err != nil {
		return err
	}
	predecessorDigest, err := digestFile(predecessorManifest)
	if err != nil || predecessorDigest != predecessorManifestSHA {
		return errors.New("released predecessor manifest digest mismatch")
	}
	manifest, err := digestFile(filepath.Join(contractRoot, "manifest.json"))
	if err != nil || manifest != value.ManifestSHA256 {
		return errors.New("contract manifest digest mismatch")
	}
	source, err := digestTree(".")
	if err != nil || source != value.SourceTreeSHA256 {
		return errors.New("source tree digest mismatch")
	}
	for _, item := range value.Artifacts {
		digest, err := digestFile(item.Path)
		if err != nil || digest != item.SHA256 {
			return fmt.Errorf("artifact digest mismatch: %s", item.Path)
		}
	}
	return nil
}

func verifyBase() error {
	output, err := command("git", "rev-parse", baseCommit+"^{tree}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(output)) != baseTree {
		return errors.New("accepted base tree mismatch")
	}
	return nil
}

func reportCount(path, field string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return 0, err
	}
	var status string
	if err := json.Unmarshal(object["status"], &status); err != nil || status != "PASS" {
		return 0, errors.New("contract runner did not pass")
	}
	var items []any
	if err := json.Unmarshal(object[field], &items); err != nil {
		return 0, err
	}
	return len(items), nil
}

func summarize(data []byte) (summary, error) {
	var result summary
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		var event struct {
			Action string `json:"Action"`
			Test   string `json:"Test"`
		}
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		if event.Action == "pass" && event.Test != "" {
			result.tests++
		}
		if event.Action == "fail" && event.Test != "" {
			result.failures++
		}
	}
	return result, scanner.Err()
}

func digestFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func digestTree(root string) (string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == ".idea" || entry.Name() == "build" || entry.Name() == "OUTPUT" {
				return filepath.SkipDir
			}
			return nil
		}
		paths = append(paths, filepath.ToSlash(path))
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

func command(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, output)
	}
	return output, nil
}

func firstError(first, second error) error {
	if first != nil {
		return first
	}
	return second
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
