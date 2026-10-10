package operationalruntime

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/contract"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func compileResultTextSchema(t *testing.T, raw []byte) *jsonschema.Schema {
	t.Helper()
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource("urn:tekroo:result-text-test", document); err != nil {
		t.Fatal(err)
	}
	schema, err := c.Compile("urn:tekroo:result-text-test")
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func TestUnicodeValidationReasonsSurviveCompletionReviewContracts(t *testing.T) {
	catalogue, err := contract.Load(os.DirFS("../.."), ContractPackagePath)
	if err != nil {
		t.Fatal(err)
	}
	reason := strings.Repeat("😀", 4096)
	result, err := parseStructuredValidationResult(append([]byte(application.ValidationResultMarker+"\n"), mustJSON(map[string]any{"schema_version": "1.0.0", "outcome": "FAIL", "reasons": []string{reason}})...))
	if err != nil {
		t.Fatal(err)
	}
	id := deterministicOperationalUUID("unicode-review-test")
	evidence := []kernel.UUIDv7{id}
	findings := reviewFindings(organization.FeatureRequest{ID: id}, id, "validator", result, evidence)
	raw := mustJSON(map[string]any{
		"review_id": id, "branch_id": "validator", "branch_policy_revision": 1, "source_role": "VALIDATOR", "round": 1,
		"result": result.Outcome, "reasons": result.Reasons, "evidence_ids": evidence, "findings": findings,
		"supersedes_result_event_ids": []kernel.UUIDv7{}, "changed_condition_evidence_ids": []kernel.UUIDv7{},
		"candidate_artifact_digest": repeatedDigest('a'),
		"independence_receipt":      kernel.IndependenceReceipt{ProvenDimensions: []kernel.IndependenceDimension{kernel.IndependenceActor}, IdentityComparisonDigest: repeatedDigest('b'), MethodIDs: []string{"independent-test"}, EvidenceIDs: evidence},
	})
	if _, err := catalogue.ResolveCommand("tekroo.command.completion-review.record-result", kernel.SchemaVersion, kernel.AggregateCompletionReview, raw); err != nil {
		t.Fatal("accepted catalogue rejected Unicode result", err)
	}
	_, _, recorded, err := kernel.ReviewBranchResultFromPayload(raw)
	if err != nil || len(recorded.Findings) != 1 || recorded.Findings[0].Summary != reason || len(recorded.Reasons) != 1 || recorded.Reasons[0] != reason {
		t.Fatal("kernel lost Unicode result", err)
	}
}

func nativeValidationTextSchema(t *testing.T) []byte {
	t.Helper()
	// Read the actual native tool schema, without adding a production export
	// solely for tests or copying a schema that could drift independently.
	file, err := parser.ParseFile(token.NewFileSet(), "../nativeagent/native_result.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var raw string
	ast.Inspect(file, func(node ast.Node) bool {
		v, ok := node.(*ast.ValueSpec)
		if !ok || len(v.Names) != 1 || v.Names[0].Name != "validationResultToolSchema" || len(v.Values) != 1 {
			return true
		}
		call, ok := v.Values[0].(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		literal, ok := call.Args[0].(*ast.BasicLit)
		if !ok {
			return true
		}
		raw, err = strconv.Unquote(literal.Value)
		return false
	})
	if err != nil || raw == "" {
		t.Fatalf("native validation schema unavailable: %v", err)
	}
	return []byte(raw)
}

func TestResultTextLengthsMatchActualJSONSchemas(t *testing.T) {
	consultSchema := compileResultTextSchema(t, consultationSchema)
	validatorSchema := compileResultTextSchema(t, nativeValidationTextSchema(t))
	for _, glyph := range []string{"a", "界", "😀"} {
		for _, count := range []int{1, 2000, 4096, 4097} {
			t.Run(glyph+"/"+strconv.Itoa(count), func(t *testing.T) {
				value := strings.Repeat(glyph, count)
				want := count <= 4096
				consult := mustJSON(map[string]any{"disposition": "NEEDS_SCOPE_CHANGE", "answer": value})
				instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(consult))
				if err != nil {
					t.Fatal(err)
				}
				if accepted := consultSchema.Validate(instance) == nil; accepted != want {
					t.Fatal("consult schema boundary", accepted)
				}
				if _, resolved, err := consultationAnswer(consult); (err == nil) != want || resolved {
					t.Fatal("consult handler/schema mismatch", err)
				}
				for _, field := range []string{"reasons", "test_evidence"} {
					payload := map[string]any{"schema_version": "1.0.0", "outcome": "PASS", "reasons": []string{"verified"}}
					payload[field] = []string{value}
					raw := mustJSON(payload)
					instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
					if err != nil {
						t.Fatal(err)
					}
					if accepted := validatorSchema.Validate(instance) == nil; accepted != want {
						t.Fatal("validator schema boundary", field, accepted)
					}
					if _, err := parseStructuredValidationResult(append([]byte(application.ValidationResultMarker+"\n"), raw...)); (err == nil) != want {
						t.Fatal("validator handler/schema mismatch", field, err)
					}
				}
			})
		}
	}
	if resultTextWithinLimit(string([]byte{0xff}), 4096) {
		t.Fatal("invalid UTF-8 accepted")
	}
}
