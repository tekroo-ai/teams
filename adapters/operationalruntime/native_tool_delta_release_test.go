//go:build mongo_integration && native_qualification

package operationalruntime

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/mongo"
	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// Qualify a catalog delta, not an entire role again. Unchanged signed bundles,
// model settings and prior role evidence are retained. New evidence exercises
// only the changed capabilities in disposable Git and Mongo storage. Nothing
// here mutates the active config, feature workspaces or operational database.
func TestPrepareNativeToolDeltaRelease(t *testing.T) {
	source, output := os.Getenv("TEKROO_NATIVE_DELTA_CONFIG"), os.Getenv("TEKROO_NATIVE_DELTA_OUTPUT")
	if source == "" || output == "" {
		t.Skip("explicit source and new output directory required")
	}
	var config ProductionConfig
	if err := readStrictJSONFile(source, &config); err != nil {
		t.Fatal(err)
	}
	if config.ExecutionBackend != "native" {
		t.Fatal("not native")
	}
	keys := map[string]ed25519.PublicKey{}
	for _, p := range config.Organization.Publishers {
		raw, err := os.ReadFile(p.PublicKeyFile)
		if err != nil {
			t.Fatal(err)
		}
		key, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(raw)))
		if err != nil || len(key) != ed25519.PublicKeySize {
			t.Fatal("invalid trusted key")
		}
		keys[p.KeyID] = key
	}
	team, err := organization.LoadTeamManifest(config.Organization.ManifestFile, config.Organization.ManifestDigest, keys)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateOperationalProfileQualifications(config.Profiles, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(output, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(name string, value any) {
		t.Helper()
		raw, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(output, name), append(raw, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	process, uri := startRuntimeMongod(t)
	defer stopRuntimeMongod(process)
	store, err := mongo.Open(contextWithTimeout(t), mongo.Config{URI: uri, Database: "native_tool_delta", ContractIdentity: kernel.ContractIdentity, ManifestSHA256: phase4ManifestSHA, MigrationLevel: 1, Policy: integratedPolicy(), BacklogLimit: 1024, DeliveryPolicyRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer closeRuntimeStore(t, store)
	ids, err := NewUUIDv7Source(SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range config.Profiles {
		p := &config.Profiles[i]
		var bundle organization.RoleBundle
		for _, role := range team.Roles {
			if role.Binding.Role == string(p.RoleFQRN) {
				bundle = role.Bundle
			}
		}
		surface, err := agenttools.NativeToolSurfaceDigest(bundle.Permissions)
		if err != nil {
			t.Fatal(err)
		}
		if surface == p.ToolPolicyDigest {
			continue
		}
		if p.Qualification == nil || p.QualificationCorpus == nil || p.NativeSettings == nil {
			t.Fatal("changed role lacks prior qualification")
		}
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "adapters/mongo"), 0700); err != nil {
			t.Fatal(err)
		}
		editable := slices.Contains(bundle.Permissions, "repository.edit")
		value := "2"
		if editable {
			value = "1"
		}
		before := "package mongo\n\nconst Value = " + value + "\n" + strings.Repeat("// retained unrelated content\n", 600)
		for name, data := range map[string]string{"AGENTS.md": "Use only the supplied Teams tools; keep unrelated bytes unchanged.\n", "go.mod": "module example.test/tooldelta\n\ngo 1.26.0\n", "adapters/mongo/value.go": before, "adapters/mongo/value_test.go": "package mongo\nimport \"testing\"\nfunc TestValue(t *testing.T) { if Value != 2 { t.Fatal(Value) } }\n"} {
			if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
		}
		for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=Tool Delta", "-c", "user.email=delta@example.test", "commit", "-qm", "baseline"}} {
			nativeQualificationGit(t, root, args...)
		}
		id, err := ids.Next()
		if err != nil {
			t.Fatal(err)
		}
		purpose := kernel.PurposeValidation
		if editable {
			purpose = kernel.PurposeImplementation
		}
		binding := nativeReleaseBinding{authority: agenttools.Authority{WorkspaceRoot: root, Permissions: bundle.Permissions, Purpose: purpose, EffectPolicyDigest: p.EffectPolicyDigest}}
		host := agenttools.Host{Timeout: 2 * time.Minute, GoBinary: config.Native.GoBinary}
		requestDigest := digestBytes([]byte(string(id) + ":" + string(surface)))
		mutation := agenttools.MutationTurnAdapter{Gateway: agenttools.MutationGateway{Bindings: binding, Host: host, Ledger: store, Policy: p.EffectPolicyDigest}, InvocationID: id, RequestDigest: requestDigest}
		tests := agenttools.TestTurnAdapter{Gateway: agenttools.TestGateway{Bindings: binding, Host: host, Ledger: store, Policy: p.EffectPolicyDigest}, InvocationID: id, RequestDigest: requestDigest}
		// This is delta evidence only. Unchanged capabilities keep their prior
		// qualification; the capability check cannot drift into unrelated work.
		definitions := []agentruntime.ToolDefinition{}
		for _, definition := range agenttools.ReadOnlyDefinitions(bundle.Permissions) {
			if definition.Name == "read_file" {
				definitions = append(definitions, definition)
			}
		}
		if editable {
			for _, definition := range agenttools.MutationDefinitions(bundle.Permissions) {
				if definition.Name == "edit_file" {
					definitions = append(definitions, definition)
				}
			}
		}
		for _, definition := range agenttools.TestDefinitions(bundle.Permissions) {
			if editable && definition.Name == "run_go_tests_worktree" || !editable && definition.Name == "run_go_tests" {
				definitions = append(definitions, definition)
			}
		}
		definitions = append(definitions, agentruntime.ToolDefinition{Name: "submit_result", Description: "Finish this capability check after its requested tool operations have succeeded.", Parameters: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["outcome"],"properties":{"outcome":{"type":"string","enum":["completed"]}}}`)})
		contract, err := agentruntime.NewStructuredTurnContract(definitions, "submit_result")
		if err != nil {
			t.Fatal(err)
		}
		journal := &nativeReleaseJournal{path: filepath.Join(output, string(p.RoleFQRN)+"-journal.json")}
		runner := agentruntime.Runner{Journal: journal, Tools: agenttools.ReadOnlyTurnAdapter{Gateway: agenttools.Gateway{Bindings: binding, Host: host}, InvocationID: id, RequestDigest: requestDigest}, Effects: toolDeltaEffects{mutation: mutation, tests: tests}, Model: agentruntime.OpenAIModel{Client: &http.Client{Timeout: 10 * time.Minute}, BaseURL: p.NativeSettings.BaseURL, Model: p.NativeSettings.Model, MaxTokens: p.NativeSettings.MaxOutputTokens, Tools: definitions, FinalTool: "submit_result", FinalSchema: definitions[len(definitions)-1].Parameters, TurnContract: contract}, FinalTool: "submit_result", Finalize: func(raw json.RawMessage) (string, error) { return string(raw), nil }, SystemPrompt: agentruntime.StructuredTurnInstruction, MaxTurns: 20}
		prompt := "Read AGENTS.md. This is a bounded tool capability check in a disposable repository, not feature work. "
		if editable {
			prompt += "Read adapters/mongo/value.go, then use edit_file to replace exactly const Value = 1 with const Value = 2 using the full-file SHA-256; preserve everything else. "
		}
		if slices.Contains(bundle.Permissions, "test.execute") {
			if editable {
				prompt += "Use run_go_tests_worktree with package ./adapters/mongo/... before finishing. "
			} else {
				prompt += "Use run_go_tests with package ./adapters/mongo/... before finishing. "
			}
		}
		prompt += "Then submit_result with outcome completed. No Git commit is needed for this isolated capability check."
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
		t.Logf("checking role=%s invocation=%s", p.RoleFQRN, id)
		result, err := runner.Run(ctx, string(id), string(requestDigest), prompt)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]int{}
		for _, entry := range journal.entries {
			if entry.Kind == agentruntime.ModelRejected {
				t.Fatalf("%s schema rejection: %s", p.RoleFQRN, entry.Payload)
			}
			if entry.Kind == agentruntime.ToolDone {
				var done agentruntime.ToolResult
				if json.Unmarshal(entry.Payload, &done) != nil || done.Error != "" {
					t.Fatalf("%s tool error: %s", p.RoleFQRN, entry.Payload)
				}
				if strings.HasPrefix(done.Name, "run_go_tests") {
					var receipt agenttools.Result
					if json.Unmarshal(done.Output, &receipt) != nil || receipt.ExecutionError != "" || receipt.ExitCode != 0 || !strings.Contains(receipt.Output, "example.test/tooldelta/adapters/mongo") {
						t.Fatal("recursive test did not pass")
					}
				}
				seen[done.Name]++
			}
		}
		if editable {
			got, err := os.ReadFile(filepath.Join(root, "adapters/mongo/value.go"))
			if err != nil || string(got) != strings.Replace(before, "const Value = 1", "const Value = 2", 1) || seen["edit_file"] != 1 {
				t.Fatal("precise edit was not executed byte-exactly once")
			}
		}
		if slices.Contains(bundle.Permissions, "test.execute") {
			name := "run_go_tests"
			if editable {
				name = "run_go_tests_worktree"
			}
			if seen[name] != 1 {
				t.Fatal("recursive test tool was not executed exactly once")
			}
		}
		write(string(p.RoleFQRN)+"-measurement.json", map[string]any{"invocation_id": id, "model_profile_digest": p.ModelProfileDigest, "tool_surface_digest": surface, "result": json.RawMessage(result), "tool_counts": seen})
		p.ToolPolicyDigest = surface
		corpus := *p.QualificationCorpus
		corpus.Revision++
		corpus.ToolSurfaceDigest = surface
		corpus.ScenarioIDs = append(slices.Clone(corpus.ScenarioIDs), "native-tool-delta-edit-subtree")
		corpusDigest, err := corpus.Digest()
		if err != nil {
			t.Fatal(err)
		}
		qual := p.Qualification.Clone()
		qual.QualificationID, err = ids.Next()
		if err != nil {
			t.Fatal(err)
		}
		qual.QualificationCorpusDigest = corpusDigest
		qual.ObservedAt = time.Now().UTC()
		qual.EvidenceIDs = append(qual.EvidenceIDs, id)
		qual.QualificationDigest, err = application.QualificationDigest(qual)
		if err != nil {
			t.Fatal(err)
		}
		p.QualificationCorpus = &corpus
		p.Qualification = &qual
	}
	write("tekrood.native-candidate.json", config)
	if _, err := LoadProductionConfig(filepath.Join(output, "tekrood.native-candidate.json")); err != nil {
		t.Fatal(err)
	}
}

type toolDeltaEffects struct {
	mutation agenttools.MutationTurnAdapter
	tests    agenttools.TestTurnAdapter
}

func (e toolDeltaEffects) Handles(name string) bool {
	return e.mutation.Handles(name) || e.tests.Handles(name)
}
func (e toolDeltaEffects) ExecuteEffect(ctx context.Context, c agentruntime.ToolCall) (json.RawMessage, error) {
	if e.tests.Handles(c.Name) {
		return e.tests.ExecuteEffect(ctx, c)
	}
	return e.mutation.ExecuteEffect(ctx, c)
}
func (e toolDeltaEffects) ReconcileEffect(ctx context.Context, c agentruntime.ToolCall) (json.RawMessage, bool, error) {
	if e.tests.Handles(c.Name) {
		return e.tests.ReconcileEffect(ctx, c)
	}
	return e.mutation.ReconcileEffect(ctx, c)
}
