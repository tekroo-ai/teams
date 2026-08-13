package kernel

import (
	"encoding/json"
	"errors"
	"time"
)

type QualificationStatus string

const (
	QualificationPass         QualificationStatus = "PASS"
	QualificationFail         QualificationStatus = "FAIL"
	QualificationNotRun       QualificationStatus = "NOT_RUN"
	QualificationInconclusive QualificationStatus = "INCONCLUSIVE"
)

func (status QualificationStatus) Valid() bool {
	return status == QualificationPass || status == QualificationFail || status == QualificationNotRun || status == QualificationInconclusive
}

type ModelProfileQualification struct {
	QualificationID           UUIDv7              `json:"qualification_id"`
	QualificationDigest       Digest              `json:"qualification_digest"`
	QualificationCorpusDigest Digest              `json:"qualification_corpus_digest"`
	ModelProfileDigest        Digest              `json:"model_profile_digest"`
	DecisionRoute             DecisionRoute       `json:"decision_route"`
	QualifiedRole             string              `json:"qualified_role"`
	QualifiedWorkKinds        []WorkKind          `json:"qualified_work_kinds"`
	Status                    QualificationStatus `json:"status"`
	ObservedAt                time.Time           `json:"observed_at"`
	ExpiresAt                 *time.Time          `json:"expires_at"`
	RevokedAt                 *time.Time          `json:"revoked_at"`
	EvidenceIDs               []UUIDv7            `json:"evidence_ids"`
}

func (qualification ModelProfileQualification) Valid() bool {
	if !qualification.QualificationID.Valid() || !qualification.QualificationDigest.Valid() || !qualification.QualificationCorpusDigest.Valid() || !qualification.ModelProfileDigest.Valid() || !qualification.DecisionRoute.ModelExecutable() || qualification.QualifiedRole == "" || len(qualification.QualifiedRole) > 256 || !qualification.Status.Valid() || qualification.ObservedAt.IsZero() || !validUniqueUUIDs(qualification.EvidenceIDs, 1, 64) || len(qualification.QualifiedWorkKinds) == 0 || len(qualification.QualifiedWorkKinds) > 7 {
		return false
	}
	seen := make(map[WorkKind]struct{}, len(qualification.QualifiedWorkKinds))
	for _, kind := range qualification.QualifiedWorkKinds {
		if !kind.Valid() {
			return false
		}
		if _, duplicate := seen[kind]; duplicate {
			return false
		}
		seen[kind] = struct{}{}
	}
	if qualification.ExpiresAt != nil && !qualification.ExpiresAt.After(qualification.ObservedAt) {
		return false
	}
	if qualification.RevokedAt != nil && qualification.RevokedAt.Before(qualification.ObservedAt) {
		return false
	}
	return true
}

func (qualification ModelProfileQualification) EligibleAt(at time.Time) bool {
	if !qualification.Valid() || qualification.Status != QualificationPass || at.IsZero() || at.Before(qualification.ObservedAt) || qualification.RevokedAt != nil && !at.Before(*qualification.RevokedAt) || qualification.ExpiresAt != nil && !at.Before(*qualification.ExpiresAt) {
		return false
	}
	return true
}

func (qualification ModelProfileQualification) Clone() ModelProfileQualification {
	copy := qualification
	copy.QualifiedWorkKinds = append([]WorkKind(nil), qualification.QualifiedWorkKinds...)
	copy.EvidenceIDs = append([]UUIDv7(nil), qualification.EvidenceIDs...)
	if qualification.ExpiresAt != nil {
		value := *qualification.ExpiresAt
		copy.ExpiresAt = &value
	}
	if qualification.RevokedAt != nil {
		value := *qualification.RevokedAt
		copy.RevokedAt = &value
	}
	return copy
}

type ConstraintOutcome string

const (
	ConstraintPass        ConstraintOutcome = "PASS"
	ConstraintFail        ConstraintOutcome = "FAIL"
	ConstraintNotReported ConstraintOutcome = "NOT_REPORTED"
)

type HardConstraintResult struct {
	ConstraintID string            `json:"constraint_id"`
	Outcome      ConstraintOutcome `json:"outcome"`
	EvidenceIDs  []UUIDv7          `json:"evidence_ids"`
}

func (result HardConstraintResult) Valid() bool {
	if result.ConstraintID == "" || len(result.ConstraintID) > 256 || !validUniqueUUIDs(result.EvidenceIDs, 1, 64) {
		return false
	}
	return result.Outcome == ConstraintPass || result.Outcome == ConstraintFail || result.Outcome == ConstraintNotReported
}

func cloneHardConstraints(values []HardConstraintResult) []HardConstraintResult {
	result := make([]HardConstraintResult, len(values))
	for index, value := range values {
		result[index] = value
		result[index].EvidenceIDs = append([]UUIDv7(nil), value.EvidenceIDs...)
	}
	return result
}

type CostObservation struct {
	Reported   bool   `json:"reported"`
	Microunits uint64 `json:"microunits"`
}

func (cost CostObservation) Valid() bool {
	return cost.Reported || cost.Microunits == 0
}

type AssignmentQualificationReceipt struct {
	QualificationID           UUIDv7              `json:"qualification_id"`
	QualificationDigest       Digest              `json:"qualification_digest"`
	QualificationCorpusDigest Digest              `json:"qualification_corpus_digest"`
	ModelProfileDigest        Digest              `json:"model_profile_digest"`
	DecisionRoute             DecisionRoute       `json:"decision_route"`
	QualifiedRole             string              `json:"qualified_role"`
	Status                    QualificationStatus `json:"status"`
	ObservedAt                time.Time           `json:"observed_at"`
}

