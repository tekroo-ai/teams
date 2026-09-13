package openhands

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

var (
	ErrInvalidConfiguration          = errors.New("invalid OpenHands execution configuration")
	ErrProtocol                      = errors.New("OpenHands execution protocol mismatch")
	errRecoveryCheckpointUnavailable = errors.New("recovery checkpoint unavailable")
)

const (
	qualifiedCondenserMaximumEvents       = 80
	pauseAfterCondensationTag             = "tekroopauseaftercondensation"
	candidateResultRequirementInstruction = "Return both exact values in the structured validation result. A different or missing value is invalid."
	candidateResultProtocolInstruction    = "The OpenHands finish tool message is the result consumed by Teams. Set finish.message to the marker on its own line followed by exactly one JSON object containing schema_version, outcome, non-empty reasons, candidate_id, and candidate_receipt_sha256. Copy both candidate identity values exactly from candidate_result_requirement. Do not summarize or paraphrase the result in finish.message. Teams rejects missing, malformed, or mismatched results."
	teamsRoleExecutionSystemPrompt        = `You execute one authorized Tekroo Teams work invocation.

The user message is the authoritative JSON execution brief. The role_grounding object identifies the running actor by FQN and the signed role bundle by FQRN. Perform only that role, within its stated instructions, capabilities, permissions, task scope, and acceptance criteria. Use only the tools exposed for this invocation. For repository work, read AGENTS.md and only the source and tests relevant to the assigned result; do not tour the repository or inspect accepted contract packages unless the task explicitly requires contract analysis. Never delegate, contact another agent, invent operational identities, or perform unrequested external, deployment, Git publishing, or lifecycle actions.

Make forward progress. Do not repeat an action unless its inputs or relevant state changed. When the task is complete or blocked by a concrete missing prerequisite, call finish exactly once. The finish message must follow result_protocol exactly; Teams ignores any informal completion claim.`
	// The qualified local model advertises a 128K context window. Keep 32K in
	// reserve for tool results, the next response, and control messages while
	// avoiding a lossy condensation during the required pre-edit inspection.
	qualifiedCondenserMaximumTokens = 96000
)

type WorkspaceBinding struct {
	WorkspaceID      string
	WorktreeID       string
	WorkingDirectory string
	// Candidate is present only for an immutable, read-only validation view.
	// The resolver verifies these identities before every OpenHands boundary
	// call so a missing, dirty, or drifted candidate never reaches the model.
	Candidate *CandidateWorkspaceBinding
}

type CandidateWorkspaceBinding struct {
	CandidateID        string        `json:"candidate_id"`
	ReceiptSHA256      kernel.Digest `json:"receipt_sha256"`
	RepositoryDigest   kernel.Digest `json:"repository_digest"`
	BaselineCommit     string        `json:"baseline_commit"`
	CandidateCommit    string        `json:"candidate_commit"`
	CandidateTree      string        `json:"candidate_tree"`
	DiffSHA256         kernel.Digest `json:"diff_sha256"`
	ChangedFilesSHA256 kernel.Digest `json:"changed_files_sha256"`
	RuntimeHookSHA256  kernel.Digest `json:"runtime_hook_sha256,omitempty"`
	AllowedReference   string        `json:"allowed_reference"`
	ReadOnly           bool          `json:"read_only"`
}

type candidateResultRequirement struct {
	CandidateID            string        `json:"candidate_id"`
	CandidateReceiptSHA256 kernel.Digest `json:"candidate_receipt_sha256"`
	Instruction            string        `json:"instruction"`
}

type WorkspaceResolver interface {
	ResolveWorkspace(context.Context, kernel.TaskOperationalScope) (WorkspaceBinding, error)
}

type ExecutionProfile struct {
	ModelProfileDigest    kernel.Digest
	RoleFQRN              kernel.RoleFQRN
	RoleBundleDigest      kernel.Digest
	RuntimeIdentityDigest kernel.Digest
	ToolPolicyDigest      kernel.Digest
	EffectPolicyDigest    kernel.Digest
	AgentSettings         json.RawMessage
	HookConfig            json.RawMessage
	// MaxIterations is a transport compatibility setting. Zero means that
	// Teams does not impose an iteration limit; the OpenHands 1.40.1 API
	// requires a positive integer, so the adapter encodes zero using the
	// largest safe signed value accepted by that API.
	MaxIterations           uint32
	AgentDelegationDisabled bool
	SemanticMemory          SemanticMemoryBinding
}

func (profile ExecutionProfile) valid() bool {
	if !profile.ModelProfileDigest.Valid() || !profile.RuntimeIdentityDigest.Valid() || !profile.ToolPolicyDigest.Valid() || !profile.EffectPolicyDigest.Valid() || !profile.AgentDelegationDisabled || !jsonObject(profile.AgentSettings) || !jsonObject(profile.HookConfig) || containsDelegationTool(profile.AgentSettings) || !profile.SemanticMemory.valid(profile.HookConfig) {
		return false
	}
	if profile.RoleFQRN == "" && profile.RoleBundleDigest == "" {
		return qualifiedAgentSettings(profile.AgentSettings)
	}
	digest, err := ModelProfileDigest(profile.RoleFQRN, profile.RoleBundleDigest, profile.AgentSettings)
	return err == nil && digest == profile.ModelProfileDigest && configurableAgentSettings(profile.AgentSettings)
}

func (profile ExecutionProfile) validFor(brief application.ExecutionBrief) bool {
	if !profile.valid() {
		return false
	}
	if profile.RoleFQRN == "" && profile.RoleBundleDigest == "" {
		return true
	}
	return profile.RoleFQRN == brief.RoleGrounding.RoleFQRN && profile.RoleBundleDigest == brief.RoleGrounding.BundleDigest
}

type AgentSettingsConfig struct {
	Model                   string
	ModelCanonicalName      string
	BaseURL                 string
	APIKey                  string
	Tools                   []string
	EnableThinking          bool
	CondenserEnableThinking bool
	ReasoningEffort         string
	MaximumOutputTokens     uint32
	CondenserOutputTokens   uint32
	TimeoutSeconds          uint32
	CondenserMaximumEvents  uint32
	CondenserMaximumTokens  uint32
}

func validReasoningEffort(effort string) bool {
	return effort == "low" || effort == "medium"
}

func NewOpenAICompatibleAgentSettings(config AgentSettingsConfig) (json.RawMessage, error) {
	if config.Model == "" || config.ModelCanonicalName == "" || config.APIKey == "" || config.Tools == nil || !validExplicitAgentTools(config.Tools) || config.MaximumOutputTokens == 0 || config.CondenserOutputTokens == 0 || config.TimeoutSeconds == 0 || config.CondenserMaximumEvents == 0 || config.CondenserMaximumTokens == 0 || config.CondenserEnableThinking || config.EnableThinking != validReasoningEffort(config.ReasoningEffort) {
		return nil, ErrInvalidConfiguration
	}
	endpoint, err := url.Parse(config.BaseURL)
	if err != nil || endpoint.Scheme != "http" && endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil {
		return nil, ErrInvalidConfiguration
	}
	llm := func(thinking bool, usageID string, maximumOutputTokens uint32, reasoningEffort string) map[string]any {
		extraBody := map[string]any{
			"chat_template_kwargs": map[string]any{
				"enable_thinking":   thinking,
				"preserve_thinking": thinking,
			},
		}
		// mlx-serve treats reasoning_effort as a behavioral control for Qwen 3.8.
		// Its reasoning_budget_tokens field only truncates reasoning after a
		// non-streaming generation, so it cannot bound the model's work. Use the
		// effort control and the real max_output_tokens generation limit instead.
		if thinking {
			extraBody["reasoning_effort"] = reasoningEffort
		}
		settings := map[string]any{
			"model": config.Model, "model_canonical_name": config.ModelCanonicalName,
			"base_url": config.BaseURL, "api_mode": "chat", "api_key": config.APIKey,
			"native_tool_calling": true, "force_string_serializer": false,
			"stream": false, "temperature": 0, "max_output_tokens": maximumOutputTokens,
			"num_retries": 0, "retry_multiplier": 0, "retry_min_wait": 0, "retry_max_wait": 0,
			"timeout": config.TimeoutSeconds, "log_completions": false,
			"litellm_extra_body": extraBody,
		}
		if usageID != "" {
			settings["usage_id"] = usageID
		}
		return settings
	}
	tools := make([]map[string]any, len(config.Tools))
	for index, name := range config.Tools {
		tools[index] = map[string]any{"name": name, "params": map[string]any{}}
	}
	settings := map[string]any{
		"kind": "Agent", "include_default_tools": []string{"FinishTool"},
		"tools": tools, "system_prompt": teamsRoleExecutionSystemPrompt,
		"agent_context": map[string]any{"system_message_suffix": qualifiedShellDisciplineSystemSuffix},
		"llm":           llm(config.EnableThinking, "", config.MaximumOutputTokens, config.ReasoningEffort),
		"condenser": map[string]any{
			"kind": "LLMSummarizingCondenser", "llm": llm(config.CondenserEnableThinking, "condenser", config.CondenserOutputTokens, ""),
			"max_size": config.CondenserMaximumEvents, "max_tokens": config.CondenserMaximumTokens, "keep_first": 2,
		},
	}
	encoded, err := json.Marshal(settings)
	if err != nil || !configurableAgentSettings(encoded) {
		return nil, ErrInvalidConfiguration
	}
	return encoded, nil
}

// ExecutionToolsForPermissions derives the exact OpenHands tool surface from
// a verified role bundle. A role without repository authority gets no
// workspace tools. Repository readers receive dedicated glob, grep, and view
// tools so they can discover code without arbitrary command execution. Explicit
// test/security execution authority adds the terminal, and only edit-authorized
// roles receive the task tracker used during implementation.
func ExecutionToolsForPermissions(permissions []string) []string {
	canRead := slices.Contains(permissions, "repository.read") || slices.Contains(permissions, "repository.edit")
	if !canRead {
		return []string{}
	}
	if slices.Contains(permissions, "repository.edit") {
		return []string{"terminal", "glob", "repository_search", "file_editor", "task_tracker"}
	}
	if slices.Contains(permissions, "test.execute") || slices.Contains(permissions, "security-check.execute") {
		return []string{"terminal", "glob", "repository_search", "repository_view"}
	}
	return []string{"glob", "repository_search", "repository_view"}
}

func validExplicitAgentTools(tools []string) bool {
	canonical := []string{"terminal", "glob", "repository_search", "repository_view", "file_editor", "task_tracker"}
	position := -1
	for _, tool := range tools {
		next := slices.Index(canonical, tool)
		if next < 0 || next <= position {
			return false
		}
		position = next
	}
	return true
}

func ModelProfileDigest(role kernel.RoleFQRN, bundleDigest kernel.Digest, agentSettings json.RawMessage) (kernel.Digest, error) {
	if !role.Valid() || !bundleDigest.Valid() || !configurableAgentSettings(agentSettings) {
		return "", ErrInvalidConfiguration
	}
	document := struct {
		SchemaVersion    string          `json:"schema_version"`
		RoleFQRN         kernel.RoleFQRN `json:"role_fqrn"`
		RoleBundleDigest kernel.Digest   `json:"role_bundle_digest"`
		AgentSettings    json.RawMessage `json:"agent_settings"`
	}{"tekroo.teams.model-profile/1.0.0", role, bundleDigest, agentSettings}
	encoded, err := json.Marshal(document)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return kernel.Digest(hex.EncodeToString(digest[:])), nil
}

const openHandsOperationallyUnboundedIterations = uint32(1<<31 - 1)

func openHandsIterationLimit(configured uint32) uint32 {
	if configured == 0 {
		return openHandsOperationallyUnboundedIterations
	}
	return configured
}

const (
	AcceptedSMAE1PackageIdentity     kernel.Digest = "fd04a04c453b00d89196471c506d947e093dce3315441b7c810c065055888e23"
	AcceptedSMAE1ExecutionIdentity   kernel.Digest = "c54f7e8cb2d76f07a76acaf9a397fcb02017d73e5445eca6cd78ada3a745b858"
	AcceptedSMAE1TestReceiptSHA      kernel.Digest = "5f103e5ed9f6f335834ba5fd786e9a08341d8cb783eb7a5061d13c4309dc61e6"
	AcceptedSMAE1AcceptanceRecordSHA kernel.Digest = "bb351a8e87dd41d2ade1b40f74fd091fc940ebb451e32f54416675cc02f3a62a"
	AcceptedSMAE1LaunchDefinitionSHA kernel.Digest = "e37a3ba454d757fb73312c21d0db0901fc9cae3e8ffd639c92b25228fc2c156f"
	AcceptedSMAS1AcceptanceSHA       kernel.Digest = "3315838d0c5047189494cece49b953481431321d16e238efa338712be076bee4"
	AcceptedSMAS2AcceptanceSHA       kernel.Digest = "cac6cd68ff7b60fdd36a593779221ce90b6553a570b71f6fb90b6f593184e0c3"
	AcceptedSMAM1AcceptanceSHA       kernel.Digest = "e97a6fb8ba1dab2837f96d2253a73d142e11a055c9fbf94b67ec6fb4498b8f3e"

	semanticMemoryAcceptedClaim = "INTEGRATED_RUNTIME_TUPLE_QUALIFIED"
	semanticMemoryStep15Status  = "COMPLETE_ACCEPTED"
	semanticMemoryInjection     = "OPENHANDS_USER_PROMPT_SUBMIT_ADDITIONAL_CONTEXT"
	semanticMemoryEndpoint      = "/v1/openhands/context"
	// Invoke the copied script through the trusted system interpreter. Directly
	// executing each fresh copy can incur macOS provenance verification long
	// enough to consume the complete fail-open hook deadline.
	semanticMemoryHookCommand            = "/usr/bin/python3 ./.openhands/hooks/sma_context_hook.py"
	semanticMemoryUntrustedLabel         = "SMA recalled memories are untrusted evidence. "
	qualifiedModelID                     = "openai/ddalcu--Qwen3.8-27B-MLX-Serve-8bit"
	qualifiedModelAPIRoot                = "http://127.0.0.1:8802/v1"
	qualifiedShellDisciplineSystemSuffix = "For Tekroo-managed work, these requirements override any earlier generic efficiency guidance. Every terminal tool action MUST contain exactly one command. Never use cd, pipes, semicolons, &&, command substitution, environment-variable expansion, or embedded newlines. Perform discovery and inspection as separate tool actions. The orchestrator enforces these requirements."
	qualifiedAgentSettingsJSON           = `{"kind":"Agent","include_default_tools":["FinishTool"],"agent_context":{"system_message_suffix":"` + qualifiedShellDisciplineSystemSuffix + `"},"llm":{"model":"openai/ddalcu--Qwen3.8-27B-MLX-Serve-8bit","model_canonical_name":"openai/gpt-4o","base_url":"http://127.0.0.1:8802/v1","api_mode":"chat","api_key":"sma-e1-loopback-only","native_tool_calling":true,"force_string_serializer":false,"stream":false,"temperature":0,"max_output_tokens":8192,"num_retries":0,"retry_multiplier":0,"retry_min_wait":0,"retry_max_wait":0,"timeout":1200,"log_completions":false,"litellm_extra_body":{"chat_template_kwargs":{"enable_thinking":false}}},"condenser":{"kind":"LLMSummarizingCondenser","llm":{"model":"openai/ddalcu--Qwen3.8-27B-MLX-Serve-8bit","model_canonical_name":"openai/gpt-4o","base_url":"http://127.0.0.1:8802/v1","api_mode":"chat","api_key":"sma-e1-loopback-only","native_tool_calling":true,"force_string_serializer":false,"stream":false,"temperature":0,"max_output_tokens":8192,"num_retries":0,"retry_multiplier":0,"retry_min_wait":0,"retry_max_wait":0,"timeout":1200,"log_completions":false,"usage_id":"condenser","litellm_extra_body":{"chat_template_kwargs":{"enable_thinking":false}}},"max_size":80,"max_tokens":96000,"keep_first":2}}`
	qualifiedSMAHookConfigJSON           = `{"hooks":{"UserPromptSubmit":[{"matcher":"*","hooks":[{"type":"command","command":"/usr/bin/python3 ./.openhands/hooks/sma_context_hook.py","timeout":1}]}]}}`
)

// NewAcceptedExecutionProfile returns the exact OpenHands/SMA/model profile
// accepted by Phase 3 Step 15. Callers bind only the four Teams policy digests,
// iteration policy, and the physically separate Teams/SMA database identities.
// A maximumIterations value of zero means unbounded.
func NewAcceptedExecutionProfile(modelProfile, runtimeIdentity, toolPolicy, effectPolicy kernel.Digest, maximumIterations uint32, teamsAuthorityDatabaseIdentity, smaMemoryDatabaseIdentity string) (ExecutionProfile, error) {
	agentSettings := json.RawMessage(qualifiedAgentSettingsJSON)
	hookConfig := json.RawMessage(qualifiedSMAHookConfigJSON)
	semanticMemory, err := NewAcceptedSemanticMemoryBinding(hookConfig, teamsAuthorityDatabaseIdentity, smaMemoryDatabaseIdentity)
	if err != nil {
		return ExecutionProfile{}, err
	}
	profile := ExecutionProfile{
		ModelProfileDigest: modelProfile, RuntimeIdentityDigest: runtimeIdentity,
		ToolPolicyDigest: toolPolicy, EffectPolicyDigest: effectPolicy,
		AgentSettings: append(json.RawMessage(nil), agentSettings...),
		HookConfig:    append(json.RawMessage(nil), hookConfig...),
		MaxIterations: maximumIterations, AgentDelegationDisabled: true,
		SemanticMemory: semanticMemory,
	}
	if !profile.valid() {
		return ExecutionProfile{}, ErrInvalidConfiguration
	}
	return profile, nil
}

// NewBoundExecutionProfile constructs a production Teams cognition profile.
// The SMA binding remains the accepted memory hook, while the model settings
// and signed role bundle are independently content-addressed by modelProfile.
func NewBoundExecutionProfile(modelProfile kernel.Digest, role kernel.RoleFQRN, roleBundle, runtimeIdentity, toolPolicy, effectPolicy kernel.Digest, agentSettings json.RawMessage, maximumIterations uint32, teamsAuthorityDatabaseIdentity, smaMemoryDatabaseIdentity string) (ExecutionProfile, error) {
	hookConfig := json.RawMessage(qualifiedSMAHookConfigJSON)
	semanticMemory, err := NewAcceptedSemanticMemoryBinding(hookConfig, teamsAuthorityDatabaseIdentity, smaMemoryDatabaseIdentity)
	if err != nil {
		return ExecutionProfile{}, err
	}
	profile := ExecutionProfile{
		ModelProfileDigest: modelProfile, RoleFQRN: role, RoleBundleDigest: roleBundle,
		RuntimeIdentityDigest: runtimeIdentity, ToolPolicyDigest: toolPolicy, EffectPolicyDigest: effectPolicy,
		AgentSettings: append(json.RawMessage(nil), agentSettings...), HookConfig: append(json.RawMessage(nil), hookConfig...),
		MaxIterations: maximumIterations, AgentDelegationDisabled: true, SemanticMemory: semanticMemory,
	}
	if !profile.valid() {
		return ExecutionProfile{}, ErrInvalidConfiguration
	}
	return profile, nil
}

// SemanticMemoryBinding freezes the already qualified SMA/OpenHands/model
// integration and its authority separation. Teams supplies prompt metadata;
// OpenHands invokes the hook; neither side receives the other's database.
type SemanticMemoryBinding struct {
	PackageIdentity                  kernel.Digest
	ExecutionIdentity                kernel.Digest
	Step15TestReceiptSHA256          kernel.Digest
	Step15AcceptanceRecordSHA256     kernel.Digest
	LaunchDefinitionSHA256           kernel.Digest
	S1AcceptanceSHA256               kernel.Digest
	S2AcceptanceSHA256               kernel.Digest
	M1AcceptanceSHA256               kernel.Digest
	AcceptedClaim                    string
	Step15Status                     string
	InjectionMechanism               string
	ContextEndpointPath              string
	HookCommand                      string
	HookTimeoutSeconds               uint32
	HookConfigurationDigest          kernel.Digest
	MaximumResults                   uint32
	MaximumContextCharacters         uint32
	UntrustedContextPrefix           string
	FailOpen                         bool
	NonAuthoritative                 bool
	TeamsAuthorityDatabaseIdentity   string
	SMAMemoryDatabaseIdentity        string
	TeamsDirectMemoryDatabaseAccess  bool
	SMADirectAuthorityDatabaseAccess bool
}

