package operatorhttp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tekroo-ai/teams/adapters/mongo"
	"github.com/tekroo-ai/teams/adapters/operationalruntime"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

var ErrInvalidConfiguration = errors.New("invalid operator HTTP configuration")

type Config struct {
	Service           Service
	BearerToken       string
	OperatorPrincipal kernel.PrincipalRef
	OperationTimeout  time.Duration
	MaximumBodyBytes  int64
	RequestStop       func()
}

type Service interface {
	Status() operationalruntime.ControlStatus
	Pause(context.Context) error
	Resume(context.Context) error
	Submit(context.Context, kernel.KernelCommand) (kernel.CommandReceipt, error)
	ReadTask(context.Context, kernel.UUIDv7) (mongo.TaskProjection, bool, error)
	ReadStory(context.Context, kernel.UUIDv7) (mongo.StoryProjection, bool, error)
	ReadInvocation(context.Context, kernel.UUIDv7) (operationalruntime.InvocationStatus, bool, error)
	SubmitFeature(context.Context, kernel.PrincipalRef, organization.FeatureRequestInput) (organization.FeatureRequest, bool, error)
	ReadFeature(context.Context, kernel.UUIDv7) (organization.FeatureRequest, bool, error)
	ApplyFeaturePlan(context.Context, kernel.UUIDv7, uint64, organization.FeaturePlan) (organization.FeatureRequest, error)
	AcceptFeature(context.Context, kernel.UUIDv7, uint64, kernel.PrincipalRef, string) (organization.FeatureRequest, error)
	AcceptFeatureWithRelease(context.Context, kernel.UUIDv7, kernel.PrincipalRef, organization.FeatureAcceptanceInput) (organization.FeatureRequest, error)
	RegisterHumanParticipant(context.Context, kernel.PrincipalRef, organization.HumanParticipantRegistration) (kernel.HumanParticipantSnapshot, error)
	AskHuman(context.Context, kernel.PrincipalRef, organization.HumanQuestionRequest) (organization.HumanNotification, error)
	RoleLibraries() []organization.RoleLibraryEntry
	SyncRoleLibraries() ([]organization.RoleLibraryEntry, error)
	Diagnostics(context.Context) (operationalruntime.Diagnostics, error)
	RepairDeadLetter(context.Context, kernel.UUIDv7, organization.OrganizationalMessage) error
	RespondToHumanQuestion(context.Context, kernel.PrincipalRef, organization.HumanResponseInput) (organization.HumanNotification, error)
	ReadHumanInteraction(context.Context, kernel.UUIDv7) (kernel.HumanInteractionSnapshot, error)
	HumanNotifications(context.Context, kernel.PrincipalRef, bool) ([]organization.HumanNotification, error)
	RequestInvocationCancellation(context.Context, kernel.PrincipalRef, kernel.UUIDv7, operationalruntime.CancellationRequest) (operationalruntime.InvocationStatus, error)
	FederationSnapshot() operationalruntime.FederationSnapshot
	ResolveFederationAlias(context.Context, string) (organization.AliasBinding, organization.FederationRoute, error)
	SendFederatedMessage(context.Context, string, organization.OrganizationalMessage) (organization.FederationDeliveryReceipt, error)
}

type OrganizationalService interface {
	RoleRoster(context.Context) ([]organization.RoleInstanceState, error)
	StartRole(context.Context, kernel.ActorFQN) (organization.RoleInstanceState, error)
	StopRole(context.Context, kernel.ActorFQN) (organization.RoleInstanceState, error)
	RestartRole(context.Context, kernel.ActorFQN) (organization.RoleInstanceState, error)
	PauseRole(context.Context, kernel.ActorFQN) (organization.RoleInstanceState, error)
	ResumeRole(context.Context, kernel.ActorFQN) (organization.RoleInstanceState, error)
	RoleInboxSnapshot(kernel.ActorFQN) []organization.OrganizationalMessage
	SendMessage(context.Context, organization.OrganizationalMessage) error
	ReadMessage(context.Context, kernel.UUIDv7) (organization.MessageClaim, bool, error)
	TraceMessages(context.Context, kernel.UUIDv7) ([]organization.MessageClaim, error)
	DeadLetters(context.Context, kernel.ActorFQN, int64) ([]organization.MessageClaim, error)
}

