package operationalruntime

import (
	"testing"

	"github.com/tekroo-ai/teams/application"
)

func TestStructuredValidationResultFailsClosed(t *testing.T) {
	valid := []byte("Validated with repository tests.\n" + application.ValidationResultMarker + "\n{\"schema_version\":\"1.0.0\",\"outcome\":\"PASS\",\"reasons\":[\"go test passed\"]}")
	if result, err := parseStructuredValidationResult(valid); err != nil || result.Outcome != "PASS" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	invalid := [][]byte{
		[]byte("PASS"),
		[]byte(application.ValidationResultMarker + "\n{\"schema_version\":\"1.0.0\",\"outcome\":\"PASS\",\"reasons\":[]}"),
		[]byte(application.ValidationResultMarker + "\n{\"schema_version\":\"1.0.0\",\"outcome\":\"PASS\",\"reasons\":[\"ok\"],\"extra\":true}"),
		[]byte(application.ValidationResultMarker + "\n{\"schema_version\":\"1.0.0\",\"outcome\":\"SUCCESS\",\"reasons\":[\"ok\"]}"),
		[]byte(application.ValidationResultMarker + "\n{}\n" + application.ValidationResultMarker + "\n{}"),
	}
	for index, value := range invalid {
		if _, err := parseStructuredValidationResult(value); err == nil {
			t.Fatalf("invalid result %d accepted", index)
		}
	}
}
