package operationalruntime

import (
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
			for _, required := range []string{"role class", "exactly one of coder, senior-coder, tester, or security", "JSON integer from 1 through 10", "arrays containing only zero-based integer task indexes", "both arrays must be empty", "finish.message is the only result Teams receives", "Do not put a summary or paraphrase in finish.message", "reasoning-only task", "Do not choose or mention an actor instance, branch"} {
				if !strings.Contains(description, required) {
					t.Fatalf("architecture schema instruction omitted %q", required)
				}
			}
		} else {
			for _, required := range []string{"Do not add or change product requirements", "actor instance", "Teams assigns operational identities"} {
				if !strings.Contains(description, required) {
					t.Fatalf("%s instruction omitted %q", stage, required)
				}
			}
		}
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
