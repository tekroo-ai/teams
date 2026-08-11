package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/tekroo-ai/teams/adapters/fake"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

func TestCompletionCoordinatorEmitsExactFencedTaskCompletion(t *testing.T) {
	request := taskCompletionRequest(t)
	catalogue := loadCatalogue(t)
	var commands []kernel.KernelCommand
	service := executionCommandFunc(func(_ context.Context, command kernel.KernelCommand, _ kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		emits, err := catalogue.ValidateFixtureCommand(command.CommandType, command.Payload)
		if err != nil || !reflect.DeepEqual(emits, []string{"tekroo.event.task.completed"}) {
			t.Fatalf("contract completion validation: emits=%v err=%v payload=%s", emits, err, command.Payload)
		}
		commands = append(commands, command)
		return kernel.CommandReceipt{OutcomeCode: kernel.OutcomeApplied, EventIDs: []kernel.UUIDv7{kernel.UUIDv7("00000000-0000-7000-8000-000000000731")}}, nil
	})
	coordinator, err := application.NewCompletionCoordinator(service)
	if err != nil {
		t.Fatal(err)
	}
	result, err := coordinator.Complete(context.Background(), request)
	if err != nil || result.Decision.Status != kernel.CompletionReady || result.Receipt == nil || len(commands) != 1 {
		t.Fatalf("completion result=%#v err=%v commands=%d", result, err, len(commands))
	}
	command := commands[0]
	if command.CommandType != "tekroo.command.task.request-completion" || command.Authority != request.Authority || command.ActorFQN == nil || *command.ActorFQN != *request.ActorFQN || command.Execution == nil || *command.Execution != *request.Execution || command.ExpectedRevision.Revision != request.Input.Work.Revision || command.ExpectedLifecycleEpoch == nil || *command.ExpectedLifecycleEpoch != request.Input.Work.LifecycleEpoch {
		t.Fatalf("task completion command = %#v", command)
	}
	if len(command.Causation) != 3 || command.Causation[0].ParentEventID != request.Input.Review.Finalization.EventID {
		t.Fatalf("completion causation = %#v", command.Causation)
	}
	var payload struct {
		OwnerFQN                   kernel.ActorFQN `json:"owner_fqn"`
		CompletionReviewID         kernel.UUIDv7   `json:"completion_review_id"`
		CompletionReviewRevision   uint64          `json:"completion_review_revision"`
		ValidationFinalizedEventID kernel.UUIDv7   `json:"validation_finalized_event_id"`
	}
	if json.Unmarshal(command.Payload, &payload) != nil || payload.OwnerFQN != *request.ActorFQN || payload.CompletionReviewID != request.Input.ReviewID || payload.CompletionReviewRevision != request.Input.Review.ReviewRevision || payload.ValidationFinalizedEventID != request.Input.Review.Finalization.EventID {
		t.Fatalf("task completion payload = %s", command.Payload)
	}
}

func TestCompletionCoordinatorStoryDependenciesAndReplayAreDeterministic(t *testing.T) {
	request := taskCompletionRequest(t)
	request.Input = applicationCompletionInput(kernel.AggregateStory)
	request.Authority = kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "completion-policy"}
	request.ActorFQN, request.Execution = nil, nil
	var commands []kernel.KernelCommand
	service := executionCommandFunc(func(_ context.Context, command kernel.KernelCommand, _ kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		commands = append(commands, command)
		return kernel.CommandReceipt{OutcomeCode: kernel.OutcomeApplied, EventIDs: []kernel.UUIDv7{kernel.UUIDv7("00000000-0000-7000-8000-000000000732")}}, nil
	})
	coordinator, _ := application.NewCompletionCoordinator(service)
	if _, err := coordinator.Complete(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Complete(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 2 || !reflect.DeepEqual(commands[0], commands[1]) || commands[0].CommandType != "tekroo.command.story.request-completion" || len(commands[0].Preconditions) != 2 || commands[0].Preconditions[0].Aggregate.ID >= commands[0].Preconditions[1].Aggregate.ID {
		t.Fatalf("story replay commands = %#v", commands)
	}
}

func TestCompletionCoordinatorDoesNotCommandForFailedReviewOrWrongTaskAuthority(t *testing.T) {
	request := taskCompletionRequest(t)
	commandCount := 0
	service := executionCommandFunc(func(context.Context, kernel.KernelCommand, kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		commandCount++
		return kernel.CommandReceipt{}, nil
	})
	coordinator, _ := application.NewCompletionCoordinator(service)
	request.Input.Review.Join.Status = "FAIL"
	request.Input.Review.Finalization.TerminalStatus = "FAIL"
	result, err := coordinator.Complete(context.Background(), request)
	if err != nil || result.Decision.Status != kernel.CompletionNotReady || result.Receipt != nil || commandCount != 0 {
		t.Fatalf("failed-review result=%#v err=%v commands=%d", result, err, commandCount)
	}
	request = taskCompletionRequest(t)
	request.Authority = kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "completion-policy"}
	if _, err := coordinator.Complete(context.Background(), request); !errors.Is(err, application.ErrInvalidCompletionCoordination) || commandCount != 0 {
		t.Fatalf("wrong-authority err=%v commands=%d", err, commandCount)
	}
}

