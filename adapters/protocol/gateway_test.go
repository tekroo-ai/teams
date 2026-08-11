package protocol_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/fake"
	"github.com/tekroo-ai/teams/adapters/protocol"
	"github.com/tekroo-ai/teams/kernel"
)

type serviceFunc func(context.Context, kernel.KernelCommand, kernel.ProvenanceBasis) (kernel.CommandReceipt, error)

func (function serviceFunc) Handle(ctx context.Context, command kernel.KernelCommand, provenance kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
	return function(ctx, command, provenance)
}

func TestGatewayCarriesExactAuthenticatedContextToTypedService(t *testing.T) {
	request := validRequest(t)
	actor := kernel.ActorFQN("teams::coder-1")
	execution := kernel.ExecutionTuple{ExecutionID: uuid("00000000-0000-7000-8000-000000000091"), FencingEpoch: 7}
	request.AuthenticatedActorFQN = &actor
	request.AuthenticatedExecution = &execution
	request.Command.ActorFQN = &actor
	request.Command.Execution = &execution
	called := false
	gateway := newGateway(t, serviceFunc(func(_ context.Context, command kernel.KernelCommand, provenance kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		called = true
		if command.ActorFQN == nil || *command.ActorFQN != actor || command.Execution == nil || *command.Execution != execution {
			t.Fatalf("service command identity = %#v", command)
		}
		if !provenance.Valid() {
			t.Fatal("service received invalid provenance")
		}
		return receiptFor(command), nil
	}))
	response := gateway.Invoke(context.Background(), request)
	if !called || response.Status != protocol.StatusReceipt || response.Receipt == nil || response.Failure != nil {
		t.Fatalf("response = %#v, called=%t", response, called)
	}
}

