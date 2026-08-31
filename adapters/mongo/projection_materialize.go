package mongo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/tekroo-ai/teams/kernel"
	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type operationalProjectionDocument struct {
	ID       string `bson:"_id"`
	StoryID  string `bson:"story_id,omitempty"`
	OwnerFQN string `bson:"owner_fqn,omitempty"`
	Phase    string `bson:"phase"`
	Data     []byte `bson:"data"`
}

func (s *Store) materializeTaskProjection(ctx context.Context, task kernel.AggregateRef, triggeringEvent kernel.UUIDv7) (*TaskProjection, error) {
	if task.Kind != kernel.AggregateTask || !task.Valid() {
		return nil, ErrInvalidDecision
	}
	snapshot, err := s.loadSnapshot(ctx, task, nil)
	if err != nil {
		return nil, err
	}
	if !snapshot.Exists || snapshot.State == nil {
		return nil, nil
	}
	created, err := s.creationEvent(ctx, task, "tekroo.event.task.created")
	if err != nil {
		return nil, err
	}
	var description struct {
		StoryID            kernel.UUIDv7   `json:"story_id"`
		Title              string          `json:"title"`
		Description        string          `json:"description"`
		AcceptanceCriteria []string        `json:"acceptance_criteria"`
		DependsOn          []kernel.UUIDv7 `json:"depends_on"`
	}
	if json.Unmarshal(created.Payload, &description) != nil || !description.StoryID.Valid() || description.Title == "" || len(description.AcceptanceCriteria) == 0 {
		return nil, ErrCorruptAggregate
	}
	profile, profileFound := snapshot.WorkProfiles[task]
	binding, bindingFound := snapshot.TaskWorkBudgets[task]
	account := snapshot.WorkBudgetAccounts[kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: binding.BudgetAccountID}]
	if !profileFound || !bindingFound || !profile.Valid() || !binding.Valid() || !account.Valid() {
		return nil, nil
	}

	projectionRevision := uint64(1)
	var existing TaskProjection
	if found, loadErr := s.readProjection(ctx, "task_projections", aggregateKey(task), &existing); loadErr != nil {
		return nil, loadErr
	} else if found {
		projectionRevision = existing.ProjectionRevision + 1
	}
	state := snapshot.State
	projection := &TaskProjection{
		Kind: "task_projection", ID: task.ID, AggregateRevision: state.Revision,
		LifecycleEpoch: state.LifecycleEpoch, ScopeRevision: state.ScopeRevision,
		Phase: string(state.Phase), Condition: string(state.Condition), ProjectionRevision: projectionRevision,
		LastEventID: triggeringEvent, SourceContractIdentity: kernel.ContractIdentity,
		Title: description.Title, Description: description.Description,
		AcceptanceCriteria: append([]string(nil), description.AcceptanceCriteria...),
		DependencyIDs:      canonicalUUIDs(description.DependsOn), StoryID: description.StoryID,
		OwnershipVersion: state.Ownership.OwnershipVersion,
		WorkProfileID:    profile.Profile.ProfileID, WorkProfileRevision: profile.Profile.ProfileRevision,
		WorkProfileDigest: profile.Profile.ProfileDigest,
		Classification: TaskClassificationProjection{
			WorkKind: string(profile.Profile.WorkKind), Ambiguity: string(profile.Profile.Ambiguity),
			Novelty: string(profile.Profile.Novelty), BlastRadius: string(profile.Profile.BlastRadius),
			SecuritySensitivity: string(profile.Profile.SecuritySensitivity), MinimumDecisionRoute: string(profile.Profile.MinimumDecisionRoute),
		},
		Budget: TaskBudgetProjection{
			AccountID: account.ID, AccountRevision: account.Revision,
			ModelInvocationLimit: account.ModelInvocationLimit, ModelInvocationsUsed: account.ModelInvocationsUsed,
			RemainingModelInvocations: account.MaximumAdditionalInvocations(), PurposeLimits: account.PurposeLimits.Clone(),
			PurposeUsed: account.PurposeUsed.Clone(), DeadlineAt: account.DeadlineAt,
		},
		InvocationCounts: map[string]uint64{}, Validation: emptyProjectionStatus(), Finding: emptyProjectionStatus(),
		Escalation: emptyProjectionStatus(), Completion: emptyProjectionStatus(), Acceptance: emptyProjectionStatus(),
	}
	if state.Ownership.OwnerFQN != nil {
		owner := *state.Ownership.OwnerFQN
		projection.OwnerFQN = &owner
	}
	if scope, found := snapshot.TaskOperationalScopes[task]; found && scope.Valid() {
		projection.OperationalScope = &TaskOperationalScopeProjection{
			OwnerFQN: scope.OwnerFQN, ExecutionID: scope.Execution.ExecutionID, FencingEpoch: scope.Execution.FencingEpoch,
			WorkspaceID: scope.WorkspaceID, WorktreeID: scope.WorktreeID, Branch: scope.Branch, BaselineSHA: scope.BaselineSHA,
			WritablePaths: append([]string(nil), scope.WritablePaths...), InterfaceConstraintEvidenceIDs: canonicalUUIDs(scope.InterfaceEvidenceIDs),
		}
	}
	if assignment, found := snapshot.QualifiedAssignments[task]; found && assignment.Valid() {
		projection.QualifiedAssignment = &TaskAssignmentProjection{
			AssignmentID: assignment.AssignmentID, RequiredRoute: string(assignment.RequiredDecisionRoute), SelectedRoute: string(assignment.SelectedDecisionRoute),
			ActorFQN: assignment.SelectedActorFQN, ExecutionID: assignment.SelectedExecutionID, FencingEpoch: assignment.SelectedFencingEpoch,
			ModelProfileDigest: assignment.ModelProfileDigest, RuntimeIdentityDigest: assignment.RuntimeIdentityDigest,
		}
	}
	var latest *kernel.WorkInvocation
	for _, invocation := range snapshot.WorkInvocations {
		if invocation.TaskID != task.ID || !invocation.Valid() {
			continue
		}
		projection.InvocationCounts["purpose:"+string(invocation.Purpose)]++
		projection.InvocationCounts["state:"+string(invocation.State)]++
		candidate := invocation.Clone()
		if latest == nil || candidate.AttemptOrdinal > latest.AttemptOrdinal || candidate.AttemptOrdinal == latest.AttemptOrdinal && candidate.Revision > latest.Revision {
			latest = &candidate
		}
	}
	if latest != nil {
		projection.LatestInvocation = &TaskInvocationProjection{InvocationID: latest.ID, State: latest.State, Purpose: latest.Purpose, AttemptOrdinal: latest.AttemptOrdinal, ConditionDigest: latest.ConditionDigest}
	}
	if projection.InvocationCounts == nil {
		projection.InvocationCounts = map[string]uint64{}
	}

	directEvents, err := s.aggregateEvents(ctx, task)
	if err != nil {
		return nil, err
	}
	projection.Completion = latestProjectionStatus(directEvents, "tekroo.event.task.completed", "COMPLETED")
	if state.Condition == kernel.ConditionBlocked {
		projection.Finding = latestProjectionStatus(directEvents, "tekroo.event.work.blocked", "BLOCKED")
	}
	for reference, review := range snapshot.Reviews {
		if review.Subject != task {
			continue
		}
		events, loadErr := s.aggregateEvents(ctx, reference)
		if loadErr != nil {
			return nil, loadErr
		}
		reviewState := "OPEN"
		if review.Join.Complete {
			reviewState = review.Join.Status
		}
		if review.Finalization != nil {
			reviewState = review.Finalization.TerminalStatus
		}
		projection.Validation = latestProjectionStatusByPrefix(events, "tekroo.event.completion-review.", reviewState)
		if len(review.CurrentFindings) > 0 {
			findingEvidence := make([]kernel.UUIDv7, 0)
			for _, finding := range review.CurrentFindings {
				findingEvidence = append(findingEvidence, finding.EvidenceIDs...)
			}
			projection.Finding = projection.Validation
			projection.Finding.State = "OPEN_FINDINGS"
			projection.Finding.EvidenceIDs = canonicalUUIDs(findingEvidence)
		}
		if review.Finalization != nil {
			projection.Acceptance = projection.Validation
		}
	}
	for reference, group := range snapshot.VariantGroups {
		if group.TaskID != task.ID {
			continue
		}
		events, loadErr := s.aggregateEvents(ctx, reference)
		if loadErr != nil {
			return nil, loadErr
		}
		projection.Finding = latestProjectionStatusByPrefix(events, "tekroo.event.variant-group.", string(group.State))
	}
	for reference, escalation := range snapshot.Escalations {
		if escalation.Subject != task {
			continue
		}
		events, loadErr := s.aggregateEvents(ctx, reference)
		if loadErr != nil {
			return nil, loadErr
		}
		projection.Escalation = latestProjectionStatusByPrefix(events, "tekroo.event.escalation.", string(escalation.State))
	}
	return projection, nil
}

