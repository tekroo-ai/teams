package organization

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

const (
	FederationSchemaVersion = "1.0.0"
	MaximumFederationHops   = 16
	MaximumFederationTTL    = 5 * time.Minute
)

var (
	ErrInvalidFederation        = errors.New("invalid federation value")
	ErrFederationUnauthorized   = errors.New("federation route is not authorized")
	ErrFederationReplayConflict = errors.New("federation replay identity conflicts with retained content")
	ErrFederationClock          = errors.New("federation envelope is outside its accepted time window")
	aliasPattern                = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{0,62}[a-z0-9])?$`)
	identifierPattern           = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._:-]{0,254}[a-z0-9])?$`)
)

type FederationStatus string

const (
	FederationActive   FederationStatus = "ACTIVE"
	FederationDisabled FederationStatus = "DISABLED"
	FederationRevoked  FederationStatus = "REVOKED"
)

type AliasBinding struct {
	SchemaVersion      string          `json:"schema_version"`
	Name               string          `json:"name"`
	Revision           uint64          `json:"revision"`
	RouteID            kernel.UUIDv7   `json:"route_id"`
	RouteRevision      uint64          `json:"route_revision"`
	DeploymentIdentity kernel.Digest   `json:"deployment_identity"`
	ActorFQN           kernel.ActorFQN `json:"actor_fqn"`
}

func (binding AliasBinding) Validate() error {
	if binding.SchemaVersion != FederationSchemaVersion || !aliasPattern.MatchString(binding.Name) || strings.ContainsAny(binding.Name, "*?") || binding.Revision == 0 || !binding.RouteID.Valid() || binding.RouteRevision == 0 || !binding.DeploymentIdentity.Valid() || !binding.ActorFQN.Valid() {
		return ErrInvalidFederation
	}
	return nil
}

type FederationTrustGrant struct {
	SchemaVersion      string           `json:"schema_version"`
	PeerID             string           `json:"peer_id"`
	DeploymentIdentity kernel.Digest    `json:"deployment_identity"`
	KeyID              string           `json:"key_id"`
	KeyEpoch           uint64           `json:"key_epoch"`
	PublicKey          string           `json:"public_key"`
	NotBefore          time.Time        `json:"not_before"`
	NotAfter           time.Time        `json:"not_after"`
	Status             FederationStatus `json:"status"`
}

func (grant FederationTrustGrant) Validate() error {
	key, err := base64.StdEncoding.Strict().DecodeString(grant.PublicKey)
	if grant.SchemaVersion != FederationSchemaVersion || !identifierPattern.MatchString(grant.PeerID) || !grant.DeploymentIdentity.Valid() || !identifierPattern.MatchString(grant.KeyID) || grant.KeyEpoch == 0 || err != nil || len(key) != ed25519.PublicKeySize || grant.NotBefore.IsZero() || !grant.NotAfter.After(grant.NotBefore) || grant.Status != FederationActive && grant.Status != FederationRevoked {
		return ErrInvalidFederation
	}
	return nil
}

func (grant FederationTrustGrant) decodedKey() ed25519.PublicKey {
	key, _ := base64.StdEncoding.Strict().DecodeString(grant.PublicKey)
	return ed25519.PublicKey(key)
}

func (grant FederationTrustGrant) PublicEd25519Key() (ed25519.PublicKey, error) {
	if grant.Validate() != nil {
		return nil, ErrInvalidFederation
	}
	return append(ed25519.PublicKey(nil), grant.decodedKey()...), nil
}

