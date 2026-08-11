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
	acceptedBaseCommit = "7f01bb8d67a30f90c714d6cccc176c48b7a31a11"
	acceptedBaseTree   = "8e1bc0877afc3b448d106617c6e185ee597b885c"
	contractIdentity   = "tekroo.kernel.contracts/0.2.0"
	manifestPath       = "CONTRACTS/tekroo.kernel.contracts/0.2.0/manifest.json"
	defaultReportPath  = "OUTPUT/phase-3/step-3-channel-gate.json"
	focusedReceiptPath = "OUTPUT/phase-3/step-3-channel-tests.jsonl"
	regressionPath     = "OUTPUT/phase-3/step-3-full-regression-tests.jsonl"
)

type report struct {
	SchemaVersion      string            `json:"schemaVersion"`
	ReportType         string            `json:"reportType"`
	Status             string            `json:"status"`
	AcceptedBaseCommit string            `json:"acceptedBaseCommit"`
	AcceptedBaseTree   string            `json:"acceptedBaseTree"`
	ContractIdentity   string            `json:"contractIdentity"`
	ManifestSHA256     string            `json:"manifestSha256"`
	SourceTreeSHA256   string            `json:"sourceTreeSha256"`
	Environment        environment       `json:"environment"`
	Executed           executed          `json:"executed"`
	Assertions         []assertion       `json:"assertions"`
	ExcludedProfiles   []excludedProfile `json:"excludedProfiles"`
	Artifacts          []artifact        `json:"artifacts"`
	DigestMethod       string            `json:"digestMethod"`
	ReportSHA256       string            `json:"reportSha256"`
}

