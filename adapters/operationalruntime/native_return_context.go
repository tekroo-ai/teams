package operationalruntime

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/tekroo-ai/teams/adapters/nativeagent"
	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// Correction authority is read from the durable feature, not supplied by the
// returning model. A blocked predecessor stays blocked; this is history access,
// not permission to mark it completed or to replay its effects.
func (service *ProductionService) correctedPlanningReturn(ctx context.Context, feature organization.FeatureRequest, stage featurePlanningStage, round uint32, planning bool, child application.ExecutionBrief, source kernel.WorkInvocation) (*nativeagent.ContextReturnBinding, error) {
	correction := feature.SpecificationCorrection
	if !planning || stage != stageArchitecture || round == 0 || correction == nil || correction.ArchitectureRound != round || correction.Validate(feature) != nil || child.Task.TaskID != featurePlanningTaskID(feature.ID, stage, round, nil) || source.TaskID != featurePlanningTaskID(feature.ID, stage, round-1, nil) || source.State != kernel.InvocationSucceeded {
		return nil, nativeagent.ErrInvalidBinding
	}
	state, _, found, err := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: source.TaskID})
	if err != nil || !found || state.Condition != kernel.ConditionBlocked {
		return nil, errors.Join(nativeagent.ErrInvalidBinding, err)
	}
	for _, ref := range correction.EvidenceRefs {
		if !slices.Contains(child.Evidence, ref) {
			return nil, nativeagent.ErrInvalidBinding
		}
	}
	return &nativeagent.ContextReturnBinding{SourceInvocationID: source.ID, SourceTaskID: source.TaskID, ChildInvocationID: child.InvocationID, ChildTaskID: child.Task.TaskID, CorrectionEvidence: append([]kernel.EvidenceRef(nil), correction.EvidenceRefs...)}, nil
}

// Restart/inspection uses the exact parent captured when this invocation
// started. A newer terminal invocation cannot replace that historical parent.
func (service *ProductionService) recordedReturnContext(ctx context.Context, brief application.ExecutionBrief) (*kernel.WorkInvocation, bool, error) {
	entries, err := service.Store.Load(ctx, string(brief.InvocationID))
	if err != nil {
		return nil, false, err
	}
	if len(entries) == 0 {
		return nil, false, nil
	}
	var start struct {
		Fork *agentruntime.ContextFork `json:"context_fork"`
	}
	if entries[0].Kind != agentruntime.Started || json.Unmarshal(entries[0].Payload, &start) != nil {
		return nil, true, nativeagent.ErrInvalidBinding
	}
	if start.Fork == nil {
		return nil, true, nil
	} // Preserve pre-upgrade completed journals as recorded.
	if !start.Fork.Source.Valid() {
		return nil, true, nativeagent.ErrInvalidBinding
	}
	prior, err := service.Store.LoadOperationalExecution(ctx, kernel.UUIDv7(start.Fork.Source.InvocationID))
	if err != nil {
		return nil, true, errors.Join(nativeagent.ErrInvalidBinding, err)
	}
	source := prior.Invocation
	return &source, true, nil
}

// Resolve from admitted task/invocation lineage, never from a model-selected
// conversation or the actor's unrelated recent work.
func (service *ProductionService) nativeReturnedInvocation(ctx context.Context, brief application.ExecutionBrief) (*kernel.WorkInvocation, error) {
	if brief.RetryOfInvocationID != nil {
		prior, err := service.Store.LoadOperationalExecution(ctx, *brief.RetryOfInvocationID)
		if err != nil {
			return nil, errors.Join(nativeagent.ErrInvalidBinding, err)
		}
		source := prior.Invocation
		return &source, nil
	}
	if brief.Purpose != kernel.PurposeRepair || brief.RecoveryDirective == nil {
		return nil, nil
	}
	if !brief.RecoveryDirective.Valid() {
		return nil, nativeagent.ErrInvalidBinding
	}
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: brief.Task.TaskID}})
	if err != nil {
		return nil, err
	}
	var source *kernel.WorkInvocation
	for _, candidate := range snapshot.WorkInvocations {
		if candidate.ID == brief.InvocationID || candidate.TaskID != brief.Task.TaskID || !candidate.State.Terminal() || candidate.FinishedAt == nil {
			continue
		}
		if source == nil || candidate.FinishedAt.After(*source.FinishedAt) {
			copy := candidate
			source = &copy
		} else if candidate.FinishedAt.Equal(*source.FinishedAt) && candidate.ID != source.ID {
			return nil, nativeagent.ErrInvalidBinding // ambiguous history is not fresh-start authority
		}
	}
	if source == nil {
		return nil, nativeagent.ErrInvalidBinding
	}
	return source, nil
}

// A subsequent design/finalization round returns to its original decision
// owner. A first finalization after earlier design-only review has no PM parent.
func (service *ProductionService) planningReturnSource(ctx context.Context, feature organization.FeatureRequest, stage featurePlanningStage, round uint32) (*kernel.WorkInvocation, error) {
	if round == 0 || stage != stageArchitecture && stage != stagePlanFinalization {
		return nil, nil
	}
	for priorRound := round; priorRound > 0; priorRound-- {
		taskID := featurePlanningTaskID(feature.ID, stage, priorRound-1, nil)
		_, _, found, err := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: taskID})
		if err != nil {
			return nil, err
		}
		if !found {
			if stage == stageArchitecture {
				return nil, nativeagent.ErrInvalidBinding
			}
			continue
		}
		snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: taskID}})
		if err != nil {
			return nil, err
		}
		source, found := latestTaskInvocation(snapshot.WorkInvocations, taskID)
		if !found || source.State != kernel.InvocationSucceeded || source.OutputDigest == nil || source.RequestDigest == nil {
			return nil, nativeagent.ErrInvalidBinding
		}
		if stage == stageArchitecture && round == 1 && feature.DesignRevision != nil && *source.OutputDigest != feature.DesignRevision.PriorDesignDigest {
			return nil, nativeagent.ErrInvalidBinding
		}
		return &source, nil
	}
	return nil, nil
}
