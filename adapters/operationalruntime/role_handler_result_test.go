package operationalruntime

import (
	"testing"

	"github.com/tekroo-ai/teams/application"
)

func TestRoleHandlerDispositionRecognizesDecisionRequest(t *testing.T) {
	output := []byte(application.OrganizationalResultMarker + `{"schema_version":"1.0.0","outcome":"needs_decision","summary":"Interface owner is absent","evidence":[],"message_proposals":[],"work_product":{}}`)
	outcome, summary, ok := roleHandlerDisposition(output)
	if !ok || outcome != "needs_decision" || summary != "Interface owner is absent" {
		t.Fatalf("decision disposition = %q %q %t", outcome, summary, ok)
	}
	if _, _, ok := roleHandlerDisposition([]byte("not a result")); ok {
		t.Fatal("unmarked output was accepted")
	}
}
