package kernel

import (
	"encoding/json"
	"errors"
	"sort"
	"time"
)

type WorkPurpose string

const (
	PurposeInvestigation  WorkPurpose = "INVESTIGATION"
	PurposeImplementation WorkPurpose = "IMPLEMENTATION"
	PurposeValidation     WorkPurpose = "VALIDATION"
	PurposeReview         WorkPurpose = "REVIEW"
	PurposeRepair         WorkPurpose = "REPAIR"
	PurposeHandoff        WorkPurpose = "HANDOFF"
	PurposeReplan         WorkPurpose = "REPLAN"
	PurposePromotion      WorkPurpose = "PROMOTION"
	PurposeEscalation     WorkPurpose = "ESCALATION"
)

var AllWorkPurposes = []WorkPurpose{
	PurposeInvestigation, PurposeImplementation, PurposeValidation,
	PurposeReview, PurposeRepair, PurposeHandoff, PurposeReplan,
	PurposePromotion, PurposeEscalation,
}

func (p WorkPurpose) Valid() bool {
	for _, candidate := range AllWorkPurposes {
		if p == candidate {
			return true
		}
	}
	return false
}

type PurposeCounters map[WorkPurpose]uint64

func (c PurposeCounters) Clone() PurposeCounters {
	copy := make(PurposeCounters, len(c))
	for purpose, value := range c {
		copy[purpose] = value
	}
	return copy
}

func (c PurposeCounters) Valid() bool {
	if len(c) != len(AllWorkPurposes) {
		return false
	}
	for _, purpose := range AllWorkPurposes {
		value, found := c[purpose]
		if !found || value > 1000 {
			return false
		}
	}
	return true
}

func zeroPurposeCounters() PurposeCounters {
	result := make(PurposeCounters, len(AllWorkPurposes))
	for _, purpose := range AllWorkPurposes {
		result[purpose] = 0
	}
	return result
}

type WorkBudgetAccount struct {
	ID                   UUIDv7          `json:"id"`
	Revision             uint64          `json:"revision"`
	RootWork             AggregateRef    `json:"root_work"`
	LifecycleEpoch       uint64          `json:"lifecycle_epoch"`
	PolicyRevision       uint64          `json:"policy_revision"`
	PolicyDigest         Digest          `json:"policy_digest"`
	ModelInvocationLimit uint64          `json:"model_invocation_limit"`
	ModelInvocationsUsed uint64          `json:"model_invocations_used"`
	PurposeLimits        PurposeCounters `json:"purpose_limits"`
	PurposeUsed          PurposeCounters `json:"purpose_used"`
	DeadlineAt           time.Time       `json:"deadline_at"`
	LastEventID          UUIDv7          `json:"last_event_id"`
}

func (b WorkBudgetAccount) Ref() AggregateRef {
	return AggregateRef{Kind: AggregateWorkBudget, ID: b.ID}
}

func (b WorkBudgetAccount) Valid() bool {
	if !b.ID.Valid() || b.Revision == 0 || b.Revision > 1000000 || (b.RootWork.Kind != AggregateStory && b.RootWork.Kind != AggregateTask) || !b.RootWork.ID.Valid() || b.LifecycleEpoch == 0 || b.PolicyRevision == 0 || !b.PolicyDigest.Valid() || b.ModelInvocationLimit == 0 || b.ModelInvocationLimit > 1000 || b.ModelInvocationsUsed > b.ModelInvocationLimit || !b.PurposeLimits.Valid() || !b.PurposeUsed.Valid() || b.DeadlineAt.IsZero() || !b.LastEventID.Valid() {
		return false
	}
	for _, purpose := range AllWorkPurposes {
		if b.PurposeUsed[purpose] > b.PurposeLimits[purpose] || b.PurposeLimits[purpose] > b.ModelInvocationLimit {
			return false
		}
	}
	return true
}

func (b WorkBudgetAccount) Clone() WorkBudgetAccount {
	copy := b
	copy.PurposeLimits = b.PurposeLimits.Clone()
	copy.PurposeUsed = b.PurposeUsed.Clone()
	return copy
}

func (b WorkBudgetAccount) MaximumAdditionalInvocations() uint64 {
	if b.ModelInvocationsUsed >= b.ModelInvocationLimit {
		return 0
	}
	return b.ModelInvocationLimit - b.ModelInvocationsUsed
}

type WorkBudgetInheritanceDecision struct {
	Accepted                     bool
	Reason                       string
	AccountID                    UUIDv7
	MaximumAdditionalInvocations uint64
}

func PlanWorkBudgetInheritance(current WorkBudgetAccount, requestedAccountID UUIDv7, requestedLimit uint64) WorkBudgetInheritanceDecision {
	decision := WorkBudgetInheritanceDecision{AccountID: current.ID, MaximumAdditionalInvocations: current.MaximumAdditionalInvocations()}
	if !current.Valid() || !requestedAccountID.Valid() || requestedLimit == 0 {
		decision.Reason = "INVALID_BUDGET"
		return decision
	}
	if requestedAccountID != current.ID {
		decision.Reason = "BUDGET_RESET_PROHIBITED"
		return decision
	}
	if requestedLimit > current.ModelInvocationLimit {
		decision.Reason = "CAPACITY_MINT_PROHIBITED"
		return decision
	}
	decision.Accepted = true
	decision.Reason = "ACCEPTED"
	return decision
}

func ConservativeMaximumAdditionalInvocations(accounts []WorkBudgetAccount) uint64 {
	var total uint64
	for _, account := range accounts {
		remaining := account.MaximumAdditionalInvocations()
		if ^uint64(0)-total < remaining {
			return ^uint64(0)
		}
		total += remaining
	}
	return total
}

type workBudgetPayload struct {
	BudgetAccountID      UUIDv7          `json:"budget_account_id"`
	RootWork             AggregateRef    `json:"root_work"`
	LifecycleEpoch       uint64          `json:"lifecycle_epoch"`
	ExpectedEpoch        uint64          `json:"expected_lifecycle_epoch"`
	ExpectedRevision     uint64          `json:"expected_budget_revision"`
	PolicyRevision       uint64          `json:"policy_revision"`
	PolicyDigest         Digest          `json:"policy_digest"`
	ModelInvocationLimit uint64          `json:"model_invocation_limit"`
	PurposeLimits        PurposeCounters `json:"purpose_limits"`
	DeadlineAt           time.Time       `json:"deadline_at"`
	EvidenceIDs          []UUIDv7        `json:"evidence_ids"`
	Authority            PrincipalRef    `json:"authority"`
}