func NewAcceptedSemanticMemoryBinding(hookConfig json.RawMessage, teamsAuthorityDatabaseIdentity, smaMemoryDatabaseIdentity string) (SemanticMemoryBinding, error) {
	digest := sha256.Sum256(hookConfig)
	binding := SemanticMemoryBinding{
		PackageIdentity:                AcceptedSMAE1PackageIdentity,
		ExecutionIdentity:              AcceptedSMAE1ExecutionIdentity,
		Step15TestReceiptSHA256:        AcceptedSMAE1TestReceiptSHA,
		Step15AcceptanceRecordSHA256:   AcceptedSMAE1AcceptanceRecordSHA,
		LaunchDefinitionSHA256:         AcceptedSMAE1LaunchDefinitionSHA,
		S1AcceptanceSHA256:             AcceptedSMAS1AcceptanceSHA,
		S2AcceptanceSHA256:             AcceptedSMAS2AcceptanceSHA,
		M1AcceptanceSHA256:             AcceptedSMAM1AcceptanceSHA,
		AcceptedClaim:                  semanticMemoryAcceptedClaim,
		Step15Status:                   semanticMemoryStep15Status,
		InjectionMechanism:             semanticMemoryInjection,
		ContextEndpointPath:            semanticMemoryEndpoint,
		HookCommand:                    semanticMemoryHookCommand,
		HookTimeoutSeconds:             1,
		HookConfigurationDigest:        kernel.Digest(hex.EncodeToString(digest[:])),
		MaximumResults:                 3,
		MaximumContextCharacters:       4096,
		UntrustedContextPrefix:         semanticMemoryUntrustedLabel,
		FailOpen:                       true,
		NonAuthoritative:               true,
		TeamsAuthorityDatabaseIdentity: teamsAuthorityDatabaseIdentity,
		SMAMemoryDatabaseIdentity:      smaMemoryDatabaseIdentity,
	}
	if !binding.valid(hookConfig) {
		return SemanticMemoryBinding{}, ErrInvalidConfiguration
	}
	return binding, nil
}

func (binding SemanticMemoryBinding) valid(hookConfig json.RawMessage) bool {
	if binding.PackageIdentity != AcceptedSMAE1PackageIdentity || binding.ExecutionIdentity != AcceptedSMAE1ExecutionIdentity || binding.Step15TestReceiptSHA256 != AcceptedSMAE1TestReceiptSHA || binding.Step15AcceptanceRecordSHA256 != AcceptedSMAE1AcceptanceRecordSHA || binding.LaunchDefinitionSHA256 != AcceptedSMAE1LaunchDefinitionSHA || binding.S1AcceptanceSHA256 != AcceptedSMAS1AcceptanceSHA || binding.S2AcceptanceSHA256 != AcceptedSMAS2AcceptanceSHA || binding.M1AcceptanceSHA256 != AcceptedSMAM1AcceptanceSHA || binding.AcceptedClaim != semanticMemoryAcceptedClaim || binding.Step15Status != semanticMemoryStep15Status || binding.InjectionMechanism != semanticMemoryInjection || binding.ContextEndpointPath != semanticMemoryEndpoint || binding.HookCommand != semanticMemoryHookCommand || binding.HookTimeoutSeconds != 1 || binding.MaximumResults != 3 || binding.MaximumContextCharacters != 4096 || binding.UntrustedContextPrefix != semanticMemoryUntrustedLabel || !binding.FailOpen || !binding.NonAuthoritative || binding.TeamsAuthorityDatabaseIdentity == "" || binding.SMAMemoryDatabaseIdentity == "" || binding.TeamsAuthorityDatabaseIdentity == binding.SMAMemoryDatabaseIdentity || binding.TeamsDirectMemoryDatabaseAccess || binding.SMADirectAuthorityDatabaseAccess {
		return false
	}
	digest := sha256.Sum256(hookConfig)
	if kernel.Digest(hex.EncodeToString(digest[:])) != binding.HookConfigurationDigest {
		return false
	}
	return containsQualifiedSemanticMemoryHook(hookConfig, binding.HookCommand, binding.HookTimeoutSeconds)
}

type qualifiedLLMSettings struct {
	Model             string  `json:"model"`
	ModelCanonical    string  `json:"model_canonical_name"`
	BaseURL           string  `json:"base_url"`
	APIMode           string  `json:"api_mode"`
	APIKey            string  `json:"api_key"`
	NativeToolCalling bool    `json:"native_tool_calling"`
	Stream            bool    `json:"stream"`
	Temperature       float64 `json:"temperature"`
	MaximumOutput     uint32  `json:"max_output_tokens"`
	Retries           uint32  `json:"num_retries"`
	Timeout           uint32  `json:"timeout"`
	ExtraBody         struct {
		ReasoningEffort       string `json:"reasoning_effort"`
		ReasoningBudgetTokens uint32 `json:"reasoning_budget_tokens"`
		ChatTemplateArguments struct {
			EnableThinking   *bool `json:"enable_thinking"`
			PreserveThinking *bool `json:"preserve_thinking"`
		} `json:"chat_template_kwargs"`
	} `json:"litellm_extra_body"`
}

type qualifiedAgentTool struct {
	Name   string         `json:"name"`
	Params map[string]any `json:"params"`
}

func validTeamsSystemPrompt(value string) bool {
	// Empty preserves the accepted Step-15 profile. Every profile constructed
	// by Teams after role grounding became executable carries the exact prompt.
	return value == "" || value == teamsRoleExecutionSystemPrompt
}

func validQualifiedAgentTools(tools []qualifiedAgentTool) bool {
	if tools == nil {
		// The immutable Step-15 profile predates explicit role tool binding.
		return true
	}
	names := make([]string, len(tools))
	for index, tool := range tools {
		if tool.Params == nil || len(tool.Params) != 0 {
			return false
		}
		names[index] = tool.Name
	}
	return validExplicitAgentTools(names)
}

func qualifiedAgentSettings(raw json.RawMessage) bool {
	var settings struct {
		IncludeDefaultTools []string             `json:"include_default_tools"`
		Tools               []qualifiedAgentTool `json:"tools"`
		SystemPrompt        string               `json:"system_prompt"`
		AgentContext        struct {
			SystemMessageSuffix string `json:"system_message_suffix"`
		} `json:"agent_context"`
		LLM       qualifiedLLMSettings `json:"llm"`
		Condenser struct {
			Kind          string               `json:"kind"`
			LLM           qualifiedLLMSettings `json:"llm"`
			MaximumEvents uint32               `json:"max_size"`
			MaximumTokens uint32               `json:"max_tokens"`
			KeepFirst     uint32               `json:"keep_first"`
		} `json:"condenser"`
	}
	if json.Unmarshal(raw, &settings) != nil || !qualifiedLLM(settings.LLM) || !qualifiedLLM(settings.Condenser.LLM) {
		return false
	}
	return slices.Equal(settings.IncludeDefaultTools, []string{"FinishTool"}) && validQualifiedAgentTools(settings.Tools) && validTeamsSystemPrompt(settings.SystemPrompt) && settings.AgentContext.SystemMessageSuffix == qualifiedShellDisciplineSystemSuffix && settings.Condenser.Kind == "LLMSummarizingCondenser" && settings.Condenser.MaximumEvents == qualifiedCondenserMaximumEvents && settings.Condenser.MaximumTokens == qualifiedCondenserMaximumTokens && settings.Condenser.KeepFirst == 2
}

func configurableAgentSettings(raw json.RawMessage) bool {
	var settings struct {
		Kind                string               `json:"kind"`
		IncludeDefaultTools []string             `json:"include_default_tools"`
		Tools               []qualifiedAgentTool `json:"tools"`
		SystemPrompt        string               `json:"system_prompt"`
		AgentContext        struct {
			SystemMessageSuffix string `json:"system_message_suffix"`
		} `json:"agent_context"`
		LLM       qualifiedLLMSettings `json:"llm"`
		Condenser struct {
			Kind          string               `json:"kind"`
			LLM           qualifiedLLMSettings `json:"llm"`
			MaximumEvents uint32               `json:"max_size"`
			MaximumTokens uint32               `json:"max_tokens"`
			KeepFirst     uint32               `json:"keep_first"`
		} `json:"condenser"`
	}
	if json.Unmarshal(raw, &settings) != nil || settings.Kind != "Agent" || !configurableLLM(settings.LLM) || !configurableLLM(settings.Condenser.LLM) {
		return false
	}
	return slices.Equal(settings.IncludeDefaultTools, []string{"FinishTool"}) && validQualifiedAgentTools(settings.Tools) && validTeamsSystemPrompt(settings.SystemPrompt) && settings.AgentContext.SystemMessageSuffix == qualifiedShellDisciplineSystemSuffix && settings.Condenser.Kind == "LLMSummarizingCondenser" && settings.Condenser.MaximumEvents > 0 && settings.Condenser.MaximumEvents <= 1000 && settings.Condenser.MaximumTokens > 0 && settings.Condenser.MaximumTokens <= 262144 && settings.Condenser.KeepFirst == 2
}

func configurableLLM(settings qualifiedLLMSettings) bool {
	endpoint, err := url.Parse(settings.BaseURL)
	if err != nil || endpoint.Scheme != "http" && endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil {
		return false
	}
	if settings.Model == "" || len(settings.Model) > 1024 || settings.ModelCanonical == "" || len(settings.ModelCanonical) > 1024 || settings.APIMode != "chat" || settings.APIKey == "" || !settings.NativeToolCalling || settings.Temperature != 0 || settings.MaximumOutput == 0 || settings.MaximumOutput > 262144 || settings.Retries != 0 || settings.Timeout == 0 || settings.Timeout > 86400 || settings.ExtraBody.ChatTemplateArguments.EnableThinking == nil || settings.ExtraBody.ChatTemplateArguments.PreserveThinking == nil {
		return false
	}
	if *settings.ExtraBody.ChatTemplateArguments.EnableThinking {
		return *settings.ExtraBody.ChatTemplateArguments.PreserveThinking && validReasoningEffort(settings.ExtraBody.ReasoningEffort) && settings.ExtraBody.ReasoningBudgetTokens == 0
	}
	return !*settings.ExtraBody.ChatTemplateArguments.PreserveThinking && settings.ExtraBody.ReasoningEffort == "" && settings.ExtraBody.ReasoningBudgetTokens == 0
}

func qualifiedLLM(settings qualifiedLLMSettings) bool {
	if settings.Model != qualifiedModelID || settings.ModelCanonical != "openai/gpt-4o" || settings.BaseURL != qualifiedModelAPIRoot || settings.APIMode != "chat" || settings.APIKey != "sma-e1-loopback-only" || !settings.NativeToolCalling || settings.Stream || settings.Temperature != 0 || settings.MaximumOutput != 8192 || settings.Retries != 0 || settings.Timeout != 1200 || settings.ExtraBody.ChatTemplateArguments.EnableThinking == nil {
		return false
	}
	return !*settings.ExtraBody.ChatTemplateArguments.EnableThinking
}

