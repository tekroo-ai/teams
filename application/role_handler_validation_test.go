package application

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestRoleHandlerSchemasAndAuthorityAreEnforced(t *testing.T) {
	handler := MessageHandlerGrounding{
		InputSchema:             json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false,"required":["task"],"properties":{"task":{"type":"string","minLength":1}}}`),
		ResultSchema:            json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false,"required":["outcome","message_proposals"],"properties":{"outcome":{"type":"string"},"message_proposals":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["type","recipient","body"],"properties":{"type":{"type":"string"},"recipient":{"type":"string"},"body":{"type":"object"}}}}}}`),
		AllowedResults:          []string{"completed"},
		AllowedMessageProposals: []string{"tekroo.message.task.review-requested"},
	}
	message := AdmittedMessage{Body: json.RawMessage(`{"task":"implement"}`)}
	if err := ValidateRoleHandlerInput(handler, message); err != nil {
		t.Fatalf("valid input: %v", err)
	}
	message.Body = json.RawMessage(`{"task":""}`)
	if err := ValidateRoleHandlerInput(handler, message); !errors.Is(err, ErrRoleHandlerSchemaViolation) {
		t.Fatalf("invalid input error = %v", err)
	}
	output := []byte(OrganizationalResultMarker + `
{"outcome":"completed","message_proposals":[{"type":"tekroo.message.task.review-requested","recipient":"teams::tester-1","body":{"task":"done"}}]}`)
	result, err := ValidateRoleHandlerResult(handler, output)
	if err != nil || result.Outcome != "completed" || len(result.MessageProposals) != 1 {
		t.Fatalf("valid result = %#v error=%v", result, err)
	}
	disallowed := []byte(OrganizationalResultMarker + `
{"outcome":"completed","message_proposals":[{"type":"tekroo.message.task.escalated","recipient":"teams::senior-coder-1","body":{"task":"done"}}]}`)
	if _, err := ValidateRoleHandlerResult(handler, disallowed); !errors.Is(err, ErrRoleHandlerSchemaViolation) {
		t.Fatalf("disallowed proposal error = %v", err)
	}
	if _, err := ValidateRoleHandlerResult(handler, append(output, []byte(` {}`)...)); !errors.Is(err, ErrRoleHandlerSchemaViolation) {
		t.Fatalf("trailing result error = %v", err)
	}
}

func TestRoleHandlerSchemaCannotLoadUnboundExternalResource(t *testing.T) {
	handler := MessageHandlerGrounding{InputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","$ref":"https://example.invalid/unbound.json"}`)}
	err := ValidateRoleHandlerInput(handler, AdmittedMessage{Body: json.RawMessage(`{"task":"implement"}`)})
	if !errors.Is(err, ErrRoleHandlerSchemaViolation) {
		t.Fatalf("external resource error = %v", err)
	}
}

func TestRoleHandlerProposalRequiresActorFQN(t *testing.T) {
	handler := MessageHandlerGrounding{
		ResultSchema:   json.RawMessage(`{"type":"object","required":["outcome","message_proposals"],"properties":{"outcome":{"type":"string"},"message_proposals":{"type":"array"}}}`),
		AllowedResults: []string{"completed"}, AllowedMessageProposals: []string{"tekroo.message.task.review-requested"},
	}
	output := []byte(OrganizationalResultMarker + `
{"outcome":"completed","message_proposals":[{"type":"tekroo.message.task.review-requested","recipient":"Bob","body":{}}]}`)
	if _, err := ValidateRoleHandlerResult(handler, output); !errors.Is(err, ErrRoleHandlerSchemaViolation) {
		t.Fatalf("alias recipient error = %v", err)
	}
}
