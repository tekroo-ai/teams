package operationalruntime

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestR55ValidatorReopenRetainsRawCounterexample(t *testing.T) {
	data, err := os.ReadFile("testdata/r55-validator-reopen-checkpoint.json")
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		State        kernel.AggregateState    `json:"state"`
		Task         organization.PlannedTask `json:"plan_task"`
		Reopened     kernel.DomainEvent       `json:"reopened_event"`
		Dependencies []struct {
			TaskID       kernel.UUIDv7 `json:"task_id"`
			OutputDigest kernel.Digest `json:"output_digest"`
			Debit        uint64        `json:"global_debit_ordinal"`
		} `json:"dependency_invocations"`
		Profile struct {
			Profile kernel.WorkProfileSnapshot `json:"profile"`
		} `json:"profile"`
		Receipts    []kernel.CommandReceipt `json:"receipts"`
		Authorities []struct {
			CommandID kernel.UUIDv7            `json:"command_id"`
			Authority kernel.AuthorityDecision `json:"authority"`
		} `json:"authorities"`
		Provenance []struct {
			CommandID    kernel.UUIDv7        `json:"command_id"`
			EvidenceRefs []kernel.EvidenceRef `json:"evidence_refs"`
		} `json:"provenance"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw.State.LifecycleEpoch != 2 || raw.Profile.Profile.Profile.LifecycleEpoch != 1 || raw.State.Phase != kernel.PhaseActive {
		t.Fatal("fixture no longer contains the interrupted epoch binding")
	}
	criteria, err := json.Marshal(raw.Task.AcceptanceCriteria)
	if err != nil {
		t.Fatal(err)
	}
	var conditions []kernel.Digest
	for _, dependencyID := range raw.Task.DependsOn {
		var latest kernel.Digest
		var debit uint64
		for _, dependency := range raw.Dependencies {
			if dependency.TaskID == dependencyID && dependency.Debit > debit {
				latest, debit = dependency.OutputDigest, dependency.Debit
			}
		}
		if !latest.Valid() {
			t.Fatal("missing raw dependency output", dependencyID)
		}
		conditions = append(conditions, latest)
	}
	condition, err := taskInvocationConditionDigest(raw.Profile.Profile.Profile.ProfileDigest, digestBytes(criteria), conditions)
	if err != nil {
		t.Fatal(err)
	}
	if !changedCandidateReopenCheckpointMatches(raw.Task, raw.State, raw.Profile.Profile.Profile, condition, raw.Reopened, kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "teams-admission-policy"}) {
		t.Fatal("repair would not recognize the actual r55 reopen checkpoint")
	}
	var bindingID kernel.UUIDv7
	for _, r := range raw.Receipts {
		if r.CommandType == "tekroo.command.task.bind-work-profile" {
			if r.ReasonCode != "UNAUTHORIZED" || r.StateChanged {
				t.Fatalf("original binding rejection changed: %+v", r)
			}
			bindingID = r.CommandID
		}
	}
	if !bindingID.Valid() {
		t.Fatal("missing original binding receipt")
	}
	authorized := false
	for _, a := range raw.Authorities {
		if a.CommandID == bindingID {
			authorized = a.Authority.Allowed
		}
	}
	if !authorized {
		t.Fatal("original authority was not allowed")
	}
	for _, p := range raw.Provenance {
		if p.CommandID != bindingID {
			continue
		}
		for _, ref := range p.EvidenceRefs {
			if ref.EvidenceID == "5e55a616-07b2-7498-88e0-33f8c4db6f78" {
				t.Fatal("fixture lost the missing historical evidence counterexample")
			}
		}
		if !containsEveryUUID(raw.Profile.Profile.Profile.ClassificationEvidenceIDs, []kernel.UUIDv7{"5e55a616-07b2-7498-88e0-33f8c4db6f78"}) {
			t.Fatal("historical profile does not name the missing evidence")
		}
		return
	}
	t.Fatal("missing original binding provenance")
}

func TestChangedCandidateReopenCheckpointRequiresExactLineage(t *testing.T) {
	tracked, _, _, _ := taskExecutionRefreshFixture(t)
	current := tracked.profile
	actor := tracked.plan.Owner
	validator := organization.PlannedTask{ID: current.TaskID, Owner: actor, Validates: []kernel.UUIDv7{candidateTestUUID(38001)}}
	state := kernel.AggregateState{Kind: kernel.AggregateTask, ID: current.TaskID, Revision: 12, LifecycleEpoch: current.LifecycleEpoch + 1, ScopeRevision: current.ScopeRevision + 1, Phase: kernel.PhaseActive, Condition: kernel.ConditionRunnable, Ownership: kernel.Ownership{OwnerFQN: &actor}}
	condition := repeatedDigest('6')
	authority := kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "teams-admission-policy"}
	execution := tracked.owner.Execution
	payload, _ := json.Marshal(map[string]any{"prior_epoch": current.LifecycleEpoch, "new_scope_revision": state.ScopeRevision, "owner_carry_forward": true, "reason": "A validated dependency produced a changed candidate; revalidate only this dependent task against the new immutable candidate.", "evidence_ids": current.ClassificationEvidenceIDs})
	key := "operator-task-recovery-reopen-" + string(validator.ID) + "-changed-candidate-" + string(condition) + ":execution:" + string(execution.ExecutionID) + ":2"
	event := kernel.DomainEvent{EventType: "tekroo.event.work.reopened", Aggregate: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: validator.ID}, AggregateRevision: state.Revision, LifecycleEpoch: state.LifecycleEpoch, Authority: authority, ActorFQN: &actor, Execution: &execution, Payload: payload, CommandID: deterministicOperationalUUID("standalone-command", "tekroo.command.work.reopen", string(validator.ID), key)}
	if !changedCandidateReopenCheckpointMatches(validator, state, current, condition, event, authority) {
		t.Fatal("exact changed-candidate checkpoint rejected")
	}
	for _, tc := range []struct {
		name   string
		mutate func(*kernel.AggregateState, *kernel.DomainEvent)
	}{
		{"unrelated command", func(_ *kernel.AggregateState, e *kernel.DomainEvent) { e.CommandID = candidateTestUUID(38002) }},
		{"intervening task event", func(s *kernel.AggregateState, _ *kernel.DomainEvent) { s.Revision++ }},
		{"multiple lifecycle gap", func(s *kernel.AggregateState, _ *kernel.DomainEvent) { s.LifecycleEpoch++ }},
		{"scope mismatch", func(s *kernel.AggregateState, _ *kernel.DomainEvent) { s.ScopeRevision++ }},
		{"different authority", func(_ *kernel.AggregateState, e *kernel.DomainEvent) {
			e.Authority = kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"}
		}},
		{"operator recovery", func(_ *kernel.AggregateState, e *kernel.DomainEvent) {
			e.Payload = json.RawMessage(`{"prior_epoch":1,"new_scope_revision":2,"owner_carry_forward":true,"reason":"operator repair","evidence_ids":["00000000-0000-7000-8000-000000000104"]}`)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, e := state, event
			tc.mutate(&s, &e)
			if changedCandidateReopenCheckpointMatches(validator, s, current, condition, e, authority) {
				t.Fatal("unrelated checkpoint accepted")
			}
		})
	}
	if changedCandidateReopenCheckpointMatches(validator, state, current, repeatedDigest('7'), event, authority) {
		t.Fatal("changed dependency condition reused old reopen")
	}
}

func TestChangedCandidateDeadlineMaintenanceDefersUnboundReopen(t *testing.T) {
	tracked, _, _, snapshot := taskExecutionRefreshFixture(t)
	now := taskExecutionRefreshAt()
	taskRef := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: tracked.plan.ID}
	profile := snapshot.WorkProfiles[taskRef]
	profile.Profile.Budgets.DeadlineAt = now.Add(-time.Minute)
	snapshot.WorkProfiles[taskRef] = profile
	state := *snapshot.State
	state.Phase = kernel.PhaseActive
	state.LifecycleEpoch++
	state.ScopeRevision++
	service := &ProductionService{clock: fixedClock{now: now}, requestTimeout: time.Minute}
	changed, err := service.reconcileFeatureProfileDeadlines(t.Context(), organization.FeatureRequest{}, organization.FeaturePlan{Tasks: []organization.PlannedTask{tracked.plan}}, map[kernel.UUIDv7]kernel.AggregateState{tracked.plan.ID: state}, nil, nil, snapshot, kernel.WorkBudgetAccount{DeadlineAt: now.Add(time.Hour)})
	if err != nil || changed {
		t.Fatalf("maintenance bypassed unfinished reopen: changed=%t err=%v", changed, err)
	}
	if snapshot.WorkProfiles[taskRef].Profile.Binding() != profile.Profile.Binding() {
		t.Fatal("maintenance mutated predecessor profile")
	}
}
