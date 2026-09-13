package organization

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

type FeatureRoleHost interface {
	ConfiguredRoleActors(string) ([]kernel.ActorFQN, error)
	EnsureStarted(context.Context, kernel.ActorFQN) (RoleInstanceState, error)
	ResolveRoleRecipients(context.Context, string) ([]kernel.ActorFQN, error)
	Status(context.Context, kernel.ActorFQN) (RoleInstanceState, bool, error)
}

type FeaturePlanMaterializer interface {
	MaterializeFeaturePlan(context.Context, FeatureRequest, FeaturePlan) error
}

type FeatureCoordinator struct {
	store       FeatureStore
	host        FeatureRoleHost
	materialize FeaturePlanMaterializer
	clock       kernel.Clock
	ids         kernel.IDSource
}

func NewFeatureCoordinator(store FeatureStore, host FeatureRoleHost, materializer FeaturePlanMaterializer, clock kernel.Clock, ids kernel.IDSource) (*FeatureCoordinator, error) {
	if store == nil || host == nil || materializer == nil || clock == nil || ids == nil {
		return nil, ErrInvalidFeature
	}
	return &FeatureCoordinator{store: store, host: host, materialize: materializer, clock: clock, ids: ids}, nil
}

func (coordinator *FeatureCoordinator) Submit(ctx context.Context, principal kernel.PrincipalRef, input FeatureRequestInput) (FeatureRequest, bool, error) {
	if coordinator == nil || !principal.Valid() || principal.Kind != kernel.PrincipalHuman || input.Validate() != nil {
		return FeatureRequest{}, false, ErrInvalidFeature
	}
	if prior, found, err := coordinator.store.LoadFeatureByIdempotencyKey(ctx, principal, input.IdempotencyKey); err != nil {
		return FeatureRequest{}, false, err
	} else if found {
		if !reflect.DeepEqual(prior.Input, input) {
			return FeatureRequest{}, false, ErrFeatureConflict
		}
		return prior, false, nil
	}
	operatorActors, err := coordinator.host.ConfiguredRoleActors("operator")
	if err != nil || len(operatorActors) != 1 {
		return FeatureRequest{}, false, errors.Join(ErrInvalidFeature, err)
	}
	operator, err := coordinator.host.EnsureStarted(ctx, operatorActors[0])
	if err != nil || operator.Status != RoleIdle || !operator.Execution.Valid() {
		return FeatureRequest{}, false, errors.Join(ErrRoleNotRunning, err)
	}
	productOwner, err := coordinator.ensurePrimaryRole(ctx, "product-owner")
	if err != nil {
		return FeatureRequest{}, false, err
	}
	now := coordinator.clock.Now().UTC()
	featureID, err := coordinator.ids.Next()
	if err != nil {
		return FeatureRequest{}, false, err
	}
	messageID, err := coordinator.ids.Next()
	if err != nil {
		return FeatureRequest{}, false, err
	}
	stepID, err := coordinator.ids.Next()
	if err != nil {
		return FeatureRequest{}, false, err
	}
	budgetID, err := coordinator.ids.Next()
	if err != nil {
		return FeatureRequest{}, false, err
	}
	feature := FeatureRequest{
		SchemaVersion: FeatureSchemaVersion, ID: featureID, Revision: 1, SubmittedBy: principal,
		Input: input, Status: FeatureSubmitted, OperatorActor: operator.ActorFQN, ProductOwnerActor: productOwner.ActorFQN,
		InitialMessageID: messageID, BudgetAccountID: budgetID, LifecycleEpoch: 1, ScopeRevision: 1,
		CreatedAt: now, UpdatedAt: now, LastMessageID: messageID, LastStepID: stepID, LastHop: 1,
	}
	body, err := json.Marshal(map[string]any{"feature_id": featureID, "request": input, "submitted_by": principal})
	if err != nil {
		return FeatureRequest{}, false, err
	}
	digest := sha256.Sum256(body)
	message := OrganizationalMessage{
		SchemaVersion: OrganizationalMessageSchemaVersion, ID: messageID, Type: "tekroo.message.feature.submitted", Purpose: PurposeRequest,
		Sender: operator.ActorFQN, SenderExecution: operator.Execution, Recipient: productOwner.ActorFQN, CorrelationID: featureID,
		Work: MessageWorkLink{FeatureID: &featureID, DAGNodeID: stepID},
		Flow: MessageFlow{ThreadID: featureID, StepID: stepID, Hop: 1, MaximumHops: input.MaximumHops, BudgetAccountID: budgetID, LifecycleEpoch: 1, ScopeRevision: 1, ProgressDigest: kernel.Digest(hex.EncodeToString(digest[:]))},
		Body: body, CreatedAt: now, ExpiresAt: now.Add(7 * 24 * time.Hour),
	}
	if feature.Validate() != nil || message.Validate() != nil {
		return FeatureRequest{}, false, ErrInvalidFeature
	}
	return coordinator.store.CreateFeature(ctx, feature, message)
}