func (s *Store) materializeStoryProjection(ctx context.Context, story kernel.AggregateRef, triggeringEvent kernel.UUIDv7) (*StoryProjection, error) {
	if story.Kind != kernel.AggregateStory || !story.Valid() {
		return nil, ErrInvalidDecision
	}
	snapshot, err := s.loadSnapshot(ctx, story, nil)
	if err != nil {
		return nil, err
	}
	if !snapshot.Exists || snapshot.State == nil {
		return nil, nil
	}
	created, err := s.creationEvent(ctx, story, "tekroo.event.story.created")
	if err != nil {
		return nil, err
	}
	var description struct {
		Title              string   `json:"title"`
		Description        string   `json:"description"`
		AcceptanceCriteria []string `json:"acceptance_criteria"`
	}
	if json.Unmarshal(created.Payload, &description) != nil || description.Title == "" || len(description.AcceptanceCriteria) == 0 {
		return nil, ErrCorruptAggregate
	}
	projectionRevision := uint64(1)
	var existing StoryProjection
	if found, loadErr := s.readProjection(ctx, "story_projections", aggregateKey(story), &existing); loadErr != nil {
		return nil, loadErr
	} else if found {
		projectionRevision = existing.ProjectionRevision + 1
	}
	state := snapshot.State
	projection := &StoryProjection{
		Kind: "story_projection", ID: story.ID, AggregateRevision: state.Revision, LifecycleEpoch: state.LifecycleEpoch,
		ScopeRevision: state.ScopeRevision, Phase: string(state.Phase), Condition: string(state.Condition), ProjectionRevision: projectionRevision,
		LastEventID: triggeringEvent, SourceContractIdentity: kernel.ContractIdentity, Title: description.Title, Description: description.Description,
		AcceptanceCriteria: append([]string(nil), description.AcceptanceCriteria...), DependencyIDs: []kernel.UUIDv7{}, TaskIDs: []kernel.UUIDv7{},
		TaskCounts: map[string]uint64{}, Completion: emptyProjectionStatus(), Acceptance: emptyProjectionStatus(), Release: emptyProjectionStatus(),
		Blocker: emptyProjectionStatus(), Escalation: emptyProjectionStatus(),
	}

	if err := scan(ctx, s.db.Collection("events"), bson.D{{Key: "event_type", Value: "tekroo.event.task.created"}}, func(document eventDocument) error {
		var event kernel.DomainEvent
		if decode(document.Data, &event) != nil {
			return ErrCorruptAggregate
		}
		var payload struct {
			StoryID   kernel.UUIDv7   `json:"story_id"`
			DependsOn []kernel.UUIDv7 `json:"depends_on"`
		}
		if json.Unmarshal(event.Payload, &payload) != nil || payload.StoryID != story.ID {
			return nil
		}
		projection.TaskIDs = append(projection.TaskIDs, event.Aggregate.ID)
		projection.DependencyIDs = append(projection.DependencyIDs, payload.DependsOn...)
		var aggregate aggregateDocument
		if err := s.db.Collection("aggregates").FindOne(ctx, bson.D{{Key: "_id", Value: aggregateKey(event.Aggregate)}}).Decode(&aggregate); err != nil {
			return err
		}
		var taskState kernel.AggregateState
		if decode(aggregate.State, &taskState) != nil {
			return ErrCorruptAggregate
		}
		projection.TaskCounts["phase:"+string(taskState.Phase)]++
		projection.TaskCounts["condition:"+string(taskState.Condition)]++
		return nil
	}); err != nil {
		return nil, err
	}
	projection.TaskIDs = canonicalUUIDs(projection.TaskIDs)
	projection.DependencyIDs = canonicalUUIDs(projection.DependencyIDs)
	directEvents, err := s.aggregateEvents(ctx, story)
	if err != nil {
		return nil, err
	}
	projection.Completion = latestProjectionStatus(directEvents, "tekroo.event.story.completed", "COMPLETED")
	projection.Acceptance = latestProjectionStatus(directEvents, "tekroo.event.story.accepted", "ACCEPTED")
	projection.Release = latestProjectionStatus(directEvents, "tekroo.event.story.release-approved", "APPROVED")
	for reference, plan := range snapshot.ReleasePlans {
		if plan.Story != story {
			continue
		}
		events, loadErr := s.aggregateEvents(ctx, reference)
		if loadErr != nil {
			return nil, loadErr
		}
		projection.Release = latestProjectionStatusByPrefix(events, "tekroo.event.release-plan.", string(plan.State))
	}
	if state.Condition == kernel.ConditionBlocked {
		projection.Blocker = latestProjectionStatus(directEvents, "tekroo.event.work.blocked", "BLOCKED")
	}
	for reference, escalation := range snapshot.Escalations {
		if escalation.Subject != story {
			continue
		}
		events, loadErr := s.aggregateEvents(ctx, reference)
		if loadErr != nil {
			return nil, loadErr
		}
		projection.Escalation = latestProjectionStatusByPrefix(events, "tekroo.event.escalation.", string(escalation.State))
	}
	return projection, nil
}

