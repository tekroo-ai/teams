package operationalruntime

import (
	"testing"

	"github.com/tekroo-ai/teams/agentruntime"
)

func TestProductionReasoningChangeCannotReuseLegacyProfileIdentity(t *testing.T) {
	path, config := writeNativeProductionFixture(t)
	budget := -1
	config.Profiles[0].NativeSettings.Reasoning = &agentruntime.ReasoningConfig{EnableThinking: true, Effort: "medium", BudgetTokens: &budget}
	writeJSON(t, path, config, 0600)
	if _, err := LoadProductionConfig(path); err == nil {
		t.Fatal("reasoning changed without rebinding profile/qualification identity")
	}
}
