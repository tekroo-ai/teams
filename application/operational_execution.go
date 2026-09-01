package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

var (
	ErrInvalidOperationalExecution = errors.New("invalid operational execution")
	ErrStaleWorkInvocation         = errors.New("work invocation authority is stale")
	ErrExternalOutcomeUnknown      = errors.New("external execution outcome is unknown")
	ErrExecutionStillRunning       = errors.New("external execution is still running")
)

// TaskExecutionSpecification is reconstructed from the authoritative
// task.created event, never from a task projection or an agent transcript.
type TaskExecutionSpecification struct {
	TaskID             kernel.UUIDv7   `json:"task_id"`
	CreatedEventID     kernel.UUIDv7   `json:"created_event_id"`
	SourceDigest       kernel.Digest   `json:"source_digest"`
	StoryID            kernel.UUIDv7   `json:"story_id"`
	Title              string          `json:"title"`
	Description        string          `json:"description"`
	AcceptanceCriteria []string        `json:"acceptance_criteria"`
	DependsOn          []kernel.UUIDv7 `json:"depends_on"`
}

func TaskExecutionSpecificationFromEvent(event kernel.DomainEvent) (TaskExecutionSpecification, error) {
	if event.EventType != "tekroo.event.task.created" || event.Aggregate.Kind != kernel.AggregateTask || !event.EventID.Valid() {
		return TaskExecutionSpecification{}, ErrInvalidOperationalExecution
	}
	var payload struct {
		StoryID            kernel.UUIDv7   `json:"story_id"`
		Title              string          `json:"title"`
		Description        string          `json:"description"`
		AcceptanceCriteria []string        `json:"acceptance_criteria"`
		DependsOn          []kernel.UUIDv7 `json:"depends_on"`
	}
	if json.Unmarshal(event.Payload, &payload) != nil {
		return TaskExecutionSpecification{}, ErrInvalidOperationalExecution
	}
	digest := sha256.Sum256(event.Payload)
	specification := TaskExecutionSpecification{
		TaskID: event.Aggregate.ID, CreatedEventID: event.EventID,
		SourceDigest: kernel.Digest(hex.EncodeToString(digest[:])), StoryID: payload.StoryID,
		Title: payload.Title, Description: payload.Description,
		AcceptanceCriteria: append([]string(nil), payload.AcceptanceCriteria...),
		DependsOn:          append([]kernel.UUIDv7(nil), payload.DependsOn...),
	}
	if !specification.Valid() {
		return TaskExecutionSpecification{}, ErrInvalidOperationalExecution
	}
	return specification, nil
}

func (s TaskExecutionSpecification) Valid() bool {
	if !s.TaskID.Valid() || !s.CreatedEventID.Valid() || !s.SourceDigest.Valid() || !s.StoryID.Valid() || s.Title == "" || len(s.Title) > 256 || len(s.Description) > 65536 || len(s.AcceptanceCriteria) == 0 || len(s.AcceptanceCriteria) > 64 || len(s.DependsOn) > 64 {
		return false
	}
	criteria := make(map[string]struct{}, len(s.AcceptanceCriteria))
	for _, value := range s.AcceptanceCriteria {
		if value == "" || len(value) > 4096 {
			return false
		}
		if _, duplicate := criteria[value]; duplicate {
			return false
		}
		criteria[value] = struct{}{}
	}
	dependencies := make(map[kernel.UUIDv7]struct{}, len(s.DependsOn))
	for _, value := range s.DependsOn {
		if !value.Valid() {
			return false
		}
		if _, duplicate := dependencies[value]; duplicate {
			return false
		}
		dependencies[value] = struct{}{}
	}
	return true
}

