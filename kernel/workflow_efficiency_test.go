package kernel

import (
	"testing"
	"time"
)

func TestValidationTimingTargetAndHardCeiling(t *testing.T) {
	within := EvaluateValidationTiming(WorkflowTiming{DesignWallTime: time.Hour, ImplementationWallTime: time.Hour, ValidationWallTime: 45 * time.Minute})
	if !within.AdmitAdditionalValidation || within.AboveTarget || within.AtHardCeiling {
		t.Fatalf("within-target decision=%+v", within)
	}
	above := EvaluateValidationTiming(WorkflowTiming{DesignWallTime: time.Hour, ImplementationWallTime: time.Hour, ValidationWallTime: 90 * time.Minute})
	if !above.AdmitAdditionalValidation || !above.AboveTarget || above.AtHardCeiling {
		t.Fatalf("above-target decision=%+v", above)
	}
	ceiling := EvaluateValidationTiming(WorkflowTiming{DesignWallTime: time.Hour, ImplementationWallTime: time.Hour, ValidationWallTime: 2 * time.Hour})
	if ceiling.AdmitAdditionalValidation || !ceiling.AtHardCeiling {
		t.Fatalf("ceiling decision=%+v", ceiling)
	}
}

func TestValidationReusesExactEvidenceAndSelectsByRisk(t *testing.T) {
	key := ValidationEvidenceKey{CandidateDigest: workflowDigest('a'), CriterionDigest: workflowDigest('b'), ToolchainDigest: workflowDigest('c'), EnvironmentDigest: workflowDigest('d')}
	evidence := &ValidationEvidence{Key: key, EvidenceID: workflowTestID(500), Passed: true}
	selection := SelectValidation(WorkflowRiskModerate, WorkflowValidationRiskSelected, key, evidence, false, WorkflowTiming{})
	if !selection.ReuseEvidence || selection.RunDeterministic || selection.RunIndependentModel {
		t.Fatalf("evidence was not reused: %+v", selection)
	}
	selection = SelectValidation(WorkflowRiskModerate, WorkflowValidationRiskSelected, key, evidence, true, WorkflowTiming{})
	if selection.ReuseEvidence || !selection.RunDeterministic || !selection.RunIndependentModel {
		t.Fatalf("changed moderate validation=%+v", selection)
	}
	selection = SelectValidation(WorkflowRiskLow, WorkflowValidationDeterministic, key, nil, false, WorkflowTiming{})
	if !selection.RunDeterministic || selection.RunIndependentModel || selection.RunSpecialized {
		t.Fatalf("low-risk validation=%+v", selection)
	}
	selection = SelectValidation(WorkflowRiskModerate, WorkflowValidationRiskSelected, key, nil, false, WorkflowTiming{DesignWallTime: time.Minute, ImplementationWallTime: time.Minute, ValidationWallTime: 2 * time.Minute})
	if selection.RunDeterministic || selection.RunIndependentModel || selection.RunSpecialized || selection.Reason != "VALIDATION_TIME_CEILING" {
		t.Fatalf("ceiling admitted work=%+v", selection)
	}
}

func TestMeasureWorkflowTimingUsesWallClockUnionAndAggregateModelTime(t *testing.T) {
	base := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	interval := func(purpose WorkPurpose, start, finish time.Duration) WorkInvocation {
		startedAt := base.Add(start)
		finishedAt := base.Add(finish)
		return WorkInvocation{Purpose: purpose, StartedAt: &startedAt, FinishedAt: &finishedAt}
	}
	first := interval(PurposeImplementation, 0, 10*time.Second)
	claimedAt := base.Add(-2 * time.Second)
	first.ClaimedAt = &claimedAt
	invocations := []WorkInvocation{
		interval(PurposeHandoff, -20*time.Second, -10*time.Second),
		first,
		interval(PurposeImplementation, 2*time.Second, 8*time.Second),
		interval(PurposeValidation, 10*time.Second, 14*time.Second),
	}
	timing := MeasureWorkflowTiming(invocations, base.Add(20*time.Second))
	if timing.CriticalPathTime != 34*time.Second || timing.DesignWallTime != 10*time.Second || timing.ImplementationWallTime != 10*time.Second || timing.ValidationWallTime != 4*time.Second || timing.AggregateModelTime != 30*time.Second || timing.QueueTime != 2*time.Second || timing.PeakParallelism != 2 {
		t.Fatalf("timing=%+v", timing)
	}
}

func workflowDigest(value byte) Digest {
	raw := make([]byte, 64)
	for index := range raw {
		raw[index] = value
	}
	return Digest(raw)
}
