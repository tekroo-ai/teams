package operationalruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

const completionReviewRecoverySchema = "1.0"

type completionReviewTimeoutBranch struct {
	BranchID   string        `json:"branch_id"`
	DeadlineAt time.Time     `json:"deadline_at"`
	Result     string        `json:"result"`
	ResultID   kernel.UUIDv7 `json:"result_event_id"`
}

type completionReviewTimeoutReceipt struct {
	SchemaVersion           string                          `json:"schema_version"`
	ReviewID                kernel.UUIDv7                   `json:"review_id"`
	Subject                 kernel.AggregateRef             `json:"subject"`
	LifecycleEpoch          uint64                          `json:"lifecycle_epoch"`
	CandidateArtifactDigest kernel.Digest                   `json:"candidate_artifact_digest"`
	Branches                []completionReviewTimeoutBranch `json:"branches"`
	FinalStatus             string                          `json:"final_status"`
	FinalizationEventID     kernel.UUIDv7                   `json:"finalization_event_id"`
}

// recoverExpiredCompletionReviews closes only expired, unfinished reviews for
// the same immutable candidate. Their retained timeout receipts become input
// evidence for a successor review, so a daemon or laptop outage can resume the
// completed work without rerunning the agent or weakening the review rules.
func (service *ProductionService) recoverExpiredCompletionReviews(ctx context.Context, feature organization.FeatureRequest, subject kernel.AggregateRef, candidate kernel.Digest, successorID kernel.UUIDv7, snapshot kernel.Snapshot, evidence []kernel.EvidenceRef) ([]kernel.EvidenceRef, error) {
	reviews := make([]kernel.CompletionReviewSnapshot, 0)
	for _, review := range snapshot.Reviews {
		if review.Subject != subject || review.LifecycleEpoch != feature.LifecycleEpoch || review.CandidateArtifactDigest != candidate {
			continue
		}
		if review.Finalization == nil {
			reviews = append(reviews, review)
		}
	}
	sort.Slice(reviews, func(left, right int) bool { return reviews[left].ReviewID < reviews[right].ReviewID })

	result := append([]kernel.EvidenceRef(nil), evidence...)
	for _, review := range reviews {
		updated, closed, err := service.closeExpiredCompletionReview(ctx, feature, review, evidence)
		if err != nil {
			return nil, err
		}
		if !closed {
			if review.ReviewID == successorID {
				continue
			}
			return nil, fmt.Errorf("completion review %s is still active", review.ReviewID)
		}
		review = updated
		if !completionReviewTimedOut(review) {
			continue
		}
		reference, err := service.ensureCompletionReviewTimeoutEvidence(ctx, feature, review)
		if err != nil {
			return nil, err
		}
		result = appendUniqueEvidence(result, reference)
	}
	if len(result) == 0 || len(result) > 64 {
		return nil, errors.New("invalid completion review recovery evidence set")
	}
	sort.Slice(result, func(left, right int) bool { return result[left].EvidenceID < result[right].EvidenceID })
	return result, nil
}

func (service *ProductionService) prepareCompletionReview(ctx context.Context, feature organization.FeatureRequest, subject kernel.AggregateRef, candidate kernel.Digest, identityPrefix string, identityParts []string, snapshot kernel.Snapshot, evidence []kernel.EvidenceRef) ([]kernel.EvidenceRef, kernel.UUIDv7, error) {
	result, err := service.appendFinalizedCompletionReviewRecoveryEvidence(ctx, feature, subject, candidate, snapshot, evidence)
	if err != nil {
		return nil, "", err
	}
	reviewID := completionReviewIdentity(identityPrefix, identityParts, candidate, result)
	result, err = service.recoverExpiredCompletionReviews(ctx, feature, subject, candidate, reviewID, snapshot, result)
	if err != nil {
		return nil, "", err
	}
	return result, completionReviewIdentity(identityPrefix, identityParts, candidate, result), nil
}