func WorkBudgetFromCreatePayload(payload []byte, eventID UUIDv7) (WorkBudgetAccount, error) {
	var value workBudgetPayload
	if json.Unmarshal(payload, &value) != nil {
		return WorkBudgetAccount{}, errors.New("invalid work budget payload")
	}
	account := WorkBudgetAccount{
		ID: value.BudgetAccountID, Revision: 1, RootWork: value.RootWork,
		LifecycleEpoch: value.LifecycleEpoch, PolicyRevision: value.PolicyRevision,
		PolicyDigest: value.PolicyDigest, ModelInvocationLimit: value.ModelInvocationLimit,
		PurposeLimits: value.PurposeLimits.Clone(), PurposeUsed: zeroPurposeCounters(),
		DeadlineAt: value.DeadlineAt, LastEventID: eventID,
	}
	if !account.Valid() || len(value.EvidenceIDs) == 0 || !value.Authority.Valid() {
		return WorkBudgetAccount{}, errors.New("invalid work budget payload")
	}
	return account, nil
}

func ApplyWorkBudgetAmendment(current WorkBudgetAccount, payload []byte, eventID UUIDv7) (WorkBudgetAccount, bool) {
	var value workBudgetPayload
	if json.Unmarshal(payload, &value) != nil || value.BudgetAccountID != current.ID || value.ExpectedRevision != current.Revision || value.ExpectedEpoch != current.LifecycleEpoch || value.PolicyRevision <= current.PolicyRevision || value.ModelInvocationLimit < current.ModelInvocationsUsed || value.DeadlineAt.Before(current.DeadlineAt) || len(value.EvidenceIDs) == 0 || !value.Authority.Valid() {
		return current, false
	}
	for _, purpose := range AllWorkPurposes {
		if value.PurposeLimits[purpose] < current.PurposeUsed[purpose] {
			return current, false
		}
	}
	next := current.Clone()
	next.Revision++
	next.PolicyRevision = value.PolicyRevision
	next.PolicyDigest = value.PolicyDigest
	next.ModelInvocationLimit = value.ModelInvocationLimit
	next.PurposeLimits = value.PurposeLimits.Clone()
	next.DeadlineAt = value.DeadlineAt
	next.LastEventID = eventID
	return next, next.Valid()
}

type TaskWorkBudgetBinding struct {
	TaskID               UUIDv7
	BudgetAccountID      UUIDv7
	TaskRevision         uint64
	LifecycleEpoch       uint64
	ScopeRevision        uint64
	ModelInvocationLimit uint64
	ModelInvocationsUsed uint64
	PurposeLimits        PurposeCounters
	PurposeUsed          PurposeCounters
	BoundEventID         UUIDv7
}

func (b TaskWorkBudgetBinding) Valid() bool {
	if !b.TaskID.Valid() || !b.BudgetAccountID.Valid() || b.TaskRevision == 0 || b.LifecycleEpoch == 0 || b.ScopeRevision == 0 || b.ModelInvocationLimit == 0 || b.ModelInvocationLimit > 1000 || b.ModelInvocationsUsed > b.ModelInvocationLimit || !b.PurposeLimits.Valid() || !b.PurposeUsed.Valid() || !b.BoundEventID.Valid() {
		return false
	}
	for _, purpose := range AllWorkPurposes {
		if b.PurposeUsed[purpose] > b.PurposeLimits[purpose] || b.PurposeLimits[purpose] > b.ModelInvocationLimit {
			return false
		}
	}
	return true
}

func (b TaskWorkBudgetBinding) Clone() TaskWorkBudgetBinding {
	copy := b
	copy.PurposeLimits = b.PurposeLimits.Clone()
	copy.PurposeUsed = b.PurposeUsed.Clone()
	return copy
}

func TaskWorkBudgetBindingFromPayload(payload []byte, eventID UUIDv7) (TaskWorkBudgetBinding, error) {
	var value struct {
		TaskID                   UUIDv7          `json:"task_id"`
		BudgetAccountID          UUIDv7          `json:"budget_account_id"`
		ExpectedTaskRevision     uint64          `json:"expected_task_revision"`
		LifecycleEpoch           uint64          `json:"lifecycle_epoch"`
		ScopeRevision            uint64          `json:"scope_revision"`
		TaskModelInvocationLimit uint64          `json:"task_model_invocation_limit"`
		PurposeLimits            PurposeCounters `json:"purpose_limits"`
	}
	if json.Unmarshal(payload, &value) != nil {
		return TaskWorkBudgetBinding{}, errors.New("invalid task budget binding")
	}
	binding := TaskWorkBudgetBinding{
		TaskID: value.TaskID, BudgetAccountID: value.BudgetAccountID,
		TaskRevision: value.ExpectedTaskRevision + 1, LifecycleEpoch: value.LifecycleEpoch,
		ScopeRevision: value.ScopeRevision, ModelInvocationLimit: value.TaskModelInvocationLimit,
		PurposeLimits: value.PurposeLimits.Clone(), PurposeUsed: zeroPurposeCounters(),
		BoundEventID: eventID,
	}
	if !binding.Valid() {
		return TaskWorkBudgetBinding{}, errors.New("invalid task budget binding")
	}
	return binding, nil
}

type TaskOperationalScope struct {
	TaskID               UUIDv7
	TaskRevision         uint64
	LifecycleEpoch       uint64
	ScopeRevision        uint64
	OwnerFQN             ActorFQN
	Execution            ExecutionTuple
	WorkspaceID          string
	WorktreeID           string
	Branch               string
	BaselineSHA          string
	WritablePaths        []string
	InterfaceEvidenceIDs []UUIDv7
	BoundEventID         UUIDv7
}

func (s TaskOperationalScope) Valid() bool {
	if !s.TaskID.Valid() || s.TaskRevision == 0 || s.LifecycleEpoch == 0 || s.ScopeRevision == 0 || !s.OwnerFQN.Valid() || !s.Execution.Valid() || s.WorkspaceID == "" || s.WorktreeID == "" || s.Branch == "" || len(s.BaselineSHA) != 40 || len(s.WritablePaths) == 0 || len(s.WritablePaths) > 256 || !s.BoundEventID.Valid() {
		return false
	}
	paths := append([]string(nil), s.WritablePaths...)
	sort.Strings(paths)
	for index, path := range paths {
		if path == "" || path != s.WritablePaths[index] || index > 0 && path == paths[index-1] {
			return false
		}
	}
	return validUniqueUUIDs(s.InterfaceEvidenceIDs, 0, 64)
}

func (s TaskOperationalScope) Clone() TaskOperationalScope {
	copy := s
	copy.WritablePaths = append([]string(nil), s.WritablePaths...)
	copy.InterfaceEvidenceIDs = append([]UUIDv7(nil), s.InterfaceEvidenceIDs...)
	return copy
}

