package operationalruntime

import (
	"testing"

	"github.com/tekroo-ai/teams/organization"
)

func TestSpecificationCorrectionPreservesAllSubmittedCriteria(t *testing.T) {
	required := []string{"Alias addresses the same instance.", "The full FQN still works."}
	stories := []SpecificationStoryInput{{
		Title: "Alias", Description: "Add one alias to one instance.", Priority: organization.PriorityNormal,
		AcceptanceCriteria: append([]string(nil), required...),
	}}
	if !correctionPreservesCriteria(required, stories) {
		t.Fatal("one story covering both criteria was rejected")
	}
	stories[0].AcceptanceCriteria = stories[0].AcceptanceCriteria[:1]
	if correctionPreservesCriteria(required, stories) {
		t.Fatal("an omitted original criterion was accepted")
	}
}
