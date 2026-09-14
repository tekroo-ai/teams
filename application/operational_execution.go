package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
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
	AdmittedMessage        *AdmittedMessage
	RetryOfConversationID  *string
	Task                   kernel.AggregateState
	Budget                 kernel.WorkBudgetAccount
	TaskBudget             kernel.TaskWorkBudgetBinding
	Scope                  kernel.TaskOperationalScope
	Profile                kernel.WorkProfileSnapshot
	Assignment             kernel.QualifiedAssignmentAuthorization
	CurrentExecution       kernel.ExecutionTuple
	Specification          TaskExecutionSpecification
	Evidence               []kernel.EvidenceRef
	RecoveryDirective      *ExecutionRecoveryDirective
	AuthorizationEventSeen bool
	ParentEventSeen        bool
}

// ExecutionRecoveryDirective is the exact operator-authored reason and
// evidence that changed the conditions for an explicit recovery. It is
// reconstructed from accepted Teams events rather than conversational state.
type ExecutionRecoveryDirective struct {
	SourceEventID kernel.UUIDv7        `json:"source_event_id"`
	Reason        string               `json:"reason"`
	Evidence      []kernel.EvidenceRef `json:"evidence"`
}

func (directive ExecutionRecoveryDirective) Valid() bool {
	if !directive.SourceEventID.Valid() || strings.TrimSpace(directive.Reason) == "" || len(directive.Reason) > 4096 || len(directive.Evidence) == 0 || len(directive.Evidence) > 64 {
		return false
	}
	for index, evidence := range directive.Evidence {
		if !evidence.EvidenceID.Valid() || !evidence.SHA256.Valid() || index > 0 && evidence.EvidenceID <= directive.Evidence[index-1].EvidenceID {
			return false
		}
	}
	return true
}

func (directive ExecutionRecoveryDirective) Clone() ExecutionRecoveryDirective {
	directive.Evidence = append([]kernel.EvidenceRef(nil), directive.Evidence...)
	return directive
}

type OperationalExecutionReader interface {
	LoadOperationalExecution(context.Context, kernel.UUIDv7) (OperationalExecutionContext, error)
	LoadOperationalExecutionByAuthorizationEvent(context.Context, kernel.UUIDv7) (OperationalExecutionContext, error)
}

// OperationalDeadlineExtensionReader is an optional runtime capability. It
// preserves an already-started invocation across recorded host suspension
// without changing the immutable authorization or its audit timestamps.
type OperationalDeadlineExtensionReader interface {
	EffectiveWorkInvocationDeadline(context.Context, kernel.WorkInvocation, time.Time) (time.Time, error)
}

