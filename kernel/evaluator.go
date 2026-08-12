package kernel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

const (
	reasonApplied            = "APPLIED"
	reasonInvalidEnvelope    = "INVALID_ENVELOPE"
	reasonInvalidPayload     = "INVALID_PAYLOAD"
	reasonUnknownCommand     = "UNKNOWN_COMMAND_TYPE"
	reasonUnsupportedVersion = "UNSUPPORTED_COMMAND_VERSION"
	reasonInvalidDAG         = "INVALID_DAG_PARENT"
	reasonInvalidEvidence    = "INVALID_EVIDENCE_REFERENCE"
	reasonUnauthorized       = "UNAUTHORIZED"
	reasonNotFound           = "TARGET_NOT_FOUND"
	reasonAlreadyExists      = "TARGET_ALREADY_EXISTS"
	reasonRevisionConflict   = "REVISION_CONFLICT"
	reasonLifecycleConflict  = "LIFECYCLE_EPOCH_CONFLICT"
	reasonPolicyConflict     = "POLICY_REVISION_CONFLICT"
	reasonCatalogueConflict  = "CATALOGUE_REVISION_CONFLICT"
	reasonClosed             = "TARGET_CLOSED"
	reasonPolicy             = "TRANSITION_POLICY"
	reasonStaleExecution     = "STALE_EXECUTION"
)

type Evaluator struct {
	Catalogue CatalogueSnapshot
}

var (
	ErrCatalogueUnavailable   = errors.New("catalogue snapshot unavailable")
	ErrInvalidDecisionContext = errors.New("invalid decision context")
)

