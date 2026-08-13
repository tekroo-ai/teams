package kernel

import (
	"encoding/json"
	"errors"
	"sort"
	"time"
)

type VariantState string

const (
	VariantOpen     VariantState = "OPEN"
	VariantCompared VariantState = "COMPARED"
	VariantTerminal VariantState = "TERMINAL"
)

type VariantClassification string

const (
	VariantMaterialAgreement      VariantClassification = "MATERIAL_AGREEMENT"
	VariantNonMaterialVariation   VariantClassification = "NON_MATERIAL_VARIATION"
	VariantMaterialDisagreement   VariantClassification = "MATERIAL_DISAGREEMENT"
	VariantInsufficientCandidates VariantClassification = "INSUFFICIENT_VALID_CANDIDATES"
	VariantInconclusive           VariantClassification = "INCONCLUSIVE"
)

func (classification VariantClassification) Valid() bool {
	switch classification {
	case VariantMaterialAgreement, VariantNonMaterialVariation, VariantMaterialDisagreement, VariantInsufficientCandidates, VariantInconclusive:
		return true
	default:
		return false
	}
}

type VariantSelectionOutcome string

const (
	VariantSelectCandidate VariantSelectionOutcome = "SELECT_CANDIDATE"
	VariantRejectAll       VariantSelectionOutcome = "REJECT_ALL"
	VariantRedesign        VariantSelectionOutcome = "REDESIGN"
	VariantSplit           VariantSelectionOutcome = "SPLIT"
	VariantHumanRequired   VariantSelectionOutcome = "HUMAN_REQUIRED"
	VariantEscalate        VariantSelectionOutcome = "ESCALATE"
)

func (outcome VariantSelectionOutcome) Valid() bool {
	switch outcome {
	case VariantSelectCandidate, VariantRejectAll, VariantRedesign, VariantSplit, VariantHumanRequired, VariantEscalate:
		return true
	default:
		return false
	}
}

type VariantGroupKey struct {
	TaskID            UUIDv7 `json:"task_id"`
	LifecycleEpoch    uint64 `json:"lifecycle_epoch"`
	ScopeRevision     uint64 `json:"scope_revision"`
	WorkProfileDigest Digest `json:"work_profile_digest"`
}

func (key VariantGroupKey) Valid() bool {
	return key.TaskID.Valid() && key.LifecycleEpoch > 0 && key.ScopeRevision > 0 && key.WorkProfileDigest.Valid()
}

type VariantCandidate struct {
	CandidateID                 UUIDv7         `json:"candidate_id"`
	ActorFQN                    ActorFQN       `json:"actor_fqn"`
	Execution                   ExecutionTuple `json:"execution"`
	ModelProfileDigest          Digest         `json:"model_profile_digest"`
	RuntimeIdentityDigest       Digest         `json:"runtime_identity_digest"`
	ContextDigest               Digest         `json:"context_digest"`
	WorkspaceDigest             Digest         `json:"workspace_digest"`
	ArtifactDigest              Digest         `json:"artifact_digest"`
	ChangedFileInventoryDigest  Digest         `json:"changed_file_inventory_digest"`
	DeterministicGateReceiptIDs []UUIDv7       `json:"deterministic_gate_receipt_ids"`
	Assumptions                 []string       `json:"assumptions"`
	UnresolvedExceptions        []string       `json:"unresolved_exceptions"`
	EvidenceIDs                 []UUIDv7       `json:"evidence_ids"`
	SubmissionEventID           UUIDv7         `json:"submission_event_id"`
}

func (candidate VariantCandidate) Valid() bool {
	return candidate.CandidateID.Valid() && candidate.ActorFQN.Valid() && candidate.Execution.Valid() && candidate.ModelProfileDigest.Valid() && candidate.RuntimeIdentityDigest.Valid() && candidate.ContextDigest.Valid() && candidate.WorkspaceDigest.Valid() && candidate.ArtifactDigest.Valid() && candidate.ChangedFileInventoryDigest.Valid() && validUniqueUUIDs(candidate.DeterministicGateReceiptIDs, 1, 64) && validUniqueStrings(candidate.Assumptions, 0, 64) && validUniqueStrings(candidate.UnresolvedExceptions, 0, 64) && validUniqueUUIDs(candidate.EvidenceIDs, 1, 64) && candidate.SubmissionEventID.Valid()
}

