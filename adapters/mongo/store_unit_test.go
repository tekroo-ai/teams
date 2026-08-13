package mongo

import (
	"reflect"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestReleasePlanKeyIsStable(t *testing.T) {
	key := kernel.ReleasePlanKey{Story: kernel.AggregateRef{Kind: kernel.AggregateStory, ID: "00000000-0000-7000-8000-000000000001"}, LifecycleEpoch: 1}
	if releasePlanKey(key) != releasePlanKey(key) {
		t.Fatal("release-plan semantic key encoding is not stable")
	}
	otherLifecycle := key
	otherLifecycle.LifecycleEpoch = 2
	if releasePlanKey(key) == releasePlanKey(otherLifecycle) {
		t.Fatal("release-plan semantic key did not bind the lifecycle epoch")
	}
}

func TestOperatorHumanAndContinuityStateBSONRoundTrip(t *testing.T) {
	validFrom := time.Date(2026, time.August, 13, 0, 0, 0, 0, time.UTC)
	operator := kernel.OperatorRoleProfile{BindingID: "00000000-0000-7000-8000-000000000901", RoleID: "operator", OperatorActorFQN: "teams::operator-1", RoleBundleVersion: "1.0.0", RoleBundleDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", RoleDefinitionDigest: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", CapabilityIDs: []string{"coordinate"}, HumanSelectionPolicyRevision: 1, HumanSelectionPolicyDigest: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", AuthorityPolicyRevision: 1, AuthorityPolicyDigest: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}
	participant := kernel.HumanParticipantSnapshot{Participant: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "human:alice"}, Revision: 1, ProfileID: "00000000-0000-7000-8000-000000000902", ProfileRevision: 1, ProfileDigest: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", PrivacyClassification: kernel.ConfidentialityInternal, RoleBindings: []kernel.HumanRoleBinding{{RoleBindingID: "00000000-0000-7000-8000-000000000903", RoleClass: "SME", RoleID: "payments-sme", ScopeKind: "PROJECT", ScopeID: "tekroo-v4", AuthorityPolicyRevision: 1, AuthorityPolicyDigest: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", ValidFrom: validFrom, Active: true}}, ParticipantPolicyRevision: 1, ParticipantPolicyDigest: "1111111111111111111111111111111111111111111111111111111111111111", Active: true}
	interaction := kernel.HumanInteractionSnapshot{InteractionID: "00000000-0000-7000-8000-000000000904", Revision: 1, Phase: kernel.HumanInteractionOpen, Subject: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: "00000000-0000-7000-8000-000000000905"}, SubjectLifecycleEpoch: 1, ExpectedSubjectRevision: 1, QuestionRevision: 1, CanonicalQuestionDigest: "2222222222222222222222222222222222222222222222222222222222222222", ResponseSpecificationDigest: "3333333333333333333333333333333333333333333333333333333333333333", OriginPrincipal: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "human:requester"}, ResponsePolicy: kernel.HumanResponsePolicy{Kind: kernel.HumanResponseExactOne}, Purpose: "ADVISORY_CONSULTATION", DeclaredEffect: "ADVISORY_ONLY", Confidentiality: kernel.ConfidentialityInternal, DeadlineAt: validFrom.Add(time.Hour), TimeoutPolicy: kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "human-timeout-policy"}, DisclosureScopeDigest: "1212121212121212121212121212121212121212121212121212121212121212", InteractionPolicyRevision: 1, InteractionPolicyDigest: "1313131313131313131313131313131313131313131313131313131313131313"}
	continuity := kernel.TeamContinuitySnapshot{Revision: 1, OperatingPosture: "CONTINUOUS", ControlState: kernel.ContinuityActive, PowerEpoch: 7, AdmissionOpen: true, ContinuityPolicyRevision: 1, ContinuityPolicyDigest: "4444444444444444444444444444444444444444444444444444444444444444", HealthRequirementDigest: "5555555555555555555555555555555555555555555555555555555555555555", LastTransitionEventID: "00000000-0000-7000-8000-000000000906"}
	state := kernel.AggregateState{Kind: kernel.AggregateSystem, ID: "00000000-0000-7000-8000-000000000907", Revision: 1, LifecycleEpoch: 1, ScopeRevision: 1, Phase: kernel.PhaseDraft, Condition: kernel.ConditionRunnable, OperatorRole: &operator, Participant: &participant, Interaction: &interaction, Continuity: &continuity}
	encoded, err := bson.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var decoded kernel.AggregateState
	if err := bson.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, state) {
		t.Fatalf("BSON round trip changed state: got %#v want %#v", decoded, state)
	}
}
