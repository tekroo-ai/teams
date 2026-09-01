package operationalruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

var ErrFeatureMaterializationPending = errors.New("feature accepted but materialization is pending")

func (service *ProductionService) SubmitFeature(ctx context.Context, principal kernel.PrincipalRef, input organization.FeatureRequestInput) (organization.FeatureRequest, bool, error) {
	if service == nil || service.Features == nil {
		return organization.FeatureRequest{}, false, organization.ErrInvalidFeature
	}
	feature, created, err := service.Features.Submit(ctx, principal, input)
	if err != nil {
		return feature, created, err
	}
	if feature.Status == organization.FeatureSubmitted {
		if err := service.materializeFeatureIntake(ctx, feature); err != nil {
			service.recordRecoveryFault("feature-intake:"+string(feature.ID), err)
			return feature, created, fmt.Errorf("%w: %v", ErrFeatureMaterializationPending, err)
		}
		service.clearRecoveryFault("feature-intake:" + string(feature.ID))
	}
	return feature, created, nil
}

func (service *ProductionService) ReadFeature(ctx context.Context, id kernel.UUIDv7) (organization.FeatureRequest, bool, error) {
	if service == nil || service.Features == nil {
		return organization.FeatureRequest{}, false, organization.ErrInvalidFeature
	}
	return service.Features.Read(ctx, id)
}

func (service *ProductionService) ApplyFeaturePlan(ctx context.Context, id kernel.UUIDv7, expectedRevision uint64, plan organization.FeaturePlan) (organization.FeatureRequest, error) {
	if service == nil || service.Features == nil {
		return organization.FeatureRequest{}, organization.ErrInvalidFeature
	}
	return service.Features.ApplyPlan(ctx, id, expectedRevision, plan)
}

func (service *ProductionService) RefineFeature(ctx context.Context, id kernel.UUIDv7, expectedRevision uint64, refinement organization.FeatureRefinement) (organization.FeatureRequest, error) {
	if service == nil || service.Features == nil {
		return organization.FeatureRequest{}, organization.ErrInvalidFeature
	}
	return service.Features.Refine(ctx, id, expectedRevision, refinement)
}

func (service *ProductionService) SpecifyFeature(ctx context.Context, id kernel.UUIDv7, expectedRevision uint64, specification organization.FeatureSpecification) (organization.FeatureRequest, error) {
	if service == nil || service.Features == nil {
		return organization.FeatureRequest{}, organization.ErrInvalidFeature
	}
	return service.Features.Specify(ctx, id, expectedRevision, specification)
}

// MaterializeFeaturePlan creates canonical story/task aggregates and binds the
// executable work profile, assignment, budget, and operational scope for each
// task. It is idempotent: stable per-feature idempotency keys recover prior
// receipts after an interrupted materialization.
func (service *ProductionService) MaterializeFeaturePlan(ctx context.Context, feature organization.FeatureRequest, plan organization.FeaturePlan) error {
	if service == nil || service.Runtime == nil || service.ids == nil || service.clock == nil || plan.Validate(feature) != nil {
		return organization.ErrInvalidFeature
	}
	if _, err := service.ensureFeaturePlanningStory(ctx, feature); err != nil {
		return err
	}
	storyEvents := make(map[kernel.UUIDv7]kernel.UUIDv7, len(plan.Stories))
	for _, story := range plan.Stories {
		payload, err := json.Marshal(map[string]any{"title": story.Title, "description": story.Description, "acceptance_criteria": story.AcceptanceCriteria})
		if err != nil {
			return err
		}
		receipt, err := service.submitPlannedCommand(ctx, feature, "tekroo.command.story.create", kernel.AggregateStory, story.ID, "story", payload, nil)
		if err != nil {
			return err
		}
		if len(receipt.EventIDs) != 1 {
			return errors.New("story creation did not return one event")
		}
		storyEvents[story.ID] = receipt.EventIDs[0]
	}
	pending := make(map[kernel.UUIDv7]organization.PlannedTask, len(plan.Tasks))
	for _, task := range plan.Tasks {
		pending[task.ID] = task
	}
	taskEvents := make(map[kernel.UUIDv7]kernel.UUIDv7, len(plan.Tasks))
	for len(pending) > 0 {
		advanced := false
		for _, task := range plan.Tasks {
			if _, remains := pending[task.ID]; !remains {
				continue
			}
			parents := []kernel.DagParent{{ParentEventID: storyEvents[task.StoryID], EdgeKind: kernel.EdgeCausal}}
			ready := true
			for _, dependency := range task.DependsOn {
				eventID, found := taskEvents[dependency]
				if !found {
					ready = false
					break
				}
				parents = append(parents, kernel.DagParent{ParentEventID: eventID, EdgeKind: kernel.EdgeCausal})
			}
			if !ready {
				continue
			}
			dependencies := append([]kernel.UUIDv7{}, task.DependsOn...)
			payload, err := json.Marshal(map[string]any{"story_id": task.StoryID, "title": task.Title, "description": task.Description, "acceptance_criteria": task.AcceptanceCriteria, "depends_on": dependencies})
			if err != nil {
				return err
			}
			receipt, err := service.submitPlannedCommand(ctx, feature, "tekroo.command.task.create", kernel.AggregateTask, task.ID, "task", payload, parents)
			if err != nil {
				return err
			}
			if len(receipt.EventIDs) != 1 {
				return errors.New("task creation did not return one event")
			}
			taskEvents[task.ID] = receipt.EventIDs[0]
			delete(pending, task.ID)
			advanced = true
		}
		if !advanced {
			return organization.ErrInvalidFeature
		}
	}
	for _, story := range plan.Stories {
		storyTaskIDs := make([]kernel.UUIDv7, 0)
		for _, task := range plan.Tasks {
			if task.StoryID == story.ID {
				storyTaskIDs = append(storyTaskIDs, task.ID)
			}
		}
		if err := service.activatePlannedStory(ctx, feature, story.ID, storyEvents[story.ID], storyTaskIDs); err != nil {
			return err
		}
	}
	return service.preparePlannedTasks(ctx, feature, plan, storyEvents, taskEvents)
}

