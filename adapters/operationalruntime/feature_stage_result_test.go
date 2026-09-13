package operationalruntime

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestInvalidPlanningOutputBlockMatchingIsExact(t *testing.T) {
	task := organization.PlannedTask{ID: "00000000-0000-7000-8000-000000000101"}
	invocation := kernel.WorkInvocation{
		ID:          "00000000-0000-7000-8000-000000000102",
		ActorFQN:    "teams::architect-1",
		Execution:   kernel.ExecutionTuple{ExecutionID: "00000000-0000-7000-8000-000000000103", FencingEpoch: 1},
		LastEventID: "00000000-0000-7000-8000-000000000104",
	}
	authority := kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "teams-admission-policy"}
	payload, err := json.Marshal(planningOutputBlockPayload{
		BlockerRefs:  []string{"teams://work-invocation/" + string(invocation.ID)},
		Reason:       invalidPlanningOutputReason,
		ReviewPolicy: invalidPlanningReviewPolicy,
	})
	if err != nil {
		t.Fatal(err)
	}
	actor := invocation.ActorFQN
	execution := invocation.Execution
	event := kernel.DomainEvent{
		EventID:   "00000000-0000-7000-8000-000000000105",
		EventType: "tekroo.event.work.blocked",
		Aggregate: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID},
		Authority: authority,
		ActorFQN:  &actor,
		Execution: &execution,
		Parents:   []kernel.DagParent{{ParentEventID: invocation.LastEventID, EdgeKind: kernel.EdgeResponse}},
		Payload:   payload,
	}
	if !isExactInvalidPlanningOutputBlock(event, task, invocation, authority) {
		t.Fatal("exact system planning-output block was not recognized")
	}

	wrongParent := event
	wrongParent.Parents = []kernel.DagParent{{ParentEventID: invocation.LastEventID, EdgeKind: kernel.EdgeCausal}}
	if isExactInvalidPlanningOutputBlock(wrongParent, task, invocation, authority) {
		t.Fatal("block with a different causal relationship was accepted")
	}
	wrongReason := event
	wrongReason.Payload = []byte(`{"blocker_refs":["teams://work-invocation/00000000-0000-7000-8000-000000000102"],"reason":"human requested pause","review_policy":"operator-or-product-owner-must-amend-scope-or-cancel"}`)
	if isExactInvalidPlanningOutputBlock(wrongReason, task, invocation, authority) {
		t.Fatal("unrelated task block was accepted for automatic recovery")
	}
}

func TestInvalidValidatorOutputBlockMatchingIsExact(t *testing.T) {
	task := organization.PlannedTask{ID: "00000000-0000-7000-8000-000000000111"}
	output := repeatedDigest('a')
	invocation := recoveryTerminalFixture(kernel.InvocationSucceeded, nil, nil)
	invocation.TaskID = task.ID
	invocation.Purpose = kernel.PurposeValidation
	invocation.AttemptFamily = "validation"
	invocation.OutputDigest = &output
	authority := kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "teams-admission-policy"}
	payload, err := json.Marshal(planningOutputBlockPayload{
		BlockerRefs:  []string{"teams://work-invocation/" + string(invocation.ID)},
		Reason:       invalidStructuredOutputReason,
		ReviewPolicy: invalidStructuredReviewPolicy,
	})
	if err != nil {
		t.Fatal(err)
	}
	actor := invocation.ActorFQN
	execution := invocation.Execution
	event := kernel.DomainEvent{
		EventID:   "00000000-0000-7000-8000-000000000112",
		EventType: "tekroo.event.work.blocked",
		Aggregate: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID},
		Authority: authority,
		ActorFQN:  &actor,
		Execution: &execution,
		Parents:   []kernel.DagParent{{ParentEventID: invocation.LastEventID, EdgeKind: kernel.EdgeResponse}},
		Payload:   payload,
	}
	if !isExactInvalidStructuredOutputBlock(event, task, invocation, authority) {
		t.Fatal("exact invalid-validator-output block was not recognized")
	}
	if !isExactRecoverableStructuredDecisionBlock(event, task, invocation, authority) {
		t.Fatal("exact structured-decision block was not recognized")
	}
	failedDecision := event
	failedDecision.Payload = []byte(`{"blocker_refs":["teams://work-invocation/00000000-0000-7000-8000-000000000321"],"reason":"whole-feature validation did not pass: material interface mismatch","review_policy":"operator-or-product-owner-must-amend-scope-or-cancel"}`)
	if !isExactRecoverableStructuredDecisionBlock(failedDecision, task, invocation, authority) {
		t.Fatal("material validator failure was not recognized as operator-recoverable")
	}
	if isExactInvalidStructuredOutputBlock(failedDecision, task, invocation, authority) {
		t.Fatal("material validator failure was misclassified as invalid structured output")
	}

	wrongInvocation := invocation
	wrongInvocation.ID = "00000000-0000-7000-8000-000000000113"
	if isExactInvalidStructuredOutputBlock(event, task, wrongInvocation, authority) {
		t.Fatal("block for another invocation was accepted")
	}
	wrongReason := event
	wrongReason.Payload = []byte(`{"blocker_refs":["teams://work-invocation/00000000-0000-7000-8000-000000000321"],"reason":"human requested pause","review_policy":"operator-or-product-owner-must-amend-scope-or-cancel"}`)
	if isExactInvalidStructuredOutputBlock(wrongReason, task, invocation, authority) {
		t.Fatal("unrelated task block was accepted for validator recovery")
	}
	if isExactRecoverableStructuredDecisionBlock(wrongReason, task, invocation, authority) {
		t.Fatal("unrelated task block was accepted as a structured decision")
	}
}

