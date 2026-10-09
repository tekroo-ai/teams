package operationalruntime

import (
	"testing"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestPlanningIdentityIncludesAllFinalizationRoundsAndHistoricalStatuses(t *testing.T) {
	feature := organization.FeatureRequest{ID: deterministicOperationalUUID("r57-regression", "feature")}
	feature.Input.MaximumTasks = 16
	for _, status := range []organization.FeatureStatus{organization.FeatureSubmitted, organization.FeatureSpecified, organization.FeatureDesigned, organization.FeaturePlanned, organization.FeatureAwaitingAcceptance, organization.FeatureCancelled} {
		feature.Status = status
		for round := uint32(0); round <= architecturePlanRecordedRoundLimit; round++ {
			for _, want := range []featurePlanningStage{stageArchitecture, stagePlanFinalization, stageArchitectureReview} {
				stage, index, gotRound, found := planningTaskIdentity(feature, featurePlanningTaskID(feature.ID, want, round, nil))
				if !found || stage != want || gotRound != round || index != nil {
					t.Fatalf("%s round %d stage %s: found=%t stage=%s round=%d index=%v", status, round, want, found, stage, gotRound, index)
				}
			}
		}
	}
	for _, stage := range []featurePlanningStage{stageRefinement, stageSpecification} {
		got, index, round, found := planningTaskIdentity(feature, featurePlanningTaskID(feature.ID, stage, 0, nil))
		if !found || got != stage || round != 0 || index != nil {
			t.Fatal("lost intake identity")
		}
	}
	index := uint32(1)
	stage, gotIndex, round, found := planningTaskIdentity(feature, featurePlanningTaskID(feature.ID, stageArchitectureTaskReview, 2, &index))
	if !found || stage != stageArchitectureTaskReview || round != 2 || gotIndex == nil || *gotIndex != index {
		t.Fatal("lost per-task review identity")
	}
	for _, taskID := range []string{"unrelated", string(featurePlanningTaskID(feature.ID, stagePlanFinalization, architecturePlanRecordedRoundLimit+1, nil))} {
		if _, _, _, found := planningTaskIdentity(feature, kernel.UUIDv7(taskID)); found {
			t.Fatal("unrecordable identity accepted")
		}
	}
}
