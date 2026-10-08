package organization_test

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func amendmentFixture(t *testing.T) (organization.FeatureRequest, organization.PlanAmendmentProposal, organization.PlanAmendmentWitness) {
	t.Helper()
	now := time.Date(2026, 10, 7, 22, 0, 0, 0, time.UTC)
	story := organization.PlannedStory{ID: featureUUID(10), Title: "Feature", Description: "Small accepted behavior", AcceptanceCriteria: []string{"works"}, Priority: organization.PriorityNormal}
	pm := activeRole("teams::project-manager-1", "project-manager", featureUUID(90), featureDigest('a'))
	task := func(id int, purpose kernel.WorkPurpose, owner kernel.ActorFQN, scope string) organization.PlannedTask {
		value := organization.PlannedTask{ID: featureUUID(id), StoryID: story.ID, Title: "Bounded work", Description: "Deliver accepted behavior", AcceptanceCriteria: []string{"verified"}, Owner: owner, ModelProfile: featureDigest('a'), DecisionRoute: kernel.RouteBoundedExecution, Purpose: purpose, Complexity: 2, Risk: organization.RiskLow, AttemptLimit: 2, ReviewRoundLimit: 1}
		if scope != "" {
			value.WriteScope = []string{scope}
		}
		return value
	}
	completed, completedTest := task(11, kernel.PurposeImplementation, "teams::coder-1", "service/provider.go"), task(12, kernel.PurposeValidation, "teams::tester-1", "")
	completedTest.DependsOn, completedTest.Validates = []kernel.UUIDv7{completed.ID}, []kernel.UUIDv7{completed.ID}
	blocked, blockedTest := task(13, kernel.PurposeImplementation, "teams::coder-2", "service/consumer.go"), task(14, kernel.PurposeValidation, "teams::tester-2", "")
	blocked.DependsOn = []kernel.UUIDv7{completed.ID}
	blockedTest.DependsOn, blockedTest.Validates = []kernel.UUIDv7{blocked.ID}, []kernel.UUIDv7{blocked.ID}
	plan := organization.FeaturePlan{Version: 1, PreparedBy: pm.ActorFQN, PreparedExecution: pm.Execution, Architecture: "Keep the accepted interface", SourceDesignDigest: featureDigest('b'), Stories: []organization.PlannedStory{story}, Tasks: []organization.PlannedTask{completed, completedTest, blocked, blockedTest}, Handoffs: []organization.PlannedHandoff{{ProviderTaskID: completed.ID, ConsumerTaskID: blocked.ID, Capability: "provider.lookup", Contract: "Existing interface"}}, CreatedAt: now}
	feature := organization.FeatureRequest{SchemaVersion: organization.FeatureSchemaVersion, ID: featureUUID(1), Revision: 6, SubmittedBy: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "paul"}, Input: featureInput(), Status: organization.FeaturePlanned, OperatorActor: "teams::operator-1", ProductOwnerActor: "teams::product-owner-1", InitialMessageID: featureUUID(2), LastMessageID: featureUUID(3), LastStepID: featureUUID(4), LastHop: 4, BudgetAccountID: featureUUID(5), LifecycleEpoch: 1, ScopeRevision: 1, CreatedAt: now.Add(-time.Hour), UpdatedAt: now, Plan: &plan,
		Specification: &organization.FeatureSpecification{PreparedBy: pm.ActorFQN, PreparedExecution: pm.Execution, Stories: []organization.PlannedStory{story}, PreparedAt: now.Add(-time.Minute)},
		Design:        &organization.FeatureDesignCandidate{PreparedBy: "teams::architect-1", PreparedExecution: kernel.ExecutionTuple{ExecutionID: featureUUID(91), FencingEpoch: 1}, OutputDigest: plan.SourceDesignDigest, PreparedAt: now.Add(-time.Minute)},
	}
	witness := organization.PlanAmendmentWitness{Tasks: map[kernel.UUIDv7]kernel.AggregateState{}, Invocations: map[kernel.AggregateRef]kernel.WorkInvocation{}, Evidence: map[kernel.UUIDv7]kernel.EvidenceMetadata{}, PM: pm.ActorFQN, PMExecution: pm.Execution, AllowedRoots: []string{"service", "cmd"}, Now: now.Add(time.Minute), ScopeChangeRequired: true}
	for _, item := range plan.Tasks {
		phase, condition := kernel.PhasePlanned, kernel.ConditionRunnable
		if item.ID == completed.ID || item.ID == completedTest.ID {
			phase = kernel.PhaseCompleted
		}
		if item.ID == blocked.ID {
			phase, condition = kernel.PhaseActive, kernel.ConditionBlocked
		}
		witness.Tasks[item.ID] = kernel.AggregateState{Kind: kernel.AggregateTask, ID: item.ID, Revision: 10, LifecycleEpoch: 1, ScopeRevision: 1, Phase: phase, Condition: condition}
	}
	output := featureDigest('c')
	invocation := kernel.WorkInvocation{ID: featureUUID(20), Revision: 4, TaskID: blocked.ID, BudgetAccountID: feature.BudgetAccountID, Purpose: kernel.PurposeImplementation, AttemptOrdinal: 1, State: kernel.InvocationSucceeded, OutputDigest: &output, TerminalEvidenceIDs: []kernel.UUIDv7{featureUUID(21)}}
	witness.Invocations[invocation.Ref()] = invocation
	witness.Evidence[featureUUID(21)] = kernel.EvidenceMetadata{Available: true, SHA256: output}
	answer := featureDigest('d')
	witness.Consultation = kernel.WorkInvocation{ID: featureUUID(22), Revision: 4, TaskID: featureUUID(23), BudgetAccountID: feature.BudgetAccountID, ActorFQN: feature.Design.PreparedBy, Purpose: kernel.PurposeHandoff, State: kernel.InvocationSucceeded, OutputDigest: &answer, TerminalEvidenceIDs: []kernel.UUIDv7{featureUUID(24)}}
	witness.Evidence[featureUUID(24)] = kernel.EvidenceMetadata{Available: true, SHA256: answer}
	limits, used := kernel.PurposeCounters{}, kernel.PurposeCounters{}
	for _, purpose := range kernel.AllWorkPurposes {
		limits[purpose], used[purpose] = 10, 0
	}
	witness.Budget = kernel.WorkBudgetAccount{ID: feature.BudgetAccountID, Revision: 8, RootWork: kernel.AggregateRef{Kind: kernel.AggregateStory, ID: story.ID}, LifecycleEpoch: 1, PolicyRevision: 1, PolicyDigest: featureDigest('e'), ModelInvocationLimit: 30, ModelInvocationsUsed: 5, PurposeLimits: limits, PurposeUsed: used, DeadlineAt: now.Add(time.Hour), LastEventID: featureUUID(25)}
	next := plan
	next.Version++
	next.CreatedAt = witness.Now
	newBlocked, newTest := blocked, blockedTest
	newBlocked.ID, newTest.ID = featureUUID(31), featureUUID(32)
	newBlocked.AttemptLimit = 1
	newBlocked.WriteScope = []string{"service/consumer.go", "cmd/main.go"}
	newTest.DependsOn, newTest.Validates = []kernel.UUIDv7{newBlocked.ID}, []kernel.UUIDv7{newBlocked.ID}
	next.Tasks = []organization.PlannedTask{completed, completedTest, newBlocked, newTest}
	next.Handoffs = []organization.PlannedHandoff{{ProviderTaskID: completed.ID, ConsumerTaskID: newBlocked.ID, Capability: "provider.lookup", Contract: "Existing interface"}}
	proposal := organization.PlanAmendmentProposal{SchemaVersion: organization.PlanAmendmentContractVersion, FeatureID: feature.ID, FeatureRevision: feature.Revision, PlanVersion: plan.Version, PlanDigest: organization.PlanAmendmentDigest(plan), SpecificationDigest: organization.PlanAmendmentDigest(*feature.Specification), BlockedTaskID: blocked.ID, BlockedTaskRevision: 10, BlockedInvocationID: invocation.ID, ConsultationID: witness.Consultation.TaskID, AnswerDigest: answer, Reason: "Cover compatibility wiring omitted from this assignment", SuccessorPlan: next,
		Replacements: []organization.PlanTaskReplacement{{PriorTaskID: blocked.ID, SuccessorTaskID: newBlocked.ID}, {PriorTaskID: blockedTest.ID, SuccessorTaskID: newTest.ID}},
		Impacts:      []organization.PlanAmendmentImpact{{Kind: "CALLER", Path: "service/consumer.go", OwnerTaskID: newBlocked.ID, Verification: "Compile and exercise consumer"}, {Kind: "WIRING", Path: "cmd/main.go", OwnerTaskID: newBlocked.ID, Verification: "Compile production construction"}},
	}
	if feature.Validate() != nil || !witness.Budget.Valid() {
		t.Fatal("invalid test fixture")
	}
	return feature, proposal, witness
}

