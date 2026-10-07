package mongo

import (
	"github.com/tekroo-ai/teams/kernel"
	"testing"
)

func TestFirstRepairFindsOnlyPriorSameTaskImplementationDirective(t *testing.T) {
	id := kernel.UUIDv7("00000000-0000-7000-8000-000000000341")
	next := kernel.WorkInvocation{TaskID: id, Purpose: kernel.PurposeRepair, AdmissionPolicyRevision: 5}
	a := kernel.WorkInvocation{ID: "00000000-0000-7000-8000-000000000342", TaskID: id, Purpose: kernel.PurposeImplementation, State: kernel.InvocationSucceeded, AdmissionPolicyRevision: 3, AttemptOrdinal: 1}
	b := a
	b.ID = "00000000-0000-7000-8000-000000000343"
	b.AttemptOrdinal = 2
	unrelated := a
	unrelated.ID = "00000000-0000-7000-8000-000000000344"
	unrelated.TaskID = unrelated.ID
	unrelated.AdmissionPolicyRevision = 4
	future := a
	future.ID = "00000000-0000-7000-8000-000000000345"
	future.AdmissionPolicyRevision = 6
	got, found := repairDirectivePredecessor(next, map[kernel.AggregateRef]kernel.WorkInvocation{a.Ref(): a, b.Ref(): b, unrelated.Ref(): unrelated, future.Ref(): future})
	if !found || got.ID != b.ID {
		t.Fatalf("wrong recovery directive predecessor: %+v", got)
	}
}
