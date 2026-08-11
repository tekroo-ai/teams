package kernel

import "encoding/json"

type CommandDefinition struct {
	TypeID            string
	Version           string
	TargetKinds       []AggregateKind
	AuthorityKinds    []PrincipalKind
	ExecutionRequired bool
	EventTypes        []string
}

type CatalogueSnapshot interface {
	ResolveCommand(commandType, version string, target AggregateKind, payload json.RawMessage) (CommandDefinition, error)
}
