package mongo

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func (s *Store) LoadOperationalExecutionByAuthorizationEvent(ctx context.Context, eventID kernel.UUIDv7) (application.OperationalExecutionContext, error) {
	if err := requireDeadline(ctx); err != nil {
		return application.OperationalExecutionContext{}, err
	}
	var document eventDocument
	if err := s.db.Collection("events").FindOne(ctx, bson.D{{Key: "_id", Value: string(eventID)}}).Decode(&document); err != nil {
		if errors.Is(err, driver.ErrNoDocuments) {
			return application.OperationalExecutionContext{}, application.ErrInvalidOperationalExecution
		}
		return application.OperationalExecutionContext{}, err
	}
	var event kernel.DomainEvent
	if decode(document.Data, &event) != nil || event.EventID != eventID || event.EventType != "tekroo.event.work-invocation.authorized" || event.Aggregate.Kind != kernel.AggregateWorkInvocation {
		return application.OperationalExecutionContext{}, application.ErrInvalidOperationalExecution
	}
	return s.LoadOperationalExecution(ctx, event.Aggregate.ID)
}

func (s *Store) LoadOperationalExecution(ctx context.Context, invocationID kernel.UUIDv7) (application.OperationalExecutionContext, error) {
	if err := requireDeadline(ctx); err != nil {
		return application.OperationalExecutionContext{}, err
	}
	invocationRef := kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: invocationID}
	initial, err := s.loadSnapshot(ctx, invocationRef, nil)
	if err != nil {
		return application.OperationalExecutionContext{}, err
	}
	invocation, found := initial.WorkInvocations[invocationRef]
	if !found || !invocation.Valid() {
		return application.OperationalExecutionContext{}, application.ErrInvalidOperationalExecution
	}
	taskRef := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: invocation.TaskID}
	budgetRef := kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: invocation.BudgetAccountID}
	snapshot, err := s.loadSnapshot(ctx, invocationRef, []kernel.AggregatePrecondition{
		{Aggregate: taskRef, Expected: kernel.NewExpectedRevision(invocation.TaskRevision)},
		{Aggregate: budgetRef, Expected: kernel.NewExpectedRevision(initial.WorkBudgetAccounts[budgetRef].Revision)},
	})
	if err != nil {
		return application.OperationalExecutionContext{}, err
	}
	invocation, found = snapshot.WorkInvocations[invocationRef]
	taskRelated, taskFound := snapshot.Related[taskRef]
	budget, budgetFound := snapshot.WorkBudgetAccounts[budgetRef]
	taskBudget, taskBudgetFound := snapshot.TaskWorkBudgets[taskRef]
	scope, scopeFound := snapshot.TaskOperationalScopes[taskRef]
	profile, profileFound := snapshot.WorkProfiles[taskRef]
	assignment, assignmentFound := snapshot.QualifiedAssignments[taskRef]
	execution, executionFound := snapshot.CurrentExecutions[invocation.ActorFQN]
	if !found || !taskFound || taskRelated.State == nil || !budgetFound || !taskBudgetFound || !scopeFound || !profileFound || !assignmentFound || !executionFound {
		return application.OperationalExecutionContext{}, application.ErrInvalidOperationalExecution
	}
	var createdDocument eventDocument
	if err := s.db.Collection("events").FindOne(ctx, bson.D{{Key: "aggregate_key", Value: aggregateKey(taskRef)}, {Key: "event_type", Value: "tekroo.event.task.created"}}).Decode(&createdDocument); err != nil {
		return application.OperationalExecutionContext{}, err
	}
	var createdEvent kernel.DomainEvent
	if decode(createdDocument.Data, &createdEvent) != nil {
		return application.OperationalExecutionContext{}, application.ErrInvalidOperationalExecution
	}
	specification, err := application.TaskExecutionSpecificationFromEvent(createdEvent)
	if err != nil {
		return application.OperationalExecutionContext{}, err
	}
	required := make(map[kernel.UUIDv7]struct{})
	for _, evidenceID := range scope.InterfaceEvidenceIDs {
		required[evidenceID] = struct{}{}
	}
	for _, evidenceID := range profile.Profile.ClassificationEvidenceIDs {
		required[evidenceID] = struct{}{}
	}
	for _, evidenceID := range assignment.EvidenceIDs {
		required[evidenceID] = struct{}{}
	}
	evidence := make([]kernel.EvidenceRef, 0, len(required))
	for evidenceID := range required {
		metadata, found := snapshot.Evidence[evidenceID]
		if !found || !metadata.Available || !metadata.SHA256.Valid() {
			return application.OperationalExecutionContext{}, application.ErrInvalidOperationalExecution
		}
		evidence = append(evidence, kernel.EvidenceRef{EvidenceID: evidenceID, SHA256: metadata.SHA256})
	}
	sort.Slice(evidence, func(left, right int) bool { return evidence[left].EvidenceID < evidence[right].EvidenceID })
	recoveryDirective, err := s.loadExecutionRecoveryDirective(ctx, invocation, taskRef, budgetRef, snapshot)
	if err != nil {
		return application.OperationalExecutionContext{}, err
	}
	retryOfConversationID, err := s.loadRetryOfConversationID(ctx, invocation, snapshot)
	if err != nil {
		return application.OperationalExecutionContext{}, err
	}
	_, authorizationSeen := snapshot.AcceptedEvents[invocation.AuthorizationEventID]
	_, parentSeen := snapshot.AcceptedEvents[invocation.ParentEventID]
	return application.OperationalExecutionContext{
		Invocation: invocation.Clone(), Task: taskRelated.State.Clone(), Budget: budget.Clone(),
		TaskBudget: taskBudget.Clone(), Scope: scope.Clone(), Profile: profile.Clone(),
		Assignment: assignment.Clone(), CurrentExecution: execution,
		Specification: specification, Evidence: evidence, RecoveryDirective: recoveryDirective, RetryOfConversationID: retryOfConversationID,
		AuthorizationEventSeen: authorizationSeen, ParentEventSeen: parentSeen,
	}, nil
}

