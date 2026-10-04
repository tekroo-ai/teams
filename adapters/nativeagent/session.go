// Package nativeagent binds the Teams-owned turn runner to one admitted
// invocation. The file-backed production backend selects it explicitly.
package nativeagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

var ErrInvalidBinding = errors.New("native agent invocation binding changed or is invalid")
var ErrRequiredTestToolUnavailable = errors.New("native agent task requires go-test but the signed role lacks test.execute permission")

// Profile is the native runner's model configuration. The digest/role fields
// must match the admitted execution brief; no model-supplied identity is used.
type Profile struct {
	RoleFQRN              kernel.RoleFQRN
	RoleBundleDigest      kernel.Digest
	ModelProfileDigest    kernel.Digest
	RuntimeIdentityDigest kernel.Digest
	ToolPolicyDigest      kernel.Digest
	EffectPolicyDigest    kernel.Digest
	BaseURL               string
	Model                 string
	APIKey                string
	MaxOutputTokens       int
	MaxTurns              int
	AllowedReadTools      []string
	AllowedEffectTools    []string
}

type Config struct {
	Bindings        agenttools.BindingSource
	Gateway         agenttools.Gateway
	Journal         agentruntime.Journal
	Effects         agenttools.EffectLedger
	SemanticContext agentruntime.SemanticContextProvider
	EvidenceReader  EvidenceReader
	ResultContract  *ResultContract
	// Candidate is resolved by Teams from the immutable workspace binding, not
	// supplied by the model. It binds a validation result to the exact receipt.
	Candidate *CandidateBinding
	// PlanFinalization carries the accepted architect output so the model only
	// authors dependencies and handoffs, not copies of immutable design fields.
	PlanFinalization *PlanFinalizationBinding
	// ValidateFinalResult applies workflow-level semantics before the native
	// runner commits a final result, while the same model turn can still repair it.
	ValidateFinalResult func(context.Context, []byte) error
	HTTP                *http.Client
	Profile             Profile
}

type CandidateBinding struct {
	ID            kernel.UUIDv7
	ReceiptSHA256 kernel.Digest
}

type PlanFinalizationBinding struct {
	SourceOutput []byte
	SourceDigest kernel.Digest
}

type Session struct {
	Runner        agentruntime.Runner
	InvocationID  string
	RequestDigest string
	Prompt        string
}

func (session Session) Run(ctx context.Context) (string, error) {
	return session.Runner.Run(ctx, session.InvocationID, session.RequestDigest, session.Prompt)
}

// PrepareReadOnly accepts only a brief matching the coordinator's request
// digest, the signed role identity, the configured model profile, and the
// current server-side Teams binding. It never enables mutation tools.
func PrepareReadOnly(ctx context.Context, brief application.ExecutionBrief, requestDigest kernel.Digest, config Config) (Session, error) {
	return prepare(ctx, brief, requestDigest, config, false)
}

// PrepareWithEffects opts this invocation into its explicit durable mutation
// allowlist. It never enables arbitrary commands or unlisted effects.
func PrepareWithEffects(ctx context.Context, brief application.ExecutionBrief, requestDigest kernel.Digest, config Config) (Session, error) {
	return prepare(ctx, brief, requestDigest, config, true)
}