func TestPlanAmendmentReservationRejectsUnplannedAndWrongPurposeSources(t *testing.T) {
	for _, kind := range []string{"unplanned", "wrong-purpose", "zero-attempt", "excess-attempt", "self-consultation"} {
		t.Run(kind, func(t *testing.T) {
			feature, proposal, witness := amendmentFixture(t)
			ref := kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: proposal.BlockedInvocationID}
			inv := witness.Invocations[ref]
			switch kind {
			case "unplanned":
				proposal.BlockedTaskID = featureUUID(99)
				state := witness.Tasks[inv.TaskID]
				state.ID, inv.TaskID = proposal.BlockedTaskID, proposal.BlockedTaskID
				witness.Tasks[state.ID] = state
			case "wrong-purpose":
				inv.Purpose = kernel.PurposeValidation
			case "zero-attempt":
				inv.AttemptOrdinal = 0
			case "excess-attempt":
				inv.AttemptOrdinal = 3
			case "self-consultation":
				proposal.ConsultationID = proposal.BlockedTaskID
				witness.Consultation.TaskID = proposal.BlockedTaskID
			}
			witness.Invocations[ref] = inv
			request := organization.PlanAmendmentReservation{AssessmentID: featureUUID(30), BlockedTaskID: proposal.BlockedTaskID, BlockedTaskRevision: proposal.BlockedTaskRevision, BlockedInvocationID: proposal.BlockedInvocationID, ConsultationID: proposal.ConsultationID, AnswerDigest: proposal.AnswerDigest}
			if _, err := organization.ReservePlanAmendment(feature, request, witness); err == nil {
				t.Fatal("invalid source consumed an assessment allowance")
			}
			if decision := organization.EvaluatePlanAmendment(feature, proposal, witness); decision.Accepted {
				t.Fatal("invalid source was approved")
			}
		})
	}
}

