package application_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/fake"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

func TestEscalationCoordinatorOpensExactPolicyOwnedEscalation(t *testing.T) {
	request := escalationOpeningRequest(t)
	catalogue := loadCatalogue(t)
	var commands []kernel.KernelCommand
	service := executionCommandFunc(func(_ context.Context, command kernel.KernelCommand, _ kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		emits, err := catalogue.ValidateFixtureCommand(command.CommandType, command.Payload)
		if err != nil || !reflect.DeepEqual(emits, []string{"tekroo.event.escalation.opened"}) {
			t.Fatalf("frozen contract validation: emits=%v err=%v payload=%s", emits, err, command.Payload)
		}
		commands = append(commands, command)
		return kernel.CommandReceipt{OutcomeCode: kernel.OutcomeApplied, EventIDs: []kernel.UUIDv7{kernel.UUIDv7("00000000-0000-7000-8000-000000000831")}}, nil
	})
	coordinator, err := application.NewEscalationCoordinator(service)
	if err != nil {
		t.Fatal(err)
	}
	result, err := coordinator.Open(context.Background(), request)
	if err != nil || result.Decision.Status != kernel.EscalationPlanReady || result.Receipt == nil || len(commands) != 1 {
		t.Fatalf("result=%#v err=%v commands=%d", result, err, len(commands))
	}
	command := commands[0]
	if command.CommandType != "tekroo.command.escalation.open" || command.Target.Kind != kernel.AggregateEscalation || command.Target.ID != request.Input.EscalationID || !command.ExpectedRevision.MustNotExist || command.Authority != request.PolicyAuthority || command.ActorFQN != nil || command.Execution != nil {
		t.Fatalf("opening command = %#v", command)
	}
	if len(command.Preconditions) != 1 || command.Preconditions[0].Aggregate.ID != request.Input.Subject.ID || command.Preconditions[0].Expected.Revision != request.Input.Subject.Revision {
		t.Fatalf("subject precondition = %#v", command.Preconditions)
	}
	if len(command.Causation) != len(request.Input.CausalPathEventIDs) {
		t.Fatalf("causation = %#v", command.Causation)
	}
	for index, eventID := range request.Input.CausalPathEventIDs {
		if command.Causation[index] != (kernel.DagParent{ParentEventID: eventID, EdgeKind: kernel.EdgeCausal}) {
			t.Fatalf("causation[%d] = %#v", index, command.Causation[index])
		}
	}
}

func TestEscalationCoordinatorResolvesOnlyAsPlannedAuthority(t *testing.T) {
	request := escalationResolutionRequest(t)
	catalogue := loadCatalogue(t)
	var command kernel.KernelCommand
	service := executionCommandFunc(func(_ context.Context, candidate kernel.KernelCommand, _ kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		command = candidate
		emits, err := catalogue.ValidateFixtureCommand(candidate.CommandType, candidate.Payload)
		if err != nil || !reflect.DeepEqual(emits, []string{"tekroo.event.escalation.resolved"}) {
			t.Fatalf("frozen contract validation: emits=%v err=%v payload=%s", emits, err, candidate.Payload)
		}
		return kernel.CommandReceipt{OutcomeCode: kernel.OutcomeApplied, EventIDs: []kernel.UUIDv7{kernel.UUIDv7("00000000-0000-7000-8000-000000000832")}}, nil
	})
	coordinator, _ := application.NewEscalationCoordinator(service)
	result, err := coordinator.Resolve(context.Background(), request)
	if err != nil || result.Decision.Status != kernel.EscalationPlanReady || result.Receipt == nil {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if command.CommandType != "tekroo.command.escalation.resolve" || command.Target.ID != request.Input.Escalation.EscalationID || command.Authority != request.Input.Authority || command.ActorFQN != request.ActorFQN || command.Execution != request.Execution || command.ExpectedRevision.Revision != request.Input.Escalation.Revision {
		t.Fatalf("resolution command = %#v", command)
	}
	if len(command.Preconditions) != 1 || command.Preconditions[0].Expected.Revision != request.Input.Subject.Revision {
		t.Fatalf("resolution precondition = %#v", command.Preconditions)
	}
	if !hasEscalationParent(command.Causation, request.Input.Escalation.OpeningEventID, kernel.EdgeResponse) {
		t.Fatalf("opening response parent absent = %#v", command.Causation)
	}
	var payload struct {
		Reasons   []string  `json:"reasons"`
		DecidedAt time.Time `json:"decided_at"`
	}
	if err := json.Unmarshal(command.Payload, &payload); err != nil || !reflect.DeepEqual(payload.Reasons, []string{"A reason", "Z reason"}) || !payload.DecidedAt.Equal(request.Input.DecidedAt) {
		t.Fatalf("resolution payload = %s, err=%v", command.Payload, err)
	}
}

func TestEscalationCoordinatorDoesNotCommandForDuplicateOrInvalidAttribution(t *testing.T) {
	opening := escalationOpeningRequest(t)
	ref := kernel.AggregateRef{Kind: kernel.AggregateEscalation, ID: opening.Input.EscalationID}
	opening.Input.EquivalentEscalationRef = &ref
	service := executionCommandFunc(func(context.Context, kernel.KernelCommand, kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		t.Fatal("no-effect plan must not issue a command")
		return kernel.CommandReceipt{}, nil
	})
	coordinator, _ := application.NewEscalationCoordinator(service)
	result, err := coordinator.Open(context.Background(), opening)
	if err != nil || result.Decision.Reason != "EQUIVALENT_ESCALATION_EXISTS" || result.Receipt != nil {
		t.Fatalf("duplicate result=%#v err=%v", result, err)
	}

	resolution := escalationResolutionRequest(t)
	resolution.ActorFQN = nil
	if _, err := coordinator.Resolve(context.Background(), resolution); err != application.ErrInvalidEscalationCoordination {
		t.Fatalf("invalid actor attribution error = %v", err)
	}
}

func escalationOpeningRequest(t *testing.T) application.EscalationOpeningRequest {
	t.Helper()
	basis, err := fake.ProvenanceBasis()
	if err != nil {
		t.Fatal(err)
	}
	openedAt := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	return application.EscalationOpeningRequest{
		Input: kernel.EscalationOpeningInput{
			EscalationID: kernel.UUIDv7("00000000-0000-7000-8000-000000000801"),
			Subject:      kernel.AggregateState{Kind: kernel.AggregateTask, ID: kernel.UUIDv7("00000000-0000-7000-8000-000000000802"), Revision: 7, LifecycleEpoch: 2, Phase: kernel.PhaseActive, Condition: kernel.ConditionRunnable},
			Trigger:      kernel.EscalationHandoffCycleDetected, ConditionDigest: kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
			Adjudicator: kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: "teams::reviewer-1"}, TimeoutPolicy: kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "escalation-timeout-policy"},
			ResolutionOwnerFQN: kernel.ActorFQN("teams::coder-1"), OpenedAt: openedAt, DeadlineAt: openedAt.Add(time.Hour), ResolutionRoundLimit: 2, PolicyRevision: 4,
			CausalPathEventIDs: []kernel.UUIDv7{kernel.UUIDv7("00000000-0000-7000-8000-000000000806"), kernel.UUIDv7("00000000-0000-7000-8000-000000000805")},
			UnresolvedQuestion: "Which directed successor resolves the cycle?",
			EvidenceRefs:       []kernel.EvidenceRef{{EvidenceID: kernel.UUIDv7("00000000-0000-7000-8000-000000000812"), SHA256: kernel.Digest("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")}},
		},
		Identity:        application.EscalationCommandIdentity{CommandID: kernel.UUIDv7("00000000-0000-7000-8000-000000000821"), CorrelationID: kernel.UUIDv7("00000000-0000-7000-8000-000000000822"), IdempotencyKey: "escalation-open-1"},
		PolicyAuthority: kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "escalation-policy"}, PolicyRevision: 4, CatalogueRevision: kernel.CatalogueRevision, Provenance: basis,
	}
}

