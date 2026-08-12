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
	baseCommit    = "d994fa9cee8edae25fb561741c4decc3c9c459e6"
	baseTree      = "735d8d94166588589c4adae14934966432012d8f"
	contractRoot  = "CONTRACTS/tekroo.kernel.contracts/0.5.0"
	reportPath    = "OUTPUT/phase-3/step-10-release-coordination-gate.json"
	focusPath     = "OUTPUT/phase-3/step-10-release-coordination-tests.jsonl"
	fullPath      = "OUTPUT/phase-3/step-10-implementation-full-regression-tests.jsonl"
	structurePath = "OUTPUT/phase-3/step-10-implementation-contract-structure.json"
	referencePath = "OUTPUT/phase-3/step-10-implementation-contract-reference.json"
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
		fatal(errors.New("usage: release-coordination-report | -verify path"))
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
	focused, focusedErr := command("go", "test", "-race", "-count=1", "-json", "./kernel", "./application", "./adapters/memory", "./adapters/fake", "./adapters/mongo")
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
		"OUTPUT/phase-3/step-10-contract-acceptance.json",
		"OUTPUT/phase-3/step-10-contract-release-receipt.json",
		"OUTPUT/phase-3/step-10-implementation-authorization.json",
		"OUTPUT/phase-3/step-10-release-contract-gate.json",
		"docs/architecture/013-deterministic-release-coordination.md",
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
		SchemaVersion: "1.0.0", ReportType: "PHASE_3_DETERMINISTIC_RELEASE_COORDINATION", Status: "PASS",
		AcceptedBaseCommit: baseCommit, AcceptedBaseTree: baseTree, ContractIdentity: "tekroo.kernel.contracts/0.5.0", ManifestSHA256: manifest, SourceTreeSHA256: source,
		Environment: environment{runtime.Version(), runtime.GOOS, runtime.GOARCH, "C", "UTC"},
		Executed:    executed{structureChecks, fixtureCases, fsum.tests, fsum.failures, rsum.tests, rsum.failures, 0},
		Assertions: []assertion{
			{"RELEASE-001-EXACT-PLAN", "PASS", []string{"TestAllFrozenCommandsReachTheirDeclaredEventThroughEvaluator", "TestStoreProjectsCompleteReleaseSequenceAndFencesDuplicateStoryPlan"}, "Approval and planning bind the exact story lifecycle, immutable ordered merge inputs, reproducibility identities, policy revisions, evidence, and one semantic plan key."},
			{"RELEASE-002-INTENT-BEFORE-EFFECT", "PASS", []string{"TestReleaseCoordinatorPersistsIntentBeforeDeterministicProviderOutcomes"}, "The provider is invoked only after an APPLIED execution-requested receipt for the exact next merge, attempt, round, and provider idempotency key."},
			{"RELEASE-003-AMBIGUITY-RECONCILIATION", "PASS", []string{"TestReleaseCoordinatorRecordsUnknownAfterTimeoutAndReplayIsIdentical", "TestReleaseCoordinatorReconcilesUnknownWithoutAnotherMerge"}, "Timeout and cancellation persist UNKNOWN; authoritative reconciliation supersedes that exact result and never initiates another merge."},
			{"RELEASE-004-PROVIDER-IDENTITY", "PASS", []string{"TestReleaseCoordinatorConvertsChangedProviderIdentityToUnknown", "TestStoreProjectsCompleteReleaseSequenceAndFencesDuplicateStoryPlan", "TestReleaseProjectionRetainsPartialOrderedProgressAndRejectsEarlyFinalization"}, "Successful observations must match the planned base and head; partial progress remains ordered; finalization requires the exact qualification, complete effective result vector, and qualified provider tree."},
			{"RELEASE-005-DUPLICATE-CONVERGENCE", "PASS", []string{"TestReleaseProviderConcurrentDuplicateUsesOneIdempotentEffect", "TestStoreProjectsCompleteReleaseSequenceAndFencesDuplicateStoryPlan"}, "Concurrent duplicate provider calls share one idempotent effect and equivalent story-lifecycle plans have one transactional winner."},
			{"RELEASE-006-CONTRACT-CONFORMANCE", "PASS", []string{"TestAllFrozenCommandsReachTheirDeclaredEventThroughEvaluator", "TestAcceptanceRequiresExactFinalizedReleaseAndOtherTerminalPathsRemainExplicit"}, "All 36 frozen valid commands, including the eight release-dependent commands, reach their one declared event; story acceptance requires the exact finalized release projection."},
			{"RELEASE-007-EXCLUDED-STORES-FAIL-CLOSED", "PASS", []string{"TestMongoFailsClosedForUnimplementedReleaseProjection"}, "The Mongo store explicitly rejects release-plan projection events; the candidate does not claim durable Mongo release coordination."},
			{"RELEASE-008-NON-CODE-TERMINAL", "PASS", []string{"TestNoReleaseRequiredFinalizesWithoutProviderIdentity"}, "A non-code release finalizes explicitly without a merge plan, qualification, provider attempt, result, or inferred provider identity."},
			{"RELEASE-009-DIRECTED-MERGE-DAG", "PASS", []string{"TestReleaseCoordinatorLinksNextOrderedMergeToPriorEffectiveResult"}, "Each later ordered merge responds to the prior merge's effective result; retries respond to the current merge's prior failed or reconciled result."},
		},
		ExcludedProfiles: []excluded{
			{"real-release-provider", "No GitHub, Git, forge, repository, branch, pull-request, or deployment mutation was performed; only the deterministic fake provider was exercised."},
			{"mongo-release-projection", "The Mongo decision store fails closed for release-plan events; implementing and validating its durable release projection requires a separately authorized slice."},
			{"production-rollout-and-migration", "Production deployment, migration execution, rollback, load, latency, and throughput are not exercised."},
			{"openhands-and-sma", "Release coordination does not alter OpenHands or SMA integration and performs no semantic-memory or model-call behavior."},
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