type Handler struct {
	service      Service
	organization OrganizationalService
	tokenDigest  [32]byte
	timeout      time.Duration
	maxBody      int64
	requestStop  func()
	principal    kernel.PrincipalRef
}

func NewHandler(config Config) (*Handler, error) {
	organizationService, ok := config.Service.(OrganizationalService)
	if config.Service == nil || !ok || len(config.BearerToken) < 32 || !config.OperatorPrincipal.Valid() || config.OperatorPrincipal.Kind != kernel.PrincipalHuman || config.OperationTimeout <= 0 || config.MaximumBodyBytes <= 0 || config.MaximumBodyBytes > 1<<20 || config.RequestStop == nil {
		return nil, ErrInvalidConfiguration
	}
	return &Handler{service: config.Service, organization: organizationService, tokenDigest: sha256.Sum256([]byte(config.BearerToken)), timeout: config.OperationTimeout, maxBody: config.MaximumBodyBytes, requestStop: config.RequestStop, principal: config.OperatorPrincipal}, nil
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	if request.Header.Get("Origin") != "" {
		writeError(writer, http.StatusForbidden, "ORIGIN_FORBIDDEN")
		return
	}
	if !handler.authorized(request) {
		writer.Header().Set("WWW-Authenticate", "Bearer")
		writeError(writer, http.StatusUnauthorized, "UNAUTHORIZED")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), handler.timeout)
	defer cancel()
	request = request.WithContext(ctx)
	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/health":
		handler.health(writer)
	case request.Method == http.MethodGet && request.URL.Path == "/v1/status":
		writeJSON(writer, http.StatusOK, handler.service.Status())
	case request.Method == http.MethodPost && request.URL.Path == "/v1/pause":
		handler.control(writer, request, "pause")
	case request.Method == http.MethodPost && request.URL.Path == "/v1/resume":
		handler.control(writer, request, "resume")
	case request.Method == http.MethodPost && request.URL.Path == "/v1/stop":
		if !emptyBody(request, handler.maxBody) {
			writeError(writer, http.StatusBadRequest, "BODY_MUST_BE_EMPTY")
			return
		}
		writeJSON(writer, http.StatusAccepted, map[string]string{"status": "STOP_REQUESTED"})
		handler.requestStop()
	case request.Method == http.MethodPost && request.URL.Path == "/v1/commands":
		handler.command(writer, request)
	case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/v1/tasks/"):
		handler.task(writer, request, strings.TrimPrefix(request.URL.Path, "/v1/tasks/"))
	case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/v1/stories/"):
		handler.story(writer, request, strings.TrimPrefix(request.URL.Path, "/v1/stories/"))
	case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/v1/invocations/"):
		handler.invocation(writer, request, strings.TrimPrefix(request.URL.Path, "/v1/invocations/"))
	case request.Method == http.MethodPost && strings.HasPrefix(request.URL.Path, "/v1/invocations/") && strings.HasSuffix(request.URL.Path, "/cancel"):
		handler.cancelInvocation(writer, request, strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, "/v1/invocations/"), "/cancel"))
	case request.Method == http.MethodGet && request.URL.Path == "/v1/roles":
		handler.roles(writer, request)
	case request.Method == http.MethodGet && request.URL.Path == "/v1/libraries":
		writeJSON(writer, http.StatusOK, handler.service.RoleLibraries())
	case request.Method == http.MethodPost && request.URL.Path == "/v1/libraries/sync":
		if !emptyBody(request, handler.maxBody) {
			writeError(writer, http.StatusBadRequest, "BODY_MUST_BE_EMPTY")
			return
		}
		libraries, err := handler.service.SyncRoleLibraries()
		if err != nil {
			writeError(writer, http.StatusConflict, "LIBRARY_SYNC_REJECTED")
			return
		}
		writeJSON(writer, http.StatusOK, libraries)
	case request.Method == http.MethodGet && request.URL.Path == "/v1/diagnostics":
		diagnostics, err := handler.service.Diagnostics(request.Context())
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "DIAGNOSTICS_READ_FAILED")
			return
		}
		writeJSON(writer, http.StatusOK, diagnostics)
	case request.Method == http.MethodGet && request.URL.Path == "/v1/federation":
		writeJSON(writer, http.StatusOK, handler.service.FederationSnapshot())
	case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/v1/federation/aliases/"):
		handler.resolveFederationAlias(writer, request, strings.TrimPrefix(request.URL.Path, "/v1/federation/aliases/"))
	case request.Method == http.MethodPost && request.URL.Path == "/v1/federation/messages":
		handler.sendFederatedMessage(writer, request)
	case request.Method == http.MethodPost && strings.HasPrefix(request.URL.Path, "/v1/dead-letters/") && strings.HasSuffix(request.URL.Path, "/repair"):
		handler.repairDeadLetter(writer, request, strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, "/v1/dead-letters/"), "/repair"))
	case request.Method == http.MethodPost && request.URL.Path == "/v1/features":
		handler.submitFeature(writer, request)
	case request.Method == http.MethodPost && request.URL.Path == "/v1/humans":
		handler.registerHuman(writer, request)
	case request.Method == http.MethodPost && request.URL.Path == "/v1/human-questions":
		handler.askHuman(writer, request)
	case request.Method == http.MethodGet && request.URL.Path == "/v1/human-notifications":
		handler.humanNotifications(writer, request)
	case request.Method == http.MethodPost && request.URL.Path == "/v1/human-responses":
		handler.respondHuman(writer, request)
	case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/v1/human-interactions/"):
		handler.humanInteraction(writer, request, strings.TrimPrefix(request.URL.Path, "/v1/human-interactions/"))
	case request.Method == http.MethodPost && strings.HasPrefix(request.URL.Path, "/v1/features/") && strings.HasSuffix(request.URL.Path, "/plan"):
		handler.applyFeaturePlan(writer, request, strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, "/v1/features/"), "/plan"))
	case request.Method == http.MethodPost && strings.HasPrefix(request.URL.Path, "/v1/features/") && strings.HasSuffix(request.URL.Path, "/accept"):
		handler.acceptFeature(writer, request, strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, "/v1/features/"), "/accept"))
	case request.Method == http.MethodPost && strings.HasPrefix(request.URL.Path, "/v1/features/") && strings.HasSuffix(request.URL.Path, "/release"):
		handler.releaseFeature(writer, request, strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, "/v1/features/"), "/release"))
	case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/v1/features/"):
		handler.readFeature(writer, request, strings.TrimPrefix(request.URL.Path, "/v1/features/"))
	case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/v1/roles/") && strings.HasSuffix(request.URL.Path, "/inbox"):
		handler.roleInbox(writer, strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, "/v1/roles/"), "/inbox"))
	case request.Method == http.MethodPost && strings.HasPrefix(request.URL.Path, "/v1/roles/"):
		handler.roleControl(writer, request, strings.TrimPrefix(request.URL.Path, "/v1/roles/"))
	case request.Method == http.MethodPost && request.URL.Path == "/v1/messages":
		handler.sendMessage(writer, request)
	case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/v1/messages/"):
		handler.readMessage(writer, request, strings.TrimPrefix(request.URL.Path, "/v1/messages/"))
	case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/v1/message-threads/"):
		handler.traceMessages(writer, request, strings.TrimPrefix(request.URL.Path, "/v1/message-threads/"))
	case request.Method == http.MethodGet && request.URL.Path == "/v1/dead-letters":
		handler.deadLetters(writer, request)
	default:
		writeError(writer, http.StatusNotFound, "NOT_FOUND")
	}
}

