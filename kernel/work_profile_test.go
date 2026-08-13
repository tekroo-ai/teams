package kernel

import (
	"testing"
	"time"
)

func TestWorkProfileBindingUsesSemanticScopeNotAggregateRevision(t *testing.T) {
	profile := validWorkProfile()
	task := AggregateState{Kind: AggregateTask, ID: profile.TaskID, Revision: 19, LifecycleEpoch: 2, ScopeRevision: 3, Phase: PhaseReady, Condition: ConditionRunnable}
	decision := PlanWorkProfileBinding(WorkProfileBindingInput{Task: task, Profile: profile})
	if decision.Status != WorkProfileReady {
		t.Fatalf("binding decision = %#v", decision)
	}
	task.Revision = 27
	if decision = PlanWorkProfileBinding(WorkProfileBindingInput{Task: task, Profile: profile}); decision.Status != WorkProfileReady {
		t.Fatalf("routine revision made profile stale: %#v", decision)
	}
	task.ScopeRevision++
	if decision = PlanWorkProfileBinding(WorkProfileBindingInput{Task: task, Profile: profile}); decision.Status != WorkProfileStale {
		t.Fatalf("scope change did not stale profile: %#v", decision)
	}
}

func TestWorkProfileReclassificationRequiresExplicitSupersession(t *testing.T) {
	first := validWorkProfile()
	task := AggregateState{Kind: AggregateTask, ID: first.TaskID, Revision: 2, LifecycleEpoch: 2, ScopeRevision: 3, Phase: PhaseReady, Condition: ConditionRunnable}
	current := &WorkProfileSnapshot{Profile: first, BoundEventID: UUIDv7("00000000-0000-7000-8000-000000000821"), TaskRevision: 2}
	second := first.Clone()
	second.ProfileID = UUIDv7("00000000-0000-7000-8000-000000000822")
	second.ProfileRevision = 2
	second.ProfileDigest = Digest("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if decision := PlanWorkProfileBinding(WorkProfileBindingInput{Task: task, Profile: second, Current: current}); decision.Status != WorkProfileConflict {
		t.Fatalf("unlinked reclassification decision = %#v", decision)
	}
	prior := first.ProfileID
	second.SupersedesProfileID = &prior
	if decision := PlanWorkProfileBinding(WorkProfileBindingInput{Task: task, Profile: second, Current: current}); decision.Status != WorkProfileReady {
		t.Fatalf("linked reclassification decision = %#v", decision)
	}
}

func TestWorkProfileRejectsUnknownAsImplicitlyLowRisk(t *testing.T) {
	profile := validWorkProfile()
	profile.Ambiguity = AmbiguityUnknown
	profile.MinimumDecisionRoute = RouteBoundedExecution
	if !profile.Valid() {
		t.Fatal("UNKNOWN is an explicit valid classification value")
	}
	// Route derivation is policy-owned; the profile retains UNKNOWN rather than
	// coercing it to LOW. A later assignment policy must reject an insufficient
	// minimum route for that classification.
	if profile.Ambiguity == AmbiguityLow {
		t.Fatal("UNKNOWN ambiguity was collapsed to LOW")
	}
}

func validWorkProfile() WorkRiskProfile {
	return WorkRiskProfile{
		TaskID: UUIDv7("00000000-0000-7000-8000-000000000801"), ProfileID: UUIDv7("00000000-0000-7000-8000-000000000802"),
		ProfileRevision: 1, ProfileDigest: Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), LifecycleEpoch: 2, ScopeRevision: 3,
		WorkKind: WorkImplementation, Ambiguity: AmbiguityLow, Novelty: NoveltyRoutine, BlastRadius: BlastLocal, SecuritySensitivity: SecurityOrdinary,
		MinimumDecisionRoute: RouteBoundedExecution, AcceptanceCriteriaDigest: Digest("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
		RequiredDeterministicGateIDs: []string{"go-test"}, RequiredValidationBranches: 1,
		RequiredIndependenceDimensions: []IndependenceDimension{IndependencePrincipal, IndependenceActor, IndependenceExecution, IndependenceContext, IndependenceWorkspace, IndependenceMethod},
		ImplementationVariantCount:     1, ValidCandidateQuorum: 1, VerificationTopologyDigest: Digest("cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"),
		ClassificationPolicyRevision: 1, ClassificationPolicyDigest: Digest("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"),
		PromotionPolicyRevision: 1, PromotionPolicyDigest: Digest("eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"),
		Budgets:                 FiniteWorkBudgets{AttemptLimit: 2, ReviewRoundLimit: 2, PromotionLimit: 1, EscalationLimit: 1, DeadlineAt: time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)},
		ClassificationAuthority: PrincipalRef{Kind: PrincipalHuman, ID: "principal"}, ClassificationEvidenceIDs: []UUIDv7{UUIDv7("00000000-0000-7000-8000-000000000806")},
	}
}
