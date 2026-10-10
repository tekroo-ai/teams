package operationalruntime

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/nativeagent"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestAdvisoryTasksPreserveDecisionSourceRouteAndFiniteAuthority(t *testing.T) {
	id := func(n int) kernel.UUIDv7 { return deterministicOperationalUUID("decision-work-test", strconv.Itoa(n)) }
	now := time.Now().UTC()
	for _, role := range []string{"architect", "project-manager"} {
		for _, route := range []kernel.DecisionRoute{kernel.RouteBoundedExecution, kernel.RouteComplexReasoning} {
			t.Run(role+"/"+string(route), func(t *testing.T) {
				source := recoveryTerminalFixture(kernel.InvocationSucceeded, nil, nil)
				source.ActorFQN = kernel.ActorFQN("fixture::" + role + "-1")
				profile := validatorMaintenanceProfile(source.TaskID, source.WorkProfile.ProfileID, source.WorkProfile.ProfileRevision, source.WorkProfile.ProfileDigest, now.Add(time.Hour), []kernel.UUIDv7{id(60001)})
				profile.WorkKind, profile.MinimumDecisionRoute = kernel.WorkDesign, route
				source.WorkProfile = profile.Binding()
				bound := application.OperationalExecutionContext{Invocation: source, Profile: kernel.WorkProfileSnapshot{Profile: profile, BoundEventID: id(60002), TaskRevision: source.TaskRevision}}
				got, err := decisionSourceRoute(source, bound)
				if err != nil || got != route {
					t.Fatalf("source classification: %s %v", got, err)
				}
				// Source classification and the new advisory's work kind are
				// separate requirements. Do not invent a source-kind restriction.
				otherKind := bound
				otherKind.Profile.Profile.WorkKind = kernel.WorkInvestigation
				if got, err := decisionSourceRoute(source, otherKind); err != nil || got != route {
					t.Fatal("valid source classification narrowed by work kind", err)
				}
				task := organization.PlannedTask{ID: id(60003), StoryID: id(60004), Title: "Consume accepted interface"}
				feature := organization.FeatureRequest{ID: id(60005), Plan: &organization.FeaturePlan{Version: 1}, PlanAmendment: &organization.FeaturePlanAmendment{AssessmentID: id(60006), ConsultationID: id(60007), PriorPlan: organization.FeaturePlan{PreparedBy: source.ActorFQN, Tasks: []organization.PlannedTask{task}}}}
				generated := consultationTask(feature, task, source, source, "Which interface?", got)
				if role == "project-manager" {
					generated = amendmentAssessmentTask(feature, source, source.ModelProfileDigest, got)
				}
				model := ProductionProfile{RoleFQRN: kernel.RoleFQRN(role), ModelProfileDigest: source.ModelProfileDigest, ToolPolicyDigest: repeatedDigest('3'), DecisionRoute: route}
				model.QualificationCorpus, model.Qualification = testQualificationBundle(t, model.ModelProfileDigest, model.RoleFQRN, route, model.ToolPolicyDigest, []kernel.WorkKind{kernel.WorkDesign}, now.Add(-time.Minute))
				owner := organization.RoleInstanceState{ActorFQN: source.ActorFQN, Role: role, ModelProfile: model.ModelProfileDigest}
				service := &ProductionService{clock: fixedClock{now: now}, profilesByModel: map[kernel.Digest]ProductionProfile{model.ModelProfileDigest: model}}
				if _, err := service.qualifiedTaskOwnerProfile(owner, generated); err != nil {
					t.Fatal("qualified advisory rejected", err)
				}
				if generated.DecisionRoute != route || generated.AttemptLimit != 1 || generated.Purpose != kernel.PurposeHandoff || len(generated.WriteScope) != 0 {
					t.Fatal("advisory changed classification or finite authority", generated)
				}
				bad := bound
				bad.Profile.Profile.ProfileDigest = repeatedDigest('e')
				if _, err := decisionSourceRoute(source, bad); !errors.Is(err, nativeagent.ErrInvalidBinding) {
					t.Fatal("unbound classification accepted", err)
				}
				bad = bound
				bad.Invocation.Execution.FencingEpoch++
				if _, err := decisionSourceRoute(source, bad); !errors.Is(err, nativeagent.ErrInvalidBinding) {
					t.Fatal("different source execution accepted", err)
				}
				if route == kernel.RouteComplexReasoning {
					model.DecisionRoute = kernel.RouteBoundedExecution
					service.profilesByModel[model.ModelProfileDigest] = model
					if _, err := service.qualifiedTaskOwnerProfile(owner, generated); !errors.Is(err, organization.ErrInvalidFeature) || !strings.Contains(err.Error(), "selected route does not satisfy") {
						t.Fatal("source requirement silently downgraded", err)
					}
				}
			})
		}
	}
}

