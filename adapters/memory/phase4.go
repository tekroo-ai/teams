package memory

import "github.com/tekroo-ai/teams/kernel"

type phase4Projection struct {
	budgetRef     *kernel.AggregateRef
	budget        *kernel.WorkBudgetAccount
	taskRef       *kernel.AggregateRef
	binding       *kernel.TaskWorkBudgetBinding
	scope         *kernel.TaskOperationalScope
	invocationRef *kernel.AggregateRef
	invocation    *kernel.WorkInvocation
}

func (p phase4Projection) apply(store *Store) {
	if p.budgetRef != nil && p.budget != nil {
		store.workBudgetAccounts[*p.budgetRef] = p.budget.Clone()
	}
	if p.taskRef != nil && p.binding != nil {
		store.taskWorkBudgets[*p.taskRef] = p.binding.Clone()
	}
	if p.taskRef != nil && p.scope != nil {
		store.taskOperationalScopes[*p.taskRef] = p.scope.Clone()
	}
	if p.invocationRef != nil && p.invocation != nil {
		store.workInvocations[*p.invocationRef] = p.invocation.Clone()
	}
}

func validateWorkBudgetCommit(accounts map[kernel.AggregateRef]kernel.WorkBudgetAccount, bindings map[kernel.AggregateRef]kernel.TaskWorkBudgetBinding, decision kernel.WorkBudgetDebitDecision) error {
	if !decision.Valid() {
		return ErrInvalidDecision
	}
	account, found := accounts[decision.Account]
	binding, bindingFound := bindings[decision.Task]
	if !found || !bindingFound || account.Revision != decision.ExpectedAccountRevision || account.ModelInvocationsUsed != decision.ExpectedGlobalUsed || account.PurposeUsed[decision.Purpose] != decision.ExpectedGlobalPurposeUsed || binding.ModelInvocationsUsed != decision.ExpectedTaskUsed || binding.PurposeUsed[decision.Purpose] != decision.ExpectedTaskPurposeUsed {
		return ErrConflict
	}
	return nil
}

func (s *Store) preparePhase4Projections(events []kernel.DomainEvent, debit *kernel.WorkBudgetDebitDecision) ([]phase4Projection, error) {
	projections := make([]phase4Projection, 0, len(events))
	for _, event := range events {
		switch event.EventType {
		case "tekroo.event.work-budget.created":
			account, err := kernel.WorkBudgetFromCreatePayload(event.Payload, event.EventID)
			if err != nil || event.Aggregate != account.Ref() || event.AggregateRevision != 1 {
				return nil, ErrInvalidDecision
			}
			if _, exists := s.workBudgetAccounts[event.Aggregate]; exists {
				return nil, ErrConflict
			}
			ref := event.Aggregate
			projections = append(projections, phase4Projection{budgetRef: &ref, budget: &account})
		case "tekroo.event.work-budget.amended":
			current, found := s.workBudgetAccounts[event.Aggregate]
			if !found || event.AggregateRevision != current.Revision+1 {
				return nil, ErrInvalidDecision
			}
			next, valid := kernel.ApplyWorkBudgetAmendment(current, event.Payload, event.EventID)
			if !valid || next.Revision != event.AggregateRevision {
				return nil, ErrInvalidDecision
			}
			ref := event.Aggregate
			projections = append(projections, phase4Projection{budgetRef: &ref, budget: &next})
		case "tekroo.event.task.work-budget-bound":
			binding, err := kernel.TaskWorkBudgetBindingFromPayload(event.Payload, event.EventID)
			if err != nil || event.Aggregate.Kind != kernel.AggregateTask || event.Aggregate.ID != binding.TaskID || event.AggregateRevision != binding.TaskRevision {
				return nil, ErrInvalidDecision
			}
			if current, exists := s.taskWorkBudgets[event.Aggregate]; exists {
				if current.BudgetAccountID != binding.BudgetAccountID || current.ModelInvocationsUsed > binding.ModelInvocationLimit {
					return nil, ErrConflict
				}
				for _, purpose := range kernel.AllWorkPurposes {
					if current.PurposeUsed[purpose] > binding.PurposeLimits[purpose] {
						return nil, ErrConflict
					}
				}
				binding.ModelInvocationsUsed = current.ModelInvocationsUsed
				binding.PurposeUsed = current.PurposeUsed.Clone()
				if !binding.Valid() {
					return nil, ErrConflict
				}
			}
			ref := event.Aggregate
			projections = append(projections, phase4Projection{taskRef: &ref, binding: &binding})
		case "tekroo.event.task.operational-scope-bound":
			scope, err := kernel.TaskOperationalScopeFromPayload(event.Payload, event.EventID)
			if err != nil || event.Aggregate.Kind != kernel.AggregateTask || event.Aggregate.ID != scope.TaskID || event.AggregateRevision != scope.TaskRevision {
				return nil, ErrInvalidDecision
			}
			ref := event.Aggregate
			projections = append(projections, phase4Projection{taskRef: &ref, scope: &scope})
		case "tekroo.event.work-invocation.authorized":
			if debit == nil || !debit.Valid() {
				return nil, ErrInvalidDecision
			}
			invocation, err := kernel.WorkInvocationFromAuthorizedEvent(event)
			if err != nil || event.Aggregate != invocation.Ref() || invocation.GlobalDebitOrdinal != debit.NextGlobalUsed || invocation.PurposeDebitOrdinal != debit.NextGlobalPurposeUsed {
				return nil, ErrInvalidDecision
			}
			account := s.workBudgetAccounts[debit.Account].Clone()
			binding := s.taskWorkBudgets[debit.Task].Clone()
			if invocation.RemainingGlobalBudget != account.ModelInvocationLimit-debit.NextGlobalUsed || invocation.RemainingPurposeBudget != account.PurposeLimits[debit.Purpose]-debit.NextGlobalPurposeUsed {
				return nil, ErrInvalidDecision
			}
			account.ModelInvocationsUsed = debit.NextGlobalUsed
			account.PurposeUsed[debit.Purpose] = debit.NextGlobalPurposeUsed
			account.LastEventID = event.EventID
			binding.ModelInvocationsUsed = debit.NextTaskUsed
			binding.PurposeUsed[debit.Purpose] = debit.NextTaskPurposeUsed
			accountRef, taskRef, invocationRef := debit.Account, debit.Task, event.Aggregate
			projections = append(projections, phase4Projection{budgetRef: &accountRef, budget: &account, taskRef: &taskRef, binding: &binding, invocationRef: &invocationRef, invocation: &invocation})
		case "tekroo.event.work-invocation.claimed", "tekroo.event.work-invocation.started", "tekroo.event.work-invocation.terminal-recorded", "tekroo.event.work-invocation.cancellation-requested", "tekroo.event.work-invocation.expired":
			current, found := s.workInvocations[event.Aggregate]
			if !found {
				return nil, ErrInvalidDecision
			}
			next, valid := kernel.ApplyWorkInvocationEvent(current, event)
			if !valid {
				return nil, ErrInvalidDecision
			}
			ref := event.Aggregate
			projections = append(projections, phase4Projection{invocationRef: &ref, invocation: &next})
		}
	}
	return projections, nil
}

func (s *Store) WorkBudgetAccount(reference kernel.AggregateRef) (kernel.WorkBudgetAccount, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	account, found := s.workBudgetAccounts[reference]
	return account.Clone(), found
}

func (s *Store) WorkInvocation(reference kernel.AggregateRef) (kernel.WorkInvocation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	invocation, found := s.workInvocations[reference]
	return invocation.Clone(), found
}
