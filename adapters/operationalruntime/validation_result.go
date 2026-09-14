package operationalruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

var errInvalidValidationResult = errors.New("invalid structured validation result")

type structuredValidationResult struct {
	SchemaVersion          string        `json:"schema_version"`
	Outcome                string        `json:"outcome"`
	Reasons                []string      `json:"reasons"`
	CandidateID            kernel.UUIDv7 `json:"candidate_id,omitempty"`
	CandidateReceiptSHA256 kernel.Digest `json:"candidate_receipt_sha256,omitempty"`

	// TestEvidence carries the raw test receipts a validator ran. It is
	// advisory evidence only: it cannot change the outcome, and every decision
	// Teams makes still comes from Outcome plus the candidate binding.
	TestEvidence []string `json:"test_evidence,omitempty"`
}

func parseStructuredValidationResult(output []byte) (structuredValidationResult, error) {
	if workProduct, wrapped := roleHandlerWorkProduct(output); wrapped {
		output = append(append([]byte(application.ValidationResultMarker+"\n"), workProduct...), '\n')
	}
	marker := []byte(application.ValidationResultMarker)
	index := bytes.LastIndex(output, marker)
	if index < 0 || bytes.Count(output, marker) != 1 {
		return structuredValidationResult{}, errInvalidValidationResult
	}
	if index > 0 && output[index-1] != '\n' {
		return structuredValidationResult{}, errInvalidValidationResult
	}
	encoded := bytes.TrimSpace(output[index+len(marker):])
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var result structuredValidationResult
	if decoder.Decode(&result) != nil {
		return structuredValidationResult{}, errInvalidValidationResult
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF || result.SchemaVersion != "1.0.0" || len(result.Reasons) == 0 || len(result.Reasons) > 64 {
		return structuredValidationResult{}, errInvalidValidationResult
	}
	switch result.Outcome {
	case "PASS", "FAIL", "BLOCKED", "INCONCLUSIVE":
	default:
		return structuredValidationResult{}, errInvalidValidationResult
	}
	seen := make(map[string]struct{}, len(result.Reasons))
	for _, reason := range result.Reasons {
		if strings.TrimSpace(reason) != reason || reason == "" || len(reason) > 4096 {
			return structuredValidationResult{}, errInvalidValidationResult
		}
		if _, duplicate := seen[reason]; duplicate {
			return structuredValidationResult{}, errInvalidValidationResult
		}
		seen[reason] = struct{}{}
	}
	if (result.CandidateID == "") != (result.CandidateReceiptSHA256 == "") || result.CandidateID != "" && (!result.CandidateID.Valid() || !result.CandidateReceiptSHA256.Valid()) {
		return structuredValidationResult{}, errInvalidValidationResult
	}
	if len(result.TestEvidence) > 256 {
		return structuredValidationResult{}, errInvalidValidationResult
	}
	for _, receipt := range result.TestEvidence {
		if strings.TrimSpace(receipt) != receipt || receipt == "" || len(receipt) > 4096 || strings.ContainsAny(receipt, "\r\n\t") {
			return structuredValidationResult{}, errInvalidValidationResult
		}
	}
	return result, nil
}
