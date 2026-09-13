package mongo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

type workflowInstanceDocument struct {
	ID               string                       `bson:"_id"`
	Revision         uint64                       `bson:"revision"`
	DefinitionDigest string                       `bson:"definition_digest"`
	RootKey          string                       `bson:"root_key"`
	BudgetAccountID  string                       `bson:"budget_account_id"`
	State            kernel.WorkflowInstanceState `bson:"state"`
	Data             []byte                       `bson:"data"`
}

type workflowAdmissionDocument struct {
	ID                 string                      `bson:"_id"`
	WorkflowInstanceID string                      `bson:"workflow_instance_id"`
	NodeID             string                      `bson:"node_id"`
	ActorFQN           string                      `bson:"actor_fqn"`
	ExecutionID        string                      `bson:"execution_id"`
	FencingEpoch       uint64                      `bson:"fencing_epoch"`
	Outcome            kernel.WorkAdmissionOutcome `bson:"outcome"`
	RecordedAt         int64                       `bson:"recorded_at_unix_nano"`
	ProposalData       []byte                      `bson:"proposal_data"`
	ResultData         []byte                      `bson:"result_data"`
}

type workflowEventDocument struct {
	ID                 string `bson:"_id"`
	WorkflowInstanceID string `bson:"workflow_instance_id"`
	Revision           uint64 `bson:"revision"`
	Kind               string `bson:"kind"`
	Data               []byte `bson:"data"`
}

