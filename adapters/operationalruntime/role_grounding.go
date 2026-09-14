package operationalruntime

import (
	"context"
	"fmt"
	"slices"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

type boundRoleDefinition struct {
	version      string
	digest       kernel.Digest
	capabilities []string
	permissions  []string
	instructions string
}

// boundRoleGroundingResolver resolves an actor FQN to its configured FQRN,
// then resolves that FQRN to the authenticated role bundle loaded with the
// team manifest. Executions fail closed at either missing link.
type boundRoleGroundingResolver struct {
	roleByActor map[kernel.ActorFQN]kernel.RoleFQRN
	byRole      map[kernel.RoleFQRN]boundRoleDefinition
	handlers    *organization.RoleHandlerResolver
}

func newBoundRoleGroundingResolver(team organization.LoadedTeam) (*boundRoleGroundingResolver, error) {
	if team.Manifest.Validate() != nil || !team.Digest.Valid() || len(team.Roles) != len(team.Manifest.Roles) {
		return nil, application.ErrInvalidConfiguration
	}
	resolver := &boundRoleGroundingResolver{
		roleByActor: make(map[kernel.ActorFQN]kernel.RoleFQRN),
		byRole:      make(map[kernel.RoleFQRN]boundRoleDefinition),
	}
	handlers, err := organization.NewRoleHandlerResolver(team)
	if err != nil {
		return nil, application.ErrInvalidConfiguration
	}
	resolver.handlers = handlers
	for _, loaded := range team.Roles {
		digest, err := loaded.Bundle.ContentDigest()
		if err != nil || loaded.Bundle.Validate() != nil || loaded.Binding.Role != loaded.Bundle.Role || loaded.Binding.BundleDigest != digest {
			return nil, application.ErrInvalidConfiguration
		}
		fqrn, err := kernel.ParseRoleFQRN(loaded.Binding.Role)
		if err != nil {
			return nil, application.ErrInvalidConfiguration
		}
		instructions := loaded.Bundle.Instructions
		if loaded.Package != nil {
			instructions = loaded.Package.Charter
		}
		resolver.byRole[fqrn] = boundRoleDefinition{
			version: loaded.Bundle.Version, digest: loaded.Binding.BundleDigest,
			capabilities: slices.Clone(loaded.Bundle.Capabilities), permissions: slices.Clone(loaded.Bundle.Permissions),
			instructions: instructions,
		}
		for instance := uint32(1); instance <= loaded.Binding.MaximumInstances; instance++ {
			actor, err := kernel.ParseActorFQN(fmt.Sprintf("%s::%s-%d", team.Manifest.Team, loaded.Binding.Role, instance))
			if err != nil {
				return nil, application.ErrInvalidConfiguration
			}
			resolver.roleByActor[actor] = fqrn
		}
	}
	return resolver, nil
}

func (resolver *boundRoleGroundingResolver) ResolveRoleHandlerGrounding(_ context.Context, actor kernel.ActorFQN, binding kernel.HandlerDispatchBinding) (application.RoleExecutionGrounding, application.MessageHandlerGrounding, error) {
	if resolver == nil || resolver.handlers == nil || !binding.Valid() {
		return application.RoleExecutionGrounding{}, application.MessageHandlerGrounding{}, application.ErrInvalidOperationalExecution
	}
	dispatch, err := resolver.handlers.ResolveVersion(actor, binding.RoleBundleDigest, binding.MessageType, binding.SubscriptionPurpose)
	if err != nil || dispatch.Disposition != organization.HandlerModel || string(dispatch.MessagePurpose) != binding.MessagePurpose || dispatch.BundleDigest != binding.RoleBundleDigest || dispatch.CharterDigest != binding.CharterDigest || dispatch.HandlerDigest != binding.HandlerDigest || dispatch.InputSchemaDigest != binding.InputSchemaDigest || dispatch.ResultSchemaDigest != binding.ResultSchemaDigest || !slices.Equal(dispatch.AllowedResults, binding.AllowedResults) || !slices.Equal(dispatch.AllowedMessageProposals, binding.AllowedMessageProposals) {
		return application.RoleExecutionGrounding{}, application.MessageHandlerGrounding{}, application.ErrInvalidOperationalExecution
	}
	grounding := application.RoleExecutionGrounding{
		ActorFQN: actor, RoleFQRN: dispatch.RoleFQRN, BundleVersion: dispatch.BundleVersion, BundleDigest: dispatch.BundleDigest,
		Capabilities: slices.Clone(dispatch.Capabilities), Permissions: slices.Clone(dispatch.Permissions), Instructions: dispatch.Charter,
	}
	if !grounding.Valid(actor) {
		return application.RoleExecutionGrounding{}, application.MessageHandlerGrounding{}, application.ErrInvalidOperationalExecution
	}
	handler := application.MessageHandlerGrounding{
		MessageID: binding.MessageID, MessageType: binding.MessageType, MessagePurpose: binding.MessagePurpose,
		SubscriptionPurpose: binding.SubscriptionPurpose, CharterDigest: dispatch.CharterDigest,
		HandlerDigest: dispatch.HandlerDigest, Instructions: dispatch.HandlerInstructions,
		InputSchemaDigest: dispatch.InputSchemaDigest, InputSchema: append([]byte(nil), dispatch.InputSchema...),
		ResultSchemaDigest: dispatch.ResultSchemaDigest, ResultSchema: append([]byte(nil), dispatch.ResultSchema...),
		AllowedResults: append([]string{}, dispatch.AllowedResults...), AllowedMessageProposals: append([]string{}, dispatch.AllowedMessageProposals...),
	}
	if !handler.Valid(binding, grounding.Instructions) {
		return application.RoleExecutionGrounding{}, application.MessageHandlerGrounding{}, application.ErrInvalidOperationalExecution
	}
	return grounding, handler, nil
}

func (resolver *boundRoleGroundingResolver) requiresMessageHandler(actor kernel.ActorFQN) bool {
	return resolver != nil && resolver.handlers != nil && resolver.handlers.RequiresHandler(actor)
}

func (resolver *boundRoleGroundingResolver) bindMessageHandler(actor kernel.ActorFQN, message organization.OrganizationalMessage) (*kernel.HandlerDispatchBinding, error) {
	if !resolver.requiresMessageHandler(actor) {
		return nil, nil
	}
	dispatch, err := resolver.handlers.ResolveMessage(actor, message)
	if err != nil || dispatch.Disposition != organization.HandlerModel {
		return nil, application.ErrInvalidOperationalExecution
	}
	allowedResults := make([]string, len(dispatch.AllowedResults))
	copy(allowedResults, dispatch.AllowedResults)
	allowedMessageProposals := make([]string, len(dispatch.AllowedMessageProposals))
	copy(allowedMessageProposals, dispatch.AllowedMessageProposals)
	binding := kernel.HandlerDispatchBinding{
		MessageID: message.ID, MessageType: message.Type, MessagePurpose: string(message.Purpose),
		MessageBodyDigest:   digestBytes(message.Body),
		SubscriptionPurpose: dispatch.SubscriptionPurpose, RoleBundleDigest: dispatch.BundleDigest,
		CharterDigest: dispatch.CharterDigest, HandlerDigest: dispatch.HandlerDigest,
		InputSchemaDigest: dispatch.InputSchemaDigest, ResultSchemaDigest: dispatch.ResultSchemaDigest,
		AllowedResults: allowedResults, AllowedMessageProposals: allowedMessageProposals,
	}
	if !binding.Valid() {
		return nil, application.ErrInvalidOperationalExecution
	}
	return &binding, nil
}

func (resolver *boundRoleGroundingResolver) ResolveRoleGrounding(_ context.Context, actor kernel.ActorFQN) (application.RoleExecutionGrounding, error) {
	if resolver == nil || !actor.Valid() {
		return application.RoleExecutionGrounding{}, application.ErrInvalidOperationalExecution
	}
	fqrn, found := resolver.roleByActor[actor]
	if !found {
		return application.RoleExecutionGrounding{}, application.ErrInvalidOperationalExecution
	}
	definition, found := resolver.byRole[fqrn]
	if !found {
		return application.RoleExecutionGrounding{}, application.ErrInvalidOperationalExecution
	}
	grounding := application.RoleExecutionGrounding{
		ActorFQN: actor, RoleFQRN: fqrn,
		BundleVersion: definition.version, BundleDigest: definition.digest,
		Capabilities: slices.Clone(definition.capabilities), Permissions: slices.Clone(definition.permissions),
		Instructions: definition.instructions,
	}
	if !grounding.Valid(actor) {
		return application.RoleExecutionGrounding{}, application.ErrInvalidOperationalExecution
	}
	return grounding, nil
}
