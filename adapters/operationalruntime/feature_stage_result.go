package operationalruntime

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"unicode"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

type refinementStageResult struct {
	SchemaVersion          string                       `json:"schema_version"`
	ResultType             string                       `json:"result_type"`
	AcceptanceDisposition  string                       `json:"acceptance_criteria_disposition"`
	ClarificationQuestions []string                     `json:"clarification_questions"`
	Priority               organization.FeaturePriority `json:"priority"`
}

const preserveSubmittedAcceptanceCriteria = "PRESERVE_SUBMITTED"

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
	StoryIndex         uint32   `json:"story_index"`
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	// Covers is accepted only for backward-compatible decoding. Story-level
	// acceptance remains authoritative and is evaluated by the generated final
	// acceptance work; planning agents are not required to duplicate it as
	// bookkeeping across implementation tasks.
	Covers    []uint32 `json:"covers"`
	DependsOn []uint32 `json:"depends_on"`
	Validates []uint32 `json:"validates"`
	// Role is accepted only for backward-compatible decoding. Operational role
	// selection belongs to Teams routing policy, not to the planning agent.
	Role             string                 `json:"role,omitempty"`
	Purpose          kernel.WorkPurpose     `json:"purpose"`
	Complexity       uint8                  `json:"complexity"`
	Risk             organization.RiskLevel `json:"risk"`
	CriticalPath     bool                   `json:"critical_path"`
	AttemptLimit     uint32                 `json:"attempt_limit"`
	ReviewRoundLimit uint32                 `json:"review_round_limit"`
}

type architectureStageResult struct {
	SchemaVersion   string                   `json:"schema_version"`
	ResultType      string                   `json:"result_type"`
	Architecture    stageText                `json:"architecture"`
	DesignDecisions []string                 `json:"design_decisions"`
	Assumptions     []string                 `json:"assumptions"`
	Tasks           []architectureTaskResult `json:"tasks"`
}

// stageText accepts either one string or an ordered string array at the model
// adapter boundary. Both forms carry the same prose; the domain model keeps a
// single canonical string so this representation tolerance cannot change plan
// semantics.
type stageText string

func (value *stageText) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*value = stageText(single)
		return nil
	}
	var paragraphs []string
	if err := json.Unmarshal(data, &paragraphs); err != nil || len(paragraphs) == 0 || len(paragraphs) > 64 {
		return errInvalidValidationResult
	}
	for _, paragraph := range paragraphs {
		if strings.TrimSpace(paragraph) != paragraph || paragraph == "" {
			return errInvalidValidationResult
		}
	}
	*value = stageText(strings.Join(paragraphs, "\n"))
	return nil
}

type architectureReviewStageResult struct {
	SchemaVersion      string                     `json:"schema_version"`
	ResultType         string                     `json:"result_type"`
	Outcome            string                     `json:"outcome"`
	ReviewedPlanDigest kernel.Digest              `json:"reviewed_plan_sha256"`
	Coverage           architectureReviewCoverage `json:"coverage"`
	Findings           []architecturePlanFinding  `json:"findings"`
	UnverifiedClaims   stageStringList            `json:"unverified_claims"`
	Reasons            stageStringList            `json:"reasons"`
	Evidence           stageStringList            `json:"evidence"`
}

type architectureReviewCoverage struct {
	StoryCount                   uint32   `json:"story_count"`
	StoryAcceptanceCriteriaCount []uint32 `json:"story_acceptance_criterion_counts"`
	TaskCount                    uint32   `json:"task_count"`
	TaskAcceptanceCriteriaCount  []uint32 `json:"task_acceptance_criterion_counts"`
	PlanCheckSubjects            []string `json:"plan_check_subjects"`
}

type architecturePlanFinding struct {
	Subject        string          `json:"subject"`
	StoryIndex     *uint32         `json:"story_index,omitempty"`
	TaskIndex      *uint32         `json:"task_index,omitempty"`
	CriterionIndex *uint32         `json:"criterion_index,omitempty"`
	PlanSubject    string          `json:"plan_subject,omitempty"`
	Reasons        stageStringList `json:"reasons"`
	Evidence       stageStringList `json:"evidence"`
}

