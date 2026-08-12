package kernel_test

import (
	"encoding/json"
	"testing"

	"github.com/tekroo-ai/teams/kernel"
)

func TestCompletionRequiresExactEvidenceCriteriaValidationAndDependencies(t *testing.T) {
	evaluator := kernel.Evaluator{Catalogue: loadCatalogue(t)}
	context := validDecisionContext(t)
	evidenceID := mustUUID(t, "00000000-0000-7000-8000-0000000000e1")
	evidenceDigest := mustDigest(t, "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee")
	validationID := mustUUID(t, "00000000-0000-7000-8000-0000000000e2")
	reviewID := mustUUID(t, "00000000-0000-7000-8000-0000000000e5")
	parentID := mustUUID(t, "00000000-0000-7000-8000-0000000000e4")
	task := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: mustUUID(t, "00000000-0000-7000-8000-0000000000e3")}
	command := validStoryCreateCommand(t)
	command.CommandType = "tekroo.command.story.request-completion"
	command.ExpectedRevision = kernel.NewExpectedRevision(5)
	command.Payload = json.RawMessage(`{"lifecycle_epoch":1,"criteria_revision":2,"evidence_ids":["00000000-0000-7000-8000-0000000000e1"],"artifact_digests":[],"unresolved_exceptions":[],"completion_review_id":"00000000-0000-7000-8000-0000000000e5","completion_review_revision":3,"branch_policy_revision":1,"validation_finalized_event_id":"00000000-0000-7000-8000-0000000000e2"}`)
	command.EvidenceRefs = []kernel.EvidenceRef{{EvidenceID: evidenceID, SHA256: evidenceDigest}}
	command.Causation = []kernel.DagParent{{ParentEventID: parentID, EdgeKind: kernel.EdgeCausal}}
	command.Preconditions = []kernel.AggregatePrecondition{{Aggregate: task, Expected: kernel.NewExpectedRevision(3)}}
	snapshot := kernel.Snapshot{
		Exists: true, Revision: 5,
		State:    &kernel.AggregateState{Kind: kernel.AggregateStory, ID: command.Target.ID, Revision: 5, LifecycleEpoch: 1, Phase: kernel.PhaseActive, Condition: kernel.ConditionRunnable},
		Evidence: map[kernel.UUIDv7]kernel.EvidenceMetadata{evidenceID: {SHA256: evidenceDigest, Available: true}},
		AcceptedEvents: map[kernel.UUIDv7]kernel.AcceptedEvent{
			validationID: {EventType: "tekroo.event.completion-review.finalized", Qualification: "PASS"},
			parentID:     {EventType: "tekroo.event.story.activated"},
		},
		Reviews: map[kernel.AggregateRef]kernel.CompletionReviewSnapshot{{Kind: kernel.AggregateCompletionReview, ID: reviewID}: {
			Subject: command.Target, LifecycleEpoch: 1, CriteriaRevision: 2, BranchPolicyRevision: 1, ReviewRevision: 3,
			Finalization: &kernel.ReviewFinalization{EventID: validationID, ReviewRevision: 3, TerminalStatus: "PASS"},
		}},
		Related: map[kernel.AggregateRef]kernel.RelatedSnapshot{task: {
			Exists: true, Revision: 3,
			State: &kernel.AggregateState{Kind: kernel.AggregateTask, ID: task.ID, Revision: 3, LifecycleEpoch: 1, Phase: kernel.PhaseActive, Condition: kernel.ConditionRunnable},
		}},
	}
	grant := grantFor(command.Authority, command, context.Provenance.GrantDigests[0])
	snapshot.Authorization = authorizationPolicy(grant)
	snapshot.Authorization.Requirements.CompletionCriteriaRevision = 2

	if decision := evaluate(t, evaluator, command, snapshot, context); decision.Receipt.ReasonCode != "DEPENDENCY_NOT_READY" || decision.NextState != nil {
		t.Fatalf("active dependency decision = %#v", decision)
	}
	snapshot.Related[task].State.Phase = kernel.PhaseCompleted
	if decision := evaluate(t, evaluator, command, snapshot, context); decision.Receipt.OutcomeCode != kernel.OutcomeApplied || decision.NextState.Phase != kernel.PhaseCompleted {
		t.Fatalf("ready completion decision = %#v", decision)
	}
	reviewRef := kernel.AggregateRef{Kind: kernel.AggregateCompletionReview, ID: reviewID}
	currentReview := snapshot.Reviews[reviewRef]
	currentReview.ReviewRevision++
	snapshot.Reviews[reviewRef] = currentReview
	if decision := evaluate(t, evaluator, command, snapshot, context); decision.Receipt.ReasonCode != "REVIEW_NOT_FINALIZED" {
		t.Fatalf("stale finalized revision decision = %#v", decision.Receipt)
	}
	currentReview.ReviewRevision--
	snapshot.Reviews[reviewRef] = currentReview
	delete(snapshot.AcceptedEvents, validationID)
	if decision := evaluate(t, evaluator, command, snapshot, context); decision.Receipt.ReasonCode != "REVIEW_NOT_FINALIZED" {
		t.Fatalf("missing validation decision = %#v", decision.Receipt)
	}
	snapshot.AcceptedEvents[validationID] = kernel.AcceptedEvent{EventType: "tekroo.event.completion-review.finalized", Qualification: "PASS"}
	command.Payload = json.RawMessage(`{"lifecycle_epoch":1,"criteria_revision":1,"evidence_ids":["00000000-0000-7000-8000-0000000000e1"],"artifact_digests":[],"unresolved_exceptions":[],"completion_review_id":"00000000-0000-7000-8000-0000000000e5","completion_review_revision":3,"branch_policy_revision":1,"validation_finalized_event_id":"00000000-0000-7000-8000-0000000000e2"}`)
	if decision := evaluate(t, evaluator, command, snapshot, context); decision.Receipt.ReasonCode != "CRITERIA_REVISION_CONFLICT" {
		t.Fatalf("stale criteria decision = %#v", decision.Receipt)
	}
}

