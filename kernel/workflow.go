package kernel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"time"
)

const WorkflowDefinitionSchemaVersion = "1.0.0"

var (
	ErrInvalidWorkflowDefinition = errors.New("invalid workflow definition")
	ErrInvalidWorkflowInstance   = errors.New("invalid workflow instance")
	ErrInvalidWorkflowTransition = errors.New("invalid workflow transition")
	workflowNamePattern          = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{0,126}[a-z0-9])?$`)
	semanticVersionPattern       = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
)

type WorkflowPurpose string

const (
	WorkflowPurposeHandoff        WorkflowPurpose = "HANDOFF"
	WorkflowPurposeImplementation WorkflowPurpose = "IMPLEMENTATION"
	WorkflowPurposeValidation     WorkflowPurpose = "VALIDATION"
	WorkflowPurposeReview         WorkflowPurpose = "REVIEW"
	WorkflowPurposeRepair         WorkflowPurpose = "REPAIR"
	WorkflowPurposeReplan         WorkflowPurpose = "REPLAN"
	WorkflowPurposePromotion      WorkflowPurpose = "PROMOTION"
)

func (purpose WorkflowPurpose) Valid() bool {
	switch purpose {
	case WorkflowPurposeHandoff, WorkflowPurposeImplementation, WorkflowPurposeValidation, WorkflowPurposeReview, WorkflowPurposeRepair, WorkflowPurposeReplan, WorkflowPurposePromotion:
		return true
	default:
		return false
	}
}

type WorkflowRisk string

const (
	WorkflowRiskLow      WorkflowRisk = "LOW"
	WorkflowRiskModerate WorkflowRisk = "MODERATE"
	WorkflowRiskHigh     WorkflowRisk = "HIGH"
	WorkflowRiskCritical WorkflowRisk = "CRITICAL"
)

func (risk WorkflowRisk) Valid() bool {
	return risk == WorkflowRiskLow || risk == WorkflowRiskModerate || risk == WorkflowRiskHigh || risk == WorkflowRiskCritical
}

type WorkflowTargetSelection string

const (
	WorkflowTargetExactActor WorkflowTargetSelection = "EXACT_ACTOR"
	WorkflowTargetCapability WorkflowTargetSelection = "CAPABILITY"
	WorkflowTargetOperator   WorkflowTargetSelection = "OPERATOR"
	WorkflowTargetNone       WorkflowTargetSelection = "NONE"
)

func (selection WorkflowTargetSelection) Valid() bool {
	return selection == WorkflowTargetExactActor || selection == WorkflowTargetCapability || selection == WorkflowTargetOperator || selection == WorkflowTargetNone
}

type WorkflowValidationPolicy string

const (
	WorkflowValidationDeterministic    WorkflowValidationPolicy = "DETERMINISTIC_ONLY"
	WorkflowValidationRiskSelected     WorkflowValidationPolicy = "RISK_SELECTED"
	WorkflowValidationSpecialized      WorkflowValidationPolicy = "SPECIALIZED"
	WorkflowValidationOperatorApproved WorkflowValidationPolicy = "OPERATOR_APPROVED"
)

func (policy WorkflowValidationPolicy) Valid() bool {
	return policy == WorkflowValidationDeterministic || policy == WorkflowValidationRiskSelected || policy == WorkflowValidationSpecialized || policy == WorkflowValidationOperatorApproved
}

type WorkflowBudgetLimits struct {
	MaximumModelInvocations uint64 `json:"maximum_model_invocations"`
	MaximumHops             uint32 `json:"maximum_hops"`
	MaximumAttempts         uint32 `json:"maximum_attempts"`
	MaximumTokens           uint64 `json:"maximum_tokens,omitempty"`
	MaximumElapsedSeconds   uint64 `json:"maximum_elapsed_seconds,omitempty"`
}

func (limits WorkflowBudgetLimits) Valid() bool {
	return limits.MaximumHops > 0 && limits.MaximumAttempts > 0
}

type WorkflowStageDefinition struct {
	StageID                 string                   `json:"stage_id"`
	DependsOn               []string                 `json:"depends_on"`
	InputSchema             string                   `json:"input_schema"`
	OutputSchema            string                   `json:"output_schema"`
	RequiredCapabilities    []string                 `json:"required_capabilities"`
	PreferredFQRNs          []RoleFQRN               `json:"preferred_fqrns,omitempty"`
	Purpose                 WorkflowPurpose          `json:"purpose"`
	Risk                    WorkflowRisk             `json:"risk"`
	ComplexityMinimum       uint8                    `json:"complexity_minimum,omitempty"`
	ComplexityMaximum       uint8                    `json:"complexity_maximum,omitempty"`
	ConcurrencyGroup        string                   `json:"concurrency_group"`
	MaximumParallelism      uint32                   `json:"maximum_parallelism"`
	AttemptLimit            uint32                   `json:"attempt_limit"`
	ReviewRoundLimit        uint32                   `json:"review_round_limit,omitempty"`
	TimeoutSeconds          uint64                   `json:"timeout_seconds,omitempty"`
	TokenBudget             uint64                   `json:"token_budget,omitempty"`
	AllowedOutgoingPurposes []string                 `json:"allowed_outgoing_purposes"`
	TargetSelection         WorkflowTargetSelection  `json:"target_selection"`
	ValidationPolicy        WorkflowValidationPolicy `json:"validation_policy"`
	CompletionCondition     string                   `json:"completion_condition,omitempty"`
	FailureCondition        string                   `json:"failure_condition,omitempty"`
}

func (stage WorkflowStageDefinition) valid() bool {
	if !workflowNamePattern.MatchString(stage.StageID) || stage.InputSchema == "" || len(stage.InputSchema) > 1024 || stage.OutputSchema == "" || len(stage.OutputSchema) > 1024 || !stage.Purpose.Valid() || !stage.Risk.Valid() || !workflowNamePattern.MatchString(stage.ConcurrencyGroup) || stage.MaximumParallelism == 0 || stage.MaximumParallelism > 1024 || stage.AttemptLimit == 0 || stage.AttemptLimit > 32 || stage.ReviewRoundLimit > 16 || stage.TimeoutSeconds > 604800 || !stage.TargetSelection.Valid() || !stage.ValidationPolicy.Valid() || len(stage.CompletionCondition) > 4096 || len(stage.FailureCondition) > 4096 {
		return false
	}
	if stage.ComplexityMinimum > 5 || stage.ComplexityMaximum > 5 || stage.ComplexityMinimum > 0 && stage.ComplexityMaximum > 0 && stage.ComplexityMinimum > stage.ComplexityMaximum {
		return false
	}
	if len(stage.DependsOn) > 255 || len(stage.RequiredCapabilities) > 64 || len(stage.PreferredFQRNs) > 32 || len(stage.AllowedOutgoingPurposes) > 5 || !sortedUniqueStrings(stage.DependsOn, true) || !sortedUniqueStrings(stage.RequiredCapabilities, false) || !sortedUniqueStrings(stage.AllowedOutgoingPurposes, true) {
		return false
	}
	for _, capability := range stage.RequiredCapabilities {
		if !workflowNamePattern.MatchString(capability) {
			return false
		}
	}
	for _, role := range stage.PreferredFQRNs {
		if !role.Valid() {
			return false
		}
	}
	for index := 1; index < len(stage.PreferredFQRNs); index++ {
		if stage.PreferredFQRNs[index] <= stage.PreferredFQRNs[index-1] {
			return false
		}
	}
	for _, purpose := range stage.AllowedOutgoingPurposes {
		switch purpose {
		case "REQUEST", "HANDOFF", "EVIDENCE", "RESPONSE", "NOTIFICATION":
		default:
			return false
		}
	}
	return true
}

type WorkflowDefinition struct {
	SchemaVersion   string                    `json:"schema_version"`
	Name            string                    `json:"name"`
	Version         string                    `json:"version"`
	ContentDigest   Digest                    `json:"content_digest"`
	TriggerTypes    []string                  `json:"trigger_types"`
	Stages          []WorkflowStageDefinition `json:"stages"`
	RootBudgets     WorkflowBudgetLimits      `json:"root_budgets"`
	ProjectionRules []string                  `json:"projection_rules"`
}

func (definition WorkflowDefinition) CalculatedDigest() (Digest, error) {
	copy := definition
	copy.ContentDigest = ""
	raw, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return Digest(hex.EncodeToString(sum[:])), nil
}

func (definition WorkflowDefinition) Clone() WorkflowDefinition {
	copy := definition
	copy.TriggerTypes = slices.Clone(definition.TriggerTypes)
	copy.ProjectionRules = slices.Clone(definition.ProjectionRules)
	copy.Stages = make([]WorkflowStageDefinition, len(definition.Stages))
	for index, stage := range definition.Stages {
		copy.Stages[index] = stage
		copy.Stages[index].DependsOn = slices.Clone(stage.DependsOn)
		copy.Stages[index].RequiredCapabilities = slices.Clone(stage.RequiredCapabilities)
		copy.Stages[index].PreferredFQRNs = slices.Clone(stage.PreferredFQRNs)
		copy.Stages[index].AllowedOutgoingPurposes = slices.Clone(stage.AllowedOutgoingPurposes)
	}
	return copy
}

func (definition WorkflowDefinition) Validate() error {
	if definition.SchemaVersion != WorkflowDefinitionSchemaVersion || !workflowNamePattern.MatchString(definition.Name) || !semanticVersionPattern.MatchString(definition.Version) || !definition.ContentDigest.Valid() || !definition.RootBudgets.Valid() || !sortedUniqueStrings(definition.TriggerTypes, false) || len(definition.TriggerTypes) > 64 || !sortedUniqueStrings(definition.ProjectionRules, true) || len(definition.ProjectionRules) > 64 || len(definition.Stages) == 0 || len(definition.Stages) > 256 {
		return ErrInvalidWorkflowDefinition
	}
	for _, trigger := range definition.TriggerTypes {
		if len(trigger) > 256 {
			return ErrInvalidWorkflowDefinition
		}
	}
	for _, projection := range definition.ProjectionRules {
		switch projection {
		case "FEATURE", "STORY", "TASK", "MESSAGE", "STATUS":
		default:
			return ErrInvalidWorkflowDefinition
		}
	}
	actual, err := definition.CalculatedDigest()
	if err != nil || actual != definition.ContentDigest {
		return ErrInvalidWorkflowDefinition
	}
	nodes := make([]string, 0, len(definition.Stages))
	edges := make([]GraphEdge, 0)
	known := make(map[string]struct{}, len(definition.Stages))
	for _, stage := range definition.Stages {
		if !stage.valid() {
			return ErrInvalidWorkflowDefinition
		}
		if _, duplicate := known[stage.StageID]; duplicate {
			return ErrInvalidWorkflowDefinition
		}
		known[stage.StageID] = struct{}{}
		nodes = append(nodes, stage.StageID)
	}
	for _, stage := range definition.Stages {
		for _, predecessor := range stage.DependsOn {
			if _, exists := known[predecessor]; !exists || predecessor == stage.StageID {
				return ErrInvalidWorkflowDefinition
			}
			edges = append(edges, GraphEdge{Parent: predecessor, Child: stage.StageID})
		}
	}
	if !ValidateDAG(nodes, edges).Valid {
		return ErrInvalidWorkflowDefinition
	}
	return nil
}

func sortedUniqueStrings(values []string, allowEmpty bool) bool {
	if !allowEmpty && len(values) == 0 {
		return false
	}
	for index, value := range values {
		if value == "" || index > 0 && value <= values[index-1] {
			return false
		}
	}
	return true
}

type WorkflowInstanceState string

const (
	WorkflowPending   WorkflowInstanceState = "PENDING"
	WorkflowActive    WorkflowInstanceState = "ACTIVE"
	WorkflowBlocked   WorkflowInstanceState = "BLOCKED"
	WorkflowCompleted WorkflowInstanceState = "COMPLETED"
	WorkflowFailed    WorkflowInstanceState = "FAILED"
	WorkflowCancelled WorkflowInstanceState = "CANCELLED"
)

func (state WorkflowInstanceState) terminal() bool {
	return state == WorkflowCompleted || state == WorkflowFailed || state == WorkflowCancelled
}

func (state WorkflowInstanceState) Valid() bool {
	return state == WorkflowPending || state == WorkflowActive || state == WorkflowBlocked || state == WorkflowCompleted || state == WorkflowFailed || state == WorkflowCancelled
}

type WorkflowNodeState string

const (
	WorkflowNodePending   WorkflowNodeState = "PENDING"
	WorkflowNodeReady     WorkflowNodeState = "READY"
	WorkflowNodeAdmitted  WorkflowNodeState = "ADMITTED"
	WorkflowNodeRunning   WorkflowNodeState = "RUNNING"
	WorkflowNodeBlocked   WorkflowNodeState = "BLOCKED"
	WorkflowNodeCompleted WorkflowNodeState = "COMPLETED"
	WorkflowNodeFailed    WorkflowNodeState = "FAILED"
	WorkflowNodeCancelled WorkflowNodeState = "CANCELLED"
)

func (state WorkflowNodeState) Valid() bool {
	switch state {
	case WorkflowNodePending, WorkflowNodeReady, WorkflowNodeAdmitted, WorkflowNodeRunning, WorkflowNodeBlocked, WorkflowNodeCompleted, WorkflowNodeFailed, WorkflowNodeCancelled:
		return true
	default:
		return false
	}
}

type WorkflowFailureClassification string

const (
	WorkflowFailureNone                 WorkflowFailureClassification = "NONE"
	WorkflowFailureRecoverableTransport WorkflowFailureClassification = "RECOVERABLE_TRANSPORT"
	WorkflowFailureCorrectableWork      WorkflowFailureClassification = "CORRECTABLE_WORK"
	WorkflowFailureTerminalPolicy       WorkflowFailureClassification = "TERMINAL_POLICY"
	WorkflowFailureCancelled            WorkflowFailureClassification = "CANCELLED"
)

func (classification WorkflowFailureClassification) Valid() bool {
	switch classification {
	case WorkflowFailureNone, WorkflowFailureRecoverableTransport, WorkflowFailureCorrectableWork, WorkflowFailureTerminalPolicy, WorkflowFailureCancelled:
		return true
	default:
		return false
	}
}

type WorkflowNode struct {
	NodeID                UUIDv7                        `json:"node_id"`
	StageID               string                        `json:"stage_id"`
	PredecessorNodeIDs    []UUIDv7                      `json:"predecessor_node_ids"`
	State                 WorkflowNodeState             `json:"state"`
	Attempt               uint32                        `json:"attempt"`
	ActorFQN              *ActorFQN                     `json:"actor_fqn,omitempty"`
	InvocationID          *UUIDv7                       `json:"invocation_id,omitempty"`
	InputEvidenceIDs      []UUIDv7                      `json:"input_evidence_ids"`
	OutputEvidenceIDs     []UUIDv7                      `json:"output_evidence_ids"`
	ProgressDigest        *Digest                       `json:"progress_digest,omitempty"`
	CheckpointID          *UUIDv7                       `json:"checkpoint_id,omitempty"`
	FailureClassification WorkflowFailureClassification `json:"failure_classification"`
	ContinuationNodeID    *UUIDv7                       `json:"continuation_node_id,omitempty"`
	BlockedFrom           *WorkflowNodeState            `json:"blocked_from,omitempty"`
}

type WorkflowInstance struct {
	SchemaVersion      string                `json:"schema_version"`
	InstanceID         UUIDv7                `json:"instance_id"`
	Revision           uint64                `json:"revision"`
	DefinitionName     string                `json:"definition_name"`
	DefinitionVersion  string                `json:"definition_version"`
	DefinitionDigest   Digest                `json:"definition_digest"`
	RootRequest        AggregateRef          `json:"root_request"`
	BudgetAccountID    UUIDv7                `json:"budget_account_id"`
	State              WorkflowInstanceState `json:"state"`
	Nodes              []WorkflowNode        `json:"nodes"`
	ProposedMessageIDs []UUIDv7              `json:"proposed_message_ids,omitempty"`
	AcceptedMessageIDs []UUIDv7              `json:"accepted_message_ids,omitempty"`
	LastEventID        *UUIDv7               `json:"last_event_id,omitempty"`
}

func NewWorkflowInstance(definition WorkflowDefinition, instanceID UUIDv7, root AggregateRef, budgetAccountID UUIDv7, nodeIDs map[string]UUIDv7) (WorkflowInstance, error) {
	if definition.Validate() != nil || !instanceID.Valid() || !root.Valid() || !budgetAccountID.Valid() || len(nodeIDs) != len(definition.Stages) {
		return WorkflowInstance{}, ErrInvalidWorkflowInstance
	}
	byStage := make(map[string]UUIDv7, len(nodeIDs))
	for stageID, nodeID := range nodeIDs {
		if !nodeID.Valid() {
			return WorkflowInstance{}, ErrInvalidWorkflowInstance
		}
		byStage[stageID] = nodeID
	}
	instance := WorkflowInstance{SchemaVersion: WorkflowDefinitionSchemaVersion, InstanceID: instanceID, Revision: 1, DefinitionName: definition.Name, DefinitionVersion: definition.Version, DefinitionDigest: definition.ContentDigest, RootRequest: root, BudgetAccountID: budgetAccountID, State: WorkflowActive, Nodes: make([]WorkflowNode, 0, len(definition.Stages))}
	for _, stage := range definition.Stages {
		nodeID, exists := byStage[stage.StageID]
		if !exists {
			return WorkflowInstance{}, ErrInvalidWorkflowInstance
		}
		predecessors := make([]UUIDv7, 0, len(stage.DependsOn))
		for _, predecessor := range stage.DependsOn {
			predecessors = append(predecessors, byStage[predecessor])
		}
		state := WorkflowNodePending
		if len(predecessors) == 0 {
			state = WorkflowNodeReady
		}
		instance.Nodes = append(instance.Nodes, WorkflowNode{NodeID: nodeID, StageID: stage.StageID, PredecessorNodeIDs: predecessors, State: state, InputEvidenceIDs: []UUIDv7{}, OutputEvidenceIDs: []UUIDv7{}, FailureClassification: WorkflowFailureNone})
	}
	if instance.Validate(definition) != nil {
		return WorkflowInstance{}, ErrInvalidWorkflowInstance
	}
	return instance, nil
}

func (instance WorkflowInstance) Validate(definition WorkflowDefinition) error {
	if definition.Validate() != nil || instance.SchemaVersion != WorkflowDefinitionSchemaVersion || !instance.InstanceID.Valid() || instance.Revision == 0 || instance.DefinitionName != definition.Name || instance.DefinitionVersion != definition.Version || instance.DefinitionDigest != definition.ContentDigest || !instance.RootRequest.Valid() || !instance.BudgetAccountID.Valid() || !instance.State.Valid() || len(instance.Nodes) < len(definition.Stages) || len(instance.Nodes) > 4096 || len(instance.ProposedMessageIDs) > 4096 || len(instance.AcceptedMessageIDs) > 4096 || !workflowValidUUIDSet(instance.ProposedMessageIDs) || !workflowValidUUIDSet(instance.AcceptedMessageIDs) || instance.LastEventID != nil && !instance.LastEventID.Valid() {
		return ErrInvalidWorkflowInstance
	}
	knownNodes := make(map[UUIDv7]struct{}, len(instance.Nodes))
	stageCounts := make(map[string]int, len(definition.Stages))
	nodeNames := make([]string, 0, len(instance.Nodes))
	edges := make([]GraphEdge, 0)
	completedNodes := 0
	failedNodes := 0
	cancelledNodes := 0
	blockedNodes := 0
	for _, node := range instance.Nodes {
		if !node.NodeID.Valid() || !workflowNamePattern.MatchString(node.StageID) || !node.State.Valid() || !node.FailureClassification.Valid() || len(node.PredecessorNodeIDs) > 255 || len(node.InputEvidenceIDs) > 1024 || len(node.OutputEvidenceIDs) > 1024 || !workflowValidUUIDSet(node.PredecessorNodeIDs) || !workflowValidUUIDSet(node.InputEvidenceIDs) || !workflowValidUUIDSet(node.OutputEvidenceIDs) {
			return ErrInvalidWorkflowInstance
		}
		if _, exists := knownNodes[node.NodeID]; exists {
			return ErrInvalidWorkflowInstance
		}
		if workflowStageByID(definition, node.StageID) == nil {
			return ErrInvalidWorkflowInstance
		}
		knownNodes[node.NodeID] = struct{}{}
		stageCounts[node.StageID]++
		nodeNames = append(nodeNames, string(node.NodeID))
		switch node.State {
		case WorkflowNodeCompleted:
			completedNodes++
		case WorkflowNodeFailed:
			failedNodes++
		case WorkflowNodeCancelled:
			cancelledNodes++
		case WorkflowNodeBlocked:
			blockedNodes++
		}
	}
	for _, stage := range definition.Stages {
		if stageCounts[stage.StageID] == 0 {
			return ErrInvalidWorkflowInstance
		}
	}
	for _, node := range instance.Nodes {
		stage := workflowStageByID(definition, node.StageID)
		for _, predecessor := range node.PredecessorNodeIDs {
			if _, exists := knownNodes[predecessor]; !exists || predecessor == node.NodeID {
				return ErrInvalidWorkflowInstance
			}
			predecessorNode := workflowNodeByID(&instance, predecessor)
			if predecessorNode == nil || predecessorNode.StageID != node.StageID && !slices.Contains(stage.DependsOn, predecessorNode.StageID) {
				return ErrInvalidWorkflowInstance
			}
			edges = append(edges, GraphEdge{Parent: string(predecessor), Child: string(node.NodeID)})
		}
		if node.ActorFQN != nil && !node.ActorFQN.Valid() || node.InvocationID != nil && !node.InvocationID.Valid() || node.ProgressDigest != nil && !node.ProgressDigest.Valid() || node.CheckpointID != nil && !node.CheckpointID.Valid() || node.ContinuationNodeID != nil && !node.ContinuationNodeID.Valid() {
			return ErrInvalidWorkflowInstance
		}
		if node.BlockedFrom != nil && (!node.BlockedFrom.Valid() || *node.BlockedFrom == WorkflowNodeBlocked) || node.State == WorkflowNodeBlocked && node.BlockedFrom == nil || node.State != WorkflowNodeBlocked && node.BlockedFrom != nil {
			return ErrInvalidWorkflowInstance
		}
		if node.BlockedFrom != nil && (*node.BlockedFrom == WorkflowNodeAdmitted || *node.BlockedFrom == WorkflowNodeRunning) && (node.ActorFQN == nil || node.InvocationID == nil || node.Attempt == 0) {
			return ErrInvalidWorkflowInstance
		}
		if node.BlockedFrom != nil && *node.BlockedFrom == WorkflowNodeReady && (node.ActorFQN != nil || node.InvocationID != nil || node.Attempt != 0) {
			return ErrInvalidWorkflowInstance
		}
		if (node.State == WorkflowNodeAdmitted || node.State == WorkflowNodeRunning || node.State == WorkflowNodeCompleted || node.State == WorkflowNodeFailed) && (node.ActorFQN == nil || node.InvocationID == nil || node.Attempt == 0) {
			return ErrInvalidWorkflowInstance
		}
		if (node.State == WorkflowNodePending || node.State == WorkflowNodeReady) && (node.ActorFQN != nil || node.InvocationID != nil || node.Attempt != 0) {
			return ErrInvalidWorkflowInstance
		}
		if node.State == WorkflowNodeReady && !predecessorsSatisfied(definition, instance, node) || node.State == WorkflowNodePending && predecessorsSatisfied(definition, instance, node) {
			return ErrInvalidWorkflowInstance
		}
		if node.State == WorkflowNodeFailed && (node.ProgressDigest == nil || node.FailureClassification == WorkflowFailureNone || node.FailureClassification == WorkflowFailureCancelled) {
			return ErrInvalidWorkflowInstance
		}
		if node.State == WorkflowNodeCancelled && node.FailureClassification != WorkflowFailureCancelled {
			return ErrInvalidWorkflowInstance
		}
	}
	if !ValidateDAG(nodeNames, edges).Valid {
		return ErrInvalidWorkflowInstance
	}
	if instance.State == WorkflowCompleted && completedNodes != len(instance.Nodes) || completedNodes == len(instance.Nodes) && instance.State != WorkflowCompleted {
		return ErrInvalidWorkflowInstance
	}
	if instance.State == WorkflowFailed && failedNodes == 0 || instance.State == WorkflowCancelled && completedNodes+cancelledNodes != len(instance.Nodes) || instance.State == WorkflowBlocked && blockedNodes == 0 {
		return ErrInvalidWorkflowInstance
	}
	return nil
}

func (instance WorkflowInstance) Clone() WorkflowInstance {
	copy := instance
	copy.Nodes = make([]WorkflowNode, len(instance.Nodes))
	for index, node := range instance.Nodes {
		copy.Nodes[index] = cloneWorkflowNode(node)
	}
	copy.ProposedMessageIDs = slices.Clone(instance.ProposedMessageIDs)
	copy.AcceptedMessageIDs = slices.Clone(instance.AcceptedMessageIDs)
	if instance.LastEventID != nil {
		value := *instance.LastEventID
		copy.LastEventID = &value
	}
	return copy
}

func cloneWorkflowNode(node WorkflowNode) WorkflowNode {
	copy := node
	copy.PredecessorNodeIDs = slices.Clone(node.PredecessorNodeIDs)
	copy.InputEvidenceIDs = slices.Clone(node.InputEvidenceIDs)
	copy.OutputEvidenceIDs = slices.Clone(node.OutputEvidenceIDs)
	if node.ActorFQN != nil {
		value := *node.ActorFQN
		copy.ActorFQN = &value
	}
	if node.InvocationID != nil {
		value := *node.InvocationID
		copy.InvocationID = &value
	}
	if node.ProgressDigest != nil {
		value := *node.ProgressDigest
		copy.ProgressDigest = &value
	}
	if node.CheckpointID != nil {
		value := *node.CheckpointID
		copy.CheckpointID = &value
	}
	if node.ContinuationNodeID != nil {
		value := *node.ContinuationNodeID
		copy.ContinuationNodeID = &value
	}
	if node.BlockedFrom != nil {
		value := *node.BlockedFrom
		copy.BlockedFrom = &value
	}
	return copy
}

func (instance WorkflowInstance) ReadyNodes() []WorkflowNode {
	ready := make([]WorkflowNode, 0)
	for _, node := range instance.Nodes {
		if node.State == WorkflowNodeReady {
			ready = append(ready, cloneWorkflowNode(node))
		}
	}
	return ready
}

// AdmissibleReadyNodes returns the current parallel frontier after accounting
// for already admitted/running work in each declared concurrency group.
func (instance WorkflowInstance) AdmissibleReadyNodes(definition WorkflowDefinition) []WorkflowNode {
	if instance.Validate(definition) != nil {
		return nil
	}
	activeByGroup := make(map[string]uint32)
	for _, node := range instance.Nodes {
		if node.State != WorkflowNodeAdmitted && node.State != WorkflowNodeRunning {
			continue
		}
		stage := workflowStageByID(definition, node.StageID)
		if stage != nil {
			activeByGroup[stage.ConcurrencyGroup]++
		}
	}
	selectedByGroup := make(map[string]uint32)
	result := make([]WorkflowNode, 0)
	for _, node := range instance.Nodes {
		if node.State != WorkflowNodeReady {
			continue
		}
		stage := workflowStageByID(definition, node.StageID)
		if stage == nil || activeByGroup[stage.ConcurrencyGroup]+selectedByGroup[stage.ConcurrencyGroup] >= stage.MaximumParallelism {
			continue
		}
		result = append(result, cloneWorkflowNode(node))
		selectedByGroup[stage.ConcurrencyGroup]++
	}
	return result
}

type WorkflowNodeSpec struct {
	NodeID             UUIDv7
	PredecessorNodeIDs []UUIDv7
	InputEvidenceIDs   []UUIDv7
}

// ExpandWorkflowNode replaces one untouched stage placeholder with a concrete
// node DAG. This is how a planning result materializes variable work without
// changing the workflow definition or teaching the runtime domain-specific
// task names.
func ExpandWorkflowNode(definition WorkflowDefinition, current WorkflowInstance, placeholderID, eventID UUIDv7, specs []WorkflowNodeSpec) (WorkflowInstance, error) {
	if current.Validate(definition) != nil || !placeholderID.Valid() || !eventID.Valid() || len(specs) == 0 || len(current.Nodes)-1+len(specs) > 4096 {
		return WorkflowInstance{}, ErrInvalidWorkflowTransition
	}
	placeholder := workflowNodeByID(&current, placeholderID)
	if placeholder == nil || placeholder.State != WorkflowNodePending && placeholder.State != WorkflowNodeReady {
		return WorkflowInstance{}, ErrInvalidWorkflowTransition
	}
	replacementIDs := make([]UUIDv7, len(specs))
	for index, spec := range specs {
		if !spec.NodeID.Valid() || !workflowValidUUIDSet(spec.PredecessorNodeIDs) || !workflowValidUUIDSet(spec.InputEvidenceIDs) || slices.Contains(replacementIDs[:index], spec.NodeID) {
			return WorkflowInstance{}, ErrInvalidWorkflowTransition
		}
		replacementIDs[index] = spec.NodeID
	}
	next := current.Clone()
	nodes := make([]WorkflowNode, 0, len(current.Nodes)-1+len(specs))
	for _, node := range next.Nodes {
		if node.NodeID == placeholderID {
			for _, spec := range specs {
				nodes = append(nodes, WorkflowNode{NodeID: spec.NodeID, StageID: placeholder.StageID, PredecessorNodeIDs: slices.Clone(spec.PredecessorNodeIDs), State: WorkflowNodePending, InputEvidenceIDs: slices.Clone(spec.InputEvidenceIDs), OutputEvidenceIDs: []UUIDv7{}, FailureClassification: WorkflowFailureNone})
			}
			continue
		}
		if slices.Contains(node.PredecessorNodeIDs, placeholderID) {
			updated := make([]UUIDv7, 0, len(node.PredecessorNodeIDs)-1+len(replacementIDs))
			for _, predecessor := range node.PredecessorNodeIDs {
				if predecessor == placeholderID {
					updated = append(updated, replacementIDs...)
				} else {
					updated = append(updated, predecessor)
				}
			}
			node.PredecessorNodeIDs = updated
		}
		nodes = append(nodes, node)
	}
	next.Nodes = nodes
	for index := range next.Nodes {
		if next.Nodes[index].State == WorkflowNodePending && predecessorsSatisfied(definition, next, next.Nodes[index]) {
			next.Nodes[index].State = WorkflowNodeReady
		}
	}
	next.Revision++
	lastEvent := eventID
	next.LastEventID = &lastEvent
	if next.Validate(definition) != nil {
		return WorkflowInstance{}, ErrInvalidWorkflowTransition
	}
	return next, nil
}

type WorkflowTransitionKind string

const (
	WorkflowTransitionAdmit      WorkflowTransitionKind = "ADMIT"
	WorkflowTransitionStart      WorkflowTransitionKind = "START"
	WorkflowTransitionComplete   WorkflowTransitionKind = "COMPLETE"
	WorkflowTransitionFail       WorkflowTransitionKind = "FAIL"
	WorkflowTransitionBlock      WorkflowTransitionKind = "BLOCK"
	WorkflowTransitionReady      WorkflowTransitionKind = "READY"
	WorkflowTransitionCancel     WorkflowTransitionKind = "CANCEL"
	WorkflowTransitionCheckpoint WorkflowTransitionKind = "CHECKPOINT"
)

type WorkflowTransition struct {
	Revision              uint64
	EventID               UUIDv7
	Kind                  WorkflowTransitionKind
	NodeID                UUIDv7
	ActorFQN              ActorFQN
	InvocationID          UUIDv7
	EvidenceIDs           []UUIDv7
	ProgressDigest        Digest
	CheckpointID          UUIDv7
	FailureClassification WorkflowFailureClassification
	RecordedAt            time.Time
}

func FoldWorkflowInstance(definition WorkflowDefinition, initial WorkflowInstance, transitions []WorkflowTransition) (WorkflowInstance, error) {
	if initial.Validate(definition) != nil {
		return WorkflowInstance{}, ErrInvalidWorkflowInstance
	}
	state := initial.Clone()
	for _, transition := range transitions {
		if transition.Revision != state.Revision+1 || !transition.EventID.Valid() || transition.RecordedAt.IsZero() {
			return WorkflowInstance{}, ErrInvalidWorkflowTransition
		}
		if err := applyWorkflowTransition(definition, &state, transition); err != nil {
			return WorkflowInstance{}, err
		}
		state.Revision = transition.Revision
		eventID := transition.EventID
		state.LastEventID = &eventID
	}
	if state.Validate(definition) != nil {
		return WorkflowInstance{}, ErrInvalidWorkflowInstance
	}
	return state, nil
}

func applyWorkflowTransition(definition WorkflowDefinition, instance *WorkflowInstance, transition WorkflowTransition) error {
	if instance.State.terminal() {
		return ErrInvalidWorkflowTransition
	}
	if transition.Kind == WorkflowTransitionCancel {
		for index := range instance.Nodes {
			if instance.Nodes[index].State != WorkflowNodeCompleted {
				instance.Nodes[index].State = WorkflowNodeCancelled
				instance.Nodes[index].FailureClassification = WorkflowFailureCancelled
			}
		}
		instance.State = WorkflowCancelled
		return nil
	}
	node := workflowNodeByID(instance, transition.NodeID)
	if node == nil {
		return ErrInvalidWorkflowTransition
	}
	switch transition.Kind {
	case WorkflowTransitionAdmit:
		if node.State != WorkflowNodeReady || !transition.ActorFQN.Valid() || !transition.InvocationID.Valid() {
			return ErrInvalidWorkflowTransition
		}
		node.State = WorkflowNodeAdmitted
		node.Attempt++
		actor := transition.ActorFQN
		invocation := transition.InvocationID
		node.ActorFQN = &actor
		node.InvocationID = &invocation
	case WorkflowTransitionStart:
		if node.State != WorkflowNodeAdmitted || node.InvocationID == nil || *node.InvocationID != transition.InvocationID {
			return ErrInvalidWorkflowTransition
		}
		node.State = WorkflowNodeRunning
	case WorkflowTransitionComplete:
		if node.State != WorkflowNodeRunning || node.InvocationID == nil || *node.InvocationID != transition.InvocationID || !transition.ProgressDigest.Valid() || !workflowValidUUIDSet(transition.EvidenceIDs) {
			return ErrInvalidWorkflowTransition
		}
		node.State = WorkflowNodeCompleted
		node.OutputEvidenceIDs = slices.Clone(transition.EvidenceIDs)
		digest := transition.ProgressDigest
		node.ProgressDigest = &digest
		node.FailureClassification = WorkflowFailureNone
		refreshWorkflowReadiness(definition, instance)
	case WorkflowTransitionFail:
		if node.State != WorkflowNodeRunning || node.InvocationID == nil || *node.InvocationID != transition.InvocationID || !transition.ProgressDigest.Valid() || !transition.FailureClassification.Valid() || transition.FailureClassification == WorkflowFailureNone {
			return ErrInvalidWorkflowTransition
		}
		node.State = WorkflowNodeFailed
		digest := transition.ProgressDigest
		node.ProgressDigest = &digest
		node.FailureClassification = transition.FailureClassification
		if transition.FailureClassification == WorkflowFailureTerminalPolicy {
			instance.State = WorkflowFailed
		} else {
			refreshWorkflowReadiness(definition, instance)
		}
	case WorkflowTransitionBlock:
		if node.State != WorkflowNodeReady && node.State != WorkflowNodeAdmitted && node.State != WorkflowNodeRunning {
			return ErrInvalidWorkflowTransition
		}
		previous := node.State
		node.BlockedFrom = &previous
		node.State = WorkflowNodeBlocked
		instance.State = WorkflowBlocked
	case WorkflowTransitionReady:
		if node.State != WorkflowNodeBlocked || node.BlockedFrom == nil || !predecessorsSatisfied(definition, *instance, *node) {
			return ErrInvalidWorkflowTransition
		}
		node.State = *node.BlockedFrom
		node.BlockedFrom = nil
		instance.State = WorkflowActive
	case WorkflowTransitionCheckpoint:
		if node.State != WorkflowNodeAdmitted && node.State != WorkflowNodeRunning && node.State != WorkflowNodeBlocked || !transition.CheckpointID.Valid() {
			return ErrInvalidWorkflowTransition
		}
		checkpoint := transition.CheckpointID
		node.CheckpointID = &checkpoint
	default:
		return ErrInvalidWorkflowTransition
	}
	return nil
}

func refreshWorkflowReadiness(definition WorkflowDefinition, instance *WorkflowInstance) {
	completed := 0
	for index := range instance.Nodes {
		node := &instance.Nodes[index]
		if node.State == WorkflowNodeCompleted {
			completed++
			continue
		}
		if node.State == WorkflowNodePending && predecessorsSatisfied(definition, *instance, *node) {
			node.State = WorkflowNodeReady
		}
	}
	if completed == len(instance.Nodes) {
		instance.State = WorkflowCompleted
	}
}

func predecessorsCompleted(instance WorkflowInstance, node WorkflowNode) bool {
	for _, predecessorID := range node.PredecessorNodeIDs {
		predecessor := workflowNodeByID(&instance, predecessorID)
		if predecessor == nil || predecessor.State != WorkflowNodeCompleted {
			return false
		}
	}
	return true
}

func predecessorsSatisfied(definition WorkflowDefinition, instance WorkflowInstance, node WorkflowNode) bool {
	stage := workflowStageByID(definition, node.StageID)
	if stage == nil || (stage.Purpose != WorkflowPurposeRepair && stage.Purpose != WorkflowPurposeReplan) {
		return predecessorsCompleted(instance, node)
	}
	if len(node.PredecessorNodeIDs) == 0 {
		return false
	}
	failed := false
	for _, predecessor := range node.PredecessorNodeIDs {
		candidate := workflowNodeByID(&instance, predecessor)
		if candidate == nil || candidate.State != WorkflowNodeCompleted && candidate.State != WorkflowNodeFailed {
			return false
		}
		failed = failed || candidate.State == WorkflowNodeFailed
	}
	return failed
}

func workflowNodeByID(instance *WorkflowInstance, id UUIDv7) *WorkflowNode {
	for index := range instance.Nodes {
		if instance.Nodes[index].NodeID == id {
			return &instance.Nodes[index]
		}
	}
	return nil
}

func workflowValidUUIDSet(values []UUIDv7) bool {
	seen := make(map[UUIDv7]struct{}, len(values))
	for _, value := range values {
		if !value.Valid() {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}