func TaskOperationalScopeFromPayload(payload []byte, eventID UUIDv7) (TaskOperationalScope, error) {
	var value struct {
		TaskID               UUIDv7   `json:"task_id"`
		ExpectedTaskRevision uint64   `json:"expected_task_revision"`
		LifecycleEpoch       uint64   `json:"lifecycle_epoch"`
		ScopeRevision        uint64   `json:"scope_revision"`
		OwnerFQN             ActorFQN `json:"owner_fqn"`
		ExecutionID          UUIDv7   `json:"execution_id"`
		FencingEpoch         uint64   `json:"fencing_epoch"`
		WorkspaceID          string   `json:"workspace_id"`
		WorktreeID           string   `json:"worktree_id"`
		Branch               string   `json:"branch"`
		BaselineSHA          string   `json:"baseline_sha"`
		WritablePaths        []string `json:"writable_paths"`
		InterfaceEvidenceIDs []UUIDv7 `json:"interface_constraint_evidence_ids"`
	}
	if json.Unmarshal(payload, &value) != nil {
		return TaskOperationalScope{}, errors.New("invalid operational scope")
	}
	scope := TaskOperationalScope{
		TaskID: value.TaskID, TaskRevision: value.ExpectedTaskRevision + 1,
		LifecycleEpoch: value.LifecycleEpoch, ScopeRevision: value.ScopeRevision,
		OwnerFQN: value.OwnerFQN, Execution: ExecutionTuple{ExecutionID: value.ExecutionID, FencingEpoch: value.FencingEpoch},
		WorkspaceID: value.WorkspaceID, WorktreeID: value.WorktreeID, Branch: value.Branch,
		BaselineSHA: value.BaselineSHA, WritablePaths: append([]string(nil), value.WritablePaths...),
		InterfaceEvidenceIDs: append([]UUIDv7(nil), value.InterfaceEvidenceIDs...), BoundEventID: eventID,
	}
	sort.Strings(scope.WritablePaths)
	if !scope.Valid() {
		return TaskOperationalScope{}, errors.New("invalid operational scope")
	}
	return scope, nil
}

type WorkInvocationState string

const (
	InvocationAuthorized  WorkInvocationState = "AUTHORIZED"
	InvocationClaimed     WorkInvocationState = "CLAIMED"
	InvocationStarted     WorkInvocationState = "STARTED"
	InvocationStartFailed WorkInvocationState = "START_FAILED"
	InvocationSucceeded   WorkInvocationState = "SUCCEEDED"
	InvocationFailed      WorkInvocationState = "FAILED"
	InvocationTimedOut    WorkInvocationState = "TIMED_OUT"
	InvocationCancelled   WorkInvocationState = "CANCELLED"
	InvocationExpired     WorkInvocationState = "EXPIRED"
)

func (s WorkInvocationState) Terminal() bool {
	return s == InvocationStartFailed || s == InvocationSucceeded || s == InvocationFailed || s == InvocationTimedOut || s == InvocationCancelled || s == InvocationExpired
}

type WorkInvocation struct {
	ID                      UUIDv7
	Revision                uint64
	State                   WorkInvocationState
	AuthorizationEventID    UUIDv7
	ParentEventID           UUIDv7
	TaskID                  UUIDv7
	BudgetAccountID         UUIDv7
	LifecycleEpoch          uint64
	ScopeRevision           uint64
	TaskRevision            uint64
	WorkProfile             WorkProfileBinding
	QualifiedAssignmentID   UUIDv7
	Purpose                 WorkPurpose
	AttemptFamily           string
	AttemptOrdinal          uint64
	ConditionDigest         Digest
	RetryOfInvocationID     *UUIDv7
	RetryOrdinal            uint64
	OutputPredicateDigest   Digest
	AllowedTerminalOutcomes []WorkInvocationState
	ToolPolicyDigest        Digest
	EffectPolicyDigest      Digest
	ActorFQN                ActorFQN
	Execution               ExecutionTuple
	ModelProfileDigest      Digest
	RuntimeIdentityDigest   Digest
	WorkspaceID             string
	DeadlineAt              time.Time
	IdempotencyKey          string
	AdmissionPolicyRevision uint64
	AdmissionPolicyDigest   Digest
	GlobalDebitOrdinal      uint64
	PurposeDebitOrdinal     uint64
	RemainingGlobalBudget   uint64
	RemainingPurposeBudget  uint64
	ClaimID                 *UUIDv7
	ClaimedAt               *time.Time
	ConversationID          *string
	RequestDigest           *Digest
	StartedAt               *time.Time
	TerminalOutcome         *WorkInvocationState
	Retryable               *bool
	TerminalEvidenceIDs     []UUIDv7
	OutputDigest            *Digest
	FinishedAt              *time.Time
	CancellationRequestedAt *time.Time
	LastEventID             UUIDv7
}

func (i WorkInvocation) Ref() AggregateRef {
	return AggregateRef{Kind: AggregateWorkInvocation, ID: i.ID}
}

func (i WorkInvocation) Clone() WorkInvocation {
	copy := i
	copy.AllowedTerminalOutcomes = append([]WorkInvocationState(nil), i.AllowedTerminalOutcomes...)
	copy.TerminalEvidenceIDs = append([]UUIDv7(nil), i.TerminalEvidenceIDs...)
	if i.RetryOfInvocationID != nil {
		value := *i.RetryOfInvocationID
		copy.RetryOfInvocationID = &value
	}
	if i.ClaimID != nil {
		value := *i.ClaimID
		copy.ClaimID = &value
	}
	if i.ClaimedAt != nil {
		value := *i.ClaimedAt
		copy.ClaimedAt = &value
	}
	if i.ConversationID != nil {
		value := *i.ConversationID
		copy.ConversationID = &value
	}
	if i.RequestDigest != nil {
		value := *i.RequestDigest
		copy.RequestDigest = &value
	}
	if i.StartedAt != nil {
		value := *i.StartedAt
		copy.StartedAt = &value
	}
	if i.TerminalOutcome != nil {
		value := *i.TerminalOutcome
		copy.TerminalOutcome = &value
	}
	if i.Retryable != nil {
		value := *i.Retryable
		copy.Retryable = &value
	}
	if i.OutputDigest != nil {
		value := *i.OutputDigest
		copy.OutputDigest = &value
	}
	if i.FinishedAt != nil {
		value := *i.FinishedAt
		copy.FinishedAt = &value
	}
	if i.CancellationRequestedAt != nil {
		value := *i.CancellationRequestedAt
		copy.CancellationRequestedAt = &value
	}
	return copy
}