func (e Evaluator) Evaluate(command KernelCommand, snapshot Snapshot, context DecisionContext) (Decision, error) {
	if e.Catalogue == nil {
		return Decision{}, ErrCatalogueUnavailable
	}
	if err := validateDecisionContext(context); err != nil {
		return Decision{}, fmt.Errorf("%w: %v", ErrInvalidDecisionContext, err)
	}
	fingerprint, err := CommandFingerprint(command)
	if err != nil {
		return Decision{}, fmt.Errorf("fingerprint command: %w", err)
	}
	context.Provenance.GrantDigests = nil
	context.Provenance.DelegationDigests = nil
	provenance, provenanceDigest, err := BuildDecisionProvenance(command, fingerprint, context)
	if err != nil {
		return Decision{}, fmt.Errorf("build decision provenance: %w", err)
	}
	context.ProvenanceDigest = provenanceDigest
	idempotencyScope, err := IdempotencyScopeDigest(command)
	if err != nil {
		return Decision{}, fmt.Errorf("fingerprint idempotency scope: %w", err)
	}
	if err := validateEnvelope(command); err != nil {
		return rejectedDecision(command, context, fingerprint, OutcomeRejectedInvalid, reasonInvalidEnvelope), nil
	}
	definition, err := e.Catalogue.ResolveCommand(command.CommandType, command.CommandVersion, command.Target.Kind, command.Payload)
	if err != nil {
		reason := reasonInvalidPayload
		if errors.Is(err, ErrUnknownCommand) {
			reason = reasonUnknownCommand
		} else if errors.Is(err, ErrUnsupportedVersion) {
			reason = reasonUnsupportedVersion
		}
		return rejectedDecision(command, context, fingerprint, OutcomeRejectedInvalid, reason), nil
	}
	if !containsPrincipalKind(definition.AuthorityKinds, command.Authority.Kind) {
		return rejectedDecision(command, context, fingerprint, OutcomeRejectedUnauthorized, reasonUnauthorized), nil
	}
	if command.ExpectedCatalogueRevision != e.Catalogue.Revision() {
		return rejectedDecision(command, context, fingerprint, OutcomeRejectedConflict, reasonCatalogueConflict), nil
	}
	authorization := snapshot.Authorization.Authorize(command, snapshot.State, context.DecidedAt)
	context.Provenance.GrantDigests = append([]Digest(nil), authorization.GrantDigests...)
	context.Provenance.DelegationDigests = append([]Digest(nil), authorization.DelegationDigests...)
	provenance, provenanceDigest, err = BuildDecisionProvenance(command, fingerprint, context)
	if err != nil {
		return Decision{}, fmt.Errorf("build authorized decision provenance: %w", err)
	}
	context.ProvenanceDigest = provenanceDigest
	authorityDecision := AuthorityDecision{
		Principal: command.Authority, Allowed: authorization.Allowed, CanReadTarget: authorization.CanReadTarget,
		Reason: authorization.ReasonCode, PolicyDigest: snapshot.Authorization.PolicyDigest,
		PolicyRevision: snapshot.Authorization.Revision, GrantDigests: append([]Digest(nil), authorization.GrantDigests...),
		DelegationDigests: append([]Digest(nil), authorization.DelegationDigests...),
	}
	if command.ExpectedPolicyRevision != snapshot.Authorization.Revision || snapshot.Authorization.PolicyDigest != context.Provenance.PolicyDigest || snapshot.Authorization.Revision != context.Provenance.PolicyRevision {
		return rejectedAuthorizedDecision(command, context, fingerprint, OutcomeRejectedConflict, reasonPolicyConflict, authorityDecision), nil
	}
	if !authorization.Allowed {
		return rejectedAuthorizedDecision(command, context, fingerprint, OutcomeRejectedUnauthorized, reasonUnauthorized, authorityDecision), nil
	}
	if !actorAttributionValid(command, snapshot, definition.ExecutionRequired) {
		return rejectedAuthorizedDecision(command, context, fingerprint, OutcomeRejectedStaleExecution, reasonStaleExecution, authorityDecision), nil
	}
	if commandCreatesAggregate(command.CommandType) != command.ExpectedRevision.MustNotExist {
		return rejectedAuthorizedDecision(command, context, fingerprint, OutcomeRejectedConflict, reasonRevisionConflict, authorityDecision), nil
	}
	if command.ExpectedRevision.MustNotExist {
		if snapshot.Exists {
			if !authorization.CanReadTarget {
				return rejectedAuthorizedDecision(command, context, fingerprint, OutcomeRejectedUnauthorized, reasonUnauthorized, authorityDecision), nil
			}
			return rejectedAuthorizedDecision(command, context, fingerprint, OutcomeRejectedConflict, reasonAlreadyExists, authorityDecision), nil
		}
	} else {
		if !snapshot.Exists {
			if !authorization.CanReadTarget {
				return rejectedAuthorizedDecision(command, context, fingerprint, OutcomeRejectedUnauthorized, reasonUnauthorized, authorityDecision), nil
			}
			return rejectedAuthorizedDecision(command, context, fingerprint, OutcomeRejectedNotFound, reasonNotFound, authorityDecision), nil
		}
		if snapshot.Revision != command.ExpectedRevision.Revision {
			if !authorization.CanReadTarget {
				return rejectedAuthorizedDecision(command, context, fingerprint, OutcomeRejectedUnauthorized, reasonUnauthorized, authorityDecision), nil
			}
			return rejectedAuthorizedDecision(command, context, fingerprint, OutcomeRejectedConflict, reasonRevisionConflict, authorityDecision), nil
		}
	}
	if !expectedLifecycleEpochMatches(command, snapshot) {
		if !authorization.CanReadTarget {
			return rejectedAuthorizedDecision(command, context, fingerprint, OutcomeRejectedUnauthorized, reasonUnauthorized, authorityDecision), nil
		}
		return rejectedAuthorizedDecision(command, context, fingerprint, OutcomeRejectedConflict, reasonLifecycleConflict, authorityDecision), nil
	}
	if !preconditionsMatch(command.Preconditions, snapshot.Related) {
		if !authorization.CanReadTarget {
			return rejectedAuthorizedDecision(command, context, fingerprint, OutcomeRejectedUnauthorized, reasonUnauthorized, authorityDecision), nil
		}
		return rejectedAuthorizedDecision(command, context, fingerprint, OutcomeRejectedConflict, reasonRevisionConflict, authorityDecision), nil
	}
	if outcome, reason := validateExecutionRegistryCommand(command, snapshot); outcome != OutcomeApplied {
		return rejectedAuthorizedDecision(command, context, fingerprint, outcome, reason, authorityDecision), nil
	}
	if !validDAGParents(command, definition, snapshot) {
		return rejectedAuthorizedDecision(command, context, fingerprint, OutcomeRejectedInvalid, reasonInvalidDAG, authorityDecision), nil
	}
	if !validEvidenceRefs(command, snapshot) {
		return rejectedAuthorizedDecision(command, context, fingerprint, OutcomeRejectedInvalid, reasonInvalidEvidence, authorityDecision), nil
	}
	if outcome, reason := validateCommandPolicy(command, snapshot, context); outcome != OutcomeApplied {
		return rejectedAuthorizedDecision(command, context, fingerprint, outcome, reason, authorityDecision), nil
	}
	attemptBudget, outcome, reason := evaluateAttemptPolicy(command, snapshot)
	if outcome != OutcomeApplied {
		return rejectedAuthorizedDecision(command, context, fingerprint, outcome, reason, authorityDecision), nil
	}

	nextState, nextRevision, outcome, reason := evolveState(command, snapshot, context.EventID)
	if outcome != OutcomeApplied {
		return rejectedAuthorizedDecision(command, context, fingerprint, outcome, reason, authorityDecision), nil
	}
	if len(definition.EventTypes) != 1 {
		return rejectedAuthorizedDecision(command, context, fingerprint, OutcomeRejectedInvalid, reasonInvalidEnvelope, authorityDecision), nil
	}
	lifecycleEpoch := uint64(1)
	if nextState != nil {
		lifecycleEpoch = nextState.LifecycleEpoch
	}
	payload := append(json.RawMessage(nil), command.Payload...)
	event := DomainEvent{
		ContractManifest:  ContractIdentity,
		EventID:           context.EventID,
		EventType:         definition.EventTypes[0],
		EventVersion:      SchemaVersion,
		Aggregate:         command.Target,
		AggregateRevision: nextRevision,
		LifecycleEpoch:    lifecycleEpoch,
		CommandID:         command.CommandID,
		Authority:         command.Authority,
		ActorFQN:          cloneActor(command.ActorFQN),
		Execution:         cloneExecution(command.Execution),
		Parents:           canonicalParents(command.Causation),
		CommittedAt:       context.DecidedAt,
		Payload:           payload,
		ProvenanceDigest:  context.ProvenanceDigest,
	}
	revision := nextRevision
	receipt := receiptFor(command, context, OutcomeApplied, reasonApplied, true, &revision, []UUIDv7{context.EventID})
	return Decision{
		CommandFingerprint: fingerprint,
		IdempotencyScope:   idempotencyScope,
		NextState:          nextState,
		Events:             []DomainEvent{event},
		Receipt:            receipt,
		Authority:          authorityDecision,
		Outbox:             []OutboxIntent{{IntentID: context.IntentID, EventID: context.EventID, Kind: "DOMAIN_EVENT"}},
		Guards:             decisionGuards(command, authorityDecision),
		Provenance:         provenance,
		AttemptBudget:      attemptBudget,
	}, nil
}

