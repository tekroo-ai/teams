package channel_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/channel"
	"github.com/tekroo-ai/teams/adapters/fake"
	"github.com/tekroo-ai/teams/adapters/protocol"
	"github.com/tekroo-ai/teams/kernel"
)

type endpointFunc func(context.Context, protocol.Request) protocol.Response

func (function endpointFunc) Invoke(ctx context.Context, request protocol.Request) protocol.Response {
	return function(ctx, request)
}

type collectingSink struct {
	records []channel.QuarantineRecord
	err     error
}

func (sink *collectingSink) Store(_ context.Context, record channel.QuarantineRecord) error {
	sink.records = append(sink.records, record)
	return sink.err
}

func TestReceiverInjectsTrustedSenderAndPreservesDeclaredDAG(t *testing.T) {
	recipient := testRecipient()
	intake := testIntake()
	invocation := testInvocation(t)
	parents := []kernel.DagParent{
		{ParentEventID: uuid("00000000-0000-7000-8000-000000000031"), EdgeKind: kernel.EdgeCausal},
		{ParentEventID: uuid("00000000-0000-7000-8000-000000000032"), EdgeKind: kernel.EdgeResponse},
	}
	invocation.Command.Causation = parents
	called := false
	receiver := newReceiver(t, recipient, endpointFunc(func(_ context.Context, request protocol.Request) protocol.Response {
		called = true
		if !requestIdentity(request).Equal(intake.AuthenticatedSender) {
			t.Fatalf("authenticated identity = %#v", requestIdentity(request))
		}
		if !reflect.DeepEqual(request.Command.Causation, parents) {
			t.Fatalf("causation = %#v, want %#v", request.Command.Causation, parents)
		}
		return protocol.Response{EnvelopeVersion: protocol.EnvelopeVersion, RequestID: request.RequestID, Status: protocol.StatusReceipt}
	}), &collectingSink{}, 64*1024)

	response := receiver.Receive(context.Background(), intake, encodeFrame(t, recipient, invocation))
	if !called || response.Status != protocol.StatusReceipt {
		t.Fatalf("called=%t response=%#v", called, response)
	}
}

func TestReceiverRejectsNonExactRecipientBeforeEndpoint(t *testing.T) {
	recipient := testRecipient()
	called := false
	sink := &collectingSink{}
	receiver := newReceiver(t, recipient, endpointFunc(func(context.Context, protocol.Request) protocol.Response {
		called = true
		return protocol.Response{}
	}), sink, 64*1024)

	tests := map[string]channel.Recipient{
		"actor mismatch": {
			ActorFQN:  kernel.ActorFQN("teams::reviewer-1"),
			Execution: recipient.Execution,
		},
		"execution ID mismatch": {
			ActorFQN:  recipient.ActorFQN,
			Execution: kernel.ExecutionTuple{ExecutionID: uuid("00000000-0000-7000-8000-000000000099"), FencingEpoch: recipient.Execution.FencingEpoch},
		},
		"fencing epoch mismatch": {
			ActorFQN:  recipient.ActorFQN,
			Execution: kernel.ExecutionTuple{ExecutionID: recipient.Execution.ExecutionID, FencingEpoch: recipient.Execution.FencingEpoch + 1},
		},
	}
	for name, addressed := range tests {
		t.Run(name, func(t *testing.T) {
			response := receiver.Receive(context.Background(), testIntake(), encodeFrame(t, addressed, testInvocation(t)))
			assertKnownInvalid(t, response)
		})
	}
	if called || len(sink.records) != 0 {
		t.Fatalf("called=%t quarantine records=%d", called, len(sink.records))
	}
}

func TestReceiverRejectsRoleWildcardAndClientAssertedAuthentication(t *testing.T) {
	recipient := testRecipient()
	called := false
	sink := &collectingSink{}
	receiver := newReceiver(t, recipient, endpointFunc(func(context.Context, protocol.Request) protocol.Response {
		called = true
		return protocol.Response{}
	}), sink, 64*1024)

	for _, invalidFQN := range []kernel.ActorFQN{"teams::coder", "teams::coder-*", "*::coder-1"} {
		addressed := recipient
		addressed.ActorFQN = invalidFQN
		response := receiver.Receive(context.Background(), testIntake(), encodeFrame(t, addressed, testInvocation(t)))
		assertKnownInvalid(t, response)
	}

	var object map[string]any
	if err := json.Unmarshal(encodeFrame(t, recipient, testInvocation(t)), &object); err != nil {
		t.Fatal(err)
	}
	invocation := object["invocation"].(map[string]any)
	invocation["authenticated_principal"] = map[string]any{"kind": "HUMAN", "id": "forged"}
	forged, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	response := receiver.Receive(context.Background(), testIntake(), forged)
	assertKnownInvalid(t, response)
	if called || len(sink.records) != 4 {
		t.Fatalf("called=%t quarantine records=%d", called, len(sink.records))
	}
	for _, record := range sink.records {
		if record.Reason != channel.ReasonMalformedEnvelope {
			t.Fatalf("quarantine reason = %q", record.Reason)
		}
	}
}