type OperationalExecutionContext struct {
	Invocation             kernel.WorkInvocation
	Task                   kernel.AggregateState
	Budget                 kernel.WorkBudgetAccount
	TaskBudget             kernel.TaskWorkBudgetBinding
	Scope                  kernel.TaskOperationalScope
	Profile                kernel.WorkProfileSnapshot
	Assignment             kernel.QualifiedAssignmentAuthorization
	CurrentExecution       kernel.ExecutionTuple
	Specification          TaskExecutionSpecification
	Evidence               []kernel.EvidenceRef
	AuthorizationEventSeen bool
	ParentEventSeen        bool
}

type OperationalExecutionReader interface {
	LoadOperationalExecution(context.Context, kernel.UUIDv7) (OperationalExecutionContext, error)
	LoadOperationalExecutionByAuthorizationEvent(context.Context, kernel.UUIDv7) (OperationalExecutionContext, error)
}

func (c OperationalExecutionContext) Validate(now time.Time) error {
	invocation := c.Invocation
	if !invocation.Valid() || c.Task.Kind != kernel.AggregateTask || c.Task.ID != invocation.TaskID || c.Task.Revision != invocation.TaskRevision || c.Task.LifecycleEpoch != invocation.LifecycleEpoch || c.Task.ScopeRevision != invocation.ScopeRevision || c.Task.Phase != kernel.PhaseActive || c.Task.Condition != kernel.ConditionRunnable || !c.AuthorizationEventSeen || !c.ParentEventSeen {
		return ErrStaleWorkInvocation
	}
	if !c.Budget.Valid() || c.Budget.ID != invocation.BudgetAccountID || c.Budget.PolicyRevision != invocation.AdmissionPolicyRevision || c.Budget.PolicyDigest != invocation.AdmissionPolicyDigest || c.Budget.ModelInvocationsUsed < invocation.GlobalDebitOrdinal || c.Budget.PurposeUsed[invocation.Purpose] < invocation.PurposeDebitOrdinal || !now.Before(c.Budget.DeadlineAt) || !now.Before(invocation.DeadlineAt) {
		return ErrStaleWorkInvocation
	}
	if !c.TaskBudget.Valid() || c.TaskBudget.TaskID != invocation.TaskID || c.TaskBudget.BudgetAccountID != invocation.BudgetAccountID || c.TaskBudget.LifecycleEpoch != invocation.LifecycleEpoch || c.TaskBudget.ScopeRevision != invocation.ScopeRevision || c.TaskBudget.ModelInvocationsUsed == 0 || c.TaskBudget.PurposeUsed[invocation.Purpose] == 0 {
		return ErrStaleWorkInvocation
	}
	if !c.Scope.Valid() || c.Scope.TaskID != invocation.TaskID || c.Scope.LifecycleEpoch != invocation.LifecycleEpoch || c.Scope.ScopeRevision != invocation.ScopeRevision || c.Scope.OwnerFQN != invocation.ActorFQN || c.Scope.Execution != invocation.Execution || c.Scope.WorkspaceID != invocation.WorkspaceID {
		return ErrStaleWorkInvocation
	}
	if !c.Profile.Valid() || c.Profile.Profile.Binding() != invocation.WorkProfile || c.Profile.Profile.TaskID != invocation.TaskID || !c.Assignment.Valid() || c.Assignment.AssignmentID != invocation.QualifiedAssignmentID || c.Assignment.TaskID != invocation.TaskID || c.Assignment.WorkProfile != invocation.WorkProfile || c.Assignment.SelectedActorFQN != invocation.ActorFQN || c.Assignment.SelectedExecution() != invocation.Execution || c.Assignment.ModelProfileDigest != invocation.ModelProfileDigest || c.Assignment.RuntimeIdentityDigest != invocation.RuntimeIdentityDigest || c.CurrentExecution != invocation.Execution {
		return ErrStaleWorkInvocation
	}
	if !c.Specification.Valid() || c.Specification.TaskID != invocation.TaskID {
		return ErrStaleWorkInvocation
	}
	required := make(map[kernel.UUIDv7]struct{})
	for _, evidenceID := range c.Scope.InterfaceEvidenceIDs {
		required[evidenceID] = struct{}{}
	}
	for _, evidenceID := range c.Profile.Profile.ClassificationEvidenceIDs {
		required[evidenceID] = struct{}{}
	}
	for _, evidenceID := range c.Assignment.EvidenceIDs {
		required[evidenceID] = struct{}{}
	}
	observed := make(map[kernel.UUIDv7]struct{}, len(c.Evidence))
	for _, evidence := range c.Evidence {
		if !evidence.EvidenceID.Valid() || !evidence.SHA256.Valid() {
			return ErrStaleWorkInvocation
		}
		observed[evidence.EvidenceID] = struct{}{}
	}
	for evidenceID := range required {
		if _, found := observed[evidenceID]; !found {
			return ErrStaleWorkInvocation
		}
	}
	return nil
}

