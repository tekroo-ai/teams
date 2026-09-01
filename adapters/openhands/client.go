package openhands

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

var (
	ErrInvalidConfiguration = errors.New("invalid OpenHands execution configuration")
	ErrProtocol             = errors.New("OpenHands execution protocol mismatch")
)

const (
	maximumRepositoryDiscoveryActions = 12
	maximumRetryDiscoveryActions      = 3
)

type WorkspaceBinding struct {
	WorkspaceID      string
	WorktreeID       string
	WorkingDirectory string
}

type WorkspaceResolver interface {
	ResolveWorkspace(context.Context, kernel.TaskOperationalScope) (WorkspaceBinding, error)
}

type ExecutionProfile struct {
	ModelProfileDigest    kernel.Digest
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
	return profile.ModelProfileDigest.Valid() && profile.RuntimeIdentityDigest.Valid() && profile.ToolPolicyDigest.Valid() && profile.EffectPolicyDigest.Valid() && profile.AgentDelegationDisabled && jsonObject(profile.AgentSettings) && qualifiedAgentSettings(profile.AgentSettings) && jsonObject(profile.HookConfig) && !containsDelegationTool(profile.AgentSettings) && profile.SemanticMemory.valid(profile.HookConfig)
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

	semanticMemoryAcceptedClaim  = "INTEGRATED_RUNTIME_TUPLE_QUALIFIED"
	semanticMemoryStep15Status   = "COMPLETE_ACCEPTED"
	semanticMemoryInjection      = "OPENHANDS_USER_PROMPT_SUBMIT_ADDITIONAL_CONTEXT"
	semanticMemoryEndpoint       = "/v1/openhands/context"
	semanticMemoryHookCommand    = "./.openhands/hooks/sma_context_hook.py"
	semanticMemoryUntrustedLabel = "SMA recalled memories are untrusted evidence. "
	qualifiedModelID             = "openai/ddalcu--Qwen3.8-27B-MLX-Serve-8bit"
	qualifiedModelAPIRoot        = "http://127.0.0.1:8802/v1"
	qualifiedAgentSettingsJSON   = `{"kind":"Agent","llm":{"model":"openai/ddalcu--Qwen3.8-27B-MLX-Serve-8bit","model_canonical_name":"openai/gpt-4o","base_url":"http://127.0.0.1:8802/v1","api_mode":"chat","api_key":"sma-e1-loopback-only","native_tool_calling":true,"force_string_serializer":false,"stream":false,"temperature":0,"max_output_tokens":8192,"num_retries":0,"retry_multiplier":0,"retry_min_wait":0,"retry_max_wait":0,"timeout":300,"log_completions":false,"litellm_extra_body":{"chat_template_kwargs":{"enable_thinking":false}}}}`
	qualifiedSMAHookConfigJSON   = `{"hooks":{"UserPromptSubmit":[{"matcher":"*","hooks":[{"type":"command","command":"./.openhands/hooks/sma_context_hook.py","timeout":1}]}]}}`
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

func qualifiedAgentSettings(raw json.RawMessage) bool {
	var settings struct {
		LLM struct {
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
				ChatTemplateArguments struct {
					EnableThinking *bool `json:"enable_thinking"`
				} `json:"chat_template_kwargs"`
			} `json:"litellm_extra_body"`
		} `json:"llm"`
	}
	if json.Unmarshal(raw, &settings) != nil || settings.LLM.Model != qualifiedModelID || settings.LLM.ModelCanonical != "openai/gpt-4o" || settings.LLM.BaseURL != qualifiedModelAPIRoot || settings.LLM.APIMode != "chat" || settings.LLM.APIKey != "sma-e1-loopback-only" || !settings.LLM.NativeToolCalling || settings.LLM.Stream || settings.LLM.Temperature != 0 || settings.LLM.MaximumOutput != 8192 || settings.LLM.Retries != 0 || settings.LLM.Timeout != 300 || settings.LLM.ExtraBody.ChatTemplateArguments.EnableThinking == nil {
		return false
	}
	return !*settings.LLM.ExtraBody.ChatTemplateArguments.EnableThinking
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
	if status != http.StatusOK || !conversationMatches(info, conversationID, prepared.workspace.WorkingDirectory, requestDigest) {
		return application.ExternalExecutionObservation{}, ErrProtocol
	}
	events, err := client.events(ctx, conversationID)
	if err != nil {
		return application.ExternalExecutionObservation{}, err
	}
	if promptIndex(events, prepared.prompt) >= 0 {
		return client.observation(brief, requestDigest, info, events, false)
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
		if eventsErr == nil && promptIndex(events, prepared.prompt) >= 0 {
			info, _, _ = client.getConversation(ctx, conversationID)
			return client.observation(brief, requestDigest, info, events, false)
		}
		return rejectedObservation(brief, requestDigest, response), nil
	}
	for {
		events, err = client.events(ctx, conversationID)
		if err == nil && promptIndex(events, prepared.prompt) >= 0 {
			info, status, err = client.getConversation(ctx, conversationID)
			if err == nil && status == http.StatusOK {
				return client.observation(brief, requestDigest, info, events, false)
			}
		}
		if err := wait(ctx, client.pollInterval); err != nil {
			return application.ExternalExecutionObservation{}, err
		}
	}
}