func decisionGuards(command KernelCommand, authority AuthorityDecision) DecisionGuards {
	guards := DecisionGuards{
		ParentIDs:      make([]UUIDv7, len(command.Causation)),
		EvidenceRefs:   append([]EvidenceRef(nil), command.EvidenceRefs...),
		Preconditions:  append([]AggregatePrecondition(nil), command.Preconditions...),
		PolicyDigest:   authority.PolicyDigest,
		PolicyRevision: authority.PolicyRevision,
	}
	for index, parent := range canonicalParents(command.Causation) {
		guards.ParentIDs[index] = parent.ParentEventID
	}
	if command.ActorFQN != nil && command.Execution != nil {
		guards.Executions = map[ActorFQN]ExecutionTuple{*command.ActorFQN: *command.Execution}
	}
	if actor, prior, registered, ok := executionRegistryExpectation(command); ok {
		if registered {
			guards.AbsentExecutions = []ActorFQN{actor}
		} else {
			if guards.Executions == nil {
				guards.Executions = make(map[ActorFQN]ExecutionTuple)
			}
			guards.Executions[actor] = prior
		}
	}
	if command.CommandType == "tekroo.command.completion-review.open" {
		if key, err := CompletionReviewKeyFromPayload(command.Payload); err == nil {
			guards.AbsentReviewKeys = []CompletionReviewKey{key}
		}
	}
	if command.CommandType == "tekroo.command.escalation.open" {
		if escalation, err := EscalationFromOpenPayload(command.Payload); err == nil {
			guards.AbsentEscalationKeys = []EscalationKey{escalation.Key()}
		}
	}
	if command.CommandType == "tekroo.command.release-plan.create" {
		if key, err := ReleasePlanKeyFromCreatePayload(command.Payload); err == nil {
			guards.AbsentReleaseKeys = []ReleasePlanKey{key}
		}
	}
	return guards
}

