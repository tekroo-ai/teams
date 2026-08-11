package httpapi

import (
	"errors"
	"mime"
	"net/http"
	"strings"

	"github.com/tekroo-ai/teams/adapters/protocol"
)

const (
	DefaultMaxBodyBytes  int64 = 1 << 20
	OutcomeUnknownHeader       = "Tekroo-Outcome-Unknown"
)

var ErrInvalidConfiguration = errors.New("invalid HTTP adapter configuration")

type Authenticator interface {
	Authenticate(*http.Request) (protocol.AuthenticatedContext, error)
}

type AuthenticatorFunc func(*http.Request) (protocol.AuthenticatedContext, error)

func (function AuthenticatorFunc) Authenticate(request *http.Request) (protocol.AuthenticatedContext, error) {
	return function(request)
}

type OriginPolicy interface {
	AllowOrigin(string) bool
}

type OriginPolicyFunc func(string) bool

func (function OriginPolicyFunc) AllowOrigin(origin string) bool { return function(origin) }

type RateLimiter interface {
	Allow(protocol.AuthenticatedContext) bool
}

type RateLimiterFunc func(protocol.AuthenticatedContext) bool

func (function RateLimiterFunc) Allow(identity protocol.AuthenticatedContext) bool {
	return function(identity)
}

type Handler struct {
	endpoint      protocol.Endpoint
	authenticator Authenticator
	origins       OriginPolicy
	limiter       RateLimiter
	maxBodyBytes  int64
}

func NewHandler(endpoint protocol.Endpoint, authenticator Authenticator, origins OriginPolicy, limiter RateLimiter, maxBodyBytes int64) (*Handler, error) {
	if endpoint == nil || authenticator == nil || origins == nil || limiter == nil || maxBodyBytes <= 0 {
		return nil, ErrInvalidConfiguration
	}
	return &Handler{endpoint: endpoint, authenticator: authenticator, origins: origins, limiter: limiter, maxBodyBytes: maxBodyBytes}, nil
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	secureJSONHeaders(writer)
	if !handler.originAllowed(request) {
		writeFailure(writer, http.StatusForbidden, protocol.FailureInvalidRequest, "origin is not allowed", false)
		return
	}
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writeFailure(writer, http.StatusMethodNotAllowed, protocol.FailureInvalidRequest, "method must be POST", false)
		return
	}
	if !hasJSONContentType(request.Header.Get("Content-Type")) {
		writeFailure(writer, http.StatusUnsupportedMediaType, protocol.FailureInvalidRequest, "content type must be application/json", false)
		return
	}
	identity, err := handler.authenticator.Authenticate(request)
	if err != nil || !identity.Valid() {
		writer.Header().Set("WWW-Authenticate", "Bearer")
		writeFailure(writer, http.StatusUnauthorized, protocol.FailureInvalidRequest, "authentication failed", false)
		return
	}
	if !handler.limiter.Allow(identity) {
		writeFailure(writer, http.StatusTooManyRequests, protocol.FailureInvalidRequest, "rate limit exceeded", false)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, handler.maxBodyBytes)
	invocation, err := protocol.DecodeInvocation(request.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeFailure(writer, http.StatusRequestEntityTooLarge, protocol.FailureInvalidRequest, "request body exceeds configured bound", false)
			return
		}
		writeFailure(writer, http.StatusBadRequest, protocol.FailureInvalidRequest, err.Error(), false)
		return
	}
	response := handler.endpoint.Invoke(request.Context(), invocation.Authenticate(identity))
	status := responseStatus(response)
	if response.Failure != nil && response.Failure.OutcomeUnknown {
		writer.Header().Set(OutcomeUnknownHeader, "true")
	}
	writer.WriteHeader(status)
	_ = protocol.EncodeResponse(writer, response)
}

func (handler *Handler) originAllowed(request *http.Request) bool {
	origin := request.Header.Get("Origin")
	return origin == "" || handler.origins.AllowOrigin(origin)
}

func hasJSONContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	return err == nil && mediaType == "application/json"
}

func responseStatus(response protocol.Response) int {
	if response.Status == protocol.StatusReceipt {
		return http.StatusOK
	}
	if response.Failure == nil {
		return http.StatusBadGateway
	}
	switch response.Failure.Code {
	case protocol.FailureInvalidRequest:
		return http.StatusBadRequest
	case protocol.FailureTimeout:
		return http.StatusGatewayTimeout
	case protocol.FailureCancelled:
		return http.StatusRequestTimeout
	default:
		return http.StatusBadGateway
	}
}

func secureJSONHeaders(writer http.ResponseWriter) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
}

func writeFailure(writer http.ResponseWriter, status int, code protocol.FailureCode, message string, outcomeUnknown bool) {
	response := protocol.Response{
		EnvelopeVersion: protocol.EnvelopeVersion,
		Status:          protocol.StatusInvocationFailure,
		Failure:         &protocol.InvocationFailure{Code: code, Message: strings.TrimSpace(message), OutcomeUnknown: outcomeUnknown},
	}
	writer.WriteHeader(status)
	_ = protocol.EncodeResponse(writer, response)
}
