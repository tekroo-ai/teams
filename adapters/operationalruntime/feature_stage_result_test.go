package operationalruntime

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestFeatureStageResultsAreStrictAndBounded(t *testing.T) {
	refinement := []byte(application.OrganizationalResultMarker + "\n{\"schema_version\":\"1.0.0\",\"result_type\":\"FEATURE_REFINEMENT\",\"acceptance_criteria\":[\"works\"],\"clarification_questions\":[],\"priority\":\"HIGH\"}")
	if _, err := parseRefinementStageResult(refinement); err != nil {
		t.Fatal(err)
	}
	specification := []byte(application.OrganizationalResultMarker + "\n{\"schema_version\":\"1.0.0\",\"result_type\":\"FEATURE_SPECIFICATION\",\"stories\":[{\"title\":\"Story\",\"description\":\"Deliver it.\",\"acceptance_criteria\":[\"works\"],\"priority\":\"HIGH\"}],\"design_constraints\":[]}")
	if _, err := parseSpecificationStageResult(specification); err != nil {
		t.Fatal(err)
	}
	plan := []byte(application.OrganizationalResultMarker + "\n{\"schema_version\":\"1.0.0\",\"result_type\":\"FEATURE_PLAN\",\"architecture\":\"bounded\",\"design_decisions\":[],\"assumptions\":[],\"tasks\":[{\"story_index\":0,\"title\":\"Implement\",\"description\":\"Implement it.\",\"acceptance_criteria\":[\"works\"],\"depends_on\":[],\"validates\":[],\"role\":\"coder\",\"purpose\":\"IMPLEMENTATION\",\"complexity\":3,\"risk\":\"LOW\",\"critical_path\":true,\"attempt_limit\":2,\"review_round_limit\":2}]}")
	if _, err := parseArchitectureStageResult(plan); err != nil {
		t.Fatal(err)
	}
	invalid := append([]byte(nil), plan...)
	invalid = append(invalid, []byte("{}")...)
	if _, err := parseArchitectureStageResult(invalid); err == nil {
		t.Fatal("trailing JSON was accepted")
	}
}

func TestPreAssignmentPlanningResultsRejectOperationalIdentity(t *testing.T) {
	refinement := []byte(application.OrganizationalResultMarker + "\n{\"schema_version\":\"1.0.0\",\"result_type\":\"FEATURE_REFINEMENT\",\"acceptance_criteria\":[\"Implementation is committed on tekroo/product-owner-1\"],\"clarification_questions\":[],\"priority\":\"HIGH\"}")
	if _, err := parseRefinementStageResult(refinement); err == nil {
		t.Fatal("refinement accepted a pre-assignment branch identity")
	}
	specification := []byte(application.OrganizationalResultMarker + "\n{\"schema_version\":\"1.0.0\",\"result_type\":\"FEATURE_SPECIFICATION\",\"stories\":[{\"title\":\"Story\",\"description\":\"Deliver it from /Users/operator/worktree.\",\"acceptance_criteria\":[\"works\"],\"priority\":\"HIGH\"}],\"design_constraints\":[]}")
	if _, err := parseSpecificationStageResult(specification); err == nil {
		t.Fatal("specification accepted a pre-assignment workspace identity")
	}
}

