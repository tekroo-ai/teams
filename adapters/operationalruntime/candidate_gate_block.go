package operationalruntime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

const candidateGateReviewPolicy = "operator-or-project-manager-must-authorize-bounded-repair-or-clarification"

type candidateGateFinding struct {
	SchemaVersion  string                   `json:"schema_version"`
	FeatureID      kernel.UUIDv7            `json:"feature_id"`
	ConsumerTaskID kernel.UUIDv7            `json:"consumer_task_id"`
	Targets        []candidateTargetReceipt `json:"targets"`
	Failure        candidateGateFailure     `json:"failure"`
}

func gateFindingTargets(plan organization.FeaturePlan, consumer organization.PlannedTask, invocations map[kernel.UUIDv7]kernel.WorkInvocation) ([]candidateTargetReceipt, error) {
	ids := validationCandidateTargetIDs(plan, consumer)
	slices.Sort(ids)
	var targets []candidateTargetReceipt
	for _, id := range ids {
		inv, found := invocations[id]
		if !found || inv.State != kernel.InvocationSucceeded || inv.OutputDigest == nil || len(inv.TerminalEvidenceIDs) == 0 {
			return nil, errInvalidCandidateWorkspace
		}
		evidence := append([]kernel.UUIDv7(nil), inv.TerminalEvidenceIDs...)
		slices.Sort(evidence)
		targets = append(targets, candidateTargetReceipt{id, inv.ID, *inv.OutputDigest, evidence})
	}
	if len(targets) == 0 {
		return nil, errInvalidCandidateWorkspace
	}
	return targets, nil
}

func (service *ProductionService) registerGateFinding(ctx context.Context, feature organization.FeatureRequest, plan organization.FeaturePlan, task organization.PlannedTask, failure *candidateGateFailure, invocations map[kernel.UUIDv7]kernel.WorkInvocation) (kernel.EvidenceRef, error) {
	targets, err := gateFindingTargets(plan, task, invocations)
	if err != nil {
		return kernel.EvidenceRef{}, err
	}
	finding := candidateGateFinding{"tekroo.teams.candidate-gate-finding/1.0.0", feature.ID, task.ID, targets, *failure}
	content, err := json.Marshal(finding)
	if err != nil {
		return kernel.EvidenceRef{}, err
	}
	digest := digestBytes(content)
	id := deterministicOperationalUUID("candidate-gate-finding", string(feature.ID), string(task.ID), string(digest))
	path := filepath.Join(service.candidates.root, "gate-findings", string(digest)+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return kernel.EvidenceRef{}, err
	}
	if err := retainGateArtifact(path, content); err != nil {
		return kernel.EvidenceRef{}, err
	}
	var sources []kernel.UUIDv7
	for _, target := range targets {
		sources = append(sources, target.TerminalEvidenceIDs...)
	}
	slices.Sort(sources)
	sources = slices.Compact(sources)
	payload, err := json.Marshal(map[string]any{
		"access_partition": feature.Input.WorkspaceID, "availability": "AVAILABLE", "byte_length": len(content), "canonical_digest": digest,
		"computation":        map[string]any{"method": "tekrood-candidate-gate-finding-v1", "build_digest": digestBytes([]byte(finding.SchemaVersion)), "configuration_digest": failure.InputSHA256, "deterministic": true},
		"deletion_tombstone": nil, "evidence_kind": "ARTIFACT", "integrity_state": "DIGEST_VERIFIED",
		"locator": path, "locator_immutable": true, "media_type": "application/json",
		"producing_component": "tekrood-candidate-gate", "producing_version": finding.SchemaVersion, "redacts": nil,
		"retention_policy": "feature-lifecycle", "sensitivity": "INTERNAL", "sha256": digest, "source_evidence_ids": sources,
		"source_timestamp": nil, "transport_provenance": "teams-git-candidate-preflight",
	})
	if err != nil {
		return kernel.EvidenceRef{}, err
	}
	_, err = service.submitDeterministicCommand(ctx, feature, "tekroo.command.evidence.register", kernel.SchemaVersion, kernel.AggregateEvidence, id, service.serviceAuthority, 0, payload, nil, nil, "gate-finding-"+string(digest))
	return kernel.EvidenceRef{EvidenceID: id, SHA256: digest}, err
}

