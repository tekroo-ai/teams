package mongo

import (
	"context"
	"errors"

	"github.com/tekroo-ai/teams/kernel"
	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type workBudgetValue struct {
	Reference kernel.AggregateRef      `json:"reference"`
	Account   kernel.WorkBudgetAccount `json:"account"`
}

type taskBudgetValue struct {
	Task    kernel.AggregateRef          `json:"task"`
	Binding kernel.TaskWorkBudgetBinding `json:"binding"`
}

type taskScopeValue struct {
	Task  kernel.AggregateRef         `json:"task"`
	Scope kernel.TaskOperationalScope `json:"scope"`
}

type invocationValue struct {
	Reference  kernel.AggregateRef   `json:"reference"`
	Invocation kernel.WorkInvocation `json:"invocation"`
}

func (s *Store) loadPhase4State(ctx context.Context, snapshot *kernel.Snapshot) error {
	snapshot.WorkBudgetAccounts = make(map[kernel.AggregateRef]kernel.WorkBudgetAccount)
	if err := scan(ctx, s.db.Collection("work_budget_accounts"), bson.D{}, func(document valueDocument) error {
		var value workBudgetValue
		if decode(document.Data, &value) != nil || value.Reference.Kind != kernel.AggregateWorkBudget || value.Reference != value.Account.Ref() || !value.Account.Valid() || document.ID != aggregateKey(value.Reference) {
			return ErrCorruptAggregate
		}
		snapshot.WorkBudgetAccounts[value.Reference] = value.Account
		return nil
	}); err != nil {
		return err
	}
	snapshot.TaskWorkBudgets = make(map[kernel.AggregateRef]kernel.TaskWorkBudgetBinding)
	if err := scan(ctx, s.db.Collection("task_work_budgets"), bson.D{}, func(document valueDocument) error {
		var value taskBudgetValue
		if decode(document.Data, &value) != nil || value.Task.Kind != kernel.AggregateTask || value.Task.ID != value.Binding.TaskID || !value.Binding.Valid() || document.ID != aggregateKey(value.Task) {
			return ErrCorruptAggregate
		}
		snapshot.TaskWorkBudgets[value.Task] = value.Binding
		return nil
	}); err != nil {
		return err
	}
	snapshot.TaskOperationalScopes = make(map[kernel.AggregateRef]kernel.TaskOperationalScope)
	if err := scan(ctx, s.db.Collection("task_operational_scopes"), bson.D{}, func(document valueDocument) error {
		var value taskScopeValue
		if decode(document.Data, &value) != nil || value.Task.Kind != kernel.AggregateTask || value.Task.ID != value.Scope.TaskID || !value.Scope.Valid() || document.ID != aggregateKey(value.Task) {
			return ErrCorruptAggregate
		}
		snapshot.TaskOperationalScopes[value.Task] = value.Scope
		return nil
	}); err != nil {
		return err
	}
	snapshot.WorkInvocations = make(map[kernel.AggregateRef]kernel.WorkInvocation)
	if err := scan(ctx, s.db.Collection("work_invocations"), bson.D{}, func(document valueDocument) error {
		var value invocationValue
		if decode(document.Data, &value) != nil || value.Reference.Kind != kernel.AggregateWorkInvocation || value.Reference != value.Invocation.Ref() || !value.Invocation.Valid() || document.ID != aggregateKey(value.Reference) {
			return ErrCorruptAggregate
		}
		snapshot.WorkInvocations[value.Reference] = value.Invocation
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func (s *Store) applyPhase4Event(ctx context.Context, event kernel.DomainEvent, debit *kernel.WorkBudgetDebitDecision) (bool, error) {
	switch event.EventType {
	case "tekroo.event.work-budget.created":
		account, err := kernel.WorkBudgetFromCreatePayload(event.Payload, event.EventID)
		if err != nil || event.Aggregate != account.Ref() || event.AggregateRevision != 1 {
			return true, ErrConflict
		}
		return true, s.insertValue(ctx, "work_budget_accounts", aggregateKey(event.Aggregate), workBudgetValue{Reference: event.Aggregate, Account: account})
	case "tekroo.event.work-budget.amended":
		var document valueDocument
		if err := s.db.Collection("work_budget_accounts").FindOne(ctx, bson.D{{Key: "_id", Value: aggregateKey(event.Aggregate)}}).Decode(&document); err != nil {
			return true, err
		}
		var value workBudgetValue
		if decode(document.Data, &value) != nil || value.Reference != event.Aggregate || value.Account.Revision+1 != event.AggregateRevision {
			return true, ErrConflict
		}
		next, valid := kernel.ApplyWorkBudgetAmendment(value.Account, event.Payload, event.EventID)
		if !valid || next.Revision != event.AggregateRevision {
			return true, ErrConflict
		}
		value.Account = next
		return true, s.replaceValue(ctx, "work_budget_accounts", aggregateKey(event.Aggregate), value)
	case "tekroo.event.task.work-budget-bound":
		binding, err := kernel.TaskWorkBudgetBindingFromPayload(event.Payload, event.EventID)
		if err != nil || event.Aggregate.Kind != kernel.AggregateTask || event.Aggregate.ID != binding.TaskID || event.AggregateRevision != binding.TaskRevision {
			return true, ErrConflict
		}
		var existing valueDocument
		err = s.db.Collection("task_work_budgets").FindOne(ctx, bson.D{{Key: "_id", Value: aggregateKey(event.Aggregate)}}).Decode(&existing)
		if err == nil {
			var current taskBudgetValue
			if decode(existing.Data, &current) != nil || current.Binding.BudgetAccountID != binding.BudgetAccountID || current.Binding.ModelInvocationsUsed > binding.ModelInvocationLimit {
				return true, ErrConflict
			}
			for _, purpose := range kernel.AllWorkPurposes {
				if current.Binding.PurposeUsed[purpose] > binding.PurposeLimits[purpose] {
					return true, ErrConflict
				}
			}
			binding.ModelInvocationsUsed = current.Binding.ModelInvocationsUsed
			binding.PurposeUsed = current.Binding.PurposeUsed.Clone()
			if !binding.Valid() {
				return true, ErrConflict
			}
		} else if !errors.Is(err, driver.ErrNoDocuments) {
			return true, err
		}
		data, err := encode(taskBudgetValue{Task: event.Aggregate, Binding: binding})
		if err != nil {
			return true, err
		}
		_, err = s.db.Collection("task_work_budgets").ReplaceOne(ctx, bson.D{{Key: "_id", Value: aggregateKey(event.Aggregate)}}, valueDocument{ID: aggregateKey(event.Aggregate), Data: data}, options.Replace().SetUpsert(true))
		return true, err
	case "tekroo.event.task.operational-scope-bound":
		scope, err := kernel.TaskOperationalScopeFromPayload(event.Payload, event.EventID)
		if err != nil || event.Aggregate.Kind != kernel.AggregateTask || event.Aggregate.ID != scope.TaskID || event.AggregateRevision != scope.TaskRevision {
			return true, ErrConflict
		}
		data, err := encode(taskScopeValue{Task: event.Aggregate, Scope: scope})
		if err != nil {
			return true, err
		}
		_, err = s.db.Collection("task_operational_scopes").ReplaceOne(ctx, bson.D{{Key: "_id", Value: aggregateKey(event.Aggregate)}}, valueDocument{ID: aggregateKey(event.Aggregate), Data: data}, options.Replace().SetUpsert(true))
		return true, err
	case "tekroo.event.work-invocation.authorized":
		if debit == nil || !debit.Valid() {
			return true, ErrInvalidDecision
		}
		invocation, err := kernel.WorkInvocationFromAuthorizedEvent(event)
		if err != nil || invocation.GlobalDebitOrdinal != debit.NextGlobalUsed || invocation.PurposeDebitOrdinal != debit.NextGlobalPurposeUsed {
			return true, ErrConflict
		}
		accountDocument, bindingDocument := valueDocument{}, valueDocument{}
		if err := s.db.Collection("work_budget_accounts").FindOne(ctx, bson.D{{Key: "_id", Value: aggregateKey(debit.Account)}}).Decode(&accountDocument); err != nil {
			return true, err
		}
		if err := s.db.Collection("task_work_budgets").FindOne(ctx, bson.D{{Key: "_id", Value: aggregateKey(debit.Task)}}).Decode(&bindingDocument); err != nil {
			return true, err
		}
		accountValue, bindingValue := workBudgetValue{}, taskBudgetValue{}
		if decode(accountDocument.Data, &accountValue) != nil || decode(bindingDocument.Data, &bindingValue) != nil || accountValue.Account.Revision != debit.ExpectedAccountRevision || accountValue.Account.ModelInvocationsUsed != debit.ExpectedGlobalUsed || accountValue.Account.PurposeUsed[debit.Purpose] != debit.ExpectedGlobalPurposeUsed || bindingValue.Binding.ModelInvocationsUsed != debit.ExpectedTaskUsed || bindingValue.Binding.PurposeUsed[debit.Purpose] != debit.ExpectedTaskPurposeUsed {
			return true, ErrConflict
		}
		if invocation.RemainingGlobalBudget != accountValue.Account.ModelInvocationLimit-debit.NextGlobalUsed || invocation.RemainingPurposeBudget != accountValue.Account.PurposeLimits[debit.Purpose]-debit.NextGlobalPurposeUsed {
			return true, ErrConflict
		}
		accountValue.Account.ModelInvocationsUsed = debit.NextGlobalUsed
		accountValue.Account.PurposeUsed[debit.Purpose] = debit.NextGlobalPurposeUsed
		accountValue.Account.LastEventID = event.EventID
		bindingValue.Binding.ModelInvocationsUsed = debit.NextTaskUsed
		bindingValue.Binding.PurposeUsed[debit.Purpose] = debit.NextTaskPurposeUsed
		if err := s.replaceValue(ctx, "work_budget_accounts", aggregateKey(debit.Account), accountValue); err != nil {
			return true, err
		}
		if err := s.replaceValue(ctx, "task_work_budgets", aggregateKey(debit.Task), bindingValue); err != nil {
			return true, err
		}
		return true, s.insertValue(ctx, "work_invocations", aggregateKey(event.Aggregate), invocationValue{Reference: event.Aggregate, Invocation: invocation})
	case "tekroo.event.work-invocation.claimed", "tekroo.event.work-invocation.started", "tekroo.event.work-invocation.terminal-recorded", "tekroo.event.work-invocation.cancellation-requested", "tekroo.event.work-invocation.expired":
		var document valueDocument
		if err := s.db.Collection("work_invocations").FindOne(ctx, bson.D{{Key: "_id", Value: aggregateKey(event.Aggregate)}}).Decode(&document); err != nil {
			return true, err
		}
		var value invocationValue
		if decode(document.Data, &value) != nil || value.Reference != event.Aggregate {
			return true, ErrConflict
		}
		next, valid := kernel.ApplyWorkInvocationEvent(value.Invocation, event)
		if !valid {
			return true, ErrConflict
		}
		value.Invocation = next
		return true, s.replaceValue(ctx, "work_invocations", aggregateKey(event.Aggregate), value)
	default:
		return false, nil
	}
}

func (s *Store) replaceValue(ctx context.Context, collection, id string, value any) error {
	data, err := encode(value)
	if err != nil {
		return err
	}
	result, err := s.db.Collection(collection).ReplaceOne(ctx, bson.D{{Key: "_id", Value: id}}, valueDocument{ID: id, Data: data})
	if err != nil {
		return err
	}
	if result.ModifiedCount != 1 {
		return ErrConflict
	}
	return nil
}
