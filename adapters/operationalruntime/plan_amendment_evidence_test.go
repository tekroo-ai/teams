package operationalruntime

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestAmendmentSuccessorEvidencePreservesAndBindsDecisionTrail(t *testing.T) {
	id := func(value int) kernel.UUIDv7 {
		return kernel.UUIDv7(fmt.Sprintf("00000000-0000-7000-8000-%012d", value))
	}
	digestByte := func(value byte) kernel.Digest { return kernel.Digest(strings.Repeat(string(value), 64)) }
	retained := organization.PlannedTask{ID: id(42100)}
	fresh := organization.PlannedTask{ID: id(42101)}
	input := kernel.EvidenceRef{EvidenceID: id(42102), SHA256: digestByte('a')}
	feature := organization.FeatureRequest{PlanAmendment: &organization.FeaturePlanAmendment{
		State: organization.AmendmentApplied, PriorPlan: organization.FeaturePlan{Tasks: []organization.PlannedTask{retained}},
		BlockedInvocationID: id(42103), ConsultationInvocationID: id(42104), AssessmentInvocationID: id(42105), AssessmentInput: &input,
	}}
	snapshot := kernel.Snapshot{WorkInvocations: map[kernel.AggregateRef]kernel.WorkInvocation{}, Evidence: map[kernel.UUIDv7]kernel.EvidenceMetadata{input.EvidenceID: {Available: true, SHA256: input.SHA256}}}
	var refs []kernel.EvidenceRef
	for i, invID := range []kernel.UUIDv7{feature.PlanAmendment.BlockedInvocationID, feature.PlanAmendment.ConsultationInvocationID, feature.PlanAmendment.AssessmentInvocationID} {
		ref := kernel.EvidenceRef{EvidenceID: id(42110 + i), SHA256: digestByte(byte('b' + i))}
		refs = append(refs, ref)
		digest := ref.SHA256
		inv := kernel.WorkInvocation{ID: invID, State: kernel.InvocationSucceeded, OutputDigest: &digest, TerminalEvidenceIDs: []kernel.UUIDv7{ref.EvidenceID}}
		snapshot.WorkInvocations[inv.Ref()] = inv
		snapshot.Evidence[ref.EvidenceID] = kernel.EvidenceMetadata{Available: true, SHA256: ref.SHA256}
	}
	base := []kernel.EvidenceRef{refs[0]}
	got, err := amendmentSuccessorEvidence(feature, fresh, snapshot, base)
	if err != nil || len(got) != 4 || !reflect.DeepEqual(base, []kernel.EvidenceRef{refs[0]}) {
		t.Fatalf("trail missing, duplicated or changed base: %+v %v", got, err)
	}
	got, err = amendmentSuccessorEvidence(feature, retained, snapshot, base)
	if err != nil || !reflect.DeepEqual(got, base) {
		t.Fatal("retained evidence changed", err)
	}
	for _, mutate := range []func(){
		func() {
			snapshot.Evidence[input.EvidenceID] = kernel.EvidenceMetadata{Available: false, SHA256: input.SHA256}
		},
		func() {
			snapshot.Evidence[input.EvidenceID] = kernel.EvidenceMetadata{Available: true, SHA256: digestByte('f')}
		},
		func() {
			snapshot.Evidence[input.EvidenceID] = kernel.EvidenceMetadata{Available: true, SHA256: input.SHA256}
			delete(snapshot.WorkInvocations, kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: feature.PlanAmendment.AssessmentInvocationID})
		},
	} {
		mutate()
		if _, err := amendmentSuccessorEvidence(feature, fresh, snapshot, base); err == nil {
			t.Fatal("unavailable or changed amendment evidence accepted")
		}
	}
}