func TestFeatureStageResultsAreStrictAndBounded(t *testing.T) {
	refinement := []byte(application.OrganizationalResultMarker + "\n{\"schema_version\":\"1.0.0\",\"result_type\":\"FEATURE_REFINEMENT\",\"acceptance_criteria_disposition\":\"PRESERVE_SUBMITTED\",\"clarification_questions\":[],\"priority\":\"HIGH\"}")
	if _, err := parseRefinementStageResult(refinement); err != nil {
		t.Fatal(err)
	}
	duplicatedCriteria := []byte(application.OrganizationalResultMarker + "\n{\"schema_version\":\"1.0.0\",\"result_type\":\"FEATURE_REFINEMENT\",\"acceptance_criteria_disposition\":\"PRESERVE_SUBMITTED\",\"acceptance_criteria\":[\"works\"],\"clarification_questions\":[],\"priority\":\"HIGH\"}")
	if _, err := parseRefinementStageResult(duplicatedCriteria); err == nil {
		t.Fatal("refinement accepted duplicated authoritative acceptance criteria")
	}
	specification := []byte(application.OrganizationalResultMarker + "\n{\"schema_version\":\"1.0.0\",\"result_type\":\"FEATURE_SPECIFICATION\",\"stories\":[{\"title\":\"Story\",\"description\":\"Deliver it.\",\"acceptance_criteria\":[\"works\"],\"priority\":\"HIGH\"}],\"design_constraints\":[]}")
	parsedSpecification, err := parseSpecificationStageResult(specification)
	if err != nil {
		t.Fatal(err)
	}
	if !specificationPreservesSubmittedCriteria([]string{"works"}, parsedSpecification) || specificationPreservesSubmittedCriteria([]string{"missing"}, parsedSpecification) {
		t.Fatal("specification acceptance-criteria preservation check is incorrect")
	}
	if _, err := parseSpecificationStageResult(append(append([]byte(nil), specification...), '}')); err != nil {
		t.Fatalf("one redundant terminal brace was not normalized: %v", err)
	}
	if _, err := parseSpecificationStageResult(append(append([]byte(nil), specification...), []byte("}}")...)); err == nil {
		t.Fatal("more than one redundant terminal brace was accepted")
	}
	plan := []byte(application.OrganizationalResultMarker + "\n{\"schema_version\":\"1.0.0\",\"result_type\":\"FEATURE_PLAN\",\"architecture\":\"bounded\",\"design_decisions\":[],\"assumptions\":[],\"tasks\":[{\"story_index\":0,\"title\":\"Implement\",\"description\":\"Implement it.\",\"acceptance_criteria\":[\"works\"],\"covers\":[0],\"depends_on\":[],\"validates\":[],\"purpose\":\"IMPLEMENTATION\",\"complexity\":3,\"risk\":\"LOW\",\"critical_path\":true,\"attempt_limit\":2,\"review_round_limit\":2}]}")
	if _, err := parseArchitectureStageResult(plan); err != nil {
		t.Fatal(err)
	}
	paragraphPlan := []byte(strings.Replace(string(plan), `"architecture":"bounded"`, `"architecture":["bounded","second paragraph"]`, 1))
	parsedParagraphPlan, err := parseArchitectureStageResult(paragraphPlan)
	if err != nil {
		t.Fatalf("architecture paragraph array was rejected: %v", err)
	}
	if string(parsedParagraphPlan.Architecture) != "bounded\nsecond paragraph" {
		t.Fatalf("architecture paragraph normalization = %q", parsedParagraphPlan.Architecture)
	}
	mediumRisk := []byte(strings.Replace(string(plan), `"risk":"LOW"`, `"risk":"MEDIUM"`, 1))
	parsedMediumRisk, err := parseArchitectureStageResult(mediumRisk)
	if err != nil {
		t.Fatalf("conventional MEDIUM risk synonym was rejected: %v", err)
	}
	if parsedMediumRisk.Tasks[0].Risk != organization.RiskModerate {
		t.Fatalf("MEDIUM risk normalized to %q, want %q", parsedMediumRisk.Tasks[0].Risk, organization.RiskModerate)
	}
	for input, want := range map[string]organization.RiskLevel{
		"low":      organization.RiskLow,
		"moderate": organization.RiskModerate,
		"high":     organization.RiskHigh,
		"critical": organization.RiskCritical,
		"medium":   organization.RiskModerate,
	} {
		lowercaseRisk := []byte(strings.Replace(string(plan), `"risk":"LOW"`, `"risk":"`+input+`"`, 1))
		parsedLowercaseRisk, err := parseArchitectureStageResult(lowercaseRisk)
		if err != nil {
			t.Fatalf("recognized lowercase risk %q was rejected: %v", input, err)
		}
		if parsedLowercaseRisk.Tasks[0].Risk != want {
			t.Fatalf("risk %q normalized to %q, want %q", input, parsedLowercaseRisk.Tasks[0].Risk, want)
		}
	}
	unknownRisk := []byte(strings.Replace(string(plan), `"risk":"LOW"`, `"risk":"SEVERE"`, 1))
	if _, err := parseArchitectureStageResult(unknownRisk); err == nil {
		t.Fatal("unknown risk value was accepted")
	}
	invalid := append([]byte(nil), plan...)
	invalid = append(invalid, []byte("{}")...)
	if _, err := parseArchitectureStageResult(invalid); err == nil {
		t.Fatal("trailing JSON was accepted")
	}
	reviewDigest := repeatedDigest('a')
	review := []byte(application.OrganizationalResultMarker + "\n{\"schema_version\":\"1.0.0\",\"result_type\":\"FEATURE_PLAN_REVIEW\",\"outcome\":\"PASS\",\"reviewed_plan_sha256\":\"" + string(reviewDigest) + "\",\"coverage\":{\"story_count\":1,\"story_acceptance_criterion_counts\":[1],\"task_count\":1,\"task_acceptance_criterion_counts\":[1],\"plan_check_subjects\":[\"STATE_OWNERSHIP\",\"CONCURRENCY_ATOMICITY\",\"DEPENDENCY_DIRECTION\",\"INTERFACE_COMPATIBILITY\",\"AUTHORIZATION_IDENTITY\",\"PARTIAL_FAILURE\"]},\"findings\":[],\"unverified_claims\":[],\"reasons\":[\"the plan is implementable\"],\"evidence\":[\"organization/feature.go\"]}")
	parsedReview, err := parseArchitectureReviewStageResult(review)
	if err != nil || parsedReview.ReviewedPlanDigest != reviewDigest || parsedReview.Outcome != "PASS" {
		t.Fatalf("architecture review result = %#v err=%v", parsedReview, err)
	}
	unknownReviewField := []byte(strings.Replace(string(review), `"outcome":"PASS"`, `"outcome":"PASS","verdict":"PASS"`, 1))
	if _, err := parseArchitectureReviewStageResult(unknownReviewField); err == nil {
		t.Fatal("architecture review accepted an unknown field")
	}
	missingFindings := []byte(strings.Replace(string(review), `,"findings":[]`, "", 1))
	if _, err := parseArchitectureReviewStageResult(missingFindings); err == nil {
		t.Fatal("architecture review accepted a missing findings array")
	}
	missingReviewEvidence := []byte(strings.Replace(string(review), `"evidence":["organization/feature.go"]`, `"evidence":[]`, 1))
	if _, err := parseArchitectureReviewStageResult(missingReviewEvidence); err == nil {
		t.Fatal("architecture review accepted no inspected evidence")
	}
	scalarReviewReason := []byte(strings.Replace(string(review), `"reasons":["the plan is implementable"]`, `"reasons":"the plan is implementable"`, 1))
	if parsedScalarReview, err := parseArchitectureReviewStageResult(scalarReviewReason); err != nil || len(parsedScalarReview.Reasons) != 1 {
		t.Fatalf("architecture review scalar reason was not normalized: result=%+v err=%v", parsedScalarReview, err)
	}
	taskDigest := repeatedDigest('b')
	taskReview := []byte(application.OrganizationalResultMarker + "\n{\"schema_version\":\"1.0.0\",\"result_type\":\"FEATURE_PLAN_TASK_REVIEW\",\"outcome\":\"PASS\",\"reviewed_plan_sha256\":\"" + string(reviewDigest) + "\",\"reviewed_task_index\":2,\"reviewed_task_sha256\":\"" + string(taskDigest) + "\",\"reviewed_dependency_indexes\":[],\"description_outcome\":\"SUPPORTED\",\"description_requires_task_change\":false,\"acceptance_criterion_checks\":[{\"criterion_index\":0,\"outcome\":\"SUPPORTED\",\"requires_task_change\":false,\"reasons\":[\"the criterion is supported\"],\"evidence\":[\"adapters/mongo/store.go\"]}],\"verified_operations\":[\"insert enforces the declared unique key\"],\"unverified_prescriptions\":[],\"reasons\":[\"the exact transition is supported\"],\"evidence\":[\"adapters/mongo/store.go\"]}")
	parsedTaskReview, err := parseArchitectureTaskReviewStageResult(taskReview)
	if err != nil || parsedTaskReview.ReviewedPlanDigest != reviewDigest || parsedTaskReview.ReviewedTaskIndex != 2 || parsedTaskReview.ReviewedTaskDigest != taskDigest {
		t.Fatalf("architecture task review result = %#v err=%v", parsedTaskReview, err)
	}
	missingVerifiedOperations := []byte(strings.Replace(string(taskReview), `"verified_operations":["insert enforces the declared unique key"]`, `"verified_operations":[]`, 1))
	if _, err := parseArchitectureTaskReviewStageResult(missingVerifiedOperations); err == nil {
		t.Fatal("architecture task review accepted no verified operations")
	}
	missingCriterionChecks := []byte(strings.Replace(string(taskReview), `"acceptance_criterion_checks":[{"criterion_index":0,"outcome":"SUPPORTED","requires_task_change":false,"reasons":["the criterion is supported"],"evidence":["adapters/mongo/store.go"]}]`, `"acceptance_criterion_checks":[]`, 1))
	if _, err := parseArchitectureTaskReviewStageResult(missingCriterionChecks); err == nil {
		t.Fatal("architecture task review accepted no criterion checks")
	}
	missingDependencies := []byte(strings.Replace(string(taskReview), `,"reviewed_dependency_indexes":[]`, "", 1))
	if _, err := parseArchitectureTaskReviewStageResult(missingDependencies); err == nil {
		t.Fatal("architecture task review accepted a missing dependency basis")
	}
	scalarExplanations := []byte(strings.ReplaceAll(string(taskReview), `"reasons":["the criterion is supported"]`, `"reasons":"the criterion is supported"`))
	scalarExplanations = []byte(strings.ReplaceAll(string(scalarExplanations), `"evidence":["adapters/mongo/store.go"]`, `"evidence":"adapters/mongo/store.go"`))
	scalarExplanations = []byte(strings.ReplaceAll(string(scalarExplanations), `"reasons":["the exact transition is supported"]`, `"reasons":"the exact transition is supported"`))
	parsedScalar, err := parseArchitectureTaskReviewStageResult(scalarExplanations)
	if err != nil || len(parsedScalar.Reasons) != 1 || len(parsedScalar.Evidence) != 1 || len(parsedScalar.CriterionChecks[0].Reasons) != 1 || len(parsedScalar.CriterionChecks[0].Evidence) != 1 {
		t.Fatalf("scalar review explanations were not normalized: result=%+v err=%v", parsedScalar, err)
	}
}

