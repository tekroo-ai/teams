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
	Service          Service
	BearerToken      string
	OperationTimeout time.Duration
	MaximumBodyBytes int64
	RequestStop      func()
}

type Service interface {
	Status() operationalruntime.ControlStatus
	Pause(context.Context) error
	Resume(context.Context) error
	Submit(context.Context, kernel.KernelCommand) (kernel.CommandReceipt, error)
	ReadTask(context.Context, kernel.UUIDv7) (mongo.TaskProjection, bool, error)
	ReadStory(context.Context, kernel.UUIDv7) (mongo.StoryProjection, bool, error)
	ReadInvocation(context.Context, kernel.UUIDv7) (operationalruntime.InvocationStatus, bool, error)
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
}

func NewHandler(config Config) (*Handler, error) {
	organizationService, ok := config.Service.(OrganizationalService)
	if config.Service == nil || !ok || len(config.BearerToken) < 32 || config.OperationTimeout <= 0 || config.MaximumBodyBytes <= 0 || config.MaximumBodyBytes > 1<<20 || config.RequestStop == nil {
		return nil, ErrInvalidConfiguration
	}
	return &Handler{service: config.Service, organization: organizationService, tokenDigest: sha256.Sum256([]byte(config.BearerToken)), timeout: config.OperationTimeout, maxBody: config.MaximumBodyBytes, requestStop: config.RequestStop}, nil
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
	case request.Method == http.MethodGet && request.URL.Path == "/v1/roles":
		handler.roles(writer, request)
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