func TestTaskOwnerQualificationMatrixAndSpecificFailures(t *testing.T) {
	id := func(n int) kernel.UUIDv7 { return deterministicOperationalUUID("decision-work-test", strconv.Itoa(n)) }
	now := time.Now().UTC()
	for _, c := range []struct {
		role    string
		route   kernel.DecisionRoute
		purpose kernel.WorkPurpose
		risk    organization.RiskLevel
		kind    kernel.WorkKind
	}{
		{"product-owner", kernel.RouteBoundedExecution, kernel.PurposeHandoff, organization.RiskModerate, kernel.WorkDesign},
		{"project-manager", kernel.RouteBoundedExecution, kernel.PurposeHandoff, organization.RiskModerate, kernel.WorkDesign},
		{"architect", kernel.RouteComplexReasoning, kernel.PurposeHandoff, organization.RiskModerate, kernel.WorkDesign},
		{"coder", kernel.RouteBoundedExecution, kernel.PurposeImplementation, organization.RiskModerate, kernel.WorkImplementation},
		{"senior-coder", kernel.RouteComplexReasoning, kernel.PurposeImplementation, organization.RiskModerate, kernel.WorkImplementation},
		{"tester", kernel.RouteBoundedExecution, kernel.PurposeValidation, organization.RiskModerate, kernel.WorkValidation},
		{"security", kernel.RouteComplexReasoning, kernel.PurposeReview, organization.RiskHigh, kernel.WorkSecurityReview},
		{"product-owner", kernel.RouteBoundedExecution, kernel.PurposePromotion, organization.RiskModerate, kernel.WorkRelease},
	} {
		t.Run(c.role+"/"+string(c.kind), func(t *testing.T) {
			p := ProductionProfile{RoleFQRN: kernel.RoleFQRN(c.role), ModelProfileDigest: repeatedDigest('a'), ToolPolicyDigest: repeatedDigest('b'), DecisionRoute: c.route}
			p.QualificationCorpus, p.Qualification = testQualificationBundle(t, p.ModelProfileDigest, p.RoleFQRN, p.DecisionRoute, p.ToolPolicyDigest, []kernel.WorkKind{c.kind}, now.Add(-time.Minute))
			owner := organization.RoleInstanceState{ActorFQN: kernel.ActorFQN("fixture::" + c.role + "-1"), Role: c.role, ModelProfile: p.ModelProfileDigest}
			task := organization.PlannedTask{ID: id(60011), Owner: owner.ActorFQN, Purpose: c.purpose, Risk: c.risk, DecisionRoute: c.route}
			s := &ProductionService{clock: fixedClock{now: now}, profilesByModel: map[kernel.Digest]ProductionProfile{p.ModelProfileDigest: p}}
			if _, err := s.qualifiedTaskOwnerProfile(owner, task); err != nil {
				t.Fatal(err)
			}
			bad := task
			bad.Purpose = kernel.PurposeInvestigation
			if _, err := s.qualifiedTaskOwnerProfile(owner, bad); !errors.Is(err, organization.ErrInvalidFeature) || !strings.Contains(err.Error(), "work kind is not qualified") || !strings.Contains(err.Error(), "work_kind=INVESTIGATION") {
				t.Fatal("missing work-specific diagnostic", err)
			}
			source := kernel.WorkInvocation{ID: id(60012), ActorFQN: owner.ActorFQN, WorkspaceID: "original"}
			owner.WorkspaceID = "original"
			s.workspacesByID = map[string]ProductionWorkspace{"original": {WorkspaceID: "original"}}
			if _, _, err := s.qualifiedDecisionOwner(owner, task, source); err != nil {
				t.Fatal("original decision workspace rejected", err)
			}
			owner.WorkspaceID = "other"
			if _, _, err := s.qualifiedDecisionOwner(owner, task, source); !errors.Is(err, nativeagent.ErrInvalidBinding) {
				t.Fatal("changed decision workspace accepted", err)
			}
			p.QualificationCorpus, p.Qualification = testQualificationBundle(t, p.ModelProfileDigest, p.RoleFQRN, p.DecisionRoute, p.ToolPolicyDigest, []kernel.WorkKind{c.kind}, now.Add(time.Minute))
			s.profilesByModel[p.ModelProfileDigest] = p
			if _, err := s.qualifiedTaskOwnerProfile(owner, task); !errors.Is(err, organization.ErrInvalidFeature) || !strings.Contains(err.Error(), "not eligible at admission time") {
				t.Fatal("ineligible qualification diagnostic", err)
			}
			delete(s.profilesByModel, p.ModelProfileDigest)
			if _, err := s.qualifiedTaskOwnerProfile(owner, task); !errors.Is(err, organization.ErrInvalidFeature) || !strings.Contains(err.Error(), "selected model profile unavailable") {
				t.Fatal("missing profile diagnostic", err)
			}
		})
	}
}
