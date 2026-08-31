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
}

type Handler struct {
	service     Service
	tokenDigest [32]byte
	timeout     time.Duration
	maxBody     int64
	requestStop func()
}

func NewHandler(config Config) (*Handler, error) {
	if config.Service == nil || len(config.BearerToken) < 32 || config.OperationTimeout <= 0 || config.MaximumBodyBytes <= 0 || config.MaximumBodyBytes > 1<<20 || config.RequestStop == nil {
		return nil, ErrInvalidConfiguration
	}
	return &Handler{service: config.Service, tokenDigest: sha256.Sum256([]byte(config.BearerToken)), timeout: config.OperationTimeout, maxBody: config.MaximumBodyBytes, requestStop: config.RequestStop}, nil
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
	default:
		writeError(writer, http.StatusNotFound, "NOT_FOUND")
	}
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
