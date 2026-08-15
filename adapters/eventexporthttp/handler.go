package eventexporthttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/tekroo-ai/teams/adapters/httpapi"
	"github.com/tekroo-ai/teams/eventexport"
)

const (
	EventExportPath                = "/v1/event-export"
	DefaultEventExportMaxBodyBytes = int64(32 << 10)
)

var ErrInvalidConfiguration = errors.New("invalid event export HTTP adapter configuration")

type EventExportHandler struct {
	service       *eventexport.Service
	authenticator httpapi.Authenticator
	origins       httpapi.OriginPolicy
	limiter       httpapi.RateLimiter
	maxBodyBytes  int64
}

func NewEventExportHandler(service *eventexport.Service, authenticator httpapi.Authenticator, origins httpapi.OriginPolicy, limiter httpapi.RateLimiter, maxBodyBytes int64) (*EventExportHandler, error) {
	if service == nil || authenticator == nil || origins == nil || limiter == nil || maxBodyBytes <= 0 {
		return nil, ErrInvalidConfiguration
	}
	return &EventExportHandler{service: service, authenticator: authenticator, origins: origins, limiter: limiter, maxBodyBytes: maxBodyBytes}, nil
}

func (handler *EventExportHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	secureJSONHeaders(writer)
	if request.URL.Path != EventExportPath {
		handler.writeFailure(writer, http.StatusNotFound, eventexport.Request{}, eventexport.FailureInvalidRequest, false)
		return
	}
	if !handler.originAllowed(request) {
		handler.writeFailure(writer, http.StatusForbidden, eventexport.Request{}, eventexport.FailureUnauthorized, false)
		return
	}
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		handler.writeFailure(writer, http.StatusMethodNotAllowed, eventexport.Request{}, eventexport.FailureInvalidRequest, false)
		return
	}
	if !hasJSONContentType(request.Header.Get("Content-Type")) {
		handler.writeFailure(writer, http.StatusUnsupportedMediaType, eventexport.Request{}, eventexport.FailureInvalidRequest, false)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, handler.maxBodyBytes)
	exportRequest, err := decodeEventExportRequest(request.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			handler.writeFailure(writer, http.StatusRequestEntityTooLarge, exportRequest, eventexport.FailureInvalidRequest, false)
			return
		}
		handler.writeFailure(writer, http.StatusBadRequest, exportRequest, eventexport.FailureInvalidRequest, false)
		return
	}
	identity, err := handler.authenticator.Authenticate(request)
	if err != nil || !identity.Valid() {
		writer.Header().Set("WWW-Authenticate", "Bearer")
		handler.writeFailure(writer, http.StatusUnauthorized, exportRequest, eventexport.FailureUnauthenticated, false)
		return
	}
	if !handler.limiter.Allow(identity) {
		handler.writeFailure(writer, http.StatusTooManyRequests, exportRequest, eventexport.FailureRateLimited, true)
		return
	}
	response, err := handler.service.Export(request.Context(), identity, exportRequest)
	if err != nil {
		status, code, retryable := classifyEventExportError(err)
		handler.writeFailure(writer, status, exportRequest, code, retryable)
		return
	}
	writer.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(writer).Encode(response)
}

func (handler *EventExportHandler) originAllowed(request *http.Request) bool {
	origin := request.Header.Get("Origin")
	return origin == "" || handler.origins.AllowOrigin(origin)
}

func (handler *EventExportHandler) writeFailure(writer http.ResponseWriter, status int, requestID eventexport.Request, code eventexport.FailureCode, retryable bool) {
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(eventexport.Failure{ProtocolVersion: eventexport.ProtocolVersion, RequestID: requestID.RequestID, Code: code, Retryable: retryable})
}

func decodeEventExportRequest(reader io.Reader) (eventexport.Request, error) {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var request eventexport.Request
	if err := decoder.Decode(&request); err != nil {
		return request, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return request, eventexport.ErrInvalidRequest
		}
		return request, err
	}
	return request, nil
}

func classifyEventExportError(err error) (int, eventexport.FailureCode, bool) {
	switch {
	case errors.Is(err, eventexport.ErrInvalidRequest), errors.Is(err, eventexport.ErrInvalidCursor):
		return http.StatusBadRequest, eventexport.FailureInvalidRequest, false
	case errors.Is(err, eventexport.ErrUnauthorized):
		return http.StatusForbidden, eventexport.FailureUnauthorized, false
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return http.StatusGatewayTimeout, eventexport.FailureDeadline, true
	case errors.Is(err, eventexport.ErrCorruptEvent):
		return http.StatusBadGateway, eventexport.FailureInternal, false
	default:
		return http.StatusServiceUnavailable, eventexport.FailureSource, true
	}
}

func hasJSONContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	return err == nil && mediaType == "application/json"
}

func secureJSONHeaders(writer http.ResponseWriter) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
}
