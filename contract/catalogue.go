package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"reflect"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/tekroo-ai/teams/kernel"
)

var ErrInvalidCommand = errors.New("invalid command")

type catalogueDocument struct {
	ContractIdentity string           `json:"contractIdentity"`
	Entries          []catalogueEntry `json:"entries"`
}

type catalogueEntry struct {
	TypeID             string                 `json:"typeId"`
	Version            string                 `json:"version"`
	Kind               string                 `json:"kind"`
	Lifecycle          string                 `json:"lifecycle"`
	TargetKinds        []kernel.AggregateKind `json:"targetKinds"`
	AuthorityKinds     []kernel.PrincipalKind `json:"authorityKinds"`
	ExecutionRequired  bool                   `json:"executionRequired"`
	Emits              []string               `json:"emits"`
	PayloadSchema      string                 `json:"payloadSchema"`
	RootAllowed        bool                   `json:"rootAllowed"`
	AllowedParentEdges []kernel.EdgeKind      `json:"allowedParentEdges"`
}

type payloadDocument struct {
	Definitions map[string]map[string]any `json:"$defs"`
}

type Catalogue struct {
	commands    map[string]catalogueEntry
	events      map[string]catalogueEntry
	payloadDefs map[string]map[string]any
}

func Load(fsys fs.FS, packageRoot string) (*Catalogue, error) {
	catalogueBytes, err := fs.ReadFile(fsys, packageRoot+"/catalogue/kernel-catalogue.json")
	if err != nil {
		return nil, fmt.Errorf("read catalogue: %w", err)
	}
	payloadBytes, err := fs.ReadFile(fsys, packageRoot+"/schemas/payloads.schema.json")
	if err != nil {
		return nil, fmt.Errorf("read payload schemas: %w", err)
	}

	var document catalogueDocument
	if err := decodeJSON(catalogueBytes, &document); err != nil {
		return nil, fmt.Errorf("decode catalogue: %w", err)
	}
	if document.ContractIdentity != kernel.ContractIdentity {
		return nil, fmt.Errorf("unexpected contract identity %q", document.ContractIdentity)
	}
	var payloads payloadDocument
	if err := decodeJSON(payloadBytes, &payloads); err != nil {
		return nil, fmt.Errorf("decode payload schemas: %w", err)
	}

	commands := make(map[string]catalogueEntry, len(document.Entries)/2)
	events := make(map[string]catalogueEntry, len(document.Entries)/2)
	for _, entry := range document.Entries {
		if entry.Lifecycle != "ACTIVE" {
			continue
		}
		switch entry.Kind {
		case "COMMAND":
			if _, duplicate := commands[entry.TypeID]; duplicate {
				return nil, fmt.Errorf("duplicate command type %q", entry.TypeID)
			}
			commands[entry.TypeID] = entry
		case "EVENT":
			if _, duplicate := events[entry.TypeID]; duplicate {
				return nil, fmt.Errorf("duplicate event type %q", entry.TypeID)
			}
			events[entry.TypeID] = entry
		}
	}
	return &Catalogue{commands: commands, events: events, payloadDefs: payloads.Definitions}, nil
}

