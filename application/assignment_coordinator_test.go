package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/fake"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

func TestAssignmentCoordinatorEmitsReadinessAndExactDispatchWithoutForgingActorAcceptance(t *testing.T) {
	request := assignmentCoordinationRequest(t)
	decision := kernel.PlanAssignment(request.Input)
	var commands []kernel.KernelCommand
	eventIDs := []kernel.UUIDv7{
		kernel.UUIDv7("00000000-0000-7000-8000-000000000081"),
		kernel.UUIDv7("00000000-0000-7000-8000-000000000082"),
		kernel.UUIDv7("00000000-0000-7000-8000-000000000083"),
	}
	service := executionCommandFunc(func(_ context.Context, command kernel.KernelCommand, _ kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		commands = append(commands, command)
		eventID := eventIDs[len(commands)-1]
		return kernel.CommandReceipt{OutcomeCode: kernel.OutcomeApplied, EventIDs: []kernel.UUIDv7{eventID}}, nil
	})
	coordinator, err := application.NewAssignmentCoordinator(service, acceptingAdmission())
	if err != nil {
		t.Fatal(err)
	}
	result, err := coordinator.Coordinate(context.Background(), request)
	if err != nil || result.ReadinessReceipt == nil || result.AuthorizationReceipt == nil || result.DispatchReceipt == nil || len(commands) != 3 {
		t.Fatalf("result=%#v err=%v commands=%d", result, err, len(commands))
	}
	if commands[0].CommandType != "tekroo.command.task.mark-ready" || commands[1].CommandType != "tekroo.command.task.authorize-qualified-assignment" || commands[2].CommandType != "tekroo.command.task.dispatch" {
		t.Fatalf("command order = %s, %s, %s", commands[0].CommandType, commands[1].CommandType, commands[2].CommandType)
	}
	var dispatch struct {
		Destination kernel.ActorFQN `json:"destination"`
		RoutingMode string          `json:"routing_mode"`
	}
	if json.Unmarshal(commands[2].Payload, &dispatch) != nil || dispatch.Destination != *decision.ActorFQN || dispatch.RoutingMode != "EXACT" {
		t.Fatalf("dispatch payload = %s", commands[2].Payload)
	}
	if commands[0].Authority != request.PolicyAuthority || commands[1].Authority != request.PolicyAuthority || commands[2].Authority != request.PolicyAuthority || commands[0].ActorFQN != nil || commands[1].ActorFQN != nil || commands[2].ActorFQN != nil || commands[0].Execution != nil || commands[1].Execution != nil || commands[2].Execution != nil {
		t.Fatalf("policy coordinator forged actor context: %#v", commands)
	}
	if commands[0].ExpectedRevision.Revision != decision.Task.Revision || commands[1].ExpectedRevision.Revision != decision.Task.Revision+1 || commands[2].ExpectedRevision.Revision != decision.Task.Revision+2 {
		t.Fatalf("revision sequence = %#v %#v %#v", commands[0].ExpectedRevision, commands[1].ExpectedRevision, commands[2].ExpectedRevision)
	}
	wantDispatchParents := []kernel.DagParent{{ParentEventID: eventIDs[1], EdgeKind: kernel.EdgeCausal}}
	if !reflect.DeepEqual(commands[2].Causation, wantDispatchParents) {
		t.Fatalf("dispatch parents = %#v, want %#v", commands[2].Causation, wantDispatchParents)
	}
}

func TestAssignmentCoordinatorStopsAfterRejectedStage(t *testing.T) {
	request := assignmentCoordinationRequest(t)
	var calls int
	service := executionCommandFunc(func(_ context.Context, command kernel.KernelCommand, _ kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		calls++
		if calls == 2 {
			return kernel.CommandReceipt{OutcomeCode: kernel.OutcomeRejectedConflict, ReasonCode: "REVISION_CONFLICT"}, nil
		}
		return kernel.CommandReceipt{OutcomeCode: kernel.OutcomeApplied, EventIDs: []kernel.UUIDv7{kernel.UUIDv7("00000000-0000-7000-8000-000000000081")}}, nil
	})
	coordinator, _ := application.NewAssignmentCoordinator(service, acceptingAdmission())
	result, err := coordinator.Coordinate(context.Background(), request)
	if err != nil || calls != 2 || result.AuthorizationReceipt == nil || result.DispatchReceipt != nil {
		t.Fatalf("result=%#v err=%v calls=%d", result, err, calls)
	}
}

