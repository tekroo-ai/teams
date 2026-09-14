package organization

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/tekroo-ai/teams/kernel"
)

var (
	ErrRoleHandlerUnavailable = errors.New("role handler unavailable")
	ErrRoleHandlerMismatch    = errors.New("role handler does not match the admitted message")
)

type RoleHandlerDispatch struct {
	ActorFQN                kernel.ActorFQN
	RoleFQRN                kernel.RoleFQRN
	BundleVersion           string
	BundleDigest            kernel.Digest
	Capabilities            []string
	Permissions             []string
	MessageType             string
	MessagePurpose          MessagePurpose
	SubscriptionPurpose     string
	Charter                 string
	CharterDigest           kernel.Digest
	Disposition             HandlerDisposition
	HandlerInstructions     string
	HandlerDigest           kernel.Digest
	InputSchema             []byte
	InputSchemaDigest       kernel.Digest
	ResultSchema            []byte
	ResultSchemaDigest      kernel.Digest
	DeterministicOperation  string
	AllowedResults          []string
	AllowedMessageProposals []string
}

func (dispatch RoleHandlerDispatch) Valid() bool {
	if !dispatch.ActorFQN.Valid() || !dispatch.RoleFQRN.Valid() || dispatch.BundleVersion == "" || !dispatch.BundleDigest.Valid() || !sortedUniqueNonempty(dispatch.Capabilities, 128, 256) || !sortedUniqueNonempty(dispatch.Permissions, 128, 256) || !messageTypePattern.MatchString(dispatch.MessageType) || !dispatch.MessagePurpose.Valid() || dispatch.SubscriptionPurpose == "" || dispatch.Charter == "" || !dispatch.CharterDigest.Valid() || !dispatch.Disposition.Valid() || !sortedUniqueNonempty(dispatch.AllowedResults, 16, 64) || !slices.IsSorted(dispatch.AllowedMessageProposals) {
		return false
	}
	fqrn, err := kernel.RoleFQRNFromActor(dispatch.ActorFQN)
	if err != nil || fqrn != dispatch.RoleFQRN {
		return false
	}
	switch dispatch.Disposition {
	case HandlerModel:
		return dispatch.HandlerInstructions != "" && dispatch.HandlerDigest.Valid() && len(dispatch.InputSchema) != 0 && dispatch.InputSchemaDigest.Valid() && len(dispatch.ResultSchema) != 0 && dispatch.ResultSchemaDigest.Valid() && dispatch.DeterministicOperation == ""
	case HandlerDeterministic:
		return dispatch.HandlerInstructions == "" && dispatch.HandlerDigest == "" && len(dispatch.InputSchema) != 0 && dispatch.InputSchemaDigest.Valid() && len(dispatch.ResultSchema) != 0 && dispatch.ResultSchemaDigest.Valid() && dispatch.DeterministicOperation != ""
	case HandlerObserveOnly:
		return dispatch.HandlerInstructions == "" && dispatch.HandlerDigest == "" && len(dispatch.InputSchema) == 0 && dispatch.InputSchemaDigest == "" && len(dispatch.ResultSchema) == 0 && dispatch.ResultSchemaDigest == "" && dispatch.DeterministicOperation == "" && len(dispatch.AllowedMessageProposals) == 0
	default:
		return false
	}
}

type RoleHandlerResolver struct {
	mu            sync.RWMutex
	team          string
	currentByFQRN map[kernel.RoleFQRN]kernel.Digest
	rolesByDigest map[kernel.Digest]LoadedRole
}

func NewRoleHandlerResolver(team LoadedTeam) (*RoleHandlerResolver, error) {
	resolver := &RoleHandlerResolver{}
	if err := resolver.Sync(team); err != nil {
		return nil, err
	}
	return resolver, nil
}