func escalationResolutionRequest(t *testing.T) application.EscalationResolutionRequest {
	t.Helper()
	opening := escalationOpeningRequest(t)
	planned := kernel.PlanEscalationOpening(opening.Input)
	escalation := planned.Escalation
	escalation.OpeningEventID = kernel.UUIDv7("00000000-0000-7000-8000-000000000813")
	actor := kernel.ActorFQN("teams::reviewer-1")
	execution := kernel.ExecutionTuple{ExecutionID: kernel.UUIDv7("00000000-0000-7000-8000-000000000814"), FencingEpoch: 3}
	return application.EscalationResolutionRequest{
		Input: kernel.EscalationResolutionInput{
			Escalation: escalation,
			Subject:    kernel.AggregateState{Kind: escalation.Subject.Kind, ID: escalation.Subject.ID, Revision: escalation.ExpectedSubjectRevision, LifecycleEpoch: escalation.SubjectLifecycleEpoch, Phase: kernel.PhaseActive, Condition: kernel.ConditionRunnable},
			Authority:  kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: string(actor)}, SourceRole: kernel.EscalationSourceAdjudicator, Round: 1, Outcome: kernel.EscalationResolved,
			Reasons: []string{"Z reason", "A reason"}, EvidenceRefs: []kernel.EvidenceRef{{EvidenceID: kernel.UUIDv7("00000000-0000-7000-8000-000000000815"), SHA256: kernel.Digest("cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")}},
			DecidedAt: escalation.DeadlineAt.Add(-time.Nanosecond), Parents: []kernel.DagParent{{ParentEventID: kernel.UUIDv7("00000000-0000-7000-8000-000000000816"), EdgeKind: kernel.EdgeCausal}},
		},
		Identity: application.EscalationCommandIdentity{CommandID: kernel.UUIDv7("00000000-0000-7000-8000-000000000823"), CorrelationID: kernel.UUIDv7("00000000-0000-7000-8000-000000000824"), IdempotencyKey: "escalation-resolve-1"},
		ActorFQN: &actor, Execution: &execution, PolicyRevision: 4, CatalogueRevision: kernel.CatalogueRevision, Provenance: opening.Provenance,
	}
}

func hasEscalationParent(parents []kernel.DagParent, eventID kernel.UUIDv7, edge kernel.EdgeKind) bool {
	for _, parent := range parents {
		if parent.ParentEventID == eventID && parent.EdgeKind == edge {
			return true
		}
	}
	return false
}