func TestAssignmentCoordinatorDispatchesReadyTaskFromReadiedEvent(t *testing.T) {
	request := assignmentCoordinationRequest(t)
	request.Input.Task.Phase = kernel.PhaseReady
	request.Input.ReadiedEventID = kernel.UUIDv7("00000000-0000-7000-8000-000000000031")
	var commands []kernel.KernelCommand
	service := executionCommandFunc(func(_ context.Context, command kernel.KernelCommand, _ kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		commands = append(commands, command)
		return kernel.CommandReceipt{OutcomeCode: kernel.OutcomeApplied, EventIDs: []kernel.UUIDv7{kernel.UUIDv7("00000000-0000-7000-8000-000000000082")}}, nil
	})
	coordinator, _ := application.NewAssignmentCoordinator(service, acceptingAdmission())
	result, err := coordinator.Coordinate(context.Background(), request)
	wantParents := []kernel.DagParent{{ParentEventID: request.Input.ReadiedEventID, EdgeKind: kernel.EdgeCausal}}
	if err != nil || result.ReadinessReceipt != nil || result.AuthorizationReceipt == nil || result.DispatchReceipt == nil || len(commands) != 2 || commands[0].CommandType != "tekroo.command.task.authorize-qualified-assignment" || commands[1].CommandType != "tekroo.command.task.dispatch" || !reflect.DeepEqual(commands[0].Causation, wantParents) {
		t.Fatalf("result=%#v err=%v commands=%#v", result, err, commands)
	}
}

func TestAssignmentCoordinatorReturnsPlannerNoEffectWithoutCommands(t *testing.T) {
	request := assignmentCoordinationRequest(t)
	for index := range request.Input.Candidates {
		request.Input.Candidates[index].Qualification.Status = kernel.QualificationFail
	}
	service := executionCommandFunc(func(context.Context, kernel.KernelCommand, kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		t.Fatal("no-effect plan must not issue a command")
		return kernel.CommandReceipt{}, nil
	})
	coordinator, _ := application.NewAssignmentCoordinator(service, acceptingAdmission())
	result, err := coordinator.Coordinate(context.Background(), request)
	if err != nil || result.Decision.Status != kernel.AssignmentNoEligibleActor || result.ReadinessReceipt != nil || result.DispatchReceipt != nil {
		t.Fatalf("no-effect result = %#v, %v", result, err)
	}
}

func TestAssignmentCoordinatorChecksContinuityBeforeIssuingAnyCommand(t *testing.T) {
	request := assignmentCoordinationRequest(t)
	service := executionCommandFunc(func(context.Context, kernel.KernelCommand, kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		t.Fatal("closed admission must prevent every assignment command")
		return kernel.CommandReceipt{}, nil
	})
	admission := continuityAdmissionFunc(func(_ context.Context, action kernel.ContinuityAction, powerEpoch uint64) (kernel.PolicyResult, error) {
		if action != kernel.ContinuityDispatch || powerEpoch != request.PowerEpoch {
			t.Fatalf("admission input = %q %d", action, powerEpoch)
		}
		return kernel.PolicyResult{Reason: "ADMISSION_CLOSED"}, nil
	})
	coordinator, err := application.NewAssignmentCoordinator(service, admission)
	if err != nil {
		t.Fatal(err)
	}
	result, err := coordinator.Coordinate(context.Background(), request)
	if !errors.Is(err, application.ErrWorkAdmissionDenied) || result.ReadinessReceipt != nil || result.AuthorizationReceipt != nil || result.DispatchReceipt != nil {
		t.Fatalf("closed-admission result=%#v err=%v", result, err)
	}
}

