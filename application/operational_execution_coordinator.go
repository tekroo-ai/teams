package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

const workInvocationOutboxKind = "WORK_INVOCATION_AUTHORIZED"

type OperationalExecutionPolicy struct {
	OperationTimeout  time.Duration
	MaximumBriefBytes int
	ConsumerID        string
	PolicyRevision    uint64
	ServiceAuthority  kernel.PrincipalRef
	ExpiryAuthority   kernel.PrincipalRef
	Provenance        kernel.ProvenanceBasis
}

func (policy OperationalExecutionPolicy) valid() bool {
	return policy.OperationTimeout > 0 && policy.MaximumBriefBytes > 0 && policy.MaximumBriefBytes <= 1<<20 && policy.ConsumerID != "" && len(policy.ConsumerID) <= 256 && policy.PolicyRevision > 0 && policy.ServiceAuthority.Kind == kernel.PrincipalService && policy.ServiceAuthority.Valid() && policy.ExpiryAuthority.Kind == kernel.PrincipalPolicy && policy.ExpiryAuthority.Valid() && policy.Provenance.Valid()
}

type OperationalExecutionResult struct {
	InvocationID   kernel.UUIDv7
	State          kernel.WorkInvocationState
	ConversationID string
	Evidence       []kernel.EvidenceRef
	Receipt        *kernel.CommandReceipt
}

type OperationalExecutionCoordinator struct {
	reader   OperationalExecutionReader
	commands ExecutionCommandService
	boundary OpenHandsExecutionBoundary
	evidence ExecutionEvidenceRecorder
	clock    kernel.Clock
	policy   OperationalExecutionPolicy
}

func NewOperationalExecutionCoordinator(reader OperationalExecutionReader, commands ExecutionCommandService, boundary OpenHandsExecutionBoundary, evidence ExecutionEvidenceRecorder, clock kernel.Clock, policy OperationalExecutionPolicy) (*OperationalExecutionCoordinator, error) {
	if reader == nil || commands == nil || boundary == nil || evidence == nil || clock == nil || !policy.valid() {
		return nil, ErrInvalidConfiguration
	}
	return &OperationalExecutionCoordinator{reader: reader, commands: commands, boundary: boundary, evidence: evidence, clock: clock, policy: policy}, nil
}