func (coordinator *FeatureCoordinator) ApplyPlan(ctx context.Context, featureID kernel.UUIDv7, expectedRevision uint64, plan FeaturePlan) (FeatureRequest, error) {
	if coordinator == nil || !featureID.Valid() || expectedRevision == 0 {
		return FeatureRequest{}, ErrInvalidFeature
	}
	feature, found, err := coordinator.store.LoadFeature(ctx, featureID)
	if err != nil {
		return FeatureRequest{}, err
	}
	if !found {
		return FeatureRequest{}, ErrFeatureNotFound
	}
	if feature.Revision != expectedRevision || feature.Status != FeatureSpecified || feature.Specification == nil {
		return FeatureRequest{}, ErrFeatureRevisionConflict
	}
	expectedPlanVersion := uint64(1)
	if feature.Plan != nil {
		if feature.PlanSupersession == nil || feature.PlanSupersession.PlanVersion != feature.Plan.Version {
			return FeatureRequest{}, ErrInvalidFeature
		}
		expectedPlanVersion = feature.Plan.Version + 1
	}
	var priorStories []PlannedStory
	if feature.Plan != nil {
		priorStories = feature.Plan.Stories
	}
	if plan.Version != expectedPlanVersion || plan.Validate(feature) != nil || !strings.Contains(string(plan.PreparedBy), "::architect-") || !featurePlanStoriesMatchSpecification(plan.Stories, feature.Specification.Stories, priorStories, plan.Version > 1) {
		return FeatureRequest{}, ErrInvalidFeature
	}
	planner, found, err := coordinator.host.Status(ctx, plan.PreparedBy)
	if err != nil || !found || planner.Status != RoleIdle {
		return FeatureRequest{}, errors.Join(ErrStaleOrganizationalClaim, err)
	}
	for _, task := range plan.Tasks {
		owner, active, ownerErr := coordinator.host.Status(ctx, task.Owner)
		if ownerErr != nil {
			return FeatureRequest{}, ownerErr
		}
		if !active || owner.Status != RoleIdle {
			owner, ownerErr = coordinator.host.EnsureStarted(ctx, task.Owner)
		}
		if ownerErr != nil || owner.Status != RoleIdle || owner.ModelProfile != task.ModelProfile {
			return FeatureRequest{}, errors.Join(ErrRoleNotRunning, ownerErr)
		}
	}
	if err := coordinator.materialize.MaterializeFeaturePlan(ctx, feature, plan); err != nil {
		return FeatureRequest{}, err
	}
	return coordinator.store.ApplyFeaturePlan(ctx, feature.ID, expectedRevision, plan, coordinator.clock.Now().UTC())
}

func featurePlanStoriesMatchSpecification(planned, specified, prior []PlannedStory, replacement bool) bool {
	if len(planned) != len(specified) {
		return false
	}
	priorIDs := make(map[kernel.UUIDv7]struct{}, len(prior))
	for _, story := range prior {
		priorIDs[story.ID] = struct{}{}
	}
	for index := range planned {
		left, right := planned[index], specified[index]
		if replacement {
			if left.ID == right.ID {
				return false
			}
			if _, reused := priorIDs[left.ID]; reused {
				return false
			}
			left.ID = right.ID
		}
		if !reflect.DeepEqual(left, right) {
			return false
		}
	}
	return true
}