func TestAssignmentCoordinatorReplayUsesIdenticalCommands(t *testing.T) {
	request := assignmentCoordinationRequest(t)
	var runs [][]kernel.KernelCommand
	var current []kernel.KernelCommand
	service := executionCommandFunc(func(_ context.Context, command kernel.KernelCommand, _ kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		current = append(current, command)
		ordinal := len(current)
		return kernel.CommandReceipt{OutcomeCode: kernel.OutcomeApplied, EventIDs: []kernel.UUIDv7{kernel.UUIDv7([]string{"", "00000000-0000-7000-8000-000000000081", "00000000-0000-7000-8000-000000000082", "00000000-0000-7000-8000-000000000083"}[ordinal])}}, nil
	})
	coordinator, _ := application.NewAssignmentCoordinator(service, acceptingAdmission())
	for range 2 {
		current = nil
		if _, err := coordinator.Coordinate(context.Background(), request); err != nil {
			t.Fatal(err)
		}
		runs = append(runs, append([]kernel.KernelCommand(nil), current...))
	}
	if !reflect.DeepEqual(runs[0], runs[1]) {
		t.Fatalf("replay commands differ: %#v %#v", runs[0], runs[1])
	}
}

func assignmentCoordinationRequest(t *testing.T) application.AssignmentCoordinationRequest {
	t.Helper()
	input := assignmentInputForApplication()
	basis, err := fake.ProvenanceBasis()
	if err != nil {
		t.Fatal(err)
	}
	return application.AssignmentCoordinationRequest{
		Input:      input,
		PowerEpoch: 7,
		Identity: application.AssignmentCommandIdentity{
			ReadinessCommandID: kernel.UUIDv7("00000000-0000-7000-8000-000000000061"), AuthorizationCommandID: kernel.UUIDv7("00000000-0000-7000-8000-000000000062"), DispatchCommandID: kernel.UUIDv7("00000000-0000-7000-8000-000000000063"), CorrelationID: kernel.UUIDv7("00000000-0000-7000-8000-000000000064"), ReadinessKey: "ready-1", AuthorizationKey: "authorize-1", DispatchKey: "dispatch-1",
		},
		PolicyAuthority: kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "assignment-policy"}, PolicyRevision: 1, CatalogueRevision: kernel.CatalogueRevision, Provenance: basis,
	}
}

func acceptingAdmission() continuityAdmissionFunc {
	return func(context.Context, kernel.ContinuityAction, uint64) (kernel.PolicyResult, error) {
		return kernel.PolicyResult{Accepted: true, Reason: "ACCEPTED"}, nil
	}
}

type continuityAdmissionFunc func(context.Context, kernel.ContinuityAction, uint64) (kernel.PolicyResult, error)

func (function continuityAdmissionFunc) Admit(ctx context.Context, action kernel.ContinuityAction, powerEpoch uint64) (kernel.PolicyResult, error) {
	return function(ctx, action, powerEpoch)
}

func assignmentInputForApplication() kernel.AssignmentInput {
	dependency := kernel.AggregateState{Kind: kernel.AggregateTask, ID: kernel.UUIDv7("00000000-0000-7000-8000-000000000011"), Revision: 2, LifecycleEpoch: 1, Phase: kernel.PhaseCompleted, Condition: kernel.ConditionRunnable}
	taskID := kernel.UUIDv7("00000000-0000-7000-8000-000000000001")
	profile := applicationWorkProfile(taskID)
	return kernel.AssignmentInput{
		Task:             kernel.AggregateState{Kind: kernel.AggregateTask, ID: taskID, Revision: 1, LifecycleEpoch: 1, ScopeRevision: 1, Phase: kernel.PhasePlanned, Condition: kernel.ConditionRunnable},
		Dependencies:     []kernel.DependencyRequirement{{Aggregate: kernel.AggregateRef{Kind: dependency.Kind, ID: dependency.ID}, RequiredPhase: kernel.PhaseCompleted, ObservedState: &dependency, EvidenceEventID: kernel.UUIDv7("00000000-0000-7000-8000-000000000021")}},
		ReadinessParents: []kernel.DagParent{{ParentEventID: kernel.UUIDv7("00000000-0000-7000-8000-000000000021"), EdgeKind: kernel.EdgeCausal}}, ReadinessPolicyRevision: 1,
		WorkProfile: kernel.WorkProfileSnapshot{Profile: profile, BoundEventID: kernel.UUIDv7("00000000-0000-7000-8000-000000000030"), TaskRevision: 1}, SelectionPolicyRevision: 1,
		SelectionPolicyDigest: kernel.Digest("9999999999999999999999999999999999999999999999999999999999999999"), DecidedAt: time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC),
		Candidates: []kernel.AssignmentCandidate{applicationAssignmentCandidate()},
	}
}

