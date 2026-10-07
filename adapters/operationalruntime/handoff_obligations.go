package operationalruntime

import (
	"fmt"
	"slices"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// Carry the PM's public-boundary agreement into both task briefs and existing
// validators. This adds no product scope, task, role, or implementation choice.
func bindPlanHandoffObligations(tasks []organization.PlannedTask, handoffs []organization.PlannedHandoff) ([]organization.PlannedTask, error) {
	result := append([]organization.PlannedTask(nil), tasks...)
	for _, h := range handoffs {
		provider, consumer := -1, -1
		for i := range result {
			if result[i].ID == h.ProviderTaskID {
				provider = i
			}
			if result[i].ID == h.ConsumerTaskID {
				consumer = i
			}
		}
		if provider < 0 || consumer < 0 {
			return nil, organization.ErrInvalidFeature
		}
		result[provider].Description += fmt.Sprintf("\nOutgoing handoff %s to task %s (consumer write scope: %v):\n%s\nVerify the actual delivered boundary is usable with the agreed inputs from that consumer's scope. For runtime API handoffs, a type assertion or nil/test-double-only construction is not sufficient evidence of runtime constructibility.", h.Capability, h.ConsumerTaskID, result[consumer].WriteScope, h.Contract)
		result[consumer].Description += fmt.Sprintf("\nIncoming handoff %s from task %s:\n%s\nUse the agreed public boundary. If an input or interface is unavailable within assigned scope, report needs_decision with exact evidence rather than expanding scope or repeating discovery of a nonexistent accessor.", h.Capability, h.ProviderTaskID, h.Contract)
		for i := range result {
			if (result[i].Purpose == kernel.PurposeValidation || result[i].Purpose == kernel.PurposeReview) && slices.Contains(result[i].Validates, h.ProviderTaskID) {
				result[i].AcceptanceCriteria = append(append([]string(nil), result[i].AcceptanceCriteria...), fmt.Sprintf("Verify handoff %s is usable through the provider's actual delivered boundary with the agreed inputs from consumer task %s's scope. For runtime API handoffs, interface assertions or nil/test-double-only construction are not runtime construction proof.", h.Capability, h.ConsumerTaskID))
				result[i].Description += fmt.Sprintf("\nHandoff %s to verify:\n%s", h.Capability, h.Contract)
			}
		}
	}
	for _, t := range result {
		if len(t.Description) > 65536 || len(t.AcceptanceCriteria) > 64 {
			return nil, organization.ErrInvalidFeature
		}
	}
	return result, nil
}