func (s *Store) loadRetryOfConversationID(ctx context.Context, invocation kernel.WorkInvocation, snapshot kernel.Snapshot) (*string, error) {
	if invocation.RetryOfInvocationID == nil {
		return nil, nil
	}
	priorRef := kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: *invocation.RetryOfInvocationID}
	prior, found := snapshot.WorkInvocations[priorRef]
	if !found {
		priorSnapshot, err := s.loadSnapshot(ctx, priorRef, nil)
		if err != nil {
			return nil, err
		}
		prior, found = priorSnapshot.WorkInvocations[priorRef]
	}
	if !found || !prior.Valid() || !prior.State.Terminal() {
		return nil, application.ErrInvalidOperationalExecution
	}
	if prior.ConversationID == nil {
		return nil, nil
	}
	conversationID := *prior.ConversationID
	if conversationID == "" {
		return nil, application.ErrInvalidOperationalExecution
	}
	return &conversationID, nil
}

func (s *Store) loadExecutionRecoveryDirective(ctx context.Context, invocation kernel.WorkInvocation, taskRef, budgetRef kernel.AggregateRef, snapshot kernel.Snapshot) (*application.ExecutionRecoveryDirective, error) {
	if invocation.Purpose != kernel.PurposeRepair && invocation.RetryOfInvocationID == nil {
		return nil, nil
	}
	if invocation.RetryOfInvocationID != nil {
		priorRef := kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: *invocation.RetryOfInvocationID}
		prior, found := snapshot.WorkInvocations[priorRef]
		if !found {
			priorSnapshot, err := s.loadSnapshot(ctx, priorRef, nil)
			if err != nil {
				return nil, err
			}
			prior, found = priorSnapshot.WorkInvocations[priorRef]
		}
		if !found || !prior.Valid() {
			return nil, application.ErrInvalidOperationalExecution
		}
		directive, found, err := s.recoveryDirectiveFromBudgetEvents(ctx, budgetRef, prior.AdmissionPolicyRevision, invocation.AdmissionPolicyRevision, profileEvidenceSet(snapshot.WorkProfiles[taskRef].Profile.ClassificationEvidenceIDs), snapshot)
		if err != nil || found {
			return directive, err
		}
	}
	return s.recoveryDirectiveFromTaskReopen(ctx, taskRef, invocation, snapshot)
}