func prepare(ctx context.Context, brief application.ExecutionBrief, requestDigest kernel.Digest, config Config, allowEffects bool) (Session, error) {
	encoded, err := json.Marshal(brief)
	if err != nil {
		return Session{}, err
	}
	digest := sha256.Sum256(encoded)
	profile := config.Profile
	if brief.ContractManifest != kernel.ContractIdentity || !brief.InvocationID.Valid() || !requestDigest.Valid() || requestDigest != kernel.Digest(hex.EncodeToString(digest[:])) ||
		!brief.RoleGrounding.Valid(brief.ActorFQN) || profile.RoleFQRN != brief.RoleGrounding.RoleFQRN ||
		profile.RoleBundleDigest != brief.RoleGrounding.BundleDigest || profile.ModelProfileDigest != brief.ModelProfileDigest ||
		profile.RuntimeIdentityDigest != brief.RuntimeIdentityDigest || profile.ToolPolicyDigest != brief.ToolPolicyDigest ||
		profile.EffectPolicyDigest != brief.EffectPolicyDigest || !profile.ModelProfileDigest.Valid() ||
		!profile.RuntimeIdentityDigest.Valid() || !profile.ToolPolicyDigest.Valid() || !profile.EffectPolicyDigest.Valid() ||
		config.Bindings == nil || config.Gateway.Bindings == nil ||
		config.Journal == nil || config.HTTP == nil || profile.Model == "" || profile.BaseURL == "" ||
		profile.MaxOutputTokens <= 0 || profile.MaxTurns < 0 || !slices.IsSorted(profile.AllowedReadTools) || !slices.IsSorted(profile.AllowedEffectTools) {
		return Session{}, ErrInvalidBinding
	}
	if allowEffects {
		if config.Effects == nil || len(profile.AllowedEffectTools) == 0 {
			return Session{}, ErrInvalidBinding
		}
	} else if len(profile.AllowedEffectTools) != 0 || config.Effects != nil {
		return Session{}, ErrInvalidBinding
	}
	if config.ResultContract != nil && (brief.MessageHandler != nil || brief.ResultProtocol == nil || brief.ResultProtocol.Marker != application.OrganizationalResultMarker) {
		return Session{}, ErrInvalidBinding
	}
	if candidate := config.Candidate; candidate != nil {
		if !candidate.ID.Valid() || !candidate.ReceiptSHA256.Valid() ||
			!strings.HasPrefix(brief.Scope.WorktreeID, "candidate-"+string(candidate.ID)+"-") ||
			!slices.ContainsFunc(brief.Evidence, func(item kernel.EvidenceRef) bool { return item.SHA256 == candidate.ReceiptSHA256 }) ||
			(brief.Purpose != kernel.PurposeValidation && brief.Purpose != kernel.PurposeReview && brief.Purpose != kernel.PurposePromotion) {
			return Session{}, ErrInvalidBinding
		}
	}
	if plan := config.PlanFinalization; plan != nil {
		sourceHash := sha256.Sum256(plan.SourceOutput)
		if brief.Purpose != kernel.PurposeHandoff || brief.MessageHandler == nil || !plan.SourceDigest.Valid() ||
			kernel.Digest(hex.EncodeToString(sourceHash[:])) != plan.SourceDigest {
			return Session{}, ErrInvalidBinding
		}
		if _, _, err := acceptedPlanWorkProduct(plan); err != nil {
			return Session{}, errors.Join(ErrInvalidBinding, err)
		}
	}
	for index, name := range profile.AllowedReadTools {
		if name == "" || index > 0 && name == profile.AllowedReadTools[index-1] {
			return Session{}, ErrInvalidBinding
		}
	}
	for index, name := range profile.AllowedEffectTools {
		if name == "" || index > 0 && name == profile.AllowedEffectTools[index-1] {
			return Session{}, ErrInvalidBinding
		}
	}
	initial, err := config.Bindings.BindToolInvocation(ctx, brief.InvocationID, requestDigest)
	if err != nil || strings.TrimSpace(initial.WorkspaceRoot) == "" {
		return Session{}, errors.Join(ErrInvalidBinding, err)
	}
	if allowEffects && (initial.Purpose != brief.Purpose || initial.EffectPolicyDigest != brief.EffectPolicyDigest) {
		return Session{}, ErrInvalidBinding
	}
	if brief.Purpose == kernel.PurposeImplementation && slices.Contains(brief.WorkProfile.RequiredDeterministicGateIDs, "go-test") && !slices.Contains(initial.Permissions, "test.execute") {
		return Session{}, errors.Join(ErrInvalidBinding, ErrRequiredTestToolUnavailable)
	}
	stable := stableBinding{source: config.Bindings, initial: initial}
	// Both model turns and tool calls pass through the same stability check.
	// The gateway still reconstructs actual authority at the moment of a call.
	config.Gateway.Bindings = stable
	canonical := agenttools.ReadOnlyDefinitions(initial.Permissions)
	allowed := make(map[string]struct{}, len(profile.AllowedReadTools))
	for _, name := range profile.AllowedReadTools {
		allowed[name] = struct{}{}
	}
	definitions := make([]agentruntime.ToolDefinition, 0, len(allowed)+len(profile.AllowedEffectTools)+2)
	for _, definition := range canonical {
		if _, ok := allowed[definition.Name]; ok {
			definitions = append(definitions, definition)
			delete(allowed, definition.Name)
		}
	}
	if len(allowed) != 0 {
		return Session{}, ErrInvalidBinding
	}
	for _, name := range profile.AllowedReadTools {
		allowed[name] = struct{}{}
	}
	var evidenceTool *admittedEvidenceTool
	if len(brief.Evidence) > 0 && config.EvidenceReader != nil {
		evidenceTool, err = newAdmittedEvidenceTool(config.EvidenceReader, brief.Evidence)
		if err != nil {
			return Session{}, err
		}
		definitions = append(definitions, evidenceTool.definition())
	}
	readDefinitionCount := len(definitions)
	var effects agentruntime.Effects
	var effectAuthority *agentruntime.EffectAuthority
	if allowEffects {
		selected := make(map[string]bool, len(profile.AllowedEffectTools))
		for _, name := range profile.AllowedEffectTools {
			selected[name] = true
		}
		for _, definition := range agenttools.MutationDefinitions(initial.Permissions) {
			if selected[definition.Name] {
				if brief.Purpose != kernel.PurposeImplementation && brief.Purpose != kernel.PurposeRepair {
					return Session{}, ErrInvalidBinding
				}
				definitions = append(definitions, definition)
				delete(selected, definition.Name)
			}
		}
		for _, definition := range agenttools.TestDefinitions(initial.Permissions) {
			if selected[definition.Name] {
				if brief.Purpose != kernel.PurposeValidation && brief.Purpose != kernel.PurposeImplementation && brief.Purpose != kernel.PurposeRepair {
					return Session{}, ErrInvalidBinding
				}
				definitions = append(definitions, definition)
				delete(selected, definition.Name)
			}
		}
		if len(selected) != 0 || len(definitions) != readDefinitionCount+len(profile.AllowedEffectTools) {
			return Session{}, ErrInvalidBinding
		}
		mutation := agenttools.MutationTurnAdapter{Gateway: agenttools.MutationGateway{
			Bindings: stable, Host: config.Gateway.Host, Ledger: config.Effects, Policy: brief.EffectPolicyDigest,
		}, InvocationID: brief.InvocationID, RequestDigest: requestDigest}
		tests := agenttools.TestTurnAdapter{Gateway: agenttools.TestGateway{
			Bindings: stable, Host: config.Gateway.Host, Ledger: config.Effects, Policy: brief.EffectPolicyDigest,
		}, InvocationID: brief.InvocationID, RequestDigest: requestDigest}
		effects = allowedEffects{mutation: mutation, tests: tests, allowed: slices.Clone(profile.AllowedEffectTools)}
		effectAuthority = &agentruntime.EffectAuthority{WorkspaceRoot: initial.WorkspaceRoot,
			Permissions: slices.Clone(initial.Permissions), Purpose: string(initial.Purpose), EffectPolicyDigest: string(initial.EffectPolicyDigest)}
	}
	var finalTool string
	var finalize func(json.RawMessage) (string, error)
	prompt := string(encoded)
	if handler := brief.MessageHandler; handler != nil {
		if brief.ResultProtocol == nil || brief.ResultProtocol.Marker != application.OrganizationalResultMarker ||
			!json.Valid(handler.ResultSchema) || len(handler.ResultSchema) == 0 {
			return Session{}, ErrInvalidBinding
		}
		finalTool = "submit_result"
		modelSchema, err := modelFacingHandlerSchema(*handler, brief.Purpose)
		if err != nil {
			return Session{}, ErrInvalidBinding
		}
		if config.PlanFinalization != nil {
			modelSchema, err = modelFacingPlanSchema(modelSchema, config.PlanFinalization)
			if err != nil {
				return Session{}, errors.Join(ErrInvalidBinding, err)
			}
		}
		definitions = append(definitions, agentruntime.ToolDefinition{
			Name: finalTool, Description: "Submit the structured result for this admitted Teams message handler.",
			Parameters: modelSchema,
			Strict:     true,
		})
		finalize = func(arguments json.RawMessage) (string, error) {
			arguments = canonicalizeEmptyProposals(arguments, *handler)
			if config.PlanFinalization != nil {
				var err error
				arguments, err = bindHandlerPlanResult(arguments, config.PlanFinalization)
				if err != nil {
					return "", err
				}
			}
			if validationPurpose(brief.Purpose) {
				var err error
				arguments, err = bindHandlerValidationResult(arguments, config.Candidate)
				if err != nil {
					return "", err
				}
			}
			output := application.OrganizationalResultMarker + "\n" + string(arguments)
			if _, err := application.ValidateRoleHandlerResult(*handler, []byte(output)); err != nil {
				return "", err
			}
			return output, nil
		}
	} else if brief.ResultProtocol != nil {
		definition, validator, ok := nativeValidationResult(brief)
		if !ok && config.ResultContract != nil {
			var resultErr error
			definition, validator, resultErr = nativeOrganizationalResult(brief, *config.ResultContract)
			ok = resultErr == nil
		}
		if !ok {
			// A result without a bound structured schema cannot silently fall
			// back to OpenHands' free-text finish.message channel.
			return Session{}, ErrInvalidBinding
		}
		finalTool, finalize = definition.Name, validator
		definitions = append(definitions, definition)
		// Only the transport instruction changes. Preserve the authenticated
		// source brief digest alongside the exact model-visible adaptation.
		nativeBrief := brief
		protocol := *brief.ResultProtocol
		protocol.Instruction = "Call submit_result once with a JSON object matching its tool schema. Do not JSON-encode the object in a string or call OpenHands finish. Teams validates the structured result before advancing."
		nativeBrief.ResultProtocol = &protocol
		adapted, err := json.Marshal(struct {
			SourceRequestDigest kernel.Digest              `json:"source_request_digest"`
			ExecutionBrief      application.ExecutionBrief `json:"execution_brief"`
			ResultContract      *ResultContract            `json:"result_contract,omitempty"`
		}{SourceRequestDigest: requestDigest, ExecutionBrief: nativeBrief, ResultContract: config.ResultContract})
		if err != nil {
			return Session{}, err
		}
		prompt = string(adapted)
	}
	model := agentruntime.OpenAIModel{
		Client: config.HTTP, BaseURL: profile.BaseURL, Model: profile.Model,
		APIKey: profile.APIKey, MaxTokens: profile.MaxOutputTokens,
		Tools: definitions,
	}
	if finalTool != "" {
		model.FinalTool = finalTool
		model.FinalSchema = append(json.RawMessage(nil), definitions[len(definitions)-1].Parameters...)
	}
	return Session{
		Runner: agentruntime.Runner{
			Journal:         config.Journal,
			Model:           boundModel{bindings: stable, id: brief.InvocationID, digest: requestDigest, model: model},
			Effects:         effects,
			EffectAuthority: effectAuthority,
			ContextProvider: config.SemanticContext,
			WorkspaceRoot:   initial.WorkspaceRoot,
			Tools: allowedReadTools{
				inner:    agenttools.ReadOnlyTurnAdapter{Gateway: config.Gateway, InvocationID: brief.InvocationID, RequestDigest: requestDigest},
				allowed:  allowed,
				evidence: evidenceTool,
			},
			SystemPrompt:        brief.RoleGrounding.Instructions,
			FinalTool:           finalTool,
			Finalize:            finalize,
			ValidateFinalResult: config.ValidateFinalResult,
			MaxFinalSubmissions: 3,
			MaxTurns:            profile.MaxTurns,
		},
		InvocationID: string(brief.InvocationID), RequestDigest: string(requestDigest), Prompt: prompt,
	}, nil
}

