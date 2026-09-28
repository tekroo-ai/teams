package openhands

import (
	"encoding/json"
	"testing"
)

func TestGeneratedSettingsAcceptMaterializedTeamsCheckpointCondenser(t *testing.T) {
	settings, err := NewOpenAICompatibleAgentSettings(AgentSettingsConfig{
		Model: "openai/local", ModelCanonicalName: "openai/gpt-4o", BaseURL: "http://127.0.0.1:8802/v1", APIKey: "fixture", Tools: []string{},
		MaximumOutputTokens: 8192, CondenserOutputTokens: 4096, TimeoutSeconds: 1200, CondenserMaximumEvents: 80, CondenserMaximumTokens: 96000,
	})
	if err != nil || !configurableAgentSettings(settings) {
		t.Fatalf("generated checkpoint profile is invalid: %v", err)
	}
	var info conversationInfo
	if err := json.Unmarshal(mustJSON(map[string]any{"agent": json.RawMessage(settings)}), &info); err != nil {
		t.Fatal(err)
	}
	info.Agent.Condenser.CondenserKind = ""
	info.Agent.Condenser.Kind = "TeamsCheckpointCondenser"
	if !conversationAgentMatches(info, settings) {
		t.Fatal("OpenHands checkpoint condenser read model did not match the request profile")
	}
	info.Agent.Condenser.Kind = "LLMSummarizingCondenser"
	if conversationAgentMatches(info, settings) {
		t.Fatal("different condenser matched the request profile")
	}
}
