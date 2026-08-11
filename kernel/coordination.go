package kernel

import (
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"time"
)

type ReviewBranchResult struct {
	BranchID                    string          `json:"branchId"`
	SourceRole                  string          `json:"sourceRole,omitempty"`
	Authority                   PrincipalRef    `json:"authority,omitempty"`
	Round                       uint64          `json:"round,omitempty"`
	Result                      string          `json:"result"`
	Reasons                     []string        `json:"reasons,omitempty"`
	EvidenceIDs                 []UUIDv7        `json:"evidenceIds,omitempty"`
	Findings                    []ReviewFinding `json:"findings,omitempty"`
	SupersedesResultEventIDs    []UUIDv7        `json:"supersedesResultEventIds,omitempty"`
	ChangedConditionEvidenceIDs []UUIDv7        `json:"changedConditionEvidenceIds,omitempty"`
	EventID                     UUIDv7          `json:"eventId,omitempty"`
	DecidedAt                   time.Time       `json:"decidedAt,omitempty"`
}

type ReviewFinding struct {
	FindingID      UUIDv7   `json:"findingId"`
	FindingKey     Digest   `json:"findingKey"`
	Classification string   `json:"classification"`
	Summary        string   `json:"summary"`
	EvidenceIDs    []UUIDv7 `json:"evidenceIds"`
}

type ReviewBranchSpec struct {
	BranchID           string       `json:"branch_id"`
	Validator          PrincipalRef `json:"validator"`
	ResolutionOwnerFQN ActorFQN     `json:"resolution_owner_fqn"`
	AcceptanceCriteria []string     `json:"acceptance_criteria"`
	InputEvidenceIDs   []UUIDv7     `json:"input_evidence_ids"`
	DeadlineAt         time.Time    `json:"deadline_at"`
	RoundLimit         uint64       `json:"round_limit"`
}

type ReviewAdjudication struct {
	Adjudicator PrincipalRef `json:"adjudicator"`
	DeadlineAt  time.Time    `json:"deadline_at"`
	RoundLimit  uint64       `json:"round_limit"`
}

type ReviewFinalization struct {
	EventID        UUIDv7   `json:"eventId"`
	ReviewRevision uint64   `json:"reviewRevision"`
	TerminalStatus string   `json:"terminalStatus"`
	ResultEventIDs []UUIDv7 `json:"resultEventIds"`
	EvidenceIDs    []UUIDv7 `json:"evidenceIds"`
}

type ReviewJoinResult struct {
	Complete bool   `json:"complete"`
	Status   string `json:"status"`
}

type CompletionReviewSnapshot struct {
	ReviewID             UUIDv7
	Subject              AggregateRef
	LifecycleEpoch       uint64
	CriteriaRevision     uint64
	EvidenceSetDigest    Digest
	BranchPolicyRevision uint64
	RequiredBranchIDs    []string
	Branches             map[string]ReviewBranchSpec
	Adjudication         ReviewAdjudication
	PartialResultPolicy  string
	Results              map[string]string
	ResultRecords        map[string]ReviewBranchResult
	KnownResultEvents    map[UUIDv7]ReviewBranchResult
	CurrentFindings      map[Digest]ReviewFinding
	ReviewRevision       uint64
	Finalization         *ReviewFinalization
	Join                 ReviewJoinResult
}

func (s CompletionReviewSnapshot) Clone() CompletionReviewSnapshot {
	copy := s
	copy.RequiredBranchIDs = append([]string(nil), s.RequiredBranchIDs...)
	copy.Results = make(map[string]string, len(s.Results))
	for branch, result := range s.Results {
		copy.Results[branch] = result
	}
	copy.Branches = make(map[string]ReviewBranchSpec, len(s.Branches))
	for branch, spec := range s.Branches {
		spec.AcceptanceCriteria = append([]string(nil), spec.AcceptanceCriteria...)
		spec.InputEvidenceIDs = append([]UUIDv7(nil), spec.InputEvidenceIDs...)
		copy.Branches[branch] = spec
	}
	copy.ResultRecords = make(map[string]ReviewBranchResult, len(s.ResultRecords))
	for branch, result := range s.ResultRecords {
		copy.ResultRecords[branch] = cloneReviewBranchResult(result)
	}
	copy.KnownResultEvents = make(map[UUIDv7]ReviewBranchResult, len(s.KnownResultEvents))
	for eventID, result := range s.KnownResultEvents {
		copy.KnownResultEvents[eventID] = cloneReviewBranchResult(result)
	}
	copy.CurrentFindings = make(map[Digest]ReviewFinding, len(s.CurrentFindings))
	for key, finding := range s.CurrentFindings {
		finding.EvidenceIDs = append([]UUIDv7(nil), finding.EvidenceIDs...)
		copy.CurrentFindings[key] = finding
	}
	if s.Finalization != nil {
		value := *s.Finalization
		value.ResultEventIDs = append([]UUIDv7(nil), value.ResultEventIDs...)
		value.EvidenceIDs = append([]UUIDv7(nil), value.EvidenceIDs...)
		copy.Finalization = &value
	}
	return copy
}

