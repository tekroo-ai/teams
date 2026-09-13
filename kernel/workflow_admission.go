package kernel

import (
	"errors"
	"slices"
	"time"
)

const WorkProposalSchemaVersion = "1.0.0"

var ErrInvalidWorkProposal = errors.New("invalid workflow work proposal")

type WorkProposal struct {
	SchemaVersion      string         `json:"schema_version"`
	ProposalID         UUIDv7         `json:"proposal_id"`
	MessageID          UUIDv7         `json:"message_id"`
	WorkflowInstanceID UUIDv7         `json:"workflow_instance_id"`
	StageID            string         `json:"stage_id"`
	NodeID             UUIDv7         `json:"node_id"`
	ActorFQN           ActorFQN       `json:"actor_fqn"`
	Execution          ExecutionTuple `json:"execution"`
	BudgetAccountID    UUIDv7         `json:"budget_account_id"`
	CausationEventIDs  []UUIDv7       `json:"causation_event_ids"`
	InputEvidenceIDs   []UUIDv7       `json:"input_evidence_ids"`
	ProposedAt         time.Time      `json:"proposed_at"`
}

func (proposal WorkProposal) Valid() bool {
	return proposal.SchemaVersion == WorkProposalSchemaVersion && proposal.ProposalID.Valid() && proposal.MessageID.Valid() && proposal.WorkflowInstanceID.Valid() && workflowNamePattern.MatchString(proposal.StageID) && proposal.NodeID.Valid() && proposal.ActorFQN.Valid() && proposal.Execution.Valid() && proposal.BudgetAccountID.Valid() && len(proposal.CausationEventIDs) > 0 && len(proposal.CausationEventIDs) <= 64 && workflowValidUUIDSet(proposal.CausationEventIDs) && len(proposal.InputEvidenceIDs) <= 1024 && workflowValidUUIDSet(proposal.InputEvidenceIDs) && !proposal.ProposedAt.IsZero()
}

type WorkAdmissionOutcome string

const (
	WorkAdmitted WorkAdmissionOutcome = "ADMITTED"
	WorkRejected WorkAdmissionOutcome = "REJECTED"
)

type WorkAdmissionReason string

const (
	AdmissionAccepted              WorkAdmissionReason = "ADMITTED"
	AdmissionUndeclaredTransition  WorkAdmissionReason = "UNDECLARED_TRANSITION"
	AdmissionPredecessorIncomplete WorkAdmissionReason = "PREDECESSOR_INCOMPLETE"
	AdmissionDuplicateNode         WorkAdmissionReason = "DUPLICATE_NODE"
	AdmissionCausationInvalid      WorkAdmissionReason = "CAUSATION_INVALID"
	AdmissionActorIneligible       WorkAdmissionReason = "ACTOR_INELIGIBLE"
	AdmissionBudgetExhausted       WorkAdmissionReason = "BUDGET_EXHAUSTED"
	AdmissionNoProgress            WorkAdmissionReason = "NO_PROGRESS"
	AdmissionWorkflowTerminal      WorkAdmissionReason = "WORKFLOW_TERMINAL"
)

type WorkAdmissionResult struct {
	SchemaVersion          string               `json:"schema_version"`
	ProposalID             UUIDv7               `json:"proposal_id"`
	WorkflowInstanceID     UUIDv7               `json:"workflow_instance_id"`
	NodeID                 UUIDv7               `json:"node_id"`
	Outcome                WorkAdmissionOutcome `json:"outcome"`
	ReasonCode             WorkAdmissionReason  `json:"reason_code"`
	RecordedEventID        UUIDv7               `json:"recorded_event_id"`
	AuthorizedInvocationID *UUIDv7              `json:"authorized_invocation_id,omitempty"`
	RecordedAt             time.Time            `json:"recorded_at"`
}

func (result WorkAdmissionResult) Valid() bool {
	if result.SchemaVersion != WorkProposalSchemaVersion || !result.ProposalID.Valid() || !result.WorkflowInstanceID.Valid() || !result.NodeID.Valid() || !result.RecordedEventID.Valid() || result.RecordedAt.IsZero() {
		return false
	}
	if result.Outcome == WorkAdmitted {
		return result.ReasonCode == AdmissionAccepted && result.AuthorizedInvocationID != nil && result.AuthorizedInvocationID.Valid()
	}
	return result.Outcome == WorkRejected && result.ReasonCode != AdmissionAccepted && result.AuthorizedInvocationID == nil
}

type WorkflowAdmissionFacts struct {
	ActorEligible          bool
	BudgetAvailable        bool
	CausationValid         bool
	ChangedConditionProven bool
}

type WorkflowAdmissionEvaluation struct {
	Result       WorkAdmissionResult
	NextInstance WorkflowInstance
}