// BindMessageToWorkflow links an already-durable compatibility message to the
// corresponding workflow node. The message, node identity, and causal event
// are committed together so a change-stream observer cannot see a partial
// binding.
func (s *Store) BindMessageToWorkflow(ctx context.Context, messageID kernel.UUIDv7, definition kernel.WorkflowDefinition, initial kernel.WorkflowInstance, stageID string) (organization.OrganizationalMessage, error) {
	if err := requireDeadline(ctx); err != nil {
		return organization.OrganizationalMessage{}, err
	}
	if s == nil || s.client == nil || !messageID.Valid() || definition.Validate() != nil || initial.Validate(definition) != nil {
		return organization.OrganizationalMessage{}, kernel.ErrInvalidWorkflowInstance
	}
	session, err := s.client.StartSession()
	if err != nil {
		return organization.OrganizationalMessage{}, err
	}
	defer session.EndSession(ctx)
	var bound organization.OrganizationalMessage
	_, err = session.WithTransaction(ctx, func(transactionContext context.Context) (any, error) {
		var messageDocument organizationalMessageDocument
		if findErr := s.db.Collection("organizational_messages").FindOne(transactionContext, bson.D{{Key: "_id", Value: string(messageID)}}).Decode(&messageDocument); findErr != nil {
			return nil, findErr
		}
		claim, decodeErr := decodeOrganizationalClaim(messageDocument)
		if decodeErr != nil || claim.State != organization.MessagePending {
			return nil, organization.ErrOrganizationalMessageConflict
		}
		message := claim.Message
		if message.Work.WorkflowInstanceID != nil {
			if *message.Work.WorkflowInstanceID != initial.InstanceID || message.Work.WorkflowStageID != stageID {
				return nil, organization.ErrOrganizationalMessageConflict
			}
			bound = message
			return nil, nil
		}

		var current kernel.WorkflowInstance
		var document workflowInstanceDocument
		workflowErr := s.db.Collection("workflow_instances").FindOne(transactionContext, bson.D{{Key: "_id", Value: string(initial.InstanceID)}}).Decode(&document)
		switch {
		case errors.Is(workflowErr, driver.ErrNoDocuments):
			current = initial.Clone()
			if current.LastEventID == nil || *current.LastEventID != messageID {
				return nil, kernel.ErrInvalidWorkflowInstance
			}
			data, encodeErr := encode(current)
			if encodeErr != nil {
				return nil, encodeErr
			}
			document = workflowInstanceDocument{ID: string(current.InstanceID), Revision: current.Revision, DefinitionDigest: string(current.DefinitionDigest), RootKey: aggregateKey(current.RootRequest), BudgetAccountID: string(current.BudgetAccountID), State: current.State, Data: data}
			if _, insertErr := s.db.Collection("workflow_instances").InsertOne(transactionContext, document); insertErr != nil {
				return nil, insertErr
			}
		case workflowErr != nil:
			return nil, workflowErr
		default:
			if decode(document.Data, &current) != nil || current.Validate(definition) != nil || current.DefinitionDigest != definition.ContentDigest {
				return nil, ErrCorruptAggregate
			}
			placeholder := workflowNodeForStage(current, stageID)
			if placeholder == nil {
				return nil, kernel.ErrInvalidWorkflowTransition
			}
			if placeholder.NodeID != message.Work.DAGNodeID {
				next, expandErr := kernel.ExpandWorkflowNode(definition, current, placeholder.NodeID, messageID, []kernel.WorkflowNodeSpec{{NodeID: message.Work.DAGNodeID, PredecessorNodeIDs: placeholder.PredecessorNodeIDs, InputEvidenceIDs: placeholder.InputEvidenceIDs}})
				if expandErr != nil {
					return nil, expandErr
				}
				data, encodeErr := encode(next)
				if encodeErr != nil {
					return nil, encodeErr
				}
				result, replaceErr := s.db.Collection("workflow_instances").ReplaceOne(transactionContext, bson.D{{Key: "_id", Value: document.ID}, {Key: "revision", Value: document.Revision}}, workflowInstanceDocument{ID: document.ID, Revision: next.Revision, DefinitionDigest: document.DefinitionDigest, RootKey: document.RootKey, BudgetAccountID: document.BudgetAccountID, State: next.State, Data: data})
				if replaceErr != nil || result.MatchedCount != 1 {
					return nil, errors.Join(organization.ErrOrganizationalMessageConflict, replaceErr)
				}
				current = next
			}
		}

		message.Work.WorkflowInstanceID = &current.InstanceID
		message.Work.WorkflowStageID = stageID
		if message.Validate() != nil {
			return nil, organization.ErrInvalidOrganizationalMessage
		}
		messageData, encodeErr := encode(message)
		if encodeErr != nil {
			return nil, encodeErr
		}
		messageResult, updateErr := s.db.Collection("organizational_messages").UpdateOne(transactionContext, bson.D{{Key: "_id", Value: messageDocument.ID}, {Key: "state", Value: organization.MessagePending}, {Key: "data", Value: messageDocument.Data}}, bson.D{{Key: "$set", Value: bson.D{{Key: "data", Value: messageData}}}})
		if updateErr != nil || messageResult.MatchedCount != 1 {
			return nil, errors.Join(organization.ErrOrganizationalMessageConflict, updateErr)
		}
		eventData, encodeErr := encode(message.Work)
		if encodeErr != nil {
			return nil, encodeErr
		}
		if _, insertErr := s.db.Collection("workflow_events").InsertOne(transactionContext, workflowEventDocument{ID: string(messageID), WorkflowInstanceID: string(current.InstanceID), Revision: current.Revision, Kind: "MESSAGE_BOUND", Data: eventData}); insertErr != nil {
			return nil, insertErr
		}
		bound = message
		return nil, nil
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()).SetReadPreference(readpref.Primary()))
	if driver.IsDuplicateKeyError(err) {
		err = organization.ErrOrganizationalMessageConflict
	}
	return bound, err
}

func workflowNodeForStage(instance kernel.WorkflowInstance, stageID string) *kernel.WorkflowNode {
	for index := range instance.Nodes {
		if instance.Nodes[index].StageID == stageID && (instance.Nodes[index].State == kernel.WorkflowNodePending || instance.Nodes[index].State == kernel.WorkflowNodeReady) {
			return &instance.Nodes[index]
		}
	}
	return nil
}

