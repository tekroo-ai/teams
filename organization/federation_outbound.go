package organization

import (
	"context"
	"crypto/ed25519"
	"errors"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

type FederationTransport interface {
	Send(context.Context, FederationRoute, FederatedEnvelope) (FederationDeliveryReceipt, error)
}

type FederationOutboundStore interface {
	RecordFederatedOutbound(context.Context, FederatedEnvelope, OrganizationalMessage, time.Time) (bool, error)
}

type FederationCoordinator struct {
	registry   FederationRegistry
	roles      RoleStateStore
	store      FederationOutboundStore
	transport  FederationTransport
	clock      kernel.Clock
	ids        kernel.IDSource
	local      kernel.Digest
	signing    FederationTrustGrant
	privateKey ed25519.PrivateKey
	ttl        time.Duration
}

func NewFederationCoordinator(registry FederationRegistry, roles RoleStateStore, store FederationOutboundStore, transport FederationTransport, clock kernel.Clock, ids kernel.IDSource, local kernel.Digest, signing FederationTrustGrant, privateKey ed25519.PrivateKey, ttl time.Duration) (*FederationCoordinator, error) {
	if registry == nil || roles == nil || store == nil || transport == nil || clock == nil || ids == nil || !local.Valid() || signing.Validate() != nil || signing.DeploymentIdentity != local || signing.Status != FederationActive || len(privateKey) != ed25519.PrivateKeySize || ttl <= 0 || ttl > MaximumFederationTTL {
		return nil, ErrInvalidFederation
	}
	public := privateKey.Public().(ed25519.PublicKey)
	if !public.Equal(signing.decodedKey()) {
		return nil, ErrInvalidFederation
	}
	return &FederationCoordinator{registry: registry, roles: roles, store: store, transport: transport, clock: clock, ids: ids, local: local, signing: signing, privateKey: append(ed25519.PrivateKey(nil), privateKey...), ttl: ttl}, nil
}

func (coordinator *FederationCoordinator) ResolveAlias(ctx context.Context, name string) (AliasBinding, FederationRoute, error) {
	if coordinator == nil || coordinator.registry == nil || !aliasPattern.MatchString(name) {
		return AliasBinding{}, FederationRoute{}, ErrInvalidFederation
	}
	alias, found, err := coordinator.registry.ResolveAlias(ctx, name)
	if err != nil || !found || alias.Validate() != nil {
		return AliasBinding{}, FederationRoute{}, errors.Join(ErrFederationUnauthorized, err)
	}
	route, found, err := coordinator.registry.LoadRoute(ctx, alias.RouteID, alias.RouteRevision)
	if err != nil || !found || route.Validate() != nil || route.Status != FederationActive || route.SourceDeployment != coordinator.local || route.DestinationDeployment != alias.DeploymentIdentity || route.DestinationActor != alias.ActorFQN || route.KeyID != coordinator.signing.KeyID {
		return AliasBinding{}, FederationRoute{}, errors.Join(ErrFederationUnauthorized, err)
	}
	return alias, route, nil
}

func (coordinator *FederationCoordinator) SendByAlias(ctx context.Context, aliasName string, message OrganizationalMessage) (FederationDeliveryReceipt, error) {
	alias, route, err := coordinator.ResolveAlias(ctx, aliasName)
	if err != nil {
		return FederationDeliveryReceipt{}, err
	}
	if message.Recipient == "" {
		message.Recipient = alias.ActorFQN
	}
	if message.Recipient != alias.ActorFQN || message.Sender != route.SourceActor || message.Validate() != nil {
		return FederationDeliveryReceipt{}, ErrFederationUnauthorized
	}
	role, found, err := coordinator.roles.LoadRole(ctx, message.Sender)
	if err != nil || !found || role.Status != RoleIdle || role.Execution != message.SenderExecution {
		return FederationDeliveryReceipt{}, errors.Join(ErrStaleOrganizationalClaim, err)
	}
	deliveryID, err := coordinator.ids.Next()
	if err != nil {
		return FederationDeliveryReceipt{}, err
	}
	replayID, err := coordinator.ids.Next()
	if err != nil {
		return FederationDeliveryReceipt{}, err
	}
	now := coordinator.clock.Now().UTC()
	envelope, err := NewFederatedEnvelope(message, route, &alias, coordinator.signing, deliveryID, replayID, now, coordinator.ttl, coordinator.privateKey)
	if err != nil {
		return FederationDeliveryReceipt{}, err
	}
	if _, err := coordinator.store.RecordFederatedOutbound(ctx, envelope, message, now); err != nil {
		return FederationDeliveryReceipt{}, err
	}
	return coordinator.transport.Send(ctx, route, envelope)
}