func (service *ProductionService) appendFinalizedCompletionReviewRecoveryEvidence(ctx context.Context, feature organization.FeatureRequest, subject kernel.AggregateRef, candidate kernel.Digest, snapshot kernel.Snapshot, evidence []kernel.EvidenceRef) ([]kernel.EvidenceRef, error) {
	reviews := make([]kernel.CompletionReviewSnapshot, 0)
	for _, review := range snapshot.Reviews {
		if review.Subject == subject && review.LifecycleEpoch == feature.LifecycleEpoch && review.CandidateArtifactDigest == candidate && completionReviewTimedOut(review) {
			reviews = append(reviews, review)
		}
	}
	sort.Slice(reviews, func(left, right int) bool { return reviews[left].ReviewID < reviews[right].ReviewID })
	result := append([]kernel.EvidenceRef(nil), evidence...)
	for _, review := range reviews {
		reference, err := service.ensureCompletionReviewTimeoutEvidence(ctx, feature, review)
		if err != nil {
			return nil, err
		}
		result = appendUniqueEvidence(result, reference)
	}
	if len(result) == 0 || len(result) > 64 {
		return nil, errors.New("invalid completion review recovery evidence set")
	}
	sort.Slice(result, func(left, right int) bool { return result[left].EvidenceID < result[right].EvidenceID })
	return result, nil
}

func completionReviewIdentity(prefix string, parts []string, candidate kernel.Digest, evidence []kernel.EvidenceRef) kernel.UUIDv7 {
	encoded, _ := json.Marshal(evidence)
	identity := append([]string{prefix}, parts...)
	identity = append(identity, string(candidate), string(digestBytes(encoded)))
	return deterministicOperationalUUID(identity...)
}

