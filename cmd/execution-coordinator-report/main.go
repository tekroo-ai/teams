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
	baseCommit = "476e6a4c9daf600acbaab7967eb5c0f001a40c26"
	baseTree   = "dd0b7391f1fb5e519d2ac3cca1a65e1ef490c895"
	reportPath = "OUTPUT/phase-3/step-4-execution-coordinator-gate.json"
	focusPath  = "OUTPUT/phase-3/step-4-execution-coordinator-tests.jsonl"
	fullPath   = "OUTPUT/phase-3/step-4-full-regression-tests.jsonl"
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
	FocusedTestCases         int `json:"focusedTestCases"`
	FocusedTestFailures      int `json:"focusedTestFailures"`
	FocusedPackagesPassed    int `json:"focusedPackagesPassed"`
	RegressionTestCases      int `json:"regressionTestCases"`
	RegressionTestFailures   int `json:"regressionTestFailures"`
	RegressionPackagesPassed int `json:"regressionPackagesPassed"`
	VetFailures              int `json:"vetFailures"`
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
type summary struct{ tests, failures, packages int }

func main() {
	if len(os.Args) == 3 && os.Args[1] == "-verify" {
		if err := verify(os.Args[2]); err != nil {
			fatal(err)
		}
		return
	}
	if len(os.Args) != 1 {
		fatal(errors.New("usage: execution-coordinator-report | -verify path"))
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
	focused, focusedErr := command("go", "test", "-race", "-count=1", "-json", "./kernel", "./application", "./adapters/memory", "./adapters/fake")
	if err := os.WriteFile(focusPath, focused, 0o644); err != nil {
		return err
	}
	fsum, err := summarize(focused)
	if err != nil {
		return err
	}
	if focusedErr != nil {
		return focusedErr
	}
	full, fullErr := command("go", "test", "-race", "-count=1", "-json", "./...")
	if err := os.WriteFile(fullPath, full, 0o644); err != nil {
		return err
	}
	rsum, err := summarize(full)
	if err != nil {
		return err
	}
	if fullErr != nil {
		return fullErr
	}
	if _, err := command("go", "vet", "./..."); err != nil {
		return err
	}
	manifest, err := digestFile("CONTRACTS/tekroo.kernel.contracts/0.2.0/manifest.json")
	if err != nil {
		return err
	}
	source, err := digestTree(".")
	if err != nil {
		return err
	}
	paths := []string{focusPath, fullPath, "OUTPUT/phase-3/step-3-acceptance.json", "OUTPUT/phase-3/step-3-release-receipt.json", "OUTPUT/phase-3/step-4-authorization.json", "docs/architecture/005-deterministic-execution-coordinator.md"}
	artifacts := make([]artifact, 0, len(paths))
	for _, path := range paths {
		digest, err := digestFile(path)
		if err != nil {
			return err
		}
		artifacts = append(artifacts, artifact{path, digest})
	}
	value := report{
		SchemaVersion: "1.0.0", ReportType: "PHASE_3_DETERMINISTIC_EXECUTION_COORDINATOR", Status: "PASS",
		AcceptedBaseCommit: baseCommit, AcceptedBaseTree: baseTree, ContractIdentity: "tekroo.kernel.contracts/0.2.0",
		ManifestSHA256: manifest, SourceTreeSHA256: source,
		Environment: environment{runtime.Version(), runtime.GOOS, runtime.GOARCH, "C", "UTC"},
		Executed:    executed{fsum.tests, fsum.failures, fsum.packages, rsum.tests, rsum.failures, rsum.packages, 0},
		Assertions: []assertion{
			{"EXEC-001-AUTH-BEFORE-START", "PASS", []string{"TestExecutionCoordinatorCommitsRegistryBeforeFakeProviderStart", "TestExecutionCoordinatorRejectsMisalignedOrDeniedStartBeforeProvider"}, "The exact execution registry transition is committed before provider Start; misalignment or rejection has no provider effect."},
			{"EXEC-002-IDEMPOTENT-CAS", "PASS", []string{"TestExecutionCoordinatorAuthorizesBeforeOneIdempotentStart", "TestExecutionCoordinatorConcurrentReplayHasOneProviderEffect"}, "Stable start identity and compare-and-set control transitions produce one provider effect across replay, restart, and concurrent invocation."},
			{"EXEC-003-BOUNDED-UNCERTAINTY", "PASS", []string{"TestExecutionCoordinatorTimeoutIsUncertainAndReconcilesWithinBudget", "TestExecutionCoordinatorRejectsWrongObservationAndBoundsReconciliation"}, "Post-dispatch timeout and identity mismatch remain UNCERTAIN; exact reconciliation is explicit and finite."},
			{"EXEC-004-LIMITS-ANTI-THRASH", "PASS", []string{"TestExecutionControlStoreEnforcesCapacityAndAntiThrash"}, "Atomic total/per-actor capacity and finite per-actor start-window limits fail closed, including uncertain capacity."},
			{"EXEC-005-PORT-CONFORMANCE", "PASS", []string{"TestExecutionCoordinatorStopAndInspectPreserveExactIdentity", "TestExecutionEngineReplaysStartAndStops"}, "Start, stop, inspect, and reconcile preserve exact provider-neutral identity and bounded completion."},
			{"EXEC-006-BOUNDARY", "PASS", []string{"TestApplicationProductionImportsRemainKernelOnly"}, "Application coordination imports only the kernel and no MongoDB, live provider, OpenHands, SMA, or Git implementation."},
		},
		ExcludedProfiles: []excluded{
			{"live-providers", "OpenHands and other live execution providers are not adopted or exercised."},
			{"production-control-persistence", "The in-memory operational control store is a deterministic reference, not production durability evidence."},
			{"work-dag-and-release", "Automated assignment, validation, completion/reopening, escalation, and Git/provider release execution remain later slices."},
			{"sma", "SMA integration and investigation remain separately authorized."},
			{"deployment-migration-security-performance", "Production deployment, migration, isolation, load, latency, and throughput are not exercised."},
		},
		Artifacts: artifacts, DigestMethod: "SHA-256 of compact JSON with reportSha256 set to the empty string",
	}
	if fsum.failures != 0 || rsum.failures != 0 {
		value.Status = "FAIL"
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
	if reported != hex.EncodeToString(digest[:]) || value.Status != "PASS" || value.AcceptedBaseCommit != baseCommit || value.AcceptedBaseTree != baseTree {
		return errors.New("report identity or digest mismatch")
	}
	if err := verifyBase(); err != nil {
		return err
	}
	manifest, err := digestFile("CONTRACTS/tekroo.kernel.contracts/0.2.0/manifest.json")
	if err != nil || manifest != value.ManifestSHA256 {
		return errors.New("manifest digest mismatch")
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
func summarize(data []byte) (summary, error) {
	var result summary
	scan := bufio.NewScanner(bytes.NewReader(data))
	for scan.Scan() {
		var event struct {
			Action  string `json:"Action"`
			Package string `json:"Package"`
			Test    string `json:"Test"`
		}
		if json.Unmarshal(scan.Bytes(), &event) != nil {
			continue
		}
		if event.Action == "pass" && event.Test != "" {
			result.tests++
		}
		if event.Action == "fail" && event.Test != "" {
			result.failures++
		}
		if event.Action == "pass" && event.Test == "" && event.Package != "" {
			result.packages++
		}
	}
	return result, scan.Err()
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
func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
