package operationalruntime

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

type SpecificationStoryInput struct {
	Title              string                       `json:"title"`
	Description        string                       `json:"description"`
	AcceptanceCriteria []string                     `json:"acceptance_criteria"`
	Priority           organization.FeaturePriority `json:"priority"`
}

// FeatureSpecificationCorrectionRequest is a human scope correction, not a
// second model result. It preserves the PM's original output and requires
// evidence from the existing feature budget.
type FeatureSpecificationCorrectionRequest struct {
	ExpectedRevision            uint64                    `json:"expected_revision"`
	ExpectedSpecificationDigest kernel.Digest             `json:"expected_specification_sha256"`
	Stories                     []SpecificationStoryInput `json:"stories"`
	DesignConstraints           []string                  `json:"design_constraints"`
	Reason                      string                    `json:"reason"`
	EvidenceRefs                []kernel.EvidenceRef      `json:"evidence_refs"`
	IdempotencyKey              string                    `json:"idempotency_key"`
}

func (request FeatureSpecificationCorrectionRequest) Valid() bool {
	if request.ExpectedRevision == 0 || !request.ExpectedSpecificationDigest.Valid() || len(request.Stories) == 0 || len(request.Stories) > organization.MaximumFeatureStories || len(request.DesignConstraints) > 64 || request.Reason == "" || len(request.Reason) > 4096 || len(request.EvidenceRefs) == 0 || len(request.EvidenceRefs) > 64 || request.IdempotencyKey == "" || len(request.IdempotencyKey) > 256 {
		return false
	}
	for _, story := range request.Stories {
		if story.Title == "" || len(story.Title) > 256 || story.Description == "" || len(story.Description) > 64<<10 || !story.Priority.Valid() || !validStageStrings(story.AcceptanceCriteria, true) {
			return false
		}
	}
	if !validStageStrings(request.DesignConstraints, false) {
		return false
	}
	seen := make(map[kernel.UUIDv7]struct{}, len(request.EvidenceRefs))
	for _, evidence := range request.EvidenceRefs {
		if !evidence.EvidenceID.Valid() || !evidence.SHA256.Valid() {
			return false
		}
		if _, duplicate := seen[evidence.EvidenceID]; duplicate {
			return false
		}
		seen[evidence.EvidenceID] = struct{}{}
	}
	return true
}

