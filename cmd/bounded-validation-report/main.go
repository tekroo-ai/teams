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
	baseCommit    = "b5138057f81414b9a61c9b752f0268e0c2d903f1"
	baseTree      = "6d99ef4e5972d3e503822a061f8c2049a845fb5d"
	contractRoot  = "CONTRACTS/tekroo.kernel.contracts/0.3.0"
	reportPath    = "OUTPUT/phase-3/step-7-bounded-validation-gate.json"
	focusPath     = "OUTPUT/phase-3/step-7-bounded-validation-tests.jsonl"
	fullPath      = "OUTPUT/phase-3/step-7-full-regression-tests.jsonl"
	structurePath = "OUTPUT/phase-3/step-7-contract-structure.json"
	referencePath = "OUTPUT/phase-3/step-7-contract-reference.json"
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
	StepAssessment     []step      `json:"stepAssessment"`
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

type step struct {
	Steps      string `json:"steps"`
	Conclusion string `json:"conclusion"`
	Basis      string `json:"basis"`
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
		fatal(errors.New("usage: bounded-validation-report | -verify path"))
	}
	if err := run(); err != nil {
		fatal(err)
	}
}

func run() error {
	if err := verifyBase(); err != nil {
		return err
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
	focused, focusedErr := command("go", "test", "-race", "-count=1", "-json", "./kernel", "./application", "./adapters/memory", "./adapters/mongo", "./internal/conformance")
	if err := os.WriteFile(focusPath, focused, 0o644); err != nil {
		return err
	}
	fsum, err := summarize(focused)
	if err != nil || focusedErr != nil {
		return firstError(err, focusedErr)
	}
	full, fullErr := command("go", "test", "-race", "-count=1", "-json", "./...")
	if err := os.WriteFile(fullPath, full, 0o644); err != nil {
		return err
	}
	rsum, err := summarize(full)
	if err != nil || fullErr != nil {
		return firstError(err, fullErr)
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
		structurePath, referencePath, focusPath, fullPath,
		"OUTPUT/phase-3/step-6-acceptance.json",
		"OUTPUT/phase-3/step-6-release-receipt.json",
		"OUTPUT/phase-3/step-7-contract-revision-authorization.json",
		"OUTPUT/phase-3/step-7-contract-encoding-gaps.md",
		filepath.Join(contractRoot, "compatibility", "from-0.2.0.json"),
		"docs/architecture/008-bounded-validation-contract-revision.md",
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
		SchemaVersion: "1.0.0", ReportType: "PHASE_3_BOUNDED_VALIDATION_CONTRACT_REVISION", Status: "PASS",
		AcceptedBaseCommit: baseCommit, AcceptedBaseTree: baseTree, ContractIdentity: "tekroo.kernel.contracts/0.3.0", ManifestSHA256: manifest, SourceTreeSHA256: source,
		Environment: environment{runtime.Version(), runtime.GOOS, runtime.GOARCH, "C", "UTC"},
		Executed:    executed{structureChecks, fixtureCases, fsum.tests, fsum.failures, rsum.tests, rsum.failures, 0},
		Assertions: []assertion{
			{"BOUND-001-EXACT-BRANCH-AUTHORITY", "PASS", []string{"TestBoundedValidationEnforcesExactAuthorityDeadlineRoundAndSupersession", "REVIEW-BOUNDED-VALIDATOR-PASS", "REVIEW-BOUNDED-WRONG-VALIDATOR"}, "Every branch binds an exact validator and resolution owner; the authenticated principal must match."},
			{"BOUND-002-FINITE-TIME-AND-ROUNDS", "PASS", []string{"TestBoundedValidationEnforcesExactAuthorityDeadlineRoundAndSupersession", "REVIEW-BOUNDED-DEADLINE", "REVIEW-BOUNDED-ROUND-LIMIT", "REVIEW-BOUNDED-POLICY-TIMEOUT"}, "Trusted decision time and finite branch/adjudication rounds bound validation and timeout outcomes."},
			{"BOUND-003-SUPERSESSION-ADJUDICATION", "PASS", []string{"TestBoundedValidationEnforcesExactAuthorityDeadlineRoundAndSupersession", "TestBoundedValidationDeduplicatesFindingKeysAndRejectsConflicts", "REVIEW-BOUNDED-CHANGED-CONDITION", "REVIEW-BOUNDED-ADJUDICATOR"}, "Finding keys deduplicate exact findings; conflicts reject. Replacing a branch result names the current result event and changed-condition evidence; adjudication uses its exact principal and budget."},
			{"BOUND-004-POLICY-FINALIZED-COMPLETION", "PASS", []string{"TestCompletionReviewFinalizationBindsCurrentResultSet", "TestCompletionRequiresExactEvidenceCriteriaValidationAndDependencies", "CAT-027-COMPLETION-REVIEW-FINALIZE-VALID", "REVIEW-BOUNDED-POST-FINALIZATION"}, "Completion consumes one current policy-finalized PASS review, never an isolated validator PASS; post-finalization results are rejected."},
			{"BOUND-005-DETERMINISTIC-JOIN-PRESERVED", "PASS", []string{"TestValidationJoinProjectionIsCanonicalAndOrderIndependent", "TestValidationJoinPartialPoliciesHaveDeterministicFailurePrecedence", "TestStorePersistsOrderIndependentCompletionReviewJoin"}, "The Step 6 canonical join and deterministic precedence remain passing after augmentation."},
			{"BOUND-006-IMMUTABLE-COMPATIBILITY", "PASS", []string{"TestFrozenContractCorpus", "TestCatalogueRejectsManifestIdentityMismatch"}, "0.3.0 is separately sealed; changed 1.1.0 review/completion records require exact-context migration and are not inferred."},
		},
		StepAssessment: []step{
			{"1-5", "UNCHANGED", "Their accepted adapter, channel, execution, and assignment boundaries do not depend on the added NEG-002 fields."},
			{"6", "FORWARD_AUGMENTED_AND_REQUALIFIED", "The released 0.2.0 join claim remains valid; bounded identity, time, round, supersession, adjudication, and finalization are added under 0.3.0."},
		},
		ExcludedProfiles: []excluded{
			{"live-validation-execution", "The kernel validates commands but does not start validators or choose organizational deadlines."},
			{"automatic-reopening-escalation-release", "Application policies that react to terminal review outcomes and provider release execution remain later slices."},
			{"production-migration-and-performance", "Historical replay adapters, production deployment, load, latency, and throughput are not exercised here."},
		},
		Artifacts: artifacts, DigestMethod: "SHA-256 of compact JSON with reportSha256 set to the empty string",
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
	var value struct {
		Status string           `json:"status"`
		Lists  map[string][]any `json:"-"`
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return 0, err
	}
	if err := json.Unmarshal(object["status"], &value.Status); err != nil || value.Status != "PASS" {
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