func (s *Store) creationEvent(ctx context.Context, aggregate kernel.AggregateRef, eventType string) (kernel.DomainEvent, error) {
	var document eventDocument
	if err := s.db.Collection("events").FindOne(ctx, bson.D{{Key: "aggregate_key", Value: aggregateKey(aggregate)}, {Key: "revision", Value: 1}, {Key: "event_type", Value: eventType}}).Decode(&document); err != nil {
		return kernel.DomainEvent{}, err
	}
	var event kernel.DomainEvent
	if decode(document.Data, &event) != nil {
		return kernel.DomainEvent{}, ErrCorruptAggregate
	}
	return event, nil
}

func (s *Store) aggregateEvents(ctx context.Context, aggregate kernel.AggregateRef) ([]kernel.DomainEvent, error) {
	cursor, err := s.db.Collection("events").Find(ctx, bson.D{{Key: "aggregate_key", Value: aggregateKey(aggregate)}}, options.Find().SetSort(bson.D{{Key: "revision", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	result := make([]kernel.DomainEvent, 0)
	for cursor.Next(ctx) {
		var document eventDocument
		if cursor.Decode(&document) != nil {
			return nil, ErrCorruptAggregate
		}
		var event kernel.DomainEvent
		if decode(document.Data, &event) != nil {
			return nil, ErrCorruptAggregate
		}
		result = append(result, event)
	}
	return result, cursor.Err()
}

func (s *Store) readProjection(ctx context.Context, collection, id string, target any) (bool, error) {
	var document operationalProjectionDocument
	err := s.db.Collection(collection).FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&document)
	if errors.Is(err, driver.ErrNoDocuments) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if decode(document.Data, target) != nil {
		return false, ErrCorruptAggregate
	}
	return true, nil
}

func latestProjectionStatus(events []kernel.DomainEvent, eventType, state string) ProjectionStatusSummary {
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].EventType == eventType {
			return statusFromEvent(events[index], state)
		}
	}
	return emptyProjectionStatus()
}

func latestProjectionStatusByPrefix(events []kernel.DomainEvent, prefix, state string) ProjectionStatusSummary {
	for index := len(events) - 1; index >= 0; index-- {
		if strings.HasPrefix(events[index].EventType, prefix) {
			return statusFromEvent(events[index], state)
		}
	}
	return emptyProjectionStatus()
}

func statusFromEvent(event kernel.DomainEvent, state string) ProjectionStatusSummary {
	var payload map[string]json.RawMessage
	_ = json.Unmarshal(event.Payload, &payload)
	evidence := []kernel.UUIDv7{}
	if raw, found := payload["evidence_ids"]; found {
		_ = json.Unmarshal(raw, &evidence)
	}
	eventID := event.EventID
	return ProjectionStatusSummary{State: state, EventID: &eventID, EvidenceIDs: canonicalUUIDs(evidence)}
}

func projectionDocument(value any) (operationalProjectionDocument, error) {
	data, err := encode(value)
	if err != nil {
		return operationalProjectionDocument{}, err
	}
	switch projection := value.(type) {
	case StoryProjection:
		if !projection.Valid() {
			return operationalProjectionDocument{}, ErrInvalidDecision
		}
		return operationalProjectionDocument{ID: aggregateKey(kernel.AggregateRef{Kind: kernel.AggregateStory, ID: projection.ID}), Phase: projection.Phase, Data: data}, nil
	case TaskProjection:
		if !projection.Valid() {
			return operationalProjectionDocument{}, ErrInvalidDecision
		}
		owner := ""
		if projection.OwnerFQN != nil {
			owner = string(*projection.OwnerFQN)
		}
		return operationalProjectionDocument{ID: aggregateKey(kernel.AggregateRef{Kind: kernel.AggregateTask, ID: projection.ID}), StoryID: string(projection.StoryID), OwnerFQN: owner, Phase: projection.Phase, Data: data}, nil
	default:
		return operationalProjectionDocument{}, fmt.Errorf("unsupported projection type %T", value)
	}
}

func sortedProjectionRefs(values map[kernel.AggregateRef]struct{}) []kernel.AggregateRef {
	result := make([]kernel.AggregateRef, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return aggregateKey(result[i]) < aggregateKey(result[j]) })
	return result
}