// RequestReplan returns one planned feature to architecture without replaying
// product ownership or specification. The caller is responsible for retiring
// the materialized tasks before this projection transition is committed.
func (coordinator *FeatureCoordinator) RequestReplan(ctx context.Context, featureID kernel.UUIDv7, expectedRevision uint64, supersession FeaturePlanSupersession) (FeatureRequest, error) {
	feature, err := coordinator.currentFeature(ctx, featureID, expectedRevision, FeaturePlanned)
	if err != nil {
		return FeatureRequest{}, err
	}
	if feature.Plan == nil || supersession.Validate(feature) != nil || supersession.RequestedBy != feature.SubmittedBy || supersession.PlanDigest != featurePlanDigest(*feature.Plan) {
		return FeatureRequest{}, ErrInvalidFeature
	}
	next := feature
	next.Revision++
	next.Status = FeatureSpecified
	next.ScopeRevision++
	next.PlanSupersession = &supersession
	next.Acceptance = nil
	next.UpdatedAt = coordinator.clock.Now().UTC()
	if next.Validate() != nil {
		return FeatureRequest{}, ErrInvalidFeature
	}
	return coordinator.store.AdvanceFeature(ctx, next, expectedRevision, nil)
}

func featurePlanDigest(plan FeaturePlan) kernel.Digest {
	raw, err := json.Marshal(plan)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return kernel.Digest(hex.EncodeToString(digest[:]))
}

func (coordinator *FeatureCoordinator) RecordAcceptanceRecommendation(ctx context.Context, featureID kernel.UUIDv7, expectedRevision uint64, acceptance FeatureAcceptance) (FeatureRequest, error) {
	feature, err := coordinator.currentFeature(ctx, featureID, expectedRevision, FeaturePlanned)
	if err != nil {
		return FeatureRequest{}, err
	}
	if acceptance.AcceptedBy != nil || acceptance.Validate(feature) != nil {
		return FeatureRequest{}, ErrInvalidFeature
	}
	next := feature
	next.Revision++
	next.Status = FeatureAwaitingAcceptance
	next.Acceptance = &acceptance
	next.UpdatedAt = coordinator.clock.Now().UTC()
	return coordinator.store.AdvanceFeature(ctx, next, expectedRevision, nil)
}

func (coordinator *FeatureCoordinator) Accept(ctx context.Context, featureID kernel.UUIDv7, expectedRevision uint64, principal kernel.PrincipalRef, releasePlanIDs []kernel.UUIDv7) (FeatureRequest, error) {
	feature, err := coordinator.currentFeature(ctx, featureID, expectedRevision, FeatureAwaitingAcceptance)
	if err != nil {
		return FeatureRequest{}, err
	}
	if principal.Kind != kernel.PrincipalHuman || !principal.Valid() || feature.Acceptance == nil || len(releasePlanIDs) != len(feature.Acceptance.StoryIDs) {
		return FeatureRequest{}, ErrInvalidFeature
	}
	next := feature
	next.Revision++
	next.Status = FeatureAccepted
	accepted := *feature.Acceptance
	accepted.AcceptedBy = &principal
	accepted.ReleasePlanIDs = append([]kernel.UUIDv7(nil), releasePlanIDs...)
	accepted.RecordedAt = coordinator.clock.Now().UTC()
	next.Acceptance = &accepted
	next.UpdatedAt = accepted.RecordedAt
	if next.Validate() != nil {
		return FeatureRequest{}, ErrInvalidFeature
	}
	return coordinator.store.AdvanceFeature(ctx, next, expectedRevision, nil)
}

func (coordinator *FeatureCoordinator) Refine(ctx context.Context, featureID kernel.UUIDv7, expectedRevision uint64, refinement FeatureRefinement) (FeatureRequest, error) {
	feature, err := coordinator.currentFeature(ctx, featureID, expectedRevision, FeatureSubmitted)
	if err != nil {
		return FeatureRequest{}, err
	}
	if refinement.Validate(feature) != nil {
		return FeatureRequest{}, ErrInvalidFeature
	}
	actor, found, err := coordinator.host.Status(ctx, refinement.PreparedBy)
	if err != nil || !found || actor.Status != RoleIdle {
		return FeatureRequest{}, errors.Join(ErrStaleOrganizationalClaim, err)
	}
	next := feature
	next.Revision++
	next.UpdatedAt = coordinator.clock.Now().UTC()
	next.Refinement = &refinement
	if len(refinement.ClarificationQuestions) > 0 {
		next.Status = FeatureClarificationRequired
		return coordinator.store.AdvanceFeature(ctx, next, expectedRevision, nil)
	}
	recipient, err := coordinator.ensurePrimaryRole(ctx, "project-manager")
	if err != nil {
		return FeatureRequest{}, err
	}
	next.Status = FeatureReadyForPlanning
	message, err := coordinator.handoffMessage(feature, refinement.PreparedBy, refinement.PreparedExecution, recipient.ActorFQN, "tekroo.message.feature.refined", PurposeHandoff, map[string]any{"feature_id": feature.ID, "refinement": refinement})
	if err != nil {
		return FeatureRequest{}, err
	}
	next.LastMessageID, next.LastStepID, next.LastHop = message.ID, message.Flow.StepID, message.Flow.Hop
	return coordinator.store.AdvanceFeature(ctx, next, expectedRevision, &message)
}