func applicationWorkProfile(taskID kernel.UUIDv7) kernel.WorkRiskProfile {
	return kernel.WorkRiskProfile{
		TaskID: taskID, ProfileID: kernel.UUIDv7("00000000-0000-7000-8000-000000000031"), ProfileRevision: 1, ProfileDigest: kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), LifecycleEpoch: 1, ScopeRevision: 1,
		WorkKind: kernel.WorkImplementation, Ambiguity: kernel.AmbiguityLow, Novelty: kernel.NoveltyRoutine, BlastRadius: kernel.BlastLocal, SecuritySensitivity: kernel.SecurityOrdinary, MinimumDecisionRoute: kernel.RouteBoundedExecution,
		AcceptanceCriteriaDigest: kernel.Digest("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"), RequiredDeterministicGateIDs: []string{"go-test"}, RequiredValidationBranches: 1,
		RequiredIndependenceDimensions: []kernel.IndependenceDimension{kernel.IndependencePrincipal, kernel.IndependenceActor, kernel.IndependenceExecution, kernel.IndependenceContext, kernel.IndependenceWorkspace, kernel.IndependenceMethod}, ImplementationVariantCount: 1, ValidCandidateQuorum: 1,
		VerificationTopologyDigest: kernel.Digest("cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"), ClassificationPolicyRevision: 1, ClassificationPolicyDigest: kernel.Digest("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"), PromotionPolicyRevision: 1, PromotionPolicyDigest: kernel.Digest("eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"),
		Budgets: kernel.FiniteWorkBudgets{AttemptLimit: 2, ReviewRoundLimit: 2, PromotionLimit: 1, EscalationLimit: 1, DeadlineAt: time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)}, ClassificationAuthority: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"}, ClassificationEvidenceIDs: []kernel.UUIDv7{kernel.UUIDv7("00000000-0000-7000-8000-000000000032")},
	}
}

func applicationAssignmentCandidate() kernel.AssignmentCandidate {
	evidenceID := kernel.UUIDv7("00000000-0000-7000-8000-000000000032")
	modelDigest := kernel.Digest("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
	return kernel.AssignmentCandidate{
		ActorFQN: kernel.ActorFQN("teams::coder-1"), Execution: kernel.ExecutionTuple{ExecutionID: kernel.UUIDv7("00000000-0000-7000-8000-000000000041"), FencingEpoch: 7}, ModelProfileDigest: modelDigest, RuntimeIdentityDigest: kernel.Digest("1111111111111111111111111111111111111111111111111111111111111111"),
		Qualification:   kernel.ModelProfileQualification{QualificationID: kernel.UUIDv7("00000000-0000-7000-8000-000000000033"), QualificationDigest: kernel.Digest("2222222222222222222222222222222222222222222222222222222222222222"), QualificationCorpusDigest: kernel.Digest("3333333333333333333333333333333333333333333333333333333333333333"), ModelProfileDigest: modelDigest, DecisionRoute: kernel.RouteBoundedExecution, QualifiedRole: "programmer", QualifiedWorkKinds: []kernel.WorkKind{kernel.WorkImplementation}, Status: kernel.QualificationPass, ObservedAt: time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC), EvidenceIDs: []kernel.UUIDv7{evidenceID}},
		HardConstraints: []kernel.HardConstraintResult{{ConstraintID: "data-residency", Outcome: kernel.ConstraintPass, EvidenceIDs: []kernel.UUIDv7{evidenceID}}}, SelectionEvidence: []kernel.EvidenceRef{{EvidenceID: evidenceID, SHA256: kernel.Digest("4444444444444444444444444444444444444444444444444444444444444444")}}, SelectionReasons: []string{"least-cost qualified profile"}, ExpectedTotalCost: kernel.CostObservation{Reported: true, Microunits: 100},
	}
}
