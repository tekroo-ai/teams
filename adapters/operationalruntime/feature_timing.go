package operationalruntime

import (
	"context"
	"errors"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

type FeatureWorkflowTiming struct {
	FeatureID  kernel.UUIDv7         `json:"feature_id"`
	ObservedAt time.Time             `json:"observed_at"`
	Timing     kernel.WorkflowTiming `json:"timing"`
}

func (service *ProductionService) ReadFeatureWorkflowTiming(ctx context.Context, featureID kernel.UUIDv7) (FeatureWorkflowTiming, error) {
	if service == nil || service.Store == nil || service.Features == nil || service.clock == nil || !featureID.Valid() {
		return FeatureWorkflowTiming{}, organization.ErrInvalidFeature
	}
	feature, found, err := service.Features.Read(ctx, featureID)
	if err != nil || !found {
		return FeatureWorkflowTiming{}, errors.Join(organization.ErrFeatureNotFound, err)
	}
	taskIDs := []kernel.UUIDv7{
		deterministicOperationalUUID("feature-planning-task", string(feature.ID), string(stageRefinement)),
		deterministicOperationalUUID("feature-planning-task", string(feature.ID), string(stageSpecification)),
		deterministicOperationalUUID("feature-planning-task", string(feature.ID), string(stageArchitecture)),
	}
	if feature.Plan != nil {
		for _, task := range feature.Plan.Tasks {
			taskIDs = append(taskIDs, task.ID)
		}
	}
	combined := kernel.Snapshot{WorkInvocations: make(map[kernel.AggregateRef]kernel.WorkInvocation)}
	for _, taskID := range taskIDs {
		snapshot, loadErr := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: taskID}})
		if loadErr != nil {
			return FeatureWorkflowTiming{}, loadErr
		}
		for reference, invocation := range snapshot.WorkInvocations {
			if invocation.TaskID == taskID {
				combined.WorkInvocations[reference] = invocation
			}
		}
	}
	observedAt := service.clock.Now().UTC()
	return FeatureWorkflowTiming{FeatureID: feature.ID, ObservedAt: observedAt, Timing: featureWorkflowTiming(feature, combined, observedAt)}, nil
}

func featureWorkflowTiming(feature organization.FeatureRequest, snapshot kernel.Snapshot, observedAt time.Time) kernel.WorkflowTiming {
	taskIDs := make(map[kernel.UUIDv7]struct{})
	for _, stage := range []featurePlanningStage{stageRefinement, stageSpecification, stageArchitecture} {
		taskIDs[deterministicOperationalUUID("feature-planning-task", string(feature.ID), string(stage))] = struct{}{}
	}
	if feature.Plan != nil {
		for _, task := range feature.Plan.Tasks {
			taskIDs[task.ID] = struct{}{}
		}
	}
	invocations := make([]kernel.WorkInvocation, 0, len(snapshot.WorkInvocations))
	seen := make(map[kernel.UUIDv7]struct{}, len(snapshot.WorkInvocations))
	for _, invocation := range snapshot.WorkInvocations {
		if _, relevant := taskIDs[invocation.TaskID]; !relevant {
			continue
		}
		if _, duplicate := seen[invocation.ID]; duplicate {
			continue
		}
		seen[invocation.ID] = struct{}{}
		invocations = append(invocations, invocation)
	}
	return kernel.MeasureWorkflowTiming(invocations, observedAt)
}

func featureValidationAtCeiling(feature organization.FeatureRequest, snapshot kernel.Snapshot, observedAt time.Time) bool {
	return kernel.EvaluateValidationTiming(featureWorkflowTiming(feature, snapshot, observedAt)).AtHardCeiling
}
