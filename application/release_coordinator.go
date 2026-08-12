package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

var (
	ErrInvalidReleaseOperation = errors.New("invalid release operation")
	ErrReleaseObservation      = errors.New("release provider observation identity mismatch")
	ErrReleaseResultRejected   = errors.New("release provider result was not durably accepted")
)

type ReleaseCoordinatorPolicy struct {
	OperationTimeout time.Duration
}

type ReleaseCommandTemplate struct {
	CommandID              kernel.UUIDv7
	Authority              kernel.PrincipalRef
	ExpectedPolicyRevision uint64
	IdempotencyKey         string
	CorrelationID          kernel.UUIDv7
	EvidenceRefs           []kernel.EvidenceRef
}

type ReleaseExecutionPlan struct {
	Plan              kernel.ReleasePlanSnapshot
	AttemptID         kernel.UUIDv7
	ProviderKey       string
	Request           ReleaseCommandTemplate
	RequestProvenance kernel.ProvenanceBasis
	Result            ReleaseCommandTemplate
	ResultProvenance  kernel.ProvenanceBasis
	ObservedAt        time.Time
}

type ReleaseReconciliationPlan struct {
	Plan       kernel.ReleasePlanSnapshot
	Command    ReleaseCommandTemplate
	Provenance kernel.ProvenanceBasis
	ObservedAt time.Time
}

type ReleaseCoordinationResult struct {
	RequestReceipt *kernel.CommandReceipt
	ResultReceipt  *kernel.CommandReceipt
	Observation    kernel.ReleaseProviderObservation
}

type ReleaseCoordinator struct {
	commands ExecutionCommandService
	provider kernel.ReleaseProvider
	policy   ReleaseCoordinatorPolicy
}

func NewReleaseCoordinator(commands ExecutionCommandService, provider kernel.ReleaseProvider, policy ReleaseCoordinatorPolicy) (*ReleaseCoordinator, error) {
	if commands == nil || provider == nil || policy.OperationTimeout <= 0 {
		return nil, ErrInvalidConfiguration
	}
	return &ReleaseCoordinator{commands: commands, provider: provider, policy: policy}, nil
}

// ExecuteNext persists the exact provider intent before invoking the provider,
// then durably records either the authoritative observation or UNKNOWN. An
// UNKNOWN result requires Reconcile before another execution can be requested.
func (coordinator *ReleaseCoordinator) ExecuteNext(ctx context.Context, operation ReleaseExecutionPlan) (ReleaseCoordinationResult, error) {
	request, merge, err := releaseProviderRequest(operation.Plan, operation.AttemptID, operation.ProviderKey)
	if err != nil || !validReleaseTemplate(operation.Request) || !validReleaseTemplate(operation.Result) || !operation.RequestProvenance.Valid() || !operation.ResultProvenance.Valid() || operation.ObservedAt.IsZero() {
		return ReleaseCoordinationResult{}, ErrInvalidReleaseOperation
	}
	requestCommand, err := executionRequestCommand(operation.Plan, request, operation.Request)
	if err != nil {
		return ReleaseCoordinationResult{}, err
	}
	requestReceipt, err := coordinator.commands.Handle(ctx, requestCommand, operation.RequestProvenance)
	result := ReleaseCoordinationResult{RequestReceipt: &requestReceipt}
	if err != nil {
		return result, fmt.Errorf("persist release execution intent: %w", err)
	}
	if requestReceipt.OutcomeCode != kernel.OutcomeApplied {
		return result, nil
	}
	if requestReceipt.ResultingRevision == nil || *requestReceipt.ResultingRevision != operation.Plan.Revision+1 || len(requestReceipt.EventIDs) != 1 {
		return result, ErrInvalidReleaseOperation
	}

	observation, effectErr := runReleaseEffect(ctx, coordinator.policy.OperationTimeout, func(effectCtx context.Context) (kernel.ReleaseProviderObservation, error) {
		return coordinator.provider.Merge(effectCtx, request)
	})
	if !releaseObservationMatches(request, observation) {
		if effectErr == nil {
			effectErr = ErrReleaseObservation
		}
		observation = uncertainReleaseObservation(request, "provider observation did not match the persisted release plan")
	}
	if effectErr != nil {
		observation = uncertainReleaseObservation(request, "provider merge acknowledgement is uncertain")
	}
	result.Observation = observation
	resultCommand, err := releaseResultCommand(operation.Plan, merge, request, observation, *requestReceipt.ResultingRevision, requestReceipt.EventIDs[0], operation.Result, operation.ObservedAt)
	if err != nil {
		return result, err
	}
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), coordinator.policy.OperationTimeout)
	defer cancel()
	resultReceipt, persistErr := coordinator.commands.Handle(persistCtx, resultCommand, operation.ResultProvenance)
	result.ResultReceipt = &resultReceipt
	if persistErr != nil {
		return result, fmt.Errorf("persist release provider result: %w", persistErr)
	}
	if resultReceipt.OutcomeCode != kernel.OutcomeApplied {
		return result, fmt.Errorf("%w: %s", ErrReleaseResultRejected, resultReceipt.ReasonCode)
	}
	if effectErr != nil {
		return result, effectErr
	}
	return result, nil
}

