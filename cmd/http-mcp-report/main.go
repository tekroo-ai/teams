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
	acceptedBaseCommit = "b5dcd667471d0d81d8ddb860d8cdf56d0c1119bb"
	acceptedBaseTree   = "ddb54a5349e8ecf9c7d3e9826cd7b546756b32f1"
	contractIdentity   = "tekroo.kernel.contracts/0.2.0"
	manifestPath       = "CONTRACTS/tekroo.kernel.contracts/0.2.0/manifest.json"
	defaultReportPath  = "OUTPUT/phase-3/step-2-http-mcp-gate.json"
	focusedReceiptPath = "OUTPUT/phase-3/step-2-http-mcp-tests.jsonl"
	regressionPath     = "OUTPUT/phase-3/step-2-full-regression-tests.jsonl"
)

type report struct {
	SchemaVersion      string            `json:"schemaVersion"`
	ReportType         string            `json:"reportType"`
	Status             string            `json:"status"`
	AcceptedBaseCommit string            `json:"acceptedBaseCommit"`
	AcceptedBaseTree   string            `json:"acceptedBaseTree"`
	ContractIdentity   string            `json:"contractIdentity"`
	ManifestSHA256     string            `json:"manifestSha256"`
	MCPWireBasis       mcpWireBasis      `json:"mcpWireBasis"`
	SourceTreeSHA256   string            `json:"sourceTreeSha256"`
	Environment        environment       `json:"environment"`
	Executed           executed          `json:"executed"`
	Assertions         []assertion       `json:"assertions"`
	ExcludedProfiles   []excludedProfile `json:"excludedProfiles"`
	Artifacts          []artifact        `json:"artifacts"`
	DigestMethod       string            `json:"digestMethod"`
	ReportSHA256       string            `json:"reportSha256"`
}

