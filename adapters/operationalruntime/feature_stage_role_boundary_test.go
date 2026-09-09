package operationalruntime

import (
	"slices"
	"strings"
	"testing"

	"github.com/tekroo-ai/teams/organization"
)

func TestSecurityReviewSeparatesImplementationContextFromReviewAcceptance(t *testing.T) {
	target := organization.PlannedTask{
		Title:              "Implement lifecycle operation",
		Description:        "Add the requested lifecycle behavior.",
		AcceptanceCriteria: []string{"FUNCTIONAL_ACCEPTANCE_SENTINEL"},
	}
	description, err := securityReviewDescription(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(description, "FUNCTIONAL_ACCEPTANCE_SENTINEL") {
		t.Fatal("security review lost the implementation context")
	}
	criteria := securityReviewAcceptanceCriteria()
	if slices.Contains(criteria, "FUNCTIONAL_ACCEPTANCE_SENTINEL") {
		t.Fatal("security review copied a functional criterion into its own acceptance criteria")
	}
	joined := strings.Join(criteria, " ")
	for _, required := range []string{"security", "authority-boundary", "evidence", "no general functional"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("security review criteria do not define the %q boundary: %q", required, joined)
		}
	}
}

func TestSecurityReviewRejectsContextThatCannotFitInExecutionBrief(t *testing.T) {
	_, err := securityReviewDescription(organization.PlannedTask{
		Title:       "oversized",
		Description: strings.Repeat("x", 65536),
	})
	if err == nil {
		t.Fatal("oversized security review context was accepted")
	}
}
