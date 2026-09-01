package organization

import (
	"errors"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

var ErrInvalidHumanOperation = errors.New("invalid human operation")

type HumanParticipantRegistration struct {
	Participant            kernel.PrincipalRef    `json:"participant"`
	DisplayLabel           string                 `json:"display_label"`
	RoleClass              string                 `json:"role_class"`
	RoleID                 string                 `json:"role_id"`
	ScopeKind              string                 `json:"scope_kind"`
	ScopeID                string                 `json:"scope_id"`
	AdvisoryTopicIDs       []string               `json:"advisory_topic_ids"`
	AuthorizedCommandTypes []string               `json:"authorized_command_types"`
	Channel                string                 `json:"channel"`
	Confidentiality        kernel.Confidentiality `json:"confidentiality_ceiling"`
	IdempotencyKey         string                 `json:"idempotency_key"`
}

func (registration HumanParticipantRegistration) Valid() bool {
	return registration.Participant.Kind == kernel.PrincipalHuman && registration.Participant.Valid() && registration.DisplayLabel != "" && len(registration.DisplayLabel) <= 256 && registration.RoleClass != "" && registration.RoleID != "" && registration.ScopeKind != "" && registration.ScopeID != "" && registration.Channel != "" && registration.Confidentiality.Valid() && featureKeyPattern.MatchString(registration.IdempotencyKey)
}

type HumanQuestionRequest struct {
	IdempotencyKey        string                 `json:"idempotency_key"`
	SubjectTaskID         kernel.UUIDv7          `json:"subject_task_id"`
	Recipient             kernel.PrincipalRef    `json:"recipient"`
	Question              string                 `json:"question"`
	ResponseSpecification string                 `json:"response_specification"`
	Purpose               string                 `json:"purpose"`
	DeclaredEffect        string                 `json:"declared_effect"`
	Confidentiality       kernel.Confidentiality `json:"confidentiality"`
	DeadlineAt            time.Time              `json:"deadline_at"`
}

func (request HumanQuestionRequest) Valid(now time.Time) bool {
	return featureKeyPattern.MatchString(request.IdempotencyKey) && request.SubjectTaskID.Valid() && request.Recipient.Kind == kernel.PrincipalHuman && request.Recipient.Valid() && request.Question != "" && len(request.Question) <= 64<<10 && request.ResponseSpecification != "" && len(request.ResponseSpecification) <= 16<<10 && request.Confidentiality.Valid() && request.DeadlineAt.After(now) && (request.Purpose == "ADVISORY_CONSULTATION" || request.Purpose == "AUTHORIZED_DECISION" || request.Purpose == "EVIDENTIARY_QUESTION" || request.Purpose == "REQUIREMENTS_CLARIFICATION" || request.Purpose == "ACCEPTANCE_FEEDBACK") && (request.DeclaredEffect == "ADVISORY_ONLY" || request.DeclaredEffect == "EVIDENCE_ONLY")
}

type HumanResponseInput struct {
	InteractionID          kernel.UUIDv7 `json:"interaction_id"`
	ExpectedRevision       uint64        `json:"expected_revision"`
	Response               string        `json:"response"`
	ResponseClassification string        `json:"response_classification"`
}

func (input HumanResponseInput) Valid() bool {
	return input.InteractionID.Valid() && input.ExpectedRevision > 0 && input.Response != "" && len(input.Response) <= 64<<10 && (input.ResponseClassification == "ANSWER" || input.ResponseClassification == "DECLINE" || input.ResponseClassification == "REQUEST_CLARIFICATION")
}

// HumanNotification is a durable delivery projection. Canonical authority and
// response state remain in the human-interaction aggregate.
type HumanNotification struct {
	SchemaVersion         string                       `json:"schema_version" bson:"schema_version"`
	InteractionID         kernel.UUIDv7                `json:"interaction_id" bson:"interaction_id"`
	SubjectTaskID         kernel.UUIDv7                `json:"subject_task_id" bson:"subject_task_id"`
	Recipient             kernel.PrincipalRef          `json:"recipient" bson:"recipient"`
	Question              string                       `json:"question" bson:"question"`
	ResponseSpecification string                       `json:"response_specification" bson:"response_specification"`
	DeliveryID            kernel.UUIDv7                `json:"delivery_id" bson:"delivery_id"`
	DeliveryEventID       kernel.UUIDv7                `json:"delivery_event_id" bson:"delivery_event_id"`
	State                 kernel.HumanInteractionPhase `json:"state" bson:"state"`
	Response              string                       `json:"response,omitempty" bson:"response,omitempty"`
	ResponseEventID       *kernel.UUIDv7               `json:"response_event_id,omitempty" bson:"response_event_id,omitempty"`
	CreatedAt             time.Time                    `json:"created_at" bson:"created_at"`
	UpdatedAt             time.Time                    `json:"updated_at" bson:"updated_at"`
}

func (notification HumanNotification) Valid() bool {
	return notification.SchemaVersion == "1.0.0" && notification.InteractionID.Valid() && notification.SubjectTaskID.Valid() && notification.Recipient.Kind == kernel.PrincipalHuman && notification.Recipient.Valid() && notification.Question != "" && notification.ResponseSpecification != "" && notification.DeliveryID.Valid() && notification.DeliveryEventID.Valid() && notification.State != "" && !notification.CreatedAt.IsZero() && !notification.UpdatedAt.Before(notification.CreatedAt) && (notification.ResponseEventID == nil || notification.ResponseEventID.Valid())
}
