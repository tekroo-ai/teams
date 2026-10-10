package operationalruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/tekroo-ai/teams/adapters/nativeagent"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// One forward advisory node per task/plan. Its answer is evidence, not scope
// authority. Unresolved scope changes stay blocked; no conversational cycle or
// reopening of a completed design/provider is permitted.
var consultationSchema = json.RawMessage(`{"type":"object","additionalProperties":false,"required":["disposition","answer"],"properties":{"disposition":{"type":"string","enum":["ANSWERED_WITHIN_SCOPE","NEEDS_SCOPE_CHANGE"]},"answer":{"type":"string","minLength":1,"maxLength":4096}}}`)

func consultationSourceBound(feature organization.FeatureRequest, source kernel.WorkInvocation) bool {
	return feature.Plan != nil && feature.Design != nil && source.State == kernel.InvocationSucceeded &&
		source.OutputDigest != nil && source.OutputDigest.Valid() && *source.OutputDigest == feature.Plan.SourceDesignDigest &&
		*source.OutputDigest == feature.Design.OutputDigest && source.ActorFQN == feature.Design.PreparedBy &&
		source.Execution == feature.Design.PreparedExecution
}

func taskConsultationID(feature organization.FeatureRequest, taskID kernel.UUIDv7) kernel.UUIDv7 {
	return deterministicOperationalUUID("task-consultation", string(feature.ID), fmt.Sprint(feature.Plan.Version), string(taskID))
}