func (coordinator *FeatureCoordinator) RespondToClarification(ctx context.Context, featureID kernel.UUIDv7, principal kernel.PrincipalRef, input FeatureClarificationResponseInput) (FeatureRequest, error) {
	if coordinator == nil || !principal.Valid() || principal.Kind != kernel.PrincipalHuman || !input.Valid() {
		return FeatureRequest{}, ErrInvalidFeature
	}
	feature, err := coordinator.currentFeature(ctx, featureID, input.ExpectedRevision, FeatureClarificationRequired)
	if err != nil {
		return FeatureRequest{}, err
	}
	if principal != feature.SubmittedBy || feature.Refinement == nil || len(input.Answers) != len(feature.Refinement.ClarificationQuestions) {
		return FeatureRequest{}, ErrInvalidFeature
	}
	now := coordinator.clock.Now().UTC()
	answers := make([]FeatureClarificationAnswer, len(input.Answers))
	for index, answer := range input.Answers {
		answers[index] = FeatureClarificationAnswer{Question: feature.Refinement.ClarificationQuestions[index], Answer: answer}
	}
	clarification := FeatureClarification{RespondedBy: principal, Answers: answers, RespondedAt: now}
	if clarification.Validate(feature) != nil {
		return FeatureRequest{}, ErrInvalidFeature
	}
	productOwner, err := coordinator.host.EnsureStarted(ctx, feature.ProductOwnerActor)
	if err != nil || productOwner.Status != RoleIdle || !productOwner.Execution.Valid() {
		return FeatureRequest{}, errors.Join(ErrRoleNotRunning, err)
	}
	recipient, err := coordinator.ensurePrimaryRole(ctx, "project-manager")
	if err != nil {
		return FeatureRequest{}, err
	}
	message, err := coordinator.handoffMessage(feature, productOwner.ActorFQN, productOwner.Execution, recipient.ActorFQN, "tekroo.message.feature.refined", PurposeHandoff, map[string]any{"feature_id": feature.ID, "refinement": feature.Refinement, "clarification": clarification})
	if err != nil {
		return FeatureRequest{}, err
	}
	next := feature
	next.Revision++
	next.Status = FeatureReadyForPlanning
	next.Clarification = &clarification
	next.UpdatedAt = now
	next.LastMessageID, next.LastStepID, next.LastHop = message.ID, message.Flow.StepID, message.Flow.Hop
	if next.Validate() != nil {
		return FeatureRequest{}, ErrInvalidFeature
	}
	return coordinator.store.AdvanceFeature(ctx, next, input.ExpectedRevision, &message)
}

func (coordinator *FeatureCoordinator) Specify(ctx context.Context, featureID kernel.UUIDv7, expectedRevision uint64, specification FeatureSpecification) (FeatureRequest, error) {
	feature, err := coordinator.currentFeature(ctx, featureID, expectedRevision, FeatureReadyForPlanning)
	if err != nil {
		return FeatureRequest{}, err
	}
	if specification.Validate(feature) != nil || !strings.Contains(string(specification.PreparedBy), "::project-manager-") {
		return FeatureRequest{}, ErrInvalidFeature
	}
	actor, found, err := coordinator.host.Status(ctx, specification.PreparedBy)
	if err != nil || !found || actor.Status != RoleIdle {
		return FeatureRequest{}, errors.Join(ErrStaleOrganizationalClaim, err)
	}
	recipient, err := coordinator.ensurePrimaryRole(ctx, "architect")
	if err != nil {
		return FeatureRequest{}, err
	}
	message, err := coordinator.handoffMessage(feature, specification.PreparedBy, specification.PreparedExecution, recipient.ActorFQN, "tekroo.message.story.design-requested", PurposeHandoff, map[string]any{"feature_id": feature.ID, "specification": specification})
	if err != nil {
		return FeatureRequest{}, err
	}
	next := feature
	next.Revision++
	next.Status = FeatureSpecified
	next.Specification = &specification
	next.UpdatedAt = coordinator.clock.Now().UTC()
	next.LastMessageID, next.LastStepID, next.LastHop = message.ID, message.Flow.StepID, message.Flow.Hop
	return coordinator.store.AdvanceFeature(ctx, next, expectedRevision, &message)
}

