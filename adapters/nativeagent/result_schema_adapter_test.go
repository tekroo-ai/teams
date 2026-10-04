package nativeagent

import (
	"encoding/json"
	"testing"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

func TestNoProposalHandlerHasZeroItemModelSchemaAndCanonicalEmptyList(t *testing.T) {
	handler := application.MessageHandlerGrounding{ResultSchema: json.RawMessage(`{"type":"object","required":["message_proposals"],"properties":{"message_proposals":{"type":"array","items":{"type":"object"}}}}`)}
	modelSchema, err := modelFacingHandlerSchema(handler, kernel.PurposeHandoff)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]struct {
			MaxItems *int `json:"maxItems"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(modelSchema, &schema); err != nil || schema.Properties["message_proposals"].MaxItems == nil || *schema.Properties["message_proposals"].MaxItems != 0 {
		t.Fatalf("model proposal authority not constrained: %s, err=%v", modelSchema, err)
	}
	canonical := canonicalizeEmptyProposals(json.RawMessage(`{"outcome":"completed"}`), handler)
	var result struct {
		MessageProposals []json.RawMessage `json:"message_proposals"`
	}
	if err := json.Unmarshal(canonical, &result); err != nil || result.MessageProposals == nil || len(result.MessageProposals) != 0 {
		t.Fatalf("empty proposal list not canonicalized: %s, err=%v", canonical, err)
	}
	withProposal := json.RawMessage(`{"message_proposals":[{"type":"x"}]}`)
	if got := canonicalizeEmptyProposals(withProposal, handler); string(got) != string(withProposal) {
		t.Fatalf("nonempty proposals were silently changed: %s", got)
	}
}

func TestValidationHandlerNestsExactVerdictAndBindsCandidate(t *testing.T) {
	handler := application.MessageHandlerGrounding{ResultSchema: json.RawMessage(`{"type":"object","properties":{"work_product":{"type":"object"},"message_proposals":{"type":"array"}}}`)}
	modelSchema, err := modelFacingHandlerSchema(handler, kernel.PurposeValidation)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	var verdict struct {
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(modelSchema, &schema) != nil || json.Unmarshal(schema.Properties["work_product"], &verdict) != nil ||
		verdict.Properties["outcome"] == nil || verdict.Properties["reasons"] == nil ||
		verdict.Properties["candidate_id"] != nil || verdict.Properties["candidate_receipt_sha256"] != nil {
		t.Fatalf("validation result schema not exposed: %s", modelSchema)
	}
	candidate := &CandidateBinding{ID: kernel.UUIDv7("00000000-0000-7000-8000-000000000965"), ReceiptSHA256: kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")}
	input := json.RawMessage(`{"outcome":"completed","work_product":{"schema_version":"1.0.0","outcome":"PASS","reasons":["exact behavior observed"]}}`)
	bound, err := bindHandlerValidationResult(input, candidate)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		WorkProduct struct {
			CandidateID            kernel.UUIDv7 `json:"candidate_id"`
			CandidateReceiptSHA256 kernel.Digest `json:"candidate_receipt_sha256"`
		} `json:"work_product"`
	}
	if json.Unmarshal(bound, &result) != nil || result.WorkProduct.CandidateID != candidate.ID || result.WorkProduct.CandidateReceiptSHA256 != candidate.ReceiptSHA256 {
		t.Fatalf("candidate not bound: %s", bound)
	}
	withTestOutput := json.RawMessage(`{"outcome":"completed","work_product":{"schema_version":"1.0.0","outcome":"PASS","reasons":["independent validation passed"],"test_evidence":["ok example.test/nativequalification\t1.203s\nPASS"]}}`)
	if _, err := bindHandlerValidationResult(withTestOutput, candidate); err != nil {
		t.Fatalf("JSON Schema-valid go test output could not be submitted: %v", err)
	}
	for _, invalid := range []string{
		`{"outcome":"completed","work_product":{"schema_version":"1.0.0","outcome":"FAIL","reasons":["failure"]}}`,
		`{"outcome":"completed","work_product":{"schema_version":"1.0.0","outcome":"PASS","reasons":["ok"],"extra":1}}`,
		`{"outcome":"completed","work_product":{"schema_version":"1.0.0","outcome":"PASS","reasons":["ok"],"candidate_id":"00000000-0000-7000-8000-000000000966"}}`,
	} {
		if _, err := bindHandlerValidationResult(json.RawMessage(invalid), candidate); err == nil {
			t.Fatalf("invalid verdict accepted: %s", invalid)
		}
	}
}