func TestPlanAmendmentSourceRepairUsesExistingFiniteAllowance(t *testing.T) {
	feature, proposal, witness := amendmentFixture(t)
	ref := kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: proposal.BlockedInvocationID}
	inv := witness.Invocations[ref]
	inv.Purpose = kernel.PurposeRepair
	witness.Invocations[ref] = inv
	request := organization.PlanAmendmentReservation{AssessmentID: featureUUID(30), BlockedTaskID: proposal.BlockedTaskID, BlockedTaskRevision: proposal.BlockedTaskRevision, BlockedInvocationID: proposal.BlockedInvocationID, ConsultationID: proposal.ConsultationID, AnswerDigest: proposal.AnswerDigest}
	if _, err := organization.ReservePlanAmendment(feature, request, witness); err != nil {
		t.Fatal("existing repair purpose cannot reserve", err)
	}
	inv.AttemptOrdinal++
	witness.Invocations[ref] = inv
	if _, err := organization.ReservePlanAmendment(feature, request, witness); err == nil {
		t.Fatal("repair ordinal exceeded existing allowance")
	}
}

func TestPlanAmendmentPreservesCompletedWorkAndRequiresIndependentPolicy(t *testing.T) {
	feature, proposal, witness := amendmentFixture(t)
	before := organization.PlanAmendmentDigest(feature)
	if decision := organization.EvaluatePlanAmendment(feature, proposal, witness); !decision.Accepted {
		t.Fatalf("valid amendment: %+v", decision)
	}
	if before != organization.PlanAmendmentDigest(feature) {
		t.Fatal("pure policy mutated original feature")
	}
	request := organization.PlanAmendmentReservation{AssessmentID: featureUUID(30), BlockedTaskID: proposal.BlockedTaskID, BlockedTaskRevision: proposal.BlockedTaskRevision, BlockedInvocationID: proposal.BlockedInvocationID, ConsultationID: proposal.ConsultationID, AnswerDigest: proposal.AnswerDigest}
	reserved, err := organization.ReservePlanAmendment(feature, request, witness)
	if err != nil || !organization.ValidPlanAmendmentTransition(feature, reserved) {
		t.Fatalf("reservation: %v", err)
	}
	again, err := organization.ReservePlanAmendment(reserved, request, witness)
	if err != nil || again.Revision != reserved.Revision {
		t.Fatal("reservation replay consumed another allowance")
	}
	request.AssessmentID = featureUUID(33)
	if _, err := organization.ReservePlanAmendment(reserved, request, witness); err == nil {
		t.Fatal("second assessment admitted")
	}
	policy := organization.PlanAmendmentPolicy{ContractVersion: organization.PlanAmendmentContractVersion, ManifestDigest: featureDigest('f'), Authority: kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "bounded-amendment-policy"}, AllowedRoots: witness.AllowedRoots}
	raw, _ := json.Marshal(proposal)
	if _, err := organization.DecodePlanAmendmentProposal(raw); err != nil {
		t.Fatalf("schema rejected valid proposal: %v", err)
	}
	if _, err := organization.RecordPlanAmendmentDecision(reserved, raw, witness, policy, kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: string(proposal.SuccessorPlan.PreparedBy)}); err == nil {
		t.Fatal("PM approved its own proposal")
	}
	decided, err := organization.RecordPlanAmendmentDecision(reserved, raw, witness, policy, policy.Authority)
	if err != nil || decided.PlanAmendment.State != organization.AmendmentApproved || !organization.ValidPlanAmendmentTransition(reserved, decided) {
		t.Fatalf("decision: %v %+v", err, decided.PlanAmendment)
	}
	if !reflect.DeepEqual(decided.Plan, feature.Plan) || decided.BudgetAccountID != feature.BudgetAccountID || decided.PlanAmendment.DeadlineAt != witness.Budget.DeadlineAt {
		t.Fatal("approval published plan or rebased budget")
	}
	if again, err := organization.RecordPlanAmendmentDecision(decided, raw, witness, policy, policy.Authority); err != nil || again.Revision != decided.Revision {
		t.Fatal("decision replay appended another revision")
	}
	witness.Now = witness.Now.Add(time.Second)
	if d := organization.RevalidatePlanAmendment(decided, witness, policy); !d.Accepted {
		t.Fatalf("approved revalidation: %+v", d)
	}
	published, err := organization.PublishPlanAmendment(decided, witness, policy)
	if err != nil || published.PlanAmendment.State != organization.AmendmentApplied || !organization.ValidPlanAmendmentTransition(decided, published) {
		t.Fatal("publication", err)
	}
	if !reflect.DeepEqual(published.Plan, &proposal.SuccessorPlan) || !reflect.DeepEqual(published.Specification, feature.Specification) || published.BudgetAccountID != feature.BudgetAccountID || published.LastHop != reserved.LastHop {
		t.Fatal("publication changed authority/budget or requirements")
	}
	for i := 0; i < 2; i++ {
		if !reflect.DeepEqual(published.Plan.Tasks[i], feature.Plan.Tasks[i]) {
			t.Fatal("completed task changed")
		}
	}
	if _, err := organization.PublishPlanAmendment(published, witness, policy); err == nil {
		t.Fatal("published twice")
	}
	state := witness.Tasks[proposal.BlockedTaskID]
	state.Revision++
	witness.Tasks[state.ID] = state
	if _, err := organization.PublishPlanAmendment(decided, witness, policy); err == nil {
		t.Fatal("stale blocked revision published")
	}
	state.Revision--
	witness.Tasks[state.ID] = state
	witness.Budget.ModelInvocationsUsed = witness.Budget.ModelInvocationLimit
	if _, err := organization.PublishPlanAmendment(decided, witness, policy); err == nil {
		t.Fatal("exhausted budget published")
	}
}