func consultationAnswer(output []byte) (string, bool, error) {
	raw := bytes.TrimSpace(bytes.TrimPrefix(output, []byte(application.OrganizationalResultMarker)))
	var result struct {
		Disposition string `json:"disposition"`
		Answer      string `json:"answer"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&result) != nil || d.Decode(&struct{}{}) != io.EOF || strings.TrimSpace(result.Answer) == "" || !resultTextWithinLimit(result.Answer, 4096) || result.Disposition != "ANSWERED_WITHIN_SCOPE" && result.Disposition != "NEEDS_SCOPE_CHANGE" {
		return "", false, application.ErrInvalidOperationalExecution
	}
	return result.Answer, result.Disposition == "ANSWERED_WITHIN_SCOPE", nil
}

func consultationTask(feature organization.FeatureRequest, task organization.PlannedTask, source kernel.WorkInvocation, blocked kernel.WorkInvocation, question string, route kernel.DecisionRoute) organization.PlannedTask {
	title := "Clarify blocked task: " + task.Title
	if len(title) > 256 {
		title = "Clarify blocked task " + string(task.ID)
	}
	return organization.PlannedTask{ID: taskConsultationID(feature, task.ID), StoryID: task.StoryID,
		Title:              title,
		Description:        "Blocked invocation: " + string(blocked.ID) + "\nAnswer the reported interface/scope question using the accepted design, supplied task evidence, and retained original reasoning. Do not implement, expand scope, revise the DAG, or ask another agent. If a change of scope or design is necessary, report NEEDS_SCOPE_CHANGE with the exact change for the operator. Otherwise explain an actionable answer within the existing scope. Return one structured object with disposition (ANSWERED_WITHIN_SCOPE or NEEDS_SCOPE_CHANGE) and answer.\nQuestion: " + question,
		AcceptanceCriteria: []string{"The answer resolves the reported question within accepted scope or identifies the exact change requiring authorization."},
		DependsOn:          []kernel.UUIDv7{source.TaskID}, Owner: source.ActorFQN, ModelProfile: source.ModelProfileDigest,
		Purpose: kernel.PurposeHandoff, DecisionRoute: route, Complexity: 2, Risk: organization.RiskModerate, AttemptLimit: 1, ReviewRoundLimit: 1}
}

func (service *ProductionService) reconcileTaskConsultations(ctx context.Context, feature organization.FeatureRequest, plan organization.FeaturePlan, states map[kernel.UUIDv7]kernel.AggregateState, invocations map[kernel.UUIDv7]kernel.WorkInvocation) (bool, error) {
	if service.newInvocationAdmissionBlocked() || !service.qualificationNative || feature.Design == nil {
		return false, nil
	}
	for _, task := range plan.Tasks {
		inv, found := invocations[task.ID]
		if !found || task.Purpose != kernel.PurposeImplementation || states[task.ID].Phase != kernel.PhaseActive || inv.State != kernel.InvocationSucceeded || inv.OutputDigest == nil {
			continue
		}
		output, err := service.Runtime.ReadExecutionOutput(ctx, *inv.OutputDigest)
		if err != nil {
			return false, err
		}
		question, outcome, reported := reportedTaskNonCompletion(output)
		if !reported || outcome != "needs_decision" {
			continue
		}
		source, _, err := service.featureArchitectureCandidate(ctx, feature)
		if err != nil {
			return false, err
		}
		if !consultationSourceBound(feature, source) {
			return false, nativeagent.ErrInvalidBinding
		}
		route, err := service.retainedDecisionRoute(ctx, source)
		if err != nil {
			return false, err
		}
		consult := consultationTask(feature, task, source, inv, question, route)
		ref := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: consult.ID}
		state, head, exists, err := service.Store.ReadAggregateHead(ctx, ref)
		if err != nil {
			return false, err
		}
		if !exists && states[task.ID].Condition != kernel.ConditionBlocked {
			continue
		}
		taskState, taskHead, present, err := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID})
		if err != nil {
			return false, err
		}
		if !present {
			return false, organization.ErrInvalidFeature
		}
		kind, err := service.classifyTaskRecovery(ctx, task, taskState, taskHead, inv)
		if err != nil {
			return false, err
		}
		if kind != taskRecoveryReportedDecision {
			continue
		}
		snapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: inv.Ref()})
		if err != nil {
			return false, err
		}
		evidence, err := evidenceForInvocations(snapshot, inv, source)
		if err != nil || len(evidence) == 0 {
			return false, errors.Join(organization.ErrInvalidFeature, err)
		}
		if exists && (state.Phase == kernel.PhaseActive || state.Phase == kernel.PhaseCompleted) {
			cs, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: ref})
			if err != nil {
				return false, err
			}
			latest, present := latestTaskInvocation(cs.WorkInvocations, consult.ID)
			if present && !latest.State.Terminal() {
				continue
			}
			if !present && state.Phase == kernel.PhaseCompleted {
				return false, application.ErrInvalidOperationalExecution
			}
			if present {
				bound, err := service.Store.LoadOperationalExecution(ctx, latest.ID)
				if err != nil {
					return false, err
				}
				if bound.Specification.Description != consult.Description {
					continue
				} // one question, never reuse an old answer
				if latest.State != kernel.InvocationSucceeded || latest.OutputDigest == nil {
					continue
				} // no automatic consultation retries
				answerRaw, err := service.Runtime.ReadExecutionOutput(ctx, *latest.OutputDigest)
				if err != nil {
					return false, err
				}
				answer, resolved, err := consultationAnswer(answerRaw)
				if err != nil {
					return false, err
				}
				if state.Phase != kernel.PhaseCompleted {
					if !bound.Profile.Valid() || bound.Profile.Profile.Binding() != latest.WorkProfile {
						return false, nativeagent.ErrInvalidBinding
					}
					consult.DecisionRoute = bound.Profile.Profile.MinimumDecisionRoute
					if err := service.completeEvidenceTask(ctx, feature, consult, state, head, latest, cs); err != nil {
						return false, err
					}
					return true, nil // recover on the next pass from durable completion
				}
				if !resolved {
					return service.reservePlanAmendment(ctx, feature, inv, latest)
				}
				// Recovery uses registered, immutable answer evidence and the same
				// existing workspace. It cannot grant a different write scope.
				refs, err := evidenceForInvocations(cs, latest)
				if err != nil {
					return false, err
				}
				_, err = service.RetryFailedTask(ctx, service.operatorIdentity.Principal, inv.ID, TaskRecoveryRequest{ExpectedRevision: inv.Revision, Reason: consultationRecoveryReason(answer), EvidenceRefs: refs, DeadlineAt: automaticRetryableRecoveryDeadline(inv.DeadlineAt), IdempotencyKey: "consultation-answer-" + string(consult.ID)})
				return err == nil, err
			}
		}
		owner, err := service.ensurePlanningActorExcept(ctx, []kernel.ActorFQN{source.ActorFQN}, "")
		if err != nil {
			return false, err
		}
		consult.ModelProfile = owner.ModelProfile
		profile, workspace, err := service.qualifiedDecisionOwner(owner, consult, source)
		if err != nil {
			return false, err
		}
		if owner.ModelProfile != source.ModelProfileDigest {
			return false, organization.ErrInvalidFeature
		}
		if !exists {
			payload, _ := json.Marshal(map[string]any{"story_id": consult.StoryID, "title": consult.Title, "description": consult.Description, "acceptance_criteria": consult.AcceptanceCriteria, "depends_on": consult.DependsOn})
			_, err = service.submitPlannedCommand(ctx, feature, "tekroo.command.task.create", kernel.AggregateTask, consult.ID, "task-consultation", payload, []kernel.DagParent{{ParentEventID: inv.LastEventID, EdgeKind: kernel.EdgeResponse}, {ParentEventID: source.LastEventID, EdgeKind: kernel.EdgeCausal}})
			if err != nil {
				return false, err
			}
			state, head, _, err = service.Store.ReadAggregateHead(ctx, ref)
			if err != nil {
				return false, err
			}
		}
		if err := service.registerExecution(ctx, owner, profile); err != nil {
			return false, err
		}
		budget, found := snapshot.WorkBudgetAccounts[kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: feature.BudgetAccountID}]
		if !found {
			return false, organization.ErrInvalidFeature
		}
		cs, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: ref})
		if err != nil {
			return false, err
		}
		wp := service.workProfile(feature, consult, state.LifecycleEpoch, state.ScopeRevision, evidence[0].EvidenceID, budget.DeadlineAt)
		tracked := &trackedTask{plan: consult, owner: owner, profile: wp, revision: state.Revision, last: head}
		if existing, found := cs.WorkProfiles[ref]; found {
			tracked.profile = existing.Profile
		} else if err := service.bindConsultationWorkProfile(ctx, feature, tracked, evidence); err != nil {
			return false, err
		}
		if err := service.activateTask(ctx, feature, tracked, profile, workspace, budget.Revision, []kernel.UUIDv7{source.LastEventID}, evidence, evidence[0].EvidenceID, []kernel.Digest{*inv.OutputDigest, *source.OutputDigest}); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

func (service *ProductionService) bindConsultationWorkProfile(ctx context.Context, feature organization.FeatureRequest, task *trackedTask, evidence []kernel.EvidenceRef) error {
	// Both the reported question and accepted design classify this advisory
	// work. The kernel requires the declared IDs to equal command evidence;
	// declaring only evidence[0] prevents admission with UNAUTHORIZED.
	task.profile.ClassificationEvidenceIDs = evidenceIDs(evidence)
	// Rejected commands retain idempotent receipts. Do not reuse the old
	// single-evidence "profile" identity with a corrected payload, or the
	// durable rejection will be replayed (or conflict) after deployment.
	key := "consultation-profile-evidence-" + string(digestBytes(mustJSON(evidence)))
	return service.applyTaskCommand(ctx, feature, task, "tekroo.command.task.bind-work-profile", kernel.SchemaVersion, service.policyAuthority, task.profile, evidence, nil, key)
}

// Resolve by deterministic task identity and authoritative Teams state. No
// model-supplied actor, conversation ID, or result schema is trusted.
func (service *ProductionService) nativeTaskConsultation(ctx context.Context, brief application.ExecutionBrief) (*nativeagent.ResultContract, *kernel.WorkInvocation, error) {
	features, err := service.nativeBriefFeatures(ctx, brief)
	if err != nil {
		return nil, nil, err
	}
	for _, feature := range features {
		if feature.Plan == nil {
			continue
		}
		plans := []*organization.FeaturePlan{feature.Plan}
		if feature.PlanAmendment != nil {
			plans = append(plans, &feature.PlanAmendment.PriorPlan)
		}
		for _, plan := range plans {
			prior := feature
			prior.Plan = plan
			for _, task := range plan.Tasks {
				if taskConsultationID(prior, task.ID) != brief.Task.TaskID {
					continue
				}
				if a := feature.PlanAmendment; a != nil && a.ConsultationID == brief.Task.TaskID && a.ConsultationInvocationID != brief.InvocationID {
					return nil, nil, nativeagent.ErrInvalidBinding
				}
				source, _, err := service.featureArchitectureCandidate(ctx, feature)
				if err != nil {
					return nil, nil, err
				}
				if !consultationSourceBound(feature, source) || source.ActorFQN != brief.ActorFQN || brief.Purpose != kernel.PurposeHandoff {
					return nil, nil, nativeagent.ErrInvalidBinding
				}
				contract := &nativeagent.ResultContract{TaskID: brief.Task.TaskID, TaskSourceDigest: brief.Task.SourceDigest, SchemaRef: "tekroo.task.consultation", SchemaSHA256: digestBytes(consultationSchema), Schema: consultationSchema}
				return contract, &source, nil
			}
		}
	}
	return nil, nil, nil
}

func consultationRecoveryReason(answer string) string {
	const prefix = "Bounded design-owner clarification (full answer in admitted evidence): "
	limit := 4096 - len(prefix)
	if len(answer) > limit {
		answer = answer[:limit]
		for !utf8.ValidString(answer) {
			answer = answer[:len(answer)-1]
		}
	}
	return prefix + answer
}
