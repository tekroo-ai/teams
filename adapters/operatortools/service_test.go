package operatortools_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/tekroo-ai/teams/adapters/mcp"
	"github.com/tekroo-ai/teams/adapters/mongo"
	"github.com/tekroo-ai/teams/adapters/operationalruntime"
	"github.com/tekroo-ai/teams/adapters/operatortools"
	"github.com/tekroo-ai/teams/adapters/protocol"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestRoleControlUsesExactActorAndRejectsUnknownOperation(t *testing.T) {
	backend := &fakeOrganization{}
	service, err := operatortools.New(backend)
	if err != nil {
		t.Fatal(err)
	}
	identity := protocol.AuthenticatedContext{Principal: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "operator"}}
	_, err = service.CallTool(context.Background(), identity, mcp.RoleControlToolName, json.RawMessage(`{"actor_fqn":"teams::coder-1","operation":"restart"}`))
	if err != nil || backend.restarted != "teams::coder-1" {
		t.Fatalf("restart err=%v actor=%q", err, backend.restarted)
	}
	_, err = service.CallTool(context.Background(), identity, mcp.RoleControlToolName, json.RawMessage(`{"actor_fqn":"teams::coder-1","operation":"launch-unbounded"}`))
	if !errors.Is(err, operatortools.ErrInvalidArguments) {
		t.Fatalf("unknown operation err=%v", err)
	}
	_, err = service.CallTool(context.Background(), identity, mcp.RoleControlToolName, json.RawMessage(`{"actor_fqn":"teams::coder-1","operation":"restart","extra":true}`))
	if !errors.Is(err, operatortools.ErrInvalidArguments) {
		t.Fatalf("unknown field err=%v", err)
	}
}

func TestFederationInspectionUsesFocusedOperatorSurface(t *testing.T) {
	service, err := operatortools.New(&fakeOrganization{})
	if err != nil {
		t.Fatal(err)
	}
	identity := protocol.AuthenticatedContext{Principal: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "operator"}}
	value, err := service.CallTool(context.Background(), identity, mcp.FederationInspectName, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, ok := value.(operationalruntime.FederationSnapshot)
	if !ok || snapshot.Configured {
		t.Fatalf("snapshot=%#v", value)
	}
	if _, err := service.CallTool(context.Background(), identity, mcp.FederationResolveName, json.RawMessage(`{"alias":"missing"}`)); !errors.Is(err, organization.ErrFederationUnauthorized) {
		t.Fatalf("resolve err=%v", err)
	}
	nonOperator := protocol.AuthenticatedContext{Principal: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "sme"}}
	if _, err := service.CallTool(context.Background(), nonOperator, mcp.FederationSendName, json.RawMessage(`{"alias":"remote","message":{}}`)); !errors.Is(err, operatortools.ErrInvalidArguments) {
		t.Fatalf("non-operator federation send err=%v", err)
	}
}

func TestFeatureTimingUsesDurableWorkflowMeasurement(t *testing.T) {
	service, err := operatortools.New(&fakeOrganization{})
	if err != nil {
		t.Fatal(err)
	}
	identity := protocol.AuthenticatedContext{Principal: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "operator"}}
	value, err := service.CallTool(context.Background(), identity, mcp.FeatureTimingToolName, json.RawMessage(`{"feature_id":"00000000-0000-7000-8000-000000000005"}`))
	if err != nil {
		t.Fatal(err)
	}
	timing, ok := value.(operationalruntime.FeatureWorkflowTiming)
	if !ok || timing.FeatureID != "00000000-0000-7000-8000-000000000005" || timing.Timing.PeakParallelism != 2 {
		t.Fatalf("timing=%#v", value)
	}
}

func TestFeatureClarificationUsesAuthenticatedHuman(t *testing.T) {
	backend := &fakeOrganization{}
	service, err := operatortools.New(backend)
	if err != nil {
		t.Fatal(err)
	}
	identity := protocol.AuthenticatedContext{Principal: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "operator"}}
	_, err = service.CallTool(context.Background(), identity, mcp.FeatureClarifyToolName, json.RawMessage(`{"feature_id":"00000000-0000-7000-8000-000000000005","expected_revision":2,"answers":["Use the documented syntax."]}`))
	if err != nil || backend.clarificationPrincipal != identity.Principal || backend.clarificationInput.ExpectedRevision != 2 {
		t.Fatalf("err=%v principal=%#v input=%#v", err, backend.clarificationPrincipal, backend.clarificationInput)
	}
}

