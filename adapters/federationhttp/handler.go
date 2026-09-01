package federationhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tekroo-ai/teams/organization"
)

const DefaultMaximumBodyBytes = int64(2 << 20)

var ErrInvalidConfiguration = errors.New("invalid federation HTTP configuration")

type Ingress interface {
	Accept(context.Context, organization.FederatedEnvelope, time.Time) (organization.FederationDeliveryReceipt, bool, error)
}

type Handler struct {
	ingress Ingress
	clock   func() time.Time
	maxBody int64
}

func NewHandler(ingress Ingress, clock func() time.Time, maximumBodyBytes int64) (*Handler, error) {
	if ingress == nil || clock == nil || maximumBodyBytes <= 0 || maximumBodyBytes > DefaultMaximumBodyBytes {
		return nil, ErrInvalidConfiguration
	}
	return &Handler{ingress: ingress, clock: clock, maxBody: maximumBodyBytes}, nil
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	if request.URL.Path != "/v1/federation/ingress" {
		writeError(writer, http.StatusNotFound, "NOT_FOUND")
		return
	}
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writeError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		return
	}
	if request.Header.Get("Origin") != "" {
		writeError(writer, http.StatusForbidden, "ORIGIN_FORBIDDEN")
		return
	}
	contentType := request.Header.Get("Content-Type")
	if contentType != "application/json" && !strings.HasPrefix(contentType, "application/json;") {
		writeError(writer, http.StatusUnsupportedMediaType, "JSON_REQUIRED")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, handler.maxBody)
	var envelope organization.FederatedEnvelope
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(writer, http.StatusRequestEntityTooLarge, "BODY_TOO_LARGE")
			return
		}
		writeError(writer, http.StatusBadRequest, "INVALID_ENVELOPE")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(writer, http.StatusBadRequest, "INVALID_ENVELOPE")
		return
	}
	receipt, created, err := handler.ingress.Accept(request.Context(), envelope, handler.clock().UTC())
	if err != nil {
		switch {
		case errors.Is(err, organization.ErrFederationReplayConflict):
			writeError(writer, http.StatusConflict, "REPLAY_CONFLICT")
		case errors.Is(err, organization.ErrFederationClock):
			writeError(writer, http.StatusUnprocessableEntity, "TIME_WINDOW_REJECTED")
		default:
			writeError(writer, http.StatusUnauthorized, "FEDERATION_REJECTED")
		}
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(writer, status, receipt)
}

func writeError(writer http.ResponseWriter, status int, code string) {
	writeJSON(writer, status, map[string]string{"error": code})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	raw, err := json.Marshal(value)
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	writer.WriteHeader(status)
	_, _ = io.Copy(writer, bytes.NewReader(append(raw, '\n')))
}
