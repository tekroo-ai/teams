// Package channel defines the provider-neutral, exact-directed channel
// boundary. Provider delivery claims remain outside this package.
package channel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"time"

	"github.com/tekroo-ai/teams/adapters/protocol"
	"github.com/tekroo-ai/teams/kernel"
)

const (
	EnvelopeVersion = "1.0.0"
	CommandType     = "tekroo.channel.command"
	CommandVersion  = "1.0.0"
)

var ErrInvalidConfiguration = errors.New("invalid channel receiver configuration")

type Recipient struct {
	ActorFQN  kernel.ActorFQN       `json:"actor_fqn"`
	Execution kernel.ExecutionTuple `json:"execution"`
}

func (recipient Recipient) Valid() bool {
	return recipient.ActorFQN.Valid() && recipient.Execution.Valid()
}

// Frame contains no authenticated sender fields. The sender is supplied by
// trusted transport composition through IntakeContext.
type Frame struct {
	EnvelopeVersion string              `json:"envelope_version"`
	MessageID       kernel.UUIDv7       `json:"message_id"`
	MessageType     string              `json:"message_type"`
	MessageVersion  string              `json:"message_version"`
	Recipient       Recipient           `json:"recipient"`
	Invocation      protocol.Invocation `json:"invocation"`
}

type IntakeContext struct {
	AuthenticatedSender protocol.AuthenticatedContext
	ReceivedAt          time.Time
	TransportProvenance string
}

func (intake IntakeContext) valid() bool {
	return intake.AuthenticatedSender.Valid() && !intake.ReceivedAt.IsZero() && len(intake.TransportProvenance) > 0 && len(intake.TransportProvenance) <= 256
}

type QuarantineReason string

const (
	ReasonUnknownMessageType        QuarantineReason = "UNKNOWN_MESSAGE_TYPE"
	ReasonUnsupportedMessageVersion QuarantineReason = "UNSUPPORTED_MESSAGE_VERSION"
	ReasonMalformedEnvelope         QuarantineReason = "MALFORMED_ENVELOPE"
)

type QuarantineRecord struct {
	Raw                 []byte
	SHA256              kernel.Digest
	AuthenticatedSender protocol.AuthenticatedContext
	ReceivedAt          time.Time
	TransportProvenance string
	Reason              QuarantineReason
	MediaType           string
	MessageType         string
	MessageVersion      string
}

type QuarantineSink interface {
	Store(context.Context, QuarantineRecord) error
}

type QuarantineSinkFunc func(context.Context, QuarantineRecord) error

func (function QuarantineSinkFunc) Store(ctx context.Context, record QuarantineRecord) error {
	return function(ctx, record)
}

type Receiver struct {
	recipient     Recipient
	endpoint      protocol.Endpoint
	quarantine    QuarantineSink
	maxFrameBytes int
}

func NewReceiver(recipient Recipient, endpoint protocol.Endpoint, quarantine QuarantineSink, maxFrameBytes int) (*Receiver, error) {
	if !recipient.Valid() || endpoint == nil || quarantine == nil || maxFrameBytes <= 0 {
		return nil, ErrInvalidConfiguration
	}
	return &Receiver{recipient: recipient, endpoint: endpoint, quarantine: quarantine, maxFrameBytes: maxFrameBytes}, nil
}

func (receiver *Receiver) Receive(ctx context.Context, intake IntakeContext, raw []byte) protocol.Response {
	response := protocol.Response{EnvelopeVersion: protocol.EnvelopeVersion}
	if !intake.valid() {
		return fail(response, protocol.FailureInvalidRequest, "invalid channel intake context", false)
	}
	if len(raw) == 0 || len(raw) > receiver.maxFrameBytes {
		return fail(response, protocol.FailureInvalidRequest, "channel frame outside configured bound", false)
	}

	var header struct {
		EnvelopeVersion string        `json:"envelope_version"`
		MessageID       kernel.UUIDv7 `json:"message_id"`
		MessageType     string        `json:"message_type"`
		MessageVersion  string        `json:"message_version"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return receiver.quarantineFailure(ctx, intake, raw, header.MessageType, header.MessageVersion, ReasonMalformedEnvelope, response)
	}
	response.RequestID = header.MessageID
	if header.EnvelopeVersion != EnvelopeVersion || !header.MessageID.Valid() || header.MessageType == "" || header.MessageVersion == "" {
		return receiver.quarantineFailure(ctx, intake, raw, header.MessageType, header.MessageVersion, ReasonMalformedEnvelope, response)
	}
	if header.MessageType != CommandType {
		return receiver.quarantineFailure(ctx, intake, raw, header.MessageType, header.MessageVersion, ReasonUnknownMessageType, response)
	}
	if header.MessageVersion != CommandVersion {
		return receiver.quarantineFailure(ctx, intake, raw, header.MessageType, header.MessageVersion, ReasonUnsupportedMessageVersion, response)
	}

	frame, err := decodeFrame(raw)
	if err != nil || frame.EnvelopeVersion != EnvelopeVersion || !frame.MessageID.Valid() || !frame.Recipient.Valid() {
		return receiver.quarantineFailure(ctx, intake, raw, header.MessageType, header.MessageVersion, ReasonMalformedEnvelope, response)
	}
	response.RequestID = frame.Invocation.RequestID
	if frame.Recipient.ActorFQN != receiver.recipient.ActorFQN || !reflect.DeepEqual(frame.Recipient.Execution, receiver.recipient.Execution) {
		return fail(response, protocol.FailureInvalidRequest, "channel recipient does not match receiver execution", false)
	}
	return receiver.endpoint.Invoke(ctx, frame.Invocation.Authenticate(intake.AuthenticatedSender))
}

func (receiver *Receiver) quarantineFailure(ctx context.Context, intake IntakeContext, raw []byte, messageType, messageVersion string, reason QuarantineReason, response protocol.Response) protocol.Response {
	digest := sha256.Sum256(raw)
	record := QuarantineRecord{
		Raw:                 bytes.Clone(raw),
		SHA256:              kernel.Digest(hex.EncodeToString(digest[:])),
		AuthenticatedSender: intake.AuthenticatedSender,
		ReceivedAt:          intake.ReceivedAt,
		TransportProvenance: intake.TransportProvenance,
		Reason:              reason,
		MediaType:           "application/json",
		MessageType:         messageType,
		MessageVersion:      messageVersion,
	}
	if err := receiver.quarantine.Store(ctx, record); err != nil {
		return fail(response, protocol.FailureService, "channel quarantine failed", false)
	}
	return fail(response, protocol.FailureInvalidRequest, "channel frame quarantined", false)
}

func decodeFrame(raw []byte) (Frame, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var frame Frame
	if err := decoder.Decode(&frame); err != nil {
		return Frame{}, fmt.Errorf("decode channel frame: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Frame{}, errors.New("decode channel frame: multiple JSON values")
		}
		return Frame{}, fmt.Errorf("decode channel frame: %w", err)
	}
	return frame, nil
}

func fail(response protocol.Response, code protocol.FailureCode, message string, outcomeUnknown bool) protocol.Response {
	response.Status = protocol.StatusInvocationFailure
	response.Failure = &protocol.InvocationFailure{Code: code, Message: message, OutcomeUnknown: outcomeUnknown}
	return response
}