type FederationRoute struct {
	SchemaVersion                string           `json:"schema_version"`
	RouteID                      kernel.UUIDv7    `json:"route_id"`
	Revision                     uint64           `json:"revision"`
	SourceDeployment             kernel.Digest    `json:"source_deployment"`
	DestinationDeployment        kernel.Digest    `json:"destination_deployment"`
	SourceActor                  kernel.ActorFQN  `json:"source_actor"`
	DestinationActor             kernel.ActorFQN  `json:"destination_actor"`
	MessageTypes                 []string         `json:"message_types"`
	Purposes                     []MessagePurpose `json:"purposes"`
	KeyID                        string           `json:"key_id"`
	Endpoint                     string           `json:"endpoint"`
	Status                       FederationStatus `json:"status"`
	AllowInsecureLoopbackForTest bool             `json:"allow_insecure_loopback_for_test,omitempty"`
}

func (route FederationRoute) Validate() error {
	endpoint, err := url.Parse(route.Endpoint)
	if route.SchemaVersion != FederationSchemaVersion || !route.RouteID.Valid() || route.Revision == 0 || !route.SourceDeployment.Valid() || !route.DestinationDeployment.Valid() || route.SourceDeployment == route.DestinationDeployment || !route.SourceActor.Valid() || !route.DestinationActor.Valid() || route.SourceActor == route.DestinationActor || !identifierPattern.MatchString(route.KeyID) || route.Status != FederationActive && route.Status != FederationDisabled || err != nil || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.Path != "/v1/federation/ingress" {
		return ErrInvalidFederation
	}
	if !validExactMessageTypes(route.MessageTypes) || !validPurposes(route.Purposes) {
		return ErrInvalidFederation
	}
	if endpoint.Scheme == "https" {
		return nil
	}
	if endpoint.Scheme != "http" || !route.AllowInsecureLoopbackForTest || !isLoopbackHost(endpoint.Hostname()) {
		return ErrInvalidFederation
	}
	return nil
}

func (route FederationRoute) Authorizes(message OrganizationalMessage) bool {
	return route.Validate() == nil && route.Status == FederationActive && message.Sender == route.SourceActor && message.Recipient == route.DestinationActor && slices.Contains(route.MessageTypes, message.Type) && slices.Contains(route.Purposes, message.Purpose)
}

type FederatedEnvelope struct {
	SchemaVersion         string          `json:"schema_version"`
	DeliveryID            kernel.UUIDv7   `json:"delivery_id"`
	ReplayID              kernel.UUIDv7   `json:"replay_id"`
	RouteID               kernel.UUIDv7   `json:"route_id"`
	RouteRevision         uint64          `json:"route_revision"`
	SourceDeployment      kernel.Digest   `json:"source_deployment"`
	DestinationDeployment kernel.Digest   `json:"destination_deployment"`
	SourceActor           kernel.ActorFQN `json:"source_actor"`
	DestinationActor      kernel.ActorFQN `json:"destination_actor"`
	KeyID                 string          `json:"key_id"`
	KeyEpoch              uint64          `json:"key_epoch"`
	IssuedAt              time.Time       `json:"issued_at"`
	ExpiresAt             time.Time       `json:"expires_at"`
	AliasName             string          `json:"alias_name,omitempty"`
	AliasRevision         uint64          `json:"alias_revision,omitempty"`
	MessageSHA256         kernel.Digest   `json:"message_sha256"`
	Message               json.RawMessage `json:"message"`
	RouteTrace            []kernel.Digest `json:"route_trace"`
	Signature             string          `json:"signature"`
}

type federationSigningMaterial struct {
	SchemaVersion         string          `json:"schema_version"`
	DeliveryID            kernel.UUIDv7   `json:"delivery_id"`
	ReplayID              kernel.UUIDv7   `json:"replay_id"`
	RouteID               kernel.UUIDv7   `json:"route_id"`
	RouteRevision         uint64          `json:"route_revision"`
	SourceDeployment      kernel.Digest   `json:"source_deployment"`
	DestinationDeployment kernel.Digest   `json:"destination_deployment"`
	SourceActor           kernel.ActorFQN `json:"source_actor"`
	DestinationActor      kernel.ActorFQN `json:"destination_actor"`
	KeyID                 string          `json:"key_id"`
	KeyEpoch              uint64          `json:"key_epoch"`
	IssuedAt              time.Time       `json:"issued_at"`
	ExpiresAt             time.Time       `json:"expires_at"`
	AliasName             string          `json:"alias_name,omitempty"`
	AliasRevision         uint64          `json:"alias_revision,omitempty"`
	MessageSHA256         kernel.Digest   `json:"message_sha256"`
	RouteTrace            []kernel.Digest `json:"route_trace"`
}

