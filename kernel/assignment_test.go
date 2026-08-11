package kernel_test

import (
	"reflect"
	"testing"

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
		input.Candidates[index].Eligible = false
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

func assignmentInput() kernel.AssignmentInput {
	taskID := assignmentUUID("00000000-0000-7000-8000-000000000001")
	dependencyOne := kernel.AggregateState{Kind: kernel.AggregateTask, ID: assignmentUUID("00000000-0000-7000-8000-000000000011"), Revision: 2, LifecycleEpoch: 1, Phase: kernel.PhaseCompleted, Condition: kernel.ConditionRunnable}
	dependencyTwo := kernel.AggregateState{Kind: kernel.AggregateStory, ID: assignmentUUID("00000000-0000-7000-8000-000000000012"), Revision: 3, LifecycleEpoch: 1, Phase: kernel.PhaseAccepted, Condition: kernel.ConditionRunnable}
	return kernel.AssignmentInput{
		Task: kernel.AggregateState{Kind: kernel.AggregateTask, ID: taskID, Revision: 1, LifecycleEpoch: 1, Phase: kernel.PhasePlanned, Condition: kernel.ConditionRunnable},
		Dependencies: []kernel.DependencyRequirement{
			{Aggregate: kernel.AggregateRef{Kind: dependencyOne.Kind, ID: dependencyOne.ID}, RequiredPhase: kernel.PhaseCompleted, ObservedState: &dependencyOne, EvidenceEventID: assignmentUUID("00000000-0000-7000-8000-000000000021")},
			{Aggregate: kernel.AggregateRef{Kind: dependencyTwo.Kind, ID: dependencyTwo.ID}, RequiredPhase: kernel.PhaseAccepted, ObservedState: &dependencyTwo, EvidenceEventID: assignmentUUID("00000000-0000-7000-8000-000000000022")},
		},
		ReadinessParents: []kernel.DagParent{
			{ParentEventID: assignmentUUID("00000000-0000-7000-8000-000000000021"), EdgeKind: kernel.EdgeCausal},
			{ParentEventID: assignmentUUID("00000000-0000-7000-8000-000000000022"), EdgeKind: kernel.EdgeDerivation},
		},
		ReadinessPolicyRevision: 1,
		Candidates: []kernel.AssignmentCandidate{
			{ActorFQN: kernel.ActorFQN("teams::coder-2"), Execution: kernel.ExecutionTuple{ExecutionID: assignmentUUID("00000000-0000-7000-8000-000000000042"), FencingEpoch: 4}, ActiveAssignments: 2, Eligible: true},
			{ActorFQN: kernel.ActorFQN("teams::coder-1"), Execution: kernel.ExecutionTuple{ExecutionID: assignmentUUID("00000000-0000-7000-8000-000000000041"), FencingEpoch: 7}, ActiveAssignments: 1, Eligible: true},
		},
	}
}

func assignmentUUID(value string) kernel.UUIDv7 { return kernel.UUIDv7(value) }