func TestUnknownHistoricalTypesAndVersionsArePreservedWithoutAliases(t *testing.T) {
	recipient := testRecipient()
	called := false
	sink := &collectingSink{}
	receiver := newReceiver(t, recipient, endpointFunc(func(context.Context, protocol.Request) protocol.Response {
		called = true
		return protocol.Response{}
	}), sink, 64*1024)

	tests := []struct {
		messageType    string
		messageVersion string
		reason         channel.QuarantineReason
	}{
		{"tekroo-agent-chat", "historical", channel.ReasonUnknownMessageType},
		{"tekroo-agent-ping", "historical", channel.ReasonUnknownMessageType},
		{"tekroo-agent-pong", "historical", channel.ReasonUnknownMessageType},
		{channel.CommandType, "9.9.9", channel.ReasonUnsupportedMessageVersion},
	}
	for _, test := range tests {
		frame := testFrame(recipient, testInvocation(t))
		frame.MessageType = test.messageType
		frame.MessageVersion = test.messageVersion
		raw, err := json.Marshal(frame)
		if err != nil {
			t.Fatal(err)
		}
		response := receiver.Receive(context.Background(), testIntake(), raw)
		assertKnownInvalid(t, response)
		record := sink.records[len(sink.records)-1]
		digest := sha256.Sum256(raw)
		if !bytes.Equal(record.Raw, raw) || record.SHA256 != kernel.Digest(hex.EncodeToString(digest[:])) || record.Reason != test.reason || record.MessageType != test.messageType || record.MessageVersion != test.messageVersion {
			t.Fatalf("quarantine record = %#v", record)
		}
		if !record.AuthenticatedSender.Equal(testIntake().AuthenticatedSender) || record.ReceivedAt != testIntake().ReceivedAt || record.TransportProvenance != testIntake().TransportProvenance || record.MediaType != "application/json" {
			t.Fatalf("quarantine provenance = %#v", record)
		}
	}
	if called {
		t.Fatal("unknown input reached endpoint")
	}
}

func TestMalformedQuarantineOversizeAndSinkFailureAreObservable(t *testing.T) {
	recipient := testRecipient()
	called := false
	sink := &collectingSink{}
	receiver := newReceiver(t, recipient, endpointFunc(func(context.Context, protocol.Request) protocol.Response {
		called = true
		return protocol.Response{}
	}), sink, 64)

	raw := []byte(`{"message_type":`)
	response := receiver.Receive(context.Background(), testIntake(), raw)
	assertKnownInvalid(t, response)
	if len(sink.records) != 1 || !bytes.Equal(sink.records[0].Raw, raw) || sink.records[0].Reason != channel.ReasonMalformedEnvelope {
		t.Fatalf("malformed quarantine = %#v", sink.records)
	}
	response = receiver.Receive(context.Background(), testIntake(), make([]byte, 65))
	assertKnownInvalid(t, response)
	if len(sink.records) != 1 {
		t.Fatalf("oversize input was retained: %d records", len(sink.records))
	}

	failingSink := &collectingSink{err: errors.New("storage unavailable")}
	failing := newReceiver(t, recipient, endpointFunc(func(context.Context, protocol.Request) protocol.Response {
		called = true
		return protocol.Response{}
	}), failingSink, 64)
	response = failing.Receive(context.Background(), testIntake(), raw)
	if response.Failure == nil || response.Failure.Code != protocol.FailureService || response.Failure.OutcomeUnknown || response.Failure.Message != "channel quarantine failed" {
		t.Fatalf("quarantine failure response = %#v", response)
	}
	if called {
		t.Fatal("rejected input reached endpoint")
	}
}