func containsQualifiedSemanticMemoryHook(raw json.RawMessage, command string, timeout uint32) bool {
	var config struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout uint32 `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || decoder.Decode(&struct{}{}) != io.EOF || len(config.Hooks) != 1 {
		return false
	}
	groups := config.Hooks["UserPromptSubmit"]
	if len(groups) != 1 || groups[0].Matcher != "*" || len(groups[0].Hooks) != 1 {
		return false
	}
	hook := groups[0].Hooks[0]
	return hook.Type == "command" && hook.Command == command && hook.Timeout == timeout
}

type ExecutionProfileResolver interface {
	ResolveExecutionProfile(context.Context, kernel.Digest, kernel.Digest, kernel.Digest, kernel.Digest) (ExecutionProfile, error)
}

type Config struct {
	BaseURL              string
	SessionAPIKey        string
	HTTPClient           *http.Client
	Workspaces           WorkspaceResolver
	Profiles             ExecutionProfileResolver
	PollInterval         time.Duration
	MaximumPages         uint32
	MaximumEvidenceBytes int
}

type Client struct {
	baseURL              *url.URL
	sessionAPIKey        string
	http                 *http.Client
	workspaces           WorkspaceResolver
	profiles             ExecutionProfileResolver
	pollInterval         time.Duration
	maximumPages         uint32
	maximumEvidenceBytes int
}

func NewClient(config Config) (*Client, error) {
	base, err := url.Parse(config.BaseURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.RawQuery != "" || base.Fragment != "" || strings.TrimRight(base.Path, "/") != "" || config.SessionAPIKey == "" || config.HTTPClient == nil || config.Workspaces == nil || config.Profiles == nil || config.PollInterval <= 0 || config.MaximumPages == 0 || config.MaximumPages > 1000 || config.MaximumEvidenceBytes <= 0 || config.MaximumEvidenceBytes > 16<<20 {
		return nil, ErrInvalidConfiguration
	}
	base.Path = ""
	return &Client{baseURL: base, sessionAPIKey: config.SessionAPIKey, http: config.HTTPClient, workspaces: config.Workspaces, profiles: config.Profiles, pollInterval: config.PollInterval, maximumPages: config.MaximumPages, maximumEvidenceBytes: config.MaximumEvidenceBytes}, nil
}

func (client *Client) Start(ctx context.Context, brief application.ExecutionBrief, requestDigest kernel.Digest) (application.ExternalExecutionObservation, error) {
	prepared, err := client.prepare(ctx, brief, requestDigest)
	if err != nil {
		return application.ExternalExecutionObservation{}, err
	}
	conversationID := string(brief.InvocationID)
	info, status, err := client.getConversation(ctx, conversationID)
	if err != nil {
		return application.ExternalExecutionObservation{}, err
	}
	if status == http.StatusNotFound {
		var forked bool
		status, _, forked, err = client.createOrForkConversation(ctx, brief, prepared)
		if err != nil {
			return application.ExternalExecutionObservation{}, err
		}
		if status != http.StatusOK && status != http.StatusCreated && status != http.StatusConflict {
			return client.reconcileRejectedStart(ctx, brief, requestDigest, status)
		}
		info, status, err = client.getConversation(ctx, conversationID)
		if err != nil || status != http.StatusOK {
			return application.ExternalExecutionObservation{}, ErrProtocol
		}
		if forked && (brief.RetryOfInvocationID == nil || info.ForkedFromConversationID != string(*brief.RetryOfInvocationID)) {
			return application.ExternalExecutionObservation{}, ErrProtocol
		}
	}
	if status != http.StatusOK || !conversationMatches(info, conversationID, prepared.workspace.WorkingDirectory, requestDigest) || !conversationAgentMatches(info, prepared.profile.AgentSettings) {
		return application.ExternalExecutionObservation{}, ErrProtocol
	}
	events, err := client.events(ctx, conversationID)
	if err != nil {
		return application.ExternalExecutionObservation{}, err
	}
	if executionPromptIndex(events, prepared, brief, requestDigest) >= 0 {
		return client.observation(ctx, brief, requestDigest, info, events, false)
	}
	status, response, err := client.request(ctx, http.MethodPost, "/api/conversations/"+url.PathEscape(conversationID)+"/events", map[string]any{
		"role": "user", "run": true,
		"content": []map[string]any{{"type": "text", "text": prepared.prompt}},
	})
	if err != nil {
		reconciled, reconcileErr := client.ReconcileStart(ctx, brief, requestDigest)
		if reconcileErr == nil && reconciled.State != application.ExternalAbsent {
			return reconciled, nil
		}
		return application.ExternalExecutionObservation{}, err
	}
	if status != http.StatusOK {
		events, eventsErr := client.events(ctx, conversationID)
		if eventsErr == nil && executionPromptIndex(events, prepared, brief, requestDigest) >= 0 {
			info, _, _ = client.getConversation(ctx, conversationID)
			return client.observation(ctx, brief, requestDigest, info, events, false)
		}
		return rejectedObservation(brief, requestDigest, response), nil
	}
	for {
		events, err = client.events(ctx, conversationID)
		if err == nil && executionPromptIndex(events, prepared, brief, requestDigest) >= 0 {
			info, status, err = client.getConversation(ctx, conversationID)
			if err == nil && status == http.StatusOK {
				return client.observation(ctx, brief, requestDigest, info, events, false)
			}
		}
		if err := wait(ctx, client.pollInterval); err != nil {
			return application.ExternalExecutionObservation{}, err
		}
	}
}

func (client *Client) createOrForkConversation(ctx context.Context, brief application.ExecutionBrief, prepared preparedExecution) (int, []byte, bool, error) {
	// An evidence-bound operator recovery already carries its predecessor and
	// checkpoint lineage in the immutable execution brief. Start it cleanly so
	// a failed agent loop is not copied into the successor model context.
	// Automatic retries still fork for ordinary conversational continuity.
	if explicitRecoveryProfile(brief) && brief.RetryOfConversationID != nil && !prepared.recoveryCheckpointPresent {
		return 0, nil, false, ErrProtocol
	}
	if brief.RetryOfInvocationID == nil || brief.RetryOfConversationID == nil || explicitRecoveryProfile(brief) || prepared.workspace.Candidate != nil {
		status, body, err := client.createConversation(ctx, brief, prepared)
		return status, body, false, err
	}
	priorID := string(*brief.RetryOfInvocationID)
	prior, status, err := client.getConversation(ctx, priorID)
	if err != nil {
		return 0, nil, false, err
	}
	if status == http.StatusNotFound {
		status, body, createErr := client.createConversation(ctx, brief, prepared)
		return status, body, false, createErr
	}
	if status != http.StatusOK || prior.ID != priorID || prior.Workspace.Kind != "LocalWorkspace" || prior.Workspace.WorkingDir != prepared.workspace.WorkingDirectory || prior.Tags["tekrooinvocation"] != priorID || !kernel.Digest(prior.Tags["tekroorequest"]).Valid() || executionStillActive(prior.ExecutionStatus) {
		return 0, nil, false, ErrProtocol
	}
	var agentSettings any
	if json.Unmarshal(prepared.profile.AgentSettings, &agentSettings) != nil {
		return 0, nil, false, ErrProtocol
	}
	payload := map[string]any{
		"id":             brief.InvocationID,
		"reset_metrics":  true,
		"agent_settings": agentSettings,
		"tags": map[string]string{
			"tekrooinvocation":        string(brief.InvocationID),
			"tekroorequest":           string(prepared.requestDigest),
			pauseAfterCondensationTag: "true",
		},
	}
	status, body, err := client.request(ctx, http.MethodPost, "/api/conversations/"+url.PathEscape(priorID)+"/fork", payload)
	return status, body, true, err
}

func (client *Client) ReconcileStart(ctx context.Context, brief application.ExecutionBrief, requestDigest kernel.Digest) (application.ExternalExecutionObservation, error) {
	prepared, err := client.prepare(ctx, brief, requestDigest)
	if err != nil {
		return application.ExternalExecutionObservation{}, err
	}
	conversationID := string(brief.InvocationID)
	info, status, err := client.getConversation(ctx, conversationID)
	if err != nil {
		return application.ExternalExecutionObservation{}, err
	}
	if status == http.StatusNotFound {
		return absentObservation(brief, requestDigest), nil
	}
	if status != http.StatusOK || !conversationMatches(info, conversationID, prepared.workspace.WorkingDirectory, requestDigest) || !conversationAgentMatches(info, prepared.profile.AgentSettings) {
		return application.ExternalExecutionObservation{}, ErrProtocol
	}
	events, err := client.events(ctx, conversationID)
	if err != nil {
		return application.ExternalExecutionObservation{}, err
	}
	if executionPromptIndex(events, prepared, brief, requestDigest) < 0 {
		return absentObservation(brief, requestDigest), nil
	}
	return client.observation(ctx, brief, requestDigest, info, events, false)
}

func (client *Client) Inspect(ctx context.Context, brief application.ExecutionBrief, conversationID string, requestDigest kernel.Digest) (application.ExternalExecutionObservation, error) {
	prepared, err := client.prepare(ctx, brief, requestDigest)
	if err != nil || conversationID != string(brief.InvocationID) {
		return application.ExternalExecutionObservation{}, ErrProtocol
	}
	info, status, err := client.getConversation(ctx, conversationID)
	if err != nil || status != http.StatusOK || !conversationMatches(info, conversationID, prepared.workspace.WorkingDirectory, requestDigest) || !conversationAgentMatches(info, prepared.profile.AgentSettings) {
		return application.ExternalExecutionObservation{}, ErrProtocol
	}
	events, err := client.events(ctx, conversationID)
	if err != nil || executionPromptIndex(events, prepared, brief, requestDigest) < 0 {
		return application.ExternalExecutionObservation{}, ErrProtocol
	}
	currentPromptIndex := executionPromptIndex(events, prepared, brief, requestDigest)
	if checkpointOrdinal := missingCompactionCheckpoint(info, events, currentPromptIndex); checkpointOrdinal > 0 && (executionStillActive(info.ExecutionStatus) || info.ExecutionStatus == "paused" && pausedAtCompactionBoundary(events, currentPromptIndex)) {
		return client.injectCompactionCheckpoint(ctx, brief, requestDigest, info, events, currentPromptIndex, checkpointOrdinal)
	}
	if violation, unresolved := unresolvedModelResponsesWithoutActionOrResult(events, currentPromptIndex); unresolved > 0 {
		// OpenHands automatically asks the model to continue after one response
		// that contains neither an action nor a visible result. Do not race that
		// built-in recovery while the conversation is still running. A later
		// action resolves the incident; a second consecutive empty response or a
		// terminal conversation proves that recovery did not make progress.
		if unresolved > 1 && executionStillActive(info.ExecutionStatus) && pendingCondensationRequest(events, currentPromptIndex) {
			return client.observation(ctx, brief, requestDigest, info, events, false)
		}
		if unresolved > 1 || !executionStillActive(info.ExecutionStatus) {
			return client.failForExecutionPolicyViolation(ctx, brief, requestDigest, info, events, "MODEL_RESPONSE_WITHOUT_ACTION_OR_RESULT", violation.ID, false)
		}
	}
	if violation, repeated, violated := workPurposeToolPolicyViolation(brief.Purpose, events, currentPromptIndex); violated {
		if repeated {
			return client.failForExecutionPolicyViolation(ctx, brief, requestDigest, info, events, "WORK_PURPOSE_REPOSITORY_MUTATION_NOT_AUTHORIZED", describeAction(violation), false)
		}
		return client.correctWorkPurposeMutationViolation(ctx, brief, requestDigest, info, events, violation)
	}
	if violation, reason, violated := roleToolPolicyViolation(brief.RoleGrounding, events, currentPromptIndex); violated {
		return client.failForExecutionPolicyViolation(ctx, brief, requestDigest, info, events, reason, describeAction(violation), false)
	}
	if violation, violated := shellDisciplineViolation(events, currentPromptIndex); violated {
		return client.correctShellDisciplineViolation(ctx, brief, requestDigest, info, events, currentPromptIndex, violation)
	}
	if executionRequiresRepositoryGrounding(brief) {
		retainedGrounding, _ := retainedSuccessfulRepositoryGrounding(brief, events, currentPromptIndex)
		if violation, violated := repositoryGroundingViolation(events, currentPromptIndex, retainedGrounding); violated {
			return client.correctRepositoryGroundingViolation(ctx, brief, requestDigest, info, events, currentPromptIndex, violation)
		}
	}
	if violation, violated := acceptedContractInspectionViolation(brief, events, currentPromptIndex); violated {
		return client.correctRepositoryScopeViolation(ctx, brief, requestDigest, info, events, currentPromptIndex, violation)
	}
	if violation, repeated, violated := checkpointCompletionRepositoryViolation(events, currentPromptIndex); violated {
		return client.correctCheckpointCompletionViolation(ctx, brief, requestDigest, info, events, violation, repeated)
	}
	if violation, repeated, violated := repeatedDeterministicValidationViolation(events, currentPromptIndex); violated {
		return client.correctDeterministicValidationViolation(ctx, brief, requestDigest, info, events, violation, repeated)
	}
	if violation, violated := repositorySearchLoopViolation(events, currentPromptIndex); violated {
		return client.correctRepositoryProgressViolation(ctx, brief, requestDigest, info, events, currentPromptIndex, violation)
	}
	if info.ExecutionStatus == "finished" && executionRequiresSuccessfulRepositoryGrounding(brief) {
		if reason := successfulRepositoryGroundingFailure(brief, events, currentPromptIndex); reason != "" {
			return client.failForExecutionPolicyViolation(ctx, brief, requestDigest, info, events, reason, "", false)
		}
	}
	if info.ExecutionStatus == "finished" && purposeRequiresEditableCandidate(brief.Purpose) && slices.Contains(brief.RoleGrounding.Permissions, "repository.edit") && prepared.workspace.Candidate == nil {
		if reason := editableCandidateCompletionReason(ctx, prepared.workspace, brief.Scope.Branch, brief.Scope.BaselineSHA); reason != "" {
			return client.correctEditableCandidateCompletion(ctx, brief, requestDigest, info, events, currentPromptIndex, reason)
		}
	}
	return client.observation(ctx, brief, requestDigest, info, events, false)
}

func purposeRequiresEditableCandidate(purpose kernel.WorkPurpose) bool {
	return purpose == kernel.PurposeImplementation || purpose == kernel.PurposeRepair
}

func executionRequiresRepositoryGrounding(brief application.ExecutionBrief) bool {
	return slices.ContainsFunc(brief.ExecutionGuidance, func(instruction string) bool {
		return strings.Contains(strings.ToLower(instruction), "agents.md")
	})
}

func executionRequiresSuccessfulRepositoryGrounding(brief application.ExecutionBrief) bool {
	if brief.Purpose != kernel.PurposeReplan {
		return false
	}
	return slices.Contains(brief.RoleGrounding.Permissions, "repository.read") ||
		slices.Contains(brief.RoleGrounding.Permissions, "repository.edit")
}

// ReconcileSuperseded closes an invocation whose canonical brief changed while
// it was in flight. The original prompt is located by its durable request
// digest, so a daemon upgrade never has to recreate or trust changed prompt
// text. No new model call is made.
func (client *Client) ReconcileSuperseded(ctx context.Context, brief application.ExecutionBrief, conversationID string, originalRequestDigest, replacementRequestDigest kernel.Digest) (application.ExternalExecutionObservation, error) {
	prepared, err := client.prepare(ctx, brief, replacementRequestDigest)
	if err != nil || conversationID != string(brief.InvocationID) || !originalRequestDigest.Valid() || originalRequestDigest == replacementRequestDigest {
		return application.ExternalExecutionObservation{}, ErrProtocol
	}
	info, status, err := client.getConversation(ctx, conversationID)
	if err != nil || status != http.StatusOK || !conversationMatches(info, conversationID, prepared.workspace.WorkingDirectory, originalRequestDigest) {
		return application.ExternalExecutionObservation{}, ErrProtocol
	}
	events, err := client.events(ctx, conversationID)
	if err != nil {
		return application.ExternalExecutionObservation{}, err
	}
	index := promptDigestIndex(events, originalRequestDigest)
	if index < 0 {
		return application.ExternalExecutionObservation{}, ErrProtocol
	}
	interrupted := false
	if executionStillActive(info.ExecutionStatus) {
		status, _, err = client.request(ctx, http.MethodPost, "/api/conversations/"+url.PathEscape(conversationID)+"/interrupt", nil)
		if err != nil || status != http.StatusOK && status != http.StatusNoContent && status != http.StatusConflict {
			return application.ExternalExecutionObservation{}, ErrProtocol
		}
		interrupted = true
		info, status, err = client.getConversation(ctx, conversationID)
		if err != nil || status != http.StatusOK {
			return application.ExternalExecutionObservation{}, ErrProtocol
		}
		events, err = client.events(ctx, conversationID)
		if err != nil {
			return application.ExternalExecutionObservation{}, err
		}
		index = promptDigestIndex(events, originalRequestDigest)
		if index < 0 {
			return application.ExternalExecutionObservation{}, ErrProtocol
		}
	}
	return client.observationAt(brief, originalRequestDigest, info, events, index, interrupted)
}

const shellDisciplineCorrectionPrefix = "TEKROO_SHELL_DISCIPLINE_CORRECTION:"
const workPurposeMutationCorrectionPrefix = "TEKROO_WORK_PURPOSE_MUTATION_CORRECTION:"
const repositoryProgressCorrectionPrefix = "TEKROO_REPOSITORY_PROGRESS_CORRECTION:"
const repositoryGroundingCorrectionPrefix = "TEKROO_REPOSITORY_GROUNDING_CORRECTION:"
const repositoryScopeCorrectionPrefix = "TEKROO_REPOSITORY_SCOPE_CORRECTION:"
const editableCandidateCompletionCorrectionPrefix = "TEKROO_CANDIDATE_COMPLETION_CORRECTION:"
const checkpointCompletionCorrectionPrefix = "TEKROO_CHECKPOINT_COMPLETION_CORRECTION:"
const deterministicValidationCorrectionPrefix = "TEKROO_DETERMINISTIC_VALIDATION_CORRECTION:"
const compactionCheckpointPrefix = "TEKROO_PROGRESS_CHECKPOINT:"
const maximumEquivalentSuccessfulValidations = 2
const maximumCheckpointCompletionReads = 8

// checkpointCompletionRepositoryViolation detects engineering work resuming
// after a progress checkpoint declared the finish step. A bounded number of
// reads remains available to recover exact details lost at compaction, but it
// must not permit an agent to reconstruct the same evidence indefinitely.
// Mutations remain immediate violations.
func checkpointCompletionRepositoryViolation(events []rawEvent, promptIndex int) (rawEvent, bool, bool) {
	checkpointIndex := -1
	for index, event := range events {
		if index <= promptIndex {
			continue
		}
		checkpoint, found := progressCheckpointFromEvent(event)
		if found && strings.Contains(checkpoint.NextAction, "finish tool") {
			checkpointIndex = index
		}
	}
	if checkpointIndex < 0 {
		return rawEvent{}, false, false
	}
	correctionIndex := -1
	for index := checkpointIndex + 1; index < len(events); index++ {
		event := events[index]
		if event.Kind == "MessageEvent" && event.Source == "user" && strings.HasPrefix(event.Text, checkpointCompletionCorrectionPrefix) {
			correctionIndex = index
		}
	}
	if correctionIndex >= 0 {
		for index := correctionIndex + 1; index < len(events); index++ {
			event := events[index]
			if event.Kind == "ActionEvent" && event.Source == "agent" && repositoryAction(event) {
				return event, true, true
			}
		}
		return rawEvent{}, false, false
	}
	reads := 0
	for index := checkpointIndex + 1; index < len(events); index++ {
		event := events[index]
		if event.Kind != "ActionEvent" || event.Source != "agent" || !repositoryAction(event) {
			continue
		}
		if mutationAction(event) {
			return event, false, true
		}
		reads++
		if reads > maximumCheckpointCompletionReads {
			return event, false, true
		}
	}
	return rawEvent{}, false, false
}

func shellDisciplineViolation(events []rawEvent, promptIndex int) (rawEvent, bool) {
	for index, event := range events {
		if index <= promptIndex || event.Kind != "ActionEvent" || event.Source != "agent" || event.ToolName != "terminal" {
			continue
		}
		command := strings.TrimSpace(event.ActionCommand)
		if violatesShellDiscipline(command) && !shellDisciplineViolationCorrected(events, index) {
			return event, true
		}
	}
	return rawEvent{}, false
}

func shellDisciplineViolationCorrected(events []rawEvent, violationIndex int) bool {
	if violationIndex < 0 {
		return false
	}
	for index := violationIndex + 1; index < len(events); index++ {
		// A model may emit multiple terminal actions in one response. One
		// correction after that response covers every violation already emitted;
		// a later violation remains uncorrected and is therefore terminal.
		if events[index].Kind == "MessageEvent" && events[index].Source == "user" && strings.Contains(events[index].Text, shellDisciplineCorrectionPrefix) {
			return true
		}
	}
	return false
}

func eventIndexByID(events []rawEvent, id string) int {
	return slices.IndexFunc(events, func(event rawEvent) bool { return event.ID == id })
}

func shellDisciplineCorrectionAllowed(events []rawEvent, promptIndex int, violation rawEvent) bool {
	lastCorrection := -1
	violationIndex := -1
	for index, event := range events {
		if index <= promptIndex {
			continue
		}
		if event.Kind == "MessageEvent" && event.Source == "user" && strings.Contains(event.Text, shellDisciplineCorrectionPrefix) {
			lastCorrection = index
		}
		if event.ID == violation.ID {
			violationIndex = index
			break
		}
	}
	if lastCorrection < 0 {
		return true
	}
	if violationIndex < 0 {
		return false
	}

	// A correction is an incident boundary, not a lifetime quota. An isolated
	// formatting slip after the agent has completed a compliant action must not
	// discard useful long-running work. Conversely, a repeat before any
	// successful compliant action proves that the correction did not restore
	// progress and terminates the invocation.
	compliant := make(map[string]struct{})
	for index := lastCorrection + 1; index < violationIndex; index++ {
		event := events[index]
		if event.Kind == "ActionEvent" && event.Source == "agent" {
			if event.ToolName != "terminal" || !violatesShellDiscipline(strings.TrimSpace(event.ActionCommand)) {
				compliant[event.ToolCallID] = struct{}{}
			}
			continue
		}
		if event.Kind != "ObservationEvent" {
			continue
		}
		if _, found := compliant[event.ToolCallID]; !found {
			continue
		}
		if !event.ObservationError && !event.ObservationTimeout && (event.ObservationExitCode == nil || *event.ObservationExitCode == 0) {
			return true
		}
	}
	return false
}

func repositoryProgressCorrectionCount(events []rawEvent, promptIndex int) int {
	count := 0
	for index, event := range events {
		if index > promptIndex && event.Kind == "MessageEvent" && event.Source == "user" && strings.Contains(event.Text, repositoryProgressCorrectionPrefix) {
			count++
		}
	}
	return count
}

func repositoryProgressCorrectionAllowed(events []rawEvent, promptIndex int, violation rawEvent) bool {
	type pendingAction struct {
		toolCallID     string
		requiresOutput bool
	}
	lastCorrection := -1
	violationIndex := -1
	for index, event := range events {
		if index <= promptIndex {
			continue
		}
		if event.Kind == "MessageEvent" && event.Source == "user" && strings.HasPrefix(event.Text, repositoryProgressCorrectionPrefix) {
			lastCorrection = index
		}
		if event.ID == violation.ID {
			violationIndex = index
			break
		}
	}
	if lastCorrection < 0 {
		return true
	}
	if violationIndex < 0 {
		return false
	}

	// A progress correction bounds one no-progress episode rather than the
	// entire invocation. A later incident is correctable only after the agent
	// has successfully read source, changed the repository, or completed a
	// deterministic validation. Search/listing churn alone cannot reset it.
	pendingByTool := make(map[string][]pendingAction)
	for index := lastCorrection + 1; index < violationIndex; index++ {
		event := events[index]
		if event.Kind == "ActionEvent" && event.Source == "agent" {
			contentRead := event.ToolName != "repository_search" && repositoryContentReadAction(event)
			if contentRead || mutationAction(event) || deterministicValidationAction(event) {
				pendingByTool[event.ToolName] = append(pendingByTool[event.ToolName], pendingAction{
					toolCallID: event.ToolCallID, requiresOutput: contentRead,
				})
			}
			continue
		}
		if event.Kind != "ObservationEvent" || len(pendingByTool[event.ToolName]) == 0 {
			continue
		}
		pendingIndex := 0
		if event.ToolCallID != "" {
			pendingIndex = slices.IndexFunc(pendingByTool[event.ToolName], func(pending pendingAction) bool {
				return pending.toolCallID == event.ToolCallID
			})
			if pendingIndex < 0 {
				continue
			}
		}
		pending := pendingByTool[event.ToolName][pendingIndex]
		pendingByTool[event.ToolName] = slices.Delete(pendingByTool[event.ToolName], pendingIndex, pendingIndex+1)
		succeeded := !event.ObservationError && !event.ObservationTimeout && (event.ObservationExitCode == nil || *event.ObservationExitCode == 0)
		if succeeded && (!pending.requiresOutput || strings.TrimSpace(event.Text) != "") {
			return true
		}
	}
	return false
}

func repositoryProgressViolationCorrected(events []rawEvent, violationIndex int) bool {
	if violationIndex < 0 {
		return false
	}
	for index := violationIndex + 1; index < len(events); index++ {
		if events[index].Kind == "MessageEvent" && events[index].Source == "user" &&
			(strings.HasPrefix(events[index].Text, repositoryProgressCorrectionPrefix) || strings.HasPrefix(events[index].Text, repositoryScopeCorrectionPrefix)) {
			return true
		}
	}
	return false
}

func repositoryGroundingCorrectionCount(events []rawEvent, promptIndex int) int {
	count := 0
	for index, event := range events {
		if index > promptIndex && event.Kind == "MessageEvent" && event.Source == "user" && strings.Contains(event.Text, repositoryGroundingCorrectionPrefix) {
			count++
		}
	}
	return count
}

func repositoryGroundingViolationCorrected(events []rawEvent, violationIndex int) bool {
	if violationIndex < 0 {
		return false
	}
	for index := violationIndex + 1; index < len(events); index++ {
		if events[index].Kind == "MessageEvent" && events[index].Source == "user" && strings.HasPrefix(events[index].Text, repositoryGroundingCorrectionPrefix) {
			return true
		}
	}
	return false
}

func repositoryScopeCorrectionCount(events []rawEvent, promptIndex int) int {
	count := 0
	for index, event := range events {
		if index > promptIndex && event.Kind == "MessageEvent" && event.Source == "user" && strings.HasPrefix(event.Text, repositoryScopeCorrectionPrefix) {
			count++
		}
	}
	return count
}

func repositoryScopeViolationCorrected(events []rawEvent, violationIndex int) bool {
	if violationIndex < 0 {
		return false
	}
	for index := violationIndex + 1; index < len(events); index++ {
		if events[index].Kind == "MessageEvent" && events[index].Source == "user" && strings.HasPrefix(events[index].Text, repositoryScopeCorrectionPrefix) {
			return true
		}
	}
	return false
}

func roleToolPolicyViolation(grounding application.RoleExecutionGrounding, events []rawEvent, promptIndex int) (rawEvent, string, bool) {
	canRead := slices.Contains(grounding.Permissions, "repository.read") || slices.Contains(grounding.Permissions, "repository.edit")
	canEdit := slices.Contains(grounding.Permissions, "repository.edit")
	for index, event := range events {
		if index <= promptIndex || event.Kind != "ActionEvent" || event.Source != "agent" || !repositoryAction(event) {
			continue
		}
		if !canRead {
			return event, "ROLE_REPOSITORY_TOOL_NOT_AUTHORIZED", true
		}
		if mutationAction(event) && !canEdit {
			return event, "ROLE_REPOSITORY_MUTATION_NOT_AUTHORIZED", true
		}
	}
	return rawEvent{}, "", false
}

// workPurposeToolPolicyViolation detects repository mutations that actually
// took effect during a purpose which may not modify the repository (validator,
// reviewer, architect, promoter). An attempted mutation the sandbox already
// refused carries no damage and needs no harness response: the agent has its
// own error observation. Only a mutation that succeeded can contaminate the
// state being evaluated, so the first one earns a correction and a repeat
// proves the agent will not stop and fences the invocation.
func workPurposeToolPolicyViolation(purpose kernel.WorkPurpose, events []rawEvent, promptIndex int) (rawEvent, bool, bool) {
	if purposeRequiresEditableCandidate(purpose) {
		return rawEvent{}, false, false
	}
	uncorrected := 0
	corrected := 0
	for index, event := range events {
		if index <= promptIndex || event.Kind != "ActionEvent" || event.Source != "agent" || !mutationAction(event) {
			continue
		}
		if !repositoryMutationTookEffect(events, index) {
			continue
		}
		if workPurposeMutationCorrected(events, index) {
			corrected++
			continue
		}
		uncorrected++
		if uncorrected == 1 {
			return event, corrected > 0, true
		}
		return event, true, true
	}
	return rawEvent{}, false, false
}

func workPurposeMutationCorrected(events []rawEvent, actionIndex int) bool {
	for index := actionIndex + 1; index < len(events); index++ {
		if events[index].Kind == "MessageEvent" && events[index].Source == "user" && strings.Contains(events[index].Text, workPurposeMutationCorrectionPrefix+events[actionIndex].ID) {
			return true
		}
	}
	return false
}

// repositoryMutationTookEffect reports whether the action at actionIndex is
// followed by an observation showing the mutation succeeded. An action whose
// observation has not arrived yet is deliberately not treated as effective;
// the next observation cycle re-evaluates it.
func repositoryMutationTookEffect(events []rawEvent, actionIndex int) bool {
	action := events[actionIndex]
	for index := actionIndex + 1; index < len(events); index++ {
		event := events[index]
		if event.Kind != "ObservationEvent" {
			continue
		}
		if event.ToolName != action.ToolName || (action.ToolCallID != "" && event.ToolCallID != "" && event.ToolCallID != action.ToolCallID) {
			continue
		}
		return !event.ObservationError && !event.ObservationTimeout && (event.ObservationExitCode == nil || *event.ObservationExitCode == 0)
	}
	return false
}

func repositoryGroundingViolation(events []rawEvent, promptIndex int, retainedGrounding bool) (rawEvent, bool) {
	type pendingRead struct {
		toolName   string
		toolCallID string
	}
	pending := make([]pendingRead, 0)
	grounded := retainedGrounding
	for index := promptIndex + 1; index < len(events); index++ {
		event := events[index]
		if event.Kind == "MessageEvent" && event.Source == "user" && strings.HasPrefix(event.Text, repositoryGroundingCorrectionPrefix) {
			// A correction rejects the repository action that raced the
			// AGENTS.md read, not a successful AGENTS.md read itself. Preserve
			// grounding when the read completed before the correction was
			// injected; otherwise the model is forced to repeat an instruction
			// read it has already received and the next useful action is falsely
			// classified as a second violation.
			pending = pending[:0]
			continue
		}
		if event.Kind == "ActionEvent" && event.Source == "agent" && repositoryAction(event) {
			if agentsInstructionReadAction(event) {
				pending = append(pending, pendingRead{toolName: event.ToolName, toolCallID: event.ToolCallID})
				continue
			}
			// Agents may establish where they are and whether the workspace is
			// clean before reading repository instructions. These actions expose
			// no source or design content and do not mutate repository state, so
			// treating them as substantive work creates false grounding failures.
			if workspaceOrientationAction(event) {
				continue
			}
			if grounded {
				continue
			}
			// Once this exact pre-grounding action has been rejected, later
			// daemon polls must not rediscover it as a new violation.
			if repositoryGroundingViolationCorrected(events, index) {
				continue
			}
			if repositoryScopeViolationCorrected(events, index) {
				continue
			}
			// A compound terminal action rejected by the shell guard did not
			// inspect the repository. Ignore it here once its correction is in
			// the journal; the next compliant repository action still requires
			// AGENTS.md grounding.
			if event.ToolName == "terminal" && violatesShellDiscipline(strings.TrimSpace(event.ActionCommand)) && shellDisciplineViolationCorrected(events, index) {
				continue
			}
			return event, true
		}
		if event.Kind != "ObservationEvent" {
			continue
		}
		pendingIndex := slices.IndexFunc(pending, func(candidate pendingRead) bool {
			return candidate.toolName == event.ToolName && (event.ToolCallID == "" || candidate.toolCallID == event.ToolCallID)
		})
		if pendingIndex < 0 {
			continue
		}
		pending = slices.Delete(pending, pendingIndex, pendingIndex+1)
		succeeded := !event.ObservationError && !event.ObservationTimeout && (event.ObservationExitCode == nil || *event.ObservationExitCode == 0)
		if succeeded && strings.TrimSpace(event.Text) != "" {
			grounded = true
		}
	}
	return rawEvent{}, false
}

func acceptedContractInspectionViolation(brief application.ExecutionBrief, events []rawEvent, promptIndex int) (rawEvent, bool) {
	protected := slices.ContainsFunc(brief.ExecutionGuidance, func(instruction string) bool {
		text := strings.ToLower(instruction)
		return strings.Contains(text, "contracts") && (strings.Contains(text, "do not modify") || strings.Contains(text, "never modify") || strings.Contains(text, "do not inspect"))
	})
	if !protected {
		return rawEvent{}, false
	}
	for index, event := range events {
		if index <= promptIndex || event.Kind != "ActionEvent" || event.Source != "agent" || !acceptedContractInspectionAction(event) || !mutationAction(event) {
			continue
		}
		if repositoryScopeViolationCorrected(events, index) {
			continue
		}
		return event, true
	}
	return rawEvent{}, false
}

func acceptedContractInspectionAction(event rawEvent) bool {
	// Only tools capable of touching repository state can inspect or modify an
	// accepted package. A finish message may legitimately cite a CONTRACTS path
	// while explaining that it remains unchanged; treating result prose as a
	// repository action rejects valid work and can send a role back into an
	// incoherent execution phase.
	if !repositoryAction(event) {
		return false
	}
	for _, value := range []string{event.ActionPath, event.ActionCommand, string(event.ActionPayload)} {
		for _, field := range strings.Fields(strings.ReplaceAll(value, "\\", "/")) {
			field = strings.Trim(field, "\"'`{}[](),:")
			for _, segment := range strings.Split(field, "/") {
				if strings.EqualFold(strings.Trim(segment, "\"'`{}[](),:"), "CONTRACTS") {
					return true
				}
			}
		}
	}
	return false
}