func (i WorkInvocation) Valid() bool {
	if !i.ID.Valid() || i.Revision == 0 || !i.AuthorizationEventID.Valid() || !i.ParentEventID.Valid() || !i.TaskID.Valid() || !i.BudgetAccountID.Valid() || i.LifecycleEpoch == 0 || i.ScopeRevision == 0 || i.TaskRevision == 0 || !i.WorkProfile.Valid() || !i.QualifiedAssignmentID.Valid() || !i.Purpose.Valid() || i.AttemptFamily == "" || i.AttemptOrdinal == 0 || !i.ConditionDigest.Valid() || !i.OutputPredicateDigest.Valid() || !i.ActorFQN.Valid() || !i.Execution.Valid() || !i.ModelProfileDigest.Valid() || !i.RuntimeIdentityDigest.Valid() || i.WorkspaceID == "" || i.DeadlineAt.IsZero() || i.IdempotencyKey == "" || i.AdmissionPolicyRevision == 0 || !i.AdmissionPolicyDigest.Valid() || i.GlobalDebitOrdinal == 0 || i.PurposeDebitOrdinal == 0 || !i.LastEventID.Valid() || len(i.AllowedTerminalOutcomes) == 0 || !i.ToolPolicyDigest.Valid() || !i.EffectPolicyDigest.Valid() {
		return false
	}
	if i.RetryOfInvocationID == nil && i.RetryOrdinal != 0 || i.RetryOfInvocationID != nil && (!i.RetryOfInvocationID.Valid() || i.RetryOrdinal == 0) {
		return false
	}
	return true
}

type invocationAuthorizationPayload struct {
	InvocationID            UUIDv7                `json:"invocation_id"`
	TaskID                  UUIDv7                `json:"task_id"`
	BudgetAccountID         UUIDv7                `json:"budget_account_id"`
	ExpectedBudgetRevision  uint64                `json:"expected_budget_revision"`
	ExpectedTaskRevision    uint64                `json:"expected_task_revision"`
	LifecycleEpoch          uint64                `json:"lifecycle_epoch"`
	ScopeRevision           uint64                `json:"scope_revision"`
	ParentEventID           UUIDv7                `json:"parent_event_id"`
	WorkProfile             WorkProfileBinding    `json:"work_profile"`
	QualifiedAssignmentID   UUIDv7                `json:"qualified_assignment_id"`
	Purpose                 WorkPurpose           `json:"purpose"`
	AttemptFamily           string                `json:"attempt_family"`
	AttemptOrdinal          uint64                `json:"attempt_ordinal"`
	ConditionDigest         Digest                `json:"condition_digest"`
	RetryOfInvocationID     *UUIDv7               `json:"retry_of_invocation_id"`
	RetryOrdinal            uint64                `json:"retry_ordinal"`
	OutputPredicateDigest   Digest                `json:"output_predicate_digest"`
	AllowedTerminalOutcomes []WorkInvocationState `json:"allowed_terminal_outcomes"`
	ToolPolicyDigest        Digest                `json:"tool_policy_digest"`
	EffectPolicyDigest      Digest                `json:"effect_policy_digest"`
	ActorFQN                ActorFQN              `json:"actor_fqn"`
	ExecutionID             UUIDv7                `json:"execution_id"`
	FencingEpoch            uint64                `json:"fencing_epoch"`
	ModelProfileDigest      Digest                `json:"model_profile_digest"`
	RuntimeIdentityDigest   Digest                `json:"runtime_identity_digest"`
	WorkspaceID             string                `json:"workspace_id"`
	DeadlineAt              time.Time             `json:"deadline_at"`
	IdempotencyKey          string                `json:"idempotency_key"`
	AdmissionPolicyRevision uint64                `json:"admission_policy_revision"`
	AdmissionPolicyDigest   Digest                `json:"admission_policy_digest"`
	GlobalDebitOrdinal      uint64                `json:"global_debit_ordinal,omitempty"`
	PurposeDebitOrdinal     uint64                `json:"purpose_debit_ordinal,omitempty"`
	RemainingGlobalBudget   uint64                `json:"remaining_global_budget,omitempty"`
	RemainingPurposeBudget  uint64                `json:"remaining_purpose_budget,omitempty"`
}

func parseInvocationAuthorization(payload []byte) (invocationAuthorizationPayload, error) {
	var value invocationAuthorizationPayload
	if json.Unmarshal(payload, &value) != nil || !value.InvocationID.Valid() || !value.TaskID.Valid() || !value.BudgetAccountID.Valid() || value.ExpectedBudgetRevision == 0 || value.ExpectedTaskRevision == 0 || value.LifecycleEpoch == 0 || value.ScopeRevision == 0 || !value.ParentEventID.Valid() || !value.WorkProfile.Valid() || !value.QualifiedAssignmentID.Valid() || !value.Purpose.Valid() || value.AttemptFamily == "" || value.AttemptOrdinal == 0 || !value.ConditionDigest.Valid() || !value.OutputPredicateDigest.Valid() || !value.ActorFQN.Valid() || !value.ExecutionID.Valid() || value.FencingEpoch == 0 || !value.ModelProfileDigest.Valid() || !value.RuntimeIdentityDigest.Valid() || value.WorkspaceID == "" || value.DeadlineAt.IsZero() || value.IdempotencyKey == "" || value.AdmissionPolicyRevision == 0 || !value.AdmissionPolicyDigest.Valid() || len(value.AllowedTerminalOutcomes) == 0 || !value.ToolPolicyDigest.Valid() || !value.EffectPolicyDigest.Valid() {
		return invocationAuthorizationPayload{}, errors.New("invalid invocation authorization")
	}
	return value, nil
}

type WorkBudgetDebitDecision struct {
	Account                   AggregateRef
	Task                      AggregateRef
	Purpose                   WorkPurpose
	ExpectedAccountRevision   uint64
	ExpectedGlobalUsed        uint64
	ExpectedGlobalPurposeUsed uint64
	ExpectedTaskUsed          uint64
	ExpectedTaskPurposeUsed   uint64
	NextGlobalUsed            uint64
	NextGlobalPurposeUsed     uint64
	NextTaskUsed              uint64
	NextTaskPurposeUsed       uint64
}

func (d WorkBudgetDebitDecision) Valid() bool {
	return d.Account.Kind == AggregateWorkBudget && d.Account.Valid() && d.Task.Kind == AggregateTask && d.Task.Valid() && d.Purpose.Valid() && d.ExpectedAccountRevision > 0 && d.NextGlobalUsed == d.ExpectedGlobalUsed+1 && d.NextGlobalPurposeUsed == d.ExpectedGlobalPurposeUsed+1 && d.NextTaskUsed == d.ExpectedTaskUsed+1 && d.NextTaskPurposeUsed == d.ExpectedTaskPurposeUsed+1
}

type InvocationAdmissionDecision struct {
	Accepted     bool
	Reason       string
	Invocation   WorkInvocation
	BudgetDebit  *WorkBudgetDebitDecision
	EventPayload json.RawMessage
}

