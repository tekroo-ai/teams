package operationalruntime

import (
	"slices"
	"strings"
	"testing"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestHandoffAgreementReachesProviderConsumerAndExistingValidator(t *testing.T) {
	provider := kernel.UUIDv7("00000000-0000-7000-8000-000000000331")
	consumer := kernel.UUIDv7("00000000-0000-7000-8000-000000000332")
	tasks := []organization.PlannedTask{{ID: provider, Purpose: kernel.PurposeImplementation, Description: "Provide adapter", WriteScope: []string{"adapter/"}}, {ID: consumer, Purpose: kernel.PurposeImplementation, Description: "Wire adapter", WriteScope: []string{"runtime/"}}, {ID: "00000000-0000-7000-8000-000000000333", Purpose: kernel.PurposeValidation, DependsOn: []kernel.UUIDv7{provider}, Validates: []kernel.UUIDv7{provider}, AcceptanceCriteria: []string{"existing criterion"}}, {ID: "00000000-0000-7000-8000-000000000334", Purpose: kernel.PurposeValidation, DependsOn: []kernel.UUIDv7{consumer}, Validates: []kernel.UUIDv7{consumer}, AcceptanceCriteria: []string{"consumer criterion"}}}
	handoffs := []organization.PlannedHandoff{{ProviderTaskID: provider, ConsumerTaskID: consumer, Capability: "storage", Contract: "OpenAdapter(Config), using public runtime configuration"}}
	got, err := bindPlanHandoffObligations(tasks, handoffs)
	if err != nil || len(got) != len(tasks) {
		t.Fatalf("new tasks or error: %v", err)
	}
	if !strings.Contains(got[0].Description, "runtime/") || !strings.Contains(got[0].Description, "not-yet-written") || !strings.Contains(got[1].Description, "needs_decision") || len(got[2].AcceptanceCriteria) != 1 || len(got[3].AcceptanceCriteria) != 2 || !strings.Contains(got[3].Description, handoffs[0].Contract) || !slices.Equal(got[3].DependsOn, []kernel.UUIDv7{consumer, provider}) || !slices.Equal(got[2].DependsOn, []kernel.UUIDv7{provider}) {
		t.Fatalf("handoff lost at execution: %+v", got)
	}
	if tasks[0].Description != "Provide adapter" || len(tasks[2].AcceptanceCriteria) != 1 || len(tasks[3].DependsOn) != 1 || len(got[1].WriteScope) != 1 || got[1].WriteScope[0] != "runtime/" {
		t.Fatal("source plan or scope mutated")
	}
	handoffs[0].ConsumerTaskID = "00000000-0000-7000-8000-000000000335"
	if _, err := bindPlanHandoffObligations(tasks, handoffs); err == nil {
		t.Fatal("nonexistent consumer accepted")
	}
}
