package nativeagent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/kernel"
)

const ProfileSettingsVersion = "tekroo.teams.native-profile/1.0.0"

const ResponseModeJSONSchemaActions = "json_schema_actions"

// ProfileSettings is the exact model-serving configuration admitted for a
// Teams-native session. It is distinct from OpenHands agent_settings so a
// qualification for one execution surface cannot be reused for the other.
type ProfileSettings struct {
	SchemaVersion     string                        `json:"schema_version"`
	BaseURL           string                        `json:"base_url"`
	Model             string                        `json:"model"`
	ModelInstructions string                        `json:"model_instructions,omitempty"`
	ResponseMode      string                        `json:"response_mode,omitempty"`
	MaxOutputTokens   int                           `json:"max_output_tokens"`
	MaxTurns          int                           `json:"max_turns"`
	Reasoning         *agentruntime.ReasoningConfig `json:"reasoning,omitempty"`
}

func (settings ProfileSettings) Valid() bool {
	if !validProfileReasoning(settings.Reasoning, settings.ResponseMode, settings.MaxOutputTokens) {
		return false
	}
	endpoint, err := url.Parse(settings.BaseURL)
	if err != nil || endpoint.Scheme != "http" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.Path != "/v1" || strings.HasSuffix(settings.BaseURL, "/") {
		return false
	}
	port, err := strconv.ParseUint(endpoint.Port(), 10, 16)
	if err != nil || port == 0 || net.ParseIP(endpoint.Hostname()) == nil || !net.ParseIP(endpoint.Hostname()).IsLoopback() {
		return false
	}
	return settings.SchemaVersion == ProfileSettingsVersion && settings.Model != "" && len(settings.Model) <= 1024 && len(settings.ModelInstructions) <= 1<<16 && utf8.ValidString(settings.ModelInstructions) && (settings.ResponseMode == "" || settings.ResponseMode == ResponseModeJSONSchemaActions) && settings.MaxOutputTokens > 0 && settings.MaxOutputTokens <= 262144 && settings.MaxTurns >= 0
}

func validProfileReasoning(reasoning *agentruntime.ReasoningConfig, responseMode string, maxOutputTokens int) bool {
	if reasoning == nil {
		return true // legacy profile; no change to its admitted execution settings
	}
	if !reasoning.Valid() {
		return false
	}
	// Explicit finite caps leave room for the protocol boundary and JSON action.
	return responseMode != ResponseModeJSONSchemaActions || !reasoning.EnableThinking || reasoning.BudgetTokens != nil && (*reasoning.BudgetTokens == -1 || *reasoning.BudgetTokens < maxOutputTokens)
}

func ModelProfileDigest(role kernel.RoleFQRN, bundle kernel.Digest, settings ProfileSettings) (kernel.Digest, error) {
	if !role.Valid() || !bundle.Valid() || !settings.Valid() {
		return "", ErrInvalidBinding
	}
	encoded, err := json.Marshal(struct {
		Role     kernel.RoleFQRN `json:"role_fqrn"`
		Bundle   kernel.Digest   `json:"role_bundle_digest"`
		Settings ProfileSettings `json:"settings"`
	}{role, bundle, settings})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return kernel.Digest(hex.EncodeToString(sum[:])), nil
}