func PlanWorkInvocationAuthorization(payload []byte, task AggregateState, account WorkBudgetAccount, binding TaskWorkBudgetBinding, scope TaskOperationalScope, profile WorkProfileSnapshot, assignment QualifiedAssignmentAuthorization, currentExecution ExecutionTuple, invocations map[AggregateRef]WorkInvocation, now time.Time, eventID UUIDv7) InvocationAdmissionDecision {
	reject := func(reason string) InvocationAdmissionDecision { return InvocationAdmissionDecision{Reason: reason} }
	value, err := parseInvocationAuthorization(payload)
	if err != nil || task.Kind != AggregateTask || task.ID != value.TaskID {
		return reject("INVALID_INVOCATION")
	}
	if !account.Valid() || account.ID != value.BudgetAccountID || binding.BudgetAccountID != account.ID || binding.TaskID != task.ID {
		return reject("MISSING_BUDGET")
	}
	if value.ExpectedBudgetRevision != account.Revision {
		return reject("STALE_BUDGET_REVISION")
	}
	if value.ExpectedTaskRevision != task.Revision {
		return reject("STALE_TASK_REVISION")
	}
	if value.LifecycleEpoch != task.LifecycleEpoch || binding.LifecycleEpoch != task.LifecycleEpoch || scope.LifecycleEpoch != task.LifecycleEpoch {
		return reject("STALE_LIFECYCLE")
	}
	if value.ScopeRevision != task.ScopeRevision || binding.ScopeRevision != task.ScopeRevision || scope.ScopeRevision != task.ScopeRevision {
		return reject("STALE_SCOPE")
	}
	if task.Condition != ConditionRunnable || task.Phase != PhaseActive {
		return reject("TASK_NOT_RUNNABLE")
	}
	if !profile.Valid() || profile.Profile.Binding() != value.WorkProfile || assignment.AssignmentID != value.QualifiedAssignmentID || assignment.WorkProfile != value.WorkProfile || assignment.TaskID != task.ID {
		return reject("STALE_ASSIGNMENT")
	}
	execution := ExecutionTuple{ExecutionID: value.ExecutionID, FencingEpoch: value.FencingEpoch}
	if value.ActorFQN != assignment.SelectedActorFQN || execution != assignment.SelectedExecution() || execution != currentExecution || scope.OwnerFQN != value.ActorFQN || scope.Execution != execution || value.ModelProfileDigest != assignment.ModelProfileDigest || value.RuntimeIdentityDigest != assignment.RuntimeIdentityDigest || scope.WorkspaceID != value.WorkspaceID {
		return reject("RUNTIME_IDENTITY_MISMATCH")
	}
	if value.AdmissionPolicyRevision != account.PolicyRevision || value.AdmissionPolicyDigest != account.PolicyDigest {
		return reject("STALE_ADMISSION_POLICY")
	}
	if !now.Before(account.DeadlineAt) || !now.Before(value.DeadlineAt) || value.DeadlineAt.After(account.DeadlineAt) {
		return reject("DEADLINE_EXPIRED")
	}
	if account.ModelInvocationsUsed >= account.ModelInvocationLimit || binding.ModelInvocationsUsed >= binding.ModelInvocationLimit {
		return reject("BUDGET_EXHAUSTED")
	}
	if account.PurposeUsed[value.Purpose] >= account.PurposeLimits[value.Purpose] || binding.PurposeUsed[value.Purpose] >= binding.PurposeLimits[value.Purpose] {
		return reject("PURPOSE_BUDGET_EXHAUSTED")
	}
	var latest *WorkInvocation
	for _, candidate := range invocations {
		if candidate.TaskID != task.ID || candidate.Purpose != value.Purpose {
			continue
		}
		if !candidate.State.Terminal() {
			return reject("ACTIVE_INVOCATION_EXISTS")
		}
		candidateCopy := candidate.Clone()
		if latest == nil || candidateCopy.AttemptOrdinal > latest.AttemptOrdinal || candidateCopy.AttemptOrdinal == latest.AttemptOrdinal && candidateCopy.Revision > latest.Revision {
			latest = &candidateCopy
		}
	}
	if latest == nil {
		if value.AttemptOrdinal != 1 || value.RetryOfInvocationID != nil || value.RetryOrdinal != 0 {
			return reject("INVALID_RETRY")
		}
	} else {
		if value.ConditionDigest == latest.ConditionDigest {
			return reject("UNCHANGED_CONDITION")
		}
		if !validChangedConditionContinuation(value, profile.Profile, account, *latest) && !validCandidateRevalidation(value, *latest) {
			return reject("INVALID_RETRY")
		}
	}
	globalUsed := account.ModelInvocationsUsed + 1
	purposeUsed := account.PurposeUsed[value.Purpose] + 1
	value.GlobalDebitOrdinal = globalUsed
	value.PurposeDebitOrdinal = purposeUsed
	value.RemainingGlobalBudget = account.ModelInvocationLimit - globalUsed
	value.RemainingPurposeBudget = account.PurposeLimits[value.Purpose] - purposeUsed
	eventPayload, err := json.Marshal(value)
	if err != nil {
		return reject("INVALID_INVOCATION")
	}
	invocation := WorkInvocation{
		ID: value.InvocationID, Revision: 1, State: InvocationAuthorized,
		AuthorizationEventID: eventID, ParentEventID: value.ParentEventID,
		TaskID: value.TaskID, BudgetAccountID: value.BudgetAccountID,
		LifecycleEpoch: value.LifecycleEpoch, ScopeRevision: value.ScopeRevision,
		TaskRevision: value.ExpectedTaskRevision, WorkProfile: value.WorkProfile,
		QualifiedAssignmentID: value.QualifiedAssignmentID, Purpose: value.Purpose,
		AttemptFamily: value.AttemptFamily, AttemptOrdinal: value.AttemptOrdinal,
		ConditionDigest: value.ConditionDigest, RetryOfInvocationID: value.RetryOfInvocationID,
		RetryOrdinal: value.RetryOrdinal, OutputPredicateDigest: value.OutputPredicateDigest,
		AllowedTerminalOutcomes: append([]WorkInvocationState(nil), value.AllowedTerminalOutcomes...),
		ToolPolicyDigest:        value.ToolPolicyDigest, EffectPolicyDigest: value.EffectPolicyDigest,
		ActorFQN: value.ActorFQN, Execution: execution, ModelProfileDigest: value.ModelProfileDigest,
		RuntimeIdentityDigest: value.RuntimeIdentityDigest, WorkspaceID: value.WorkspaceID,
		DeadlineAt: value.DeadlineAt, IdempotencyKey: value.IdempotencyKey,
		AdmissionPolicyRevision: value.AdmissionPolicyRevision, AdmissionPolicyDigest: value.AdmissionPolicyDigest,
		GlobalDebitOrdinal: value.GlobalDebitOrdinal, PurposeDebitOrdinal: value.PurposeDebitOrdinal,
		RemainingGlobalBudget: value.RemainingGlobalBudget, RemainingPurposeBudget: value.RemainingPurposeBudget,
		LastEventID: eventID,
	}
	if !invocation.Valid() {
		return reject("INVALID_INVOCATION")
	}
	debit := WorkBudgetDebitDecision{
		Account: account.Ref(), Task: AggregateRef{Kind: AggregateTask, ID: task.ID}, Purpose: value.Purpose,
		ExpectedAccountRevision: account.Revision, ExpectedGlobalUsed: account.ModelInvocationsUsed,
		ExpectedGlobalPurposeUsed: account.PurposeUsed[value.Purpose], ExpectedTaskUsed: binding.ModelInvocationsUsed,
		ExpectedTaskPurposeUsed: binding.PurposeUsed[value.Purpose], NextGlobalUsed: globalUsed,
		NextGlobalPurposeUsed: purposeUsed, NextTaskUsed: binding.ModelInvocationsUsed + 1,
		NextTaskPurposeUsed: binding.PurposeUsed[value.Purpose] + 1,
	}
	return InvocationAdmissionDecision{Accepted: true, Reason: "ACCEPTED", Invocation: invocation, BudgetDebit: &debit, EventPayload: eventPayload}
}

