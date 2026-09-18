package operationalruntime

import (
	"testing"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// Regression: after a durable plan supersession the stage-message admission
// still authorizes the invocation created for the retired planning round. The
// successor round's architecture task activates with its own round-derived
// invocation identity, so the reconciler must not strand on the resulting
// admission-identity mismatch.
func TestSupersededRoundStageInvocationIsAcceptedForItsOwnTask(t *testing.T) {
	featureID := kernel.UUIDv7("01a0b4d4-47ef-7f44-a93f-bde790c3b046")
	feature := organization.FeatureRequest{ID: featureID, PlanSupersession: &organization.FeaturePlanSupersession{}}
	admitted := deterministicOperationalUUID("workflow-test-invocation", "round-0-design")
	admission := kernel.WorkAdmissionResult{AuthorizedInvocationID: &admitted}

	round0 := workflowTestInvocation("round-0-design")
	round0.ID = admitted
	round1Task := featurePlanningTaskID(featureID, stageArchitecture, 1, nil)
	round1 := workflowTestInvocation("round-1-design")
	round1.TaskID = round1Task
	round1.ID = deterministicOperationalUUID("work-invocation", string(featureID), string(round1Task), "handoff", "1")
	round1.Purpose = kernel.PurposeHandoff
	round1.AttemptOrdinal = 1
	if !workflowStageInvocationAccepted(feature, admission, round1, kernel.Snapshot{WorkInvocations: map[kernel.AggregateRef]kernel.WorkInvocation{round1.Ref(): round1}}) {
		t.Fatal("successor architecture round was stranded on the superseded round's admission identity")
	}

	withoutSupersession := organization.FeatureRequest{ID: featureID}
	if workflowStageInvocationAccepted(withoutSupersession, admission, round1, kernel.Snapshot{WorkInvocations: map[kernel.AggregateRef]kernel.WorkInvocation{round1.Ref(): round1}}) {
		t.Fatal("successor-shaped invocation was accepted without a durable plan supersession")
	}

	foreign := workflowTestInvocation("foreign")
	if workflowStageInvocationAccepted(feature, admission, foreign, kernel.Snapshot{WorkInvocations: map[kernel.AggregateRef]kernel.WorkInvocation{foreign.Ref(): foreign}}) {
		t.Fatal("foreign invocation was accepted against the stage admission")
	}

	if !workflowStageInvocationAccepted(feature, admission, round0, kernel.Snapshot{WorkInvocations: map[kernel.AggregateRef]kernel.WorkInvocation{round0.Ref(): round0}}) {
		t.Fatal("admitted round-0 invocation was no longer accepted")
	}

	alien := workflowTestInvocation("round-0-alien")
	alien.TaskID = featurePlanningTaskID(featureID, stageArchitecture, 0, nil)
	if workflowStageInvocationAccepted(feature, admission, alien, kernel.Snapshot{WorkInvocations: map[kernel.AggregateRef]kernel.WorkInvocation{alien.Ref(): alien}}) {
		t.Fatal("unrelated invocation on the admitted round-0 task was accepted without an admission family")
	}
}
