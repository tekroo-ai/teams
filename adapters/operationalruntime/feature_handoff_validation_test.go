package operationalruntime

import (
	"strings"
	"testing"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestAuthoredImplementationHandoffCountsOnlyAuthoredEdges(t *testing.T) {
	tasks := []architectureTaskResult{
		{Purpose: kernel.PurposeImplementation, Risk: organization.RiskLow},
		{Purpose: kernel.PurposeImplementation, Risk: organization.RiskLow, DependsOn: []uint32{0}},
		{Purpose: kernel.PurposeImplementation, Risk: organization.RiskLow},
		{Purpose: kernel.PurposeImplementation, Risk: organization.RiskHigh, DependsOn: []uint32{1}},
	}
	handoffs := authoredImplementationHandoffs(tasks)
	for index, want := range []bool{false, true, false, true} {
		if handoffs[index] != want {
			t.Fatalf("handoff[%d] = %t, want %t", index, handoffs[index], want)
		}
	}
	// A scheduling edge introduced during normalization must not turn an
	// independent low-risk task into an additional model review.
	if got := requiredMaterializedTaskCount(tasks); got != 9 {
		t.Fatalf("required task count = %d, want 9", got)
	}
	tasks[2].DependsOn = []uint32{1}
	if got := requiredMaterializedTaskCount(tasks); got != 10 {
		t.Fatalf("task count with another authored handoff = %d, want 10", got)
	}
}

func TestImplementationValidationCriteriaBindsRealHandoffOnlyWhenNeeded(t *testing.T) {
	original := []string{"the consumer produces the requested result"}
	independent := implementationValidationCriteria(original, false)
	if len(independent) != 1 || independent[0] != original[0] {
		t.Fatalf("independent criteria changed: %v", independent)
	}
	handoff := implementationValidationCriteria(original, true)
	if len(handoff) != 2 || handoff[0] != original[0] || !strings.Contains(handoff[1], "actual call or data exchange") || !strings.Contains(handoff[1], "stand-in") {
		t.Fatalf("handoff criteria do not require the real dependency: %v", handoff)
	}
	if len(original) != 1 {
		t.Fatalf("source criteria mutated: %v", original)
	}
}