func (c OperationalExecutionContext) Validate(now time.Time) error {
	invocation := c.Invocation
	invalidRetryContext := invocation.RetryOfInvocationID == nil && c.RetryOfConversationID != nil || invocation.RetryOfInvocationID != nil && c.RetryOfConversationID != nil && *c.RetryOfConversationID != string(*invocation.RetryOfInvocationID)
	invalidAdmittedMessage := invocation.HandlerDispatch == nil && c.AdmittedMessage != nil || invocation.HandlerDispatch != nil && (c.AdmittedMessage == nil || !c.AdmittedMessage.Valid(*invocation.HandlerDispatch))
	if !invocation.Valid() || invalidRetryContext || invalidAdmittedMessage || c.Task.Kind != kernel.AggregateTask || c.Task.ID != invocation.TaskID || c.Task.Revision != invocation.TaskRevision || c.Task.LifecycleEpoch != invocation.LifecycleEpoch || c.Task.ScopeRevision != invocation.ScopeRevision || c.Task.Phase != kernel.PhaseActive || c.Task.Condition != kernel.ConditionRunnable || !c.AuthorizationEventSeen || !c.ParentEventSeen {
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
	RetryOfInvocationID    *kernel.UUIDv7              `json:"retry_of_invocation_id"`
	RetryOfConversationID  *string                     `json:"retry_of_conversation_id,omitempty"`
	RetryOrdinal           uint64                      `json:"retry_ordinal"`
	ConditionDigest        kernel.Digest               `json:"condition_digest"`
	OutputPredicateDigest  kernel.Digest               `json:"output_predicate_digest"`
	ToolPolicyDigest       kernel.Digest               `json:"tool_policy_digest"`
	EffectPolicyDigest     kernel.Digest               `json:"effect_policy_digest"`
	WorkProfile            kernel.WorkRiskProfile      `json:"work_profile"`
	AssignmentID           kernel.UUIDv7               `json:"assignment_id"`
	DecisionRoute          kernel.DecisionRoute        `json:"decision_route"`
	ActorFQN               kernel.ActorFQN             `json:"actor_fqn"`
	RoleGrounding          RoleExecutionGrounding      `json:"role_grounding"`
	MessageHandler         *MessageHandlerGrounding    `json:"message_handler,omitempty"`
	AdmittedMessage        *AdmittedMessage            `json:"admitted_message,omitempty"`
	Execution              kernel.ExecutionTuple       `json:"execution"`
	ModelProfileDigest     kernel.Digest               `json:"model_profile_digest"`
	RuntimeIdentityDigest  kernel.Digest               `json:"runtime_identity_digest"`
	Scope                  kernel.TaskOperationalScope `json:"scope"`
	Evidence               []kernel.EvidenceRef        `json:"evidence"`
	RemainingGlobalBudget  uint64                      `json:"remaining_global_budget"`
	RemainingPurposeBudget uint64                      `json:"remaining_purpose_budget"`
	DeadlineAt             time.Time                   `json:"deadline_at"`
	CoordinationRule       string                      `json:"coordination_rule"`
	ExecutionGuidance      []string                    `json:"execution_guidance"`
	RecoveryDirective      *ExecutionRecoveryDirective `json:"recovery_directive,omitempty"`
	ResultProtocol         *ExecutionResultProtocol    `json:"result_protocol,omitempty"`
	SemanticContext        SemanticContextRequest      `json:"semantic_context"`
}

// RoleExecutionGrounding binds one exact running actor FQN to the authenticated
// role bundle indexed by its FQRN. The bundle digest binds the human-readable
// duties and permissions to the signed bundle loaded at service startup.
type RoleExecutionGrounding struct {
	ActorFQN      kernel.ActorFQN `json:"actor_fqn"`
	RoleFQRN      kernel.RoleFQRN `json:"role_fqrn"`
	BundleVersion string          `json:"bundle_version"`
	BundleDigest  kernel.Digest   `json:"bundle_digest"`
	Capabilities  []string        `json:"capabilities"`
	Permissions   []string        `json:"permissions"`
	Instructions  string          `json:"instructions"`
}

func (grounding RoleExecutionGrounding) Valid(actor kernel.ActorFQN) bool {
	fqrn, err := kernel.RoleFQRNFromActor(actor)
	if err != nil || grounding.ActorFQN != actor || grounding.RoleFQRN != fqrn || grounding.BundleVersion == "" || !grounding.BundleDigest.Valid() || strings.TrimSpace(grounding.Instructions) == "" || len(grounding.Instructions) > 1<<20 || len(grounding.Capabilities) == 0 || len(grounding.Permissions) == 0 || !slices.IsSorted(grounding.Capabilities) || !slices.IsSorted(grounding.Permissions) {
		return false
	}
	for _, values := range [][]string{grounding.Capabilities, grounding.Permissions} {
		previous := ""
		for _, value := range values {
			if value == "" || value <= previous {
				return false
			}
			previous = value
		}
	}
	return true
}

type RoleGroundingResolver interface {
	ResolveRoleGrounding(context.Context, kernel.ActorFQN) (RoleExecutionGrounding, error)
}

// MessageHandlerGrounding contains only the handler selected by the admitted
// organizational message. Unrelated handlers never enter the model context.
type MessageHandlerGrounding struct {
	MessageID               kernel.UUIDv7   `json:"message_id"`
	MessageType             string          `json:"message_type"`
	MessagePurpose          string          `json:"message_purpose"`
	SubscriptionPurpose     string          `json:"subscription_purpose"`
	CharterDigest           kernel.Digest   `json:"charter_digest"`
	HandlerDigest           kernel.Digest   `json:"handler_digest"`
	Instructions            string          `json:"instructions"`
	InputSchemaDigest       kernel.Digest   `json:"input_schema_digest"`
	InputSchema             json.RawMessage `json:"input_schema"`
	ResultSchemaDigest      kernel.Digest   `json:"result_schema_digest"`
	ResultSchema            json.RawMessage `json:"result_schema"`
	AllowedResults          []string        `json:"allowed_results"`
	AllowedMessageProposals []string        `json:"allowed_message_proposals"`
}

type AdmittedMessage struct {
	ID      kernel.UUIDv7   `json:"id"`
	Type    string          `json:"type"`
	Purpose string          `json:"purpose"`
	Body    json.RawMessage `json:"body"`
}

func (message AdmittedMessage) Valid(binding kernel.HandlerDispatchBinding) bool {
	return message.ID == binding.MessageID && message.Type == binding.MessageType && message.Purpose == binding.MessagePurpose && len(message.Body) > 0 && contentDigest(message.Body) == binding.MessageBodyDigest
}

func cloneAdmittedMessage(message *AdmittedMessage) *AdmittedMessage {
	if message == nil {
		return nil
	}
	copy := *message
	copy.Body = append(json.RawMessage(nil), message.Body...)
	return &copy
}

func (grounding MessageHandlerGrounding) Valid(binding kernel.HandlerDispatchBinding, charter string) bool {
	if !binding.Valid() || grounding.MessageID != binding.MessageID || grounding.MessageType != binding.MessageType || grounding.MessagePurpose != binding.MessagePurpose || grounding.SubscriptionPurpose != binding.SubscriptionPurpose || grounding.CharterDigest != binding.CharterDigest || grounding.HandlerDigest != binding.HandlerDigest || grounding.InputSchemaDigest != binding.InputSchemaDigest || grounding.ResultSchemaDigest != binding.ResultSchemaDigest || !slices.Equal(grounding.AllowedResults, binding.AllowedResults) || !slices.Equal(grounding.AllowedMessageProposals, binding.AllowedMessageProposals) || strings.TrimSpace(grounding.Instructions) == "" || len(grounding.Instructions) > 1<<20 || len(grounding.InputSchema) == 0 || len(grounding.ResultSchema) == 0 {
		return false
	}
	return contentDigest([]byte(charter)) == grounding.CharterDigest && contentDigest([]byte(grounding.Instructions)) == grounding.HandlerDigest && contentDigest(grounding.InputSchema) == grounding.InputSchemaDigest && contentDigest(grounding.ResultSchema) == grounding.ResultSchemaDigest
}

type RoleHandlerGroundingResolver interface {
	ResolveRoleHandlerGrounding(context.Context, kernel.ActorFQN, kernel.HandlerDispatchBinding) (RoleExecutionGrounding, MessageHandlerGrounding, error)
}

func contentDigest(content []byte) kernel.Digest {
	digest := sha256.Sum256(content)
	return kernel.Digest(hex.EncodeToString(digest[:]))
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

var sharedExecutionGuidance = []string{
	"Before acting, inspect role_grounding: actor_fqn identifies this running instance, role_fqrn identifies its signed role bundle, and the bundle instructions, capabilities, and permissions define the role you must perform.",
	"Read and follow AGENTS.md before taking repository actions.",
	"Use rg or rg --files for repository discovery.",
	"The authorized workspace is already the terminal working directory. Issue exactly one shell command per terminal action: do not use cd, &&, semicolons, pipes, command substitution, environment-variable expansion, or multiple commands separated by newlines.",
	"Every inspection must resolve a concrete open question in the assigned task. When a search identifies the relevant implementation and tests, inspect those files and stop discovery; do not enumerate unrelated directories to prove absence.",
	"Read accepted CONTRACTS packages only when they directly resolve an open question in the assigned task. Never modify accepted CONTRACTS packages.",
	"Stay inside the authorized workspace and task scope.",
}

var editableExecutionGuidance = []string{
	"Inspect enough current code and tests to justify the change, then make the smallest cohesive edit. If the task remains ambiguous after the relevant surfaces are exhausted, report the concrete blocker.",
	"Map and extend existing interfaces before adding a parallel abstraction.",
	"Implement in cohesive increments and run focused tests after each increment.",
	"Before reporting success, commit the intended changes and tests on the assigned branch and leave Git status clean. Validators receive only the committed candidate.",
}

var readOnlyExecutionGuidance = []string{
	"This task does not authorize repository edits. Do not edit repository files or create implementation artifacts.",
	"Finish the assigned plan, review, or report from enough observed repository evidence to support it; if the relevant surfaces are exhausted and evidence remains insufficient, report the concrete blocker.",
	"Inspect current interfaces and relevant tests only as needed to perform the assigned role, then return the required result through the finish tool.",
	"When the task requires a structured result, validate the complete finish message against result_protocol and every required field in the task's result schema before calling finish.",
}

var validationExecutionGuidance = []string{
	"Act as an independent engineering validator, not only as a test runner. Candidate-authored tests, builds, vet, and race checks are necessary evidence but do not by themselves prove that the implementation matches an acceptance criterion.",
	"For every acceptance criterion, trace the relevant production control or data path and test a concrete plausible near-miss against that path. A cited test supports a criterion only when its assertions distinguish the required behavior from that near-miss.",
	"Return PASS only when every criterion is supported by the inspected production behavior and distinguishing verification. Return FAIL for an observed contradiction and INCONCLUSIVE when the available evidence cannot distinguish compliance from a plausible near-miss; never convert missing evidence into PASS.",
}

var reviewExecutionGuidance = []string{
	"Perform only the review defined by the task acceptance criteria and the signed role's capabilities. Implementation details and test results may be inspected as evidence, but they do not expand the review's authority.",
	"Report evidence-backed findings within that review boundary. Do not assume ownership of implementation, architecture, general functional validation, product acceptance, or release unless the task and signed role explicitly assign it.",
	"Return PASS only when every review criterion is supported by relevant inspected or executed evidence. Return FAIL for an observed contradiction and INCONCLUSIVE when the available evidence is insufficient; never convert missing evidence into PASS.",
}

var sharedRetainedRetryExecutionGuidance = []string{
	"This is a bounded retry. Reuse the current workspace and retained task evidence; do not restart repository discovery from the beginning.",
	"The prior OpenHands conversation is retained in this retry. Do not reread AGENTS.md or repeat ls, rg, find, sed, cat, or file-view actions already present in that history.",
	"Continue from the retained checkpoint. Distinct, relevant inspections are allowed; exact repeated actions without an intervening state change are rejected.",
}

var editableRetainedRetryExecutionGuidance = []string{
	"Make the smallest justified code or test edit from the retained findings, using additional distinct inspections only where the checkpoint leaves a concrete gap.",
}

var readOnlyRetainedRetryExecutionGuidance = []string{
	"Produce the assigned plan, review, or report immediately from the retained findings. Do not perform implementation edits.",
}

var sharedExplicitRecoveryExecutionGuidance = []string{
	"This explicit recovery starts a clean OpenHands conversation. Prior conversational history is not available; use the current workspace and task evidence as the durable recovery state.",
	"The recovery_directive records the exact condition that made the preceding result unacceptable. Address it before re-evaluating the original acceptance criteria; satisfying the original task without resolving the directive is not a successful recovery.",
	"Inspect Git status, recent commits, and the focused diff before reading source broadly. The workspace may already contain a completed implementation from the failed invocation.",
	"Do not restart implementation or force a new edit when the current committed work already satisfies the task. Verify the existing result against the acceptance criteria and finish promptly.",
}

var editableExplicitRecoveryExecutionGuidance = []string{
	"If the existing workspace does not satisfy the task, make the smallest justified code or test correction, then run focused verification. If it already satisfies the task, run focused verification and finish without changing it.",
	"Map and extend existing interfaces before adding a parallel abstraction.",
	"Implement in cohesive increments and run focused tests after each increment.",
}

var readOnlyExplicitRecoveryExecutionGuidance = []string{
	"This task does not authorize repository edits. Do not edit repository files or create implementation artifacts.",
	"If the preceding result was rejected by a deterministic result validator, preserve its supported engineering conclusions and regenerate the complete structured result from the authoritative task schema. Before calling finish, verify every required object field is present and every required value is non-empty.",
	"Produce the assigned plan, review, or report from the current workspace and task evidence, then return the result through the finish tool.",
}

func hasExecutionPermission(permissions []string, required string) bool {
	_, found := slices.BinarySearch(permissions, required)
	return found
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

func cloneUUID(value *kernel.UUIDv7) *kernel.UUIDv7 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func BuildExecutionBrief(current OperationalExecutionContext, grounding RoleExecutionGrounding, maximumBytes int) (ExecutionBrief, kernel.Digest, error) {
	return BuildExecutionBriefWithHandler(current, grounding, nil, maximumBytes)
}

func BuildExecutionBriefWithHandler(current OperationalExecutionContext, grounding RoleExecutionGrounding, handler *MessageHandlerGrounding, maximumBytes int) (ExecutionBrief, kernel.Digest, error) {
	if maximumBytes <= 0 || maximumBytes > 1<<20 {
		return ExecutionBrief{}, "", ErrInvalidOperationalExecution
	}
	invocation := current.Invocation
	if !grounding.Valid(invocation.ActorFQN) {
		return ExecutionBrief{}, "", ErrInvalidOperationalExecution
	}
	if invocation.HandlerDispatch == nil && (handler != nil || current.AdmittedMessage != nil) || invocation.HandlerDispatch != nil && (handler == nil || current.AdmittedMessage == nil || !current.AdmittedMessage.Valid(*invocation.HandlerDispatch) || grounding.BundleDigest != invocation.HandlerDispatch.RoleBundleDigest || !handler.Valid(*invocation.HandlerDispatch, grounding.Instructions)) {
		return ExecutionBrief{}, "", ErrInvalidOperationalExecution
	}
	if handler != nil {
		if err := ValidateRoleHandlerInput(*handler, *current.AdmittedMessage); err != nil {
			return ExecutionBrief{}, "", err
		}
	}
	evidence := append([]kernel.EvidenceRef(nil), current.Evidence...)
	sort.Slice(evidence, func(left, right int) bool { return evidence[left].EvidenceID < evidence[right].EvidenceID })
	brief := ExecutionBrief{
		ContractManifest: kernel.ContractIdentity, InvocationID: invocation.ID,
		AuthorizationEventID: invocation.AuthorizationEventID, ParentEventID: invocation.ParentEventID,
		Task: current.Specification, TaskRevision: invocation.TaskRevision,
		LifecycleEpoch: invocation.LifecycleEpoch, ScopeRevision: invocation.ScopeRevision,
		Purpose: invocation.Purpose, AttemptFamily: invocation.AttemptFamily,
		AttemptOrdinal: invocation.AttemptOrdinal, RetryOfInvocationID: cloneUUID(invocation.RetryOfInvocationID), RetryOfConversationID: cloneString(current.RetryOfConversationID), RetryOrdinal: invocation.RetryOrdinal,
		ConditionDigest: invocation.ConditionDigest, OutputPredicateDigest: invocation.OutputPredicateDigest,
		ToolPolicyDigest: invocation.ToolPolicyDigest, EffectPolicyDigest: invocation.EffectPolicyDigest,
		WorkProfile: current.Profile.Profile.Clone(), AssignmentID: invocation.QualifiedAssignmentID,
		DecisionRoute: current.Assignment.SelectedDecisionRoute, ActorFQN: invocation.ActorFQN,
		RoleGrounding:   grounding,
		MessageHandler:  cloneMessageHandlerGrounding(handler),
		AdmittedMessage: cloneAdmittedMessage(current.AdmittedMessage),
		Execution:       invocation.Execution, ModelProfileDigest: invocation.ModelProfileDigest,
		RuntimeIdentityDigest: invocation.RuntimeIdentityDigest, Scope: current.Scope.Clone(),
		Evidence: evidence, RemainingGlobalBudget: invocation.RemainingGlobalBudget,
		RemainingPurposeBudget: invocation.RemainingPurposeBudget, DeadlineAt: invocation.DeadlineAt,
		CoordinationRule: evidenceOnlyCoordinationRule,
	}
	brief.ExecutionGuidance = append([]string(nil), sharedExecutionGuidance...)
	mutationAuthorized := hasExecutionPermission(grounding.Permissions, "repository.edit") &&
		(invocation.Purpose == kernel.PurposeImplementation || invocation.Purpose == kernel.PurposeRepair)
	// A retry is not necessarily an operator-directed recovery. Changed-candidate
	// validation and other technical continuations can legitimately use a
	// successor profile without an operator-authored directive. Presence of the
	// durable directive distinguishes those paths; REPAIR always requires it.
	explicitRecovery := current.Profile.Profile.SupersedesProfileID != nil && (current.RecoveryDirective != nil || invocation.Purpose == kernel.PurposeRepair)
	if explicitRecovery {
		if current.RecoveryDirective == nil || !current.RecoveryDirective.Valid() {
			return ExecutionBrief{}, "", ErrInvalidOperationalExecution
		}
		availableEvidence := make(map[kernel.UUIDv7]kernel.Digest, len(evidence))
		for _, reference := range evidence {
			availableEvidence[reference.EvidenceID] = reference.SHA256
		}
		for _, reference := range current.RecoveryDirective.Evidence {
			if availableEvidence[reference.EvidenceID] != reference.SHA256 {
				return ExecutionBrief{}, "", ErrInvalidOperationalExecution
			}
		}
		directive := current.RecoveryDirective.Clone()
		brief.RecoveryDirective = &directive
	}
	if explicitRecovery {
		brief.ExecutionGuidance = append(brief.ExecutionGuidance, sharedExplicitRecoveryExecutionGuidance...)
		if mutationAuthorized {
			brief.ExecutionGuidance = append(brief.ExecutionGuidance, editableExplicitRecoveryExecutionGuidance...)
		} else {
			brief.ExecutionGuidance = append(brief.ExecutionGuidance, readOnlyExplicitRecoveryExecutionGuidance...)
		}
	} else {
		if mutationAuthorized {
			brief.ExecutionGuidance = append(brief.ExecutionGuidance, editableExecutionGuidance...)
		} else {
			brief.ExecutionGuidance = append(brief.ExecutionGuidance, readOnlyExecutionGuidance...)
		}
		if invocation.RetryOrdinal > 0 {
			brief.ExecutionGuidance = append(brief.ExecutionGuidance, sharedRetainedRetryExecutionGuidance...)
			if mutationAuthorized {
				brief.ExecutionGuidance = append(brief.ExecutionGuidance, editableRetainedRetryExecutionGuidance...)
			} else {
				brief.ExecutionGuidance = append(brief.ExecutionGuidance, readOnlyRetainedRetryExecutionGuidance...)
			}
		}
	}
	if handler != nil {
		brief.ResultProtocol = &ExecutionResultProtocol{
			SchemaVersion: "1.0.0", Marker: OrganizationalResultMarker,
			Outcomes:    append([]string(nil), handler.AllowedResults...),
			Instruction: "The OpenHands finish tool message is the result consumed by Teams. Set finish.message to the marker on its own line followed by exactly one JSON object conforming to message_handler.result_schema. Put any task-specific structured result required by the admitted work inside work_product, not beside the envelope. Return only results and message proposals allowed by the selected handler. Teams validates the result and remains the sole authority that records state changes or dispatches successor work.",
		}
	} else if invocation.Purpose == kernel.PurposeValidation {
		brief.ExecutionGuidance = append(brief.ExecutionGuidance, validationExecutionGuidance...)
		brief.ResultProtocol = &ExecutionResultProtocol{
			SchemaVersion: "1.0.0", Marker: ValidationResultMarker,
			Outcomes:    []string{"PASS", "FAIL", "BLOCKED", "INCONCLUSIVE"},
			Instruction: "The OpenHands finish tool message is the result consumed by Teams. Set finish.message to the marker on its own line followed by exactly one JSON object containing schema_version, outcome, and non-empty reasons. For PASS, reasons must cover every acceptance criterion and identify the inspected production control or data path plus verification that distinguishes the required behavior from a plausible near-miss; test names or aggregate test commands alone are insufficient. Do not summarize or paraphrase the result in finish.message. Teams will reject missing or malformed results.",
		}
	} else if invocation.Purpose == kernel.PurposeReview {
		brief.ExecutionGuidance = append(brief.ExecutionGuidance, reviewExecutionGuidance...)
		brief.ResultProtocol = &ExecutionResultProtocol{
			SchemaVersion: "1.0.0", Marker: ValidationResultMarker,
			Outcomes:    []string{"PASS", "FAIL", "BLOCKED", "INCONCLUSIVE"},
			Instruction: "The OpenHands finish tool message is the result consumed by Teams. Set finish.message to the marker on its own line followed by exactly one JSON object containing schema_version, outcome, and non-empty reasons. For PASS, reasons must cover every assigned review criterion with evidence inside the signed role's review boundary. Do not make a broader implementation, architecture, product-acceptance, release, or workflow determination. Do not summarize or paraphrase the result in finish.message. Teams will reject missing or malformed results.",
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

func cloneMessageHandlerGrounding(handler *MessageHandlerGrounding) *MessageHandlerGrounding {
	if handler == nil {
		return nil
	}
	copy := *handler
	copy.InputSchema = append(json.RawMessage(nil), handler.InputSchema...)
	copy.ResultSchema = append(json.RawMessage(nil), handler.ResultSchema...)
	copy.AllowedResults = append([]string(nil), handler.AllowedResults...)
	copy.AllowedMessageProposals = append([]string(nil), handler.AllowedMessageProposals...)
	return &copy
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
	ReconcileSuperseded(context.Context, ExecutionBrief, string, kernel.Digest, kernel.Digest) (ExternalExecutionObservation, error)
	Cancel(context.Context, ExecutionBrief, string, kernel.Digest) (ExternalExecutionObservation, error)
}

type ExecutionEvidenceRecorder interface {
	RecordExecutionEvidence(context.Context, kernel.WorkInvocation, []ExecutionEvidence) ([]kernel.EvidenceRef, error)
}

func externalObservationMatches(brief ExecutionBrief, requestDigest kernel.Digest, observation ExternalExecutionObservation) bool {
	return observation.InvocationID == brief.InvocationID && observation.RequestDigest == requestDigest && observation.ActorFQN == brief.ActorFQN && observation.Execution == brief.Execution && observation.ModelProfileDigest == brief.ModelProfileDigest && observation.RuntimeIdentityDigest == brief.RuntimeIdentityDigest && observation.WorkspaceID == brief.Scope.WorkspaceID && observation.ToolPolicyDigest == brief.ToolPolicyDigest && observation.EffectPolicyDigest == brief.EffectPolicyDigest
}