// This is a harness finding, not an invented tester invocation or a PASS
// receipt. Escalate with immutable diagnostics instead of spending another
// model turn blindly guessing which owner should repair a combined candidate.
func (service *ProductionService) blockCandidateGate(ctx context.Context, feature organization.FeatureRequest, plan organization.FeaturePlan, task *trackedTask, failure *candidateGateFailure, invocations map[kernel.UUIDv7]kernel.WorkInvocation) error {
	evidence, err := service.registerGateFinding(ctx, feature, plan, task.plan, failure, invocations)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{
		"blocker_refs": []string{"teams://evidence/" + string(evidence.EvidenceID)},
		"reason":       failure.Error(), "review_policy": candidateGateReviewPolicy,
	})
	if err != nil {
		return err
	}
	receipt, err := service.submitDeterministicActorTargetCommand(ctx, feature, "tekroo.command.work.block", kernel.AggregateTask, task.plan.ID, service.policyAuthority, task.owner.ActorFQN, task.owner.Execution, task.revision, task.profile.LifecycleEpoch, payload, []kernel.DagParent{{ParentEventID: task.last, EdgeKind: kernel.EdgeCausal}}, []kernel.EvidenceRef{evidence}, "candidate-gate-failed-"+string(task.plan.ID)+"-"+string(evidence.SHA256))
	if err != nil {
		return err
	}
	if len(receipt.EventIDs) != 1 {
		return errInvalidCandidateWorkspace
	}
	task.revision, task.last = revisionAfter(receipt, task.revision), receipt.EventIDs[0]
	return nil
}

func (service *ProductionService) readCandidateGateBlock(ctx context.Context, task organization.PlannedTask, head kernel.UUIDv7) (kernel.DomainEvent, kernel.EvidenceRef, bool, error) {
	event, found, err := service.Store.ReadEvent(ctx, head)
	if err != nil || !found {
		return event, kernel.EvidenceRef{}, false, errors.Join(errInvalidCandidateWorkspace, err)
	}
	// Deadline/continuity maintenance may append a profile binding without
	// resolving the blocker. Do not lose the durable finding behind that head.
	for depth := 0; event.EventType == "tekroo.event.task.work-profile-bound"; depth++ {
		if depth >= 32 || event.Aggregate != (kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}) {
			return event, kernel.EvidenceRef{}, false, errInvalidCandidateWorkspace
		}
		var predecessor *kernel.DomainEvent
		for _, parent := range event.Parents {
			candidate, found, err := service.Store.ReadEvent(ctx, parent.ParentEventID)
			if err != nil || !found {
				return event, kernel.EvidenceRef{}, false, errors.Join(errInvalidCandidateWorkspace, err)
			}
			if candidate.Aggregate == event.Aggregate {
				if predecessor != nil {
					return event, kernel.EvidenceRef{}, false, errInvalidCandidateWorkspace
				}
				predecessor = &candidate
			}
		}
		if predecessor == nil || predecessor.EventID == event.EventID {
			return event, kernel.EvidenceRef{}, false, errInvalidCandidateWorkspace
		}
		event = *predecessor
	}
	var payload planningOutputBlockPayload
	if event.EventType != "tekroo.event.work.blocked" || event.Authority != service.policyAuthority || event.Aggregate != (kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}) || json.Unmarshal(event.Payload, &payload) != nil || payload.ReviewPolicy != candidateGateReviewPolicy {
		return event, kernel.EvidenceRef{}, false, nil
	}
	if len(payload.BlockerRefs) != 1 || !strings.HasPrefix(payload.BlockerRefs[0], "teams://evidence/") {
		return event, kernel.EvidenceRef{}, false, errInvalidCandidateWorkspace
	}
	if event.ActorFQN == nil || event.Execution == nil || !event.ActorFQN.Valid() || !event.Execution.Valid() {
		return event, kernel.EvidenceRef{}, false, errInvalidCandidateWorkspace
	}
	id := kernel.UUIDv7(strings.TrimPrefix(payload.BlockerRefs[0], "teams://evidence/"))
	if !id.Valid() {
		return event, kernel.EvidenceRef{}, false, errInvalidCandidateWorkspace
	}
	_, evidenceHead, found, err := service.Store.ReadAggregateRevisionHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateEvidence, ID: id})
	if err != nil || !found {
		return event, kernel.EvidenceRef{}, false, errors.Join(errInvalidCandidateWorkspace, err)
	}
	registered, found, err := service.Store.ReadEvent(ctx, evidenceHead)
	var metadata struct {
		SHA256             kernel.Digest `json:"sha256"`
		ProducingComponent string        `json:"producing_component"`
	}
	if err != nil || !found || registered.EventType != "tekroo.event.evidence.registered" || registered.Authority != service.serviceAuthority || json.Unmarshal(registered.Payload, &metadata) != nil || !metadata.SHA256.Valid() || metadata.ProducingComponent != "tekrood-candidate-gate" {
		return event, kernel.EvidenceRef{}, false, errors.Join(errInvalidCandidateWorkspace, err)
	}
	return event, kernel.EvidenceRef{EvidenceID: id, SHA256: metadata.SHA256}, true, nil
}

