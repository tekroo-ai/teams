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
	acceptedBaseCommit = "e19e3fc647d20e5c487b04bbd08c80d8d9289a6d"
	acceptedBaseTree   = "2f5460d4a35d6f6a83de6735dd3b81f8e76917b6"
	contractIdentity   = "tekroo.kernel.contracts/0.2.0"
	manifestPath       = "CONTRACTS/tekroo.kernel.contracts/0.2.0/manifest.json"
	defaultReportPath  = "OUTPUT/phase-3/step-1-thin-adapter-gate.json"
	adapterReceiptPath = "OUTPUT/phase-3/step-1-thin-adapter-tests.jsonl"
	regressionPath     = "OUTPUT/phase-3/step-1-full-regression-tests.jsonl"
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
	AdapterTestCases       int `json:"adapterTestCases"`
	AdapterTestFailures    int `json:"adapterTestFailures"`
	AdapterPackagesPassed  int `json:"adapterPackagesPassed"`
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
		fatal(errors.New("usage: thin-adapter-report [-output path] | -verify path"))
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
	adapterOutput, adapterErr := runCommand("go", "test", "-race", "-count=1", "-json", "./adapters/protocol", "./adapters/stdio", "./adapters/daemon", "./adapters/cli")
	if err := os.WriteFile(adapterReceiptPath, adapterOutput, 0o644); err != nil {
		return err
	}
	adapterTests, err := summarizeTests(adapterOutput)
	if err != nil {
		return err
	}
	if adapterErr != nil {
		return adapterErr
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
		adapterReceiptPath,
		regressionPath,
		"OUTPUT/phase-3/step-1-authorization.json",
		"docs/architecture/002-thin-adapter-bootstrap.md",
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
		SchemaVersion: "1.0.0", ReportType: "PHASE_3_THIN_ADAPTER_BOOTSTRAP", Status: "PASS",
		AcceptedBaseCommit: acceptedBaseCommit, AcceptedBaseTree: acceptedBaseTree,
		ContractIdentity: contractIdentity, ManifestSHA256: manifestDigest, SourceTreeSHA256: sourceDigest,
		Environment: environment{GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Locale: "C", TimeZone: "UTC"},
		Executed: executed{
			AdapterTestCases: adapterTests.Tests, AdapterTestFailures: adapterTests.Failures, AdapterPackagesPassed: adapterTests.Packages,
			RegressionTestCases: regressionTests.Tests, RegressionTestFailures: regressionTests.Failures, RegressionPackages: regressionTests.Packages,
			VetFailures: 0,
		},
		Assertions: []assertion{
			{ID: "ADAPTER-001-TYPED-SERVICE", Status: "PASS", Tests: []string{"TestGatewayInvokesApplicationHandlerAndPreservesIdempotentReceipt", "TestGatewayCarriesExactAuthenticatedContextToTypedService"}, EvidenceFloor: "The gateway invokes the accepted typed application handler and preserves its idempotent stored receipt."},
			{ID: "ADAPTER-002-IDENTITY-AND-FENCE", Status: "PASS", Tests: []string{"TestGatewayRejectsIdentityFenceAndIdempotencyMismatchBeforeService"}, EvidenceFloor: "Mismatched authenticated principal, actor FQN, execution tuple, and missing semantic idempotency are rejected before service dispatch."},
			{ID: "ADAPTER-003-BOUNDED-COMPLETION", Status: "PASS", Tests: []string{"TestGatewayTimeoutAfterDispatchRemainsExplicitlyUncertain", "TestGatewayCancellationAfterDispatchRemainsExplicitlyUncertain", "TestGatewayCancellationBeforeDispatchIsKnownNoEffect", "TestGatewayCompletesWhenServiceIgnoresCancellation"}, EvidenceFloor: "Pre-dispatch cancellation reports known no effect; post-dispatch timeout/cancellation reports uncertainty; gateway completion remains bounded when a service ignores cancellation."},
			{ID: "ADAPTER-004-STRICT-FRAMING", Status: "PASS", Tests: []string{"TestStrictRequestCodecRejectsUnknownOrMultipleValues", "TestServerProcessesStrictFramesAndPreservesOneResponsePerFrame"}, EvidenceFloor: "Strict JSON and bounded newline framing execute, including malformed-frame recovery with one response per parsed frame."},
			{ID: "ADAPTER-005-THIN-IMPORT-BOUNDARY", Status: "PASS", Tests: []string{"TestThinAdapterProductionImportsRemainWithinBoundary"}, EvidenceFloor: "The production protocol, stdio, daemon, and CLI packages import only their explicit internal thin-adapter/kernel allowlist and no persistence or MongoDB package."},
			{ID: "ADAPTER-006-DAEMON-AND-CLI", Status: "PASS", Tests: []string{"TestHostCancellationClosesInputAndCompletes", "TestHostShutdownTimeoutKeepsRunFenceUntilServerStops", "TestInvokeReturnsStableExitCodesAndJSONResponse", "TestInvokeRejectsMalformedInputWithoutCallingEndpoint"}, EvidenceFloor: "The daemon exits through closeable-input cancellation, retains its one-run fence while a timed-out server remains alive, and the injectable CLI returns stable success, invocation-failure, operational, and usage codes."},
		},
		ExcludedProfiles: []excludedProfile{
			{Profile: "http-mcp", Reason: "Only a semantic endpoint contract is present; no HTTP or MCP transport is implemented or qualified."},
			{Profile: "channels", Reason: "Only exact directed-recipient semantics are present; no channel transport is implemented or qualified."},
			{Profile: "openhands-sma-provider-e2e", Reason: "Live providers, credentials, models, OpenHands, and SMA require separate authority and qualification."},
			{Profile: "deployment-migration-performance", Reason: "Production deployment, historical migration, load, latency, and throughput are not exercised by this gate."},
		},
		Artifacts:    artifacts,
		DigestMethod: "SHA-256 of compact JSON with reportSha256 set to the empty string",
	}
	if adapterTests.Failures != 0 || regressionTests.Failures != 0 {
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
