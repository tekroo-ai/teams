package nativeagent

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"
	"strings"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

// The native transport uses a structured tool call, then restores the
// coordinator's existing marker format. This does not change the accepted
// validation result or its downstream parser.
var validationResultToolSchema = json.RawMessage(`{"type":"object","additionalProperties":false,"required":["schema_version","outcome","reasons"],"properties":{"schema_version":{"const":"1.0.0"},"outcome":{"enum":["PASS","FAIL","BLOCKED","INCONCLUSIVE"]},"reasons":{"type":"array","minItems":1,"maxItems":64,"uniqueItems":true,"items":{"type":"string","minLength":1,"maxLength":4096}},"candidate_id":{"type":"string"},"candidate_receipt_sha256":{"type":"string","pattern":"^[0-9a-f]{64}$"},"test_evidence":{"type":"array","maxItems":256,"items":{"type":"string","minLength":1,"maxLength":4096}}}}`)

func nativeValidationResult(brief application.ExecutionBrief) (agentruntime.ToolDefinition, func(json.RawMessage) (string, error), bool) {
	if brief.ResultProtocol == nil || brief.ResultProtocol.Marker != application.ValidationResultMarker || brief.ResultProtocol.SchemaVersion != "1.0.0" ||
		!slices.Equal(brief.ResultProtocol.Outcomes, []string{"PASS", "FAIL", "BLOCKED", "INCONCLUSIVE"}) {
		return agentruntime.ToolDefinition{}, nil, false
	}
	switch brief.Purpose {
	case kernel.PurposeValidation, kernel.PurposeReview, kernel.PurposePromotion:
	default:
		return agentruntime.ToolDefinition{}, nil, false
	}
	definition := agentruntime.ToolDefinition{
		Name: "submit_result", Description: "Submit the structured validation, review, or acceptance result for this Teams invocation.",
		Parameters: append(json.RawMessage(nil), validationResultToolSchema...),
		Strict:     true,
	}
	return definition, func(arguments json.RawMessage) (string, error) {
		if err := validateNativeValidationPayload(arguments); err != nil {
			return "", err
		}
		return application.ValidationResultMarker + "\n" + string(arguments), nil
	}, true
}

func validationPurpose(purpose kernel.WorkPurpose) bool {
	return purpose == kernel.PurposeValidation || purpose == kernel.PurposeReview || purpose == kernel.PurposePromotion
}

func validateNativeValidationPayload(arguments json.RawMessage) error {
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	var result struct {
		SchemaVersion          string        `json:"schema_version"`
		Outcome                string        `json:"outcome"`
		Reasons                []string      `json:"reasons"`
		CandidateID            kernel.UUIDv7 `json:"candidate_id"`
		CandidateReceiptSHA256 kernel.Digest `json:"candidate_receipt_sha256"`
		TestEvidence           []string      `json:"test_evidence"`
	}
	if decoder.Decode(&result) != nil || decoder.Decode(new(any)) != io.EOF || result.SchemaVersion != "1.0.0" || len(result.Reasons) < 1 || len(result.Reasons) > 64 || len(result.TestEvidence) > 256 {
		return ErrInvalidBinding
	}
	switch result.Outcome {
	case "PASS", "FAIL", "BLOCKED", "INCONCLUSIVE":
	default:
		return ErrInvalidBinding
	}
	seen := make(map[string]bool, len(result.Reasons))
	for _, reason := range result.Reasons {
		if strings.TrimSpace(reason) != reason || reason == "" || len(reason) > 4096 || seen[reason] {
			return ErrInvalidBinding
		}
		seen[reason] = true
	}
	if (result.CandidateID == "") != (result.CandidateReceiptSHA256 == "") || result.CandidateID != "" && (!result.CandidateID.Valid() || !result.CandidateReceiptSHA256.Valid()) {
		return ErrInvalidBinding
	}
	for _, evidence := range result.TestEvidence {
		if strings.TrimSpace(evidence) == "" || len(evidence) > 4096 {
			return ErrInvalidBinding
		}
	}
	return nil
}