func (s *Store) CreateWorkflowInstance(ctx context.Context, definition kernel.WorkflowDefinition, instance kernel.WorkflowInstance) error {
	if err := requireDeadline(ctx); err != nil {
		return err
	}
	if s == nil || s.db == nil || instance.Validate(definition) != nil {
		return kernel.ErrInvalidWorkflowInstance
	}
	data, err := encode(instance)
	if err != nil {
		return err
	}
	document := workflowInstanceDocument{ID: string(instance.InstanceID), Revision: instance.Revision, DefinitionDigest: string(instance.DefinitionDigest), RootKey: aggregateKey(instance.RootRequest), BudgetAccountID: string(instance.BudgetAccountID), State: instance.State, Data: data}
	_, err = s.db.Collection("workflow_instances").InsertOne(ctx, document)
	if !driver.IsDuplicateKeyError(err) {
		return err
	}
	var current workflowInstanceDocument
	if findErr := s.db.Collection("workflow_instances").FindOne(ctx, bson.D{{Key: "_id", Value: document.ID}}).Decode(&current); findErr != nil {
		if errors.Is(findErr, driver.ErrNoDocuments) {
			return organization.ErrOrganizationalMessageConflict
		}
		return findErr
	}
	if current.Revision == document.Revision && current.DefinitionDigest == document.DefinitionDigest && current.RootKey == document.RootKey && current.BudgetAccountID == document.BudgetAccountID && bytes.Equal(current.Data, document.Data) {
		return nil
	}
	return organization.ErrOrganizationalMessageConflict
}

func (s *Store) ReadWorkflowInstanceByBudget(ctx context.Context, budgetID kernel.UUIDv7) (kernel.WorkflowInstance, bool, error) {
	if err := requireDeadline(ctx); err != nil {
		return kernel.WorkflowInstance{}, false, err
	}
	if s == nil || s.db == nil || !budgetID.Valid() {
		return kernel.WorkflowInstance{}, false, kernel.ErrInvalidWorkflowInstance
	}
	var document workflowInstanceDocument
	err := s.db.Collection("workflow_instances").FindOne(ctx, bson.D{{Key: "budget_account_id", Value: string(budgetID)}}).Decode(&document)
	if errors.Is(err, driver.ErrNoDocuments) {
		return kernel.WorkflowInstance{}, false, nil
	}
	if err != nil {
		return kernel.WorkflowInstance{}, false, err
	}
	var instance kernel.WorkflowInstance
	if decode(document.Data, &instance) != nil || instance.InstanceID != kernel.UUIDv7(document.ID) || instance.BudgetAccountID != budgetID {
		return kernel.WorkflowInstance{}, false, ErrCorruptAggregate
	}
	return instance, true, nil
}

func (s *Store) ReadWorkflowInstance(ctx context.Context, id kernel.UUIDv7) (kernel.WorkflowInstance, bool, error) {
	if err := requireDeadline(ctx); err != nil {
		return kernel.WorkflowInstance{}, false, err
	}
	if s == nil || s.db == nil || !id.Valid() {
		return kernel.WorkflowInstance{}, false, kernel.ErrInvalidWorkflowInstance
	}
	var document workflowInstanceDocument
	err := s.db.Collection("workflow_instances").FindOne(ctx, bson.D{{Key: "_id", Value: string(id)}}).Decode(&document)
	if errors.Is(err, driver.ErrNoDocuments) {
		return kernel.WorkflowInstance{}, false, nil
	}
	if err != nil {
		return kernel.WorkflowInstance{}, false, err
	}
	var instance kernel.WorkflowInstance
	if decode(document.Data, &instance) != nil || instance.InstanceID != id || instance.Revision != document.Revision || string(instance.DefinitionDigest) != document.DefinitionDigest || aggregateKey(instance.RootRequest) != document.RootKey || string(instance.BudgetAccountID) != document.BudgetAccountID || instance.State != document.State {
		return kernel.WorkflowInstance{}, false, ErrCorruptAggregate
	}
	return instance, true, nil
}

