package operationalruntime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestTaskConsultationIsOneForwardNodeBoundToOriginalQuestion(t *testing.T) {
	feature := organization.FeatureRequest{ID: "00000000-0000-7000-8000-000000000311", Plan: &organization.FeaturePlan{Version: 1}}
	task := organization.PlannedTask{ID: "00000000-0000-7000-8000-000000000312", StoryID: "00000000-0000-7000-8000-000000000313", Title: "Consume adapter"}
	source := recoveryTerminalFixture(kernel.InvocationSucceeded, nil, nil)
	source.TaskID = "00000000-0000-7000-8000-000000000314"
	source.ActorFQN = "some-team::decision-owner-1"
	blocked := source
	blocked.ID = "00000000-0000-7000-8000-000000000315"
	consult := consultationTask(feature, task, source, blocked, "The constructor input is unavailable in my write scope.", kernel.RouteComplexReasoning)
	if consult.ID == task.ID || consult.ID == source.TaskID || consult.Owner != source.ActorFQN || consult.AttemptLimit != 1 || consult.Purpose != kernel.PurposeHandoff || len(consult.DependsOn) != 1 || consult.DependsOn[0] != source.TaskID || len(consult.WriteScope) != 0 {
		t.Fatalf("advisory node changed authority or reopened work: %+v", consult)
	}
	other := blocked
	other.ID = "00000000-0000-7000-8000-000000000316"
	next := consultationTask(feature, task, source, other, "Different question", kernel.RouteComplexReasoning)
	if next.ID != consult.ID || next.Description == consult.Description {
		t.Fatal("later attempts can create new consultation cycles or reuse the original answer")
	}
	feature.Plan.Version++
	if taskConsultationID(feature, task.ID) == consult.ID {
		t.Fatal("distinct plan versions share consultation identity")
	}
	if !strings.Contains(consult.Description, "NEEDS_SCOPE_CHANGE") || !strings.Contains(consult.Description, string(blocked.ID)) {
		t.Fatal("scope escalation or original invocation binding lost")
	}
}

func TestConsultationAnswerRequiresRawObjectAndExplicitScopeDisposition(t *testing.T) {
	for _, resolved := range []bool{true, false} {
		disposition := "NEEDS_SCOPE_CHANGE"
		if resolved {
			disposition = "ANSWERED_WITHIN_SCOPE"
		}
		raw := []byte(application.OrganizationalResultMarker + ` {"disposition":"` + disposition + `","answer":"Use the admitted interface."}`)
		answer, got, err := consultationAnswer(raw)
		if err != nil || got != resolved || answer != "Use the admitted interface." {
			t.Fatalf("answer: %q %t %v", answer, got, err)
		}
	}
	for _, raw := range []string{`"{\"answer\":\"x\"}"`, `{"answer":"x"}`, `{"disposition":"APPROVED","answer":"x"}`, `{"disposition":"ANSWERED_WITHIN_SCOPE","answer":" "}`, `{"disposition":"ANSWERED_WITHIN_SCOPE","answer":"x","scope":["."]}`} {
		if _, _, err := consultationAnswer([]byte(raw)); err == nil {
			t.Fatalf("accepted invalid/scope-granting answer: %s", raw)
		}
	}
	reason := consultationRecoveryReason(strings.Repeat("界", 2000))
	if len(reason) > 4096 || !utf8.ValidString(reason) || !strings.Contains(reason, "full answer in admitted evidence") {
		t.Fatal("recovery reason exceeds contract or loses full-answer reference")
	}
}

func TestReportedDecisionRecoveryRequiresExactInvocationBlock(t *testing.T) {
	task := organization.PlannedTask{ID: "00000000-0000-7000-8000-000000000322", Purpose: kernel.PurposeImplementation}
	inv := recoveryTerminalFixture(kernel.InvocationSucceeded, nil, nil)
	inv.TaskID = task.ID
	digest := repeatedDigest('a')
	inv.OutputDigest = &digest
	authority := kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "policy"}
	event := escalationBlockFixture(t, task, inv, authority, func(p *planningOutputBlockPayload) { p.Reason = "task reported needs_decision: missing input" })
	if !isExactReportedDecisionBlock(event, task, inv, authority) {
		t.Fatal("exact decision block rejected")
	}
	for _, reason := range []string{failedTaskEscalationReason, "task reported blocked: missing input", invalidStructuredOutputReason} {
		bad := event
		var p planningOutputBlockPayload
		_ = json.Unmarshal(event.Payload, &p)
		p.Reason = reason
		bad.Payload, _ = json.Marshal(p)
		if isExactReportedDecisionBlock(bad, task, inv, authority) {
			t.Fatalf("unrelated block admitted: %s", reason)
		}
	}
	stale := inv
	stale.ID = "00000000-0000-7000-8000-000000000323"
	if isExactReportedDecisionBlock(event, task, stale, authority) {
		t.Fatal("stale question can reuse answer")
	}
	// No accepted design means no consultation or store/model access.
	service := &ProductionService{qualificationNative: true}
	changed, err := service.reconcileTaskConsultations(context.Background(), organization.FeatureRequest{}, organization.FeaturePlan{}, nil, nil)
	if err != nil || changed {
		t.Fatalf("empty feature admitted consultation: %t %v", changed, err)
	}
}

func TestConsultationSourceMatchesAcceptedPlanDesignAndExecution(t *testing.T) {
	digest := repeatedDigest('a')
	source := recoveryTerminalFixture(kernel.InvocationSucceeded, nil, nil)
	source.OutputDigest = &digest
	feature := organization.FeatureRequest{
		Plan:   &organization.FeaturePlan{SourceDesignDigest: digest},
		Design: &organization.FeatureDesignCandidate{OutputDigest: digest, PreparedBy: source.ActorFQN, PreparedExecution: source.Execution},
	}
	if !consultationSourceBound(feature, source) {
		t.Fatal("accepted design source rejected")
	}
	for _, mutate := range []func(*organization.FeatureRequest, *kernel.WorkInvocation){
		func(f *organization.FeatureRequest, _ *kernel.WorkInvocation) {
			f.Plan.SourceDesignDigest = repeatedDigest('b')
		},
		func(f *organization.FeatureRequest, _ *kernel.WorkInvocation) {
			f.Design.OutputDigest = repeatedDigest('b')
		},
		func(f *organization.FeatureRequest, _ *kernel.WorkInvocation) {
			f.Design.PreparedBy = "other::decision-owner-1"
		},
		func(f *organization.FeatureRequest, _ *kernel.WorkInvocation) {
			f.Design.PreparedExecution.FencingEpoch++
		},
		func(_ *organization.FeatureRequest, s *kernel.WorkInvocation) { s.State = kernel.InvocationFailed },
		func(_ *organization.FeatureRequest, s *kernel.WorkInvocation) { s.OutputDigest = nil },
	} {
		copyFeature, copySource := feature, source
		copyPlan, copyDesign := *feature.Plan, *feature.Design
		copyFeature.Plan, copyFeature.Design = &copyPlan, &copyDesign
		mutate(&copyFeature, &copySource)
		if consultationSourceBound(copyFeature, copySource) {
			t.Fatal("stale or different design authority admitted")
		}
	}
}
