package kernel

import (
	"encoding/json"
	"errors"
)

// ReleaseState is the contract-level state of a deterministic release plan.
// Provider execution remains outside the kernel; this model only adjudicates
// whether a proposed durable transition is legal.
type ReleaseState string

const (
	ReleaseAbsent             ReleaseState = "ABSENT"
	ReleasePlanned            ReleaseState = "PLANNED"
	ReleaseQualified          ReleaseState = "QUALIFIED"
	ReleaseExecuting          ReleaseState = "EXECUTING"
	ReleaseReconciling        ReleaseState = "RECONCILING"
	ReleaseReadyForAcceptance ReleaseState = "READY_FOR_ACCEPTANCE"
	ReleaseFailed             ReleaseState = "FAILED"
	ReleaseBlocked            ReleaseState = "BLOCKED"
	ReleaseNotRequired        ReleaseState = "NO_RELEASE_REQUIRED"
)

type ReleaseMode string

const (
	ReleaseModeCode        ReleaseMode = "CODE"
	ReleaseModeNotRequired ReleaseMode = "NO_RELEASE_REQUIRED"
)

type ReleaseAction string

const (
	ReleaseCreate           ReleaseAction = "CREATE"
	ReleaseQualify          ReleaseAction = "QUALIFY"
	ReleaseRequestExecution ReleaseAction = "REQUEST_EXECUTION"
	ReleaseRecordResult     ReleaseAction = "RECORD_RESULT"
	ReleaseReconcile        ReleaseAction = "RECONCILE"
	ReleaseFinalize         ReleaseAction = "FINALIZE"
)

type ReleaseOutcome string

const (
	ReleaseOutcomeMerged        ReleaseOutcome = "MERGED"
	ReleaseOutcomeAlreadyMerged ReleaseOutcome = "ALREADY_MERGED"
	ReleaseOutcomeFailed        ReleaseOutcome = "FAILED"
	ReleaseOutcomeUnknown       ReleaseOutcome = "UNKNOWN"
)

type ReleaseSnapshot struct {
	State                 ReleaseState
	ReleaseMode           ReleaseMode
	Author                PrincipalRef
	AuthorApprovalEventID UUIDv7
	PlanDigest            Digest
	BaseCommit            string
	HeadCommits           []string
	MergeOrder            []UUIDv7
	NextMergeIndex        uint64
	ExecutionRoundLimit   uint64
	NextRound             uint64
	ActiveRound           uint64
	ActiveMergeID         UUIDv7
	ActiveAttemptID       UUIDv7
	UnresolvedAttemptID   UUIDv7
	LatestResultEventID   UUIDv7
	QualifiedTreeDigest   string
	ProviderTreeDigest    string
}

type ReleaseTransition struct {
	Action                  ReleaseAction
	Author                  PrincipalRef
	AuthorApprovalEventID   UUIDv7
	PlanDigest              Digest
	BaseCommit              string
	HeadCommits             []string
	MergeID                 UUIDv7
	AttemptID               UUIDv7
	Round                   uint64
	ResultEventID           UUIDv7
	SupersedesResultEventID UUIDv7
	Outcome                 ReleaseOutcome
	QualifiedTreeDigest     string
	ProviderTreeDigest      string
	TerminalStatus          ReleaseState
}

type ReleaseEvaluation struct {
	Accepted bool         `json:"accepted"`
	Reason   string       `json:"reason"`
	State    ReleaseState `json:"state"`
}

