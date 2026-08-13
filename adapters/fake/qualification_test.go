package fake_test

import (
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/fake"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

func TestFakeQualificationPreservesNotRunAndRequiresCompleteCorpus(t *testing.T) {
	corpus := application.QualificationCorpusDefinition{
		CorpusID: "00000000-0000-7000-8000-000000000901", Revision: 1, DecisionRoute: kernel.RouteBoundedExecution, QualifiedRole: "programmer",
		WorkKinds: []kernel.WorkKind{kernel.WorkImplementation}, ScenarioIDs: []string{"bounded-tool-use", "contract-fidelity"},
		ThresholdDigest: "3333333333333333333333333333333333333333333333333333333333333333", ToolSurfaceDigest: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
	}
	request := fake.FakeQualificationRequest{
		QualificationID: "00000000-0000-7000-8000-000000000905", ModelProfileDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Corpus: corpus, ObservedAt: time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC),
		Results: []fake.QualificationScenarioResult{
			{ScenarioID: "contract-fidelity", Status: kernel.QualificationPass, EvidenceIDs: []kernel.UUIDv7{"00000000-0000-7000-8000-000000000906"}},
			{ScenarioID: "bounded-tool-use", Status: kernel.QualificationNotRun, EvidenceIDs: []kernel.UUIDv7{"00000000-0000-7000-8000-000000000907"}},
		},
	}
	qualification, err := fake.RunQualification(request)
	if err != nil || qualification.Status != kernel.QualificationNotRun || qualification.EligibleAt(request.ObservedAt.Add(time.Minute)) {
		t.Fatalf("qualification=%#v err=%v", qualification, err)
	}
	request.Results = request.Results[:1]
	if _, err := fake.RunQualification(request); err != fake.ErrInvalidFakeQualification {
		t.Fatalf("incomplete corpus error = %v", err)
	}
}
