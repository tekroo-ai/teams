package operationalruntime

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

type refinementStageResult struct {
	SchemaVersion          string                       `json:"schema_version"`
	ResultType             string                       `json:"result_type"`
	AcceptanceCriteria     []string                     `json:"acceptance_criteria"`
	ClarificationQuestions []string                     `json:"clarification_questions"`
	Priority               organization.FeaturePriority `json:"priority"`
}

type specificationStageResult struct {
	SchemaVersion string `json:"schema_version"`
	ResultType    string `json:"result_type"`
	Stories       []struct {
		Title              string                       `json:"title"`
		Description        string                       `json:"description"`
		AcceptanceCriteria []string                     `json:"acceptance_criteria"`
		Priority           organization.FeaturePriority `json:"priority"`
	} `json:"stories"`
	DesignConstraints []string `json:"design_constraints"`
}

type architectureTaskResult struct {
	StoryIndex         uint32                 `json:"story_index"`
	Title              string                 `json:"title"`
	Description        string                 `json:"description"`
	AcceptanceCriteria []string               `json:"acceptance_criteria"`
	DependsOn          []uint32               `json:"depends_on"`
	Validates          []uint32               `json:"validates"`
	Role               string                 `json:"role"`
	Purpose            kernel.WorkPurpose     `json:"purpose"`
	Complexity         uint8                  `json:"complexity"`
	Risk               organization.RiskLevel `json:"risk"`
	CriticalPath       bool                   `json:"critical_path"`
	AttemptLimit       uint32                 `json:"attempt_limit"`
	ReviewRoundLimit   uint32                 `json:"review_round_limit"`
}

type architectureStageResult struct {
	SchemaVersion   string                   `json:"schema_version"`
	ResultType      string                   `json:"result_type"`
	Architecture    string                   `json:"architecture"`
	DesignDecisions []string                 `json:"design_decisions"`
	Assumptions     []string                 `json:"assumptions"`
	Tasks           []architectureTaskResult `json:"tasks"`
}

func decodeOrganizationalStageResult(output []byte, target any) error {
	marker := []byte(application.OrganizationalResultMarker)
	index := bytes.LastIndex(output, marker)
	if index < 0 || bytes.Count(output, marker) != 1 || index > 0 && output[index-1] != '\n' {
		return errInvalidValidationResult
	}
	decoder := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(output[index+len(marker):])))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return errInvalidValidationResult
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return errInvalidValidationResult
	}
	return nil
}

func validStageStrings(values []string, required bool) bool {
	if required && len(values) == 0 || len(values) > 64 {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != value || value == "" || len(value) > 4096 {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func parseRefinementStageResult(output []byte) (refinementStageResult, error) {
	var result refinementStageResult
	if decodeOrganizationalStageResult(output, &result) != nil || result.SchemaVersion != "1.0.0" || result.ResultType != "FEATURE_REFINEMENT" || !result.Priority.Valid() || !validStageStrings(result.AcceptanceCriteria, true) || len(result.AcceptanceCriteria) > 32 || !validStageStrings(result.ClarificationQuestions, false) || len(result.ClarificationQuestions) > 16 {
		return refinementStageResult{}, organization.ErrInvalidFeature
	}
	return result, nil
}

func parseSpecificationStageResult(output []byte) (specificationStageResult, error) {
	var result specificationStageResult
	if decodeOrganizationalStageResult(output, &result) != nil || result.SchemaVersion != "1.0.0" || result.ResultType != "FEATURE_SPECIFICATION" || len(result.Stories) == 0 || len(result.Stories) > organization.MaximumFeatureStories || !validStageStrings(result.DesignConstraints, false) {
		return specificationStageResult{}, organization.ErrInvalidFeature
	}
	for _, story := range result.Stories {
		if strings.TrimSpace(story.Title) == "" || len(story.Title) > 256 || strings.TrimSpace(story.Description) == "" || len(story.Description) > 64<<10 || !story.Priority.Valid() || !validStageStrings(story.AcceptanceCriteria, true) || len(story.AcceptanceCriteria) > 32 {
			return specificationStageResult{}, organization.ErrInvalidFeature
		}
	}
	return result, nil
}

func parseArchitectureStageResult(output []byte) (architectureStageResult, error) {
	var result architectureStageResult
	if decodeOrganizationalStageResult(output, &result) != nil || result.SchemaVersion != "1.0.0" || result.ResultType != "FEATURE_PLAN" || strings.TrimSpace(result.Architecture) == "" || len(result.Architecture) > 64<<10 || !validStageStrings(result.DesignDecisions, false) || !validStageStrings(result.Assumptions, false) || len(result.Tasks) == 0 || len(result.Tasks) > organization.MaximumFeatureTasks {
		return architectureStageResult{}, organization.ErrInvalidFeature
	}
	for index, task := range result.Tasks {
		if task.StoryIndex >= organization.MaximumFeatureStories || strings.TrimSpace(task.Title) == "" || len(task.Title) > 256 || strings.TrimSpace(task.Description) == "" || len(task.Description) > 64<<10 || !validStageStrings(task.AcceptanceCriteria, true) || task.Role == "" || !task.Purpose.Valid() || task.Complexity == 0 || task.Complexity > 10 || !task.Risk.Valid() || task.AttemptLimit == 0 || task.AttemptLimit > 16 || task.ReviewRoundLimit == 0 || task.ReviewRoundLimit > 8 {
			return architectureStageResult{}, organization.ErrInvalidFeature
		}
		for _, dependency := range append(append([]uint32(nil), task.DependsOn...), task.Validates...) {
			if dependency >= uint32(index) {
				return architectureStageResult{}, organization.ErrInvalidFeature
			}
		}
	}
	return result, nil
}