func (handler *Handler) resolveFederationAlias(writer http.ResponseWriter, request *http.Request, name string) {
	if name == "" || strings.Contains(name, "/") {
		writeError(writer, http.StatusBadRequest, "INVALID_FEDERATION_ALIAS")
		return
	}
	alias, route, err := handler.service.ResolveFederationAlias(request.Context(), name)
	if err != nil {
		writeError(writer, http.StatusNotFound, "FEDERATION_ALIAS_NOT_FOUND")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"alias": alias, "route": route})
}

func (handler *Handler) sendFederatedMessage(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Alias   string                             `json:"alias"`
		Message organization.OrganizationalMessage `json:"message"`
	}
	if decodeBody(writer, request, handler.maxBody, &input) != nil || input.Alias == "" {
		writeError(writer, http.StatusBadRequest, "INVALID_FEDERATED_MESSAGE")
		return
	}
	receipt, err := handler.service.SendFederatedMessage(request.Context(), input.Alias, input.Message)
	if err != nil {
		writeError(writer, http.StatusConflict, "FEDERATED_MESSAGE_REJECTED")
		return
	}
	writeJSON(writer, http.StatusCreated, receipt)
}

func (handler *Handler) cancelInvocation(writer http.ResponseWriter, request *http.Request, value string) {
	id := kernel.UUIDv7(value)
	var input operationalruntime.CancellationRequest
	if !id.Valid() || strings.Contains(value, "/") || decodeBody(writer, request, handler.maxBody, &input) != nil || !input.Valid() {
		writeError(writer, http.StatusBadRequest, "INVALID_CANCELLATION_REQUEST")
		return
	}
	status, err := handler.service.RequestInvocationCancellation(request.Context(), handler.principal, id, input)
	if err != nil {
		writeError(writer, http.StatusConflict, "CANCELLATION_REJECTED")
		return
	}
	writeJSON(writer, http.StatusOK, status)
}