type allowedReadTools struct {
	inner    agentruntime.ReadOnlyTools
	allowed  map[string]struct{}
	evidence *admittedEvidenceTool
}

type allowedEffects struct {
	mutation agenttools.MutationTurnAdapter
	tests    agenttools.TestTurnAdapter
	allowed  []string
}

func (effects allowedEffects) Handles(name string) bool {
	return slices.Contains(effects.allowed, name) && (effects.mutation.Handles(name) || effects.tests.Handles(name))
}

func (effects allowedEffects) ExecuteEffect(ctx context.Context, call agentruntime.ToolCall) (json.RawMessage, error) {
	if !effects.Handles(call.Name) {
		return nil, agenttools.ErrForbidden
	}
	if effects.tests.Handles(call.Name) {
		return effects.tests.ExecuteEffect(ctx, call)
	}
	return effects.mutation.ExecuteEffect(ctx, call)
}

func (effects allowedEffects) ReconcileEffect(ctx context.Context, call agentruntime.ToolCall) (json.RawMessage, bool, error) {
	if !effects.Handles(call.Name) {
		return nil, false, agenttools.ErrForbidden
	}
	if effects.tests.Handles(call.Name) {
		return effects.tests.ReconcileEffect(ctx, call)
	}
	return effects.mutation.ReconcileEffect(ctx, call)
}

