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
	baseCommit    = "43df1515dfdd77d2d671aab551c383cdf1e5981f"
	baseTree      = "a9166b0d0d87dc03d1c4a6fefb3b0c5b605659d4"
	contractRoot  = "CONTRACTS/tekroo.kernel.contracts/0.3.0"
	reportPath    = "OUTPUT/phase-3/step-8-completion-reopening-gate.json"
	focusPath     = "OUTPUT/phase-3/step-8-completion-reopening-tests.jsonl"
	fullPath      = "OUTPUT/phase-3/step-8-full-regression-tests.jsonl"
	structurePath = "OUTPUT/phase-3/step-8-contract-structure.json"
	referencePath = "OUTPUT/phase-3/step-8-contract-reference.json"
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
		fatal(errors.New("usage: completion-reopening-report | -verify path"))
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
	focused, focusedErr := command("go", "test", "-race", "-count=1", "-json", "./kernel", "./application", "./adapters/memory", "./adapters/protocol")
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
		"OUTPUT/phase-3/step-7-acceptance.json",
		"OUTPUT/phase-3/step-7-release-receipt.json",
		"OUTPUT/phase-3/step-8-authorization.json",
		"OUTPUT/phase-3/step-7-bounded-validation-gate.json",
		"docs/architecture/009-deterministic-completion-reopening.md",
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
		SchemaVersion: "1.0.0", ReportType: "PHASE_3_DETERMINISTIC_COMPLETION_REOPENING", Status: "PASS",
		AcceptedBaseCommit: baseCommit, AcceptedBaseTree: baseTree, ContractIdentity: "tekroo.kernel.contracts/0.3.0", ManifestSHA256: manifest, SourceTreeSHA256: source,
		Environment: environment{runtime.Version(), runtime.GOOS, runtime.GOARCH, "C", "UTC"},
		Executed:    executed{structureChecks, fixtureCases, fsum.tests, fsum.failures, rsum.tests, rsum.failures, 0},
		Assertions: []assertion{
			{"COMPLETE-001-FINALIZED-REVIEW", "PASS", []string{"TestCompletionPlanIsCanonicalAndBindsCurrentFinalizedReview", "TestCompletionRequiresExactEvidenceCriteriaValidationAndDependencies", "TestCompletionReviewFinalizationBindsCurrentResultSet"}, "Completion binds the exact current finalized PASS review, its revisions, finalization event, evidence, and lifecycle epoch."},
			{"COMPLETE-002-TASK-OWNER-FENCE", "PASS", []string{"TestCompletionCoordinatorEmitsExactFencedTaskCompletion", "TestEvaluatorRejectsStaleCataloguePolicyAndLifecycleContext", "TestExecutionFencingRequiresExactCurrentTuple"}, "Task completion is submitted only by the exact owner actor with the exact current execution fence."},
			{"COMPLETE-003-STORY-DEPENDENCIES", "PASS", []string{"TestCompletionCoordinatorStoryDependenciesAndReplayAreDeterministic", "TestCompletionRequiresExactEvidenceCriteriaValidationAndDependencies"}, "Story completion carries canonical exact-revision preconditions for every supplied completed task."},
			{"COMPLETE-004-NO-EFFECT", "PASS", []string{"TestCompletionPlanHasNoEffectForNonPassStaleOrClosedWork", "TestCompletionCoordinatorDoesNotCommandForFailedReviewOrWrongTaskAuthority"}, "Failed, stale, unresolved, already-complete, and unauthorized inputs emit no coordination command."},
			{"REOPEN-001-EXPLICIT-EPOCH", "PASS", []string{"TestReopeningPlanIsExplicitCanonicalAndTerminalOnly", "TestCompletionCoordinatorEmitsExplicitReopeningWithoutActorImpersonation", "TestAcceptanceReopenSuccessorAndCorrectionUseExplicitTerminalPaths"}, "Reopening is a separate human/policy command; the kernel increments the epoch and applies the explicit ownership carry-forward choice."},
			{"COORD-001-REPLAY-DETERMINISM", "PASS", []string{"TestCompletionCoordinatorStoryDependenciesAndReplayAreDeterministic", "TestHandlerCommitsOnceAndReturnsStoredReceiptOnReplay"}, "Identical planning inputs emit identical commands, and durable handler idempotency prevents duplicate authoritative transitions."},
			{"COORD-002-BOUNDARY", "PASS", []string{"TestApplicationProductionImportsRemainKernelOnly"}, "The pure planner imports no application or adapter; the coordinator imports only the kernel boundary and performs no provider, Git, OpenHands, or SMA effect."},
		},
		ExcludedProfiles: []excluded{
			{"automatic-failure-policy", "The slice does not decide whether a failed or inconclusive review should trigger reopening or escalation."},
			{"escalation-and-organizational-acceptance", "Escalation execution and organizational acceptance coordination remain later slices."},
			{"release-provider-execution", "Release plans, synthesized-merge execution, and Git/provider reconciliation remain later slices."},
			{"deployment-migration-performance", "Production deployment, migration execution, load, latency, and throughput are not exercised."},
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
