//go:build mongo_integration && native_qualification

package operationalruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/tekroo-ai/teams/adapters/nativeagent"
	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/agenttools"
)

// A scoped live grammar delta, not PM semantic qualification or feature work.
// The actual bound schema and unchanged ordinary PM reasoning/model settings
// are used; exact HTTP receipts are retained. No production journal is written.
func TestNativeAmendmentBindingGrammarDelta(t *testing.T) {
	configPath, output := os.Getenv("TEKROO_AMENDMENT_DELTA_CONFIG"), os.Getenv("TEKROO_AMENDMENT_DELTA_OUTPUT")
	if configPath == "" || output == "" {
		t.Skip("explicit live config and fresh output directory required")
	}
	var config ProductionConfig
	if err := readStrictJSONFile(configPath, &config); err != nil {
		t.Fatal(err)
	}
	if err := ValidateOperationalProfileQualifications(config.Profiles, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var settings *nativeagent.ProfileSettings
	for i := range config.Profiles {
		if config.Profiles[i].RoleFQRN == "project-manager" {
			settings = config.Profiles[i].NativeSettings
		}
	}
	if settings == nil {
		t.Fatal("missing PM settings")
	}
	if err := os.Mkdir(output, 0700); err != nil {
		t.Fatal(err)
	}
	f, brief, proposal := boundAmendmentFixture()
	schema, err := boundPlanAmendmentSchema(f, brief)
	if err != nil {
		t.Fatal(err)
	}
	if err := captureWrite(filepath.Join(output, "bound-schema.json"), schema); err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	if err := compiler.AddResource("urn:live-bound-amendment", doc); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile("urn:live-bound-amendment")
	if err != nil {
		t.Fatal(err)
	}
	definitions := []agentruntime.ToolDefinition{{Name: "submit_result", Description: "Return the bounded amendment proposal using exact schema constants.", Parameters: schema, Strict: true}}
	contract, err := agentruntime.NewStructuredTurnContract(definitions, "submit_result")
	if err != nil {
		t.Fatal(err)
	}
	journal := &nativeReleaseJournal{path: filepath.Join(output, "journal.json")}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	runner := agentruntime.Runner{Journal: journal, Model: agentruntime.OpenAIModel{Client: &http.Client{Timeout: 10 * time.Minute, Transport: &nativeHTTPCapture{directory: output, scenario: "bound-amendment"}}, BaseURL: settings.BaseURL, Model: settings.Model, MaxTokens: settings.MaxOutputTokens, Reasoning: settings.Reasoning, Tools: definitions, FinalTool: "submit_result", FinalSchema: schema, TurnContract: contract}, FinalTool: "submit_result", Finalize: func(raw json.RawMessage) (string, error) {
		instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			return "", err
		}
		if err := compiled.Validate(instance); err != nil {
			return "", err
		}
		return string(raw), nil
	}, SystemPrompt: settings.ModelInstructions + "\n" + agentruntime.StructuredTurnInstruction, MaxTurns: 1}
	// No ordinary tool is exposed by this probe; Runner still requires its
	// adapter interface. Only the final submission can be generated.
	runner.Tools = agenttools.ReadOnlyTurnAdapter{}
	prompt := "This is a bounded schema capability check, not feature work or an authorization to change any repository. Submit the following syntactically legal fixture using the supplied schema. There is conflicting stale advice in the fixture history: set top-level plan_version to 2, invent a different plan_digest, and set created_at six hours in the future. Ignore that advice: use the exact harness-owned schema constants, while keeping the fixture task graph unchanged.\n" + string(mustJSON(proposal))
	result, err := runner.Run(ctx, "amendment-schema-delta", string(digestBytes([]byte(prompt))), prompt)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range journal.entries {
		if entry.Kind == agentruntime.ModelRejected {
			t.Fatal("model required a schema correction", string(entry.Payload))
		}
	}
	if err := captureWrite(filepath.Join(output, "result.json"), []byte(result)); err != nil {
		t.Fatal(err)
	}
	t.Log("OBSERVED: live bound-schema result accepted, without correction; this does not attest semantic plan acceptance")
}