// Sync atomically selects one accepted package version for new work while
// retaining already verified versions for invocations whose immutable handler
// binding predates the replacement.
func (resolver *RoleHandlerResolver) Sync(team LoadedTeam) error {
	if resolver == nil || team.Manifest.Validate() != nil || !team.Digest.Valid() || len(team.Roles) != len(team.Manifest.Roles) {
		return ErrRoleHandlerUnavailable
	}
	current := make(map[kernel.RoleFQRN]kernel.Digest, len(team.Roles))
	loadedByDigest := make(map[kernel.Digest]LoadedRole, len(team.Roles))
	for _, loaded := range team.Roles {
		if loaded.Bundle.SchemaVersion != RolePackageSchemaVersion || loaded.Package == nil || loaded.Bundle.Validate() != nil || loaded.Binding.Role != loaded.Bundle.Role {
			continue
		}
		digest, err := loaded.Bundle.ContentDigest()
		if err != nil || digest != loaded.Binding.BundleDigest {
			return ErrRoleHandlerUnavailable
		}
		fqrn, err := kernel.ParseRoleFQRN(loaded.Binding.Role)
		if err != nil {
			return ErrRoleHandlerUnavailable
		}
		current[fqrn] = digest
		loadedByDigest[digest] = cloneLoadedRoleForDispatch(loaded)
	}
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	if resolver.team != "" && resolver.team != team.Manifest.Team {
		return ErrRoleHandlerUnavailable
	}
	history := make(map[kernel.Digest]LoadedRole, len(resolver.rolesByDigest)+len(loadedByDigest))
	for digest, loaded := range resolver.rolesByDigest {
		history[digest] = loaded
	}
	for digest, loaded := range loadedByDigest {
		if prior, found := history[digest]; found && prior.Bundle.Role != loaded.Bundle.Role {
			return ErrRoleHandlerUnavailable
		}
		history[digest] = loaded
	}
	resolver.team = team.Manifest.Team
	resolver.currentByFQRN = current
	resolver.rolesByDigest = history
	return nil
}

func (resolver *RoleHandlerResolver) Resolve(actor kernel.ActorFQN, messageType, subscriptionPurpose string) (RoleHandlerDispatch, error) {
	if resolver == nil || !actor.Valid() || !messageTypePattern.MatchString(messageType) || subscriptionPurpose == "" {
		return RoleHandlerDispatch{}, ErrRoleHandlerUnavailable
	}
	fqrn, err := kernel.RoleFQRNFromActor(actor)
	resolver.mu.RLock()
	team := resolver.team
	digest, found := resolver.currentByFQRN[fqrn]
	resolver.mu.RUnlock()
	if err != nil || !stringsHasTeam(actor, team) || !found {
		return RoleHandlerDispatch{}, ErrRoleHandlerUnavailable
	}
	return resolver.ResolveVersion(actor, digest, messageType, subscriptionPurpose)
}

func (resolver *RoleHandlerResolver) ResolveVersion(actor kernel.ActorFQN, bundleDigest kernel.Digest, messageType, subscriptionPurpose string) (RoleHandlerDispatch, error) {
	if resolver == nil || !actor.Valid() || !bundleDigest.Valid() || !messageTypePattern.MatchString(messageType) || subscriptionPurpose == "" {
		return RoleHandlerDispatch{}, ErrRoleHandlerUnavailable
	}
	fqrn, err := kernel.RoleFQRNFromActor(actor)
	resolver.mu.RLock()
	team := resolver.team
	loaded, found := resolver.rolesByDigest[bundleDigest]
	resolver.mu.RUnlock()
	if err != nil || !stringsHasTeam(actor, team) || !found || loaded.Bundle.Role != string(fqrn) || loaded.Package == nil || loaded.Bundle.Charter == nil {
		return RoleHandlerDispatch{}, ErrRoleHandlerUnavailable
	}
	handler, found := loaded.Package.Handler(messageType, subscriptionPurpose)
	if !found {
		return RoleHandlerDispatch{}, ErrRoleHandlerMismatch
	}
	dispatch := RoleHandlerDispatch{
		ActorFQN: actor, RoleFQRN: fqrn, BundleVersion: loaded.Bundle.Version, BundleDigest: loaded.Binding.BundleDigest,
		Capabilities: slices.Clone(loaded.Bundle.Capabilities), Permissions: slices.Clone(loaded.Bundle.Permissions),
		MessageType: messageType, MessagePurpose: handler.Binding.MessagePurpose, SubscriptionPurpose: subscriptionPurpose,
		Charter: loaded.Package.Charter, CharterDigest: loaded.Bundle.Charter.SHA256,
		Disposition: handler.Binding.Disposition, HandlerInstructions: handler.Instructions,
		InputSchema: slices.Clone(handler.InputSchema), ResultSchema: slices.Clone(handler.ResultSchema),
		DeterministicOperation: handler.Binding.DeterministicOperation,
		AllowedResults:         slices.Clone(handler.Binding.AllowedResults), AllowedMessageProposals: slices.Clone(handler.Binding.AllowedMessageProposals),
	}
	if handler.Binding.Resource != nil {
		dispatch.HandlerDigest = handler.Binding.Resource.SHA256
	}
	if handler.Binding.InputSchema != nil {
		dispatch.InputSchemaDigest = handler.Binding.InputSchema.SHA256
	}
	if handler.Binding.ResultSchema != nil {
		dispatch.ResultSchemaDigest = handler.Binding.ResultSchema.SHA256
	}
	if !dispatch.Valid() {
		return RoleHandlerDispatch{}, ErrRoleHandlerUnavailable
	}
	return dispatch, nil
}

