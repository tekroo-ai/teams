package mongo

import (
	"context"
	"errors"
	"sort"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
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
	_, authorizationSeen := snapshot.AcceptedEvents[invocation.AuthorizationEventID]
	_, parentSeen := snapshot.AcceptedEvents[invocation.ParentEventID]
	return application.OperationalExecutionContext{
		Invocation: invocation.Clone(), Task: taskRelated.State.Clone(), Budget: budget.Clone(),
		TaskBudget: taskBudget.Clone(), Scope: scope.Clone(), Profile: profile.Clone(),
		Assignment: assignment.Clone(), CurrentExecution: execution,
		Specification: specification, Evidence: evidence,
		AuthorizationEventSeen: authorizationSeen, ParentEventSeen: parentSeen,
	}, nil
}

var _ application.OperationalExecutionReader = (*Store)(nil)