func validChangedConditionContinuation(value invocationAuthorizationPayload, profile WorkRiskProfile, account WorkBudgetAccount, prior WorkInvocation) bool {
	if value.RetryOfInvocationID == nil || value.RetryOrdinal == 0 || profile.SupersedesProfileID == nil {
		return false
	}
	if !prior.Valid() || *value.RetryOfInvocationID != prior.ID || prior.TaskID != value.TaskID || prior.Purpose != value.Purpose || value.AttemptOrdinal != prior.AttemptOrdinal+1 || value.RetryOrdinal != prior.RetryOrdinal+1 || value.ConditionDigest == prior.ConditionDigest || profile.ProfileRevision <= prior.WorkProfile.ProfileRevision || value.WorkProfile != profile.Binding() || !value.DeadlineAt.After(prior.DeadlineAt) || !profile.Budgets.DeadlineAt.Equal(value.DeadlineAt) || account.PolicyRevision <= prior.AdmissionPolicyRevision || len(prior.TerminalEvidenceIDs) == 0 {
		return false
	}
	recoverable := prior.State == InvocationTimedOut ||
		prior.State == InvocationCancelled && prior.CancellationRequestedAt != nil ||
		prior.State == InvocationFailed || prior.State == InvocationStartFailed
	if !recoverable {
		return false
	}
	evidence := make(map[UUIDv7]struct{}, len(profile.ClassificationEvidenceIDs))
	for _, id := range profile.ClassificationEvidenceIDs {
		evidence[id] = struct{}{}
	}
	for _, id := range prior.TerminalEvidenceIDs {
		if _, ok := evidence[id]; !ok {
			return false
		}
	}
	return true
}

func validCandidateRevalidation(value invocationAuthorizationPayload, prior WorkInvocation) bool {
	// HANDOFF covers a configured workflow's structured planning stages.
	// Promotion is included because the operational layer admits it only after
	// verifying the exact invalid-structured-output block; a recorded product
	// decision never reaches this path, and the new attempt must still return a
	// passing structured acceptance.
	if value.Purpose != PurposeHandoff && value.Purpose != PurposeValidation && value.Purpose != PurposeReview && value.Purpose != PurposeRepair && value.Purpose != PurposeReplan && value.Purpose != PurposePromotion {
		return false
	}
	return value.RetryOfInvocationID != nil && *value.RetryOfInvocationID == prior.ID && value.RetryOrdinal == prior.RetryOrdinal+1 && value.AttemptOrdinal == prior.AttemptOrdinal+1 && prior.State == InvocationSucceeded && value.ConditionDigest != prior.ConditionDigest
}

func WorkInvocationFromAuthorizedEvent(event DomainEvent) (WorkInvocation, error) {
	value, err := parseInvocationAuthorization(event.Payload)
	if err != nil || value.GlobalDebitOrdinal == 0 || value.PurposeDebitOrdinal == 0 {
		return WorkInvocation{}, errors.New("invalid authorization event")
	}
	invocation := WorkInvocation{
		ID: value.InvocationID, Revision: event.AggregateRevision, State: InvocationAuthorized,
		AuthorizationEventID: event.EventID, ParentEventID: value.ParentEventID,
		TaskID: value.TaskID, BudgetAccountID: value.BudgetAccountID, LifecycleEpoch: value.LifecycleEpoch,
		ScopeRevision: value.ScopeRevision, TaskRevision: value.ExpectedTaskRevision,
		WorkProfile: value.WorkProfile, QualifiedAssignmentID: value.QualifiedAssignmentID,
		Purpose: value.Purpose, AttemptFamily: value.AttemptFamily, AttemptOrdinal: value.AttemptOrdinal,
		ConditionDigest: value.ConditionDigest, RetryOfInvocationID: value.RetryOfInvocationID,
		RetryOrdinal: value.RetryOrdinal, OutputPredicateDigest: value.OutputPredicateDigest,
		AllowedTerminalOutcomes: append([]WorkInvocationState(nil), value.AllowedTerminalOutcomes...),
		ToolPolicyDigest:        value.ToolPolicyDigest, EffectPolicyDigest: value.EffectPolicyDigest,
		ActorFQN: value.ActorFQN, Execution: ExecutionTuple{ExecutionID: value.ExecutionID, FencingEpoch: value.FencingEpoch},
		ModelProfileDigest: value.ModelProfileDigest, RuntimeIdentityDigest: value.RuntimeIdentityDigest,
		WorkspaceID: value.WorkspaceID, DeadlineAt: value.DeadlineAt, IdempotencyKey: value.IdempotencyKey,
		AdmissionPolicyRevision: value.AdmissionPolicyRevision, AdmissionPolicyDigest: value.AdmissionPolicyDigest,
		GlobalDebitOrdinal: value.GlobalDebitOrdinal, PurposeDebitOrdinal: value.PurposeDebitOrdinal,
		RemainingGlobalBudget: value.RemainingGlobalBudget, RemainingPurposeBudget: value.RemainingPurposeBudget,
		LastEventID: event.EventID,
	}
	if !invocation.Valid() || invocation.ID != event.Aggregate.ID {
		return WorkInvocation{}, errors.New("invalid authorization event")
	}
	return invocation, nil
}

