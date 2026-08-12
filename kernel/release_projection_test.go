package kernel

import (
	"encoding/json"
	"testing"
	"time"
)

func TestReleaseProjectionRetainsPartialOrderedProgressAndRejectsEarlyFinalization(t *testing.T) {
	plan := twoMergeReleasePlan()
	requestPayload, _ := json.Marshal(map[string]any{
		"release_plan_id": plan.ReleasePlanID, "expected_release_revision": plan.Revision, "plan_digest": plan.PlanDigest,
		"merge_id": plan.OrderedMerges[0].MergeID, "attempt_id": UUIDv7("00000000-0000-7000-8000-000000000755"), "round": 1,
		"provider_idempotency_key": "release-751-merge-752-round-1", "evidence_ids": []UUIDv7{"00000000-0000-7000-8000-000000000750"},
	})
	requested, ok := ApplyReleaseEvent(plan, releaseProjectionEvent("00000000-0000-7000-8000-000000000757", "tekroo.event.release-plan.execution-requested", 3, plan.ReleasePlanID, requestPayload))
	if !ok || requested.State != ReleaseExecuting {
		t.Fatalf("execution request = %#v, %t", requested, ok)
	}
	resultPayload, _ := json.Marshal(map[string]any{
		"release_plan_id": plan.ReleasePlanID, "expected_release_revision": requested.Revision, "plan_digest": plan.PlanDigest,
		"merge_id": plan.OrderedMerges[0].MergeID, "attempt_id": UUIDv7("00000000-0000-7000-8000-000000000755"), "outcome": "MERGED",
		"reasons": []string{"first planned head merged"}, "evidence_ids": []UUIDv7{"00000000-0000-7000-8000-000000000756"}, "observed_at": "2026-08-11T12:00:00Z",
		"observed_base_commit": plan.BaseCommit, "observed_head_commit": plan.OrderedMerges[0].HeadCommit, "observed_tree_digest": "4444444444444444444444444444444444444444",
	})
	partial, ok := ApplyReleaseEvent(requested, releaseProjectionEvent("00000000-0000-7000-8000-000000000758", "tekroo.event.release-plan.result-recorded", 4, plan.ReleasePlanID, resultPayload))
	if !ok || partial.State != ReleaseQualified || partial.NextMergeIndex != 1 || partial.NextRound != 1 || len(partial.Results) != 1 {
		t.Fatalf("partial release = %#v, %t", partial, ok)
	}
	finalPayload, _ := json.Marshal(map[string]any{
		"release_plan_id": plan.ReleasePlanID, "expected_release_revision": partial.Revision, "plan_digest": plan.PlanDigest, "release_mode": "CODE",
		"terminal_status": "READY_FOR_ACCEPTANCE", "qualification_event_id": plan.Qualification.EventID, "qualified_tree_digest": plan.ExpectedQualifiedTree,
		"provider_tree_digest": plan.ExpectedQualifiedTree, "result_event_ids": []UUIDv7{"00000000-0000-7000-8000-000000000758"},
	})
	if _, accepted := ApplyReleaseEvent(partial, releaseProjectionEvent("00000000-0000-7000-8000-000000000759", "tekroo.event.release-plan.finalized", 5, plan.ReleasePlanID, finalPayload)); accepted {
		t.Fatal("partially merged release finalized before the second ordered merge")
	}
}