func validateExecutionRegistryCommand(command KernelCommand, snapshot Snapshot) (OutcomeCode, string) {
	actor, prior, registering, ok := executionRegistryExpectation(command)
	if !ok {
		return OutcomeApplied, reasonApplied
	}
	current, exists := snapshot.CurrentExecutions[actor]
	if registering {
		if exists {
			return OutcomeRejectedConflict, reasonRevisionConflict
		}
		return OutcomeApplied, reasonApplied
	}
	if !exists || current != prior {
		return OutcomeRejectedStaleExecution, reasonStaleExecution
	}
	return OutcomeApplied, reasonApplied
}

func executionRegistryExpectation(command KernelCommand) (ActorFQN, ExecutionTuple, bool, bool) {
	if command.CommandType != "tekroo.command.execution.register" && command.CommandType != "tekroo.command.execution.replace" {
		return "", ExecutionTuple{}, false, false
	}
	object, err := decodePayloadObject(command.Payload)
	if err != nil {
		return "", ExecutionTuple{}, false, false
	}
	actor, err := ParseActorFQN(fmt.Sprint(object["actor_fqn"]))
	if err != nil {
		return "", ExecutionTuple{}, false, false
	}
	if command.CommandType == "tekroo.command.execution.register" {
		return actor, ExecutionTuple{}, true, true
	}
	priorID, err := ParseUUIDv7(fmt.Sprint(object["prior_execution_id"]))
	if err != nil {
		return "", ExecutionTuple{}, false, false
	}
	number, ok := object["new_fencing_epoch"].(json.Number)
	if !ok {
		return "", ExecutionTuple{}, false, false
	}
	newEpoch, err := number.Int64()
	if err != nil || newEpoch < 2 {
		return "", ExecutionTuple{}, false, false
	}
	return actor, ExecutionTuple{ExecutionID: priorID, FencingEpoch: uint64(newEpoch - 1)}, false, true
}

func validateEnvelope(command KernelCommand) error {
	if command.ContractManifest != ContractIdentity || command.CommandType == "" || command.CommandVersion == "" {
		return errors.New("contract or command identity mismatch")
	}
	if !command.CommandID.Valid() || !command.Target.Valid() || !command.Authority.Valid() {
		return errors.New("invalid identity")
	}
	if command.ActorFQN != nil && !command.ActorFQN.Valid() {
		return errors.New("invalid actor FQN")
	}
	if command.Execution != nil && !command.Execution.Valid() {
		return errors.New("invalid execution tuple")
	}
	if command.ExpectedRevision.MustNotExist == (command.ExpectedRevision.Revision > 0) {
		return errors.New("invalid expected revision")
	}
	if command.ExpectedPolicyRevision == 0 || command.ExpectedCatalogueRevision == 0 {
		return errors.New("missing expected policy or catalogue revision")
	}
	if command.ExpectedLifecycleEpoch != nil && *command.ExpectedLifecycleEpoch == 0 {
		return errors.New("invalid expected lifecycle epoch")
	}
	if len(command.IdempotencyKey) < 1 || len(command.IdempotencyKey) > 256 || !command.CorrelationID.Valid() {
		return errors.New("invalid idempotency or correlation identity")
	}
	if len(command.Causation) > 64 || len(command.EvidenceRefs) > 64 || len(command.Preconditions) > 64 || len(command.Payload) == 0 {
		return errors.New("envelope bounds exceeded")
	}
	if !validPreconditionVector(command.Target, command.Preconditions) {
		return errors.New("invalid aggregate preconditions")
	}
	seenParents := make(map[DagParent]struct{}, len(command.Causation))
	for _, parent := range command.Causation {
		if !parent.ParentEventID.Valid() || !parent.EdgeKind.Valid() {
			return errors.New("invalid causal parent")
		}
		if _, duplicate := seenParents[parent]; duplicate {
			return errors.New("duplicate causal parent")
		}
		seenParents[parent] = struct{}{}
	}
	seenEvidence := make(map[EvidenceRef]struct{}, len(command.EvidenceRefs))
	for _, evidence := range command.EvidenceRefs {
		if !evidence.EvidenceID.Valid() || !evidence.SHA256.Valid() {
			return errors.New("invalid evidence reference")
		}
		if _, duplicate := seenEvidence[evidence]; duplicate {
			return errors.New("duplicate evidence reference")
		}
		seenEvidence[evidence] = struct{}{}
	}
	return nil
}