func (c *Catalogue) ResolveCommand(commandType, version string, target kernel.AggregateKind, payload json.RawMessage) (kernel.CommandDefinition, error) {
	entry, found := c.commands[commandType]
	if !found {
		return kernel.CommandDefinition{}, fmt.Errorf("%w: %w", ErrInvalidCommand, kernel.ErrUnknownCommand)
	}
	if entry.Version != version {
		return kernel.CommandDefinition{}, fmt.Errorf("%w: %w", ErrInvalidCommand, kernel.ErrUnsupportedVersion)
	}
	if !containsTarget(entry.TargetKinds, target) {
		return kernel.CommandDefinition{}, fmt.Errorf("%w: %w: kind %q", ErrInvalidCommand, kernel.ErrInvalidTarget, target)
	}
	definitionName, found := strings.CutPrefix(entry.PayloadSchema, "schemas/payloads.schema.json#/$defs/")
	if !found {
		return kernel.CommandDefinition{}, fmt.Errorf("%w: unsupported payload schema reference", ErrInvalidCommand)
	}
	rule, found := c.payloadDefs[definitionName]
	if !found {
		return kernel.CommandDefinition{}, fmt.Errorf("%w: payload schema is missing", ErrInvalidCommand)
	}
	var value any
	if err := decodeJSON(payload, &value); err != nil {
		return kernel.CommandDefinition{}, fmt.Errorf("%w: %w: malformed JSON", ErrInvalidCommand, kernel.ErrInvalidPayload)
	}
	if err := validateValue(value, rule); err != nil {
		return kernel.CommandDefinition{}, fmt.Errorf("%w: %w: %v", ErrInvalidCommand, kernel.ErrInvalidPayload, err)
	}
	return kernel.CommandDefinition{
		TypeID:             entry.TypeID,
		Version:            entry.Version,
		TargetKinds:        append([]kernel.AggregateKind(nil), entry.TargetKinds...),
		AuthorityKinds:     append([]kernel.PrincipalKind(nil), entry.AuthorityKinds...),
		ExecutionRequired:  entry.ExecutionRequired,
		EventTypes:         append([]string(nil), entry.Emits...),
		RootAllowed:        entry.RootAllowed,
		AllowedParentEdges: append([]kernel.EdgeKind(nil), entry.AllowedParentEdges...),
	}, nil
}

func (c *Catalogue) ResolveEvent(eventType, version string, target kernel.AggregateKind, payload json.RawMessage) (kernel.EventDefinition, error) {
	entry, found := c.events[eventType]
	if !found {
		return kernel.EventDefinition{}, kernel.ErrUnknownEvent
	}
	if entry.Version != version {
		return kernel.EventDefinition{}, kernel.ErrUnsupportedVersion
	}
	if !containsTarget(entry.TargetKinds, target) {
		return kernel.EventDefinition{}, kernel.ErrInvalidTarget
	}
	definitionName, found := strings.CutPrefix(entry.PayloadSchema, "schemas/payloads.schema.json#/$defs/")
	if !found {
		return kernel.EventDefinition{}, kernel.ErrInvalidPayload
	}
	var value any
	if err := decodeJSON(payload, &value); err != nil {
		return kernel.EventDefinition{}, fmt.Errorf("%w: malformed JSON", kernel.ErrInvalidPayload)
	}
	if err := validateValue(value, c.payloadDefs[definitionName]); err != nil {
		return kernel.EventDefinition{}, fmt.Errorf("%w: %v", kernel.ErrInvalidPayload, err)
	}
	return kernel.EventDefinition{TypeID: entry.TypeID, Version: entry.Version, TargetKinds: append([]kernel.AggregateKind(nil), entry.TargetKinds...)}, nil
}

func (c *Catalogue) ValidateFixtureCommand(commandType string, payload json.RawMessage) ([]string, error) {
	entry, found := c.commands[commandType]
	if !found {
		return nil, fmt.Errorf("%w: unknown command", ErrInvalidCommand)
	}
	definitionName, found := strings.CutPrefix(entry.PayloadSchema, "schemas/payloads.schema.json#/$defs/")
	if !found {
		return nil, fmt.Errorf("%w: unsupported payload schema reference", ErrInvalidCommand)
	}
	var value any
	if err := decodeJSON(payload, &value); err != nil {
		return nil, fmt.Errorf("%w: malformed payload", ErrInvalidCommand)
	}
	if err := validateValue(value, c.payloadDefs[definitionName]); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCommand, err)
	}
	return append([]string(nil), entry.Emits...), nil
}

func decodeJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func containsTarget(values []kernel.AggregateKind, target kernel.AggregateKind) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func validateValue(value any, rule map[string]any) error {
	if alternatives, ok := rule["anyOf"].([]any); ok {
		for _, alternative := range alternatives {
			candidate, ok := alternative.(map[string]any)
			if ok && validateValue(value, candidate) == nil {
				return nil
			}
		}
		return errors.New("does not match any allowed schema")
	}
	if constant, exists := rule["const"]; exists && !reflect.DeepEqual(value, constant) {
		return errors.New("does not match required constant")
	}
	if enum, ok := rule["enum"].([]any); ok {
		matched := false
		for _, allowed := range enum {
			if reflect.DeepEqual(value, allowed) {
				matched = true
				break
			}
		}
		if !matched {
			return errors.New("value is outside enum")
		}
	}

	typeName, _ := rule["type"].(string)
	switch typeName {
	case "null":
		if value != nil {
			return errors.New("expected null")
		}
	case "string":
		text, ok := value.(string)
		if !ok {
			return errors.New("expected string")
		}
		length := utf8.RuneCountInString(text)
		if minimum, ok := integerRule(rule, "minLength"); ok && length < minimum {
			return errors.New("string is too short")
		}
		if maximum, ok := integerRule(rule, "maxLength"); ok && length > maximum {
			return errors.New("string is too long")
		}
		if expression, ok := rule["pattern"].(string); ok {
			pattern, err := regexp.Compile(expression)
			if err != nil || !pattern.MatchString(text) {
				return errors.New("string does not match pattern")
			}
		}
	case "integer":
		number, ok := value.(json.Number)
		if !ok {
			return errors.New("expected integer")
		}
		integer, err := number.Int64()
		if err != nil {
			return errors.New("expected integer")
		}
		if minimum, ok := int64Rule(rule, "minimum"); ok && integer < minimum {
			return errors.New("integer is below minimum")
		}
		if maximum, ok := int64Rule(rule, "maximum"); ok && integer > maximum {
			return errors.New("integer is above maximum")
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return errors.New("expected boolean")
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			return errors.New("expected array")
		}
		if minimum, ok := integerRule(rule, "minItems"); ok && len(items) < minimum {
			return errors.New("array has too few items")
		}
		if maximum, ok := integerRule(rule, "maxItems"); ok && len(items) > maximum {
			return errors.New("array has too many items")
		}
		if itemRule, ok := rule["items"].(map[string]any); ok {
			for _, item := range items {
				if err := validateValue(item, itemRule); err != nil {
					return fmt.Errorf("invalid array item: %w", err)
				}
			}
		}
		if unique, ok := rule["uniqueItems"].(bool); ok && unique {
			seen := make(map[string]struct{}, len(items))
			for _, item := range items {
				encoded, err := json.Marshal(item)
				if err != nil {
					return errors.New("cannot canonicalize array item")
				}
				if _, duplicate := seen[string(encoded)]; duplicate {
					return errors.New("array items are not unique")
				}
				seen[string(encoded)] = struct{}{}
			}
		}
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return errors.New("expected object")
		}
		properties, _ := rule["properties"].(map[string]any)
		if minimum, ok := integerRule(rule, "minProperties"); ok && len(object) < minimum {
			return errors.New("object has too few properties")
		}
		if required, ok := rule["required"].([]any); ok {
			for _, rawName := range required {
				name, _ := rawName.(string)
				if _, present := object[name]; !present {
					return fmt.Errorf("missing required property %q", name)
				}
			}
		}
		if additional, ok := rule["additionalProperties"].(bool); ok && !additional {
			for name := range object {
				if _, allowed := properties[name]; !allowed {
					return fmt.Errorf("unknown property %q", name)
				}
			}
		}
		for name, property := range properties {
			item, present := object[name]
			propertyRule, ok := property.(map[string]any)
			if present && ok {
				if err := validateValue(item, propertyRule); err != nil {
					return fmt.Errorf("property %q: %w", name, err)
				}
			}
		}
	}
	return nil
}

func integerRule(rule map[string]any, name string) (int, bool) {
	value, ok := int64Rule(rule, name)
	return int(value), ok
}

func int64Rule(rule map[string]any, name string) (int64, bool) {
	number, ok := rule[name].(json.Number)
	if !ok {
		return 0, false
	}
	value, err := number.Int64()
	return value, err == nil
}