func agentsInstructionReadAction(event rawEvent) bool {
	if !repositoryContentReadAction(event) {
		return false
	}
	target := event.ActionPath
	if target == "" {
		target = event.ActionCommand
	}
	return strings.Contains(strings.ToLower(target), "agents.md")
}

func workspaceOrientationAction(event rawEvent) bool {
	if event.Kind != "ActionEvent" || event.Source != "agent" || event.ToolName != "terminal" {
		return false
	}
	command := strings.TrimSpace(event.ActionCommand)
	if command == "" || violatesShellDiscipline(command) {
		return false
	}
	fields := strings.Fields(command)
	if len(fields) == 1 && fields[0] == "pwd" {
		return true
	}
	if len(fields) >= 1 && fields[0] == "ls" {
		return true
	}
	if len(fields) >= 2 && fields[0] == "git" && fields[1] == "status" {
		return true
	}
	return len(fields) >= 3 && fields[0] == "git" && fields[1] == "rev-parse" && slices.Contains(fields[2:], "--show-toplevel")
}

func violatesShellDiscipline(command string) bool {
	command = strings.TrimSpace(command)
	if command == "cd" || strings.HasPrefix(command, "cd ") || strings.ContainsAny(command, "\r\n") {
		return true
	}
	fields := strings.Fields(command)
	if len(fields) > 1 {
		tool := filepath.Base(fields[0])
		if tool == "git" || tool == "make" {
			for _, field := range fields[1:] {
				if field == "-C" || strings.HasPrefix(field, "-C/") {
					return true
				}
			}
		}
	}
	var singleQuoted, doubleQuoted, escaped bool
	for _, character := range command {
		if escaped {
			escaped = false
			continue
		}
		if character == '\\' && !singleQuoted {
			escaped = true
			continue
		}
		if character == '\'' && !doubleQuoted {
			singleQuoted = !singleQuoted
			continue
		}
		if character == '"' && !singleQuoted {
			doubleQuoted = !doubleQuoted
			continue
		}
		if singleQuoted {
			continue
		}
		if character == '$' || character == '`' {
			return true
		}
		if !doubleQuoted && (character == ';' || character == '|' || character == '&') {
			return true
		}
	}
	return false
}

func repositorySearchLoopViolation(events []rawEvent, promptIndex int) (rawEvent, bool) {
	type pendingAction struct {
		event      rawEvent
		toolCallID string
		signature  string
		mutation   bool
		batch      uint64
	}
	seen := make(map[string]map[string]struct{})
	pendingByTool := make(map[string][]pendingAction)
	batchNovel := make(map[uint64]bool)
	var currentBatch uint64
	actionBatchOpen := false
	for index, event := range events {
		if index <= promptIndex {
			continue
		}
		if event.Kind == "MessageEvent" && event.Source == "user" && strings.HasPrefix(event.Text, compactionCheckpointPrefix) {
			// The first exact reread after a compacted or restarted execution is
			// legitimate: the model may need to reconstruct a detail that could not
			// be serialized into the checkpoint. A second identical action/result in
			// this new continuity period is still a real no-progress signal.
			seen = make(map[string]map[string]struct{})
			pendingByTool = make(map[string][]pendingAction)
			batchNovel = make(map[uint64]bool)
			actionBatchOpen = false
			continue
		}
		if event.Kind == "ActionEvent" && event.Source == "agent" {
			if !actionBatchOpen {
				currentBatch++
				actionBatchOpen = true
			}
			if mutationAction(event) {
				pendingByTool[event.ToolName] = append(pendingByTool[event.ToolName], pendingAction{event: event, toolCallID: event.ToolCallID, mutation: true, batch: currentBatch})
				continue
			}
			signature, tracked := equivalentRepositoryActionSignature(event)
			if tracked {
				if len(seen[signature]) == 0 {
					batchNovel[currentBatch] = true
				}
				pendingByTool[event.ToolName] = append(pendingByTool[event.ToolName], pendingAction{event: event, toolCallID: event.ToolCallID, signature: signature, batch: currentBatch})
			}
			continue
		}
		actionBatchOpen = false
		if event.Kind != "ObservationEvent" || len(pendingByTool[event.ToolName]) == 0 {
			continue
		}
		pendingIndex := 0
		if event.ToolCallID != "" {
			pendingIndex = slices.IndexFunc(pendingByTool[event.ToolName], func(pending pendingAction) bool {
				return pending.toolCallID == event.ToolCallID
			})
			if pendingIndex < 0 {
				continue
			}
		}
		pending := pendingByTool[event.ToolName][pendingIndex]
		pendingByTool[event.ToolName] = slices.Delete(pendingByTool[event.ToolName], pendingIndex, pendingIndex+1)
		succeeded := !event.ObservationError && !event.ObservationTimeout && (event.ObservationExitCode == nil || *event.ObservationExitCode == 0)
		if pending.mutation {
			if succeeded {
				seen = make(map[string]map[string]struct{})
				batchNovel = make(map[uint64]bool)
			}
			continue
		}
		resultSignature, ok := repositoryObservationSignature(event)
		if !ok {
			continue
		}
		results := seen[pending.signature]
		if results == nil {
			results = make(map[string]struct{})
			seen[pending.signature] = results
		}
		if _, repeated := results[resultSignature]; repeated && !batchNovel[pending.batch] {
			if !repositoryProgressViolationCorrected(events, eventIndexByID(events, pending.event.ID)) {
				return pending.event, true
			}
			continue
		}
		results[resultSignature] = struct{}{}
	}
	return rawEvent{}, false
}

func repositoryObservationSignature(event rawEvent) (string, bool) {
	if event.Kind != "ObservationEvent" {
		return "", false
	}
	var observation json.RawMessage
	if len(event.Raw) > 0 {
		var envelope struct {
			Observation json.RawMessage `json:"observation"`
		}
		if json.Unmarshal(event.Raw, &envelope) == nil && len(envelope.Observation) > 0 {
			var canonical any
			if json.Unmarshal(envelope.Observation, &canonical) == nil {
				observation, _ = json.Marshal(canonical)
			}
		}
	}
	encoded, err := json.Marshal(struct {
		Kind      string          `json:"kind"`
		Text      string          `json:"text"`
		Error     bool            `json:"error"`
		Timeout   bool            `json:"timeout"`
		ExitCode  *int            `json:"exit_code,omitempty"`
		Truncated *bool           `json:"truncated,omitempty"`
		Payload   json.RawMessage `json:"payload,omitempty"`
	}{event.ObservationKind, event.Text, event.ObservationError, event.ObservationTimeout, event.ObservationExitCode, event.ObservationTruncated, observation})
	if err != nil {
		return "", false
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), true
}

func repositoryAction(event rawEvent) bool {
	switch event.ToolName {
	case "terminal", "file_editor", "glob", "repository_search", "repository_view":
		return true
	default:
		return false
	}
}

func repositoryFileListingAction(event rawEvent) bool {
	if event.ToolName == "glob" {
		return true
	}
	if event.ToolName == "file_editor" || event.ToolName == "repository_view" {
		if event.ToolName == "file_editor" && !strings.EqualFold(strings.TrimSpace(event.ActionCommand), "view") || event.ActionPath == "" {
			return false
		}
		info, err := os.Stat(event.ActionPath)
		if err == nil {
			return info.IsDir()
		}
		return !probableRepositoryFile(event.ActionPath)
	}
	if event.ToolName != "terminal" {
		return false
	}
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(event.ActionCommand)))
	if len(fields) == 0 {
		return false
	}
	command := filepath.Base(fields[0])
	if command == "ls" || command == "find" || command == "fd" || command == "tree" {
		return true
	}
	if command == "git" && len(fields) > 1 && fields[1] == "ls-files" {
		return true
	}
	if command != "rg" {
		return false
	}
	for _, field := range fields[1:] {
		if field == "-l" || field == "--files" || field == "--files-with-matches" || strings.Contains(field, "l") && strings.HasPrefix(field, "-") && !strings.HasPrefix(field, "--") {
			return true
		}
	}
	return false
}

func repositoryInspectionAction(event rawEvent) bool {
	if event.ToolName == "repository_search" {
		return true
	}
	if event.ToolName == "file_editor" || event.ToolName == "repository_view" {
		return (event.ToolName == "repository_view" || strings.EqualFold(strings.TrimSpace(event.ActionCommand), "view")) && event.ActionPath != "" && !repositoryFileListingAction(event)
	}
	if event.ToolName != "terminal" {
		return false
	}
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(event.ActionCommand)))
	if len(fields) < 2 {
		return false
	}
	switch filepath.Base(fields[0]) {
	case "cat", "head", "tail", "sed":
		return true
	case "rg", "grep":
		return !repositoryFileListingAction(event)
	default:
		return false
	}
}

func repositoryContentReadAction(event rawEvent) bool {
	if event.ToolName == "repository_search" {
		return true
	}
	if event.ToolName == "file_editor" || event.ToolName == "repository_view" {
		return (event.ToolName == "repository_view" || strings.EqualFold(strings.TrimSpace(event.ActionCommand), "view")) && event.ActionPath != "" && !repositoryFileListingAction(event)
	}
	if event.ToolName != "terminal" {
		return false
	}
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(event.ActionCommand)))
	if len(fields) < 2 {
		return false
	}
	switch filepath.Base(fields[0]) {
	case "cat", "grep", "head", "tail", "sed":
		return true
	default:
		return false
	}
}

func probableRepositoryFile(path string) bool {
	base := strings.ToLower(filepath.Base(filepath.Clean(path)))
	if filepath.Ext(base) != "" {
		return true
	}
	switch base {
	case "dockerfile", "makefile", "license", "notice", "readme", "go.mod", "go.sum":
		return true
	default:
		return false
	}
}

func successfulRepositoryGroundingFailure(brief application.ExecutionBrief, events []rawEvent, promptIndex int) string {
	type pendingRead struct {
		toolCallID string
		agents     bool
	}
	pendingByTool := make(map[string][]pendingRead)
	agentsRead, sourceRead := retainedSuccessfulRepositoryGrounding(brief, events, promptIndex)
	for index, event := range events {
		if index <= promptIndex {
			continue
		}
		if event.Kind == "ActionEvent" && event.Source == "agent" && repositoryContentReadAction(event) {
			target := event.ActionPath
			if target == "" {
				target = event.ActionCommand
			}
			pendingByTool[event.ToolName] = append(pendingByTool[event.ToolName], pendingRead{toolCallID: event.ToolCallID, agents: strings.Contains(strings.ToLower(target), "agents.md")})
			continue
		}
		if event.Kind != "ObservationEvent" || len(pendingByTool[event.ToolName]) == 0 {
			continue
		}
		pendingIndex := 0
		if event.ToolCallID != "" {
			pendingIndex = slices.IndexFunc(pendingByTool[event.ToolName], func(pending pendingRead) bool {
				return pending.toolCallID == event.ToolCallID
			})
			if pendingIndex < 0 {
				continue
			}
		}
		pending := pendingByTool[event.ToolName][pendingIndex]
		pendingByTool[event.ToolName] = slices.Delete(pendingByTool[event.ToolName], pendingIndex, pendingIndex+1)
		succeeded := !event.ObservationError && !event.ObservationTimeout && (event.ObservationExitCode == nil || *event.ObservationExitCode == 0)
		if !succeeded || strings.TrimSpace(event.Text) == "" {
			continue
		}
		if pending.agents {
			agentsRead = true
		} else {
			sourceRead = true
		}
	}
	if !agentsRead {
		return "REPLAN_AGENTS_INSTRUCTIONS_NOT_INSPECTED"
	}
	if !sourceRead {
		return "REPLAN_REPOSITORY_NOT_INSPECTED"
	}
	return ""
}

func retainedSuccessfulRepositoryGrounding(brief application.ExecutionBrief, events []rawEvent, promptIndex int) (bool, bool) {
	if !explicitRecoveryProfile(brief) || promptIndex < 0 || promptIndex >= len(events) {
		return false, false
	}
	var input struct {
		RecoveryCheckpoint *progressCheckpoint `json:"recovery_checkpoint"`
	}
	if json.Unmarshal([]byte(events[promptIndex].Text), &input) != nil || input.RecoveryCheckpoint == nil {
		return false, false
	}
	checkpoint := input.RecoveryCheckpoint
	if checkpoint.SchemaVersion != "tekroo.teams.execution-progress-checkpoint/1.2.0" || checkpoint.Source != "OPENHANDS_EVENT_JOURNAL" || !checkpoint.SourceJournalSHA256.Valid() || checkpoint.InvocationID != brief.InvocationID || checkpoint.PriorInvocationID == nil || brief.RetryOfInvocationID == nil || *checkpoint.PriorInvocationID != *brief.RetryOfInvocationID {
		return false, false
	}
	encoded, err := json.Marshal(brief)
	if err != nil {
		return false, false
	}
	digest := sha256.Sum256(encoded)
	if checkpoint.AuthoritativeExecution.ExecutionBriefSHA256 != kernel.Digest(hex.EncodeToString(digest[:])) {
		return false, false
	}
	agentsRead := false
	sourceRead := false
	for _, evidence := range checkpoint.RepositoryEvidence {
		if evidence.Outcome != "SUCCEEDED" || !evidence.ObservationSHA256.Valid() {
			continue
		}
		target := evidence.Path
		if target == "" {
			target = evidence.Command
		}
		if strings.Contains(strings.ToLower(target), "agents.md") {
			agentsRead = true
		} else {
			sourceRead = true
		}
	}
	return agentsRead, sourceRead
}

func equivalentRepositoryActionSignature(event rawEvent) (string, bool) {
	// This guard exists to break repository-discovery loops. Build, test,
	// formatting, and version-control commands may legitimately be repeated to
	// establish stability or to verify state after another check; treating those
	// commands as repeated discovery can interrupt a successful validation just
	// before it reports its result.
	if !repositoryInspectionAction(event) && !repositoryFileListingAction(event) {
		return "", false
	}
	if event.ToolName == "terminal" && strings.TrimSpace(event.ActionCommand) == "" {
		return "", false
	}
	payload := event.ActionPayload
	if len(payload) == 0 {
		encoded, err := json.Marshal(struct {
			Command string `json:"command"`
			Path    string `json:"path,omitempty"`
		}{strings.TrimSpace(event.ActionCommand), event.ActionPath})
		if err != nil {
			return "", false
		}
		payload = encoded
	} else {
		var canonical any
		if json.Unmarshal(payload, &canonical) != nil {
			return "", false
		}
		encoded, err := json.Marshal(canonical)
		if err != nil {
			return "", false
		}
		payload = encoded
	}
	digest := sha256.Sum256(payload)
	return event.ToolName + ":" + hex.EncodeToString(digest[:]), true
}