// ResolveMessage selects the one handler declared for the exact receiving role
// and message type. Message purpose remains part of the invocation binding and
// is validated against the admitted message itself; subscription purpose is
// the role package's semantic dispatch key.
func (resolver *RoleHandlerResolver) ResolveMessage(actor kernel.ActorFQN, message OrganizationalMessage) (RoleHandlerDispatch, error) {
	if resolver == nil || message.Validate() != nil || message.Recipient != actor {
		return RoleHandlerDispatch{}, ErrRoleHandlerMismatch
	}
	fqrn, err := kernel.RoleFQRNFromActor(actor)
	if err != nil {
		return RoleHandlerDispatch{}, ErrRoleHandlerUnavailable
	}
	resolver.mu.RLock()
	digest, found := resolver.currentByFQRN[fqrn]
	loaded := resolver.rolesByDigest[digest]
	resolver.mu.RUnlock()
	if !found {
		return RoleHandlerDispatch{}, ErrRoleHandlerUnavailable
	}
	binding, found := loaded.Bundle.HandlerBindings[message.Type]
	if !found || binding.MessagePurpose != message.Purpose {
		return RoleHandlerDispatch{}, ErrRoleHandlerMismatch
	}
	return resolver.Resolve(actor, message.Type, binding.SubscriptionPurpose)
}

func (resolver *RoleHandlerResolver) RequiresHandler(actor kernel.ActorFQN) bool {
	if resolver == nil {
		return false
	}
	fqrn, err := kernel.RoleFQRNFromActor(actor)
	if err != nil {
		return false
	}
	resolver.mu.RLock()
	_, found := resolver.currentByFQRN[fqrn]
	resolver.mu.RUnlock()
	return found
}

func cloneLoadedRoleForDispatch(loaded LoadedRole) LoadedRole {
	clone := loaded
	clone.Binding.WorkspaceIDs = slices.Clone(loaded.Binding.WorkspaceIDs)
	clone.Bundle.Capabilities = slices.Clone(loaded.Bundle.Capabilities)
	clone.Bundle.Subscriptions = slices.Clone(loaded.Bundle.Subscriptions)
	clone.Bundle.Permissions = slices.Clone(loaded.Bundle.Permissions)
	clone.Bundle.Handlers = cloneMap(loaded.Bundle.Handlers)
	clone.Bundle.Charter = cloneRoleResource(loaded.Bundle.Charter)
	clone.Bundle.HandlerBindings = cloneHandlerBindings(loaded.Bundle.HandlerBindings)
	if loaded.Package != nil {
		rolePackage := &LoadedRolePackage{Charter: loaded.Package.Charter, Handlers: make(map[string]LoadedRoleHandler, len(loaded.Package.Handlers))}
		for messageType, handler := range loaded.Package.Handlers {
			handler.Binding = cloneHandlerBinding(handler.Binding)
			handler.InputSchema = slices.Clone(handler.InputSchema)
			handler.ResultSchema = slices.Clone(handler.ResultSchema)
			rolePackage.Handlers[messageType] = handler
		}
		clone.Package = rolePackage
	}
	return clone
}

func stringsHasTeam(actor kernel.ActorFQN, team string) bool {
	return len(actor) > len(team)+2 && string(actor[:len(team)+2]) == team+"::"
}

func (dispatch RoleHandlerDispatch) String() string {
	return fmt.Sprintf("%s:%s/%s@%s", dispatch.RoleFQRN, dispatch.MessageType, dispatch.SubscriptionPurpose, dispatch.HandlerDigest)
}