func (service *ProductionService) closeExpiredCompletionReview(ctx context.Context, feature organization.FeatureRequest, review kernel.CompletionReviewSnapshot, evidence []kernel.EvidenceRef) (kernel.CompletionReviewSnapshot, bool, error) {
	if review.Finalization != nil {
		return review, completionReviewTimedOut(review), nil
	}
	missing := make([]kernel.ReviewBranchSpec, 0)
	for _, branchID := range review.RequiredBranchIDs {
		if _, recorded := review.ResultRecords[branchID]; recorded {
			continue
		}
		branch, found := review.Branches[branchID]
		if !found || !service.clock.Now().UTC().After(branch.DeadlineAt.UTC()) {
			return review, false, nil
		}
		missing = append(missing, branch)
	}
	if len(missing) == 0 {
		return review, false, nil
	}
	sort.Slice(missing, func(left, right int) bool { return missing[left].BranchID < missing[right].BranchID })

	reviewRef := kernel.AggregateRef{Kind: kernel.AggregateCompletionReview, ID: review.ReviewID}
	revision, parent, found, err := service.Store.ReadAggregateRevisionHead(ctx, reviewRef)
	if err != nil || !found || revision != review.ReviewRevision {
		return review, false, errors.Join(organization.ErrInvalidFeature, err)
	}
	for _, branch := range missing {
		comparison, _ := json.Marshal(map[string]any{"policy": service.policyAuthority, "review_id": review.ReviewID, "branch_id": branch.BranchID})
		payload, _ := json.Marshal(map[string]any{
			"review_id": review.ReviewID, "branch_id": branch.BranchID, "branch_policy_revision": review.BranchPolicyRevision,
			"source_role": "POLICY_TIMEOUT", "round": uint64(1), "result": "BLOCKED",
			"reasons":      []string{"the deterministic completion review remained unfinished after its bound deadline"},
			"evidence_ids": evidenceIDs(evidence), "findings": []any{}, "supersedes_result_event_ids": []kernel.UUIDv7{}, "changed_condition_evidence_ids": []kernel.UUIDv7{},
			"candidate_artifact_digest": review.CandidateArtifactDigest,
			"independence_receipt":      map[string]any{"proven_dimensions": branch.RequiredIndependenceDimensions, "identity_comparison_digest": digestBytes(comparison), "method_ids": branch.RequiredMethodIDs, "evidence_ids": evidenceIDs(evidence)},
		})
		timeoutKey := fmt.Sprintf("review-timeout-v2-%s-%s-policy-%d", review.ReviewID, branch.BranchID, service.provenance.PolicyRevision)
		recorded, submitErr := service.submitDeterministicCommand(ctx, feature, "tekroo.command.completion-review.record-result", kernel.SchemaVersion, kernel.AggregateCompletionReview, review.ReviewID, service.policyAuthority, revision, payload, []kernel.DagParent{{ParentEventID: parent, EdgeKind: kernel.EdgeResponse}}, evidence, timeoutKey)
		if submitErr != nil {
			return review, false, submitErr
		}
		parent = recorded.EventIDs[0]
		revision++
	}

	fresh, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: reviewRef})
	if err != nil {
		return review, false, err
	}
	updated, found := fresh.Reviews[reviewRef]
	if !found || !updated.Join.Complete || updated.Join.Status != "BLOCKED" {
		return review, false, organization.ErrInvalidFeature
	}
	resultIDs := make([]kernel.UUIDv7, 0, len(updated.ResultRecords))
	for _, result := range updated.ResultRecords {
		resultIDs = append(resultIDs, result.EventID)
	}
	sort.Slice(resultIDs, func(left, right int) bool { return resultIDs[left] < resultIDs[right] })
	finalPayload, _ := json.Marshal(map[string]any{
		"review_id": updated.ReviewID, "subject_kind": updated.Subject.Kind, "subject_id": updated.Subject.ID,
		"lifecycle_epoch": updated.LifecycleEpoch, "branch_policy_revision": updated.BranchPolicyRevision,
		"expected_review_revision": updated.ReviewRevision, "terminal_status": updated.Join.Status,
		"result_event_ids": resultIDs, "evidence_ids": evidenceIDs(evidence), "scope_revision": updated.ScopeRevision,
		"work_profile": updated.WorkProfile, "candidate_artifact_digest": updated.CandidateArtifactDigest,
		"verification_topology_digest": updated.VerificationTopologyDigest,
	})
	revision, parent, found, err = service.Store.ReadAggregateRevisionHead(ctx, reviewRef)
	if err != nil || !found || revision != updated.ReviewRevision {
		return review, false, errors.Join(organization.ErrInvalidFeature, err)
	}
	_, err = service.submitDeterministicCommand(ctx, feature, "tekroo.command.completion-review.finalize", kernel.SchemaVersion, kernel.AggregateCompletionReview, updated.ReviewID, service.policyAuthority, updated.ReviewRevision, finalPayload, []kernel.DagParent{{ParentEventID: parent, EdgeKind: kernel.EdgeResponse}}, evidence, "review-timeout-finalize-"+string(updated.ReviewID))
	if err != nil {
		return review, false, err
	}
	fresh, err = service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: reviewRef})
	if err != nil {
		return review, false, err
	}
	updated, found = fresh.Reviews[reviewRef]
	if !found || !completionReviewTimedOut(updated) {
		return review, false, organization.ErrInvalidFeature
	}
	return updated, true, nil
}

