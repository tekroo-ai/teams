package operationalruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/tekroo-ai/teams/adapters/mongo"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

const runtimeContinuityProducingVersion = "teams-v4-runtime-continuity-v1"

func (service *ProductionService) stopRuntimeContinuitySession(ctx context.Context) error {
	if deadline, ok := ctx.Deadline(); ok && deadline.After(time.Now()) {
		return service.Store.StopRuntimeSession(ctx, service.deploymentIdentity, service.continuitySession, service.clock.Now().UTC())
	}
	operationContext, cancel := context.WithTimeout(context.Background(), service.projectionTimeout)
	defer cancel()
	return service.Store.StopRuntimeSession(operationContext, service.deploymentIdentity, service.continuitySession, service.clock.Now().UTC())
}

func (service *ProductionService) runRuntimeContinuity(ctx context.Context) error {
	for {
		timer := time.NewTimer(service.continuityHeartbeat)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if err := service.observeAndReconcileRuntimeContinuity(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			service.recordRecoveryFault("runtime-continuity", err)
			continue
		}
		service.clearRecoveryFault("runtime-continuity")
	}
}

func (service *ProductionService) observeAndReconcileRuntimeContinuity(ctx context.Context) error {
	if service == nil || service.Store == nil || !service.continuitySession.Valid() {
		return application.ErrInvalidConfiguration
	}
	service.continuityMu.Lock()
	defer service.continuityMu.Unlock()
	operationContext, cancel := context.WithTimeout(ctx, service.projectionTimeout)
	_, observed, err := service.Store.HeartbeatRuntimeSession(operationContext, service.deploymentIdentity, service.continuitySession, service.clock.Now().UTC(), service.continuityThreshold)
	cancel()
	if err != nil {
		return err
	}
	if !observed {
		return nil
	}
	paused := false
	if service.Controller.Inspect().State == ControlRunning {
		if err := service.Controller.Pause(ctx); err != nil {
			return err
		}
		paused = true
	}
	if paused {
		defer func() {
			_ = service.Controller.Resume(context.WithoutCancel(ctx))
		}()
	}
	return service.reconcileRuntimeSuspensions(ctx)
}

func (service *ProductionService) reconcileRuntimeSuspensions(ctx context.Context) error {
	if service == nil || service.Store == nil || !service.deploymentIdentity.Valid() {
		return application.ErrInvalidConfiguration
	}
	windows, err := service.Store.ListRuntimeSuspensions(ctx, service.deploymentIdentity)
	if err != nil {
		return err
	}
	if len(windows) == 0 {
		return nil
	}
	features, err := service.Store.ListFeatures(ctx, []organization.FeatureStatus{organization.FeaturePlanned, organization.FeatureApproved}, 1000)
	if err != nil {
		return err
	}
	for _, feature := range features {
		if feature.Plan == nil {
			return organization.ErrInvalidFeature
		}
		for _, window := range windows {
			if err := service.reconcileFeatureRuntimeSuspension(ctx, feature, *feature.Plan, window); err != nil {
				return fmt.Errorf("feature %s suspension %s: %w", feature.ID, window.ID, err)
			}
		}
	}
	return nil
}