func TestPreAssignmentPlanningResultsPreserveOperatorSuppliedActorFQN(t *testing.T) {
	feature := organization.FeatureRequest{Input: organization.FeatureRequestInput{
		Title:              "Add a local name",
		Description:        "Start teams::coder-1 as Bob.",
		AcceptanceCriteria: []string{"teams::coder-1 remains the authoritative identity"},
		Constraints:        []string{"Do not assign a different actor"},
	}}
	allowed := featureAuthorizedActorFQNs(feature)
	refinement := []byte(application.OrganizationalResultMarker + "\n{\"schema_version\":\"1.0.0\",\"result_type\":\"FEATURE_REFINEMENT\",\"acceptance_criteria\":[\"teams::coder-1 remains the authoritative identity\"],\"clarification_questions\":[],\"priority\":\"HIGH\"}")
	if _, err := parseRefinementStageResult(refinement, allowed...); err != nil {
		t.Fatalf("operator-supplied FQN was rejected: %v", err)
	}
	invented := []byte(application.OrganizationalResultMarker + "\n{\"schema_version\":\"1.0.0\",\"result_type\":\"FEATURE_REFINEMENT\",\"acceptance_criteria\":[\"teams::coder-2 performs the work\"],\"clarification_questions\":[],\"priority\":\"HIGH\"}")
	if _, err := parseRefinementStageResult(invented, allowed...); err == nil {
		t.Fatal("model-invented FQN was accepted")
	}
	syntaxReference := []byte(application.OrganizationalResultMarker + "\n{\"schema_version\":\"1.0.0\",\"result_type\":\"FEATURE_REFINEMENT\",\"acceptance_criteria\":[\"names containing the FQN separator :: are rejected\",\"teams::coder-1 remains authoritative\"],\"clarification_questions\":[],\"priority\":\"HIGH\"}")
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
		_, _, _, instruction, _, err := planningStageDefinition(stage)
		if err != nil {
			t.Fatal(err)
		}
		description, err := featurePlanningDescription(feature, stage, instruction)
		if err != nil {
			t.Fatal(err)
		}
		for _, required := range []string{"AUTHORITATIVE_FEATURE_STATE_JSON", "Preserve exact request", "Add Subtract without changing Add.", "Subtract works"} {
			if !strings.Contains(description, required) {
				t.Fatalf("%s description omitted %q: %s", stage, required, description)
			}
		}
		if stage == stageArchitecture {
			for _, required := range []string{"role class", "exactly one of coder, senior-coder, tester, or security", "JSON integer from 1 through 10", "arrays containing only zero-based integer task indexes", "IMPLEMENTATION and INVESTIGATION tasks must always use", "bounded local coding model", "three or more architectural layers", "two through six causal IMPLEMENTATION tasks", "complexity no greater than 6", "single causal order", "read AGENTS.md", "use rg for discovery", "read-only repository tools", "relevant current architecture", "repository-relative file paths", "Do not edit files", "finish.message is the only result Teams receives", "Do not put a summary or paraphrase in finish.message", "Do not choose or mention an actor instance, branch"} {
				if !strings.Contains(description, required) {
					t.Fatalf("architecture schema instruction omitted %q", required)
				}
			}
		} else {
			for _, required := range []string{"Do not add or change product requirements", "actor instance", "Teams assigns operational identities", "Call the OpenHands finish tool exactly once", "finish.message is the only result Teams receives", "A prose assessment is not a result"} {
				if !strings.Contains(description, required) {
					t.Fatalf("%s instruction omitted %q", stage, required)
				}
			}
			if stage == stageSpecification {
				for _, required := range []string{"smallest complete specification", "exactly one story", "verify that the result parses as JSON", "ends with ]}, never ]}}"} {
					if !strings.Contains(description, required) {
						t.Fatalf("specification schema instruction omitted %q", required)
					}
				}
			}
		}
	}
}