func expectedLifecycleEpochMatches(command KernelCommand, snapshot Snapshot) bool {
	if !commandRequiresLifecycleEpoch(command) {
		return command.ExpectedLifecycleEpoch == nil
	}
	if command.ExpectedRevision.MustNotExist || command.ExpectedLifecycleEpoch == nil || snapshot.State == nil {
		return false
	}
	return *command.ExpectedLifecycleEpoch == snapshot.State.LifecycleEpoch
}

func commandRequiresLifecycleEpoch(command KernelCommand) bool {
	if command.CommandType == "tekroo.command.story.create" || command.CommandType == "tekroo.command.task.create" || command.CommandType == "tekroo.command.record.correct" {
		return false
	}
	return command.Target.Kind == AggregateStory || command.Target.Kind == AggregateTask
}

func validPreconditionVector(target AggregateRef, values []AggregatePrecondition) bool {
	canonical := canonicalPreconditions(values)
	for index, value := range values {
		if !value.Aggregate.Valid() || value.Aggregate == target || value.Expected.MustNotExist == (value.Expected.Revision > 0) || value != canonical[index] {
			return false
		}
		if index > 0 && values[index-1].Aggregate == value.Aggregate {
			return false
		}
	}
	return true
}

func canonicalPreconditions(values []AggregatePrecondition) []AggregatePrecondition {
	preconditions := append([]AggregatePrecondition(nil), values...)
	sort.Slice(preconditions, func(i, j int) bool {
		if preconditions[i].Aggregate.Kind != preconditions[j].Aggregate.Kind {
			return preconditions[i].Aggregate.Kind < preconditions[j].Aggregate.Kind
		}
		return preconditions[i].Aggregate.ID < preconditions[j].Aggregate.ID
	})
	return preconditions
}

func preconditionsMatch(values []AggregatePrecondition, related map[AggregateRef]RelatedSnapshot) bool {
	for _, value := range values {
		current, found := related[value.Aggregate]
		if value.Expected.MustNotExist {
			if found && current.Exists {
				return false
			}
			continue
		}
		if !found || !current.Exists || current.Revision != value.Expected.Revision {
			return false
		}
	}
	return true
}

func actorAttributionValid(command KernelCommand, snapshot Snapshot, required bool) bool {
	if command.Authority.Kind == PrincipalActor {
		if command.ActorFQN == nil || command.Authority.ID != string(*command.ActorFQN) {
			return false
		}
	}
	if required && (command.ActorFQN == nil || command.Execution == nil) {
		return false
	}
	if command.Execution == nil {
		return !required
	}
	if command.ActorFQN == nil {
		return false
	}
	current, found := snapshot.CurrentExecutions[*command.ActorFQN]
	return found && current == *command.Execution
}

func validDAGParents(command KernelCommand, definition CommandDefinition, snapshot Snapshot) bool {
	if len(command.Causation) == 0 {
		return definition.RootAllowed
	}
	allowed := make(map[EdgeKind]struct{}, len(definition.AllowedParentEdges))
	for _, kind := range definition.AllowedParentEdges {
		allowed[kind] = struct{}{}
	}
	for _, parent := range command.Causation {
		if _, ok := allowed[parent.EdgeKind]; !ok {
			return false
		}
		accepted, ok := snapshot.AcceptedEvents[parent.ParentEventID]
		if !ok || accepted.Quarantined || accepted.EventType == "" {
			return false
		}
	}
	return true
}

