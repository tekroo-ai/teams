package organization

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

const maximumWorkflowDefinitionBytes = 4 << 20

var ErrWorkflowDefinitionNotFound = errors.New("workflow definition not found")

var workflowStagePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{0,126}[a-z0-9])?$`)

func LoadWorkflowDefinition(path string, expectedDigest kernel.Digest) (kernel.WorkflowDefinition, error) {
	if !filepath.IsAbs(path) || !expectedDigest.Valid() {
		return kernel.WorkflowDefinition{}, kernel.ErrInvalidWorkflowDefinition
	}
	raw, err := readBounded(path, maximumWorkflowDefinitionBytes)
	if err != nil {
		return kernel.WorkflowDefinition{}, fmt.Errorf("%w: %v", kernel.ErrInvalidWorkflowDefinition, err)
	}
	var definition kernel.WorkflowDefinition
	if err := decodeStrict(raw, &definition); err != nil || definition.Validate() != nil {
		return kernel.WorkflowDefinition{}, kernel.ErrInvalidWorkflowDefinition
	}
	if definition.ContentDigest != expectedDigest {
		return kernel.WorkflowDefinition{}, fmt.Errorf("%w: content digest mismatch", kernel.ErrInvalidWorkflowDefinition)
	}
	return definition, nil
}

type WorkflowLibrary struct {
	mu          sync.RWMutex
	definitions map[string]kernel.WorkflowDefinition
}

func NewWorkflowLibrary() *WorkflowLibrary {
	return &WorkflowLibrary{definitions: make(map[string]kernel.WorkflowDefinition)}
}

func (library *WorkflowLibrary) Add(definition kernel.WorkflowDefinition) error {
	if library == nil || definition.Validate() != nil {
		return kernel.ErrInvalidWorkflowDefinition
	}
	key := workflowDefinitionKey(definition.Name, definition.Version)
	library.mu.Lock()
	defer library.mu.Unlock()
	if current, exists := library.definitions[key]; exists && current.ContentDigest != definition.ContentDigest {
		return kernel.ErrInvalidWorkflowDefinition
	}
	library.definitions[key] = definition.Clone()
	return nil
}

func (library *WorkflowLibrary) Lookup(name, version string) (kernel.WorkflowDefinition, error) {
	if library == nil {
		return kernel.WorkflowDefinition{}, ErrWorkflowDefinitionNotFound
	}
	library.mu.RLock()
	definition, exists := library.definitions[workflowDefinitionKey(name, version)]
	library.mu.RUnlock()
	if !exists {
		return kernel.WorkflowDefinition{}, ErrWorkflowDefinitionNotFound
	}
	return definition.Clone(), nil
}

func (library *WorkflowLibrary) LookupTrigger(trigger string) (kernel.WorkflowDefinition, error) {
	if library == nil || trigger == "" {
		return kernel.WorkflowDefinition{}, ErrWorkflowDefinitionNotFound
	}
	library.mu.RLock()
	defer library.mu.RUnlock()
	var match kernel.WorkflowDefinition
	found := false
	for _, definition := range library.definitions {
		if !slices.Contains(definition.TriggerTypes, trigger) {
			continue
		}
		if found {
			return kernel.WorkflowDefinition{}, kernel.ErrInvalidWorkflowDefinition
		}
		match, found = definition, true
	}
	if !found {
		return kernel.WorkflowDefinition{}, ErrWorkflowDefinitionNotFound
	}
	return match.Clone(), nil
}

func workflowDefinitionKey(name, version string) string {
	return name + "\x00" + version
}

func MessageCreatesWorkflowWork(message OrganizationalMessage) bool {
	return message.Purpose == PurposeRequest || message.Purpose == PurposeHandoff
}

func WorkProposalFromMessage(message OrganizationalMessage, proposalID kernel.UUIDv7, execution kernel.ExecutionTuple, causationEventIDs, inputEvidenceIDs []kernel.UUIDv7, proposedAt time.Time) (kernel.WorkProposal, error) {
	if message.Validate() != nil || !MessageCreatesWorkflowWork(message) || message.Work.WorkflowInstanceID == nil || message.Work.WorkflowStageID == "" {
		return kernel.WorkProposal{}, kernel.ErrInvalidWorkProposal
	}
	proposal := kernel.WorkProposal{
		SchemaVersion: kernel.WorkProposalSchemaVersion, ProposalID: proposalID, MessageID: message.ID,
		WorkflowInstanceID: *message.Work.WorkflowInstanceID, StageID: message.Work.WorkflowStageID, NodeID: message.Work.DAGNodeID,
		ActorFQN: message.Recipient, Execution: execution, BudgetAccountID: message.Flow.BudgetAccountID,
		CausationEventIDs: append([]kernel.UUIDv7(nil), causationEventIDs...), InputEvidenceIDs: append([]kernel.UUIDv7(nil), inputEvidenceIDs...), ProposedAt: proposedAt,
	}
	if !proposal.Valid() {
		return kernel.WorkProposal{}, kernel.ErrInvalidWorkProposal
	}
	return proposal, nil
}

type WorkflowAdmissionRequest struct {
	Message                OrganizationalMessage
	Definition             kernel.WorkflowDefinition
	Proposal               kernel.WorkProposal
	Facts                  kernel.WorkflowAdmissionFacts
	RecordedEventID        kernel.UUIDv7
	AuthorizedInvocationID kernel.UUIDv7
	IntentID               kernel.UUIDv7
	RecordedAt             time.Time
}

func (request WorkflowAdmissionRequest) Valid() bool {
	return request.Message.Validate() == nil && MessageCreatesWorkflowWork(request.Message) && request.Definition.Validate() == nil && request.Proposal.Valid() && request.Proposal.MessageID == request.Message.ID && request.Proposal.ActorFQN == request.Message.Recipient && request.Proposal.NodeID == request.Message.Work.DAGNodeID && request.Message.Work.WorkflowInstanceID != nil && request.Proposal.WorkflowInstanceID == *request.Message.Work.WorkflowInstanceID && request.Proposal.StageID == request.Message.Work.WorkflowStageID && request.Proposal.BudgetAccountID == request.Message.Flow.BudgetAccountID && request.RecordedEventID.Valid() && request.AuthorizedInvocationID.Valid() && request.IntentID.Valid() && !request.RecordedAt.IsZero()
}

type WorkflowMessageAdmissionStore interface {
	BindMessageToWorkflow(context.Context, kernel.UUIDv7, kernel.WorkflowDefinition, kernel.WorkflowInstance, string) (OrganizationalMessage, error)
	CreateWorkflowInstance(context.Context, kernel.WorkflowDefinition, kernel.WorkflowInstance) error
	ReadWorkflowInstance(context.Context, kernel.UUIDv7) (kernel.WorkflowInstance, bool, error)
	ReadWorkflowInstanceByBudget(context.Context, kernel.UUIDv7) (kernel.WorkflowInstance, bool, error)
	AdmitWorkflowMessage(context.Context, WorkflowAdmissionRequest) (kernel.WorkAdmissionResult, bool, error)
	WorkflowAdmission(context.Context, kernel.UUIDv7) (kernel.WorkAdmissionResult, bool, error)
	ApplyWorkflowTransition(context.Context, kernel.WorkflowDefinition, kernel.UUIDv7, kernel.WorkflowTransition) (kernel.WorkflowInstance, error)
	ExpandWorkflowNode(context.Context, kernel.WorkflowDefinition, kernel.UUIDv7, kernel.UUIDv7, kernel.UUIDv7, []kernel.WorkflowNodeSpec) (kernel.WorkflowInstance, error)
}