type ExecutionBrief struct {
	ContractManifest       string                      `json:"contract_manifest"`
	InvocationID           kernel.UUIDv7               `json:"invocation_id"`
	AuthorizationEventID   kernel.UUIDv7               `json:"authorization_event_id"`
	ParentEventID          kernel.UUIDv7               `json:"parent_event_id"`
	Task                   TaskExecutionSpecification  `json:"task"`
	TaskRevision           uint64                      `json:"task_revision"`
	LifecycleEpoch         uint64                      `json:"lifecycle_epoch"`
	ScopeRevision          uint64                      `json:"scope_revision"`
	Purpose                kernel.WorkPurpose          `json:"purpose"`
	AttemptFamily          string                      `json:"attempt_family"`
	AttemptOrdinal         uint64                      `json:"attempt_ordinal"`
	RetryOrdinal           uint64                      `json:"retry_ordinal"`
	ConditionDigest        kernel.Digest               `json:"condition_digest"`
	OutputPredicateDigest  kernel.Digest               `json:"output_predicate_digest"`
	ToolPolicyDigest       kernel.Digest               `json:"tool_policy_digest"`
	EffectPolicyDigest     kernel.Digest               `json:"effect_policy_digest"`
	WorkProfile            kernel.WorkRiskProfile      `json:"work_profile"`
	AssignmentID           kernel.UUIDv7               `json:"assignment_id"`
	DecisionRoute          kernel.DecisionRoute        `json:"decision_route"`
	ActorFQN               kernel.ActorFQN             `json:"actor_fqn"`
	Execution              kernel.ExecutionTuple       `json:"execution"`
	ModelProfileDigest     kernel.Digest               `json:"model_profile_digest"`
	RuntimeIdentityDigest  kernel.Digest               `json:"runtime_identity_digest"`
	Scope                  kernel.TaskOperationalScope `json:"scope"`
	Evidence               []kernel.EvidenceRef        `json:"evidence"`
	RemainingGlobalBudget  uint64                      `json:"remaining_global_budget"`
	RemainingPurposeBudget uint64                      `json:"remaining_purpose_budget"`
	DeadlineAt             time.Time                   `json:"deadline_at"`
	CoordinationRule       string                      `json:"coordination_rule"`
	ResultProtocol         *ExecutionResultProtocol    `json:"result_protocol,omitempty"`
	SemanticContext        SemanticContextRequest      `json:"semantic_context"`
}

type ExecutionResultProtocol struct {
	SchemaVersion string   `json:"schema_version"`
	Marker        string   `json:"marker"`
	Outcomes      []string `json:"outcomes"`
	Instruction   string   `json:"instruction"`
}

const ValidationResultMarker = "TEKROO_VALIDATION_RESULT:"
const OrganizationalResultMarker = "TEKROO_ORGANIZATIONAL_RESULT:"

const evidenceOnlyCoordinationRule = "RETURN_EVIDENCE_AND_PROPOSALS_TO_TEAMS_ONLY;DO_NOT_ADDRESS_OR_INVOKE_ANOTHER_AGENT"