func TestPlanAmendmentRejectsUnsafePlans(t *testing.T) {
	cases := []struct {
		name   string
		change func(*organization.FeatureRequest, *organization.PlanAmendmentProposal, *organization.PlanAmendmentWitness)
		code   string
	}{
		{"stale digest", func(f *organization.FeatureRequest, p *organization.PlanAmendmentProposal, w *organization.PlanAmendmentWitness) {
			p.PlanDigest = featureDigest('1')
		}, "STALE_SOURCE"},
		{"stale blocked revision", func(f *organization.FeatureRequest, p *organization.PlanAmendmentProposal, w *organization.PlanAmendmentWitness) {
			p.BlockedTaskRevision++
		}, "UNBOUND_QUESTION"},
		{"in scope answer", func(f *organization.FeatureRequest, p *organization.PlanAmendmentProposal, w *organization.PlanAmendmentWitness) {
			w.ScopeChangeRequired = false
		}, "UNBOUND_QUESTION"},
		{"missing evidence", func(f *organization.FeatureRequest, p *organization.PlanAmendmentProposal, w *organization.PlanAmendmentWitness) {
			delete(w.Evidence, featureUUID(24))
		}, "EVIDENCE_UNAVAILABLE"},
		{"changed architecture", func(f *organization.FeatureRequest, p *organization.PlanAmendmentProposal, w *organization.PlanAmendmentWitness) {
			p.SuccessorPlan.Architecture += " plus new behavior"
		}, "PRODUCT_OR_DESIGN_CHANGED"},
		{"rewritten completed node", func(f *organization.FeatureRequest, p *organization.PlanAmendmentProposal, w *organization.PlanAmendmentWitness) {
			p.SuccessorPlan.Tasks[0].Description += " changed"
		}, "TASK_REWRITTEN"},
		{"whole repo scope", func(f *organization.FeatureRequest, p *organization.PlanAmendmentProposal, w *organization.PlanAmendmentWitness) {
			p.SuccessorPlan.Tasks[2].WriteScope = []string{"."}
		}, "WRITE_SCOPE_DENIED"},
		{"escape scope", func(f *organization.FeatureRequest, p *organization.PlanAmendmentProposal, w *organization.PlanAmendmentWitness) {
			p.SuccessorPlan.Tasks[2].WriteScope = []string{"../secret"}
		}, "WRITE_SCOPE_DENIED"},
		{"runtime hook scope", func(f *organization.FeatureRequest, p *organization.PlanAmendmentProposal, w *organization.PlanAmendmentWitness) {
			p.SuccessorPlan.Tasks[2].WriteScope = []string{"service/.openhands/hook.py"}
		}, "WRITE_SCOPE_DENIED"},
		{"outside policy", func(f *organization.FeatureRequest, p *organization.PlanAmendmentProposal, w *organization.PlanAmendmentWitness) {
			w.AllowedRoots = []string{"service"}
		}, "WRITE_SCOPE_DENIED"},
		{"missing impact", func(f *organization.FeatureRequest, p *organization.PlanAmendmentProposal, w *organization.PlanAmendmentWitness) {
			p.Impacts = p.Impacts[:1]
		}, "IMPACT_COVERAGE_MISSING"},
		{"minted task capacity", func(f *organization.FeatureRequest, p *organization.PlanAmendmentProposal, w *organization.PlanAmendmentWitness) {
			f.Input.MaximumTasks = 4
		}, "BUDGET_EXHAUSTED"},
		{"global budget", func(f *organization.FeatureRequest, p *organization.PlanAmendmentProposal, w *organization.PlanAmendmentWitness) {
			w.Budget.ModelInvocationsUsed = w.Budget.ModelInvocationLimit - 1
		}, "BUDGET_EXHAUSTED"},
		{"purpose budget", func(f *organization.FeatureRequest, p *organization.PlanAmendmentProposal, w *organization.PlanAmendmentWitness) {
			w.Budget.PurposeUsed[kernel.PurposeValidation] = w.Budget.PurposeLimits[kernel.PurposeValidation]
		}, "BUDGET_EXHAUSTED"},
		{"deadline", func(f *organization.FeatureRequest, p *organization.PlanAmendmentProposal, w *organization.PlanAmendmentWitness) {
			w.Now = w.Budget.DeadlineAt
		}, "BUDGET_EXHAUSTED"},
		{"hop exhaustion", func(f *organization.FeatureRequest, p *organization.PlanAmendmentProposal, w *organization.PlanAmendmentWitness) {
			f.LastHop = f.Input.MaximumHops - 1
		}, "BUDGET_EXHAUSTED"},
		{"unfinished invocation", func(f *organization.FeatureRequest, p *organization.PlanAmendmentProposal, w *organization.PlanAmendmentWitness) {
			inv := w.Invocations[kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: p.BlockedInvocationID}]
			inv.State = kernel.InvocationStarted
			w.Invocations[inv.Ref()] = inv
		}, "UNBOUND_QUESTION"},
		{"replacement resets attempts", func(f *organization.FeatureRequest, p *organization.PlanAmendmentProposal, w *organization.PlanAmendmentWitness) {
			p.SuccessorPlan.Tasks[2].AttemptLimit = 2
		}, "ATTEMPT_ALLOWANCE_EXHAUSTED"},
		{"exhausted predecessor", func(f *organization.FeatureRequest, p *organization.PlanAmendmentProposal, w *organization.PlanAmendmentWitness) {
			ref := kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: p.BlockedInvocationID}
			inv := w.Invocations[ref]
			inv.AttemptOrdinal = 2
			w.Invocations[ref] = inv
		}, "ATTEMPT_ALLOWANCE_EXHAUSTED"},
		{"foreign team", func(f *organization.FeatureRequest, p *organization.PlanAmendmentProposal, w *organization.PlanAmendmentWitness) {
			p.SuccessorPlan.Tasks[2].Owner = "other::coder-1"
		}, "OWNER_OUTSIDE_TEAM"},
		{"completed downstream replaced", func(f *organization.FeatureRequest, p *organization.PlanAmendmentProposal, w *organization.PlanAmendmentWitness) {
			state := w.Tasks[featureUUID(14)]
			state.Phase = kernel.PhaseCompleted
			w.Tasks[state.ID] = state
		}, "COMPLETED_WORK_CHANGED"},
		{"overlapping live writer", func(f *organization.FeatureRequest, p *organization.PlanAmendmentProposal, w *organization.PlanAmendmentWitness) {
			p.SuccessorPlan.Tasks[2].WriteScope = append(p.SuccessorPlan.Tasks[2].WriteScope, "service/provider.go")
			p.Impacts = append(p.Impacts, organization.PlanAmendmentImpact{Kind: "PRODUCTION_IMPLEMENTER", Path: "service/provider.go", OwnerTaskID: featureUUID(31), Verification: "compile"})
			inv := kernel.WorkInvocation{ID: featureUUID(80), TaskID: featureUUID(11), State: kernel.InvocationStarted}
			w.Invocations[inv.Ref()] = inv
		}, "ACTIVE_WRITER_OVERLAP"},
		{"previous dependency removed", func(f *organization.FeatureRequest, p *organization.PlanAmendmentProposal, w *organization.PlanAmendmentWitness) {
			p.SuccessorPlan.Tasks[2].DependsOn = nil
			p.SuccessorPlan.Handoffs = nil
		}, "DEPENDENCY_DROPPED"},
		{"lost handoff contract", func(f *organization.FeatureRequest, p *organization.PlanAmendmentProposal, w *organization.PlanAmendmentWitness) {
			p.SuccessorPlan.Handoffs[0].Contract += " new signature"
		}, "RETAINED_HANDOFF_CHANGED"},
		{"cycle", func(f *organization.FeatureRequest, p *organization.PlanAmendmentProposal, w *organization.PlanAmendmentWitness) {
			p.SuccessorPlan.Tasks[2].DependsOn = append(p.SuccessorPlan.Tasks[2].DependsOn, p.SuccessorPlan.Tasks[3].ID)
		}, "INVALID_SUCCESSOR_PLAN"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			feature, proposal, witness := amendmentFixture(t)
			// Break fixture aliases so adversarial mutation cannot change the source.
			raw, _ := json.Marshal(proposal)
			_ = json.Unmarshal(raw, &proposal)
			item.change(&feature, &proposal, &witness)
			decision := organization.EvaluatePlanAmendment(feature, proposal, witness)
			if decision.Accepted || decision.Code != item.code {
				t.Fatalf("got %+v, want %s", decision, item.code)
			}
		})
	}
}

