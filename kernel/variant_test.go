package kernel

import (
	"encoding/json"
	"testing"
	"time"
)

func TestVariantGroupEnforcesIsolationAndMaterialDisagreementEscalation(t *testing.T) {
	group := testVariantGroup(t)
	first := testVariantCandidatePayload(t, UUIDv7("00000000-0000-7000-8000-000000000808"), ActorFQN("teams::coder-1"), UUIDv7("00000000-0000-7000-8000-000000000811"), "1")
	next, ok := ApplyVariantEvent(group, testVariantEvent(group, 2, "tekroo.event.variant-group.candidate-submitted", UUIDv7("00000000-0000-7000-8000-000000000821"), first))
	if !ok {
		t.Fatal("first independent candidate was rejected")
	}
	duplicateContext := testVariantCandidatePayload(t, UUIDv7("00000000-0000-7000-8000-000000000809"), ActorFQN("teams::coder-2"), UUIDv7("00000000-0000-7000-8000-000000000812"), "1")
	if _, accepted := ApplyVariantEvent(next, testVariantEvent(next, 3, "tekroo.event.variant-group.candidate-submitted", UUIDv7("00000000-0000-7000-8000-000000000822"), duplicateContext)); accepted {
		t.Fatal("candidate with copied context was accepted as independent")
	}
	second := testVariantCandidatePayload(t, UUIDv7("00000000-0000-7000-8000-000000000809"), ActorFQN("teams::coder-2"), UUIDv7("00000000-0000-7000-8000-000000000812"), "2")
	next, ok = ApplyVariantEvent(next, testVariantEvent(next, 3, "tekroo.event.variant-group.candidate-submitted", UUIDv7("00000000-0000-7000-8000-000000000823"), second))
	if !ok {
		t.Fatal("second independent candidate was rejected")
	}
	comparisonPayload, _ := json.Marshal(map[string]any{
		"variant_group_id": group.VariantGroupID, "expected_group_revision": 3,
		"comparator": group.Comparator, "comparator_execution_id": UUIDv7("00000000-0000-7000-8000-000000000813"),
		"candidate_ids": []UUIDv7{UUIDv7("00000000-0000-7000-8000-000000000808"), UUIDv7("00000000-0000-7000-8000-000000000809")},
		"classification": VariantMaterialDisagreement, "comparison_method_id": group.ComparisonMethodID,
		"comparison_receipt_digest": Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), "reasons": []string{"material difference"}, "evidence_ids": []UUIDv7{UUIDv7("00000000-0000-7000-8000-000000000806")},
	})
	next, ok = ApplyVariantEvent(next, testVariantEvent(next, 4, "tekroo.event.variant-group.comparison-recorded", UUIDv7("00000000-0000-7000-8000-000000000824"), comparisonPayload))
	if !ok || next.State != VariantCompared {
		t.Fatalf("comparison state = %#v", next)
	}
	selectPayload, _ := json.Marshal(map[string]any{
		"variant_group_id": group.VariantGroupID, "expected_group_revision": 4, "comparison_event_id": next.Comparison.EventID,
		"adjudicator": group.Adjudicator, "outcome": VariantSelectCandidate, "selected_candidate_id": UUIDv7("00000000-0000-7000-8000-000000000808"),
		"reasons": []string{"choose first"}, "evidence_ids": []UUIDv7{UUIDv7("00000000-0000-7000-8000-000000000806")},
	})
	if _, accepted := ApplyVariantEvent(next, testVariantEvent(next, 5, "tekroo.event.variant-group.selected", UUIDv7("00000000-0000-7000-8000-000000000825"), selectPayload)); accepted {
		t.Fatal("material disagreement selected a candidate without escalation")
	}
	escalatePayload, _ := json.Marshal(map[string]any{
		"variant_group_id": group.VariantGroupID, "expected_group_revision": 4, "comparison_event_id": next.Comparison.EventID,
		"adjudicator": group.Adjudicator, "outcome": VariantEscalate, "reasons": []string{"material disagreement"},
		"evidence_ids": []UUIDv7{UUIDv7("00000000-0000-7000-8000-000000000806")},
	})
	terminal, ok := ApplyVariantEvent(next, testVariantEvent(next, 5, "tekroo.event.variant-group.selected", UUIDv7("00000000-0000-7000-8000-000000000826"), escalatePayload))
	if !ok || terminal.State != VariantTerminal || terminal.Selection.Outcome != VariantEscalate {
		t.Fatalf("escalated state = %#v", terminal)
	}
}

