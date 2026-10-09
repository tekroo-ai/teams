package operationalruntime

import (
	"context"
	"errors"
	"testing"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

type planningLineageFixture struct {
	states      map[kernel.AggregateRef]kernel.AggregateState
	invocations map[kernel.AggregateRef]kernel.WorkInvocation
}

func (f planningLineageFixture) ReadAggregateHead(_ context.Context, ref kernel.AggregateRef) (kernel.AggregateState, kernel.UUIDv7, bool, error) {
	state, found := f.states[ref]
	return state, ref.ID, found, nil
}
func (f planningLineageFixture) LoadDecision(_ context.Context, _ kernel.KernelCommand) (kernel.Snapshot, error) {
	return kernel.Snapshot{WorkInvocations: f.invocations}, nil
}

func TestPMRevisionResolvesTheActuallyReviewedRound(t *testing.T) {
	for _, sourceRound := range []uint32{0, 1, 2, architecturePlanRecordedRoundLimit - 1} {
		feature := organization.FeatureRequest{ID: deterministicOperationalUUID("revision-regression", "feature"), DesignRevision: &organization.FeatureDesignRevision{PriorDesignDigest: repeatedDigest('a'), DecisionDigest: repeatedDigest('b')}}
		fixture := planningLineageFixture{states: map[kernel.AggregateRef]kernel.AggregateState{}, invocations: map[kernel.AggregateRef]kernel.WorkInvocation{}}
		for round := uint32(0); round <= sourceRound; round++ {
			ref := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: featurePlanningTaskID(feature.ID, stageArchitecture, round, nil)}
			fixture.states[ref] = kernel.AggregateState{Phase: kernel.PhaseCompleted}
		}
		architectureID := featurePlanningTaskID(feature.ID, stageArchitecture, sourceRound, nil)
		pmID := featurePlanningTaskID(feature.ID, stagePlanFinalization, sourceRound, nil)
		architecture := workflowTestInvocation("source-design")
		architecture.TaskID, architecture.State, architecture.OutputDigest = architectureID, kernel.InvocationSucceeded, &feature.DesignRevision.PriorDesignDigest
		pm := workflowTestInvocation("source-pm-decision")
		pm.TaskID, pm.State, pm.OutputDigest = pmID, kernel.InvocationSucceeded, &feature.DesignRevision.DecisionDigest
		fixture.states[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: pmID}] = kernel.AggregateState{Phase: kernel.PhaseCompleted}
		fixture.invocations[architecture.Ref()], fixture.invocations[pm.Ref()] = architecture, pm
		got, err := designRevisionSourceRound(t.Context(), fixture, feature)
		if err != nil || got != sourceRound {
			t.Fatalf("reviewed round %d resolved as %d: %v", sourceRound, got, err)
		}
		// A completed successor must not consume a second revision allowance or
		// move the original decision's provenance to the latest design round.
		fixture.states[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: featurePlanningTaskID(feature.ID, stageArchitecture, sourceRound+1, nil)}] = kernel.AggregateState{Phase: kernel.PhaseCompleted}
		got, err = designRevisionSourceRound(t.Context(), fixture, feature)
		if err != nil || got != sourceRound {
			t.Fatal("successor replaced the source decision", err)
		}
		bad := feature
		copy := *feature.DesignRevision
		copy.PriorDesignDigest = repeatedDigest('c')
		bad.DesignRevision = &copy
		if _, err := designRevisionSourceRound(t.Context(), fixture, bad); !errors.Is(err, organization.ErrInvalidFeature) {
			t.Fatal("mismatched accepted design digest admitted", err)
		}
		fixture.states[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: pmID}] = kernel.AggregateState{Phase: kernel.PhaseActive}
		if _, err := designRevisionSourceRound(t.Context(), fixture, feature); !errors.Is(err, organization.ErrInvalidFeature) {
			t.Fatal("unfinished PM decision admitted", err)
		}
	}
}
