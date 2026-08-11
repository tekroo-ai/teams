package kernel

import (
	"encoding/json"
	"errors"
)

var (
	ErrUnknownCommand     = errors.New("unknown command type")
	ErrUnsupportedVersion = errors.New("unsupported command version")
	ErrInvalidTarget      = errors.New("invalid command target")
	ErrInvalidPayload     = errors.New("invalid command payload")
	ErrUnknownEvent       = errors.New("unknown event type")
)

type CommandDefinition struct {
	TypeID             string
	Version            string
	TargetKinds        []AggregateKind
	AuthorityKinds     []PrincipalKind
	ExecutionRequired  bool
	EventTypes         []string
	RootAllowed        bool
	AllowedParentEdges []EdgeKind
}

type CatalogueSnapshot interface {
	ResolveCommand(commandType, version string, target AggregateKind, payload json.RawMessage) (CommandDefinition, error)
	ResolveEvent(eventType, version string, target AggregateKind, payload json.RawMessage) (EventDefinition, error)
}

type EventDefinition struct {
	TypeID      string
	Version     string
	TargetKinds []AggregateKind
}
