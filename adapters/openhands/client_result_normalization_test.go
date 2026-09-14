package openhands

import (
	"encoding/json"
	"testing"

	"github.com/tekroo-ai/teams/application"
)

func TestNormalizedHandlerResultOutputAcceptsOnlyValidatedSuffix(t *testing.T) {
	handler := application.MessageHandlerGrounding{
		ResultSchema:   json.RawMessage(`{"type":"object","additionalProperties":false,"required":["outcome","message_proposals"],"properties":{"outcome":{"type":"string"},"message_proposals":{"type":"array"}}}`),
		AllowedResults: []string{"completed"},
	}
	brief := application.ExecutionBrief{MessageHandler: &handler}
	valid := []byte(application.OrganizationalResultMarker + `
{"outcome":"completed","message_proposals":[]}`)
	verbose := append([]byte("Verification passed.\n\n"), valid...)
	got := normalizedHandlerResultOutput(brief, verbose)
	if string(got) != string(valid) {
		t.Fatalf("normalized output = %q", got)
	}
	invalid := append([]byte("Verification passed.\n\n"), []byte(application.OrganizationalResultMarker+` {"outcome":"completed"}`)...)
	if got := normalizedHandlerResultOutput(brief, invalid); string(got) != string(invalid) {
		t.Fatalf("invalid suffix was normalized: %q", got)
	}
}