func (service *ProductionService) reconcileFeatureRuntimeSuspension(ctx context.Context, feature organization.FeatureRequest, plan organization.FeaturePlan, window mongo.RuntimeSuspensionWindow) error {
	if !window.Valid() {
		return fmt.Errorf("%w: suspension window invalid", organization.ErrInvalidFeature)
	}
	if err := plan.Validate(feature); err != nil {
		return fmt.Errorf("%w: suspension replay plan validation: %w", organization.ErrInvalidFeature, err)
	}
	if !window.ResumedAt.After(feature.CreatedAt) {
		return nil
	}
	budgetRef := kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: feature.BudgetAccountID}
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: budgetRef})
	if err != nil {
		return err
	}
	account, found := snapshot.WorkBudgetAccounts[budgetRef]
	if !found {
		return nil
	}
	if !account.Valid() {
		return fmt.Errorf("%w: budget account %s invalid during suspension replay", organization.ErrInvalidFeature, account.ID)
	}
	if startedInvocationExists(snapshot.WorkInvocations, plan) {
		// An already-started invocation retains its immutable brief. The worker
		// uses the same suspension ledger to extend only its expiry comparison;
		// the feature budget is amended after that invocation becomes terminal.
		return nil
	}
	// The operation identity is independent of the currently loaded authority
	// policy. A policy successor must not make the same host-suspension interval
	// eligible for a second deadline allowance.
	key := "runtime-suspension-" + string(window.ID)
	commandID := deterministicOperationalUUID("command", string(feature.ID), "tekroo.command.work-budget.amend", string(account.ID), key)
	receipt, attempted, err := service.Store.ReadCommandReceipt(ctx, commandID)
	if err != nil {
		return err
	}
	var targetDeadline time.Time
	var evidence []kernel.EvidenceRef
	if attempted {
		targetDeadline, evidence, err = service.runtimeSuspensionAmendmentReceipt(ctx, receipt, account.ID, window)
		if err != nil {
			return err
		}
	} else {
		if !account.DeadlineAt.After(window.SuspendedAt) {
			return nil
		}
		evidenceRef, evidenceErr := service.ensureRuntimeSuspensionEvidence(ctx, window)
		if evidenceErr != nil {
			return evidenceErr
		}
		evidence = []kernel.EvidenceRef{evidenceRef}
		targetDeadline = account.DeadlineAt.Add(window.Duration())
		policyDigest := digestBytes([]byte("runtime-suspension-budget\x00" + string(account.PolicyDigest) + "\x00" + string(window.ID) + "\x00" + targetDeadline.Format(time.RFC3339Nano)))
		payload, marshalErr := json.Marshal(map[string]any{
			"budget_account_id": account.ID, "expected_budget_revision": account.Revision,
			"expected_lifecycle_epoch": account.LifecycleEpoch, "policy_revision": account.PolicyRevision + 1,
			"policy_digest": policyDigest, "model_invocation_limit": account.ModelInvocationLimit,
			"purpose_limits": account.PurposeLimits, "deadline_at": targetDeadline,
			"reason":       "exclude a recorded host-suspension interval from the feature execution deadline",
			"evidence_ids": []kernel.UUIDv7{evidenceRef.EvidenceID}, "authority": service.policyAuthority,
		})
		if marshalErr != nil {
			return marshalErr
		}
		receipt, err = service.submitDeterministicCommand(ctx, feature, "tekroo.command.work-budget.amend", kernel.OperationalSchemaVersion, kernel.AggregateWorkBudget, account.ID, service.policyAuthority, account.Revision, payload, []kernel.DagParent{{ParentEventID: account.LastEventID, EdgeKind: kernel.EdgeCausal}}, evidence, key)
		if err != nil {
			return err
		}
		if receipt.OutcomeCode != kernel.OutcomeApplied || !receipt.StateChanged {
			return fmt.Errorf("%w: suspension budget amend outcome %s state-changed %t", organization.ErrInvalidFeature, receipt.OutcomeCode, receipt.StateChanged)
		}
	}
	return service.extendFeatureProfilesForRuntimeSuspension(ctx, feature, plan, targetDeadline, evidence)
}

func startedInvocationExists(invocations map[kernel.AggregateRef]kernel.WorkInvocation, plan organization.FeaturePlan) bool {
	for _, item := range plan.Tasks {
		if invocation, found := latestTaskInvocation(invocations, item.ID); found && invocation.State == kernel.InvocationStarted {
			return true
		}
	}
	return false
}

func (service *ProductionService) ensureRuntimeSuspensionEvidence(ctx context.Context, window mongo.RuntimeSuspensionWindow) (kernel.EvidenceRef, error) {
	receiptBytes, err := json.Marshal(window)
	if err != nil {
		return kernel.EvidenceRef{}, err
	}
	digest := digestBytes(receiptBytes)
	evidenceID := deterministicOperationalUUID("runtime-suspension-evidence", string(window.ID))
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateEvidence, ID: evidenceID}})
	if err != nil {
		return kernel.EvidenceRef{}, err
	}
	if metadata, found := snapshot.Evidence[evidenceID]; found {
		if !metadata.Available || metadata.SHA256 != digest {
			return kernel.EvidenceRef{}, organization.ErrInvalidFeature
		}
		return kernel.EvidenceRef{EvidenceID: evidenceID, SHA256: digest}, nil
	}
	payload, err := json.Marshal(map[string]any{
		"access_partition": "runtime-continuity:" + string(window.DeploymentIdentity), "availability": "AVAILABLE",
		"byte_length": len(receiptBytes), "canonical_digest": digest, "computation": nil, "deletion_tombstone": nil,
		"evidence_kind": "EXTERNAL_OBSERVATION", "integrity_state": "DIGEST_VERIFIED",
		"locator": "teams://runtime-suspension/" + string(window.ID), "locator_immutable": true,
		"media_type": "application/json", "producing_component": "tekrood-runtime-continuity",
		"producing_version": runtimeContinuityProducingVersion, "redacts": nil, "retention_policy": "deployment-lifecycle",
		"sensitivity": "INTERNAL", "sha256": digest, "source_evidence_ids": []kernel.UUIDv7{},
		"source_timestamp": window.ResumedAt, "transport_provenance": "tekrood-durable-heartbeat",
	})
	if err != nil {
		return kernel.EvidenceRef{}, err
	}
	if _, err := service.submitStandaloneCommand(ctx, "tekroo.command.evidence.register", kernel.AggregateEvidence, evidenceID, service.serviceAuthority, 0, payload, nil, nil, "runtime-suspension-evidence:"+string(window.ID)); err != nil {
		return kernel.EvidenceRef{}, err
	}
	return kernel.EvidenceRef{EvidenceID: evidenceID, SHA256: digest}, nil
}