const (
	SemanticContextLabel      = "NON_AUTHORITATIVE_SEMANTIC_MEMORY_CONTEXT"
	SemanticContextUse        = "ORIENTATION_AND_EVIDENCE_ONLY"
	SemanticContextPrecedence = "CURRENT_TEAMS_STATE_AND_CURRENT_TASK_EVIDENCE_ALWAYS_PREVAIL"
)

var semanticContextForbiddenEffects = []string{
	"ACCEPTANCE",
	"ASSIGNMENT",
	"BUDGET",
	"COMPLETION",
	"INVOCATION_AUTHORITY",
	"LEASE",
	"OWNERSHIP",
	"RELEASE",
	"REVIEW",
	"ROUTING",
	"STORY_STATE",
	"TASK_STATE",
}

// SemanticContextRequest is read-only metadata supplied to the qualified
// OpenHands prompt hook. It helps SMA select useful memories but grants no
// authority: Teams does not parse recalled context into commands or state.
type SemanticContextRequest struct {
	Label                    string                    `json:"label"`
	AllowedUse               string                    `json:"allowed_use"`
	CurrentRealityRule       string                    `json:"current_reality_rule"`
	NonAuthoritative         bool                      `json:"non_authoritative"`
	NoTeamsAuthorityFallback bool                      `json:"no_teams_authority_fallback"`
	InvocationID             kernel.UUIDv7             `json:"invocation_id"`
	AuthorizationEventID     kernel.UUIDv7             `json:"authorization_event_id"`
	ParentEventID            kernel.UUIDv7             `json:"parent_event_id"`
	TaskID                   kernel.UUIDv7             `json:"task_id"`
	StoryID                  kernel.UUIDv7             `json:"story_id"`
	TaskCreatedEventID       kernel.UUIDv7             `json:"task_created_event_id"`
	TaskCreatedSourceDigest  kernel.Digest             `json:"task_created_source_digest"`
	TaskRevision             uint64                    `json:"task_revision"`
	LifecycleEpoch           uint64                    `json:"lifecycle_epoch"`
	ScopeRevision            uint64                    `json:"scope_revision"`
	OperationalScopeEventID  kernel.UUIDv7             `json:"operational_scope_event_id"`
	AssignmentID             kernel.UUIDv7             `json:"assignment_id"`
	ActorFQN                 kernel.ActorFQN           `json:"actor_fqn"`
	Execution                kernel.ExecutionTuple     `json:"execution"`
	WorkspaceID              string                    `json:"workspace_id"`
	WorktreeID               string                    `json:"worktree_id"`
	BaselineSHA              string                    `json:"baseline_sha"`
	WorkProfile              kernel.WorkProfileBinding `json:"work_profile"`
	ModelProfileDigest       kernel.Digest             `json:"model_profile_digest"`
	RuntimeIdentityDigest    kernel.Digest             `json:"runtime_identity_digest"`
	ToolPolicyDigest         kernel.Digest             `json:"tool_policy_digest"`
	EffectPolicyDigest       kernel.Digest             `json:"effect_policy_digest"`
	Evidence                 []kernel.EvidenceRef      `json:"evidence"`
	ForbiddenEffects         []string                  `json:"forbidden_effects"`
}

