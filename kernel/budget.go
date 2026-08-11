package kernel

type BudgetOutcome string

const (
	BudgetConsumed  BudgetOutcome = "CONSUMED"
	BudgetDuplicate BudgetOutcome = "DUPLICATE"
	BudgetExhausted BudgetOutcome = "EXHAUSTED"
	BudgetRejected  BudgetOutcome = "REJECTED_UNCHANGED"
)

type AttemptBudget struct {
	Limit    uint32
	Used     uint32
	attempts map[string]Digest
}

type AttemptBudgetKey struct {
	Subject   AggregateRef `json:"subject"`
	Operation string       `json:"operation"`
}

type AttemptBudgetSnapshot struct {
	Limit    uint32            `json:"limit"`
	Used     uint32            `json:"used"`
	Attempts map[string]Digest `json:"attempts"`
}

type AttemptBudgetDecision struct {
	Key             AttemptBudgetKey `json:"key"`
	ExpectedUsed    uint32           `json:"expected_used"`
	Limit           uint32           `json:"limit"`
	AttemptKey      string           `json:"attempt_key"`
	ConditionDigest Digest           `json:"condition_digest"`
}

func NewAttemptBudget(limit uint32) AttemptBudget {
	return AttemptBudget{Limit: limit, attempts: make(map[string]Digest)}
}

func (b AttemptBudget) Clone() AttemptBudget {
	copy := AttemptBudget{Limit: b.Limit, Used: b.Used, attempts: make(map[string]Digest, len(b.attempts))}
	for key, digest := range b.attempts {
		copy.attempts[key] = digest
	}
	return copy
}

func (b *AttemptBudget) Consume(attemptKey string, conditionDigest Digest, changedCondition, authorizedOverride bool) BudgetOutcome {
	if b.attempts == nil {
		b.attempts = make(map[string]Digest)
	}
	if prior, exists := b.attempts[attemptKey]; exists {
		if prior == conditionDigest {
			return BudgetDuplicate
		}
		return BudgetRejected
	}
	if attemptKey == "" || !conditionDigest.Valid() || (!changedCondition && !authorizedOverride) {
		return BudgetRejected
	}
	if b.Limit == 0 || b.Used >= b.Limit {
		return BudgetExhausted
	}
	b.attempts[attemptKey] = conditionDigest
	b.Used++
	return BudgetConsumed
}

func evaluateAttemptPolicy(command KernelCommand, snapshot Snapshot) (*AttemptBudgetDecision, OutcomeCode, string) {
	limit, governed := snapshot.Authorization.Requirements.AttemptLimits[command.CommandType]
	if !governed {
		return nil, OutcomeApplied, reasonApplied
	}
	if limit == 0 {
		return nil, OutcomeRejectedPolicy, "RETRY_BUDGET_EXHAUSTED"
	}
	key := AttemptBudgetKey{Subject: command.Target, Operation: command.CommandType}
	current, found := snapshot.AttemptBudgets[key]
	if !found {
		current = AttemptBudgetSnapshot{Limit: limit, Attempts: make(map[string]Digest)}
	}
	if current.Limit != limit || current.Used > current.Limit {
		return nil, OutcomeRejectedConflict, reasonPolicyConflict
	}
	conditionCommand := command
	conditionCommand.Causation = nil
	conditionDigest, err := CommandFingerprint(conditionCommand)
	if err != nil {
		return nil, OutcomeRejectedInvalid, reasonInvalidEnvelope
	}
	changed := true
	for _, prior := range current.Attempts {
		if prior == conditionDigest {
			changed = false
			break
		}
	}
	budget := AttemptBudget{Limit: current.Limit, Used: current.Used, attempts: make(map[string]Digest, len(current.Attempts))}
	for attemptKey, digest := range current.Attempts {
		budget.attempts[attemptKey] = digest
	}
	attemptKey := string(command.CommandID)
	switch budget.Consume(attemptKey, conditionDigest, changed, snapshot.Authorization.Requirements.AllowUnchangedRetry) {
	case BudgetConsumed:
		return &AttemptBudgetDecision{Key: key, ExpectedUsed: current.Used, Limit: limit, AttemptKey: attemptKey, ConditionDigest: conditionDigest}, OutcomeApplied, reasonApplied
	case BudgetExhausted:
		return nil, OutcomeRejectedPolicy, "RETRY_BUDGET_EXHAUSTED"
	case BudgetDuplicate:
		return nil, OutcomeRejectedConflict, "DOMAIN_ATTEMPT_REPLAY"
	default:
		return nil, OutcomeRejectedPolicy, "RETRY_CONDITION_UNCHANGED"
	}
}

func (value AttemptBudgetDecision) Valid() bool {
	if !value.Key.Subject.Valid() || value.Key.Operation == "" || value.Limit == 0 || value.ExpectedUsed >= value.Limit || value.AttemptKey == "" || !value.ConditionDigest.Valid() {
		return false
	}
	return true
}