// Reconcile performs an authoritative read for the unresolved attempt and
// persists a superseding reconciliation event. It never initiates a merge.
func (coordinator *ReleaseCoordinator) Reconcile(ctx context.Context, operation ReleaseReconciliationPlan) (ReleaseCoordinationResult, error) {
	if !operation.Plan.Valid() || operation.Plan.State != kernel.ReleaseReconciling || operation.Plan.ActiveAttempt == nil || !validReleaseTemplate(operation.Command) || !operation.Provenance.Valid() || operation.ObservedAt.IsZero() {
		return ReleaseCoordinationResult{}, ErrInvalidReleaseOperation
	}
	request, _, err := releaseProviderRequest(operation.Plan, operation.Plan.ActiveAttempt.AttemptID, operation.Plan.ActiveAttempt.ProviderIdempotencyKey)
	if err != nil {
		return ReleaseCoordinationResult{}, err
	}
	prior, found := operation.Plan.Results[request.MergeID]
	if !found || prior.Outcome != kernel.ReleaseOutcomeUnknown || !prior.ResultEventID.Valid() {
		return ReleaseCoordinationResult{}, ErrInvalidReleaseOperation
	}
	observation, effectErr := runReleaseEffect(ctx, coordinator.policy.OperationTimeout, func(effectCtx context.Context) (kernel.ReleaseProviderObservation, error) {
		return coordinator.provider.Reconcile(effectCtx, request)
	})
	if !releaseObservationMatches(request, observation) {
		if effectErr == nil {
			effectErr = ErrReleaseObservation
		}
		observation = uncertainReleaseObservation(request, "provider reconciliation did not match the persisted release plan")
	}
	if effectErr != nil {
		observation = uncertainReleaseObservation(request, "release provider is unavailable for reconciliation")
	}
	result := ReleaseCoordinationResult{Observation: observation}
	command, err := releaseReconciliationCommand(operation.Plan, prior.ResultEventID, request, observation, operation.Command, operation.ObservedAt)
	if err != nil {
		return result, err
	}
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), coordinator.policy.OperationTimeout)
	defer cancel()
	receipt, persistErr := coordinator.commands.Handle(persistCtx, command, operation.Provenance)
	result.ResultReceipt = &receipt
	if persistErr != nil {
		return result, fmt.Errorf("persist release reconciliation: %w", persistErr)
	}
	if receipt.OutcomeCode != kernel.OutcomeApplied {
		return result, fmt.Errorf("%w: %s", ErrReleaseResultRejected, receipt.ReasonCode)
	}
	if effectErr != nil {
		return result, effectErr
	}
	return result, nil
}