func testVariantGroup(t *testing.T) VariantGroupSnapshot {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"variant_group_id": UUIDv7("00000000-0000-7000-8000-000000000807"), "task_id": UUIDv7("00000000-0000-7000-8000-000000000801"),
		"work_profile": WorkProfileBinding{ProfileID: UUIDv7("00000000-0000-7000-8000-000000000802"), ProfileRevision: 1, ProfileDigest: Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), LifecycleEpoch: 1, ScopeRevision: 1},
		"base_artifact_digest": Digest("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"), "input_evidence_set_digest": Digest("cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"),
		"toolchain_digest": Digest("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"), "dependency_lock_digest": Digest("eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"), "acceptance_manifest_digest": Digest("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"),
		"candidate_count": 2, "valid_candidate_quorum": 2, "required_independence_dimensions": []IndependenceDimension{IndependenceActor, IndependenceExecution, IndependenceContext, IndependenceWorkspace},
		"comparison_method_id": "structured-diff-v1", "comparison_policy_digest": Digest("1111111111111111111111111111111111111111111111111111111111111111"), "materiality_policy_digest": Digest("2222222222222222222222222222222222222222222222222222222222222222"),
		"comparator": PrincipalRef{Kind: PrincipalActor, ID: "teams::reviewer-1"}, "adjudicator": PrincipalRef{Kind: PrincipalHuman, ID: "principal"},
		"submission_deadline_at": "2026-08-14T00:00:00Z", "decision_deadline_at": "2026-08-15T00:00:00Z", "replacement_budget": 1,
		"evidence_ids": []UUIDv7{UUIDv7("00000000-0000-7000-8000-000000000806")},
	})
	group, err := VariantGroupFromOpenPayload(payload, UUIDv7("00000000-0000-7000-8000-000000000820"))
	if err != nil {
		t.Fatal(err)
	}
	return group
}

func testVariantCandidatePayload(t *testing.T, candidateID UUIDv7, actor ActorFQN, executionID UUIDv7, discriminator string) json.RawMessage {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"variant_group_id": UUIDv7("00000000-0000-7000-8000-000000000807"), "expected_group_revision": 1, "candidate_id": candidateID,
		"actor_fqn": actor, "execution_id": executionID, "fencing_epoch": 1,
		"model_profile_digest": Digest("3333333333333333333333333333333333333333333333333333333333333333"), "runtime_identity_digest": Digest("4444444444444444444444444444444444444444444444444444444444444444"),
		"context_digest": Digest(discriminator + "555555555555555555555555555555555555555555555555555555555555555"), "workspace_digest": Digest(discriminator + "666666666666666666666666666666666666666666666666666666666666666"),
		"artifact_digest": Digest(discriminator + "777777777777777777777777777777777777777777777777777777777777777"), "changed_file_inventory_digest": Digest(discriminator + "888888888888888888888888888888888888888888888888888888888888888"),
		"deterministic_gate_receipt_ids": []UUIDv7{UUIDv7("00000000-0000-7000-8000-000000000806")}, "assumptions": []string{}, "unresolved_exceptions": []string{}, "evidence_ids": []UUIDv7{UUIDv7("00000000-0000-7000-8000-000000000806")},
	})
	return payload
}

func testVariantEvent(group VariantGroupSnapshot, revision uint64, eventType string, eventID UUIDv7, payload json.RawMessage) DomainEvent {
	return DomainEvent{EventID: eventID, EventType: eventType, Aggregate: AggregateRef{Kind: AggregateVariantGroup, ID: group.VariantGroupID}, AggregateRevision: revision, CommittedAt: time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC), Payload: payload}
}