func TestNormalizeArchitectureTaskRelationsSerializesImplementationAndRemovesRedundantValidationEdges(t *testing.T) {
	tasks := []architectureTaskResult{
		{Role: "coder", Purpose: kernel.PurposeImplementation},
		{Role: "senior-coder", Purpose: kernel.PurposeImplementation, DependsOn: []uint32{0}, Validates: []uint32{0}},
		{Role: "tester", Purpose: kernel.PurposeImplementation, DependsOn: []uint32{0}, Validates: []uint32{0}},
	}
	normalized, err := normalizeArchitectureTaskRelations(tasks)
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized[1].Validates) != 0 || len(normalized[2].Validates) != 0 {
		t.Fatalf("implementation validation edges remain: %#v", normalized)
	}
	if !reflect.DeepEqual(normalized[2].DependsOn, []uint32{0, 1}) {
		t.Fatalf("third task dependencies = %v", normalized[2].DependsOn)
	}
	for index, task := range normalized {
		if task.Role != "coder" {
			t.Fatalf("implementation task %d role = %q, want coder", index, task.Role)
		}
	}
	invalidFirstRole := []architectureTaskResult{{Role: "tester", Purpose: kernel.PurposeImplementation}}
	normalized, err = normalizeArchitectureTaskRelations(invalidFirstRole)
	if err != nil || normalized[0].Role != "coder" {
		t.Fatalf("invalid first implementation role normalized = %#v err=%v", normalized, err)
	}
	invalid := []architectureTaskResult{{Purpose: kernel.PurposeImplementation}, {Purpose: kernel.PurposeImplementation, Validates: []uint32{0}}}
	if _, err := normalizeArchitectureTaskRelations(invalid); err == nil {
		t.Fatal("implementation validation edge without matching dependency was accepted")
	}
}

func TestRetryableFeaturePlanningInvocation(t *testing.T) {
	retryable := true
	notRetryable := false
	tests := []struct {
		name       string
		invocation kernel.WorkInvocation
		want       bool
	}{
		{name: "failed", invocation: kernel.WorkInvocation{State: kernel.InvocationFailed, AttemptOrdinal: 1, Retryable: &retryable}, want: true},
		{name: "timed out", invocation: kernel.WorkInvocation{State: kernel.InvocationTimedOut, AttemptOrdinal: 1, Retryable: &retryable}, want: true},
		{name: "start failed", invocation: kernel.WorkInvocation{State: kernel.InvocationStartFailed, AttemptOrdinal: 1, Retryable: &retryable}, want: true},
		{name: "not retryable", invocation: kernel.WorkInvocation{State: kernel.InvocationFailed, AttemptOrdinal: 1, Retryable: &notRetryable}},
		{name: "second attempt remains bounded", invocation: kernel.WorkInvocation{State: kernel.InvocationFailed, AttemptOrdinal: 2, Retryable: &retryable}, want: true},
		{name: "attempts exhausted", invocation: kernel.WorkInvocation{State: kernel.InvocationFailed, AttemptOrdinal: 3, Retryable: &retryable}},
		{name: "succeeded", invocation: kernel.WorkInvocation{State: kernel.InvocationSucceeded, AttemptOrdinal: 1, Retryable: &retryable}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := retryableFeaturePlanningInvocation(test.invocation, 3); got != test.want {
				t.Fatalf("retryableFeaturePlanningInvocation() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestTechnicalPlanningFailureClassification(t *testing.T) {
	for _, reason := range []string{"EXECUTION_BRIEF_SUPERSEDED", "SHELL_DISCIPLINE_VIOLATION", "REPEATED_SHELL_DISCIPLINE_VIOLATION", "REPEATED_REPOSITORY_SEARCH", "REPOSITORY_DISCOVERY_LIMIT_EXCEEDED"} {
		if !technicalPlanningFailure([]byte(`{"reason":"` + reason + `"}`)) {
			t.Fatalf("technical reason %q was not recognized", reason)
		}
	}
	if !technicalPlanningFailure([]byte(`{"invocation_id":"invocation-1","request_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","conversation_id":"conversation-1","state":"FAILED"}`)) {
		t.Fatal("retryable runtime failure receipt was not recognized")
	}
	for _, output := range [][]byte{[]byte(`{"reason":"TEST_FAILURE"}`), []byte(`{"reason":""}`), []byte(`not-json`)} {
		if technicalPlanningFailure(output) {
			t.Fatalf("substantive or malformed output classified as technical: %s", output)
		}
	}
}