func (receipt AssignmentQualificationReceipt) Valid() bool {
	return receipt.QualificationID.Valid() && receipt.QualificationDigest.Valid() && receipt.QualificationCorpusDigest.Valid() && receipt.ModelProfileDigest.Valid() && receipt.DecisionRoute.ModelExecutable() && receipt.QualifiedRole != "" && len(receipt.QualifiedRole) <= 256 && receipt.Status == QualificationPass && !receipt.ObservedAt.IsZero()
}

type QualifiedAssignmentAuthorization struct {
	AssignmentID            UUIDv7                         `json:"assignment_id"`
	TaskID                  UUIDv7                         `json:"task_id"`
	ExpectedTaskRevision    uint64                         `json:"expected_task_revision"`
	WorkProfile             WorkProfileBinding             `json:"work_profile"`
	RequiredDecisionRoute   DecisionRoute                  `json:"required_decision_route"`
	SelectedDecisionRoute   DecisionRoute                  `json:"selected_decision_route"`
	SelectedActorFQN        ActorFQN                       `json:"selected_actor_fqn"`
	SelectedExecutionID     UUIDv7                         `json:"selected_execution_id"`
	SelectedFencingEpoch    uint64                         `json:"selected_fencing_epoch"`
	ModelProfileDigest      Digest                         `json:"model_profile_digest"`
	RuntimeIdentityDigest   Digest                         `json:"runtime_identity_digest"`
	Qualification           AssignmentQualificationReceipt `json:"qualification"`
	SelectionPolicyRevision uint64                         `json:"selection_policy_revision"`
	SelectionPolicyDigest   Digest                         `json:"selection_policy_digest"`
	HardConstraintResults   []HardConstraintResult         `json:"hard_constraint_results"`
	SelectionReasons        []string                       `json:"selection_reasons"`
	EvidenceIDs             []UUIDv7                       `json:"evidence_ids"`
	AuthorizationEventID    UUIDv7                         `json:"authorization_event_id"`
}

func QualifiedAssignmentAuthorizationFromPayload(payload []byte, eventID UUIDv7) (QualifiedAssignmentAuthorization, error) {
	var value QualifiedAssignmentAuthorization
	if json.Unmarshal(payload, &value) != nil {
		return QualifiedAssignmentAuthorization{}, errors.New("invalid qualified assignment authorization")
	}
	value.AuthorizationEventID = eventID
	if !value.Valid() {
		return QualifiedAssignmentAuthorization{}, errors.New("invalid qualified assignment authorization")
	}
	return value, nil
}

func (authorization QualifiedAssignmentAuthorization) Valid() bool {
	if !authorization.AssignmentID.Valid() || !authorization.TaskID.Valid() || authorization.ExpectedTaskRevision == 0 || !authorization.WorkProfile.Valid() || !authorization.RequiredDecisionRoute.ModelExecutable() || !authorization.SelectedDecisionRoute.Satisfies(authorization.RequiredDecisionRoute) || !authorization.SelectedActorFQN.Valid() || !authorization.SelectedExecution().Valid() || !authorization.ModelProfileDigest.Valid() || !authorization.RuntimeIdentityDigest.Valid() || !authorization.Qualification.Valid() || authorization.Qualification.ModelProfileDigest != authorization.ModelProfileDigest || authorization.Qualification.DecisionRoute != authorization.SelectedDecisionRoute || authorization.SelectionPolicyRevision == 0 || !authorization.SelectionPolicyDigest.Valid() || !validUniqueStrings(authorization.SelectionReasons, 1, 64) || !validUniqueUUIDs(authorization.EvidenceIDs, 1, 64) || len(authorization.HardConstraintResults) == 0 || len(authorization.HardConstraintResults) > 64 || !authorization.AuthorizationEventID.Valid() {
		return false
	}
	evidence := make(map[UUIDv7]struct{}, len(authorization.EvidenceIDs))
	for _, evidenceID := range authorization.EvidenceIDs {
		evidence[evidenceID] = struct{}{}
	}
	constraints := make(map[string]struct{}, len(authorization.HardConstraintResults))
	for _, result := range authorization.HardConstraintResults {
		if !result.Valid() || result.Outcome != ConstraintPass {
			return false
		}
		if _, duplicate := constraints[result.ConstraintID]; duplicate {
			return false
		}
		constraints[result.ConstraintID] = struct{}{}
		for _, evidenceID := range result.EvidenceIDs {
			if _, included := evidence[evidenceID]; !included {
				return false
			}
		}
	}
	return true
}

func (authorization QualifiedAssignmentAuthorization) SelectedExecution() ExecutionTuple {
	return ExecutionTuple{ExecutionID: authorization.SelectedExecutionID, FencingEpoch: authorization.SelectedFencingEpoch}
}

func (authorization QualifiedAssignmentAuthorization) Clone() QualifiedAssignmentAuthorization {
	copy := authorization
	copy.HardConstraintResults = cloneHardConstraints(authorization.HardConstraintResults)
	copy.SelectionReasons = append([]string(nil), authorization.SelectionReasons...)
	copy.EvidenceIDs = append([]UUIDv7(nil), authorization.EvidenceIDs...)
	return copy
}