func TestAcceptanceRequiresExactFinalizedReleaseAndOtherTerminalPathsRemainExplicit(t *testing.T) {
	evaluator := kernel.Evaluator{Catalogue: loadCatalogue(t)}
	context := validDecisionContext(t)
	evidenceID := mustUUID(t, "00000000-0000-7000-8000-0000000000f1")
	evidenceDigest := mustDigest(t, "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
	parentID := mustUUID(t, "00000000-0000-7000-8000-000000000760")
	command := validStoryCreateCommand(t)
	command.CommandType = "tekroo.command.story.request-acceptance"
	command.ExpectedRevision = kernel.NewExpectedRevision(7)
	command.Payload = json.RawMessage(`{"lifecycle_epoch":1,"acceptance_policy_revision":4,"release_mode":"CODE","release_plan_id":"00000000-0000-7000-8000-000000000751","release_plan_revision":8,"release_finalized_event_id":"00000000-0000-7000-8000-000000000760","qualified_tree_digest":"3333333333333333333333333333333333333333","evidence_ids":["00000000-0000-7000-8000-0000000000f1"]}`)
	command.EvidenceRefs = []kernel.EvidenceRef{{EvidenceID: evidenceID, SHA256: evidenceDigest}}
	command.Causation = []kernel.DagParent{{ParentEventID: parentID, EdgeKind: kernel.EdgeResponse}}
	plan := releaseFixturePlan(t)
	plan.Story = command.Target
	plan.Revision = 8
	plan.State = kernel.ReleaseReadyForAcceptance
	plan.Qualification = releaseFixtureQualification(plan, mustUUID(t, "00000000-0000-7000-8000-000000000758"))
	plan.NextMergeIndex = uint64(len(plan.OrderedMerges))
	plan.FinalizationEventID = parentID
	plan.ProviderTreeDigest = plan.ExpectedQualifiedTree
	planRef := kernel.AggregateRef{Kind: kernel.AggregateReleasePlan, ID: plan.ReleasePlanID}
	command.Preconditions = []kernel.AggregatePrecondition{{Aggregate: planRef, Expected: kernel.NewExpectedRevision(plan.Revision)}}
	snapshot := kernel.Snapshot{
		Exists: true, Revision: 7,
		State:          &kernel.AggregateState{Kind: kernel.AggregateStory, ID: command.Target.ID, Revision: 7, LifecycleEpoch: 1, Phase: kernel.PhaseCompleted, Condition: kernel.ConditionRunnable},
		Evidence:       map[kernel.UUIDv7]kernel.EvidenceMetadata{evidenceID: {SHA256: evidenceDigest, Available: true}},
		AcceptedEvents: map[kernel.UUIDv7]kernel.AcceptedEvent{parentID: {EventType: "tekroo.event.release-plan.finalized"}},
		Related:        map[kernel.AggregateRef]kernel.RelatedSnapshot{planRef: {Exists: true, Revision: plan.Revision}},
		ReleasePlans:   map[kernel.AggregateRef]kernel.ReleasePlanSnapshot{planRef: plan},
	}
	grant := grantFor(command.Authority, command, context.Provenance.GrantDigests[0])
	snapshot.Authorization = authorizationPolicy(grant)
	snapshot.Authorization.Requirements.AcceptancePolicyRevision = 4
	if decision := evaluate(t, evaluator, command, snapshot, context); decision.Receipt.OutcomeCode != kernel.OutcomeApplied || decision.NextState == nil || decision.NextState.Phase != kernel.PhaseAccepted {
		t.Fatalf("acceptance decision = %#v", decision)
	}
	stalePlan := plan
	stalePlan.ProviderTreeDigest = "4444444444444444444444444444444444444444"
	snapshot.ReleasePlans[planRef] = stalePlan
	if decision := evaluate(t, evaluator, command, snapshot, context); decision.Receipt.OutcomeCode != kernel.OutcomeRejectedPolicy || decision.Receipt.ReasonCode != "RELEASE_GATE_FAILED" || decision.NextState != nil {
		t.Fatalf("tree mismatch decision = %#v", decision)
	}
	snapshot.ReleasePlans[planRef] = plan

	reopen := command
	reopen.CommandType = "tekroo.command.work.reopen"
	reopen.Payload = json.RawMessage(`{"prior_epoch":1,"new_scope_revision":2,"reason":"remediate","owner_carry_forward":false,"evidence_ids":["00000000-0000-7000-8000-0000000000f1"]}`)
	snapshot.Authorization = authorizationPolicy(grantFor(reopen.Authority, reopen, context.Provenance.GrantDigests[0]))
	owner := kernel.ActorFQN("teams::coder-1")
	snapshot.State.Ownership = kernel.Ownership{OwnerFQN: &owner, OwnershipVersion: 3}
	decision := evaluate(t, evaluator, reopen, snapshot, context)
	if decision.Receipt.OutcomeCode != kernel.OutcomeApplied || decision.NextState.LifecycleEpoch != 2 || decision.NextState.Ownership.OwnerFQN != nil {
		t.Fatalf("reopen decision = %#v", decision)
	}

	successor := reopen
	successor.CommandType = "tekroo.command.work.create-successor"
	successor.Payload = json.RawMessage(`{"successor_ids":["00000000-0000-7000-8000-0000000000f2"],"relation":"SUPERSESSION","ownership_rule":"UNOWNED","dependency_rule":"EXPLICIT","reason":"new subject"}`)
	successor.EvidenceRefs = nil
	snapshot.Authorization = authorizationPolicy(grantFor(successor.Authority, successor, context.Provenance.GrantDigests[0]))
	if decision := evaluate(t, evaluator, successor, snapshot, context); decision.Receipt.OutcomeCode != kernel.OutcomeApplied || decision.NextState.Phase != kernel.PhaseCompleted {
		t.Fatalf("successor decision = %#v", decision)
	}

	correction := successor
	correction.CommandType = "tekroo.command.record.correct"
	correction.Payload = json.RawMessage(`{"target_event_id":"00000000-0000-7000-8000-0000000000f3","corrected_fields":{"meaning":"corrected"},"reason":"audit correction","evidence_ids":["00000000-0000-7000-8000-0000000000f1"]}`)
	correction.EvidenceRefs = []kernel.EvidenceRef{{EvidenceID: evidenceID, SHA256: evidenceDigest}}
	snapshot.Authorization = authorizationPolicy(grantFor(correction.Authority, correction, context.Provenance.GrantDigests[0]))
	if decision := evaluate(t, evaluator, correction, snapshot, context); decision.Receipt.ReasonCode != "CORRECTION_TARGET_NOT_FOUND" {
		t.Fatalf("missing correction target = %#v", decision.Receipt)
	}
	targetEvent := mustUUID(t, "00000000-0000-7000-8000-0000000000f3")
	snapshot.AcceptedEvents[targetEvent] = kernel.AcceptedEvent{EventType: "tekroo.event.story.completed"}
	if decision := evaluate(t, evaluator, correction, snapshot, context); decision.Receipt.OutcomeCode != kernel.OutcomeApplied || snapshot.AcceptedEvents[targetEvent].EventType != "tekroo.event.story.completed" {
		t.Fatalf("correction decision = %#v", decision)
	}
}
