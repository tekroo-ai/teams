package application

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/tekroo-ai/teams/kernel"
)

var ErrRoleHandlerSchemaViolation = errors.New("role handler schema violation")

type rejectingSchemaLoader struct{}

func (rejectingSchemaLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("external schema resource is not package-bound: %s", url)
}

// ValidateRoleHandlerInput verifies the exact admitted body against the
// content-addressed input schema before any external model call is possible.
func ValidateRoleHandlerInput(handler MessageHandlerGrounding, message AdmittedMessage) error {
	if err := validateJSONInstance(handler.InputSchema, message.Body); err != nil {
		return errors.Join(ErrRoleHandlerSchemaViolation, err)
	}
	return nil
}

type RoleHandlerResult struct {
	Outcome          string
	MessageProposals []RoleHandlerMessageProposal
	Raw              json.RawMessage
}

type RoleHandlerMessageProposal struct {
	Type      string          `json:"type"`
	Recipient kernel.ActorFQN `json:"recipient"`
	Body      json.RawMessage `json:"body"`
}

// ValidateRoleHandlerResult accepts only the declared finish marker followed
// by one JSON value, validates that value against the selected handler's
// content-addressed result schema, and applies the handler's authority limits.
func ValidateRoleHandlerResult(handler MessageHandlerGrounding, output []byte) (RoleHandlerResult, error) {
	raw, err := markedJSON(output, OrganizationalResultMarker)
	if err != nil {
		return RoleHandlerResult{}, errors.Join(ErrRoleHandlerSchemaViolation, err)
	}
	if err := validateJSONInstance(handler.ResultSchema, raw); err != nil {
		return RoleHandlerResult{}, errors.Join(ErrRoleHandlerSchemaViolation, err)
	}
	var envelope struct {
		Outcome          string                       `json:"outcome"`
		MessageProposals []RoleHandlerMessageProposal `json:"message_proposals"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || !slices.Contains(handler.AllowedResults, envelope.Outcome) {
		return RoleHandlerResult{}, ErrRoleHandlerSchemaViolation
	}
	for _, proposal := range envelope.MessageProposals {
		if !proposal.Recipient.Valid() || len(proposal.Body) == 0 || !slices.Contains(handler.AllowedMessageProposals, proposal.Type) {
			return RoleHandlerResult{}, ErrRoleHandlerSchemaViolation
		}
	}
	return RoleHandlerResult{Outcome: envelope.Outcome, MessageProposals: envelope.MessageProposals, Raw: append(json.RawMessage(nil), raw...)}, nil
}

func validateJSONInstance(schemaBytes, instanceBytes []byte) error {
	if len(schemaBytes) == 0 || len(instanceBytes) == 0 {
		return ErrRoleHandlerSchemaViolation
	}
	schemaDocument, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaBytes))
	if err != nil {
		return fmt.Errorf("decode schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	compiler.UseLoader(rejectingSchemaLoader{})
	const schemaURL = "urn:tekroo:role-handler-schema"
	if err := compiler.AddResource(schemaURL, schemaDocument); err != nil {
		return fmt.Errorf("add schema: %w", err)
	}
	compiled, err := compiler.Compile(schemaURL)
	if err != nil {
		return fmt.Errorf("compile schema: %w", err)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(instanceBytes))
	if err != nil {
		return fmt.Errorf("decode instance: %w", err)
	}
	if err := compiled.Validate(instance); err != nil {
		return fmt.Errorf("validate instance: %w", err)
	}
	return nil
}

func markedJSON(output []byte, marker string) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(output)
	prefix := []byte(marker)
	if !bytes.HasPrefix(trimmed, prefix) {
		return nil, ErrRoleHandlerSchemaViolation
	}
	trimmed = bytes.TrimSpace(trimmed[len(prefix):])
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if _, object := value.(map[string]any); !object {
		return nil, ErrRoleHandlerSchemaViolation
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, ErrRoleHandlerSchemaViolation
	}
	return append(json.RawMessage(nil), trimmed...), nil
}
