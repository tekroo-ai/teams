package operatortools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/tekroo-ai/teams/adapters/mcp"
	"github.com/tekroo-ai/teams/adapters/mongo"
	"github.com/tekroo-ai/teams/adapters/operationalruntime"
	"github.com/tekroo-ai/teams/adapters/protocol"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

var (
	ErrInvalidConfiguration = errors.New("invalid operator tool configuration")
	ErrInvalidArguments     = errors.New("invalid operator tool arguments")
	ErrUnknownTool          = errors.New("unknown operator tool")
	ErrNotFound             = errors.New("operator tool resource not found")
)

type Organization interface {
	Status() operationalruntime.ControlStatus
	ReadTask(context.Context, kernel.UUIDv7) (mongo.TaskProjection, bool, error)
	ReadStory(context.Context, kernel.UUIDv7) (mongo.StoryProjection, bool, error)
	ReadInvocation(context.Context, kernel.UUIDv7) (operationalruntime.InvocationStatus, bool, error)
	RoleRoster(context.Context) ([]organization.RoleInstanceState, error)
	StartRole(context.Context, kernel.ActorFQN) (organization.RoleInstanceState, error)
	StopRole(context.Context, kernel.ActorFQN) (organization.RoleInstanceState, error)
	RestartRole(context.Context, kernel.ActorFQN) (organization.RoleInstanceState, error)
	PauseRole(context.Context, kernel.ActorFQN) (organization.RoleInstanceState, error)
	ResumeRole(context.Context, kernel.ActorFQN) (organization.RoleInstanceState, error)
	SendMessage(context.Context, organization.OrganizationalMessage) error
	ReadMessage(context.Context, kernel.UUIDv7) (organization.MessageClaim, bool, error)
	TraceMessages(context.Context, kernel.UUIDv7) ([]organization.MessageClaim, error)
	DeadLetters(context.Context, kernel.ActorFQN, int64) ([]organization.MessageClaim, error)
	SubmitFeature(context.Context, kernel.PrincipalRef, organization.FeatureRequestInput) (organization.FeatureRequest, bool, error)
	ReadFeature(context.Context, kernel.UUIDv7) (organization.FeatureRequest, bool, error)
	ApplyFeaturePlan(context.Context, kernel.UUIDv7, uint64, organization.FeaturePlan) (organization.FeatureRequest, error)
	AcceptFeature(context.Context, kernel.UUIDv7, uint64, kernel.PrincipalRef, string) (organization.FeatureRequest, error)
	RegisterHumanParticipant(context.Context, kernel.PrincipalRef, organization.HumanParticipantRegistration) (kernel.HumanParticipantSnapshot, error)
	AskHuman(context.Context, kernel.PrincipalRef, organization.HumanQuestionRequest) (organization.HumanNotification, error)
	RespondToHumanQuestion(context.Context, kernel.PrincipalRef, organization.HumanResponseInput) (organization.HumanNotification, error)
	ReadHumanInteraction(context.Context, kernel.UUIDv7) (kernel.HumanInteractionSnapshot, error)
	HumanNotifications(context.Context, kernel.PrincipalRef, bool) ([]organization.HumanNotification, error)
}

// Service translates focused operator operations into domain calls while the
// MCP package remains a transport-only adapter.
type Service struct {
	organization Organization
}

func New(organization Organization) (*Service, error) {
	if organization == nil {
		return nil, ErrInvalidConfiguration
	}
	return &Service{organization: organization}, nil
}