func TestGatewayRejectsIdentityFenceAndIdempotencyMismatchBeforeService(t *testing.T) {
	var calls atomic.Int32
	gateway := newGateway(t, serviceFunc(func(context.Context, kernel.KernelCommand, kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		calls.Add(1)
		return kernel.CommandReceipt{}, nil
	}))
	tests := map[string]func(*protocol.Request){
		"principal": func(request *protocol.Request) {
			request.AuthenticatedPrincipal.ID = "different"
		},
		"actor": func(request *protocol.Request) {
			actor := kernel.ActorFQN("teams::coder-1")
			request.AuthenticatedActorFQN = &actor
		},
		"idempotency": func(request *protocol.Request) {
			request.Command.IdempotencyKey = ""
		},
		"execution": func(request *protocol.Request) {
			actor := kernel.ActorFQN("teams::coder-1")
			request.AuthenticatedActorFQN = &actor
			request.Command.ActorFQN = &actor
			request.AuthenticatedExecution = &kernel.ExecutionTuple{ExecutionID: uuid("00000000-0000-7000-8000-000000000091"), FencingEpoch: 1}
			request.Command.Execution = &kernel.ExecutionTuple{ExecutionID: uuid("00000000-0000-7000-8000-000000000092"), FencingEpoch: 1}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			request := validRequest(t)
			mutate(&request)
			response := gateway.Invoke(context.Background(), request)
			if response.Status != protocol.StatusInvocationFailure || response.Failure == nil || response.Failure.Code != protocol.FailureInvalidRequest || response.Failure.OutcomeUnknown {
				t.Fatalf("response = %#v", response)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("service calls = %d, want 0", calls.Load())
	}
}

func TestGatewayTimeoutAfterDispatchRemainsExplicitlyUncertain(t *testing.T) {
	gateway := newGateway(t, serviceFunc(func(ctx context.Context, _ kernel.KernelCommand, _ kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		<-ctx.Done()
		return kernel.CommandReceipt{}, ctx.Err()
	}))
	request := validRequest(t)
	request.TimeoutMillis = 5
	response := gateway.Invoke(context.Background(), request)
	if response.Failure == nil || response.Failure.Code != protocol.FailureTimeout || !response.Failure.OutcomeUnknown || response.Receipt != nil {
		t.Fatalf("timeout response = %#v", response)
	}
}

func TestGatewayCancellationAfterDispatchRemainsExplicitlyUncertain(t *testing.T) {
	started := make(chan struct{})
	gateway := newGateway(t, serviceFunc(func(ctx context.Context, _ kernel.KernelCommand, _ kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		close(started)
		<-ctx.Done()
		return kernel.CommandReceipt{}, ctx.Err()
	}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan protocol.Response, 1)
	request := validRequest(t)
	go func() { done <- gateway.Invoke(ctx, request) }()
	<-started
	cancel()
	response := <-done
	if response.Failure == nil || response.Failure.Code != protocol.FailureCancelled || !response.Failure.OutcomeUnknown {
		t.Fatalf("cancel response = %#v", response)
	}
}

func TestGatewayCancellationBeforeDispatchIsKnownNoEffect(t *testing.T) {
	called := false
	gateway := newGateway(t, serviceFunc(func(context.Context, kernel.KernelCommand, kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		called = true
		return kernel.CommandReceipt{}, nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	response := gateway.Invoke(ctx, validRequest(t))
	if called || response.Failure == nil || response.Failure.Code != protocol.FailureCancelled || response.Failure.OutcomeUnknown {
		t.Fatalf("response = %#v, called=%t", response, called)
	}
}

func TestGatewayCompletesWhenServiceIgnoresCancellation(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	gateway := newGateway(t, serviceFunc(func(context.Context, kernel.KernelCommand, kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		<-release
		return kernel.CommandReceipt{}, errors.New("late result")
	}))
	request := validRequest(t)
	request.TimeoutMillis = 5
	started := time.Now()
	response := gateway.Invoke(context.Background(), request)
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("gateway returned after %s, want bounded completion", elapsed)
	}
	if response.Failure == nil || response.Failure.Code != protocol.FailureTimeout || !response.Failure.OutcomeUnknown {
		t.Fatalf("response = %#v", response)
	}
}

func TestGatewayDoesNotExposeServiceErrorDetails(t *testing.T) {
	gateway := newGateway(t, serviceFunc(func(context.Context, kernel.KernelCommand, kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		return kernel.CommandReceipt{}, errors.New("secret database DSN")
	}))
	response := gateway.Invoke(context.Background(), validRequest(t))
	if response.Failure == nil || response.Failure.Code != protocol.FailureService || !response.Failure.OutcomeUnknown {
		t.Fatalf("response = %#v", response)
	}
	if strings.Contains(response.Failure.Message, "secret") || response.Failure.Message != "command service failed after dispatch" {
		t.Fatalf("service failure message = %q", response.Failure.Message)
	}
}

func TestGatewayRejectsTimeoutOutsideConfiguredBound(t *testing.T) {
	gateway := newGateway(t, serviceFunc(func(context.Context, kernel.KernelCommand, kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
		t.Fatal("service must not be called")
		return kernel.CommandReceipt{}, nil
	}))
	request := validRequest(t)
	request.TimeoutMillis = 1001
	response := gateway.Invoke(context.Background(), request)
	if response.Failure == nil || response.Failure.Code != protocol.FailureInvalidRequest || response.Failure.OutcomeUnknown {
		t.Fatalf("response = %#v", response)
	}
}

func TestStrictRequestCodecRejectsUnknownOrMultipleValues(t *testing.T) {
	request := validRequest(t)
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	object["unknown"] = true
	encoded, _ = json.Marshal(object)
	if _, err := protocol.DecodeRequest(bytes.NewReader(encoded)); !errors.Is(err, protocol.ErrInvalidRequest) {
		t.Fatalf("unknown-field error = %v", err)
	}
	valid, _ := json.Marshal(request)
	if _, err := protocol.DecodeRequest(strings.NewReader(string(valid) + "\n{}")); !errors.Is(err, protocol.ErrInvalidRequest) {
		t.Fatalf("multiple-value error = %v", err)
	}
}

func validRequest(t *testing.T) protocol.Request {
	t.Helper()
	provenance, err := fake.ProvenanceBasis()
	if err != nil {
		t.Fatal(err)
	}
	principal := kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"}
	command := kernel.KernelCommand{
		ContractManifest: kernel.ContractIdentity,
		CommandID:        uuid("00000000-0000-7000-8000-000000000001"),
		CommandType:      "tekroo.command.story.create", CommandVersion: kernel.SchemaVersion,
		Target:    kernel.AggregateRef{Kind: kernel.AggregateStory, ID: uuid("00000000-0000-7000-8000-000000000002")},
		Authority: principal, ExpectedRevision: kernel.MustNotExist(),
		ExpectedPolicyRevision: 1, ExpectedCatalogueRevision: kernel.CatalogueRevision,
		IdempotencyKey: "protocol-test", CorrelationID: uuid("00000000-0000-7000-8000-000000000003"),
		Payload: json.RawMessage(`{"title":"adapter test"}`),
	}
	return protocol.Request{
		EnvelopeVersion:        protocol.EnvelopeVersion,
		RequestID:              uuid("00000000-0000-7000-8000-000000000004"),
		AuthenticatedPrincipal: principal, TimeoutMillis: 100,
		Command: protocol.CommandFromKernel(command), Provenance: provenance,
	}
}

func newGateway(t *testing.T, service protocol.CommandService) *protocol.Gateway {
	t.Helper()
	gateway, err := protocol.NewGateway(service, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return gateway
}

func receiptFor(command kernel.KernelCommand) kernel.CommandReceipt {
	return kernel.CommandReceipt{ContractManifest: kernel.ContractIdentity, CommandID: command.CommandID, CommandType: command.CommandType, Target: command.Target, OutcomeCode: kernel.OutcomeApplied}
}

func uuid(value string) kernel.UUIDv7 { return kernel.UUIDv7(value) }
