package operationalruntime

import (
	"context"
	"errors"
	"fmt"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func (service *ProductionService) workflowPlanningStageDefinition(stage featurePlanningStage) (string, kernel.WorkPurpose, kernel.DecisionRoute, string, string, []string, error) {
	if service == nil || service.WorkflowLibrary == nil {
		return legacyPlanningStageDefinition(stage)
	}
	workflowStageID, ok := map[featurePlanningStage]string{stageRefinement: "refine", stageSpecification: "specify", stageArchitecture: "design"}[stage]
	if !ok {
		return "", "", "", "", "", nil, organization.ErrInvalidFeature
	}
	definition, err := service.WorkflowLibrary.LookupTrigger("tekroo.message.feature.submitted")
	if err != nil {
		return "", "", "", "", "", nil, err
	}
	workflowStage, found := configuredWorkflowStage(definition, workflowStageID)
	if !found || len(workflowStage.PreferredFQRNs) == 0 || workflowStage.CompletionCondition == "" {
		return "", "", "", "", "", nil, kernel.ErrInvalidWorkflowDefinition
	}
	purpose := kernel.WorkPurpose(workflowStage.Purpose)
	if !purpose.Valid() {
		return "", "", "", "", "", nil, kernel.ErrInvalidWorkflowDefinition
	}
	route := kernel.RouteBoundedExecution
	if workflowStage.Risk == kernel.WorkflowRiskHigh || workflowStage.Risk == kernel.WorkflowRiskCritical || workflowStage.ComplexityMinimum >= 2 {
		route = kernel.RouteComplexReasoning
	}
	title := fmt.Sprintf("%s: %s", workflowStage.StageID, definition.Name)
	description := fmt.Sprintf("Perform workflow stage %q using the immutable role bundle. Input schema: %s. Output schema: %s. Completion contract: %s", workflowStage.StageID, workflowStage.InputSchema, workflowStage.OutputSchema, workflowStage.CompletionCondition)
	return string(workflowStage.PreferredFQRNs[0]), purpose, route, title, description, []string{workflowStage.CompletionCondition}, nil
}

func configuredWorkflowStage(definition kernel.WorkflowDefinition, stageID string) (kernel.WorkflowStageDefinition, bool) {
	for _, stage := range definition.Stages {
		if stage.StageID == stageID {
			return stage, true
		}
	}
	return kernel.WorkflowStageDefinition{}, false
}

// workflowInvocationInAdmissionFamily keeps the workflow node bound to the
// invocation that admitted the stage while allowing task-level, changed-
// condition retries to finish that same stage. The task invocation chain
// retains the exact attempt provenance.
func workflowInvocationInAdmissionFamily(current kernel.WorkInvocation, admitted kernel.UUIDv7, invocations map[kernel.AggregateRef]kernel.WorkInvocation) bool {
	visited := make(map[kernel.UUIDv7]struct{})
	for {
		if current.ID == admitted {
			return true
		}
		if _, seen := visited[current.ID]; seen {
			return false
		}
		visited[current.ID] = struct{}{}
		if current.RetryOfInvocationID == nil {
			return false
		}
		prior, found := invocations[kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: *current.RetryOfInvocationID}]
		if !found {
			return false
		}
		current = prior
	}
}

func planningInvocationUsesCurrentProfile(snapshot kernel.Snapshot, taskID kernel.UUIDv7, invocation kernel.WorkInvocation) bool {
	profile, found := snapshot.WorkProfiles[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: taskID}]
	return found && profile.Profile.Binding() == invocation.WorkProfile
}

func (service *ProductionService) prepareFeatureWorkflow(ctx context.Context, feature organization.FeatureRequest) error {
	if service == nil || service.WorkflowLibrary == nil {
		return nil
	}
	storyID, err := service.ensureFeaturePlanningStory(ctx, feature)
	if err != nil {
		return err
	}
	evidenceID, evidence, err := service.ensureFeaturePlanningEvidence(ctx, feature)
	if err != nil {
		return err
	}
	deadline := feature.CreatedAt.Add(service.planningDeadline)
	if _, err = service.ensureFeatureWorkBudget(ctx, feature, evidenceID, evidence, deadline); err != nil {
		return err
	}
	claim, found, err := service.MessageBus.Read(ctx, feature.LastMessageID)
	if err != nil || !found {
		return errors.Join(organization.ErrOrganizationalMessageNotFound, err)
	}
	return service.bindTriggeredMessageWorkflow(ctx, claim.Message, kernel.AggregateRef{Kind: kernel.AggregateStory, ID: storyID})
}