func buildSemanticContextRequest(current OperationalExecutionContext, evidence []kernel.EvidenceRef) SemanticContextRequest {
	invocation := current.Invocation
	return SemanticContextRequest{
		Label: SemanticContextLabel, AllowedUse: SemanticContextUse,
		CurrentRealityRule: SemanticContextPrecedence, NonAuthoritative: true,
		NoTeamsAuthorityFallback: true, InvocationID: invocation.ID,
		AuthorizationEventID: invocation.AuthorizationEventID, ParentEventID: invocation.ParentEventID,
		TaskID: invocation.TaskID, StoryID: current.Specification.StoryID,
		TaskCreatedEventID:      current.Specification.CreatedEventID,
		TaskCreatedSourceDigest: current.Specification.SourceDigest,
		TaskRevision:            invocation.TaskRevision, LifecycleEpoch: invocation.LifecycleEpoch,
		ScopeRevision: invocation.ScopeRevision, OperationalScopeEventID: current.Scope.BoundEventID,
		AssignmentID: invocation.QualifiedAssignmentID, ActorFQN: invocation.ActorFQN,
		Execution: invocation.Execution, WorkspaceID: current.Scope.WorkspaceID,
		WorktreeID: current.Scope.WorktreeID, BaselineSHA: current.Scope.BaselineSHA,
		WorkProfile: invocation.WorkProfile, ModelProfileDigest: invocation.ModelProfileDigest,
		RuntimeIdentityDigest: invocation.RuntimeIdentityDigest,
		ToolPolicyDigest:      invocation.ToolPolicyDigest, EffectPolicyDigest: invocation.EffectPolicyDigest,
		Evidence:         append([]kernel.EvidenceRef(nil), evidence...),
		ForbiddenEffects: append([]string(nil), semanticContextForbiddenEffects...),
	}
}

func (request SemanticContextRequest) Valid() bool {
	if request.Label != SemanticContextLabel || request.AllowedUse != SemanticContextUse || request.CurrentRealityRule != SemanticContextPrecedence || !request.NonAuthoritative || !request.NoTeamsAuthorityFallback || !request.InvocationID.Valid() || !request.AuthorizationEventID.Valid() || !request.ParentEventID.Valid() || !request.TaskID.Valid() || !request.StoryID.Valid() || !request.TaskCreatedEventID.Valid() || !request.TaskCreatedSourceDigest.Valid() || request.TaskRevision == 0 || request.LifecycleEpoch == 0 || request.ScopeRevision == 0 || !request.OperationalScopeEventID.Valid() || !request.AssignmentID.Valid() || !request.ActorFQN.Valid() || !request.Execution.Valid() || request.WorkspaceID == "" || request.WorktreeID == "" || len(request.BaselineSHA) != 40 || !request.WorkProfile.Valid() || !request.ModelProfileDigest.Valid() || !request.RuntimeIdentityDigest.Valid() || !request.ToolPolicyDigest.Valid() || !request.EffectPolicyDigest.Valid() || len(request.ForbiddenEffects) != len(semanticContextForbiddenEffects) {
		return false
	}
	for index, effect := range semanticContextForbiddenEffects {
		if request.ForbiddenEffects[index] != effect {
			return false
		}
	}
	for _, evidence := range request.Evidence {
		if !evidence.EvidenceID.Valid() || !evidence.SHA256.Valid() {
			return false
		}
	}
	return true
}

func (brief ExecutionBrief) SemanticContextValid() bool {
	request := brief.SemanticContext
	if !request.Valid() || request.InvocationID != brief.InvocationID || request.AuthorizationEventID != brief.AuthorizationEventID || request.ParentEventID != brief.ParentEventID || request.TaskID != brief.Task.TaskID || request.StoryID != brief.Task.StoryID || request.TaskCreatedEventID != brief.Task.CreatedEventID || request.TaskCreatedSourceDigest != brief.Task.SourceDigest || request.TaskRevision != brief.TaskRevision || request.LifecycleEpoch != brief.LifecycleEpoch || request.ScopeRevision != brief.ScopeRevision || request.OperationalScopeEventID != brief.Scope.BoundEventID || request.AssignmentID != brief.AssignmentID || request.ActorFQN != brief.ActorFQN || request.Execution != brief.Execution || request.WorkspaceID != brief.Scope.WorkspaceID || request.WorktreeID != brief.Scope.WorktreeID || request.BaselineSHA != brief.Scope.BaselineSHA || request.WorkProfile != brief.WorkProfile.Binding() || request.ModelProfileDigest != brief.ModelProfileDigest || request.RuntimeIdentityDigest != brief.RuntimeIdentityDigest || request.ToolPolicyDigest != brief.ToolPolicyDigest || request.EffectPolicyDigest != brief.EffectPolicyDigest || len(request.Evidence) != len(brief.Evidence) {
		return false
	}
	for index := range request.Evidence {
		if request.Evidence[index] != brief.Evidence[index] {
			return false
		}
	}
	return true
}