// EvaluateRelease is the provider-neutral executable model carried by the
// 0.5.0 conformance corpus. It never performs Git or provider I/O.
func EvaluateRelease(current ReleaseSnapshot, transition ReleaseTransition) ReleaseEvaluation {
	reject := func(reason string) ReleaseEvaluation {
		return ReleaseEvaluation{Accepted: false, Reason: reason, State: current.State}
	}
	accept := func(state ReleaseState) ReleaseEvaluation {
		return ReleaseEvaluation{Accepted: true, Reason: "ACCEPTED", State: state}
	}

	switch current.State {
	case ReleaseReadyForAcceptance, ReleaseFailed, ReleaseBlocked, ReleaseNotRequired:
		return reject("RELEASE_TERMINAL")
	}
	if transition.PlanDigest != "" && transition.PlanDigest != current.PlanDigest {
		return reject("PLAN_DIGEST_MISMATCH")
	}

	switch transition.Action {
	case ReleaseCreate:
		if current.State != ReleaseAbsent {
			return reject("INVALID_RELEASE_STATE")
		}
		if transition.Author != current.Author || transition.AuthorApprovalEventID != current.AuthorApprovalEventID {
			return reject("AUTHOR_APPROVAL_MISMATCH")
		}
		return accept(ReleasePlanned)
	case ReleaseQualify:
		if current.ReleaseMode != ReleaseModeCode || current.State != ReleasePlanned {
			return reject("INVALID_RELEASE_STATE")
		}
		if transition.BaseCommit != current.BaseCommit || !equalStrings(transition.HeadCommits, current.HeadCommits) {
			return reject("QUALIFICATION_INPUT_MISMATCH")
		}
		return accept(ReleaseQualified)
	case ReleaseRequestExecution:
		if current.State == ReleaseReconciling {
			return reject("RECONCILIATION_REQUIRED")
		}
		if current.State == ReleaseExecuting {
			return reject("EXECUTION_IN_PROGRESS")
		}
		if current.State != ReleaseQualified {
			return reject("QUALIFICATION_REQUIRED")
		}
		if current.NextMergeIndex >= uint64(len(current.MergeOrder)) || current.MergeOrder[current.NextMergeIndex] != transition.MergeID {
			return reject("MERGE_ORDER_CONFLICT")
		}
		if current.NextRound > current.ExecutionRoundLimit || transition.Round > current.ExecutionRoundLimit {
			return reject("RETRY_BUDGET_EXHAUSTED")
		}
		if transition.Round != current.NextRound {
			return reject("ROUND_CONFLICT")
		}
		return accept(ReleaseExecuting)
	case ReleaseRecordResult:
		if current.State != ReleaseExecuting || current.ActiveAttemptID != transition.AttemptID {
			return reject("ATTEMPT_MISMATCH")
		}
		return releaseOutcomeEvaluation(current, transition.Outcome)
	case ReleaseReconcile:
		if current.State != ReleaseReconciling || current.UnresolvedAttemptID != transition.AttemptID {
			return reject("ATTEMPT_MISMATCH")
		}
		if current.LatestResultEventID != transition.SupersedesResultEventID {
			return reject("RESULT_SUPERSESSION_MISMATCH")
		}
		return releaseOutcomeEvaluation(current, transition.Outcome)
	case ReleaseFinalize:
		if current.ReleaseMode == ReleaseModeNotRequired {
			if current.State == ReleasePlanned && transition.TerminalStatus == ReleaseNotRequired {
				return accept(ReleaseNotRequired)
			}
			return reject("INVALID_RELEASE_STATE")
		}
		if current.State != ReleaseQualified || current.NextMergeIndex != uint64(len(current.MergeOrder)) {
			return reject("RELEASE_INCOMPLETE")
		}
		if transition.ProviderTreeDigest != current.QualifiedTreeDigest {
			return reject("TREE_MISMATCH")
		}
		return accept(transition.TerminalStatus)
	default:
		return reject("INVALID_ACTION")
	}
}

