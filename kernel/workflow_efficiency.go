package kernel

import (
	"sort"
	"time"
)

type WorkflowTiming struct {
	CriticalPathTime       time.Duration `json:"critical_path_time"`
	DesignWallTime         time.Duration `json:"design_wall_time"`
	ImplementationWallTime time.Duration `json:"implementation_wall_time"`
	ValidationWallTime     time.Duration `json:"validation_wall_time"`
	AggregateModelTime     time.Duration `json:"aggregate_model_time"`
	QueueTime              time.Duration `json:"queue_time"`
	ModelTokens            *uint64       `json:"model_tokens,omitempty"`
	EvidenceReused         *uint64       `json:"evidence_reused,omitempty"`
	DuplicateChecksAvoided *uint64       `json:"duplicate_checks_avoided,omitempty"`
	PeakParallelism        uint32        `json:"peak_parallelism"`
}

type workflowInterval struct{ start, end time.Time }

// MeasureWorkflowTiming derives reproducible wall-clock and aggregate model
// time from durable invocation timestamps. Category wall time is the union of
// intervals, so concurrent agents are not double-counted; aggregate model time
// deliberately sums those same intervals.
func MeasureWorkflowTiming(invocations []WorkInvocation, observedAt time.Time) WorkflowTiming {
	if observedAt.IsZero() {
		return WorkflowTiming{}
	}
	var result WorkflowTiming
	categories := make(map[WorkPurpose][]workflowInterval)
	all := make([]workflowInterval, 0, len(invocations))
	for _, invocation := range invocations {
		if invocation.ClaimedAt != nil && invocation.StartedAt != nil && !invocation.StartedAt.Before(*invocation.ClaimedAt) {
			result.QueueTime += invocation.StartedAt.Sub(*invocation.ClaimedAt)
		}
		if invocation.StartedAt == nil {
			continue
		}
		end := observedAt
		if invocation.FinishedAt != nil {
			end = *invocation.FinishedAt
		}
		if end.Before(*invocation.StartedAt) {
			continue
		}
		interval := workflowInterval{start: *invocation.StartedAt, end: end}
		categories[invocation.Purpose] = append(categories[invocation.Purpose], interval)
		all = append(all, interval)
		result.AggregateModelTime += end.Sub(*invocation.StartedAt)
	}
	result.DesignWallTime = unionWorkflowDuration(append(append(append([]workflowInterval(nil), categories[PurposeHandoff]...), categories[PurposeReplan]...), categories[PurposeInvestigation]...))
	result.ImplementationWallTime = unionWorkflowDuration(append(append([]workflowInterval(nil), categories[PurposeImplementation]...), categories[PurposeRepair]...))
	result.ValidationWallTime = unionWorkflowDuration(append(append(append([]workflowInterval(nil), categories[PurposeValidation]...), categories[PurposeReview]...), categories[PurposePromotion]...))
	result.CriticalPathTime = workflowSpanDuration(all)
	result.PeakParallelism = peakWorkflowParallelism(all)
	return result
}

func workflowSpanDuration(intervals []workflowInterval) time.Duration {
	if len(intervals) == 0 {
		return 0
	}
	start, end := intervals[0].start, intervals[0].end
	for _, interval := range intervals[1:] {
		if interval.start.Before(start) {
			start = interval.start
		}
		if interval.end.After(end) {
			end = interval.end
		}
	}
	return end.Sub(start)
}

func unionWorkflowDuration(intervals []workflowInterval) time.Duration {
	if len(intervals) == 0 {
		return 0
	}
	sort.Slice(intervals, func(left, right int) bool {
		if intervals[left].start.Equal(intervals[right].start) {
			return intervals[left].end.Before(intervals[right].end)
		}
		return intervals[left].start.Before(intervals[right].start)
	})
	start, end := intervals[0].start, intervals[0].end
	var total time.Duration
	for _, interval := range intervals[1:] {
		if !interval.start.After(end) {
			if interval.end.After(end) {
				end = interval.end
			}
			continue
		}
		total += end.Sub(start)
		start, end = interval.start, interval.end
	}
	return total + end.Sub(start)
}

func peakWorkflowParallelism(intervals []workflowInterval) uint32 {
	type boundary struct {
		at    time.Time
		delta int
	}
	boundaries := make([]boundary, 0, 2*len(intervals))
	for _, interval := range intervals {
		boundaries = append(boundaries, boundary{at: interval.start, delta: 1}, boundary{at: interval.end, delta: -1})
	}
	sort.Slice(boundaries, func(left, right int) bool {
		if boundaries[left].at.Equal(boundaries[right].at) {
			return boundaries[left].delta < boundaries[right].delta
		}
		return boundaries[left].at.Before(boundaries[right].at)
	})
	current, peak := 0, 0
	for _, item := range boundaries {
		current += item.delta
		if current > peak {
			peak = current
		}
	}
	return uint32(peak)
}

type ValidationTimingDecision struct {
	AdmitAdditionalValidation bool
	AboveTarget               bool
	AtHardCeiling             bool
}

func EvaluateValidationTiming(timing WorkflowTiming) ValidationTimingDecision {
	productive := timing.DesignWallTime + timing.ImplementationWallTime
	if productive <= 0 {
		return ValidationTimingDecision{}
	}
	decision := ValidationTimingDecision{AdmitAdditionalValidation: timing.ValidationWallTime < productive}
	decision.AboveTarget = timing.ValidationWallTime*2 > productive
	decision.AtHardCeiling = timing.ValidationWallTime >= productive
	return decision
}

type ValidationEvidenceKey struct {
	CandidateDigest   Digest
	CriterionDigest   Digest
	ToolchainDigest   Digest
	EnvironmentDigest Digest
}

func (key ValidationEvidenceKey) Valid() bool {
	return key.CandidateDigest.Valid() && key.CriterionDigest.Valid() && key.ToolchainDigest.Valid() && key.EnvironmentDigest.Valid()
}

type ValidationEvidence struct {
	Key        ValidationEvidenceKey
	EvidenceID UUIDv7
	Passed     bool
}

type ValidationSelection struct {
	ReuseEvidence       bool
	RunDeterministic    bool
	RunIndependentModel bool
	RunSpecialized      bool
	Reason              string
}

func SelectValidation(risk WorkflowRisk, policy WorkflowValidationPolicy, key ValidationEvidenceKey, existing *ValidationEvidence, changedCondition bool, timing WorkflowTiming) ValidationSelection {
	if !risk.Valid() || !policy.Valid() || !key.Valid() {
		return ValidationSelection{Reason: "INVALID_VALIDATION_REQUEST"}
	}
	if existing != nil && existing.Passed && existing.Key == key && !changedCondition {
		return ValidationSelection{ReuseEvidence: true, Reason: "UNCHANGED_PASSING_EVIDENCE"}
	}
	selection := ValidationSelection{RunDeterministic: true, Reason: "VALIDATION_SELECTED"}
	if EvaluateValidationTiming(timing).AtHardCeiling {
		return ValidationSelection{Reason: "VALIDATION_TIME_CEILING"}
	}
	switch policy {
	case WorkflowValidationDeterministic:
	case WorkflowValidationRiskSelected:
		selection.RunIndependentModel = risk == WorkflowRiskModerate || risk == WorkflowRiskHigh || risk == WorkflowRiskCritical
	case WorkflowValidationSpecialized:
		selection.RunSpecialized = true
	case WorkflowValidationOperatorApproved:
		selection.Reason = "OPERATOR_APPROVAL_REQUIRED"
	}
	return selection
}
