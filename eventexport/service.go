package eventexport

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/tekroo-ai/teams/adapters/protocol"
	"github.com/tekroo-ai/teams/kernel"
)

type Config struct {
	Source       Source
	Authorizer   Authorizer
	CursorKey    []byte
	CursorTTL    time.Duration
	MaximumLimit int
	MaximumWait  time.Duration
	Clock        func() time.Time
	Telemetry    Telemetry
}

type Service struct {
	source       Source
	authorizer   Authorizer
	cursorKey    []byte
	cursorTTL    time.Duration
	maximumLimit int
	maximumWait  time.Duration
	clock        func() time.Time
	telemetry    Telemetry
}

type cursorEnvelope struct {
	Version   string                        `json:"v"`
	SourceID  string                        `json:"s"`
	Identity  protocol.AuthenticatedContext `json:"i"`
	Position  string                        `json:"p"`
	ExpiresAt time.Time                     `json:"e"`
}

func NewService(config Config) (*Service, error) {
	if config.Source == nil || config.Authorizer == nil || len(config.CursorKey) < 32 || config.CursorTTL <= 0 {
		return nil, ErrInvalidRequest
	}
	if config.MaximumLimit <= 0 || config.MaximumLimit > MaximumLimit {
		config.MaximumLimit = MaximumLimit
	}
	if config.MaximumWait <= 0 || config.MaximumWait > MaximumWait {
		config.MaximumWait = MaximumWait
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	return &Service{source: config.Source, authorizer: config.Authorizer, cursorKey: append([]byte(nil), config.CursorKey...), cursorTTL: config.CursorTTL, maximumLimit: config.MaximumLimit, maximumWait: config.MaximumWait, clock: config.Clock, telemetry: config.Telemetry}, nil
}

func (service *Service) Export(parent context.Context, identity protocol.AuthenticatedContext, request Request) (response Response, err error) {
	started := service.clock()
	response = Response{ProtocolVersion: ProtocolVersion, RequestID: request.RequestID, SourceID: request.SourceID, SourceContractManifest: kernel.ContractIdentity, SourceContractManifestSHA256: SourceManifestSHA256, Records: []Record{}}
	defer func() { service.observe(response, identity, started, err) }()
	if err = service.validate(identity, request); err != nil {
		return response, err
	}
	position := []byte(nil)
	if request.Cursor != "" {
		position, err = service.decodeCursor(request.Cursor, identity, request.SourceID)
		if err != nil {
			return response, err
		}
	}
	wait := time.Duration(request.WaitMillis) * time.Millisecond
	ctx, cancel := context.WithTimeout(parent, wait)
	defer cancel()
	feed, openErr := service.source.Open(ctx, request.SourceID, position)
	if errors.Is(openErr, ErrResyncRequired) {
		response.Status = StatusResyncRequired
		response.ReasonCode = "SOURCE_CURSOR_HISTORY_LOST"
		response.NextCursor, err = service.encodeCursor(identity, request.SourceID, nil)
		return response, err
	}
	if openErr != nil {
		return response, openErr
	}
	defer func() {
		closeContext, closeCancel := context.WithTimeout(context.Background(), time.Second)
		defer closeCancel()
		_ = feed.Close(closeContext)
	}()

	for len(response.Records) < request.Limit {
		raw, nextErr := feed.Next(ctx)
		if errors.Is(nextErr, ErrCaughtUp) || errors.Is(nextErr, context.DeadlineExceeded) {
			break
		}
		if errors.Is(nextErr, ErrResyncRequired) {
			response.Status = StatusResyncRequired
			response.ReasonCode = "SOURCE_CURSOR_HISTORY_LOST"
			response.NextCursor, err = service.encodeCursor(identity, request.SourceID, nil)
			return response, err
		}
		if nextErr != nil {
			return response, nextErr
		}
		event, decodeErr := DecodeDomainEvent(raw.Bytes)
		if decodeErr != nil {
			return response, decodeErr
		}
		cursor, cursorErr := service.encodeCursor(identity, request.SourceID, raw.Position)
		if cursorErr != nil {
			return response, cursorErr
		}
		digest := sha256.Sum256(raw.Bytes)
		response.Records = append(response.Records, Record{EventID: event.EventID, ContractManifest: event.ContractManifest, EventType: event.EventType, EventVersion: event.EventVersion, EventBytesBase64: base64.StdEncoding.EncodeToString(raw.Bytes), EventBytesSHA256: kernel.Digest(hex.EncodeToString(digest[:])), EventBytesLength: len(raw.Bytes), Cursor: cursor})
	}
	response.NextCursor, err = service.encodeCursor(identity, request.SourceID, feed.Position())
	if err != nil {
		return response, err
	}
	if len(response.Records) == 0 {
		response.Status = StatusCaughtUp
	} else {
		response.Status = StatusRecords
	}
	return response, nil
}

func (service *Service) validate(identity protocol.AuthenticatedContext, request Request) error {
	if !identity.Valid() || request.ProtocolVersion != ProtocolVersion || !request.RequestID.Valid() || strings.TrimSpace(request.SourceID) != request.SourceID || request.SourceID == "" || len(request.SourceID) > 256 || request.Limit <= 0 || request.Limit > service.maximumLimit || request.WaitMillis == 0 || time.Duration(request.WaitMillis)*time.Millisecond > service.maximumWait {
		return ErrInvalidRequest
	}
	if !service.authorizer.Allow(identity, request.SourceID) {
		return ErrUnauthorized
	}
	return nil
}

func (service *Service) encodeCursor(identity protocol.AuthenticatedContext, sourceID string, position []byte) (string, error) {
	envelope := cursorEnvelope{Version: ProtocolVersion, SourceID: sourceID, Identity: identity, Position: base64.RawURLEncoding.EncodeToString(position), ExpiresAt: service.clock().Add(service.cursorTTL).UTC()}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, service.cursorKey)
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (service *Service) decodeCursor(cursor string, identity protocol.AuthenticatedContext, sourceID string) ([]byte, error) {
	parts := strings.Split(cursor, ".")
	if len(parts) != 2 {
		return nil, ErrInvalidCursor
	}
	payload, payloadErr := base64.RawURLEncoding.DecodeString(parts[0])
	signature, signatureErr := base64.RawURLEncoding.DecodeString(parts[1])
	if payloadErr != nil || signatureErr != nil {
		return nil, ErrInvalidCursor
	}
	mac := hmac.New(sha256.New, service.cursorKey)
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return nil, ErrInvalidCursor
	}
	var envelope cursorEnvelope
	if json.Unmarshal(payload, &envelope) != nil || envelope.Version != ProtocolVersion || envelope.SourceID != sourceID || !envelope.Identity.Equal(identity) || !service.clock().Before(envelope.ExpiresAt) {
		return nil, ErrInvalidCursor
	}
	position, err := base64.RawURLEncoding.DecodeString(envelope.Position)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	return position, nil
}

func (service *Service) observe(response Response, identity protocol.AuthenticatedContext, started time.Time, resultErr error) {
	if service.telemetry == nil {
		return
	}
	status := string(response.Status)
	if resultErr != nil {
		switch {
		case errors.Is(resultErr, ErrInvalidRequest):
			status = "INVALID_REQUEST"
		case errors.Is(resultErr, ErrUnauthorized):
			status = "UNAUTHORIZED"
		case errors.Is(resultErr, ErrInvalidCursor):
			status = "INVALID_CURSOR"
		case errors.Is(resultErr, context.DeadlineExceeded), errors.Is(resultErr, context.Canceled):
			status = "DEADLINE_EXCEEDED"
		default:
			status = "SOURCE_FAILURE"
		}
	}
	digest := sha256.Sum256([]byte(response.NextCursor))
	service.telemetry.Observe(TelemetryRecord{Status: status, SourceID: response.SourceID, Principal: identity.Principal, RecordCount: len(response.Records), Duration: service.clock().Sub(started), CursorDigest: kernel.Digest(hex.EncodeToString(digest[:]))})
}
