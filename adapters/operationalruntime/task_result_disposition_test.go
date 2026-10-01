package operationalruntime

import (
	"strings"
	"testing"

	"github.com/tekroo-ai/teams/application"
)

func TestReportedTaskNonCompletionIsNotCandidateSuccess(t *testing.T) {
	for _, outcome := range []string{"blocked", "needs_decision", "failed"} {
		t.Run(outcome, func(t *testing.T) {
			output := []byte(application.OrganizationalResultMarker + `{"outcome":"` + outcome + `","summary":"missing upstream capability","work_product":{}}`)
			reason, got, reported := reportedTaskNonCompletion(output)
			if !reported || got != outcome || !strings.Contains(reason, "missing upstream capability") {
				t.Fatalf("reason=%q outcome=%q reported=%t", reason, got, reported)
			}
		})
	}
	for _, output := range [][]byte{
		[]byte(application.OrganizationalResultMarker + `{"outcome":"completed","summary":"done"}`),
		[]byte("not a result"),
	} {
		if reason, outcome, reported := reportedTaskNonCompletion(output); reported || reason != "" || outcome != "" {
			t.Fatalf("unexpected non-completion: %q %q %t", reason, outcome, reported)
		}
	}
}
