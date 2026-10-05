package nativeagent

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

func TestEnvelopePropertyReplacementPreservesUnrelatedConstraints(t *testing.T) {
	source := json.RawMessage(`{"type":"object","required":["outcome","work_product"],"properties":{"outcome":{"const":"completed"},"work_product":{"type":"object"}},"if":{"properties":{"outcome":{"const":"completed"}}},"then":{"allOf":[true,{"properties":{"work_product":{"required":["teams_owned"]}}}]},"else":false}`)
	var schema map[string]json.RawMessage
	if err := json.Unmarshal(source, &schema); err != nil {
		t.Fatal(err)
	}
	replacement := json.RawMessage(`{"type":"object","additionalProperties":false,"required":["delta"],"properties":{"delta":{"type":"array"}}}`)
	if err := replaceEnvelopeProperty(schema, "work_product", replacement); err != nil {
		t.Fatal(err)
	}
	adapted, _ := json.Marshal(schema)
	if !schemaAccepts(t, adapted, []byte(`{"outcome":"completed","work_product":{"delta":[]}}`)) ||
		schemaAccepts(t, adapted, []byte(`{"outcome":"failed","work_product":{"delta":[]}}`)) ||
		schemaAccepts(t, adapted, []byte(`{"outcome":"completed","work_product":{"delta":"[]"}}`)) {
		t.Fatalf("replacement changed unrelated constraints: %s", adapted)
	}
}

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

func TestFixedHandlerEnvelopeVersionIsTeamsOwned(t *testing.T) {
	handler := application.MessageHandlerGrounding{ResultSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["schema_version","outcome"],"properties":{"schema_version":{"const":"1.0.0"},"outcome":{"const":"completed"}}}`)}
	modelSchema, err := modelFacingHandlerSchema(handler, kernel.PurposeHandoff)
	if err != nil {
		t.Fatal(err)
	}
	var exposed struct {
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(modelSchema, &exposed) != nil || slices.Contains(exposed.Required, "schema_version") || exposed.Properties["schema_version"] != nil {
		t.Fatalf("transport-owned version still exposed to model: %s", modelSchema)
	}
	bound := bindHandlerResultVersion(json.RawMessage(`{"outcome":"completed"}`), handler)
	var result map[string]any
	if json.Unmarshal(bound, &result) != nil || result["schema_version"] != "1.0.0" {
		t.Fatalf("fixed version not bound: %s", bound)
	}
	wrong := json.RawMessage(`{"schema_version":"2.0.0","outcome":"completed"}`)
	if got := bindHandlerResultVersion(wrong, handler); string(got) != string(wrong) {
		t.Fatalf("incorrect supplied version silently rewritten: %s", got)
	}
}

func TestSignedNestedConstantsAreBoundWithoutChangingModelDecisions(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","required":["schema_version","outcome","work_product"],"properties":{"schema_version":{"const":"1.0.0"},"outcome":{"enum":["completed","failed"]},"work_product":{"type":"object","required":["schema_version","result_type","tasks"],"properties":{"schema_version":{"const":"1.0.0"},"result_type":{"const":"FEATURE_PLAN"},"tasks":{"type":"array"}}}}}`)
	input := json.RawMessage(`{"outcome":"completed","work_product":{"tasks":[]}}`)
	bound := bindHandlerFixedConstants(input, schema)
	var result struct {
		SchemaVersion string `json:"schema_version"`
		Outcome       string `json:"outcome"`
		WorkProduct   struct {
			SchemaVersion string `json:"schema_version"`
			ResultType    string `json:"result_type"`
			Tasks         []any  `json:"tasks"`
		} `json:"work_product"`
	}
	if err := json.Unmarshal(bound, &result); err != nil || result.SchemaVersion != "1.0.0" || result.Outcome != "completed" || result.WorkProduct.SchemaVersion != "1.0.0" || result.WorkProduct.ResultType != "FEATURE_PLAN" || result.WorkProduct.Tasks == nil {
		t.Fatalf("signed constants were not bound: %s, err=%v", bound, err)
	}
	wrong := json.RawMessage(`{"outcome":"completed","work_product":{"schema_version":"2.0.0","tasks":[]}}`)
	bound = bindHandlerFixedConstants(wrong, schema)
	if !strings.Contains(string(bound), `"schema_version":"2.0.0"`) {
		t.Fatalf("model-supplied conflicting constant was rewritten: %s", bound)
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
	if _, err := bindHandlerValidationResult(json.RawMessage(`{"outcome":"completed","work_product":{"schema_version":"1.0.0","outcome":"PASS","reasons":["ok"],"criteria":[]}}`), candidate); err == nil || !strings.Contains(err.Error(), `unsupported field "criteria"`) {
		t.Fatalf("unsupported acceptance field was not identified: %v", err)
	}
}