func (s *Store) AdmitWorkflowMessage(ctx context.Context, request organization.WorkflowAdmissionRequest) (kernel.WorkAdmissionResult, bool, error) {
	if err := requireDeadline(ctx); err != nil {
		return kernel.WorkAdmissionResult{}, false, err
	}
	if s == nil || s.client == nil || !request.Valid() {
		return kernel.WorkAdmissionResult{}, false, kernel.ErrInvalidWorkProposal
	}
	session, err := s.client.StartSession()
	if err != nil {
		return kernel.WorkAdmissionResult{}, false, err
	}
	defer session.EndSession(ctx)
	var result kernel.WorkAdmissionResult
	var replayed bool
	_, err = session.WithTransaction(ctx, func(transactionContext context.Context) (any, error) {
		prior, found, priorErr := s.readWorkflowAdmission(transactionContext, request.Message.ID)
		if priorErr != nil {
			return nil, priorErr
		}
		if found {
			if !equalWorkProposal(prior.proposal, request.Proposal) {
				return nil, organization.ErrOrganizationalMessageConflict
			}
			result, replayed = prior.result, true
			return nil, nil
		}

		var messageDocument organizationalMessageDocument
		if findErr := s.db.Collection("organizational_messages").FindOne(transactionContext, bson.D{{Key: "_id", Value: string(request.Message.ID)}}).Decode(&messageDocument); findErr != nil {
			if errors.Is(findErr, driver.ErrNoDocuments) {
				return nil, organization.ErrOrganizationalMessageNotFound
			}
			return nil, findErr
		}
		messageData, marshalErr := encode(request.Message)
		if marshalErr != nil {
			return nil, marshalErr
		}
		if messageDocument.State != organization.MessagePending || !bytes.Equal(messageDocument.Data, messageData) {
			return nil, organization.ErrOrganizationalMessageConflict
		}

		role, roleFound, roleErr := s.LoadRole(transactionContext, request.Proposal.ActorFQN)
		if roleErr != nil {
			return nil, roleErr
		}
		if !roleFound || role.Execution != request.Proposal.Execution || role.Status != organization.RoleIdle {
			return nil, organization.ErrStaleOrganizationalClaim
		}

		var workflowDocument workflowInstanceDocument
		if findErr := s.db.Collection("workflow_instances").FindOne(transactionContext, bson.D{{Key: "_id", Value: string(request.Proposal.WorkflowInstanceID)}}).Decode(&workflowDocument); findErr != nil {
			return nil, findErr
		}
		var current kernel.WorkflowInstance
		if decode(workflowDocument.Data, &current) != nil || current.Revision != workflowDocument.Revision || current.DefinitionDigest != request.Definition.ContentDigest || current.Validate(request.Definition) != nil {
			return nil, ErrCorruptAggregate
		}

		facts := request.Facts
		facts.CausationValid = facts.CausationValid && s.workflowCausationExists(transactionContext, request.Proposal.CausationEventIDs)
		facts.BudgetAvailable = facts.BudgetAvailable && s.workflowBudgetAvailable(transactionContext, request)
		facts.ChangedConditionProven = facts.ChangedConditionProven && s.workflowEvidenceExists(transactionContext, request.Proposal.InputEvidenceIDs)
		evaluation, evaluateErr := kernel.EvaluateWorkflowAdmission(request.Definition, current, request.Proposal, facts, request.RecordedEventID, request.AuthorizedInvocationID, request.RecordedAt)
		if evaluateErr != nil {
			return nil, evaluateErr
		}
		nextData, marshalErr := encode(evaluation.NextInstance)
		if marshalErr != nil {
			return nil, marshalErr
		}
		workflowResult, replaceErr := s.db.Collection("workflow_instances").ReplaceOne(transactionContext, bson.D{{Key: "_id", Value: workflowDocument.ID}, {Key: "revision", Value: workflowDocument.Revision}}, workflowInstanceDocument{ID: workflowDocument.ID, Revision: evaluation.NextInstance.Revision, DefinitionDigest: string(evaluation.NextInstance.DefinitionDigest), RootKey: workflowDocument.RootKey, BudgetAccountID: workflowDocument.BudgetAccountID, State: evaluation.NextInstance.State, Data: nextData})
		if replaceErr != nil || workflowResult.MatchedCount != 1 {
			return nil, errors.Join(organization.ErrOrganizationalMessageConflict, replaceErr)
		}
		resultData, marshalErr := encode(evaluation.Result)
		if marshalErr != nil {
			return nil, marshalErr
		}
		proposalData, marshalErr := encode(request.Proposal)
		if marshalErr != nil {
			return nil, marshalErr
		}
		messageResult, updateErr := s.db.Collection("organizational_messages").UpdateOne(transactionContext, bson.D{{Key: "_id", Value: messageDocument.ID}, {Key: "state", Value: organization.MessagePending}}, bson.D{{Key: "$set", Value: bson.D{{Key: "state", Value: organization.MessageResolved}, {Key: "resolution", Value: string(evaluation.Result.ReasonCode)}, {Key: "evidence_digest", Value: string(workAdmissionDigest(resultData))}}}})
		if updateErr != nil || messageResult.MatchedCount != 1 {
			return nil, errors.Join(organization.ErrOrganizationalMessageConflict, updateErr)
		}
		admission := workflowAdmissionDocument{ID: string(request.Message.ID), WorkflowInstanceID: string(request.Proposal.WorkflowInstanceID), NodeID: string(request.Proposal.NodeID), ActorFQN: string(request.Proposal.ActorFQN), ExecutionID: string(request.Proposal.Execution.ExecutionID), FencingEpoch: request.Proposal.Execution.FencingEpoch, Outcome: evaluation.Result.Outcome, RecordedAt: request.RecordedAt.UnixNano(), ProposalData: proposalData, ResultData: resultData}
		if _, insertErr := s.db.Collection("workflow_admissions").InsertOne(transactionContext, admission); insertErr != nil {
			return nil, insertErr
		}
		kind := "WORK_REJECTED"
		if evaluation.Result.Outcome == kernel.WorkAdmitted {
			kind = "WORK_ADMITTED"
		}
		if _, insertErr := s.db.Collection("workflow_events").InsertOne(transactionContext, workflowEventDocument{ID: string(request.RecordedEventID), WorkflowInstanceID: string(request.Proposal.WorkflowInstanceID), Revision: evaluation.NextInstance.Revision, Kind: kind, Data: resultData}); insertErr != nil {
			return nil, insertErr
		}
		result = evaluation.Result
		return nil, nil
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()).SetReadPreference(readpref.Primary()))
	if driver.IsDuplicateKeyError(err) {
		err = organization.ErrOrganizationalMessageConflict
	}
	return result, replayed, err
}