func TestCompletionCoordinatorEmitsExplicitReopeningWithoutActorImpersonation(t *testing.T) {
	request := reopeningRequest(t)
	catalogue := loadCatalogue(t)
	var command kernel.KernelCommand
	service := executionCommandFunc(func(_ context.Context, value kernel.KernelCommand, _ kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		command = value
		emits, err := catalogue.ValidateFixtureCommand(value.CommandType, value.Payload)
		if err != nil || !reflect.DeepEqual(emits, []string{"tekroo.event.work.reopened"}) {
			t.Fatalf("contract reopening validation: emits=%v err=%v payload=%s", emits, err, value.Payload)
		}
		return kernel.CommandReceipt{OutcomeCode: kernel.OutcomeApplied, EventIDs: []kernel.UUIDv7{kernel.UUIDv7("00000000-0000-7000-8000-000000000733")}}, nil
	})
	coordinator, _ := application.NewCompletionCoordinator(service)
	result, err := coordinator.Reopen(context.Background(), request)
	if err != nil || result.Decision.Status != kernel.ReopeningReady || result.Receipt == nil {
		t.Fatalf("reopening result=%#v err=%v", result, err)
	}
	if command.CommandType != "tekroo.command.work.reopen" || command.Authority != request.Authority || command.ActorFQN != nil || command.Execution != nil || command.ExpectedLifecycleEpoch == nil || *command.ExpectedLifecycleEpoch != 2 {
		t.Fatalf("reopening command = %#v", command)
	}
	var payload struct {
		PriorEpoch       uint64 `json:"prior_epoch"`
		NewScopeRevision uint64 `json:"new_scope_revision"`
		OwnerCarry       bool   `json:"owner_carry_forward"`
	}
	if json.Unmarshal(command.Payload, &payload) != nil || payload.PriorEpoch != 2 || payload.NewScopeRevision != 4 || !payload.OwnerCarry {
		t.Fatalf("reopening payload = %s", command.Payload)
	}
}

func taskCompletionRequest(t *testing.T) application.CompletionCoordinationRequest {
	t.Helper()
	basis, err := fake.ProvenanceBasis()
	if err != nil {
		t.Fatal(err)
	}
	actor := kernel.ActorFQN("teams::coder-1")
	return application.CompletionCoordinationRequest{
		Input:     applicationCompletionInput(kernel.AggregateTask),
		Identity:  application.CompletionCommandIdentity{CommandID: kernel.UUIDv7("00000000-0000-7000-8000-000000000721"), CorrelationID: kernel.UUIDv7("00000000-0000-7000-8000-000000000722"), IdempotencyKey: "complete-work-1"},
		Authority: kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: string(actor)}, ActorFQN: &actor,
		Execution:      &kernel.ExecutionTuple{ExecutionID: kernel.UUIDv7("00000000-0000-7000-8000-000000000723"), FencingEpoch: 4},
		PolicyRevision: 7, CatalogueRevision: kernel.CatalogueRevision, Provenance: basis,
	}
}