// Process consumes exactly one invocation authorization intent. Repeated calls
// reconcile the same invocation; they never authorize another model call.
func (coordinator *OperationalExecutionCoordinator) Process(ctx context.Context, intent kernel.OutboxIntent) (OperationalExecutionResult, error) {
	if err := ctx.Err(); err != nil {
		return OperationalExecutionResult{}, err
	}
	if !intent.IntentID.Valid() || !intent.EventID.Valid() || intent.Kind != workInvocationOutboxKind {
		return OperationalExecutionResult{}, ErrInvalidOperationalExecution
	}
	current, err := coordinator.reader.LoadOperationalExecutionByAuthorizationEvent(ctx, intent.EventID)
	if err != nil {
		return OperationalExecutionResult{}, err
	}
	if current.Invocation.AuthorizationEventID != intent.EventID {
		return OperationalExecutionResult{}, ErrInvalidOperationalExecution
	}
	claimedThisCall := false
	for transitions := 0; transitions < 5; transitions++ {
		invocation := current.Invocation
		result := OperationalExecutionResult{InvocationID: invocation.ID, State: invocation.State}
		if invocation.ConversationID != nil {
			result.ConversationID = *invocation.ConversationID
		}
		if invocation.State.Terminal() {
			return result, nil
		}
		switch invocation.State {
		case kernel.InvocationAuthorized:
			if !coordinator.clock.Now().Before(invocation.DeadlineAt) {
				receipt, commandErr := coordinator.expire(ctx, invocation)
				result.Receipt = &receipt
				if commandErr != nil {
					return result, commandErr
				}
				current, err = coordinator.reader.LoadOperationalExecution(ctx, invocation.ID)
				if err != nil {
					return result, err
				}
				continue
			}
			if err := current.Validate(coordinator.clock.Now()); err != nil {
				return result, err
			}
			receipt, commandErr := coordinator.claim(ctx, intent, current)
			result.Receipt = &receipt
			if commandErr != nil {
				return result, commandErr
			}
			claimedThisCall = true
			current, err = coordinator.reader.LoadOperationalExecution(ctx, invocation.ID)
			if err != nil {
				return result, err
			}
			continue
		case kernel.InvocationClaimed:
			if err := current.Validate(coordinator.clock.Now()); err != nil {
				return coordinator.recordKnownStartFailure(ctx, current, err)
			}
			brief, requestDigest, briefErr := BuildExecutionBrief(current, coordinator.policy.MaximumBriefBytes)
			if briefErr != nil {
				return coordinator.recordKnownStartFailure(ctx, current, briefErr)
			}
			observation, effectErr := coordinator.startOrReconcile(ctx, brief, requestDigest, claimedThisCall)
			if effectErr != nil {
				return result, effectErr
			}
			if !externalObservationMatches(brief, requestDigest, observation) {
				return result, ErrExternalOutcomeUnknown
			}
			switch observation.State {
			case ExternalAbsent, ExternalUnknown:
				return result, ErrExternalOutcomeUnknown
			case ExternalRejected:
				return coordinator.recordTerminalObservation(ctx, current, observation, kernel.InvocationStartFailed)
			case ExternalRunning, ExternalSucceeded, ExternalFailed, ExternalTimedOut, ExternalCancelled:
				if observation.ConversationID == "" {
					return result, ErrExternalOutcomeUnknown
				}
				receipt, commandErr := coordinator.recordStarted(ctx, invocation, observation)
				result.Receipt = &receipt
				if commandErr != nil {
					return result, commandErr
				}
				current, err = coordinator.reader.LoadOperationalExecution(ctx, invocation.ID)
				if err != nil {
					return result, err
				}
				if observation.State != ExternalRunning {
					return coordinator.recordTerminalObservation(ctx, current, observation, terminalOutcome(observation.State))
				}
				continue
			default:
				return result, ErrExternalOutcomeUnknown
			}
		case kernel.InvocationStarted:
			brief, requestDigest, briefErr := BuildExecutionBrief(current, coordinator.policy.MaximumBriefBytes)
			if briefErr != nil || invocation.ConversationID == nil || invocation.RequestDigest == nil || *invocation.RequestDigest != requestDigest {
				return result, ErrInvalidOperationalExecution
			}
			var observation ExternalExecutionObservation
			deadlineExceeded := !coordinator.clock.Now().Before(invocation.DeadlineAt)
			if invocation.CancellationRequestedAt != nil || deadlineExceeded {
				observation, err = coordinator.callBoundary(ctx, func(effectCtx context.Context) (ExternalExecutionObservation, error) {
					return coordinator.boundary.Cancel(effectCtx, brief, *invocation.ConversationID, requestDigest)
				})
			} else {
				observation, err = coordinator.callBoundary(ctx, func(effectCtx context.Context) (ExternalExecutionObservation, error) {
					return coordinator.boundary.Inspect(effectCtx, brief, *invocation.ConversationID, requestDigest)
				})
			}
			if err != nil || !externalObservationMatches(brief, requestDigest, observation) || observation.ConversationID != *invocation.ConversationID || observation.State == ExternalAbsent || observation.State == ExternalUnknown || observation.State == ExternalRejected {
				return result, ErrExternalOutcomeUnknown
			}
			if observation.State == ExternalRunning {
				return result, nil
			}
			outcome := terminalOutcome(observation.State)
			if deadlineExceeded && observation.State == ExternalCancelled {
				outcome = kernel.InvocationTimedOut
			}
			return coordinator.recordTerminalObservation(ctx, current, observation, outcome)
		default:
			return result, ErrInvalidOperationalExecution
		}
	}
	return OperationalExecutionResult{InvocationID: current.Invocation.ID, State: current.Invocation.State}, ErrInvalidOperationalExecution
}

func (coordinator *OperationalExecutionCoordinator) startOrReconcile(ctx context.Context, brief ExecutionBrief, requestDigest kernel.Digest, claimedThisCall bool) (ExternalExecutionObservation, error) {
	if claimedThisCall {
		observation, err := coordinator.callBoundary(ctx, func(effectCtx context.Context) (ExternalExecutionObservation, error) {
			return coordinator.boundary.Start(effectCtx, brief, requestDigest)
		})
		if err == nil {
			return observation, nil
		}
	}
	observation, err := coordinator.callBoundary(ctx, func(effectCtx context.Context) (ExternalExecutionObservation, error) {
		return coordinator.boundary.ReconcileStart(effectCtx, brief, requestDigest)
	})
	if err != nil {
		return ExternalExecutionObservation{}, ErrExternalOutcomeUnknown
	}
	if observation.State != ExternalAbsent {
		return observation, nil
	}
	return coordinator.callBoundary(ctx, func(effectCtx context.Context) (ExternalExecutionObservation, error) {
		return coordinator.boundary.Start(effectCtx, brief, requestDigest)
	})
}