func cloneReviewBranchResult(value ReviewBranchResult) ReviewBranchResult {
	value.Reasons = append([]string(nil), value.Reasons...)
	value.EvidenceIDs = append([]UUIDv7(nil), value.EvidenceIDs...)
	value.SupersedesResultEventIDs = append([]UUIDv7(nil), value.SupersedesResultEventIDs...)
	value.ChangedConditionEvidenceIDs = append([]UUIDv7(nil), value.ChangedConditionEvidenceIDs...)
	value.Findings = append([]ReviewFinding(nil), value.Findings...)
	for index := range value.Findings {
		value.Findings[index].EvidenceIDs = append([]UUIDv7(nil), value.Findings[index].EvidenceIDs...)
	}
	return value
}

// EvaluateAllPassJoin is order-independent. A repeated branch is accepted only
// when its result is identical; contradictory repeats make the join a conflict.
func EvaluateAllPassJoin(required []string, results []ReviewBranchResult) ReviewJoinResult {
	return EvaluateReviewJoin(required, "WAIT_ALL", results)
}

func EvaluateReviewJoin(required []string, partialResultPolicy string, results []ReviewBranchResult) ReviewJoinResult {
	if partialResultPolicy != "WAIT_ALL" && partialResultPolicy != "FAIL_FAST" {
		return ReviewJoinResult{Status: "CONFLICT"}
	}
	requiredSet := make(map[string]struct{}, len(required))
	if len(required) == 0 || len(required) > 32 || len(results) > 64 {
		return ReviewJoinResult{Status: "CONFLICT"}
	}
	for _, branch := range required {
		if branch == "" || len(branch) > 4096 {
			return ReviewJoinResult{Status: "CONFLICT"}
		}
		if _, duplicate := requiredSet[branch]; duplicate {
			return ReviewJoinResult{Status: "CONFLICT"}
		}
		requiredSet[branch] = struct{}{}
	}
	observed := make(map[string]string, len(results))
	for _, result := range results {
		if !validReviewResult(result.Result) {
			return ReviewJoinResult{Status: "CONFLICT"}
		}
		if _, requiredBranch := requiredSet[result.BranchID]; !requiredBranch {
			return ReviewJoinResult{Status: "CONFLICT"}
		}
		if prior, duplicate := observed[result.BranchID]; duplicate && prior != result.Result {
			return ReviewJoinResult{Status: "CONFLICT"}
		}
		observed[result.BranchID] = result.Result
	}
	terminal := deterministicReviewFailure(observed)
	if terminal != "" && partialResultPolicy == "FAIL_FAST" {
		return ReviewJoinResult{Complete: true, Status: terminal}
	}
	if len(observed) != len(requiredSet) {
		return ReviewJoinResult{Status: "PENDING"}
	}
	if terminal != "" {
		return ReviewJoinResult{Complete: true, Status: terminal}
	}
	return ReviewJoinResult{Complete: true, Status: "PASS"}
}

func deterministicReviewFailure(results map[string]string) string {
	for _, candidate := range []string{"FAIL", "BLOCKED", "INCONCLUSIVE", "CANCELLED", "SUPERSEDED"} {
		for _, result := range results {
			if result == candidate {
				return candidate
			}
		}
	}
	return ""
}

func validReviewResult(value string) bool {
	switch value {
	case "PASS", "FAIL", "BLOCKED", "INCONCLUSIVE", "SUPERSEDED", "CANCELLED":
		return true
	default:
		return false
	}
}

