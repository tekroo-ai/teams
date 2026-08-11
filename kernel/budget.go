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
