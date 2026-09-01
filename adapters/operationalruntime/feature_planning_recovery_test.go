package operationalruntime

import (
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

func TestPlanningRecoveryRequestValidationAndDigestAreDeterministic(t *testing.T) {
	invocationID := kernel.UUIDv7("00000000-0000-7000-8000-000000000301")
	first := kernel.EvidenceRef{EvidenceID: "00000000-0000-7000-8000-000000000302", SHA256: repeatedDigest('a')}
	second := kernel.EvidenceRef{EvidenceID: "00000000-0000-7000-8000-000000000303", SHA256: repeatedDigest('b')}
	deadline := time.Date(2026, 9, 1, 15, 0, 0, 0, time.UTC)
	request := PlanningRecoveryRequest{ExpectedRevision: 5, Reason: "correct observed planning drift", EvidenceRefs: []kernel.EvidenceRef{second, first}, DeadlineAt: deadline, IdempotencyKey: "planning-recovery-1"}
	if !request.Valid() {
		t.Fatal("valid recovery request was rejected")
	}
	digest, err := planningRecoveryConditionDigest(invocationID, request)
	if err != nil {
		t.Fatal(err)
	}
	reordered := request
	reordered.EvidenceRefs = []kernel.EvidenceRef{first, second}
	reorderedDigest, err := planningRecoveryConditionDigest(invocationID, reordered)
	if err != nil {
		t.Fatal(err)
	}
	if digest != reorderedDigest {
		t.Fatalf("evidence order changed recovery digest: %s != %s", digest, reorderedDigest)
	}
	duplicate := request
	duplicate.EvidenceRefs = []kernel.EvidenceRef{first, first}
	if duplicate.Valid() {
		t.Fatal("duplicate recovery evidence was accepted")
	}
	withoutDeadline := request
	withoutDeadline.DeadlineAt = time.Time{}
	if withoutDeadline.Valid() {
		t.Fatal("recovery without a successor deadline was accepted")
	}
	changedDeadline := request
	changedDeadline.DeadlineAt = deadline.Add(time.Minute)
	changedDigest, err := planningRecoveryConditionDigest(invocationID, changedDeadline)
	if err != nil {
		t.Fatal(err)
	}
	if changedDigest == digest {
		t.Fatal("deadline change did not change the recovery condition")
	}
}

func TestPlanningRecoveryProfileSupersedesDeadlineAndIsIdempotent(t *testing.T) {
	tracked, _, _, _ := taskExecutionRefreshFixture(t)
	prior := tracked.profile.Binding()
	deadline := tracked.profile.Budgets.DeadlineAt.Add(time.Hour)
	evidenceID := kernel.UUIDv7("00000000-0000-7000-8000-000000000304")
	condition := repeatedDigest('c')
	planning := ProductionPlanning{PolicyRevision: 2, ClassificationPolicyDigest: repeatedDigest('d'), PromotionPolicyDigest: repeatedDigest('e'), VerificationTopologyDigest: repeatedDigest('f')}

	successor, alreadyBound, err := planningRecoveryProfile(tracked.profile, prior, planning, condition, deadline, []kernel.UUIDv7{evidenceID})
	if err != nil {
		t.Fatal(err)
	}
	if alreadyBound || successor.ProfileRevision != prior.ProfileRevision+1 || successor.ProfileID == prior.ProfileID || successor.SupersedesProfileID == nil || *successor.SupersedesProfileID != prior.ProfileID || !successor.Budgets.DeadlineAt.Equal(deadline) || !containsEveryUUID(successor.ClassificationEvidenceIDs, []kernel.UUIDv7{evidenceID}) {
		t.Fatalf("successor = %#v alreadyBound=%t", successor, alreadyBound)
	}
	reloaded, alreadyBound, err := planningRecoveryProfile(successor, prior, planning, condition, deadline, []kernel.UUIDv7{evidenceID})
	if err != nil || !alreadyBound || reloaded.ProfileDigest != successor.ProfileDigest {
		t.Fatalf("idempotent successor = %#v alreadyBound=%t err=%v", reloaded, alreadyBound, err)
	}
}

func TestFeatureBudgetPolicyAcceptsOnlyExpiredPredecessorForRecovery(t *testing.T) {
	now := time.Date(2026, 9, 1, 16, 0, 0, 0, time.UTC)
	planning := ProductionPlanning{PolicyRevision: 2, BudgetPolicyDigest: repeatedDigest('b')}
	account := kernel.WorkBudgetAccount{PolicyRevision: 1, PolicyDigest: repeatedDigest('a'), DeadlineAt: now.Add(time.Minute)}
	if featureBudgetPolicyAccepted(account, planning, now) {
		t.Fatal("unexpired predecessor policy was accepted")
	}
	account.DeadlineAt = now.Add(-time.Minute)
	if !featureBudgetPolicyAccepted(account, planning, now) {
		t.Fatal("expired predecessor policy was not accepted for recovery")
	}
	account.PolicyRevision = planning.PolicyRevision
	if featureBudgetPolicyAccepted(account, planning, now) {
		t.Fatal("current revision with the wrong digest was accepted")
	}
	account.PolicyDigest = planning.BudgetPolicyDigest
	if !featureBudgetPolicyAccepted(account, planning, now) {
		t.Fatal("current policy was rejected")
	}
}
