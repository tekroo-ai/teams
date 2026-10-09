package operationalruntime

import (
	"context"
	"errors"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// The admitted message is a durable task-to-feature link. Do not reconstruct
// current dispatch identity from a feature status or a capped feature listing.
func (service *ProductionService) dispatchedTaskFeature(ctx context.Context, taskID kernel.UUIDv7) (organization.FeatureRequest, bool, error) {
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: taskID}})
	if err != nil {
		return organization.FeatureRequest{}, false, err
	}
	invocation, found := latestTaskInvocation(snapshot.WorkInvocations, taskID)
	if !found || invocation.HandlerDispatch == nil {
		return organization.FeatureRequest{}, false, nil // bounded legacy compatibility
	}
	binding := invocation.HandlerDispatch
	claim, found, err := service.Store.ReadMessage(ctx, binding.MessageID)
	if err != nil || !found || !binding.Valid() || claim.Message.Type != binding.MessageType || string(claim.Message.Purpose) != binding.MessagePurpose || digestBytes(claim.Message.Body) != binding.MessageBodyDigest || claim.Message.Flow.BudgetAccountID != invocation.BudgetAccountID || claim.Message.Work.TaskID != nil && *claim.Message.Work.TaskID != taskID {
		return organization.FeatureRequest{}, false, errors.Join(organization.ErrInvalidFeature, err)
	}
	if claim.Message.Work.FeatureID == nil {
		// Non-feature workflow messages legitimately have no feature link. A
		// planning handler, however, cannot run with that mandatory link missing.
		switch binding.MessageType {
		case "tekroo.message.feature.submitted", "tekroo.message.feature.refined", "tekroo.message.story.design-requested", "tekroo.message.feature.design-proposed":
			return organization.FeatureRequest{}, false, organization.ErrInvalidFeature
		default:
			return organization.FeatureRequest{}, false, nil
		}
	}
	feature, found, err := service.Store.LoadFeature(ctx, *claim.Message.Work.FeatureID)
	if err != nil || !found || feature.BudgetAccountID != invocation.BudgetAccountID {
		return organization.FeatureRequest{}, false, errors.Join(organization.ErrInvalidFeature, err)
	}
	return feature, true, nil
}

// Advisory nodes have no message handler of their own. Their authoritative
// task dependencies lead back to the admitted design/PM message instead.
func (service *ProductionService) nativeBriefFeatures(ctx context.Context, brief application.ExecutionBrief) ([]organization.FeatureRequest, error) {
	var linked *organization.FeatureRequest
	for _, taskID := range append([]kernel.UUIDv7{brief.Task.TaskID}, brief.Task.DependsOn...) {
		feature, found, err := service.dispatchedTaskFeature(ctx, taskID)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		if linked != nil && linked.ID != feature.ID {
			return nil, organization.ErrInvalidFeature
		}
		copy := feature
		linked = &copy
	}
	if linked != nil {
		return []organization.FeatureRequest{*linked}, nil
	}
	return service.Store.ListFeatures(ctx, nativePlanningInspectionStatuses(), 1000) // pre-handler compatibility only
}

// Creation, dispatch and recovery use the same deterministic identity for every
// recorded round. Status is deliberately not an identity filter: old journals
// remain inspectable after a feature advances.
func planningTaskIdentity(feature organization.FeatureRequest, taskID kernel.UUIDv7) (featurePlanningStage, *uint32, uint32, bool) {
	for _, stage := range []featurePlanningStage{stageRefinement, stageSpecification} {
		if featurePlanningTaskID(feature.ID, stage, 0, nil) == taskID {
			return stage, nil, 0, true
		}
	}
	for round := uint32(0); round <= architecturePlanRecordedRoundLimit; round++ {
		for _, stage := range []featurePlanningStage{stageArchitecture, stagePlanFinalization, stageArchitectureReview} {
			if featurePlanningTaskID(feature.ID, stage, round, nil) == taskID {
				return stage, nil, round, true
			}
		}
		for index := uint32(0); index < feature.Input.MaximumTasks && index < organization.MaximumFeatureTasks; index++ {
			if featurePlanningTaskID(feature.ID, stageArchitectureTaskReview, round, &index) == taskID {
				copy := index
				return stageArchitectureTaskReview, &copy, round, true
			}
		}
	}
	return "", nil, 0, false
}

// A PM has one revision allowance, relative to the design it actually reviewed,
// not necessarily architecture round zero. Both durable output digests must
// identify the same completed round; ambiguous or absent lineage fails closed.
func (service *ProductionService) designRevisionSourceRound(ctx context.Context, feature organization.FeatureRequest) (uint32, error) {
	return designRevisionSourceRound(ctx, service.Store, feature)
}

type planningLineageReader interface {
	ReadAggregateHead(context.Context, kernel.AggregateRef) (kernel.AggregateState, kernel.UUIDv7, bool, error)
	LoadDecision(context.Context, kernel.KernelCommand) (kernel.Snapshot, error)
}

func designRevisionSourceRound(ctx context.Context, reader planningLineageReader, feature organization.FeatureRequest) (uint32, error) {
	if feature.DesignRevision == nil {
		return 0, organization.ErrInvalidFeature
	}
	var sourceRound uint32
	matched := false
	for round := uint32(0); round <= architecturePlanRecordedRoundLimit; round++ {
		architectureID := featurePlanningTaskID(feature.ID, stageArchitecture, round, nil)
		architectureState, _, recorded, err := reader.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: architectureID})
		if err != nil {
			return 0, err
		}
		if !recorded {
			break // architecture rounds are created contiguously
		}
		pmID := featurePlanningTaskID(feature.ID, stagePlanFinalization, round, nil)
		state, _, found, err := reader.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: pmID})
		if err != nil {
			return 0, err
		}
		if !found || state.Phase != kernel.PhaseCompleted {
			continue
		}
		snapshot, err := reader.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: pmID}})
		if err != nil {
			return 0, err
		}
		pm, found := latestTaskInvocation(snapshot.WorkInvocations, pmID)
		if !found || pm.State != kernel.InvocationSucceeded || pm.OutputDigest == nil || *pm.OutputDigest != feature.DesignRevision.DecisionDigest {
			continue
		}
		architecture, architectureFound := latestTaskInvocation(snapshot.WorkInvocations, architectureID)
		if !architectureFound || architectureState.Phase != kernel.PhaseCompleted || architecture.State != kernel.InvocationSucceeded || architecture.OutputDigest == nil || *architecture.OutputDigest != feature.DesignRevision.PriorDesignDigest || matched || round == architecturePlanRecordedRoundLimit {
			return 0, organization.ErrInvalidFeature
		}
		sourceRound, matched = round, true
	}
	if !matched {
		return 0, organization.ErrInvalidFeature
	}
	return sourceRound, nil
}
