package application_test

import (
	"testing"
	"time"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

func TestModelRegistryIsContentAddressedAndRevocationFailsClosed(t *testing.T) {
	registry := application.NewInMemoryModelRegistry()
	profile := registryProfile()
	profileDigest, err := registry.RegisterProfile(profile)
	if err != nil {
		t.Fatal(err)
	}
	reordered := profile
	reordered.CapabilityTags = []string{"structured-output", "tools"}
	repeatedDigest, err := registry.RegisterProfile(reordered)
	if err != nil || repeatedDigest != profileDigest {
		t.Fatalf("reordered profile digest=%s err=%v", repeatedDigest, err)
	}

	corpus := registryCorpus()
	corpusDigest, err := registry.RegisterCorpus(corpus)
	if err != nil {
		t.Fatal(err)
	}
	observed := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	qualification := kernel.ModelProfileQualification{
		QualificationID: "00000000-0000-7000-8000-000000000905", QualificationCorpusDigest: corpusDigest,
		ModelProfileDigest: profileDigest, DecisionRoute: kernel.RouteBoundedExecution, QualifiedRole: "programmer",
		QualifiedWorkKinds: []kernel.WorkKind{kernel.WorkImplementation}, Status: kernel.QualificationPass, ObservedAt: observed,
		EvidenceIDs: []kernel.UUIDv7{"00000000-0000-7000-8000-000000000906"},
	}
	qualification.QualificationDigest, err = application.QualificationDigest(qualification)
	if err != nil || registry.RecordQualification(qualification) != nil {
		t.Fatalf("record qualification digest=%s err=%v", qualification.QualificationDigest, err)
	}
	if eligible := registry.EligibleQualifications(kernel.RouteBoundedExecution, kernel.WorkImplementation, observed.Add(time.Minute)); len(eligible) != 1 || eligible[0].QualificationID != qualification.QualificationID {
		t.Fatalf("eligible qualifications = %#v", eligible)
	}

	revocation := application.QualificationRevocation{
		QualificationID: qualification.QualificationID, RevokedAt: observed.Add(2 * time.Minute),
		Authority: kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "qualification-policy"}, Reason: "retained regression failed",
		EvidenceIDs: []kernel.UUIDv7{"00000000-0000-7000-8000-000000000907"},
	}
	revocation.RevocationDigest, err = application.RevocationDigest(revocation)
	if err != nil || registry.RevokeQualification(revocation) != nil {
		t.Fatalf("revoke digest=%s err=%v", revocation.RevocationDigest, err)
	}
	if eligible := registry.EligibleQualifications(kernel.RouteBoundedExecution, kernel.WorkImplementation, observed.Add(3*time.Minute)); len(eligible) != 0 {
		t.Fatalf("revoked qualification remained eligible: %#v", eligible)
	}
}

func TestModelRegistryRejectsQualificationWithoutPreregisteredCorpus(t *testing.T) {
	registry := application.NewInMemoryModelRegistry()
	profileDigest, err := registry.RegisterProfile(registryProfile())
	if err != nil {
		t.Fatal(err)
	}
	qualification := kernel.ModelProfileQualification{
		QualificationID: "00000000-0000-7000-8000-000000000905", QualificationCorpusDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ModelProfileDigest: profileDigest, DecisionRoute: kernel.RouteBoundedExecution, QualifiedRole: "programmer", QualifiedWorkKinds: []kernel.WorkKind{kernel.WorkImplementation},
		Status: kernel.QualificationPass, ObservedAt: time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC), EvidenceIDs: []kernel.UUIDv7{"00000000-0000-7000-8000-000000000906"},
	}
	qualification.QualificationDigest, _ = application.QualificationDigest(qualification)
	if err := registry.RecordQualification(qualification); err != application.ErrInvalidQualification {
		t.Fatalf("unregistered corpus error = %v", err)
	}
}

func TestModelProfileRejectsUnclaimedOptionalDigests(t *testing.T) {
	profile := registryProfile()
	profile.WeightIdentityAvailable = false
	profile.WeightsDigest = "malformed"
	if _, err := application.NewInMemoryModelRegistry().RegisterProfile(profile); err != application.ErrInvalidModelProfile {
		t.Fatalf("unclaimed weights digest error = %v", err)
	}

	profile = registryProfile()
	profile.CostReported = false
	profile.CostScheduleDigest = "malformed"
	if _, err := application.NewInMemoryModelRegistry().RegisterProfile(profile); err != application.ErrInvalidModelProfile {
		t.Fatalf("unclaimed cost digest error = %v", err)
	}
}

func registryProfile() application.ModelProfileIdentity {
	return application.ModelProfileIdentity{
		ProviderIdentity: "fake-provider", EndpointClass: "LOCAL_TEST", ModelIdentifier: "fake-coder", ModelRevision: "1",
		WeightIdentityAvailable: true, WeightsDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Quantization: "none",
		InferenceEngine: "fake-engine", InferenceEngineVersion: "1", SamplingConfigurationDigest: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		ContextLimit: 32768, OutputLimit: 4096, ToolSurfaceDigest: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		SystemPromptDigest: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", RoleLibraryDigest: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		RuntimeIdentityDigest: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", IsolationPolicyDigest: "1111111111111111111111111111111111111111111111111111111111111111",
		DataResidencyPolicyDigest: "2222222222222222222222222222222222222222222222222222222222222222", CostReported: false,
		CapabilityTags: []string{"tools", "structured-output"},
	}
}

func registryCorpus() application.QualificationCorpusDefinition {
	return application.QualificationCorpusDefinition{
		CorpusID: "00000000-0000-7000-8000-000000000901", Revision: 1, DecisionRoute: kernel.RouteBoundedExecution, QualifiedRole: "programmer",
		WorkKinds: []kernel.WorkKind{kernel.WorkImplementation}, ScenarioIDs: []string{"bounded-tool-use", "contract-fidelity"},
		ThresholdDigest: "3333333333333333333333333333333333333333333333333333333333333333", ToolSurfaceDigest: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
	}
}
