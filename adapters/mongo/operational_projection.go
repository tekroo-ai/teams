package mongo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

var (
	ErrProjectionGap             = errors.New("operational projection revision gap")
	ErrProjectionConflict        = errors.New("operational projection revision conflict")
	ErrProjectionRebuildMismatch = errors.New("operational projection rebuild mismatch")
)

const operationalProjectorName = "teams-operational-projections-v1"

type ProjectionStatusSummary struct {
	State       string          `json:"state"`
	EventID     *kernel.UUIDv7  `json:"event_id"`
	EvidenceIDs []kernel.UUIDv7 `json:"evidence_ids"`
}

type StoryProjection struct {
	Kind                   string                  `json:"kind"`
	ID                     kernel.UUIDv7           `json:"id"`
	AggregateRevision      uint64                  `json:"aggregate_revision"`
	LifecycleEpoch         uint64                  `json:"lifecycle_epoch"`
	ScopeRevision          uint64                  `json:"scope_revision"`
	Phase                  string                  `json:"phase"`
	Condition              string                  `json:"condition"`
	ProjectionRevision     uint64                  `json:"projection_revision"`
	LastEventID            kernel.UUIDv7           `json:"last_event_id"`
	SourceContractIdentity string                  `json:"source_contract_identity"`
	Title                  string                  `json:"title"`
	Description            string                  `json:"description"`
	AcceptanceCriteria     []string                `json:"acceptance_criteria"`
	DependencyIDs          []kernel.UUIDv7         `json:"dependency_ids"`
	TaskIDs                []kernel.UUIDv7         `json:"task_ids"`
	TaskCounts             map[string]uint64       `json:"task_counts"`
	Completion             ProjectionStatusSummary `json:"completion"`
	Acceptance             ProjectionStatusSummary `json:"acceptance"`
	Release                ProjectionStatusSummary `json:"release"`
	Blocker                ProjectionStatusSummary `json:"blocker"`
	Escalation             ProjectionStatusSummary `json:"escalation"`
}

func (p StoryProjection) Valid() bool {
	return p.Kind == "story_projection" && p.ID.Valid() && p.AggregateRevision > 0 && p.LifecycleEpoch > 0 && p.ScopeRevision > 0 && p.Phase != "" && p.Condition != "" && p.ProjectionRevision > 0 && p.LastEventID.Valid() && p.SourceContractIdentity == kernel.ContractIdentity && p.Title != "" && len(p.AcceptanceCriteria) > 0 && validProjectionUUIDs(p.DependencyIDs) && validProjectionUUIDs(p.TaskIDs) && p.TaskCounts != nil && p.Completion.valid() && p.Acceptance.valid() && p.Release.valid() && p.Blocker.valid() && p.Escalation.valid()
}

type TaskClassificationProjection struct {
	WorkKind             string `json:"work_kind"`
	Ambiguity            string `json:"ambiguity"`
	Novelty              string `json:"novelty"`
	BlastRadius          string `json:"blast_radius"`
	SecuritySensitivity  string `json:"security_sensitivity"`
	MinimumDecisionRoute string `json:"minimum_decision_route"`
}

type TaskBudgetProjection struct {
	AccountID                 kernel.UUIDv7          `json:"account_id"`
	AccountRevision           uint64                 `json:"account_revision"`
	ModelInvocationLimit      uint64                 `json:"model_invocation_limit"`
	ModelInvocationsUsed      uint64                 `json:"model_invocations_used"`
	RemainingModelInvocations uint64                 `json:"remaining_model_invocations"`
	PurposeLimits             kernel.PurposeCounters `json:"purpose_limits"`
	PurposeUsed               kernel.PurposeCounters `json:"purpose_used"`
	DeadlineAt                time.Time              `json:"deadline_at"`
}