func (s *Store) ApplyWorkflowTransition(ctx context.Context, definition kernel.WorkflowDefinition, instanceID kernel.UUIDv7, transition kernel.WorkflowTransition) (kernel.WorkflowInstance, error) {
	if err := requireDeadline(ctx); err != nil {
		return kernel.WorkflowInstance{}, err
	}
	if s == nil || s.client == nil || !instanceID.Valid() {
		return kernel.WorkflowInstance{}, kernel.ErrInvalidWorkflowTransition
	}
	return s.updateWorkflowInstance(ctx, definition, instanceID, transition.EventID, string(transition.Kind), func(current kernel.WorkflowInstance) (kernel.WorkflowInstance, error) {
		return kernel.FoldWorkflowInstance(definition, current, []kernel.WorkflowTransition{transition})
	}, transition)
}

func (s *Store) ExpandWorkflowNode(ctx context.Context, definition kernel.WorkflowDefinition, instanceID, placeholderID, eventID kernel.UUIDv7, specs []kernel.WorkflowNodeSpec) (kernel.WorkflowInstance, error) {
	if err := requireDeadline(ctx); err != nil {
		return kernel.WorkflowInstance{}, err
	}
	if s == nil || s.client == nil || !instanceID.Valid() || !placeholderID.Valid() || !eventID.Valid() {
		return kernel.WorkflowInstance{}, kernel.ErrInvalidWorkflowTransition
	}
	return s.updateWorkflowInstance(ctx, definition, instanceID, eventID, "EXPAND", func(current kernel.WorkflowInstance) (kernel.WorkflowInstance, error) {
		return kernel.ExpandWorkflowNode(definition, current, placeholderID, eventID, specs)
	}, specs)
}

