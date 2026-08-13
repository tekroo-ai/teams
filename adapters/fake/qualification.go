package fake

import (
	"errors"
	"sort"
	"time"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

var ErrInvalidFakeQualification = errors.New("invalid fake qualification input")

type QualificationScenarioResult struct {
	ScenarioID  string
	Status      kernel.QualificationStatus
	EvidenceIDs []kernel.UUIDv7
}

type FakeQualificationRequest struct {
	QualificationID    kernel.UUIDv7
	ModelProfileDigest kernel.Digest
	Corpus             application.QualificationCorpusDefinition
	ObservedAt         time.Time
	ExpiresAt          *time.Time
	Results            []QualificationScenarioResult
}

func RunQualification(request FakeQualificationRequest) (kernel.ModelProfileQualification, error) {
	corpusDigest, corpusErr := request.Corpus.Digest()
	if !request.QualificationID.Valid() || !request.ModelProfileDigest.Valid() || corpusErr != nil || request.ObservedAt.IsZero() || len(request.Results) != len(request.Corpus.ScenarioIDs) {
		return kernel.ModelProfileQualification{}, ErrInvalidFakeQualification
	}
	expected := append([]string(nil), request.Corpus.ScenarioIDs...)
	sort.Strings(expected)
	results := append([]QualificationScenarioResult(nil), request.Results...)
	sort.Slice(results, func(i, j int) bool { return results[i].ScenarioID < results[j].ScenarioID })
	status := kernel.QualificationPass
	evidence := make([]kernel.UUIDv7, 0)
	for index, result := range results {
		if result.ScenarioID == "" || result.ScenarioID != expected[index] || !result.Status.Valid() || len(result.EvidenceIDs) == 0 {
			return kernel.ModelProfileQualification{}, ErrInvalidFakeQualification
		}
		evidence = append(evidence, result.EvidenceIDs...)
		status = combinedQualificationStatus(status, result.Status)
	}
	sort.Slice(evidence, func(i, j int) bool { return evidence[i] < evidence[j] })
	for index, id := range evidence {
		if !id.Valid() || index > 0 && evidence[index-1] == id {
			return kernel.ModelProfileQualification{}, ErrInvalidFakeQualification
		}
	}
	workKinds := append([]kernel.WorkKind(nil), request.Corpus.WorkKinds...)
	sort.Slice(workKinds, func(i, j int) bool { return workKinds[i] < workKinds[j] })
	qualification := kernel.ModelProfileQualification{
		QualificationID: request.QualificationID, QualificationCorpusDigest: corpusDigest,
		ModelProfileDigest: request.ModelProfileDigest, DecisionRoute: request.Corpus.DecisionRoute,
		QualifiedRole: request.Corpus.QualifiedRole, QualifiedWorkKinds: workKinds, Status: status,
		ObservedAt: request.ObservedAt, ExpiresAt: request.ExpiresAt, EvidenceIDs: evidence,
	}
	digest, err := application.QualificationDigest(qualification)
	if err != nil {
		return kernel.ModelProfileQualification{}, err
	}
	qualification.QualificationDigest = digest
	if !qualification.Valid() {
		return kernel.ModelProfileQualification{}, ErrInvalidFakeQualification
	}
	return qualification, nil
}

func combinedQualificationStatus(current, next kernel.QualificationStatus) kernel.QualificationStatus {
	if current == kernel.QualificationFail || next == kernel.QualificationFail {
		return kernel.QualificationFail
	}
	if current == kernel.QualificationInconclusive || next == kernel.QualificationInconclusive {
		return kernel.QualificationInconclusive
	}
	if current == kernel.QualificationNotRun || next == kernel.QualificationNotRun {
		return kernel.QualificationNotRun
	}
	return kernel.QualificationPass
}
