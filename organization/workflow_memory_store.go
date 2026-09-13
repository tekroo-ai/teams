package organization

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/tekroo-ai/teams/kernel"
)

func (store *MemoryOrganizationalMessageStore) BindMessageToWorkflow(ctx context.Context, messageID kernel.UUIDv7, definition kernel.WorkflowDefinition, initial kernel.WorkflowInstance, stageID string) (OrganizationalMessage, error) {
	if err := ctx.Err(); err != nil {
		return OrganizationalMessage{}, err
	}
	if store == nil || !messageID.Valid() || definition.Validate() != nil || initial.Validate(definition) != nil {
		return OrganizationalMessage{}, kernel.ErrInvalidWorkflowInstance
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.ensureWorkflowMapsLocked()
	claim, found := store.messages[messageID]
	if !found || claim.State != MessagePending {
		return OrganizationalMessage{}, ErrOrganizationalMessageConflict
	}
	message := claim.Message
	if message.Work.WorkflowInstanceID != nil {
		if *message.Work.WorkflowInstanceID != initial.InstanceID || message.Work.WorkflowStageID != stageID {
			return OrganizationalMessage{}, ErrOrganizationalMessageConflict
		}
		return message, nil
	}
	current, exists := store.workflows[initial.InstanceID]
	if !exists {
		if initial.LastEventID == nil || *initial.LastEventID != messageID {
			return OrganizationalMessage{}, kernel.ErrInvalidWorkflowInstance
		}
		if rootInstance, duplicate := store.workflowRoots[initial.RootRequest]; duplicate && rootInstance != initial.InstanceID {
			return OrganizationalMessage{}, ErrOrganizationalMessageConflict
		}
		current = initial.Clone()
		store.workflows[current.InstanceID] = current
		store.workflowRoots[current.RootRequest] = current.InstanceID
	} else {
		if current.Validate(definition) != nil || current.DefinitionDigest != definition.ContentDigest {
			return OrganizationalMessage{}, kernel.ErrInvalidWorkflowInstance
		}
		placeholder := memoryWorkflowNodeForStage(current, stageID)
		if placeholder == nil {
			return OrganizationalMessage{}, kernel.ErrInvalidWorkflowTransition
		}
		if placeholder.NodeID != message.Work.DAGNodeID {
			next, err := kernel.ExpandWorkflowNode(definition, current, placeholder.NodeID, messageID, []kernel.WorkflowNodeSpec{{NodeID: message.Work.DAGNodeID, PredecessorNodeIDs: placeholder.PredecessorNodeIDs, InputEvidenceIDs: placeholder.InputEvidenceIDs}})
			if err != nil {
				return OrganizationalMessage{}, err
			}
			current = next
			store.workflows[current.InstanceID] = current
		}
	}
	message.Work.WorkflowInstanceID = &current.InstanceID
	message.Work.WorkflowStageID = stageID
	if message.Validate() != nil {
		return OrganizationalMessage{}, ErrInvalidOrganizationalMessage
	}
	claim.Message = message
	store.messages[messageID] = claim
	store.workflowEvents[messageID] = digestJSON(message.Work)
	return message, nil
}

func memoryWorkflowNodeForStage(instance kernel.WorkflowInstance, stageID string) *kernel.WorkflowNode {
	for index := range instance.Nodes {
		node := &instance.Nodes[index]
		if node.StageID == stageID && (node.State == kernel.WorkflowNodePending || node.State == kernel.WorkflowNodeReady) {
			return node
		}
	}
	return nil
}

func (store *MemoryOrganizationalMessageStore) CreateWorkflowInstance(ctx context.Context, definition kernel.WorkflowDefinition, instance kernel.WorkflowInstance) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if store == nil || instance.Validate(definition) != nil {
		return kernel.ErrInvalidWorkflowInstance
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.ensureWorkflowMapsLocked()
	if rootInstance, found := store.workflowRoots[instance.RootRequest]; found && rootInstance != instance.InstanceID {
		return ErrOrganizationalMessageConflict
	}
	if existing, found := store.workflows[instance.InstanceID]; found {
		if existing.DefinitionDigest == instance.DefinitionDigest && existing.Revision == instance.Revision {
			return nil
		}
		return ErrOrganizationalMessageConflict
	}
	store.workflows[instance.InstanceID] = instance.Clone()
	store.workflowRoots[instance.RootRequest] = instance.InstanceID
	return nil
}

func (store *MemoryOrganizationalMessageStore) ReadWorkflowInstance(ctx context.Context, id kernel.UUIDv7) (kernel.WorkflowInstance, bool, error) {
	if err := ctx.Err(); err != nil {
		return kernel.WorkflowInstance{}, false, err
	}
	if store == nil || !id.Valid() {
		return kernel.WorkflowInstance{}, false, kernel.ErrInvalidWorkflowInstance
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.ensureWorkflowMapsLocked()
	instance, found := store.workflows[id]
	return instance.Clone(), found, nil
}

func (store *MemoryOrganizationalMessageStore) ReadWorkflowInstanceByBudget(ctx context.Context, budgetID kernel.UUIDv7) (kernel.WorkflowInstance, bool, error) {
	if err := ctx.Err(); err != nil {
		return kernel.WorkflowInstance{}, false, err
	}
	if store == nil || !budgetID.Valid() {
		return kernel.WorkflowInstance{}, false, kernel.ErrInvalidWorkflowInstance
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.ensureWorkflowMapsLocked()
	var match kernel.WorkflowInstance
	found := false
	for _, instance := range store.workflows {
		if instance.BudgetAccountID != budgetID {
			continue
		}
		if found && match.InstanceID != instance.InstanceID {
			return kernel.WorkflowInstance{}, false, kernel.ErrInvalidWorkflowInstance
		}
		match, found = instance.Clone(), true
	}
	return match, found, nil
}

func (store *MemoryOrganizationalMessageStore) AdmitWorkflowMessage(ctx context.Context, request WorkflowAdmissionRequest) (kernel.WorkAdmissionResult, bool, error) {
	if err := ctx.Err(); err != nil {
		return kernel.WorkAdmissionResult{}, false, err
	}
	if store == nil || !request.Valid() {
		return kernel.WorkAdmissionResult{}, false, kernel.ErrInvalidWorkProposal
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.ensureWorkflowMapsLocked()
	if prior, found := store.workflowAdmissions[request.Message.ID]; found {
		proposal := store.workflowProposals[request.Message.ID]
		if !proposalEqual(proposal, request.Proposal) {
			return kernel.WorkAdmissionResult{}, false, ErrOrganizationalMessageConflict
		}
		return prior, true, nil
	}
	claim, found := store.messages[request.Message.ID]
	if !found || claim.State != MessagePending || claim.Message.ID != request.Message.ID || claim.Message.Recipient != request.Proposal.ActorFQN {
		return kernel.WorkAdmissionResult{}, false, ErrOrganizationalMessageConflict
	}
	current, found := store.workflows[request.Proposal.WorkflowInstanceID]
	if !found || current.DefinitionDigest != request.Definition.ContentDigest {
		return kernel.WorkAdmissionResult{}, false, kernel.ErrInvalidWorkflowInstance
	}
	evaluation, err := kernel.EvaluateWorkflowAdmission(request.Definition, current, request.Proposal, request.Facts, request.RecordedEventID, request.AuthorizedInvocationID, request.RecordedAt)
	if err != nil {
		return kernel.WorkAdmissionResult{}, false, err
	}
	resultBytes, err := json.Marshal(evaluation.Result)
	if err != nil {
		return kernel.WorkAdmissionResult{}, false, err
	}
	digest := sha256.Sum256(resultBytes)
	claim.State = MessageResolved
	claim.Resolution = string(evaluation.Result.ReasonCode)
	claim.Evidence = kernel.Digest(hex.EncodeToString(digest[:]))
	store.messages[request.Message.ID] = claim
	store.workflows[current.InstanceID] = evaluation.NextInstance.Clone()
	store.workflowProposals[request.Message.ID] = comparableWorkProposal(request.Proposal)
	store.workflowAdmissions[request.Message.ID] = evaluation.Result
	store.workflowEvents[request.RecordedEventID] = digestJSON(evaluation.Result)
	if evaluation.Result.Outcome == kernel.WorkAdmitted {
		store.workflowIntents[request.IntentID] = kernel.OutboxIntent{IntentID: request.IntentID, EventID: request.RecordedEventID, Kind: "WORKFLOW_WORK_ADMITTED"}
	}
	return evaluation.Result, false, nil
}

func comparableWorkProposal(proposal kernel.WorkProposal) kernel.WorkProposal {
	copy := proposal
	copy.CausationEventIDs = append([]kernel.UUIDv7(nil), proposal.CausationEventIDs...)
	copy.InputEvidenceIDs = append([]kernel.UUIDv7(nil), proposal.InputEvidenceIDs...)
	return copy
}

func proposalEqual(left, right kernel.WorkProposal) bool {
	leftBytes, leftErr := json.Marshal(left)
	rightBytes, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftBytes) == string(rightBytes)
}

func (store *MemoryOrganizationalMessageStore) ensureWorkflowMapsLocked() {
	if store.workflows == nil {
		store.workflows = make(map[kernel.UUIDv7]kernel.WorkflowInstance)
	}
	if store.workflowProposals == nil {
		store.workflowProposals = make(map[kernel.UUIDv7]kernel.WorkProposal)
	}
	if store.workflowAdmissions == nil {
		store.workflowAdmissions = make(map[kernel.UUIDv7]kernel.WorkAdmissionResult)
	}
	if store.workflowIntents == nil {
		store.workflowIntents = make(map[kernel.UUIDv7]kernel.OutboxIntent)
	}
	if store.workflowRoots == nil {
		store.workflowRoots = make(map[kernel.AggregateRef]kernel.UUIDv7)
	}
	if store.workflowEvents == nil {
		store.workflowEvents = make(map[kernel.UUIDv7]kernel.Digest)
	}
}

func (store *MemoryOrganizationalMessageStore) WorkflowIntentCount() int {
	if store == nil {
		return 0
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.ensureWorkflowMapsLocked()
	return len(store.workflowIntents)
}

func (store *MemoryOrganizationalMessageStore) WorkflowAdmission(ctx context.Context, messageID kernel.UUIDv7) (kernel.WorkAdmissionResult, bool, error) {
	if err := ctx.Err(); err != nil {
		return kernel.WorkAdmissionResult{}, false, err
	}
	if !messageID.Valid() {
		return kernel.WorkAdmissionResult{}, false, errors.New("invalid message id")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.ensureWorkflowMapsLocked()
	result, found := store.workflowAdmissions[messageID]
	return result, found, nil
}

func (store *MemoryOrganizationalMessageStore) ApplyWorkflowTransition(ctx context.Context, definition kernel.WorkflowDefinition, instanceID kernel.UUIDv7, transition kernel.WorkflowTransition) (kernel.WorkflowInstance, error) {
	if err := ctx.Err(); err != nil {
		return kernel.WorkflowInstance{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.ensureWorkflowMapsLocked()
	current, found := store.workflows[instanceID]
	if !found {
		return kernel.WorkflowInstance{}, kernel.ErrInvalidWorkflowInstance
	}
	eventDigest := digestJSON(transition)
	if prior, exists := store.workflowEvents[transition.EventID]; exists {
		if prior != eventDigest {
			return kernel.WorkflowInstance{}, ErrOrganizationalMessageConflict
		}
		return current.Clone(), nil
	}
	next, err := kernel.FoldWorkflowInstance(definition, current, []kernel.WorkflowTransition{transition})
	if err != nil {
		return kernel.WorkflowInstance{}, err
	}
	store.workflows[instanceID] = next.Clone()
	store.workflowEvents[transition.EventID] = eventDigest
	return next, nil
}

func (store *MemoryOrganizationalMessageStore) ExpandWorkflowNode(ctx context.Context, definition kernel.WorkflowDefinition, instanceID, placeholderID, eventID kernel.UUIDv7, specs []kernel.WorkflowNodeSpec) (kernel.WorkflowInstance, error) {
	if err := ctx.Err(); err != nil {
		return kernel.WorkflowInstance{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.ensureWorkflowMapsLocked()
	current, found := store.workflows[instanceID]
	if !found {
		return kernel.WorkflowInstance{}, kernel.ErrInvalidWorkflowInstance
	}
	eventDigest := digestJSON(specs)
	if prior, exists := store.workflowEvents[eventID]; exists {
		if prior != eventDigest {
			return kernel.WorkflowInstance{}, ErrOrganizationalMessageConflict
		}
		return current.Clone(), nil
	}
	next, err := kernel.ExpandWorkflowNode(definition, current, placeholderID, eventID, specs)
	if err != nil {
		return kernel.WorkflowInstance{}, err
	}
	store.workflows[instanceID] = next.Clone()
	store.workflowEvents[eventID] = eventDigest
	return next, nil
}

func digestJSON(value any) kernel.Digest {
	raw, _ := json.Marshal(value)
	digest := sha256.Sum256(raw)
	return kernel.Digest(hex.EncodeToString(digest[:]))
}
