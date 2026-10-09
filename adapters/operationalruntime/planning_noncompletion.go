package operationalruntime

import "fmt"

// Only model-handler planning stages use this envelope. Review protocols keep
// their own verdicts and cannot be bypassed with a fabricated outer outcome.
func planningNonCompletion(stage featurePlanningStage, output []byte) (reason, key string, reported bool) {
	switch stage {
	case stageRefinement, stageSpecification, stageArchitecture, stagePlanFinalization:
	default:
		return "", "", false
	}
	outcome, summary, recognized := roleHandlerDisposition(output)
	if !recognized {
		return "", "", false
	}
	switch outcome {
	case "needs_decision", "blocked", "failed":
	default:
		return "", "", false
	}
	if stage == stagePlanFinalization && outcome == "needs_decision" {
		return planDecisionRequiredReason + ": " + summary, "plan-decision-required", true
	}
	return fmt.Sprintf("planning reported %s: %s", outcome, summary), "planning-reported-" + outcome, true
}
