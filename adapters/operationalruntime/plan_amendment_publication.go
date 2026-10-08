package operationalruntime

import (
	"context"
	"errors"
	"fmt"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func (service *ProductionService) publishPlanAmendment(ctx context.Context, feature organization.FeatureRequest) error {
	a := feature.PlanAmendment
	if a == nil || a.State != organization.AmendmentApproved || service.planning.PlanAmendment == nil {
		return organization.ErrPlanAmendment
	}
	w, _, _, err := service.amendmentWitness(ctx, feature, a.BlockedInvocationID, a.ConsultationID)
	if err != nil {
		return err
	}
	proposal := a.Proposal
	assessment, found := w.Invocations[kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: a.AssessmentInvocationID}]
	if !found || assessment.State != kernel.InvocationSucceeded || assessment.OutputDigest == nil || *assessment.OutputDigest != a.AssessmentOutputDigest {
		return organization.ErrPlanAmendment
	}
	w.PM, w.PMExecution = assessment.ActorFQN, assessment.Execution
	policy := service.planning.PlanAmendment.Policy
	if decision := organization.RevalidatePlanAmendment(feature, w, policy); !decision.Accepted {
		return fmt.Errorf("amendment publication requires operator: %s: %w", decision.Code, organization.ErrPlanAmendment)
	}
	// Capture the predecessor before publishing. No implementation is changed:
	// this is an immutable byte checkpoint in a different repository.
	shadow := feature
	shadowRecord := *a
	shadowRecord.State = organization.AmendmentApplied
	shadow.PlanAmendment, shadow.Plan = &shadowRecord, &proposal.SuccessorPlan
	for _, task := range proposal.SuccessorPlan.Tasks {
		if task.Purpose == kernel.PurposeImplementation {
			if _, err := service.amendmentCheckpointComponent(ctx, shadow, task); err != nil {
				return err
			}
		}
	}
	if err := service.materializeAmendmentSuccessors(ctx, feature); err != nil {
		return err
	}
	// Preparation is restartable, but it is not publication/admission. Read all
	// state again and CAS the visible plan under a complete graph/budget fence.
	w, _, fence, err := service.amendmentWitness(ctx, feature, a.BlockedInvocationID, a.ConsultationID)
	if err != nil {
		return err
	}
	w.PM, w.PMExecution = assessment.ActorFQN, assessment.Execution
	guarded := make(map[kernel.AggregateRef]bool)
	for _, guard := range fence.Preconditions {
		guarded[guard.Aggregate] = true
	}
	for _, task := range proposal.SuccessorPlan.Tasks {
		ref := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}
		state, _, exists, err := service.Store.ReadAggregateHead(ctx, ref)
		if err != nil || !exists {
			return errors.Join(organization.ErrPlanAmendment, err)
		}
		if !guarded[ref] {
			if state.Phase != kernel.PhasePlanned || state.Condition != kernel.ConditionRunnable {
				return organization.ErrPlanAmendment
			}
			fence.Preconditions = append(fence.Preconditions, kernel.AggregatePrecondition{Aggregate: ref, Expected: kernel.NewExpectedRevision(state.Revision)})
		}
	}
	next, err := organization.PublishPlanAmendment(feature, w, policy)
	if err != nil {
		return err
	}
	_, err = service.Store.CompareAndSwapPlanAmendment(ctx, next, feature.Revision, fence)
	return err
}