func repeatedDeterministicValidationViolation(events []rawEvent, promptIndex int) (rawEvent, bool, bool) {
	succeeded := successfulActionIndexes(events)
	boundary := promptIndex
	for index, event := range events {
		if index <= promptIndex {
			continue
		}
		if event.Kind == "Condensation" || event.Kind == "MessageEvent" && event.Source == "user" && strings.HasPrefix(event.Text, compactionCheckpointPrefix) {
			boundary = index
			continue
		}
		if succeeded[index] && mutationAction(event) {
			boundary = index
		}
	}

	correctionIndex := -1
	correctedSignature := ""
	for index := boundary + 1; index < len(events); index++ {
		event := events[index]
		if event.Kind != "MessageEvent" || event.Source != "user" || !strings.HasPrefix(event.Text, deterministicValidationCorrectionPrefix) {
			continue
		}
		correctionIndex = index
		line := strings.SplitN(event.Text, "\n", 2)[0]
		violationID := strings.TrimSpace(strings.TrimPrefix(line, deterministicValidationCorrectionPrefix))
		for prior := boundary + 1; prior < index; prior++ {
			if events[prior].ID == violationID {
				correctedSignature, _ = equivalentDeterministicValidationSignature(events[prior])
				break
			}
		}
	}

	start := boundary + 1
	repeated := false
	if correctionIndex >= start {
		start = correctionIndex + 1
		repeated = true
	}
	counts := make(map[string]int)
	for index := start; index < len(events); index++ {
		event := events[index]
		signature, validation := equivalentDeterministicValidationSignature(event)
		if !validation {
			continue
		}
		if repeated && correctedSignature != "" && signature == correctedSignature {
			return event, true, true
		}
		if !succeeded[index] {
			continue
		}
		counts[signature]++
		if counts[signature] > maximumEquivalentSuccessfulValidations {
			return event, repeated, true
		}
	}
	return rawEvent{}, false, false
}

func successfulActionIndexes(events []rawEvent) map[int]bool {
	type pendingAction struct {
		index      int
		toolCallID string
	}
	pendingByTool := make(map[string][]pendingAction)
	succeeded := make(map[int]bool)
	for index, event := range events {
		if event.Kind == "ActionEvent" && event.Source == "agent" {
			pendingByTool[event.ToolName] = append(pendingByTool[event.ToolName], pendingAction{index: index, toolCallID: event.ToolCallID})
			continue
		}
		if event.Kind != "ObservationEvent" || len(pendingByTool[event.ToolName]) == 0 {
			continue
		}
		pendingIndex := 0
		if event.ToolCallID != "" {
			pendingIndex = slices.IndexFunc(pendingByTool[event.ToolName], func(pending pendingAction) bool {
				return pending.toolCallID == event.ToolCallID
			})
			if pendingIndex < 0 {
				continue
			}
		}
		pending := pendingByTool[event.ToolName][pendingIndex]
		pendingByTool[event.ToolName] = slices.Delete(pendingByTool[event.ToolName], pendingIndex, pendingIndex+1)
		if !event.ObservationError && !event.ObservationTimeout && (event.ObservationExitCode == nil || *event.ObservationExitCode == 0) {
			succeeded[pending.index] = true
		}
	}
	return succeeded
}

func equivalentDeterministicValidationSignature(event rawEvent) (string, bool) {
	if !deterministicValidationAction(event) {
		return "", false
	}
	fields := strings.Fields(strings.TrimSpace(event.ActionCommand))
	if len(fields) < 2 {
		return "", false
	}
	parts := make([]string, 0, len(fields))
	parts = append(parts, strings.ToLower(filepath.Base(strings.Trim(fields[0], "\"'"))), strings.ToLower(strings.Trim(fields[1], "\"'")))
	for index := 2; index < len(fields); index++ {
		field := strings.Trim(fields[index], "\"'")
		lower := strings.ToLower(field)
		if lower == "-v" || lower == "-json" || strings.HasPrefix(lower, "-count=") || strings.HasPrefix(lower, "-timeout=") || strings.Contains(lower, ">&") {
			continue
		}
		if lower == "-count" || lower == "-timeout" {
			index++
			continue
		}
		parts = append(parts, strings.TrimSuffix(field, "/"))
	}
	if len(parts) > 2 {
		slices.Sort(parts[2:])
	}
	encoded, err := json.Marshal(parts)
	if err != nil {
		return "", false
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), true
}

func (client *Client) failForExecutionPolicyViolation(ctx context.Context, brief application.ExecutionBrief, requestDigest kernel.Digest, info conversationInfo, events []rawEvent, reason, command string, retryable bool) (application.ExternalExecutionObservation, error) {
	conversationID := string(brief.InvocationID)
	if executionStillActive(info.ExecutionStatus) {
		status, _, err := client.request(ctx, http.MethodPost, "/api/conversations/"+url.PathEscape(conversationID)+"/interrupt", nil)
		if err != nil || status != http.StatusOK && status != http.StatusNoContent && status != http.StatusConflict {
			return application.ExternalExecutionObservation{}, ErrProtocol
		}
		if refreshed, refreshedStatus, refreshErr := client.getConversation(ctx, conversationID); refreshErr == nil && refreshedStatus == http.StatusOK {
			info = refreshed
		}
		if refreshed, refreshErr := client.events(ctx, conversationID); refreshErr == nil {
			events = refreshed
		}
	}
	observation, err := client.observation(ctx, brief, requestDigest, info, events, true)
	if err != nil {
		return application.ExternalExecutionObservation{}, err
	}
	output, err := json.Marshal(struct {
		Reason  string `json:"reason"`
		Command string `json:"command"`
	}{reason, command})
	if err != nil {
		return application.ExternalExecutionObservation{}, err
	}
	observation.State = application.ExternalFailed
	observation.Retryable = retryable
	observation.Output = output
	return observation, nil
}

func (client *Client) correctDeterministicValidationViolation(ctx context.Context, brief application.ExecutionBrief, requestDigest kernel.Digest, info conversationInfo, events []rawEvent, violation rawEvent, repeated bool) (application.ExternalExecutionObservation, error) {
	if repeated {
		return client.failForExecutionPolicyViolation(ctx, brief, requestDigest, info, events, "REPEATED_DETERMINISTIC_VALIDATION_NO_PROGRESS", describeAction(violation), true)
	}
	conversationID := string(brief.InvocationID)
	if executionStillActive(info.ExecutionStatus) {
		status, _, err := client.request(ctx, http.MethodPost, "/api/conversations/"+url.PathEscape(conversationID)+"/interrupt", nil)
		if err != nil || status != http.StatusOK && status != http.StatusNoContent && status != http.StatusConflict {
			return application.ExternalExecutionObservation{}, ErrProtocol
		}
	}
	if refreshed, refreshedStatus, err := client.getConversation(ctx, conversationID); err == nil && refreshedStatus == http.StatusOK {
		info = refreshed
	}
	if refreshed, err := client.events(ctx, conversationID); err == nil {
		events = refreshed
	}
	for index := eventIndexByID(events, violation.ID) + 1; index > 0 && index < len(events); index++ {
		if events[index].Kind == "MessageEvent" && events[index].Source == "user" && strings.HasPrefix(events[index].Text, deterministicValidationCorrectionPrefix) {
			return client.observation(ctx, brief, requestDigest, info, events, false)
		}
	}
	correction := deterministicValidationCorrectionPrefix + violation.ID + "\nAn equivalent deterministic validation has already succeeded twice without an intervening repository change or compaction boundary. Reuse that evidence. Run only a materially different check still required by the acceptance criteria; otherwise call the finish tool exactly once with the result required by result_protocol."
	status, _, err := client.request(ctx, http.MethodPost, "/api/conversations/"+url.PathEscape(conversationID)+"/events", map[string]any{
		"role": "user", "run": true,
		"content": []map[string]any{{"type": "text", "text": correction}},
	})
	if err != nil || status != http.StatusOK {
		return application.ExternalExecutionObservation{}, ErrProtocol
	}
	if refreshed, refreshedStatus, refreshErr := client.getConversation(ctx, conversationID); refreshErr == nil && refreshedStatus == http.StatusOK {
		info = refreshed
	}
	if refreshed, refreshErr := client.events(ctx, conversationID); refreshErr == nil {
		events = refreshed
	}
	return client.observation(ctx, brief, requestDigest, info, events, false)
}

func (client *Client) correctShellDisciplineViolation(ctx context.Context, brief application.ExecutionBrief, requestDigest kernel.Digest, info conversationInfo, events []rawEvent, promptIndex int, violation rawEvent) (application.ExternalExecutionObservation, error) {
	if !shellDisciplineCorrectionAllowed(events, promptIndex, violation) {
		return client.failForExecutionPolicyViolation(ctx, brief, requestDigest, info, events, "REPEATED_SHELL_DISCIPLINE_VIOLATION", strings.TrimSpace(violation.ActionCommand), true)
	}
	conversationID := string(brief.InvocationID)
	if executionStillActive(info.ExecutionStatus) {
		status, _, err := client.request(ctx, http.MethodPost, "/api/conversations/"+url.PathEscape(conversationID)+"/interrupt", nil)
		if err != nil || status != http.StatusOK && status != http.StatusNoContent && status != http.StatusConflict {
			return application.ExternalExecutionObservation{}, ErrProtocol
		}
	}
	if refreshed, refreshedStatus, err := client.getConversation(ctx, conversationID); err == nil && refreshedStatus == http.StatusOK {
		info = refreshed
	}
	if refreshed, err := client.events(ctx, conversationID); err == nil {
		events = refreshed
	}
	if shellDisciplineViolationCorrected(events, eventIndexByID(events, violation.ID)) {
		return client.observation(ctx, brief, requestDigest, info, events, false)
	}
	correction := shellDisciplineCorrectionPrefix + violation.ID + "\nThe previous terminal action violated the workspace shell rules. Continue this same task, but issue exactly one command in each terminal action. The terminal already uses the authorized workspace: do not use cd, git -C, make -C, pipes, semicolons, &&, command substitution, environment-variable expansion, or embedded newlines. If AGENTS.md has not yet been read successfully, read it before any other repository action. Split discovery and file inspection into separate actions."
	status, _, err := client.request(ctx, http.MethodPost, "/api/conversations/"+url.PathEscape(conversationID)+"/events", map[string]any{
		"role": "user", "run": true,
		"content": []map[string]any{{"type": "text", "text": correction}},
	})
	if err != nil || status != http.StatusOK {
		return application.ExternalExecutionObservation{}, ErrProtocol
	}
	if refreshed, refreshedStatus, refreshErr := client.getConversation(ctx, conversationID); refreshErr == nil && refreshedStatus == http.StatusOK {
		info = refreshed
	}
	if refreshed, refreshErr := client.events(ctx, conversationID); refreshErr == nil {
		events = refreshed
	}
	return client.observation(ctx, brief, requestDigest, info, events, false)
}

func (client *Client) correctRepositoryProgressViolation(ctx context.Context, brief application.ExecutionBrief, requestDigest kernel.Digest, info conversationInfo, events []rawEvent, promptIndex int, violation rawEvent) (application.ExternalExecutionObservation, error) {
	if !repositoryProgressCorrectionAllowed(events, promptIndex, violation) {
		return client.failForExecutionPolicyViolation(ctx, brief, requestDigest, info, events, "REPEATED_CAPABILITY_MISMATCH_REPOSITORY_NO_PROGRESS", describeAction(violation), false)
	}
	conversationID := string(brief.InvocationID)
	if executionStillActive(info.ExecutionStatus) {
		status, _, err := client.request(ctx, http.MethodPost, "/api/conversations/"+url.PathEscape(conversationID)+"/interrupt", nil)
		if err != nil || status != http.StatusOK && status != http.StatusNoContent && status != http.StatusConflict {
			return application.ExternalExecutionObservation{}, ErrProtocol
		}
	}
	if refreshed, refreshedStatus, err := client.getConversation(ctx, conversationID); err == nil && refreshedStatus == http.StatusOK {
		info = refreshed
	}
	if refreshed, err := client.events(ctx, conversationID); err == nil {
		events = refreshed
	}
	if repositoryProgressViolationCorrected(events, eventIndexByID(events, violation.ID)) {
		return client.observation(ctx, brief, requestDigest, info, events, false)
	}
	correction := repositoryProgressCorrectionPrefix + violation.ID + "\nThe most recent repository action exactly repeated an earlier action and returned the same result within the current uninterrupted work period. Continue from that result. Choose the next action required by your assigned role; do not repeat the same action unless repository state or its inputs change."
	status, _, err := client.request(ctx, http.MethodPost, "/api/conversations/"+url.PathEscape(conversationID)+"/events", map[string]any{
		"role": "user", "run": true,
		"content": []map[string]any{{"type": "text", "text": correction}},
	})
	if err != nil || status != http.StatusOK {
		return application.ExternalExecutionObservation{}, ErrProtocol
	}
	if refreshed, refreshedStatus, refreshErr := client.getConversation(ctx, conversationID); refreshErr == nil && refreshedStatus == http.StatusOK {
		info = refreshed
	}
	if refreshed, refreshErr := client.events(ctx, conversationID); refreshErr == nil {
		events = refreshed
	}
	return client.observation(ctx, brief, requestDigest, info, events, false)
}

// correctWorkPurposeMutationViolation gives a non-writing purpose one
// correction after a repository mutation actually took effect. A second
// effective mutation fences the invocation: the verifier's own result can no
// longer be trusted.
func (client *Client) correctWorkPurposeMutationViolation(ctx context.Context, brief application.ExecutionBrief, requestDigest kernel.Digest, info conversationInfo, events []rawEvent, violation rawEvent) (application.ExternalExecutionObservation, error) {
	conversationID := string(brief.InvocationID)
	if executionStillActive(info.ExecutionStatus) {
		status, _, err := client.request(ctx, http.MethodPost, "/api/conversations/"+url.PathEscape(conversationID)+"/interrupt", nil)
		if err != nil || status != http.StatusOK && status != http.StatusNoContent && status != http.StatusConflict {
			return application.ExternalExecutionObservation{}, ErrProtocol
		}
	}
	if refreshed, refreshedStatus, err := client.getConversation(ctx, conversationID); err == nil && refreshedStatus == http.StatusOK {
		info = refreshed
	}
	if refreshed, err := client.events(ctx, conversationID); err == nil {
		events = refreshed
	}
	if index := eventIndexByID(events, violation.ID); index >= 0 && workPurposeMutationCorrected(events, index) {
		return client.observation(ctx, brief, requestDigest, info, events, false)
	}
	correction := workPurposeMutationCorrectionPrefix + violation.ID + "\nThe previous action modified the repository, which this task's purpose does not authorize. The change stands; do not modify the repository further and do not attempt to undo it. Complete the assigned evaluation using the repository as it now is, and disclose the modification in your result."
	status, _, err := client.request(ctx, http.MethodPost, "/api/conversations/"+url.PathEscape(conversationID)+"/events", map[string]any{
		"role": "user", "run": true,
		"content": []map[string]any{{"type": "text", "text": correction}},
	})
	if err != nil || status != http.StatusOK {
		return application.ExternalExecutionObservation{}, ErrProtocol
	}
	if refreshed, refreshedStatus, refreshErr := client.getConversation(ctx, conversationID); refreshErr == nil && refreshedStatus == http.StatusOK {
		info = refreshed
	}
	if refreshed, refreshErr := client.events(ctx, conversationID); refreshErr == nil {
		events = refreshed
	}
	return client.observation(ctx, brief, requestDigest, info, events, false)
}

func (client *Client) correctCheckpointCompletionViolation(ctx context.Context, brief application.ExecutionBrief, requestDigest kernel.Digest, info conversationInfo, events []rawEvent, violation rawEvent, repeated bool) (application.ExternalExecutionObservation, error) {
	if repeated {
		return client.failForExecutionPolicyViolation(ctx, brief, requestDigest, info, events, "REPEATED_CHECKPOINT_COMPLETION_VIOLATION", describeAction(violation), false)
	}
	conversationID := string(brief.InvocationID)
	if executionStillActive(info.ExecutionStatus) {
		status, _, err := client.request(ctx, http.MethodPost, "/api/conversations/"+url.PathEscape(conversationID)+"/interrupt", nil)
		if err != nil || status != http.StatusOK && status != http.StatusNoContent && status != http.StatusConflict {
			return application.ExternalExecutionObservation{}, ErrProtocol
		}
	}
	if refreshed, refreshedStatus, err := client.getConversation(ctx, conversationID); err == nil && refreshedStatus == http.StatusOK {
		info = refreshed
	}
	if refreshed, err := client.events(ctx, conversationID); err == nil {
		events = refreshed
	}
	for index := eventIndexByID(events, violation.ID) + 1; index > 0 && index < len(events); index++ {
		if events[index].Kind == "MessageEvent" && events[index].Source == "user" && strings.HasPrefix(events[index].Text, checkpointCompletionCorrectionPrefix) {
			return client.observation(ctx, brief, requestDigest, info, events, false)
		}
	}
	correction := checkpointCompletionCorrectionPrefix + violation.ID + "\nThe retained progress checkpoint has completed the repository-evidence phase and the bounded last-mile read allowance is exhausted. Do not read or modify the repository further. Evaluate the retained evidence and call the finish tool exactly once with the result required by result_protocol."
	status, _, err := client.request(ctx, http.MethodPost, "/api/conversations/"+url.PathEscape(conversationID)+"/events", map[string]any{
		"role": "user", "run": true,
		"content": []map[string]any{{"type": "text", "text": correction}},
	})
	if err != nil || status != http.StatusOK {
		return application.ExternalExecutionObservation{}, ErrProtocol
	}
	if refreshed, refreshedStatus, refreshErr := client.getConversation(ctx, conversationID); refreshErr == nil && refreshedStatus == http.StatusOK {
		info = refreshed
	}
	if refreshed, refreshErr := client.events(ctx, conversationID); refreshErr == nil {
		events = refreshed
	}
	return client.observation(ctx, brief, requestDigest, info, events, false)
}

func (client *Client) correctEditableCandidateCompletion(ctx context.Context, brief application.ExecutionBrief, requestDigest kernel.Digest, info conversationInfo, events []rawEvent, promptIndex int, reason string) (application.ExternalExecutionObservation, error) {
	for index, event := range events {
		if index > promptIndex && event.Kind == "MessageEvent" && event.Source == "user" && strings.HasPrefix(event.Text, editableCandidateCompletionCorrectionPrefix) {
			return client.failForExecutionPolicyViolation(ctx, brief, requestDigest, info, events, "EDITABLE_CANDIDATE_NOT_COMMITTED", reason, true)
		}
	}
	conversationID := string(brief.InvocationID)
	correction := editableCandidateCompletionCorrectionPrefix + reason + "\nThe implementation cannot be handed to its independent validator until Git contains an immutable candidate. Continue this same task without repeating completed discovery or implementation. Inspect Git status, stage only the intended source and test files, never stage the Teams-injected .openhands runtime hook, commit the intended candidate on the assigned branch, verify the workspace is clean apart from that injected hook, and then call finish exactly once."
	status, _, err := client.request(ctx, http.MethodPost, "/api/conversations/"+url.PathEscape(conversationID)+"/events", map[string]any{
		"role": "user", "run": true,
		"content": []map[string]any{{"type": "text", "text": correction}},
	})
	if err != nil || status != http.StatusOK {
		return application.ExternalExecutionObservation{}, ErrProtocol
	}
	if refreshed, refreshedStatus, refreshErr := client.getConversation(ctx, conversationID); refreshErr == nil && refreshedStatus == http.StatusOK {
		info = refreshed
	}
	if refreshed, refreshErr := client.events(ctx, conversationID); refreshErr == nil {
		events = refreshed
	}
	return client.observation(ctx, brief, requestDigest, info, events, false)
}