func TestReceiverPreservesPostDispatchTimeoutUncertainty(t *testing.T) {
	service := serviceFunc(func(ctx context.Context, _ kernel.KernelCommand, _ kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		<-ctx.Done()
		return kernel.CommandReceipt{}, ctx.Err()
	})
	gateway, err := protocol.NewGateway(service, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	invocation := testInvocation(t)
	invocation.TimeoutMillis = 5
	receiver := newReceiver(t, testRecipient(), gateway, &collectingSink{}, 64*1024)
	response := receiver.Receive(context.Background(), testIntake(), encodeFrame(t, testRecipient(), invocation))
	if response.Failure == nil || response.Failure.Code != protocol.FailureTimeout || !response.Failure.OutcomeUnknown {
		t.Fatalf("timeout response = %#v", response)
	}
}

type serviceFunc func(context.Context, kernel.KernelCommand, kernel.ProvenanceBasis) (kernel.CommandReceipt, error)

func (function serviceFunc) Handle(ctx context.Context, command kernel.KernelCommand, provenance kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
	return function(ctx, command, provenance)
}

func newReceiver(t *testing.T, recipient channel.Recipient, endpoint protocol.Endpoint, sink channel.QuarantineSink, maxBytes int) *channel.Receiver {
	t.Helper()
	receiver, err := channel.NewReceiver(recipient, endpoint, sink, maxBytes)
	if err != nil {
		t.Fatal(err)
	}
	return receiver
}

func testRecipient() channel.Recipient {
	return channel.Recipient{
		ActorFQN: kernel.ActorFQN("teams::coder-1"),
		Execution: kernel.ExecutionTuple{
			ExecutionID:  uuid("00000000-0000-7000-8000-000000000041"),
			FencingEpoch: 7,
		},
	}
}

func testIntake() channel.IntakeContext {
	actor := kernel.ActorFQN("teams::coordinator-1")
	execution := kernel.ExecutionTuple{ExecutionID: uuid("00000000-0000-7000-8000-000000000042"), FencingEpoch: 3}
	return channel.IntakeContext{
		AuthenticatedSender: protocol.AuthenticatedContext{
			Principal: kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: string(actor)},
			ActorFQN:  &actor,
			Execution: &execution,
		},
		ReceivedAt:          time.Date(2026, 8, 11, 12, 30, 0, 0, time.UTC),
		TransportProvenance: "test-provider:delivery-17",
	}
}

func testInvocation(t *testing.T) protocol.Invocation {
	t.Helper()
	basis, err := fake.ProvenanceBasis()
	if err != nil {
		t.Fatal(err)
	}
	intake := testIntake()
	command := kernel.KernelCommand{
		ContractManifest:          kernel.ContractIdentity,
		CommandID:                 uuid("00000000-0000-7000-8000-000000000001"),
		CommandType:               "tekroo.command.story.create",
		CommandVersion:            kernel.SchemaVersion,
		Target:                    kernel.AggregateRef{Kind: kernel.AggregateStory, ID: uuid("00000000-0000-7000-8000-000000000002")},
		Authority:                 intake.AuthenticatedSender.Principal,
		ActorFQN:                  intake.AuthenticatedSender.ActorFQN,
		Execution:                 intake.AuthenticatedSender.Execution,
		ExpectedRevision:          kernel.MustNotExist(),
		ExpectedPolicyRevision:    1,
		ExpectedCatalogueRevision: kernel.CatalogueRevision,
		IdempotencyKey:            "channel-test",
		CorrelationID:             uuid("00000000-0000-7000-8000-000000000003"),
		Payload:                   json.RawMessage(`{"title":"channel test"}`),
	}
	return protocol.Invocation{
		EnvelopeVersion: protocol.EnvelopeVersion,
		RequestID:       uuid("00000000-0000-7000-8000-000000000004"),
		TimeoutMillis:   100,
		Command:         protocol.CommandFromKernel(command),
		Provenance:      basis,
	}
}

func testFrame(recipient channel.Recipient, invocation protocol.Invocation) channel.Frame {
	return channel.Frame{
		EnvelopeVersion: channel.EnvelopeVersion,
		MessageID:       uuid("00000000-0000-7000-8000-000000000005"),
		MessageType:     channel.CommandType,
		MessageVersion:  channel.CommandVersion,
		Recipient:       recipient,
		Invocation:      invocation,
	}
}

func encodeFrame(t *testing.T, recipient channel.Recipient, invocation protocol.Invocation) []byte {
	t.Helper()
	raw, err := json.Marshal(testFrame(recipient, invocation))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func requestIdentity(request protocol.Request) protocol.AuthenticatedContext {
	return protocol.AuthenticatedContext{
		Principal: request.AuthenticatedPrincipal,
		ActorFQN:  request.AuthenticatedActorFQN,
		Execution: request.AuthenticatedExecution,
	}
}

func assertKnownInvalid(t *testing.T, response protocol.Response) {
	t.Helper()
	if response.Status != protocol.StatusInvocationFailure || response.Failure == nil || response.Failure.Code != protocol.FailureInvalidRequest || response.Failure.OutcomeUnknown {
		t.Fatalf("response = %#v", response)
	}
}

func uuid(value string) kernel.UUIDv7 { return kernel.UUIDv7(value) }