type TaskOperationalScopeProjection struct {
	OwnerFQN                       kernel.ActorFQN `json:"owner_fqn"`
	ExecutionID                    kernel.UUIDv7   `json:"execution_id"`
	FencingEpoch                   uint64          `json:"fencing_epoch"`
	WorkspaceID                    string          `json:"workspace_id"`
	WorktreeID                     string          `json:"worktree_id"`
	Branch                         string          `json:"branch"`
	BaselineSHA                    string          `json:"baseline_sha"`
	WritablePaths                  []string        `json:"writable_paths"`
	InterfaceConstraintEvidenceIDs []kernel.UUIDv7 `json:"interface_constraint_evidence_ids"`
}

type TaskAssignmentProjection struct {
	AssignmentID          kernel.UUIDv7   `json:"assignment_id"`
	RequiredRoute         string          `json:"required_route"`
	SelectedRoute         string          `json:"selected_route"`
	ActorFQN              kernel.ActorFQN `json:"actor_fqn"`
	ExecutionID           kernel.UUIDv7   `json:"execution_id"`
	FencingEpoch          uint64          `json:"fencing_epoch"`
	ModelProfileDigest    kernel.Digest   `json:"model_profile_digest"`
	RuntimeIdentityDigest kernel.Digest   `json:"runtime_identity_digest"`
}

type TaskInvocationProjection struct {
	InvocationID    kernel.UUIDv7              `json:"invocation_id"`
	State           kernel.WorkInvocationState `json:"state"`
	Purpose         kernel.WorkPurpose         `json:"purpose"`
	AttemptOrdinal  uint64                     `json:"attempt_ordinal"`
	ConditionDigest kernel.Digest              `json:"condition_digest"`
}

type TaskProjection struct {
	Kind                   string                          `json:"kind"`
	ID                     kernel.UUIDv7                   `json:"id"`
	AggregateRevision      uint64                          `json:"aggregate_revision"`
	LifecycleEpoch         uint64                          `json:"lifecycle_epoch"`
	ScopeRevision          uint64                          `json:"scope_revision"`
	Phase                  string                          `json:"phase"`
	Condition              string                          `json:"condition"`
	ProjectionRevision     uint64                          `json:"projection_revision"`
	LastEventID            kernel.UUIDv7                   `json:"last_event_id"`
	SourceContractIdentity string                          `json:"source_contract_identity"`
	Title                  string                          `json:"title"`
	Description            string                          `json:"description"`
	AcceptanceCriteria     []string                        `json:"acceptance_criteria"`
	DependencyIDs          []kernel.UUIDv7                 `json:"dependency_ids"`
	StoryID                kernel.UUIDv7                   `json:"story_id"`
	OwnerFQN               *kernel.ActorFQN                `json:"owner_fqn"`
	OwnershipVersion       uint64                          `json:"ownership_version"`
	OperationalScope       *TaskOperationalScopeProjection `json:"operational_scope"`
	WorkProfileID          kernel.UUIDv7                   `json:"work_profile_id"`
	WorkProfileRevision    uint64                          `json:"work_profile_revision"`
	WorkProfileDigest      kernel.Digest                   `json:"work_profile_digest"`
	Classification         TaskClassificationProjection    `json:"classification"`
	Budget                 TaskBudgetProjection            `json:"budget"`
	QualifiedAssignment    *TaskAssignmentProjection       `json:"qualified_assignment"`
	LatestInvocation       *TaskInvocationProjection       `json:"latest_invocation"`
	InvocationCounts       map[string]uint64               `json:"invocation_counts"`
	Validation             ProjectionStatusSummary         `json:"validation"`
	Finding                ProjectionStatusSummary         `json:"finding"`
	Escalation             ProjectionStatusSummary         `json:"escalation"`
	Completion             ProjectionStatusSummary         `json:"completion"`
	Acceptance             ProjectionStatusSummary         `json:"acceptance"`
}