func ApplyWorkInvocationEvent(current WorkInvocation, event DomainEvent) (WorkInvocation, bool) {
	if event.Aggregate != current.Ref() || event.AggregateRevision != current.Revision+1 || current.State.Terminal() {
		return current, false
	}
	next := current.Clone()
	switch event.EventType {
	case "tekroo.event.work-invocation.claimed":
		var value struct {
			InvocationID          UUIDv7    `json:"invocation_id"`
			ExpectedRevision      uint64    `json:"expected_invocation_revision"`
			ClaimID               UUIDv7    `json:"claim_id"`
			ActorFQN              ActorFQN  `json:"actor_fqn"`
			ExecutionID           UUIDv7    `json:"execution_id"`
			FencingEpoch          uint64    `json:"fencing_epoch"`
			ModelProfileDigest    Digest    `json:"model_profile_digest"`
			RuntimeIdentityDigest Digest    `json:"runtime_identity_digest"`
			ClaimedAt             time.Time `json:"claimed_at"`
		}
		if json.Unmarshal(event.Payload, &value) != nil || current.State != InvocationAuthorized || value.InvocationID != current.ID || value.ExpectedRevision != current.Revision || !value.ClaimID.Valid() || value.ActorFQN != current.ActorFQN || (ExecutionTuple{ExecutionID: value.ExecutionID, FencingEpoch: value.FencingEpoch}) != current.Execution || value.ModelProfileDigest != current.ModelProfileDigest || value.RuntimeIdentityDigest != current.RuntimeIdentityDigest || value.ClaimedAt.IsZero() || !value.ClaimedAt.Before(current.DeadlineAt) {
			return current, false
		}
		next.State = InvocationClaimed
		next.ClaimID = &value.ClaimID
		next.ClaimedAt = &value.ClaimedAt
	case "tekroo.event.work-invocation.started":
		var value struct {
			InvocationID     UUIDv7    `json:"invocation_id"`
			ExpectedRevision uint64    `json:"expected_invocation_revision"`
			ClaimID          UUIDv7    `json:"claim_id"`
			ConversationID   string    `json:"conversation_id"`
			RequestDigest    Digest    `json:"request_digest"`
			StartedAt        time.Time `json:"started_at"`
		}
		if json.Unmarshal(event.Payload, &value) != nil || current.State != InvocationClaimed || value.InvocationID != current.ID || value.ExpectedRevision != current.Revision || current.ClaimID == nil || value.ClaimID != *current.ClaimID || value.ConversationID == "" || !value.RequestDigest.Valid() || value.StartedAt.IsZero() || !value.StartedAt.Before(current.DeadlineAt) {
			return current, false
		}
		next.State = InvocationStarted
		next.ConversationID = &value.ConversationID
		next.RequestDigest = &value.RequestDigest
		next.StartedAt = &value.StartedAt
	case "tekroo.event.work-invocation.terminal-recorded":
		var value struct {
			InvocationID     UUIDv7              `json:"invocation_id"`
			ExpectedRevision uint64              `json:"expected_invocation_revision"`
			ClaimID          UUIDv7              `json:"claim_id"`
			Outcome          WorkInvocationState `json:"outcome"`
			Retryable        bool                `json:"retryable"`
			EvidenceIDs      []UUIDv7            `json:"evidence_ids"`
			OutputDigest     Digest              `json:"output_digest"`
			FinishedAt       time.Time           `json:"finished_at"`
		}
		if json.Unmarshal(event.Payload, &value) != nil || (current.State != InvocationStarted && !(current.State == InvocationClaimed && value.Outcome == InvocationStartFailed)) || value.InvocationID != current.ID || value.ExpectedRevision != current.Revision || current.ClaimID == nil || value.ClaimID != *current.ClaimID || !containsInvocationOutcome(current.AllowedTerminalOutcomes, value.Outcome) || len(value.EvidenceIDs) == 0 || !validUniqueUUIDs(value.EvidenceIDs, 1, 64) || !value.OutputDigest.Valid() || value.FinishedAt.IsZero() {
			return current, false
		}
		next.State = value.Outcome
		next.TerminalOutcome = &value.Outcome
		next.Retryable = &value.Retryable
		next.TerminalEvidenceIDs = append([]UUIDv7(nil), value.EvidenceIDs...)
		next.OutputDigest = &value.OutputDigest
		next.FinishedAt = &value.FinishedAt
	case "tekroo.event.work-invocation.cancellation-requested":
		var value struct {
			InvocationID     UUIDv7    `json:"invocation_id"`
			ExpectedRevision uint64    `json:"expected_invocation_revision"`
			RequestedAt      time.Time `json:"requested_at"`
		}
		if json.Unmarshal(event.Payload, &value) != nil || current.State != InvocationStarted || current.CancellationRequestedAt != nil || value.InvocationID != current.ID || value.ExpectedRevision != current.Revision || value.RequestedAt.IsZero() {
			return current, false
		}
		next.CancellationRequestedAt = &value.RequestedAt
	case "tekroo.event.work-invocation.expired":
		var value struct {
			InvocationID     UUIDv7    `json:"invocation_id"`
			ExpectedRevision uint64    `json:"expected_invocation_revision"`
			DeadlineAt       time.Time `json:"deadline_at"`
			ObservedAt       time.Time `json:"observed_at"`
		}
		if json.Unmarshal(event.Payload, &value) != nil || current.State != InvocationAuthorized || value.InvocationID != current.ID || value.ExpectedRevision != current.Revision || !value.DeadlineAt.Equal(current.DeadlineAt) || value.ObservedAt.Before(current.DeadlineAt) {
			return current, false
		}
		next.State = InvocationExpired
		next.FinishedAt = &value.ObservedAt
	default:
		return current, false
	}
	next.Revision = event.AggregateRevision
	next.LastEventID = event.EventID
	return next, next.Valid()
}

func containsInvocationOutcome(values []WorkInvocationState, target WorkInvocationState) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

type InvocationAdmissionScenarioBudget struct {
	AccountID    string    `json:"accountId"`
	DeadlineAt   time.Time `json:"deadlineAt"`
	Limit        uint64    `json:"limit"`
	Used         uint64    `json:"used"`
	PurposeLimit uint64    `json:"purposeLimit"`
	PurposeUsed  uint64    `json:"purposeUsed"`
}

type InvocationAdmissionScenarioExact struct {
	ActorFQN       string `json:"actorFqn"`
	ExecutionID    string `json:"executionId"`
	FencingEpoch   uint64 `json:"fencingEpoch"`
	LifecycleEpoch uint64 `json:"lifecycleEpoch"`
	ScopeRevision  uint64 `json:"scopeRevision"`
	TaskRevision   uint64 `json:"taskRevision"`
	ModelDigest    string `json:"modelDigest"`
	RuntimeDigest  string `json:"runtimeDigest"`
}

type InvocationAdmissionScenarioTerminal struct {
	InvocationID string `json:"invocationId"`
	RetryOrdinal uint64 `json:"retryOrdinal"`
	Retryable    bool   `json:"retryable"`
}

type InvocationAdmissionScenarioGiven struct {
	Budget              *InvocationAdmissionScenarioBudget   `json:"budget"`
	Consumed            bool                                 `json:"consumed"`
	Exact               InvocationAdmissionScenarioExact     `json:"exact"`
	LastConditionDigest *string                              `json:"lastConditionDigest"`
	PriorTerminal       *InvocationAdmissionScenarioTerminal `json:"priorTerminal"`
	State               string                               `json:"state"`
}