func (coordinator *OperationalExecutionCoordinator) callBoundary(ctx context.Context, call func(context.Context) (ExternalExecutionObservation, error)) (ExternalExecutionObservation, error) {
	effectCtx, cancel := context.WithTimeout(ctx, coordinator.policy.OperationTimeout)
	defer cancel()
	return call(effectCtx)
}

func (coordinator *OperationalExecutionCoordinator) claim(ctx context.Context, intent kernel.OutboxIntent, current OperationalExecutionContext) (kernel.CommandReceipt, error) {
	invocation := current.Invocation
	payload, _ := json.Marshal(struct {
		InvocationID          kernel.UUIDv7   `json:"invocation_id"`
		ExpectedRevision      uint64          `json:"expected_invocation_revision"`
		ClaimID               kernel.UUIDv7   `json:"claim_id"`
		ActorFQN              kernel.ActorFQN `json:"actor_fqn"`
		ExecutionID           kernel.UUIDv7   `json:"execution_id"`
		FencingEpoch          uint64          `json:"fencing_epoch"`
		ModelProfileDigest    kernel.Digest   `json:"model_profile_digest"`
		RuntimeIdentityDigest kernel.Digest   `json:"runtime_identity_digest"`
		ConsumerID            string          `json:"consumer_id"`
		ClaimedAt             time.Time       `json:"claimed_at"`
	}{invocation.ID, invocation.Revision, intent.IntentID, invocation.ActorFQN, invocation.Execution.ExecutionID, invocation.Execution.FencingEpoch, invocation.ModelProfileDigest, invocation.RuntimeIdentityDigest, coordinator.policy.ConsumerID, coordinator.clock.Now()})
	actor, execution := invocation.ActorFQN, invocation.Execution
	command := coordinator.command(invocation, "tekroo.command.work-invocation.claim", payload, nil, coordinator.policy.ServiceAuthority)
	command.ActorFQN, command.Execution = &actor, &execution
	command.Preconditions = []kernel.AggregatePrecondition{
		{Aggregate: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: invocation.TaskID}, Expected: kernel.NewExpectedRevision(current.Task.Revision)},
		{Aggregate: invocationBudgetRef(invocation), Expected: kernel.NewExpectedRevision(current.Budget.Revision)},
	}
	return coordinator.apply(ctx, command)
}

func (coordinator *OperationalExecutionCoordinator) expire(ctx context.Context, invocation kernel.WorkInvocation) (kernel.CommandReceipt, error) {
	payload, _ := json.Marshal(struct {
		InvocationID     kernel.UUIDv7       `json:"invocation_id"`
		ExpectedRevision uint64              `json:"expected_invocation_revision"`
		DeadlineAt       time.Time           `json:"deadline_at"`
		ObservedAt       time.Time           `json:"observed_at"`
		Authority        kernel.PrincipalRef `json:"authority"`
	}{invocation.ID, invocation.Revision, invocation.DeadlineAt, coordinator.clock.Now(), coordinator.policy.ExpiryAuthority})
	return coordinator.apply(ctx, coordinator.command(invocation, "tekroo.command.work-invocation.expire", payload, nil, coordinator.policy.ExpiryAuthority))
}

func (coordinator *OperationalExecutionCoordinator) recordStarted(ctx context.Context, invocation kernel.WorkInvocation, observation ExternalExecutionObservation) (kernel.CommandReceipt, error) {
	payload, _ := json.Marshal(struct {
		InvocationID     kernel.UUIDv7 `json:"invocation_id"`
		ExpectedRevision uint64        `json:"expected_invocation_revision"`
		ClaimID          kernel.UUIDv7 `json:"claim_id"`
		ConversationID   string        `json:"conversation_id"`
		RequestDigest    kernel.Digest `json:"request_digest"`
		StartedAt        time.Time     `json:"started_at"`
	}{invocation.ID, invocation.Revision, *invocation.ClaimID, observation.ConversationID, observation.RequestDigest, coordinator.clock.Now()})
	return coordinator.apply(ctx, coordinator.command(invocation, "tekroo.command.work-invocation.record-started", payload, nil, coordinator.policy.ServiceAuthority))
}

