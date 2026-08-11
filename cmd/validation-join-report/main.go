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
	baseCommit = "0f4f046567ce3ac297d907ccc0bc2dd1aa99d0b2"
	baseTree   = "21300a855e97d6c15df594051bcb9edf07229b0d"
	reportPath = "OUTPUT/phase-3/step-6-validation-join-gate.json"
	focusPath  = "OUTPUT/phase-3/step-6-validation-join-tests.jsonl"
	fullPath   = "OUTPUT/phase-3/step-6-full-regression-tests.jsonl"
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

type summary struct {
	tests    int
	failures int
	packages int
}

func main() {
	if len(os.Args) == 3 && os.Args[1] == "-verify" {
		if err := verify(os.Args[2]); err != nil {
			fatal(err)
		}
		return
	}
	if len(os.Args) != 1 {
		fatal(errors.New("usage: validation-join-report | -verify path"))
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
	focused, focusedErr := command("go", "test", "-race", "-count=1", "-json", "./kernel", "./application", "./adapters/protocol")
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
	paths := []string{focusPath, fullPath, "OUTPUT/phase-3/step-5-acceptance.json", "OUTPUT/phase-3/step-5-release-receipt.json", "OUTPUT/phase-3/step-6-authorization.json", "docs/architecture/007-deterministic-validation-join.md"}
	artifacts := make([]artifact, 0, len(paths))
	for _, path := range paths {
		digest, err := digestFile(path)
		if err != nil {
			return err
		}
		artifacts = append(artifacts, artifact{path, digest})
	}
	value := report{
		SchemaVersion: "1.0.0", ReportType: "PHASE_3_DETERMINISTIC_VALIDATION_JOIN", Status: "PASS",
		AcceptedBaseCommit: baseCommit, AcceptedBaseTree: baseTree, ContractIdentity: "tekroo.kernel.contracts/0.2.0", ManifestSHA256: manifest, SourceTreeSHA256: source,
		Environment: environment{runtime.Version(), runtime.GOOS, runtime.GOARCH, "C", "UTC"}, Executed: executed{fsum.tests, fsum.failures, fsum.packages, rsum.tests, rsum.failures, rsum.packages, 0},
		Assertions: []assertion{
			{"VALIDATE-001-ORDER-INDEPENDENT", "PASS", []string{"TestValidationReviewPlanIsInvariantToBranchAndParentOrder", "TestValidationJoinProjectionIsCanonicalAndOrderIndependent", "TestStorePersistsOrderIndependentCompletionReviewJoin"}, "Branch, parent, result-arrival, and map iteration order do not alter the canonical review plan or join."},
			{"VALIDATE-002-PARTIAL-POLICY", "PASS", []string{"TestValidationJoinPartialPoliciesHaveDeterministicFailurePrecedence"}, "WAIT_ALL remains pending until all branches report; FAIL_FAST terminates on a non-pass result, with FAIL deterministically preceding INCONCLUSIVE."},
			{"VALIDATE-003-REPLAY-CONFLICT", "PASS", []string{"TestValidationBranchReplayIsIdempotentAndContradictionConflicts", "TestValidationCoordinatorReplayEmitsIdenticalCommand"}, "Identical branch results and open commands replay idempotently; contradictory branch results conflict instead of overwriting accepted state."},
			{"VALIDATE-004-AUTHORITY-CONTRACT", "PASS", []string{"TestValidationCoordinatorOpensCanonicalPolicyOwnedReview", "TestGatewayCarriesExactAuthenticatedContextToTypedService", "TestGatewayRejectsIdentityFenceAndIdempotencyMismatchBeforeService"}, "The coordinator emits a frozen-schema policy-owned open command without validator identity; validator submissions remain behind exact authenticated gateway context."},
			{"VALIDATE-005-BOUNDED-NO-EFFECT", "PASS", []string{"TestValidationJoinRejectsInvalidAndUnboundedInputs", "TestValidationCoordinatorReturnsInvalidPlanWithoutCommand", "TestValidationCoordinatorPreservesRejectedReceipt"}, "Malformed, duplicate, unknown, or over-bound inputs produce stable conflict/invalid outcomes without hidden command or retry loops."},
			{"VALIDATE-006-BOUNDARY", "PASS", []string{"TestApplicationProductionImportsRemainKernelOnly"}, "Pure join code imports no adapter; application coordination imports no persistence, provider, OpenHands, SMA, or Git implementation."},
		},
		ExcludedProfiles: []excluded{
			{"bounded-review-contract-extension", "Resolution owner, deadline, adjudicator, per-branch round budget, and finding supersession are not encoded by contract 0.2.0."},
			{"completion-reopening-escalation-release", "Completion, reopening, escalation execution, acceptance, and release coordination remain later slices."},
			{"live-validators-providers-sma", "OpenHands, SMA, and live validators/providers remain separately authorized."},
			{"deployment-migration-performance", "Production deployment, migration, load, latency, and throughput are not exercised."},
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
	scanner := bufio.NewScanner(bytes.NewReader(data))
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
			result.tests++
		}
		if event.Action == "fail" && event.Test != "" {
			result.failures++
		}
		if event.Action == "pass" && event.Test == "" && event.Package != "" {
			result.packages++
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

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