func validEvidenceRefs(command KernelCommand, snapshot Snapshot) bool {
	for _, reference := range command.EvidenceRefs {
		metadata, ok := snapshot.Evidence[reference.EvidenceID]
		if !ok || !metadata.Available || metadata.SHA256 != reference.SHA256 {
			return false
		}
	}
	return true
}

func canonicalParents(values []DagParent) []DagParent {
	parents := append([]DagParent(nil), values...)
	sort.Slice(parents, func(i, j int) bool {
		if parents[i].EdgeKind != parents[j].EdgeKind {
			return parents[i].EdgeKind < parents[j].EdgeKind
		}
		return parents[i].ParentEventID < parents[j].ParentEventID
	})
	return parents
}

func validateDecisionContext(context DecisionContext) error {
	if context.ReceivedAt.IsZero() || context.DecidedAt.IsZero() || context.DecidedAt.Before(context.ReceivedAt) {
		return errors.New("invalid decision time")
	}
	if !context.EventID.Valid() || !context.IntentID.Valid() || !context.Provenance.Valid() {
		return errors.New("invalid decision identity")
	}
	return nil
}

func evolveState(command KernelCommand, snapshot Snapshot, eventID UUIDv7) (*AggregateState, uint64, OutcomeCode, string) {
	if command.CommandType == "tekroo.command.story.create" || command.CommandType == "tekroo.command.task.create" {
		phase := PhaseDraft
		if command.Target.Kind == AggregateTask {
			phase = PhasePlanned
		}
		state := &AggregateState{
			Kind:           command.Target.Kind,
			ID:             command.Target.ID,
			Revision:       1,
			LifecycleEpoch: 1,
			Phase:          phase,
			Condition:      ConditionRunnable,
			Ownership:      Ownership{},
		}
		return state, 1, OutcomeApplied, reasonApplied
	}

	nextRevision := snapshot.Revision + 1
	if (command.Target.Kind == AggregateStory || command.Target.Kind == AggregateTask) && snapshot.State == nil {
		return nil, 0, OutcomeRejectedConflict, reasonRevisionConflict
	}
	if snapshot.State == nil {
		return nil, nextRevision, OutcomeApplied, reasonApplied
	}
	state := snapshot.State.Clone()
	if state.ID != command.Target.ID || state.Kind != command.Target.Kind || state.Revision != snapshot.Revision {
		return nil, 0, OutcomeRejectedConflict, reasonRevisionConflict
	}
	if terminalCommandProhibited(command.CommandType, state.Phase) {
		return nil, 0, OutcomeRejectedClosed, reasonClosed
	}

	action, hasAction := commandLifecycleAction(command.CommandType)
	if hasAction {
		model := LifecycleStory
		if state.Kind == AggregateTask {
			model = LifecycleTask
		}
		result := ApplyLifecycleActions(model, LifecycleState{
			Phase:          state.Phase,
			Condition:      state.Condition,
			LifecycleEpoch: state.LifecycleEpoch,
		}, []LifecycleAction{action})
		if result.Rejected != nil {
			return nil, 0, OutcomeRejectedPolicy, reasonPolicy
		}
		state.Phase = result.Phase
		state.Condition = result.Condition
		state.LifecycleEpoch = result.LifecycleEpoch
		if command.CommandType == "tekroo.command.work.reopen" {
			object, _ := decodePayloadObject(command.Payload)
			carry, _ := object["owner_carry_forward"].(bool)
			if !carry {
				state.Ownership.OwnerFQN = nil
				state.Ownership.AssignedEventID = nil
			}
		}
	}
	if outcome, reason := applyOwnership(command, &state, eventID); outcome != OutcomeApplied {
		return nil, 0, outcome, reason
	}
	state.Revision = nextRevision
	return &state, nextRevision, OutcomeApplied, reasonApplied
}