func (handler *Handler) humanNotifications(writer http.ResponseWriter, request *http.Request) {
	openOnly := true
	if value := request.URL.Query().Get("open_only"); value != "" {
		if value != "true" && value != "false" {
			writeError(writer, http.StatusBadRequest, "INVALID_NOTIFICATION_FILTER")
			return
		}
		openOnly = value == "true"
	}
	values, err := handler.service.HumanNotifications(request.Context(), handler.principal, openOnly)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "NOTIFICATION_READ_FAILED")
		return
	}
	writeJSON(writer, http.StatusOK, values)
}

func (handler *Handler) respondHuman(writer http.ResponseWriter, request *http.Request) {
	var input organization.HumanResponseInput
	if decodeBody(writer, request, handler.maxBody, &input) != nil || !input.Valid() {
		writeError(writer, http.StatusBadRequest, "INVALID_HUMAN_RESPONSE")
		return
	}
	value, err := handler.service.RespondToHumanQuestion(request.Context(), handler.principal, input)
	if err != nil {
		writeError(writer, http.StatusConflict, "HUMAN_RESPONSE_REJECTED")
		return
	}
	writeJSON(writer, http.StatusOK, value)
}

func (handler *Handler) humanInteraction(writer http.ResponseWriter, request *http.Request, value string) {
	id := kernel.UUIDv7(value)
	if !id.Valid() || strings.Contains(value, "/") {
		writeError(writer, http.StatusBadRequest, "INVALID_HUMAN_INTERACTION_ID")
		return
	}
	interaction, err := handler.service.ReadHumanInteraction(request.Context(), id)
	if err != nil {
		writeError(writer, http.StatusNotFound, "HUMAN_INTERACTION_NOT_FOUND")
		return
	}
	if interaction.OriginPrincipal != handler.principal {
		if _, selected := interaction.Selected(handler.principal); !selected {
			writeError(writer, http.StatusNotFound, "HUMAN_INTERACTION_NOT_FOUND")
			return
		}
	}
	writeJSON(writer, http.StatusOK, interaction)
}

func (handler *Handler) repairDeadLetter(writer http.ResponseWriter, request *http.Request, value string) {
	id := kernel.UUIDv7(value)
	var successor organization.OrganizationalMessage
	if !id.Valid() || strings.Contains(value, "/") || decodeBody(writer, request, handler.maxBody, &successor) != nil || successor.Validate() != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_DEAD_LETTER_REPAIR")
		return
	}
	if err := handler.service.RepairDeadLetter(request.Context(), id, successor); err != nil {
		writeError(writer, http.StatusConflict, "DEAD_LETTER_REPAIR_REJECTED")
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{"failed_message_id": id, "successor_message_id": successor.ID})
}

func (handler *Handler) registerHuman(writer http.ResponseWriter, request *http.Request) {
	var input organization.HumanParticipantRegistration
	if err := decodeBody(writer, request, handler.maxBody, &input); err != nil || !input.Valid() {
		writeError(writer, http.StatusBadRequest, "INVALID_HUMAN_REGISTRATION")
		return
	}
	participant, err := handler.service.RegisterHumanParticipant(request.Context(), handler.principal, input)
	if err != nil {
		writeError(writer, http.StatusConflict, "HUMAN_REGISTRATION_REJECTED")
		return
	}
	writeJSON(writer, http.StatusCreated, participant)
}