func (coordinator *OperationalExecutionCoordinator) recordKnownStartFailure(ctx context.Context, current OperationalExecutionContext, cause error) (OperationalExecutionResult, error) {
	invocation := current.Invocation
	brief, requestDigest, _ := BuildExecutionBrief(current, coordinator.policy.MaximumBriefBytes)
	if !requestDigest.Valid() {
		encoded, _ := json.Marshal(struct {
			InvocationID kernel.UUIDv7 `json:"invocation_id"`
			Reason       string        `json:"reason"`
		}{invocation.ID, cause.Error()})
		digest := sha256.Sum256(encoded)
		requestDigest = kernel.Digest(hex.EncodeToString(digest[:]))
	}
	observation := ExternalExecutionObservation{
		InvocationID: invocation.ID, RequestDigest: requestDigest, State: ExternalRejected,
		ActorFQN: invocation.ActorFQN, Execution: invocation.Execution,
		ModelProfileDigest: invocation.ModelProfileDigest, RuntimeIdentityDigest: invocation.RuntimeIdentityDigest,
		WorkspaceID: invocation.WorkspaceID, ToolPolicyDigest: invocation.ToolPolicyDigest,
		EffectPolicyDigest: invocation.EffectPolicyDigest, Output: []byte(cause.Error()),
	}
	_ = brief
	return coordinator.recordTerminalObservation(ctx, current, observation, kernel.InvocationStartFailed)
}

func (coordinator *OperationalExecutionCoordinator) recordTerminalObservation(ctx context.Context, current OperationalExecutionContext, observation ExternalExecutionObservation, outcome kernel.WorkInvocationState) (OperationalExecutionResult, error) {
	invocation := current.Invocation
	result := OperationalExecutionResult{InvocationID: invocation.ID, State: invocation.State, ConversationID: observation.ConversationID}
	if !containsAllowedOutcome(invocation.AllowedTerminalOutcomes, outcome) {
		return result, ErrInvalidOperationalExecution
	}
	receiptEvidence, err := providerReceiptEvidence(invocation, observation)
	if err != nil {
		return result, err
	}
	items := append([]ExecutionEvidence(nil), observation.Evidence...)
	if len(observation.Output) > 0 {
		outputKind := "MODEL_OUTPUT"
		if observation.State == ExternalRejected {
			outputKind = "EXTERNAL_OBSERVATION"
		}
		items = append(items, ExecutionEvidence{
			EvidenceID: deterministicUUID("provider-output", string(invocation.ID), string(observation.RequestDigest), string(observation.State), string(observation.Output)),
			Kind:       outputKind, MediaType: "application/octet-stream", SourceTimestamp: stableEvidenceTime(invocation),
			Content: append([]byte(nil), observation.Output...),
		})
	}
	items = append(items, receiptEvidence)
	references, err := coordinator.evidence.RecordExecutionEvidence(ctx, invocation, items)
	if err != nil {
		return result, err
	}
	sort.Slice(references, func(left, right int) bool { return references[left].EvidenceID < references[right].EvidenceID })
	if len(references) == 0 || len(references) > 64 {
		return result, ErrInvalidOperationalExecution
	}
	for index, reference := range references {
		if !reference.EvidenceID.Valid() || !reference.SHA256.Valid() || index > 0 && reference.EvidenceID == references[index-1].EvidenceID {
			return result, ErrInvalidOperationalExecution
		}
	}
	result.Evidence = append([]kernel.EvidenceRef(nil), references...)
	output := observation.Output
	if len(output) == 0 {
		output = receiptEvidence.Content
	}
	digest := sha256.Sum256(output)
	outputDigest := kernel.Digest(hex.EncodeToString(digest[:]))
	evidenceIDs := make([]kernel.UUIDv7, len(references))
	for index, reference := range references {
		evidenceIDs[index] = reference.EvidenceID
	}
	payload, _ := json.Marshal(struct {
		InvocationID     kernel.UUIDv7              `json:"invocation_id"`
		ExpectedRevision uint64                     `json:"expected_invocation_revision"`
		ClaimID          kernel.UUIDv7              `json:"claim_id"`
		Outcome          kernel.WorkInvocationState `json:"outcome"`
		Retryable        bool                       `json:"retryable"`
		EvidenceIDs      []kernel.UUIDv7            `json:"evidence_ids"`
		OutputDigest     kernel.Digest              `json:"output_digest"`
		FinishedAt       time.Time                  `json:"finished_at"`
	}{invocation.ID, invocation.Revision, *invocation.ClaimID, outcome, observation.Retryable, evidenceIDs, outputDigest, coordinator.clock.Now()})
	command := coordinator.command(invocation, "tekroo.command.work-invocation.record-terminal", payload, references, coordinator.policy.ServiceAuthority)
	receipt, err := coordinator.apply(ctx, command)
	result.Receipt = &receipt
	if err != nil {
		return result, err
	}
	result.State = outcome
	return result, nil
}