func releaseProviderRequest(plan kernel.ReleasePlanSnapshot, attemptID kernel.UUIDv7, providerKey string) (kernel.ReleaseMergeRequest, kernel.ReleaseMergePlan, error) {
	if !plan.Valid() || (plan.State != kernel.ReleaseQualified && plan.State != kernel.ReleaseReconciling) || plan.NextMergeIndex >= uint64(len(plan.OrderedMerges)) || !attemptID.Valid() || providerKey == "" {
		return kernel.ReleaseMergeRequest{}, kernel.ReleaseMergePlan{}, ErrInvalidReleaseOperation
	}
	merge := plan.OrderedMerges[plan.NextMergeIndex]
	round := plan.NextRound
	if plan.State == kernel.ReleaseReconciling {
		if plan.ActiveAttempt == nil || plan.ActiveAttempt.MergeID != merge.MergeID || plan.ActiveAttempt.AttemptID != attemptID || plan.ActiveAttempt.ProviderIdempotencyKey != providerKey {
			return kernel.ReleaseMergeRequest{}, kernel.ReleaseMergePlan{}, ErrInvalidReleaseOperation
		}
		round = plan.ActiveAttempt.Round
	}
	request := kernel.ReleaseMergeRequest{
		ReleasePlanID: plan.ReleasePlanID, PlanDigest: plan.PlanDigest, MergeID: merge.MergeID, AttemptID: attemptID, Round: round,
		ProviderIdempotencyKey: providerKey, RepositoryURL: plan.RepositoryURL, BaseRef: plan.BaseRef, BaseCommit: plan.BaseCommit,
		ChangeRef: merge.ChangeRef, HeadCommit: merge.HeadCommit, MergeStrategy: plan.MergeStrategy,
	}
	if !request.Valid() {
		return kernel.ReleaseMergeRequest{}, kernel.ReleaseMergePlan{}, ErrInvalidReleaseOperation
	}
	return request, merge, nil
}

func executionRequestCommand(plan kernel.ReleasePlanSnapshot, request kernel.ReleaseMergeRequest, template ReleaseCommandTemplate) (kernel.KernelCommand, error) {
	parent, ok := executionRequestParent(plan)
	if !ok {
		return kernel.KernelCommand{}, ErrInvalidReleaseOperation
	}
	payload, err := json.Marshal(struct {
		ReleasePlanID          kernel.UUIDv7   `json:"release_plan_id"`
		ExpectedRevision       uint64          `json:"expected_release_revision"`
		PlanDigest             kernel.Digest   `json:"plan_digest"`
		MergeID                kernel.UUIDv7   `json:"merge_id"`
		AttemptID              kernel.UUIDv7   `json:"attempt_id"`
		Round                  uint64          `json:"round"`
		ProviderIdempotencyKey string          `json:"provider_idempotency_key"`
		EvidenceIDs            []kernel.UUIDv7 `json:"evidence_ids"`
	}{plan.ReleasePlanID, plan.Revision, plan.PlanDigest, request.MergeID, request.AttemptID, request.Round, request.ProviderIdempotencyKey, evidenceIDs(template.EvidenceRefs)})
	if err != nil {
		return kernel.KernelCommand{}, err
	}
	return releaseCommand(template, "tekroo.command.release-plan.request-execution", plan.ReleasePlanID, plan.Revision, []kernel.DagParent{parent}, payload), nil
}

func releaseResultCommand(plan kernel.ReleasePlanSnapshot, merge kernel.ReleaseMergePlan, request kernel.ReleaseMergeRequest, observation kernel.ReleaseProviderObservation, expectedRevision uint64, requestEventID kernel.UUIDv7, template ReleaseCommandTemplate, observedAt time.Time) (kernel.KernelCommand, error) {
	payload, err := json.Marshal(releaseResultPayload{
		ReleasePlanID: plan.ReleasePlanID, ExpectedRevision: expectedRevision, PlanDigest: plan.PlanDigest, MergeID: merge.MergeID, AttemptID: request.AttemptID,
		Reasons: observation.Reasons, EvidenceIDs: evidenceIDs(template.EvidenceRefs), ObservedAt: observedAt.UTC().Format(time.RFC3339Nano), Outcome: observation.Outcome,
		ObservedBaseCommit: observation.BaseCommit, ObservedHeadCommit: observation.HeadCommit, ObservedTreeDigest: observation.TreeDigest,
	})
	if err != nil {
		return kernel.KernelCommand{}, err
	}
	return releaseCommand(template, "tekroo.command.release-plan.record-result", plan.ReleasePlanID, expectedRevision, []kernel.DagParent{{ParentEventID: requestEventID, EdgeKind: kernel.EdgeResponse}}, payload), nil
}