func EvaluateWorkflowAdmission(definition WorkflowDefinition, current WorkflowInstance, proposal WorkProposal, facts WorkflowAdmissionFacts, recordedEventID, authorizedInvocationID UUIDv7, recordedAt time.Time) (WorkflowAdmissionEvaluation, error) {
	if definition.Validate() != nil || current.Validate(definition) != nil || !proposal.Valid() || !recordedEventID.Valid() || !authorizedInvocationID.Valid() || recordedAt.IsZero() || proposal.WorkflowInstanceID != current.InstanceID || proposal.BudgetAccountID != current.BudgetAccountID || proposal.ProposedAt.After(recordedAt) {
		return WorkflowAdmissionEvaluation{}, ErrInvalidWorkProposal
	}
	next := current.Clone()
	result := WorkAdmissionResult{SchemaVersion: WorkProposalSchemaVersion, ProposalID: proposal.ProposalID, WorkflowInstanceID: current.InstanceID, NodeID: proposal.NodeID, Outcome: WorkRejected, ReasonCode: AdmissionUndeclaredTransition, RecordedEventID: recordedEventID, RecordedAt: recordedAt}
	reason := workflowAdmissionReason(definition, current, proposal, facts)
	result.ReasonCode = reason
	next.ProposedMessageIDs = appendUniqueUUID(next.ProposedMessageIDs, proposal.MessageID)
	if reason == AdmissionAccepted {
		result.Outcome = WorkAdmitted
		invocationID := authorizedInvocationID
		result.AuthorizedInvocationID = &invocationID
		transition := WorkflowTransition{Revision: current.Revision + 1, EventID: recordedEventID, Kind: WorkflowTransitionAdmit, NodeID: proposal.NodeID, ActorFQN: proposal.ActorFQN, InvocationID: authorizedInvocationID, RecordedAt: recordedAt}
		if err := applyWorkflowTransition(definition, &next, transition); err != nil {
			return WorkflowAdmissionEvaluation{}, ErrInvalidWorkProposal
		}
		next.AcceptedMessageIDs = appendUniqueUUID(next.AcceptedMessageIDs, proposal.MessageID)
	}
	next.Revision = current.Revision + 1
	eventID := recordedEventID
	next.LastEventID = &eventID
	if next.Validate(definition) != nil || !result.Valid() {
		return WorkflowAdmissionEvaluation{}, ErrInvalidWorkProposal
	}
	return WorkflowAdmissionEvaluation{Result: result, NextInstance: next}, nil
}

func workflowAdmissionReason(definition WorkflowDefinition, current WorkflowInstance, proposal WorkProposal, facts WorkflowAdmissionFacts) WorkAdmissionReason {
	if current.State.terminal() {
		return AdmissionWorkflowTerminal
	}
	if slices.Contains(current.ProposedMessageIDs, proposal.MessageID) || slices.Contains(current.AcceptedMessageIDs, proposal.MessageID) {
		return AdmissionDuplicateNode
	}
	node := workflowNodeByID(&current, proposal.NodeID)
	if node == nil || node.StageID != proposal.StageID || !workflowStageExists(definition, proposal.StageID) {
		return AdmissionUndeclaredTransition
	}
	if node.State == WorkflowNodePending {
		return AdmissionPredecessorIncomplete
	}
	if node.State != WorkflowNodeReady {
		return AdmissionDuplicateNode
	}
	if !facts.CausationValid {
		return AdmissionCausationInvalid
	}
	if !facts.ActorEligible {
		return AdmissionActorIneligible
	}
	if !facts.BudgetAvailable {
		return AdmissionBudgetExhausted
	}
	stage := workflowStageByID(definition, proposal.StageID)
	if stage != nil && (stage.Purpose == WorkflowPurposeRepair || stage.Purpose == WorkflowPurposeReplan) && workflowHasFailedPredecessor(current, *node) && !facts.ChangedConditionProven {
		return AdmissionNoProgress
	}
	return AdmissionAccepted
}

func workflowHasFailedPredecessor(instance WorkflowInstance, node WorkflowNode) bool {
	for _, predecessorID := range node.PredecessorNodeIDs {
		predecessor := workflowNodeByID(&instance, predecessorID)
		if predecessor != nil && predecessor.State == WorkflowNodeFailed {
			return true
		}
	}
	return false
}

func workflowStageByID(definition WorkflowDefinition, stageID string) *WorkflowStageDefinition {
	for index := range definition.Stages {
		if definition.Stages[index].StageID == stageID {
			return &definition.Stages[index]
		}
	}
	return nil
}

func workflowStageExists(definition WorkflowDefinition, stageID string) bool {
	for _, stage := range definition.Stages {
		if stage.StageID == stageID {
			return true
		}
	}
	return false
}

func appendUniqueUUID(values []UUIDv7, value UUIDv7) []UUIDv7 {
	if slices.Contains(values, value) {
		return values
	}
	return append(values, value)
}
