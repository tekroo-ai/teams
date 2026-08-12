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
	baseCommit    = "876787ccd12ba5d02437ab8f3097058696491561"
	baseTree      = "77d09a6903b80022ab3c15c91d00f5210de01f84"
	contractRoot  = "CONTRACTS/tekroo.kernel.contracts/0.5.0"
	reportPath    = "OUTPUT/phase-3/step-13-capstone-gate.json"
	focusPath     = "OUTPUT/phase-3/step-13-capstone-tests.jsonl"
	mongoPath     = "OUTPUT/phase-3/step-13-mongo-integration-tests.jsonl"
	fullPath      = "OUTPUT/phase-3/step-13-full-regression-tests.jsonl"
	structurePath = "OUTPUT/phase-3/step-13-contract-structure.json"
	referencePath = "OUTPUT/phase-3/step-13-contract-reference.json"
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
	GoVersion      string `json:"goVersion"`
	GOOS           string `json:"goos"`
	GOARCH         string `json:"goarch"`
	GitVersion     string `json:"gitVersion"`
	MongoDBProfile string `json:"mongoDbProfile"`
	Locale         string `json:"locale"`
	TimeZone       string `json:"timeZone"`
}

type executed struct {
	ContractStructureChecks      int `json:"contractStructureChecks"`
	ContractFixtureCases         int `json:"contractFixtureCases"`
	CapstoneRaceCases            int `json:"capstoneRaceCases"`
	CapstoneRaceFailures         int `json:"capstoneRaceFailures"`
	MongoIntegrationRaceCases    int `json:"mongoIntegrationRaceCases"`
	MongoIntegrationRaceFailures int `json:"mongoIntegrationRaceFailures"`
	FullRegressionRaceCases      int `json:"fullRegressionRaceCases"`
	FullRegressionRaceFailures   int `json:"fullRegressionRaceFailures"`
	VetFailures                  int `json:"vetFailures"`
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

type summary struct{ tests, failures int }

func main() {
	if len(os.Args) == 3 && os.Args[1] == "-verify" {
		fatal(verify(os.Args[2]))
		return
	}
	if len(os.Args) != 1 {
		fatal(errors.New("usage: capstone-report | -verify path"))
	}
	fatal(run())
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
	structure, err := reportCount(structurePath, "checks")
	if err != nil {
		return err
	}
	fixtures, err := reportCount(referencePath, "cases")
	if err != nil {
		return err
	}
	focused, focusedErr := command("go", "test", "-race", "-count=1", "-tags", "mongo_integration", "-run", "Test(IsolatedCapstone|CapstoneFixture)", "-json", "./adapters/mongo")
	if err := os.WriteFile(focusPath, focused, 0o644); err != nil {
		return err
	}
	fsum, err := summarize(focused)
	if err != nil || focusedErr != nil {
		return firstError(err, focusedErr)
	}
	mongo, mongoErr := command("go", "test", "-race", "-count=1", "-tags", "mongo_integration", "-json", "./adapters/mongo")
	if err := os.WriteFile(mongoPath, mongo, 0o644); err != nil {
		return err
	}
	msum, err := summarize(mongo)
	if err != nil || mongoErr != nil {
		return firstError(err, mongoErr)
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
	gitVersion, err := command("git", "--version")
	if err != nil {
		return err
	}
	paths := []string{
		structurePath, referencePath, focusPath, mongoPath, fullPath,
		"OUTPUT/phase-3/step-12-acceptance.json",
		"OUTPUT/phase-3/step-12-release-receipt.json",
		"OUTPUT/phase-3/step-13-authorization.json",
		"docs/architecture/016-isolated-deterministic-capstone.md",
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
		SchemaVersion: "1.0.0", ReportType: "PHASE_3_ISOLATED_DETERMINISTIC_CAPSTONE", Status: "PASS",
		AcceptedBaseCommit: baseCommit, AcceptedBaseTree: baseTree, ContractIdentity: "tekroo.kernel.contracts/0.5.0", ManifestSHA256: manifest, SourceTreeSHA256: source,
		Environment: environment{runtime.Version(), runtime.GOOS, runtime.GOARCH, strings.TrimSpace(string(gitVersion)), "fresh local single-node replica set", "C", "UTC"},
		Executed:    executed{structure, fixtures, fsum.tests, fsum.failures, msum.tests, msum.failures, rsum.tests, rsum.failures, 0},
		Assertions: []assertion{
			{"CAPSTONE-001-COMPOSED-PATH", "PASS", []string{"TestIsolatedCapstonePersistsExactReleaseBeforeStoryAcceptance"}, "The application handler, evaluator, Mongo store, release coordinator, local Git provider, protocol gateway, and HTTP adapter complete one deterministic release-to-acceptance path."},
			{"CAPSTONE-002-INTENT-BEFORE-EFFECT", "PASS", []string{"TestIsolatedCapstonePersistsExactReleaseBeforeStoryAcceptance"}, "The provider observes the matching Mongo projection in EXECUTING state with the exact active attempt before it mutates the isolated Git ref."},
			{"CAPSTONE-003-EXACT-GIT-IDENTITY", "PASS", []string{"TestIsolatedCapstonePersistsExactReleaseBeforeStoryAcceptance", "TestCapstoneFixtureUsesNoNetworkRepository"}, "The test-owned local bare repository advances to the exact planned head and qualified tree without a network repository."},
			{"CAPSTONE-004-ACCEPTANCE-FENCE", "PASS", []string{"TestIsolatedCapstonePersistsExactReleaseBeforeStoryAcceptance"}, "HTTP story acceptance is rejected before finalization and applied only after the exact finalized release projection is durable."},
			{"CAPSTONE-005-REPLAY-AND-RESTART", "PASS", []string{"TestIsolatedCapstonePersistsExactReleaseBeforeStoryAcceptance"}, "Acceptance replay returns the original event; after restart the accepted story and terminal release projection reload with exact event counts."},
			{"CAPSTONE-006-LAYERED-REGRESSION", "PASS", []string{"contract structure runner", "contract reference runner", "Mongo integration race suite", "full Go race suite", "go vet"}, "The released 0.5.0 contract and all prior repository profiles remain qualified."},
		},
		ExcludedProfiles: []excluded{
			{"network-forge-and-existing-repositories", "No forge API, credential, network repository, or pre-existing repository is used or mutated."},
			{"historical-migration", "No Tekroo v3 source or historical database is migrated or mutated."},
			{"production-deployment-performance", "Production topology, deployment, rollback, load, latency, throughput, and resource budgets are not exercised."},
			{"openhands-and-sma-investigations", "The separately gated OPENHANDS-Q1, SMA-Q1, and SMA-Q2 investigations are not executed."},
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

func command(name string, arguments ...string) ([]byte, error) {
	cmd := exec.Command(name, arguments...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC", "GIT_TERMINAL_PROMPT=0", "GIT_NO_LAZY_FETCH=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s %s: %w\n%s", name, strings.Join(arguments, " "), err, output)
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
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
