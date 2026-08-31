package operationalruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func (service *ProductionService) SubmitFeature(ctx context.Context, principal kernel.PrincipalRef, input organization.FeatureRequestInput) (organization.FeatureRequest, bool, error) {
	if service == nil || service.Features == nil {
		return organization.FeatureRequest{}, false, organization.ErrInvalidFeature
	}
	return service.Features.Submit(ctx, principal, input)
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

// MaterializeFeaturePlan creates only canonical story/task aggregates. It is
// idempotent: stable per-feature idempotency keys recover prior receipts after
// an interrupted materialization.
func (service *ProductionService) MaterializeFeaturePlan(ctx context.Context, feature organization.FeatureRequest, plan organization.FeaturePlan) error {
	if service == nil || service.Runtime == nil || service.ids == nil || service.clock == nil || plan.Validate(feature) != nil {
		return organization.ErrInvalidFeature
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
			payload, err := json.Marshal(map[string]any{"story_id": task.StoryID, "title": task.Title, "description": task.Description, "acceptance_criteria": task.AcceptanceCriteria, "depends_on": task.DependsOn})
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
	return nil
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
	return service.Submit(ctx, command)
}

func timePointer(value time.Time) *time.Time { return &value }

var _ organization.FeaturePlanMaterializer = (*ProductionService)(nil)
