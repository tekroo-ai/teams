package organization

import (
	"sync"

	"github.com/tekroo-ai/teams/kernel"
)

// RoleInbox is an in-process notification cache over the durable Mongo inbox.
// It wakes role workers without claiming work and without invoking a model.
type RoleInbox struct {
	mu       sync.Mutex
	messages map[kernel.ActorFQN]map[kernel.UUIDv7]OrganizationalMessage
}

func NewRoleInbox() *RoleInbox {
	return &RoleInbox{messages: make(map[kernel.ActorFQN]map[kernel.UUIDv7]OrganizationalMessage)}
}

func (inbox *RoleInbox) Notify(message OrganizationalMessage) error {
	if inbox == nil || message.Validate() != nil {
		return ErrInvalidOrganizationalMessage
	}
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	byID := inbox.messages[message.Recipient]
	if byID == nil {
		byID = make(map[kernel.UUIDv7]OrganizationalMessage)
		inbox.messages[message.Recipient] = byID
	}
	byID[message.ID] = message
	return nil
}

func (inbox *RoleInbox) Snapshot(actor kernel.ActorFQN) []OrganizationalMessage {
	if inbox == nil {
		return nil
	}
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	byID := inbox.messages[actor]
	result := make([]OrganizationalMessage, 0, len(byID))
	for _, message := range byID {
		result = append(result, message)
	}
	return result
}

func (inbox *RoleInbox) Remove(actor kernel.ActorFQN, id kernel.UUIDv7) {
	if inbox == nil {
		return
	}
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	delete(inbox.messages[actor], id)
}