func TestPlanAmendmentGeneratedSchemaMatchesRuntime(t *testing.T) {
	raw, err := os.ReadFile("../CONTRACTS/tekroo.kernel.contracts/0.15.0/schemas/feature-plan-amendment-proposal.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var packaged, runtime any
	if json.Unmarshal(raw, &packaged) != nil || json.Unmarshal(organization.PlanAmendmentProposalSchema(), &runtime) != nil || !reflect.DeepEqual(packaged, runtime) {
		t.Fatal("packaged model guidance and runtime schema diverged")
	}
}

func TestPlanAmendmentSchemaRejectsEncodedObjectsAndAmbiguousFields(t *testing.T) {
	_, proposal, _ := amendmentFixture(t)
	raw, _ := json.Marshal(proposal)
	var value map[string]any
	_ = json.Unmarshal(raw, &value)
	tests := []struct {
		name   string
		change func(map[string]any)
	}{
		{"encoded plan", func(v map[string]any) {
			encoded, _ := json.Marshal(v["successor_plan"])
			v["successor_plan"] = string(encoded)
		}},
		{"unknown field", func(v map[string]any) { v["free_scope_grant"] = true }},
		{"missing version", func(v map[string]any) { delete(v, "schema_version") }},
		{"risk elaboration", func(v map[string]any) {
			v["successor_plan"].(map[string]any)["tasks"].([]any)[2].(map[string]any)["risk"] = "LOW because it is safe"
		}},
		{"attempt overflow", func(v map[string]any) {
			v["successor_plan"].(map[string]any)["tasks"].([]any)[2].(map[string]any)["attempt_limit"] = 100
		}},
		{"wrong path key", func(v map[string]any) {
			impact := v["impacts"].([]any)[0].(map[string]any)
			impact["file_path"] = impact["path"]
			delete(impact, "path")
		}},
	}
	for _, item := range tests {
		t.Run(item.name, func(t *testing.T) {
			var copy map[string]any
			_ = json.Unmarshal(raw, &copy)
			item.change(copy)
			invalid, _ := json.Marshal(copy)
			if _, err := organization.DecodePlanAmendmentProposal(invalid); err == nil {
				t.Fatal("invalid schema passed")
			}
		})
	}
	encoded, _ := json.Marshal(string(raw))
	for _, invalid := range [][]byte{encoded, append(append([]byte(nil), raw...), []byte(" {}")...), []byte(strings.Replace(string(raw), `"answer_digest"`, `"answer_digest_typo"`, 1))} {
		if _, err := organization.DecodePlanAmendmentProposal(invalid); err == nil {
			t.Fatal("invalid JSON boundary accepted")
		}
	}
}