func (client *Client) correctRepositoryGroundingViolation(ctx context.Context, brief application.ExecutionBrief, requestDigest kernel.Digest, info conversationInfo, events []rawEvent, promptIndex int, violation rawEvent) (application.ExternalExecutionObservation, error) {
	if repositoryGroundingCorrectionCount(events, promptIndex) > 0 {
		return client.failForExecutionPolicyViolation(ctx, brief, requestDigest, info, events, "REPEATED_REPOSITORY_ACTION_BEFORE_AGENTS_GROUNDING", describeAction(violation), false)
	}
	conversationID := string(brief.InvocationID)
	if executionStillActive(info.ExecutionStatus) {
		status, _, err := client.request(ctx, http.MethodPost, "/api/conversations/"+url.PathEscape(conversationID)+"/interrupt", nil)
		if err != nil || status != http.StatusOK && status != http.StatusNoContent && status != http.StatusConflict {
			return application.ExternalExecutionObservation{}, ErrProtocol
		}
	}
	if refreshed, refreshedStatus, err := client.getConversation(ctx, conversationID); err == nil && refreshedStatus == http.StatusOK {
		info = refreshed
	}
	if refreshed, err := client.events(ctx, conversationID); err == nil {
		events = refreshed
	}
	if repositoryGroundingCorrectionCount(events, promptIndex) > 0 {
		return client.observation(ctx, brief, requestDigest, info, events, false)
	}
	correction := repositoryGroundingCorrectionPrefix + violation.ID + "\nA repository action was issued before the AGENTS.md result was available. Continue this same task under AGENTS.md. If that read succeeded, do not repeat it; otherwise read AGENTS.md successfully before any other repository action. Do not repeat the premature action solely to recover it."
	status, _, err := client.request(ctx, http.MethodPost, "/api/conversations/"+url.PathEscape(conversationID)+"/events", map[string]any{
		"role": "user", "run": true,
		"content": []map[string]any{{"type": "text", "text": correction}},
	})
	if err != nil || status != http.StatusOK {
		return application.ExternalExecutionObservation{}, ErrProtocol
	}
	if refreshed, refreshedStatus, refreshErr := client.getConversation(ctx, conversationID); refreshErr == nil && refreshedStatus == http.StatusOK {
		info = refreshed
	}
	if refreshed, refreshErr := client.events(ctx, conversationID); refreshErr == nil {
		events = refreshed
	}
	return client.observation(ctx, brief, requestDigest, info, events, false)
}

func (client *Client) correctRepositoryScopeViolation(ctx context.Context, brief application.ExecutionBrief, requestDigest kernel.Digest, info conversationInfo, events []rawEvent, promptIndex int, violation rawEvent) (application.ExternalExecutionObservation, error) {
	if repositoryScopeCorrectionCount(events, promptIndex) > 0 {
		return client.failForExecutionPolicyViolation(ctx, brief, requestDigest, info, events, "REPEATED_ACCEPTED_CONTRACT_SCOPE_VIOLATION", describeAction(violation), false)
	}
	conversationID := string(brief.InvocationID)
	if executionStillActive(info.ExecutionStatus) {
		status, _, err := client.request(ctx, http.MethodPost, "/api/conversations/"+url.PathEscape(conversationID)+"/interrupt", nil)
		if err != nil || status != http.StatusOK && status != http.StatusNoContent && status != http.StatusConflict {
			return application.ExternalExecutionObservation{}, ErrProtocol
		}
	}
	if refreshed, refreshedStatus, err := client.getConversation(ctx, conversationID); err == nil && refreshedStatus == http.StatusOK {
		info = refreshed
	}
	if refreshed, err := client.events(ctx, conversationID); err == nil {
		events = refreshed
	}
	if repositoryScopeViolationCorrected(events, eventIndexByID(events, violation.ID)) {
		return client.observation(ctx, brief, requestDigest, info, events, false)
	}
	correction := repositoryScopeCorrectionPrefix + violation.ID + "\nAccepted CONTRACTS packages are immutable evidence and outside this work item. Continue the assigned work without modifying CONTRACTS or OUTPUT."
	status, _, err := client.request(ctx, http.MethodPost, "/api/conversations/"+url.PathEscape(conversationID)+"/events", map[string]any{
		"role": "user", "run": true,
		"content": []map[string]any{{"type": "text", "text": correction}},
	})
	if err != nil || status != http.StatusOK {
		return application.ExternalExecutionObservation{}, ErrProtocol
	}
	if refreshed, refreshedStatus, refreshErr := client.getConversation(ctx, conversationID); refreshErr == nil && refreshedStatus == http.StatusOK {
		info = refreshed
	}
	if refreshed, refreshErr := client.events(ctx, conversationID); refreshErr == nil {
		events = refreshed
	}
	return client.observation(ctx, brief, requestDigest, info, events, false)
}

func describeAction(event rawEvent) string {
	command := strings.TrimSpace(event.ActionCommand)
	if command == "" && len(event.ActionPayload) > 0 {
		command = string(event.ActionPayload)
	}
	if event.ActionPath == "" {
		return command
	}
	if strings.Contains(command, event.ActionPath) {
		return command
	}
	return strings.TrimSpace(command + " " + event.ActionPath)
}

// An evidence-bound operator recovery supersedes the prior work profile and
// starts in a clean conversation, so it receives the normal bounded execution
// window. Automatic retries retain predecessor history and receive only the
// smaller additional allowance.
func explicitRecoveryProfile(brief application.ExecutionBrief) bool {
	return brief.RetryOrdinal > 0 && brief.WorkProfile.SupersedesProfileID != nil
}

type progressCheckpoint struct {
	SchemaVersion          string                       `json:"schema_version"`
	InvocationID           kernel.UUIDv7                `json:"invocation_id"`
	PriorInvocationID      *kernel.UUIDv7               `json:"prior_invocation_id,omitempty"`
	AuthoritativeExecution checkpointExecutionAuthority `json:"authoritative_execution"`
	Source                 string                       `json:"source"`
	SourceEventCount       int                          `json:"source_event_count"`
	SourceJournalSHA256    kernel.Digest                `json:"source_journal_sha256"`
	Actions                []checkpointAction           `json:"actions"`
	RepositoryEvidence     []checkpointAction           `json:"repository_evidence,omitempty"`
	InspectedPaths         []string                     `json:"inspected_paths"`
	ChangedPaths           []string                     `json:"changed_paths"`
	Validations            []checkpointAction           `json:"validations"`
	NextAction             string                       `json:"next_action"`
	ContinuationRule       string                       `json:"continuation_rule"`
}

const (
	maximumCheckpointActions            = 12
	maximumCheckpointRepositoryEvidence = 12
	maximumCheckpointValidations        = 24
)

// checkpointExecutionAuthority repeats the minimum canonical Teams state that
// the model needs after OpenHands removes older messages. It is derived only
// from the already authenticated execution brief; the model and condenser
// cannot alter it.
type checkpointExecutionAuthority struct {
	ExecutionBriefSHA256 kernel.Digest                          `json:"execution_brief_sha256"`
	AuthorizationEventID kernel.UUIDv7                          `json:"authorization_event_id"`
	Task                 application.TaskExecutionSpecification `json:"task"`
	TaskRevision         uint64                                 `json:"task_revision"`
	LifecycleEpoch       uint64                                 `json:"lifecycle_epoch"`
	ScopeRevision        uint64                                 `json:"scope_revision"`
	Purpose              kernel.WorkPurpose                     `json:"purpose"`
	ActorFQN             kernel.ActorFQN                        `json:"actor_fqn"`
	RoleGrounding        application.RoleExecutionGrounding     `json:"role_grounding"`
	Scope                kernel.TaskOperationalScope            `json:"scope"`
	CoordinationRule     string                                 `json:"coordination_rule"`
	ExecutionGuidance    []string                               `json:"execution_guidance"`
	ResultProtocol       *application.ExecutionResultProtocol   `json:"result_protocol,omitempty"`
}

type checkpointAction struct {
	EventID            string        `json:"event_id"`
	Tool               string        `json:"tool"`
	Command            string        `json:"command"`
	Path               string        `json:"path,omitempty"`
	Outcome            string        `json:"outcome"`
	ObservationSHA256  kernel.Digest `json:"observation_sha256,omitempty"`
	ObservationExcerpt string        `json:"observation_excerpt,omitempty"`
}

func (client *Client) recoveryCheckpoint(ctx context.Context, brief application.ExecutionBrief, workspace WorkspaceBinding) (progressCheckpoint, error) {
	if brief.RetryOfInvocationID == nil {
		return progressCheckpoint{}, ErrProtocol
	}
	priorID := string(*brief.RetryOfInvocationID)
	info, status, err := client.getConversation(ctx, priorID)
	if err == nil && status == http.StatusNotFound {
		return progressCheckpoint{}, errRecoveryCheckpointUnavailable
	}
	if err != nil || status != http.StatusOK || info.ID != priorID || info.Workspace.Kind != "LocalWorkspace" || !recoveryWorkspaceMatches(info.Workspace.WorkingDir, workspace, brief.Task.TaskID) || info.Tags["tekrooinvocation"] != priorID || !kernel.Digest(info.Tags["tekroorequest"]).Valid() || executionStillActive(info.ExecutionStatus) {
		return progressCheckpoint{}, errors.Join(ErrProtocol, err)
	}
	events, err := client.events(ctx, priorID)
	if err != nil {
		return progressCheckpoint{}, err
	}
	checkpoint := buildProgressCheckpoint(brief, events, -1)
	checkpoint.PriorInvocationID = brief.RetryOfInvocationID
	return checkpoint, nil
}

func recoveryWorkspaceMatches(priorPath string, current WorkspaceBinding, taskID kernel.UUIDv7) bool {
	priorPath = filepath.Clean(priorPath)
	currentPath := filepath.Clean(current.WorkingDirectory)
	if !filepath.IsAbs(priorPath) || !filepath.IsAbs(currentPath) {
		return false
	}
	if priorPath == currentPath {
		return true
	}
	if current.Candidate == nil || !taskID.Valid() || filepath.Dir(priorPath) != filepath.Dir(currentPath) {
		return false
	}
	if filepath.Base(currentPath) != current.Candidate.CandidateID+"-"+string(taskID) {
		return false
	}
	priorBase := filepath.Base(priorPath)
	if len(priorBase) != 73 || priorBase[36] != '-' || priorBase[37:] != string(taskID) {
		return false
	}
	return kernel.UUIDv7(priorBase[:36]).Valid()
}

func buildProgressCheckpoint(brief application.ExecutionBrief, events []rawEvent, afterIndex int) progressCheckpoint {
	type pendingAction struct {
		toolCallID string
		event      rawEvent
		index      int
	}
	pendingByTool := make(map[string][]pendingAction)
	actions := make([]checkpointAction, 0)
	repositoryEvidence := make([]checkpointAction, 0)
	validations := make([]checkpointAction, 0)
	inspected := make(map[string]struct{})
	changed := make(map[string]struct{})
	journal := sha256.New()
	sourceEventCount := len(events) - afterIndex - 1
	retainPrior := func(retained progressCheckpoint) {
		sourceEventCount += retained.SourceEventCount
		journal.Write([]byte(retained.SourceJournalSHA256))
		for _, path := range retained.InspectedPaths {
			inspected[path] = struct{}{}
		}
		for _, path := range retained.ChangedPaths {
			changed[path] = struct{}{}
		}
		for _, validation := range retained.Validations {
			if validation.Outcome == "SUCCEEDED" {
				validations = retainCheckpointAction(validations, validation, maximumCheckpointValidations)
			}
		}
		for _, evidence := range retained.RepositoryEvidence {
			if evidence.Outcome == "SUCCEEDED" {
				repositoryEvidence = retainCheckpointAction(repositoryEvidence, evidence, maximumCheckpointRepositoryEvidence)
			}
		}
		// Version 1.1 did not have repository_evidence. Its Actions are
		// nevertheless raw, digest-bound observations, so a successor can
		// recover useful source evidence from the immediately prior slice.
		for _, action := range retained.Actions {
			if action.Outcome == "SUCCEEDED" && checkpointActionIsRepositoryEvidence(action) {
				repositoryEvidence = retainCheckpointAction(repositoryEvidence, action, maximumCheckpointRepositoryEvidence)
			}
		}
	}
	if afterIndex < 0 {
		for _, event := range events {
			if retained, found := recoveryCheckpointFromExecutionPrompt(event); found {
				retainPrior(retained)
				break
			}
		}
	}
	if afterIndex >= 0 {
		if retained, found := progressCheckpointFromEvent(events[afterIndex]); found {
			retainPrior(retained)
		}
	}
	for index, event := range events {
		if index <= afterIndex {
			continue
		}
		journal.Write(event.Raw)
		if event.Kind == "ActionEvent" && event.Source == "agent" && repositoryAction(event) {
			pendingByTool[event.ToolName] = append(pendingByTool[event.ToolName], pendingAction{toolCallID: event.ToolCallID, event: event, index: len(actions)})
			actions = append(actions, checkpointAction{EventID: event.ID, Tool: event.ToolName, Command: truncateRunes(describeAction(event), 500), Path: truncateRunes(event.ActionPath, 1000), Outcome: "PENDING"})
			continue
		}
		if event.Kind != "ObservationEvent" || len(pendingByTool[event.ToolName]) == 0 {
			continue
		}
		pendingIndex := 0
		if event.ToolCallID != "" {
			pendingIndex = slices.IndexFunc(pendingByTool[event.ToolName], func(pending pendingAction) bool {
				return pending.toolCallID == event.ToolCallID
			})
			if pendingIndex < 0 {
				continue
			}
		}
		pending := pendingByTool[event.ToolName][pendingIndex]
		pendingByTool[event.ToolName] = slices.Delete(pendingByTool[event.ToolName], pendingIndex, pendingIndex+1)
		outcome := "SUCCEEDED"
		if event.ObservationTimeout {
			outcome = "TIMED_OUT"
		} else if event.ObservationError || event.ObservationExitCode != nil && *event.ObservationExitCode != 0 {
			outcome = "FAILED"
		}
		digest := sha256.Sum256(event.Raw)
		actions[pending.index].Outcome = outcome
		actions[pending.index].ObservationSHA256 = kernel.Digest(hex.EncodeToString(digest[:]))
		actions[pending.index].ObservationExcerpt = truncateRunes(strings.TrimSpace(event.Text), 1600)
		if outcome == "SUCCEEDED" && repositoryContentReadAction(pending.event) && pending.event.ActionPath != "" {
			inspected[pending.event.ActionPath] = struct{}{}
		}
		if outcome == "SUCCEEDED" && checkpointRepositoryEvidenceAction(pending.event) {
			repositoryEvidence = retainCheckpointAction(repositoryEvidence, actions[pending.index], maximumCheckpointRepositoryEvidence)
		}
		if outcome == "SUCCEEDED" && mutationAction(pending.event) && pending.event.ActionPath != "" {
			changed[pending.event.ActionPath] = struct{}{}
		}
		if deterministicValidationAction(pending.event) {
			validations = retainCheckpointAction(validations, actions[pending.index], maximumCheckpointValidations)
		}
	}
	actions = compactCheckpointActions(actions, maximumCheckpointActions)
	return progressCheckpoint{
		SchemaVersion:          "tekroo.teams.execution-progress-checkpoint/1.2.0",
		InvocationID:           brief.InvocationID,
		AuthoritativeExecution: checkpointAuthority(brief),
		Source:                 "OPENHANDS_EVENT_JOURNAL",
		SourceEventCount:       sourceEventCount,
		SourceJournalSHA256:    kernel.Digest(hex.EncodeToString(journal.Sum(nil))),
		Actions:                actions,
		RepositoryEvidence:     repositoryEvidence,
		InspectedPaths:         sortedKeys(inspected),
		ChangedPaths:           sortedKeys(changed),
		Validations:            validations,
		NextAction:             checkpointNextAction(brief, actions, changed, validations),
		ContinuationRule:       "Treat authoritative_execution as canonical. Perform next_action first. A focused reread of retained pre-checkpoint evidence is allowed when compression removed a needed detail; never repeat an action already performed after this checkpoint unless repository state or its inputs changed.",
	}
}

func compactCheckpointActions(actions []checkpointAction, maximum int) []checkpointAction {
	if maximum <= 0 || len(actions) <= maximum {
		return actions
	}
	return append([]checkpointAction(nil), actions[len(actions)-maximum:]...)
}

func recoveryCheckpointFromExecutionPrompt(event rawEvent) (progressCheckpoint, bool) {
	if event.Kind != "MessageEvent" || event.Source != "user" {
		return progressCheckpoint{}, false
	}
	var input struct {
		RecoveryCheckpoint *progressCheckpoint `json:"recovery_checkpoint"`
	}
	if json.Unmarshal([]byte(event.Text), &input) != nil || input.RecoveryCheckpoint == nil {
		return progressCheckpoint{}, false
	}
	checkpoint := *input.RecoveryCheckpoint
	if checkpoint.SchemaVersion != "tekroo.teams.execution-progress-checkpoint/1.2.0" || checkpoint.Source != "OPENHANDS_EVENT_JOURNAL" || !checkpoint.SourceJournalSHA256.Valid() || !checkpoint.InvocationID.Valid() || checkpoint.PriorInvocationID == nil || !checkpoint.PriorInvocationID.Valid() {
		return progressCheckpoint{}, false
	}
	return checkpoint, true
}

func progressCheckpointFromEvent(event rawEvent) (progressCheckpoint, bool) {
	if event.Kind != "MessageEvent" || event.Source != "user" || !strings.HasPrefix(event.Text, compactionCheckpointPrefix) {
		return progressCheckpoint{}, false
	}
	parts := strings.SplitN(event.Text, "\n", 3)
	if len(parts) != 3 {
		return progressCheckpoint{}, false
	}
	var checkpoint progressCheckpoint
	if json.Unmarshal([]byte(parts[2]), &checkpoint) != nil || checkpoint.SchemaVersion != "tekroo.teams.execution-progress-checkpoint/1.1.0" && checkpoint.SchemaVersion != "tekroo.teams.execution-progress-checkpoint/1.2.0" || !checkpoint.SourceJournalSHA256.Valid() {
		return progressCheckpoint{}, false
	}
	return checkpoint, true
}

func retainCheckpointAction(retained []checkpointAction, action checkpointAction, maximum int) []checkpointAction {
	key := action.Tool + "\x00" + action.Command + "\x00" + action.Path
	if index := slices.IndexFunc(retained, func(candidate checkpointAction) bool {
		return candidate.Tool+"\x00"+candidate.Command+"\x00"+candidate.Path == key
	}); index >= 0 {
		retained = slices.Delete(retained, index, index+1)
	}
	retained = append(retained, action)
	if len(retained) > maximum {
		retained = retained[len(retained)-maximum:]
	}
	return retained
}

func checkpointRepositoryEvidenceAction(event rawEvent) bool {
	if repositoryContentReadAction(event) {
		return true
	}
	if event.ToolName != "terminal" {
		return false
	}
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(event.ActionCommand)))
	return len(fields) > 1 && filepath.Base(fields[0]) == "git" && (fields[1] == "diff" || fields[1] == "show")
}

func checkpointActionIsRepositoryEvidence(action checkpointAction) bool {
	switch action.Tool {
	case "repository_view", "repository_search":
		return true
	case "terminal":
		fields := strings.Fields(strings.ToLower(strings.TrimSpace(action.Command)))
		if len(fields) < 2 {
			return false
		}
		switch filepath.Base(fields[0]) {
		case "cat", "head", "tail", "sed", "grep", "rg":
			return true
		case "git":
			return fields[1] == "diff" || fields[1] == "show"
		}
	}
	return false
}

func checkpointActionIsReadOnlyInspection(action checkpointAction) bool {
	switch action.Tool {
	case "repository_view", "repository_search", "glob":
		return true
	case "file_editor":
		return strings.EqualFold(strings.TrimSpace(action.Command), "view")
	case "terminal":
		fields := strings.Fields(strings.ToLower(strings.TrimSpace(action.Command)))
		if len(fields) == 0 {
			return false
		}
		switch filepath.Base(fields[0]) {
		case "cat", "head", "tail", "sed", "grep", "rg", "ls", "find", "pwd", "which":
			return true
		case "git":
			if len(fields) < 2 {
				return false
			}
			return fields[1] == "status" || fields[1] == "log" || fields[1] == "diff" || fields[1] == "show"
		case "go":
			return len(fields) >= 2 && (fields[1] == "env" || fields[1] == "version")
		}
	}
	return false
}