func TestEventWaitToolUsesGenericAggregatePredicate(t *testing.T) {
	backend := &fakeOrganization{}
	service, err := operatortools.New(backend)
	if err != nil {
		t.Fatal(err)
	}
	identity := protocol.AuthenticatedContext{Principal: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "operator"}}
	value, err := service.CallTool(context.Background(), identity, mcp.EventWaitToolName, json.RawMessage(`{"aggregate":{"kind":"work-invocation","id":"00000000-0000-7000-8000-000000000003"},"after_revision":3,"event_types":["tekroo.event.work-invocation.terminal-recorded"],"timeout_millis":90000}`))
	if err != nil {
		t.Fatal(err)
	}
	result, ok := value.(operationalruntime.EventWaitResult)
	if !ok || result.Outcome != operationalruntime.EventWaitTimedOut || backend.waited.AfterRevision != 3 || backend.waited.TimeoutMillis != 90_000 {
		t.Fatalf("result=%#v request=%#v", value, backend.waited)
	}
	if _, err := service.CallTool(context.Background(), identity, mcp.EventWaitToolName, json.RawMessage(`{"aggregate":{"kind":"invalid","id":"00000000-0000-7000-8000-000000000003"},"timeout_millis":1}`)); !errors.Is(err, operatortools.ErrInvalidArguments) {
		t.Fatalf("invalid wait err=%v", err)
	}
	nonOperator := protocol.AuthenticatedContext{Principal: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "sme"}}
	if _, err := service.CallTool(context.Background(), nonOperator, mcp.EventWaitToolName, json.RawMessage(`{"aggregate":{"kind":"work-invocation","id":"00000000-0000-7000-8000-000000000003"},"after_revision":3,"timeout_millis":1}`)); !errors.Is(err, operatortools.ErrInvalidArguments) {
		t.Fatalf("non-operator wait err=%v", err)
	}
}

type fakeOrganization struct {
	restarted              kernel.ActorFQN
	waited                 operationalruntime.EventWaitRequest
	clarificationPrincipal kernel.PrincipalRef
	clarificationInput     organization.FeatureClarificationResponseInput
}

func (*fakeOrganization) OperatorIdentity() protocol.AuthenticatedContext {
	return protocol.AuthenticatedContext{Principal: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "operator"}}
}

func (*fakeOrganization) Status() operationalruntime.ControlStatus {
	return operationalruntime.ControlStatus{State: operationalruntime.ControlRunning}
}
func (*fakeOrganization) ReadTask(context.Context, kernel.UUIDv7) (mongo.TaskProjection, bool, error) {
	return mongo.TaskProjection{}, false, nil
}
func (*fakeOrganization) ReadStory(context.Context, kernel.UUIDv7) (mongo.StoryProjection, bool, error) {
	return mongo.StoryProjection{}, false, nil
}
func (*fakeOrganization) ReadInvocation(context.Context, kernel.UUIDv7) (operationalruntime.InvocationStatus, bool, error) {
	return operationalruntime.InvocationStatus{}, false, nil
}

func (fake *fakeOrganization) WaitForEvent(_ context.Context, request operationalruntime.EventWaitRequest) (operationalruntime.EventWaitResult, error) {
	fake.waited = request
	return operationalruntime.EventWaitResult{Outcome: operationalruntime.EventWaitTimedOut, Aggregate: request.Aggregate, AfterRevision: request.AfterRevision}, nil
}