func (service *ProductionService) ensureCompletionReviewTimeoutEvidence(ctx context.Context, feature organization.FeatureRequest, review kernel.CompletionReviewSnapshot) (kernel.EvidenceRef, error) {
	if !completionReviewTimedOut(review) {
		return kernel.EvidenceRef{}, organization.ErrInvalidFeature
	}
	branches := make([]completionReviewTimeoutBranch, 0)
	var sourceTimestamp time.Time
	for branchID, result := range review.ResultRecords {
		if result.SourceRole != "POLICY_TIMEOUT" {
			continue
		}
		branch := review.Branches[branchID]
		branches = append(branches, completionReviewTimeoutBranch{BranchID: branchID, DeadlineAt: branch.DeadlineAt.UTC(), Result: result.Result, ResultID: result.EventID})
		if branch.DeadlineAt.After(sourceTimestamp) {
			sourceTimestamp = branch.DeadlineAt.UTC()
		}
	}
	sort.Slice(branches, func(left, right int) bool { return branches[left].BranchID < branches[right].BranchID })
	receipt := completionReviewTimeoutReceipt{
		SchemaVersion: completionReviewRecoverySchema, ReviewID: review.ReviewID, Subject: review.Subject,
		LifecycleEpoch: review.LifecycleEpoch, CandidateArtifactDigest: review.CandidateArtifactDigest,
		Branches: branches, FinalStatus: review.Finalization.TerminalStatus, FinalizationEventID: review.Finalization.EventID,
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return kernel.EvidenceRef{}, err
	}
	digest := digestBytes(encoded)
	evidenceID := deterministicOperationalUUID("completion-review-timeout-evidence", string(review.ReviewID), string(review.Finalization.EventID), string(digest))
	ref := kernel.AggregateRef{Kind: kernel.AggregateEvidence, ID: evidenceID}
	snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: ref})
	if err != nil {
		return kernel.EvidenceRef{}, err
	}
	if metadata, found := snapshot.Evidence[evidenceID]; found {
		if !metadata.Available || metadata.SHA256 != digest {
			return kernel.EvidenceRef{}, organization.ErrInvalidFeature
		}
		return kernel.EvidenceRef{EvidenceID: evidenceID, SHA256: digest}, nil
	}
	sourceIDs := completionReviewSourceEvidenceIDs(review)
	payload, _ := json.Marshal(map[string]any{
		"access_partition": feature.Input.WorkspaceID, "availability": "AVAILABLE", "byte_length": len(encoded),
		"canonical_digest": digest, "computation": nil, "deletion_tombstone": nil, "evidence_kind": "DECISION_RECORD",
		"integrity_state": "DIGEST_VERIFIED", "locator": "teams://completion-review/" + string(review.ReviewID) + "/timeout-receipt",
		"locator_immutable": true, "media_type": "application/json", "producing_component": "tekrood-completion-review-recovery",
		"producing_version": FeaturePlanningVersion, "redacts": nil, "retention_policy": "feature-lifecycle", "sensitivity": "INTERNAL",
		"sha256": digest, "source_evidence_ids": sourceIDs, "source_timestamp": sourceTimestamp,
		"transport_provenance": "teams-durable-completion-review-projection",
	})
	if _, err := service.submitDeterministicCommand(ctx, feature, "tekroo.command.evidence.register", kernel.SchemaVersion, kernel.AggregateEvidence, evidenceID, service.serviceAuthority, 0, payload, nil, nil, "completion-review-timeout-evidence-"+string(review.ReviewID)); err != nil {
		return kernel.EvidenceRef{}, err
	}
	return kernel.EvidenceRef{EvidenceID: evidenceID, SHA256: digest}, nil
}

func completionReviewTimedOut(review kernel.CompletionReviewSnapshot) bool {
	if review.Finalization == nil || (review.Finalization.TerminalStatus != "BLOCKED" && review.Finalization.TerminalStatus != "CANCELLED") {
		return false
	}
	for _, result := range review.ResultRecords {
		if result.SourceRole == "POLICY_TIMEOUT" {
			return true
		}
	}
	return false
}

func completionReviewSourceEvidenceIDs(review kernel.CompletionReviewSnapshot) []kernel.UUIDv7 {
	seen := make(map[kernel.UUIDv7]struct{})
	for _, branch := range review.Branches {
		for _, id := range branch.InputEvidenceIDs {
			seen[id] = struct{}{}
		}
	}
	result := make([]kernel.UUIDv7, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}
	sort.Slice(result, func(left, right int) bool { return result[left] < result[right] })
	return result
}

func appendUniqueEvidence(values []kernel.EvidenceRef, candidate kernel.EvidenceRef) []kernel.EvidenceRef {
	for _, value := range values {
		if value.EvidenceID == candidate.EvidenceID {
			return values
		}
	}
	return append(values, candidate)
}