func checkpointAuthority(brief application.ExecutionBrief) checkpointExecutionAuthority {
	encoded, _ := json.Marshal(brief)
	digest := sha256.Sum256(encoded)
	return checkpointExecutionAuthority{
		ExecutionBriefSHA256: kernel.Digest(hex.EncodeToString(digest[:])),
		AuthorizationEventID: brief.AuthorizationEventID,
		Task:                 brief.Task,
		TaskRevision:         brief.TaskRevision,
		LifecycleEpoch:       brief.LifecycleEpoch,
		ScopeRevision:        brief.ScopeRevision,
		Purpose:              brief.Purpose,
		ActorFQN:             brief.ActorFQN,
		RoleGrounding:        brief.RoleGrounding,
		Scope:                brief.Scope,
		CoordinationRule:     brief.CoordinationRule,
		ExecutionGuidance:    append([]string(nil), brief.ExecutionGuidance...),
		ResultProtocol:       brief.ResultProtocol,
	}
}

func checkpointNextAction(brief application.ExecutionBrief, actions []checkpointAction, changed map[string]struct{}, validations []checkpointAction) string {
	submit := "submit the required result through the finish tool"
	if brief.ResultProtocol != nil && brief.ResultProtocol.Marker != "" {
		submit = "submit the required " + brief.ResultProtocol.Marker + " result through the finish tool"
	}
	switch brief.Purpose {
	case kernel.PurposeReplan, kernel.PurposeInvestigation, kernel.PurposeReview, kernel.PurposeHandoff, kernel.PurposeEscalation:
		return "Use the retained successful repository evidence to complete the authoritative task now and " + submit + "; do not retry failed broad discovery. Reread retained pre-checkpoint evidence only when one exact detail is needed."
	}
	for index := len(actions) - 1; index >= 0; index-- {
		switch actions[index].Outcome {
		case "FAILED", "TIMED_OUT", "PENDING":
			if checkpointActionIsReadOnlyInspection(actions[index]) {
				continue
			}
			return fmt.Sprintf("Resolve the retained %s %s action before continuing; do not repeat any unrelated successful discovery.", strings.ToLower(actions[index].Outcome), actions[index].Tool)
		}
	}
	for index := len(validations) - 1; index >= 0; index-- {
		if validations[index].Outcome != "SUCCEEDED" {
			return "Repair the latest failed validation, then rerun only that affected validation."
		}
	}
	switch brief.Purpose {
	case kernel.PurposeImplementation, kernel.PurposeRepair:
		if len(changed) == 0 {
			return "Use the retained repository evidence to implement the first unmet acceptance criterion now; do not repeat broad discovery."
		}
		if len(validations) == 0 {
			return "Run the narrowest deterministic validation that covers the retained changes."
		}
		return "Compare the validated changes with every authoritative acceptance criterion and " + submit + "."
	case kernel.PurposeValidation:
		if len(validations) == 0 {
			return "Run the first required deterministic validation against the authoritative candidate."
		}
		return "Evaluate the retained validation evidence and " + submit + "."
	case kernel.PurposePromotion:
		return "Verify the retained candidate identity and promotion preconditions, then " + submit + "."
	default:
		return "Perform the first incomplete requirement in the authoritative task, then " + submit + "."
	}
}

func truncateRunes(value string, maximum int) string {
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	return string(runes[:maximum])
}

func sortedKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}

func missingCompactionCheckpoint(info conversationInfo, events []rawEvent, promptIndex int) int {
	condenserRuns := len(info.Stats.UsageToMetrics["condenser"].TokenUsages)
	if condenserRuns == 0 {
		return 0
	}
	covered := 0
	for index, event := range events {
		if index <= promptIndex || event.Kind != "MessageEvent" || event.Source != "user" {
			continue
		}
		line := strings.SplitN(event.Text, "\n", 2)[0]
		if !strings.HasPrefix(line, compactionCheckpointPrefix) {
			continue
		}
		ordinal, err := strconv.Atoi(strings.TrimPrefix(line, compactionCheckpointPrefix))
		if err == nil && ordinal > covered {
			covered = ordinal
		}
	}
	if condenserRuns > covered {
		return condenserRuns
	}
	return 0
}

func latestCompactionCheckpointIndex(events []rawEvent, promptIndex int) int {
	result := promptIndex
	for index, event := range events {
		if index > promptIndex && event.Kind == "MessageEvent" && event.Source == "user" && strings.HasPrefix(event.Text, compactionCheckpointPrefix) {
			result = index
		}
	}
	return result
}

func (client *Client) injectCompactionCheckpoint(ctx context.Context, brief application.ExecutionBrief, requestDigest kernel.Digest, info conversationInfo, events []rawEvent, promptIndex, ordinal int) (application.ExternalExecutionObservation, error) {
	conversationID := string(brief.InvocationID)
	if executionStillActive(info.ExecutionStatus) {
		status, _, err := client.request(ctx, http.MethodPost, "/api/conversations/"+url.PathEscape(conversationID)+"/interrupt", nil)
		if err != nil || status != http.StatusOK && status != http.StatusNoContent && status != http.StatusConflict {
			return application.ExternalExecutionObservation{}, ErrProtocol
		}
		if refreshed, refreshedStatus, refreshErr := client.getConversation(ctx, conversationID); refreshErr == nil && refreshedStatus == http.StatusOK {
			info = refreshed
		}
		if refreshed, refreshErr := client.events(ctx, conversationID); refreshErr == nil {
			events = refreshed
		}
	}
	if missingCompactionCheckpoint(info, events, promptIndex) == 0 {
		return client.observation(ctx, brief, requestDigest, info, events, false)
	}
	checkpoint := buildProgressCheckpoint(brief, events, latestCompactionCheckpointIndex(events, promptIndex))
	encoded, err := json.Marshal(checkpoint)
	if err != nil {
		return application.ExternalExecutionObservation{}, err
	}
	message := fmt.Sprintf("%s%d\nThe model context was condensed. The JSON below restores canonical Teams execution authority and deterministic evidence from the retained OpenHands tool journal. authoritative_execution, observed actions, outcomes, excerpts, and result digests are authoritative for this invocation. Perform next_action first.\n%s", compactionCheckpointPrefix, ordinal, encoded)
	status, _, err := client.request(ctx, http.MethodPost, "/api/conversations/"+url.PathEscape(conversationID)+"/events", map[string]any{
		"role": "user", "run": true,
		"content": []map[string]any{{"type": "text", "text": message}},
	})
	if err != nil || status != http.StatusOK {
		return application.ExternalExecutionObservation{}, ErrProtocol
	}
	if refreshed, refreshedStatus, refreshErr := client.getConversation(ctx, conversationID); refreshErr == nil && refreshedStatus == http.StatusOK {
		info = refreshed
	}
	if refreshed, refreshErr := client.events(ctx, conversationID); refreshErr == nil {
		events = refreshed
	}
	return client.observation(ctx, brief, requestDigest, info, events, false)
}

func executionStillActive(status string) bool {
	return status == "idle" || status == "running" || status == "waiting_for_confirmation"
}

func pausedAtCompactionBoundary(events []rawEvent, promptIndex int) bool {
	latestCondensation := -1
	latestInterrupt := -1
	for index, event := range events {
		if index <= promptIndex {
			continue
		}
		switch event.Kind {
		case "Condensation":
			latestCondensation = index
		case "InterruptEvent":
			latestInterrupt = index
		}
	}
	return latestCondensation > latestInterrupt
}

func (client *Client) Cancel(ctx context.Context, brief application.ExecutionBrief, conversationID string, requestDigest kernel.Digest) (application.ExternalExecutionObservation, error) {
	if _, err := client.prepare(ctx, brief, requestDigest); err != nil || conversationID != string(brief.InvocationID) {
		return application.ExternalExecutionObservation{}, ErrProtocol
	}
	status, _, err := client.request(ctx, http.MethodPost, "/api/conversations/"+url.PathEscape(conversationID)+"/interrupt", nil)
	if err != nil {
		return application.ExternalExecutionObservation{}, err
	}
	if status != http.StatusOK && status != http.StatusNoContent && status != http.StatusConflict {
		return application.ExternalExecutionObservation{}, ErrProtocol
	}
	info, status, err := client.getConversation(ctx, conversationID)
	if err != nil || status != http.StatusOK {
		return application.ExternalExecutionObservation{}, ErrProtocol
	}
	events, err := client.events(ctx, conversationID)
	if err != nil {
		return application.ExternalExecutionObservation{}, err
	}
	return client.observation(ctx, brief, requestDigest, info, events, true)
}

type preparedExecution struct {
	prompt                    string
	requestDigest             kernel.Digest
	workspace                 WorkspaceBinding
	profile                   ExecutionProfile
	recoveryCheckpointPresent bool
}

func (client *Client) prepare(ctx context.Context, brief application.ExecutionBrief, requestDigest kernel.Digest) (preparedExecution, error) {
	encoded, err := json.Marshal(brief)
	if err != nil {
		return preparedExecution{}, err
	}
	digest := sha256.Sum256(encoded)
	invalidRetry := brief.RetryOfInvocationID == nil && (brief.RetryOrdinal != 0 || brief.RetryOfConversationID != nil) || brief.RetryOfInvocationID != nil && (!brief.RetryOfInvocationID.Valid() || *brief.RetryOfInvocationID == brief.InvocationID || brief.RetryOrdinal == 0 || brief.RetryOfConversationID != nil && *brief.RetryOfConversationID != string(*brief.RetryOfInvocationID))
	if kernel.Digest(hex.EncodeToString(digest[:])) != requestDigest || brief.ContractManifest != kernel.ContractIdentity || brief.CoordinationRule == "" || !brief.RoleGrounding.Valid(brief.ActorFQN) || invalidRetry {
		return preparedExecution{}, ErrProtocol
	}
	workspace, err := client.workspaces.ResolveWorkspace(ctx, brief.Scope)
	if err != nil || workspace.WorkspaceID != brief.Scope.WorkspaceID || workspace.WorktreeID != brief.Scope.WorktreeID || !filepath.IsAbs(workspace.WorkingDirectory) {
		return preparedExecution{}, errors.Join(ErrProtocol, fmt.Errorf("resolve workspace %q/%q: %w", brief.Scope.WorkspaceID, brief.Scope.WorktreeID, err))
	}
	prompt := encoded
	recoveryCheckpointPresent := false
	if workspace.Candidate != nil || explicitRecoveryProfile(brief) {
		var envelope map[string]any
		if json.Unmarshal(encoded, &envelope) != nil {
			return preparedExecution{}, ErrProtocol
		}
		if explicitRecoveryProfile(brief) && brief.RetryOfConversationID != nil {
			checkpoint, checkpointErr := client.recoveryCheckpoint(ctx, brief, workspace)
			if checkpointErr != nil && !errors.Is(checkpointErr, errRecoveryCheckpointUnavailable) {
				return preparedExecution{}, errors.Join(ErrProtocol, checkpointErr)
			}
			if checkpointErr == nil {
				envelope["recovery_checkpoint"] = checkpoint
				recoveryCheckpointPresent = true
			}
		}
		if workspace.Candidate != nil {
			candidateEvidenceBound := false
			for _, evidence := range brief.Evidence {
				if evidence.SHA256 == workspace.Candidate.ReceiptSHA256 {
					candidateEvidenceBound = true
					break
				}
			}
			if !candidateEvidenceBound {
				return preparedExecution{}, ErrProtocol
			}
			envelope["candidate"] = workspace.Candidate
			envelope["candidate_result_requirement"] = candidateResultRequirement{
				CandidateID:            workspace.Candidate.CandidateID,
				CandidateReceiptSHA256: workspace.Candidate.ReceiptSHA256,
				Instruction:            candidateResultRequirementInstruction,
			}
			if protocol, ok := envelope["result_protocol"].(map[string]any); ok {
				protocol["instruction"] = candidateResultProtocolInstruction
			}
		}
		prompt, err = json.Marshal(envelope)
		if err != nil {
			return preparedExecution{}, ErrProtocol
		}
	}
	profile, err := client.profiles.ResolveExecutionProfile(ctx, brief.ModelProfileDigest, brief.RuntimeIdentityDigest, brief.ToolPolicyDigest, brief.EffectPolicyDigest)
	if err != nil || profile.ModelProfileDigest != brief.ModelProfileDigest || profile.RuntimeIdentityDigest != brief.RuntimeIdentityDigest || profile.ToolPolicyDigest != brief.ToolPolicyDigest || profile.EffectPolicyDigest != brief.EffectPolicyDigest || !profile.validFor(brief) || !brief.SemanticContextValid() {
		return preparedExecution{}, errors.Join(ErrProtocol, fmt.Errorf("resolve execution profile: %w", err))
	}
	return preparedExecution{prompt: string(prompt), requestDigest: requestDigest, workspace: workspace, profile: profile, recoveryCheckpointPresent: recoveryCheckpointPresent}, nil
}

func containsDelegationTool(raw json.RawMessage) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return true
	}
	forbidden := map[string]struct{}{"TaskTool": {}, "TaskToolSet": {}, "DelegateTool": {}, "DelegateToolSet": {}, "WorkflowTool": {}, "WorkflowToolSet": {}}
	var visit func(any) bool
	visit = func(current any) bool {
		switch typed := current.(type) {
		case string:
			_, found := forbidden[typed]
			return found
		case []any:
			for _, item := range typed {
				if visit(item) {
					return true
				}
			}
		case map[string]any:
			for _, item := range typed {
				if visit(item) {
					return true
				}
			}
		}
		return false
	}
	return visit(value)
}

func (client *Client) createConversation(ctx context.Context, brief application.ExecutionBrief, prepared preparedExecution) (int, []byte, error) {
	var agentSettings, hookConfig any
	if json.Unmarshal(prepared.profile.AgentSettings, &agentSettings) != nil || json.Unmarshal(prepared.profile.HookConfig, &hookConfig) != nil {
		return 0, nil, ErrProtocol
	}
	payload := map[string]any{
		"conversation_id":        brief.InvocationID,
		"agent_settings":         agentSettings,
		"secrets_encrypted":      true,
		"workspace":              map[string]any{"kind": "LocalWorkspace", "working_dir": prepared.workspace.WorkingDirectory},
		"worktree":               false,
		"max_iterations":         openHandsIterationLimit(prepared.profile.MaxIterations),
		"stuck_detection":        true,
		"autotitle":              false,
		"hook_config":            hookConfig,
		"observability_metadata": map[string]any{"tekroo_invocation_id": brief.InvocationID, "tekroo_request_digest": prepared.requestDigest},
		"observability_tags":     []string{"tekroo-invocation", "tekroo-work-invocation"},
		"tags": map[string]string{
			"tekrooinvocation":        string(brief.InvocationID),
			"tekroorequest":           string(prepared.requestDigest),
			pauseAfterCondensationTag: "true",
		},
	}
	return client.request(ctx, http.MethodPost, "/api/conversations", payload)
}

type conversationInfo struct {
	ID                       string `json:"id"`
	ExecutionStatus          string `json:"execution_status"`
	CreatedAt                string `json:"created_at"`
	UpdatedAt                string `json:"updated_at"`
	ForkedFromConversationID string `json:"forked_from_conversation_id"`
	Workspace                struct {
		Kind       string `json:"kind"`
		WorkingDir string `json:"working_dir"`
	} `json:"workspace"`
	Agent conversationAgentSettings `json:"agent"`
	Tags  map[string]string         `json:"tags"`
	Stats struct {
		UsageToMetrics map[string]struct {
			TokenUsages []json.RawMessage `json:"token_usages"`
		} `json:"usage_to_metrics"`
	} `json:"stats"`
}

type conversationLLMSettings struct {
	Model                 string   `json:"model"`
	ModelCanonicalName    string   `json:"model_canonical_name"`
	BaseURL               string   `json:"base_url"`
	APIMode               string   `json:"api_mode"`
	NativeToolCalling     *bool    `json:"native_tool_calling"`
	ForceStringSerializer *bool    `json:"force_string_serializer"`
	Stream                *bool    `json:"stream"`
	Temperature           *float64 `json:"temperature"`
	MaxOutputTokens       *int     `json:"max_output_tokens"`
	NumRetries            *int     `json:"num_retries"`
	Timeout               *float64 `json:"timeout"`
	UsageID               string   `json:"usage_id"`
	LiteLLMExtraBody      struct {
		ReasoningEffort       string `json:"reasoning_effort"`
		ReasoningBudgetTokens uint32 `json:"reasoning_budget_tokens"`
		ChatTemplateKwargs    struct {
			EnableThinking   *bool `json:"enable_thinking"`
			PreserveThinking *bool `json:"preserve_thinking"`
		} `json:"chat_template_kwargs"`
	} `json:"litellm_extra_body"`
}

type conversationAgentSettings struct {
	Kind                string   `json:"kind"`
	IncludeDefaultTools []string `json:"include_default_tools"`
	SystemPrompt        string   `json:"system_prompt"`
	Tools               []struct {
		Name   string         `json:"name"`
		Params map[string]any `json:"params"`
	} `json:"tools"`
	AgentContext struct {
		SystemMessageSuffix string `json:"system_message_suffix"`
	} `json:"agent_context"`
	LLM       conversationLLMSettings `json:"llm"`
	Condenser struct {
		Kind          string                  `json:"kind"`
		LLM           conversationLLMSettings `json:"llm"`
		MaximumEvents uint32                  `json:"max_size"`
		MaximumTokens uint32                  `json:"max_tokens"`
		KeepFirst     uint32                  `json:"keep_first"`
	} `json:"condenser"`
}

func conversationAgentMatches(info conversationInfo, expectedRaw json.RawMessage) bool {
	var expected conversationAgentSettings
	if json.Unmarshal(expectedRaw, &expected) != nil {
		return false
	}
	// OpenHands materializes the omitted primary usage identifier as "default"
	// in its read model. Treat that server-owned default as equivalent to the
	// absent request field while still rejecting any other usage partition.
	if expected.LLM.UsageID == "" && info.Agent.LLM.UsageID == "default" {
		info.Agent.LLM.UsageID = ""
	}
	if expected.Tools != nil && !reflect.DeepEqual(info.Agent.Tools, expected.Tools) {
		return false
	}
	info.Agent.Tools = nil
	expected.Tools = nil
	return reflect.DeepEqual(info.Agent, expected)
}

func (client *Client) getConversation(ctx context.Context, conversationID string) (conversationInfo, int, error) {
	status, body, err := client.request(ctx, http.MethodGet, "/api/conversations/"+url.PathEscape(conversationID), nil)
	if err != nil || status == http.StatusNotFound {
		return conversationInfo{}, status, err
	}
	var info conversationInfo
	if status != http.StatusOK || json.Unmarshal(body, &info) != nil {
		return conversationInfo{}, status, ErrProtocol
	}
	return info, status, nil
}

type rawEvent struct {
	Raw                  json.RawMessage
	ID                   string
	Kind                 string
	Source               string
	ObservationKind      string
	Timestamp            time.Time
	Text                 string
	Summary              string
	ToolName             string
	ToolCallID           string
	ActionCommand        string
	ActionPayload        json.RawMessage
	ActionPath           string
	ObservationError     bool
	ObservationTimeout   bool
	ObservationExitCode  *int
	ObservationTruncated *bool
}

func unresolvedModelResponsesWithoutActionOrResult(events []rawEvent, promptIndex int) (rawEvent, int) {
	var latest rawEvent
	count := 0
	for index, event := range events {
		if index <= promptIndex || event.Source != "agent" {
			continue
		}
		if event.Kind == "ActionEvent" {
			latest = rawEvent{}
			count = 0
			continue
		}
		if event.Kind == "MessageEvent" && strings.TrimSpace(event.Text) == "" {
			latest = event
			count++
		}
	}
	return latest, count
}

func pendingCondensationRequest(events []rawEvent, promptIndex int) bool {
	pending := false
	for index, event := range events {
		if index <= promptIndex {
			continue
		}
		switch event.Kind {
		case "CondensationRequest":
			pending = true
		case "Condensation":
			pending = false
		}
	}
	return pending
}