// A successor implementation result is real changed-condition evidence. The
// same target outputs keep the node blocked across ticks and daemon restart.
// Toolchain/configuration changes require an explicit operator recovery.
func (service *ProductionService) reconcileCandidateGateBlocks(ctx context.Context, feature organization.FeatureRequest, plan organization.FeaturePlan, states map[kernel.UUIDv7]kernel.AggregateState, heads map[kernel.UUIDv7]kernel.UUIDv7, invocations map[kernel.UUIDv7]kernel.WorkInvocation, snapshot kernel.Snapshot) (bool, error) {
	for _, task := range plan.Tasks {
		state := states[task.ID]
		if state.Condition != kernel.ConditionBlocked {
			continue
		}
		blocked, evidence, gateBlocked, err := service.readCandidateGateBlock(ctx, task, heads[task.ID])
		if err != nil {
			return false, err
		}
		if !gateBlocked {
			continue
		}
		content, err := os.ReadFile(filepath.Join(service.candidates.root, "gate-findings", string(evidence.SHA256)+".json"))
		var finding candidateGateFinding
		if err != nil || digestBytes(content) != evidence.SHA256 || json.Unmarshal(content, &finding) != nil || finding.SchemaVersion != "tekroo.teams.candidate-gate-finding/1.0.0" || finding.FeatureID != feature.ID || finding.ConsumerTaskID != task.ID {
			return false, errors.Join(errInvalidCandidateWorkspace, err)
		}
		targets, err := gateFindingTargets(plan, task, invocations)
		if err != nil { // A repair is still in flight; there is no candidate yet.
			continue
		}
		if candidateTargetsEqual(targets, finding.Targets) {
			continue
		}
		profile, found := snapshot.WorkProfiles[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}]
		if !found || !profile.Valid() {
			return false, organization.ErrInvalidFeature
		}
		refs := []kernel.EvidenceRef{evidence}
		for _, target := range targets {
			for _, id := range target.TerminalEvidenceIDs {
				ref, found := snapshot.Evidence[id]
				if !found || !ref.Available || !ref.SHA256.Valid() {
					return false, errInvalidCandidateWorkspace
				}
				refs = append(refs, kernel.EvidenceRef{EvidenceID: id, SHA256: ref.SHA256})
			}
		}
		execution, found := snapshot.CurrentExecutions[*blocked.ActorFQN]
		if !found || !execution.Valid() {
			return false, errInvalidCandidateWorkspace
		}
		payload, err := json.Marshal(map[string]any{"resolved_blocker_refs": []string{"teams://evidence/" + string(evidence.EvidenceID)}, "evidence_ids": evidenceIDs(refs)})
		if err != nil {
			return false, err
		}
		encoded, _ := json.Marshal(targets)
		if _, err := service.submitDeterministicActorTargetCommand(ctx, feature, "tekroo.command.work.unblock", kernel.AggregateTask, task.ID, service.policyAuthority, *blocked.ActorFQN, execution, state.Revision, profile.Profile.LifecycleEpoch, payload, []kernel.DagParent{{ParentEventID: heads[task.ID], EdgeKind: kernel.EdgeResponse}}, refs, "candidate-gate-changed-"+string(task.ID)+"-"+string(digestBytes(encoded))); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}
