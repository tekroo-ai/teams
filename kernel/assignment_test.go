package kernel_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

func TestAssignmentPlanIsInvariantToDependencyCandidateAndParentOrder(t *testing.T) {
	input := assignmentInput()
	first := kernel.PlanAssignment(input)
	input.Dependencies[0], input.Dependencies[1] = input.Dependencies[1], input.Dependencies[0]
	input.Candidates[0], input.Candidates[1] = input.Candidates[1], input.Candidates[0]
	input.ReadinessParents[0], input.ReadinessParents[1] = input.ReadinessParents[1], input.ReadinessParents[0]
	second := kernel.PlanAssignment(input)
	if !reflect.DeepEqual(first, second) || first.Status != kernel.AssignmentReady || first.ActorFQN == nil || *first.ActorFQN != kernel.ActorFQN("teams::coder-1") || !first.NeedsReadiness {
		t.Fatalf("decisions differ or selection is wrong: %#v %#v", first, second)
	}
}

func TestAssignmentPlanUsesExactDependencyPredicate(t *testing.T) {
	input := assignmentInput()
	input.Dependencies[0].ObservedState.Phase = kernel.PhaseClosed
	decision := kernel.PlanAssignment(input)
	if decision.Status != kernel.AssignmentBlocked || decision.ActorFQN != nil {
		t.Fatalf("closed dependency treated as completed: %#v", decision)
	}
	input.Dependencies[0].ObservedState = nil
	if decision = kernel.PlanAssignment(input); decision.Status != kernel.AssignmentBlocked {
		t.Fatalf("missing dependency = %#v", decision)
	}
}

func TestAssignmentPlanReturnsFiniteNoEffectOutcomes(t *testing.T) {
	input := assignmentInput()
	for index := range input.Candidates {
		input.Candidates[index].Qualification.Status = kernel.QualificationFail
	}
	if decision := kernel.PlanAssignment(input); decision.Status != kernel.AssignmentNoEligibleActor || decision.ActorFQN != nil {
		t.Fatalf("no-candidate decision = %#v", decision)
	}
	input = assignmentInput()
	owner := kernel.ActorFQN("teams::reviewer-1")
	input.Task.Ownership.OwnerFQN = &owner
	if decision := kernel.PlanAssignment(input); decision.Status != kernel.AssignmentAlreadyOwned {
		t.Fatalf("owned decision = %#v", decision)
	}
	input = assignmentInput()
	input.Candidates = append(input.Candidates, input.Candidates[0])
	if decision := kernel.PlanAssignment(input); decision.Status != kernel.AssignmentInvalid {
		t.Fatalf("duplicate actor decision = %#v", decision)
	}
	input = assignmentInput()
	input.Candidates = make([]kernel.AssignmentCandidate, 65)
	if decision := kernel.PlanAssignment(input); decision.Status != kernel.AssignmentInvalid {
		t.Fatalf("unbounded candidate decision = %#v", decision)
	}
}

func TestAssignmentPlanPreservesActorFQNAcrossExecutionRestart(t *testing.T) {
	input := assignmentInput()
	before := kernel.PlanAssignment(input)
	for index := range input.Candidates {
		if input.Candidates[index].ActorFQN == kernel.ActorFQN("teams::coder-1") {
			input.Candidates[index].Execution = kernel.ExecutionTuple{ExecutionID: assignmentUUID("00000000-0000-7000-8000-000000000099"), FencingEpoch: 8}
		}
	}
	after := kernel.PlanAssignment(input)
	if before.ActorFQN == nil || after.ActorFQN == nil || *before.ActorFQN != *after.ActorFQN || before.Execution == nil || after.Execution == nil || *before.Execution == *after.Execution {
		t.Fatalf("restart identity decisions = %#v %#v", before, after)
	}
}

