package operationalruntime

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/tekroo-ai/teams/application"
)

// run-059 counterexample: invocation 2cad15b4-360b-71dc-858f-cbdaff4fd569 returned a
// valid PASS whose only extra field was the raw test receipts it actually ran.
// The parser rejected the whole result, blocking the task.
func TestStructuredValidationResultRetainsRun059ValidatorOutput(t *testing.T) {
	stored, err := os.ReadFile("testdata/run059-validator-2cad15b4.result.txt")
	if err != nil {
		t.Fatal(err)
	}
	result, err := parseStructuredValidationResult(stored)
	if err != nil {
		t.Fatalf("retained validator output rejected: %v", err)
	}
	if result.Outcome != "PASS" {
		t.Fatalf("outcome=%q", result.Outcome)
	}
	if len(result.Reasons) != 9 || len(result.TestEvidence) != 14 {
		t.Fatalf("reasons=%d receipts=%d", len(result.Reasons), len(result.TestEvidence))
	}
	if result.CandidateID != "5e0ef6c2-5ea2-7143-8f9b-37336b3942ea" ||
		result.CandidateReceiptSHA256 != "6a00dcdbee2d8ea26069dee8698a12f38aed6dd126fbf6b5708b02a726b927d8" {
		t.Fatalf("candidate binding lost: %#v", result)
	}
}

func TestStructuredValidationResultReceiptBoundary(t *testing.T) {
	receipts := make([]string, 0, 257)
	for range 257 {
		receipts = append(receipts, "go test ./...: ok")
	}
	encoded, err := json.Marshal(map[string]any{
		"schema_version": "1.0.0",
		"outcome":        "PASS",
		"reasons":        []string{"go test passed"},
		"test_evidence":  receipts,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseStructuredValidationResult([]byte(application.ValidationResultMarker + "\n" + string(encoded))); err == nil {
		t.Fatal("unbounded test_evidence accepted")
	}

	for _, receipt := range []string{"", " ok", "ok ", "first\nsecond", "first\tsecond", strings.Repeat("r", 4097)} {
		encoded, err := json.Marshal(map[string]any{
			"schema_version": "1.0.0",
			"outcome":        "PASS",
			"reasons":        []string{"go test passed"},
			"test_evidence":  []string{receipt},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := parseStructuredValidationResult([]byte(application.ValidationResultMarker + "\n" + string(encoded))); err == nil {
			t.Fatalf("malformed receipt accepted: %q", receipt)
		}
	}
}