func (s *Store) recoveryDirectiveFromBudgetEvents(ctx context.Context, budgetRef kernel.AggregateRef, afterPolicyRevision, throughPolicyRevision uint64, profileEvidence map[kernel.UUIDv7]struct{}, snapshot kernel.Snapshot) (*application.ExecutionRecoveryDirective, bool, error) {
	cursor, err := s.db.Collection("events").Find(ctx, bson.D{{Key: "aggregate_key", Value: aggregateKey(budgetRef)}, {Key: "event_type", Value: "tekroo.event.work-budget.amended"}}, options.Find().SetSort(bson.D{{Key: "revision", Value: -1}}))
	if err != nil {
		return nil, false, err
	}
	defer cursor.Close(ctx)
	for cursor.Next(ctx) {
		var document eventDocument
		if cursor.Decode(&document) != nil {
			return nil, false, application.ErrInvalidOperationalExecution
		}
		var event kernel.DomainEvent
		var payload struct {
			PolicyRevision uint64          `json:"policy_revision"`
			Reason         string          `json:"reason"`
			EvidenceIDs    []kernel.UUIDv7 `json:"evidence_ids"`
		}
		if decode(document.Data, &event) != nil || json.Unmarshal(event.Payload, &payload) != nil {
			return nil, false, application.ErrInvalidOperationalExecution
		}
		if event.Authority.Kind != kernel.PrincipalHuman || payload.PolicyRevision <= afterPolicyRevision || payload.PolicyRevision > throughPolicyRevision || !recoveryEvidenceBoundToProfile(payload.EvidenceIDs, profileEvidence) {
			continue
		}
		directive, err := recoveryDirective(event.EventID, payload.Reason, payload.EvidenceIDs, snapshot)
		return directive, err == nil, err
	}
	if err := cursor.Err(); err != nil {
		return nil, false, err
	}
	return nil, false, nil
}

func profileEvidenceSet(ids []kernel.UUIDv7) map[kernel.UUIDv7]struct{} {
	result := make(map[kernel.UUIDv7]struct{}, len(ids))
	for _, id := range ids {
		result[id] = struct{}{}
	}
	return result
}

func recoveryEvidenceBoundToProfile(ids []kernel.UUIDv7, profileEvidence map[kernel.UUIDv7]struct{}) bool {
	if len(ids) == 0 {
		return false
	}
	for _, id := range ids {
		if _, found := profileEvidence[id]; !found {
			return false
		}
	}
	return true
}

func (s *Store) recoveryDirectiveFromTaskReopen(ctx context.Context, taskRef kernel.AggregateRef, invocation kernel.WorkInvocation, snapshot kernel.Snapshot) (*application.ExecutionRecoveryDirective, error) {
	cursor, err := s.db.Collection("events").Find(ctx, bson.D{{Key: "aggregate_key", Value: aggregateKey(taskRef)}, {Key: "event_type", Value: "tekroo.event.work.reopened"}}, options.Find().SetSort(bson.D{{Key: "revision", Value: -1}}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	for cursor.Next(ctx) {
		var document eventDocument
		if cursor.Decode(&document) != nil {
			return nil, application.ErrInvalidOperationalExecution
		}
		var event kernel.DomainEvent
		var payload struct {
			NewScopeRevision uint64          `json:"new_scope_revision"`
			Reason           string          `json:"reason"`
			EvidenceIDs      []kernel.UUIDv7 `json:"evidence_ids"`
		}
		if decode(document.Data, &event) != nil || json.Unmarshal(event.Payload, &payload) != nil {
			return nil, application.ErrInvalidOperationalExecution
		}
		if event.LifecycleEpoch != invocation.LifecycleEpoch || payload.NewScopeRevision != invocation.ScopeRevision || event.AggregateRevision > invocation.TaskRevision {
			continue
		}
		return recoveryDirective(event.EventID, payload.Reason, payload.EvidenceIDs, snapshot)
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}
	return nil, nil
}

func recoveryDirective(eventID kernel.UUIDv7, reason string, evidenceIDs []kernel.UUIDv7, snapshot kernel.Snapshot) (*application.ExecutionRecoveryDirective, error) {
	references := make([]kernel.EvidenceRef, 0, len(evidenceIDs))
	for _, evidenceID := range evidenceIDs {
		metadata, found := snapshot.Evidence[evidenceID]
		if !found || !metadata.Available || !metadata.SHA256.Valid() {
			return nil, application.ErrInvalidOperationalExecution
		}
		references = append(references, kernel.EvidenceRef{EvidenceID: evidenceID, SHA256: metadata.SHA256})
	}
	sort.Slice(references, func(left, right int) bool { return references[left].EvidenceID < references[right].EvidenceID })
	directive := &application.ExecutionRecoveryDirective{SourceEventID: eventID, Reason: reason, Evidence: references}
	if !directive.Valid() {
		return nil, application.ErrInvalidOperationalExecution
	}
	return directive, nil
}

var _ application.OperationalExecutionReader = (*Store)(nil)