func (s *Store) updateWorkflowInstance(ctx context.Context, definition kernel.WorkflowDefinition, instanceID, eventID kernel.UUIDv7, kind string, update func(kernel.WorkflowInstance) (kernel.WorkflowInstance, error), eventValue any) (kernel.WorkflowInstance, error) {
	eventData, err := encode(eventValue)
	if err != nil {
		return kernel.WorkflowInstance{}, err
	}
	session, err := s.client.StartSession()
	if err != nil {
		return kernel.WorkflowInstance{}, err
	}
	defer session.EndSession(ctx)
	var next kernel.WorkflowInstance
	_, err = session.WithTransaction(ctx, func(transactionContext context.Context) (any, error) {
		var document workflowInstanceDocument
		if findErr := s.db.Collection("workflow_instances").FindOne(transactionContext, bson.D{{Key: "_id", Value: string(instanceID)}}).Decode(&document); findErr != nil {
			return nil, findErr
		}
		var current kernel.WorkflowInstance
		if decode(document.Data, &current) != nil || current.Revision != document.Revision || current.Validate(definition) != nil {
			return nil, ErrCorruptAggregate
		}
		var existing workflowEventDocument
		existingErr := s.db.Collection("workflow_events").FindOne(transactionContext, bson.D{{Key: "_id", Value: string(eventID)}}).Decode(&existing)
		if existingErr == nil {
			if existing.WorkflowInstanceID != string(instanceID) || existing.Kind != kind || !bytes.Equal(existing.Data, eventData) {
				return nil, organization.ErrOrganizationalMessageConflict
			}
			next = current
			return nil, nil
		}
		if !errors.Is(existingErr, driver.ErrNoDocuments) {
			return nil, existingErr
		}
		var updateErr error
		next, updateErr = update(current)
		if updateErr != nil {
			return nil, updateErr
		}
		nextData, encodeErr := encode(next)
		if encodeErr != nil {
			return nil, encodeErr
		}
		result, replaceErr := s.db.Collection("workflow_instances").ReplaceOne(transactionContext, bson.D{{Key: "_id", Value: document.ID}, {Key: "revision", Value: document.Revision}}, workflowInstanceDocument{ID: document.ID, Revision: next.Revision, DefinitionDigest: document.DefinitionDigest, RootKey: document.RootKey, BudgetAccountID: document.BudgetAccountID, State: next.State, Data: nextData})
		if replaceErr != nil || result.MatchedCount != 1 {
			return nil, errors.Join(organization.ErrOrganizationalMessageConflict, replaceErr)
		}
		if _, insertErr := s.db.Collection("workflow_events").InsertOne(transactionContext, workflowEventDocument{ID: string(eventID), WorkflowInstanceID: string(instanceID), Revision: next.Revision, Kind: kind, Data: eventData}); insertErr != nil {
			return nil, insertErr
		}
		return nil, nil
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()).SetReadPreference(readpref.Primary()))
	if driver.IsDuplicateKeyError(err) {
		return kernel.WorkflowInstance{}, organization.ErrOrganizationalMessageConflict
	}
	return next, err
}

