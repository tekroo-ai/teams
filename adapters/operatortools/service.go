package operatortools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/tekroo-ai/teams/adapters/mcp"
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

func (service *Service) CallTool(ctx context.Context, name string, arguments json.RawMessage) (any, error) {
	if service == nil || service.organization == nil {
		return nil, ErrInvalidConfiguration
	}
	switch name {
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