type environment struct {
	GoVersion string `json:"goVersion"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	Locale    string `json:"locale"`
	TimeZone  string `json:"timeZone"`
}

type executed struct {
	FocusedTestCases       int `json:"focusedTestCases"`
	FocusedTestFailures    int `json:"focusedTestFailures"`
	FocusedPackagesPassed  int `json:"focusedPackagesPassed"`
	RegressionTestCases    int `json:"regressionTestCases"`
	RegressionTestFailures int `json:"regressionTestFailures"`
	RegressionPackages     int `json:"regressionPackagesPassed"`
	VetFailures            int `json:"vetFailures"`
}

type assertion struct {
	ID            string   `json:"id"`
	Status        string   `json:"status"`
	Tests         []string `json:"tests"`
	EvidenceFloor string   `json:"evidenceFloor"`
}

type excludedProfile struct {
	Profile string `json:"profile"`
	Reason  string `json:"reason"`
}

type artifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type testSummary struct {
	Tests    int
	Failures int
	Packages int
}

func main() {
	output := defaultReportPath
	if len(os.Args) == 3 && os.Args[1] == "-verify" {
		if err := verify(os.Args[2]); err != nil {
			fatal(err)
		}
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "-output" {
		output = os.Args[2]
	} else if len(os.Args) != 1 {
		fatal(errors.New("usage: channel-report [-output path] | -verify path"))
	}
	if err := run(output); err != nil {
		fatal(err)
	}
}

func run(output string) error {
	if err := verifyAcceptedBase(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}
	focusedOutput, focusedErr := runCommand("go", "test", "-race", "-count=1", "-json", "./adapters/protocol", "./adapters/channel")
	if err := os.WriteFile(focusedReceiptPath, focusedOutput, 0o644); err != nil {
		return err
	}
	focusedTests, err := summarizeTests(focusedOutput)
	if err != nil {
		return err
	}
	if focusedErr != nil {
		return focusedErr
	}
	regressionOutput, regressionErr := runCommand("go", "test", "-race", "-count=1", "-json", "./...")
	if err := os.WriteFile(regressionPath, regressionOutput, 0o644); err != nil {
		return err
	}
	regressionTests, err := summarizeTests(regressionOutput)
	if err != nil {
		return err
	}
	if regressionErr != nil {
		return regressionErr
	}
	if _, err := runCommand("go", "vet", "./..."); err != nil {
		return err
	}
	manifestDigest, err := fileDigest(manifestPath)
	if err != nil {
		return err
	}
	sourceDigest, err := sourceTreeDigest(".")
	if err != nil {
		return err
	}
	artifactPaths := []string{
		focusedReceiptPath,
		regressionPath,
		"OUTPUT/phase-3/step-2-acceptance.json",
		"OUTPUT/phase-3/step-2-release-receipt.json",
		"OUTPUT/phase-3/step-3-authorization.json",
		"docs/architecture/004-exact-directed-channel-adapter.md",
	}
	artifacts := make([]artifact, 0, len(artifactPaths))
	for _, path := range artifactPaths {
		digest, err := fileDigest(path)
		if err != nil {
			return err
		}
		artifacts = append(artifacts, artifact{Path: path, SHA256: digest})
	}
	result := report{
		SchemaVersion: "1.0.0", ReportType: "PHASE_3_EXACT_DIRECTED_CHANNEL", Status: "PASS",
		AcceptedBaseCommit: acceptedBaseCommit, AcceptedBaseTree: acceptedBaseTree,
		ContractIdentity: contractIdentity, ManifestSHA256: manifestDigest, SourceTreeSHA256: sourceDigest,
		Environment: environment{GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Locale: "C", TimeZone: "UTC"},
		Executed: executed{
			FocusedTestCases: focusedTests.Tests, FocusedTestFailures: focusedTests.Failures, FocusedPackagesPassed: focusedTests.Packages,
			RegressionTestCases: regressionTests.Tests, RegressionTestFailures: regressionTests.Failures, RegressionPackages: regressionTests.Packages,
			VetFailures: 0,
		},
		Assertions: []assertion{
			{ID: "CHANNEL-001-EXACT-DIRECTED", Status: "PASS", Tests: []string{"TestReceiverInjectsTrustedSenderAndPreservesDeclaredDAG", "TestReceiverRejectsNonExactRecipientBeforeEndpoint", "TestReceiverRejectsRoleWildcardAndClientAssertedAuthentication"}, EvidenceFloor: "One valid actor FQN and one execution tuple must exactly match the configured receiver before dispatch; role, wildcard, stale execution, and client-authenticated identities do not reach the endpoint."},
			{ID: "CHANNEL-002-UNKNOWN-LOSSLESS", Status: "PASS", Tests: []string{"TestUnknownHistoricalTypesAndVersionsArePreservedWithoutAliases", "TestMalformedQuarantineOversizeAndSinkFailureAreObservable"}, EvidenceFloor: "Bounded unknown, unsupported, and malformed inputs are quarantined with byte-identical content, digest, trusted source, receive time, provenance, and reason; historical chat/ping/pong names are not aliases."},
			{ID: "CHANNEL-003-IDEMPOTENT-REDELIVERY", Status: "PASS", Tests: []string{"TestDuplicateChannelDeliveryCommitsOneOrganizationalEffect"}, EvidenceFloor: "Duplicate delivery through the real gateway, application handler, evaluator, and memory store returns the original receipt and commits one event."},
			{ID: "CHANNEL-004-DAG-PRESERVATION", Status: "PASS", Tests: []string{"TestReceiverInjectsTrustedSenderAndPreservesDeclaredDAG"}, EvidenceFloor: "The channel forwards only declared command DAG parents and synthesizes no edge from delivery, route, sender, or receive time."},
			{ID: "CHANNEL-005-UNCERTAIN-OUTCOME", Status: "PASS", Tests: []string{"TestReceiverPreservesPostDispatchTimeoutUncertainty"}, EvidenceFloor: "A timeout after dispatch remains explicitly outcome-unknown at the channel surface."},
			{ID: "CHANNEL-006-IMPORT-OWNERSHIP-BOUNDARY", Status: "PASS", Tests: []string{"TestThinAdapterProductionImportsRemainWithinBoundary"}, EvidenceFloor: "Channel production code imports only protocol and kernel packages; it has no MongoDB, persistence, delivery-claim, or ownership mutation dependency."},
		},
		ExcludedProfiles: []excludedProfile{
			{Profile: "broad-routing", Reason: "Role-class, wildcard, fan-out, and broadcast recipient-set, ownership, join, timeout, and partial-delivery semantics are not approved."},
			{Profile: "mongo-delivery-mechanics", Reason: "Change streams, outbox claims, claim epochs, leases, resume, readdressing, and dead letters remain unchanged and are not requalified by this slice."},
			{Profile: "historical-message-aliases", Reason: "tekroo-agent-chat, tekroo-agent-ping, and tekroo-agent-pong remain observed-but-undefined and are deliberately not mapped."},
			{Profile: "openhands-sma-provider-e2e", Reason: "Live providers, credentials, OpenHands, and SMA remain outside this authority."},
			{Profile: "deployment-migration-performance", Reason: "Public deployment, historical migration, production topology, load, latency, and throughput are not exercised."},
		},
		Artifacts: artifacts, DigestMethod: "SHA-256 of compact JSON with reportSha256 set to the empty string",
	}
	if focusedTests.Failures != 0 || regressionTests.Failures != 0 {
		result.Status = "FAIL"
	}
	canonical, err := json.Marshal(result)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(canonical)
	result.ReportSHA256 = hex.EncodeToString(digest[:])
	pretty, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(output, append(pretty, '\n'), 0o644)
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
		return errors.New("report digest mismatch")
	}
	if value.Status != "PASS" || value.AcceptedBaseCommit != acceptedBaseCommit || value.AcceptedBaseTree != acceptedBaseTree {
		return errors.New("report status or accepted base mismatch")
	}
	if err := verifyAcceptedBase(); err != nil {
		return err
	}
	manifestDigest, err := fileDigest(manifestPath)
	if err != nil || manifestDigest != value.ManifestSHA256 {
		return errors.New("manifest digest mismatch")
	}
	sourceDigest, err := sourceTreeDigest(".")
	if err != nil || sourceDigest != value.SourceTreeSHA256 {
		return errors.New("source tree digest mismatch")
	}
	for _, artifact := range value.Artifacts {
		digest, err := fileDigest(artifact.Path)
		if err != nil || digest != artifact.SHA256 {
			return fmt.Errorf("artifact digest mismatch: %s", artifact.Path)
		}
	}
	return nil
}

func verifyAcceptedBase() error {
	output, err := runCommand("git", "rev-parse", acceptedBaseCommit+"^{tree}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(output)) != acceptedBaseTree {
		return errors.New("accepted base tree mismatch")
	}
	return nil
}

func summarizeTests(output []byte) (testSummary, error) {
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
	return summary, scanner.Err()
}

func sourceTreeDigest(root string) (string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".idea", "build", "OUTPUT":
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

func fileDigest(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
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

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