type VariantComparison struct {
	Comparator              PrincipalRef          `json:"comparator"`
	ComparatorExecutionID   UUIDv7                `json:"comparator_execution_id"`
	CandidateIDs            []UUIDv7              `json:"candidate_ids"`
	Classification          VariantClassification `json:"classification"`
	ComparisonMethodID      string                `json:"comparison_method_id"`
	ComparisonReceiptDigest Digest                `json:"comparison_receipt_digest"`
	Reasons                 []string              `json:"reasons"`
	EvidenceIDs             []UUIDv7              `json:"evidence_ids"`
	EventID                 UUIDv7                `json:"event_id"`
}

func (comparison VariantComparison) Valid() bool {
	return comparison.Comparator.Valid() && comparison.ComparatorExecutionID.Valid() && validUniqueUUIDs(comparison.CandidateIDs, 2, 32) && comparison.Classification.Valid() && comparison.ComparisonMethodID != "" && len(comparison.ComparisonMethodID) <= 256 && comparison.ComparisonReceiptDigest.Valid() && validUniqueStrings(comparison.Reasons, 1, 64) && validUniqueUUIDs(comparison.EvidenceIDs, 1, 64) && comparison.EventID.Valid()
}

type VariantSelection struct {
	ComparisonEventID   UUIDv7                  `json:"comparison_event_id"`
	Adjudicator         PrincipalRef            `json:"adjudicator"`
	Outcome             VariantSelectionOutcome `json:"outcome"`
	SelectedCandidateID *UUIDv7                 `json:"selected_candidate_id,omitempty"`
	Reasons             []string                `json:"reasons"`
	EvidenceIDs         []UUIDv7                `json:"evidence_ids"`
	EventID             UUIDv7                  `json:"event_id"`
}

func (selection VariantSelection) Valid() bool {
	if !selection.ComparisonEventID.Valid() || !selection.Adjudicator.Valid() || !selection.Outcome.Valid() || !validUniqueStrings(selection.Reasons, 1, 64) || !validUniqueUUIDs(selection.EvidenceIDs, 1, 64) || !selection.EventID.Valid() {
		return false
	}
	return selection.Outcome == VariantSelectCandidate && selection.SelectedCandidateID != nil && selection.SelectedCandidateID.Valid() || selection.Outcome != VariantSelectCandidate && selection.SelectedCandidateID == nil
}

type VariantGroupSnapshot struct {
	VariantGroupID                 UUIDv7                      `json:"variant_group_id"`
	Revision                       uint64                      `json:"revision"`
	State                          VariantState                `json:"state"`
	TaskID                         UUIDv7                      `json:"task_id"`
	WorkProfile                    WorkProfileBinding          `json:"work_profile"`
	BaseArtifactDigest             Digest                      `json:"base_artifact_digest"`
	InputEvidenceSetDigest         Digest                      `json:"input_evidence_set_digest"`
	ToolchainDigest                Digest                      `json:"toolchain_digest"`
	DependencyLockDigest           Digest                      `json:"dependency_lock_digest"`
	AcceptanceManifestDigest       Digest                      `json:"acceptance_manifest_digest"`
	CandidateCount                 uint64                      `json:"candidate_count"`
	ValidCandidateQuorum           uint64                      `json:"valid_candidate_quorum"`
	RequiredIndependenceDimensions []IndependenceDimension     `json:"required_independence_dimensions"`
	ComparisonMethodID             string                      `json:"comparison_method_id"`
	ComparisonPolicyDigest         Digest                      `json:"comparison_policy_digest"`
	MaterialityPolicyDigest        Digest                      `json:"materiality_policy_digest"`
	Comparator                     PrincipalRef                `json:"comparator"`
	Adjudicator                    PrincipalRef                `json:"adjudicator"`
	SubmissionDeadlineAt           time.Time                   `json:"submission_deadline_at"`
	DecisionDeadlineAt             time.Time                   `json:"decision_deadline_at"`
	ReplacementBudget              uint64                      `json:"replacement_budget"`
	EvidenceIDs                    []UUIDv7                    `json:"evidence_ids"`
	Candidates                     map[UUIDv7]VariantCandidate `json:"candidates"`
	Comparison                     *VariantComparison          `json:"comparison,omitempty"`
	Selection                      *VariantSelection           `json:"selection,omitempty"`
}