func TestArchitectureTaskReviewCoverageBindsEveryCriterionAndRejectsSubstitution(t *testing.T) {
	task := architectureTaskResult{
		Description:        "Perform the operation exactly as specified.",
		AcceptanceCriteria: []string{"the first invariant holds", "the second invariant holds"},
	}
	checks := make([]architectureTaskCriterionReview, len(task.AcceptanceCriteria))
	for index := range task.AcceptanceCriteria {
		checks[index] = architectureTaskCriterionReview{CriterionIndex: uint32(index), Outcome: "SUPPORTED", Reasons: []string{"supported"}, Evidence: []string{"source.go"}}
	}
	result := architectureTaskReviewStageResult{
		Outcome: "PASS", ReviewedDependencyIndexes: []uint32{}, DescriptionOutcome: "SUPPORTED",
		CriterionChecks: checks, VerifiedOperations: []string{"operation"}, Reasons: []string{"supported"}, Evidence: []string{"source.go"},
	}
	if !validArchitectureTaskReviewCoverage(result, []architectureTaskResult{task}, 0) {
		t.Fatal("complete exact review coverage was rejected")
	}
	substitution := result
	substitution.DescriptionRequiresTaskChange = true
	if validArchitectureTaskReviewCoverage(substitution, []architectureTaskResult{task}, 0) {
		t.Fatal("PASS accepted a required task substitution")
	}
	missing := result
	missing.CriterionChecks = missing.CriterionChecks[:1]
	if validArchitectureTaskReviewCoverage(missing, []architectureTaskResult{task}, 0) {
		t.Fatal("PASS accepted incomplete criterion coverage")
	}
	duplicate := result
	duplicate.CriterionChecks = append([]architectureTaskCriterionReview(nil), result.CriterionChecks...)
	duplicate.CriterionChecks[1] = duplicate.CriterionChecks[0]
	if validArchitectureTaskReviewCoverage(duplicate, []architectureTaskResult{task}, 0) {
		t.Fatal("PASS accepted duplicate criterion coverage")
	}
	unverified := result
	unverified.UnverifiedPrescriptions = []string{"external behavior is not proven"}
	if validArchitectureTaskReviewCoverage(unverified, []architectureTaskResult{task}, 0) {
		t.Fatal("PASS accepted an unverified prescription")
	}
	failure := substitution
	failure.Outcome = "FAIL"
	if !validArchitectureTaskReviewCoverage(failure, []architectureTaskResult{task}, 0) {
		t.Fatal("FAIL with a required task change was rejected")
	}
	decisiveFailure := result
	decisiveFailure.Outcome = "FAIL"
	decisiveFailure.CriterionChecks = []architectureTaskCriterionReview{
		{CriterionIndex: 1, Outcome: "UNSUPPORTED", RequiresTaskChange: true, Reasons: []string{"the prescribed transition is not implementable"}, Evidence: []string{"source.go"}},
		{CriterionIndex: 2, Outcome: "SUPPORTED", Reasons: []string{"surplus review detail"}, Evidence: []string{"source.go"}},
	}
	if !validArchitectureTaskReviewCoverage(decisiveFailure, []architectureTaskResult{task}, 0) {
		t.Fatal("identity-bound FAIL with one decisive in-range finding was rejected because of surplus coverage")
	}
	unsupportedOnlyOutOfRange := decisiveFailure
	unsupportedOnlyOutOfRange.DescriptionOutcome = "SUPPORTED"
	unsupportedOnlyOutOfRange.DescriptionRequiresTaskChange = false
	unsupportedOnlyOutOfRange.CriterionChecks = []architectureTaskCriterionReview{
		{CriterionIndex: 2, Outcome: "UNSUPPORTED", RequiresTaskChange: true, Reasons: []string{"not a reviewed criterion"}, Evidence: []string{"source.go"}},
	}
	if validArchitectureTaskReviewCoverage(unsupportedOnlyOutOfRange, []architectureTaskResult{task}, 0) {
		t.Fatal("FAIL accepted an unsupported finding only outside the reviewed task")
	}
}