func (handler *Handler) askHuman(writer http.ResponseWriter, request *http.Request) {
	var input organization.HumanQuestionRequest
	if err := decodeBody(writer, request, handler.maxBody, &input); err != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_HUMAN_QUESTION")
		return
	}
	notification, err := handler.service.AskHuman(request.Context(), handler.principal, input)
	if err != nil {
		writeError(writer, http.StatusConflict, "HUMAN_QUESTION_REJECTED")
		return
	}
	writeJSON(writer, http.StatusCreated, notification)
}

func (handler *Handler) submitFeature(writer http.ResponseWriter, request *http.Request) {
	var input organization.FeatureRequestInput
	if err := decodeBody(writer, request, handler.maxBody, &input); err != nil || input.Validate() != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_FEATURE_REQUEST")
		return
	}
	feature, created, err := handler.service.SubmitFeature(request.Context(), handler.principal, input)
	if err != nil {
		writeError(writer, http.StatusConflict, "FEATURE_SUBMISSION_FAILED")
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(writer, status, feature)
}

func (handler *Handler) readFeature(writer http.ResponseWriter, request *http.Request, value string) {
	id := kernel.UUIDv7(value)
	if !id.Valid() || strings.Contains(value, "/") {
		writeError(writer, http.StatusBadRequest, "INVALID_FEATURE_ID")
		return
	}
	feature, found, err := handler.service.ReadFeature(request.Context(), id)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "FEATURE_READ_FAILED")
		return
	}
	if !found {
		writeError(writer, http.StatusNotFound, "FEATURE_NOT_FOUND")
		return
	}
	writeJSON(writer, http.StatusOK, feature)
}

func (handler *Handler) applyFeaturePlan(writer http.ResponseWriter, request *http.Request, value string) {
	id := kernel.UUIDv7(value)
	if !id.Valid() || strings.Contains(value, "/") {
		writeError(writer, http.StatusBadRequest, "INVALID_FEATURE_ID")
		return
	}
	var input struct {
		ExpectedRevision uint64                   `json:"expected_revision"`
		Plan             organization.FeaturePlan `json:"plan"`
	}
	if err := decodeBody(writer, request, handler.maxBody, &input); err != nil || input.ExpectedRevision == 0 {
		writeError(writer, http.StatusBadRequest, "INVALID_FEATURE_PLAN")
		return
	}
	feature, err := handler.service.ApplyFeaturePlan(request.Context(), id, input.ExpectedRevision, input.Plan)
	if err != nil {
		writeError(writer, http.StatusConflict, "FEATURE_PLAN_REJECTED")
		return
	}
	writeJSON(writer, http.StatusOK, feature)
}

func (handler *Handler) acceptFeature(writer http.ResponseWriter, request *http.Request, value string) {
	id := kernel.UUIDv7(value)
	if !id.Valid() || strings.Contains(value, "/") {
		writeError(writer, http.StatusBadRequest, "INVALID_FEATURE_ID")
		return
	}
	var input struct {
		ExpectedRevision uint64 `json:"expected_revision"`
		NoReleaseReason  string `json:"no_release_reason"`
	}
	if err := decodeBody(writer, request, handler.maxBody, &input); err != nil || input.ExpectedRevision == 0 || input.NoReleaseReason == "" {
		writeError(writer, http.StatusBadRequest, "INVALID_FEATURE_ACCEPTANCE")
		return
	}
	feature, err := handler.service.AcceptFeature(request.Context(), id, input.ExpectedRevision, handler.principal, input.NoReleaseReason)
	if err != nil {
		writeError(writer, http.StatusConflict, "FEATURE_ACCEPTANCE_REJECTED")
		return
	}
	writeJSON(writer, http.StatusOK, feature)
}

func (handler *Handler) releaseFeature(writer http.ResponseWriter, request *http.Request, value string) {
	id := kernel.UUIDv7(value)
	if !id.Valid() || strings.Contains(value, "/") {
		writeError(writer, http.StatusBadRequest, "INVALID_FEATURE_ID")
		return
	}
	var input organization.FeatureAcceptanceInput
	if err := decodeBody(writer, request, handler.maxBody, &input); err != nil || input.Mode != organization.FeatureAcceptanceCode {
		writeError(writer, http.StatusBadRequest, "INVALID_FEATURE_RELEASE")
		return
	}
	feature, err := handler.service.AcceptFeatureWithRelease(request.Context(), id, handler.principal, input)
	if err != nil {
		writeError(writer, http.StatusConflict, "FEATURE_RELEASE_REJECTED")
		return
	}
	writeJSON(writer, http.StatusOK, feature)
}

