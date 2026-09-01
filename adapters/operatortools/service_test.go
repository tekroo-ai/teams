package operatortools_test

import (
	"context"
	"encoding/json"
	"errors"
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

type fakeOrganization struct {
	restarted kernel.ActorFQN
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
func (*fakeOrganization) ApplyFeaturePlan(context.Context, kernel.UUIDv7, uint64, organization.FeaturePlan) (organization.FeatureRequest, error) {
	return organization.FeatureRequest{}, nil
}
func (*fakeOrganization) AcceptFeature(context.Context, kernel.UUIDv7, uint64, kernel.PrincipalRef, string) (organization.FeatureRequest, error) {
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