func terminalCommandProhibited(commandType string, phase Phase) bool {
	if phase != PhaseCompleted && phase != PhaseAccepted && phase != PhaseClosed {
		return false
	}
	switch commandType {
	case "tekroo.command.work.reopen", "tekroo.command.work.create-successor", "tekroo.command.record.correct":
		return false
	case "tekroo.command.story.request-acceptance":
		return phase != PhaseCompleted
	case "tekroo.command.story.approve-release":
		return phase != PhaseCompleted
	default:
		return true
	}
}

func commandCreatesAggregate(commandType string) bool {
	switch commandType {
	case "tekroo.command.story.create",
		"tekroo.command.task.create",
		"tekroo.command.evidence.register",
		"tekroo.command.execution.register",
		"tekroo.command.completion-review.open",
		"tekroo.command.escalation.open",
		"tekroo.command.release-plan.create":
		return true
	default:
		return false
	}
}

func commandLifecycleAction(commandType string) (LifecycleAction, bool) {
	switch commandType {
	case "tekroo.command.story.authorize":
		return ActionAuthorize, true
	case "tekroo.command.story.begin-planning":
		return ActionBeginPlanning, true
	case "tekroo.command.story.activate", "tekroo.command.task.activate":
		return ActionActivate, true
	case "tekroo.command.story.request-completion", "tekroo.command.task.request-completion":
		return ActionComplete, true
	case "tekroo.command.story.request-acceptance":
		return ActionAccept, true
	case "tekroo.command.task.mark-ready":
		return ActionMarkReady, true
	case "tekroo.command.work.block":
		return ActionBlock, true
	case "tekroo.command.work.unblock":
		return ActionUnblock, true
	case "tekroo.command.work.reopen":
		return ActionReopen, true
	case "tekroo.command.work.close":
		return ActionClose, true
	default:
		return "", false
	}
}

func applyOwnership(command KernelCommand, state *AggregateState, eventID UUIDv7) (OutcomeCode, string) {
	switch command.CommandType {
	case "tekroo.command.task.acquire-ownership":
		payload, err := ownershipPayload(command.Payload, "owner_fqn")
		if err != nil || state.Ownership.OwnerFQN != nil || payload.Version != state.Ownership.OwnershipVersion {
			return OutcomeRejectedConflict, reasonRevisionConflict
		}
		state.Ownership.OwnerFQN = &payload.Owner
		state.Ownership.OwnershipVersion++
		state.Ownership.AssignedEventID = &eventID
	case "tekroo.command.task.release-ownership":
		payload, err := ownershipPayload(command.Payload, "owner_fqn")
		if err != nil || state.Ownership.OwnerFQN == nil || *state.Ownership.OwnerFQN != payload.Owner || payload.Version != state.Ownership.OwnershipVersion {
			return OutcomeRejectedConflict, reasonRevisionConflict
		}
		state.Ownership.OwnerFQN = nil
		state.Ownership.OwnershipVersion++
		state.Ownership.AssignedEventID = nil
	case "tekroo.command.task.handoff", "tekroo.command.task.force-reassign":
		payload, err := reassignmentPayload(command.Payload)
		if err != nil || state.Ownership.OwnerFQN == nil || *state.Ownership.OwnerFQN != payload.Prior || payload.Version != state.Ownership.OwnershipVersion {
			return OutcomeRejectedConflict, reasonRevisionConflict
		}
		state.Ownership.OwnerFQN = &payload.Next
		state.Ownership.OwnershipVersion++
		state.Ownership.AssignedEventID = &eventID
	case "tekroo.command.task.activate":
		payload, err := ownershipPayload(command.Payload, "owner_fqn")
		if err != nil || state.Ownership.OwnerFQN == nil || *state.Ownership.OwnerFQN != payload.Owner || payload.Version != state.Ownership.OwnershipVersion {
			return OutcomeRejectedPolicy, reasonPolicy
		}
	}
	return OutcomeApplied, reasonApplied
}

type parsedOwnership struct {
	Owner   ActorFQN
	Version uint64
}