func SemanticContextForbiddenEffects() []string {
	return append([]string(nil), semanticContextForbiddenEffects...)
}

func BuildExecutionBrief(current OperationalExecutionContext, maximumBytes int) (ExecutionBrief, kernel.Digest, error) {
	if maximumBytes <= 0 || maximumBytes > 1<<20 {
		return ExecutionBrief{}, "", ErrInvalidOperationalExecution
	}
	invocation := current.Invocation
	evidence := append([]kernel.EvidenceRef(nil), current.Evidence...)
	sort.Slice(evidence, func(left, right int) bool { return evidence[left].EvidenceID < evidence[right].EvidenceID })
	brief := ExecutionBrief{
		ContractManifest: kernel.ContractIdentity, InvocationID: invocation.ID,
		AuthorizationEventID: invocation.AuthorizationEventID, ParentEventID: invocation.ParentEventID,
		Task: current.Specification, TaskRevision: invocation.TaskRevision,
		LifecycleEpoch: invocation.LifecycleEpoch, ScopeRevision: invocation.ScopeRevision,
		Purpose: invocation.Purpose, AttemptFamily: invocation.AttemptFamily,
		AttemptOrdinal: invocation.AttemptOrdinal, RetryOrdinal: invocation.RetryOrdinal,
		ConditionDigest: invocation.ConditionDigest, OutputPredicateDigest: invocation.OutputPredicateDigest,
		ToolPolicyDigest: invocation.ToolPolicyDigest, EffectPolicyDigest: invocation.EffectPolicyDigest,
		WorkProfile: current.Profile.Profile.Clone(), AssignmentID: invocation.QualifiedAssignmentID,
		DecisionRoute: current.Assignment.SelectedDecisionRoute, ActorFQN: invocation.ActorFQN,
		Execution: invocation.Execution, ModelProfileDigest: invocation.ModelProfileDigest,
		RuntimeIdentityDigest: invocation.RuntimeIdentityDigest, Scope: current.Scope.Clone(),
		Evidence: evidence, RemainingGlobalBudget: invocation.RemainingGlobalBudget,
		RemainingPurposeBudget: invocation.RemainingPurposeBudget, DeadlineAt: invocation.DeadlineAt,
		CoordinationRule: evidenceOnlyCoordinationRule,
	}
	if invocation.Purpose == kernel.PurposeValidation || invocation.Purpose == kernel.PurposeReview {
		brief.ResultProtocol = &ExecutionResultProtocol{
			SchemaVersion: "1.0.0", Marker: ValidationResultMarker,
			Outcomes:    []string{"PASS", "FAIL", "BLOCKED", "INCONCLUSIVE"},
			Instruction: "The OpenHands finish tool message is the result consumed by Teams. Set finish.message to the marker on its own line followed by exactly one JSON object containing schema_version, outcome, and non-empty reasons. Do not summarize or paraphrase the result in finish.message. Teams will reject missing or malformed results.",
		}
	} else if invocation.Purpose == kernel.PurposePromotion {
		brief.ResultProtocol = &ExecutionResultProtocol{
			SchemaVersion: "1.0.0", Marker: ValidationResultMarker,
			Outcomes:    []string{"PASS", "FAIL", "BLOCKED", "INCONCLUSIVE"},
			Instruction: "Act as the product owner. The OpenHands finish tool message is the result consumed by Teams. Set finish.message to the marker on its own line followed by exactly one JSON object containing schema_version, outcome, and non-empty reasons. Do not summarize or paraphrase the result in finish.message. PASS means every story acceptance criterion is supported by the supplied completion evidence; Teams remains the authority that records acceptance and release state.",
		}
	} else if invocation.Purpose == kernel.PurposeHandoff || invocation.Purpose == kernel.PurposeReplan {
		brief.ResultProtocol = &ExecutionResultProtocol{
			SchemaVersion: "1.0.0", Marker: OrganizationalResultMarker,
			Outcomes:    []string{"STRUCTURED_HANDOFF"},
			Instruction: "The OpenHands finish tool message is the result consumed by Teams. Set finish.message to the marker on its own line followed by exactly one JSON object conforming to the result schema in the task description. Do not summarize or paraphrase the result in finish.message. Teams will reject missing, malformed, or extra fields.",
		}
	}
	brief.SemanticContext = buildSemanticContextRequest(current, evidence)
	if !brief.SemanticContextValid() {
		return ExecutionBrief{}, "", ErrInvalidOperationalExecution
	}
	encoded, err := json.Marshal(brief)
	if err != nil || len(encoded) > maximumBytes {
		return ExecutionBrief{}, "", ErrInvalidOperationalExecution
	}
	digest := sha256.Sum256(encoded)
	return brief, kernel.Digest(hex.EncodeToString(digest[:])), nil
}