func TestCompletePlanReviewCoverageBindsEveryTaskAndMaterialPlanCondition(t *testing.T) {
	stories := []organization.PlannedStory{{Title: "Capability", Description: "Deliver the capability.", AcceptanceCriteria: []string{"the capability works"}}}
	tasks := []architectureTaskResult{{Title: "State owner", AcceptanceCriteria: []string{"owns state"}}, {Title: "Consumer", AcceptanceCriteria: []string{"uses owner", "preserves invariant"}}}
	coverage := architectureReviewCoverage{
		StoryCount: 1, StoryAcceptanceCriteriaCount: []uint32{1},
		TaskCount: 2, TaskAcceptanceCriteriaCount: []uint32{1, 2},
		PlanCheckSubjects: append([]string(nil), requiredArchitecturePlanCheckSubjects...),
	}
	result := architectureReviewStageResult{Outcome: "PASS", Coverage: coverage, Findings: []architecturePlanFinding{}, UnverifiedClaims: stageStringList{}, Reasons: []string{"supported"}, Evidence: []string{"source.go"}}
	if !validArchitectureReviewCoverage(result, stories, tasks) {
		t.Fatal("complete supported review coverage was rejected")
	}
	missingStoryCriterion := result
	missingStoryCriterion.Coverage.StoryAcceptanceCriteriaCount = []uint32{0}
	if validArchitectureReviewCoverage(missingStoryCriterion, stories, tasks) {
		t.Fatal("PASS accepted missing source-story criterion coverage")
	}
	missingTask := result
	missingTask.Coverage.TaskCount = 1
	if validArchitectureReviewCoverage(missingTask, stories, tasks) {
		t.Fatal("PASS accepted a missing task count")
	}
	missingCriterion := result
	missingCriterion.Coverage.TaskAcceptanceCriteriaCount = []uint32{1, 1}
	if validArchitectureReviewCoverage(missingCriterion, stories, tasks) {
		t.Fatal("PASS accepted missing acceptance-criterion coverage")
	}
	missingSubject := result
	missingSubject.Coverage.PlanCheckSubjects = append([]string(nil), result.Coverage.PlanCheckSubjects...)
	missingSubject.Coverage.PlanCheckSubjects[len(missingSubject.Coverage.PlanCheckSubjects)-1] = requiredArchitecturePlanCheckSubjects[0]
	if validArchitectureReviewCoverage(missingSubject, stories, tasks) {
		t.Fatal("PASS accepted a missing material plan condition")
	}
	taskIndex, criterionIndex := uint32(1), uint32(1)
	finding := architecturePlanFinding{Subject: "TASK_ACCEPTANCE_CRITERION", TaskIndex: &taskIndex, CriterionIndex: &criterionIndex, Reasons: []string{"unsupported"}, Evidence: []string{"consumer.go"}}
	withFinding := result
	withFinding.Findings = []architecturePlanFinding{finding}
	if validArchitectureReviewCoverage(withFinding, stories, tasks) {
		t.Fatal("PASS accepted a material finding")
	}
	unverified := result
	unverified.UnverifiedClaims = []string{"concurrency claim"}
	if validArchitectureReviewCoverage(unverified, stories, tasks) {
		t.Fatal("PASS accepted an unverified material claim")
	}
	failure := withFinding
	failure.Outcome = "FAIL"
	if !validArchitectureReviewCoverage(failure, stories, tasks) {
		t.Fatal("FAIL with a material finding was rejected")
	}
	outOfRange := failure
	badTaskIndex := uint32(2)
	outOfRange.Findings = []architecturePlanFinding{{Subject: "TASK_DESCRIPTION", TaskIndex: &badTaskIndex, Reasons: []string{"unsupported"}, Evidence: []string{"consumer.go"}}}
	if validArchitectureReviewCoverage(outOfRange, stories, tasks) {
		t.Fatal("finding for an out-of-range task was accepted")
	}
	duplicate := failure
	duplicate.Findings = []architecturePlanFinding{finding, finding}
	if validArchitectureReviewCoverage(duplicate, stories, tasks) {
		t.Fatal("duplicate material finding was accepted")
	}
}

func TestArchitectureTaskReviewUsesExactTransitiveDependencyContracts(t *testing.T) {
	tasks := []architectureTaskResult{
		{Title: "Domain", Description: "Define the domain interface.", AcceptanceCriteria: []string{"the interface exists"}},
		{Title: "Storage", Description: "Implement the interface.", AcceptanceCriteria: []string{"the adapter persists state"}, DependsOn: []uint32{0}},
		{Title: "API", Description: "Expose the adapter.", AcceptanceCriteria: []string{"the API uses the adapter"}, DependsOn: []uint32{1}},
	}
	contracts, err := architectureTaskDependencyContracts(tasks, 2)
	if err != nil || len(contracts) != 2 || contracts[0].TaskIndex != 0 || contracts[1].TaskIndex != 1 {
		t.Fatalf("dependency contracts = %#v err=%v", contracts, err)
	}
	result := architectureTaskReviewStageResult{
		Outcome: "PASS", ReviewedDependencyIndexes: []uint32{contracts[0].TaskIndex, contracts[1].TaskIndex},
		DescriptionOutcome: "SUPPORTED",
		CriterionChecks:    []architectureTaskCriterionReview{{CriterionIndex: 0, Outcome: "SUPPORTED", Reasons: []string{"supported"}, Evidence: []string{"source.go"}}},
		VerifiedOperations: []string{"operation"}, Reasons: []string{"supported"}, Evidence: []string{"source.go"},
	}
	if !validArchitectureTaskReviewCoverage(result, tasks, 2) {
		t.Fatal("exact transitive dependency basis was rejected")
	}
	result.ReviewedDependencyIndexes[0], result.ReviewedDependencyIndexes[1] = result.ReviewedDependencyIndexes[1], result.ReviewedDependencyIndexes[0]
	if validArchitectureTaskReviewCoverage(result, tasks, 2) {
		t.Fatal("reordered dependency basis was accepted")
	}
}

