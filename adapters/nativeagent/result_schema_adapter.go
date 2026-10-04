package nativeagent

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

// modelFacingHandlerSchema narrows the signed result contract to the admitted
// handler's proposal authority. The signed schema remains the final validator.
func modelFacingHandlerSchema(handler application.MessageHandlerGrounding, purpose kernel.WorkPurpose) (json.RawMessage, error) {
	var schema map[string]json.RawMessage
	if err := json.Unmarshal(handler.ResultSchema, &schema); err != nil {
		return nil, err
	}
	var properties map[string]json.RawMessage
	if err := json.Unmarshal(schema["properties"], &properties); err != nil {
		return nil, err
	}
	if properties == nil {
		return nil, ErrInvalidBinding
	}
	// The signed envelope retains its fixed version, but it is transport
	// metadata supplied by Teams, not a decision the model must make.
	if fixedHandlerResultVersion(handler.ResultSchema) {
		delete(properties, "schema_version")
		var required []string
		if err := json.Unmarshal(schema["required"], &required); err != nil {
			return nil, ErrInvalidBinding
		}
		schema["required"], _ = json.Marshal(slices.DeleteFunc(required, func(name string) bool { return name == "schema_version" }))
	}
	if validationPurpose(purpose) {
		// The signed envelope allows an object here, while downstream Teams
		// requires a structured validation verdict. Candidate identity is
		// supplied by Teams from the immutable workspace, not guessed by the
		// model from its branch name or commit SHA.
		var verdict map[string]json.RawMessage
		if err := json.Unmarshal(validationResultToolSchema, &verdict); err != nil {
			return nil, err
		}
		var verdictProperties map[string]json.RawMessage
		if err := json.Unmarshal(verdict["properties"], &verdictProperties); err != nil {
			return nil, err
		}
		delete(verdictProperties, "candidate_id")
		delete(verdictProperties, "candidate_receipt_sha256")
		verdict["properties"], _ = json.Marshal(verdictProperties)
		properties["work_product"], _ = json.Marshal(verdict)
	}
	if len(handler.AllowedMessageProposals) != 0 {
		schema["properties"], _ = json.Marshal(properties)
		return json.Marshal(schema)
	}
	proposalRaw, present := properties["message_proposals"]
	if !present {
		schema["properties"], _ = json.Marshal(properties)
		return json.Marshal(schema)
	}
	var proposal map[string]json.RawMessage
	if err := json.Unmarshal(proposalRaw, &proposal); err != nil {
		return nil, err
	}
	proposal["maxItems"] = json.RawMessage("0")
	proposal["description"] = json.RawMessage(`"Required array. No message proposals are authorized here; use []."`)
	properties["message_proposals"], _ = json.Marshal(proposal)
	schema["properties"], _ = json.Marshal(properties)
	return json.Marshal(schema)
}

func fixedHandlerResultVersion(raw json.RawMessage) bool {
	var schema struct {
		Required   []string `json:"required"`
		Properties map[string]struct {
			Const string `json:"const"`
		} `json:"properties"`
	}
	return json.Unmarshal(raw, &schema) == nil && slices.Contains(schema.Required, "schema_version") &&
		schema.Properties["schema_version"].Const == "1.0.0"
}

func bindHandlerResultVersion(arguments json.RawMessage, handler application.MessageHandlerGrounding) json.RawMessage {
	if !fixedHandlerResultVersion(handler.ResultSchema) {
		return arguments
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(arguments, &envelope) != nil || envelope == nil {
		return arguments
	}
	if _, supplied := envelope["schema_version"]; !supplied {
		envelope["schema_version"] = json.RawMessage(`"1.0.0"`)
		encoded, err := json.Marshal(envelope)
		if err == nil {
			return encoded
		}
	}
	return arguments
}

func bindHandlerValidationResult(arguments json.RawMessage, candidate *CandidateBinding) (json.RawMessage, error) {
	var envelope map[string]json.RawMessage
	if json.Unmarshal(arguments, &envelope) != nil || envelope == nil {
		return nil, ErrInvalidBinding
	}
	var outerOutcome string
	if json.Unmarshal(envelope["outcome"], &outerOutcome) != nil {
		return nil, ErrInvalidBinding
	}
	var workProduct map[string]json.RawMessage
	if json.Unmarshal(envelope["work_product"], &workProduct) != nil || workProduct == nil {
		return nil, ErrInvalidBinding
	}
	keys := make([]string, 0, len(workProduct))
	for name := range workProduct {
		keys = append(keys, name)
	}
	slices.Sort(keys)
	for _, name := range keys {
		switch name {
		case "schema_version", "outcome", "reasons", "candidate_id", "candidate_receipt_sha256", "test_evidence":
		default:
			return nil, fmt.Errorf("%w: submit_result.work_product contains unsupported field %q", ErrInvalidBinding, name)
		}
	}
	if candidate != nil {
		for name, value := range map[string]string{
			"candidate_id":             string(candidate.ID),
			"candidate_receipt_sha256": string(candidate.ReceiptSHA256),
		} {
			if existing, present := workProduct[name]; present {
				var claimed string
				if json.Unmarshal(existing, &claimed) != nil || claimed != value {
					return nil, ErrInvalidBinding
				}
			}
			workProduct[name], _ = json.Marshal(value)
		}
	} else if workProduct["candidate_id"] != nil || workProduct["candidate_receipt_sha256"] != nil {
		return nil, ErrInvalidBinding
	}
	encoded, err := json.Marshal(workProduct)
	if err != nil || validateNativeValidationPayload(encoded) != nil {
		return nil, ErrInvalidBinding
	}
	var verdict struct {
		Outcome string `json:"outcome"`
	}
	if json.Unmarshal(encoded, &verdict) != nil || map[string]string{
		"completed": "PASS", "failed": "FAIL", "blocked": "BLOCKED", "needs_decision": "INCONCLUSIVE",
	}[outerOutcome] != verdict.Outcome {
		return nil, ErrInvalidBinding
	}
	envelope["work_product"] = encoded
	return json.Marshal(envelope)
}

// An omitted proposal list and an explicit empty list have the same meaning
// only when the admitted handler cannot propose any message. The raw model
// call remains visible in the journal; signed validation sees the canonical
// empty list rather than a relaxed schema.
func canonicalizeEmptyProposals(arguments json.RawMessage, handler application.MessageHandlerGrounding) json.RawMessage {
	if len(handler.AllowedMessageProposals) != 0 {
		return arguments
	}
	var schema struct {
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(handler.ResultSchema, &schema) != nil || !slices.Contains(schema.Required, "message_proposals") || schema.Properties["message_proposals"] == nil {
		return arguments
	}
	var result map[string]json.RawMessage
	if json.Unmarshal(arguments, &result) != nil || result == nil {
		return arguments
	}
	if _, present := result["message_proposals"]; present {
		return arguments
	}
	result["message_proposals"] = json.RawMessage("[]")
	canonical, err := json.Marshal(result)
	if err != nil {
		return arguments
	}
	return canonical
}