func (s *Store) WorkflowAdmission(ctx context.Context, messageID kernel.UUIDv7) (kernel.WorkAdmissionResult, bool, error) {
	if err := requireDeadline(ctx); err != nil {
		return kernel.WorkAdmissionResult{}, false, err
	}
	if s == nil || s.db == nil || !messageID.Valid() {
		return kernel.WorkAdmissionResult{}, false, kernel.ErrInvalidWorkProposal
	}
	admission, found, err := s.readWorkflowAdmission(ctx, messageID)
	return admission.result, found, err
}

type storedWorkflowAdmission struct {
	proposal kernel.WorkProposal
	result   kernel.WorkAdmissionResult
}

func (s *Store) readWorkflowAdmission(ctx context.Context, messageID kernel.UUIDv7) (storedWorkflowAdmission, bool, error) {
	var document workflowAdmissionDocument
	err := s.db.Collection("workflow_admissions").FindOne(ctx, bson.D{{Key: "_id", Value: string(messageID)}}).Decode(&document)
	if errors.Is(err, driver.ErrNoDocuments) {
		return storedWorkflowAdmission{}, false, nil
	}
	if err != nil {
		return storedWorkflowAdmission{}, false, err
	}
	var proposal kernel.WorkProposal
	var result kernel.WorkAdmissionResult
	if decode(document.ProposalData, &proposal) != nil || decode(document.ResultData, &result) != nil || proposal.MessageID != messageID || result.ProposalID != proposal.ProposalID || !proposal.Valid() || !result.Valid() {
		return storedWorkflowAdmission{}, false, ErrCorruptAggregate
	}
	return storedWorkflowAdmission{proposal: proposal, result: result}, true, nil
}

func equalWorkProposal(left, right kernel.WorkProposal) bool {
	leftData, leftErr := encode(left)
	rightData, rightErr := encode(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftData, rightData)
}

func (s *Store) workflowCausationExists(ctx context.Context, ids []kernel.UUIDv7) bool {
	for _, id := range ids {
		if s.db.Collection("events").FindOne(ctx, bson.D{{Key: "_id", Value: string(id)}}).Err() != nil {
			if s.db.Collection("workflow_events").FindOne(ctx, bson.D{{Key: "_id", Value: string(id)}}).Err() != nil {
				return false
			}
		}
	}
	return true
}

func (s *Store) workflowEvidenceExists(ctx context.Context, ids []kernel.UUIDv7) bool {
	if len(ids) == 0 {
		return false
	}
	for _, id := range ids {
		var evidence evidenceDocument
		if s.db.Collection("evidence").FindOne(ctx, bson.D{{Key: "_id", Value: string(id)}, {Key: "available", Value: true}}).Decode(&evidence) != nil {
			return false
		}
	}
	return true
}

func (s *Store) workflowBudgetAvailable(ctx context.Context, request organization.WorkflowAdmissionRequest) bool {
	var document valueDocument
	if s.db.Collection("work_budget_accounts").FindOne(ctx, bson.D{{Key: "_id", Value: aggregateKey(kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: request.Proposal.BudgetAccountID})}}).Decode(&document) != nil {
		return false
	}
	var value workBudgetValue
	if decode(document.Data, &value) != nil || !value.Account.Valid() || value.Account.ID != request.Proposal.BudgetAccountID || !value.Account.DeadlineAt.After(request.RecordedAt) || value.Account.ModelInvocationsUsed >= value.Account.ModelInvocationLimit {
		return false
	}
	purpose, found := workflowWorkPurpose(request.Definition, request.Proposal.StageID)
	return found && value.Account.PurposeUsed[purpose] < value.Account.PurposeLimits[purpose]
}

func workflowWorkPurpose(definition kernel.WorkflowDefinition, stageID string) (kernel.WorkPurpose, bool) {
	for _, stage := range definition.Stages {
		if stage.StageID != stageID {
			continue
		}
		purpose := kernel.WorkPurpose(stage.Purpose)
		return purpose, purpose.Valid()
	}
	return "", false
}

func workAdmissionDigest(data []byte) kernel.Digest {
	digest := sha256.Sum256(data)
	return kernel.Digest(hex.EncodeToString(digest[:]))
}