func CompletionReviewFromPayload(payload json.RawMessage) (CompletionReviewSnapshot, error) {
	var value struct {
		SubjectKind          string   `json:"subject_kind"`
		SubjectID            UUIDv7   `json:"subject_id"`
		LifecycleEpoch       uint64   `json:"lifecycle_epoch"`
		CriteriaRevision     uint64   `json:"criteria_revision"`
		EvidenceSetDigest    Digest   `json:"evidence_set_digest"`
		BranchPolicyRevision uint64   `json:"branch_policy_revision"`
		RequiredBranchIDs    []string `json:"required_branch_ids"`
		Branches             []struct {
			BranchID           string       `json:"branch_id"`
			Validator          PrincipalRef `json:"validator"`
			ResolutionOwnerFQN ActorFQN     `json:"resolution_owner_fqn"`
			AcceptanceCriteria []string     `json:"acceptance_criteria"`
			InputEvidenceIDs   []UUIDv7     `json:"input_evidence_ids"`
			DeadlineAt         time.Time    `json:"deadline_at"`
			RoundLimit         uint64       `json:"round_limit"`
		} `json:"branches"`
		Adjudication struct {
			Adjudicator PrincipalRef `json:"adjudicator"`
			DeadlineAt  time.Time    `json:"deadline_at"`
			RoundLimit  uint64       `json:"round_limit"`
		} `json:"adjudication"`
		JoinRule            string `json:"join_rule"`
		PartialResultPolicy string `json:"partial_result_policy"`
	}
	if json.Unmarshal(payload, &value) != nil {
		return CompletionReviewSnapshot{}, errors.New("invalid completion review policy")
	}
	subject := AggregateRef{Kind: AggregateKind(value.SubjectKind), ID: value.SubjectID}
	if !subject.Valid() || value.LifecycleEpoch == 0 || value.CriteriaRevision == 0 || !value.EvidenceSetDigest.Valid() || value.BranchPolicyRevision == 0 || value.JoinRule != "ALL_PASS" || (value.PartialResultPolicy != "WAIT_ALL" && value.PartialResultPolicy != "FAIL_FAST") {
		return CompletionReviewSnapshot{}, errors.New("invalid completion review policy")
	}
	legacy := len(value.Branches) == 0
	if legacy && len(value.RequiredBranchIDs) == 0 {
		return CompletionReviewSnapshot{}, errors.New("invalid completion review branches")
	}
	branches := make(map[string]ReviewBranchSpec, len(value.Branches))
	required := append([]string(nil), value.RequiredBranchIDs...)
	if !legacy {
		required = required[:0]
		for _, branch := range value.Branches {
			if branch.BranchID == "" || !branch.Validator.Valid() || !branch.ResolutionOwnerFQN.Valid() || !validUniqueBoundedStrings(branch.AcceptanceCriteria) || !validUUIDSet(branch.InputEvidenceIDs, true) || branch.DeadlineAt.IsZero() || branch.RoundLimit == 0 || branch.RoundLimit > 1000 {
				return CompletionReviewSnapshot{}, errors.New("invalid bounded completion review branch")
			}
			if _, duplicate := branches[branch.BranchID]; duplicate {
				return CompletionReviewSnapshot{}, errors.New("duplicate completion review branch")
			}
			criteria := append([]string(nil), branch.AcceptanceCriteria...)
			sort.Strings(criteria)
			evidence := append([]UUIDv7(nil), branch.InputEvidenceIDs...)
			sort.Slice(evidence, func(i, j int) bool { return evidence[i] < evidence[j] })
			branches[branch.BranchID] = ReviewBranchSpec{BranchID: branch.BranchID, Validator: branch.Validator, ResolutionOwnerFQN: branch.ResolutionOwnerFQN, AcceptanceCriteria: criteria, InputEvidenceIDs: evidence, DeadlineAt: branch.DeadlineAt, RoundLimit: branch.RoundLimit}
			required = append(required, branch.BranchID)
		}
		if !value.Adjudication.Adjudicator.Valid() || value.Adjudication.DeadlineAt.IsZero() || value.Adjudication.RoundLimit == 0 || value.Adjudication.RoundLimit > 1000 {
			return CompletionReviewSnapshot{}, errors.New("invalid completion review adjudication")
		}
	}
	join := EvaluateReviewJoin(required, value.PartialResultPolicy, nil)
	if join.Status == "CONFLICT" {
		return CompletionReviewSnapshot{}, errors.New("invalid completion review branches")
	}
	required, _ = canonicalValidationBranches(required)
	return CompletionReviewSnapshot{
		Subject:              subject,
		LifecycleEpoch:       value.LifecycleEpoch,
		CriteriaRevision:     value.CriteriaRevision,
		EvidenceSetDigest:    value.EvidenceSetDigest,
		BranchPolicyRevision: value.BranchPolicyRevision,
		RequiredBranchIDs:    required,
		Branches:             branches,
		Adjudication:         ReviewAdjudication{Adjudicator: value.Adjudication.Adjudicator, DeadlineAt: value.Adjudication.DeadlineAt, RoundLimit: value.Adjudication.RoundLimit},
		PartialResultPolicy:  value.PartialResultPolicy,
		Results:              make(map[string]string),
		ResultRecords:        make(map[string]ReviewBranchResult),
		KnownResultEvents:    make(map[UUIDv7]ReviewBranchResult),
		CurrentFindings:      make(map[Digest]ReviewFinding),
		ReviewRevision:       1,
		Join:                 join,
	}, nil
}