func (handler *Handler) roles(writer http.ResponseWriter, request *http.Request) {
	roster, err := handler.organization.RoleRoster(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "ROLE_ROSTER_FAILED")
		return
	}
	writeJSON(writer, http.StatusOK, roster)
}

func (handler *Handler) roleInbox(writer http.ResponseWriter, value string) {
	actor := kernel.ActorFQN(value)
	if !actor.Valid() || strings.Contains(value, "/") {
		writeError(writer, http.StatusBadRequest, "INVALID_ACTOR_FQN")
		return
	}
	writeJSON(writer, http.StatusOK, handler.organization.RoleInboxSnapshot(actor))
}

func (handler *Handler) roleControl(writer http.ResponseWriter, request *http.Request, value string) {
	parts := strings.Split(value, "/")
	if len(parts) != 2 || !kernel.ActorFQN(parts[0]).Valid() || !emptyBody(request, handler.maxBody) {
		writeError(writer, http.StatusBadRequest, "INVALID_ROLE_OPERATION")
		return
	}
	actor := kernel.ActorFQN(parts[0])
	var state organization.RoleInstanceState
	var err error
	switch parts[1] {
	case "start":
		state, err = handler.organization.StartRole(request.Context(), actor)
	case "stop":
		state, err = handler.organization.StopRole(request.Context(), actor)
	case "restart":
		state, err = handler.organization.RestartRole(request.Context(), actor)
	case "pause":
		state, err = handler.organization.PauseRole(request.Context(), actor)
	case "resume":
		state, err = handler.organization.ResumeRole(request.Context(), actor)
	default:
		writeError(writer, http.StatusNotFound, "ROLE_OPERATION_NOT_FOUND")
		return
	}
	if err != nil {
		writeError(writer, http.StatusConflict, "ROLE_OPERATION_FAILED")
		return
	}
	writeJSON(writer, http.StatusOK, state)
}

func (handler *Handler) sendMessage(writer http.ResponseWriter, request *http.Request) {
	var message organization.OrganizationalMessage
	if err := decodeBody(writer, request, handler.maxBody, &message); err != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_MESSAGE_JSON")
		return
	}
	if err := handler.organization.SendMessage(request.Context(), message); err != nil {
		writeError(writer, http.StatusConflict, "MESSAGE_REJECTED")
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{"message_id": message.ID, "recipient": message.Recipient})
}

func (handler *Handler) readMessage(writer http.ResponseWriter, request *http.Request, value string) {
	id := kernel.UUIDv7(value)
	if !id.Valid() || strings.Contains(value, "/") {
		writeError(writer, http.StatusBadRequest, "INVALID_MESSAGE_ID")
		return
	}
	claim, found, err := handler.organization.ReadMessage(request.Context(), id)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "MESSAGE_READ_FAILED")
		return
	}
	if !found {
		writeError(writer, http.StatusNotFound, "MESSAGE_NOT_FOUND")
		return
	}
	writeJSON(writer, http.StatusOK, claim)
}

func (handler *Handler) traceMessages(writer http.ResponseWriter, request *http.Request, value string) {
	id := kernel.UUIDv7(value)
	if !id.Valid() || strings.Contains(value, "/") {
		writeError(writer, http.StatusBadRequest, "INVALID_THREAD_ID")
		return
	}
	trace, err := handler.organization.TraceMessages(request.Context(), id)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "MESSAGE_TRACE_FAILED")
		return
	}
	writeJSON(writer, http.StatusOK, trace)
}

func (handler *Handler) deadLetters(writer http.ResponseWriter, request *http.Request) {
	recipient := kernel.ActorFQN(request.URL.Query().Get("recipient"))
	if recipient != "" && !recipient.Valid() {
		writeError(writer, http.StatusBadRequest, "INVALID_ACTOR_FQN")
		return
	}
	letters, err := handler.organization.DeadLetters(request.Context(), recipient, 100)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "DEAD_LETTER_READ_FAILED")
		return
	}
	writeJSON(writer, http.StatusOK, letters)
}