func (envelope FederatedEnvelope) signingMaterial() federationSigningMaterial {
	return federationSigningMaterial{SchemaVersion: envelope.SchemaVersion, DeliveryID: envelope.DeliveryID, ReplayID: envelope.ReplayID, RouteID: envelope.RouteID, RouteRevision: envelope.RouteRevision, SourceDeployment: envelope.SourceDeployment, DestinationDeployment: envelope.DestinationDeployment, SourceActor: envelope.SourceActor, DestinationActor: envelope.DestinationActor, KeyID: envelope.KeyID, KeyEpoch: envelope.KeyEpoch, IssuedAt: envelope.IssuedAt.UTC(), ExpiresAt: envelope.ExpiresAt.UTC(), AliasName: envelope.AliasName, AliasRevision: envelope.AliasRevision, MessageSHA256: envelope.MessageSHA256, RouteTrace: slices.Clone(envelope.RouteTrace)}
}

func NewFederatedEnvelope(message OrganizationalMessage, route FederationRoute, alias *AliasBinding, grant FederationTrustGrant, deliveryID, replayID kernel.UUIDv7, issuedAt time.Time, ttl time.Duration, privateKey ed25519.PrivateKey) (FederatedEnvelope, error) {
	if message.Validate() != nil || route.Validate() != nil || grant.Validate() != nil || !route.Authorizes(message) || route.SourceDeployment != grant.DeploymentIdentity || route.KeyID != grant.KeyID || grant.Status != FederationActive || !deliveryID.Valid() || !replayID.Valid() || issuedAt.IsZero() || ttl <= 0 || ttl > MaximumFederationTTL || len(privateKey) != ed25519.PrivateKeySize {
		return FederatedEnvelope{}, ErrInvalidFederation
	}
	if issuedAt.Before(grant.NotBefore) || !issuedAt.Before(grant.NotAfter) || issuedAt.Add(ttl).After(grant.NotAfter) {
		return FederatedEnvelope{}, ErrFederationClock
	}
	if alias != nil && (alias.Validate() != nil || alias.RouteID != route.RouteID || alias.RouteRevision != route.Revision || alias.DeploymentIdentity != route.DestinationDeployment || alias.ActorFQN != route.DestinationActor) {
		return FederatedEnvelope{}, ErrInvalidFederation
	}
	raw, err := json.Marshal(message)
	if err != nil {
		return FederatedEnvelope{}, err
	}
	digest := sha256.Sum256(raw)
	envelope := FederatedEnvelope{SchemaVersion: FederationSchemaVersion, DeliveryID: deliveryID, ReplayID: replayID, RouteID: route.RouteID, RouteRevision: route.Revision, SourceDeployment: route.SourceDeployment, DestinationDeployment: route.DestinationDeployment, SourceActor: route.SourceActor, DestinationActor: route.DestinationActor, KeyID: grant.KeyID, KeyEpoch: grant.KeyEpoch, IssuedAt: issuedAt.UTC(), ExpiresAt: issuedAt.Add(ttl).UTC(), MessageSHA256: kernel.Digest(hex.EncodeToString(digest[:])), Message: raw, RouteTrace: []kernel.Digest{route.SourceDeployment}}
	if alias != nil {
		envelope.AliasName, envelope.AliasRevision = alias.Name, alias.Revision
	}
	material, err := json.Marshal(envelope.signingMaterial())
	if err != nil {
		return FederatedEnvelope{}, err
	}
	envelope.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, material))
	return envelope, nil
}