func TestRequiredMaterializedTaskCountSelectsReviewWorkByRisk(t *testing.T) {
	tasks := []architectureTaskResult{
		{Purpose: kernel.PurposeImplementation, Risk: organization.RiskModerate},
		{Purpose: kernel.PurposeImplementation, Risk: organization.RiskHigh},
		{Purpose: kernel.PurposeInvestigation, Risk: organization.RiskLow},
	}
	// Three authored tasks, one high-risk task review, one high-risk security
	// review, one joined feature validator, and one final acceptance task.
	if got := requiredMaterializedTaskCount(tasks); got != 7 {
		t.Fatalf("materialized task count = %d, want 7", got)
	}
}

func TestPreAssignmentPlanningResultsRejectOperationalIdentity(t *testing.T) {
	refinement := []byte(application.OrganizationalResultMarker + "\n{\"schema_version\":\"1.0.0\",\"result_type\":\"FEATURE_REFINEMENT\",\"acceptance_criteria_disposition\":\"PRESERVE_SUBMITTED\",\"clarification_questions\":[\"Implementation is committed on tekroo/product-owner-1\"],\"priority\":\"HIGH\"}")
	if _, err := parseRefinementStageResult(refinement); err == nil {
		t.Fatal("refinement accepted a pre-assignment branch identity")
	}
	specification := []byte(application.OrganizationalResultMarker + "\n{\"schema_version\":\"1.0.0\",\"result_type\":\"FEATURE_SPECIFICATION\",\"stories\":[{\"title\":\"Story\",\"description\":\"Deliver it from /Users/operator/worktree.\",\"acceptance_criteria\":[\"works\"],\"priority\":\"HIGH\"}],\"design_constraints\":[]}")
	if _, err := parseSpecificationStageResult(specification); err == nil {
		t.Fatal("specification accepted a pre-assignment workspace identity")
	}
}

func TestPreAssignmentPlanningResultsAllowRepositoryPathContainingTekrooDirectory(t *testing.T) {
	plan := []byte(application.OrganizationalResultMarker + "\n{\"schema_version\":\"1.0.0\",\"result_type\":\"FEATURE_PLAN\",\"architecture\":\"bounded\",\"design_decisions\":[],\"assumptions\":[],\"tasks\":[{\"story_index\":0,\"title\":\"Implement\",\"description\":\"Update cmd/tekroo/main.go.\",\"acceptance_criteria\":[\"works\"],\"covers\":[0],\"depends_on\":[],\"validates\":[],\"role\":\"coder\",\"purpose\":\"IMPLEMENTATION\",\"complexity\":3,\"risk\":\"LOW\",\"critical_path\":true,\"attempt_limit\":2,\"review_round_limit\":2}]}")
	if _, err := parseArchitectureStageResult(plan); err != nil {
		t.Fatalf("repository path was mistaken for an operational branch identity: %v", err)
	}
}

func TestPreAssignmentPlanningResultsPreserveOperatorSuppliedActorFQN(t *testing.T) {
	feature := organization.FeatureRequest{Input: organization.FeatureRequestInput{
		Title:              "Preserve an assigned actor",
		Description:        "Keep teams::coder-1 assigned to the task.",
		AcceptanceCriteria: []string{"teams::coder-1 remains the authoritative identity"},
		Constraints:        []string{"Do not assign a different actor"},
	}}
	allowed := featureAuthorizedActorFQNs(feature)
	refinement := []byte(application.OrganizationalResultMarker + "\n{\"schema_version\":\"1.0.0\",\"result_type\":\"FEATURE_REFINEMENT\",\"acceptance_criteria_disposition\":\"PRESERVE_SUBMITTED\",\"clarification_questions\":[\"Does teams::coder-1 remain the authoritative identity?\"],\"priority\":\"HIGH\"}")
	if _, err := parseRefinementStageResult(refinement, allowed...); err != nil {
		t.Fatalf("operator-supplied FQN was rejected: %v", err)
	}
	invented := []byte(application.OrganizationalResultMarker + "\n{\"schema_version\":\"1.0.0\",\"result_type\":\"FEATURE_REFINEMENT\",\"acceptance_criteria_disposition\":\"PRESERVE_SUBMITTED\",\"clarification_questions\":[\"Should teams::coder-2 perform the work?\"],\"priority\":\"HIGH\"}")
	if _, err := parseRefinementStageResult(invented, allowed...); err == nil {
		t.Fatal("model-invented FQN was accepted")
	}
	syntaxReference := []byte(application.OrganizationalResultMarker + "\n{\"schema_version\":\"1.0.0\",\"result_type\":\"FEATURE_REFINEMENT\",\"acceptance_criteria_disposition\":\"PRESERVE_SUBMITTED\",\"clarification_questions\":[\"Are names containing the FQN separator :: rejected while teams::coder-1 remains authoritative?\"],\"priority\":\"HIGH\"}")
	if _, err := parseRefinementStageResult(syntaxReference, allowed...); err != nil {
		t.Fatalf("FQN syntax reference was rejected as an actor identity: %v", err)
	}
}