func releaseReconciliationCommand(plan kernel.ReleasePlanSnapshot, priorResultEventID kernel.UUIDv7, request kernel.ReleaseMergeRequest, observation kernel.ReleaseProviderObservation, template ReleaseCommandTemplate, observedAt time.Time) (kernel.KernelCommand, error) {
	payload, err := json.Marshal(releaseReconciliationPayload{
		ReleasePlanID: plan.ReleasePlanID, ExpectedRevision: plan.Revision, PlanDigest: plan.PlanDigest, MergeID: request.MergeID, AttemptID: request.AttemptID,
		ReconciliationID: template.CommandID, SupersedesResultEventID: priorResultEventID, Reasons: observation.Reasons, EvidenceIDs: evidenceIDs(template.EvidenceRefs),
		ObservedAt: observedAt.UTC().Format(time.RFC3339Nano), ProviderState: observation.State, Outcome: observation.Outcome,
		ObservedBaseCommit: observation.BaseCommit, ObservedHeadCommit: observation.HeadCommit, ObservedTreeDigest: observation.TreeDigest,
	})
	if err != nil {
		return kernel.KernelCommand{}, err
	}
	return releaseCommand(template, "tekroo.command.release-plan.record-reconciliation", plan.ReleasePlanID, plan.Revision, []kernel.DagParent{{ParentEventID: priorResultEventID, EdgeKind: kernel.EdgeResponse}}, payload), nil
}

type releaseResultPayload struct {
	ReleasePlanID      kernel.UUIDv7         `json:"release_plan_id"`
	ExpectedRevision   uint64                `json:"expected_release_revision"`
	PlanDigest         kernel.Digest         `json:"plan_digest"`
	MergeID            kernel.UUIDv7         `json:"merge_id"`
	AttemptID          kernel.UUIDv7         `json:"attempt_id"`
	Reasons            []string              `json:"reasons"`
	EvidenceIDs        []kernel.UUIDv7       `json:"evidence_ids"`
	ObservedAt         string                `json:"observed_at"`
	Outcome            kernel.ReleaseOutcome `json:"outcome"`
	ObservedBaseCommit string                `json:"observed_base_commit,omitempty"`
	ObservedHeadCommit string                `json:"observed_head_commit,omitempty"`
	ObservedTreeDigest string                `json:"observed_tree_digest,omitempty"`
}

type releaseReconciliationPayload struct {
	ReleasePlanID           kernel.UUIDv7               `json:"release_plan_id"`
	ExpectedRevision        uint64                      `json:"expected_release_revision"`
	PlanDigest              kernel.Digest               `json:"plan_digest"`
	MergeID                 kernel.UUIDv7               `json:"merge_id"`
	AttemptID               kernel.UUIDv7               `json:"attempt_id"`
	ReconciliationID        kernel.UUIDv7               `json:"reconciliation_id"`
	SupersedesResultEventID kernel.UUIDv7               `json:"supersedes_result_event_id"`
	Reasons                 []string                    `json:"reasons"`
	EvidenceIDs             []kernel.UUIDv7             `json:"evidence_ids"`
	ObservedAt              string                      `json:"observed_at"`
	ProviderState           kernel.ReleaseProviderState `json:"provider_state"`
	Outcome                 kernel.ReleaseOutcome       `json:"outcome"`
	ObservedBaseCommit      string                      `json:"observed_base_commit,omitempty"`
	ObservedHeadCommit      string                      `json:"observed_head_commit,omitempty"`
	ObservedTreeDigest      string                      `json:"observed_tree_digest,omitempty"`
}

func releaseCommand(template ReleaseCommandTemplate, commandType string, planID kernel.UUIDv7, revision uint64, parents []kernel.DagParent, payload json.RawMessage) kernel.KernelCommand {
	return kernel.KernelCommand{
		ContractManifest: kernel.ContractIdentity, CommandID: template.CommandID, CommandType: commandType, CommandVersion: kernel.SchemaVersion,
		Target: kernel.AggregateRef{Kind: kernel.AggregateReleasePlan, ID: planID}, Authority: template.Authority, ExpectedRevision: kernel.NewExpectedRevision(revision),
		ExpectedPolicyRevision: template.ExpectedPolicyRevision, ExpectedCatalogueRevision: kernel.CatalogueRevision, IdempotencyKey: template.IdempotencyKey,
		CorrelationID: template.CorrelationID, Causation: parents, Payload: payload, EvidenceRefs: append([]kernel.EvidenceRef(nil), template.EvidenceRefs...),
	}
}