// No story reactivation, retained-task command, assignment or invocation is
// performed here. Only fresh successors are created/classified/messaged. Their
// execution frontier becomes visible after the separately fenced publication.
func (service *ProductionService) materializeAmendmentSuccessors(ctx context.Context, feature organization.FeatureRequest) error {
	a := feature.PlanAmendment
	plan := a.Proposal.SuccessorPlan
	retained := make(map[kernel.UUIDv7]bool)
	for _, task := range a.PriorPlan.Tasks {
		retained[task.ID] = true
	}
	selected := make(map[kernel.UUIDv7]bool)
	events := make(map[kernel.UUIDv7]kernel.UUIDv7)
	stories := make(map[kernel.UUIDv7]kernel.UUIDv7)
	for _, story := range plan.Stories {
		state, _, exists, err := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateStory, ID: story.ID})
		if err != nil || !exists || state.Phase != kernel.PhaseActive {
			return errors.Join(organization.ErrPlanAmendment, err)
		}
		event, exists, err := service.Store.ReadAggregateEventAtRevision(ctx, kernel.AggregateRef{Kind: kernel.AggregateStory, ID: story.ID}, 1)
		if err != nil || !exists || event.EventType != "tekroo.event.story.created" {
			return errors.Join(organization.ErrPlanAmendment, err)
		}
		stories[story.ID] = event.EventID
	}
	pending := make(map[kernel.UUIDv7]organization.PlannedTask)
	for _, task := range plan.Tasks {
		if retained[task.ID] {
			event, exists, err := service.Store.ReadAggregateEventAtRevision(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}, 1)
			if err != nil || !exists {
				return errors.Join(organization.ErrPlanAmendment, err)
			}
			if event.EventType != "tekroo.event.task.created" {
				return organization.ErrPlanAmendment
			}
			events[task.ID] = event.EventID
		} else {
			selected[task.ID] = true
			pending[task.ID] = task
		}
	}
	for len(pending) > 0 {
		advanced := false
		for _, task := range plan.Tasks {
			if _, exists := pending[task.ID]; !exists {
				continue
			}
			parents := []kernel.DagParent{{ParentEventID: stories[task.StoryID], EdgeKind: kernel.EdgeCausal}}
			ready := true
			for _, dependency := range task.DependsOn {
				event, exists := events[dependency]
				if !exists {
					ready = false
					break
				}
				parents = append(parents, kernel.DagParent{ParentEventID: event, EdgeKind: kernel.EdgeCausal})
			}
			if !ready {
				continue
			}
			// Idempotency comparison includes the immutable task payload; use the
			// original create receipt on replay, not a new ownership transition.
			payload := mustJSON(map[string]any{"story_id": task.StoryID, "title": task.Title, "description": task.Description, "acceptance_criteria": task.AcceptanceCriteria, "depends_on": append([]kernel.UUIDv7{}, task.DependsOn...)})
			receipt, err := service.submitPlannedCommand(ctx, feature, "tekroo.command.task.create", kernel.AggregateTask, task.ID, "amendment-successor", payload, parents)
			if err != nil || len(receipt.EventIDs) != 1 {
				return errors.Join(organization.ErrPlanAmendment, err)
			}
			events[task.ID] = receipt.EventIDs[0]
			delete(pending, task.ID)
			advanced = true
		}
		if !advanced {
			return organization.ErrPlanAmendment
		}
	}
	return service.preparePlannedTaskSubset(ctx, feature, plan, stories, events, selected)
}

func (service *ProductionService) ensurePublishedAmendmentMessages(ctx context.Context, feature organization.FeatureRequest) error {
	a := feature.PlanAmendment
	retained := make(map[kernel.UUIDv7]bool)
	for _, task := range a.PriorPlan.Tasks {
		retained[task.ID] = true
	}
	selected := make(map[kernel.UUIDv7]bool)
	events := make(map[kernel.UUIDv7]kernel.UUIDv7)
	for _, task := range feature.Plan.Tasks {
		event, exists, err := service.Store.ReadAggregateEventAtRevision(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}, 1)
		if err != nil || !exists || event.EventType != "tekroo.event.task.created" {
			return errors.Join(organization.ErrPlanAmendment, err)
		}
		events[task.ID] = event.EventID
		if !retained[task.ID] {
			selected[task.ID] = true
		}
	}
	return service.ensurePlannedTaskMessageSubset(ctx, feature, *feature.Plan, events, organization.PlanAmendmentDigest(*feature.Plan), a.DeadlineAt, selected)
}