func TestAssignmentDerivesEligibilityAndFailsClosed(t *testing.T) {
	input := assignmentInput()
	input.WorkProfile.Profile.MinimumDecisionRoute = kernel.RouteComplexReasoning
	if decision := kernel.PlanAssignment(input); decision.Status != kernel.AssignmentNoEligibleActor {
		t.Fatalf("capability downgrade decision = %#v", decision)
	}

	input = assignmentInput()
	revokedAt := input.DecidedAt.Add(-time.Minute)
	for index := range input.Candidates {
		input.Candidates[index].Qualification.RevokedAt = &revokedAt
	}
	if decision := kernel.PlanAssignment(input); decision.Status != kernel.AssignmentNoEligibleActor {
		t.Fatalf("revoked qualification decision = %#v", decision)
	}

	input = assignmentInput()
	for index := range input.Candidates {
		input.Candidates[index].HardConstraints[0].Outcome = kernel.ConstraintFail
	}
	if decision := kernel.PlanAssignment(input); decision.Status != kernel.AssignmentNoEligibleActor {
		t.Fatalf("failed hard constraint decision = %#v", decision)
	}
}

func TestAssignmentDoesNotTreatMissingCostAsZero(t *testing.T) {
	input := assignmentInput()
	input.Candidates[0].ExpectedTotalCost = kernel.CostObservation{}
	input.Candidates[0].ActiveAssignments = 0
	decision := kernel.PlanAssignment(input)
	if decision.Status != kernel.AssignmentReady || decision.ActorFQN == nil || *decision.ActorFQN != kernel.ActorFQN("teams::coder-1") {
		t.Fatalf("missing cost outranked reported cost: %#v", decision)
	}
}

func assignmentInput() kernel.AssignmentInput {
	taskID := assignmentUUID("00000000-0000-7000-8000-000000000001")
	dependencyOne := kernel.AggregateState{Kind: kernel.AggregateTask, ID: assignmentUUID("00000000-0000-7000-8000-000000000011"), Revision: 2, LifecycleEpoch: 1, Phase: kernel.PhaseCompleted, Condition: kernel.ConditionRunnable}
	dependencyTwo := kernel.AggregateState{Kind: kernel.AggregateStory, ID: assignmentUUID("00000000-0000-7000-8000-000000000012"), Revision: 3, LifecycleEpoch: 1, Phase: kernel.PhaseAccepted, Condition: kernel.ConditionRunnable}
	profile := assignmentProfile(taskID)
	return kernel.AssignmentInput{
		Task: kernel.AggregateState{Kind: kernel.AggregateTask, ID: taskID, Revision: 1, LifecycleEpoch: 1, ScopeRevision: 1, Phase: kernel.PhasePlanned, Condition: kernel.ConditionRunnable},
		Dependencies: []kernel.DependencyRequirement{
			{Aggregate: kernel.AggregateRef{Kind: dependencyOne.Kind, ID: dependencyOne.ID}, RequiredPhase: kernel.PhaseCompleted, ObservedState: &dependencyOne, EvidenceEventID: assignmentUUID("00000000-0000-7000-8000-000000000021")},
			{Aggregate: kernel.AggregateRef{Kind: dependencyTwo.Kind, ID: dependencyTwo.ID}, RequiredPhase: kernel.PhaseAccepted, ObservedState: &dependencyTwo, EvidenceEventID: assignmentUUID("00000000-0000-7000-8000-000000000022")},
		},
		ReadinessParents: []kernel.DagParent{
			{ParentEventID: assignmentUUID("00000000-0000-7000-8000-000000000021"), EdgeKind: kernel.EdgeCausal},
			{ParentEventID: assignmentUUID("00000000-0000-7000-8000-000000000022"), EdgeKind: kernel.EdgeDerivation},
		},
		ReadinessPolicyRevision: 1,
		WorkProfile:             kernel.WorkProfileSnapshot{Profile: profile, BoundEventID: assignmentUUID("00000000-0000-7000-8000-000000000030"), TaskRevision: 1},
		SelectionPolicyRevision: 1,
		SelectionPolicyDigest:   kernel.Digest("9999999999999999999999999999999999999999999999999999999999999999"),
		DecidedAt:               time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC),
		Candidates: []kernel.AssignmentCandidate{
			assignmentCandidate("teams::coder-2", "00000000-0000-7000-8000-000000000042", 4, 2, 200),
			assignmentCandidate("teams::coder-1", "00000000-0000-7000-8000-000000000041", 7, 1, 100),
		},
	}
}

