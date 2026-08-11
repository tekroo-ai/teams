package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

const EnvelopeVersion = "1.0.0"

var (
	ErrInvalidConfiguration = errors.New("invalid protocol gateway configuration")
	ErrInvalidRequest       = errors.New("invalid protocol request")
)

type CommandService interface {
	Handle(context.Context, kernel.KernelCommand, kernel.ProvenanceBasis) (kernel.CommandReceipt, error)
}

type Endpoint interface {
	Invoke(context.Context, Request) Response
}

type Command struct {
	ContractManifest          string                         `json:"contract_manifest"`
	CommandID                 kernel.UUIDv7                  `json:"command_id"`
	CommandType               string                         `json:"command_type"`
	CommandVersion            string                         `json:"command_version"`
	Target                    kernel.AggregateRef            `json:"target"`
	Authority                 kernel.PrincipalRef            `json:"authority"`
	ActorFQN                  *kernel.ActorFQN               `json:"actor_fqn"`
	Execution                 *kernel.ExecutionTuple         `json:"execution"`
	ExpectedRevision          kernel.ExpectedRevision        `json:"expected_revision"`
	Preconditions             []kernel.AggregatePrecondition `json:"preconditions"`
	ExpectedLifecycleEpoch    *uint64                        `json:"expected_lifecycle_epoch"`
	ExpectedPolicyRevision    uint64                         `json:"expected_policy_revision"`
	ExpectedCatalogueRevision uint64                         `json:"expected_catalogue_revision"`
	IdempotencyKey            string                         `json:"idempotency_key"`
	CorrelationID             kernel.UUIDv7                  `json:"correlation_id"`
	Causation                 []kernel.DagParent             `json:"causation"`
	IssuedAt                  *time.Time                     `json:"issued_at"`
	Payload                   json.RawMessage                `json:"payload"`
	EvidenceRefs              []kernel.EvidenceRef           `json:"evidence_refs"`
}

func CommandFromKernel(command kernel.KernelCommand) Command {
	return Command{
		ContractManifest: command.ContractManifest, CommandID: command.CommandID,
		CommandType: command.CommandType, CommandVersion: command.CommandVersion,
		Target: command.Target, Authority: command.Authority, ActorFQN: command.ActorFQN,
		Execution: command.Execution, ExpectedRevision: command.ExpectedRevision,
		Preconditions: command.Preconditions, ExpectedLifecycleEpoch: command.ExpectedLifecycleEpoch,
		ExpectedPolicyRevision:    command.ExpectedPolicyRevision,
		ExpectedCatalogueRevision: command.ExpectedCatalogueRevision,
		IdempotencyKey:            command.IdempotencyKey, CorrelationID: command.CorrelationID,
		Causation: command.Causation, IssuedAt: command.IssuedAt,
		Payload: command.Payload, EvidenceRefs: command.EvidenceRefs,
	}
}

func (command Command) Kernel() kernel.KernelCommand {
	return kernel.KernelCommand{
		ContractManifest: command.ContractManifest, CommandID: command.CommandID,
		CommandType: command.CommandType, CommandVersion: command.CommandVersion,
		Target: command.Target, Authority: command.Authority, ActorFQN: command.ActorFQN,
		Execution: command.Execution, ExpectedRevision: command.ExpectedRevision,
		Preconditions: command.Preconditions, ExpectedLifecycleEpoch: command.ExpectedLifecycleEpoch,
		ExpectedPolicyRevision:    command.ExpectedPolicyRevision,
		ExpectedCatalogueRevision: command.ExpectedCatalogueRevision,
		IdempotencyKey:            command.IdempotencyKey, CorrelationID: command.CorrelationID,
		Causation: command.Causation, IssuedAt: command.IssuedAt,
		Payload: command.Payload, EvidenceRefs: command.EvidenceRefs,
	}
}

type Request struct {
	EnvelopeVersion        string                 `json:"envelope_version"`
	RequestID              kernel.UUIDv7          `json:"request_id"`
	AuthenticatedPrincipal kernel.PrincipalRef    `json:"authenticated_principal"`
	AuthenticatedActorFQN  *kernel.ActorFQN       `json:"authenticated_actor_fqn"`
	AuthenticatedExecution *kernel.ExecutionTuple `json:"authenticated_execution"`
	TimeoutMillis          uint32                 `json:"timeout_millis"`
	Command                Command                `json:"command"`
	Provenance             kernel.ProvenanceBasis `json:"provenance"`
}

type ResponseStatus string

const (
	StatusReceipt           ResponseStatus = "RECEIPT"
	StatusInvocationFailure ResponseStatus = "INVOCATION_FAILURE"
)

type FailureCode string

const (
	FailureInvalidRequest FailureCode = "INVALID_REQUEST"
	FailureTimeout        FailureCode = "TIMEOUT"
	FailureCancelled      FailureCode = "CANCELLED"
	FailureService        FailureCode = "SERVICE_FAILURE"
)

type InvocationFailure struct {
	Code           FailureCode `json:"code"`
	Message        string      `json:"message"`
	OutcomeUnknown bool        `json:"outcome_unknown"`
}

type Response struct {
	EnvelopeVersion string                 `json:"envelope_version"`
	RequestID       kernel.UUIDv7          `json:"request_id"`
	Status          ResponseStatus         `json:"status"`
	Receipt         *kernel.CommandReceipt `json:"receipt"`
	Failure         *InvocationFailure     `json:"failure"`
}

type Gateway struct {
	service    CommandService
	maxTimeout time.Duration
}

