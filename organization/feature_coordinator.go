package organization

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
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
	productOwners, err := coordinator.host.ResolveRoleRecipients(ctx, "product-owner")
	if err != nil || len(productOwners) != 1 {
		return FeatureRequest{}, false, errors.Join(ErrRoleNotRunning, err)
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
		Input: input, Status: FeatureSubmitted, OperatorActor: operator.ActorFQN, ProductOwnerActor: productOwners[0],
		InitialMessageID: messageID, BudgetAccountID: budgetID, LifecycleEpoch: 1, ScopeRevision: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	body, err := json.Marshal(map[string]any{"feature_id": featureID, "request": input, "submitted_by": principal})
	if err != nil {
		return FeatureRequest{}, false, err
	}
	digest := sha256.Sum256(body)
	message := OrganizationalMessage{
		SchemaVersion: OrganizationalMessageSchemaVersion, ID: messageID, Type: "tekroo.message.feature.requested", Purpose: PurposeRequest,
		Sender: operator.ActorFQN, SenderExecution: operator.Execution, Recipient: productOwners[0], CorrelationID: featureID,
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
	if feature.Revision != expectedRevision || feature.Status == FeatureCancelled {
		return FeatureRequest{}, ErrFeatureRevisionConflict
	}
	if plan.Validate(feature) != nil || !planningRole(plan.PreparedBy) {
		return FeatureRequest{}, ErrInvalidFeature
	}
	planner, found, err := coordinator.host.Status(ctx, plan.PreparedBy)
	if err != nil || !found || planner.Status != RoleIdle || planner.Execution != plan.PreparedExecution {
		return FeatureRequest{}, errors.Join(ErrStaleOrganizationalClaim, err)
	}
	for _, task := range plan.Tasks {
		owner, active, ownerErr := coordinator.host.Status(ctx, task.Owner)
		if ownerErr != nil || !active || owner.Status != RoleIdle || owner.ModelProfile != task.ModelProfile {
			return FeatureRequest{}, errors.Join(ErrRoleNotRunning, ownerErr)
		}
	}
	if err := coordinator.materialize.MaterializeFeaturePlan(ctx, feature, plan); err != nil {
		return FeatureRequest{}, err
	}
	return coordinator.store.ApplyFeaturePlan(ctx, feature.ID, expectedRevision, plan, coordinator.clock.Now().UTC())
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