func TestFeaturePlanningDescriptionCarriesAuthoritativeFeatureState(t *testing.T) {
	feature := organization.FeatureRequest{
		ID: "00000000-0000-7000-8000-000000000101",
		Input: organization.FeatureRequestInput{
			IdempotencyKey: "feature-context", Team: "example", Title: "Preserve exact request",
			Description: "Add Subtract without changing Add.", AcceptanceCriteria: []string{"Subtract works"},
			Priority: organization.PriorityNormal, Constraints: []string{"No new dependency"},
			Repository: "example/repository", WorkspaceID: "engineering", MaximumStories: 2, MaximumTasks: 8, MaximumHops: 8,
		},
		Refinement: &organization.FeatureRefinement{
			PreparedBy: "example::product-owner-1", PreparedExecution: kernel.ExecutionTuple{ExecutionID: "00000000-0000-7000-8000-000000000102", FencingEpoch: 1},
			AcceptanceCriteria: []string{"Subtract works"}, ClarificationQuestions: []string{}, Priority: organization.PriorityNormal, PreparedAt: time.Now().UTC(),
		},
		Specification: &organization.FeatureSpecification{
			PreparedBy: "example::project-manager-1", PreparedExecution: kernel.ExecutionTuple{ExecutionID: "00000000-0000-7000-8000-000000000103", FencingEpoch: 1},
			Stories:           []organization.PlannedStory{{ID: "00000000-0000-7000-8000-000000000104", Title: "Subtract", Description: "Implement Subtract.", AcceptanceCriteria: []string{"Subtract works"}, Priority: organization.PriorityNormal}},
			DesignConstraints: []string{"No new dependency"}, PreparedAt: time.Now().UTC(),
		},
	}
	for _, stage := range []featurePlanningStage{stageRefinement, stageSpecification, stageArchitecture} {
		_, _, route, _, instruction, _, err := legacyPlanningStageDefinition(stage)
		if err != nil {
			t.Fatal(err)
		}
		wantRoute := kernel.RouteBoundedExecution
		if stage == stageArchitecture {
			wantRoute = kernel.RouteComplexReasoning
		}
		if route != wantRoute {
			t.Fatalf("%s route = %s, want %s", stage, route, wantRoute)
		}
		description, err := featurePlanningDescription(feature, stage, instruction, softwareDevelopmentTaskRoutingPolicy())
		if err != nil {
			t.Fatal(err)
		}
		for _, required := range []string{"AUTHORITATIVE_FEATURE_STATE_JSON", "Preserve exact request", "Add Subtract without changing Add.", "Subtract works"} {
			if !strings.Contains(description, required) {
				t.Fatalf("%s description omitted %q: %s", stage, required, description)
			}
		}
		if count := strings.Count(description, "Subtract works"); count != 1 {
			t.Fatalf("%s description repeated authoritative acceptance criterion %d times", stage, count)
		}
		if stage == stageArchitecture {
			for _, required := range []string{"input_sha256", "refinement_sha256", "specification_sha256"} {
				if !strings.Contains(description, required) {
					t.Fatalf("architecture description omitted source provenance %q", required)
				}
			}
		}
		if stage == stageArchitecture {
			for _, required := range []string{"Read AGENTS.md", "read-only repository tools", "repository-relative files", "actually inspected", "external package or API", "inspected repository evidence", "dependency-manifest entry alone is insufficient", "implementation responsibility", "acyclic plan", "authored_task_policy.maximum_task_complexity", "Do not author test, validation, security-review, or product-acceptance tasks", "whole-feature validation", "do not select an operational role", "Teams applies execution_routing_policy", "task-specific acceptance criteria", "feature specification remains authoritative", "final product acceptance", "do not duplicate them across tasks merely for bookkeeping", "invariants", "ownership boundaries", "state transitions", "linearization points", "failure or compensation semantics", "read-then-act sequence", "proof of atomicity", "partial-failure safety", "without prescribing a particular implementation", "exactly one owning component or layer", "consume that owner's abstraction", "must not depend on higher-level workflow, deployment, or team configuration", "duplicate, conflicting, or inverted ownership", "one consistent state model", "unresolved mutually exclusive alternatives", "external control surfaces", "HIGH risk", "materialized_total=2*len(tasks)+count(HIGH-or-CRITICAL tasks)+2", "task_budget.maximum_total_tasks", "maximum_moderate_or_lower_implementation_tasks", "authored_task_policy", "purpose=IMPLEMENTATION", "backward depends_on indexes", "LOW, MODERATE, HIGH, or CRITICAL", "finish.message"} {
				if !strings.Contains(description, required) {
					t.Fatalf("architecture schema instruction omitted %q", required)
				}
			}
			if strings.Contains(instruction, "alias resolution") {
				t.Fatal("architecture instruction retained feature-adjacent alias language")
			}
			for _, required := range []string{"\"maximum_total_tasks\":8", "\"validation_tasks_per_implementation\":1", "\"additional_security_review_per_high_risk_implementation\":1", "\"reserved_feature_validation_tasks\":1", "\"reserved_product_acceptance_tasks\":1", "\"maximum_moderate_or_lower_implementation_tasks\":3"} {
				if !strings.Contains(description, required) {
					t.Fatalf("architecture description omitted derived task budget %q", required)
				}
			}
			if strings.Contains(description, "coverage_requirements") {
				t.Fatalf("architecture description retained redundant coverage indexes: %s", description)
			}
			if !strings.Contains(description, "\"authored_task_policy\":{\"allowed_purposes\":[\"IMPLEMENTATION\"],\"maximum_task_complexity\":6,\"may_author_validates\":false,\"role_selection_owner\":\"TEAMS_ROUTING_POLICY\",\"tasks_are_serialized\":false}") {
				t.Fatalf("architecture description omitted exact authored task policy: %s", description)
			}
			if !strings.Contains(description, "\"execution_routing_policy\":{\"purpose_routes\":[{\"purpose\":\"IMPLEMENTATION\",\"maximum_task_complexity\":6,\"may_author_validation_links\":false,\"serialize_tasks\":false,\"plan_complexity_role_bands\":[{\"minimum_plan_complexity\":1,\"role\":\"coder\"},{\"minimum_plan_complexity\":5,\"role\":\"senior-coder\"}]}]}") {
				t.Fatalf("architecture description omitted exact execution routing policy: %s", description)
			}
			if len(instruction) > 3200 {
				t.Fatalf("architecture instruction is %d bytes, want at most 3200", len(instruction))
			}
		} else {
			for _, required := range []string{"authoritative request", "operational identities", "finish.message", "call finish once"} {
				if !strings.Contains(description, required) {
					t.Fatalf("%s instruction omitted %q", stage, required)
				}
			}
			if len(instruction) > 1000 {
				t.Fatalf("%s instruction is %d bytes, want at most 1000", stage, len(instruction))
			}
			if stage == stageRefinement {
				for _, required := range []string{"acceptance_criteria_disposition", "PRESERVE_SUBMITTED", "without changing"} {
					if !strings.Contains(description, required) {
						t.Fatalf("refinement schema instruction omitted %q", required)
					}
				}
			}
			if stage == stageSpecification {
				for _, required := range []string{"smallest complete product specification", "Default to one story", "independently usable, testable, and releasable from the current baseline", "if every other proposed story were omitted", "Apply that omission test", "merge dependent outcomes", "one story, not layer stories", "Preserve every submitted acceptance criterion verbatim", "FEATURE_SPECIFICATION"} {
					if !strings.Contains(description, required) {
						t.Fatalf("specification schema instruction omitted %q", required)
					}
				}
			}
		}
	}
	planOutput := []byte(application.OrganizationalResultMarker + "\n{\"schema_version\":\"1.0.0\",\"result_type\":\"FEATURE_PLAN\",\"architecture\":\"bounded\",\"design_decisions\":[],\"assumptions\":[],\"tasks\":[{\"story_index\":0,\"title\":\"Define\",\"description\":\"Define the interface.\",\"acceptance_criteria\":[\"interface exists\"],\"depends_on\":[],\"validates\":[],\"purpose\":\"IMPLEMENTATION\",\"complexity\":3,\"risk\":\"LOW\",\"critical_path\":true,\"attempt_limit\":2,\"review_round_limit\":2},{\"story_index\":0,\"title\":\"Implement\",\"description\":\"Implement it.\",\"acceptance_criteria\":[\"works\"],\"depends_on\":[0],\"validates\":[],\"purpose\":\"IMPLEMENTATION\",\"complexity\":3,\"risk\":\"LOW\",\"critical_path\":true,\"attempt_limit\":2,\"review_round_limit\":2}]}")
	planDigest := digestBytes(planOutput)
	invocation := kernel.WorkInvocation{OutputDigest: &planDigest}
	_, _, _, _, instruction, _, err := legacyPlanningStageDefinition(stageArchitectureReview)
	if err != nil {
		t.Fatal(err)
	}
	reviewDescription, err := featureArchitectureReviewDescription(feature, invocation, planOutput, instruction)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"independently inspect", "every source-story description and criterion", "every task description and criterion", "materially supported as written", "complete acyclic result", "state ownership", "concurrency and atomicity", "interface compatibility", "dependency contracts", "command names", "endpoint paths", "tool names", "compatibility requirements", "stop discovery", "coverage", "findings", "Do not emit repetitive per-item supported verdicts", "Do not edit files", "PASS only", "FEATURE_PLAN_REVIEW", "reviewed_plan_sha256", "AUTHORITATIVE_FEATURE_PLAN_REVIEW_STATE_JSON", string(planDigest), "Implement it."} {
		if !strings.Contains(reviewDescription, required) {
			t.Fatalf("architecture review description omitted %q", required)
		}
	}
	taskReviewRole, taskReviewPurpose, taskReviewRoute, _, taskReviewInstruction, _, err := legacyPlanningStageDefinition(stageArchitectureTaskReview)
	if err != nil {
		t.Fatal(err)
	}
	if taskReviewRole != "" || taskReviewPurpose != kernel.PurposeReplan || taskReviewRoute != kernel.RouteComplexReasoning {
		t.Fatalf("task review binding = %s/%s/%s", taskReviewRole, taskReviewPurpose, taskReviewRoute)
	}
	taskReviewDescription, err := featureArchitectureTaskReviewDescription(feature, invocation, planOutput, 1, taskReviewInstruction)
	if err != nil {
		t.Fatal(err)
	}
	parsedPlan, err := parseArchitectureStageResult(planOutput)
	if err != nil {
		t.Fatal(err)
	}
	taskDigest, err := featurePlanningStateDigest(parsedPlan.Tasks[1])
	if err != nil {
		t.Fatal(err)
	}
	dependencyDigest, err := featurePlanningStateDigest(parsedPlan.Tasks[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"read-only technical review", "transitive dependency contracts", "promised earlier DAG outcomes", "do not reject a task merely because", "missing or insufficient dependency contract", "every operation", "state mutation", "atomicity claim", "exactly as written", "alternative implementation", "replacement mechanism", "not evidence for PASS", "remembered external-platform behavior", "unverified_prescriptions", "every acceptance criterion", "description_requires_task_change", "requires_task_change", "SUPPORTED or UNSUPPORTED", "return FAIL", "FEATURE_PLAN_TASK_REVIEW", "reviewed_dependency_indexes", "expected_reviewed_dependency_indexes", "expected_acceptance_criterion_indexes", "dependency_contracts", "verified_operations", "AUTHORITATIVE_FEATURE_PLAN_TASK_REVIEW_STATE_JSON", string(planDigest), string(taskDigest), string(dependencyDigest), "Define the interface.", "Implement it.", `"expected_reviewed_dependency_indexes":[0]`, `"expected_acceptance_criterion_indexes":[0]`} {
		if !strings.Contains(taskReviewDescription, required) {
			t.Fatalf("architecture task review description omitted %q", required)
		}
	}
	for _, redundant := range []string{"reviewed_dependency_sha256", "reviewed_description_sha256", "acceptance_criteria_sha256", "criterion_sha256"} {
		if strings.Contains(taskReviewDescription, redundant) {
			t.Fatalf("architecture task review description retained redundant model-transcribed identity %q", redundant)
		}
	}
}

