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

func TestValidationCoordinatorOpensCanonicalPolicyOwnedReview(t *testing.T) {
	request := validationReviewRequest(t)
	catalogue := loadCatalogue(t)
	var commands []kernel.KernelCommand
	service := executionCommandFunc(func(_ context.Context, command kernel.KernelCommand, _ kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		emits, err := catalogue.ValidateFixtureCommand(command.CommandType, command.Payload)
		if err != nil || !reflect.DeepEqual(emits, []string{"tekroo.event.completion-review.opened"}) {
			t.Fatalf("frozen contract validation: emits=%v err=%v payload=%s", emits, err, command.Payload)
		}
		commands = append(commands, command)
		return kernel.CommandReceipt{OutcomeCode: kernel.OutcomeApplied, EventIDs: []kernel.UUIDv7{kernel.UUIDv7("00000000-0000-7000-8000-000000000601")}}, nil
	})
	coordinator, err := application.NewValidationReviewCoordinator(service)
	if err != nil {
		t.Fatal(err)
	}
	result, err := coordinator.Open(context.Background(), request)
	if err != nil || result.Decision.Status != kernel.ValidationOpeningReady || result.Receipt == nil || len(commands) != 1 {
		t.Fatalf("result=%#v err=%v commands=%d", result, err, len(commands))
	}
	command := commands[0]
	if command.CommandType != "tekroo.command.completion-review.open" || command.Target.Kind != kernel.AggregateCompletionReview || command.Target.ID != request.Input.ReviewID || !command.ExpectedRevision.MustNotExist || command.Authority != request.PolicyAuthority || command.ActorFQN != nil || command.Execution != nil {
		t.Fatalf("review command = %#v", command)
	}
	if len(command.Preconditions) != 1 || command.Preconditions[0].Aggregate.ID != request.Input.Subject.ID || command.Preconditions[0].Expected.Revision != request.Input.Subject.Revision {
		t.Fatalf("subject precondition = %#v", command.Preconditions)
	}
	var payload struct {
		RequiredBranchIDs []string `json:"required_branch_ids"`
		JoinRule          string   `json:"join_rule"`
	}
	if json.Unmarshal(command.Payload, &payload) != nil || !reflect.DeepEqual(payload.RequiredBranchIDs, []string{"review", "tests"}) || payload.JoinRule != "ALL_PASS" {
		t.Fatalf("review payload = %s", command.Payload)
	}
}

func TestValidationCoordinatorReturnsInvalidPlanWithoutCommand(t *testing.T) {
	request := validationReviewRequest(t)
	request.Input.RequiredBranchIDs = []string{"tests", "tests"}
	service := executionCommandFunc(func(context.Context, kernel.KernelCommand, kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		t.Fatal("invalid plan must not issue a command")
		return kernel.CommandReceipt{}, nil
	})
	coordinator, _ := application.NewValidationReviewCoordinator(service)
	result, err := coordinator.Open(context.Background(), request)
	if err != nil || result.Decision.Status != kernel.ValidationOpeningInvalid || result.Receipt != nil {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestValidationCoordinatorReplayEmitsIdenticalCommand(t *testing.T) {
	request := validationReviewRequest(t)
	var commands []kernel.KernelCommand
	service := executionCommandFunc(func(_ context.Context, command kernel.KernelCommand, _ kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		commands = append(commands, command)
		return kernel.CommandReceipt{OutcomeCode: kernel.OutcomeApplied, EventIDs: []kernel.UUIDv7{kernel.UUIDv7("00000000-0000-7000-8000-000000000601")}}, nil
	})
	coordinator, _ := application.NewValidationReviewCoordinator(service)
	if _, err := coordinator.Open(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Open(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 2 || !reflect.DeepEqual(commands[0], commands[1]) {
		t.Fatalf("replay commands = %#v", commands)
	}
}

func TestValidationCoordinatorPreservesRejectedReceipt(t *testing.T) {
	request := validationReviewRequest(t)
	service := executionCommandFunc(func(context.Context, kernel.KernelCommand, kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		return kernel.CommandReceipt{OutcomeCode: kernel.OutcomeRejectedConflict, ReasonCode: "REVIEW_ALREADY_OPEN"}, nil
	})
	coordinator, _ := application.NewValidationReviewCoordinator(service)
	result, err := coordinator.Open(context.Background(), request)
	if err != nil || result.Receipt == nil || result.Receipt.OutcomeCode != kernel.OutcomeRejectedConflict {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func validationReviewRequest(t *testing.T) application.ValidationReviewRequest {
	t.Helper()
	basis, err := fake.ProvenanceBasis()
	if err != nil {
		t.Fatal(err)
	}
	return application.ValidationReviewRequest{
		Input: kernel.ValidationReviewInput{
			ReviewID:         kernel.UUIDv7("00000000-0000-7000-8000-000000000501"),
			Subject:          kernel.AggregateState{Kind: kernel.AggregateTask, ID: kernel.UUIDv7("00000000-0000-7000-8000-000000000101"), Revision: 3, LifecycleEpoch: 2, Phase: kernel.PhaseActive, Condition: kernel.ConditionRunnable},
			CriteriaRevision: 5, EvidenceSetDigest: kernel.Digest("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"), BranchPolicyRevision: 4,
			RequiredBranchIDs: []string{"tests", "review"}, PartialResultPolicy: "WAIT_ALL",
			Parents: []kernel.DagParent{{ParentEventID: kernel.UUIDv7("00000000-0000-7000-8000-000000000201"), EdgeKind: kernel.EdgeCausal}},
		},
		Identity:        application.ValidationReviewCommandIdentity{CommandID: kernel.UUIDv7("00000000-0000-7000-8000-000000000401"), CorrelationID: kernel.UUIDv7("00000000-0000-7000-8000-000000000402"), IdempotencyKey: "validation-review-1"},
		PolicyAuthority: kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "validation-policy"}, PolicyRevision: 7, CatalogueRevision: kernel.CatalogueRevision, Provenance: basis,
	}
}