type architectureTaskReviewStageResult struct {
	SchemaVersion                 string                            `json:"schema_version"`
	ResultType                    string                            `json:"result_type"`
	Outcome                       string                            `json:"outcome"`
	ReviewedPlanDigest            kernel.Digest                     `json:"reviewed_plan_sha256"`
	ReviewedTaskIndex             uint32                            `json:"reviewed_task_index"`
	ReviewedTaskDigest            kernel.Digest                     `json:"reviewed_task_sha256"`
	ReviewedDependencyIndexes     []uint32                          `json:"reviewed_dependency_indexes"`
	DescriptionOutcome            string                            `json:"description_outcome"`
	DescriptionRequiresTaskChange bool                              `json:"description_requires_task_change"`
	CriterionChecks               []architectureTaskCriterionReview `json:"acceptance_criterion_checks"`
	VerifiedOperations            []string                          `json:"verified_operations"`
	UnverifiedPrescriptions       []string                          `json:"unverified_prescriptions"`
	Reasons                       stageStringList                   `json:"reasons"`
	Evidence                      stageStringList                   `json:"evidence"`
}

type architectureTaskCriterionReview struct {
	CriterionIndex     uint32          `json:"criterion_index"`
	Outcome            string          `json:"outcome"`
	RequiresTaskChange bool            `json:"requires_task_change"`
	Reasons            stageStringList `json:"reasons"`
	Evidence           stageStringList `json:"evidence"`
}

// stageStringList accepts either one string or a string array at the model
// adapter boundary and normalizes both forms to a list. The distinction is
// presentational; substantive validation below remains identical.
type stageStringList []string

func (values *stageStringList) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*values = stageStringList{single}
		return nil
	}
	var multiple []string
	if err := json.Unmarshal(data, &multiple); err != nil {
		return err
	}
	*values = multiple
	return nil
}