func (tools allowedReadTools) ExecuteReadOnly(ctx context.Context, call agentruntime.ToolCall) (json.RawMessage, error) {
	if call.Name == "read_evidence" && tools.evidence != nil {
		return tools.evidence.read(ctx, call.Arguments)
	}
	if _, ok := tools.allowed[call.Name]; !ok {
		return nil, agenttools.ErrForbidden
	}
	return tools.inner.ExecuteReadOnly(ctx, call)
}

type stableBinding struct {
	source  agenttools.BindingSource
	initial agenttools.Authority
}

func (binding stableBinding) BindToolInvocation(ctx context.Context, id kernel.UUIDv7, digest kernel.Digest) (agenttools.Authority, error) {
	current, err := binding.source.BindToolInvocation(ctx, id, digest)
	if err != nil {
		return agenttools.Authority{}, err
	}
	if current.WorkspaceRoot != binding.initial.WorkspaceRoot || current.Purpose != binding.initial.Purpose || current.EffectPolicyDigest != binding.initial.EffectPolicyDigest || !reflect.DeepEqual(current.Permissions, binding.initial.Permissions) {
		return agenttools.Authority{}, ErrInvalidBinding
	}
	return current, nil
}

type boundModel struct {
	bindings agenttools.BindingSource
	id       kernel.UUIDv7
	digest   kernel.Digest
	model    agentruntime.Model
}

func (bound boundModel) Complete(ctx context.Context, messages []agentruntime.Message) (agentruntime.Completion, error) {
	if _, err := bound.bindings.BindToolInvocation(ctx, bound.id, bound.digest); err != nil {
		return agentruntime.Completion{}, err
	}
	return bound.model.Complete(ctx, messages)
}
