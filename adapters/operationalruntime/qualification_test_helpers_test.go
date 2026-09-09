package operationalruntime

import (
	"testing"
	"time"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

func testQualificationBundle(t *testing.T, model kernel.Digest, role kernel.RoleFQRN, route kernel.DecisionRoute, toolSurface kernel.Digest, workKinds []kernel.WorkKind, observedAt time.Time) (*application.QualificationCorpusDefinition, *kernel.ModelProfileQualification) {
	t.Helper()
	corpus := application.QualificationCorpusDefinition{
		CorpusID: deterministicOperationalUUID("test-qualification-corpus", string(model), string(role), string(route)),
		Revision: 1, DecisionRoute: route, QualifiedRole: string(role),
		WorkKinds: append([]kernel.WorkKind(nil), workKinds...), ScenarioIDs: []string{"role-specific-tool-use"},
		ThresholdDigest: repeatedDigest('c'), ToolSurfaceDigest: toolSurface,
	}
	corpusDigest, err := corpus.Digest()
	if err != nil {
		t.Fatal(err)
	}
	qualification := kernel.ModelProfileQualification{
		QualificationID:           deterministicOperationalUUID("test-model-qualification", string(model), string(role), string(route)),
		QualificationCorpusDigest: corpusDigest, ModelProfileDigest: model, DecisionRoute: route,
		QualifiedRole: string(role), QualifiedWorkKinds: append([]kernel.WorkKind(nil), workKinds...),
		Status: kernel.QualificationPass, ObservedAt: observedAt,
		EvidenceIDs: []kernel.UUIDv7{deterministicOperationalUUID("test-model-qualification-evidence", string(model), string(role), string(route))},
	}
	qualification.QualificationDigest, err = application.QualificationDigest(qualification)
	if err != nil {
		t.Fatal(err)
	}
	return &corpus, &qualification
}

func allTestWorkKinds() []kernel.WorkKind {
	return []kernel.WorkKind{
		kernel.WorkInvestigation,
		kernel.WorkDesign,
		kernel.WorkImplementation,
		kernel.WorkDebugging,
		kernel.WorkValidation,
		kernel.WorkSecurityReview,
		kernel.WorkRelease,
	}
}