func (handler *Handler) invocation(writer http.ResponseWriter, request *http.Request, value string) {
	id := kernel.UUIDv7(value)
	if !id.Valid() || strings.Contains(value, "/") {
		writeError(writer, http.StatusBadRequest, "INVALID_INVOCATION_ID")
		return
	}
	status, found, err := handler.service.ReadInvocation(request.Context(), id)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "READ_FAILED")
		return
	}
	if !found {
		writeError(writer, http.StatusNotFound, "INVOCATION_NOT_FOUND")
		return
	}
	writeJSON(writer, http.StatusOK, status)
}

func (handler *Handler) authorized(request *http.Request) bool {
	value := request.Header.Get("Authorization")
	if !strings.HasPrefix(value, "Bearer ") {
		return false
	}
	observed := sha256.Sum256([]byte(strings.TrimPrefix(value, "Bearer ")))
	return subtle.ConstantTimeCompare(observed[:], handler.tokenDigest[:]) == 1
}

func (handler *Handler) health(writer http.ResponseWriter) {
	status := handler.service.Status()
	switch status.State {
	case operationalruntime.ControlRunning, operationalruntime.ControlPaused:
		writeJSON(writer, http.StatusOK, map[string]any{"status": "ok", "runtime": status.State, "contract": kernel.ContractIdentity})
	default:
		writeJSON(writer, http.StatusServiceUnavailable, map[string]any{"status": "unavailable", "runtime": status.State, "contract": kernel.ContractIdentity})
	}
}

func (handler *Handler) control(writer http.ResponseWriter, request *http.Request, operation string) {
	if !emptyBody(request, handler.maxBody) {
		writeError(writer, http.StatusBadRequest, "BODY_MUST_BE_EMPTY")
		return
	}
	var err error
	if operation == "pause" {
		err = handler.service.Pause(request.Context())
	} else {
		err = handler.service.Resume(request.Context())
	}
	if err != nil {
		writeError(writer, http.StatusConflict, "CONTROL_CONFLICT")
		return
	}
	writeJSON(writer, http.StatusOK, handler.service.Status())
}

func (handler *Handler) command(writer http.ResponseWriter, request *http.Request) {
	var command kernel.KernelCommand
	if err := decodeBody(writer, request, handler.maxBody, &command); err != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_COMMAND_JSON")
		return
	}
	if command.Authority != handler.principal {
		writeError(writer, http.StatusForbidden, "COMMAND_AUTHORITY_MISMATCH")
		return
	}
	receipt, err := handler.service.Submit(request.Context(), command)
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			status = http.StatusGatewayTimeout
		}
		writeError(writer, status, "COMMAND_FAILED")
		return
	}
	writeJSON(writer, http.StatusOK, receipt)
}

func (handler *Handler) task(writer http.ResponseWriter, request *http.Request, value string) {
	id := kernel.UUIDv7(value)
	if !id.Valid() || strings.Contains(value, "/") {
		writeError(writer, http.StatusBadRequest, "INVALID_TASK_ID")
		return
	}
	projection, found, err := handler.service.ReadTask(request.Context(), id)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "READ_FAILED")
		return
	}
	if !found {
		writeError(writer, http.StatusNotFound, "TASK_NOT_FOUND")
		return
	}
	writeJSON(writer, http.StatusOK, projection)
}

func (handler *Handler) story(writer http.ResponseWriter, request *http.Request, value string) {
	id := kernel.UUIDv7(value)
	if !id.Valid() || strings.Contains(value, "/") {
		writeError(writer, http.StatusBadRequest, "INVALID_STORY_ID")
		return
	}
	projection, found, err := handler.service.ReadStory(request.Context(), id)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "READ_FAILED")
		return
	}
	if !found {
		writeError(writer, http.StatusNotFound, "STORY_NOT_FOUND")
		return
	}
	writeJSON(writer, http.StatusOK, projection)
}

func decodeBody(writer http.ResponseWriter, request *http.Request, maximum int64, target any) error {
	request.Body = http.MaxBytesReader(writer, request.Body, maximum)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON content")
	}
	return nil
}

func emptyBody(request *http.Request, maximum int64) bool {
	if request.Body == nil {
		return true
	}
	limited := io.LimitReader(request.Body, maximum+1)
	raw, err := io.ReadAll(limited)
	return err == nil && len(bytes.TrimSpace(raw)) == 0
}

func writeError(writer http.ResponseWriter, status int, code string) {
	writeJSON(writer, status, map[string]string{"error": code})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

var _ http.Handler = (*Handler)(nil)
var _ Service = (*operationalruntime.ProductionService)(nil)