func releaseOutcomeEvaluation(current ReleaseSnapshot, outcome ReleaseOutcome) ReleaseEvaluation {
	switch outcome {
	case ReleaseOutcomeUnknown:
		return ReleaseEvaluation{Accepted: true, Reason: "ACCEPTED", State: ReleaseReconciling}
	case ReleaseOutcomeMerged, ReleaseOutcomeAlreadyMerged:
		return ReleaseEvaluation{Accepted: true, Reason: "ACCEPTED", State: ReleaseQualified}
	case ReleaseOutcomeFailed:
		state := ReleaseQualified
		if current.ActiveRound >= current.ExecutionRoundLimit {
			state = ReleaseFailed
		}
		return ReleaseEvaluation{Accepted: true, Reason: "ACCEPTED", State: state}
	default:
		return ReleaseEvaluation{Accepted: false, Reason: "INVALID_OUTCOME", State: current.State}
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

type ReleasePlanKey struct {
	Story          AggregateRef `json:"story"`
	LifecycleEpoch uint64       `json:"lifecycle_epoch"`
}

func (key ReleasePlanKey) Valid() bool {
	return key.Story.Kind == AggregateStory && key.Story.Valid() && key.LifecycleEpoch > 0
}

type ReleaseMergePlan struct {
	MergeID    UUIDv7 `json:"merge_id"`
	ChangeRef  string `json:"change_ref"`
	HeadCommit string `json:"head_commit"`
	Role       string `json:"role"`
}

type ReleaseQualification struct {
	EventID             UUIDv7   `json:"event_id"`
	QualificationID     UUIDv7   `json:"qualification_id"`
	QualifiedBaseCommit string   `json:"qualified_base_commit"`
	OrderedHeadCommits  []string `json:"ordered_head_commits"`
	QualifiedTreeDigest string   `json:"qualified_tree_digest"`
	GateDefinition      Digest   `json:"gate_definition_digest"`
	Toolchain           Digest   `json:"toolchain_digest"`
	DependencyLock      Digest   `json:"dependency_lock_digest"`
	ArtifactDigests     []Digest `json:"artifact_digests"`
}

type ReleaseAttempt struct {
	MergeID                UUIDv7 `json:"merge_id"`
	AttemptID              UUIDv7 `json:"attempt_id"`
	Round                  uint64 `json:"round"`
	ProviderIdempotencyKey string `json:"provider_idempotency_key"`
	RequestEventID         UUIDv7 `json:"request_event_id"`
}

type ReleaseResult struct {
	MergeID               UUIDv7         `json:"merge_id"`
	AttemptID             UUIDv7         `json:"attempt_id"`
	Outcome               ReleaseOutcome `json:"outcome"`
	ResultEventID         UUIDv7         `json:"result_event_id"`
	ObservedTree          string         `json:"observed_tree_digest,omitempty"`
	Reconciled            bool           `json:"reconciled"`
	ReconciliationEventID UUIDv7         `json:"reconciliation_event_id,omitempty"`
}

type ReleasePlanSnapshot struct {
	ReleasePlanID          UUIDv7                   `json:"release_plan_id"`
	OpeningEventID         UUIDv7                   `json:"opening_event_id"`
	Revision               uint64                   `json:"revision"`
	State                  ReleaseState             `json:"state"`
	Mode                   ReleaseMode              `json:"release_mode"`
	Story                  AggregateRef             `json:"story"`
	StoryLifecycleEpoch    uint64                   `json:"story_lifecycle_epoch"`
	ExpectedStoryRevision  uint64                   `json:"expected_story_revision"`
	Author                 PrincipalRef             `json:"author"`
	AuthorApprovalEventID  UUIDv7                   `json:"author_approval_event_id"`
	AuthorApprovalRevision uint64                   `json:"author_approval_revision"`
	PolicyRevision         uint64                   `json:"release_policy_revision"`
	PlanDigest             Digest                   `json:"plan_digest"`
	EvidenceIDs            []UUIDv7                 `json:"evidence_ids"`
	RepositoryURL          string                   `json:"repository_url,omitempty"`
	BaseRef                string                   `json:"base_ref,omitempty"`
	BaseCommit             string                   `json:"base_commit,omitempty"`
	OrderedMerges          []ReleaseMergePlan       `json:"ordered_merges,omitempty"`
	MergeStrategy          string                   `json:"merge_strategy,omitempty"`
	GitVersion             string                   `json:"git_version,omitempty"`
	ConflictPolicy         string                   `json:"conflict_policy,omitempty"`
	ContractManifest       string                   `json:"contract_manifest,omitempty"`
	ManifestSHA256         Digest                   `json:"manifest_sha256,omitempty"`
	RequiredProfiles       []string                 `json:"required_profiles,omitempty"`
	ExpectedQualifiedTree  string                   `json:"expected_qualified_tree,omitempty"`
	ExecutionRoundLimit    uint64                   `json:"execution_round_limit,omitempty"`
	NoReleaseReason        string                   `json:"no_release_reason,omitempty"`
	Qualification          *ReleaseQualification    `json:"qualification,omitempty"`
	NextMergeIndex         uint64                   `json:"next_merge_index"`
	NextRound              uint64                   `json:"next_round"`
	ActiveAttempt          *ReleaseAttempt          `json:"active_attempt,omitempty"`
	Results                map[UUIDv7]ReleaseResult `json:"results"`
	FinalizationEventID    UUIDv7                   `json:"finalization_event_id,omitempty"`
	ProviderTreeDigest     string                   `json:"provider_tree_digest,omitempty"`
}

func (snapshot ReleasePlanSnapshot) Key() ReleasePlanKey {
	return ReleasePlanKey{Story: snapshot.Story, LifecycleEpoch: snapshot.StoryLifecycleEpoch}
}

func (snapshot ReleasePlanSnapshot) Clone() ReleasePlanSnapshot {
	copy := snapshot
	copy.EvidenceIDs = append([]UUIDv7(nil), snapshot.EvidenceIDs...)
	copy.OrderedMerges = append([]ReleaseMergePlan(nil), snapshot.OrderedMerges...)
	copy.RequiredProfiles = append([]string(nil), snapshot.RequiredProfiles...)
	if snapshot.Qualification != nil {
		qualification := *snapshot.Qualification
		qualification.OrderedHeadCommits = append([]string(nil), snapshot.Qualification.OrderedHeadCommits...)
		qualification.ArtifactDigests = append([]Digest(nil), snapshot.Qualification.ArtifactDigests...)
		copy.Qualification = &qualification
	}
	if snapshot.ActiveAttempt != nil {
		attempt := *snapshot.ActiveAttempt
		copy.ActiveAttempt = &attempt
	}
	copy.Results = make(map[UUIDv7]ReleaseResult, len(snapshot.Results))
	for key, value := range snapshot.Results {
		copy.Results[key] = value
	}
	return copy
}

func (snapshot ReleasePlanSnapshot) Valid() bool {
	if !snapshot.ReleasePlanID.Valid() || !snapshot.OpeningEventID.Valid() || snapshot.Revision == 0 || !snapshot.Key().Valid() || snapshot.ExpectedStoryRevision == 0 || !snapshot.Author.Valid() || !snapshot.AuthorApprovalEventID.Valid() || snapshot.AuthorApprovalRevision == 0 || snapshot.PolicyRevision == 0 || !snapshot.PlanDigest.Valid() || len(snapshot.EvidenceIDs) == 0 || !uniqueValidUUIDs(snapshot.EvidenceIDs) || snapshot.Results == nil {
		return false
	}
	if snapshot.Mode == ReleaseModeNotRequired {
		if snapshot.NoReleaseReason == "" || len(snapshot.OrderedMerges) != 0 || snapshot.NextMergeIndex != 0 || snapshot.NextRound != 1 || snapshot.ActiveAttempt != nil || snapshot.Qualification != nil || len(snapshot.Results) != 0 {
			return false
		}
		if snapshot.State == ReleasePlanned {
			return snapshot.FinalizationEventID == "" && snapshot.ProviderTreeDigest == ""
		}
		return snapshot.State == ReleaseNotRequired && snapshot.FinalizationEventID.Valid() && snapshot.ProviderTreeDigest == ""
	}
	if snapshot.Mode != ReleaseModeCode || snapshot.RepositoryURL == "" || snapshot.BaseRef == "" || snapshot.BaseCommit == "" || len(snapshot.OrderedMerges) == 0 || snapshot.MergeStrategy != "FF_ONLY_ORDERED" || snapshot.ConflictPolicy != "FAIL_NO_IMPROVISATION" || snapshot.ContractManifest != ContractIdentity || !snapshot.ManifestSHA256.Valid() || snapshot.ExpectedQualifiedTree == "" || snapshot.ExecutionRoundLimit == 0 || snapshot.ExecutionRoundLimit > 1000 {
		return false
	}
	seen := make(map[UUIDv7]struct{}, len(snapshot.OrderedMerges))
	for _, merge := range snapshot.OrderedMerges {
		if !merge.MergeID.Valid() || merge.ChangeRef == "" || merge.HeadCommit == "" || merge.Role == "" {
			return false
		}
		if _, duplicate := seen[merge.MergeID]; duplicate {
			return false
		}
		seen[merge.MergeID] = struct{}{}
	}
	if snapshot.NextMergeIndex > uint64(len(snapshot.OrderedMerges)) || snapshot.NextRound == 0 || snapshot.NextRound > snapshot.ExecutionRoundLimit {
		return false
	}
	for mergeID, result := range snapshot.Results {
		if mergeID != result.MergeID || !result.MergeID.Valid() || !result.AttemptID.Valid() || !result.ResultEventID.Valid() || (result.Outcome != ReleaseOutcomeMerged && result.Outcome != ReleaseOutcomeAlreadyMerged && result.Outcome != ReleaseOutcomeFailed && result.Outcome != ReleaseOutcomeUnknown) || (result.Reconciled && !result.ReconciliationEventID.Valid()) {
			return false
		}
	}
	if snapshot.State != ReleasePlanned && snapshot.Qualification == nil {
		return false
	}
	if snapshot.Qualification != nil {
		heads := make([]string, len(snapshot.OrderedMerges))
		for index := range snapshot.OrderedMerges {
			heads[index] = snapshot.OrderedMerges[index].HeadCommit
		}
		if !snapshot.Qualification.EventID.Valid() || !snapshot.Qualification.QualificationID.Valid() || snapshot.Qualification.QualifiedBaseCommit != snapshot.BaseCommit || !equalStrings(snapshot.Qualification.OrderedHeadCommits, heads) || snapshot.Qualification.QualifiedTreeDigest != snapshot.ExpectedQualifiedTree {
			return false
		}
	}
	switch snapshot.State {
	case ReleasePlanned:
		return snapshot.Qualification == nil && snapshot.ActiveAttempt == nil && snapshot.NextMergeIndex == 0 && snapshot.FinalizationEventID == ""
	case ReleaseQualified:
		return snapshot.ActiveAttempt == nil && snapshot.FinalizationEventID == ""
	case ReleaseExecuting, ReleaseReconciling:
		return snapshot.ActiveAttempt != nil && snapshot.ActiveAttempt.MergeID.Valid() && snapshot.ActiveAttempt.AttemptID.Valid() && snapshot.ActiveAttempt.Round > 0 && snapshot.ActiveAttempt.ProviderIdempotencyKey != "" && snapshot.ActiveAttempt.RequestEventID.Valid() && snapshot.FinalizationEventID == ""
	case ReleaseReadyForAcceptance:
		return snapshot.ActiveAttempt == nil && snapshot.NextMergeIndex == uint64(len(snapshot.OrderedMerges)) && snapshot.FinalizationEventID.Valid() && snapshot.ProviderTreeDigest == snapshot.ExpectedQualifiedTree
	case ReleaseFailed:
		return snapshot.ActiveAttempt == nil && snapshot.FinalizationEventID == ""
	default:
		return false
	}
}

func ReleasePlanFromCreatePayload(payload json.RawMessage, openingEventID UUIDv7) (ReleasePlanSnapshot, error) {
	var value struct {
		ReleasePlanID          UUIDv7             `json:"release_plan_id"`
		StoryID                UUIDv7             `json:"story_id"`
		StoryLifecycleEpoch    uint64             `json:"story_lifecycle_epoch"`
		ExpectedStoryRevision  uint64             `json:"expected_story_revision"`
		Author                 PrincipalRef       `json:"author"`
		AuthorApprovalEventID  UUIDv7             `json:"author_approval_event_id"`
		AuthorApprovalRevision uint64             `json:"author_approval_revision"`
		PolicyRevision         uint64             `json:"release_policy_revision"`
		PlanDigest             Digest             `json:"plan_digest"`
		EvidenceIDs            []UUIDv7           `json:"evidence_ids"`
		Mode                   ReleaseMode        `json:"release_mode"`
		RepositoryURL          string             `json:"repository_url"`
		BaseRef                string             `json:"base_ref"`
		BaseCommit             string             `json:"base_commit"`
		OrderedMerges          []ReleaseMergePlan `json:"ordered_merges"`
		MergeStrategy          string             `json:"merge_strategy"`
		GitVersion             string             `json:"git_version"`
		ConflictPolicy         string             `json:"conflict_policy"`
		ContractManifest       string             `json:"contract_manifest"`
		ManifestSHA256         Digest             `json:"manifest_sha256"`
		RequiredProfiles       []string           `json:"required_profiles"`
		ExpectedQualifiedTree  string             `json:"expected_qualified_tree"`
		ExecutionRoundLimit    uint64             `json:"execution_round_limit"`
		NoReleaseReason        string             `json:"no_release_reason"`
	}
	if err := json.Unmarshal(payload, &value); err != nil {
		return ReleasePlanSnapshot{}, err
	}
	snapshot := ReleasePlanSnapshot{ReleasePlanID: value.ReleasePlanID, OpeningEventID: openingEventID, Revision: 1, State: ReleasePlanned, Mode: value.Mode, Story: AggregateRef{Kind: AggregateStory, ID: value.StoryID}, StoryLifecycleEpoch: value.StoryLifecycleEpoch, ExpectedStoryRevision: value.ExpectedStoryRevision, Author: value.Author, AuthorApprovalEventID: value.AuthorApprovalEventID, AuthorApprovalRevision: value.AuthorApprovalRevision, PolicyRevision: value.PolicyRevision, PlanDigest: value.PlanDigest, EvidenceIDs: append([]UUIDv7(nil), value.EvidenceIDs...), RepositoryURL: value.RepositoryURL, BaseRef: value.BaseRef, BaseCommit: value.BaseCommit, OrderedMerges: append([]ReleaseMergePlan(nil), value.OrderedMerges...), MergeStrategy: value.MergeStrategy, GitVersion: value.GitVersion, ConflictPolicy: value.ConflictPolicy, ContractManifest: value.ContractManifest, ManifestSHA256: value.ManifestSHA256, RequiredProfiles: append([]string(nil), value.RequiredProfiles...), ExpectedQualifiedTree: value.ExpectedQualifiedTree, ExecutionRoundLimit: value.ExecutionRoundLimit, NoReleaseReason: value.NoReleaseReason, NextRound: 1, Results: map[UUIDv7]ReleaseResult{}}
	if !snapshot.Valid() {
		return ReleasePlanSnapshot{}, errors.New("invalid release plan")
	}
	return snapshot, nil
}

func ReleasePlanKeyFromCreatePayload(payload json.RawMessage) (ReleasePlanKey, error) {
	var value struct {
		StoryID             UUIDv7 `json:"story_id"`
		StoryLifecycleEpoch uint64 `json:"story_lifecycle_epoch"`
	}
	if err := json.Unmarshal(payload, &value); err != nil {
		return ReleasePlanKey{}, err
	}
	key := ReleasePlanKey{Story: AggregateRef{Kind: AggregateStory, ID: value.StoryID}, LifecycleEpoch: value.StoryLifecycleEpoch}
	if !key.Valid() {
		return ReleasePlanKey{}, errors.New("invalid release plan key")
	}
	return key, nil
}

type ReleaseQualificationTransition struct {
	ExpectedRevision    uint64
	PlanDigest          Digest
	QualificationID     UUIDv7
	QualifiedBaseCommit string
	OrderedHeadCommits  []string
	QualifiedTreeDigest string
	GateDefinition      Digest
	Toolchain           Digest
	DependencyLock      Digest
	ArtifactDigests     []Digest
	EvidenceIDs         []UUIDv7
	ContractManifest    string
	ManifestSHA256      Digest
	RequiredProfiles    []string
}

func ReleaseQualificationFromPayload(payload json.RawMessage) (ReleaseQualificationTransition, error) {
	var value struct {
		ExpectedRevision    uint64   `json:"expected_release_revision"`
		PlanDigest          Digest   `json:"plan_digest"`
		QualificationID     UUIDv7   `json:"qualification_id"`
		QualifiedBaseCommit string   `json:"qualified_base_commit"`
		OrderedHeadCommits  []string `json:"ordered_head_commits"`
		QualifiedTreeDigest string   `json:"qualified_tree_digest"`
		GateDefinition      Digest   `json:"gate_definition_digest"`
		Toolchain           Digest   `json:"toolchain_digest"`
		DependencyLock      Digest   `json:"dependency_lock_digest"`
		ArtifactDigests     []Digest `json:"artifact_digests"`
		EvidenceIDs         []UUIDv7 `json:"evidence_ids"`
		ContractManifest    string   `json:"contract_manifest"`
		ManifestSHA256      Digest   `json:"manifest_sha256"`
		RequiredProfiles    []string `json:"required_profiles"`
	}
	if err := json.Unmarshal(payload, &value); err != nil {
		return ReleaseQualificationTransition{}, err
	}
	result := ReleaseQualificationTransition{value.ExpectedRevision, value.PlanDigest, value.QualificationID, value.QualifiedBaseCommit, append([]string(nil), value.OrderedHeadCommits...), value.QualifiedTreeDigest, value.GateDefinition, value.Toolchain, value.DependencyLock, append([]Digest(nil), value.ArtifactDigests...), append([]UUIDv7(nil), value.EvidenceIDs...), value.ContractManifest, value.ManifestSHA256, append([]string(nil), value.RequiredProfiles...)}
	if result.ExpectedRevision == 0 || !result.PlanDigest.Valid() || !result.QualificationID.Valid() || result.QualifiedBaseCommit == "" || len(result.OrderedHeadCommits) == 0 || result.QualifiedTreeDigest == "" || !result.GateDefinition.Valid() || !result.Toolchain.Valid() || !result.DependencyLock.Valid() || len(result.ArtifactDigests) == 0 || len(result.EvidenceIDs) == 0 || result.ContractManifest != ContractIdentity || !result.ManifestSHA256.Valid() || len(result.RequiredProfiles) == 0 {
		return ReleaseQualificationTransition{}, errors.New("invalid release qualification")
	}
	return result, nil
}

func ReleaseAttemptFromPayload(payload json.RawMessage) (ReleaseAttempt, UUIDv7, uint64, Digest, []UUIDv7, error) {
	var value struct {
		ReleasePlanID    UUIDv7   `json:"release_plan_id"`
		ExpectedRevision uint64   `json:"expected_release_revision"`
		PlanDigest       Digest   `json:"plan_digest"`
		MergeID          UUIDv7   `json:"merge_id"`
		AttemptID        UUIDv7   `json:"attempt_id"`
		Round            uint64   `json:"round"`
		Key              string   `json:"provider_idempotency_key"`
		EvidenceIDs      []UUIDv7 `json:"evidence_ids"`
	}
	if err := json.Unmarshal(payload, &value); err != nil {
		return ReleaseAttempt{}, "", 0, "", nil, err
	}
	attempt := ReleaseAttempt{MergeID: value.MergeID, AttemptID: value.AttemptID, Round: value.Round, ProviderIdempotencyKey: value.Key}
	if !value.ReleasePlanID.Valid() || value.ExpectedRevision == 0 || !value.PlanDigest.Valid() || !attempt.MergeID.Valid() || !attempt.AttemptID.Valid() || attempt.Round == 0 || attempt.ProviderIdempotencyKey == "" || len(value.EvidenceIDs) == 0 {
		return ReleaseAttempt{}, "", 0, "", nil, errors.New("invalid release attempt")
	}
	return attempt, value.ReleasePlanID, value.ExpectedRevision, value.PlanDigest, append([]UUIDv7(nil), value.EvidenceIDs...), nil
}

type ReleaseResultTransition struct {
	ReleasePlanID           UUIDv7
	ExpectedRevision        uint64
	PlanDigest              Digest
	MergeID                 UUIDv7
	AttemptID               UUIDv7
	Outcome                 ReleaseOutcome
	EvidenceIDs             []UUIDv7
	SupersedesResultEventID UUIDv7
	ReconciliationID        UUIDv7
	ProviderState           string
	ObservedBase            string
	ObservedHead            string
	ObservedTree            string
}

func ReleaseResultFromPayload(payload json.RawMessage, reconciliation bool) (ReleaseResultTransition, error) {
	var value struct {
		ReleasePlanID    UUIDv7         `json:"release_plan_id"`
		ExpectedRevision uint64         `json:"expected_release_revision"`
		PlanDigest       Digest         `json:"plan_digest"`
		MergeID          UUIDv7         `json:"merge_id"`
		AttemptID        UUIDv7         `json:"attempt_id"`
		Outcome          ReleaseOutcome `json:"outcome"`
		EvidenceIDs      []UUIDv7       `json:"evidence_ids"`
		Supersedes       UUIDv7         `json:"supersedes_result_event_id"`
		ReconciliationID UUIDv7         `json:"reconciliation_id"`
		ProviderState    string         `json:"provider_state"`
		ObservedBase     string         `json:"observed_base_commit"`
		ObservedHead     string         `json:"observed_head_commit"`
		ObservedTree     string         `json:"observed_tree_digest"`
	}
	if err := json.Unmarshal(payload, &value); err != nil {
		return ReleaseResultTransition{}, err
	}
	result := ReleaseResultTransition{value.ReleasePlanID, value.ExpectedRevision, value.PlanDigest, value.MergeID, value.AttemptID, value.Outcome, append([]UUIDv7(nil), value.EvidenceIDs...), value.Supersedes, value.ReconciliationID, value.ProviderState, value.ObservedBase, value.ObservedHead, value.ObservedTree}
	if !result.ReleasePlanID.Valid() || result.ExpectedRevision == 0 || !result.PlanDigest.Valid() || !result.MergeID.Valid() || !result.AttemptID.Valid() || len(result.EvidenceIDs) == 0 || (result.Outcome != ReleaseOutcomeMerged && result.Outcome != ReleaseOutcomeAlreadyMerged && result.Outcome != ReleaseOutcomeFailed && result.Outcome != ReleaseOutcomeUnknown) {
		return ReleaseResultTransition{}, errors.New("invalid release result")
	}
	if reconciliation && (!result.SupersedesResultEventID.Valid() || !result.ReconciliationID.Valid()) {
		return ReleaseResultTransition{}, errors.New("invalid release reconciliation")
	}
	if result.Outcome == ReleaseOutcomeMerged || result.Outcome == ReleaseOutcomeAlreadyMerged {
		if result.ObservedBase == "" || result.ObservedHead == "" || result.ObservedTree == "" || (reconciliation && result.ProviderState != string(ReleaseProviderMerged)) {
			return ReleaseResultTransition{}, errors.New("invalid merged release result")
		}
	} else if result.ObservedBase != "" || result.ObservedHead != "" || result.ObservedTree != "" {
		return ReleaseResultTransition{}, errors.New("unverified release identity")
	}
	if reconciliation {
		if result.Outcome == ReleaseOutcomeFailed && result.ProviderState != string(ReleaseProviderOpen) && result.ProviderState != string(ReleaseProviderClosedUnmerged) && result.ProviderState != string(ReleaseProviderMissing) {
			return ReleaseResultTransition{}, errors.New("invalid failed provider state")
		}
		if result.Outcome == ReleaseOutcomeUnknown && result.ProviderState != string(ReleaseProviderUnavailable) {
			return ReleaseResultTransition{}, errors.New("invalid unavailable provider state")
		}
	}
	return result, nil
}

func ApplyReleaseEvent(snapshot ReleasePlanSnapshot, event DomainEvent) (ReleasePlanSnapshot, bool) {
	if !snapshot.Valid() || event.Aggregate.ID != snapshot.ReleasePlanID || event.AggregateRevision != snapshot.Revision+1 || !event.EventID.Valid() || event.CommittedAt.IsZero() {
		return ReleasePlanSnapshot{}, false
	}
	next := snapshot.Clone()
	switch event.EventType {
	case "tekroo.event.release-plan.qualification-recorded":
		value, err := ReleaseQualificationFromPayload(event.Payload)
		heads := make([]string, len(snapshot.OrderedMerges))
		for index := range snapshot.OrderedMerges {
			heads[index] = snapshot.OrderedMerges[index].HeadCommit
		}
		if err != nil || value.ExpectedRevision != snapshot.Revision || value.PlanDigest != snapshot.PlanDigest || value.QualifiedBaseCommit != snapshot.BaseCommit || !equalStrings(value.OrderedHeadCommits, heads) || value.QualifiedTreeDigest != snapshot.ExpectedQualifiedTree || value.ContractManifest != snapshot.ContractManifest || value.ManifestSHA256 != snapshot.ManifestSHA256 || !equalStrings(value.RequiredProfiles, snapshot.RequiredProfiles) || snapshot.State != ReleasePlanned {
			return ReleasePlanSnapshot{}, false
		}
		next.State = ReleaseQualified
		next.Qualification = &ReleaseQualification{EventID: event.EventID, QualificationID: value.QualificationID, QualifiedBaseCommit: value.QualifiedBaseCommit, OrderedHeadCommits: value.OrderedHeadCommits, QualifiedTreeDigest: value.QualifiedTreeDigest, GateDefinition: value.GateDefinition, Toolchain: value.Toolchain, DependencyLock: value.DependencyLock, ArtifactDigests: value.ArtifactDigests}
	case "tekroo.event.release-plan.execution-requested":
		attempt, releasePlanID, revision, plan, _, err := ReleaseAttemptFromPayload(event.Payload)
		if err != nil || releasePlanID != snapshot.ReleasePlanID || revision != snapshot.Revision || plan != snapshot.PlanDigest || snapshot.State != ReleaseQualified || snapshot.NextMergeIndex >= uint64(len(snapshot.OrderedMerges)) || snapshot.OrderedMerges[snapshot.NextMergeIndex].MergeID != attempt.MergeID || attempt.Round != snapshot.NextRound || attempt.Round > snapshot.ExecutionRoundLimit {
			return ReleasePlanSnapshot{}, false
		}
		attempt.RequestEventID = event.EventID
		next.State = ReleaseExecuting
		next.ActiveAttempt = &attempt
	case "tekroo.event.release-plan.result-recorded":
		value, err := ReleaseResultFromPayload(event.Payload, false)
		if err != nil || value.ReleasePlanID != snapshot.ReleasePlanID || value.ExpectedRevision != snapshot.Revision || value.PlanDigest != snapshot.PlanDigest || snapshot.ActiveAttempt == nil || snapshot.ActiveAttempt.AttemptID != value.AttemptID || snapshot.ActiveAttempt.MergeID != value.MergeID || snapshot.State != ReleaseExecuting || !releaseResultIdentityMatches(snapshot, value) {
			return ReleasePlanSnapshot{}, false
		}
		result := ReleaseResult{MergeID: value.MergeID, AttemptID: value.AttemptID, Outcome: value.Outcome, ResultEventID: event.EventID, ObservedTree: value.ObservedTree}
		next.Results[value.MergeID] = result
		applyReleaseOutcome(&next, result, snapshot.ActiveAttempt.Round)
	case "tekroo.event.release-plan.reconciliation-recorded":
		value, err := ReleaseResultFromPayload(event.Payload, true)
		prior, found := snapshot.Results[value.MergeID]
		if err != nil || !found || value.ReleasePlanID != snapshot.ReleasePlanID || value.ExpectedRevision != snapshot.Revision || value.PlanDigest != snapshot.PlanDigest || snapshot.State != ReleaseReconciling || snapshot.ActiveAttempt == nil || snapshot.ActiveAttempt.AttemptID != value.AttemptID || prior.ResultEventID != value.SupersedesResultEventID || prior.Outcome != ReleaseOutcomeUnknown || !releaseResultIdentityMatches(snapshot, value) {
			return ReleasePlanSnapshot{}, false
		}
		result := ReleaseResult{MergeID: value.MergeID, AttemptID: value.AttemptID, Outcome: value.Outcome, ResultEventID: prior.ResultEventID, ObservedTree: value.ObservedTree, Reconciled: true, ReconciliationEventID: event.EventID}
		next.Results[value.MergeID] = result
		applyReleaseOutcome(&next, result, snapshot.ActiveAttempt.Round)
	case "tekroo.event.release-plan.finalized":
		var value struct {
			ReleasePlanID        UUIDv7       `json:"release_plan_id"`
			ExpectedRevision     uint64       `json:"expected_release_revision"`
			PlanDigest           Digest       `json:"plan_digest"`
			Mode                 ReleaseMode  `json:"release_mode"`
			TerminalStatus       ReleaseState `json:"terminal_status"`
			ProviderTree         string       `json:"provider_tree_digest"`
			QualifiedTree        string       `json:"qualified_tree_digest"`
			QualificationEventID UUIDv7       `json:"qualification_event_id"`
			ResultEventIDs       []UUIDv7     `json:"result_event_ids"`
		}
		if json.Unmarshal(event.Payload, &value) != nil || value.ReleasePlanID != snapshot.ReleasePlanID || value.ExpectedRevision != snapshot.Revision || value.PlanDigest != snapshot.PlanDigest || value.Mode != snapshot.Mode {
			return ReleasePlanSnapshot{}, false
		}
		if snapshot.Mode == ReleaseModeCode && (snapshot.State != ReleaseQualified || snapshot.NextMergeIndex != uint64(len(snapshot.OrderedMerges)) || snapshot.Qualification == nil || value.QualificationEventID != snapshot.Qualification.EventID || value.QualifiedTree != snapshot.ExpectedQualifiedTree || value.TerminalStatus != ReleaseReadyForAcceptance || value.ProviderTree != snapshot.ExpectedQualifiedTree || snapshot.ProviderTreeDigest != snapshot.ExpectedQualifiedTree || !equalUUIDs(value.ResultEventIDs, effectiveReleaseResultEventIDs(snapshot))) {
			return ReleasePlanSnapshot{}, false
		}
		if snapshot.Mode == ReleaseModeNotRequired && (value.TerminalStatus != ReleaseNotRequired || len(value.ResultEventIDs) != 0 || value.QualificationEventID != "" || value.QualifiedTree != "" || value.ProviderTree != "") {
			return ReleasePlanSnapshot{}, false
		}
		next.State = value.TerminalStatus
		next.FinalizationEventID = event.EventID
		next.ProviderTreeDigest = value.ProviderTree
	default:
		return ReleasePlanSnapshot{}, false
	}
	next.Revision++
	return next, next.Valid()
}

func applyReleaseOutcome(next *ReleasePlanSnapshot, result ReleaseResult, round uint64) {
	switch result.Outcome {
	case ReleaseOutcomeUnknown:
		next.State = ReleaseReconciling
	case ReleaseOutcomeMerged, ReleaseOutcomeAlreadyMerged:
		next.State = ReleaseQualified
		next.ProviderTreeDigest = result.ObservedTree
		next.NextMergeIndex++
		next.NextRound = 1
		next.ActiveAttempt = nil
	case ReleaseOutcomeFailed:
		next.ActiveAttempt = nil
		if round >= next.ExecutionRoundLimit {
			next.State = ReleaseFailed
		} else {
			next.State = ReleaseQualified
			next.NextRound = round + 1
		}
	}
}

func releaseResultIdentityMatches(snapshot ReleasePlanSnapshot, value ReleaseResultTransition) bool {
	if snapshot.NextMergeIndex >= uint64(len(snapshot.OrderedMerges)) {
		return false
	}
	merge := snapshot.OrderedMerges[snapshot.NextMergeIndex]
	if value.MergeID != merge.MergeID {
		return false
	}
	if value.Outcome == ReleaseOutcomeMerged || value.Outcome == ReleaseOutcomeAlreadyMerged {
		return value.ObservedBase == snapshot.BaseCommit && value.ObservedHead == merge.HeadCommit
	}
	return true
}

func effectiveReleaseResultEventIDs(snapshot ReleasePlanSnapshot) []UUIDv7 {
	ids := make([]UUIDv7, 0, len(snapshot.OrderedMerges))
	for _, merge := range snapshot.OrderedMerges {
		result, found := snapshot.Results[merge.MergeID]
		if !found || (result.Outcome != ReleaseOutcomeMerged && result.Outcome != ReleaseOutcomeAlreadyMerged) {
			return nil
		}
		eventID := result.ResultEventID
		if result.Reconciled {
			eventID = result.ReconciliationEventID
		}
		ids = append(ids, eventID)
	}
	return ids
}

func equalUUIDs(left, right []UUIDv7) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func releaseEventTypeForCommand(commandType string) (string, bool) {
	switch commandType {
	case "tekroo.command.release-plan.record-qualification":
		return "tekroo.event.release-plan.qualification-recorded", true
	case "tekroo.command.release-plan.request-execution":
		return "tekroo.event.release-plan.execution-requested", true
	case "tekroo.command.release-plan.record-result":
		return "tekroo.event.release-plan.result-recorded", true
	case "tekroo.command.release-plan.record-reconciliation":
		return "tekroo.event.release-plan.reconciliation-recorded", true
	case "tekroo.command.release-plan.finalize":
		return "tekroo.event.release-plan.finalized", true
	default:
		return "", false
	}
}