func (snapshot VariantGroupSnapshot) Key() VariantGroupKey {
	return VariantGroupKey{TaskID: snapshot.TaskID, LifecycleEpoch: snapshot.WorkProfile.LifecycleEpoch, ScopeRevision: snapshot.WorkProfile.ScopeRevision, WorkProfileDigest: snapshot.WorkProfile.ProfileDigest}
}

func (snapshot VariantGroupSnapshot) Valid() bool {
	if !snapshot.VariantGroupID.Valid() || snapshot.Revision == 0 || !snapshot.Key().Valid() || !snapshot.BaseArtifactDigest.Valid() || !snapshot.InputEvidenceSetDigest.Valid() || !snapshot.ToolchainDigest.Valid() || !snapshot.DependencyLockDigest.Valid() || !snapshot.AcceptanceManifestDigest.Valid() || snapshot.CandidateCount < 2 || snapshot.CandidateCount > 32 || snapshot.ValidCandidateQuorum < 2 || snapshot.ValidCandidateQuorum > snapshot.CandidateCount || !validUniqueDimensions(snapshot.RequiredIndependenceDimensions) || !requiredVariantIsolation(snapshot.RequiredIndependenceDimensions) || snapshot.ComparisonMethodID == "" || len(snapshot.ComparisonMethodID) > 256 || !snapshot.ComparisonPolicyDigest.Valid() || !snapshot.MaterialityPolicyDigest.Valid() || !snapshot.Comparator.Valid() || !snapshot.Adjudicator.Valid() || snapshot.SubmissionDeadlineAt.IsZero() || !snapshot.DecisionDeadlineAt.After(snapshot.SubmissionDeadlineAt) || snapshot.ReplacementBudget > 32 || !validUniqueUUIDs(snapshot.EvidenceIDs, 1, 64) || len(snapshot.Candidates) > int(snapshot.CandidateCount+snapshot.ReplacementBudget) {
		return false
	}
	return snapshot.State == VariantOpen || snapshot.State == VariantCompared && snapshot.Comparison != nil || snapshot.State == VariantTerminal && snapshot.Comparison != nil && snapshot.Selection != nil
}

func (snapshot VariantGroupSnapshot) Clone() VariantGroupSnapshot {
	copy := snapshot
	copy.RequiredIndependenceDimensions = append([]IndependenceDimension(nil), snapshot.RequiredIndependenceDimensions...)
	copy.EvidenceIDs = append([]UUIDv7(nil), snapshot.EvidenceIDs...)
	copy.Candidates = make(map[UUIDv7]VariantCandidate, len(snapshot.Candidates))
	for id, candidate := range snapshot.Candidates {
		candidate.DeterministicGateReceiptIDs = append([]UUIDv7(nil), candidate.DeterministicGateReceiptIDs...)
		candidate.Assumptions = append([]string(nil), candidate.Assumptions...)
		candidate.UnresolvedExceptions = append([]string(nil), candidate.UnresolvedExceptions...)
		candidate.EvidenceIDs = append([]UUIDv7(nil), candidate.EvidenceIDs...)
		copy.Candidates[id] = candidate
	}
	if snapshot.Comparison != nil {
		value := *snapshot.Comparison
		value.CandidateIDs = append([]UUIDv7(nil), value.CandidateIDs...)
		value.Reasons = append([]string(nil), value.Reasons...)
		value.EvidenceIDs = append([]UUIDv7(nil), value.EvidenceIDs...)
		copy.Comparison = &value
	}
	if snapshot.Selection != nil {
		value := *snapshot.Selection
		value.Reasons = append([]string(nil), value.Reasons...)
		value.EvidenceIDs = append([]UUIDv7(nil), value.EvidenceIDs...)
		if snapshot.Selection.SelectedCandidateID != nil {
			candidateID := *snapshot.Selection.SelectedCandidateID
			value.SelectedCandidateID = &candidateID
		}
		copy.Selection = &value
	}
	return copy
}