func (client *Client) events(ctx context.Context, conversationID string) ([]rawEvent, error) {
	result := make([]rawEvent, 0, 100)
	totalBytes := 0
	pageID := ""
	seenPages := make(map[string]struct{})
	for page := uint32(0); page < client.maximumPages; page++ {
		path := "/api/conversations/" + url.PathEscape(conversationID) + "/events/search?limit=100"
		if pageID != "" {
			path += "&page_id=" + url.QueryEscape(pageID)
		}
		status, body, err := client.request(ctx, http.MethodGet, path, nil)
		if err != nil || status != http.StatusOK {
			return nil, ErrProtocol
		}
		var response struct {
			Items      []json.RawMessage `json:"items"`
			NextPageID string            `json:"next_page_id"`
		}
		if json.Unmarshal(body, &response) != nil {
			return nil, ErrProtocol
		}
		for _, raw := range response.Items {
			totalBytes += len(raw)
			if totalBytes > client.maximumEvidenceBytes {
				return nil, ErrProtocol
			}
			event, err := decodeEvent(raw)
			if err != nil {
				return nil, err
			}
			result = append(result, event)
		}
		if response.NextPageID == "" {
			return result, nil
		}
		if _, duplicate := seenPages[response.NextPageID]; duplicate {
			return nil, ErrProtocol
		}
		seenPages[response.NextPageID] = struct{}{}
		pageID = response.NextPageID
	}
	return nil, ErrProtocol
}

func decodeEvent(raw json.RawMessage) (rawEvent, error) {
	var envelope struct {
		ID         string `json:"id"`
		Kind       string `json:"kind"`
		Source     string `json:"source"`
		Timestamp  string `json:"timestamp"`
		LLMMessage struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"llm_message"`
		Observation struct {
			Kind      string `json:"kind"`
			IsError   bool   `json:"is_error"`
			Timeout   bool   `json:"timeout"`
			ExitCode  *int   `json:"exit_code"`
			Truncated *bool  `json:"truncated"`
			Content   []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"observation"`
		ToolName   string `json:"tool_name"`
		ToolCallID string `json:"tool_call_id"`
		Summary    string `json:"summary"`
		Action     struct {
			Command string `json:"command"`
			Kind    string `json:"kind"`
			Path    string `json:"path"`
		} `json:"action"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.ID == "" || envelope.Kind == "" {
		return rawEvent{}, ErrProtocol
	}
	var actionEnvelope struct {
		Action json.RawMessage `json:"action"`
	}
	if json.Unmarshal(raw, &actionEnvelope) != nil {
		return rawEvent{}, ErrProtocol
	}
	var text strings.Builder
	for _, content := range envelope.LLMMessage.Content {
		if content.Type == "text" {
			text.WriteString(content.Text)
		}
	}
	if envelope.Kind == "ObservationEvent" {
		for _, content := range envelope.Observation.Content {
			if content.Type == "text" {
				text.WriteString(content.Text)
			}
		}
	}
	var timestamp time.Time
	if envelope.Timestamp != "" {
		timestamp, _ = time.Parse(time.RFC3339Nano, envelope.Timestamp)
	}
	return rawEvent{Raw: append(json.RawMessage(nil), raw...), ID: envelope.ID, Kind: envelope.Kind, Source: envelope.Source, ObservationKind: envelope.Observation.Kind, Timestamp: timestamp, Text: text.String(), Summary: envelope.Summary, ToolName: envelope.ToolName, ToolCallID: envelope.ToolCallID, ActionCommand: envelope.Action.Command, ActionPayload: append(json.RawMessage(nil), actionEnvelope.Action...), ActionPath: envelope.Action.Path, ObservationError: envelope.Observation.IsError, ObservationTimeout: envelope.Observation.Timeout, ObservationExitCode: envelope.Observation.ExitCode, ObservationTruncated: envelope.Observation.Truncated}, nil
}

func deterministicValidationAction(event rawEvent) bool {
	if event.ToolName != "terminal" {
		return false
	}
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(event.ActionCommand)))
	if len(fields) < 2 {
		return false
	}
	switch filepath.Base(fields[0]) {
	case "go":
		switch fields[1] {
		case "build", "test", "vet":
			return true
		}
	case "git":
		if fields[1] == "diff" {
			for _, field := range fields[2:] {
				if field == "--check" {
					return true
				}
			}
		}
	}
	return false
}

func mutationAction(event rawEvent) bool {
	command := strings.ToLower(strings.TrimSpace(event.ActionCommand))
	if event.ToolName == "file_editor" {
		return command != "" && command != "view"
	}
	if event.ToolName != "terminal" || command == "" {
		return false
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	executable := filepath.Base(strings.Trim(fields[0], "\"'"))
	if len(fields) > 1 && executable == "git" {
		switch fields[1] {
		case "add", "am", "apply", "bisect", "branch", "checkout", "cherry-pick", "clean", "commit", "merge", "mv", "rebase", "reset", "restore", "revert", "rm", "stash", "switch", "tag", "update-ref":
			return true
		}
	}
	switch executable {
	case "apply_patch", "tee", "touch", "mkdir", "rm", "mv", "cp", "truncate", "patch":
		return true
	case "gofmt":
		if slices.Contains(fields[1:], "-w") {
			return true
		}
	case "go":
		if len(fields) > 1 && fields[1] == "fmt" {
			return true
		}
	case "sed":
		for _, field := range fields[1:] {
			if field == "-i" || strings.HasPrefix(field, "-i.") {
				return true
			}
		}
	case "perl":
		for _, field := range fields[1:] {
			if strings.HasPrefix(field, "-") && strings.Contains(field, "i") {
				return true
			}
		}
	}
	return writesThroughRedirection(command)
}

func writesThroughRedirection(command string) bool {
	singleQuoted, doubleQuoted, escaped := false, false, false
	for index := 0; index < len(command); index++ {
		character := command[index]
		if escaped {
			escaped = false
			continue
		}
		if character == '\\' && !singleQuoted {
			escaped = true
			continue
		}
		if character == '\'' && !doubleQuoted {
			singleQuoted = !singleQuoted
			continue
		}
		if character == '"' && !singleQuoted {
			doubleQuoted = !doubleQuoted
			continue
		}
		if singleQuoted || doubleQuoted || character != '>' {
			continue
		}

		target := index + 1
		if target < len(command) && command[target] == '>' {
			target++
		}
		for target < len(command) && (command[target] == ' ' || command[target] == '\t') {
			target++
		}
		if target < len(command) && command[target] == '&' {
			target++
			digits := target
			for target < len(command) && command[target] >= '0' && command[target] <= '9' {
				target++
			}
			if target > digits {
				index = target - 1
				continue
			}
		}
		end := target
		for end < len(command) && command[end] != ' ' && command[end] != '\t' {
			end++
		}
		path := strings.Trim(command[target:end], "\"'")
		// A redirect cannot mutate the repository when its target is outside
		// it: /dev/null discards and the scratch directories are agent-local.
		// Scratch written inside the repository would dirty the immutable
		// candidate and remains a violation.
		if path == "/dev/null" || strings.HasPrefix(path, "/tmp/") || strings.HasPrefix(path, "/var/tmp/") {
			index = end - 1
			continue
		}
		return true
	}
	return false
}

func (client *Client) observation(ctx context.Context, brief application.ExecutionBrief, requestDigest kernel.Digest, info conversationInfo, events []rawEvent, interrupted bool) (application.ExternalExecutionObservation, error) {
	prepared, err := client.prepare(ctx, brief, requestDigest)
	if err != nil {
		return application.ExternalExecutionObservation{}, err
	}
	index := executionPromptIndex(events, prepared, brief, requestDigest)
	if index < 0 {
		return application.ExternalExecutionObservation{}, ErrProtocol
	}
	return client.observationAt(brief, requestDigest, info, events, index, interrupted)
}

func (client *Client) observationAt(brief application.ExecutionBrief, requestDigest kernel.Digest, info conversationInfo, events []rawEvent, index int, interrupted bool) (application.ExternalExecutionObservation, error) {
	state := application.ExternalRunning
	retryable := false
	switch info.ExecutionStatus {
	case "finished":
		state = application.ExternalSucceeded
	case "error", "stuck":
		state, retryable = application.ExternalFailed, true
	case "paused":
		if interrupted {
			state = application.ExternalCancelled
		}
	case "idle", "running", "waiting_for_confirmation", "deleting":
	default:
		return application.ExternalExecutionObservation{}, ErrProtocol
	}
	journal := make([]json.RawMessage, 0, len(events)-index)
	toolArtifactJournal := make([]json.RawMessage, 0)
	var journalTime time.Time
	var toolArtifactTime time.Time
	var modelEvent *rawEvent
	output := []byte(nil)
	for eventIndex, event := range events {
		if eventIndex < index && event.Kind != "HookExecutionEvent" {
			continue
		}
		if eventEvidenceKind(event) == "" {
			continue
		}
		timestamp := event.Timestamp
		if timestamp.IsZero() {
			timestamp, _ = time.Parse(time.RFC3339Nano, info.CreatedAt)
		}
		if timestamp.IsZero() {
			return application.ExternalExecutionObservation{}, ErrProtocol
		}
		if journalTime.IsZero() || timestamp.Before(journalTime) {
			journalTime = timestamp
		}
		journal = append(journal, append(json.RawMessage(nil), event.Raw...))
		if event.Kind == "ActionEvent" || event.Kind == "ObservationEvent" || event.Kind == "ACPToolCallEvent" {
			if toolArtifactTime.IsZero() || timestamp.Before(toolArtifactTime) {
				toolArtifactTime = timestamp
			}
			toolArtifactJournal = append(toolArtifactJournal, append(json.RawMessage(nil), event.Raw...))
		}
		if agentFinalEvent(event) {
			output = []byte(event.Text)
			copy := event
			modelEvent = &copy
		}
	}
	journalBytes, err := json.Marshal(journal)
	if err != nil || len(journalBytes) > client.maximumEvidenceBytes || journalTime.IsZero() {
		return application.ExternalExecutionObservation{}, ErrProtocol
	}
	evidence := []application.ExecutionEvidence{{
		EvidenceID: deterministicEventEvidenceID(brief.InvocationID, "event-journal", journalBytes),
		Kind:       "EXTERNAL_OBSERVATION", MediaType: "application/json", SourceTimestamp: journalTime,
		Content: journalBytes,
	}}
	if len(toolArtifactJournal) > 0 {
		toolArtifactBytes, marshalErr := json.Marshal(toolArtifactJournal)
		if marshalErr != nil || len(toolArtifactBytes) > client.maximumEvidenceBytes || toolArtifactTime.IsZero() {
			return application.ExternalExecutionObservation{}, ErrProtocol
		}
		evidence = append(evidence, application.ExecutionEvidence{
			EvidenceID: deterministicEventEvidenceID(brief.InvocationID, "tool-artifact-journal", toolArtifactBytes),
			Kind:       "ARTIFACT", MediaType: "application/json", SourceTimestamp: toolArtifactTime,
			Content: toolArtifactBytes,
		})
	}
	if modelEvent != nil {
		modelTime := modelEvent.Timestamp
		if modelTime.IsZero() {
			modelTime = journalTime
		}
		evidence = append(evidence, application.ExecutionEvidence{
			EvidenceID: deterministicEventEvidenceID(brief.InvocationID, modelEvent.ID, modelEvent.Raw),
			Kind:       "MODEL_OUTPUT", MediaType: "application/json", SourceTimestamp: modelTime,
			Content: append([]byte(nil), modelEvent.Raw...),
		})
	}
	return application.ExternalExecutionObservation{
		InvocationID: brief.InvocationID, RequestDigest: requestDigest,
		ConversationID: string(brief.InvocationID), State: state,
		ActorFQN: brief.ActorFQN, Execution: brief.Execution,
		ModelProfileDigest: brief.ModelProfileDigest, RuntimeIdentityDigest: brief.RuntimeIdentityDigest,
		WorkspaceID: brief.Scope.WorkspaceID, ToolPolicyDigest: brief.ToolPolicyDigest,
		EffectPolicyDigest: brief.EffectPolicyDigest, Retryable: retryable,
		Evidence: evidence, Output: output,
	}, nil
}

func eventEvidenceKind(event rawEvent) string {
	if agentFinalEvent(event) {
		return "MODEL_OUTPUT"
	}
	switch event.Kind {
	case "MessageEvent":
		return "SOURCE_SNAPSHOT"
	case "ActionEvent", "ObservationEvent", "ACPToolCallEvent":
		return "TOOL_RESULT"
	case "HookExecutionEvent", "AgentErrorEvent", "ConversationStateUpdateEvent", "Condensation", "CondensationRequest", "CondensationSummaryEvent", "UserInterruptEvent":
		return "EXTERNAL_OBSERVATION"
	default:
		return ""
	}
}

func agentFinalEvent(event rawEvent) bool {
	return event.Kind == "MessageEvent" && event.Source == "agent" && strings.TrimSpace(event.Text) != "" || event.Kind == "ObservationEvent" && event.Source == "environment" && event.ObservationKind == "FinishObservation" && event.Text != ""
}

func promptIndex(events []rawEvent, prompt string) int {
	for index, event := range events {
		if event.Kind == "MessageEvent" && event.Source == "user" && event.Text == prompt {
			return index
		}
	}
	return -1
}

// executionPromptIndex first uses exact prompt identity. Explicit recovery
// prompts also contain a checkpoint derived from the predecessor event journal,
// so their bytes may legitimately change when checkpoint serialization evolves
// during a daemon upgrade. In that case, locate the original prompt by its
// canonical execution-brief digest and verify every injected authority binding
// before accepting it. This keeps in-flight work inspectable without treating
// mutable prompt presentation as organizational identity.
func executionPromptIndex(events []rawEvent, prepared preparedExecution, brief application.ExecutionBrief, requestDigest kernel.Digest) int {
	if index := promptIndex(events, prepared.prompt); index >= 0 {
		return index
	}
	if !explicitRecoveryProfile(brief) {
		return -1
	}
	for index, event := range events {
		if event.Kind == "MessageEvent" && event.Source == "user" && recoveryExecutionPromptMatches(event.Text, prepared.workspace.Candidate, brief, requestDigest) {
			return index
		}
	}
	return -1
}

func recoveryExecutionPromptMatches(prompt string, expectedCandidate *CandidateWorkspaceBinding, brief application.ExecutionBrief, requestDigest kernel.Digest) bool {
	var observedBrief application.ExecutionBrief
	var envelope struct {
		Candidate                  *CandidateWorkspaceBinding  `json:"candidate"`
		CandidateResultRequirement *candidateResultRequirement `json:"candidate_result_requirement"`
		RecoveryCheckpoint         *progressCheckpoint         `json:"recovery_checkpoint"`
	}
	if json.Unmarshal([]byte(prompt), &observedBrief) != nil || json.Unmarshal([]byte(prompt), &envelope) != nil || envelope.RecoveryCheckpoint == nil {
		return false
	}
	checkpoint := envelope.RecoveryCheckpoint
	if checkpoint.SchemaVersion != "tekroo.teams.execution-progress-checkpoint/1.1.0" && checkpoint.SchemaVersion != "tekroo.teams.execution-progress-checkpoint/1.2.0" || checkpoint.InvocationID != brief.InvocationID || checkpoint.PriorInvocationID == nil || brief.RetryOfInvocationID == nil || *checkpoint.PriorInvocationID != *brief.RetryOfInvocationID || checkpoint.Source != "OPENHANDS_EVENT_JOURNAL" || checkpoint.SourceEventCount <= 0 || !checkpoint.SourceJournalSHA256.Valid() || checkpoint.AuthoritativeExecution.ExecutionBriefSHA256 != requestDigest || !reflect.DeepEqual(checkpoint.AuthoritativeExecution, checkpointAuthority(brief)) {
		return false
	}
	if (expectedCandidate == nil) != (envelope.Candidate == nil) {
		return false
	}
	if expectedCandidate != nil {
		if !reflect.DeepEqual(*envelope.Candidate, *expectedCandidate) || envelope.CandidateResultRequirement == nil || envelope.CandidateResultRequirement.CandidateID != expectedCandidate.CandidateID || envelope.CandidateResultRequirement.CandidateReceiptSHA256 != expectedCandidate.ReceiptSHA256 || envelope.CandidateResultRequirement.Instruction != candidateResultRequirementInstruction || observedBrief.ResultProtocol == nil || observedBrief.ResultProtocol.Instruction != candidateResultProtocolInstruction {
			return false
		}
	} else if envelope.CandidateResultRequirement != nil {
		return false
	}
	// Candidate prompts replace only explanatory result-protocol prose. Restore
	// the signed brief value before recomputing its canonical request digest.
	observedBrief.ResultProtocol = brief.ResultProtocol
	encoded, err := json.Marshal(observedBrief)
	if err != nil {
		return false
	}
	digest := sha256.Sum256(encoded)
	return kernel.Digest(hex.EncodeToString(digest[:])) == requestDigest
}

func promptDigestIndex(events []rawEvent, digest kernel.Digest) int {
	for index, event := range events {
		if event.Kind != "MessageEvent" || event.Source != "user" {
			continue
		}
		sum := sha256.Sum256([]byte(event.Text))
		if kernel.Digest(hex.EncodeToString(sum[:])) == digest {
			return index
		}
	}
	return -1
}

func conversationMatches(info conversationInfo, conversationID, workingDirectory string, requestDigest kernel.Digest) bool {
	return info.ID == conversationID && info.Workspace.Kind == "LocalWorkspace" && filepath.Clean(info.Workspace.WorkingDir) == filepath.Clean(workingDirectory) && info.Tags["tekrooinvocation"] == conversationID && info.Tags["tekroorequest"] == string(requestDigest)
}

func (client *Client) reconcileRejectedStart(ctx context.Context, brief application.ExecutionBrief, requestDigest kernel.Digest, status int) (application.ExternalExecutionObservation, error) {
	reconciled, err := client.ReconcileStart(ctx, brief, requestDigest)
	if err == nil && reconciled.State != application.ExternalAbsent {
		return reconciled, nil
	}
	return rejectedObservation(brief, requestDigest, []byte(strconv.Itoa(status))), nil
}

func absentObservation(brief application.ExecutionBrief, digest kernel.Digest) application.ExternalExecutionObservation {
	return identityObservation(brief, digest, application.ExternalAbsent)
}

func rejectedObservation(brief application.ExecutionBrief, digest kernel.Digest, output []byte) application.ExternalExecutionObservation {
	observation := identityObservation(brief, digest, application.ExternalRejected)
	observation.Output = append([]byte(nil), output...)
	return observation
}

func identityObservation(brief application.ExecutionBrief, digest kernel.Digest, state application.ExternalExecutionState) application.ExternalExecutionObservation {
	return application.ExternalExecutionObservation{
		InvocationID: brief.InvocationID, RequestDigest: digest, State: state,
		ActorFQN: brief.ActorFQN, Execution: brief.Execution,
		ModelProfileDigest: brief.ModelProfileDigest, RuntimeIdentityDigest: brief.RuntimeIdentityDigest,
		WorkspaceID: brief.Scope.WorkspaceID, ToolPolicyDigest: brief.ToolPolicyDigest,
		EffectPolicyDigest: brief.EffectPolicyDigest,
	}
}

func deterministicEventEvidenceID(invocationID kernel.UUIDv7, eventID string, raw []byte) kernel.UUIDv7 {
	digest := sha256.Sum256(raw)
	input := append([]byte(string(invocationID)+"\x00"+eventID+"\x00"), digest[:]...)
	hash := sha256.Sum256(input)
	value := hex.EncodeToString(hash[:16])
	value = value[:12] + "7" + value[13:16] + "8" + value[17:]
	return kernel.UUIDv7(value[:8] + "-" + value[8:12] + "-" + value[12:16] + "-" + value[16:20] + "-" + value[20:32])
}

func (client *Client) request(ctx context.Context, method, path string, body any) (int, []byte, error) {
	endpoint := *client.baseURL
	endpoint.Path = path
	endpoint.RawQuery = ""
	if separator := strings.IndexByte(path, '?'); separator >= 0 {
		endpoint.Path = path[:separator]
		endpoint.RawQuery = path[separator+1:]
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), reader)
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("X-Session-API-Key", client.sessionAPIKey)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.http.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, (16<<20)+1))
	if err != nil || len(content) > 16<<20 {
		return 0, nil, ErrProtocol
	}
	return response.StatusCode, content, nil
}

func jsonObject(raw json.RawMessage) bool {
	var value map[string]any
	return len(raw) > 0 && json.Unmarshal(raw, &value) == nil && value != nil
}

func mustJSON(value any) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}

func wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

var _ application.OpenHandsExecutionBoundary = (*Client)(nil)