func (p TaskProjection) Valid() bool {
	if p.Kind != "task_projection" || !p.ID.Valid() || p.AggregateRevision == 0 || p.LifecycleEpoch == 0 || p.ScopeRevision == 0 || p.Phase == "" || p.Condition == "" || p.ProjectionRevision == 0 || !p.LastEventID.Valid() || p.SourceContractIdentity != kernel.ContractIdentity || p.Title == "" || len(p.AcceptanceCriteria) == 0 || !validProjectionUUIDs(p.DependencyIDs) || !p.StoryID.Valid() || !p.WorkProfileID.Valid() || p.WorkProfileRevision == 0 || !p.WorkProfileDigest.Valid() || p.Classification.WorkKind == "" || p.Classification.Ambiguity == "" || p.Classification.Novelty == "" || p.Classification.BlastRadius == "" || p.Classification.SecuritySensitivity == "" || p.Classification.MinimumDecisionRoute == "" || !p.Budget.valid() || p.InvocationCounts == nil || !p.Validation.valid() || !p.Finding.valid() || !p.Escalation.valid() || !p.Completion.valid() || !p.Acceptance.valid() {
		return false
	}
	if p.OwnerFQN != nil && !p.OwnerFQN.Valid() {
		return false
	}
	if p.OperationalScope != nil && !p.OperationalScope.valid() {
		return false
	}
	if p.QualifiedAssignment != nil && !p.QualifiedAssignment.valid() {
		return false
	}
	return p.LatestInvocation == nil || p.LatestInvocation.valid()
}

func (s ProjectionStatusSummary) valid() bool {
	return s.State != "" && (s.EventID == nil || s.EventID.Valid()) && validProjectionUUIDs(s.EvidenceIDs)
}

func (b TaskBudgetProjection) valid() bool {
	return b.AccountID.Valid() && b.AccountRevision > 0 && b.ModelInvocationLimit > 0 && b.ModelInvocationsUsed <= b.ModelInvocationLimit && b.RemainingModelInvocations == b.ModelInvocationLimit-b.ModelInvocationsUsed && b.PurposeLimits.Valid() && b.PurposeUsed.Valid() && !b.DeadlineAt.IsZero()
}

func (s TaskOperationalScopeProjection) valid() bool {
	return s.OwnerFQN.Valid() && s.ExecutionID.Valid() && s.FencingEpoch > 0 && s.WorkspaceID != "" && s.WorktreeID != "" && s.Branch != "" && len(s.BaselineSHA) == 40 && len(s.WritablePaths) > 0 && validProjectionUUIDs(s.InterfaceConstraintEvidenceIDs)
}

func (a TaskAssignmentProjection) valid() bool {
	return a.AssignmentID.Valid() && a.RequiredRoute != "" && a.SelectedRoute != "" && a.ActorFQN.Valid() && a.ExecutionID.Valid() && a.FencingEpoch > 0 && a.ModelProfileDigest.Valid() && a.RuntimeIdentityDigest.Valid()
}

func (i TaskInvocationProjection) valid() bool {
	return i.InvocationID.Valid() && i.State != "" && i.Purpose.Valid() && i.AttemptOrdinal > 0 && i.ConditionDigest.Valid()
}

type ProjectionCheckpoint struct {
	Kind               string        `json:"kind"`
	Projector          string        `json:"projector"`
	LastEventID        kernel.UUIDv7 `json:"last_event_id"`
	LastEventDigest    kernel.Digest `json:"last_event_digest"`
	ProjectionRevision uint64        `json:"projection_revision"`
}

type ProjectionFault struct {
	ProjectionKind   string              `json:"projection_kind"`
	Aggregate        kernel.AggregateRef `json:"aggregate"`
	ExpectedRevision uint64              `json:"expected_revision"`
	ObservedRevision uint64              `json:"observed_revision"`
	EventID          kernel.UUIDv7       `json:"event_id"`
	EventDigest      kernel.Digest       `json:"event_digest"`
	FaultKind        string              `json:"fault_kind"`
	ObservedAt       time.Time           `json:"observed_at"`
}

