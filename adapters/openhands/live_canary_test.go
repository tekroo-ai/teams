package openhands

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

// TestLiveTeamsControlledRoleCanary is intentionally skipped during ordinary
// test runs. A qualification package supplies one exact execution brief,
// workspace, and model profile; the test then starts and observes OpenHands
// exclusively through Client. This matters because Client enforces the same
// compaction checkpoints, tool policy, and no-progress checks used by tekrood.
func TestLiveTeamsControlledRoleCanary(t *testing.T) {
	packagePath := os.Getenv("TEKROO_OPENHANDS_CANARY_PACKAGE")
	if packagePath == "" {
		t.Skip("TEKROO_OPENHANDS_CANARY_PACKAGE is not set")
	}
	encoded, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	var qualification struct {
		Brief         application.ExecutionBrief `json:"brief"`
		AgentSettings json.RawMessage            `json:"agent_settings"`
		Workspace     WorkspaceBinding           `json:"workspace"`
		BaseURL       string                     `json:"base_url"`
		SessionAPIKey string                     `json:"session_api_key"`
		APIKeyFile    string                     `json:"session_api_key_file"`
		Timeout       string                     `json:"timeout"`
	}
	if err := json.Unmarshal(encoded, &qualification); err != nil {
		t.Fatal(err)
	}
	if qualification.SessionAPIKey == "" && qualification.APIKeyFile != "" {
		key, readErr := os.ReadFile(qualification.APIKeyFile)
		if readErr != nil {
			t.Fatal(readErr)
		}
		qualification.SessionAPIKey = strings.TrimSpace(string(key))
	}
	if qualification.BaseURL == "" || qualification.SessionAPIKey == "" || qualification.Workspace.WorkingDirectory == "" {
		t.Fatal("qualification package is incomplete")
	}
	timeout := 30 * time.Minute
	if qualification.Timeout != "" {
		timeout, err = time.ParseDuration(qualification.Timeout)
		if err != nil {
			t.Fatal(err)
		}
	}
	profile, err := NewBoundExecutionProfile(
		qualification.Brief.ModelProfileDigest,
		qualification.Brief.RoleGrounding.RoleFQRN,
		qualification.Brief.RoleGrounding.BundleDigest,
		qualification.Brief.RuntimeIdentityDigest,
		qualification.Brief.ToolPolicyDigest,
		qualification.Brief.EffectPolicyDigest,
		qualification.AgentSettings,
		0,
		"tekroo_phase9_qualification",
		"sma_v4",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !profile.validFor(qualification.Brief) {
		t.Fatal("resolved execution profile does not match the execution brief")
	}
	if !qualification.Brief.SemanticContext.Valid() {
		t.Fatal("semantic context is structurally invalid")
	}
	if !qualification.Brief.SemanticContextValid() {
		t.Fatalf("semantic context does not match execution brief: context=%+v brief=%+v", qualification.Brief.SemanticContext, qualification.Brief)
	}
	client, err := NewClient(Config{
		BaseURL: qualification.BaseURL, SessionAPIKey: qualification.SessionAPIKey,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Workspaces: staticWorkspace{binding: qualification.Workspace},
		Profiles:   staticProfile{profile: profile}, PollInterval: 100 * time.Millisecond,
		MaximumPages: 128, MaximumEvidenceBytes: 16 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	briefBytes, err := json.Marshal(qualification.Brief)
	if err != nil {
		t.Fatal(err)
	}
	digestBytes := sha256.Sum256(briefBytes)
	requestDigest := kernel.Digest(hex.EncodeToString(digestBytes[:]))
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	observation, err := client.Start(ctx, qualification.Brief, requestDigest)
	if err != nil {
		info, status, getErr := client.getConversation(ctx, string(qualification.Brief.InvocationID))
		var expected conversationAgentSettings
		_ = json.Unmarshal(qualification.AgentSettings, &expected)
		t.Fatalf("start: %v; conversation status=%d get_error=%v matches=%t actual_agent=%+v expected_agent=%+v", err, status, getErr, conversationAgentMatches(info, qualification.AgentSettings), info.Agent, expected)
	}
	for observation.State == application.ExternalRunning {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(250 * time.Millisecond):
		}
		observation, err = client.Inspect(ctx, qualification.Brief, string(qualification.Brief.InvocationID), requestDigest)
		if err != nil {
			t.Fatal(err)
		}
	}
	if observation.State != application.ExternalSucceeded {
		t.Fatalf("canary state=%s retryable=%t output=%s", observation.State, observation.Retryable, observation.Output)
	}
	if strings.TrimSpace(string(observation.Output)) == "" {
		t.Fatal("canary finished without a model result")
	}
	if protocol := qualification.Brief.ResultProtocol; protocol != nil && !strings.HasPrefix(strings.TrimSpace(string(observation.Output)), protocol.Marker) {
		t.Fatalf("canary output does not begin with %q: %s", protocol.Marker, observation.Output)
	}
	t.Logf("conversation=%s state=%s evidence=%d output=%s", qualification.Brief.InvocationID, observation.State, len(observation.Evidence), observation.Output)
}