type ExternalExecutionState string

const (
	ExternalAbsent    ExternalExecutionState = "ABSENT"
	ExternalRunning   ExternalExecutionState = "RUNNING"
	ExternalSucceeded ExternalExecutionState = "SUCCEEDED"
	ExternalFailed    ExternalExecutionState = "FAILED"
	ExternalTimedOut  ExternalExecutionState = "TIMED_OUT"
	ExternalCancelled ExternalExecutionState = "CANCELLED"
	ExternalRejected  ExternalExecutionState = "START_REJECTED"
	ExternalUnknown   ExternalExecutionState = "UNKNOWN"
)

type ExecutionEvidence struct {
	EvidenceID      kernel.UUIDv7
	Kind            string
	MediaType       string
	SourceTimestamp time.Time
	Content         []byte
}

type ExternalExecutionObservation struct {
	InvocationID          kernel.UUIDv7
	RequestDigest         kernel.Digest
	ConversationID        string
	State                 ExternalExecutionState
	ActorFQN              kernel.ActorFQN
	Execution             kernel.ExecutionTuple
	ModelProfileDigest    kernel.Digest
	RuntimeIdentityDigest kernel.Digest
	WorkspaceID           string
	ToolPolicyDigest      kernel.Digest
	EffectPolicyDigest    kernel.Digest
	Retryable             bool
	Evidence              []ExecutionEvidence
	Output                []byte
}

type OpenHandsExecutionBoundary interface {
	Start(context.Context, ExecutionBrief, kernel.Digest) (ExternalExecutionObservation, error)
	ReconcileStart(context.Context, ExecutionBrief, kernel.Digest) (ExternalExecutionObservation, error)
	Inspect(context.Context, ExecutionBrief, string, kernel.Digest) (ExternalExecutionObservation, error)
	Cancel(context.Context, ExecutionBrief, string, kernel.Digest) (ExternalExecutionObservation, error)
}

type ExecutionEvidenceRecorder interface {
	RecordExecutionEvidence(context.Context, kernel.WorkInvocation, []ExecutionEvidence) ([]kernel.EvidenceRef, error)
}

func externalObservationMatches(brief ExecutionBrief, requestDigest kernel.Digest, observation ExternalExecutionObservation) bool {
	return observation.InvocationID == brief.InvocationID && observation.RequestDigest == requestDigest && observation.ActorFQN == brief.ActorFQN && observation.Execution == brief.Execution && observation.ModelProfileDigest == brief.ModelProfileDigest && observation.RuntimeIdentityDigest == brief.RuntimeIdentityDigest && observation.WorkspaceID == brief.Scope.WorkspaceID && observation.ToolPolicyDigest == brief.ToolPolicyDigest && observation.EffectPolicyDigest == brief.EffectPolicyDigest
}