func softwareDevelopmentTaskRoutingPolicy() workflowTaskRoutingPolicy {
	return workflowTaskRoutingPolicy{PurposeRoutes: []purposeTaskRoutingPolicy{{
		Purpose:                  kernel.PurposeImplementation,
		MaximumTaskComplexity:    6,
		MayAuthorValidationLinks: false,
		SerializeTasks:           false,
		PlanComplexityRoleBands: []planComplexityRoleBand{
			{MinimumPlanComplexity: 1, Role: "coder"},
			{MinimumPlanComplexity: 5, Role: "senior-coder"},
		},
	}}}
}

func parseArchitectureTaskForTest(t *testing.T, output []byte) architectureTaskResult {
	t.Helper()
	result, err := parseArchitectureStageResult(output)
	if err != nil {
		t.Fatal(err)
	}
	return result.Tasks[0]
}

func TestNormalizeArchitectureTaskCriteriaKeepsStoryAuthoritySeparate(t *testing.T) {
	specification := organization.FeatureSpecification{Stories: []organization.PlannedStory{
		{AcceptanceCriteria: []string{"first invariant", "second invariant"}},
	}}
	tasks := []architectureTaskResult{
		{StoryIndex: 0, Purpose: kernel.PurposeImplementation, AcceptanceCriteria: []string{"task-specific check", "first invariant"}, Covers: []uint32{1}},
		{StoryIndex: 0, Purpose: kernel.PurposeImplementation, AcceptanceCriteria: []string{"integration check"}, Covers: []uint32{99, 99}},
	}
	bound, err := normalizeArchitectureTaskCriteria(specification, tasks)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(bound[0].AcceptanceCriteria, []string{"task-specific check", "first invariant"}) || len(bound[0].Covers) != 0 {
		t.Fatalf("first task criteria = %v", bound[0].AcceptanceCriteria)
	}
	if !reflect.DeepEqual(bound[1].AcceptanceCriteria, []string{"integration check"}) || len(bound[1].Covers) != 0 {
		t.Fatalf("second task criteria = %v", bound[1].AcceptanceCriteria)
	}

	invalid := map[string][]architectureTaskResult{
		"out-of-range story":  {{StoryIndex: 1, Purpose: kernel.PurposeImplementation, AcceptanceCriteria: []string{"first invariant"}}},
		"duplicate criterion": {{StoryIndex: 0, Purpose: kernel.PurposeImplementation, AcceptanceCriteria: []string{"first invariant", "first invariant"}}},
	}
	for name, candidate := range invalid {
		if _, err := normalizeArchitectureTaskCriteria(specification, candidate); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

func TestNormalizeArchitectureTaskRelationsPreservesImplementationDAG(t *testing.T) {
	tasks := []architectureTaskResult{
		{Role: "coder", Purpose: kernel.PurposeImplementation, Complexity: 4},
		{Role: "senior-coder", Purpose: kernel.PurposeImplementation, Complexity: 5, DependsOn: []uint32{0}},
		{Role: "tester", Purpose: kernel.PurposeImplementation, Complexity: 3, DependsOn: []uint32{0}},
	}
	normalized, err := normalizeArchitectureTaskRelations(tasks, softwareDevelopmentTaskRoutingPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(normalized[2].DependsOn, []uint32{0}) {
		t.Fatalf("third task dependencies = %v", normalized[2].DependsOn)
	}
	for index, task := range normalized {
		if task.Role != "senior-coder" {
			t.Fatalf("task %d role = %q, want policy-selected senior-coder", index, task.Role)
		}
	}
	invalid := map[string][]architectureTaskResult{
		"implementation validates":  {{Role: "coder", Purpose: kernel.PurposeImplementation, Complexity: 4}, {Role: "coder", Purpose: kernel.PurposeImplementation, Complexity: 4, Validates: []uint32{0}}},
		"oversized implementation":  {{Role: "coder", Purpose: kernel.PurposeImplementation, Complexity: 7}},
		"model-authored validation": {{Role: "tester", Purpose: kernel.PurposeValidation, Complexity: 3}},
	}
	for name, candidate := range invalid {
		if _, err := normalizeArchitectureTaskRelations(candidate, softwareDevelopmentTaskRoutingPolicy()); err == nil {
			t.Fatalf("%s was silently normalized or accepted", name)
		}
	}
}

func TestNormalizeArchitectureTaskRelationsUsesConfigurableNonSoftwareRoles(t *testing.T) {
	policy := workflowTaskRoutingPolicy{PurposeRoutes: []purposeTaskRoutingPolicy{{
		Purpose:                  kernel.PurposeInvestigation,
		MaximumTaskComplexity:    8,
		MayAuthorValidationLinks: false,
		SerializeTasks:           false,
		PlanComplexityRoleBands: []planComplexityRoleBand{
			{MinimumPlanComplexity: 1, Role: "field-researcher"},
			{MinimumPlanComplexity: 7, Role: "lead-field-researcher"},
		},
	}}}
	tasks := []architectureTaskResult{
		{Role: "ignored-author-suggestion", Purpose: kernel.PurposeInvestigation, Complexity: 2},
		{Purpose: kernel.PurposeInvestigation, Complexity: 7},
	}
	normalized, err := normalizeArchitectureTaskRelations(tasks, policy)
	if err != nil {
		t.Fatal(err)
	}
	for index, task := range normalized {
		if task.Role != "lead-field-researcher" {
			t.Fatalf("task %d role = %q, want policy-selected lead-field-researcher", index, task.Role)
		}
		if len(task.DependsOn) != 0 {
			t.Fatalf("task %d was serialized despite configurable policy: %v", index, task.DependsOn)
		}
	}
}

func TestValidateArchitectureStageOutputIncludesMaterializationRules(t *testing.T) {
	feature := organization.FeatureRequest{
		Input: organization.FeatureRequestInput{MaximumTasks: 8},
		Specification: &organization.FeatureSpecification{Stories: []organization.PlannedStory{
			{AcceptanceCriteria: []string{"works"}},
		}},
	}
	homogeneous := []byte(application.OrganizationalResultMarker + "\n{\"schema_version\":\"1.0.0\",\"result_type\":\"FEATURE_PLAN\",\"architecture\":\"bounded\",\"design_decisions\":[],\"assumptions\":[],\"tasks\":[{\"story_index\":0,\"title\":\"First\",\"description\":\"Implement first.\",\"acceptance_criteria\":[\"works\"],\"covers\":[0],\"depends_on\":[],\"validates\":[],\"role\":\"coder\",\"purpose\":\"IMPLEMENTATION\",\"complexity\":3,\"risk\":\"LOW\",\"critical_path\":true,\"attempt_limit\":2,\"review_round_limit\":2},{\"story_index\":0,\"title\":\"Second\",\"description\":\"Implement second.\",\"acceptance_criteria\":[\"still works\"],\"covers\":[],\"depends_on\":[0],\"validates\":[],\"role\":\"coder\",\"purpose\":\"IMPLEMENTATION\",\"complexity\":3,\"risk\":\"LOW\",\"critical_path\":true,\"attempt_limit\":2,\"review_round_limit\":2}]}")
	service := &ProductionService{}
	if err := service.validateFeatureStageOutput(feature, stageArchitecture, homogeneous); err != nil {
		t.Fatalf("homogeneous executable plan rejected: %v", err)
	}
	mixed := []byte(strings.Replace(string(homogeneous), `"role":"coder","purpose":"IMPLEMENTATION","complexity":3,"risk":"LOW","critical_path":true,"attempt_limit":2,"review_round_limit":2}]}`, `"role":"senior-coder","purpose":"IMPLEMENTATION","complexity":3,"risk":"LOW","critical_path":true,"attempt_limit":2,"review_round_limit":2}]}`, 1))
	if err := service.validateFeatureStageOutput(feature, stageArchitecture, mixed); err != nil {
		t.Fatalf("legacy mixed model role suggestions overrode Teams routing policy: %v", err)
	}
	storyWithAdditionalCriteria := feature
	storyWithAdditionalCriteria.Specification = &organization.FeatureSpecification{Stories: []organization.PlannedStory{{
		AcceptanceCriteria: []string{"works", "backward compatibility remains intact"},
	}}}
	if err := service.validateFeatureStageOutput(storyWithAdditionalCriteria, stageArchitecture, homogeneous); err != nil {
		t.Fatalf("task-local acceptance criteria were incorrectly required to duplicate story acceptance: %v", err)
	}
}
