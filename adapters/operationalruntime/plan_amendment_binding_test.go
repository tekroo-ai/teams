package operationalruntime

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func boundAmendmentFixture() (organization.FeatureRequest, application.ExecutionBrief, organization.PlanAmendmentProposal) {
	id := candidateTestUUID
	sha := kernel.Digest(strings.Repeat("a", 64))
	execution := kernel.ExecutionTuple{ExecutionID: id(61001), FencingEpoch: 2}
	now := time.Date(2026, 10, 10, 1, 23, 53, 0, time.UTC)
	story := organization.PlannedStory{ID: id(61002), Title: "Unchanged story", Description: "Accepted behavior", AcceptanceCriteria: []string{"works"}, Priority: organization.PriorityNormal}
	task := organization.PlannedTask{ID: id(61003), StoryID: story.ID, Title: "Repair caller", Description: "Preserve completed provider", AcceptanceCriteria: []string{"works"}, Owner: "teams::coder-1", ModelProfile: sha, DecisionRoute: kernel.RouteBoundedExecution, Purpose: kernel.PurposeImplementation, Complexity: 2, Risk: organization.RiskLow, AttemptLimit: 1, ReviewRoundLimit: 1, WriteScope: []string{"service/caller.go"}}
	a := &organization.FeaturePlanAmendment{AssessmentID: id(61004), FeatureRevision: 5, BlockedTaskID: id(61005), BlockedTaskRevision: 9, BlockedInvocationID: id(61006), ConsultationID: id(61007), AnswerDigest: sha, PriorDigest: sha, SpecificationDigest: sha, RequestedAt: now,
		PriorPlan: organization.FeaturePlan{Version: 1, PreparedBy: "teams::project-manager-1"}}
	f := organization.FeatureRequest{ID: id(61008), Revision: 6, PlanAmendment: a}
	brief := application.ExecutionBrief{ActorFQN: a.PriorPlan.PreparedBy, Execution: execution, Task: application.TaskExecutionSpecification{TaskID: a.AssessmentID}}
	p := organization.PlanAmendmentProposal{SchemaVersion: organization.PlanAmendmentContractVersion, FeatureID: f.ID, FeatureRevision: a.FeatureRevision, PlanVersion: 1, PlanDigest: sha, SpecificationDigest: sha, BlockedTaskID: a.BlockedTaskID, BlockedTaskRevision: a.BlockedTaskRevision, BlockedInvocationID: a.BlockedInvocationID, ConsultationID: a.ConsultationID, AnswerDigest: sha, Reason: "Cover omitted caller ownership",
		SuccessorPlan: organization.FeaturePlan{Version: 2, PreparedBy: brief.ActorFQN, PreparedExecution: execution, Architecture: "Unchanged architecture", SourceDesignDigest: sha, Stories: []organization.PlannedStory{story}, Tasks: []organization.PlannedTask{task}, CreatedAt: now},
		Replacements:  []organization.PlanTaskReplacement{{PriorTaskID: a.BlockedTaskID, SuccessorTaskID: task.ID}}, Impacts: []organization.PlanAmendmentImpact{{Kind: "CALLER", Path: "service/caller.go", OwnerTaskID: task.ID, Verification: "Compile and test caller"}}}
	return f, brief, p
}

func TestBoundAmendmentSchemaPinsHarnessIdentityWithoutChangingFrozenContract(t *testing.T) {
	id := candidateTestUUID
	frozen := organization.PlanAmendmentProposalSchema()
	f, brief, p := boundAmendmentFixture()
	now := f.PlanAmendment.RequestedAt
	schema, err := boundPlanAmendmentSchema(f, brief)
	if err != nil {
		t.Fatal(err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	if err := compiler.AddResource("urn:bound-amendment-test", document); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile("urn:bound-amendment-test")
	if err != nil {
		t.Fatal(err)
	}
	validate := func(p organization.PlanAmendmentProposal) error {
		t.Helper()
		instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(mustJSON(p)))
		if err != nil {
			t.Fatal(err)
		}
		return compiled.Validate(instance)
	}
	if err := validate(p); err != nil {
		t.Fatal("exact request rejected", err)
	}
	for _, tc := range []struct {
		name   string
		change func(*organization.PlanAmendmentProposal)
	}{
		{"feature", func(p *organization.PlanAmendmentProposal) { p.FeatureID = id(61009) }},
		{"reservation revision, not current revision", func(p *organization.PlanAmendmentProposal) { p.FeatureRevision = f.Revision }},
		{"r59 successor as source version", func(p *organization.PlanAmendmentProposal) { p.PlanVersion = 2 }},
		{"r59 invented digest", func(p *organization.PlanAmendmentProposal) { p.PlanDigest = kernel.Digest(strings.Repeat("b", 64)) }},
		{"specification digest", func(p *organization.PlanAmendmentProposal) {
			p.SpecificationDigest = kernel.Digest(strings.Repeat("b", 64))
		}},
		{"blocked task", func(p *organization.PlanAmendmentProposal) { p.BlockedTaskID = id(61009) }},
		{"blocked revision", func(p *organization.PlanAmendmentProposal) { p.BlockedTaskRevision++ }},
		{"blocked invocation", func(p *organization.PlanAmendmentProposal) { p.BlockedInvocationID = id(61009) }},
		{"consultation", func(p *organization.PlanAmendmentProposal) { p.ConsultationID = id(61009) }},
		{"answer digest", func(p *organization.PlanAmendmentProposal) { p.AnswerDigest = kernel.Digest(strings.Repeat("b", 64)) }},
		{"successor version", func(p *organization.PlanAmendmentProposal) { p.SuccessorPlan.Version++ }},
		{"preparer", func(p *organization.PlanAmendmentProposal) { p.SuccessorPlan.PreparedBy = "teams::project-manager-2" }},
		{"old execution", func(p *organization.PlanAmendmentProposal) { p.SuccessorPlan.PreparedExecution.ExecutionID = id(61009) }},
		{"fence", func(p *organization.PlanAmendmentProposal) { p.SuccessorPlan.PreparedExecution.FencingEpoch++ }},
		{"r59 future timestamp", func(p *organization.PlanAmendmentProposal) { p.SuccessorPlan.CreatedAt = now.Add(6 * time.Hour) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := p
			tc.change(&changed)
			if organization.ValidatePlanAmendmentProposalJSON(mustJSON(changed)) != nil {
				t.Fatal("case must remain syntactically legal under the frozen generic contract")
			}
			if validate(changed) == nil {
				t.Fatal("request allowed invented harness identity")
			}
		})
	}
	replay, err := boundPlanAmendmentSchema(f, brief)
	if err != nil || !bytes.Equal(schema, replay) || !bytes.Equal(frozen, organization.PlanAmendmentProposalSchema()) {
		t.Fatal("request schema not restart-stable or mutated the frozen contract", err)
	}
	brief.Execution.FencingEpoch++
	refenced, err := boundPlanAmendmentSchema(f, brief)
	if err != nil || bytes.Equal(schema, refenced) {
		t.Fatal("new execution authority reused the old schema", err)
	}
	brief.ActorFQN = "teams::project-manager-2"
	if _, err := boundPlanAmendmentSchema(f, brief); err == nil {
		t.Fatal("wrong actor accepted")
	}
}
