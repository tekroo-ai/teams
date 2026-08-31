package memory

import (
	"context"
	"sort"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

func (s *Store) LoadOperationalExecutionByAuthorizationEvent(ctx context.Context, eventID kernel.UUIDv7) (application.OperationalExecutionContext, error) {
	if err := ctx.Err(); err != nil {
		return application.OperationalExecutionContext{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	event, found := s.events[eventID]
	if !found || event.EventType != "tekroo.event.work-invocation.authorized" || event.Aggregate.Kind != kernel.AggregateWorkInvocation {
		return application.OperationalExecutionContext{}, application.ErrInvalidOperationalExecution
	}
	return s.loadOperationalExecutionLocked(event.Aggregate.ID)
}

func (s *Store) LoadOperationalExecution(ctx context.Context, invocationID kernel.UUIDv7) (application.OperationalExecutionContext, error) {
	if err := ctx.Err(); err != nil {
		return application.OperationalExecutionContext{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loadOperationalExecutionLocked(invocationID)
}

func (s *Store) loadOperationalExecutionLocked(invocationID kernel.UUIDv7) (application.OperationalExecutionContext, error) {
	invocationRef := kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: invocationID}
	invocation, found := s.workInvocations[invocationRef]
	if !found || !invocation.Valid() {
		return application.OperationalExecutionContext{}, application.ErrInvalidOperationalExecution
	}
	taskRef := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: invocation.TaskID}
	budgetRef := kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: invocation.BudgetAccountID}
	task, taskFound := s.states[taskRef]
	budget, budgetFound := s.workBudgetAccounts[budgetRef]
	taskBudget, taskBudgetFound := s.taskWorkBudgets[taskRef]
	scope, scopeFound := s.taskOperationalScopes[taskRef]
	profile, profileFound := s.workProfiles[taskRef]
	assignment, assignmentFound := s.qualifiedAssignments[taskRef]
	execution, executionFound := s.executions[invocation.ActorFQN]
	if !taskFound || !budgetFound || !taskBudgetFound || !scopeFound || !profileFound || !assignmentFound || !executionFound {
		return application.OperationalExecutionContext{}, application.ErrInvalidOperationalExecution
	}
	var specification application.TaskExecutionSpecification
	specificationFound := false
	for _, event := range s.events {
		if event.Aggregate == taskRef && event.EventType == "tekroo.event.task.created" {
			parsed, err := application.TaskExecutionSpecificationFromEvent(event)
			if err != nil {
				return application.OperationalExecutionContext{}, err
			}
			specification, specificationFound = parsed, true
			break
		}
	}
	if !specificationFound {
		return application.OperationalExecutionContext{}, application.ErrInvalidOperationalExecution
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
		metadata, found := s.evidence[evidenceID]
		if !found || !metadata.Available || !metadata.SHA256.Valid() {
			return application.OperationalExecutionContext{}, application.ErrInvalidOperationalExecution
		}
		evidence = append(evidence, kernel.EvidenceRef{EvidenceID: evidenceID, SHA256: metadata.SHA256})
	}
	sort.Slice(evidence, func(left, right int) bool { return evidence[left].EvidenceID < evidence[right].EvidenceID })
	_, authorizationSeen := s.events[invocation.AuthorizationEventID]
	_, parentSeen := s.events[invocation.ParentEventID]
	return application.OperationalExecutionContext{
		Invocation: invocation.Clone(), Task: task.Clone(), Budget: budget.Clone(),
		TaskBudget: taskBudget.Clone(), Scope: scope.Clone(), Profile: profile.Clone(),
		Assignment: assignment.Clone(), CurrentExecution: execution,
		Specification: specification, Evidence: evidence,
		AuthorizationEventSeen: authorizationSeen, ParentEventSeen: parentSeen,
	}, nil
}

var _ application.OperationalExecutionReader = (*Store)(nil)