func NewGateway(service CommandService, maxTimeout time.Duration) (*Gateway, error) {
	if service == nil || maxTimeout <= 0 {
		return nil, ErrInvalidConfiguration
	}
	return &Gateway{service: service, maxTimeout: maxTimeout}, nil
}

func (gateway *Gateway) Invoke(parent context.Context, request Request) Response {
	response := Response{EnvelopeVersion: EnvelopeVersion, RequestID: request.RequestID}
	if err := gateway.validate(request); err != nil {
		response.Status = StatusInvocationFailure
		response.Failure = &InvocationFailure{Code: FailureInvalidRequest, Message: err.Error(), OutcomeUnknown: false}
		return response
	}
	timeout := time.Duration(request.TimeoutMillis) * time.Millisecond
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		response.Status = StatusInvocationFailure
		code := FailureCancelled
		if errors.Is(err, context.DeadlineExceeded) {
			code = FailureTimeout
		}
		response.Failure = &InvocationFailure{Code: code, Message: err.Error(), OutcomeUnknown: false}
		return response
	}
	type serviceResult struct {
		receipt kernel.CommandReceipt
		err     error
	}
	completed := make(chan serviceResult, 1)
	go func() {
		receipt, err := gateway.service.Handle(ctx, request.Command.Kernel(), request.Provenance)
		completed <- serviceResult{receipt: receipt, err: err}
	}()
	var receipt kernel.CommandReceipt
	var err error
	select {
	case result := <-completed:
		receipt, err = result.receipt, result.err
	case <-ctx.Done():
		response.Status = StatusInvocationFailure
		code := FailureCancelled
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			code = FailureTimeout
		}
		response.Failure = &InvocationFailure{Code: code, Message: ctx.Err().Error(), OutcomeUnknown: true}
		return response
	}
	if err == nil {
		response.Status = StatusReceipt
		response.Receipt = &receipt
		return response
	}
	response.Status = StatusInvocationFailure
	failure := &InvocationFailure{Code: FailureService, Message: "command service failed after dispatch", OutcomeUnknown: true}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		failure.Code = FailureTimeout
	} else if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		failure.Code = FailureCancelled
	}
	response.Failure = failure
	return response
}

func (gateway *Gateway) validate(request Request) error {
	command := request.Command
	if request.EnvelopeVersion != EnvelopeVersion || !request.RequestID.Valid() {
		return fmt.Errorf("%w: envelope identity", ErrInvalidRequest)
	}
	if request.TimeoutMillis == 0 || time.Duration(request.TimeoutMillis)*time.Millisecond > gateway.maxTimeout {
		return fmt.Errorf("%w: timeout outside configured bound", ErrInvalidRequest)
	}
	if command.ContractManifest != kernel.ContractIdentity || !command.CommandID.Valid() || command.CommandType == "" || command.CommandVersion != kernel.SchemaVersion || !command.Target.Valid() || !command.Authority.Valid() {
		return fmt.Errorf("%w: command identity", ErrInvalidRequest)
	}
	if command.ExpectedPolicyRevision == 0 || command.ExpectedCatalogueRevision == 0 || len(command.IdempotencyKey) == 0 || len(command.IdempotencyKey) > 256 || !command.CorrelationID.Valid() {
		return fmt.Errorf("%w: command context", ErrInvalidRequest)
	}
	if (command.ExpectedRevision.MustNotExist && command.ExpectedRevision.Revision != 0) || (!command.ExpectedRevision.MustNotExist && command.ExpectedRevision.Revision == 0) {
		return fmt.Errorf("%w: expected revision", ErrInvalidRequest)
	}
	if len(command.Preconditions) > 64 || len(command.Causation) > 64 || len(command.EvidenceRefs) > 64 || !jsonObject(command.Payload) {
		return fmt.Errorf("%w: bounded command payload", ErrInvalidRequest)
	}
	if !request.AuthenticatedPrincipal.Valid() || request.AuthenticatedPrincipal != command.Authority {
		return fmt.Errorf("%w: authenticated principal mismatch", ErrInvalidRequest)
	}
	if !sameActor(request.AuthenticatedActorFQN, command.ActorFQN) || !sameExecution(request.AuthenticatedExecution, command.Execution) {
		return fmt.Errorf("%w: authenticated execution mismatch", ErrInvalidRequest)
	}
	if command.Execution != nil && command.ActorFQN == nil {
		return fmt.Errorf("%w: execution without actor", ErrInvalidRequest)
	}
	if command.ActorFQN != nil && !command.ActorFQN.Valid() {
		return fmt.Errorf("%w: actor FQN", ErrInvalidRequest)
	}
	if command.Execution != nil && !command.Execution.Valid() {
		return fmt.Errorf("%w: execution fence", ErrInvalidRequest)
	}
	if !request.Provenance.Valid() {
		return fmt.Errorf("%w: provenance basis", ErrInvalidRequest)
	}
	return nil
}

func DecodeRequest(reader io.Reader) (Request, error) {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var request Request
	if err := decoder.Decode(&request); err != nil {
		return Request{}, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Request{}, fmt.Errorf("%w: multiple JSON values", ErrInvalidRequest)
		}
		return Request{}, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	return request, nil
}

func EncodeResponse(writer io.Writer, response Response) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(response)
}

func jsonObject(payload json.RawMessage) bool {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil || value == nil {
		return false
	}
	var extra any
	return errors.Is(decoder.Decode(&extra), io.EOF)
}

func sameActor(left, right *kernel.ActorFQN) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func sameExecution(left, right *kernel.ExecutionTuple) bool {
	return reflect.DeepEqual(left, right)
}