func ReviewBranchResultFromPayload(payload json.RawMessage) (UUIDv7, uint64, ReviewBranchResult, error) {
	var value struct {
		ReviewID             UUIDv7   `json:"review_id"`
		BranchID             string   `json:"branch_id"`
		BranchPolicyRevision uint64   `json:"branch_policy_revision"`
		SourceRole           string   `json:"source_role"`
		Round                uint64   `json:"round"`
		Result               string   `json:"result"`
		Reasons              []string `json:"reasons"`
		EvidenceIDs          []UUIDv7 `json:"evidence_ids"`
		Findings             []struct {
			FindingID      UUIDv7   `json:"finding_id"`
			FindingKey     Digest   `json:"finding_key"`
			Classification string   `json:"classification"`
			Summary        string   `json:"summary"`
			EvidenceIDs    []UUIDv7 `json:"evidence_ids"`
		} `json:"findings"`
		SupersedesResultEventIDs    []UUIDv7 `json:"supersedes_result_event_ids"`
		ChangedConditionEvidenceIDs []UUIDv7 `json:"changed_condition_evidence_ids"`
	}
	if json.Unmarshal(payload, &value) != nil || !value.ReviewID.Valid() || value.BranchID == "" || value.BranchPolicyRevision == 0 || !validReviewResult(value.Result) {
		return "", 0, ReviewBranchResult{}, errors.New("invalid review branch result")
	}
	result := ReviewBranchResult{BranchID: value.BranchID, SourceRole: value.SourceRole, Round: value.Round, Result: value.Result, Reasons: value.Reasons, EvidenceIDs: value.EvidenceIDs, SupersedesResultEventIDs: value.SupersedesResultEventIDs, ChangedConditionEvidenceIDs: value.ChangedConditionEvidenceIDs}
	findingKeys := make(map[Digest]struct{}, len(value.Findings))
	for _, finding := range value.Findings {
		if !finding.FindingID.Valid() || !finding.FindingKey.Valid() || finding.Summary == "" || !validUUIDSet(finding.EvidenceIDs, true) {
			return "", 0, ReviewBranchResult{}, errors.New("invalid review finding")
		}
		if _, duplicate := findingKeys[finding.FindingKey]; duplicate {
			return "", 0, ReviewBranchResult{}, errors.New("duplicate review finding key")
		}
		findingKeys[finding.FindingKey] = struct{}{}
		evidence := append([]UUIDv7(nil), finding.EvidenceIDs...)
		sort.Slice(evidence, func(i, j int) bool { return evidence[i] < evidence[j] })
		result.Findings = append(result.Findings, ReviewFinding{FindingID: finding.FindingID, FindingKey: finding.FindingKey, Classification: finding.Classification, Summary: finding.Summary, EvidenceIDs: evidence})
	}
	if value.SourceRole != "" && (value.Round == 0 || value.Round > 1000 || !validUUIDSet(value.EvidenceIDs, true) || !validUUIDSet(value.SupersedesResultEventIDs, false) || !validUUIDSet(value.ChangedConditionEvidenceIDs, false)) {
		return "", 0, ReviewBranchResult{}, errors.New("invalid bounded review branch result")
	}
	sort.Strings(result.Reasons)
	sort.Slice(result.EvidenceIDs, func(i, j int) bool { return result.EvidenceIDs[i] < result.EvidenceIDs[j] })
	sort.Slice(result.Findings, func(i, j int) bool { return result.Findings[i].FindingKey < result.Findings[j].FindingKey })
	sort.Slice(result.SupersedesResultEventIDs, func(i, j int) bool { return result.SupersedesResultEventIDs[i] < result.SupersedesResultEventIDs[j] })
	sort.Slice(result.ChangedConditionEvidenceIDs, func(i, j int) bool {
		return result.ChangedConditionEvidenceIDs[i] < result.ChangedConditionEvidenceIDs[j]
	})
	return value.ReviewID, value.BranchPolicyRevision, result, nil
}