func reopeningRequest(t *testing.T) application.ReopeningCoordinationRequest {
	t.Helper()
	basis, err := fake.ProvenanceBasis()
	if err != nil {
		t.Fatal(err)
	}
	owner := kernel.ActorFQN("teams::coder-1")
	return application.ReopeningCoordinationRequest{
		Input: kernel.ReopeningInput{
			Work:             kernel.AggregateState{Kind: kernel.AggregateTask, ID: kernel.UUIDv7("00000000-0000-7000-8000-000000000711"), Revision: 7, LifecycleEpoch: 2, Phase: kernel.PhaseCompleted, Condition: kernel.ConditionRunnable, Ownership: kernel.Ownership{OwnerFQN: &owner, OwnershipVersion: 3}},
			NewScopeRevision: 4, Reason: "new evidence requires remediation", OwnerCarryForward: true,
			EvidenceRefs: []kernel.EvidenceRef{{EvidenceID: kernel.UUIDv7("00000000-0000-7000-8000-000000000712"), SHA256: kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")}},
			Parents:      []kernel.DagParent{{ParentEventID: kernel.UUIDv7("00000000-0000-7000-8000-000000000714"), EdgeKind: kernel.EdgeCausal}},
		},
		Identity:  application.CompletionCommandIdentity{CommandID: kernel.UUIDv7("00000000-0000-7000-8000-000000000724"), CorrelationID: kernel.UUIDv7("00000000-0000-7000-8000-000000000725"), IdempotencyKey: "reopen-work-1"},
		Authority: kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "reopening-policy"}, PolicyRevision: 7, CatalogueRevision: kernel.CatalogueRevision, Provenance: basis,
	}
}

func applicationCompletionInput(kind kernel.AggregateKind) kernel.CompletionInput {
	workID := kernel.UUIDv7("00000000-0000-7000-8000-000000000701")
	owner := kernel.ActorFQN("teams::coder-1")
	finalizedID := kernel.UUIDv7("00000000-0000-7000-8000-000000000702")
	reviewID := kernel.UUIDv7("00000000-0000-7000-8000-000000000703")
	input := kernel.CompletionInput{
		Work:            kernel.AggregateState{Kind: kind, ID: workID, Revision: 5, LifecycleEpoch: 2, Phase: kernel.PhaseActive, Condition: kernel.ConditionRunnable, Ownership: kernel.Ownership{OwnerFQN: &owner, OwnershipVersion: 3}},
		ReviewID:        reviewID,
		Review:          kernel.CompletionReviewSnapshot{ReviewID: reviewID, Subject: kernel.AggregateRef{Kind: kind, ID: workID}, LifecycleEpoch: 2, CriteriaRevision: 6, BranchPolicyRevision: 4, ReviewRevision: 3, Join: kernel.ReviewJoinResult{Complete: true, Status: "PASS"}, Finalization: &kernel.ReviewFinalization{EventID: finalizedID, ReviewRevision: 3, TerminalStatus: "PASS"}},
		EvidenceRefs:    []kernel.EvidenceRef{{EvidenceID: kernel.UUIDv7("00000000-0000-7000-8000-000000000704"), SHA256: kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")}},
		ArtifactDigests: []kernel.Digest{kernel.Digest("cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")},
		Parents:         []kernel.DagParent{{ParentEventID: kernel.UUIDv7("00000000-0000-7000-8000-000000000706"), EdgeKind: kernel.EdgeDerivation}, {ParentEventID: kernel.UUIDv7("00000000-0000-7000-8000-000000000707"), EdgeKind: kernel.EdgeCausal}},
	}
	if kind == kernel.AggregateStory {
		input.DependencyTasks = []kernel.AggregateState{
			{Kind: kernel.AggregateTask, ID: kernel.UUIDv7("00000000-0000-7000-8000-000000000709"), Revision: 2, LifecycleEpoch: 1, Phase: kernel.PhaseCompleted},
			{Kind: kernel.AggregateTask, ID: kernel.UUIDv7("00000000-0000-7000-8000-000000000708"), Revision: 3, LifecycleEpoch: 1, Phase: kernel.PhaseCompleted},
		}
	}
	return input
}
