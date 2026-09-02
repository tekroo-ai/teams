package operationalruntime

import (
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestImplementationChainDependencyReadyBeforeSharedValidation(t *testing.T) {
	owner := kernel.ActorFQN("teams::senior-coder-1")
	upstream := organization.PlannedTask{Purpose: kernel.PurposeImplementation, Owner: owner}
	downstream := organization.PlannedTask{Purpose: kernel.PurposeImplementation, Owner: owner}
	digest := kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	invocation := kernel.WorkInvocation{State: kernel.InvocationSucceeded, OutputDigest: &digest}

	if !implementationChainDependencyReady(downstream, upstream, invocation, true) {
		t.Fatal("successful same-owner implementation dependency must allow the next implementation stage to run before shared validation")
	}

	otherOwner := downstream
	otherOwner.Owner = kernel.ActorFQN("teams::senior-coder-2")
	failed := invocation
	failed.State = kernel.InvocationFailed
	missingOutput := invocation
	missingOutput.OutputDigest = nil
	validation := downstream
	validation.Purpose = kernel.PurposeValidation

	for name, candidate := range map[string]bool{
		"missing invocation": implementationChainDependencyReady(downstream, upstream, invocation, false),
		"different owner":    implementationChainDependencyReady(otherOwner, upstream, invocation, true),
		"failed invocation":  implementationChainDependencyReady(downstream, upstream, failed, true),
		"missing output":     implementationChainDependencyReady(downstream, upstream, missingOutput, true),
		"non-implementation": implementationChainDependencyReady(validation, upstream, invocation, true),
	} {
		if candidate {
			t.Fatalf("%s must retain the completed-phase dependency requirement", name)
		}
	}
}

func TestFeatureDeadlineExtensionEvidenceIDsUsesAcceptedSuccessorProfile(t *testing.T) {
	_, _, _, snapshot := taskExecutionRefreshFixture(t)
	var original kernel.WorkProfileSnapshot
	for _, profile := range snapshot.WorkProfiles {
		original = profile
		break
	}
	deadline := time.Date(2026, 9, 2, 3, 0, 0, 0, time.UTC)
	firstTask := original.Profile.TaskID
	secondTask := kernel.UUIDv7("00000000-0000-7000-8000-000000000201")
	recoveryEvidence := kernel.UUIDv7("00000000-0000-7000-8000-000000000202")
	priorProfileID := original.Profile.ProfileID
	recovered := original.Clone()
	recovered.Profile.ProfileID = "00000000-0000-7000-8000-000000000203"
	recovered.Profile.ProfileRevision++
	recovered.Profile.ProfileDigest = repeatedDigest('d')
	recovered.Profile.Budgets.DeadlineAt = deadline
	recovered.Profile.ClassificationEvidenceIDs = append(recovered.Profile.ClassificationEvidenceIDs, recoveryEvidence)
	recovered.Profile.SupersedesProfileID = &priorProfileID
	recovered.BoundEventID = "00000000-0000-7000-8000-000000000204"
	snapshot.WorkProfiles = map[kernel.AggregateRef]kernel.WorkProfileSnapshot{
		{Kind: kernel.AggregateTask, ID: firstTask}: recovered,
	}
	plan := organization.FeaturePlan{Tasks: []organization.PlannedTask{{ID: firstTask}, {ID: secondTask}}}

	evidenceIDs := featureDeadlineExtensionEvidenceIDs(plan, snapshot, deadline)
	if !containsEveryUUID(evidenceIDs, []kernel.UUIDv7{original.Profile.ClassificationEvidenceIDs[0], recoveryEvidence}) {
		t.Fatalf("deadline evidence = %v, want original and recovery evidence", evidenceIDs)
	}

	if evidence := featureDeadlineExtensionEvidenceIDs(plan, snapshot, deadline.Add(time.Minute)); len(evidence) != 0 {
		t.Fatalf("mismatched deadline evidence = %v, want none", evidence)
	}
}
