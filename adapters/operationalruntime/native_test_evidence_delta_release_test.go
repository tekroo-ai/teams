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
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// Preserve prior role qualification; add real model/tool receipts only for the
// exact test-contract delta. This never dispatches a feature or changes a run.
func TestPrepareNativeTestEvidenceDeltaRelease(t *testing.T) {
	source, output, priorPath := os.Getenv("TEKROO_NATIVE_TEST_EVIDENCE_CONFIG"), os.Getenv("TEKROO_NATIVE_TEST_EVIDENCE_OUTPUT"), os.Getenv("TEKROO_NATIVE_TEST_EVIDENCE_PRIOR_TOOLS")
	if source == "" || output == "" || priorPath == "" {
		t.Skip("explicit source, new output and prior tool catalog required")
	}
	var config ProductionConfig
	if err := readStrictJSONFile(source, &config); err != nil {
		t.Fatal(err)
	}
	if config.ExecutionBackend != "native" {
		t.Fatal("not native")
	}
	if err := ValidateOperationalProfileQualifications(config.Profiles, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var prior []agenttools.Spec
	if err := readStrictJSONFile(priorPath, &prior); err != nil {
		t.Fatal(err)
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
	if err := os.Mkdir(output, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(name string, value any) {
		t.Helper()
		raw, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := captureWrite(filepath.Join(output, name), append(raw, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	write("baseline-config.json", config)
	write("prior-tools.json", prior)
	process, uri := startRuntimeMongod(t)
	defer stopRuntimeMongod(process)
	store, err := mongo.Open(contextWithTimeout(t), mongo.Config{URI: uri, Database: "native_test_evidence_delta", ContractIdentity: kernel.ContractIdentity, ManifestSHA256: phase4ManifestSHA, MigrationLevel: 1, Policy: integratedPolicy(), BacklogLimit: 1024, DeliveryPolicyRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer closeRuntimeStore(t, store)
	ids, err := NewUUIDv7Source(SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for i := range config.Profiles {
		p := &config.Profiles[i]
		index := slices.IndexFunc(team.Roles, func(r organization.LoadedRole) bool { return r.Binding.Role == string(p.RoleFQRN) })
		if index < 0 {
			t.Fatal("profile lacks signed role")
		}
		role := team.Roles[index]
		if p.RoleBundleDigest != role.Binding.BundleDigest || p.ModelProfileDigest != role.Binding.ModelProfileDigest {
			t.Fatal("profile differs from signed binding")
		}
		surface, err := agenttools.NativeToolSurfaceDigest(role.Bundle.Permissions)
		if err != nil {
			t.Fatal(err)
		}
		if surface == p.ToolPolicyDigest {
			continue
		}
		if !slices.Contains(role.Bundle.Permissions, "test.execute") || p.Qualification == nil || p.NativeSettings == nil {
			t.Fatal("unexpected unqualified or non-test surface change")
		}
		oldTools := append(agenttools.ReadOnlyDefinitions(role.Bundle.Permissions), agenttools.MutationDefinitions(role.Bundle.Permissions)...)
		oldTools = append(oldTools, agenttools.TestDefinitions(role.Bundle.Permissions)...)
		for j := range oldTools {
			if !slices.Contains([]string{"run_go_tests", "run_go_tests_worktree"}, oldTools[j].Name) {
				continue
			}
			old := slices.IndexFunc(prior, func(s agenttools.Spec) bool { return s.Name == oldTools[j].Name })
			if old < 0 {
				t.Fatal("missing prior test definition")
			}
			oldTools[j].Description, oldTools[j].Parameters = prior[old].Description, prior[old].InputSchema
		}
		slices.SortFunc(oldTools, func(a, b agentruntime.ToolDefinition) int { return strings.Compare(a.Name, b.Name) })
		oldRaw, err := json.Marshal(struct {
			SchemaVersion string                        `json:"schema_version"`
			Tools         []agentruntime.ToolDefinition `json:"tools"`
		}{"tekroo.teams.native-tool-surface/1.0.0", oldTools})
		if err != nil || digestBytes(oldRaw) != p.ToolPolicyDigest {
			t.Fatal("unexpected delta outside the two test definitions")
		}
		root := t.TempDir()
		files := map[string]string{
			"AGENTS.md":           "Bounded test-evidence qualification. Use only exposed tools; do not modify sources.\n",
			"go.mod":              "module example.test/testevidence\n\ngo 1.26.0\n",
			"value.go":            "package evidence\n\nconst Value = 2\n",
			"value_test.go":       "package evidence\nimport \"testing\"\nfunc TestDefaultEvidence(t *testing.T) { if Value != 2 { t.Fatal(Value) } }\n",
			"integration_test.go": "//go:build mongo_integration\n\npackage evidence\nimport \"testing\"\nfunc TestIntegrationEvidence(t *testing.T) { if Value != 2 { t.Fatal(Value) } }\n",
		}
		for name, raw := range files {
			writeText(t, filepath.Join(root, name), raw, 0600)
		}
		for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=Test Evidence Qualification", "-c", "user.email=qualification@example.test", "commit", "-qm", "baseline"}} {
			nativeQualificationGit(t, root, args...)
		}
		definitions := agenttools.TestDefinitions(role.Bundle.Permissions)
		names := []string{}
		selected := []agentruntime.ToolDefinition{}
		for _, d := range definitions {
			if slices.Contains([]string{"run_go_tests", "run_go_tests_worktree"}, d.Name) {
				selected = append(selected, d)
				names = append(names, d.Name)
			}
		}
		definitions = append(selected, agentruntime.ToolDefinition{Name: "submit_result", Description: "Finish only after all requested actual tests succeeded.", Parameters: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["outcome"],"properties":{"outcome":{"type":"string","enum":["completed"]}}}`)})
		contract, err := agentruntime.NewStructuredTurnContract(definitions, "submit_result")
		if err != nil {
			t.Fatal(err)
		}
		id, err := ids.Next()
		if err != nil {
			t.Fatal(err)
		}
		request := digestBytes([]byte(string(id) + ":" + string(surface)))
		purpose := kernel.PurposeValidation
		if slices.Contains(names, "run_go_tests_worktree") {
			purpose = kernel.PurposeImplementation
		}
		binding := nativeReleaseBinding{authority: agenttools.Authority{WorkspaceRoot: root, Permissions: role.Bundle.Permissions, Purpose: purpose, EffectPolicyDigest: p.EffectPolicyDigest}}
		tests := agenttools.TestTurnAdapter{Gateway: agenttools.TestGateway{Bindings: binding, Host: agenttools.Host{Timeout: 2 * time.Minute, GoBinary: config.Native.GoBinary}, Ledger: store, Policy: p.EffectPolicyDigest}, InvocationID: id, RequestDigest: request}
		journal := &nativeReleaseJournal{path: filepath.Join(output, string(p.RoleFQRN)+"-journal.json")}
		settings := p.NativeSettings
		runner := agentruntime.Runner{Journal: journal, Effects: tests, Model: agentruntime.OpenAIModel{Client: &http.Client{Timeout: 10 * time.Minute, Transport: &nativeHTTPCapture{directory: output, scenario: string(p.RoleFQRN)}}, BaseURL: settings.BaseURL, Model: settings.Model, MaxTokens: settings.MaxOutputTokens, Reasoning: settings.Reasoning, Tools: definitions, FinalTool: "submit_result", FinalSchema: definitions[len(definitions)-1].Parameters, TurnContract: contract}, FinalTool: "submit_result", Finalize: func(raw json.RawMessage) (string, error) { return string(raw), nil }, SystemPrompt: settings.ModelInstructions + "\n" + agentruntime.StructuredTurnInstruction, MaxTurns: settings.MaxTurns}
		prompt := "This is a bounded test-tool qualification, not feature work. For EACH of the exposed test tools " + strings.Join(names, ", ") + ", call it exactly twice in order: first with package . and no tags; second with package . and tags [mongo_integration]. The first should provide PASS for TestDefaultEvidence and excluded NOT_RUN for TestIntegrationEvidence. The second must provide actual PASS events for both tests. Package ok alone is not tagged coverage. Do not change sources or do other work. Finish with submit_result outcome completed only after all actual calls succeeded."
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
		started := time.Now().UTC()
		t.Logf("test-evidence delta role=%s invocation=%s tools=%v", p.RoleFQRN, id, names)
		result, err := runner.Run(ctx, string(id), string(request), prompt)
		finished := time.Now().UTC()
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		counts := map[string]int{}
		for _, entry := range journal.entries {
			if entry.Kind == agentruntime.ModelRejected {
				t.Fatal("schema correction", string(entry.Payload))
			}
			if entry.Kind != agentruntime.ToolDone {
				continue
			}
			var done agentruntime.ToolResult
			if json.Unmarshal(entry.Payload, &done) != nil || done.Error != "" || !slices.Contains(names, done.Name) {
				t.Fatal("failed or unexpected tool", string(entry.Payload))
			}
			var got agenttools.Result
			if json.Unmarshal(done.Output, &got) != nil || got.ExecutionError != "" || got.ExitCode != 0 || got.PermissionDenial || got.TestEvidence == nil || !got.TestEvidence.Complete {
				t.Fatal("missing successful test evidence", string(done.Output))
			}
			tagged := counts[done.Name] == 1
			if counts[done.Name] > 1 || tagged && !slices.Equal(got.TestEvidence.Tags, []string{"mongo_integration"}) || !tagged && len(got.TestEvidence.Tags) != 0 {
				t.Fatal("wrong test tag sequence")
			}
			if !slices.Contains(got.TestEvidence.Tests, agenttools.GoTestCase{Package: "example.test/testevidence", Name: "TestDefaultEvidence", Status: "PASS"}) {
				t.Fatal("default test did not pass")
			}
			integration := agenttools.GoTestCase{Package: "example.test/testevidence", Name: "TestIntegrationEvidence", Status: "PASS"}
			if tagged {
				if !slices.Contains(got.TestEvidence.Tests, integration) || len(got.TestEvidence.Excluded) != 0 {
					t.Fatal("tagged test did not execute")
				}
			} else {
				integration.Status = "NOT_RUN"
				if !slices.Contains(got.TestEvidence.Excluded, integration) || len(got.TestEvidence.Tests) != 1 {
					t.Fatal("excluded test falsely reported as executed")
				}
			}
			for _, test := range got.TestEvidence.Tests {
				if test.Status != "PASS" {
					t.Fatal("nonpassing test", test)
				}
			}
			counts[done.Name]++
		}
		for _, name := range names {
			if counts[name] != 2 {
				t.Fatal("missing exact test calls", counts)
			}
		}
		var completion map[string]string
		if json.Unmarshal([]byte(result), &completion) != nil || len(completion) != 1 || completion["outcome"] != "completed" || len(journal.entries) == 0 || journal.entries[len(journal.entries)-1].Kind != agentruntime.Finished {
			t.Fatal("incomplete result")
		}
		for name, before := range files {
			got, err := os.ReadFile(filepath.Join(root, name))
			if err != nil || string(got) != before {
				t.Fatal("fixture source changed")
			}
		}
		if status := strings.TrimSpace(nativeQualificationGit(t, root, "status", "--porcelain")); status != "" {
			t.Fatal("fixture Git state changed", status)
		}
		write(string(p.RoleFQRN)+"-measurement.json", map[string]any{"invocation_id": id, "model_profile_digest": p.ModelProfileDigest, "prior_tool_surface_digest": p.ToolPolicyDigest, "tool_surface_digest": surface, "started_at": started, "finished_at": finished, "tool_counts": counts, "result": json.RawMessage(result), "files_unchanged": true, "qualification_scope": "exact test tools tag/evidence delta; prior signed-role/model/work-kind evidence retained"})
		bindNamedToolDeltaQualification(t, p, surface, id, "native-tool-delta-tagged-test-evidence", ids)
		checked++
	}
	if checked == 0 {
		t.Fatal("no changed test role was exercised")
	}
	if err := ValidateOperationalProfileQualifications(config.Profiles, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	write("tekrood.native-candidate.json", config)
	if _, err := LoadProductionConfig(filepath.Join(output, "tekrood.native-candidate.json")); err != nil {
		t.Fatal(err)
	}
}
