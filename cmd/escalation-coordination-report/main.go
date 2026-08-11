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
	baseCommit    = "53929a5678ea6682c6e3a96f85b3d4b7a7414caa"
	baseTree      = "df8a35901a7eef3506fd6af90cad6ca592ad1bfa"
	contractRoot  = "CONTRACTS/tekroo.kernel.contracts/0.4.0"
	reportPath    = "OUTPUT/phase-3/step-9-escalation-coordination-gate.json"
	focusPath     = "OUTPUT/phase-3/step-9-escalation-coordination-tests.jsonl"
	fullPath      = "OUTPUT/phase-3/step-9-implementation-full-regression-tests.jsonl"
	mongoPath     = "OUTPUT/phase-3/step-9-mongo-integration-tests.jsonl"
	structurePath = "OUTPUT/phase-3/step-9-implementation-contract-structure.json"
	referencePath = "OUTPUT/phase-3/step-9-implementation-contract-reference.json"
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
	MongoIntegrationCases   int `json:"mongoIntegrationCases"`
	MongoIntegrationFailure int `json:"mongoIntegrationFailures"`
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
		fatal(errors.New("usage: escalation-coordination-report | -verify path"))
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
	focused, focusedErr := command("go", "test", "-race", "-count=1", "-json", "./kernel", "./application", "./adapters/memory")
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
	mongo, mongoErr := command("go", "test", "-tags", "mongo_integration", "-count=1", "-json", "./adapters/mongo")
	if err := os.WriteFile(mongoPath, mongo, 0o644); err != nil {
		return err
	}
	msum, err := summarize(mongo)
	if err != nil || mongoErr != nil {
		return firstError(err, mongoErr)
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
		structurePath, referencePath, focusPath, fullPath, mongoPath,
		"OUTPUT/phase-3/step-9-contract-acceptance.json",
		"OUTPUT/phase-3/step-9-contract-release-receipt.json",
		"OUTPUT/phase-3/step-9-implementation-authorization.json",
		"OUTPUT/phase-3/step-9-escalation-contract-gate.json",
		"docs/architecture/011-deterministic-escalation-coordination.md",
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
		SchemaVersion: "1.0.0", ReportType: "PHASE_3_DETERMINISTIC_ESCALATION_COORDINATION", Status: "PASS",
		AcceptedBaseCommit: baseCommit, AcceptedBaseTree: baseTree, ContractIdentity: "tekroo.kernel.contracts/0.4.0", ManifestSHA256: manifest, SourceTreeSHA256: source,
		Environment: environment{runtime.Version(), runtime.GOOS, runtime.GOARCH, "C", "UTC"},
		Executed:    executed{structureChecks, fixtureCases, fsum.tests, fsum.failures, rsum.tests, rsum.failures, msum.tests, msum.failures, 0},
		Assertions: []assertion{
			{"ESCALATE-001-EXACT-OPENING", "PASS", []string{"TestPlanEscalationOpeningCanonicalizesEvidenceAndRetainsDirectedPath", "TestEscalationCoordinatorOpensExactPolicyOwnedEscalation", "TestEscalationOpenPolicyFencesSemanticDuplicateAndSubjectLifecycle"}, "Opening binds one exact nonterminal subject revision and lifecycle, bounded authorities and policy, ordered causal path, condition digest, question, and canonical evidence."},
			{"ESCALATE-002-SEMANTIC-UNIQUENESS", "PASS", []string{"TestPlanEscalationOpeningFailsClosedOnDuplicateAndBounds", "TestStoreAllowsOneConcurrentEscalationPerSemanticKey", "TestEscalationProjectionSurvivesRestartAndRejectsSemanticDuplicate"}, "The semantic key is checked by the evaluator and transactionally retained by both repositories, giving concurrent equivalent openings one winner."},
			{"ESCALATE-003-BOUNDED-TERMINAL", "PASS", []string{"TestPlanEscalationResolutionEnforcesAuthorityDeadlineAndTerminality", "TestTimeoutResolutionRequiresPolicyAfterDeadlineAndBoundedOutcome", "TestEscalationResolutionUsesTrustedDecisionTimeAndExactAdjudicator"}, "Only the exact adjudicator within budget and deadline, or the exact timeout policy strictly after deadline with a bounded outcome, can make an open escalation terminal."},
			{"ESCALATE-004-DURABLE-PROJECTION", "PASS", []string{"TestStorePersistsEscalationOpeningAndTerminalResolution", "TestEscalationProjectionSurvivesRestartAndRejectsSemanticDuplicate"}, "Opening and resolution projections preserve their event IDs, subject binding, semantic key, revision, state, and terminal outcome across repository reload or restart."},
			{"ESCALATE-005-CONTRACT-CONFORMANCE", "PASS", []string{"TestAllFrozenCommandsReachTheirDeclaredEventThroughEvaluator", "TestEscalationCoordinatorOpensExactPolicyOwnedEscalation", "TestEscalationCoordinatorResolvesOnlyAsPlannedAuthority"}, "Both escalation commands validate against the released 0.4.0 schemas and reach their one declared event through the authoritative evaluator."},
			{"ESCALATE-006-NO-IMPLICIT-CONTROL", "PASS", []string{"TestEscalationCoordinatorDoesNotCommandForDuplicateOrInvalidAttribution", "TestApplicationProductionImportsRemainKernelOnly"}, "Invalid or duplicate plans issue no command; the slice contains no trigger discovery, provider effect, OpenHands, SMA, Git, release, or deployment control."},
		},
		ExcludedProfiles: []excluded{
			{"trigger-detection", "Upstream policy must explicitly classify a supported trigger; this slice does not infer one from retries, validation, blocking, or handoffs."},
			{"successor-and-process-control", "The slice records escalation decisions but does not choose or execute retries, ownership transfers, handoffs, or successor work."},
			{"organizational-acceptance-release-provider", "Acceptance, release planning, synthesized merge, and Git/provider execution remain separate slices."},
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
