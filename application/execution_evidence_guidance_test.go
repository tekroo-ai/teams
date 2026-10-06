package application

import (
	"strings"
	"testing"
)

func TestExecutionGuidancePreservesIntegrationOwnershipAndEvidenceLimits(t *testing.T) {
	for _, tc := range []struct {
		guidance []string
		required []string
	}{
		{repositoryExecutionGuidance, []string{"truncated search", "incomplete evidence", "Literal search"}},
		{readOnlyExecutionGuidance, []string{"real application entry point", "concrete implementation", "required write scope", "report that precise gap"}},
		{validationExecutionGuidance, []string{"configured concrete implementation", "identified later task", "skipped integration test", "infrastructure limitation", "unverified"}},
	} {
		joined := strings.Join(tc.guidance, "\n")
		for _, phrase := range tc.required {
			if !strings.Contains(joined, phrase) {
				t.Errorf("missing general evidence guidance %q", phrase)
			}
		}
	}
}