func (service *Service) CallTool(ctx context.Context, identity protocol.AuthenticatedContext, name string, arguments json.RawMessage) (any, error) {
	if service == nil || service.organization == nil {
		return nil, ErrInvalidConfiguration
	}
	if !identity.Valid() || identity.Principal.Kind != kernel.PrincipalHuman || identity.ActorFQN != nil || identity.Execution != nil {
		return nil, ErrInvalidArguments
	}
	switch name {
	case mcp.StatusToolName:
		var input struct{}
		if err := decodeStrict(arguments, &input); err != nil {
			return nil, ErrInvalidArguments
		}
		return service.organization.Status(), nil
	case mcp.TaskGetToolName:
		var input struct {
			ID kernel.UUIDv7 `json:"task_id"`
		}
		if err := decodeStrict(arguments, &input); err != nil || !input.ID.Valid() {
			return nil, ErrInvalidArguments
		}
		value, found, err := service.organization.ReadTask(ctx, input.ID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, ErrNotFound
		}
		return value, nil
	case mcp.StoryGetToolName:
		var input struct {
			ID kernel.UUIDv7 `json:"story_id"`
		}
		if err := decodeStrict(arguments, &input); err != nil || !input.ID.Valid() {
			return nil, ErrInvalidArguments
		}
		value, found, err := service.organization.ReadStory(ctx, input.ID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, ErrNotFound
		}
		return value, nil
	case mcp.InvocationGetToolName:
		var input struct {
			ID kernel.UUIDv7 `json:"invocation_id"`
		}
		if err := decodeStrict(arguments, &input); err != nil || !input.ID.Valid() {
			return nil, ErrInvalidArguments
		}
		value, found, err := service.organization.ReadInvocation(ctx, input.ID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, ErrNotFound
		}
		return value, nil
	case mcp.FeatureSubmitToolName:
		var input organization.FeatureRequestInput
		if err := decodeStrict(arguments, &input); err != nil || input.Validate() != nil {
			return nil, ErrInvalidArguments
		}
		feature, created, err := service.organization.SubmitFeature(ctx, identity.Principal, input)
		if err != nil {
			return nil, err
		}
		return map[string]any{"created": created, "feature": feature}, nil
	case mcp.FeatureGetToolName:
		var input struct {
			ID kernel.UUIDv7 `json:"feature_id"`
		}
		if err := decodeStrict(arguments, &input); err != nil || !input.ID.Valid() {
			return nil, ErrInvalidArguments
		}
		feature, found, err := service.organization.ReadFeature(ctx, input.ID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, ErrNotFound
		}
		return feature, nil
	case mcp.FeaturePlanToolName:
		var input struct {
			ID               kernel.UUIDv7            `json:"feature_id"`
			ExpectedRevision uint64                   `json:"expected_revision"`
			Plan             organization.FeaturePlan `json:"plan"`
		}
		if err := decodeStrict(arguments, &input); err != nil || !input.ID.Valid() || input.ExpectedRevision == 0 {
			return nil, ErrInvalidArguments
		}
		return service.organization.ApplyFeaturePlan(ctx, input.ID, input.ExpectedRevision, input.Plan)
	case mcp.FeatureAcceptToolName:
		var input struct {
			ID               kernel.UUIDv7 `json:"feature_id"`
			ExpectedRevision uint64        `json:"expected_revision"`
			NoReleaseReason  string        `json:"no_release_reason"`
		}
		if err := decodeStrict(arguments, &input); err != nil || !input.ID.Valid() || input.ExpectedRevision == 0 || input.NoReleaseReason == "" {
			return nil, ErrInvalidArguments
		}
		return service.organization.AcceptFeature(ctx, input.ID, input.ExpectedRevision, identity.Principal, input.NoReleaseReason)
	case mcp.HumanRegisterToolName:
		var input organization.HumanParticipantRegistration
		if err := decodeStrict(arguments, &input); err != nil || !input.Valid() {
			return nil, ErrInvalidArguments
		}
		return service.organization.RegisterHumanParticipant(ctx, identity.Principal, input)
	case mcp.HumanAskToolName:
		var input organization.HumanQuestionRequest
		if err := decodeStrict(arguments, &input); err != nil {
			return nil, ErrInvalidArguments
		}
		return service.organization.AskHuman(ctx, identity.Principal, input)
	case mcp.HumanNotificationsName:
		var input struct {
			OpenOnly bool `json:"open_only,omitempty"`
		}
		if err := decodeStrict(arguments, &input); err != nil {
			return nil, ErrInvalidArguments
		}
		return service.organization.HumanNotifications(ctx, identity.Principal, input.OpenOnly)
	case mcp.HumanRespondToolName:
		var input organization.HumanResponseInput
		if err := decodeStrict(arguments, &input); err != nil || !input.Valid() {
			return nil, ErrInvalidArguments
		}
		return service.organization.RespondToHumanQuestion(ctx, identity.Principal, input)
	case mcp.HumanInteractionName:
		var input struct {
			ID kernel.UUIDv7 `json:"interaction_id"`
		}
		if err := decodeStrict(arguments, &input); err != nil || !input.ID.Valid() {
			return nil, ErrInvalidArguments
		}
		interaction, err := service.organization.ReadHumanInteraction(ctx, input.ID)
		if err != nil {
			return nil, err
		}
		if interaction.OriginPrincipal != identity.Principal {
			if _, selected := interaction.Selected(identity.Principal); !selected {
				return nil, ErrNotFound
			}
		}
		return interaction, nil
	case mcp.RolesListToolName:
		var input struct{}
		if err := decodeStrict(arguments, &input); err != nil {
			return nil, err
		}
		return service.organization.RoleRoster(ctx)
	case mcp.RoleControlToolName:
		var input struct {
			Actor     kernel.ActorFQN `json:"actor_fqn"`
			Operation string          `json:"operation"`
		}
		if err := decodeStrict(arguments, &input); err != nil || !input.Actor.Valid() {
			return nil, ErrInvalidArguments
		}
		switch input.Operation {
		case "start":
			return service.organization.StartRole(ctx, input.Actor)
		case "stop":
			return service.organization.StopRole(ctx, input.Actor)
		case "restart":
			return service.organization.RestartRole(ctx, input.Actor)
		case "pause":
			return service.organization.PauseRole(ctx, input.Actor)
		case "resume":
			return service.organization.ResumeRole(ctx, input.Actor)
		default:
			return nil, ErrInvalidArguments
		}
	case mcp.MessageSendToolName:
		var input organization.OrganizationalMessage
		if err := decodeStrict(arguments, &input); err != nil || input.Validate() != nil {
			return nil, ErrInvalidArguments
		}
		if err := service.organization.SendMessage(ctx, input); err != nil {
			return nil, err
		}
		return map[string]any{"message_id": input.ID, "recipient": input.Recipient}, nil
	case mcp.MessageGetToolName:
		var input struct {
			ID kernel.UUIDv7 `json:"message_id"`
		}
		if err := decodeStrict(arguments, &input); err != nil || !input.ID.Valid() {
			return nil, ErrInvalidArguments
		}
		claim, found, err := service.organization.ReadMessage(ctx, input.ID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, ErrNotFound
		}
		return claim, nil
	case mcp.MessageTraceToolName:
		var input struct {
			ID kernel.UUIDv7 `json:"thread_id"`
		}
		if err := decodeStrict(arguments, &input); err != nil || !input.ID.Valid() {
			return nil, ErrInvalidArguments
		}
		return service.organization.TraceMessages(ctx, input.ID)
	case mcp.DeadLettersToolName:
		var input struct {
			Recipient kernel.ActorFQN `json:"recipient,omitempty"`
		}
		if err := decodeStrict(arguments, &input); err != nil || input.Recipient != "" && !input.Recipient.Valid() {
			return nil, ErrInvalidArguments
		}
		return service.organization.DeadLetters(ctx, input.Recipient, 100)
	default:
		return nil, ErrUnknownTool
	}
}

func decodeStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return ErrInvalidArguments
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrInvalidArguments
	}
	return nil
}

var _ mcp.FocusedTools = (*Service)(nil)