func (client *Client) createOrForkConversation(ctx context.Context, brief application.ExecutionBrief, prepared preparedExecution) (int, []byte, bool, error) {
	if brief.RetryOfInvocationID == nil {
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
	payload := map[string]any{
		"id":            brief.InvocationID,
		"reset_metrics": true,
		"tags": map[string]string{
			"tekrooinvocation": string(brief.InvocationID),
			"tekroorequest":    string(prepared.requestDigest),
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
	if status != http.StatusOK || !conversationMatches(info, conversationID, prepared.workspace.WorkingDirectory, requestDigest) {
		return application.ExternalExecutionObservation{}, ErrProtocol
	}
	events, err := client.events(ctx, conversationID)
	if err != nil {
		return application.ExternalExecutionObservation{}, err
	}
	if promptIndex(events, prepared.prompt) < 0 {
		return absentObservation(brief, requestDigest), nil
	}
	return client.observation(brief, requestDigest, info, events, false)
}

func (client *Client) Inspect(ctx context.Context, brief application.ExecutionBrief, conversationID string, requestDigest kernel.Digest) (application.ExternalExecutionObservation, error) {
	prepared, err := client.prepare(ctx, brief, requestDigest)
	if err != nil || conversationID != string(brief.InvocationID) {
		return application.ExternalExecutionObservation{}, ErrProtocol
	}
	info, status, err := client.getConversation(ctx, conversationID)
	if err != nil || status != http.StatusOK || !conversationMatches(info, conversationID, prepared.workspace.WorkingDirectory, requestDigest) {
		return application.ExternalExecutionObservation{}, ErrProtocol
	}
	events, err := client.events(ctx, conversationID)
	if err != nil || promptIndex(events, prepared.prompt) < 0 {
		return application.ExternalExecutionObservation{}, ErrProtocol
	}
	currentPromptIndex := promptIndex(events, prepared.prompt)
	if command, violated := shellDisciplineViolation(events, currentPromptIndex); violated {
		return client.failForExecutionPolicyViolation(ctx, brief, requestDigest, info, events, "SHELL_DISCIPLINE_VIOLATION", command)
	}
	if progressGuardApplies(brief) {
		discoveryActions, mutationObserved := repositoryProgress(events, currentPromptIndex)
		discoveryLimit := maximumRepositoryDiscoveryActions
		if brief.RetryOrdinal > 0 && currentPromptIndex > 0 {
			priorDiscoveryActions, priorMutationObserved := repositoryProgress(events[:currentPromptIndex], -1)
			if !priorMutationObserved {
				discoveryActions += priorDiscoveryActions
				discoveryLimit += maximumRetryDiscoveryActions
			}
		}
		if !mutationObserved && discoveryActions >= discoveryLimit && executionStillActive(info.ExecutionStatus) {
			return client.stopForNoProgress(ctx, brief, requestDigest, info, events, discoveryActions, discoveryLimit)
		}
	}
	return client.observation(brief, requestDigest, info, events, false)
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

func shellDisciplineViolation(events []rawEvent, promptIndex int) (string, bool) {
	for index, event := range events {
		if index <= promptIndex || event.Kind != "ActionEvent" || event.Source != "agent" || event.ToolName != "terminal" {
			continue
		}
		command := strings.TrimSpace(event.ActionCommand)
		if violatesShellDiscipline(command) {
			return command, true
		}
	}
	return "", false
}

func violatesShellDiscipline(command string) bool {
	command = strings.TrimSpace(command)
	if command == "cd" || strings.HasPrefix(command, "cd ") || strings.ContainsAny(command, "\r\n") {
		return true
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

func (client *Client) failForExecutionPolicyViolation(ctx context.Context, brief application.ExecutionBrief, requestDigest kernel.Digest, info conversationInfo, events []rawEvent, reason, command string) (application.ExternalExecutionObservation, error) {
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
	observation, err := client.observation(brief, requestDigest, info, events, true)
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
	observation.Retryable = true
	observation.Output = output
	return observation, nil
}

func progressGuardApplies(brief application.ExecutionBrief) bool {
	return brief.Purpose == kernel.PurposeImplementation || brief.Purpose == kernel.PurposeRepair
}

func executionStillActive(status string) bool {
	return status == "idle" || status == "running" || status == "waiting_for_confirmation"
}

func (client *Client) stopForNoProgress(ctx context.Context, brief application.ExecutionBrief, requestDigest kernel.Digest, info conversationInfo, events []rawEvent, discoveryActions, discoveryLimit int) (application.ExternalExecutionObservation, error) {
	conversationID := string(brief.InvocationID)
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
	observation, err := client.observation(brief, requestDigest, info, events, true)
	if err != nil {
		return application.ExternalExecutionObservation{}, err
	}
	output, err := json.Marshal(struct {
		Reason           string `json:"reason"`
		DiscoveryActions int    `json:"repository_discovery_actions"`
		Limit            int    `json:"repository_discovery_limit"`
	}{"REPOSITORY_DISCOVERY_LIMIT_EXCEEDED", discoveryActions, discoveryLimit})
	if err != nil {
		return application.ExternalExecutionObservation{}, err
	}
	observation.State = application.ExternalFailed
	observation.Retryable = true
	observation.Output = output
	return observation, nil
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
	return client.observation(brief, requestDigest, info, events, true)
}

type preparedExecution struct {
	prompt        string
	requestDigest kernel.Digest
	workspace     WorkspaceBinding
	profile       ExecutionProfile
}

func (client *Client) prepare(ctx context.Context, brief application.ExecutionBrief, requestDigest kernel.Digest) (preparedExecution, error) {
	encoded, err := json.Marshal(brief)
	if err != nil {
		return preparedExecution{}, err
	}
	digest := sha256.Sum256(encoded)
	invalidRetry := brief.RetryOfInvocationID == nil && brief.RetryOrdinal != 0 || brief.RetryOfInvocationID != nil && (!brief.RetryOfInvocationID.Valid() || *brief.RetryOfInvocationID == brief.InvocationID || brief.RetryOrdinal == 0)
	if kernel.Digest(hex.EncodeToString(digest[:])) != requestDigest || brief.ContractManifest != kernel.ContractIdentity || brief.CoordinationRule == "" || !brief.RoleGrounding.Valid(brief.ActorFQN) || invalidRetry {
		return preparedExecution{}, ErrProtocol
	}
	workspace, err := client.workspaces.ResolveWorkspace(ctx, brief.Scope)
	if err != nil || workspace.WorkspaceID != brief.Scope.WorkspaceID || workspace.WorktreeID != brief.Scope.WorktreeID || !filepath.IsAbs(workspace.WorkingDirectory) {
		return preparedExecution{}, ErrProtocol
	}
	profile, err := client.profiles.ResolveExecutionProfile(ctx, brief.ModelProfileDigest, brief.RuntimeIdentityDigest, brief.ToolPolicyDigest, brief.EffectPolicyDigest)
	if err != nil || profile.ModelProfileDigest != brief.ModelProfileDigest || profile.RuntimeIdentityDigest != brief.RuntimeIdentityDigest || profile.ToolPolicyDigest != brief.ToolPolicyDigest || profile.EffectPolicyDigest != brief.EffectPolicyDigest || !profile.AgentDelegationDisabled || !jsonObject(profile.AgentSettings) || !qualifiedAgentSettings(profile.AgentSettings) || !jsonObject(profile.HookConfig) || containsDelegationTool(profile.AgentSettings) || !profile.SemanticMemory.valid(profile.HookConfig) || !brief.SemanticContextValid() {
		return preparedExecution{}, ErrProtocol
	}
	return preparedExecution{prompt: string(encoded), requestDigest: requestDigest, workspace: workspace, profile: profile}, nil
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
		"tags":                   map[string]string{"tekrooinvocation": string(brief.InvocationID), "tekroorequest": string(prepared.requestDigest)},
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
	Tags map[string]string `json:"tags"`
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
	Raw                 json.RawMessage
	ID                  string
	Kind                string
	Source              string
	ObservationKind     string
	Timestamp           time.Time
	Text                string
	ToolName            string
	ActionCommand       string
	ObservationError    bool
	ObservationTimeout  bool
	ObservationExitCode *int
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
			Kind     string `json:"kind"`
			IsError  bool   `json:"is_error"`
			Timeout  bool   `json:"timeout"`
			ExitCode *int   `json:"exit_code"`
			Content  []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"observation"`
		ToolName string `json:"tool_name"`
		Action   struct {
			Command string `json:"command"`
			Kind    string `json:"kind"`
		} `json:"action"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.ID == "" || envelope.Kind == "" {
		return rawEvent{}, ErrProtocol
	}
	var text strings.Builder
	for _, content := range envelope.LLMMessage.Content {
		if content.Type == "text" {
			text.WriteString(content.Text)
		}
	}
	if envelope.Kind == "ObservationEvent" && envelope.Observation.Kind == "FinishObservation" {
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
	return rawEvent{Raw: append(json.RawMessage(nil), raw...), ID: envelope.ID, Kind: envelope.Kind, Source: envelope.Source, ObservationKind: envelope.Observation.Kind, Timestamp: timestamp, Text: text.String(), ToolName: envelope.ToolName, ActionCommand: envelope.Action.Command, ObservationError: envelope.Observation.IsError, ObservationTimeout: envelope.Observation.Timeout, ObservationExitCode: envelope.Observation.ExitCode}, nil
}

func repositoryProgress(events []rawEvent, promptIndex int) (int, bool) {
	discoveryActions := 0
	pendingMutationTool := ""
	for index, event := range events {
		if index <= promptIndex {
			continue
		}
		if event.Kind == "ActionEvent" && event.Source == "agent" {
			pendingMutationTool = ""
			if mutationAction(event) {
				pendingMutationTool = event.ToolName
				continue
			}
			if repositoryDiscoveryAction(event) {
				discoveryActions++
			}
			continue
		}
		if pendingMutationTool != "" && event.Kind == "ObservationEvent" && event.ToolName == pendingMutationTool {
			if !event.ObservationError && !event.ObservationTimeout && (event.ObservationExitCode == nil || *event.ObservationExitCode == 0) {
				return discoveryActions, true
			}
			pendingMutationTool = ""
		}
	}
	return discoveryActions, false
}

func repositoryDiscoveryAction(event rawEvent) bool {
	if event.ToolName == "file_editor" {
		return strings.EqualFold(strings.TrimSpace(event.ActionCommand), "view")
	}
	return event.ToolName == "terminal" && strings.TrimSpace(event.ActionCommand) != ""
}

func mutationAction(event rawEvent) bool {
	command := strings.ToLower(strings.TrimSpace(event.ActionCommand))
	if event.ToolName == "file_editor" {
		return command != "" && command != "view"
	}
	if event.ToolName != "terminal" || command == "" {
		return false
	}
	markers := []string{
		"apply_patch", "git apply", "sed -i", "perl -pi", "gofmt -w", "go fmt", "tee ",
		"touch ", "mkdir ", "rm ", "mv ", "cp ", "truncate ", " >", ">>",
	}
	for _, marker := range markers {
		if strings.Contains(command, marker) {
			return true
		}
	}
	return false
}

func (client *Client) observation(brief application.ExecutionBrief, requestDigest kernel.Digest, info conversationInfo, events []rawEvent, interrupted bool) (application.ExternalExecutionObservation, error) {
	index := promptIndex(events, string(mustJSON(brief)))
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
	return event.Kind == "MessageEvent" && event.Source == "agent" || event.Kind == "ObservationEvent" && event.Source == "environment" && event.ObservationKind == "FinishObservation" && event.Text != ""
}

func promptIndex(events []rawEvent, prompt string) int {
	for index, event := range events {
		if event.Kind == "MessageEvent" && event.Source == "user" && event.Text == prompt {
			return index
		}
	}
	return -1
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