func ownershipPayload(payload json.RawMessage, ownerField string) (parsedOwnership, error) {
	object, err := decodePayloadObject(payload)
	if err != nil {
		return parsedOwnership{}, err
	}
	ownerText, ok := object[ownerField].(string)
	if !ok {
		return parsedOwnership{}, errors.New("owner is missing")
	}
	owner, err := ParseActorFQN(ownerText)
	if err != nil {
		return parsedOwnership{}, err
	}
	versionName := "expected_ownership_version"
	if ownerField == "owner_fqn" {
		if _, exists := object["ownership_version"]; exists {
			versionName = "ownership_version"
		}
	}
	number, ok := object[versionName].(json.Number)
	if !ok {
		return parsedOwnership{}, errors.New("ownership version is missing")
	}
	version, err := number.Int64()
	if err != nil || version < 0 {
		return parsedOwnership{}, errors.New("ownership version is invalid")
	}
	return parsedOwnership{Owner: owner, Version: uint64(version)}, nil
}

type parsedReassignment struct {
	Prior   ActorFQN
	Next    ActorFQN
	Version uint64
}

func reassignmentPayload(payload json.RawMessage) (parsedReassignment, error) {
	object, err := decodePayloadObject(payload)
	if err != nil {
		return parsedReassignment{}, err
	}
	prior, err := ParseActorFQN(fmt.Sprint(object["prior_owner_fqn"]))
	if err != nil {
		return parsedReassignment{}, err
	}
	next, err := ParseActorFQN(fmt.Sprint(object["new_owner_fqn"]))
	if err != nil {
		return parsedReassignment{}, err
	}
	number, ok := object["expected_ownership_version"].(json.Number)
	if !ok {
		return parsedReassignment{}, errors.New("ownership version is missing")
	}
	version, err := number.Int64()
	if err != nil || version < 0 {
		return parsedReassignment{}, errors.New("ownership version is invalid")
	}
	return parsedReassignment{Prior: prior, Next: next, Version: uint64(version)}, nil
}

func decodePayloadObject(payload json.RawMessage) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		return nil, err
	}
	return object, nil
}

func rejectedDecision(command KernelCommand, context DecisionContext, fingerprint Digest, outcome OutcomeCode, reason string) Decision {
	idempotencyScope, _ := IdempotencyScopeDigest(command)
	provenance, _, _ := BuildDecisionProvenance(command, fingerprint, context)
	return Decision{
		CommandFingerprint: fingerprint,
		IdempotencyScope:   idempotencyScope,
		Receipt:            receiptFor(command, context, outcome, reason, false, nil, nil),
		Authority: AuthorityDecision{
			Principal: command.Authority,
			Allowed:   outcome != OutcomeRejectedUnauthorized,
			Reason:    reason,
		},
		Provenance: provenance,
	}
}

func rejectedAuthorizedDecision(command KernelCommand, context DecisionContext, fingerprint Digest, outcome OutcomeCode, reason string, authority AuthorityDecision) Decision {
	decision := rejectedDecision(command, context, fingerprint, outcome, reason)
	decision.Authority = authority
	decision.Guards = DecisionGuards{
		Preconditions: append([]AggregatePrecondition(nil), command.Preconditions...),
		PolicyDigest:  authority.PolicyDigest, PolicyRevision: authority.PolicyRevision,
	}
	return decision
}

func receiptFor(command KernelCommand, context DecisionContext, outcome OutcomeCode, reason string, changed bool, revision *uint64, eventIDs []UUIDv7) CommandReceipt {
	return CommandReceipt{
		ContractManifest:  ContractIdentity,
		CommandID:         command.CommandID,
		CommandType:       command.CommandType,
		Target:            command.Target,
		OutcomeCode:       outcome,
		ReasonCode:        reason,
		StateChanged:      changed,
		ResultingRevision: revision,
		EventIDs:          append([]UUIDv7(nil), eventIDs...),
		ReceivedAt:        context.ReceivedAt,
		DecidedAt:         context.DecidedAt,
		ProvenanceDigest:  context.ProvenanceDigest,
	}
}

func containsPrincipalKind(values []PrincipalKind, target PrincipalKind) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func cloneActor(value *ActorFQN) *ActorFQN {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneExecution(value *ExecutionTuple) *ExecutionTuple {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
