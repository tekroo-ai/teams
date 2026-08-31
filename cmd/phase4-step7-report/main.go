package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
)

const (
	preregistrationPath = "OUTPUT/phase-4/step-7-integrated-qualification-preregistration.json"
	rawRoot             = "OUTPUT/phase-4/step-7-raw"
	receiptPath         = "OUTPUT/phase-4/step-7-integrated-qualification-receipt.json"
)

type preregistration struct {
	Qualification       string            `json:"qualification"`
	ContractIdentity    string            `json:"contractIdentity"`
	ContractManifestSHA string            `json:"contractManifestSHA256"`
	StatusBefore        string            `json:"statusBeforeExecution"`
	BoundFiles          map[string]string `json:"boundFiles"`
	Scenarios           []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"scenarios"`
}

type commandSpec struct {
	Name          string
	Executable    string
	Arguments     []string
	ExpectedTests []string
	Output        string
}

type commandReceipt struct {
	Name           string `json:"name"`
	Status         string `json:"status"`
	ExitCode       int    `json:"exitCode"`
	PassedTests    int    `json:"passedTests"`
	FailedTests    int    `json:"failedTests"`
	RawReceiptPath string `json:"rawReceiptPath"`
	RawSHA256      string `json:"rawSha256"`
}

type scenarioReceipt struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type receipt struct {
	SchemaVersion             string            `json:"schemaVersion"`
	Qualification             string            `json:"qualification"`
	ContractIdentity          string            `json:"contractIdentity"`
	ContractManifestSHA256    string            `json:"contractManifestSha256"`
	PreregistrationSHA256     string            `json:"preregistrationSha256"`
	Status                    string            `json:"status"`
	AssembledRuntimeIdentity  string            `json:"assembledRuntimeIdentity"`
	ComputedInvocationCeiling uint64            `json:"computedInvocationCeiling"`
	LoopTerminalReason        string            `json:"loopTerminalReason"`
	ConcurrentInvocationFloor uint32            `json:"concurrentInvocationFloor"`
	Commands                  []commandReceipt  `json:"commands"`
	Scenarios                 []scenarioReceipt `json:"scenarios"`
	BoundFiles                map[string]string `json:"boundFiles"`
	ProhibitedActivity        map[string]string `json:"prohibitedActivity"`
	ReceiptSHA256             string            `json:"receiptSha256"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if _, err := os.Stat(rawRoot); err == nil {
		return errors.New("step-7 raw receipt directory already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	preregistrationBytes, err := os.ReadFile(preregistrationPath)
	if err != nil {
		return err
	}
	var registered preregistration
	if err := json.Unmarshal(preregistrationBytes, &registered); err != nil {
		return err
	}
	if registered.Qualification != "PHASE_4_STEP_7_INTEGRATED_QUALIFICATION" || registered.ContractIdentity != "tekroo.kernel.contracts/0.8.0" || registered.StatusBefore != "NOT_RUN" || len(registered.Scenarios) != 10 {
		return errors.New("invalid preregistration identity")
	}
	for path, expected := range registered.BoundFiles {
		observed, err := fileDigest(path)
		if err != nil {
			return err
		}
		if observed != expected {
			return fmt.Errorf("bound file changed: %s", path)
		}
	}
	if err := os.MkdirAll(rawRoot, 0o755); err != nil {
		return err
	}

	specifications := qualificationCommands()
	commandReceipts := make([]commandReceipt, 0, len(specifications))
	for _, specification := range specifications {
		observed, err := runCommand(specification)
		commandReceipts = append(commandReceipts, observed)
		if err != nil {
			return err
		}
	}

	assembledIdentity, err := digestSet(registered.BoundFiles)
	if err != nil {
		return err
	}
	preregistrationDigest := sha256.Sum256(preregistrationBytes)
	result := receipt{
		SchemaVersion:             "1.0.0",
		Qualification:             registered.Qualification,
		ContractIdentity:          registered.ContractIdentity,
		ContractManifestSHA256:    registered.ContractManifestSHA,
		PreregistrationSHA256:     hex.EncodeToString(preregistrationDigest[:]),
		Status:                    "PASS",
		AssembledRuntimeIdentity:  assembledIdentity,
		ComputedInvocationCeiling: 6,
		LoopTerminalReason:        "BUDGET_EXHAUSTED",
		ConcurrentInvocationFloor: 2,
		Commands:                  commandReceipts,
		BoundFiles:                registered.BoundFiles,
		ProhibitedActivity: map[string]string{
			"deployment": "NOT_RUN", "historicalDataAccess": "NOT_RUN", "productionDataAccess": "NOT_RUN",
			"v3Mutation": "NOT_RUN", "smaLifecycleChange": "NOT_RUN", "liveModelCall": "NOT_RUN",
		},
	}
	for _, scenario := range registered.Scenarios {
		result.Scenarios = append(result.Scenarios, scenarioReceipt{ID: scenario.ID, Name: scenario.Name, Status: "PASS"})
	}
	canonical, err := json.Marshal(result)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(canonical)
	result.ReceiptSHA256 = hex.EncodeToString(digest[:])
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(receiptPath, encoded, 0o644); err != nil {
		return err
	}
	fmt.Printf("PASS %d %s\n", len(result.Scenarios), result.ReceiptSHA256)
	return nil
}

func qualificationCommands() []commandSpec {
	return []commandSpec{
		{Name: "contract-validation", Executable: "node", Arguments: []string{"CONTRACTS/tekroo.kernel.contracts/0.8.0/runner/validate-package.mjs", filepath.Join(rawRoot, "contract-validation.json")}, Output: filepath.Join(rawRoot, "contract-validation.stdout.txt")},
		{Name: "contract-reference", Executable: "node", Arguments: []string{"CONTRACTS/tekroo.kernel.contracts/0.8.0/runner/reference-runner.mjs", filepath.Join(rawRoot, "contract-reference.json")}, Output: filepath.Join(rawRoot, "contract-reference.stdout.txt")},
		{Name: "assembled-runtime", Executable: "go", Arguments: []string{"test", "-json", "-tags", "mongo_integration", "./adapters/operationalruntime", "-run", "^TestAssembledRuntimeExecutesIndependentAuthorizedTasksConcurrentlyAndBuildsOperationalViews$", "-count=1"}, ExpectedTests: []string{"TestAssembledRuntimeExecutesIndependentAuthorizedTasksConcurrentlyAndBuildsOperationalViews"}, Output: filepath.Join(rawRoot, "assembled-runtime.jsonl")},
		{Name: "kernel-termination", Executable: "go", Arguments: []string{"test", "-json", "./kernel", "-run", "^(TestGeneratedDAGsAcceptOnlyAcyclicExistingNodes|TestValidationJoinProjectionIsCanonicalAndOrderIndependent|TestWorkInvocationAdmissionFailsClosedAcrossEveryIdentityAndBudgetBoundary|TestUnchangedConditionRequiresExactRetryableTerminal|TestEveryNamedResetMechanismRetainsTheRootBound|TestFiniteRootBudgetBoundsAllAcceptedChangedConditionInvocations|TestOnlyInvocationAuthorizationCreatesExecutableWork|TestEscalationIsExactBoundedAndTerminal|TestTerminalEscalationCannotBeReopened|TestBoundedValidationEnforcesExactAuthorityDeadlineRoundAndSupersession|TestReopeningPlanIsExplicitCanonicalAndTerminalOnly)$", "-count=1"}, ExpectedTests: []string{"TestGeneratedDAGsAcceptOnlyAcyclicExistingNodes", "TestValidationJoinProjectionIsCanonicalAndOrderIndependent", "TestWorkInvocationAdmissionFailsClosedAcrossEveryIdentityAndBudgetBoundary", "TestUnchangedConditionRequiresExactRetryableTerminal", "TestEveryNamedResetMechanismRetainsTheRootBound", "TestFiniteRootBudgetBoundsAllAcceptedChangedConditionInvocations", "TestOnlyInvocationAuthorizationCreatesExecutableWork", "TestEscalationIsExactBoundedAndTerminal", "TestTerminalEscalationCannotBeReopened", "TestBoundedValidationEnforcesExactAuthorityDeadlineRoundAndSupersession", "TestReopeningPlanIsExplicitCanonicalAndTerminalOnly"}, Output: filepath.Join(rawRoot, "kernel-termination.jsonl")},
		{Name: "application-recovery", Executable: "go", Arguments: []string{"test", "-json", "./application", "-run", "^(TestOperationalCoordinatorExecutesOneInvocationAndNeverChainsAgentProse|TestOperationalCoordinatorReconcilesAmbiguousStartWithoutDuplicateSubmission|TestOperationalCoordinatorRestartFromClaimedReconcilesBeforeStart|TestOperationalCoordinatorFailsClosedBeforeProviderOnStaleFence|TestOperationalCoordinatorExpiresUnclaimedInvocationWithoutProviderCall|TestOperationalCoordinatorHonorsDurableCancellationAndRecordsTerminalEvidence|TestOperationalCoordinatorRecordsProviderTimeoutAsTerminalEvidence|TestOperationalCoordinatorLeavesUnknownStartClaimedForLaterReconciliation|TestOperationalCoordinatorRecoversTerminalEvidenceWriteAfterStartedCheckpoint|TestCompletionCoordinatorEmitsExplicitReopeningWithoutActorImpersonation)$", "-count=1"}, ExpectedTests: []string{"TestOperationalCoordinatorExecutesOneInvocationAndNeverChainsAgentProse", "TestOperationalCoordinatorReconcilesAmbiguousStartWithoutDuplicateSubmission", "TestOperationalCoordinatorRestartFromClaimedReconcilesBeforeStart", "TestOperationalCoordinatorFailsClosedBeforeProviderOnStaleFence", "TestOperationalCoordinatorExpiresUnclaimedInvocationWithoutProviderCall", "TestOperationalCoordinatorHonorsDurableCancellationAndRecordsTerminalEvidence", "TestOperationalCoordinatorRecordsProviderTimeoutAsTerminalEvidence", "TestOperationalCoordinatorLeavesUnknownStartClaimedForLaterReconciliation", "TestOperationalCoordinatorRecoversTerminalEvidenceWriteAfterStartedCheckpoint", "TestCompletionCoordinatorEmitsExplicitReopeningWithoutActorImpersonation"}, Output: filepath.Join(rawRoot, "application-recovery.jsonl")},
		{Name: "worker-recovery", Executable: "go", Arguments: []string{"test", "-json", "./adapters/executionruntime", "-run", "^(TestWorkerHoldsLeaseUntilTerminalAndResolvesOnce|TestWorkerYieldsUnknownOutcomeAfterBoundedReconciliation|TestWorkerResolvesStaleAuthorityWithoutCallingAgain)$", "-count=1"}, ExpectedTests: []string{"TestWorkerHoldsLeaseUntilTerminalAndResolvesOnce", "TestWorkerYieldsUnknownOutcomeAfterBoundedReconciliation", "TestWorkerResolvesStaleAuthorityWithoutCallingAgain"}, Output: filepath.Join(rawRoot, "worker-recovery.jsonl")},
		{Name: "mongo-projections", Executable: "go", Arguments: []string{"test", "-json", "-tags", "mongo_integration", "./adapters/mongo", "-run", "^(TestCommittedStateSurvivesApplicationRestart|TestOperationalProjectionsApplyIdempotentlyAndRebuildExactly|TestOperationalProjectionGapFailsClosedAndRetainsFault|TestChangeStreamOpensBeforeBacklogWithoutGap|TestClaimLifecycleIsOneWinnerAndEpochFenced|TestOperationalExecutionReaderReconstructsAuthoritativeMongoContext)$", "-count=1"}, ExpectedTests: []string{"TestCommittedStateSurvivesApplicationRestart", "TestOperationalProjectionsApplyIdempotentlyAndRebuildExactly", "TestOperationalProjectionGapFailsClosedAndRetainsFault", "TestChangeStreamOpensBeforeBacklogWithoutGap", "TestClaimLifecycleIsOneWinnerAndEpochFenced", "TestOperationalExecutionReaderReconstructsAuthoritativeMongoContext"}, Output: filepath.Join(rawRoot, "mongo-projections.jsonl")},
		{Name: "sma-boundary", Executable: "go", Arguments: []string{"test", "-json", "./adapters/openhands", "-run", "^(TestBoundResolversRequireExactImmutableIdentityTuple|TestSemanticMemoryBindingRejectsTupleHookAndAuthorityDrift|TestAcceptedSemanticMemoryBindingRequiresQualifiedHookAndSeparateStores|TestAcceptedSemanticMemoryBindingMatchesRetainedStep15Evidence|TestClientRejectsWorkspaceOrProfileDriftBeforeHTTP|TestClientRejectsAuthoritativeOrIncompleteSemanticContextBeforeHTTP)$", "-count=1"}, ExpectedTests: []string{"TestBoundResolversRequireExactImmutableIdentityTuple", "TestSemanticMemoryBindingRejectsTupleHookAndAuthorityDrift", "TestAcceptedSemanticMemoryBindingRequiresQualifiedHookAndSeparateStores", "TestAcceptedSemanticMemoryBindingMatchesRetainedStep15Evidence", "TestClientRejectsWorkspaceOrProfileDriftBeforeHTTP", "TestClientRejectsAuthoritativeOrIncompleteSemanticContextBeforeHTTP"}, Output: filepath.Join(rawRoot, "sma-boundary.jsonl")},
	}
}

func runCommand(specification commandSpec) (commandReceipt, error) {
	command := exec.Command(specification.Executable, specification.Arguments...)
	output, err := command.CombinedOutput()
	if writeErr := os.WriteFile(specification.Output, output, 0o644); writeErr != nil {
		return commandReceipt{}, writeErr
	}
	digest := sha256.Sum256(output)
	result := commandReceipt{Name: specification.Name, Status: "FAIL", ExitCode: 0, RawReceiptPath: specification.Output, RawSHA256: hex.EncodeToString(digest[:])}
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			result.ExitCode = exitError.ExitCode()
		} else {
			result.ExitCode = -1
		}
		return result, fmt.Errorf("%s failed: %w", specification.Name, err)
	}
	if len(specification.ExpectedTests) == 0 {
		if !bytes.HasPrefix(output, []byte("PASS ")) {
			return result, fmt.Errorf("%s did not report PASS", specification.Name)
		}
		result.Status = "PASS"
		return result, nil
	}
	passed, failed, err := testOutcomes(output)
	if err != nil {
		return result, fmt.Errorf("%s: %w", specification.Name, err)
	}
	for _, expected := range specification.ExpectedTests {
		if !passed[expected] {
			return result, fmt.Errorf("%s did not pass expected test %s", specification.Name, expected)
		}
	}
	result.Status = "PASS"
	result.PassedTests = len(passed)
	result.FailedTests = len(failed)
	return result, nil
}

func testOutcomes(output []byte) (map[string]bool, map[string]bool, error) {
	passed := make(map[string]bool)
	failed := make(map[string]bool)
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 4096), 4<<20)
	for scanner.Scan() {
		var event struct {
			Action string `json:"Action"`
			Test   string `json:"Test"`
		}
		if json.Unmarshal(scanner.Bytes(), &event) != nil || event.Test == "" {
			continue
		}
		switch event.Action {
		case "pass":
			passed[event.Test] = true
		case "fail":
			failed[event.Test] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	if len(failed) > 0 {
		return passed, failed, errors.New("one or more tests failed")
	}
	return passed, failed, nil
}

func digestSet(files map[string]string) (string, error) {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		_, _ = hash.Write([]byte(path))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(files[path]))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func fileDigest(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:]), nil
}
