package kernel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

const (
	reasonApplied          = "APPLIED"
	reasonInvalidEnvelope  = "INVALID_ENVELOPE"
	reasonInvalidPayload   = "INVALID_PAYLOAD"
	reasonUnauthorized     = "UNAUTHORIZED"
	reasonNotFound         = "TARGET_NOT_FOUND"
	reasonAlreadyExists    = "TARGET_ALREADY_EXISTS"
	reasonRevisionConflict = "REVISION_CONFLICT"
	reasonClosed           = "TARGET_CLOSED"
	reasonPolicy           = "TRANSITION_POLICY"
	reasonStaleExecution   = "STALE_EXECUTION"
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
	if err := validateEnvelope(command); err != nil {
		return rejectedDecision(command, context, fingerprint, OutcomeRejectedInvalid, reasonInvalidEnvelope), nil
	}
	definition, err := e.Catalogue.ResolveCommand(command.CommandType, command.CommandVersion, command.Target.Kind, command.Payload)
	if err != nil {
		return rejectedDecision(command, context, fingerprint, OutcomeRejectedInvalid, reasonInvalidPayload), nil
	}
	if !containsPrincipalKind(definition.AuthorityKinds, command.Authority.Kind) {
		return rejectedDecision(command, context, fingerprint, OutcomeRejectedUnauthorized, reasonUnauthorized), nil
	}
	if definition.ExecutionRequired && command.Execution == nil {
		return rejectedDecision(command, context, fingerprint, OutcomeRejectedStaleExecution, reasonStaleExecution), nil
	}
	if commandCreatesAggregate(command.CommandType) != command.ExpectedRevision.MustNotExist {
		return rejectedDecision(command, context, fingerprint, OutcomeRejectedConflict, reasonRevisionConflict), nil
	}
	if command.ExpectedRevision.MustNotExist {
		if snapshot.Exists {
			return rejectedDecision(command, context, fingerprint, OutcomeRejectedConflict, reasonAlreadyExists), nil
		}
	} else {
		if !snapshot.Exists {
			return rejectedDecision(command, context, fingerprint, OutcomeRejectedNotFound, reasonNotFound), nil
		}
		if snapshot.Revision != command.ExpectedRevision.Revision {
			return rejectedDecision(command, context, fingerprint, OutcomeRejectedConflict, reasonRevisionConflict), nil
		}
	}

	nextState, nextRevision, outcome, reason := evolveState(command, snapshot, context.EventID)
	if outcome != OutcomeApplied {
		return rejectedDecision(command, context, fingerprint, outcome, reason), nil
	}
	if len(definition.EventTypes) != 1 {
		return rejectedDecision(command, context, fingerprint, OutcomeRejectedInvalid, reasonInvalidEnvelope), nil
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
		Parents:           append([]DagParent(nil), command.Causation...),
		CommittedAt:       context.DecidedAt,
		Payload:           payload,
		ProvenanceDigest:  context.ProvenanceDigest,
	}
	revision := nextRevision
	receipt := receiptFor(command, context, OutcomeApplied, reasonApplied, true, &revision, []UUIDv7{context.EventID})
	return Decision{
		CommandFingerprint: fingerprint,
		NextState:          nextState,
		Events:             []DomainEvent{event},
		Receipt:            receipt,
		Authority:          AuthorityDecision{Principal: command.Authority, Allowed: true, Reason: reasonApplied},
	}, nil
}

func validateEnvelope(command KernelCommand) error {
	if command.ContractManifest != ContractIdentity || command.CommandVersion != SchemaVersion {
		return errors.New("contract or command version mismatch")
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
	if len(command.IdempotencyKey) < 1 || len(command.IdempotencyKey) > 256 || !command.CorrelationID.Valid() {
		return errors.New("invalid idempotency or correlation identity")
	}
	if len(command.Causation) > 64 || len(command.EvidenceRefs) > 64 || len(command.Payload) == 0 {
		return errors.New("envelope bounds exceeded")
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

func validateDecisionContext(context DecisionContext) error {
	if context.ReceivedAt.IsZero() || context.DecidedAt.IsZero() || context.DecidedAt.Before(context.ReceivedAt) {
		return errors.New("invalid decision time")
	}
	if !context.EventID.Valid() || !context.ProvenanceDigest.Valid() {
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
	if state.Phase == PhaseClosed {
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
	}
	if outcome, reason := applyOwnership(command, &state, eventID); outcome != OutcomeApplied {
		return nil, 0, outcome, reason
	}
	state.Revision = nextRevision
	return &state, nextRevision, OutcomeApplied, reasonApplied
}

func commandCreatesAggregate(commandType string) bool {
	switch commandType {
	case "tekroo.command.story.create",
		"tekroo.command.task.create",
		"tekroo.command.evidence.register",
		"tekroo.command.execution.register",
		"tekroo.command.completion-review.open":
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
	return Decision{
		CommandFingerprint: fingerprint,
		Receipt:            receiptFor(command, context, outcome, reason, false, nil, nil),
		Authority: AuthorityDecision{
			Principal: command.Authority,
			Allowed:   outcome != OutcomeRejectedUnauthorized,
			Reason:    reason,
		},
	}
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
