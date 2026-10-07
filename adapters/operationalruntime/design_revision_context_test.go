package operationalruntime

import (
	"encoding/json"
	"testing"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/organization"
)

func TestDesignRevisionFeedbackKeepsOriginalProposalStructured(t *testing.T) {
	output := []byte(application.OrganizationalResultMarker + `{"outcome":"completed","work_product":{"design_decisions":["A"],"tasks":[]}}`)
	before := string(output)
	revision := organization.FeatureDesignRevision{PriorDesignDigest: digestBytes(output), Reason: "Clarify one decision"}
	feedback, err := designRevisionFeedback(revision, output)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Product map[string]json.RawMessage `json:"prior_design_work_product"`
	}
	if json.Unmarshal(feedback, &parsed) != nil || parsed.Product["design_decisions"] == nil {
		t.Fatalf("proposal was string-encoded: %s", feedback)
	}
	if before != string(output) {
		t.Fatal("original output was changed")
	}
	invalid := []byte(application.OrganizationalResultMarker + `{"work_product":"{\"tasks\":[]}"}`)
	revision.PriorDesignDigest = digestBytes(invalid)
	if _, err := designRevisionFeedback(revision, invalid); err == nil {
		t.Fatal("accepted JSON hidden in a string")
	}
	revision.PriorDesignDigest = digestBytes(output)
	if _, err := designRevisionFeedback(revision, invalid); err == nil {
		t.Fatal("accepted a mismatched source digest")
	}
}