type mcpWireBasis struct {
	ProtocolVersion string   `json:"protocolVersion"`
	OfficialSources []string `json:"officialSources"`
	ObservedOn      string   `json:"observedOn"`
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
		fatal(errors.New("usage: http-mcp-report [-output path] | -verify path"))
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
	focusedOutput, focusedErr := runCommand("go", "test", "-race", "-count=1", "-json", "./adapters/protocol", "./adapters/httpapi", "./adapters/mcp")
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
		"OUTPUT/phase-3/step-1-acceptance.json",
		"OUTPUT/phase-3/step-1-release-receipt.json",
		"OUTPUT/phase-3/step-2-authorization.json",
		"docs/architecture/003-http-mcp-adapter.md",
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
		SchemaVersion: "1.0.0", ReportType: "PHASE_3_HTTP_MCP_ADAPTER", Status: "PASS",
		AcceptedBaseCommit: acceptedBaseCommit, AcceptedBaseTree: acceptedBaseTree,
		ContractIdentity: contractIdentity, ManifestSHA256: manifestDigest,
		MCPWireBasis: mcpWireBasis{
			ProtocolVersion: "2026-07-28", ObservedOn: "2026-08-11",
			OfficialSources: []string{
				"https://modelcontextprotocol.io/specification/2026-07-28/basic",
				"https://modelcontextprotocol.io/specification/2026-07-28/server/discover",
				"https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/streamable-http",
				"https://modelcontextprotocol.io/specification/2026-07-28/server/tools",
			},
		},
		SourceTreeSHA256: sourceDigest,
		Environment:      environment{GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Locale: "C", TimeZone: "UTC"},
		Executed: executed{
			FocusedTestCases: focusedTests.Tests, FocusedTestFailures: focusedTests.Failures, FocusedPackagesPassed: focusedTests.Packages,
			RegressionTestCases: regressionTests.Tests, RegressionTestFailures: regressionTests.Failures, RegressionPackages: regressionTests.Packages,
			VetFailures: 0,
		},
		Assertions: []assertion{
			{ID: "HTTP-MCP-001-TYPED-HTTP", Status: "PASS", Tests: []string{"TestHTTPHandlerThroughGatewayAndApplicationCommitsOnceOnReplay", "TestHTTPHandlerInjectsServerAuthenticatedIdentity"}, EvidenceFloor: "HTTP invokes the accepted gateway/application path, commits one event across replay, and injects authenticated identity outside client JSON."},
			{ID: "HTTP-MCP-002-HTTP-FAIL-CLOSED", Status: "PASS", Tests: []string{"TestHTTPHandlerRejectsClientAssertedIdentityAndSecurityFailuresBeforeDispatch", "TestHTTPHandlerFailsClosedOnAuthenticationRateAndBodyBound", "TestGatewayDoesNotExposeServiceErrorDetails"}, EvidenceFloor: "Client-asserted identity, forbidden origins, authentication failure, rate denial, content-type errors, and oversized bodies do not dispatch; arbitrary internal service error text is not exposed."},
			{ID: "HTTP-MCP-003-CURRENT-STATELESS-MCP", Status: "PASS", Tests: []string{"TestMCPStatelessToolsListAndCommandCall", "TestMCPValidatesPerRequestMetadataAndMirroredHeaders", "TestMCPRejectsMissingBodyMetadataAndNonRequestMessages", "TestMCPUnknownMethodUsesHTTP404AndJSONRPCMethodNotFound"}, EvidenceFloor: "The current 2026-07-28 stateless request model, mandatory server discovery, required body metadata, mirrored header checks, resultType, deterministic tool listing, strict request direction, and unknown-method HTTP status execute."},
			{ID: "HTTP-MCP-004-MCP-FAIL-CLOSED", Status: "PASS", Tests: []string{"TestMCPFailsClosedOnOriginAuthenticationRateAcceptBodyAndMethod"}, EvidenceFloor: "Origin, authentication, rate, content negotiation, body, and POST-only constraints fail closed before tool dispatch."},
			{ID: "HTTP-MCP-005-UNCERTAIN-OUTCOME", Status: "PASS", Tests: []string{"TestHTTPHandlerMapsUncertainOutcomeWithoutClaimingRollback", "TestMCPToolPreservesGatewayTimeoutUncertainty"}, EvidenceFloor: "HTTP and MCP preserve post-dispatch timeout uncertainty and do not translate transport failure into rollback or command failure."},
			{ID: "HTTP-MCP-006-IMPORT-BOUNDARY", Status: "PASS", Tests: []string{"TestThinAdapterProductionImportsRemainWithinBoundary"}, EvidenceFloor: "HTTP and MCP production packages import only explicit thin transport/protocol dependencies and no persistence or MongoDB package."},
		},
		ExcludedProfiles: []excludedProfile{
			{Profile: "mcp-sse-mrtr-subscriptions", Reason: "Request-scoped SSE progress, subscriptions, and multi-round-trip requests are not implemented or qualified."},
			{Profile: "legacy-mcp", Reason: "Initialization, protocol sessions, GET streams, server-initiated requests, and resumability from older MCP revisions are deliberately unsupported."},
			{Profile: "channels", Reason: "Channel transport remains a later separately qualified slice."},
			{Profile: "openhands-sma-provider-e2e", Reason: "Live providers, credentials, OpenHands, and SMA remain outside this authority."},
			{Profile: "deployment-migration-performance", Reason: "Public deployment, historical migration, production topology, load, latency, and throughput are not exercised."},
		},
		Artifacts:    artifacts,
		DigestMethod: "SHA-256 of compact JSON with reportSha256 set to the empty string",
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
	if value.Status != "PASS" || value.AcceptedBaseCommit != acceptedBaseCommit || value.AcceptedBaseTree != acceptedBaseTree || value.MCPWireBasis.ProtocolVersion != "2026-07-28" {
		return errors.New("report status, accepted base, or MCP wire basis mismatch")
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