func decodeOrganizationalStageResult(output []byte, target any) error {
	marker := []byte(application.OrganizationalResultMarker)
	index := bytes.LastIndex(output, marker)
	if index < 0 || bytes.Count(output, marker) != 1 || index > 0 && output[index-1] != '\n' {
		return errInvalidValidationResult
	}
	payload := bytes.TrimSpace(output[index+len(marker):])
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return errInvalidValidationResult
	}
	trailing := bytes.TrimSpace(payload[decoder.InputOffset():])
	// Some native tool-call serializers emit one redundant root-closing brace
	// inside the finish message after producing an otherwise complete JSON
	// object. Accept only that unambiguous transport artifact; arbitrary text,
	// another JSON value, or more than one redundant delimiter still fails.
	if len(trailing) != 0 && !bytes.Equal(trailing, []byte("}")) {
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

var preAssignmentOperationalIdentityMarkers = []string{
	"::",
	"refs/heads/",
	"/Users/",
	".worktrees/",
	"model_profile_digest",
	"runtime_identity_digest",
	"execution_id",
	"fencing_epoch",
	"worktree_id",
	"workspace_id",
}

func containsPreAssignmentOperationalIdentity(values ...string) bool {
	return containsPreAssignmentOperationalIdentityExcept(nil, values...)
}

func containsPreAssignmentOperationalIdentityExcept(allowedActorFQNs []kernel.ActorFQN, values ...string) bool {
	allowed := make(map[kernel.ActorFQN]struct{}, len(allowedActorFQNs))
	for _, actor := range allowedActorFQNs {
		allowed[actor] = struct{}{}
	}
	for _, value := range values {
		if containsTekrooBranchIdentity(value) {
			return true
		}
		for _, marker := range preAssignmentOperationalIdentityMarkers {
			if marker == "::" {
				continue
			}
			if strings.Contains(value, marker) {
				return true
			}
		}
		for _, token := range operationalIdentityTokens(value) {
			if !strings.Contains(token, "::") {
				continue
			}
			actor, err := kernel.ParseActorFQN(token)
			if err != nil {
				// Syntax references such as the bare FQN separator "::" are
				// requirements, not operational identities. Only a token that
				// parses as an actor FQN can introduce an actor assignment.
				continue
			}
			if _, authorized := allowed[actor]; !authorized {
				return true
			}
		}
	}
	return false
}

func containsTekrooBranchIdentity(value string) bool {
	for _, token := range strings.FieldsFunc(value, func(character rune) bool {
		return !unicode.IsLetter(character) && !unicode.IsDigit(character) && !strings.ContainsRune("._-/:", character)
	}) {
		if strings.HasPrefix(token, "tekroo/") {
			return true
		}
	}
	return false
}

func operationalIdentityTokens(value string) []string {
	return strings.FieldsFunc(value, func(character rune) bool {
		return !unicode.IsLetter(character) && !unicode.IsDigit(character) && !strings.ContainsRune("._-:", character)
	})
}

func featureAuthorizedActorFQNs(feature organization.FeatureRequest) []kernel.ActorFQN {
	values := append([]string{feature.Input.Title, feature.Input.Description}, feature.Input.AcceptanceCriteria...)
	values = append(values, feature.Input.Constraints...)
	seen := make(map[kernel.ActorFQN]struct{})
	actors := make([]kernel.ActorFQN, 0)
	for _, value := range values {
		for _, token := range operationalIdentityTokens(value) {
			actor, err := kernel.ParseActorFQN(token)
			if err != nil {
				continue
			}
			if _, exists := seen[actor]; exists {
				continue
			}
			seen[actor] = struct{}{}
			actors = append(actors, actor)
		}
	}
	return actors
}

func parseRefinementStageResult(output []byte, allowedActorFQNs ...kernel.ActorFQN) (refinementStageResult, error) {
	var result refinementStageResult
	if decodeOrganizationalStageResult(output, &result) != nil || result.SchemaVersion != "1.0.0" || result.ResultType != "FEATURE_REFINEMENT" || result.AcceptanceDisposition != preserveSubmittedAcceptanceCriteria || !result.Priority.Valid() || !validStageStrings(result.ClarificationQuestions, false) || len(result.ClarificationQuestions) > 16 || containsPreAssignmentOperationalIdentityExcept(allowedActorFQNs, result.ClarificationQuestions...) {
		return refinementStageResult{}, organization.ErrInvalidFeature
	}
	return result, nil
}

func parseSpecificationStageResult(output []byte, allowedActorFQNs ...kernel.ActorFQN) (specificationStageResult, error) {
	var result specificationStageResult
	if decodeOrganizationalStageResult(output, &result) != nil || result.SchemaVersion != "1.0.0" || result.ResultType != "FEATURE_SPECIFICATION" || len(result.Stories) == 0 || len(result.Stories) > organization.MaximumFeatureStories || !validStageStrings(result.DesignConstraints, false) {
		return specificationStageResult{}, organization.ErrInvalidFeature
	}
	for _, story := range result.Stories {
		storyFields := append([]string{story.Title, story.Description}, story.AcceptanceCriteria...)
		if strings.TrimSpace(story.Title) == "" || len(story.Title) > 256 || strings.TrimSpace(story.Description) == "" || len(story.Description) > 64<<10 || !story.Priority.Valid() || !validStageStrings(story.AcceptanceCriteria, true) || len(story.AcceptanceCriteria) > 32 || containsPreAssignmentOperationalIdentityExcept(allowedActorFQNs, storyFields...) {
			return specificationStageResult{}, organization.ErrInvalidFeature
		}
	}
	if containsPreAssignmentOperationalIdentityExcept(allowedActorFQNs, result.DesignConstraints...) {
		return specificationStageResult{}, organization.ErrInvalidFeature
	}
	return result, nil
}

func parseArchitectureStageResult(output []byte, allowedActorFQNs ...kernel.ActorFQN) (architectureStageResult, error) {
	var result architectureStageResult
	if decodeOrganizationalStageResult(output, &result) != nil || result.SchemaVersion != "1.0.0" || result.ResultType != "FEATURE_PLAN" || strings.TrimSpace(string(result.Architecture)) == "" || len(result.Architecture) > 64<<10 || !validStageStrings(result.DesignDecisions, false) || !validStageStrings(result.Assumptions, false) || len(result.Tasks) == 0 || len(result.Tasks) > organization.MaximumFeatureTasks {
		return architectureStageResult{}, organization.ErrInvalidFeature
	}
	for index := range result.Tasks {
		task := &result.Tasks[index]
		// Risk is model-authored JSON, so normalize the spelling at this adapter
		// boundary before validating the canonical domain value. MEDIUM remains
		// the sole semantic synonym for MODERATE; unknown values still fail.
		task.Risk = organization.RiskLevel(strings.ToUpper(string(task.Risk)))
		if task.Risk == organization.RiskLevel("MEDIUM") {
			task.Risk = organization.RiskModerate
		}
		if task.StoryIndex >= organization.MaximumFeatureStories || strings.TrimSpace(task.Title) == "" || len(task.Title) > 256 || strings.TrimSpace(task.Description) == "" || len(task.Description) > 64<<10 || !validStageStrings(task.AcceptanceCriteria, true) || containsPreAssignmentOperationalIdentityExcept(allowedActorFQNs, append([]string{task.Title, task.Description}, task.AcceptanceCriteria...)...) || !task.Purpose.Valid() || task.Complexity == 0 || task.Complexity > 10 || !task.Risk.Valid() || task.AttemptLimit == 0 || task.AttemptLimit > 16 || task.ReviewRoundLimit == 0 || task.ReviewRoundLimit > 8 {
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

func parseArchitectureReviewStageResult(output []byte) (architectureReviewStageResult, error) {
	var result architectureReviewStageResult
	if decodeOrganizationalStageResult(output, &result) != nil || result.SchemaVersion != "1.0.0" || result.ResultType != "FEATURE_PLAN_REVIEW" || result.Outcome != "PASS" && result.Outcome != "FAIL" || !result.ReviewedPlanDigest.Valid() || result.Coverage.StoryCount == 0 || result.Coverage.StoryCount > organization.MaximumFeatureStories || len(result.Coverage.StoryAcceptanceCriteriaCount) != int(result.Coverage.StoryCount) || result.Coverage.TaskCount == 0 || result.Coverage.TaskCount > organization.MaximumFeatureTasks || len(result.Coverage.TaskAcceptanceCriteriaCount) != int(result.Coverage.TaskCount) || len(result.Coverage.PlanCheckSubjects) != len(requiredArchitecturePlanCheckSubjects) || result.Findings == nil || len(result.Findings) > 64 || result.UnverifiedClaims == nil || !validStageStrings(result.UnverifiedClaims, false) || !validStageStrings(result.Reasons, true) || !validStageStrings(result.Evidence, true) || len(result.UnverifiedClaims) > 64 || len(result.Reasons) > 32 || len(result.Evidence) > 64 {
		return architectureReviewStageResult{}, organization.ErrInvalidFeature
	}
	for _, count := range append(append([]uint32(nil), result.Coverage.StoryAcceptanceCriteriaCount...), result.Coverage.TaskAcceptanceCriteriaCount...) {
		if count == 0 || count > 64 {
			return architectureReviewStageResult{}, organization.ErrInvalidFeature
		}
	}
	for _, finding := range result.Findings {
		if !validArchitecturePlanFinding(finding) {
			return architectureReviewStageResult{}, organization.ErrInvalidFeature
		}
	}
	return result, nil
}

var requiredArchitecturePlanCheckSubjects = []string{
	"STATE_OWNERSHIP",
	"CONCURRENCY_ATOMICITY",
	"DEPENDENCY_DIRECTION",
	"INTERFACE_COMPATIBILITY",
	"AUTHORIZATION_IDENTITY",
	"PARTIAL_FAILURE",
}

func validArchitecturePlanFinding(finding architecturePlanFinding) bool {
	if !validStageStrings([]string(finding.Reasons), true) || !validStageStrings([]string(finding.Evidence), true) || len(finding.Reasons) > 16 || len(finding.Evidence) > 32 {
		return false
	}
	switch finding.Subject {
	case "STORY_DESCRIPTION":
		return finding.StoryIndex != nil && finding.TaskIndex == nil && finding.CriterionIndex == nil && finding.PlanSubject == ""
	case "STORY_ACCEPTANCE_CRITERION":
		return finding.StoryIndex != nil && finding.TaskIndex == nil && finding.CriterionIndex != nil && finding.PlanSubject == ""
	case "TASK_DESCRIPTION":
		return finding.StoryIndex == nil && finding.TaskIndex != nil && finding.CriterionIndex == nil && finding.PlanSubject == ""
	case "TASK_ACCEPTANCE_CRITERION":
		return finding.StoryIndex == nil && finding.TaskIndex != nil && finding.CriterionIndex != nil && finding.PlanSubject == ""
	case "PLAN_CHECK":
		return finding.StoryIndex == nil && finding.TaskIndex == nil && finding.CriterionIndex == nil && finding.PlanSubject != ""
	default:
		return false
	}
}

func validArchitectureReviewCoverage(result architectureReviewStageResult, stories []organization.PlannedStory, tasks []architectureTaskResult) bool {
	coverage := result.Coverage
	if coverage.StoryCount != uint32(len(stories)) || len(coverage.StoryAcceptanceCriteriaCount) != len(stories) || coverage.TaskCount != uint32(len(tasks)) || len(coverage.TaskAcceptanceCriteriaCount) != len(tasks) || len(coverage.PlanCheckSubjects) != len(requiredArchitecturePlanCheckSubjects) {
		return false
	}
	for index, story := range stories {
		if coverage.StoryAcceptanceCriteriaCount[index] != uint32(len(story.AcceptanceCriteria)) {
			return false
		}
	}
	for index, task := range tasks {
		if coverage.TaskAcceptanceCriteriaCount[index] != uint32(len(task.AcceptanceCriteria)) {
			return false
		}
	}
	for index, subject := range requiredArchitecturePlanCheckSubjects {
		if coverage.PlanCheckSubjects[index] != subject {
			return false
		}
	}
	seenFindings := make(map[string]struct{}, len(result.Findings))
	for _, finding := range result.Findings {
		if !validArchitecturePlanFinding(finding) {
			return false
		}
		key := finding.Subject + ":" + finding.PlanSubject
		switch finding.Subject {
		case "STORY_DESCRIPTION":
			if *finding.StoryIndex >= uint32(len(stories)) {
				return false
			}
			key += ":" + strconv.FormatUint(uint64(*finding.StoryIndex), 10)
		case "STORY_ACCEPTANCE_CRITERION":
			if *finding.StoryIndex >= uint32(len(stories)) || *finding.CriterionIndex >= uint32(len(stories[*finding.StoryIndex].AcceptanceCriteria)) {
				return false
			}
			key += ":" + strconv.FormatUint(uint64(*finding.StoryIndex), 10) + ":" + strconv.FormatUint(uint64(*finding.CriterionIndex), 10)
		case "TASK_DESCRIPTION":
			if *finding.TaskIndex >= uint32(len(tasks)) {
				return false
			}
			key += ":" + strconv.FormatUint(uint64(*finding.TaskIndex), 10)
		case "TASK_ACCEPTANCE_CRITERION":
			if *finding.TaskIndex >= uint32(len(tasks)) || *finding.CriterionIndex >= uint32(len(tasks[*finding.TaskIndex].AcceptanceCriteria)) {
				return false
			}
			key += ":" + strconv.FormatUint(uint64(*finding.TaskIndex), 10) + ":" + strconv.FormatUint(uint64(*finding.CriterionIndex), 10)
		case "PLAN_CHECK":
			found := false
			for _, subject := range requiredArchitecturePlanCheckSubjects {
				found = found || finding.PlanSubject == subject
			}
			if !found {
				return false
			}
		}
		if _, duplicate := seenFindings[key]; duplicate {
			return false
		}
		seenFindings[key] = struct{}{}
	}
	allSupported := len(result.Findings) == 0 && len(result.UnverifiedClaims) == 0
	if result.Outcome == "PASS" {
		return allSupported
	}
	return !allSupported
}

func parseArchitectureTaskReviewStageResult(output []byte) (architectureTaskReviewStageResult, error) {
	var result architectureTaskReviewStageResult
	if decodeOrganizationalStageResult(output, &result) != nil || result.SchemaVersion != "1.0.0" || result.ResultType != "FEATURE_PLAN_TASK_REVIEW" || result.Outcome != "PASS" && result.Outcome != "FAIL" || !result.ReviewedPlanDigest.Valid() || !result.ReviewedTaskDigest.Valid() || !validArchitectureReviewIndexes(result.ReviewedDependencyIndexes) || !validArchitectureReviewOutcome(result.DescriptionOutcome) || len(result.CriterionChecks) == 0 || len(result.CriterionChecks) > 32 || !validStageStrings(result.VerifiedOperations, true) || !validStageStrings(result.UnverifiedPrescriptions, false) || !validStageStrings([]string(result.Reasons), true) || !validStageStrings([]string(result.Evidence), true) || len(result.VerifiedOperations) > 64 || len(result.UnverifiedPrescriptions) > 64 || len(result.Reasons) > 32 || len(result.Evidence) > 64 {
		return architectureTaskReviewStageResult{}, organization.ErrInvalidFeature
	}
	for _, check := range result.CriterionChecks {
		if !validArchitectureReviewOutcome(check.Outcome) || !validStageStrings([]string(check.Reasons), true) || !validStageStrings([]string(check.Evidence), true) || len(check.Reasons) > 16 || len(check.Evidence) > 32 {
			return architectureTaskReviewStageResult{}, organization.ErrInvalidFeature
		}
	}
	return result, nil
}

func validArchitectureReviewIndexes(values []uint32) bool {
	if values == nil || len(values) > organization.MaximumFeatureTasks {
		return false
	}
	seen := make(map[uint32]struct{}, len(values))
	for _, value := range values {
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validArchitectureReviewOutcome(value string) bool {
	return value == "SUPPORTED" || value == "UNSUPPORTED"
}

func normalizeArchitectureReviewOutcome(value string) string {
	switch strings.ToUpper(value) {
	case "PASS", "SUPPORTED":
		return "SUPPORTED"
	case "FAIL", "UNSUPPORTED":
		return "UNSUPPORTED"
	default:
		return value
	}
}