func ApplyReviewBranchResult(snapshot CompletionReviewSnapshot, result ReviewBranchResult) (CompletionReviewSnapshot, bool) {
	if len(snapshot.Branches) > 0 {
		return ApplyBoundedReviewBranchResult(snapshot, result)
	}
	next := snapshot.Clone()
	for _, required := range next.RequiredBranchIDs {
		if required != result.BranchID {
			continue
		}
		if prior, exists := next.Results[result.BranchID]; exists && prior != result.Result {
			return snapshot, false
		}
		next.Results[result.BranchID] = result.Result
		values := make([]ReviewBranchResult, 0, len(next.Results))
		for branch, branchResult := range next.Results {
			values = append(values, ReviewBranchResult{BranchID: branch, Result: branchResult})
		}
		next.Join = EvaluateReviewJoin(next.RequiredBranchIDs, next.PartialResultPolicy, values)
		return next, next.Join.Status != "CONFLICT"
	}
	return snapshot, false
}

func ApplyBoundedReviewBranchResult(snapshot CompletionReviewSnapshot, result ReviewBranchResult) (CompletionReviewSnapshot, bool) {
	next := snapshot.Clone()
	if BoundedReviewResultReason(next, result) != "" {
		return snapshot, false
	}
	next.ResultRecords[result.BranchID] = cloneReviewBranchResult(result)
	next.KnownResultEvents[result.EventID] = cloneReviewBranchResult(result)
	next.CurrentFindings, _ = currentFindingSet(next.ResultRecords)
	next.Results[result.BranchID] = result.Result
	next.ReviewRevision++
	values := make([]ReviewBranchResult, 0, len(next.Results))
	for branch, branchResult := range next.Results {
		values = append(values, ReviewBranchResult{BranchID: branch, Result: branchResult})
	}
	next.Join = EvaluateReviewJoin(next.RequiredBranchIDs, next.PartialResultPolicy, values)
	return next, next.Join.Status != "CONFLICT"
}

func BoundedReviewResultReason(snapshot CompletionReviewSnapshot, result ReviewBranchResult) string {
	if snapshot.Finalization != nil {
		return "REVIEW_ALREADY_FINALIZED"
	}
	spec, required := snapshot.Branches[result.BranchID]
	if !required || !result.EventID.Valid() || result.DecidedAt.IsZero() || !result.Authority.Valid() || result.Round == 0 || !validReviewResult(result.Result) || !validUUIDSet(result.EvidenceIDs, true) {
		return "INVALID_REVIEW_RESULT"
	}
	switch result.SourceRole {
	case "VALIDATOR":
		if result.Authority != spec.Validator {
			return "VALIDATOR_MISMATCH"
		}
		if result.Round > spec.RoundLimit {
			return "BRANCH_ROUND_EXHAUSTED"
		}
		if result.DecidedAt.After(spec.DeadlineAt) {
			return "BRANCH_DEADLINE_EXCEEDED"
		}
	case "ADJUDICATOR":
		if result.Authority != snapshot.Adjudication.Adjudicator {
			return "ADJUDICATOR_MISMATCH"
		}
		if result.Round > snapshot.Adjudication.RoundLimit {
			return "ADJUDICATION_EXHAUSTED"
		}
		if result.DecidedAt.After(snapshot.Adjudication.DeadlineAt) {
			return "ADJUDICATION_DEADLINE_EXCEEDED"
		}
	case "POLICY_TIMEOUT":
		if result.Authority.Kind != PrincipalPolicy {
			return "POLICY_AUTHORITY_REQUIRED"
		}
		if !result.DecidedAt.After(spec.DeadlineAt) {
			return "DEADLINE_NOT_REACHED"
		}
		if result.Result != "BLOCKED" && result.Result != "CANCELLED" {
			return "INVALID_TIMEOUT_RESULT"
		}
	default:
		return "INVALID_SOURCE_ROLE"
	}
	if _, duplicateEvent := snapshot.KnownResultEvents[result.EventID]; duplicateEvent {
		return "DUPLICATE_RESULT_EVENT"
	}
	prior, hasPrior := snapshot.ResultRecords[result.BranchID]
	if hasPrior {
		if len(result.SupersedesResultEventIDs) != 1 || result.SupersedesResultEventIDs[0] != prior.EventID || len(result.ChangedConditionEvidenceIDs) == 0 || (result.SourceRole == prior.SourceRole && result.Round <= prior.Round) {
			return "CHANGED_CONDITION_REQUIRED"
		}
	} else if len(result.SupersedesResultEventIDs) != 0 {
		return "UNKNOWN_SUPERSEDED_RESULT"
	}
	candidate := make(map[string]ReviewBranchResult, len(snapshot.ResultRecords)+1)
	for branch, current := range snapshot.ResultRecords {
		candidate[branch] = current
	}
	candidate[result.BranchID] = result
	if _, valid := currentFindingSet(candidate); !valid {
		return "FINDING_KEY_CONFLICT"
	}
	return ""
}