func (coordinator *FeatureCoordinator) currentFeature(ctx context.Context, id kernel.UUIDv7, revision uint64, status FeatureStatus) (FeatureRequest, error) {
	if coordinator == nil || !id.Valid() || revision == 0 {
		return FeatureRequest{}, ErrInvalidFeature
	}
	feature, found, err := coordinator.store.LoadFeature(ctx, id)
	if err != nil {
		return FeatureRequest{}, err
	}
	if !found {
		return FeatureRequest{}, ErrFeatureNotFound
	}
	if feature.Revision != revision || feature.Status != status {
		return FeatureRequest{}, ErrFeatureRevisionConflict
	}
	return feature, nil
}

func (coordinator *FeatureCoordinator) handoffMessage(feature FeatureRequest, sender kernel.ActorFQN, execution kernel.ExecutionTuple, recipient kernel.ActorFQN, messageType string, purpose MessagePurpose, value any) (OrganizationalMessage, error) {
	messageID, err := coordinator.ids.Next()
	if err != nil {
		return OrganizationalMessage{}, err
	}
	stepID, err := coordinator.ids.Next()
	if err != nil {
		return OrganizationalMessage{}, err
	}
	body, err := json.Marshal(value)
	if err != nil {
		return OrganizationalMessage{}, err
	}
	digest := sha256.Sum256(body)
	parentStep, cause := feature.LastStepID, feature.LastMessageID
	now := coordinator.clock.Now().UTC()
	message := OrganizationalMessage{
		SchemaVersion: OrganizationalMessageSchemaVersion, ID: messageID, Type: messageType, Purpose: purpose,
		Sender: sender, SenderExecution: execution, Recipient: recipient, CausationID: &cause, CorrelationID: feature.ID,
		Work: MessageWorkLink{FeatureID: &feature.ID, DAGNodeID: stepID},
		Flow: MessageFlow{ThreadID: feature.ID, StepID: stepID, ParentStepID: &parentStep, Hop: feature.LastHop + 1, MaximumHops: feature.Input.MaximumHops, BudgetAccountID: feature.BudgetAccountID, LifecycleEpoch: feature.LifecycleEpoch, ScopeRevision: feature.ScopeRevision, ProgressDigest: kernel.Digest(hex.EncodeToString(digest[:]))},
		Body: body, CreatedAt: now, ExpiresAt: now.Add(7 * 24 * time.Hour),
	}
	if message.Validate() != nil {
		return OrganizationalMessage{}, ErrInvalidFeature
	}
	return message, nil
}

func (coordinator *FeatureCoordinator) ensurePrimaryRole(ctx context.Context, role string) (RoleInstanceState, error) {
	configured, err := coordinator.host.ConfiguredRoleActors(role)
	if err != nil || len(configured) == 0 {
		return RoleInstanceState{}, errors.Join(ErrRoleNotRunning, err)
	}
	sort.Slice(configured, func(left, right int) bool { return configured[left] < configured[right] })
	primary := configured[0]
	state, found, statusErr := coordinator.host.Status(ctx, primary)
	if statusErr == nil && found && state.Status == RoleIdle {
		return state, nil
	}
	state, err = coordinator.host.EnsureStarted(ctx, primary)
	if err != nil || state.Status != RoleIdle {
		return RoleInstanceState{}, errors.Join(ErrRoleNotRunning, statusErr, err)
	}
	return state, nil
}

func planningRole(actor kernel.ActorFQN) bool {
	parts := strings.Split(string(actor), "::")
	if len(parts) != 2 {
		return false
	}
	roleInstance := parts[1]
	for _, role := range []string{"product-owner", "project-manager", "architect"} {
		if strings.HasPrefix(roleInstance, role+"-") {
			return true
		}
	}
	return false
}

func (coordinator *FeatureCoordinator) Read(ctx context.Context, id kernel.UUIDv7) (FeatureRequest, bool, error) {
	if coordinator == nil || !id.Valid() {
		return FeatureRequest{}, false, ErrInvalidFeature
	}
	return coordinator.store.LoadFeature(ctx, id)
}