func (service *ProductionService) runtimeSuspensionAmendmentReceipt(ctx context.Context, receipt kernel.CommandReceipt, budgetID kernel.UUIDv7, window mongo.RuntimeSuspensionWindow) (time.Time, []kernel.EvidenceRef, error) {
	// A replayed suspension returns its original receipt with NO_CHANGE and
	// StateChanged false; the idempotent replay is the success path after a
	// daemon restart, not a defect. Only foreign or rejected receipts are wrong.
	if receipt.CommandType != "tekroo.command.work-budget.amend" || receipt.Target != (kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: budgetID}) || (receipt.OutcomeCode != kernel.OutcomeApplied && receipt.OutcomeCode != kernel.OutcomeNoChange) || len(receipt.EventIDs) != 1 {
		return time.Time{}, nil, fmt.Errorf("%w: suspension receipt %s type %s outcome %s changed %t events %d", organization.ErrInvalidFeature, receipt.CommandID, receipt.CommandType, receipt.OutcomeCode, receipt.StateChanged, len(receipt.EventIDs))
	}
	event, found, err := service.Store.ReadEvent(ctx, receipt.EventIDs[0])
	if err != nil || !found {
		return time.Time{}, nil, fmt.Errorf("%w: suspension receipt event %s found %t: %w", organization.ErrInvalidFeature, receipt.EventIDs[0], found, err)
	}
	var payload struct {
		DeadlineAt  time.Time       `json:"deadline_at"`
		EvidenceIDs []kernel.UUIDv7 `json:"evidence_ids"`
	}
	expectedEvidenceID := deterministicOperationalUUID("runtime-suspension-evidence", string(window.ID))
	if json.Unmarshal(event.Payload, &payload) != nil || payload.DeadlineAt.IsZero() || !containsEveryUUID(payload.EvidenceIDs, []kernel.UUIDv7{expectedEvidenceID}) {
		return time.Time{}, nil, fmt.Errorf("%w: suspension amendment receipt %s payload unreadable (deadline %v evidence %d)", organization.ErrInvalidFeature, event.EventID, payload.DeadlineAt, len(payload.EvidenceIDs))
	}
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: receipt.Target})
	if err != nil {
		return time.Time{}, nil, err
	}
	evidence, err := evidenceRefsForIDs(snapshot, payload.EvidenceIDs)
	if err != nil {
		return time.Time{}, nil, err
	}
	return payload.DeadlineAt, evidence, nil
}

func (service *ProductionService) extendFeatureProfilesForRuntimeSuspension(ctx context.Context, feature organization.FeatureRequest, plan organization.FeaturePlan, deadline time.Time, evidence []kernel.EvidenceRef) error {
	if deadline.IsZero() || len(evidence) == 0 {
		return organization.ErrInvalidFeature
	}
	for _, item := range plan.Tasks {
		taskRef := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: item.ID}
		state, head, found, err := service.Store.ReadAggregateHead(ctx, taskRef)
		if err != nil {
			return err
		}
		if !found || state.Phase == kernel.PhaseCompleted {
			continue
		}
		snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: taskRef})
		if err != nil {
			return err
		}
		// Runtime-suspension recovery may extend work that has not started. Once
		// an invocation exists, its immutable profile and task recovery path own
		// continuity; rebinding here would stale completed evidence on restart.
		if _, found := latestTaskInvocation(snapshot.WorkInvocations, item.ID); found {
			continue
		}
		profileSnapshot, found := snapshot.WorkProfiles[taskRef]
		if !found || !profileSnapshot.Valid() {
			return fmt.Errorf("%w: suspension profile extension for task %s has no valid profile snapshot", organization.ErrInvalidFeature, item.ID)
		}
		if !profileSnapshot.Profile.Budgets.DeadlineAt.Before(deadline) {
			continue
		}
		condition := digestBytes([]byte("runtime-suspension-profile\x00" + string(feature.ID) + "\x00" + string(item.ID) + "\x00" + deadline.Format(time.RFC3339Nano)))
		// The successor keeps the base profile's classification evidence: the
		// suspension provenance is carried by the budget-amendment event and its
		// receipt, not by the profile slot. Appending one evidence id per window
		// accumulated past the Valid() cap of 64 after repeated host suspensions
		// and made every startup fail.
		successor, alreadyBound, err := planningRecoveryProfile(profileSnapshot.Profile, profileSnapshot.Profile.Binding(), service.planning, condition, deadline, nil)
		if err != nil {
			return fmt.Errorf("%w: suspension profile successor for task %s: %w", organization.ErrInvalidFeature, item.ID, err)
		}
		if alreadyBound {
			continue
		}
		profileEvidence, err := evidenceRefsForIDs(snapshot, successor.ClassificationEvidenceIDs)
		if err != nil {
			return fmt.Errorf("suspension profile evidence for task %s: %w", item.ID, err)
		}
		tracked := &trackedTask{plan: item, revision: state.Revision, last: head, profile: successor}
		key := "runtime-suspension-profile-evidence-complete-" + string(successor.ProfileID)
		if err := service.applyTaskCommand(ctx, feature, tracked, "tekroo.command.task.bind-work-profile", kernel.SchemaVersion, service.policyAuthority, successor, profileEvidence, nil, key); err != nil {
			return fmt.Errorf("suspension bind profile task %s rev %d: %w", item.ID, state.Revision, err)
		}
	}
	return nil
}
