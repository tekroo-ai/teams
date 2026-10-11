package nativeagent

import (
	"github.com/tekroo-ai/teams/agenttools"
	"testing"
)

func TestValidationPASSCannotManufactureNamedExecution(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		complete     bool
		excluded     bool
		wantError    bool
	}{
		{"passing", "PASS", true, false, false},
		{"skipped", "SKIP", true, false, true},
		{"failed", "FAIL", true, false, true},
		{"incomplete", "PASS", false, false, true},
		{"excluded-duplicate", "PASS", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &agenttools.GoTestEvidence{Complete: tc.complete, Tests: []agenttools.GoTestCase{{Package: "p", Name: "TestRequired", Status: tc.status}}}
			if tc.excluded {
				e.Excluded = []agenttools.GoTestCase{{Package: "q", Name: "TestRequired", Status: "NOT_RUN"}}
			}
			err := checkClaimedTestEvidence("TestRequired proves the criterion", map[string]agenttools.Result{"p": {TestEvidence: e}})
			if (err != nil) != tc.wantError {
				t.Fatalf("unexpected acceptance: %v", err)
			}
		})
	}
	if checkClaimedTestEvidence("TestRequired passed", map[string]agenttools.Result{"p": {Output: "ok p"}}) == nil {
		t.Fatal("legacy package ok counted as named proof")
	}
	results := map[string]agenttools.Result{"plain": {TestEvidence: &agenttools.GoTestEvidence{Complete: true, Excluded: []agenttools.GoTestCase{{Package: "p", Name: "TestRequired", Status: "NOT_RUN"}}}}}
	if checkClaimedTestEvidence("TestRequired passed", results) == nil {
		t.Fatal("initial invalid claim accepted")
	}
	results["tagged"] = agenttools.Result{TestEvidence: &agenttools.GoTestEvidence{Complete: true, Tests: []agenttools.GoTestCase{{Package: "p", Name: "TestRequired", Status: "PASS"}}}}
	if err := checkClaimedTestEvidence("TestRequired passed", results); err != nil {
		t.Fatalf("corrected selection could not recover in the same assessment: %v", err)
	}
}