func VariantGroupFromOpenPayload(payload json.RawMessage, eventID UUIDv7) (VariantGroupSnapshot, error) {
	var snapshot VariantGroupSnapshot
	if json.Unmarshal(payload, &snapshot) != nil {
		return VariantGroupSnapshot{}, errors.New("invalid variant group")
	}
	snapshot.Revision = 1
	snapshot.State = VariantOpen
	snapshot.Candidates = make(map[UUIDv7]VariantCandidate)
	snapshot.Comparison = nil
	snapshot.Selection = nil
	if !eventID.Valid() || !snapshot.Valid() {
		return VariantGroupSnapshot{}, errors.New("invalid variant group")
	}
	return snapshot, nil
}

func ApplyVariantEvent(snapshot VariantGroupSnapshot, event DomainEvent) (VariantGroupSnapshot, bool) {
	if !snapshot.Valid() || event.Aggregate.Kind != AggregateVariantGroup || event.Aggregate.ID != snapshot.VariantGroupID || event.AggregateRevision != snapshot.Revision+1 || !event.EventID.Valid() {
		return snapshot, false
	}
	next := snapshot.Clone()
	switch event.EventType {
	case "tekroo.event.variant-group.candidate-submitted":
		candidate, ok := variantCandidateFromPayload(event.Payload, event.EventID)
		if !ok || snapshot.State != VariantOpen || event.CommittedAt.After(snapshot.SubmissionDeadlineAt) || len(snapshot.Candidates) >= int(snapshot.CandidateCount+snapshot.ReplacementBudget) || !variantCandidateIndependent(snapshot.Candidates, candidate) {
			return snapshot, false
		}
		next.Candidates[candidate.CandidateID] = candidate
	case "tekroo.event.variant-group.comparison-recorded":
		comparison, ok := variantComparisonFromPayload(event.Payload, event.EventID)
		if !ok || snapshot.State != VariantOpen || uint64(len(snapshot.Candidates)) < snapshot.ValidCandidateQuorum || event.CommittedAt.After(snapshot.DecisionDeadlineAt) || comparison.Comparator != snapshot.Comparator || comparison.ComparisonMethodID != snapshot.ComparisonMethodID || !exactCandidateSet(comparison.CandidateIDs, snapshot.Candidates) {
			return snapshot, false
		}
		next.Comparison = &comparison
		next.State = VariantCompared
	case "tekroo.event.variant-group.selected":
		selection, ok := variantSelectionFromPayload(event.Payload, event.EventID)
		if !ok || snapshot.State != VariantCompared || snapshot.Comparison == nil || selection.ComparisonEventID != snapshot.Comparison.EventID || selection.Adjudicator != snapshot.Adjudicator || event.CommittedAt.After(snapshot.DecisionDeadlineAt) {
			return snapshot, false
		}
		if snapshot.Comparison.Classification == VariantMaterialDisagreement && selection.Outcome != VariantEscalate {
			return snapshot, false
		}
		if selection.Outcome == VariantSelectCandidate {
			candidate, found := snapshot.Candidates[*selection.SelectedCandidateID]
			if !found || snapshot.Comparison.Classification == VariantMaterialDisagreement || snapshot.Comparator.Kind == PrincipalActor && snapshot.Comparator.ID == string(candidate.ActorFQN) {
				return snapshot, false
			}
		}
		next.Selection = &selection
		next.State = VariantTerminal
	default:
		return snapshot, false
	}
	next.Revision++
	return next, next.Valid()
}

