package organization

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

const (
	FeatureSchemaVersion  = "1.0.0"
	MaximumFeatureStories = 32
	MaximumFeatureTasks   = 128
)

var (
	ErrInvalidFeature          = errors.New("invalid feature request or plan")
	ErrFeatureConflict         = errors.New("feature request conflict")
	ErrFeatureNotFound         = errors.New("feature request not found")
	ErrFeatureRevisionConflict = errors.New("feature request revision conflict")
	featureKeyPattern          = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$`)
)

type FeaturePriority string

const (
	PriorityLow      FeaturePriority = "LOW"
	PriorityNormal   FeaturePriority = "NORMAL"
	PriorityHigh     FeaturePriority = "HIGH"
	PriorityCritical FeaturePriority = "CRITICAL"
)

func (priority FeaturePriority) Valid() bool {
	switch priority {
	case PriorityLow, PriorityNormal, PriorityHigh, PriorityCritical:
		return true
	default:
		return false
	}
}

type FeatureStatus string

const (
	FeatureSubmitted             FeatureStatus = "SUBMITTED"
	FeatureClarificationRequired FeatureStatus = "CLARIFICATION_REQUIRED"
	FeatureReadyForPlanning      FeatureStatus = "READY_FOR_PLANNING"
	FeatureSpecified             FeatureStatus = "SPECIFIED"
	FeaturePlanned               FeatureStatus = "PLANNED"
	FeatureApproved              FeatureStatus = "APPROVED"
	FeatureAwaitingAcceptance    FeatureStatus = "AWAITING_ACCEPTANCE"
	FeatureAccepted              FeatureStatus = "ACCEPTED"
	FeatureCancelled             FeatureStatus = "CANCELLED"
)

func (status FeatureStatus) Valid() bool {
	switch status {
	case FeatureSubmitted, FeatureClarificationRequired, FeatureReadyForPlanning, FeatureSpecified, FeaturePlanned, FeatureApproved, FeatureAwaitingAcceptance, FeatureAccepted, FeatureCancelled:
		return true
	default:
		return false
	}
}

type FeatureRequestInput struct {
	IdempotencyKey     string          `json:"idempotency_key"`
	Team               string          `json:"team"`
	Title              string          `json:"title"`
	Description        string          `json:"description"`
	AcceptanceCriteria []string        `json:"acceptance_criteria"`
	Priority           FeaturePriority `json:"priority"`
	Constraints        []string        `json:"constraints"`
	Repository         string          `json:"repository"`
	WorkspaceID        string          `json:"workspace_id"`
	MaximumStories     uint32          `json:"maximum_stories"`
	MaximumTasks       uint32          `json:"maximum_tasks"`
	MaximumHops        uint32          `json:"maximum_hops"`
}

func (input FeatureRequestInput) Validate() error {
	if !featureKeyPattern.MatchString(input.IdempotencyKey) || !namePattern.MatchString(input.Team) || input.Title == "" || len(input.Title) > 256 || input.Description == "" || len(input.Description) > 64<<10 || !input.Priority.Valid() || input.Repository == "" || len(input.Repository) > 4096 || input.WorkspaceID == "" || len(input.WorkspaceID) > 1024 || input.MaximumStories == 0 || input.MaximumStories > MaximumFeatureStories || input.MaximumTasks == 0 || input.MaximumTasks > MaximumFeatureTasks || input.MaximumHops < 4 || input.MaximumHops > MaximumOrganizationalHops || len(input.AcceptanceCriteria) == 0 || len(input.AcceptanceCriteria) > 32 || len(input.Constraints) > 32 {
		return ErrInvalidFeature
	}
	for _, item := range append(append([]string(nil), input.AcceptanceCriteria...), input.Constraints...) {
		if item == "" || len(item) > 4096 {
			return ErrInvalidFeature
		}
	}
	return nil
}

type FeatureRequest struct {
	SchemaVersion     string                   `json:"schema_version"`
	ID                kernel.UUIDv7            `json:"id"`
	Revision          uint64                   `json:"revision"`
	SubmittedBy       kernel.PrincipalRef      `json:"submitted_by"`
	Input             FeatureRequestInput      `json:"input"`
	Status            FeatureStatus            `json:"status"`
	OperatorActor     kernel.ActorFQN          `json:"operator_actor"`
	ProductOwnerActor kernel.ActorFQN          `json:"product_owner_actor"`
	InitialMessageID  kernel.UUIDv7            `json:"initial_message_id"`
	BudgetAccountID   kernel.UUIDv7            `json:"budget_account_id"`
	LifecycleEpoch    uint64                   `json:"lifecycle_epoch"`
	ScopeRevision     uint64                   `json:"scope_revision"`
	CreatedAt         time.Time                `json:"created_at"`
	UpdatedAt         time.Time                `json:"updated_at"`
	LastMessageID     kernel.UUIDv7            `json:"last_message_id"`
	LastStepID        kernel.UUIDv7            `json:"last_step_id"`
	LastHop           uint32                   `json:"last_hop"`
	Refinement        *FeatureRefinement       `json:"refinement,omitempty"`
	Clarification     *FeatureClarification    `json:"clarification,omitempty"`
	Specification     *FeatureSpecification    `json:"specification,omitempty"`
	Plan              *FeaturePlan             `json:"plan,omitempty"`
	PlanSupersession  *FeaturePlanSupersession `json:"plan_supersession,omitempty"`
	Acceptance        *FeatureAcceptance       `json:"acceptance,omitempty"`
}

func (feature FeatureRequest) Validate() error {
	if feature.SchemaVersion != FeatureSchemaVersion || !feature.ID.Valid() || feature.Revision == 0 || !feature.SubmittedBy.Valid() || feature.SubmittedBy.Kind != kernel.PrincipalHuman || feature.Input.Validate() != nil || !feature.Status.Valid() || !feature.OperatorActor.Valid() || !feature.ProductOwnerActor.Valid() || feature.OperatorActor == feature.ProductOwnerActor || !feature.InitialMessageID.Valid() || !feature.LastMessageID.Valid() || !feature.LastStepID.Valid() || feature.LastHop == 0 || feature.LastHop > feature.Input.MaximumHops || !feature.BudgetAccountID.Valid() || feature.LifecycleEpoch == 0 || feature.ScopeRevision == 0 || feature.CreatedAt.IsZero() || feature.UpdatedAt.Before(feature.CreatedAt) {
		return ErrInvalidFeature
	}
	if feature.Plan != nil && feature.Plan.Validate(feature) != nil {
		return ErrInvalidFeature
	}
	if feature.PlanSupersession != nil && feature.PlanSupersession.Validate(feature) != nil {
		return ErrInvalidFeature
	}
	if feature.Status == FeatureSpecified && feature.Plan != nil && feature.PlanSupersession == nil {
		return ErrInvalidFeature
	}
	if feature.PlanSupersession != nil && feature.Status != FeatureSpecified {
		return ErrInvalidFeature
	}
	if feature.Refinement != nil && feature.Refinement.Validate(feature) != nil {
		return ErrInvalidFeature
	}
	if feature.Clarification != nil && feature.Clarification.Validate(feature) != nil {
		return ErrInvalidFeature
	}
	if feature.Status == FeatureClarificationRequired && (feature.Refinement == nil || len(feature.Refinement.ClarificationQuestions) == 0 || feature.Clarification != nil) {
		return ErrInvalidFeature
	}
	if feature.Refinement != nil && len(feature.Refinement.ClarificationQuestions) > 0 && feature.Status != FeatureClarificationRequired && feature.Clarification == nil {
		return ErrInvalidFeature
	}
	if feature.Specification != nil && feature.Specification.Validate(feature) != nil {
		return ErrInvalidFeature
	}
	if feature.Acceptance != nil && feature.Acceptance.Validate(feature) != nil {
		return ErrInvalidFeature
	}
	return nil
}

// FeaturePlanSupersession records an operator-authorized correction of an
// already materialized plan. The superseded plan remains on the feature while
// the replacement architecture round runs, preserving its exact projection
// and the immutable task/evidence history that was derived from it.
type FeaturePlanSupersession struct {
	PlanVersion       uint64               `json:"plan_version"`
	PlanDigest        kernel.Digest        `json:"plan_digest"`
	ArchitectureRound uint32               `json:"architecture_round"`
	RequestedBy       kernel.PrincipalRef  `json:"requested_by"`
	Reason            string               `json:"reason"`
	EvidenceRefs      []kernel.EvidenceRef `json:"evidence_refs"`
	DeadlineAt        time.Time            `json:"deadline_at"`
	RequestedAt       time.Time            `json:"requested_at"`
	IdempotencyKey    string               `json:"idempotency_key"`
}

func (supersession FeaturePlanSupersession) Validate(feature FeatureRequest) error {
	if feature.Plan == nil || supersession.PlanVersion == 0 || supersession.PlanVersion != feature.Plan.Version || !supersession.PlanDigest.Valid() || supersession.ArchitectureRound == 0 || supersession.RequestedBy.Kind != kernel.PrincipalHuman || !supersession.RequestedBy.Valid() || supersession.Reason == "" || len(supersession.Reason) > 4096 || len(supersession.EvidenceRefs) == 0 || len(supersession.EvidenceRefs) > 64 || supersession.DeadlineAt.IsZero() || !supersession.DeadlineAt.After(supersession.RequestedAt) || supersession.RequestedAt.Before(feature.CreatedAt) || supersession.IdempotencyKey == "" || len(supersession.IdempotencyKey) > 256 {
		return ErrInvalidFeature
	}
	seen := make(map[kernel.UUIDv7]struct{}, len(supersession.EvidenceRefs))
	for _, evidence := range supersession.EvidenceRefs {
		if !evidence.EvidenceID.Valid() || !evidence.SHA256.Valid() {
			return ErrInvalidFeature
		}
		if _, duplicate := seen[evidence.EvidenceID]; duplicate {
			return ErrInvalidFeature
		}
		seen[evidence.EvidenceID] = struct{}{}
	}
	return nil
}

type FeatureAcceptance struct {
	RecommendedBy      kernel.ActorFQN       `json:"recommended_by"`
	RecommendationRun  kernel.ExecutionTuple `json:"recommendation_execution"`
	Recommendation     string                `json:"recommendation"`
	RecommendationHash kernel.Digest         `json:"recommendation_digest"`
	AcceptedBy         *kernel.PrincipalRef  `json:"accepted_by,omitempty"`
	StoryIDs           []kernel.UUIDv7       `json:"story_ids"`
	ReleasePlanIDs     []kernel.UUIDv7       `json:"release_plan_ids,omitempty"`
	RecordedAt         time.Time             `json:"recorded_at"`
}

func (acceptance FeatureAcceptance) Validate(feature FeatureRequest) error {
	if !acceptance.RecommendedBy.Valid() || !acceptance.RecommendationRun.Valid() || acceptance.Recommendation != "PASS" || !acceptance.RecommendationHash.Valid() || len(acceptance.StoryIDs) == 0 || acceptance.RecordedAt.Before(feature.CreatedAt) {
		return ErrInvalidFeature
	}
	if acceptance.AcceptedBy != nil && (acceptance.AcceptedBy.Kind != kernel.PrincipalHuman || !acceptance.AcceptedBy.Valid() || len(acceptance.ReleasePlanIDs) != len(acceptance.StoryIDs)) {
		return ErrInvalidFeature
	}
	if acceptance.AcceptedBy == nil && len(acceptance.ReleasePlanIDs) != 0 {
		return ErrInvalidFeature
	}
	seen := make(map[kernel.UUIDv7]struct{}, len(acceptance.StoryIDs))
	for _, id := range acceptance.StoryIDs {
		if !id.Valid() {
			return ErrInvalidFeature
		}
		if _, duplicate := seen[id]; duplicate {
			return ErrInvalidFeature
		}
		seen[id] = struct{}{}
	}
	for _, id := range acceptance.ReleasePlanIDs {
		if !id.Valid() {
			return ErrInvalidFeature
		}
	}
	return nil
}

type FeatureRefinement struct {
	PreparedBy             kernel.ActorFQN       `json:"prepared_by"`
	PreparedExecution      kernel.ExecutionTuple `json:"prepared_execution"`
	AcceptanceCriteria     []string              `json:"acceptance_criteria"`
	ClarificationQuestions []string              `json:"clarification_questions"`
	Priority               FeaturePriority       `json:"priority"`
	PreparedAt             time.Time             `json:"prepared_at"`
}

type FeatureClarificationAnswer struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

type FeatureClarification struct {
	RespondedBy kernel.PrincipalRef          `json:"responded_by"`
	Answers     []FeatureClarificationAnswer `json:"answers"`
	RespondedAt time.Time                    `json:"responded_at"`
}

func (clarification FeatureClarification) Validate(feature FeatureRequest) error {
	if feature.Refinement == nil || clarification.RespondedBy != feature.SubmittedBy || clarification.RespondedBy.Kind != kernel.PrincipalHuman || clarification.RespondedAt.Before(feature.Refinement.PreparedAt) || len(clarification.Answers) != len(feature.Refinement.ClarificationQuestions) {
		return ErrInvalidFeature
	}
	for index, answer := range clarification.Answers {
		if answer.Question != feature.Refinement.ClarificationQuestions[index] || answer.Answer == "" || len(answer.Answer) > 4096 {
			return ErrInvalidFeature
		}
	}
	return nil
}

type FeatureClarificationResponseInput struct {
	ExpectedRevision uint64   `json:"expected_revision"`
	Answers          []string `json:"answers"`
}

func (input FeatureClarificationResponseInput) Valid() bool {
	return input.ExpectedRevision > 0 && len(input.Answers) > 0 && len(input.Answers) <= 16 && validBoundedStrings(input.Answers, 4096) == nil
}

func (refinement FeatureRefinement) Validate(feature FeatureRequest) error {
	if refinement.PreparedBy != feature.ProductOwnerActor || !refinement.PreparedExecution.Valid() || len(refinement.AcceptanceCriteria) == 0 || len(refinement.AcceptanceCriteria) > 32 || len(refinement.ClarificationQuestions) > 16 || !refinement.Priority.Valid() || refinement.PreparedAt.Before(feature.CreatedAt) {
		return ErrInvalidFeature
	}
	return validBoundedStrings(append(append([]string(nil), refinement.AcceptanceCriteria...), refinement.ClarificationQuestions...), 4096)
}

type FeatureSpecification struct {
	PreparedBy        kernel.ActorFQN       `json:"prepared_by"`
	PreparedExecution kernel.ExecutionTuple `json:"prepared_execution"`
	Stories           []PlannedStory        `json:"stories"`
	DesignConstraints []string              `json:"design_constraints"`
	PreparedAt        time.Time             `json:"prepared_at"`
}

func (specification FeatureSpecification) Validate(feature FeatureRequest) error {
	if !specification.PreparedBy.Valid() || !specification.PreparedExecution.Valid() || len(specification.Stories) == 0 || len(specification.Stories) > int(feature.Input.MaximumStories) || len(specification.DesignConstraints) > 64 || specification.PreparedAt.Before(feature.CreatedAt) {
		return ErrInvalidFeature
	}
	seen := make(map[kernel.UUIDv7]struct{}, len(specification.Stories))
	for _, story := range specification.Stories {
		if !story.Valid() {
			return ErrInvalidFeature
		}
		if _, duplicate := seen[story.ID]; duplicate {
			return ErrInvalidFeature
		}
		seen[story.ID] = struct{}{}
	}
	return validBoundedStrings(specification.DesignConstraints, 4096)
}

type RiskLevel string

const (
	RiskLow      RiskLevel = "LOW"
	RiskModerate RiskLevel = "MODERATE"
	RiskHigh     RiskLevel = "HIGH"
	RiskCritical RiskLevel = "CRITICAL"
)

func (risk RiskLevel) Valid() bool {
	switch risk {
	case RiskLow, RiskModerate, RiskHigh, RiskCritical:
		return true
	default:
		return false
	}
}

type PlannedStory struct {
	ID                 kernel.UUIDv7   `json:"id"`
	Title              string          `json:"title"`
	Description        string          `json:"description"`
	AcceptanceCriteria []string        `json:"acceptance_criteria"`
	Priority           FeaturePriority `json:"priority"`
	SupersedesStoryID  *kernel.UUIDv7  `json:"supersedes_story_id,omitempty"`
}

func (story PlannedStory) Valid() bool {
	return story.ID.Valid() && story.Title != "" && len(story.Title) <= 256 && story.Description != "" && len(story.Description) <= 64<<10 && len(story.AcceptanceCriteria) > 0 && len(story.AcceptanceCriteria) <= 32 && story.Priority.Valid() && (story.SupersedesStoryID == nil || story.SupersedesStoryID.Valid()) && validBoundedStrings(story.AcceptanceCriteria, 4096) == nil
}

type PlannedTask struct {
	ID                 kernel.UUIDv7        `json:"id"`
	StoryID            kernel.UUIDv7        `json:"story_id"`
	Title              string               `json:"title"`
	Description        string               `json:"description"`
	AcceptanceCriteria []string             `json:"acceptance_criteria"`
	DependsOn          []kernel.UUIDv7      `json:"depends_on"`
	Validates          []kernel.UUIDv7      `json:"validates,omitempty"`
	Owner              kernel.ActorFQN      `json:"owner"`
	ModelProfile       kernel.Digest        `json:"model_profile_digest"`
	DecisionRoute      kernel.DecisionRoute `json:"decision_route"`
	Purpose            kernel.WorkPurpose   `json:"purpose"`
	Complexity         uint8                `json:"complexity"`
	Risk               RiskLevel            `json:"risk"`
	CriticalPath       bool                 `json:"critical_path"`
	AttemptLimit       uint32               `json:"attempt_limit"`
	ReviewRoundLimit   uint32               `json:"review_round_limit"`
}

type FeaturePlan struct {
	Version           uint64                `json:"version"`
	PreparedBy        kernel.ActorFQN       `json:"prepared_by"`
	PreparedExecution kernel.ExecutionTuple `json:"prepared_execution"`
	Architecture      string                `json:"architecture"`
	DesignDecisions   []string              `json:"design_decisions"`
	Assumptions       []string              `json:"assumptions"`
	Stories           []PlannedStory        `json:"stories"`
	Tasks             []PlannedTask         `json:"tasks"`
	CreatedAt         time.Time             `json:"created_at"`
}

func (plan FeaturePlan) Validate(feature FeatureRequest) error {
	if plan.Version == 0 || !plan.PreparedBy.Valid() || !plan.PreparedExecution.Valid() || plan.Architecture == "" || len(plan.Architecture) > 64<<10 || plan.CreatedAt.Before(feature.CreatedAt) || len(plan.Stories) == 0 || len(plan.Stories) > int(feature.Input.MaximumStories) || len(plan.Tasks) == 0 || len(plan.Tasks) > int(feature.Input.MaximumTasks) || len(plan.DesignDecisions) > 64 || len(plan.Assumptions) > 64 {
		return ErrInvalidFeature
	}
	stories := make(map[kernel.UUIDv7]struct{}, len(plan.Stories))
	for _, story := range plan.Stories {
		if !story.Valid() {
			return ErrInvalidFeature
		}
		if _, exists := stories[story.ID]; exists {
			return ErrInvalidFeature
		}
		stories[story.ID] = struct{}{}
	}
	tasks := make(map[kernel.UUIDv7]PlannedTask, len(plan.Tasks))
	for _, task := range plan.Tasks {
		if !task.ID.Valid() || task.Title == "" || task.Description == "" || len(task.AcceptanceCriteria) == 0 || !task.Owner.Valid() || !task.ModelProfile.Valid() || !task.DecisionRoute.ModelExecutable() || !task.Purpose.Valid() || task.Complexity == 0 || task.Complexity > 10 || !task.Risk.Valid() || task.AttemptLimit == 0 || task.ReviewRoundLimit == 0 {
			return ErrInvalidFeature
		}
		if _, found := stories[task.StoryID]; !found {
			return ErrInvalidFeature
		}
		if _, exists := tasks[task.ID]; exists {
			return ErrInvalidFeature
		}
		tasks[task.ID] = task
	}
	validationCoverage := make(map[kernel.UUIDv7]uint32, len(tasks))
	for _, task := range plan.Tasks {
		dependencies := make(map[kernel.UUIDv7]struct{}, len(task.DependsOn))
		for _, dependency := range task.DependsOn {
			if _, duplicate := dependencies[dependency]; duplicate {
				return ErrInvalidFeature
			}
			dependencies[dependency] = struct{}{}
		}
		validated := make(map[kernel.UUIDv7]struct{}, len(task.Validates))
		for _, targetID := range task.Validates {
			target, found := tasks[targetID]
			_, dependency := dependencies[targetID]
			if !found || !dependency || targetID == task.ID || target.Owner == task.Owner {
				return ErrInvalidFeature
			}
			if _, duplicate := validated[targetID]; duplicate {
				return ErrInvalidFeature
			}
			validated[targetID] = struct{}{}
			validationCoverage[targetID]++
		}
		validationPurpose := task.Purpose == kernel.PurposeValidation || task.Purpose == kernel.PurposeReview
		if validationPurpose != (len(task.Validates) > 0) {
			return ErrInvalidFeature
		}
		if validationPurpose {
			for targetID := range validated {
				if task.AttemptLimit < tasks[targetID].ReviewRoundLimit+1 {
					return ErrInvalidFeature
				}
			}
		}
	}
	for _, task := range plan.Tasks {
		if task.Purpose == kernel.PurposeImplementation || task.Purpose == kernel.PurposeRepair {
			if validationCoverage[task.ID] == 0 {
				return ErrInvalidFeature
			}
		}
	}
	visiting := make(map[kernel.UUIDv7]bool, len(tasks))
	visited := make(map[kernel.UUIDv7]bool, len(tasks))
	var visit func(kernel.UUIDv7) bool
	visit = func(id kernel.UUIDv7) bool {
		if visiting[id] {
			return false
		}
		if visited[id] {
			return true
		}
		visiting[id] = true
		for _, parent := range tasks[id].DependsOn {
			if parent == id {
				return false
			}
			if _, found := tasks[parent]; !found || !visit(parent) {
				return false
			}
		}
		visiting[id] = false
		visited[id] = true
		return true
	}
	for id := range tasks {
		if !visit(id) {
			return ErrInvalidFeature
		}
	}
	return nil
}

type FeatureStore interface {
	CreateFeature(context.Context, FeatureRequest, OrganizationalMessage) (FeatureRequest, bool, error)
	LoadFeature(context.Context, kernel.UUIDv7) (FeatureRequest, bool, error)
	LoadFeatureByIdempotencyKey(context.Context, kernel.PrincipalRef, string) (FeatureRequest, bool, error)
	ApplyFeaturePlan(context.Context, kernel.UUIDv7, uint64, FeaturePlan, time.Time) (FeatureRequest, error)
	AdvanceFeature(context.Context, FeatureRequest, uint64, *OrganizationalMessage) (FeatureRequest, error)
}

func validBoundedStrings(values []string, maximum int) error {
	for _, value := range values {
		if value == "" || len(value) > maximum {
			return ErrInvalidFeature
		}
	}
	return nil
}
