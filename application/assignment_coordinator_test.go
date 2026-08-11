package application_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

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
	}
	service := executionCommandFunc(func(_ context.Context, command kernel.KernelCommand, _ kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		commands = append(commands, command)
		eventID := eventIDs[len(commands)-1]
		return kernel.CommandReceipt{OutcomeCode: kernel.OutcomeApplied, EventIDs: []kernel.UUIDv7{eventID}}, nil
	})
	coordinator, err := application.NewAssignmentCoordinator(service)
	if err != nil {
		t.Fatal(err)
	}
	result, err := coordinator.Coordinate(context.Background(), request)
	if err != nil || result.ReadinessReceipt == nil || result.DispatchReceipt == nil || len(commands) != 2 {
		t.Fatalf("result=%#v err=%v commands=%d", result, err, len(commands))
	}
	if commands[0].CommandType != "tekroo.command.task.mark-ready" || commands[1].CommandType != "tekroo.command.task.dispatch" {
		t.Fatalf("command order = %s, %s", commands[0].CommandType, commands[1].CommandType)
	}
	var dispatch struct {
		Destination kernel.ActorFQN `json:"destination"`
		RoutingMode string          `json:"routing_mode"`
	}
	if json.Unmarshal(commands[1].Payload, &dispatch) != nil || dispatch.Destination != *decision.ActorFQN || dispatch.RoutingMode != "EXACT" {
		t.Fatalf("dispatch payload = %s", commands[1].Payload)
	}
	if commands[0].Authority != request.PolicyAuthority || commands[1].Authority != request.PolicyAuthority || commands[0].ActorFQN != nil || commands[1].ActorFQN != nil || commands[0].Execution != nil || commands[1].Execution != nil {
		t.Fatalf("policy coordinator forged actor context: %#v", commands)
	}
	if commands[0].ExpectedRevision.Revision != decision.Task.Revision || commands[1].ExpectedRevision.Revision != decision.Task.Revision+1 {
		t.Fatalf("revision sequence = %#v %#v", commands[0].ExpectedRevision, commands[1].ExpectedRevision)
	}
	wantDispatchParents := []kernel.DagParent{{ParentEventID: eventIDs[0], EdgeKind: kernel.EdgeCausal}}
	if !reflect.DeepEqual(commands[1].Causation, wantDispatchParents) {
		t.Fatalf("dispatch parents = %#v, want %#v", commands[1].Causation, wantDispatchParents)
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
	coordinator, _ := application.NewAssignmentCoordinator(service)
	result, err := coordinator.Coordinate(context.Background(), request)
	if err != nil || calls != 2 || result.DispatchReceipt == nil {
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
	coordinator, _ := application.NewAssignmentCoordinator(service)
	result, err := coordinator.Coordinate(context.Background(), request)
	wantParents := []kernel.DagParent{{ParentEventID: request.Input.ReadiedEventID, EdgeKind: kernel.EdgeCausal}}
	if err != nil || result.ReadinessReceipt != nil || result.DispatchReceipt == nil || len(commands) != 1 || commands[0].CommandType != "tekroo.command.task.dispatch" || !reflect.DeepEqual(commands[0].Causation, wantParents) {
		t.Fatalf("result=%#v err=%v commands=%#v", result, err, commands)
	}
}

func TestAssignmentCoordinatorReturnsPlannerNoEffectWithoutCommands(t *testing.T) {
	request := assignmentCoordinationRequest(t)
	for index := range request.Input.Candidates {
		request.Input.Candidates[index].Eligible = false
	}
	service := executionCommandFunc(func(context.Context, kernel.KernelCommand, kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		t.Fatal("no-effect plan must not issue a command")
		return kernel.CommandReceipt{}, nil
	})
	coordinator, _ := application.NewAssignmentCoordinator(service)
	result, err := coordinator.Coordinate(context.Background(), request)
	if err != nil || result.Decision.Status != kernel.AssignmentNoEligibleActor || result.ReadinessReceipt != nil || result.DispatchReceipt != nil {
		t.Fatalf("no-effect result = %#v, %v", result, err)
	}
}

func TestAssignmentCoordinatorReplayUsesIdenticalCommands(t *testing.T) {
	request := assignmentCoordinationRequest(t)
	var runs [][]kernel.KernelCommand
	var current []kernel.KernelCommand
	service := executionCommandFunc(func(_ context.Context, command kernel.KernelCommand, _ kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		current = append(current, command)
		ordinal := len(current)
		return kernel.CommandReceipt{OutcomeCode: kernel.OutcomeApplied, EventIDs: []kernel.UUIDv7{kernel.UUIDv7([]string{"", "00000000-0000-7000-8000-000000000081", "00000000-0000-7000-8000-000000000082"}[ordinal])}}, nil
	})
	coordinator, _ := application.NewAssignmentCoordinator(service)
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
		Input: input,
		Identity: application.AssignmentCommandIdentity{
			ReadinessCommandID: kernel.UUIDv7("00000000-0000-7000-8000-000000000061"), DispatchCommandID: kernel.UUIDv7("00000000-0000-7000-8000-000000000062"), CorrelationID: kernel.UUIDv7("00000000-0000-7000-8000-000000000064"), ReadinessKey: "ready-1", DispatchKey: "dispatch-1",
		},
		PolicyAuthority: kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "assignment-policy"}, PolicyRevision: 1, CatalogueRevision: kernel.CatalogueRevision, Provenance: basis,
	}
}

func assignmentInputForApplication() kernel.AssignmentInput {
	dependency := kernel.AggregateState{Kind: kernel.AggregateTask, ID: kernel.UUIDv7("00000000-0000-7000-8000-000000000011"), Revision: 2, LifecycleEpoch: 1, Phase: kernel.PhaseCompleted, Condition: kernel.ConditionRunnable}
	return kernel.AssignmentInput{
		Task:             kernel.AggregateState{Kind: kernel.AggregateTask, ID: kernel.UUIDv7("00000000-0000-7000-8000-000000000001"), Revision: 1, LifecycleEpoch: 1, Phase: kernel.PhasePlanned, Condition: kernel.ConditionRunnable},
		Dependencies:     []kernel.DependencyRequirement{{Aggregate: kernel.AggregateRef{Kind: dependency.Kind, ID: dependency.ID}, RequiredPhase: kernel.PhaseCompleted, ObservedState: &dependency, EvidenceEventID: kernel.UUIDv7("00000000-0000-7000-8000-000000000021")}},
		ReadinessParents: []kernel.DagParent{{ParentEventID: kernel.UUIDv7("00000000-0000-7000-8000-000000000021"), EdgeKind: kernel.EdgeCausal}}, ReadinessPolicyRevision: 1,
		Candidates: []kernel.AssignmentCandidate{{ActorFQN: kernel.ActorFQN("teams::coder-1"), Execution: kernel.ExecutionTuple{ExecutionID: kernel.UUIDv7("00000000-0000-7000-8000-000000000041"), FencingEpoch: 7}, Eligible: true}},
	}
}
