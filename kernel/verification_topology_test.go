package kernel

import "testing"

func TestRequiredSeparationUsesExactPrincipalActorAndExecution(t *testing.T) {
	implementer := WorkExecutionIdentity{
		Principal: PrincipalRef{Kind: PrincipalActor, ID: "teams::coder-1"}, ActorFQN: ActorFQN("teams::coder-1"),
		ExecutionID: UUIDv7("00000000-0000-7000-8000-000000000901"), FencingEpoch: 3,
		ModelProfileDigest: Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), WorkspaceDigest: Digest("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"), ContextDigest: Digest("cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"),
	}
	required := []IndependenceDimension{IndependencePrincipal, IndependenceActor, IndependenceExecution, IndependenceMethod}
	receipt := IndependenceReceipt{ProvenDimensions: required, IdentityComparisonDigest: Digest("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"), MethodIDs: []string{"go-test"}, EvidenceIDs: []UUIDv7{UUIDv7("00000000-0000-7000-8000-000000000902")}}
	validatorPrincipal := PrincipalRef{Kind: PrincipalActor, ID: "teams::reviewer-1"}
	validatorActor := ActorFQN("teams::reviewer-1")
	validatorExecution := ExecutionTuple{ExecutionID: UUIDv7("00000000-0000-7000-8000-000000000903"), FencingEpoch: 1}
	if !RequiredSeparationProven(implementer, validatorPrincipal, &validatorActor, &validatorExecution, required, receipt) {
		t.Fatal("exact independent validator was rejected")
	}
	sameActor := implementer.ActorFQN
	if RequiredSeparationProven(implementer, validatorPrincipal, &sameActor, &validatorExecution, required, receipt) {
		t.Fatal("same actor was treated as actor-independent")
	}
	sameExecution := implementer.Execution()
	if RequiredSeparationProven(implementer, validatorPrincipal, &validatorActor, &sameExecution, required, receipt) {
		t.Fatal("same execution was treated as execution-independent")
	}
}

func TestIndependenceReceiptMustProveEveryRequiredDimensionAndMethod(t *testing.T) {
	receipt := IndependenceReceipt{ProvenDimensions: []IndependenceDimension{IndependenceActor}, IdentityComparisonDigest: Digest("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"), MethodIDs: []string{"review"}, EvidenceIDs: []UUIDv7{UUIDv7("00000000-0000-7000-8000-000000000902")}}
	if receipt.Proves([]IndependenceDimension{IndependenceActor, IndependenceExecution}, []string{"review"}) {
		t.Fatal("missing execution independence was accepted")
	}
	if receipt.Proves([]IndependenceDimension{IndependenceActor}, []string{"go-test"}) {
		t.Fatal("missing required method was accepted")
	}
}

func TestApprovedEscalationVocabularyIsRecognized(t *testing.T) {
	values := []EscalationTrigger{
		EscalationRequirementContradiction, EscalationArchitectureAmbiguity, EscalationMaterialVariantDisagreement,
		EscalationCapabilityMismatch, EscalationRiskProfileBreach, EscalationBlastRadiusExceeded,
		EscalationSecurityClassificationElevated, EscalationRootCauseUnresolved, EscalationRequiredToolUnavailable,
		EscalationNoveltyReclassified,
	}
	for _, value := range values {
		if !value.Valid() {
			t.Fatalf("approved trigger %q is invalid", value)
		}
	}
}
