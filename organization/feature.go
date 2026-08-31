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
	FeaturePlanned               FeatureStatus = "PLANNED"
	FeatureApproved              FeatureStatus = "APPROVED"
	FeatureCancelled             FeatureStatus = "CANCELLED"
)

func (status FeatureStatus) Valid() bool {
	switch status {
	case FeatureSubmitted, FeatureClarificationRequired, FeatureReadyForPlanning, FeaturePlanned, FeatureApproved, FeatureCancelled:
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
	SchemaVersion     string              `json:"schema_version"`
	ID                kernel.UUIDv7       `json:"id"`
	Revision          uint64              `json:"revision"`
	SubmittedBy       kernel.PrincipalRef `json:"submitted_by"`
	Input             FeatureRequestInput `json:"input"`
	Status            FeatureStatus       `json:"status"`
	OperatorActor     kernel.ActorFQN     `json:"operator_actor"`
	ProductOwnerActor kernel.ActorFQN     `json:"product_owner_actor"`
	InitialMessageID  kernel.UUIDv7       `json:"initial_message_id"`
	BudgetAccountID   kernel.UUIDv7       `json:"budget_account_id"`
	LifecycleEpoch    uint64              `json:"lifecycle_epoch"`
	ScopeRevision     uint64              `json:"scope_revision"`
	CreatedAt         time.Time           `json:"created_at"`
	UpdatedAt         time.Time           `json:"updated_at"`
	Plan              *FeaturePlan        `json:"plan,omitempty"`
}

func (feature FeatureRequest) Validate() error {
	if feature.SchemaVersion != FeatureSchemaVersion || !feature.ID.Valid() || feature.Revision == 0 || !feature.SubmittedBy.Valid() || feature.SubmittedBy.Kind != kernel.PrincipalHuman || feature.Input.Validate() != nil || !feature.Status.Valid() || !feature.OperatorActor.Valid() || !feature.ProductOwnerActor.Valid() || feature.OperatorActor == feature.ProductOwnerActor || !feature.InitialMessageID.Valid() || !feature.BudgetAccountID.Valid() || feature.LifecycleEpoch == 0 || feature.ScopeRevision == 0 || feature.CreatedAt.IsZero() || feature.UpdatedAt.Before(feature.CreatedAt) {
		return ErrInvalidFeature
	}
	if feature.Plan != nil && feature.Plan.Validate(feature) != nil {
		return ErrInvalidFeature
	}
	return nil
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

type PlannedTask struct {
	ID                 kernel.UUIDv7   `json:"id"`
	StoryID            kernel.UUIDv7   `json:"story_id"`
	Title              string          `json:"title"`
	Description        string          `json:"description"`
	AcceptanceCriteria []string        `json:"acceptance_criteria"`
	DependsOn          []kernel.UUIDv7 `json:"depends_on"`
	Owner              kernel.ActorFQN `json:"owner"`
	ModelProfile       kernel.Digest   `json:"model_profile_digest"`
	Complexity         uint8           `json:"complexity"`
	Risk               RiskLevel       `json:"risk"`
	CriticalPath       bool            `json:"critical_path"`
	AttemptLimit       uint32          `json:"attempt_limit"`
	ReviewRoundLimit   uint32          `json:"review_round_limit"`
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
		if !story.ID.Valid() || story.Title == "" || story.Description == "" || len(story.AcceptanceCriteria) == 0 || !story.Priority.Valid() {
			return ErrInvalidFeature
		}
		if _, exists := stories[story.ID]; exists {
			return ErrInvalidFeature
		}
		stories[story.ID] = struct{}{}
	}
	tasks := make(map[kernel.UUIDv7]PlannedTask, len(plan.Tasks))
	for _, task := range plan.Tasks {
		if !task.ID.Valid() || task.Title == "" || task.Description == "" || len(task.AcceptanceCriteria) == 0 || !task.Owner.Valid() || !task.ModelProfile.Valid() || task.Complexity == 0 || task.Complexity > 10 || !task.Risk.Valid() || task.AttemptLimit == 0 || task.ReviewRoundLimit == 0 {
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
}
