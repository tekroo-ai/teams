package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"

	"github.com/tekroo-ai/teams/kernel"
)

// AuthenticatedContext is produced by trusted transport composition, never by
// decoding client-controlled command JSON.
type AuthenticatedContext struct {
	Principal kernel.PrincipalRef    `json:"principal"`
	ActorFQN  *kernel.ActorFQN       `json:"actor_fqn"`
	Execution *kernel.ExecutionTuple `json:"execution"`
}

func (identity AuthenticatedContext) Valid() bool {
	if !identity.Principal.Valid() {
		return false
	}
	if identity.ActorFQN == nil {
		return identity.Execution == nil
	}
	if !identity.ActorFQN.Valid() {
		return false
	}
	return identity.Execution == nil || identity.Execution.Valid()
}

func (identity AuthenticatedContext) Equal(other AuthenticatedContext) bool {
	return identity.Principal == other.Principal && sameActor(identity.ActorFQN, other.ActorFQN) && reflect.DeepEqual(identity.Execution, other.Execution)
}

// Invocation is the client-controlled portion of a request. Authentication is
// deliberately absent and must be injected by a trusted adapter.
type Invocation struct {
	EnvelopeVersion string                 `json:"envelope_version"`
	RequestID       kernel.UUIDv7          `json:"request_id"`
	TimeoutMillis   uint32                 `json:"timeout_millis"`
	Command         Command                `json:"command"`
	Provenance      kernel.ProvenanceBasis `json:"provenance"`
}

func (invocation Invocation) Authenticate(identity AuthenticatedContext) Request {
	return Request{
		EnvelopeVersion:        invocation.EnvelopeVersion,
		RequestID:              invocation.RequestID,
		AuthenticatedPrincipal: identity.Principal,
		AuthenticatedActorFQN:  identity.ActorFQN,
		AuthenticatedExecution: identity.Execution,
		TimeoutMillis:          invocation.TimeoutMillis,
		Command:                invocation.Command,
		Provenance:             invocation.Provenance,
	}
}

func InvocationFromRequest(request Request) Invocation {
	return Invocation{
		EnvelopeVersion: request.EnvelopeVersion,
		RequestID:       request.RequestID,
		TimeoutMillis:   request.TimeoutMillis,
		Command:         request.Command,
		Provenance:      request.Provenance,
	}
}

func DecodeInvocation(reader io.Reader) (Invocation, error) {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var invocation Invocation
	if err := decoder.Decode(&invocation); err != nil {
		return Invocation{}, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Invocation{}, fmt.Errorf("%w: multiple JSON values", ErrInvalidRequest)
		}
		return Invocation{}, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	return invocation, nil
}