func currentFindingSet(results map[string]ReviewBranchResult) (map[Digest]ReviewFinding, bool) {
	findings := make(map[Digest]ReviewFinding)
	for _, result := range results {
		for _, finding := range result.Findings {
			prior, duplicate := findings[finding.FindingKey]
			if duplicate && !reflect.DeepEqual(prior, finding) {
				return nil, false
			}
			if !duplicate {
				copy := finding
				copy.EvidenceIDs = append([]UUIDv7(nil), finding.EvidenceIDs...)
				findings[finding.FindingKey] = copy
			}
		}
	}
	return findings, true
}

func ApplyReviewFinalization(snapshot CompletionReviewSnapshot, eventID UUIDv7, payload json.RawMessage) (CompletionReviewSnapshot, bool) {
	var value struct {
		ReviewID               UUIDv7   `json:"review_id"`
		SubjectKind            string   `json:"subject_kind"`
		SubjectID              UUIDv7   `json:"subject_id"`
		LifecycleEpoch         uint64   `json:"lifecycle_epoch"`
		BranchPolicyRevision   uint64   `json:"branch_policy_revision"`
		ExpectedReviewRevision uint64   `json:"expected_review_revision"`
		TerminalStatus         string   `json:"terminal_status"`
		ResultEventIDs         []UUIDv7 `json:"result_event_ids"`
		EvidenceIDs            []UUIDv7 `json:"evidence_ids"`
	}
	if json.Unmarshal(payload, &value) != nil {
		return snapshot, false
	}
	subject := AggregateRef{Kind: AggregateKind(value.SubjectKind), ID: value.SubjectID}
	if !value.ReviewID.Valid() || !eventID.Valid() || snapshot.Finalization != nil || subject != snapshot.Subject || value.LifecycleEpoch != snapshot.LifecycleEpoch || value.BranchPolicyRevision != snapshot.BranchPolicyRevision || value.ExpectedReviewRevision != snapshot.ReviewRevision || !snapshot.Join.Complete || value.TerminalStatus != snapshot.Join.Status || !validUUIDSet(value.ResultEventIDs, true) || !validUUIDSet(value.EvidenceIDs, true) {
		return snapshot, false
	}
	want := make([]UUIDv7, 0, len(snapshot.ResultRecords))
	for _, result := range snapshot.ResultRecords {
		want = append(want, result.EventID)
	}
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	got := append([]UUIDv7(nil), value.ResultEventIDs...)
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	if !reflect.DeepEqual(want, got) {
		return snapshot, false
	}
	next := snapshot.Clone()
	next.Finalization = &ReviewFinalization{EventID: eventID, ReviewRevision: value.ExpectedReviewRevision, TerminalStatus: value.TerminalStatus, ResultEventIDs: got, EvidenceIDs: append([]UUIDv7(nil), value.EvidenceIDs...)}
	return next, true
}

func validUUIDSet(values []UUIDv7, required bool) bool {
	if required && len(values) == 0 {
		return false
	}
	seen := make(map[UUIDv7]struct{}, len(values))
	for _, value := range values {
		if !value.Valid() {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validUniqueBoundedStrings(values []string) bool {
	if len(values) == 0 || len(values) > 64 {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" || len(value) > 4096 {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func CanonicalSuccessorIDs(ids []UUIDv7) bool {
	if len(ids) == 0 {
		return false
	}
	for index, id := range ids {
		if !id.Valid() {
			return false
		}
		if index > 0 && ids[index-1] >= id {
			return false
		}
	}
	return sort.SliceIsSorted(ids, func(i, j int) bool { return ids[i] < ids[j] })
}

func CompatibilityOutcome(sourceVersion string, exactContextAvailable bool) string {
	if sourceVersion == SchemaVersion && exactContextAvailable {
		return "ACCEPTED"
	}
	return "MIGRATION_REQUIRED"
}
