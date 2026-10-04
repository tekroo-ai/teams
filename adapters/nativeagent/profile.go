package nativeagent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/tekroo-ai/teams/kernel"
)

const ProfileSettingsVersion = "tekroo.teams.native-profile/1.0.0"

// ProfileSettings is the exact model-serving configuration admitted for a
// Teams-native session. It is distinct from OpenHands agent_settings so a
// qualification for one execution surface cannot be reused for the other.
type ProfileSettings struct {
	SchemaVersion   string `json:"schema_version"`
	BaseURL         string `json:"base_url"`
	Model           string `json:"model"`
	MaxOutputTokens int    `json:"max_output_tokens"`
	MaxTurns        int    `json:"max_turns"`
}

func (settings ProfileSettings) Valid() bool {
	endpoint, err := url.Parse(settings.BaseURL)
	if err != nil || endpoint.Scheme != "http" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.Path != "/v1" || strings.HasSuffix(settings.BaseURL, "/") {
		return false
	}
	port, err := strconv.ParseUint(endpoint.Port(), 10, 16)
	if err != nil || port == 0 || net.ParseIP(endpoint.Hostname()) == nil || !net.ParseIP(endpoint.Hostname()).IsLoopback() {
		return false
	}
	return settings.SchemaVersion == ProfileSettingsVersion && settings.Model != "" && len(settings.Model) <= 1024 && settings.MaxOutputTokens > 0 && settings.MaxOutputTokens <= 262144 && settings.MaxTurns >= 0
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