func (service *ProductionService) CorrectFeatureSpecification(ctx context.Context, principal kernel.PrincipalRef, featureID kernel.UUIDv7, request FeatureSpecificationCorrectionRequest) (organization.FeatureRequest, error) {
	if service == nil || service.Features == nil || principal != service.operatorIdentity.Principal || !featureID.Valid() || !request.Valid() {
		return organization.FeatureRequest{}, application.ErrInvalidConfiguration
	}
	feature, found, err := service.ReadFeature(ctx, featureID)
	if err != nil || !found {
		return organization.FeatureRequest{}, errors.Join(organization.ErrFeatureNotFound, err)
	}
	if feature.SpecificationCorrection != nil && feature.SpecificationCorrection.IdempotencyKey == request.IdempotencyKey {
		if feature.Revision == request.ExpectedRevision+1 && feature.SpecificationCorrection.PriorDigest == request.ExpectedSpecificationDigest && feature.SpecificationCorrection.Reason == request.Reason && sameEvidenceSet(feature.SpecificationCorrection.EvidenceRefs, request.EvidenceRefs) && correctionMatchesSpecification(feature.Specification, request) {
			return feature, nil
		}
		return organization.FeatureRequest{}, organization.ErrFeatureRevisionConflict
	}
	if feature.Status != organization.FeatureSpecified || feature.Revision != request.ExpectedRevision || feature.Specification == nil || feature.SpecificationCorrection != nil || feature.Plan != nil || len(request.Stories) > int(feature.Input.MaximumStories) {
		return organization.FeatureRequest{}, organization.ErrFeatureRevisionConflict
	}
	priorDigest, err := featurePlanningStateDigest(*feature.Specification)
	if err != nil || priorDigest != request.ExpectedSpecificationDigest || !correctionPreservesCriteria(feature.Input.AcceptanceCriteria, request.Stories) {
		return organization.FeatureRequest{}, organization.ErrInvalidFeature
	}
	round, err := service.latestArchitecturePlanRound(ctx, feature)
	if err != nil || round >= architecturePlanRecordedRoundLimit {
		return organization.FeatureRequest{}, errors.Join(organization.ErrInvalidFeature, err)
	}
	state, _, taskFound, err := service.Store.ReadAggregateHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateTask, ID: featurePlanningTaskID(feature.ID, stageArchitecture, round, nil)})
	if err != nil || !taskFound || state.Condition != kernel.ConditionBlocked {
		return organization.FeatureRequest{}, errors.Join(organization.ErrInvalidFeature, err)
	}
	taskSnapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: featurePlanningTaskID(feature.ID, stageArchitecture, round, nil)}})
	if err != nil {
		return organization.FeatureRequest{}, err
	}
	latest, latestFound := latestTaskInvocation(taskSnapshot.WorkInvocations, featurePlanningTaskID(feature.ID, stageArchitecture, round, nil))
	if !latestFound || !latest.State.Terminal() {
		return organization.FeatureRequest{}, application.ErrInvalidOperationalExecution
	}
	budgetRef := kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: feature.BudgetAccountID}
	budgetSnapshot, err := service.Store.LoadDecision(ctx, kernel.KernelCommand{Target: budgetRef})
	if err != nil {
		return organization.FeatureRequest{}, err
	}
	now := service.clock.Now().UTC()
	budget, budgetFound := budgetSnapshot.WorkBudgetAccounts[budgetRef]
	if !budgetFound || !budget.Valid() || !budget.DeadlineAt.After(now.Add(service.requestTimeout)) {
		return organization.FeatureRequest{}, organization.ErrInvalidFeature
	}
	evidenceIDs := make([]kernel.UUIDv7, len(request.EvidenceRefs))
	for index, evidence := range request.EvidenceRefs {
		evidenceIDs[index] = evidence.EvidenceID
	}
	registered, err := evidenceRefsForIDs(budgetSnapshot, evidenceIDs)
	if err != nil || !sameEvidenceSet(registered, request.EvidenceRefs) {
		return organization.FeatureRequest{}, errors.Join(organization.ErrInvalidFeature, err)
	}
	stories := make([]organization.PlannedStory, len(request.Stories))
	for index, story := range request.Stories {
		stories[index] = organization.PlannedStory{
			ID:    deterministicOperationalUUID("feature-corrected-story", string(feature.ID), fmt.Sprint(feature.ScopeRevision+1), fmt.Sprint(index), story.Title),
			Title: story.Title, Description: story.Description, AcceptanceCriteria: append([]string(nil), story.AcceptanceCriteria...), Priority: story.Priority,
		}
	}
	replacement := organization.FeatureSpecification{
		PreparedBy: feature.Specification.PreparedBy, PreparedExecution: feature.Specification.PreparedExecution,
		Stories: stories, DesignConstraints: append([]string(nil), request.DesignConstraints...),
		PreparedAt: feature.Specification.PreparedAt, AmendedBy: &principal, AmendedAt: &now,
	}
	replacementDigest, err := featurePlanningStateDigest(replacement)
	if err != nil || replacementDigest == priorDigest {
		return organization.FeatureRequest{}, organization.ErrInvalidFeature
	}
	correction := organization.FeatureSpecificationCorrection{
		PriorSpecification: *feature.Specification, PriorDigest: priorDigest, ReplacementDigest: replacementDigest,
		ArchitectureRound: round + 1, RequestedBy: principal, Reason: request.Reason,
		EvidenceRefs: append([]kernel.EvidenceRef(nil), registered...), RequestedAt: now, IdempotencyKey: request.IdempotencyKey,
	}
	return service.Features.CorrectSpecification(ctx, feature.ID, feature.Revision, replacement, correction)
}

func correctionPreservesCriteria(required []string, stories []SpecificationStoryInput) bool {
	for _, criterion := range required {
		found := false
		for _, story := range stories {
			for _, candidate := range story.AcceptanceCriteria {
				if criterion == candidate {
					found = true
					break
				}
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func correctionMatchesSpecification(specification *organization.FeatureSpecification, request FeatureSpecificationCorrectionRequest) bool {
	if specification == nil || len(specification.Stories) != len(request.Stories) || !slices.Equal(specification.DesignConstraints, request.DesignConstraints) {
		return false
	}
	for index, story := range request.Stories {
		actual := specification.Stories[index]
		if actual.Title != story.Title || actual.Description != story.Description || actual.Priority != story.Priority || !slices.Equal(actual.AcceptanceCriteria, story.AcceptanceCriteria) {
			return false
		}
	}
	return true
}