func TestNoReleaseRequiredFinalizesWithoutProviderIdentity(t *testing.T) {
	plan := ReleasePlanSnapshot{
		ReleasePlanID: "00000000-0000-7000-8000-000000000771", OpeningEventID: "00000000-0000-7000-8000-000000000772", Revision: 1,
		State: ReleasePlanned, Mode: ReleaseModeNotRequired, Story: AggregateRef{Kind: AggregateStory, ID: "00000000-0000-7000-8000-000000000101"}, StoryLifecycleEpoch: 1, ExpectedStoryRevision: 8,
		Author: PrincipalRef{Kind: PrincipalHuman, ID: "principal-author"}, AuthorApprovalEventID: "00000000-0000-7000-8000-000000000749", AuthorApprovalRevision: 1,
		PolicyRevision: 1, PlanDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", EvidenceIDs: []UUIDv7{"00000000-0000-7000-8000-000000000750"},
		NoReleaseReason: "documentation-only story", NextRound: 1, Results: map[UUIDv7]ReleaseResult{},
	}
	if !plan.Valid() {
		t.Fatal("no-release plan fixture is invalid")
	}
	payload, _ := json.Marshal(map[string]any{
		"release_plan_id": plan.ReleasePlanID, "expected_release_revision": plan.Revision, "plan_digest": plan.PlanDigest,
		"release_mode": "NO_RELEASE_REQUIRED", "terminal_status": "NO_RELEASE_REQUIRED", "reasons": []string{"documentation-only story"},
		"evidence_ids": []UUIDv7{"00000000-0000-7000-8000-000000000750"},
	})
	final, ok := ApplyReleaseEvent(plan, releaseProjectionEvent("00000000-0000-7000-8000-000000000773", "tekroo.event.release-plan.finalized", 2, plan.ReleasePlanID, payload))
	if !ok || final.State != ReleaseNotRequired || !final.FinalizationEventID.Valid() || final.ProviderTreeDigest != "" || len(final.Results) != 0 {
		t.Fatalf("no-release finalization = %#v, %t", final, ok)
	}
}

func twoMergeReleasePlan() ReleasePlanSnapshot {
	plan := ReleasePlanSnapshot{
		ReleasePlanID: "00000000-0000-7000-8000-000000000751", OpeningEventID: "00000000-0000-7000-8000-000000000748", Revision: 2, State: ReleaseQualified, Mode: ReleaseModeCode,
		Story: AggregateRef{Kind: AggregateStory, ID: "00000000-0000-7000-8000-000000000101"}, StoryLifecycleEpoch: 1, ExpectedStoryRevision: 8,
		Author: PrincipalRef{Kind: PrincipalHuman, ID: "principal-author"}, AuthorApprovalEventID: "00000000-0000-7000-8000-000000000749", AuthorApprovalRevision: 1,
		PolicyRevision: 1, PlanDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", EvidenceIDs: []UUIDv7{"00000000-0000-7000-8000-000000000750"},
		RepositoryURL: "https://example.invalid/tekroo/teams.git", BaseRef: "main", BaseCommit: "1111111111111111111111111111111111111111",
		OrderedMerges: []ReleaseMergePlan{
			{MergeID: "00000000-0000-7000-8000-000000000752", ChangeRef: "refs/heads/story-1", HeadCommit: "2222222222222222222222222222222222222222", Role: "story"},
			{MergeID: "00000000-0000-7000-8000-000000000753", ChangeRef: "refs/heads/story-2", HeadCommit: "3333333333333333333333333333333333333333", Role: "story"},
		},
		MergeStrategy: "FF_ONLY_ORDERED", GitVersion: "git version 2.51.0", ConflictPolicy: "FAIL_NO_IMPROVISATION", ContractManifest: ContractIdentity,
		ManifestSHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", RequiredProfiles: []string{"contract-structure", "core-hermetic"},
		ExpectedQualifiedTree: "5555555555555555555555555555555555555555", ExecutionRoundLimit: 2, NextRound: 1, Results: map[UUIDv7]ReleaseResult{},
	}
	plan.Qualification = &ReleaseQualification{
		EventID: "00000000-0000-7000-8000-000000000754", QualificationID: "00000000-0000-7000-8000-000000000764", QualifiedBaseCommit: plan.BaseCommit,
		OrderedHeadCommits: []string{plan.OrderedMerges[0].HeadCommit, plan.OrderedMerges[1].HeadCommit}, QualifiedTreeDigest: plan.ExpectedQualifiedTree,
		GateDefinition: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", Toolchain: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		DependencyLock: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", ArtifactDigests: []Digest{"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"},
	}
	return plan
}

func releaseProjectionEvent(eventID UUIDv7, eventType string, revision uint64, planID UUIDv7, payload json.RawMessage) DomainEvent {
	return DomainEvent{EventID: eventID, EventType: eventType, Aggregate: AggregateRef{Kind: AggregateReleasePlan, ID: planID}, AggregateRevision: revision, CommittedAt: time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC), Payload: payload}
}