func executionRequestParent(plan kernel.ReleasePlanSnapshot) (kernel.DagParent, bool) {
	mergeIndex := plan.NextMergeIndex
	if plan.NextRound == 1 {
		if mergeIndex == 0 && plan.Qualification != nil && plan.Qualification.EventID.Valid() {
			return kernel.DagParent{ParentEventID: plan.Qualification.EventID, EdgeKind: kernel.EdgeResponse}, true
		}
		if mergeIndex > 0 {
			if eventID, found := effectiveReleaseResultEventID(plan.Results[plan.OrderedMerges[mergeIndex-1].MergeID]); found {
				return kernel.DagParent{ParentEventID: eventID, EdgeKind: kernel.EdgeResponse}, true
			}
		}
	}
	if eventID, found := effectiveReleaseResultEventID(plan.Results[plan.OrderedMerges[mergeIndex].MergeID]); found {
		return kernel.DagParent{ParentEventID: eventID, EdgeKind: kernel.EdgeResponse}, true
	}
	return kernel.DagParent{}, false
}

func effectiveReleaseResultEventID(result kernel.ReleaseResult) (kernel.UUIDv7, bool) {
	eventID := result.ResultEventID
	if result.Reconciled {
		eventID = result.ReconciliationEventID
	}
	return eventID, eventID.Valid()
}

func validReleaseTemplate(template ReleaseCommandTemplate) bool {
	if !template.CommandID.Valid() || !template.Authority.Valid() || template.ExpectedPolicyRevision == 0 || template.IdempotencyKey == "" || !template.CorrelationID.Valid() || len(template.EvidenceRefs) == 0 || len(template.EvidenceRefs) > 64 {
		return false
	}
	seen := make(map[kernel.UUIDv7]struct{}, len(template.EvidenceRefs))
	for _, reference := range template.EvidenceRefs {
		if !reference.EvidenceID.Valid() || !reference.SHA256.Valid() {
			return false
		}
		if _, duplicate := seen[reference.EvidenceID]; duplicate {
			return false
		}
		seen[reference.EvidenceID] = struct{}{}
	}
	return true
}

func evidenceIDs(refs []kernel.EvidenceRef) []kernel.UUIDv7 {
	ids := make([]kernel.UUIDv7, len(refs))
	for index := range refs {
		ids[index] = refs[index].EvidenceID
	}
	return ids
}

func releaseObservationMatches(request kernel.ReleaseMergeRequest, observation kernel.ReleaseProviderObservation) bool {
	if observation.ReleasePlanID != request.ReleasePlanID || observation.MergeID != request.MergeID || observation.AttemptID != request.AttemptID || !validReleaseReasons(observation.Reasons) {
		return false
	}
	switch observation.Outcome {
	case kernel.ReleaseOutcomeMerged, kernel.ReleaseOutcomeAlreadyMerged:
		return observation.State == kernel.ReleaseProviderMerged && observation.BaseCommit == request.BaseCommit && observation.HeadCommit == request.HeadCommit && observation.TreeDigest != ""
	case kernel.ReleaseOutcomeFailed:
		return (observation.State == kernel.ReleaseProviderOpen || observation.State == kernel.ReleaseProviderClosedUnmerged || observation.State == kernel.ReleaseProviderMissing) && observation.BaseCommit == "" && observation.HeadCommit == "" && observation.TreeDigest == ""
	case kernel.ReleaseOutcomeUnknown:
		return observation.State == kernel.ReleaseProviderUnavailable && observation.BaseCommit == "" && observation.HeadCommit == "" && observation.TreeDigest == ""
	default:
		return false
	}
}

func validReleaseReasons(reasons []string) bool {
	if len(reasons) == 0 || len(reasons) > 64 {
		return false
	}
	seen := make(map[string]struct{}, len(reasons))
	for _, reason := range reasons {
		if reason == "" || len(reason) > 4096 {
			return false
		}
		if _, duplicate := seen[reason]; duplicate {
			return false
		}
		seen[reason] = struct{}{}
	}
	return true
}

func uncertainReleaseObservation(request kernel.ReleaseMergeRequest, reason string) kernel.ReleaseProviderObservation {
	return kernel.ReleaseProviderObservation{ReleasePlanID: request.ReleasePlanID, MergeID: request.MergeID, AttemptID: request.AttemptID, State: kernel.ReleaseProviderUnavailable, Outcome: kernel.ReleaseOutcomeUnknown, Reasons: []string{reason}}
}

func runReleaseEffect(ctx context.Context, timeout time.Duration, effect func(context.Context) (kernel.ReleaseProviderObservation, error)) (kernel.ReleaseProviderObservation, error) {
	effectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return effect(effectCtx)
}