func (*fakeOrganization) RoleRoster(context.Context) ([]organization.RoleInstanceState, error) {
	return nil, nil
}
func (*fakeOrganization) StartRole(context.Context, kernel.ActorFQN) (organization.RoleInstanceState, error) {
	return organization.RoleInstanceState{}, nil
}
func (*fakeOrganization) StopRole(context.Context, kernel.ActorFQN) (organization.RoleInstanceState, error) {
	return organization.RoleInstanceState{}, nil
}
func (fake *fakeOrganization) RestartRole(_ context.Context, actor kernel.ActorFQN) (organization.RoleInstanceState, error) {
	fake.restarted = actor
	return organization.RoleInstanceState{ActorFQN: actor}, nil
}
func (*fakeOrganization) PauseRole(context.Context, kernel.ActorFQN) (organization.RoleInstanceState, error) {
	return organization.RoleInstanceState{}, nil
}
func (*fakeOrganization) ResumeRole(context.Context, kernel.ActorFQN) (organization.RoleInstanceState, error) {
	return organization.RoleInstanceState{}, nil
}
func (*fakeOrganization) SendMessage(context.Context, organization.OrganizationalMessage) error {
	return nil
}
func (*fakeOrganization) ReadMessage(context.Context, kernel.UUIDv7) (organization.MessageClaim, bool, error) {
	return organization.MessageClaim{}, false, nil
}
func (*fakeOrganization) TraceMessages(context.Context, kernel.UUIDv7) ([]organization.MessageClaim, error) {
	return nil, nil
}
func (*fakeOrganization) DeadLetters(context.Context, kernel.ActorFQN, int64) ([]organization.MessageClaim, error) {
	return nil, nil
}
func (*fakeOrganization) SubmitFeature(context.Context, kernel.PrincipalRef, organization.FeatureRequestInput) (organization.FeatureRequest, bool, error) {
	return organization.FeatureRequest{}, true, nil
}
func (*fakeOrganization) ReadFeature(context.Context, kernel.UUIDv7) (organization.FeatureRequest, bool, error) {
	return organization.FeatureRequest{}, false, nil
}
func (fake *fakeOrganization) RespondToFeatureClarification(_ context.Context, principal kernel.PrincipalRef, _ kernel.UUIDv7, input organization.FeatureClarificationResponseInput) (organization.FeatureRequest, error) {
	fake.clarificationPrincipal = principal
	fake.clarificationInput = input
	return organization.FeatureRequest{}, nil
}
func (*fakeOrganization) ReadFeatureWorkflowTiming(_ context.Context, featureID kernel.UUIDv7) (operationalruntime.FeatureWorkflowTiming, error) {
	return operationalruntime.FeatureWorkflowTiming{FeatureID: featureID, Timing: kernel.WorkflowTiming{PeakParallelism: 2}}, nil
}
func (*fakeOrganization) ApplyFeaturePlan(context.Context, kernel.UUIDv7, uint64, organization.FeaturePlan) (organization.FeatureRequest, error) {
	return organization.FeatureRequest{}, nil
}
func (*fakeOrganization) AcceptFeature(context.Context, kernel.UUIDv7, uint64, kernel.PrincipalRef, string) (organization.FeatureRequest, error) {
	return organization.FeatureRequest{}, nil
}
func (*fakeOrganization) AcceptFeatureWithRelease(context.Context, kernel.UUIDv7, kernel.PrincipalRef, organization.FeatureAcceptanceInput) (organization.FeatureRequest, error) {
	return organization.FeatureRequest{}, nil
}
func (*fakeOrganization) RegisterHumanParticipant(context.Context, kernel.PrincipalRef, organization.HumanParticipantRegistration) (kernel.HumanParticipantSnapshot, error) {
	return kernel.HumanParticipantSnapshot{}, nil
}
func (*fakeOrganization) AskHuman(context.Context, kernel.PrincipalRef, organization.HumanQuestionRequest) (organization.HumanNotification, error) {
	return organization.HumanNotification{}, nil
}
func (*fakeOrganization) RespondToHumanQuestion(context.Context, kernel.PrincipalRef, organization.HumanResponseInput) (organization.HumanNotification, error) {
	return organization.HumanNotification{}, nil
}
func (*fakeOrganization) ReadHumanInteraction(context.Context, kernel.UUIDv7) (kernel.HumanInteractionSnapshot, error) {
	return kernel.HumanInteractionSnapshot{}, nil
}
func (*fakeOrganization) HumanNotifications(context.Context, kernel.PrincipalRef, bool) ([]organization.HumanNotification, error) {
	return nil, nil
}
func (*fakeOrganization) RoleLibraries() []organization.RoleLibraryEntry {
	return []organization.RoleLibraryEntry{{Team: "teams", Version: "1.0.0", ManifestDigest: kernel.Digest(strings.Repeat("a", 64)), Roles: []string{"operator"}}}
}
func (fake *fakeOrganization) SyncRoleLibraries() ([]organization.RoleLibraryEntry, error) {
	return fake.RoleLibraries(), nil
}
func (fake *fakeOrganization) Diagnostics(context.Context) (operationalruntime.Diagnostics, error) {
	return operationalruntime.Diagnostics{Control: fake.Status()}, nil
}
func (*fakeOrganization) RepairDeadLetter(context.Context, kernel.UUIDv7, organization.OrganizationalMessage) error {
	return nil
}
func (*fakeOrganization) RequestInvocationCancellation(context.Context, kernel.PrincipalRef, kernel.UUIDv7, operationalruntime.CancellationRequest) (operationalruntime.InvocationStatus, error) {
	return operationalruntime.InvocationStatus{}, nil
}
func (*fakeOrganization) FederationSnapshot() operationalruntime.FederationSnapshot {
	return operationalruntime.FederationSnapshot{}
}
func (*fakeOrganization) ResolveFederationAlias(context.Context, string) (organization.AliasBinding, organization.FederationRoute, error) {
	return organization.AliasBinding{}, organization.FederationRoute{}, organization.ErrFederationUnauthorized
}
func (*fakeOrganization) SendFederatedMessage(context.Context, string, organization.OrganizationalMessage) (organization.FederationDeliveryReceipt, error) {
	return organization.FederationDeliveryReceipt{}, organization.ErrFederationUnauthorized
}
