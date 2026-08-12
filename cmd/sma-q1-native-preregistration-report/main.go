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
	acceptedBaseCommit = "7c8a5e54987b101d2b04a18b76600393fe6b757c"
	acceptedBaseTree   = "73546d32c5c5b06179962b45c50d35b49739dfb5"
	priorManifestPath  = "investigations/sma-q1/preregistration.json"
	priorManifestSHA   = "e819e7de6cfdcf3a1d7c2100a7a7702b1cc6837b07b9242a3116523fc4245ac1"
	manifestPath       = "investigations/sma-q1/preregistration-native-v2.json"
	manifestSHA        = "c618371d571d5333aebd2bdc83d2db2559115f5ec3e1903b33e8e0ab157edf5d"
	reportPath         = "OUTPUT/phase-3/step-14a-sma-q1-native-preregistration-gate.json"
	regressionPath     = "OUTPUT/phase-3/step-14a-full-regression-tests.jsonl"
	smaRoot            = "/Users/paul/work/tekroo-ai/sma"
	smaBaselineCommit  = "d5d49ca18d5845b4ba172cf169d4d7b196c5b38d"
	smaBaselineTree    = "5f2ccb3734be6ae36d194ddabc91df4b002c90a2"
)

type manifest struct {
	SchemaVersion string `json:"schemaVersion"`
	RecordType    string `json:"recordType"`
	AmendmentID   string `json:"amendmentId"`
	Status        string `json:"status"`
	Supersession  struct {
		PriorInvestigationID string `json:"priorInvestigationId"`
		PriorManifestSHA256  string `json:"priorManifestSHA256"`
		Effect               string `json:"effect"`
		PriorExecutionState  string `json:"priorExecutionState"`
		PriorResultClaim     string `json:"priorResultClaim"`
	} `json:"supersession"`
	Architecture struct {
		SemanticBoundary    string `json:"semanticBoundary"`
		ModelTrafficProxy   string `json:"modelTrafficProxy"`
		CaptureThroughHook  bool   `json:"captureThroughHook"`
		PromptTimeCognition bool   `json:"promptTimeCognition"`
	} `json:"architectureReconciliation"`
	Authority struct {
		Purpose           string   `json:"purpose"`
		ExcludedAuthority []string `json:"excludedAuthority"`
		Level2Training    string   `json:"level2Training"`
	} `json:"authorityBoundary"`
	WP4Baseline struct {
		Commit                 string `json:"commit"`
		Tree                   string `json:"tree"`
		WorktreeState          string `json:"worktreeState"`
		WP5ArtifactObservation string `json:"wp5ArtifactObservation"`
	} `json:"wp4Baseline"`
	ExecutionPrerequisites    []string          `json:"executionPrerequisites"`
	RequiredIdentityReceipts  []string          `json:"requiredIdentityReceipts"`
	SyntheticMemoryCorpus     []json.RawMessage `json:"syntheticMemoryCorpus"`
	ScenarioCorpus            []scenario        `json:"scenarioCorpus"`
	WP5EvidenceMap            []evidenceMap     `json:"wp5EvidenceMap"`
	Step15ReservedScenarioIDs []string          `json:"step15ReservedScenarioIds"`
	Thresholds                struct {
		AbsoluteSafety map[string]int     `json:"absoluteSafety"`
		Semantic       map[string]float64 `json:"semantic"`
		Reliability    map[string]float64 `json:"reliability"`
		Latency        map[string]float64 `json:"latencyMilliseconds"`
		Bounds         map[string]int     `json:"bounds"`
		Measurement    map[string]string  `json:"measurement"`
	} `json:"thresholds"`
	RequiredRawReceipts           []string `json:"requiredRawReceiptsPerScenario"`
	StopConditions                []string `json:"stopConditions"`
	ExecutionState                string   `json:"executionState"`
	WP5ResultsObserved            bool     `json:"wp5ResultsObserved"`
	ScenarioResultClaimAuthorized bool     `json:"scenarioResultClaimAuthorized"`
	Step15ExecutionAuthorized     bool     `json:"step15ExecutionAuthorized"`
}

type scenario struct {
	ID          string `json:"id"`
	Class       string `json:"class"`
	Partition   string `json:"partition"`
	Repetitions int    `json:"repetitions"`
	Prompt      string `json:"prompt"`
	Fault       string `json:"fault"`
	Expected    string `json:"expected"`
}