func (envelope FederatedEnvelope) DecodeAndVerify(route FederationRoute, grant FederationTrustGrant, destination kernel.Digest, now time.Time, allowedFutureSkew time.Duration) (OrganizationalMessage, error) {
	aliasValid := envelope.AliasName == "" && envelope.AliasRevision == 0 || aliasPattern.MatchString(envelope.AliasName) && envelope.AliasRevision > 0
	if route.Validate() != nil || grant.Validate() != nil || envelope.SchemaVersion != FederationSchemaVersion || !envelope.DeliveryID.Valid() || !envelope.ReplayID.Valid() || !envelope.RouteID.Valid() || envelope.RouteID != route.RouteID || envelope.RouteRevision != route.Revision || envelope.SourceDeployment != route.SourceDeployment || envelope.DestinationDeployment != route.DestinationDeployment || envelope.DestinationDeployment != destination || envelope.SourceActor != route.SourceActor || envelope.DestinationActor != route.DestinationActor || envelope.KeyID != route.KeyID || envelope.KeyID != grant.KeyID || envelope.KeyEpoch != grant.KeyEpoch || envelope.SourceDeployment != grant.DeploymentIdentity || route.Status != FederationActive || grant.Status != FederationActive || now.IsZero() || allowedFutureSkew < 0 || len(envelope.Message) == 0 || !envelope.MessageSHA256.Valid() || !validRouteTrace(envelope.RouteTrace, route.SourceDeployment, destination) || !aliasValid {
		return OrganizationalMessage{}, ErrFederationUnauthorized
	}
	if now.Add(allowedFutureSkew).Before(envelope.IssuedAt) || envelope.IssuedAt.Before(grant.NotBefore) || !envelope.IssuedAt.Before(grant.NotAfter) || envelope.ExpiresAt.After(grant.NotAfter) || now.Before(grant.NotBefore) || !now.Before(grant.NotAfter) || !envelope.ExpiresAt.After(envelope.IssuedAt) || envelope.ExpiresAt.Sub(envelope.IssuedAt) > MaximumFederationTTL || !now.Before(envelope.ExpiresAt) {
		return OrganizationalMessage{}, ErrFederationClock
	}
	digest := sha256.Sum256(envelope.Message)
	if !bytes.Equal([]byte(envelope.MessageSHA256), []byte(hex.EncodeToString(digest[:]))) {
		return OrganizationalMessage{}, ErrFederationUnauthorized
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(envelope.Signature)
	material, materialErr := json.Marshal(envelope.signingMaterial())
	if err != nil || materialErr != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(grant.decodedKey(), material, signature) {
		return OrganizationalMessage{}, ErrFederationUnauthorized
	}
	var message OrganizationalMessage
	if decodeStrict(envelope.Message, &message) != nil || message.Validate() != nil || !route.Authorizes(message) || message.Sender != envelope.SourceActor || message.Recipient != envelope.DestinationActor || message.CreatedAt.After(envelope.IssuedAt) || !now.Before(message.ExpiresAt) {
		return OrganizationalMessage{}, ErrFederationUnauthorized
	}
	return message, nil
}

type FederationDeliveryReceipt struct {
	SchemaVersion string        `json:"schema_version"`
	DeliveryID    kernel.UUIDv7 `json:"delivery_id"`
	ReplayID      kernel.UUIDv7 `json:"replay_id"`
	RouteID       kernel.UUIDv7 `json:"route_id"`
	RouteRevision uint64        `json:"route_revision"`
	MessageID     kernel.UUIDv7 `json:"message_id"`
	MessageSHA256 kernel.Digest `json:"message_sha256"`
	AcceptedAt    time.Time     `json:"accepted_at"`
	Outcome       string        `json:"outcome"`
}

func (receipt FederationDeliveryReceipt) Valid() bool {
	return receipt.SchemaVersion == FederationSchemaVersion && receipt.DeliveryID.Valid() && receipt.ReplayID.Valid() && receipt.RouteID.Valid() && receipt.RouteRevision > 0 && receipt.MessageID.Valid() && receipt.MessageSHA256.Valid() && !receipt.AcceptedAt.IsZero() && receipt.Outcome == "ACCEPTED"
}

type FederationRegistry interface {
	ResolveAlias(context.Context, string) (AliasBinding, bool, error)
	LoadRoute(context.Context, kernel.UUIDv7, uint64) (FederationRoute, bool, error)
	LoadTrustGrant(context.Context, string, uint64) (FederationTrustGrant, bool, error)
}

type FederationInboundStore interface {
	AcceptFederatedMessage(context.Context, FederatedEnvelope, OrganizationalMessage, time.Time) (FederationDeliveryReceipt, bool, error)
}

type FederationIngress struct {
	registry    FederationRegistry
	store       FederationInboundStore
	destination kernel.Digest
	futureSkew  time.Duration
}

func NewFederationIngress(registry FederationRegistry, store FederationInboundStore, destination kernel.Digest, futureSkew time.Duration) (*FederationIngress, error) {
	if registry == nil || store == nil || !destination.Valid() || futureSkew < 0 || futureSkew > time.Minute {
		return nil, ErrInvalidFederation
	}
	return &FederationIngress{registry: registry, store: store, destination: destination, futureSkew: futureSkew}, nil
}

func (ingress *FederationIngress) Accept(ctx context.Context, envelope FederatedEnvelope, now time.Time) (FederationDeliveryReceipt, bool, error) {
	if ingress == nil || ingress.registry == nil || ingress.store == nil {
		return FederationDeliveryReceipt{}, false, ErrInvalidFederation
	}
	route, found, err := ingress.registry.LoadRoute(ctx, envelope.RouteID, envelope.RouteRevision)
	if err != nil || !found {
		return FederationDeliveryReceipt{}, false, errors.Join(ErrFederationUnauthorized, err)
	}
	grant, found, err := ingress.registry.LoadTrustGrant(ctx, envelope.KeyID, envelope.KeyEpoch)
	if err != nil || !found {
		return FederationDeliveryReceipt{}, false, errors.Join(ErrFederationUnauthorized, err)
	}
	message, err := envelope.DecodeAndVerify(route, grant, ingress.destination, now, ingress.futureSkew)
	if err != nil {
		return FederationDeliveryReceipt{}, false, err
	}
	return ingress.store.AcceptFederatedMessage(ctx, envelope, message, now)
}

type StaticFederationRegistry struct {
	aliases map[string]AliasBinding
	routes  map[string]FederationRoute
	grants  map[string]FederationTrustGrant
}

func NewStaticFederationRegistry(aliases []AliasBinding, routes []FederationRoute, grants []FederationTrustGrant) (*StaticFederationRegistry, error) {
	registry := &StaticFederationRegistry{aliases: make(map[string]AliasBinding, len(aliases)), routes: make(map[string]FederationRoute, len(routes)), grants: make(map[string]FederationTrustGrant, len(grants))}
	for _, alias := range aliases {
		if alias.Validate() != nil {
			return nil, ErrInvalidFederation
		}
		if _, exists := registry.aliases[alias.Name]; exists {
			return nil, ErrInvalidFederation
		}
		registry.aliases[alias.Name] = alias
	}
	for _, route := range routes {
		if route.Validate() != nil {
			return nil, ErrInvalidFederation
		}
		key := routeKey(route.RouteID, route.Revision)
		if _, exists := registry.routes[key]; exists {
			return nil, ErrInvalidFederation
		}
		registry.routes[key] = route
	}
	for _, grant := range grants {
		if grant.Validate() != nil {
			return nil, ErrInvalidFederation
		}
		key := grantKey(grant.KeyID, grant.KeyEpoch)
		if _, exists := registry.grants[key]; exists {
			return nil, ErrInvalidFederation
		}
		registry.grants[key] = grant
	}
	for _, alias := range aliases {
		route, found := registry.routes[routeKey(alias.RouteID, alias.RouteRevision)]
		if !found || route.DestinationDeployment != alias.DeploymentIdentity || route.DestinationActor != alias.ActorFQN {
			return nil, ErrInvalidFederation
		}
	}
	for _, route := range routes {
		authorizedKey := false
		for _, grant := range grants {
			if grant.KeyID == route.KeyID && grant.DeploymentIdentity == route.SourceDeployment {
				authorizedKey = true
				break
			}
		}
		if !authorizedKey {
			return nil, ErrInvalidFederation
		}
	}
	return registry, nil
}

func (registry *StaticFederationRegistry) ResolveAlias(_ context.Context, name string) (AliasBinding, bool, error) {
	value, found := registry.aliases[name]
	return value, found, nil
}

func (registry *StaticFederationRegistry) LoadRoute(_ context.Context, id kernel.UUIDv7, revision uint64) (FederationRoute, bool, error) {
	value, found := registry.routes[routeKey(id, revision)]
	return value, found, nil
}

func (registry *StaticFederationRegistry) LoadTrustGrant(_ context.Context, keyID string, epoch uint64) (FederationTrustGrant, bool, error) {
	value, found := registry.grants[grantKey(keyID, epoch)]
	return value, found, nil
}

func (registry *StaticFederationRegistry) Aliases() []AliasBinding {
	values := make([]AliasBinding, 0, len(registry.aliases))
	for _, value := range registry.aliases {
		values = append(values, value)
	}
	slices.SortFunc(values, func(a, b AliasBinding) int { return strings.Compare(a.Name, b.Name) })
	return values
}

func (registry *StaticFederationRegistry) Routes() []FederationRoute {
	values := make([]FederationRoute, 0, len(registry.routes))
	for _, value := range registry.routes {
		values = append(values, value)
	}
	slices.SortFunc(values, func(a, b FederationRoute) int {
		if a.RouteID == b.RouteID {
			return int(a.Revision) - int(b.Revision)
		}
		return strings.Compare(string(a.RouteID), string(b.RouteID))
	})
	return values
}

func (registry *StaticFederationRegistry) TrustGrants() []FederationTrustGrant {
	values := make([]FederationTrustGrant, 0, len(registry.grants))
	for _, value := range registry.grants {
		values = append(values, value)
	}
	slices.SortFunc(values, func(a, b FederationTrustGrant) int {
		if a.KeyID == b.KeyID {
			return int(a.KeyEpoch) - int(b.KeyEpoch)
		}
		return strings.Compare(a.KeyID, b.KeyID)
	})
	return values
}

func validExactMessageTypes(values []string) bool {
	if len(values) == 0 || len(values) > 64 {
		return false
	}
	prior := ""
	for _, value := range values {
		if !messageTypePattern.MatchString(value) || value <= prior || strings.ContainsAny(value, "*?") {
			return false
		}
		prior = value
	}
	return true
}

func validPurposes(values []MessagePurpose) bool {
	if len(values) == 0 || len(values) > 5 {
		return false
	}
	prior := ""
	for _, value := range values {
		if !value.Valid() || string(value) <= prior {
			return false
		}
		prior = string(value)
	}
	return true
}

func validRouteTrace(trace []kernel.Digest, source, destination kernel.Digest) bool {
	if len(trace) == 0 || len(trace) >= MaximumFederationHops || trace[0] != source {
		return false
	}
	seen := make(map[kernel.Digest]struct{}, len(trace)+1)
	for _, value := range trace {
		if !value.Valid() || value == destination {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func routeKey(id kernel.UUIDv7, revision uint64) string {
	return string(id) + ":" + jsonNumber(revision)
}
func grantKey(id string, epoch uint64) string { return id + ":" + jsonNumber(epoch) }

func jsonNumber(value uint64) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