func (service *ProductionService) activatePlannedStory(ctx context.Context, feature organization.FeatureRequest, storyID, createdEventID kernel.UUIDv7, taskIDs []kernel.UUIDv7) error {
	sort.Slice(taskIDs, func(left, right int) bool { return taskIDs[left] < taskIDs[right] })
	authorized, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.story.authorize", kernel.SchemaVersion, kernel.AggregateStory, storyID, feature.SubmittedBy, 1, mustJSON(map[string]any{"reason": "operator-authorized feature scope", "scope_revision": feature.ScopeRevision}), []kernel.DagParent{{ParentEventID: createdEventID, EdgeKind: kernel.EdgeCausal}}, nil, "story-authorize-"+string(storyID))
	if err != nil {
		return err
	}
	planning, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.story.begin-planning", kernel.SchemaVersion, kernel.AggregateStory, storyID, feature.SubmittedBy, 2, mustJSON(map[string]any{"accountable_owner_fqn": feature.ProductOwnerActor, "planning_budget": uint64(feature.Input.MaximumTasks)}), []kernel.DagParent{{ParentEventID: authorized.EventIDs[0], EdgeKind: kernel.EdgeCausal}}, nil, "story-planning-"+string(storyID))
	if err != nil {
		return err
	}
	_, err = service.submitDeterministicCommand(ctx, feature, "tekroo.command.story.activate", kernel.SchemaVersion, kernel.AggregateStory, storyID, feature.SubmittedBy, 3, mustJSON(map[string]any{"plan_digest": digestBytes([]byte(string(feature.ID) + "\x00" + string(storyID))), "required_task_ids": taskIDs}), []kernel.DagParent{{ParentEventID: planning.EventIDs[0], EdgeKind: kernel.EdgeCausal}}, nil, "story-activate-"+string(storyID))
	return err
}

func mustJSON(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

func (service *ProductionService) ensureFeaturePlanningStory(ctx context.Context, feature organization.FeatureRequest) (kernel.CommandReceipt, error) {
	id := deterministicOperationalUUID("feature-planning-story", string(feature.ID))
	payload, err := json.Marshal(map[string]any{
		"title":               "Plan feature: " + feature.Input.Title,
		"description":         feature.Input.Description,
		"acceptance_criteria": feature.Input.AcceptanceCriteria,
	})
	if err != nil {
		return kernel.CommandReceipt{}, err
	}
	return service.submitPlannedCommand(ctx, feature, "tekroo.command.story.create", kernel.AggregateStory, id, "planning-story", payload, nil)
}

func (service *ProductionService) submitPlannedCommand(ctx context.Context, feature organization.FeatureRequest, commandType string, kind kernel.AggregateKind, id kernel.UUIDv7, label string, payload []byte, parents []kernel.DagParent) (kernel.CommandReceipt, error) {
	commandID, err := service.ids.Next()
	if err != nil {
		return kernel.CommandReceipt{}, err
	}
	now := service.clock.Now().UTC()
	command := kernel.KernelCommand{
		ContractManifest:          kernel.ContractIdentity,
		CommandID:                 commandID,
		CommandType:               commandType,
		CommandVersion:            kernel.SchemaVersion,
		Target:                    kernel.AggregateRef{Kind: kind, ID: id},
		Authority:                 feature.SubmittedBy,
		ExpectedRevision:          kernel.MustNotExist(),
		Preconditions:             []kernel.AggregatePrecondition{},
		ExpectedPolicyRevision:    service.provenance.PolicyRevision,
		ExpectedCatalogueRevision: kernel.CatalogueRevision,
		IdempotencyKey:            fmt.Sprintf("feature:%s:%s:%s:create", feature.ID, label, id),
		CorrelationID:             feature.ID,
		Causation:                 append([]kernel.DagParent(nil), parents...),
		IssuedAt:                  timePointer(now),
		Payload:                   payload,
		EvidenceRefs:              []kernel.EvidenceRef{},
	}
	receipt, err := service.Submit(ctx, command)
	if err != nil {
		return receipt, err
	}
	if receipt.OutcomeCode != kernel.OutcomeApplied && receipt.OutcomeCode != kernel.OutcomeNoChange {
		return receipt, fmt.Errorf("%s rejected: %s", commandType, receipt.ReasonCode)
	}
	return receipt, nil
}

func timePointer(value time.Time) *time.Time { return &value }

var _ organization.FeaturePlanMaterializer = (*ProductionService)(nil)