type evidenceMap struct {
	WP5Criterion string   `json:"wp5Criterion"`
	ScenarioIDs  []string `json:"scenarioIds"`
	Eligibility  string   `json:"eligibility"`
}

type report struct {
	SchemaVersion       string            `json:"schemaVersion"`
	ReportType          string            `json:"reportType"`
	Status              string            `json:"status"`
	ResultClaim         string            `json:"resultClaim"`
	ExecutionState      string            `json:"executionState"`
	WP5ResultsObserved  bool              `json:"wp5ResultsObserved"`
	Step15Authorized    bool              `json:"step15Authorized"`
	AcceptedBaseCommit  string            `json:"acceptedBaseCommit"`
	AcceptedBaseTree    string            `json:"acceptedBaseTree"`
	PriorManifestSHA256 string            `json:"priorManifestSha256"`
	ManifestSHA256      string            `json:"manifestSha256"`
	SourceTreeSHA256    string            `json:"sourceTreeSha256"`
	Environment         environment       `json:"environment"`
	Validation          validation        `json:"validation"`
	FreezeObservation   freezeObservation `json:"freezeObservation"`
	Assertions          []assertion       `json:"assertions"`
	ExcludedProfiles    []excluded        `json:"excludedProfiles"`
	Artifacts           []artifact        `json:"artifacts"`
	DigestMethod        string            `json:"digestMethod"`
	ReportSHA256        string            `json:"reportSha256"`
}