type InvocationAdmissionScenarioAction struct {
	Action           string  `json:"action"`
	ActorFQN         string  `json:"actorFqn"`
	ExecutionID      string  `json:"executionId"`
	FencingEpoch     uint64  `json:"fencingEpoch"`
	LifecycleEpoch   uint64  `json:"lifecycleEpoch"`
	ScopeRevision    uint64  `json:"scopeRevision"`
	TaskRevision     uint64  `json:"taskRevision"`
	ModelDigest      string  `json:"modelDigest"`
	RuntimeDigest    string  `json:"runtimeDigest"`
	ConditionDigest  string  `json:"conditionDigest"`
	Origin           string  `json:"origin"`
	RetryOf          *string `json:"retryOf"`
	RetryOrdinal     uint64  `json:"retryOrdinal"`
	BeforeDeadline   bool    `json:"beforeDeadline"`
	Consumed         bool    `json:"consumed"`
	IdentityMatches  bool    `json:"identityMatches"`
	Outcome          string  `json:"outcome"`
	TerminalEvidence bool    `json:"terminalEvidence"`
}

type InvocationAdmissionScenarioResult struct {
	Accepted                     bool    `json:"accepted"`
	Reason                       string  `json:"reason"`
	State                        string  `json:"state,omitempty"`
	GlobalUsed                   *uint64 `json:"globalUsed,omitempty"`
	PurposeUsed                  *uint64 `json:"purposeUsed,omitempty"`
	MaximumAdditionalInvocations *uint64 `json:"maximumAdditionalInvocations,omitempty"`
	CancellationRequested        *bool   `json:"cancellationRequested,omitempty"`
}

func EvaluateInvocationAdmissionScenario(given InvocationAdmissionScenarioGiven, action InvocationAdmissionScenarioAction) InvocationAdmissionScenarioResult {
	result := InvocationAdmissionScenarioResult{Reason: "INVALID_ACTION"}
	switch action.Action {
	case "CLAIM":
		result.State = given.State
		if given.State != string(InvocationAuthorized) || given.Consumed || action.Consumed {
			result.Reason = "PERMIT_ALREADY_CONSUMED"
			return result
		}
		if !action.BeforeDeadline || !action.IdentityMatches {
			result.Reason = "RUNTIME_IDENTITY_MISMATCH"
			return result
		}
		result.Accepted, result.Reason, result.State = true, "ACCEPTED", string(InvocationClaimed)
		return result
	case "REQUEST_CANCELLATION":
		result.State = given.State
		if given.State != string(InvocationStarted) {
			result.Reason = "INVALID_INVOCATION_TRANSITION"
			return result
		}
		requested := true
		result.Accepted, result.Reason, result.CancellationRequested = true, "CANCELLATION_REQUESTED", &requested
		return result
	case "RECORD_TERMINAL":
		result.State = given.State
		if action.Outcome == string(InvocationCancelled) && !action.TerminalEvidence {
			result.Reason = "TERMINAL_EVIDENCE_REQUIRED"
			return result
		}
		result.Accepted, result.Reason, result.State = true, "ACCEPTED", action.Outcome
		return result
	case "AUTHORIZE":
		if given.Budget == nil {
			result.Reason = "MISSING_BUDGET"
			return result
		}
		if action.Origin != "TEAMS_KERNEL" {
			result.Reason = "DIRECT_WAKEUP_PROHIBITED"
			return result
		}
		if action.TaskRevision != given.Exact.TaskRevision {
			result.Reason = "STALE_TASK_REVISION"
			return result
		}
		if action.LifecycleEpoch != given.Exact.LifecycleEpoch {
			result.Reason = "STALE_LIFECYCLE"
			return result
		}
		if action.ScopeRevision != given.Exact.ScopeRevision || action.ActorFQN != given.Exact.ActorFQN || action.ExecutionID != given.Exact.ExecutionID || action.FencingEpoch != given.Exact.FencingEpoch || action.ModelDigest != given.Exact.ModelDigest || action.RuntimeDigest != given.Exact.RuntimeDigest {
			result.Reason = "RUNTIME_IDENTITY_MISMATCH"
			return result
		}
		if given.Budget.Used >= given.Budget.Limit || given.Budget.PurposeUsed >= given.Budget.PurposeLimit {
			result.Reason = "BUDGET_EXHAUSTED"
			return result
		}
		if given.LastConditionDigest != nil && *given.LastConditionDigest == action.ConditionDigest {
			if given.PriorTerminal == nil || !given.PriorTerminal.Retryable || action.RetryOf == nil || *action.RetryOf != given.PriorTerminal.InvocationID || action.RetryOrdinal != given.PriorTerminal.RetryOrdinal+1 {
				result.Reason = "UNCHANGED_CONDITION"
				return result
			}
		}
		globalUsed, purposeUsed := given.Budget.Used+1, given.Budget.PurposeUsed+1
		remaining := given.Budget.Limit - globalUsed
		result.Accepted, result.Reason = true, "ACCEPTED"
		result.GlobalUsed, result.PurposeUsed, result.MaximumAdditionalInvocations = &globalUsed, &purposeUsed, &remaining
		return result
	default:
		return result
	}
}

type WorkBudgetScenarioGiven struct {
	Limit         uint64 `json:"limit"`
	Used          uint64 `json:"used"`
	RootAccountID string `json:"rootAccountId"`
	Revision      uint64 `json:"revision"`
}

type WorkBudgetScenarioAction struct {
	Action           string `json:"action"`
	AccountID        string `json:"accountId"`
	RequestedLimit   uint64 `json:"requestedLimit"`
	Authorized       bool   `json:"authorized"`
	ExpectedRevision uint64 `json:"expectedRevision"`
	NewLimit         uint64 `json:"newLimit"`
}

type WorkBudgetScenarioResult struct {
	Accepted                     bool    `json:"accepted"`
	Reason                       string  `json:"reason"`
	MaximumAdditionalInvocations uint64  `json:"maximumAdditionalInvocations"`
	Revision                     *uint64 `json:"revision,omitempty"`
}

func EvaluateWorkBudgetScenario(given WorkBudgetScenarioGiven, action WorkBudgetScenarioAction) WorkBudgetScenarioResult {
	result := WorkBudgetScenarioResult{MaximumAdditionalInvocations: given.Limit - given.Used}
	switch action.Action {
	case "COMPUTE_BOUND":
		result.Accepted, result.Reason = true, "ACCEPTED"
	case "INHERIT":
		if action.AccountID != given.RootAccountID {
			result.Reason = "BUDGET_RESET_PROHIBITED"
		} else if action.RequestedLimit > given.Limit {
			result.Reason = "CAPACITY_MINT_PROHIBITED"
		} else {
			result.Accepted, result.Reason = true, "ACCEPTED"
		}
	case "AMEND":
		if !action.Authorized || action.ExpectedRevision != given.Revision || action.NewLimit < given.Used {
			result.Reason = "INVALID_BUDGET_AMENDMENT"
		} else {
			revision := given.Revision + 1
			result.Accepted, result.Reason, result.Revision = true, "ACCEPTED", &revision
			result.MaximumAdditionalInvocations = action.NewLimit - given.Used
		}
	default:
		result.Reason = "INVALID_ACTION"
	}
	return result
}