func variantCandidateFromPayload(payload json.RawMessage, eventID UUIDv7) (VariantCandidate, bool) {
	var value struct {
		CandidateID                 UUIDv7   `json:"candidate_id"`
		ActorFQN                    ActorFQN `json:"actor_fqn"`
		ExecutionID                 UUIDv7   `json:"execution_id"`
		FencingEpoch                uint64   `json:"fencing_epoch"`
		ModelProfileDigest          Digest   `json:"model_profile_digest"`
		RuntimeIdentityDigest       Digest   `json:"runtime_identity_digest"`
		ContextDigest               Digest   `json:"context_digest"`
		WorkspaceDigest             Digest   `json:"workspace_digest"`
		ArtifactDigest              Digest   `json:"artifact_digest"`
		ChangedFileInventoryDigest  Digest   `json:"changed_file_inventory_digest"`
		DeterministicGateReceiptIDs []UUIDv7 `json:"deterministic_gate_receipt_ids"`
		Assumptions                 []string `json:"assumptions"`
		UnresolvedExceptions        []string `json:"unresolved_exceptions"`
		EvidenceIDs                 []UUIDv7 `json:"evidence_ids"`
	}
	if json.Unmarshal(payload, &value) != nil {
		return VariantCandidate{}, false
	}
	candidate := VariantCandidate{CandidateID: value.CandidateID, ActorFQN: value.ActorFQN, Execution: ExecutionTuple{ExecutionID: value.ExecutionID, FencingEpoch: value.FencingEpoch}, ModelProfileDigest: value.ModelProfileDigest, RuntimeIdentityDigest: value.RuntimeIdentityDigest, ContextDigest: value.ContextDigest, WorkspaceDigest: value.WorkspaceDigest, ArtifactDigest: value.ArtifactDigest, ChangedFileInventoryDigest: value.ChangedFileInventoryDigest, DeterministicGateReceiptIDs: value.DeterministicGateReceiptIDs, Assumptions: value.Assumptions, UnresolvedExceptions: value.UnresolvedExceptions, EvidenceIDs: value.EvidenceIDs, SubmissionEventID: eventID}
	return candidate, candidate.Valid()
}

func variantComparisonFromPayload(payload json.RawMessage, eventID UUIDv7) (VariantComparison, bool) {
	var comparison VariantComparison
	if json.Unmarshal(payload, &comparison) != nil {
		return VariantComparison{}, false
	}
	comparison.EventID = eventID
	sort.Slice(comparison.CandidateIDs, func(i, j int) bool { return comparison.CandidateIDs[i] < comparison.CandidateIDs[j] })
	return comparison, comparison.Valid()
}

func variantSelectionFromPayload(payload json.RawMessage, eventID UUIDv7) (VariantSelection, bool) {
	var selection VariantSelection
	if json.Unmarshal(payload, &selection) != nil {
		return VariantSelection{}, false
	}
	selection.EventID = eventID
	return selection, selection.Valid()
}

func requiredVariantIsolation(values []IndependenceDimension) bool {
	required := map[IndependenceDimension]bool{IndependenceActor: false, IndependenceExecution: false, IndependenceContext: false, IndependenceWorkspace: false}
	for _, value := range values {
		if _, exists := required[value]; exists {
			required[value] = true
		}
	}
	for _, present := range required {
		if !present {
			return false
		}
	}
	return true
}

func variantCandidateIndependent(existing map[UUIDv7]VariantCandidate, candidate VariantCandidate) bool {
	if _, duplicate := existing[candidate.CandidateID]; duplicate {
		return false
	}
	for _, prior := range existing {
		if prior.ActorFQN == candidate.ActorFQN || prior.Execution == candidate.Execution || prior.ContextDigest == candidate.ContextDigest || prior.WorkspaceDigest == candidate.WorkspaceDigest || prior.ArtifactDigest == candidate.ArtifactDigest {
			return false
		}
	}
	return true
}

func exactCandidateSet(ids []UUIDv7, candidates map[UUIDv7]VariantCandidate) bool {
	if len(ids) != len(candidates) {
		return false
	}
	for _, id := range ids {
		if _, found := candidates[id]; !found {
			return false
		}
	}
	return true
}