func (service *ProductionService) bindTriggeredMessageWorkflow(ctx context.Context, message organization.OrganizationalMessage, root kernel.AggregateRef) error {
	if service == nil || service.WorkflowLibrary == nil || service.Store == nil || message.Validate() != nil {
		return kernel.ErrInvalidWorkflowInstance
	}
	definition, err := service.WorkflowLibrary.LookupTrigger(message.Type)
	if errors.Is(err, organization.ErrWorkflowDefinitionNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !root.Valid() {
		return kernel.ErrInvalidWorkflowInstance
	}
	claim, found, err := service.MessageBus.Read(ctx, message.ID)
	if err != nil || !found {
		return errors.Join(organization.ErrOrganizationalMessageNotFound, err)
	}
	message = claim.Message
	if message.Work.WorkflowInstanceID != nil {
		return nil
	}
	nodeIDs := make(map[string]kernel.UUIDv7, len(definition.Stages))
	for _, stage := range definition.Stages {
		nodeIDs[stage.StageID] = deterministicOperationalUUID("workflow-node", string(message.Flow.BudgetAccountID), string(definition.ContentDigest), stage.StageID)
	}
	initialStage, err := initialWorkflowStage(definition)
	if err != nil {
		return err
	}
	nodeIDs[initialStage] = message.Work.DAGNodeID
	instanceID := deterministicOperationalUUID("workflow-instance", string(message.Flow.BudgetAccountID), string(definition.ContentDigest))
	instance, err := kernel.NewWorkflowInstance(definition, instanceID, root, message.Flow.BudgetAccountID, nodeIDs)
	if err != nil {
		return err
	}
	lastEvent := message.ID
	instance.LastEventID = &lastEvent
	_, err = service.Store.BindMessageToWorkflow(ctx, message.ID, definition, instance, initialStage)
	return err
}

func workflowRootFromMessage(message organization.OrganizationalMessage) (kernel.AggregateRef, bool) {
	if message.Work.TaskID != nil {
		return kernel.AggregateRef{Kind: kernel.AggregateTask, ID: *message.Work.TaskID}, true
	}
	if message.Work.StoryID != nil {
		return kernel.AggregateRef{Kind: kernel.AggregateStory, ID: *message.Work.StoryID}, true
	}
	return kernel.AggregateRef{}, false
}

func initialWorkflowStage(definition kernel.WorkflowDefinition) (string, error) {
	var stageID string
	for _, stage := range definition.Stages {
		if len(stage.DependsOn) != 0 {
			continue
		}
		if stageID != "" {
			return "", kernel.ErrInvalidWorkflowDefinition
		}
		stageID = stage.StageID
	}
	if stageID == "" {
		return "", kernel.ErrInvalidWorkflowDefinition
	}
	return stageID, nil
}

func workflowStageForFeatureStatus(status organization.FeatureStatus) (string, bool) {
	switch status {
	case organization.FeatureSubmitted:
		return "refine", true
	case organization.FeatureReadyForPlanning:
		return "specify", true
	case organization.FeatureSpecified:
		return "design", true
	default:
		return "", false
	}
}

func (service *ProductionService) featureWorkflowAdmission(ctx context.Context, feature organization.FeatureRequest) (kernel.WorkAdmissionResult, bool, error) {
	if service == nil || service.WorkflowLibrary == nil {
		return kernel.WorkAdmissionResult{}, false, nil
	}
	result, found, err := service.Store.WorkflowAdmission(ctx, feature.LastMessageID)
	if err != nil || !found || result.Outcome != kernel.WorkAdmitted {
		return result, found, err
	}
	return result, true, nil
}

func (service *ProductionService) bindCurrentFeatureWorkflowStage(ctx context.Context, feature organization.FeatureRequest) error {
	if service == nil || service.WorkflowLibrary == nil {
		return nil
	}
	claim, found, err := service.MessageBus.Read(ctx, feature.LastMessageID)
	if err != nil || !found {
		return errors.Join(organization.ErrOrganizationalMessageNotFound, err)
	}
	// A stage message may already be admitted or executing. Its durable
	// workflow coordinates are immutable, so there is nothing to repair.
	if claim.Message.Work.WorkflowInstanceID != nil {
		return nil
	}
	stageID, ok := workflowStageForFeatureStatus(feature.Status)
	if !ok {
		return nil
	}
	instance, found, err := service.Store.ReadWorkflowInstanceByBudget(ctx, feature.BudgetAccountID)
	if err != nil {
		return err
	}
	if !found {
		return service.prepareFeatureWorkflow(ctx, feature)
	}
	definition, err := service.WorkflowLibrary.Lookup(instance.DefinitionName, instance.DefinitionVersion)
	if err != nil || definition.ContentDigest != instance.DefinitionDigest {
		return errors.Join(kernel.ErrInvalidWorkflowDefinition, err)
	}
	_, err = service.Store.BindMessageToWorkflow(ctx, feature.LastMessageID, definition, instance, stageID)
	return err
}

func (service *ProductionService) startFeatureWorkflowStage(ctx context.Context, feature organization.FeatureRequest, admission kernel.WorkAdmissionResult) error {
	if service == nil || service.WorkflowLibrary == nil {
		return nil
	}
	instance, found, err := service.Store.ReadWorkflowInstanceByBudget(ctx, feature.BudgetAccountID)
	if err != nil || !found {
		return errors.Join(kernel.ErrInvalidWorkflowInstance, err)
	}
	for _, node := range instance.Nodes {
		if node.NodeID != admission.NodeID {
			continue
		}
		if node.State == kernel.WorkflowNodeRunning || node.State == kernel.WorkflowNodeCompleted {
			return nil
		}
		if node.State != kernel.WorkflowNodeAdmitted || admission.AuthorizedInvocationID == nil {
			return kernel.ErrInvalidWorkflowTransition
		}
		definition, lookupErr := service.WorkflowLibrary.Lookup(instance.DefinitionName, instance.DefinitionVersion)
		if lookupErr != nil {
			return lookupErr
		}
		eventID := deterministicOperationalUUID("workflow-start", string(instance.InstanceID), string(node.NodeID), string(*admission.AuthorizedInvocationID))
		_, err = service.Store.ApplyWorkflowTransition(ctx, definition, instance.InstanceID, kernel.WorkflowTransition{Revision: instance.Revision + 1, EventID: eventID, Kind: kernel.WorkflowTransitionStart, NodeID: node.NodeID, InvocationID: *admission.AuthorizedInvocationID, RecordedAt: service.clock.Now().UTC()})
		return err
	}
	return kernel.ErrInvalidWorkflowTransition
}

func (service *ProductionService) completeFeatureWorkflowStage(ctx context.Context, feature organization.FeatureRequest, admission kernel.WorkAdmissionResult, outputDigest kernel.Digest) error {
	if service == nil || service.WorkflowLibrary == nil {
		return nil
	}
	instance, found, err := service.Store.ReadWorkflowInstanceByBudget(ctx, feature.BudgetAccountID)
	if err != nil || !found {
		return errors.Join(kernel.ErrInvalidWorkflowInstance, err)
	}
	for _, node := range instance.Nodes {
		if node.NodeID != admission.NodeID {
			continue
		}
		if node.State == kernel.WorkflowNodeCompleted {
			return nil
		}
		if node.State != kernel.WorkflowNodeRunning || admission.AuthorizedInvocationID == nil || !outputDigest.Valid() {
			return kernel.ErrInvalidWorkflowTransition
		}
		definition, lookupErr := service.WorkflowLibrary.Lookup(instance.DefinitionName, instance.DefinitionVersion)
		if lookupErr != nil {
			return lookupErr
		}
		eventID := deterministicOperationalUUID("workflow-complete", string(instance.InstanceID), string(node.NodeID), string(outputDigest))
		_, err = service.Store.ApplyWorkflowTransition(ctx, definition, instance.InstanceID, kernel.WorkflowTransition{Revision: instance.Revision + 1, EventID: eventID, Kind: kernel.WorkflowTransitionComplete, NodeID: node.NodeID, InvocationID: *admission.AuthorizedInvocationID, EvidenceIDs: []kernel.UUIDv7{}, ProgressDigest: outputDigest, RecordedAt: service.clock.Now().UTC()})
		return err
	}
	return kernel.ErrInvalidWorkflowTransition
}