func providerReceiptEvidence(invocation kernel.WorkInvocation, observation ExternalExecutionObservation) (ExecutionEvidence, error) {
	content, err := json.Marshal(struct {
		InvocationID          kernel.UUIDv7          `json:"invocation_id"`
		RequestDigest         kernel.Digest          `json:"request_digest"`
		ConversationID        string                 `json:"conversation_id"`
		State                 ExternalExecutionState `json:"state"`
		ActorFQN              kernel.ActorFQN        `json:"actor_fqn"`
		Execution             kernel.ExecutionTuple  `json:"execution"`
		ModelProfileDigest    kernel.Digest          `json:"model_profile_digest"`
		RuntimeIdentityDigest kernel.Digest          `json:"runtime_identity_digest"`
		WorkspaceID           string                 `json:"workspace_id"`
		ToolPolicyDigest      kernel.Digest          `json:"tool_policy_digest"`
		EffectPolicyDigest    kernel.Digest          `json:"effect_policy_digest"`
	}{observation.InvocationID, observation.RequestDigest, observation.ConversationID, observation.State, observation.ActorFQN, observation.Execution, observation.ModelProfileDigest, observation.RuntimeIdentityDigest, observation.WorkspaceID, observation.ToolPolicyDigest, observation.EffectPolicyDigest})
	if err != nil {
		return ExecutionEvidence{}, err
	}
	return ExecutionEvidence{
		EvidenceID: deterministicUUID("provider-receipt", string(invocation.ID), string(observation.RequestDigest), string(observation.State)),
		Kind:       "PROVIDER_RECEIPT", MediaType: "application/json", SourceTimestamp: stableEvidenceTime(invocation), Content: content,
	}, nil
}

func (coordinator *OperationalExecutionCoordinator) command(invocation kernel.WorkInvocation, commandType string, payload json.RawMessage, evidence []kernel.EvidenceRef, authority kernel.PrincipalRef) kernel.KernelCommand {
	payloadDigest := sha256.Sum256(payload)
	commandID := deterministicUUID("operational-command", string(invocation.ID), commandType, fmt.Sprint(invocation.Revision), hex.EncodeToString(payloadDigest[:]))
	return kernel.KernelCommand{
		ContractManifest: kernel.ContractIdentity, CommandID: commandID,
		CommandType: commandType, CommandVersion: kernel.OperationalSchemaVersion,
		Target: invocation.Ref(), Authority: authority,
		ExpectedRevision:          kernel.NewExpectedRevision(invocation.Revision),
		ExpectedPolicyRevision:    coordinator.policy.PolicyRevision,
		ExpectedCatalogueRevision: kernel.CatalogueRevision,
		IdempotencyKey:            fmt.Sprintf("work-invocation/%s/%s/%d/%x", invocation.ID, commandType, invocation.Revision, payloadDigest),
		CorrelationID:             invocation.ID,
		Causation:                 []kernel.DagParent{{ParentEventID: invocation.LastEventID, EdgeKind: kernel.EdgeCausal}},
		Payload:                   append(json.RawMessage(nil), payload...), EvidenceRefs: append([]kernel.EvidenceRef(nil), evidence...),
	}
}

func (coordinator *OperationalExecutionCoordinator) apply(ctx context.Context, command kernel.KernelCommand) (kernel.CommandReceipt, error) {
	receipt, err := coordinator.commands.Handle(ctx, command, coordinator.policy.Provenance)
	if err != nil {
		return receipt, err
	}
	if receipt.OutcomeCode != kernel.OutcomeApplied && receipt.OutcomeCode != kernel.OutcomeNoChange {
		return receipt, fmt.Errorf("%w: %s", ErrStaleWorkInvocation, receipt.ReasonCode)
	}
	return receipt, nil
}

func invocationBudgetRef(invocation kernel.WorkInvocation) kernel.AggregateRef {
	return kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: invocation.BudgetAccountID}
}

func terminalOutcome(state ExternalExecutionState) kernel.WorkInvocationState {
	switch state {
	case ExternalSucceeded:
		return kernel.InvocationSucceeded
	case ExternalFailed:
		return kernel.InvocationFailed
	case ExternalTimedOut:
		return kernel.InvocationTimedOut
	case ExternalCancelled:
		return kernel.InvocationCancelled
	default:
		return ""
	}
}

func containsAllowedOutcome(values []kernel.WorkInvocationState, target kernel.WorkInvocationState) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func deterministicUUID(parts ...string) kernel.UUIDv7 {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(part))
	}
	value := hex.EncodeToString(hash.Sum(nil)[:16])
	value = value[:12] + "7" + value[13:16] + "8" + value[17:]
	return kernel.UUIDv7(value[:8] + "-" + value[8:12] + "-" + value[12:16] + "-" + value[16:20] + "-" + value[20:32])
}
