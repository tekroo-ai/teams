package operationalruntime

import (
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestPlannedDependencyReadinessExposesUnacceptedCandidateOnlyToValidator(t *testing.T) {
	digest := kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	invocation := kernel.WorkInvocation{State: kernel.InvocationSucceeded, OutputDigest: &digest}
	active := kernel.AggregateState{Phase: kernel.PhaseActive}
	completed := kernel.AggregateState{Phase: kernel.PhaseCompleted}

	if !plannedDependencyReady(true, active, invocation, true) {
		t.Fatal("explicit validator could not inspect a successful candidate")
	}
	if plannedDependencyReady(false, active, invocation, true) {
		t.Fatal("ordinary downstream work received an unaccepted candidate")
	}
	if !plannedDependencyReady(false, completed, kernel.WorkInvocation{}, false) {
		t.Fatal("completed dependency did not release ordinary downstream work")
	}
}

func TestActorHasActiveInvocationEnforcesOneWorkItemPerCurrentExecution(t *testing.T) {
	actor := kernel.ActorFQN("teams::coder-1")
	other := kernel.ActorFQN("teams::coder-2")
	current := kernel.ExecutionTuple{ExecutionID: candidateTestUUID(1098), FencingEpoch: 2}
	stale := kernel.ExecutionTuple{ExecutionID: candidateTestUUID(1099), FencingEpoch: 1}
	invocations := map[kernel.AggregateRef]kernel.WorkInvocation{
		{Kind: kernel.AggregateWorkInvocation, ID: candidateTestUUID(1101)}: {ActorFQN: actor, Execution: current, State: kernel.InvocationSucceeded},
		{Kind: kernel.AggregateWorkInvocation, ID: candidateTestUUID(1102)}: {ActorFQN: other, Execution: current, State: kernel.InvocationStarted},
	}
	if actorHasActiveInvocation(invocations, actor, current) {
		t.Fatal("terminal invocation kept its actor occupied")
	}
	invocations[kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: candidateTestUUID(1103)}] = kernel.WorkInvocation{ActorFQN: actor, Execution: stale, State: kernel.InvocationAuthorized}
	if actorHasActiveInvocation(invocations, actor, current) {
		t.Fatal("invocation fenced by a prior execution kept the restarted actor occupied")
	}
	invocations[kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: candidateTestUUID(1104)}] = kernel.WorkInvocation{ActorFQN: actor, Execution: current, State: kernel.InvocationAuthorized}
	if !actorHasActiveInvocation(invocations, actor, current) {
		t.Fatal("nonterminal invocation did not occupy its actor")
	}
}

func TestRoleNeedsStartDistinguishesStoppedFromPaused(t *testing.T) {
	for _, test := range []struct {
		name   string
		found  bool
		status organization.RoleStatus
		want   bool
	}{
		{name: "absent", found: false, want: true},
		{name: "stopped", found: true, status: organization.RoleStopped, want: true},
		{name: "failed", found: true, status: organization.RoleFailed, want: true},
		{name: "idle", found: true, status: organization.RoleIdle, want: false},
		{name: "operator paused", found: true, status: organization.RolePaused, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := roleNeedsStart(test.found, test.status); got != test.want {
				t.Fatalf("roleNeedsStart(%v, %s) = %v, want %v", test.found, test.status, got, test.want)
			}
		})
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

	service := &ProductionService{}
	evidenceIDs, err := service.featureDeadlineExtensionEvidenceIDs(t.Context(), plan, snapshot, kernel.WorkBudgetAccount{DeadlineAt: deadline})
	if err != nil {
		t.Fatal(err)
	}
	if !containsEveryUUID(evidenceIDs, []kernel.UUIDv7{original.Profile.ClassificationEvidenceIDs[0], recoveryEvidence}) {
		t.Fatalf("deadline evidence = %v, want original and recovery evidence", evidenceIDs)
	}

	if evidence := profileDeadlineExtensionEvidenceIDs(plan, snapshot, deadline.Add(time.Minute)); len(evidence) != 0 {
		t.Fatalf("mismatched deadline evidence = %v, want none", evidence)
	}
}

func TestTaskReviewAcceptsDistinctVerifiedCandidateArtifacts(t *testing.T) {
	first := repeatedDigest('a')
	second := repeatedDigest('b')
	if !candidateArtifactsValid([]kernel.Digest{first, second}) {
		t.Fatal("valid task-local and assembled validator candidates were rejected because their diffs differ")
	}
	if candidateArtifactsValid([]kernel.Digest{first, ""}) {
		t.Fatal("invalid validator candidate artifact was accepted")
	}
}

func TestDurableWorkspaceIdentityDigestSurvivesResolverRestart(t *testing.T) {
	scope := kernel.TaskOperationalScope{
		WorkspaceID: "coder-1", WorktreeID: "task-00000000-0000-7000-8000-000000000301",
		Branch: "tekroo/task/feature/task", BaselineSHA: "1111111111111111111111111111111111111111",
		WritablePaths: []string{"."},
	}
	first, err := durableWorkspaceIdentityDigest(scope)
	if err != nil || !first.Valid() {
		t.Fatalf("durable workspace identity digest = %q, err = %v", first, err)
	}
	second, err := durableWorkspaceIdentityDigest(scope)
	if err != nil || second != first {
		t.Fatalf("workspace identity changed across reconstruction: first=%q second=%q err=%v", first, second, err)
	}
}