type environment struct {
	GoVersion string `json:"goVersion"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	Locale    string `json:"locale"`
	TimeZone  string `json:"timeZone"`
}

type validation struct {
	StructuralChecks        int `json:"structuralChecks"`
	ScenarioDefinitions     int `json:"scenarioDefinitions"`
	ScenarioRepetitions     int `json:"scenarioRepetitions"`
	WP5EligibleScenarios    int `json:"wp5EligibleScenarios"`
	Step15ReservedScenarios int `json:"step15ReservedScenarios"`
	ScenariosExecuted       int `json:"scenariosExecuted"`
	RegressionTestCases     int `json:"regressionTestCases"`
	RegressionTestFailures  int `json:"regressionTestFailures"`
	VetFailures             int `json:"vetFailures"`
}

type freezeObservation struct {
	ObservationKind    string `json:"observationKind"`
	SMACommit          string `json:"smaCommit"`
	SMATree            string `json:"smaTree"`
	SMAWorktreeClean   bool   `json:"smaWorktreeClean"`
	SMAStatusSHA256    string `json:"smaStatusSha256"`
	WP5DocumentCount   int    `json:"wp5DocumentCount"`
	Gate5ArtifactCount int    `json:"gate5ArtifactCount"`
	Qualification      string `json:"qualification"`
}

type assertion struct {
	ID            string   `json:"id"`
	Status        string   `json:"status"`
	Evidence      []string `json:"evidence"`
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

type testSummary struct{ Tests, Failures int }

func main() {
	if len(os.Args) == 3 && os.Args[1] == "-verify" {
		fatal(verify(os.Args[2]))
		return
	}
	if len(os.Args) != 1 {
		fatal(errors.New("usage: sma-q1-native-preregistration-report | -verify path"))
	}
	fatal(run())
}

func run() error {
	if err := verifyAcceptedBase(); err != nil {
		return err
	}
	value, checks, repetitions, wp5Count, err := validateManifest()
	if err != nil {
		return err
	}
	observation, err := observeFreezeBoundary()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		return err
	}
	regression, regressionErr := command("go", "test", "-race", "-count=1", "-json", "./...")
	if err := os.WriteFile(regressionPath, regression, 0o644); err != nil {
		return err
	}
	tests, err := summarize(regression)
	if err != nil {
		return err
	}
	if regressionErr != nil {
		return regressionErr
	}
	if _, err := command("go", "vet", "./..."); err != nil {
		return err
	}
	sourceDigest, err := digestTree(".")
	if err != nil {
		return err
	}
	artifactPaths := []string{
		priorManifestPath,
		"OUTPUT/phase-3/step-14-sma-q1-preregistration-gate.json",
		"OUTPUT/phase-3/step-14a-authorization.json",
		manifestPath,
		"docs/architecture/018-sma-q1-native-path-amendment.md",
		regressionPath,
	}
	artifacts := make([]artifact, 0, len(artifactPaths))
	for _, path := range artifactPaths {
		digest, err := digestFile(path)
		if err != nil {
			return err
		}
		artifacts = append(artifacts, artifact{path, digest})
	}
	result := report{
		SchemaVersion: "1.0.0", ReportType: "PHASE_3_SMA_Q1_NATIVE_PREREGISTRATION_AMENDMENT_GATE",
		Status: "PASS_AMENDMENT", ResultClaim: "NOT_AUTHORIZED", ExecutionState: "NOT_STARTED",
		WP5ResultsObserved: false, Step15Authorized: false,
		AcceptedBaseCommit: acceptedBaseCommit, AcceptedBaseTree: acceptedBaseTree,
		PriorManifestSHA256: priorManifestSHA, ManifestSHA256: manifestSHA, SourceTreeSHA256: sourceDigest,
		Environment:       environment{runtime.Version(), runtime.GOOS, runtime.GOARCH, "C", "UTC"},
		Validation:        validation{checks, len(value.ScenarioCorpus), repetitions, wp5Count, len(value.Step15ReservedScenarioIDs), 0, tests.Tests, tests.Failures, 0},
		FreezeObservation: observation,
		Assertions: []assertion{
			{"SMAQ1N-PREREG-001-NONDESTRUCTIVE-SUPERSESSION", "PASS", []string{priorManifestPath, manifestPath}, "The original digest is unchanged, remains NOT_STARTED, and is superseded only for future execution."},
			{"SMAQ1N-PREREG-002-BEFORE-WP5-RESULTS", "PASS", []string{reportPath}, "The clean WP4 baseline was observed before any WP5 document or Gate 5 artifact; zero WP5 result is used."},
			{"SMAQ1N-PREREG-003-NATIVE-BOUNDARY", "PASS", []string{manifestPath, "docs/architecture/018-sma-q1-native-path-amendment.md"}, "Capture uses persisted events and delivery uses UserPromptSubmit additionalContext; model-traffic proxying and prompt-time cognition are excluded."},
			{"SMAQ1N-PREREG-004-FINITE-CORPUS", "PASS", []string{manifestPath}, "Four exact memories and eighteen exact scenarios with 96 fixed repetitions are content-addressed."},
			{"SMAQ1N-PREREG-005-WP5-EVIDENCE-FENCE", "PASS", []string{manifestPath}, "Eight scenarios may reuse WP5 evidence only on exact digest and field match; ten remain explicitly reserved for Step 15."},
			{"SMAQ1N-PREREG-006-THRESHOLDS-AND-RECEIPTS", "PASS", []string{manifestPath}, "Safety, semantic, reliability, latency, resource bounds, measurement rules, and raw receipt requirements are frozen."},
			{"SMAQ1N-PREREG-007-IDENTITY-LIMITATION", "PASS", []string{manifestPath}, "Canary workspace/profile partitioning is testable but is not represented as final durable authorship, role, scope, visibility, or Teams actor identity."},
			{"SMAQ1N-PREREG-008-AUTHORITY-BOUNDARY", "PASS", []string{manifestPath}, "SMA remains semantic memory only, protected retrieval fails closed, prompt submission fails open, and Level 2 remains disabled."},
		},
		ExcludedProfiles: []excluded{
			{"wp5-execution", "Teams does not execute, inspect, or adjudicate WP5 results in Step 14A."},
			{"sma-q1-execution", "No native hook, capture, retrieval, lifecycle, model, datastore, fault, latency, or contamination scenario is executed."},
			{"sma-mutation-or-operation", "The SMA source, services, databases, vectors, OpenHands, Ollama, and launchd state are not modified or operated."},
			{"step15-and-downstream", "Step 15, OpenHands-Q1, SMA-Q2, production rollout, and historical migration remain unauthorized."},
		},
		Artifacts:    artifacts,
		DigestMethod: "SHA-256 of compact JSON with reportSha256 set to the empty string",
	}
	canonical, err := json.Marshal(result)
	if err != nil {
		return err
	}
	result.ReportSHA256 = digestBytes(canonical)
	pretty, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(reportPath, append(pretty, '\n'), 0o644)
}

func validateManifest() (manifest, int, int, int, error) {
	var value manifest
	prior, err := os.ReadFile(priorManifestPath)
	if err != nil {
		return value, 0, 0, 0, err
	}
	if digestBytes(prior) != priorManifestSHA {
		return value, 0, 0, 0, errors.New("prior manifest digest changed")
	}
	var priorState struct {
		Status                string `json:"status"`
		ExecutionState        string `json:"executionState"`
		ResultClaimAuthorized bool   `json:"resultClaimAuthorized"`
	}
	if err := json.Unmarshal(prior, &priorState); err != nil {
		return value, 0, 0, 0, err
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return value, 0, 0, 0, err
	}
	if digestBytes(data) != manifestSHA {
		return value, 0, 0, 0, errors.New("native amendment digest changed")
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return value, 0, 0, 0, err
	}
	checks := 0
	check := func(ok bool, message string) error {
		if !ok {
			return errors.New(message)
		}
		checks++
		return nil
	}
	if err := check(priorState.Status == "FROZEN_CANDIDATE_NOT_EXECUTED" && priorState.ExecutionState == "NOT_STARTED" && !priorState.ResultClaimAuthorized, "prior manifest state changed"); err != nil {
		return value, checks, 0, 0, err
	}
	if err := check(value.SchemaVersion == "2.0.0" && value.RecordType == "INVESTIGATION_PREREGISTRATION_AMENDMENT" && value.AmendmentID == "SMA-Q1-NATIVE-OPENHANDS-VERTICAL-SLICE-V2", "amendment identity changed"); err != nil {
		return value, checks, 0, 0, err
	}
	if err := check(value.Status == "FROZEN_CANDIDATE_NOT_EXECUTED" && value.ExecutionState == "NOT_STARTED" && !value.WP5ResultsObserved && !value.ScenarioResultClaimAuthorized && !value.Step15ExecutionAuthorized, "amendment execution state changed"); err != nil {
		return value, checks, 0, 0, err
	}
	if err := check(value.Supersession.PriorInvestigationID == "SMA-Q1-CORE-PROXY-VERTICAL-SLICE" && value.Supersession.PriorManifestSHA256 == priorManifestSHA && value.Supersession.Effect == "SUPERSEDED_FOR_FUTURE_EXECUTION_ONLY" && value.Supersession.PriorExecutionState == "NOT_STARTED" && value.Supersession.PriorResultClaim == "NONE", "supersession rule changed"); err != nil {
		return value, checks, 0, 0, err
	}
	if err := check(value.Architecture.ModelTrafficProxy == "EXCLUDED" && !value.Architecture.CaptureThroughHook && !value.Architecture.PromptTimeCognition && strings.Contains(value.Architecture.SemanticBoundary, "OpenHands Agent Server"), "native architecture boundary changed"); err != nil {
		return value, checks, 0, 0, err
	}
	if err := check(value.Authority.Purpose == "SEMANTIC_MEMORY_ONLY" && value.Authority.Level2Training == "DEFERRED_AND_DISABLED" && equalSet(value.Authority.ExcludedAuthority, []string{"workflow state", "process control", "task audit", "acceptance", "organizational authority", "outcome measurement"}), "authority boundary changed"); err != nil {
		return value, checks, 0, 0, err
	}
	if err := check(value.WP4Baseline.Commit == smaBaselineCommit && value.WP4Baseline.Tree == smaBaselineTree && value.WP4Baseline.WorktreeState == "CLEAN" && value.WP4Baseline.WP5ArtifactObservation == "NO_DOCS_OR_TARGET_GATE5_ARTIFACT_OBSERVED", "WP4 baseline changed"); err != nil {
		return value, checks, 0, 0, err
	}
	if err := check(len(value.ExecutionPrerequisites) == 10 && len(value.RequiredIdentityReceipts) == 11 && len(value.RequiredRawReceipts) == 8, "execution receipt requirements incomplete"); err != nil {
		return value, checks, 0, 0, err
	}
	if err := check(len(value.SyntheticMemoryCorpus) == 4, "synthetic memory corpus incomplete"); err != nil {
		return value, checks, 0, 0, err
	}
	expectedIDs := []string{
		"SMAQ1N-001-FIRST-PROMPT-EMPTY", "SMAQ1N-002-SAME-PARTITION-RECALL", "SMAQ1N-003-CROSS-PARTITION-DENIAL",
		"SMAQ1N-004-CURRENT-INSTRUCTION-PRECEDENCE", "SMAQ1N-005-RAW-INELIGIBLE", "SMAQ1N-006-DUPLICATE-EVENT-CAPTURE",
		"SMAQ1N-007-RETRIEVAL-OUTAGE", "SMAQ1N-008-CAPTURE-OUTAGE-RECONCILIATION", "SMAQ1N-009-ADDITIONAL-CONTEXT-INTEGRITY",
		"SMAQ1N-010-HOOK-DEADLINE-AND-MALFORMED", "SMAQ1N-011-PARENT-CHILD-PROVENANCE", "SMAQ1N-012-RESTART-PARTITION-CONTINUITY",
		"SMAQ1N-013-FOUR-CHANNEL-CONCURRENCY", "SMAQ1N-014-FEEDBACK-LOOP-PREVENTION", "SMAQ1N-015-SECRET-QUARANTINE",
		"SMAQ1N-016-EMPTY-RESULT", "SMAQ1N-017-OVERSIZED-CONTEXT-BOUND", "SMAQ1N-018-CONDENSATION-REANCHOR",
	}
	expectedRepetitions := []int{10, 10, 10, 5, 3, 3, 5, 5, 5, 5, 5, 3, 5, 3, 3, 10, 3, 3}
	repetitions := 0
	scenarioSet := map[string]bool{}
	scenariosOK := len(value.ScenarioCorpus) == len(expectedIDs)
	if scenariosOK {
		for i, item := range value.ScenarioCorpus {
			if item.ID != expectedIDs[i] || item.Repetitions != expectedRepetitions[i] || item.Class == "" || item.Prompt == "" || item.Fault == "" || item.Expected == "" || scenarioSet[item.ID] {
				scenariosOK = false
				break
			}
			scenarioSet[item.ID] = true
			repetitions += item.Repetitions
		}
	}
	if err := check(scenariosOK && repetitions == 96, "scenario corpus changed or incomplete"); err != nil {
		return value, checks, 0, 0, err
	}
	wp5Set := map[string]bool{}
	mapOK := len(value.WP5EvidenceMap) == 6
	for _, mapping := range value.WP5EvidenceMap {
		if mapping.WP5Criterion == "" || mapping.Eligibility != "ELIGIBLE_IF_EXACT" {
			mapOK = false
		}
		for _, id := range mapping.ScenarioIDs {
			if !scenarioSet[id] || wp5Set[id] {
				mapOK = false
			}
			wp5Set[id] = true
		}
	}
	reservedSet := map[string]bool{}
	for _, id := range value.Step15ReservedScenarioIDs {
		if !scenarioSet[id] || wp5Set[id] || reservedSet[id] {
			mapOK = false
		}
		reservedSet[id] = true
	}
	if err := check(mapOK && len(wp5Set) == 8 && len(reservedSet) == 10 && len(wp5Set)+len(reservedSet) == len(scenarioSet), "WP5 and Step 15 scenario partition changed"); err != nil {
		return value, checks, 0, 0, err
	}
	abs := map[string]int{"crossPartitionLeakCount": 0, "secretExposureCount": 0, "rawMemoryInjectionCount": 0, "currentInstructionOverrideCount": 0, "feedbackLoopMemoryCount": 0, "unboundedRequestResponseContextOrQueueCount": 0, "lostRawProvenanceCount": 0}
	semantic := map[string]float64{"samePartitionExpectedRecallAt5": 1, "samePartitionAnswerAccuracy": 1, "irrelevantOrEmptyQueryInjectionRate": 0, "currentInstructionPrecedenceRate": 1}
	reliability := map[string]float64{"healthyPromptSubmissionRate": 1, "retrievalOutagePromptSubmissionRate": 1, "captureOutagePromptSubmissionRate": 1, "duplicateCaptureCountPerOpenHandsEventId": 0, "boundedScenarioTerminationRate": 1, "promptByteIdentityRate": 1}
	latency := map[string]float64{"contextServiceHardDeadline": 500, "contextServiceWarmHitP95": 200, "contextServiceWarmNoHitP95": 200, "hookHardTimeout": 1000, "residentHookOverheadP95": 500, "scenarioWallClockMaximum": 120000}
	bounds := map[string]int{"maximumInjectedMemories": 5, "maximumInjectedContextCharacters": 10000, "maximumInboundBridgeRequestBytes": 1048576, "maximumReceiptPayloadBytes": 2097152, "maximumConcurrentChannels": 4, "maximumQueuedOperations": 8, "automaticRawEvidenceCharacters": 0}
	if err := check(equalJSON(value.Thresholds.AbsoluteSafety, abs) && equalJSON(value.Thresholds.Semantic, semantic) && equalJSON(value.Thresholds.Reliability, reliability) && equalJSON(value.Thresholds.Latency, latency) && equalJSON(value.Thresholds.Bounds, bounds) && len(value.Thresholds.Measurement) == 5, "threshold set changed"); err != nil {
		return value, checks, 0, 0, err
	}
	if err := check(len(value.StopConditions) == 7, "stop conditions incomplete"); err != nil {
		return value, checks, 0, 0, err
	}
	return value, checks, repetitions, len(wp5Set), nil
}

func observeFreezeBoundary() (freezeObservation, error) {
	result := freezeObservation{ObservationKind: "READ_ONLY_PRE_WP5_RESULT_FREEZE_BOUNDARY"}
	head, err := commandIn(smaRoot, "git", "rev-parse", "HEAD")
	if err != nil {
		return result, err
	}
	tree, err := commandIn(smaRoot, "git", "rev-parse", "HEAD^{tree}")
	if err != nil {
		return result, err
	}
	status, err := commandIn(smaRoot, "git", "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return result, err
	}
	result.SMACommit = strings.TrimSpace(string(head))
	result.SMATree = strings.TrimSpace(string(tree))
	result.SMAWorktreeClean = len(status) == 0
	result.SMAStatusSHA256 = digestBytes(status)
	documents, err := filepath.Glob(filepath.Join(smaRoot, "docs", "WP5*"))
	if err != nil {
		return result, err
	}
	result.WP5DocumentCount = len(documents)
	gate5Count := 0
	target := filepath.Join(smaRoot, "target")
	if _, statErr := os.Stat(target); statErr == nil {
		err = filepath.WalkDir(target, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() && strings.Contains(strings.ToLower(entry.Name()), "gate5") {
				gate5Count++
			}
			return nil
		})
		if err != nil {
			return result, err
		}
	} else if !os.IsNotExist(statErr) {
		return result, statErr
	}
	result.Gate5ArtifactCount = gate5Count
	result.Qualification = "This observation proves only that the exact clean WP4 baseline and no named WP5/Gate 5 artifact were present when the amendment gate ran; it is not a WP5 or SMA-Q1 result."
	if result.SMACommit != smaBaselineCommit || result.SMATree != smaBaselineTree || !result.SMAWorktreeClean || result.WP5DocumentCount != 0 || result.Gate5ArtifactCount != 0 {
		return result, errors.New("WP5 work or results were observed before amendment freeze")
	}
	return result, nil
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
	if value.Status != "PASS_AMENDMENT" || value.ResultClaim != "NOT_AUTHORIZED" || value.ExecutionState != "NOT_STARTED" || value.WP5ResultsObserved || value.Step15Authorized || value.Validation.ScenariosExecuted != 0 || reported != digestBytes(canonical) {
		return errors.New("report state or self-digest mismatch")
	}
	if err := verifyAcceptedBase(); err != nil {
		return err
	}
	if _, _, _, _, err := validateManifest(); err != nil {
		return err
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

func verifyAcceptedBase() error {
	output, err := command("git", "rev-parse", acceptedBaseCommit+"^{tree}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(output)) != acceptedBaseTree {
		return errors.New("accepted base tree mismatch")
	}
	return nil
}

func summarize(data []byte) (testSummary, error) {
	var result testSummary
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
			result.Tests++
		}
		if event.Action == "fail" && event.Test != "" {
			result.Failures++
		}
	}
	return result, scanner.Err()
}

func digestTree(root string) (string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && path != root && (entry.Name() == ".git" || entry.Name() == ".idea" || entry.Name() == "build" || entry.Name() == "OUTPUT") {
			return filepath.SkipDir
		}
		if !entry.IsDir() {
			paths = append(paths, path)
		}
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
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return "", err
		}
		hash.Write([]byte(filepath.ToSlash(relative)))
		hash.Write([]byte{0})
		hash.Write(data)
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func digestFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return digestBytes(data), nil
}

func digestBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func equalSet(left, right []string) bool {
	a, b := append([]string(nil), left...), append([]string(nil), right...)
	sort.Strings(a)
	sort.Strings(b)
	return equalJSON(a, b)
}

func equalJSON(left, right any) bool {
	a, errA := json.Marshal(left)
	b, errB := json.Marshal(right)
	return errA == nil && errB == nil && bytes.Equal(a, b)
}

func command(name string, arguments ...string) ([]byte, error) {
	return commandIn("", name, arguments...)
}

func commandIn(directory, name string, arguments ...string) ([]byte, error) {
	cmd := exec.Command(name, arguments...)
	cmd.Dir = directory
	cmd.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC", "GIT_TERMINAL_PROMPT=0", "GIT_NO_LAZY_FETCH=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s %s: %w\n%s", name, strings.Join(arguments, " "), err, output)
	}
	return output, nil
}

func fatal(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