func (f ProjectionFault) RecordFaultPayload() (json.RawMessage, error) {
	if (f.Aggregate.Kind != kernel.AggregateStory && f.Aggregate.Kind != kernel.AggregateTask) || !f.Aggregate.Valid() || f.ExpectedRevision == 0 || f.ObservedRevision == 0 || !f.EventID.Valid() || !f.EventDigest.Valid() || (f.FaultKind != "REVISION_GAP" && f.FaultKind != "REVISION_CONFLICT" && f.FaultKind != "REBUILD_MISMATCH") || f.ObservedAt.IsZero() {
		return nil, ErrInvalidDecision
	}
	return json.Marshal(f)
}

type ProjectionAdvance struct {
	Accepted  bool
	Duplicate bool
	Reason    string
	Fault     *ProjectionFault
}

func AssessProjectionAdvance(checkpoint *ProjectionCheckpoint, event kernel.DomainEvent, observedAt time.Time) ProjectionAdvance {
	digest, err := operationalEventDigest(event)
	if err != nil || !event.Aggregate.Valid() || !event.EventID.Valid() || event.AggregateRevision == 0 {
		return ProjectionAdvance{Reason: "INVALID_EVENT"}
	}
	if checkpoint == nil {
		if event.AggregateRevision == 1 {
			return ProjectionAdvance{Accepted: true, Reason: "NEXT"}
		}
		return projectionFaultAdvance(event, digest, 1, "REVISION_GAP", observedAt)
	}
	if checkpoint.ProjectionRevision == event.AggregateRevision && checkpoint.LastEventID == event.EventID && checkpoint.LastEventDigest == digest {
		return ProjectionAdvance{Accepted: true, Duplicate: true, Reason: "DUPLICATE"}
	}
	if event.AggregateRevision != checkpoint.ProjectionRevision+1 {
		kind := "REVISION_CONFLICT"
		if event.AggregateRevision > checkpoint.ProjectionRevision+1 {
			kind = "REVISION_GAP"
		}
		return projectionFaultAdvance(event, digest, checkpoint.ProjectionRevision+1, kind, observedAt)
	}
	return ProjectionAdvance{Accepted: true, Reason: "NEXT"}
}

func projectionFaultAdvance(event kernel.DomainEvent, digest kernel.Digest, expected uint64, kind string, observedAt time.Time) ProjectionAdvance {
	projectionKind := "TASK"
	if event.Aggregate.Kind == kernel.AggregateStory {
		projectionKind = "STORY"
	}
	fault := ProjectionFault{ProjectionKind: projectionKind, Aggregate: event.Aggregate, ExpectedRevision: expected, ObservedRevision: event.AggregateRevision, EventID: event.EventID, EventDigest: digest, FaultKind: kind, ObservedAt: observedAt.UTC()}
	return ProjectionAdvance{Reason: kind, Fault: &fault}
}

func operationalEventDigest(event kernel.DomainEvent) (kernel.Digest, error) {
	data, err := json.Marshal(event)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return kernel.Digest(hex.EncodeToString(digest[:])), nil
}

func canonicalProjectionDigest(value any) (kernel.Digest, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return kernel.Digest(hex.EncodeToString(digest[:])), nil
}

func canonicalProjectionEqual(left, right any) bool {
	return reflect.DeepEqual(left, right)
}

func emptyProjectionStatus() ProjectionStatusSummary {
	return ProjectionStatusSummary{State: "NONE", EvidenceIDs: []kernel.UUIDv7{}}
}

func canonicalUUIDs(values []kernel.UUIDv7) []kernel.UUIDv7 {
	result := append([]kernel.UUIDv7(nil), values...)
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	unique := make([]kernel.UUIDv7, 0, len(result))
	for _, value := range result {
		if len(unique) == 0 || unique[len(unique)-1] != value {
			unique = append(unique, value)
		}
	}
	if unique == nil {
		unique = []kernel.UUIDv7{}
	}
	return unique
}

func validProjectionUUIDs(values []kernel.UUIDv7) bool {
	for index, value := range values {
		if !value.Valid() || index > 0 && values[index-1] >= value {
			return false
		}
	}
	return values != nil
}

func projectionCheckpointID(aggregate kernel.AggregateRef) string {
	return fmt.Sprintf("%s:%s", operationalProjectorName, aggregateKey(aggregate))
}