func assignmentProfile(taskID kernel.UUIDv7) kernel.WorkRiskProfile {
	return kernel.WorkRiskProfile{
		TaskID: taskID, ProfileID: assignmentUUID("00000000-0000-7000-8000-000000000031"), ProfileRevision: 1,
		ProfileDigest: kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), LifecycleEpoch: 1, ScopeRevision: 1,
		WorkKind: kernel.WorkImplementation, Ambiguity: kernel.AmbiguityLow, Novelty: kernel.NoveltyRoutine, BlastRadius: kernel.BlastLocal, SecuritySensitivity: kernel.SecurityOrdinary,
		MinimumDecisionRoute: kernel.RouteBoundedExecution, AcceptanceCriteriaDigest: kernel.Digest("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
		RequiredDeterministicGateIDs: []string{"go-test"}, RequiredValidationBranches: 1,
		RequiredIndependenceDimensions: []kernel.IndependenceDimension{kernel.IndependencePrincipal, kernel.IndependenceActor, kernel.IndependenceExecution, kernel.IndependenceContext, kernel.IndependenceWorkspace, kernel.IndependenceMethod},
		ImplementationVariantCount:     1, ValidCandidateQuorum: 1, VerificationTopologyDigest: kernel.Digest("cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"),
		ClassificationPolicyRevision: 1, ClassificationPolicyDigest: kernel.Digest("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"),
		PromotionPolicyRevision: 1, PromotionPolicyDigest: kernel.Digest("eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"),
		Budgets:                 kernel.FiniteWorkBudgets{AttemptLimit: 2, ReviewRoundLimit: 2, PromotionLimit: 1, EscalationLimit: 1, DeadlineAt: time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)},
		ClassificationAuthority: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"}, ClassificationEvidenceIDs: []kernel.UUIDv7{assignmentUUID("00000000-0000-7000-8000-000000000032")},
	}
}

func assignmentCandidate(actor, execution string, fence uint64, active uint32, cost uint64) kernel.AssignmentCandidate {
	modelDigest := kernel.Digest("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
	evidenceID := assignmentUUID("00000000-0000-7000-8000-000000000032")
	return kernel.AssignmentCandidate{
		ActorFQN: kernel.ActorFQN(actor), Execution: kernel.ExecutionTuple{ExecutionID: assignmentUUID(execution), FencingEpoch: fence}, ActiveAssignments: active,
		ModelProfileDigest: modelDigest, RuntimeIdentityDigest: kernel.Digest("1111111111111111111111111111111111111111111111111111111111111111"),
		Qualification:     kernel.ModelProfileQualification{QualificationID: assignmentUUID("00000000-0000-7000-8000-000000000033"), QualificationDigest: kernel.Digest("2222222222222222222222222222222222222222222222222222222222222222"), QualificationCorpusDigest: kernel.Digest("3333333333333333333333333333333333333333333333333333333333333333"), ModelProfileDigest: modelDigest, DecisionRoute: kernel.RouteBoundedExecution, QualifiedRole: "programmer", QualifiedWorkKinds: []kernel.WorkKind{kernel.WorkImplementation}, Status: kernel.QualificationPass, ObservedAt: time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC), EvidenceIDs: []kernel.UUIDv7{evidenceID}},
		HardConstraints:   []kernel.HardConstraintResult{{ConstraintID: "data-residency", Outcome: kernel.ConstraintPass, EvidenceIDs: []kernel.UUIDv7{evidenceID}}},
		SelectionEvidence: []kernel.EvidenceRef{{EvidenceID: evidenceID, SHA256: kernel.Digest("4444444444444444444444444444444444444444444444444444444444444444")}}, SelectionReasons: []string{"least-cost qualified profile"}, ExpectedTotalCost: kernel.CostObservation{Reported: true, Microunits: cost},
	}
}

func assignmentUUID(value string) kernel.UUIDv7 { return kernel.UUIDv7(value) }
