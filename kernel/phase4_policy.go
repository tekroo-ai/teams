package kernel

import "encoding/json"

type phase4PolicyDecision struct {
	EventPayload   json.RawMessage
	WorkBudget     *WorkBudgetDebitDecision
	LifecycleEpoch uint64
}

func evaluatePhase4Command(command KernelCommand, snapshot Snapshot, context DecisionContext) (phase4PolicyDecision, OutcomeCode, string, bool) {
	result := phase4PolicyDecision{EventPayload: append(json.RawMessage(nil), command.Payload...), LifecycleEpoch: 1}
	switch command.CommandType {
	case "tekroo.command.work-budget.create":
		account, err := WorkBudgetFromCreatePayload(command.Payload, context.EventID)
		if err != nil || account.ID != command.Target.ID || !payloadEvidenceMatchesObject(command.Payload, command.EvidenceRefs) || !payloadAuthorityMatches(command.Payload, command.Authority) {
			return result, OutcomeRejectedInvalid, reasonInvalidPayload, true
		}
		root, found := snapshot.Related[account.RootWork]
		if !found || !root.Exists || root.State == nil || root.State.LifecycleEpoch != account.LifecycleEpoch || !containsAggregatePrecondition(command.Preconditions, account.RootWork) || !context.DecidedAt.Before(account.DeadlineAt) {
			return result, OutcomeRejectedConflict, "INVALID_ROOT_BUDGET", true
		}
		result.LifecycleEpoch = account.LifecycleEpoch
		return result, OutcomeApplied, reasonApplied, true
	case "tekroo.command.work-budget.amend":
		current, found := snapshot.WorkBudgetAccounts[command.Target]
		if !found || !current.Valid() {
			return result, OutcomeRejectedConflict, "MISSING_BUDGET", true
		}
		_, valid := ApplyWorkBudgetAmendment(current, command.Payload, context.EventID)
		if !valid || !payloadEvidenceMatchesObject(command.Payload, command.EvidenceRefs) || !payloadAuthorityMatches(command.Payload, command.Authority) {
			return result, OutcomeRejectedPolicy, "INVALID_BUDGET_AMENDMENT", true
		}
		result.LifecycleEpoch = current.LifecycleEpoch
		return result, OutcomeApplied, reasonApplied, true
	case "tekroo.command.task.bind-work-budget":
		binding, err := TaskWorkBudgetBindingFromPayload(command.Payload, context.EventID)
		accountRef := AggregateRef{Kind: AggregateWorkBudget, ID: binding.BudgetAccountID}
		account, accountFound := snapshot.WorkBudgetAccounts[accountRef]
		if err != nil || snapshot.State == nil || binding.TaskID != command.Target.ID || binding.TaskRevision != snapshot.Revision+1 || binding.LifecycleEpoch != snapshot.State.LifecycleEpoch || binding.ScopeRevision != snapshot.State.ScopeRevision || !accountFound || !account.Valid() || account.LifecycleEpoch != binding.LifecycleEpoch || !containsAggregatePrecondition(command.Preconditions, accountRef) || !payloadEvidenceMatchesObject(command.Payload, command.EvidenceRefs) {
			return result, OutcomeRejectedConflict, "INVALID_BUDGET_BINDING", true
		}
		if current, exists := snapshot.TaskWorkBudgets[command.Target]; exists {
			if current.BudgetAccountID != binding.BudgetAccountID {
				return result, OutcomeRejectedPolicy, "BUDGET_RESET_PROHIBITED", true
			}
			if current.ModelInvocationsUsed > binding.ModelInvocationLimit {
				return result, OutcomeRejectedPolicy, "CAPACITY_MINT_PROHIBITED", true
			}
			for _, purpose := range AllWorkPurposes {
				if current.PurposeUsed[purpose] > binding.PurposeLimits[purpose] {
					return result, OutcomeRejectedPolicy, "CAPACITY_MINT_PROHIBITED", true
				}
			}
		}
		if binding.ModelInvocationLimit > account.ModelInvocationLimit {
			return result, OutcomeRejectedPolicy, "CAPACITY_MINT_PROHIBITED", true
		}
		for _, purpose := range AllWorkPurposes {
			if binding.PurposeLimits[purpose] > account.PurposeLimits[purpose] {
				return result, OutcomeRejectedPolicy, "CAPACITY_MINT_PROHIBITED", true
			}
		}
		result.LifecycleEpoch = binding.LifecycleEpoch
		return result, OutcomeApplied, reasonApplied, true
	case "tekroo.command.task.bind-operational-scope":
		scope, err := TaskOperationalScopeFromPayload(command.Payload, context.EventID)
		if err != nil || snapshot.State == nil || scope.TaskID != command.Target.ID || scope.TaskRevision != snapshot.Revision+1 || scope.LifecycleEpoch != snapshot.State.LifecycleEpoch || scope.ScopeRevision != snapshot.State.ScopeRevision || snapshot.State.Ownership.OwnerFQN == nil || *snapshot.State.Ownership.OwnerFQN != scope.OwnerFQN || snapshot.CurrentExecutions[scope.OwnerFQN] != scope.Execution || !payloadEvidenceFieldMatchesObject(command.Payload, "interface_constraint_evidence_ids", command.EvidenceRefs) {
			return result, OutcomeRejectedConflict, "INVALID_OPERATIONAL_SCOPE", true
		}
		result.LifecycleEpoch = scope.LifecycleEpoch
		return result, OutcomeApplied, reasonApplied, true
	case "tekroo.command.work-invocation.authorize":
		value, err := parseInvocationAuthorization(command.Payload)
		if err != nil || value.InvocationID != command.Target.ID || command.IdempotencyKey != value.IdempotencyKey || !containsDagParent(command.Causation, value.ParentEventID, EdgeCausal) {
			return result, OutcomeRejectedInvalid, "INVALID_INVOCATION", true
		}
		taskRef := AggregateRef{Kind: AggregateTask, ID: value.TaskID}
		budgetRef := AggregateRef{Kind: AggregateWorkBudget, ID: value.BudgetAccountID}
		taskRelated, taskFound := snapshot.Related[taskRef]
		account, budgetFound := snapshot.WorkBudgetAccounts[budgetRef]
		binding, bindingFound := snapshot.TaskWorkBudgets[taskRef]
		scope, scopeFound := snapshot.TaskOperationalScopes[taskRef]
		profile, profileFound := snapshot.WorkProfiles[taskRef]
		assignment, assignmentFound := snapshot.QualifiedAssignments[taskRef]
		if !taskFound || taskRelated.State == nil || !budgetFound || !bindingFound || !scopeFound || !profileFound || !assignmentFound || !containsAggregatePrecondition(command.Preconditions, taskRef) || !containsAggregatePrecondition(command.Preconditions, budgetRef) {
			return result, OutcomeRejectedPolicy, "MISSING_BUDGET", true
		}
		decision := PlanWorkInvocationAuthorization(command.Payload, *taskRelated.State, account, binding, scope, profile, assignment, snapshot.CurrentExecutions[value.ActorFQN], snapshot.WorkInvocations, context.DecidedAt, context.EventID)
		if !decision.Accepted {
			return result, OutcomeRejectedPolicy, decision.Reason, true
		}
		result.EventPayload = decision.EventPayload
		result.WorkBudget = decision.BudgetDebit
		result.LifecycleEpoch = decision.Invocation.LifecycleEpoch
		return result, OutcomeApplied, reasonApplied, true
	case "tekroo.command.work-invocation.claim",
		"tekroo.command.work-invocation.record-started",
		"tekroo.command.work-invocation.record-terminal",
		"tekroo.command.work-invocation.request-cancellation",
		"tekroo.command.work-invocation.expire":
		current, found := snapshot.WorkInvocations[command.Target]
		if !found || !current.Valid() {
			return result, OutcomeRejectedNotFound, reasonNotFound, true
		}
		eventType := map[string]string{
			"tekroo.command.work-invocation.claim":                "tekroo.event.work-invocation.claimed",
			"tekroo.command.work-invocation.record-started":       "tekroo.event.work-invocation.started",
			"tekroo.command.work-invocation.record-terminal":      "tekroo.event.work-invocation.terminal-recorded",
			"tekroo.command.work-invocation.request-cancellation": "tekroo.event.work-invocation.cancellation-requested",
			"tekroo.command.work-invocation.expire":               "tekroo.event.work-invocation.expired",
		}[command.CommandType]
		event := DomainEvent{EventID: context.EventID, EventType: eventType, Aggregate: command.Target, AggregateRevision: snapshot.Revision + 1, Payload: command.Payload}
		if _, valid := ApplyWorkInvocationEvent(current, event); !valid {
			if command.CommandType == "tekroo.command.work-invocation.claim" && current.State != InvocationAuthorized {
				return result, OutcomeRejectedConflict, "PERMIT_ALREADY_CONSUMED", true
			}
			if command.CommandType == "tekroo.command.work-invocation.record-terminal" {
				return result, OutcomeRejectedPolicy, "TERMINAL_EVIDENCE_REQUIRED", true
			}
			return result, OutcomeRejectedConflict, "INVALID_INVOCATION_TRANSITION", true
		}
		result.LifecycleEpoch = current.LifecycleEpoch
		return result, OutcomeApplied, reasonApplied, true
	default:
		return result, OutcomeApplied, reasonApplied, false
	}
}

func payloadEvidenceMatchesObject(payload []byte, references []EvidenceRef) bool {
	object, err := decodePayloadObject(payload)
	return err == nil && payloadEvidenceMatches(object, references)
}

func payloadEvidenceFieldMatchesObject(payload []byte, field string, references []EvidenceRef) bool {
	object, err := decodePayloadObject(payload)
	return err == nil && payloadEvidenceFieldMatches(object, field, references)
}

func payloadAuthorityMatches(payload []byte, authority PrincipalRef) bool {
	var object struct {
		Authority PrincipalRef `json:"authority"`
	}
	return json.Unmarshal(payload, &object) == nil && object.Authority == authority
}
