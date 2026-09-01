package organization

import (
	"context"
	"sync"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

// MemoryFederationStore is the deterministic reference implementation used by
// unit tests and local compositions. The MongoDB adapter provides the durable
// transactional implementation for tekrood.
type MemoryFederationStore struct {
	mu         sync.Mutex
	messages   OrganizationalMessageStore
	byReplay   map[kernel.UUIDv7]FederationDeliveryReceipt
	byDelivery map[kernel.UUIDv7]FederationDeliveryReceipt
}

func NewMemoryFederationStore(messages OrganizationalMessageStore) (*MemoryFederationStore, error) {
	if messages == nil {
		return nil, ErrInvalidFederation
	}
	return &MemoryFederationStore{messages: messages, byReplay: make(map[kernel.UUIDv7]FederationDeliveryReceipt), byDelivery: make(map[kernel.UUIDv7]FederationDeliveryReceipt)}, nil
}

func (store *MemoryFederationStore) AcceptFederatedMessage(ctx context.Context, envelope FederatedEnvelope, message OrganizationalMessage, now time.Time) (FederationDeliveryReceipt, bool, error) {
	if store == nil || store.messages == nil || message.Validate() != nil || now.IsZero() {
		return FederationDeliveryReceipt{}, false, ErrInvalidFederation
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if retained, found := store.byReplay[envelope.ReplayID]; found {
		if retained.DeliveryID != envelope.DeliveryID || retained.RouteID != envelope.RouteID || retained.RouteRevision != envelope.RouteRevision || retained.MessageID != message.ID || retained.MessageSHA256 != envelope.MessageSHA256 {
			return FederationDeliveryReceipt{}, false, ErrFederationReplayConflict
		}
		return retained, false, nil
	}
	if retained, found := store.byDelivery[envelope.DeliveryID]; found {
		if retained.ReplayID != envelope.ReplayID || retained.MessageSHA256 != envelope.MessageSHA256 {
			return FederationDeliveryReceipt{}, false, ErrFederationReplayConflict
		}
		return retained, false, nil
	}
	if err := store.messages.AppendMessage(ctx, message); err != nil {
		return FederationDeliveryReceipt{}, false, err
	}
	receipt := FederationDeliveryReceipt{SchemaVersion: FederationSchemaVersion, DeliveryID: envelope.DeliveryID, ReplayID: envelope.ReplayID, RouteID: envelope.RouteID, RouteRevision: envelope.RouteRevision, MessageID: message.ID, MessageSHA256: envelope.MessageSHA256, AcceptedAt: now.UTC(), Outcome: "ACCEPTED"}
	store.byReplay[envelope.ReplayID] = receipt
	store.byDelivery[envelope.DeliveryID] = receipt
	return receipt, true, nil
}
